package stage0

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/llvmabi"
	"github.com/osty/osty/internal/mir"
)

// ---- fixture builders ----

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

// fnSpec describes a synthetic function: return type + zero or more
// params + the AssignInstr.Src to feed into the single-instruction
// pattern. This keeps the call sites short.
type fnSpec struct {
	name   string
	retT   mir.Type
	params []paramSpec
	src    mir.RValue
}

type paramSpec struct {
	name string
	ty   mir.Type
}

func makeFn(spec fnSpec) *mir.Function {
	locals := []*mir.Local{
		{ID: 0, Name: "ret", Type: spec.retT, IsReturn: true},
	}
	paramIDs := make([]mir.LocalID, 0, len(spec.params))
	for i, p := range spec.params {
		id := mir.LocalID(i + 1)
		paramIDs = append(paramIDs, id)
		locals = append(locals, &mir.Local{
			ID:      id,
			Name:    p.name,
			Type:    p.ty,
			IsParam: true,
		})
	}
	return &mir.Function{
		Name:        spec.name,
		Params:      paramIDs,
		ReturnType:  spec.retT,
		ReturnLocal: 0,
		Locals:      locals,
		Entry:       0,
		Blocks: []*mir.BasicBlock{
			{
				ID: 0,
				Instrs: []mir.Instr{
					&mir.AssignInstr{
						Dest: mir.Place{Local: 0},
						Src:  spec.src,
					},
				},
				Term: &mir.ReturnTerm{},
			},
		},
	}
}

func intConst(v int64) *mir.ConstOp {
	return &mir.ConstOp{Const: &mir.IntConst{Value: v, T: ir.TInt}, T: ir.TInt}
}

func boolConst(v bool) *mir.ConstOp {
	return &mir.ConstOp{Const: &mir.BoolConst{Value: v}, T: ir.TBool}
}

func stringConst(v string) *mir.ConstOp {
	return &mir.ConstOp{Const: &mir.StringConst{Value: v}, T: ir.TString}
}

func byteConst(v byte) *mir.ConstOp {
	return &mir.ConstOp{Const: &mir.ByteConst{Value: v}, T: ir.TByte}
}

func charConst(v rune) *mir.ConstOp {
	return &mir.ConstOp{Const: &mir.CharConst{Value: v}, T: ir.TChar}
}

func paramCopy(id mir.LocalID, ty mir.Type) *mir.CopyOp {
	return &mir.CopyOp{Place: mir.Place{Local: id}, T: ty}
}

func localCopy(id mir.LocalID, ty mir.Type) *mir.CopyOp {
	return &mir.CopyOp{Place: mir.Place{Local: id}, T: ty}
}

func useRV(op mir.Operand) *mir.UseRV {
	return &mir.UseRV{Op: op}
}

func binaryRV(op mir.BinaryOp, left, right mir.Operand, t mir.Type) *mir.BinaryRV {
	return &mir.BinaryRV{Op: op, Left: left, Right: right, T: t}
}

func moduleWith(fns ...*mir.Function) *mir.Module {
	return &mir.Module{
		Package:   "main",
		Functions: fns,
		Layouts:   mir.NewLayoutTable(),
	}
}

func emit(t *testing.T, fns ...*mir.Function) string {
	t.Helper()
	got, err := EmitMIR(moduleWith(fns...), llvmabi.Options{PackageName: "main"})
	if err != nil {
		t.Fatalf("EmitMIR: %v", err)
	}
	return string(got)
}

func mustReject(t *testing.T, fns ...*mir.Function) error {
	t.Helper()
	_, err := EmitMIR(moduleWith(fns...), llvmabi.Options{PackageName: "main"})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want wrapped ErrUnsupported", err)
	}
	return err
}

// ---- P1: trivial main ----

func TestStage0EmitsTrivialMain(t *testing.T) {
	t.Parallel()
	got, err := EmitMIR(moduleWith(trivialMainFn()), llvmabi.Options{PackageName: "main", Target: "x86_64-unknown-linux-gnu"})
	if err != nil {
		t.Fatalf("EmitMIR: %v", err)
	}
	for _, want := range []string{
		"stage0 bootstrap LLVM IR",
		"; package: main",
		"target triple = \"x86_64-unknown-linux-gnu\"",
		"define i32 @main()",
		"ret i32 0",
	} {
		if !strings.Contains(string(got), want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
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
	intLit := makeFn(fnSpec{name: "zero", retT: ir.TInt, src: useRV(intConst(0))})
	err := mustReject(t, intLit)
	if !strings.Contains(err.Error(), "module has no `main` function") {
		t.Fatalf("err = %q, want missing-main reason", err.Error())
	}
}

func TestStage0RejectsMainWithParameters(t *testing.T) {
	t.Parallel()
	main := trivialMainFn()
	main.Params = []mir.LocalID{0}
	mustReject(t, main)
}

func TestStage0RejectsMainWithInstructions(t *testing.T) {
	t.Parallel()
	main := trivialMainFn()
	main.Blocks[0].Instrs = []mir.Instr{&mir.AssignInstr{}}
	mustReject(t, main)
}

func TestStage0RejectsIntrinsicDeclaration(t *testing.T) {
	t.Parallel()
	main := trivialMainFn()
	main.IsIntrinsic = true
	mustReject(t, main)
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

// ---- single-instruction return: literal & passthrough ----

func TestStage0EmitsIntLiteralReturn(t *testing.T) {
	t.Parallel()
	got := emit(t, trivialMainFn(),
		makeFn(fnSpec{name: "zero", retT: ir.TInt, src: useRV(intConst(0))}),
		makeFn(fnSpec{name: "answer", retT: ir.TInt, src: useRV(intConst(42))}),
		makeFn(fnSpec{name: "neg_one", retT: ir.TInt, src: useRV(intConst(-1))}),
	)
	for _, want := range []string{
		"define i64 @zero()", "ret i64 0",
		"define i64 @answer()", "ret i64 42",
		"define i64 @neg_one()", "ret i64 -1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0EmitsBoolLiteralReturn(t *testing.T) {
	t.Parallel()
	got := emit(t, trivialMainFn(),
		makeFn(fnSpec{name: "always_true", retT: ir.TBool, src: useRV(boolConst(true))}),
		makeFn(fnSpec{name: "never", retT: ir.TBool, src: useRV(boolConst(false))}),
	)
	for _, want := range []string{
		"define i1 @always_true()", "ret i1 true",
		"define i1 @never()", "ret i1 false",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0EmitsIntParamPassthrough(t *testing.T) {
	t.Parallel()
	got := emit(t, trivialMainFn(),
		makeFn(fnSpec{
			name:   "identity",
			retT:   ir.TInt,
			params: []paramSpec{{name: "x", ty: ir.TInt}},
			src:    useRV(paramCopy(1, ir.TInt)),
		}),
		makeFn(fnSpec{
			name:   "forward",
			retT:   ir.TInt,
			params: []paramSpec{{name: "value", ty: ir.TInt}},
			src:    useRV(paramCopy(1, ir.TInt)),
		}),
	)
	for _, want := range []string{
		"define i64 @identity(i64 %x)",
		"ret i64 %x",
		"define i64 @forward(i64 %value)",
		"ret i64 %value",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0EmitsBoolParamPassthrough(t *testing.T) {
	t.Parallel()
	got := emit(t, trivialMainFn(),
		makeFn(fnSpec{
			name:   "echo",
			retT:   ir.TBool,
			params: []paramSpec{{name: "flag", ty: ir.TBool}},
			src:    useRV(paramCopy(1, ir.TBool)),
		}),
	)
	for _, want := range []string{"define i1 @echo(i1 %flag)", "ret i1 %flag"} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0SanitizesEmptyParamName(t *testing.T) {
	t.Parallel()
	got := emit(t, trivialMainFn(),
		makeFn(fnSpec{
			name:   "identity",
			retT:   ir.TInt,
			params: []paramSpec{{name: "", ty: ir.TInt}},
			src:    useRV(paramCopy(1, ir.TInt)),
		}),
	)
	if !strings.Contains(got, "define i64 @identity(i64 %a)") {
		t.Fatalf("expected fallback param name `%%a`:\n%s", got)
	}
}

// TestStage0RenamesParamCollidingWithEntryLabel guards against a
// regression where a user-source parameter named `entry` collides
// with the LLVM `entry:` block label (LLVM puts param names and
// block labels in the same per-function value namespace, so the
// duplicate name produces "unable to create block named 'entry'").
// The fix renames such a parameter via the disambiguation pass.
func TestStage0RenamesParamCollidingWithEntryLabel(t *testing.T) {
	t.Parallel()
	got := emit(t, trivialMainFn(),
		makeFn(fnSpec{
			name:   "passthroughEntry",
			retT:   ir.TInt,
			params: []paramSpec{{name: "entry", ty: ir.TInt}},
			src:    useRV(paramCopy(1, ir.TInt)),
		}),
	)
	if strings.Contains(got, "(i64 %entry)") {
		t.Fatalf("expected param `entry` to be renamed to avoid label collision:\n%s", got)
	}
	if !strings.Contains(got, "define i64 @passthroughEntry(i64 %entry.0)") {
		t.Fatalf("expected param to be renamed to `%%entry.0`:\n%s", got)
	}
	if !strings.Contains(got, "ret i64 %entry.0") {
		t.Fatalf("expected return to reference renamed param:\n%s", got)
	}
}

// ---- single-instruction return: arithmetic ops ----

func TestStage0EmitsIntArithOps(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		op   mir.BinaryOp
		llvm string
	}{
		{"add", mir.BinAdd, "add"},
		{"sub", mir.BinSub, "sub"},
		{"mul", mir.BinMul, "mul"},
		{"div", mir.BinDiv, "sdiv"},
		{"mod", mir.BinMod, "srem"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			fn := makeFn(fnSpec{
				name:   c.name,
				retT:   ir.TInt,
				params: []paramSpec{{name: "a", ty: ir.TInt}, {name: "b", ty: ir.TInt}},
				src:    binaryRV(c.op, paramCopy(1, ir.TInt), paramCopy(2, ir.TInt), ir.TInt),
			})
			got := emit(t, trivialMainFn(), fn)
			body := "%0 = " + c.llvm + " i64 %a, %b"
			for _, want := range []string{"define i64 @" + c.name + "(i64 %a, i64 %b)", body, "ret i64 %0"} {
				if !strings.Contains(got, want) {
					t.Fatalf("emitted IR missing %q:\n%s", want, got)
				}
			}
		})
	}
}

// ---- single-instruction return: comparison ops → Bool ----

func TestStage0EmitsIntComparisonOps(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		op   mir.BinaryOp
		llvm string
	}{
		{"eq", mir.BinEq, "icmp eq"},
		{"ne", mir.BinNeq, "icmp ne"},
		{"lt", mir.BinLt, "icmp slt"},
		{"le", mir.BinLeq, "icmp sle"},
		{"gt", mir.BinGt, "icmp sgt"},
		{"ge", mir.BinGeq, "icmp sge"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			fn := makeFn(fnSpec{
				name:   c.name,
				retT:   ir.TBool,
				params: []paramSpec{{name: "a", ty: ir.TInt}, {name: "b", ty: ir.TInt}},
				src:    binaryRV(c.op, paramCopy(1, ir.TInt), paramCopy(2, ir.TInt), ir.TBool),
			})
			got := emit(t, trivialMainFn(), fn)
			body := "%0 = " + c.llvm + " i64 %a, %b"
			for _, want := range []string{"define i1 @" + c.name + "(i64 %a, i64 %b)", body, "ret i1 %0"} {
				if !strings.Contains(got, want) {
					t.Fatalf("emitted IR missing %q:\n%s", want, got)
				}
			}
		})
	}
}

// ---- single-instruction return: bitwise / shift ops ----

func TestStage0EmitsIntBitwiseShiftOps(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		op   mir.BinaryOp
		llvm string
	}{
		{"bit_and", mir.BinBitAnd, "and"},
		{"bit_or", mir.BinBitOr, "or"},
		{"bit_xor", mir.BinBitXor, "xor"},
		{"shl", mir.BinShl, "shl"},
		{"shr", mir.BinShr, "ashr"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			fn := makeFn(fnSpec{
				name:   c.name,
				retT:   ir.TInt,
				params: []paramSpec{{name: "a", ty: ir.TInt}, {name: "b", ty: ir.TInt}},
				src:    binaryRV(c.op, paramCopy(1, ir.TInt), paramCopy(2, ir.TInt), ir.TInt),
			})
			got := emit(t, trivialMainFn(), fn)
			body := "%0 = " + c.llvm + " i64 %a, %b"
			for _, want := range []string{"define i64 @" + c.name + "(i64 %a, i64 %b)", body, "ret i64 %0"} {
				if !strings.Contains(got, want) {
					t.Fatalf("emitted IR missing %q:\n%s", want, got)
				}
			}
		})
	}
}

// ---- single-instruction return: logical Bool ops ----

func TestStage0EmitsBoolLogicalOps(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		op   mir.BinaryOp
		llvm string
	}{
		{"both", mir.BinAnd, "and"},
		{"either", mir.BinOr, "or"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			fn := makeFn(fnSpec{
				name:   c.name,
				retT:   ir.TBool,
				params: []paramSpec{{name: "p", ty: ir.TBool}, {name: "q", ty: ir.TBool}},
				src:    binaryRV(c.op, paramCopy(1, ir.TBool), paramCopy(2, ir.TBool), ir.TBool),
			})
			got := emit(t, trivialMainFn(), fn)
			body := "%0 = " + c.llvm + " i1 %p, %q"
			for _, want := range []string{"define i1 @" + c.name + "(i1 %p, i1 %q)", body, "ret i1 %0"} {
				if !strings.Contains(got, want) {
					t.Fatalf("emitted IR missing %q:\n%s", want, got)
				}
			}
		})
	}
}

// ---- mixed const+var operands ----

func TestStage0EmitsConstPlusVar(t *testing.T) {
	t.Parallel()
	got := emit(t, trivialMainFn(),
		makeFn(fnSpec{
			name:   "inc",
			retT:   ir.TInt,
			params: []paramSpec{{name: "x", ty: ir.TInt}},
			src:    binaryRV(mir.BinAdd, paramCopy(1, ir.TInt), intConst(1), ir.TInt),
		}),
		makeFn(fnSpec{
			name:   "dec",
			retT:   ir.TInt,
			params: []paramSpec{{name: "x", ty: ir.TInt}},
			src:    binaryRV(mir.BinSub, paramCopy(1, ir.TInt), intConst(1), ir.TInt),
		}),
		makeFn(fnSpec{
			name:   "double",
			retT:   ir.TInt,
			params: []paramSpec{{name: "x", ty: ir.TInt}},
			src:    binaryRV(mir.BinMul, intConst(2), paramCopy(1, ir.TInt), ir.TInt),
		}),
	)
	for _, want := range []string{
		"define i64 @inc(i64 %x)", "%0 = add i64 %x, 1",
		"define i64 @dec(i64 %x)", "%0 = sub i64 %x, 1",
		"define i64 @double(i64 %x)", "%0 = mul i64 2, %x",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0EmitsConstOnlyBinaryOp(t *testing.T) {
	t.Parallel()
	// `fn answer() -> Int { 1 + 2 }` — both operands are consts.
	// Stage0 doesn't const-fold; LLVM downstream optimisers can.
	got := emit(t, trivialMainFn(),
		makeFn(fnSpec{
			name: "answer",
			retT: ir.TInt,
			src:  binaryRV(mir.BinAdd, intConst(1), intConst(2), ir.TInt),
		}),
	)
	for _, want := range []string{"define i64 @answer()", "%0 = add i64 1, 2", "ret i64 %0"} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0EmitsSwappedParamOrder(t *testing.T) {
	t.Parallel()
	// `fn diff(a: Int, b: Int) -> Int { b - a }` — param 2 is the
	// left operand. Stage0 used to be strict about operand order
	// matching params; the unified single-instruction matcher now
	// emits whatever MIR provides.
	got := emit(t, trivialMainFn(),
		makeFn(fnSpec{
			name:   "diff",
			retT:   ir.TInt,
			params: []paramSpec{{name: "a", ty: ir.TInt}, {name: "b", ty: ir.TInt}},
			src:    binaryRV(mir.BinSub, paramCopy(2, ir.TInt), paramCopy(1, ir.TInt), ir.TInt),
		}),
	)
	if !strings.Contains(got, "%0 = sub i64 %b, %a") {
		t.Fatalf("expected swapped operands `%%b, %%a`:\n%s", got)
	}
}

func TestStage0EmitsComparisonAgainstConst(t *testing.T) {
	t.Parallel()
	got := emit(t, trivialMainFn(),
		makeFn(fnSpec{
			name:   "is_zero",
			retT:   ir.TBool,
			params: []paramSpec{{name: "x", ty: ir.TInt}},
			src:    binaryRV(mir.BinEq, paramCopy(1, ir.TInt), intConst(0), ir.TBool),
		}),
	)
	for _, want := range []string{"define i1 @is_zero(i64 %x)", "%0 = icmp eq i64 %x, 0", "ret i1 %0"} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

// ---- rejection paths ----

func TestStage0RejectsNineParams(t *testing.T) {
	t.Parallel()
	mustReject(t, trivialMainFn(),
		makeFn(fnSpec{
			name: "nine",
			retT: ir.TInt,
			params: []paramSpec{
				{name: "a", ty: ir.TInt},
				{name: "b", ty: ir.TInt},
				{name: "c", ty: ir.TInt},
				{name: "d", ty: ir.TInt},
				{name: "e", ty: ir.TInt},
				{name: "f", ty: ir.TInt},
				{name: "g", ty: ir.TInt},
				{name: "h", ty: ir.TInt},
				{name: "i", ty: ir.TInt},
				{name: "j", ty: ir.TInt},
				{name: "k", ty: ir.TInt},
				{name: "l", ty: ir.TInt},
				{name: "m", ty: ir.TInt},
				{name: "n", ty: ir.TInt},
				{name: "o", ty: ir.TInt},
				{name: "p", ty: ir.TInt},
				{name: "q", ty: ir.TInt},
				{name: "r", ty: ir.TInt},
				{name: "s", ty: ir.TInt},
				{name: "t", ty: ir.TInt},
				{name: "u", ty: ir.TInt},
				{name: "v", ty: ir.TInt},
				{name: "w", ty: ir.TInt},
				{name: "x", ty: ir.TInt},
				{name: "y", ty: ir.TInt},
				{name: "z", ty: ir.TInt},
				{name: "p26", ty: ir.TInt},
				{name: "p27", ty: ir.TInt},
				{name: "p28", ty: ir.TInt},
				{name: "p29", ty: ir.TInt},
				{name: "p30", ty: ir.TInt},
				{name: "p31", ty: ir.TInt},
				{name: "p32", ty: ir.TInt},
				{name: "p33", ty: ir.TInt},
				{name: "p34", ty: ir.TInt},
				{name: "p35", ty: ir.TInt},
				{name: "p36", ty: ir.TInt},
				{name: "p37", ty: ir.TInt},
				{name: "p38", ty: ir.TInt},
				{name: "p39", ty: ir.TInt},
				{name: "p40", ty: ir.TInt},
				{name: "p41", ty: ir.TInt},
				{name: "p42", ty: ir.TInt},
				{name: "p43", ty: ir.TInt},
				{name: "p44", ty: ir.TInt},
				{name: "p45", ty: ir.TInt},
				{name: "p46", ty: ir.TInt},
				{name: "p47", ty: ir.TInt},
				{name: "p48", ty: ir.TInt},
				{name: "p49", ty: ir.TInt},
			},
			src: useRV(paramCopy(1, ir.TInt)),
		}),
	)
}

func TestStage0RejectsFloatReturn(t *testing.T) {
	t.Parallel()
	mustReject(t, trivialMainFn(),
		makeFn(fnSpec{
			name: "pi",
			retT: ir.TFloat,
			src:  useRV(intConst(3)), // type mismatch — Float ret with Int operand
		}),
	)
}

func TestStage0RejectsTypeMismatchedLiteralReturn(t *testing.T) {
	t.Parallel()
	// Return Bool but body produces Int literal.
	mustReject(t, trivialMainFn(),
		makeFn(fnSpec{
			name: "bad",
			retT: ir.TBool,
			src:  useRV(intConst(1)),
		}),
	)
}

func TestStage0RejectsTypeMismatchedComparisonReturn(t *testing.T) {
	t.Parallel()
	// Return Int but body is comparison (which is Bool).
	mustReject(t, trivialMainFn(),
		makeFn(fnSpec{
			name:   "bad",
			retT:   ir.TInt,
			params: []paramSpec{{name: "a", ty: ir.TInt}, {name: "b", ty: ir.TInt}},
			src:    binaryRV(mir.BinEq, paramCopy(1, ir.TInt), paramCopy(2, ir.TInt), ir.TInt),
		}),
	)
}

func TestStage0RejectsMixedOperandTypesInArith(t *testing.T) {
	t.Parallel()
	// Arith op with Bool operand. classifyBinary says Add expects
	// Int operands → declines.
	mustReject(t, trivialMainFn(),
		makeFn(fnSpec{
			name:   "bad",
			retT:   ir.TInt,
			params: []paramSpec{{name: "a", ty: ir.TInt}, {name: "p", ty: ir.TBool}},
			src:    binaryRV(mir.BinAdd, paramCopy(1, ir.TInt), paramCopy(2, ir.TBool), ir.TInt),
		}),
	)
}

func TestStage0RejectsLogicalOnIntInputs(t *testing.T) {
	t.Parallel()
	// BinAnd on Int → expects Bool inputs.
	mustReject(t, trivialMainFn(),
		makeFn(fnSpec{
			name:   "bad",
			retT:   ir.TBool,
			params: []paramSpec{{name: "a", ty: ir.TInt}, {name: "b", ty: ir.TInt}},
			src:    binaryRV(mir.BinAnd, paramCopy(1, ir.TInt), paramCopy(2, ir.TInt), ir.TBool),
		}),
	)
}

func TestStage0RejectsProjectionInOperand(t *testing.T) {
	t.Parallel()
	// CopyOp with a field projection — stage0 doesn't lower
	// aggregates. Decline.
	fn := makeFn(fnSpec{
		name:   "bad",
		retT:   ir.TInt,
		params: []paramSpec{{name: "p", ty: ir.TInt}},
		src:    useRV(paramCopy(1, ir.TInt)),
	})
	cp := fn.Blocks[0].Instrs[0].(*mir.AssignInstr).Src.(*mir.UseRV).Op.(*mir.CopyOp)
	cp.Place.Projections = []mir.Projection{&mir.FieldProj{Index: 0}}
	mustReject(t, trivialMainFn(), fn)
}

func TestStage0RejectsCopyFromNonParam(t *testing.T) {
	t.Parallel()
	// CopyOp pointing at the return local — stage0 doesn't track
	// non-param locals in single-instruction patterns.
	fn := makeFn(fnSpec{
		name: "bad",
		retT: ir.TInt,
		src:  useRV(paramCopy(0, ir.TInt)),
	})
	mustReject(t, trivialMainFn(), fn)
}

func TestStage0RejectsUnsupportedBinaryOp(t *testing.T) {
	t.Parallel()
	// BinInvalid is sentinel for "no op". classifyBinary returns
	// "" → matcher declines.
	mustReject(t, trivialMainFn(),
		makeFn(fnSpec{
			name:   "bad",
			retT:   ir.TInt,
			params: []paramSpec{{name: "a", ty: ir.TInt}, {name: "b", ty: ir.TInt}},
			src:    binaryRV(mir.BinInvalid, paramCopy(1, ir.TInt), paramCopy(2, ir.TInt), ir.TInt),
		}),
	)
}

// ---- P3a: multi-instruction sequential return ----
//
// makeMultiInstrFn builds a function with the given instruction
// sequence. `extraLocals` lists non-param non-return locals (the
// targets of intermediate AssignInstrs).
func makeMultiInstrFn(name string, retT mir.Type, params []paramSpec, extraLocals []paramSpec, instrs []mir.Instr) *mir.Function {
	locals := []*mir.Local{
		{ID: 0, Name: "ret", Type: retT, IsReturn: true},
	}
	paramIDs := make([]mir.LocalID, 0, len(params))
	nextID := mir.LocalID(1)
	for _, p := range params {
		paramIDs = append(paramIDs, nextID)
		locals = append(locals, &mir.Local{ID: nextID, Name: p.name, Type: p.ty, IsParam: true})
		nextID++
	}
	for _, l := range extraLocals {
		locals = append(locals, &mir.Local{ID: nextID, Name: l.name, Type: l.ty})
		nextID++
	}
	return &mir.Function{
		Name:        name,
		Params:      paramIDs,
		ReturnType:  retT,
		ReturnLocal: 0,
		Locals:      locals,
		Entry:       0,
		Blocks: []*mir.BasicBlock{
			{ID: 0, Instrs: instrs, Term: &mir.ReturnTerm{}},
		},
	}
}

func assign(destID mir.LocalID, src mir.RValue) *mir.AssignInstr {
	return &mir.AssignInstr{Dest: mir.Place{Local: destID}, Src: src}
}

func TestStage0EmitsLetThenReturn(t *testing.T) {
	t.Parallel()
	// `fn double(x: Int) -> Int { let y = x; y + y }`
	// Locals: 0 ret, 1 x param, 2 y temp.
	// MIR: ret = (CopyOp y) which is binary on (CopyOp y, CopyOp y).
	// Actually the lowerer would compute: y = x; ret = y + y.
	// Use UseRV(CopyOp x) for y, then BinaryRV(BinAdd, CopyOp y, CopyOp y) for ret.
	fn := makeMultiInstrFn(
		"double",
		ir.TInt,
		[]paramSpec{{name: "x", ty: ir.TInt}},
		[]paramSpec{{name: "y", ty: ir.TInt}},
		[]mir.Instr{
			assign(2, useRV(paramCopy(1, ir.TInt))),
			assign(0, binaryRV(mir.BinAdd, paramCopy(2, ir.TInt), paramCopy(2, ir.TInt), ir.TInt)),
		},
	)
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"define i64 @double(i64 %x)",
		"%0 = add i64 %x, %x", // y inlined as %x
		"ret i64 %0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0EmitsThreeStepArith(t *testing.T) {
	t.Parallel()
	// `fn poly(a: Int, b: Int) -> Int {
	//      let s = a + b
	//      let p = a * b
	//      s - p
	//  }`
	// Locals: 0 ret, 1 a, 2 b, 3 s, 4 p.
	fn := makeMultiInstrFn(
		"poly",
		ir.TInt,
		[]paramSpec{{name: "a", ty: ir.TInt}, {name: "b", ty: ir.TInt}},
		[]paramSpec{{name: "s", ty: ir.TInt}, {name: "p", ty: ir.TInt}},
		[]mir.Instr{
			assign(3, binaryRV(mir.BinAdd, paramCopy(1, ir.TInt), paramCopy(2, ir.TInt), ir.TInt)),
			assign(4, binaryRV(mir.BinMul, paramCopy(1, ir.TInt), paramCopy(2, ir.TInt), ir.TInt)),
			assign(0, binaryRV(mir.BinSub, paramCopy(3, ir.TInt), paramCopy(4, ir.TInt), ir.TInt)),
		},
	)
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"define i64 @poly(i64 %a, i64 %b)",
		"%0 = add i64 %a, %b",
		"%1 = mul i64 %a, %b",
		"%2 = sub i64 %0, %1",
		"ret i64 %2",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0EmitsConstThenUse(t *testing.T) {
	t.Parallel()
	// `fn add_one(x: Int) -> Int { let one = 1; x + one }`
	fn := makeMultiInstrFn(
		"add_one",
		ir.TInt,
		[]paramSpec{{name: "x", ty: ir.TInt}},
		[]paramSpec{{name: "one", ty: ir.TInt}},
		[]mir.Instr{
			assign(2, useRV(intConst(1))),
			assign(0, binaryRV(mir.BinAdd, paramCopy(1, ir.TInt), paramCopy(2, ir.TInt), ir.TInt)),
		},
	)
	got := emit(t, trivialMainFn(), fn)
	// `one` inlines to literal 1.
	if !strings.Contains(got, "%0 = add i64 %x, 1") {
		t.Fatalf("expected inlined const 1:\n%s", got)
	}
}

func TestStage0EmitsBoolThreshold(t *testing.T) {
	t.Parallel()
	// `fn over(x: Int, threshold: Int) -> Bool {
	//      let diff = x - threshold
	//      diff > 0
	//  }`
	fn := makeMultiInstrFn(
		"over",
		ir.TBool,
		[]paramSpec{{name: "x", ty: ir.TInt}, {name: "threshold", ty: ir.TInt}},
		[]paramSpec{{name: "diff", ty: ir.TInt}},
		[]mir.Instr{
			assign(3, binaryRV(mir.BinSub, paramCopy(1, ir.TInt), paramCopy(2, ir.TInt), ir.TInt)),
			assign(0, binaryRV(mir.BinGt, paramCopy(3, ir.TInt), intConst(0), ir.TBool)),
		},
	)
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"define i1 @over(i64 %x, i64 %threshold)",
		"%0 = sub i64 %x, %threshold",
		"%1 = icmp sgt i64 %0, 0",
		"ret i1 %1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0EmitsBoolLogicChain(t *testing.T) {
	t.Parallel()
	// `fn ranged(x: Int, lo: Int, hi: Int) -> Bool {
	//      let above = x >= lo
	//      let below = x <= hi
	//      above && below
	//  }`
	fn := makeMultiInstrFn(
		"ranged",
		ir.TBool,
		[]paramSpec{{name: "x", ty: ir.TInt}, {name: "lo", ty: ir.TInt}, {name: "hi", ty: ir.TInt}},
		nil,
		nil,
	)
	// Three-param functions are rejected by P2e — confirm this
	// remains the case under sequential pattern too.
	mustReject(t, trivialMainFn(), fn)
}

func TestStage0EmitsTwoParamBoolLogic(t *testing.T) {
	t.Parallel()
	// `fn between(lo: Int, hi: Int) -> Bool {
	//      let valid = lo <= hi
	//      let nonneg = lo >= 0
	//      valid && nonneg
	//  }`
	fn := makeMultiInstrFn(
		"between",
		ir.TBool,
		[]paramSpec{{name: "lo", ty: ir.TInt}, {name: "hi", ty: ir.TInt}},
		[]paramSpec{{name: "valid", ty: ir.TBool}, {name: "nonneg", ty: ir.TBool}},
		[]mir.Instr{
			assign(3, binaryRV(mir.BinLeq, paramCopy(1, ir.TInt), paramCopy(2, ir.TInt), ir.TBool)),
			assign(4, binaryRV(mir.BinGeq, paramCopy(1, ir.TInt), intConst(0), ir.TBool)),
			assign(0, binaryRV(mir.BinAnd, paramCopy(3, ir.TBool), paramCopy(4, ir.TBool), ir.TBool)),
		},
	)
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"define i1 @between(i64 %lo, i64 %hi)",
		"%0 = icmp sle i64 %lo, %hi",
		"%1 = icmp sge i64 %lo, 0",
		"%2 = and i1 %0, %1",
		"ret i1 %2",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0EmitsCopyChain(t *testing.T) {
	t.Parallel()
	// `fn passthrough(x: Int) -> Int { let a = x; let b = a; b }`
	// Both temps inline to %x; no LLVM emitted.
	fn := makeMultiInstrFn(
		"passthrough",
		ir.TInt,
		[]paramSpec{{name: "x", ty: ir.TInt}},
		[]paramSpec{{name: "a", ty: ir.TInt}, {name: "b", ty: ir.TInt}},
		[]mir.Instr{
			assign(2, useRV(paramCopy(1, ir.TInt))),
			assign(3, useRV(paramCopy(2, ir.TInt))),
			assign(0, useRV(paramCopy(3, ir.TInt))),
		},
	)
	got := emit(t, trivialMainFn(), fn)
	if !strings.Contains(got, "ret i64 %x") {
		t.Fatalf("expected `ret i64 %%x` (full inline chain):\n%s", got)
	}
	// No SSA register should have been emitted.
	if strings.Contains(got, "%0 =") {
		t.Fatalf("expected no SSA register for inline chain:\n%s", got)
	}
}

// ---- multi-instruction rejection paths ----

func TestStage0RejectsLocalReassignment(t *testing.T) {
	t.Parallel()
	// `let y = x; y = 1; y` — assigning to `y` twice declines.
	fn := makeMultiInstrFn(
		"bad",
		ir.TInt,
		[]paramSpec{{name: "x", ty: ir.TInt}},
		[]paramSpec{{name: "y", ty: ir.TInt}},
		[]mir.Instr{
			assign(2, useRV(paramCopy(1, ir.TInt))),
			assign(2, useRV(intConst(1))), // reassign
			assign(0, useRV(paramCopy(2, ir.TInt))),
		},
	)
	mustReject(t, trivialMainFn(), fn)
}

func TestStage0GenericCFGStringBuilderReassignment(t *testing.T) {
	t.Parallel()
	fn := makeMultiInstrFn(
		"usage",
		ir.TString,
		nil,
		[]paramSpec{{name: "out", ty: ir.TString}},
		[]mir.Instr{
			assign(1, useRV(stringConst("osty"))),
			assign(1, binaryRV(mir.BinAdd, paramCopy(1, ir.TString), stringConst(" check"), ir.TString)),
			assign(1, binaryRV(mir.BinAdd, paramCopy(1, ir.TString), stringConst("\n"), ir.TString)),
			assign(0, useRV(paramCopy(1, ir.TString))),
		},
	)
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"define ptr @usage()",
		"%out.slot = alloca ptr",
		"call ptr @osty_rt_strings_Concat",
		"store ptr %",
		"ret ptr %",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0RejectsForwardReference(t *testing.T) {
	t.Parallel()
	// Use of `y` before it's defined.
	fn := makeMultiInstrFn(
		"bad",
		ir.TInt,
		[]paramSpec{{name: "x", ty: ir.TInt}},
		[]paramSpec{{name: "y", ty: ir.TInt}},
		[]mir.Instr{
			// Read y before assigning it.
			assign(0, useRV(paramCopy(2, ir.TInt))),
			assign(2, useRV(paramCopy(1, ir.TInt))),
		},
	)
	mustReject(t, trivialMainFn(), fn)
}

func TestStage0RejectsParamReassignment(t *testing.T) {
	t.Parallel()
	// `x = 1` — overwriting a param declines.
	fn := makeMultiInstrFn(
		"bad",
		ir.TInt,
		[]paramSpec{{name: "x", ty: ir.TInt}},
		nil,
		[]mir.Instr{
			assign(1, useRV(intConst(1))), // overwrite param x
			assign(0, useRV(paramCopy(1, ir.TInt))),
		},
	)
	mustReject(t, trivialMainFn(), fn)
}

func TestStage0RejectsReturnLocalNeverAssigned(t *testing.T) {
	t.Parallel()
	// Block has instructions but never writes to ret.
	fn := makeMultiInstrFn(
		"bad",
		ir.TInt,
		[]paramSpec{{name: "x", ty: ir.TInt}},
		[]paramSpec{{name: "y", ty: ir.TInt}},
		[]mir.Instr{
			assign(2, useRV(paramCopy(1, ir.TInt))),
		},
	)
	mustReject(t, trivialMainFn(), fn)
}

func TestStage0RejectsTypeMismatchInChain(t *testing.T) {
	t.Parallel()
	// Local typed Bool, assigned an Int operand.
	fn := makeMultiInstrFn(
		"bad",
		ir.TBool,
		[]paramSpec{{name: "x", ty: ir.TInt}},
		[]paramSpec{{name: "y", ty: ir.TBool}}, // Bool local
		[]mir.Instr{
			assign(2, useRV(paramCopy(1, ir.TInt))), // Int → Bool: type mismatch
			assign(0, useRV(paramCopy(2, ir.TBool))),
		},
	)
	mustReject(t, trivialMainFn(), fn)
}

// ---- P3b: function calls ----

func callInstr(destID mir.LocalID, calleeSym string, calleeFnTy *ir.FnType, args ...mir.Operand) *mir.CallInstr {
	return &mir.CallInstr{
		Dest:   &mir.Place{Local: destID},
		Callee: &mir.FnRef{Symbol: calleeSym, Type: calleeFnTy},
		Args:   args,
	}
}

func fnTy(ret mir.Type, params ...mir.Type) *ir.FnType {
	return &ir.FnType{Params: params, Return: ret}
}

func TestStage0EmitsLeafCall(t *testing.T) {
	t.Parallel()
	// `fn add(a, b) -> Int { a + b }`
	addFn := makeFn(fnSpec{
		name:   "add",
		retT:   ir.TInt,
		params: []paramSpec{{name: "a", ty: ir.TInt}, {name: "b", ty: ir.TInt}},
		src:    binaryRV(mir.BinAdd, paramCopy(1, ir.TInt), paramCopy(2, ir.TInt), ir.TInt),
	})
	// `fn caller() -> Int { add(1, 2) }`
	caller := makeMultiInstrFn(
		"caller",
		ir.TInt,
		nil,
		[]paramSpec{{name: "r", ty: ir.TInt}},
		[]mir.Instr{
			callInstr(1, "add", fnTy(ir.TInt, ir.TInt, ir.TInt), intConst(1), intConst(2)),
			assign(0, useRV(paramCopy(1, ir.TInt))),
		},
	)
	got := emit(t, trivialMainFn(), addFn, caller)
	for _, want := range []string{
		"define i64 @add(i64 %a, i64 %b)",
		"define i64 @caller()",
		"%0 = call i64 @add(i64 1, i64 2)",
		"ret i64 %0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0EmitsCallWithVariableArgs(t *testing.T) {
	t.Parallel()
	addFn := makeFn(fnSpec{
		name:   "add",
		retT:   ir.TInt,
		params: []paramSpec{{name: "a", ty: ir.TInt}, {name: "b", ty: ir.TInt}},
		src:    binaryRV(mir.BinAdd, paramCopy(1, ir.TInt), paramCopy(2, ir.TInt), ir.TInt),
	})
	// `fn caller(x: Int, y: Int) -> Int { add(x, y) }`
	caller := makeMultiInstrFn(
		"caller",
		ir.TInt,
		[]paramSpec{{name: "x", ty: ir.TInt}, {name: "y", ty: ir.TInt}},
		[]paramSpec{{name: "r", ty: ir.TInt}},
		[]mir.Instr{
			callInstr(3, "add", fnTy(ir.TInt, ir.TInt, ir.TInt), paramCopy(1, ir.TInt), paramCopy(2, ir.TInt)),
			assign(0, useRV(paramCopy(3, ir.TInt))),
		},
	)
	got := emit(t, trivialMainFn(), addFn, caller)
	if !strings.Contains(got, "%0 = call i64 @add(i64 %x, i64 %y)") {
		t.Fatalf("expected call with param args:\n%s", got)
	}
}

func TestStage0EmitsCallChainedWithArith(t *testing.T) {
	t.Parallel()
	doubleFn := makeFn(fnSpec{
		name:   "double",
		retT:   ir.TInt,
		params: []paramSpec{{name: "x", ty: ir.TInt}},
		src:    binaryRV(mir.BinMul, paramCopy(1, ir.TInt), intConst(2), ir.TInt),
	})
	// `fn quad(x: Int) -> Int { let d = double(x); d + d }`
	quad := makeMultiInstrFn(
		"quad",
		ir.TInt,
		[]paramSpec{{name: "x", ty: ir.TInt}},
		[]paramSpec{{name: "d", ty: ir.TInt}},
		[]mir.Instr{
			callInstr(2, "double", fnTy(ir.TInt, ir.TInt), paramCopy(1, ir.TInt)),
			assign(0, binaryRV(mir.BinAdd, paramCopy(2, ir.TInt), paramCopy(2, ir.TInt), ir.TInt)),
		},
	)
	got := emit(t, trivialMainFn(), doubleFn, quad)
	for _, want := range []string{
		"%0 = call i64 @double(i64 %x)",
		"%1 = add i64 %0, %0",
		"ret i64 %1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0EmitsBoolReturningCall(t *testing.T) {
	t.Parallel()
	isPos := makeFn(fnSpec{
		name:   "is_positive",
		retT:   ir.TBool,
		params: []paramSpec{{name: "n", ty: ir.TInt}},
		src:    binaryRV(mir.BinGt, paramCopy(1, ir.TInt), intConst(0), ir.TBool),
	})
	caller := makeMultiInstrFn(
		"check",
		ir.TBool,
		[]paramSpec{{name: "x", ty: ir.TInt}},
		[]paramSpec{{name: "r", ty: ir.TBool}},
		[]mir.Instr{
			callInstr(2, "is_positive", fnTy(ir.TBool, ir.TInt), paramCopy(1, ir.TInt)),
			assign(0, useRV(paramCopy(2, ir.TBool))),
		},
	)
	got := emit(t, trivialMainFn(), isPos, caller)
	for _, want := range []string{
		"define i1 @is_positive(i64 %n)",
		"%0 = call i1 @is_positive(i64 %x)",
		"ret i1 %0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0EmitsZeroArgCall(t *testing.T) {
	t.Parallel()
	zero := makeFn(fnSpec{
		name: "zero",
		retT: ir.TInt,
		src:  useRV(intConst(0)),
	})
	caller := makeMultiInstrFn(
		"plus_zero",
		ir.TInt,
		[]paramSpec{{name: "x", ty: ir.TInt}},
		[]paramSpec{{name: "z", ty: ir.TInt}},
		[]mir.Instr{
			callInstr(2, "zero", fnTy(ir.TInt)),
			assign(0, binaryRV(mir.BinAdd, paramCopy(1, ir.TInt), paramCopy(2, ir.TInt), ir.TInt)),
		},
	)
	got := emit(t, trivialMainFn(), zero, caller)
	for _, want := range []string{"%0 = call i64 @zero()", "%1 = add i64 %x, %0"} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0EmitsKnownErrTypedValueCall(t *testing.T) {
	t.Parallel()
	helper := makeFn(fnSpec{
		name: "helper",
		retT: ir.TInt,
		src:  useRV(intConst(42)),
	})
	caller := makeMultiInstrFn(
		"caller",
		ir.TInt,
		nil,
		[]paramSpec{{name: "r", ty: ir.TInt}},
		[]mir.Instr{
			&mir.CallInstr{
				Dest:   &mir.Place{Local: 1},
				Callee: &mir.FnRef{Symbol: "helper", Type: ir.ErrTypeVal},
			},
			assign(0, useRV(paramCopy(1, ir.TInt))),
		},
	)
	got := emit(t, trivialMainFn(), helper, caller)
	for _, want := range []string{
		"define i64 @helper()",
		"define i64 @caller()",
		"%0 = call i64 @helper()",
		"ret i64 %0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "declare i64 @helper") {
		t.Fatalf("known helper should not be redeclared:\n%s", got)
	}
}

func TestStage0EmitsKnownCallWithUserNamedParam(t *testing.T) {
	t.Parallel()
	node := &ir.NamedType{Name: "AstNode"}
	helper := makeFn(fnSpec{
		name:   "nodeKind",
		retT:   ir.TInt,
		params: []paramSpec{{name: "node", ty: node}},
		src:    useRV(intConst(7)),
	})
	caller := makeMultiInstrFn(
		"caller",
		ir.TInt,
		[]paramSpec{{name: "node", ty: node}},
		[]paramSpec{{name: "r", ty: ir.TInt}},
		[]mir.Instr{
			callInstr(2, "nodeKind", fnTy(ir.TInt, node), paramCopy(1, node)),
			assign(0, useRV(paramCopy(2, ir.TInt))),
		},
	)
	got := emit(t, trivialMainFn(), helper, caller)
	for _, want := range []string{
		"define i64 @nodeKind(ptr %node)",
		"define i64 @caller(ptr %node)",
		"%0 = call i64 @nodeKind(ptr %node)",
		"ret i64 %0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0InlinesConstGlobalRef(t *testing.T) {
	t.Parallel()
	checkName := &ir.NamedType{Name: "CheckName"}
	initFn := makeFn(fnSpec{
		name: "_init_CheckLockfile",
		retT: ir.TString,
		src:  useRV(stringConst("lockfile")),
	})
	caller := makeFn(fnSpec{
		name: "checkName",
		retT: checkName,
		src:  &mir.GlobalRefRV{Name: "CheckLockfile", T: checkName},
	})
	module := moduleWith(trivialMainFn(), caller)
	module.Globals = []*mir.Global{
		{Name: "CheckLockfile", Type: checkName, Init: initFn},
	}
	gotBytes, err := EmitMIR(module, llvmabi.Options{PackageName: "main"})
	if err != nil {
		t.Fatalf("EmitMIR: %v", err)
	}
	got := string(gotBytes)
	for _, want := range []string{
		"@.str.0 = private unnamed_addr constant [9 x i8] c\"lockfile\\00\"",
		"define ptr @checkName()",
		"ret ptr @.str.0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0P21DeclaresUnknownDirectCall(t *testing.T) {
	t.Parallel()
	caller := makeMultiInstrFn(
		"wrapper",
		ir.TString,
		nil,
		nil,
		[]mir.Instr{
			callInstr(0, "external_string_leaf", fnTy(ir.TString)),
		},
	)
	got := emit(t, trivialMainFn(), caller)
	for _, want := range []string{
		"declare ptr @external_string_leaf()",
		"define ptr @wrapper()",
		"%0 = call ptr @external_string_leaf()",
		"ret ptr %0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0P21DeclaresUnknownDirectCallWithArgs(t *testing.T) {
	t.Parallel()
	caller := makeMultiInstrFn(
		"wrap_key",
		ir.TString,
		[]paramSpec{{name: "owner", ty: ir.TString}, {name: "name", ty: ir.TString}},
		nil,
		[]mir.Instr{
			callInstr(0, "external_key", fnTy(ir.TString, ir.TString, ir.TString), paramCopy(1, ir.TString), paramCopy(2, ir.TString)),
		},
	)
	got := emit(t, trivialMainFn(), caller)
	for _, want := range []string{
		"declare ptr @external_key(ptr, ptr)",
		"define ptr @wrap_key(ptr %owner, ptr %name)",
		"%0 = call ptr @external_key(ptr %owner, ptr %name)",
		"ret ptr %0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0P21OpaqueNamedArgWithErrTypedCallee(t *testing.T) {
	t.Parallel()
	snapshot := &ir.NamedType{Name: "Snapshot"}
	caller := makeMultiInstrFn(
		"WriteSnapshot",
		ir.TString,
		[]paramSpec{{name: "path", ty: ir.TString}, {name: "snap", ty: snapshot}},
		nil,
		[]mir.Instr{
			&mir.CallInstr{
				Dest:   &mir.Place{Local: 0},
				Callee: &mir.FnRef{Symbol: "runtime.cihost.WriteSnapshotHost", Type: ir.ErrTypeVal},
				Args:   []mir.Operand{paramCopy(1, ir.TString), paramCopy(2, snapshot)},
			},
		},
	)
	got := emit(t, trivialMainFn(), caller)
	for _, want := range []string{
		"declare ptr @runtime.cihost.WriteSnapshotHost(ptr, ptr)",
		"define ptr @WriteSnapshot(ptr %path, ptr %snap)",
		"%0 = call ptr @runtime.cihost.WriteSnapshotHost(ptr %path, ptr %snap)",
		"ret ptr %0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0P23OpaqueNamedIntermediateCallChain(t *testing.T) {
	t.Parallel()
	formatter := &ir.NamedType{Name: "OstyAstFormatter"}
	node := &ir.NamedType{Name: "AstNode"}
	fn := makeMultiInstrFn(
		"ostyAstBlock",
		ir.TString,
		[]paramSpec{{name: "f", ty: formatter}, {name: "idx", ty: ir.TInt}},
		[]paramSpec{{name: "node", ty: node}},
		[]mir.Instr{
			&mir.CallInstr{
				Dest:   &mir.Place{Local: 3},
				Callee: &mir.FnRef{Symbol: "ostyAstNode", Type: ir.ErrTypeVal},
				Args:   []mir.Operand{paramCopy(1, formatter), paramCopy(2, ir.TInt)},
			},
			&mir.CallInstr{
				Dest:   &mir.Place{Local: 0},
				Callee: &mir.FnRef{Symbol: "ostyAstBlockFromNode", Type: ir.ErrTypeVal},
				Args:   []mir.Operand{paramCopy(1, formatter), paramCopy(3, node)},
			},
		},
	)
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"declare ptr @ostyAstNode(ptr, i64)",
		"declare ptr @ostyAstBlockFromNode(ptr, ptr)",
		"define ptr @ostyAstBlock(ptr %f, i64 %idx)",
		"%0 = call ptr @ostyAstNode(ptr %f, i64 %idx)",
		"%1 = call ptr @ostyAstBlockFromNode(ptr %f, ptr %0)",
		"ret ptr %1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0P22StringConcatIntrinsicChain(t *testing.T) {
	t.Parallel()
	fn := makeMultiInstrFn(
		"checkFnKey",
		ir.TString,
		[]paramSpec{{name: "name", ty: ir.TString}, {name: "owner", ty: ir.TString}},
		nil,
		[]mir.Instr{
			&mir.IntrinsicInstr{
				Dest: &mir.Place{Local: 0},
				Kind: mir.IntrinsicStringConcat,
				Args: []mir.Operand{
					paramCopy(2, ir.TString),
					stringConst("\u001f"),
					paramCopy(1, ir.TString),
				},
			},
		},
	)
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"declare ptr @osty_rt_strings_Concat(ptr, ptr)",
		"%0 = call ptr @osty_rt_strings_Concat(ptr %owner, ptr @.str.0)",
		"%1 = call ptr @osty_rt_strings_Concat(ptr %0, ptr %name)",
		"ret ptr %1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0P22StringSplitJoinIntrinsicFlow(t *testing.T) {
	t.Parallel()
	listString := &ir.NamedType{Name: "List", Args: []ir.Type{ir.TString}, Builtin: true}
	fn := makeMultiInstrFn(
		"stripUnderscores",
		ir.TString,
		[]paramSpec{{name: "text", ty: ir.TString}},
		[]paramSpec{{name: "parts", ty: listString}},
		[]mir.Instr{
			&mir.IntrinsicInstr{
				Dest: &mir.Place{Local: 2},
				Kind: mir.IntrinsicStringSplit,
				Args: []mir.Operand{paramCopy(1, ir.TString), stringConst("_")},
			},
			&mir.IntrinsicInstr{
				Dest: &mir.Place{Local: 0},
				Kind: mir.IntrinsicStringJoin,
				Args: []mir.Operand{paramCopy(2, listString), stringConst("")},
			},
		},
	)
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"declare ptr @osty_rt_strings_Split(ptr, ptr)",
		"declare ptr @osty_rt_strings_Join(ptr, ptr)",
		"%0 = call ptr @osty_rt_strings_Split(ptr %text, ptr @.str.0)",
		"%1 = call ptr @osty_rt_strings_Join(ptr %0, ptr @.str.1)",
		"ret ptr %1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0P22StringListLiteralJoinFlow(t *testing.T) {
	t.Parallel()
	listString := &ir.NamedType{Name: "List", Args: []ir.Type{ir.TString}, Builtin: true}
	fn := makeMultiInstrFn(
		"joinLiteral",
		ir.TString,
		nil,
		[]paramSpec{{name: "parts", ty: listString}},
		[]mir.Instr{
			assign(1, &mir.AggregateRV{
				Kind: mir.AggList,
				T:    listString,
				Fields: []mir.Operand{
					stringConst("a"),
					stringConst("b"),
				},
			}),
			&mir.IntrinsicInstr{
				Dest: &mir.Place{Local: 0},
				Kind: mir.IntrinsicStringJoin,
				Args: []mir.Operand{paramCopy(1, listString), stringConst(",")},
			},
		},
	)
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"declare ptr @osty_rt_list_new()",
		"declare void @osty_rt_list_push_string(ptr, ptr)",
		"%0 = call ptr @osty_rt_list_new()",
		"call void @osty_rt_list_push_string(ptr %0, ptr @.str.0)",
		"call void @osty_rt_list_push_string(ptr %0, ptr @.str.1)",
		"%1 = call ptr @osty_rt_strings_Join(ptr %0, ptr @.str.2)",
		"ret ptr %1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0P23PayloadlessEnumFeedsCall(t *testing.T) {
	t.Parallel()
	kind := &ir.NamedType{Name: "FixtureKind"}
	fn := makeMultiInstrFn(
		"fixtureName",
		ir.TString,
		nil,
		[]paramSpec{{name: "kind", ty: kind}},
		[]mir.Instr{
			assign(1, &mir.AggregateRV{
				Kind:       mir.AggEnumVariant,
				T:          kind,
				VariantIdx: 1,
				VariantTag: "FixtureSource",
			}),
			&mir.CallInstr{
				Dest:   &mir.Place{Local: 0},
				Callee: &mir.FnRef{Symbol: "externalFixtureName", Type: ir.ErrTypeVal},
				Args:   []mir.Operand{paramCopy(1, kind)},
			},
		},
	)
	module := moduleWith(trivialMainFn(), fn)
	module.Layouts.Enums["FixtureKind"] = &mir.EnumLayout{
		Name:         "FixtureKind",
		Discriminant: ir.TInt,
		Variants: []mir.VariantLayout{
			{Index: 0, Name: "FixtureManual"},
			{Index: 1, Name: "FixtureSource"},
		},
	}
	gotBytes, err := EmitMIR(module, llvmabi.Options{PackageName: "main"})
	if err != nil {
		t.Fatalf("EmitMIR: %v", err)
	}
	got := string(gotBytes)
	for _, want := range []string{
		"declare ptr @externalFixtureName(i64)",
		"%0 = call ptr @externalFixtureName(i64 1)",
		"ret ptr %0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0PayloadlessEnumComparesFnConstVariant(t *testing.T) {
	t.Parallel()
	kind := &ir.NamedType{Name: "FixtureKind"}
	fn := makeFn(fnSpec{
		name:   "isSourceFixture",
		retT:   ir.TBool,
		params: []paramSpec{{name: "kind", ty: kind}},
		src: binaryRV(
			mir.BinEq,
			paramCopy(1, kind),
			&mir.ConstOp{Const: &mir.FnConst{Symbol: "FixtureKind__FixtureSource", T: ir.ErrTypeVal}, T: ir.ErrTypeVal},
			ir.TBool,
		),
	})
	module := moduleWith(trivialMainFn(), fn)
	module.Layouts.Enums["FixtureKind"] = &mir.EnumLayout{
		Name:         "FixtureKind",
		Discriminant: ir.TInt,
		Variants: []mir.VariantLayout{
			{Index: 0, Name: "FixtureManual"},
			{Index: 1, Name: "FixtureSource"},
		},
	}
	gotBytes, err := EmitMIR(module, llvmabi.Options{PackageName: "main"})
	if err != nil {
		t.Fatalf("EmitMIR: %v", err)
	}
	got := string(gotBytes)
	for _, want := range []string{
		"define i1 @isSourceFixture(i64 %kind)",
		"%0 = icmp eq i64 %kind, 1",
		"ret i1 %0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0AggregateResolvesOnbFnConstAndEmptyDefaults(t *testing.T) {
	t.Parallel()
	kind := &ir.NamedType{Name: "OnbInstrKind"}
	cond := &ir.NamedType{Name: "OnbCond"}
	instrTy := &ir.NamedType{Name: "OnbInstr"}
	fn := &mir.Function{
		Name:        "onbInstrAddReg",
		Params:      []mir.LocalID{1, 2, 3},
		ReturnType:  instrTy,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: instrTy, IsReturn: true},
			{ID: 1, Name: "dst", Type: ir.TString, IsParam: true},
			{ID: 2, Name: "lhs", Type: ir.TString, IsParam: true},
			{ID: 3, Name: "rhs", Type: ir.TString, IsParam: true},
			{ID: 4, Name: "kind", Type: ir.ErrTypeVal},
			{ID: 5, Name: "empty", Type: ir.ErrTypeVal},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{{
			ID: 0,
			Instrs: []mir.Instr{
				assign(4, useRV(&mir.ConstOp{Const: &mir.FnConst{Symbol: "OnbInstrKind", T: ir.ErrTypeVal}, T: ir.ErrTypeVal})),
				&mir.CallInstr{Dest: &mir.Place{Local: 5}, Callee: &mir.FnRef{Symbol: "emptyOnbInstr", Type: ir.ErrTypeVal}},
				assign(0, &mir.AggregateRV{
					Kind: mir.AggStruct,
					T:    instrTy,
					Fields: []mir.Operand{
						&mir.CopyOp{Place: mir.Place{Local: 4, Projections: []mir.Projection{&mir.FieldProj{Index: 0, Type: ir.ErrTypeVal}}}, T: ir.ErrTypeVal},
						paramCopy(1, ir.TString),
						&mir.CopyOp{Place: mir.Place{Local: 5, Projections: []mir.Projection{&mir.FieldProj{Index: 2, Type: ir.TString}}}, T: ir.TString},
						paramCopy(2, ir.TString),
						paramCopy(3, ir.TString),
						&mir.CopyOp{Place: mir.Place{Local: 5, Projections: []mir.Projection{&mir.FieldProj{Index: 5, Type: ir.TString}}}, T: ir.TString},
						&mir.CopyOp{Place: mir.Place{Local: 5, Projections: []mir.Projection{&mir.FieldProj{Index: 6, Type: ir.TInt}}}, T: ir.TInt},
						&mir.CopyOp{Place: mir.Place{Local: 5, Projections: []mir.Projection{&mir.FieldProj{Index: 7, Type: ir.TInt}}}, T: ir.TInt},
						&mir.CopyOp{Place: mir.Place{Local: 5, Projections: []mir.Projection{&mir.FieldProj{Index: 8, Type: cond}}}, T: cond},
						&mir.CopyOp{Place: mir.Place{Local: 5, Projections: []mir.Projection{&mir.FieldProj{Index: 9, Type: ir.TInt}}}, T: ir.TInt},
						&mir.CopyOp{Place: mir.Place{Local: 5, Projections: []mir.Projection{&mir.FieldProj{Index: 10, Type: ir.TString}}}, T: ir.TString},
						&mir.CopyOp{Place: mir.Place{Local: 5, Projections: []mir.Projection{&mir.FieldProj{Index: 11, Type: ir.TString}}}, T: ir.TString},
					},
				}),
			},
			Term: &mir.ReturnTerm{},
		}},
	}
	module := moduleWith(trivialMainFn(), fn)
	module.Layouts.Enums["OnbInstrKind"] = &mir.EnumLayout{
		Name: "OnbInstrKind",
		Variants: []mir.VariantLayout{
			{Index: 0, Name: "OnbInstrInvalid"},
			{Index: 1, Name: "OnbInstrBrk"},
			{Index: 2, Name: "OnbInstrAddReg"},
		},
	}
	module.Layouts.Enums["OnbCond"] = &mir.EnumLayout{
		Name:     "OnbCond",
		Variants: []mir.VariantLayout{{Index: 0, Name: "OnbCondEq"}},
	}
	module.Layouts.Structs["OnbInstr"] = &mir.StructLayout{
		Name: "OnbInstr",
		Fields: []mir.FieldLayout{
			{Index: 0, Name: "kind", Type: kind},
			{Index: 1, Name: "dst", Type: ir.TString},
			{Index: 2, Name: "src", Type: ir.TString},
			{Index: 3, Name: "lhs", Type: ir.TString},
			{Index: 4, Name: "rhs", Type: ir.TString},
			{Index: 5, Name: "base", Type: ir.TString},
			{Index: 6, Name: "imm", Type: ir.TInt},
			{Index: 7, Name: "offset", Type: ir.TInt},
			{Index: 8, Name: "cond", Type: cond},
			{Index: 9, Name: "targetBlock", Type: ir.TInt},
			{Index: 10, Name: "symbol", Type: ir.TString},
			{Index: 11, Name: "label", Type: ir.TString},
		},
	}
	gotBytes, err := EmitMIR(module, llvmabi.Options{PackageName: "main"})
	if err != nil {
		t.Fatalf("EmitMIR: %v", err)
	}
	got := string(gotBytes)
	for _, want := range []string{
		"define %OnbInstr @onbInstrAddReg(ptr %dst, ptr %lhs, ptr %rhs)",
		"insertvalue %OnbInstr poison, i64 2, 0",
		"insertvalue %OnbInstr %0, ptr %dst, 1",
		"insertvalue %OnbInstr %8, i64 -1, 9",
		"ret %OnbInstr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "call %OnbInstr @emptyOnbInstr") || strings.Contains(got, "call ptr @emptyOnbInstr") {
		t.Fatalf("emptyOnbInstr spread should be resolved without a runtime call:\n%s", got)
	}
}

func TestStage0AggregateUsesErrTypedEnumPayloadCall(t *testing.T) {
	t.Parallel()
	tomlKind := &ir.NamedType{Name: "TomlKind"}
	tomlValue := &ir.NamedType{Name: "TomlValue"}
	fn := &mir.Function{
		Name:        "tomlValueStr",
		Params:      []mir.LocalID{1, 2},
		ReturnType:  tomlValue,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: tomlValue, IsReturn: true},
			{ID: 1, Name: "s", Type: ir.TString, IsParam: true},
			{ID: 2, Name: "line", Type: ir.TInt, IsParam: true},
			{ID: 3, Name: "kind", Type: ir.ErrTypeVal},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{{
			ID: 0,
			Instrs: []mir.Instr{
				&mir.CallInstr{
					Dest:   &mir.Place{Local: 3},
					Callee: &mir.FnRef{Symbol: "KStr", Type: ir.ErrTypeVal},
					Args: []mir.Operand{
						&mir.ConstOp{Const: &mir.FnConst{Symbol: "KStr", T: ir.ErrTypeVal}, T: ir.ErrTypeVal},
						paramCopy(1, ir.TString),
					},
				},
				assign(0, &mir.AggregateRV{
					Kind: mir.AggStruct,
					T:    tomlValue,
					Fields: []mir.Operand{
						localCopy(3, ir.ErrTypeVal),
						paramCopy(2, ir.TInt),
					},
				}),
			},
			Term: &mir.ReturnTerm{},
		}},
	}
	module := moduleWith(trivialMainFn(), fn)
	module.Layouts.Structs["TomlValue"] = &mir.StructLayout{
		Name: "TomlValue",
		Fields: []mir.FieldLayout{
			{Index: 0, Name: "kind", Type: tomlKind},
			{Index: 1, Name: "line", Type: ir.TInt},
		},
	}
	gotBytes, err := EmitMIR(module, llvmabi.Options{PackageName: "main"})
	if err != nil {
		t.Fatalf("EmitMIR: %v", err)
	}
	got := string(gotBytes)
	for _, want := range []string{
		"declare ptr @KStr(ptr)",
		"define %TomlValue @tomlValueStr(ptr %s, i64 %line)",
		"%0 = call ptr @KStr(ptr %s)",
		"insertvalue %TomlValue poison, ptr %0, 0",
		"insertvalue %TomlValue %1, i64 %line, 1",
		"ret %TomlValue %2",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "FnConst") {
		t.Fatalf("FnConst metadata argument should not be emitted:\n%s", got)
	}
}

func TestStage0TreatsErrTypedIntConstAsIntLiteral(t *testing.T) {
	t.Parallel()
	fn := makeFn(fnSpec{
		name: "fallbackIndex",
		retT: ir.TInt,
		src:  useRV(&mir.ConstOp{Const: &mir.IntConst{Value: -1, T: ir.ErrTypeVal}, T: ir.ErrTypeVal}),
	})
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"define i64 @fallbackIndex()",
		"ret i64 -1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0GenericCFGSkipsUnreachableErrorOnlyLocals(t *testing.T) {
	t.Parallel()
	fn := &mir.Function{
		Name:        "returnBeforeErrorSink",
		ReturnType:  ir.TBool,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: ir.TBool, IsReturn: true},
			{ID: 1, Name: "scratch", Type: ir.ErrTypeVal},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{
			{ID: 0, Instrs: []mir.Instr{assign(0, useRV(boolConst(true)))}, Term: &mir.GotoTerm{Target: 1}},
			{ID: 1, Term: &mir.ReturnTerm{}},
			{
				ID: 2,
				Instrs: []mir.Instr{
					assign(1, useRV(&mir.ConstOp{Const: &mir.FnConst{Symbol: "unreachableDebug", T: ir.ErrTypeVal}, T: ir.ErrTypeVal})),
				},
				Term: &mir.UnreachableTerm{},
			},
		},
	}
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"define i1 @returnBeforeErrorSink()",
		"br label %bb.1",
		"bb.1:",
		"ret i1 %0",
		"bb.2:",
		"unreachable",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "unreachableDebug") {
		t.Fatalf("unreachable-only ErrType instruction should not be emitted:\n%s", got)
	}
}

func TestStage0GenericCFGSynthesizesListAccumulatorReturn(t *testing.T) {
	t.Parallel()
	listString := &ir.NamedType{Name: "List", Builtin: true, Args: []ir.Type{ir.TString}}
	fn := &mir.Function{
		Name:        "cloneStrings",
		Params:      []mir.LocalID{1},
		ReturnType:  listString,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: listString, IsReturn: true},
			{ID: 1, Name: "src", Type: listString, IsParam: true},
			{ID: 2, Name: "out", Type: listString},
			{ID: 3, Name: "iter", Type: listString},
			{ID: 4, Name: "len", Type: ir.TInt},
			{ID: 5, Name: "idx", Type: ir.TInt},
			{ID: 6, Name: "keepGoing", Type: ir.TBool},
			{ID: 7, Name: "elem", Type: ir.TString},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{
			{
				ID: 0,
				Instrs: []mir.Instr{
					assign(2, &mir.AggregateRV{Kind: mir.AggList, T: listString}),
					assign(3, useRV(paramCopy(1, listString))),
					assign(4, &mir.LenRV{Place: mir.Place{Local: 3}, T: ir.TInt}),
					assign(5, useRV(intConst(0))),
				},
				Term: &mir.GotoTerm{Target: 1},
			},
			{
				ID:     1,
				Instrs: []mir.Instr{assign(6, binaryRV(mir.BinLt, localCopy(5, ir.TInt), localCopy(4, ir.TInt), ir.TBool))},
				Term:   &mir.BranchTerm{Cond: localCopy(6, ir.TBool), Then: 2, Else: 4},
			},
			{
				ID: 2,
				Instrs: []mir.Instr{
					assign(7, useRV(&mir.CopyOp{
						Place: mir.Place{Local: 3, Projections: []mir.Projection{&mir.IndexProj{Index: localCopy(5, ir.TInt), ElemType: ir.TString}}},
						T:     ir.TString,
					})),
					&mir.IntrinsicInstr{Kind: mir.IntrinsicListPush, Args: []mir.Operand{localCopy(2, listString), localCopy(7, ir.TString)}},
				},
				Term: &mir.GotoTerm{Target: 3},
			},
			{
				ID:     3,
				Instrs: []mir.Instr{assign(5, binaryRV(mir.BinAdd, localCopy(5, ir.TInt), intConst(1), ir.TInt))},
				Term:   &mir.GotoTerm{Target: 1},
			},
			{
				ID:     4,
				Instrs: []mir.Instr{&mir.StorageDeadInstr{Local: 2}},
				Term:   &mir.UnreachableTerm{},
			},
		},
	}
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"define ptr @cloneStrings(ptr %src)",
		"call ptr @osty_rt_list_new()",
		"call ptr @osty_rt_list_get_string",
		"call void @osty_rt_list_push_string",
		"bb.4:",
		"ret ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0GenericCFGSynthesizesCallMutatedListAccumulatorReturn(t *testing.T) {
	t.Parallel()
	arenaTy := &ir.NamedType{Name: "AstArena"}
	nodeTy := &ir.NamedType{Name: "AstNode"}
	listString := &ir.NamedType{Name: "List", Builtin: true, Args: []ir.Type{ir.TString}}
	fn := &mir.Function{
		Name:        "collectTaintTags",
		Params:      []mir.LocalID{1, 2},
		ReturnType:  listString,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: listString, IsReturn: true},
			{ID: 1, Name: "arena", Type: arenaTy, IsParam: true},
			{ID: 2, Name: "node", Type: nodeTy, IsParam: true},
			{ID: 3, Name: "out", Type: listString},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{
			{
				ID: 0,
				Instrs: []mir.Instr{
					assign(3, &mir.AggregateRV{Kind: mir.AggList, T: listString}),
					&mir.CallInstr{
						Callee: &mir.FnRef{Symbol: "taintCollectFromNode", Type: ir.ErrTypeVal},
						Args:   []mir.Operand{paramCopy(1, arenaTy), paramCopy(2, nodeTy), localCopy(3, listString)},
					},
				},
				Term: &mir.GotoTerm{Target: 1},
			},
			{
				ID:     1,
				Instrs: []mir.Instr{&mir.StorageDeadInstr{Local: 3}},
				Term:   &mir.UnreachableTerm{},
			},
		},
	}
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"declare void @taintCollectFromNode(ptr, ptr, ptr)",
		"define ptr @collectTaintTags(ptr %arena, ptr %node)",
		"call ptr @osty_rt_list_new()",
		"call void @taintCollectFromNode(ptr %arena, ptr %node, ptr %",
		"bb.1:",
		"load ptr, ptr %out.slot",
		"ret ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0GenericCFGSynthesizesStringJoinReturn(t *testing.T) {
	t.Parallel()
	listString := &ir.NamedType{Name: "List", Builtin: true, Args: []ir.Type{ir.TString}}
	fn := &mir.Function{
		Name:        "joinLines",
		Params:      []mir.LocalID{1},
		ReturnType:  ir.TString,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: ir.TString, IsReturn: true},
			{ID: 1, Name: "lines", Type: listString, IsParam: true},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{
			{ID: 0, Term: &mir.GotoTerm{Target: 1}},
			{
				ID: 1,
				Instrs: []mir.Instr{
					&mir.StorageDeadInstr{Local: 0},
					&mir.IntrinsicInstr{
						Kind: mir.IntrinsicStringJoin,
						Args: []mir.Operand{paramCopy(1, listString), stringConst("\n")},
					},
				},
				Term: &mir.UnreachableTerm{},
			},
		},
	}
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"declare ptr @osty_rt_strings_Join(ptr, ptr)",
		"define ptr @joinLines(ptr %lines)",
		"br label %bb.1",
		"bb.1:",
		"%0 = call ptr @osty_rt_strings_Join(ptr %lines, ptr @.str.0)",
		"ret ptr %0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "unreachable") {
		t.Fatalf("string_join sink should become the returned value:\n%s", got)
	}
}

func TestStage0GenericCFGSynthesizesStringAccumulatorReturn(t *testing.T) {
	t.Parallel()
	listString := &ir.NamedType{Name: "List", Builtin: true, Args: []ir.Type{ir.TString}}
	fn := &mir.Function{
		Name:        "monomorphDedupeKey",
		Params:      []mir.LocalID{1, 2, 3},
		ReturnType:  ir.TString,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: ir.TString, IsReturn: true},
			{ID: 1, Name: "fnName", Type: ir.TString, IsParam: true},
			{ID: 2, Name: "pkg", Type: ir.TString, IsParam: true},
			{ID: 3, Name: "typeArgCodes", Type: listString, IsParam: true},
			{ID: 4, Name: "key", Type: ir.TString},
			{ID: 5, Name: "init", Type: ir.TString},
			{ID: 6, Name: "_iter", Type: listString},
			{ID: 7, Name: "_len", Type: ir.TInt},
			{ID: 8, Name: "_idx", Type: ir.TInt},
			{ID: 9, Name: "keepGoing", Type: ir.TBool},
			{ID: 10, Name: "_elem", Type: ir.TString},
			{ID: 11, Name: "next", Type: ir.TString},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{
			{
				ID: 0,
				Instrs: []mir.Instr{
					&mir.IntrinsicInstr{Dest: &mir.Place{Local: 5}, Kind: mir.IntrinsicStringConcat, Args: []mir.Operand{paramCopy(2, ir.TString), stringConst("::"), paramCopy(1, ir.TString)}},
					assign(4, useRV(localCopy(5, ir.TString))),
					assign(6, useRV(paramCopy(3, listString))),
					assign(7, &mir.LenRV{Place: mir.Place{Local: 6}, T: ir.TInt}),
					assign(8, useRV(intConst(0))),
				},
				Term: &mir.GotoTerm{Target: 1},
			},
			{
				ID:     1,
				Instrs: []mir.Instr{assign(9, binaryRV(mir.BinLt, localCopy(8, ir.TInt), localCopy(7, ir.TInt), ir.TBool))},
				Term:   &mir.BranchTerm{Cond: localCopy(9, ir.TBool), Then: 2, Else: 4},
			},
			{
				ID: 2,
				Instrs: []mir.Instr{
					assign(10, useRV(&mir.CopyOp{
						Place: mir.Place{Local: 6, Projections: []mir.Projection{&mir.IndexProj{Index: localCopy(8, ir.TInt), ElemType: ir.TString}}},
						T:     ir.TString,
					})),
					&mir.IntrinsicInstr{Dest: &mir.Place{Local: 11}, Kind: mir.IntrinsicStringConcat, Args: []mir.Operand{localCopy(4, ir.TString), stringConst(":"), localCopy(10, ir.TString)}},
					assign(4, useRV(localCopy(11, ir.TString))),
				},
				Term: &mir.GotoTerm{Target: 3},
			},
			{
				ID:     3,
				Instrs: []mir.Instr{assign(8, binaryRV(mir.BinAdd, localCopy(8, ir.TInt), intConst(1), ir.TInt))},
				Term:   &mir.GotoTerm{Target: 1},
			},
			{
				ID:     4,
				Instrs: []mir.Instr{&mir.StorageDeadInstr{Local: 4}},
				Term:   &mir.UnreachableTerm{},
			},
		},
	}
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"define ptr @monomorphDedupeKey(ptr %fnName, ptr %pkg, ptr %typeArgCodes)",
		"call ptr @osty_rt_strings_Concat",
		"call ptr @osty_rt_list_get_string",
		"bb.4:",
		"load ptr, ptr %key.slot",
		"ret ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0GenericCFGSynthesizesScalarAccumulatorReturn(t *testing.T) {
	t.Parallel()
	listString := &ir.NamedType{Name: "List", Builtin: true, Args: []ir.Type{ir.TString}}
	fn := &mir.Function{
		Name:        "countMatchingStrings",
		Params:      []mir.LocalID{1, 2},
		ReturnType:  ir.TInt,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: ir.TInt, IsReturn: true},
			{ID: 1, Name: "xs", Type: listString, IsParam: true},
			{ID: 2, Name: "kind", Type: ir.TString, IsParam: true},
			{ID: 3, Name: "n", Type: ir.TInt},
			{ID: 4, Name: "iter", Type: listString},
			{ID: 5, Name: "len", Type: ir.TInt},
			{ID: 6, Name: "idx", Type: ir.TInt},
			{ID: 7, Name: "keepGoing", Type: ir.TBool},
			{ID: 8, Name: "elem", Type: ir.TString},
			{ID: 9, Name: "matched", Type: ir.TBool},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{
			{
				ID: 0,
				Instrs: []mir.Instr{
					assign(3, useRV(intConst(0))),
					assign(4, useRV(paramCopy(1, listString))),
					assign(5, &mir.LenRV{Place: mir.Place{Local: 4}, T: ir.TInt}),
					assign(6, useRV(intConst(0))),
				},
				Term: &mir.GotoTerm{Target: 1},
			},
			{
				ID:     1,
				Instrs: []mir.Instr{assign(7, binaryRV(mir.BinLt, localCopy(6, ir.TInt), localCopy(5, ir.TInt), ir.TBool))},
				Term:   &mir.BranchTerm{Cond: localCopy(7, ir.TBool), Then: 2, Else: 4},
			},
			{
				ID: 2,
				Instrs: []mir.Instr{
					assign(8, useRV(&mir.CopyOp{
						Place: mir.Place{Local: 4, Projections: []mir.Projection{&mir.IndexProj{Index: localCopy(6, ir.TInt), ElemType: ir.TString}}},
						T:     ir.TString,
					})),
					assign(9, binaryRV(mir.BinEq, localCopy(8, ir.TString), localCopy(2, ir.TString), ir.TBool)),
				},
				Term: &mir.BranchTerm{Cond: localCopy(9, ir.TBool), Then: 3, Else: 5},
			},
			{
				ID:     3,
				Instrs: []mir.Instr{assign(3, binaryRV(mir.BinAdd, localCopy(3, ir.TInt), intConst(1), ir.TInt))},
				Term:   &mir.GotoTerm{Target: 5},
			},
			{
				ID:     4,
				Instrs: []mir.Instr{&mir.StorageDeadInstr{Local: 3}},
				Term:   &mir.UnreachableTerm{},
			},
			{
				ID:     5,
				Instrs: []mir.Instr{assign(6, binaryRV(mir.BinAdd, localCopy(6, ir.TInt), intConst(1), ir.TInt))},
				Term:   &mir.GotoTerm{Target: 1},
			},
		},
	}
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"define i64 @countMatchingStrings(ptr %xs, ptr %kind)",
		"call ptr @osty_rt_list_get_string",
		"call i1 @osty_rt_strings_Equal",
		"bb.4:",
		"load i64, ptr %n.slot",
		"ret i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "load i64, ptr %idx.slot\n  ret i64") {
		t.Fatalf("loop index must not be selected as the synthetic return:\n%s", got)
	}
}

func TestStage0GenericCFGSynthesizesScalarAccumulatorReturnWhenElementUnused(t *testing.T) {
	t.Parallel()
	treeTy := &ir.NamedType{Name: "FrontParseTree"}
	nodeTy := &ir.NamedType{Name: "FrontParseNode"}
	listNode := &ir.NamedType{Name: "List", Builtin: true, Args: []ir.Type{nodeTy}}
	fn := &mir.Function{
		Name:        "frontParseTreeNodeCount",
		Params:      []mir.LocalID{1},
		ReturnType:  ir.TInt,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: ir.TInt, IsReturn: true},
			{ID: 1, Name: "tree", Type: treeTy, IsParam: true},
			{ID: 2, Name: "count", Type: ir.TInt},
			{ID: 3, Name: "_iter", Type: listNode},
			{ID: 4, Name: "_len", Type: ir.TInt},
			{ID: 5, Name: "_idx", Type: ir.TInt},
			{ID: 6, Name: "keepGoing", Type: ir.TBool},
			{ID: 7, Name: "_elem", Type: nodeTy},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{
			{
				ID: 0,
				Instrs: []mir.Instr{
					&mir.StorageLiveInstr{Local: 2},
					assign(2, useRV(intConst(0))),
					&mir.StorageLiveInstr{Local: 3},
					assign(3, useRV(&mir.CopyOp{
						Place: mir.Place{Local: 1, Projections: []mir.Projection{&mir.FieldProj{Index: 0, Name: "nodes", Type: listNode}}},
						T:     listNode,
					})),
					assign(4, &mir.LenRV{Place: mir.Place{Local: 3}, T: ir.TInt}),
					assign(5, useRV(intConst(0))),
				},
				Term: &mir.GotoTerm{Target: 1},
			},
			{
				ID:     1,
				Instrs: []mir.Instr{assign(6, binaryRV(mir.BinLt, localCopy(5, ir.TInt), localCopy(4, ir.TInt), ir.TBool))},
				Term:   &mir.BranchTerm{Cond: localCopy(6, ir.TBool), Then: 2, Else: 4},
			},
			{
				ID:     2,
				Instrs: []mir.Instr{assign(2, binaryRV(mir.BinAdd, localCopy(2, ir.TInt), intConst(1), ir.TInt))},
				Term:   &mir.GotoTerm{Target: 3},
			},
			{
				ID:     3,
				Instrs: []mir.Instr{assign(5, binaryRV(mir.BinAdd, localCopy(5, ir.TInt), intConst(1), ir.TInt))},
				Term:   &mir.GotoTerm{Target: 1},
			},
			{
				ID: 4,
				Instrs: []mir.Instr{
					&mir.StorageDeadInstr{Local: 7},
					&mir.StorageDeadInstr{Local: 5},
					&mir.StorageDeadInstr{Local: 3},
					&mir.StorageDeadInstr{Local: 2},
				},
				Term: &mir.UnreachableTerm{},
			},
		},
	}
	module := moduleWith(trivialMainFn(), fn)
	module.Layouts.Structs["FrontParseTree"] = &mir.StructLayout{
		Name: "FrontParseTree",
		Fields: []mir.FieldLayout{
			{Index: 0, Name: "nodes", Type: listNode},
		},
	}
	gotBytes, err := EmitMIR(module, llvmabi.Options{PackageName: "main"})
	if err != nil {
		t.Fatalf("EmitMIR: %v", err)
	}
	got := string(gotBytes)
	for _, want := range []string{
		"%FrontParseTree = type { ptr }",
		"define i64 @frontParseTreeNodeCount(ptr %tree)",
		"getelementptr inbounds %FrontParseTree",
		"call i64 @osty_rt_list_len(ptr %",
		"load i64, ptr %count.slot",
		"ret i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "load i64, ptr %_idx.slot\n  ret i64") {
		t.Fatalf("loop index must not be selected as the synthetic return:\n%s", got)
	}
}

func TestStage0GenericCFGSynthesizesOpaqueAccumulatorReturn(t *testing.T) {
	t.Parallel()
	fileTy := &ir.NamedType{Name: "AstFile"}
	reportTy := &ir.NamedType{Name: "SelfLintReport"}
	listInt := &ir.NamedType{Name: "List", Builtin: true, Args: []ir.Type{ir.TInt}}
	fn := &mir.Function{
		Name:        "selfLintAstCheckUnnecessaryWrap",
		Params:      []mir.LocalID{1, 2},
		ReturnType:  reportTy,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: reportTy, IsReturn: true},
			{ID: 1, Name: "file", Type: fileTy, IsParam: true},
			{ID: 2, Name: "report", Type: reportTy, IsParam: true},
			{ID: 3, Name: "out", Type: reportTy},
			{ID: 4, Name: "items", Type: listInt},
			{ID: 5, Name: "len", Type: ir.TInt},
			{ID: 6, Name: "idx", Type: ir.TInt},
			{ID: 7, Name: "keepGoing", Type: ir.TBool},
			{ID: 8, Name: "elem", Type: ir.TInt},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{
			{
				ID: 0,
				Instrs: []mir.Instr{
					assign(3, useRV(paramCopy(2, reportTy))),
					assign(4, &mir.AggregateRV{Kind: mir.AggList, T: listInt, Fields: []mir.Operand{intConst(1), intConst(2)}}),
					assign(5, &mir.LenRV{Place: mir.Place{Local: 4}, T: ir.TInt}),
					assign(6, useRV(intConst(0))),
				},
				Term: &mir.GotoTerm{Target: 1},
			},
			{
				ID:     1,
				Instrs: []mir.Instr{assign(7, binaryRV(mir.BinLt, localCopy(6, ir.TInt), localCopy(5, ir.TInt), ir.TBool))},
				Term:   &mir.BranchTerm{Cond: localCopy(7, ir.TBool), Then: 2, Else: 4},
			},
			{
				ID: 2,
				Instrs: []mir.Instr{
					assign(8, useRV(&mir.CopyOp{
						Place: mir.Place{Local: 4, Projections: []mir.Projection{&mir.IndexProj{Index: localCopy(6, ir.TInt), ElemType: ir.TInt}}},
						T:     ir.TInt,
					})),
					&mir.CallInstr{
						Dest:   &mir.Place{Local: 3},
						Callee: &mir.FnRef{Symbol: "selfLintUnnecessaryWrapDecl", Type: ir.ErrTypeVal},
						Args:   []mir.Operand{paramCopy(1, fileTy), localCopy(8, ir.TInt), localCopy(3, reportTy)},
					},
				},
				Term: &mir.GotoTerm{Target: 3},
			},
			{
				ID:     3,
				Instrs: []mir.Instr{assign(6, binaryRV(mir.BinAdd, localCopy(6, ir.TInt), intConst(1), ir.TInt))},
				Term:   &mir.GotoTerm{Target: 1},
			},
			{
				ID:     4,
				Instrs: []mir.Instr{&mir.StorageDeadInstr{Local: 3}},
				Term:   &mir.UnreachableTerm{},
			},
		},
	}
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"define ptr @selfLintAstCheckUnnecessaryWrap(ptr %file, ptr %report)",
		"call ptr @selfLintUnnecessaryWrapDecl(ptr %file, i64 %",
		"store ptr %",
		"bb.4:",
		"load ptr, ptr %out.slot",
		"ret ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "load ptr, ptr %report.slot") {
		t.Fatalf("param copy slot must not be selected as the synthetic return:\n%s", got)
	}
}

func TestStage0GenericCFGSynthesizesDiscardedCallReturn(t *testing.T) {
	t.Parallel()
	diagTy := &ir.NamedType{Name: "CheckDiagnostic"}
	codeTy := &ir.NamedType{Name: "CheckCode"}
	fn := &mir.Function{
		Name:        "diagNonExhaustiveMatch",
		Params:      []mir.LocalID{1, 2, 3},
		ReturnType:  diagTy,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: diagTy, IsReturn: true},
			{ID: 1, Name: "witness", Type: ir.TString, IsParam: true},
			{ID: 2, Name: "start", Type: ir.TInt, IsParam: true},
			{ID: 3, Name: "end", Type: ir.TInt, IsParam: true},
			{ID: 4, Name: "isEmpty", Type: ir.TBool},
			{ID: 5, Name: "msg", Type: ir.TString},
			{ID: 6, Name: "code", Type: codeTy},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{
			{
				ID:     0,
				Instrs: []mir.Instr{assign(4, binaryRV(mir.BinEq, paramCopy(1, ir.TString), stringConst(""), ir.TBool))},
				Term:   &mir.BranchTerm{Cond: localCopy(4, ir.TBool), Then: 1, Else: 2},
			},
			{
				ID:     1,
				Instrs: []mir.Instr{assign(5, useRV(stringConst("non-exhaustive match")))},
				Term:   &mir.GotoTerm{Target: 3},
			},
			{
				ID: 2,
				Instrs: []mir.Instr{
					&mir.IntrinsicInstr{
						Dest: &mir.Place{Local: 5},
						Kind: mir.IntrinsicStringConcat,
						Args: []mir.Operand{stringConst("non-exhaustive match: "), paramCopy(1, ir.TString)},
					},
				},
				Term: &mir.GotoTerm{Target: 3},
			},
			{
				ID: 3,
				Instrs: []mir.Instr{
					&mir.CallInstr{Dest: &mir.Place{Local: 6}, Callee: &mir.FnRef{Symbol: "checkCodeNonExhaustiveMatch", Type: fnTy(codeTy)}},
					&mir.CallInstr{
						Callee: &mir.FnRef{Symbol: "checkDiag", Type: ir.ErrTypeVal},
						Args:   []mir.Operand{localCopy(6, codeTy), localCopy(5, ir.TString), paramCopy(2, ir.TInt), paramCopy(3, ir.TInt)},
					},
				},
				Term: &mir.UnreachableTerm{},
			},
		},
	}
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"declare ptr @checkCodeNonExhaustiveMatch()",
		"declare ptr @checkDiag(ptr, ptr, i64, i64)",
		"define ptr @diagNonExhaustiveMatch(ptr %witness, i64 %start, i64 %end)",
		"call ptr @checkCodeNonExhaustiveMatch()",
		"call ptr @checkDiag(ptr %",
		"ret ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "bb.3:\n  unreachable") {
		t.Fatalf("discarded return call block must return the call result:\n%s", got)
	}
}

func TestStage0GenericCFGSynthesizesDiscardedIntCallReturn(t *testing.T) {
	t.Parallel()
	arenaTy := &ir.NamedType{Name: "CoreArena"}
	nodeTy := &ir.NamedType{Name: "CoreNode"}
	fn := &mir.Function{
		Name:        "coreArenaAddWrapper",
		Params:      []mir.LocalID{1, 2},
		ReturnType:  ir.TInt,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: ir.TInt, IsReturn: true},
			{ID: 1, Name: "arena", Type: arenaTy, IsParam: true},
			{ID: 2, Name: "node", Type: nodeTy, IsParam: true},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{
			{
				ID: 0,
				Instrs: []mir.Instr{
					&mir.CallInstr{
						Callee: &mir.FnRef{Symbol: "coreArenaAdd", Type: ir.ErrTypeVal},
						Args:   []mir.Operand{paramCopy(1, arenaTy), paramCopy(2, nodeTy)},
					},
				},
				Term: &mir.UnreachableTerm{},
			},
		},
	}
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"declare i64 @coreArenaAdd(ptr, ptr)",
		"define i64 @coreArenaAddWrapper(ptr %arena, ptr %node)",
		"%0 = call i64 @coreArenaAdd(ptr %arena, ptr %node)",
		"ret i64 %0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0ShortCircuitBoolReturnsDiscardedFallbackCall(t *testing.T) {
	t.Parallel()
	kindTy := &ir.NamedType{Name: "FrontTypeKind"}
	fn := &mir.Function{
		Name:        "frontTypeIsNumeric",
		Params:      []mir.LocalID{1},
		ReturnType:  ir.TBool,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: ir.TBool, IsReturn: true},
			{ID: 1, Name: "kind", Type: kindTy, IsParam: true},
			{ID: 2, Name: "scratch", Type: ir.TBool},
			{ID: 3, Name: "left", Type: ir.TBool},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{
			{
				ID: 0,
				Instrs: []mir.Instr{
					&mir.CallInstr{Dest: &mir.Place{Local: 3}, Callee: &mir.FnRef{Symbol: "frontTypeIsInteger", Type: ir.ErrTypeVal}, Args: []mir.Operand{paramCopy(1, kindTy)}},
				},
				Term: &mir.BranchTerm{Cond: localCopy(3, ir.TBool), Then: 1, Else: 2},
			},
			{ID: 1, Term: &mir.GotoTerm{Target: 3}},
			{
				ID: 2,
				Instrs: []mir.Instr{
					&mir.CallInstr{Callee: &mir.FnRef{Symbol: "frontTypeIsFloat", Type: ir.ErrTypeVal}, Args: []mir.Operand{paramCopy(1, kindTy)}},
				},
				Term: &mir.GotoTerm{Target: 3},
			},
			{ID: 3, Term: &mir.UnreachableTerm{}},
		},
	}
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"declare i1 @frontTypeIsInteger(ptr)",
		"declare i1 @frontTypeIsFloat(ptr)",
		"define i1 @frontTypeIsNumeric(ptr %kind)",
		"%0 = call i1 @frontTypeIsInteger(ptr %kind)",
		"br i1 %0, label %or.true.1, label %or.call.2",
		"or.true.1:",
		"ret i1 true",
		"or.call.2:",
		"%1 = call i1 @frontTypeIsFloat(ptr %kind)",
		"ret i1 %1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0ShortCircuitBoolReturnsDiscardedFallbackIntrinsic(t *testing.T) {
	t.Parallel()
	fn := &mir.Function{
		Name:        "selfLintIsIntentionalDiscard",
		Params:      []mir.LocalID{1},
		ReturnType:  ir.TBool,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: ir.TBool, IsReturn: true},
			{ID: 1, Name: "name", Type: ir.TString, IsParam: true},
			{ID: 2, Name: "scratch", Type: ir.TBool},
			{ID: 3, Name: "left", Type: ir.TBool},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{
			{
				ID:     0,
				Instrs: []mir.Instr{assign(3, binaryRV(mir.BinEq, paramCopy(1, ir.TString), stringConst("_"), ir.TBool))},
				Term:   &mir.BranchTerm{Cond: localCopy(3, ir.TBool), Then: 1, Else: 2},
			},
			{ID: 1, Term: &mir.GotoTerm{Target: 3}},
			{
				ID: 2,
				Instrs: []mir.Instr{
					&mir.IntrinsicInstr{Kind: mir.IntrinsicStringStartsWith, Args: []mir.Operand{paramCopy(1, ir.TString), stringConst("_")}},
				},
				Term: &mir.GotoTerm{Target: 3},
			},
			{ID: 3, Term: &mir.UnreachableTerm{}},
		},
	}
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"declare i1 @osty_rt_strings_HasPrefix(ptr, ptr)",
		"define i1 @selfLintIsIntentionalDiscard(ptr %name)",
		"call i1 @osty_rt_strings_Equal(ptr %name",
		"ret i1 true",
		"call i1 @osty_rt_strings_HasPrefix(ptr %name",
		"ret i1 %1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0P24CallAndIntrinsicMix(t *testing.T) {
	t.Parallel()
	fn := makeMultiInstrFn(
		"decorate",
		ir.TString,
		[]paramSpec{{name: "name", ty: ir.TString}},
		[]paramSpec{{name: "suffix", ty: ir.TString}},
		[]mir.Instr{
			callInstr(2, "external_suffix", fnTy(ir.TString)),
			&mir.IntrinsicInstr{
				Dest: &mir.Place{Local: 0},
				Kind: mir.IntrinsicStringConcat,
				Args: []mir.Operand{paramCopy(1, ir.TString), paramCopy(2, ir.TString)},
			},
		},
	)
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"declare ptr @external_suffix()",
		"%0 = call ptr @external_suffix()",
		"%1 = call ptr @osty_rt_strings_Concat(ptr %name, ptr %0)",
		"ret ptr %1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0P25AggregateReturnAfterCalls(t *testing.T) {
	t.Parallel()
	child := &ir.NamedType{Name: "Child"}
	fixture := &ir.NamedType{Name: "Fixture"}
	fn := makeMultiInstrFn(
		"fixture",
		fixture,
		nil,
		[]paramSpec{{name: "child", ty: child}, {name: "label", ty: ir.TString}},
		[]mir.Instr{
			&mir.CallInstr{
				Dest:   &mir.Place{Local: 1},
				Callee: &mir.FnRef{Symbol: "externalChild", Type: ir.ErrTypeVal},
			},
			callInstr(2, "externalLabel", fnTy(ir.TString)),
			assign(0, &mir.AggregateRV{
				Kind: mir.AggStruct,
				T:    fixture,
				Fields: []mir.Operand{
					paramCopy(1, child),
					paramCopy(2, ir.TString),
				},
			}),
		},
	)
	module := moduleWith(trivialMainFn(), fn)
	module.Layouts.Structs["Fixture"] = &mir.StructLayout{
		Name: "Fixture",
		Fields: []mir.FieldLayout{
			{Index: 0, Name: "child", Type: child},
			{Index: 1, Name: "label", Type: ir.TString},
		},
	}
	gotBytes, err := EmitMIR(module, llvmabi.Options{PackageName: "main"})
	if err != nil {
		t.Fatalf("EmitMIR: %v", err)
	}
	got := string(gotBytes)
	for _, want := range []string{
		"%Fixture = type { ptr, ptr }",
		"declare ptr @externalChild()",
		"declare ptr @externalLabel()",
		"%0 = call ptr @externalChild()",
		"%1 = call ptr @externalLabel()",
		"%2 = insertvalue %Fixture poison, ptr %0, 0",
		"%3 = insertvalue %Fixture %2, ptr %1, 1",
		"ret %Fixture %3",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0AggregateReturnCanReadBaseFields(t *testing.T) {
	t.Parallel()
	rec := &ir.NamedType{Name: "Rec"}
	fn := makeMultiInstrFn(
		"makeRec",
		rec,
		[]paramSpec{{name: "label", ty: ir.TString}},
		[]paramSpec{{name: "base", ty: rec}},
		[]mir.Instr{
			callInstr(2, "emptyRec", fnTy(rec)),
			assign(0, &mir.AggregateRV{
				Kind: mir.AggStruct,
				T:    rec,
				Fields: []mir.Operand{
					intConst(4),
					paramCopy(1, ir.TString),
					&mir.CopyOp{
						Place: mir.Place{
							Local: 2,
							Projections: []mir.Projection{
								&mir.FieldProj{Index: 2, Name: "count", Type: ir.TInt},
							},
						},
						T: ir.TInt,
					},
				},
			}),
		},
	)
	module := moduleWith(trivialMainFn(), fn)
	module.Layouts.Structs["Rec"] = &mir.StructLayout{
		Name: "Rec",
		Fields: []mir.FieldLayout{
			{Index: 0, Name: "kind", Type: ir.TInt},
			{Index: 1, Name: "label", Type: ir.TString},
			{Index: 2, Name: "count", Type: ir.TInt},
		},
	}
	gotBytes, err := EmitMIR(module, llvmabi.Options{PackageName: "main"})
	if err != nil {
		t.Fatalf("EmitMIR: %v", err)
	}
	got := string(gotBytes)
	for _, want := range []string{
		"%Rec = type { i64, ptr, i64 }",
		"declare ptr @emptyRec()",
		"%0 = call ptr @emptyRec()",
		"%stage0.field.slot.0 = getelementptr inbounds %Rec, ptr %0, i32 0, i32 2",
		"%stage0.field.1 = load i64, ptr %stage0.field.slot.0",
		"%1 = insertvalue %Rec poison, i64 4, 0",
		"%2 = insertvalue %Rec %1, ptr %label, 1",
		"%3 = insertvalue %Rec %2, i64 %stage0.field.1, 2",
		"ret %Rec %3",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0P25StructFieldListLen(t *testing.T) {
	t.Parallel()
	result := &ir.NamedType{Name: "FrontCheckResult"}
	listInt := &ir.NamedType{Name: "List", Args: []ir.Type{ir.TInt}, Builtin: true}
	fn := makeMultiInstrFn(
		"frontCheckResultTypedNodeCount",
		ir.TInt,
		[]paramSpec{{name: "result", ty: result}},
		nil,
		[]mir.Instr{
			&mir.IntrinsicInstr{
				Dest: &mir.Place{Local: 0},
				Kind: mir.IntrinsicListLen,
				Args: []mir.Operand{
					&mir.CopyOp{
						Place: mir.Place{
							Local: 1,
							Projections: []mir.Projection{
								&mir.FieldProj{Index: 0, Name: "typedNodes", Type: listInt},
							},
						},
						T: listInt,
					},
				},
			},
		},
	)
	module := moduleWith(trivialMainFn(), fn)
	module.Layouts.Structs["FrontCheckResult"] = &mir.StructLayout{
		Name: "FrontCheckResult",
		Fields: []mir.FieldLayout{
			{Index: 0, Name: "typedNodes", Type: listInt},
		},
	}
	gotBytes, err := EmitMIR(module, llvmabi.Options{PackageName: "main"})
	if err != nil {
		t.Fatalf("EmitMIR: %v", err)
	}
	got := string(gotBytes)
	for _, want := range []string{
		"%FrontCheckResult = type { ptr }",
		"declare i64 @osty_rt_list_len(ptr)",
		"define i64 @frontCheckResultTypedNodeCount(%FrontCheckResult %result)",
		"%0 = extractvalue %FrontCheckResult %result, 0",
		"%1 = call i64 @osty_rt_list_len(ptr %0)",
		"ret i64 %1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0P26VoidListPush(t *testing.T) {
	t.Parallel()
	listInt := &ir.NamedType{Name: "List", Args: []ir.Type{ir.TInt}, Builtin: true}
	fn := makeMultiInstrFn(
		"pushCode",
		ir.TUnit,
		[]paramSpec{{name: "codes", ty: listInt}, {name: "code", ty: ir.TInt}},
		nil,
		[]mir.Instr{
			&mir.IntrinsicInstr{
				Kind: mir.IntrinsicListPush,
				Args: []mir.Operand{paramCopy(1, listInt), paramCopy(2, ir.TInt)},
			},
			assign(0, useRV(&mir.ConstOp{Const: &mir.UnitConst{}, T: ir.TUnit})),
		},
	)
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"declare void @osty_rt_list_push_i64(ptr, i64)",
		"define void @pushCode(ptr %codes, i64 %code)",
		"call void @osty_rt_list_push_i64(ptr %codes, i64 %code)",
		"ret void",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0P26VoidListPushToStructField(t *testing.T) {
	t.Parallel()
	env := &ir.NamedType{Name: "CheckEnv"}
	ext := &ir.NamedType{Name: "CheckInterfaceExt"}
	listExt := &ir.NamedType{Name: "List", Args: []ir.Type{ext}, Builtin: true}
	fn := makeMultiInstrFn(
		"checkRegisterInterfaceExtends",
		ir.TUnit,
		[]paramSpec{{name: "env", ty: env}, {name: "ext", ty: ext}},
		nil,
		[]mir.Instr{
			&mir.IntrinsicInstr{
				Kind: mir.IntrinsicListPush,
				Args: []mir.Operand{
					&mir.CopyOp{
						Place: mir.Place{
							Local: 1,
							Projections: []mir.Projection{
								&mir.FieldProj{Index: 0, Name: "extends", Type: listExt},
							},
						},
						T: listExt,
					},
					paramCopy(2, ext),
				},
			},
			assign(0, useRV(&mir.ConstOp{Const: &mir.UnitConst{}, T: ir.TUnit})),
		},
	)
	module := moduleWith(trivialMainFn(), fn)
	module.Layouts.Structs["CheckEnv"] = &mir.StructLayout{
		Name: "CheckEnv",
		Fields: []mir.FieldLayout{
			{Index: 0, Name: "extends", Type: listExt},
		},
	}
	gotBytes, err := EmitMIR(module, llvmabi.Options{PackageName: "main"})
	if err != nil {
		t.Fatalf("EmitMIR: %v", err)
	}
	got := string(gotBytes)
	for _, want := range []string{
		"%CheckEnv = type { ptr }",
		"define void @checkRegisterInterfaceExtends(ptr %env, ptr %ext)",
		"getelementptr inbounds %CheckEnv, ptr %env, i32 0, i32 0",
		"load ptr, ptr %stage0.field.slot.0",
		"call void @osty_rt_list_push_ptr(ptr %stage0.field.1, ptr %ext)",
		"ret void",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0SequentialProjectedFieldOperand(t *testing.T) {
	t.Parallel()
	env := &ir.NamedType{Name: "CheckEnv"}
	listInt := &ir.NamedType{Name: "List", Args: []ir.Type{ir.TInt}, Builtin: true}
	fieldCopy := &mir.CopyOp{
		Place: mir.Place{
			Local: 1,
			Projections: []mir.Projection{
				&mir.FieldProj{Index: 0, Name: "codes", Type: listInt},
			},
		},
		T: listInt,
	}
	fn := makeMultiInstrFn(
		"hasCodes",
		ir.TBool,
		[]paramSpec{{name: "env", ty: env}},
		[]paramSpec{{name: "count", ty: ir.TInt}},
		[]mir.Instr{
			&mir.IntrinsicInstr{Dest: &mir.Place{Local: 2}, Kind: mir.IntrinsicListLen, Args: []mir.Operand{fieldCopy}},
			assign(0, binaryRV(mir.BinGt, paramCopy(2, ir.TInt), intConst(0), ir.TBool)),
		},
	)
	module := moduleWith(trivialMainFn(), fn)
	module.Layouts.Structs["CheckEnv"] = &mir.StructLayout{
		Name:   "CheckEnv",
		Fields: []mir.FieldLayout{{Index: 0, Name: "codes", Type: listInt}},
	}
	gotBytes, err := EmitMIR(module, llvmabi.Options{PackageName: "main"})
	if err != nil {
		t.Fatalf("EmitMIR: %v", err)
	}
	got := string(gotBytes)
	for _, want := range []string{
		"%CheckEnv = type { ptr }",
		"define i1 @hasCodes(ptr %env)",
		"%stage0.field.slot.0 = getelementptr inbounds %CheckEnv, ptr %env, i32 0, i32 0",
		"%stage0.field.1 = load ptr, ptr %stage0.field.slot.0",
		"%0 = call i64 @osty_rt_list_len(ptr %stage0.field.1)",
		"%1 = icmp sgt i64 %0, 0",
		"ret i1 %1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0SequentialNestedProjectedFieldOperand(t *testing.T) {
	t.Parallel()
	env := &ir.NamedType{Name: "Env"}
	local := &ir.NamedType{Name: "LocalEnv"}
	listInt := &ir.NamedType{Name: "List", Args: []ir.Type{ir.TInt}, Builtin: true}
	fieldCopy := &mir.CopyOp{
		Place: mir.Place{
			Local: 1,
			Projections: []mir.Projection{
				&mir.FieldProj{Index: 0, Name: "local", Type: local},
				&mir.FieldProj{Index: 0, Name: "bindings", Type: listInt},
			},
		},
		T: listInt,
	}
	fn := makeMultiInstrFn(
		"bindingCount",
		ir.TInt,
		[]paramSpec{{name: "env", ty: env}},
		nil,
		[]mir.Instr{
			&mir.IntrinsicInstr{Dest: &mir.Place{Local: 0}, Kind: mir.IntrinsicListLen, Args: []mir.Operand{fieldCopy}},
		},
	)
	module := moduleWith(trivialMainFn(), fn)
	module.Layouts.Structs["Env"] = &mir.StructLayout{
		Name:   "Env",
		Fields: []mir.FieldLayout{{Index: 0, Name: "local", Type: local}},
	}
	module.Layouts.Structs["LocalEnv"] = &mir.StructLayout{
		Name:   "LocalEnv",
		Fields: []mir.FieldLayout{{Index: 0, Name: "bindings", Type: listInt}},
	}
	gotBytes, err := EmitMIR(module, llvmabi.Options{PackageName: "main"})
	if err != nil {
		t.Fatalf("EmitMIR: %v", err)
	}
	got := string(gotBytes)
	for _, want := range []string{
		"%Env = type { ptr }",
		"%LocalEnv = type { ptr }",
		"define i64 @bindingCount(ptr %env)",
		"%stage0.field.slot.0 = getelementptr inbounds %Env, ptr %env, i32 0, i32 0",
		"%stage0.field.base.1 = load ptr, ptr %stage0.field.slot.0",
		"%stage0.field.slot.2 = getelementptr inbounds %LocalEnv, ptr %stage0.field.base.1, i32 0, i32 0",
		"%stage0.field.3 = load ptr, ptr %stage0.field.slot.2",
		"%0 = call i64 @osty_rt_list_len(ptr %stage0.field.3)",
		"ret i64 %0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0SequentialLenRVAndIndexProjection(t *testing.T) {
	t.Parallel()
	listString := &ir.NamedType{Name: "List", Args: []ir.Type{ir.TString}, Builtin: true}
	lenFn := makeMultiInstrFn(
		"lenViaRV",
		ir.TInt,
		[]paramSpec{{name: "items", ty: listString}},
		nil,
		[]mir.Instr{
			assign(0, &mir.LenRV{Place: mir.Place{Local: 1}, T: ir.TInt}),
		},
	)
	atFn := makeMultiInstrFn(
		"at",
		ir.TString,
		[]paramSpec{{name: "items", ty: listString}, {name: "idx", ty: ir.TInt}},
		nil,
		[]mir.Instr{
			assign(0, useRV(&mir.CopyOp{
				Place: mir.Place{
					Local: 1,
					Projections: []mir.Projection{
						&mir.IndexProj{Index: paramCopy(2, ir.TInt), ElemType: ir.TString},
					},
				},
				T: ir.TString,
			})),
		},
	)
	got := emit(t, trivialMainFn(), lenFn, atFn)
	for _, want := range []string{
		"declare i64 @osty_rt_list_len(ptr)",
		"declare ptr @osty_rt_list_get_string(ptr, i64)",
		"define i64 @lenViaRV(ptr %items)",
		"%0 = call i64 @osty_rt_list_len(ptr %items)",
		"ret i64 %0",
		"define ptr @at(ptr %items, i64 %idx)",
		"%stage0.list.get.0 = call ptr @osty_rt_list_get_string(ptr %items, i64 %idx)",
		"ret ptr %stage0.list.get.0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0BytesLenIndexAndCompare(t *testing.T) {
	t.Parallel()
	listByte := &ir.NamedType{Name: "List", Args: []ir.Type{ir.TByte}, Builtin: true}
	lenFn := makeMultiInstrFn(
		"bytesLenViaRV",
		ir.TInt,
		[]paramSpec{{name: "data", ty: ir.TBytes}},
		nil,
		[]mir.Instr{
			assign(0, &mir.LenRV{Place: mir.Place{Local: 1}, T: ir.TInt}),
		},
	)
	atFn := makeMultiInstrFn(
		"byteAt",
		ir.TByte,
		[]paramSpec{{name: "data", ty: ir.TBytes}, {name: "idx", ty: ir.TInt}},
		nil,
		[]mir.Instr{
			assign(0, useRV(&mir.CopyOp{
				Place: mir.Place{Local: 1, Projections: []mir.Projection{
					&mir.IndexProj{Index: paramCopy(2, ir.TInt), ElemType: ir.TByte},
				}},
				T: ir.TByte,
			})),
		},
	)
	isOpenFn := makeMultiInstrFn(
		"isOpen",
		ir.TBool,
		[]paramSpec{{name: "data", ty: ir.TBytes}, {name: "idx", ty: ir.TInt}},
		nil,
		[]mir.Instr{
			assign(0, binaryRV(mir.BinEq, &mir.CopyOp{
				Place: mir.Place{Local: 1, Projections: []mir.Projection{
					&mir.IndexProj{Index: paramCopy(2, ir.TInt), ElemType: ir.TByte},
				}},
				T: ir.TByte,
			}, byteConst('['), ir.TBool)),
		},
	)
	listAtFn := makeMultiInstrFn(
		"listByteAt",
		ir.TByte,
		[]paramSpec{{name: "items", ty: listByte}, {name: "idx", ty: ir.TInt}},
		nil,
		[]mir.Instr{
			assign(0, useRV(&mir.CopyOp{
				Place: mir.Place{Local: 1, Projections: []mir.Projection{
					&mir.IndexProj{Index: paramCopy(2, ir.TInt), ElemType: ir.TByte},
				}},
				T: ir.TByte,
			})),
		},
	)
	listSetFn := &mir.Function{
		Name:       "listByteSet",
		Params:     []mir.LocalID{1, 2, 3},
		ReturnType: ir.TUnit,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: ir.TUnit, IsReturn: true},
			{ID: 1, Name: "items", Type: listByte, IsParam: true},
			{ID: 2, Name: "idx", Type: ir.TInt, IsParam: true},
			{ID: 3, Name: "value", Type: ir.TByte, IsParam: true},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{
			{
				ID: 0,
				Instrs: []mir.Instr{
					&mir.AssignInstr{
						Dest: mir.Place{Local: 1, Projections: []mir.Projection{
							&mir.IndexProj{Index: paramCopy(2, ir.TInt), ElemType: ir.TByte},
						}},
						Src: useRV(paramCopy(3, ir.TByte)),
					},
				},
				Term: &mir.ReturnTerm{},
			},
		},
	}
	got := emit(t, trivialMainFn(), lenFn, atFn, isOpenFn, listAtFn, listSetFn)
	for _, want := range []string{
		"declare i64 @osty_rt_bytes_len(ptr)",
		"declare i8 @osty_rt_bytes_get(ptr, i64)",
		"declare void @osty_rt_list_get_bytes_v1(ptr, i64, ptr, i64)",
		"declare void @osty_rt_list_set_bytes_v1(ptr, i64, ptr, i64, ptr)",
		"define i64 @bytesLenViaRV(ptr %data)",
		"%0 = call i64 @osty_rt_bytes_len(ptr %data)",
		"define i8 @byteAt(ptr %data, i64 %idx)",
		"%stage0.bytes.get.0 = call i8 @osty_rt_bytes_get(ptr %data, i64 %idx)",
		"ret i8 %stage0.bytes.get.0",
		"define i1 @isOpen(ptr %data, i64 %idx)",
		"call i8 @osty_rt_bytes_get(ptr %data, i64 %idx)",
		"%0 = icmp eq i8 %stage0.bytes.get.",
		", 91",
		"define i8 @listByteAt(ptr %items, i64 %idx)",
		"call void @osty_rt_list_get_bytes_v1(ptr %items, i64 %idx, ptr %stage0.list.get.byte.slot.",
		"load i8, ptr %stage0.list.get.byte.slot.",
		"define void @listByteSet(ptr %items, i64 %idx, i8 %value)",
		"call void @osty_rt_list_set_bytes_v1(ptr %items, i64 %idx, ptr %",
		", i64 1, ptr null)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0CharListAndScalarToString(t *testing.T) {
	t.Parallel()
	listChar := &ir.NamedType{Name: "List", Args: []ir.Type{ir.TChar}, Builtin: true}
	listAtFn := makeMultiInstrFn(
		"listCharAt",
		ir.TChar,
		[]paramSpec{{name: "chars", ty: listChar}, {name: "idx", ty: ir.TInt}},
		nil,
		[]mir.Instr{
			assign(0, useRV(&mir.CopyOp{
				Place: mir.Place{Local: 1, Projections: []mir.Projection{
					&mir.IndexProj{Index: paramCopy(2, ir.TInt), ElemType: ir.TChar},
				}},
				T: ir.TChar,
			})),
		},
	)
	isUpperFn := makeMultiInstrFn(
		"isUpperA",
		ir.TBool,
		[]paramSpec{{name: "chars", ty: listChar}, {name: "idx", ty: ir.TInt}},
		nil,
		[]mir.Instr{
			assign(0, binaryRV(mir.BinGeq, &mir.CopyOp{
				Place: mir.Place{Local: 1, Projections: []mir.Projection{
					&mir.IndexProj{Index: paramCopy(2, ir.TInt), ElemType: ir.TChar},
				}},
				T: ir.TChar,
			}, charConst('A'), ir.TBool)),
		},
	)
	charToStringFn := makeMultiInstrFn(
		"charToString",
		ir.TString,
		[]paramSpec{{name: "ch", ty: ir.TChar}},
		nil,
		[]mir.Instr{
			&mir.IntrinsicInstr{Dest: &mir.Place{Local: 0}, Kind: mir.IntrinsicStringConcat, Args: []mir.Operand{paramCopy(1, ir.TChar)}},
		},
	)
	intToStringFn := makeMultiInstrFn(
		"intToString",
		ir.TString,
		[]paramSpec{{name: "n", ty: ir.TInt}},
		nil,
		[]mir.Instr{
			&mir.IntrinsicInstr{Dest: &mir.Place{Local: 0}, Kind: mir.IntrinsicStringConcat, Args: []mir.Operand{paramCopy(1, ir.TInt)}},
		},
	)
	pushCharFn := &mir.Function{
		Name:       "pushChar",
		Params:     []mir.LocalID{1, 2},
		ReturnType: ir.TUnit,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: ir.TUnit, IsReturn: true},
			{ID: 1, Name: "chars", Type: listChar, IsParam: true},
			{ID: 2, Name: "ch", Type: ir.TChar, IsParam: true},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{
			{
				ID: 0,
				Instrs: []mir.Instr{
					&mir.IntrinsicInstr{Kind: mir.IntrinsicListPush, Args: []mir.Operand{paramCopy(1, listChar), paramCopy(2, ir.TChar)}},
				},
				Term: &mir.ReturnTerm{},
			},
		},
	}
	got := emit(t, trivialMainFn(), listAtFn, isUpperFn, charToStringFn, intToStringFn, pushCharFn)
	for _, want := range []string{
		"declare void @osty_rt_list_get_bytes_v1(ptr, i64, ptr, i64)",
		"declare void @osty_rt_list_push_bytes_v1(ptr, ptr, i64)",
		"declare ptr @osty_rt_char_to_string(i32)",
		"declare ptr @osty_rt_int_to_string(i64)",
		"define i32 @listCharAt(ptr %chars, i64 %idx)",
		"call void @osty_rt_list_get_bytes_v1(ptr %chars, i64 %idx, ptr %stage0.list.get.bytes.slot.",
		", i64 4)",
		"load i32, ptr %stage0.list.get.bytes.slot.",
		"define i1 @isUpperA(ptr %chars, i64 %idx)",
		"icmp uge i32 %stage0.list.get.bytes.",
		", 65",
		"define ptr @charToString(i32 %ch)",
		"call ptr @osty_rt_char_to_string(i32 %ch)",
		"define ptr @intToString(i64 %n)",
		"call ptr @osty_rt_int_to_string(i64 %n)",
		"define void @pushChar(ptr %chars, i32 %ch)",
		"call void @osty_rt_list_push_bytes_v1(ptr %chars, ptr %",
		", i64 4)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0PrimitiveByteCharConversions(t *testing.T) {
	t.Parallel()
	byteToInt := makeMultiInstrFn(
		"byteToInt",
		ir.TInt,
		[]paramSpec{{name: "b", ty: ir.TByte}},
		nil,
		[]mir.Instr{
			&mir.IntrinsicInstr{Dest: &mir.Place{Local: 0}, Kind: mir.IntrinsicByteToInt, Args: []mir.Operand{paramCopy(1, ir.TByte)}},
		},
	)
	charToInt := makeMultiInstrFn(
		"charToInt",
		ir.TInt,
		[]paramSpec{{name: "ch", ty: ir.TChar}},
		nil,
		[]mir.Instr{
			&mir.IntrinsicInstr{Dest: &mir.Place{Local: 0}, Kind: mir.IntrinsicCharToInt, Args: []mir.Operand{paramCopy(1, ir.TChar)}},
		},
	)
	intToByte := makeMultiInstrFn(
		"intToByte",
		ir.TByte,
		[]paramSpec{{name: "n", ty: ir.TInt}},
		nil,
		[]mir.Instr{
			&mir.IntrinsicInstr{Dest: &mir.Place{Local: 0}, Kind: mir.IntrinsicIntToByte, Args: []mir.Operand{paramCopy(1, ir.TInt)}},
		},
	)
	byteToChar := makeMultiInstrFn(
		"byteToChar",
		ir.TChar,
		[]paramSpec{{name: "b", ty: ir.TByte}},
		nil,
		[]mir.Instr{
			&mir.IntrinsicInstr{Dest: &mir.Place{Local: 0}, Kind: mir.IntrinsicByteToChar, Args: []mir.Operand{paramCopy(1, ir.TByte)}},
		},
	)
	got := emit(t, trivialMainFn(), byteToInt, charToInt, intToByte, byteToChar)
	for _, want := range []string{
		"define i64 @byteToInt(i8 %b)",
		"zext i8 %b to i64",
		"define i64 @charToInt(i32 %ch)",
		"zext i32 %ch to i64",
		"define i8 @intToByte(i64 %n)",
		"trunc i64 %n to i8",
		"define i32 @byteToChar(i8 %b)",
		"zext i8 %b to i32",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0SequentialContainerLenIntrinsics(t *testing.T) {
	t.Parallel()
	listInt := &ir.NamedType{Name: "List", Args: []ir.Type{ir.TInt}, Builtin: true}
	mapStringInt := &ir.NamedType{Name: "Map", Args: []ir.Type{ir.TString, ir.TInt}, Builtin: true}
	setString := &ir.NamedType{Name: "Set", Args: []ir.Type{ir.TString}, Builtin: true}
	emptyFn := makeMultiInstrFn(
		"isEmpty",
		ir.TBool,
		[]paramSpec{{name: "items", ty: listInt}},
		nil,
		[]mir.Instr{
			&mir.IntrinsicInstr{Dest: &mir.Place{Local: 0}, Kind: mir.IntrinsicListIsEmpty, Args: []mir.Operand{paramCopy(1, listInt)}},
		},
	)
	mapLenFn := makeMultiInstrFn(
		"mapLen",
		ir.TInt,
		[]paramSpec{{name: "items", ty: mapStringInt}},
		nil,
		[]mir.Instr{
			&mir.IntrinsicInstr{Dest: &mir.Place{Local: 0}, Kind: mir.IntrinsicMapLen, Args: []mir.Operand{paramCopy(1, mapStringInt)}},
		},
	)
	setLenFn := makeMultiInstrFn(
		"setLen",
		ir.TInt,
		[]paramSpec{{name: "items", ty: setString}},
		nil,
		[]mir.Instr{
			&mir.IntrinsicInstr{Dest: &mir.Place{Local: 0}, Kind: mir.IntrinsicSetLen, Args: []mir.Operand{paramCopy(1, setString)}},
		},
	)
	bytesLenFn := makeMultiInstrFn(
		"bytesLen",
		ir.TInt,
		[]paramSpec{{name: "data", ty: ir.TBytes}},
		nil,
		[]mir.Instr{
			&mir.IntrinsicInstr{Dest: &mir.Place{Local: 0}, Kind: mir.IntrinsicBytesLen, Args: []mir.Operand{paramCopy(1, ir.TBytes)}},
		},
	)
	stringEmptyFn := makeMultiInstrFn(
		"stringEmpty",
		ir.TBool,
		[]paramSpec{{name: "s", ty: ir.TString}},
		nil,
		[]mir.Instr{
			&mir.IntrinsicInstr{Dest: &mir.Place{Local: 0}, Kind: mir.IntrinsicStringIsEmpty, Args: []mir.Operand{paramCopy(1, ir.TString)}},
		},
	)
	bytesEmptyFn := makeMultiInstrFn(
		"bytesEmpty",
		ir.TBool,
		[]paramSpec{{name: "data", ty: ir.TBytes}},
		nil,
		[]mir.Instr{
			&mir.IntrinsicInstr{Dest: &mir.Place{Local: 0}, Kind: mir.IntrinsicBytesIsEmpty, Args: []mir.Operand{paramCopy(1, ir.TBytes)}},
		},
	)
	got := emit(t, trivialMainFn(), emptyFn, mapLenFn, setLenFn, bytesLenFn, stringEmptyFn, bytesEmptyFn)
	for _, want := range []string{
		"%stage0.list.is_empty.len.0 = call i64 @osty_rt_list_len(ptr %items)",
		"%stage0.list.is_empty.1 = icmp eq i64 %stage0.list.is_empty.len.0, 0",
		"ret i1 %stage0.list.is_empty.1",
		"declare i64 @osty_rt_map_len(ptr)",
		"%0 = call i64 @osty_rt_map_len(ptr %items)",
		"declare i64 @osty_rt_set_len(ptr)",
		"%0 = call i64 @osty_rt_set_len(ptr %items)",
		"declare i64 @osty_rt_bytes_len(ptr)",
		"%0 = call i64 @osty_rt_bytes_len(ptr %data)",
		"declare i1 @osty_rt_bytes_is_empty(ptr)",
		"%0 = call i1 @osty_rt_bytes_is_empty(ptr %data)",
		"call i64 @osty_rt_strings_ByteLen(ptr %s)",
		"icmp eq i64 %stage0.string.is_empty.len",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0SequentialCollectionValueIntrinsics(t *testing.T) {
	t.Parallel()
	listInt := &ir.NamedType{Name: "List", Args: []ir.Type{ir.TInt}, Builtin: true}
	mapStringInt := &ir.NamedType{Name: "Map", Args: []ir.Type{ir.TString, ir.TInt}, Builtin: true}
	setString := &ir.NamedType{Name: "Set", Args: []ir.Type{ir.TString}, Builtin: true}
	sortedFn := makeMultiInstrFn(
		"sorted",
		listInt,
		[]paramSpec{{name: "items", ty: listInt}},
		nil,
		[]mir.Instr{
			&mir.IntrinsicInstr{Dest: &mir.Place{Local: 0}, Kind: mir.IntrinsicListSorted, Args: []mir.Operand{paramCopy(1, listInt)}},
		},
	)
	toSetFn := makeMultiInstrFn(
		"toSet",
		&ir.NamedType{Name: "Set", Args: []ir.Type{ir.TInt}, Builtin: true},
		[]paramSpec{{name: "items", ty: listInt}},
		nil,
		[]mir.Instr{
			&mir.IntrinsicInstr{Dest: &mir.Place{Local: 0}, Kind: mir.IntrinsicListToSet, Args: []mir.Operand{paramCopy(1, listInt)}},
		},
	)
	toStringFn := makeMultiInstrFn(
		"listText",
		ir.TString,
		[]paramSpec{{name: "items", ty: listInt}},
		nil,
		[]mir.Instr{
			&mir.IntrinsicInstr{Dest: &mir.Place{Local: 0}, Kind: mir.IntrinsicListToString, Args: []mir.Operand{paramCopy(1, listInt)}},
		},
	)
	containsFn := makeMultiInstrFn(
		"hasKey",
		ir.TBool,
		[]paramSpec{{name: "items", ty: mapStringInt}, {name: "key", ty: ir.TString}},
		nil,
		[]mir.Instr{
			&mir.IntrinsicInstr{Dest: &mir.Place{Local: 0}, Kind: mir.IntrinsicMapContains, Args: []mir.Operand{paramCopy(1, mapStringInt), paramCopy(2, ir.TString)}},
		},
	)
	setContainsFn := makeMultiInstrFn(
		"hasSeen",
		ir.TBool,
		[]paramSpec{{name: "seen", ty: setString}, {name: "key", ty: ir.TString}},
		nil,
		[]mir.Instr{
			&mir.IntrinsicInstr{Dest: &mir.Place{Local: 0}, Kind: mir.IntrinsicSetContains, Args: []mir.Operand{paramCopy(1, setString), paramCopy(2, ir.TString)}},
		},
	)
	keysSortedFn := makeMultiInstrFn(
		"keysSorted",
		&ir.NamedType{Name: "List", Args: []ir.Type{ir.TString}, Builtin: true},
		[]paramSpec{{name: "items", ty: mapStringInt}},
		nil,
		[]mir.Instr{
			&mir.IntrinsicInstr{Dest: &mir.Place{Local: 0}, Kind: mir.IntrinsicMapKeysSorted, Args: []mir.Operand{paramCopy(1, mapStringInt)}},
		},
	)
	incrFn := makeMultiInstrFn(
		"incr",
		ir.TInt,
		[]paramSpec{{name: "items", ty: mapStringInt}, {name: "key", ty: ir.TString}, {name: "delta", ty: ir.TInt}},
		nil,
		[]mir.Instr{
			&mir.IntrinsicInstr{Dest: &mir.Place{Local: 0}, Kind: mir.IntrinsicMapIncr, Args: []mir.Operand{paramCopy(1, mapStringInt), paramCopy(2, ir.TString), paramCopy(3, ir.TInt)}},
		},
	)
	mapNewFn := makeMultiInstrFn(
		"mapNew",
		mapStringInt,
		nil,
		nil,
		[]mir.Instr{
			&mir.IntrinsicInstr{Dest: &mir.Place{Local: 0}, Kind: mir.IntrinsicMapNew},
		},
	)
	setNewFn := makeMultiInstrFn(
		"setNew",
		setString,
		nil,
		nil,
		[]mir.Instr{
			&mir.IntrinsicInstr{Dest: &mir.Place{Local: 0}, Kind: mir.IntrinsicSetNew},
		},
	)
	got := emit(t, trivialMainFn(), sortedFn, toSetFn, toStringFn, containsFn, setContainsFn, keysSortedFn, incrFn, mapNewFn, setNewFn)
	for _, want := range []string{
		"declare ptr @osty_rt_list_sorted_i64(ptr)",
		"declare ptr @osty_rt_list_to_set_i64(ptr)",
		"declare ptr @osty_rt_map_keys_sorted_string(ptr)",
		"declare i1 @osty_rt_map_contains_string(ptr, ptr)",
		"declare i1 @osty_rt_set_contains_string(ptr, ptr)",
		"declare i64 @osty_rt_map_incr_i64_string(ptr, ptr, i64)",
		"declare ptr @osty_rt_list_to_string_i64(ptr)",
		"declare ptr @osty_rt_map_new(i64, i64, i64, ptr)",
		"declare ptr @osty_rt_set_new(i64)",
		"%0 = call ptr @osty_rt_list_sorted_i64(ptr %items)",
		"%0 = call ptr @osty_rt_list_to_set_i64(ptr %items)",
		"%0 = call ptr @osty_rt_map_keys_sorted_string(ptr %items)",
		"%0 = call i1 @osty_rt_map_contains_string(ptr %items, ptr %key)",
		"%0 = call i1 @osty_rt_set_contains_string(ptr %seen, ptr %key)",
		"%0 = call i64 @osty_rt_map_incr_i64_string(ptr %items, ptr %key, i64 %delta)",
		"%0 = call ptr @osty_rt_list_to_string_i64(ptr %items)",
		"%0 = call ptr @osty_rt_map_new(i64 5, i64 1, i64 8, ptr null)",
		"%0 = call ptr @osty_rt_set_new(i64 5)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0SequentialBytesHintsAndRawNullIntrinsics(t *testing.T) {
	t.Parallel()
	containsFn := makeMultiInstrFn(
		"bytesContains",
		ir.TBool,
		[]paramSpec{{name: "data", ty: ir.TBytes}, {name: "needle", ty: ir.TBytes}},
		nil,
		[]mir.Instr{
			&mir.IntrinsicInstr{Dest: &mir.Place{Local: 0}, Kind: mir.IntrinsicBytesContains, Args: []mir.Operand{paramCopy(1, ir.TBytes), paramCopy(2, ir.TBytes)}},
		},
	)
	repeatFn := makeMultiInstrFn(
		"bytesRepeat",
		ir.TBytes,
		[]paramSpec{{name: "data", ty: ir.TBytes}, {name: "n", ty: ir.TInt}},
		nil,
		[]mir.Instr{
			&mir.IntrinsicInstr{Dest: &mir.Place{Local: 0}, Kind: mir.IntrinsicBytesRepeat, Args: []mir.Operand{paramCopy(1, ir.TBytes), paramCopy(2, ir.TInt)}},
		},
	)
	toHexFn := makeMultiInstrFn(
		"bytesHex",
		ir.TString,
		[]paramSpec{{name: "data", ty: ir.TBytes}},
		nil,
		[]mir.Instr{
			&mir.IntrinsicInstr{Dest: &mir.Place{Local: 0}, Kind: mir.IntrinsicBytesToHex, Args: []mir.Operand{paramCopy(1, ir.TBytes)}},
		},
	)
	fromStringFn := makeMultiInstrFn(
		"bytesFromString",
		ir.TBytes,
		[]paramSpec{{name: "text", ty: ir.TString}},
		nil,
		[]mir.Instr{
			&mir.IntrinsicInstr{Dest: &mir.Place{Local: 0}, Kind: mir.IntrinsicBytesFromString, Args: []mir.Operand{paramCopy(1, ir.TString)}},
		},
	)
	rawNullFn := makeMultiInstrFn(
		"rawNull",
		ir.TRawPtr,
		nil,
		nil,
		[]mir.Instr{
			&mir.IntrinsicInstr{Dest: &mir.Place{Local: 0}, Kind: mir.IntrinsicRawNull},
		},
	)
	likelyFn := makeMultiInstrFn(
		"likely",
		ir.TBool,
		[]paramSpec{{name: "ok", ty: ir.TBool}},
		nil,
		[]mir.Instr{
			&mir.IntrinsicInstr{Dest: &mir.Place{Local: 0}, Kind: mir.IntrinsicLikely, Args: []mir.Operand{paramCopy(1, ir.TBool)}},
		},
	)
	got := emit(t, trivialMainFn(), containsFn, repeatFn, toHexFn, fromStringFn, rawNullFn, likelyFn)
	for _, want := range []string{
		"declare i64 @osty_rt_bytes_index_of(ptr, ptr)",
		"declare ptr @osty_rt_bytes_repeat(ptr, i64)",
		"declare ptr @osty_rt_bytes_to_hex(ptr)",
		"declare ptr @osty_rt_strings_ToBytes(ptr)",
		"declare i1 @llvm.expect.i1(i1, i1)",
		"call i64 @osty_rt_bytes_index_of(ptr %data, ptr %needle)",
		"icmp ne i64 %stage0.bytes.contains.index",
		"%0 = call ptr @osty_rt_bytes_repeat(ptr %data, i64 %n)",
		"%0 = call ptr @osty_rt_bytes_to_hex(ptr %data)",
		"%0 = call ptr @osty_rt_strings_ToBytes(ptr %text)",
		"define ptr @rawNull()",
		"ret ptr null",
		"%0 = call i1 @llvm.expect.i1(i1 %ok, i1 true)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0SequentialUnaryRValues(t *testing.T) {
	t.Parallel()
	neg := makeMultiInstrFn(
		"neg",
		ir.TInt,
		[]paramSpec{{name: "x", ty: ir.TInt}},
		nil,
		[]mir.Instr{
			assign(0, &mir.UnaryRV{Op: mir.UnNeg, Arg: paramCopy(1, ir.TInt), T: ir.TInt}),
		},
	)
	bitNot := makeMultiInstrFn(
		"bitNot",
		ir.TInt,
		[]paramSpec{{name: "x", ty: ir.TInt}},
		nil,
		[]mir.Instr{
			assign(0, &mir.UnaryRV{Op: mir.UnBitNot, Arg: paramCopy(1, ir.TInt), T: ir.TInt}),
		},
	)
	not := makeMultiInstrFn(
		"not",
		ir.TBool,
		[]paramSpec{{name: "ok", ty: ir.TBool}},
		nil,
		[]mir.Instr{
			assign(0, &mir.UnaryRV{Op: mir.UnNot, Arg: paramCopy(1, ir.TBool), T: ir.TBool}),
		},
	)
	plus := makeMultiInstrFn(
		"plus",
		ir.TInt,
		[]paramSpec{{name: "x", ty: ir.TInt}},
		nil,
		[]mir.Instr{
			assign(0, &mir.UnaryRV{Op: mir.UnPlus, Arg: paramCopy(1, ir.TInt), T: ir.TInt}),
		},
	)
	got := emit(t, trivialMainFn(), neg, bitNot, not, plus)
	for _, want := range []string{
		"define i64 @neg(i64 %x)",
		"%0 = sub i64 0, %x",
		"define i64 @bitNot(i64 %x)",
		"%0 = xor i64 %x, -1",
		"define i1 @not(i1 %ok)",
		"%0 = xor i1 %ok, true",
		"define i64 @plus(i64 %x)",
		"ret i64 %x",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0SequentialProjectedFieldWrite(t *testing.T) {
	t.Parallel()
	node := &ir.NamedType{Name: "Node"}
	fn := makeMultiInstrFn(
		"setKind",
		ir.TInt,
		[]paramSpec{{name: "node", ty: node}, {name: "kind", ty: ir.TInt}},
		nil,
		[]mir.Instr{
			&mir.AssignInstr{
				Dest: mir.Place{
					Local: 1,
					Projections: []mir.Projection{
						&mir.FieldProj{Index: 0, Name: "kind", Type: ir.TInt},
					},
				},
				Src: useRV(paramCopy(2, ir.TInt)),
			},
			assign(0, useRV(intConst(1))),
		},
	)
	module := moduleWith(trivialMainFn(), fn)
	module.Layouts.Structs["Node"] = &mir.StructLayout{
		Name:   "Node",
		Fields: []mir.FieldLayout{{Index: 0, Name: "kind", Type: ir.TInt}},
	}
	gotBytes, err := EmitMIR(module, llvmabi.Options{PackageName: "main"})
	if err != nil {
		t.Fatalf("EmitMIR: %v", err)
	}
	got := string(gotBytes)
	for _, want := range []string{
		"%Node = type { i64 }",
		"define i64 @setKind(ptr %node, i64 %kind)",
		"%stage0.field.store.slot.0 = getelementptr inbounds %Node, ptr %node, i32 0, i32 0",
		"store i64 %kind, ptr %stage0.field.store.slot.0",
		"ret i64 1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0P26VoidUnknownCallAfterListLiteral(t *testing.T) {
	t.Parallel()
	listString := &ir.NamedType{Name: "List", Args: []ir.Type{ir.TString}, Builtin: true}
	fn := makeMultiInstrFn(
		"emitParts",
		ir.TUnit,
		nil,
		[]paramSpec{{name: "parts", ty: listString}},
		[]mir.Instr{
			assign(1, &mir.AggregateRV{
				Kind: mir.AggList,
				T:    listString,
				Fields: []mir.Operand{
					stringConst("a"),
					stringConst("b"),
				},
			}),
			&mir.CallInstr{
				Callee: &mir.FnRef{Symbol: "externalEmit", Type: ir.ErrTypeVal},
				Args:   []mir.Operand{paramCopy(1, listString)},
			},
			assign(0, useRV(&mir.ConstOp{Const: &mir.UnitConst{}, T: ir.TUnit})),
		},
	)
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"declare void @externalEmit(ptr)",
		"define void @emitParts()",
		"%0 = call ptr @osty_rt_list_new()",
		"call void @osty_rt_list_push_string(ptr %0, ptr @.str.0)",
		"call void @osty_rt_list_push_string(ptr %0, ptr @.str.1)",
		"call void @externalEmit(ptr %0)",
		"ret void",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0SequentialReturnKeepsVoidCall(t *testing.T) {
	t.Parallel()
	fn := makeMultiInstrFn(
		"touchThenReturn",
		ir.TInt,
		[]paramSpec{{name: "path", ty: ir.TString}},
		nil,
		[]mir.Instr{
			&mir.CallInstr{
				Callee: &mir.FnRef{Symbol: "externalTouch", Type: ir.ErrTypeVal},
				Args:   []mir.Operand{paramCopy(1, ir.TString)},
			},
			assign(0, useRV(intConst(7))),
		},
	)
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"declare void @externalTouch(ptr)",
		"define i64 @touchThenReturn(ptr %path)",
		"call void @externalTouch(ptr %path)",
		"ret i64 7",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0P27VoidRuntimeIntrinsics(t *testing.T) {
	t.Parallel()
	listInt := &ir.NamedType{Name: "List", Args: []ir.Type{ir.TInt}, Builtin: true}
	setInt := &ir.NamedType{Name: "Set", Args: []ir.Type{ir.TInt}, Builtin: true}
	fn := makeMultiInstrFn(
		"touchRuntime",
		ir.TUnit,
		[]paramSpec{{name: "items", ty: listInt}, {name: "seen", ty: setInt}, {name: "value", ty: ir.TInt}},
		nil,
		[]mir.Instr{
			&mir.IntrinsicInstr{Kind: mir.IntrinsicListReverse, Args: []mir.Operand{paramCopy(1, listInt)}},
			&mir.IntrinsicInstr{Kind: mir.IntrinsicSetInsert, Args: []mir.Operand{paramCopy(2, setInt), paramCopy(3, ir.TInt)}},
			&mir.IntrinsicInstr{Kind: mir.IntrinsicYield},
			&mir.IntrinsicInstr{Kind: mir.IntrinsicSleep, Args: []mir.Operand{intConst(1)}},
			&mir.IntrinsicInstr{Kind: mir.IntrinsicCheckCancelled},
			assign(0, useRV(&mir.ConstOp{Const: &mir.UnitConst{}, T: ir.TUnit})),
		},
	)
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"declare void @osty_rt_list_reverse(ptr)",
		"declare i1 @osty_rt_set_insert_i64(ptr, i64)",
		"declare void @osty_rt_yield()",
		"declare void @osty_rt_sleep(i64)",
		"declare void @osty_rt_check_cancelled()",
		"call void @osty_rt_list_reverse(ptr %items)",
		"call i1 @osty_rt_set_insert_i64(ptr %seen, i64 %value)",
		"call void @osty_rt_yield()",
		"call void @osty_rt_sleep(i64 1)",
		"call void @osty_rt_check_cancelled()",
		"ret void",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0P27CollectionVoidMutators(t *testing.T) {
	t.Parallel()
	listInt := &ir.NamedType{Name: "List", Args: []ir.Type{ir.TInt}, Builtin: true}
	listString := &ir.NamedType{Name: "List", Args: []ir.Type{ir.TString}, Builtin: true}
	mapStringInt := &ir.NamedType{Name: "Map", Args: []ir.Type{ir.TString, ir.TInt}, Builtin: true}
	setInt := &ir.NamedType{Name: "Set", Args: []ir.Type{ir.TInt}, Builtin: true}
	fn := makeMultiInstrFn(
		"mutateCollections",
		ir.TUnit,
		[]paramSpec{
			{name: "items", ty: listInt},
			{name: "table", ty: mapStringInt},
			{name: "seen", ty: setInt},
			{name: "value", ty: ir.TInt},
			{name: "key", ty: ir.TString},
			{name: "parts", ty: listString},
			{name: "text", ty: ir.TString},
			{name: "sep", ty: ir.TString},
		},
		nil,
		[]mir.Instr{
			&mir.IntrinsicInstr{Kind: mir.IntrinsicListInsert, Args: []mir.Operand{paramCopy(1, listInt), intConst(0), paramCopy(4, ir.TInt)}},
			&mir.IntrinsicInstr{Kind: mir.IntrinsicListClear, Args: []mir.Operand{paramCopy(1, listInt)}},
			&mir.IntrinsicInstr{Kind: mir.IntrinsicMapSet, Args: []mir.Operand{paramCopy(2, mapStringInt), paramCopy(5, ir.TString), paramCopy(4, ir.TInt)}},
			&mir.IntrinsicInstr{Kind: mir.IntrinsicMapClear, Args: []mir.Operand{paramCopy(2, mapStringInt)}},
			&mir.IntrinsicInstr{Kind: mir.IntrinsicSetClear, Args: []mir.Operand{paramCopy(3, setInt)}},
			&mir.IntrinsicInstr{Kind: mir.IntrinsicStringSplitInto, Args: []mir.Operand{paramCopy(6, listString), paramCopy(7, ir.TString), paramCopy(8, ir.TString)}},
			assign(0, useRV(&mir.ConstOp{Const: &mir.UnitConst{}, T: ir.TUnit})),
		},
	)
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"declare void @osty_rt_list_insert_i64(ptr, i64, i64)",
		"declare void @osty_rt_list_clear(ptr)",
		"declare void @osty_rt_map_insert_string(ptr, ptr, ptr)",
		"declare void @osty_rt_map_clear(ptr)",
		"declare void @osty_rt_set_clear(ptr)",
		"declare void @osty_rt_strings_SplitInto(ptr, ptr, ptr)",
		"call void @osty_rt_list_insert_i64(ptr %items, i64 0, i64 %value)",
		"call void @osty_rt_list_clear(ptr %items)",
		"%stage0.map.value.0 = alloca i64",
		"store i64 %value, ptr %stage0.map.value.0",
		"call void @osty_rt_map_insert_string(ptr %table, ptr %key, ptr %stage0.map.value.0)",
		"call void @osty_rt_map_clear(ptr %table)",
		"call void @osty_rt_set_clear(ptr %seen)",
		"call void @osty_rt_strings_SplitInto(ptr %parts, ptr %text, ptr %sep)",
		"ret void",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0P27RemainingCollectionVoidMutators(t *testing.T) {
	t.Parallel()
	listInt := &ir.NamedType{Name: "List", Args: []ir.Type{ir.TInt}, Builtin: true}
	mapStringInt := &ir.NamedType{Name: "Map", Args: []ir.Type{ir.TString, ir.TInt}, Builtin: true}
	setString := &ir.NamedType{Name: "Set", Args: []ir.Type{ir.TString}, Builtin: true}
	fn := makeMultiInstrFn(
		"drainCollections",
		ir.TUnit,
		[]paramSpec{
			{name: "items", ty: listInt},
			{name: "table", ty: mapStringInt},
			{name: "seen", ty: setString},
			{name: "key", ty: ir.TString},
			{name: "idx", ty: ir.TInt},
		},
		nil,
		[]mir.Instr{
			&mir.IntrinsicInstr{Kind: mir.IntrinsicListPop, Args: []mir.Operand{paramCopy(1, listInt)}},
			&mir.IntrinsicInstr{Kind: mir.IntrinsicListRemoveAt, Args: []mir.Operand{paramCopy(1, listInt), paramCopy(5, ir.TInt)}},
			&mir.IntrinsicInstr{Kind: mir.IntrinsicMapRemove, Args: []mir.Operand{paramCopy(2, mapStringInt), paramCopy(4, ir.TString)}},
			&mir.IntrinsicInstr{Kind: mir.IntrinsicSetRemove, Args: []mir.Operand{paramCopy(3, setString), paramCopy(4, ir.TString)}},
			assign(0, useRV(&mir.ConstOp{Const: &mir.UnitConst{}, T: ir.TUnit})),
		},
	)
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"declare void @osty_rt_list_pop_discard(ptr)",
		"declare void @osty_rt_list_remove_at_discard(ptr, i64)",
		"declare i1 @osty_rt_map_remove_string(ptr, ptr)",
		"declare i1 @osty_rt_set_remove_string(ptr, ptr)",
		"call void @osty_rt_list_pop_discard(ptr %items)",
		"call void @osty_rt_list_remove_at_discard(ptr %items, i64 %idx)",
		"call i1 @osty_rt_map_remove_string(ptr %table, ptr %key)",
		"call i1 @osty_rt_set_remove_string(ptr %seen, ptr %key)",
		"ret void",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0P27PrintFamilyAndAbortIntrinsics(t *testing.T) {
	t.Parallel()
	fn := makeMultiInstrFn(
		"writeDiagnostics",
		ir.TUnit,
		[]paramSpec{{name: "n", ty: ir.TInt}, {name: "msg", ty: ir.TString}},
		nil,
		[]mir.Instr{
			&mir.IntrinsicInstr{Kind: mir.IntrinsicPrint, Args: []mir.Operand{paramCopy(1, ir.TInt)}},
			&mir.IntrinsicInstr{Kind: mir.IntrinsicPrintln, Args: []mir.Operand{paramCopy(2, ir.TString)}},
			&mir.IntrinsicInstr{Kind: mir.IntrinsicEprint, Args: []mir.Operand{paramCopy(2, ir.TString)}},
			&mir.IntrinsicInstr{Kind: mir.IntrinsicEprintln, Args: []mir.Operand{paramCopy(1, ir.TInt)}},
			&mir.IntrinsicInstr{Kind: mir.IntrinsicAbort, Args: []mir.Operand{stringConst("stop")}},
			assign(0, useRV(&mir.ConstOp{Const: &mir.UnitConst{}, T: ir.TUnit})),
		},
	)
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"@.fmt.stage0.print.int = private unnamed_addr constant [5 x i8] c\"%lld\\00\"",
		"@.fmt.stage0.print.str = private unnamed_addr constant [3 x i8] c\"%s\\00\"",
		"@.fmt.stage0.println.int = private unnamed_addr constant [6 x i8] c\"%lld\\0A\\00\"",
		"@.fmt.stage0.println.str = private unnamed_addr constant [4 x i8] c\"%s\\0A\\00\"",
		"@stderr = external global ptr",
		"declare i32 @fprintf(ptr, ptr, ...)",
		"declare void @osty_rt_panic(ptr)",
		"call i32 (ptr, ...) @printf(ptr @.fmt.stage0.print.int, i64 %n)",
		"call i32 (ptr, ...) @printf(ptr @.fmt.stage0.println.str, ptr %msg)",
		"load ptr, ptr @stderr",
		"call i32 (ptr, ptr, ...) @fprintf",
		"call void @osty_rt_panic(ptr @.str.0)",
		"ret void",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0P27ConcurrencyValueIntrinsics(t *testing.T) {
	t.Parallel()
	chanInt := &ir.NamedType{Name: "Channel", Args: []ir.Type{ir.TInt}, Builtin: true}
	group := &ir.NamedType{Name: "TaskGroup", Builtin: true}
	makeFn := makeMultiInstrFn(
		"makeChannelAndProbeCancel",
		ir.TBool,
		nil,
		[]paramSpec{{name: "ch", ty: chanInt}, {name: "closed", ty: ir.TBool}, {name: "cancelled", ty: ir.TBool}},
		[]mir.Instr{
			&mir.IntrinsicInstr{Dest: &mir.Place{Local: 1}, Kind: mir.IntrinsicChanMake, Args: []mir.Operand{intConst(2)}},
			&mir.IntrinsicInstr{Dest: &mir.Place{Local: 2}, Kind: mir.IntrinsicChanIsClosed, Args: []mir.Operand{paramCopy(1, chanInt)}},
			&mir.IntrinsicInstr{Dest: &mir.Place{Local: 3}, Kind: mir.IntrinsicIsCancelled},
			assign(0, useRV(paramCopy(2, ir.TBool))),
		},
	)
	groupFn := makeMultiInstrFn(
		"groupCancelled",
		ir.TBool,
		[]paramSpec{{name: "group", ty: group}},
		nil,
		[]mir.Instr{
			&mir.IntrinsicInstr{Dest: &mir.Place{Local: 0}, Kind: mir.IntrinsicGroupIsCancelled, Args: []mir.Operand{paramCopy(1, group)}},
		},
	)
	got := emit(t, trivialMainFn(), makeFn, groupFn)
	for _, want := range []string{
		"declare ptr @osty_rt_thread_chan_make(i64)",
		"declare i1 @osty_rt_thread_chan_is_closed(ptr)",
		"declare i1 @osty_rt_cancel_is_cancelled()",
		"declare i1 @osty_rt_task_group_is_cancelled(ptr)",
		"%0 = call ptr @osty_rt_thread_chan_make(i64 2)",
		"%1 = call i1 @osty_rt_thread_chan_is_closed(ptr %0)",
		"%2 = call i1 @osty_rt_cancel_is_cancelled()",
		"ret i1 %1",
		"%0 = call i1 @osty_rt_task_group_is_cancelled(ptr %group)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0P27ConcurrencyVoidIntrinsics(t *testing.T) {
	t.Parallel()
	chanInt := &ir.NamedType{Name: "Channel", Args: []ir.Type{ir.TInt}, Builtin: true}
	group := &ir.NamedType{Name: "TaskGroup", Builtin: true}
	selectBuilder := &ir.NamedType{Name: "SelectBuilder"}
	arm := &ir.NamedType{Name: "SelectArm"}
	fn := makeMultiInstrFn(
		"wireConcurrency",
		ir.TUnit,
		[]paramSpec{
			{name: "ch", ty: chanInt},
			{name: "value", ty: ir.TInt},
			{name: "group", ty: group},
			{name: "sel", ty: selectBuilder},
			{name: "arm", ty: arm},
		},
		nil,
		[]mir.Instr{
			&mir.IntrinsicInstr{Kind: mir.IntrinsicChanSend, Args: []mir.Operand{paramCopy(1, chanInt), paramCopy(2, ir.TInt)}},
			&mir.IntrinsicInstr{Kind: mir.IntrinsicChanClose, Args: []mir.Operand{paramCopy(1, chanInt)}},
			&mir.IntrinsicInstr{Kind: mir.IntrinsicGroupCancel, Args: []mir.Operand{paramCopy(3, group)}},
			&mir.IntrinsicInstr{Kind: mir.IntrinsicSelectRecv, Args: []mir.Operand{paramCopy(4, selectBuilder), paramCopy(1, chanInt), paramCopy(5, arm)}},
			&mir.IntrinsicInstr{Kind: mir.IntrinsicSelectSend, Args: []mir.Operand{paramCopy(4, selectBuilder), paramCopy(1, chanInt), paramCopy(2, ir.TInt), paramCopy(5, arm)}},
			&mir.IntrinsicInstr{Kind: mir.IntrinsicSelectTimeout, Args: []mir.Operand{paramCopy(4, selectBuilder), intConst(5), paramCopy(5, arm)}},
			&mir.IntrinsicInstr{Kind: mir.IntrinsicSelectDefault, Args: []mir.Operand{paramCopy(4, selectBuilder), paramCopy(5, arm)}},
			assign(0, useRV(&mir.ConstOp{Const: &mir.UnitConst{}, T: ir.TUnit})),
		},
	)
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"declare void @osty_rt_thread_chan_send_i64(ptr, i64)",
		"declare void @osty_rt_thread_chan_close(ptr)",
		"declare void @osty_rt_task_group_cancel(ptr)",
		"declare void @osty_rt_select_recv(ptr, ptr, ptr)",
		"declare void @osty_rt_select_send_i64(ptr, ptr, i64, ptr)",
		"declare void @osty_rt_select_timeout(ptr, i64, ptr)",
		"declare void @osty_rt_select_default(ptr, ptr)",
		"call void @osty_rt_thread_chan_send_i64(ptr %ch, i64 %value)",
		"call void @osty_rt_thread_chan_close(ptr %ch)",
		"call void @osty_rt_task_group_cancel(ptr %group)",
		"call void @osty_rt_select_recv(ptr %sel, ptr %ch, ptr %arm)",
		"call void @osty_rt_select_send_i64(ptr %sel, ptr %ch, i64 %value, ptr %arm)",
		"call void @osty_rt_select_timeout(ptr %sel, i64 5, ptr %arm)",
		"call void @osty_rt_select_default(ptr %sel, ptr %arm)",
		"ret void",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

// ---- P3b rejection paths ----

func TestStage0EmitsCallWithReturnOnlyCalleeType(t *testing.T) {
	t.Parallel()
	caller := makeMultiInstrFn(
		"callReturnOnly",
		ir.TInt,
		nil,
		[]paramSpec{{name: "r", ty: ir.TInt}},
		[]mir.Instr{
			&mir.CallInstr{
				Dest:   &mir.Place{Local: 1},
				Callee: &mir.FnRef{Symbol: "external_symbol_not_in_module", Type: ir.TInt},
				Args:   []mir.Operand{intConst(1)},
			},
			assign(0, useRV(paramCopy(1, ir.TInt))),
		},
	)
	got := emit(t, trivialMainFn(), caller)
	for _, want := range []string{
		"declare i64 @external_symbol_not_in_module(i64)",
		"define i64 @callReturnOnly()",
		"call i64 @external_symbol_not_in_module(i64 1)",
		"ret i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0RejectsCallReturnTypeMismatch(t *testing.T) {
	t.Parallel()
	// callee declared to return Int, but local typed Bool.
	addFn := makeFn(fnSpec{
		name:   "add",
		retT:   ir.TInt,
		params: []paramSpec{{name: "a", ty: ir.TInt}, {name: "b", ty: ir.TInt}},
		src:    binaryRV(mir.BinAdd, paramCopy(1, ir.TInt), paramCopy(2, ir.TInt), ir.TInt),
	})
	caller := makeMultiInstrFn(
		"bad",
		ir.TBool,
		nil,
		[]paramSpec{{name: "r", ty: ir.TBool}}, // Bool local
		[]mir.Instr{
			callInstr(1, "add", fnTy(ir.TInt, ir.TInt, ir.TInt), intConst(1), intConst(2)),
			assign(0, useRV(paramCopy(1, ir.TBool))),
		},
	)
	mustReject(t, trivialMainFn(), addFn, caller)
}

func TestStage0RejectsCallArityMismatch(t *testing.T) {
	t.Parallel()
	addFn := makeFn(fnSpec{
		name:   "add",
		retT:   ir.TInt,
		params: []paramSpec{{name: "a", ty: ir.TInt}, {name: "b", ty: ir.TInt}},
		src:    binaryRV(mir.BinAdd, paramCopy(1, ir.TInt), paramCopy(2, ir.TInt), ir.TInt),
	})
	// Pass only one arg but FnType says 2.
	caller := makeMultiInstrFn(
		"bad",
		ir.TInt,
		nil,
		[]paramSpec{{name: "r", ty: ir.TInt}},
		[]mir.Instr{
			callInstr(1, "add", fnTy(ir.TInt, ir.TInt, ir.TInt), intConst(1)),
			assign(0, useRV(paramCopy(1, ir.TInt))),
		},
	)
	mustReject(t, trivialMainFn(), addFn, caller)
}

func TestStage0RejectsIndirectCall(t *testing.T) {
	t.Parallel()
	caller := makeMultiInstrFn(
		"bad",
		ir.TInt,
		nil,
		[]paramSpec{{name: "r", ty: ir.TInt}},
		[]mir.Instr{
			&mir.CallInstr{
				Dest:   &mir.Place{Local: 1},
				Callee: &mir.IndirectCall{Callee: paramCopy(0, ir.TInt)},
				Args:   nil,
			},
			assign(0, useRV(paramCopy(1, ir.TInt))),
		},
	)
	mustReject(t, trivialMainFn(), caller)
}

func TestStage0EmitsDiscardedValueCall(t *testing.T) {
	t.Parallel()
	other := makeFn(fnSpec{
		name: "other",
		retT: ir.TInt,
		src:  useRV(intConst(0)),
	})
	caller := makeMultiInstrFn(
		"bad",
		ir.TInt,
		nil,
		nil,
		[]mir.Instr{
			&mir.CallInstr{
				Dest:   nil,
				Callee: &mir.FnRef{Symbol: "other", Type: fnTy(ir.TInt)},
				Args:   nil,
			},
			assign(0, useRV(intConst(7))),
		},
	)
	got := emit(t, trivialMainFn(), other, caller)
	for _, want := range []string{
		"define i64 @other()",
		"define i64 @bad()",
		"call i64 @other()",
		"store i64 7, ptr %ret.slot",
		"ret i64 %0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0EmitsScalarReturnChain(t *testing.T) {
	t.Parallel()
	fn := &mir.Function{
		Name:        "floatOpcode",
		Params:      []mir.LocalID{1},
		ReturnType:  ir.TString,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: ir.TString, IsReturn: true},
			{ID: 1, Name: "op", Type: ir.TString, IsParam: true},
			{ID: 2, Name: "condAdd", Type: ir.TBool},
			{ID: 3, Name: "condSub", Type: ir.TBool},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{
			{
				ID: 0,
				Instrs: []mir.Instr{
					assign(2, binaryRV(mir.BinEq, paramCopy(1, ir.TString), stringConst("add"), ir.TBool)),
				},
				Term: &mir.BranchTerm{Cond: paramCopy(2, ir.TBool), Then: 1, Else: 2},
			},
			{ID: 1, Instrs: []mir.Instr{assign(0, useRV(stringConst("fadd")))}, Term: &mir.ReturnTerm{}},
			{ID: 2, Term: &mir.GotoTerm{Target: 3}},
			{
				ID: 3,
				Instrs: []mir.Instr{
					assign(3, binaryRV(mir.BinEq, paramCopy(1, ir.TString), stringConst("sub"), ir.TBool)),
				},
				Term: &mir.BranchTerm{Cond: paramCopy(3, ir.TBool), Then: 4, Else: 5},
			},
			{ID: 4, Instrs: []mir.Instr{assign(0, useRV(stringConst("fsub")))}, Term: &mir.ReturnTerm{}},
			{ID: 5, Instrs: []mir.Instr{assign(0, useRV(stringConst("unknown")))}, Term: &mir.ReturnTerm{}},
		},
	}
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"define ptr @floatOpcode(ptr %op)",
		"entry:",
		"%0 = call i1 @osty_rt_strings_Equal(ptr %op, ptr @.str.0)",
		"br i1 %0, label %return.1, label %chain.3",
		"return.1:",
		"ret ptr @.str.1",
		"chain.3:",
		"%1 = call i1 @osty_rt_strings_Equal(ptr %op, ptr @.str.2)",
		"br i1 %1, label %return.4, label %return.5",
		"return.4:",
		"ret ptr @.str.3",
		"return.5:",
		"ret ptr @.str.4",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0EmitsStringOrderingCompare(t *testing.T) {
	t.Parallel()
	got := emit(t, trivialMainFn(),
		makeFn(fnSpec{
			name:   "isIdentStart",
			retT:   ir.TBool,
			params: []paramSpec{{name: "unit", ty: ir.TString}},
			src:    binaryRV(mir.BinGeq, paramCopy(1, ir.TString), stringConst("A"), ir.TBool),
		}),
	)
	for _, want := range []string{
		"declare i64 @osty_rt_strings_Compare(ptr, ptr)",
		"define i1 @isIdentStart(ptr %unit)",
		"%stage0.string.cmp.0 = call i64 @osty_rt_strings_Compare(ptr %unit, ptr @.str.0)",
		"%stage0.string.cmp.pred.1 = icmp sge i64 %stage0.string.cmp.0, 0",
		"ret i1 %stage0.string.cmp.pred.1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0EmitsShortCircuitGuardReturn(t *testing.T) {
	t.Parallel()
	listInt := &ir.NamedType{Name: "List", Args: []ir.Type{ir.TInt}, Builtin: true}
	fn := &mir.Function{
		Name:        "safeAt",
		Params:      []mir.LocalID{1, 2},
		ReturnType:  ir.TInt,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: ir.TInt, IsReturn: true},
			{ID: 1, Name: "xs", Type: listInt, IsParam: true},
			{ID: 2, Name: "idx", Type: ir.TInt, IsParam: true},
			{ID: 3, Name: "guard", Type: ir.TBool},
			{ID: 4, Name: "negative", Type: ir.TBool},
			{ID: 5, Name: "len", Type: ir.TInt},
			{ID: 6, Name: "pastEnd", Type: ir.TBool},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{
			{
				ID: 0,
				Instrs: []mir.Instr{
					assign(4, binaryRV(mir.BinLt, paramCopy(2, ir.TInt), intConst(0), ir.TBool)),
				},
				Term: &mir.BranchTerm{Cond: paramCopy(4, ir.TBool), Then: 1, Else: 2},
			},
			{ID: 1, Instrs: []mir.Instr{assign(3, useRV(boolConst(true)))}, Term: &mir.GotoTerm{Target: 3}},
			{
				ID: 2,
				Instrs: []mir.Instr{
					&mir.IntrinsicInstr{Dest: &mir.Place{Local: 5}, Kind: mir.IntrinsicListLen, Args: []mir.Operand{paramCopy(1, listInt)}},
					assign(3, binaryRV(mir.BinGeq, paramCopy(2, ir.TInt), paramCopy(5, ir.TInt), ir.TBool)),
				},
				Term: &mir.GotoTerm{Target: 3},
			},
			{ID: 3, Term: &mir.BranchTerm{Cond: paramCopy(3, ir.TBool), Then: 4, Else: 5}},
			{ID: 4, Instrs: []mir.Instr{assign(0, useRV(intConst(-1)))}, Term: &mir.ReturnTerm{}},
			{ID: 5, Term: &mir.GotoTerm{Target: 6}},
			{
				ID: 6,
				Instrs: []mir.Instr{
					assign(0, useRV(&mir.CopyOp{
						Place: mir.Place{
							Local: 1,
							Projections: []mir.Projection{
								&mir.IndexProj{Index: paramCopy(2, ir.TInt), ElemType: ir.TInt},
							},
						},
						T: ir.TInt,
					})),
				},
				Term: &mir.ReturnTerm{},
			},
		},
	}
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"define i64 @safeAt(ptr %xs, i64 %idx)",
		"%0 = icmp slt i64 %idx, 0",
		"br i1 %0, label %guard.then.1, label %guard.else.2",
		"guard.merge.3:",
		"phi i1 [true, %guard.then.1], [%2, %guard.else.2]",
		"%1 = call i64 @osty_rt_list_len(ptr %xs)",
		"%2 = icmp sge i64 %idx, %1",
		"return.4:",
		"ret i64 -1",
		"return.6:",
		"call i64 @osty_rt_list_get_i64(ptr %xs, i64 %idx)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0EmitsShortCircuitBoolReturn(t *testing.T) {
	t.Parallel()
	fn := &mir.Function{
		Name:        "isTool",
		Params:      []mir.LocalID{1},
		ReturnType:  ir.TBool,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: ir.TBool, IsReturn: true},
			{ID: 1, Name: "name", Type: ir.TString, IsParam: true},
			{ID: 2, Name: "first", Type: ir.TBool},
			{ID: 3, Name: "combined", Type: ir.TBool},
			{ID: 4, Name: "tail", Type: ir.TBool},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{
			{
				ID: 0,
				Instrs: []mir.Instr{
					assign(2, binaryRV(mir.BinEq, paramCopy(1, ir.TString), stringConst("check"), ir.TBool)),
				},
				Term: &mir.BranchTerm{Cond: paramCopy(2, ir.TBool), Then: 1, Else: 2},
			},
			{ID: 1, Instrs: []mir.Instr{assign(3, useRV(boolConst(true)))}, Term: &mir.GotoTerm{Target: 3}},
			{
				ID: 2,
				Instrs: []mir.Instr{
					assign(3, binaryRV(mir.BinEq, paramCopy(1, ir.TString), stringConst("lint"), ir.TBool)),
				},
				Term: &mir.GotoTerm{Target: 3},
			},
			{ID: 3, Term: &mir.BranchTerm{Cond: paramCopy(3, ir.TBool), Then: 4, Else: 5}},
			{ID: 4, Instrs: []mir.Instr{assign(0, useRV(boolConst(true)))}, Term: &mir.GotoTerm{Target: 6}},
			{
				ID: 5,
				Instrs: []mir.Instr{
					assign(0, binaryRV(mir.BinEq, paramCopy(1, ir.TString), stringConst("resolve"), ir.TBool)),
				},
				Term: &mir.GotoTerm{Target: 6},
			},
			{ID: 6, Term: &mir.ReturnTerm{}},
		},
	}
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"define i1 @isTool(ptr %name)",
		"%0 = call i1 @osty_rt_strings_Equal(ptr %name, ptr @.str.0)",
		"br i1 %0, label %or.then.1, label %or.else.2",
		"or.merge.3:",
		"phi i1 [true, %or.then.1], [%1, %or.else.2]",
		"%1 = call i1 @osty_rt_strings_Equal(ptr %name, ptr @.str.1)",
		"br i1 %stage0.or.",
		"or.merge.6:",
		"phi i1 [true, %or.then.4], [%2, %or.else.5]",
		"%2 = call i1 @osty_rt_strings_Equal(ptr %name, ptr @.str.2)",
		"ret i1 %stage0.or.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0EmitsScalarReturnChainWithGotoArms(t *testing.T) {
	t.Parallel()
	fn := &mir.Function{
		Name:        "containsOffset",
		Params:      []mir.LocalID{1, 2, 3},
		ReturnType:  ir.TBool,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: ir.TBool, IsReturn: true},
			{ID: 1, Name: "start", Type: ir.TInt, IsParam: true},
			{ID: 2, Name: "end", Type: ir.TInt, IsParam: true},
			{ID: 3, Name: "offset", Type: ir.TInt, IsParam: true},
			{ID: 4, Name: "before", Type: ir.TBool},
			{ID: 5, Name: "inside", Type: ir.TBool},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{
			{
				ID: 0,
				Instrs: []mir.Instr{
					assign(4, binaryRV(mir.BinLt, paramCopy(3, ir.TInt), paramCopy(1, ir.TInt), ir.TBool)),
				},
				Term: &mir.BranchTerm{Cond: paramCopy(4, ir.TBool), Then: 1, Else: 2},
			},
			{ID: 1, Instrs: []mir.Instr{assign(0, useRV(boolConst(false)))}, Term: &mir.ReturnTerm{}},
			{ID: 2, Term: &mir.GotoTerm{Target: 3}},
			{
				ID: 3,
				Instrs: []mir.Instr{
					assign(5, binaryRV(mir.BinLt, paramCopy(3, ir.TInt), paramCopy(2, ir.TInt), ir.TBool)),
				},
				Term: &mir.BranchTerm{Cond: paramCopy(5, ir.TBool), Then: 4, Else: 5},
			},
			{ID: 4, Instrs: []mir.Instr{assign(0, useRV(boolConst(true)))}, Term: &mir.GotoTerm{Target: 6}},
			{ID: 5, Instrs: []mir.Instr{assign(0, useRV(boolConst(false)))}, Term: &mir.GotoTerm{Target: 6}},
			{ID: 6, Term: &mir.ReturnTerm{}},
		},
	}
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"define i1 @containsOffset(i64 %start, i64 %end, i64 %offset)",
		"%0 = icmp slt i64 %offset, %start",
		"br i1 %0, label %return.1, label %chain.3",
		"return.1:",
		"ret i1 false",
		"chain.3:",
		"%1 = icmp slt i64 %offset, %end",
		"br i1 %1, label %return.4, label %return.5",
		"return.4:",
		"ret i1 true",
		"return.5:",
		"ret i1 false",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

// TestStage0EmitsScalarReturnChainHighParamCount exercises the param cap
// raised from 8 → 32 in matchScalarReturnChain. Models a 12-param
// `mirLegacyAssignOpCode`-shaped enum-mapping helper:
// `if op == kEq { return tEq } else if op == kAdd { return tAdd } ...`.
// Without the cap raise, the function declines on `len(fn.Params) > 8`
// before the body walk even runs.
func TestStage0EmitsScalarReturnChainHighParamCount(t *testing.T) {
	t.Parallel()
	// 12 params: op + 5 enum kinds + 5 enum tokens + default. Each `if op == kN`
	// returns the matching token; final block returns the default.
	const armCount = 5
	locals := []*mir.Local{
		{ID: 0, Name: "ret", Type: ir.TInt, IsReturn: true},
		{ID: 1, Name: "op", Type: ir.TInt, IsParam: true},
	}
	params := []mir.LocalID{1}
	// 5 kinds (k1..k5)
	for i := 0; i < armCount; i++ {
		id := mir.LocalID(2 + i)
		locals = append(locals, &mir.Local{ID: id, Name: fmt.Sprintf("k%d", i+1), Type: ir.TInt, IsParam: true})
		params = append(params, id)
	}
	// 5 tokens (t1..t5)
	for i := 0; i < armCount; i++ {
		id := mir.LocalID(2 + armCount + i)
		locals = append(locals, &mir.Local{ID: id, Name: fmt.Sprintf("t%d", i+1), Type: ir.TInt, IsParam: true})
		params = append(params, id)
	}
	// default (token0)
	defaultID := mir.LocalID(2 + 2*armCount)
	locals = append(locals, &mir.Local{ID: defaultID, Name: "tDefault", Type: ir.TInt, IsParam: true})
	params = append(params, defaultID)
	// Cond locals — one per arm.
	for i := 0; i < armCount; i++ {
		locals = append(locals, &mir.Local{ID: mir.LocalID(20 + i), Name: fmt.Sprintf("cond%d", i+1), Type: ir.TBool})
	}

	// Build the CFG: entry → branch(cond1) → return.1 | chain.2 → branch(cond2) → return.3 | chain.4 → ... → final
	// Each arm: 3 blocks (cond block, then-return, else-goto-next).
	blocks := []*mir.BasicBlock{}
	for i := 0; i < armCount; i++ {
		condBlockID := mir.BlockID(3 * i)
		thenBlockID := mir.BlockID(3*i + 1)
		elseBlockID := mir.BlockID(3*i + 2)
		nextChainID := mir.BlockID(3 * (i + 1))
		condLocalID := mir.LocalID(20 + i)
		kindParamID := mir.LocalID(2 + i)
		tokenParamID := mir.LocalID(2 + armCount + i)
		// Cond block: assign condN = (op == kN), branch.
		blocks = append(blocks, &mir.BasicBlock{
			ID: condBlockID,
			Instrs: []mir.Instr{
				assign(condLocalID, binaryRV(mir.BinEq, paramCopy(1, ir.TInt), paramCopy(kindParamID, ir.TInt), ir.TBool)),
			},
			Term: &mir.BranchTerm{Cond: paramCopy(condLocalID, ir.TBool), Then: thenBlockID, Else: elseBlockID},
		})
		// Then: return tN.
		blocks = append(blocks, &mir.BasicBlock{
			ID:     thenBlockID,
			Instrs: []mir.Instr{assign(0, useRV(paramCopy(tokenParamID, ir.TInt)))},
			Term:   &mir.ReturnTerm{},
		})
		// Else: goto next chain.
		blocks = append(blocks, &mir.BasicBlock{
			ID:   elseBlockID,
			Term: &mir.GotoTerm{Target: nextChainID},
		})
	}
	// Final block: return default.
	finalID := mir.BlockID(3 * armCount)
	blocks = append(blocks, &mir.BasicBlock{
		ID:     finalID,
		Instrs: []mir.Instr{assign(0, useRV(paramCopy(defaultID, ir.TInt)))},
		Term:   &mir.ReturnTerm{},
	})

	fn := &mir.Function{
		Name:        "enumOpcode",
		Params:      params,
		ReturnType:  ir.TInt,
		ReturnLocal: 0,
		Locals:      locals,
		Entry:       0,
		Blocks:      blocks,
	}
	got := emit(t, trivialMainFn(), fn)

	// Stage0 routes the function to *some* matcher (scalar-return-chain or
	// the generic-CFG fallback). Either is fine for this regression — what
	// matters is that the >8-param ceiling no longer rejects out-of-hand.
	// Verify (a) the 12-param signature renders with the source names (no
	// fallback `p0`/`p1` since every param has a real `loc.Name`), and
	// (b) every arm + default token reaches the IR.
	for _, want := range []string{
		"define i64 @enumOpcode(i64 %op, i64 %k1, i64 %k2, i64 %k3, i64 %k4, i64 %k5, i64 %t1, i64 %t2, i64 %t3, i64 %t4, i64 %t5, i64 %tDefault)",
		"icmp eq i64 %op, %k1",
		"icmp eq i64 %op, %k5",
		"%t1",
		"%t5",
		"%tDefault",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

// ---- P3c: if-else with phi-merged return ----

// makeIfElseFn assembles the canonical 4-block if-else MIR shape.
//
//	entry: cond = entryInstrs...; branch cond -> then, else
//	then : thenInstrs (last AssignInstr writes ret); goto merge
//	else : elseInstrs (last AssignInstr writes ret); goto merge
//	merge: ret
func makeIfElseFn(name string, retT mir.Type, params []paramSpec, extraLocals []paramSpec, entryInstrs []mir.Instr, cond mir.Operand, thenInstrs []mir.Instr, elseInstrs []mir.Instr) *mir.Function {
	locals := []*mir.Local{
		{ID: 0, Name: "ret", Type: retT, IsReturn: true},
	}
	paramIDs := make([]mir.LocalID, 0, len(params))
	nextID := mir.LocalID(1)
	for _, p := range params {
		paramIDs = append(paramIDs, nextID)
		locals = append(locals, &mir.Local{ID: nextID, Name: p.name, Type: p.ty, IsParam: true})
		nextID++
	}
	for _, l := range extraLocals {
		locals = append(locals, &mir.Local{ID: nextID, Name: l.name, Type: l.ty})
		nextID++
	}
	return &mir.Function{
		Name:        name,
		Params:      paramIDs,
		ReturnType:  retT,
		ReturnLocal: 0,
		Locals:      locals,
		Entry:       0,
		Blocks: []*mir.BasicBlock{
			{ID: 0, Instrs: entryInstrs, Term: &mir.BranchTerm{Cond: cond, Then: 1, Else: 2}},
			{ID: 1, Instrs: thenInstrs, Term: &mir.GotoTerm{Target: 3}},
			{ID: 2, Instrs: elseInstrs, Term: &mir.GotoTerm{Target: 3}},
			{ID: 3, Instrs: nil, Term: &mir.ReturnTerm{}},
		},
	}
}

func TestStage0EmitsIfElseConstReturns(t *testing.T) {
	t.Parallel()
	// `fn sign(x: Int) -> Int { if x > 0 { 1 } else { -1 } }`
	// MIR: entry has cond temp; then assigns 1 to ret; else assigns -1.
	fn := makeIfElseFn(
		"sign",
		ir.TInt,
		[]paramSpec{{name: "x", ty: ir.TInt}},
		[]paramSpec{{name: "cond", ty: ir.TBool}},
		[]mir.Instr{
			assign(2, binaryRV(mir.BinGt, paramCopy(1, ir.TInt), intConst(0), ir.TBool)),
		},
		paramCopy(2, ir.TBool),
		[]mir.Instr{
			assign(0, useRV(intConst(1))),
		},
		[]mir.Instr{
			assign(0, useRV(intConst(-1))),
		},
	)
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"define i64 @sign(i64 %x)",
		"entry:",
		"%0 = icmp sgt i64 %x, 0",
		"br i1 %0, label %then.1, label %else.2",
		"then.1:",
		"br label %merge.3",
		"else.2:",
		"br label %merge.3",
		"merge.3:",
		"%retval = phi i64 [ 1, %then.1 ], [ -1, %else.2 ]",
		"ret i64 %retval",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0EmitsIfElseWithBranchArith(t *testing.T) {
	t.Parallel()
	// `fn abs(x: Int) -> Int { if x < 0 { 0 - x } else { x } }`
	fn := makeIfElseFn(
		"abs",
		ir.TInt,
		[]paramSpec{{name: "x", ty: ir.TInt}},
		[]paramSpec{
			{name: "cond", ty: ir.TBool},
			{name: "neg", ty: ir.TInt},
		},
		[]mir.Instr{
			assign(2, binaryRV(mir.BinLt, paramCopy(1, ir.TInt), intConst(0), ir.TBool)),
		},
		paramCopy(2, ir.TBool),
		[]mir.Instr{
			assign(3, binaryRV(mir.BinSub, intConst(0), paramCopy(1, ir.TInt), ir.TInt)),
			assign(0, useRV(paramCopy(3, ir.TInt))),
		},
		[]mir.Instr{
			assign(0, useRV(paramCopy(1, ir.TInt))),
		},
	)
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"%0 = icmp slt i64 %x, 0",
		"br i1 %0, label %then.1, label %else.2",
		"then.1:",
		"%1 = sub i64 0, %x",
		"%retval = phi i64 [ %1, %then.1 ], [ %x, %else.2 ]",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0EmitsIfElseBoolReturn(t *testing.T) {
	t.Parallel()
	// `fn flip(b: Bool) -> Bool { if b { false } else { true } }`
	fn := makeIfElseFn(
		"flip",
		ir.TBool,
		[]paramSpec{{name: "b", ty: ir.TBool}},
		nil,
		nil, // no entry instructions; cond reads param directly
		paramCopy(1, ir.TBool),
		[]mir.Instr{
			assign(0, useRV(boolConst(false))),
		},
		[]mir.Instr{
			assign(0, useRV(boolConst(true))),
		},
	)
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"define i1 @flip(i1 %b)",
		"br i1 %b, label %then.1, label %else.2",
		"%retval = phi i1 [ false, %then.1 ], [ true, %else.2 ]",
		"ret i1 %retval",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0EmitsIfElseWithCallInBranch(t *testing.T) {
	t.Parallel()
	doubleFn := makeFn(fnSpec{
		name:   "double",
		retT:   ir.TInt,
		params: []paramSpec{{name: "x", ty: ir.TInt}},
		src:    binaryRV(mir.BinMul, paramCopy(1, ir.TInt), intConst(2), ir.TInt),
	})
	// `fn maybe_double(x: Int, flag: Bool) -> Int { if flag { double(x) } else { x } }`
	fn := makeIfElseFn(
		"maybe_double",
		ir.TInt,
		[]paramSpec{{name: "x", ty: ir.TInt}, {name: "flag", ty: ir.TBool}},
		[]paramSpec{{name: "d", ty: ir.TInt}},
		nil,
		paramCopy(2, ir.TBool),
		[]mir.Instr{
			callInstr(3, "double", fnTy(ir.TInt, ir.TInt), paramCopy(1, ir.TInt)),
			assign(0, useRV(paramCopy(3, ir.TInt))),
		},
		[]mir.Instr{
			assign(0, useRV(paramCopy(1, ir.TInt))),
		},
	)
	got := emit(t, trivialMainFn(), doubleFn, fn)
	for _, want := range []string{
		"br i1 %flag, label %then.1, label %else.2",
		"%0 = call i64 @double(i64 %x)",
		"%retval = phi i64 [ %0, %then.1 ], [ %x, %else.2 ]",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0EmitsIfElseWithEntryArith(t *testing.T) {
	t.Parallel()
	// `fn classify(x: Int, y: Int) -> Int { let s = x + y; if s > 0 { s } else { 0 } }`
	fn := makeIfElseFn(
		"classify",
		ir.TInt,
		[]paramSpec{{name: "x", ty: ir.TInt}, {name: "y", ty: ir.TInt}},
		[]paramSpec{
			{name: "s", ty: ir.TInt},
			{name: "cond", ty: ir.TBool},
		},
		[]mir.Instr{
			assign(3, binaryRV(mir.BinAdd, paramCopy(1, ir.TInt), paramCopy(2, ir.TInt), ir.TInt)),
			assign(4, binaryRV(mir.BinGt, paramCopy(3, ir.TInt), intConst(0), ir.TBool)),
		},
		paramCopy(4, ir.TBool),
		[]mir.Instr{
			assign(0, useRV(paramCopy(3, ir.TInt))),
		},
		[]mir.Instr{
			assign(0, useRV(intConst(0))),
		},
	)
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"%0 = add i64 %x, %y",
		"%1 = icmp sgt i64 %0, 0",
		"br i1 %1, label %then.1, label %else.2",
		// `s` is the entry-block result %0; then branch reads it.
		"%retval = phi i64 [ %0, %then.1 ], [ 0, %else.2 ]",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

// ---- P3c rejection paths ----

func TestStage0RejectsThreeBlockShape(t *testing.T) {
	t.Parallel()
	// 3 blocks instead of 4 — declines.
	fn := &mir.Function{
		Name:        "bad",
		Params:      []mir.LocalID{1},
		ReturnType:  ir.TInt,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: ir.TInt, IsReturn: true},
			{ID: 1, Name: "x", Type: ir.TInt, IsParam: true},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{
			{ID: 0, Term: &mir.BranchTerm{Cond: paramCopy(1, ir.TInt), Then: 1, Else: 1}},
			{ID: 1, Instrs: []mir.Instr{assign(0, useRV(paramCopy(1, ir.TInt)))}, Term: &mir.ReturnTerm{}},
		},
	}
	mustReject(t, trivialMainFn(), fn)
}

func TestStage0RejectsIfElseWhereOneBranchSkipsRet(t *testing.T) {
	t.Parallel()
	// else branch never assigns to ret — declines.
	fn := makeIfElseFn(
		"bad",
		ir.TInt,
		[]paramSpec{{name: "x", ty: ir.TInt}},
		nil,
		nil,
		paramCopy(1, ir.TInt), // bad — Int as cond
		[]mir.Instr{assign(0, useRV(intConst(1)))},
		nil, // no ret assign in else
	)
	mustReject(t, trivialMainFn(), fn)
}

func TestStage0RejectsIfElseWithIntCondition(t *testing.T) {
	t.Parallel()
	fn := makeIfElseFn(
		"bad",
		ir.TInt,
		[]paramSpec{{name: "x", ty: ir.TInt}},
		nil,
		nil,
		paramCopy(1, ir.TInt), // Int condition
		[]mir.Instr{assign(0, useRV(intConst(1)))},
		[]mir.Instr{assign(0, useRV(intConst(0)))},
	)
	mustReject(t, trivialMainFn(), fn)
}

func TestStage0EmitsIfElseWhereThenElseTargetsDiffer(t *testing.T) {
	t.Parallel()
	// Manually construct: then goes to block 3, else goes to block 4. No common merge.
	// The scalar return-chain matcher now emits both arms as direct returns.
	fn := &mir.Function{
		Name:        "bad",
		Params:      []mir.LocalID{1},
		ReturnType:  ir.TInt,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: ir.TInt, IsReturn: true},
			{ID: 1, Name: "b", Type: ir.TBool, IsParam: true},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{
			{ID: 0, Term: &mir.BranchTerm{Cond: paramCopy(1, ir.TBool), Then: 1, Else: 2}},
			{ID: 1, Instrs: []mir.Instr{assign(0, useRV(intConst(1)))}, Term: &mir.GotoTerm{Target: 3}},
			{ID: 2, Instrs: []mir.Instr{assign(0, useRV(intConst(2)))}, Term: &mir.GotoTerm{Target: 4}}, // different target
			{ID: 3, Term: &mir.ReturnTerm{}},
			{ID: 4, Term: &mir.ReturnTerm{}},
		},
	}
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"define i64 @bad(i1 %b)",
		"br i1 %b, label %return.1, label %return.2",
		"return.1:",
		"ret i64 1",
		"return.2:",
		"ret i64 2",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0GenericCFGEmitsIfElseWithMergeInstructions(t *testing.T) {
	t.Parallel()
	fn := makeIfElseFn(
		"mergeValue",
		ir.TInt,
		[]paramSpec{{name: "b", ty: ir.TBool}},
		nil,
		nil,
		paramCopy(1, ir.TBool),
		[]mir.Instr{assign(0, useRV(intConst(1)))},
		[]mir.Instr{assign(0, useRV(intConst(2)))},
	)
	// Inject an instruction into the merge block (id 3).
	fn.Blocks[3].Instrs = []mir.Instr{assign(0, useRV(intConst(99)))}
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"define i64 @mergeValue(i1 %b)",
		"%ret.slot = alloca i64",
		"br i1 %b, label %bb.1, label %bb.2",
		"bb.3:",
		"store i64 99, ptr %ret.slot",
		"ret i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0RejectsCallParamTypeMismatch(t *testing.T) {
	t.Parallel()
	doubleFn := makeFn(fnSpec{
		name:   "double",
		retT:   ir.TInt,
		params: []paramSpec{{name: "x", ty: ir.TInt}},
		src:    binaryRV(mir.BinMul, paramCopy(1, ir.TInt), intConst(2), ir.TInt),
	})
	// Pass a Bool as the Int param.
	caller := makeMultiInstrFn(
		"bad",
		ir.TInt,
		[]paramSpec{{name: "flag", ty: ir.TBool}},
		[]paramSpec{{name: "r", ty: ir.TInt}},
		[]mir.Instr{
			callInstr(2, "double", fnTy(ir.TInt, ir.TInt), paramCopy(1, ir.TBool)),
			assign(0, useRV(paramCopy(2, ir.TInt))),
		},
	)
	mustReject(t, trivialMainFn(), doubleFn, caller)
}

// ---- P6: while-loop with stack-allocated mutable locals ----

// makeWhileLoopFn assembles the canonical 4-block while-loop MIR
// shape used by the front-end:
//
//	entry  : pre-loop instructions + GotoTerm(header)
//	header : header instructions + BranchTerm(cond, body, exit)
//	body   : loop body + GotoTerm(header)
//	exit   : post-loop instructions + ReturnTerm
func makeWhileLoopFn(name string, retT mir.Type, params []paramSpec, allLocals []localSpec, entryInstrs, headerInstrs []mir.Instr, cond mir.Operand, bodyInstrs, exitInstrs []mir.Instr) *mir.Function {
	locals := []*mir.Local{
		{ID: 0, Name: "ret", Type: retT, IsReturn: true, Mut: true},
	}
	paramIDs := make([]mir.LocalID, 0, len(params))
	nextID := mir.LocalID(1)
	for _, p := range params {
		paramIDs = append(paramIDs, nextID)
		locals = append(locals, &mir.Local{ID: nextID, Name: p.name, Type: p.ty, IsParam: true})
		nextID++
	}
	for _, l := range allLocals {
		locals = append(locals, &mir.Local{ID: nextID, Name: l.name, Type: l.ty, Mut: l.mut})
		nextID++
	}
	return &mir.Function{
		Name:        name,
		Params:      paramIDs,
		ReturnType:  retT,
		ReturnLocal: 0,
		Locals:      locals,
		Entry:       0,
		Blocks: []*mir.BasicBlock{
			{ID: 0, Instrs: entryInstrs, Term: &mir.GotoTerm{Target: 1}},
			{ID: 1, Instrs: headerInstrs, Term: &mir.BranchTerm{Cond: cond, Then: 2, Else: 3}},
			{ID: 2, Instrs: bodyInstrs, Term: &mir.GotoTerm{Target: 1}},
			{ID: 3, Instrs: exitInstrs, Term: &mir.ReturnTerm{}},
		},
	}
}

type localSpec struct {
	name string
	ty   mir.Type
	mut  bool
}

func TestStage0EmitsCountToWhileLoop(t *testing.T) {
	t.Parallel()
	// `fn count_to(n: Int) -> Int { let mut acc = 0; while acc < n { acc = acc + 1 } acc }`
	// Locals: 0 ret (mut), 1 n param, 2 acc (mut), 3 cond (immut)
	fn := makeWhileLoopFn(
		"count_to",
		ir.TInt,
		[]paramSpec{{name: "n", ty: ir.TInt}},
		[]localSpec{
			{name: "acc", ty: ir.TInt, mut: true},
			{name: "cond", ty: ir.TBool},
		},
		[]mir.Instr{
			assign(2, useRV(intConst(0))),
		},
		[]mir.Instr{
			assign(3, binaryRV(mir.BinLt, paramCopy(2, ir.TInt), paramCopy(1, ir.TInt), ir.TBool)),
		},
		paramCopy(3, ir.TBool),
		[]mir.Instr{
			assign(2, binaryRV(mir.BinAdd, paramCopy(2, ir.TInt), intConst(1), ir.TInt)),
		},
		[]mir.Instr{
			assign(0, useRV(paramCopy(2, ir.TInt))),
		},
	)
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"define i64 @count_to(i64 %n)",
		"%acc.slot = alloca i64",
		"store i64 0, ptr %acc.slot",
		"br label %header.1",
		"header.1:",
		"= load i64, ptr %acc.slot",
		"icmp slt i64",
		"br i1 ",
		"body.2:",
		"add i64 ",
		"store i64 ",
		"exit.3:",
		"ret i64 ",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0P27WhileVoidIntrinsics(t *testing.T) {
	t.Parallel()
	listInt := &ir.NamedType{Name: "List", Args: []ir.Type{ir.TInt}, Builtin: true}
	fn := makeWhileLoopFn(
		"pushWhileCounting",
		ir.TInt,
		[]paramSpec{{name: "items", ty: listInt}, {name: "n", ty: ir.TInt}},
		[]localSpec{
			{name: "i", ty: ir.TInt, mut: true},
			{name: "cond", ty: ir.TBool},
		},
		[]mir.Instr{
			assign(3, useRV(intConst(0))),
		},
		[]mir.Instr{
			assign(4, binaryRV(mir.BinLt, paramCopy(3, ir.TInt), paramCopy(2, ir.TInt), ir.TBool)),
		},
		paramCopy(4, ir.TBool),
		[]mir.Instr{
			&mir.IntrinsicInstr{Kind: mir.IntrinsicListPush, Args: []mir.Operand{paramCopy(1, listInt), paramCopy(3, ir.TInt)}},
			&mir.IntrinsicInstr{Kind: mir.IntrinsicPrintln, Args: []mir.Operand{paramCopy(3, ir.TInt)}},
			assign(3, binaryRV(mir.BinAdd, paramCopy(3, ir.TInt), intConst(1), ir.TInt)),
		},
		[]mir.Instr{
			assign(0, useRV(paramCopy(3, ir.TInt))),
		},
	)
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"declare void @osty_rt_list_push_i64(ptr, i64)",
		"@.fmt.stage0.println.int = private unnamed_addr constant [6 x i8] c\"%lld\\0A\\00\"",
		"define i64 @pushWhileCounting(ptr %items, i64 %n)",
		"body.2:",
		"call void @osty_rt_list_push_i64(ptr %items, i64",
		"call i32 (ptr, ...) @printf(ptr @.fmt.stage0.println.int, i64",
		"ret i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0P27WhileValueIntrinsics(t *testing.T) {
	t.Parallel()
	listInt := &ir.NamedType{Name: "List", Args: []ir.Type{ir.TInt}, Builtin: true}
	fn := makeWhileLoopFn(
		"measureWhile",
		ir.TInt,
		[]paramSpec{{name: "items", ty: listInt}, {name: "keepGoing", ty: ir.TBool}},
		[]localSpec{
			{name: "acc", ty: ir.TInt, mut: true},
			{name: "len", ty: ir.TInt},
		},
		[]mir.Instr{
			assign(3, useRV(intConst(0))),
		},
		nil,
		paramCopy(2, ir.TBool),
		[]mir.Instr{
			&mir.IntrinsicInstr{Dest: &mir.Place{Local: 4}, Kind: mir.IntrinsicListLen, Args: []mir.Operand{paramCopy(1, listInt)}},
			assign(3, useRV(paramCopy(4, ir.TInt))),
		},
		[]mir.Instr{
			assign(0, useRV(paramCopy(3, ir.TInt))),
		},
	)
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"declare i64 @osty_rt_list_len(ptr)",
		"define i64 @measureWhile(ptr %items, i1 %keepGoing)",
		"br i1 %keepGoing, label %body.2, label %exit.3",
		"body.2:",
		"= call i64 @osty_rt_list_len(ptr %items)",
		"store i64",
		"ret i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0WhileMapGetInitializesMissPayload(t *testing.T) {
	t.Parallel()
	mapStringInt := &ir.NamedType{Name: "Map", Args: []ir.Type{ir.TString, ir.TInt}, Builtin: true}
	optInt := &ir.NamedType{Name: "Option", Args: []ir.Type{ir.TInt}, Builtin: true}
	fn := makeWhileLoopFn(
		"lookupWhile",
		optInt,
		[]paramSpec{{name: "items", ty: mapStringInt}, {name: "keepGoing", ty: ir.TBool}},
		[]localSpec{{name: "hit", ty: optInt}},
		nil,
		nil,
		paramCopy(2, ir.TBool),
		[]mir.Instr{
			&mir.IntrinsicInstr{Dest: &mir.Place{Local: 3}, Kind: mir.IntrinsicMapGet, Args: []mir.Operand{paramCopy(1, mapStringInt), stringConst("missing")}},
		},
		[]mir.Instr{
			assign(0, useRV(paramCopy(3, optInt))),
		},
	)
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"%stage0.Option.i64 = type { i64, i64 }",
		"declare i1 @osty_rt_map_get_string(ptr, ptr, ptr)",
		"define ptr @lookupWhile(ptr %items, i1 %keepGoing)",
		"body.2:",
		"%0 = alloca i64",
		"store i64 0, ptr %0",
		"%1 = call i1 @osty_rt_map_get_string(ptr %items, ptr @.str.0, ptr %0)",
		"%6 = load i64, ptr %0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "store i64 0, ptr %0") > strings.Index(got, "%1 = call i1 @osty_rt_map_get_string") {
		t.Fatalf("map_get miss payload initialization appears after runtime call:\n%s", got)
	}
}

func TestStage0P28WhileListIterationLoweringPieces(t *testing.T) {
	t.Parallel()
	listString := &ir.NamedType{Name: "List", Args: []ir.Type{ir.TString}, Builtin: true}
	fn := makeWhileLoopFn(
		"cloneStringListWhile",
		listString,
		[]paramSpec{{name: "src", ty: listString}},
		[]localSpec{
			{name: "out", ty: listString, mut: true},
			{name: "len", ty: ir.TInt},
			{name: "idx", ty: ir.TInt, mut: true},
			{name: "cond", ty: ir.TBool},
			{name: "elem", ty: ir.TString},
		},
		[]mir.Instr{
			assign(2, &mir.AggregateRV{Kind: mir.AggList, T: listString}),
			assign(3, &mir.LenRV{Place: mir.Place{Local: 1}, T: ir.TInt}),
			assign(4, useRV(intConst(0))),
		},
		[]mir.Instr{
			assign(5, binaryRV(mir.BinLt, paramCopy(4, ir.TInt), paramCopy(3, ir.TInt), ir.TBool)),
		},
		paramCopy(5, ir.TBool),
		[]mir.Instr{
			assign(6, useRV(&mir.CopyOp{
				Place: mir.Place{
					Local: 1,
					Projections: []mir.Projection{
						&mir.IndexProj{Index: paramCopy(4, ir.TInt), ElemType: ir.TString},
					},
				},
				T: ir.TString,
			})),
			&mir.IntrinsicInstr{Kind: mir.IntrinsicListPush, Args: []mir.Operand{paramCopy(2, listString), paramCopy(6, ir.TString)}},
			assign(4, binaryRV(mir.BinAdd, paramCopy(4, ir.TInt), intConst(1), ir.TInt)),
		},
		[]mir.Instr{
			assign(0, useRV(paramCopy(2, listString))),
		},
	)
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"declare ptr @osty_rt_list_new()",
		"declare i64 @osty_rt_list_len(ptr)",
		"declare ptr @osty_rt_list_get_string(ptr, i64)",
		"define ptr @cloneStringListWhile(ptr %src)",
		"= call ptr @osty_rt_list_new()",
		"= call i64 @osty_rt_list_len(ptr %src)",
		"= call ptr @osty_rt_list_get_string(ptr %src, i64",
		"call void @osty_rt_list_push_string(ptr",
		"ret ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0P28WhileErrTypedHelperCall(t *testing.T) {
	t.Parallel()
	resultTy := &ir.NamedType{Name: "SelfResolveResult"}
	fn := makeWhileLoopFn(
		"resolveBeforeLoop",
		resultTy,
		[]paramSpec{{name: "source", ty: ir.TString}, {name: "keepGoing", ty: ir.TBool}},
		[]localSpec{
			{name: "result", ty: resultTy},
		},
		[]mir.Instr{
			&mir.CallInstr{
				Dest:   &mir.Place{Local: 3},
				Callee: &mir.FnRef{Symbol: "selfResolveSource", Type: ir.ErrTypeVal},
				Args:   []mir.Operand{paramCopy(1, ir.TString)},
			},
		},
		nil,
		paramCopy(2, ir.TBool),
		nil,
		[]mir.Instr{
			assign(0, useRV(paramCopy(3, resultTy))),
		},
	)
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"declare ptr @selfResolveSource(ptr)",
		"define ptr @resolveBeforeLoop(ptr %source, i1 %keepGoing)",
		"%0 = call ptr @selfResolveSource(ptr %source)",
		"br i1 %keepGoing, label %body.2, label %exit.3",
		"ret ptr %0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0P29GenericCFGSwitchInt(t *testing.T) {
	t.Parallel()
	fn := &mir.Function{
		Name:        "tagCode",
		Params:      []mir.LocalID{1},
		ReturnType:  ir.TInt,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: ir.TInt, IsReturn: true},
			{ID: 1, Name: "tag", Type: ir.TInt, IsParam: true},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{
			{ID: 0, Term: &mir.SwitchIntTerm{
				Scrutinee: paramCopy(1, ir.TInt),
				Cases: []mir.SwitchCase{
					{Value: 1, Target: 1},
					{Value: 2, Target: 2},
				},
				Default: 3,
			}},
			{ID: 1, Instrs: []mir.Instr{assign(0, useRV(intConst(10)))}, Term: &mir.ReturnTerm{}},
			{ID: 2, Instrs: []mir.Instr{assign(0, useRV(intConst(20)))}, Term: &mir.ReturnTerm{}},
			{ID: 3, Instrs: []mir.Instr{assign(0, useRV(intConst(-1)))}, Term: &mir.ReturnTerm{}},
		},
	}
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"define i64 @tagCode(i64 %tag)",
		"switch i64 %tag, label %bb.3 [",
		"i64 1, label %bb.1",
		"i64 2, label %bb.2",
		"store i64 10, ptr %ret.slot",
		"store i64 20, ptr %ret.slot",
		"store i64 -1, ptr %ret.slot",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0P29GenericCFGBoolSyntheticReturnAccumulator(t *testing.T) {
	t.Parallel()
	fn := &mir.Function{
		Name:        "isFlag",
		Params:      []mir.LocalID{1},
		ReturnType:  ir.TBool,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "_return", Type: ir.TBool, IsReturn: true},
			{ID: 1, Name: "name", Type: ir.TString, IsParam: true},
			{ID: 2, Name: "cond", Type: ir.TBool},
			{ID: 3, Name: "out", Type: ir.TBool},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{
			{
				ID: 0,
				Instrs: []mir.Instr{
					assign(2, binaryRV(mir.BinEq, paramCopy(1, ir.TString), stringConst("A"), ir.TBool)),
				},
				Term: &mir.BranchTerm{Cond: localCopy(2, ir.TBool), Then: 1, Else: 2},
			},
			{
				ID:     1,
				Instrs: []mir.Instr{assign(3, useRV(boolConst(true)))},
				Term:   &mir.GotoTerm{Target: 3},
			},
			{
				ID: 2,
				Instrs: []mir.Instr{
					assign(3, binaryRV(mir.BinEq, paramCopy(1, ir.TString), stringConst("B"), ir.TBool)),
				},
				Term: &mir.GotoTerm{Target: 3},
			},
			{ID: 3, Term: &mir.UnreachableTerm{}},
		},
	}
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"declare i1 @osty_rt_strings_Equal(ptr, ptr)",
		"define i1 @isFlag(ptr %name)",
		"ret i1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0P29GenericCFGBoolSyntheticReturnCond(t *testing.T) {
	t.Parallel()
	fn := &mir.Function{
		Name:        "isRawPtr",
		Params:      []mir.LocalID{1},
		ReturnType:  ir.TBool,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "_return", Type: ir.TBool, IsReturn: true},
			{ID: 1, Name: "name", Type: ir.TString, IsParam: true},
			{ID: 2, Name: "cond", Type: ir.TBool},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{
			{
				ID: 0,
				Instrs: []mir.Instr{
					assign(2, binaryRV(mir.BinEq, paramCopy(1, ir.TString), stringConst("RawPtr"), ir.TBool)),
				},
				Term: &mir.BranchTerm{Cond: localCopy(2, ir.TBool), Then: 1, Else: 1},
			},
			{ID: 1, Term: &mir.UnreachableTerm{}},
		},
	}
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"declare i1 @osty_rt_strings_Equal(ptr, ptr)",
		"define i1 @isRawPtr(ptr %name)",
		"ret i1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0P29GenericCFGStringOptionCoalesceSyntheticReturn(t *testing.T) {
	t.Parallel()
	optString := &ir.OptionalType{Inner: ir.TString}
	fn := &mir.Function{
		Name:        "MirRuntimeDecls__signature",
		Params:      []mir.LocalID{1, 2},
		ReturnType:  ir.TString,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "_return", Type: ir.TString, IsReturn: true},
			{ID: 1, Name: "self", Type: &ir.NamedType{Name: "MirRuntimeDecls"}, IsParam: true},
			{ID: 2, Name: "name", Type: ir.TString, IsParam: true},
			{ID: 4, Name: "_coalesce", Type: optString},
			{ID: 5, Name: "", Type: ir.TInt},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{
			{
				ID: 0,
				Instrs: []mir.Instr{
					assign(4, &mir.AggregateRV{Kind: mir.AggEnumVariant, VariantIdx: 1, Fields: nil, T: optString}),
					assign(5, &mir.DiscriminantRV{Place: mir.Place{Local: 4}}),
				},
				Term: &mir.SwitchIntTerm{
					Scrutinee: localCopy(5, ir.TInt),
					Cases:     []mir.SwitchCase{{Value: 0, Target: 1}},
					Default:   1,
				},
			},
			{ID: 1, Term: &mir.UnreachableTerm{}},
		},
	}
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"%stage0.Option.ptr = type { i64, ptr }",
		"select i1",
		"ret ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0P29GenericCFGPreExitDiscardedIntrinsicReturnString(t *testing.T) {
	t.Parallel()
	fn := &mir.Function{
		Name:        "hirLowerStripSign",
		Params:      []mir.LocalID{1},
		ReturnType:  ir.TString,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "_return", Type: ir.TString, IsReturn: true},
			{ID: 1, Name: "text", Type: ir.TString, IsParam: true},
			{ID: 2, Name: "", Type: ir.TBool},
			{ID: 3, Name: "", Type: ir.TBool},
			{ID: 5, Name: "", Type: ir.TInt},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{
			{
				ID: 0,
				Instrs: []mir.Instr{
					&mir.IntrinsicInstr{
						Dest: &mir.Place{Local: 3},
						Kind: mir.IntrinsicStringStartsWith,
						Args: []mir.Operand{localCopy(1, ir.TString), stringConst("+")},
					},
				},
				Term: &mir.BranchTerm{Cond: localCopy(3, ir.TBool), Then: 1, Else: 2},
			},
			{
				ID:     1,
				Instrs: []mir.Instr{assign(2, useRV(boolConst(true)))},
				Term:   &mir.GotoTerm{Target: 3},
			},
			{
				ID: 2,
				Instrs: []mir.Instr{
					&mir.IntrinsicInstr{
						Dest: &mir.Place{Local: 2},
						Kind: mir.IntrinsicStringStartsWith,
						Args: []mir.Operand{localCopy(1, ir.TString), stringConst("-")},
					},
				},
				Term: &mir.GotoTerm{Target: 3},
			},
			{ID: 3, Term: &mir.BranchTerm{Cond: localCopy(2, ir.TBool), Then: 4, Else: 6}},
			{
				ID: 4,
				Instrs: []mir.Instr{
					&mir.IntrinsicInstr{Dest: &mir.Place{Local: 5}, Kind: mir.IntrinsicStringLen, Args: []mir.Operand{localCopy(1, ir.TString)}},
					&mir.IntrinsicInstr{Dest: nil, Kind: mir.IntrinsicStringSubstring, Args: []mir.Operand{localCopy(1, ir.TString), intConst(1), localCopy(5, ir.TInt)}},
				},
				Term: &mir.GotoTerm{Target: 6},
			},
			{ID: 5, Term: &mir.GotoTerm{Target: 6}},
			{ID: 6, Term: &mir.UnreachableTerm{}},
		},
	}
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"declare ptr @osty_rt_strings_Slice(ptr, i64, i64)",
		"call ptr @osty_rt_strings_Slice(",
		"ret ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0P29GenericCFGMapNewRecoversArgsFromStructLayout(t *testing.T) {
	t.Parallel()
	tomlValue := &ir.NamedType{Name: "TomlValue"}
	tomlTable := &ir.NamedType{Name: "TomlTable"}
	listString := &ir.NamedType{Name: "List", Builtin: true, Args: []ir.Type{ir.TString}}
	mapStringTomlValue := &ir.NamedType{Name: "Map", Builtin: true, Args: []ir.Type{ir.TString, tomlValue}}
	mapPoison := &ir.NamedType{Name: "Map", Builtin: true, Args: []ir.Type{ir.ErrTypeVal, ir.ErrTypeVal}}
	listPoison := &ir.NamedType{Name: "List", Builtin: true, Args: []ir.Type{ir.ErrTypeVal}}
	fn := &mir.Function{
		Name:        "newTomlTable",
		ReturnType:  tomlTable,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "_return", Type: tomlTable, IsReturn: true},
			{ID: 1, Name: "", Type: listPoison},
			{ID: 2, Name: "", Type: mapPoison},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{
			{
				ID: 0,
				Instrs: []mir.Instr{
					assign(1, &mir.AggregateRV{Kind: mir.AggList, Fields: nil, T: listPoison}),
					&mir.IntrinsicInstr{Dest: &mir.Place{Local: 2}, Kind: mir.IntrinsicMapNew, Args: nil},
					assign(0, &mir.AggregateRV{
						Kind:   mir.AggStruct,
						Fields: []mir.Operand{localCopy(1, listPoison), localCopy(2, mapPoison), boolConst(false), intConst(0)},
						T:      tomlTable,
					}),
				},
				Term: &mir.ReturnTerm{},
			},
		},
	}
	mod := moduleWith(trivialMainFn(), fn)
	mod.Layouts.Structs["TomlTable"] = &mir.StructLayout{
		Name: "TomlTable",
		Fields: []mir.FieldLayout{
			{Index: 0, Name: "keys", Type: listString},
			{Index: 1, Name: "items", Type: mapStringTomlValue},
			{Index: 2, Name: "inline", Type: ir.TBool},
			{Index: 3, Name: "line", Type: ir.TInt},
		},
	}
	got, err := EmitMIR(mod, llvmabi.Options{PackageName: "main"})
	if err != nil {
		t.Fatalf("EmitMIR: %v", err)
	}
	for _, want := range []string{
		"declare ptr @osty_rt_map_new(i64, i64, i64, ptr)",
		"call ptr @osty_rt_map_new(i64 5, i64 4, i64 8, ptr null)",
		"define ptr @newTomlTable()",
		"ret ptr",
	} {
		if !strings.Contains(string(got), want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, string(got))
		}
	}
}

func TestStage0P29GenericCFGBoolTerminalCondThroughGotoChain(t *testing.T) {
	t.Parallel()
	hirTypeKind := &ir.NamedType{Name: "HirTypeKind"}
	hirType := &ir.NamedType{Name: "HirType"}
	fn := &mir.Function{
		Name:        "hirTypeIsList",
		Params:      []mir.LocalID{1},
		ReturnType:  ir.TBool,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "_return", Type: ir.TBool, IsReturn: true},
			{ID: 1, Name: "t", Type: hirType, IsParam: true},
			{ID: 2, Name: "_scrut", Type: hirTypeKind},
			{ID: 3, Name: "", Type: ir.TInt},
			{ID: 5, Name: "", Type: ir.TBool},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{
			{
				ID: 0,
				Instrs: []mir.Instr{
					&mir.StorageLiveInstr{},
					assign(2, useRV(&mir.CopyOp{
						Place: mir.Place{Local: 1, Projections: []mir.Projection{&mir.FieldProj{Index: 0, Name: "kind", Type: hirTypeKind}}},
						T:     hirTypeKind,
					})),
					assign(3, &mir.DiscriminantRV{Place: mir.Place{Local: 2}}),
				},
				Term: &mir.SwitchIntTerm{
					Scrutinee: localCopy(3, ir.TInt),
					Cases:     []mir.SwitchCase{{Value: 2, Target: 2}},
					Default:   1,
				},
			},
			{
				ID: 1,
				Instrs: []mir.Instr{
					&mir.StorageDeadInstr{},
				},
				Term: &mir.UnreachableTerm{},
			},
			{
				ID: 2,
				Instrs: []mir.Instr{
					assign(5, binaryRV(mir.BinEq,
						&mir.CopyOp{Place: mir.Place{Local: 1, Projections: []mir.Projection{&mir.FieldProj{Index: 1, Name: "name", Type: ir.TString}}}, T: ir.TString},
						stringConst("List"),
						ir.TBool,
					)),
				},
				Term: &mir.BranchTerm{Cond: localCopy(5, ir.TBool), Then: 8, Else: 8},
			},
			{
				ID:     8,
				Instrs: []mir.Instr{&mir.StorageDeadInstr{}},
				Term:   &mir.GotoTerm{Target: 1},
			},
		},
	}
	mod := moduleWith(trivialMainFn(), fn)
	mod.Layouts.Enums["HirTypeKind"] = &mir.EnumLayout{
		Name:         "HirTypeKind",
		Discriminant: ir.TInt,
		Variants: []mir.VariantLayout{
			{Index: 0, Name: "A"},
			{Index: 1, Name: "B"},
			{Index: 2, Name: "List"},
		},
	}
	mod.Layouts.Structs["HirType"] = &mir.StructLayout{
		Name: "HirType",
		Fields: []mir.FieldLayout{
			{Index: 0, Name: "kind", Type: hirTypeKind},
			{Index: 1, Name: "name", Type: ir.TString},
		},
	}
	got, err := EmitMIR(mod, llvmabi.Options{PackageName: "main"})
	if err != nil {
		t.Fatalf("EmitMIR: %v", err)
	}
	for _, want := range []string{
		"define i1 @hirTypeIsList(ptr %t)",
		"ret i1",
	} {
		if !strings.Contains(string(got), want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, string(got))
		}
	}
}

func TestStage0P29GenericCFGStringNamedLocalSyntheticReturn(t *testing.T) {
	t.Parallel()
	fn := &mir.Function{
		Name:        "ostyAstParam",
		Params:      []mir.LocalID{1, 2},
		ReturnType:  ir.TString,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "_return", Type: ir.TString, IsReturn: true},
			{ID: 1, Name: "f", Type: &ir.NamedType{Name: "Fmt"}, IsParam: true},
			{ID: 2, Name: "idx", Type: ir.TInt, IsParam: true},
			{ID: 3, Name: "node", Type: &ir.NamedType{Name: "Node"}},
			{ID: 4, Name: "text", Type: ir.TString},
			{ID: 5, Name: "", Type: ir.TBool},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{
			{
				ID: 0,
				Instrs: []mir.Instr{
					assign(4, useRV(stringConst("a"))),
					assign(5, useRV(boolConst(true))),
				},
				Term: &mir.BranchTerm{Cond: localCopy(5, ir.TBool), Then: 1, Else: 2},
			},
			{
				ID:     1,
				Instrs: []mir.Instr{assign(4, useRV(stringConst("b")))},
				Term:   &mir.GotoTerm{Target: 3},
			},
			{
				ID:     2,
				Instrs: []mir.Instr{assign(4, useRV(stringConst("c")))},
				Term:   &mir.GotoTerm{Target: 3},
			},
			{ID: 3, Term: &mir.UnreachableTerm{}},
		},
	}
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"define ptr @ostyAstParam(ptr %f, i64 %idx)",
		"ret ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0P29GenericCFGTupleReturnProjection(t *testing.T) {
	t.Parallel()
	boxTy := &ir.NamedType{Name: "Box"}
	tupleTy := &ir.TupleType{Elems: []ir.Type{ir.TBool, boxTy}}
	fn := &mir.Function{
		Name:        "tupleOk",
		ReturnType:  ir.TBool,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: ir.TBool, IsReturn: true},
			{ID: 1, Name: "pair", Type: tupleTy},
			{ID: 2, Name: "ok", Type: ir.TBool},
			{ID: 3, Name: "box", Type: boxTy},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{
			{
				ID: 0,
				Instrs: []mir.Instr{
					&mir.CallInstr{
						Dest: &mir.Place{Local: 1},
						Callee: &mir.FnRef{
							Symbol: "makePair",
							Type:   &ir.FnType{Params: []ir.Type{ir.TInt}, Return: tupleTy},
						},
						Args: []mir.Operand{intConst(1)},
					},
					assign(2, useRV(&mir.CopyOp{
						Place: mir.Place{
							Local: 1,
							Projections: []mir.Projection{
								&mir.TupleProj{Index: 0, Type: ir.TBool},
							},
						},
						T: ir.TBool,
					})),
					assign(3, useRV(&mir.CopyOp{
						Place: mir.Place{
							Local: 1,
							Projections: []mir.Projection{
								&mir.TupleProj{Index: 1, Type: boxTy},
							},
						},
						T: boxTy,
					})),
					assign(0, useRV(paramCopy(2, ir.TBool))),
				},
				Term: &mir.ReturnTerm{},
			},
		},
	}
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"%.tuple.0 = type { i1, ptr }",
		"declare %.tuple.0 @makePair(i64)",
		"%0 = call %.tuple.0 @makePair(i64 1)",
		"%1 = extractvalue %.tuple.0 %0, 0",
		"%2 = extractvalue %.tuple.0 %0, 1",
		"ret i1 %4",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0OptionalAggregateRecoversPoisonTypeFromDest(t *testing.T) {
	t.Parallel()
	optString := &ir.OptionalType{Inner: ir.TString}
	fn := &mir.Function{
		Name:        "optFromPoison",
		ReturnType:  optString,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "_return", Type: optString, IsReturn: true},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{
			{
				ID: 0,
				Instrs: []mir.Instr{
					assign(0, &mir.AggregateRV{
						Kind:       mir.AggEnumVariant,
						VariantIdx: 0,
						Fields:     []mir.Operand{stringConst("hello")},
						T:          ir.ErrTypeVal,
					}),
				},
				Term: &mir.ReturnTerm{},
			},
		},
	}
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"%stage0.Option.ptr = type { i64, ptr }",
		"declare ptr @osty_rt_stage0_alloc(i64)",
		"define ptr @optFromPoison()",
		"store i64 0, ptr",
		"store ptr @.str.0, ptr",
		"ret ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0P29GenericCFGPreExitDiscardedCallReturnNamed(t *testing.T) {
	t.Parallel()
	hirBlock := &ir.NamedType{Name: "HirBlock"}
	hirSpan := &ir.NamedType{Name: "HirSpan"}
	listHirBlock := &ir.NamedType{Name: "List", Builtin: true, Args: []ir.Type{hirBlock}}
	fn := &mir.Function{
		Name:        "hirOptimizeFirstBlock",
		Params:      []mir.LocalID{1},
		ReturnType:  hirBlock,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "_return", Type: hirBlock, IsReturn: true},
			{ID: 1, Name: "xs", Type: listHirBlock, IsParam: true},
			{ID: 2, Name: "", Type: ir.TBool},
			{ID: 3, Name: "", Type: ir.TInt},
			{ID: 5, Name: "", Type: hirSpan},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{
			{
				ID: 0,
				Instrs: []mir.Instr{
					&mir.IntrinsicInstr{
						Dest: &mir.Place{Local: 3},
						Kind: mir.IntrinsicListLen,
						Args: []mir.Operand{&mir.CopyOp{Place: mir.Place{Local: 1}, T: listHirBlock}},
					},
					assign(2, binaryRV(mir.BinGt, localCopy(3, ir.TInt), intConst(0), ir.TBool)),
				},
				Term: &mir.BranchTerm{Cond: localCopy(2, ir.TBool), Then: 3, Else: 2},
			},
			{ID: 1, Term: &mir.GotoTerm{Target: 3}},
			{
				ID: 2,
				Instrs: []mir.Instr{
					&mir.CallInstr{
						Dest:   &mir.Place{Local: 5},
						Callee: &mir.FnRef{Symbol: "hirNoSpan", Type: ir.ErrTypeVal},
						Args:   nil,
					},
					&mir.CallInstr{
						Dest:   nil,
						Callee: &mir.FnRef{Symbol: "hirBlock", Type: ir.ErrTypeVal},
						Args:   []mir.Operand{&mir.CopyOp{Place: mir.Place{Local: 5}, T: hirSpan}},
					},
					&mir.StorageDeadInstr{},
				},
				Term: &mir.GotoTerm{Target: 3},
			},
			{ID: 3, Term: &mir.UnreachableTerm{}},
		},
	}
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"define ptr @hirOptimizeFirstBlock(ptr %xs)",
		"declare ptr @hirNoSpan()",
		"declare ptr @hirBlock(ptr)",
		"call ptr @hirBlock(",
		"ret ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0LowersUnitMinusInt(t *testing.T) {
	t.Parallel()
	fn := &mir.Function{
		Name:        "fallbackSlot",
		Params:      []mir.LocalID{1},
		ReturnType:  ir.TInt,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: ir.TInt, IsReturn: true},
			{ID: 1, Name: "ok", Type: ir.TBool, IsParam: true},
			{ID: 2, Name: "", Type: ir.TUnit},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{
			{ID: 0, Term: &mir.BranchTerm{Cond: paramCopy(1, ir.TBool), Then: 1, Else: 2}},
			{ID: 1, Instrs: []mir.Instr{assign(0, useRV(intConst(7)))}, Term: &mir.ReturnTerm{}},
			{ID: 2, Instrs: []mir.Instr{
				assign(0, binaryRV(mir.BinSub, localCopy(2, ir.TUnit), intConst(1), ir.TInt)),
			}, Term: &mir.ReturnTerm{}},
		},
	}
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"define i64 @fallbackSlot(i1 %ok)",
		"br i1 %ok, label %return.1, label %return.2",
		"%0 = sub i64 0, 1",
		"ret i64 %0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0P29GenericCFGSingleBlockFieldWrite(t *testing.T) {
	t.Parallel()
	boxTy := &ir.NamedType{Name: "Box"}
	fn := &mir.Function{
		Name:       "setBoxValue",
		Params:     []mir.LocalID{1, 2},
		ReturnType: ir.TUnit,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: ir.TUnit, IsReturn: true},
			{ID: 1, Name: "box", Type: boxTy, IsParam: true},
			{ID: 2, Name: "value", Type: ir.TInt, IsParam: true},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{
			{
				ID: 0,
				Instrs: []mir.Instr{
					&mir.AssignInstr{
						Dest: mir.Place{Local: 1, Projections: []mir.Projection{
							&mir.FieldProj{Index: 0, Name: "value", Type: ir.TInt},
						}},
						Src: useRV(paramCopy(2, ir.TInt)),
					},
				},
				Term: &mir.ReturnTerm{},
			},
		},
	}
	module := moduleWith(trivialMainFn(), fn)
	module.Layouts.Structs["Box"] = &mir.StructLayout{
		Name: "Box",
		Fields: []mir.FieldLayout{
			{Index: 0, Name: "value", Type: ir.TInt},
		},
	}
	gotBytes, err := EmitMIR(module, llvmabi.Options{PackageName: "main"})
	if err != nil {
		t.Fatalf("EmitMIR: %v", err)
	}
	got := string(gotBytes)
	for _, want := range []string{
		"%Box = type { i64 }",
		"define void @setBoxValue(ptr %box, i64 %value)",
		"%stage0.field.store.slot.0 = getelementptr inbounds %Box, ptr %box, i32 0, i32 0",
		"store i64 %value, ptr %stage0.field.store.slot.0",
		"ret void",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0P29GenericCFGProjectedFieldIncrement(t *testing.T) {
	t.Parallel()
	boxTy := &ir.NamedType{Name: "Box"}
	fn := &mir.Function{
		Name:       "bumpBoxValue",
		Params:     []mir.LocalID{1},
		ReturnType: ir.TUnit,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: ir.TUnit, IsReturn: true},
			{ID: 1, Name: "box", Type: boxTy, IsParam: true},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{
			{
				ID: 0,
				Instrs: []mir.Instr{
					&mir.AssignInstr{
						Dest: mir.Place{Local: 1, Projections: []mir.Projection{
							&mir.FieldProj{Index: 0, Name: "value", Type: ir.TInt},
						}},
						Src: binaryRV(mir.BinAdd, &mir.CopyOp{
							Place: mir.Place{Local: 1, Projections: []mir.Projection{
								&mir.FieldProj{Index: 0, Name: "value", Type: ir.TInt},
							}},
							T: ir.TInt,
						}, intConst(1), ir.TInt),
					},
				},
				Term: &mir.ReturnTerm{},
			},
		},
	}
	module := moduleWith(trivialMainFn(), fn)
	module.Layouts.Structs["Box"] = &mir.StructLayout{
		Name:   "Box",
		Fields: []mir.FieldLayout{{Index: 0, Name: "value", Type: ir.TInt}},
	}
	gotBytes, err := EmitMIR(module, llvmabi.Options{PackageName: "main"})
	if err != nil {
		t.Fatalf("EmitMIR: %v", err)
	}
	got := string(gotBytes)
	for _, want := range []string{
		"define void @bumpBoxValue(ptr %box)",
		"getelementptr inbounds %Box, ptr %box, i32 0, i32 0",
		"load i64, ptr %stage0.field.slot.",
		"add i64 %",
		"store i64 %",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0P29GenericCFGIndexedFieldWrite(t *testing.T) {
	t.Parallel()
	listInt := &ir.NamedType{Name: "List", Builtin: true, Args: []ir.Type{ir.TInt}}
	tableTy := &ir.NamedType{Name: "Table"}
	fn := &mir.Function{
		Name:       "setTableValue",
		Params:     []mir.LocalID{1, 2, 3},
		ReturnType: ir.TUnit,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: ir.TUnit, IsReturn: true},
			{ID: 1, Name: "table", Type: tableTy, IsParam: true},
			{ID: 2, Name: "idx", Type: ir.TInt, IsParam: true},
			{ID: 3, Name: "value", Type: ir.TInt, IsParam: true},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{
			{
				ID: 0,
				Instrs: []mir.Instr{
					&mir.AssignInstr{
						Dest: mir.Place{Local: 1, Projections: []mir.Projection{
							&mir.FieldProj{Index: 0, Name: "values", Type: listInt},
							&mir.IndexProj{Index: paramCopy(2, ir.TInt), ElemType: ir.TInt},
						}},
						Src: useRV(paramCopy(3, ir.TInt)),
					},
				},
				Term: &mir.ReturnTerm{},
			},
		},
	}
	module := moduleWith(trivialMainFn(), fn)
	module.Layouts.Structs["Table"] = &mir.StructLayout{
		Name: "Table",
		Fields: []mir.FieldLayout{
			{Index: 0, Name: "values", Type: listInt},
		},
	}
	gotBytes, err := EmitMIR(module, llvmabi.Options{PackageName: "main"})
	if err != nil {
		t.Fatalf("EmitMIR: %v", err)
	}
	got := string(gotBytes)
	for _, want := range []string{
		"%Table = type { ptr }",
		"declare void @osty_rt_list_set_i64(ptr, i64, i64)",
		"getelementptr inbounds %Table, ptr %table, i32 0, i32 0",
		"load ptr, ptr %stage0.field.slot",
		"call void @osty_rt_list_set_i64(ptr %0, i64 %idx, i64 %value)",
		"ret void",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0P29GenericCFGIndexedStructFieldRead(t *testing.T) {
	t.Parallel()
	recTy := &ir.NamedType{Name: "InspectRecord"}
	listRec := &ir.NamedType{Name: "List", Builtin: true, Args: []ir.Type{recTy}}
	fn := &mir.Function{
		Name:        "inspectRecordStart",
		Params:      []mir.LocalID{1, 2},
		ReturnType:  ir.TInt,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: ir.TInt, IsReturn: true},
			{ID: 1, Name: "recs", Type: listRec, IsParam: true},
			{ID: 2, Name: "i", Type: ir.TInt, IsParam: true},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{
			{
				ID: 0,
				Instrs: []mir.Instr{
					assign(0, useRV(&mir.CopyOp{
						Place: mir.Place{Local: 1, Projections: []mir.Projection{
							&mir.IndexProj{Index: paramCopy(2, ir.TInt), ElemType: recTy},
							&mir.FieldProj{Index: 0, Name: "start", Type: ir.TInt},
						}},
						T: ir.TInt,
					})),
				},
				Term: &mir.ReturnTerm{},
			},
		},
	}
	module := moduleWith(trivialMainFn(), fn)
	module.Layouts.Structs["InspectRecord"] = &mir.StructLayout{
		Name: "InspectRecord",
		Fields: []mir.FieldLayout{
			{Index: 0, Name: "start", Type: ir.TInt},
			{Index: 1, Name: "kind", Type: ir.TString},
		},
	}
	gotBytes, err := EmitMIR(module, llvmabi.Options{PackageName: "main"})
	if err != nil {
		t.Fatalf("EmitMIR: %v", err)
	}
	got := string(gotBytes)
	for _, want := range []string{
		"%InspectRecord = type { i64, ptr }",
		"declare ptr @osty_rt_list_get_ptr(ptr, i64)",
		"%0 = call ptr @osty_rt_list_get_ptr(ptr %recs, i64 %i)",
		"getelementptr inbounds %InspectRecord, ptr %0, i32 0, i32 0",
		"ret i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0P29GenericCFGOptionPayloadProjection(t *testing.T) {
	t.Parallel()
	mapStringString := &ir.NamedType{Name: "Map", Builtin: true, Args: []ir.Type{ir.TString, ir.TString}}
	optString := &ir.OptionalType{Inner: ir.TString}
	fn := &mir.Function{
		Name:        "lookupSignature",
		Params:      []mir.LocalID{1, 2},
		ReturnType:  ir.TString,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: ir.TString, IsReturn: true},
			{ID: 1, Name: "signatures", Type: mapStringString, IsParam: true},
			{ID: 2, Name: "name", Type: ir.TString, IsParam: true},
			{ID: 3, Name: "maybe", Type: optString},
			{ID: 4, Name: "tag", Type: ir.TInt},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{
			{
				ID: 0,
				Instrs: []mir.Instr{
					&mir.IntrinsicInstr{Dest: &mir.Place{Local: 3}, Kind: mir.IntrinsicMapGet, Args: []mir.Operand{paramCopy(1, mapStringString), paramCopy(2, ir.TString)}},
					assign(4, &mir.DiscriminantRV{Place: mir.Place{Local: 3}, T: ir.TInt}),
				},
				Term: &mir.SwitchIntTerm{
					Scrutinee: localCopy(4, ir.TInt),
					Cases:     []mir.SwitchCase{{Value: 0, Target: 1, Label: "Some"}},
					Default:   2,
				},
			},
			{
				ID: 1,
				Instrs: []mir.Instr{
					assign(0, useRV(&mir.CopyOp{
						Place: mir.Place{Local: 3, Projections: []mir.Projection{
							&mir.VariantProj{Variant: 0, Name: "Some", FieldIdx: 0, Type: ir.TString},
						}},
						T: ir.TString,
					})),
				},
				Term: &mir.GotoTerm{Target: 3},
			},
			{
				ID:     2,
				Instrs: []mir.Instr{assign(0, useRV(stringConst("")))},
				Term:   &mir.GotoTerm{Target: 3},
			},
			{ID: 3, Term: &mir.ReturnTerm{}},
		},
	}
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"%stage0.Option.ptr = type { i64, ptr }",
		"declare i1 @osty_rt_map_get_string(ptr, ptr, ptr)",
		"define ptr @lookupSignature(ptr %signatures, ptr %name)",
		"store ptr %",
		"switch i64 %",
		"getelementptr inbounds %stage0.Option.ptr, ptr %",
		", i32 0, i32 1",
		"ret ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0P29GenericCFGNestedIndexedFieldListPush(t *testing.T) {
	t.Parallel()
	methodTy := &ir.NamedType{Name: "Method"}
	itemTy := &ir.NamedType{Name: "Item"}
	listItem := &ir.NamedType{Name: "List", Builtin: true, Args: []ir.Type{itemTy}}
	listMethod := &ir.NamedType{Name: "List", Builtin: true, Args: []ir.Type{methodTy}}
	outerTy := &ir.NamedType{Name: "Outer"}
	fn := &mir.Function{
		Name:       "appendMethod",
		Params:     []mir.LocalID{1, 2, 3},
		ReturnType: ir.TUnit,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: ir.TUnit, IsReturn: true},
			{ID: 1, Name: "outer", Type: outerTy, IsParam: true},
			{ID: 2, Name: "i", Type: ir.TInt, IsParam: true},
			{ID: 3, Name: "method", Type: methodTy, IsParam: true},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{
			{
				ID: 0,
				Instrs: []mir.Instr{
					&mir.IntrinsicInstr{
						Kind: mir.IntrinsicListPush,
						Args: []mir.Operand{
							&mir.CopyOp{
								Place: mir.Place{Local: 1, Projections: []mir.Projection{
									&mir.FieldProj{Index: 0, Name: "items", Type: listItem},
									&mir.IndexProj{Index: paramCopy(2, ir.TInt), ElemType: itemTy},
									&mir.FieldProj{Index: 0, Name: "methods", Type: listMethod},
								}},
								T: listMethod,
							},
							paramCopy(3, methodTy),
						},
					},
				},
				Term: &mir.ReturnTerm{},
			},
		},
	}
	module := moduleWith(trivialMainFn(), fn)
	module.Layouts.Structs["Outer"] = &mir.StructLayout{
		Name:   "Outer",
		Fields: []mir.FieldLayout{{Index: 0, Name: "items", Type: listItem}},
	}
	module.Layouts.Structs["Item"] = &mir.StructLayout{
		Name:   "Item",
		Fields: []mir.FieldLayout{{Index: 0, Name: "methods", Type: listMethod}},
	}
	gotBytes, err := EmitMIR(module, llvmabi.Options{PackageName: "main"})
	if err != nil {
		t.Fatalf("EmitMIR: %v", err)
	}
	got := string(gotBytes)
	for _, want := range []string{
		"%Outer = type { ptr }",
		"%Item = type { ptr }",
		"declare ptr @osty_rt_list_get_ptr(ptr, i64)",
		"declare void @osty_rt_list_push_ptr(ptr, ptr)",
		"define void @appendMethod(ptr %outer, i64 %i, ptr %method)",
		"getelementptr inbounds %Outer, ptr %outer, i32 0, i32 0",
		"call ptr @osty_rt_list_get_ptr",
		"getelementptr inbounds %Item, ptr %",
		"call void @osty_rt_list_push_ptr(ptr %",
		", ptr %method)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0GenericCFGEmitsLoopLikeCFGWithoutBackEdge(t *testing.T) {
	t.Parallel()
	fn := makeWhileLoopFn(
		"countOnce",
		ir.TInt,
		[]paramSpec{{name: "n", ty: ir.TInt}},
		[]localSpec{
			{name: "acc", ty: ir.TInt, mut: true},
			{name: "cond", ty: ir.TBool},
		},
		[]mir.Instr{assign(2, useRV(intConst(0)))},
		[]mir.Instr{assign(3, binaryRV(mir.BinLt, paramCopy(2, ir.TInt), paramCopy(1, ir.TInt), ir.TBool))},
		paramCopy(3, ir.TBool),
		[]mir.Instr{assign(2, binaryRV(mir.BinAdd, paramCopy(2, ir.TInt), intConst(1), ir.TInt))},
		[]mir.Instr{assign(0, useRV(paramCopy(2, ir.TInt)))},
	)
	// Repoint body's terminator to exit (id 3), so this is no longer
	// the narrow while matcher but is still a valid scalar CFG.
	fn.Blocks[2].Term = &mir.GotoTerm{Target: 3}
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"define i64 @countOnce(i64 %n)",
		"%ret.slot = alloca i64",
		"br label %bb.3",
		"bb.3:",
		"ret i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0GenericCFGSwitchWithUnreachableDefault(t *testing.T) {
	t.Parallel()
	codeTy := &ir.NamedType{Name: "Code"}
	fn := &mir.Function{
		Name:        "codeText",
		Params:      []mir.LocalID{1},
		ReturnType:  ir.TString,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: ir.TString, IsReturn: true},
			{ID: 1, Name: "code", Type: codeTy, IsParam: true},
			{ID: 2, Name: "match", Type: ir.TString},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{
			{
				ID: 0,
				Term: &mir.SwitchIntTerm{
					Scrutinee: paramCopy(1, codeTy),
					Cases:     []mir.SwitchCase{{Value: 0, Target: 2}, {Value: 1, Target: 3}},
					Default:   4,
				},
			},
			{ID: 1, Instrs: []mir.Instr{assign(0, useRV(localCopy(2, ir.TString)))}, Term: &mir.ReturnTerm{}},
			{ID: 2, Instrs: []mir.Instr{assign(2, useRV(stringConst("a")))}, Term: &mir.GotoTerm{Target: 1}},
			{ID: 3, Instrs: []mir.Instr{assign(2, useRV(stringConst("b")))}, Term: &mir.GotoTerm{Target: 1}},
			{ID: 4, Term: &mir.UnreachableTerm{}},
		},
	}
	module := moduleWith(trivialMainFn(), fn)
	module.Layouts.Enums["Code"] = &mir.EnumLayout{
		Name: "Code",
		Variants: []mir.VariantLayout{
			{Index: 0, Name: "A"},
			{Index: 1, Name: "B"},
		},
	}
	gotBytes, err := EmitMIR(module, llvmabi.Options{PackageName: "main"})
	if err != nil {
		t.Fatalf("EmitMIR: %v", err)
	}
	got := string(gotBytes)
	for _, want := range []string{
		"define ptr @codeText(i64 %code)",
		"switch i64 %code",
		"unreachable",
		"ret ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0InfersNonFnTypeFnRefReturn(t *testing.T) {
	t.Parallel()
	listString := &ir.NamedType{Name: "List", Builtin: true, Args: []ir.Type{ir.TString}}
	collect := makeMultiInstrFn(
		"collectFiles",
		listString,
		[]paramSpec{{name: "root", ty: ir.TString}},
		[]paramSpec{{name: "out", ty: listString}},
		[]mir.Instr{
			assign(2, &mir.AggregateRV{Kind: mir.AggList, T: listString}),
			assign(0, useRV(localCopy(2, listString))),
		},
	)
	fn := &mir.Function{
		Name:        "wrapFiles",
		Params:      []mir.LocalID{1},
		ReturnType:  listString,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: listString, IsReturn: true},
			{ID: 1, Name: "root", Type: ir.TString, IsParam: true},
			{ID: 2, Name: "files", Type: listString},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{{
			ID: 0,
			Instrs: []mir.Instr{
				&mir.CallInstr{Dest: &mir.Place{Local: 2}, Callee: &mir.FnRef{Symbol: "collectFiles", Type: listString}, Args: []mir.Operand{paramCopy(1, ir.TString)}},
				assign(0, useRV(localCopy(2, listString))),
			},
			Term: &mir.ReturnTerm{},
		}},
	}
	got := emit(t, trivialMainFn(), collect, fn)
	for _, want := range []string{
		"define ptr @collectFiles(ptr %root)",
		"define ptr @wrapFiles(ptr %root)",
		"call ptr @collectFiles(ptr %root)",
		"ret ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0DiscardsValueIntrinsic(t *testing.T) {
	t.Parallel()
	fn := &mir.Function{
		Name:        "hasDashThenTrue",
		ReturnType:  ir.TBool,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: ir.TBool, IsReturn: true},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{{
			ID: 0,
			Instrs: []mir.Instr{
				&mir.IntrinsicInstr{Kind: mir.IntrinsicStringIsEmpty, Args: []mir.Operand{stringConst("")}},
				&mir.IntrinsicInstr{Kind: mir.IntrinsicStringStartsWith, Args: []mir.Operand{stringConst("-x"), stringConst("-")}},
				assign(0, useRV(boolConst(true))),
			},
			Term: &mir.ReturnTerm{},
		}},
	}
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"call i64 @osty_rt_strings_ByteLen",
		"declare i1 @osty_rt_strings_HasPrefix(ptr, ptr)",
		"call i1 @osty_rt_strings_HasPrefix",
		"ret i1 true",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0StringConcatCallsKnownZeroArgFnConst(t *testing.T) {
	t.Parallel()
	disc := makeFn(fnSpec{
		name: "mirDiscriminantNone",
		retT: ir.TString,
		src:  useRV(stringConst("1")),
	})
	fn := &mir.Function{
		Name:        "noneLiteral",
		ReturnType:  ir.TString,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: ir.TString, IsReturn: true},
			{ID: 1, Name: "s", Type: ir.TString},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{{
			ID: 0,
			Instrs: []mir.Instr{
				&mir.IntrinsicInstr{
					Dest: &mir.Place{Local: 1},
					Kind: mir.IntrinsicStringConcat,
					Args: []mir.Operand{
						stringConst("{ i64 "),
						&mir.ConstOp{Const: &mir.FnConst{Symbol: "mirDiscriminantNone", T: ir.ErrTypeVal}, T: ir.ErrTypeVal},
						stringConst(", i64 0 }"),
					},
				},
				assign(0, useRV(localCopy(1, ir.TString))),
			},
			Term: &mir.ReturnTerm{},
		}},
	}
	got := emit(t, trivialMainFn(), disc, fn)
	for _, want := range []string{
		"define ptr @mirDiscriminantNone()",
		"define ptr @noneLiteral()",
		"call ptr @mirDiscriminantNone()",
		"call ptr @osty_rt_strings_Concat",
		"ret ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0StoresProjectedRuntimeIntrinsicResult(t *testing.T) {
	t.Parallel()
	nodeTy := &ir.NamedType{Name: "AstNode"}
	fn := &mir.Function{
		Name:        "trimNodeLabel",
		Params:      []mir.LocalID{1},
		ReturnType:  ir.TString,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: ir.TString, IsReturn: true},
			{ID: 1, Name: "text", Type: ir.TString, IsParam: true},
			{ID: 2, Name: "node", Type: nodeTy},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{{
			ID: 0,
			Instrs: []mir.Instr{
				&mir.CallInstr{Dest: &mir.Place{Local: 2}, Callee: &mir.FnRef{Symbol: "emptyAstNode", Type: ir.ErrTypeVal}},
				&mir.IntrinsicInstr{
					Dest: &mir.Place{Local: 2, Projections: []mir.Projection{
						&mir.FieldProj{Index: 0, Name: "label", Type: ir.TString},
					}},
					Kind: mir.IntrinsicStringTrimPrefix,
					Args: []mir.Operand{paramCopy(1, ir.TString), stringConst("#")},
				},
				assign(0, useRV(&mir.CopyOp{
					Place: mir.Place{Local: 2, Projections: []mir.Projection{
						&mir.FieldProj{Index: 0, Name: "label", Type: ir.TString},
					}},
					T: ir.TString,
				})),
			},
			Term: &mir.ReturnTerm{},
		}},
	}
	module := moduleWith(trivialMainFn(), fn)
	module.Layouts.Structs["AstNode"] = &mir.StructLayout{
		Name:   "AstNode",
		Fields: []mir.FieldLayout{{Index: 0, Name: "label", Type: ir.TString}},
	}
	gotBytes, err := EmitMIR(module, llvmabi.Options{PackageName: "main"})
	if err != nil {
		t.Fatalf("EmitMIR: %v", err)
	}
	got := string(gotBytes)
	for _, want := range []string{
		"%AstNode = type { ptr }",
		"declare ptr @osty_rt_strings_TrimPrefix(ptr, ptr)",
		"call ptr @emptyAstNode()",
		"call ptr @osty_rt_strings_TrimPrefix",
		"store ptr %",
		"ret ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0GenericCFGResultAggregateDiscriminantAndProjection(t *testing.T) {
	t.Parallel()
	resultIntString := &ir.NamedType{Name: "Result", Builtin: true, Args: []ir.Type{ir.TInt, ir.TString}}
	fn := &mir.Function{
		Name:        "resultOkPayload",
		ReturnType:  ir.TInt,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: ir.TInt, IsReturn: true},
			{ID: 1, Name: "res", Type: resultIntString},
			{ID: 2, Name: "tag", Type: ir.TInt},
			{ID: 3, Name: "payload", Type: ir.TInt},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{{
			ID: 0,
			Instrs: []mir.Instr{
				assign(1, &mir.AggregateRV{Kind: mir.AggEnumVariant, VariantIdx: 0, Fields: []mir.Operand{intConst(7)}, T: resultIntString}),
				assign(2, &mir.DiscriminantRV{Place: mir.Place{Local: 1}, T: ir.TInt}),
				assign(3, useRV(&mir.CopyOp{
					Place: mir.Place{Local: 1, Projections: []mir.Projection{
						&mir.VariantProj{Variant: 0, Name: "Ok", FieldIdx: 0, Type: ir.TInt},
					}},
					T: ir.TInt,
				})),
				assign(0, binaryRV(mir.BinAdd, localCopy(2, ir.TInt), localCopy(3, ir.TInt), ir.TInt)),
			},
			Term: &mir.ReturnTerm{},
		}},
	}
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"%stage0.Result.i64.ptr = type { i64, i64, ptr }",
		"store i64 0",
		"getelementptr inbounds %stage0.Result.i64.ptr",
		", i32 0, i32 1",
		"load i64",
		"ret i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0DiscriminantPayloadfulEnumUsesTagSlot(t *testing.T) {
	t.Parallel()
	payloadfulKind := &ir.NamedType{Name: "PayloadfulKind"}
	fn := &mir.Function{
		Name:        "payloadfulDiscriminant",
		ReturnType:  ir.TInt,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: ir.TInt, IsReturn: true},
			{ID: 1, Name: "kind", Type: payloadfulKind},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{{
			ID: 0,
			Instrs: []mir.Instr{
				assign(1, &mir.AggregateRV{
					Kind:       mir.AggEnumVariant,
					VariantIdx: 1,
					Fields:     []mir.Operand{stringConst("payload")},
					T:          payloadfulKind,
				}),
				assign(0, &mir.DiscriminantRV{
					Place: mir.Place{Local: 1},
					T:     ir.TInt,
				}),
			},
			Term: &mir.ReturnTerm{},
		}},
	}
	module := moduleWith(trivialMainFn(), fn)
	module.Layouts.Enums["PayloadfulKind"] = &mir.EnumLayout{
		Name: "PayloadfulKind",
		Variants: []mir.VariantLayout{
			{Index: 0, Name: "Plain", Payload: []mir.FieldLayout{{Index: 0, Name: "v", Type: ir.TInt}}},
			{Index: 1, Name: "Tagged", Payload: []mir.FieldLayout{{Index: 0, Name: "v", Type: ir.TString}}},
		},
	}
	gotBytes, err := EmitMIR(module, llvmabi.Options{PackageName: "main"})
	if err != nil {
		t.Fatalf("EmitMIR: %v", err)
	}
	got := string(gotBytes)
	for _, want := range []string{
		"%stage0.Enum.PayloadfulKind.1 = type { i64, ptr }",
		"store i64 1, ptr",
		"getelementptr i64, ptr %",
		"load i64, ptr",
		"ret i64 %",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0GenericCFGCallStoresIntoListIndex(t *testing.T) {
	t.Parallel()
	listString := &ir.NamedType{Name: "List", Builtin: true, Args: []ir.Type{ir.TString}}
	fn := &mir.Function{
		Name:        "fillFirst",
		ReturnType:  listString,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: listString, IsReturn: true},
			{ID: 1, Name: "out", Type: listString},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{{
			ID: 0,
			Instrs: []mir.Instr{
				assign(1, &mir.AggregateRV{Kind: mir.AggList, T: listString}),
				&mir.CallInstr{
					Dest: &mir.Place{Local: 1, Projections: []mir.Projection{
						&mir.IndexProj{Index: intConst(0), ElemType: ir.TString},
					}},
					Callee: &mir.FnRef{Symbol: "renderName", Type: ir.ErrTypeVal},
				},
				assign(0, useRV(localCopy(1, listString))),
			},
			Term: &mir.ReturnTerm{},
		}},
	}
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"declare ptr @renderName()",
		"declare void @osty_rt_list_set_string(ptr, i64, ptr)",
		"call ptr @renderName()",
		"call void @osty_rt_list_set_string",
		"ret ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}

func TestStage0RejectsWhileLoopWithIntCondition(t *testing.T) {
	t.Parallel()
	fn := makeWhileLoopFn(
		"bad",
		ir.TInt,
		[]paramSpec{{name: "n", ty: ir.TInt}},
		[]localSpec{{name: "acc", ty: ir.TInt, mut: true}},
		[]mir.Instr{assign(2, useRV(intConst(0)))},
		nil,
		paramCopy(1, ir.TInt), // Int cond — wrong
		[]mir.Instr{assign(2, binaryRV(mir.BinAdd, paramCopy(2, ir.TInt), intConst(1), ir.TInt))},
		[]mir.Instr{assign(0, useRV(paramCopy(2, ir.TInt)))},
	)
	mustReject(t, trivialMainFn(), fn)
}

func TestStage0RejectsWhileLoopExitNotReturning(t *testing.T) {
	t.Parallel()
	// Exit block ends with a Goto rather than ReturnTerm — declines.
	fn := makeWhileLoopFn(
		"bad",
		ir.TInt,
		[]paramSpec{{name: "n", ty: ir.TInt}},
		[]localSpec{
			{name: "acc", ty: ir.TInt, mut: true},
			{name: "cond", ty: ir.TBool},
		},
		[]mir.Instr{assign(2, useRV(intConst(0)))},
		[]mir.Instr{assign(3, binaryRV(mir.BinLt, paramCopy(2, ir.TInt), paramCopy(1, ir.TInt), ir.TBool))},
		paramCopy(3, ir.TBool),
		[]mir.Instr{assign(2, binaryRV(mir.BinAdd, paramCopy(2, ir.TInt), intConst(1), ir.TInt))},
		[]mir.Instr{assign(0, useRV(paramCopy(2, ir.TInt)))},
	)
	fn.Blocks[3].Term = &mir.GotoTerm{Target: 0}
	mustReject(t, trivialMainFn(), fn)
}

// ---- OSTY_STAGE0_LIST_ALL_DECLINES env var ----
//
// Build a module with main + N declined functions. Default mode bails
// on the first decline (so only one name appears in the error). With
// the env var set, EmitMIR continues past each decline and the
// returned error names every blocking function in one pass — letting
// install-self iterations plan multi-PR unblock waves instead of one
// fail-and-fix-and-rebuild cycle per function.

// undecidableFn returns a function that no stage0 matcher will accept,
// because its single block contains an instruction kind nothing
// classifies. The Name is what the aggregated diagnostic should list.
func undecidableFn(name string) *mir.Function {
	return &mir.Function{
		Name:        name,
		ReturnType:  ir.TInt,
		ReturnLocal: 0,
		Locals: []*mir.Local{
			{ID: 0, Name: "ret", Type: ir.TInt, IsReturn: true},
		},
		Entry: 0,
		Blocks: []*mir.BasicBlock{{
			ID: 0,
			// Self-referential GotoTerm so the block neither returns
			// nor terminates legally — every matcher's terminator
			// shape check fails.
			Instrs: []mir.Instr{},
			Term:   &mir.GotoTerm{Target: 0},
		}},
	}
}

func TestStage0DefaultStopsAtFirstDecline(t *testing.T) {
	// Cannot use t.Parallel — we touch the env var.
	t.Setenv(ListAllDeclinesEnv, "")

	module := moduleWith(trivialMainFn(), undecidableFn("first"), undecidableFn("second"))
	_, err := EmitMIR(module, llvmabi.Options{PackageName: "main"})
	if err == nil {
		t.Fatal("expected error for declined functions")
	}
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want wrapped ErrUnsupported", err)
	}
	// Default mode reports exactly one function — whichever was reached
	// first. The aggregated multi-name diagnostic must NOT appear.
	msg := err.Error()
	if strings.Contains(msg, " function(s) declined:") {
		t.Fatalf("default mode unexpectedly aggregated declines:\n%s", msg)
	}
}

func TestStage0ListAllDeclinesAggregatesAcrossModule(t *testing.T) {
	// Cannot use t.Parallel — we touch the env var.
	t.Setenv(ListAllDeclinesEnv, "1")

	module := moduleWith(trivialMainFn(), undecidableFn("alpha"), undecidableFn("beta"), undecidableFn("gamma"))
	_, err := EmitMIR(module, llvmabi.Options{PackageName: "main"})
	if err == nil {
		t.Fatal("expected aggregated error")
	}
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want wrapped ErrUnsupported", err)
	}
	msg := err.Error()
	for _, want := range []string{
		"3 function(s) declined:",
		"alpha:",
		"beta:",
		"gamma:",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("aggregated diagnostic missing %q:\n%s", want, msg)
		}
	}
}

func TestStage0ListAllDeclinesSkipsAggregationOnCleanModule(t *testing.T) {
	// Cannot use t.Parallel — we touch the env var.
	t.Setenv(ListAllDeclinesEnv, "1")

	got, err := EmitMIR(moduleWith(trivialMainFn()), llvmabi.Options{PackageName: "main"})
	if err != nil {
		t.Fatalf("EmitMIR with no declines: %v", err)
	}
	if !strings.Contains(string(got), "define i32 @main()") {
		t.Fatalf("expected normal main emission with env var on:\n%s", got)
	}
}


func TestStage0GenericCFGResultEnumReturn(t *testing.T) {
	t.Parallel()
	resultIntString := &ir.NamedType{Name: "Result", Builtin: true, Args: []ir.Type{ir.TInt, ir.TString}}
	fn := &mir.Function{
		Name:        "hexDigit",
		ReturnType:  resultIntString,
		ReturnLocal: 0,
		Entry:       0,
		Locals: []*mir.Local{
			{ID: 0, Name: "_return", Type: resultIntString, IsReturn: true},
			{ID: 1, Name: "x", Type: ir.TInt, IsParam: true},
			{ID: 2, Type: ir.TBool},
			{ID: 3, Type: ir.TBool},
			{ID: 4, Name: "n", Type: ir.TInt},
		},
		Params: []mir.LocalID{1},
		Blocks: []*mir.BasicBlock{
			{
				ID: 0,
				Instrs: []mir.Instr{
					assign(3, binaryRV(mir.BinGeq, paramCopy(1, ir.TInt), intConst(0), ir.TBool)),
				},
				Term: &mir.BranchTerm{Then: 1, Else: 2, Cond: localCopy(3, ir.TBool)},
			},
			{
				ID: 1,
				Instrs: []mir.Instr{
					assign(2, binaryRV(mir.BinLeq, paramCopy(1, ir.TInt), intConst(9), ir.TBool)),
				},
				Term: &mir.GotoTerm{Target: 3},
			},
			{
				ID: 2,
				Instrs: []mir.Instr{
					assign(2, useRV(&mir.ConstOp{Const: &mir.BoolConst{}, T: ir.TBool})),
				},
				Term: &mir.GotoTerm{Target: 3},
			},
			{
				ID: 3,
				Term: &mir.BranchTerm{Then: 4, Else: 5, Cond: localCopy(2, ir.TBool)},
			},
			{
				ID: 4,
				Instrs: []mir.Instr{
					assign(4, binaryRV(mir.BinSub, paramCopy(1, ir.TInt), intConst(0), ir.TInt)),
					assign(0, &mir.AggregateRV{Kind: mir.AggEnumVariant, VariantIdx: 0, Fields: []mir.Operand{localCopy(4, ir.TInt)}, T: resultIntString}),
				},
				Term: &mir.ReturnTerm{},
			},
			{
				ID: 5,
				Instrs: []mir.Instr{
					assign(0, &mir.AggregateRV{Kind: mir.AggEnumVariant, VariantIdx: 1, Fields: []mir.Operand{stringConst("err")}, T: resultIntString}),
				},
				Term: &mir.ReturnTerm{},
			},
		},
	}
	got := emit(t, trivialMainFn(), fn)
	for _, want := range []string{
		"define ptr @hexDigit(i64 %x)",
		"osty_rt_stage0_alloc",
		"ret ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("emitted IR missing %q:\n%s", want, got)
		}
	}
}
