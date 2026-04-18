package main

import (
	"testing"

	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
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
	tests, err := discoverNativeTests(pkg, nil)
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
	tests, err := discoverNativeTests(pkg, nil)
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
	tests, _ := discoverNativeTests(pkg, nil)
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

// mustResolveSingleFilePackage parses src, builds a one-file
// Package, and resolves it. Fails the test on any parse /
// resolve diagnostic.
func mustResolveSingleFilePackage(t *testing.T, path string, src []byte) *resolve.Package {
	t.Helper()
	file, diags := parser.ParseDiagnostics(src)
	for _, d := range diags {
		if d != nil && d.Severity.String() == "error" {
			t.Fatalf("parse error: %s", d.Message)
		}
	}
	if file == nil {
		t.Fatal("parse returned nil file")
	}
	pkg := &resolve.Package{
		Name: "inline_test_pkg",
		Dir:  ".",
		Files: []*resolve.PackageFile{{
			Path:   path,
			Source: src,
			File:   file,
		}},
	}
	return pkg
}
