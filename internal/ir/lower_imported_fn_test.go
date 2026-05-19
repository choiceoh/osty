package ir

import (
	"testing"

	"github.com/osty/osty/internal/selfhost/api"
)

// TestLowerImportedFnRoundTripsScalarSignature locks the Task B
// import-surface → FnDecl conversion for a simple no-arg / single-arg
// scalar signature. The dep's free fn `frontInvalidTypeRepr() -> FrontTypeRepr`
// arrives via PackageCheckFn (the Go-side checker boundary) and must
// land as an `*ir.FnDecl` whose Name + Params + Return can be read
// back by MIR's `useDeclFnType`.
func TestLowerImportedFnRoundTripsScalarSignature(t *testing.T) {
	l := &lowerer{}
	intRepr := api.TypeRepr{Kind: "primitive", Name: "Int"}
	stringRepr := api.TypeRepr{Kind: "primitive", Name: "String"}
	cases := []struct {
		name       string
		fn         api.PackageCheckFn
		wantName   string
		wantParams int
		wantRet    Type
	}{
		{
			name: "no-arg unit-return",
			fn: api.PackageCheckFn{
				Name:       "ping",
				ReturnType: "()",
			},
			wantName:   "ping",
			wantParams: 0,
			wantRet:    TUnit,
		},
		{
			name: "single-arg Int -> Int",
			fn: api.PackageCheckFn{
				Name:           "double",
				ParamNames:     []string{"n"},
				ParamTypeReprs: []api.TypeRepr{intRepr},
				ReturnTypeRepr: &intRepr,
			},
			wantName:   "double",
			wantParams: 1,
			wantRet:    TInt,
		},
		{
			name: "trailing-default ? prefix stripped",
			fn: api.PackageCheckFn{
				Name:           "withDefault",
				ParamNames:     []string{"x", "?y"},
				ParamTypeReprs: []api.TypeRepr{intRepr, intRepr},
				ReturnTypeRepr: &stringRepr,
			},
			wantName:   "withDefault",
			wantParams: 2,
			wantRet:    TString,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := l.lowerImportedFn(&tc.fn)
			if got == nil {
				t.Fatalf("lowerImportedFn returned nil for %+v", tc.fn)
			}
			if got.Name != tc.wantName {
				t.Fatalf("Name = %q, want %q", got.Name, tc.wantName)
			}
			if len(got.Params) != tc.wantParams {
				t.Fatalf("Params length = %d, want %d", len(got.Params), tc.wantParams)
			}
			if got.Return.String() != tc.wantRet.String() {
				t.Fatalf("Return = %v, want %v", got.Return, tc.wantRet)
			}
			// Trailing-default `?y` must be stripped on the bare Param.Name.
			for _, p := range got.Params {
				if len(p.Name) > 0 && p.Name[0] == '?' {
					t.Fatalf("Param %q still carries `?` prefix; should be stripped", p.Name)
				}
			}
		})
	}
}

// TestLowerImportedFnSkipsAliasMethodForm locks the
// `import_surface_arena.go:225-229` duality: the arena walker
// registers each free fn twice — once with `Owner == ""` (free) and
// once with `Owner == alias` (alias-method). UseDecl.Imports should
// only receive the free-fn entry; alias-method dispatch goes through
// the receiver-type method lookup in MIR, not through useDeclFnType.
func TestLowerImportedFnSkipsAliasMethodForm(t *testing.T) {
	l := &lowerer{}
	freeFn := api.PackageCheckFn{Name: "make", Owner: ""}
	aliasMethod := api.PackageCheckFn{Name: "make", Owner: "dep"}

	if got := l.lowerImportedFn(&freeFn); got == nil {
		t.Fatal("lowerImportedFn dropped free-fn form (Owner=\"\")")
	}
	if got := l.lowerImportedFn(&aliasMethod); got != nil {
		t.Fatalf("lowerImportedFn populated alias-method form: got %+v, want nil", got)
	}
}

// TestLowerImportedTypeReprCoversBuiltinShapes locks the type-repr
// → ir.Type mapping for the receiver shapes MIR's
// `recoveredMethodReturnType` and friends key on. Builtin generics
// (Option / Result / List / Map / Set) must carry `Builtin: true` so
// downstream lookups match the existing arms; primitive types map to
// the canonical singletons.
//
// Wire-format reminders (per internal/selfhost/api/types.go):
//   - `optional`'s inner type lives in `Return`, NOT `Args[0]`.
//   - `fn`'s return type lives in `Return`; param types are `Args`.
//   - `named`'s `Name` may carry a qualified prefix (`dep.Foo`) that
//     must be split into `NamedType.Package` + `NamedType.Name`.
func TestLowerImportedTypeReprCoversBuiltinShapes(t *testing.T) {
	intRepr := api.TypeRepr{Kind: "primitive", Name: "Int"}
	stringRepr := api.TypeRepr{Kind: "primitive", Name: "String"}
	if got := lowerImportedTypeRepr(&intRepr); got != TInt {
		t.Fatalf("primitive Int -> %v, want TInt", got)
	}
	listIntRepr := api.TypeRepr{Kind: "named", Name: "List", Args: []api.TypeRepr{intRepr}}
	got := lowerImportedTypeRepr(&listIntRepr)
	nt, ok := got.(*NamedType)
	if !ok || nt.Name != "List" || nt.Package != "" || !nt.Builtin || len(nt.Args) != 1 || nt.Args[0] != TInt {
		t.Fatalf("List<Int> -> %v, want NamedType{Name:List, Builtin:true, Args:[TInt]}", got)
	}
	resultRepr := api.TypeRepr{Kind: "named", Name: "Result", Args: []api.TypeRepr{intRepr, stringRepr}}
	got = lowerImportedTypeRepr(&resultRepr)
	nt, ok = got.(*NamedType)
	if !ok || nt.Name != "Result" || !nt.Builtin || len(nt.Args) != 2 {
		t.Fatalf("Result<Int, String> -> %v, want builtin Result", got)
	}
	// Optional inner lives in `Return`, NOT `Args[0]` — matches the
	// production wire format from `internal/selfhost/api/types.go:49`
	// and the renderer's `tr.Return.String()` dereference.
	optRepr := api.TypeRepr{Kind: "optional", Return: &intRepr}
	got = lowerImportedTypeRepr(&optRepr)
	ot, ok := got.(*OptionalType)
	if !ok || ot.Inner != TInt {
		t.Fatalf("Optional<Int> -> %v, want OptionalType{Inner:TInt}", got)
	}
	// Optional with nil Return defaults inner to ErrTypeVal so the
	// wrapper still surfaces as Optional (recovery in MIR can still
	// see "this is an Option<?>", just not what's inside).
	optBareRepr := api.TypeRepr{Kind: "optional"}
	got = lowerImportedTypeRepr(&optBareRepr)
	if ot, ok = got.(*OptionalType); !ok || ot.Inner != ErrTypeVal {
		t.Fatalf("Optional<?> with nil Return -> %v, want OptionalType{Inner:ErrTypeVal}", got)
	}
	unknownRepr := api.TypeRepr{Kind: "named", Name: "MyStruct"}
	got = lowerImportedTypeRepr(&unknownRepr)
	nt, ok = got.(*NamedType)
	if !ok || nt.Name != "MyStruct" || nt.Package != "" || nt.Builtin {
		t.Fatalf("MyStruct (user, unqualified) -> %v, want NamedType{Name:MyStruct, Builtin:false}", got)
	}
	// Qualified name `dep.Foo` must split into Package + Name so the
	// downstream IR invariants (`NamedType.String()`, alias lookups)
	// hold. Per the API contract, `Path` stays empty and the dot lives
	// in `Name`.
	qualifiedRepr := api.TypeRepr{Kind: "named", Name: "dep.Foo"}
	got = lowerImportedTypeRepr(&qualifiedRepr)
	nt, ok = got.(*NamedType)
	if !ok || nt.Package != "dep" || nt.Name != "Foo" || nt.Builtin {
		t.Fatalf("dep.Foo -> %v, want NamedType{Package:dep, Name:Foo, Builtin:false}", got)
	}
	// A user type named "dep.List" must NOT be tagged `Builtin: true`
	// — only the unqualified prelude names earn that flag.
	depListRepr := api.TypeRepr{Kind: "named", Name: "dep.List"}
	got = lowerImportedTypeRepr(&depListRepr)
	nt, ok = got.(*NamedType)
	if !ok || nt.Package != "dep" || nt.Name != "List" || nt.Builtin {
		t.Fatalf("dep.List -> %v, want non-builtin qualified", got)
	}
	// Unit / tuple / fn — the other kinds an import surface can emit
	// for fn-typed params or struct field types.
	unitRepr := api.TypeRepr{Kind: "unit"}
	if got := lowerImportedTypeRepr(&unitRepr); got != TUnit {
		t.Fatalf("unit -> %v, want TUnit", got)
	}
	tupleRepr := api.TypeRepr{Kind: "tuple", Args: []api.TypeRepr{intRepr, stringRepr}}
	got = lowerImportedTypeRepr(&tupleRepr)
	tt, ok := got.(*TupleType)
	if !ok || len(tt.Elems) != 2 || tt.Elems[0] != TInt || tt.Elems[1] != TString {
		t.Fatalf("(Int, String) -> %v, want TupleType{Elems:[TInt, TString]}", got)
	}
	fnRepr := api.TypeRepr{
		Kind:       "fn",
		Args:       []api.TypeRepr{intRepr},
		Return:     &stringRepr,
		ParamNames: []string{"n"},
	}
	got = lowerImportedTypeRepr(&fnRepr)
	ft, ok := got.(*FnType)
	if !ok || len(ft.Params) != 1 || ft.Params[0] != TInt || ft.Return != TString {
		t.Fatalf("fn(Int) -> String -> %v, want FnType{Params:[TInt], Return:TString}", got)
	}
	if len(ft.ParamNames) != 1 || ft.ParamNames[0] != "n" {
		t.Fatalf("fn ParamNames = %v, want [n]", ft.ParamNames)
	}
}
