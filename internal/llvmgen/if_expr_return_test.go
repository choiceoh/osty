package llvmgen

import "testing"

func TestIfExpressionBranchMayReturnEarly(t *testing.T) {
	file := parseLLVMGenFile(t, `fn choose(flag: Bool) -> Int {
    let x = if flag {
        return 1
    } else {
        2
    }
    x
}
`)

	_, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/if_expr_return.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
}
