package backend

import (
	"strings"
	"testing"
)

// TestStage0P17IfElseAggregateReturnStruct verifies stage0 lowers the
// 4-block if-else-with-struct-AggregateRV-arms shape produced by the
// front-end for `fn name(p) -> Struct { if cond { S{..} } else { S{..} } }`.
//
// This is the next toolchain self-build blocker after P15/P16 — many
// `parse*` helpers in `toolchain/airepair_flags.osty` and friends use
// exactly this shape.
func TestStage0P17IfElseAggregateReturnStruct(t *testing.T) {
	src := `pub struct Result {
    pub mode: String,
    pub ok: Bool,
}

pub fn parseMode(value: String) -> Result {
    if value == "auto" {
        Result { mode: "auto", ok: true }
    } else {
        Result { mode: "", ok: false }
    }
}

fn main() {}
`
	out, err := realStage0Run(t, src)
	if err != nil {
		t.Fatalf("stage0 declined: %v", err)
	}
	wants := []string{
		"%Result = type { ptr, i1 }",
		// Named struct returns go through the heap-pointer ABI (see
		// aggregateReturnIsHeapPtr). The in-register phi still builds the
		// struct value; emitAggregateValueToPtrReturn boxes it once at the
		// return seam.
		"define ptr @parseMode(",
		"call i1 @osty_rt_strings_Equal(",
		"br i1",
		"insertvalue %Result poison",
		"insertvalue %Result %",
		`%retval = phi %Result [`,
		"store %Result %retval, ptr %stage0.agg.ret.slot",
		"ret ptr %stage0.agg.ret.slot",
	}
	got := string(out)
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("stage0 output missing %q\n--- output ---\n%s", want, got)
		}
	}
}

// Tuple variant of P17 — same 4-block shape but with TupleType return.
func TestStage0P17IfElseAggregateReturnTuple(t *testing.T) {
	src := `pub fn split(value: String) -> (String, Bool) {
    if value == "yes" {
        ("yes", true)
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
		"define ",
		"@split(",
		"insertvalue",
		"phi",
		"ret",
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("stage0 output missing %q\n--- output ---\n%s", want, got)
		}
	}
}

// Param-passthrough variant: one struct field uses the function param
// directly (CopyOp) rather than a constant. Verifies classifyAggregateField
// resolves param locals from inside an if-else branch arm.
func TestStage0P17IfElseAggregateBranchUsesParam(t *testing.T) {
	src := `pub struct Pair {
    pub label: String,
    pub flag: Bool,
}

pub fn label(value: String) -> Pair {
    if value == "" {
        Pair { label: "(empty)", flag: false }
    } else {
        Pair { label: value, flag: true }
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
		"%Pair = type { ptr, i1 }",
		// Named struct return → ptr ABI.
		"define ptr @label(ptr %value)",
		// else arm should reference the param register directly
		"insertvalue %Pair poison, ptr %value, 0",
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("stage0 output missing %q\n--- output ---\n%s", want, got)
		}
	}
}
