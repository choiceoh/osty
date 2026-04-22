package ir

import (
	"testing"
)

// Inline closures (`|x| x + 1`) leave their per-param AST Type nil
// because the user didn't annotate them — the checker's inference
// picks them up via the call-site context (e.g. `xs.fold(0, |acc, n|
// acc + n)` resolves to `fn(Int, Int) -> Int`). `lowerParam`
// faithfully forwards the nil, but downstream IR validation rejects
// nil param types ("Closure: param[i] nil Type").
//
// `lowerClosure` backfills any nil per-param Type from the closure's
// inferred FnType (out.T), which the checker has already populated.
// Verifies via a stdlib call site that exercises the path
// end-to-end: `xs.fold(0, |acc, n| acc + n)`.
func TestLowerClosureBackfillsNilParamTypesFromInferredFnType(t *testing.T) {
	src := `
fn main() {
    let xs: List<Int> = [1, 2, 3]
    let _ = xs.fold(0, |acc, n| acc + n)
}
`
	mod := lowerSrc(t, src)
	var found bool
	Inspect(mod, func(n Node) bool {
		c, ok := n.(*Closure)
		if !ok {
			return true
		}
		if len(c.Params) != 2 {
			return true
		}
		found = true
		for i, p := range c.Params {
			if p == nil {
				t.Errorf("closure param[%d] nil pointer", i)
				continue
			}
			if p.Type == nil {
				t.Errorf("closure param[%d] (%q) Type still nil after backfill", i, p.Name)
			}
		}
		return true
	})
	if !found {
		t.Fatalf("no 2-param Closure found in lowered module — fold call site missing?")
	}
	// Validate the whole module: the nil-Type wall would surface here
	// without the backfill.
	if errs := Validate(mod); len(errs) != 0 {
		t.Fatalf("module Validate failed after closure backfill: %v", errs)
	}
}

// A closure with EXPLICIT param types must keep them — the backfill
// only fires when the AST type is nil. Locks against a regression
// where the backfill loop accidentally overwrites annotated types.
func TestLowerClosurePreservesExplicitParamTypes(t *testing.T) {
	src := `
fn apply(f: fn(Int) -> Int, x: Int) -> Int {
    f(x)
}

fn main() {
    let _ = apply(|n: Int| n * 2, 5)
}
`
	mod := lowerSrc(t, src)
	var checked bool
	Inspect(mod, func(n Node) bool {
		c, ok := n.(*Closure)
		if !ok || len(c.Params) != 1 {
			return true
		}
		checked = true
		if c.Params[0].Type == nil {
			t.Errorf("explicitly typed closure param has nil Type")
		}
		return true
	})
	if !checked {
		t.Fatalf("no 1-param explicit-type Closure found")
	}
}
