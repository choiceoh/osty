package backend

import (
	"context"
	"os/exec"
	"testing"
)

// TestLLVMBackendBinaryRunsCollectionToString covers the
// `List<T>.toString()` / `Set<T>.toString()` / `Map<K, V>.toString()`
// method-call return-type recovery that was missing from
// `builtinMethodReturnType` in `internal/mir/lower.go`.
//
// Before the fix, the receiver type recovery for these three builtins
// only listed `len` / `isEmpty` / `contains` / `containsKey`; calling
// `.toString()` fell through, the local was typed `<error>` /
// fallback-Int, and the lir_proto path silently:
//
//   - emitted a `ptrtoint` on the ptr-returning `osty_rt_<x>_to_string`
//     result,
//   - stored the resulting i64 into a slot the println intrinsic
//     dispatch then routed to `osty_rt_int_to_string`,
//   - producing the pointer address as decimal text instead of the
//     intended `{1, 2, 3}` / `[a, b]` shape.
//
// The bug was completely hidden by the MIR JSON wire-format
// off-by-one (PR #1894): with the wrong intrinsic dispatch,
// `IntrinsicSetToString` decoded as something else entirely and the
// observed runtime crash was `runtime.set.to_string: unsupported
// element kind` — the wrong symptom for the wrong root cause.
//
// This regression test invokes all three `.toString()` paths end-to-end
// (build → link → execute) and asserts the exact stdout shape so any
// future drop of the toString return-type registration trips
// immediately rather than going months without anyone noticing the
// pointer-as-integer output.
func TestLLVMBackendBinaryRunsCollectionToString(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `fn main() {
    let xs: List<Int> = [1, 2, 3]
    println(xs.toString())
    let s = xs.toSet()
    println(s.toString())
    let m: Map<String, Int> = {:}
    println(m.toString())
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
	if got, want := string(output), "[1, 2, 3]\n{1, 2, 3}\n{}\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}
