package main

import (
	"path/filepath"
	"testing"

	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/selfhost"
)

// v0.5 (G32) inline `#[test]` discovery — the legacy `test*` name
// prefix convention continues to work, and functions carrying
// `#[test]` are picked up regardless of their name.

func TestDiscoverNativeTestsPicksUpInlineTestAnnotation(t *testing.T) {
	src := []byte(`pub fn add(a: Int, b: Int) -> Int { a + b }

#[test]
fn sum_of_two() {
    let _ = add(2, 3)
}

#[test]
fn another_case() {
    let _ = add(0, 0)
}

fn testLegacyPrefix() {
    let _ = add(1, 1)
}

fn notATest() -> Int {
    42
}
`)
	pkg := mustResolveSingleFilePackage(t, "inline_test.osty", src)
	tests, err := discoverNativeTests(pkg, nil, false)
	if err != nil {
		t.Fatalf("discoverNativeTests: %v", err)
	}
	wantNames := map[string]bool{
		"sum_of_two":       true,
		"another_case":     true,
		"testLegacyPrefix": true,
	}
	gotNames := map[string]bool{}
	for _, tc := range tests {
		gotNames[tc.Name] = true
	}
	for want := range wantNames {
		if !gotNames[want] {
			t.Errorf("expected %q to be discovered, got %v", want, gotNames)
		}
	}
	if gotNames["notATest"] {
		t.Error("notATest should not be discovered (no #[test], no test prefix)")
	}
	if gotNames["add"] {
		t.Error("add should not be discovered (production function)")
	}
	for _, pf := range pkg.Files {
		if pf.File != nil {
			t.Fatalf("discoverNativeTests materialized public AST for %s", pf.Path)
		}
	}
}

func TestDiscoverNativeTestsSkipsTestingName(t *testing.T) {
	src := []byte(`#[test]
fn testing() {
    let _ = 1
}

fn testReal() {
    let _ = 1
}
`)
	pkg := mustResolveSingleFilePackage(t, "testing_skip_test.osty", src)
	tests, err := discoverNativeTests(pkg, nil, false)
	if err != nil {
		t.Fatalf("discoverNativeTests: %v", err)
	}
	for _, tc := range tests {
		if tc.Name == "testing" {
			t.Error(`function named "testing" must always be skipped, even with #[test]`)
		}
	}
}

func TestDiscoverNativeTestsRejectsInlineTestWithParams(t *testing.T) {
	// `#[test]` functions must be zero-arity / zero-return. A function
	// with params that also carries `#[test]` is silently skipped so
	// the test harness never tries to invoke it with no arguments.
	src := []byte(`#[test]
fn withParam(n: Int) {
    let _ = n
}

#[test]
fn goodTest() {
    let _ = 1
}
`)
	pkg := mustResolveSingleFilePackage(t, "param_test.osty", src)
	tests, _ := discoverNativeTests(pkg, nil, false)
	for _, tc := range tests {
		if tc.Name == "withParam" {
			t.Error("#[test] on a function with parameters must be skipped, not discovered")
		}
	}
	foundGood := false
	for _, tc := range tests {
		if tc.Name == "goodTest" {
			foundGood = true
		}
	}
	if !foundGood {
		t.Error("goodTest should be discovered")
	}
}

func TestDiscoverNativeTestsSplitsTestAndBench(t *testing.T) {
	src := []byte(`fn testAlpha() { let _ = 1 }

fn benchAlpha() { let _ = 1 }

#[test]
fn inline_case() { let _ = 1 }
`)
	pkg := mustResolveSingleFilePackage(t, "mix_test.osty", src)

	// Default (test) mode: bench-prefixed functions are ignored so the
	// runner never accidentally invokes a benchmark harness as a plain
	// test. `#[test]` annotation is picked up regardless of name prefix.
	testMode, err := discoverNativeTests(pkg, nil, false)
	if err != nil {
		t.Fatalf("discoverNativeTests(test): %v", err)
	}
	names := map[string]bool{}
	for _, tc := range testMode {
		names[tc.Name] = true
	}
	if !names["testAlpha"] || !names["inline_case"] {
		t.Errorf("test-mode missing expected names: %v", names)
	}
	if names["benchAlpha"] {
		t.Error("test-mode must not pick up bench-prefixed functions")
	}

	// Bench mode (spec §11.4): only `bench*` names are discovered.
	benchMode, err := discoverNativeTests(pkg, nil, true)
	if err != nil {
		t.Fatalf("discoverNativeTests(bench): %v", err)
	}
	benchNames := map[string]bool{}
	for _, tc := range benchMode {
		benchNames[tc.Name] = true
	}
	if !benchNames["benchAlpha"] {
		t.Errorf("bench-mode should discover benchAlpha, got %v", benchNames)
	}
	if benchNames["testAlpha"] || benchNames["inline_case"] {
		t.Errorf("bench-mode must not pull test-prefixed / #[test] functions, got %v", benchNames)
	}
}

func TestPrepareNativeTestBackendEntryFallbackKeepsNativePackageScopes(t *testing.T) {
	dir := t.TempDir()
	helperPath := filepath.Join(dir, "helper.osty")
	testPath := filepath.Join(dir, "main_test.osty")
	helperSrc := []byte("pub fn helper() -> Int { 1 }\n")
	testSrc := []byte("fn testUsesHelper() { let _ = helper() }\n")
	pkg := &resolve.Package{
		Name: "native_test_pkg",
		Dir:  dir,
		Files: []*resolve.PackageFile{
			{Path: helperPath, Source: helperSrc, Run: selfhost.Run(helperSrc)},
			{Path: testPath, Source: testSrc, Run: selfhost.Run(testSrc)},
		},
	}
	for _, pf := range pkg.Files {
		if pf.File != nil {
			t.Fatalf("test setup unexpectedly materialized %s", pf.Path)
		}
	}
	if got := countLowerableFiles(pkg); got != 2 {
		t.Fatalf("countLowerableFiles(native run-only package) = %d, want 2", got)
	}

	entry, err := prepareNativeTestBackendEntry(testPath, pkg)
	if err != nil {
		t.Fatalf("prepareNativeTestBackendEntry() error = %v", err)
	}
	if pkg.Files[0].File == nil || pkg.Files[1].File == nil {
		t.Fatal("package lowering fallback did not materialize native-owned package files")
	}
	if entry.File != pkg.Files[1].File {
		t.Fatal("backend entry did not keep the original package entry file")
	}
	if string(entry.Source) != string(testSrc) {
		t.Fatalf("backend entry source = %q, want entry-file source; fallback must not merge package text", entry.Source)
	}
	if entry.IR == nil || len(entry.IR.Decls) != 2 {
		t.Fatalf("backend entry decl count = %d, want 2 package decls", len(entry.IR.Decls))
	}
}

// mustResolveSingleFilePackage parses src, builds a one-file
// Package, and resolves it. Fails the test on any parse /
// resolve diagnostic.
func mustResolveSingleFilePackage(t *testing.T, path string, src []byte) *resolve.Package {
	t.Helper()
	run := selfhost.Run(src)
	diags := run.Diagnostics()
	for _, d := range diags {
		if d != nil && d.Severity.String() == "error" {
			t.Fatalf("parse error: %s", d.Message)
		}
	}
	pkg := &resolve.Package{
		Name: "inline_test_pkg",
		Dir:  ".",
		Files: []*resolve.PackageFile{{
			Path:   path,
			Source: src,
			Run:    run,
		}},
	}
	return pkg
}
