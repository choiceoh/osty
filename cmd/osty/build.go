package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/gen"
	"github.com/osty/osty/internal/manifest"
	"github.com/osty/osty/internal/pkgmgr"
	"github.com/osty/osty/internal/profile"
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
		fmt.Fprintln(os.Stderr, "usage: osty build [--offline | --locked | --frozen] [--profile NAME | --release] [--target TRIPLE] [--features LIST] [--no-default-features] [--force] [PATH]")
	}
	var offline, force, locked, frozen bool
	fs.BoolVar(&offline, "offline", false, "do not fetch dependencies; fail if caches are missing")
	fs.BoolVar(&locked, "locked", false, "fail if osty.lock would change")
	fs.BoolVar(&frozen, "frozen", false, "imply --locked --offline; require an existing osty.lock")
	fs.BoolVar(&force, "force", false, "ignore the build cache; transpile every input")
	var pf profileFlags
	pf.register(fs)
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

	// Profile resolution. Errors here come from unknown `--profile`
	// names or malformed `--target` triples and are usage errors.
	resolved, profileName, perr := pf.resolve(m, profile.NameDebug)
	if perr != nil {
		fmt.Fprintf(os.Stderr, "osty build: %v\n", perr)
		os.Exit(2)
	}
	announceProfile(resolved)
	triple := ""
	if resolved.Target != nil {
		triple = resolved.Target.Triple
	}

	// Step 2.5: incremental-build shortcut. Hash every .osty file
	// under the project root and compare against the cached
	// fingerprint. A matching record lets us skip the front-end +
	// gen entirely; --force overrides this.
	if !force {
		if fp, err := profile.ReadFingerprint(root, profileName, triple); err == nil && fp != nil {
			curSrc, err := profile.HashSources(root, isOstySource)
			if err == nil {
				augmentSourcesWithProjectFiles(curSrc, root)
				fresh := profile.NewFingerprint(curSrc, resolved, toolVersion())
				if fp.Equal(fresh) {
					fmt.Printf("Build is up to date (cache: %s)\n",
						profile.CachePath(root, profileName, triple))
					return
				}
			}
		}
	}

	// Step 3–4: dependency resolution + vendoring. Done for both
	// package and workspace manifests so `use` targets inside any
	// contained package find their vendored deps.
	var graph *pkgmgr.Graph
	var env *pkgmgr.Env
	{
		var err error
		graph, env, err = resolveAndVendorEnvOpts(m, root, resolveOpts{
			Offline: offline, Locked: locked, Frozen: frozen,
		})
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
	featSet := featureSet(resolved)
	if m.Workspace != nil {
		buildWorkspace(root, m, flags, deps, resolved, featSet)
	} else {
		buildPackage(root, m, flags, deps, resolved, featSet)
	}

	// Step 6: record the build fingerprint under .osty/cache/ so the
	// next invocation can short-circuit on unchanged inputs. A
	// failure to write the fingerprint is logged but doesn't fail
	// the build — correctness is preserved, we just lose the
	// incremental speed-up next time.
	if sources, err := profile.HashSources(root, isOstySource); err == nil {
		augmentSourcesWithProjectFiles(sources, root)
		fp := profile.NewFingerprint(sources, resolved, toolVersion())
		if err := fp.Write(root); err != nil {
			fmt.Fprintf(os.Stderr, "osty build: warning: cache write failed: %v\n", err)
		}
	}
}

// augmentSourcesWithProjectFiles folds osty.toml + osty.lock hashes
// into the fingerprint map so a dependency bump or profile tweak
// invalidates the cache even when no .osty byte moved. Keys are
// prefixed with ":" so they can never collide with a real path.
func augmentSourcesWithProjectFiles(sources map[string]string, root string) {
	for _, rel := range []string{manifest.ManifestFile, "osty.lock"} {
		p := filepath.Join(root, rel)
		if h, err := profile.HashFile(p); err == nil {
			sources[":"+rel] = h
		}
	}
}

// fileIsFeatureGated reads the file at path, inspects its
// `// @feature: NAME` pragma (if any), and reports whether the file
// should be excluded from the build under the active feature set.
// Returns (skipped, missingFeature). When the file is absent or can't
// be read the function fails safe by including it (skipped=false).
func fileIsFeatureGated(path string, active map[string]bool) (bool, string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, ""
	}
	ok, missing := profile.FileNeedsFeatures(data, active)
	return !ok, missing
}

// featureSet turns the resolved feature list into a map for O(1)
// lookup during the per-file pragma check. Nil resolved or empty
// feature list means "no features active".
func featureSet(r *profile.Resolved) map[string]bool {
	out := map[string]bool{}
	if r == nil {
		return out
	}
	for _, f := range r.Features {
		out[f] = true
	}
	return out
}

// isOstySource is the predicate used by cache fingerprinting. .osty
// files under testdata/ are ordinarily source inputs too, but for
// incremental-build purposes anything ending in .osty counts.
func isOstySource(name string) bool {
	return filepath.Ext(name) == ".osty"
}

// toolVersion returns a stamp used to invalidate the cache when the
// compiler itself changes. Today it's a compile-time constant;
// future wiring (set via -ldflags during release builds) will
// substitute a git sha.
func toolVersion() string {
	return "osty-dev"
}

// buildWorkspace runs lex → parse → resolve → check over every member
// of the workspace rooted at dir. Exits non-zero on any error-severity
// diagnostic. deps supplies the Workspace's DepProvider so `use`
// targets to vendored packages resolve. When the root manifest declares
// a binary entry point, it is additionally transpiled and linked with
// `go build` using the resolved profile's go-flags / env.
func buildWorkspace(dir string, m *manifest.Manifest, flags cliFlags, deps resolve.DepProvider, resolved *profile.Resolved, feats map[string]bool) {
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
	// Transpile + `go build` the root binary package (if any). Library
	// members fall out of the binary emit for now — multi-target
	// workspace builds are tracked as emitter/back-end parity work.
	if m.HasPackage {
		rootPkg := ws.Packages[""]
		if rootPkg != nil {
			emitAndBuild(dir, m, rootPkg, results[""], checks[""], resolved, feats)
		}
	}
}

// buildPackage runs the front-end over a single-package project and
// then drives gen + `go build` for the binary entry point.
// When deps is non-nil, we wrap the package in a one-member Workspace
// so `use` references to vendored external deps resolve through the
// DepProvider. The plain resolve.LoadPackage path is kept as a
// fallback for zero-dep projects because it's simpler and has no
// workspace state to carry.
func buildPackage(dir string, m *manifest.Manifest, flags cliFlags, deps resolve.DepProvider, resolved *profile.Resolved, feats map[string]bool) {
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
		rootPkg := ws.Packages[""]
		if rootPkg != nil {
			emitAndBuild(dir, m, rootPkg, results[""], checks[""], resolved, feats)
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
	// Note: the no-deps path feeds a synthetic PackageResult because
	// emitAndBuild expects a *resolve.PackageResult with Diags; we
	// already have all of it from ResolvePackage above.
	emitAndBuild(dir, m, pkg, res, chk, resolved, feats)
}

// emitAndBuild is the gen + `go build` driver. It picks the entry
// file (manifest `[bin].path` or default `main.osty`), transpiles it
// to Go via gen.Generate, writes the output under
// .osty/out/<profile>[-<triple>]/, and invokes the Go toolchain with
// the resolved profile's go-flags + target env. Libraries (no entry
// file on disk) are a no-op until the emitter grows package-per-package
// output.
//
// Files whose header declares `@feature: NAME` via the @feature
// pragma are skipped when NAME isn't in the active feature set, so
// feature-gated modules drop out before transpile.
//
// A failure at the go-build step returns a non-zero exit; a gen-time
// TODO marker is only logged so the clean portion remains inspectable.
func emitAndBuild(root string, m *manifest.Manifest, pkg *resolve.Package, pr *resolve.PackageResult, chk *check.Result, resolved *profile.Resolved, feats map[string]bool) {
	// 1. Locate the entry file. A library project has no entry;
	// skip the emit path so `osty build` still works as a front-end
	// check for libs.
	entryRel := "main.osty"
	if m != nil && m.Bin != nil && m.Bin.Path != "" {
		entryRel = m.Bin.Path
	}
	entryAbs := filepath.Join(root, entryRel)
	if _, err := os.Stat(entryAbs); err != nil {
		// Library or deferred binary: emit step is a no-op.
		return
	}
	// 2. Feature-pragma filter: don't emit a file whose pragma
	// requires an inactive feature. If the entry file is gated out
	// the whole build degrades to front-end only.
	if skipped, reason := fileIsFeatureGated(entryAbs, feats); skipped {
		fmt.Fprintf(os.Stderr, "osty build: skipping %s (feature %q not enabled)\n",
			entryRel, reason)
		return
	}
	// 3. Find the PackageFile matching entryAbs so we can pass AST +
	// Refs into gen.
	var entryFile *resolve.PackageFile
	absEntry, _ := filepath.Abs(entryAbs)
	for _, pf := range pkg.Files {
		if fp, _ := filepath.Abs(pf.Path); fp == absEntry {
			entryFile = pf
			break
		}
	}
	if entryFile == nil {
		fmt.Fprintf(os.Stderr, "osty build: entry %s not in package\n", entryRel)
		os.Exit(1)
	}
	// 4. Transpile. Unsupported lowering shapes produce TODO markers;
	// we log the warning but proceed so simple programs build.
	res := &resolve.Result{
		Refs:      entryFile.Refs,
		TypeRefs:  entryFile.TypeRefs,
		FileScope: entryFile.FileScope,
	}
	if chk == nil {
		chk = &check.Result{}
	}
	goSrc, gerr := gen.GenerateMapped("main", entryFile.File, res, chk, entryAbs)
	// 5. Write the generated Go into the profile-scoped out dir.
	profileName, triple := resolvedKey(resolved)
	outDir := profile.OutputDir(root, profileName, triple)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "osty build: %v\n", err)
		os.Exit(1)
	}
	goSrc = prependBuildConstraint(goSrc, resolved)
	goPath := filepath.Join(outDir, "main.go")
	if err := os.WriteFile(goPath, goSrc, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "osty build: %v\n", err)
		os.Exit(1)
	}
	reportTranspileWarning("osty build", entryAbs, goPath, gerr)
	// 6. Invoke `go build -o <bin>` with profile flags + target env.
	binName := binaryName(m)
	if triple != "" {
		binName += "-" + triple
	}
	if runtime.GOOS == "windows" && (triple == "" || resolved.Target == nil || resolved.Target.OS == "windows") {
		binName += ".exe"
	}
	binPath := filepath.Join(outDir, binName)
	buildArgs := []string{"build"}
	buildArgs = append(buildArgs, resolved.GoFlags()...)
	buildArgs = append(buildArgs, "-o", binPath, goPath)
	cmd := exec.Command("go", buildArgs...)
	var stderr bytes.Buffer
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderr)
	cmd.Dir = outDir
	cmd.Env = mergeEnv(os.Environ(), resolved.GoEnv())
	if err := cmd.Run(); err != nil {
		reportGoFailure(goFailureReport{
			Tool:      "osty build",
			Action:    "go build",
			Args:      cmd.Args,
			WorkDir:   outDir,
			Generated: []string{goPath},
			Source:    entryAbs,
			Stderr:    stderr.String(),
			Err:       err,
		})
		fmt.Fprintf(os.Stderr, "osty build: go build: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Built %s (%s)\n", binPath, profileName)
}

// binaryName returns the binary name for the package: the manifest's
// [bin].name override, the package name, or "app" as a last-resort
// default.
func binaryName(m *manifest.Manifest) string {
	if m != nil && m.Bin != nil && m.Bin.Name != "" {
		return m.Bin.Name
	}
	if m != nil && m.Package.Name != "" {
		return m.Package.Name
	}
	return "app"
}

// resolvedKey unpacks a Resolved into (profile name, triple) so the
// common out-dir / cache-key computations don't each have to
// nil-check Target.
func resolvedKey(r *profile.Resolved) (string, string) {
	name := ""
	triple := ""
	if r != nil {
		if r.Profile != nil {
			name = r.Profile.Name
		}
		if r.Target != nil {
			triple = r.Target.Triple
		}
	}
	return name, triple
}

// prependBuildConstraint injects any //go:build constraints that the
// active feature set implies. Go's tooling already consumes the
// `-tags=feat_*` flag we attach in Resolved.GoFlags, so the constraint
// here is a belt-and-suspenders — downstream tooling (IDE builders,
// gopls) that doesn't see our -tags flag still treats the file
// correctly.
func prependBuildConstraint(src []byte, r *profile.Resolved) []byte {
	if r == nil || len(r.Features) == 0 {
		return src
	}
	constraints := make([]string, 0, len(r.Features))
	for _, f := range r.Features {
		constraints = append(constraints, "feat_"+f)
	}
	sort.Strings(constraints)
	header := "//go:build " + join(constraints, " && ") + "\n\n"
	return append([]byte(header), src...)
}

// join is a small helper to avoid importing strings in the narrow
// prepend path (keeping the import list minimal elsewhere).
func join(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for i := 1; i < len(parts); i++ {
		out += sep + parts[i]
	}
	return out
}

// (renderManifestDiags was superseded by loadManifestWithDiag in
// pkg_helpers.go — it now handles both load and rendering.)
