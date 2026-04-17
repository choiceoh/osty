package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	if got := stderr.String(); !strings.Contains(got, "osty test: testExpectOkFails: exit status 1") {
		t.Fatalf("stderr = %q, want native expectOk failure summary", got)
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
