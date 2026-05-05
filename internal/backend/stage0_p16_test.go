package backend

import (
	"strings"
	"testing"
)

// TestStage0P16StringEqArgConst — `fn isAuto(s: String) -> Bool { s == "auto" }`
// shape. Front-end lowers to single AssignInstr BinaryRV(==,
// Cp(s), StringConst("auto")) writing to the Bool return local.
// stage0 must route the compare through the runtime ABI rather than
// emitting `icmp eq` (which would compare String pointers, not
// content).
func TestStage0P16StringEqArgConst(t *testing.T) {
	src := `fn isAuto(s: String) -> Bool { s == "auto" }
fn main() {}`
	out, err := realStage0Run(t, src)
	if err != nil {
		t.Fatalf("stage0 declined: %v", err)
	}
	wants := []string{
		"declare i1 @osty_rt_strings_Equal(ptr, ptr)",
		"define i1 @isAuto(ptr %s) {",
		"call i1 @osty_rt_strings_Equal(ptr %s, ptr",
	}
	got := string(out)
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("stage0 output missing %q\n--- output ---\n%s", want, got)
		}
	}
}

// TestStage0P16StringEqTwoArgs — both sides are String params:
// `fn eq(a: String, b: String) -> Bool { a == b }`.
func TestStage0P16StringEqTwoArgs(t *testing.T) {
	src := `fn eq(a: String, b: String) -> Bool { a == b }
fn main() {}`
	out, err := realStage0Run(t, src)
	if err != nil {
		t.Fatalf("stage0 declined: %v", err)
	}
	wants := []string{
		"declare i1 @osty_rt_strings_Equal(ptr, ptr)",
		"define i1 @eq(ptr %a, ptr %b) {",
		"call i1 @osty_rt_strings_Equal(ptr %a, ptr %b)",
	}
	got := string(out)
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("stage0 output missing %q\n--- output ---\n%s", want, got)
		}
	}
}

// TestStage0P16StringEqRuntimeDeclaredOnce verifies the runtime ABI
// declaration is emitted exactly once even when multiple functions
// use String ==.
func TestStage0P16StringEqRuntimeDeclaredOnce(t *testing.T) {
	src := `fn isFoo(s: String) -> Bool { s == "foo" }
fn isBar(s: String) -> Bool { s == "bar" }
fn main() {}`
	out, err := realStage0Run(t, src)
	if err != nil {
		t.Fatalf("stage0 declined: %v", err)
	}
	got := string(out)
	if c := strings.Count(got, "declare i1 @osty_rt_strings_Equal"); c != 1 {
		t.Errorf("declare i1 @osty_rt_strings_Equal count = %d, want 1\n--- output ---\n%s", c, got)
	}
}

// TestStage0P16IntEqStillUsesIcmp — Int == Int must continue to use
// `icmp eq` (the existing P2e path). Regression guard: the P16
// special case must not swallow scalar Int comparisons.
func TestStage0P16IntEqStillUsesIcmp(t *testing.T) {
	src := `fn isZero(n: Int) -> Bool { n == 0 }
fn main() {}`
	out, err := realStage0Run(t, src)
	if err != nil {
		t.Fatalf("stage0 declined: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "icmp eq i64") {
		t.Errorf("expected `icmp eq i64` for Int==Int (not runtime call)\n--- output ---\n%s", got)
	}
	if strings.Contains(got, "osty_rt_strings_Equal") {
		t.Errorf("Int==Int must not call String runtime\n--- output ---\n%s", got)
	}
}
