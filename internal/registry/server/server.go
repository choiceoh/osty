package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/osty/osty/internal/manifest"
	"github.com/osty/osty/internal/registry"
)

// DefaultMaxTarballBytes caps the size of a publish body. 16 MiB is
// generous for a language still specifying its stdlib and comfortably
// below Go's default server memory footprint.
const DefaultMaxTarballBytes = 16 << 20

// Config wires the ingredients of a Server together. Zero value is
// not usable; use New.
type Config struct {
	Storage *Storage
	Tokens  *TokenDB

	// MaxTarballBytes bounds the accepted upload size. Zero picks
	// DefaultMaxTarballBytes.
	MaxTarballBytes int64

	// Logger receives one line per request. nil uses log.Default().
	Logger *log.Logger
}

// Server is the HTTP handler. It implements http.Handler; mount it
// at "/" of any ServeMux or serve it directly with http.ListenAndServe.
type Server struct {
	cfg Config
	mux *http.ServeMux
	log *log.Logger
	max int64
}

// New returns a Server configured with cfg. It panics if Storage or
// Tokens are nil — those are programming errors, not runtime
// failures worth surfacing as an error path.
func New(cfg Config) *Server {
	if cfg.Storage == nil {
		panic("registry/server: Storage is nil")
	}
	if cfg.Tokens == nil {
		panic("registry/server: Tokens is nil")
	}
	s := &Server{
		cfg: cfg,
		mux: http.NewServeMux(),
		log: cfg.Logger,
		max: cfg.MaxTarballBytes,
	}
	if s.log == nil {
		s.log = log.Default()
	}
	if s.max == 0 {
		s.max = DefaultMaxTarballBytes
	}
	// Only one pattern is registered at the mux — everything under
	// /v1/ is routed inside handleV1 so we can do custom method-based
	// dispatch without juggling six StripPrefix wrappers.
	s.mux.HandleFunc("/v1/crates/", s.handleV1)
	s.mux.HandleFunc("/v1/crates", s.handleV1)
	s.mux.HandleFunc("/health", s.handleHealth)
	s.mux.HandleFunc("/", s.handleRoot)
	return s
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// handleHealth is a cheap liveness probe for load balancers.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, "ok\n")
}

// handleRoot is the human-facing landing page. It lists packages so
// an operator can eyeball what's published; GETs under /v1/ are
// routed elsewhere.
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	names, err := s.cfg.Storage.List()
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "list packages: %v", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"registry": "osty",
		"version":  "v1",
		"packages": names,
	})
}

// handleV1 dispatches every /v1/crates/* path based on the number of
// segments after "crates":
//
//	/v1/crates/<name>           → GET: index
//	/v1/crates/<name>/<ver>     → PUT: publish
//	/v1/crates/<name>/<ver>/tar → GET: download
func (s *Server) handleV1(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/v1/crates")
	rest = strings.TrimPrefix(rest, "/")
	if rest == "" {
		// Reserved for a future package-listing endpoint; for now 404
		// so clients that stumble here get a clear signal.
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(rest, "/")
	// Percent-decode each path segment — Go's mux leaves them raw.
	for i := range parts {
		if decoded, err := url.PathUnescape(parts[i]); err == nil {
			parts[i] = decoded
		}
	}
	switch len(parts) {
	case 1:
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.handleIndex(w, r, parts[0])
	case 2:
		if r.Method != http.MethodPut {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.handlePublish(w, r, parts[0], parts[1])
	case 3:
		if parts[2] != "tar" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.handleDownload(w, r, parts[0], parts[1])
	default:
		http.NotFound(w, r)
	}
}

// handleIndex serves GET /v1/crates/<name>.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request, name string) {
	idx, err := s.cfg.Storage.Index(name)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			s.writeError(w, http.StatusNotFound, "package %q not found", name)
			return
		}
		s.writeError(w, http.StatusBadRequest, "%v", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(idx)
	s.log.Printf("GET  /v1/crates/%s → 200 (%d versions)", name, len(idx.Versions))
}

// handleDownload serves GET /v1/crates/<name>/<version>/tar.
func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request, name, version string) {
	rc, err := s.cfg.Storage.OpenTarball(name, version)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			s.writeError(w, http.StatusNotFound, "%s@%s not found", name, version)
			return
		}
		s.writeError(w, http.StatusBadRequest, "%v", err)
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", "application/x-tar+gzip")
	// The client checks the sha256 against the registry-advertised
	// hash; surfacing the checksum here gives operators a sanity
	// check without re-hashing.
	if idx, err := s.cfg.Storage.Index(name); err == nil {
		for _, v := range idx.Versions {
			if v.Version == version {
				w.Header().Set("Osty-Checksum", v.Checksum)
				break
			}
		}
	}
	n, _ := io.Copy(w, rc)
	s.log.Printf("GET  /v1/crates/%s/%s/tar → 200 (%d bytes)", name, version, n)
}

// handlePublish serves PUT /v1/crates/<name>/<version>. The flow:
//
//  1. Verify Authorization: Bearer token, scope `publish:<name>`.
//  2. Read the whole body into memory (bounded by MaxTarballBytes)
//     and sha256 it.
//  3. If the client sent Osty-Checksum, verify it matches; if not,
//     use our own computed checksum as authoritative.
//  4. Extract osty.toml from inside the tarball to harvest the
//     declared dependencies into the index entry.
//  5. Storage.Publish writes the blob + rewrites the index.
func (s *Server) handlePublish(w http.ResponseWriter, r *http.Request, name, version string) {
	token := parseBearer(r.Header.Get("Authorization"))
	if err := s.cfg.Tokens.Authorize(token, "publish:"+name); err != nil {
		if errors.Is(err, ErrForbidden) {
			s.writeError(w, http.StatusForbidden, "token is not authorized to publish %q", name)
			return
		}
		s.writeError(w, http.StatusUnauthorized, "missing or invalid token")
		return
	}
	if err := ValidateName(name); err != nil {
		s.writeError(w, http.StatusBadRequest, "%v", err)
		return
	}
	if err := ValidateVersion(version); err != nil {
		s.writeError(w, http.StatusBadRequest, "%v", err)
		return
	}

	// Bound the upload size. http.MaxBytesReader converts overrun
	// into a specific error at Read time.
	r.Body = http.MaxBytesReader(w, r.Body, s.max)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.writeError(w, http.StatusRequestEntityTooLarge,
			"upload exceeds %d bytes or read failed: %v", s.max, err)
		return
	}
	if len(body) == 0 {
		s.writeError(w, http.StatusBadRequest, "empty body")
		return
	}

	sum := sha256.Sum256(body)
	computed := "sha256:" + hex.EncodeToString(sum[:])
	if declared := r.Header.Get("Osty-Checksum"); declared != "" && declared != computed {
		s.writeError(w, http.StatusBadRequest,
			"Osty-Checksum mismatch: header=%s computed=%s", declared, computed)
		return
	}

	// Parse the Osty-Metadata header (URL-encoded JSON, as the client
	// serializes it in registry.Client.Publish).
	var meta registry.PublishMetadata
	if raw := r.Header.Get("Osty-Metadata"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &meta); err != nil {
			s.writeError(w, http.StatusBadRequest, "invalid Osty-Metadata: %v", err)
			return
		}
	}

	// Peek inside the tarball for osty.toml so the index carries the
	// package's declared deps + declared version (sanity check).
	pkgName, pkgVersion, deps, err := extractManifestFromTarball(body)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "reading osty.toml from tarball: %v", err)
		return
	}
	if pkgName != "" && pkgName != name {
		s.writeError(w, http.StatusBadRequest,
			"manifest package.name %q does not match URL name %q", pkgName, name)
		return
	}
	if pkgVersion != "" && pkgVersion != version {
		s.writeError(w, http.StatusBadRequest,
			"manifest package.version %q does not match URL version %q", pkgVersion, version)
		return
	}

	if err := s.cfg.Storage.Publish(PublishInput{
		Name:         name,
		Version:      version,
		Checksum:     computed,
		Tarball:      body,
		Metadata:     meta,
		Dependencies: deps,
	}); err != nil {
		if errors.Is(err, ErrVersionExists) {
			s.writeError(w, http.StatusConflict, "%s@%s already published", name, version)
			return
		}
		s.writeError(w, http.StatusInternalServerError, "publish: %v", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"name":     name,
		"version":  version,
		"checksum": computed,
	})
	s.log.Printf("PUT  /v1/crates/%s/%s → 201 (%d bytes, %s)", name, version, len(body), shortSum(computed))
}

// writeError sends a plaintext error body. The HTTP client surfaces
// this body verbatim to the user, so keep the message terse and
// actionable.
func (s *Server) writeError(w http.ResponseWriter, code int, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	http.Error(w, msg, code)
}

// extractManifestFromTarball reads osty.toml out of a gzipped tar
// archive without writing to disk. Returns the declared package
// name, version, and dependency list. Missing osty.toml is not an
// error — the registry is tolerant of archives produced by older
// tooling, and deps just stay empty in the index.
func extractManifestFromTarball(body []byte) (name, version string, deps []registry.VersionDependency, err error) {
	gz, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return "", "", nil, fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return "", "", nil, nil
		}
		if err != nil {
			return "", "", nil, err
		}
		// Tarballs produced by pkgmgr.CreateTarGz put files at the
		// archive root (no wrapping directory), so both "osty.toml"
		// and "<pkg>/osty.toml" shapes are accepted.
		if path.Base(hdr.Name) != manifest.ManifestFile {
			continue
		}
		// Only accept a top-level or once-nested osty.toml. Deeper
		// paths likely belong to a vendored dep that slipped into the
		// archive and shouldn't drive the registry's index.
		depth := strings.Count(strings.Trim(hdr.Name, "/"), "/")
		if depth > 1 {
			continue
		}
		const maxManifestBytes = 1 << 20
		buf, err := io.ReadAll(io.LimitReader(tr, maxManifestBytes))
		if err != nil {
			return "", "", nil, err
		}
		m, perr := manifest.Parse(buf)
		if perr != nil {
			return "", "", nil, fmt.Errorf("parse osty.toml: %w", perr)
		}
		for _, d := range m.Dependencies {
			if d.VersionReq == "" {
				// path/git deps are skipped in the public index —
				// they only make sense in the publishing workspace.
				continue
			}
			deps = append(deps, registry.VersionDependency{
				Name: d.Name,
				Req:  d.VersionReq,
				Kind: "normal",
			})
		}
		for _, d := range m.DevDependencies {
			if d.VersionReq == "" {
				continue
			}
			deps = append(deps, registry.VersionDependency{
				Name: d.Name,
				Req:  d.VersionReq,
				Kind: "dev",
			})
		}
		if m.HasPackage {
			return m.Package.Name, m.Package.Version, deps, nil
		}
		return "", "", deps, nil
	}
}

// shortSum renders the first 10 hex chars of a "sha256:<hex>" string
// for log lines.
func shortSum(sum string) string {
	const prefix = "sha256:"
	if strings.HasPrefix(sum, prefix) {
		sum = sum[len(prefix):]
	}
	if len(sum) > 10 {
		sum = sum[:10]
	}
	return sum
}
