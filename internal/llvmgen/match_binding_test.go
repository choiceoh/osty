package llvmgen

import (
	"strings"
	"testing"
)

func TestGenerateTagEnumBindingMatchExpression(t *testing.T) {
	file := parseLLVMGenFile(t, `enum Kind {
    Ready,
    Waiting,
}

fn keep(kind: Kind) -> Kind {
    match kind {
        matched -> matched,
    }
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/match_binding_tag.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"define i64 @keep(i64 %kind)",
		"ret i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGeneratePrimitiveLiteralAndBindingMatchExpression(t *testing.T) {
	file := parseLLVMGenFile(t, `fn classify(n: Int) -> Int {
    match n {
        0 -> 0,
        rest -> rest,
    }
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/match_binding_primitive.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"define i64 @classify(i64 %n)",
		"icmp eq i64 %n, 0",
		"phi i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}
