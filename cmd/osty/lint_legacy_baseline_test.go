package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/osty/osty/internal/selfhost"
)

// TestRunLintFileLegacyAstbridgeBaseline completes the FILE legacy
// matrix: lint shares the same parser.ParseDetailed + resolveFile +
// check.File pipeline that runCheckFileLegacy already instruments,
// then adds the lint engine on top of the same *ast.File. No extra
// lowerings expected beyond the one the parse pass already does —
// lint.File walks the existing tree.
//
// Warm-path baseline: 1 bump per user-file invocation.
func TestRunLintFileLegacyAstbridgeBaseline(t *testing.T) {
	src := []byte(`fn main() {
    let x = 1
    let y = x + 2
    y
}
`)
	path := filepath.Join(t.TempDir(), "main.osty")
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	flags := cliFlags{noColor: true}
	formatter := newFormatter(path, src, flags)

	// Warm stdlib/checker caches so the measurement reflects the
	// per-invocation cost only (same rationale as
	// warmStdlibCachesForBaseline in check_legacy_baseline_test.go).
	captureStdouterr(t, func() {
		_ = runLintFileLegacy(path, src, formatter, flags)
	})

	selfhost.ResetAstbridgeLowerCount()
	var exit int
	captureStdouterr(t, func() {
		exit = runLintFileLegacy(path, src, formatter, flags)
	})
	if exit != 0 {
		t.Fatalf("runLintFileLegacy exit = %d, want 0", exit)
	}
	const want = 1
	if got := selfhost.AstbridgeLowerCount(); got != want {
		t.Fatalf("AstbridgeLowerCount after warm runLintFileLegacy = %d, want %d (lint shares check's one parse lowering — lint.File walks the existing *ast.File)", got, want)
	}
}

// TestRunLintPackageLegacyAstbridgeBaseline is the DIR lint sibling.
// Same cost shape as runCheckPackageLegacy: one *ast.File lowering
// per package file via resolve.LoadPackageWithTransform; the lint
// engine reuses those trees for its rules.
//
// Warm-path baseline: N bumps for an N-file package.
func TestRunLintPackageLegacyAstbridgeBaseline(t *testing.T) {
	dir := t.TempDir()
	aPath := filepath.Join(dir, "a.osty")
	bPath := filepath.Join(dir, "b.osty")
	if err := os.WriteFile(aPath, []byte(`pub fn helper() -> Int { 1 }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bPath, []byte(`fn main() {
    let x = 1
    let y = x + 2
    y
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	flags := cliFlags{noColor: true}

	captureStdouterr(t, func() {
		_ = runLintPackageLegacy(dir, flags)
	})

	selfhost.ResetAstbridgeLowerCount()
	var exit int
	captureStdouterr(t, func() {
		exit = runLintPackageLegacy(dir, flags)
	})
	if exit != 0 {
		t.Fatalf("runLintPackageLegacy exit = %d, want 0", exit)
	}
	const want = 2 // two source files, one *ast.File lowering each
	if got := selfhost.AstbridgeLowerCount(); got != want {
		t.Fatalf("AstbridgeLowerCount after warm runLintPackageLegacy = %d, want %d", got, want)
	}
}
