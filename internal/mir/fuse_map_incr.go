package mir

// fuseMapInsertGetOrAdd detects the canonical counter pattern
//
//	m.insert(k, m.getOr(k, 0) + delta)
//
// and rewrites it to a single `IntrinsicMapIncr(m, k, delta)` call.
// The fused form does one map probe + in-place value update on hit
// (falling through to insert on miss), saving one full hash probe
// per call site.
//
// The pattern shows up in every counter-heavy benchmark:
//
//   - lru-sim: `accessed.insert(key, accessed.getOr(key, 0) + 1)`
//   - log-aggregator: `levelCounts.insert(level, levelCounts.getOr(level, 0) + 1)`
//   - markdown-stats: `counts.insert(kind, counts.getOr(kind, 0) + 1)`
//                     `chars.insert("heading", chars.getOr("heading", 0) + n)`
//
// At the MIR level the user expression lowers to three instructions
// in the same basic block, with two intermediate locals threading
// the data:
//
//	IntrinsicInstr{MapGetOr, [m, k, IntConst(0)], dest=N}
//	AssignInstr{V, BinaryRV{Add, CopyOp(N), <delta>}}
//	IntrinsicInstr{MapSet, [m, k, CopyOp(V)]}
//
// The fusion replaces the triple with:
//
//	IntrinsicInstr{MapIncr, [m, k, <delta>], dest=V}
//
// (`dest=V` so callers that read the new value still find it; the
// map_incr_i64_<suffix> runtime helper returns the post-increment
// value.) The original GetOr and the AssignInstr become dead and
// the standard `deadAssignElim` peephole removes them on the next
// fixed-point iteration.
//
// Safety conditions (all must hold; otherwise the unfused triple
// stays intact):
//
//   - The three instructions are in the same basic block, in the
//     order GetOr → Add → MapSet, with no other reads of `N` or `V`
//     between them. Cross-block patterns aren't detected (they'd
//     require region-flavour analysis).
//   - The map operand `m` is the same `Place` (same local, same
//     projections) on the GetOr and the MapSet.
//   - The key operand `k` is the same `Place` on the GetOr and the
//     MapSet.
//   - The default operand on GetOr is exactly `IntConst{Value: 0}`.
//     Non-zero defaults (`m.getOr(k, 7) + delta`) don't fuse — the
//     runtime helper assumes "missing key = 0".
//   - Both `N` and `V` are temporaries with a single read each (the
//     N→Add and V→MapSet edges) and no other writes / reads / escapes.
//   - The map's value type is Int (i64). The runtime helper is
//     specialised to i64 values.
func fuseMapInsertGetOrAdd(fn *Function) bool {
	if fn == nil || fn.IsExternal || fn.IsIntrinsic || len(fn.Blocks) == 0 {
		return false
	}
	changed := false
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		for i := 0; i+2 < len(bb.Instrs); i++ {
			getOr, ok := bb.Instrs[i].(*IntrinsicInstr)
			if !ok || getOr.Kind != IntrinsicMapGetOr || getOr.Dest == nil {
				continue
			}
			if !isZeroIntConstOperand(getOr.Args[2]) {
				continue
			}
			nLocal := getOr.Dest.Local
			add, ok := bb.Instrs[i+1].(*AssignInstr)
			if !ok || add.Dest.HasProjections() {
				continue
			}
			binary, ok := add.Src.(*BinaryRV)
			if !ok || binary.Op != BinAdd {
				continue
			}
			delta, ok := matchAddOfLocal(binary, nLocal)
			if !ok {
				continue
			}
			vLocal := add.Dest.Local
			set, ok := bb.Instrs[i+2].(*IntrinsicInstr)
			if !ok || set.Kind != IntrinsicMapSet {
				continue
			}
			if set.Dest != nil || len(set.Args) != 3 {
				continue
			}
			if !sameMapKeyOperands(getOr.Args[0], getOr.Args[1], set.Args[0], set.Args[1]) {
				continue
			}
			if !isCopyOfLocal(set.Args[2], vLocal) {
				continue
			}
			if localUsageOutside(fn, nLocal, bb.ID, i, i+1) {
				continue
			}
			if localUsageOutside(fn, vLocal, bb.ID, i+1, i+2) {
				continue
			}
			// Build the fused intrinsic. dest=V so any later read of
			// the post-add result still resolves correctly. The
			// runtime helper returns the new value.
			fused := &IntrinsicInstr{
				Kind: IntrinsicMapIncr,
				Dest: &Place{Local: vLocal},
				Args: []Operand{
					getOr.Args[0], // map
					getOr.Args[1], // key
					delta,
				},
				SpanV: getOr.SpanV,
			}
			rewritten := make([]Instr, 0, len(bb.Instrs)-2)
			rewritten = append(rewritten, bb.Instrs[:i]...)
			rewritten = append(rewritten, fused)
			rewritten = append(rewritten, bb.Instrs[i+3:]...)
			bb.Instrs = rewritten
			changed = true
			i--
		}
	}
	return changed
}

// isZeroIntConstOperand returns true for a `CopyOp` / `MoveOp` /
// `ConstOp` whose value is the i64 constant 0.
func isZeroIntConstOperand(op Operand) bool {
	co, ok := op.(*ConstOp)
	if !ok {
		return false
	}
	ic, ok := co.Const.(*IntConst)
	if !ok {
		return false
	}
	return ic.Value == 0
}

// matchAddOfLocal returns (delta, true) if the BinaryRV is
// `<lhs> + <rhs>` with one side being `CopyOp{nLocal}` (no
// projections) and the other being any operand. The other operand
// becomes the delta. Either side may be the local; addition is
// commutative.
func matchAddOfLocal(b *BinaryRV, nLocal LocalID) (Operand, bool) {
	if isCopyOfLocal(b.Left, nLocal) {
		return b.Right, true
	}
	if isCopyOfLocal(b.Right, nLocal) {
		return b.Left, true
	}
	return nil, false
}

// isCopyOfLocal returns true for `CopyOp{Place: {Local: l, no
// projections}}` or the equivalent `MoveOp` shape.
func isCopyOfLocal(op Operand, l LocalID) bool {
	switch x := op.(type) {
	case *CopyOp:
		return x.Place.Local == l && !x.Place.HasProjections()
	case *MoveOp:
		return x.Place.Local == l && !x.Place.HasProjections()
	}
	return false
}

// sameMapKeyOperands reports whether the (map, key) pair on the
// GetOr matches the (map, key) pair on the MapSet — same Place
// (same local + same projection chain) on both sides.
func sameMapKeyOperands(getOrMap, getOrKey, setMap, setKey Operand) bool {
	return sameOperandPlace(getOrMap, setMap) && sameOperandPlace(getOrKey, setKey)
}

// sameOperandPlace returns true when both operands read the same
// Place. ConstOps with the same value also count (so `getOr(_,
// "heading", 0) + n` followed by `insert("heading", _)` matches —
// the user wrote a literal both times). Other operand kinds bail
// the comparison rather than try to be smart.
func sameOperandPlace(a, b Operand) bool {
	if ca, ok := a.(*CopyOp); ok {
		if cb, ok := b.(*CopyOp); ok {
			return placesEqual(ca.Place, cb.Place)
		}
		if mb, ok := b.(*MoveOp); ok {
			return placesEqual(ca.Place, mb.Place)
		}
	}
	if ma, ok := a.(*MoveOp); ok {
		if cb, ok := b.(*CopyOp); ok {
			return placesEqual(ma.Place, cb.Place)
		}
		if mb, ok := b.(*MoveOp); ok {
			return placesEqual(ma.Place, mb.Place)
		}
	}
	if cca, ok := a.(*ConstOp); ok {
		if ccb, ok := b.(*ConstOp); ok {
			return constsEqual(cca.Const, ccb.Const)
		}
	}
	return false
}

// placesEqual compares two Places for structural equality: same
// root local + identical projection chain (each projection's
// shape and operands match). Currently handles the IndexProj /
// FieldProj / VariantProj / DerefProj projection kinds; unknown
// kinds bail to false (the fusion stays correct, just doesn't
// fire).
func placesEqual(a, b Place) bool {
	if a.Local != b.Local {
		return false
	}
	if len(a.Projections) != len(b.Projections) {
		return false
	}
	for i := range a.Projections {
		if !projectionsEqual(a.Projections[i], b.Projections[i]) {
			return false
		}
	}
	return true
}

func projectionsEqual(a, b Projection) bool {
	switch ap := a.(type) {
	case *IndexProj:
		bp, ok := b.(*IndexProj)
		return ok && sameOperandPlace(ap.Index, bp.Index)
	case *FieldProj:
		bp, ok := b.(*FieldProj)
		return ok && ap.Index == bp.Index
	case *VariantProj:
		bp, ok := b.(*VariantProj)
		return ok && ap.Variant == bp.Variant && ap.FieldIdx == bp.FieldIdx
	case *DerefProj:
		_, ok := b.(*DerefProj)
		return ok
	}
	return false
}

// constsEqual checks structural equality for the few Const kinds
// the pattern actually feeds in (string literals for map keys).
// Other kinds (Int / Float / Bool / Char) appear in the delta
// position, not the key — but we cover them too for robustness.
func constsEqual(a, b Const) bool {
	switch ac := a.(type) {
	case *IntConst:
		bc, ok := b.(*IntConst)
		return ok && ac.Value == bc.Value
	case *StringConst:
		bc, ok := b.(*StringConst)
		return ok && ac.Value == bc.Value
	case *BoolConst:
		bc, ok := b.(*BoolConst)
		return ok && ac.Value == bc.Value
	case *NullConst:
		_, ok := b.(*NullConst)
		return ok
	}
	return false
}

// localUsageOutside returns true when `local` is read or written
// anywhere in the function except inside `bb` between instruction
// indices `start` and `end` (inclusive on both ends — those are the
// instructions we plan to keep as the legitimate uses). Reads /
// writes outside that window block the fusion: we'd be deleting
// the producer for a live consumer.
func localUsageOutside(fn *Function, local LocalID, ownBB BlockID, start, end int) bool {
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		for j, instr := range bb.Instrs {
			if bb.ID == ownBB && j >= start && j <= end {
				continue
			}
			if instrTouchesLocal(instr, local) {
				return true
			}
		}
		switch t := bb.Term.(type) {
		case *BranchTerm:
			if operandTouchesLocal(t.Cond, local) {
				return true
			}
		case *SwitchIntTerm:
			if operandTouchesLocal(t.Scrutinee, local) {
				return true
			}
		}
	}
	return false
}

func instrTouchesLocal(instr Instr, local LocalID) bool {
	switch x := instr.(type) {
	case *AssignInstr:
		if x.Dest.Local == local {
			return true
		}
		if rvalueTouchesLocal(x.Src, local) {
			return true
		}
	case *CallInstr:
		if x.Dest != nil && x.Dest.Local == local {
			return true
		}
		for _, op := range x.Args {
			if operandTouchesLocal(op, local) {
				return true
			}
		}
	case *IntrinsicInstr:
		if x.Dest != nil && x.Dest.Local == local {
			return true
		}
		for _, op := range x.Args {
			if operandTouchesLocal(op, local) {
				return true
			}
		}
	case *StorageLiveInstr, *StorageDeadInstr:
		// Markers, not real reads. The fusion is safe even when a
		// later StorageDead names the local — that just means the
		// local goes out of scope after the dead block, which is
		// fine since the fused intrinsic targets `vLocal` instead
		// (the post-add result) and the `nLocal` becomes
		// genuinely unused. The StorageDead may dangle on a now-
		// removed local; the standard `deadAssignElim` peephole
		// drops dangling storage markers on the next iteration.
		// (Mirrors `splitDestEscapeSafe`'s policy in
		// `internal/mir/escape_split.go`.)
		return false
	}
	return false
}

func rvalueTouchesLocal(rv RValue, local LocalID) bool {
	switch x := rv.(type) {
	case *UseRV:
		return operandTouchesLocal(x.Op, local)
	case *UnaryRV:
		return operandTouchesLocal(x.Arg, local)
	case *BinaryRV:
		return operandTouchesLocal(x.Left, local) || operandTouchesLocal(x.Right, local)
	case *AggregateRV:
		for _, f := range x.Fields {
			if operandTouchesLocal(f, local) {
				return true
			}
		}
	case *CastRV:
		return operandTouchesLocal(x.Arg, local)
	case *AddressOfRV:
		return x.Place.Local == local
	case *RefRV:
		return x.Place.Local == local
	}
	return false
}

func operandTouchesLocal(op Operand, local LocalID) bool {
	if op == nil {
		return false
	}
	switch x := op.(type) {
	case *CopyOp:
		return placeTouchesLocal(x.Place, local)
	case *MoveOp:
		return placeTouchesLocal(x.Place, local)
	}
	return false
}

func placeTouchesLocal(p Place, local LocalID) bool {
	if p.Local == local {
		return true
	}
	for _, proj := range p.Projections {
		if ip, ok := proj.(*IndexProj); ok {
			if operandTouchesLocal(ip.Index, local) {
				return true
			}
		}
	}
	return false
}
