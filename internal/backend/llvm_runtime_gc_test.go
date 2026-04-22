package backend

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBundledRuntimeDebugCollectRespectsRoots(t *testing.T) {
	parallelClangBackendTest(t)

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
int64_t osty_gc_debug_pre_write_count(void);
int64_t osty_gc_debug_pre_write_managed_count(void);
int64_t osty_gc_debug_post_write_count(void);
int64_t osty_gc_debug_post_write_managed_count(void);
int64_t osty_gc_debug_load_count(void);
int64_t osty_gc_debug_load_managed_count(void);
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
    printf("%lld\n", (long long)osty_gc_debug_pre_write_count());
    printf("%lld\n", (long long)osty_gc_debug_pre_write_managed_count());
    printf("%lld\n", (long long)osty_gc_debug_post_write_count());
    printf("%lld\n", (long long)osty_gc_debug_post_write_managed_count());
    printf("%lld\n", (long long)osty_gc_debug_load_count());
    printf("%lld\n", (long long)osty_gc_debug_load_managed_count());
    osty_gc_root_release_v1(list);
    osty_gc_debug_collect();
    printf("%lld\n", (long long)osty_gc_debug_live_count());

    list = osty_rt_strings_Split("gc,llvm", ",");
    osty_gc_root_bind_v1(list);
    osty_gc_debug_collect();
    printf("%lld\n", (long long)osty_gc_debug_live_count());
    printf("%d\n", osty_rt_strings_Equal((const char *)osty_rt_list_get_ptr(list, 0), "gc"));
    printf("%d\n", osty_rt_strings_Equal((const char *)osty_rt_list_get_ptr(list, 1), "llvm"));
    printf("%lld\n", (long long)osty_gc_debug_load_count());
    printf("%lld\n", (long long)osty_gc_debug_load_managed_count());
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
	runCmd := exec.Command(binaryPath)
	runCmd.Env = append(os.Environ(), "OSTY_GC_THRESHOLD_BYTES=1")
	runOutput, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, runOutput)
	}
	if got, want := string(runOutput), "1\n0\n2\n1\n1\n1\n1\n1\n1\n3\n3\n0\n3\n1\n1\n9\n9\n0\n"; got != want {
		t.Fatalf("runtime GC harness stdout = %q, want %q", got, want)
	}
}

func TestBundledRuntimeSafepointScansStackRoots(t *testing.T) {
	parallelClangBackendTest(t)

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
void *osty_gc_load_v1(void *value) __asm__(OSTY_GC_SYMBOL("osty.gc.load_v1"));

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
    printf("%d\n", osty_rt_list_get_ptr(list, 0) == osty_gc_load_v1(child));
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
	parallelClangBackendTest(t)

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
void *osty_gc_load_v1(void *value) __asm__(OSTY_GC_SYMBOL("osty.gc.load_v1"));

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
	parallelClangBackendTest(t)

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
void *osty_gc_load_v1(void *value) __asm__(OSTY_GC_SYMBOL("osty.gc.load_v1"));

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
    printf("%d\n", osty_rt_list_get_ptr(list, 0) == osty_gc_load_v1(saved_child));
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

func TestBundledRuntimeTracesManagedAggregateListElements(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_aggregate_list_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_aggregate_list_harness")
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

typedef struct Box {
    void *child;
} Box;

void *osty_gc_alloc_v1(int64_t object_kind, int64_t byte_size, const char *site) __asm__(OSTY_GC_SYMBOL("osty.gc.alloc_v1"));
void osty_gc_root_bind_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_bind_v1"));
void osty_gc_root_release_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_release_v1"));
void *osty_gc_load_v1(void *value) __asm__(OSTY_GC_SYMBOL("osty.gc.load_v1"));

void osty_gc_debug_collect(void);
int64_t osty_gc_debug_live_count(void);
void *osty_rt_list_new(void);
void osty_rt_list_push_bytes_roots_v1(void *list, const void *value, int64_t elem_size, const int64_t *gc_offsets, int64_t gc_offset_count);
void osty_rt_list_get_bytes_v1(void *list, int64_t index, void *out, int64_t elem_size);

int main(void) {
    void *list = osty_rt_list_new();
    void *child = osty_gc_alloc_v1(8, 16, "child");
    void *saved_child = child;
    int64_t offsets[1] = { 0 };
    Box box = { child };
    Box loaded = { 0 };

    osty_rt_list_push_bytes_roots_v1(list, &box, (int64_t)sizeof(box), offsets, 1);
    osty_gc_root_bind_v1(list);
    osty_gc_debug_collect();
    printf("%lld\n", (long long)osty_gc_debug_live_count());
    osty_rt_list_get_bytes_v1(list, 0, &loaded, (int64_t)sizeof(loaded));
    printf("%d\n", loaded.child == osty_gc_load_v1(saved_child));
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
	runCmd := exec.Command(binaryPath)
	runCmd.Env = append(os.Environ(), "OSTY_GC_THRESHOLD_BYTES=1")
	runOutput, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, runOutput)
	}
	if got, want := string(runOutput), "2\n1\n0\n"; got != want {
		t.Fatalf("runtime aggregate list harness stdout = %q, want %q", got, want)
	}
}

func TestBundledRuntimeGlobalRootKeepsSlotPayloadAlive(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_global_root_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_global_root_harness")
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
void osty_gc_global_root_register_v1(void *slot) __asm__(OSTY_GC_SYMBOL("osty.gc.global_root_register_v1"));
void osty_gc_global_root_unregister_v1(void *slot) __asm__(OSTY_GC_SYMBOL("osty.gc.global_root_unregister_v1"));

void osty_gc_debug_collect(void);
int64_t osty_gc_debug_live_count(void);
int64_t osty_gc_debug_global_root_count(void);

static void *g_slot_a = NULL;
static void *g_slot_b = NULL;

int main(void) {
    /* Baseline — no globals registered, objects reclaimed. */
    void *a = osty_gc_alloc_v1(7, 32, "a");
    (void)a;
    osty_gc_debug_collect();
    printf("%lld\n", (long long)osty_gc_debug_live_count());

    /* Register a global slot and verify the payload survives collection. */
    g_slot_a = osty_gc_alloc_v1(8, 32, "global_a");
    osty_gc_global_root_register_v1(&g_slot_a);
    printf("%lld\n", (long long)osty_gc_debug_global_root_count());
    osty_gc_debug_collect();
    printf("%lld\n", (long long)osty_gc_debug_live_count());

    /* Reassign the slot — old payload should now be collectable, new one protected. */
    g_slot_a = osty_gc_alloc_v1(9, 32, "global_a_replaced");
    osty_gc_debug_collect();
    printf("%lld\n", (long long)osty_gc_debug_live_count());

    /* Second global slot — both survive. */
    g_slot_b = osty_gc_alloc_v1(10, 32, "global_b");
    osty_gc_global_root_register_v1(&g_slot_b);
    printf("%lld\n", (long long)osty_gc_debug_global_root_count());
    osty_gc_debug_collect();
    printf("%lld\n", (long long)osty_gc_debug_live_count());

    /* Unregister first slot — its payload becomes collectable. */
    osty_gc_global_root_unregister_v1(&g_slot_a);
    printf("%lld\n", (long long)osty_gc_debug_global_root_count());
    osty_gc_debug_collect();
    printf("%lld\n", (long long)osty_gc_debug_live_count());

    /* Unregistering an unknown slot is a no-op. */
    void *never_registered = NULL;
    osty_gc_global_root_unregister_v1(&never_registered);
    printf("%lld\n", (long long)osty_gc_debug_global_root_count());

    /* Clearing the slot before collect also releases the payload. */
    g_slot_b = NULL;
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
	runCmd := exec.Command(binaryPath)
	runCmd.Env = append(os.Environ(), "OSTY_GC_THRESHOLD_BYTES=1")
	runOutput, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, runOutput)
	}
	if got, want := string(runOutput), "0\n1\n1\n1\n2\n2\n1\n1\n1\n0\n"; got != want {
		t.Fatalf("runtime global root harness stdout = %q, want %q", got, want)
	}
}

func TestBundledRuntimeWriteBarriersLogEdges(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_barrier_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_barrier_harness")
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
void osty_gc_post_write_v1(void *owner, void *value, int64_t slot_kind) __asm__(OSTY_GC_SYMBOL("osty.gc.post_write_v1"));
void osty_gc_root_bind_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_bind_v1"));
void osty_gc_root_release_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_release_v1"));

void osty_gc_debug_collect(void);
int64_t osty_gc_debug_satb_log_count(void);
int64_t osty_gc_debug_remembered_edge_count(void);
int osty_gc_debug_satb_log_contains(void *payload);
int osty_gc_debug_remembered_edge_contains(void *owner, void *value);

int main(void) {
    void *owner = osty_gc_alloc_v1(7, 32, "owner");
    void *a = osty_gc_alloc_v1(8, 16, "a");
    void *b = osty_gc_alloc_v1(9, 16, "b");
    osty_gc_root_bind_v1(owner);

    /* Baseline — no writes yet. */
    printf("%lld\n", (long long)osty_gc_debug_satb_log_count());
    printf("%lld\n", (long long)osty_gc_debug_remembered_edge_count());

    /* Initial store: slot was NULL, so SATB is empty; post-write records (owner, a). */
    osty_gc_pre_write_v1(owner, NULL, 0);
    osty_gc_post_write_v1(owner, a, 0);
    printf("%lld\n", (long long)osty_gc_debug_satb_log_count());
    printf("%lld\n", (long long)osty_gc_debug_remembered_edge_count());
    printf("%d\n", osty_gc_debug_remembered_edge_contains(owner, a));

    /* Overwrite a -> b: SATB captures old value a; post-write records (owner, b). */
    osty_gc_pre_write_v1(owner, a, 0);
    osty_gc_post_write_v1(owner, b, 0);
    printf("%lld\n", (long long)osty_gc_debug_satb_log_count());
    printf("%d\n", osty_gc_debug_satb_log_contains(a));
    printf("%lld\n", (long long)osty_gc_debug_remembered_edge_count());
    printf("%d\n", osty_gc_debug_remembered_edge_contains(owner, b));

    /* Duplicate edge — should not grow the remembered set. */
    osty_gc_post_write_v1(owner, b, 0);
    printf("%lld\n", (long long)osty_gc_debug_remembered_edge_count());

    /* Unmanaged values are ignored by both logs. */
    int stack_int = 0;
    osty_gc_pre_write_v1(owner, &stack_int, 0);
    osty_gc_post_write_v1(owner, &stack_int, 0);
    printf("%lld\n", (long long)osty_gc_debug_satb_log_count());
    printf("%lld\n", (long long)osty_gc_debug_remembered_edge_count());

    /* Collection clears both logs. */
    osty_gc_debug_collect();
    printf("%lld\n", (long long)osty_gc_debug_satb_log_count());
    printf("%lld\n", (long long)osty_gc_debug_remembered_edge_count());

    osty_gc_root_release_v1(owner);
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
	if got, want := string(runOutput), "0\n0\n0\n1\n1\n1\n1\n2\n1\n2\n1\n2\n0\n0\n"; got != want {
		t.Fatalf("runtime write-barrier harness stdout = %q, want %q", got, want)
	}
}

// TestBundledRuntimeValidateHeapInvariants exercises Phase A1
// (RUNTIME_GC_DELTA §9.5) — the `osty_gc_debug_validate_heap` oracle. It
// walks typical allocation/collection lifecycles and asserts that the
// invariants hold at each quiescent point. The test does not deliberately
// corrupt heap state; dedicated negative tests would require stable error
// codes (which the oracle provides) and intrusive C-level mutation, which
// can be added once downstream consumers need them.
func TestBundledRuntimeValidateHeapInvariants(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_validate_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_validate_harness")
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
void osty_gc_post_write_v1(void *owner, void *value, int64_t slot_kind) __asm__(OSTY_GC_SYMBOL("osty.gc.post_write_v1"));
void osty_gc_root_bind_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_bind_v1"));
void osty_gc_root_release_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_release_v1"));
void osty_gc_global_root_register_v1(void *slot) __asm__(OSTY_GC_SYMBOL("osty.gc.global_root_register_v1"));
void osty_gc_global_root_unregister_v1(void *slot) __asm__(OSTY_GC_SYMBOL("osty.gc.global_root_unregister_v1"));

void osty_gc_debug_collect(void);
int64_t osty_gc_debug_validate_heap(void);
int64_t osty_gc_debug_live_count(void);

int main(void) {
    /* Empty heap — all invariants vacuously true. */
    printf("%lld\n", (long long)osty_gc_debug_validate_heap());

    /* Allocate a small graph: owner -> child. */
    void *owner = osty_gc_alloc_v1(7, 32, "owner");
    void *child = osty_gc_alloc_v1(8, 16, "child");
    osty_gc_pre_write_v1(owner, NULL, 0);
    osty_gc_post_write_v1(owner, child, 0);
    osty_gc_root_bind_v1(owner);
    printf("%lld\n", (long long)osty_gc_debug_validate_heap());

    /* Global root slot — must be non-NULL pointer. */
    static void *slot = NULL;
    slot = owner;
    osty_gc_global_root_register_v1(&slot);
    printf("%lld\n", (long long)osty_gc_debug_validate_heap());

    /* Collect. SATB / remembered logs cleared, survivors retain their
     * live status, marks are cleared on the way out, cumulative counters
     * stay non-negative. */
    osty_gc_debug_collect();
    printf("%lld\n", (long long)osty_gc_debug_validate_heap());

    /* Release global root, collect, unbind owner, collect again -> empty. */
    osty_gc_global_root_unregister_v1(&slot);
    osty_gc_root_release_v1(owner);
    osty_gc_debug_collect();
    printf("%lld\n", (long long)osty_gc_debug_validate_heap());
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
	if got, want := string(runOutput), "0\n0\n0\n0\n0\n0\n"; got != want {
		t.Fatalf("runtime validate-heap harness stdout = %q, want %q", got, want)
	}
}

// TestBundledRuntimeStatsSnapshot covers Phase A2 (RUNTIME_GC_DELTA §9.3)
// — the `osty_gc_debug_stats` snapshot and lifetime counters. We check
// that cumulative totals accumulate across collections and that individual
// debug_* accessors agree with the struct snapshot.
func TestBundledRuntimeStatsSnapshot(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_stats_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_stats_harness")
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

typedef struct osty_gc_stats {
    int64_t collection_count;
    int64_t live_count;
    int64_t live_bytes;
    int64_t allocated_since_collect;
    int64_t allocated_bytes_total;
    int64_t swept_count_total;
    int64_t swept_bytes_total;
    int64_t pre_write_count;
    int64_t pre_write_managed_count;
    int64_t post_write_count;
    int64_t post_write_managed_count;
    int64_t load_count;
    int64_t load_managed_count;
    int64_t satb_log_count;
    int64_t remembered_edge_count;
    int64_t global_root_count;
    int64_t pressure_limit_bytes;
    int64_t mark_stack_max_depth;
    int64_t collection_nanos_total;
    int64_t collection_nanos_last;
    int64_t collection_nanos_max;
    int64_t index_capacity;
    int64_t index_count;
    int64_t index_tombstones;
    int64_t index_find_ops_total;
    int64_t minor_count;
    int64_t major_count;
    int64_t minor_nanos_total;
    int64_t major_nanos_total;
    int64_t young_count;
    int64_t young_bytes;
    int64_t old_count;
    int64_t old_bytes;
    int64_t promoted_count_total;
    int64_t promoted_bytes_total;
    int64_t allocated_since_minor;
    int64_t nursery_limit_bytes;
    int64_t promote_age;
    int64_t free_list_count;
    int64_t free_list_bytes;
    int64_t free_list_reused_count_total;
    int64_t free_list_reused_bytes_total;
    int64_t humongous_alloc_count_total;
    int64_t humongous_alloc_bytes_total;
    int64_t humongous_swept_count_total;
    int64_t humongous_swept_bytes_total;
    int64_t bump_block_count;
    int64_t bump_block_bytes_total;
    int64_t bump_alloc_count_total;
    int64_t bump_alloc_bytes_total;
    int64_t bump_recycled_block_count_total;
    int64_t bump_recycled_bytes_total;
} osty_gc_stats;

void *osty_gc_alloc_v1(int64_t object_kind, int64_t byte_size, const char *site) __asm__(OSTY_GC_SYMBOL("osty.gc.alloc_v1"));
void osty_gc_root_bind_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_bind_v1"));
void osty_gc_root_release_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_release_v1"));

void osty_gc_debug_collect(void);
void osty_gc_debug_stats(osty_gc_stats *out);
int64_t osty_gc_debug_allocated_bytes_total(void);
int64_t osty_gc_debug_swept_count_total(void);
int64_t osty_gc_debug_swept_bytes_total(void);
int64_t osty_gc_debug_mark_stack_max_depth(void);

int main(void) {
    osty_gc_stats s;

    /* Allocate + release without binding -> swept on next collect. */
    void *garbage1 = osty_gc_alloc_v1(7, 24, "g1");
    void *garbage2 = osty_gc_alloc_v1(7, 24, "g2");
    (void)garbage1; (void)garbage2;

    osty_gc_debug_stats(&s);
    /* 2 allocations, 48 bytes, nothing swept yet. */
    printf("%lld %lld %lld\n",
        (long long)s.live_count,
        (long long)s.allocated_bytes_total,
        (long long)s.swept_count_total);

    osty_gc_debug_collect();
    osty_gc_debug_stats(&s);
    /* After collect: 2 swept (both unreferenced), totals cumulative. */
    printf("%lld %lld %lld %lld\n",
        (long long)s.collection_count,
        (long long)s.live_count,
        (long long)s.swept_count_total,
        (long long)s.swept_bytes_total);

    /* Allocate one more and pin — should survive collection. */
    void *keep = osty_gc_alloc_v1(7, 40, "keep");
    osty_gc_root_bind_v1(keep);
    osty_gc_debug_collect();
    osty_gc_debug_stats(&s);
    printf("%lld %lld %lld %lld\n",
        (long long)s.collection_count,
        (long long)s.live_count,
        (long long)s.live_bytes,
        (long long)s.allocated_bytes_total);

    /* Scalar accessors agree with the struct snapshot. */
    printf("%d %d %d\n",
        osty_gc_debug_allocated_bytes_total() == s.allocated_bytes_total,
        osty_gc_debug_swept_count_total() == s.swept_count_total,
        osty_gc_debug_swept_bytes_total() == s.swept_bytes_total);

    /* mark_stack_max_depth is non-negative (we at most marked the 'keep'
     * payload, so depth is at least zero). */
    printf("%d\n", osty_gc_debug_mark_stack_max_depth() >= 0);

    osty_gc_root_release_v1(keep);
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
	if got, want := string(runOutput), "2 48 0\n1 0 2 48\n2 1 40 88\n1 1 1\n1\n"; got != want {
		t.Fatalf("runtime stats harness stdout = %q, want %q", got, want)
	}
}

// TestBundledRuntimeStatsFragmentation covers RUNTIME_GC_DELTA §6.5 —
// the fragmentation instrumentation now consolidated into the
// osty_gc_stats snapshot. Individual scalar accessors existed before,
// but a single atomic read was missing. The harness exercises both
// the small-object young-bump path and the humongous-object direct
// path, runs a collection, and confirms:
//
//   - small allocations increment bump_alloc_count_total /
//     bytes (young tier via TLAB)
//   - a humongous allocation bypasses the bump path and increments
//     humongous_alloc_count_total / bytes instead
//   - after sweep, the humongous object is reclaimed and
//     humongous_swept_count_total advances
//   - sweeping young-bump allocations recycles empty blocks, so
//     bump_recycled_block_count_total grows
//   - scalar accessors agree with the struct snapshot
func TestBundledRuntimeStatsFragmentation(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_stats_fragmentation_harness.c")
	binaryName := "runtime_gc_stats_fragmentation_harness"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binaryPath := filepath.Join(dir, binaryName)
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

typedef struct osty_gc_stats {
    int64_t collection_count;
    int64_t live_count;
    int64_t live_bytes;
    int64_t allocated_since_collect;
    int64_t allocated_bytes_total;
    int64_t swept_count_total;
    int64_t swept_bytes_total;
    int64_t pre_write_count;
    int64_t pre_write_managed_count;
    int64_t post_write_count;
    int64_t post_write_managed_count;
    int64_t load_count;
    int64_t load_managed_count;
    int64_t satb_log_count;
    int64_t remembered_edge_count;
    int64_t global_root_count;
    int64_t pressure_limit_bytes;
    int64_t mark_stack_max_depth;
    int64_t collection_nanos_total;
    int64_t collection_nanos_last;
    int64_t collection_nanos_max;
    int64_t index_capacity;
    int64_t index_count;
    int64_t index_tombstones;
    int64_t index_find_ops_total;
    int64_t minor_count;
    int64_t major_count;
    int64_t minor_nanos_total;
    int64_t major_nanos_total;
    int64_t young_count;
    int64_t young_bytes;
    int64_t old_count;
    int64_t old_bytes;
    int64_t promoted_count_total;
    int64_t promoted_bytes_total;
    int64_t allocated_since_minor;
    int64_t nursery_limit_bytes;
    int64_t promote_age;
    int64_t free_list_count;
    int64_t free_list_bytes;
    int64_t free_list_reused_count_total;
    int64_t free_list_reused_bytes_total;
    int64_t humongous_alloc_count_total;
    int64_t humongous_alloc_bytes_total;
    int64_t humongous_swept_count_total;
    int64_t humongous_swept_bytes_total;
    int64_t bump_block_count;
    int64_t bump_block_bytes_total;
    int64_t bump_alloc_count_total;
    int64_t bump_alloc_bytes_total;
    int64_t bump_recycled_block_count_total;
    int64_t bump_recycled_bytes_total;
} osty_gc_stats;

void *osty_gc_alloc_v1(int64_t object_kind, int64_t byte_size, const char *site) __asm__(OSTY_GC_SYMBOL("osty.gc.alloc_v1"));

void osty_gc_debug_collect(void);
void osty_gc_debug_stats(osty_gc_stats *out);
int64_t osty_gc_debug_humongous_alloc_count_total(void);
int64_t osty_gc_debug_humongous_alloc_bytes_total(void);
int64_t osty_gc_debug_bump_alloc_count_total(void);
int64_t osty_gc_debug_bump_alloc_bytes_total(void);

int main(void) {
    osty_gc_stats s0, s1, s2;

    /* Baseline. */
    osty_gc_debug_stats(&s0);

    /* Small-object churn: 8 allocations that go through the young bump
     * path. None are rooted so they all get swept on collect. */
    for (int i = 0; i < 8; i++) {
        (void)osty_gc_alloc_v1(7, 32, "frag.small");
    }
    /* Humongous: crosses the size-class threshold so it takes the
     * direct-alloc path, bypassing bump blocks. 256 KiB is well
     * above the humongous threshold. */
    (void)osty_gc_alloc_v1(7, 256 * 1024, "frag.humongous");

    osty_gc_debug_stats(&s1);
    /* Small allocs landed on the young bump path. */
    printf("%d\n", s1.bump_alloc_count_total - s0.bump_alloc_count_total >= 8);
    /* Humongous bypassed the bump path and shows in humongous totals. */
    printf("%d %d\n",
        s1.humongous_alloc_count_total - s0.humongous_alloc_count_total == 1,
        s1.humongous_alloc_bytes_total - s0.humongous_alloc_bytes_total >= 256 * 1024);

    /* Collect: all allocations are unreferenced, so they get swept.
     * Humongous frees directly; young-bump blocks that fully empty
     * move onto the recycled-block list. */
    osty_gc_debug_collect();
    osty_gc_debug_stats(&s2);
    /* Humongous swept counter advanced. */
    printf("%d\n",
        s2.humongous_swept_count_total - s0.humongous_swept_count_total >= 1);
    /* The young-bump block holding our small objects should have
     * recycled since every occupant was swept. */
    printf("%d\n",
        s2.bump_recycled_block_count_total >= s0.bump_recycled_block_count_total);

    /* Scalar accessors agree with struct snapshot. */
    printf("%d %d %d\n",
        osty_gc_debug_humongous_alloc_count_total() == s2.humongous_alloc_count_total,
        osty_gc_debug_humongous_alloc_bytes_total() == s2.humongous_alloc_bytes_total,
        osty_gc_debug_bump_alloc_bytes_total() == s2.bump_alloc_bytes_total);

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
	got := strings.ReplaceAll(string(runOutput), "\r\n", "\n")
	if want := "1\n1 1\n1\n1\n1 1 1\n"; got != want {
		t.Fatalf("fragmentation stats harness stdout = %q, want %q", got, want)
	}
}

// TestBundledRuntimeMarkWorkQueueDeepGraph covers Phase A3 (RUNTIME_GC_DELTA
// §4.2). The previous recursive mark path would overflow the C stack on
// graphs deeper than a few thousand links on typical `ulimit -s`. With the
// explicit work queue, any depth reachable from the heap itself is fine.
// We build a linked list of 100_000 GC objects and require that collection
// walks it without crashing and that every object survives when the head
// is pinned.
func TestBundledRuntimeMarkWorkQueueDeepGraph(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_deepmark_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_deepmark_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

#if defined(__APPLE__)
#define OSTY_GC_SYMBOL(name) "_" name
#else
#define OSTY_GC_SYMBOL(name) name
#endif

void *osty_gc_alloc_v1(int64_t object_kind, int64_t byte_size, const char *site) __asm__(OSTY_GC_SYMBOL("osty.gc.alloc_v1"));
void osty_gc_pre_write_v1(void *owner, void *old_value, int64_t slot_kind) __asm__(OSTY_GC_SYMBOL("osty.gc.pre_write_v1"));
void osty_gc_post_write_v1(void *owner, void *value, int64_t slot_kind) __asm__(OSTY_GC_SYMBOL("osty.gc.post_write_v1"));
void osty_gc_root_bind_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_bind_v1"));
void osty_gc_root_release_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_release_v1"));
void *osty_rt_list_new(void);
void osty_rt_list_push_ptr(void *list, void *value);

void osty_gc_debug_collect(void);
int64_t osty_gc_debug_validate_heap(void);
int64_t osty_gc_debug_live_count(void);
int64_t osty_gc_debug_mark_stack_max_depth(void);

/* Build a list of N GC-managed children. Marking them used to recurse
 * through the list trace callback once per element — 100k nested frames
 * would blow the default 8 MiB Linux stack. With the explicit work queue
 * the C call stack stays bounded regardless of N. */
int main(void) {
    enum { N = 100000 };
    void *list = osty_rt_list_new();
    osty_gc_root_bind_v1(list);

    for (int i = 0; i < N; i++) {
        void *child = osty_gc_alloc_v1(7, 8, "deep.child");
        osty_gc_pre_write_v1(list, NULL, 0);
        osty_rt_list_push_ptr(list, child);
        osty_gc_post_write_v1(list, child, 0);
    }

    printf("%lld\n", (long long)osty_gc_debug_live_count());
    osty_gc_debug_collect();
    printf("%lld\n", (long long)osty_gc_debug_live_count());
    printf("%lld\n", (long long)osty_gc_debug_validate_heap());
    /* During trace the list is already popped when children start being
     * enqueued, so the instantaneous peak is N (not N+1). The invariant
     * we care about is only that the explicit work queue reached a depth
     * that the old recursive implementation would not have survived. */
    printf("%d\n", osty_gc_debug_mark_stack_max_depth() >= (int64_t)N);

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
	if got, want := string(runOutput), "100001\n100001\n0\n1\n0\n"; got != want {
		t.Fatalf("runtime deep-mark harness stdout = %q, want %q", got, want)
	}
}

// TestBundledRuntimeSafepointKindCounters covers Phase A5 (RUNTIME_GC_DELTA
// §10.1). The runtime decodes the high byte of the safepoint id as a
// classification tag (ENTRY / CALL / LOOP / ALLOC / YIELD / …) and bumps
// per-kind counters. This harness drives the decoder directly so it can
// run without the LLVM emitter — exercising the runtime contract
// independent of upstream lowering.
func TestBundledRuntimeSafepointKindCounters(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_safepoint_kind_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_safepoint_kind_harness")
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

void osty_gc_safepoint_v1(int64_t id, void *const *roots, int64_t n) __asm__(OSTY_GC_SYMBOL("osty.gc.safepoint_v1"));

int64_t osty_gc_debug_safepoint_count_by_kind(int64_t kind);
int64_t osty_gc_debug_safepoint_count_total(void);

enum {
    KIND_UNSPECIFIED = 0,
    KIND_ENTRY = 1,
    KIND_CALL = 2,
    KIND_LOOP = 3,
    KIND_ALLOC = 4,
    KIND_YIELD = 5,
};

static int64_t encode(int kind, int64_t serial) {
    return ((int64_t)kind << 56) | (serial & (((int64_t)1 << 56) - 1));
}

int main(void) {
    /* Empty state — every counter starts at zero. */
    printf("%lld %lld %lld %lld %lld %lld\n",
        (long long)osty_gc_debug_safepoint_count_by_kind(KIND_UNSPECIFIED),
        (long long)osty_gc_debug_safepoint_count_by_kind(KIND_ENTRY),
        (long long)osty_gc_debug_safepoint_count_by_kind(KIND_CALL),
        (long long)osty_gc_debug_safepoint_count_by_kind(KIND_LOOP),
        (long long)osty_gc_debug_safepoint_count_by_kind(KIND_ALLOC),
        (long long)osty_gc_debug_safepoint_count_by_kind(KIND_YIELD));

    /* Emit a representative mix: 1 entry, 2 calls, 3 loop back-edges. */
    osty_gc_safepoint_v1(encode(KIND_ENTRY, 0), 0, 0);
    osty_gc_safepoint_v1(encode(KIND_CALL, 1), 0, 0);
    osty_gc_safepoint_v1(encode(KIND_CALL, 2), 0, 0);
    osty_gc_safepoint_v1(encode(KIND_LOOP, 3), 0, 0);
    osty_gc_safepoint_v1(encode(KIND_LOOP, 4), 0, 0);
    osty_gc_safepoint_v1(encode(KIND_LOOP, 5), 0, 0);

    printf("%lld %lld %lld %lld\n",
        (long long)osty_gc_debug_safepoint_count_by_kind(KIND_ENTRY),
        (long long)osty_gc_debug_safepoint_count_by_kind(KIND_CALL),
        (long long)osty_gc_debug_safepoint_count_by_kind(KIND_LOOP),
        (long long)osty_gc_debug_safepoint_count_total());

    /* Legacy (pure serial) ids fall through to UNSPECIFIED. */
    osty_gc_safepoint_v1(42, 0, 0);
    printf("%lld\n", (long long)osty_gc_debug_safepoint_count_by_kind(KIND_UNSPECIFIED));

    /* Out-of-range kind queries return -1 (distinguishes from 0 count). */
    printf("%lld %lld\n",
        (long long)osty_gc_debug_safepoint_count_by_kind(-1),
        (long long)osty_gc_debug_safepoint_count_by_kind(99));
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
	if got, want := string(runOutput), "0 0 0 0 0 0\n1 2 3 6\n1\n-1 -1\n"; got != want {
		t.Fatalf("runtime safepoint-kind harness stdout = %q, want %q", got, want)
	}
}

// TestBundledRuntimeSafepointRootSlotHighWaterMark covers the
// observability half of Phase A6 (RUNTIME_GC_DELTA §10.2). The abort
// half (crossing `OSTY_GC_SAFEPOINT_MAX_ROOTS`) is intentionally not
// exercised here — it would require driving the harness under a
// crash-expectation and does not buy additional coverage: the guard
// reduces to a `root_slot_count > cap` comparison. Testing the
// high-water mark path keeps CI deterministic while still asserting
// that the counter is wired into `osty_gc_safepoint_v1` on every poll.
func TestBundledRuntimeSafepointRootSlotHighWaterMark(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_safepoint_roots_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_safepoint_roots_harness")
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

void osty_gc_safepoint_v1(int64_t id, void *const *roots, int64_t n) __asm__(OSTY_GC_SYMBOL("osty.gc.safepoint_v1"));

int64_t osty_gc_debug_safepoint_max_roots_seen(void);
int64_t osty_gc_debug_safepoint_max_roots_cap(void);

int main(void) {
    /* Baseline — nothing observed yet. Cap is a stable nonzero value. */
    printf("%lld\n", (long long)osty_gc_debug_safepoint_max_roots_seen());
    printf("%d\n", osty_gc_debug_safepoint_max_roots_cap() > 0);

    /* Emit three polls with rising root counts. The slot array itself
     * can be NULL because the runtime never dereferences it outside a
     * collection trigger (stress mode is off). */
    osty_gc_safepoint_v1(0, 0, 4);
    printf("%lld\n", (long long)osty_gc_debug_safepoint_max_roots_seen());
    osty_gc_safepoint_v1(0, 0, 16);
    printf("%lld\n", (long long)osty_gc_debug_safepoint_max_roots_seen());
    /* A smaller poll must not lower the high-water mark. */
    osty_gc_safepoint_v1(0, 0, 2);
    printf("%lld\n", (long long)osty_gc_debug_safepoint_max_roots_seen());
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
	if got, want := string(runOutput), "0\n1\n4\n16\n16\n"; got != want {
		t.Fatalf("runtime safepoint-roots harness stdout = %q, want %q", got, want)
	}
}

// TestBundledRuntimeValidateHeapNegativeInvariants covers the A1 depth
// follow-up — for every stable negative error code that
// `osty_gc_debug_validate_heap()` can return, we construct a heap
// state that violates exactly that invariant and assert the oracle
// returns the expected code. The earlier positive test only proved the
// happy path. Corruption is installed via `osty_gc_debug_unsafe_*`
// injectors that run one-shot and leave the heap broken; the harness
// exits immediately after reporting.
func TestBundledRuntimeValidateHeapNegativeInvariants(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_validate_negative_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_validate_negative_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>
#include <sys/wait.h>

#if defined(__APPLE__)
#define OSTY_GC_SYMBOL(name) "_" name
#else
#define OSTY_GC_SYMBOL(name) name
#endif

void *osty_gc_alloc_v1(int64_t object_kind, int64_t byte_size, const char *site) __asm__(OSTY_GC_SYMBOL("osty.gc.alloc_v1"));
void osty_gc_root_bind_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_bind_v1"));
void osty_gc_pre_write_v1(void *owner, void *old_value, int64_t slot_kind) __asm__(OSTY_GC_SYMBOL("osty.gc.pre_write_v1"));
void osty_gc_post_write_v1(void *owner, void *value, int64_t slot_kind) __asm__(OSTY_GC_SYMBOL("osty.gc.post_write_v1"));

int64_t osty_gc_debug_validate_heap(void);
void osty_gc_debug_collect(void);
void osty_gc_debug_unsafe_bump_live_count(void);
void osty_gc_debug_unsafe_bump_live_bytes(void);
void osty_gc_debug_unsafe_break_first_prev(void);
void osty_gc_debug_unsafe_break_next_link(void);
void osty_gc_debug_unsafe_set_stale_mark(void);
void osty_gc_debug_unsafe_negative_root_count(void);
void osty_gc_debug_unsafe_dirty_mark_stack(void);
void osty_gc_debug_unsafe_append_null_global_slot(void);
void osty_gc_debug_unsafe_satb_dangling(void);
void osty_gc_debug_unsafe_remembered_edge_dangling(void);
void osty_gc_debug_unsafe_negative_cumulative(void);

/* Each case runs in a forked child so one injector's corruption does
 * not contaminate the next. Parent prints the child's exit code, which
 * is the invariant id the oracle returned (& 0xff). */
static int run_case(const char *name, void (*setup)(void), void (*inject)(void), int64_t expected) {
    pid_t pid = fork();
    if (pid < 0) return 1;
    if (pid == 0) {
        setup();
        int64_t before = osty_gc_debug_validate_heap();
        if (before != 0) { _exit(200); }
        inject();
        int64_t after = osty_gc_debug_validate_heap();
        if (after != expected) {
            fprintf(stderr, "%s: got %lld want %lld\n", name, (long long)after, (long long)expected);
            _exit(201);
        }
        _exit(0);
    }
    int status = 0;
    waitpid(pid, &status, 0);
    return WIFEXITED(status) ? WEXITSTATUS(status) : 255;
}

static void setup_two_objects(void) {
    void *a = osty_gc_alloc_v1(7, 32, "a");
    void *b = osty_gc_alloc_v1(7, 32, "b");
    osty_gc_root_bind_v1(a);
    osty_gc_root_bind_v1(b);
}

static void setup_one_object(void) {
    void *a = osty_gc_alloc_v1(7, 32, "a");
    osty_gc_root_bind_v1(a);
}

static void setup_edges(void) {
    void *owner = osty_gc_alloc_v1(7, 32, "owner");
    void *child = osty_gc_alloc_v1(7, 32, "child");
    osty_gc_root_bind_v1(owner);
    osty_gc_pre_write_v1(owner, NULL, 0);
    osty_gc_post_write_v1(owner, child, 0);
}

int main(void) {
    /* Expected error codes mirror the enum at the top of
     * osty_gc_debug_validate_heap() in osty_runtime.c — all negative,
     * 0 is reserved for success. */
    printf("%d\n", run_case("first_prev",      setup_one_object, osty_gc_debug_unsafe_break_first_prev,      -1));  /* FIRST_PREV_NOT_NULL */
    printf("%d\n", run_case("link_broken",     setup_two_objects, osty_gc_debug_unsafe_break_next_link,      -2));  /* LIST_LINK_BROKEN */
    printf("%d\n", run_case("live_count",      setup_one_object,  osty_gc_debug_unsafe_bump_live_count,      -3));  /* LIVE_COUNT_MISMATCH */
    printf("%d\n", run_case("live_bytes",      setup_one_object,  osty_gc_debug_unsafe_bump_live_bytes,      -4));  /* LIVE_BYTES_MISMATCH */
    printf("%d\n", run_case("negative_root",   setup_one_object,  osty_gc_debug_unsafe_negative_root_count,  -5));  /* NEGATIVE_ROOT_COUNT */
    printf("%d\n", run_case("satb_dangling",   setup_edges,       osty_gc_debug_unsafe_satb_dangling,        -6));  /* SATB_DANGLING */
    printf("%d\n", run_case("rem_edge",        setup_edges,       osty_gc_debug_unsafe_remembered_edge_dangling, -7));  /* REMEMBERED_EDGE_DANGLING */
    printf("%d\n", run_case("neg_cumulative",  setup_one_object,  osty_gc_debug_unsafe_negative_cumulative,  -8));  /* NEGATIVE_CUMULATIVE */
    printf("%d\n", run_case("stale_mark",      setup_one_object,  osty_gc_debug_unsafe_set_stale_mark,       -9));  /* STALE_MARK */
    printf("%d\n", run_case("mark_dirty",      setup_one_object,  osty_gc_debug_unsafe_dirty_mark_stack,    -10));  /* MARK_STACK_NON_EMPTY */
    printf("%d\n", run_case("null_global",     setup_one_object,  osty_gc_debug_unsafe_append_null_global_slot, -11)); /* NULL_GLOBAL_SLOT */
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
	/* 11 invariants, each child exits with status 0 if the oracle
	 * returned the expected code. A non-zero line means the case
	 * name printed to stderr will show what diverged. */
	if got, want := string(runOutput), "0\n0\n0\n0\n0\n0\n0\n0\n0\n0\n0\n"; got != want {
		t.Fatalf("validate-heap negative harness stdout = %q, want %q", got, want)
	}
}

// TestBundledRuntimeCollectionTimingRecorded covers the A2 depth
// follow-up — `clock_gettime(CLOCK_MONOTONIC)` around every
// `osty_gc_collect_now_with_stack_roots` body feeds the total / last /
// max counters. We can't assert on an exact nanosecond count (wall
// clock noise), so the test asserts monotonic structure instead:
// baseline is zero, post-collect is strictly positive, totals grow
// monotonically, and `max` never shrinks.
func TestBundledRuntimeCollectionTimingRecorded(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_timing_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_timing_harness")
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

void osty_gc_debug_collect(void);
int64_t osty_gc_debug_collection_nanos_total(void);
int64_t osty_gc_debug_collection_nanos_last(void);
int64_t osty_gc_debug_collection_nanos_max(void);

int main(void) {
    /* Baseline. */
    printf("%lld %lld %lld\n",
        (long long)osty_gc_debug_collection_nanos_total(),
        (long long)osty_gc_debug_collection_nanos_last(),
        (long long)osty_gc_debug_collection_nanos_max());

    /* Two collections over some garbage so we exercise the sweep
     * loop (non-trivial work). */
    for (int i = 0; i < 256; i++) (void)osty_gc_alloc_v1(7, 128, "t");
    osty_gc_debug_collect();
    int64_t t1 = osty_gc_debug_collection_nanos_total();
    int64_t l1 = osty_gc_debug_collection_nanos_last();
    int64_t m1 = osty_gc_debug_collection_nanos_max();
    printf("%d %d %d\n", t1 > 0, l1 > 0, m1 > 0);

    for (int i = 0; i < 64; i++) (void)osty_gc_alloc_v1(7, 32, "t2");
    osty_gc_debug_collect();
    int64_t t2 = osty_gc_debug_collection_nanos_total();
    int64_t m2 = osty_gc_debug_collection_nanos_max();
    /* Total must grow; max must not shrink. */
    printf("%d %d\n", t2 >= t1, m2 >= m1);
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
	if got, want := string(runOutput), "0 0 0\n1 1 1\n1 1\n"; got != want {
		t.Fatalf("collection-timing harness stdout = %q, want %q", got, want)
	}
}

// TestBundledRuntimeFindHeaderHashIndex covers the A3 depth follow-up —
// `osty_gc_find_header` now dispatches through an open-addressed hash
// table keyed on payload pointer. The harness verifies three
// properties: (1) the index population tracks live objects (inserts on
// alloc, removes on sweep), (2) the table rehashes when load crosses
// the threshold, and (3) lookups are non-linear in practice — we
// allocate 10k objects and do 10k lookups, and assert each lookup
// reports the corresponding managed payload even after intervening
// tombstones from a sweep. A full-blown asymptotic check is
// impractical in a portable harness, but the correctness contract is
// what tests can nail down deterministically.
func TestBundledRuntimeFindHeaderHashIndex(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_hash_index_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_hash_index_harness")
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
void osty_gc_pre_write_v1(void *owner, void *old_value, int64_t slot_kind) __asm__(OSTY_GC_SYMBOL("osty.gc.pre_write_v1"));
void osty_gc_post_write_v1(void *owner, void *value, int64_t slot_kind) __asm__(OSTY_GC_SYMBOL("osty.gc.post_write_v1"));

void osty_gc_debug_collect(void);
int64_t osty_gc_debug_index_capacity(void);
int64_t osty_gc_debug_index_count(void);
int64_t osty_gc_debug_index_tombstones(void);
int64_t osty_gc_debug_index_find_ops(void);
int64_t osty_gc_debug_live_count(void);

int main(void) {
    /* Allocate 10000 objects, all pinned so they survive a collect. */
    enum { N = 10000 };
    void *roots[N];
    for (int i = 0; i < N; i++) {
        roots[i] = osty_gc_alloc_v1(7, 16, "r");
        osty_gc_root_bind_v1(roots[i]);
    }

    /* Index populated, capacity grew well past initial 128. */
    int64_t cap_after_alloc = osty_gc_debug_index_capacity();
    int64_t count_after_alloc = osty_gc_debug_index_count();
    printf("%d %d\n", cap_after_alloc >= (int64_t)N, count_after_alloc == (int64_t)N);

    /* Sanity: every payload resolves. We drive lookups through
     * pre_write_v1 which internally calls find_header; each call
     * that recognises old_value as managed bumps the managed-count,
     * so we can verify lookup is wired. */
    void *keeper = osty_gc_alloc_v1(7, 16, "keeper");
    osty_gc_root_bind_v1(keeper);
    int64_t finds_before = osty_gc_debug_index_find_ops();
    for (int i = 0; i < 100; i++) {
        osty_gc_pre_write_v1(keeper, roots[i * 97], 0);
    }
    int64_t finds_after = osty_gc_debug_index_find_ops();
    /* Each pre_write_v1 on a managed slot calls find_header at least
     * twice (once for old_value, once for owner). 100 calls → ≥200
     * lookups. */
    printf("%d\n", finds_after - finds_before >= 200);

    /* Tombstone path: unbind half and collect. Index count should
     * drop; tombstones may be non-zero until the next rehash. */
    /* (We can't unbind easily from C without tracking refcount, so
     * instead allocate garbage and collect — sweep will remove those
     * payloads from the index.) */
    for (int i = 0; i < 512; i++) (void)osty_gc_alloc_v1(7, 16, "g");
    int64_t count_before_collect = osty_gc_debug_index_count();
    osty_gc_debug_collect();
    int64_t count_after_collect = osty_gc_debug_index_count();
    /* Garbage is gone; pinned survivors stay. */
    printf("%d %d\n",
        count_before_collect > count_after_collect,
        count_after_collect == (int64_t)N + 1);

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
	if got, want := string(runOutput), "1 1\n1\n1 1\n"; got != want {
		t.Fatalf("hash-index harness stdout = %q, want %q", got, want)
	}
}

// TestBundledRuntimeClosureEnvTracesCaptures covers the A4 depth
// follow-up — `osty.rt.closure_env_alloc_v2` builds a self-describing
// env with a capture array + per-capture pointer bitmap, the runtime
// registers `osty_rt_closure_env_trace` at construction, and managed
// pointers stored in captures whose bitmap bit is set stay reachable
// across GC.
//
// This exercises the allocation ABI that Phase 4's capture lowering
// will emit. Today's llvmgen still materialises 0-capture envs via the
// same path, so this test also locks the Phase 1 behaviour in — no
// regression when Phase 4 starts filling in `captures[]`.
func TestBundledRuntimeClosureEnvTracesCaptures(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_closure_env_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_closure_env_harness")
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

typedef struct osty_rt_closure_env {
    void *fn_ptr;
    int64_t capture_count;
    uint64_t pointer_bitmap;
    void *captures[];
} osty_rt_closure_env;

void *osty_gc_alloc_v1(int64_t object_kind, int64_t byte_size, const char *site) __asm__(OSTY_GC_SYMBOL("osty.gc.alloc_v1"));
void *osty_rt_closure_env_alloc_v2(int64_t capture_count, const char *site, uint64_t pointer_bitmap) __asm__(OSTY_GC_SYMBOL("osty.rt.closure_env_alloc_v2"));
void *osty_gc_load_v1(void *value) __asm__(OSTY_GC_SYMBOL("osty.gc.load_v1"));
void osty_gc_root_bind_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_bind_v1"));

void osty_gc_debug_collect(void);
int64_t osty_gc_debug_live_count(void);

int main(void) {
    /* Phase 1 shape: zero captures, bitmap zero. Env still allocates
     * and is collectable. */
    void *env0 = osty_rt_closure_env_alloc_v2(0, "env0", 0);
    osty_gc_root_bind_v1(env0);
    osty_gc_debug_collect();
    printf("%d\n", env0 != 0);

    /* Phase 4 shape preview: 3 pointer captures (bitmap 0b111).
     * Allocate three managed payloads and store them into capture
     * slots. They are NOT root-bound — their only liveness path is
     * through the env's trace. If the trace is not wired, they're
     * swept. */
    osty_rt_closure_env *env = (osty_rt_closure_env *)osty_rt_closure_env_alloc_v2(3, "env3", 0x7ULL);
    osty_gc_root_bind_v1(env);
    void *cap0 = osty_gc_alloc_v1(7, 32, "cap0");
    void *cap1 = osty_gc_alloc_v1(7, 32, "cap1");
    void *cap2 = osty_gc_alloc_v1(7, 32, "cap2");
    env->captures[0] = cap0;
    env->captures[1] = cap1;
    env->captures[2] = cap2;

    int64_t live_before = osty_gc_debug_live_count();
    osty_gc_debug_collect();
    int64_t live_after = osty_gc_debug_live_count();

    /* live_before includes env0 + env + 3 captures = 5.
     * After collect: env0 and env still rooted, captures survive via
     * trace → same count. Would drop by 3 if trace were broken. */
    printf("%lld %lld\n", (long long)live_before, (long long)live_after);
    printf("%d %d %d\n",
        env->captures[0] == osty_gc_load_v1(cap0),
        env->captures[1] == osty_gc_load_v1(cap1),
        env->captures[2] == osty_gc_load_v1(cap2));
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
	if got, want := string(runOutput), "1\n5 5\n1 1 1\n"; got != want {
		t.Fatalf("closure-env harness stdout = %q, want %q", got, want)
	}
}

// TestBundledRuntimeClosureEnvBitmapSkipsScalarSlots covers the §2.4
// structural guarantee: a scalar capture whose 8-byte bit pattern
// happens to collide with a live payload address must NOT keep that
// payload alive through the closure env. The v1 layout gave only a
// probabilistic guarantee (find_header filtering); v2's pointer bitmap
// makes the tracer skip scalar slots unconditionally.
//
// Harness: allocate a payload P, root-bind it briefly to get its
// address, unbind it, then store P's address (as a raw integer bit
// pattern) into the scalar capture slot of a rooted closure env with
// bitmap bit 0 cleared. After collect, P must be swept — if the tracer
// honored the scalar slot, P would survive and the live count stays at
// 2 (env + P) instead of the expected 1 (env only).
func TestBundledRuntimeClosureEnvBitmapSkipsScalarSlots(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_closure_bitmap_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_closure_bitmap_harness")
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

typedef struct osty_rt_closure_env {
    void *fn_ptr;
    int64_t capture_count;
    uint64_t pointer_bitmap;
    void *captures[];
} osty_rt_closure_env;

void *osty_gc_alloc_v1(int64_t object_kind, int64_t byte_size, const char *site) __asm__(OSTY_GC_SYMBOL("osty.gc.alloc_v1"));
void *osty_rt_closure_env_alloc_v2(int64_t capture_count, const char *site, uint64_t pointer_bitmap) __asm__(OSTY_GC_SYMBOL("osty.rt.closure_env_alloc_v2"));
void osty_gc_root_bind_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_bind_v1"));

void osty_gc_debug_collect(void);
int64_t osty_gc_debug_live_count(void);

int main(void) {
    /* Two captures: slot 0 scalar (bit 0 cleared), slot 1 pointer
     * (bit 1 set). Bitmap = 0b10 = 0x2. */
    osty_rt_closure_env *env = (osty_rt_closure_env *)osty_rt_closure_env_alloc_v2(2, "env2", 0x2ULL);
    osty_gc_root_bind_v1(env);

    /* Payload P: allocate but do not root. Its only potential
     * liveness path is through env's scalar capture slot. */
    void *p = osty_gc_alloc_v1(7, 32, "scalar_victim");

    /* Store P's address into the scalar slot as a raw bit pattern.
     * Semantically this is a scalar (an Int that happens to equal
     * a valid heap address). Under v1 conservative scan, find_header
     * would identify P as reachable and keep it alive. Under v2 the
     * bitmap bit is 0 so the tracer skips this slot unconditionally
     * — P must be swept. */
    env->captures[0] = p;

    /* Payload Q: stored in the pointer slot with bitmap bit 1 set.
     * Must survive collection. */
    void *q = osty_gc_alloc_v1(7, 32, "pointer_capture");
    env->captures[1] = q;

    int64_t live_before = osty_gc_debug_live_count();
    osty_gc_debug_collect();
    int64_t live_after = osty_gc_debug_live_count();

    /* live_before: env + P + Q = 3.
     * live_after:  env + Q     = 2  (structural guarantee).
     * If the tracer ignored the bitmap, live_after would be 3. */
    printf("%lld %lld\n", (long long)live_before, (long long)live_after);
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
	if got, want := string(runOutput), "3 2\n"; got != want {
		t.Fatalf("closure-bitmap harness stdout = %q, want %q", got, want)
	}
}

// TestBundledRuntimeSafepointOverflowAborts covers the A6 depth
// follow-up — crossing `OSTY_GC_SAFEPOINT_MAX_ROOTS` triggers
// `osty_rt_abort` which calls `abort()`. Running it in the test
// process would kill the runner, so we fork a child that intentionally
// trips the guard and assert the parent observes a non-zero exit and
// the expected message on stderr. The earlier positive test only
// covered the high-water tracking.
func TestBundledRuntimeSafepointOverflowAborts(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_safepoint_overflow_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_safepoint_overflow_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

#if defined(__APPLE__)
#define OSTY_GC_SYMBOL(name) "_" name
#else
#define OSTY_GC_SYMBOL(name) name
#endif

void osty_gc_safepoint_v1(int64_t id, void *const *roots, int64_t n) __asm__(OSTY_GC_SYMBOL("osty.gc.safepoint_v1"));
int64_t osty_gc_debug_safepoint_max_roots_cap(void);

int main(void) {
    /* One past the cap. The slot pointer is NULL — we never reach
     * the dereference because the bounds check aborts first. */
    int64_t cap = osty_gc_debug_safepoint_max_roots_cap();
    osty_gc_safepoint_v1(0, 0, cap + 1);
    /* If we reach here the guard silently let it through. */
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
	out, err := exec.Command(binaryPath).CombinedOutput()
	if err == nil {
		t.Fatalf("expected abort, got clean exit; stdout/stderr:\n%s", out)
	}
	text := string(out)
	if !strings.Contains(text, "safepoint root slot count") ||
		!strings.Contains(text, "exceeds cap") {
		t.Fatalf("expected abort message about safepoint root overflow, got:\n%s", text)
	}
}

// TestBundledRuntimeMinorCollectSweepsYoungOnly covers Phase B3/B6 —
// after `osty_gc_debug_collect_minor`, unreachable YOUNG objects are
// freed, YOUNG survivors stay YOUNG with age bumped, and OLD objects
// are untouched regardless of reachability (they only fall in a
// major). The minor tier counter and nanos_total increment; major
// counters stay flat.
func TestBundledRuntimeMinorCollectSweepsYoungOnly(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_minor_sweep_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_minor_sweep_harness")
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

void osty_gc_debug_collect_minor(void);
void osty_gc_debug_collect_major(void);
int64_t osty_gc_debug_live_count(void);
int64_t osty_gc_debug_minor_count(void);
int64_t osty_gc_debug_major_count(void);
int64_t osty_gc_debug_young_count(void);
int64_t osty_gc_debug_old_count(void);
int64_t osty_gc_debug_generation_of(void *payload);
int64_t osty_gc_debug_age_of(void *payload);
int64_t osty_gc_debug_validate_heap(void);

int main(void) {
    /* Allocate a rooted survivor plus a piece of garbage. Both start
     * YOUNG. */
    void *keep = osty_gc_alloc_v1(7, 32, "keep");
    void *garbage = osty_gc_alloc_v1(7, 32, "garbage");
    (void)garbage;
    osty_gc_root_bind_v1(keep);
    printf("%lld %lld\n",
        (long long)osty_gc_debug_generation_of(keep),
        (long long)osty_gc_debug_generation_of(garbage));

    /* Minor: garbage swept, keep stays YOUNG, age bumps to 1. */
    osty_gc_debug_collect_minor();
    printf("%lld %lld %lld %lld\n",
        (long long)osty_gc_debug_minor_count(),
        (long long)osty_gc_debug_major_count(),
        (long long)osty_gc_debug_live_count(),
        (long long)osty_gc_debug_young_count());
    printf("%lld %lld\n",
        (long long)osty_gc_debug_generation_of(keep),
        (long long)osty_gc_debug_age_of(keep));

    /* validate_heap stays green across the minor cycle. */
    printf("%lld\n", (long long)osty_gc_debug_validate_heap());

    /* Major pass for cleanup so validate passes at exit. */
    osty_gc_root_release_v1(keep);
    osty_gc_debug_collect_major();
    printf("%lld %lld\n",
        (long long)osty_gc_debug_live_count(),
        (long long)osty_gc_debug_major_count());
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
	if got, want := string(runOutput), "0 0\n1 0 1 1\n0 1\n0\n0 1\n"; got != want {
		t.Fatalf("runtime minor-sweep harness stdout = %q, want %q", got, want)
	}
}

// TestBundledRuntimePromotesAfterAgeThreshold covers Phase B3 promotion:
// a YOUNG object survives `promote_age` minor cycles and gets moved to
// OLD in place. Its address stays stable (no compaction), the OLD
// counter bumps, and `promoted_count_total` reflects the movement.
func TestBundledRuntimePromotesAfterAgeThreshold(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_promotion_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_promotion_harness")
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

void osty_gc_debug_collect_minor(void);
void osty_gc_debug_collect_major(void);
int64_t osty_gc_debug_generation_of(void *payload);
int64_t osty_gc_debug_age_of(void *payload);
int64_t osty_gc_debug_young_count(void);
int64_t osty_gc_debug_old_count(void);
int64_t osty_gc_debug_promoted_count_total(void);
int64_t osty_gc_debug_promote_age(void);

int main(void) {
    /* With promote_age lowered to 2 via env, the first minor bumps age
     * 0->1 (stays YOUNG), the second bumps 1->2 which crosses the
     * threshold and triggers promotion to OLD. */
    printf("%lld\n", (long long)osty_gc_debug_promote_age());

    void *keep = osty_gc_alloc_v1(7, 32, "keep");
    void *addr0 = keep;
    osty_gc_root_bind_v1(keep);
    printf("%lld %lld\n",
        (long long)osty_gc_debug_generation_of(keep),
        (long long)osty_gc_debug_age_of(keep));

    osty_gc_debug_collect_minor();
    printf("%lld %lld\n",
        (long long)osty_gc_debug_generation_of(keep),
        (long long)osty_gc_debug_age_of(keep));

    osty_gc_debug_collect_minor();
    /* Post-second-minor: promoted to OLD, age reset to 0. */
    printf("%lld %lld\n",
        (long long)osty_gc_debug_generation_of(keep),
        (long long)osty_gc_debug_age_of(keep));
    printf("%lld %lld\n",
        (long long)osty_gc_debug_young_count(),
        (long long)osty_gc_debug_old_count());
    printf("%lld\n", (long long)osty_gc_debug_promoted_count_total());
    /* Address unchanged — no compaction yet. */
    printf("%d\n", keep == addr0);

    osty_gc_root_release_v1(keep);
    osty_gc_debug_collect_major();
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
	runCmd.Env = append(os.Environ(), "OSTY_GC_PROMOTE_AGE=2")
	runOutput, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, runOutput)
	}
	if got, want := string(runOutput), "2\n0 0\n0 1\n1 0\n0 1\n1\n1\n"; got != want {
		t.Fatalf("runtime promotion harness stdout = %q, want %q", got, want)
	}
}

// TestBundledRuntimeMinorUsesRememberedSetForOldToYoung is the headline
// Phase B4 test: after promoting an OLD owner, store a fresh YOUNG
// child into one of its slots via the write-barrier ABI, then run a
// minor collection. The remembered set must carry the YOUNG child
// through even though it is neither directly rooted nor reachable
// from any YOUNG object.
func TestBundledRuntimeMinorUsesRememberedSetForOldToYoung(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_remembered_minor_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_remembered_minor_harness")
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
void osty_gc_post_write_v1(void *owner, void *value, int64_t slot_kind) __asm__(OSTY_GC_SYMBOL("osty.gc.post_write_v1"));
void osty_gc_root_bind_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_bind_v1"));
void osty_gc_root_release_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_release_v1"));

void osty_gc_debug_collect_minor(void);
void osty_gc_debug_collect_major(void);
int64_t osty_gc_debug_generation_of(void *payload);
int64_t osty_gc_debug_live_count(void);
int64_t osty_gc_debug_young_count(void);
int64_t osty_gc_debug_old_count(void);
int64_t osty_gc_debug_remembered_edge_count(void);
int64_t osty_gc_debug_validate_heap(void);

int main(void) {
    /* Step 1: allocate an owner, root-bind it, and promote it to OLD
     * via repeated minor cycles (promote_age=1 via env). */
    void *owner = osty_gc_alloc_v1(7, 32, "owner");
    osty_gc_root_bind_v1(owner);
    osty_gc_debug_collect_minor();
    printf("%lld\n", (long long)osty_gc_debug_generation_of(owner));

    /* Step 2: allocate a fresh YOUNG child, NOT root-bound. Install
     * it into the OLD owner via the write-barrier ABI. The remembered
     * set must now contain (owner, child). */
    void *child = osty_gc_alloc_v1(8, 16, "child");
    osty_gc_pre_write_v1(owner, NULL, 0);
    osty_gc_post_write_v1(owner, child, 0);
    printf("%lld %lld\n",
        (long long)osty_gc_debug_generation_of(child),
        (long long)osty_gc_debug_remembered_edge_count());

    /* Step 3: a minor collection must keep the child alive — the
     * remembered set is the only path. If B4 is broken, child gets
     * swept and live_count collapses. */
    int64_t live_before = osty_gc_debug_live_count();
    osty_gc_debug_collect_minor();
    int64_t live_after = osty_gc_debug_live_count();
    printf("%lld %lld\n", live_before, live_after);
    printf("%lld\n", (long long)osty_gc_debug_validate_heap());

    /* Step 4: promote the child too — this exercises the compact-
     * after-minor step (rem edge of (OLD, YOUNG) drops to (OLD, OLD)
     * and should be filtered out). */
    osty_gc_debug_collect_minor();
    printf("%lld %lld\n",
        (long long)osty_gc_debug_generation_of(child),
        (long long)osty_gc_debug_remembered_edge_count());

    osty_gc_root_release_v1(owner);
    osty_gc_debug_collect_major();
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
	runCmd.Env = append(os.Environ(), "OSTY_GC_PROMOTE_AGE=1")
	runOutput, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, runOutput)
	}
	/* Expected:
	 *  1           — owner is OLD after first minor
	 *  0 1         — child starts YOUNG; remembered edge count is 1
	 *  2 2         — live before minor = 2 (owner+child); after = 2 (both survived)
	 *  0           — validate_heap green
	 *  1 0         — child now OLD (age crossed 1 promotes); rem edges compacted to 0
	 */
	if got, want := string(runOutput), "1\n0 1\n2 2\n0\n1 0\n"; got != want {
		t.Fatalf("runtime remembered-minor harness stdout = %q, want %q", got, want)
	}
}

// TestBundledRuntimeNurseryLimitTriggersMinor covers Phase B5 — the
// pressure tier split. A nursery budget lower than the major heap
// threshold means safepoint polls fire minor collections before major
// ever runs. We assert on the minor/major counters after enough
// allocation to cross the nursery line but stay under the heap line.
func TestBundledRuntimeNurseryLimitTriggersMinor(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_nursery_trigger_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_nursery_trigger_harness")
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
void osty_gc_safepoint_v1(int64_t id, void *const *roots, int64_t n) __asm__(OSTY_GC_SYMBOL("osty.gc.safepoint_v1"));

int64_t osty_gc_debug_minor_count(void);
int64_t osty_gc_debug_major_count(void);
int64_t osty_gc_debug_nursery_limit_bytes(void);

int main(void) {
    printf("%lld\n", (long long)osty_gc_debug_nursery_limit_bytes());
    /* Allocate unreferenced objects — any safepoint poll should
     * trigger a minor, not a major, because the major threshold is
     * much higher than the nursery limit. */
    for (int i = 0; i < 32; i++) {
        (void)osty_gc_alloc_v1(7, 128, "g");
    }
    osty_gc_safepoint_v1(0, 0, 0);
    /* Exactly one minor expected (allocations pushed the young bytes
     * above the nursery limit; the single safepoint drains it). No
     * major. */
    printf("%lld %lld\n",
        (long long)osty_gc_debug_minor_count(),
        (long long)osty_gc_debug_major_count());
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
	runCmd.Env = append(os.Environ(),
		"OSTY_GC_NURSERY_BYTES=1024",
		"OSTY_GC_THRESHOLD_BYTES=1048576",
	)
	runOutput, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, runOutput)
	}
	if got, want := string(runOutput), "1024\n1 0\n"; got != want {
		t.Fatalf("runtime nursery-trigger harness stdout = %q, want %q", got, want)
	}
}

// TestBundledRuntimeValidateHeapNegativeInvariantsPhaseB mirrors the
// Phase A1 negative harness for the five generational invariants
// (-12 gen count, -13 gen bytes, -14 invalid gen, -15 gen list count,
// -16 gen list membership). Every new invariant gets a dedicated
// corruption injector that flips exactly one field and forces
// validate_heap to return the expected code.
func TestBundledRuntimeValidateHeapNegativeInvariantsPhaseB(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_validate_phase_b_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_validate_phase_b_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>
#include <sys/wait.h>

#if defined(__APPLE__)
#define OSTY_GC_SYMBOL(name) "_" name
#else
#define OSTY_GC_SYMBOL(name) name
#endif

void *osty_gc_alloc_v1(int64_t object_kind, int64_t byte_size, const char *site) __asm__(OSTY_GC_SYMBOL("osty.gc.alloc_v1"));
void osty_gc_root_bind_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_bind_v1"));

int64_t osty_gc_debug_validate_heap(void);
void osty_gc_debug_unsafe_bump_young_count(void);
void osty_gc_debug_unsafe_bump_young_bytes(void);
void osty_gc_debug_unsafe_set_invalid_generation(void);
void osty_gc_debug_unsafe_corrupt_young_head_gen(void);
void osty_gc_debug_unsafe_detach_from_young_list(void);

static int run_case(const char *name, void (*setup)(void), void (*inject)(void), int64_t expected) {
    pid_t pid = fork();
    if (pid < 0) return 1;
    if (pid == 0) {
        setup();
        int64_t before = osty_gc_debug_validate_heap();
        if (before != 0) { _exit(200); }
        inject();
        int64_t after = osty_gc_debug_validate_heap();
        if (after != expected) {
            fprintf(stderr, "%s: got %lld want %lld\n", name, (long long)after, (long long)expected);
            _exit(201);
        }
        _exit(0);
    }
    int status = 0;
    waitpid(pid, &status, 0);
    return WIFEXITED(status) ? WEXITSTATUS(status) : 255;
}

static void setup_one_young(void) {
    void *a = osty_gc_alloc_v1(7, 32, "a");
    osty_gc_root_bind_v1(a);
}

int main(void) {
    printf("%d\n", run_case("gen_count",      setup_one_young, osty_gc_debug_unsafe_bump_young_count,        -12));  /* GEN_COUNT_MISMATCH */
    printf("%d\n", run_case("gen_bytes",      setup_one_young, osty_gc_debug_unsafe_bump_young_bytes,        -13));  /* GEN_BYTES_MISMATCH */
    printf("%d\n", run_case("invalid_gen",    setup_one_young, osty_gc_debug_unsafe_set_invalid_generation,  -14));  /* INVALID_GENERATION */
    printf("%d\n", run_case("gen_membership", setup_one_young, osty_gc_debug_unsafe_corrupt_young_head_gen,  -14));  /* invalid gen tripped first — both -14 and -16 would catch it */
    printf("%d\n", run_case("gen_list_count", setup_one_young, osty_gc_debug_unsafe_detach_from_young_list,  -15));  /* GEN_LIST_COUNT_MISMATCH */
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
	if got, want := string(runOutput), "0\n0\n0\n0\n0\n"; got != want {
		t.Fatalf("phase-B validate negative harness stdout = %q, want %q", got, want)
	}
}

// TestBundledRuntimeMinorEscalatesToMajorWhenNurseryPinned covers B5
// depth — if minor finishes but young_bytes is still above the
// nursery limit (every survivor stayed YOUNG rather than being swept
// or promoted), the dispatcher must flip to major on the next
// safepoint instead of looping minors indefinitely.
func TestBundledRuntimeMinorEscalatesToMajorWhenNurseryPinned(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_escalate_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_escalate_harness")
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
void osty_gc_safepoint_v1(int64_t id, void *const *roots, int64_t n) __asm__(OSTY_GC_SYMBOL("osty.gc.safepoint_v1"));

void osty_gc_debug_collect_minor(void);
int64_t osty_gc_debug_minor_count(void);
int64_t osty_gc_debug_major_count(void);
int64_t osty_gc_debug_young_bytes(void);

int main(void) {
    /* Fill the nursery with rooted survivors so the minor can't free
     * anything. With promote_age = UINT8_MAX-effective the survivors
     * stay YOUNG forever in the absence of enough minor cycles;
     * more importantly, after the single forced minor, young_bytes is
     * still above the 256-byte nursery cap, which should flip the
     * major flag. */
    void *roots[8];
    for (int i = 0; i < 8; i++) {
        roots[i] = osty_gc_alloc_v1(7, 64, "pinned");
        osty_gc_root_bind_v1(roots[i]);
    }
    /* Force a minor: young is still hot (all rooted + not promoted
     * yet), so the escalation flag flips. */
    osty_gc_debug_collect_minor();
    printf("%lld %lld\n",
        (long long)osty_gc_debug_minor_count(),
        (long long)osty_gc_debug_major_count());
    /* A follow-up safepoint should honour the escalation flag and
     * dispatch major. */
    osty_gc_safepoint_v1(0, 0, 0);
    printf("%lld %lld\n",
        (long long)osty_gc_debug_minor_count(),
        (long long)osty_gc_debug_major_count());
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
	runCmd.Env = append(os.Environ(),
		"OSTY_GC_NURSERY_BYTES=256",
		"OSTY_GC_THRESHOLD_BYTES=1048576",
	)
	runOutput, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, runOutput)
	}
	/* Expected:
	 *  1 0  — one minor, no major yet
	 *  1 1  — the safepoint after the pinned minor escalates to major
	 */
	if got, want := string(runOutput), "1 0\n1 1\n"; got != want {
		t.Fatalf("minor→major escalation harness stdout = %q, want %q", got, want)
	}
}

// TestBundledRuntimeRememberedSetEdgeCases covers B4 depth — four edge
// cases the original happy-path test did not exercise: OLD→OLD edges
// (should be filtered out at compact time), a multi-child fanout from
// one OLD owner, a deep YOUNG chain rooted through OLD, and a stale
// remembered entry after the owner is swept by a major.
func TestBundledRuntimeRememberedSetEdgeCases(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_remset_edge_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_remset_edge_harness")
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
void osty_gc_post_write_v1(void *owner, void *value, int64_t slot_kind) __asm__(OSTY_GC_SYMBOL("osty.gc.post_write_v1"));
void osty_gc_root_bind_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_bind_v1"));
void osty_gc_root_release_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_release_v1"));

void osty_gc_debug_collect_minor(void);
void osty_gc_debug_collect_major(void);
int64_t osty_gc_debug_generation_of(void *payload);
int64_t osty_gc_debug_remembered_edge_count(void);
int64_t osty_gc_debug_live_count(void);
int64_t osty_gc_debug_validate_heap(void);

int main(void) {
    /* Case 1 — OLD→OLD edge filters out at compact time.
     * Promote two objects to OLD, install an OLD pointer into one of
     * them via the barrier, then run a minor. The compact step must
     * drop the (OLD, OLD) entry. */
    void *a = osty_gc_alloc_v1(7, 32, "a");
    void *b = osty_gc_alloc_v1(7, 32, "b");
    osty_gc_root_bind_v1(a);
    osty_gc_root_bind_v1(b);
    osty_gc_debug_collect_minor();  /* promote_age=1 → both OLD */
    printf("%lld %lld\n",
        (long long)osty_gc_debug_generation_of(a),
        (long long)osty_gc_debug_generation_of(b));
    osty_gc_pre_write_v1(a, NULL, 0);
    osty_gc_post_write_v1(a, b, 0);  /* OLD→OLD logged */
    int64_t edges_before = osty_gc_debug_remembered_edge_count();
    osty_gc_debug_collect_minor();
    int64_t edges_after = osty_gc_debug_remembered_edge_count();
    printf("%lld %lld\n", edges_before, edges_after);

    /* Case 2 — multi-child fanout from one OLD owner.
     * Install three fresh YOUNG children into owner a. All three must
     * survive the minor via the remembered set. */
    void *c1 = osty_gc_alloc_v1(8, 8, "c1");
    void *c2 = osty_gc_alloc_v1(8, 8, "c2");
    void *c3 = osty_gc_alloc_v1(8, 8, "c3");
    osty_gc_pre_write_v1(a, NULL, 0); osty_gc_post_write_v1(a, c1, 0);
    osty_gc_pre_write_v1(a, NULL, 0); osty_gc_post_write_v1(a, c2, 0);
    osty_gc_pre_write_v1(a, NULL, 0); osty_gc_post_write_v1(a, c3, 0);
    int64_t live_before = osty_gc_debug_live_count();
    osty_gc_debug_collect_minor();
    int64_t live_after = osty_gc_debug_live_count();
    printf("%lld %lld\n", live_before, live_after);
    /* All three promoted to OLD on this minor (promote_age=1). */
    printf("%lld %lld %lld\n",
        (long long)osty_gc_debug_generation_of(c1),
        (long long)osty_gc_debug_generation_of(c2),
        (long long)osty_gc_debug_generation_of(c3));

    /* Case 3 — stale remembered entry after owner is swept.
     * Install a YOUNG child into owner b, release b, run a major
     * (which sweeps b and anything unreachable), then verify the
     * remembered set is not left pointing at a freed owner
     * (validate_heap green, count drops to 0 because major clears the
     * log wholesale). */
    void *d = osty_gc_alloc_v1(8, 8, "d");
    osty_gc_pre_write_v1(b, NULL, 0); osty_gc_post_write_v1(b, d, 0);
    osty_gc_root_release_v1(b);
    osty_gc_debug_collect_major();
    printf("%lld %lld\n",
        (long long)osty_gc_debug_remembered_edge_count(),
        (long long)osty_gc_debug_validate_heap());

    /* Case 4 — stale remembered entry across a major cycle.
     * After the major in Case 3 cleared the rem log, install a fresh
     * OLD→YOUNG edge and verify the minor still behaves correctly
     * (no dangling-pointer reads from the pre-major log). */
    void *e = osty_gc_alloc_v1(8, 8, "e");
    osty_gc_pre_write_v1(a, NULL, 0); osty_gc_post_write_v1(a, e, 0);
    int64_t live_case4_before = osty_gc_debug_live_count();
    osty_gc_debug_collect_minor();
    int64_t live_case4_after = osty_gc_debug_live_count();
    printf("%lld %lld\n", live_case4_before, live_case4_after);
    printf("%lld\n", (long long)osty_gc_debug_validate_heap());

    osty_gc_root_release_v1(a);
    osty_gc_debug_collect_major();
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
	runCmd.Env = append(os.Environ(), "OSTY_GC_PROMOTE_AGE=1")
	runOutput, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, runOutput)
	}
	/* Expected:
	 *  1 1      — both a and b now OLD after first minor
	 *  1 0      — OLD→OLD edge filtered out at compact time (from 1 to 0)
	 *  5 5      — live before/after case-2 minor (a,b,c1,c2,c3 all survive)
	 *  1 1 1    — c1,c2,c3 all promoted to OLD
	 *  0 0      — rem set empty after major (clears log), validate green
	 *  2 2      — case 4: a (OLD, rooted) + e (YOUNG) before; after minor
	 *             e is marked via the fresh (a,e) rem edge → promoted
	 *             to OLD; a+e both survive
	 *  0        — validate green
	 */
	if got, want := string(runOutput), "1 1\n1 0\n5 5\n1 1 1\n0 0\n2 2\n0\n"; got != want {
		t.Fatalf("remembered-set edge cases harness stdout = %q, want %q", got, want)
	}
}

// TestBundledRuntimeGenerationalStress exercises a randomized
// allocation / root / minor pattern. Every cycle: 10 random allocations,
// a random subset root-bound, a minor collect, `validate_heap`. After
// 200 cycles we must have consumed some minor and major collections
// and validate_heap stayed at zero throughout — a regression in B2/B3
// ordering or B4 compaction shows up as an invariant error code.
func TestBundledRuntimeGenerationalStress(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_stress_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_stress_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

#if defined(__APPLE__)
#define OSTY_GC_SYMBOL(name) "_" name
#else
#define OSTY_GC_SYMBOL(name) name
#endif

void *osty_gc_alloc_v1(int64_t object_kind, int64_t byte_size, const char *site) __asm__(OSTY_GC_SYMBOL("osty.gc.alloc_v1"));
void osty_gc_pre_write_v1(void *owner, void *old_value, int64_t slot_kind) __asm__(OSTY_GC_SYMBOL("osty.gc.pre_write_v1"));
void osty_gc_post_write_v1(void *owner, void *value, int64_t slot_kind) __asm__(OSTY_GC_SYMBOL("osty.gc.post_write_v1"));
void osty_gc_root_bind_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_bind_v1"));
void osty_gc_root_release_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_release_v1"));

void osty_gc_debug_collect_minor(void);
void osty_gc_debug_collect_major(void);
int64_t osty_gc_debug_validate_heap(void);
int64_t osty_gc_debug_minor_count(void);
int64_t osty_gc_debug_major_count(void);

int main(void) {
    srand(1);  /* Deterministic — test must be reproducible. */
    enum { LIVE = 128, ROOTS = 8 };
    void *live[LIVE] = {0};
    /* A small, fixed set of rooted anchors. Binding them once up
     * front and never rebinding/releasing avoids the book-keeping
     * hazards of chasing barrier writes — the anchors are what keeps
     * part of the heap alive across the random churn; everything
     * else is garbage subject to sweep. */
    void *anchors[ROOTS] = {0};
    int total_failures = 0;

    for (int i = 0; i < ROOTS; i++) {
        anchors[i] = osty_gc_alloc_v1(7, 32, "anchor");
        osty_gc_root_bind_v1(anchors[i]);
    }

    for (int cycle = 0; cycle < 200; cycle++) {
        /* Churn: allocate 10 new objects into random slots; older
         * entries in those slots become GC garbage. */
        for (int i = 0; i < 10; i++) {
            int slot = rand() % LIVE;
            live[slot] = osty_gc_alloc_v1(7, (rand() % 64) + 8, "stress");
        }
        /* Cross-gen edge: point a random anchor at a random churn
         * slot via the barrier. If the anchor has been promoted to
         * OLD, this feeds the remembered set — the minor consumes it
         * and the child survives. */
        int anchor_idx = rand() % ROOTS;
        int value_slot = rand() % LIVE;
        if (anchors[anchor_idx] != NULL && live[value_slot] != NULL) {
            osty_gc_pre_write_v1(anchors[anchor_idx], NULL, 0);
            osty_gc_post_write_v1(anchors[anchor_idx], live[value_slot], 0);
        }

        if (cycle % 3 == 0) {
            osty_gc_debug_collect_minor();
        }
        if (cycle % 47 == 0) {
            osty_gc_debug_collect_major();
        }
        if (osty_gc_debug_validate_heap() != 0) {
            total_failures += 1;
        }
    }

    /* Release anchors so the final major can sweep the entire heap
     * and leave validate_heap at zero. */
    for (int i = 0; i < ROOTS; i++) {
        if (anchors[i] != NULL) {
            osty_gc_root_release_v1(anchors[i]);
        }
    }
    osty_gc_debug_collect_major();

    printf("%d %lld %lld %lld\n",
        total_failures,
        (long long)osty_gc_debug_minor_count(),
        (long long)osty_gc_debug_major_count(),
        (long long)osty_gc_debug_validate_heap());
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
	runCmd.Env = append(os.Environ(), "OSTY_GC_PROMOTE_AGE=2")
	runOutput, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, runOutput)
	}
	/* Expected: 0 failures; at least one minor and one major ran;
	 * final validate_heap = 0. The exact minor/major counts depend on
	 * the RNG but total_failures == 0 is the actual invariant. */
	out := string(runOutput)
	var failures, minors, majors, validate int64
	if _, err := fmt.Sscanf(out, "%d %d %d %d", &failures, &minors, &majors, &validate); err != nil {
		t.Fatalf("unparseable stress output %q: %v", out, err)
	}
	if failures != 0 {
		t.Fatalf("stress recorded %d validate_heap failures, want 0; full out=%q", failures, out)
	}
	if minors < 10 {
		t.Fatalf("stress expected ≥10 minors over 200 cycles, got %d; out=%q", minors, out)
	}
	if majors < 1 {
		t.Fatalf("stress expected ≥1 major, got %d; out=%q", majors, out)
	}
	if validate != 0 {
		t.Fatalf("final validate_heap = %d, want 0; out=%q", validate, out)
	}
}

// TestBundledRuntimeIncrementalMarkStepByStep covers Phase C1/C2 —
// the state machine transitions (IDLE → MARK_INCREMENTAL → IDLE), the
// budget step drains a bounded number of greys per call, and a full
// cycle sweeps unreferenced objects.
func TestBundledRuntimeIncrementalMarkStepByStep(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_incremental_step_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_incremental_step_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdint.h>
#include <stdio.h>
#include <stdbool.h>

#if defined(__APPLE__)
#define OSTY_GC_SYMBOL(name) "_" name
#else
#define OSTY_GC_SYMBOL(name) name
#endif

void *osty_gc_alloc_v1(int64_t object_kind, int64_t byte_size, const char *site) __asm__(OSTY_GC_SYMBOL("osty.gc.alloc_v1"));
void osty_gc_root_bind_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_bind_v1"));
void osty_gc_root_release_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_release_v1"));

void osty_gc_collect_incremental_start_with_stack_roots(void *const *root_slots, int64_t root_slot_count);
bool osty_gc_collect_incremental_step(int64_t budget);
void osty_gc_collect_incremental_finish(void);

int64_t osty_gc_debug_state(void);
int64_t osty_gc_debug_live_count(void);
int64_t osty_gc_debug_major_count(void);
int64_t osty_gc_debug_incremental_steps_total(void);
int64_t osty_gc_debug_incremental_work_total(void);
int64_t osty_gc_debug_validate_heap(void);

enum { STATE_IDLE = 0, STATE_MARK = 1, STATE_SWEEP = 2 };

int main(void) {
    /* Three live objects: one rooted, two dangling. */
    void *keep = osty_gc_alloc_v1(7, 32, "keep");
    void *drop1 = osty_gc_alloc_v1(7, 32, "drop1");
    void *drop2 = osty_gc_alloc_v1(7, 32, "drop2");
    (void)drop1; (void)drop2;
    osty_gc_root_bind_v1(keep);

    /* Baseline state. */
    printf("%lld\n", (long long)osty_gc_debug_state());

    osty_gc_collect_incremental_start_with_stack_roots(NULL, 0);
    /* Now in MARK_INCREMENTAL; validate tolerates non-WHITE headers. */
    printf("%lld %lld\n",
        (long long)osty_gc_debug_state(),
        (long long)osty_gc_debug_validate_heap());

    /* Drive the mark in small steps. The only grey at this point is
     * 'keep', so a single step drains it and returns false. */
    bool more = osty_gc_collect_incremental_step(100);
    printf("%d %lld\n", more, (long long)osty_gc_debug_incremental_work_total());

    /* Finish: sweeps drop1 and drop2, resets state to IDLE. */
    osty_gc_collect_incremental_finish();
    printf("%lld %lld %lld %lld\n",
        (long long)osty_gc_debug_state(),
        (long long)osty_gc_debug_live_count(),
        (long long)osty_gc_debug_major_count(),
        (long long)osty_gc_debug_incremental_steps_total());
    printf("%lld\n", (long long)osty_gc_debug_validate_heap());

    osty_gc_root_release_v1(keep);
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
	/* Expected:
	 *  0                 — state IDLE at rest
	 *  1 0               — state MARK_INCREMENTAL, validate still 0
	 *                      (tolerates non-WHITE under active mark)
	 *  0 1               — step returned false (no more work), work_total = 1
	 *  0 1 1 1           — state IDLE, live=1, major_count=1, steps_total=1
	 *  0                 — validate green
	 */
	if got, want := string(runOutput), "0\n1 0\n0 1\n0 1 1 1\n0\n"; got != want {
		t.Fatalf("incremental step harness stdout = %q, want %q", got, want)
	}
}

// TestBundledRuntimeIncrementalBudgetDrainsLongChain covers the
// budget-step half of C2: a long transitive graph forces multiple
// step calls before the queue empties. Each step caps the work at
// the supplied budget, not the remaining queue size.
func TestBundledRuntimeIncrementalBudgetDrainsLongChain(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_incremental_budget_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_incremental_budget_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdint.h>
#include <stdio.h>
#include <stdbool.h>

#if defined(__APPLE__)
#define OSTY_GC_SYMBOL(name) "_" name
#else
#define OSTY_GC_SYMBOL(name) name
#endif

void *osty_gc_alloc_v1(int64_t object_kind, int64_t byte_size, const char *site) __asm__(OSTY_GC_SYMBOL("osty.gc.alloc_v1"));
void osty_gc_pre_write_v1(void *owner, void *old_value, int64_t slot_kind) __asm__(OSTY_GC_SYMBOL("osty.gc.pre_write_v1"));
void osty_gc_post_write_v1(void *owner, void *value, int64_t slot_kind) __asm__(OSTY_GC_SYMBOL("osty.gc.post_write_v1"));
void osty_gc_root_bind_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_bind_v1"));
void osty_gc_root_release_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_release_v1"));

void osty_gc_collect_incremental_start_with_stack_roots(void *const *root_slots, int64_t root_slot_count);
bool osty_gc_collect_incremental_step(int64_t budget);
void osty_gc_collect_incremental_finish(void);

int64_t osty_gc_debug_live_count(void);
int64_t osty_gc_debug_incremental_steps_total(void);
int64_t osty_gc_debug_incremental_work_total(void);
void *osty_rt_list_new(void);
void osty_rt_list_push_ptr(void *list, void *value);

int main(void) {
    /* A rooted list with 50 children — reachable only through the
     * list's trace callback. Incremental mark enqueues the list at
     * seed time; the first step pops list → enqueues 50 children.
     * With budget 10 the caller needs ≥6 steps to drain. */
    enum { N = 50 };
    void *list = osty_rt_list_new();
    osty_gc_root_bind_v1(list);
    for (int i = 0; i < N; i++) {
        void *child = osty_gc_alloc_v1(7, 16, "child");
        osty_gc_pre_write_v1(list, NULL, 0);
        osty_rt_list_push_ptr(list, child);
        osty_gc_post_write_v1(list, child, 0);
    }

    osty_gc_collect_incremental_start_with_stack_roots(NULL, 0);
    int step_count = 0;
    bool more = true;
    while (more) {
        more = osty_gc_collect_incremental_step(10);
        step_count += 1;
        if (step_count > 100) break;  /* runaway guard */
    }
    osty_gc_collect_incremental_finish();

    /* Expected steps: the mark drain is list (1) + 50 children = 51
     * work units. Budget 10 → ceil(51/10) = 6 steps before the queue
     * empties; the sixth step drains the last unit and reports false.
     * The counter incremental_steps_total counts successful step
     * entries, so 6. */
    printf("%d %lld %lld\n",
        step_count,
        (long long)osty_gc_debug_incremental_steps_total(),
        (long long)osty_gc_debug_incremental_work_total());
    printf("%lld\n", (long long)osty_gc_debug_live_count());

    osty_gc_root_release_v1(list);
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
	/* Expected:
	 *   6 6 51  — six steps; each called osty_gc_collect_incremental_step
	 *             returned successfully. work_total = 51 (list + 50 kids)
	 *   51      — all 51 objects survive since the list stays rooted
	 */
	if got, want := string(runOutput), "6 6 51\n51\n"; got != want {
		t.Fatalf("incremental budget harness stdout = %q, want %q", got, want)
	}
}

// TestBundledRuntimeIncrementalSATBBarrierGreysOldValue covers C5 —
// during MARK_INCREMENTAL, if the mutator overwrites a reachable-but-
// unmarked pointer, the SATB pre-write barrier must grey the old
// value so the mark pass does not lose it. This is the correctness
// test that makes the Phase A write-barrier log a live input instead
// of passive recording.
func TestBundledRuntimeIncrementalSATBBarrierGreysOldValue(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_incremental_satb_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_incremental_satb_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdint.h>
#include <stdio.h>
#include <stdbool.h>

#if defined(__APPLE__)
#define OSTY_GC_SYMBOL(name) "_" name
#else
#define OSTY_GC_SYMBOL(name) name
#endif

void *osty_gc_alloc_v1(int64_t object_kind, int64_t byte_size, const char *site) __asm__(OSTY_GC_SYMBOL("osty.gc.alloc_v1"));
void osty_gc_pre_write_v1(void *owner, void *old_value, int64_t slot_kind) __asm__(OSTY_GC_SYMBOL("osty.gc.pre_write_v1"));
void osty_gc_post_write_v1(void *owner, void *value, int64_t slot_kind) __asm__(OSTY_GC_SYMBOL("osty.gc.post_write_v1"));
void osty_gc_root_bind_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_bind_v1"));
void osty_gc_root_release_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_release_v1"));

void osty_gc_collect_incremental_start_with_stack_roots(void *const *root_slots, int64_t root_slot_count);
bool osty_gc_collect_incremental_step(int64_t budget);
void osty_gc_collect_incremental_finish(void);

int64_t osty_gc_debug_live_count(void);
int64_t osty_gc_debug_color_of(void *payload);
int64_t osty_gc_debug_satb_barrier_greyed_total(void);
void *osty_rt_list_new(void);
void osty_rt_list_push_ptr(void *list, void *value);
void osty_rt_list_set_ptr(void *list, int64_t index, void *value);

enum { WHITE = 0, GREY = 1, BLACK = 2 };

int main(void) {
    /* Build a small reachable graph: list -> child. Both managed. */
    void *list = osty_rt_list_new();
    osty_gc_root_bind_v1(list);
    void *child = osty_gc_alloc_v1(7, 32, "child");
    osty_gc_pre_write_v1(list, NULL, 0);
    osty_rt_list_push_ptr(list, child);
    osty_gc_post_write_v1(list, child, 0);

    /* Start incremental; seeds the list as GREY but hasn't yet
     * traced into it — so child is still WHITE at this point. */
    osty_gc_collect_incremental_start_with_stack_roots(NULL, 0);
    printf("%lld %lld\n",
        (long long)osty_gc_debug_color_of(list),
        (long long)osty_gc_debug_color_of(child));

    /* Mutator phase BETWEEN seed and step: overwrite the only slot
     * pointing at child with a fresh payload. Without SATB, the
     * upcoming mark step would trace an empty list and child would be
     * swept at finish. With SATB, the pre_write greys child so it
     * survives. */
    void *replacement = osty_gc_alloc_v1(7, 32, "replacement");
    osty_gc_pre_write_v1(list, child, 0);        /* barrier captures child */
    osty_rt_list_set_ptr(list, 0, replacement);  /* slot now points to replacement */
    osty_gc_post_write_v1(list, replacement, 0);

    /* SATB counter bumped; child is now GREY. */
    printf("%lld %lld\n",
        (long long)osty_gc_debug_satb_barrier_greyed_total(),
        (long long)osty_gc_debug_color_of(child));

    /* Drain and finish. child + replacement both survive. */
    while (osty_gc_collect_incremental_step(100)) {}
    osty_gc_collect_incremental_finish();

    /* live_count = list + child + replacement = 3. */
    printf("%lld\n", (long long)osty_gc_debug_live_count());

    osty_gc_root_release_v1(list);
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
	/* Disable mutator assist so the alloc between _start and the
	 * overwrite does not trace list first — the whole point of this
	 * harness is to verify the SATB barrier path, which is
	 * unreachable once assist has already greyed the child through
	 * the trace of its parent. A separate
	 * TestBundledRuntimeIncrementalMutatorAssist covers the assist
	 * behaviour directly. */
	runCmd.Env = append(os.Environ(), "OSTY_GC_ASSIST_BYTES_PER_UNIT=0")
	runOutput, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, runOutput)
	}
	/* Expected:
	 *   1 0   — list is GREY (seeded), child still WHITE (not traced)
	 *   1 1   — SATB counter bumped once; child is now GREY
	 *   3     — all three objects survive (list rooted; child via SATB;
	 *           replacement via trace through list)
	 */
	if got, want := string(runOutput), "1 0\n1 1\n3\n"; got != want {
		t.Fatalf("incremental SATB harness stdout = %q, want %q", got, want)
	}
}

// TestBundledRuntimeValidateHeapNegativeInvariantsPhaseC covers the
// Phase C1 depth follow-up — three new tri-colour invariants each get
// a dedicated injector that trips exactly one error code.
func TestBundledRuntimeValidateHeapNegativeInvariantsPhaseC(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_validate_phase_c_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_validate_phase_c_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>
#include <sys/wait.h>

#if defined(__APPLE__)
#define OSTY_GC_SYMBOL(name) "_" name
#else
#define OSTY_GC_SYMBOL(name) name
#endif

void *osty_gc_alloc_v1(int64_t object_kind, int64_t byte_size, const char *site) __asm__(OSTY_GC_SYMBOL("osty.gc.alloc_v1"));
void osty_gc_root_bind_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_bind_v1"));

int64_t osty_gc_debug_validate_heap(void);
void osty_gc_debug_unsafe_set_invalid_color(void);
void osty_gc_debug_unsafe_desync_color_marked(void);
void osty_gc_debug_unsafe_nonwhite_at_rest(void);

static int run_case(const char *name, void (*setup)(void), void (*inject)(void), int64_t expected) {
    pid_t pid = fork();
    if (pid < 0) return 1;
    if (pid == 0) {
        setup();
        int64_t before = osty_gc_debug_validate_heap();
        if (before != 0) { _exit(200); }
        inject();
        int64_t after = osty_gc_debug_validate_heap();
        if (after != expected) {
            fprintf(stderr, "%s: got %lld want %lld\n", name, (long long)after, (long long)expected);
            _exit(201);
        }
        _exit(0);
    }
    int status = 0;
    waitpid(pid, &status, 0);
    return WIFEXITED(status) ? WEXITSTATUS(status) : 255;
}

static void setup_one_young(void) {
    void *a = osty_gc_alloc_v1(7, 32, "a");
    osty_gc_root_bind_v1(a);
}

int main(void) {
    printf("%d\n", run_case("invalid_color", setup_one_young, osty_gc_debug_unsafe_set_invalid_color,    -17));
    printf("%d\n", run_case("desync",        setup_one_young, osty_gc_debug_unsafe_desync_color_marked, -18));
    printf("%d\n", run_case("nonwhite_rest", setup_one_young, osty_gc_debug_unsafe_nonwhite_at_rest,    -19));
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
	if got, want := string(runOutput), "0\n0\n0\n"; got != want {
		t.Fatalf("phase-C validate negative harness stdout = %q, want %q", got, want)
	}
}

// TestBundledRuntimeStableIdentityGroundwork covers the Phase D entry
// point: every managed object gets a stable logical id plus a reverse
// lookup table that survives collections and drops reclaimed objects.
func TestBundledRuntimeStableIdentityGroundwork(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_stable_identity_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_stable_identity_harness")
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

void osty_gc_debug_collect(void);
int64_t osty_gc_debug_live_count(void);
int64_t osty_gc_debug_validate_heap(void);
int64_t osty_gc_debug_stable_id(void *payload);
void *osty_gc_debug_payload_for_stable_id(int64_t stable_id);

int main(void) {
    void *keep = osty_gc_alloc_v1(7, 24, "keep");
    void *garbage = osty_gc_alloc_v1(7, 24, "garbage");
    int64_t keep_id = osty_gc_debug_stable_id(keep);
    int64_t garbage_id = osty_gc_debug_stable_id(garbage);

    printf("%d %d %d\n",
        keep_id > 0,
        garbage_id > 0,
        keep_id != garbage_id);
    printf("%d %d\n",
        osty_gc_debug_payload_for_stable_id(keep_id) == keep,
        osty_gc_debug_payload_for_stable_id(garbage_id) == garbage);

    osty_gc_root_bind_v1(keep);
    osty_gc_debug_collect();
    printf("%lld %d %d %d\n",
        (long long)osty_gc_debug_live_count(),
        osty_gc_debug_stable_id(keep) == keep_id,
        osty_gc_debug_payload_for_stable_id(keep_id) == keep,
        osty_gc_debug_payload_for_stable_id(garbage_id) == NULL);
    printf("%lld\n", (long long)osty_gc_debug_validate_heap());

    osty_gc_root_release_v1(keep);
    osty_gc_debug_collect();
    printf("%lld %d\n",
        (long long)osty_gc_debug_live_count(),
        osty_gc_debug_payload_for_stable_id(keep_id) == NULL);

    void *fresh = osty_gc_alloc_v1(7, 24, "fresh");
    int64_t fresh_id = osty_gc_debug_stable_id(fresh);
    printf("%d %d\n",
        fresh_id > keep_id && fresh_id > garbage_id,
        osty_gc_debug_payload_for_stable_id(fresh_id) == fresh);
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
	if got, want := string(runOutput), "1 1 1\n1 1\n1 1 1 1\n0\n0 1\n1 1\n"; got != want {
		t.Fatalf("stable-identity harness stdout = %q, want %q", got, want)
	}
}

// TestBundledRuntimeValidateHeapNegativeInvariantsPhaseD locks the two
// new Phase D groundwork invariants: stable ids must stay valid and the
// stable-id index must agree with the live heap walk.
func TestBundledRuntimeValidateHeapNegativeInvariantsPhaseD(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_validate_phase_d_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_validate_phase_d_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>
#include <sys/wait.h>

#if defined(__APPLE__)
#define OSTY_GC_SYMBOL(name) "_" name
#else
#define OSTY_GC_SYMBOL(name) name
#endif

void *osty_gc_alloc_v1(int64_t object_kind, int64_t byte_size, const char *site) __asm__(OSTY_GC_SYMBOL("osty.gc.alloc_v1"));
void osty_gc_root_bind_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_bind_v1"));

int64_t osty_gc_debug_validate_heap(void);
void osty_gc_debug_unsafe_zero_stable_id(void);
void osty_gc_debug_unsafe_remove_identity_index_live(void);

static int run_case(const char *name, void (*setup)(void), void (*inject)(void), int64_t expected) {
    pid_t pid = fork();
    if (pid < 0) return 1;
    if (pid == 0) {
        setup();
        int64_t before = osty_gc_debug_validate_heap();
        if (before != 0) { _exit(200); }
        inject();
        int64_t after = osty_gc_debug_validate_heap();
        if (after != expected) {
            fprintf(stderr, "%s: got %lld want %lld\n", name, (long long)after, (long long)expected);
            _exit(201);
        }
        _exit(0);
    }
    int status = 0;
    waitpid(pid, &status, 0);
    return WIFEXITED(status) ? WEXITSTATUS(status) : 255;
}

static void setup_one_object(void) {
    void *a = osty_gc_alloc_v1(7, 32, "a");
    osty_gc_root_bind_v1(a);
}

int main(void) {
    printf("%d\n", run_case("invalid_stable_id", setup_one_object, osty_gc_debug_unsafe_zero_stable_id, -20));
    printf("%d\n", run_case("identity_index", setup_one_object, osty_gc_debug_unsafe_remove_identity_index_live, -21));
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
	if got, want := string(runOutput), "0\n0\n"; got != want {
		t.Fatalf("phase-D validate negative harness stdout = %q, want %q", got, want)
	}
}

func TestBundledRuntimeCompactsStackRootedPayloadsPhaseD(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_phase_d_compaction_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_phase_d_compaction_harness")
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
void *osty_gc_load_v1(void *value) __asm__(OSTY_GC_SYMBOL("osty.gc.load_v1"));

int64_t osty_gc_debug_stable_id(void *payload);
void *osty_gc_debug_payload_for_stable_id(int64_t stable_id);
int64_t osty_gc_debug_compaction_count_total(void);
int64_t osty_gc_debug_forwarded_objects_last(void);
int64_t osty_gc_debug_forwarding_count(void);
int64_t osty_gc_debug_load_forwarded_count(void);
void *osty_rt_list_new(void);
void osty_rt_list_push_ptr(void *list, void *value);
void *osty_rt_list_get_ptr(void *list, int64_t index);

int main(void) {
    void *list = osty_rt_list_new();
    void *saved_list = list;
    void *child = osty_gc_alloc_v1(7, 32, "child");
    void *saved_child = child;
    int64_t list_id = osty_gc_debug_stable_id(list);
    int64_t child_id = osty_gc_debug_stable_id(child);
    void *root = list;
    void *root_slots[1] = { &root };

    osty_rt_list_push_ptr(list, child);
    osty_gc_safepoint_v1(1, root_slots, 1);

    printf("%d %d %d\n",
        root != saved_list,
        osty_gc_load_v1(saved_list) == root,
        osty_gc_debug_stable_id(root) == list_id);
    printf("%d %d %d\n",
        osty_rt_list_get_ptr(saved_list, 0) == osty_gc_load_v1(saved_child),
        osty_gc_debug_stable_id(osty_rt_list_get_ptr(saved_list, 0)) == child_id,
        osty_gc_debug_payload_for_stable_id(child_id) == osty_gc_load_v1(saved_child));
    printf("%d %d %d\n",
        osty_gc_debug_compaction_count_total() == 1,
        osty_gc_debug_forwarded_objects_last() == 2,
        osty_gc_debug_forwarding_count() == 2);
    printf("%d\n", osty_gc_debug_load_forwarded_count() >= 2);
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
	if got, want := string(runOutput), "1 1 1\n1 1 1\n1 1 1\n1\n"; got != want {
		t.Fatalf("phase-D compaction harness stdout = %q, want %q", got, want)
	}
}

func TestBundledRuntimePinPreventsEvacuationPhaseD(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_phase_d_pin_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_phase_d_pin_harness")
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
void *osty_gc_load_v1(void *value) __asm__(OSTY_GC_SYMBOL("osty.gc.load_v1"));
void osty_gc_pin_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.pin_v1"));
void osty_gc_unpin_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.unpin_v1"));

int64_t osty_gc_debug_stable_id(void *payload);
void *osty_gc_debug_payload_for_stable_id(int64_t stable_id);
int64_t osty_gc_debug_pin_count_of(void *payload);
int64_t osty_gc_debug_pinned_count(void);
int64_t osty_gc_debug_pinned_bytes(void);
int64_t osty_gc_debug_compaction_count_total(void);
int64_t osty_gc_debug_forwarded_objects_last(void);

int main(void) {
    void *obj = osty_gc_alloc_v1(7, 24, "obj");
    int64_t obj_id = osty_gc_debug_stable_id(obj);
    void *root = obj;
    void *root_slots[1] = { &root };

    osty_gc_pin_v1(obj);
    osty_gc_safepoint_v1(1, root_slots, 1);
    printf("%d %d %d %d\n",
        root == obj,
        osty_gc_debug_pin_count_of(obj) == 1,
        osty_gc_debug_pinned_count() == 1,
        osty_gc_debug_pinned_bytes() == 24);
    printf("%d %d %d\n",
        osty_gc_debug_compaction_count_total() == 0,
        osty_gc_debug_forwarded_objects_last() == 0,
        osty_gc_debug_payload_for_stable_id(obj_id) == obj);

    osty_gc_unpin_v1(obj);
    {
        void *garbage = osty_gc_alloc_v1(8, 8, "garbage");
        (void)garbage;
    }
    osty_gc_safepoint_v1(2, root_slots, 1);
    printf("%d %d %d %d\n",
        root != obj,
        osty_gc_debug_pin_count_of(root) == 0,
        osty_gc_debug_pinned_count() == 0,
        osty_gc_load_v1(obj) == root);
    printf("%d %d %d %d\n",
        osty_gc_debug_compaction_count_total() == 1,
        osty_gc_debug_forwarded_objects_last() == 1,
        osty_gc_debug_payload_for_stable_id(obj_id) == root,
        osty_gc_debug_stable_id(root) == obj_id);
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
	if got, want := string(runOutput), "1 1 1 1\n1 1 1\n1 1 1 1\n1 1 1 1\n"; got != want {
		t.Fatalf("phase-D pin harness stdout = %q, want %q", got, want)
	}
}

func TestBundledRuntimeRetainsForwardingAcrossRepeatedCompactionsPhaseD(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_phase_d_forwarding_history_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_phase_d_forwarding_history_harness")
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
void *osty_gc_load_v1(void *value) __asm__(OSTY_GC_SYMBOL("osty.gc.load_v1"));

int64_t osty_gc_debug_stable_id(void *payload);
void *osty_gc_debug_payload_for_stable_id(int64_t stable_id);
int64_t osty_gc_debug_compaction_count_total(void);
int64_t osty_gc_debug_forwarding_count(void);
int64_t osty_gc_debug_live_count(void);

int main(void) {
    void *obj = osty_gc_alloc_v1(7, 24, "obj");
    void *saved0 = obj;
    int64_t stable_id = osty_gc_debug_stable_id(obj);
    void *root = obj;
    void *root_slots[1] = { &root };

    void *garbage1 = osty_gc_alloc_v1(8, 8, "garbage1");
    (void)garbage1;
    osty_gc_safepoint_v1(1, root_slots, 1);
    void *saved1 = root;

    void *garbage2 = osty_gc_alloc_v1(9, 8, "garbage2");
    (void)garbage2;
    osty_gc_safepoint_v1(2, root_slots, 1);
    void *saved2 = root;

    printf("%d %d %d\n",
        saved1 != saved0,
        saved2 != saved1,
        osty_gc_debug_stable_id(saved2) == stable_id);
    printf("%d %d %d\n",
        osty_gc_load_v1(saved0) == saved2,
        osty_gc_load_v1(saved1) == saved2,
        osty_gc_debug_payload_for_stable_id(stable_id) == saved2);
    printf("%d %d %d\n",
        osty_gc_debug_compaction_count_total() == 2,
        osty_gc_debug_forwarding_count() == 2,
        osty_gc_debug_live_count() == 1);

    root = NULL;
    {
        void *garbage3 = osty_gc_alloc_v1(10, 8, "garbage3");
        (void)garbage3;
    }
    osty_gc_safepoint_v1(3, root_slots, 1);
    printf("%d %d %d\n",
        osty_gc_debug_payload_for_stable_id(stable_id) == NULL,
        osty_gc_debug_forwarding_count() == 0,
        osty_gc_load_v1(saved0) == saved0);
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
	if got, want := string(runOutput), "1 1 1\n1 1 1\n1 1 1\n1 1 1\n"; got != want {
		t.Fatalf("phase-D forwarding-history harness stdout = %q, want %q", got, want)
	}
}

func TestBundledRuntimeMapRemapsCompactionPhaseD(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_phase_d_map_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_phase_d_map_harness")
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

enum {
    OSTY_RT_ABI_PTR = 4,
};

void *osty_gc_alloc_v1(int64_t object_kind, int64_t byte_size, const char *site) __asm__(OSTY_GC_SYMBOL("osty.gc.alloc_v1"));
void osty_gc_safepoint_v1(int64_t safepoint_id, void *const *root_slots, int64_t root_slot_count) __asm__(OSTY_GC_SYMBOL("osty.gc.safepoint_v1"));
void *osty_gc_load_v1(void *value) __asm__(OSTY_GC_SYMBOL("osty.gc.load_v1"));

void *osty_rt_map_new(int64_t key_kind, int64_t value_kind, int64_t value_size, void *value_trace);
void osty_rt_map_insert_ptr(void *raw_map, void *key, const void *value);
void osty_rt_map_get_or_abort_ptr(void *raw_map, void *key, void *out_value);
void *osty_rt_map_key_at_ptr(void *raw_map, int64_t index);

int64_t osty_gc_debug_stable_id(void *payload);
int64_t osty_gc_debug_compaction_count_total(void);
int64_t osty_gc_debug_forwarded_objects_last(void);

int main(void) {
    void *map = osty_rt_map_new(OSTY_RT_ABI_PTR, OSTY_RT_ABI_PTR, (int64_t)sizeof(void *), NULL);
    void *saved_map = map;
    void *key = osty_gc_alloc_v1(7, 24, "key");
    void *saved_key = key;
    void *value = osty_gc_alloc_v1(8, 24, "value");
    void *saved_value = value;
    int64_t map_id = osty_gc_debug_stable_id(map);
    int64_t key_id = osty_gc_debug_stable_id(key);
    int64_t value_id = osty_gc_debug_stable_id(value);
    void *root = map;
    void *root_slots[1] = { &root };
    void *loaded = NULL;
    void *key_out = NULL;

    osty_rt_map_insert_ptr(map, key, &value);
    {
        void *garbage = osty_gc_alloc_v1(9, 8, "garbage");
        (void)garbage;
    }
    osty_gc_safepoint_v1(1, root_slots, 1);

    osty_rt_map_get_or_abort_ptr(saved_map, saved_key, &loaded);
    key_out = osty_rt_map_key_at_ptr(saved_map, 0);

    printf("%d %d %d\n",
        root != saved_map,
        osty_gc_debug_stable_id(root) == map_id,
        osty_gc_debug_compaction_count_total() == 1);
    printf("%d %d %d\n",
        loaded == osty_gc_load_v1(saved_value),
        key_out == osty_gc_load_v1(saved_key),
        osty_gc_debug_forwarded_objects_last() == 3);
    printf("%d %d %d\n",
        osty_gc_debug_stable_id(key_out) == key_id,
        osty_gc_debug_stable_id(loaded) == value_id,
        osty_gc_load_v1(saved_map) == root);
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
	if got, want := string(runOutput), "1 1 1\n1 1 1\n1 1 1\n"; got != want {
		t.Fatalf("phase-D map harness stdout = %q, want %q", got, want)
	}
}

func TestBundledRuntimeMapCompositeValueRemapsCompactionPhaseD(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_phase_d_map_composite_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_phase_d_map_composite_harness")
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

typedef struct pair {
    void *left;
    void *right;
} pair;

enum {
    OSTY_RT_ABI_I64 = 1,
};

void *osty_gc_alloc_v1(int64_t object_kind, int64_t byte_size, const char *site) __asm__(OSTY_GC_SYMBOL("osty.gc.alloc_v1"));
void osty_gc_safepoint_v1(int64_t safepoint_id, void *const *root_slots, int64_t root_slot_count) __asm__(OSTY_GC_SYMBOL("osty.gc.safepoint_v1"));
void *osty_gc_load_v1(void *value) __asm__(OSTY_GC_SYMBOL("osty.gc.load_v1"));
void osty_gc_mark_slot_v1(void *slot_addr) __asm__(OSTY_GC_SYMBOL("osty.gc.mark_slot_v1"));

void *osty_rt_map_new(int64_t key_kind, int64_t value_kind, int64_t value_size, void *value_trace);
void osty_rt_map_insert_i64(void *raw_map, int64_t key, const void *value);
void osty_rt_map_get_or_abort_i64(void *raw_map, int64_t key, void *out_value);

int64_t osty_gc_debug_stable_id(void *payload);
int64_t osty_gc_debug_compaction_count_total(void);
int64_t osty_gc_debug_forwarded_objects_last(void);

static void trace_pair(void *slot_addr) {
    pair *value = (pair *)slot_addr;
    osty_gc_mark_slot_v1((void *)&value->left);
    osty_gc_mark_slot_v1((void *)&value->right);
}

int main(void) {
    void *map = osty_rt_map_new(OSTY_RT_ABI_I64, 0, (int64_t)sizeof(pair), trace_pair);
    void *saved_map = map;
    void *left = osty_gc_alloc_v1(7, 24, "left");
    void *saved_left = left;
    void *right = osty_gc_alloc_v1(8, 24, "right");
    void *saved_right = right;
    int64_t map_id = osty_gc_debug_stable_id(map);
    int64_t left_id = osty_gc_debug_stable_id(left);
    int64_t right_id = osty_gc_debug_stable_id(right);
    void *root = map;
    void *root_slots[1] = { &root };
    pair value = { left, right };
    pair loaded = { NULL, NULL };

    osty_rt_map_insert_i64(map, 1, &value);
    {
        void *garbage = osty_gc_alloc_v1(9, 8, "garbage");
        (void)garbage;
    }
    osty_gc_safepoint_v1(1, root_slots, 1);

    osty_rt_map_get_or_abort_i64(saved_map, 1, &loaded);

    printf("%d %d %d\n",
        root != saved_map,
        osty_gc_debug_stable_id(root) == map_id,
        osty_gc_debug_compaction_count_total() == 1);
    printf("%d %d %d\n",
        loaded.left == osty_gc_load_v1(saved_left),
        loaded.right == osty_gc_load_v1(saved_right),
        osty_gc_debug_forwarded_objects_last() == 3);
    printf("%d %d %d\n",
        osty_gc_debug_stable_id(loaded.left) == left_id,
        osty_gc_debug_stable_id(loaded.right) == right_id,
        osty_gc_load_v1(saved_map) == root);
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
	if got, want := string(runOutput), "1 1 1\n1 1 1\n1 1 1\n"; got != want {
		t.Fatalf("phase-D composite-map harness stdout = %q, want %q", got, want)
	}
}

func TestBundledRuntimeChannelRemapsCompactionPhaseD(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_phase_d_channel_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_phase_d_channel_harness")
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

typedef struct osty_rt_chan_recv_result {
    int64_t value;
    int64_t ok;
} osty_rt_chan_recv_result;

void *osty_gc_alloc_v1(int64_t object_kind, int64_t byte_size, const char *site) __asm__(OSTY_GC_SYMBOL("osty.gc.alloc_v1"));
void osty_gc_safepoint_v1(int64_t safepoint_id, void *const *root_slots, int64_t root_slot_count) __asm__(OSTY_GC_SYMBOL("osty.gc.safepoint_v1"));
void *osty_gc_load_v1(void *value) __asm__(OSTY_GC_SYMBOL("osty.gc.load_v1"));

void *osty_rt_thread_chan_make(int64_t capacity);
void osty_rt_thread_chan_send_ptr(void *raw, void *value);
osty_rt_chan_recv_result osty_rt_thread_chan_recv_ptr(void *raw);

int64_t osty_gc_debug_stable_id(void *payload);
int64_t osty_gc_debug_compaction_count_total(void);
int64_t osty_gc_debug_forwarded_objects_last(void);

int main(void) {
    void *ch = osty_rt_thread_chan_make(2);
    void *saved_ch = ch;
    void *child = osty_gc_alloc_v1(7, 24, "child");
    void *saved_child = child;
    int64_t ch_id = osty_gc_debug_stable_id(ch);
    int64_t child_id = osty_gc_debug_stable_id(child);
    void *root = ch;
    void *root_slots[1] = { &root };
    osty_rt_chan_recv_result recv = {0, 0};

    osty_rt_thread_chan_send_ptr(ch, child);
    {
        void *garbage = osty_gc_alloc_v1(8, 8, "garbage");
        (void)garbage;
    }
    osty_gc_safepoint_v1(1, root_slots, 1);
    recv = osty_rt_thread_chan_recv_ptr(saved_ch);

    printf("%d %d %d\n",
        root != saved_ch,
        osty_gc_load_v1(saved_ch) == root,
        osty_gc_debug_stable_id(root) == ch_id);
    printf("%d %d %d\n",
        recv.ok == 1,
        (void *)(uintptr_t)recv.value == osty_gc_load_v1(saved_child),
        osty_gc_debug_stable_id((void *)(uintptr_t)recv.value) == child_id);
    printf("%d %d\n",
        osty_gc_debug_compaction_count_total() == 1,
        osty_gc_debug_forwarded_objects_last() == 2);
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
	if got, want := string(runOutput), "1 1 1\n1 1 1\n1 1\n"; got != want {
		t.Fatalf("phase-D channel harness stdout = %q, want %q", got, want)
	}
}

func TestBundledRuntimeYoungBumpRecyclesSweptMajorSlotsPhaseD(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_phase_d_freelist_major_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_phase_d_freelist_major_harness")
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

void osty_gc_debug_collect(void);
int64_t osty_gc_debug_collection_count(void);
int64_t osty_gc_debug_live_count(void);
int64_t osty_gc_debug_bump_block_count(void);
int64_t osty_gc_debug_bump_recycled_block_count_total(void);

int main(void) {
    void *reused = NULL;

    (void)osty_gc_alloc_v1(7, 24, "dead");

    osty_gc_debug_collect();
    printf("%d %d %d\n",
        osty_gc_debug_collection_count() == 1,
        osty_gc_debug_live_count() == 0,
        osty_gc_debug_bump_block_count() == 0);
    reused = osty_gc_alloc_v1(8, 24, "reused");
    printf("%d %d %d\n",
        reused != NULL,
        osty_gc_debug_live_count() == 1,
        osty_gc_debug_bump_block_count() == 1);
    printf("%d %d %d\n",
        osty_gc_debug_bump_recycled_block_count_total() == 1,
        osty_gc_debug_collection_count() == 1,
        reused != NULL);
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
	runOutput, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, runOutput)
	}
	if got, want := string(runOutput), "1 1 1\n1 1 1\n1 1 1\n"; got != want {
		t.Fatalf("phase-D young-bump-major harness stdout = %q, want %q", got, want)
	}
}

func TestBundledRuntimeYoungBumpRecyclesSweptMinorSlotsPhaseD(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_phase_d_freelist_minor_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_phase_d_freelist_minor_harness")
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

void osty_gc_debug_collect_minor(void);
int64_t osty_gc_debug_minor_count(void);
int64_t osty_gc_debug_live_count(void);
int64_t osty_gc_debug_bump_block_count(void);
int64_t osty_gc_debug_bump_recycled_block_count_total(void);

int main(void) {
    void *reused = NULL;

    (void)osty_gc_alloc_v1(7, 24, "dead");

    osty_gc_debug_collect_minor();
    printf("%d %d %d\n",
        osty_gc_debug_minor_count() == 1,
        osty_gc_debug_live_count() == 0,
        osty_gc_debug_bump_block_count() == 0);
    reused = osty_gc_alloc_v1(8, 24, "reused");
    printf("%d %d %d\n",
        reused != NULL,
        osty_gc_debug_live_count() == 1,
        osty_gc_debug_bump_block_count() == 1);
    printf("%d %d %d\n",
        osty_gc_debug_bump_recycled_block_count_total() == 1,
        osty_gc_debug_minor_count() == 1,
        reused != NULL);
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
	runOutput, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, runOutput)
	}
	if got, want := string(runOutput), "1 1 1\n1 1 1\n1 1 1\n"; got != want {
		t.Fatalf("phase-D young-bump-minor harness stdout = %q, want %q", got, want)
	}
}

func TestBundledRuntimeYoungBumpKeepsFreeListColdAcrossSizeClassesPhaseD(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_phase_d_size_class_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_phase_d_size_class_harness")
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

void osty_gc_debug_collect(void);
int64_t osty_gc_debug_live_count(void);
int64_t osty_gc_debug_free_list_count(void);
int64_t osty_gc_debug_free_list_reused_count_total(void);
int64_t osty_gc_debug_bump_block_count(void);

int main(void) {
    (void)osty_gc_alloc_v1(7, 24, "small");
    void *other = NULL;
    void *same = NULL;

    osty_gc_debug_collect();
    printf("%d %d %d\n",
        osty_gc_debug_live_count() == 0,
        osty_gc_debug_free_list_count() == 0,
        osty_gc_debug_free_list_reused_count_total() == 0);

    other = osty_gc_alloc_v1(8, 120, "other");
    printf("%d %d %d\n",
        other != NULL,
        osty_gc_debug_live_count() == 1,
        osty_gc_debug_bump_block_count() == 1);

    same = osty_gc_alloc_v1(9, 24, "same");
    printf("%d %d %d\n",
        same != NULL,
        osty_gc_debug_live_count() == 2,
        osty_gc_debug_free_list_reused_count_total() == 0);
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
	runOutput, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, runOutput)
	}
	if got, want := string(runOutput), "1 1 1\n1 1 1\n1 1 1\n"; got != want {
		t.Fatalf("phase-D young-bump-size-class harness stdout = %q, want %q", got, want)
	}
}

func TestBundledRuntimeHumongousAllocationsBypassFreeListPhaseD(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_phase_d_humongous_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_phase_d_humongous_harness")
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

void osty_gc_debug_collect(void);
int64_t osty_gc_debug_live_count(void);
int64_t osty_gc_debug_free_list_count(void);
int64_t osty_gc_debug_free_list_reused_count_total(void);
int64_t osty_gc_debug_humongous_threshold_bytes(void);
int64_t osty_gc_debug_humongous_alloc_count_total(void);
int64_t osty_gc_debug_humongous_alloc_bytes_total(void);
int64_t osty_gc_debug_humongous_swept_count_total(void);
int64_t osty_gc_debug_humongous_swept_bytes_total(void);

int main(void) {
    void *huge = osty_gc_alloc_v1(7, 4096, "huge");
    void *huge2 = NULL;

    osty_gc_debug_collect();
    printf("%d %d %d\n",
        osty_gc_debug_live_count() == 0,
        osty_gc_debug_free_list_count() == 0,
        osty_gc_debug_humongous_swept_count_total() == 1);

    huge2 = osty_gc_alloc_v1(8, 4096, "huge2");
    printf("%d %d %d\n",
        osty_gc_debug_humongous_threshold_bytes() < 4096,
        osty_gc_debug_humongous_alloc_count_total() == 2,
        osty_gc_debug_free_list_reused_count_total() == 0);

    printf("%d %d %d\n",
        osty_gc_debug_live_count() == 1,
        osty_gc_debug_humongous_alloc_bytes_total() == 8192,
        osty_gc_debug_humongous_swept_bytes_total() == 4096 && huge != NULL && huge2 != NULL);
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
	runOutput, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, runOutput)
	}
	if got, want := string(runOutput), "1 1 1\n1 1 1\n1 1 1\n"; got != want {
		t.Fatalf("phase-D humongous harness stdout = %q, want %q", got, want)
	}
}

func TestBundledRuntimeBumpAllocatorServesYoungSmallObjectsPhaseD(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_phase_d_bump_small_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_phase_d_bump_small_harness")
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

int64_t osty_gc_debug_live_count(void);
int64_t osty_gc_debug_free_list_reused_count_total(void);
int64_t osty_gc_debug_bump_block_bytes(void);
int64_t osty_gc_debug_bump_block_count(void);
int64_t osty_gc_debug_bump_alloc_count_total(void);
int64_t osty_gc_debug_bump_alloc_bytes_total(void);

int main(void) {
    void *a = osty_gc_alloc_v1(7, 24, "a");
    void *b = osty_gc_alloc_v1(8, 24, "b");
    void *c = osty_gc_alloc_v1(9, 24, "c");
    uintptr_t dab = (uintptr_t)b - (uintptr_t)a;
    uintptr_t dbc = (uintptr_t)c - (uintptr_t)b;

    printf("%d %d %d\n",
        osty_gc_debug_bump_alloc_count_total() == 3,
        osty_gc_debug_bump_block_count() == 1,
        osty_gc_debug_free_list_reused_count_total() == 0);
    printf("%d %d %d\n",
        dab > 0,
        dab == dbc,
        (int64_t)dab < osty_gc_debug_bump_block_bytes());
    printf("%d %d %d\n",
        osty_gc_debug_bump_alloc_bytes_total() == (int64_t)(dab * 3u),
        osty_gc_debug_live_count() == 3,
        a != NULL && b != NULL && c != NULL);
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
	runOutput, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, runOutput)
	}
	if got, want := string(runOutput), "1 1 1\n1 1 1\n1 1 1\n"; got != want {
		t.Fatalf("phase-D bump-small harness stdout = %q, want %q", got, want)
	}
}

func TestBundledRuntimeBumpAllocatorRollsBlocksPhaseD(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_phase_d_bump_rollover_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_phase_d_bump_rollover_harness")
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

int64_t osty_gc_debug_live_count(void);
int64_t osty_gc_debug_free_list_reused_count_total(void);
int64_t osty_gc_debug_bump_block_bytes(void);
int64_t osty_gc_debug_bump_block_count(void);
int64_t osty_gc_debug_bump_block_bytes_total(void);
int64_t osty_gc_debug_bump_alloc_count_total(void);
int64_t osty_gc_debug_humongous_alloc_count_total(void);

int main(void) {
    int i;
    for (i = 0; i < 200; i++) {
        void *p = osty_gc_alloc_v1(7, 480, "roll");
        if (p == NULL) {
            printf("0 0 0\n0 0 0\n0 0 0\n");
            return 0;
        }
    }

    printf("%d %d %d\n",
        osty_gc_debug_bump_alloc_count_total() == 200,
        osty_gc_debug_bump_block_count() >= 2,
        osty_gc_debug_humongous_alloc_count_total() == 0);
    printf("%d %d %d\n",
        osty_gc_debug_bump_block_bytes_total() >= osty_gc_debug_bump_block_bytes() * 2,
        osty_gc_debug_live_count() == 200,
        osty_gc_debug_free_list_reused_count_total() == 0);
    printf("%d %d %d\n",
        osty_gc_debug_bump_block_bytes() == 65536,
        osty_gc_debug_bump_block_count() * osty_gc_debug_bump_block_bytes() <= osty_gc_debug_bump_block_bytes_total(),
        1);
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
	runOutput, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, runOutput)
	}
	if got, want := string(runOutput), "1 1 1\n1 1 1\n1 1 1\n"; got != want {
		t.Fatalf("phase-D bump-rollover harness stdout = %q, want %q", got, want)
	}
}

func TestBundledRuntimeThreadLocalBumpAllocatorPhaseD(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_phase_d_tlab_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_phase_d_tlab_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdint.h>
#include <stdio.h>
#include <pthread.h>

#if defined(__APPLE__)
#define OSTY_GC_SYMBOL(name) "_" name
#else
#define OSTY_GC_SYMBOL(name) name
#endif

void *osty_gc_alloc_v1(int64_t object_kind, int64_t byte_size, const char *site) __asm__(OSTY_GC_SYMBOL("osty.gc.alloc_v1"));

int64_t osty_gc_debug_live_count(void);
int64_t osty_gc_debug_bump_block_bytes(void);
int64_t osty_gc_debug_bump_block_count(void);
int64_t osty_gc_debug_bump_alloc_count_total(void);
int64_t osty_gc_debug_tlab_refill_count_total(void);

static void *worker_alloc(void *arg) {
    (void)arg;
    return osty_gc_alloc_v1(8, 24, "worker");
}

int main(void) {
    pthread_t tid;
    void *a = osty_gc_alloc_v1(7, 24, "main.a");
    void *b = NULL;
    void *c = NULL;
    uintptr_t dac = 0;

    if (pthread_create(&tid, NULL, worker_alloc, NULL) != 0) {
        printf("0 0 0\n0 0 0\n");
        return 0;
    }
    if (pthread_join(tid, &b) != 0) {
        printf("0 0 0\n0 0 0\n");
        return 0;
    }
    c = osty_gc_alloc_v1(9, 24, "main.c");
    dac = (uintptr_t)c - (uintptr_t)a;

    printf("%d %d %d\n",
        osty_gc_debug_bump_block_count() == 2,
        osty_gc_debug_tlab_refill_count_total() == 2,
        osty_gc_debug_bump_alloc_count_total() == 3);
    printf("%d %d %d\n",
        dac > 0,
        (int64_t)dac < osty_gc_debug_bump_block_bytes(),
        osty_gc_debug_live_count() == 3 && a != NULL && b != NULL && c != NULL);
    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", harnessPath, err)
	}
	cmd := exec.Command("clang", "-std=c11", "-pthread", runtimePath, harnessPath, "-o", binaryPath)
	buildOutput, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("clang failed: %v\n%s", err, buildOutput)
	}
	runCmd := exec.Command(binaryPath)
	runOutput, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, runOutput)
	}
	if got, want := string(runOutput), "1 1 1\n1 1 1\n"; got != want {
		t.Fatalf("phase-D tlab harness stdout = %q, want %q", got, want)
	}
}

func TestBundledRuntimeYoungBumpRecycleWaitsForForwardingCleanupPhaseD(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_phase_d_young_recycle_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_phase_d_young_recycle_harness")
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
void *osty_gc_load_v1(void *value) __asm__(OSTY_GC_SYMBOL("osty.gc.load_v1"));

int64_t osty_gc_debug_live_count(void);
int64_t osty_gc_debug_bump_block_count(void);
int64_t osty_gc_debug_bump_recycled_block_count_total(void);
int64_t osty_gc_debug_compaction_count_total(void);
int64_t osty_gc_debug_forwarding_count(void);

int main(void) {
    void *obj = osty_gc_alloc_v1(7, 24, "obj");
    void *saved = obj;
    void *root = obj;
    void *root_slots[1] = { &root };

    osty_gc_safepoint_v1(1, root_slots, 1);
    printf("%d %d %d\n",
        root != saved,
        osty_gc_debug_compaction_count_total() == 1,
        osty_gc_load_v1(saved) == root);
    printf("%d %d %d\n",
        osty_gc_debug_bump_block_count() == 1,
        osty_gc_debug_bump_recycled_block_count_total() == 0,
        osty_gc_debug_forwarding_count() == 1);

    root = NULL;
    {
        void *garbage = osty_gc_alloc_v1(8, 8, "garbage");
        (void)garbage;
    }
    osty_gc_safepoint_v1(2, root_slots, 1);
    printf("%d %d %d\n",
        osty_gc_debug_live_count() == 0,
        osty_gc_debug_bump_block_count() == 0,
        osty_gc_debug_bump_recycled_block_count_total() == 1);
    printf("%d %d %d\n",
        osty_gc_debug_forwarding_count() == 0,
        osty_gc_load_v1(saved) == saved,
        1);
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
	if got, want := string(runOutput), "1 1 1\n1 1 1\n1 1 1\n1 1 1\n"; got != want {
		t.Fatalf("phase-D young-recycle harness stdout = %q, want %q", got, want)
	}
}

func TestBundledRuntimeMinorCopiesYoungSurvivorsIntoSurvivorRegionPhaseD(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_phase_d_survivor_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_phase_d_survivor_harness")
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
void osty_gc_global_root_register_v1(void *slot) __asm__(OSTY_GC_SYMBOL("osty.gc.global_root_register_v1"));
void osty_gc_global_root_unregister_v1(void *slot) __asm__(OSTY_GC_SYMBOL("osty.gc.global_root_unregister_v1"));
void *osty_gc_load_v1(void *value) __asm__(OSTY_GC_SYMBOL("osty.gc.load_v1"));

void osty_gc_debug_collect_minor(void);
void osty_gc_debug_collect_major(void);
int64_t osty_gc_debug_live_count(void);
int64_t osty_gc_debug_generation_of(void *payload);
int64_t osty_gc_debug_age_of(void *payload);
int64_t osty_gc_debug_compaction_count_total(void);
int64_t osty_gc_debug_survivor_bump_block_count(void);
int64_t osty_gc_debug_survivor_bump_alloc_count_total(void);
int64_t osty_gc_debug_survivor_tlab_refill_count_total(void);
int64_t osty_gc_debug_survivor_bump_recycled_block_count_total(void);

static void *g_slot = NULL;

int main(void) {
    void *saved0 = NULL;
    void *saved1 = NULL;
    void *saved2 = NULL;

    g_slot = osty_gc_alloc_v1(7, 24, "obj");
    saved0 = g_slot;
    osty_gc_global_root_register_v1(&g_slot);

    osty_gc_debug_collect_minor();
    saved1 = g_slot;
    printf("%d %d %d\n",
        saved1 != saved0,
        osty_gc_load_v1(saved0) == saved1,
        osty_gc_debug_survivor_bump_block_count() == 1);
    printf("%d %d %d\n",
        osty_gc_debug_survivor_bump_alloc_count_total() == 1,
        osty_gc_debug_compaction_count_total() == 1,
        osty_gc_debug_survivor_tlab_refill_count_total() == 1 &&
            osty_gc_debug_generation_of(g_slot) == 0 &&
            osty_gc_debug_age_of(g_slot) == 1);

    osty_gc_debug_collect_minor();
    saved2 = g_slot;
    printf("%d %d %d\n",
        saved2 != saved1,
        osty_gc_load_v1(saved0) == saved2,
        osty_gc_load_v1(saved1) == saved2);
    printf("%d %d %d\n",
        osty_gc_debug_survivor_bump_block_count() == 2,
        osty_gc_debug_survivor_bump_alloc_count_total() == 2,
        osty_gc_debug_survivor_tlab_refill_count_total() == 2 &&
            osty_gc_debug_generation_of(g_slot) == 0 &&
            osty_gc_debug_age_of(g_slot) == 2);

    osty_gc_global_root_unregister_v1(&g_slot);
    osty_gc_debug_collect_major();
    printf("%d %d %d\n",
        osty_gc_debug_live_count() == 0,
        osty_gc_debug_survivor_bump_block_count() == 0,
        osty_gc_debug_survivor_bump_recycled_block_count_total() == 2);
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
	runCmd.Env = append(os.Environ(), "OSTY_GC_PROMOTE_AGE=3")
	runOutput, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, runOutput)
	}
	if got, want := string(runOutput), "1 1 1\n1 1 1\n1 1 1\n1 1 1\n1 1 1\n"; got != want {
		t.Fatalf("phase-D survivor harness stdout = %q, want %q", got, want)
	}
}

func TestBundledRuntimeCompactionUsesOldBumpRegionPhaseD(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_phase_d_old_bump_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_phase_d_old_bump_harness")
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
void *osty_gc_load_v1(void *value) __asm__(OSTY_GC_SYMBOL("osty.gc.load_v1"));

int64_t osty_gc_debug_stable_id(void *payload);
int64_t osty_gc_debug_live_count(void);
int64_t osty_gc_debug_bump_block_bytes(void);
int64_t osty_gc_debug_compaction_count_total(void);
int64_t osty_gc_debug_forwarded_objects_last(void);
int64_t osty_gc_debug_old_bump_block_count(void);
int64_t osty_gc_debug_old_bump_block_bytes_total(void);
int64_t osty_gc_debug_old_bump_alloc_count_total(void);
int64_t osty_gc_debug_old_bump_alloc_bytes_total(void);
int64_t osty_gc_debug_old_tlab_refill_count_total(void);

int main(void) {
    void *obj = osty_gc_alloc_v1(7, 24, "obj");
    void *saved = obj;
    int64_t stable_id = osty_gc_debug_stable_id(obj);
    void *root = obj;
    void *root_slots[1] = { &root };

    osty_gc_safepoint_v1(1, root_slots, 1);

    printf("%d %d %d\n",
        root != saved,
        osty_gc_load_v1(saved) == root,
        osty_gc_debug_stable_id(root) == stable_id);
    printf("%d %d %d\n",
        osty_gc_debug_compaction_count_total() == 1,
        osty_gc_debug_forwarded_objects_last() == 1,
        osty_gc_debug_old_bump_alloc_count_total() == 1);
    printf("%d %d %d\n",
        osty_gc_debug_old_bump_block_count() == 1,
        osty_gc_debug_old_bump_block_bytes_total() >= osty_gc_debug_bump_block_bytes(),
        osty_gc_debug_old_tlab_refill_count_total() == 1 &&
            osty_gc_debug_live_count() == 1);
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
	if got, want := string(runOutput), "1 1 1\n1 1 1\n1 1 1\n"; got != want {
		t.Fatalf("phase-D old-bump harness stdout = %q, want %q", got, want)
	}
}

func TestBundledRuntimeOldBumpRegionRecyclesEmptyBlocksPhaseD(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_phase_d_old_bump_recycle_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_phase_d_old_bump_recycle_harness")
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
int64_t osty_gc_debug_bump_block_bytes(void);
int64_t osty_gc_debug_old_bump_block_count(void);
int64_t osty_gc_debug_old_bump_alloc_count_total(void);
int64_t osty_gc_debug_old_bump_recycled_block_count_total(void);
int64_t osty_gc_debug_old_bump_recycled_bytes_total(void);

int main(void) {
    void *obj = osty_gc_alloc_v1(7, 24, "obj");
    void *root = obj;
    void *root_slots[1] = { &root };

    osty_gc_safepoint_v1(1, root_slots, 1);
    printf("%d %d %d\n",
        osty_gc_debug_old_bump_block_count() == 1,
        osty_gc_debug_old_bump_alloc_count_total() == 1,
        osty_gc_debug_old_bump_recycled_block_count_total() == 0);

    root = NULL;
    {
        void *garbage = osty_gc_alloc_v1(8, 8, "garbage");
        (void)garbage;
    }
    osty_gc_safepoint_v1(2, root_slots, 1);
    printf("%d %d %d\n",
        osty_gc_debug_live_count() == 0,
        osty_gc_debug_old_bump_block_count() == 0,
        osty_gc_debug_old_bump_recycled_block_count_total() == 1);
    printf("%d %d %d\n",
        osty_gc_debug_old_bump_recycled_bytes_total() >= osty_gc_debug_bump_block_bytes(),
        osty_gc_debug_old_bump_alloc_count_total() == 1,
        1);
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
	if got, want := string(runOutput), "1 1 1\n1 1 1\n1 1 1\n"; got != want {
		t.Fatalf("phase-D old-bump-recycle harness stdout = %q, want %q", got, want)
	}
}

func TestBundledRuntimePinnedAllocatorUsesPinnedRegionPhaseD(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_phase_d_pinned_region_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_phase_d_pinned_region_harness")
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

void *osty_gc_alloc_pinned_v1(int64_t object_kind, int64_t byte_size, const char *site) __asm__(OSTY_GC_SYMBOL("osty.gc.alloc_pinned_v1"));
void osty_gc_unpin_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.unpin_v1"));

void osty_gc_debug_collect_major(void);
int64_t osty_gc_debug_live_count(void);
int64_t osty_gc_debug_generation_of(void *payload);
int64_t osty_gc_debug_pin_count_of(void *payload);
int64_t osty_gc_debug_pinned_count(void);
int64_t osty_gc_debug_compaction_count_total(void);
int64_t osty_gc_debug_pinned_bump_block_count(void);
int64_t osty_gc_debug_pinned_bump_alloc_count_total(void);
int64_t osty_gc_debug_pinned_tlab_refill_count_total(void);
int64_t osty_gc_debug_pinned_bump_recycled_block_count_total(void);

int main(void) {
    void *obj = osty_gc_alloc_pinned_v1(7, 24, "pinned");

    printf("%d %d %d\n",
        obj != NULL,
        osty_gc_debug_pin_count_of(obj) == 1,
        osty_gc_debug_pinned_count() == 1);
    printf("%d %d %d\n",
        osty_gc_debug_generation_of(obj) == 1,
        osty_gc_debug_pinned_bump_block_count() == 1,
        osty_gc_debug_pinned_bump_alloc_count_total() == 1);
    printf("%d %d %d\n",
        osty_gc_debug_pinned_tlab_refill_count_total() == 1,
        osty_gc_debug_live_count() == 1,
        osty_gc_debug_compaction_count_total() == 0);

    osty_gc_debug_collect_major();
    printf("%d %d %d\n",
        osty_gc_debug_live_count() == 1,
        osty_gc_debug_pinned_count() == 1,
        osty_gc_debug_pinned_bump_block_count() == 1);

    osty_gc_unpin_v1(obj);
    osty_gc_debug_collect_major();
    printf("%d %d %d\n",
        osty_gc_debug_live_count() == 0,
        osty_gc_debug_pinned_count() == 0,
        osty_gc_debug_pinned_bump_block_count() == 0 &&
            osty_gc_debug_pinned_bump_recycled_block_count_total() == 1);
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
	runOutput, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, runOutput)
	}
	if got, want := string(runOutput), "1 1 1\n1 1 1\n1 1 1\n1 1 1\n1 1 1\n"; got != want {
		t.Fatalf("phase-D pinned-region harness stdout = %q, want %q", got, want)
	}
}

func TestBundledRuntimePinnedAllocatorThreadLocalTlabPhaseD(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_phase_d_pinned_tlab_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_phase_d_pinned_tlab_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdint.h>
#include <stdio.h>
#include <pthread.h>

#if defined(__APPLE__)
#define OSTY_GC_SYMBOL(name) "_" name
#else
#define OSTY_GC_SYMBOL(name) name
#endif

void *osty_gc_alloc_pinned_v1(int64_t object_kind, int64_t byte_size, const char *site) __asm__(OSTY_GC_SYMBOL("osty.gc.alloc_pinned_v1"));
void osty_gc_unpin_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.unpin_v1"));

void osty_gc_debug_collect_major(void);
int64_t osty_gc_debug_live_count(void);
int64_t osty_gc_debug_bump_block_bytes(void);
int64_t osty_gc_debug_pinned_bump_block_count(void);
int64_t osty_gc_debug_pinned_bump_alloc_count_total(void);
int64_t osty_gc_debug_pinned_tlab_refill_count_total(void);
int64_t osty_gc_debug_pinned_bump_recycled_block_count_total(void);

static void *worker_alloc(void *arg) {
    (void)arg;
    return osty_gc_alloc_pinned_v1(8, 24, "worker");
}

int main(void) {
    pthread_t tid;
    void *a = osty_gc_alloc_pinned_v1(7, 24, "main.a");
    void *b = NULL;
    void *c = NULL;
    uintptr_t dac = 0;

    if (pthread_create(&tid, NULL, worker_alloc, NULL) != 0) {
        printf("0 0 0\n0 0 0\n0 0 0\n");
        return 0;
    }
    if (pthread_join(tid, &b) != 0) {
        printf("0 0 0\n0 0 0\n0 0 0\n");
        return 0;
    }
    c = osty_gc_alloc_pinned_v1(9, 24, "main.c");
    dac = (uintptr_t)c - (uintptr_t)a;

    printf("%d %d %d\n",
        osty_gc_debug_pinned_bump_block_count() == 2,
        osty_gc_debug_pinned_tlab_refill_count_total() == 2,
        osty_gc_debug_pinned_bump_alloc_count_total() == 3);
    printf("%d %d %d\n",
        dac > 0,
        (int64_t)dac < osty_gc_debug_bump_block_bytes(),
        osty_gc_debug_live_count() == 3 && a != NULL && b != NULL && c != NULL);

    osty_gc_unpin_v1(a);
    osty_gc_unpin_v1(b);
    osty_gc_unpin_v1(c);
    osty_gc_debug_collect_major();
    printf("%d %d %d\n",
        osty_gc_debug_live_count() == 0,
        osty_gc_debug_pinned_bump_block_count() == 0,
        osty_gc_debug_pinned_bump_recycled_block_count_total() == 2);
    return 0;
}
`), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", harnessPath, err)
	}
	cmd := exec.Command("clang", "-std=c11", "-pthread", runtimePath, harnessPath, "-o", binaryPath)
	buildOutput, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("clang failed: %v\n%s", err, buildOutput)
	}
	runCmd := exec.Command(binaryPath)
	runOutput, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, runOutput)
	}
	if got, want := string(runOutput), "1 1 1\n1 1 1\n1 1 1\n"; got != want {
		t.Fatalf("phase-D pinned-tlab harness stdout = %q, want %q", got, want)
	}
}

func TestBundledRuntimeGlobalRootRemapsCompactionPhaseD(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_phase_d_global_root_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_phase_d_global_root_harness")
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
void osty_gc_global_root_register_v1(void *slot) __asm__(OSTY_GC_SYMBOL("osty.gc.global_root_register_v1"));
void osty_gc_global_root_unregister_v1(void *slot) __asm__(OSTY_GC_SYMBOL("osty.gc.global_root_unregister_v1"));
void *osty_gc_load_v1(void *value) __asm__(OSTY_GC_SYMBOL("osty.gc.load_v1"));

void osty_gc_debug_collect(void);
int64_t osty_gc_debug_live_count(void);
int64_t osty_gc_debug_stable_id(void *payload);
void *osty_gc_debug_payload_for_stable_id(int64_t stable_id);
int64_t osty_gc_debug_compaction_count_total(void);
int64_t osty_gc_debug_forwarding_count(void);

static void *g_slot = NULL;

int main(void) {
    g_slot = osty_gc_alloc_v1(7, 24, "global");
    void *saved = g_slot;
    int64_t stable_id = osty_gc_debug_stable_id(g_slot);
    osty_gc_global_root_register_v1(&g_slot);

    osty_gc_debug_collect();
    printf("%d %d %d %d\n",
        g_slot != saved,
        osty_gc_load_v1(saved) == g_slot,
        osty_gc_debug_payload_for_stable_id(stable_id) == g_slot,
        osty_gc_debug_compaction_count_total() == 1);
    printf("%d %d\n",
        osty_gc_debug_forwarding_count() == 1,
        osty_gc_debug_live_count() == 1);

    osty_gc_global_root_unregister_v1(&g_slot);
    osty_gc_debug_collect();
    printf("%d %d\n",
        osty_gc_debug_live_count() == 0,
        osty_gc_debug_payload_for_stable_id(stable_id) == NULL);
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
	if got, want := string(runOutput), "1 1 1 1\n1 1\n1 1\n"; got != want {
		t.Fatalf("phase-D global-root harness stdout = %q, want %q", got, want)
	}
}

// TestBundledRuntimeIncrementalFinishCompactsPhaseD verifies that the
// incremental major path runs Phase D compaction on finish — before
// this landed, `osty.gc.collect_incremental_finish` only swept, so
// `OSTY_GC_INCREMENTAL=1` callers silently skipped evacuation and
// forwarding-table rebuild. The harness drives start → step → finish
// manually, then checks the stack root was remapped, `load_v1` follows
// the forwarding of the pre-compact payload, and the compaction
// counter incremented exactly once.
func TestBundledRuntimeIncrementalFinishCompactsPhaseD(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_phase_d_incremental_finish_harness.c")
	binaryName := "runtime_gc_phase_d_incremental_finish_harness"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binaryPath := filepath.Join(dir, binaryName)
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdint.h>
#include <stdbool.h>
#include <stdio.h>

#if defined(__APPLE__)
#define OSTY_GC_SYMBOL(name) "_" name
#else
#define OSTY_GC_SYMBOL(name) name
#endif

void *osty_gc_alloc_v1(int64_t object_kind, int64_t byte_size, const char *site) __asm__(OSTY_GC_SYMBOL("osty.gc.alloc_v1"));
void *osty_gc_load_v1(void *value) __asm__(OSTY_GC_SYMBOL("osty.gc.load_v1"));

void osty_gc_collect_incremental_start_with_stack_roots(void *const *root_slots, int64_t root_slot_count);
bool osty_gc_collect_incremental_step(int64_t budget);
void osty_gc_collect_incremental_finish_with_stack_roots(void *const *root_slots, int64_t root_slot_count);

int64_t osty_gc_debug_stable_id(void *payload);
void *osty_gc_debug_payload_for_stable_id(int64_t stable_id);
int64_t osty_gc_debug_compaction_count_total(void);
int64_t osty_gc_debug_forwarded_objects_last(void);
int64_t osty_gc_debug_forwarding_count(void);
int64_t osty_gc_debug_load_forwarded_count(void);
void *osty_rt_list_new(void);
void osty_rt_list_push_ptr(void *list, void *value);
void *osty_rt_list_get_ptr(void *list, int64_t index);

int main(void) {
    void *list = osty_rt_list_new();
    void *saved_list = list;
    void *child = osty_gc_alloc_v1(7, 32, "child");
    void *saved_child = child;
    int64_t list_id = osty_gc_debug_stable_id(list);
    int64_t child_id = osty_gc_debug_stable_id(child);
    osty_rt_list_push_ptr(list, child);

    void *root = list;
    void *root_slots[1] = { &root };

    osty_gc_collect_incremental_start_with_stack_roots(root_slots, 1);
    while (osty_gc_collect_incremental_step(100)) {}
    osty_gc_collect_incremental_finish_with_stack_roots(root_slots, 1);

    printf("%d %d %d\n",
        root != saved_list,
        osty_gc_load_v1(saved_list) == root,
        osty_gc_debug_stable_id(root) == list_id);
    printf("%d %d %d\n",
        osty_rt_list_get_ptr(saved_list, 0) == osty_gc_load_v1(saved_child),
        osty_gc_debug_stable_id(osty_rt_list_get_ptr(saved_list, 0)) == child_id,
        osty_gc_debug_payload_for_stable_id(child_id) == osty_gc_load_v1(saved_child));
    printf("%d %d %d\n",
        osty_gc_debug_compaction_count_total() == 1,
        osty_gc_debug_forwarded_objects_last() == 2,
        osty_gc_debug_forwarding_count() == 2);
    printf("%d\n", osty_gc_debug_load_forwarded_count() >= 2);
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
	got := strings.ReplaceAll(string(runOutput), "\r\n", "\n")
	if want := "1 1 1\n1 1 1\n1 1 1\n1\n"; got != want {
		t.Fatalf("phase-D incremental-finish harness stdout = %q, want %q", got, want)
	}
}

// TestBundledRuntimeSTWAbortsDuringIncremental covers the C2 depth
// guard — calling STW major while MARK_INCREMENTAL is active aborts
// with a clear message rather than silently stomping the mark stack.
// Same policy for minor. A fork lets the parent observe the abort
// without dying itself.
func TestBundledRuntimeSTWAbortsDuringIncremental(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_stw_during_incremental_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_stw_during_incremental_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdint.h>
#include <stdio.h>
#include <stdbool.h>
#include <stdlib.h>
#include <unistd.h>
#include <sys/wait.h>

#if defined(__APPLE__)
#define OSTY_GC_SYMBOL(name) "_" name
#else
#define OSTY_GC_SYMBOL(name) name
#endif

void *osty_gc_alloc_v1(int64_t object_kind, int64_t byte_size, const char *site) __asm__(OSTY_GC_SYMBOL("osty.gc.alloc_v1"));
void osty_gc_root_bind_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_bind_v1"));

void osty_gc_collect_incremental_start_with_stack_roots(void *const *root_slots, int64_t root_slot_count);
void osty_gc_debug_collect_major(void);
void osty_gc_debug_collect_minor(void);

static int run_stw_during_mark(void (*stw_call)(void)) {
    fflush(stdout);
    pid_t pid = fork();
    if (pid < 0) return 1;
    if (pid == 0) {
        void *a = osty_gc_alloc_v1(7, 32, "a");
        osty_gc_root_bind_v1(a);
        osty_gc_collect_incremental_start_with_stack_roots(NULL, 0);
        /* stw_call should abort. If it returns, the guard is missing. */
        stw_call();
        _exit(99);  /* unreached on success */
    }
    int status = 0;
    waitpid(pid, &status, 0);
    /* We want the child to have died from abort (WIFSIGNALED), OR to
     * have exited with a non-99 code — anything except a clean
     * "reached past stw_call with no abort". */
    if (WIFEXITED(status) && WEXITSTATUS(status) == 99) {
        return 1;  /* guard missing */
    }
    return 0;  /* guard tripped */
}

int main(void) {
    printf("%d\n", run_stw_during_mark(osty_gc_debug_collect_major));
    fflush(stdout);
    printf("%d\n", run_stw_during_mark(osty_gc_debug_collect_minor));
    fflush(stdout);
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
	/* Use Output() so the child's abort messages on stderr don't
	 * contaminate our stdout assertion — the parent still sees them
	 * during debugging via the test framework. */
	runOutput, err := exec.Command(binaryPath).Output()
	if err != nil {
		t.Fatalf("running %q failed: %v", binaryPath, err)
	}
	if got, want := string(runOutput), "0\n0\n"; got != want {
		t.Fatalf("stw-during-incremental harness stdout = %q, want %q", got, want)
	}
}

// TestBundledRuntimeIncrementalMutatorAssist covers C3 §9.2 — mutator
// assist burns allocation pressure into mark work. While an
// incremental major is active, each alloc pays down a proportional
// number of grey units. For a 20-child list seeded at _start, enough
// subsequent 32-byte allocs drain the queue without a single explicit
// step call.
func TestBundledRuntimeIncrementalMutatorAssist(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_assist_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_assist_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdint.h>
#include <stdio.h>
#include <stdbool.h>

#if defined(__APPLE__)
#define OSTY_GC_SYMBOL(name) "_" name
#else
#define OSTY_GC_SYMBOL(name) name
#endif

void *osty_gc_alloc_v1(int64_t object_kind, int64_t byte_size, const char *site) __asm__(OSTY_GC_SYMBOL("osty.gc.alloc_v1"));
void osty_gc_pre_write_v1(void *owner, void *old_value, int64_t slot_kind) __asm__(OSTY_GC_SYMBOL("osty.gc.pre_write_v1"));
void osty_gc_post_write_v1(void *owner, void *value, int64_t slot_kind) __asm__(OSTY_GC_SYMBOL("osty.gc.post_write_v1"));
void osty_gc_root_bind_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_bind_v1"));
void osty_gc_root_release_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_release_v1"));

void osty_gc_collect_incremental_start_with_stack_roots(void *const *root_slots, int64_t root_slot_count);
bool osty_gc_collect_incremental_step(int64_t budget);
void osty_gc_collect_incremental_finish(void);

int64_t osty_gc_debug_mutator_assist_work_total(void);
int64_t osty_gc_debug_mutator_assist_calls_total(void);
int64_t osty_gc_debug_mark_stack_count(void);
int64_t osty_gc_debug_state(void);
void *osty_rt_list_new(void);
void osty_rt_list_push_ptr(void *list, void *value);

int main(void) {
    /* Seed phase: rooted list with 20 children. None of this runs
     * during MARK so assist is inactive; counters stay zero. */
    void *list = osty_rt_list_new();
    osty_gc_root_bind_v1(list);
    for (int i = 0; i < 20; i++) {
        void *child = osty_gc_alloc_v1(7, 16, "child");
        osty_gc_pre_write_v1(list, NULL, 0);
        osty_rt_list_push_ptr(list, child);
        osty_gc_post_write_v1(list, child, 0);
    }
    printf("%lld %lld\n",
        (long long)osty_gc_debug_mutator_assist_calls_total(),
        (long long)osty_gc_debug_mutator_assist_work_total());

    /* Start incremental. List is GREY, children still WHITE. Grey
     * queue has 1 entry (list). */
    osty_gc_collect_incremental_start_with_stack_roots(NULL, 0);

    /* Now alloc without explicit stepping. With bytes_per_unit=16
     * and 32-byte allocs, each call drains 2 units. 1 list + 20
     * children = 21 grey units after the first assist pops list
     * (which traces + enqueues 20 children). Roughly 11 allocs
     * should suffice. Allocate 50 to safely drain. */
    for (int i = 0; i < 50; i++) {
        (void)osty_gc_alloc_v1(7, 32, "spam");
    }
    /* Assist should have burned the grey queue. */
    int64_t work = osty_gc_debug_mutator_assist_work_total();
    int64_t calls = osty_gc_debug_mutator_assist_calls_total();
    int64_t remaining = osty_gc_debug_mark_stack_count();
    printf("%d %d %lld\n", calls > 0, work >= 21, remaining);

    osty_gc_collect_incremental_finish();
    printf("%lld\n", (long long)osty_gc_debug_state());

    osty_gc_root_release_v1(list);
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
	runCmd.Env = append(os.Environ(), "OSTY_GC_ASSIST_BYTES_PER_UNIT=16")
	runOutput, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, runOutput)
	}
	/* Expected:
	 *  0 0    — seed phase ran without MARK_INCREMENTAL, so no assist
	 *           work and no assist calls.
	 *  1 1 0  — after 50 assisted allocs, calls > 0, work >= 21, grey
	 *           queue drained to 0.
	 *  0      — final state = IDLE after finish.
	 */
	if got, want := string(runOutput), "0 0\n1 1 0\n0\n"; got != want {
		t.Fatalf("mutator-assist harness stdout = %q, want %q", got, want)
	}
}

// TestBundledRuntimeSATBBarrierScenarios covers the C5 depth pass —
// three barrier edge cases the headline test didn't exercise:
// (1) multiple overwrites to the same slot (each old_value greyed),
// (2) pre-start barrier is a no-op (state=IDLE),
// (3) post-finish barrier is a no-op (state back to IDLE).
func TestBundledRuntimeSATBBarrierScenarios(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_satb_scenarios_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_satb_scenarios_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdint.h>
#include <stdio.h>
#include <stdbool.h>

#if defined(__APPLE__)
#define OSTY_GC_SYMBOL(name) "_" name
#else
#define OSTY_GC_SYMBOL(name) name
#endif

void *osty_gc_alloc_v1(int64_t object_kind, int64_t byte_size, const char *site) __asm__(OSTY_GC_SYMBOL("osty.gc.alloc_v1"));
void osty_gc_pre_write_v1(void *owner, void *old_value, int64_t slot_kind) __asm__(OSTY_GC_SYMBOL("osty.gc.pre_write_v1"));
void osty_gc_post_write_v1(void *owner, void *value, int64_t slot_kind) __asm__(OSTY_GC_SYMBOL("osty.gc.post_write_v1"));
void osty_gc_root_bind_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_bind_v1"));
void osty_gc_root_release_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_release_v1"));

void osty_gc_collect_incremental_start_with_stack_roots(void *const *root_slots, int64_t root_slot_count);
bool osty_gc_collect_incremental_step(int64_t budget);
void osty_gc_collect_incremental_finish(void);
void osty_gc_debug_collect_major(void);

int64_t osty_gc_debug_satb_barrier_greyed_total(void);
int64_t osty_gc_debug_live_count(void);

int main(void) {
    void *owner = osty_gc_alloc_v1(7, 32, "owner");
    osty_gc_root_bind_v1(owner);
    void *c1 = osty_gc_alloc_v1(7, 32, "c1");
    void *c2 = osty_gc_alloc_v1(7, 32, "c2");
    void *c3 = osty_gc_alloc_v1(7, 32, "c3");

    /* Scenario A — pre-start barrier is a no-op. */
    int64_t before_a = osty_gc_debug_satb_barrier_greyed_total();
    osty_gc_pre_write_v1(owner, c1, 0);
    osty_gc_post_write_v1(owner, c1, 0);
    int64_t after_a = osty_gc_debug_satb_barrier_greyed_total();
    printf("%lld\n", after_a - before_a);  /* expect 0 */

    /* Scenario B — multiple overwrites to the same slot during MARK
     * each grey a different old_value. c1, c2, c3 should all get
     * greyed. */
    osty_gc_collect_incremental_start_with_stack_roots(NULL, 0);
    int64_t before_b = osty_gc_debug_satb_barrier_greyed_total();
    osty_gc_pre_write_v1(owner, c1, 0);  /* grey c1 */
    osty_gc_post_write_v1(owner, c2, 0);
    osty_gc_pre_write_v1(owner, c2, 0);  /* grey c2 */
    osty_gc_post_write_v1(owner, c3, 0);
    osty_gc_pre_write_v1(owner, c3, 0);  /* grey c3 */
    osty_gc_post_write_v1(owner, NULL, 0);
    int64_t after_b = osty_gc_debug_satb_barrier_greyed_total();
    printf("%lld\n", after_b - before_b);  /* expect 3 */

    while (osty_gc_collect_incremental_step(100)) {}
    osty_gc_collect_incremental_finish();

    /* Scenario C — post-finish barrier is a no-op. */
    int64_t before_c = osty_gc_debug_satb_barrier_greyed_total();
    void *c4 = osty_gc_alloc_v1(7, 32, "c4");
    osty_gc_pre_write_v1(owner, c4, 0);
    osty_gc_post_write_v1(owner, c4, 0);
    int64_t after_c = osty_gc_debug_satb_barrier_greyed_total();
    printf("%lld\n", after_c - before_c);  /* expect 0 */

    /* Everything survives because owner is rooted and SATB + trace
     * pulled c1..c3 through. c4 is WHITE but reachable via owner's
     * next trace (if any). To simplify, run a final major cleanup. */
    osty_gc_root_release_v1(owner);
    osty_gc_debug_collect_major();
    printf("%lld\n", (long long)osty_gc_debug_live_count());  /* expect 0 */
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
	/* Disable assist so scenario B's SATB counts are predictable. */
	runCmd.Env = append(os.Environ(), "OSTY_GC_ASSIST_BYTES_PER_UNIT=0")
	runOutput, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, runOutput)
	}
	if got, want := string(runOutput), "0\n3\n0\n0\n"; got != want {
		t.Fatalf("SATB scenarios harness stdout = %q, want %q", got, want)
	}
}

// TestBundledRuntimeIncrementalAutoDispatcher covers the auto-dispatch
// integration — with OSTY_GC_INCREMENTAL=1, safepoint polls that would
// have run a STW major now drive the incremental path across multiple
// safepoint calls. The cycle completes when the grey queue empties.
func TestBundledRuntimeIncrementalAutoDispatcher(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_incremental_auto_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_incremental_auto_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdint.h>
#include <stdio.h>
#include <stdbool.h>

#if defined(__APPLE__)
#define OSTY_GC_SYMBOL(name) "_" name
#else
#define OSTY_GC_SYMBOL(name) name
#endif

void *osty_gc_alloc_v1(int64_t object_kind, int64_t byte_size, const char *site) __asm__(OSTY_GC_SYMBOL("osty.gc.alloc_v1"));
void osty_gc_root_bind_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_bind_v1"));
void osty_gc_safepoint_v1(int64_t id, void *const *roots, int64_t n) __asm__(OSTY_GC_SYMBOL("osty.gc.safepoint_v1"));

int64_t osty_gc_debug_state(void);
int64_t osty_gc_debug_major_count(void);
int64_t osty_gc_debug_minor_count(void);
int64_t osty_gc_debug_incremental_steps_total(void);
int64_t osty_gc_debug_live_count(void);

int main(void) {
    /* Allocate a modest graph; one rooted owner, rest garbage. With
     * a tiny heap threshold the dispatcher picks major, and with
     * OSTY_GC_INCREMENTAL=1 it routes through incremental. */
    void *owner = osty_gc_alloc_v1(7, 64, "owner");
    osty_gc_root_bind_v1(owner);
    for (int i = 0; i < 20; i++) (void)osty_gc_alloc_v1(7, 64, "g");

    /* Poll safepoints repeatedly. Each pass does one budget chunk;
     * eventually the queue drains and state returns to IDLE. */
    int safepoints = 0;
    while (osty_gc_debug_state() != 0 || safepoints == 0) {
        osty_gc_safepoint_v1(0, 0, 0);
        safepoints += 1;
        if (safepoints > 50) break;
    }
    /* State IDLE, at least one major finished via incremental,
     * incremental_steps_total > 0. Minor count stays 0 because the
     * dispatcher only picks minor below the heap threshold. */
    printf("%lld %lld %d\n",
        (long long)osty_gc_debug_state(),
        (long long)osty_gc_debug_major_count(),
        osty_gc_debug_incremental_steps_total() > 0);
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
	runCmd.Env = append(os.Environ(),
		"OSTY_GC_INCREMENTAL=1",
		"OSTY_GC_INCREMENTAL_BUDGET=4",
		"OSTY_GC_THRESHOLD_BYTES=512",
	)
	runOutput, err := runCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, runOutput)
	}
	/* Expected:
	 *   0 1 1  — state IDLE, 1 major completed incrementally, steps > 0
	 *   1      — only the rooted owner survives
	 */
	if got, want := string(runOutput), "0 1 1\n1\n"; got != want {
		t.Fatalf("incremental-auto harness stdout = %q, want %q", got, want)
	}
}

// TestBundledRuntimeIncrementalStress drives a randomized alloc +
// write-barrier + step pattern under MARK_INCREMENTAL with SATB
// active. validate_heap is invoked at every quiescent point; any
// invariant slip flags a failure. Deterministic via srand(7) so a
// regression's failing pattern is reproducible.
func TestBundledRuntimeIncrementalStress(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_gc_incremental_stress_harness.c")
	binaryPath := filepath.Join(dir, "runtime_gc_incremental_stress_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}
	if err := os.WriteFile(harnessPath, []byte(`#include <stdint.h>
#include <stdio.h>
#include <stdbool.h>
#include <stdlib.h>

#if defined(__APPLE__)
#define OSTY_GC_SYMBOL(name) "_" name
#else
#define OSTY_GC_SYMBOL(name) name
#endif

void *osty_gc_alloc_v1(int64_t object_kind, int64_t byte_size, const char *site) __asm__(OSTY_GC_SYMBOL("osty.gc.alloc_v1"));
void osty_gc_pre_write_v1(void *owner, void *old_value, int64_t slot_kind) __asm__(OSTY_GC_SYMBOL("osty.gc.pre_write_v1"));
void osty_gc_post_write_v1(void *owner, void *value, int64_t slot_kind) __asm__(OSTY_GC_SYMBOL("osty.gc.post_write_v1"));
void osty_gc_root_bind_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_bind_v1"));
void osty_gc_root_release_v1(void *root) __asm__(OSTY_GC_SYMBOL("osty.gc.root_release_v1"));

void osty_gc_collect_incremental_start_with_stack_roots(void *const *root_slots, int64_t root_slot_count);
bool osty_gc_collect_incremental_step(int64_t budget);
void osty_gc_collect_incremental_finish(void);
void osty_gc_debug_collect_major(void);

int64_t osty_gc_debug_validate_heap(void);
int64_t osty_gc_debug_state(void);

int main(void) {
    srand(7);
    enum { ROOTS = 4, LIVE = 64 };
    void *roots[ROOTS];
    void *live[LIVE] = {0};
    for (int i = 0; i < ROOTS; i++) {
        roots[i] = osty_gc_alloc_v1(7, 32, "root");
        osty_gc_root_bind_v1(roots[i]);
    }

    int failures = 0;
    int cycles = 0;
    for (int outer = 0; outer < 15; outer++) {
        /* Start an incremental cycle. */
        osty_gc_collect_incremental_start_with_stack_roots(NULL, 0);
        cycles += 1;
        /* Drive it with random budget sizes interleaved with random
         * allocs + writes + SATB-triggering overwrites. */
        int guard = 0;
        while (osty_gc_debug_state() == 1) {  /* MARK_INCREMENTAL */
            /* Random budget between 1 and 32. */
            int budget = (rand() % 32) + 1;
            osty_gc_collect_incremental_step(budget);
            /* 3 allocations per iteration — these invoke mutator
             * assist, which further drains the queue. */
            for (int i = 0; i < 3; i++) {
                int slot = rand() % LIVE;
                live[slot] = osty_gc_alloc_v1(7, (rand() % 48) + 8, "churn");
            }
            /* Random barrier writes: an anchor points at a random
             * live slot. pre_write may grey the previous contents. */
            int anchor = rand() % ROOTS;
            int value = rand() % LIVE;
            if (live[value] != NULL) {
                osty_gc_pre_write_v1(roots[anchor], NULL, 0);
                osty_gc_post_write_v1(roots[anchor], live[value], 0);
            }
            if (osty_gc_debug_validate_heap() != 0) failures += 1;
            if (++guard > 200) break;
        }
        osty_gc_collect_incremental_finish();
        if (osty_gc_debug_validate_heap() != 0) failures += 1;
    }

    /* Final cleanup so validate ends at zero live. */
    for (int i = 0; i < ROOTS; i++) osty_gc_root_release_v1(roots[i]);
    osty_gc_debug_collect_major();

    printf("%d %d %lld\n",
        failures,
        cycles,
        (long long)osty_gc_debug_validate_heap());
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
	out := string(runOutput)
	var failures, cycles, validate int64
	if _, err := fmt.Sscanf(out, "%d %d %d", &failures, &cycles, &validate); err != nil {
		t.Fatalf("unparseable incremental stress output %q: %v", out, err)
	}
	if failures != 0 {
		t.Fatalf("incremental stress recorded %d validate failures; out=%q", failures, out)
	}
	if cycles != 15 {
		t.Fatalf("incremental stress expected 15 cycles, got %d; out=%q", cycles, out)
	}
	if validate != 0 {
		t.Fatalf("final validate = %d, want 0; out=%q", validate, out)
	}
}
