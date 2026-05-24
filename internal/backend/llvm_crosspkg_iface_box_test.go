package backend

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

// TestLLVMBackendBinaryCrossPkgInterfaceBoxingErrConstruct locks in
// the cross-pkg interface boxing fix from PR #2007 (step 3/4 of
// cross-pkg interface support). Before that PR the body-lowered
// `error.new(msg)` body produced an assign-coercion error:
//
//	assign coercion `%error.BasicError` → `ptr` is not supported
//	(dest `_return:Error`, src `error.BasicError`)
//
// because the destination MIR type (Error, a cross-pkg interface) was
// lowered to opaque `ptr` via PR #2004's PascalCase fallback but the
// LIR Proto assign path had no `Aggregate → Ptr` branch. The fix
// GC-allocates the struct on the heap, copies the value in, and uses
// the heap pointer as the interface value — sound for the
// pattern-match-on-discriminant subset (`Err(_) -> …`).
//
// The Err arm of the match here exercises the boxed Error all the
// way through: construct via `Err(error.new(...))`, box the
// BasicError aggregate into the Error slot, embed in the
// Result<Int, Error> Err variant, return, then destructure and
// reach the Err arm via discriminant check. If any layer regresses
// (boxing dropped, dest hint not propagated, GC root not bound)
// the binary segfaults or links wrong.
//
// `err.message()` virtual dispatch is OUT of scope — that needs the
// cross-pkg vtable injection (step 3.5) which is not yet
// implemented. `Err(_)` here verifies the boxing prerequisite.
func TestLLVMBackendBinaryCrossPkgInterfaceBoxingErrConstruct(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `use std.error

fn make() -> Result<Int, Error> {
    Err(error.new("oops"))
}

fn main() {
    match make() {
        Ok(v) -> println(v),
        Err(_) -> println(-1),
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
	if got, want := strings.TrimSpace(string(output)), "-1"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}
