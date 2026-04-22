package llvmgen

import (
	"strings"
	"testing"
)

func TestGenerateExprStmtBoolLiteralIsIgnored(t *testing.T) {
	file := parseLLVMGenFile(t, `fn main() {
    true
    println(1)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/expr_stmt_bool_literal.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	if got := string(ir); !strings.Contains(got, "i64 1") {
		t.Fatalf("generated IR missing println payload:\n%s", got)
	}
}
