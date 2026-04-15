package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/manifest"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
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
// Actual test *execution* (the Go runner harness) is pending — this
// command currently validates the tests and reports what was found.
//
// Exit codes:
//
//	0   every test file's package type-checks cleanly
//	1   at least one error-severity diagnostic was emitted
//	2   usage error or manifest / I/O failure
func runTest(args []string, flags cliFlags) {
	fs := flag.NewFlagSet("test", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: osty test [--offline] [PATH|FILTER...]")
	}
	var offline bool
	fs.BoolVar(&offline, "offline", false, "do not fetch dependencies; fail if caches are missing")
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
		if err := resolveAndVendor(man, root, offline); err != nil {
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
	for _, dir := range dirs {
		ok, t, b := runTestDir(dir, byDir[dir], root, flags)
		if !ok {
			anyErr = true
			continue
		}
		passed += len(byDir[dir])
		totalTests += t
		totalBenches += b
	}

	fmt.Printf("\n%d / %d test file(s) pass — %d tests, %d benchmarks discovered\n",
		passed, len(testFiles), totalTests, totalBenches)
	fmt.Println()
	fmt.Println("note: test execution is not yet implemented; `osty test` currently")
	fmt.Println("validates and reports discovered functions. The runner harness will")
	fmt.Println("arrive in a later phase — see LANG_SPEC_v0.3 §11.")

	if anyErr {
		os.Exit(1)
	}
}

// runTestDir runs the full front-end pipeline over one package
// directory and emits per-file results. interesting is the subset of
// files the user asked about (explicit paths or the walker's
// *_test.osty match); every .osty file in dir — including non-test
// files — is loaded for resolution so cross-file refs work.
//
// Returns (ok, totalTests, totalBenchmarks). ok is false when any
// error-severity diagnostic was printed for this package.
func runTestDir(dir string, interesting []string, root string, flags cliFlags) (bool, int, int) {
	pkg, err := resolve.LoadPackageWithTests(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty test: %v\n", err)
		return false, 0, 0
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
		}
		totalTests += tests
		totalBenches += benches
		fmt.Printf("ok    %s  (%d tests, %d benchmarks)\n", rel, tests, benches)
		for _, e := range entries {
			fmt.Printf("        %-6s %-32s (line %d)\n",
				e.kind.label(), e.name, e.line)
		}
	}
	return fileOK, totalTests, totalBenches
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
