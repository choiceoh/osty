package ir

import "testing"

func TestSubstituteTypesReplacesTypeVarInNamedArgs(t *testing.T) {
	v := &TypeVar{Name: "T"}
	list := &NamedType{Name: "List", Args: []Type{v}, Builtin: true}
	fn := &FnDecl{Name: "f", Return: list, Body: &Block{}}
	SubstituteTypes(fn, SubstEnv{"T": TInt})
	if got := list.Args[0]; got != TInt {
		t.Fatalf("expected TInt after subst, got %T (%v)", got, got)
	}
}

func TestSubstituteTypesNoopOnEmptyEnv(t *testing.T) {
	v := &TypeVar{Name: "T"}
	list := &NamedType{Name: "List", Args: []Type{v}, Builtin: true}
	fn := &FnDecl{Return: list, Body: &Block{}}
	SubstituteTypes(fn, SubstEnv{})
	if list.Args[0] != v {
		t.Fatalf("empty env should leave TypeVar untouched, got %T", list.Args[0])
	}
}

func TestSubstituteTypesNilRootIsSafe(t *testing.T) {
	// Must not panic.
	SubstituteTypes(nil, SubstEnv{"T": TInt})
}

func TestSubstituteTypesOnlyReplacesNamedVars(t *testing.T) {
	// A TypeVar with a name not in the env must survive the walk.
	v := &TypeVar{Name: "U"}
	opt := &OptionalType{Inner: v}
	SubstituteTypes(&FnDecl{Return: opt, Body: &Block{}}, SubstEnv{"T": TInt})
	if opt.Inner != v {
		t.Fatalf("unrelated TypeVar should be untouched, got %T", opt.Inner)
	}
}

func TestSubstituteTypesDeepOptional(t *testing.T) {
	opt := &OptionalType{Inner: &TypeVar{Name: "T"}}
	SubstituteTypes(&FnDecl{Return: opt, Body: &Block{}}, SubstEnv{"T": TString})
	if opt.Inner != TString {
		t.Fatalf("expected TString inner, got %T", opt.Inner)
	}
}

func TestSubstituteTypesDeepTuple(t *testing.T) {
	tup := &TupleType{Elems: []Type{&TypeVar{Name: "T"}, TBool}}
	SubstituteTypes(&FnDecl{Return: tup, Body: &Block{}}, SubstEnv{"T": TInt})
	if tup.Elems[0] != TInt || tup.Elems[1] != TBool {
		t.Fatalf("tuple subst dropped sibling: %+v", tup.Elems)
	}
}

func TestSubstituteTypesFnTypeParamsAndReturn(t *testing.T) {
	ft := &FnType{Params: []Type{&TypeVar{Name: "T"}}, Return: &TypeVar{Name: "T"}}
	SubstituteTypes(&FnDecl{Return: ft, Body: &Block{}}, SubstEnv{"T": TInt})
	if ft.Params[0] != TInt || ft.Return != TInt {
		t.Fatalf("FnType subst failed: params=%v return=%v", ft.Params[0], ft.Return)
	}
}

func TestSubstituteTypesReachesFnDeclParamsAndBody(t *testing.T) {
	// End-to-end: substitution of a generic fn declaration should replace
	// every Type field reachable from the declaration — params, return,
	// and the expression types inside the body.
	v := &TypeVar{Name: "T"}
	bodyIdent := &Ident{Name: "x", Kind: IdentParam, T: v}
	fn := &FnDecl{
		Name:   "id",
		Return: v,
		Params: []*Param{{Name: "x", Type: v}},
		Body: &Block{
			Stmts:  []Stmt{&LetStmt{Name: "y", Type: v, Value: bodyIdent}},
			Result: &Ident{Name: "y", Kind: IdentLocal, T: v},
		},
	}
	SubstituteTypes(fn, SubstEnv{"T": TInt})
	if fn.Params[0].Type != TInt {
		t.Fatalf("param type not substituted: %T", fn.Params[0].Type)
	}
	if fn.Return != TInt {
		t.Fatalf("return type not substituted: %T", fn.Return)
	}
	if bodyIdent.T != TInt {
		t.Fatalf("body ident type not substituted: %T", bodyIdent.T)
	}
	letStmt := fn.Body.Stmts[0].(*LetStmt)
	if letStmt.Type != TInt {
		t.Fatalf("let stmt annotation type not substituted: %T", letStmt.Type)
	}
	if fn.Body.Result.(*Ident).T != TInt {
		t.Fatalf("body result ident type not substituted")
	}
}

// A Closure whose Params were never type-backfilled (p.Type == nil)
// is legitimate mid-pipeline input when an outer specialization carries
// TypeVars through the closure's inferred FnType (Closure.T) but hadn't
// had lowerClosure's backfill run. After substitution, the Closure case
// must re-run the same backfill so validator's
// "Closure: param[i] nil Type" wall is not reintroduced. Without this,
// ≥2 test files in a package importing a generic fn with an inner
// closure (e.g. fmt.joinWith<T>) trip the validator at IR merge time.
func TestSubstituteClosureBackfillsNilParamTypesFromSubstitutedFnType(t *testing.T) {
	// Closure with nil param Type; inferred FnType on Closure.T uses
	// TypeVar T that substitution will resolve to Int.
	ft := &FnType{Params: []Type{&TypeVar{Name: "T"}}, Return: TString}
	closure := &Closure{
		T:      ft,
		Return: TString,
		Params: []*Param{{Name: "n", Type: nil}},
		Body:   &Block{Result: &StringLit{Parts: []StringPart{{IsLit: true, Lit: ""}}}},
	}
	fn := &FnDecl{
		Name:   "takeClosure",
		Return: TUnit,
		Body:   &Block{Result: closure},
	}
	SubstituteTypes(fn, SubstEnv{"T": TInt})
	if closure.Params[0].Type == nil {
		t.Fatalf("closure param Type still nil after substitution backfill")
	}
	if closure.Params[0].Type != TInt {
		t.Fatalf("closure param Type not substituted: got %T (%v)", closure.Params[0].Type, closure.Params[0].Type)
	}
	if errs := Validate(&Module{Package: "main", Decls: []Decl{fn}}); len(errs) != 0 {
		t.Fatalf("Validate failed after backfill: %v", errs)
	}
}

func TestSubstituteTypesRewritesCallExprTypeArgs(t *testing.T) {
	// A generic call inside a specialization body needs its TypeArgs
	// rewritten before the outer worklist picks them up. The call's own
	// result type also ripples through.
	call := &CallExpr{
		Callee:   &Ident{Name: "id", Kind: IdentFn, T: ErrTypeVal},
		TypeArgs: []Type{&TypeVar{Name: "T"}},
		Args:     []Arg{{Value: &Ident{Name: "x", Kind: IdentParam, T: &TypeVar{Name: "T"}}}},
		T:        &TypeVar{Name: "T"},
	}
	fn := &FnDecl{
		Name:   "caller",
		Return: TUnit,
		Body:   &Block{Stmts: []Stmt{&ExprStmt{X: call}}},
	}
	SubstituteTypes(fn, SubstEnv{"T": TInt})
	if call.TypeArgs[0] != TInt {
		t.Fatalf("call TypeArgs not substituted: %T", call.TypeArgs[0])
	}
	if call.T != TInt {
		t.Fatalf("call result type not substituted: %T", call.T)
	}
	if argT := call.Args[0].Value.(*Ident).T; argT != TInt {
		t.Fatalf("call arg ident type not substituted: %T", argT)
	}
}
