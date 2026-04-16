package llvmgen

import (
	"strings"
	"testing"
)

func TestGenerateNestedReturnInIfStmt(t *testing.T) {
	file := parseLLVMGenFile(t, `fn choose(ok: Bool) -> Int {
    if ok {
        return 42
    }
    0
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/nested_return_if.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"define i64 @choose(i1 %ok)",
		"ret i64 42",
		"ret i64 0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateNestedReturnInLoopBody(t *testing.T) {
	file := parseLLVMGenFile(t, `fn find(limit: Int) -> Int {
    for i in 0..limit {
        if i == 3 {
            return 42
        }
    }
    0
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/nested_return_loop.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"for.cond",
		"for.body",
		"for.end",
		"ret i64 42",
		"ret i64 0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateIfBothBranchesReturnVoid(t *testing.T) {
	file := parseLLVMGenFile(t, `fn stop(ok: Bool) {
    if ok {
        return
    } else {
        return
    }
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/return_void_branches.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	if gotCount := strings.Count(got, "ret void"); gotCount != 2 {
		t.Fatalf("generated IR ret void count = %d, want 2:\n%s", gotCount, got)
	}
}
