package llvmgen

import "testing"

func TestBoolLiteralExpressionStatementIsIgnored(t *testing.T) {
	file := parseLLVMGenFile(t, `fn run(flag: Bool) {
    if flag {
        true
    } else {
        false
    }
}
`)

	_, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/bool_expr_stmt.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
}
