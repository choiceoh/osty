package stage0

import (
	"sort"
	"strconv"
	"strings"

	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/mir"
)

// DeclineCategory tags a declined function with the upstream root-cause
// bucket so PR planning (`docs/stage0_p24_scope.md`) can prioritise
// fixes by observed frequency instead of source-shape intuition. Each
// category captures one distinct gap; a single decline that matches
// multiple is assigned the highest-ROI one (A > B > C > D > E > F).
type DeclineCategory string

const (
	// CategoryUnknown — no detector fired. Bucket size signals the
	// categories that still need to be added.
	CategoryUnknown DeclineCategory = "unknown"
	// CategoryOptionalProj — Cat A: an operand reads a struct field
	// whose type is `Option<T>` via a FieldProj chain. Stage0's
	// projection emitters need a tagged-box ABI for that case (doc §6.1).
	CategoryOptionalProj DeclineCategory = "A-optional-proj"
	// CategoryFnConstArg — Cat B: a CallInstr passes `FnConst` as an
	// argument, often degraded to `ErrType` because the front-end lost
	// the function-pointer type (doc §6.1).
	CategoryFnConstArg DeclineCategory = "B-fnconst-arg"
	// CategoryCallRetLoss — Cat C: a CallInstr's callee has a return
	// type of `*ir.ErrType` — the front-end / IR pipeline erased the
	// callee signature (doc §6.2 `anySemReq` style).
	CategoryCallRetLoss DeclineCategory = "C-call-ret-loss"
	// CategoryEnumAggMatch — Cat D: function has a SwitchIntTerm with
	// three or more cases and at least one block produces an
	// AggregateRV (struct/tuple). The `tyToRepr` shape (doc §1.3).
	CategoryEnumAggMatch DeclineCategory = "D-enum-agg-match"
	// CategoryMultiForList — Cat E: two or more for-in-list induction
	// patterns chained in the same function (`useDeclTailAfter`,
	// doc §1.1).
	CategoryMultiForList DeclineCategory = "E-multi-for-list"
	// CategoryStrEqChain — Cat F: three or more BinaryRV `==` ops
	// between String operands plus a SwitchIntTerm — the
	// `frontTypeReprToString` shape (doc §1.2).
	CategoryStrEqChain DeclineCategory = "F-streq-chain"
)

// DeclineCategoryOrder is the priority order used when a function
// matches multiple detectors. Earlier wins so PR planning sees the
// dominant *upstream MIR* bucket (A/B/C) before the *source-shape*
// buckets (D/E/F). Unknown is last so a fall-through never masks a
// concrete pattern.
var DeclineCategoryOrder = []DeclineCategory{
	CategoryOptionalProj,
	CategoryFnConstArg,
	CategoryCallRetLoss,
	CategoryEnumAggMatch,
	CategoryMultiForList,
	CategoryStrEqChain,
	CategoryUnknown,
}

// ClassifyDecline returns the highest-priority root-cause bucket for
// `fn`. The function is assumed to have already declined; the classifier
// does not re-run the matchers. Detection walks the MIR exactly once
// and short-circuits on the first match in priority order.
func ClassifyDecline(fn *mir.Function) DeclineCategory {
	if fn == nil {
		return CategoryUnknown
	}
	signals := scanDeclineSignals(fn)
	for _, cat := range DeclineCategoryOrder {
		switch cat {
		case CategoryOptionalProj:
			if signals.optionalProj {
				return cat
			}
		case CategoryFnConstArg:
			if signals.fnConstArg {
				return cat
			}
		case CategoryCallRetLoss:
			if signals.callRetLoss {
				return cat
			}
		case CategoryEnumAggMatch:
			if signals.enumAggMatch {
				return cat
			}
		case CategoryMultiForList:
			if signals.multiForList {
				return cat
			}
		case CategoryStrEqChain:
			if signals.strEqChain {
				return cat
			}
		case CategoryUnknown:
			return cat
		}
	}
	return CategoryUnknown
}

// declineSignals collects the boolean indicators produced by a single
// MIR walk. Each detector caps at its earliest hit so the cost is
// O(instrs) regardless of how many categories match.
type declineSignals struct {
	optionalProj bool
	fnConstArg   bool
	callRetLoss  bool
	enumAggMatch bool
	multiForList bool
	strEqChain   bool
}

func scanDeclineSignals(fn *mir.Function) declineSignals {
	var s declineSignals
	switchCases := 0
	hasAggregate := false
	strEqCount := 0
	forInListLoops := countForInListLoops(fn)
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		if sw, ok := bb.Term.(*mir.SwitchIntTerm); ok {
			if c := len(sw.Cases); c > switchCases {
				switchCases = c
			}
		}
		for _, instr := range bb.Instrs {
			switch step := instr.(type) {
			case *mir.AssignInstr:
				if !s.optionalProj && placeHasOptionalLeaf(step.Dest) {
					s.optionalProj = true
				}
				inspectRValue(step.Src, &s, &hasAggregate, &strEqCount)
			case *mir.CallInstr:
				if step.Dest != nil && !s.optionalProj && placeHasOptionalLeaf(*step.Dest) {
					s.optionalProj = true
				}
				if !s.callRetLoss && calleeReturnsErrType(step.Callee) {
					s.callRetLoss = true
				}
				for _, arg := range step.Args {
					inspectOperand(arg, &s)
				}
			case *mir.IntrinsicInstr:
				for _, arg := range step.Args {
					inspectOperand(arg, &s)
				}
			}
		}
	}
	if !s.enumAggMatch && switchCases >= 3 && hasAggregate {
		s.enumAggMatch = true
	}
	if !s.multiForList && forInListLoops >= 2 {
		s.multiForList = true
	}
	if !s.strEqChain && strEqCount >= 3 && switchCases > 0 {
		s.strEqChain = true
	}
	return s
}

func inspectRValue(rv mir.RValue, s *declineSignals, hasAggregate *bool, strEqCount *int) {
	if rv == nil {
		return
	}
	switch r := rv.(type) {
	case *mir.UseRV:
		inspectOperand(r.Op, s)
	case *mir.BinaryRV:
		inspectOperand(r.Left, s)
		inspectOperand(r.Right, s)
		if r.Op == mir.BinEq && operandIsString(r.Left) && operandIsString(r.Right) {
			*strEqCount++
		}
	case *mir.UnaryRV:
		inspectOperand(r.Arg, s)
	case *mir.AggregateRV:
		*hasAggregate = true
		for _, f := range r.Fields {
			inspectOperand(f, s)
		}
	}
}

func inspectOperand(op mir.Operand, s *declineSignals) {
	if op == nil {
		return
	}
	switch o := op.(type) {
	case *mir.CopyOp:
		if !s.optionalProj && placeHasOptionalLeaf(o.Place) {
			s.optionalProj = true
		}
	case *mir.MoveOp:
		if !s.optionalProj && placeHasOptionalLeaf(o.Place) {
			s.optionalProj = true
		}
	case *mir.ConstOp:
		if !s.fnConstArg {
			if _, ok := o.Const.(*mir.FnConst); ok {
				s.fnConstArg = true
			}
		}
	}
}

// placeHasOptionalLeaf reports whether the last FieldProj/TupleProj of
// `p` lands on a slot whose declared Type is `*ir.OptionalType`. The
// VariantProj case is excluded — those project into Option/Result
// payloads and stage0 already handles that path via
// `resolveProjectedOptionPayloadSlot`.
func placeHasOptionalLeaf(p mir.Place) bool {
	if len(p.Projections) == 0 {
		return false
	}
	last := p.Projections[len(p.Projections)-1]
	switch leaf := last.(type) {
	case *mir.FieldProj:
		return typeIsOptional(leaf.Type)
	case *mir.TupleProj:
		return typeIsOptional(leaf.Type)
	}
	return false
}

func typeIsOptional(t mir.Type) bool {
	_, ok := t.(*ir.OptionalType)
	return ok
}

// calleeReturnsErrType reports whether a CallInstr's callee carries a
// return type that the front-end has degraded to `*ir.ErrType`. The
// classifier conflates `FnRef.Type = *ir.ErrType` (signature lost) and
// `FnRef.Type` whose return slot is `*ir.ErrType` (return-only loss);
// both are doc §6.2 symptoms that block stage0's aggregate constructor.
func calleeReturnsErrType(callee mir.Callee) bool {
	ref, ok := callee.(*mir.FnRef)
	if !ok || ref == nil {
		return false
	}
	if _, ok := ref.Type.(*ir.ErrType); ok {
		return true
	}
	fnTy, ok := ref.Type.(*ir.FnType)
	if !ok || fnTy == nil {
		return false
	}
	_, ok = fnTy.Return.(*ir.ErrType)
	return ok
}

func operandIsString(op mir.Operand) bool {
	if op == nil {
		return false
	}
	t := op.Type()
	prim, ok := t.(*ir.PrimType)
	return ok && prim != nil && prim.Kind == ir.PrimString
}

// countForInListLoops approximates the number of for-in-list induction
// patterns. A for-in-list MIR subgraph has a head block whose terminator
// branches on `idx < len` and whose body block increments idx by 1; we
// count those headers as a stand-in. Over-counts nested loops by one
// per nesting level — the categoriser only needs `>= 2`, so the
// approximation is tight enough.
func countForInListLoops(fn *mir.Function) int {
	count := 0
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		br, ok := bb.Term.(*mir.BranchTerm)
		if !ok {
			continue
		}
		if !branchCondIsLessThan(br.Cond, bb) {
			continue
		}
		count++
	}
	return count
}

// branchCondIsLessThan recognises the `idx < len` comparison emitted
// by the for-in-list induction lowering. The pattern is conservative —
// it only matches when the assignment that produced the cond is the
// last instr in `bb` and is `Binary(Lt, Copy(idx), Copy(len))`.
func branchCondIsLessThan(cond mir.Operand, bb *mir.BasicBlock) bool {
	cp, ok := cond.(*mir.CopyOp)
	if !ok || cp.Place.HasProjections() {
		return false
	}
	condLocal := cp.Place.Local
	if len(bb.Instrs) == 0 {
		return false
	}
	last := bb.Instrs[len(bb.Instrs)-1]
	ai, ok := last.(*mir.AssignInstr)
	if !ok || ai.Dest.HasProjections() || ai.Dest.Local != condLocal {
		return false
	}
	bin, ok := ai.Src.(*mir.BinaryRV)
	if !ok {
		return false
	}
	return bin.Op == mir.BinLt
}

// CategoryBuckets aggregates ClassifyDecline calls into a sorted view
// suitable for the audit-test summary. The slice is sorted by
// descending count so the dominant root cause is the first row.
type CategoryBuckets []CategoryBucket

// CategoryBucket is one row of the audit-test classifier summary.
type CategoryBucket struct {
	Category DeclineCategory
	Count    int
}

// NewCategoryBuckets collects per-function categories into a sorted
// bucket list. Categories with zero count are preserved at the tail so
// `--50%` deltas can be computed against a stable baseline.
func NewCategoryBuckets(perFn []DeclineCategory) CategoryBuckets {
	totals := make(map[DeclineCategory]int, len(DeclineCategoryOrder))
	for _, c := range DeclineCategoryOrder {
		totals[c] = 0
	}
	for _, c := range perFn {
		if _, ok := totals[c]; !ok {
			totals[c] = 0
		}
		totals[c]++
	}
	out := make(CategoryBuckets, 0, len(totals))
	for c, n := range totals {
		out = append(out, CategoryBucket{Category: c, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return string(out[i].Category) < string(out[j].Category)
	})
	return out
}

// Render formats the buckets as a single human-readable string used by
// the audit-test summary and the LIST_ALL_DECLINES footer.
func (b CategoryBuckets) Render() string {
	var sb strings.Builder
	for _, row := range b {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString("  ")
		sb.WriteString(string(row.Category))
		sb.WriteString(" = ")
		sb.WriteString(strconv.Itoa(row.Count))
	}
	return sb.String()
}
