package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

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

// resolveAndVendor is the shared package-manager entry point used by
// `osty add`, `osty update`, `osty run`, `osty test`, and `osty
// publish`. It resolves the dependency graph declared by m, vendors
// each node into <root>/.osty/deps/, and writes / refreshes
// osty.lock.
//
// A manifest with no dependencies is a no-op. A manifest with only
// dev-dependencies still triggers resolution because those are
// needed for `osty test`.
func resolveAndVendor(m *manifest.Manifest, root string, offline bool) error {
	_, _, err := resolveAndVendorEnv(m, root, offline)
	return err
}

// resolveAndVendorEnv is the richer variant that returns the graph +
// env so callers needing to attach a DepProvider to their Workspace
// (build / run / test) can reuse the same resolution. An empty deps
// list short-circuits and returns a bare env — the Workspace still
// gets constructed, it just has nothing to look up through the
// provider.
func resolveAndVendorEnv(m *manifest.Manifest, root string, offline bool) (*pkgmgr.Graph, *pkgmgr.Env, error) {
	if m == nil {
		return nil, nil, fmt.Errorf("nil manifest")
	}
	env, err := pkgmgr.DefaultEnv(root)
	if err != nil {
		return nil, nil, err
	}
	env.Offline = offline
	for _, r := range m.Registries {
		env.Registries[r.Name] = r.URL
	}
	if len(m.Dependencies) == 0 && len(m.DevDependencies) == 0 {
		return &pkgmgr.Graph{Root: m}, env, nil
	}
	graph, err := pkgmgr.Resolve(context.Background(), m, env)
	if err != nil {
		return nil, env, fmt.Errorf("resolve: %w", err)
	}
	if err := pkgmgr.Vendor(graph, env); err != nil {
		return graph, env, fmt.Errorf("vendor: %w", err)
	}
	if err := lockfile.Write(root, pkgmgr.LockFromGraph(graph)); err != nil {
		return graph, env, fmt.Errorf("write lockfile: %w", err)
	}
	return graph, env, nil
}
