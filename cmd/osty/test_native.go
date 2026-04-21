package main

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	mathrand "math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/backend"
	"github.com/osty/osty/internal/canonical"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/runner"
)

type nativeTestCase struct {
	Name string
	Path string
}

func pluralKindLabel(benchMode bool) string {
	if benchMode {
		return "benchmarks"
	}
	return "tests"
}

func singularKindLabel(benchMode bool) string {
	if benchMode {
		return "benchmark"
	}
	return "test"
}

func runTest(args []string, flags cliFlags) {
	os.Exit(runTestMain(args, flags, os.Stdout, os.Stderr))
}

func runTestMain(args []string, flags cliFlags, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: osty test [--offline | --locked | --frozen] [--backend NAME] [--emit MODE] [--airepair=false] [--airepair-mode MODE] [--seed HEX] [--serial] [--jobs N] [--doc] [--bench] [--benchtime DUR] [PATH|FILTER...]")
	}
	var offline, locked, frozen bool
	fs.BoolVar(&offline, "offline", false, "do not fetch dependencies; fail if caches are missing")
	fs.BoolVar(&locked, "locked", false, "fail if osty.lock would change")
	fs.BoolVar(&frozen, "frozen", false, "imply --locked --offline; require an existing osty.lock")
	var aiRepairModeName string
	registerAIRepairCommandFlags(fs, &flags.aiRepair, &aiRepairModeName)
	var backendName string
	var emitName string
	fs.StringVar(&backendName, "backend", defaultBackendName(), "code generation backend (llvm)")
	fs.StringVar(&emitName, "emit", "", "artifact mode to execute (binary)")
	var seedFlag string
	fs.StringVar(&seedFlag, "seed", "", "deterministic test-order seed (decimal or 0x-hex); default is a fresh random seed")
	var serial bool
	fs.BoolVar(&serial, "serial", false, "run tests sequentially in the shuffled order (default: parallel)")
	var jobs int
	fs.IntVar(&jobs, "jobs", 0, "max concurrent tests when parallel (0 = runtime.NumCPU())")
	var docTests bool
	fs.BoolVar(&docTests, "doc", false, "v0.5 G32: extract `osty-fenced examples from /// doc comments and run each as an additional test")
	var benchMode bool
	fs.BoolVar(&benchMode, "bench", false, "spec §11.4: run `bench*` functions (discovered like tests) instead of `test*`; each bench prints its timing summary")
	var benchTime string
	fs.StringVar(&benchTime, "benchtime", "", "auto-tune each benchmark's iteration count to run for at least this Go-style duration (e.g. 500ms, 2s); overrides the N argument in testing.benchmark(N, …). Only meaningful with --bench.")
	var pf profileFlags
	pf.register(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	seed, err := resolveTestSeed(seedFlag)
	if err != nil {
		fmt.Fprintf(stderr, "osty test: %v\n", err)
		return 2
	}
	mode, ok := parseAIRepairMode(aiRepairModeName)
	if !ok {
		fmt.Fprintf(stderr, "osty test: unknown airepair mode %q (want auto, rewrite, parse, or frontend)\n", aiRepairModeName)
		return 2
	}
	flags.aiMode = mode
	_ = offline
	_ = locked
	_ = frozen
	_ = pf

	backendID, emitMode := resolveBackendAndEmitFlags("test", backendName, emitName)
	pkgDir, filters, err := resolveTestTarget(fs.Args())
	if err != nil {
		fmt.Fprintf(stderr, "osty test: %v\n", err)
		return 2
	}
	benchTimeNs, err := resolveBenchTime(benchTime, benchMode)
	if err != nil {
		fmt.Fprintf(stderr, "osty test: %v\n", err)
		return 2
	}

	pkg, err := resolve.LoadPackageWithTestsTransform(pkgDir, aiRepairSourceTransform("osty test --airepair", stderr, flags))
	if err != nil {
		fmt.Fprintf(stderr, "osty test: %v\n", err)
		return 1
	}
	parseDiags := packageParseDiags(pkg)
	if hasError(parseDiags) {
		printPackageDiags(pkg, parseDiags, flags)
		fmt.Fprintf(stderr, "osty test: parse errors in %s\n", pkgDir)
		return 1
	}

	tests, err := discoverNativeTests(pkg, filters, benchMode)
	if err != nil {
		fmt.Fprintf(stderr, "osty test: %v\n", err)
		return 1
	}
	if docTests {
		if benchMode {
			fmt.Fprintln(stderr, "osty test: --doc and --bench cannot be combined")
			return 2
		}
		doc, err := appendDoctestCases(pkg, filters)
		if err != nil {
			fmt.Fprintf(stderr, "osty test --doc: %v\n", err)
			return 1
		}
		tests = append(tests, doc...)
	}
	kindLabel := pluralKindLabel(benchMode)
	if len(tests) == 0 {
		fmt.Fprintf(stdout, "running 0 %s\n", kindLabel)
		return 0
	}

	// Shuffle with the resolved seed. Spec §11.7 — tests run in a random
	// order so accidentally shared state surfaces instead of hiding
	// behind declaration-order luck; the seed is printed so users can
	// reproduce.
	shuffleNativeTests(tests, seed)

	b, err := backend.New(backendID)
	if err != nil {
		fmt.Fprintf(stderr, "osty test: %v\n", err)
		return 2
	}
	tmpRoot, err := os.MkdirTemp("", "osty-test-*")
	if err != nil {
		fmt.Fprintf(stderr, "osty test: %v\n", err)
		return 1
	}
	defer os.RemoveAll(tmpRoot)

	fmt.Fprintf(stdout, "running %d %s (seed %s)\n", len(tests), kindLabel, formatTestSeed(seed))
	started := time.Now()
	workers := resolveTestWorkers(serial, jobs, len(tests))
	failures := executeNativeTests(context.Background(), b, emitMode, tmpRoot, pkg, tests, workers, seed, stdout, stderr, benchMode, benchTimeNs)
	elapsed := time.Since(started)
	if failures > 0 {
		fmt.Fprintf(stdout, "FAIL\t%d/%d %s failed in %s (seed %s)\n", failures, len(tests), kindLabel, formatTestDuration(elapsed), formatTestSeed(seed))
		return 1
	}
	fmt.Fprintf(stdout, "ok\t%d %s passed in %s (seed %s)\n", len(tests), kindLabel, formatTestDuration(elapsed), formatTestSeed(seed))
	return 0
}

// executeNativeTests dispatches the compile+run loop over the shuffled
// tests slice. With workers==1 we keep the serial single-goroutine path
// so output ordering stays stable and deterministic under the seed.
// With workers>1 each test is compiled and run on its own goroutine and
// results are printed in completion order — still reproducible because
// the *set* of executed tests and the failure verdict do not depend on
// scheduling.
func executeNativeTests(ctx context.Context, b backend.Backend, emitMode backend.EmitMode, tmpRoot string, pkg *resolve.Package, tests []nativeTestCase, workers int, seed uint64, stdout, stderr io.Writer, benchMode bool, benchTimeNs int64) int {
	if workers <= 1 {
		failures := 0
		for _, tc := range tests {
			if reportNativeTestResult(runSingleNativeTest(ctx, b, emitMode, tmpRoot, pkg, tc, benchTimeNs), stdout, stderr, benchMode) {
				failures++
			}
		}
		return failures
	}
	sem := make(chan struct{}, workers)
	resultsCh := make(chan nativeTestOutcome, len(tests))
	var wg sync.WaitGroup
	for _, tc := range tests {
		tc := tc
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			resultsCh <- runSingleNativeTest(ctx, b, emitMode, tmpRoot, pkg, tc, benchTimeNs)
		}()
	}
	go func() {
		wg.Wait()
		close(resultsCh)
	}()
	failures := 0
	for outcome := range resultsCh {
		if reportNativeTestResult(outcome, stdout, stderr, benchMode) {
			failures++
		}
	}
	return failures
}

type nativeTestOutcome struct {
	Test    nativeTestCase
	Run     nativeTestRun
	Err     error
	Elapsed time.Duration
}

func runSingleNativeTest(ctx context.Context, b backend.Backend, emitMode backend.EmitMode, tmpRoot string, pkg *resolve.Package, tc nativeTestCase, benchTimeNs int64) nativeTestOutcome {
	start := time.Now()
	run, err := compileAndRunNativeTest(ctx, b, emitMode, tmpRoot, pkg, tc, benchTimeNs)
	return nativeTestOutcome{Test: tc, Run: run, Err: err, Elapsed: time.Since(start)}
}

// reportNativeTestResult emits the per-test status line and any captured
// child-process output. Returns true iff the test failed, so callers can
// tally the failure count without re-inspecting the outcome.
//
// In bench mode the child's stdout is always surfaced — each benchmark
// prints its own `bench <path>:<line> iter=… total=…ns avg=…ns` summary
// from the LLVM timing harness (internal/llvmgen/stmt.go:
// emitTestingBenchmarkStmt), and swallowing that would defeat the whole
// point of running `--bench`.
func reportNativeTestResult(o nativeTestOutcome, stdout, stderr io.Writer, benchMode bool) bool {
	dur := formatTestDuration(o.Elapsed)
	if o.Err != nil {
		fmt.Fprintf(stdout, "FAIL\t%s\t%s\n", o.Test.Name, dur)
		fmt.Fprintf(stderr, "osty test: %s: %v\n", o.Test.Name, o.Err)
		if strings.TrimSpace(o.Run.Stdout) != "" {
			fmt.Fprintf(stdout, "%s", o.Run.Stdout)
			if !strings.HasSuffix(o.Run.Stdout, "\n") {
				fmt.Fprintln(stdout)
			}
		}
		if strings.TrimSpace(o.Run.Stderr) != "" {
			fmt.Fprintf(stderr, "%s", o.Run.Stderr)
			if !strings.HasSuffix(o.Run.Stderr, "\n") {
				fmt.Fprintln(stderr)
			}
		}
		return true
	}
	fmt.Fprintf(stdout, "ok\t%s\t%s\n", o.Test.Name, dur)
	if benchMode && strings.TrimSpace(o.Run.Stdout) != "" {
		fmt.Fprintf(stdout, "%s", o.Run.Stdout)
		if !strings.HasSuffix(o.Run.Stdout, "\n") {
			fmt.Fprintln(stdout)
		}
	}
	return false
}

// resolveBenchTime parses --benchtime into nanoseconds. Empty / unset
// returns 0 (meaning "use the user's declared N"). --benchtime without
// --bench is rejected so users aren't surprised when it silently no-ops
// in test mode.
func resolveBenchTime(raw string, benchMode bool) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	if !benchMode {
		return 0, fmt.Errorf("--benchtime requires --bench")
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid --benchtime %q: %w", raw, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("--benchtime must be positive, got %q", raw)
	}
	return d.Nanoseconds(), nil
}

// resolveTestSeed parses the --seed flag. The empty string produces a
// fresh cryptographic-quality random seed; a leading `0x` switches the
// parser to base-16 per spec §11.7's printed format.
func resolveTestSeed(raw string) (uint64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		var b [8]byte
		if _, err := cryptorand.Read(b[:]); err != nil {
			return 0, fmt.Errorf("generate random seed: %w", err)
		}
		return binary.LittleEndian.Uint64(b[:]), nil
	}
	base := 10
	s := raw
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		base = 16
		s = s[2:]
	}
	v, err := strconv.ParseUint(s, base, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid --seed %q: %w", raw, err)
	}
	return v, nil
}

func formatTestSeed(seed uint64) string {
	return fmt.Sprintf("0x%X", seed)
}

// formatTestDuration renders the per-test wall time. Short tests round
// to milliseconds so the column stays narrow; longer ones promote to
// seconds with two decimals for readability.
func formatTestDuration(d time.Duration) string {
	if d < time.Second {
		ms := d.Milliseconds()
		if ms < 1 {
			ms = 1
		}
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.2fs", d.Seconds())
}

func resolveTestWorkers(serial bool, jobs, n int) int {
	return runner.ResolveTestWorkers(serial, jobs, runtime.NumCPU(), n)
}

// shuffleNativeTests randomizes the test slice in place using a PRNG
// seeded from the resolved seed. The seeded rng is deterministic so
// --seed reproduces the same order regardless of host.
func shuffleNativeTests(tests []nativeTestCase, seed uint64) {
	// math/rand/v2 needs two uint64 words to seed PCG; derive a second
	// independent word from the first via a splitmix step so a single
	// user-supplied seed still fills both slots reproducibly.
	mix := seed + 0x9E3779B97F4A7C15
	mix = (mix ^ (mix >> 30)) * 0xBF58476D1CE4E5B9
	mix = (mix ^ (mix >> 27)) * 0x94D049BB133111EB
	mix ^= mix >> 31
	rng := mathrand.New(mathrand.NewPCG(seed, mix))
	rng.Shuffle(len(tests), func(i, j int) { tests[i], tests[j] = tests[j], tests[i] })
}

func resolveTestTarget(args []string) (string, []string, error) {
	if len(args) == 0 {
		return ".", nil, nil
	}
	first := args[0]
	if info, err := os.Stat(first); err == nil {
		if info.IsDir() {
			return first, args[1:], nil
		}
		return filepath.Dir(first), args[1:], nil
	}
	return ".", args, nil
}

func packageParseDiags(pkg *resolve.Package) []*diag.Diagnostic {
	var out []*diag.Diagnostic
	if pkg == nil {
		return out
	}
	for _, pf := range pkg.Files {
		if pf == nil {
			continue
		}
		out = append(out, pf.ParseDiags...)
	}
	return out
}

func discoverNativeTests(pkg *resolve.Package, filters []string, benchMode bool) ([]nativeTestCase, error) {
	if pkg == nil {
		return nil, fmt.Errorf("missing package for test discovery")
	}
	seen := map[string]string{}
	var tests []nativeTestCase
	for _, pf := range pkg.Files {
		if pf == nil || pf.File == nil {
			continue
		}
		for _, decl := range pf.File.Decls {
			fn, ok := decl.(*ast.FnDecl)
			if !ok || fn == nil {
				continue
			}
			if fn.Recv != nil || len(fn.Generics) != 0 {
				continue
			}
			if fn.Name == "main" {
				return nil, fmt.Errorf("package %s already defines main; native test runner currently requires a library-style package", pkg.Dir)
			}
			// `testing` is the stdlib module helper name; skip it in
			// either mode so it can't shadow a real test.
			if fn.Name == "testing" {
				continue
			}
			// Selection rules:
			//   test mode   — names starting with `test` *or* functions
			//                 carrying `#[test]` (v0.5 G32); bench names
			//                 are skipped.
			//   bench mode  — spec §11.4: names starting with `bench`,
			//                 zero params, no return. No annotation
			//                 surface yet.
			if benchMode {
				if !strings.HasPrefix(fn.Name, "bench") {
					continue
				}
			} else {
				if strings.HasPrefix(fn.Name, "bench") {
					continue
				}
				if !strings.HasPrefix(fn.Name, "test") && !hasTestAnnotation(fn) {
					continue
				}
			}
			if len(fn.Params) != 0 || fn.ReturnType != nil || fn.Body == nil {
				continue
			}
			if !matchesTestFilters(fn.Name, filters) {
				continue
			}
			if prev, exists := seen[fn.Name]; exists {
				return nil, fmt.Errorf("duplicate %s function %q in %s and %s", singularKindLabel(benchMode), fn.Name, prev, pf.Path)
			}
			seen[fn.Name] = pf.Path
			tests = append(tests, nativeTestCase{Name: fn.Name, Path: pf.Path})
		}
	}
	sort.Slice(tests, func(i, j int) bool {
		if tests[i].Path != tests[j].Path {
			return tests[i].Path < tests[j].Path
		}
		return tests[i].Name < tests[j].Name
	})
	return tests, nil
}

func matchesTestFilters(name string, filters []string) bool {
	return runner.MatchesTestFilters(name, filters)
}

// hasTestAnnotation reports whether a FnDecl carries `#[test]`.
// v0.5 (G32) §11 introduces the annotation so tests can sit inline
// next to production code without relying on the `test` name prefix
// or the `_test.osty` file split. The annotation has no arguments
// today, so exact-match is sufficient.
func hasTestAnnotation(fn *ast.FnDecl) bool {
	if fn == nil {
		return false
	}
	for _, a := range fn.Annotations {
		if a != nil && a.Name == "test" {
			return true
		}
	}
	return false
}

type nativeTestRun struct {
	Stdout string
	Stderr string
}

func compileAndRunNativeTest(ctx context.Context, b backend.Backend, emitMode backend.EmitMode, tmpRoot string, pkg *resolve.Package, tc nativeTestCase, benchTimeNs int64) (nativeTestRun, error) {
	file, src, sourcePath, err := parseNativeTestEntry(pkg, tc)
	if err != nil {
		return nativeTestRun{}, err
	}
	name := sanitizeNativeTestName(tc.Name)
	layoutRoot := filepath.Join(tmpRoot, name)
	res := resolveFile(file)
	chk := check.File(file, res, checkOptsForSource(src))
	entry, err := backend.PrepareEntry("main", sourcePath, file, res, chk)
	if err != nil {
		return nativeTestRun{}, err
	}
	// Hand the concatenated source bytes to the backend so the LLVM
	// emitter can quote the original expression text of
	// `testing.assertEq` arguments in failure messages. Without this
	// the backend falls back to location-only output.
	entry.Source = src
	req := backend.Request{
		Layout: backend.Layout{
			Root:    layoutRoot,
			Profile: "test",
		},
		Emit:       emitMode,
		Entry:      entry,
		BinaryName: name,
	}
	result, err := b.Emit(ctx, req)
	if err != nil {
		return nativeTestRun{}, err
	}
	return runNativeTestBinary(result.Artifacts.Binary, benchTimeNs)
}

func parseNativeTestEntry(pkg *resolve.Package, tc nativeTestCase) (*ast.File, []byte, string, error) {
	if pkg == nil {
		return nil, nil, "", fmt.Errorf("missing package")
	}
	runnerPath := filepath.Join(pkg.Dir, "__osty_test_runner__.osty")
	runner := []byte(fmt.Sprintf("fn main() {\n    %s()\n}\n", tc.Name))
	testPkg := &resolve.Package{
		Dir:  pkg.Dir,
		Name: pkg.Name,
	}
	testPkg.Files = append(testPkg.Files, pkg.Files...)
	runnerFile, runnerDiags := parser.ParseDiagnostics(runner)
	runnerCanonical, runnerCanonicalMap := canonical.SourceWithMap(runner, runnerFile)
	testPkg.Files = append(testPkg.Files, &resolve.PackageFile{
		Path:            runnerPath,
		Source:          runner,
		CanonicalSource: runnerCanonical,
		CanonicalMap:    runnerCanonicalMap,
		File:            runnerFile,
		ParseDiags:      runnerDiags,
	})
	file, src, err := parseGenEmitFile(testPkg)
	if err != nil {
		return nil, nil, "", err
	}
	sourcePath := tc.Path
	if sourcePath == "" {
		sourcePath = runnerPath
	}
	return file, src, sourcePath, nil
}

func sanitizeNativeTestName(name string) string {
	return runner.SanitizeNativeTestName(name)
}

func runNativeTestBinary(binPath string, benchTimeNs int64) (nativeTestRun, error) {
	if binPath == "" {
		return nativeTestRun{}, fmt.Errorf("native backend did not produce a binary")
	}
	absBin, err := filepath.Abs(binPath)
	if err != nil {
		return nativeTestRun{}, err
	}
	cmd := exec.Command(absBin)
	// OSTY_BENCH_TIME_NS is read by osty_rt_bench_target_ns. Only set
	// it when non-zero so unrelated child processes (e.g. the test
	// runner invoked without --bench) don't inherit a stale value from
	// ambient environment.
	if benchTimeNs > 0 {
		cmd.Env = append(os.Environ(), fmt.Sprintf("OSTY_BENCH_TIME_NS=%d", benchTimeNs))
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	run := nativeTestRun{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}
	if err == nil {
		return run, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return run, fmt.Errorf("exit status %d", exitErr.ExitCode())
	}
	return run, err
}
