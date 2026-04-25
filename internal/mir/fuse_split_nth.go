package mir

// fuseNonEscapingSplitNth detects the `expr.split(sep)[K]` pattern and
// replaces it with a single `IntrinsicStringNthSegment` call. The full
// `Split` materialises a `List<String>` with N pieces; when only one
// constant index is consumed, the list itself is dead — eliminate it
// and dup just the segment we care about.
//
// Pattern (markdown-stats `classify(line)` shape):
//
//	let parts = value.split(sep)        // IntrinsicStringSplit, dest=L
//	let first = parts[0]                // AssignInstr with IndexProj on L
//	// `parts` otherwise unused
//
// After the transform:
//
//	let first = NthSegment(value, sep, 0)   // IntrinsicStringNthSegment, dest=M
//	// list-typed local L is dead; deadAssignElim removes the
//	// now-unreachable Split call and storage markers.
//
// Safety conditions (all must hold):
//   - The split call's Dest local L has a single IntrinsicStringSplit
//     write and no other writes (same shape `splitDestEscapeSafe`
//     enforces for the loop variant).
//   - L's only read produces an IndexProj with a non-negative i64
//     IntConst index (no `parts.len()`, no `for x in parts`, no second
//     `parts[i]`). Other read shapes leave the unfused Split path
//     intact — the heuristic isn't trying to be exhaustive.
//   - The index read happens in an AssignInstr whose RValue is
//     `UseRV{Op: CopyOp{Place: parts[K]}}` — the canonical lowering
//     for `let first = parts[K]`. Other RValue shapes on top of an
//     IndexProj (e.g. `BinaryRV{IndexProj, …}`) bail to keep the
//     pass narrow.
//
// Fires before `hoistNonEscapingSplitInLoops` so the `[K]`-only
// pattern wins over the loop-hoist transform — the hoist still
// allocates one List and dups all pieces, which we can avoid here.
func fuseNonEscapingSplitNth(fn *Function) bool {
	if fn == nil || fn.IsExternal || fn.IsIntrinsic || len(fn.Blocks) == 0 {
		return false
	}
	changed := false
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		for instrIdx := 0; instrIdx < len(bb.Instrs); instrIdx++ {
			ii, isIntr := bb.Instrs[instrIdx].(*IntrinsicInstr)
			if !isIntr || ii.Kind != IntrinsicStringSplit {
				continue
			}
			if ii.Dest == nil || ii.Dest.HasProjections() {
				continue
			}
			if len(ii.Args) != 2 {
				continue
			}
			destL := ii.Dest.Local
			indexLocal, indexValue, indexAssignBB, indexAssignIdx, ok :=
				findSingleIndexProjUseFn(fn, destL, ii)
			if !ok {
				continue
			}
			// Build the replacement: IntrinsicStringNthSegment
			// targeting the original index-read destination.
			indexConst := &ConstOp{
				Const: &IntConst{Value: indexValue, T: TInt},
				T:     TInt,
			}
			fused := &IntrinsicInstr{
				Kind: IntrinsicStringNthSegment,
				Dest: &Place{Local: indexLocal},
				Args: []Operand{
					ii.Args[0], // value
					ii.Args[1], // sep
					indexConst,
				},
				SpanV: ii.SpanV,
			}
			// Apply both edits in a defined order: the index-read
			// might be in the same BB as the Split (the typical
			// straight-line case) or in a successor block. Compute
			// the adjusted index first, splice out the Split, then
			// install the fused intrinsic at the adjusted position.
			// Split is pure side-effect-wise (no abort, no IO,
			// only allocation), so removing the unused result
			// removes nothing observable.
			ab := fn.Block(indexAssignBB)
			adjustedIdx := indexAssignIdx
			if indexAssignBB == bb.ID && indexAssignIdx > instrIdx {
				adjustedIdx = indexAssignIdx - 1
			}
			bb.Instrs = append(bb.Instrs[:instrIdx], bb.Instrs[instrIdx+1:]...)
			ab.Instrs[adjustedIdx] = fused
			changed = true
			instrIdx-- // re-examine the slot we deleted from
		}
	}
	return changed
}

// findSingleIndexProjUseFn returns (indexAssignDestLocal, K, BB, idx,
// true) when destL has exactly one read across the function and that
// read is an `AssignInstr{Src: UseRV{Op: CopyOp{Place: destL[K_const]}}}`
// with K_const a non-negative IntConst. Otherwise returns
// `(_, _, _, _, false)`. Any read shape other than the canonical
// IndexProj-into-Use path counts as "not the simple pattern" and
// disables the fusion for that local. Storage markers and the
// originating split call are excluded from the read tally.
func findSingleIndexProjUseFn(fn *Function, destL LocalID, srcCall *IntrinsicInstr) (LocalID, int64, BlockID, int, bool) {
	if int(destL) < 0 || int(destL) >= len(fn.Locals) {
		return 0, 0, 0, 0, false
	}
	if fn.Locals[destL].IsParam || fn.Locals[destL].IsReturn || fn.ReturnLocal == destL {
		return 0, 0, 0, 0, false
	}
	type indexUse struct {
		bb       BlockID
		idx      int
		destLoc  LocalID
		constIdx int64
	}
	var match indexUse
	matches := 0
	otherReads := 0
	writes := 0
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		for j, instr := range bb.Instrs {
			switch x := instr.(type) {
			case *AssignInstr:
				if x.Dest.Local == destL {
					if x.Dest.HasProjections() {
						return 0, 0, 0, 0, false
					}
					return 0, 0, 0, 0, false // unrelated direct write — bail
				}
				if u, ok := x.Src.(*UseRV); ok {
					if cu, ok := u.Op.(*CopyOp); ok && cu.Place.Local == destL {
						if k, isConstIdx := singleConstIndexProjection(cu.Place); isConstIdx && !x.Dest.HasProjections() {
							match = indexUse{bb: bb.ID, idx: j, destLoc: x.Dest.Local, constIdx: k}
							matches++
							continue
						}
					}
				}
				if rvalueLeaksLocal(x.Src, destL) {
					otherReads++
				}
			case *CallInstr:
				if x.Dest != nil && x.Dest.Local == destL {
					return 0, 0, 0, 0, false
				}
				for _, op := range x.Args {
					if operandLeaksLocal(op, destL) {
						otherReads++
					}
				}
				if ind, ok := x.Callee.(*IndirectCall); ok {
					if operandLeaksLocal(ind.Callee, destL) {
						otherReads++
					}
				}
			case *IntrinsicInstr:
				if x == srcCall {
					writes++
					continue
				}
				if x.Dest != nil && x.Dest.Local == destL {
					return 0, 0, 0, 0, false
				}
				for _, op := range x.Args {
					if operandLeaksLocal(op, destL) {
						otherReads++
					}
				}
			case *StorageLiveInstr, *StorageDeadInstr:
				// markers — fine.
			}
		}
		switch t := bb.Term.(type) {
		case *BranchTerm:
			if operandLeaksLocal(t.Cond, destL) {
				otherReads++
			}
		case *SwitchIntTerm:
			if operandLeaksLocal(t.Scrutinee, destL) {
				otherReads++
			}
		}
	}
	if writes != 1 || matches != 1 || otherReads != 0 {
		return 0, 0, 0, 0, false
	}
	return match.destLoc, match.constIdx, match.bb, match.idx, true
}

// singleConstIndexProjection reports whether place's projections are
// exactly `[IndexProj{Index: ConstOp{IntConst{K}}}]` with K >= 0,
// returning K when so. Any other shape (multiple projections, non-
// const index, negative K, non-Int type) returns false.
func singleConstIndexProjection(p Place) (int64, bool) {
	if len(p.Projections) != 1 {
		return 0, false
	}
	idx, ok := p.Projections[0].(*IndexProj)
	if !ok {
		return 0, false
	}
	co, ok := idx.Index.(*ConstOp)
	if !ok {
		return 0, false
	}
	ic, ok := co.Const.(*IntConst)
	if !ok || ic.Value < 0 {
		return 0, false
	}
	return ic.Value, true
}
