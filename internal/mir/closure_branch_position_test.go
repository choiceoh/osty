package mir

import (
	"strings"
	"testing"
)

// TestLowerClosureBackfillFromBranchTrailingPosition covers
// closures sitting in the trailing position of an `if/else` or
// `match` whose result type comes from the enclosing fn's
// return slot. Mirrors the return-position handling from
// PR #1838 but for the branch-expression shape:
//
//	fn pick(b: Bool) -> fn(Int) -> Int {
//	    if b { |x| x + 1 } else { |x| x - 1 }
//	}
//
// The fn's return type (`fn(Int) -> Int`) propagates through
// `backfillTrailingClosure` into both branches' trailing
// closures. Without this, each branch's closure lowers with
// `Params[0].Type = nil` and the lifted MIR body has
// `_2: <error>` for the param slot, poisoning the body.
//
// Both `IfStmt` (the parser's representation for trailing
// `if/else`) and `IfExpr` (when the body is wrapped in an
// expression context) are covered, plus the matching
// `MatchStmt` / `MatchExpr` and `IfLetExpr` shapes.
//
// `closure_in_if_let_with_payload_capture` is left as future
// work: the captured `n` from `if let Some(n) = opt` reads
// `<error>` from the pattern binding at closure-lowering time
// because the IR-side if-let pattern type recovery hasn't run
// yet. PR #1820 fixed the MIR layer for the bound payload, but
// the IR Capture.T snapshots the binding earlier in the pipeline.
// Tracked separately.
func TestLowerClosureBackfillFromBranchTrailingPosition(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			"closure_in_if_branch",
			`fn pick(b: Bool) -> fn(Int) -> Int {
    if b { |x| x + 1 } else { |x| x - 1 }
}`,
		},
		{
			"closure_in_match_arm",
			`fn pick(b: Bool) -> fn(Int) -> Int {
    match b {
        true -> |x| x + 1,
        false -> |x| x - 1,
    }
}`,
		},
		{
			"closure_in_if_branch_nested_arith",
			`fn pickAdd(a: Int, b: Bool) -> fn(Int) -> Int {
    if b { |x| x + a } else { |x| x }
}`,
		},
		{
			"closure_in_match_arm_returns_bool_predicate",
			`fn cmp(b: Bool) -> fn(Int) -> Bool {
    match b {
        true -> |x| x > 0,
        false -> |x| x < 0,
    }
}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mirText := lowerSourceToMIR(t, c.src)
			if strings.Contains(mirText, "<error>") {
				t.Errorf("<error> leak in %s:\n%s", c.name, mirText)
			}
		})
	}
}
