package onb

import (
	"fmt"
	"math"
	"strings"

	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/mir"
)

// argRegs is the AAPCS64 integer-argument register sequence. Phase A2 caps
// at 8 register slots — anything beyond that needs stack passing which the
// dev backend defers to a later slice. Small structs (≤16B) consume two
// adjacent register slots (e.g. {x0, x1}), so a function that takes a
// `Point` plus a scalar uses three slots, not two.
var argRegs = []Reg{RegX0, RegX1, RegX2, RegX3, RegX4, RegX5, RegX6, RegX7}

// fpArgRegs is the AAPCS64 floating-point-argument register sequence.
// AAPCS64 keeps two independent register cursors (integer + FP), so a
// `fn mix(a: Int, x: Float64, b: Int)` takes a in x0, x in d0, b in
// x1 — not the often-mistaken x0 / x1 / x2 mapping. The dev backend
// follows that rule for both `paramShuffle` and `lowerCall`.
var fpArgRegs = []Reg{RegD0, RegD1, RegD2, RegD3, RegD4, RegD5, RegD6, RegD7}

// abiSmallStructRegLimit is the AAPCS64 cutoff for "small struct" handling:
// up to two integer registers. Sizes ≤ 8 bytes consume one register, sizes
// 9–16 bytes consume two adjacent registers, anything bigger goes through
// indirect (sret-style) passing.
const abiSmallStructRegLimit = 2

// abiIndirectStructFieldLimit caps the size of structs the dev backend
// will pass / return by reference. The MVP supports 3- and 4-field
// all-scalar structs (24B / 32B); broader sizes need a memcpy-class
// approach the slice doesn't ship. Larger structs fall back to the
// LLVM backend.
const abiIndirectStructFieldLimit = 4

// regX8 is the AAPCS64 indirect-result register. The caller stores
// the address of the caller-allocated return buffer here before
// branching to the callee, and the callee writes the return value
// through it. ONB's epilogue copies the local `$ret` slot's bytes
// into [x8 + offset] for sret-returning functions.
const regX8 Reg = "x8"

// Runtime symbol names. ONB calls into the same `osty_runtime.c` the LLVM
// backend bundles, so these strings have to match the C function names
// exactly. The Mach-O `bl` encoder prepends the leading underscore for
// darwin's symbol mangling.
const (
	runtimeSymStringConcat       = "osty_rt_strings_Concat"
	runtimeSymListNew            = "osty_rt_list_new"
	runtimeSymListPushI64        = "osty_rt_list_push_i64"
	runtimeSymListPushI1         = "osty_rt_list_push_i1"
	runtimeSymListPushF64        = "osty_rt_list_push_f64"
	runtimeSymListPushString     = "osty_rt_list_push_string"
	runtimeSymListLen            = "osty_rt_list_len"
	runtimeSymListGetI64         = "osty_rt_list_get_i64"
	runtimeSymListGetI1          = "osty_rt_list_get_i1"
	runtimeSymListGetF64         = "osty_rt_list_get_f64"
	runtimeSymListGetString      = "osty_rt_list_get_string"
	// closure_env_alloc_v2 is exported by the runtime under the dotted
	// (LLVM-shaped) name via __asm__("osty.rt..."). Mach-O symbol
	// encoding prepends the leading underscore, matching the call site.
	runtimeSymClosureEnvAllocV2 = "osty.rt.closure_env_alloc_v2"
	runtimeSymStringByteLen     = "osty_rt_strings_ByteLen"
)

// closureEnvCapturesOffset is the byte offset within an
// `osty_rt_closure_env` where the captures array begins. The runtime
// header is `{ ptr fn (8B); i64 capture_count (8B); u64
// pointer_bitmap (8B); ptr captures[] }`. Kept in sync with the LLVM
// backend's matching constant by sharing the layout in C.
const closureEnvCapturesOffset = 24

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
	state := &lowerState{target: target, mod: mod}
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
	mod      *mir.Module // module-scope layouts for struct size lookup

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
		// Walk the parameter list with a register-cursor: each scalar
		// param consumes one register, each small struct consumes one
		// or two. Anything beyond x7 fails the guard until stack-arg
		// passing lands in a future slice. abiRegSlots reads the
		// module's StructLayout via s.mod, so the guard works even
		// before s.fn is set below.
		regCursor := 0
		for _, paramID := range fn.Params {
			loc := lookupLocal(fn, paramID)
			if loc == nil {
				return Function{}, fmt.Errorf("%w: %s parameter %v missing local entry", ErrUnsupportedShape, fn.Name, paramID)
			}
			slots, ok := s.abiRegSlots(loc.Type)
			if !ok || slots == 0 {
				return Function{}, fmt.Errorf("%w: %s parameter %s has type %s outside ABI", ErrUnsupportedShape, fn.Name, loc.Name, loc.Type)
			}
			if regCursor+slots > len(argRegs) {
				return Function{}, fmt.Errorf("%w: %s parameter %s would overflow argument registers", ErrUnsupportedShape, fn.Name, loc.Name)
			}
			regCursor += slots
		}
		if fn.ReturnType != mir.TUnit && !s.isABIPassableType(fn.ReturnType) {
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
	// Scalar / Unit returns travel through x0 — no stack slot needed
	// for `$ret` in the common case, and skipping it keeps leaf helpers
	// frame-less. Small struct returns are different: even though no
	// instruction *reads* $ret (it's purely a write target), the
	// AggregateRV writer needs a slot to stamp each field into, and
	// the epilogue needs that same slot to load slot+0 → x0 and
	// slot+8 → x1. Force the slot in by inserting $ret into `read`.
	if fn.ReturnType == mir.TUnit || isABIScalarType(fn.ReturnType) {
		delete(read, fn.ReturnLocal)
	} else {
		read[fn.ReturnLocal] = true
	}
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
		size := s.localTypeSize(loc.Type)
		if size == 0 {
			continue
		}
		s.localSlots[loc.ID] = off
		off += size
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
	case *mir.UnreachableTerm:
		// `match` exhaustiveness adds an unreachable trap as the
		// switch default. Reaching this PC is a compiler bug or UB,
		// so the safest lowering is `brk #1` (aarch64 software
		// breakpoint) — the process aborts immediately rather than
		// fall through into the next function.
		emit([]Instr{&Brk{Imm: 1}}, termSpan)
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

// paramShuffle copies every read parameter from its AAPCS64 argument
// register(s) into its assigned stack slot. Scalar Int / Bool / String
// / pointer params consume one integer register; small structs and
// enums consume one or two adjacent integer registers, with register N
// landing at slot+N*8 to match the field-projection offsets the rest
// of the body expects. Float params consume one FP register (d0..d7)
// from the independent FP cursor — AAPCS64 doesn't merge integer and
// FP register usage. Indirect-passed structs (>16B all-scalar) take
// one integer register — a *pointer* to a caller-allocated buffer —
// and the prologue copies the struct's bytes through x9 into the
// param's local slot so the rest of the body sees the value as if
// it were locally constructed. Parameters that are never read still
// advance the cursor so subsequent params see the right register.
func (s *lowerState) paramShuffle(fn *mir.Function) []Instr {
	var out []Instr
	regCursor := 0
	fpCursor := 0
	for _, paramID := range fn.Params {
		loc := lookupLocal(fn, paramID)
		if loc == nil {
			continue
		}
		slot, hasSlot := s.localSlots[paramID]
		if isFloatABIType(loc.Type) {
			if hasSlot && fpCursor < len(fpArgRegs) {
				out = append(out, &StoreFloat64Stack{Src: fpArgRegs[fpCursor], Offset: slot})
			}
			fpCursor++
			continue
		}
		if s.abiUsesIndirectStruct(loc.Type) {
			if hasSlot && regCursor < len(argRegs) {
				size := s.indirectStructByteSize(loc.Type)
				ptrReg := argRegs[regCursor]
				for off := int64(0); off < size; off += 8 {
					out = append(out,
						&LoadFromReg{Dst: RegX9, Src: ptrReg, Offset: off},
						&Store64Stack{Src: RegX9, Offset: slot + off},
					)
				}
			}
			regCursor++
			continue
		}
		slots, ok := s.abiRegSlots(loc.Type)
		if !ok {
			continue
		}
		for i := 0; i < slots; i++ {
			if hasSlot {
				out = append(out, &Store64Stack{Src: argRegs[regCursor+i], Offset: slot + int64(i)*8})
			}
		}
		regCursor += slots
	}
	return out
}

// epilogue emits the function exit sequence. main returns process exit
// code 0 in w0; user functions load their _return slot into x0 (and x1
// for small struct returns) or nothing (Unit return) before falling
// through to Ret, which itself emits the FP/LR restore + ret. Small
// structs (≤ 16B) follow AAPCS64's 2-register return convention: slot+0
// → x0, slot+8 → x1.
func (s *lowerState) epilogue(fn *mir.Function) []Instr {
	if fn.Name == "main" {
		return []Instr{&MovImm32{Dst: RegW0, Imm: 0}, &Ret{}}
	}
	if fn.ReturnType == mir.TUnit {
		return []Instr{&Ret{}}
	}
	slot, ok := s.localSlots[fn.ReturnLocal]
	if !ok {
		// _return was never read locally. This path is rarely exercised
		// today — the front end usually emits Assign _return := <expr>
		// followed by ReturnTerm, which marks the local as both written
		// and read — but we keep a safe default so the regression is
		// loud if it changes.
		return []Instr{
			&MovImm64{Dst: RegX0, Imm: 0},
			&Ret{},
		}
	}
	if isFloatABIType(fn.ReturnType) {
		return []Instr{
			&LoadFloat64Stack{Dst: RegD0, Offset: slot},
			&Ret{},
		}
	}
	if s.abiUsesIndirectStruct(fn.ReturnType) {
		// Sret return: the body wrote $ret into its local slot;
		// copy each 8-byte half through the caller's buffer
		// pointer in x8. The MVP assumes x8 wasn't clobbered by
		// the body — make()-style constructors with no
		// intermediate calls satisfy that. A follow-up that adds
		// save/restore at the prologue will lift the restriction.
		size := s.indirectStructByteSize(fn.ReturnType)
		out := make([]Instr, 0, int(size/8)*2+1)
		for off := int64(0); off < size; off += 8 {
			out = append(out,
				&Load64Stack{Dst: RegX9, Offset: slot + off},
				&StoreToReg{Src: RegX9, Base: regX8, Offset: off},
			)
		}
		out = append(out, &Ret{})
		return out
	}
	slots, _ := s.abiRegSlots(fn.ReturnType)
	if slots == 0 {
		slots = 1
	}
	retRegs := []Reg{RegX0, RegX1}
	out := make([]Instr, 0, slots+1)
	for i := 0; i < slots && i < len(retRegs); i++ {
		out = append(out, &Load64Stack{Dst: retRegs[i], Offset: slot + int64(i)*8})
	}
	out = append(out, &Ret{})
	return out
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
		return s.lowerAssignToProjection(instr)
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
		// Whole-enum / whole-struct copy through `use _local`: the
		// scrutinee in `match m { … }` lowers to `_scrut = use m`,
		// and that local is later read by `discriminant _scrut` and
		// `_scrut@Some` projections. A 1-register copy would clip
		// the payload byte for an Option<scalar>; route multi-slot
		// reads through a per-half copy so every byte ends up in
		// the destination slot.
		return s.lowerUseAssign(fn, rv, instr.Dest.Local, slot)
	case *mir.BinaryRV:
		return s.lowerBinaryAssign(rv, slot)
	case *mir.AggregateRV:
		return s.lowerAggregateAssign(rv, slot)
	case *mir.NullaryRV:
		return s.lowerNullaryAssign(rv, slot)
	case *mir.DiscriminantRV:
		return s.lowerDiscriminantAssign(rv, slot)
	case *mir.LenRV:
		return s.lowerLenAssign(rv, slot)
	default:
		return nil, fmt.Errorf("%w: rvalue %T is outside phase 1", ErrUnsupportedShape, instr.Src)
	}
}

// lowerLenAssign lowers `dest = len <place>` for List operands. The
// runtime call returns the length in x0; we capture it into the
// destination slot. Used by the `for x in list` lowering, which
// stages `_len = len _iter` as the upper bound of an indexed loop.
func (s *lowerState) lowerLenAssign(rv *mir.LenRV, destSlot int64) ([]Instr, error) {
	if rv.Place.HasProjections() {
		return nil, fmt.Errorf("%w: len of projected place", ErrUnsupportedShape)
	}
	srcSlot, ok := s.localSlots[rv.Place.Local]
	if !ok {
		return nil, fmt.Errorf("%w: len of local%d without slot", ErrUnsupportedShape, rv.Place.Local)
	}
	return []Instr{
		&Load64Stack{Dst: RegX0, Offset: srcSlot},
		&BranchLink{Symbol: runtimeSymListLen},
		&Store64Stack{Src: RegX0, Offset: destSlot},
	}, nil
}

// lowerAssignToProjection covers `dest.field = rvalue` patterns where
// the destination has a projection chain. Today's coverage is limited
// to FieldProj on a struct local (`p.x = 5`) and VariantProj on an
// enum local — both reduce to a static byte-offset write inside the
// local's stack slot. IndexProj writes (`xs[i] = v`) and dest-side
// VariantProj-with-payload don't lower yet because they need a
// runtime call (the former) or a discriminant check (the latter).
//
// The rvalue path mirrors `lowerAssign` for non-projected dests but
// scoped to a single 8-byte slot — we don't try to cover struct or
// enum-typed field writes (which would need multi-slot stores).
func (s *lowerState) lowerAssignToProjection(instr *mir.AssignInstr) ([]Instr, error) {
	dest := instr.Dest
	for _, p := range dest.Projections {
		switch p.(type) {
		case *mir.FieldProj, *mir.VariantProj:
		default:
			return nil, fmt.Errorf("%w: assign-to-projection of %T", ErrUnsupportedShape, p)
		}
	}
	baseSlot, ok := s.localSlots[dest.Local]
	if !ok {
		return nil, fmt.Errorf("%w: assign-to-projection on local%d without slot", ErrUnsupportedShape, dest.Local)
	}
	off, err := s.placeProjectionOffset(dest)
	if err != nil {
		return nil, err
	}
	slot := baseSlot + off
	switch rv := instr.Src.(type) {
	case *mir.UseRV:
		// Detect Float dest by walking the projection chain to its
		// final type. The MIR carries the field's type on the
		// FieldProj / VariantProj node; we can use that without
		// re-resolving through the layout table.
		fieldT := projectionEndType(dest)
		if isFloatABIType(fieldT) {
			mat, err := s.materialiseFloatOperand(rv.Op, RegD8)
			if err != nil {
				return nil, err
			}
			return append(mat, &StoreFloat64Stack{Src: RegD8, Offset: slot}), nil
		}
		mat, err := s.materialiseOperand(rv.Op, RegX9)
		if err != nil {
			return nil, err
		}
		return append(mat, &Store64Stack{Src: RegX9, Offset: slot}), nil
	case *mir.BinaryRV:
		return s.lowerBinaryAssign(rv, slot)
	default:
		return nil, fmt.Errorf("%w: assign-to-projection rvalue %T", ErrUnsupportedShape, instr.Src)
	}
}

// projectionEndType returns the MIR type that lives at the end of a
// projection chain. FieldProj.Type / VariantProj.Type carry the
// field's declared type, so the helper just reads the last
// projection's `Type` field. Empty chains return TUnit (caller is
// responsible for not asking that question).
func projectionEndType(place mir.Place) mir.Type {
	if len(place.Projections) == 0 {
		return mir.TUnit
	}
	last := place.Projections[len(place.Projections)-1]
	switch p := last.(type) {
	case *mir.FieldProj:
		return p.Type
	case *mir.VariantProj:
		return p.Type
	}
	return mir.TUnit
}

// lowerUseAssign handles `Assign dest := Use(operand)`. The common case
// is a single 8-byte slot copy through x9, but assignments where the
// destination local takes more than one ABI register (struct / enum
// passed via the AAPCS64 small-struct rule) need a per-slot copy so
// the payload byte isn't clipped. The destination's local type drives
// the slot count; the operand's type matches by construction (the
// front end never emits cross-shape Use rvalues). Float locals route
// through the d-register path so the bit pattern survives any
// future store-byte fusing the integer path might pick up.
func (s *lowerState) lowerUseAssign(fn *mir.Function, rv *mir.UseRV, destID mir.LocalID, destSlot int64) ([]Instr, error) {
	destLoc := lookupLocal(fn, destID)
	if destLoc != nil {
		if isFloatABIType(destLoc.Type) {
			mat, err := s.materialiseFloatOperand(rv.Op, RegD8)
			if err != nil {
				return nil, err
			}
			return append(mat, &StoreFloat64Stack{Src: RegD8, Offset: destSlot}), nil
		}
		if slots, ok := s.abiRegSlots(destLoc.Type); ok && slots > 1 {
			return s.lowerUseAssignMulti(rv, slots, destSlot)
		}
	}
	mat, err := s.materialiseOperand(rv.Op, RegX9)
	if err != nil {
		return nil, err
	}
	return append(mat, &Store64Stack{Src: RegX9, Offset: destSlot}), nil
}

// lowerUseAssignMulti copies an N-slot value (struct / small enum)
// from a Copy/Move source slot to the destination slot a half at a
// time. Const operands aren't valid for multi-slot copies — the front
// end constructs them via AggregateRV instead — so we reject them
// with the unsupported sentinel.
func (s *lowerState) lowerUseAssignMulti(rv *mir.UseRV, slots int, destSlot int64) ([]Instr, error) {
	var place mir.Place
	switch o := rv.Op.(type) {
	case *mir.CopyOp:
		place = o.Place
	case *mir.MoveOp:
		place = o.Place
	default:
		return nil, fmt.Errorf("%w: multi-slot use operand %T", ErrUnsupportedShape, rv.Op)
	}
	if place.HasProjections() {
		return nil, fmt.Errorf("%w: multi-slot use with projection", ErrUnsupportedShape)
	}
	srcSlot, ok := s.localSlots[place.Local]
	if !ok {
		return nil, fmt.Errorf("%w: multi-slot use of local%d without slot", ErrUnsupportedShape, place.Local)
	}
	out := make([]Instr, 0, slots*2)
	for i := 0; i < slots; i++ {
		out = append(out,
			&Load64Stack{Dst: RegX9, Offset: srcSlot + int64(i)*8},
			&Store64Stack{Src: RegX9, Offset: destSlot + int64(i)*8},
		)
	}
	return out, nil
}

// lowerAggregateAssign currently covers the empty-list literal,
// all-scalar struct literals, and small enum variants. The runtime
// allocates the list eagerly via `osty_rt_list_new`; struct + enum
// literals are stamped directly into the destination slot.
func (s *lowerState) lowerAggregateAssign(rv *mir.AggregateRV, destSlot int64) ([]Instr, error) {
	switch rv.Kind {
	case mir.AggList:
		if len(rv.Fields) != 0 {
			return nil, fmt.Errorf("%w: non-empty list literal", ErrUnsupportedShape)
		}
		return []Instr{
			&BranchLink{Symbol: runtimeSymListNew},
			&Store64Stack{Src: RegX0, Offset: destSlot},
		}, nil
	case mir.AggStruct:
		return s.lowerStructLiteralAssign(rv, destSlot)
	case mir.AggEnumVariant:
		return s.lowerEnumVariantAssign(rv, destSlot)
	case mir.AggClosure:
		return s.lowerClosureLiteralAssign(rv, destSlot)
	default:
		return nil, fmt.Errorf("%w: aggregate kind %v", ErrUnsupportedShape, rv.Kind)
	}
}

// lowerClosureLiteralAssign lowers `dest = aggregate closure(<fnConst>,
// <captures...>)`. Layout:
//
//  1. Allocate env via osty_rt_closure_env_alloc_v2(N, site, bitmap).
//     Returns the env pointer in x0.
//  2. Move env to x10 so we can stage the fn-pointer + captures
//     without losing the base.
//  3. Load lifted-fn address into x9 via adrp/add and store at
//     [x10 + 0].
//  4. For each capture i, materialise the value and store at
//     [x10 + 24 + i*8]. Captures are scalar / pointer types; Float
//     captures route through d8 + StoreToReg via x9 bitcast.
//  5. Store the env pointer (still in x10) into the destination
//     slot.
//
// The MVP keeps `pointer_bitmap` at 0 — the GC may then false-retain
// integer-sized scalar captures that happen to look like pointers.
// That's a known limitation; the LLVM backend computes a real
// bitmap by inspecting capture types (see `internal/llvmgen/fn_value.go`).
// For ONB's dev path this is acceptable trade-off; bitmap accuracy
// becomes important when GC-stress tests start flagging false roots.
func (s *lowerState) lowerClosureLiteralAssign(rv *mir.AggregateRV, destSlot int64) ([]Instr, error) {
	if len(rv.Fields) == 0 {
		return nil, fmt.Errorf("%w: closure literal needs at least the fn-const field", ErrUnsupportedShape)
	}
	fnConst, ok := rv.Fields[0].(*mir.ConstOp)
	if !ok {
		return nil, fmt.Errorf("%w: closure literal field 0 is %T, want ConstOp", ErrUnsupportedShape, rv.Fields[0])
	}
	fc, ok := fnConst.Const.(*mir.FnConst)
	if !ok || fc.Symbol == "" {
		return nil, fmt.Errorf("%w: closure literal field 0 is %T, want FnConst", ErrUnsupportedShape, fnConst.Const)
	}
	captureCount := int64(len(rv.Fields) - 1)
	siteLabel := s.addCString("onb.closure.env")
	out := []Instr{
		// alloc_v2(capture_count, site, bitmap=0)
		&MovImm64{Dst: RegX0, Imm: captureCount},
		&LoadCStringAddress{Dst: RegX1, Label: siteLabel},
		&MovImm64{Dst: RegX2, Imm: 0},
		&BranchLink{Symbol: runtimeSymClosureEnvAllocV2},
		// stash env pointer in x10 so subsequent stores can use a
		// stable base — x9 is reused as the value scratch.
		&MovRegReg{Dst: RegX10, Src: RegX0},
		// fn pointer at [env + 0]
		&LoadSymbolAddress{Dst: RegX9, Symbol: fc.Symbol},
		&StoreToReg{Src: RegX9, Base: RegX10, Offset: 0},
	}
	for i, capture := range rv.Fields[1:] {
		captureOffset := int64(closureEnvCapturesOffset + i*8)
		captureT := capture.Type()
		if isFloatABIType(captureT) {
			mat, err := s.materialiseFloatOperand(capture, RegD8)
			if err != nil {
				return nil, err
			}
			out = append(out, mat...)
			out = append(out,
				&FmovXFromD{Dst: RegX9, Src: RegD8},
				&StoreToReg{Src: RegX9, Base: RegX10, Offset: captureOffset},
			)
			continue
		}
		mat, err := s.materialiseOperand(capture, RegX9)
		if err != nil {
			return nil, err
		}
		out = append(out, mat...)
		out = append(out, &StoreToReg{Src: RegX9, Base: RegX10, Offset: captureOffset})
	}
	out = append(out, &Store64Stack{Src: RegX10, Offset: destSlot})
	return out, nil
}

// lowerEnumVariantAssign writes an enum literal: discriminant value
// to slot+0, then each payload field to slot+8 + i*8. Backends with
// a niche optimisation could fold the discriminant into the payload
// for some shapes, but the dev backend always uses the same layout
// to keep `frame variable` predictable.
func (s *lowerState) lowerEnumVariantAssign(rv *mir.AggregateRV, destSlot int64) ([]Instr, error) {
	layout := s.lookupEnumLayout(rv.T)
	if layout == nil {
		return nil, fmt.Errorf("%w: unknown enum layout for %s", ErrUnsupportedShape, rv.T)
	}
	if rv.VariantIdx < 0 || rv.VariantIdx >= len(layout.Variants) {
		return nil, fmt.Errorf("%w: variant index %d out of range for %s", ErrUnsupportedShape, rv.VariantIdx, rv.T)
	}
	variant := layout.Variants[rv.VariantIdx]
	if !s.payloadAllScalar(variant.Payload) {
		return nil, fmt.Errorf("%w: variant %s.%s has non-scalar payload", ErrUnsupportedShape, rv.T, variant.Name)
	}
	if len(rv.Fields) != len(variant.Payload) {
		return nil, fmt.Errorf("%w: variant %s.%s field count mismatch (%d vs %d)", ErrUnsupportedShape, rv.T, variant.Name, len(rv.Fields), len(variant.Payload))
	}
	tag := s.enumDiscriminantValue(layout, rv.VariantIdx)
	out := []Instr{
		&MovImm64{Dst: RegX9, Imm: tag},
		&Store64Stack{Src: RegX9, Offset: destSlot},
	}
	for i, field := range rv.Fields {
		mat, err := s.materialiseOperand(field, RegX9)
		if err != nil {
			return nil, err
		}
		out = append(out, mat...)
		out = append(out, &Store64Stack{Src: RegX9, Offset: destSlot + s.enumPayloadOffset(i)})
	}
	return out, nil
}

// lowerNullaryAssign covers `dest = none T?` — write the None tag (0)
// to slot+0 and leave the payload undefined. Other nullary rvalues
// don't exist in MIR today; new entries land here as the enum extends.
func (s *lowerState) lowerNullaryAssign(rv *mir.NullaryRV, destSlot int64) ([]Instr, error) {
	if rv.Kind != mir.NullaryNone {
		return nil, fmt.Errorf("%w: nullary kind %v", ErrUnsupportedShape, rv.Kind)
	}
	layout := s.lookupEnumLayout(rv.T)
	if layout == nil {
		return nil, fmt.Errorf("%w: nullary none on non-enum type %s", ErrUnsupportedShape, rv.T)
	}
	// Find the None variant; for synthetic Option layouts it's at
	// index 0 by construction. For user enums that re-use `none`
	// (none today) this would walk Variants for Name == "None".
	noneIdx := 0
	for i, v := range layout.Variants {
		if v.Name == "None" {
			noneIdx = i
			break
		}
	}
	tag := s.enumDiscriminantValue(layout, noneIdx)
	return []Instr{
		&MovImm64{Dst: RegX9, Imm: tag},
		&Store64Stack{Src: RegX9, Offset: destSlot},
	}, nil
}

// lowerDiscriminantAssign reads the discriminant tag of an enum local
// into the destination slot. The discriminant always sits at offset 0
// of the source slot, so this is just `ldr x9, [sp+src]; str x9,
// [sp+dest]`. Place projections on the source aren't supported — the
// front end always emits `discriminant <local>` rather than
// `discriminant <local>.field`.
func (s *lowerState) lowerDiscriminantAssign(rv *mir.DiscriminantRV, destSlot int64) ([]Instr, error) {
	if rv.Place.HasProjections() {
		return nil, fmt.Errorf("%w: discriminant of projected place", ErrUnsupportedShape)
	}
	srcSlot, ok := s.localSlots[rv.Place.Local]
	if !ok {
		return nil, fmt.Errorf("%w: discriminant of local%d without slot", ErrUnsupportedShape, rv.Place.Local)
	}
	return []Instr{
		&Load64Stack{Dst: RegX9, Offset: srcSlot},
		&Store64Stack{Src: RegX9, Offset: destSlot},
	}, nil
}

// lowerStructLiteralAssign writes each scalar field of a struct literal
// into its slot offset. Phase B.5 v1 only supports all-scalar structs;
// the slot allocator already sized the destination to fit. Fields are
// materialised one at a time through x9 to avoid burning x10 on a temp
// — there's no register pressure issue because each field write is
// independent.
func (s *lowerState) lowerStructLiteralAssign(rv *mir.AggregateRV, destSlot int64) ([]Instr, error) {
	layout := s.lookupStructLayout(rv.T)
	if layout == nil {
		return nil, fmt.Errorf("%w: unknown struct layout for %s", ErrUnsupportedShape, rv.T)
	}
	if len(rv.Fields) != len(layout.Fields) {
		return nil, fmt.Errorf("%w: struct %s field count mismatch (%d vs %d)", ErrUnsupportedShape, rv.T, len(rv.Fields), len(layout.Fields))
	}
	var out []Instr
	for i, field := range rv.Fields {
		if !isABIScalarType(layout.Fields[i].Type) {
			return nil, fmt.Errorf("%w: struct %s field %s has non-scalar type %s", ErrUnsupportedShape, rv.T, layout.Fields[i].Name, layout.Fields[i].Type)
		}
		mat, err := s.materialiseOperand(field, RegX9)
		if err != nil {
			return nil, err
		}
		out = append(out, mat...)
		out = append(out, &Store64Stack{Src: RegX9, Offset: destSlot + int64(i)*8})
	}
	return out, nil
}

// isABIScalarType reports whether a MIR type fits cleanly in one AAPCS64
// integer register. Phase A2 accepts Int (i64), Bool (i1), String (ptr),
// Float (Float / Float64 — d-register at the call boundary, scalar for
// slot allocation), and pointer-shaped types (FnType / ClosureEnv —
// closure environments live on the heap; the local holds an 8-byte
// pointer to them).
func isABIScalarType(t mir.Type) bool {
	if t == mir.TInt || t == mir.TBool || t == mir.TString {
		return true
	}
	if isFloatABIType(t) {
		return true
	}
	if isClosureScalarType(t) {
		return true
	}
	return isBuiltinPointerType(t)
}

// isClosureScalarType reports whether t is a closure-shaped pointer
// — a function value (`fn(A) -> R`) or the lifted body's first-arg
// `ClosureEnv` builtin. Both are 8 bytes at the ABI boundary.
func isClosureScalarType(t mir.Type) bool {
	if _, ok := t.(*ir.FnType); ok {
		return true
	}
	nt, ok := t.(*ir.NamedType)
	if !ok || nt == nil {
		return false
	}
	return nt.Builtin && nt.Name == "ClosureEnv"
}

// isBuiltinPointerType reports whether t is a builtin generic
// container that's represented as a heap pointer at the ABI
// boundary. Lists, Maps, Sets, Channels, and similar runtime-
// managed values all fit this shape — the local holds an 8-byte
// pointer to the runtime data structure regardless of element
// type. Lets `abiRegSlots` accept these as 1-reg pointer args /
// returns without enumerating each builtin.
func isBuiltinPointerType(t mir.Type) bool {
	nt, ok := t.(*ir.NamedType)
	if !ok || nt == nil {
		return false
	}
	if !nt.Builtin {
		return false
	}
	switch nt.Name {
	case "List", "Map", "Set", "Channel", "Bytes", "Handle":
		return true
	}
	return false
}

// isFloatABIType reports whether t is a Float / Float64 — values that
// occupy a d-register on AAPCS64 calls and need fmov / fadd opcodes
// rather than the integer-side str / add lowerings.
func isFloatABIType(t mir.Type) bool {
	return t == mir.TFloat || t == mir.TFloat64
}

// abiRegSlots reports how many AAPCS64 integer-argument registers an ABI
// value of type t consumes. Scalars are 1, small structs (≤ 16B with all
// scalar fields) are ceil(size/8), Unit is 0 (callers should skip), and
// anything else returns 0 with ok=false to signal the lowerer should bail.
func (s *lowerState) abiRegSlots(t mir.Type) (int, bool) {
	if t == mir.TUnit {
		return 0, true
	}
	if isABIScalarType(t) {
		return 1, true
	}
	if layout := s.lookupStructLayout(t); layout != nil {
		for _, f := range layout.Fields {
			if !isABIScalarType(f.Type) {
				return 0, false
			}
		}
		n := len(layout.Fields)
		if n == 0 {
			return 0, false
		}
		// Small structs (≤2 regs) ride the direct register path
		// established in Week 13. Larger all-scalar structs go
		// through the indirect (sret) path: the caller passes a
		// pointer in one integer register (or x8 for returns) and
		// the callee dereferences. Indirect uses 1 regular arg
		// register slot (the pointer), so we report 1 here. The
		// caller distinguishes direct vs indirect via
		// abiUsesIndirectStruct(t).
		if n <= abiSmallStructRegLimit {
			return n, true
		}
		if n <= abiIndirectStructFieldLimit {
			return 1, true
		}
		return 0, false
	}
	if layout := s.lookupEnumLayout(t); layout != nil {
		size := s.enumSlotSize(layout)
		if size == 0 {
			return 0, false
		}
		// Discriminant (1 reg) + at most one payload reg fits the
		// AAPCS64 small-struct convention. enumSlotSize already
		// caps at 16B so the divide here can't exceed
		// abiSmallStructRegLimit.
		return int(size / 8), true
	}
	return 0, false
}

// isABIPassableType reports whether t is something the ABI guards can
// accept as a parameter or return type. Scalars and small structs are in;
// builtin generics (List<T>, Map<K,V>, etc.) and large structs stay out
// until a follow-up slice teaches the lowerer about indirect passing.
func (s *lowerState) isABIPassableType(t mir.Type) bool {
	if isABIScalarType(t) {
		return true
	}
	_, ok := s.abiRegSlots(t)
	return ok
}

// abiUsesIndirectStruct reports whether a struct/enum type t passes
// or returns through the AAPCS64 indirect (sret) ABI: the value is
// laid out in a caller-allocated buffer whose address rides in a
// regular argument register (for params) or x8 (for returns). Phase
// A2 supports 3- to 4-field all-scalar structs this way; up to 16B
// (1- or 2-field) takes the direct register path.
func (s *lowerState) abiUsesIndirectStruct(t mir.Type) bool {
	layout := s.lookupStructLayout(t)
	if layout == nil {
		return false
	}
	for _, f := range layout.Fields {
		if !isABIScalarType(f.Type) {
			return false
		}
	}
	n := len(layout.Fields)
	return n > abiSmallStructRegLimit && n <= abiIndirectStructFieldLimit
}

// indirectStructByteSize returns the byte size of an indirect-passed
// struct slot — n × 8. Caller pre-checks abiUsesIndirectStruct.
func (s *lowerState) indirectStructByteSize(t mir.Type) int64 {
	layout := s.lookupStructLayout(t)
	if layout == nil {
		return 0
	}
	return int64(len(layout.Fields)) * 8
}

// lookupStructLayout resolves a NamedType to its `mod.Layouts.Structs`
// entry. Returns nil for non-named types or layouts the front end didn't
// register. The lowerer uses this to size struct slots and to compute
// per-field offsets — Phase B.5 v1 only handles all-scalar structs so a
// straightforward `field_index * 8` is enough.
func (s *lowerState) lookupStructLayout(t mir.Type) *mir.StructLayout {
	if s.mod == nil || s.mod.Layouts == nil {
		return nil
	}
	nt, ok := t.(*ir.NamedType)
	if !ok || nt == nil {
		return nil
	}
	if !structLayoutSupported(nt) {
		return nil
	}
	key := nt.Name
	if nt.Package != "" && !nt.Builtin {
		key = strings.TrimPrefix(nt.Package, "std.") + "." + nt.Name
	}
	return s.mod.Layouts.Structs[key]
}

// structLayoutSupported reports whether the named type is a Phase B.5 v1
// candidate — built-in types (List, Map, etc.) are still routed through
// runtime calls, so we explicitly opt them out.
func structLayoutSupported(nt *ir.NamedType) bool {
	if nt == nil || nt.Builtin {
		return false
	}
	return true
}

// lookupEnumLayout resolves a MIR type to the enum layout the front end
// registered. User-defined enums (`enum Color { Red, Green, Blue }`)
// live in `mod.Layouts.Enums` keyed by the enum's name. Optional types
// (`T?`) use a synthetic layout the lowerer fabricates on the fly so
// the rest of the enum codegen path can speak in the same vocabulary
// — there's no separate Option entry in the layout table because the
// surface form is `OptionalType` rather than a NamedType.
//
// The synthetic Option layout uses the canonical convention from
// `internal/llvmgen` and `internal/mir/lower.go`: discriminant 0 ==
// None, 1 == Some, with the inner type as the Some payload. Result
// types are surfaced as `NamedType{Name: "Result"}` and do live in
// `mod.Layouts.Enums` after the front end's `buildLayouts` pass, so
// they take the user-defined branch.
func (s *lowerState) lookupEnumLayout(t mir.Type) *mir.EnumLayout {
	if s.mod == nil {
		return nil
	}
	if opt, ok := t.(*ir.OptionalType); ok && opt != nil {
		return s.syntheticOptionLayout(opt.Inner)
	}
	nt, ok := t.(*ir.NamedType)
	if !ok || nt == nil {
		return nil
	}
	// Builtin Option / Maybe / Result types use synthetic layouts
	// (None=0/Some=1, Err=0/Ok=1) the front end doesn't bother
	// registering into `mod.Layouts.Enums`. The MIR layer canonicalises
	// `T?` references to `NamedType{Name: "Option", Args: [T],
	// Builtin: true}` (notably AggregateRV.T for `Some(...)` calls)
	// so the lowerer must recognise both shapes.
	if nt.Builtin {
		switch nt.Name {
		case "Option", "Maybe":
			if len(nt.Args) >= 1 {
				return s.syntheticOptionLayout(nt.Args[0])
			}
		case "Result":
			if len(nt.Args) >= 2 {
				return s.syntheticResultLayout(nt.Args[0], nt.Args[1])
			}
		}
	}
	if s.mod.Layouts == nil {
		return nil
	}
	return s.mod.Layouts.Enums[nt.Name]
}

// syntheticOptionLayout builds the canonical 2-variant enum layout
// (None=0 / Some(inner)=1) used wherever the front end emits an
// optional type without registering it into `mod.Layouts.Enums`. Kept
// out of `lookupEnumLayout` so the OptionalType and NamedType paths
// share the same shape.
func (s *lowerState) syntheticOptionLayout(inner mir.Type) *mir.EnumLayout {
	return &mir.EnumLayout{
		Name:         "Option",
		Discriminant: mir.TInt,
		Variants: []mir.VariantLayout{
			{Index: 0, Name: "None"},
			{Index: 1, Name: "Some", Payload: []mir.FieldLayout{{Index: 0, Name: "value", Type: inner}}},
		},
	}
}

// syntheticResultLayout builds the 2-variant enum layout (Err=0 /
// Ok=1) for `Result<T, E>`. The Err arm carries one E-typed payload
// field; the Ok arm carries one T-typed field. Each variant has a
// single payload register so the whole Result fits in 16 bytes (1
// disc + 1 payload), which keeps it eligible for AAPCS64
// small-struct passing. The Err and Ok payload types live in
// different variants — `enumSlotSize` reports the maximum payload
// reg count across all variants, so a Result whose Err half needs
// 2 regs would stop being eligible.
func (s *lowerState) syntheticResultLayout(okT, errT mir.Type) *mir.EnumLayout {
	return &mir.EnumLayout{
		Name:         "Result",
		Discriminant: mir.TInt,
		Variants: []mir.VariantLayout{
			{Index: 0, Name: "Err", Payload: []mir.FieldLayout{{Index: 0, Name: "error", Type: errT}}},
			{Index: 1, Name: "Ok", Payload: []mir.FieldLayout{{Index: 0, Name: "value", Type: okT}}},
		},
	}
}

// enumSlotSize returns the on-stack byte size for an enum-typed local.
// The layout is `[disc 8B][payload N×8B]` where N is the maximum
// payload field count across all variants. Discriminant lives at slot
// offset 0, payload starts at slot offset 8. Phase A2 caps total size
// at 16B (1 disc reg + 1 payload reg) so the value fits the AAPCS64
// small-struct ABI; larger payloads return 0 to signal "fall back".
func (s *lowerState) enumSlotSize(layout *mir.EnumLayout) int64 {
	if layout == nil {
		return 0
	}
	maxPayload := 0
	for _, v := range layout.Variants {
		if !s.payloadAllScalar(v.Payload) {
			return 0
		}
		if len(v.Payload) > maxPayload {
			maxPayload = len(v.Payload)
		}
	}
	// Discriminant + payload, capped at 2 registers total.
	total := int64(8 + maxPayload*8)
	if total > 16 {
		return 0
	}
	return total
}

// payloadAllScalar reports whether every payload field fits in one
// 8-byte ABI register. Scalars (Int / Bool / String) qualify
// directly; no-payload enums (`MyError` etc.) also pass because their
// slot is just the discriminant. Multi-register payloads (large
// struct, deep enum) bail out so the lowerer keeps the whole enum
// inside a 16-byte 2-register window.
//
// Naming kept for git-blame continuity — the predicate now accepts
// any 1-register type, not strictly scalars.
func (s *lowerState) payloadAllScalar(fields []mir.FieldLayout) bool {
	for _, f := range fields {
		if !s.isABIWordType(f.Type) {
			return false
		}
	}
	return true
}

// isABIWordType reports whether t consumes exactly one 8-byte ABI
// register (and therefore can sit in an enum payload slot). Scalars
// and no-payload enums qualify; structs always need ≥1 register
// per field so a struct with 1 scalar field also passes.
func (s *lowerState) isABIWordType(t mir.Type) bool {
	if isABIScalarType(t) {
		return true
	}
	slots, ok := s.abiRegSlots(t)
	return ok && slots == 1
}

// enumPayloadOffset returns the byte offset of the FieldIdx-th payload
// element relative to the enum's slot. Discriminant lives at offset 0,
// payload starts at offset 8.
func (s *lowerState) enumPayloadOffset(fieldIdx int) int64 {
	if fieldIdx < 0 {
		return 8 // "whole payload tuple" — start of payload region
	}
	return int64(8 + fieldIdx*8)
}

// enumDiscriminantValue returns the integer tag for a variant. Most
// enums simply use the variant index, but the synthetic Option layout
// keeps None=0 / Some=1 explicit so the sequence matches the rest of
// the toolchain's convention.
func (s *lowerState) enumDiscriminantValue(layout *mir.EnumLayout, variantIdx int) int64 {
	if layout == nil || variantIdx < 0 || variantIdx >= len(layout.Variants) {
		return int64(variantIdx)
	}
	return int64(layout.Variants[variantIdx].Index)
}

// localTypeSize returns the on-stack byte size for a local. Scalars and
// builtin pointer-shaped types (List<T>, Map<K,V>) share one 8-byte
// slot. User-defined structs with all-scalar fields get one slot per
// field. Enum-typed locals (user enums + Option<scalar> + Result with
// scalar arms) get `[disc 8B][payload N×8B]` layout up to 16B total.
// Unit-typed locals report 0 — the slot allocator skips them entirely.
func (s *lowerState) localTypeSize(t mir.Type) int64 {
	if t == mir.TUnit {
		return 0
	}
	if isABIScalarType(t) {
		return 8
	}
	if sl := s.lookupStructLayout(t); sl != nil {
		// Each field reserves one 8-byte slot. >16-byte structs still
		// fit because we don't try to pass them in registers yet — the
		// slot just grows. Mixed scalar/composite fields fall through
		// to "treat as opaque pointer" so pre-existing List/Map locals
		// keep their 8-byte slot.
		for _, f := range sl.Fields {
			if !isABIScalarType(f.Type) {
				return 8
			}
		}
		return int64(len(sl.Fields)) * 8
	}
	if el := s.lookupEnumLayout(t); el != nil {
		if size := s.enumSlotSize(el); size > 0 {
			return size
		}
	}
	// Anything else (builtin generics, raw pointer wrappers, opaque
	// runtime handles) lives at the ABI as a single 8-byte pointer.
	return 8
}

// fieldByteOffset returns the byte offset of `index`-th field inside the
// struct described by t. Returns -1 if t isn't a recognised struct so
// callers can short-circuit before generating bad code.
func (s *lowerState) fieldByteOffset(t mir.Type, index int) int64 {
	if s.lookupStructLayout(t) == nil {
		return -1
	}
	return int64(index) * 8
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
// Scalars (Int, Bool, String) get their primitive DebugTypeKind. Small
// structs (≤ 16B with all-scalar fields) get DebugTypeStruct plus the
// struct's name and field list, which the macho writer threads into a
// DW_TAG_structure_type + DW_TAG_member tree so lldb can pretty-print
// `frame variable` rows like `(Point) p = (x = 3, y = 4)`. Mixed or
// composite structs collapse to DebugTypeNone so they stay invisible
// rather than confusing the debugger with half-described aggregates.
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
		if dl, ok := s.debugLocalForStruct(loc, slot); ok {
			out = append(out, dl)
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

// debugLocalForStruct produces a DebugLocal entry for a small struct
// local. Returns ok=false when the local isn't a recognised small
// struct so the caller can fall through to the scalar path. Only
// all-scalar small structs surface — anything with a non-scalar field
// would need either a recursive struct DIE or pointer-DIE plumbing
// neither of which the dev backend ships yet.
func (s *lowerState) debugLocalForStruct(loc *mir.Local, slot int64) (DebugLocal, bool) {
	layout := s.lookupStructLayout(loc.Type)
	if layout == nil {
		return DebugLocal{}, false
	}
	if len(layout.Fields) == 0 {
		return DebugLocal{}, false
	}
	fields := make([]DebugStructField, 0, len(layout.Fields))
	for _, f := range layout.Fields {
		kind := mirTypeToDebugKind(f.Type)
		if kind == DebugTypeNone {
			return DebugLocal{}, false
		}
		fields = append(fields, DebugStructField{Name: f.Name, FieldKind: kind})
	}
	return DebugLocal{
		Name:         loc.Name,
		SlotOffset:   slot,
		TypeKind:     DebugTypeStruct,
		StructName:   layout.Name,
		StructFields: fields,
	}, true
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
	if isFloatABIType(rv.T) {
		return s.lowerFloatBinaryAssign(rv, destSlot)
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

// lowerFloatBinaryAssign lowers `dest = lhs <op> rhs` for two Float
// operands. The two operands materialise into d8/d9, then `f<op> d8,
// d8, d9` produces the result, which a `str d8, [sp, #destSlot]`
// commits back. d8/d9 are AAPCS64 callee-saved — using them as the
// scratch pair means a follow-up that adds a register allocator can
// keep them around across calls without extra spills.
func (s *lowerState) lowerFloatBinaryAssign(rv *mir.BinaryRV, destSlot int64) ([]Instr, error) {
	lhs, err := s.materialiseFloatOperand(rv.Left, RegD8)
	if err != nil {
		return nil, err
	}
	rhs, err := s.materialiseFloatOperand(rv.Right, RegD9)
	if err != nil {
		return nil, err
	}
	out := append([]Instr(nil), lhs...)
	out = append(out, rhs...)
	switch rv.Op {
	case mir.BinAdd:
		out = append(out, &FaddReg{Dst: RegD8, Lhs: RegD8, Rhs: RegD9})
	case mir.BinSub:
		out = append(out, &FsubReg{Dst: RegD8, Lhs: RegD8, Rhs: RegD9})
	case mir.BinMul:
		out = append(out, &FmulReg{Dst: RegD8, Lhs: RegD8, Rhs: RegD9})
	case mir.BinDiv:
		out = append(out, &FdivReg{Dst: RegD8, Lhs: RegD8, Rhs: RegD9})
	default:
		return nil, fmt.Errorf("%w: float binary op %v is outside phase 1", ErrUnsupportedShape, rv.Op)
	}
	out = append(out, &StoreFloat64Stack{Src: RegD8, Offset: destSlot})
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

// lowerCall emits an AAPCS64 direct call. Integer / pointer / struct
// args land in x0..x7 (with structs consuming two adjacent x slots);
// Float64 args land in d0..d7 from the independent FP cursor. After
// `bl _<name>`, the return value moves into the destination slot:
// scalar integer returns capture x0, scalar Float returns capture d0
// via `str d0`, small struct returns capture {x0, x1} into
// dest+0/+8. FnRef-only — indirect calls and parameter types outside
// ABI fall back.
func (s *lowerState) lowerCall(fn *mir.Function, instr *mir.CallInstr) ([]Instr, error) {
	if ind, ok := instr.Callee.(*mir.IndirectCall); ok {
		return s.lowerIndirectCall(fn, instr, ind)
	}
	ref, ok := instr.Callee.(*mir.FnRef)
	if !ok {
		return nil, fmt.Errorf("%w: callee shape %T", ErrUnsupportedShape, instr.Callee)
	}
	var out []Instr
	regCursor := 0
	fpCursor := 0
	// Sret-style return: callee writes the return value through
	// x8 into a buffer the caller allocates. The dest local's slot
	// is that buffer — we just stage `add x8, sp, #destSlot` before
	// the bl and skip the post-call capture entirely.
	sretDestSlot := int64(-1)
	if instr.Dest != nil && !instr.Dest.HasProjections() {
		destType := s.destType(instr.Dest.Local)
		if s.abiUsesIndirectStruct(destType) {
			if slot, ok := s.localSlots[instr.Dest.Local]; ok {
				sretDestSlot = slot
			}
		}
	}
	for _, arg := range instr.Args {
		argType := arg.Type()
		if isFloatABIType(argType) {
			if fpCursor >= len(fpArgRegs) {
				return nil, fmt.Errorf("%w: call to %s needs more than %d FP argument registers", ErrUnsupportedShape, ref.Symbol, len(fpArgRegs))
			}
			mat, err := s.materialiseFloatOperand(arg, fpArgRegs[fpCursor])
			if err != nil {
				return nil, err
			}
			out = append(out, mat...)
			fpCursor++
			continue
		}
		if s.abiUsesIndirectStruct(argType) {
			// Pass &(srcLocal_slot) in argRegs[regCursor]. The arg
			// is always a Copy / Move of a local — front-end never
			// emits struct literals as call arguments — so we
			// resolve to a slot and emit `add Xt, sp, #slot`.
			ptrInstrs, err := s.indirectArgAddress(arg, argRegs[regCursor])
			if err != nil {
				return nil, err
			}
			out = append(out, ptrInstrs...)
			regCursor++
			continue
		}
		slots, ok := s.abiRegSlots(argType)
		if !ok {
			return nil, fmt.Errorf("%w: call argument has type %s outside ABI", ErrUnsupportedShape, argType)
		}
		if regCursor+slots > len(argRegs) {
			return nil, fmt.Errorf("%w: call to %s needs more than %d argument registers", ErrUnsupportedShape, ref.Symbol, len(argRegs))
		}
		if slots > 1 {
			// Multi-register struct argument. Today's slow-path only
			// supports a whole-struct Copy/Move out of a stack slot —
			// FieldProj-of-struct-arg or struct-literal-as-arg are
			// not lowered yet.
			mat, err := s.materialiseStructOperand(arg, argRegs[regCursor:regCursor+slots])
			if err != nil {
				return nil, err
			}
			out = append(out, mat...)
		} else {
			mat, err := s.materialiseOperand(arg, argRegs[regCursor])
			if err != nil {
				return nil, err
			}
			out = append(out, mat...)
		}
		regCursor += slots
	}
	// Stage x8 = &destSlot last so the address materialisation
	// happens after every other arg-reg setup — this matches the
	// AAPCS64 convention and avoids accidentally clobbering x8 with
	// an arg materialiser that happens to use it as scratch (none
	// today, but keeps the slot order future-proof).
	if sretDestSlot >= 0 {
		out = append(out, &LoadStackAddress{Dst: regX8, Offset: sretDestSlot})
	}
	out = append(out, &BranchLink{Symbol: ref.Symbol})
	if instr.Dest != nil && !instr.Dest.HasProjections() && sretDestSlot < 0 {
		if slot, ok := s.localSlots[instr.Dest.Local]; ok {
			destType := s.destType(instr.Dest.Local)
			if isFloatABIType(destType) {
				out = append(out, &StoreFloat64Stack{Src: RegD0, Offset: slot})
			} else {
				retSlots, _ := s.abiRegSlots(destType)
				if retSlots == 0 {
					retSlots = 1
				}
				retRegs := []Reg{RegX0, RegX1}
				for i := 0; i < retSlots && i < len(retRegs); i++ {
					out = append(out, &Store64Stack{Src: retRegs[i], Offset: slot + int64(i)*8})
				}
			}
		}
	}
	return out, nil
}

// lowerIndirectCall lowers `call *<closure_local>(args...)`. The
// closure value is an env-pointer; per the Phase A4 fn-value runtime
// contract the lifted body's signature is `(env, args...)`, so the
// lowering stages the env in x0, the user args in x1.., d0..d7, and
// loads the fn pointer from the env's first slot before `blr`.
//
// The fn pointer load goes into x9 — neither the caller nor the
// callee can rely on x9 surviving across the call, so we don't need
// to spill it. The env pointer in x0 is consumed by the call as the
// ClosureEnv argument.
func (s *lowerState) lowerIndirectCall(fn *mir.Function, instr *mir.CallInstr, ind *mir.IndirectCall) ([]Instr, error) {
	if len(instr.Args) > len(argRegs)-1 {
		return nil, fmt.Errorf("%w: indirect call user-arg count %d exceeds limit", ErrUnsupportedShape, len(instr.Args))
	}
	envInstrs, err := s.materialiseOperand(ind.Callee, RegX0)
	if err != nil {
		return nil, err
	}
	out := append([]Instr(nil), envInstrs...)
	regCursor := 1 // x0 reserved for env
	fpCursor := 0
	for _, arg := range instr.Args {
		argType := arg.Type()
		if isFloatABIType(argType) {
			if fpCursor >= len(fpArgRegs) {
				return nil, fmt.Errorf("%w: indirect call needs more than %d FP arg regs", ErrUnsupportedShape, len(fpArgRegs))
			}
			mat, err := s.materialiseFloatOperand(arg, fpArgRegs[fpCursor])
			if err != nil {
				return nil, err
			}
			out = append(out, mat...)
			fpCursor++
			continue
		}
		if regCursor >= len(argRegs) {
			return nil, fmt.Errorf("%w: indirect call needs more than %d int arg regs", ErrUnsupportedShape, len(argRegs)-1)
		}
		slots, ok := s.abiRegSlots(argType)
		if !ok || slots != 1 {
			return nil, fmt.Errorf("%w: indirect call arg type %s not supported", ErrUnsupportedShape, argType)
		}
		mat, err := s.materialiseOperand(arg, argRegs[regCursor])
		if err != nil {
			return nil, err
		}
		out = append(out, mat...)
		regCursor++
	}
	// Load fn pointer from env+0 into x9 then `blr x9`. The env
	// pointer is already in x0 from the materialise above and stays
	// there through the branch.
	out = append(out,
		&LoadFromReg{Dst: RegX9, Src: RegX0, Offset: 0},
		&BranchLinkReg{Reg: RegX9},
	)
	if instr.Dest != nil && !instr.Dest.HasProjections() {
		if slot, ok := s.localSlots[instr.Dest.Local]; ok {
			destType := s.destType(instr.Dest.Local)
			if isFloatABIType(destType) {
				out = append(out, &StoreFloat64Stack{Src: RegD0, Offset: slot})
			} else {
				out = append(out, &Store64Stack{Src: RegX0, Offset: slot})
			}
		}
	}
	return out, nil
}

// indirectArgAddress produces the instructions that land &(src_slot)
// in the destination argument register. Indirect args are always
// passed as a Copy or Move of a stack-allocated local; struct
// literals in argument position would need a pre-materialise pass we
// don't ship yet.
func (s *lowerState) indirectArgAddress(arg mir.Operand, dst Reg) ([]Instr, error) {
	var place mir.Place
	switch o := arg.(type) {
	case *mir.CopyOp:
		place = o.Place
	case *mir.MoveOp:
		place = o.Place
	default:
		return nil, fmt.Errorf("%w: indirect struct arg operand %T", ErrUnsupportedShape, arg)
	}
	if place.HasProjections() {
		return nil, fmt.Errorf("%w: indirect struct arg with projection", ErrUnsupportedShape)
	}
	slot, ok := s.localSlots[place.Local]
	if !ok {
		return nil, fmt.Errorf("%w: indirect struct arg from local%d without slot", ErrUnsupportedShape, place.Local)
	}
	return []Instr{&LoadStackAddress{Dst: dst, Offset: slot}}, nil
}


// destType returns the MIR type of a local in the current function. The
// caller already knows the local is a Dest of a CallInstr, so a missing
// entry is only possible for malformed MIR — we fall back to TInt so the
// 1-register capture path keeps working.
func (s *lowerState) destType(id mir.LocalID) mir.Type {
	if loc := lookupLocal(s.fn, id); loc != nil {
		return loc.Type
	}
	return mir.TInt
}

// materialiseStructOperand loads a whole-struct operand into N consecutive
// argument registers. Slot+0 lands in regs[0], slot+8 in regs[1], etc.
// Only Copy/Move from a stack-slot-backed local with no projection are
// handled; FieldProj-of-struct or struct-literal-as-arg patterns return
// the unsupported sentinel so the LLVM fallback owns them.
func (s *lowerState) materialiseStructOperand(op mir.Operand, regs []Reg) ([]Instr, error) {
	var place mir.Place
	switch o := op.(type) {
	case *mir.CopyOp:
		place = o.Place
	case *mir.MoveOp:
		place = o.Place
	default:
		return nil, fmt.Errorf("%w: struct argument operand %T", ErrUnsupportedShape, op)
	}
	if place.HasProjections() {
		return nil, fmt.Errorf("%w: struct argument with projection", ErrUnsupportedShape)
	}
	slot, ok := s.localSlots[place.Local]
	if !ok {
		return nil, fmt.Errorf("%w: struct argument from local%d without slot", ErrUnsupportedShape, place.Local)
	}
	out := make([]Instr, 0, len(regs))
	for i, r := range regs {
		out = append(out, &Load64Stack{Dst: r, Offset: slot + int64(i)*8})
	}
	return out, nil
}

// materialiseOperand emits the instruction sequence that lands the operand's
// value in dst. Supports Int / Bool / String constants and Copy/Move of
// locals that have a stack slot. String constants land in dst as a pointer
// to their `__cstring` entry. Float operands take a separate path via
// materialiseFloatOperand because they need d-register destinations.
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
		if hasIndexProjection(o.Place) {
			return s.loadIndexedPlaceIntoIntReg(o.Place, dst)
		}
		if hasEnvDerefProjection(s, o.Place) {
			return s.loadEnvProjection(o.Place, dst)
		}
		return s.loadPlaceIntoReg(o.Place, dst)
	case *mir.MoveOp:
		if hasIndexProjection(o.Place) {
			return s.loadIndexedPlaceIntoIntReg(o.Place, dst)
		}
		if hasEnvDerefProjection(s, o.Place) {
			return s.loadEnvProjection(o.Place, dst)
		}
		return s.loadPlaceIntoReg(o.Place, dst)
	default:
		return nil, fmt.Errorf("%w: operand %T", ErrUnsupportedShape, op)
	}
}

// hasIndexProjection reports whether the place's projection chain
// contains an IndexProj — `place[idx]`. Used by the operand
// materialiser to switch from the static slot+offset path to a
// runtime list_get_* call.
func hasIndexProjection(place mir.Place) bool {
	for _, p := range place.Projections {
		if _, ok := p.(*mir.IndexProj); ok {
			return true
		}
	}
	return false
}

// loadIndexedPlaceIntoIntReg lowers `local[idx]` into a runtime call
// to `osty_rt_list_get_*`, capturing x0 (or the appropriate result
// register) into `dst`. The destination must be an integer register;
// Float reads route through `loadIndexedPlaceIntoFloatReg`. The
// helper assumes the projection chain is exactly one IndexProj — no
// nested struct/variant chasing — which matches every for-in lowering
// and direct `list[i]` read the front end emits today.
func (s *lowerState) loadIndexedPlaceIntoIntReg(place mir.Place, dst Reg) ([]Instr, error) {
	prefix, idxProj, err := s.indexCallPrefix(place, mir.TInt)
	if err != nil {
		return nil, err
	}
	sym, err := listGetSymbol(idxProj.ElemType)
	if err != nil {
		return nil, err
	}
	out := append([]Instr(nil), prefix...)
	out = append(out, &BranchLink{Symbol: sym})
	if dst != RegX0 {
		out = append(out, &MovRegReg{Dst: dst, Src: RegX0})
	}
	return out, nil
}

// loadIndexedPlaceIntoFloatReg is the FP twin of
// loadIndexedPlaceIntoIntReg — it stages `osty_rt_list_get_f64` and
// the result already arrives in d0, so we only need a final fmov if
// the caller asked for a different d-register.
func (s *lowerState) loadIndexedPlaceIntoFloatReg(place mir.Place, dst Reg) ([]Instr, error) {
	prefix, idxProj, err := s.indexCallPrefix(place, mir.TInt)
	if err != nil {
		return nil, err
	}
	if !isFloatABIType(idxProj.ElemType) {
		return nil, fmt.Errorf("%w: indexed float load on non-float list element %s", ErrUnsupportedShape, idxProj.ElemType)
	}
	out := append([]Instr(nil), prefix...)
	out = append(out, &BranchLink{Symbol: runtimeSymListGetF64})
	if dst != RegD0 {
		// Bitcast d0 → x9 → dst keeps us within the existing opcode
		// catalogue — full d-to-d moves aren't surfaced as a LIR
		// node yet because no other code path needs them.
		out = append(out,
			&FmovXFromD{Dst: RegX9, Src: RegD0},
			&FmovDFromX{Dst: dst, Src: RegX9},
		)
	}
	return out, nil
}

// indexCallPrefix materialises the list pointer into x0 and the
// index value into x1, then returns the IndexProj so the caller can
// route to the right runtime symbol. Used by both the int and float
// indexed-load paths so list/index plumbing only lives in one place.
func (s *lowerState) indexCallPrefix(place mir.Place, indexExpected mir.Type) ([]Instr, *mir.IndexProj, error) {
	if len(place.Projections) != 1 {
		return nil, nil, fmt.Errorf("%w: indexed place with %d projections", ErrUnsupportedShape, len(place.Projections))
	}
	idxProj, ok := place.Projections[0].(*mir.IndexProj)
	if !ok {
		return nil, nil, fmt.Errorf("%w: expected IndexProj, got %T", ErrUnsupportedShape, place.Projections[0])
	}
	listSlot, ok := s.localSlots[place.Local]
	if !ok {
		return nil, nil, fmt.Errorf("%w: indexed read of local%d without slot", ErrUnsupportedShape, place.Local)
	}
	idxInstrs, err := s.materialiseOperand(idxProj.Index, RegX1)
	if err != nil {
		return nil, nil, err
	}
	out := append([]Instr(nil), &Load64Stack{Dst: RegX0, Offset: listSlot})
	out = append(out, idxInstrs...)
	return out, idxProj, nil
}

// listGetSymbol picks the runtime list_get entry point for an element
// type. Symmetric with the dispatch in lowerListPush; new element
// types add an entry here together with their runtime symbol.
func listGetSymbol(elem mir.Type) (string, error) {
	switch {
	case elem == mir.TInt:
		return runtimeSymListGetI64, nil
	case elem == mir.TBool:
		return runtimeSymListGetI1, nil
	case elem == mir.TString:
		return runtimeSymListGetString, nil
	case isFloatABIType(elem):
		return runtimeSymListGetF64, nil
	}
	return "", fmt.Errorf("%w: list element type %s", ErrUnsupportedShape, elem)
}

// materialiseFloatOperand emits the instruction sequence that lands a
// Float64-typed operand in `dst` (a d-register). Supports FloatConst
// (movz/movk into a scratch x register + fmov d, x) and Copy/Move of
// Float-typed locals (ldr d, [sp, #N]). The IntConst path covers the
// MIR shape `const 2 Float` that the front end emits when an integer
// literal appears in a float context — we promote the integer to a
// Float64 bit pattern at compile time.
func (s *lowerState) materialiseFloatOperand(op mir.Operand, dst Reg) ([]Instr, error) {
	switch o := op.(type) {
	case *mir.ConstOp:
		switch c := o.Const.(type) {
		case *mir.FloatConst:
			return floatConstInstrs(dst, c.Value), nil
		case *mir.IntConst:
			// `const 2 Float` form — promote to float bit pattern.
			return floatConstInstrs(dst, float64(c.Value)), nil
		default:
			return nil, fmt.Errorf("%w: float const %T as operand", ErrUnsupportedShape, o.Const)
		}
	case *mir.CopyOp:
		if hasIndexProjection(o.Place) {
			return s.loadIndexedPlaceIntoFloatReg(o.Place, dst)
		}
		return s.loadFloatPlaceIntoReg(o.Place, dst)
	case *mir.MoveOp:
		if hasIndexProjection(o.Place) {
			return s.loadIndexedPlaceIntoFloatReg(o.Place, dst)
		}
		return s.loadFloatPlaceIntoReg(o.Place, dst)
	default:
		return nil, fmt.Errorf("%w: float operand %T", ErrUnsupportedShape, op)
	}
}

// floatConstInstrs emits `movz/movk` into x9 followed by `fmov dst,
// x9` to deposit a Float64 immediate into a d-register. The dev
// backend always uses x9 as the FP-immediate scratch — choosing the
// same register every time keeps the lowering simple and lets the
// asm renderer print a uniform sequence.
func floatConstInstrs(dst Reg, value float64) []Instr {
	bits := int64(math.Float64bits(value))
	return []Instr{
		&MovImm64{Dst: RegX9, Imm: bits},
		&FmovDFromX{Dst: dst, Src: RegX9},
	}
}

// loadFloatPlaceIntoReg materialises a Float-typed local into a
// d-register via `ldr d, [sp, #slot+offset]`. Uses the same
// projection machinery as scalars so future struct-of-Float lowerings
// keep working without special casing.
func (s *lowerState) loadFloatPlaceIntoReg(place mir.Place, dst Reg) ([]Instr, error) {
	slot, ok := s.localSlots[place.Local]
	if !ok {
		return nil, fmt.Errorf("%w: read of float local%d without slot", ErrUnsupportedShape, place.Local)
	}
	off, err := s.placeProjectionOffset(place)
	if err != nil {
		return nil, err
	}
	return []Instr{&LoadFloat64Stack{Dst: dst, Offset: slot + off}}, nil
}

func (s *lowerState) loadPlaceIntoReg(place mir.Place, dst Reg) ([]Instr, error) {
	slot, ok := s.localSlots[place.Local]
	if !ok {
		return nil, fmt.Errorf("%w: read of local%d without slot", ErrUnsupportedShape, place.Local)
	}
	off, err := s.placeProjectionOffset(place)
	if err != nil {
		return nil, err
	}
	return []Instr{&Load64Stack{Dst: dst, Offset: slot + off}}, nil
}

// placeProjectionOffset reduces a Place's projection chain to a single
// byte offset relative to the local's slot, but **only** when the
// chain stays inside the slot. Closure-env projections (`*` then a
// FieldProj on a ClosureEnv) need a runtime indirection: the slot
// holds a pointer, so the lowerer dereferences before reading. Such
// places are handled by `loadPlaceIntoReg` directly via
// `loadEnvProjection` — this helper returns ok=false to signal
// "this isn't a slot-local read" and the caller routes accordingly.
func (s *lowerState) placeProjectionOffset(place mir.Place) (int64, error) {
	if !place.HasProjections() {
		return 0, nil
	}
	loc := lookupLocal(s.fn, place.Local)
	if loc == nil {
		return 0, fmt.Errorf("%w: missing local%d for projection", ErrUnsupportedShape, place.Local)
	}
	currentType := loc.Type
	off := int64(0)
	for _, p := range place.Projections {
		switch proj := p.(type) {
		case *mir.FieldProj:
			fieldOff := s.fieldByteOffset(currentType, proj.Index)
			if fieldOff < 0 {
				return 0, fmt.Errorf("%w: field projection on non-struct %s", ErrUnsupportedShape, currentType)
			}
			off += fieldOff
			currentType = proj.Type
		case *mir.VariantProj:
			off += s.enumPayloadOffset(proj.FieldIdx)
			currentType = proj.Type
		default:
			return 0, fmt.Errorf("%w: projection %T", ErrUnsupportedShape, p)
		}
	}
	return off, nil
}

// hasEnvDerefProjection reports whether the place's first projection
// is a Deref on a closure-shaped pointer (ClosureEnv / FnType). Such
// places need to be loaded through a runtime indirection rather than
// summed to a static slot offset.
func hasEnvDerefProjection(s *lowerState, place mir.Place) bool {
	if len(place.Projections) == 0 {
		return false
	}
	if _, ok := place.Projections[0].(*mir.DerefProj); !ok {
		return false
	}
	loc := lookupLocal(s.fn, place.Local)
	if loc == nil {
		return false
	}
	return isClosureScalarType(loc.Type)
}

// loadEnvProjection lowers `<env>.*.{i}` reads — the lifted body's
// access to a closure capture or to the fn-pointer slot. The MIR
// uses TupleProj (or occasionally FieldProj) on the second leg of
// the chain; both index into the env layout. Layout:
//
//	field 0 → env+0     (the lifted fn pointer)
//	field i → env+24 + (i-1)*8   (the (i-1)-th capture)
//
// Trailing chains beyond two entries are unsupported; the lifted
// body never produces them today.
func (s *lowerState) loadEnvProjection(place mir.Place, dst Reg) ([]Instr, error) {
	if len(place.Projections) != 2 {
		return nil, fmt.Errorf("%w: env projection chain length %d", ErrUnsupportedShape, len(place.Projections))
	}
	idx, ok := envProjectionIndex(place.Projections[1])
	if !ok {
		return nil, fmt.Errorf("%w: env projection second entry %T", ErrUnsupportedShape, place.Projections[1])
	}
	envSlot, ok := s.localSlots[place.Local]
	if !ok {
		return nil, fmt.Errorf("%w: env local%d without slot", ErrUnsupportedShape, place.Local)
	}
	off := closureEnvFieldByteOffset(idx)
	return []Instr{
		&Load64Stack{Dst: RegX9, Offset: envSlot},
		&LoadFromReg{Dst: dst, Src: RegX9, Offset: off},
	}, nil
}

// envProjectionIndex extracts the MIR-level index from either a
// FieldProj or a TupleProj — the front end uses TupleProj for
// closure captures (the env captures array is conceptually a
// tuple), while struct-shaped envs would emit FieldProj. Both shapes
// reduce to the same env layout offset.
func envProjectionIndex(p mir.Projection) (int, bool) {
	switch v := p.(type) {
	case *mir.FieldProj:
		return v.Index, true
	case *mir.TupleProj:
		return v.Index, true
	}
	return 0, false
}

// closureEnvFieldByteOffset maps an MIR-level FieldProj index on a
// ClosureEnv pointee to the runtime layout offset. Index 0 is the
// fn-pointer slot (offset 0); index N>=1 is capture N-1 starting at
// `closureEnvCapturesOffset`.
func closureEnvFieldByteOffset(index int) int64 {
	if index <= 0 {
		return 0
	}
	return int64(closureEnvCapturesOffset) + int64(index-1)*8
}

func (s *lowerState) lowerIntrinsic(instr *mir.IntrinsicInstr) ([]Instr, error) {
	switch instr.Kind {
	case mir.IntrinsicPrintln:
		return s.lowerPrintln(instr)
	case mir.IntrinsicListPush:
		return s.lowerListPush(instr)
	case mir.IntrinsicListLen:
		return s.lowerListLen(instr)
	case mir.IntrinsicStringLen:
		return s.lowerStringLen(instr)
	case mir.IntrinsicStringIsEmpty:
		return s.lowerStringIsEmpty(instr)
	case mir.IntrinsicListIsEmpty:
		return s.lowerListIsEmpty(instr)
	case mir.IntrinsicOptionIsSome:
		return s.lowerOptionIsSome(instr)
	case mir.IntrinsicOptionIsNone:
		return s.lowerOptionIsNone(instr)
	default:
		return nil, fmt.Errorf("%w: intrinsic %s is outside phase 1", ErrUnsupportedShape, instr.Kind)
	}
}

// lowerStringIsEmpty composes `osty_rt_strings_ByteLen(s) == 0` and
// stores the bool result. The runtime returns the byte count in x0;
// `cmp x0, #0` + `cset Xd, eq` materialises the bool without going
// through a stack round-trip for the intermediate length.
func (s *lowerState) lowerStringIsEmpty(instr *mir.IntrinsicInstr) ([]Instr, error) {
	if len(instr.Args) != 1 {
		return nil, fmt.Errorf("%w: string_is_empty expects 1 arg", ErrUnsupportedShape)
	}
	receiver, err := s.materialiseOperand(instr.Args[0], RegX0)
	if err != nil {
		return nil, err
	}
	out := append([]Instr(nil), receiver...)
	out = append(out,
		&BranchLink{Symbol: runtimeSymStringByteLen},
		&MovImm64{Dst: RegX9, Imm: 0},
		&Cmp{Lhs: RegX0, Rhs: RegX9},
		&Cset{Dst: RegX9, Cond: CondEq},
	)
	if instr.Dest != nil && !instr.Dest.HasProjections() {
		if slot, ok := s.localSlots[instr.Dest.Local]; ok {
			out = append(out, &Store64Stack{Src: RegX9, Offset: slot})
		}
	}
	return out, nil
}

// lowerListIsEmpty composes `osty_rt_list_len(xs) == 0` similarly.
func (s *lowerState) lowerListIsEmpty(instr *mir.IntrinsicInstr) ([]Instr, error) {
	if len(instr.Args) != 1 {
		return nil, fmt.Errorf("%w: list_is_empty expects 1 arg", ErrUnsupportedShape)
	}
	receiver, err := s.materialiseOperand(instr.Args[0], RegX0)
	if err != nil {
		return nil, err
	}
	out := append([]Instr(nil), receiver...)
	out = append(out,
		&BranchLink{Symbol: runtimeSymListLen},
		&MovImm64{Dst: RegX9, Imm: 0},
		&Cmp{Lhs: RegX0, Rhs: RegX9},
		&Cset{Dst: RegX9, Cond: CondEq},
	)
	if instr.Dest != nil && !instr.Dest.HasProjections() {
		if slot, ok := s.localSlots[instr.Dest.Local]; ok {
			out = append(out, &Store64Stack{Src: RegX9, Offset: slot})
		}
	}
	return out, nil
}

// lowerOptionIsSome / lowerOptionIsNone read the option's
// discriminant (slot+0 of the enum local) and compare to the Some
// tag (= 1). The Option layout is the synthetic one from Week 14 so
// these always look at offset 0 regardless of the inner type.
func (s *lowerState) lowerOptionIsSome(instr *mir.IntrinsicInstr) ([]Instr, error) {
	return s.lowerOptionDiscriminantCompare(instr, CondEq)
}

func (s *lowerState) lowerOptionIsNone(instr *mir.IntrinsicInstr) ([]Instr, error) {
	return s.lowerOptionDiscriminantCompare(instr, CondNe)
}

// lowerOptionDiscriminantCompare emits the shared isSome/isNone body:
// load the discriminant byte (option slot + 0) into x9, compare to
// the Some tag (1), and `cset` with the caller-chosen condition.
// CondEq → isSome (x9 == 1); CondNe → isNone (x9 != 1).
func (s *lowerState) lowerOptionDiscriminantCompare(instr *mir.IntrinsicInstr, cond Cond) ([]Instr, error) {
	if len(instr.Args) != 1 {
		return nil, fmt.Errorf("%w: option discriminant compare expects 1 arg", ErrUnsupportedShape)
	}
	op := instr.Args[0]
	var place mir.Place
	switch o := op.(type) {
	case *mir.CopyOp:
		place = o.Place
	case *mir.MoveOp:
		place = o.Place
	default:
		return nil, fmt.Errorf("%w: option discriminant operand %T", ErrUnsupportedShape, op)
	}
	if place.HasProjections() {
		return nil, fmt.Errorf("%w: option discriminant on projected place", ErrUnsupportedShape)
	}
	srcSlot, ok := s.localSlots[place.Local]
	if !ok {
		return nil, fmt.Errorf("%w: option discriminant of local%d without slot", ErrUnsupportedShape, place.Local)
	}
	out := []Instr{
		&Load64Stack{Dst: RegX9, Offset: srcSlot},
		&MovImm64{Dst: RegX10, Imm: 1}, // Some tag
		&Cmp{Lhs: RegX9, Rhs: RegX10},
		&Cset{Dst: RegX9, Cond: cond},
	}
	if instr.Dest != nil && !instr.Dest.HasProjections() {
		if slot, ok := s.localSlots[instr.Dest.Local]; ok {
			out = append(out, &Store64Stack{Src: RegX9, Offset: slot})
		}
	}
	return out, nil
}

// lowerStringLen lowers `s.len()` on a String operand into a runtime
// call to `osty_rt_strings_ByteLen`. Receiver lives in x0; the
// runtime returns a byte count in x0 which we capture into the
// destination slot.
func (s *lowerState) lowerStringLen(instr *mir.IntrinsicInstr) ([]Instr, error) {
	if len(instr.Args) != 1 {
		return nil, fmt.Errorf("%w: string_len expects 1 arg, got %d", ErrUnsupportedShape, len(instr.Args))
	}
	receiver, err := s.materialiseOperand(instr.Args[0], RegX0)
	if err != nil {
		return nil, err
	}
	out := append([]Instr(nil), receiver...)
	out = append(out, &BranchLink{Symbol: runtimeSymStringByteLen})
	if instr.Dest != nil && !instr.Dest.HasProjections() {
		if slot, ok := s.localSlots[instr.Dest.Local]; ok {
			out = append(out, &Store64Stack{Src: RegX0, Offset: slot})
		}
	}
	return out, nil
}

// lowerListPush lowers `IntrinsicListPush(list, value)` into a runtime
// call. The runtime ships type-specific entry points for the four
// scalar element widths Osty lists encounter today — i64, i1, f64,
// and string-pointer — so we dispatch by the value operand's MIR
// type and route Float values through d0 (the AAPCS64 FP arg slot)
// instead of x1.
func (s *lowerState) lowerListPush(instr *mir.IntrinsicInstr) ([]Instr, error) {
	if len(instr.Args) != 2 {
		return nil, fmt.Errorf("%w: list_push expects 2 args, got %d", ErrUnsupportedShape, len(instr.Args))
	}
	list, err := s.materialiseOperand(instr.Args[0], RegX0)
	if err != nil {
		return nil, err
	}
	value := instr.Args[1]
	valueT := value.Type()
	out := append([]Instr(nil), list...)
	switch {
	case valueT == mir.TInt:
		mat, err := s.materialiseOperand(value, RegX1)
		if err != nil {
			return nil, err
		}
		out = append(out, mat...)
		out = append(out, &BranchLink{Symbol: runtimeSymListPushI64})
	case valueT == mir.TBool:
		mat, err := s.materialiseOperand(value, RegX1)
		if err != nil {
			return nil, err
		}
		out = append(out, mat...)
		out = append(out, &BranchLink{Symbol: runtimeSymListPushI1})
	case isFloatABIType(valueT):
		mat, err := s.materialiseFloatOperand(value, RegD0)
		if err != nil {
			return nil, err
		}
		out = append(out, mat...)
		out = append(out, &BranchLink{Symbol: runtimeSymListPushF64})
	case valueT == mir.TString:
		mat, err := s.materialiseOperand(value, RegX1)
		if err != nil {
			return nil, err
		}
		out = append(out, mat...)
		out = append(out, &BranchLink{Symbol: runtimeSymListPushString})
	default:
		return nil, fmt.Errorf("%w: list_push value type %s", ErrUnsupportedShape, valueT)
	}
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
	if mat, ok, err := s.loadFloatPrintArg(arg); err != nil || ok {
		if err != nil {
			return nil, err
		}
		// Darwin/aarch64 vararg ABI: every variadic argument lands
		// on the stack regardless of register class. We materialise
		// the Float64 in d9, bitcast it to x1 via `fmov`, then drop
		// it at [sp+0] alongside the format-string pointer in x0.
		// `%g` is the printf format that matches Osty's println for
		// floats so trailing zeros stay quiet (matches LLVM
		// backend's choice).
		label := s.addCString("%g\n")
		out := []Instr{&LoadCStringAddress{Dst: RegX0, Label: label}}
		out = append(out, mat...)
		out = append(out, &FmovXFromD{Dst: RegX1, Src: RegD9})
		if s.target.ObjectFormat == "mach-o" {
			out = append(out, &Store64Stack{Src: RegX1, Offset: 0})
		}
		out = append(out, &BranchLink{Symbol: "printf"})
		return out, nil
	}
	loadValue, ok, err := s.loadIntPrintArg(arg)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%w: println currently requires a string, int literal, Int local, String local, or Float local", ErrUnsupportedShape)
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

// loadFloatPrintArg materialises a Float-typed operand into d9 so the
// caller can finish the printf shuffle. Returns ok=false when the
// operand isn't a Float — the caller falls through to the int path.
func (s *lowerState) loadFloatPrintArg(op mir.Operand) ([]Instr, bool, error) {
	if !isFloatABIType(op.Type()) {
		return nil, false, nil
	}
	instrs, err := s.materialiseFloatOperand(op, RegD9)
	if err != nil {
		return nil, false, err
	}
	return instrs, true, nil
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
		// Indirect-call closures read the closure local through the
		// callee operand. Without surfacing it here, the slot
		// allocator drops the local and the lowerer fails with a
		// missing-slot error at the indirect-call site.
		if ind, ok := i.Callee.(*mir.IndirectCall); ok {
			collectOperandLocals(ind.Callee, out)
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
	case *mir.AggregateRV:
		// Each payload field is an operand; struct / enum / list /
		// tuple literals all flow through here. Without this, locals
		// captured into struct/enum literals would lose their slot
		// when no other instruction also reads them.
		for _, f := range r.Fields {
			collectOperandLocals(f, out)
		}
	case *mir.DiscriminantRV:
		// The scrutinee local is read by `discriminant _scrut` even
		// though the place itself isn't an Operand. Force it in so
		// the slot allocator reserves a slot for the enum value.
		out[r.Place.Local] = true
	case *mir.LenRV:
		// `len _list` reads the list local. Same reasoning as
		// DiscriminantRV — the place isn't an Operand, so we have
		// to surface the local explicitly.
		out[r.Place.Local] = true
	}
}

func collectOperandLocals(op mir.Operand, out map[mir.LocalID]bool) {
	switch o := op.(type) {
	case *mir.CopyOp:
		out[o.Place.Local] = true
		collectPlaceProjectionLocals(o.Place, out)
	case *mir.MoveOp:
		out[o.Place.Local] = true
		collectPlaceProjectionLocals(o.Place, out)
	}
}

// collectPlaceProjectionLocals walks a place's projection chain for
// IndexProj entries and records their index operand's locals — the
// index local has to live in a stack slot so the lowerer can load it
// into x1 before the runtime list_get_* call.
func collectPlaceProjectionLocals(place mir.Place, out map[mir.LocalID]bool) {
	for _, p := range place.Projections {
		ip, ok := p.(*mir.IndexProj)
		if !ok {
			continue
		}
		collectOperandLocals(ip.Index, out)
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
