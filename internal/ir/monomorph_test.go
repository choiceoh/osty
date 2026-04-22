package ir

import (
	"strings"
	"testing"
)

// genericIdentity constructs an `fn id<T>(x: T) -> T { x }` declaration.
// Exposed as a helper so the individual tests stay focused on what they
// exercise rather than on boilerplate IR construction.
func genericIdentity() *FnDecl {
	tv := &TypeVar{Name: "T"}
	return &FnDecl{
		Name:     "id",
		Generics: []*TypeParam{{Name: "T"}},
		Params:   []*Param{{Name: "x", Type: tv}},
		Return:   tv,
		Body: &Block{
			Result: &Ident{Name: "x", Kind: IdentParam, T: tv},
		},
	}
}

// genericCall produces an `id::<T>(value)` call expression used inside a
// caller's body.
func genericCall(typeArg Type, value Expr, resultT Type) *CallExpr {
	return &CallExpr{
		Callee:   &Ident{Name: "id", Kind: IdentFn, T: ErrTypeVal},
		TypeArgs: []Type{typeArg},
		Args:     []Arg{{Value: value}},
		T:        resultT,
	}
}

// ==== Module-level shape ====

func TestMonomorphizeNilModule(t *testing.T) {
	out, errs := Monomorphize(nil)
	if out != nil {
		t.Fatalf("expected nil module for nil input, got %+v", out)
	}
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
}

func TestMonomorphizeEmptyModulePassesThrough(t *testing.T) {
	in := &Module{Package: "main"}
	out, errs := Monomorphize(in)
	if out == nil {
		t.Fatalf("expected non-nil output module")
	}
	if out.Package != "main" {
		t.Fatalf("expected package copy, got %q", out.Package)
	}
	if len(out.Decls) != 0 || len(out.Script) != 0 {
		t.Fatalf("expected empty output, got Decls=%d Script=%d", len(out.Decls), len(out.Script))
	}
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
}

func TestMonomorphizeUnusedGenericIsDropped(t *testing.T) {
	// A generic free fn with no concrete callers must not end up in the
	// output — demand-driven spec semantics.
	in := &Module{Package: "main", Decls: []Decl{genericIdentity()}}
	out, errs := Monomorphize(in)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(out.Decls) != 0 {
		t.Fatalf("expected 0 decls after dropping unused generic, got %d: %+v", len(out.Decls), out.Decls)
	}
}

// ==== Single specialization ====

func TestMonomorphizeSingleFreeFnSpecialization(t *testing.T) {
	caller := &FnDecl{
		Name:   "main",
		Return: TUnit,
		Body: &Block{Stmts: []Stmt{
			&ExprStmt{X: genericCall(TInt, intLit("5"), TInt)},
		}},
	}
	in := &Module{Package: "main", Decls: []Decl{genericIdentity(), caller}}
	out, errs := Monomorphize(in)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}

	// Expected layout: caller followed by the specialization at the tail.
	if got := len(out.Decls); got != 2 {
		t.Fatalf("expected 2 output decls, got %d", got)
	}
	mainCp, ok := out.Decls[0].(*FnDecl)
	if !ok || mainCp.Name != "main" {
		t.Fatalf("expected main at [0], got %T (%v)", out.Decls[0], out.Decls[0])
	}
	spec, ok := out.Decls[1].(*FnDecl)
	if !ok {
		t.Fatalf("expected FnDecl specialization at [1], got %T", out.Decls[1])
	}
	if len(spec.Generics) != 0 {
		t.Fatalf("specialization must shed generics, got %d", len(spec.Generics))
	}
	if spec.Params[0].Type != TInt || spec.Return != TInt {
		t.Fatalf("specialization signature not substituted: params[0]=%T return=%T",
			spec.Params[0].Type, spec.Return)
	}
	if bodyIdent, ok := spec.Body.Result.(*Ident); !ok || bodyIdent.T != TInt {
		t.Fatalf("specialization body ident type not concrete")
	}

	callCp := mainCp.Body.Stmts[0].(*ExprStmt).X.(*CallExpr)
	if len(callCp.TypeArgs) != 0 {
		t.Fatalf("call site TypeArgs should be cleared, got %+v", callCp.TypeArgs)
	}
	id, ok := callCp.Callee.(*Ident)
	if !ok {
		t.Fatalf("call callee should be Ident, got %T", callCp.Callee)
	}
	if id.Name != spec.Name {
		t.Fatalf("call site name not rewritten: got %q, expected %q", id.Name, spec.Name)
	}
	// Original generic fn must be untouched — Monomorphize returns a new
	// Module rather than mutating the input.
	origFn := in.Decls[0].(*FnDecl)
	if origFn.Name != "id" || len(origFn.Generics) != 1 {
		t.Fatalf("original module mutated: %+v", origFn)
	}
}

// ==== Dedup ====

func TestMonomorphizeDedupesSameTypeArgs(t *testing.T) {
	// Two `id::<Int>(...)` calls must share one specialization.
	caller := &FnDecl{
		Name:   "main",
		Return: TUnit,
		Body: &Block{Stmts: []Stmt{
			&ExprStmt{X: genericCall(TInt, intLit("5"), TInt)},
			&ExprStmt{X: genericCall(TInt, intLit("6"), TInt)},
		}},
	}
	in := &Module{Package: "main", Decls: []Decl{genericIdentity(), caller}}
	out, _ := Monomorphize(in)
	specCount := 0
	var mangled string
	for _, d := range out.Decls {
		fn, ok := d.(*FnDecl)
		if !ok || fn.Name == "main" {
			continue
		}
		specCount++
		mangled = fn.Name
	}
	if specCount != 1 {
		t.Fatalf("expected exactly 1 specialization for duplicate calls, got %d", specCount)
	}
	mainCp := out.Decls[0].(*FnDecl)
	for i, stmt := range mainCp.Body.Stmts {
		id := stmt.(*ExprStmt).X.(*CallExpr).Callee.(*Ident)
		if id.Name != mangled {
			t.Fatalf("call %d: want mangled %q, got %q", i, mangled, id.Name)
		}
	}
}

func TestMonomorphizeDistinctTypeArgsMakeTwoSpecializations(t *testing.T) {
	caller := &FnDecl{
		Name:   "main",
		Return: TUnit,
		Body: &Block{Stmts: []Stmt{
			&ExprStmt{X: genericCall(TInt, intLit("5"), TInt)},
			&ExprStmt{X: genericCall(TString, strLit("hi"), TString)},
		}},
	}
	in := &Module{Package: "main", Decls: []Decl{genericIdentity(), caller}}
	out, _ := Monomorphize(in)
	var mangledNames []string
	for _, d := range out.Decls {
		if fn, ok := d.(*FnDecl); ok && fn.Name != "main" {
			mangledNames = append(mangledNames, fn.Name)
		}
	}
	if len(mangledNames) != 2 {
		t.Fatalf("expected 2 specializations for distinct type args, got %d (%v)", len(mangledNames), mangledNames)
	}
	if mangledNames[0] == mangledNames[1] {
		t.Fatalf("specializations must have distinct mangled names, got duplicate %q", mangledNames[0])
	}
}

// ==== Non-generic paths ====

func TestMonomorphizePreservesNonGenericDecls(t *testing.T) {
	// A plain fn with a plain call must be copied as-is with no extra
	// specialization artifacts.
	f := &FnDecl{
		Name:   "add1",
		Return: TInt,
		Params: []*Param{{Name: "x", Type: TInt}},
		Body: &Block{Result: &BinaryExpr{
			Op:    BinAdd,
			Left:  &Ident{Name: "x", Kind: IdentParam, T: TInt},
			Right: intLit("1"),
			T:     TInt,
		}},
	}
	in := &Module{Package: "main", Decls: []Decl{f}}
	out, errs := Monomorphize(in)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(out.Decls) != 1 {
		t.Fatalf("expected 1 decl preserved, got %d", len(out.Decls))
	}
	cp := out.Decls[0].(*FnDecl)
	if cp == f {
		t.Fatalf("non-generic fn should be cloned, not aliased")
	}
	if cp.Name != "add1" || len(cp.Generics) != 0 {
		t.Fatalf("unexpected specialization on non-generic fn: %+v", cp)
	}
}

// ==== Nested generic calls inside a specialization body ====

func TestMonomorphizeNestedGenericCallDiscoversAdditionalSpecialization(t *testing.T) {
	// fn id<T>(x: T) -> T { x }
	// fn wrap<U>(y: U) -> U { id::<U>(y) }
	// fn main() { wrap::<Int>(5) }
	// → two specializations: id_Int and wrap_Int.
	idFn := genericIdentity()
	wrap := &FnDecl{
		Name:     "wrap",
		Generics: []*TypeParam{{Name: "U"}},
		Params:   []*Param{{Name: "y", Type: &TypeVar{Name: "U"}}},
		Return:   &TypeVar{Name: "U"},
		Body: &Block{Result: &CallExpr{
			Callee:   &Ident{Name: "id", Kind: IdentFn, T: ErrTypeVal},
			TypeArgs: []Type{&TypeVar{Name: "U"}},
			Args:     []Arg{{Value: &Ident{Name: "y", Kind: IdentParam, T: &TypeVar{Name: "U"}}}},
			T:        &TypeVar{Name: "U"},
		}},
	}
	caller := &FnDecl{
		Name:   "main",
		Return: TUnit,
		Body: &Block{Stmts: []Stmt{&ExprStmt{X: &CallExpr{
			Callee:   &Ident{Name: "wrap", Kind: IdentFn, T: ErrTypeVal},
			TypeArgs: []Type{TInt},
			Args:     []Arg{{Value: intLit("5")}},
			T:        TInt,
		}}}},
	}
	in := &Module{Package: "main", Decls: []Decl{idFn, wrap, caller}}
	out, errs := Monomorphize(in)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	specCount := 0
	sawIdInt := false
	sawWrapInt := false
	for _, d := range out.Decls {
		fn, ok := d.(*FnDecl)
		if !ok || fn.Name == "main" {
			continue
		}
		specCount++
		switch {
		case strings.HasPrefix(fn.Name, "_Z") && strings.Contains(fn.Name, "2id"):
			sawIdInt = true
		case strings.HasPrefix(fn.Name, "_Z") && strings.Contains(fn.Name, "4wrap"):
			sawWrapInt = true
		}
	}
	if specCount != 2 {
		t.Fatalf("expected 2 specializations (wrap + nested id), got %d", specCount)
	}
	if !sawIdInt || !sawWrapInt {
		t.Fatalf("expected both id and wrap specializations; sawId=%v sawWrap=%v", sawIdInt, sawWrapInt)
	}
}

// ==== Builtin aggregate type argument ====

func TestMonomorphizeBuiltinListTypeArg(t *testing.T) {
	// id::<List<Int>>(xs) — aggregate type code should be accepted.
	listInt := &NamedType{Name: "List", Args: []Type{TInt}, Builtin: true}
	caller := &FnDecl{
		Name:   "main",
		Return: TUnit,
		Body: &Block{Stmts: []Stmt{&ExprStmt{X: &CallExpr{
			Callee:   &Ident{Name: "id", Kind: IdentFn, T: ErrTypeVal},
			TypeArgs: []Type{listInt},
			Args:     []Arg{{Value: &ListLit{Elem: TInt}}},
			T:        listInt,
		}}}},
	}
	in := &Module{Package: "main", Decls: []Decl{genericIdentity(), caller}}
	out, errs := Monomorphize(in)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	specCount := 0
	for _, d := range out.Decls {
		if fn, ok := d.(*FnDecl); ok && fn.Name != "main" {
			specCount++
			// Substituted parameter should be the list aggregate, not a
			// TypeVar.
			if _, isVar := fn.Params[0].Type.(*TypeVar); isVar {
				t.Fatalf("specialization param still a TypeVar: %+v", fn.Params[0].Type)
			}
		}
	}
	if specCount != 1 {
		t.Fatalf("expected 1 List<Int> specialization, got %d", specCount)
	}
}

// ==== Error paths ====

func TestMonomorphizeArityMismatchRecordsError(t *testing.T) {
	// fn f<T, U>(x: T) { … } — call supplies only one type arg.
	fn := &FnDecl{
		Name:     "f",
		Generics: []*TypeParam{{Name: "T"}, {Name: "U"}},
		Params:   []*Param{{Name: "x", Type: &TypeVar{Name: "T"}}},
		Return:   TUnit,
		Body:     &Block{},
	}
	caller := &FnDecl{
		Name:   "main",
		Return: TUnit,
		Body: &Block{Stmts: []Stmt{&ExprStmt{X: &CallExpr{
			Callee:   &Ident{Name: "f", Kind: IdentFn, T: ErrTypeVal},
			TypeArgs: []Type{TInt},
			Args:     []Arg{{Value: intLit("5")}},
			T:        TUnit,
		}}}},
	}
	in := &Module{Package: "main", Decls: []Decl{fn, caller}}
	_, errs := Monomorphize(in)
	if len(errs) == 0 {
		t.Fatalf("expected arity-mismatch error, got none")
	}
	if !strings.Contains(errs[0].Error(), "arity mismatch") {
		t.Fatalf("expected error to mention arity mismatch, got %q", errs[0].Error())
	}
}

func TestMonomorphizeLingeringTypeVarRecordsError(t *testing.T) {
	// A caller that is itself generic would leave a TypeVar in the type
	// args when an outer specialization tries to request with an unbound
	// param. We simulate this directly: the top-level caller is generic,
	// so Monomorphize never enters it, but its body still sits in the
	// input. We call a generic fn with a TypeVar that's not in env from
	// inside a *non-generic* fn — the rewriter should flag it.
	tv := &TypeVar{Name: "T"}
	caller := &FnDecl{
		Name:   "main",
		Return: TUnit,
		Body: &Block{Stmts: []Stmt{&ExprStmt{X: &CallExpr{
			Callee:   &Ident{Name: "id", Kind: IdentFn, T: ErrTypeVal},
			TypeArgs: []Type{tv}, // unbound in caller — bug case
			Args:     []Arg{{Value: intLit("5")}},
			T:        tv,
		}}}},
	}
	in := &Module{Package: "main", Decls: []Decl{genericIdentity(), caller}}
	_, errs := Monomorphize(in)
	if len(errs) == 0 {
		t.Fatalf("expected lingering-TypeVar error, got none")
	}
	joined := ""
	for _, e := range errs {
		joined += e.Error() + "\n"
	}
	if !strings.Contains(joined, "type variable") {
		t.Fatalf("expected error to mention type variable, got %q", joined)
	}
}

// ==== Mangled symbol validity ====

func TestMonomorphizeMangledNameIsValidLLVMIdent(t *testing.T) {
	caller := &FnDecl{
		Name:   "main",
		Return: TUnit,
		Body: &Block{Stmts: []Stmt{
			&ExprStmt{X: genericCall(TInt, intLit("5"), TInt)},
		}},
	}
	in := &Module{Package: "main", Decls: []Decl{genericIdentity(), caller}}
	out, _ := Monomorphize(in)
	for _, d := range out.Decls {
		fn, ok := d.(*FnDecl)
		if !ok || fn.Name == "main" {
			continue
		}
		if !isSimpleIdent(fn.Name) {
			t.Fatalf("specialization name %q is not a simple identifier", fn.Name)
		}
		if !strings.HasPrefix(fn.Name, "_Z") {
			t.Fatalf("specialization name %q missing Itanium `_Z` prefix", fn.Name)
		}
	}
}

// isSimpleIdent mirrors the subset LLVM will accept for bare global names.
func isSimpleIdent(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r == '_':
		case 'a' <= r && r <= 'z':
		case 'A' <= r && r <= 'Z':
		case i > 0 && '0' <= r && r <= '9':
		default:
			return false
		}
	}
	return true
}

// ==== Methods on types ====

func TestMonomorphizeScansStructMethodBodies(t *testing.T) {
	// A struct method body that contains a generic call site must still
	// get rewritten and must add a specialization to the output.
	method := &FnDecl{
		Name:   "use",
		Return: TInt,
		Params: []*Param{{Name: "self", Type: &NamedType{Name: "S"}}},
		Body:   &Block{Result: genericCall(TInt, intLit("7"), TInt)},
	}
	sd := &StructDecl{Name: "S", Methods: []*FnDecl{method}}
	in := &Module{Package: "main", Decls: []Decl{genericIdentity(), sd}}
	out, errs := Monomorphize(in)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	// Struct copied over + 1 specialization for id::<Int>.
	specCount := 0
	var structCp *StructDecl
	for _, d := range out.Decls {
		switch d := d.(type) {
		case *StructDecl:
			structCp = d
		case *FnDecl:
			specCount++
		}
	}
	if structCp == nil {
		t.Fatalf("struct decl missing from output")
	}
	if specCount != 1 {
		t.Fatalf("expected 1 specialization from method body scan, got %d", specCount)
	}
	// Method body call must be rewritten.
	bodyCall := structCp.Methods[0].Body.Result.(*CallExpr)
	if len(bodyCall.TypeArgs) != 0 {
		t.Fatalf("method body call TypeArgs should be cleared")
	}
	id := bodyCall.Callee.(*Ident)
	if !strings.HasPrefix(id.Name, "_Z") {
		t.Fatalf("method body call not rewritten to mangled name: %q", id.Name)
	}
}

// ==== Script-level call sites ====

func TestMonomorphizeScansTopLevelScript(t *testing.T) {
	// Top-level `id::<Int>(5)` (not inside a fn) must also drive
	// specialization and get rewritten.
	in := &Module{
		Package: "main",
		Decls:   []Decl{genericIdentity()},
		Script:  []Stmt{&ExprStmt{X: genericCall(TInt, intLit("5"), TInt)}},
	}
	out, errs := Monomorphize(in)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(out.Script) != 1 {
		t.Fatalf("expected one script stmt, got %d", len(out.Script))
	}
	call := out.Script[0].(*ExprStmt).X.(*CallExpr)
	if len(call.TypeArgs) != 0 {
		t.Fatalf("script call TypeArgs not cleared: %+v", call.TypeArgs)
	}
	id := call.Callee.(*Ident)
	if !strings.HasPrefix(id.Name, "_Z") {
		t.Fatalf("script call not rewritten, got %q", id.Name)
	}
	// Specialization must sit in Decls.
	specCount := 0
	for _, d := range out.Decls {
		if _, ok := d.(*FnDecl); ok {
			specCount++
		}
	}
	if specCount != 1 {
		t.Fatalf("expected 1 specialization from script scan, got %d", specCount)
	}
}

// ==== Phase 2 mangling helpers (Osty policy snapshot) ====

func TestMonomorphMangleTypePairIntInt(t *testing.T) {
	req := NewMonomorphTypeRequest("main", "Pair", []string{"l", "l"})
	got := MonomorphMangleType(req).Symbol()
	const want = "_ZTSN4main4PairIllEE"
	if got != want {
		t.Fatalf("MonomorphMangleType(Pair<Int,Int>): got %q, want %q", got, want)
	}
}

func TestMonomorphMangleTypeEnumMaybeInt(t *testing.T) {
	req := NewMonomorphTypeRequest("main", "Maybe", []string{"l"})
	got := MonomorphMangleType(req).Symbol()
	const want = "_ZTSN4main5MaybeIlEE"
	if got != want {
		t.Fatalf("MonomorphMangleType(Maybe<Int>): got %q, want %q", got, want)
	}
}

func TestMonomorphMangleTypeNamePartMatches(t *testing.T) {
	// The symbol must be decomposable as `_ZTS` + <nested-template-name>.
	req := NewMonomorphTypeRequest("main", "Box", []string{"b"})
	sym := MonomorphMangleType(req).Symbol()
	body := MonomorphMangleTypeName("main", "Box", []string{"b"})
	if sym != "_ZTS"+body {
		t.Fatalf("symbol %q not equal to _ZTS + body %q", sym, body)
	}
}

func TestMonomorphMangleTypeEmptyPkgDefaultsToMain(t *testing.T) {
	// Script files lower with Package="" — type symbols must still carry
	// a package segment so demanglers print a qualified name.
	got := MonomorphMangleTypeName("", "Box", []string{"l"})
	const want = "N4main3BoxIlEE"
	if got != want {
		t.Fatalf("MonomorphMangleTypeName with blank pkg: got %q, want %q", got, want)
	}
}

func TestMonomorphUserTemplateNestedNestedGeneric(t *testing.T) {
	// User-declared generic referenced inside another type-code position
	// (e.g. id::<Pair<Int, Int>>) must produce an `I…E` template wrap.
	argCodes := "ll"
	got := MonomorphUserTemplateNested("main", "Pair", argCodes)
	const want = "N4main4PairIllEE"
	if got != want {
		t.Fatalf("MonomorphUserTemplateNested: got %q, want %q", got, want)
	}
}

func TestMonomorphTypeDedupeKeyDistinctFromFn(t *testing.T) {
	fnKey := MonomorphDedupeKey("Pair", "main", []string{"l", "l"})
	typeKey := MonomorphTypeDedupeKey("Pair", "main", []string{"l", "l"})
	if fnKey == typeKey {
		t.Fatalf("fn and type dedupe keys must not collide for same name/args\nfn:   %q\ntype: %q", fnKey, typeKey)
	}
	// Sanity: the type key carries the "type" segment so the distinction
	// is visible to anyone debugging the engine.
	if !strings.Contains(typeKey, "\x1ftype\x1f") {
		t.Fatalf("type dedupe key should contain the `type` marker, got %q", typeKey)
	}
}

// ==== Phase 2 typeCodeOf user-generic extension ====

func TestTypeCodeOfUserGenericNamedType(t *testing.T) {
	// A user-declared `Pair<Int, Int>` reference routes through the new
	// MonomorphUserTemplateNested branch so fn symbols that take it as a
	// type argument stay unique per concrete instantiation.
	pair := &NamedType{Name: "Pair", Args: []Type{TInt, TInt}}
	got := typeCodeOf(pair, "main")
	const want = "N4main4PairIllEE"
	if got != want {
		t.Fatalf("typeCodeOf(Pair<Int,Int>): got %q, want %q", got, want)
	}
}

func TestTypeCodeOfUserGenericDistinctArgs(t *testing.T) {
	a := typeCodeOf(&NamedType{Name: "Pair", Args: []Type{TInt, TBool}}, "main")
	b := typeCodeOf(&NamedType{Name: "Pair", Args: []Type{TBool, TInt}}, "main")
	if a == b {
		t.Fatalf("distinct generic args must produce distinct type codes, both %q", a)
	}
}

func TestTypeCodeOfNestedUserGeneric(t *testing.T) {
	// Maybe<Pair<Int, Bool>> — the inner Pair must expand recursively so
	// the outer encoding is decidable.
	inner := &NamedType{Name: "Pair", Args: []Type{TInt, TBool}}
	outer := &NamedType{Name: "Maybe", Args: []Type{inner}}
	got := typeCodeOf(outer, "main")
	const want = "N4main5MaybeIN4main4PairIlbEEEE"
	if got != want {
		t.Fatalf("typeCodeOf(Maybe<Pair<Int,Bool>>): got %q, want %q", got, want)
	}
}

func TestTypeCodeOfUserNamedWithoutArgsUnchanged(t *testing.T) {
	// A non-generic user struct reference should still hit the plain
	// MonomorphUserNested branch — the Phase 2 change must not disturb
	// existing fn-mangle symbols for non-generic user types.
	got := typeCodeOf(&NamedType{Name: "Point"}, "main")
	const want = "N4main5PointE"
	if got != want {
		t.Fatalf("typeCodeOf(Point): got %q, want %q", got, want)
	}
}

// ==== Phase 2 rewriteType helper ====

// newTestMonoState builds a blank monoState populated only with the
// index maps and package — enough for exercising rewriteType and the
// request* helpers in isolation.
func newTestMonoState(structs map[string]*StructDecl, enums map[string]*EnumDecl) *monoState {
	return &monoState{
		pkg:                  "main",
		out:                  &Module{Package: "main"},
		genericsByName:       map[string]*FnDecl{},
		seen:                 map[string]string{},
		genericStructsByName: structs,
		genericEnumsByName:   enums,
		typeSeen:             map[string]string{},
		structsByName:        map[string]*StructDecl{},
		enumsByName:          map[string]*EnumDecl{},
		structsByMangled:     map[string]*StructDecl{},
		enumsByMangled:       map[string]*EnumDecl{},
		originalStructOf:     map[string]*StructDecl{},
		originalEnumOf:       map[string]*EnumDecl{},
		receiverEnvOf:        map[string]SubstEnv{},
		methodSeen:           map[string]string{},
	}
}

func TestRewriteTypePassesThroughUnknownNamed(t *testing.T) {
	s := newTestMonoState(nil, nil)
	nt := &NamedType{Name: "Unknown", Args: []Type{TInt}}
	out := s.rewriteType(nt)
	if out != nt {
		t.Fatalf("unknown NamedType should be returned unchanged, got %T", out)
	}
	if len(s.typeQueue) != 0 {
		t.Fatalf("typeQueue should stay empty for unknown names, got %d", len(s.typeQueue))
	}
}

func TestRewriteTypeReplacesGenericStruct(t *testing.T) {
	pair := &StructDecl{
		Name:     "Pair",
		Generics: []*TypeParam{{Name: "T"}, {Name: "U"}},
		Fields: []*Field{
			{Name: "first", Type: &TypeVar{Name: "T"}},
			{Name: "second", Type: &TypeVar{Name: "U"}},
		},
	}
	s := newTestMonoState(map[string]*StructDecl{"Pair": pair}, nil)
	nt := &NamedType{Name: "Pair", Args: []Type{TInt, TInt}}
	out := s.rewriteType(nt)
	named, ok := out.(*NamedType)
	if !ok {
		t.Fatalf("expected NamedType after rewrite, got %T", out)
	}
	if !strings.HasPrefix(named.Name, "_ZTS") {
		t.Fatalf("rewrite should produce Itanium type-symbol, got %q", named.Name)
	}
	if len(named.Args) != 0 {
		t.Fatalf("mangled NamedType should drop Args, got %+v", named.Args)
	}
	if len(s.typeQueue) != 1 {
		t.Fatalf("expected one queued type spec, got %d", len(s.typeQueue))
	}
	if s.typeQueue[0].structDecl != pair {
		t.Fatalf("queued spec should reference the original decl")
	}
}

func TestRewriteTypeReplacesGenericEnum(t *testing.T) {
	maybe := &EnumDecl{
		Name:     "Maybe",
		Generics: []*TypeParam{{Name: "T"}},
		Variants: []*Variant{
			{Name: "Some", Payload: []Type{&TypeVar{Name: "T"}}},
			{Name: "None"},
		},
	}
	s := newTestMonoState(nil, map[string]*EnumDecl{"Maybe": maybe})
	nt := &NamedType{Name: "Maybe", Args: []Type{TInt}}
	out := s.rewriteType(nt)
	named, _ := out.(*NamedType)
	if named == nil || !strings.HasPrefix(named.Name, "_ZTS") {
		t.Fatalf("expected mangled enum reference, got %T %+v", out, out)
	}
	if len(s.typeQueue) != 1 || s.typeQueue[0].enumDecl != maybe {
		t.Fatalf("queued spec mismatch: %+v", s.typeQueue)
	}
}

func TestRewriteTypeDedupesRepeatedRequests(t *testing.T) {
	pair := &StructDecl{
		Name:     "Pair",
		Generics: []*TypeParam{{Name: "T"}, {Name: "U"}},
	}
	s := newTestMonoState(map[string]*StructDecl{"Pair": pair}, nil)
	first := s.rewriteType(&NamedType{Name: "Pair", Args: []Type{TInt, TInt}})
	second := s.rewriteType(&NamedType{Name: "Pair", Args: []Type{TInt, TInt}})
	if first.(*NamedType).Name != second.(*NamedType).Name {
		t.Fatalf("dedupe: repeated rewrite should produce identical mangled name, got %q vs %q",
			first.(*NamedType).Name, second.(*NamedType).Name)
	}
	if len(s.typeQueue) != 1 {
		t.Fatalf("dedupe: typeQueue should hold one entry, got %d", len(s.typeQueue))
	}
}

func TestRewriteTypeBottomUpNestedGeneric(t *testing.T) {
	pair := &StructDecl{
		Name:     "Pair",
		Generics: []*TypeParam{{Name: "T"}, {Name: "U"}},
	}
	maybe := &EnumDecl{
		Name:     "Maybe",
		Generics: []*TypeParam{{Name: "T"}},
	}
	s := newTestMonoState(
		map[string]*StructDecl{"Pair": pair},
		map[string]*EnumDecl{"Maybe": maybe},
	)
	// Pair<Maybe<Int>, Bool> — inner Maybe<Int> must specialize first so
	// Pair's type-arg codes reference the inner mangled symbol.
	inner := &NamedType{Name: "Maybe", Args: []Type{TInt}}
	outer := &NamedType{Name: "Pair", Args: []Type{inner, TBool}}
	out := s.rewriteType(outer)
	named, _ := out.(*NamedType)
	if named == nil {
		t.Fatalf("expected outer rewrite to produce NamedType, got %T", out)
	}
	if len(s.typeQueue) != 2 {
		t.Fatalf("expected two queued specs (inner + outer), got %d", len(s.typeQueue))
	}
	// The outer instance must have been enqueued *after* the inner one
	// because the engine encodes the inner mangled symbol as part of
	// the outer request's type-arg codes.
	if s.typeQueue[0].enumDecl != maybe {
		t.Fatalf("inner enum spec should be enqueued first, got %+v", s.typeQueue[0])
	}
	if s.typeQueue[1].structDecl != pair {
		t.Fatalf("outer struct spec should be enqueued second, got %+v", s.typeQueue[1])
	}
}

func TestRewriteTypeBuiltinPreservesArgs(t *testing.T) {
	// Builtin generics (List<T>, Option<T>) must NOT request
	// user-struct specializations — they stay as-is so the LLVM runtime
	// paths keep handling them.
	s := newTestMonoState(nil, nil)
	list := &NamedType{Name: "List", Args: []Type{TInt}, Builtin: true}
	out := s.rewriteType(list)
	if out != list {
		t.Fatalf("builtin NamedType must not be replaced, got %T", out)
	}
	if len(s.typeQueue) != 0 {
		t.Fatalf("builtin NamedType must not enqueue type specs")
	}
}

func TestRewriteTypeArityMismatchRecordsError(t *testing.T) {
	pair := &StructDecl{
		Name:     "Pair",
		Generics: []*TypeParam{{Name: "T"}, {Name: "U"}},
	}
	s := newTestMonoState(map[string]*StructDecl{"Pair": pair}, nil)
	// Only one type arg for a two-param generic.
	s.rewriteType(&NamedType{Name: "Pair", Args: []Type{TInt}})
	if len(s.errs) == 0 {
		t.Fatalf("expected arity-mismatch error, got none")
	}
}

// ==== Phase 2 integration — generic struct / enum specialization ====

// genericPairStruct returns `struct Pair<T, U> { first: T, second: U }`.
// Shared helper so the integration tests stay focused on behavior.
func genericPairStruct() *StructDecl {
	return &StructDecl{
		Name:     "Pair",
		Generics: []*TypeParam{{Name: "T"}, {Name: "U"}},
		Fields: []*Field{
			{Name: "first", Type: &TypeVar{Name: "T"}},
			{Name: "second", Type: &TypeVar{Name: "U"}},
		},
	}
}

// genericMaybeEnum returns `enum Maybe<T> { Some(T), None }`.
func genericMaybeEnum() *EnumDecl {
	return &EnumDecl{
		Name:     "Maybe",
		Generics: []*TypeParam{{Name: "T"}},
		Variants: []*Variant{
			{Name: "Some", Payload: []Type{&TypeVar{Name: "T"}}},
			{Name: "None"},
		},
	}
}

// findStructSpec returns the first mangled StructDecl in a module, or
// nil when none is present. Non-mangled structs (original unchanged
// declarations or non-generic user types) are skipped.
func findStructSpec(m *Module) *StructDecl {
	for _, d := range m.Decls {
		if sd, ok := d.(*StructDecl); ok && strings.HasPrefix(sd.Name, "_ZTS") {
			return sd
		}
	}
	return nil
}

// findEnumSpec returns the first mangled EnumDecl in a module.
func findEnumSpec(m *Module) *EnumDecl {
	for _, d := range m.Decls {
		if ed, ok := d.(*EnumDecl); ok && strings.HasPrefix(ed.Name, "_ZTS") {
			return ed
		}
	}
	return nil
}

// countSpecs returns how many mangled struct-or-enum specializations
// appear in the module output. Used by dedup/distinct tests.
func countSpecs(m *Module) int {
	n := 0
	for _, d := range m.Decls {
		switch x := d.(type) {
		case *StructDecl:
			if strings.HasPrefix(x.Name, "_ZTS") {
				n++
			}
		case *EnumDecl:
			if strings.HasPrefix(x.Name, "_ZTS") {
				n++
			}
		}
	}
	return n
}

func TestMonomorphizeGenericStructSpecialization(t *testing.T) {
	pair := genericPairStruct()
	pairT := &NamedType{Name: "Pair", Args: []Type{TInt, TInt}}
	lit := &StructLit{
		TypeName: "Pair",
		T:        pairT,
		Fields: []StructLitField{
			{Name: "first", Value: intLit("1")},
			{Name: "second", Value: intLit("2")},
		},
	}
	main := &FnDecl{
		Name:   "main",
		Return: TUnit,
		Body: &Block{Stmts: []Stmt{
			&LetStmt{Name: "p", Value: lit, Type: pairT},
		}},
	}
	in := &Module{Package: "main", Decls: []Decl{pair, main}}
	out, errs := Monomorphize(in)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	// Expected: main (copied) + 1 Pair specialization. Original generic
	// Pair must be dropped.
	spec := findStructSpec(out)
	if spec == nil {
		t.Fatalf("Pair specialization missing from output decls: %+v", out.Decls)
	}
	if spec.Generics != nil {
		t.Fatalf("specialization must shed generics, got %d", len(spec.Generics))
	}
	if spec.Fields[0].Type != TInt || spec.Fields[1].Type != TInt {
		t.Fatalf("fields not substituted: %+v", spec.Fields)
	}
	// Original Pair must be dropped.
	for _, d := range out.Decls {
		if sd, ok := d.(*StructDecl); ok && sd.Name == "Pair" {
			t.Fatalf("original generic Pair leaked into output")
		}
	}
}

func TestMonomorphizeGenericEnumSpecialization(t *testing.T) {
	maybe := genericMaybeEnum()
	maybeT := &NamedType{Name: "Maybe", Args: []Type{TInt}}
	lit := &VariantLit{
		Enum:    "Maybe",
		Variant: "Some",
		T:       maybeT,
		Args:    []Arg{{Value: intLit("42")}},
	}
	main := &FnDecl{
		Name:   "main",
		Return: TUnit,
		Body:   &Block{Stmts: []Stmt{&LetStmt{Name: "m", Value: lit, Type: maybeT}}},
	}
	in := &Module{Package: "main", Decls: []Decl{maybe, main}}
	out, errs := Monomorphize(in)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	spec := findEnumSpec(out)
	if spec == nil {
		t.Fatalf("Maybe specialization missing from output decls: %+v", out.Decls)
	}
	if spec.Generics != nil {
		t.Fatalf("specialization must shed generics")
	}
	// Some variant's payload must be concretized to Int.
	var some *Variant
	for _, v := range spec.Variants {
		if v.Name == "Some" {
			some = v
		}
	}
	if some == nil || len(some.Payload) != 1 || some.Payload[0] != TInt {
		t.Fatalf("Some variant payload not substituted to Int: %+v", some)
	}
}

func TestMonomorphizeStructLitRewrittenToMangled(t *testing.T) {
	pair := genericPairStruct()
	pairT := &NamedType{Name: "Pair", Args: []Type{TInt, TInt}}
	lit := &StructLit{
		TypeName: "Pair",
		T:        pairT,
		Fields: []StructLitField{
			{Name: "first", Value: intLit("1")},
			{Name: "second", Value: intLit("2")},
		},
	}
	main := &FnDecl{
		Name: "main", Return: TUnit,
		Body: &Block{Stmts: []Stmt{&ExprStmt{X: lit}}},
	}
	in := &Module{Package: "main", Decls: []Decl{pair, main}}
	out, _ := Monomorphize(in)
	mainCp := out.Decls[0].(*FnDecl)
	litCp := mainCp.Body.Stmts[0].(*ExprStmt).X.(*StructLit)
	if !strings.HasPrefix(litCp.TypeName, "_ZTS") {
		t.Fatalf("StructLit.TypeName should be mangled, got %q", litCp.TypeName)
	}
	nt, ok := litCp.T.(*NamedType)
	if !ok || nt.Name != litCp.TypeName {
		t.Fatalf("StructLit.T name must equal TypeName: T=%+v TypeName=%q", litCp.T, litCp.TypeName)
	}
	if len(nt.Args) != 0 {
		t.Fatalf("mangled NamedType should drop Args, got %+v", nt.Args)
	}
}

func TestMonomorphizeLetStmtTypeAnnotationRewritten(t *testing.T) {
	pair := genericPairStruct()
	pairT := &NamedType{Name: "Pair", Args: []Type{TInt, TInt}}
	main := &FnDecl{
		Name: "main", Return: TUnit,
		Body: &Block{Stmts: []Stmt{&LetStmt{
			Name: "p",
			Type: pairT,
			Value: &StructLit{
				TypeName: "Pair",
				T:        pairT,
				Fields: []StructLitField{
					{Name: "first", Value: intLit("1")},
					{Name: "second", Value: intLit("2")},
				},
			},
		}}},
	}
	in := &Module{Package: "main", Decls: []Decl{pair, main}}
	out, _ := Monomorphize(in)
	mainCp := out.Decls[0].(*FnDecl)
	let := mainCp.Body.Stmts[0].(*LetStmt)
	nt, ok := let.Type.(*NamedType)
	if !ok || !strings.HasPrefix(nt.Name, "_ZTS") {
		t.Fatalf("LetStmt.Type must be mangled NamedType, got %+v", let.Type)
	}
}

func TestMonomorphizeStructDedupesSameTypeArgs(t *testing.T) {
	pair := genericPairStruct()
	makeLit := func() *StructLit {
		pairT := &NamedType{Name: "Pair", Args: []Type{TInt, TInt}}
		return &StructLit{
			TypeName: "Pair", T: pairT,
			Fields: []StructLitField{
				{Name: "first", Value: intLit("1")},
				{Name: "second", Value: intLit("2")},
			},
		}
	}
	main := &FnDecl{
		Name: "main", Return: TUnit,
		Body: &Block{Stmts: []Stmt{
			&ExprStmt{X: makeLit()},
			&ExprStmt{X: makeLit()},
		}},
	}
	in := &Module{Package: "main", Decls: []Decl{pair, main}}
	out, _ := Monomorphize(in)
	if got := countSpecs(out); got != 1 {
		t.Fatalf("dedupe: expected 1 specialization for duplicate lits, got %d", got)
	}
}

func TestMonomorphizeStructDistinctTypeArgs(t *testing.T) {
	pair := genericPairStruct()
	mkLit := func(a, b Type) *StructLit {
		return &StructLit{
			TypeName: "Pair",
			T:        &NamedType{Name: "Pair", Args: []Type{a, b}},
			Fields: []StructLitField{
				{Name: "first", Value: intLit("1")},
				{Name: "second", Value: intLit("2")},
			},
		}
	}
	main := &FnDecl{
		Name: "main", Return: TUnit,
		Body: &Block{Stmts: []Stmt{
			&ExprStmt{X: mkLit(TInt, TBool)},
			&ExprStmt{X: mkLit(TBool, TInt)},
		}},
	}
	in := &Module{Package: "main", Decls: []Decl{pair, main}}
	out, _ := Monomorphize(in)
	if got := countSpecs(out); got != 2 {
		t.Fatalf("distinct args: expected 2 specializations, got %d", got)
	}
}

func TestMonomorphizeNestedGenericStructType(t *testing.T) {
	// Pair<Maybe<Bool>, Int> — the inner Maybe must be specialized before
	// the outer Pair's request so Pair's type-arg codes can embed the
	// inner mangled name.
	pair := genericPairStruct()
	maybe := genericMaybeEnum()
	maybeBool := &NamedType{Name: "Maybe", Args: []Type{TBool}}
	pairT := &NamedType{Name: "Pair", Args: []Type{maybeBool, TInt}}
	main := &FnDecl{
		Name: "main", Return: TUnit,
		Body: &Block{Stmts: []Stmt{&LetStmt{
			Name: "p",
			Type: pairT,
			Value: &StructLit{
				TypeName: "Pair",
				T:        pairT,
				Fields: []StructLitField{
					{Name: "first", Value: intLit("0")},
					{Name: "second", Value: intLit("0")},
				},
			},
		}}},
	}
	in := &Module{Package: "main", Decls: []Decl{pair, maybe, main}}
	out, errs := Monomorphize(in)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if got := countSpecs(out); got != 2 {
		t.Fatalf("expected 2 specs (inner Maybe + outer Pair), got %d\n%+v", got, out.Decls)
	}
}

func TestMonomorphizeUnusedGenericStructDropped(t *testing.T) {
	pair := genericPairStruct()
	in := &Module{Package: "main", Decls: []Decl{pair}}
	out, errs := Monomorphize(in)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(out.Decls) != 0 {
		t.Fatalf("expected empty output for unused generic struct, got %+v", out.Decls)
	}
}

func TestMonomorphizeFnWithGenericStructReturn(t *testing.T) {
	// fn pack<T>(x: T) -> Pair<T, T> { Pair { first: x, second: x } }
	// fn main() { pack::<Int>(1) }
	// Expect: pack specialization AND Pair<Int,Int> specialization.
	pair := genericPairStruct()
	tv := &TypeVar{Name: "T"}
	pairTT := &NamedType{Name: "Pair", Args: []Type{tv, tv}}
	pack := &FnDecl{
		Name:     "pack",
		Generics: []*TypeParam{{Name: "T"}},
		Params:   []*Param{{Name: "x", Type: tv}},
		Return:   pairTT,
		Body: &Block{Result: &StructLit{
			TypeName: "Pair",
			T:        pairTT,
			Fields: []StructLitField{
				{Name: "first", Value: &Ident{Name: "x", Kind: IdentParam, T: tv}},
				{Name: "second", Value: &Ident{Name: "x", Kind: IdentParam, T: tv}},
			},
		}},
	}
	mainCall := &CallExpr{
		Callee:   &Ident{Name: "pack", Kind: IdentFn, T: ErrTypeVal},
		TypeArgs: []Type{TInt},
		Args:     []Arg{{Value: intLit("1")}},
		T:        &NamedType{Name: "Pair", Args: []Type{TInt, TInt}},
	}
	main := &FnDecl{
		Name: "main", Return: TUnit,
		Body: &Block{Stmts: []Stmt{&ExprStmt{X: mainCall}}},
	}
	in := &Module{Package: "main", Decls: []Decl{pair, pack, main}}
	out, errs := Monomorphize(in)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	// Collect fn and struct specs.
	var fnSpecs, structSpecs int
	for _, d := range out.Decls {
		switch x := d.(type) {
		case *FnDecl:
			if strings.HasPrefix(x.Name, "_Z") && !strings.HasPrefix(x.Name, "_ZTS") {
				fnSpecs++
			}
		case *StructDecl:
			if strings.HasPrefix(x.Name, "_ZTS") {
				structSpecs++
			}
		}
	}
	if fnSpecs != 1 || structSpecs != 1 {
		t.Fatalf("expected 1 fn spec + 1 struct spec; got fn=%d struct=%d\n%+v",
			fnSpecs, structSpecs, out.Decls)
	}
	// The fn specialization's return type must be the mangled Pair.
	for _, d := range out.Decls {
		if fn, ok := d.(*FnDecl); ok && strings.HasPrefix(fn.Name, "_Z") && !strings.HasPrefix(fn.Name, "_ZTS") {
			nt, _ := fn.Return.(*NamedType)
			if nt == nil || !strings.HasPrefix(nt.Name, "_ZTS") {
				t.Fatalf("pack specialization return type not rewritten: %+v", fn.Return)
			}
		}
	}
}

func TestMonomorphizeVariantLitEnumFieldMangled(t *testing.T) {
	maybe := genericMaybeEnum()
	lit := &VariantLit{
		Enum:    "Maybe",
		Variant: "Some",
		T:       &NamedType{Name: "Maybe", Args: []Type{TInt}},
		Args:    []Arg{{Value: intLit("42")}},
	}
	main := &FnDecl{
		Name: "main", Return: TUnit,
		Body: &Block{Stmts: []Stmt{&ExprStmt{X: lit}}},
	}
	in := &Module{Package: "main", Decls: []Decl{maybe, main}}
	out, _ := Monomorphize(in)
	mainCp := out.Decls[0].(*FnDecl)
	litCp := mainCp.Body.Stmts[0].(*ExprStmt).X.(*VariantLit)
	if !strings.HasPrefix(litCp.Enum, "_ZTS") {
		t.Fatalf("VariantLit.Enum should be mangled, got %q", litCp.Enum)
	}
}

func TestMonomorphizeVariantLitUsesLetContextType(t *testing.T) {
	maybe := genericMaybeEnum()
	valueType := &NamedType{Name: "Maybe", Args: []Type{TInt}}
	lit := &VariantLit{
		Enum:    "Maybe",
		Variant: "Some",
		T:       ErrTypeVal,
		Args:    []Arg{{Value: intLit("42")}},
	}
	main := &FnDecl{
		Name: "main", Return: TUnit,
		Body: &Block{Stmts: []Stmt{&LetStmt{Name: "m", Type: valueType, Value: lit}}},
	}
	in := &Module{Package: "main", Decls: []Decl{maybe, main}}
	out, _ := Monomorphize(in)
	mainCp := out.Decls[0].(*FnDecl)
	letCp := mainCp.Body.Stmts[0].(*LetStmt)
	litCp := letCp.Value.(*VariantLit)
	if !strings.HasPrefix(litCp.Enum, "_ZTS") {
		t.Fatalf("VariantLit.Enum should use let-context mangled type, got %q", litCp.Enum)
	}
	nt, ok := litCp.T.(*NamedType)
	if !ok || nt.Name != litCp.Enum {
		t.Fatalf("VariantLit.T should match mangled enum, got %#v", litCp.T)
	}
}

func TestMonomorphizeVariantPatternUsesScrutineeType(t *testing.T) {
	maybe := genericMaybeEnum()
	valueType := &NamedType{Name: "Maybe", Args: []Type{TInt}}
	main := &FnDecl{
		Name: "main", Return: TUnit,
		Body: &Block{Stmts: []Stmt{
			&ExprStmt{X: &IfLetExpr{
				Pattern: &VariantPat{
					Enum:    "Maybe",
					Variant: "Some",
					Args:    []Pattern{&IdentPat{Name: "x"}},
				},
				Scrutinee: &Ident{Name: "m", Kind: IdentLocal, T: valueType},
				Then:      &Block{},
				Else:      &Block{},
				T:         TUnit,
			}},
		}},
	}
	in := &Module{Package: "main", Decls: []Decl{maybe, main}}
	out, _ := Monomorphize(in)
	mainCp := out.Decls[0].(*FnDecl)
	ifLet := mainCp.Body.Stmts[0].(*ExprStmt).X.(*IfLetExpr)
	pat := ifLet.Pattern.(*VariantPat)
	if !strings.HasPrefix(pat.Enum, "_ZTS") {
		t.Fatalf("VariantPat.Enum should use scrutinee mangled type, got %q", pat.Enum)
	}
}

func TestMonomorphizeVariantLitInfersTypeFromPayload(t *testing.T) {
	maybe := genericMaybeEnum()
	main := &FnDecl{
		Name: "main", Return: TUnit,
		Body: &Block{Stmts: []Stmt{
			&LetStmt{
				Name: "m",
				Type: ErrTypeVal,
				Value: &VariantLit{
					Enum:    "Maybe",
					Variant: "Some",
					T:       ErrTypeVal,
					Args:    []Arg{{Value: intLit("42")}},
				},
			},
			&ExprStmt{X: &IfLetExpr{
				Pattern: &VariantPat{
					Enum:    "Maybe",
					Variant: "Some",
					Args:    []Pattern{&IdentPat{Name: "x"}},
				},
				Scrutinee: &Ident{Name: "m", Kind: IdentLocal, T: ErrTypeVal},
				Then:      &Block{},
				Else:      &Block{},
				T:         TUnit,
			}},
		}},
	}
	in := &Module{Package: "main", Decls: []Decl{maybe, main}}
	out, errs := Monomorphize(in)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	mainCp := out.Decls[0].(*FnDecl)
	letCp := mainCp.Body.Stmts[0].(*LetStmt)
	nt, ok := letCp.Type.(*NamedType)
	if !ok || !strings.HasPrefix(nt.Name, "_ZTS") {
		t.Fatalf("let type should infer to mangled enum, got %#v", letCp.Type)
	}
	litCp := letCp.Value.(*VariantLit)
	if litCp.Enum != nt.Name {
		t.Fatalf("VariantLit.Enum = %q, want %q", litCp.Enum, nt.Name)
	}
	ifLet := mainCp.Body.Stmts[1].(*ExprStmt).X.(*IfLetExpr)
	scrutinee := ifLet.Scrutinee.(*Ident)
	scrutineeType, ok := scrutinee.T.(*NamedType)
	if !ok || scrutineeType.Name != nt.Name {
		t.Fatalf("scrutinee type should reuse inferred enum, got %#v", scrutinee.T)
	}
	pat := ifLet.Pattern.(*VariantPat)
	if pat.Enum != nt.Name {
		t.Fatalf("pattern enum = %q, want %q", pat.Enum, nt.Name)
	}
}

func TestMonomorphizePayloadFreeVariantUsesLetContextType(t *testing.T) {
	maybe := genericMaybeEnum()
	valueType := &NamedType{Name: "Maybe", Args: []Type{TInt}}
	main := &FnDecl{
		Name: "main", Return: TUnit,
		Body: &Block{Stmts: []Stmt{
			&LetStmt{
				Name: "m",
				Type: valueType,
				Value: &FieldExpr{
					X:    &Ident{Name: "Maybe", Kind: IdentTypeName, T: valueType},
					Name: "None",
					T:    ErrTypeVal,
				},
			},
			&ExprStmt{X: &IfLetExpr{
				Pattern:   &VariantPat{Enum: "Maybe", Variant: "None"},
				Scrutinee: &Ident{Name: "m", Kind: IdentLocal, T: ErrTypeVal},
				Then:      &Block{},
				Else:      &Block{},
				T:         TUnit,
			}},
		}},
	}
	in := &Module{Package: "main", Decls: []Decl{maybe, main}}
	out, errs := Monomorphize(in)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	mainCp := out.Decls[0].(*FnDecl)
	letCp := mainCp.Body.Stmts[0].(*LetStmt)
	nt, ok := letCp.Type.(*NamedType)
	if !ok || !strings.HasPrefix(nt.Name, "_ZTS") {
		t.Fatalf("let type should rewrite to mangled enum, got %#v", letCp.Type)
	}
	fieldCp := letCp.Value.(*FieldExpr)
	if fieldCp.T == nil || fieldCp.T.String() != nt.String() {
		t.Fatalf("field expr type = %#v, want %q", fieldCp.T, nt.String())
	}
	base := fieldCp.X.(*Ident)
	if base.Name != nt.Name {
		t.Fatalf("field base name = %q, want %q", base.Name, nt.Name)
	}
	ifLet := mainCp.Body.Stmts[1].(*ExprStmt).X.(*IfLetExpr)
	pat := ifLet.Pattern.(*VariantPat)
	if pat.Enum != nt.Name {
		t.Fatalf("pattern enum = %q, want %q", pat.Enum, nt.Name)
	}
}

func TestMonomorphizeStructMethodSpecialization(t *testing.T) {
	// struct Pair<T, U> { … } impl { fn first(self) -> T { self.first } }
	// fn main() { let p = Pair<Int, Bool>; p.first() }
	// → specialization's method has concrete receiver + return type.
	pair := genericPairStruct()
	tv := &TypeVar{Name: "T"}
	pair.Methods = []*FnDecl{{
		Name:   "first",
		Return: tv,
		Params: []*Param{{Name: "self", Type: &NamedType{Name: "Pair", Args: []Type{tv, &TypeVar{Name: "U"}}}}},
		Body:   &Block{Result: &FieldExpr{X: &Ident{Name: "self", Kind: IdentParam, T: tv}, Name: "first", T: tv}},
	}}
	pairT := &NamedType{Name: "Pair", Args: []Type{TInt, TBool}}
	main := &FnDecl{
		Name: "main", Return: TUnit,
		Body: &Block{Stmts: []Stmt{&LetStmt{
			Name: "p",
			Type: pairT,
			Value: &StructLit{
				TypeName: "Pair",
				T:        pairT,
				Fields: []StructLitField{
					{Name: "first", Value: intLit("1")},
					{Name: "second", Value: &BoolLit{Value: true}},
				},
			},
		}}},
	}
	in := &Module{Package: "main", Decls: []Decl{pair, main}}
	out, errs := Monomorphize(in)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	spec := findStructSpec(out)
	if spec == nil || len(spec.Methods) != 1 {
		t.Fatalf("struct spec missing method: %+v", spec)
	}
	m := spec.Methods[0]
	if m.Return != TInt {
		t.Fatalf("method return type not substituted to Int: %T", m.Return)
	}
	// The receiver param's type must have been rewritten to the mangled
	// Pair specialization so llvmgen's method-owner resolution succeeds.
	recvT, ok := m.Params[0].Type.(*NamedType)
	if !ok || !strings.HasPrefix(recvT.Name, "_ZTS") {
		t.Fatalf("method receiver type not rewritten to mangled: %+v", m.Params[0].Type)
	}
}

func TestMonomorphizeStructMethodCallReceiverTypeRewritten(t *testing.T) {
	// p.first() call — receiver expression type must be the mangled
	// specialization so downstream method dispatch resolves correctly.
	pair := genericPairStruct()
	tv := &TypeVar{Name: "T"}
	pair.Methods = []*FnDecl{{
		Name:   "first",
		Return: tv,
		Params: []*Param{{Name: "self", Type: &NamedType{Name: "Pair", Args: []Type{tv, &TypeVar{Name: "U"}}}}},
		Body:   &Block{Result: &FieldExpr{X: &Ident{Name: "self", Kind: IdentParam, T: tv}, Name: "first", T: tv}},
	}}
	pairT := &NamedType{Name: "Pair", Args: []Type{TInt, TBool}}
	recv := &Ident{Name: "p", Kind: IdentLocal, T: pairT}
	call := &MethodCall{
		Receiver: recv,
		Name:     "first",
		T:        TInt,
	}
	main := &FnDecl{
		Name: "main", Return: TUnit,
		Body: &Block{Stmts: []Stmt{
			&LetStmt{Name: "p", Type: pairT, Value: &StructLit{
				TypeName: "Pair", T: pairT,
				Fields: []StructLitField{
					{Name: "first", Value: intLit("1")},
					{Name: "second", Value: &BoolLit{Value: true}},
				},
			}},
			&ExprStmt{X: call},
		}},
	}
	in := &Module{Package: "main", Decls: []Decl{pair, main}}
	out, _ := Monomorphize(in)
	mainCp := out.Decls[0].(*FnDecl)
	callCp := mainCp.Body.Stmts[1].(*ExprStmt).X.(*MethodCall)
	recvT, ok := callCp.Receiver.(*Ident).T.(*NamedType)
	if !ok || !strings.HasPrefix(recvT.Name, "_ZTS") {
		t.Fatalf("MethodCall receiver type not rewritten, got %+v", callCp.Receiver.(*Ident).T)
	}
}

func TestMonomorphizeEnumMethodBodyScanned(t *testing.T) {
	// enum Maybe<T> { Some(T), None } impl { fn wrap(self) -> T { id::<T>(0) } }
	// Use a generic free fn `id<U>(x: U) -> U` inside an enum method so
	// the method body's call site still drives the fn worklist after
	// specialization. The concrete T = Int ⇒ id::<Int>(0) ⇒ one fn spec.
	idFn := genericIdentity()
	maybe := genericMaybeEnum()
	tv := &TypeVar{Name: "T"}
	maybe.Methods = []*FnDecl{{
		Name:   "wrap",
		Return: tv,
		Params: []*Param{{Name: "self", Type: &NamedType{Name: "Maybe", Args: []Type{tv}}}},
		Body: &Block{Result: &CallExpr{
			Callee:   &Ident{Name: "id", Kind: IdentFn, T: ErrTypeVal},
			TypeArgs: []Type{tv},
			Args:     []Arg{{Value: intLit("0")}},
			T:        tv,
		}},
	}}
	maybeT := &NamedType{Name: "Maybe", Args: []Type{TInt}}
	main := &FnDecl{
		Name: "main", Return: TUnit,
		Body: &Block{Stmts: []Stmt{&LetStmt{
			Name: "m", Type: maybeT,
			Value: &VariantLit{Enum: "Maybe", Variant: "Some", T: maybeT,
				Args: []Arg{{Value: intLit("1")}}},
		}}},
	}
	in := &Module{Package: "main", Decls: []Decl{idFn, maybe, main}}
	out, errs := Monomorphize(in)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	// Expect at least one fn spec (id::<Int>) + one enum spec (Maybe<Int>).
	fnSpecs := 0
	enumSpecs := 0
	for _, d := range out.Decls {
		switch x := d.(type) {
		case *FnDecl:
			if strings.HasPrefix(x.Name, "_Z") && !strings.HasPrefix(x.Name, "_ZTS") {
				fnSpecs++
			}
		case *EnumDecl:
			if strings.HasPrefix(x.Name, "_ZTS") {
				enumSpecs++
			}
		}
	}
	if fnSpecs < 1 {
		t.Fatalf("expected id specialization from enum method body, got %d fn specs\n%+v",
			fnSpecs, out.Decls)
	}
	if enumSpecs != 1 {
		t.Fatalf("expected 1 Maybe<Int> spec, got %d\n%+v", enumSpecs, out.Decls)
	}
}

// ==== Phase 4 method-local generic specialization ====

// genericBoxNonGeneric returns a `struct Box { value: Int; fn get<U>(self, u: U) -> U { u } }`
// — a non-generic owner carrying a method with its own generic param.
// Exposed as a helper so tests stay focused on behavior.
func genericBoxNonGenericOwner() *StructDecl {
	return &StructDecl{
		Name:   "Box",
		Fields: []*Field{{Name: "value", Type: TInt}},
		Methods: []*FnDecl{{
			Name:     "get",
			Generics: []*TypeParam{{Name: "U"}},
			Params: []*Param{
				{Name: "self", Type: &NamedType{Name: "Box"}},
				{Name: "u", Type: &TypeVar{Name: "U"}},
			},
			Return: &TypeVar{Name: "U"},
			Body:   &Block{Result: &Ident{Name: "u", Kind: IdentParam, T: &TypeVar{Name: "U"}}},
		}},
	}
}

// genericVecGenericOwner returns a `struct Vec<T>` with both a plain
// method and a `map<U>` method exercising receiver+method generics.
func genericVecGenericOwner() *StructDecl {
	tvT := &TypeVar{Name: "T"}
	tvU := &TypeVar{Name: "U"}
	return &StructDecl{
		Name:     "Vec",
		Generics: []*TypeParam{{Name: "T"}},
		Fields:   []*Field{{Name: "head", Type: tvT}},
		Methods: []*FnDecl{{
			Name:     "map",
			Generics: []*TypeParam{{Name: "U"}},
			Params: []*Param{
				{Name: "self", Type: &NamedType{Name: "Vec", Args: []Type{tvT}}},
				{Name: "seed", Type: tvU},
			},
			Return: tvU,
			Body:   &Block{Result: &Ident{Name: "seed", Kind: IdentParam, T: tvU}},
		}},
	}
}

func TestMonomorphizeMethodLocalGenericOnNonGenericOwner(t *testing.T) {
	// struct Box { value: Int; fn get<U>(self, u: U) -> U { u } }
	// fn main() { let b = Box { value: 1 }; b.get::<Int>(7) }
	// Expect: Box.Methods gets a specialization "get_ZIlE" with concrete
	// param + return types, original generic `get` is dropped.
	box := genericBoxNonGenericOwner()
	call := &MethodCall{
		Receiver: &Ident{Name: "b", Kind: IdentLocal, T: &NamedType{Name: "Box"}},
		Name:     "get",
		TypeArgs: []Type{TInt},
		Args:     []Arg{{Value: intLit("7")}},
		T:        TInt,
	}
	main := &FnDecl{
		Name: "main", Return: TUnit,
		Body: &Block{Stmts: []Stmt{
			&LetStmt{Name: "b", Type: &NamedType{Name: "Box"},
				Value: &StructLit{TypeName: "Box", T: &NamedType{Name: "Box"},
					Fields: []StructLitField{{Name: "value", Value: intLit("1")}}}},
			&ExprStmt{X: call},
		}},
	}
	in := &Module{Package: "main", Decls: []Decl{box, main}}
	out, errs := Monomorphize(in)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	// Find the Box decl in the output.
	var boxCp *StructDecl
	for _, d := range out.Decls {
		if sd, ok := d.(*StructDecl); ok && sd.Name == "Box" {
			boxCp = sd
		}
	}
	if boxCp == nil {
		t.Fatalf("Box missing from output decls")
	}
	if len(boxCp.Methods) != 1 {
		t.Fatalf("expected exactly 1 method (specialization), got %d: %+v", len(boxCp.Methods), boxCp.Methods)
	}
	spec := boxCp.Methods[0]
	if len(spec.Generics) != 0 {
		t.Fatalf("method specialization must shed method-local generics, got %d", len(spec.Generics))
	}
	if !strings.Contains(spec.Name, "_Z") {
		t.Fatalf("method specialization name should contain '_Z' marker, got %q", spec.Name)
	}
	if spec.Return != TInt {
		t.Fatalf("method specialization return not substituted to Int: %T", spec.Return)
	}
	if spec.Params[1].Type != TInt {
		t.Fatalf("method specialization param 'u' not substituted: %T", spec.Params[1].Type)
	}
	// Call site is rewritten to the mangled method name with TypeArgs cleared.
	mainCp := out.Decls[1].(*FnDecl)
	callCp := mainCp.Body.Stmts[1].(*ExprStmt).X.(*MethodCall)
	if callCp.Name != spec.Name {
		t.Fatalf("call site name not rewritten to %q, got %q", spec.Name, callCp.Name)
	}
	if len(callCp.TypeArgs) != 0 {
		t.Fatalf("call site TypeArgs should be cleared, got %+v", callCp.TypeArgs)
	}
}

func TestMonomorphizeMethodLocalGenericDedup(t *testing.T) {
	box := genericBoxNonGenericOwner()
	mk := func() *MethodCall {
		return &MethodCall{
			Receiver: &Ident{Name: "b", Kind: IdentLocal, T: &NamedType{Name: "Box"}},
			Name:     "get",
			TypeArgs: []Type{TInt},
			Args:     []Arg{{Value: intLit("7")}},
			T:        TInt,
		}
	}
	main := &FnDecl{
		Name: "main", Return: TUnit,
		Body: &Block{Stmts: []Stmt{
			&ExprStmt{X: mk()},
			&ExprStmt{X: mk()},
		}},
	}
	in := &Module{Package: "main", Decls: []Decl{box, main}}
	out, _ := Monomorphize(in)
	var boxCp *StructDecl
	for _, d := range out.Decls {
		if sd, ok := d.(*StructDecl); ok && sd.Name == "Box" {
			boxCp = sd
		}
	}
	if boxCp == nil || len(boxCp.Methods) != 1 {
		t.Fatalf("expected 1 deduped method, got %+v", boxCp)
	}
}

func TestMonomorphizeMethodLocalGenericDistinctArgs(t *testing.T) {
	box := genericBoxNonGenericOwner()
	main := &FnDecl{
		Name: "main", Return: TUnit,
		Body: &Block{Stmts: []Stmt{
			&ExprStmt{X: &MethodCall{
				Receiver: &Ident{Name: "b", Kind: IdentLocal, T: &NamedType{Name: "Box"}},
				Name:     "get",
				TypeArgs: []Type{TInt},
				Args:     []Arg{{Value: intLit("1")}},
				T:        TInt,
			}},
			&ExprStmt{X: &MethodCall{
				Receiver: &Ident{Name: "b", Kind: IdentLocal, T: &NamedType{Name: "Box"}},
				Name:     "get",
				TypeArgs: []Type{TBool},
				Args:     []Arg{{Value: &BoolLit{Value: true}}},
				T:        TBool,
			}},
		}},
	}
	in := &Module{Package: "main", Decls: []Decl{box, main}}
	out, _ := Monomorphize(in)
	var boxCp *StructDecl
	for _, d := range out.Decls {
		if sd, ok := d.(*StructDecl); ok && sd.Name == "Box" {
			boxCp = sd
		}
	}
	if boxCp == nil || len(boxCp.Methods) != 2 {
		t.Fatalf("expected 2 distinct method specializations, got %+v", boxCp.Methods)
	}
	if boxCp.Methods[0].Name == boxCp.Methods[1].Name {
		t.Fatalf("distinct type args should produce distinct mangled names")
	}
}

func TestMonomorphizeMethodLocalGenericOnGenericOwner(t *testing.T) {
	// struct Vec<T> { head: T; fn map<U>(self, seed: U) -> U }
	// fn main() { let v: Vec<Int> = …; v.map::<String>("x") }
	// Expect: Vec<Int> specialization exists + one method specialization
	// of map with concrete Int receiver context and String param/return.
	vec := genericVecGenericOwner()
	vecInt := &NamedType{Name: "Vec", Args: []Type{TInt}}
	recv := &Ident{Name: "v", Kind: IdentLocal, T: vecInt}
	call := &MethodCall{
		Receiver: recv,
		Name:     "map",
		TypeArgs: []Type{TString},
		Args:     []Arg{{Value: strLit("x")}},
		T:        TString,
	}
	main := &FnDecl{
		Name: "main", Return: TUnit,
		Body: &Block{Stmts: []Stmt{
			&LetStmt{Name: "v", Type: vecInt,
				Value: &StructLit{TypeName: "Vec", T: vecInt,
					Fields: []StructLitField{{Name: "head", Value: intLit("0")}}}},
			&ExprStmt{X: call},
		}},
	}
	in := &Module{Package: "main", Decls: []Decl{vec, main}}
	out, errs := Monomorphize(in)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	spec := findStructSpec(out)
	if spec == nil {
		t.Fatalf("Vec specialization missing; decls=%+v", out.Decls)
	}
	// spec.Methods should have one method specialization (the generic
	// original is dropped in Pass 6).
	if len(spec.Methods) != 1 {
		t.Fatalf("expected 1 method spec on Vec<Int>, got %d: %+v", len(spec.Methods), spec.Methods)
	}
	m := spec.Methods[0]
	if len(m.Generics) != 0 {
		t.Fatalf("method spec must not carry method-local generics")
	}
	if m.Return != TString {
		t.Fatalf("method spec return type not substituted to String: %T", m.Return)
	}
}

func TestMonomorphizeGenericOwnerNonGenericMethodClearsReceiverTypeArgs(t *testing.T) {
	// Checker instantiation metadata can attach the owner's concrete
	// args to a nongeneric method call on a generic owner. Monomorph
	// should clear those stale TypeArgs once the receiver type has been
	// rewritten to the concrete owner specialization.
	tvT := &TypeVar{Name: "T"}
	vec := &StructDecl{
		Name:     "Vec",
		Generics: []*TypeParam{{Name: "T"}},
		Fields:   []*Field{{Name: "head", Type: tvT}},
		Methods: []*FnDecl{{
			Name: "headVal",
			Params: []*Param{
				{Name: "self", Type: &NamedType{Name: "Vec", Args: []Type{tvT}}},
			},
			Return: tvT,
			Body:   &Block{Result: &FieldExpr{X: &Ident{Name: "self", Kind: IdentParam, T: &NamedType{Name: "Vec", Args: []Type{tvT}}}, Name: "head", T: tvT}},
		}},
	}
	vecInt := &NamedType{Name: "Vec", Args: []Type{TInt}}
	call := &MethodCall{
		Receiver: &Ident{Name: "v", Kind: IdentLocal, T: vecInt},
		Name:     "headVal",
		TypeArgs: []Type{TInt},
		T:        TInt,
	}
	main := &FnDecl{
		Name: "main", Return: TUnit,
		Body: &Block{Stmts: []Stmt{
			&LetStmt{Name: "v", Type: vecInt,
				Value: &StructLit{TypeName: "Vec", T: vecInt,
					Fields: []StructLitField{{Name: "head", Value: intLit("1")}}}},
			&ExprStmt{X: call},
		}},
	}
	out, errs := Monomorphize(&Module{Package: "main", Decls: []Decl{vec, main}})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	var mainCp *FnDecl
	for _, d := range out.Decls {
		if fn, ok := d.(*FnDecl); ok && fn.Name == "main" {
			mainCp = fn
			break
		}
	}
	if mainCp == nil {
		t.Fatalf("main missing from output decls: %+v", out.Decls)
	}
	callCp := mainCp.Body.Stmts[1].(*ExprStmt).X.(*MethodCall)
	if len(callCp.TypeArgs) != 0 {
		t.Fatalf("nongeneric method call should shed receiver TypeArgs after monomorph, got %+v", callCp.TypeArgs)
	}
}

func TestMonomorphizeBuiltinListMethodFallbackClearsReceiverTypeArgs(t *testing.T) {
	// Some stdlib-bodied specializations (notably Map helpers) call
	// builtin generic owners like List.push without that owner template
	// being present in the module. Monomorph should still clear the
	// checker-carried receiver args on known nongeneric builtin methods
	// instead of leaving stale turbofish metadata behind.
	listInt := &NamedType{Name: "List", Args: []Type{TInt}}
	call := &MethodCall{
		Receiver: &Ident{Name: "xs", Kind: IdentLocal, T: listInt},
		Name:     "push",
		TypeArgs: []Type{TInt},
		Args:     []Arg{{Value: intLit("1")}},
		T:        TUnit,
	}
	main := &FnDecl{
		Name: "main", Return: TUnit,
		Body: &Block{Stmts: []Stmt{
			&LetStmt{Name: "xs", Type: listInt},
			&ExprStmt{X: call},
		}},
	}
	out, errs := Monomorphize(&Module{Package: "main", Decls: []Decl{main}})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	mainCp := out.Decls[0].(*FnDecl)
	callCp := mainCp.Body.Stmts[1].(*ExprStmt).X.(*MethodCall)
	if len(callCp.TypeArgs) != 0 {
		t.Fatalf("builtin List.push fallback should clear stale receiver TypeArgs, got %+v", callCp.TypeArgs)
	}
}

func TestMonomorphizeBuiltinOptionalMethodFallbackClearsReceiverTypeArgs(t *testing.T) {
	optInt := &OptionalType{Inner: TInt}
	call := &MethodCall{
		Receiver: &Ident{Name: "x", Kind: IdentLocal, T: optInt},
		Name:     "isSome",
		TypeArgs: []Type{TInt},
		T:        TBool,
	}
	main := &FnDecl{
		Name: "main", Return: TUnit,
		Body: &Block{Stmts: []Stmt{
			&LetStmt{Name: "x", Type: optInt},
			&ExprStmt{X: call},
		}},
	}
	out, errs := Monomorphize(&Module{Package: "main", Decls: []Decl{main}})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	mainCp := out.Decls[0].(*FnDecl)
	callCp := mainCp.Body.Stmts[1].(*ExprStmt).X.(*MethodCall)
	if len(callCp.TypeArgs) != 0 {
		t.Fatalf("builtin Option.isSome fallback should clear stale receiver TypeArgs, got %+v", callCp.TypeArgs)
	}
}

func TestMonomorphizeBuiltinMapMethodMissingTemplateClearsReceiverTypeArgs(t *testing.T) {
	// Some lowered stdlib paths can leave the builtin owner declaration
	// present without the original method template attached. Monomorph
	// should still clear checker-carried receiver args on known
	// nongeneric builtin methods instead of leaking stale turbofish
	// metadata.
	mapDecl := &StructDecl{
		Name:     "Map",
		Generics: []*TypeParam{{Name: "K"}, {Name: "V"}},
	}
	mapKV := &NamedType{Name: "Map", Args: []Type{TString, TInt}}
	call := &MethodCall{
		Receiver: &Ident{Name: "m", Kind: IdentLocal, T: mapKV},
		Name:     "get",
		TypeArgs: []Type{TString, TInt},
		Args:     []Arg{{Value: strLit("k")}},
		T:        &OptionalType{Inner: TInt},
	}
	main := &FnDecl{
		Name: "main", Return: TUnit,
		Body: &Block{Stmts: []Stmt{
			&LetStmt{Name: "m", Type: mapKV},
			&ExprStmt{X: call},
		}},
	}
	out, errs := Monomorphize(&Module{Package: "main", Decls: []Decl{mapDecl, main}})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	var mainCp *FnDecl
	for _, d := range out.Decls {
		if fn, ok := d.(*FnDecl); ok && fn.Name == "main" {
			mainCp = fn
			break
		}
	}
	if mainCp == nil {
		t.Fatalf("main missing from output decls: %+v", out.Decls)
	}
	callCp := mainCp.Body.Stmts[1].(*ExprStmt).X.(*MethodCall)
	if len(callCp.TypeArgs) != 0 {
		t.Fatalf("builtin Map.get missing-template fallback should clear stale receiver TypeArgs, got %+v", callCp.TypeArgs)
	}
}

func TestMonomorphizeBuiltinSetMethodFallbackSubstitutesReceiverReturnType(t *testing.T) {
	// Default-off stdlib body lowering still runs monomorphization. For
	// builtin nongeneric methods like Set<T>.toList(), the call result
	// type and any let binding that records it should inherit the
	// receiver's concrete T even when no builtin owner template was
	// injected into the module.
	tvT := &TypeVar{Name: "T"}
	setInt := &NamedType{Name: "Set", Args: []Type{TInt}}
	call := &MethodCall{
		Receiver: &Ident{Name: "seen", Kind: IdentLocal, T: setInt},
		Name:     "toList",
		T:        &NamedType{Name: "List", Args: []Type{tvT}},
	}
	listT := &NamedType{Name: "List", Args: []Type{&TypeVar{Name: "T"}}}
	main := &FnDecl{
		Name: "main", Return: TUnit,
		Body: &Block{Stmts: []Stmt{
			&LetStmt{Name: "seen", Type: setInt},
			&LetStmt{Name: "ids", Type: listT, Value: call},
		}},
	}
	out, errs := Monomorphize(&Module{Package: "main", Decls: []Decl{main}})
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	mainCp := out.Decls[0].(*FnDecl)
	ids := mainCp.Body.Stmts[1].(*LetStmt)
	listTyp, ok := ids.Type.(*NamedType)
	if !ok || listTyp == nil || listTyp.Name != "List" || len(listTyp.Args) != 1 || typeString(listTyp.Args[0]) != typeString(TInt) {
		t.Fatalf("let binding type should concrete-substitute Set<T>.toList() to List<Int>, got %+v", ids.Type)
	}
	callCp := ids.Value.(*MethodCall)
	callRet, ok := callCp.T.(*NamedType)
	if !ok || callRet == nil || callRet.Name != "List" || len(callRet.Args) != 1 || typeString(callRet.Args[0]) != typeString(TInt) {
		t.Fatalf("builtin Set.toList fallback should concrete-substitute method return type, got %+v", callCp.T)
	}
}

func TestMonomorphizeMethodLocalGenericArityMismatch(t *testing.T) {
	box := genericBoxNonGenericOwner()
	// get<U> — pass two type args
	call := &MethodCall{
		Receiver: &Ident{Name: "b", Kind: IdentLocal, T: &NamedType{Name: "Box"}},
		Name:     "get",
		TypeArgs: []Type{TInt, TBool},
		Args:     []Arg{{Value: intLit("1")}},
		T:        TInt,
	}
	main := &FnDecl{
		Name: "main", Return: TUnit,
		Body: &Block{Stmts: []Stmt{&ExprStmt{X: call}}},
	}
	in := &Module{Package: "main", Decls: []Decl{box, main}}
	_, errs := Monomorphize(in)
	if len(errs) == 0 {
		t.Fatalf("expected arity-mismatch error, got none")
	}
	joined := ""
	for _, e := range errs {
		joined += e.Error() + "\n"
	}
	if !strings.Contains(joined, "arity mismatch") {
		t.Fatalf("expected 'arity mismatch' in err, got %q", joined)
	}
}

func TestMonomorphizeMethodLocalGenericPreservedGenericsDropped(t *testing.T) {
	// After specialization, NO struct/enum in out.Decls may still carry
	// a method with len(Generics) > 0.
	box := genericBoxNonGenericOwner()
	call := &MethodCall{
		Receiver: &Ident{Name: "b", Kind: IdentLocal, T: &NamedType{Name: "Box"}},
		Name:     "get",
		TypeArgs: []Type{TInt},
		Args:     []Arg{{Value: intLit("1")}},
		T:        TInt,
	}
	main := &FnDecl{
		Name: "main", Return: TUnit,
		Body: &Block{Stmts: []Stmt{&ExprStmt{X: call}}},
	}
	out, _ := Monomorphize(&Module{Package: "main", Decls: []Decl{box, main}})
	for _, d := range out.Decls {
		switch x := d.(type) {
		case *StructDecl:
			for _, m := range x.Methods {
				if len(m.Generics) > 0 {
					t.Fatalf("Pass 6 missed a preserved generic method: %s.%s", x.Name, m.Name)
				}
			}
		case *EnumDecl:
			for _, m := range x.Methods {
				if len(m.Generics) > 0 {
					t.Fatalf("Pass 6 missed a preserved generic method: %s.%s", x.Name, m.Name)
				}
			}
		}
	}
}

func TestMonomorphizeMethodLocalGenericUncalledPreservedMethodsStillDropped(t *testing.T) {
	// A generic method that is never called should still be absent
	// from the final output (Pass 6 strip), and no specialization is
	// generated.
	box := genericBoxNonGenericOwner()
	in := &Module{Package: "main", Decls: []Decl{box}}
	out, errs := Monomorphize(in)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	for _, d := range out.Decls {
		if sd, ok := d.(*StructDecl); ok && sd.Name == "Box" {
			if len(sd.Methods) != 0 {
				t.Fatalf("Box should have zero methods when nothing called, got %+v", sd.Methods)
			}
		}
	}
}

func TestMonomorphMangleMethodHelpers(t *testing.T) {
	got := MonomorphMangleMethodName("map", []string{"Ss"})
	const want = "map_ZISsE"
	if got != want {
		t.Fatalf("MonomorphMangleMethodName: got %q, want %q", got, want)
	}
	if MonomorphMangleMethodName("bare", nil) != "bare" {
		t.Fatalf("empty typeArgCodes should pass through method name unchanged")
	}
	fnKey := MonomorphDedupeKey("get", "main", []string{"l"})
	methodKey := MonomorphMethodDedupeKey("main", "get", []string{"l"})
	if fnKey == methodKey {
		t.Fatalf("method dedupe key must not collide with fn dedupe key")
	}
}

// ==== Phase 5 generic interface specialization ====

// genericIteratorInterface returns `interface Iterator<T> { fn next(self) -> T }`.
func genericIteratorInterface() *InterfaceDecl {
	tv := &TypeVar{Name: "T"}
	return &InterfaceDecl{
		Name:     "Iterator",
		Generics: []*TypeParam{{Name: "T"}},
		Methods: []*FnDecl{{
			Name:   "next",
			Params: []*Param{{Name: "self", Type: &NamedType{Name: "Iterator", Args: []Type{tv}}}},
			Return: tv,
		}},
	}
}

// findInterfaceSpec returns the first mangled InterfaceDecl in a module.
func findInterfaceSpec(m *Module) *InterfaceDecl {
	for _, d := range m.Decls {
		if id, ok := d.(*InterfaceDecl); ok && strings.HasPrefix(id.Name, "_ZTS") {
			return id
		}
	}
	return nil
}

func TestMonomorphizeGenericInterfaceSpecialization(t *testing.T) {
	// interface Iterator<T> { fn next(self) -> T }
	// fn use(it: Iterator<Int>) { ... }
	// The parameter type triggers specialization of Iterator<Int>.
	iter := genericIteratorInterface()
	user := &FnDecl{
		Name:   "use",
		Params: []*Param{{Name: "it", Type: &NamedType{Name: "Iterator", Args: []Type{TInt}}}},
		Return: TUnit,
		Body:   &Block{},
	}
	in := &Module{Package: "main", Decls: []Decl{iter, user}}
	out, errs := Monomorphize(in)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	spec := findInterfaceSpec(out)
	if spec == nil {
		t.Fatalf("Iterator specialization missing from output: %+v", out.Decls)
	}
	if spec.Generics != nil {
		t.Fatalf("specialization must shed generics, got %d", len(spec.Generics))
	}
	// The cloned fn `use` must have its param type rewritten to the mangled interface.
	userCp := out.Decls[0].(*FnDecl)
	pt, ok := userCp.Params[0].Type.(*NamedType)
	if !ok || !strings.HasPrefix(pt.Name, "_ZTS") {
		t.Fatalf("use.Params[0].Type not rewritten to mangled interface: %+v", userCp.Params[0].Type)
	}
	if pt.Name != spec.Name {
		t.Fatalf("mangled names mismatch: param=%q spec=%q", pt.Name, spec.Name)
	}
	// Method signature must have concrete return.
	if len(spec.Methods) != 1 || spec.Methods[0].Return != TInt {
		t.Fatalf("interface method return not substituted to Int: %+v", spec.Methods)
	}
}

func TestMonomorphizeGenericInterfaceDedup(t *testing.T) {
	iter := genericIteratorInterface()
	makeUser := func(name string) *FnDecl {
		return &FnDecl{
			Name:   name,
			Params: []*Param{{Name: "it", Type: &NamedType{Name: "Iterator", Args: []Type{TInt}}}},
			Return: TUnit,
			Body:   &Block{},
		}
	}
	in := &Module{Package: "main", Decls: []Decl{iter, makeUser("a"), makeUser("b")}}
	out, _ := Monomorphize(in)
	specs := 0
	for _, d := range out.Decls {
		if id, ok := d.(*InterfaceDecl); ok && strings.HasPrefix(id.Name, "_ZTS") {
			specs++
		}
	}
	if specs != 1 {
		t.Fatalf("expected 1 deduped Iterator<Int> spec, got %d", specs)
	}
}

func TestMonomorphizeGenericInterfaceDistinctArgs(t *testing.T) {
	iter := genericIteratorInterface()
	a := &FnDecl{
		Name:   "a",
		Params: []*Param{{Name: "it", Type: &NamedType{Name: "Iterator", Args: []Type{TInt}}}},
		Return: TUnit, Body: &Block{},
	}
	b := &FnDecl{
		Name:   "b",
		Params: []*Param{{Name: "it", Type: &NamedType{Name: "Iterator", Args: []Type{TBool}}}},
		Return: TUnit, Body: &Block{},
	}
	in := &Module{Package: "main", Decls: []Decl{iter, a, b}}
	out, _ := Monomorphize(in)
	specs := 0
	for _, d := range out.Decls {
		if id, ok := d.(*InterfaceDecl); ok && strings.HasPrefix(id.Name, "_ZTS") {
			specs++
		}
	}
	if specs != 2 {
		t.Fatalf("expected 2 specs for distinct type args, got %d", specs)
	}
}

func TestMonomorphizeUnusedGenericInterfaceDropped(t *testing.T) {
	iter := genericIteratorInterface()
	in := &Module{Package: "main", Decls: []Decl{iter}}
	out, errs := Monomorphize(in)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	for _, d := range out.Decls {
		if _, ok := d.(*InterfaceDecl); ok {
			t.Fatalf("unused generic interface should be dropped, still in output: %+v", d)
		}
	}
}

func TestMonomorphizeGenericInterfaceArityMismatch(t *testing.T) {
	iter := genericIteratorInterface() // Generics=1
	// Iterator<Int, Bool> — 2 type args
	user := &FnDecl{
		Name:   "use",
		Params: []*Param{{Name: "it", Type: &NamedType{Name: "Iterator", Args: []Type{TInt, TBool}}}},
		Return: TUnit, Body: &Block{},
	}
	in := &Module{Package: "main", Decls: []Decl{iter, user}}
	_, errs := Monomorphize(in)
	if len(errs) == 0 {
		t.Fatalf("expected arity-mismatch error, got none")
	}
	joined := ""
	for _, e := range errs {
		joined += e.Error() + "\n"
	}
	if !strings.Contains(joined, "arity mismatch") || !strings.Contains(joined, "Iterator") {
		t.Fatalf("expected 'arity mismatch' + 'Iterator' in err, got %q", joined)
	}
}

func TestMonomorphizeInfersVariantLitTypeFromIntPayload(t *testing.T) {
	// Simulate the front-end's ErrType drop: Maybe.Some(42) with every
	// type slot left as ErrType. The recovery heuristic should infer
	// `Maybe<Int>` from the IntLit payload, drive a Maybe<Int>
	// specialization, and rewrite the VariantLit.T + Enum accordingly.
	maybe := genericMaybeEnum()
	lit := &VariantLit{
		Enum:    "Maybe",
		Variant: "Some",
		T:       ErrTypeVal,
		Args:    []Arg{{Value: &IntLit{Text: "42", T: ErrTypeVal}}},
	}
	main := &FnDecl{
		Name: "main", Return: TUnit,
		Body: &Block{Stmts: []Stmt{&LetStmt{
			Name:  "m",
			Type:  ErrTypeVal, // no annotation
			Value: lit,
		}}},
	}
	in := &Module{Package: "main", Decls: []Decl{maybe, main}}
	out, errs := Monomorphize(in)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	spec := findEnumSpec(out)
	if spec == nil {
		t.Fatalf("Maybe specialization missing; decls=%+v", out.Decls)
	}
	// Post-rewrite VariantLit should carry the mangled enum name in
	// both Enum and T fields.
	mainCp := out.Decls[0].(*FnDecl)
	litCp := mainCp.Body.Stmts[0].(*LetStmt).Value.(*VariantLit)
	if !strings.HasPrefix(litCp.Enum, "_ZTS") {
		t.Fatalf("VariantLit.Enum should be mangled post-recovery, got %q", litCp.Enum)
	}
	if nt, ok := litCp.T.(*NamedType); !ok || nt.Name != litCp.Enum {
		t.Fatalf("VariantLit.T should sync with mangled Enum, got %+v", litCp.T)
	}
	// The LetStmt annotation was missing — the scanner should have
	// adopted the value's concrete type.
	let := mainCp.Body.Stmts[0].(*LetStmt)
	if _, isErr := let.Type.(*ErrType); isErr {
		t.Fatalf("LetStmt.Type should be propagated from value, still ErrType")
	}
}

func TestMonomorphizeVariantRecoveryLeavesPayloadFreeAlone(t *testing.T) {
	// `Maybe.None` has no payload, so there's no concrete type to unify
	// T against — the heuristic must bail out and leave the VariantLit
	// untouched (no specialization requested).
	maybe := genericMaybeEnum()
	lit := &VariantLit{
		Enum:    "Maybe",
		Variant: "None",
		T:       ErrTypeVal,
	}
	main := &FnDecl{
		Name: "main", Return: TUnit,
		Body: &Block{Stmts: []Stmt{&LetStmt{Name: "m", Type: ErrTypeVal, Value: lit}}},
	}
	in := &Module{Package: "main", Decls: []Decl{maybe, main}}
	out, _ := Monomorphize(in)
	if findEnumSpec(out) != nil {
		t.Fatalf("payload-free Maybe.None must NOT drive a specialization (no inferable T)")
	}
}

func TestMonomorphizeStructArityMismatchRecordsError(t *testing.T) {
	pair := genericPairStruct()
	main := &FnDecl{
		Name: "main", Return: TUnit,
		Body: &Block{Stmts: []Stmt{&LetStmt{
			Name: "p",
			Type: &NamedType{Name: "Pair", Args: []Type{TInt}},
			Value: &StructLit{
				TypeName: "Pair",
				T:        &NamedType{Name: "Pair", Args: []Type{TInt}},
				Fields:   []StructLitField{{Name: "first", Value: intLit("1")}},
			},
		}}},
	}
	in := &Module{Package: "main", Decls: []Decl{pair, main}}
	_, errs := Monomorphize(in)
	if len(errs) == 0 {
		t.Fatalf("expected arity-mismatch error for Pair with 1 type arg, got none")
	}
	joined := ""
	for _, e := range errs {
		joined += e.Error() + "\n"
	}
	if !strings.Contains(joined, "arity mismatch") || !strings.Contains(joined, "Pair") {
		t.Fatalf("expected 'arity mismatch' + struct name in err, got %q", joined)
	}
}
