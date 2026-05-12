package backend

import (
	"strings"
	"testing"
)

// TestStage0P19TwoArmElseIfChain — the simplest else-if shape:
// `if A { S } else if B { S } else { S }`.
func TestStage0P19TwoArmElseIfChain(t *testing.T) {
	src := `pub struct R {
    pub mode: String,
    pub ok: Bool,
}

pub fn parseMode(value: String) -> R {
    if value == "auto" {
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
		// Named struct return → heap-pointer ABI.
		"define ptr @parseMode(ptr %value) {",
		// Two String== runtime calls + one phi over 3 arms.
		"call i1 @osty_rt_strings_Equal",
		// First rung branches to its arm or the second rung's cond block.
		"entry:",
		"rung1.",
		// Three arm labels (arm0, arm1, fallback).
		"arm0.",
		"arm1.",
		"fallback.",
		// 3-incoming phi (still %R-typed; boxed to ptr at the ret seam).
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

// 3-arm else-if chain — `if A {} else if B {} else if C {} else {}`.
func TestStage0P19ThreeArmElseIfChain(t *testing.T) {
	src := `pub struct R {
    pub mode: String,
    pub ok: Bool,
}

pub fn parseMode(value: String) -> R {
    if value == "auto" {
        R { mode: "auto", ok: true }
    } else if value == "rewrite" {
        R { mode: "rewrite", ok: true }
    } else if value == "parse" {
        R { mode: "parse", ok: true }
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
		// 4-incoming phi (3 arms + final else).
		"%retval = phi %R [",
		"arm0.",
		"arm1.",
		"arm2.",
		"fallback.",
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("stage0 output missing %q\n--- output ---\n%s", want, got)
		}
	}
}

// Tuple variant of 2-arm else-if.
func TestStage0P19TupleReturnElseIfChain(t *testing.T) {
	src := `pub fn classify(value: String) -> (String, Bool) {
    if value == "yes" {
        ("yes", true)
    } else if value == "no" {
        ("no", false)
    } else {
        ("", false)
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
		"= type { ptr, i1 }",
		"@classify(ptr %value)",
		"insertvalue",
		"%retval = phi",
		"ret",
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("stage0 output missing %q\n--- output ---\n%s", want, got)
		}
	}
}
