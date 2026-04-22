package llvmgen

import (
	"strings"
	"testing"
)

func TestRangeExprInferredLetCompiles(t *testing.T) {
	file := parseLLVMGenFile(t, `fn main() {
    let r = 0..10
    let _ = r
}
`)
	if _, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/range_expr_let.osty"}); err != nil {
		t.Fatalf("inferred range let errored: %v", err)
	}
}

func TestRangeExprFieldAccessLowers(t *testing.T) {
	file := parseLLVMGenFile(t, `fn main() {
    let r = 0..10
    println(r.hasStart)
    println(r.start)
    println(r.inclusive)
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/range_expr_fields.osty"})
	if err != nil {
		t.Fatalf("range field access errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"extractvalue %Range.i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}
