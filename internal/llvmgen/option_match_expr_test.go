package llvmgen

import (
	"strings"
	"testing"
)

// match-as-EXPRESSION on Option<scalar> walled with LLVM011
// "match scrutinee type ptr, want enum tag" — the value-yielding
// form (let / fn-return / nested call) walked a different lowering
// route than the statement form, and that route had no Optional
// dispatch.
//
// emitOptionalMatchExprValue mirrors emitOptionalMatchStmt's branch
// shape but uses emitIfExprPhi to merge None / Some arm values, so
// `let x = match opt { Some(n) -> n, None -> -1 }` lowers cleanly.
//
// The Some payload reuses bindOptionalMatchPayload, which already
// handles ptr / scalar / aggregate via loadValueFromAddress (set up
// by PR #576's scalar-Option boxing on the producer side); the new
// expr path is a thin wrapper around it.
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
	// Producer side: gc.alloc_v1 for Some(42) (already shipped in #576).
	if !strings.Contains(got, "call ptr @osty.gc.alloc_v1") {
		t.Fatalf("Some(42) did not box via gc.alloc_v1:\n%s", got)
	}
	// Phi merging None (-1) and Some (loaded i64) into the function's
	// return value — the value-yielding contract of emitIfExprPhi.
	if !strings.Contains(got, "phi i64") {
		t.Fatalf("match-expr did not emit phi i64 for joined arms:\n%s", got)
	}
	// Some arm loads the boxed scalar back — same load helper the
	// statement path uses.
	if !strings.Contains(got, "load i64") {
		t.Fatalf("Some arm did not load i64 from box:\n%s", got)
	}
	// isNil branch on the receiver ptr — the dispatch the new
	// expr-value path adds.
	if !strings.Contains(got, "icmp eq ptr ") {
		t.Fatalf("match-expr did not branch on null-ptr check:\n%s", got)
	}
}

// Wildcard arm fills the missing side. `match opt { None -> 0, _ -> 1 }`
// (or symmetrically `_ -> -1, Some(x) -> x`) both lower; the
// arm-resolution loop consults wildcardArm whenever Some or None is
// missing so the call site doesn't have to spell both out
// exhaustively.
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
	if _, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/option_int_match_wild.osty"}); err != nil {
		t.Fatalf("Option<Int> match-expr with wildcard errored: %v", err)
	}
}

// Round-trip on List<Int>.get inside a match-expression: producer
// boxes (PR #576), source type advertised as `Int?` by
// staticListMethodSourceType (this PR), match-expr dispatch fires
// (this PR), consumer loads i64 from the box (existing).
//
// Without staticListMethodSourceType the call's source type is
// unknown so the new match-expr branch wouldn't fire, falling back
// to the LLVM011 wall. The hook is the load-bearing piece.
func TestOptionIntMatchExprOnListGet(t *testing.T) {
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
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/option_list_get_match.osty"})
	if err != nil {
		t.Fatalf("match xs.get(0) errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"call i64 @osty_rt_list_get_i64",
		"phi i64",
		"load i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in IR:\n%s", want, got)
		}
	}
}
