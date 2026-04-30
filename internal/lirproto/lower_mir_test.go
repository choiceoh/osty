package lirproto

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/mir"
)

func TestLowerMIRSingleBlockConstReturn(t *testing.T) {
	fn := &mir.Function{Name: "answer", ReturnType: mir.TInt}
	fn.ReturnLocal = fn.NewLocal("_return", mir.TInt, false, mir.Span{})
	fn.Locals[fn.ReturnLocal].IsReturn = true
	bbID := fn.NewBlock(mir.Span{})
	fn.Entry = bbID
	bb := fn.Block(bbID)
	bb.Append(&mir.AssignInstr{
		Dest: mir.Place{Local: fn.ReturnLocal},
		Src: &mir.UseRV{
			Op: &mir.ConstOp{Const: &mir.IntConst{Value: 42, T: mir.TInt}, T: mir.TInt},
		},
	})
	bb.SetTerminator(&mir.ReturnTerm{})

	res := NewLowerer(Config{SourcePath: "/tmp/answer.osty"}).LowerMIR(&mir.Module{
		Package:   "main",
		Functions: []*mir.Function{fn},
	})
	if !res.OK() {
		t.Fatalf("LowerMIR() diagnostics = %+v, want OK", res.Diagnostics)
	}
	got := Render(res.Module)
	for _, want := range []string{
		`source_filename = "/tmp/answer.osty"`,
		"define i64 @answer() {",
		"entry:",
		"  %l0 = alloca i64",
		"  store i64 42, ptr %l0",
		"  %t0 = load i64, ptr %l0",
		"  ret i64 %t0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered LIR missing %q:\n%s", want, got)
		}
	}
}

func TestLowerMIRSingleBlockBinaryParams(t *testing.T) {
	fn := &mir.Function{Name: "add", ReturnType: mir.TInt}
	fn.ReturnLocal = fn.NewLocal("_return", mir.TInt, false, mir.Span{})
	fn.Locals[fn.ReturnLocal].IsReturn = true
	a := fn.NewLocal("a", mir.TInt, false, mir.Span{})
	b := fn.NewLocal("b", mir.TInt, false, mir.Span{})
	fn.Locals[a].IsParam = true
	fn.Locals[b].IsParam = true
	fn.Params = []mir.LocalID{a, b}
	bbID := fn.NewBlock(mir.Span{})
	fn.Entry = bbID
	bb := fn.Block(bbID)
	bb.Append(&mir.AssignInstr{
		Dest: mir.Place{Local: fn.ReturnLocal},
		Src: &mir.BinaryRV{
			Op:    mir.BinAdd,
			Left:  &mir.CopyOp{Place: mir.Place{Local: a}, T: mir.TInt},
			Right: &mir.CopyOp{Place: mir.Place{Local: b}, T: mir.TInt},
			T:     mir.TInt,
		},
	})
	bb.SetTerminator(&mir.ReturnTerm{})

	res := NewLowerer(Config{}).LowerMIR(&mir.Module{
		Package:   "main",
		Functions: []*mir.Function{fn},
	})
	if !res.OK() {
		t.Fatalf("LowerMIR() diagnostics = %+v, want OK", res.Diagnostics)
	}
	got := Render(res.Module)
	for _, want := range []string{
		"define i64 @add(i64 %p1, i64 %p2) {",
		"  %l0 = alloca i64",
		"  %l1 = alloca i64",
		"  store i64 %p1, ptr %l1",
		"  %l2 = alloca i64",
		"  store i64 %p2, ptr %l2",
		"  %t0 = load i64, ptr %l1",
		"  %t1 = load i64, ptr %l2",
		"  %t2 = add i64 %t0, %t1",
		"  store i64 %t2, ptr %l0",
		"  %t3 = load i64, ptr %l0",
		"  ret i64 %t3",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered LIR missing %q:\n%s", want, got)
		}
	}
}

func TestLowerMIRScalarBranchBlocks(t *testing.T) {
	fn := &mir.Function{Name: "choose", ReturnType: mir.TInt}
	fn.ReturnLocal = fn.NewLocal("_return", mir.TInt, false, mir.Span{})
	fn.Locals[fn.ReturnLocal].IsReturn = true
	cond := fn.NewLocal("cond", mir.TBool, false, mir.Span{})
	fn.Locals[cond].IsParam = true
	fn.Params = []mir.LocalID{cond}

	entryID := fn.NewBlock(mir.Span{})
	thenID := fn.NewBlock(mir.Span{})
	elseID := fn.NewBlock(mir.Span{})
	fn.Entry = entryID
	fn.Block(entryID).SetTerminator(&mir.BranchTerm{
		Cond: &mir.CopyOp{Place: mir.Place{Local: cond}, T: mir.TBool},
		Then: thenID,
		Else: elseID,
	})
	appendReturnInt(fn, thenID, 1)
	appendReturnInt(fn, elseID, 2)

	res := NewLowerer(Config{}).LowerMIR(&mir.Module{
		Package:   "main",
		Functions: []*mir.Function{fn},
	})
	if !res.OK() {
		t.Fatalf("LowerMIR() diagnostics = %+v, want OK", res.Diagnostics)
	}
	got := Render(res.Module)
	for _, want := range []string{
		"define i64 @choose(i1 %p1) {",
		"entry:",
		"  %l0 = alloca i64",
		"  %l1 = alloca i1",
		"  store i1 %p1, ptr %l1",
		"  %t0 = load i1, ptr %l1",
		"  br i1 %t0, label %bb1, label %bb2",
		"bb1:",
		"  store i64 1, ptr %l0",
		"bb2:",
		"  store i64 2, ptr %l0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered LIR missing %q:\n%s", want, got)
		}
	}
}

func TestLowerMIRScalarSwitchBlocks(t *testing.T) {
	fn := &mir.Function{Name: "pick", ReturnType: mir.TInt}
	fn.ReturnLocal = fn.NewLocal("_return", mir.TInt, false, mir.Span{})
	fn.Locals[fn.ReturnLocal].IsReturn = true
	n := fn.NewLocal("n", mir.TInt, false, mir.Span{})
	fn.Locals[n].IsParam = true
	fn.Params = []mir.LocalID{n}

	entryID := fn.NewBlock(mir.Span{})
	zeroID := fn.NewBlock(mir.Span{})
	oneID := fn.NewBlock(mir.Span{})
	defaultID := fn.NewBlock(mir.Span{})
	fn.Entry = entryID
	fn.Block(entryID).SetTerminator(&mir.SwitchIntTerm{
		Scrutinee: &mir.CopyOp{Place: mir.Place{Local: n}, T: mir.TInt},
		Cases: []mir.SwitchCase{
			{Value: 0, Target: zeroID},
			{Value: 1, Target: oneID},
		},
		Default: defaultID,
	})
	appendReturnInt(fn, zeroID, 10)
	appendReturnInt(fn, oneID, 20)
	appendReturnInt(fn, defaultID, 30)

	res := NewLowerer(Config{}).LowerMIR(&mir.Module{
		Package:   "main",
		Functions: []*mir.Function{fn},
	})
	if !res.OK() {
		t.Fatalf("LowerMIR() diagnostics = %+v, want OK", res.Diagnostics)
	}
	got := Render(res.Module)
	for _, want := range []string{
		"define i64 @pick(i64 %p1) {",
		"  %t0 = load i64, ptr %l1",
		"  switch i64 %t0, label %bb3 [",
		"    i64 0, label %bb1",
		"    i64 1, label %bb2",
		"bb1:",
		"  store i64 10, ptr %l0",
		"bb2:",
		"  store i64 20, ptr %l0",
		"bb3:",
		"  store i64 30, ptr %l0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered LIR missing %q:\n%s", want, got)
		}
	}
}

func TestLowerMIRScalarDirectCall(t *testing.T) {
	callee := &mir.Function{Name: "inc", ReturnType: mir.TInt}
	callee.ReturnLocal = callee.NewLocal("_return", mir.TInt, false, mir.Span{})
	callee.Locals[callee.ReturnLocal].IsReturn = true
	x := callee.NewLocal("x", mir.TInt, false, mir.Span{})
	callee.Locals[x].IsParam = true
	callee.Params = []mir.LocalID{x}
	calleeEntry := callee.NewBlock(mir.Span{})
	callee.Entry = calleeEntry
	callee.Block(calleeEntry).Append(&mir.AssignInstr{
		Dest: mir.Place{Local: callee.ReturnLocal},
		Src: &mir.BinaryRV{
			Op:    mir.BinAdd,
			Left:  &mir.CopyOp{Place: mir.Place{Local: x}, T: mir.TInt},
			Right: &mir.ConstOp{Const: &mir.IntConst{Value: 1, T: mir.TInt}, T: mir.TInt},
			T:     mir.TInt,
		},
	})
	callee.Block(calleeEntry).SetTerminator(&mir.ReturnTerm{})

	caller := &mir.Function{Name: "main", ReturnType: mir.TInt}
	caller.ReturnLocal = caller.NewLocal("_return", mir.TInt, false, mir.Span{})
	caller.Locals[caller.ReturnLocal].IsReturn = true
	callerEntry := caller.NewBlock(mir.Span{})
	caller.Entry = callerEntry
	caller.Block(callerEntry).Append(&mir.CallInstr{
		Dest: &mir.Place{Local: caller.ReturnLocal},
		Callee: &mir.FnRef{
			Symbol: "inc",
			Type: &ir.FnType{
				Params: []ir.Type{mir.TInt},
				Return: mir.TInt,
			},
		},
		Args: []mir.Operand{
			&mir.ConstOp{Const: &mir.IntConst{Value: 41, T: mir.TInt}, T: mir.TInt},
		},
	})
	caller.Block(callerEntry).SetTerminator(&mir.ReturnTerm{})

	res := NewLowerer(Config{}).LowerMIR(&mir.Module{
		Package:   "main",
		Functions: []*mir.Function{callee, caller},
	})
	if !res.OK() {
		t.Fatalf("LowerMIR() diagnostics = %+v, want OK", res.Diagnostics)
	}
	got := Render(res.Module)
	for _, want := range []string{
		"define i64 @inc(i64 %p1) {",
		"  %t1 = add i64 %t0, 1",
		"define i64 @main() {",
		"  %t0 = call i64 @inc(i64 41)",
		"  store i64 %t0, ptr %l0",
		"  %t1 = load i64, ptr %l0",
		"  ret i64 %t1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered LIR missing %q:\n%s", want, got)
		}
	}
}

func TestLowerMIRScalarVoidDirectCall(t *testing.T) {
	noop := &mir.Function{Name: "noop", ReturnType: mir.TUnit}
	noop.ReturnLocal = noop.NewLocal("_return", mir.TUnit, false, mir.Span{})
	noop.Locals[noop.ReturnLocal].IsReturn = true
	noopEntry := noop.NewBlock(mir.Span{})
	noop.Entry = noopEntry
	noop.Block(noopEntry).SetTerminator(&mir.ReturnTerm{})

	caller := &mir.Function{Name: "main", ReturnType: mir.TUnit}
	caller.ReturnLocal = caller.NewLocal("_return", mir.TUnit, false, mir.Span{})
	caller.Locals[caller.ReturnLocal].IsReturn = true
	callerEntry := caller.NewBlock(mir.Span{})
	caller.Entry = callerEntry
	caller.Block(callerEntry).Append(&mir.CallInstr{
		Callee: &mir.FnRef{
			Symbol: "noop",
			Type: &ir.FnType{
				Return: mir.TUnit,
			},
		},
	})
	caller.Block(callerEntry).SetTerminator(&mir.ReturnTerm{})

	res := NewLowerer(Config{}).LowerMIR(&mir.Module{
		Package:   "main",
		Functions: []*mir.Function{noop, caller},
	})
	if !res.OK() {
		t.Fatalf("LowerMIR() diagnostics = %+v, want OK", res.Diagnostics)
	}
	got := Render(res.Module)
	for _, want := range []string{
		"define void @noop() {",
		"  ret void",
		"define void @main() {",
		"  call void @noop()",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered LIR missing %q:\n%s", want, got)
		}
	}
}

func TestLowerMIRScalarPrintlnStringIntrinsic(t *testing.T) {
	fn := &mir.Function{Name: "main", ReturnType: mir.TUnit}
	fn.ReturnLocal = fn.NewLocal("_return", mir.TUnit, false, mir.Span{})
	fn.Locals[fn.ReturnLocal].IsReturn = true
	entry := fn.NewBlock(mir.Span{})
	fn.Entry = entry
	fn.Block(entry).Append(&mir.IntrinsicInstr{
		Kind: mir.IntrinsicPrintln,
		Args: []mir.Operand{
			&mir.ConstOp{Const: &mir.StringConst{Value: "hello"}, T: mir.TString},
		},
	})
	fn.Block(entry).SetTerminator(&mir.ReturnTerm{})

	res := NewLowerer(Config{}).LowerMIR(&mir.Module{
		Package:   "main",
		Functions: []*mir.Function{fn},
	})
	if !res.OK() {
		t.Fatalf("LowerMIR() diagnostics = %+v, want OK", res.Diagnostics)
	}
	got := Render(res.Module)
	for _, want := range []string{
		`@.str.0 = private unnamed_addr constant [6 x i8] c"hello\00"`,
		"declare void @osty_rt_io_write(ptr, i1, i1)",
		"define void @main() {",
		"  call void @osty_rt_io_write(ptr @.str.0, i1 true, i1 false)",
		"  ret void",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered LIR missing %q:\n%s", want, got)
		}
	}
}

func TestLowerMIRScalarEprintlnIntIntrinsic(t *testing.T) {
	fn := &mir.Function{Name: "main", ReturnType: mir.TUnit}
	fn.ReturnLocal = fn.NewLocal("_return", mir.TUnit, false, mir.Span{})
	fn.Locals[fn.ReturnLocal].IsReturn = true
	entry := fn.NewBlock(mir.Span{})
	fn.Entry = entry
	fn.Block(entry).Append(&mir.IntrinsicInstr{
		Kind: mir.IntrinsicEprintln,
		Args: []mir.Operand{
			&mir.ConstOp{Const: &mir.IntConst{Value: 7, T: mir.TInt}, T: mir.TInt},
		},
	})
	fn.Block(entry).SetTerminator(&mir.ReturnTerm{})

	res := NewLowerer(Config{}).LowerMIR(&mir.Module{
		Package:   "main",
		Functions: []*mir.Function{fn},
	})
	if !res.OK() {
		t.Fatalf("LowerMIR() diagnostics = %+v, want OK", res.Diagnostics)
	}
	got := Render(res.Module)
	for _, want := range []string{
		"declare ptr @osty_rt_int_to_string(i64)",
		"declare void @osty_rt_io_write(ptr, i1, i1)",
		"  %t0 = call ptr @osty_rt_int_to_string(i64 7)",
		"  call void @osty_rt_io_write(ptr %t0, i1 true, i1 true)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered LIR missing %q:\n%s", want, got)
		}
	}
}

func TestLowerMIRScalarPrintFloat32IntrinsicExtendsToDouble(t *testing.T) {
	fn := &mir.Function{Name: "main", ReturnType: mir.TUnit}
	fn.ReturnLocal = fn.NewLocal("_return", mir.TUnit, false, mir.Span{})
	fn.Locals[fn.ReturnLocal].IsReturn = true
	entry := fn.NewBlock(mir.Span{})
	fn.Entry = entry
	fn.Block(entry).Append(&mir.IntrinsicInstr{
		Kind: mir.IntrinsicPrint,
		Args: []mir.Operand{
			&mir.ConstOp{Const: &mir.FloatConst{Value: 1.5, T: mir.TFloat32}, T: mir.TFloat32},
		},
	})
	fn.Block(entry).SetTerminator(&mir.ReturnTerm{})

	res := NewLowerer(Config{}).LowerMIR(&mir.Module{
		Package:   "main",
		Functions: []*mir.Function{fn},
	})
	if !res.OK() {
		t.Fatalf("LowerMIR() diagnostics = %+v, want OK", res.Diagnostics)
	}
	got := Render(res.Module)
	for _, want := range []string{
		"declare ptr @osty_rt_float_to_string(double)",
		"  %t0 = fpext float 1.5 to double",
		"  %t1 = call ptr @osty_rt_float_to_string(double %t0)",
		"  call void @osty_rt_io_write(ptr %t1, i1 false, i1 false)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered LIR missing %q:\n%s", want, got)
		}
	}
}

func TestLowerMIRScalarCastIntResize(t *testing.T) {
	fn := &mir.Function{Name: "widen", ReturnType: mir.TInt}
	fn.ReturnLocal = fn.NewLocal("_return", mir.TInt, false, mir.Span{})
	fn.Locals[fn.ReturnLocal].IsReturn = true
	entry := fn.NewBlock(mir.Span{})
	fn.Entry = entry
	fn.Block(entry).Append(&mir.AssignInstr{
		Dest: mir.Place{Local: fn.ReturnLocal},
		Src: &mir.CastRV{
			Kind: mir.CastIntResize,
			Arg:  &mir.ConstOp{Const: &mir.IntConst{Value: 7, T: mir.TInt32}, T: mir.TInt32},
			From: mir.TInt32,
			To:   mir.TInt,
		},
	})
	fn.Block(entry).SetTerminator(&mir.ReturnTerm{})

	res := NewLowerer(Config{}).LowerMIR(&mir.Module{
		Package:   "main",
		Functions: []*mir.Function{fn},
	})
	if !res.OK() {
		t.Fatalf("LowerMIR() diagnostics = %+v, want OK", res.Diagnostics)
	}
	got := Render(res.Module)
	for _, want := range []string{
		"define i64 @widen() {",
		"  %t0 = sext i32 7 to i64",
		"  store i64 %t0, ptr %l0",
		"  ret i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered LIR missing %q:\n%s", want, got)
		}
	}
}

func TestLowerMIRScalarCastUnsignedIntResizeUsesZExt(t *testing.T) {
	fn := &mir.Function{Name: "uwiden", ReturnType: mir.TUInt64}
	fn.ReturnLocal = fn.NewLocal("_return", mir.TUInt64, false, mir.Span{})
	fn.Locals[fn.ReturnLocal].IsReturn = true
	entry := fn.NewBlock(mir.Span{})
	fn.Entry = entry
	fn.Block(entry).Append(&mir.AssignInstr{
		Dest: mir.Place{Local: fn.ReturnLocal},
		Src: &mir.CastRV{
			Kind: mir.CastIntResize,
			Arg:  &mir.ConstOp{Const: &mir.IntConst{Value: 7, T: mir.TUInt32}, T: mir.TUInt32},
			From: mir.TUInt32,
			To:   mir.TUInt64,
		},
	})
	fn.Block(entry).SetTerminator(&mir.ReturnTerm{})

	res := NewLowerer(Config{}).LowerMIR(&mir.Module{
		Package:   "main",
		Functions: []*mir.Function{fn},
	})
	if !res.OK() {
		t.Fatalf("LowerMIR() diagnostics = %+v, want OK", res.Diagnostics)
	}
	got := Render(res.Module)
	for _, want := range []string{
		"define i64 @uwiden() {",
		"  %t0 = zext i32 7 to i64",
		"  store i64 %t0, ptr %l0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered LIR missing %q:\n%s", want, got)
		}
	}
}

func TestLowerMIRScalarCastFloatResizeTruncates(t *testing.T) {
	fn := &mir.Function{Name: "narrowFloat", ReturnType: mir.TFloat32}
	fn.ReturnLocal = fn.NewLocal("_return", mir.TFloat32, false, mir.Span{})
	fn.Locals[fn.ReturnLocal].IsReturn = true
	entry := fn.NewBlock(mir.Span{})
	fn.Entry = entry
	fn.Block(entry).Append(&mir.AssignInstr{
		Dest: mir.Place{Local: fn.ReturnLocal},
		Src: &mir.CastRV{
			Kind: mir.CastFloatResize,
			Arg:  &mir.ConstOp{Const: &mir.FloatConst{Value: 1.5, T: mir.TFloat}, T: mir.TFloat},
			From: mir.TFloat,
			To:   mir.TFloat32,
		},
	})
	fn.Block(entry).SetTerminator(&mir.ReturnTerm{})

	res := NewLowerer(Config{}).LowerMIR(&mir.Module{
		Package:   "main",
		Functions: []*mir.Function{fn},
	})
	if !res.OK() {
		t.Fatalf("LowerMIR() diagnostics = %+v, want OK", res.Diagnostics)
	}
	got := Render(res.Module)
	for _, want := range []string{
		"define float @narrowFloat() {",
		"  %t0 = fptrunc double 1.5 to float",
		"  store float %t0, ptr %l0",
		"  ret float",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered LIR missing %q:\n%s", want, got)
		}
	}
}

func TestLowerMIRScalarCastIntToFloat(t *testing.T) {
	fn := &mir.Function{Name: "toFloat", ReturnType: mir.TFloat}
	fn.ReturnLocal = fn.NewLocal("_return", mir.TFloat, false, mir.Span{})
	fn.Locals[fn.ReturnLocal].IsReturn = true
	entry := fn.NewBlock(mir.Span{})
	fn.Entry = entry
	fn.Block(entry).Append(&mir.AssignInstr{
		Dest: mir.Place{Local: fn.ReturnLocal},
		Src: &mir.CastRV{
			Kind: mir.CastIntToFloat,
			Arg:  &mir.ConstOp{Const: &mir.IntConst{Value: 7, T: mir.TInt}, T: mir.TInt},
			From: mir.TInt,
			To:   mir.TFloat,
		},
	})
	fn.Block(entry).SetTerminator(&mir.ReturnTerm{})

	res := NewLowerer(Config{}).LowerMIR(&mir.Module{
		Package:   "main",
		Functions: []*mir.Function{fn},
	})
	if !res.OK() {
		t.Fatalf("LowerMIR() diagnostics = %+v, want OK", res.Diagnostics)
	}
	got := Render(res.Module)
	for _, want := range []string{
		"define double @toFloat() {",
		"  %t0 = sitofp i64 7 to double",
		"  store double %t0, ptr %l0",
		"  ret double",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered LIR missing %q:\n%s", want, got)
		}
	}
}

func TestLowerMIRScalarDirectCallArgCoercion(t *testing.T) {
	callee := &mir.Function{Name: "id", ReturnType: mir.TInt}
	callee.ReturnLocal = callee.NewLocal("_return", mir.TInt, false, mir.Span{})
	callee.Locals[callee.ReturnLocal].IsReturn = true
	x := callee.NewLocal("x", mir.TInt, false, mir.Span{})
	callee.Locals[x].IsParam = true
	callee.Params = []mir.LocalID{x}
	calleeEntry := callee.NewBlock(mir.Span{})
	callee.Entry = calleeEntry
	callee.Block(calleeEntry).Append(&mir.AssignInstr{
		Dest: mir.Place{Local: callee.ReturnLocal},
		Src: &mir.UseRV{
			Op: &mir.CopyOp{Place: mir.Place{Local: x}, T: mir.TInt},
		},
	})
	callee.Block(calleeEntry).SetTerminator(&mir.ReturnTerm{})

	caller := &mir.Function{Name: "main", ReturnType: mir.TInt}
	caller.ReturnLocal = caller.NewLocal("_return", mir.TInt, false, mir.Span{})
	caller.Locals[caller.ReturnLocal].IsReturn = true
	callerEntry := caller.NewBlock(mir.Span{})
	caller.Entry = callerEntry
	caller.Block(callerEntry).Append(&mir.CallInstr{
		Dest: &mir.Place{Local: caller.ReturnLocal},
		Callee: &mir.FnRef{
			Symbol: "id",
			Type: &ir.FnType{
				Params: []ir.Type{mir.TInt},
				Return: mir.TInt,
			},
		},
		Args: []mir.Operand{
			&mir.ConstOp{Const: &mir.IntConst{Value: 41, T: mir.TInt32}, T: mir.TInt32},
		},
	})
	caller.Block(callerEntry).SetTerminator(&mir.ReturnTerm{})

	res := NewLowerer(Config{}).LowerMIR(&mir.Module{
		Package:   "main",
		Functions: []*mir.Function{callee, caller},
	})
	if !res.OK() {
		t.Fatalf("LowerMIR() diagnostics = %+v, want OK", res.Diagnostics)
	}
	got := Render(res.Module)
	for _, want := range []string{
		"define i64 @main() {",
		"  %t0 = sext i32 41 to i64",
		"  %t1 = call i64 @id(i64 %t0)",
		"  store i64 %t1, ptr %l0",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered LIR missing %q:\n%s", want, got)
		}
	}
}

func TestLowerMIRScalarPrimitiveConversionIntrinsics(t *testing.T) {
	tests := []struct {
		name    string
		kind    mir.IntrinsicKind
		retType mir.Type
		arg     mir.Operand
		common  []string
	}{
		{
			name:    "byteToInt",
			kind:    mir.IntrinsicByteToInt,
			retType: mir.TInt,
			arg:     &mir.ConstOp{Const: &mir.ByteConst{Value: 7}, T: mir.TByte},
			common: []string{
				"define i64 @byteToInt() {",
				"  %t0 = zext i8 7 to i64",
				"  store i64 %t0, ptr %l0",
				"  ret i64",
			},
		},
		{
			name:    "charToInt",
			kind:    mir.IntrinsicCharToInt,
			retType: mir.TInt,
			arg:     &mir.ConstOp{Const: &mir.CharConst{Value: 'A'}, T: mir.TChar},
			common: []string{
				"define i64 @charToInt() {",
				"  %t0 = zext i32 65 to i64",
				"  store i64 %t0, ptr %l0",
				"  ret i64",
			},
		},
		{
			name:    "intToByte",
			kind:    mir.IntrinsicIntToByte,
			retType: mir.TByte,
			arg:     &mir.ConstOp{Const: &mir.IntConst{Value: 7, T: mir.TInt}, T: mir.TInt},
			common: []string{
				"define i8 @intToByte() {",
				"  %t0 = trunc i64 7 to i8",
				"  store i8 %t0, ptr %l0",
				"  ret i8",
			},
		},
		{
			name:    "intToChar",
			kind:    mir.IntrinsicIntToChar,
			retType: mir.TChar,
			arg:     &mir.ConstOp{Const: &mir.IntConst{Value: 65, T: mir.TInt}, T: mir.TInt},
			common: []string{
				"define i32 @intToChar() {",
				"  %t0 = trunc i64 65 to i32",
				"  store i32 %t0, ptr %l0",
				"  ret i32",
			},
		},
		{
			name:    "byteToChar",
			kind:    mir.IntrinsicByteToChar,
			retType: mir.TChar,
			arg:     &mir.ConstOp{Const: &mir.ByteConst{Value: 7}, T: mir.TByte},
			common: []string{
				"define i32 @byteToChar() {",
				"  %t0 = zext i8 7 to i32",
				"  store i32 %t0, ptr %l0",
				"  ret i32",
			},
		},
		{
			name:    "charToByte",
			kind:    mir.IntrinsicCharToByte,
			retType: mir.TByte,
			arg:     &mir.ConstOp{Const: &mir.CharConst{Value: 'A'}, T: mir.TChar},
			common: []string{
				"define i8 @charToByte() {",
				"  %t0 = trunc i32 65 to i8",
				"  store i8 %t0, ptr %l0",
				"  ret i8",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fn := &mir.Function{Name: tc.name, ReturnType: tc.retType}
			fn.ReturnLocal = fn.NewLocal("_return", tc.retType, false, mir.Span{})
			fn.Locals[fn.ReturnLocal].IsReturn = true
			entry := fn.NewBlock(mir.Span{})
			fn.Entry = entry
			fn.Block(entry).Append(&mir.IntrinsicInstr{
				Dest: &mir.Place{Local: fn.ReturnLocal},
				Kind: tc.kind,
				Args: []mir.Operand{tc.arg},
			})
			fn.Block(entry).SetTerminator(&mir.ReturnTerm{})

			res := NewLowerer(Config{}).LowerMIR(&mir.Module{
				Package:   "main",
				Functions: []*mir.Function{fn},
			})
			if !res.OK() {
				t.Fatalf("LowerMIR() diagnostics = %+v, want OK", res.Diagnostics)
			}
			got := Render(res.Module)
			for _, want := range tc.common {
				if !strings.Contains(got, want) {
					t.Fatalf("rendered LIR missing %q:\n%s", want, got)
				}
			}
		})
	}
}

func TestLowerMIRTupleLiteralAndElementRead(t *testing.T) {
	tupleT := &ir.TupleType{Elems: []ir.Type{mir.TInt, mir.TInt}}
	fn := &mir.Function{Name: "pack", ReturnType: mir.TInt}
	fn.ReturnLocal = fn.NewLocal("_return", mir.TInt, false, mir.Span{})
	fn.Locals[fn.ReturnLocal].IsReturn = true
	tupleLocal := fn.NewLocal("t", tupleT, false, mir.Span{})
	entry := fn.NewBlock(mir.Span{})
	fn.Entry = entry
	fn.Block(entry).Append(&mir.AssignInstr{
		Dest: mir.Place{Local: tupleLocal},
		Src: &mir.AggregateRV{
			Kind: mir.AggTuple,
			Fields: []mir.Operand{
				&mir.ConstOp{Const: &mir.IntConst{Value: 1, T: mir.TInt}, T: mir.TInt},
				&mir.ConstOp{Const: &mir.IntConst{Value: 2, T: mir.TInt}, T: mir.TInt},
			},
			T: tupleT,
		},
	})
	fn.Block(entry).Append(&mir.AssignInstr{
		Dest: mir.Place{Local: fn.ReturnLocal},
		Src: &mir.BinaryRV{
			Op: mir.BinAdd,
			Left: &mir.CopyOp{
				Place: mir.Place{Local: tupleLocal}.Project(&mir.TupleProj{Index: 0, Type: mir.TInt}),
				T:     mir.TInt,
			},
			Right: &mir.CopyOp{
				Place: mir.Place{Local: tupleLocal}.Project(&mir.TupleProj{Index: 1, Type: mir.TInt}),
				T:     mir.TInt,
			},
			T: mir.TInt,
		},
	})
	fn.Block(entry).SetTerminator(&mir.ReturnTerm{})

	res := NewLowerer(Config{}).LowerMIR(&mir.Module{
		Package:   "main",
		Functions: []*mir.Function{fn},
	})
	if !res.OK() {
		t.Fatalf("LowerMIR() diagnostics = %+v, want OK", res.Diagnostics)
	}
	got := Render(res.Module)
	for _, want := range []string{
		"%Tuple.i64.i64 = type { i64, i64 }",
		"define i64 @pack() {",
		"  %t0 = insertvalue %Tuple.i64.i64 undef, i64 1, 0",
		"  %t1 = insertvalue %Tuple.i64.i64 %t0, i64 2, 1",
		"  store %Tuple.i64.i64 %t1, ptr %l1",
		"  %t3 = extractvalue %Tuple.i64.i64 %t2, 0",
		"  %t5 = extractvalue %Tuple.i64.i64 %t4, 1",
		"  %t6 = add i64 %t3, %t5",
		"  ret i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered LIR missing %q:\n%s", want, got)
		}
	}
}

func TestLowerMIRStructLiteralAndFieldRead(t *testing.T) {
	pointT := &ir.NamedType{Name: "Point"}
	layouts := mir.NewLayoutTable()
	layouts.Structs["Point"] = &mir.StructLayout{
		Name:    "Point",
		Mangled: "Point",
		Fields: []mir.FieldLayout{
			{Index: 0, Name: "x", Type: mir.TInt},
			{Index: 1, Name: "y", Type: mir.TInt},
		},
	}

	fn := &mir.Function{Name: "sum", ReturnType: mir.TInt}
	fn.ReturnLocal = fn.NewLocal("_return", mir.TInt, false, mir.Span{})
	fn.Locals[fn.ReturnLocal].IsReturn = true
	pointLocal := fn.NewLocal("p", pointT, false, mir.Span{})
	entry := fn.NewBlock(mir.Span{})
	fn.Entry = entry
	fn.Block(entry).Append(&mir.AssignInstr{
		Dest: mir.Place{Local: pointLocal},
		Src: &mir.AggregateRV{
			Kind: mir.AggStruct,
			Fields: []mir.Operand{
				&mir.ConstOp{Const: &mir.IntConst{Value: 1, T: mir.TInt}, T: mir.TInt},
				&mir.ConstOp{Const: &mir.IntConst{Value: 2, T: mir.TInt}, T: mir.TInt},
			},
			T: pointT,
		},
	})
	fn.Block(entry).Append(&mir.AssignInstr{
		Dest: mir.Place{Local: fn.ReturnLocal},
		Src: &mir.BinaryRV{
			Op: mir.BinAdd,
			Left: &mir.CopyOp{
				Place: mir.Place{Local: pointLocal}.Project(&mir.FieldProj{Index: 0, Name: "x", Type: mir.TInt}),
				T:     mir.TInt,
			},
			Right: &mir.CopyOp{
				Place: mir.Place{Local: pointLocal}.Project(&mir.FieldProj{Index: 1, Name: "y", Type: mir.TInt}),
				T:     mir.TInt,
			},
			T: mir.TInt,
		},
	})
	fn.Block(entry).SetTerminator(&mir.ReturnTerm{})

	res := NewLowerer(Config{}).LowerMIR(&mir.Module{
		Package:   "main",
		Layouts:   layouts,
		Functions: []*mir.Function{fn},
	})
	if !res.OK() {
		t.Fatalf("LowerMIR() diagnostics = %+v, want OK", res.Diagnostics)
	}
	got := Render(res.Module)
	for _, want := range []string{
		"%Point = type { i64, i64 }",
		"define i64 @sum() {",
		"  %t0 = insertvalue %Point undef, i64 1, 0",
		"  %t1 = insertvalue %Point %t0, i64 2, 1",
		"  store %Point %t1, ptr %l1",
		"  %t3 = extractvalue %Point %t2, 0",
		"  %t5 = extractvalue %Point %t4, 1",
		"  %t6 = add i64 %t3, %t5",
		"  ret i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered LIR missing %q:\n%s", want, got)
		}
	}
}

func TestLowerMIRNestedStructProjectedAssign(t *testing.T) {
	innerT := &ir.NamedType{Name: "Inner"}
	outerT := &ir.NamedType{Name: "Outer"}
	layouts := mir.NewLayoutTable()
	layouts.Structs["Inner"] = &mir.StructLayout{
		Name:    "Inner",
		Mangled: "Inner",
		Fields: []mir.FieldLayout{
			{Index: 0, Name: "value", Type: mir.TInt},
		},
	}
	layouts.Structs["Outer"] = &mir.StructLayout{
		Name:    "Outer",
		Mangled: "Outer",
		Fields: []mir.FieldLayout{
			{Index: 0, Name: "inner", Type: innerT},
			{Index: 1, Name: "flag", Type: mir.TInt},
		},
	}

	fn := &mir.Function{Name: "update", ReturnType: mir.TInt}
	fn.ReturnLocal = fn.NewLocal("_return", mir.TInt, false, mir.Span{})
	fn.Locals[fn.ReturnLocal].IsReturn = true
	innerLocal := fn.NewLocal("inner", innerT, false, mir.Span{})
	outerLocal := fn.NewLocal("outer", outerT, false, mir.Span{})
	entry := fn.NewBlock(mir.Span{})
	fn.Entry = entry
	fn.Block(entry).Append(&mir.AssignInstr{
		Dest: mir.Place{Local: innerLocal},
		Src: &mir.AggregateRV{
			Kind: mir.AggStruct,
			Fields: []mir.Operand{
				&mir.ConstOp{Const: &mir.IntConst{Value: 0, T: mir.TInt}, T: mir.TInt},
			},
			T: innerT,
		},
	})
	fn.Block(entry).Append(&mir.AssignInstr{
		Dest: mir.Place{Local: outerLocal},
		Src: &mir.AggregateRV{
			Kind: mir.AggStruct,
			Fields: []mir.Operand{
				&mir.CopyOp{Place: mir.Place{Local: innerLocal}, T: innerT},
				&mir.ConstOp{Const: &mir.IntConst{Value: 1, T: mir.TInt}, T: mir.TInt},
			},
			T: outerT,
		},
	})
	fn.Block(entry).Append(&mir.AssignInstr{
		Dest: mir.Place{Local: outerLocal}.
			Project(&mir.FieldProj{Index: 0, Name: "inner", Type: innerT}).
			Project(&mir.FieldProj{Index: 0, Name: "value", Type: mir.TInt}),
		Src: &mir.UseRV{
			Op: &mir.ConstOp{Const: &mir.IntConst{Value: 42, T: mir.TInt}, T: mir.TInt},
		},
	})
	fn.Block(entry).Append(&mir.AssignInstr{
		Dest: mir.Place{Local: fn.ReturnLocal},
		Src: &mir.BinaryRV{
			Op: mir.BinAdd,
			Left: &mir.CopyOp{
				Place: mir.Place{Local: outerLocal}.
					Project(&mir.FieldProj{Index: 0, Name: "inner", Type: innerT}).
					Project(&mir.FieldProj{Index: 0, Name: "value", Type: mir.TInt}),
				T: mir.TInt,
			},
			Right: &mir.CopyOp{
				Place: mir.Place{Local: outerLocal}.Project(&mir.FieldProj{Index: 1, Name: "flag", Type: mir.TInt}),
				T:     mir.TInt,
			},
			T: mir.TInt,
		},
	})
	fn.Block(entry).SetTerminator(&mir.ReturnTerm{})

	res := NewLowerer(Config{}).LowerMIR(&mir.Module{
		Package:   "main",
		Layouts:   layouts,
		Functions: []*mir.Function{fn},
	})
	if !res.OK() {
		t.Fatalf("LowerMIR() diagnostics = %+v, want OK", res.Diagnostics)
	}
	got := Render(res.Module)
	for _, want := range []string{
		"%Inner = type { i64 }",
		"%Outer = type { %Inner, i64 }",
		"define i64 @update() {",
		"extractvalue %Outer",
		"insertvalue %Inner",
		"insertvalue %Outer",
		"store %Outer",
		"add i64",
		"ret i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered LIR missing %q:\n%s", want, got)
		}
	}
}

func TestLowerMIRProjectedCallResult(t *testing.T) {
	cellT := &ir.NamedType{Name: "Cell"}
	layouts := mir.NewLayoutTable()
	layouts.Structs["Cell"] = &mir.StructLayout{
		Name:    "Cell",
		Mangled: "Cell",
		Fields: []mir.FieldLayout{
			{Index: 0, Name: "value", Type: mir.TInt},
			{Index: 1, Name: "other", Type: mir.TInt},
		},
	}

	makeFn := &mir.Function{Name: "makeValue", ReturnType: mir.TInt}
	makeFn.ReturnLocal = makeFn.NewLocal("_return", mir.TInt, false, mir.Span{})
	makeFn.Locals[makeFn.ReturnLocal].IsReturn = true
	makeEntry := makeFn.NewBlock(mir.Span{})
	makeFn.Entry = makeEntry
	makeFn.Block(makeEntry).Append(&mir.AssignInstr{
		Dest: mir.Place{Local: makeFn.ReturnLocal},
		Src: &mir.UseRV{
			Op: &mir.ConstOp{Const: &mir.IntConst{Value: 7, T: mir.TInt}, T: mir.TInt},
		},
	})
	makeFn.Block(makeEntry).SetTerminator(&mir.ReturnTerm{})

	fillFn := &mir.Function{Name: "fill", ReturnType: mir.TInt}
	fillFn.ReturnLocal = fillFn.NewLocal("_return", mir.TInt, false, mir.Span{})
	fillFn.Locals[fillFn.ReturnLocal].IsReturn = true
	cellLocal := fillFn.NewLocal("cell", cellT, false, mir.Span{})
	fillEntry := fillFn.NewBlock(mir.Span{})
	fillFn.Entry = fillEntry
	fillFn.Block(fillEntry).Append(&mir.AssignInstr{
		Dest: mir.Place{Local: cellLocal},
		Src: &mir.AggregateRV{
			Kind: mir.AggStruct,
			Fields: []mir.Operand{
				&mir.ConstOp{Const: &mir.IntConst{Value: 0, T: mir.TInt}, T: mir.TInt},
				&mir.ConstOp{Const: &mir.IntConst{Value: 1, T: mir.TInt}, T: mir.TInt},
			},
			T: cellT,
		},
	})
	fillFn.Block(fillEntry).Append(&mir.CallInstr{
		Dest: &mir.Place{
			Local: cellLocal,
			Projections: []mir.Projection{
				&mir.FieldProj{Index: 0, Name: "value", Type: mir.TInt},
			},
		},
		Callee: &mir.FnRef{
			Symbol: "makeValue",
			Type: &ir.FnType{
				Return: mir.TInt,
			},
		},
	})
	fillFn.Block(fillEntry).Append(&mir.AssignInstr{
		Dest: mir.Place{Local: fillFn.ReturnLocal},
		Src: &mir.BinaryRV{
			Op: mir.BinAdd,
			Left: &mir.CopyOp{
				Place: mir.Place{Local: cellLocal}.Project(&mir.FieldProj{Index: 0, Name: "value", Type: mir.TInt}),
				T:     mir.TInt,
			},
			Right: &mir.CopyOp{
				Place: mir.Place{Local: cellLocal}.Project(&mir.FieldProj{Index: 1, Name: "other", Type: mir.TInt}),
				T:     mir.TInt,
			},
			T: mir.TInt,
		},
	})
	fillFn.Block(fillEntry).SetTerminator(&mir.ReturnTerm{})

	res := NewLowerer(Config{}).LowerMIR(&mir.Module{
		Package:   "main",
		Layouts:   layouts,
		Functions: []*mir.Function{makeFn, fillFn},
	})
	if !res.OK() {
		t.Fatalf("LowerMIR() diagnostics = %+v, want OK", res.Diagnostics)
	}
	got := Render(res.Module)
	for _, want := range []string{
		"%Cell = type { i64, i64 }",
		"define i64 @makeValue() {",
		"define i64 @fill() {",
		"call i64 @makeValue()",
		"load %Cell",
		"insertvalue %Cell",
		"store %Cell",
		"extractvalue %Cell",
		"add i64",
		"ret i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered LIR missing %q:\n%s", want, got)
		}
	}
}

func TestLowerMIRProjectedIntrinsicResult(t *testing.T) {
	cellT := &ir.NamedType{Name: "Cell"}
	layouts := mir.NewLayoutTable()
	layouts.Structs["Cell"] = &mir.StructLayout{
		Name:    "Cell",
		Mangled: "Cell",
		Fields: []mir.FieldLayout{
			{Index: 0, Name: "value", Type: mir.TInt},
			{Index: 1, Name: "other", Type: mir.TInt},
		},
	}

	fn := &mir.Function{Name: "fillIntrinsic", ReturnType: mir.TInt}
	fn.ReturnLocal = fn.NewLocal("_return", mir.TInt, false, mir.Span{})
	fn.Locals[fn.ReturnLocal].IsReturn = true
	cellLocal := fn.NewLocal("cell", cellT, false, mir.Span{})
	byteLocal := fn.NewLocal("b", mir.TByte, false, mir.Span{})
	fn.Locals[byteLocal].IsParam = true
	fn.Params = []mir.LocalID{byteLocal}
	entry := fn.NewBlock(mir.Span{})
	fn.Entry = entry
	fn.Block(entry).Append(&mir.AssignInstr{
		Dest: mir.Place{Local: cellLocal},
		Src: &mir.AggregateRV{
			Kind: mir.AggStruct,
			Fields: []mir.Operand{
				&mir.ConstOp{Const: &mir.IntConst{Value: 0, T: mir.TInt}, T: mir.TInt},
				&mir.ConstOp{Const: &mir.IntConst{Value: 1, T: mir.TInt}, T: mir.TInt},
			},
			T: cellT,
		},
	})
	fn.Block(entry).Append(&mir.IntrinsicInstr{
		Dest: &mir.Place{
			Local: cellLocal,
			Projections: []mir.Projection{
				&mir.FieldProj{Index: 0, Name: "value", Type: mir.TInt},
			},
		},
		Kind: mir.IntrinsicByteToInt,
		Args: []mir.Operand{
			&mir.CopyOp{Place: mir.Place{Local: byteLocal}, T: mir.TByte},
		},
	})
	fn.Block(entry).Append(&mir.AssignInstr{
		Dest: mir.Place{Local: fn.ReturnLocal},
		Src: &mir.BinaryRV{
			Op: mir.BinAdd,
			Left: &mir.CopyOp{
				Place: mir.Place{Local: cellLocal}.Project(&mir.FieldProj{Index: 0, Name: "value", Type: mir.TInt}),
				T:     mir.TInt,
			},
			Right: &mir.CopyOp{
				Place: mir.Place{Local: cellLocal}.Project(&mir.FieldProj{Index: 1, Name: "other", Type: mir.TInt}),
				T:     mir.TInt,
			},
			T: mir.TInt,
		},
	})
	fn.Block(entry).SetTerminator(&mir.ReturnTerm{})

	res := NewLowerer(Config{}).LowerMIR(&mir.Module{
		Package:   "main",
		Layouts:   layouts,
		Functions: []*mir.Function{fn},
	})
	if !res.OK() {
		t.Fatalf("LowerMIR() diagnostics = %+v, want OK", res.Diagnostics)
	}
	got := Render(res.Module)
	for _, want := range []string{
		"%Cell = type { i64, i64 }",
		"define i64 @fillIntrinsic(i8 %p2) {",
		"zext i8",
		"to i64",
		"load %Cell",
		"insertvalue %Cell",
		"store %Cell",
		"extractvalue %Cell",
		"add i64",
		"ret i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered LIR missing %q:\n%s", want, got)
		}
	}
}

func appendReturnInt(fn *mir.Function, block mir.BlockID, value int64) {
	bb := fn.Block(block)
	bb.Append(&mir.AssignInstr{
		Dest: mir.Place{Local: fn.ReturnLocal},
		Src: &mir.UseRV{
			Op: &mir.ConstOp{Const: &mir.IntConst{Value: value, T: mir.TInt}, T: mir.TInt},
		},
	})
	bb.SetTerminator(&mir.ReturnTerm{})
}
