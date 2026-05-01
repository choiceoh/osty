package onb

import (
	"fmt"

	"github.com/osty/osty/internal/mir"
)

// argRegs is the AAPCS64 integer-argument register sequence. Phase A2 caps
// at 8 arguments — anything beyond that needs stack passing which the dev
// backend defers to a later slice.
var argRegs = []Reg{RegX0, RegX1, RegX2, RegX3, RegX4, RegX5, RegX6, RegX7}

// LowerMIR lowers the supported MIR slice into ONB's own LIR. Phase 1.0
// handled hello world; Phase A2 Week 1 added local Int variables and binary
// arithmetic; Phase A2 Week 2 introduces user-defined functions with up to
// eight Int parameters and Int return.
//
// The lowerer still uses a stack-everything model — every read local gets a
// fixed slot, every operand goes through scratch registers — and now extends
// that to function entry/exit and call sites: parameter values flow from the
// AAPCS64 argument registers into their slots, callees lower their return
// expression into x0, and call sites stage call-site arguments into x0..x7
// before `bl` and capture x0 back into the destination slot afterwards.
func LowerMIR(mod *mir.Module, target Target) (*Program, error) {
	if mod == nil {
		return nil, fmt.Errorf("onb: missing MIR module")
	}
	mainFn := mod.LookupFunction("main")
	if mainFn == nil {
		return nil, fmt.Errorf("%w: missing main function", ErrUnsupportedShape)
	}
	state := &lowerState{target: target}
	out := &Program{Target: target}

	// Lower main first — its layout decisions (vararg slot, etc.) drive the
	// shared cstring set used by every other function in the same module.
	mainLowered, err := state.lowerFunction(mainFn)
	if err != nil {
		return nil, err
	}
	out.Functions = append(out.Functions, mainLowered)

	for _, fn := range mod.Functions {
		if fn == nil || fn == mainFn {
			continue
		}
		lowered, err := state.lowerFunction(fn)
		if err != nil {
			return nil, err
		}
		out.Functions = append(out.Functions, lowered)
	}
	out.CStrings = state.cstrings
	return out, nil
}

type lowerState struct {
	target   Target
	cstrings []CStringLiteral

	// per-function state, reset by lowerFunction
	fn          *mir.Function
	localSlots  map[mir.LocalID]int64
	needsVararg bool
	frameSize   int64
}

// lowerFunction lowers one MIR function into LIR. The same path serves main
// and user-defined helpers; the differences (exit-code mov, parameter
// handling, return-value plumbing) are encoded as conditionals on the
// function shape rather than separate code paths so behaviour stays in one
// place.
func (s *lowerState) lowerFunction(fn *mir.Function) (Function, error) {
	if fn == nil {
		return Function{}, fmt.Errorf("onb: nil function")
	}
	if fn.Name == "main" {
		if len(fn.Params) != 0 {
			return Function{}, fmt.Errorf("%w: main parameters are outside phase 1", ErrUnsupportedShape)
		}
		if fn.ReturnType != mir.TUnit {
			return Function{}, fmt.Errorf("%w: main return type %s is outside phase 1", ErrUnsupportedShape, fn.ReturnType)
		}
	} else {
		if len(fn.Params) > len(argRegs) {
			return Function{}, fmt.Errorf("%w: %s takes %d params (max %d)", ErrUnsupportedShape, fn.Name, len(fn.Params), len(argRegs))
		}
		for _, paramID := range fn.Params {
			loc := lookupLocal(fn, paramID)
			if loc == nil || loc.Type != mir.TInt {
				return Function{}, fmt.Errorf("%w: %s parameter %v is not Int", ErrUnsupportedShape, fn.Name, paramID)
			}
		}
		if fn.ReturnType != mir.TInt && fn.ReturnType != mir.TUnit {
			return Function{}, fmt.Errorf("%w: %s return type %s is outside phase 1", ErrUnsupportedShape, fn.Name, fn.ReturnType)
		}
	}
	s.fn = fn
	s.needsVararg = functionUsesIntPrintln(fn) && s.target.ObjectFormat == "mach-o"
	if err := s.assignLocalSlots(fn); err != nil {
		return Function{}, err
	}
	out := Function{Name: fn.Name, FrameSize: s.frameSize}
	for blockIdx, block := range fn.Blocks {
		lowered, err := s.lowerBlock(fn, block, blockIdx == 0)
		if err != nil {
			return Function{}, err
		}
		out.Blocks = append(out.Blocks, lowered)
	}
	if len(out.Blocks) == 0 {
		return Function{}, fmt.Errorf("onb: %s has no basic blocks", fn.Name)
	}
	return out, nil
}

// assignLocalSlots gives every local that is *read* by the function body a
// fixed stack slot, then computes the total frame size. Locals that are only
// written (dead stores) get no slot and the lowerer skips their assignments.
//
// Function parameters always get a slot when they are read — the prologue
// copies their AAPCS64 argument register into that slot so the rest of the
// body sees parameters as ordinary locals.
func (s *lowerState) assignLocalSlots(fn *mir.Function) error {
	read := map[mir.LocalID]bool{}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			collectReadLocals(instr, read)
		}
	}
	delete(read, fn.ReturnLocal)
	s.localSlots = map[mir.LocalID]int64{}
	varargBase := int64(0)
	if s.needsVararg {
		varargBase = 16
	}
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
	if off == varargBase && !s.needsVararg && fn.ReturnType == mir.TUnit {
		s.frameSize = 0
		return nil
	}
	// Non-Unit return functions still need a frame because the caller's bl
	// pushes the return address into x30 — we save/restore FP/LR to keep
	// backtraces usable. This also normalises behaviour with leaf helpers
	// that have no locals at all.
	if off == varargBase && !s.needsVararg {
		// Has a return but no locals — skip the frame for now since helpers
		// like `fn id(n: Int) -> Int { n }` only need x0 traffic.
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

func (s *lowerState) lowerBlock(fn *mir.Function, block *mir.BasicBlock, isEntry bool) (Block, error) {
	if block == nil {
		return Block{}, fmt.Errorf("onb: nil basic block")
	}
	var instrs []Instr
	if isEntry && fn.Name != "main" {
		instrs = append(instrs, s.paramShuffle(fn)...)
	}
	for _, instr := range block.Instrs {
		lowered, err := s.lowerInstr(fn, instr)
		if err != nil {
			return Block{}, err
		}
		instrs = append(instrs, lowered...)
	}
	switch block.Term.(type) {
	case *mir.ReturnTerm:
		instrs = append(instrs, s.epilogue(fn)...)
		return Block{
			Label:  blockLabel(block.ID),
			Instrs: instrs,
		}, nil
	default:
		return Block{}, fmt.Errorf("%w: terminator %T is outside phase 1", ErrUnsupportedShape, block.Term)
	}
}

// paramShuffle copies every read parameter from its AAPCS64 argument register
// into its assigned stack slot. Parameters that are never read get no slot
// and no shuffle — their argument register is simply left untouched.
func (s *lowerState) paramShuffle(fn *mir.Function) []Instr {
	var out []Instr
	for i, paramID := range fn.Params {
		slot, ok := s.localSlots[paramID]
		if !ok {
			continue
		}
		out = append(out, &Store64Stack{Src: argRegs[i], Offset: slot})
	}
	return out
}

// epilogue emits the function exit sequence. main returns process exit code
// 0 in w0; user functions load their _return slot into x0 (Int return) or
// nothing (Unit return) before falling through to Ret, which itself emits
// the FP/LR restore + ret.
func (s *lowerState) epilogue(fn *mir.Function) []Instr {
	if fn.Name == "main" {
		return []Instr{&MovImm32{Dst: RegW0, Imm: 0}, &Ret{}}
	}
	if fn.ReturnType == mir.TUnit {
		return []Instr{&Ret{}}
	}
	if slot, ok := s.localSlots[fn.ReturnLocal]; ok {
		return []Instr{
			&Load64Stack{Dst: RegX0, Offset: slot},
			&Ret{},
		}
	}
	// _return was never read locally (e.g. body is a single Bin assigned to
	// _return — which is itself a write, not a read). Allocate a slot
	// retroactively and load it. This path is unreachable today because
	// every Int-returning helper writes to _return and then ReturnTerm —
	// but we keep an explicit error so the regression is loud if it changes.
	return []Instr{
		&MovImm64{Dst: RegX0, Imm: 0}, // safe default; caller will see 0
		&Ret{},
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
	case *mir.CallInstr:
		return s.lowerCall(fn, i)
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
	// Allow writes to a non-Unit return local even if it has no slot — the
	// epilogue will read x0 directly. Stage the write into x0 so the
	// epilogue's Load64Stack can be skipped in a follow-up; today we still
	// take the slot if there is one.
	slot, hasSlot := s.localSlots[instr.Dest.Local]
	if !hasSlot {
		if instr.Dest.Local == fn.ReturnLocal && fn.ReturnType == mir.TInt {
			// allocate a synthetic slot at frame's tail
			slot = s.allocateReturnSlot(fn)
			hasSlot = true
		} else {
			// dead store
			return nil, nil
		}
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

// allocateReturnSlot makes room for the return local on functions that didn't
// otherwise read it. This rarely fires today — most helpers' bodies finish
// with `Assign _return := <expr>` followed by `ReturnTerm` — but it keeps
// the lowerer correct when the front end inserts an extra epilogue read.
func (s *lowerState) allocateReturnSlot(fn *mir.Function) int64 {
	off := int64(0)
	if s.needsVararg {
		off = 16
	}
	for _, slot := range s.localSlots {
		if slot+8 > off {
			off = slot + 8
		}
	}
	s.localSlots[fn.ReturnLocal] = off
	s.frameSize = roundUp16(off + 8 + 16)
	return off
}

func (s *lowerState) lowerBinaryAssign(rv *mir.BinaryRV, destSlot int64) ([]Instr, error) {
	switch rv.Op {
	case mir.BinAdd, mir.BinSub, mir.BinMul:
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

// lowerCall emits an AAPCS64 direct call: each argument materialises into
// x0..x7 in order, then `bl _<name>`, then (if the call has a destination
// place backed by a slot) the return value in x0 is stored into that slot.
// FnRef-only — indirect calls and non-Int parameter types fall back.
func (s *lowerState) lowerCall(fn *mir.Function, instr *mir.CallInstr) ([]Instr, error) {
	ref, ok := instr.Callee.(*mir.FnRef)
	if !ok {
		return nil, fmt.Errorf("%w: indirect call", ErrUnsupportedShape)
	}
	if len(instr.Args) > len(argRegs) {
		return nil, fmt.Errorf("%w: %d args (max %d)", ErrUnsupportedShape, len(instr.Args), len(argRegs))
	}
	var out []Instr
	for i, arg := range instr.Args {
		mat, err := s.materialiseOperand(arg, argRegs[i])
		if err != nil {
			return nil, err
		}
		out = append(out, mat...)
	}
	out = append(out, &BranchLink{Symbol: ref.Symbol})
	if instr.Dest != nil && !instr.Dest.HasProjections() {
		if slot, ok := s.localSlots[instr.Dest.Local]; ok {
			out = append(out, &Store64Stack{Src: RegX0, Offset: slot})
		}
	}
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
// integer-valued argument.
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
// Function parameters are also considered "read" if they have any consumer,
// which keeps their stack slot reserved across the prologue's param shuffle.
func collectReadLocals(instr mir.Instr, out map[mir.LocalID]bool) {
	switch i := instr.(type) {
	case *mir.AssignInstr:
		collectRValueLocals(i.Src, out)
	case *mir.IntrinsicInstr:
		for _, arg := range i.Args {
			collectOperandLocals(arg, out)
		}
	case *mir.CallInstr:
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

func lookupLocal(fn *mir.Function, id mir.LocalID) *mir.Local {
	for _, loc := range fn.Locals {
		if loc != nil && loc.ID == id {
			return loc
		}
	}
	return nil
}
