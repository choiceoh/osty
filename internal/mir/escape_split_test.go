package mir

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/ir"
)

// listOfStringT is the MIR type used by the lowerer for `List<String>`
// — a builtin NamedType `List` parameterised by `String`. The escape
// pass keys off the dest local's type when synthesising the hoisted
// buffer's AggregateRV, so it must round-trip through this exact
// shape.
func listOfStringT() Type {
	return &ir.NamedType{Name: "List", Args: []ir.Type{TString}, Builtin: true}
}

// buildSplitInLoopFn synthesises a function with the structure produced
// by `for row in rows { let parts = row.split(",") ... }`:
//
//	entry → header → body → step → header
//	header → exit (loop break)
//
// Returns the function plus the body block ID so individual tests can
// pin extra instructions into the body to exercise positive / negative
// shapes.
func buildSplitInLoopFn(t *testing.T) (*Function, BlockID, LocalID) {
	t.Helper()
	fn, _ := newTestFunction("split_loop", TInt)
	rowsT := listOfStringT()
	rowsL := fn.NewLocal("rows", rowsT, false, Span{})
	fn.Locals[rowsL].IsParam = true
	fn.Params = []LocalID{rowsL}
	idx := fn.NewLocal("idx", TInt, true, Span{})
	row := fn.NewLocal("row", TString, false, Span{})
	parts := fn.NewLocal("parts", listOfStringT(), false, Span{})

	header := fn.NewBlock(Span{})
	body := fn.NewBlock(Span{})
	step := fn.NewBlock(Span{})
	exit := fn.NewBlock(Span{})

	// entry block already exists (BlockID 0). Wire entry → header.
	entry := fn.Block(fn.Entry)
	entry.Instrs = append(entry.Instrs,
		&StorageLiveInstr{Local: idx},
		&AssignInstr{
			Dest: Place{Local: idx},
			Src:  &UseRV{Op: &ConstOp{Const: &IntConst{Value: 0, T: TInt}, T: TInt}},
		},
	)
	entry.SetTerminator(&GotoTerm{Target: header})

	// header: branch idx<len ? body : exit
	headerBB := fn.Block(header)
	headerBB.SetTerminator(&BranchTerm{
		Cond: &ConstOp{Const: &BoolConst{Value: true}, T: TBool},
		Then: body,
		Else: exit,
	})

	// body: row = rows[idx]; parts = row.split(",")
	bodyBB := fn.Block(body)
	bodyBB.Instrs = append(bodyBB.Instrs,
		&StorageLiveInstr{Local: row},
		&AssignInstr{
			Dest: Place{Local: row},
			Src: &UseRV{Op: &CopyOp{
				Place: Place{Local: rowsL, Projections: []Projection{
					&IndexProj{Index: &CopyOp{Place: Place{Local: idx}, T: TInt}, ElemType: TString},
				}},
				T: TString,
			}},
		},
		&StorageLiveInstr{Local: parts},
		&IntrinsicInstr{
			Kind: IntrinsicStringSplit,
			Dest: &Place{Local: parts},
			Args: []Operand{
				&CopyOp{Place: Place{Local: row}, T: TString},
				&ConstOp{Const: &StringConst{Value: ","}, T: TString},
			},
		},
	)
	bodyBB.SetTerminator(&GotoTerm{Target: step})

	// step: idx += 1; goto header (back edge)
	stepBB := fn.Block(step)
	stepBB.Instrs = append(stepBB.Instrs,
		&AssignInstr{
			Dest: Place{Local: idx},
			Src: &BinaryRV{
				Op:    BinAdd,
				Left:  &CopyOp{Place: Place{Local: idx}, T: TInt},
				Right: &ConstOp{Const: &IntConst{Value: 1, T: TInt}, T: TInt},
				T:     TInt,
			},
		},
	)
	stepBB.SetTerminator(&GotoTerm{Target: header})

	// exit: ReturnTerm
	exitBB := fn.Block(exit)
	exitBB.SetTerminator(&ReturnTerm{})

	return fn, body, parts
}

// findIntrinsic walks the block for a single intrinsic of kind k and
// returns it (or nil + the count when zero or multiple).
func findIntrinsic(bb *BasicBlock, k IntrinsicKind) (*IntrinsicInstr, int) {
	var hits []*IntrinsicInstr
	for _, ins := range bb.Instrs {
		if ii, ok := ins.(*IntrinsicInstr); ok && ii.Kind == k {
			hits = append(hits, ii)
		}
	}
	if len(hits) == 1 {
		return hits[0], 1
	}
	return nil, len(hits)
}

func TestHoistSplit_PositiveBareUseAsIndex(t *testing.T) {
	fn, bodyID, parts := buildSplitInLoopFn(t)
	bodyBB := fn.Block(bodyID)
	// Read parts[0] into a sink (still in the body, before goto step).
	sink := fn.NewLocal("first", TString, false, Span{})
	bodyBB.Instrs = append(bodyBB.Instrs[:len(bodyBB.Instrs)],
		&StorageLiveInstr{Local: sink},
		&AssignInstr{
			Dest: Place{Local: sink},
			Src: &UseRV{Op: &CopyOp{
				Place: Place{Local: parts, Projections: []Projection{
					&IndexProj{
						Index:    &ConstOp{Const: &IntConst{Value: 0, T: TInt}, T: TInt},
						ElemType: TString,
					},
				}},
				T: TString,
			}},
		},
	)
	// Re-hook terminator after splice.
	bodyBB.SetTerminator(bodyBB.Term)

	if !hoistNonEscapingSplitInLoops(fn) {
		t.Fatalf("expected transform to apply")
	}
	if _, n := findIntrinsic(bodyBB, IntrinsicStringSplit); n != 0 {
		t.Fatalf("expected IntrinsicStringSplit to be removed from body, got %d remaining", n)
	}
	splitInto, n := findIntrinsic(bodyBB, IntrinsicStringSplitInto)
	if n != 1 {
		t.Fatalf("expected exactly 1 IntrinsicStringSplitInto in body, got %d", n)
	}
	if len(splitInto.Args) != 3 {
		t.Fatalf("expected SplitInto to have 3 args, got %d", len(splitInto.Args))
	}
	if splitInto.Dest != nil {
		t.Fatalf("expected SplitInto Dest=nil (void result)")
	}
	// Preheader is the entry block (only non-loop predecessor of header).
	entry := fn.Block(fn.Entry)
	hasNew := false
	for _, ins := range entry.Instrs {
		if a, ok := ins.(*AssignInstr); ok {
			if agg, ok := a.Src.(*AggregateRV); ok && agg.Kind == AggList && len(agg.Fields) == 0 {
				hasNew = true
			}
		}
	}
	if !hasNew {
		t.Fatalf("expected hoisted AggregateRV{AggList, []} in entry/preheader")
	}
}

func TestHoistSplit_NegativeReturnEscapes(t *testing.T) {
	fn, bodyID, parts := buildSplitInLoopFn(t)
	bodyBB := fn.Block(bodyID)
	// Make parts the return value (escape via ReturnLocal assignment).
	bodyBB.Instrs = append(bodyBB.Instrs,
		&AssignInstr{
			Dest: Place{Local: fn.ReturnLocal},
			Src: &UseRV{Op: &CopyOp{
				Place: Place{Local: parts},
				T:     listOfStringT(),
			}},
		},
	)
	bodyBB.SetTerminator(bodyBB.Term)
	// fn.ReturnLocal is TInt in the test helper but the test only cares
	// that the transform refuses to fire when the bare list pointer is
	// captured into another local — type mismatch is irrelevant here.

	if hoistNonEscapingSplitInLoops(fn) {
		t.Fatalf("expected transform to refuse: parts is captured by return-local assignment")
	}
}

func TestHoistSplit_NegativeCallArgEscapes(t *testing.T) {
	fn, bodyID, parts := buildSplitInLoopFn(t)
	bodyBB := fn.Block(bodyID)
	// Pass parts as a call argument → escape.
	bodyBB.Instrs = append(bodyBB.Instrs,
		&CallInstr{
			Callee: &FnRef{Symbol: "consume_parts", Type: nil},
			Args: []Operand{
				&CopyOp{Place: Place{Local: parts}, T: listOfStringT()},
			},
		},
	)
	bodyBB.SetTerminator(bodyBB.Term)

	if hoistNonEscapingSplitInLoops(fn) {
		t.Fatalf("expected transform to refuse: parts passed as call argument")
	}
}

func TestHoistSplit_NegativeAggregateFieldEscapes(t *testing.T) {
	fn, bodyID, parts := buildSplitInLoopFn(t)
	bodyBB := fn.Block(bodyID)
	// Box parts into a tuple → escape.
	tupleT := &ir.TupleType{Elems: []ir.Type{listOfStringT()}}
	tupleL := fn.NewLocal("box", tupleT, false, Span{})
	bodyBB.Instrs = append(bodyBB.Instrs,
		&StorageLiveInstr{Local: tupleL},
		&AssignInstr{
			Dest: Place{Local: tupleL},
			Src: &AggregateRV{
				Kind:   AggTuple,
				Fields: []Operand{&CopyOp{Place: Place{Local: parts}, T: listOfStringT()}},
				T:      tupleT,
			},
		},
	)
	bodyBB.SetTerminator(bodyBB.Term)

	if hoistNonEscapingSplitInLoops(fn) {
		t.Fatalf("expected transform to refuse: parts captured into tuple aggregate")
	}
}

func TestHoistSplit_NegativeNoLoop(t *testing.T) {
	fn, _ := newTestFunction("flat", TInt)
	row := fn.NewLocal("row", TString, false, Span{})
	parts := fn.NewLocal("parts", listOfStringT(), false, Span{})
	entry := fn.Block(fn.Entry)
	entry.Instrs = append(entry.Instrs,
		&StorageLiveInstr{Local: row},
		&AssignInstr{
			Dest: Place{Local: row},
			Src:  &UseRV{Op: &ConstOp{Const: &StringConst{Value: "a,b"}, T: TString}},
		},
		&StorageLiveInstr{Local: parts},
		&IntrinsicInstr{
			Kind: IntrinsicStringSplit,
			Dest: &Place{Local: parts},
			Args: []Operand{
				&CopyOp{Place: Place{Local: row}, T: TString},
				&ConstOp{Const: &StringConst{Value: ","}, T: TString},
			},
		},
	)

	if hoistNonEscapingSplitInLoops(fn) {
		t.Fatalf("expected transform to refuse: split is not inside any loop")
	}
}

func TestHoistSplit_PrintMIRRendersOpcode(t *testing.T) {
	// Quick sanity that the new IntrinsicKind round-trips through the
	// printer so debug dumps don't surface "invalid".
	got := IntrinsicStringSplitInto.String()
	if !strings.Contains(got, "split_into") {
		t.Fatalf("expected printer to render new opcode, got %q", got)
	}
}
