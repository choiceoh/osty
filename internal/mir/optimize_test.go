package mir

import (
	"strings"
	"testing"
)

// helper: build a valid-enough MIR fn with a given return type, a
// single entry block that the caller fills, and a ReturnTerm.
func newTestFunction(name string, retT Type) (*Function, *BasicBlock) {
	fn := &Function{Name: name, ReturnType: retT}
	fn.ReturnLocal = fn.NewLocal("_return", retT, false, Span{})
	fn.Locals[fn.ReturnLocal].IsReturn = true
	entry := fn.NewBlock(Span{})
	fn.Entry = entry
	bb := fn.Block(entry)
	bb.SetTerminator(&ReturnTerm{})
	return fn, bb
}

// ==== const propagation ====

func TestCopyPropagateSubstitutesConstant(t *testing.T) {
	fn, bb := newTestFunction("propagate", TInt)
	n := fn.NewLocal("n", TInt, false, Span{})

	// _n = use const 5
	bb.Instrs = append(bb.Instrs, &AssignInstr{
		Dest: Place{Local: n},
		Src:  &UseRV{Op: &ConstOp{Const: &IntConst{Value: 5, T: TInt}, T: TInt}},
	})
	// _return = _n + const 1
	bb.Instrs = append(bb.Instrs, &AssignInstr{
		Dest: Place{Local: fn.ReturnLocal},
		Src: &BinaryRV{
			Op:    BinAdd,
			Left:  &CopyOp{Place: Place{Local: n}, T: TInt},
			Right: &ConstOp{Const: &IntConst{Value: 1, T: TInt}, T: TInt},
			T:     TInt,
		},
	})

	if !copyPropagateFn(fn) {
		t.Fatalf("expected const prop to report change")
	}

	// The BinaryRV's Left must now be the const.
	bin := bb.Instrs[1].(*AssignInstr).Src.(*BinaryRV)
	if _, ok := bin.Left.(*ConstOp); !ok {
		t.Fatalf("expected Left to be ConstOp after propagation, got %T", bin.Left)
	}
}

func TestCopyPropagateInvalidatesOnWrite(t *testing.T) {
	fn, bb := newTestFunction("reassign", TInt)
	n := fn.NewLocal("n", TInt, false, Span{})

	// _n = const 5
	// _return = _n     (should substitute const 5)
	// _n = const 99
	// _return = _n     (should substitute const 99, not 5)
	bb.Instrs = append(bb.Instrs,
		&AssignInstr{
			Dest: Place{Local: n},
			Src:  &UseRV{Op: &ConstOp{Const: &IntConst{Value: 5, T: TInt}, T: TInt}},
		},
		&AssignInstr{
			Dest: Place{Local: fn.ReturnLocal},
			Src:  &UseRV{Op: &CopyOp{Place: Place{Local: n}, T: TInt}},
		},
		&AssignInstr{
			Dest: Place{Local: n},
			Src:  &UseRV{Op: &ConstOp{Const: &IntConst{Value: 99, T: TInt}, T: TInt}},
		},
		&AssignInstr{
			Dest: Place{Local: fn.ReturnLocal},
			Src:  &UseRV{Op: &CopyOp{Place: Place{Local: n}, T: TInt}},
		},
	)

	copyPropagateFn(fn)

	firstUse := bb.Instrs[1].(*AssignInstr).Src.(*UseRV).Op.(*ConstOp).Const.(*IntConst).Value
	if firstUse != 5 {
		t.Fatalf("first _return: expected const 5, got %d", firstUse)
	}
	secondUse := bb.Instrs[3].(*AssignInstr).Src.(*UseRV).Op.(*ConstOp).Const.(*IntConst).Value
	if secondUse != 99 {
		t.Fatalf("second _return: expected const 99, got %d", secondUse)
	}
}

func TestCopyPropagateLeavesProjectedReadsAlone(t *testing.T) {
	// `_n = const 5; _return = _n.field` must NOT substitute because the
	// read has projections.
	fn, bb := newTestFunction("projected", TInt)
	n := fn.NewLocal("n", TInt, false, Span{})
	bb.Instrs = append(bb.Instrs,
		&AssignInstr{
			Dest: Place{Local: n},
			Src:  &UseRV{Op: &ConstOp{Const: &IntConst{Value: 5, T: TInt}, T: TInt}},
		},
		&AssignInstr{
			Dest: Place{Local: fn.ReturnLocal},
			Src: &UseRV{Op: &CopyOp{
				Place: Place{Local: n, Projections: []Projection{&FieldProj{Index: 0, Name: "x", Type: TInt}}},
				T:     TInt,
			}},
		},
	)
	copyPropagateFn(fn)
	src := bb.Instrs[1].(*AssignInstr).Src.(*UseRV).Op
	if _, ok := src.(*CopyOp); !ok {
		t.Fatalf("projected read must be preserved, got %T", src)
	}
}

// ==== dead assign ====

func TestDeadAssignDropsUnreadLocal(t *testing.T) {
	fn, bb := newTestFunction("deadwrite", TUnit)
	dead := fn.NewLocal("dead", TInt, false, Span{})
	bb.Instrs = append(bb.Instrs,
		&AssignInstr{
			Dest: Place{Local: dead},
			Src:  &UseRV{Op: &ConstOp{Const: &IntConst{Value: 42, T: TInt}, T: TInt}},
		},
		&AssignInstr{
			Dest: Place{Local: fn.ReturnLocal},
			Src:  &UseRV{Op: &ConstOp{Const: &UnitConst{}, T: TUnit}},
		},
	)
	if !deadAssignElim(fn) {
		t.Fatalf("expected dead-assign to report change")
	}
	if len(bb.Instrs) != 1 {
		t.Fatalf("expected 1 instr after dead-assign elim, got %d", len(bb.Instrs))
	}
	if a, ok := bb.Instrs[0].(*AssignInstr); !ok || a.Dest.Local != fn.ReturnLocal {
		t.Fatalf("remaining instr is not the return assign: %T", bb.Instrs[0])
	}
}

func TestDeadAssignKeepsReadLocal(t *testing.T) {
	fn, bb := newTestFunction("used", TInt)
	used := fn.NewLocal("x", TInt, false, Span{})
	bb.Instrs = append(bb.Instrs,
		&AssignInstr{
			Dest: Place{Local: used},
			Src:  &UseRV{Op: &ConstOp{Const: &IntConst{Value: 7, T: TInt}, T: TInt}},
		},
		&AssignInstr{
			Dest: Place{Local: fn.ReturnLocal},
			Src:  &UseRV{Op: &CopyOp{Place: Place{Local: used}, T: TInt}},
		},
	)
	if deadAssignElim(fn) {
		t.Fatalf("dead-assign should not have reported change")
	}
	if len(bb.Instrs) != 2 {
		t.Fatalf("expected 2 instrs, got %d", len(bb.Instrs))
	}
}

func TestDeadAssignRefusesParamAndReturn(t *testing.T) {
	fn := &Function{Name: "guards", ReturnType: TInt}
	fn.ReturnLocal = fn.NewLocal("_return", TInt, false, Span{})
	fn.Locals[fn.ReturnLocal].IsReturn = true
	pid := fn.NewLocal("p", TInt, false, Span{})
	fn.Locals[pid].IsParam = true
	fn.Params = append(fn.Params, pid)
	entry := fn.NewBlock(Span{})
	fn.Entry = entry
	bb := fn.Block(entry)
	// Reassign a param — should not be touched.
	bb.Instrs = append(bb.Instrs, &AssignInstr{
		Dest: Place{Local: pid},
		Src:  &UseRV{Op: &ConstOp{Const: &IntConst{Value: 1, T: TInt}, T: TInt}},
	})
	// Unused return write — also must stay (ReturnTerm implicitly reads it).
	bb.Instrs = append(bb.Instrs, &AssignInstr{
		Dest: Place{Local: fn.ReturnLocal},
		Src:  &UseRV{Op: &ConstOp{Const: &IntConst{Value: 2, T: TInt}, T: TInt}},
	})
	bb.SetTerminator(&ReturnTerm{})
	if deadAssignElim(fn) {
		t.Fatalf("dead-assign should not touch param or return slot")
	}
	if len(bb.Instrs) != 2 {
		t.Fatalf("instrs were modified: %d remaining", len(bb.Instrs))
	}
}

// ==== empty-block collapse ====

func TestCollapseEmptyGotoShortensChain(t *testing.T) {
	fn, entry := newTestFunction("chain", TUnit)
	// entry → b1 → b2 → final, where b1 and b2 are empty gotos and
	// final carries a ReturnTerm.
	b1 := fn.NewBlock(Span{})
	b2 := fn.NewBlock(Span{})
	final := fn.NewBlock(Span{})
	fn.Block(b1).SetTerminator(&GotoTerm{Target: b2})
	fn.Block(b2).SetTerminator(&GotoTerm{Target: final})
	fn.Block(final).SetTerminator(&ReturnTerm{})
	entry.SetTerminator(&GotoTerm{Target: b1})

	if !collapseEmptyGotos(fn) {
		t.Fatalf("expected collapse to report change")
	}
	gt, ok := entry.Term.(*GotoTerm)
	if !ok {
		t.Fatalf("entry terminator changed type: %T", entry.Term)
	}
	if gt.Target != final {
		t.Fatalf("entry goto should point straight at final bb%d, got bb%d", final, gt.Target)
	}
}

func TestCollapseEmptyGotoRetargetsBranchArms(t *testing.T) {
	fn, entry := newTestFunction("branch", TUnit)
	relay := fn.NewBlock(Span{})
	target := fn.NewBlock(Span{})
	fn.Block(relay).SetTerminator(&GotoTerm{Target: target})
	fn.Block(target).SetTerminator(&ReturnTerm{})
	cond := fn.NewLocal("c", TBool, false, Span{})
	entry.SetTerminator(&BranchTerm{
		Cond: &CopyOp{Place: Place{Local: cond}, T: TBool},
		Then: relay,
		Else: target,
	})
	if !collapseEmptyGotos(fn) {
		t.Fatalf("expected collapse to report change")
	}
	br := entry.Term.(*BranchTerm)
	if br.Then != target || br.Else != target {
		t.Fatalf("branch arms not retargeted: Then=bb%d Else=bb%d (want both bb%d)", br.Then, br.Else, target)
	}
}

func TestCollapseEmptyGotoSurvivesCycle(t *testing.T) {
	// A pathological empty-goto cycle: b1 → b2 → b1. The collapser
	// must not loop forever; it should leave the cycle alone.
	fn, entry := newTestFunction("cycle", TUnit)
	b1 := fn.NewBlock(Span{})
	b2 := fn.NewBlock(Span{})
	fn.Block(b1).SetTerminator(&GotoTerm{Target: b2})
	fn.Block(b2).SetTerminator(&GotoTerm{Target: b1})
	entry.SetTerminator(&GotoTerm{Target: b1})
	// Should not hang:
	collapseEmptyGotos(fn)
	if gt, ok := entry.Term.(*GotoTerm); !ok || (gt.Target != b1 && gt.Target != b2) {
		t.Fatalf("cycle handling broke entry goto: %+v", entry.Term)
	}
}

// ==== end-to-end ====

func TestOptimizeRunsAllPassesAndLeavesModuleValid(t *testing.T) {
	fn, bb := newTestFunction("e2e", TInt)
	tmp := fn.NewLocal("tmp", TInt, false, Span{})
	// _tmp = const 41
	// _return = _tmp + const 1
	// …const prop should give _return = 41 + 1, then _tmp becomes dead
	// and its write is dropped.
	bb.Instrs = append(bb.Instrs,
		&AssignInstr{
			Dest: Place{Local: tmp},
			Src:  &UseRV{Op: &ConstOp{Const: &IntConst{Value: 41, T: TInt}, T: TInt}},
		},
		&AssignInstr{
			Dest: Place{Local: fn.ReturnLocal},
			Src: &BinaryRV{
				Op:    BinAdd,
				Left:  &CopyOp{Place: Place{Local: tmp}, T: TInt},
				Right: &ConstOp{Const: &IntConst{Value: 1, T: TInt}, T: TInt},
				T:     TInt,
			},
		},
	)

	mod := &Module{Package: "main", Functions: []*Function{fn}, Layouts: NewLayoutTable()}
	Optimize(mod)

	if errs := Validate(mod); len(errs) > 0 {
		t.Fatalf("optimized module fails validation:\n%v\n\n%s", errs, Print(mod))
	}

	if len(bb.Instrs) != 1 {
		t.Fatalf("expected a single surviving instr, got %d:\n%s", len(bb.Instrs), Print(mod))
	}
	ret := bb.Instrs[0].(*AssignInstr).Src.(*BinaryRV)
	if _, ok := ret.Left.(*ConstOp); !ok {
		t.Fatalf("Left should be ConstOp after optimize, got %T", ret.Left)
	}
}

// Regression: const propagation must reach through IndexProj operands
// embedded inside RValue-held places (DiscriminantRV, LenRV, etc.) —
// the first cut of rewriteRValue treated those as "nothing to rewrite".
func TestCopyPropagateRewritesRValuePlaceIndex(t *testing.T) {
	fn, bb := newTestFunction("reach_through_proj", TInt)
	xs := fn.NewLocal("xs", TInt, false, Span{})
	idx := fn.NewLocal("idx", TInt, false, Span{})
	fn.Locals[xs].IsParam = false // synthetic, not a real param in this test

	// _idx = const 0
	// _return = len _xs[_idx]
	bb.Instrs = append(bb.Instrs,
		&AssignInstr{
			Dest: Place{Local: idx},
			Src:  &UseRV{Op: &ConstOp{Const: &IntConst{Value: 0, T: TInt}, T: TInt}},
		},
		&AssignInstr{
			Dest: Place{Local: fn.ReturnLocal},
			Src: &LenRV{
				Place: Place{
					Local: xs,
					Projections: []Projection{
						&IndexProj{
							Index:    &CopyOp{Place: Place{Local: idx}, T: TInt},
							ElemType: TInt,
						},
					},
				},
				T: TInt,
			},
		},
	)
	copyPropagateFn(fn)
	lr := bb.Instrs[1].(*AssignInstr).Src.(*LenRV)
	ip := lr.Place.Projections[0].(*IndexProj)
	if _, ok := ip.Index.(*ConstOp); !ok {
		t.Fatalf("IndexProj.Index inside LenRV should be const-prop'd, got %T", ip.Index)
	}
}

// After const propagation, a BranchTerm whose condition is now a bool
// literal should collapse to a GotoTerm.
func TestFoldConstantBranch(t *testing.T) {
	fn, entry := newTestFunction("foldbr", TUnit)
	cond := fn.NewLocal("c", TBool, false, Span{})
	thenBB := fn.NewBlock(Span{})
	elseBB := fn.NewBlock(Span{})
	fn.Block(thenBB).SetTerminator(&ReturnTerm{})
	fn.Block(elseBB).SetTerminator(&ReturnTerm{})

	entry.Instrs = append(entry.Instrs, &AssignInstr{
		Dest: Place{Local: cond},
		Src:  &UseRV{Op: &ConstOp{Const: &BoolConst{Value: true}, T: TBool}},
	})
	entry.SetTerminator(&BranchTerm{
		Cond: &CopyOp{Place: Place{Local: cond}, T: TBool},
		Then: thenBB,
		Else: elseBB,
	})
	copyPropagateFn(fn)

	gt, ok := entry.Term.(*GotoTerm)
	if !ok {
		t.Fatalf("expected BranchTerm to fold to GotoTerm, got %T", entry.Term)
	}
	if gt.Target != thenBB {
		t.Fatalf("expected goto -> bb%d (true arm), got bb%d", thenBB, gt.Target)
	}
}

// SwitchIntTerm whose scrutinee is a literal should collapse to a
// GotoTerm at the matching case (or Default when no case matches).
func TestFoldConstantSwitchInt(t *testing.T) {
	fn, entry := newTestFunction("foldsw", TUnit)
	scrut := fn.NewLocal("s", TInt, false, Span{})
	caseA := fn.NewBlock(Span{})
	caseB := fn.NewBlock(Span{})
	def := fn.NewBlock(Span{})
	for _, id := range []BlockID{caseA, caseB, def} {
		fn.Block(id).SetTerminator(&ReturnTerm{})
	}

	entry.Instrs = append(entry.Instrs, &AssignInstr{
		Dest: Place{Local: scrut},
		Src:  &UseRV{Op: &ConstOp{Const: &IntConst{Value: 1, T: TInt}, T: TInt}},
	})
	entry.SetTerminator(&SwitchIntTerm{
		Scrutinee: &CopyOp{Place: Place{Local: scrut}, T: TInt},
		Cases: []SwitchCase{
			{Value: 0, Target: caseA},
			{Value: 1, Target: caseB},
		},
		Default: def,
	})
	copyPropagateFn(fn)

	gt, ok := entry.Term.(*GotoTerm)
	if !ok {
		t.Fatalf("expected SwitchIntTerm to fold to GotoTerm, got %T", entry.Term)
	}
	if gt.Target != caseB {
		t.Fatalf("expected goto -> bb%d (value 1), got bb%d", caseB, gt.Target)
	}
}

// Same, but scrutinee value doesn't match any case — collapse to Default.
func TestFoldConstantSwitchIntFallsThroughToDefault(t *testing.T) {
	fn, entry := newTestFunction("folddef", TUnit)
	scrut := fn.NewLocal("s", TInt, false, Span{})
	caseA := fn.NewBlock(Span{})
	def := fn.NewBlock(Span{})
	fn.Block(caseA).SetTerminator(&ReturnTerm{})
	fn.Block(def).SetTerminator(&ReturnTerm{})
	entry.Instrs = append(entry.Instrs, &AssignInstr{
		Dest: Place{Local: scrut},
		Src:  &UseRV{Op: &ConstOp{Const: &IntConst{Value: 99, T: TInt}, T: TInt}},
	})
	entry.SetTerminator(&SwitchIntTerm{
		Scrutinee: &CopyOp{Place: Place{Local: scrut}, T: TInt},
		Cases:     []SwitchCase{{Value: 0, Target: caseA}},
		Default:   def,
	})
	copyPropagateFn(fn)
	gt, ok := entry.Term.(*GotoTerm)
	if !ok {
		t.Fatalf("expected fold to GotoTerm, got %T", entry.Term)
	}
	if gt.Target != def {
		t.Fatalf("expected goto -> default bb%d, got bb%d", def, gt.Target)
	}
}

// ==== cross-block const propagation ====

// A const defined in the entry block and referenced in a successor
// block must propagate through the Goto edge.
func TestCrossBlockConstPropThroughGoto(t *testing.T) {
	fn, entry := newTestFunction("across", TInt)
	x := fn.NewLocal("x", TInt, false, Span{})
	// entry: _x = const 42; goto body
	entry.Instrs = append(entry.Instrs, &AssignInstr{
		Dest: Place{Local: x},
		Src:  &UseRV{Op: &ConstOp{Const: &IntConst{Value: 42, T: TInt}, T: TInt}},
	})
	body := fn.NewBlock(Span{})
	entry.SetTerminator(&GotoTerm{Target: body})
	bb := fn.Block(body)
	// body: _return = _x + 1; return
	bb.Instrs = append(bb.Instrs, &AssignInstr{
		Dest: Place{Local: fn.ReturnLocal},
		Src: &BinaryRV{
			Op:    BinAdd,
			Left:  &CopyOp{Place: Place{Local: x}, T: TInt},
			Right: &ConstOp{Const: &IntConst{Value: 1, T: TInt}, T: TInt},
			T:     TInt,
		},
	})
	bb.SetTerminator(&ReturnTerm{})

	if !copyPropagateFn(fn) {
		t.Fatalf("expected cross-block prop to report change")
	}
	bin := bb.Instrs[0].(*AssignInstr).Src.(*BinaryRV)
	if _, ok := bin.Left.(*ConstOp); !ok {
		t.Fatalf("expected cross-block const on successor's BinaryRV.Left, got %T", bin.Left)
	}
}

// When the same local is assigned distinct constants in two
// predecessors of a join block, the cross-block analysis must *not*
// propagate — the join sees mixed values and the binding drops.
func TestCrossBlockConstPropDropsOnMismatch(t *testing.T) {
	fn, entry := newTestFunction("mismatch", TInt)
	x := fn.NewLocal("x", TInt, false, Span{})
	c := fn.NewLocal("c", TBool, true, Span{})
	// entry: (assume _c is set externally; don't assign it so the Branch
	// cond isn't const-folded away.) branch _c → [thenBB, elseBB]
	thenBB := fn.NewBlock(Span{})
	elseBB := fn.NewBlock(Span{})
	joinBB := fn.NewBlock(Span{})
	entry.SetTerminator(&BranchTerm{
		Cond: &CopyOp{Place: Place{Local: c}, T: TBool},
		Then: thenBB,
		Else: elseBB,
	})
	// thenBB: _x = const 5; goto join
	fn.Block(thenBB).Instrs = append(fn.Block(thenBB).Instrs, &AssignInstr{
		Dest: Place{Local: x},
		Src:  &UseRV{Op: &ConstOp{Const: &IntConst{Value: 5, T: TInt}, T: TInt}},
	})
	fn.Block(thenBB).SetTerminator(&GotoTerm{Target: joinBB})
	// elseBB: _x = const 7; goto join
	fn.Block(elseBB).Instrs = append(fn.Block(elseBB).Instrs, &AssignInstr{
		Dest: Place{Local: x},
		Src:  &UseRV{Op: &ConstOp{Const: &IntConst{Value: 7, T: TInt}, T: TInt}},
	})
	fn.Block(elseBB).SetTerminator(&GotoTerm{Target: joinBB})
	// joinBB: _return = _x; return
	fn.Block(joinBB).Instrs = append(fn.Block(joinBB).Instrs, &AssignInstr{
		Dest: Place{Local: fn.ReturnLocal},
		Src:  &UseRV{Op: &CopyOp{Place: Place{Local: x}, T: TInt}},
	})
	fn.Block(joinBB).SetTerminator(&ReturnTerm{})

	copyPropagateFn(fn)
	// join's read of _x must NOT have been substituted with a const.
	joinRV := fn.Block(joinBB).Instrs[0].(*AssignInstr).Src.(*UseRV)
	if _, ok := joinRV.Op.(*ConstOp); ok {
		t.Fatalf("join block must not propagate a const across mismatched predecessors: got %T", joinRV.Op)
	}
}

// Matching constants from both arms of a branch do survive the meet —
// the join block sees the same ConstOp on both entries.
func TestCrossBlockConstPropSurvivesMatchingPredecessors(t *testing.T) {
	fn, entry := newTestFunction("agree", TInt)
	x := fn.NewLocal("x", TInt, false, Span{})
	c := fn.NewLocal("c", TBool, true, Span{})
	thenBB := fn.NewBlock(Span{})
	elseBB := fn.NewBlock(Span{})
	joinBB := fn.NewBlock(Span{})
	entry.SetTerminator(&BranchTerm{
		Cond: &CopyOp{Place: Place{Local: c}, T: TBool},
		Then: thenBB,
		Else: elseBB,
	})
	for _, bid := range []BlockID{thenBB, elseBB} {
		fn.Block(bid).Instrs = append(fn.Block(bid).Instrs, &AssignInstr{
			Dest: Place{Local: x},
			Src:  &UseRV{Op: &ConstOp{Const: &IntConst{Value: 9, T: TInt}, T: TInt}},
		})
		fn.Block(bid).SetTerminator(&GotoTerm{Target: joinBB})
	}
	fn.Block(joinBB).Instrs = append(fn.Block(joinBB).Instrs, &AssignInstr{
		Dest: Place{Local: fn.ReturnLocal},
		Src:  &UseRV{Op: &CopyOp{Place: Place{Local: x}, T: TInt}},
	})
	fn.Block(joinBB).SetTerminator(&ReturnTerm{})

	copyPropagateFn(fn)
	joinRV := fn.Block(joinBB).Instrs[0].(*AssignInstr).Src.(*UseRV)
	co, ok := joinRV.Op.(*ConstOp)
	if !ok {
		t.Fatalf("expected matching predecessors to propagate; got %T", joinRV.Op)
	}
	if ic, ok := co.Const.(*IntConst); !ok || ic.Value != 9 {
		t.Fatalf("expected const 9 on join read, got %+v", co.Const)
	}
}

// ==== dead call-dest + storage marker cleanup ====

func TestDeadCallDestNiledWhenResultUnused(t *testing.T) {
	fn, bb := newTestFunction("callvoid", TUnit)
	tmp := fn.NewLocal("tmp", TInt, false, Span{})
	// _tmp = call foo()  — but _tmp is never read. Pass should nil the Dest.
	dest := Place{Local: tmp}
	bb.Instrs = append(bb.Instrs, &CallInstr{
		Dest:   &dest,
		Callee: &FnRef{Symbol: "foo"},
	})
	if !deadAssignElim(fn) {
		t.Fatalf("expected dead-call-dest cleanup to report change")
	}
	if bb.Instrs[0].(*CallInstr).Dest != nil {
		t.Fatalf("call Dest should have been nilled")
	}
}

// Storage{Live,Dead} on a fully dead local should be dropped.
func TestDeadStorageMarkersDropped(t *testing.T) {
	fn, bb := newTestFunction("markers", TUnit)
	dead := fn.NewLocal("dead", TInt, false, Span{})
	bb.Instrs = append(bb.Instrs,
		&StorageLiveInstr{Local: dead},
		&AssignInstr{
			Dest: Place{Local: dead},
			Src:  &UseRV{Op: &ConstOp{Const: &IntConst{Value: 0, T: TInt}, T: TInt}},
		},
		&StorageDeadInstr{Local: dead},
	)
	deadAssignElim(fn)
	// All three should be gone: assign + both markers.
	if len(bb.Instrs) != 0 {
		t.Fatalf("expected all instrs dropped, got %d: %+v", len(bb.Instrs), bb.Instrs)
	}
}

// Storage markers on a *read* local must be preserved.
func TestStorageMarkersKeptForLiveLocal(t *testing.T) {
	fn, bb := newTestFunction("alive", TInt)
	v := fn.NewLocal("v", TInt, false, Span{})
	bb.Instrs = append(bb.Instrs,
		&StorageLiveInstr{Local: v},
		&AssignInstr{
			Dest: Place{Local: v},
			Src:  &UseRV{Op: &ConstOp{Const: &IntConst{Value: 1, T: TInt}, T: TInt}},
		},
		&AssignInstr{
			Dest: Place{Local: fn.ReturnLocal},
			Src:  &UseRV{Op: &CopyOp{Place: Place{Local: v}, T: TInt}},
		},
	)
	deadAssignElim(fn)
	haveLive := false
	for _, i := range bb.Instrs {
		if _, ok := i.(*StorageLiveInstr); ok {
			haveLive = true
		}
	}
	if !haveLive {
		t.Fatalf("StorageLive on live local must be kept; got %+v", bb.Instrs)
	}
}

func TestOptimizeIsIdempotent(t *testing.T) {
	fn, bb := newTestFunction("idem", TInt)
	tmp := fn.NewLocal("tmp", TInt, false, Span{})
	bb.Instrs = append(bb.Instrs,
		&AssignInstr{
			Dest: Place{Local: tmp},
			Src:  &UseRV{Op: &ConstOp{Const: &IntConst{Value: 7, T: TInt}, T: TInt}},
		},
		&AssignInstr{
			Dest: Place{Local: fn.ReturnLocal},
			Src:  &UseRV{Op: &CopyOp{Place: Place{Local: tmp}, T: TInt}},
		},
	)
	mod := &Module{Package: "main", Functions: []*Function{fn}, Layouts: NewLayoutTable()}
	Optimize(mod)
	first := Print(mod)
	Optimize(mod)
	if Print(mod) != first {
		t.Fatalf("Optimize is not idempotent")
	}
	if !strings.Contains(first, "use const 7") {
		t.Fatalf("expected const 7 in optimized output:\n%s", first)
	}
}
