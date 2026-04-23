package backend

import (
	"context"
	"os/exec"
	"testing"
)

// TestLLVMBackendBinaryRunsListFirstLastInt covers List<Int>.first() /
// .last() which were parsed + checked but the MIR→LLVM emitter simply
// didn't have cases for IntrinsicListFirst / IntrinsicListLast — the
// `isSupportedIntrinsic` gate also filtered them out, so `xs.first()`
// on any program fell back to the legacy AST path and tripped
// LLVM015 "call: call target *ast.TurbofishExpr".
//
// The fix materialises them as `len == 0 ? None : Some(get(idx))` on
// the typed-runtime list (i64/i1/f64/ptr/string). The Option slot-1
// is i64-wide by layout, so non-i64 scalars widen via zext /
// ptrtoint / bitcast before insertvalue.
func TestLLVMBackendBinaryRunsListFirstLastInt(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `fn main() {
    let xs: List<Int> = [10, 20, 30]
    println(xs.first() ?? -1)
    println(xs.last() ?? -1)
    let empty: List<Int> = []
    println(empty.first() ?? 99)
    println(empty.last() ?? 99)
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
	if got, want := string(output), "10\n30\n99\n99\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

// TestLLVMBackendBinaryRunsListFirstLastString covers the String /
// ptr-slot case where the Option<String> aggregate's second field is
// still i64 by layout, so the emitted insertvalue must ptrtoint the
// runtime get result to i64 before inserting.
func TestLLVMBackendBinaryRunsListFirstLastString(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `fn main() {
    let strs: List<String> = ["hello", "world"]
    println(strs.first() ?? "empty")
    println(strs.last() ?? "empty")
    let empty: List<String> = []
    println(empty.first() ?? "none")
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
	if got, want := string(output), "hello\nworld\nnone\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}
