package backend

import (
	"strings"
	"testing"
)

// TestStage0P21DirectAggregateCallSingleParam — simplest P21 shape:
// `fn wrap(a: String) -> R { innerFn(a) }` where innerFn is defined in
// the same module and returns a struct value.
func TestStage0P21DirectAggregateCallSingleParam(t *testing.T) {
	src := `pub struct R {
    pub mode: String,
    pub ok: Bool,
}

pub fn innerFn(a: String) -> R {
    R { mode: a, ok: true }
}

pub fn wrap(a: String) -> R {
    innerFn(a)
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
		"define %R @wrap(ptr %a) {",
		"entry:",
		"%0 = call %R @innerFn(ptr %a)",
		"ret %R %0",
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("stage0 output missing %q\n--- output ---\n%s", want, got)
		}
	}
}

// TestStage0P21DirectAggregateCallTwoParams — P21 with two scalar params
// of different types (String + Bool).
func TestStage0P21DirectAggregateCallTwoParams(t *testing.T) {
	src := `pub struct R {
    pub mode: String,
    pub ok: Bool,
}

pub fn innerFn(a: String, b: Bool) -> R {
    R { mode: a, ok: b }
}

pub fn wrap(a: String, b: Bool) -> R {
    innerFn(a, b)
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
		"define %R @wrap(ptr %a, i1 %b) {",
		"%0 = call %R @innerFn(ptr %a, i1 %b)",
		"ret %R %0",
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("stage0 output missing %q\n--- output ---\n%s", want, got)
		}
	}
}

// TestStage0P21DirectAggregateCallThreeParams — P21 with three params
// (Int + String + Bool), verifying the multi-param path.
func TestStage0P21DirectAggregateCallThreeParams(t *testing.T) {
	src := `pub struct Config {
    pub n: Int,
    pub name: String,
    pub active: Bool,
}

pub fn makeConfig(n: Int, name: String, active: Bool) -> Config {
    Config { n: n, name: name, active: active }
}

pub fn wrap(n: Int, name: String, active: Bool) -> Config {
    makeConfig(n, name, active)
}

fn main() {}
`
	out, err := realStage0Run(t, src)
	if err != nil {
		t.Fatalf("stage0 declined: %v", err)
	}
	got := string(out)
	wants := []string{
		"%Config = type { i64, ptr, i1 }",
		"define %Config @wrap(i64 %n, ptr %name, i1 %active) {",
		"%0 = call %Config @makeConfig(i64 %n, ptr %name, i1 %active)",
		"ret %Config %0",
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("stage0 output missing %q\n--- output ---\n%s", want, got)
		}
	}
}

// TestStage0P21DirectAggregateCallTupleReturn — P21 with a tuple return type.
func TestStage0P21DirectAggregateCallTupleReturn(t *testing.T) {
	src := `pub fn makePair(a: Int, b: Bool) -> (Int, Bool) {
    (a, b)
}

pub fn wrap(x: Int, y: Bool) -> (Int, Bool) {
    makePair(x, y)
}

fn main() {}
`
	out, err := realStage0Run(t, src)
	if err != nil {
		t.Fatalf("stage0 declined: %v", err)
	}
	got := string(out)
	wants := []string{
		"= type { i64, i1 }",
		"define %",
		"@wrap(i64 %x, i1 %y) {",
		"@makePair(i64 %x, i1 %y)",
		"ret %",
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("stage0 output missing %q\n--- output ---\n%s", want, got)
		}
	}
}

// TestStage0P21DirectAggregateCallZeroParams — degenerate: 0 params, just a
// forwarding wrapper.
func TestStage0P21DirectAggregateCallZeroParams(t *testing.T) {
	src := `pub struct R {
    pub mode: String,
    pub ok: Bool,
}

pub fn defaultR() -> R {
    R { mode: "default", ok: true }
}

pub fn wrap() -> R {
    defaultR()
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
		"define %R @wrap() {",
		"%0 = call %R @defaultR()",
		"ret %R %0",
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("stage0 output missing %q\n--- output ---\n%s", want, got)
		}
	}
}
