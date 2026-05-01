package onb

import (
	"fmt"

	"github.com/osty/osty/internal/mir"
)

// LowerMIR lowers the supported MIR slice into ONB's own LIR. Phase 1.0
// handled hello world (`fn main() {}` plus `println(literal)`); Phase A2
// extends coverage to local Int variables and the three binary ops the MIR
// emits without further folding (`+`, `-`, `*`). The lowerer uses a
// stack-everything model — every local that is read gets a fixed stack slot
// and every operand goes through scratch registers x9/x10. No register
// allocator yet; that is a Week 2 concern.
func LowerMIR(mod *mir.Module, target Target) (*Program, error) {
	if mod == nil {
		return nil, fmt.Errorf("onb: missing MIR module")
	}
	mainFn := mod.LookupFunction("main")
	if mainFn == nil {
		return nil, fmt.Errorf("%w: missing main function", ErrUnsupportedShape)
	}
	state := &lowerState{target: target}
	fn, err := state.lowerMainFunction(mainFn)
	if err != nil {
		return nil, err
	}
	return &Program{
		Target:    target,
		Functions: []Function{fn},
		CStrings:  state.cstrings,
	}, nil
}

type lowerState struct {
	target   Target
	cstrings []CStringLiteral

	// per-function state, reset by lowerMainFunction
	fn          *mir.Function
	localSlots  map[mir.LocalID]int64
	needsVararg bool
	frameSize   int64
}

func (s *lowerState) lowerMainFunction(fn *mir.Function) (Function, error) {
	if fn == nil {
		return Function{}, fmt.Errorf("onb: nil main function")
	}
	if len(fn.Params) != 0 {
		return Function{}, fmt.Errorf("%w: main parameters are outside phase 1", ErrUnsupportedShape)
	}
	if fn.ReturnType != mir.TUnit {
		return Function{}, fmt.Errorf("%w: main return type %s is outside phase 1", ErrUnsupportedShape, fn.ReturnType)
	}
	s.fn = fn
	s.needsVararg = functionUsesIntPrintln(fn) && s.target.ObjectFormat == "mach-o"
	if err := s.assignLocalSlots(fn); err != nil {
		return Function{}, err
	}
	out := Function{Name: fn.Name, FrameSize: s.frameSize}
	for _, block := range fn.Blocks {
		lowered, err := s.lowerBlock(fn, block)
		if err != nil {
			return Function{}, err
		}
		out.Blocks = append(out.Blocks, lowered)
	}
	if len(out.Blocks) == 0 {
		return Function{}, fmt.Errorf("onb: main has no basic blocks")
	}
	return out, nil
}

// assignLocalSlots gives every local that is *read* by the function body a
// fixed stack slot, then computes the total frame size. Locals that are only
// written (dead stores) get no slot and the lowerer skips their assignments.
func (s *lowerState) assignLocalSlots(fn *mir.Function) error {
	read := map[mir.LocalID]bool{}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			collectReadLocals(instr, read)
		}
	}
	delete(read, fn.ReturnLocal) // unit-return slot is elided separately
	s.localSlots = map[mir.LocalID]int64{}
	varargBase := int64(0)
	if s.needsVararg {
		varargBase = 16 // reserve [sp+0..15] for printf's vararg integer slot
	}
	// deterministic order: iterate locals in declaration order, slot only the
	// ones actually read. Skips Unit-typed locals because the lowerer never
	// emits any code that reads them.
	off := varargBase
	for _, loc := range fn.Locals {
		if !read[loc.ID] {
			continue
		}
		if loc.Type == mir.TUnit {
			continue
		}
		s.localSlots[loc.ID] = off
		off += 8
	}
	if off == varargBase && !s.needsVararg {
		s.frameSize = 0
		return nil
	}
	s.frameSize = roundUp16(off + 16) // +16 for FP/LR save area at the top
	return nil
}

func roundUp16(n int64) int64 {
	if r := n % 16; r != 0 {
		n += 16 - r
	}
	return n
}

func (s *lowerState) lowerBlock(fn *mir.Function, block *mir.BasicBlock) (Block, error) {
	if block == nil {
		return Block{}, fmt.Errorf("onb: nil basic block")
	}
	var instrs []Instr
	for _, instr := range block.Instrs {
		lowered, err := s.lowerInstr(fn, instr)
		if err != nil {
			return Block{}, err
		}
		instrs = append(instrs, lowered...)
	}
	switch block.Term.(type) {
	case *mir.ReturnTerm:
		instrs = append(instrs,
			&MovImm32{Dst: RegW0, Imm: 0},
			&Ret{},
		)
		return Block{
			Label:  blockLabel(block.ID),
			Instrs: instrs,
		}, nil
	default:
		return Block{}, fmt.Errorf("%w: terminator %T is outside phase 1", ErrUnsupportedShape, block.Term)
	}
}

func blockLabel(id mir.BlockID) string {
	return fmt.Sprintf("bb%d", int(id))
}

func (s *lowerState) lowerInstr(fn *mir.Function, instr mir.Instr) ([]Instr, error) {
	switch i := instr.(type) {
	case *mir.StorageLiveInstr, *mir.StorageDeadInstr:
		return nil, nil
	case *mir.AssignInstr:
		return s.lowerAssign(fn, i)
	case *mir.IntrinsicInstr:
		return s.lowerIntrinsic(i)
	}
	return nil, fmt.Errorf("%w: instruction %T is outside phase 1", ErrUnsupportedShape, instr)
}

func (s *lowerState) lowerAssign(fn *mir.Function, instr *mir.AssignInstr) ([]Instr, error) {
	if isUnitReturnAssignment(fn, instr) {
		return nil, nil
	}
	if instr.Dest.HasProjections() {
		return nil, fmt.Errorf("%w: place projection on assign dest", ErrUnsupportedShape)
	}
	slot, hasSlot := s.localSlots[instr.Dest.Local]
	if !hasSlot {
		// dead store — local was never read
		return nil, nil
	}
	switch rv := instr.Src.(type) {
	case *mir.UseRV:
		mat, err := s.materialiseOperand(rv.Op, RegX9)
		if err != nil {
			return nil, err
		}
		return append(mat, &Store64Stack{Src: RegX9, Offset: slot}), nil
	case *mir.BinaryRV:
		return s.lowerBinaryAssign(rv, slot)
	default:
		return nil, fmt.Errorf("%w: rvalue %T is outside phase 1", ErrUnsupportedShape, instr.Src)
	}
}

func (s *lowerState) lowerBinaryAssign(rv *mir.BinaryRV, destSlot int64) ([]Instr, error) {
	switch rv.Op {
	case mir.BinAdd, mir.BinSub, mir.BinMul:
		// supported
	default:
		return nil, fmt.Errorf("%w: binary op %v is outside phase 1", ErrUnsupportedShape, rv.Op)
	}
	lhs, err := s.materialiseOperand(rv.Left, RegX9)
	if err != nil {
		return nil, err
	}
	rhs, err := s.materialiseOperand(rv.Right, RegX10)
	if err != nil {
		return nil, err
	}
	out := append([]Instr{}, lhs...)
	out = append(out, rhs...)
	switch rv.Op {
	case mir.BinAdd:
		out = append(out, &AddReg{Dst: RegX9, Lhs: RegX9, Rhs: RegX10})
	case mir.BinSub:
		out = append(out, &SubReg{Dst: RegX9, Lhs: RegX9, Rhs: RegX10})
	case mir.BinMul:
		out = append(out, &MulReg{Dst: RegX9, Lhs: RegX9, Rhs: RegX10})
	}
	out = append(out, &Store64Stack{Src: RegX9, Offset: destSlot})
	return out, nil
}

// materialiseOperand emits the instruction sequence that lands the operand's
// value in dst. Supports Int constants and Copy/Move of locals that have a
// stack slot.
func (s *lowerState) materialiseOperand(op mir.Operand, dst Reg) ([]Instr, error) {
	switch o := op.(type) {
	case *mir.ConstOp:
		c, ok := o.Const.(*mir.IntConst)
		if !ok {
			return nil, fmt.Errorf("%w: const %T as operand", ErrUnsupportedShape, o.Const)
		}
		return []Instr{&MovImm64{Dst: dst, Imm: c.Value}}, nil
	case *mir.CopyOp:
		return s.loadPlaceIntoReg(o.Place, dst)
	case *mir.MoveOp:
		return s.loadPlaceIntoReg(o.Place, dst)
	default:
		return nil, fmt.Errorf("%w: operand %T", ErrUnsupportedShape, op)
	}
}

func (s *lowerState) loadPlaceIntoReg(place mir.Place, dst Reg) ([]Instr, error) {
	if place.HasProjections() {
		return nil, fmt.Errorf("%w: place projection in operand", ErrUnsupportedShape)
	}
	slot, ok := s.localSlots[place.Local]
	if !ok {
		return nil, fmt.Errorf("%w: read of local%d without slot", ErrUnsupportedShape, place.Local)
	}
	return []Instr{&Load64Stack{Dst: dst, Offset: slot}}, nil
}

func (s *lowerState) lowerIntrinsic(instr *mir.IntrinsicInstr) ([]Instr, error) {
	switch instr.Kind {
	case mir.IntrinsicPrintln:
		return s.lowerPrintln(instr)
	default:
		return nil, fmt.Errorf("%w: intrinsic %s is outside phase 1", ErrUnsupportedShape, instr.Kind)
	}
}

func (s *lowerState) lowerPrintln(instr *mir.IntrinsicInstr) ([]Instr, error) {
	if len(instr.Args) != 1 {
		return nil, fmt.Errorf("%w: println currently requires one argument", ErrUnsupportedShape)
	}
	arg := instr.Args[0]
	if text, ok := stringConstFromOperand(arg); ok {
		label := s.addCString(text)
		return []Instr{
			&LoadCStringAddress{Dst: RegX0, Label: label},
			&BranchLink{Symbol: "puts"},
		}, nil
	}
	// Int println — either a constant immediate or a local read. Both go
	// through the printf("%lld\n", value) path. The format string lives in
	// __cstring; the integer value flows through x1 and (on darwin only) the
	// vararg slot at [sp+0].
	loadValue, ok, err := s.loadIntPrintArg(arg)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%w: println currently requires a string, int literal, or Int local", ErrUnsupportedShape)
	}
	label := s.addCString("%lld\n")
	out := []Instr{&LoadCStringAddress{Dst: RegX0, Label: label}}
	out = append(out, loadValue...)
	if s.target.ObjectFormat == "mach-o" {
		out = append(out, &Store64Stack{Src: RegX1, Offset: 0})
	}
	out = append(out, &BranchLink{Symbol: "printf"})
	return out, nil
}

// loadIntPrintArg materialises the integer argument for printf into x1.
// Returns ok=false if the operand is not an int we can lower yet.
func (s *lowerState) loadIntPrintArg(op mir.Operand) ([]Instr, bool, error) {
	switch o := op.(type) {
	case *mir.ConstOp:
		c, ok := o.Const.(*mir.IntConst)
		if !ok {
			return nil, false, nil
		}
		return []Instr{&MovImm64{Dst: RegX1, Imm: c.Value}}, true, nil
	case *mir.CopyOp:
		instrs, err := s.loadPlaceIntoReg(o.Place, RegX1)
		if err != nil {
			return nil, false, err
		}
		return instrs, true, nil
	case *mir.MoveOp:
		instrs, err := s.loadPlaceIntoReg(o.Place, RegX1)
		if err != nil {
			return nil, false, err
		}
		return instrs, true, nil
	default:
		return nil, false, nil
	}
}

func (s *lowerState) addCString(value string) string {
	for _, existing := range s.cstrings {
		if existing.Value == value {
			return existing.Label
		}
	}
	label := fmt.Sprintf("str%d", len(s.cstrings))
	s.cstrings = append(s.cstrings, CStringLiteral{Label: label, Value: value})
	return label
}

func isUnitReturnAssignment(fn *mir.Function, instr *mir.AssignInstr) bool {
	if fn == nil || instr == nil || instr.Dest.HasProjections() || instr.Dest.Local != fn.ReturnLocal {
		return false
	}
	use, ok := instr.Src.(*mir.UseRV)
	if !ok {
		return false
	}
	c, ok := use.Op.(*mir.ConstOp)
	if !ok {
		return false
	}
	_, ok = c.Const.(*mir.UnitConst)
	return ok
}

// stringConstFromOperand returns the string-literal value when op is a
// constant string operand, otherwise ok=false.
func stringConstFromOperand(op mir.Operand) (string, bool) {
	c, ok := op.(*mir.ConstOp)
	if !ok {
		return "", false
	}
	s, ok := c.Const.(*mir.StringConst)
	if !ok {
		return "", false
	}
	return s.Value, true
}

// functionUsesIntPrintln reports whether the function calls println with an
// integer-valued argument. Used to decide whether the frame needs a vararg
// slot at [sp+0] (darwin/aarch64 ABI for variadic int passing).
func functionUsesIntPrintln(fn *mir.Function) bool {
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			intr, ok := instr.(*mir.IntrinsicInstr)
			if !ok || intr.Kind != mir.IntrinsicPrintln || len(intr.Args) != 1 {
				continue
			}
			if _, isString := stringConstFromOperand(intr.Args[0]); isString {
				continue
			}
			return true
		}
	}
	return false
}

// collectReadLocals walks every operand and collects locals that flow into a
// read context (Copy / Move or a Place inside an aggregate / projection).
func collectReadLocals(instr mir.Instr, out map[mir.LocalID]bool) {
	switch i := instr.(type) {
	case *mir.AssignInstr:
		collectRValueLocals(i.Src, out)
	case *mir.IntrinsicInstr:
		for _, arg := range i.Args {
			collectOperandLocals(arg, out)
		}
	}
}

func collectRValueLocals(rv mir.RValue, out map[mir.LocalID]bool) {
	switch r := rv.(type) {
	case *mir.UseRV:
		collectOperandLocals(r.Op, out)
	case *mir.BinaryRV:
		collectOperandLocals(r.Left, out)
		collectOperandLocals(r.Right, out)
	}
}

func collectOperandLocals(op mir.Operand, out map[mir.LocalID]bool) {
	switch o := op.(type) {
	case *mir.CopyOp:
		out[o.Place.Local] = true
	case *mir.MoveOp:
		out[o.Place.Local] = true
	}
}
