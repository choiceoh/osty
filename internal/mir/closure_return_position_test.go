package mir

import (
	"strings"
	"testing"
)

// TestLowerClosureBackfillsFromReturnAndAnnotation locks in the
// IR-lowering hand-off for closures whose expected signature
// comes from somewhere other than a method-call arg slot —
// follow-up to PR #1828 (which handled method-arg closure
// inference) and PR #1836 (which fixed chain return type
// recovery).
//
// Two surfaces, both routed through `backfillClosure` after the
// initial lowering:
//
//  1. **Return-position closure** —
//
//     ```osty
//     fn makeAdder(n: Int) -> fn(Int) -> Int {
//         |x| x + n
//     }
//     ```
//
//     The trailing-expression closure inherits its signature
//     from the declared return type. `backfillTrailingClosure`
//     walks Block.Result and the last ExprStmt of Block.Stmts
//     because the parser keeps single-expression bodies as
//     statement-position trailing expressions.
//
//  2. **LetStmt-with-FnType-annotation** —
//
//     ```osty
//     let f: fn(Int) -> Int = |x| x + 1
//     ```
//
//     The annotation on the binding propagates back into the
//     Closure value so its un-annotated params resolve.
//
// Without these, the closure's `Params[i].Type` stays nil and
// every Ident reading the param lowers as ErrTypeVal, poisoning
// the closure body and any consuming expression.
//
// Asserts: (a) closure body fn signature is concrete (no
// `_<idx>: <error>` slots in the lifted closure fn), (b) no
// `<error>` leaks anywhere in the MIR.
func TestLowerClosureBackfillsFromReturnAndAnnotation(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			"return_position_capture_int",
			`fn makeAdder(n: Int) -> fn(Int) -> Int {
    |x| x + n
}`,
		},
		{
			"return_position_bool_predicate",
			`fn gt(n: Int) -> fn(Int) -> Bool {
    |x| x > n
}`,
		},
		{
			"return_position_no_capture",
			`fn inc() -> fn(Int) -> Int {
    |x| x + 1
}`,
		},
		{
			"let_binding_with_annotation",
			`fn main() {
    let f: fn(Int) -> Int = |x| x + 1
    let _ = f(5)
}`,
		},
		{
			"let_binding_with_bool_predicate",
			`fn main() {
    let pred: fn(Int) -> Bool = |n| n > 0
    let _ = pred(5)
}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mirText := lowerSourceToMIR(t, c.src)
			if strings.Contains(mirText, "<error>") {
				t.Errorf("<error> leaked into MIR for %s:\n%s", c.name, mirText)
			}
		})
	}
}
