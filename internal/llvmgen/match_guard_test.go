package llvmgen

import (
	"strings"
	"testing"
)

func TestGeneratePayloadEnumMatchGuards(t *testing.T) {
	file := parseLLVMGenFile(t, `enum Maybe {
    Some(Int)
    None
}

fn describe(value: Maybe) -> String {
    match value {
        Some(v) if v > 0 -> "positive",
        Some(v) if v < 0 -> "negative",
        Some(_) -> "zero",
        None -> "missing",
    }
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/match_guard_payload.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"%Maybe = type { i64, i64 }",
		"extractvalue %Maybe",
		"icmp sgt i64",
		"icmp slt i64",
		"phi ptr",
		"positive",
		"negative",
		"zero",
		"missing",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateTagEnumMatchGuards(t *testing.T) {
	file := parseLLVMGenFile(t, `enum Kind {
    Ready,
    Waiting,
}

fn choose(flag: Bool, kind: Kind) -> Int {
    match kind {
        Ready if flag -> 1,
        Ready -> 2,
        Waiting -> 3,
    }
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/match_guard_tag.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"define i64 @choose(i1 %flag, i64 %kind)",
		"icmp eq i64 %kind, 0",
		"phi i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}
