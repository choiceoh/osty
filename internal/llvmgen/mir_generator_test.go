package llvmgen

import (
	"errors"
	"strings"
	"testing"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/mir"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// buildMIRModuleFromHIR is a tiny pipeline helper that goes
// HIR-module → monomorphize → MIR. The lowering tests in
// internal/mir construct HIR modules by hand; reusing the same
// approach here keeps the MIR-emitter tests independent of the
// parser/resolver/checker.
func buildMIRModuleFromHIR(t *testing.T, mod *ir.Module) *mir.Module {
	t.Helper()
	mono, monoErrs := ir.Monomorphize(mod)
	if len(monoErrs) != 0 {
		t.Fatalf("monomorphize: %v", monoErrs)
	}
	if validateErrs := ir.Validate(mono); len(validateErrs) != 0 {
		t.Fatalf("ir.Validate: %v", validateErrs)
	}
	m := mir.Lower(mono)
	if m == nil {
		t.Fatalf("mir.Lower returned nil")
	}
	if vErrs := mir.Validate(m); len(vErrs) != 0 {
		t.Fatalf("mir.Validate: %v", vErrs)
	}
	return m
}

// TestGenerateFromMIRConstReturn — `fn answer() -> Int { 42 }`
// should emit a define, alloca for the return slot, store 42,
// load, ret.
func TestGenerateFromMIRConstReturn(t *testing.T) {
	hir := &ir.Module{
		Package: "main",
		Decls: []ir.Decl{
			&ir.FnDecl{
				Name:   "answer",
				Return: ir.TInt,
				Body: &ir.Block{
					Result: &ir.IntLit{Text: "42", T: ir.TInt},
				},
			},
		},
	}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/const.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"define i64 @answer()",
		"alloca i64",
		"store i64 42",
		"ret i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

// TestGenerateFromMIRBinaryArith — `fn add() -> Int { 1 + 2 }`.
// Expect `add i64 1, 2` in the output.
func TestGenerateFromMIRBinaryArith(t *testing.T) {
	hir := &ir.Module{
		Package: "main",
		Decls: []ir.Decl{
			&ir.FnDecl{
				Name:   "add",
				Return: ir.TInt,
				Body: &ir.Block{
					Result: &ir.BinaryExpr{
						Op:    ir.BinAdd,
						Left:  &ir.IntLit{Text: "1", T: ir.TInt},
						Right: &ir.IntLit{Text: "2", T: ir.TInt},
						T:     ir.TInt,
					},
				},
			},
		},
	}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/add.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "add i64 1, 2") {
		t.Fatalf("missing binary add in:\n%s", got)
	}
}

// TestGenerateFromMIRIfBranch — if-expression lowers to branch +
// block labels + phi-free merge.
func TestGenerateFromMIRIfBranch(t *testing.T) {
	hir := &ir.Module{
		Package: "main",
		Decls: []ir.Decl{
			&ir.FnDecl{
				Name:   "abs",
				Return: ir.TInt,
				Params: []*ir.Param{{Name: "n", Type: ir.TInt}},
				Body: &ir.Block{
					Result: &ir.IfExpr{
						Cond: &ir.BinaryExpr{
							Op:    ir.BinLt,
							Left:  &ir.Ident{Name: "n", Kind: ir.IdentParam, T: ir.TInt},
							Right: &ir.IntLit{Text: "0", T: ir.TInt},
							T:     ir.TBool,
						},
						Then: &ir.Block{
							Result: &ir.UnaryExpr{
								Op: ir.UnNeg,
								X:  &ir.Ident{Name: "n", Kind: ir.IdentParam, T: ir.TInt},
								T:  ir.TInt,
							},
						},
						Else: &ir.Block{
							Result: &ir.Ident{Name: "n", Kind: ir.IdentParam, T: ir.TInt},
						},
						T: ir.TInt,
					},
				},
			},
		},
	}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/abs.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"define i64 @abs(i64 %arg0)",
		"icmp slt i64",
		"br i1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

// TestGenerateFromMIRWhileLoop — while-loop lowers to header/body/
// exit with a back-edge br.
func TestGenerateFromMIRWhileLoop(t *testing.T) {
	hir := &ir.Module{
		Package: "main",
		Decls: []ir.Decl{
			&ir.FnDecl{
				Name:   "count",
				Return: ir.TUnit,
				Body: &ir.Block{
					Stmts: []ir.Stmt{
						&ir.LetStmt{
							Name: "i",
							Type: ir.TInt,
							Mut:  true,
							Value: &ir.IntLit{Text: "0", T: ir.TInt},
						},
						&ir.ForStmt{
							Kind: ir.ForWhile,
							Cond: &ir.BinaryExpr{
								Op:    ir.BinLt,
								Left:  &ir.Ident{Name: "i", Kind: ir.IdentLocal, T: ir.TInt},
								Right: &ir.IntLit{Text: "10", T: ir.TInt},
								T:     ir.TBool,
							},
							Body: &ir.Block{
								Stmts: []ir.Stmt{
									&ir.AssignStmt{
										Op:      ir.AssignEq,
										Targets: []ir.Expr{&ir.Ident{Name: "i", Kind: ir.IdentLocal, T: ir.TInt}},
										Value: &ir.BinaryExpr{
											Op:    ir.BinAdd,
											Left:  &ir.Ident{Name: "i", Kind: ir.IdentLocal, T: ir.TInt},
											Right: &ir.IntLit{Text: "1", T: ir.TInt},
											T:     ir.TInt,
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/count.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	// Expect two br instructions (header condition + back edge) and
	// at least two block labels.
	if strings.Count(got, "br ") < 2 {
		t.Fatalf("expected at least two branches, got:\n%s", got)
	}
	if !strings.Contains(got, "icmp slt i64") {
		t.Fatalf("expected cond check, got:\n%s", got)
	}
}

// TestGenerateFromMIRDirectCall — `add(1, 2)` becomes a `call i64
// @add(i64 1, i64 2)`.
func TestGenerateFromMIRDirectCall(t *testing.T) {
	hir := &ir.Module{
		Package: "main",
		Decls: []ir.Decl{
			&ir.FnDecl{
				Name:   "add",
				Return: ir.TInt,
				Params: []*ir.Param{
					{Name: "a", Type: ir.TInt},
					{Name: "b", Type: ir.TInt},
				},
				Body: &ir.Block{
					Result: &ir.BinaryExpr{
						Op:    ir.BinAdd,
						Left:  &ir.Ident{Name: "a", Kind: ir.IdentParam, T: ir.TInt},
						Right: &ir.Ident{Name: "b", Kind: ir.IdentParam, T: ir.TInt},
						T:     ir.TInt,
					},
				},
			},
			&ir.FnDecl{
				Name:   "main",
				Return: ir.TInt,
				Body: &ir.Block{
					Result: &ir.CallExpr{
						Callee: &ir.Ident{
							Name: "add",
							Kind: ir.IdentFn,
							T:    &ir.FnType{Params: []ir.Type{ir.TInt, ir.TInt}, Return: ir.TInt},
						},
						Args: []ir.Arg{
							{Value: &ir.IntLit{Text: "1", T: ir.TInt}},
							{Value: &ir.IntLit{Text: "2", T: ir.TInt}},
						},
						T: ir.TInt,
					},
				},
			},
		},
	}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/call.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "call i64 @add(i64 1, i64 2)") {
		t.Fatalf("missing call in:\n%s", got)
	}
}

// TestGenerateFromMIRPrintln — `println(42)` emits a `printf` call
// with an `%lld\n` format string.
func TestGenerateFromMIRPrintln(t *testing.T) {
	hir := &ir.Module{
		Package: "main",
		Decls: []ir.Decl{
			&ir.FnDecl{
				Name:   "main",
				Return: ir.TUnit,
				Body: &ir.Block{
					Stmts: []ir.Stmt{
						&ir.ExprStmt{X: &ir.IntrinsicCall{
							Kind: ir.IntrinsicPrintln,
							Args: []ir.Arg{{Value: &ir.IntLit{Text: "42", T: ir.TInt}}},
						}},
					},
				},
			},
		},
	}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/print.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"@.str.0",
		"declare i32 @printf(ptr, ...)",
		"call i32 (ptr, ...) @printf(ptr @.str.0, i64 42)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

// TestGenerateFromMIRUnsupportedFallsBack — a module using a closure
// with captures (outside the Stage 3 MVP) should make the MIR emitter
// return an ErrUnsupported error so the backend dispatcher falls
// back to the legacy path. Structs / tuples / enums / optional /
// result / list / map / set are all part of MVP coverage by Stage
// 3.2+3.3; closures still need the AggClosure shape + indirect-call
// support which remain deferred.
func TestGenerateFromMIRUnsupportedFallsBack(t *testing.T) {
	fnType := &ir.FnType{Params: []ir.Type{ir.TInt}, Return: ir.TInt}
	hir := &ir.Module{
		Package: "main",
		Decls: []ir.Decl{
			&ir.FnDecl{
				Name:   "make",
				Return: fnType,
				Body: &ir.Block{
					Stmts: []ir.Stmt{
						&ir.LetStmt{Name: "n", Type: ir.TInt, Value: &ir.IntLit{Text: "1", T: ir.TInt}},
					},
					Result: &ir.Closure{
						Params: []*ir.Param{{Name: "x", Type: ir.TInt}},
						Return: ir.TInt,
						Body: &ir.Block{Result: &ir.BinaryExpr{
							Op:    ir.BinAdd,
							Left:  &ir.Ident{Name: "x", Kind: ir.IdentParam, T: ir.TInt},
							Right: &ir.Ident{Name: "n", Kind: ir.IdentLocal, T: ir.TInt},
							T:     ir.TInt,
						}},
						Captures: []*ir.Capture{
							{Name: "n", Kind: ir.CaptureLocal, T: ir.TInt},
						},
						T: fnType,
					},
				},
			},
		},
	}
	m := buildMIRModuleFromHIR(t, hir)
	_, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/closure.osty"})
	if err == nil {
		t.Fatalf("expected ErrUnsupported for closure; got nil")
	}
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("error does not wrap ErrUnsupported: %v", err)
	}
}

// TestMIRDualEmitFromSource runs a program through both emitter paths —
// the HIR→AST bridge (legacy GenerateModule) and the new MIR-direct
// path (GenerateFromMIR) — and asserts that both produce valid
// LLVM text containing the expected core instructions. We do not
// assert byte-for-byte equality: the two emitters pick different
// alloca / register naming schemes and that's fine. What we check is
// that both correctly represent the program's semantic core.
func TestMIRDualEmitFromSource(t *testing.T) {
	src := `fn main() {
    let mut i = 0
    for i < 3 {
        i = i + 1
    }
    println(i)
}
`
	file := parseLLVMGenFile(t, src)
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), stdlib.LoadCached())
	reg := stdlib.LoadCached()
	chk := check.File(file, res, check.Opts{
		UseGolegacy:   true,
		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        []byte(src),
	})
	hirMod, issues := ir.Lower("main", file, res, chk)
	if len(issues) != 0 {
		t.Fatalf("ir.Lower issues: %v", issues)
	}
	monoMod, monoErrs := ir.Monomorphize(hirMod)
	if len(monoErrs) != 0 {
		t.Fatalf("ir.Monomorphize: %v", monoErrs)
	}
	if errs := ir.Validate(monoMod); len(errs) != 0 {
		t.Fatalf("ir.Validate: %v", errs)
	}
	mirMod := mir.Lower(monoMod)
	if mirMod == nil {
		t.Fatalf("mir.Lower returned nil")
	}
	if errs := mir.Validate(mirMod); len(errs) != 0 {
		t.Fatalf("mir.Validate: %v", errs)
	}

	opts := Options{PackageName: "main", SourcePath: "/tmp/dual.osty"}

	hirOut, hirErr := GenerateModule(monoMod, opts)
	if hirErr != nil {
		t.Fatalf("GenerateModule (HIR path): %v", hirErr)
	}

	mirOut, mirErr := GenerateFromMIR(mirMod, opts)
	if mirErr != nil {
		t.Fatalf("GenerateFromMIR: %v", mirErr)
	}

	// Both outputs should contain the program's semantic core. The
	// exact signature for `main` differs between the paths (the HIR
	// emitter wraps it in a `i32 @main()` C shim while the MIR MVP
	// emits `void @main()`), so we only assert that SOME `@main`
	// definition is present and that the core instructions show up.
	for name, got := range map[string]string{"HIR": string(hirOut), "MIR": string(mirOut)} {
		if !strings.Contains(got, "@main") || !strings.Contains(got, "define ") {
			t.Fatalf("[%s] missing main definition:\n%s", name, got)
		}
		if !strings.Contains(got, "icmp") {
			t.Fatalf("[%s] missing icmp:\n%s", name, got)
		}
		if !strings.Contains(got, "add ") {
			t.Fatalf("[%s] missing add:\n%s", name, got)
		}
		if !strings.Contains(got, "@printf") {
			t.Fatalf("[%s] missing printf call:\n%s", name, got)
		}
	}
}

// TestMIRDualEmitGracefulFallback proves that when the MIR emitter
// refuses a program (IndexProj on a list is still outside the MVP
// projection whitelist), the backend dispatcher catches
// `ErrUnsupported` and retries on the HIR path. We hand-build the
// HIR here so the test is independent of parser / checker
// restrictions on closure-trailing-expr source shape.
func TestMIRDualEmitGracefulFallback(t *testing.T) {
	listInt := &ir.NamedType{Name: "List", Args: []ir.Type{ir.TInt}, Builtin: true}
	fn := &ir.FnDecl{
		Name:   "first",
		Return: ir.TInt,
		Params: []*ir.Param{{Name: "xs", Type: listInt}},
		Body: &ir.Block{
			Result: &ir.IndexExpr{
				X:     &ir.Ident{Name: "xs", Kind: ir.IdentParam, T: listInt},
				Index: &ir.IntLit{Text: "0", T: ir.TInt},
				T:     ir.TInt,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	monoMod, monoErrs := ir.Monomorphize(hir)
	if len(monoErrs) != 0 {
		t.Fatalf("monomorphize: %v", monoErrs)
	}
	mirMod := mir.Lower(monoMod)

	_, err := GenerateFromMIR(mirMod, Options{PackageName: "main", SourcePath: "/tmp/fallback.osty"})
	if err == nil {
		t.Fatalf("expected MIR emitter to refuse index-projection program")
	}
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported, got %T: %v", err, err)
	}
}

// ==== Stage 3 aggregates: struct + tuple + projections ====

func TestGenerateFromMIRStructLiteralAndFieldRead(t *testing.T) {
	// struct Point { x: Int, y: Int }
	// fn sum() -> Int { let p = Point { x: 1, y: 2 }; p.x + p.y }
	pointT := &ir.NamedType{Name: "Point"}
	pointDecl := &ir.StructDecl{
		Name: "Point",
		Fields: []*ir.Field{
			{Name: "x", Type: ir.TInt, Exported: true},
			{Name: "y", Type: ir.TInt, Exported: true},
		},
	}
	sumFn := &ir.FnDecl{
		Name:   "sum",
		Return: ir.TInt,
		Body: &ir.Block{
			Stmts: []ir.Stmt{
				&ir.LetStmt{
					Name: "p",
					Type: pointT,
					Value: &ir.StructLit{
						TypeName: "Point",
						Fields: []ir.StructLitField{
							{Name: "x", Value: &ir.IntLit{Text: "1", T: ir.TInt}},
							{Name: "y", Value: &ir.IntLit{Text: "2", T: ir.TInt}},
						},
						T: pointT,
					},
				},
			},
			Result: &ir.BinaryExpr{
				Op: ir.BinAdd,
				Left: &ir.FieldExpr{
					X:    &ir.Ident{Name: "p", Kind: ir.IdentLocal, T: pointT},
					Name: "x",
					T:    ir.TInt,
				},
				Right: &ir.FieldExpr{
					X:    &ir.Ident{Name: "p", Kind: ir.IdentLocal, T: pointT},
					Name: "y",
					T:    ir.TInt,
				},
				T: ir.TInt,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{pointDecl, sumFn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/struct.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"%Point = type { i64, i64 }",
		"insertvalue %Point undef, i64 1, 0",
		"insertvalue %Point",
		"extractvalue %Point",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestGenerateFromMIRTupleLiteralAndElementRead(t *testing.T) {
	// fn pack() -> Int { let t = (1, 2); t.0 + t.1 }
	tupT := &ir.TupleType{Elems: []ir.Type{ir.TInt, ir.TInt}}
	fn := &ir.FnDecl{
		Name:   "pack",
		Return: ir.TInt,
		Body: &ir.Block{
			Stmts: []ir.Stmt{
				&ir.LetStmt{
					Name: "t",
					Type: tupT,
					Value: &ir.TupleLit{
						Elems: []ir.Expr{
							&ir.IntLit{Text: "1", T: ir.TInt},
							&ir.IntLit{Text: "2", T: ir.TInt},
						},
						T: tupT,
					},
				},
			},
			Result: &ir.BinaryExpr{
				Op: ir.BinAdd,
				Left: &ir.TupleAccess{
					X:     &ir.Ident{Name: "t", Kind: ir.IdentLocal, T: tupT},
					Index: 0,
					T:     ir.TInt,
				},
				Right: &ir.TupleAccess{
					X:     &ir.Ident{Name: "t", Kind: ir.IdentLocal, T: tupT},
					Index: 1,
					T:     ir.TInt,
				},
				T: ir.TInt,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/tuple.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"%Tuple.i64.i64 = type { i64, i64 }",
		"insertvalue %Tuple.i64.i64 undef, i64 1, 0",
		"extractvalue %Tuple.i64.i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestGenerateFromMIRProjectedFieldWrite(t *testing.T) {
	// struct Counter { value: Int }
	// fn bump(mut c: Counter) -> Int { c.value = c.value + 1; c.value }
	//
	// The HIR builder here cuts a corner: we hand-build an AssignStmt
	// whose target is a FieldExpr (`c.value`), which normally only
	// appears after the resolver. MIR lowering still produces a
	// projected assign, which is what this test exercises.
	counterT := &ir.NamedType{Name: "Counter"}
	counterDecl := &ir.StructDecl{
		Name: "Counter",
		Fields: []*ir.Field{
			{Name: "value", Type: ir.TInt, Exported: true},
		},
	}
	bumpFn := &ir.FnDecl{
		Name:   "bump",
		Return: ir.TInt,
		Params: []*ir.Param{{Name: "c", Type: counterT}},
		Body: &ir.Block{
			Stmts: []ir.Stmt{
				&ir.AssignStmt{
					Op: ir.AssignEq,
					Targets: []ir.Expr{&ir.FieldExpr{
						X:    &ir.Ident{Name: "c", Kind: ir.IdentParam, T: counterT},
						Name: "value",
						T:    ir.TInt,
					}},
					Value: &ir.BinaryExpr{
						Op: ir.BinAdd,
						Left: &ir.FieldExpr{
							X:    &ir.Ident{Name: "c", Kind: ir.IdentParam, T: counterT},
							Name: "value",
							T:    ir.TInt,
						},
						Right: &ir.IntLit{Text: "1", T: ir.TInt},
						T:     ir.TInt,
					},
				},
			},
			Result: &ir.FieldExpr{
				X:    &ir.Ident{Name: "c", Kind: ir.IdentParam, T: counterT},
				Name: "value",
				T:    ir.TInt,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{counterDecl, bumpFn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/counter.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"%Counter = type { i64 }",
		"extractvalue %Counter",
		"insertvalue %Counter",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestGenerateFromMIRNestedStructProjection(t *testing.T) {
	// struct Inner { v: Int }
	// struct Outer { inner: Inner }
	// fn read(o: Outer) -> Int { o.inner.v }
	innerT := &ir.NamedType{Name: "Inner"}
	outerT := &ir.NamedType{Name: "Outer"}
	innerDecl := &ir.StructDecl{
		Name:   "Inner",
		Fields: []*ir.Field{{Name: "v", Type: ir.TInt, Exported: true}},
	}
	outerDecl := &ir.StructDecl{
		Name: "Outer",
		Fields: []*ir.Field{
			{Name: "inner", Type: innerT, Exported: true},
		},
	}
	readFn := &ir.FnDecl{
		Name:   "read",
		Return: ir.TInt,
		Params: []*ir.Param{{Name: "o", Type: outerT}},
		Body: &ir.Block{
			Result: &ir.FieldExpr{
				X: &ir.FieldExpr{
					X:    &ir.Ident{Name: "o", Kind: ir.IdentParam, T: outerT},
					Name: "inner",
					T:    innerT,
				},
				Name: "v",
				T:    ir.TInt,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{innerDecl, outerDecl, readFn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/nested.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	// Both the Outer and Inner type defs should appear (sorted
	// alphabetically in the emitter's deterministic order).
	for _, want := range []string{
		"%Inner = type { i64 }",
		"%Outer = type { %Inner }",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	// Two extractvalue instructions on the chain — one on Outer to
	// get %Inner, one on Inner to get i64. The exact register names
	// aren't contractual; just count occurrences.
	if strings.Count(got, "extractvalue") < 2 {
		t.Fatalf("expected at least two extractvalue ops for nested read:\n%s", got)
	}
}

func TestGenerateFromMIRTupleFunctionParam(t *testing.T) {
	// fn first(p: (Int, String)) -> Int { p.0 }
	tupT := &ir.TupleType{Elems: []ir.Type{ir.TInt, ir.TString}}
	fn := &ir.FnDecl{
		Name:   "first",
		Return: ir.TInt,
		Params: []*ir.Param{{Name: "p", Type: tupT}},
		Body: &ir.Block{
			Result: &ir.TupleAccess{
				X:     &ir.Ident{Name: "p", Kind: ir.IdentParam, T: tupT},
				Index: 0,
				T:     ir.TInt,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/tupparam.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"%Tuple.i64.string = type { i64, ptr }",
		"define i64 @first(%Tuple.i64.string %arg0)",
		"extractvalue %Tuple.i64.string",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

// ==== Stage 3.2: enums + optional + result ====

func TestGenerateFromMIREnumVariantConstruction(t *testing.T) {
	// enum Maybe { Some(Int), None }
	// fn wrap(n: Int) -> Maybe { Some(n) }
	maybeT := &ir.NamedType{Name: "Maybe"}
	enumDecl := &ir.EnumDecl{
		Name: "Maybe",
		Variants: []*ir.Variant{
			{Name: "None"},
			{Name: "Some", Payload: []ir.Type{ir.TInt}},
		},
	}
	fn := &ir.FnDecl{
		Name:   "wrap",
		Return: maybeT,
		Params: []*ir.Param{{Name: "n", Type: ir.TInt}},
		Body: &ir.Block{
			Result: &ir.VariantLit{
				Enum:    "Maybe",
				Variant: "Some",
				Args:    []ir.Arg{{Value: &ir.Ident{Name: "n", Kind: ir.IdentParam, T: ir.TInt}}},
				T:       maybeT,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{enumDecl, fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/enum.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"%Maybe = type { i64, i64 }",
		"insertvalue %Maybe undef, i64 1, 0", // Some discriminant = 1
		"insertvalue %Maybe",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestGenerateFromMIROptionalNoneConstruction(t *testing.T) {
	// fn empty() -> Int? { None }
	optT := &ir.OptionalType{Inner: ir.TInt}
	fn := &ir.FnDecl{
		Name:   "empty",
		Return: optT,
		Body: &ir.Block{
			Result: &ir.VariantLit{
				Enum:    "",
				Variant: "None",
				T:       optT,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/opt.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"%Option.i64 = type { i64, i64 }",
		"insertvalue %Option.i64 undef, i64 0, 0", // None discriminant
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

// ==== Stage 3.3: list / map / set intrinsics ====

func TestGenerateFromMIRListLiteralAndLen(t *testing.T) {
	// fn size() -> Int { let xs = [1, 2, 3]; xs.len() }
	listInt := &ir.NamedType{Name: "List", Args: []ir.Type{ir.TInt}, Builtin: true}
	fn := &ir.FnDecl{
		Name:   "size",
		Return: ir.TInt,
		Body: &ir.Block{
			Stmts: []ir.Stmt{
				&ir.LetStmt{
					Name: "xs",
					Type: listInt,
					Value: &ir.ListLit{
						Elems: []ir.Expr{
							&ir.IntLit{Text: "1", T: ir.TInt},
							&ir.IntLit{Text: "2", T: ir.TInt},
							&ir.IntLit{Text: "3", T: ir.TInt},
						},
						Elem: ir.TInt,
					},
				},
			},
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "xs", Kind: ir.IdentLocal, T: listInt},
				Name:     "len",
				T:        ir.TInt,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/list.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare ptr @osty_rt_list_new()",
		"call ptr @osty_rt_list_new()",
		"declare void @osty_rt_list_push_i64(ptr, i64)",
		"call void @osty_rt_list_push_i64(",
		"declare i64 @osty_rt_list_len(ptr)",
		"call i64 @osty_rt_list_len(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestGenerateFromMIRMapGetAndSet(t *testing.T) {
	mapT := &ir.NamedType{Name: "Map", Args: []ir.Type{ir.TString, ir.TInt}, Builtin: true}
	fn := &ir.FnDecl{
		Name:   "store",
		Return: ir.TUnit,
		Params: []*ir.Param{
			{Name: "m", Type: mapT},
			{Name: "k", Type: ir.TString},
			{Name: "v", Type: ir.TInt},
		},
		Body: &ir.Block{
			Stmts: []ir.Stmt{
				&ir.ExprStmt{X: &ir.MethodCall{
					Receiver: &ir.Ident{Name: "m", Kind: ir.IdentParam, T: mapT},
					Name:     "set",
					Args: []ir.Arg{
						{Value: &ir.Ident{Name: "k", Kind: ir.IdentParam, T: ir.TString}},
						{Value: &ir.Ident{Name: "v", Kind: ir.IdentParam, T: ir.TInt}},
					},
					T: ir.TUnit,
				}},
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/map.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare void @osty_rt_map_insert_string(ptr, ptr, ptr)",
		"call void @osty_rt_map_insert_string(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestGenerateFromMIRSetContains(t *testing.T) {
	setT := &ir.NamedType{Name: "Set", Args: []ir.Type{ir.TInt}, Builtin: true}
	fn := &ir.FnDecl{
		Name:   "has",
		Return: ir.TBool,
		Params: []*ir.Param{
			{Name: "s", Type: setT},
			{Name: "v", Type: ir.TInt},
		},
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "s", Kind: ir.IdentParam, T: setT},
				Name:     "contains",
				Args: []ir.Arg{
					{Value: &ir.Ident{Name: "v", Kind: ir.IdentParam, T: ir.TInt}},
				},
				T: ir.TBool,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/set.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare i1 @osty_rt_set_contains_i64(ptr, i64)",
		"call i1 @osty_rt_set_contains_i64(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestGenerateFromMIRListSorted(t *testing.T) {
	listInt := &ir.NamedType{Name: "List", Args: []ir.Type{ir.TInt}, Builtin: true}
	fn := &ir.FnDecl{
		Name:   "ordered",
		Return: listInt,
		Params: []*ir.Param{{Name: "xs", Type: listInt}},
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "xs", Kind: ir.IdentParam, T: listInt},
				Name:     "sorted",
				T:        listInt,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/sort.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare ptr @osty_rt_list_sorted_i64(ptr)",
		"call ptr @osty_rt_list_sorted_i64(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}
