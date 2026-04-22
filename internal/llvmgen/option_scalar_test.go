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
//
// NOTE: the match-as-EXPRESSION path (where match yields a value into
// a let / fn return) walks a different lowering route and still trips
// LLVM011 with "match scrutinee type ptr, want enum tag". That's the
// next layer to lift; for this PR the scope is the producer-side wall
// and the consumer-side statement path round-trip.
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
