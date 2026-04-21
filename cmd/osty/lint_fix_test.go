package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFixManifest scaffolds a minimal package manifest so package-mode
// lint resolves. Used by all --fix package tests.
func writeFixManifest(t *testing.T, dir string) {
	t.Helper()
	manifest := strings.Join([]string{
		`[package]`,
		`name = "fixdemo"`,
		`version = "0.1.0"`,
		`edition = "0.3"`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "osty.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

// TestLintFixSingleFileRewritesUnusedBinding exercises the pre-existing
// single-file --fix path. L0001 carries a rename-to-_prefix fix; after
// --fix the file should name the binding `_noisy`.
func TestLintFixSingleFileRewritesUnusedBinding(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixManifest(t, dir)
	src := "fn main() {\n    let noisy = 1\n}\n"
	path := filepath.Join(dir, "main.osty")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	got := runOstyCLI(t, "lint", "--fix", path)
	if got.exit != 0 {
		t.Fatalf("exit = %d, want 0\nstderr:\n%s", got.exit, got.stderr)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read post-fix: %v", err)
	}
	if !strings.Contains(string(data), "_noisy") {
		t.Fatalf("post-fix file = %q, want the L0001 rename to `_noisy`", string(data))
	}
	if !strings.Contains(got.stderr, "applied") {
		t.Fatalf("stderr = %q, want summary line reporting applied fixes", got.stderr)
	}
}

// TestLintFixPackageModeRewritesEveryFile verifies the package-mode
// --fix path added alongside this test. Two files each have a distinct
// unused binding; both should be rewritten in a single invocation.
func TestLintFixPackageModeRewritesEveryFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixManifest(t, dir)
	aSrc := "fn alpha() {\n    let droppedA = 1\n}\n"
	bSrc := "fn beta() {\n    let droppedB = 2\n}\n"
	aPath := filepath.Join(dir, "a.osty")
	bPath := filepath.Join(dir, "b.osty")
	for path, body := range map[string]string{aPath: aSrc, bPath: bSrc} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	got := runOstyCLI(t, "lint", "--fix", dir)
	if got.exit != 0 {
		t.Fatalf("exit = %d, want 0\nstderr:\n%s", got.exit, got.stderr)
	}
	aAfter, _ := os.ReadFile(aPath)
	bAfter, _ := os.ReadFile(bPath)
	if !strings.Contains(string(aAfter), "_droppedA") {
		t.Fatalf("a.osty post-fix = %q, want rename to `_droppedA`", string(aAfter))
	}
	if !strings.Contains(string(bAfter), "_droppedB") {
		t.Fatalf("b.osty post-fix = %q, want rename to `_droppedB`", string(bAfter))
	}
	if !strings.Contains(got.stderr, "applied 2 fix") {
		t.Fatalf("stderr = %q, want summary to mention both fixes applied", got.stderr)
	}
}

// TestLintFixDryRunLeavesFilesUnchanged verifies that --fix-dry-run
// prints the post-fix text to stdout but does NOT write back to disk.
func TestLintFixDryRunLeavesFilesUnchanged(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixManifest(t, dir)
	src := "fn main() {\n    let noisy = 1\n}\n"
	path := filepath.Join(dir, "main.osty")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	got := runOstyCLI(t, "lint", "--fix-dry-run", path)
	if got.exit != 0 {
		t.Fatalf("exit = %d, want 0\nstderr:\n%s", got.exit, got.stderr)
	}
	after, _ := os.ReadFile(path)
	if string(after) != src {
		t.Fatalf("source mutated under --fix-dry-run: got %q, want %q", string(after), src)
	}
	if !strings.Contains(got.stdout, "_noisy") {
		t.Fatalf("stdout = %q, want post-fix preview mentioning `_noisy`", got.stdout)
	}
	if !strings.Contains(got.stderr, "would apply") {
		t.Fatalf("stderr = %q, want dry-run summary line", got.stderr)
	}
}

// TestLintFixPackageDryRunHeadsEachFile verifies the dry-run preview
// prefixes each changed file with a `// ==== path ====` header so users
// can diff per-file in a multi-file package.
func TestLintFixPackageDryRunHeadsEachFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixManifest(t, dir)
	aSrc := "fn alpha() {\n    let droppedA = 1\n}\n"
	bSrc := "fn beta() {\n    let droppedB = 2\n}\n"
	aPath := filepath.Join(dir, "a.osty")
	bPath := filepath.Join(dir, "b.osty")
	for path, body := range map[string]string{aPath: aSrc, bPath: bSrc} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	got := runOstyCLI(t, "lint", "--fix-dry-run", dir)
	if got.exit != 0 {
		t.Fatalf("exit = %d, want 0\nstderr:\n%s", got.exit, got.stderr)
	}
	if !strings.Contains(got.stdout, "// ==== ") {
		t.Fatalf("stdout = %q, want per-file header markers", got.stdout)
	}
	if !strings.Contains(got.stdout, "a.osty") || !strings.Contains(got.stdout, "b.osty") {
		t.Fatalf("stdout = %q, want both filenames in the dry-run preview", got.stdout)
	}
}
