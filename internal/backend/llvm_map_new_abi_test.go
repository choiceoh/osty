package backend

import (
	"context"
	"os/exec"
	"testing"
)

// TestLLVMBackendBinaryRunsMapLiteralInMain covers a critical ABI
// mismatch in the native-owned LLVM emitter: `osty_rt_map_new` is a
// 4-arg C function `(key_kind, value_kind, value_size, trace)` but
// llvmNativeEvalMapLit was calling it with zero args. The runtime's
// `value_size <= 0` guard then aborted with "invalid map value size"
// as soon as the call register holding x2 (value_size) happened to be
// non-positive (common on arm64 where x0..x3 carry stale caller
// values).
//
// Programs whose Map lived only inside a nested helper + more-complex
// shape fell back to the Go-side AST emitter which did pass the 4
// args; the simpler top-level cases here went through the native-owned
// fast path and tripped the abort.
//
// This test exercises the minimal Map<String, Int> shape at `fn main`
// scope so the native emitter is the one producing the IR.
func TestLLVMBackendBinaryRunsMapLiteralInMain(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `fn main() {
    let mut m: Map<String, Int> = {:}
    m.insert("a", 1)
    m.insert("b", 2)
    println(m.len())
    println(m.containsKey("a"))
    println(m.containsKey("z"))
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
	if got, want := string(output), "2\ntrue\nfalse\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}
