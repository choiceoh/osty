package mir

import (
	"strings"
	"testing"
)

// TestCanonicalPatternsLowerWithoutErrorLeaks walks the canonical
// patterns from CLAUDE.md §A (program structure / type system /
// expressions / pattern matching / error handling) through the
// full source → MIR pipeline and asserts no `<error>` types leak
// through. Acts as a meta-regression for the cumulative IR/MIR
// unlocks shipped in PRs #1811, #1815, #1818, #1820, #1822, #1824,
// #1828 — a future refactor that breaks any one of them surfaces
// here as a leak, not silently as a downstream LLVM emit fail.
//
// Each subtest is a self-contained source program drawn from
// (or stylistically modelled on) the canonical CLAUDE.md examples
// so the test doubles as a "Osty-style spec corpus": if your IDE
// shows `<error>` leaking through any of these, the front-end
// lowering pipeline regressed on a documented user-facing shape.
//
// Coverage matrix:
//
//   - method chain on List with closure inference (§A.7, §B.4)
//   - Option combinator chain (§A.6, §B.3)
//   - Result `?` propagation through nested struct payload (§A.6)
//   - if-let + match on prelude Option/Result (§A.5)
//   - nested struct destructuring (§A.5)
//   - `?.field` with one and two levels of nesting (§A.6)
func TestCanonicalPatternsLowerWithoutErrorLeaks(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "list_higher_order_chain",
			src: `fn pipeline(xs: List<Int>) -> Int {
    xs.filter(|n| n > 0).map(|n| n * 2).fold(0, |acc, n| acc + n)
}`,
		},
		{
			name: "option_combinator_chain",
			src: `fn cityOf(name: String?) -> String {
    name.map(|s| s).filter(|s| s != "").unwrapOr("anonymous")
}`,
		},
		{
			name: "result_question_propagation",
			src: `fn parsePair(s: String) -> Result<Int, Error> {
    let n = s.toInt()?
    Ok(n + 1)
}`,
		},
		{
			name: "match_option_with_struct_payload",
			src: `struct Profile {
    name: String,
    age: Int,
}

fn describe(p: Profile?) -> String {
    match p {
        Some(v) -> v.name,
        None -> "anonymous",
    }
}`,
		},
		{
			name: "if_let_some_chain",
			src: `fn pick(opt: Int?) -> Int {
    if let Some(n) = opt {
        n + 1
    } else {
        0
    }
}`,
		},
		{
			name: "nested_destructure_no_alias",
			src: `struct Address {
    city: String,
    zip: String,
}

struct User {
    name: String,
    addr: Address,
    score: Int,
}

fn cityScore(u: User) -> Int {
    let User { addr: Address { city, .. }, score, .. } = u
    score + city.len()
}`,
		},
		{
			name: "optional_field_double_chain",
			src: `struct Address {
    city: String,
}

struct User {
    addr: Address?,
}

fn cityOf(u: User?) -> String {
    u?.addr?.city ?? "unknown"
}`,
		},
		{
			name: "result_map_chain",
			src: `fn doubled(r: Result<Int, String>) -> Result<Int, String> {
    r.map(|n| n * 2)
}`,
		},
		{
			name: "list_enumerate_through_specialization",
			src: `fn indexed(xs: List<Int>) -> Int {
    let pairs = xs.enumerate()
    pairs.len()
}`,
		},
		{
			name: "closure_capture_int",
			src: `fn makeAdder(n: Int) -> fn(Int) -> Int {
    |x| x + n
}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mirText := lowerSourceToMIR(t, c.src)
			if strings.Contains(mirText, "<error>") {
				t.Errorf("<error> leak in canonical pattern %s:\n%s", c.name, mirText)
			}
			for _, ban := range []string{
				"unsupported projection",
				"projection on non-aggregate",
			} {
				if strings.Contains(mirText, ban) {
					t.Errorf("GAP-INSTR-006 %q in %s:\n%s", ban, c.name, mirText)
				}
			}
		})
	}
}
