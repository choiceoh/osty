package mir

import (
	"testing"
)

// fuseNonEscapingSplitNth has two flavours:
//
//   1. Single-index (markdown-stats): one `parts[K]` read, fuse to a
//      single NthSegment call.
//   2. Multi-index (log-aggregator): N reads `parts[K0..KM]`, fuse
//      each to its own NthSegment call sharing the same underlying
//      string + separator.
//
// Either flavour relies on the originating Split being entirely dead
// after rewrite (tracked via the now-unused list local). These tests
// drive each shape and the relevant bail cases.

// buildStraightLineSplitFn synthesises a simple straight-line function
// that:
//
//   parts = value.split(sep)
//   ... callers append `parts[K]`-style reads here
//
// Returns the function, its only block, the value local, the sep
// local (filled in from a string literal), and the parts local for
// downstream tests to add reads against.
func buildStraightLineSplitFn(t *testing.T) (*Function, *BasicBlock, LocalID) {
	t.Helper()
	fn, bb := newTestFunction("split_nth", TString)
	value := fn.NewLocal("value", TString, false, Span{})
	fn.Locals[value].IsParam = true
	fn.Params = []LocalID{value}
	parts := fn.NewLocal("parts", listOfStringT(), false, Span{})
	bb.Instrs = append(bb.Instrs,
		&StorageLiveInstr{Local: parts},
		&IntrinsicInstr{
			Kind: IntrinsicStringSplit,
			Dest: &Place{Local: parts},
			Args: []Operand{
				&CopyOp{Place: Place{Local: value}, T: TString},
				&ConstOp{Const: &StringConst{Value: ","}, T: TString},
			},
		},
	)
	return fn, bb, parts
}

// addPartsKRead appends `let dest = parts[k]` to bb. Returns the
// new dest local so the caller can inspect it (or hand it back into
// the function as the return value to keep the read alive for
// liveness analysis).
func addPartsKRead(fn *Function, bb *BasicBlock, parts LocalID, k int64) LocalID {
	dest := fn.NewLocal("part", TString, false, Span{})
	bb.Instrs = append(bb.Instrs,
		&StorageLiveInstr{Local: dest},
		&AssignInstr{
			Dest: Place{Local: dest},
			Src: &UseRV{Op: &CopyOp{
				Place: Place{Local: parts, Projections: []Projection{
					&IndexProj{
						Index:    &ConstOp{Const: &IntConst{Value: k, T: TInt}, T: TInt},
						ElemType: TString,
					},
				}},
				T: TString,
			}},
		},
	)
	return dest
}

// findAllIntrinsics returns every intrinsic of kind k in bb. Used by
// the multi-index test to count fused calls.
func findAllIntrinsics(bb *BasicBlock, k IntrinsicKind) []*IntrinsicInstr {
	var hits []*IntrinsicInstr
	for _, ins := range bb.Instrs {
		if ii, ok := ins.(*IntrinsicInstr); ok && ii.Kind == k {
			hits = append(hits, ii)
		}
	}
	return hits
}

func TestFuseSplitNth_SingleIndex_StillFuses(t *testing.T) {
	// Regression: the existing markdown-stats `parts[0]` shape must
	// keep firing after the multi-index lift.
	fn, bb, parts := buildStraightLineSplitFn(t)
	_ = addPartsKRead(fn, bb, parts, 0)
	bb.SetTerminator(&ReturnTerm{})

	if !fuseNonEscapingSplitNth(fn) {
		t.Fatalf("expected transform to apply")
	}
	if _, n := findIntrinsic(bb, IntrinsicStringSplit); n != 0 {
		t.Fatalf("Split should be removed; %d remaining", n)
	}
	hits := findAllIntrinsics(bb, IntrinsicStringNthSegment)
	if len(hits) != 1 {
		t.Fatalf("expected 1 NthSegment, got %d", len(hits))
	}
	if k, ok := constArgK(hits[0]); !ok || k != 0 {
		t.Fatalf("expected K=0, got %d ok=%v", k, ok)
	}
}

func TestFuseSplitNth_MultiIndex_FusesEachRead(t *testing.T) {
	// log-aggregator's `let ts = parts[0]; let level = parts[1]`
	// must produce TWO NthSegment calls — one per index — and the
	// originating Split must still be removed.
	fn, bb, parts := buildStraightLineSplitFn(t)
	_ = addPartsKRead(fn, bb, parts, 0)
	_ = addPartsKRead(fn, bb, parts, 1)
	bb.SetTerminator(&ReturnTerm{})

	if !fuseNonEscapingSplitNth(fn) {
		t.Fatalf("expected transform to apply")
	}
	if _, n := findIntrinsic(bb, IntrinsicStringSplit); n != 0 {
		t.Fatalf("Split should be removed after multi-index fuse; %d remaining", n)
	}
	hits := findAllIntrinsics(bb, IntrinsicStringNthSegment)
	if len(hits) != 2 {
		t.Fatalf("expected 2 NthSegment calls, got %d", len(hits))
	}
	// Index ordering: matches sorted by (BB, idx) ascending. The
	// parts[0] read was inserted first, so its NthSegment lives at
	// the lower instruction index — which means K=0 then K=1.
	k0, ok0 := constArgK(hits[0])
	k1, ok1 := constArgK(hits[1])
	if !ok0 || !ok1 || k0 != 0 || k1 != 1 {
		t.Fatalf("expected NthSegment indices [0, 1], got [%d, %d] (ok=%v %v)", k0, k1, ok0, ok1)
	}
}

func TestFuseSplitNth_MultiIndex_NonContiguousIndices(t *testing.T) {
	// User code might do `parts[0]; parts[2]; parts[5]` — sparse
	// indices. Each read is independent; the fusion should rewrite
	// every one regardless of gaps in the K values.
	fn, bb, parts := buildStraightLineSplitFn(t)
	addPartsKRead(fn, bb, parts, 0)
	addPartsKRead(fn, bb, parts, 2)
	_ = addPartsKRead(fn, bb, parts, 5)
	bb.SetTerminator(&ReturnTerm{})

	if !fuseNonEscapingSplitNth(fn) {
		t.Fatalf("expected transform to apply")
	}
	hits := findAllIntrinsics(bb, IntrinsicStringNthSegment)
	if len(hits) != 3 {
		t.Fatalf("expected 3 NthSegment calls, got %d", len(hits))
	}
	want := []int64{0, 2, 5}
	for i, want := range want {
		got, ok := constArgK(hits[i])
		if !ok || got != want {
			t.Fatalf("expected K=%d at position %d, got %d (ok=%v)", want, i, got, ok)
		}
	}
}

func TestFuseSplitNth_BailsOnRepeatedIndex(t *testing.T) {
	// Two reads of `parts[0]` is technically two valid fuse targets
	// but downstream IR consumers don't expect a single source local
	// to be written twice. The pass treats every canonical read as a
	// separate replacement target — confirm the rewrite still
	// produces two NthSegment calls so dead-code elim can drop one
	// later if both dest locals turn out to be identical use chains.
	// (Not strictly a bail, but pinning the behaviour so a future
	// change doesn't accidentally collapse them into a single call.)
	fn, bb, parts := buildStraightLineSplitFn(t)
	_ = addPartsKRead(fn, bb, parts, 0)
	_ = addPartsKRead(fn, bb, parts, 0)
	bb.SetTerminator(&ReturnTerm{})

	if !fuseNonEscapingSplitNth(fn) {
		t.Fatalf("expected transform to apply for repeated K=0 reads")
	}
	hits := findAllIntrinsics(bb, IntrinsicStringNthSegment)
	if len(hits) != 2 {
		t.Fatalf("expected 2 NthSegment calls (one per read), got %d", len(hits))
	}
}

func TestFuseSplitNth_BailsOnPartsLen(t *testing.T) {
	// `parts.len()` lowers to an IntrinsicInstr that takes `parts`
	// as an argument — that's a non-IndexProj read, which the
	// safety check counts as `otherReads` and forces the unfused
	// Split path to remain.
	fn, bb, parts := buildStraightLineSplitFn(t)
	addPartsKRead(fn, bb, parts, 0)
	lenLocal := fn.NewLocal("count", TInt, false, Span{})
	bb.Instrs = append(bb.Instrs,
		&StorageLiveInstr{Local: lenLocal},
		&IntrinsicInstr{
			Kind: IntrinsicListLen,
			Dest: &Place{Local: lenLocal},
			Args: []Operand{
				&CopyOp{Place: Place{Local: parts}, T: listOfStringT()},
			},
		},
	)
	bb.SetTerminator(&ReturnTerm{})

	// Note: the test function returns String per newTestFunction,
	// but ReturnTerm.Value carries an Int. The pass under test
	// doesn't validate types, only structure; this is fine for
	// driving the read-shape analysis.
	if fuseNonEscapingSplitNth(fn) {
		t.Fatalf("expected NO transform — parts.len() should disable fusion")
	}
	if _, n := findIntrinsic(bb, IntrinsicStringSplit); n != 1 {
		t.Fatalf("Split should remain; got %d", n)
	}
}

func TestFuseSplitNth_BailsOnNonConstIndex(t *testing.T) {
	// `parts[i]` with a non-const index forces the unfused path
	// because the runtime intrinsic takes a constant K. Even if
	// other reads are canonical, the presence of one variable-index
	// read means we can't safely drop the parts list.
	fn, bb, parts := buildStraightLineSplitFn(t)
	addPartsKRead(fn, bb, parts, 0)
	i := fn.NewLocal("i", TInt, false, Span{})
	bb.Instrs = append(bb.Instrs,
		&StorageLiveInstr{Local: i},
		&AssignInstr{
			Dest: Place{Local: i},
			Src:  &UseRV{Op: &ConstOp{Const: &IntConst{Value: 1, T: TInt}, T: TInt}},
		},
	)
	dyn := fn.NewLocal("dyn", TString, false, Span{})
	bb.Instrs = append(bb.Instrs,
		&StorageLiveInstr{Local: dyn},
		&AssignInstr{
			Dest: Place{Local: dyn},
			Src: &UseRV{Op: &CopyOp{
				Place: Place{Local: parts, Projections: []Projection{
					&IndexProj{
						Index:    &CopyOp{Place: Place{Local: i}, T: TInt},
						ElemType: TString,
					},
				}},
				T: TString,
			}},
		},
	)
	bb.SetTerminator(&ReturnTerm{})

	if fuseNonEscapingSplitNth(fn) {
		t.Fatalf("expected NO transform — non-const index should disable fusion")
	}
	if _, n := findIntrinsic(bb, IntrinsicStringSplit); n != 1 {
		t.Fatalf("Split should remain when non-const index is present; got %d", n)
	}
}

// constArgK extracts the trailing IntConst K argument of a
// fused IntrinsicStringNthSegment call: NthSegment(value, sep, K).
func constArgK(ii *IntrinsicInstr) (int64, bool) {
	if ii == nil || len(ii.Args) != 3 {
		return 0, false
	}
	co, ok := ii.Args[2].(*ConstOp)
	if !ok {
		return 0, false
	}
	ic, ok := co.Const.(*IntConst)
	if !ok {
		return 0, false
	}
	return ic.Value, true
}
