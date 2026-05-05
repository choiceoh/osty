package stage0

import (
	"errors"
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

func paramCopy(id mir.LocalID, ty mir.Type) *mir.CopyOp {
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

// ---- single-instruction return: arithmetic ops ----

func TestStage0EmitsIntArithOps(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		op     mir.BinaryOp
		llvm   string
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

func TestStage0RejectsThreeParams(t *testing.T) {
	t.Parallel()
	mustReject(t, trivialMainFn(),
		makeFn(fnSpec{
			name:   "three",
			retT:   ir.TInt,
			params: []paramSpec{{name: "a", ty: ir.TInt}, {name: "b", ty: ir.TInt}, {name: "c", ty: ir.TInt}},
			src:    useRV(paramCopy(1, ir.TInt)),
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
