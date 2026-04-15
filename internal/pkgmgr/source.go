// Package pkgmgr is the dependency resolver, fetcher, and vendoring
// layer sitting between the manifest / lockfile and the rest of the
// toolchain.
//
// Lifecycle from a CLI command's point of view:
//
//  1. manifest.Load() gives us the declared deps.
//  2. pkgmgr.Resolve() walks them, picks concrete versions, and
//     returns a ResolvedGraph. Uses osty.lock when present; writes
//     one back when the graph changes.
//  3. pkgmgr.Vendor() materializes every node of the graph into
//     `<project>/.osty/deps/<name>/` so the workspace
//     loader can find them by directory.
//  4. The caller builds a resolve.Workspace over the project root
//     plus the vendored deps and proceeds with the normal front-end
//     pipeline.
//
// The resolver is intentionally simple — a greedy highest-match per
// name. That's enough for path and git sources; the registry source
// is where we'd grow a real SAT-style solver if it becomes
// necessary. Diamond conflicts currently error cleanly rather than
// silently picking a winner.
package pkgmgr

import (
	"context"

	"github.com/osty/osty/internal/manifest"
)

// SourceKind selects how a Source materializes its package. Each
// Dependency from the manifest maps to exactly one kind.
type SourceKind int

const (
	// SourcePath is a local path (`path = "../lib"`). Always available,
	// never hits the network. Checksum is the SHA-256 of the directory's
	// content tree.
	SourcePath SourceKind = iota
	// SourceGit clones a git repository to the user cache then
	// materializes a committed ref.
	SourceGit
	// SourceRegistry downloads a signed tarball from an osty registry.
	SourceRegistry
)

func (k SourceKind) String() string {
	switch k {
	case SourcePath:
		return "path"
	case SourceGit:
		return "git"
	case SourceRegistry:
		return "registry"
	}
	return "unknown"
}

// Source is the uniform interface over local, git, and registry
// dependencies. Resolve() calls Fetch on each source, then feeds the
// resulting FetchedPackage to the graph builder.
//
// Implementations are defined in source_path.go, source_git.go, and
// source_registry.go. A Source value represents the *requirement*
// (name + pointer to remote / path) — Fetch materializes that
// requirement into a concrete version on disk.
type Source interface {
	// Kind reports which backing technology the Source uses.
	Kind() SourceKind

	// Name is the local dependency name (the key under
	// [dependencies] in the manifest). Not necessarily the package's
	// canonical name — see FetchedPackage.Manifest.Package.Name.
	Name() string

	// URI returns a lockfile-stable URI that identifies the source:
	//
	//   path+./relative/dir
	//   git+https://example/repo?tag=v1.0.0
	//   registry+https://registry.osty.dev
	//
	// Written to osty.lock's `source` field. The resolver compares
	// URIs for cache reuse.
	URI() string

	// Fetch materializes the source into cache + returns the local
	// directory plus metadata needed by the resolver.
	Fetch(ctx context.Context, env *Env) (*FetchedPackage, error)
}

// FetchedPackage is the output of Source.Fetch: the sources are now
// on disk at LocalDir, and we have access to the package's own
// manifest.
type FetchedPackage struct {
	// LocalDir is the absolute path to the unpacked package sources.
	// The directory contains osty.toml plus .osty files; resolver,
	// checker, and transpiler all run over it directly.
	LocalDir string

	// Manifest is the unpacked package's osty.toml. Parsed because
	// the graph builder needs Package.Name / Version and transitive
	// Dependencies.
	Manifest *manifest.Manifest

	// Version is what the lockfile pins: for registry + git sources
	// this is the semver string (or raw commit for `git rev=`);
	// for path sources it mirrors the sibling manifest's
	// Package.Version so graph-node identity is stable.
	Version string

	// Checksum is the content-hash pin. Empty for path sources;
	// required for registry + git sources.
	Checksum string
}

// Env carries the ambient configuration the resolver needs: caches,
// registry credentials, transport. Collected here so Source
// implementations don't each reach for globals.
type Env struct {
	// CacheDir is the user-global cache root, typically
	// ~/.osty/cache. Registry downloads + cloned git repos live
	// under subdirectories here.
	CacheDir string

	// VendorDir is the per-project vendor root, typically
	// <project>/.osty/deps. Final materialized deps land here with
	// predictable `<name>-<version>` directory names.
	VendorDir string

	// ProjectRoot is the directory that contains the top-level
	// osty.toml. Used to resolve relative path dependencies.
	ProjectRoot string

	// Registry holds registry base URLs indexed by name. The default
	// registry is keyed "".
	Registries map[string]string

	// Offline, when true, forbids any network or disk side effect
	// beyond the existing caches. Used by `osty check` et al when
	// the user explicitly opts out of updates.
	Offline bool
}

// DefaultEnv returns an Env populated from the user's home directory
// and the given project root. Callers typically tweak Registries
// based on the manifest's [registries.*] overrides before passing it
// in.
func DefaultEnv(projectRoot string) (*Env, error) {
	home, err := userHomeDir()
	if err != nil {
		return nil, err
	}
	cache := joinPath(home, ".osty", "cache")
	vendor := joinPath(projectRoot, ".osty", "deps")
	return &Env{
		CacheDir:    cache,
		VendorDir:   vendor,
		ProjectRoot: projectRoot,
		Registries: map[string]string{
			"": DefaultRegistryURL,
		},
	}, nil
}

// DefaultRegistryURL is the URL of the official osty registry when no
// [registries] override is present. Exported so tests can swap it.
var DefaultRegistryURL = "https://registry.osty.dev"

// NewSource constructs the Source matching a manifest.Dependency.
// Exactly one of Path / Git / VersionReq must be set; NewSource
// assumes the manifest parser enforced that.
func NewSource(d manifest.Dependency) (Source, error) {
	return newSourceFromDir(d, "")
}

// newSourceFromDir is the resolver's internal constructor. baseDir is
// the directory containing the manifest that declared d, so transitive
// path dependencies resolve relative to their own package instead of
// the root project.
func newSourceFromDir(d manifest.Dependency, baseDir string) (Source, error) {
	switch {
	case d.Path != "":
		return &pathSource{name: d.Name, path: d.Path, baseDir: baseDir}, nil
	case d.Git != nil:
		return &gitSource{
			name:   d.Name,
			url:    d.Git.URL,
			tag:    d.Git.Tag,
			branch: d.Git.Branch,
			rev:    d.Git.Rev,
		}, nil
	case d.VersionReq != "":
		return &registrySource{
			name:         d.Name,
			packageName:  firstNonEmpty(d.PackageName, d.Name),
			versionReq:   d.VersionReq,
			registryName: d.Registry,
		}, nil
	}
	return nil, &depError{Name: d.Name, Msg: "dependency has no source"}
}

// depError wraps a diagnostic about a single dependency. Defined here
// so source_* files can return structured errors without importing a
// separate error type.
type depError struct {
	Name string
	Msg  string
}

func (e *depError) Error() string { return e.Name + ": " + e.Msg }

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
