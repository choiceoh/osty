package llvmgen

import (
	"strings"
	"testing"
)

func TestGenerateWhileStyleForLoop(t *testing.T) {
	file := parseLLVMGenFile(t, `fn main() {
    let mut i = 0
    let mut sum = 0
    for i < 4 {
        sum = sum + i
        i = i + 1
    }
    println(sum)
}
`)

	ir, err := Generate(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/while_for.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"for.cond",
		"for.body",
		"for.end",
		"icmp slt i64",
		"br i1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateBareForLoop(t *testing.T) {
	file := parseLLVMGenFile(t, `fn main() {
    let mut count = 0
    for {
        count = count + 1
        println(count)
    }
}
`)

	ir, err := Generate(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/bare_for.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"for.cond",
		"for.body",
		"for.end",
		"br label %for.body",
		"br label %for.cond",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}
