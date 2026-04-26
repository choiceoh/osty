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
// `parts.len()` extension (defensive-bounds-check shape from log-
// aggregator-style parsers):
//
//	let parts = line.split(" ")
//	if parts.len() < 3 { continue }
//	let ts    = parts[0]
//	let level = parts[1]
//
// Naively, the `parts.len()` read disqualified fusion — it's a non-
// canonical use of the parts list. But for any non-empty separator,
// `len(Split(value, sep)) == Count(value, sep) + 1` exactly (the
// runtime always pushes the trailing piece), so the read can be
// rewritten to a runtime `Count` plus a `+1`. After the rewrite the
// parts list is unused and the originating Split disappears with
// the rest of the pattern:
//
//	if Count(line, " ") + 1 < 3 { continue }
//	let ts    = NthSegment(line, " ", 0)
//	let level = NthSegment(line, " ", 1)
//
// The empty-separator case is excluded — Split on an empty separator
// performs byte-level expansion returning N pieces while Count
// returns N+1, so the rewrite would be off by one. The fusion pass
// requires the Split's separator argument to be a non-empty
// StringConst literal, which covers every realistic call site.
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
//   - L's reads are EXCLUSIVELY canonical IndexProj reads OR bare-
//     local `parts.len()` reads. No `for x in parts`, no second
//     indirect index `parts[i]`, no escapes via call args / branch
//     operands. ANY other read shape leaves the unfused Split path
//     intact.
//   - Each indexed read happens in an `AssignInstr{Src: UseRV{Op:
//     CopyOp{Place: parts[K]}}}`. Other RValue shapes (binary,
//     compound assign) bail.
//   - When at least one `parts.len()` read is present, the Split's
//     separator argument MUST be a non-empty StringConst literal.
//     The `Count + 1` rewrite is exact only for that shape.
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
			matches, lenReads, ok := findIndexProjUsesFn(fn, destL, ii)
			if !ok {
				continue
			}
			// `len`-rewrite requires a non-empty StringConst separator
			// (see top-of-file rationale: empty-sep Split returns N
			// pieces while Count returns N+1). If we have any len reads
			// but the separator isn't a non-empty literal, leave the
			// pattern alone rather than fuse half of it.
			if len(lenReads) > 0 && !isNonEmptyStringConst(ii.Args[1]) {
				continue
			}
			// Per-block splicing strategy: replace canonical IndexProj
			// reads in-place (no index shift), then for `parts.len()`
			// reads emit a 2-instruction sequence in place of the
			// original ListLen — replacing the existing slot with the
			// Count call and inserting the `+1` AssignInstr just after.
			// We process len rewrites in REVERSE block order so the
			// inserted instructions don't shift indices we still need
			// to find for siblings in the same block.
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
				continue
			}
			// `parts.len()` rewrites: for each one, replace the
			// IntrinsicListLen with `tmp = Count(value, sep)` (writing
			// a fresh local) and insert `AssignInstr{Dest: original,
			// Src: tmp + 1}` right after. Sort by (BB desc, idx desc)
			// so insertion in one slot doesn't shift indices of later
			// rewrites in the same block.
			sortLenReadsForRewrite(lenReads)
			for _, lr := range lenReads {
				ab := fn.Block(lr.bb)
				if ab == nil {
					ok = false
					break
				}
				tmp := fn.NewLocal("split_nth_count_tmp", TInt, false, ii.SpanV)
				ab.Instrs[lr.idx] = &IntrinsicInstr{
					Kind: IntrinsicStringCount,
					Dest: &Place{Local: tmp},
					Args: []Operand{
						ii.Args[0], // value
						ii.Args[1], // sep
					},
					SpanV: ii.SpanV,
				}
				addOne := &AssignInstr{
					Dest: Place{Local: lr.destLoc},
					Src: &BinaryRV{
						Op:    BinAdd,
						Left:  &CopyOp{Place: Place{Local: tmp}, T: TInt},
						Right: &ConstOp{Const: &IntConst{Value: 1, T: TInt}, T: TInt},
						T:     TInt,
					},
					SpanV: ii.SpanV,
				}
				// Splice insert at lr.idx+1.
				ab.Instrs = append(ab.Instrs, nil)
				copy(ab.Instrs[lr.idx+2:], ab.Instrs[lr.idx+1:])
				ab.Instrs[lr.idx+1] = addOne
				// Same-block bookkeeping: if the Split call we're about
				// to remove sits at a higher index in the same block,
				// the insert just shifted it by one. Track the shift
				// so the splice-out below targets the correct slot.
				if lr.bb == bb.ID && lr.idx <= instrIdx {
					instrIdx++
				}
			}
			if !ok {
				continue
			}
			// Now remove the originating Split. After all the
			// in-place rewrites above the Split call still sits at
			// `bb.Instrs[instrIdx]` (adjusted for any same-block
			// inserts above).
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

// lenUseMatch records one `parts.len()` read of the originating
// Split's destination local. The rewrite path replaces the
// IntrinsicListLen with a 2-instruction sequence:
//
//	tmp = IntrinsicStringCount(value, sep)
//	dest = tmp + 1
//
// so the original `dest` local sees the same value as before.
type lenUseMatch struct {
	bb      BlockID
	idx     int
	destLoc LocalID
}

// isNonEmptyStringConst returns true when `op` is a non-empty
// StringConst literal. Used to gate the `parts.len() → Count(value,
// sep) + 1` rewrite: an empty separator triggers different runtime
// semantics (byte-level expansion in Split, value-len + 1 in Count)
// and would make the rewrite off by one.
func isNonEmptyStringConst(op Operand) bool {
	co, ok := op.(*ConstOp)
	if !ok {
		return false
	}
	sc, ok := co.Const.(*StringConst)
	if !ok {
		return false
	}
	return len(sc.Value) > 0
}

// argReadsBareLocal reports whether `op` is `CopyOp{Place: bare-local
// destL}` — i.e., reads the entire list as a single value (no
// projections). Used to detect `parts.len()` and similar full-list
// reads where the receiver is just `parts` with nothing after.
func argReadsBareLocal(op Operand, destL LocalID) bool {
	cu, ok := op.(*CopyOp)
	if !ok {
		return false
	}
	return cu.Place.Local == destL && len(cu.Place.Projections) == 0
}

// sortLenReadsForRewrite orders len rewrites bottom-up within each
// block (highest BlockID first, then highest instrIdx first). The
// rewrite inserts an extra instruction per len read; processing
// bottom-up means earlier-block / earlier-idx rewrites still find
// the right offsets when their turn comes.
func sortLenReadsForRewrite(lr []lenUseMatch) {
	sort.SliceStable(lr, func(i, j int) bool {
		if lr[i].bb != lr[j].bb {
			return lr[i].bb > lr[j].bb
		}
		return lr[i].idx > lr[j].idx
	})
}

// findIndexProjUsesFn returns the set of canonical index reads of
// `destL` and the set of `parts.len()` reads (plus `true`) when
// EVERY non-storage read of `destL` is one of:
//
//   - `AssignInstr{Src: UseRV{Op: CopyOp{Place: destL[K_const]}}}`
//     (canonical IndexProj — gets fused to NthSegment)
//   - `IntrinsicInstr{Kind: IntrinsicListLen, Args: [CopyOp{destL}]}`
//     (parts.len() — gets rewritten to Count + 1, but only when the
//     originating Split's separator is a non-empty StringConst)
//
// Any other read shape (rvalue leak, call arg, branch/switch operand,
// non-const index, `for x in parts`, etc.) makes the function return
// `(_, _, false)` — the unfused Split path stays intact.
//
// `srcCall` is the originating Split intrinsic; it's the one allowed
// write of `destL` and is excluded from the read tally.
//
// Returned matches are sorted by (BlockID asc, instrIdx asc) so the
// caller can splice the originating Split last without disturbing
// earlier replacement indices. lenReads have a separate sort step
// (sortLenReadsForRewrite) since their rewrite inserts new
// instructions and needs to process bottom-up.
func findIndexProjUsesFn(fn *Function, destL LocalID, srcCall *IntrinsicInstr) ([]indexUseMatch, []lenUseMatch, bool) {
	if int(destL) < 0 || int(destL) >= len(fn.Locals) {
		return nil, nil, false
	}
	if fn.Locals[destL].IsParam || fn.Locals[destL].IsReturn || fn.ReturnLocal == destL {
		return nil, nil, false
	}
	matches := make([]indexUseMatch, 0, 4)
	lenReads := make([]lenUseMatch, 0, 2)
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
						return nil, nil, false
					}
					return nil, nil, false // unrelated direct write — bail
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
						return nil, nil, false
					}
				}
				if rvalueLeaksLocal(x.Src, destL) {
					otherReads++
				}
			case *CallInstr:
				if x.Dest != nil && x.Dest.Local == destL {
					return nil, nil, false
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
					return nil, nil, false
				}
				// `parts.len()` — IntrinsicListLen with `parts` as
				// Args[0] and a Dest that isn't `parts` itself. The
				// rewrite path can replace this with `Count(value,
				// sep) + 1` when the originating Split's separator
				// is a non-empty literal. Track separately so the
				// caller can gate on the separator shape after
				// collecting.
				if x.Kind == IntrinsicListLen && x.Dest != nil &&
					!x.Dest.HasProjections() && x.Dest.Local != destL &&
					len(x.Args) == 1 && argReadsBareLocal(x.Args[0], destL) {
					lenReads = append(lenReads, lenUseMatch{
						bb: bb.ID, idx: j,
						destLoc: x.Dest.Local,
					})
					continue
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
		return nil, nil, false
	}
	// Sort canonical matches by (bb, idx) ascending. Stable order
	// makes the rewrite loop deterministic and avoids surprising
	// index-shift bugs if the caller ever needs to mutate Instrs
	// alongside the matches.
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].bb != matches[j].bb {
			return matches[i].bb < matches[j].bb
		}
		return matches[i].idx < matches[j].idx
	})
	return matches, lenReads, true
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
