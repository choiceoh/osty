package backend

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestBundledRuntimeSnapshotLifecycle exercises the three legs of
// osty_rt_test_snapshot through the real C runtime compiled by clang:
//
//  1. First call with no existing golden → writes the file + prints a
//     "created" line to stdout + exits 0.
//  2. Second call with a matching payload → no output, exits 0.
//  3. Third call with a different payload → prints "mismatch" + a
//     line-level diff + exits 1.
//
// The test redirects snapshot writes via OSTY_SNAPSHOT_DIR so nothing
// pollutes the repo; the runtime still applies the sanitize +
// __snapshots__ subdir rules the same way as a real test binary.
func TestBundledRuntimeSnapshotLifecycle(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_snapshot_harness.c")
	binaryPath := filepath.Join(dir, "runtime_snapshot_harness")
	snapshotRoot := filepath.Join(dir, "snap-root")
	if err := os.Mkdir(snapshotRoot, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", snapshotRoot, err)
	}

	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}

	// Harness drives the public snapshot entry point with whatever
	// payload argv[1] contains. argv[2] sets the snapshot name so we
	// can exercise the filename sanitizer separately. We can't easily
	// test "exit(1) on mismatch" from a single run, so the test
	// orchestrates multiple invocations from Go.
	harness := `#include <stdio.h>
#include <stdlib.h>
#include <string.h>

const char *osty_rt_strings_DiffLines(const char *actual, const char *expected);
void osty_rt_test_snapshot(const char *name, const char *output, const char *source_path);

int main(int argc, char **argv) {
    if (argc < 4) {
        fprintf(stderr, "usage: %s MODE NAME PAYLOAD\n", argv[0]);
        return 2;
    }
    const char *mode = argv[1];
    const char *name = argv[2];
    const char *payload = argv[3];
    const char *source = "/synthetic/source/file.osty";
    if (strcmp(mode, "snapshot") == 0) {
        osty_rt_test_snapshot(name, payload, source);
        return 0;
    }
    if (strcmp(mode, "diff") == 0) {
        if (argc < 5) {
            fprintf(stderr, "diff mode needs two payloads\n");
            return 2;
        }
        const char *diff = osty_rt_strings_DiffLines(argv[3], argv[4]);
        printf("%s", diff == NULL ? "" : diff);
        return 0;
    }
    fprintf(stderr, "unknown mode %s\n", mode);
    return 2;
}
`
	if err := os.WriteFile(harnessPath, []byte(harness), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", harnessPath, err)
	}

	buildOutput, err := exec.Command("clang", "-std=c11", runtimePath, harnessPath, "-o", binaryPath).CombinedOutput()
	if err != nil {
		t.Fatalf("clang failed: %v\n%s", err, buildOutput)
	}

	run := func(env []string, args ...string) (string, string, int) {
		t.Helper()
		cmd := exec.Command(binaryPath, args...)
		cmd.Env = append(os.Environ(), env...)
		stdout, err := cmd.Output()
		stderr := ""
		exitCode := 0
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
			exitCode = ee.ExitCode()
		} else if err != nil {
			t.Fatalf("running %q: %v", binaryPath, err)
		}
		return string(stdout), stderr, exitCode
	}

	env := []string{"OSTY_SNAPSHOT_DIR=" + snapshotRoot}

	// Leg 1: first call creates the golden.
	stdout, stderr, code := run(env, "snapshot", "hello", "alpha\nbeta\ngamma\n")
	if code != 0 {
		t.Fatalf("first snapshot call exited %d; stderr=%q", code, stderr)
	}
	if !strings.HasPrefix(stdout, "snapshot: created ") {
		t.Fatalf("first call stdout = %q, want created line", stdout)
	}
	snapPath := filepath.Join(snapshotRoot, "__snapshots__", "hello.snap")
	saved, err := os.ReadFile(snapPath)
	if err != nil {
		t.Fatalf("snapshot file not written to %q: %v", snapPath, err)
	}
	if got := string(saved); got != "alpha\nbeta\ngamma\n" {
		t.Fatalf("saved snapshot = %q, want %q", got, "alpha\nbeta\ngamma\n")
	}

	// Leg 2: matching second call is silent and passes.
	stdout, stderr, code = run(env, "snapshot", "hello", "alpha\nbeta\ngamma\n")
	if code != 0 {
		t.Fatalf("matching snapshot call exited %d; stderr=%q stdout=%q", code, stderr, stdout)
	}
	if stdout != "" {
		t.Fatalf("matching snapshot call produced stdout %q, want empty", stdout)
	}

	// Leg 3: diverging payload fails with diff.
	stdout, stderr, code = run(env, "snapshot", "hello", "alpha\nBETA\ngamma\n")
	if code != 1 {
		t.Fatalf("mismatch snapshot call exited %d; want 1. stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "mismatch") {
		t.Fatalf("mismatch stdout missing 'mismatch': %q", stdout)
	}
	if !strings.Contains(stdout, "OSTY_UPDATE_SNAPSHOTS=1") {
		t.Fatalf("mismatch stdout missing update hint: %q", stdout)
	}
	// The diff should mark the bad actual line with `-` and the good
	// expected line with `+`. (The runtime treats the first argument
	// as "actual" for symmetry with assertEq's left/right labels.)
	if !strings.Contains(stdout, "- alpha\nBETA\ngamma") && !strings.Contains(stdout, "- BETA") {
		t.Fatalf("mismatch stdout missing `- BETA` line: %q", stdout)
	}
	if !strings.Contains(stdout, "+ beta") {
		t.Fatalf("mismatch stdout missing `+ beta` line: %q", stdout)
	}

	// Leg 4: OSTY_UPDATE_SNAPSHOTS overwrites regardless of prior content.
	updateEnv := append(append([]string{}, env...), "OSTY_UPDATE_SNAPSHOTS=1")
	stdout, stderr, code = run(updateEnv, "snapshot", "hello", "brand\nnew\ncontent\n")
	if code != 0 {
		t.Fatalf("update snapshot call exited %d; stderr=%q", code, stderr)
	}
	if !strings.HasPrefix(stdout, "snapshot: updated ") {
		t.Fatalf("update call stdout = %q, want updated line", stdout)
	}
	saved, err = os.ReadFile(snapPath)
	if err != nil {
		t.Fatalf("snapshot file missing after update: %v", err)
	}
	if got := string(saved); got != "brand\nnew\ncontent\n" {
		t.Fatalf("updated snapshot = %q, want new content", got)
	}

	// Leg 5: sanitizer collapses unsafe characters in the name.
	stdout, _, code = run(env, "snapshot", "tricky/name with.chars!", "ok\n")
	if code != 0 {
		t.Fatalf("sanitized snapshot exited %d", code)
	}
	// 'tricky/name with.chars!' → 'tricky_name_with_chars_'
	sanitizedPath := filepath.Join(snapshotRoot, "__snapshots__", "tricky_name_with_chars_.snap")
	if _, err := os.Stat(sanitizedPath); err != nil {
		t.Fatalf("sanitizer did not produce expected path %q: %v", sanitizedPath, err)
	}
	if !strings.Contains(stdout, "tricky_name_with_chars_.snap") {
		t.Fatalf("sanitizer stdout missing expected path: %q", stdout)
	}
}

// TestBundledRuntimeListPrimitiveToString drives
// osty_rt_list_primitive_to_string against the typed list runtime —
// push → format → compare — for each supported element kind
// (Int/Float/Bool/String) plus the empty-list edge. Validates the
// output shape `[\n  elem,\n  ...\n]` that the structural-diff path
// relies on to produce meaningful line-level diffs.
func TestBundledRuntimeListPrimitiveToString(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_list_fmt_harness.c")
	binaryPath := filepath.Join(dir, "runtime_list_fmt_harness")

	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}

	harness := `#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>

void *osty_rt_list_new(void);
void osty_rt_list_push_i64(void *, int64_t);
void osty_rt_list_push_f64(void *, double);
void osty_rt_list_push_i1(void *, bool);
void osty_rt_list_push_ptr(void *, void *);
const char *osty_rt_list_primitive_to_string(void *list, int64_t elem_kind);

int main(int argc, char **argv) {
    if (argc != 2) return 2;
    const char *mode = argv[1];
    if (strcmp(mode, "int") == 0) {
        void *xs = osty_rt_list_new();
        osty_rt_list_push_i64(xs, 1);
        osty_rt_list_push_i64(xs, 2);
        osty_rt_list_push_i64(xs, 3);
        fputs(osty_rt_list_primitive_to_string(xs, 1), stdout);
    } else if (strcmp(mode, "float") == 0) {
        void *xs = osty_rt_list_new();
        osty_rt_list_push_f64(xs, 0.5);
        osty_rt_list_push_f64(xs, -1.25);
        fputs(osty_rt_list_primitive_to_string(xs, 2), stdout);
    } else if (strcmp(mode, "bool") == 0) {
        void *xs = osty_rt_list_new();
        osty_rt_list_push_i1(xs, true);
        osty_rt_list_push_i1(xs, false);
        osty_rt_list_push_i1(xs, true);
        fputs(osty_rt_list_primitive_to_string(xs, 3), stdout);
    } else if (strcmp(mode, "string") == 0) {
        void *xs = osty_rt_list_new();
        osty_rt_list_push_ptr(xs, (void *)"alpha");
        osty_rt_list_push_ptr(xs, (void *)"beta");
        fputs(osty_rt_list_primitive_to_string(xs, 4), stdout);
    } else if (strcmp(mode, "empty") == 0) {
        void *xs = osty_rt_list_new();
        fputs(osty_rt_list_primitive_to_string(xs, 1), stdout);
    } else if (strcmp(mode, "null") == 0) {
        fputs(osty_rt_list_primitive_to_string(NULL, 1), stdout);
    } else {
        return 2;
    }
    return 0;
}
`
	if err := os.WriteFile(harnessPath, []byte(harness), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", harnessPath, err)
	}
	if out, err := exec.Command("clang", "-std=c11", runtimePath, harnessPath, "-o", binaryPath).CombinedOutput(); err != nil {
		t.Fatalf("clang failed: %v\n%s", err, out)
	}

	fmt := func(mode string) string {
		out, err := exec.Command(binaryPath, mode).Output()
		if err != nil {
			t.Fatalf("%s mode: %v", mode, err)
		}
		return string(out)
	}

	if got := fmt("int"); got != "[\n  1,\n  2,\n  3,\n]" {
		t.Fatalf("int format = %q", got)
	}
	// Float formatter uses %.17g so exactly-representable fractions
	// round-trip without noise. 0.5 is exact; -1.25 is exact.
	if got := fmt("float"); got != "[\n  0.5,\n  -1.25,\n]" {
		t.Fatalf("float format = %q", got)
	}
	if got := fmt("bool"); got != "[\n  true,\n  false,\n  true,\n]" {
		t.Fatalf("bool format = %q", got)
	}
	if got := fmt("string"); got != "[\n  \"alpha\",\n  \"beta\",\n]" {
		t.Fatalf("string format = %q", got)
	}
	if got := fmt("empty"); got != "[]" {
		t.Fatalf("empty format = %q", got)
	}
	if got := fmt("null"); got != "[]" {
		t.Fatalf("null format = %q", got)
	}
}

// TestBundledRuntimeDiffLines exercises osty_rt_strings_DiffLines
// directly without going through the snapshot helper. Covers the edge
// cases the in-process diff must get right: byte-equal inputs produce
// no output, all-different inputs produce all `-`/`+` lines, and an
// interior change produces a trimmed diff block with surrounding
// context lines.
func TestBundledRuntimeDiffLines(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_diff_harness.c")
	binaryPath := filepath.Join(dir, "runtime_diff_harness")

	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}

	harness := `#include <stdio.h>
const char *osty_rt_strings_DiffLines(const char *actual, const char *expected);
int main(int argc, char **argv) {
    if (argc != 3) return 2;
    const char *d = osty_rt_strings_DiffLines(argv[1], argv[2]);
    printf("%s", d == NULL ? "" : d);
    return 0;
}
`
	if err := os.WriteFile(harnessPath, []byte(harness), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", harnessPath, err)
	}
	if out, err := exec.Command("clang", "-std=c11", runtimePath, harnessPath, "-o", binaryPath).CombinedOutput(); err != nil {
		t.Fatalf("clang failed: %v\n%s", err, out)
	}

	diff := func(a, b string) string {
		out, err := exec.Command(binaryPath, a, b).Output()
		if err != nil {
			t.Fatalf("diff harness %q vs %q: %v", a, b, err)
		}
		return string(out)
	}

	// Equal inputs — empty diff.
	if got := diff("abc\n", "abc\n"); got != "" {
		t.Fatalf("equal inputs produced diff %q", got)
	}

	// Interior single-line change: context lines on both sides.
	got := diff("alpha\nbeta\ngamma\n", "alpha\nBETA\ngamma\n")
	// trim-prefix: "alpha" equal → context.
	// divergent block: `- beta` / `+ BETA`.
	// trim-suffix: "gamma" equal → context.
	wantLines := []string{"  alpha", "- beta", "+ BETA", "  gamma"}
	for _, w := range wantLines {
		if !strings.Contains(got, w) {
			t.Fatalf("diff missing %q in:\n%s", w, got)
		}
	}

	// Complete divergence — no common prefix/suffix.
	got = diff("one\ntwo\n", "uno\ndos\n")
	for _, w := range []string{"- one", "- two", "+ uno", "+ dos"} {
		if !strings.Contains(got, w) {
			t.Fatalf("full-diverge diff missing %q in:\n%s", w, got)
		}
	}

	// Empty actual produces only `+` lines.
	got = diff("", "only\nexpected\n")
	if strings.Contains(got, "- ") {
		t.Fatalf("empty-actual diff should have no `- ` lines: %q", got)
	}
	for _, w := range []string{"+ only", "+ expected"} {
		if !strings.Contains(got, w) {
			t.Fatalf("empty-actual diff missing %q in:\n%s", w, got)
		}
	}
}
