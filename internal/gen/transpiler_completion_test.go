package gen_test

import (
	"strings"
	"testing"
)

// TestStructSpreadUpdate exercises the `..spread` functional-update
// form. The generator lowers `Point { x: 10, ..p }` to an IIFE that
// copies `p` and overrides the explicit fields.
func TestStructSpreadUpdate(t *testing.T) {
	src := `struct Point { x: Int, y: Int }

fn main() {
    let p = Point { x: 1, y: 2 }
    let q = Point { x: 10, ..p }
    println("{q.x} {q.y}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "10 2" {
		t.Errorf("spread update: got %q; want %q\n--- go ---\n%s", out, "10 2", goSrc)
	}
}

// TestForTuplePatternAnyArity verifies for-loops with a tuple pattern
// of arity ≠ 2 destructure cleanly. Previously only 2-element tuples
// were supported; wider tuples fell through to a TODO marker.
func TestForTuplePatternAnyArity(t *testing.T) {
	src := `fn main() {
    let xs: List<(Int, Int, Int)> = [(1, 2, 3), (4, 5, 6)]
    let mut sum = 0
    for (a, b, c) in xs {
        sum = sum + a + b + c
    }
    println("{sum}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	// 1+2+3 + 4+5+6 = 21
	if strings.TrimSpace(out) != "21" {
		t.Errorf("for tuple pattern: got %q; want 21\n--- go ---\n%s", out, goSrc)
	}
}

// TestForTuplePatternPairList verifies a two-element tuple pattern
// over a List<(A, B)> destructures the element value. This is distinct
// from the `for (k, v) in map` lowering, where Go's range supplies key
// and value separately.
func TestForTuplePatternPairList(t *testing.T) {
	src := `fn main() {
    let pairs: List<(Int, Int)> = [(1, 2), (3, 4)]
    let mut total = 0
    for (a, b) in pairs {
        total = total + a + b
    }
    println("{total}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "10" {
		t.Errorf("for pair-list tuple pattern: got %q; want 10\n--- go ---\n%s", out, goSrc)
	}
}

// TestForEnumerateNestedTuplePattern covers the rebase merge point:
// origin/main added enumerate() lowering while the completion patch
// moved tuple destructuring onto the recursive pattern-binding path.
func TestForEnumerateNestedTuplePattern(t *testing.T) {
	src := `fn main() {
    let pairs: List<(Int, Int)> = [(1, 2), (3, 4)]
    let mut total = 0
    for (i, (a, b)) in pairs.enumerate() {
        total = total + i + a + b
    }
    println("{total}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "11" {
		t.Errorf("for enumerate nested tuple pattern: got %q; want 11\n--- go ---\n%s", out, goSrc)
	}
}

// TestNestedLetPattern verifies nested tuple destructuring inside a
// struct `let` pattern reuses the same binding machinery as match.
func TestNestedLetPattern(t *testing.T) {
	src := `struct PairBox { pair: (Int, Int), tail: Int }

fn main() {
    let box = PairBox { pair: (2, 3), tail: 4 }
    let PairBox { pair: (a, b), tail } = box
    println("{a + b + tail}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "9" {
		t.Errorf("nested let pattern: got %q; want 9\n--- go ---\n%s", out, goSrc)
	}
}

// TestForLet verifies the `for let pat = expr { ... }` lowering emits
// the expected break-on-miss loop shell. We don't exercise full runtime
// behaviour here — the end-to-end `osty test` path on examples/calc
// covers the runtime side through testing.context's closure arm. Here
// we only assert the generated Go compiles and structurally contains
// a `for {` scaffold so a regression to the previous TODO marker
// (which emitted a plain block) fails the test.
func TestForLet(t *testing.T) {
	src := `fn peek() -> Int? {
    None
}

fn main() {
    for let Some(v) = peek() {
        println("{v}")
    }
    println("done")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	s := string(goSrc)
	if !strings.Contains(s, "for {") {
		t.Errorf("for-let: expected `for {` scaffold in output:\n%s", s)
	}
	if strings.Contains(s, "TODO(phase4): for let") {
		t.Errorf("for-let: TODO marker still present:\n%s", s)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "done" {
		t.Errorf("for-let: got %q; want done", out)
	}
}

// TestForLetUserEnum verifies `for let` goes through the general
// pattern-test path, not just the Option/Result special cases.
func TestForLetUserEnum(t *testing.T) {
	src := `enum Step {
    More(Int),
    Done,
}

fn next(n: Int) -> Step {
    if n > 0 {
        More(n)
    } else {
        Done
    }
}

fn main() {
    let mut n = 3
    let mut total = 0
    for let More(v) = next(n) {
        total = total + v
        n = n - 1
    }
    println("{total}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "6" {
		t.Errorf("for-let user enum: got %q; want 6\n--- go ---\n%s", out, goSrc)
	}
}

// TestForLetNestedDestructure verifies for-let binds nested payload
// patterns after the match succeeds.
func TestForLetNestedDestructure(t *testing.T) {
	src := `fn maybePair(n: Int) -> (Int, Int)? {
    if n < 1 { Some((2, 3)) } else { None }
}

fn main() {
    let mut n = 0
    let mut total = 0
    for let Some((a, b)) = maybePair(n) {
        total = total + a + b
        n = n + 1
    }
    println("{total}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	if strings.Contains(string(goSrc), "TODO: for-let") {
		t.Errorf("for-let TODO marker still present:\n%s", goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "5" {
		t.Errorf("for-let nested destructure: got %q\n--- go ---\n%s", out, goSrc)
	}
}

// TestMatchTuplePattern verifies tuple patterns in match arms generate
// a conjunction of per-element tests (previously hardcoded `true`).
func TestMatchTuplePattern(t *testing.T) {
	src := `fn classify(p: (Int, Int)) -> String {
    match p {
        (0, 0) ->"origin",
        (0, _) ->"y-axis",
        (_, 0) ->"x-axis",
        _ ->"other",
    }
}

fn main() {
    println("{classify((0, 0))}")
    println("{classify((0, 5))}")
    println("{classify((3, 0))}")
    println("{classify((4, 7))}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	want := "origin\ny-axis\nx-axis\nother"
	if strings.TrimSpace(out) != want {
		t.Errorf("match tuple pattern: got %q; want %q\n--- go ---\n%s", out, want, goSrc)
	}
}

// TestMatchStructPattern verifies struct patterns in match arms compile
// the per-field tests that the old TODO marker elided.
func TestMatchStructPattern(t *testing.T) {
	src := `struct Box { w: Int, h: Int }

fn describe(b: Box) -> String {
    match b {
        Box { w: 0, h: 0 } ->"empty",
        Box { w: 0, .. } ->"thin-w",
        Box { h: 0, .. } ->"thin-h",
        _ ->"solid",
    }
}

fn main() {
    println("{describe(Box { w: 0, h: 0 })}")
    println("{describe(Box { w: 0, h: 5 })}")
    println("{describe(Box { w: 3, h: 0 })}")
    println("{describe(Box { w: 2, h: 2 })}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	want := "empty\nthin-w\nthin-h\nsolid"
	if strings.TrimSpace(out) != want {
		t.Errorf("match struct pattern: got %q; want %q\n--- go ---\n%s", out, want, goSrc)
	}
}

// TestClosureBodyVoidTail verifies a closure whose body tail is a
// void-returning user fn doesn't get wrapped in a spurious `return`.
// This is the bug that previously broke `osty test` on calc's
// testClampTable via testing.context.
func TestClosureBodyVoidTail(t *testing.T) {
	src := `fn noop() {
}

fn apply(f: fn() -> ()) {
    f()
}

fn main() {
    apply(|| { noop() })
    println("ok")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	if strings.Contains(string(goSrc), "return noop()") {
		t.Errorf("closure wraps void tail in return:\n%s", goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "ok" {
		t.Errorf("closure void tail: got %q; want ok", out)
	}
}

// TestClosureStructPatternParam verifies closure parameter
// destructuring supports the struct LetPattern form from v0.3.
func TestClosureStructPatternParam(t *testing.T) {
	src := `struct User { name: String, age: Int }

fn render(f: fn(User) -> String, u: User) -> String {
    f(u)
}

fn main() {
    let out = render(|User { name, age }| "{name}:{age}", User { name: "ada", age: 37 })
    println(out)
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "ada:37" {
		t.Errorf("closure struct pattern: got %q; want ada:37\n--- go ---\n%s", out, goSrc)
	}
}
