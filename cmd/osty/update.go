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

// runUpdate implements `osty update [NAME...]`:
//
//   - No arguments: re-resolve every dependency against the current
//     manifest, refreshing the lockfile. Registry / git sources
//     advance within their declared constraints; path sources are
//     re-hashed.
//
//   - With one or more NAMEs: only those top-level dependencies are
//     re-resolved. Their transitive deps are also refreshed. Everything
//     else in the lockfile is left pinned at its existing version.
//
// Exit codes: 0 success, 1 I/O failure, 2 usage / manifest error,
// 3 resolve failure.
func runUpdate(args []string, cliF cliFlags) {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: osty update [--offline] [NAME...]")
	}
	var offline bool
	fs.BoolVar(&offline, "offline", false, "do not fetch dependencies; fail if caches are missing")
	_ = fs.Parse(args)

	m, root, abort := loadManifestWithDiag(".", cliF)
	if abort {
		os.Exit(2)
	}
	_ = filepath.Join(root, manifest.ManifestFile) // reserved for future selective-rewrite support
	env, err := pkgmgr.DefaultEnv(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty update: %v\n", err)
		os.Exit(1)
	}
	env.Offline = offline
	for _, r := range m.Registries {
		env.Registries[r.Name] = r.URL
	}

	// Selective update: we implement it by discarding just the
	// targeted entries from the lockfile before resolving. The
	// resolver will then pick fresh versions for those names while
	// honoring existing pins for the rest.
	targets := fs.Args()
	if len(targets) > 0 {
		if err := dropLockEntries(root, targets); err != nil {
			fmt.Fprintf(os.Stderr, "osty update: %v\n", err)
			os.Exit(1)
		}
	} else {
		// Full update: drop the lockfile entirely so Resolve starts
		// fresh. Using os.Remove (not truncate) so a missing lockfile
		// is still treated as "first resolve".
		_ = os.Remove(filepath.Join(root, lockfile.LockFile))
	}

	graph, err := pkgmgr.Resolve(context.Background(), m, env)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty update: %v\n", err)
		os.Exit(3)
	}
	if err := pkgmgr.Vendor(graph, env); err != nil {
		fmt.Fprintf(os.Stderr, "osty update: %v\n", err)
		os.Exit(1)
	}
	if err := lockfile.Write(root, pkgmgr.LockFromGraph(graph)); err != nil {
		fmt.Fprintf(os.Stderr, "osty update: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Updated %d dependencies\n", len(graph.Nodes))
	for _, name := range graph.Order {
		n := graph.Nodes[name]
		if n == nil || n.Fetched == nil {
			continue
		}
		fmt.Printf("  %s %s\n", name, n.Fetched.Version)
	}
	_ = cliF
}

// dropLockEntries removes the given package names from the lockfile
// in place. If the lockfile doesn't exist this is a no-op — a missing
// lockfile already means "re-resolve everything."
func dropLockEntries(root string, names []string) error {
	lock, err := lockfile.Read(root)
	if err != nil {
		return err
	}
	if lock == nil {
		return nil
	}
	drop := map[string]bool{}
	for _, n := range names {
		drop[n] = true
	}
	kept := lock.Packages[:0]
	for _, p := range lock.Packages {
		if drop[p.Name] {
			continue
		}
		kept = append(kept, p)
	}
	lock.Packages = kept
	return lockfile.Write(root, lock)
}
