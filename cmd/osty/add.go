package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/osty/osty/internal/lockfile"
	"github.com/osty/osty/internal/manifest"
	"github.com/osty/osty/internal/pkgmgr"
)

// runAdd implements `osty add <pkg>`: mutate osty.toml to include a
// new dependency, then re-resolve the lockfile so the new entry is
// immediately pinned.
//
// Supported spellings (parsed by parseAddArg):
//
//	osty add simple              # registry, "*" version (latest)
//	osty add simple@^1.0         # registry, explicit version req
//	osty add ./local-lib         # local path (relative or absolute)
//	osty add --path ../lib       # explicit path form
//	osty add --git https://github.com/x/y        # git source, default branch
//	osty add --git https://...x/y --tag v1.0.0   # git with tag
//	osty add --git https://...x/y --rev abcdef   # git at specific commit
//	osty add simple --dev        # goes into [dev-dependencies]
//	osty add simple --rename s   # use `s` locally instead of `simple`
//
// Exit codes: 0 success, 1 I/O failure, 2 usage error, 3 resolve
// failure (dep could not be fetched / doesn't match req).
func runAdd(args []string, cliF cliFlags) {
	fs := flag.NewFlagSet("add", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: osty add [flags] NAME[@VERSION_REQ]")
		fmt.Fprintln(os.Stderr, "       osty add [flags] --path DIR")
		fmt.Fprintln(os.Stderr, "       osty add [flags] --git URL [--tag REF | --branch REF | --rev SHA]")
	}
	var (
		flagPath    string
		flagGit     string
		flagTag     string
		flagBranch  string
		flagRev     string
		flagVersion string
		flagRename  string
		flagDev     bool
		flagOffline bool
	)
	fs.StringVar(&flagPath, "path", "", "local filesystem dependency path")
	fs.StringVar(&flagGit, "git", "", "git dependency URL")
	fs.StringVar(&flagTag, "tag", "", "git tag to pin (with --git)")
	fs.StringVar(&flagBranch, "branch", "", "git branch (with --git)")
	fs.StringVar(&flagRev, "rev", "", "git commit SHA (with --git)")
	fs.StringVar(&flagVersion, "version", "", "explicit version requirement (overrides @spec)")
	fs.StringVar(&flagRename, "rename", "", "local alias for the dep (defaults to package name)")
	fs.BoolVar(&flagDev, "dev", false, "add to [dev-dependencies]")
	fs.BoolVar(&flagOffline, "offline", false, "do not fetch dependencies; fail if caches are missing")
	_ = fs.Parse(args)

	m, root, abort := loadManifestWithDiag(".", cliF)
	if abort {
		os.Exit(2)
	}
	manifestPath := filepath.Join(root, manifest.ManifestFile)

	dep, err := buildDepFromAddArgs(fs.Args(), addFlags{
		path: flagPath, git: flagGit, tag: flagTag, branch: flagBranch,
		rev: flagRev, version: flagVersion, rename: flagRename,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty add: %v\n", err)
		fs.Usage()
		os.Exit(2)
	}

	// Abort if a dep with that local name already exists. Upgrading in
	// place is what `osty update NAME --version X` would do; a plain
	// `add` must not silently overwrite.
	if depExists(m, dep.Name, flagDev) {
		fmt.Fprintf(os.Stderr, "osty add: a dependency named %q already exists in %s; use `osty update` to change it\n",
			dep.Name, manifest.ManifestFile)
		os.Exit(2)
	}

	// Eagerly fetch the new dep so errors (bad URL, missing path,
	// version req with no matches) surface before we mutate disk.
	env, err := pkgmgr.DefaultEnv(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty add: %v\n", err)
		os.Exit(1)
	}
	env.Offline = flagOffline
	for _, r := range m.Registries {
		env.Registries[r.Name] = r.URL
	}
	src, err := pkgmgr.NewSource(dep)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty add: %v\n", err)
		os.Exit(3)
	}
	fetched, err := src.Fetch(context.Background(), env)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty add: %v\n", err)
		os.Exit(3)
	}

	// If the user didn't supply a version requirement for a registry
	// dep, pin to `^<fetched>` so future updates stay within the major.
	if dep.VersionReq == "" && dep.Path == "" && dep.Git == nil {
		// Shouldn't happen — NewSource would have errored. Guard for
		// defensive clarity.
		fmt.Fprintf(os.Stderr, "osty add: internal: dep has no source after fetch\n")
		os.Exit(1)
	}
	if src.Kind() == pkgmgr.SourceRegistry && strings.TrimSpace(dep.VersionReq) == "*" {
		dep.VersionReq = "^" + fetched.Version
	}

	if flagDev {
		m.DevDependencies = append(m.DevDependencies, dep)
	} else {
		m.Dependencies = append(m.Dependencies, dep)
	}
	if err := manifest.Write(manifestPath, m); err != nil {
		fmt.Fprintf(os.Stderr, "osty add: write manifest: %v\n", err)
		os.Exit(1)
	}

	// Re-resolve so the lockfile reflects the new graph.
	graph, err := pkgmgr.Resolve(context.Background(), m, env)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty add: resolve: %v\n", err)
		os.Exit(3)
	}
	if err := pkgmgr.Vendor(graph, env); err != nil {
		fmt.Fprintf(os.Stderr, "osty add: vendor: %v\n", err)
		os.Exit(1)
	}
	if err := lockfile.Write(root, pkgmgr.LockFromGraph(graph)); err != nil {
		fmt.Fprintf(os.Stderr, "osty add: write lockfile: %v\n", err)
		os.Exit(1)
	}
	what := "dependency"
	if flagDev {
		what = "dev-dependency"
	}
	fmt.Printf("Added %s %s %s (%s)\n", what, dep.Name, fetched.Version, src.URI())
	_ = cliF
}

// addFlags bundles the CLI flags relevant to building a Dependency
// from the arguments. Kept in a struct so buildDepFromAddArgs has a
// single, testable surface.
type addFlags struct {
	path, git, tag, branch, rev string
	version, rename             string
}

// buildDepFromAddArgs maps `osty add` CLI input to a Dependency
// record. Returns an error for mixed-source combinations or missing
// positional names.
func buildDepFromAddArgs(positional []string, f addFlags) (manifest.Dependency, error) {
	// Figure out which source the user is specifying.
	hasPath := f.path != ""
	hasGit := f.git != ""
	hasPos := len(positional) > 0

	switch {
	case hasPath && hasGit:
		return manifest.Dependency{}, fmt.Errorf("--path and --git are mutually exclusive")
	case hasPath && hasPos:
		return manifest.Dependency{}, fmt.Errorf("cannot combine --path with a positional name")
	case hasGit && hasPos:
		return manifest.Dependency{}, fmt.Errorf("cannot combine --git with a positional name")
	}

	switch {
	case hasPath:
		name := f.rename
		if name == "" {
			name = filepath.Base(f.path)
		}
		if name == "" {
			return manifest.Dependency{}, fmt.Errorf("could not derive a name from path %q; pass --rename", f.path)
		}
		return manifest.Dependency{
			Name:         name,
			Path:         f.path,
			DefaultFeats: true,
		}, nil
	case hasGit:
		name := f.rename
		if name == "" {
			name = guessNameFromGitURL(f.git)
		}
		if name == "" {
			return manifest.Dependency{}, fmt.Errorf("could not derive a name from %q; pass --rename", f.git)
		}
		gs := &manifest.GitSource{URL: f.git, Tag: f.tag, Branch: f.branch, Rev: f.rev}
		return manifest.Dependency{
			Name:         name,
			Git:          gs,
			DefaultFeats: true,
		}, nil
	}
	// Registry form: <name>[@<ver>]
	if len(positional) == 0 {
		return manifest.Dependency{}, fmt.Errorf("no dependency name given")
	}
	if len(positional) > 1 {
		return manifest.Dependency{}, fmt.Errorf("expected a single NAME[@VERSION]; got %q", strings.Join(positional, " "))
	}
	name, ver := splitNameVer(positional[0])
	if f.version != "" {
		ver = f.version
	}
	if ver == "" {
		ver = "*"
	}
	localName := f.rename
	if localName == "" {
		localName = name
	}
	return manifest.Dependency{
		Name:         localName,
		PackageName:  name,
		VersionReq:   ver,
		DefaultFeats: true,
	}, nil
}

// splitNameVer splits `name@version`. Versions may contain `.` and
// `-`; the first `@` is the split point (registry names cannot
// contain `@`).
func splitNameVer(s string) (string, string) {
	if i := strings.Index(s, "@"); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// guessNameFromGitURL extracts the last path segment of a git URL as
// the local alias. Strips a trailing `.git` suffix. Returns "" if the
// URL is too short to pattern-match.
func guessNameFromGitURL(u string) string {
	// Drop trailing slashes.
	u = strings.TrimRight(u, "/")
	// Prefer the segment after the last `/`, falling back on `:` for
	// scp-style git URLs (`git@host:user/repo`).
	var seg string
	if i := strings.LastIndex(u, "/"); i >= 0 {
		seg = u[i+1:]
	} else if i := strings.LastIndex(u, ":"); i >= 0 {
		seg = u[i+1:]
	} else {
		seg = u
	}
	return strings.TrimSuffix(seg, ".git")
}

// depExists reports whether a dep with local name exists in either
// the normal or dev list. Used to reject duplicate `osty add` calls.
func depExists(m *manifest.Manifest, name string, dev bool) bool {
	list := m.Dependencies
	if dev {
		list = m.DevDependencies
	}
	for _, d := range list {
		if d.Name == name {
			return true
		}
	}
	return false
}
