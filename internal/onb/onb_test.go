package onb

import (
	"bytes"
	"debug/macho"
	"errors"
	"strings"
	"testing"

	"github.com/osty/osty/internal/mir"
)

func TestResolveTargetAcceptsPhaseOneAArch64Targets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		triple string
		os     string
		format string
	}{
		{triple: "aarch64-apple-darwin", os: "darwin", format: "mach-o"},
		{triple: "arm64-apple-darwin", os: "darwin", format: "mach-o"},
		{triple: "aarch64-unknown-linux-gnu", os: "linux", format: "elf"},
	}
	for _, tt := range tests {
		got, err := ResolveTarget(tt.triple)
		if err != nil {
			t.Fatalf("ResolveTarget(%q) returned error: %v", tt.triple, err)
		}
		if got.OS != tt.os || got.Arch != "aarch64" || got.ObjectFormat != tt.format {
			t.Fatalf("ResolveTarget(%q) = %+v", tt.triple, got)
		}
	}
}

func TestResolveTargetRejectsNonAArch64(t *testing.T) {
	t.Parallel()

	_, err := ResolveTarget("x86_64-unknown-linux-gnu")
	if !errors.Is(err, ErrUnsupportedTarget) {
		t.Fatalf("ResolveTarget() error = %v, want ErrUnsupportedTarget", err)
	}
}

func TestCompileReturnsPhasePlanAndNotImplemented(t *testing.T) {
	t.Parallel()

	mod := &mir.Module{
		Functions: []*mir.Function{unitMainMIR()},
	}
	plan, err := Compile(t.Context(), Request{
		Module:       mod,
		TargetTriple: "aarch64-apple-darwin",
		EmitMode:     "object",
	})
	if !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("Compile() error = %v, want ErrNotImplemented", err)
	}
	if plan == nil {
		t.Fatal("Compile() returned nil plan")
	}
	if plan.Program == nil || len(plan.Program.Functions) != 1 {
		t.Fatalf("Compile() program = %+v", plan.Program)
	}
	if got := plan.StageSummary(); !strings.Contains(got, "MIR consumer=implemented") {
		t.Fatalf("StageSummary() = %q", got)
	}
	if got := plan.StageSummary(); !strings.Contains(got, "aarch64 assembly renderer=implemented") {
		t.Fatalf("StageSummary() = %q", got)
	}
	if got := plan.NextPlannedStage(); got != "4-pass minimal optimizer" {
		t.Fatalf("NextPlannedStage() = %q, want 4-pass minimal optimizer", got)
	}
}

func TestLowerMIRLowersUnitMainToAArch64LIR(t *testing.T) {
	t.Parallel()

	program, err := LowerMIR(&mir.Module{
		Functions: []*mir.Function{unitMainMIR()},
	}, Target{Triple: "aarch64-apple-darwin", OS: "darwin", Arch: "aarch64", ObjectFormat: "mach-o"})
	if err != nil {
		t.Fatalf("LowerMIR() returned error: %v", err)
	}
	if len(program.Functions) != 1 || len(program.Functions[0].Blocks) != 1 {
		t.Fatalf("LowerMIR() = %+v", program)
	}
	instrs := program.Functions[0].Blocks[0].Instrs
	if len(instrs) != 2 {
		t.Fatalf("main instr count = %d, want 2", len(instrs))
	}
	mov, ok := instrs[0].(*MovImm32)
	if !ok {
		t.Fatalf("main instr[0] = %T, want *MovImm32", instrs[0])
	}
	if mov.Dst != RegW0 || mov.Imm != 0 {
		t.Fatalf("main mov = %+v, want w0/#0", mov)
	}
	if _, ok := instrs[1].(*Ret); !ok {
		t.Fatalf("main instr[1] = %T, want *Ret", instrs[1])
	}
}

func TestLowerMIRLowersPrintlnStringLiteral(t *testing.T) {
	t.Parallel()

	program, err := LowerMIR(&mir.Module{
		Functions: []*mir.Function{helloMainMIR("hi")},
	}, Target{Triple: "aarch64-apple-darwin", OS: "darwin", Arch: "aarch64", ObjectFormat: "mach-o"})
	if err != nil {
		t.Fatalf("LowerMIR() returned error: %v", err)
	}
	if len(program.CStrings) != 1 {
		t.Fatalf("CStrings = %+v, want one literal", program.CStrings)
	}
	if program.CStrings[0].Label != "str0" || program.CStrings[0].Value != "hi" {
		t.Fatalf("CStrings[0] = %+v, want str0/hi", program.CStrings[0])
	}
	instrs := program.Functions[0].Blocks[0].Instrs
	if len(instrs) != 4 {
		t.Fatalf("main instr count = %d, want 4", len(instrs))
	}
	load, ok := instrs[0].(*LoadCStringAddress)
	if !ok {
		t.Fatalf("main instr[0] = %T, want *LoadCStringAddress", instrs[0])
	}
	if load.Dst != RegX0 || load.Label != "str0" {
		t.Fatalf("load = %+v, want x0/str0", load)
	}
	bl, ok := instrs[1].(*BranchLink)
	if !ok {
		t.Fatalf("main instr[1] = %T, want *BranchLink", instrs[1])
	}
	if bl.Symbol != "puts" {
		t.Fatalf("branch symbol = %q, want puts", bl.Symbol)
	}
}

func TestLowerMIRLowersPrintlnIntLiteral(t *testing.T) {
	t.Parallel()

	program, err := LowerMIR(&mir.Module{
		Functions: []*mir.Function{intPrintlnMainMIR(123)},
	}, Target{Triple: "aarch64-apple-darwin", OS: "darwin", Arch: "aarch64", ObjectFormat: "mach-o"})
	if err != nil {
		t.Fatalf("LowerMIR() returned error: %v", err)
	}
	if len(program.CStrings) != 1 || program.CStrings[0].Value != "%lld\n" {
		t.Fatalf("CStrings = %+v, want printf format", program.CStrings)
	}
	instrs := program.Functions[0].Blocks[0].Instrs
	if len(instrs) != 6 {
		t.Fatalf("main instr count = %d, want 6", len(instrs))
	}
	mov, ok := instrs[1].(*MovImm64)
	if !ok {
		t.Fatalf("main instr[1] = %T, want *MovImm64", instrs[1])
	}
	if mov.Dst != RegX1 || mov.Imm != 123 {
		t.Fatalf("mov64 = %+v, want x1/#123", mov)
	}
	store, ok := instrs[2].(*Store64Stack)
	if !ok {
		t.Fatalf("main instr[2] = %T, want *Store64Stack", instrs[2])
	}
	if store.Src != RegX1 || store.Offset != 0 {
		t.Fatalf("store = %+v, want x1/[sp]", store)
	}
	bl, ok := instrs[3].(*BranchLink)
	if !ok {
		t.Fatalf("main instr[3] = %T, want *BranchLink", instrs[3])
	}
	if bl.Symbol != "printf" {
		t.Fatalf("branch symbol = %q, want printf", bl.Symbol)
	}
}

func TestRenderAssemblyRendersDarwinMain(t *testing.T) {
	t.Parallel()

	program, err := LowerMIR(&mir.Module{
		Functions: []*mir.Function{unitMainMIR()},
	}, Target{Triple: "aarch64-apple-darwin", OS: "darwin", Arch: "aarch64", ObjectFormat: "mach-o"})
	if err != nil {
		t.Fatalf("LowerMIR() returned error: %v", err)
	}
	asm, err := RenderAssembly(program)
	if err != nil {
		t.Fatalf("RenderAssembly() returned error: %v", err)
	}
	text := string(asm)
	for _, want := range []string{".globl _main", "_main:", "\tmov w0, #0", "\tret"} {
		if !strings.Contains(text, want) {
			t.Fatalf("assembly missing %q:\n%s", want, text)
		}
	}
}

func TestRenderAssemblyRendersDarwinPrintlnLiteral(t *testing.T) {
	t.Parallel()

	program, err := LowerMIR(&mir.Module{
		Functions: []*mir.Function{helloMainMIR("hi")},
	}, Target{Triple: "aarch64-apple-darwin", OS: "darwin", Arch: "aarch64", ObjectFormat: "mach-o"})
	if err != nil {
		t.Fatalf("LowerMIR() returned error: %v", err)
	}
	asm, err := RenderAssembly(program)
	if err != nil {
		t.Fatalf("RenderAssembly() returned error: %v", err)
	}
	text := string(asm)
	for _, want := range []string{
		"\tstp x29, x30, [sp, #-16]!",
		"\tadrp x0, L_.str0@PAGE",
		"\tadd x0, x0, L_.str0@PAGEOFF",
		"\tbl _puts",
		"\tldp x29, x30, [sp], #16",
		".section __TEXT,__cstring,cstring_literals",
		"L_.str0:",
		"\t.asciz \"hi\"",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("assembly missing %q:\n%s", want, text)
		}
	}
}

func TestRenderAssemblyRendersDarwinPrintlnIntLiteral(t *testing.T) {
	t.Parallel()

	program, err := LowerMIR(&mir.Module{
		Functions: []*mir.Function{intPrintlnMainMIR(123)},
	}, Target{Triple: "aarch64-apple-darwin", OS: "darwin", Arch: "aarch64", ObjectFormat: "mach-o"})
	if err != nil {
		t.Fatalf("LowerMIR() returned error: %v", err)
	}
	asm, err := RenderAssembly(program)
	if err != nil {
		t.Fatalf("RenderAssembly() returned error: %v", err)
	}
	text := string(asm)
	for _, want := range []string{
		"\tsub sp, sp, #32",
		"\tadrp x0, L_.str0@PAGE",
		"\tmovz x1, #123",
		"\tstr x1, [sp]",
		"\tbl _printf",
		"\tadd sp, sp, #32",
		"\t.asciz \"%lld\\n\"",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("assembly missing %q:\n%s", want, text)
		}
	}
}

func TestEmitObjectWritesMinimalMachO(t *testing.T) {
	t.Parallel()

	program, err := LowerMIR(&mir.Module{
		Functions: []*mir.Function{unitMainMIR()},
	}, Target{Triple: "aarch64-apple-darwin", OS: "darwin", Arch: "aarch64", ObjectFormat: "mach-o"})
	if err != nil {
		t.Fatalf("LowerMIR() returned error: %v", err)
	}
	obj, err := EmitObject(program)
	if err != nil {
		t.Fatalf("EmitObject() returned error: %v", err)
	}
	f, err := macho.NewFile(bytes.NewReader(obj))
	if err != nil {
		t.Fatalf("macho.NewFile() returned error: %v", err)
	}
	if f.Type != macho.TypeObj {
		t.Fatalf("Mach-O type = %v, want object", f.Type)
	}
	if f.Cpu != macho.CpuArm64 {
		t.Fatalf("Mach-O CPU = %v, want arm64", f.Cpu)
	}
	if len(f.Sections) != 1 || f.Sections[0].Name != "__text" {
		t.Fatalf("Mach-O sections = %+v", f.Sections)
	}
	text, err := f.Sections[0].Data()
	if err != nil {
		t.Fatalf("__text Data() returned error: %v", err)
	}
	wantText := []byte{0x00, 0x00, 0x80, 0x52, 0xc0, 0x03, 0x5f, 0xd6}
	if !bytes.Equal(text, wantText) {
		t.Fatalf("__text = % x, want % x", text, wantText)
	}
	if f.Symtab == nil || len(f.Symtab.Syms) != 1 || f.Symtab.Syms[0].Name != "_main" {
		t.Fatalf("Mach-O symtab = %+v", f.Symtab)
	}
}

func TestEmitObjectWritesMachOPrintlnRelocations(t *testing.T) {
	t.Parallel()

	program, err := LowerMIR(&mir.Module{
		Functions: []*mir.Function{helloMainMIR("hi")},
	}, Target{Triple: "aarch64-apple-darwin", OS: "darwin", Arch: "aarch64", ObjectFormat: "mach-o"})
	if err != nil {
		t.Fatalf("LowerMIR() returned error: %v", err)
	}
	obj, err := EmitObject(program)
	if err != nil {
		t.Fatalf("EmitObject() returned error: %v", err)
	}
	f, err := macho.NewFile(bytes.NewReader(obj))
	if err != nil {
		t.Fatalf("macho.NewFile() returned error: %v", err)
	}
	if len(f.Sections) != 2 || f.Sections[0].Name != "__text" || f.Sections[1].Name != "__cstring" {
		t.Fatalf("Mach-O sections = %+v", f.Sections)
	}
	if len(f.Sections[0].Relocs) != 3 {
		t.Fatalf("__text relocs = %+v, want 3", f.Sections[0].Relocs)
	}
	if f.Symtab == nil {
		t.Fatal("Mach-O symtab is nil")
	}
	var sawMain, sawString, sawPuts bool
	for _, sym := range f.Symtab.Syms {
		switch sym.Name {
		case "_main":
			sawMain = true
		case "L_.str0":
			sawString = true
		case "_puts":
			sawPuts = true
		}
	}
	if !sawMain || !sawString || !sawPuts {
		t.Fatalf("Mach-O symbols = %+v, want _main, L_.str0, _puts", f.Symtab.Syms)
	}
}

func TestEmitObjectWritesMachOPrintlnIntRelocations(t *testing.T) {
	t.Parallel()

	program, err := LowerMIR(&mir.Module{
		Functions: []*mir.Function{intPrintlnMainMIR(123)},
	}, Target{Triple: "aarch64-apple-darwin", OS: "darwin", Arch: "aarch64", ObjectFormat: "mach-o"})
	if err != nil {
		t.Fatalf("LowerMIR() returned error: %v", err)
	}
	obj, err := EmitObject(program)
	if err != nil {
		t.Fatalf("EmitObject() returned error: %v", err)
	}
	f, err := macho.NewFile(bytes.NewReader(obj))
	if err != nil {
		t.Fatalf("macho.NewFile() returned error: %v", err)
	}
	if len(f.Sections) != 2 || f.Sections[0].Name != "__text" || f.Sections[1].Name != "__cstring" {
		t.Fatalf("Mach-O sections = %+v", f.Sections)
	}
	if len(f.Sections[0].Relocs) != 3 {
		t.Fatalf("__text relocs = %+v, want 3", f.Sections[0].Relocs)
	}
	var sawPrintf bool
	for _, sym := range f.Symtab.Syms {
		if sym.Name == "_printf" {
			sawPrintf = true
		}
	}
	if !sawPrintf {
		t.Fatalf("Mach-O symbols = %+v, want _printf", f.Symtab.Syms)
	}
}

func unitMainMIR() *mir.Function {
	fn := &mir.Function{
		Name:        "main",
		ReturnType:  mir.TUnit,
		ReturnLocal: 0,
		Entry:       0,
		Locals: []*mir.Local{
			{ID: 0, Name: "$ret", Type: mir.TUnit, IsReturn: true},
		},
	}
	block := fn.NewBlock(mir.Span{})
	fn.Block(block).SetTerminator(&mir.ReturnTerm{})
	return fn
}

func helloMainMIR(text string) *mir.Function {
	fn := unitMainMIR()
	fn.Block(fn.Entry).Instrs = append(fn.Block(fn.Entry).Instrs, &mir.IntrinsicInstr{
		Kind: mir.IntrinsicPrintln,
		Args: []mir.Operand{
			&mir.ConstOp{Const: &mir.StringConst{Value: text}, T: mir.TString},
		},
	})
	return fn
}

func intPrintlnMainMIR(value int64) *mir.Function {
	fn := unitMainMIR()
	fn.Block(fn.Entry).Instrs = append(fn.Block(fn.Entry).Instrs, &mir.IntrinsicInstr{
		Kind: mir.IntrinsicPrintln,
		Args: []mir.Operand{
			&mir.ConstOp{Const: &mir.IntConst{Value: value, T: mir.TInt}, T: mir.TInt},
		},
	})
	return fn
}

// addLocalsMIR builds the MIR that the front-end emits for
//
//	fn main() {
//	    let x = 10
//	    let y = 32
//	    println(x + y)
//	}
//
// after const-folding x and y. The named locals are elided, leaving a single
// temp local that holds the binary-op result and feeds into println.
func addLocalsMIR() *mir.Function {
	fn := &mir.Function{
		Name:        "main",
		ReturnType:  mir.TUnit,
		ReturnLocal: 0,
		Entry:       0,
		Locals: []*mir.Local{
			{ID: 0, Name: "$ret", Type: mir.TUnit, IsReturn: true},
			{ID: 1, Name: "tmp", Type: mir.TInt},
		},
	}
	block := fn.NewBlock(mir.Span{})
	fn.Block(block).Instrs = []mir.Instr{
		&mir.AssignInstr{
			Dest: mir.Place{Local: 1},
			Src: &mir.BinaryRV{
				Op:    mir.BinAdd,
				Left:  &mir.ConstOp{Const: &mir.IntConst{Value: 10, T: mir.TInt}, T: mir.TInt},
				Right: &mir.ConstOp{Const: &mir.IntConst{Value: 32, T: mir.TInt}, T: mir.TInt},
				T:     mir.TInt,
			},
		},
		&mir.IntrinsicInstr{
			Kind: mir.IntrinsicPrintln,
			Args: []mir.Operand{&mir.CopyOp{Place: mir.Place{Local: 1}, T: mir.TInt}},
		},
	}
	fn.Block(block).SetTerminator(&mir.ReturnTerm{})
	return fn
}

// chainMIR builds the MIR for `let a = 10; let b = 3; println(a - b * 2)` —
// the multiplication folds into a temp (local2), then the subtraction reads
// that temp via Copy and feeds the result (local1) into println.
func chainMIR() *mir.Function {
	fn := &mir.Function{
		Name:        "main",
		ReturnType:  mir.TUnit,
		ReturnLocal: 0,
		Entry:       0,
		Locals: []*mir.Local{
			{ID: 0, Name: "$ret", Type: mir.TUnit, IsReturn: true},
			{ID: 1, Name: "outer", Type: mir.TInt},
			{ID: 2, Name: "inner", Type: mir.TInt},
		},
	}
	block := fn.NewBlock(mir.Span{})
	fn.Block(block).Instrs = []mir.Instr{
		&mir.AssignInstr{
			Dest: mir.Place{Local: 2},
			Src: &mir.BinaryRV{
				Op:    mir.BinMul,
				Left:  &mir.ConstOp{Const: &mir.IntConst{Value: 3, T: mir.TInt}, T: mir.TInt},
				Right: &mir.ConstOp{Const: &mir.IntConst{Value: 2, T: mir.TInt}, T: mir.TInt},
				T:     mir.TInt,
			},
		},
		&mir.AssignInstr{
			Dest: mir.Place{Local: 1},
			Src: &mir.BinaryRV{
				Op:    mir.BinSub,
				Left:  &mir.ConstOp{Const: &mir.IntConst{Value: 10, T: mir.TInt}, T: mir.TInt},
				Right: &mir.CopyOp{Place: mir.Place{Local: 2}, T: mir.TInt},
				T:     mir.TInt,
			},
		},
		&mir.IntrinsicInstr{
			Kind: mir.IntrinsicPrintln,
			Args: []mir.Operand{&mir.CopyOp{Place: mir.Place{Local: 1}, T: mir.TInt}},
		},
	}
	fn.Block(block).SetTerminator(&mir.ReturnTerm{})
	return fn
}

func TestLowerMIRAssignsSlotsForReadLocalsOnly(t *testing.T) {
	t.Parallel()

	program, err := LowerMIR(&mir.Module{
		Functions: []*mir.Function{addLocalsMIR()},
	}, Target{Triple: "aarch64-apple-darwin", OS: "darwin", Arch: "aarch64", ObjectFormat: "mach-o"})
	if err != nil {
		t.Fatalf("LowerMIR() returned error: %v", err)
	}
	fn := program.Functions[0]
	if fn.FrameSize == 0 {
		t.Fatalf("FrameSize = 0, want non-zero (printf vararg + 1 local + FP/LR)")
	}
	if fn.FrameSize%16 != 0 {
		t.Fatalf("FrameSize = %d, want multiple of 16", fn.FrameSize)
	}
}

func TestLowerMIRLowersAddBetweenConstants(t *testing.T) {
	t.Parallel()

	program, err := LowerMIR(&mir.Module{
		Functions: []*mir.Function{addLocalsMIR()},
	}, Target{Triple: "aarch64-apple-darwin", OS: "darwin", Arch: "aarch64", ObjectFormat: "mach-o"})
	if err != nil {
		t.Fatalf("LowerMIR() returned error: %v", err)
	}
	instrs := program.Functions[0].Blocks[0].Instrs
	var sawMov10, sawMov32, sawAdd, sawStore, sawLoadForPrintf bool
	for _, instr := range instrs {
		switch i := instr.(type) {
		case *MovImm64:
			if i.Imm == 10 {
				sawMov10 = true
			}
			if i.Imm == 32 {
				sawMov32 = true
			}
		case *AddReg:
			sawAdd = true
		case *Store64Stack:
			if i.Offset != 0 {
				sawStore = true // local store, not vararg
			}
		case *Load64Stack:
			if i.Dst == RegX1 {
				sawLoadForPrintf = true
			}
		}
	}
	if !sawMov10 || !sawMov32 {
		t.Fatalf("expected mov #10 and mov #32; instrs=%+v", instrs)
	}
	if !sawAdd {
		t.Fatalf("expected AddReg; instrs=%+v", instrs)
	}
	if !sawStore {
		t.Fatalf("expected Store64Stack at local slot; instrs=%+v", instrs)
	}
	if !sawLoadForPrintf {
		t.Fatalf("expected Load64Stack into x1 for printf; instrs=%+v", instrs)
	}
}

func TestLowerMIRLowersSubMulChain(t *testing.T) {
	t.Parallel()

	program, err := LowerMIR(&mir.Module{
		Functions: []*mir.Function{chainMIR()},
	}, Target{Triple: "aarch64-apple-darwin", OS: "darwin", Arch: "aarch64", ObjectFormat: "mach-o"})
	if err != nil {
		t.Fatalf("LowerMIR() returned error: %v", err)
	}
	instrs := program.Functions[0].Blocks[0].Instrs
	var sawMul, sawSub bool
	for _, instr := range instrs {
		switch instr.(type) {
		case *MulReg:
			sawMul = true
		case *SubReg:
			sawSub = true
		}
	}
	if !sawMul {
		t.Fatalf("expected MulReg; instrs=%+v", instrs)
	}
	if !sawSub {
		t.Fatalf("expected SubReg; instrs=%+v", instrs)
	}
}

func TestRenderAssemblyRendersArithFrame(t *testing.T) {
	t.Parallel()

	program, err := LowerMIR(&mir.Module{
		Functions: []*mir.Function{addLocalsMIR()},
	}, Target{Triple: "aarch64-apple-darwin", OS: "darwin", Arch: "aarch64", ObjectFormat: "mach-o"})
	if err != nil {
		t.Fatalf("LowerMIR() returned error: %v", err)
	}
	asm, err := RenderAssembly(program)
	if err != nil {
		t.Fatalf("RenderAssembly() returned error: %v", err)
	}
	text := string(asm)
	for _, want := range []string{
		"\tsub sp, sp,",
		"\tstp x29, x30, [sp, #",
		"\tmovz x9, #10",
		"\tmovz x10, #32",
		"\tadd x9, x9, x10",
		"\tldr x1, [sp,",
		"\tbl _printf",
		"\tldp x29, x30, [sp, #",
		"\tadd sp, sp, #",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("assembly missing %q:\n%s", want, text)
		}
	}
}

func TestEmitObjectWritesMachOArithRelocations(t *testing.T) {
	t.Parallel()

	program, err := LowerMIR(&mir.Module{
		Functions: []*mir.Function{addLocalsMIR()},
	}, Target{Triple: "aarch64-apple-darwin", OS: "darwin", Arch: "aarch64", ObjectFormat: "mach-o"})
	if err != nil {
		t.Fatalf("LowerMIR() returned error: %v", err)
	}
	obj, err := EmitObject(program)
	if err != nil {
		t.Fatalf("EmitObject() returned error: %v", err)
	}
	f, err := macho.NewFile(bytes.NewReader(obj))
	if err != nil {
		t.Fatalf("macho.NewFile() returned error: %v", err)
	}
	if len(f.Sections) != 2 || f.Sections[0].Name != "__text" {
		t.Fatalf("Mach-O sections = %+v", f.Sections)
	}
	if f.Symtab == nil {
		t.Fatal("Mach-O symtab is nil")
	}
	var sawPrintf bool
	for _, sym := range f.Symtab.Syms {
		if sym.Name == "_printf" {
			sawPrintf = true
		}
	}
	if !sawPrintf {
		t.Fatalf("Mach-O symbols = %+v, want _printf for vararg println", f.Symtab.Syms)
	}
}
