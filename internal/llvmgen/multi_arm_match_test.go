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

	ir, err := generateFromAST(file, Options{
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

	ir, err := generateFromAST(file, Options{
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

func TestGeneratePayloadEnumMultiArmMatchWithBlockBodies(t *testing.T) {
	file := parseLLVMGenFile(t, `enum Maybe {
    Some(Int)
    More(Int)
    None
}

fn score(value: Maybe) -> Int {
    match value {
        Some(x) -> {
            let base = x
            base + 1
        },
        More(y) -> {
            let base = y
            base + 2
        },
        _ -> 0,
    }
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/payload_multi_arm_block_match.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"%Maybe = type { i64, i64 }",
		"define i64 @score(%Maybe %value)",
		"extractvalue %Maybe",
		"phi i64",
		"icmp eq i64",
		", 1",
		", 2",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
	if gotCount := strings.Count(got, "extractvalue %Maybe"); gotCount < 2 {
		t.Fatalf("generated IR should extract payload for multiple arms, got %d extracts:\n%s", gotCount, got)
	}
}
