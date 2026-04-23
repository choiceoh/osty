package backend

import (
	"context"
	"os/exec"
	"testing"
)

// TestLLVMBackendBinaryRunsListPopReturnsRealValue covers the MIR
// emitter bug where `xs.pop()` — which the stdlib declares as
// `-> T?` — silently returned a zero-initialised `Option<T>` (i.e.
// always `None`) and called `osty_rt_list_pop_discard` separately.
// The MIR IntrinsicListPop comment admitted the shortcut: "A future
// typed-pop runtime family can replace this with real Some(T)
// returns."
//
// This PR implements the real `Some(T)` return by computing
// `list[len-1]` via `osty_rt_list_get_<kind>` BEFORE the discard,
// wrapping in `Some` for the non-empty branch and `None` for the
// empty branch. The dest-less form (`xs.pop()` as a statement, or
// `let _ = xs.pop()`) still routes through the raw `pop_discard`
// call — matching the existing AST-path behaviour exercised by
// TestGenerateDiscardedListPopNoLongerTripsLLVM015.
func TestLLVMBackendBinaryRunsListPopReturnsRealValue(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `fn main() {
    let mut xs: List<Int> = [1, 2, 3, 4, 5]
    match xs.pop() {
        Some(v) -> println(v),
        None -> println(-1),
    }
    println(xs.pop() ?? -1)
    println(xs.len())

    let mut empty: List<Int> = []
    match empty.pop() {
        Some(v) -> println(v),
        None -> println(-99),
    }
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
	// Drain order: 5 (from [1..5]), then 4, leaving len 3. Empty list
	// pop reaches the None arm.
	if got, want := string(output), "5\n4\n3\n-99\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

// TestLLVMBackendBinaryRunsListPopString verifies the ptr-slot widening
// path — Option<String> stores the popped pointer as i64 through
// ptrtoint, matching the layout convention used by List.first/last and
// the new List.get safe-get path.
func TestLLVMBackendBinaryRunsListPopString(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `fn main() {
    let mut strs: List<String> = ["a", "b", "c"]
    println(strs.pop() ?? "none")
    println(strs.pop() ?? "none")
    println(strs.pop() ?? "none")
    println(strs.pop() ?? "none")
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
	if got, want := string(output), "c\nb\na\nnone\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}
