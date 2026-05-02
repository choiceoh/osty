package backend

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestBundledRuntimeListSetBytesV1RoundTrip drives the real C runtime
// through clang to prove that `osty_rt_list_set_bytes_v1` exists as
// a public symbol and writes the supplied bytes into the list slot
// at the given index. The MIR emitter routes `xs[i] = value` /
// `xs[i].field = v` for composite element types through this `_v1`
// symbol; before this fix programs that updated list elements by
// field link-failed with "Undefined symbol _osty_rt_list_set_bytes_v1".
//
// Exercise both "primitive element" (i64) and "composite element"
// (Pair { i64, i64 }) shapes — the latter is the one that previously
// only had a `_get_bytes_v1` pair.
func TestBundledRuntimeListSetBytesV1RoundTrip(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_list_set_bytes_v1_harness.c")
	binaryPath := filepath.Join(dir, "runtime_list_set_bytes_v1_harness")

	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}

	harness := `#include <stdint.h>
#include <stdio.h>

void *osty_rt_list_new(void);
void osty_rt_list_push_bytes_v1(void *list, const void *value, int64_t elem_size);
void osty_rt_list_get_bytes_v1(void *list, int64_t index, void *out, int64_t elem_size);
void osty_rt_list_set_bytes_v1(void *list, int64_t index, const void *value, int64_t elem_size, void *trace_elem);
int64_t osty_rt_list_len(void *list);

typedef struct { int64_t a; int64_t b; } Pair;

int main(void) {
    // Primitive element width — i64.
    void *ints = osty_rt_list_new();
    int64_t v = 0;
    v = 10; osty_rt_list_push_bytes_v1(ints, &v, (int64_t)sizeof(v));
    v = 20; osty_rt_list_push_bytes_v1(ints, &v, (int64_t)sizeof(v));
    v = 30; osty_rt_list_push_bytes_v1(ints, &v, (int64_t)sizeof(v));

    int64_t replaced = 999;
    osty_rt_list_set_bytes_v1(ints, 1, &replaced, (int64_t)sizeof(replaced), (void *)0);

    for (int64_t i = 0; i < osty_rt_list_len(ints); i++) {
        int64_t out = 0;
        osty_rt_list_get_bytes_v1(ints, i, &out, (int64_t)sizeof(out));
        printf("ints[%lld]=%lld\n", (long long)i, (long long)out);
    }

    // Composite element width — Pair { i64, i64 } — the shape MIR
    // routes for indexed-write-preceding-projection updates.
    void *pairs = osty_rt_list_new();
    Pair p = {1, 2};
    osty_rt_list_push_bytes_v1(pairs, &p, (int64_t)sizeof(p));
    p = (Pair){3, 4};
    osty_rt_list_push_bytes_v1(pairs, &p, (int64_t)sizeof(p));

    Pair updated = {99, 100};
    osty_rt_list_set_bytes_v1(pairs, 0, &updated, (int64_t)sizeof(updated), (void *)0);

    for (int64_t i = 0; i < osty_rt_list_len(pairs); i++) {
        Pair out = {0, 0};
        osty_rt_list_get_bytes_v1(pairs, i, &out, (int64_t)sizeof(out));
        printf("pairs[%lld]=(%lld,%lld)\n", (long long)i, (long long)out.a, (long long)out.b);
    }

    return 0;
}
`
	if err := os.WriteFile(harnessPath, []byte(harness), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", harnessPath, err)
	}

	clangArgs := []string{"-std=c11", runtimePath, harnessPath, "-o", binaryPath}
	if runtime.GOOS == "darwin" {
		// Match production `osty build`'s host-frame link flags so the
		// keychain symbols resolve. Other helpers in this package would
		// fail the same way without these — see llvm.go's link command.
		clangArgs = append(clangArgs, "-framework", "Security", "-framework", "CoreFoundation")
	}
	buildOutput, err := runtimeClangCommand(clangArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("clang failed: %v\n%s", err, buildOutput)
	}
	runOutput, err := exec.Command(binaryPath).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, runOutput)
	}

	want := "ints[0]=10\n" +
		"ints[1]=999\n" +
		"ints[2]=30\n" +
		"pairs[0]=(99,100)\n" +
		"pairs[1]=(3,4)\n"
	if got := string(runOutput); got != want {
		t.Fatalf("runtime list_set_bytes_v1 harness stdout mismatch\n---got---\n%s\n---want---\n%s", got, want)
	}
}
