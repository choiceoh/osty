// Package server implements the HTTP service side of the osty
// registry protocol defined in internal/registry/client.go.
//
// The server is deliberately small: a filesystem-backed storage
// layout, a token-gated publish endpoint, and read-only index /
// tarball endpoints. It can be run on its own (cmd/osty-registry)
// or embedded as an http.Handler in another Go binary.
//
// Storage layout on disk (rooted at Storage.Root):
//
//	<root>/
//	  crates/
//	    <name>/
//	      index.json             ← IndexEntry, rewritten on every publish
//	      <version>/
//	        package.tgz          ← raw upload body
//	        metadata.json        ← PublishMetadata mirror (for listing pages)
//	  tokens.json                ← TokenDB (see auth.go)
//
// The layout is human-inspectable by design — a registry operator can
// hand-edit index.json to yank a version or rotate tokens without
// running a separate admin tool.
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/osty/osty/internal/registry"
)

// ErrNotFound is returned by Storage when a package or version does
// not exist. Callers translate this to HTTP 404.
var ErrNotFound = errors.New("not found")

// ErrVersionExists is returned when a publish would overwrite an
// already-published, non-yanked version. Callers translate this to
// HTTP 409.
var ErrVersionExists = errors.New("version already published")

// namePattern constrains package names to the same character set the
// CLI accepts in `osty.toml`: ASCII letters, digits, underscores,
// and dashes. Rejecting anything else keeps filesystem storage safe
// from path traversal.
var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// versionPattern is a loose SemVer-ish filter — strict parsing
// happens in the pkgmgr/semver package when a client resolves a dep.
// We only enforce "no path separators, reasonable shape" here.
var versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.\-]+)?$`)

// ValidateName reports whether name is acceptable on this registry.
func ValidateName(name string) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("invalid package name %q (allowed: [A-Za-z0-9_-], 1..64 chars, must start with alnum)", name)
	}
	return nil
}

// ValidateVersion reports whether version is a well-formed SemVer.
func ValidateVersion(version string) error {
	if !versionPattern.MatchString(version) {
		return fmt.Errorf("invalid version %q (expected MAJOR.MINOR.PATCH[-prerelease][+build])", version)
	}
	return nil
}

// Storage is a filesystem-backed registry backend. All methods are
// safe for concurrent use; a single sync.Mutex serializes index
// mutations. Read operations release the lock before touching disk,
// so slow tarball streams do not block publishes.
type Storage struct {
	Root string

	mu sync.Mutex
}

// NewStorage returns a Storage rooted at root. The directory is
// created if missing. Callers pass an absolute path in production;
// tests pass t.TempDir().
func NewStorage(root string) (*Storage, error) {
	if root == "" {
		return nil, fmt.Errorf("registry storage root must not be empty")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(abs, "crates"), 0o755); err != nil {
		return nil, err
	}
	return &Storage{Root: abs}, nil
}

// Index returns the IndexEntry for name. Yanked versions are
// included in the response — the client filters them on read.
// Returns ErrNotFound if the package has never been published.
func (s *Storage) Index(name string) (*registry.IndexEntry, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	p := s.indexPath(name)
	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var idx registry.IndexEntry
	if err := json.Unmarshal(b, &idx); err != nil {
		return nil, fmt.Errorf("corrupt index %s: %w", p, err)
	}
	return &idx, nil
}

// OpenTarball returns an open file handle for a published tarball.
// Caller is responsible for Closing it. Returns ErrNotFound if the
// version does not exist.
func (s *Storage) OpenTarball(name, version string) (io.ReadCloser, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	if err := ValidateVersion(version); err != nil {
		return nil, err
	}
	f, err := os.Open(s.tarballPath(name, version))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return f, nil
}

// PublishInput carries everything Publish needs to commit a new
// version. Tarball is the raw `.tgz` bytes already validated against
// Checksum by the HTTP layer.
type PublishInput struct {
	Name         string
	Version      string
	Checksum     string // "sha256:<hex>"
	Tarball      []byte
	Metadata     registry.PublishMetadata
	Dependencies []registry.VersionDependency
	Features     map[string][]string
	PublishedAt  time.Time
}

// Publish writes a new version. It rejects re-publishing an existing
// version (ErrVersionExists) — the registry is append-only from a
// client's perspective.
//
// The sequence is deliberately "write blob first, then rewrite
// index" so a crash mid-publish leaves a harmless orphan tarball
// rather than a dangling index entry pointing at a missing file.
func (s *Storage) Publish(in PublishInput) error {
	if err := ValidateName(in.Name); err != nil {
		return err
	}
	if err := ValidateVersion(in.Version); err != nil {
		return err
	}
	if len(in.Tarball) == 0 {
		return fmt.Errorf("empty tarball")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Reject duplicate versions.
	if _, err := os.Stat(s.tarballPath(in.Name, in.Version)); err == nil {
		return ErrVersionExists
	}

	versionDir := s.versionDir(in.Name, in.Version)
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(versionDir, "package.tgz"), in.Tarball, 0o644); err != nil {
		return err
	}
	metaBytes, err := json.MarshalIndent(in.Metadata, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(versionDir, "metadata.json"), metaBytes, 0o644); err != nil {
		return err
	}

	// Load-or-create the index and append this version.
	var idx registry.IndexEntry
	if b, err := os.ReadFile(s.indexPath(in.Name)); err == nil {
		if err := json.Unmarshal(b, &idx); err != nil {
			return fmt.Errorf("corrupt index: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if idx.Name == "" {
		idx.Name = in.Name
	}
	at := in.PublishedAt
	if at.IsZero() {
		at = time.Now().UTC()
	}
	idx.Versions = append(idx.Versions, registry.Version{
		Version:      in.Version,
		Checksum:     in.Checksum,
		PublishedAt:  at,
		Yanked:       false,
		Dependencies: in.Dependencies,
		Features:     in.Features,
	})
	// Sort by PublishedAt so the listing has a stable order; ties
	// broken by version string. Callers do semver ranking themselves.
	sort.SliceStable(idx.Versions, func(i, j int) bool {
		if !idx.Versions[i].PublishedAt.Equal(idx.Versions[j].PublishedAt) {
			return idx.Versions[i].PublishedAt.Before(idx.Versions[j].PublishedAt)
		}
		return idx.Versions[i].Version < idx.Versions[j].Version
	})
	out, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.indexPath(in.Name), out, 0o644)
}

// Yank marks a version as yanked without deleting its tarball. An
// unyank is a Yank with yanked=false. Returns ErrNotFound if the
// package or version is unknown.
func (s *Storage) Yank(name, version string, yanked bool) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	if err := ValidateVersion(version); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.indexPath(name))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotFound
		}
		return err
	}
	var idx registry.IndexEntry
	if err := json.Unmarshal(b, &idx); err != nil {
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
		return ErrNotFound
	}
	out, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.indexPath(name), out, 0o644)
}

// List returns the names of every package with at least one
// published version, in lexicographic order. Used by the root
// endpoint so operators can browse the registry contents.
func (s *Storage) List() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(s.Root, "crates"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(s.Root, "crates", e.Name(), "index.json")); err == nil {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func (s *Storage) indexPath(name string) string {
	return filepath.Join(s.Root, "crates", name, "index.json")
}

func (s *Storage) versionDir(name, version string) string {
	return filepath.Join(s.Root, "crates", name, version)
}

func (s *Storage) tarballPath(name, version string) string {
	return filepath.Join(s.versionDir(name, version), "package.tgz")
}

// writeFileAtomic writes data to a sibling temp file then renames it
// over path. On POSIX filesystems rename is atomic, so readers see
// either the old or new file, never a half-written one.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	cleanup = false
	return nil
}
