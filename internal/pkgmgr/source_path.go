package pkgmgr

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/osty/osty/internal/manifest"
)

// pathSource is a local filesystem dependency — `dep = { path = "..." }`.
// Fetch does no copying: the LocalDir is the directory the manifest
// points at, resolved against the project root. The package manager
// trusts the filesystem here — changes to the source directory
// automatically flow into the next resolve + build.
type pathSource struct {
	name    string
	path    string // as written in the manifest, possibly relative
	baseDir string // directory containing the manifest that declared path
}

func (s *pathSource) Kind() SourceKind { return SourcePath }
func (s *pathSource) Name() string     { return s.name }

// URI returns a lockfile-stable identifier for the source. For path
// dependencies we record the literal manifest string — we do NOT
// absolutize it, so lockfiles stay portable when collaborators have
// identical relative layouts.
func (s *pathSource) URI() string {
	return GolegacyPathSourceURI(filepath.ToSlash(s.path))
}

func (s *pathSource) absPath(env *Env) string {
	abs := s.path
	if filepath.IsAbs(abs) {
		return filepath.Clean(abs)
	}
	base := s.baseDir
	if base == "" && env != nil {
		base = env.ProjectRoot
	}
	return filepath.Clean(filepath.Join(base, abs))
}

// Fetch reads the dependency's osty.toml to discover its version and
// its own declared deps. The returned LocalDir is the absolute
// directory on disk.
func (s *pathSource) Fetch(ctx context.Context, env *Env) (*FetchedPackage, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	abs := s.absPath(env)
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("path dependency %s: %w", s.name, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("path dependency %s: %s is not a directory", s.name, abs)
	}
	manifestPath := filepath.Join(abs, manifest.ManifestFile)
	depManifest, err := manifest.Read(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("path dependency %s: %w", s.name, err)
	}
	// Path sources don't get a checksum — the source of truth is the
	// working tree, which may change between builds. Lockfile writers
	// leave checksum empty for this kind.
	return &FetchedPackage{
		LocalDir: abs,
		Manifest: depManifest,
		Version:  depManifest.Package.Version,
		Checksum: "",
	}, nil
}
