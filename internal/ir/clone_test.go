package ir

import "testing"

// ==== CloneType ====

func TestCloneTypePreservesSingletons(t *testing.T) {
	// Primitive and ErrType singletons must not be duplicated — downstream
	// code switches on pointer identity for fast dispatch.
	cases := []Type{TInt, TString, TBool, TUnit, TNever, ErrTypeVal}
	for _, orig := range cases {
		if CloneType(orig) != orig {
			t.Fatalf("CloneType duplicated singleton %T (%v)", orig, orig)
		}
	}
}

func TestCloneTypeNamedIsDeep(t *testing.T) {
	orig := &NamedType{Name: "List", Args: []Type{TInt}, Builtin: true}
	cp, ok := CloneType(orig).(*NamedType)
	if !ok || cp == orig {
		t.Fatalf("expected fresh *NamedType clone, got %T same=%v", CloneType(orig), cp == orig)
	}
	if cp.Name != "List" || !cp.Builtin || len(cp.Args) != 1 || cp.Args[0] != TInt {
		t.Fatalf("clone lost field data: %+v", cp)
	}
	// Mutating the clone's Args slice must not leak back to the original.
	cp.Args[0] = TString
	if orig.Args[0] != TInt {
		t.Fatalf("mutation on clone leaked to original Args[0]=%v", orig.Args[0])
	}
}

func TestCloneTypeOptionalAndTuple(t *testing.T) {
	opt := &OptionalType{Inner: &NamedType{Name: "Box", Args: []Type{TInt}}}
	cp, ok := CloneType(opt).(*OptionalType)
	if !ok || cp == opt {
		t.Fatalf("expected fresh OptionalType clone")
	}
	if _, ok := cp.Inner.(*NamedType); !ok {
		t.Fatalf("inner not cloned to NamedType, got %T", cp.Inner)
	}
	if cp.Inner == opt.Inner {
		t.Fatalf("Inner pointer shared with original")
	}

	tup := &TupleType{Elems: []Type{TInt, &NamedType{Name: "X"}}}
	cpT, ok := CloneType(tup).(*TupleType)
	if !ok || cpT == tup {
		t.Fatalf("expected fresh TupleType clone")
	}
	if len(cpT.Elems) != 2 || cpT.Elems[0] != TInt {
		t.Fatalf("tuple elems lost: %+v", cpT.Elems)
	}
	if cpT.Elems[1] == tup.Elems[1] {
		t.Fatalf("non-primitive tuple elem pointer shared with original")
	}
}

func TestCloneTypeFnTypeDeep(t *testing.T) {
	ft := &FnType{Params: []Type{TInt, &TypeVar{Name: "T"}}, Return: &TypeVar{Name: "T"}}
	cp, ok := CloneType(ft).(*FnType)
	if !ok || cp == ft {
		t.Fatalf("expected fresh FnType clone")
	}
	if cp.Params[1] == ft.Params[1] {
		t.Fatalf("FnType TypeVar param shared with original")
	}
	if cp.Return == ft.Return {
		t.Fatalf("FnType return TypeVar shared with original")
	}
	if cp.Params[0] != TInt {
		t.Fatalf("primitive param should still share singleton")
	}
}

func TestCloneTypeVarCopiedWithFields(t *testing.T) {
	orig := &TypeVar{Name: "T", Owner: "id"}
	cp, ok := CloneType(orig).(*TypeVar)
	if !ok || cp == orig {
		t.Fatalf("expected fresh *TypeVar clone")
	}
	if cp.Name != "T" || cp.Owner != "id" {
		t.Fatalf("TypeVar fields not preserved: %+v", cp)
	}
}

func TestCloneTypeNilSafe(t *testing.T) {
	if CloneType(nil) != nil {
		t.Fatalf("CloneType(nil) should return nil")
	}
	if Clone(nil) != nil {
		t.Fatalf("Clone(nil) should return nil")
	}
}

// ==== Clone (nodes) ====

func TestCloneFnDeclProducesIndependentBody(t *testing.T) {
	orig := &FnDecl{
		Name:   "id",
		Return: TInt,
		Params: []*Param{{Name: "x", Type: TInt}},
		Body: &Block{Stmts: []Stmt{
			&ReturnStmt{Value: &Ident{Name: "x", Kind: IdentParam, T: TInt}},
		}},
	}
	cp, ok := Clone(orig).(*FnDecl)
	if !ok || cp == orig {
		t.Fatalf("expected fresh *FnDecl clone")
	}
	if cp.Body == orig.Body {
		t.Fatalf("Body pointer should not be shared")
	}
	if cp.Params[0] == orig.Params[0] {
		t.Fatalf("Param pointer should not be shared")
	}
	// Mutate the clone — original must stay intact.
	cp.Body.Stmts = append(cp.Body.Stmts, &BreakStmt{})
	if got := len(orig.Body.Stmts); got != 1 {
		t.Fatalf("original body mutated (len=%d)", got)
	}
	cp.Name = "id_Int"
	if orig.Name != "id" {
		t.Fatalf("original name mutated: %q", orig.Name)
	}
}

func TestCloneModuleDeclsNotShared(t *testing.T) {
	fn := &FnDecl{Name: "f", Return: TUnit, Body: &Block{}}
	mod := &Module{Package: "main", Decls: []Decl{fn}}
	cp, ok := Clone(mod).(*Module)
	if !ok || cp == mod {
		t.Fatalf("expected fresh *Module clone")
	}
	if len(cp.Decls) != 1 || cp.Decls[0] == fn {
		t.Fatalf("Decls element pointer shared with original")
	}
	cp.Decls = append(cp.Decls, &FnDecl{Name: "g", Return: TUnit})
	if got := len(mod.Decls); got != 1 {
		t.Fatalf("original Decls mutated (len=%d)", got)
	}
}

func TestCloneIdentCarriesTypeArgs(t *testing.T) {
	// Bare turbofish (`f::<Int>`) attaches type args to an Ident. Clone
	// must not share the TypeArgs slice.
	orig := &Ident{Name: "f", Kind: IdentFn, TypeArgs: []Type{&NamedType{Name: "X"}}, T: ErrTypeVal}
	cp, ok := Clone(orig).(*Ident)
	if !ok || cp == orig {
		t.Fatalf("expected fresh Ident clone")
	}
	if len(cp.TypeArgs) != 1 {
		t.Fatalf("TypeArgs lost: %+v", cp.TypeArgs)
	}
	if cp.TypeArgs[0] == orig.TypeArgs[0] {
		t.Fatalf("non-primitive TypeArgs element shared with original")
	}
}
