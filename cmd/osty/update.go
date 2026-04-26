package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/osty/osty/internal/lockfile"
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
	env, err := pkgmgr.DefaultEnv(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty update: %v\n", err)
		os.Exit(1)
	}
	env.Offline = offline
	for _, r := range m.Registries {
		env.Registries[r.Name] = r.URL
	}

	// Capture the pre-update versions so we can render a meaningful
	// diff at the end. A missing lockfile (first-time resolve) gives
	// us an empty map, in which case the diff degenerates to the
	// usual "Updated N" listing.
	prev := map[string]string{}
	if existing, _ := lockfile.Read(root); existing != nil {
		for _, p := range existing.Packages {
			prev[p.Name] = p.Version
		}
	}

	// Selective update: discard just the targeted entries from the
	// lockfile before resolving. The resolver then picks fresh
	// versions for those names while honoring existing pins for the
	// rest.
	targets := fs.Args()
	if len(targets) > 0 {
		// Validate that every targeted name actually appears as a
		// top-level dependency in the manifest. Otherwise the user
		// gets a silent no-op when they typo a name.
		known := map[string]bool{}
		for _, d := range m.Dependencies {
			known[d.Name] = true
		}
		for _, d := range m.DevDependencies {
			known[d.Name] = true
		}
		var unknown []string
		for _, t := range targets {
			if !known[t] {
				unknown = append(unknown, t)
			}
		}
		if len(unknown) > 0 {
			fmt.Fprintf(os.Stderr, "osty update: no such dependency in osty.toml: %v\n", unknown)
			os.Exit(2)
		}
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
	newLock, err := pkgmgr.LockFromGraph(graph)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty update: project lockfile: %v\n", err)
		os.Exit(1)
	}
	if err := lockfile.Write(root, newLock); err != nil {
		fmt.Fprintf(os.Stderr, "osty update: %v\n", err)
		os.Exit(1)
	}
	// Diff render. For each resolved package: → marks an upgrade,
	// + marks a freshly-added entry, blank prefix means no change.
	changed := 0
	fmt.Printf("Resolved %d dependencies\n", len(graph.Nodes))
	for _, name := range graph.Order {
		n := graph.Nodes[name]
		if n == nil || n.Fetched == nil {
			continue
		}
		old, hadPrev := prev[name]
		switch {
		case !hadPrev:
			fmt.Printf("  + %s %s\n", name, n.Fetched.Version)
			changed++
		case old != n.Fetched.Version:
			fmt.Printf("  → %s %s -> %s\n", name, old, n.Fetched.Version)
			changed++
		default:
			fmt.Printf("    %s %s\n", name, n.Fetched.Version)
		}
	}
	if changed == 0 {
		fmt.Println("Lockfile already up-to-date")
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
