package stage0

import (
	"errors"
	"strings"
	"testing"

	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/llvmabi"
	"github.com/osty/osty/internal/mir"
)

func trivialMainFn() *mir.Function {
	return &mir.Function{
		Name:        "main",
		ReturnType:  ir.TUnit,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: ir.TUnit, IsReturn: true},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{
			{ID: 0, Term: &mir.ReturnTerm{}},
		},
	}
}

func intLiteralFn(name string, value int64) *mir.Function {
	return &mir.Function{
		Name:        name,
		ReturnType:  ir.TInt,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: ir.TInt, IsReturn: true},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{
			{
				ID: 0,
				Instrs: []mir.Instr{
					&mir.AssignInstr{
						Dest: mir.Place{Local: 0},
						Src: &mir.UseRV{
							Op: &mir.ConstOp{
								Const: &mir.IntConst{Value: value, T: ir.TInt},
								T:     ir.TInt,
							},
						},
					},
				},
				Term: &mir.ReturnTerm{},
			},
		},
	}
}

// intParamPassthroughFn builds the canonical
// `fn name(<paramName>: Int) -> Int { <paramName> }` MIR shape: one
// block, one AssignInstr that copies the param local to the return
// local.
func intParamPassthroughFn(name, paramName string) *mir.Function {
	const paramID mir.LocalID = 1
	return &mir.Function{
		Name:        name,
		Params:      []mir.LocalID{paramID},
		ReturnType:  ir.TInt,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: ir.TInt, IsReturn: true},
			{ID: paramID, Name: paramName, Type: ir.TInt, IsParam: true},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{
			{
				ID: 0,
				Instrs: []mir.Instr{
					&mir.AssignInstr{
						Dest: mir.Place{Local: 0},
						Src: &mir.UseRV{
							Op: &mir.CopyOp{Place: mir.Place{Local: paramID}, T: ir.TInt},
						},
					},
				},
				Term: &mir.ReturnTerm{},
			},
		},
	}
}

func moduleWith(fns ...*mir.Function) *mir.Module {
	return &mir.Module{
		Package:   "main",
		Functions: fns,
		Layouts:   mir.NewLayoutTable(),
	}
}

// ---- P1: trivial main ----

func TestStage0EmitsTrivialMain(t *testing.T) {
	t.Parallel()
	got, err := EmitMIR(moduleWith(trivialMainFn()), llvmabi.Options{PackageName: "main", Target: "x86_64-unknown-linux-gnu"})
	if err != nil {
		t.Fatalf("EmitMIR: %v", err)
	}
	s := string(got)
	for _, want := range []string{
		"stage0 bootstrap LLVM IR",
		"; package: main",
		"target triple = \"x86_64-unknown-linux-gnu\"",
		"define i32 @main()",
		"ret i32 0",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, s)
		}
	}
}

func TestStage0RejectsNilModule(t *testing.T) {
	t.Parallel()
	_, err := EmitMIR(nil, llvmabi.Options{})
	if err == nil {
		t.Fatal("expected error for nil module")
	}
}

func TestStage0RejectsModuleWithoutMain(t *testing.T) {
	t.Parallel()
	_, err := EmitMIR(moduleWith(intLiteralFn("zero", 0)), llvmabi.Options{})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want wrapped ErrUnsupported", err)
	}
	if !strings.Contains(err.Error(), "module has no `main` function") {
		t.Fatalf("err = %q, want missing-main reason", err.Error())
	}
}

func TestStage0RejectsMainWithParameters(t *testing.T) {
	t.Parallel()
	module := moduleWith(trivialMainFn())
	module.Functions[0].Params = []mir.LocalID{0}
	_, err := EmitMIR(module, llvmabi.Options{})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want wrapped ErrUnsupported", err)
	}
}

func TestStage0RejectsMainWithInstructions(t *testing.T) {
	t.Parallel()
	module := moduleWith(trivialMainFn())
	module.Functions[0].Blocks[0].Instrs = []mir.Instr{&mir.AssignInstr{}}
	_, err := EmitMIR(module, llvmabi.Options{})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want wrapped ErrUnsupported", err)
	}
}

func TestStage0RejectsIntrinsicDeclaration(t *testing.T) {
	t.Parallel()
	module := moduleWith(trivialMainFn())
	module.Functions[0].IsIntrinsic = true
	_, err := EmitMIR(module, llvmabi.Options{})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want wrapped ErrUnsupported", err)
	}
}

func TestStage0FallsBackPackageName(t *testing.T) {
	t.Parallel()
	module := moduleWith(trivialMainFn())
	module.Package = ""
	got, err := EmitMIR(module, llvmabi.Options{PackageName: "demo"})
	if err != nil {
		t.Fatalf("EmitMIR: %v", err)
	}
	if !strings.Contains(string(got), "; package: demo") {
		t.Fatalf("expected fallback package name from opts:\n%s", got)
	}
}

// ---- P2a: non-main int literal return ----

func TestStage0EmitsIntLiteralFunctionAlongsideMain(t *testing.T) {
	t.Parallel()
	got, err := EmitMIR(moduleWith(trivialMainFn(), intLiteralFn("zero", 0), intLiteralFn("forty_two", 42), intLiteralFn("neg_one", -1)), llvmabi.Options{PackageName: "main"})
	if err != nil {
		t.Fatalf("EmitMIR: %v", err)
	}
	s := string(got)
	for _, want := range []string{
		"define i32 @main()",
		"ret i32 0",
		"define i64 @zero()",
		"ret i64 0",
		"define i64 @forty_two()",
		"ret i64 42",
		"define i64 @neg_one()",
		"ret i64 -1",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, s)
		}
	}
}

func TestStage0RejectsBoolReturnFunction(t *testing.T) {
	t.Parallel()
	fn := intLiteralFn("flag", 1)
	fn.ReturnType = ir.TBool
	_, err := EmitMIR(moduleWith(trivialMainFn(), fn), llvmabi.Options{})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want wrapped ErrUnsupported", err)
	}
}

func TestStage0RejectsMultiInstructionFunction(t *testing.T) {
	t.Parallel()
	fn := intLiteralFn("two_step", 7)
	fn.Blocks[0].Instrs = append(fn.Blocks[0].Instrs, &mir.AssignInstr{
		Dest: mir.Place{Local: 0},
		Src: &mir.UseRV{
			Op: &mir.ConstOp{Const: &mir.IntConst{Value: 9, T: ir.TInt}, T: ir.TInt},
		},
	})
	_, err := EmitMIR(moduleWith(trivialMainFn(), fn), llvmabi.Options{})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want wrapped ErrUnsupported", err)
	}
}

func TestStage0RejectsBinaryRVFunction(t *testing.T) {
	t.Parallel()
	fn := intLiteralFn("sum", 0)
	fn.Blocks[0].Instrs[0] = &mir.AssignInstr{
		Dest: mir.Place{Local: 0},
		Src: &mir.BinaryRV{
			Op:    mir.BinAdd,
			Left:  &mir.ConstOp{Const: &mir.IntConst{Value: 1, T: ir.TInt}, T: ir.TInt},
			Right: &mir.ConstOp{Const: &mir.IntConst{Value: 2, T: ir.TInt}, T: ir.TInt},
			T:     ir.TInt,
		},
	}
	_, err := EmitMIR(moduleWith(trivialMainFn(), fn), llvmabi.Options{})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want wrapped ErrUnsupported", err)
	}
}

// ---- P2b: int param passthrough ----

func TestStage0EmitsIntParamPassthrough(t *testing.T) {
	t.Parallel()
	got, err := EmitMIR(moduleWith(trivialMainFn(), intParamPassthroughFn("identity", "x"), intParamPassthroughFn("forward", "value")), llvmabi.Options{PackageName: "main"})
	if err != nil {
		t.Fatalf("EmitMIR: %v", err)
	}
	s := string(got)
	for _, want := range []string{
		"define i64 @identity(i64 %x)",
		"ret i64 %x",
		"define i64 @forward(i64 %value)",
		"ret i64 %value",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, s)
		}
	}
}

func TestStage0SanitizesEmptyParamName(t *testing.T) {
	t.Parallel()
	// Synthetic temporaries reach MIR with empty Name; we fall back
	// to `%p` rather than emit malformed IR.
	got, err := EmitMIR(moduleWith(trivialMainFn(), intParamPassthroughFn("identity", "")), llvmabi.Options{PackageName: "main"})
	if err != nil {
		t.Fatalf("EmitMIR: %v", err)
	}
	if !strings.Contains(string(got), "define i64 @identity(i64 %p)") {
		t.Fatalf("expected fallback param name `%%p`:\n%s", got)
	}
}

func TestStage0RejectsTwoParamFunction(t *testing.T) {
	t.Parallel()
	fn := intParamPassthroughFn("add", "x")
	fn.Params = append(fn.Params, 2)
	fn.Locals = append(fn.Locals, &mir.Local{ID: 2, Name: "y", Type: ir.TInt, IsParam: true})
	_, err := EmitMIR(moduleWith(trivialMainFn(), fn), llvmabi.Options{})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want wrapped ErrUnsupported", err)
	}
}

func TestStage0RejectsBoolParamPassthrough(t *testing.T) {
	t.Parallel()
	fn := intParamPassthroughFn("flag", "b")
	fn.ReturnType = ir.TBool
	for _, l := range fn.Locals {
		l.Type = ir.TBool
	}
	_, err := EmitMIR(moduleWith(trivialMainFn(), fn), llvmabi.Options{})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want wrapped ErrUnsupported", err)
	}
}

func TestStage0RejectsPassthroughCopyingNonParam(t *testing.T) {
	t.Parallel()
	fn := intParamPassthroughFn("mistake", "x")
	// CopyOp source = ret local (not the param) — even with the
	// right signature shape, this isn't the passthrough pattern.
	fn.Blocks[0].Instrs[0] = &mir.AssignInstr{
		Dest: mir.Place{Local: 0},
		Src: &mir.UseRV{
			Op: &mir.CopyOp{Place: mir.Place{Local: 0}, T: ir.TInt},
		},
	}
	_, err := EmitMIR(moduleWith(trivialMainFn(), fn), llvmabi.Options{})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want wrapped ErrUnsupported", err)
	}
}
