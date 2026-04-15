package main

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/manifest"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
	"github.com/osty/osty/internal/testgen"
)

// runTest implements `osty test [--offline] [PATH|FILTER...]` per
// LANG_SPEC_v0.3 §11.
//
// Two invocation shapes are supported:
//
//   - `osty test` inside a project with an `osty.toml`: walks the
//     project tree for `*_test.osty` files, groups them by containing
//     directory, runs the full front-end pipeline per package (with
//     test files included so they can reference sibling non-test
//     declarations, per §11), and reports discovered `test*` /
//     `bench*` functions per file.
//
//   - `osty test PATH` where PATH is a single `.osty` file or a bare
//     directory (no manifest required): runs the same pipeline but
//     scoped to just that file or directory. Useful for ad-hoc testing
//     outside a project.
//
// Positional arguments that are neither an existing file nor an
// existing directory are treated as substring filters on the test
// file's basename — so `osty test auth` matches `auth_test.osty` and
// `user_auth_test.osty`, preserving the previous behaviour.
//
// Discovery rules (spec §11 / §11.4):
//
//   - `test*` top-level fns with no parameters, no receiver, no
//     generics, and unit return type are tests;
//   - `bench*` top-level fns with the same shape are benchmarks;
//   - every other declaration is ignored for discovery purposes.
//
// Test execution is driven by the internal/testgen package: after
// the front-end validates a package, gen transpiles every source
// file to Go, testgen merges them, writes an auto-generated harness
// alongside, and executes a cached test binary. Assertion failures
// surface as FAIL lines and the overall exit code reflects the
// combined verdict.
//
// Exit codes:
//
//	0   every discovered test passed
//	1   at least one test failed OR a front-end error was emitted
//	2   usage error or manifest / I/O failure
func runTest(args []string, flags cliFlags) {
	fs := flag.NewFlagSet("test", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: osty test [--offline | --locked | --frozen] [PATH|FILTER...]")
	}
	var offline, locked, frozen bool
	fs.BoolVar(&offline, "offline", false, "do not fetch dependencies; fail if caches are missing")
	fs.BoolVar(&locked, "locked", false, "fail if osty.lock would change")
	fs.BoolVar(&frozen, "frozen", false, "imply --locked --offline; require an existing osty.lock")
	var pf profileFlags
	pf.register(fs)
	_ = fs.Parse(args)
	positional := fs.Args()

	// Split positional args into path targets (existing on disk) and
	// name filters (everything else). Paths shortcut the manifest walk
	// so `osty test ./foo.osty` works without an osty.toml.
	var explicitPaths []string
	var filters []string
	for _, a := range positional {
		if _, err := os.Stat(a); err == nil {
			explicitPaths = append(explicitPaths, a)
		} else {
			filters = append(filters, a)
		}
	}

	// Locate a manifest when running without explicit paths. When a
	// manifest exists we walk the whole project and run dependency
	// vendoring; when absent, we fall back to the cwd for test
	// discovery so ad-hoc layouts still work.
	var root string
	var man *manifest.Manifest
	if _, err := manifest.FindRoot("."); err == nil {
		m, mroot, abort := loadManifestWithDiag(".", flags)
		if abort {
			os.Exit(2)
		}
		root = mroot
		man = m
	} else {
		abs, absErr := filepath.Abs(".")
		if absErr != nil {
			fmt.Fprintf(os.Stderr, "osty test: %v\n", absErr)
			os.Exit(2)
		}
		root = abs
	}

	// Vendor deps when a manifest exists and declares any. No-op
	// otherwise — the resolver still works without [dependencies].
	if man != nil {
		if _, _, err := resolveAndVendorEnvOpts(man, root, resolveOpts{
			Offline: offline, Locked: locked, Frozen: frozen,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "osty test: %v\n", err)
			os.Exit(2)
		}
		// Resolve the effective profile so feature gating + target
		// metadata surface here too. `osty test` defaults to the
		// built-in `test` profile unless the user picks another with
		// --profile.
		resolved, _, perr := pf.resolve(man, profileTestFallback)
		if perr != nil {
			fmt.Fprintf(os.Stderr, "osty test: %v\n", perr)
			os.Exit(2)
		}
		announceProfile(resolved)
	}

	// Collect test files: explicit positional paths if given; otherwise
	// a recursive walk of the project root. Directories in explicit
	// paths expand to every `*_test.osty` beneath them so `osty test
	// ./internal/` works.
	testFiles, collectErr := collectTestTargets(root, explicitPaths)
	if collectErr != nil {
		fmt.Fprintf(os.Stderr, "osty test: %v\n", collectErr)
		os.Exit(2)
	}
	if len(filters) > 0 {
		testFiles = filterTestFiles(testFiles, filters)
	}
	if len(testFiles) == 0 {
		fmt.Println("No test files found (*_test.osty).")
		return
	}

	// Group test files by their containing directory so each package
	// resolves as a unit — test files in the same directory share the
	// namespace of every non-test file beside them, per §11.
	byDir := make(map[string][]string)
	for _, p := range testFiles {
		byDir[filepath.Dir(p)] = append(byDir[filepath.Dir(p)], p)
	}
	dirs := make([]string, 0, len(byDir))
	for d := range byDir {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)

	anyErr := false
	passed := 0
	totalTests, totalBenches := 0, 0
	totalPass, totalFail := 0, 0
	for _, dir := range dirs {
		ok, t, b, runPass, runFail := runTestDir(dir, byDir[dir], root, flags)
		if !ok {
			anyErr = true
			continue
		}
		passed += len(byDir[dir])
		totalTests += t
		totalBenches += b
		totalPass += runPass
		totalFail += runFail
	}

	fmt.Printf("\n%d / %d test file(s) validated — %d tests, %d benchmarks discovered\n",
		passed, len(testFiles), totalTests, totalBenches)
	fmt.Printf("execution: %d passed, %d failed\n", totalPass, totalFail)

	if anyErr || totalFail > 0 {
		os.Exit(1)
	}
}

// runTestDir runs the full front-end pipeline over one package
// directory and emits per-file results. interesting is the subset of
// files the user asked about (explicit paths or the walker's
// *_test.osty match); every .osty file in dir — including non-test
// files — is loaded for resolution so cross-file refs work.
//
// Returns (ok, totalTests, totalBenchmarks, runPass, runFail). ok is
// false when any error-severity diagnostic was printed for this
// package; runPass/runFail come from the compiled harness's own
// pass/fail counts (zero when the front-end failed and execution was
// skipped, or when code generation hit a construct gen can't emit).
func runTestDir(dir string, interesting []string, root string, flags cliFlags) (bool, int, int, int, int) {
	pkg, err := resolve.LoadPackageWithTests(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty test: %v\n", err)
		return false, 0, 0, 0, 0
	}

	pr := resolveTestPackage(dir, pkg)
	chk := check.Package(pkg, pr, checkOpts())

	all := append([]*diag.Diagnostic{}, pr.Diags...)
	all = append(all, chk.Diags...)

	// Render package-level diagnostics once so the user sees every
	// error with the correct source snippet. Individual file status is
	// still printed afterwards so the pass/fail summary stays legible.
	if len(all) > 0 {
		printPackageDiags(pkg, all, flags)
	}

	interestingSet := map[string]bool{}
	for _, p := range interesting {
		abs, _ := filepath.Abs(p)
		interestingSet[abs] = true
	}

	// Per-file report: the user supplied `*_test.osty` files (or a dir
	// containing them); emit one status line per interesting file plus
	// an indented list of discovered test/bench functions.
	fileOK := true
	totalTests, totalBenches := 0, 0
	var allEntries []testgen.Entry
	// Order the output by file path so runs against the same tree are
	// deterministic regardless of filesystem walk order.
	sorted := make([]*resolve.PackageFile, 0, len(pkg.Files))
	sorted = append(sorted, pkg.Files...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Path < sorted[j].Path
	})
	for _, pf := range sorted {
		abs, _ := filepath.Abs(pf.Path)
		if !interestingSet[abs] {
			continue
		}
		rel, err := filepath.Rel(root, pf.Path)
		if err != nil {
			rel = pf.Path
		}
		fileHasErr := fileHasErrors(pf, all)
		if fileHasErr {
			fmt.Printf("FAIL  %s\n", rel)
			fileOK = false
			continue
		}
		entries := discoverTests(pf.File, pf.Path)
		tests, benches := 0, 0
		for _, e := range entries {
			if e.kind == kindBench {
				benches++
			} else {
				tests++
			}
			allEntries = append(allEntries, testgen.Entry{
				Name: e.name,
				Kind: testgenKind(e.kind),
				File: filepath.Base(pf.Path),
				Line: e.line,
			})
		}
		totalTests += tests
		totalBenches += benches
		fmt.Printf("ok    %s  (%d tests, %d benchmarks)\n", rel, tests, benches)
		for _, e := range entries {
			fmt.Printf("        %-6s %-32s (line %d)\n",
				e.kind.label(), e.name, e.line)
		}
	}

	// Execute if the package validated cleanly and we found at least
	// one discoverable entry. Front-end failures short-circuit
	// execution — running a package that didn't type-check would
	// just surface confusing Go-compile errors downstream.
	runPass, runFail := 0, 0
	if fileOK && len(allEntries) > 0 {
		var execErr error
		runPass, runFail, execErr = executeTestPackage(dir, pkg, chk, allEntries, root)
		if execErr != nil {
			fmt.Fprintf(os.Stderr, "osty test: %v\n", execErr)
			fileOK = false
		}
	}
	return fileOK, totalTests, totalBenches, runPass, runFail
}

// testgenKind converts the cmd-local testKind to the testgen Entry
// kind. Kept as a tiny helper so cmd/osty doesn't have to import
// testgen solely for a type constant in the enum switch.
func testgenKind(k testKind) testgen.EntryKind {
	if k == kindBench {
		return testgen.KindBench
	}
	return testgen.KindTest
}

// fileHasErrors reports whether any error-severity diagnostic in diags
// was produced for pf. Matches by byte offset — the parse/resolve/
// check pipeline emits per-file positions, and a file owns the
// diagnostic when the position's offset falls within its source.
//
// An empty diagnostic list returns false. Parse-time diagnostics are
// attributed by identity match against pf.ParseDiags.
func fileHasErrors(pf *resolve.PackageFile, diags []*diag.Diagnostic) bool {
	for _, pd := range pf.ParseDiags {
		if pd.Severity == diag.Error {
			return true
		}
	}
	for _, d := range diags {
		if d.Severity != diag.Error {
			continue
		}
		pos := d.PrimaryPos()
		if pos.Line == 0 {
			continue
		}
		// Resolver / checker positions are per-file line numbers, but
		// offsets are set per-input-buffer by the lexer. An offset that
		// fits within this file's source bytes is a reasonable (if
		// heuristic) attribution match.
		if pos.Offset > 0 && pos.Offset <= len(pf.Source) {
			return true
		}
	}
	return false
}

// resolveTestPackage runs name resolution over pkg with the cached
// stdlib registry attached so `use std.testing` in test files binds
// to the real module's PkgScope. pkg is registered under the empty
// dotted path — the single-package convention Workspace uses for the
// anonymous root — and every `use std.*` target referenced by the
// package is pre-loaded before ResolveAll runs. The pre-load is
// necessary because ResolveAll only resolves packages that were in
// ws.Packages at the start of the pass; stdlib packages added
// implicitly during bodyPass never get a resolver and panic later.
//
// Fallback: if NewWorkspace fails (only happens when filepath.Abs
// fails), we run a stdlib-unaware resolve so the user still gets
// useful diagnostics for everything that doesn't depend on `std.*`.
func resolveTestPackage(dir string, pkg *resolve.Package) *resolve.PackageResult {
	ws, err := resolve.NewWorkspace(dir)
	if err != nil {
		return resolve.ResolvePackage(pkg, resolve.NewPrelude())
	}
	ws.Stdlib = stdlib.LoadCached()
	ws.Packages[""] = pkg
	pkg.Name = filepath.Base(dir)
	preloadUseTargets(ws, pkg)
	results := ws.ResolveAll()
	if pr, ok := results[""]; ok && pr != nil {
		return pr
	}
	return &resolve.PackageResult{PackageScope: pkg.PkgScope}
}

// preloadUseTargets seeds ws.Packages with every package that pkg's
// `use` declarations reference. LoadPackage is recursive, so a single
// call per target chases the whole closure — including stdlib
// lookups routed through ws.Stdlib. Errors are intentionally swallowed
// here; the resolver surfaces missing-package diagnostics with richer
// source context during bodyPass.
func preloadUseTargets(ws *resolve.Workspace, pkg *resolve.Package) {
	for _, pf := range pkg.Files {
		if pf.File == nil {
			continue
		}
		for _, u := range pf.File.Uses {
			if u.IsGoFFI {
				continue
			}
			target := strings.Join(u.Path, ".")
			if target == "" {
				continue
			}
			if _, already := ws.Packages[target]; already {
				continue
			}
			_, _ = ws.LoadPackage(target)
		}
	}
}

// testEntry describes one discovered test or benchmark function —
// enough metadata to print a useful listing and (in a future phase) to
// drive code generation of a runner harness.
type testEntry struct {
	name string
	kind testKind
	path string
	line int
	col  int
}

type testKind int

const (
	kindTest testKind = iota
	kindBench
)

func (k testKind) label() string {
	switch k {
	case kindBench:
		return "BENCH"
	default:
		return "TEST"
	}
}

// discoverTests walks a file's top-level declarations and returns
// every fn that matches the test-discovery rules from spec §11 /
// §11.4 — see runTest for the rule set. Methods, nested functions,
// and associated fns are silently skipped.
func discoverTests(file *ast.File, path string) []testEntry {
	if file == nil {
		return nil
	}
	var out []testEntry
	for _, d := range file.Decls {
		fn, ok := d.(*ast.FnDecl)
		if !ok {
			continue
		}
		if !isDiscoverableSignature(fn) {
			continue
		}
		kind, matched := classifyTestName(fn.Name)
		if !matched {
			continue
		}
		out = append(out, testEntry{
			name: fn.Name,
			kind: kind,
			path: path,
			line: fn.PosV.Line,
			col:  fn.PosV.Column,
		})
	}
	return out
}

// isDiscoverableSignature reports whether a top-level fn has a shape
// that `osty test` is willing to consider: no receiver, no generics,
// zero parameters, no explicit return type (unit).
func isDiscoverableSignature(fn *ast.FnDecl) bool {
	if fn == nil {
		return false
	}
	if fn.Recv != nil {
		return false
	}
	if len(fn.Params) != 0 {
		return false
	}
	if len(fn.Generics) != 0 {
		return false
	}
	if fn.ReturnType != nil {
		return false
	}
	return true
}

// classifyTestName maps a fn name to its discovery kind. Any name
// starting with `test` is a test; any name starting with `bench` is a
// benchmark (spec §11 / §11.4). The bare prefix alone counts — `fn
// test()` is a valid test — because the spec wording imposes no
// suffix requirement.
func classifyTestName(name string) (testKind, bool) {
	switch {
	case strings.HasPrefix(name, "test"):
		return kindTest, true
	case strings.HasPrefix(name, "bench"):
		return kindBench, true
	}
	return 0, false
}

// collectTestTargets returns every `*_test.osty` path the user asked
// about. When explicit is empty the function falls back to a
// recursive walk of root (skipping `.osty/`, `.git/`, and other
// dot-prefixed directories). Explicit entries may be files (used as-is
// if they end in `_test.osty`) or directories (walked for test files).
func collectTestTargets(root string, explicit []string) ([]string, error) {
	if len(explicit) == 0 {
		return collectTestFiles(root)
	}
	var out []string
	seen := map[string]bool{}
	for _, p := range explicit {
		info, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			files, err := collectTestFiles(p)
			if err != nil {
				return nil, err
			}
			for _, f := range files {
				if !seen[f] {
					seen[f] = true
					out = append(out, f)
				}
			}
			continue
		}
		// Single-file path: include test files as-is. Non-test files
		// aren't runnable here, but we still accept them so `osty test
		// foo.osty` gives a useful error rather than silently dropping.
		abs, _ := filepath.Abs(p)
		if !seen[abs] {
			seen[abs] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out, nil
}

// collectTestFiles walks root (excluding .osty/**, .git/**, and other
// dot-prefixed directories) for `*_test.osty` files. Returns a sorted
// slice so the runner's per-file output is deterministic across runs.
func collectTestFiles(root string) ([]string, error) {
	var out []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			base := filepath.Base(path)
			if path != root && (base == ".osty" || base == ".git" || strings.HasPrefix(base, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, "_test.osty") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// filterTestFiles keeps only files whose basename contains any of
// `filters` as a substring. Case-sensitive; matches `auth` →
// `auth_test.osty` and `user_auth_test.osty`.
func filterTestFiles(files []string, filters []string) []string {
	if len(filters) == 0 {
		return files
	}
	var out []string
	for _, f := range files {
		base := filepath.Base(f)
		for _, q := range filters {
			if strings.Contains(base, q) {
				out = append(out, f)
				break
			}
		}
	}
	return out
}

// executeTestPackage transpiles pkg via testgen, writes the Go
// sources into a per-package scratch directory under $TMPDIR, and
// executes a cached test binary for the generated suite. It returns
// the runtime pass/fail counts parsed from the harness's own summary
// line.
//
// The scratch directory is keyed by the SHA-1 of the package's
// absolute path so concurrent invocations targeting different
// packages don't collide. Generated sources are rewritten every run;
// compiled binaries are keyed by source content and toolchain identity.
//
// A non-nil error indicates an I/O problem, a testgen merge error,
// or a Go build / invocation failure unrelated to test verdicts.
// Individual test failures show up as FAIL lines in the harness's
// stdout and are counted into runFail; they don't become a Go
// error from this function.
func executeTestPackage(dir string, pkg *resolve.Package, chk *check.Result, entries []testgen.Entry, root string) (int, int, error) {
	// Sort entries in source-declaration order so the harness output
	// mirrors the validation listing printed above it.
	sorted := append([]testgen.Entry(nil), entries...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].File != sorted[j].File {
			return sorted[i].File < sorted[j].File
		}
		return sorted[i].Line < sorted[j].Line
	})

	srcs, genErr := testgen.GenerateHarness(pkg, chk, sorted)

	outDir, err := scratchDir(dir)
	if err != nil {
		return 0, 0, err
	}
	mainPath := filepath.Join(outDir, "main.go")
	harnessPath := filepath.Join(outDir, "harness.go")
	if err := os.WriteFile(mainPath, srcs.Main, 0o644); err != nil {
		return 0, 0, err
	}
	if err := os.WriteFile(harnessPath, srcs.Harness, 0o644); err != nil {
		return 0, 0, err
	}
	// Non-fatal gen warnings come back as (partial output, err). We
	// still try to run the harness because the clean portion often
	// compiles; surface the generated file path for post-mortem work.
	reportTranspileWarning("osty test", dir, mainPath, genErr)
	// The generated package needs a module context. The harness has no
	// third-party dependencies so a bare go.mod is sufficient; it
	// also insulates the scratch directory from ambient GOPATH /
	// workspace settings that might otherwise hijack the build.
	goMod := []byte("module ostytest\n\ngo 1.22\n")
	goModPath := filepath.Join(outDir, "go.mod")
	if err := os.WriteFile(goModPath, goMod, 0o644); err != nil {
		return 0, 0, err
	}

	fmt.Printf("\n--- running tests in %s ---\n", relOrSelf(dir, root))

	binPath := cachedTestBinaryPath(outDir, map[string][]byte{
		"main.go":    srcs.Main,
		"harness.go": srcs.Harness,
		"go.mod":     goMod,
	})
	buildArgs, buildStderr, buildErr := ensureCachedTestBinary(outDir, binPath)
	if buildErr != nil {
		reportGoFailure(goFailureReport{
			Tool:      "osty test",
			Action:    "go build",
			Args:      buildArgs,
			WorkDir:   outDir,
			Generated: []string{mainPath, harnessPath},
			Source:    dir,
			Stderr:    buildStderr,
			Err:       buildErr,
		})
		return 0, 0, fmt.Errorf("go build failed in %s: %w", outDir, buildErr)
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.Command(binPath)
	cmd.Dir = outDir
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	// The harness prints each TEST line and a trailing summary to
	// stdout. Echo everything so the user sees the live output,
	// then parse the "N passed, M failed" tail for our counts.
	os.Stdout.Write(stdout.Bytes())
	if stderr.Len() > 0 {
		os.Stderr.Write(stderr.Bytes())
	}

	runPass, runFail, parsed := parseHarnessSummary(stdout.String())
	if runErr != nil && !parsed {
		// The generated program failed before the harness could print a summary.
		// The sources were left behind under outDir for post-mortem
		// — point the user there.
		reportGoFailure(goFailureReport{
			Tool:      "osty test",
			Action:    "test binary",
			Args:      cmd.Args,
			WorkDir:   outDir,
			Generated: []string{mainPath, harnessPath},
			Source:    dir,
			Stderr:    stderr.String(),
			Err:       runErr,
		})
		return 0, 0, fmt.Errorf("test binary failed in %s: %w", outDir, runErr)
	}
	return runPass, runFail, nil
}

func cachedTestBinaryPath(outDir string, files map[string][]byte) string {
	h := sha1.New()
	h.Write([]byte(runtime.GOOS))
	h.Write([]byte{0})
	h.Write([]byte(runtime.GOARCH))
	h.Write([]byte{0})
	h.Write([]byte(runtime.Version()))
	h.Write([]byte{0})
	if path, err := exec.LookPath("go"); err == nil {
		h.Write([]byte(path))
		h.Write([]byte{0})
		if info, err := os.Stat(path); err == nil {
			fmt.Fprintf(h, "%d:%d", info.Size(), info.ModTime().UnixNano())
			h.Write([]byte{0})
		}
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write(files[name])
		h.Write([]byte{0})
	}
	sum := h.Sum(nil)
	name := "osty-test-bin-" + hex.EncodeToString(sum[:8])
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(outDir, name)
}

func ensureCachedTestBinary(outDir, binPath string) ([]string, string, error) {
	if _, err := os.Stat(binPath); err == nil {
		return []string{binPath}, "", nil
	} else if !os.IsNotExist(err) {
		return nil, "", err
	}

	tmp, err := os.CreateTemp(outDir, ".osty-test-bin-*")
	if err != nil {
		return nil, "", err
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return nil, "", err
	}
	if err := os.Remove(tmpPath); err != nil {
		return nil, "", err
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.Command("go", "build", "-o", tmpPath, ".")
	cmd.Dir = outDir
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		_ = os.Remove(tmpPath)
		return cmd.Args, stdout.String() + stderr.String(), err
	}
	if err := os.Rename(tmpPath, binPath); err != nil {
		_ = os.Remove(tmpPath)
		if _, statErr := os.Stat(binPath); statErr == nil {
			return []string{binPath}, "", nil
		}
		return cmd.Args, "", err
	}
	return []string{binPath}, "", nil
}

// scratchDir returns a stable per-package scratch directory under
// $TMPDIR keyed by the SHA-1 of the absolute package path. The
// returned directory is created if it doesn't exist; contents are
// overwritten on each run.
func scratchDir(pkgDir string) (string, error) {
	abs, err := filepath.Abs(pkgDir)
	if err != nil {
		return "", err
	}
	sum := sha1.Sum([]byte(abs))
	key := hex.EncodeToString(sum[:8])
	base := filepath.Join(os.TempDir(), "osty-test-"+key)
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", err
	}
	return base, nil
}

// parseHarnessSummary scans the harness's stdout for the trailing
// "N passed, M failed, K total" line and returns the parsed counts.
// ok is false when no line matched — which means the harness didn't
// reach its summary() call (usually a go-compile error upstream).
func parseHarnessSummary(out string) (pass int, fail int, ok bool) {
	for _, line := range strings.Split(out, "\n") {
		var p, f, t int
		n, err := fmt.Sscanf(strings.TrimSpace(line), "%d passed, %d failed, %d total", &p, &f, &t)
		if err == nil && n == 3 {
			return p, f, true
		}
	}
	return 0, 0, false
}

// relOrSelf returns a path relative to root when possible, otherwise
// the original path. Used to render compact status headers like
// "running tests in examples/calc" instead of absolute paths.
func relOrSelf(p, root string) string {
	if rel, err := filepath.Rel(root, p); err == nil {
		return rel
	}
	return p
}
