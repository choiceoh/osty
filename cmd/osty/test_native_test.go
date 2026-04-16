package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
