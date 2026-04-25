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
	// Structural rewrite — runs once before the peephole fixed point so
	// the alias copy / new buffer local feed copy-prop / dead-assign
	// elimination on the same Optimize call.
	hoistNonEscapingSplitInLoops(fn)
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

// copyPropagateFn runs cross-block const propagation. The analysis is
// a standard forward dataflow: each block's entry state is the meet of
// its predecessors' exit states, where "meet" keeps a local binding
// iff every visited predecessor agrees on the same ConstOp value.
// Bindings that don't survive the meet fall off. A worklist drives the
// iteration to fixed point; loops and back-edges are handled the same
// way.
//
// Once the entry states stabilise, each block is walked linearly to
// substitute bare reads of const-bound locals with the constants. The
// per-block walk is the same as the previous single-block pass; the
// cross-block analysis just seeds it with a non-empty initial state
// when predecessors agree.
//
// Mutation events that invalidate a tracked binding (during the walk):
//   - An AssignInstr whose Dest.Local == N (with or without projections).
//   - A CallInstr / IntrinsicInstr whose Dest is non-nil and targets N.
//
// Returns true when at least one operand was rewritten.
func copyPropagateFn(fn *Function) bool {
	if fn == nil || len(fn.Blocks) == 0 {
		return false
	}
	entry, _ := computeBlockStates(fn)
	changed := false
	for i, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		seed := entry[i]
		if seed == nil {
			seed = map[LocalID]*ConstOp{}
		}
		if propagateBlockWithSeed(bb, seed) {
			changed = true
		}
	}
	return changed
}

// computeBlockStates runs the forward dataflow and returns (entryStates,
// exitStates). Each slice is indexed by BlockID. A nil entry means the
// block was never reached by the analysis (orphan / unreachable).
func computeBlockStates(fn *Function) ([]map[LocalID]*ConstOp, []map[LocalID]*ConstOp) {
	n := len(fn.Blocks)
	entryStates := make([]map[LocalID]*ConstOp, n)
	exitStates := make([]map[LocalID]*ConstOp, n)
	if int(fn.Entry) < 0 || int(fn.Entry) >= n {
		return entryStates, exitStates
	}
	preds := Predecessors(fn)

	entryStates[fn.Entry] = map[LocalID]*ConstOp{}
	worklist := []BlockID{fn.Entry}
	inQueue := make([]bool, n)
	inQueue[fn.Entry] = true

	for len(worklist) > 0 {
		id := worklist[len(worklist)-1]
		worklist = worklist[:len(worklist)-1]
		inQueue[id] = false
		bb := fn.Blocks[id]
		if bb == nil {
			continue
		}
		in := entryStates[id]
		out := transferBlock(bb, in)
		if statesEqual(exitStates[id], out) {
			continue
		}
		exitStates[id] = out
		for _, succ := range Successors(bb.Term) {
			if int(succ) < 0 || int(succ) >= n {
				continue
			}
			newIn := meetPredecessors(succ, preds[succ], exitStates, fn.Entry, entryStates)
			if statesEqual(entryStates[succ], newIn) {
				continue
			}
			entryStates[succ] = newIn
			if !inQueue[succ] {
				worklist = append(worklist, succ)
				inQueue[succ] = true
			}
		}
	}
	return entryStates, exitStates
}

// transferBlock replays the block's instructions starting from `in`
// and returns the exit state. Invalidates bindings on any write —
// including projected destinations and call / intrinsic results — and
// records new const-bindings from `_N = use ConstOp{...}`.
func transferBlock(bb *BasicBlock, in map[LocalID]*ConstOp) map[LocalID]*ConstOp {
	known := copyState(in)
	for _, instr := range bb.Instrs {
		switch x := instr.(type) {
		case *AssignInstr:
			if x.Dest.HasProjections() {
				delete(known, x.Dest.Local)
				continue
			}
			if use, ok := x.Src.(*UseRV); ok {
				if co, ok := use.Op.(*ConstOp); ok {
					known[x.Dest.Local] = co
					continue
				}
			}
			delete(known, x.Dest.Local)
		case *CallInstr:
			if x.Dest != nil {
				delete(known, x.Dest.Local)
			}
		case *IntrinsicInstr:
			if x.Dest != nil {
				delete(known, x.Dest.Local)
			}
		}
	}
	return known
}

// meetPredecessors computes the entry state of `succ` as the meet of
// every visited predecessor's exit state. When no predecessor has
// been visited, the function returns the current entry state (so the
// entry block's seeded {} survives).
func meetPredecessors(succ BlockID, preds []BlockID, exits []map[LocalID]*ConstOp, entryBlock BlockID, entries []map[LocalID]*ConstOp) map[LocalID]*ConstOp {
	var result map[LocalID]*ConstOp
	havePred := false
	for _, p := range preds {
		if int(p) < 0 || int(p) >= len(exits) {
			continue
		}
		out := exits[p]
		if out == nil {
			continue
		}
		if !havePred {
			result = copyState(out)
			havePred = true
			continue
		}
		result = meetStates(result, out)
	}
	if !havePred {
		// Block has no visited predecessor yet. Preserve any seeded
		// entry (e.g. fn.Entry's initial empty map) — dropping it would
		// de-initialise the seed.
		return entries[succ]
	}
	// Entry block is never refined by its successors' back-edges; keep
	// its seeded empty map.
	if succ == entryBlock {
		return map[LocalID]*ConstOp{}
	}
	return result
}

// meetStates keeps a binding iff both sides agree on the same ConstOp
// value. Missing keys drop out. Returns a fresh map — callers own it.
func meetStates(a, b map[LocalID]*ConstOp) map[LocalID]*ConstOp {
	out := map[LocalID]*ConstOp{}
	for k, v := range a {
		if w, ok := b[k]; ok && constOpEqual(v, w) {
			out[k] = v
		}
	}
	return out
}

// constOpEqual compares the Const value of two ConstOps structurally.
// Types are compared via their canonical String (both sides live in
// the same ir.Type vocabulary, which is canonicalised by
// monomorphisation).
func constOpEqual(a, b *ConstOp) bool {
	if a == nil || b == nil {
		return a == b
	}
	if typeString(a.T) != typeString(b.T) {
		return false
	}
	return constValueEqual(a.Const, b.Const)
}

func constValueEqual(a, b Const) bool {
	if a == nil || b == nil {
		return a == b
	}
	switch x := a.(type) {
	case *IntConst:
		y, ok := b.(*IntConst)
		return ok && x.Value == y.Value && typeString(x.Type()) == typeString(y.Type())
	case *BoolConst:
		y, ok := b.(*BoolConst)
		return ok && x.Value == y.Value
	case *FloatConst:
		y, ok := b.(*FloatConst)
		return ok && x.Value == y.Value && typeString(x.Type()) == typeString(y.Type())
	case *StringConst:
		y, ok := b.(*StringConst)
		return ok && x.Value == y.Value
	case *CharConst:
		y, ok := b.(*CharConst)
		return ok && x.Value == y.Value
	case *ByteConst:
		y, ok := b.(*ByteConst)
		return ok && x.Value == y.Value
	case *UnitConst:
		_, ok := b.(*UnitConst)
		return ok
	case *NullConst:
		y, ok := b.(*NullConst)
		return ok && typeString(x.Type()) == typeString(y.Type())
	case *FnConst:
		y, ok := b.(*FnConst)
		return ok && x.Symbol == y.Symbol
	}
	return false
}

// statesEqual reports whether two states contain the same bindings.
// nil is treated as distinct from an empty map so the worklist sees
// the first transition.
func statesEqual(a, b map[LocalID]*ConstOp) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		w, ok := b[k]
		if !ok {
			return false
		}
		if !constOpEqual(v, w) {
			return false
		}
	}
	return true
}

func copyState(in map[LocalID]*ConstOp) map[LocalID]*ConstOp {
	out := make(map[LocalID]*ConstOp, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// propagateBlockWithSeed runs the per-block propagation walk starting
// from the given initial set of const bindings. The seed is mutated
// in place so callers that want to preserve their copy must pass a
// clone.
func propagateBlockWithSeed(bb *BasicBlock, known map[LocalID]*ConstOp) bool {
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
	// After const prop, a branch / switch whose scrutinee is now a
	// literal can collapse into an unconditional goto. We only fold
	// BoolConst branches and IntConst switches — the only shapes the
	// lowerer emits. Non-matching types are left alone.
	if foldTerminator(bb) {
		changed = true
	}
	return changed
}

// foldTerminator rewrites a BranchTerm with a literal bool condition
// or a SwitchIntTerm with a literal integer scrutinee into a plain
// GotoTerm. Returns true on rewrite. Preserves span for diagnostics.
func foldTerminator(bb *BasicBlock) bool {
	switch t := bb.Term.(type) {
	case *BranchTerm:
		co, ok := t.Cond.(*ConstOp)
		if !ok {
			return false
		}
		bc, ok := co.Const.(*BoolConst)
		if !ok {
			return false
		}
		target := t.Else
		if bc.Value {
			target = t.Then
		}
		bb.Term = &GotoTerm{Target: target, SpanV: t.SpanV}
		return true
	case *SwitchIntTerm:
		co, ok := t.Scrutinee.(*ConstOp)
		if !ok {
			return false
		}
		ic, ok := co.Const.(*IntConst)
		if !ok {
			return false
		}
		target := t.Default
		for _, c := range t.Cases {
			if c.Value == ic.Value {
				target = c.Target
				break
			}
		}
		bb.Term = &GotoTerm{Target: target, SpanV: t.SpanV}
		return true
	}
	return false
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
// Returns whether any operand was changed. Place-carrying rvalues are
// walked via rewritePlaceProjections so index operands embedded in
// projection chains still get substituted.
func rewriteRValue(rv RValue, rewrite func(*Operand)) bool {
	before := newChangeCounter()
	record := func(op *Operand) {
		old := *op
		rewrite(op)
		if *op != old {
			before.n++
		}
	}
	recordPlace := func(p *Place) {
		for i := range p.Projections {
			if ip, ok := p.Projections[i].(*IndexProj); ok {
				record(&ip.Index)
			}
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
	case *DiscriminantRV:
		recordPlace(&x.Place)
	case *LenRV:
		recordPlace(&x.Place)
	case *AddressOfRV:
		recordPlace(&x.Place)
	case *RefRV:
		recordPlace(&x.Place)
	case *NullaryRV, *GlobalRefRV:
		// nothing to rewrite.
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
// The pass also nils the Dest pointer of Call / Intrinsic instructions
// whose captured return value is dead: the call still executes (its
// side effects matter) but the dead write drops away, which in turn
// feeds the next iteration of read counting. Finally, Storage{Live,
// Dead} markers on fully-dead locals are dropped since they serve no
// downstream purpose once the local has no writes or reads.
//
// Returns true when at least one instruction was removed or rewritten.
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
			switch x := instr.(type) {
			case *AssignInstr:
				if !x.Dest.HasProjections() && localIsDead(fn, x.Dest.Local, reads) {
					drop = true
				}
			case *CallInstr:
				if x.Dest != nil && !x.Dest.HasProjections() && localIsDead(fn, x.Dest.Local, reads) {
					x.Dest = nil
					changed = true
				}
			case *IntrinsicInstr:
				if x.Dest != nil && !x.Dest.HasProjections() && localIsDead(fn, x.Dest.Local, reads) {
					x.Dest = nil
					changed = true
				}
			case *StorageLiveInstr:
				if localIsDead(fn, x.Local, reads) {
					drop = true
				}
			case *StorageDeadInstr:
				if localIsDead(fn, x.Local, reads) {
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
