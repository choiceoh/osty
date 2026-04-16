package backend

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestBundledRuntimeDebugCollectRespectsRoots(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found on PATH")
	}

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdint.h>
#include <stdio.h>

#if defined(__APPLE__)
#define OSTY_GC_SYMBOL(name) "_" name
#else
#define OSTY_GC_SYMBOL(name) name
#endif

void *osty_gc_alloc_v1(int64_t object_kind, int64_t byte_size, const char *site) __asm__(OSTY_GC_SYMBOL("osty.gc.alloc_v1"));
void osty_gc_pre_write_v1(void *owner, void *old_value, int64_t slot_kind) __asm__(OSTY_GC_SYMBOL("osty.gc.pre_write_v1"));
void *osty_gc_load_v1(void *value) __asm__(OSTY_GC_SYMBOL("osty.gc.load_v1"));
void osty_gc_root_bind_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_bind_v1"));
void osty_gc_root_release_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_release_v1"));

void osty_gc_debug_collect(void);
int64_t osty_gc_debug_live_count(void);
int64_t osty_gc_debug_collection_count(void);
void *osty_rt_list_new(void);
void osty_rt_list_push_ptr(void *list, void *value);
int64_t osty_rt_list_len(void *list);
void *osty_rt_strings_Split(const char *value, const char *sep);
void *osty_rt_list_get_ptr(void *list, int64_t index);
int osty_rt_strings_Equal(const char *left, const char *right);

int main(void) {
    void *leaf = osty_gc_alloc_v1(7, 32, "leaf");
    osty_gc_root_bind_v1(leaf);
    osty_gc_debug_collect();
    printf("%lld\n", (long long)osty_gc_debug_live_count());
    osty_gc_root_release_v1(leaf);
    osty_gc_debug_collect();
    printf("%lld\n", (long long)osty_gc_debug_live_count());

    void *list = osty_rt_list_new();
    void *child = osty_gc_alloc_v1(8, 16, "child");
    osty_gc_pre_write_v1(list, child, 0);
    osty_rt_list_push_ptr(list, child);
    osty_gc_root_bind_v1(list);
    osty_gc_debug_collect();
    printf("%lld\n", (long long)osty_gc_debug_live_count());
    printf("%lld\n", (long long)osty_rt_list_len(list));
    printf("%d\n", osty_gc_load_v1(list) == list);
    osty_gc_root_release_v1(list);
    osty_gc_debug_collect();
    printf("%lld\n", (long long)osty_gc_debug_live_count());

    list = osty_rt_strings_Split("gc,llvm", ",");
    osty_gc_root_bind_v1(list);
    osty_gc_debug_collect();
    printf("%lld\n", (long long)osty_gc_debug_live_count());
    printf("%d\n", osty_rt_strings_Equal((const char *)osty_rt_list_get_ptr(list, 0), "gc"));
    printf("%d\n", osty_rt_strings_Equal((const char *)osty_rt_list_get_ptr(list, 1), "llvm"));
    osty_gc_root_release_v1(list);
    osty_gc_debug_collect();
    printf("%lld\n", (long long)osty_gc_debug_live_count());
    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", harnessPath, err)
	}
	cmd := exec.Command("clang", "-std=c11", runtimePath, harnessPath, "-o", binaryPath)
	buildOutput, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("clang failed: %v\n%s", err, buildOutput)
	}
	runOutput, err := exec.Command(binaryPath).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, runOutput)
	}
	if got, want := string(runOutput), "1\n0\n2\n1\n1\n0\n3\n1\n1\n0\n"; got != want {
		t.Fatalf("runtime GC harness stdout = %q, want %q", got, want)
	}
}

func TestBundledRuntimeSafepointScansStackRoots(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found on PATH")
	}

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_safepoint_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_safepoint_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdint.h>
#include <stdio.h>

#if defined(__APPLE__)
#define OSTY_GC_SYMBOL(name) "_" name
#else
#define OSTY_GC_SYMBOL(name) name
#endif

void *osty_gc_alloc_v1(int64_t object_kind, int64_t byte_size, const char *site) __asm__(OSTY_GC_SYMBOL("osty.gc.alloc_v1"));
void osty_gc_safepoint_v1(int64_t safepoint_id, void *const *root_slots, int64_t root_slot_count) __asm__(OSTY_GC_SYMBOL("osty.gc.safepoint_v1"));

int64_t osty_gc_debug_live_count(void);
void *osty_rt_list_new(void);
void osty_rt_list_push_ptr(void *list, void *value);
void *osty_rt_list_get_ptr(void *list, int64_t index);

int main(void) {
    void *leaf = osty_gc_alloc_v1(7, 32, "leaf");
    void *root = leaf;
    void *root_slots[1] = { &root };
    osty_gc_safepoint_v1(1, root_slots, 1);
    printf("%lld\n", (long long)osty_gc_debug_live_count());
    root = NULL;
    osty_gc_safepoint_v1(2, root_slots, 1);
    printf("%lld\n", (long long)osty_gc_debug_live_count());

    void *list = osty_rt_list_new();
    void *child = osty_gc_alloc_v1(8, 16, "child");
    osty_rt_list_push_ptr(list, child);
    root = list;
    osty_gc_safepoint_v1(3, root_slots, 1);
    printf("%lld\n", (long long)osty_gc_debug_live_count());
    printf("%d\n", osty_rt_list_get_ptr(list, 0) == child);
    root = NULL;
    osty_gc_safepoint_v1(4, root_slots, 1);
    printf("%lld\n", (long long)osty_gc_debug_live_count());
    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", harnessPath, err)
	}
	cmd := exec.Command("clang", "-std=c11", runtimePath, harnessPath, "-o", binaryPath)
	buildOutput, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("clang failed: %v\n%s", err, buildOutput)
	}
	runCmd := exec.Command(binaryPath)
	runCmd.Env = append(os.Environ(), "OSTY_GC_STRESS=1")
	runOutput, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, runOutput)
	}
	if got, want := string(runOutput), "1\n0\n2\n1\n0\n"; got != want {
		t.Fatalf("runtime safepoint harness stdout = %q, want %q", got, want)
	}
}

func TestBundledRuntimeAutoCollectsAtPressureSafepoints(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found on PATH")
	}

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_pressure_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_pressure_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdint.h>
#include <stdio.h>

#if defined(__APPLE__)
#define OSTY_GC_SYMBOL(name) "_" name
#else
#define OSTY_GC_SYMBOL(name) name
#endif

void *osty_gc_alloc_v1(int64_t object_kind, int64_t byte_size, const char *site) __asm__(OSTY_GC_SYMBOL("osty.gc.alloc_v1"));
void osty_gc_root_bind_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_bind_v1"));
void osty_gc_root_release_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_release_v1"));
void osty_gc_safepoint_v1(int64_t safepoint_id, void *const *root_slots, int64_t root_slot_count) __asm__(OSTY_GC_SYMBOL("osty.gc.safepoint_v1"));

int64_t osty_gc_debug_live_count(void);
int64_t osty_gc_debug_collection_count(void);

int main(void) {
    void *root = osty_gc_alloc_v1(7, 32, "root");
    void *leaf = osty_gc_alloc_v1(8, 16, "leaf");
    (void)leaf;
    osty_gc_root_bind_v1(root);
    osty_gc_safepoint_v1(1, NULL, 0);
    printf("%lld\n", (long long)osty_gc_debug_collection_count());
    printf("%lld\n", (long long)osty_gc_debug_live_count());
    osty_gc_root_release_v1(root);
    leaf = osty_gc_alloc_v1(9, 8, "late");
    (void)leaf;
    osty_gc_safepoint_v1(2, NULL, 0);
    printf("%lld\n", (long long)osty_gc_debug_collection_count());
    printf("%lld\n", (long long)osty_gc_debug_live_count());
    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", harnessPath, err)
	}
	cmd := exec.Command("clang", "-std=c11", runtimePath, harnessPath, "-o", binaryPath)
	buildOutput, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("clang failed: %v\n%s", err, buildOutput)
	}
	runCmd := exec.Command(binaryPath)
	runCmd.Env = append(os.Environ(), "OSTY_GC_THRESHOLD_BYTES=1")
	runOutput, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, runOutput)
	}
	if got, want := string(runOutput), "1\n1\n2\n0\n"; got != want {
		t.Fatalf("runtime pressure harness stdout = %q, want %q", got, want)
	}
}

func TestBundledRuntimePressureKeepsRootedListChildrenAlive(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found on PATH")
	}

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_pressure_list_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_pressure_list_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdint.h>
#include <stdio.h>

#if defined(__APPLE__)
#define OSTY_GC_SYMBOL(name) "_" name
#else
#define OSTY_GC_SYMBOL(name) name
#endif

void *osty_gc_alloc_v1(int64_t object_kind, int64_t byte_size, const char *site) __asm__(OSTY_GC_SYMBOL("osty.gc.alloc_v1"));
void osty_gc_root_bind_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_bind_v1"));
void osty_gc_root_release_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_release_v1"));
void osty_gc_safepoint_v1(int64_t safepoint_id, void *const *root_slots, int64_t root_slot_count) __asm__(OSTY_GC_SYMBOL("osty.gc.safepoint_v1"));

int64_t osty_gc_debug_live_count(void);
int64_t osty_gc_debug_collection_count(void);
void *osty_rt_list_new(void);
void osty_rt_list_push_ptr(void *list, void *value);
void *osty_rt_list_get_ptr(void *list, int64_t index);

int main(void) {
    void *list = osty_rt_list_new();
    void *child = osty_gc_alloc_v1(8, 16, "child");
    void *saved_child = child;
    osty_rt_list_push_ptr(list, child);
    osty_gc_root_bind_v1(list);
    child = osty_gc_alloc_v1(9, 8, "garbage");
    (void)child;
    osty_gc_safepoint_v1(1, NULL, 0);
    printf("%lld\n", (long long)osty_gc_debug_collection_count());
    printf("%lld\n", (long long)osty_gc_debug_live_count());
    printf("%d\n", osty_rt_list_get_ptr(list, 0) == saved_child);
    osty_gc_root_release_v1(list);
    child = osty_gc_alloc_v1(10, 8, "late");
    (void)child;
    osty_gc_safepoint_v1(2, NULL, 0);
    printf("%lld\n", (long long)osty_gc_debug_collection_count());
    printf("%lld\n", (long long)osty_gc_debug_live_count());
    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", harnessPath, err)
	}
	cmd := exec.Command("clang", "-std=c11", runtimePath, harnessPath, "-o", binaryPath)
	buildOutput, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("clang failed: %v\n%s", err, buildOutput)
	}
	runCmd := exec.Command(binaryPath)
	runCmd.Env = append(os.Environ(), "OSTY_GC_THRESHOLD_BYTES=1")
	runOutput, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, runOutput)
	}
	if got, want := string(runOutput), "1\n2\n1\n2\n0\n"; got != want {
		t.Fatalf("runtime pressure list harness stdout = %q, want %q", got, want)
	}
}
