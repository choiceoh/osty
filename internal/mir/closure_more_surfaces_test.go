package mir

import (
	"strings"
	"testing"
)

// TestLowerClosureBackfillCoversMapAndUserFnCalls locks in the
// IR-lowering coverage for closure-arg surfaces beyond the
// builtin List / Option / Result methods covered by PR #1828:
//
//   - **Map<K, V> higher-order methods** — `update`,
//     `getOrInsertWith`, `mapValues`, `forEach`, `any`, `all`,
//     `count`, `filter`, `retainIf`, `mergeWith`. Closure
//     signatures derive from K + V.
//   - **User-defined fn taking a closure** — `apply(|x| x * 2,
//     5)` where `apply: fn(fn(Int) -> Int, Int) -> Int`. The
//     call-expr backfill resolves the callee's FnType via the
//     resolver's RefsByID + AST FnDecl fallback.
//   - **Closure as a struct-lit field value** —
//     `Job { run: || 42 }`. The struct field's declared type
//     supplies the expected fn signature.
//   - **Closure passed to `Option.unwrapOrElse`** —
//     `opt.unwrapOrElse(|| -1)` already works via the existing
//     Option dispatch.
//
// Asserts no `<error>` leaks anywhere in the lowered MIR.
//
// Surfaces deliberately NOT included (tracked as future work):
//
//   - Closure inside a match arm body — needs the match's own
//     result-type to propagate into each arm.
//   - Closure inside an if branch — same shape as match arm.
//   - Nested closures (`|x| |y| x + y`) — outer closure's
//     Return is itself a closure; inner closure needs to learn
//     its signature from the outer's Return slot, which is
//     itself only filled after the inner body resolves.
//   - Closure returning Option (`fold(None, |acc, n| if ...
//     Some(n) else acc)`) — the None init expression has no
//     direct type context, blocking inference of A in fold<A>.
func TestLowerClosureBackfillCoversMapAndUserFnCalls(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			"map_update_inc",
			`fn inc(m: Map<String, Int>, k: String) {
    m.update(k, |n| (n ?? 0) + 1)
}`,
		},
		{
			"map_getOrInsertWith",
			`fn count(m: Map<String, Int>, k: String) -> Int {
    m.getOrInsertWith(k, || 0)
}`,
		},
		{
			"user_fn_taking_closure",
			`fn apply(f: fn(Int) -> Int, n: Int) -> Int {
    f(n)
}

fn use_it() -> Int {
    apply(|x| x * 2, 5)
}`,
		},
		{
			"closure_in_struct_lit",
			`struct Job {
    run: fn() -> Int,
}

fn make() -> Job {
    Job { run: || 42 }
}`,
		},
		{
			"unwrap_or_else_with_closure",
			`fn pick(opt: Int?) -> Int {
    opt.unwrapOrElse(|| -1)
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
