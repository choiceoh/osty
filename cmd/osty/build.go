package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/osty/osty/internal/backend"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/manifest"
	"github.com/osty/osty/internal/pkgmgr"
	"github.com/osty/osty/internal/profile"
	ostyquery "github.com/osty/osty/internal/query/osty"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/runner"
	"github.com/osty/osty/internal/stdlib"
	"github.com/osty/osty/internal/toolchain"
)

// runBuild implements the `osty build` subcommand: the manifest-aware
// project pipeline end-to-end.
//
//  1. Locate osty.toml (walking up from PATH, default cwd).
//  2. Load + validate the manifest, rendering any E2xxx diagnostics.
//  3. Resolve dependencies (osty.lock is read; regenerated if stale).
//  4. Vendor deps into <project>/.osty/deps/<name>/.
//  5. Seed the query graph with project sources, then run the front-end
//     (parse + resolve + type-check + lint) across the graph — as a workspace
//     when [workspace] is present, as a single package otherwise.
//  6. Emit the selected backend artifact through the graph's Emit query
//     (LLVM IR/object/binary) under .osty/out/<profile>[-<target>]/<backend>/.
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
		fmt.Fprintln(os.Stderr, "usage: osty build [--offline | --locked | --frozen] [--profile NAME | --release] [--target TRIPLE] [--features LIST] [--no-default-features] [--backend NAME] [--emit MODE] [--force] [--airepair=false] [--airepair-mode MODE] [PATH]")
	}
	var offline, force, locked, frozen bool
	fs.BoolVar(&offline, "offline", false, "do not fetch dependencies; fail if caches are missing")
	fs.BoolVar(&locked, "locked", false, "fail if osty.lock would change")
	fs.BoolVar(&frozen, "frozen", false, "imply --locked --offline; require an existing osty.lock")
	fs.BoolVar(&force, "force", false, "ignore the build cache; rebuild every input")
	var aiRepairModeName string
	registerAIRepairCommandFlags(fs, &flags.aiRepair, &aiRepairModeName)
	var backendName string
	var emitName string
	fs.StringVar(&backendName, "backend", defaultBackendName(), "code generation backend (llvm, onb)")
	fs.StringVar(&emitName, "emit", "", "artifact mode (llvm-ir, object, or binary)")
	var pf profileFlags
	pf.register(fs)
	_ = fs.Parse(args)
	mode, ok := parseAIRepairMode(aiRepairModeName)
	if !ok {
		fmt.Fprintf(os.Stderr, "osty build: unknown airepair mode %q (want auto, rewrite, parse, or frontend)\n", aiRepairModeName)
		os.Exit(2)
	}
	flags.aiMode = mode
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

	// Turn on the native-checker cache for this process. First
	// invocation warms the cache; every subsequent `osty build` with
	// unchanged package bytes hits it and skips the multi-second
	// checker round-trip. No-op under OSTY_CHECKER_CACHE=0.
	enableCheckerCacheForRoot(root)

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

// countLowerableFiles reports how many package files can enter the legacy IR
// package-lowering boundary. Native-owned packages carry Run with File left nil
// until that boundary explicitly materializes public AST compatibility, so
// counting only pf.File would incorrectly route compile fallbacks through the
// synthetic single-file path.
func countLowerableFiles(pkg *resolve.Package) int {
	if pkg == nil {
		return 0
	}
	n := 0
	for _, pf := range pkg.Files {
		if pf.CanMaterializeFile() {
			n++
		}
	}
	return n
}

// toolVersion returns a stamp used to invalidate the cache when the
// compiler itself changes. Today it's a compile-time constant;
// future wiring (set via -ldflags during release builds) will
// substitute a git sha.
func toolVersion() string {
	return toolchain.Version()
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
	ws.SourceTransform = aiRepairSourceTransform("osty build --airepair", os.Stderr, flags)
	ws.Stdlib = stdlib.Load()
	ws.Deps = deps
	if m.HasPackage {
		_, _ = ws.LoadPackageNative("")
	}
	for _, mem := range m.Workspace.Members {
		if _, err := ws.LoadPackageNative(mem); err != nil {
			fmt.Fprintf(os.Stderr, "osty build: member %s: %v\n", mem, err)
			os.Exit(1)
		}
	}
	eng, seeded := seedBuildWorkspaceEngine(ws)
	printBuildWorkspaceQueryDiags(eng, seeded, flags)

	// Emit the root binary package (if any). Library members fall out of the
	// binary emit for now — multi-target workspace builds are tracked as
	// emitter/backend parity work.
	if m.HasPackage {
		rootDir := ostyquery.NormalizePath(dir)
		rw := eng.Queries.ResolveWorkspace.Get(eng.DB, seeded.Root)
		if rw != nil && rw.PackageByDir(rootDir) != nil {
			return emitAndBuildViaQuery(dir, m, eng, ostyquery.LowerKey{
				WorkspaceRoot: seeded.Root,
				Dir:           rootDir,
			}, resolved, feats, backendID, emitMode)
		}
	}
	return nil
}

// buildPackage runs the front-end over a single-package project and then drives
// the selected backend for the binary entry point.
// When deps is non-nil, we wrap the package in a one-member Workspace
// so `use` references to vendored external deps resolve through the
// DepProvider. The zero-dep path uses the same native package loader without
// workspace state.
func buildPackage(dir string, m *manifest.Manifest, flags cliFlags, deps resolve.DepProvider, resolved *profile.Resolved, feats map[string]bool, backendID backend.Name, emitMode backend.EmitMode) *backend.Result {
	if deps != nil {
		ws, err := resolve.NewWorkspace(dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "osty build: %v\n", err)
			os.Exit(1)
		}
		ws.SourceTransform = aiRepairSourceTransform("osty build --airepair", os.Stderr, flags)
		ws.Stdlib = stdlib.Load()
		ws.Deps = deps
		if _, err := ws.LoadPackageNative(""); err != nil {
			fmt.Fprintf(os.Stderr, "osty build: %v\n", err)
			os.Exit(1)
		}
		eng, seeded := seedBuildWorkspaceEngine(ws)
		printBuildWorkspaceQueryDiags(eng, seeded, flags)
		rootDir := ostyquery.NormalizePath(dir)
		rw := eng.Queries.ResolveWorkspace.Get(eng.DB, seeded.Root)
		if rw != nil && rw.PackageByDir(rootDir) != nil {
			return emitAndBuildViaQuery(dir, m, eng, ostyquery.LowerKey{
				WorkspaceRoot: seeded.Root,
				Dir:           rootDir,
			}, resolved, feats, backendID, emitMode)
		}
		return nil
	}
	eng := ostyquery.NewEngine()
	seeded, err := eng.SeedPackageDir(dir, aiRepairSourceTransform("osty build --airepair", os.Stderr, flags))
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty build: %v\n", err)
		os.Exit(1)
	}
	rp := eng.Queries.ResolvePackage.Get(eng.DB, seeded.Dir)
	chk := eng.Queries.CheckPackage.Get(eng.DB, seeded.Dir)
	var pkg *resolve.Package
	var res *resolve.PackageResult
	if rp != nil {
		pkg = rp.Package()
		res = rp.PackageResult()
	}
	ds := append([]*diag.Diagnostic{}, resDiags(res)...)
	if chk != nil {
		ds = append(ds, chk.Diags...)
	}
	printPackageDiags(pkg, ds, flags)
	if hasError(ds) {
		os.Exit(1)
	}
	return emitAndBuildViaQuery(dir, m, eng, ostyquery.LowerKey{
		Dir: seeded.Dir,
	}, resolved, feats, backendID, emitMode)
}

func seedBuildWorkspaceEngine(ws *resolve.Workspace) (*ostyquery.Engine, ostyquery.SeededWorkspace) {
	eng := ostyquery.NewEngine()
	seeded, err := eng.SeedLoadedWorkspace(ws)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty build: %v\n", err)
		os.Exit(1)
	}
	return eng, seeded
}

func printBuildWorkspaceQueryDiags(eng *ostyquery.Engine, seeded ostyquery.SeededWorkspace, flags cliFlags) {
	rw := eng.Queries.ResolveWorkspace.Get(eng.DB, seeded.Root)
	cw := eng.Queries.CheckWorkspace.Get(eng.DB, seeded.Root)
	if rw == nil {
		return
	}
	anyErr := false
	for _, member := range seeded.Packages {
		pkg := rw.PackageByDir(member.Dir)
		if pkg == nil {
			continue
		}
		var ds []*diag.Diagnostic
		if pr := rw.ResultByDir(member.Dir); pr != nil {
			ds = append(ds, pr.Diags...)
		}
		if cw != nil {
			if cr := cw.ResultByDir(member.Dir); cr != nil {
				ds = append(ds, cr.Diags...)
			}
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

func resDiags(res *resolve.PackageResult) []*diag.Diagnostic {
	if res == nil {
		return nil
	}
	return res.Diags
}

func emitAndBuildViaQuery(root string, m *manifest.Manifest, eng *ostyquery.Engine, lower ostyquery.LowerKey, resolved *profile.Resolved, feats map[string]bool, backendID backend.Name, emitMode backend.EmitMode) *backend.Result {
	profileName, triple := resolvedKey(resolved)
	binName := buildBinaryNameForEmit(m, resolved, triple, emitMode)
	emitResult := emitViaQuery("build", root, m, eng, lower, resolved, feats, backendID, emitMode, binName)
	if emitResult == nil {
		return nil
	}
	return finishBuildEmitResult(backendID, emitMode, emitResult, profileName)
}

func emitViaQuery(command string, root string, m *manifest.Manifest, eng *ostyquery.Engine, lower ostyquery.LowerKey, resolved *profile.Resolved, feats map[string]bool, backendID backend.Name, emitMode backend.EmitMode, binName string) *backend.Result {
	commandName := "osty " + command
	entryRel := "main.osty"
	if m != nil && m.Bin != nil && m.Bin.Path != "" {
		entryRel = m.Bin.Path
	}
	entryAbs := filepath.Join(root, entryRel)
	if _, err := os.Stat(entryAbs); err != nil {
		if m != nil && m.Bin != nil && m.Bin.Path != "" {
			fmt.Fprintf(os.Stderr, "%s: entry %s not found: %v\n", commandName, entryRel, err)
			os.Exit(1)
		}
		return nil
	}
	if skipped, reason := fileIsFeatureGated(entryAbs, feats); skipped {
		fmt.Fprintf(os.Stderr, "%s: skipping %s (feature %q not enabled)\n",
			commandName,
			entryRel, reason)
		return nil
	}

	pkg := queryPackageForLowerKey(eng, lower)
	if pkg == nil {
		fmt.Fprintf(os.Stderr, "%s: package %s not in query graph\n", commandName, lower.Dir)
		os.Exit(1)
	}
	absEntry, _ := filepath.Abs(entryAbs)
	entryInPackage := false
	for _, pf := range pkg.Files {
		if pf == nil {
			continue
		}
		if fp, _ := filepath.Abs(pf.Path); fp == absEntry {
			entryInPackage = true
			break
		}
	}
	if !entryInPackage {
		fmt.Fprintf(os.Stderr, "%s: entry %s not in package\n", commandName, entryRel)
		os.Exit(1)
	}

	profileName, triple := resolvedKey(resolved)
	layout := backend.Layout{
		Root:    root,
		Profile: profileName,
		Target:  triple,
	}
	features := resolvedFeatures(resolved)
	linkLibraries := resolvedLinkLibraries(resolved)

	if backendID == backend.NameLLVM {
		if emitResult, usedExternal, err := tryExternalPackageLLVMArtifacts(context.Background(), emitMode, layout, binName, features, linkLibraries, entryAbs, pkg); usedExternal {
			if err != nil {
				exitBackendEmitError(command, emitResult, err)
			}
			return emitResult
		}
	}

	lower.PackageName = "main"
	lower.SourcePath = entryAbs
	lower.EntryPath = entryAbs
	target := ostyquery.NewEmitTarget(lower, backendID, emitMode, layout, binName, features).
		WithLinkLibraries(linkLibraries)
	emitted := eng.Queries.Emit.Get(eng.DB, target)
	if emitted.Err != nil {
		exitBackendEmitError(command, emitted.Result, emitted.Err)
	}
	return emitted.Result
}

func queryPackageForLowerKey(eng *ostyquery.Engine, lower ostyquery.LowerKey) *resolve.Package {
	if lower.WorkspaceRoot != "" {
		lower.WorkspaceRoot = ostyquery.NormalizePath(lower.WorkspaceRoot)
	}
	lower.Dir = ostyquery.NormalizePath(lower.Dir)
	if lower.WorkspaceRoot != "" {
		rw := eng.Queries.ResolveWorkspace.Get(eng.DB, lower.WorkspaceRoot)
		if rw == nil {
			return nil
		}
		return rw.PackageByDir(lower.Dir)
	}
	rp := eng.Queries.ResolvePackage.Get(eng.DB, lower.Dir)
	if rp == nil {
		return nil
	}
	return rp.Package()
}

func buildBinaryNameForEmit(m *manifest.Manifest, resolved *profile.Resolved, triple string, emitMode backend.EmitMode) string {
	if emitMode != backend.EmitBinary {
		return ""
	}
	binBaseOverride := ""
	pkgName := ""
	if m != nil {
		if m.Bin != nil {
			binBaseOverride = m.Bin.Name
		}
		pkgName = m.Package.Name
	}
	targetOS := ""
	if resolved != nil && resolved.Target != nil {
		targetOS = resolved.Target.OS
	}
	return runner.BuildBinaryName(binBaseOverride, pkgName, triple, targetOS, runtime.GOOS)
}

func resolvedFeatures(r *profile.Resolved) []string {
	if r == nil {
		return nil
	}
	return r.Features
}

func resolvedLinkLibraries(r *profile.Resolved) []string {
	if r == nil || r.Target == nil || len(r.Target.Link) == 0 {
		return nil
	}
	return append([]string(nil), r.Target.Link...)
}

func finishBuildEmitResult(backendID backend.Name, emitMode backend.EmitMode, emitResult *backend.Result, profileName string) *backend.Result {
	if emitResult == nil {
		fmt.Fprintf(os.Stderr, "osty build: backend %q emit %q did not produce a buildable artifact\n", backendID, emitMode)
		os.Exit(1)
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
