package registry

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/osty/osty/internal/manifest"
	"github.com/osty/osty/internal/pkgmgr/semver"
)

// DefaultMaxUploadBytes is the default upper bound for one published
// tarball. Servers can raise it for large private registries.
const DefaultMaxUploadBytes int64 = 64 << 20

const checksumPrefix = "sha256:"

var (
	errPackageNotFound = errors.New("registry package not found")
	errVersionNotFound = errors.New("registry package version not found")
	errVersionExists   = errors.New("registry package version already exists")
)

// AuthorizeFunc decides whether a mutating request is allowed. The
// action is one of "publish", "yank", or "unyank".
type AuthorizeFunc func(r *http.Request, action, name, version string) bool

// BearerTokenAuth returns an AuthorizeFunc that accepts exactly one
// bearer token. Empty tokens reject every mutating request.
func BearerTokenAuth(token string) AuthorizeFunc {
	token = strings.TrimSpace(token)
	return func(r *http.Request, action, name, version string) bool {
		got := strings.TrimSpace(r.Header.Get("Authorization"))
		if token == "" || !strings.HasPrefix(got, "Bearer ") {
			return false
		}
		return strings.TrimSpace(strings.TrimPrefix(got, "Bearer ")) == token
	}
}

// Server exposes the osty registry HTTP protocol backed by a FileStore.
// The zero-value is not usable; construct it with NewServer.
type Server struct {
	Store *FileStore

	// Authorize is consulted for publish/yank/unyank. Nil means writes
	// are accepted, which is useful for httptest and deliberately local
	// development servers.
	Authorize AuthorizeFunc

	// Now supplies PublishedAt timestamps. Defaults to time.Now().UTC.
	Now func() time.Time

	// MaxUploadBytes caps PUT /v1/crates/<name>/<version> bodies. Zero
	// selects DefaultMaxUploadBytes.
	MaxUploadBytes int64
}

// NewServer constructs a registry HTTP handler over store.
func NewServer(store *FileStore) *Server {
	return &Server{Store: store}
}

// ServeHTTP routes the wire protocol used by Client.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.Store == nil {
		http.Error(w, "registry server has no store configured", http.StatusInternalServerError)
		return
	}
	segments, err := pathSegments(r.URL.EscapedPath())
	if err != nil {
		writeRegistryError(w, err)
		return
	}
	if len(segments) < 2 || segments[0] != "v1" || segments[1] != "crates" {
		http.NotFound(w, r)
		return
	}

	switch {
	case r.Method == http.MethodGet && len(segments) == 2:
		s.handleSearch(w, r)
	case r.Method == http.MethodGet && len(segments) == 3:
		s.handleIndex(w, r, segments[2])
	case r.Method == http.MethodPut && len(segments) == 4:
		s.handlePublish(w, r, segments[2], segments[3])
	case r.Method == http.MethodGet && len(segments) == 5 && segments[4] == "tar":
		s.handleTarball(w, r, segments[2], segments[3])
	case r.Method == http.MethodDelete && len(segments) == 5 && segments[4] == "yank":
		s.handleYank(w, r, segments[2], segments[3], true)
	case r.Method == http.MethodPut && len(segments) == 5 && segments[4] == "unyank":
		s.handleYank(w, r, segments[2], segments[3], false)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request, name string) {
	if err := validatePackageName(name); err != nil {
		writeRegistryError(w, badRequest("%v", err))
		return
	}
	entry, err := s.Store.Index(name)
	if err != nil {
		writeRegistryError(w, err)
		return
	}
	data, etag, err := jsonWithETag(entry)
	if err != nil {
		writeRegistryError(w, err)
		return
	}
	w.Header().Set("ETag", etag)
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	writeJSONBytes(w, http.StatusOK, data)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if strings.TrimSpace(q) == "" {
		writeRegistryError(w, badRequest("registry search query is empty"))
		return
	}
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeRegistryError(w, badRequest("invalid search limit %q", raw))
			return
		}
		limit = n
	}
	results, err := s.Store.Search(q, limit)
	if err != nil {
		writeRegistryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, results)
}

func (s *Server) handlePublish(w http.ResponseWriter, r *http.Request, name, version string) {
	if err := validatePackageName(name); err != nil {
		writeRegistryError(w, badRequest("%v", err))
		return
	}
	if err := validateVersionString(version); err != nil {
		writeRegistryError(w, badRequest("%v", err))
		return
	}
	if !s.authorized(r, "publish", name, version) {
		http.Error(w, "registry publish requires a valid bearer token", http.StatusUnauthorized)
		return
	}
	if ct := strings.TrimSpace(r.Header.Get("Content-Type")); ct != "" {
		base := strings.ToLower(strings.TrimSpace(strings.Split(ct, ";")[0]))
		if base != "application/x-tar+gzip" {
			writeRegistryError(w, badRequest("unsupported content type %q", ct))
			return
		}
	}
	var meta PublishMetadata
	if raw := strings.TrimSpace(r.Header.Get("Osty-Metadata")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &meta); err != nil {
			writeRegistryError(w, badRequest("invalid Osty-Metadata header: %v", err))
			return
		}
	}

	tmpPath := ""
	defer func() {
		if tmpPath != "" {
			_ = os.Remove(tmpPath)
		}
	}()
	var gotChecksum string
	var err error
	tmpPath, gotChecksum, err = s.writeUploadTemp(r)
	if err != nil {
		writeRegistryError(w, err)
		return
	}
	wantChecksum := strings.TrimSpace(r.Header.Get("Osty-Checksum"))
	if wantChecksum != "" {
		if !strings.HasPrefix(wantChecksum, checksumPrefix) {
			writeRegistryError(w, badRequest("unsupported checksum %q", wantChecksum))
			return
		}
		if wantChecksum != gotChecksum {
			writeRegistryError(w, badRequest("checksum mismatch: want %s, got %s", wantChecksum, gotChecksum))
			return
		}
	}

	m, err := manifestFromTarball(tmpPath)
	if err != nil {
		writeRegistryError(w, badRequest("invalid package tarball: %v", err))
		return
	}
	if !m.HasPackage {
		writeRegistryError(w, badRequest("package tarball has no [package] table"))
		return
	}
	if m.Package.Name != name || m.Package.Version != version {
		writeRegistryError(w, badRequest(
			"package tarball identity mismatch: URL has %s %s, manifest has %s %s",
			name, version, m.Package.Name, m.Package.Version))
		return
	}
	deps, err := versionDependencies(m)
	if err != nil {
		writeRegistryError(w, badRequest("%v", err))
		return
	}
	stored := StoredVersion{
		Version:      version,
		Checksum:     gotChecksum,
		PublishedAt:  s.now(),
		Dependencies: deps,
		Features:     copyFeatures(m),
		Metadata:     meta,
	}
	if err := s.Store.Publish(name, stored, tmpPath); err != nil {
		writeRegistryError(w, err)
		return
	}
	tmpPath = ""
	writeJSON(w, http.StatusCreated, map[string]bool{"ok": true})
}

func (s *Server) handleTarball(w http.ResponseWriter, r *http.Request, name, version string) {
	if err := validatePackageName(name); err != nil {
		writeRegistryError(w, badRequest("%v", err))
		return
	}
	if err := validateVersionString(version); err != nil {
		writeRegistryError(w, badRequest("%v", err))
		return
	}
	f, err := s.Store.OpenTarball(name, version)
	if err != nil {
		writeRegistryError(w, err)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/x-tar+gzip")
	if st, err := f.Stat(); err == nil {
		w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
	}
	_, _ = io.Copy(w, f)
}

func (s *Server) handleYank(w http.ResponseWriter, r *http.Request, name, version string, yanked bool) {
	if err := validatePackageName(name); err != nil {
		writeRegistryError(w, badRequest("%v", err))
		return
	}
	if err := validateVersionString(version); err != nil {
		writeRegistryError(w, badRequest("%v", err))
		return
	}
	action := "unyank"
	if yanked {
		action = "yank"
	}
	if !s.authorized(r, action, name, version) {
		http.Error(w, "registry mutation requires a valid bearer token", http.StatusUnauthorized)
		return
	}
	if err := s.Store.SetYanked(name, version, yanked); err != nil {
		writeRegistryError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) authorized(r *http.Request, action, name, version string) bool {
	return s.Authorize == nil || s.Authorize(r, action, name, version)
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Server) maxUploadBytes() int64 {
	if s.MaxUploadBytes > 0 {
		return s.MaxUploadBytes
	}
	return DefaultMaxUploadBytes
}

func (s *Server) writeUploadTemp(r *http.Request) (string, string, error) {
	tmp, err := s.Store.createUploadTemp()
	if err != nil {
		return "", "", err
	}
	tmpPath := tmp.Name()
	defer tmp.Close()

	h := sha256.New()
	limit := s.maxUploadBytes()
	n, err := io.Copy(tmp, io.TeeReader(io.LimitReader(r.Body, limit+1), h))
	if err != nil {
		_ = os.Remove(tmpPath)
		return "", "", err
	}
	if n > limit {
		_ = os.Remove(tmpPath)
		return "", "", statusError{status: http.StatusRequestEntityTooLarge, msg: fmt.Sprintf("package upload exceeds %d bytes", limit)}
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", "", err
	}
	return tmpPath, checksumPrefix + hex.EncodeToString(h.Sum(nil)), nil
}

// StoredVersion is one persisted version record in the file-backed
// registry. It is intentionally a superset of Version: the public
// index hides Metadata, while search uses it for useful listings.
type StoredVersion struct {
	Version      string              `json:"version"`
	Checksum     string              `json:"checksum"`
	PublishedAt  time.Time           `json:"published_at"`
	Yanked       bool                `json:"yanked"`
	Dependencies []VersionDependency `json:"dependencies"`
	Features     map[string][]string `json:"features,omitempty"`
	Metadata     PublishMetadata     `json:"metadata,omitempty"`
	Downloads    int64               `json:"downloads,omitempty"`
}

func (v StoredVersion) indexVersion() Version {
	return Version{
		Version:      v.Version,
		Checksum:     v.Checksum,
		PublishedAt:  v.PublishedAt,
		Yanked:       v.Yanked,
		Dependencies: append([]VersionDependency(nil), v.Dependencies...),
		Features:     copyStringSliceMap(v.Features),
	}
}

// PackageRecord is the on-disk package metadata file.
type PackageRecord struct {
	Name     string          `json:"name"`
	Versions []StoredVersion `json:"versions"`
}

func (r *PackageRecord) indexEntry() *IndexEntry {
	out := &IndexEntry{Name: r.Name}
	for _, v := range r.Versions {
		out.Versions = append(out.Versions, v.indexVersion())
	}
	return out
}

// FileStore is a small real registry backend rooted on disk.
//
// Layout:
//
//	<Root>/packages/<name>/package.json
//	<Root>/packages/<name>/<version>.tgz
//	<Root>/tmp/upload-*.tgz
type FileStore struct {
	Root string
	mu   sync.Mutex
}

// NewFileStore returns a file-backed registry store rooted at root.
func NewFileStore(root string) *FileStore {
	return &FileStore{Root: root}
}

// Index returns the public index entry for name.
func (fs *FileStore) Index(name string) (*IndexEntry, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	rec, err := fs.loadRecordLocked(name)
	if err != nil {
		return nil, err
	}
	return rec.indexEntry(), nil
}

// Publish persists one uploaded tarball and adds its metadata to the
// package index. Duplicate (name, version) publishes are rejected.
func (fs *FileStore) Publish(name string, version StoredVersion, uploadPath string) error {
	if err := validatePackageName(name); err != nil {
		return err
	}
	if err := validateVersionString(version.Version); err != nil {
		return err
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()

	rec, err := fs.loadRecordLocked(name)
	if errors.Is(err, errPackageNotFound) {
		rec = &PackageRecord{Name: name}
	} else if err != nil {
		return err
	}
	for _, v := range rec.Versions {
		if v.Version == version.Version {
			return errVersionExists
		}
	}
	if err := os.MkdirAll(fs.packageDir(name), 0o755); err != nil {
		return err
	}
	dst := fs.tarballPath(name, version.Version)
	if _, err := os.Stat(dst); err == nil {
		return errVersionExists
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(uploadPath, dst); err != nil {
		return err
	}
	rec.Versions = append(rec.Versions, version)
	if err := fs.saveRecordLocked(rec); err != nil {
		_ = os.Remove(dst)
		return err
	}
	return nil
}

// OpenTarball opens the published tarball and increments the persisted
// download counter. Yanked versions remain downloadable.
func (fs *FileStore) OpenTarball(name, version string) (*os.File, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	rec, err := fs.loadRecordLocked(name)
	if err != nil {
		return nil, err
	}
	idx := -1
	for i, v := range rec.Versions {
		if v.Version == version {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, errVersionNotFound
	}
	f, err := os.Open(fs.tarballPath(name, version))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errVersionNotFound
		}
		return nil, err
	}
	rec.Versions[idx].Downloads++
	if err := fs.saveRecordLocked(rec); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

// SetYanked toggles a version's yanked state.
func (fs *FileStore) SetYanked(name, version string, yanked bool) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	rec, err := fs.loadRecordLocked(name)
	if err != nil {
		return err
	}
	for i, v := range rec.Versions {
		if v.Version == version {
			rec.Versions[i].Yanked = yanked
			return fs.saveRecordLocked(rec)
		}
	}
	return errVersionNotFound
}

// Search performs a small case-insensitive name/metadata search.
// limit <= 0 selects the server default page size.
func (fs *FileStore) Search(query string, limit int) (*SearchResults, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil, fmt.Errorf("registry search query is empty")
	}
	if limit <= 0 {
		limit = 20
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	records, err := fs.loadAllRecordsLocked()
	if err != nil {
		return nil, err
	}
	type scored struct {
		hit   SearchHit
		score int
	}
	var hits []scored
	for _, rec := range records {
		latest, ok := latestActiveVersion(rec)
		if !ok {
			continue
		}
		score, ok := searchScore(rec.Name, latest.Metadata, q)
		if !ok {
			continue
		}
		hits = append(hits, scored{
			score: score,
			hit: SearchHit{
				Name:          rec.Name,
				LatestVersion: latest.Version,
				Description:   latest.Metadata.Description,
				Downloads:     totalDownloads(rec),
			},
		})
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score < hits[j].score
		}
		return hits[i].hit.Name < hits[j].hit.Name
	})
	out := &SearchResults{Total: len(hits)}
	for i, h := range hits {
		if i >= limit {
			break
		}
		out.Hits = append(out.Hits, h.hit)
	}
	return out, nil
}

func (fs *FileStore) createUploadTemp() (*os.File, error) {
	if fs == nil || strings.TrimSpace(fs.Root) == "" {
		return nil, fmt.Errorf("registry store root is empty")
	}
	dir := filepath.Join(fs.Root, "tmp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return os.CreateTemp(dir, "upload-*.tgz")
}

func (fs *FileStore) loadAllRecordsLocked() ([]*PackageRecord, error) {
	root := filepath.Join(fs.Root, "packages")
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []*PackageRecord
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, ent.Name(), "package.json"))
		if err != nil {
			return nil, err
		}
		var rec PackageRecord
		if err := json.Unmarshal(data, &rec); err != nil {
			return nil, fmt.Errorf("decode package record %s: %w", ent.Name(), err)
		}
		out = append(out, &rec)
	}
	return out, nil
}

func (fs *FileStore) loadRecordLocked(name string) (*PackageRecord, error) {
	data, err := os.ReadFile(fs.recordPath(name))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errPackageNotFound
		}
		return nil, err
	}
	var rec PackageRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("decode package record %s: %w", name, err)
	}
	if rec.Name == "" {
		rec.Name = name
	}
	return &rec, nil
}

func (fs *FileStore) saveRecordLocked(rec *PackageRecord) error {
	if err := os.MkdirAll(fs.packageDir(rec.Name), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	dst := fs.recordPath(rec.Name)
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func (fs *FileStore) packageDir(name string) string {
	return filepath.Join(fs.Root, "packages", sanitizeIndexName(name))
}

func (fs *FileStore) recordPath(name string) string {
	return filepath.Join(fs.packageDir(name), "package.json")
}

func (fs *FileStore) tarballPath(name, version string) string {
	return filepath.Join(fs.packageDir(name), sanitizeIndexName(version)+".tgz")
}

func latestActiveVersion(rec *PackageRecord) (StoredVersion, bool) {
	var best StoredVersion
	ok := false
	for _, v := range rec.Versions {
		if v.Yanked {
			continue
		}
		if !ok || versionGreater(v.Version, best.Version) {
			best = v
			ok = true
		}
	}
	return best, ok
}

func versionGreater(a, b string) bool {
	av, aerr := semver.ParseVersion(a)
	bv, berr := semver.ParseVersion(b)
	if aerr == nil && berr == nil {
		return semver.Compare(av, bv) > 0
	}
	return a > b
}

func totalDownloads(rec *PackageRecord) int64 {
	var n int64
	for _, v := range rec.Versions {
		n += v.Downloads
	}
	return n
}

func searchScore(name string, meta PublishMetadata, q string) (int, bool) {
	lowerName := strings.ToLower(name)
	switch {
	case lowerName == q:
		return 0, true
	case strings.HasPrefix(lowerName, q):
		return 1, true
	case strings.Contains(lowerName, q):
		return 2, true
	}
	if strings.Contains(strings.ToLower(meta.Description), q) {
		return 3, true
	}
	for _, kw := range meta.Keywords {
		if strings.Contains(strings.ToLower(kw), q) {
			return 4, true
		}
	}
	return 0, false
}

func manifestFromTarball(tarPath string) (*manifest.Manifest, error) {
	f, err := os.Open(tarPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("missing %s", manifest.ManifestFile)
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			continue
		}
		name := strings.TrimPrefix(path.Clean(filepath.ToSlash(hdr.Name)), "./")
		if name != manifest.ManifestFile {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(tr, 1<<20))
		if err != nil {
			return nil, err
		}
		m, err := manifest.Parse(data)
		if err != nil {
			return nil, err
		}
		if diags := manifest.Validate(m); len(diags) > 0 {
			return nil, errors.New(diags[0].Message)
		}
		return m, nil
	}
}

func versionDependencies(m *manifest.Manifest) ([]VersionDependency, error) {
	var out []VersionDependency
	add := func(kind string, deps []manifest.Dependency) error {
		for _, d := range deps {
			if d.Path != "" {
				return fmt.Errorf("dependency %q uses path=%q; path dependencies cannot be published", d.Name, d.Path)
			}
			if d.Git != nil {
				continue
			}
			name := d.PackageName
			if name == "" {
				name = d.Name
			}
			req := d.VersionReq
			if req == "" {
				req = "*"
			}
			out = append(out, VersionDependency{Name: name, Req: req, Kind: kind})
		}
		return nil
	}
	if err := add("normal", m.Dependencies); err != nil {
		return nil, err
	}
	if err := add("dev", m.DevDependencies); err != nil {
		return nil, err
	}
	return out, nil
}

func copyFeatures(m *manifest.Manifest) map[string][]string {
	features := copyStringSliceMap(m.Features)
	if len(m.DefaultFeatures) > 0 {
		if features == nil {
			features = map[string][]string{}
		}
		if _, ok := features["default"]; !ok {
			features["default"] = append([]string(nil), m.DefaultFeatures...)
		}
	}
	return features
}

func copyStringSliceMap(in map[string][]string) map[string][]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]string, len(in))
	for k, v := range in {
		out[k] = append([]string(nil), v...)
	}
	return out
}

func validatePackageName(name string) error {
	if name == "" {
		return fmt.Errorf("package name is empty")
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r == '_':
			continue
		case i > 0 && r >= '0' && r <= '9',
			i > 0 && r == '-':
			continue
		default:
			return fmt.Errorf("invalid package name %q", name)
		}
	}
	return nil
}

func validateVersionString(version string) error {
	if _, err := semver.ParseVersion(version); err != nil {
		return fmt.Errorf("invalid package version %q: %w", version, err)
	}
	return nil
}

type statusError struct {
	status int
	msg    string
}

func (e statusError) Error() string { return e.msg }

func badRequest(format string, args ...any) error {
	return statusError{status: http.StatusBadRequest, msg: fmt.Sprintf(format, args...)}
}

func writeRegistryError(w http.ResponseWriter, err error) {
	var se statusError
	switch {
	case errors.As(err, &se):
		http.Error(w, se.msg, se.status)
	case errors.Is(err, errPackageNotFound), errors.Is(err, errVersionNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, errVersionExists):
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		writeRegistryError(w, err)
		return
	}
	writeJSONBytes(w, status, data)
}

func writeJSONBytes(w http.ResponseWriter, status int, data []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

func jsonWithETag(v any) ([]byte, string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(data)
	return data, `"` + checksumPrefix + hex.EncodeToString(sum[:]) + `"`, nil
}

func etagMatches(header, etag string) bool {
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "*" || part == etag {
			return true
		}
	}
	return false
}

func pathSegments(escapedPath string) ([]string, error) {
	raw := strings.Trim(escapedPath, "/")
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			return nil, badRequest("empty path segment")
		}
		decoded, err := url.PathUnescape(part)
		if err != nil {
			return nil, badRequest("invalid path escape %q", part)
		}
		if decoded == "" || strings.Contains(decoded, "/") {
			return nil, badRequest("invalid path segment %q", decoded)
		}
		out = append(out, decoded)
	}
	return out, nil
}
