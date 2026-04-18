package ir

import "testing"

func TestReachEmptyModule(t *testing.T) {
	if got := Reach(nil); len(got) != 0 {
		t.Fatalf("Reach(nil) = %v, want empty", got)
	}
	if got := Reach(&Module{Package: "main"}); len(got) != 0 {
		t.Fatalf("Reach(empty) = %v, want empty", got)
	}
}

func TestReachIgnoresBareIdents(t *testing.T) {
	// `a + b` has no qualified refs.
	mod := &Module{
		Package: "main",
		Script: []Stmt{
			&ExprStmt{X: &BinaryExpr{
				Op:    BinAdd,
				Left:  &Ident{Name: "a", Kind: IdentLocal},
				Right: &Ident{Name: "b", Kind: IdentLocal},
			}},
		},
	}
	if got := Reach(mod); len(got) != 0 {
		t.Fatalf("Reach() = %v, want empty", got)
	}
}

func TestReachCollectsQualifiedCall(t *testing.T) {
	// `strings.compare(a, b)` at script level.
	call := &CallExpr{
		Callee: &FieldExpr{
			X:    &Ident{Name: "strings", Kind: IdentUnknown},
			Name: "compare",
		},
		Args: []Arg{
			{Value: &Ident{Name: "a", Kind: IdentLocal}},
			{Value: &Ident{Name: "b", Kind: IdentLocal}},
		},
	}
	mod := &Module{
		Package: "main",
		Script:  []Stmt{&ExprStmt{X: call}},
	}
	got := Reach(mod)
	want := QualifiedRef{Qualifier: "strings", Name: "compare"}
	if _, ok := got[want]; !ok {
		t.Fatalf("Reach() = %v, want contains %v", got, want)
	}
	if len(got) != 1 {
		t.Fatalf("Reach() = %v, want exactly one entry", got)
	}
}

func TestReachDedupesRepeatedCalls(t *testing.T) {
	mk := func() *CallExpr {
		return &CallExpr{
			Callee: &FieldExpr{X: &Ident{Name: "strings"}, Name: "compare"},
			Args: []Arg{
				{Value: &Ident{Name: "a"}},
				{Value: &Ident{Name: "b"}},
			},
		}
	}
	mod := &Module{
		Package: "main",
		Script: []Stmt{
			&ExprStmt{X: mk()},
			&ExprStmt{X: mk()},
		},
	}
	if got := Reach(mod); len(got) != 1 {
		t.Fatalf("Reach() size = %d, want 1 (deduped)", len(got))
	}
}

func TestReachWalksIntoFnBody(t *testing.T) {
	// fn f() { strings.compare(a, b) }
	callInside := &CallExpr{
		Callee: &FieldExpr{X: &Ident{Name: "strings"}, Name: "compare"},
		Args: []Arg{
			{Value: &Ident{Name: "a"}},
			{Value: &Ident{Name: "b"}},
		},
	}
	fn := &FnDecl{
		Name: "f",
		Body: &Block{Stmts: []Stmt{&ExprStmt{X: callInside}}},
	}
	mod := &Module{
		Package: "main",
		Decls:   []Decl{fn},
	}
	got := Reach(mod)
	want := QualifiedRef{Qualifier: "strings", Name: "compare"}
	if _, ok := got[want]; !ok {
		t.Fatalf("Reach() = %v, want contains %v", got, want)
	}
}

func TestReachSkipsFieldAccessOnNonIdent(t *testing.T) {
	// (getObj.load()).compare(a) — outer receiver is CallExpr, not Ident.
	inner := &CallExpr{
		Callee: &FieldExpr{X: &Ident{Name: "getObj"}, Name: "load"},
	}
	outer := &CallExpr{
		Callee: &FieldExpr{X: inner, Name: "compare"},
		Args:   []Arg{{Value: &Ident{Name: "a"}}},
	}
	mod := &Module{
		Package: "main",
		Script:  []Stmt{&ExprStmt{X: outer}},
	}
	got := Reach(mod)
	if _, ok := got[QualifiedRef{Qualifier: "getObj", Name: "load"}]; !ok {
		t.Fatalf("Reach() missing inner getObj.load: %v", got)
	}
	for ref := range got {
		if ref.Name == "compare" {
			t.Fatalf("Reach() unexpectedly contained %v (outer receiver is a call, not Ident)", ref)
		}
	}
}
