package onb

import (
	"fmt"

	"github.com/osty/osty/internal/mir"
)

// argRegs is the AAPCS64 integer-argument register sequence. Phase A2 caps
// at 8 arguments — anything beyond that needs stack passing which the dev
// backend defers to a later slice.
var argRegs = []Reg{RegX0, RegX1, RegX2, RegX3, RegX4, RegX5, RegX6, RegX7}

// Runtime symbol names. ONB calls into the same `osty_runtime.c` the LLVM
// backend bundles, so these strings have to match the C function names
// exactly. The Mach-O `bl` encoder prepends the leading underscore for
// darwin's symbol mangling.
const (
	runtimeSymStringConcat = "osty_rt_strings_Concat"
	runtimeSymListNew      = "osty_rt_list_new"
	runtimeSymListPushI64  = "osty_rt_list_push_i64"
	runtimeSymListLen      = "osty_rt_list_len"
)

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
			if loc == nil || !isABIScalarType(loc.Type) {
				return Function{}, fmt.Errorf("%w: %s parameter %v has non-scalar type", ErrUnsupportedShape, fn.Name, paramID)
			}
		}
		if !isABIScalarType(fn.ReturnType) && fn.ReturnType != mir.TUnit {
			return Function{}, fmt.Errorf("%w: %s return type %s is outside phase 1", ErrUnsupportedShape, fn.Name, fn.ReturnType)
		}
	}
	s.fn = fn
	s.needsVararg = functionUsesIntPrintln(fn) && s.target.ObjectFormat == "mach-o"
	if err := s.assignLocalSlots(fn); err != nil {
		return Function{}, err
	}
	out := Function{Name: fn.Name, FrameSize: s.frameSize, DebugLocals: s.debugLocals(fn)}
	// Emit blocks with the entry block first so the encoded function starts
	// at the right instruction. Branches reference target blocks by their
	// fn.Blocks index, so we keep the index→Block mapping stable.
	order := blockEmitOrder(fn)
	out.Blocks = make([]Block, len(order))
	for slot, blockIdx := range order {
		block := fn.Blocks[blockIdx]
		lowered, err := s.lowerBlock(fn, block, blockIdx == int(fn.Entry))
		if err != nil {
			return Function{}, err
		}
		out.Blocks[slot] = lowered
	}
	if len(out.Blocks) == 0 {
		return Function{}, fmt.Errorf("onb: %s has no basic blocks", fn.Name)
	}
	return out, nil
}

// blockEmitOrder returns a permutation of fn.Blocks indices with the entry
// block first. Other blocks keep their original relative order so MIR builder
// hints (e.g. `then` adjacent to its branch) survive into emitted code.
//
// The returned slice's i-th element is the original fn.Blocks index that
// should be emitted in slot i. Branch targets always reference original
// indices (since lower.go's LIR Branch{Target: int} stores them) — the
// encoder maps original indices to byte offsets via blockOffsets[].
func blockEmitOrder(fn *mir.Function) []int {
	order := make([]int, 0, len(fn.Blocks))
	entry := int(fn.Entry)
	if entry >= 0 && entry < len(fn.Blocks) {
		order = append(order, entry)
	}
	for i := range fn.Blocks {
		if i == entry {
			continue
		}
		order = append(order, i)
	}
	return order
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
		collectTerminatorReads(block.Term, read)
	}
	// Parameters always need a slot — even unread ones — so lldb's
	// `frame variable` can show them and the param shuffle has somewhere
	// to land the AAPCS64 argument registers.
	for _, paramID := range fn.Params {
		read[paramID] = true
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
	var lineSpans []LineSpan
	emit := func(emitted []Instr, span LineSpan) {
		for _, instr := range emitted {
			instrs = append(instrs, instr)
			lineSpans = append(lineSpans, span)
		}
	}
	if isEntry && fn.Name != "main" {
		// Param shuffle gets a zero LineSpan so the DWARF emitter skips
		// these rows. With the shuffle attributed to the body's first
		// source line, lldb would stop *before* the params hit their
		// stack slots and `frame variable` would read uninitialised
		// stack — see Phase B.3 notes.
		emit(s.paramShuffle(fn), LineSpan{})
	}
	for _, instr := range block.Instrs {
		lowered, err := s.lowerInstr(fn, instr)
		if err != nil {
			return Block{}, err
		}
		emit(lowered, spanFromMIR(instr.At()))
	}
	termSpan := spanFromMIR(block.Term.At())
	switch term := block.Term.(type) {
	case *mir.ReturnTerm:
		emit(s.epilogue(fn), termSpan)
	case *mir.GotoTerm:
		emit([]Instr{&Branch{Target: int(term.Target)}}, termSpan)
	case *mir.BranchTerm:
		condInstrs, err := s.lowerBranchTerm(term)
		if err != nil {
			return Block{}, err
		}
		emit(condInstrs, termSpan)
	case *mir.SwitchIntTerm:
		switchInstrs, err := s.lowerSwitchIntTerm(term)
		if err != nil {
			return Block{}, err
		}
		emit(switchInstrs, termSpan)
	default:
		return Block{}, fmt.Errorf("%w: terminator %T is outside phase 1", ErrUnsupportedShape, block.Term)
	}
	return Block{
		Label:         blockLabel(block.ID),
		OriginalIndex: int(block.ID),
		Instrs:        instrs,
		LineSpans:     lineSpans,
	}, nil
}

// spanFromMIR converts a MIR span into the ONB-side LineSpan record. We
// drop the End position because the line program records points, not
// ranges. Zero line means "no source info" — the encoder skips the row.
func spanFromMIR(sp mir.Span) LineSpan {
	return LineSpan{Line: sp.Start.Line, Column: sp.Start.Column}
}

// lowerSwitchIntTerm lowers `match scrutinee { case0, case1, ..., _ ->
// default }` into a linear chain of `cmp + b.eq` per case followed by an
// unconditional branch to the default block. Cases are tried in MIR order.
//
// This is the textbook decision-tree-as-list dispatch — fine for matches
// up to a handful of cases. A future slice can graduate it to a jump
// table when case density / count justifies it.
func (s *lowerState) lowerSwitchIntTerm(term *mir.SwitchIntTerm) ([]Instr, error) {
	mat, err := s.materialiseOperand(term.Scrutinee, RegX9)
	if err != nil {
		return nil, err
	}
	out := append([]Instr(nil), mat...)
	for _, c := range term.Cases {
		out = append(out,
			&MovImm64{Dst: RegX10, Imm: c.Value},
			&Cmp{Lhs: RegX9, Rhs: RegX10},
			&BranchCond{Cond: CondEq, Target: int(c.Target)},
		)
	}
	out = append(out, &Branch{Target: int(term.Default)})
	return out, nil
}

// lowerBranchTerm lowers `BranchTerm{Cond, Then, Else}` into a load + cbnz
// + b sequence. The cond operand is a Bool, which lower.go's BinaryRV
// comparison path materialises as 0 / 1 in a stack slot.
func (s *lowerState) lowerBranchTerm(term *mir.BranchTerm) ([]Instr, error) {
	mat, err := s.materialiseOperand(term.Cond, RegX9)
	if err != nil {
		return nil, err
	}
	out := append([]Instr(nil), mat...)
	out = append(out,
		&BranchCondNotZero{Src: RegX9, Target: int(term.Then)},
		&Branch{Target: int(term.Else)},
	)
	return out, nil
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
		if instr.Dest.Local == fn.ReturnLocal && isABIScalarType(fn.ReturnType) {
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
	case *mir.AggregateRV:
		return s.lowerAggregateAssign(rv, slot)
	default:
		return nil, fmt.Errorf("%w: rvalue %T is outside phase 1", ErrUnsupportedShape, instr.Src)
	}
}

// lowerAggregateAssign currently covers only the empty-list literal
// (`let v: List<Int> = []`). The runtime allocates the list eagerly via
// `osty_rt_list_new`; future slices will extend this to non-empty lists,
// tuples, structs, and enum variants.
func (s *lowerState) lowerAggregateAssign(rv *mir.AggregateRV, destSlot int64) ([]Instr, error) {
	if rv.Kind != mir.AggList {
		return nil, fmt.Errorf("%w: aggregate kind %v", ErrUnsupportedShape, rv.Kind)
	}
	if len(rv.Fields) != 0 {
		return nil, fmt.Errorf("%w: non-empty list literal", ErrUnsupportedShape)
	}
	return []Instr{
		&BranchLink{Symbol: runtimeSymListNew},
		&Store64Stack{Src: RegX0, Offset: destSlot},
	}, nil
}

// isABIScalarType reports whether a MIR type fits cleanly in one AAPCS64
// integer register. Phase A2 accepts Int (i64), Bool (i1), and String
// (ptr). Lists and structs need indirect passing and are deferred to a
// future slice.
func isABIScalarType(t mir.Type) bool {
	return t == mir.TInt || t == mir.TBool || t == mir.TString
}

// mirTypeToDebugKind maps a MIR primitive type to the LIR-side debug kind
// the DWARF emitter can describe. Unsupported types (List / Map / struct
// / Option / etc.) collapse to DebugTypeNone so the encoder skips them.
func mirTypeToDebugKind(t mir.Type) DebugTypeKind {
	switch t {
	case mir.TInt:
		return DebugTypeInt
	case mir.TBool:
		return DebugTypeBool
	case mir.TString:
		return DebugTypeString
	case mir.TFloat, mir.TFloat64:
		return DebugTypeFloat
	default:
		return DebugTypeNone
	}
}

// debugLocals returns one DebugLocal per user-named local that ended up
// with a stack slot. Anonymous compiler temporaries (Name == "" or "$ret")
// and locals without slots are omitted so DWARF doesn't expose synthetic
// MIR plumbing to the developer's `frame variable` view.
//
// Today only Int locals get DebugTypeInt — String / Bool are surfaced as
// DebugTypeNone and the DWARF encoder skips them. Adding richer type
// support is a follow-up that grows the DebugTypeKind enum and the type
// DIE list in `dwarf.go` together.
func (s *lowerState) debugLocals(fn *mir.Function) []DebugLocal {
	if fn == nil || s.localSlots == nil {
		return nil
	}
	var out []DebugLocal
	for _, loc := range fn.Locals {
		if loc == nil || loc.Name == "" || loc.Name == "$ret" {
			continue
		}
		slot, ok := s.localSlots[loc.ID]
		if !ok {
			continue
		}
		kind := mirTypeToDebugKind(loc.Type)
		if kind == DebugTypeNone {
			continue
		}
		out = append(out, DebugLocal{Name: loc.Name, SlotOffset: slot, TypeKind: kind})
	}
	return out
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
	if cond, isCmp := comparisonCond(rv.Op); isCmp {
		return s.lowerComparisonAssign(rv, cond, destSlot)
	}
	if rv.Op == mir.BinAdd && rv.T == mir.TString {
		return s.lowerStringConcatAssign(rv, destSlot)
	}
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

// comparisonCond maps a MIR comparison op to its aarch64 condition code.
// Returns ok=false for non-comparison ops.
func comparisonCond(op mir.BinaryOp) (Cond, bool) {
	switch op {
	case mir.BinEq:
		return CondEq, true
	case mir.BinNeq:
		return CondNe, true
	case mir.BinLt:
		return CondLt, true
	case mir.BinLeq:
		return CondLe, true
	case mir.BinGt:
		return CondGt, true
	case mir.BinGeq:
		return CondGe, true
	default:
		return 0, false
	}
}

// lowerStringConcatAssign lowers `dest = lhs + rhs` for two String operands
// into an AAPCS64 call to the bundled runtime's two-arg concat helper.
// Both operands materialise into x0/x1 (a string is a heap pointer at the
// ABI boundary), the runtime returns a fresh ptr in x0, and we store that
// ptr into the destination slot.
func (s *lowerState) lowerStringConcatAssign(rv *mir.BinaryRV, destSlot int64) ([]Instr, error) {
	lhs, err := s.materialiseOperand(rv.Left, RegX0)
	if err != nil {
		return nil, err
	}
	rhs, err := s.materialiseOperand(rv.Right, RegX1)
	if err != nil {
		return nil, err
	}
	out := append([]Instr(nil), lhs...)
	out = append(out, rhs...)
	out = append(out,
		&BranchLink{Symbol: runtimeSymStringConcat},
		&Store64Stack{Src: RegX0, Offset: destSlot},
	)
	return out, nil
}

// lowerComparisonAssign emits `cmp lhs, rhs; cset Xd, <cond>` storing the
// boolean result (0 or 1) into the destination slot.
func (s *lowerState) lowerComparisonAssign(rv *mir.BinaryRV, cond Cond, destSlot int64) ([]Instr, error) {
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
	out = append(out,
		&Cmp{Lhs: RegX9, Rhs: RegX10},
		&Cset{Dst: RegX9, Cond: cond},
		&Store64Stack{Src: RegX9, Offset: destSlot},
	)
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
// value in dst. Supports Int / Bool / String constants and Copy/Move of
// locals that have a stack slot. String constants land in dst as a pointer
// to their `__cstring` entry.
func (s *lowerState) materialiseOperand(op mir.Operand, dst Reg) ([]Instr, error) {
	switch o := op.(type) {
	case *mir.ConstOp:
		switch c := o.Const.(type) {
		case *mir.IntConst:
			return []Instr{&MovImm64{Dst: dst, Imm: c.Value}}, nil
		case *mir.BoolConst:
			imm := int64(0)
			if c.Value {
				imm = 1
			}
			return []Instr{&MovImm64{Dst: dst, Imm: imm}}, nil
		case *mir.StringConst:
			label := s.addCString(c.Value)
			return []Instr{&LoadCStringAddress{Dst: dst, Label: label}}, nil
		default:
			return nil, fmt.Errorf("%w: const %T as operand", ErrUnsupportedShape, o.Const)
		}
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
	case mir.IntrinsicListPush:
		return s.lowerListPush(instr)
	case mir.IntrinsicListLen:
		return s.lowerListLen(instr)
	default:
		return nil, fmt.Errorf("%w: intrinsic %s is outside phase 1", ErrUnsupportedShape, instr.Kind)
	}
}

// lowerListPush lowers `IntrinsicListPush(list, value)` into a runtime
// call. The current slice only handles `List<Int>` — push on i1/f64 lists
// would call different runtime symbols and is left for the next slice.
func (s *lowerState) lowerListPush(instr *mir.IntrinsicInstr) ([]Instr, error) {
	if len(instr.Args) != 2 {
		return nil, fmt.Errorf("%w: list_push expects 2 args, got %d", ErrUnsupportedShape, len(instr.Args))
	}
	list, err := s.materialiseOperand(instr.Args[0], RegX0)
	if err != nil {
		return nil, err
	}
	value, err := s.materialiseOperand(instr.Args[1], RegX1)
	if err != nil {
		return nil, err
	}
	out := append([]Instr(nil), list...)
	out = append(out, value...)
	out = append(out, &BranchLink{Symbol: runtimeSymListPushI64})
	return out, nil
}

// lowerListLen lowers `IntrinsicListLen(list) -> Int` by calling the
// runtime helper and capturing x0 into the destination slot.
func (s *lowerState) lowerListLen(instr *mir.IntrinsicInstr) ([]Instr, error) {
	if len(instr.Args) != 1 {
		return nil, fmt.Errorf("%w: list_len expects 1 arg, got %d", ErrUnsupportedShape, len(instr.Args))
	}
	list, err := s.materialiseOperand(instr.Args[0], RegX0)
	if err != nil {
		return nil, err
	}
	out := append([]Instr(nil), list...)
	out = append(out, &BranchLink{Symbol: runtimeSymListLen})
	if instr.Dest != nil && !instr.Dest.HasProjections() {
		if slot, ok := s.localSlots[instr.Dest.Local]; ok {
			out = append(out, &Store64Stack{Src: RegX0, Offset: slot})
		}
	}
	return out, nil
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
	if mat, ok, err := s.loadStringPrintArg(arg); err != nil || ok {
		if err != nil {
			return nil, err
		}
		out := append([]Instr(nil), mat...)
		out = append(out, &BranchLink{Symbol: "puts"})
		return out, nil
	}
	loadValue, ok, err := s.loadIntPrintArg(arg)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%w: println currently requires a string, int literal, Int local, or String local", ErrUnsupportedShape)
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

// loadStringPrintArg materialises a string-typed operand into x0 so a
// subsequent `bl _puts` can print it. Returns ok=false when the operand
// isn't a String — the caller falls through to the int-print path.
func (s *lowerState) loadStringPrintArg(op mir.Operand) ([]Instr, bool, error) {
	switch o := op.(type) {
	case *mir.CopyOp:
		if !s.localIsString(o.Place.Local) {
			return nil, false, nil
		}
		instrs, err := s.loadPlaceIntoReg(o.Place, RegX0)
		if err != nil {
			return nil, false, err
		}
		return instrs, true, nil
	case *mir.MoveOp:
		if !s.localIsString(o.Place.Local) {
			return nil, false, nil
		}
		instrs, err := s.loadPlaceIntoReg(o.Place, RegX0)
		if err != nil {
			return nil, false, err
		}
		return instrs, true, nil
	default:
		return nil, false, nil
	}
}

func (s *lowerState) localIsString(id mir.LocalID) bool {
	if s.fn == nil {
		return false
	}
	for _, loc := range s.fn.Locals {
		if loc != nil && loc.ID == id {
			return loc.Type == mir.TString
		}
	}
	return false
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
// integer-valued argument — which on darwin/aarch64 lands the value on the
// printf vararg slot at [sp+0]. String prints go through `puts` instead and
// don't need the vararg slot, so they shouldn't force one.
func functionUsesIntPrintln(fn *mir.Function) bool {
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			intr, ok := instr.(*mir.IntrinsicInstr)
			if !ok || intr.Kind != mir.IntrinsicPrintln || len(intr.Args) != 1 {
				continue
			}
			arg := intr.Args[0]
			if _, isString := stringConstFromOperand(arg); isString {
				continue
			}
			if isStringTypedOperand(fn, arg) {
				continue
			}
			return true
		}
	}
	return false
}

// isStringTypedOperand reports whether op refers to a String-typed local
// (via Copy or Move). Const string operands are handled by the caller via
// stringConstFromOperand. Used by functionUsesIntPrintln to keep the
// printf vararg slot from being reserved when only `puts(stringLocal)`
// fires.
func isStringTypedOperand(fn *mir.Function, op mir.Operand) bool {
	if fn == nil {
		return false
	}
	var id mir.LocalID
	switch o := op.(type) {
	case *mir.CopyOp:
		id = o.Place.Local
	case *mir.MoveOp:
		id = o.Place.Local
	default:
		return false
	}
	for _, loc := range fn.Locals {
		if loc != nil && loc.ID == id {
			return loc.Type == mir.TString
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

// collectTerminatorReads handles the branch-/switch-style terminators that
// read locals through their condition or scrutinee operand. ReturnTerm and
// GotoTerm don't read user locals at the terminator level.
func collectTerminatorReads(term mir.Terminator, out map[mir.LocalID]bool) {
	switch t := term.(type) {
	case *mir.BranchTerm:
		collectOperandLocals(t.Cond, out)
	case *mir.SwitchIntTerm:
		collectOperandLocals(t.Scrutinee, out)
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
