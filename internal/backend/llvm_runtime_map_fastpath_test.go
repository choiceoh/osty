package backend

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestBundledRuntimeMapIndexedHashCachePreservesStringLookup(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_map_index_hash_harness.c")
	binaryPath := filepath.Join(dir, "runtime_map_index_hash_harness")

	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}

	harness := `#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define OSTY_RT_ABI_STRING 5
#define OSTY_RT_ABI_I64 1

void *osty_rt_map_new(int64_t key_kind, int64_t value_kind, int64_t value_size, void *value_trace);
bool osty_rt_map_get_string(void *raw_map, const char *key, void *out_value);
bool osty_rt_map_remove_string(void *raw_map, const char *key);
void osty_rt_map_insert_string(void *raw_map, const char *key, const void *value);
void osty_rt_map_clear(void *raw_map);

static char *dup_cstr(const char *src) {
    size_t len = strlen(src);
    char *dst = (char *)malloc(len + 1);
    if (dst == NULL) {
        fprintf(stderr, "oom\n");
        exit(1);
    }
    memcpy(dst, src, len + 1);
    return dst;
}

int main(void) {
    static const char *words[] = {
        "alpha", "beta", "gamma", "delta", "epsilon",
        "zeta", "eta", "theta", "iota", "kappa",
    };
    void *map = osty_rt_map_new(OSTY_RT_ABI_STRING, OSTY_RT_ABI_I64, (int64_t)sizeof(int64_t), NULL);
    int64_t out = -1;
    int64_t sum = 0;
    for (size_t i = 0; i < sizeof(words) / sizeof(words[0]); ++i) {
        int64_t value = (int64_t)(i + 1) * 11;
        osty_rt_map_insert_string(map, words[i], &value);
    }
    if (!osty_rt_map_get_string(map, dup_cstr("delta"), &out) || out != 44) {
        printf("delta-miss %lld\n", (long long)out);
        return 1;
    }
    sum += out;
    if (!osty_rt_map_remove_string(map, dup_cstr("beta"))) {
        printf("beta-remove\n");
        return 1;
    }
    if (osty_rt_map_get_string(map, dup_cstr("beta"), &out)) {
        printf("beta-hit %lld\n", (long long)out);
        return 1;
    }
    if (!osty_rt_map_get_string(map, dup_cstr("theta"), &out) || out != 88) {
        printf("theta-miss %lld\n", (long long)out);
        return 1;
    }
    sum += out;
    osty_rt_map_clear(map);
    {
        int64_t value = 123;
        osty_rt_map_insert_string(map, dup_cstr("after-clear"), &value);
    }
    if (!osty_rt_map_get_string(map, dup_cstr("after-clear"), &out) || out != 123) {
        printf("after-clear %lld\n", (long long)out);
        return 1;
    }
    sum += out;
    printf("%lld\n", (long long)sum);
    return 0;
}
`
	if err := os.WriteFile(harnessPath, []byte(harness), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", harnessPath, err)
	}

	buildOutput, err := exec.Command("clang", "-std=c11", runtimePath, harnessPath, "-o", binaryPath).CombinedOutput()
	if err != nil {
		t.Fatalf("clang failed: %v\n%s", err, buildOutput)
	}
	runOutput, err := exec.Command(binaryPath).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, runOutput)
	}

	if got, want := string(runOutput), "255\n"; got != want {
		t.Fatalf("runtime map indexed hash harness stdout mismatch\n---got---\n%s\n---want---\n%s", got, want)
	}
}
