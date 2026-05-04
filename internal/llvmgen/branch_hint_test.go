package llvmgen

import (
	"strings"
	"testing"
)

// TestBranchHintBuiltinsLowerToLLVMExpect pins the SPEC_GAPS
// `a12-branch-hints` fix: the prelude `likely(cond)` / `unlikely(cond)`
// builtins must lower to `call i1 @llvm.expect.i1(i1 %cond, i1 <hint>)`
// so LLVM biases block placement after the next branch. Runtime
// semantics are identity — the hint affects layout, not the value.
func TestBranchHintBuiltinsLowerToLLVMExpect(t *testing.T) {
	src := `fn main() {
    let n = 5
    if likely(n > 0) {
        println("positive")
    }
    if unlikely(n < 0) {
        println("negative")
    }
}
`
	mod := lowerSrcLLVM(t, src)
	ir, err := GenerateModule(mod, Options{PackageName: "main", SourcePath: "/tmp/branch_hint.osty"})
	if err != nil {
		t.Fatalf("GenerateModule: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"declare i1 @llvm.expect.i1(i1, i1)",
		"call i1 @llvm.expect.i1(i1 %",
		"i1 true)",  // likely's expected
		"i1 false)", // unlikely's expected
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("branch-hint IR missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "@likely(") || strings.Contains(got, "@unlikely(") {
		t.Fatalf("likely/unlikely leaked as undefined external:\n%s", got)
	}
}
