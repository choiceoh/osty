package backend

import (
	"path/filepath"

	"github.com/osty/osty/internal/profile"
)

// Layout identifies the project/profile/target namespace for backend
// artifacts. It preserves the existing profile.ArtifactKey convention and adds
// a backend subdirectory underneath it.
type Layout struct {
	Root    string
	Profile string
	Target  string
}

// Key returns the profile/target artifact key, e.g. "debug" or
// "release-amd64-linux".
func (l Layout) Key() string {
	return profile.ArtifactKey(l.Profile, l.Target)
}

// OutputRoot returns <root>/.osty/out/<key>.
func (l Layout) OutputRoot() string {
	return profile.OutputDir(l.Root, l.Profile, l.Target)
}

// OutputDir returns <root>/.osty/out/<key>/<backend>.
func (l Layout) OutputDir(n Name) string {
	return filepath.Join(l.OutputRoot(), n.String())
}

// CacheRoot returns <root>/.osty/cache/<key>.
func (l Layout) CacheRoot() string {
	return filepath.Join(l.Root, profile.CacheDirName, l.Key())
}

// CachePath returns <root>/.osty/cache/<key>/<backend>.json.
func (l Layout) CachePath(n Name) string {
	return filepath.Join(l.CacheRoot(), n.String()+".json")
}

// LegacyCachePath returns the current Go-only fingerprint path. It exists so
// the migration can read or retire old cache files deliberately.
func (l Layout) LegacyCachePath() string {
	return profile.CachePath(l.Root, l.Profile, l.Target)
}

// Artifacts is the conventional set of paths a backend may write. Not every
// field is populated by every backend or emit mode.
type Artifacts struct {
	Key       string
	OutputDir string
	CachePath string

	GoSource string
	LLVMIR   string
	Object   string
	Binary   string

	RuntimeDir string
}

// Artifacts returns conventional artifact paths for backend n. binaryName may
// be empty when the caller only needs source/IR/object paths.
func (l Layout) Artifacts(n Name, binaryName string) Artifacts {
	out := Artifacts{
		Key:       l.Key(),
		OutputDir: l.OutputDir(n),
		CachePath: l.CachePath(n),
	}
	switch n {
	case NameGo:
		out.GoSource = filepath.Join(out.OutputDir, "main.go")
	case NameLLVM:
		out.LLVMIR = filepath.Join(out.OutputDir, "main.ll")
		out.Object = filepath.Join(out.OutputDir, "main.o")
		out.RuntimeDir = filepath.Join(out.OutputDir, "runtime")
	}
	if binaryName != "" {
		out.Binary = filepath.Join(out.OutputDir, binaryName)
	}
	return out
}
