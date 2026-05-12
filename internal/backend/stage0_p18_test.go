package backend

import (
	"strings"
	"testing"
)

// TestStage0P18OrShortCircuitIfElseAggregate verifies stage0 lowers
// the 7-block "`||` short-circuit + if-else struct return" shape that
// `parseAiRepairMode`-class toolchain helpers use:
//
//	fn name(value: String) -> Result {
//	    if value == "" || value == "auto" {
//	        Result { ... }
//	    } else {
//	        Result { ... }
//	    }
//	}
func TestStage0P18OrShortCircuitIfElseAggregate(t *testing.T) {
	src := `pub struct R {
    pub mode: String,
    pub ok: Bool,
}

pub fn parseMode(value: String) -> R {
    if value == "" || value == "auto" {
        R { mode: "auto", ok: true }
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
		// left cond evaluation in entry
		"call i1 @osty_rt_strings_Equal",
		// short-circuit branch
		"br i1 %0, label %or_short.",
		// or_short block just brs to or_merge
		"or_short.",
		"or_right.",
		"or_merge.",
		// phi over the merged Bool
		"%or = phi i1 [ true, %or_short.",
		// branch to if-else arms
		"br i1 %or, label %then.",
		// final struct phi still uses %R; boxed to ptr at the ret seam.
		"%retval = phi %R",
		"store %R %retval, ptr %stage0.agg.ret.slot",
		"ret ptr %stage0.agg.ret.slot",
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("stage0 output missing %q\n--- output ---\n%s", want, got)
		}
	}
}

// Tuple variant — same `||` short-circuit but tuple return instead of
// struct.
func TestStage0P18OrShortCircuitTupleReturn(t *testing.T) {
	src := `pub fn classify(value: String) -> (String, Bool) {
    if value == "" || value == "auto" {
        ("auto", true)
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
		"%or = phi i1",
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
