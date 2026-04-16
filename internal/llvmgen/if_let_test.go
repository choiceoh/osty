package llvmgen

import (
	"strings"
	"testing"
)

func TestGenerateIfLetPayloadEnumStmt(t *testing.T) {
	file := parseLLVMGenFile(t, `enum Maybe {
    Some(Int)
    None
}

fn main() {
    let value: Maybe = Some(42)
    if let Some(x) = value {
        println(x)
    } else {
        println(0)
    }
}
`)

	ir, err := Generate(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/if_let_payload_stmt.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"%Maybe = type { i64, i64 }",
		"extractvalue %Maybe",
		"icmp eq i64",
		"call i32 (ptr, ...) @printf",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateIfLetPayloadEnumExpr(t *testing.T) {
	file := parseLLVMGenFile(t, `enum Maybe {
    Some(Int)
    None
}

fn score(value: Maybe) -> Int {
    if let Some(x) = value {
        x
    } else {
        0
    }
}
`)

	ir, err := Generate(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/if_let_payload_expr.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"define i64 @score(%Maybe %value)",
		"extractvalue %Maybe",
		"phi i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateIfLetQualifiedVariantForms(t *testing.T) {
	file := parseLLVMGenFile(t, `enum Maybe {
    Some(Int)
    None
}

fn main() {
    let value: Maybe = Maybe.Some(42)
    if let Maybe.Some(x) = value {
        println(x)
    } else {
        println(0)
    }
}
`)

	ir, err := Generate(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/if_let_qualified_variant.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"%Maybe = type { i64, i64 }",
		"insertvalue %Maybe",
		"extractvalue %Maybe",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}
