package backend

import (
	"context"
	"os/exec"
	"testing"
)

// TestLLVMBackendBinaryRunsListSafeGetOption covers the
// `list.get(i) -> T?` ABI mismatch: the MIR `IntrinsicListGet` is
// documented as "Dest is T" (matching `list[i]` indexing, aborts on
// OOB) but `list.get(i)` returns `T?` and expects the Option
// aggregate. The emitter was calling `osty_rt_list_get_<kind>`
// (returns raw T) and storing the raw scalar into an `%Option.T`
// slot — clang rejected with:
//
//   '%tN' defined with type 'i64' but expected '%Option.i64'
//
// Fix detects `i.Dest` typed as OptionalType and routes through a
// bounds-guarded len-check + Some/None wrap (same pattern as
// emitListIntrinsic for First/Last in #783). Scalar elements widen
// to i64 before insertvalue to match the fixed Option slot width.
//
// OOB semantics: both negative idx and idx >= len yield None (matches
// the stdlib spec for `.get`, distinct from `list[i]` which aborts).
func TestLLVMBackendBinaryRunsListSafeGetOption(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `fn main() {
    let xs: List<Int> = [10, 20, 30]
    println(xs.get(0) ?? -1)
    println(xs.get(2) ?? -1)
    println(xs.get(5) ?? -99)
    println(xs.get(-1) ?? -99)

    let strs: List<String> = ["a", "b", "c"]
    println(strs.get(0) ?? "none")
    println(strs.get(9) ?? "none")
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
	want := "10\n30\n-99\n-99\na\nnone\n"
	if got := string(output); got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

// TestLLVMBackendBinaryRunsListSafeGetMatch exercises the match-arm
// form of the same pattern — ensures the Option aggregate produced
// by safe-get flows through the match-scrutinee binding path and
// the Some/None arms type-check cleanly.
func TestLLVMBackendBinaryRunsListSafeGetMatch(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `fn main() {
    let xs: List<Int> = [10, 20, 30]
    match xs.get(1) {
        Some(v) -> println(v),
        None -> println(-1),
    }
    match xs.get(99) {
        Some(v) -> println(v),
        None -> println(-1),
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
	if got, want := string(output), "20\n-1\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}
