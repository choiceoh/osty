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

func TestNoElseIfBeforeSignedTailExpression(t *testing.T) {
	file := parseLLVMGenFile(t, `fn first(idx: Int) -> Int {
    if idx < 0 {
        return -1
    }
    -1
}

fn second(idx: Int) -> Int {
    if idx < 0 {
        return -1
    }
    +1
}
`)

	_, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/if_no_else_signed_tail.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
}
