package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/osty/osty/internal/lockfile"
	"github.com/osty/osty/internal/manifest"
	"github.com/osty/osty/internal/pkgmgr"
)

// runRemove implements `osty remove NAME [NAME...]` (alias `rm`):
// drop one or more dependencies from osty.toml and re-resolve so the
// lockfile is consistent. Both [dependencies] and [dev-dependencies]
// are searched; --dev / --no-dev narrow the scope when a name lives
// in both.
//
// Exit codes: 0 success, 1 I/O failure, 2 usage / not-found error,
// 3 resolve failure after removal.
func runRemove(args []string, cliF cliFlags) {
	fs := flag.NewFlagSet("remove", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: osty remove [--dev] [--offline] NAME [NAME...]")
	}
	var (
		devOnly bool
		offline bool
	)
	fs.BoolVar(&devOnly, "dev", false, "only remove from [dev-dependencies]")
	fs.BoolVar(&offline, "offline", false, "do not fetch dependencies during the post-remove resolve")
	_ = fs.Parse(args)

	names := fs.Args()
	if len(names) == 0 {
		fs.Usage()
		os.Exit(2)
	}

	m, root, abort := loadManifestWithDiag(".", cliF)
	if abort {
		os.Exit(2)
	}
	manifestPath := filepath.Join(root, manifest.ManifestFile)

	missing := []string{}
	for _, name := range names {
		removed := removeDep(m, name, devOnly)
		if !removed {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "osty remove: no such dependency: %v\n", missing)
		os.Exit(2)
	}

	if err := manifest.Write(manifestPath, m); err != nil {
		fmt.Fprintf(os.Stderr, "osty remove: write manifest: %v\n", err)
		os.Exit(1)
	}

	// Drop the removed names from the lockfile up-front so the
	// resolver doesn't try to honor a stale pin for a dep that no
	// longer exists in the manifest closure.
	if err := dropLockEntries(root, names); err != nil {
		fmt.Fprintf(os.Stderr, "osty remove: %v\n", err)
		os.Exit(1)
	}

	env, err := pkgmgr.DefaultEnv(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty remove: %v\n", err)
		os.Exit(1)
	}
	env.Offline = offline
	for _, r := range m.Registries {
		env.Registries[r.Name] = r.URL
	}
	graph, err := pkgmgr.Resolve(context.Background(), m, env)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty remove: resolve: %v\n", err)
		os.Exit(3)
	}
	if err := pkgmgr.Vendor(graph, env); err != nil {
		fmt.Fprintf(os.Stderr, "osty remove: vendor: %v\n", err)
		os.Exit(1)
	}
	newLock, err := pkgmgr.LockFromGraph(graph)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty remove: project lockfile: %v\n", err)
		os.Exit(1)
	}
	if err := lockfile.Write(root, newLock); err != nil {
		fmt.Fprintf(os.Stderr, "osty remove: write lockfile: %v\n", err)
		os.Exit(1)
	}

	// Remove the vendored symlinks for the dropped names. Leftover
	// symlinks would point at stale cache entries and confuse the
	// workspace loader.
	for _, name := range names {
		_ = os.Remove(filepath.Join(env.VendorDir, name))
	}

	for _, name := range names {
		fmt.Printf("Removed %s\n", name)
	}
	_ = cliF
}

// removeDep drops the dependency named `name` from m. When devOnly is
// true only the dev list is searched. Returns true iff at least one
// entry was removed.
func removeDep(m *manifest.Manifest, name string, devOnly bool) bool {
	removed := false
	if !devOnly {
		m.Dependencies, removed = filterDeps(m.Dependencies, name, removed)
	}
	m.DevDependencies, removed = filterDeps(m.DevDependencies, name, removed)
	return removed
}

func filterDeps(in []manifest.Dependency, name string, alreadyRemoved bool) ([]manifest.Dependency, bool) {
	out := in[:0]
	removed := alreadyRemoved
	for _, d := range in {
		if d.Name == name {
			removed = true
			continue
		}
		out = append(out, d)
	}
	return out, removed
}
