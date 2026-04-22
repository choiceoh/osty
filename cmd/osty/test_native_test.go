package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/osty/osty/internal/backend"
	"github.com/osty/osty/internal/resolve"
)

func TestRunTestMainPassesNativeLLVMTest(t *testing.T) {
	requireClangForNativeTest(t)

	dir := t.TempDir()
	writeNativeTestFile(t, dir, "lib.osty", `pub fn add(a: Int, b: Int) -> Int {
    a + b
}
`)
	writeNativeTestFile(t, dir, "lib_test.osty", `use std.testing as t

fn testAdd() {
    t.context("simple", || {
        t.assertEq(add(1, 2), 3)
    })
}
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runTestMain([]string{dir}, cliFlags{}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runTestMain() exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, "ok\ttestAdd") || !strings.Contains(got, "ok\t1 tests passed") {
		t.Fatalf("stdout = %q, want passing test summary", got)
	}
	if got := stderr.String(); strings.TrimSpace(got) != "" {
		t.Fatalf("stderr = %q, want empty stderr", got)
	}
}

func TestRunTestMainBenchSupportsMultiFilePackageLowering(t *testing.T) {
	requireClangForNativeTest(t)

	dir := t.TempDir()
	writeNativeTestFile(t, dir, "lib.osty", `pub fn accumulate(rows: List<String>) -> Int {
    let mut totals: Map<String, Int> = {:}
    for row in rows {
        let next = if totals.containsKey(row) {
            totals.getOr(row, 0) + 1
        } else {
            1
        }
        totals.insert(row, next)
    }

    let keys = totals.keys().sorted()
    let mut checksum = 0
    for key in keys {
        checksum = checksum + totals.getOr(key, 0) + key.len()
    }
    checksum
}
`)
	writeNativeTestFile(t, dir, "lib_test.osty", `use std.testing

fn benchAccumulate() {
    let rows = ["compiler", "runtime", "compiler", "lsp"]
    testing.benchmark(5, || {
        let _ = accumulate(rows)
        Ok(())
    })
}
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runTestMain([]string{"--bench", "--serial", dir}, cliFlags{}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runTestMain() bench exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, "ok\tbenchAccumulate") || !strings.Contains(got, "ok\t1 benchmarks passed") {
		t.Fatalf("stdout = %q, want passing benchmark summary", got)
	}
	if got := stderr.String(); strings.TrimSpace(got) != "" {
		t.Fatalf("stderr = %q, want empty stderr", got)
	}
}

func TestCompileNativeTestBundleUsesManagedNativeLLVMGenWhenCovered(t *testing.T) {
	dir := t.TempDir()
	path := writeNativeTestFile(t, dir, "lib_test.osty", `fn testAdd() {}
`)
	pkg := &resolve.Package{
		Dir:  dir,
		Name: "demo",
		Files: []*resolve.PackageFile{
			{Path: path, Source: []byte("fn testAdd() {}\n")},
		},
	}

	oldTryIR := tryExternalPackageLLVMIR
	oldEmit := emitPrebuiltLLVMIR
	t.Cleanup(func() {
		tryExternalPackageLLVMIR = oldTryIR
		emitPrebuiltLLVMIR = oldEmit
	})

	objectPath := filepath.Join(t.TempDir(), "bundle.o")
	tryExternalPackageLLVMIR = func(entryPath string, gotPkg *resolve.Package) ([]byte, bool, []error, error) {
		if entryPath != path {
			t.Fatalf("entryPath = %q, want %q", entryPath, path)
		}
		if gotPkg != pkg {
			t.Fatal("got unexpected package pointer")
		}
		return []byte("; external ir"), true, nil, nil
	}
	emitPrebuiltLLVMIR = func(_ context.Context, req backend.Request, irOut []byte, warnings []error) (*backend.Result, error) {
		if req.Emit != backend.EmitObject {
			t.Fatalf("emit mode = %q, want object", req.Emit)
		}
		if req.BinaryName != "" {
			t.Fatalf("binaryName = %q, want empty", req.BinaryName)
		}
		if string(irOut) != "; external ir" {
			t.Fatalf("irOut = %q, want external ir", irOut)
		}
		return &backend.Result{
			Backend: backend.NameLLVM,
			Emit:    backend.EmitObject,
			Artifacts: backend.Artifacts{
				Object: objectPath,
			},
		}, nil
	}

	assets, err := compileNativeTestBundle(context.Background(), backend.LLVMBackend{}, t.TempDir(), pkg, nativeTestBundle{SourcePath: path})
	if err != nil {
		t.Fatalf("compileNativeTestBundle() error = %v", err)
	}
	if assets.ObjectPath != objectPath {
		t.Fatalf("object path = %q, want %q", assets.ObjectPath, objectPath)
	}
	if assets.RuntimeObjectPath != "" {
		t.Fatalf("runtime object path = %q, want empty when external result has no runtime dir", assets.RuntimeObjectPath)
	}
}

func TestRunTestMainReportsNativeLLVMFailure(t *testing.T) {
	requireClangForNativeTest(t)

	dir := t.TempDir()
	writeNativeTestFile(t, dir, "lib.osty", `pub fn add(a: Int, b: Int) -> Int {
    a + b
}
`)
	writeNativeTestFile(t, dir, "lib_test.osty", `use std.testing

fn testAddFails() {
    testing.assertEq(add(1, 2), 4)
}
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runTestMain([]string{dir}, cliFlags{}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("runTestMain() exit = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, "FAIL\ttestAddFails") || !strings.Contains(got, "testing.assertEq failed at") {
		t.Fatalf("stdout = %q, want failing assertion output", got)
	}
	// The native harness quotes the original source text of each
	// argument so the reader sees which expression diverged without
	// cross-referencing the file. `add(1, 2)` is the left operand,
	// `4` is the right — both must survive the LLVM emitter's span
	// capture intact. For scalar arguments (Int here) the runtime
	// value is also appended so the reader sees `= 3` for the
	// computed left side and `= 4` for the expected right side — the
	// actual delta in one glance.
	combined := stdout.String() + stderr.String()
	if !strings.Contains(combined, "left=`add(1, 2)` = 3") || !strings.Contains(combined, "right=`4` = 4") {
		t.Fatalf("output missing per-argument source text + runtime value quoting:\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if got := stderr.String(); !strings.Contains(got, "osty test: testAddFails: exit status 1") {
		t.Fatalf("stderr = %q, want native failure summary", got)
	}
}

func TestRunTestMainPassesNativeLLVMResultTests(t *testing.T) {
	requireClangForNativeTest(t)

	dir := t.TempDir()
	writeNativeTestFile(t, dir, "lib.osty", `pub enum CalcError {
    DivideByZero,
}

pub fn div(a: Int, b: Int) -> Result<Int, CalcError> {
    if b == 0 { Err(DivideByZero) } else { Ok(a / b) }
}
`)
	writeNativeTestFile(t, dir, "lib_test.osty", `use std.testing

fn testDiv() {
    let q = testing.expectOk(div(10, 2))
    testing.assertEq(q, 5)
    testing.expectError(div(1, 0))
}
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runTestMain([]string{dir}, cliFlags{}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runTestMain() exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, "ok\ttestDiv") || !strings.Contains(got, "ok\t1 tests passed") {
		t.Fatalf("stdout = %q, want Result-based passing test summary", got)
	}
	if got := stderr.String(); strings.TrimSpace(got) != "" {
		t.Fatalf("stderr = %q, want empty stderr", got)
	}
}

func TestRunTestMainReportsNativeLLVMExpectOkFailure(t *testing.T) {
	requireClangForNativeTest(t)

	dir := t.TempDir()
	writeNativeTestFile(t, dir, "lib.osty", `pub enum CalcError {
    DivideByZero,
}

pub fn div(a: Int, b: Int) -> Result<Int, CalcError> {
    if b == 0 { Err(DivideByZero) } else { Ok(a / b) }
}
`)
	writeNativeTestFile(t, dir, "lib_test.osty", `use std.testing

fn testExpectOkFails() {
    testing.expectOk(div(1, 0))
}
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runTestMain([]string{dir}, cliFlags{}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("runTestMain() exit = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, "FAIL\ttestExpectOkFails") || !strings.Contains(got, "testing.expectOk failed at") {
		t.Fatalf("stdout = %q, want expectOk failure output", got)
	}
	// expectOk also quotes the Result-producing expression so the
	// reader knows which call returned an error without jumping to
	// the source. Runtime value capture of the Result payload is
	// deferred (tracked separately with ToString protocol dispatch).
	combined := stdout.String() + stderr.String()
	if !strings.Contains(combined, "expr=`div(1, 0)`") {
		t.Fatalf("output missing expectOk expression quoting:\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if got := stderr.String(); !strings.Contains(got, "osty test: testExpectOkFails: exit status 1") {
		t.Fatalf("stderr = %q, want native expectOk failure summary", got)
	}
}

func TestRunTestMainReportsNativeLLVMAssertTrueFailure(t *testing.T) {
	requireClangForNativeTest(t)

	dir := t.TempDir()
	writeNativeTestFile(t, dir, "lib.osty", `pub fn even(n: Int) -> Bool {
    n % 2 == 0
}
`)
	writeNativeTestFile(t, dir, "lib_test.osty", `use std.testing

fn testAssertTrueFails() {
    testing.assertTrue(even(3))
}
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runTestMain([]string{dir}, cliFlags{}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("runTestMain() exit = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	combined := stdout.String() + stderr.String()
	// assertTrue quotes the condition expression — no runtime value
	// capture for Bool since it carries no extra information beyond
	// "failed" (the cond is always false on failure).
	if !strings.Contains(combined, "testing.assertTrue failed at") || !strings.Contains(combined, "cond=`even(3)`") {
		t.Fatalf("output missing assertTrue cond quoting:\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}

func TestRunTestMainReportsNativeLLVMAssertEqStringFailure(t *testing.T) {
	requireClangForNativeTest(t)

	dir := t.TempDir()
	writeNativeTestFile(t, dir, "lib.osty", `pub fn greet(name: String) -> String {
    "hi " + name
}
`)
	writeNativeTestFile(t, dir, "lib_test.osty", `use std.testing

fn testGreetFails() {
    testing.assertEq(greet("ada"), "hello ada")
}
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runTestMain([]string{dir}, cliFlags{}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("runTestMain() exit = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	combined := stdout.String() + stderr.String()
	// Strings are ptrs the runtime already knows how to print, so
	// the dispatcher emits them directly — `hi ada` and `hello ada`
	// must both appear verbatim alongside the quoted source text.
	if !strings.Contains(combined, "left=`greet(\"ada\")` = hi ada") || !strings.Contains(combined, `right=`+"`"+`"hello ada"`+"`"+` = hello ada`) {
		t.Fatalf("output missing String runtime value capture:\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}

func TestRunTestMainPassesNativeLLVMTupleTableTests(t *testing.T) {
	requireClangForNativeTest(t)

	dir := t.TempDir()
	writeNativeTestFile(t, dir, "lib.osty", `pub fn clamp(v: Int, lo: Int, hi: Int) -> Int {
    if v < lo { lo } else if v > hi { hi } else { v }
}
`)
	writeNativeTestFile(t, dir, "lib_test.osty", `use std.testing

fn testClampTable() {
    let cases = [
        (5, 0, 10, 5),
        (-1, 0, 10, 0),
        (99, 0, 10, 10),
    ]
    for c in cases {
        let (v, lo, hi, expected) = c
        testing.assertEq(clamp(v, lo, hi), expected)
    }
}

fn benchClampHotPath() {
    let _ = clamp(1, 0, 10)
}
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runTestMain([]string{dir}, cliFlags{}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runTestMain() exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, "ok\ttestClampTable") || !strings.Contains(got, "ok\t1 tests passed") {
		t.Fatalf("stdout = %q, want tuple-table passing test summary", got)
	}
	if got := stderr.String(); strings.TrimSpace(got) != "" {
		t.Fatalf("stderr = %q, want empty stderr", got)
	}
}

func TestRunTestMainPassesNativeLLVMManagedAggregateListTests(t *testing.T) {
	requireClangForNativeTest(t)

	dir := t.TempDir()
	writeNativeTestFile(t, dir, "lib.osty", `use runtime.strings as strings {
    fn Split(s: String, sep: String) -> List<String>
}

pub struct Bucket {
    items: List<String>,
}

pub fn bucketLen(bucket: Bucket) -> Int {
    bucket.items.len()
}
`)
	writeNativeTestFile(t, dir, "lib_test.osty", `use std.testing
use runtime.strings as strings {
    fn Split(s: String, sep: String) -> List<String>
}

fn testManagedAggregateList() {
    let buckets = [
        Bucket { items: strings.Split("gc,llvm", ",") },
    ]
    for bucket in buckets {
        testing.assertEq(bucketLen(bucket), 2)
    }
}
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runTestMain([]string{dir}, cliFlags{}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runTestMain() exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, "ok\ttestManagedAggregateList") || !strings.Contains(got, "ok\t1 tests passed") {
		t.Fatalf("stdout = %q, want managed-aggregate passing test summary", got)
	}
	if got := stderr.String(); strings.TrimSpace(got) != "" {
		t.Fatalf("stderr = %q, want empty stderr", got)
	}
}

func TestRunTestMainAIRepairAllowsForeignSyntaxBeforeDiscovery(t *testing.T) {
	dir := t.TempDir()
	// `x := 1` is residual foreign syntax — the parser front-end does not
	// normalize it (that only covers keyword aliases like `func`→`fn`,
	// promoted in commit 10a634d), so airepair is the only route that
	// lets the package reach discovery.
	writeNativeTestFile(t, dir, "helper.osty", "pub fn helper() -> Int {\n    x := 1\n    x\n}\n")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runTestMain([]string{dir}, cliFlags{}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runTestMain() auto airepair exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, "running 0 tests") {
		t.Fatalf("stdout = %q, want zero-test summary", got)
	}
	if got := stderr.String(); !strings.Contains(got, "osty test --airepair: applied 1 repair(s)") {
		t.Fatalf("stderr = %q, want in-memory airepair summary", got)
	}

	stdout.Reset()
	stderr.Reset()
	code = runTestMain([]string{"--airepair=false", dir}, cliFlags{}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("runTestMain() --airepair=false exit = %d, want parse failure\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
}

func TestRunTestMainBenchModeRunsBenchmark(t *testing.T) {
	requireClangForNativeTest(t)

	dir := t.TempDir()
	writeNativeTestFile(t, dir, "lib.osty", `pub fn add(a: Int, b: Int) -> Int {
    a + b
}
`)
	writeNativeTestFile(t, dir, "lib_test.osty", `use std.testing

fn benchAdd() {
    testing.benchmark(50, || {
        let _ = add(1, 2)
        Ok(())
    })
}

fn testShouldBeSkippedInBenchMode() {
    testing.assertEq(1, 2)
}
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	// Serial execution keeps stdout ordering stable so we can match
	// against the bench summary verbatim; the timing harness writes one
	// `bench …` line per call-site, and we want that line ordered after
	// its `ok` status line, not interleaved with a sibling benchmark.
	code := runTestMain([]string{"--bench", "--serial", dir}, cliFlags{}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runTestMain() --bench exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	got := stdout.String()
	// The prefix tells us discovery kicked the right bucket. Bench mode
	// must NOT run the failing testShouldBeSkippedInBenchMode — if it
	// had, the code would be 1 and we'd already be in the Fatal branch.
	if !strings.Contains(got, "running 1 benchmarks") {
		t.Fatalf("stdout = %q, want bench-mode discovery header", got)
	}
	if !strings.Contains(got, "ok\tbenchAdd") {
		t.Fatalf("stdout = %q, want benchAdd in ok list", got)
	}
	if !strings.Contains(got, "ok\t1 benchmarks passed") {
		t.Fatalf("stdout = %q, want benchmarks-passed summary", got)
	}
	// The bench harness emits a single summary line per testing.benchmark
	// call; prefix `bench ` and the `iter=/total=/avg=` keys are the
	// contract the CLI promises (§11.4). Numeric values are non-zero for
	// a 50-iteration loop on any clock with ns resolution.
	if !strings.Contains(got, "bench ") || !strings.Contains(got, "iter=50") ||
		!strings.Contains(got, "total=") || !strings.Contains(got, "avg=") ||
		!strings.Contains(got, "ns") {
		t.Fatalf("stdout = %q, want `bench … iter=50 total=…ns avg=…ns` summary", got)
	}
	if got := stderr.String(); strings.TrimSpace(got) != "" {
		t.Fatalf("stderr = %q, want empty stderr", got)
	}
}

func TestRunTestMainBenchModePrintsDistributionLine(t *testing.T) {
	requireClangForNativeTest(t)

	dir := t.TempDir()
	writeNativeTestFile(t, dir, "lib.osty", `pub fn add(a: Int, b: Int) -> Int {
    a + b
}
`)
	writeNativeTestFile(t, dir, "lib_test.osty", `use std.testing

fn benchAdd() {
    testing.benchmark(30, || {
        let _ = add(1, 2)
        Ok(())
    })
}
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runTestMain([]string{"--bench", "--serial", dir}, cliFlags{}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runTestMain() --bench exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	got := stdout.String()
	// Per-iter sampling feeds the distribution line. Values can all be
	// zero on coarse clocks, but the four keys are contractually
	// present whenever the bench ran.
	if !strings.Contains(got, "min=") || !strings.Contains(got, "p50=") ||
		!strings.Contains(got, "p99=") || !strings.Contains(got, "max=") {
		t.Fatalf("stdout = %q, want min=/p50=/p99=/max= distribution line", got)
	}
}

func TestRunTestMainBenchModeQuestionMarkOkPath(t *testing.T) {
	requireClangForNativeTest(t)

	dir := t.TempDir()
	writeNativeTestFile(t, dir, "lib.osty", `pub enum MyErr { Bad }

pub fn wrapped(x: Int) -> Result<Int, MyErr> {
    Ok(x + 1)
}
`)
	writeNativeTestFile(t, dir, "lib_test.osty", `use std.testing

fn benchWithTry() {
    testing.benchmark(20, || {
        let v = wrapped(5)?
        let _ = v
        Ok(())
    })
}
`)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runTestMain([]string{"--bench", "--serial", dir}, cliFlags{}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runTestMain() --bench Ok-path exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "ok\tbenchWithTry") {
		t.Fatalf("stdout = %q, want benchWithTry to pass on Ok path", stdout.String())
	}
}

func TestRunTestMainBenchModeQuestionMarkErrPathFails(t *testing.T) {
	requireClangForNativeTest(t)

	dir := t.TempDir()
	writeNativeTestFile(t, dir, "lib.osty", `pub enum MyErr { Bad }

pub fn wrapped(x: Int) -> Result<Int, MyErr> {
    Err(Bad)
}
`)
	writeNativeTestFile(t, dir, "lib_test.osty", `use std.testing

fn benchFailing() {
    testing.benchmark(20, || {
        let v = wrapped(5)?
        let _ = v
        Ok(())
    })
}
`)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runTestMain([]string{"--bench", "--serial", dir}, cliFlags{}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("runTestMain() --bench Err-path exit = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	combined := stdout.String() + stderr.String()
	if !strings.Contains(combined, "bench `?` propagated failure at") {
		t.Fatalf("output missing bench ? failure message:\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
}

func TestRunTestMainBenchTimeAutoTunes(t *testing.T) {
	requireClangForNativeTest(t)

	dir := t.TempDir()
	writeNativeTestFile(t, dir, "lib.osty", `pub fn add(a: Int, b: Int) -> Int {
    a + b
}
`)
	// The body declares N=5 but --benchtime overrides it. A 50ms
	// target on add(1,2) produces a final N of thousands, so `iter=5`
	// in the output would mean auto-tune didn't fire.
	writeNativeTestFile(t, dir, "lib_test.osty", `use std.testing

fn benchScaled() {
    testing.benchmark(5, || {
        let _ = add(1, 2)
        Ok(())
    })
}
`)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runTestMain([]string{"--bench", "--serial", "--benchtime", "50ms", dir}, cliFlags{}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runTestMain() --benchtime exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	got := stdout.String()
	if strings.Contains(got, "iter=5 ") {
		t.Fatalf("auto-tune did not fire, bench still ran 5 iters:\nstdout:\n%s", got)
	}
	if !strings.Contains(got, "iter=") {
		t.Fatalf("stdout = %q, want iter= summary", got)
	}
}

func TestRunTestMainBenchTimeRequiresBenchMode(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runTestMain([]string{"--benchtime", "100ms"}, cliFlags{}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("runTestMain() --benchtime alone exit = %d, want 2\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "--benchtime requires --bench") {
		t.Fatalf("stderr = %q, want --benchtime / --bench dependency diagnostic", stderr.String())
	}
}

func TestRunTestMainSkipsBenchInTestMode(t *testing.T) {
	requireClangForNativeTest(t)

	dir := t.TempDir()
	writeNativeTestFile(t, dir, "lib.osty", `pub fn noop() {
}
`)
	writeNativeTestFile(t, dir, "lib_test.osty", `use std.testing

fn testOk() {
    testing.assertEq(1, 1)
}

// Deliberately invalid: if bench mode discovery leaks into plain test
// mode this would run and fail because the naked 1 in a Result-returning
// closure doesn't typecheck. The correct behavior is to skip the bench
// entirely when --bench is absent.
fn benchShouldNotRun() {
    testing.assertEq(99, 0)
}
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runTestMain([]string{dir}, cliFlags{}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runTestMain() default exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	got := stdout.String()
	if !strings.Contains(got, "running 1 tests") || !strings.Contains(got, "ok\ttestOk") {
		t.Fatalf("stdout = %q, want only testOk in default mode", got)
	}
	if strings.Contains(got, "benchShouldNotRun") {
		t.Fatalf("stdout = %q, bench should not run in default test mode", got)
	}
}

func requireClangForNativeTest(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skipf("clang not available: %v", err)
	}
}

func writeNativeTestFile(t *testing.T, dir, name, contents string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func TestResolveTestSeedAcceptsDecimalAndHex(t *testing.T) {
	cases := []struct {
		in   string
		want uint64
	}{
		{"0", 0},
		{"42", 42},
		{"0x8F3A2B71", 0x8F3A2B71},
		{"0XFF", 0xFF},
	}
	for _, tc := range cases {
		got, err := resolveTestSeed(tc.in)
		if err != nil {
			t.Fatalf("resolveTestSeed(%q) error: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("resolveTestSeed(%q) = %#x, want %#x", tc.in, got, tc.want)
		}
	}
	if _, err := resolveTestSeed("not-a-number"); err == nil {
		t.Fatalf("resolveTestSeed(\"not-a-number\") expected error")
	}
}

func TestResolveTestSeedEmptyIsFresh(t *testing.T) {
	a, err := resolveTestSeed("")
	if err != nil {
		t.Fatalf("fresh seed: %v", err)
	}
	b, err := resolveTestSeed("")
	if err != nil {
		t.Fatalf("fresh seed: %v", err)
	}
	// Two fresh seeds almost certainly differ; if both are zero the RNG
	// source is broken and this test exists to surface that.
	if a == 0 && b == 0 {
		t.Fatalf("two fresh seeds were both zero — crypto/rand not wired?")
	}
}

func TestBenchChildEnvStripsAmbientTargetWhenUnset(t *testing.T) {
	parent := []string{
		"PATH=/usr/bin",
		"OSTY_BENCH_TIME_NS=1000000000",
		"HOME=/tmp",
	}
	got := benchChildEnv(parent, 0)
	for _, e := range got {
		if strings.HasPrefix(e, "OSTY_BENCH_TIME_NS=") {
			t.Fatalf("benchChildEnv must drop OSTY_BENCH_TIME_NS when caller passed 0, got %q", e)
		}
	}
	// Other vars must pass through untouched so the child inherits a
	// normal environment minus the one bench knob.
	seenPath, seenHome := false, false
	for _, e := range got {
		if e == "PATH=/usr/bin" {
			seenPath = true
		}
		if e == "HOME=/tmp" {
			seenHome = true
		}
	}
	if !seenPath || !seenHome {
		t.Fatalf("benchChildEnv dropped non-bench vars, got %v", got)
	}
}

func TestBenchChildEnvOverwritesAmbientTargetWhenSet(t *testing.T) {
	parent := []string{
		"PATH=/usr/bin",
		"OSTY_BENCH_TIME_NS=1000000000",
	}
	got := benchChildEnv(parent, 500_000_000)
	var ours string
	count := 0
	for _, e := range got {
		if strings.HasPrefix(e, "OSTY_BENCH_TIME_NS=") {
			count++
			ours = e
		}
	}
	// Exactly one OSTY_BENCH_TIME_NS entry — both the stale ambient
	// value and a duplicate-append would confuse the child's getenv.
	if count != 1 {
		t.Fatalf("expected exactly one OSTY_BENCH_TIME_NS in env, got %d (%v)", count, got)
	}
	if ours != "OSTY_BENCH_TIME_NS=500000000" {
		t.Fatalf("OSTY_BENCH_TIME_NS = %q, want our injected value", ours)
	}
}

func TestShuffleNativeTestsDeterministicPerSeed(t *testing.T) {
	base := []nativeTestCase{
		{Name: "a"}, {Name: "b"}, {Name: "c"}, {Name: "d"}, {Name: "e"},
		{Name: "f"}, {Name: "g"}, {Name: "h"}, {Name: "i"}, {Name: "j"},
	}
	clone := func() []nativeTestCase { return append([]nativeTestCase(nil), base...) }
	a := clone()
	b := clone()
	shuffleNativeTests(a, 0xDEADBEEF)
	shuffleNativeTests(b, 0xDEADBEEF)
	if !sameNativeTestOrder(a, b) {
		t.Fatalf("same seed produced different orders:\n  a=%v\n  b=%v", a, b)
	}
	c := clone()
	shuffleNativeTests(c, 0xCAFEBABE)
	if sameNativeTestOrder(a, c) {
		t.Fatalf("different seeds produced the same order: %v", a)
	}
}

func sameNativeTestOrder(a, b []nativeTestCase) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name {
			return false
		}
	}
	return true
}

func TestResolveTestWorkersClamps(t *testing.T) {
	if got := resolveTestWorkers(true, 4, 10); got != 1 {
		t.Fatalf("--serial should force 1 worker, got %d", got)
	}
	if got := resolveTestWorkers(false, 4, 2); got != 2 {
		t.Fatalf("workers should clamp to len(tests)=2, got %d", got)
	}
	if got := resolveTestWorkers(false, -3, 10); got < 1 {
		t.Fatalf("workers should default to NumCPU>=1, got %d", got)
	}
}

func TestFormatTestDurationSwitchesUnits(t *testing.T) {
	if got := formatTestDuration(120 * time.Millisecond); got != "120ms" {
		t.Fatalf("120ms format = %q", got)
	}
	if got := formatTestDuration(2500 * time.Millisecond); got != "2.50s" {
		t.Fatalf("2.5s format = %q", got)
	}
	if got := formatTestDuration(time.Microsecond); got != "1ms" {
		t.Fatalf("sub-millisecond rounds up to 1ms, got %q", got)
	}
}

func TestRunTestMainHeaderIncludesSeedAndTiming(t *testing.T) {
	requireClangForNativeTest(t)

	dir := t.TempDir()
	writeNativeTestFile(t, dir, "lib.osty", `pub fn add(a: Int, b: Int) -> Int {
    a + b
}
`)
	writeNativeTestFile(t, dir, "lib_test.osty", `use std.testing

fn testAdd() {
    testing.assertEq(add(1, 2), 3)
}
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runTestMain([]string{"--seed", "0x1234", dir}, cliFlags{}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runTestMain() exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	got := stdout.String()
	if !strings.Contains(got, "running 1 tests (seed 0x1234)") {
		t.Fatalf("stdout missing seeded header: %q", got)
	}
	// ok\ttestAdd\t<duration> — duration column is present.
	if !strings.Contains(got, "ok\ttestAdd\t") {
		t.Fatalf("stdout missing per-test timing column: %q", got)
	}
	if !strings.Contains(got, "seed 0x1234") {
		t.Fatalf("summary should echo the seed: %q", got)
	}
}

func TestRunTestMainSerialFlagRunsInShuffledOrder(t *testing.T) {
	requireClangForNativeTest(t)

	dir := t.TempDir()
	writeNativeTestFile(t, dir, "lib.osty", `pub fn id(x: Int) -> Int { x }
`)
	// Three tests — with --serial + fixed seed, output order must be
	// deterministic across runs.
	writeNativeTestFile(t, dir, "lib_test.osty", `use std.testing

fn testAlpha() { testing.assertEq(id(1), 1) }
fn testBravo() { testing.assertEq(id(2), 2) }
fn testCharlie() { testing.assertEq(id(3), 3) }
`)

	// Only the test-name order is seed-deterministic; wall-clock
	// durations legitimately differ between runs, so extract the name
	// sequence before comparing.
	capture := func() []string {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		code := runTestMain([]string{"--seed", "0xABCDEF", "--serial", dir}, cliFlags{}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("runTestMain() exit = %d\nstderr:\n%s", code, stderr.String())
		}
		var order []string
		for _, line := range strings.Split(stdout.String(), "\n") {
			if strings.HasPrefix(line, "ok\ttest") || strings.HasPrefix(line, "FAIL\ttest") {
				fields := strings.Split(line, "\t")
				if len(fields) >= 2 {
					order = append(order, fields[1])
				}
			}
		}
		return order
	}
	first := capture()
	second := capture()
	if strings.Join(first, ",") != strings.Join(second, ",") {
		t.Fatalf("--serial + fixed seed should reproduce the same order.\nfirst: %v\nsecond: %v", first, second)
	}
	if len(first) != 3 {
		t.Fatalf("expected 3 test status lines, got %v", first)
	}
	seen := map[string]bool{}
	for _, n := range first {
		seen[n] = true
	}
	for _, want := range []string{"testAlpha", "testBravo", "testCharlie"} {
		if !seen[want] {
			t.Fatalf("stdout missing %s status line; saw %v", want, first)
		}
	}
}
