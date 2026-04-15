package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
)

// Server is the HTTP handler implementing the registry wire protocol
// (see client.go for the client side). It is safe to wrap in
// httptest.NewServer or to pass to http.ListenAndServe directly:
//
//	store, _ := registry.NewStorage("/var/lib/osty-registry")
//	srv := &registry.Server{Storage: store, Auth: registry.AllowAll{}}
//	http.ListenAndServe(":8080", srv)
//
// Routes:
//
//	GET  /v1/crates/<name>           → IndexEntry JSON (404 if unpublished)
//	GET  /v1/crates/<name>/<ver>/tar → tarball bytes
//	PUT  /v1/crates/<name>/<ver>     → publish (auth required)
//	POST /v1/crates/<name>/<ver>/yank   → mark yanked   (auth required)
//	POST /v1/crates/<name>/<ver>/unyank → clear yanked  (auth required)
//	GET  /v1/healthz                 → "ok"
//
// Unknown methods return 405; unknown paths under /v1 return 404.
type Server struct {
	// Storage persists the index and tarballs. Required.
	Storage *Storage

	// Auth decides whether a bearer token may mutate a package.
	// When nil, every mutating request is rejected.
	Auth Authorizer

	// MaxTarballBytes caps upload sizes. 0 means 32 MiB (default),
	// negative disables the limit. Publish requests with a larger
	// body return 413 Payload Too Large.
	MaxTarballBytes int64

	// Logger receives one line per request for operator visibility.
	// Nil logs to the standard logger.
	Logger *log.Logger
}

// defaultMaxTarball matches what most source distributions fit into
// comfortably; configurable per-deployment.
const defaultMaxTarball = 32 << 20 // 32 MiB

// ServeHTTP dispatches requests to the right handler. Keeping the
// routing inline (vs. pulling in a router) avoids a dependency; the
// path space is small enough that hand-written matching is clearer.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// /v1/healthz is dirt-simple: used by load balancers and by the
	// integration tests to confirm the server is up.
	if r.URL.Path == "/v1/healthz" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.WriteString(w, "ok")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/v1/crates/")
	if path == r.URL.Path {
		// Path didn't start with /v1/crates/.
		http.NotFound(w, r)
		return
	}
	segs := splitPath(path)
	switch len(segs) {
	case 1:
		// /v1/crates/<name>
		s.handleIndex(w, r, segs[0])
	case 3:
		// /v1/crates/<name>/<version>/<verb>
		s.handleVersion(w, r, segs[0], segs[1], segs[2])
	case 2:
		// /v1/crates/<name>/<version>   (publish)
		s.handlePublish(w, r, segs[0], segs[1])
	default:
		http.NotFound(w, r)
	}
}

func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	idx, err := s.Storage.ReadIndex(name)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, fmt.Sprintf("package %q not found", name), http.StatusNotFound)
			return
		}
		s.logf("index %s: %v", name, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(idx)
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request, name, version, verb string) {
	switch verb {
	case "tar":
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.serveTarball(w, r, name, version)
	case "yank", "unyank":
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := s.authorizeRequest(r, name); err != nil {
			s.writeAuthError(w, err)
			return
		}
		if err := s.Storage.Yank(name, version, verb == "yank"); err != nil {
			if os.IsNotExist(err) {
				http.Error(w, "version not found", http.StatusNotFound)
				return
			}
			s.logf("yank %s@%s: %v", name, version, err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) serveTarball(w http.ResponseWriter, r *http.Request, name, version string) {
	f, err := s.Storage.OpenTarball(name, version)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "tarball not found", http.StatusNotFound)
			return
		}
		s.logf("tarball %s@%s: %v", name, version, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/x-tar+gzip")
	if info, err := f.Stat(); err == nil {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
	}
	_, _ = io.Copy(w, f)
}

func (s *Server) handlePublish(w http.ResponseWriter, r *http.Request, name, version string) {
	if r.Method != http.MethodPut {
		w.Header().Set("Allow", "PUT")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := s.authorizeRequest(r, name); err != nil {
		s.writeAuthError(w, err)
		return
	}
	limit := s.MaxTarballBytes
	if limit == 0 {
		limit = defaultMaxTarball
	}
	var body io.Reader = r.Body
	if limit > 0 {
		body = http.MaxBytesReader(w, r.Body, limit)
	}
	entry := PublishEntry{
		Name:     name,
		Version:  version,
		Checksum: r.Header.Get("Osty-Checksum"),
	}
	// Parse the Osty-Metadata header if present. A missing header is
	// fine (metadata is optional for listing pages); malformed JSON
	// is a client error.
	if meta := r.Header.Get("Osty-Metadata"); meta != "" {
		if err := json.Unmarshal([]byte(meta), &entry.Metadata); err != nil {
			http.Error(w, "invalid Osty-Metadata header: "+err.Error(),
				http.StatusBadRequest)
			return
		}
	}
	if err := s.Storage.Publish(entry, body); err != nil {
		switch {
		case errors.Is(err, ErrVersionExists):
			http.Error(w, err.Error(), http.StatusConflict)
		case errors.Is(err, ErrChecksumMismatch):
			http.Error(w, err.Error(), http.StatusBadRequest)
		default:
			// MaxBytesReader surfaces as a generic error; translate it
			// so clients see a meaningful status.
			if strings.Contains(err.Error(), "http: request body too large") {
				http.Error(w, "tarball exceeds server limit", http.StatusRequestEntityTooLarge)
				return
			}
			s.logf("publish %s@%s: %v", name, version, err)
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	s.logf("published %s@%s", name, version)
	w.WriteHeader(http.StatusCreated)
	_, _ = io.WriteString(w, "ok\n")
}

// authorizeRequest pulls the bearer token off the request and asks
// the configured Authorizer whether it may act on pkg.
func (s *Server) authorizeRequest(r *http.Request, pkg string) error {
	if s.Auth == nil {
		return ErrUnauthorized
	}
	token := ""
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		token = strings.TrimPrefix(h, "Bearer ")
	}
	return s.Auth.Authorize(token, pkg)
}

// writeAuthError translates the sentinel error set returned by
// Authorizer implementations into HTTP status codes. Kept as one
// function so every route handles auth consistently.
func (s *Server) writeAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrForbidden):
		http.Error(w, err.Error(), http.StatusForbidden)
	default:
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}
}

func (s *Server) logf(format string, args ...any) {
	if s.Logger != nil {
		s.Logger.Printf(format, args...)
		return
	}
	log.Printf("registry: "+format, args...)
}
