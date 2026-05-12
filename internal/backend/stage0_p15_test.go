package backend

import (
	"strings"
	"testing"
)

// TestStage0P15StructFieldBinaryOp verifies that stage0 covers the
// inlined "two-field arithmetic" shape — the front-end emits this for
// `fn name(p: Struct) -> T { p.fieldA op p.fieldB }` as a single
// AssignInstr whose Src is BinaryRV with two CopyOp(param.fieldN)
// operands. Without this matcher stage0 declines pointSum-style
// cross-file callers and blocks toolchain self-build.
func TestStage0P15StructFieldBinaryOpSum(t *testing.T) {
	src := `struct Point { x: Int, y: Int }
fn pointSum(p: Point) -> Int { p.x + p.y }
fn main() {}`
	out, err := realStage0Run(t, src)
	if err != nil {
		t.Fatalf("stage0 declined: %v", err)
	}
	wants := []string{
		"%Point = type { i64, i64 }",
		"define i64 @pointSum(ptr %p) {",
		"getelementptr inbounds %Point, ptr %p, i32 0, i32 0",
		"getelementptr inbounds %Point, ptr %p, i32 0, i32 1",
		"load i64",
		"add i64",
	}
	got := string(out)
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("stage0 output missing %q\n--- output ---\n%s", want, got)
		}
	}
}

// Comparison op + two fields → Bool return.
func TestStage0P15StructFieldBinaryOpCompare(t *testing.T) {
	src := `struct Bounds { lo: Int, hi: Int }
fn boundsOk(b: Bounds) -> Bool { b.lo < b.hi }
fn main() {}`
	out, err := realStage0Run(t, src)
	if err != nil {
		t.Fatalf("stage0 declined: %v", err)
	}
	wants := []string{
		"define i1 @boundsOk(ptr %b) {",
		"getelementptr inbounds %Bounds, ptr %b, i32 0, i32 0",
		"getelementptr inbounds %Bounds, ptr %b, i32 0, i32 1",
		"icmp slt i64",
	}
	got := string(out)
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("stage0 output missing %q\n--- output ---\n%s", want, got)
		}
	}
}

// One field + one constant operand.
func TestStage0P15StructFieldBinaryOpFieldPlusConst(t *testing.T) {
	src := `struct Counter { n: Int }
fn bump(c: Counter) -> Int { c.n + 1 }
fn main() {}`
	out, err := realStage0Run(t, src)
	if err != nil {
		t.Fatalf("stage0 declined: %v", err)
	}
	wants := []string{
		"define i64 @bump(ptr %c) {",
		"getelementptr inbounds %Counter, ptr %c, i32 0, i32 0",
		"add i64",
		", 1",
	}
	got := string(out)
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("stage0 output missing %q\n--- output ---\n%s", want, got)
		}
	}
}
