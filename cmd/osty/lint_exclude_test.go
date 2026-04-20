package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeLintExcludeManifest scaffolds a project dir with a `[lint]
// exclude = ["gen/**"]` osty.toml and returns the dir. Used by both
// single-file lint exclude tests so their manifest shape stays in sync.
func writeLintExcludeManifest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	manifest := strings.Join([]string{
		`[package]`,
		`name = "demo"`,
		`version = "0.1.0"`,
		`edition = "0.3"`,
		``,
		`[lint]`,
		`exclude = ["gen/**"]`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "osty.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return dir
}

func TestLintSingleFileExcludeAnnouncesSkip(t *testing.T) {
	t.Parallel()
	dir := writeLintExcludeManifest(t)
	genDir := filepath.Join(dir, "gen")
	if err := os.MkdirAll(genDir, 0o755); err != nil {
		t.Fatalf("mkdir gen: %v", err)
	}
	excluded := filepath.Join(genDir, "noisy.osty")
	if err := os.WriteFile(excluded, []byte("fn main() {}\n"), 0o644); err != nil {
		t.Fatalf("write excluded: %v", err)
	}

	got := runOstyCLI(t, "lint", excluded)
	if got.exit != 0 {
		t.Fatalf("osty lint exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}
	if !strings.Contains(got.stderr, "skipping") {
		t.Fatalf("stderr = %q, want a skip announcement so the exit 0 isn't confused with lint success", got.stderr)
	}
	if !strings.Contains(got.stderr, excluded) {
		t.Fatalf("stderr = %q, want skip message to name the excluded file %s", got.stderr, excluded)
	}
	if !strings.Contains(got.stderr, `"gen/**"`) {
		t.Fatalf("stderr = %q, want skip message to quote the matching exclude pattern", got.stderr)
	}
}

func TestLintSingleFileNonExcludedStaysQuiet(t *testing.T) {
	t.Parallel()
	dir := writeLintExcludeManifest(t)
	included := filepath.Join(dir, "main.osty")
	if err := os.WriteFile(included, []byte("fn main() {}\n"), 0o644); err != nil {
		t.Fatalf("write included: %v", err)
	}

	got := runOstyCLI(t, "lint", included)
	if got.exit != 0 {
		t.Fatalf("osty lint exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}
	if strings.Contains(got.stderr, "skipping") {
		t.Fatalf("stderr = %q, did not want skip announcement for a non-excluded file", got.stderr)
	}
}
