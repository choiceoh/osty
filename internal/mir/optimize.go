package mir

// Optimize runs MIR's in-place peephole passes on a module to a fixed
// point and returns the module unchanged. The passes are conservative:
//
//   - Const propagation within a single basic block. When `_N = use
//     ConstOp{...}` appears and `_N` has no further writes before a
//     read, the read operand is rewritten to the constant directly.
//     Never crosses a block boundary — we are not in SSA form, so a
//     local can be reassigned between predecessors without us seeing
//     it.
//   - Dead-assign elimination. Any `AssignInstr` whose destination is
//     a bare local (no projections), not a parameter, not the return
//     slot, and never read anywhere in the function is dropped. Every
//     RValue is pure so the drop is side-effect-safe.
//   - Empty-block collapse. When a block has no instructions and its
//     terminator is a `GotoTerm{C}`, every edge in the CFG that
//     targeted that block is retargeted to `C` directly. Runs until no
//     more edges can be shortened.
//
// The passes preserve validator invariants: LocalIDs and BlockIDs stay
// put, the Entry block is retained, no instruction gains side effects.
// Orphan blocks left behind by collapse are safe — `Validate` already
// tolerates them because `mir.Lower` emits them for unreachable code.
//
// Optimize is idempotent: a second call on the same module is a
// no-op modulo the fixed-point iteration cost.
func Optimize(m *Module) *Module {
	if m == nil {
		return nil
	}
	for _, fn := range m.Functions {
		optimizeFunction(fn)
	}
	for _, g := range m.Globals {
		if g != nil && g.Init != nil {
			optimizeFunction(g.Init)
		}
	}
	return m
}

// optimizeFunction runs all passes on a single function to a fixed
// point. Each pass returns true when it mutated the function; we loop
// while any pass reports progress, bounded to avoid pathological
// cases (there aren't any today, but the bound keeps unexpected
// mutual-progress chains from running indefinitely).
func optimizeFunction(fn *Function) {
	if fn == nil || fn.IsExternal || fn.IsIntrinsic {
		return
	}
	const maxIters = 16
	for i := 0; i < maxIters; i++ {
		changed := false
		if copyPropagateFn(fn) {
			changed = true
		}
		if collapseEmptyGotos(fn) {
			changed = true
		}
		if deadAssignElim(fn) {
			changed = true
		}
		if !changed {
			return
		}
	}
}

// ==== copy propagation ====

// copyPropagateFn runs per-block const propagation. Within each block,
// bare writes of the form `_N = use ConstOp{...}` populate a map; bare
// reads of `_N` are rewritten to the constant until `_N` is mutated.
//
// Mutation events that invalidate a tracked binding:
//   - An AssignInstr whose Dest.Local == N (with or without projections).
//   - A CallInstr / IntrinsicInstr whose Dest is non-nil and targets N.
//
// Returns true when at least one operand was rewritten.
func copyPropagateFn(fn *Function) bool {
	changed := false
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		if propagateBlock(bb) {
			changed = true
		}
	}
	return changed
}

func propagateBlock(bb *BasicBlock) bool {
	known := map[LocalID]*ConstOp{}
	changed := false
	rewrite := func(op *Operand) {
		if *op == nil {
			return
		}
		if co := substituteConst(*op, known); co != nil {
			*op = co
			changed = true
		}
	}
	rewriteOps := func(ops []Operand) {
		for i := range ops {
			rewrite(&ops[i])
		}
	}
	rewritePlaceProjections := func(p *Place) {
		for i := range p.Projections {
			if ip, ok := p.Projections[i].(*IndexProj); ok {
				rewrite(&ip.Index)
			}
		}
	}

	for _, instr := range bb.Instrs {
		switch x := instr.(type) {
		case *AssignInstr:
			// Reads in the RHS see bindings established so far.
			rewritePlaceProjections(&x.Dest)
			if x.Src != nil {
				if rewriteRValue(x.Src, rewrite) {
					changed = true
				}
			}
			// Track or invalidate.
			if x.Dest.HasProjections() {
				// Projection write modifies part of the destination local;
				// the whole local's known-const binding is no longer safe.
				delete(known, x.Dest.Local)
				continue
			}
			// Bare write: record when RHS is a Use of a constant.
			if use, ok := x.Src.(*UseRV); ok {
				if co, ok := use.Op.(*ConstOp); ok {
					known[x.Dest.Local] = co
					continue
				}
			}
			delete(known, x.Dest.Local)

		case *CallInstr:
			rewriteOps(x.Args)
			if ind, ok := x.Callee.(*IndirectCall); ok {
				rewrite(&ind.Callee)
			}
			if x.Dest != nil {
				rewritePlaceProjections(x.Dest)
				delete(known, x.Dest.Local)
			}

		case *IntrinsicInstr:
			rewriteOps(x.Args)
			if x.Dest != nil {
				rewritePlaceProjections(x.Dest)
				delete(known, x.Dest.Local)
			}
		}
	}

	// Terminator operands may also benefit from const propagation.
	switch t := bb.Term.(type) {
	case *BranchTerm:
		if co := substituteConst(t.Cond, known); co != nil {
			t.Cond = co
			changed = true
		}
	case *SwitchIntTerm:
		if co := substituteConst(t.Scrutinee, known); co != nil {
			t.Scrutinee = co
			changed = true
		}
	}
	return changed
}

// substituteConst returns a ConstOp replacement for op when it is a
// bare read (Copy/Move with no projections) of a local currently bound
// to a constant. Returns nil when no substitution applies.
func substituteConst(op Operand, known map[LocalID]*ConstOp) *ConstOp {
	if op == nil || len(known) == 0 {
		return nil
	}
	var p Place
	switch x := op.(type) {
	case *CopyOp:
		p = x.Place
	case *MoveOp:
		p = x.Place
	default:
		return nil
	}
	if len(p.Projections) != 0 {
		return nil
	}
	co, ok := known[p.Local]
	if !ok {
		return nil
	}
	// Return a fresh ConstOp each time so callers may mutate it safely.
	return &ConstOp{Const: co.Const, T: co.T}
}

// rewriteRValue walks an RValue's operand fields and applies rewrite.
// Returns whether any operand was changed.
func rewriteRValue(rv RValue, rewrite func(*Operand)) bool {
	before := newChangeCounter()
	record := func(op *Operand) {
		old := *op
		rewrite(op)
		if *op != old {
			before.n++
		}
	}
	switch x := rv.(type) {
	case *UseRV:
		record(&x.Op)
	case *UnaryRV:
		record(&x.Arg)
	case *BinaryRV:
		record(&x.Left)
		record(&x.Right)
	case *AggregateRV:
		for i := range x.Fields {
			record(&x.Fields[i])
		}
	case *CastRV:
		record(&x.Arg)
	case *DiscriminantRV, *LenRV, *AddressOfRV, *RefRV, *NullaryRV, *GlobalRefRV:
		// these take a Place directly or nothing — nothing to rewrite.
	}
	return before.n > 0
}

type changeCounter struct{ n int }

func newChangeCounter() *changeCounter { return &changeCounter{} }

// ==== dead-assign elimination ====

// deadAssignElim drops AssignInstrs whose bare destination local is
// never read anywhere in the function and is neither a parameter nor
// the return slot. Because all RValues are side-effect free, dropping
// such an assignment preserves observable behaviour.
//
// Returns true when at least one instruction was removed.
func deadAssignElim(fn *Function) bool {
	reads := collectLocalReads(fn)
	changed := false
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		out := bb.Instrs[:0]
		for _, instr := range bb.Instrs {
			drop := false
			if a, ok := instr.(*AssignInstr); ok && !a.Dest.HasProjections() {
				if localIsDead(fn, a.Dest.Local, reads) {
					drop = true
				}
			}
			if !drop {
				out = append(out, instr)
				continue
			}
			changed = true
		}
		bb.Instrs = out
	}
	return changed
}

// localIsDead reports whether the local can be safely dropped: not a
// parameter, not the return slot, and read zero times.
func localIsDead(fn *Function, id LocalID, reads map[LocalID]int) bool {
	if int(id) < 0 || int(id) >= len(fn.Locals) {
		return false
	}
	loc := fn.Locals[id]
	if loc == nil || loc.IsParam || loc.IsReturn {
		return false
	}
	return reads[id] == 0
}

// collectLocalReads scans every read site in a function and returns a
// count per LocalID. A "read" includes: any operand resolving to
// Copy/Move on a place whose root local is N; any IndexProj.Index
// operand that references N; any projection-write whose base is N
// (load-modify-store); and the implicit ReturnLocal read by ReturnTerm.
func collectLocalReads(fn *Function) map[LocalID]int {
	reads := map[LocalID]int{}
	countOp := func(op Operand) {
		countOperandReads(op, reads)
	}
	countPlace := func(p Place, dest bool) {
		// A Place used for a *bare* write (no projections) does not read
		// the local. A projection write reads the local to load-modify-
		// store, so it counts as a read.
		if dest && !p.HasProjections() {
			return
		}
		reads[p.Local]++
		for _, proj := range p.Projections {
			if ip, ok := proj.(*IndexProj); ok {
				countOp(ip.Index)
			}
		}
	}
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		for _, instr := range bb.Instrs {
			switch x := instr.(type) {
			case *AssignInstr:
				countPlace(x.Dest, true)
				countRValueReads(x.Src, countOp, reads)
			case *CallInstr:
				if x.Dest != nil {
					countPlace(*x.Dest, true)
				}
				if ind, ok := x.Callee.(*IndirectCall); ok {
					countOp(ind.Callee)
				}
				for _, op := range x.Args {
					countOp(op)
				}
			case *IntrinsicInstr:
				if x.Dest != nil {
					countPlace(*x.Dest, true)
				}
				for _, op := range x.Args {
					countOp(op)
				}
			}
		}
		switch t := bb.Term.(type) {
		case *BranchTerm:
			countOp(t.Cond)
		case *SwitchIntTerm:
			countOp(t.Scrutinee)
		case *ReturnTerm:
			if int(fn.ReturnLocal) >= 0 && int(fn.ReturnLocal) < len(fn.Locals) {
				reads[fn.ReturnLocal]++
			}
		}
	}
	return reads
}

func countOperandReads(op Operand, reads map[LocalID]int) {
	if op == nil {
		return
	}
	switch x := op.(type) {
	case *CopyOp:
		countReadPlace(x.Place, reads)
	case *MoveOp:
		countReadPlace(x.Place, reads)
	}
}

// countReadPlace counts a read of the base local plus any index
// operands embedded in the projection chain.
func countReadPlace(p Place, reads map[LocalID]int) {
	reads[p.Local]++
	for _, proj := range p.Projections {
		if ip, ok := proj.(*IndexProj); ok {
			countOperandReads(ip.Index, reads)
		}
	}
}

func countRValueReads(rv RValue, countOp func(Operand), reads map[LocalID]int) {
	switch x := rv.(type) {
	case *UseRV:
		countOp(x.Op)
	case *UnaryRV:
		countOp(x.Arg)
	case *BinaryRV:
		countOp(x.Left)
		countOp(x.Right)
	case *AggregateRV:
		for _, op := range x.Fields {
			countOp(op)
		}
	case *CastRV:
		countOp(x.Arg)
	case *DiscriminantRV:
		countReadPlace(x.Place, reads)
	case *LenRV:
		countReadPlace(x.Place, reads)
	case *RefRV:
		countReadPlace(x.Place, reads)
	case *AddressOfRV:
		countReadPlace(x.Place, reads)
	}
}

// ==== empty-block collapse ====

// collapseEmptyGotos retargets every control-flow edge whose target is
// an empty `GotoTerm{C}` block to `C` directly. Runs iteratively: if
// the new target is itself an empty-goto, we keep walking. Cycles of
// empty-goto-only blocks (which the lowerer never emits) are detected
// by a seen-set and left alone.
//
// Returns true when at least one edge was shortened.
func collapseEmptyGotos(fn *Function) bool {
	resolve := func(target BlockID) BlockID {
		seen := map[BlockID]bool{}
		for {
			if int(target) < 0 || int(target) >= len(fn.Blocks) {
				return target
			}
			if seen[target] {
				return target
			}
			bb := fn.Blocks[target]
			if bb == nil || len(bb.Instrs) != 0 {
				return target
			}
			gt, ok := bb.Term.(*GotoTerm)
			if !ok {
				return target
			}
			seen[target] = true
			target = gt.Target
		}
	}

	changed := false
	retarget := func(p *BlockID) {
		next := resolve(*p)
		if next != *p {
			*p = next
			changed = true
		}
	}

	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		switch t := bb.Term.(type) {
		case *GotoTerm:
			retarget(&t.Target)
		case *BranchTerm:
			retarget(&t.Then)
			retarget(&t.Else)
		case *SwitchIntTerm:
			for i := range t.Cases {
				retarget(&t.Cases[i].Target)
			}
			retarget(&t.Default)
		}
	}
	// fn.Entry itself may be an empty-goto block. Shorten it so the
	// function's advertised entry matches what the edges see.
	retargetEntry := resolve(fn.Entry)
	if retargetEntry != fn.Entry {
		fn.Entry = retargetEntry
		changed = true
	}
	return changed
}
