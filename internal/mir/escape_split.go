package mir

import "fmt"

// hoistNonEscapingSplitInLoops moves `IntrinsicStringSplit` allocations
// out of loop bodies when the result list does not escape the iteration.
//
// Pattern (csv_parse, record_pipeline-style hot loops):
//
//	for row in rows {
//	    let parts = row.split(",")
//	    use parts[0], parts[1], parts.len()
//	}
//
// Each iteration today calls `osty_rt_strings_Split` which allocates a
// fresh `osty_rt_list` plus grows the backing array on the first few
// pushes. After the transform, one persistent buffer is allocated in the
// loop preheader, and each iteration calls `osty_rt_strings_SplitInto`
// which clears and refills the buffer in place. Per-iteration cost drops
// from `1 list_new + 1-2 realloc + N piece dups` to `N piece dups` once
// the backing array has stabilised.
//
// Safety conditions (all must hold):
//   - The split call lives in a natural loop (reachable from a back-edge
//     header and reaching the back-edge source).
//   - The result local L's only writes are the split call we're
//     rewriting; reads of L only ever produce a derived scalar
//     (`parts[i]` element via IndexProj, `parts.len()` via LenRV) or
//     a Storage{Live,Dead} marker. Any read that yields the bare list
//     pointer — return, call argument, aggregate field, alias copy,
//     `&parts` — is treated as escape and bails the transform.
//   - The loop has a unique preheader (a single predecessor of the
//     header outside the loop body). MVP bails on multiple preheaders
//     instead of synthesising one.
//
// Returns true when any function block was rewritten.
func hoistNonEscapingSplitInLoops(fn *Function) bool {
	if fn == nil || fn.IsExternal || fn.IsIntrinsic || len(fn.Blocks) == 0 {
		return false
	}
	backEdges := findBackEdges(fn)
	if len(backEdges) == 0 {
		return false
	}
	preds := Predecessors(fn)
	changed := false
	for _, edge := range backEdges {
		body := loopBodyBlocks(fn, edge.header, edge.from, preds)
		if len(body) == 0 {
			continue
		}
		preheader, ok := uniquePreheader(edge.header, body, preds)
		if !ok {
			continue
		}
		for _, bid := range body {
			bb := fn.Block(bid)
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
				if !splitDestEscapeSafe(fn, destL, ii) {
					continue
				}
				bufType := fn.Locals[destL].Type
				bufID := fn.NewLocal(fmt.Sprintf("_split_buf_%d", destL), bufType, true, fn.Locals[destL].SpanV)
				phBB := fn.Block(preheader)
				phBB.Instrs = append(phBB.Instrs,
					&StorageLiveInstr{Local: bufID, SpanV: phBB.SpanV},
					&AssignInstr{
						Dest:  Place{Local: bufID},
						Src:   &AggregateRV{Kind: AggList, Fields: nil, T: bufType},
						SpanV: phBB.SpanV,
					},
				)
				splitInto := &IntrinsicInstr{
					Kind: IntrinsicStringSplitInto,
					Args: []Operand{
						&CopyOp{Place: Place{Local: bufID}, T: bufType},
						ii.Args[0], ii.Args[1],
					},
					SpanV: ii.SpanV,
				}
				aliasAssign := &AssignInstr{
					Dest:  Place{Local: destL},
					Src:   &UseRV{Op: &CopyOp{Place: Place{Local: bufID}, T: bufType}},
					SpanV: ii.SpanV,
				}
				rewritten := make([]Instr, 0, len(bb.Instrs)+1)
				rewritten = append(rewritten, bb.Instrs[:instrIdx]...)
				rewritten = append(rewritten, splitInto, aliasAssign)
				rewritten = append(rewritten, bb.Instrs[instrIdx+1:]...)
				bb.Instrs = rewritten
				changed = true
				instrIdx++ // skip the appended alias on the next iteration
			}
		}
	}
	return changed
}

// backEdge captures one back-edge in the CFG: from → header where
// header is an ancestor of from in the DFS tree from fn.Entry.
type backEdge struct {
	from   BlockID
	header BlockID
}

// findBackEdges runs an iterative DFS from fn.Entry and reports every
// edge whose terminator successor is gray when visited.
func findBackEdges(fn *Function) []backEdge {
	if fn == nil || len(fn.Blocks) == 0 {
		return nil
	}
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make([]int, len(fn.Blocks))
	type frame struct {
		bid        BlockID
		successors []BlockID
		nextIdx    int
	}
	push := func(stack []frame, b BlockID) []frame {
		if int(b) < 0 || int(b) >= len(fn.Blocks) || color[b] != white {
			return stack
		}
		color[b] = gray
		bb := fn.Blocks[b]
		var succ []BlockID
		if bb != nil {
			succ = Successors(bb.Term)
		}
		return append(stack, frame{bid: b, successors: succ})
	}
	var edges []backEdge
	stack := push(nil, fn.Entry)
	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		if top.nextIdx >= len(top.successors) {
			color[top.bid] = black
			stack = stack[:len(stack)-1]
			continue
		}
		next := top.successors[top.nextIdx]
		top.nextIdx++
		if int(next) < 0 || int(next) >= len(fn.Blocks) {
			continue
		}
		switch color[next] {
		case white:
			stack = push(stack, next)
		case gray:
			edges = append(edges, backEdge{from: top.bid, header: next})
		}
	}
	return edges
}

// loopBodyBlocks returns the natural loop induced by a back edge
// (backSrc → header): blocks reachable from header that can also reach
// backSrc. Both header and backSrc are included.
func loopBodyBlocks(fn *Function, header, backSrc BlockID, preds map[BlockID][]BlockID) []BlockID {
	forward := map[BlockID]bool{header: true}
	stack := []BlockID{header}
	for len(stack) > 0 {
		b := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		bb := fn.Block(b)
		if bb == nil {
			continue
		}
		for _, succ := range Successors(bb.Term) {
			if forward[succ] {
				continue
			}
			forward[succ] = true
			stack = append(stack, succ)
		}
	}
	if !forward[backSrc] {
		return nil
	}
	backward := map[BlockID]bool{backSrc: true, header: true}
	stack = append(stack[:0], backSrc)
	for len(stack) > 0 {
		b := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, pred := range preds[b] {
			if backward[pred] {
				continue
			}
			backward[pred] = true
			stack = append(stack, pred)
		}
	}
	var body []BlockID
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		if forward[bb.ID] && backward[bb.ID] {
			body = append(body, bb.ID)
		}
	}
	return body
}

// uniquePreheader returns the sole predecessor of header that lies
// outside the loop body. Returns false when zero or multiple such
// predecessors exist — MVP doesn't synthesise a preheader.
func uniquePreheader(header BlockID, body []BlockID, preds map[BlockID][]BlockID) (BlockID, bool) {
	inBody := map[BlockID]bool{}
	for _, b := range body {
		inBody[b] = true
	}
	found := BlockID(-1)
	for _, p := range preds[header] {
		if inBody[p] {
			continue
		}
		if found != -1 {
			return -1, false
		}
		found = p
	}
	if found == -1 {
		return -1, false
	}
	return found, true
}

// splitDestEscapeSafe verifies that destL is only written by srcCall
// and that every read of destL across the function either yields a
// derived scalar (IndexProj element, LenRV) or is a Storage marker.
// Any read that surfaces the bare list pointer is treated as escape.
func splitDestEscapeSafe(fn *Function, destL LocalID, srcCall *IntrinsicInstr) bool {
	if int(destL) < 0 || int(destL) >= len(fn.Locals) {
		return false
	}
	if fn.Locals[destL].IsParam || fn.Locals[destL].IsReturn {
		return false
	}
	if fn.ReturnLocal == destL {
		return false
	}
	writes := 0
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		for _, instr := range bb.Instrs {
			switch x := instr.(type) {
			case *AssignInstr:
				if x.Dest.Local == destL {
					if x.Dest.HasProjections() {
						return false
					}
					return false // unrelated direct write — bail
				}
				if rvalueLeaksLocal(x.Src, destL) {
					return false
				}
			case *CallInstr:
				if x.Dest != nil && x.Dest.Local == destL {
					return false
				}
				for _, op := range x.Args {
					if operandLeaksLocal(op, destL) {
						return false
					}
				}
				if ind, ok := x.Callee.(*IndirectCall); ok {
					if operandLeaksLocal(ind.Callee, destL) {
						return false
					}
				}
			case *IntrinsicInstr:
				if x == srcCall {
					writes++
					continue
				}
				if x.Dest != nil && x.Dest.Local == destL {
					return false
				}
				// Read-only list query intrinsics receive the bare list
				// pointer as Args[0] but do not retain it past the call —
				// safe to pass even though placeLeaksRoot would otherwise
				// flag the bare CopyOp. Mutating intrinsics
				// (push/insert/clear/pop) are deliberately *not* on this
				// list; the buf reuse model only holds when nothing
				// modifies the list between the SplitInto and the next
				// iteration.
				if len(x.Args) >= 1 && operandLeaksLocal(x.Args[0], destL) && readOnlyListQueryIntrinsic(x.Kind) {
					// Args[0] is the receiver — fine for query intrinsics.
					// Check the rest don't leak.
					for _, op := range x.Args[1:] {
						if operandLeaksLocal(op, destL) {
							return false
						}
					}
					continue
				}
				for _, op := range x.Args {
					if operandLeaksLocal(op, destL) {
						return false
					}
				}
			case *StorageLiveInstr, *StorageDeadInstr:
				// markers — fine.
			}
		}
		switch t := bb.Term.(type) {
		case *BranchTerm:
			if operandLeaksLocal(t.Cond, destL) {
				return false
			}
		case *SwitchIntTerm:
			if operandLeaksLocal(t.Scrutinee, destL) {
				return false
			}
		}
	}
	return writes == 1
}

// rvalueLeaksLocal reports whether evaluating rv yields a value that
// contains destL's bare list pointer (and could therefore alias it
// outside the loop). LenRV / DiscriminantRV produce scalars and never
// leak. IndexProj reads under any operand produce element values, not
// the list itself, so they are also safe.
func rvalueLeaksLocal(rv RValue, destL LocalID) bool {
	switch x := rv.(type) {
	case *UseRV:
		return operandLeaksLocal(x.Op, destL)
	case *UnaryRV:
		return operandLeaksLocal(x.Arg, destL)
	case *BinaryRV:
		return operandLeaksLocal(x.Left, destL) || operandLeaksLocal(x.Right, destL)
	case *AggregateRV:
		for _, f := range x.Fields {
			if operandLeaksLocal(f, destL) {
				return true
			}
		}
		return false
	case *CastRV:
		return operandLeaksLocal(x.Arg, destL)
	case *DiscriminantRV, *LenRV:
		return false
	case *AddressOfRV:
		return x.Place.Local == destL
	case *RefRV:
		return x.Place.Local == destL
	case *NullaryRV, *GlobalRefRV:
		return false
	}
	return true // unknown rv — be conservative
}

// operandLeaksLocal returns true for a CopyOp/MoveOp whose Place would
// surface the bare list pointer (root local destL with no projections,
// or with a non-IndexProj first projection).
func operandLeaksLocal(op Operand, destL LocalID) bool {
	if op == nil {
		return false
	}
	switch x := op.(type) {
	case *CopyOp:
		return placeLeaksRoot(x.Place, destL)
	case *MoveOp:
		return placeLeaksRoot(x.Place, destL)
	}
	return false
}

// placeLeaksRoot reports whether reading place yields the bare list
// pointer of destL. Element reads (`destL[i]`, `destL[i].field`) do
// not — IndexProj decomposes the list. Field/tuple/variant projections
// before any IndexProj would not appear on a List<T>, so we treat them
// conservatively as escape.
func placeLeaksRoot(p Place, destL LocalID) bool {
	if p.Local != destL {
		return false
	}
	if len(p.Projections) == 0 {
		return true
	}
	_, isIndex := p.Projections[0].(*IndexProj)
	return !isIndex
}

// readOnlyListQueryIntrinsic enumerates the IntrinsicKind values that
// take a List receiver as Args[0] and observe but never retain the list
// past the call. Mutating list intrinsics (push, insert, clear, pop,
// etc.) are deliberately excluded — the SplitInto reuse model assumes
// nothing else changes the buffer between iterations.
func readOnlyListQueryIntrinsic(k IntrinsicKind) bool {
	switch k {
	case IntrinsicListLen, IntrinsicListIsEmpty, IntrinsicListGet,
		IntrinsicListContains, IntrinsicListFirst, IntrinsicListLast,
		IntrinsicListIndexOf, IntrinsicListSorted, IntrinsicListSlice:
		return true
	}
	return false
}
