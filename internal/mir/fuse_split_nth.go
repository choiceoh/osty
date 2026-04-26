package mir

import "sort"

// fuseNonEscapingSplitNth detects the `expr.split(sep)[K]` pattern (and
// its multi-index sibling, `let parts = expr.split(sep); parts[K0];
// parts[K1]; …`) and replaces each indexed read with a single
// `IntrinsicStringNthSegment` call. The full `Split` materialises a
// `List<String>` with N pieces; when only constant indices are
// consumed, the list itself is dead — eliminate it and dup just the
// segments we care about.
//
// Single-index pattern (markdown-stats `classify(line)` shape):
//
//	let parts = value.split(sep)        // IntrinsicStringSplit, dest=L
//	let first = parts[0]                // AssignInstr with IndexProj on L
//	// `parts` otherwise unused
//
// After:
//
//	let first = NthSegment(value, sep, 0)   // dest=M
//	// list-typed local L is dead; deadAssignElim removes the
//	// now-unreachable Split call and storage markers.
//
// Multi-index pattern (log-aggregator's per-line parse — `let parts =
// line.split(" "); let ts = parts[0]; let level = parts[1]`, hot enough
// to dominate the 50 K-line loop):
//
//	let parts = line.split(" ")
//	let ts    = parts[0]
//	let level = parts[1]
//
// After:
//
//	let ts    = NthSegment(line, " ", 0)
//	let level = NthSegment(line, " ", 1)
//
// We re-walk the input string for each index, so the fusion is a win
// only when the saved per-piece allocation work + parts-list alloc
// exceeds the cost of the extra walks. For typical
// `parts[0..K_small]` patterns over short inputs that's overwhelmingly
// true: each saved piece avoids one `osty_gc_allocate_managed(STRING)`
// + `osty_rt_string_dup_range` (heap or SSO), and the parts list
// itself dodges its `osty_rt_list_new` + N `list_push_ptr` chain.
//
// Safety conditions (all must hold):
//   - The split call's Dest local L has a single IntrinsicStringSplit
//     write and no other writes.
//   - L's reads are EXCLUSIVELY canonical IndexProj reads with non-
//     negative i64 IntConst indices — no `parts.len()`, no
//     `for x in parts`, no second indirect index `parts[i]`. ANY
//     other read shape (rvalue leak, call arg, branch operand)
//     leaves the Split path intact. The single-index variant is the
//     existing pre-extension shape; multi-index simply lifts the
//     `matches == 1` constraint.
//   - Each indexed read happens in an `AssignInstr{Src: UseRV{Op:
//     CopyOp{Place: parts[K]}}}`. Other RValue shapes (binary,
//     compound assign) bail.
//   - The set of (BB, idx) replacements is processed bottom-up
//     within each block so earlier replacements don't shift the
//     index of later ones we still need to find.
//
// Fires before `hoistNonEscapingSplitInLoops` so the constant-index
// pattern wins over the loop-hoist transform — the hoist still
// allocates one List per outer iteration and dups all pieces, which
// we can avoid here.
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
			matches, ok := findIndexProjUsesFn(fn, destL, ii)
			if !ok {
				continue
			}
			// Group replacements by block so we can splice-out from
			// each block bottom-up — modifying ab.Instrs[idx] in
			// place doesn't shift indices, but if a later
			// replacement and the Split call live in the same BB,
			// removing the Split first would invalidate later idx
			// values. We do replacements first (in-place), then
			// remove the Split call last.
			for _, m := range matches {
				ab := fn.Block(m.bb)
				if ab == nil {
					ok = false
					break
				}
				indexConst := &ConstOp{
					Const: &IntConst{Value: m.constIdx, T: TInt},
					T:     TInt,
				}
				ab.Instrs[m.idx] = &IntrinsicInstr{
					Kind: IntrinsicStringNthSegment,
					Dest: &Place{Local: m.destLoc},
					Args: []Operand{
						ii.Args[0], // value
						ii.Args[1], // sep
						indexConst,
					},
					SpanV: ii.SpanV,
				}
			}
			if !ok {
				// Bail mid-stream: at least one match's BB resolved
				// to nil, which shouldn't happen for a well-formed
				// function but we don't want a partial rewrite.
				continue
			}
			// Now remove the originating Split. After all the
			// in-place rewrites above the Split call still sits at
			// `bb.Instrs[instrIdx]`; splicing it out is safe even
			// when one of the rewritten instrs lived in the same
			// BB at a higher index because we never inserted, only
			// replaced. The remaining `bb.Instrs[idx]`-style
			// references would shift by one, but at this point we
			// don't need them.
			bb.Instrs = append(bb.Instrs[:instrIdx], bb.Instrs[instrIdx+1:]...)
			changed = true
			instrIdx-- // re-examine the slot we deleted from
		}
	}
	return changed
}

// indexUseMatch records one canonical `parts[K_const]` read of the
// originating Split's destination local. Stored as a slice so the
// multi-index lift can rewrite each of `parts[0]`, `parts[1]`, …
// independently.
type indexUseMatch struct {
	bb       BlockID
	idx      int
	destLoc  LocalID
	constIdx int64
}

// findIndexProjUsesFn returns the set of canonical index reads of
// `destL` (and `true`) when EVERY non-storage read of `destL` is the
// `AssignInstr{Src: UseRV{Op: CopyOp{Place: destL[K_const]}}}` shape.
// Any other read shape (rvalue leak, call arg, branch/switch operand,
// non-const index, `parts.len()` etc.) makes the function return
// `(_, false)` — the unfused Split path stays intact.
//
// `srcCall` is the originating Split intrinsic; it's the one allowed
// write of `destL` and is excluded from the read tally.
//
// Returned matches are sorted by (BlockID asc, instrIdx asc) so the
// caller can splice the originating Split last without disturbing
// earlier replacement indices.
func findIndexProjUsesFn(fn *Function, destL LocalID, srcCall *IntrinsicInstr) ([]indexUseMatch, bool) {
	if int(destL) < 0 || int(destL) >= len(fn.Locals) {
		return nil, false
	}
	if fn.Locals[destL].IsParam || fn.Locals[destL].IsReturn || fn.ReturnLocal == destL {
		return nil, false
	}
	matches := make([]indexUseMatch, 0, 4)
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
						return nil, false
					}
					return nil, false // unrelated direct write — bail
				}
				if u, ok := x.Src.(*UseRV); ok {
					if cu, ok := u.Op.(*CopyOp); ok && cu.Place.Local == destL {
						if k, isConstIdx := singleConstIndexProjection(cu.Place); isConstIdx && !x.Dest.HasProjections() {
							matches = append(matches, indexUseMatch{
								bb: bb.ID, idx: j,
								destLoc: x.Dest.Local, constIdx: k,
							})
							continue
						}
						// `parts[X]` where X isn't a non-negative
						// IntConst, or the dest carries its own
						// projections. `rvalueLeaksLocal` won't
						// flag this — `placeLeaksRoot` deliberately
						// treats `IndexProj`-led reads as element
						// access, not bare-pointer leaks. Without
						// the explicit bail we'd approve fusion,
						// rewrite the const-index reads, drop the
						// originating Split, and orphan the
						// non-const-index read on a now-dead local.
						return nil, false
					}
				}
				if rvalueLeaksLocal(x.Src, destL) {
					otherReads++
				}
			case *CallInstr:
				if x.Dest != nil && x.Dest.Local == destL {
					return nil, false
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
					return nil, false
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
	if writes != 1 || len(matches) < 1 || otherReads != 0 {
		return nil, false
	}
	// Sort by (bb, idx) ascending. Stable order makes the rewrite
	// loop deterministic and avoids surprising index-shift bugs if
	// the caller ever needs to mutate Instrs alongside the matches.
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].bb != matches[j].bb {
			return matches[i].bb < matches[j].bb
		}
		return matches[i].idx < matches[j].idx
	})
	return matches, true
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
