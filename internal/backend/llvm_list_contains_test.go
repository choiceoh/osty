package backend

import (
	"context"
	"os/exec"
	"testing"
)

// TestLLVMBackendBinaryRunsListContains covers `xs.contains(x) -> Bool`
// — the MIR intrinsic kind existed and the stdlib sig was registered
// but the LLVM emitter had no case, so user calls fell back to the
// legacy AST path and walled on `LLVM015 TurbofishExpr`.
//
// Emit is a trimmed variant of IntrinsicListIndexOf's inline scan
// from #797: pre-store `false`, overwrite with `true` on the first
// match, skip the Some/None wrapping since the dest is plain i1
// (Bool). String elements route through `osty_rt_strings_Equal`;
// doubles use `fcmp oeq`; other scalars use `icmp eq`.
func TestLLVMBackendBinaryRunsListContains(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `fn main() {
    let xs: List<Int> = [10, 20, 30]
    println(xs.contains(20))
    println(xs.contains(99))

    let ss: List<String> = ["apple", "banana", "cherry"]
    println(ss.contains("banana"))
    println(ss.contains("grape"))

    let empty: List<Int> = []
    println(empty.contains(1))
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
	// Bool prints as 1/0 through println's integer format path.
	want := "1\n0\n1\n0\n0\n"
	if got := string(output); got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}
