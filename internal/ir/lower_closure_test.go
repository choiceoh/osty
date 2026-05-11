package ir

import (
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/selfhost/api"
	"github.com/osty/osty/internal/token"
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

func TestLowerClosureUsesByteRangeFallbackWhenNodeIDsDiverge(t *testing.T) {
	closure := &ast.ClosureExpr{
		ID:   21,
		PosV: token.Pos{Line: 1, Column: 1, Offset: 0},
		EndV: token.Pos{Line: 1, Column: 13, Offset: 12},
		Params: []*ast.Param{
			{Name: "x"},
			{Name: "y"},
		},
		Body: &ast.IntLit{Text: "1"},
	}
	file := &ast.File{
		Stmts: []ast.Stmt{&ast.ExprStmt{X: closure}},
	}
	chk := &check.Result{
		NativeCheckResult: &api.CheckResult{
			TypedNodes: []api.CheckedNode{{
				// AST closure uses NodeID 21; SemanticDB record 999 forces
				// lowering onto the byte-range fallback instead of the byID
				// path. This guards the IR switchover against subprocess
				// JSON-boundary type loss when Go/selfhost NodeIDs diverge.
				NodeID: 999,
				Kind:   "Closure",
				Start:  0,
				End:    12,
				Type: &api.TypeRepr{
					Kind: "fn",
					Args: []api.TypeRepr{
						{Kind: "primitive", Name: "Int"},
						{Kind: "primitive", Name: "String"},
					},
					Return: &api.TypeRepr{Kind: "primitive", Name: "Bool"},
				},
			}},
		},
	}

	mod, issues := Lower("main", file, nil, chk)
	if len(issues) != 0 {
		t.Fatalf("Lower() issues = %v, want none", issues)
	}
	stmt, ok := mod.Script[0].(*ExprStmt)
	if !ok {
		t.Fatalf("script[0] = %T, want *ExprStmt", mod.Script[0])
	}
	lowered, ok := stmt.X.(*Closure)
	if !ok {
		t.Fatalf("stmt.X = %T, want *Closure", stmt.X)
	}
	if lowered.Return != TBool {
		t.Fatalf("closure return = %#v, want TBool from byte-range fallback", lowered.Return)
	}
	if len(lowered.Params) != 2 {
		t.Fatalf("closure params = %d, want 2", len(lowered.Params))
	}
	if lowered.Params[0].Type != TInt {
		t.Fatalf("closure param[0] type = %#v, want TInt from byte-range fallback", lowered.Params[0].Type)
	}
	if lowered.Params[1].Type != TString {
		t.Fatalf("closure param[1] type = %#v, want TString from byte-range fallback", lowered.Params[1].Type)
	}
	fnT, ok := lowered.T.(*FnType)
	if !ok {
		t.Fatalf("closure T = %T, want *FnType", lowered.T)
	}
	if len(fnT.Params) != 2 || fnT.Params[0] != TInt || fnT.Params[1] != TString || fnT.Return != TBool {
		t.Fatalf("closure FnType = %#v, want fn(Int, String) -> Bool", fnT)
	}
}
