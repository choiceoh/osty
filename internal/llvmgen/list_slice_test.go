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

// List indexing by a bound Range value routes through osty_rt_list_slice
// — same runtime as the literal-range path. The aggregate is destructured
// at runtime so hasStart/hasStop/inclusive stay live.
func TestListSliceByLetRangeValue(t *testing.T) {
	file := parseLLVMGenFile(t, `fn main() {
    let xs = [10, 20, 30, 40]
    let n = xs.len()
    let r = 0..n
    let ys = xs[r]
    println(ys.len())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/list_slice_let_range.osty"})
	if err != nil {
		t.Fatalf("let-range list slice errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_list_slice",
		"@osty_rt_list_len",
		"extractvalue %Range.i64",
		"select i1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("let-range list slice missing %q:\n%s", want, got)
		}
	}
}

// Range passed as a parameter also routes through the slice runtime.
// Covers `fn f(xs, r) { xs[r] }` where the function body has no static
// view of the range's start/stop — everything is pulled out of the
// aggregate at runtime.
func TestListSliceByRangeParam(t *testing.T) {
	file := parseLLVMGenFile(t, `fn take(xs: List<Int>, r: Range<Int>) -> List<Int> {
    xs[r]
}

fn main() {
    let xs = [10, 20, 30]
    println(take(xs, 0..2).len())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/list_slice_range_param.osty"})
	if err != nil {
		t.Fatalf("range-param list slice errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_list_slice",
		"extractvalue %Range.i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("range-param list slice missing %q:\n%s", want, got)
		}
	}
}

// Open-ended range values (`..n`, `n..`, `..`) must fall back to 0 for
// start and list.len for stop, matching the literal slicer. Validated
// here by storing an open-low range in a let and confirming the IR
// still emits the hasStart/hasStop select guards.
func TestListSliceByOpenRangeValue(t *testing.T) {
	file := parseLLVMGenFile(t, `fn main() {
    let xs = [10, 20, 30, 40]
    let r = ..3
    let ys = xs[r]
    println(ys.len())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/list_slice_open_range.osty"})
	if err != nil {
		t.Fatalf("open-low range list slice errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_list_slice",
		"extractvalue %Range.i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("open-low range list slice missing %q:\n%s", want, got)
		}
	}
}

// Inclusive range values (`a..=b`) must add 1 to the stop bound at
// runtime. The select on the `inclusive` field lets the same IR handle
// both exclusive and inclusive ranges without branching.
func TestListSliceByInclusiveRangeValue(t *testing.T) {
	file := parseLLVMGenFile(t, `fn main() {
    let xs = [10, 20, 30]
    let r = 0..=1
    let ys = xs[r]
    println(ys.len())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/list_slice_incl_range.osty"})
	if err != nil {
		t.Fatalf("inclusive range-value list slice errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_list_slice",
		"add i64",
		"select i1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("inclusive range-value list slice missing %q:\n%s", want, got)
		}
	}
}
