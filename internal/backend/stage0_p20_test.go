package backend

import (
	"strings"
	"testing"
)

// TestStage0P20OrPlusElseIfChain — `parseAiRepairMode` shape:
// `||` head + N-arm else-if chain + final fallback.
func TestStage0P20OrPlusElseIfChain(t *testing.T) {
	src := `pub struct R {
    pub mode: String,
    pub ok: Bool,
}

pub fn parseMode(value: String) -> R {
    if value == "" || value == "auto" {
        R { mode: "auto", ok: true }
    } else if value == "rewrite" {
        R { mode: "rewrite", ok: true }
    } else if value == "parse" {
        R { mode: "parse", ok: true }
    } else if value == "frontend" {
        R { mode: "frontend", ok: true }
    } else {
        R { mode: "", ok: false }
    }
}

fn main() {}
`
	out, err := realStage0Run(t, src)
	if err != nil {
		t.Fatalf("stage0 declined: %v", err)
	}
	got := string(out)
	wants := []string{
		"%R = type { ptr, i1 }",
		// Named struct return → heap-pointer ABI.
		"define ptr @parseMode(ptr %value) {",
		// `||` head
		"or_short.",
		"or_right.",
		"or_merge.",
		"%or = phi i1",
		// 4 rungs (the `||`-merged + 3 else-if rungs)
		"rung1.",
		"rung2.",
		"rung3.",
		// 5 arms (arm0 from `||`, 3 else-if arms, fallback)
		"arm0.",
		"arm1.",
		"arm2.",
		"arm3.",
		"fallback.",
		// 5-incoming phi (still %R-typed; boxed to ptr at the ret seam).
		"%retval = phi %R [",
		"store %R %retval, ptr %stage0.agg.ret.slot",
		"ret ptr %stage0.agg.ret.slot",
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("stage0 output missing %q\n--- output ---\n%s", want, got)
		}
	}
}

// `||` head + 1 else-if + final-else (smallest combo).
func TestStage0P20OrHeadOneArm(t *testing.T) {
	src := `pub struct R {
    pub mode: String,
    pub ok: Bool,
}

pub fn parseMode(value: String) -> R {
    if value == "" || value == "auto" {
        R { mode: "auto", ok: true }
    } else if value == "rewrite" {
        R { mode: "rewrite", ok: true }
    } else {
        R { mode: "", ok: false }
    }
}

fn main() {}
`
	out, err := realStage0Run(t, src)
	if err != nil {
		t.Fatalf("stage0 declined: %v", err)
	}
	got := string(out)
	wants := []string{
		"%R = type { ptr, i1 }",
		"%or = phi i1",
		// 2 rungs (or-merged + 1 else-if), 3 arms
		"rung1.",
		"arm0.",
		"arm1.",
		"fallback.",
		"%retval = phi %R [",
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("stage0 output missing %q\n--- output ---\n%s", want, got)
		}
	}
}
