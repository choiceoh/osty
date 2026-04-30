package lirproto

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/llvmgen"
	"github.com/osty/osty/internal/mir"
)

func TestLowerMIRParityWithCurrentMIRGenerator(t *testing.T) {
	tests := []struct {
		name       string
		sourcePath string
		build      func() *mir.Module
		common     []string
	}{
		{
			name:       "const_return",
			sourcePath: "/tmp/lir_proto_parity_const.osty",
			build:      buildParityConstReturnModule,
			common: []string{
				"define i64 @answer()",
				"store i64 42",
				"ret i64",
			},
		},
		{
			name:       "direct_call",
			sourcePath: "/tmp/lir_proto_parity_call.osty",
			build:      buildParityDirectCallModule,
			common: []string{
				"define i64 @inc(i64",
				"define i64 @main()",
				"call i64 @inc(i64 41)",
				"ret i64",
			},
		},
		{
			name:       "branch",
			sourcePath: "/tmp/lir_proto_parity_branch.osty",
			build:      buildParityBranchModule,
			common: []string{
				"define i64 @choose(i1",
				"br i1",
				"label %bb1",
				"label %bb2",
				"store i64 1",
				"store i64 2",
				"ret i64",
			},
		},
		{
			name:       "switch",
			sourcePath: "/tmp/lir_proto_parity_switch.osty",
			build:      buildParitySwitchModule,
			common: []string{
				"define i64 @pick(i64",
				"switch i64",
				"i64 0, label %bb1",
				"i64 1, label %bb2",
				"label %bb3",
				"store i64 10",
				"store i64 20",
				"store i64 30",
				"ret i64",
			},
		},
		{
			name:       "println_string",
			sourcePath: "/tmp/lir_proto_parity_print_string.osty",
			build:      buildParityPrintlnStringModule,
			common: []string{
				`@.str.0 = private unnamed_addr constant [6 x i8] c"hello\00"`,
				"declare void @osty_rt_io_write(ptr, i1, i1)",
				"call void @osty_rt_io_write(ptr @.str.0, i1 true, i1 false)",
			},
		},
		{
			name:       "cast_int_resize",
			sourcePath: "/tmp/lir_proto_parity_cast_int_resize.osty",
			build:      buildParityCastIntResizeModule,
			common: []string{
				"define i64 @widen()",
				"sext i32",
				"to i64",
				"store i64",
				"ret i64",
			},
		},
		{
			name:       "cast_float_resize",
			sourcePath: "/tmp/lir_proto_parity_cast_float_resize.osty",
			build:      buildParityCastFloatResizeModule,
			common: []string{
				"define float @narrowFloat()",
				"fptrunc double",
				"to float",
				"store float",
				"ret float",
			},
		},
		{
			name:       "cast_int_to_float",
			sourcePath: "/tmp/lir_proto_parity_cast_int_to_float.osty",
			build:      buildParityCastIntToFloatModule,
			common: []string{
				"define double @toFloat()",
				"sitofp i64",
				"to double",
				"store double",
				"ret double",
			},
		},
		{
			name:       "direct_call_arg_coercion",
			sourcePath: "/tmp/lir_proto_parity_call_arg_coercion.osty",
			build:      buildParityDirectCallArgCoercionModule,
			common: []string{
				"define i64 @id(i64",
				"define i64 @main()",
				"sext i32",
				"to i64",
				"call i64 @id(i64",
				"ret i64",
			},
		},
		{
			name:       "primitive_int_to_byte",
			sourcePath: "/tmp/lir_proto_parity_primitive_int_to_byte.osty",
			build:      buildParityPrimitiveIntToByteModule,
			common: []string{
				"define i8 @intToByte()",
				"trunc i64",
				"to i8",
				"store i8",
				"ret i8",
			},
		},
		{
			name:       "primitive_byte_to_char",
			sourcePath: "/tmp/lir_proto_parity_primitive_byte_to_char.osty",
			build:      buildParityPrimitiveByteToCharModule,
			common: []string{
				"define i32 @byteToChar()",
				"zext i8",
				"to i32",
				"store i32",
				"ret i32",
			},
		},
		{
			name:       "tuple_literal_read",
			sourcePath: "/tmp/lir_proto_parity_tuple_literal_read.osty",
			build:      buildParityTupleLiteralReadModule,
			common: []string{
				"%Tuple.i64.i64 = type { i64, i64 }",
				"define i64 @pack()",
				"insertvalue %Tuple.i64.i64 undef, i64 1, 0",
				"insertvalue %Tuple.i64.i64",
				"extractvalue %Tuple.i64.i64",
				"add i64",
				"ret i64",
			},
		},
		{
			name:       "struct_literal_read",
			sourcePath: "/tmp/lir_proto_parity_struct_literal_read.osty",
			build:      buildParityStructLiteralReadModule,
			common: []string{
				"%Point = type { i64, i64 }",
				"define i64 @sum()",
				"insertvalue %Point undef, i64 1, 0",
				"insertvalue %Point",
				"extractvalue %Point",
				"add i64",
				"ret i64",
			},
		},
		{
			name:       "nested_struct_projected_assign",
			sourcePath: "/tmp/lir_proto_parity_nested_struct_projected_assign.osty",
			build:      buildParityNestedStructProjectedAssignModule,
			common: []string{
				"%Inner = type { i64 }",
				"%Outer = type { %Inner, i64 }",
				"define i64 @update()",
				"extractvalue %Outer",
				"insertvalue %Inner",
				"insertvalue %Outer",
				"store %Outer",
				"add i64",
				"ret i64",
			},
		},
		{
			name:       "projected_call_result",
			sourcePath: "/tmp/lir_proto_parity_projected_call_result.osty",
			build:      buildParityProjectedCallResultModule,
			common: []string{
				"%Cell = type { i64, i64 }",
				"define i64 @makeValue()",
				"define i64 @fill()",
				"call i64 @makeValue()",
				"load %Cell",
				"insertvalue %Cell",
				"store %Cell",
				"extractvalue %Cell",
				"add i64",
				"ret i64",
			},
		},
		{
			name:       "projected_intrinsic_result",
			sourcePath: "/tmp/lir_proto_parity_projected_intrinsic_result.osty",
			build:      buildParityProjectedIntrinsicResultModule,
			common: []string{
				"%Cell = type { i64, i64 }",
				"define i64 @fillIntrinsic(i8",
				"zext i8",
				"to i64",
				"load %Cell",
				"insertvalue %Cell",
				"store %Cell",
				"extractvalue %Cell",
				"add i64",
				"ret i64",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lirOut := renderParityLIR(t, tc.build(), tc.sourcePath)
			mirOut := renderCurrentMIRGenerator(t, tc.build(), tc.sourcePath)
			for _, want := range tc.common {
				assertContains(t, "LIR Proto", lirOut, want)
				assertContains(t, "current MIR generator", mirOut, want)
			}
		})
	}
}

func renderParityLIR(t *testing.T, mod *mir.Module, sourcePath string) string {
	t.Helper()
	res := NewLowerer(Config{
		PackageName: "main",
		SourcePath:  sourcePath,
	}).LowerMIR(mod)
	if !res.OK() {
		t.Fatalf("LIR Proto diagnostics = %+v, want OK", res.Diagnostics)
	}
	return Render(res.Module)
}

func renderCurrentMIRGenerator(t *testing.T, mod *mir.Module, sourcePath string) string {
	t.Helper()
	out, err := llvmgen.GenerateFromMIR(mod, llvmgen.Options{
		PackageName: "main",
		SourcePath:  sourcePath,
	})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	return string(out)
}

func assertContains(t *testing.T, label, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("%s output missing %q:\n%s", label, want, got)
	}
}

func buildParityConstReturnModule() *mir.Module {
	fn := &mir.Function{Name: "answer", ReturnType: mir.TInt}
	fn.ReturnLocal = fn.NewLocal("_return", mir.TInt, false, mir.Span{})
	fn.Locals[fn.ReturnLocal].IsReturn = true
	entry := fn.NewBlock(mir.Span{})
	fn.Entry = entry
	fn.Block(entry).Append(&mir.AssignInstr{
		Dest: mir.Place{Local: fn.ReturnLocal},
		Src: &mir.UseRV{
			Op: &mir.ConstOp{Const: &mir.IntConst{Value: 42, T: mir.TInt}, T: mir.TInt},
		},
	})
	fn.Block(entry).SetTerminator(&mir.ReturnTerm{})
	return &mir.Module{Package: "main", Functions: []*mir.Function{fn}}
}

func buildParityDirectCallModule() *mir.Module {
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
	return &mir.Module{Package: "main", Functions: []*mir.Function{callee, caller}}
}

func buildParityBranchModule() *mir.Module {
	fn := &mir.Function{Name: "choose", ReturnType: mir.TInt}
	fn.ReturnLocal = fn.NewLocal("_return", mir.TInt, false, mir.Span{})
	fn.Locals[fn.ReturnLocal].IsReturn = true
	cond := fn.NewLocal("cond", mir.TBool, false, mir.Span{})
	fn.Locals[cond].IsParam = true
	fn.Params = []mir.LocalID{cond}

	entry := fn.NewBlock(mir.Span{})
	thenBlock := fn.NewBlock(mir.Span{})
	elseBlock := fn.NewBlock(mir.Span{})
	fn.Entry = entry
	fn.Block(entry).SetTerminator(&mir.BranchTerm{
		Cond: &mir.CopyOp{Place: mir.Place{Local: cond}, T: mir.TBool},
		Then: thenBlock,
		Else: elseBlock,
	})
	appendParityReturnInt(fn, thenBlock, 1)
	appendParityReturnInt(fn, elseBlock, 2)
	return &mir.Module{Package: "main", Functions: []*mir.Function{fn}}
}

func buildParitySwitchModule() *mir.Module {
	fn := &mir.Function{Name: "pick", ReturnType: mir.TInt}
	fn.ReturnLocal = fn.NewLocal("_return", mir.TInt, false, mir.Span{})
	fn.Locals[fn.ReturnLocal].IsReturn = true
	n := fn.NewLocal("n", mir.TInt, false, mir.Span{})
	fn.Locals[n].IsParam = true
	fn.Params = []mir.LocalID{n}

	entry := fn.NewBlock(mir.Span{})
	zero := fn.NewBlock(mir.Span{})
	one := fn.NewBlock(mir.Span{})
	defaultBlock := fn.NewBlock(mir.Span{})
	fn.Entry = entry
	fn.Block(entry).SetTerminator(&mir.SwitchIntTerm{
		Scrutinee: &mir.CopyOp{Place: mir.Place{Local: n}, T: mir.TInt},
		Cases: []mir.SwitchCase{
			{Value: 0, Target: zero},
			{Value: 1, Target: one},
		},
		Default: defaultBlock,
	})
	appendParityReturnInt(fn, zero, 10)
	appendParityReturnInt(fn, one, 20)
	appendParityReturnInt(fn, defaultBlock, 30)
	return &mir.Module{Package: "main", Functions: []*mir.Function{fn}}
}

func buildParityPrintlnStringModule() *mir.Module {
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
	return &mir.Module{Package: "main", Functions: []*mir.Function{fn}}
}

func buildParityCastIntResizeModule() *mir.Module {
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
	return &mir.Module{Package: "main", Functions: []*mir.Function{fn}}
}

func buildParityCastFloatResizeModule() *mir.Module {
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
	return &mir.Module{Package: "main", Functions: []*mir.Function{fn}}
}

func buildParityCastIntToFloatModule() *mir.Module {
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
	return &mir.Module{Package: "main", Functions: []*mir.Function{fn}}
}

func buildParityDirectCallArgCoercionModule() *mir.Module {
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
	tmp := caller.NewLocal("tmp", mir.TInt32, false, mir.Span{})
	callerEntry := caller.NewBlock(mir.Span{})
	caller.Entry = callerEntry
	caller.Block(callerEntry).Append(&mir.AssignInstr{
		Dest: mir.Place{Local: tmp},
		Src: &mir.UseRV{
			Op: &mir.ConstOp{Const: &mir.IntConst{Value: 41, T: mir.TInt32}, T: mir.TInt32},
		},
	})
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
			&mir.CopyOp{Place: mir.Place{Local: tmp}, T: mir.TInt32},
		},
	})
	caller.Block(callerEntry).SetTerminator(&mir.ReturnTerm{})
	return &mir.Module{Package: "main", Functions: []*mir.Function{callee, caller}}
}

func buildParityPrimitiveIntToByteModule() *mir.Module {
	fn := &mir.Function{Name: "intToByte", ReturnType: mir.TByte}
	fn.ReturnLocal = fn.NewLocal("_return", mir.TByte, false, mir.Span{})
	fn.Locals[fn.ReturnLocal].IsReturn = true
	entry := fn.NewBlock(mir.Span{})
	fn.Entry = entry
	fn.Block(entry).Append(&mir.IntrinsicInstr{
		Dest: &mir.Place{Local: fn.ReturnLocal},
		Kind: mir.IntrinsicIntToByte,
		Args: []mir.Operand{
			&mir.ConstOp{Const: &mir.IntConst{Value: 7, T: mir.TInt}, T: mir.TInt},
		},
	})
	fn.Block(entry).SetTerminator(&mir.ReturnTerm{})
	return &mir.Module{Package: "main", Functions: []*mir.Function{fn}}
}

func buildParityPrimitiveByteToCharModule() *mir.Module {
	fn := &mir.Function{Name: "byteToChar", ReturnType: mir.TChar}
	fn.ReturnLocal = fn.NewLocal("_return", mir.TChar, false, mir.Span{})
	fn.Locals[fn.ReturnLocal].IsReturn = true
	entry := fn.NewBlock(mir.Span{})
	fn.Entry = entry
	fn.Block(entry).Append(&mir.IntrinsicInstr{
		Dest: &mir.Place{Local: fn.ReturnLocal},
		Kind: mir.IntrinsicByteToChar,
		Args: []mir.Operand{
			&mir.ConstOp{Const: &mir.ByteConst{Value: 7}, T: mir.TByte},
		},
	})
	fn.Block(entry).SetTerminator(&mir.ReturnTerm{})
	return &mir.Module{Package: "main", Functions: []*mir.Function{fn}}
}

func buildParityTupleLiteralReadModule() *mir.Module {
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
	return &mir.Module{Package: "main", Functions: []*mir.Function{fn}}
}

func buildParityStructLiteralReadModule() *mir.Module {
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
	return &mir.Module{Package: "main", Layouts: layouts, Functions: []*mir.Function{fn}}
}

func buildParityNestedStructProjectedAssignModule() *mir.Module {
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
	return &mir.Module{Package: "main", Layouts: layouts, Functions: []*mir.Function{fn}}
}

func buildParityProjectedCallResultModule() *mir.Module {
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
	return &mir.Module{Package: "main", Layouts: layouts, Functions: []*mir.Function{makeFn, fillFn}}
}

func buildParityProjectedIntrinsicResultModule() *mir.Module {
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
	return &mir.Module{Package: "main", Layouts: layouts, Functions: []*mir.Function{fn}}
}

func appendParityReturnInt(fn *mir.Function, block mir.BlockID, value int64) {
	bb := fn.Block(block)
	bb.Append(&mir.AssignInstr{
		Dest: mir.Place{Local: fn.ReturnLocal},
		Src: &mir.UseRV{
			Op: &mir.ConstOp{Const: &mir.IntConst{Value: value, T: mir.TInt}, T: mir.TInt},
		},
	})
	bb.SetTerminator(&mir.ReturnTerm{})
}
