package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"

	"github.com/osty/osty/internal/backend"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/manifest"
	"github.com/osty/osty/internal/pkgmgr"
	"github.com/osty/osty/internal/profile"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// runBuild implements the `osty build` subcommand: the manifest-aware
// project pipeline end-to-end.
//
//  1. Locate osty.toml (walking up from PATH, default cwd).
//  2. Load + validate the manifest, rendering any E2xxx diagnostics.
//  3. Resolve dependencies (osty.lock is read; regenerated if stale).
//  4. Vendor deps into <project>/.osty/deps/<name>/.
//  5. Run the front-end (parse + resolve + type-check + lint) across
//     the project sources — as a workspace when [workspace] is present,
//     as a single package otherwise.
//  6. Emit the selected backend artifact (LLVM IR/object/binary)
//     under .osty/out/<profile>[-<target>]/<backend>/.
//  7. Record a backend-aware fingerprint under .osty/cache/ so an
//     unchanged build can skip the front-end and backend work.
//
// Exit codes:
//
//	0   manifest + every package is clean and requested artifacts were produced
//	1   I/O failure, vendor / lockfile write error, or at least one
//	    package emitted an error-severity diagnostic
//	2   usage error or manifest validation failure
//	3   dependency resolution failure
func runBuild(args []string, flags cliFlags) {
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: osty build [--offline | --locked | --frozen] [--profile NAME | --release] [--target TRIPLE] [--features LIST] [--no-default-features] [--backend NAME] [--emit MODE] [--force] [PATH]")
	}
	var offline, force, locked, frozen bool
	fs.BoolVar(&offline, "offline", false, "do not fetch dependencies; fail if caches are missing")
	fs.BoolVar(&locked, "locked", false, "fail if osty.lock would change")
	fs.BoolVar(&frozen, "frozen", false, "imply --locked --offline; require an existing osty.lock")
	fs.BoolVar(&force, "force", false, "ignore the build cache; rebuild every input")
	var backendName string
	var emitName string
	fs.StringVar(&backendName, "backend", defaultBackendName(), "code generation backend (llvm)")
	fs.StringVar(&emitName, "emit", "", "artifact mode (llvm-ir, object, or binary)")
	var pf profileFlags
	pf.register(fs)
	_ = fs.Parse(args)
	backendID, emitMode := resolveBackendAndEmitFlags("build", backendName, emitName)
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
	if !force && cacheableBuildEmit(backendID, emitMode) {
		if fp, err := profile.ReadFingerprintForBackend(root, profileName, triple, backendID.String()); err == nil && fp != nil {
			curSrc, err := profile.HashSources(root, isOstySource)
			if err == nil {
				augmentSourcesWithProjectFiles(curSrc, root)
				fresh := profile.NewBackendFingerprint(curSrc, resolved, toolVersion(),
					backendID.String(), emitMode.String(), nil)
				if fp.Equal(fresh) && cachedArtifactsExist(root, fp.Artifacts) {
					fmt.Printf("Build is up to date (cache: %s)\n",
						profile.BackendCachePath(root, profileName, triple, backendID.String()))
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
	var emitResult *backend.Result
	if m.Workspace != nil {
		emitResult = buildWorkspace(root, m, flags, deps, resolved, featSet, backendID, emitMode)
	} else {
		emitResult = buildPackage(root, m, flags, deps, resolved, featSet, backendID, emitMode)
	}

	// Step 6: record the build fingerprint under .osty/cache/ so the
	// next invocation can short-circuit on unchanged inputs. A
	// failure to write the fingerprint is logged but doesn't fail
	// the build — correctness is preserved, we just lose the
	// incremental speed-up next time.
	if cacheableBuildEmit(backendID, emitMode) && emitResult != nil {
		if sources, err := profile.HashSources(root, isOstySource); err == nil {
			augmentSourcesWithProjectFiles(sources, root)
			artifacts := fingerprintArtifacts(root, emitResult.Artifacts)
			fp := profile.NewBackendFingerprint(sources, resolved, toolVersion(),
				backendID.String(), emitMode.String(), artifacts)
			if err := fp.Write(root); err != nil {
				fmt.Fprintf(os.Stderr, "osty build: warning: cache write failed: %v\n", err)
			}
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

func cacheableBuildEmit(backendID backend.Name, emitMode backend.EmitMode) bool {
	_ = backendID
	if emitMode == backend.EmitBinary {
		return true
	}
	return emitMode == backend.EmitLLVMIR || emitMode == backend.EmitObject
}

func fingerprintArtifacts(root string, artifacts backend.Artifacts) map[string]string {
	out := map[string]string{}
	add := func(key, path string) {
		if path == "" {
			return
		}
		if _, err := os.Stat(path); err != nil {
			return
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return
		}
		out[key] = filepath.ToSlash(rel)
	}
	add("llvm_ir", artifacts.LLVMIR)
	add("object", artifacts.Object)
	add("binary", artifacts.Binary)
	add("runtime_dir", artifacts.RuntimeDir)
	return out
}

func cachedArtifactsExist(root string, artifacts map[string]string) bool {
	if len(artifacts) == 0 {
		return true
	}
	for _, rel := range artifacts {
		if rel == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			return false
		}
	}
	return true
}

// buildWorkspace runs lex → parse → resolve → check over every member
// of the workspace rooted at dir. Exits non-zero on any error-severity
// diagnostic. deps supplies the Workspace's DepProvider so `use`
// targets to vendored packages resolve. When the root manifest declares
// a binary entry point, it is additionally emitted through the selected
// backend.
func buildWorkspace(dir string, m *manifest.Manifest, flags cliFlags, deps resolve.DepProvider, resolved *profile.Resolved, feats map[string]bool, backendID backend.Name, emitMode backend.EmitMode) *backend.Result {
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
	checks := check.Workspace(ws, results, checkOpts())
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
	// Emit the root binary package (if any). Library members fall out of the
	// binary emit for now — multi-target workspace builds are tracked as
	// emitter/backend parity work.
	if m.HasPackage {
		rootPkg := ws.Packages[""]
		if rootPkg != nil {
			return emitAndBuild(dir, m, rootPkg, results[""], checks[""], resolved, feats, backendID, emitMode)
		}
	}
	return nil
}

// buildPackage runs the front-end over a single-package project and then drives
// the selected backend for the binary entry point.
// When deps is non-nil, we wrap the package in a one-member Workspace
// so `use` references to vendored external deps resolve through the
// DepProvider. The plain resolve.LoadPackage path is kept as a
// fallback for zero-dep projects because it's simpler and has no
// workspace state to carry.
func buildPackage(dir string, m *manifest.Manifest, flags cliFlags, deps resolve.DepProvider, resolved *profile.Resolved, feats map[string]bool, backendID backend.Name, emitMode backend.EmitMode) *backend.Result {
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
		checks := check.Workspace(ws, results, checkOpts())
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
			return emitAndBuild(dir, m, rootPkg, results[""], checks[""], resolved, feats, backendID, emitMode)
		}
		return nil
	}
	pkg, err := resolve.LoadPackage(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty build: %v\n", err)
		os.Exit(1)
	}
	res := resolve.ResolvePackage(pkg, resolve.NewPrelude())
	chk := check.Package(pkg, res, checkOpts())
	ds := append(append([]*diag.Diagnostic{}, res.Diags...), chk.Diags...)
	printPackageDiags(pkg, ds, flags)
	if hasError(ds) {
		os.Exit(1)
	}
	// Note: the no-deps path feeds a synthetic PackageResult because
	// emitAndBuild expects a *resolve.PackageResult with Diags; we
	// already have all of it from ResolvePackage above.
	return emitAndBuild(dir, m, pkg, res, chk, resolved, feats, backendID, emitMode)
}

// emitAndBuild picks the entry file (manifest `[bin].path` or default
// `main.osty`) and drives the selected native backend. Libraries (no entry file
// on disk) are a no-op until the emitter grows package-per-package output.
//
// Files whose header declares `@feature: NAME` via the @feature
// pragma are skipped when NAME isn't in the active feature set, so
// feature-gated modules drop out before backend emission.
//
// A failure at the backend/toolchain step returns a non-zero exit; a gen-time
// TODO marker is only logged so the clean portion remains inspectable.
func emitAndBuild(root string, m *manifest.Manifest, pkg *resolve.Package, pr *resolve.PackageResult, chk *check.Result, resolved *profile.Resolved, feats map[string]bool, backendID backend.Name, emitMode backend.EmitMode) *backend.Result {
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
		return nil
	}
	// 2. Feature-pragma filter: don't emit a file whose pragma
	// requires an inactive feature. If the entry file is gated out
	// the whole build degrades to front-end only.
	if skipped, reason := fileIsFeatureGated(entryAbs, feats); skipped {
		fmt.Fprintf(os.Stderr, "osty build: skipping %s (feature %q not enabled)\n",
			entryRel, reason)
		return nil
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
	profileName, triple := resolvedKey(resolved)
	binName := ""
	if emitMode == backend.EmitBinary {
		binName = binaryName(m)
		if triple != "" {
			binName += "-" + triple
		}
		if runtime.GOOS == "windows" && (triple == "" || resolved.Target == nil || resolved.Target.OS == "windows") {
			binName += ".exe"
		}
	}
	selectedBackend := backendFromCLI("build", backendID)
	emitResult, err := selectedBackend.Emit(context.Background(), backend.Request{
		Layout: backend.Layout{
			Root:    root,
			Profile: profileName,
			Target:  triple,
		},
		Emit: emitMode,
		Entry: backend.Entry{
			PackageName: "main",
			SourcePath:  entryAbs,
			File:        entryFile.File,
			Resolve:     res,
			Check:       chk,
		},
		BinaryName: binName,
		Features:   resolved.Features,
	})
	if err != nil {
		exitBackendEmitError("build", emitResult, err)
	}
	switch emitMode {
	case backend.EmitBinary:
		if emitResult.Artifacts.Binary != "" {
			fmt.Printf("Built %s (%s)\n", emitResult.Artifacts.Binary, profileName)
			return emitResult
		}
	case backend.EmitObject:
		if emitResult.Artifacts.Object != "" {
			fmt.Printf("Generated %s (%s)\n", emitResult.Artifacts.Object, profileName)
			return emitResult
		}
	case backend.EmitLLVMIR:
		if artifact := emitResult.Artifacts.SourcePath(); artifact != "" {
			fmt.Printf("Generated %s (%s)\n", artifact, profileName)
			return emitResult
		}
	}
	fmt.Fprintf(os.Stderr, "osty build: backend %q emit %q did not produce a buildable artifact\n", backendID, emitMode)
	os.Exit(1)
	return nil
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

// (renderManifestDiags was superseded by loadManifestWithDiag in
// pkg_helpers.go — it now handles both load and rendering.)
