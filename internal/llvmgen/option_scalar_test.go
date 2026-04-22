package llvmgen

import (
	"strings"
	"testing"
)

// Option<scalar> — `Some(x)` for scalar payloads (Int, Bool, Byte,
// Char, Float) used to wall LLVM011 "Some payload type i64 requires
// boxed Option; only ptr-backed or aggregate-struct Some(...) is
// lowered". The backend now boxes scalar payloads into a GC-managed
// heap cell via the same osty.gc.alloc_v1 path used for aggregate
// structs, so None stays null-ptr and Some(42) lowers to a managed
// ptr holding the scalar.
//
// The match-arm consumer at bindOptionalMatchPayload already loads
// scalars from this shape via loadValueFromAddress, so no symmetric
// consumer-side change was needed — the boxing side is what was
// missing.
func TestOptionIntSomeLowersToGcAlloc(t *testing.T) {
	file := parseLLVMGenFile(t, `fn maybe(n: Int) -> Int? {
    if n > 0 {
        Some(n)
    } else {
        None
    }
}

fn main() {
    let _ = maybe(42)
    let _ = maybe(0)
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/option_int.osty"})
	if err != nil {
		t.Fatalf("Some<Int> errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"call ptr @osty.gc.alloc_v1",
		"declare ptr @osty.gc.alloc_v1(i64, i64, ptr)",
		"store i64 ",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Some<Int> did not invoke osty.gc.alloc_v1 + store i64: missing %q:\n%s", want, got)
		}
	}
}

func TestOptionBoolSomeLowersToGcAlloc(t *testing.T) {
	file := parseLLVMGenFile(t, `fn maybe(flag: Bool) -> Bool? {
    if flag {
        Some(flag)
    } else {
        None
    }
}

fn main() {
    let _ = maybe(true)
    let _ = maybe(false)
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/option_bool.osty"})
	if err != nil {
		t.Fatalf("Some<Bool> errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"call ptr @osty.gc.alloc_v1",
		"store i1 ",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Some<Bool> did not invoke osty.gc.alloc_v1 + store i1: missing %q:\n%s", want, got)
		}
	}
}

// Sanity check: a None-only return on a scalar Option doesn't
// allocate. The wall was on producer side for Some(x); None must
// keep its zero-cost null-ptr representation.
func TestOptionIntNoneStaysNull(t *testing.T) {
	file := parseLLVMGenFile(t, `fn nope() -> Int? {
    None
}

fn main() {
    let _ = nope()
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/option_int_none.osty"})
	if err != nil {
		t.Fatalf("None<Int> errored: %v", err)
	}
	got := string(ir)
	if strings.Contains(got, "call ptr @osty.gc.alloc_v1") {
		t.Fatalf("None<Int> unexpectedly emitted gc.alloc_v1 call:\n%s", got)
	}
	if !strings.Contains(got, "ret ptr null") {
		t.Fatalf("None<Int> did not return null ptr:\n%s", got)
	}
}

// Round-trip: producer boxes, consumer unboxes. `match` AS STATEMENT
// on a scalar Option must load the i64 back from the heap cell when
// pattern is `Some(n)`. Verifies the existing consumer path
// (bindOptionalMatchPayload → loadValueFromAddress) correctly pairs
// with the new producer boxing.
func TestOptionIntMatchStmtRoundTrip(t *testing.T) {
	file := parseLLVMGenFile(t, `fn describe(n: Int?) {
    match n {
        Some(x) -> println(x.toString()),
        None -> println("none"),
    }
}

fn main() {
    describe(Some(42))
    describe(None)
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/option_int_match.osty"})
	if err != nil {
		t.Fatalf("Option<Int> match-stmt errored: %v", err)
	}
	got := string(ir)
	// Producer side: gc.alloc_v1 for Some(42).
	if !strings.Contains(got, "call ptr @osty.gc.alloc_v1") {
		t.Fatalf("Some(42) did not box via gc.alloc_v1:\n%s", got)
	}
	// Consumer side: the Some arm must load the i64 back from the ptr.
	// loadValueFromAddress emits `load i64, ptr ...`.
	if !strings.Contains(got, "load i64") {
		t.Fatalf("Some arm did not load i64 from box:\n%s", got)
	}
}

// match-as-EXPRESSION on a scalar Option lowers via
// emitOptionalMatchExprValue: isNil branch → None arm value as
// `then`, Some arm (with payload bound) as `else`, joined via phi.
// Validates the value-yielding companion to the statement path.
func TestOptionIntMatchExprRoundTrip(t *testing.T) {
	file := parseLLVMGenFile(t, `fn describe(n: Int?) -> Int {
    match n {
        Some(x) -> x,
        None -> -1,
    }
}

fn main() {
    let _ = describe(Some(42))
    let _ = describe(None)
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/option_int_match_expr.osty"})
	if err != nil {
		t.Fatalf("Option<Int> match-expr errored: %v", err)
	}
	got := string(ir)
	if !strings.Contains(got, "call ptr @osty.gc.alloc_v1") {
		t.Fatalf("Some(42) did not box via gc.alloc_v1:\n%s", got)
	}
	// Phi merging None (-1) and Some (loaded i64) into the function's
	// return value.
	if !strings.Contains(got, "phi i64") {
		t.Fatalf("match-expr did not emit phi i64 for joined arms:\n%s", got)
	}
	if !strings.Contains(got, "load i64") {
		t.Fatalf("Some arm did not load i64 from box in expr position:\n%s", got)
	}
	if !strings.Contains(got, "icmp eq ptr ") {
		t.Fatalf("match-expr did not branch on null-ptr check:\n%s", got)
	}
}

// Wildcard arm fills the missing side. `match opt { None -> 0, _ -> x }`
// (or `_ -> -1, Some(x) -> x`) both lower cleanly.
func TestOptionIntMatchExprWildcardCoversSome(t *testing.T) {
	file := parseLLVMGenFile(t, `fn describe(n: Int?) -> Int {
    match n {
        None -> 0,
        _ -> 1,
    }
}

fn main() {
    let _ = describe(Some(7))
    let _ = describe(None)
}
`)
	_, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/option_int_match_wild.osty"})
	if err != nil {
		t.Fatalf("Option<Int> match-expr with wildcard errored: %v", err)
	}
}

// list.get on List<Int> previously walled with LLVM011 "list.get on
// List<non-ptr elem i64>". The scalar branch now lowers via the typed
// runtime symbol (osty_rt_list_get_i64) plus heap boxing of the
// fetched scalar — the resulting ptr matches the ptr-Option ABI so
// downstream match arms work unchanged.
func TestListGetIntScalarBoxes(t *testing.T) {
	file := parseLLVMGenFile(t, `fn first(xs: List<Int>) -> Int {
    match xs.get(0) {
        Some(n) -> n,
        None -> -1,
    }
}

fn main() {
    let xs: List<Int> = [10, 20, 30]
    let _ = first(xs)
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/list_get_int.osty"})
	if err != nil {
		t.Fatalf("List<Int>.get errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"call i64 @osty_rt_list_get_i64",
		"call ptr @osty.gc.alloc_v1",
		"store i64 ",
		"phi ptr",
		"load i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("List<Int>.get missing %q in IR:\n%s", want, got)
		}
	}
}

// list.get on List<Char> uses the byte-array path (Char is i32, not in
// the typed runtime set). The runtime call goes through
// osty_rt_list_get_bytes_v1 with an out-buffer + load + box.
func TestListGetCharScalarBoxesViaBytesV1(t *testing.T) {
	file := parseLLVMGenFile(t, `fn firstChar(s: String) -> Int {
    let chars: List<Char> = s.chars()
    match chars.get(0) {
        Some(c) -> c.toInt(),
        None -> -1,
    }
}

fn main() {
    let _ = firstChar("hi")
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/list_get_char.osty"})
	if err != nil {
		t.Fatalf("List<Char>.get errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"call void @osty_rt_list_get_bytes_v1",
		"call ptr @osty.gc.alloc_v1",
		"store i32 ",
		"load i32",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("List<Char>.get missing %q in IR:\n%s", want, got)
		}
	}
}

// Option<Int>.unwrap() loads the boxed scalar; previously returned
// the heap ptr unchanged, which the caller would then misuse as i64.
// The Some branch must emit `load i64` before the value reaches the
// downstream consumer.
func TestOptionIntUnwrapLoadsScalar(t *testing.T) {
	file := parseLLVMGenFile(t, `fn forceFirst(xs: List<Int>) -> Int {
    xs.get(0).unwrap()
}

fn main() {
    let xs: List<Int> = [42, 99]
    let _ = forceFirst(xs)
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/option_int_unwrap.osty"})
	if err != nil {
		t.Fatalf("Option<Int>.unwrap() errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"call i64 @osty_rt_list_get_i64",
		"call ptr @osty.gc.alloc_v1",
		"unwrap on None at",
		"load i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Option<Int>.unwrap() missing %q in IR:\n%s", want, got)
		}
	}
}

// Option<Int>.? propagates a None back to the caller as null ptr.
// The Some branch loads the i64 via loadTypedPointerValue. Verifies
// the existing `?` lowering already supports scalar Option (line
// 1246-1256 of expr.go does an innerTyp != "ptr" load when needed).
func TestOptionIntQuestionPropagationLoadsScalar(t *testing.T) {
	file := parseLLVMGenFile(t, `fn maybePlusOne(xs: List<Int>) -> Int? {
    let x = xs.get(0)?
    Some(x + 1)
}

fn main() {
    let xs: List<Int> = [42]
    let _ = maybePlusOne(xs)
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/option_int_question.osty"})
	if err != nil {
		t.Fatalf("Option<Int>.? errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"call ptr @osty.gc.alloc_v1",
		"icmp eq ptr ",
		"ret ptr null",
		"load i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Option<Int>.? missing %q in IR:\n%s", want, got)
		}
	}
}

// Out-of-bounds on a scalar list.get must return null ptr, not box a
// garbage value. The phi merges `boxed_ptr` (in-bounds) and `null`
// (out-of-bounds), so a `match get(...) { None -> ... }` arm fires
// correctly for OOB indices.
func TestListGetIntOutOfBoundsReturnsNull(t *testing.T) {
	file := parseLLVMGenFile(t, `fn isPresent(xs: List<Int>, i: Int) -> Bool {
    match xs.get(i) {
        Some(_) -> true,
        None -> false,
    }
}

fn main() {
    let xs: List<Int> = [1, 2, 3]
    let _ = isPresent(xs, 0)
    let _ = isPresent(xs, 99)
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/list_get_oob.osty"})
	if err != nil {
		t.Fatalf("List<Int>.get OOB test errored: %v", err)
	}
	got := string(ir)
	if !strings.Contains(got, "phi ptr ") || !strings.Contains(got, "[ null, ") {
		t.Fatalf("expected phi ptr with `[ null, ... ]` arm for OOB None branch:\n%s", got)
	}
}
