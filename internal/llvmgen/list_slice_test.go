package llvmgen

import (
	"strings"
	"testing"
)

// List slice syntax — `xs[start..end]`, `xs[start..=end]`, `xs[..end]`,
// `xs[start..]` — lowers to `osty_rt_list_slice`. Prior to Tier A-1
// List-slice support the same shape tripped LLVM013 "expression
// *ast.RangeExpr" because the RangeExpr was never dispatched out of
// the index path for a List<T> base.
func TestListSliceHalfOpenRange(t *testing.T) {
	file := parseLLVMGenFile(t, `fn take(xs: List<Int>) -> List<Int> {
    xs[1..3]
}

fn main() {
    let xs = [10, 20, 30, 40]
    let ys = take(xs)
    println(ys.len())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/list_slice_half_open.osty"})
	if err != nil {
		t.Fatalf("half-open list slice errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_list_slice",
		"declare ptr @osty_rt_list_slice(ptr, i64, i64)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

// Inclusive `..=` must add 1 to the upper bound so `xs[0..=1]` emits
// elements at index 0 and 1 — same convention the String slicer uses.
func TestListSliceInclusiveRange(t *testing.T) {
	file := parseLLVMGenFile(t, `fn head2(xs: List<Int>) -> List<Int> {
    xs[0..=1]
}

fn main() {
    let xs = [10, 20, 30]
    println(head2(xs).len())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/list_slice_incl.osty"})
	if err != nil {
		t.Fatalf("inclusive list slice errored: %v", err)
	}
	got := string(ir)
	if !strings.Contains(got, "@osty_rt_list_slice") {
		t.Fatalf("inclusive list slice did not call runtime slice:\n%s", got)
	}
	if !strings.Contains(got, "add i64") {
		t.Fatalf("inclusive list slice missing `add i64` upper-bound adjustment:\n%s", got)
	}
}

// Missing upper bound `xs[1..]` defaults to list.len via osty_rt_list_len,
// mirroring the String slicer's ByteLen fallback.
func TestListSliceOpenHigh(t *testing.T) {
	file := parseLLVMGenFile(t, `fn dropFirst(xs: List<Int>) -> List<Int> {
    xs[1..]
}

fn main() {
    let xs = [10, 20, 30]
    println(dropFirst(xs).len())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/list_slice_open_high.osty"})
	if err != nil {
		t.Fatalf("open-high list slice errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_list_slice",
		"@osty_rt_list_len",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("open-high list slice missing %q:\n%s", want, got)
		}
	}
}

// List<String> slicing must preserve the pointer-list ABI so downstream
// iteration (get_ptr, sorted_string) keeps working on the result.
func TestListSliceStringElements(t *testing.T) {
	file := parseLLVMGenFile(t, `fn firstTwo(xs: List<String>) -> List<String> {
    xs[0..2]
}

fn main() {
    let xs = ["a", "b", "c"]
    println(firstTwo(xs).len())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/list_slice_string.osty"})
	if err != nil {
		t.Fatalf("list<string> slice errored: %v", err)
	}
	got := string(ir)
	if !strings.Contains(got, "@osty_rt_list_slice") {
		t.Fatalf("list<string> slice did not call runtime slice:\n%s", got)
	}
}
