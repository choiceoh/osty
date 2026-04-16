package mir

import (
	"fmt"
)

// Validate walks a MIR Module and returns a slice of structural
// errors. An empty slice means the module passes every invariant the
// MIR layer enforces; a non-empty slice means the lowerer has a bug
// (not that the user's program is wrong — MIR invariants are internal
// contracts).
//
// The validator is deliberately light-weight: it catches shape bugs
// that would otherwise surface as an LLVM verifier failure or worse,
// a silently-miscompiled program. It does NOT redo type checking.
//
// Invariants checked:
//
//   - Module is non-nil and carries a package name.
//   - Every function has a non-empty block list when not external, an
//     entry block whose ID is in range, and every block has a
//     terminator.
//   - No instruction follows a terminator inside a block.
//   - Every Place references a local that exists and every projection
//     carries a non-nil Type.
//   - Every Operand carries a non-nil Type.
//   - Every RValue's inputs carry types.
//   - Terminator successors reference existing blocks.
//   - No HIR-only node leaked into MIR: no ir.Pattern, no ir.MatchExpr,
//     etc. (The design guarantees this at the type level by not
//     importing the HIR node types directly; the validator's job is
//     to catch the shape invariants within MIR itself.)
func Validate(m *Module) []error {
	v := &validator{}
	v.validateModule(m)
	return v.errs
}

type validator struct {
	errs []error
	fn   *Function // current function context
}

func (v *validator) addf(format string, args ...any) {
	v.errs = append(v.errs, fmt.Errorf(format, args...))
}

func (v *validator) validateModule(m *Module) {
	if m == nil {
		v.addf("module: nil")
		return
	}
	if m.Package == "" {
		v.addf("module: empty package")
	}
	names := map[string]bool{}
	for i, fn := range m.Functions {
		if fn == nil {
			v.addf("module.Functions[%d]: nil", i)
			continue
		}
		if fn.Name == "" {
			v.addf("function[%d]: empty name", i)
		}
		if names[fn.Name] {
			v.addf("function %q: duplicate symbol", fn.Name)
		}
		names[fn.Name] = true
		v.validateFunction(fn)
	}
	for i, g := range m.Globals {
		if g == nil {
			v.addf("module.Globals[%d]: nil", i)
			continue
		}
		v.validateGlobal(g)
	}
}

func (v *validator) validateGlobal(g *Global) {
	if g.Name == "" {
		v.addf("global: empty name")
	}
	if g.Type == nil {
		v.addf("global %q: nil Type", g.Name)
	}
	if g.Init != nil {
		v.validateFunction(g.Init)
	}
}

func (v *validator) validateFunction(fn *Function) {
	prev := v.fn
	v.fn = fn
	defer func() { v.fn = prev }()

	if fn.ReturnType == nil {
		v.addf("function %q: nil ReturnType", fn.Name)
	}

	// Return local exists and has the declared return type (when the
	// function has a body). External / intrinsic functions may carry an
	// empty locals list.
	if !fn.IsExternal && !fn.IsIntrinsic {
		if int(fn.ReturnLocal) < 0 || int(fn.ReturnLocal) >= len(fn.Locals) {
			v.addf("function %q: ReturnLocal %d out of range (locals=%d)", fn.Name, fn.ReturnLocal, len(fn.Locals))
		} else {
			ret := fn.Locals[fn.ReturnLocal]
			if ret == nil {
				v.addf("function %q: ReturnLocal %d is nil", fn.Name, fn.ReturnLocal)
			} else if !ret.IsReturn {
				v.addf("function %q: ReturnLocal %d is not marked IsReturn", fn.Name, fn.ReturnLocal)
			}
		}
	}

	// Parameters reference existing locals and are flagged as params.
	for i, pid := range fn.Params {
		if int(pid) < 0 || int(pid) >= len(fn.Locals) {
			v.addf("function %q: Params[%d]=%d out of range (locals=%d)", fn.Name, i, pid, len(fn.Locals))
			continue
		}
		loc := fn.Locals[pid]
		if loc == nil {
			v.addf("function %q: Params[%d]=%d is nil", fn.Name, i, pid)
			continue
		}
		if !loc.IsParam {
			v.addf("function %q: Params[%d]=_%d is not marked IsParam", fn.Name, i, pid)
		}
	}

	// Locals carry types and unique IDs.
	for i, loc := range fn.Locals {
		if loc == nil {
			v.addf("function %q: Locals[%d]: nil", fn.Name, i)
			continue
		}
		if int(loc.ID) != i {
			v.addf("function %q: Locals[%d].ID = %d (mismatch)", fn.Name, i, loc.ID)
		}
		if loc.Type == nil {
			v.addf("function %q: Locals[%d]=_%d nil Type", fn.Name, i, loc.ID)
		}
	}

	if fn.IsExternal || fn.IsIntrinsic {
		if len(fn.Blocks) != 0 {
			v.addf("function %q: external/intrinsic must have no blocks", fn.Name)
		}
		return
	}

	if len(fn.Blocks) == 0 {
		v.addf("function %q: no blocks", fn.Name)
		return
	}
	if int(fn.Entry) < 0 || int(fn.Entry) >= len(fn.Blocks) {
		v.addf("function %q: Entry=%d out of range (blocks=%d)", fn.Name, fn.Entry, len(fn.Blocks))
	}

	for i, bb := range fn.Blocks {
		if bb == nil {
			v.addf("function %q: Blocks[%d]: nil", fn.Name, i)
			continue
		}
		if int(bb.ID) != i {
			v.addf("function %q: Blocks[%d].ID = %d (mismatch)", fn.Name, i, bb.ID)
		}
		v.validateBlock(fn, bb)
	}
}

func (v *validator) validateBlock(fn *Function, bb *BasicBlock) {
	for i, instr := range bb.Instrs {
		if instr == nil {
			v.addf("function %q bb%d: instruction[%d] nil", fn.Name, bb.ID, i)
			continue
		}
		v.validateInstr(fn, bb, i, instr)
	}
	if bb.Term == nil {
		v.addf("function %q bb%d: missing terminator", fn.Name, bb.ID)
		return
	}
	v.validateTerm(fn, bb, bb.Term)
}

func (v *validator) validateInstr(fn *Function, bb *BasicBlock, idx int, instr Instr) {
	switch x := instr.(type) {
	case *AssignInstr:
		v.validatePlace(fn, bb, x.Dest, "AssignInstr.Dest")
		if x.Src == nil {
			v.addf("function %q bb%d instr[%d]: AssignInstr nil Src", fn.Name, bb.ID, idx)
			return
		}
		v.validateRValue(fn, bb, x.Src)
	case *CallInstr:
		if x.Dest != nil {
			v.validatePlace(fn, bb, *x.Dest, "CallInstr.Dest")
		}
		v.validateCallee(fn, bb, x.Callee)
		for i, op := range x.Args {
			v.validateOperand(fn, bb, op, fmt.Sprintf("CallInstr.Args[%d]", i))
		}
	case *IntrinsicInstr:
		if x.Dest != nil {
			v.validatePlace(fn, bb, *x.Dest, "IntrinsicInstr.Dest")
		}
		if x.Kind == IntrinsicInvalid {
			v.addf("function %q bb%d instr[%d]: IntrinsicInvalid", fn.Name, bb.ID, idx)
		}
		for i, op := range x.Args {
			v.validateOperand(fn, bb, op, fmt.Sprintf("IntrinsicInstr.Args[%d]", i))
		}
	case *StorageLiveInstr:
		if int(x.Local) < 0 || int(x.Local) >= len(fn.Locals) {
			v.addf("function %q bb%d instr[%d]: StorageLive _%d out of range", fn.Name, bb.ID, idx, x.Local)
		}
	case *StorageDeadInstr:
		if int(x.Local) < 0 || int(x.Local) >= len(fn.Locals) {
			v.addf("function %q bb%d instr[%d]: StorageDead _%d out of range", fn.Name, bb.ID, idx, x.Local)
		}
	default:
		v.addf("function %q bb%d instr[%d]: unknown instruction %T", fn.Name, bb.ID, idx, instr)
	}
}

func (v *validator) validateCallee(fn *Function, bb *BasicBlock, c Callee) {
	switch x := c.(type) {
	case nil:
		v.addf("function %q bb%d: CallInstr nil callee", fn.Name, bb.ID)
	case *FnRef:
		if x.Symbol == "" {
			v.addf("function %q bb%d: FnRef empty symbol", fn.Name, bb.ID)
		}
	case *IndirectCall:
		if x.Callee == nil {
			v.addf("function %q bb%d: IndirectCall nil operand", fn.Name, bb.ID)
			return
		}
		v.validateOperand(fn, bb, x.Callee, "IndirectCall.Callee")
	default:
		v.addf("function %q bb%d: unknown callee %T", fn.Name, bb.ID, c)
	}
}

func (v *validator) validateRValue(fn *Function, bb *BasicBlock, rv RValue) {
	switch x := rv.(type) {
	case *UseRV:
		v.validateOperand(fn, bb, x.Op, "UseRV")
	case *UnaryRV:
		if x.T == nil {
			v.addf("function %q bb%d: UnaryRV nil Type", fn.Name, bb.ID)
		}
		v.validateOperand(fn, bb, x.Arg, "UnaryRV.Arg")
	case *BinaryRV:
		if x.T == nil {
			v.addf("function %q bb%d: BinaryRV nil Type", fn.Name, bb.ID)
		}
		v.validateOperand(fn, bb, x.Left, "BinaryRV.Left")
		v.validateOperand(fn, bb, x.Right, "BinaryRV.Right")
	case *AggregateRV:
		if x.T == nil {
			v.addf("function %q bb%d: AggregateRV nil Type", fn.Name, bb.ID)
		}
		if x.Kind == 0 {
			v.addf("function %q bb%d: AggregateRV zero Kind", fn.Name, bb.ID)
		}
		for i, op := range x.Fields {
			v.validateOperand(fn, bb, op, fmt.Sprintf("AggregateRV.Fields[%d]", i))
		}
	case *DiscriminantRV:
		v.validatePlace(fn, bb, x.Place, "DiscriminantRV.Place")
		if x.T == nil {
			v.addf("function %q bb%d: DiscriminantRV nil Type", fn.Name, bb.ID)
		}
	case *LenRV:
		v.validatePlace(fn, bb, x.Place, "LenRV.Place")
		if x.T == nil {
			v.addf("function %q bb%d: LenRV nil Type", fn.Name, bb.ID)
		}
	case *CastRV:
		v.validateOperand(fn, bb, x.Arg, "CastRV.Arg")
		if x.From == nil || x.To == nil {
			v.addf("function %q bb%d: CastRV nil From/To", fn.Name, bb.ID)
		}
	case *AddressOfRV:
		v.validatePlace(fn, bb, x.Place, "AddressOfRV.Place")
	case *RefRV:
		v.validatePlace(fn, bb, x.Place, "RefRV.Place")
	case *NullaryRV:
		if x.Kind == 0 {
			v.addf("function %q bb%d: NullaryRV zero Kind", fn.Name, bb.ID)
		}
		if x.T == nil {
			v.addf("function %q bb%d: NullaryRV nil Type", fn.Name, bb.ID)
		}
	case *GlobalRefRV:
		if x.Name == "" {
			v.addf("function %q bb%d: GlobalRefRV empty Name", fn.Name, bb.ID)
		}
		if x.T == nil {
			v.addf("function %q bb%d: GlobalRefRV nil Type", fn.Name, bb.ID)
		}
	default:
		v.addf("function %q bb%d: unknown rvalue %T", fn.Name, bb.ID, rv)
	}
}

func (v *validator) validateOperand(fn *Function, bb *BasicBlock, op Operand, ctx string) {
	if op == nil {
		v.addf("function %q bb%d %s: nil operand", fn.Name, bb.ID, ctx)
		return
	}
	if op.Type() == nil {
		v.addf("function %q bb%d %s: operand has nil Type", fn.Name, bb.ID, ctx)
	}
	switch x := op.(type) {
	case *CopyOp:
		v.validatePlace(fn, bb, x.Place, ctx+".Place")
	case *MoveOp:
		v.validatePlace(fn, bb, x.Place, ctx+".Place")
	case *ConstOp:
		if x.Const == nil {
			v.addf("function %q bb%d %s: ConstOp nil Const", fn.Name, bb.ID, ctx)
		}
	default:
		v.addf("function %q bb%d %s: unknown operand %T", fn.Name, bb.ID, ctx, op)
	}
}

func (v *validator) validatePlace(fn *Function, bb *BasicBlock, p Place, ctx string) {
	if int(p.Local) < 0 || int(p.Local) >= len(fn.Locals) {
		v.addf("function %q bb%d %s: local _%d out of range", fn.Name, bb.ID, ctx, p.Local)
		return
	}
	if fn.Locals[p.Local] == nil {
		v.addf("function %q bb%d %s: local _%d is nil", fn.Name, bb.ID, ctx, p.Local)
		return
	}
	for i, proj := range p.Projections {
		v.validateProjection(fn, bb, proj, fmt.Sprintf("%s.Projections[%d]", ctx, i))
	}
}

func (v *validator) validateProjection(fn *Function, bb *BasicBlock, proj Projection, ctx string) {
	if proj == nil {
		v.addf("function %q bb%d %s: nil projection", fn.Name, bb.ID, ctx)
		return
	}
	switch x := proj.(type) {
	case *FieldProj:
		if x.Type == nil {
			v.addf("function %q bb%d %s: FieldProj nil Type", fn.Name, bb.ID, ctx)
		}
		if x.Index < 0 {
			v.addf("function %q bb%d %s: FieldProj negative index %d", fn.Name, bb.ID, ctx, x.Index)
		}
	case *TupleProj:
		if x.Type == nil {
			v.addf("function %q bb%d %s: TupleProj nil Type", fn.Name, bb.ID, ctx)
		}
		if x.Index < 0 {
			v.addf("function %q bb%d %s: TupleProj negative index %d", fn.Name, bb.ID, ctx, x.Index)
		}
	case *VariantProj:
		if x.Type == nil {
			v.addf("function %q bb%d %s: VariantProj nil Type", fn.Name, bb.ID, ctx)
		}
	case *IndexProj:
		if x.ElemType == nil {
			v.addf("function %q bb%d %s: IndexProj nil ElemType", fn.Name, bb.ID, ctx)
		}
		v.validateOperand(fn, bb, x.Index, ctx+".Index")
	case *DerefProj:
		if x.Type == nil {
			v.addf("function %q bb%d %s: DerefProj nil Type", fn.Name, bb.ID, ctx)
		}
	default:
		v.addf("function %q bb%d %s: unknown projection %T", fn.Name, bb.ID, ctx, proj)
	}
}

func (v *validator) validateTerm(fn *Function, bb *BasicBlock, t Terminator) {
	switch x := t.(type) {
	case *GotoTerm:
		v.validateBlockRef(fn, bb, x.Target, "Goto.Target")
	case *BranchTerm:
		v.validateOperand(fn, bb, x.Cond, "Branch.Cond")
		v.validateBlockRef(fn, bb, x.Then, "Branch.Then")
		v.validateBlockRef(fn, bb, x.Else, "Branch.Else")
	case *SwitchIntTerm:
		v.validateOperand(fn, bb, x.Scrutinee, "SwitchInt.Scrutinee")
		for i, c := range x.Cases {
			v.validateBlockRef(fn, bb, c.Target, fmt.Sprintf("SwitchInt.Cases[%d]", i))
		}
		v.validateBlockRef(fn, bb, x.Default, "SwitchInt.Default")
	case *ReturnTerm, *UnreachableTerm:
		// leaf — no successor refs.
	default:
		v.addf("function %q bb%d: unknown terminator %T", fn.Name, bb.ID, t)
	}
}

func (v *validator) validateBlockRef(fn *Function, bb *BasicBlock, target BlockID, ctx string) {
	if int(target) < 0 || int(target) >= len(fn.Blocks) {
		v.addf("function %q bb%d %s: target bb%d out of range (blocks=%d)", fn.Name, bb.ID, ctx, target, len(fn.Blocks))
	}
}
