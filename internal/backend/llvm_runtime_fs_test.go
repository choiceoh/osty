package backend

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBundledRuntimeFsToolingHelpers(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	root := filepath.Join(dir, "fs-root")
	if err := os.MkdirAll(filepath.Join(root, "src", "nested"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "a.txt"), []byte("alpha\nsame\n"), 0o644); err != nil {
		t.Fatalf("WriteFile a.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "nested", "b.osty"), []byte("beta\n"), 0o644); err != nil {
		t.Fatalf("WriteFile b.osty: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "other.txt"), []byte("gamma\n"), 0o644); err != nil {
		t.Fatalf("WriteFile other.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "right.txt"), []byte("alpha\nchanged\n"), 0o644); err != nil {
		t.Fatalf("WriteFile right.txt: %v", err)
	}

	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_fs_harness.c")
	binaryPath := filepath.Join(dir, "runtime_fs_harness")
	if runtime.GOOS == "windows" {
		binaryPath += ".exe"
	}
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}

	harness := `#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

void *osty_rt_fs_walk(const char *root);
void *osty_rt_fs_glob(const char *pattern);
void *osty_rt_fs_watch(const char *root);
void *osty_rt_fs_atomic_write_string(const char *path, const char *contents);
void *osty_rt_fs_lock_file(const char *path);
void *osty_rt_fs_hash_file(const char *path);
void *osty_rt_fs_copy_dir(const char *from, const char *to);
void *osty_rt_fs_diff_files(const char *left, const char *right);
void *osty_rt_fs_read_string(const char *path);
bool osty_rt_fs_exists(const char *path);
int64_t osty_rt_list_len(void *raw_list);
const char *osty_rt_list_get_string(void *raw_list, int64_t index);

static void join(char *out, size_t cap, const char *base, const char *suffix) {
    int n = snprintf(out, cap, "%s/%s", base, suffix);
    if (n < 0 || (size_t)n >= cap) {
        fprintf(stderr, "path too long\n");
        exit(2);
    }
}

static void fail(const char *msg) {
    fprintf(stderr, "%s\n", msg);
    exit(1);
}

static void require_no_error(void *err, const char *label) {
    if (err != NULL) {
        fprintf(stderr, "%s failed\n", label);
        exit(1);
    }
}

static void normalize_slashes(char *path) {
    for (char *p = path; *p != '\0'; p++) {
        if (*p == '\\') {
            *p = '/';
        }
    }
}

static int list_contains(void *list, const char *needle) {
    char want[4096];
    int wrote = snprintf(want, sizeof(want), "%s", needle);
    if (wrote < 0 || (size_t)wrote >= sizeof(want)) {
        fail("path too long");
    }
    normalize_slashes(want);
    int64_t n = osty_rt_list_len(list);
    for (int64_t i = 0; i < n; i++) {
        const char *item = osty_rt_list_get_string(list, i);
        char got[4096];
        wrote = snprintf(got, sizeof(got), "%s", item == NULL ? "" : item);
        if (wrote < 0 || (size_t)wrote >= sizeof(got)) {
            fail("path too long");
        }
        normalize_slashes(got);
        if (strcmp(got, want) == 0) {
            return 1;
        }
    }
    return 0;
}

int main(int argc, char **argv) {
    if (argc != 2) {
        fail("usage: runtime_fs_harness ROOT");
    }
    const char *root = argv[1];
    char path[4096];
    char path2[4096];
    char pattern[4096];

    void *walked = osty_rt_fs_walk(root);
    if (walked == NULL) {
        fail("walk returned null");
    }
    join(path, sizeof(path), root, "src/");
    if (!list_contains(walked, path)) {
        fail("walk missing src directory");
    }
    join(path, sizeof(path), root, "src/nested/b.osty");
    if (!list_contains(walked, path)) {
        fail("walk missing nested file");
    }

    join(pattern, sizeof(pattern), root, "**/*.osty");
    void *matched = osty_rt_fs_glob(pattern);
    if (matched == NULL || osty_rt_list_len(matched) != 1 || !list_contains(matched, path)) {
        fail("glob did not match nested osty file");
    }

    void *snapshot = osty_rt_fs_watch(root);
    if (snapshot == NULL || osty_rt_list_len(snapshot) < osty_rt_list_len(walked)) {
        fail("watch snapshot too small");
    }

    join(path, sizeof(path), root, "src/a.txt");
    void *hash = osty_rt_fs_hash_file(path);
    if (hash == NULL || strlen((const char *)hash) != 64) {
        fail("hashFile did not return sha256 hex");
    }
    join(path2, sizeof(path2), root, "right.txt");
    const char *diff = (const char *)osty_rt_fs_diff_files(path, path2);
    if (diff == NULL || strstr(diff, "- same") == NULL || strstr(diff, "+ changed") == NULL) {
        fail("diffFiles missing line diff");
    }

    join(path, sizeof(path), root, "atomic.txt");
    require_no_error(osty_rt_fs_atomic_write_string(path, "atom"), "atomicWriteString");
    const char *text = (const char *)osty_rt_fs_read_string(path);
    if (text == NULL || strcmp(text, "atom") != 0) {
        fail("atomicWriteString did not write content");
    }

    join(path, sizeof(path), root, "tool.lock");
    require_no_error(osty_rt_fs_lock_file(path), "lockFile first");
    if (osty_rt_fs_lock_file(path) == NULL) {
        fail("lockFile second call should fail");
    }

    join(path, sizeof(path), root, "src");
    join(path2, sizeof(path2), root, "copy");
    require_no_error(osty_rt_fs_copy_dir(path, path2), "copyDir");
    join(path, sizeof(path), root, "copy/nested/b.osty");
    if (!osty_rt_fs_exists(path)) {
        fail("copyDir did not copy nested file");
    }

    printf("ok\n");
    return 0;
}
`
	if err := os.WriteFile(harnessPath, []byte(harness), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", harnessPath, err)
	}

	buildOutput, err := runtimeClangCommand("-std=c11", runtimePath, harnessPath, "-o", binaryPath).CombinedOutput()
	if err != nil {
		t.Fatalf("clang failed: %v\n%s", err, buildOutput)
	}
	runOutput, err := exec.Command(binaryPath, root).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, runOutput)
	}
	if got := strings.TrimSpace(string(runOutput)); got != "ok" {
		t.Fatalf("runtime fs stdout = %q, want ok", got)
	}
}
