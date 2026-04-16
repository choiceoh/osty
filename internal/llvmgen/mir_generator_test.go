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

// TestGenerateFromMIRUnsupportedFallsBack — building a module with a
// struct should make the MIR emitter return an ErrUnsupported error
// so the backend dispatcher falls back to the legacy path.
func TestGenerateFromMIRUnsupportedFallsBack(t *testing.T) {
	pointT := &ir.NamedType{Name: "Point"}
	hir := &ir.Module{
		Package: "main",
		Decls: []ir.Decl{
			&ir.StructDecl{
				Name: "Point",
				Fields: []*ir.Field{
					{Name: "x", Type: ir.TInt, Exported: true},
				},
			},
			&ir.FnDecl{
				Name:   "make",
				Return: pointT,
				Body: &ir.Block{
					Result: &ir.StructLit{
						TypeName: "Point",
						Fields: []ir.StructLitField{
							{Name: "x", Value: &ir.IntLit{Text: "1", T: ir.TInt}},
						},
						T: pointT,
					},
				},
			},
		},
	}
	m := buildMIRModuleFromHIR(t, hir)
	_, err := GenerateFromMIR(m, Options{PackageName: "main", SourcePath: "/tmp/struct.osty"})
	if err == nil {
		t.Fatalf("expected ErrUnsupported for struct; got nil")
	}
	// Must wrap ErrUnsupported so the backend dispatcher can distinguish
	// "this shape isn't in the MVP yet" from internal bugs.
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
// refuses a program (struct types aren't in the MVP), the backend
// dispatcher (via Options.UseMIR and the opts wiring in
// internal/backend/llvm.go) can catch ErrUnsupported and retry on
// the HIR path. We validate the sentinel semantics directly here —
// the end-to-end dispatch wiring is covered by the backend tests in
// internal/backend.
func TestMIRDualEmitGracefulFallback(t *testing.T) {
	src := `struct Point {
    x: Int,
    y: Int,
}

fn origin() -> Point {
    Point { x: 0, y: 0 }
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
	hirMod, _ := ir.Lower("main", file, res, chk)
	monoMod, _ := ir.Monomorphize(hirMod)
	mirMod := mir.Lower(monoMod)

	_, err := GenerateFromMIR(mirMod, Options{PackageName: "main", SourcePath: "/tmp/fallback.osty"})
	if err == nil {
		t.Fatalf("expected MIR emitter to refuse struct-bearing program")
	}
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported, got %T: %v", err, err)
	}

	// The HIR path must still accept it.
	out, hirErr := GenerateModule(monoMod, Options{PackageName: "main", SourcePath: "/tmp/fallback.osty"})
	if hirErr != nil {
		t.Fatalf("HIR path rejected a program the legacy emitter should accept: %v", hirErr)
	}
	if !strings.Contains(string(out), "define ") {
		t.Fatalf("HIR path produced no define line:\n%s", string(out))
	}
}
