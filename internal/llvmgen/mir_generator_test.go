package llvmgen

import (
	"errors"
	"regexp"
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
							Name:  "i",
							Type:  ir.TInt,
							Mut:   true,
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

// TestGenerateFromMIRUnsupportedFallsBack — a module using an
// unresolved NamedType (not a Builtin collection, not in the
// module's LayoutTable, not a known prelude name) must trip
// `ErrUnsupported` so the backend dispatcher falls back to the
// legacy path.
func TestGenerateFromMIRUnsupportedFallsBack(t *testing.T) {
	unknownT := &ir.NamedType{Name: "Unknown"}
	hir := &ir.Module{
		Package: "main",
		Decls: []ir.Decl{
			&ir.FnDecl{
				Name:   "use",
				Return: ir.TUnit,
				Params: []*ir.Param{{Name: "x", Type: unknownT}},
				Body:   &ir.Block{},
			},
		},
	}
	m := buildMIRModuleFromHIR(t, hir)
	_, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/unknown.osty"})
	if err == nil {
		t.Fatalf("expected ErrUnsupported for unknown NamedType; got nil")
	}
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("error does not wrap ErrUnsupported: %v", err)
	}
}

func TestGenerateFromMIRAllowsPoisonedUnusedLocal(t *testing.T) {
	fn := &mir.Function{
		Name:       "answer",
		ReturnType: ir.TInt,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: ir.TInt, Mut: true, IsReturn: true},
			{ID: 1, Name: "poison", Type: ir.ErrTypeVal},
		},
		ReturnLocal: 0,
		Entry:       0,
		Blocks: []*mir.BasicBlock{
			{
				ID: 0,
				Instrs: []mir.Instr{
					&mir.AssignInstr{
						Dest: mir.Place{Local: 0},
						Src: &mir.UseRV{Op: &mir.ConstOp{
							Const: &mir.IntConst{Value: 42, T: ir.TInt},
							T:     ir.TInt,
						}},
					},
				},
				Term: &mir.ReturnTerm{},
			},
		},
	}
	m := &mir.Module{
		Package:   "main",
		Functions: []*mir.Function{fn},
		Layouts:   mir.NewLayoutTable(),
	}
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/poisoned_local.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"define i64 @answer()",
		"%l1 = alloca ptr",
		"store i64 42",
		"ret i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestGenerateFromMIRRecoversPoisonedLetTypeFromValue(t *testing.T) {
	hir := &ir.Module{
		Package: "main",
		Decls: []ir.Decl{
			&ir.FnDecl{
				Name:   "label",
				Return: ir.TString,
				Body: &ir.Block{
					Stmts: []ir.Stmt{
						&ir.LetStmt{
							Name:  "prefix",
							Type:  ir.ErrTypeVal,
							Value: &ir.StringLit{Parts: []ir.StringPart{{IsLit: true, Lit: "warn"}}},
						},
					},
					Result: &ir.Ident{Name: "prefix", Kind: ir.IdentLocal, T: ir.TString},
				},
			},
		},
	}
	m := buildMIRModuleFromHIR(t, hir)
	fn := m.LookupFunction("label")
	if fn == nil {
		t.Fatalf("missing label function")
	}
	if got := fn.Locals[1].Type.String(); got != "String" {
		t.Fatalf("expected recovered let local type String, got %s", got)
	}
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/recovered_let.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	if !strings.Contains(string(out), "define ptr @label()") {
		t.Fatalf("missing label definition in:\n%s", out)
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

	// Both outputs should contain the program's semantic core and a
	// C-entry-compatible `i32 @main()` so linked binaries exit through
	// a defined status code instead of inheriting an undefined `void`
	// main ABI.
	for name, got := range map[string]string{"HIR": string(hirOut), "MIR": string(mirOut)} {
		if !strings.Contains(got, "define i32 @main()") {
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
		if !strings.Contains(got, "ret i32 0") {
			t.Fatalf("[%s] missing C-entry return 0:\n%s", name, got)
		}
	}
}

// TestMIRDualEmitGracefulFallback proves that when the MIR emitter
// refuses a program (a closure with captures is still outside the
// MVP), the backend dispatcher catches `ErrUnsupported` and retries
// on the HIR path. We hand-build the HIR here so the test is
// independent of parser / checker restrictions on closure-trailing-
// expr source shape.
func TestMIRDualEmitGracefulFallback(t *testing.T) {
	// Use an unresolved NamedType — still outside MVP.
	unknownT := &ir.NamedType{Name: "Unknown"}
	hir := &ir.Module{
		Package: "main",
		Decls: []ir.Decl{
			&ir.FnDecl{
				Name:   "use",
				Return: ir.TUnit,
				Params: []*ir.Param{{Name: "x", Type: unknownT}},
				Body:   &ir.Block{},
			},
		},
	}
	monoMod, monoErrs := ir.Monomorphize(hir)
	if len(monoErrs) != 0 {
		t.Fatalf("monomorphize: %v", monoErrs)
	}
	mirMod := mir.Lower(monoMod)

	_, err := GenerateFromMIR(mirMod, Options{PackageName: "main", SourcePath: "/tmp/fallback.osty"})
	if err == nil {
		t.Fatalf("expected MIR emitter to refuse global-bearing program")
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

func TestGenerateFromMIRForInListUsesLenRV(t *testing.T) {
	listInt := &ir.NamedType{Name: "List", Args: []ir.Type{ir.TInt}, Builtin: true}
	hir := &ir.Module{
		Package: "main",
		Decls: []ir.Decl{
			&ir.FnDecl{
				Name:   "walk",
				Return: ir.TUnit,
				Params: []*ir.Param{{Name: "xs", Type: listInt}},
				Body: &ir.Block{
					Stmts: []ir.Stmt{
						&ir.ForStmt{
							Kind: ir.ForIn,
							Var:  "x",
							Iter: &ir.Ident{Name: "xs", Kind: ir.IdentParam, T: listInt},
							Body: &ir.Block{},
						},
					},
				},
			},
		},
	}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/for_in_list.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
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

// TestGenerateFromMIRMapStructValueSet — Map<String, Point> insert.
// The composite value is spilled into a stack slot sized to %Point,
// then passed by pointer to the runtime. This is the same shape the
// primitive-value path uses after the Stage 3.11-follow-up rewrite;
// composite values are what made the rewrite necessary.
func TestGenerateFromMIRMapStructValueSet(t *testing.T) {
	// struct Point { x: Int, y: Int }
	// fn store(m: Map<String, Point>, k: String, p: Point) { m.set(k, p) }
	pointT := &ir.NamedType{Name: "Point"}
	pointDecl := &ir.StructDecl{
		Name: "Point",
		Fields: []*ir.Field{
			{Name: "x", Type: ir.TInt, Exported: true},
			{Name: "y", Type: ir.TInt, Exported: true},
		},
	}
	mapT := &ir.NamedType{Name: "Map", Args: []ir.Type{ir.TString, pointT}, Builtin: true}
	fn := &ir.FnDecl{
		Name:   "store",
		Return: ir.TUnit,
		Params: []*ir.Param{
			{Name: "m", Type: mapT},
			{Name: "k", Type: ir.TString},
			{Name: "p", Type: pointT},
		},
		Body: &ir.Block{
			Stmts: []ir.Stmt{
				&ir.ExprStmt{X: &ir.MethodCall{
					Receiver: &ir.Ident{Name: "m", Kind: ir.IdentParam, T: mapT},
					Name:     "set",
					Args: []ir.Arg{
						{Value: &ir.Ident{Name: "k", Kind: ir.IdentParam, T: ir.TString}},
						{Value: &ir.Ident{Name: "p", Kind: ir.IdentParam, T: pointT}},
					},
					T: ir.TUnit,
				}},
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{pointDecl, fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/map_struct_set.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		// Struct layout for Point.
		"%Point = type { i64, i64 }",
		// Insert signature stays `(ptr, key, ptr)`.
		"declare void @osty_rt_map_insert_string(ptr, ptr, ptr)",
		// Value is spilled into a %Point slot before the call.
		"alloca %Point",
		"store %Point ",
		// The runtime gets the composite by pointer.
		"call void @osty_rt_map_insert_string(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	// Order check: the alloca and store must precede the map_insert
	// call — otherwise the runtime would memcpy from undef memory.
	allocaIdx := strings.Index(got, "alloca %Point")
	storeIdx := strings.Index(got, "store %Point ")
	callIdx := strings.Index(got, "call void @osty_rt_map_insert_string(")
	if allocaIdx < 0 || storeIdx < 0 || callIdx < 0 {
		t.Fatalf("ordering markers missing in:\n%s", got)
	}
	if !(allocaIdx < storeIdx && storeIdx < callIdx) {
		t.Fatalf("expected alloca → store → call ordering in:\n%s", got)
	}
}

// TestGenerateFromMIRMapStructValueGet — Map<String, Point> read.
// The out-slot is sized to %Point; the runtime writes into it; we
// load and store into the destination local. Validates the rewritten
// `void(ptr, K, ptr)` signature and the post-load copy to dest.
func TestGenerateFromMIRMapStructValueGet(t *testing.T) {
	// struct Point { x: Int, y: Int }
	// fn lookup(m: Map<String, Point>, k: String) -> Point { m[k] }
	pointT := &ir.NamedType{Name: "Point"}
	pointDecl := &ir.StructDecl{
		Name: "Point",
		Fields: []*ir.Field{
			{Name: "x", Type: ir.TInt, Exported: true},
			{Name: "y", Type: ir.TInt, Exported: true},
		},
	}
	mapT := &ir.NamedType{Name: "Map", Args: []ir.Type{ir.TString, pointT}, Builtin: true}
	fn := &ir.FnDecl{
		Name:   "lookup",
		Return: pointT,
		Params: []*ir.Param{
			{Name: "m", Type: mapT},
			{Name: "k", Type: ir.TString},
		},
		Body: &ir.Block{
			Result: &ir.IndexExpr{
				X:     &ir.Ident{Name: "m", Kind: ir.IdentParam, T: mapT},
				Index: &ir.Ident{Name: "k", Kind: ir.IdentParam, T: ir.TString},
				T:     pointT,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{pointDecl, fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/map_struct_get.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"%Point = type { i64, i64 }",
		// Get now has the corrected `void(ptr, K, ptr)` signature
		// matching the real runtime.
		"declare void @osty_rt_map_get_or_abort_string(ptr, ptr, ptr)",
		// Out-slot sized to %Point.
		"alloca %Point",
		// The call takes the out-slot as its third argument.
		"call void @osty_rt_map_get_or_abort_string(",
		// Load from the out-slot.
		"load %Point, ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

// TestGenerateFromMIRMapPrimitiveGet — primitive-value map_get after
// the rewrite. Regression guard against reverting to the old
// ptr-returning signature.
func TestGenerateFromMIRMapPrimitiveGet(t *testing.T) {
	// fn lookup(m: Map<String, Int>, k: String) -> Int { m[k] }
	mapT := &ir.NamedType{Name: "Map", Args: []ir.Type{ir.TString, ir.TInt}, Builtin: true}
	fn := &ir.FnDecl{
		Name:   "lookup",
		Return: ir.TInt,
		Params: []*ir.Param{
			{Name: "m", Type: mapT},
			{Name: "k", Type: ir.TString},
		},
		Body: &ir.Block{
			Result: &ir.IndexExpr{
				X:     &ir.Ident{Name: "m", Kind: ir.IdentParam, T: mapT},
				Index: &ir.Ident{Name: "k", Kind: ir.IdentParam, T: ir.TString},
				T:     ir.TInt,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/map_prim_get.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare void @osty_rt_map_get_or_abort_string(ptr, ptr, ptr)",
		"call void @osty_rt_map_get_or_abort_string(",
		"alloca i64",
		"load i64, ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	// The old ptr-returning signature must not leak back in.
	if strings.Contains(got, "declare ptr @osty_rt_map_get_or_abort_") {
		t.Fatalf("regressed to ptr-returning get signature:\n%s", got)
	}
}

// TestGenerateFromMIRMapGetMethodDestSlotFastPath — `let v = m.get(k)`
// lowers to an IntrinsicMapGet with a known Dest local. When the
// destination slot's LLVM type matches the value type, the emitter
// hands the dest slot directly to the runtime — no extra alloca +
// load + store pair. Locks in the fast path extracted during the
// Stage 3.12 refactor.
func TestGenerateFromMIRMapGetMethodDestSlotFastPath(t *testing.T) {
	// fn lookup(m: Map<String, Int>, k: String) -> Int {
	//     let v = m.get(k)
	//     v
	// }
	mapT := &ir.NamedType{Name: "Map", Args: []ir.Type{ir.TString, ir.TInt}, Builtin: true}
	fn := &ir.FnDecl{
		Name:   "lookup",
		Return: ir.TInt,
		Params: []*ir.Param{
			{Name: "m", Type: mapT},
			{Name: "k", Type: ir.TString},
		},
		Body: &ir.Block{
			Stmts: []ir.Stmt{
				&ir.LetStmt{
					Name: "v",
					Type: ir.TInt,
					Value: &ir.MethodCall{
						Receiver: &ir.Ident{Name: "m", Kind: ir.IdentParam, T: mapT},
						Name:     "get",
						Args: []ir.Arg{
							{Value: &ir.Ident{Name: "k", Kind: ir.IdentParam, T: ir.TString}},
						},
						T: ir.TInt,
					},
				},
			},
			Result: &ir.Ident{Name: "v", Kind: ir.IdentLocal, T: ir.TInt},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/map_get_method.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	// The runtime call's 3rd argument is the dest local's alloca
	// slot (`%lN`) rather than a fresh emitter temp (`%tN`). That's
	// the signal that the fast path skipped the extra alloca +
	// load + store a separate out-slot would require.
	if !strings.Contains(got, "call void @osty_rt_map_get_or_abort_string(ptr %t0, ptr %t1, ptr %l") {
		t.Fatalf("expected runtime call to use dest local slot directly:\n%s", got)
	}
	// Fresh-temp out-slot would look like `call ... ptr %t<N>)` at
	// the 3rd arg — guard against it.
	if strings.Contains(got, "call void @osty_rt_map_get_or_abort_string(ptr %t0, ptr %t1, ptr %t") {
		t.Fatalf("dest-slot fast path regressed to fresh-temp out-slot:\n%s", got)
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

// ==== Stage 3.4: non-capturing closures + indirect calls ====

func TestGenerateFromMIRNonCapturingClosureValue(t *testing.T) {
	// fn pickDouble() -> fn(Int) -> Int { |x| x + 1 }
	// MIR lowering for a non-capturing closure produces AggregateRV{
	// Kind: AggClosure, Fields: [FnConst("<parent>__closure1")]}.
	// The MIR emitter should collapse that to the fn-pointer value,
	// emitting `@<symbol>` directly.
	fnType := &ir.FnType{Params: []ir.Type{ir.TInt}, Return: ir.TInt}
	pickDouble := &ir.FnDecl{
		Name:   "pickDouble",
		Return: fnType,
		Body: &ir.Block{
			Result: &ir.Closure{
				Params: []*ir.Param{{Name: "x", Type: ir.TInt}},
				Return: ir.TInt,
				Body: &ir.Block{Result: &ir.BinaryExpr{
					Op:    ir.BinAdd,
					Left:  &ir.Ident{Name: "x", Kind: ir.IdentParam, T: ir.TInt},
					Right: &ir.IntLit{Text: "1", T: ir.TInt},
					T:     ir.TInt,
				}},
				T: fnType,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{pickDouble}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/closure.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	// Under the uniform env ABI, a non-capturing closure still
	// allocates a 1-field env `{ ptr fn }` and the closure VALUE is
	// the env ptr. The lifted fn takes env as first arg.
	if !strings.Contains(got, "define ptr @pickDouble()") {
		t.Fatalf("expected pickDouble to return ptr (env), got:\n%s", got)
	}
	// The lifted closure body receives env as its first param.
	if !strings.Contains(got, "define i64 @pickDouble__closure1(ptr ") {
		t.Fatalf("expected lifted closure with env-first-arg ABI, got:\n%s", got)
	}
	// The closure value is an env struct allocated on the stack with
	// the fn ptr stored at slot 0.
	if !strings.Contains(got, "alloca %ClosureEnv.ptr") {
		t.Fatalf("expected 1-field closure env alloca, got:\n%s", got)
	}
	if !strings.Contains(got, "store ptr @pickDouble__closure1") {
		t.Fatalf("expected fn-pointer store of @pickDouble__closure1 into env slot:\n%s", got)
	}
}

func TestGenerateFromMIRIndirectCall(t *testing.T) {
	// fn apply(f: fn(Int) -> Int, x: Int) -> Int { f(x) }
	fnType := &ir.FnType{Params: []ir.Type{ir.TInt}, Return: ir.TInt}
	fn := &ir.FnDecl{
		Name:   "apply",
		Return: ir.TInt,
		Params: []*ir.Param{
			{Name: "f", Type: fnType},
			{Name: "x", Type: ir.TInt},
		},
		Body: &ir.Block{
			Result: &ir.CallExpr{
				Callee: &ir.Ident{Name: "f", Kind: ir.IdentLocal, T: fnType},
				Args: []ir.Arg{
					{Value: &ir.Ident{Name: "x", Kind: ir.IdentParam, T: ir.TInt}},
				},
				T: ir.TInt,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/apply.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	// The body must call through the ptr-typed local. Under the
	// uniform closure ABI (Stage 3.8), every IndirectCall passes the
	// env pointer as implicit first arg, so the signature includes
	// `ptr` even though the user FnType is just `fn(Int) -> Int`.
	for _, want := range []string{
		"define i64 @apply(ptr %arg0, i64 %arg1)",
		// Load fn ptr from env[0], then call with env prefix.
		"call i64 (ptr, i64)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

// ==== Stage 3.5: IndexProj on List ====

func TestGenerateFromMIRListIndexRead(t *testing.T) {
	// fn first(xs: List<Int>) -> Int { xs[0] }
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
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/idx.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare i64 @osty_rt_list_get_i64(ptr, i64)",
		"call i64 @osty_rt_list_get_i64(ptr ",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestGenerateFromMIRListIndexReadPtrElem(t *testing.T) {
	// fn head(xs: List<String>) -> String { xs[0] }
	listStr := &ir.NamedType{Name: "List", Args: []ir.Type{ir.TString}, Builtin: true}
	fn := &ir.FnDecl{
		Name:   "head",
		Return: ir.TString,
		Params: []*ir.Param{{Name: "xs", Type: listStr}},
		Body: &ir.Block{
			Result: &ir.IndexExpr{
				X:     &ir.Ident{Name: "xs", Kind: ir.IdentParam, T: listStr},
				Index: &ir.IntLit{Text: "0", T: ir.TInt},
				T:     ir.TString,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/idx-str.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	// String elements are ptr-wide in the runtime, so the typed
	// runtime suffix is `_ptr`.
	for _, want := range []string{
		"declare ptr @osty_rt_list_get_ptr(ptr, i64)",
		"call ptr @osty_rt_list_get_ptr(ptr ",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestGenerateFromMIRVectorizedScalarListParamUsesRawDataFastPath(t *testing.T) {
	src := `fn sumDot(xs: List<Int>, ys: List<Int>, n: Int) -> Int {
    let mut acc = 0
    for i in 0..n {
        acc = acc + xs[i] * ys[i]
    }
    acc
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
	hir, issues := ir.Lower("main", file, res, chk)
	if len(issues) != 0 {
		t.Fatalf("ir.Lower issues: %v", issues)
	}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/sumdot.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "declare ptr @osty_rt_list_data_i64(ptr)") {
		t.Fatalf("expected raw-data helper declaration in:\n%s", got)
	}
	if gotCount := strings.Count(got, "call ptr @osty_rt_list_data_i64(ptr "); gotCount != 2 {
		t.Fatalf("expected two raw-data cache calls (xs + ys), got %d in:\n%s", gotCount, got)
	}
	if !strings.Contains(got, "getelementptr inbounds i64, ptr ") {
		t.Fatalf("expected scalar list fast path GEP in:\n%s", got)
	}
}

// ==== Stage 3.6: composite list element types (bytes ABI) ====

func TestGenerateFromMIRListStructPushAndRead(t *testing.T) {
	// struct Point { x: Int, y: Int }
	// fn build() -> Int {
	//     let xs = [Point { x: 1, y: 2 }]
	//     xs[0].x
	// }
	pointT := &ir.NamedType{Name: "Point"}
	pointDecl := &ir.StructDecl{
		Name: "Point",
		Fields: []*ir.Field{
			{Name: "x", Type: ir.TInt, Exported: true},
			{Name: "y", Type: ir.TInt, Exported: true},
		},
	}
	listPoint := &ir.NamedType{Name: "List", Args: []ir.Type{pointT}, Builtin: true}
	fn := &ir.FnDecl{
		Name:   "build",
		Return: ir.TInt,
		Body: &ir.Block{
			Stmts: []ir.Stmt{
				&ir.LetStmt{
					Name: "xs",
					Type: listPoint,
					Value: &ir.ListLit{
						Elems: []ir.Expr{
							&ir.StructLit{
								TypeName: "Point",
								Fields: []ir.StructLitField{
									{Name: "x", Value: &ir.IntLit{Text: "1", T: ir.TInt}},
									{Name: "y", Value: &ir.IntLit{Text: "2", T: ir.TInt}},
								},
								T: pointT,
							},
						},
						Elem: pointT,
					},
				},
			},
			Result: &ir.FieldExpr{
				X: &ir.IndexExpr{
					X:     &ir.Ident{Name: "xs", Kind: ir.IdentLocal, T: listPoint},
					Index: &ir.IntLit{Text: "0", T: ir.TInt},
					T:     pointT,
				},
				Name: "x",
				T:    ir.TInt,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{pointDecl, fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/list_struct.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		// Struct layout for Point.
		"%Point = type { i64, i64 }",
		// Bytes-ABI push with a Point stack slot.
		"declare void @osty_rt_list_push_bytes_v1(ptr, ptr, i64)",
		"call void @osty_rt_list_push_bytes_v1(",
		// Bytes-ABI get with an out-pointer.
		"declare void @osty_rt_list_get_bytes_v1(ptr, i64, ptr, i64)",
		"call void @osty_rt_list_get_bytes_v1(",
		// Size computed via the gep-null-1 idiom.
		"getelementptr %Point, ptr null, i32 1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestGenerateFromMIRListTupleElement(t *testing.T) {
	tupT := &ir.TupleType{Elems: []ir.Type{ir.TInt, ir.TString}}
	listTup := &ir.NamedType{Name: "List", Args: []ir.Type{tupT}, Builtin: true}
	fn := &ir.FnDecl{
		Name:   "make",
		Return: listTup,
		Body: &ir.Block{
			Result: &ir.ListLit{
				Elems: []ir.Expr{
					&ir.TupleLit{
						Elems: []ir.Expr{
							&ir.IntLit{Text: "1", T: ir.TInt},
							&ir.StringLit{Parts: []ir.StringPart{{IsLit: true, Lit: "a"}}},
						},
						T: tupT,
					},
				},
				Elem: tupT,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/list_tuple.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"%Tuple.i64.string = type { i64, ptr }",
		"call void @osty_rt_list_push_bytes_v1(",
		"getelementptr %Tuple.i64.string, ptr null, i32 1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

// ==== Stage 3.8: capturing closures via uniform env ABI ====

func TestGenerateFromMIRCapturingClosure(t *testing.T) {
	// fn makeAdder() -> fn(Int) -> Int {
	//   let n = 10
	//   |x| x + n     // captures n
	// }
	fnType := &ir.FnType{Params: []ir.Type{ir.TInt}, Return: ir.TInt}
	fn := &ir.FnDecl{
		Name:   "makeAdder",
		Return: fnType,
		Body: &ir.Block{
			Stmts: []ir.Stmt{
				&ir.LetStmt{Name: "n", Type: ir.TInt, Value: &ir.IntLit{Text: "10", T: ir.TInt}},
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
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/cap.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	// The lifted fn takes env as first param, then user args.
	if !strings.Contains(got, "define i64 @makeAdder__closure1(ptr ") {
		t.Fatalf("expected lifted fn with env-first-arg ABI, got:\n%s", got)
	}
	// The env struct has two fields: ptr fn + i64 capture.
	if !strings.Contains(got, "alloca %ClosureEnv.ptr.i64") {
		t.Fatalf("expected 2-field env alloca, got:\n%s", got)
	}
	// Both fn ptr and capture get stored into the env via GEPs.
	if !strings.Contains(got, "store ptr @makeAdder__closure1") {
		t.Fatalf("expected fn ptr store, got:\n%s", got)
	}
	if strings.Count(got, "store i64 ") < 1 {
		t.Fatalf("expected at least one capture store, got:\n%s", got)
	}
}

func TestGenerateFromMIRCapturingClosureCalledIndirectly(t *testing.T) {
	// fn apply(f: fn(Int) -> Int, x: Int) -> Int { f(x) }
	//
	// The MIR lowerer routes `f(x)` through IndirectCall. Combined
	// with the env ABI this emits `call i64 (ptr, i64) <fnptr>(<env>,
	// <x>)` — the env ptr (= f itself) is implicit first arg.
	fnType := &ir.FnType{Params: []ir.Type{ir.TInt}, Return: ir.TInt}
	apply := &ir.FnDecl{
		Name:   "apply",
		Return: ir.TInt,
		Params: []*ir.Param{
			{Name: "f", Type: fnType},
			{Name: "x", Type: ir.TInt},
		},
		Body: &ir.Block{
			Result: &ir.CallExpr{
				Callee: &ir.Ident{Name: "f", Kind: ir.IdentLocal, T: fnType},
				Args: []ir.Arg{
					{Value: &ir.Ident{Name: "x", Kind: ir.IdentParam, T: ir.TInt}},
				},
				T: ir.TInt,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{apply}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/apply-cap.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "call i64 (ptr, i64)") {
		t.Fatalf("expected env-first-arg indirect call signature, got:\n%s", got)
	}
	// Load fn ptr from env slot 0.
	if !strings.Contains(got, "= load ptr, ptr ") {
		t.Fatalf("expected env[0] fn-ptr load, got:\n%s", got)
	}
}

func TestGenerateFromMIRTopLevelFnAsValue(t *testing.T) {
	// fn double(x: Int) -> Int { x * 2 }
	// fn apply(f: fn(Int) -> Int, x: Int) -> Int { f(x) }
	// fn main() -> Int { apply(double, 3) }
	//
	// Passing the top-level `double` as a value requires the
	// emitter to wrap it in a thunk + 1-field env so the apply
	// callee can use the uniform env ABI.
	fnType := &ir.FnType{Params: []ir.Type{ir.TInt}, Return: ir.TInt}
	double := &ir.FnDecl{
		Name:   "double",
		Return: ir.TInt,
		Params: []*ir.Param{{Name: "x", Type: ir.TInt}},
		Body: &ir.Block{
			Result: &ir.BinaryExpr{
				Op:    ir.BinMul,
				Left:  &ir.Ident{Name: "x", Kind: ir.IdentParam, T: ir.TInt},
				Right: &ir.IntLit{Text: "2", T: ir.TInt},
				T:     ir.TInt,
			},
		},
	}
	apply := &ir.FnDecl{
		Name:   "apply",
		Return: ir.TInt,
		Params: []*ir.Param{
			{Name: "f", Type: fnType},
			{Name: "x", Type: ir.TInt},
		},
		Body: &ir.Block{
			Result: &ir.CallExpr{
				Callee: &ir.Ident{Name: "f", Kind: ir.IdentLocal, T: fnType},
				Args: []ir.Arg{
					{Value: &ir.Ident{Name: "x", Kind: ir.IdentParam, T: ir.TInt}},
				},
				T: ir.TInt,
			},
		},
	}
	mainFn := &ir.FnDecl{
		Name:   "main",
		Return: ir.TInt,
		Body: &ir.Block{
			Result: &ir.CallExpr{
				Callee: &ir.Ident{
					Name: "apply", Kind: ir.IdentFn,
					T: &ir.FnType{Params: []ir.Type{fnType, ir.TInt}, Return: ir.TInt},
				},
				Args: []ir.Arg{
					{Value: &ir.Ident{Name: "double", Kind: ir.IdentFn, T: fnType}},
					{Value: &ir.IntLit{Text: "3", T: ir.TInt}},
				},
				T: ir.TInt,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{double, apply, mainFn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/toplevel.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	// A thunk is generated for `double` so the uniform env ABI can
	// call through it.
	if !strings.Contains(got, "define private i64 @__osty_closure_thunk_double(ptr ") {
		t.Fatalf("expected thunk for double, got:\n%s", got)
	}
	if !strings.Contains(got, "call i64 @double(i64 %arg0)") {
		t.Fatalf("expected thunk to delegate to @double, got:\n%s", got)
	}
}

// ==== Stage 3.10: top-level globals ====

func TestGenerateFromMIRGlobalReadAndInit(t *testing.T) {
	// pub let version = 42
	// fn get() -> Int { version }
	hir := &ir.Module{
		Package: "main",
		Decls: []ir.Decl{
			&ir.LetDecl{
				Name:     "version",
				Type:     ir.TInt,
				Value:    &ir.IntLit{Text: "42", T: ir.TInt},
				Exported: true,
			},
			&ir.FnDecl{
				Name:   "get",
				Return: ir.TInt,
				Body: &ir.Block{
					Result: &ir.Ident{Name: "version", Kind: ir.IdentGlobal, T: ir.TInt},
				},
			},
		},
	}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/global.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		// Module-level global with zeroinitializer.
		"@version = global i64 zeroinitializer",
		// Init fn from the MIR lowerer.
		"define i64 @_init_version()",
		// Ctor runs the init and stores into the global.
		"define private void @__osty_init_globals()",
		"call i64 @_init_version()",
		"store i64 %vversion, ptr @version",
		// llvm.global_ctors registration.
		"@llvm.global_ctors",
		// Read side in `get` loads from the global.
		"load i64, ptr @version",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestGenerateFromMIRMultipleGlobals(t *testing.T) {
	// pub let a = 1
	// pub let b = 2
	// fn sum() -> Int { a + b }
	hir := &ir.Module{
		Package: "main",
		Decls: []ir.Decl{
			&ir.LetDecl{Name: "a", Type: ir.TInt, Value: &ir.IntLit{Text: "1", T: ir.TInt}, Exported: true},
			&ir.LetDecl{Name: "b", Type: ir.TInt, Value: &ir.IntLit{Text: "2", T: ir.TInt}, Exported: true},
			&ir.FnDecl{
				Name:   "sum",
				Return: ir.TInt,
				Body: &ir.Block{
					Result: &ir.BinaryExpr{
						Op:    ir.BinAdd,
						Left:  &ir.Ident{Name: "a", Kind: ir.IdentGlobal, T: ir.TInt},
						Right: &ir.Ident{Name: "b", Kind: ir.IdentGlobal, T: ir.TInt},
						T:     ir.TInt,
					},
				},
			},
		},
	}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/multi.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	// Both globals + both inits + the ctor stores both.
	for _, want := range []string{
		"@a = global i64 zeroinitializer",
		"@b = global i64 zeroinitializer",
		"call i64 @_init_a()",
		"call i64 @_init_b()",
		"store i64 %va, ptr @a",
		"store i64 %vb, ptr @b",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

// ==== Stage 3.9: concurrency intrinsic runtime mapping ====

func TestGenerateFromMIRChanSendRecv(t *testing.T) {
	// fn push(ch: Channel<Int>, v: Int) { ch <- v }
	chanInt := &ir.NamedType{Name: "Channel", Args: []ir.Type{ir.TInt}}
	fn := &ir.FnDecl{
		Name:   "push",
		Return: ir.TUnit,
		Params: []*ir.Param{
			{Name: "ch", Type: chanInt},
			{Name: "v", Type: ir.TInt},
		},
		Body: &ir.Block{
			Stmts: []ir.Stmt{
				&ir.ChanSendStmt{
					Channel: &ir.Ident{Name: "ch", Kind: ir.IdentParam, T: chanInt},
					Value:   &ir.Ident{Name: "v", Kind: ir.IdentParam, T: ir.TInt},
				},
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/chan.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare void @osty_rt_thread_chan_send_i64(ptr, i64)",
		"call void @osty_rt_thread_chan_send_i64(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestGenerateFromMIRChanMakeAndClose(t *testing.T) {
	// use std.thread as thread
	// fn setup() { let ch = thread.chan::<Int>(4); ch.close() }
	useT := &ir.UseDecl{
		Path: []string{"std", "thread"}, RawPath: "std.thread", Alias: "thread",
	}
	chanInt := &ir.NamedType{Name: "Channel", Args: []ir.Type{ir.TInt}}
	fn := &ir.FnDecl{
		Name:   "setup",
		Return: ir.TUnit,
		Body: &ir.Block{
			Stmts: []ir.Stmt{
				&ir.LetStmt{
					Name: "ch",
					Type: chanInt,
					Value: &ir.MethodCall{
						Receiver: &ir.Ident{Name: "thread", T: &ir.NamedType{Name: "ThreadModule"}},
						Name:     "chan",
						TypeArgs: []ir.Type{ir.TInt},
						Args:     []ir.Arg{{Value: &ir.IntLit{Text: "4", T: ir.TInt}}},
						T:        chanInt,
					},
				},
				&ir.ExprStmt{X: &ir.MethodCall{
					Receiver: &ir.Ident{Name: "ch", Kind: ir.IdentLocal, T: chanInt},
					Name:     "close",
					T:        ir.TUnit,
				}},
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{useT, fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/make.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare ptr @osty_rt_thread_chan_make(i64)",
		"call ptr @osty_rt_thread_chan_make(i64 4)",
		"declare void @osty_rt_thread_chan_close(ptr)",
		"call void @osty_rt_thread_chan_close(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestGenerateFromMIRTaskGroupAndSpawn(t *testing.T) {
	// fn launch(body: fn(Group) -> Int) -> Int { taskGroup(body) }
	groupT := &ir.NamedType{Name: "Group"}
	fnType := &ir.FnType{Params: []ir.Type{groupT}, Return: ir.TInt}
	fn := &ir.FnDecl{
		Name:   "launch",
		Return: ir.TInt,
		Params: []*ir.Param{{Name: "body", Type: fnType}},
		Body: &ir.Block{
			Result: &ir.CallExpr{
				Callee: &ir.Ident{
					Name: "taskGroup", Kind: ir.IdentFn,
					T: &ir.FnType{Params: []ir.Type{fnType}, Return: ir.TInt},
				},
				Args: []ir.Arg{
					{Value: &ir.Ident{Name: "body", Kind: ir.IdentParam, T: fnType}},
				},
				T: ir.TInt,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/task.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare i64 @osty_rt_task_group(ptr)",
		"call i64 @osty_rt_task_group(ptr ",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestGenerateFromMIRCancellationHelpers(t *testing.T) {
	useT := &ir.UseDecl{
		Path: []string{"std", "thread"}, RawPath: "std.thread", Alias: "thread",
	}
	fn := &ir.FnDecl{
		Name:   "check",
		Return: ir.TBool,
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "thread", T: &ir.NamedType{Name: "ThreadModule"}},
				Name:     "isCancelled",
				T:        ir.TBool,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{useT, fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/cancel.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare i1 @osty_rt_cancel_is_cancelled()",
		"call i1 @osty_rt_cancel_is_cancelled()",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

// ==== Stage 3.11: GC roots / safepoints (Options.EmitGC) ====

// TestGenerateFromMIREmitGCDefaultOff — With EmitGC off (the default),
// none of the osty.gc.* runtime symbols may appear even when the
// function owns a managed-ptr local. This keeps the pre-Stage-3.11
// MIR corpus byte-stable.
func TestGenerateFromMIREmitGCDefaultOff(t *testing.T) {
	// fn identity(s: String) -> String { s }
	fn := &ir.FnDecl{
		Name:   "identity",
		Return: ir.TString,
		Params: []*ir.Param{{Name: "s", Type: ir.TString}},
		Body: &ir.Block{
			Result: &ir.Ident{Name: "s", Kind: ir.IdentParam, T: ir.TString},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/gc-off.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, gcSym := range []string{
		"osty.gc.root_bind_v1",
		"osty.gc.root_release_v1",
		"osty.gc.safepoint_v1",
	} {
		if strings.Contains(got, gcSym) {
			t.Fatalf("unexpected GC symbol %q with EmitGC off:\n%s", gcSym, got)
		}
	}
}

// TestGenerateFromMIREmitGCManagedParam — A function with a
// managed-ptr param (String) should surface the param slot in the
// entry safepoint root array rather than function-long root_bind
// pinning.
func TestGenerateFromMIREmitGCManagedParam(t *testing.T) {
	// fn identity(s: String) -> String { s }
	fn := &ir.FnDecl{
		Name:   "identity",
		Return: ir.TString,
		Params: []*ir.Param{{Name: "s", Type: ir.TString}},
		Body: &ir.Block{
			Result: &ir.Ident{Name: "s", Kind: ir.IdentParam, T: ir.TString},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{
		PackageName: "main",
		SourcePath:  "/tmp/gc-param.osty",
		EmitGC:      true,
	})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare void @osty.gc.safepoint_v1(i64, ptr, i64)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	for _, forbidden := range []string{
		"osty.gc.root_bind_v1",
		"osty.gc.root_release_v1",
	} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("unexpected legacy root-binding symbol %q in:\n%s", forbidden, got)
		}
	}
	if !strings.Contains(got, "alloca ptr, i64 2") {
		t.Fatalf("expected entry safepoint array for return slot + param slot in:\n%s", got)
	}
	for _, want := range []string{
		"store ptr %l0, ptr %t1",
		"store ptr %p1, ptr %t2",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected safepoint root array to contain %q in:\n%s", want, got)
		}
	}
	if !regexp.MustCompile(`call void @osty\.gc\.safepoint_v1\(i64 72057594037927936, ptr %t\d+, i64 2\)`).MatchString(got) {
		t.Fatalf("expected entry safepoint to receive two explicit roots in:\n%s", got)
	}
}

// TestGenerateFromMIREmitGCNonParamLocalZeroInit — A non-param
// managed local must be null-initialised before the first safepoint so
// the GC never scans undef memory.
func TestGenerateFromMIREmitGCNonParamLocalZeroInit(t *testing.T) {
	// fn first(xs: List<Int>) -> List<Int> { let y = xs; y }
	listInt := &ir.NamedType{Name: "List", Args: []ir.Type{ir.TInt}, Builtin: true}
	fn := &ir.FnDecl{
		Name:   "first",
		Return: listInt,
		Params: []*ir.Param{{Name: "xs", Type: listInt}},
		Body: &ir.Block{
			Stmts: []ir.Stmt{
				&ir.LetStmt{
					Name:  "y",
					Type:  listInt,
					Value: &ir.Ident{Name: "xs", Kind: ir.IdentParam, T: listInt},
				},
			},
			Result: &ir.Ident{Name: "y", Kind: ir.IdentLocal, T: listInt},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{
		PackageName: "main",
		SourcePath:  "/tmp/gc-local.osty",
		EmitGC:      true,
	})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "store ptr null, ptr %l") {
		t.Fatalf("expected non-param local to be null-initialised in:\n%s", got)
	}
	// The null-init must happen before the first safepoint so the GC sees a
	// valid (null) pointer at every safepoint.
	zeroIdx := strings.Index(got, "store ptr null, ptr %l")
	safepointIdx := strings.Index(got, "call void @osty.gc.safepoint_v1(i64 72057594037927936")
	if zeroIdx < 0 || safepointIdx < 0 {
		t.Fatalf("zero-init / safepoint markers missing in:\n%s", got)
	}
	if !(zeroIdx < safepointIdx) {
		t.Fatalf("expected zero-init before the first safepoint in:\n%s", got)
	}
}

// TestGenerateFromMIREmitGCNoManagedLocals — A function with only
// value-typed locals still gets an entry safepoint. root_bind /
// root_release never fire because there are no managed slots.
func TestGenerateFromMIREmitGCNoManagedLocals(t *testing.T) {
	// fn answer() -> Int { 42 }
	fn := &ir.FnDecl{
		Name:   "answer",
		Return: ir.TInt,
		Body: &ir.Block{
			Result: &ir.IntLit{Text: "42", T: ir.TInt},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{
		PackageName: "main",
		SourcePath:  "/tmp/gc-noptr.osty",
		EmitGC:      true,
	})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "call void @osty.gc.safepoint_v1") {
		t.Fatalf("expected entry safepoint in:\n%s", got)
	}
	if strings.Contains(got, "osty.gc.root_bind_v1") {
		t.Fatalf("unexpected root_bind in pointer-free function:\n%s", got)
	}
	if strings.Contains(got, "osty.gc.root_release_v1") {
		t.Fatalf("unexpected root_release in pointer-free function:\n%s", got)
	}
}

func TestGenerateFromMIREmitGCChunksSafepointRoots(t *testing.T) {
	oldChunkSize := safepointRootChunkSize
	safepointRootChunkSize = 4
	defer func() { safepointRootChunkSize = oldChunkSize }()

	params := []*ir.Param{
		{Name: "a", Type: ir.TString},
		{Name: "b", Type: ir.TString},
		{Name: "c", Type: ir.TString},
		{Name: "d", Type: ir.TString},
		{Name: "e", Type: ir.TString},
		{Name: "f", Type: ir.TString},
		{Name: "g", Type: ir.TString},
	}
	fn := &ir.FnDecl{
		Name:   "head",
		Return: ir.TString,
		Params: params,
		Body: &ir.Block{
			Result: &ir.Ident{Name: "a", Kind: ir.IdentParam, T: ir.TString},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{
		PackageName: "main",
		SourcePath:  "/tmp/gc-chunks.osty",
		EmitGC:      true,
	})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	allocas := regexp.MustCompile(`alloca ptr, i64 (\d+)`).FindAllStringSubmatch(got, -1)
	if len(allocas) != 2 {
		t.Fatalf("expected two safepoint root allocas, got %d in:\n%s", len(allocas), got)
	}
	if allocas[0][1] != "4" || allocas[1][1] != "4" {
		t.Fatalf("expected safepoint root chunk sizes [4 4], got [%s %s] in:\n%s", allocas[0][1], allocas[1][1], got)
	}
	if gotCount := strings.Count(got, "call void @osty.gc.safepoint_v1(i64"); gotCount != 2 {
		t.Fatalf("expected two chunked safepoint calls, got %d in:\n%s", gotCount, got)
	}
}

func TestGenerateFromMIRReusesSafepointRootArrays(t *testing.T) {
	fn := &ir.FnDecl{
		Name:   "count",
		Return: ir.TString,
		Params: []*ir.Param{{Name: "s", Type: ir.TString}},
		Body: &ir.Block{
			Stmts: []ir.Stmt{
				&ir.LetStmt{
					Name:  "i",
					Type:  ir.TInt,
					Mut:   true,
					Value: &ir.IntLit{Text: "0", T: ir.TInt},
				},
				&ir.ForStmt{
					Kind: ir.ForWhile,
					Cond: &ir.BinaryExpr{
						Op:    ir.BinLt,
						Left:  &ir.Ident{Name: "i", Kind: ir.IdentLocal, T: ir.TInt},
						Right: &ir.IntLit{Text: "2", T: ir.TInt},
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
			Result: &ir.Ident{Name: "s", Kind: ir.IdentParam, T: ir.TString},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{
		PackageName: "main",
		SourcePath:  "/tmp/gc-root-array-reuse.osty",
		EmitGC:      true,
	})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	if gotCount := strings.Count(got, "alloca ptr, i64 2"); gotCount != 1 {
		t.Fatalf("expected one reused safepoint root array alloca, got %d in:\n%s", gotCount, got)
	}
	if gotCount := strings.Count(got, "call void @osty.gc.safepoint_v1(i64"); gotCount != 2 {
		t.Fatalf("expected entry + loop safepoints, got %d in:\n%s", gotCount, got)
	}
	callRe := regexp.MustCompile(`call void @osty\.gc\.safepoint_v1\(i64 \d+, ptr (%t\d+), i64 2\)`)
	matches := callRe.FindAllStringSubmatch(got, -1)
	if len(matches) != 2 {
		t.Fatalf("expected two safepoint calls sharing a root array pointer in:\n%s", got)
	}
	if matches[0][1] != matches[1][1] {
		t.Fatalf("expected safepoints to reuse the same root array pointer, got %q and %q in:\n%s", matches[0][1], matches[1][1], got)
	}
}

func TestGenerateFromMIRNullsManagedDeadLocalsBeforeLoopSafepoint(t *testing.T) {
	fn := &ir.FnDecl{
		Name:   "keep",
		Return: ir.TString,
		Params: []*ir.Param{{Name: "s", Type: ir.TString}},
		Body: &ir.Block{
			Stmts: []ir.Stmt{
				&ir.LetStmt{
					Name:  "i",
					Type:  ir.TInt,
					Mut:   true,
					Value: &ir.IntLit{Text: "0", T: ir.TInt},
				},
				&ir.ForStmt{
					Kind: ir.ForWhile,
					Cond: &ir.BinaryExpr{
						Op:    ir.BinLt,
						Left:  &ir.Ident{Name: "i", Kind: ir.IdentLocal, T: ir.TInt},
						Right: &ir.IntLit{Text: "1", T: ir.TInt},
						T:     ir.TBool,
					},
					Body: &ir.Block{
						Stmts: []ir.Stmt{
							&ir.Block{
								Stmts: []ir.Stmt{
									&ir.LetStmt{
										Name:  "t",
										Type:  ir.TString,
										Value: &ir.Ident{Name: "s", Kind: ir.IdentParam, T: ir.TString},
									},
								},
							},
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
			Result: &ir.Ident{Name: "s", Kind: ir.IdentParam, T: ir.TString},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{
		PackageName: "main",
		SourcePath:  "/tmp/gc-dead-local-null.osty",
		EmitGC:      true,
	})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	if gotCount := strings.Count(got, "call void @osty.gc.safepoint_v1(i64"); gotCount != 2 {
		t.Fatalf("expected entry + loop safepoints, got %d in:\n%s", gotCount, got)
	}
	nullMatches := regexp.MustCompile(`store ptr null, ptr (%l\d+)`).FindAllStringSubmatchIndex(got, -1)
	if len(nullMatches) < 3 {
		t.Fatalf("expected entry zero-init plus storage-dead nulling in:\n%s", got)
	}
	nullCounts := map[string]int{}
	nullPos := map[string][]int{}
	for _, match := range nullMatches {
		slot := got[match[2]:match[3]]
		nullCounts[slot]++
		nullPos[slot] = append(nullPos[slot], match[0])
	}
	var deadSlot string
	for slot, count := range nullCounts {
		if count == 2 {
			deadSlot = slot
			break
		}
	}
	if deadSlot == "" {
		t.Fatalf("expected one managed local slot to be nulled twice (entry + storage_dead) in:\n%s", got)
	}
	firstPoll := strings.Index(got, "call void @osty.gc.safepoint_v1(i64")
	lastPoll := strings.LastIndex(got, "call void @osty.gc.safepoint_v1(i64")
	if firstPoll < 0 || lastPoll <= firstPoll {
		t.Fatalf("expected distinct entry and loop safepoints in:\n%s", got)
	}
	if !(nullPos[deadSlot][1] > firstPoll && nullPos[deadSlot][1] < lastPoll) {
		t.Fatalf("expected storage-dead nulling for %s between entry and loop safepoints in:\n%s", deadSlot, got)
	}
}

// TestGenerateFromMIREmitGCLoopSafepoint — A while-loop lowers to a
// cond block whose branch terminator targets the loop body and the
// exit. The back-edge from the body to the cond block must carry a
// safepoint so cancellation / GC polls fire inside tight loops.
func TestGenerateFromMIREmitGCLoopSafepoint(t *testing.T) {
	// fn count() { let mut i = 0; while i < 10 { i = i + 1 } }
	hir := &ir.Module{
		Package: "main",
		Decls: []ir.Decl{
			&ir.FnDecl{
				Name:   "count",
				Return: ir.TUnit,
				Body: &ir.Block{
					Stmts: []ir.Stmt{
						&ir.LetStmt{
							Name:  "i",
							Type:  ir.TInt,
							Mut:   true,
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
	out, err := GenerateFromMIR(m, Options{
		PackageName: "main",
		SourcePath:  "/tmp/gc-loop.osty",
		EmitGC:      true,
	})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	// Entry + at least one back-edge safepoint = ≥2 calls.
	safepoints := strings.Count(got, "call void @osty.gc.safepoint_v1")
	if safepoints < 2 {
		t.Fatalf("expected ≥2 safepoints (entry + back-edge), got %d in:\n%s", safepoints, got)
	}
	// Entry safepoint encodes kind=ENTRY (1<<56) | serial=0, loop
	// back-edge encodes kind=LOOP (3<<56) | serial=1. See
	// encodeSafepointID + safepointKind* in generator.go.
	if !strings.Contains(got, "safepoint_v1(i64 72057594037927936,") {
		t.Fatalf("expected entry safepoint (kind=ENTRY, serial 0) in:\n%s", got)
	}
	if !strings.Contains(got, "safepoint_v1(i64 216172782113783809,") {
		t.Fatalf("expected loop-backedge safepoint (kind=LOOP, serial 1) in:\n%s", got)
	}
}

// TestGenerateFromMIRStringEquality — `fn eq() -> Bool { "a" == "b" }`
// must lower to osty_rt_strings_Equal rather than a raw `icmp eq ptr`
// (the latter would reduce to pointer identity, not content equality).
func TestGenerateFromMIRStringEquality(t *testing.T) {
	hir := &ir.Module{
		Package: "main",
		Decls: []ir.Decl{
			&ir.FnDecl{
				Name:   "eq",
				Return: ir.TBool,
				Body: &ir.Block{
					Result: &ir.BinaryExpr{
						Op:    ir.BinEq,
						Left:  &ir.StringLit{Parts: []ir.StringPart{{IsLit: true, Lit: "a"}}},
						Right: &ir.StringLit{Parts: []ir.StringPart{{IsLit: true, Lit: "b"}}},
						T:     ir.TBool,
					},
				},
			},
		},
	}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/streq.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare i1 @osty_rt_strings_Equal(ptr, ptr)",
		"call i1 @osty_rt_strings_Equal(ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "icmp eq ptr") {
		t.Fatalf("found raw icmp eq ptr (should route through runtime helper):\n%s", got)
	}
}

// TestGenerateFromMIRStringInequality — `!=` negates the runtime
// equality result via xor, preserving the content-compare semantics.
func TestGenerateFromMIRStringInequality(t *testing.T) {
	hir := &ir.Module{
		Package: "main",
		Decls: []ir.Decl{
			&ir.FnDecl{
				Name:   "neq",
				Return: ir.TBool,
				Body: &ir.Block{
					Result: &ir.BinaryExpr{
						Op:    ir.BinNeq,
						Left:  &ir.StringLit{Parts: []ir.StringPart{{IsLit: true, Lit: "a"}}},
						Right: &ir.StringLit{Parts: []ir.StringPart{{IsLit: true, Lit: "b"}}},
						T:     ir.TBool,
					},
				},
			},
		},
	}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/strneq.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"call i1 @osty_rt_strings_Equal(ptr",
		"xor i1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

// TestGenerateFromMIRStringOrdering — String `<` / `<=` / `>` / `>=`
// must lower to osty_rt_strings_Compare (i64 -1/0/+1) + `icmp <pred>
// i64 result, 0`. Without this routing the MIR path would emit a raw
// `icmp slt ptr` comparison, which reduces to pointer address ordering
// rather than content ordering.
func TestGenerateFromMIRStringOrdering(t *testing.T) {
	cases := []struct {
		name string
		op   ir.BinOp
		pred string
	}{
		{"lt", ir.BinLt, "icmp slt i64"},
		{"leq", ir.BinLeq, "icmp sle i64"},
		{"gt", ir.BinGt, "icmp sgt i64"},
		{"geq", ir.BinGeq, "icmp sge i64"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hir := &ir.Module{
				Package: "main",
				Decls: []ir.Decl{
					&ir.FnDecl{
						Name:   "cmp",
						Return: ir.TBool,
						Body: &ir.Block{
							Result: &ir.BinaryExpr{
								Op:    tc.op,
								Left:  &ir.StringLit{Parts: []ir.StringPart{{IsLit: true, Lit: "a"}}},
								Right: &ir.StringLit{Parts: []ir.StringPart{{IsLit: true, Lit: "b"}}},
								T:     ir.TBool,
							},
						},
					},
				},
			}
			m := buildMIRModuleFromHIR(t, hir)
			out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/strord.osty"})
			if err != nil {
				t.Fatalf("GenerateFromMIR: %v", err)
			}
			got := string(out)
			for _, want := range []string{
				"declare i64 @osty_rt_strings_Compare(ptr, ptr)",
				"call i64 @osty_rt_strings_Compare(ptr",
				tc.pred,
				", 0",
			} {
				if !strings.Contains(got, want) {
					t.Fatalf("missing %q in:\n%s", want, got)
				}
			}
			if strings.Contains(got, "icmp slt ptr") || strings.Contains(got, "icmp sle ptr") ||
				strings.Contains(got, "icmp sgt ptr") || strings.Contains(got, "icmp sge ptr") {
				t.Fatalf("found raw icmp on ptr (ordering must route through runtime):\n%s", got)
			}
		})
	}
}

// TestGenerateFromMIRSetRemove verifies Set.remove(item) dispatches
// through the runtime `osty_rt_set_remove_<kind>` symbols. The ABI
// mirrors set_insert — i1 return, typed by element LLVM kind.
func TestGenerateFromMIRSetRemove(t *testing.T) {
	setT := &ir.NamedType{Name: "Set", Args: []ir.Type{ir.TInt}, Builtin: true}
	fn := &ir.FnDecl{
		Name:   "drop",
		Return: ir.TBool,
		Params: []*ir.Param{
			{Name: "s", Type: setT},
			{Name: "v", Type: ir.TInt},
		},
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "s", Kind: ir.IdentParam, T: setT},
				Name:     "remove",
				Args: []ir.Arg{
					{Value: &ir.Ident{Name: "v", Kind: ir.IdentParam, T: ir.TInt}},
				},
				T: ir.TBool,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/set_remove.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare i1 @osty_rt_set_remove_i64(ptr, i64)",
		"call i1 @osty_rt_set_remove_i64(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

// TestGenerateFromMIRSetRemoveString verifies the string-key runtime
// suffix variant — Set<String>.remove routes through the string
// dispatch.
func TestGenerateFromMIRSetRemoveString(t *testing.T) {
	setT := &ir.NamedType{Name: "Set", Args: []ir.Type{ir.TString}, Builtin: true}
	fn := &ir.FnDecl{
		Name:   "drop",
		Return: ir.TBool,
		Params: []*ir.Param{
			{Name: "s", Type: setT},
			{Name: "v", Type: ir.TString},
		},
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "s", Kind: ir.IdentParam, T: setT},
				Name:     "remove",
				Args: []ir.Arg{
					{Value: &ir.Ident{Name: "v", Kind: ir.IdentParam, T: ir.TString}},
				},
				T: ir.TBool,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/set_remove_string.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare i1 @osty_rt_set_remove_string(ptr, ptr)",
		"call i1 @osty_rt_set_remove_string(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

// TestGenerateFromMIRBytesLen verifies Bytes.len() dispatches through
// `osty_rt_bytes_len(ptr) -> i64`. Bytes is a primitive, lowered to
// an opaque pointer at the LLVM level.
func TestGenerateFromMIRBytesLen(t *testing.T) {
	fn := &ir.FnDecl{
		Name:   "sz",
		Return: ir.TInt,
		Params: []*ir.Param{{Name: "b", Type: ir.TBytes}},
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "b", Kind: ir.IdentParam, T: ir.TBytes},
				Name:     "len",
				T:        ir.TInt,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/bytes_len.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare i64 @osty_rt_bytes_len(ptr)",
		"call i64 @osty_rt_bytes_len(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestGenerateFromMIRStringInterpolationConcat(t *testing.T) {
	fn := &ir.FnDecl{
		Name:   "qualify",
		Return: ir.TString,
		Params: []*ir.Param{
			{Name: "alias", Type: ir.TString},
			{Name: "name", Type: ir.TString},
		},
		Body: &ir.Block{
			Result: &ir.StringLit{
				Parts: []ir.StringPart{
					{Expr: &ir.Ident{Name: "alias", Kind: ir.IdentParam, T: ir.TString}},
					{IsLit: true, Lit: "."},
					{Expr: &ir.Ident{Name: "name", Kind: ir.IdentParam, T: ir.TString}},
				},
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/string_concat.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "declare ptr @osty_rt_strings_ConcatN(i64, ptr)") {
		t.Fatalf("missing string concat_n runtime decl in:\n%s", got)
	}
	if strings.Count(got, "call ptr @osty_rt_strings_ConcatN(") != 1 {
		t.Fatalf("expected one concat_n call in:\n%s", got)
	}
	if strings.Contains(got, "call ptr @osty_rt_strings_Concat(") {
		t.Fatalf("did not expect chained concat calls in:\n%s", got)
	}
}

func TestGenerateFromMIRStringInterpolationConcatBoxesScalars(t *testing.T) {
	fn := &ir.FnDecl{
		Name:   "render",
		Return: ir.TString,
		Params: []*ir.Param{
			{Name: "n", Type: ir.TInt},
			{Name: "ok", Type: ir.TBool},
		},
		Body: &ir.Block{
			Result: &ir.StringLit{
				Parts: []ir.StringPart{
					{Expr: &ir.Ident{Name: "n", Kind: ir.IdentParam, T: ir.TInt}},
					{IsLit: true, Lit: ":"},
					{Expr: &ir.Ident{Name: "ok", Kind: ir.IdentParam, T: ir.TBool}},
				},
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/string_concat_scalars.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare ptr @osty_rt_int_to_string(i64)",
		"declare ptr @osty_rt_bool_to_string(i1)",
		"call ptr @osty_rt_int_to_string(",
		"call ptr @osty_rt_bool_to_string(",
		"call ptr @osty_rt_strings_ConcatN(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "call ptr @osty_rt_strings_Concat(") {
		t.Fatalf("did not expect chained concat calls in:\n%s", got)
	}
}

// TestGenerateFromMIRStringInterpolationBoxesCharAndByte pins the
// Char (i32) and Byte (i8) dispatch in emitStringConcatBoxed so they
// call osty_rt_char_to_string / osty_rt_byte_to_string instead of
// being misrouted through osty_rt_int_to_string(i64) (which produces
// malformed LLVM IR on width mismatch).
func TestGenerateFromMIRStringInterpolationBoxesCharAndByte(t *testing.T) {
	fn := &ir.FnDecl{
		Name:   "render",
		Return: ir.TString,
		Params: []*ir.Param{
			{Name: "c", Type: ir.TChar},
			{Name: "b", Type: ir.TByte},
		},
		Body: &ir.Block{
			Result: &ir.StringLit{
				Parts: []ir.StringPart{
					{Expr: &ir.Ident{Name: "c", Kind: ir.IdentParam, T: ir.TChar}},
					{IsLit: true, Lit: "="},
					{Expr: &ir.Ident{Name: "b", Kind: ir.IdentParam, T: ir.TByte}},
				},
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/char_byte_concat.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare ptr @osty_rt_char_to_string(i32)",
		"declare ptr @osty_rt_byte_to_string(i8)",
		"call ptr @osty_rt_char_to_string(",
		"call ptr @osty_rt_byte_to_string(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "@osty_rt_int_to_string") {
		t.Fatalf("Char/Byte should not route through osty_rt_int_to_string:\n%s", got)
	}
}

func TestGenerateFromMIRBinaryStringAddUsesRuntimeConcat(t *testing.T) {
	fn := &ir.FnDecl{
		Name:   "greet",
		Return: ir.TString,
		Params: []*ir.Param{{Name: "name", Type: ir.TString}},
		Body: &ir.Block{
			Result: &ir.BinaryExpr{
				Op:    ir.BinAdd,
				Left:  &ir.StringLit{Parts: []ir.StringPart{{IsLit: true, Lit: "hi "}}},
				Right: &ir.Ident{Name: "name", Kind: ir.IdentParam, T: ir.TString},
				T:     ir.TString,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/string_add.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare ptr @osty_rt_strings_Concat(ptr, ptr)",
		"call ptr @osty_rt_strings_Concat(ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "add ptr") {
		t.Fatalf("string add lowered to raw ptr add:\n%s", got)
	}
}

func TestGenerateFromMIRStringInterpolationRecoversFieldExprTypes(t *testing.T) {
	diagT := &ir.NamedType{Name: "Diag"}
	fn := &ir.FnDecl{
		Name:   "render",
		Return: ir.TString,
		Params: []*ir.Param{{Name: "d", Type: diagT}},
		Body: &ir.Block{
			Result: &ir.StringLit{
				Parts: []ir.StringPart{
					{Expr: &ir.FieldExpr{
						X:    &ir.Ident{Name: "d", Kind: ir.IdentParam, T: diagT},
						Name: "code",
						T:    ir.ErrTypeVal,
					}},
					{IsLit: true, Lit: ": "},
					{Expr: &ir.FieldExpr{
						X:    &ir.Ident{Name: "d", Kind: ir.IdentParam, T: diagT},
						Name: "message",
						T:    ir.ErrTypeVal,
					}},
				},
			},
		},
	}
	hir := &ir.Module{
		Package: "main",
		Decls: []ir.Decl{
			&ir.StructDecl{
				Name: "Diag",
				Fields: []*ir.Field{
					{Name: "code", Type: ir.TString, Exported: true},
					{Name: "message", Type: ir.TString, Exported: true},
				},
			},
			fn,
		},
	}
	m := buildMIRModuleFromHIR(t, hir)
	mirFn := m.LookupFunction("render")
	if mirFn == nil {
		t.Fatalf("missing render function")
	}
	for _, loc := range mirFn.Locals {
		if loc == nil {
			continue
		}
		if _, ok := loc.Type.(*ir.ErrType); ok {
			t.Fatalf("unexpected poisoned local _%d in MIR:\n%s", loc.ID, mir.PrintFunction(mirFn))
		}
	}
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/string_field_concat.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "declare ptr @osty_rt_strings_ConcatN(i64, ptr)") {
		t.Fatalf("missing concat_n decl in:\n%s", got)
	}
	if strings.Count(got, "call ptr @osty_rt_strings_ConcatN(") != 1 {
		t.Fatalf("expected one concat_n call in:\n%s", got)
	}
	if strings.Contains(got, "call ptr @osty_rt_strings_Concat(") {
		t.Fatalf("did not expect chained concat calls in:\n%s", got)
	}
}

// TestGenerateFromMIRStringChars verifies String.chars() dispatches
// through `osty_rt_strings_Chars(ptr) -> ptr` — matching the legacy
// emitter's routing so `.chars()` participates in MIR-direct object /
// binary emission instead of forcing fallback on `mir-backend`.
func TestGenerateFromMIRStringChars(t *testing.T) {
	fn := &ir.FnDecl{
		Name:   "toChars",
		Return: &ir.NamedType{Name: "List", Args: []ir.Type{ir.TChar}, Builtin: true},
		Params: []*ir.Param{{Name: "s", Type: ir.TString}},
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "s", Kind: ir.IdentParam, T: ir.TString},
				Name:     "chars",
				T:        &ir.NamedType{Name: "List", Args: []ir.Type{ir.TChar}, Builtin: true},
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/string_chars.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare ptr @osty_rt_strings_Chars(ptr)",
		"call ptr @osty_rt_strings_Chars(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

// TestGenerateFromMIRStringBytes — same as StringChars but for the
// bytes family; the runtime symbol is `osty_rt_strings_Bytes`.
func TestGenerateFromMIRStringBytes(t *testing.T) {
	fn := &ir.FnDecl{
		Name:   "toBytes",
		Return: &ir.NamedType{Name: "List", Args: []ir.Type{ir.TByte}, Builtin: true},
		Params: []*ir.Param{{Name: "s", Type: ir.TString}},
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "s", Kind: ir.IdentParam, T: ir.TString},
				Name:     "bytes",
				T:        &ir.NamedType{Name: "List", Args: []ir.Type{ir.TByte}, Builtin: true},
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/string_bytes.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare ptr @osty_rt_strings_Bytes(ptr)",
		"call ptr @osty_rt_strings_Bytes(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

// TestGenerateFromMIRStringLen verifies String.len() dispatches
// through `osty_rt_strings_ByteLen(ptr) -> i64` (matching
// `llvmStringRuntimeByteLenSymbol()` the legacy emitter already uses).
func TestGenerateFromMIRStringLen(t *testing.T) {
	fn := &ir.FnDecl{
		Name:   "sz",
		Return: ir.TInt,
		Params: []*ir.Param{{Name: "s", Type: ir.TString}},
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "s", Kind: ir.IdentParam, T: ir.TString},
				Name:     "len",
				T:        ir.TInt,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/string_len.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare i64 @osty_rt_strings_ByteLen(ptr)",
		"call i64 @osty_rt_strings_ByteLen(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

// TestGenerateFromMIRStringIsEmpty — `.isEmpty()` composes
// `byte_len == 0` instead of a dedicated runtime symbol, matching the
// legacy emitter in expr.go. That gives two artifacts the test can
// lock in: the `ByteLen` call and an `icmp eq i64 %..., 0`.
func TestGenerateFromMIRStringIsEmpty(t *testing.T) {
	fn := &ir.FnDecl{
		Name:   "blank",
		Return: ir.TBool,
		Params: []*ir.Param{{Name: "s", Type: ir.TString}},
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "s", Kind: ir.IdentParam, T: ir.TString},
				Name:     "isEmpty",
				T:        ir.TBool,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/string_empty.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare i64 @osty_rt_strings_ByteLen(ptr)",
		"call i64 @osty_rt_strings_ByteLen(",
		"icmp eq i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

// TestGenerateFromMIRStringToUpper verifies String.toUpper()
// dispatches through `osty_rt_strings_ToUpper(ptr) -> ptr`.
func TestGenerateFromMIRStringToUpper(t *testing.T) {
	fn := &ir.FnDecl{
		Name:   "loud",
		Return: ir.TString,
		Params: []*ir.Param{{Name: "s", Type: ir.TString}},
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "s", Kind: ir.IdentParam, T: ir.TString},
				Name:     "toUpper",
				T:        ir.TString,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/string_to_upper.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare ptr @osty_rt_strings_ToUpper(ptr)",
		"call ptr @osty_rt_strings_ToUpper(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

// TestGenerateFromMIRStringToLower verifies String.toLower()
// dispatches through `osty_rt_strings_ToLower(ptr) -> ptr`.
func TestGenerateFromMIRStringToLower(t *testing.T) {
	fn := &ir.FnDecl{
		Name:   "soft",
		Return: ir.TString,
		Params: []*ir.Param{{Name: "s", Type: ir.TString}},
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "s", Kind: ir.IdentParam, T: ir.TString},
				Name:     "toLower",
				T:        ir.TString,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/string_to_lower.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare ptr @osty_rt_strings_ToLower(ptr)",
		"call ptr @osty_rt_strings_ToLower(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestGenerateFromMIRStringToInt(t *testing.T) {
	resultT := &ir.NamedType{Name: "Result", Args: []ir.Type{ir.TInt, ir.TString}}
	fn := &ir.FnDecl{
		Name:   "parseInt",
		Return: resultT,
		Params: []*ir.Param{{Name: "s", Type: ir.TString}},
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "s", Kind: ir.IdentParam, T: ir.TString},
				Name:     "toInt",
				T:        resultT,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/string_to_int.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare i1 @osty_rt_strings_IsValidInt(ptr)",
		"declare i64 @osty_rt_strings_ToInt(ptr)",
		"call i64 @osty_rt_strings_ToInt(",
		"insertvalue %Result.i64.string undef, i64 1, 0",
		"insertvalue %Result.i64.string undef, i64 0, 0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestGenerateFromMIRStringToFloat(t *testing.T) {
	resultT := &ir.NamedType{Name: "Result", Args: []ir.Type{ir.TFloat, ir.TString}}
	fn := &ir.FnDecl{
		Name:   "parseFloat",
		Return: resultT,
		Params: []*ir.Param{{Name: "s", Type: ir.TString}},
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "s", Kind: ir.IdentParam, T: ir.TString},
				Name:     "toFloat",
				T:        resultT,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/string_to_float.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare i1 @osty_rt_strings_IsValidFloat(ptr)",
		"declare double @osty_rt_strings_ToFloat(ptr)",
		"call double @osty_rt_strings_ToFloat(",
		"insertvalue %Result.f64.string undef, i64 1, 0",
		"insertvalue %Result.f64.string undef, i64 0, 0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

// TestGenerateFromMIRBytesIsEmpty verifies Bytes.isEmpty() dispatches
// through `osty_rt_bytes_is_empty(ptr) -> i1`.
func TestGenerateFromMIRBytesIsEmpty(t *testing.T) {
	fn := &ir.FnDecl{
		Name:   "blank",
		Return: ir.TBool,
		Params: []*ir.Param{{Name: "b", Type: ir.TBytes}},
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "b", Kind: ir.IdentParam, T: ir.TBytes},
				Name:     "isEmpty",
				T:        ir.TBool,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/bytes_empty.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare i1 @osty_rt_bytes_is_empty(ptr)",
		"call i1 @osty_rt_bytes_is_empty(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

// TestGenerateFromMIRBytesGet verifies Bytes.get() lowers through the
// bytes runtime helpers and rebuilds `Byte?` as the standard optional
// aggregate shape.
func TestGenerateFromMIRBytesGet(t *testing.T) {
	optByte := &ir.OptionalType{Inner: ir.TByte}
	fn := &ir.FnDecl{
		Name:   "second",
		Return: optByte,
		Params: []*ir.Param{{Name: "b", Type: ir.TBytes}},
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "b", Kind: ir.IdentParam, T: ir.TBytes},
				Name:     "get",
				Args:     []ir.Arg{{Value: &ir.IntLit{Text: "1", T: ir.TInt}}},
				T:        optByte,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/bytes_get.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare i64 @osty_rt_bytes_len(ptr)",
		"declare i8 @osty_rt_bytes_get(ptr, i64)",
		"call i8 @osty_rt_bytes_get(",
		"%Option.i8 = type { i64, i64 }",
		"phi %Option.i8",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

// TestGenerateFromMIRBytesContains verifies Bytes.contains() lowers
// through `osty_rt_bytes_index_of(ptr, ptr) -> i64` plus a `>= 0`
// comparison to produce Bool.
func TestGenerateFromMIRBytesContains(t *testing.T) {
	fn := &ir.FnDecl{
		Name:   "has",
		Return: ir.TBool,
		Params: []*ir.Param{
			{Name: "a", Type: ir.TBytes},
			{Name: "b", Type: ir.TBytes},
		},
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "a", Kind: ir.IdentParam, T: ir.TBytes},
				Name:     "contains",
				Args:     []ir.Arg{{Value: &ir.Ident{Name: "b", Kind: ir.IdentParam, T: ir.TBytes}}},
				T:        ir.TBool,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/bytes_contains.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare i64 @osty_rt_bytes_index_of(ptr, ptr)",
		"call i64 @osty_rt_bytes_index_of(",
		"icmp sge i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

// TestGenerateFromMIRBytesStartsWith verifies Bytes.startsWith()
// lowers through `osty_rt_bytes_index_of(ptr, ptr) -> i64` plus an
// `== 0` comparison to produce Bool.
func TestGenerateFromMIRBytesStartsWith(t *testing.T) {
	fn := &ir.FnDecl{
		Name:   "hasPrefix",
		Return: ir.TBool,
		Params: []*ir.Param{
			{Name: "a", Type: ir.TBytes},
			{Name: "b", Type: ir.TBytes},
		},
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "a", Kind: ir.IdentParam, T: ir.TBytes},
				Name:     "startsWith",
				Args:     []ir.Arg{{Value: &ir.Ident{Name: "b", Kind: ir.IdentParam, T: ir.TBytes}}},
				T:        ir.TBool,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/bytes_starts_with.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare i64 @osty_rt_bytes_index_of(ptr, ptr)",
		"call i64 @osty_rt_bytes_index_of(",
		"icmp eq i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

// TestGenerateFromMIRBytesEndsWith verifies Bytes.endsWith() lowers
// through `osty_rt_bytes_last_index_of(ptr, ptr) -> i64` plus
// length-aware suffix comparison.
func TestGenerateFromMIRBytesEndsWith(t *testing.T) {
	fn := &ir.FnDecl{
		Name:   "hasSuffix",
		Return: ir.TBool,
		Params: []*ir.Param{
			{Name: "a", Type: ir.TBytes},
			{Name: "b", Type: ir.TBytes},
		},
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "a", Kind: ir.IdentParam, T: ir.TBytes},
				Name:     "endsWith",
				Args:     []ir.Arg{{Value: &ir.Ident{Name: "b", Kind: ir.IdentParam, T: ir.TBytes}}},
				T:        ir.TBool,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/bytes_ends_with.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare i64 @osty_rt_bytes_last_index_of(ptr, ptr)",
		"declare i64 @osty_rt_bytes_len(ptr)",
		"call i64 @osty_rt_bytes_last_index_of(",
		"icmp eq i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

// TestGenerateFromMIRBytesIndexOf verifies Bytes.indexOf() lowers
// through `osty_rt_bytes_index_of(ptr, ptr) -> i64` and rebuilds the
// standard `Int?` aggregate.
func TestGenerateFromMIRBytesIndexOf(t *testing.T) {
	optInt := &ir.OptionalType{Inner: ir.TInt}
	fn := &ir.FnDecl{
		Name:   "find",
		Return: optInt,
		Params: []*ir.Param{
			{Name: "a", Type: ir.TBytes},
			{Name: "b", Type: ir.TBytes},
		},
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "a", Kind: ir.IdentParam, T: ir.TBytes},
				Name:     "indexOf",
				Args:     []ir.Arg{{Value: &ir.Ident{Name: "b", Kind: ir.IdentParam, T: ir.TBytes}}},
				T:        optInt,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/bytes_index_of.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare i64 @osty_rt_bytes_index_of(ptr, ptr)",
		"call i64 @osty_rt_bytes_index_of(",
		"%Option.i64 = type { i64, i64 }",
		"phi %Option.i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

// TestGenerateFromMIRBytesLastIndexOf verifies Bytes.lastIndexOf()
// lowers through `osty_rt_bytes_last_index_of(ptr, ptr) -> i64` and
// rebuilds the standard `Int?` aggregate.
func TestGenerateFromMIRBytesLastIndexOf(t *testing.T) {
	optInt := &ir.OptionalType{Inner: ir.TInt}
	fn := &ir.FnDecl{
		Name:   "findLast",
		Return: optInt,
		Params: []*ir.Param{
			{Name: "a", Type: ir.TBytes},
			{Name: "b", Type: ir.TBytes},
		},
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "a", Kind: ir.IdentParam, T: ir.TBytes},
				Name:     "lastIndexOf",
				Args:     []ir.Arg{{Value: &ir.Ident{Name: "b", Kind: ir.IdentParam, T: ir.TBytes}}},
				T:        optInt,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/bytes_last_index_of.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare i64 @osty_rt_bytes_last_index_of(ptr, ptr)",
		"call i64 @osty_rt_bytes_last_index_of(",
		"%Option.i64 = type { i64, i64 }",
		"phi %Option.i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestGenerateFromMIRBytesSplit(t *testing.T) {
	listBytes := &ir.NamedType{Name: "List", Args: []ir.Type{ir.TBytes}, Builtin: true}
	fn := &ir.FnDecl{
		Name:   "parts",
		Return: listBytes,
		Params: []*ir.Param{
			{Name: "a", Type: ir.TBytes},
			{Name: "sep", Type: ir.TBytes},
		},
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "a", Kind: ir.IdentParam, T: ir.TBytes},
				Name:     "split",
				Args:     []ir.Arg{{Value: &ir.Ident{Name: "sep", Kind: ir.IdentParam, T: ir.TBytes}}},
				T:        listBytes,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/bytes_split.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare ptr @osty_rt_bytes_split(ptr, ptr)",
		"call ptr @osty_rt_bytes_split(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestGenerateFromMIRBytesJoin(t *testing.T) {
	listBytes := &ir.NamedType{Name: "List", Args: []ir.Type{ir.TBytes}, Builtin: true}
	fn := &ir.FnDecl{
		Name:   "joinAll",
		Return: ir.TBytes,
		Params: []*ir.Param{
			{Name: "sep", Type: ir.TBytes},
			{Name: "parts", Type: listBytes},
		},
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "sep", Kind: ir.IdentParam, T: ir.TBytes},
				Name:     "join",
				Args:     []ir.Arg{{Value: &ir.Ident{Name: "parts", Kind: ir.IdentParam, T: listBytes}}},
				T:        ir.TBytes,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/bytes_join.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare ptr @osty_rt_bytes_join(ptr, ptr)",
		"call ptr @osty_rt_bytes_join(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

// TestGenerateFromMIRBytesConcat verifies Bytes.concat() dispatches
// through `osty_rt_bytes_concat(ptr, ptr) -> ptr`.
func TestGenerateFromMIRBytesConcat(t *testing.T) {
	fn := &ir.FnDecl{
		Name:   "join",
		Return: ir.TBytes,
		Params: []*ir.Param{
			{Name: "a", Type: ir.TBytes},
			{Name: "b", Type: ir.TBytes},
		},
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "a", Kind: ir.IdentParam, T: ir.TBytes},
				Name:     "concat",
				Args:     []ir.Arg{{Value: &ir.Ident{Name: "b", Kind: ir.IdentParam, T: ir.TBytes}}},
				T:        ir.TBytes,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/bytes_concat.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare ptr @osty_rt_bytes_concat(ptr, ptr)",
		"call ptr @osty_rt_bytes_concat(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

// TestGenerateFromMIRBytesSlice verifies Bytes.slice() dispatches
// through `osty_rt_bytes_slice(ptr, i64, i64) -> ptr`.
func TestGenerateFromMIRBytesSlice(t *testing.T) {
	fn := &ir.FnDecl{
		Name:   "window",
		Return: ir.TBytes,
		Params: []*ir.Param{
			{Name: "a", Type: ir.TBytes},
		},
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "a", Kind: ir.IdentParam, T: ir.TBytes},
				Name:     "slice",
				Args: []ir.Arg{
					{Value: &ir.IntLit{Text: "1", T: ir.TInt}},
					{Value: &ir.IntLit{Text: "3", T: ir.TInt}},
				},
				T: ir.TBytes,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/bytes_slice.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare ptr @osty_rt_bytes_slice(ptr, i64, i64)",
		"call ptr @osty_rt_bytes_slice(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

// TestGenerateFromMIRBytesRepeat verifies Bytes.repeat() dispatches
// through `osty_rt_bytes_repeat(ptr, i64) -> ptr`.
func TestGenerateFromMIRBytesRepeat(t *testing.T) {
	fn := &ir.FnDecl{
		Name:   "echo",
		Return: ir.TBytes,
		Params: []*ir.Param{
			{Name: "a", Type: ir.TBytes},
		},
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "a", Kind: ir.IdentParam, T: ir.TBytes},
				Name:     "repeat",
				Args:     []ir.Arg{{Value: &ir.IntLit{Text: "3", T: ir.TInt}}},
				T:        ir.TBytes,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/bytes_repeat.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare ptr @osty_rt_bytes_repeat(ptr, i64)",
		"call ptr @osty_rt_bytes_repeat(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestGenerateFromMIRBytesReplace(t *testing.T) {
	fn := &ir.FnDecl{
		Name:   "swapOne",
		Return: ir.TBytes,
		Params: []*ir.Param{
			{Name: "a", Type: ir.TBytes},
			{Name: "old", Type: ir.TBytes},
			{Name: "new", Type: ir.TBytes},
		},
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "a", Kind: ir.IdentParam, T: ir.TBytes},
				Name:     "replace",
				Args: []ir.Arg{
					{Value: &ir.Ident{Name: "old", Kind: ir.IdentParam, T: ir.TBytes}},
					{Value: &ir.Ident{Name: "new", Kind: ir.IdentParam, T: ir.TBytes}},
				},
				T: ir.TBytes,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/bytes_replace.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare ptr @osty_rt_bytes_replace(ptr, ptr, ptr)",
		"call ptr @osty_rt_bytes_replace(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestGenerateFromMIRBytesReplaceAll(t *testing.T) {
	fn := &ir.FnDecl{
		Name:   "swapAll",
		Return: ir.TBytes,
		Params: []*ir.Param{
			{Name: "a", Type: ir.TBytes},
			{Name: "old", Type: ir.TBytes},
			{Name: "new", Type: ir.TBytes},
		},
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "a", Kind: ir.IdentParam, T: ir.TBytes},
				Name:     "replaceAll",
				Args: []ir.Arg{
					{Value: &ir.Ident{Name: "old", Kind: ir.IdentParam, T: ir.TBytes}},
					{Value: &ir.Ident{Name: "new", Kind: ir.IdentParam, T: ir.TBytes}},
				},
				T: ir.TBytes,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/bytes_replace_all.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare ptr @osty_rt_bytes_replace_all(ptr, ptr, ptr)",
		"call ptr @osty_rt_bytes_replace_all(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestGenerateFromMIRBytesTrimLeft(t *testing.T) {
	fn := &ir.FnDecl{
		Name:   "trimHead",
		Return: ir.TBytes,
		Params: []*ir.Param{
			{Name: "a", Type: ir.TBytes},
			{Name: "cutset", Type: ir.TBytes},
		},
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "a", Kind: ir.IdentParam, T: ir.TBytes},
				Name:     "trimLeft",
				Args:     []ir.Arg{{Value: &ir.Ident{Name: "cutset", Kind: ir.IdentParam, T: ir.TBytes}}},
				T:        ir.TBytes,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/bytes_trim_left.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare ptr @osty_rt_bytes_trim_left(ptr, ptr)",
		"call ptr @osty_rt_bytes_trim_left(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestGenerateFromMIRBytesTrimRight(t *testing.T) {
	fn := &ir.FnDecl{
		Name:   "trimTail",
		Return: ir.TBytes,
		Params: []*ir.Param{
			{Name: "a", Type: ir.TBytes},
			{Name: "cutset", Type: ir.TBytes},
		},
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "a", Kind: ir.IdentParam, T: ir.TBytes},
				Name:     "trimRight",
				Args:     []ir.Arg{{Value: &ir.Ident{Name: "cutset", Kind: ir.IdentParam, T: ir.TBytes}}},
				T:        ir.TBytes,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/bytes_trim_right.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare ptr @osty_rt_bytes_trim_right(ptr, ptr)",
		"call ptr @osty_rt_bytes_trim_right(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestGenerateFromMIRBytesTrim(t *testing.T) {
	fn := &ir.FnDecl{
		Name:   "trimBoth",
		Return: ir.TBytes,
		Params: []*ir.Param{
			{Name: "a", Type: ir.TBytes},
			{Name: "cutset", Type: ir.TBytes},
		},
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "a", Kind: ir.IdentParam, T: ir.TBytes},
				Name:     "trim",
				Args:     []ir.Arg{{Value: &ir.Ident{Name: "cutset", Kind: ir.IdentParam, T: ir.TBytes}}},
				T:        ir.TBytes,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/bytes_trim.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare ptr @osty_rt_bytes_trim(ptr, ptr)",
		"call ptr @osty_rt_bytes_trim(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestGenerateFromMIRBytesTrimSpace(t *testing.T) {
	fn := &ir.FnDecl{
		Name:   "trimWs",
		Return: ir.TBytes,
		Params: []*ir.Param{
			{Name: "a", Type: ir.TBytes},
		},
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "a", Kind: ir.IdentParam, T: ir.TBytes},
				Name:     "trimSpace",
				T:        ir.TBytes,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/bytes_trim_space.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare ptr @osty_rt_bytes_trim_space(ptr)",
		"call ptr @osty_rt_bytes_trim_space(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestGenerateFromMIRBytesToUpper(t *testing.T) {
	fn := &ir.FnDecl{
		Name:   "loud",
		Return: ir.TBytes,
		Params: []*ir.Param{
			{Name: "a", Type: ir.TBytes},
		},
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "a", Kind: ir.IdentParam, T: ir.TBytes},
				Name:     "toUpper",
				T:        ir.TBytes,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/bytes_to_upper.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare ptr @osty_rt_bytes_to_upper(ptr)",
		"call ptr @osty_rt_bytes_to_upper(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestGenerateFromMIRBytesToLower(t *testing.T) {
	fn := &ir.FnDecl{
		Name:   "soft",
		Return: ir.TBytes,
		Params: []*ir.Param{
			{Name: "a", Type: ir.TBytes},
		},
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "a", Kind: ir.IdentParam, T: ir.TBytes},
				Name:     "toLower",
				T:        ir.TBytes,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/bytes_to_lower.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare ptr @osty_rt_bytes_to_lower(ptr)",
		"call ptr @osty_rt_bytes_to_lower(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestGenerateFromMIRBytesToHex(t *testing.T) {
	fn := &ir.FnDecl{
		Name:   "render",
		Return: ir.TString,
		Params: []*ir.Param{
			{Name: "a", Type: ir.TBytes},
		},
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "a", Kind: ir.IdentParam, T: ir.TBytes},
				Name:     "toHex",
				T:        ir.TString,
			},
		},
	}
	hir := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	m := buildMIRModuleFromHIR(t, hir)
	out, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/bytes_to_hex.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare ptr @osty_rt_bytes_to_hex(ptr)",
		"call ptr @osty_rt_bytes_to_hex(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}
