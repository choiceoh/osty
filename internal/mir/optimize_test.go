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
