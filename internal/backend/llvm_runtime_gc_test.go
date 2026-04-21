package backend

import (
	"os"
	"os/exec"
	"path/filepath"
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
	runOutput, err := exec.Command(binaryPath).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, runOutput)
	}
	if got, want := string(runOutput), "1\n0\n2\n1\n1\n1\n1\n1\n1\n1\n1\n0\n3\n1\n1\n3\n3\n0\n"; got != want {
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
    printf("%d\n", loaded.child == saved_child);
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
	runOutput, err := exec.Command(binaryPath).CombinedOutput()
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
	runOutput, err := exec.Command(binaryPath).CombinedOutput()
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
	runOutput, err := exec.Command(binaryPath).CombinedOutput()
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
	runOutput, err := exec.Command(binaryPath).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, runOutput)
	}
	if got, want := string(runOutput), "2 48 0\n1 0 2 48\n2 1 40 88\n1 1 1\n1\n"; got != want {
		t.Fatalf("runtime stats harness stdout = %q, want %q", got, want)
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
