package backend

import (
	"context"
	"os/exec"
	"testing"
)

// TestLLVMBackendBinaryRunsClosureCoalesceInferredReturn covers the
// canonical `map.update(k, |n| (n ?? 0) + 1)` pattern from
// CLAUDE.md §B.9.1 which was silently blocked at the native checker:
//
//   - `tokenToBinOp(FrontQQ)` returned `BoInvalid` (no matching variant)
//   - `binOpIsCoalesce(BoInvalid)` returned false (stub)
//   - `elabInferBinary` fell into the default path with `op=BoInvalid`
//   - `binOpResultType(BoInvalid, ...)` returned `tErr`
//   - Closure inferred return type became `<error>`, tripping LLVM011
//     "function return type: type `<error>`" at backend emit
//
// The fix routes `FrontQQ` directly to `elabCoalesce` regardless of
// `tokenToBinOp` (which has no BinOp variant for `?? `), and
// `elabCoalesce` now also accepts `TkOptional` receivers (the shape
// `|n: Int?|` produces) in addition to the `TkNamed "Option"` shape.
func TestLLVMBackendBinaryRunsClosureCoalesceInferredReturn(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `fn main() {
    let f = |n: Int?| n ?? 0
    println(f(Some(5)))
    println(f(None))
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
	if got, want := string(output), "5\n0\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

// TestLLVMBackendBinaryRunsMapUpdateCanonicalPattern exercises the
// CLAUDE.md §B.9.1 canonical counting pattern end-to-end:
//
//	counts.update(word, |n: Int?| (n ?? 0) + 1)
//
// This needs the closure return type to infer through `(n ?? 0) + 1`
// as `Int` — the same coalesce-in-closure-body fix the previous test
// covers, but additionally driving Map.update's closure-taking
// combinator path all the way through monomorph + codegen.
func TestLLVMBackendBinaryRunsMapUpdateCanonicalPattern(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `fn main() {
    let mut counts: Map<String, Int> = {:}
    for word in ["the", "quick", "brown", "fox", "the", "quick"] {
        counts.update(word, |n: Int?| (n ?? 0) + 1)
    }
    println(counts.len())
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
	if got, want := string(output), "4\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}
