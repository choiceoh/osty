package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/lockfile"
	"github.com/osty/osty/internal/manifest"
	"github.com/osty/osty/internal/pkgmgr"
)

// loadManifestWithDiag is the shared manifest entry point used by
// every manifest-consuming subcommand (`add`, `update`, `build`,
// `run`, `test`, `publish`). It walks up from `start` to find
// osty.toml, parses + validates, and renders every diagnostic through
// the standard formatter so manifest errors carry the same caret
// underlines and Exxxx codes as compile errors.
//
// Returns:
//   - m: the parsed manifest (nil only when the file couldn't be read)
//   - root: the directory containing osty.toml (empty when m is nil)
//   - abort: true when diagnostics include any error-severity entry;
//     callers should Exit(2) in that case so usage error semantics
//     stay consistent across subcommands.
//
// The helper prints diagnostics to stderr as a side effect; callers
// only need to check `abort` and exit.
func loadManifestWithDiag(start string, flags cliFlags) (m *manifest.Manifest, root string, abort bool) {
	m, mdiags, loadErr := manifest.LoadDir(start)
	if loadErr != nil {
		fmt.Fprintf(os.Stderr, "osty: %v\n", loadErr)
		return nil, "", true
	}
	if m != nil && m.Path() != "" {
		root = filepath.Dir(m.Path())
	} else if r, err := manifest.FindRoot(start); err == nil {
		root = r
	} else {
		root = start
	}
	// Render diagnostics through the shared formatter. Source bytes
	// (when present) give us caret underlines that point at the right
	// TOML line; without them the formatter falls back to the header
	// + hint only.
	if len(mdiags) > 0 {
		filename := filepath.Join(root, manifest.ManifestFile)
		var src []byte
		if m != nil {
			src = m.Source()
		}
		f := newFormatter(filename, src, flags)
		printDiags(f, mdiags, flags)
	}
	if m == nil {
		return nil, root, true
	}
	for _, d := range mdiags {
		if d.Severity == diag.Error {
			return m, root, true
		}
	}
	return m, root, false
}

// resolveOpts bundles the toggles the package-manager flow accepts.
// Threaded through every CLI subcommand that touches the resolver
// (build, run, test, fetch, publish, add, update) so behavior stays
// consistent.
type resolveOpts struct {
	// Offline forbids any network or fresh-fetch work. Cached
	// downloads still satisfy registry deps.
	Offline bool
	// Locked refuses to *write* a different lockfile than the one
	// already on disk. The resolve still runs; if the result would
	// alter osty.lock, the caller errors out instead of overwriting.
	// Mirrors `cargo --locked`.
	Locked bool
	// Frozen implies Locked AND Offline AND additionally requires
	// that osty.lock already exist. Mirrors `cargo --frozen`.
	Frozen bool
}

func resolveAndVendorEnvOpts(m *manifest.Manifest, root string, opts resolveOpts) (*pkgmgr.Graph, *pkgmgr.Env, error) {
	if m == nil {
		return nil, nil, fmt.Errorf("nil manifest")
	}
	if opts.Frozen {
		opts.Locked = true
		opts.Offline = true
	}
	env, err := pkgmgr.DefaultEnv(root)
	if err != nil {
		return nil, nil, err
	}
	env.Offline = opts.Offline
	for _, r := range m.Registries {
		env.Registries[r.Name] = r.URL
	}
	// --frozen requires an existing lockfile so CI fails fast on a
	// fresh checkout that forgot to commit one.
	priorLock, _ := lockfile.Read(root)
	if opts.Frozen && priorLock == nil {
		return nil, env, fmt.Errorf("--frozen requires an existing %s; run `osty update` first", lockfile.LockFile)
	}
	if len(m.Dependencies) == 0 && len(m.DevDependencies) == 0 {
		return &pkgmgr.Graph{Root: m}, env, nil
	}
	graph, err := pkgmgr.Resolve(context.Background(), m, env)
	if err != nil {
		return nil, env, fmt.Errorf("resolve: %w", err)
	}
	newLock := pkgmgr.LockFromGraph(graph)
	if opts.Locked {
		if changes := pkgmgr.DiffLock(priorLock, newLock); len(changes) > 0 {
			var b strings.Builder
			fmt.Fprintf(&b, "--locked: %s would change:\n", lockfile.LockFile)
			for _, c := range changes {
				fmt.Fprintf(&b, "  %s\n", c.String())
			}
			fmt.Fprintf(&b, "rerun without --locked to update the lockfile.")
			return graph, env, fmt.Errorf("%s", b.String())
		}
	}
	if err := pkgmgr.Vendor(graph, env); err != nil {
		return graph, env, fmt.Errorf("vendor: %w", err)
	}
	if !opts.Locked {
		if err := lockfile.Write(root, newLock); err != nil {
			return graph, env, fmt.Errorf("write lockfile: %w", err)
		}
	}
	return graph, env, nil
}
