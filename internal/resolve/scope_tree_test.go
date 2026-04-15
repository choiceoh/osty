package resolve

import (
	"strings"
	"testing"
)

// TestScopeChildrenFn asserts that a fn decl's body appears as a child
// of the enclosing file scope, and that nested blocks appear beneath
// the fn scope.
func TestScopeChildrenFn(t *testing.T) {
	res := resolveSrc(t, `
fn greet(name: String) -> String {
    let g = "hi"
    g
}
`)
	fileScope := res.FileScope
	if got := len(fileScope.Children()); got != 1 {
		t.Fatalf("file scope: got %d children, want 1", got)
	}
	fnScope := fileScope.Children()[0]
	if !strings.HasPrefix(fnScope.Kind(), "fn:") {
		t.Fatalf("expected fn:* child, got %q", fnScope.Kind())
	}
	// The fn scope holds the parameter; its single child block holds the
	// body's let binding.
	if _, ok := fnScope.Symbols()["name"]; !ok {
		t.Fatalf("fn scope missing `name` parameter: %v", fnScope.Symbols())
	}
	if got := len(fnScope.Children()); got != 1 {
		t.Fatalf("fn scope: got %d children, want 1 (body block)", got)
	}
	block := fnScope.Children()[0]
	if block.Kind() != "block" {
		t.Fatalf("expected block child, got %q", block.Kind())
	}
	if _, ok := block.Symbols()["g"]; !ok {
		t.Fatalf("block scope missing `let g`: %v", block.Symbols())
	}
}

// TestScopeChildrenForAndClosure asserts that for-loops and closures
// appear as nested scopes with the expected kinds, in creation order.
func TestScopeChildrenForAndClosure(t *testing.T) {
	res := resolveSrc(t, `
fn loop() {
    for i in 0..10 {
        let x = i
    }
    let add = |a: Int, b: Int| a + b
}
`)
	fn := res.FileScope.Children()[0]
	body := fn.Children()[0] // fn body is a block
	kinds := make([]string, 0, len(body.Children()))
	for _, c := range body.Children() {
		kinds = append(kinds, c.Kind())
	}
	// For-loop opens one scope (holding `i`), whose body opens another block.
	// The closure opens one scope (holding params a and b).
	if len(kinds) < 2 {
		t.Fatalf("expected >= 2 children on fn body, got %v", kinds)
	}
	if kinds[0] != "for" {
		t.Fatalf("first child: got %q, want for", kinds[0])
	}
	if kinds[len(kinds)-1] != "closure" {
		t.Fatalf("last child: got %q, want closure", kinds[len(kinds)-1])
	}
	closure := body.Children()[len(body.Children())-1]
	if _, ok := closure.Symbols()["a"]; !ok {
		t.Fatalf("closure scope missing `a`: %v", closure.Symbols())
	}
}

// TestScopeChildrenOrPatternNoLeak verifies that the throwaway scopes
// bindOrPattern creates for alternative-by-alternative dup checking do
// NOT leak into the enclosing match-arm scope.
func TestScopeChildrenOrPatternNoLeak(t *testing.T) {
	res := resolveSrc(t, `pub enum Shape {
    Circle(Float),
    Rect(Float, Float),
}
fn radius(s: Shape) -> Float {
    match s {
        Circle(r) | Rect(r, _) -> r,
    }
}
`)
	// File scope now has [enum:Shape, fn:radius]. Use collectKind to walk
	// the whole file looking for match-arm scopes — don't assume index.
	armScopes := collectKind(res.FileScope, "match-arm")
	if len(armScopes) != 1 {
		t.Fatalf("expected 1 match-arm scope, got %d", len(armScopes))
	}
	arm := armScopes[0]
	for _, child := range arm.Children() {
		if child.Kind() == "or-alt" {
			t.Fatalf("or-alt scope leaked into match-arm: %v", arm.Children())
		}
	}
	// `r` is the shared binding across both alternatives (Circle(r) and
	// Rect(r, _)); it should be committed to the match-arm scope.
	if _, ok := arm.Symbols()["r"]; !ok {
		t.Fatalf("match-arm scope missing `r` binding from or-pattern: %v",
			arm.Symbols())
	}
}

// collectKind walks the scope tree rooted at s and returns every
// descendant (and s itself) whose Kind() equals want. Used by tests that
// want to assert a specific scope exists regardless of exact nesting.
func collectKind(s *Scope, want string) []*Scope {
	var out []*Scope
	var walk func(*Scope)
	walk = func(n *Scope) {
		if n.Kind() == want {
			out = append(out, n)
		}
		for _, c := range n.Children() {
			walk(c)
		}
	}
	walk(s)
	return out
}
