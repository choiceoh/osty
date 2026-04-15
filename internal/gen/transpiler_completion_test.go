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
