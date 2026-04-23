package backend

import (
	"context"
	"os/exec"
	"testing"
)

// TestLLVMBackendBinaryRunsBareNoneAsCallArg covers a MIR lowering gap
// for the bare `None` identifier in expression position. The resolver
// tags `None` as `SymBuiltin` (see internal/resolve/prelude.go:56),
// which HIR then lifts to `IdentBuiltin`. The MIR `lowerIdent` switch
// only handled `IdentFn` / `IdentGlobal` / `IdentVariant`, so
// `IdentBuiltin` fell through to the "unknown identifier" fallback
// that emits a `FnConst{Symbol: "None"}`. At LLVM level that renders
// as `@None` — a reference to an undeclared global, failing with
// "global variable reference must have pointer type" at clang link
// time.
//
// The fix materialises `None` as a NullaryRV with the ident's
// checker-resolved type, matching the existing `?` propagation path
// for Option<T> error arms (see emitQuestionExprOption in lower.go).
func TestLLVMBackendBinaryRunsBareNoneAsCallArg(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `fn plus(n: Int?) -> Int { n ?? -1 }

fn main() {
    println(plus(None))
    println(plus(Some(5)))
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
	if got, want := string(output), "-1\n5\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

// TestLLVMBackendBinaryRunsBareNoneInListLiteral makes sure the same
// fix works for `None` inside a typed `List<Int?>` literal — another
// position where the checker seeds id.T from the list element type
// and the MIR emitter then asks lowerIdent to produce a proper
// Option aggregate.
func TestLLVMBackendBinaryRunsBareNoneInListLiteral(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `fn main() {
    let xs: List<Int?> = [None, Some(10), None, Some(20)]
    let mut sum = 0
    for x in xs {
        sum = sum + (x ?? 0)
    }
    println(sum)
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
	if got, want := string(output), "30\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}
