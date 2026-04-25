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

// TestLintHierarchicalConfigMergeExclude verifies that Exclude patterns
// from a parent osty.toml are inherited when a child osty.toml also
// exists. The child adds its own "gen/**" exclusion while the parent
// declares "vendor/**". Files under vendor/ in the child directory
// should still be skipped because the parent pattern is merged.
func TestLintHierarchicalConfigMergeExclude(t *testing.T) {
	t.Parallel()

	// Build: root/osty.toml (parent) -> root/child/osty.toml (child)
	root := t.TempDir()

	parentManifest := strings.Join([]string{
		`[package]`,
		`name = "workspace"`,
		`version = "0.1.0"`,
		`edition = "0.3"`,
		``,
		`[lint]`,
		`exclude = ["vendor/**"]`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(root, "osty.toml"), []byte(parentManifest), 0o644); err != nil {
		t.Fatalf("write parent manifest: %v", err)
	}

	childDir := filepath.Join(root, "child")
	if err := os.MkdirAll(childDir, 0o755); err != nil {
		t.Fatalf("mkdir child: %v", err)
	}

	childManifest := strings.Join([]string{
		`[package]`,
		`name = "child"`,
		`version = "0.1.0"`,
		`edition = "0.3"`,
		``,
		`[lint]`,
		`exclude = ["gen/**"]`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(childDir, "osty.toml"), []byte(childManifest), 0o644); err != nil {
		t.Fatalf("write child manifest: %v", err)
	}

	// Create a vendor/ dir inside child/ — the parent's "vendor/**"
	// pattern should still match after merging.
	vendorDir := filepath.Join(childDir, "vendor")
	if err := os.MkdirAll(vendorDir, 0o755); err != nil {
		t.Fatalf("mkdir vendor: %v", err)
	}
	vendored := filepath.Join(vendorDir, "dep.osty")
	if err := os.WriteFile(vendored, []byte("fn main() {}\n"), 0o644); err != nil {
		t.Fatalf("write vendored: %v", err)
	}

	got := runOstyCLI(t, "lint", vendored)
	if got.exit != 0 {
		t.Fatalf("osty lint exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}
	if !strings.Contains(got.stderr, "skipping") {
		t.Fatalf("stderr = %q, want a skip announcement for vendor/** inherited from parent config", got.stderr)
	}
	if !strings.Contains(got.stderr, `"vendor/**"`) {
		t.Fatalf("stderr = %q, want skip message to quote the parent exclude pattern vendor/**", got.stderr)
	}
}

// TestLintHierarchicalConfigMergeDeny verifies that a child's Deny
// list overrides the parent's, not accumulates. The parent denies
// "dead_code"; the child denies "self_assign". Only the child's
// denial should apply when linting inside the child directory.
func TestLintHierarchicalConfigMergeDeny(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	parentManifest := strings.Join([]string{
		`[package]`,
		`name = "workspace"`,
		`version = "0.1.0"`,
		`edition = "0.3"`,
		``,
		`[lint]`,
		`deny = ["dead_code"]`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(root, "osty.toml"), []byte(parentManifest), 0o644); err != nil {
		t.Fatalf("write parent manifest: %v", err)
	}

	childDir := filepath.Join(root, "child")
	if err := os.MkdirAll(childDir, 0o755); err != nil {
		t.Fatalf("mkdir child: %v", err)
	}

	childManifest := strings.Join([]string{
		`[package]`,
		`name = "child"`,
		`version = "0.1.0"`,
		`edition = "0.3"`,
		``,
		`[lint]`,
		`deny = ["self_assign"]`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(childDir, "osty.toml"), []byte(childManifest), 0o644); err != nil {
		t.Fatalf("write child manifest: %v", err)
	}

	// self_assign (denied by child) should be promoted to error.
	childFile := filepath.Join(childDir, "main.osty")
	// x = x is a self-assignment (L0042).
	src := "fn main() {\n    let mut x = 1\n    x = x\n}\n"
	if err := os.WriteFile(childFile, []byte(src), 0o644); err != nil {
		t.Fatalf("write child file: %v", err)
	}

	got := runOstyCLI(t, "lint", childFile)
	// self_assign is denied by the child config, so the lint warning
	// should be elevated to error severity → non-zero exit.
	if got.exit == 0 {
		t.Fatalf("osty lint exit = 0, want non-zero (self_assign denied by child config)\nstdout:\n%s\nstderr:\n%s", got.stdout, got.stderr)
	}
	if !strings.Contains(got.stdout, "L0042") && !strings.Contains(got.stderr, "L0042") {
		t.Fatalf("output should contain L0042 (self_assign)\nstdout:\n%s\nstderr:\n%s", got.stdout, got.stderr)
	}
}

// TestLintHierarchicalConfigChildAllowOverridesParentDeny verifies that
// when a parent denies a code but the child allows it, the child's
// Allow wins — demonstrating the child-override semantics.
func TestLintHierarchicalConfigChildAllowOverridesParentDeny(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	// Parent denies unused_let (L0001).
	parentManifest := strings.Join([]string{
		`[package]`,
		`name = "workspace"`,
		`version = "0.1.0"`,
		`edition = "0.3"`,
		``,
		`[lint]`,
		`deny = ["unused_let"]`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(root, "osty.toml"), []byte(parentManifest), 0o644); err != nil {
		t.Fatalf("write parent manifest: %v", err)
	}

	childDir := filepath.Join(root, "child")
	if err := os.MkdirAll(childDir, 0o755); err != nil {
		t.Fatalf("mkdir child: %v", err)
	}

	// Child allows unused_let — this overrides the parent's deny.
	childManifest := strings.Join([]string{
		`[package]`,
		`name = "child"`,
		`version = "0.1.0"`,
		`edition = "0.3"`,
		``,
		`[lint]`,
		`allow = ["unused_let"]`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(childDir, "osty.toml"), []byte(childManifest), 0o644); err != nil {
		t.Fatalf("write child manifest: %v", err)
	}

	// unused_let (L0001) is normally a warning but parent denies it.
	// Child allows it → child's Allow overrides parent's Deny.
	childFile := filepath.Join(childDir, "main.osty")
	src := "fn main() {\n    let unused = 42\n}\n"
	if err := os.WriteFile(childFile, []byte(src), 0o644); err != nil {
		t.Fatalf("write child file: %v", err)
	}

	got := runOstyCLI(t, "lint", childFile)
	// Child's allow should suppress the warning entirely.
	if got.exit != 0 {
		t.Fatalf("osty lint exit = %d, want 0 (unused_let allowed by child config)\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}
	if strings.Contains(got.stdout, "L0001") {
		t.Fatalf("stdout should not contain L0001 — child allows it\nstdout:\n%s\nstderr:\n%s", got.stdout, got.stderr)
	}
}
