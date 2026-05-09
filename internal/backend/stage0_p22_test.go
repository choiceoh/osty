package backend

import (
	"strings"
	"testing"
)

// TestStage0P22ListClone — canonical hirCloneStringList shape:
// `fn clone(src: List<String>) -> List<String>` — for-in-list that
// copies each element into a fresh output list.
func TestStage0P22ListClone(t *testing.T) {
	src := `fn cloneStrings(src: List<String>) -> List<String> {
    let mut out: List<String> = []
    for s in src {
        out.push(s)
    }
    out
}

fn main() {}
`
	out, err := realStage0Run(t, src)
	if err != nil {
		dumpMIRGap(t, src, err)
		t.Fatalf("stage0 declined: %v", err)
	}
	got := string(out)
	wants := []string{
		"declare ptr @osty_rt_list_new()",
		"declare i64 @osty_rt_list_len(ptr)",
		"declare void @osty_rt_list_push_string(ptr, ptr)",
		"define ptr @cloneStrings(ptr %src)",
		"call ptr @osty_rt_list_new()",
		"call i64 @osty_rt_list_len(",
		"icmp slt i64",
		"@osty_rt_list_get_string(",
		"@osty_rt_list_push_string(",
		"ret ptr",
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("stage0 output missing %q\n--- output ---\n%s", want, got)
		}
	}
}

// TestStage0P22IntCounter — unconditional accumulation: `n = n + len(elem)`
// where the body never branches. This keeps the 5-block CFG that P22 covers.
func TestStage0P22IntCounter(t *testing.T) {
	src := `fn totalLen(xs: List<String>) -> Int {
    let mut n = 0
    for s in xs {
        n = n + s.len()
    }
    n
}

fn main() {}
`
	out, err := realStage0Run(t, src)
	if err != nil {
		dumpMIRGap(t, src, err)
		t.Fatalf("stage0 declined: %v", err)
	}
	got := string(out)
	wants := []string{
		"define i64 @totalLen(ptr %xs)",
		"call i64 @osty_rt_list_len(",
		"alloca i64",
		"store i64 0,",
		"load i64,",
		"icmp slt i64",
		"@osty_rt_list_get_string(",
		"add i64",
		"ret i64",
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("stage0 output missing %q\n--- output ---\n%s", want, got)
		}
	}
}

// TestStage0P22SumAccumulator — accumulator pattern: sum all elements.
func TestStage0P22SumAccumulator(t *testing.T) {
	src := `fn sumList(xs: List<Int>) -> Int {
    let mut total = 0
    for x in xs {
        total = total + x
    }
    total
}

fn main() {}
`
	out, err := realStage0Run(t, src)
	if err != nil {
		dumpMIRGap(t, src, err)
		t.Fatalf("stage0 declined: %v", err)
	}
	got := string(out)
	wants := []string{
		"define i64 @sumList(ptr %xs)",
		"call i64 @osty_rt_list_len(",
		"alloca i64",
		"@osty_rt_list_get_i64(",
		"add i64",
		"ret i64",
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("stage0 output missing %q\n--- output ---\n%s", want, got)
		}
	}
}

// TestStage0P22IntListClone — clone List<Int>.
func TestStage0P22IntListClone(t *testing.T) {
	src := `fn cloneInts(src: List<Int>) -> List<Int> {
    let mut out: List<Int> = []
    for x in src {
        out.push(x)
    }
    out
}

fn main() {}
`
	out, err := realStage0Run(t, src)
	if err != nil {
		dumpMIRGap(t, src, err)
		t.Fatalf("stage0 declined: %v", err)
	}
	got := string(out)
	wants := []string{
		"declare ptr @osty_rt_list_new()",
		"declare void @osty_rt_list_push_i64(ptr, i64)",
		"define ptr @cloneInts(ptr %src)",
		"call ptr @osty_rt_list_new()",
		"call i64 @osty_rt_list_len(",
		"@osty_rt_list_get_i64(",
		"@osty_rt_list_push_i64(",
		"ret ptr",
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("stage0 output missing %q\n--- output ---\n%s", want, got)
		}
	}
}

// TestStage0P22StringLenSum — body uses a value-returning intrinsic
// (string byte-length) to accumulate. Verifies that P22 handles
// IntrinsicInstr with a non-nil Dest inside the loop body.
func TestStage0P22StringLenSum(t *testing.T) {
	src := `fn totalLen(xs: List<String>) -> Int {
    let mut n = 0
    for s in xs {
        n = n + s.len()
    }
    n
}

fn main() {}
`
	out, err := realStage0Run(t, src)
	if err != nil {
		dumpMIRGap(t, src, err)
		t.Fatalf("stage0 declined: %v", err)
	}
	got := string(out)
	wants := []string{
		"define i64 @totalLen(ptr %xs)",
		"call i64 @osty_rt_list_len(",
		"alloca i64",
		"@osty_rt_list_get_string(",
		"add i64",
		"ret i64",
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("stage0 output missing %q\n--- output ---\n%s", want, got)
		}
	}
}
