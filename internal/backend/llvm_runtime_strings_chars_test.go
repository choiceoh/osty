package backend

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestBundledRuntimeStringsCharsAndBytesRoundTrip drives the real C
// runtime through clang to prove that osty_rt_strings_Chars +
// osty_rt_strings_Bytes produce lists whose elements can be read back
// with osty_rt_list_get_bytes_v1 at the same element width the Osty
// frontend assumes (i32 for Char, i8 for Byte). UTF-8 coverage exercises:
//
//   - pure ASCII  "abc"
//   - 2-byte      "ä"  (U+00E4  — C3 A4)
//   - 3-byte      "漢" (U+6F22  — E6 BC A2)
//   - 4-byte      "🜂" (U+1F702 — F0 9F 9C 82)
//   - empty input "" → empty list
//   - ill-formed  "\xC3" (truncated 2-byte lead) → one U+FFFD
//   - ill-formed  "\xE6\xBC" (truncated 3-byte sequence) → one U+FFFD
//   - ill-formed  "\xED\xA0\x80" (surrogate-encoded U+D800) → three U+FFFDs
//     (maximal subpart: ED is a valid lead, A0 is outside the 0x80..0x9F
//     range allowed after ED → first FFFD; then 80 sits in continuation
//     position with no lead → second FFFD; trailing 80 → third FFFD.)
//   - ill-formed  "\xFF" (invalid lead byte) → one U+FFFD
//
// All expected stdout lines are printed in order, so the check is a
// straight byte-equality compare — no JSON, no framing.
func TestBundledRuntimeStringsCharsAndBytesRoundTrip(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_strings_chars_harness.c")
	binaryPath := filepath.Join(dir, "runtime_strings_chars_harness")

	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}

	harness := `#include <stdint.h>
#include <stdio.h>

void *osty_rt_list_new(void);
int64_t osty_rt_list_len(void *list);
void osty_rt_list_get_bytes_v1(void *list, int64_t index, void *out, int64_t elem_size);
void *osty_rt_strings_Chars(const char *value);
void *osty_rt_strings_Bytes(const char *value);

static void dump_chars(const char *label, const char *value) {
    void *list = osty_rt_strings_Chars(value);
    int64_t n = osty_rt_list_len(list);
    printf("%s len=%lld:", label, (long long)n);
    for (int64_t i = 0; i < n; i++) {
        int32_t cp = 0;
        osty_rt_list_get_bytes_v1(list, i, &cp, (int64_t)sizeof(cp));
        printf(" %ld", (long)cp);
    }
    printf("\n");
}

static void dump_bytes(const char *label, const char *value) {
    void *list = osty_rt_strings_Bytes(value);
    int64_t n = osty_rt_list_len(list);
    printf("%s len=%lld:", label, (long long)n);
    for (int64_t i = 0; i < n; i++) {
        int8_t b = 0;
        osty_rt_list_get_bytes_v1(list, i, &b, (int64_t)sizeof(b));
        printf(" %d", (int)(unsigned char)b);
    }
    printf("\n");
}

int main(void) {
    // Well-formed inputs.
    dump_chars("ascii", "abc");                        // 'a' 'b' 'c'
    dump_chars("two-byte", "\xC3\xA4");                // U+00E4
    dump_chars("three-byte", "\xE6\xBC\xA2");          // U+6F22
    dump_chars("four-byte", "\xF0\x9F\x9C\x82");       // U+1F702
    dump_chars("empty", "");

    // Ill-formed — maximal subpart recovery.
    dump_chars("trunc2", "\xC3");                      // truncated 2-byte lead
    dump_chars("trunc3", "\xE6\xBC");                  // truncated 3-byte sequence
    dump_chars("ed_surr", "\xED\xA0\x80");             // UTF-16 surrogate via UTF-8
    dump_chars("bad_lead", "\xFF");                    // invalid lead byte

    // Byte mirror of the ascii case.
    dump_bytes("ascii_b", "abc");                      // 97 98 99
    // Byte mirror of the two-byte codepoint — bytes() does no decode,
    // it must surface the raw 0xC3 0xA4 as unsigned integers.
    dump_bytes("two_b", "\xC3\xA4");                   // 195 164

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

	want := "ascii len=3: 97 98 99\n" +
		"two-byte len=1: 228\n" +
		"three-byte len=1: 28450\n" +
		"four-byte len=1: 128770\n" +
		"empty len=0:\n" +
		"trunc2 len=1: 65533\n" +
		"trunc3 len=1: 65533\n" +
		"ed_surr len=3: 65533 65533 65533\n" +
		"bad_lead len=1: 65533\n" +
		"ascii_b len=3: 97 98 99\n" +
		"two_b len=2: 195 164\n"

	if got := string(runOutput); got != want {
		t.Fatalf("runtime chars/bytes harness stdout mismatch\n---got---\n%s\n---want---\n%s", got, want)
	}
}

func TestBundledRuntimeStringsHelpersPreserveSemantics(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_strings_helpers_harness.c")
	binaryPath := filepath.Join(dir, "runtime_strings_helpers_harness")

	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}

	harness := `#include <stdint.h>
#include <stdio.h>

int64_t osty_rt_strings_ByteLen(const char *value);
int64_t osty_rt_strings_Compare(const char *left, const char *right);
int osty_rt_strings_Equal(const char *left, const char *right);
const char *osty_rt_strings_Concat(const char *left, const char *right);
const char *osty_rt_strings_ConcatN(int64_t count, const char *const *parts);
void *osty_rt_list_new(void);
void osty_rt_list_push_ptr(void *list, void *value);
void *osty_rt_list_sorted_string(void *raw_list);
int64_t osty_rt_list_len(void *list);
void *osty_rt_list_get_ptr(void *list, int64_t index);

int main(void) {
    const char *runtime = osty_rt_strings_Concat("run", "time");
    const char *pieces[3] = { "map", "/", "key" };
    const char *joined = osty_rt_strings_ConcatN(3, pieces);

    printf("%lld %lld\n",
           (long long)osty_rt_strings_ByteLen("compiler"),
           (long long)osty_rt_strings_ByteLen(runtime));
    printf("%d %d %d\n",
           osty_rt_strings_Equal("osty", "osty"),
           osty_rt_strings_Equal(runtime, "runtime"),
           osty_rt_strings_Equal(joined, "map/key"));
    printf("%lld %lld %lld\n",
           (long long)osty_rt_strings_Compare("a", "b"),
           (long long)osty_rt_strings_Compare("same", "same"),
           (long long)osty_rt_strings_Compare(NULL, ""));

    void *items = osty_rt_list_new();
    osty_rt_list_push_ptr(items, (void *)"runtime");
    osty_rt_list_push_ptr(items, (void *)runtime);
    osty_rt_list_push_ptr(items, (void *)"compiler");
    void *sorted = osty_rt_list_sorted_string(items);
    for (int64_t i = 0; i < osty_rt_list_len(sorted); i++) {
        printf("%s\n", (const char *)osty_rt_list_get_ptr(sorted, i));
    }
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

	want := "8 7\n" +
		"1 1 1\n" +
		"-1 0 0\n" +
		"compiler\n" +
		"runtime\n" +
		"runtime\n"
	if got := string(runOutput); got != want {
		t.Fatalf("runtime string helpers harness stdout mismatch\n---got---\n%s\n---want---\n%s", got, want)
	}
}
