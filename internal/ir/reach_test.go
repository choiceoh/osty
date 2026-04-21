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

func TestReachMethodsEmpty(t *testing.T) {
	if got := ReachMethods(nil); len(got) != 0 {
		t.Fatalf("ReachMethods(nil) = %v, want empty", got)
	}
	if got := ReachMethods(&Module{Package: "main"}); len(got) != 0 {
		t.Fatalf("ReachMethods(empty) = %v, want empty", got)
	}
}

func TestReachMethodsCollectsStdlibMethodCall(t *testing.T) {
	hexT := &NamedType{Package: "encoding", Name: "Hex"}
	call := &MethodCall{
		Receiver: &Ident{Name: "h", Kind: IdentLocal, T: hexT},
		Name:     "encode",
		Args:     []Arg{{Value: &Ident{Name: "data", Kind: IdentLocal}}},
	}
	mod := &Module{
		Package: "main",
		Script:  []Stmt{&ExprStmt{X: call}},
	}
	got := ReachMethods(mod)
	want := MethodRef{Module: "encoding", Type: "Hex", Method: "encode"}
	if _, ok := got[want]; !ok {
		t.Fatalf("ReachMethods() = %v, want contains %v", got, want)
	}
	if len(got) != 1 {
		t.Fatalf("ReachMethods() = %v, want exactly one entry", got)
	}
}

func TestReachMethodsDedupes(t *testing.T) {
	hexT := &NamedType{Package: "encoding", Name: "Hex"}
	mk := func() *MethodCall {
		return &MethodCall{
			Receiver: &Ident{Name: "h", Kind: IdentLocal, T: hexT},
			Name:     "encode",
		}
	}
	mod := &Module{
		Package: "main",
		Script: []Stmt{
			&ExprStmt{X: mk()},
			&ExprStmt{X: mk()},
		},
	}
	if got := ReachMethods(mod); len(got) != 1 {
		t.Fatalf("ReachMethods() size = %d, want 1 (deduped)", len(got))
	}
}

func TestReachMethodsSkipsUserTypes(t *testing.T) {
	// User-defined `MyType` has no Package qualifier — must be ignored
	// so the method-injection consumer doesn't try to look it up in the
	// stdlib registry.
	userT := &NamedType{Package: "", Name: "MyType"}
	call := &MethodCall{
		Receiver: &Ident{Name: "v", Kind: IdentLocal, T: userT},
		Name:     "doThing",
	}
	mod := &Module{
		Package: "main",
		Script:  []Stmt{&ExprStmt{X: call}},
	}
	if got := ReachMethods(mod); len(got) != 0 {
		t.Fatalf("ReachMethods() = %v, want empty (user-type method)", got)
	}
}

func TestReachMethodsSkipsUntypedReceiver(t *testing.T) {
	// A method call whose receiver hasn't been typed (Type() returns
	// nil) must be silently skipped — not panic. The lowerer can leave
	// types unset for diagnostic-only IR, and reach is a best-effort
	// pre-injection scan.
	call := &MethodCall{
		Receiver: &Ident{Name: "v", Kind: IdentLocal},
		Name:     "doThing",
	}
	mod := &Module{
		Package: "main",
		Script:  []Stmt{&ExprStmt{X: call}},
	}
	if got := ReachMethods(mod); len(got) != 0 {
		t.Fatalf("ReachMethods() = %v, want empty (untyped receiver)", got)
	}
}

func TestReachAndReachMethodsAreIndependent(t *testing.T) {
	// A module containing both a free-fn call and a typed method call
	// should populate Reach and ReachMethods on separate keys. Guards
	// against a future refactor that accidentally couples the two
	// visitors and drops one shape on the floor.
	//
	// Note: Reach (the legacy free-fn visitor) sees `h.encode(...)` as
	// a QualifiedRef{"h", "encode"} too — that's intentional, since the
	// shape is indistinguishable at the syntax level and registry
	// lookup filters non-stdlib qualifiers. ReachMethods is the
	// type-aware narrowing that targets only the stdlib MethodCall
	// shape.
	freeCall := &CallExpr{
		Callee: &FieldExpr{X: &Ident{Name: "strings"}, Name: "compare"},
		Args:   []Arg{{Value: &Ident{Name: "a"}}, {Value: &Ident{Name: "b"}}},
	}
	hexT := &NamedType{Package: "encoding", Name: "Hex"}
	methodCall := &MethodCall{
		Receiver: &Ident{Name: "h", T: hexT},
		Name:     "encode",
	}
	mod := &Module{
		Package: "main",
		Script: []Stmt{
			&ExprStmt{X: freeCall},
			&ExprStmt{X: methodCall},
		},
	}
	rch := Reach(mod)
	if _, ok := rch[QualifiedRef{Qualifier: "strings", Name: "compare"}]; !ok {
		t.Fatalf("Reach() missing strings.compare: %v", rch)
	}
	mrch := ReachMethods(mod)
	if _, ok := mrch[MethodRef{Module: "encoding", Type: "Hex", Method: "encode"}]; !ok {
		t.Fatalf("ReachMethods() missing encoding.Hex.encode: %v", mrch)
	}
	if len(mrch) != 1 {
		t.Fatalf("ReachMethods() size = %d, want exactly 1", len(mrch))
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
