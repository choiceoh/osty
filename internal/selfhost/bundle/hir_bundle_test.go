package bundle

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestToolchainHirBundleIncludesHirLower(t *testing.T) {
	files := ToolchainHirFiles()
	if len(files) == 0 {
		t.Fatal("ToolchainHirFiles() returned empty list")
	}
	expected := []string{
		"toolchain/ty.osty",
		"toolchain/hir.osty",
		"toolchain/hir_clone.osty",
		"toolchain/monomorph_pass.osty",
		"toolchain/pmcompile.osty",
		"toolchain/hir_lower.osty",
	}
	seen := make(map[string]bool)
	for _, f := range files {
		seen[f] = true
	}
	for _, want := range expected {
		if !seen[want] {
			t.Errorf("ToolchainHirFiles() missing %q", want)
		}
	}
}

func TestToolchainMirBundleIncludesMirLower(t *testing.T) {
	files := ToolchainMirFiles()
	expected := []string{
		"toolchain/hir.osty",
		"toolchain/hir_lower.osty",
		"toolchain/mir.osty",
		"toolchain/mir_lower.osty",
		"toolchain/mir_optimize.osty",
		"toolchain/mir_validator.osty",
	}
	seen := make(map[string]bool)
	for _, f := range files {
		seen[f] = true
	}
	for _, want := range expected {
		if !seen[want] {
			t.Errorf("ToolchainMirFiles() missing %q", want)
		}
	}
}

func TestMergeToolchainHirContainsSignatures(t *testing.T) {
	root := repoRoot(t)
	merged, err := MergeToolchainHir(root)
	if err != nil {
		t.Fatalf("MergeToolchainHir() error = %v", err)
	}
	if len(merged) == 0 {
		t.Fatal("MergeToolchainHir() returned empty output")
	}
	// Spot-check that the HIR lowerer's public surface landed in the
	// merged source — if the bundle file list accidentally loses
	// hir_lower.osty, these checks trip.
	mustContain(t, merged, "pub fn hirLowerer(")
	mustContain(t, merged, "pub fn hirLowerModule(")
	mustContain(t, merged, "pub fn hirLowerExtractInlineMode(")
	mustContain(t, merged, "pub fn hirLowerExtractVectorizeArgs(")
	// HIR data model should also be in scope.
	mustContain(t, merged, "pub struct HirModule")
	// The merged output must have been scrubbed of `use std.strings`
	// imports by the shared merger.
	if bytes.Contains(merged, []byte("\nuse std.strings")) {
		t.Fatal("merged HIR bundle still contains `use std.strings` imports")
	}
}

func TestMergeToolchainMirContainsSignatures(t *testing.T) {
	root := repoRoot(t)
	merged, err := MergeToolchainMir(root)
	if err != nil {
		t.Fatalf("MergeToolchainMir() error = %v", err)
	}
	if len(merged) == 0 {
		t.Fatal("MergeToolchainMir() returned empty output")
	}
	mustContain(t, merged, "pub fn mirLowerModule(")
	mustContain(t, merged, "pub fn mirLowerStmt(")
	mustContain(t, merged, "pub fn mirLowerExprToRValue(")
	mustContain(t, merged, "pub struct MirModule")
	if bytes.Contains(merged, []byte("\nuse std.strings")) {
		t.Fatal("merged MIR bundle still contains `use std.strings` imports")
	}
}

func mustContain(t *testing.T, haystack []byte, needle string) {
	t.Helper()
	if !bytes.Contains(haystack, []byte(needle)) {
		t.Errorf("merged bundle missing expected text %q", needle)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	// Locate the repo root by walking up until we find go.mod — tests
	// run with the package dir as cwd, so the relative path varies.
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not locate go.mod above %s", strings.TrimSpace(dir))
		}
		dir = parent
	}
}
