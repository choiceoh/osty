package main

import (
	"flag"
	"fmt"
	"os"
	"sort"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/manifest"
	"github.com/osty/osty/internal/pkgmgr"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// runBuild implements the `osty build` subcommand: the manifest-aware
// front-end pipeline end-to-end.
//
//  1. Locate osty.toml (walking up from PATH, default cwd).
//  2. Load + validate the manifest, rendering any E2xxx diagnostics.
//  3. Resolve dependencies (osty.lock is read; regenerated if stale).
//  4. Vendor deps into <project>/.osty/deps/<name>/.
//  5. Run the front-end (parse + resolve + type-check) across the
//     project sources — as a workspace when [workspace] is present,
//     as a single package otherwise.
//
// Exit codes:
//
//	0   manifest + every package is clean
//	1   I/O failure, vendor / lockfile write error, or at least one
//	    package emitted an error-severity diagnostic
//	2   usage error or manifest validation failure
//	3   dependency resolution failure
func runBuild(args []string, flags cliFlags) {
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: osty build [--offline] [PATH]")
	}
	var offline bool
	fs.BoolVar(&offline, "offline", false, "do not fetch dependencies; fail if caches are missing")
	_ = fs.Parse(args)
	start := "."
	if fs.NArg() == 1 {
		start = fs.Arg(0)
	} else if fs.NArg() > 1 {
		fs.Usage()
		os.Exit(2)
	}

	// Step 1+2: manifest load + validate via the shared helper. It
	// walks up to find osty.toml, renders any E2xxx diagnostics with
	// caret underlines, and signals abort when validation fails.
	m, root, abort := loadManifestWithDiag(start, flags)
	if abort {
		os.Exit(2)
	}

	// Step 3–4: dependency resolution + vendoring. Done for both
	// package and workspace manifests so `use` targets inside any
	// contained package find their vendored deps.
	var graph *pkgmgr.Graph
	var env *pkgmgr.Env
	{
		var err error
		graph, env, err = resolveAndVendorEnv(m, root, offline)
		if err != nil {
			fmt.Fprintf(os.Stderr, "osty build: %v\n", err)
			os.Exit(3)
		}
		if graph != nil && len(graph.Nodes) > 0 {
			fmt.Printf("Resolved %d dependencies for %s v%s\n",
				len(graph.Nodes), m.Package.Name, m.Package.Version)
			for _, name := range graph.Order {
				n := graph.Nodes[name]
				if n == nil || n.Fetched == nil {
					continue
				}
				fmt.Printf("  %s %s\t(%s)\n", name, n.Fetched.Version, n.Source.URI())
			}
		}
	}

	// Step 5: front-end pass. Workspaces go through resolve.NewWorkspace
	// so cross-member `use` paths resolve; standalone packages use the
	// single-package loader.
	deps := pkgmgr.NewDepProvider(m, graph, env)
	if m.Workspace != nil {
		buildWorkspace(root, m, flags, deps)
		return
	}
	buildPackage(root, flags, deps)
}

// buildWorkspace runs lex → parse → resolve → check over every member
// of the workspace rooted at dir. Exits non-zero on any error-severity
// diagnostic. deps supplies the Workspace's DepProvider so `use`
// targets to vendored packages resolve.
func buildWorkspace(dir string, m *manifest.Manifest, flags cliFlags, deps resolve.DepProvider) {
	ws, err := resolve.NewWorkspace(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty build: %v\n", err)
		os.Exit(1)
	}
	ws.Stdlib = stdlib.Load()
	ws.Deps = deps
	if m.HasPackage {
		_, _ = ws.LoadPackage("")
	}
	for _, mem := range m.Workspace.Members {
		if _, err := ws.LoadPackage(mem); err != nil {
			fmt.Fprintf(os.Stderr, "osty build: member %s: %v\n", mem, err)
			os.Exit(1)
		}
	}
	results := ws.ResolveAll()
	checks := check.Workspace(ws, results)
	paths := make([]string, 0, len(ws.Packages))
	for p := range ws.Packages {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	anyErr := false
	for _, p := range paths {
		pkg := ws.Packages[p]
		r, ok := results[p]
		if !ok || pkg == nil {
			continue
		}
		ds := append([]*diag.Diagnostic{}, r.Diags...)
		if cr, ok := checks[p]; ok && cr != nil {
			ds = append(ds, cr.Diags...)
		}
		printPackageDiags(pkg, ds, flags)
		if hasError(ds) {
			anyErr = true
		}
	}
	if anyErr {
		os.Exit(1)
	}
}

// buildPackage runs the front-end over a single-package project. When
// deps is non-nil, we wrap the package in a one-member Workspace so
// `use` references to vendored external deps resolve through the
// DepProvider. The plain resolve.LoadPackage path is kept as a
// fallback for zero-dep projects because it's simpler and has no
// workspace state to carry.
func buildPackage(dir string, flags cliFlags, deps resolve.DepProvider) {
	if deps != nil {
		ws, err := resolve.NewWorkspace(dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "osty build: %v\n", err)
			os.Exit(1)
		}
		ws.Stdlib = stdlib.Load()
		ws.Deps = deps
		if _, err := ws.LoadPackage(""); err != nil {
			fmt.Fprintf(os.Stderr, "osty build: %v\n", err)
			os.Exit(1)
		}
		results := ws.ResolveAll()
		checks := check.Workspace(ws, results)
		for key, pkg := range ws.Packages {
			r := results[key]
			if r == nil || pkg == nil {
				continue
			}
			ds := append([]*diag.Diagnostic{}, r.Diags...)
			if cr, ok := checks[key]; ok && cr != nil {
				ds = append(ds, cr.Diags...)
			}
			printPackageDiags(pkg, ds, flags)
			if hasError(ds) {
				os.Exit(1)
			}
		}
		return
	}
	pkg, err := resolve.LoadPackage(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty build: %v\n", err)
		os.Exit(1)
	}
	res := resolve.ResolvePackage(pkg, resolve.NewPrelude())
	chk := check.Package(pkg, res)
	ds := append(append([]*diag.Diagnostic{}, res.Diags...), chk.Diags...)
	printPackageDiags(pkg, ds, flags)
	if hasError(ds) {
		os.Exit(1)
	}
}

// (renderManifestDiags was superseded by loadManifestWithDiag in
// pkg_helpers.go — it now handles both load and rendering.)
