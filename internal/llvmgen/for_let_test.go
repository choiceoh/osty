package llvmgen

import (
	"strings"
	"testing"
)

func TestGenerateForLetPayloadEnumLoop(t *testing.T) {
	file := parseLLVMGenFile(t, `enum Maybe {
    Some(Int)
    None
}

fn main() {
    let mut value: Maybe = Some(42)
    for let Some(x) = value {
        println(x)
        value = None
    }
}
`)

	ir, err := Generate(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/for_let_payload_loop.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"%Maybe = type { i64, i64 }",
		"for.cond",
		"for.body",
		"for.end",
		"extractvalue %Maybe",
		"store %Maybe",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateForLetQualifiedVariantLoop(t *testing.T) {
	file := parseLLVMGenFile(t, `enum Maybe {
    Some(Int)
    None
}

fn main() {
    let mut value: Maybe = Maybe.Some(42)
    for let Maybe.Some(x) = value {
        println(x)
        value = Maybe.None
    }
}
`)

	ir, err := Generate(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/for_let_qualified_loop.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"%Maybe = type { i64, i64 }",
		"insertvalue %Maybe",
		"extractvalue %Maybe",
		"br i1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}
