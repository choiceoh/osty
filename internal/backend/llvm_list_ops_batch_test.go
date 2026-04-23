package backend

import (
	"context"
	"os/exec"
	"testing"
)

// TestLLVMBackendBinaryRunsListReverseInPlace covers `xs.reverse()` —
// an in-place reverse that returns Unit. Routed through the new
// `IntrinsicListReverse` → `osty_rt_list_reverse` runtime helper
// (generic byte-swap using list->elem_size, so one C function covers
// every element type).
func TestLLVMBackendBinaryRunsListReverseInPlace(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `fn main() {
    let mut xs: List<Int> = [1, 2, 3, 4, 5]
    xs.reverse()
    for x in xs { println(x) }
    println(xs.len())
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	output, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "5\n4\n3\n2\n1\n5\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

// TestLLVMBackendBinaryRunsListReversedNewList covers `xs.reversed()`
// — returns a freshly allocated List<T> with reversed elements while
// leaving the original unchanged. The new list inherits the source's
// elem_size + trace callback so GC bookkeeping stays intact.
func TestLLVMBackendBinaryRunsListReversedNewList(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `fn main() {
    let orig: List<Int> = [10, 20, 30]
    let rev = orig.reversed()
    for x in rev { println(x) }
    // Verify source untouched.
    for x in orig { println(x) }
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	output, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	want := "30\n20\n10\n10\n20\n30\n"
	if got := string(output); got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

// TestLLVMBackendBinaryRunsListRemoveAt covers `xs.removeAt(i) -> T`
// (abort-on-OOB). Emitter reads the element via `list_get_<kind>`
// BEFORE calling `osty_rt_list_remove_at_discard` so the captured
// value stays valid after the shift.
func TestLLVMBackendBinaryRunsListRemoveAt(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `fn main() {
    let mut xs: List<Int> = [10, 20, 30, 40]
    let r = xs.removeAt(1)
    println(r)
    for x in xs { println(x) }
    println(xs.len())
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	output, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	want := "20\n10\n30\n40\n3\n"
	if got := string(output); got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

// TestLLVMBackendBinaryRunsListIndexOf covers `xs.indexOf(x) -> Int?`
// — inline linear scan. Found case yields Some(i), not-found yields
// None. String elements route through the string-equal runtime
// helper; other scalars use plain icmp / fcmp.
func TestLLVMBackendBinaryRunsListIndexOf(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `fn main() {
    let ys: List<Int> = [5, 10, 15, 20, 25]
    println(ys.indexOf(5) ?? -1)
    println(ys.indexOf(15) ?? -1)
    println(ys.indexOf(25) ?? -1)
    println(ys.indexOf(99) ?? -1)

    let ss: List<String> = ["apple", "banana", "cherry"]
    println(ss.indexOf("banana") ?? -1)
    println(ss.indexOf("grape") ?? -1)
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	output, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	// 5@0, 15@2, 25@4, 99 missing, "banana"@1, "grape" missing.
	want := "0\n2\n4\n-1\n1\n-1\n"
	if got := string(output); got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}
