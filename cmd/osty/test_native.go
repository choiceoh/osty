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

func runTest(args []string, flags cliFlags) {
	os.Exit(runTestMain(args, flags, os.Stdout, os.Stderr))
}

func runTestMain(args []string, flags cliFlags, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: osty test [--offline | --locked | --frozen] [--backend NAME] [--emit MODE] [--airepair=false] [--airepair-mode MODE] [--seed HEX] [--serial] [--jobs N] [PATH|FILTER...]")
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

	tests, err := discoverNativeTests(pkg, filters)
	if err != nil {
		fmt.Fprintf(stderr, "osty test: %v\n", err)
		return 1
	}
	if len(tests) == 0 {
		fmt.Fprintln(stdout, "running 0 tests")
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

	fmt.Fprintf(stdout, "running %d tests (seed %s)\n", len(tests), formatTestSeed(seed))
	started := time.Now()
	workers := resolveTestWorkers(serial, jobs, len(tests))
	failures := executeNativeTests(context.Background(), b, emitMode, tmpRoot, pkg, tests, workers, seed, stdout, stderr)
	elapsed := time.Since(started)
	if failures > 0 {
		fmt.Fprintf(stdout, "FAIL\t%d/%d tests failed in %s (seed %s)\n", failures, len(tests), formatTestDuration(elapsed), formatTestSeed(seed))
		return 1
	}
	fmt.Fprintf(stdout, "ok\t%d tests passed in %s (seed %s)\n", len(tests), formatTestDuration(elapsed), formatTestSeed(seed))
	return 0
}

// executeNativeTests dispatches the compile+run loop over the shuffled
// tests slice. With workers==1 we keep the serial single-goroutine path
// so output ordering stays stable and deterministic under the seed.
// With workers>1 each test is compiled and run on its own goroutine and
// results are printed in completion order — still reproducible because
// the *set* of executed tests and the failure verdict do not depend on
// scheduling.
func executeNativeTests(ctx context.Context, b backend.Backend, emitMode backend.EmitMode, tmpRoot string, pkg *resolve.Package, tests []nativeTestCase, workers int, seed uint64, stdout, stderr io.Writer) int {
	if workers <= 1 {
		failures := 0
		for _, tc := range tests {
			if reportNativeTestResult(runSingleNativeTest(ctx, b, emitMode, tmpRoot, pkg, tc), stdout, stderr) {
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
			resultsCh <- runSingleNativeTest(ctx, b, emitMode, tmpRoot, pkg, tc)
		}()
	}
	go func() {
		wg.Wait()
		close(resultsCh)
	}()
	failures := 0
	for outcome := range resultsCh {
		if reportNativeTestResult(outcome, stdout, stderr) {
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

func runSingleNativeTest(ctx context.Context, b backend.Backend, emitMode backend.EmitMode, tmpRoot string, pkg *resolve.Package, tc nativeTestCase) nativeTestOutcome {
	start := time.Now()
	run, err := compileAndRunNativeTest(ctx, b, emitMode, tmpRoot, pkg, tc)
	return nativeTestOutcome{Test: tc, Run: run, Err: err, Elapsed: time.Since(start)}
}

// reportNativeTestResult emits the per-test status line and any captured
// child-process output. Returns true iff the test failed, so callers can
// tally the failure count without re-inspecting the outcome.
func reportNativeTestResult(o nativeTestOutcome, stdout, stderr io.Writer) bool {
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
	return false
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

func discoverNativeTests(pkg *resolve.Package, filters []string) ([]nativeTestCase, error) {
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
			if !strings.HasPrefix(fn.Name, "test") || fn.Name == "testing" {
				continue
			}
			if len(fn.Params) != 0 || fn.ReturnType != nil || fn.Body == nil {
				continue
			}
			if !matchesTestFilters(fn.Name, filters) {
				continue
			}
			if prev, exists := seen[fn.Name]; exists {
				return nil, fmt.Errorf("duplicate test function %q in %s and %s", fn.Name, prev, pf.Path)
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

type nativeTestRun struct {
	Stdout string
	Stderr string
}

func compileAndRunNativeTest(ctx context.Context, b backend.Backend, emitMode backend.EmitMode, tmpRoot string, pkg *resolve.Package, tc nativeTestCase) (nativeTestRun, error) {
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
	return runNativeTestBinary(result.Artifacts.Binary)
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

func runNativeTestBinary(binPath string) (nativeTestRun, error) {
	if binPath == "" {
		return nativeTestRun{}, fmt.Errorf("native backend did not produce a binary")
	}
	absBin, err := filepath.Abs(binPath)
	if err != nil {
		return nativeTestRun{}, err
	}
	cmd := exec.Command(absBin)
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
