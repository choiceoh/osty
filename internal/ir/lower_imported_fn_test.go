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
func TestLowerImportedTypeReprCoversBuiltinShapes(t *testing.T) {
	intRepr := api.TypeRepr{Kind: "primitive", Name: "Int"}
	stringRepr := api.TypeRepr{Kind: "primitive", Name: "String"}
	if got := lowerImportedTypeRepr(&intRepr); got != TInt {
		t.Fatalf("primitive Int -> %v, want TInt", got)
	}
	listIntRepr := api.TypeRepr{Kind: "named", Name: "List", Args: []api.TypeRepr{intRepr}}
	got := lowerImportedTypeRepr(&listIntRepr)
	nt, ok := got.(*NamedType)
	if !ok || nt.Name != "List" || !nt.Builtin || len(nt.Args) != 1 || nt.Args[0] != TInt {
		t.Fatalf("List<Int> -> %v, want NamedType{Name:List, Builtin:true, Args:[TInt]}", got)
	}
	resultRepr := api.TypeRepr{Kind: "named", Name: "Result", Args: []api.TypeRepr{intRepr, stringRepr}}
	got = lowerImportedTypeRepr(&resultRepr)
	nt, ok = got.(*NamedType)
	if !ok || nt.Name != "Result" || !nt.Builtin || len(nt.Args) != 2 {
		t.Fatalf("Result<Int, String> -> %v, want builtin Result", got)
	}
	optRepr := api.TypeRepr{Kind: "optional", Args: []api.TypeRepr{intRepr}}
	got = lowerImportedTypeRepr(&optRepr)
	ot, ok := got.(*OptionalType)
	if !ok || ot.Inner != TInt {
		t.Fatalf("Optional<Int> -> %v, want OptionalType{Inner:TInt}", got)
	}
	unknownRepr := api.TypeRepr{Kind: "named", Name: "MyStruct"}
	got = lowerImportedTypeRepr(&unknownRepr)
	nt, ok = got.(*NamedType)
	if !ok || nt.Name != "MyStruct" || nt.Builtin {
		t.Fatalf("MyStruct (user) -> %v, want NamedType{Name:MyStruct, Builtin:false}", got)
	}
}
