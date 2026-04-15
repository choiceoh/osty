// Storage is the filesystem-backed persistence layer for a registry
// backend. It mirrors the wire protocol 1:1: every read the server
// performs maps to exactly one directory / JSON read, and every
// publish is a single transactional write.
//
// Layout under Storage.Root:
//
//	crates/
//	  <name>/
//	    index.json                # IndexEntry
//	    <version>/
//	      package.tgz             # uploaded tarball
//	      metadata.json           # PublishMetadata (for listings)
//	tokens.json                   # auth database (optional)
//
// Concurrency: a single RWMutex guards the whole tree. The registry
// is expected to run in a single process (small-scale hosting), so a
// global lock is both correct and simpler than per-package locking.
// Readers take RLock, publishers take Lock.

package registry

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Storage persists the registry's index and blobs under Root.
type Storage struct {
	Root string

	mu sync.RWMutex
}

// NewStorage returns a Storage rooted at dir. The directory is
// created if it does not yet exist. Callers may populate the
// directory out-of-band (e.g. `cp -r` from a backup) and then point
// NewStorage at it.
func NewStorage(dir string) (*Storage, error) {
	if dir == "" {
		return nil, fmt.Errorf("storage root is empty")
	}
	if err := os.MkdirAll(filepath.Join(dir, "crates"), 0o755); err != nil {
		return nil, err
	}
	return &Storage{Root: dir}, nil
}

// nameRE constrains package names to characters that are safe in
// URLs and on every common filesystem. Matches the regex manifest
// validation uses (keep in sync if that ever changes).
var nameRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_\-]*$`)

// validateName returns an error if name would be unsafe to use as a
// path segment. Prevents path traversal via `..` and rejects
// empty / shell-hostile inputs.
func validateName(name string) error {
	if name == "" {
		return fmt.Errorf("name is empty")
	}
	if len(name) > 128 {
		return fmt.Errorf("name too long")
	}
	if !nameRE.MatchString(name) {
		return fmt.Errorf("invalid package name %q", name)
	}
	return nil
}

// validateVersion is a cheap structural check before hitting the
// filesystem — we trust the authoritative parse happens elsewhere
// (semver.ParseVersion). Here we only forbid things that would be
// unsafe as path segments.
func validateVersion(v string) error {
	if v == "" {
		return fmt.Errorf("version is empty")
	}
	if len(v) > 64 {
		return fmt.Errorf("version too long")
	}
	if strings.ContainsAny(v, "/\\ \t\n\x00") || v == "." || v == ".." {
		return fmt.Errorf("invalid version %q", v)
	}
	return nil
}

// ReadIndex returns the current IndexEntry for name. When no such
// package has been published yet, returns (nil, os.ErrNotExist).
func (s *Storage) ReadIndex(name string) (*IndexEntry, error) {
	if err := validateName(name); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.readIndexLocked(name)
}

func (s *Storage) readIndexLocked(name string) (*IndexEntry, error) {
	f, err := os.Open(filepath.Join(s.Root, "crates", name, "index.json"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var idx IndexEntry
	if err := json.NewDecoder(f).Decode(&idx); err != nil {
		return nil, fmt.Errorf("decode index for %s: %w", name, err)
	}
	return &idx, nil
}

// OpenTarball opens the stored tarball for reading. Caller must Close
// the returned file. Returns os.ErrNotExist when the version was
// never published (or has been removed).
func (s *Storage) OpenTarball(name, version string) (*os.File, error) {
	if err := validateName(name); err != nil {
		return nil, err
	}
	if err := validateVersion(version); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return os.Open(s.tarballPath(name, version))
}

func (s *Storage) tarballPath(name, version string) string {
	return filepath.Join(s.Root, "crates", name, version, "package.tgz")
}

// Publish persists a new version under name. The operation is
// write-once per (name, version): re-publishing an existing version
// returns ErrVersionExists so registries can enforce immutability.
//
// The tarball is read in full (we need its checksum anyway) and the
// caller-provided expected checksum is verified before any on-disk
// state changes. Atomicity: the tarball is written to a temp file
// first, then renamed; the updated index is written via the same
// write-then-rename pattern.
func (s *Storage) Publish(entry PublishEntry, tarball io.Reader) error {
	if err := validateName(entry.Name); err != nil {
		return err
	}
	if err := validateVersion(entry.Version); err != nil {
		return err
	}
	if entry.PublishedAt.IsZero() {
		entry.PublishedAt = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// Reject duplicates before writing anything.
	if existing, err := s.readIndexLocked(entry.Name); err == nil {
		for _, v := range existing.Versions {
			if v.Version == entry.Version {
				return ErrVersionExists
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	pkgDir := filepath.Join(s.Root, "crates", entry.Name, entry.Version)
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		return err
	}

	// Stream the tarball to disk, hashing as we go. Writing to a temp
	// file keeps partial uploads invisible if the connection drops.
	tarPath := filepath.Join(pkgDir, "package.tgz")
	tmpPath := tarPath + ".tmp"
	tmp, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	hw := newHashWriter()
	mw := io.MultiWriter(tmp, hw)
	n, err := io.Copy(mw, tarball)
	if err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	got := hw.checksum()
	if entry.Checksum != "" && entry.Checksum != got {
		os.Remove(tmpPath)
		return fmt.Errorf("%w: advertised %s, got %s",
			ErrChecksumMismatch, entry.Checksum, got)
	}
	entry.Checksum = got
	entry.Size = n
	if err := os.Rename(tmpPath, tarPath); err != nil {
		os.Remove(tmpPath)
		return err
	}

	// Write metadata sidecar — independent of the index so listing
	// pages can present descriptions/keywords without walking every
	// version entry.
	metaPath := filepath.Join(pkgDir, "metadata.json")
	if err := writeJSONAtomic(metaPath, entry.Metadata); err != nil {
		return err
	}

	// Update the index.
	idx, err := s.readIndexLocked(entry.Name)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		idx = &IndexEntry{Name: entry.Name}
	}
	idx.Versions = append(idx.Versions, Version{
		Version:      entry.Version,
		Checksum:     entry.Checksum,
		PublishedAt:  entry.PublishedAt,
		Yanked:       false,
		Dependencies: entry.Dependencies,
		Features:     entry.Features,
	})
	sortVersions(idx.Versions)
	indexPath := filepath.Join(s.Root, "crates", entry.Name, "index.json")
	return writeJSONAtomic(indexPath, idx)
}

// Yank marks an existing version as yanked. Yanked versions stay
// downloadable (existing lockfiles keep working) but are hidden from
// resolver queries. Returns os.ErrNotExist if the package or version
// is unknown.
func (s *Storage) Yank(name, version string, yanked bool) error {
	if err := validateName(name); err != nil {
		return err
	}
	if err := validateVersion(version); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	idx, err := s.readIndexLocked(name)
	if err != nil {
		return err
	}
	found := false
	for i := range idx.Versions {
		if idx.Versions[i].Version == version {
			idx.Versions[i].Yanked = yanked
			found = true
			break
		}
	}
	if !found {
		return os.ErrNotExist
	}
	indexPath := filepath.Join(s.Root, "crates", name, "index.json")
	return writeJSONAtomic(indexPath, idx)
}

// PublishEntry is the input to Storage.Publish: everything needed to
// mint a new index row plus the tarball metadata the server should
// persist.
type PublishEntry struct {
	Name         string
	Version      string
	Checksum     string // optional; verified if non-empty, otherwise recorded
	PublishedAt  time.Time
	Dependencies []VersionDependency
	Features     map[string][]string
	Metadata     PublishMetadata
	Size         int64 // set by Storage.Publish after reading the tarball
}

// sortVersions orders an index's versions by (parsed semver) so
// readers always see ascending order. Versions that fail to parse
// sort to the end in registry-insertion order — they should be rare
// enough (broken uploads) that stable fallback ordering is fine.
func sortVersions(vs []Version) {
	sort.SliceStable(vs, func(i, j int) bool {
		return compareVersionStrings(vs[i].Version, vs[j].Version) < 0
	})
}

// compareVersionStrings is a minimal, dependency-free version
// comparator. We intentionally do not import pkgmgr/semver to keep
// this package leaf-level (server tooling can vendor it without
// pulling the package manager). The comparison is "good enough" for
// index ordering: lex by dot-segments, numeric chunks compared
// numerically.
func compareVersionStrings(a, b string) int {
	as := strings.SplitN(a, "+", 2)[0]
	bs := strings.SplitN(b, "+", 2)[0]
	// Split off pre-release.
	aMain, aPre := splitPre(as)
	bMain, bPre := splitPre(bs)
	if c := compareDotted(aMain, bMain); c != 0 {
		return c
	}
	// No pre-release > with pre-release (semver §11).
	if aPre == "" && bPre != "" {
		return 1
	}
	if aPre != "" && bPre == "" {
		return -1
	}
	return compareDotted(aPre, bPre)
}

func splitPre(s string) (string, string) {
	i := strings.IndexByte(s, '-')
	if i < 0 {
		return s, ""
	}
	return s[:i], s[i+1:]
}

func compareDotted(a, b string) int {
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	n := len(as)
	if len(bs) < n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		if c := compareIdent(as[i], bs[i]); c != 0 {
			return c
		}
	}
	return len(as) - len(bs)
}

func compareIdent(a, b string) int {
	an, aok := parseUint(a)
	bn, bok := parseUint(b)
	if aok && bok {
		switch {
		case an < bn:
			return -1
		case an > bn:
			return 1
		default:
			return 0
		}
	}
	// Numeric < alphanumeric, per semver ordering for pre-release
	// identifiers; for main version components both sides are
	// numeric in practice so this branch is rare.
	if aok {
		return -1
	}
	if bok {
		return 1
	}
	return strings.Compare(a, b)
}

func parseUint(s string) (uint64, bool) {
	if s == "" {
		return 0, false
	}
	var n uint64
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + uint64(c-'0')
	}
	return n, true
}

// writeJSONAtomic marshals v to path using the write-temp-then-rename
// idiom so concurrent readers never observe a half-written file.
func writeJSONAtomic(path string, v any) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
