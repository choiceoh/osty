package llvmgen

import (
	"strings"
	"testing"
)

// TestRangeExprValuePositionLowers locks the fix for the
// value-position `RangeExpr` wall that used to stop the merged
// toolchain probe at LLVM013. The exact shape mirrors the probe's
// motivating formatter case: an `if` expression on the left of `..`.
func TestRangeExprValuePositionLowers(t *testing.T) {
	file := parseLLVMGenFile(t, `fn build(ok: Bool) -> Range<Int> {
    if ok { 0 } else { 1 }..10
}

fn main() {
    let _ = build(true)
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/range_expr_value.osty"})
	if err != nil {
		t.Fatalf("range value lowering errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"%Range.i64 = type { i64, i64, i1, i1, i1 }",
		"insertvalue %Range.i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}
