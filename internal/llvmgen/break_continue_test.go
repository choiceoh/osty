package llvmgen

import (
	"strings"
	"testing"
)

func TestGenerateRangeLoopBreakAndContinue(t *testing.T) {
	file := parseLLVMGenFile(t, `fn main() {
    for i in 0..6 {
        if i == 1 {
            continue
        }
        if i == 4 {
            break
        }
        println(i)
    }
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/range_break_continue.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"for.cont",
		"for.end",
		"br label %for.cont",
		"br label %for.end",
		"icmp eq i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateWhileLoopBreakAndContinue(t *testing.T) {
	file := parseLLVMGenFile(t, `fn main() {
    let mut i = 0
    for i < 6 {
        i = i + 1
        if i == 2 {
            continue
        }
        if i == 5 {
            break
        }
        println(i)
    }
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/while_break_continue.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"for.cond",
		"for.cont",
		"for.end",
		"br label %for.cont",
		"br label %for.end",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}
