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
