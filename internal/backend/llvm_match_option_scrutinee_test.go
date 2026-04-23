package backend

import (
	"context"
	"os/exec"
	"testing"
)

// TestLLVMBackendBinaryRunsMatchOptionScrutineeBindsPayload covers a
// subtle MIR projection bug where `match <scrut>: T? { Some(v) -> ... }`
// typed the `v` binding as `T?` instead of `T`. The failure surfaced
// downstream as "println of non-primitive Int?" because `v` flowed
// into `println` with the wrong LLVM aggregate type.
//
// Root cause: `isScalarPayload` classified `*ir.OptionalType` as
// scalar, so the short-circuit in `variantLookupPayloadType`
// (`isScalarPayload(scrutT) && idx == 0 → return scrutT`) fired on
// the FIRST ProjVariant(Some) against an `Int?` scrutinee — before
// the variant-container → payload narrowing could run. The bind then
// inherited the unnarrowed Option<Int> type.
//
// The short-circuit's original purpose is "ProjVariantN(0) after
// ProjVariant already narrowed to a scalar is redundant" — that
// semantic only applies when the value is genuinely scalar-leaf
// (PrimType / FnType), not when it's still a variant container like
// OptionalType. Removing OptionalType from the classifier lets
// projectionResultType / projectionToPlace correctly narrow through
// the Some-variant step.
//
// Patterns covered:
//   - List<Int>.first() scrutinee → Some(v: Int)
//   - empty list → None arm reached
//   - List<String>.last() → Some(s: String) with String payload
func TestLLVMBackendBinaryRunsMatchOptionScrutineeBindsPayload(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `fn main() {
    let xs: List<Int> = [10, 20, 30]
    match xs.first() {
        Some(v) -> println(v),
        None -> println(-1),
    }

    let empty: List<Int> = []
    match empty.first() {
        Some(v) -> println(v),
        None -> println(-999),
    }

    let strs: List<String> = ["hello", "world"]
    match strs.last() {
        Some(s) -> println(s),
        None -> println("empty"),
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
	if got, want := string(output), "10\n-999\nworld\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}
