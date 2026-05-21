package main

import (
	"path/filepath"
	"testing"

	"github.com/osty/osty/internal/backend"
	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/mir"
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

// TestDiscoverNativeTestsAcceptsPackageWithMain locks the gate-lift:
// the test runner used to refuse any package that defined a top-level
// `main()` ("native test runner currently requires a library-style
// package"). That gate blocked `osty test toolchain/` because the
// toolchain CLI itself is a binary-style package (`toolchain/main.osty`
// declares `fn main()`). The discovery pass now skips `main` silently
// — same way it already skipped `testing` — and the link-time symbol
// clash with the C test driver's `main` is handled later by
// `stripMainFromEntry` filtering the lowered MIR + IR.
func TestDiscoverNativeTestsAcceptsPackageWithMain(t *testing.T) {
	src := []byte(`fn main() {
    let _ = 1
}

fn testReal() {
    let _ = 2
}

#[test]
fn inline_case() {
    let _ = 3
}
`)
	pkg := mustResolveSingleFilePackage(t, "with_main_test.osty", src)
	tests, err := discoverNativeTests(pkg, nil, false)
	if err != nil {
		t.Fatalf("discoverNativeTests returned error on package with main(): %v", err)
	}
	names := map[string]bool{}
	for _, tc := range tests {
		names[tc.Name] = true
	}
	if names["main"] {
		t.Error(`main must NOT be discovered as a test`)
	}
	if !names["testReal"] || !names["inline_case"] {
		t.Errorf("expected real tests to be discovered alongside main(), got %v", names)
	}
}

// TestStripMainFromEntryRemovesMainFromMIRAndIR pins the lowering-side
// half of the gate-lift: even though discovery accepts packages with
// `main()`, the LLVM backend would still produce a duplicate-symbol
// error when linked against the C test driver's `main`. The strip
// helper removes `main` from both the MIR and IR carried on the
// backend entry, mirroring `cmd/osty-native-llvmgen.stripMainForLibraryMode`
// which the external subprocess path triggers via `TryPackageLibrary`.
func TestStripMainFromEntryRemovesMainFromMIRAndIR(t *testing.T) {
	entry := &backend.Entry{
		MIR: &mir.Module{
			Functions: []*mir.Function{
				{Name: "main"},
				{Name: "testReal"},
				{Name: "helper"},
			},
		},
		IR: &ir.Module{
			Decls: []ir.Decl{
				&ir.FnDecl{Name: "main"},
				&ir.FnDecl{Name: "testReal"},
				&ir.FnDecl{Name: "helper"},
			},
		},
	}
	stripMainFromEntry(entry)
	if len(entry.MIR.Functions) != 2 {
		t.Fatalf("after strip MIR.Functions count = %d, want 2 (main removed)", len(entry.MIR.Functions))
	}
	for _, fn := range entry.MIR.Functions {
		if fn.Name == "main" {
			t.Fatal("stripMainFromEntry left main in MIR.Functions")
		}
	}
	if len(entry.IR.Decls) != 2 {
		t.Fatalf("after strip IR.Decls count = %d, want 2 (main removed)", len(entry.IR.Decls))
	}
	for _, d := range entry.IR.Decls {
		if fn, ok := d.(*ir.FnDecl); ok && fn.Name == "main" {
			t.Fatal("stripMainFromEntry left main in IR.Decls")
		}
	}
}

// TestStripMainFromEntryHandlesNilSafely guards the defensive nil
// branches; the helper is called unconditionally from
// `prepareNativeTestBackendEntry` and must not panic on the legitimate
// "MIR-only" or "IR-only" entry shapes the backend produces for
// different lowering routes.
func TestStripMainFromEntryHandlesNilSafely(t *testing.T) {
	// nil entry
	stripMainFromEntry(nil)
	// nil MIR + IR
	stripMainFromEntry(&backend.Entry{})
	// IR present, MIR nil
	stripMainFromEntry(&backend.Entry{IR: &ir.Module{Decls: []ir.Decl{&ir.FnDecl{Name: "main"}}}})
	// MIR present, IR nil
	stripMainFromEntry(&backend.Entry{MIR: &mir.Module{Functions: []*mir.Function{{Name: "main"}}}})
	// nil entries inside Functions / Decls
	entry := &backend.Entry{
		MIR: &mir.Module{Functions: []*mir.Function{nil, {Name: "main"}, nil, {Name: "ok"}}},
		IR:  &ir.Module{Decls: []ir.Decl{nil, &ir.FnDecl{Name: "main"}, nil, &ir.FnDecl{Name: "ok"}}},
	}
	stripMainFromEntry(entry)
	if len(entry.MIR.Functions) != 1 || entry.MIR.Functions[0].Name != "ok" {
		t.Fatalf("nil/main filter MIR result = %v, want [ok]", entry.MIR.Functions)
	}
	if len(entry.IR.Decls) != 1 {
		t.Fatalf("nil/main filter IR result count = %d, want 1", len(entry.IR.Decls))
	}
	if fn, ok := entry.IR.Decls[0].(*ir.FnDecl); !ok || fn.Name != "ok" {
		t.Fatalf("nil/main filter IR result = %v, want [ok]", entry.IR.Decls)
	}
}
