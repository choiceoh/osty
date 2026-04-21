package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunTestMainAssertEqListIntShowsStructuralDiff is the end-to-end
// proof that assertEq on two `List<Int>` surfaces a line-level diff
// through the full `osty test` pipeline — parse, resolve, check, IR
// lower, LLVM codegen, clang compile, exec, runner capture. Before
// this change the message stopped at `left=...source-text...` with
// no value capture because the backend had no runtime formatter for
// composite types.
func TestRunTestMainAssertEqListIntShowsStructuralDiff(t *testing.T) {
	requireClangForNativeTest(t)

	dir := t.TempDir()
	writeNativeTestFile(t, dir, "lib_test.osty", `use std.testing as t

fn testListIntDiverges() {
    let a: List<Int> = [1, 2, 3]
    let b: List<Int> = [1, 99, 3]
    t.assertEq(a, b)
}
`)

	var stdout, stderr bytes.Buffer
	code := runTestMain([]string{dir}, cliFlags{}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("runTestMain() exit = %d, want 1 (assertEq should fail)\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	combined := stdout.String() + stderr.String()
	// The list formatter's 2-space element indent sits inside the
	// diff-line prefix, so the rendered marker is `-   2,` (diff `- `
	// + list `  2,`). Match against the combined shape to avoid an
	// off-by-2 when the indent policy shifts.
	for _, want := range []string{
		"FAIL\ttestListIntDiverges",
		"diff (- left, + right):",
		"-   2,",
		"+   99,",
	} {
		if !strings.Contains(combined, want) {
			t.Fatalf("List<Int> assertEq failure missing %q in runner output:\nstdout:\n%s\nstderr:\n%s", want, stdout.String(), stderr.String())
		}
	}
}

// TestRunTestMainSnapshotFirstRunCreatesGolden drives a fresh
// `testing.snapshot` call through the full `osty test` pipeline and
// asserts the happy path: the test passes, the golden gets written
// under `__snapshots__/`, and the "snapshot: created" line surfaces
// in the runner's per-test stdout. Redirects snapshot writes into
// the tempdir via OSTY_SNAPSHOT_DIR so no stray files land in the
// worktree even if the test blows up.
func TestRunTestMainSnapshotFirstRunCreatesGolden(t *testing.T) {
	requireClangForNativeTest(t)

	dir := t.TempDir()
	snapRoot := filepath.Join(dir, "snap-root")
	if err := os.Mkdir(snapRoot, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", snapRoot, err)
	}
	t.Setenv("OSTY_SNAPSHOT_DIR", snapRoot)
	t.Setenv("OSTY_UPDATE_SNAPSHOTS", "")

	writeNativeTestFile(t, dir, "lib_test.osty", `use std.testing as t

fn testSnapshotFirstRun() {
    t.snapshot("greeting", "hello\nworld\n")
}
`)

	var stdout, stderr bytes.Buffer
	code := runTestMain([]string{dir}, cliFlags{}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runTestMain() exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	// On pass, the runner only forwards the per-test `ok` line; the
	// child's `snapshot: created` stdout is intentionally quiet. The
	// observable side-effect is the golden file on disk.
	if !strings.Contains(stdout.String(), "ok\ttestSnapshotFirstRun") {
		t.Fatalf("stdout missing passing line:\n%s", stdout.String())
	}
	golden := filepath.Join(snapRoot, "__snapshots__", "greeting.snap")
	saved, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("golden not written at %q: %v", golden, err)
	}
	if got := string(saved); got != "hello\nworld\n" {
		t.Fatalf("golden content = %q, want %q", got, "hello\nworld\n")
	}
}

// TestRunTestMainSnapshotMismatchFailsWithDiff writes a golden that
// disagrees with the test's output and confirms the runner surfaces
// both the failure and the line-level diff to the user. The update
// hint must appear so callers discover --update-snapshots without
// reading source.
func TestRunTestMainSnapshotMismatchFailsWithDiff(t *testing.T) {
	requireClangForNativeTest(t)

	dir := t.TempDir()
	snapRoot := filepath.Join(dir, "snap-root")
	snapDir := filepath.Join(snapRoot, "__snapshots__")
	if err := os.MkdirAll(snapDir, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", snapDir, err)
	}
	// Pre-seed a disagreeing golden so the test sees a mismatch.
	if err := os.WriteFile(filepath.Join(snapDir, "greeting.snap"), []byte("hello\nold world\n"), 0o644); err != nil {
		t.Fatalf("seed golden: %v", err)
	}
	t.Setenv("OSTY_SNAPSHOT_DIR", snapRoot)
	t.Setenv("OSTY_UPDATE_SNAPSHOTS", "")

	writeNativeTestFile(t, dir, "lib_test.osty", `use std.testing as t

fn testSnapshotDiverges() {
    t.snapshot("greeting", "hello\nnew world\n")
}
`)

	var stdout, stderr bytes.Buffer
	code := runTestMain([]string{dir}, cliFlags{}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("runTestMain() exit = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	combined := stdout.String() + stderr.String()
	for _, want := range []string{
		"FAIL\ttestSnapshotDiverges",
		"mismatch",
		"OSTY_UPDATE_SNAPSHOTS=1",
		"- new world",
		"+ old world",
	} {
		if !strings.Contains(combined, want) {
			t.Fatalf("missing %q in runner output:\nstdout:\n%s\nstderr:\n%s", want, stdout.String(), stderr.String())
		}
	}
}

// TestRunTestMainUpdateSnapshotsFlag confirms the --update-snapshots
// CLI flag converts a would-be mismatch into a pass by overwriting
// the golden. This locks in that the flag actually propagates the
// OSTY_UPDATE_SNAPSHOTS env var to the child test binaries.
func TestRunTestMainUpdateSnapshotsFlag(t *testing.T) {
	requireClangForNativeTest(t)

	dir := t.TempDir()
	snapRoot := filepath.Join(dir, "snap-root")
	snapDir := filepath.Join(snapRoot, "__snapshots__")
	if err := os.MkdirAll(snapDir, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", snapDir, err)
	}
	goldenPath := filepath.Join(snapDir, "greeting.snap")
	if err := os.WriteFile(goldenPath, []byte("stale content\n"), 0o644); err != nil {
		t.Fatalf("seed golden: %v", err)
	}
	t.Setenv("OSTY_SNAPSHOT_DIR", snapRoot)
	// Ensure a stray outer setting doesn't mask the flag's effect.
	t.Setenv("OSTY_UPDATE_SNAPSHOTS", "")

	writeNativeTestFile(t, dir, "lib_test.osty", `use std.testing as t

fn testSnapshotUpdates() {
    t.snapshot("greeting", "fresh content\n")
}
`)

	var stdout, stderr bytes.Buffer
	code := runTestMain([]string{"--update-snapshots", dir}, cliFlags{}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runTestMain() --update-snapshots exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	// The observable side-effect of --update-snapshots is the golden
	// file being replaced — the child's `snapshot: updated` stdout is
	// swallowed on the passing path.
	saved, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read updated golden: %v", err)
	}
	if got := string(saved); got != "fresh content\n" {
		t.Fatalf("golden content after update = %q, want fresh content", got)
	}
}
