package llvmgen

import (
	"strings"
	"testing"
)

func TestGenerateTagEnumMultiArmMatchWithBlockBodies(t *testing.T) {
	file := parseLLVMGenFile(t, `enum Kind {
    Ready,
    Waiting,
    Done,
}

fn score(kind: Kind) -> Int {
    match kind {
        Ready -> {
            let base = 40
            base + 2
        },
        Waiting -> {
            let base = 20
            base + 1
        },
        Done -> 0,
    }
}
`)

	ir, err := Generate(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/multi_arm_block_match.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"define i64 @score(i64 %kind)",
		"if.expr.then",
		"if.expr.else",
		"phi i64",
		"add i64 40, 2",
		"add i64 20, 1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateTagEnumMultiArmMatchWithCalls(t *testing.T) {
	file := parseLLVMGenFile(t, `enum Kind {
    Ready,
    Waiting,
    Done,
}

fn ready() -> Int {
    42
}

fn waiting() -> Int {
    21
}

fn score(kind: Kind) -> Int {
    match kind {
        Ready -> ready(),
        Waiting -> waiting(),
        _ -> 0,
    }
}
`)

	ir, err := Generate(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/multi_arm_call_match.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"define i64 @ready()",
		"define i64 @waiting()",
		"call i64 @ready()",
		"call i64 @waiting()",
		"phi i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}
