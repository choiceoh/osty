package ir

import (
	"math/big"
	"strconv"
	"strings"
)

// OptimizeOptions tunes the IR-level optimisation pipeline. The zero
// value enables every pass — pass a populated struct to disable
// specific passes during debugging.
type OptimizeOptions struct {
	DisableConstFold bool
	DisableDeadCode  bool
	DisableBranch    bool
	DisableSimplify  bool
}

// Optimize runs the standard IR optimisation pipeline on m in place
// and returns the same module for chaining. The passes are deliberately
// conservative: they only rewrite shapes that are provably safe at
// this level, so the result remains valid input for every backend.
//
// The optimiser is idempotent — running it twice on the same module
// produces the same IR the first run emitted.
func Optimize(m *Module, opts OptimizeOptions) *Module {
	if m == nil {
		return m
	}
	o := &optimizer{opts: opts}
	o.visitModule(m)
	return m
}

type optimizer struct {
	opts OptimizeOptions
}

func (o *optimizer) visitModule(m *Module) {
	for _, d := range m.Decls {
		o.visitDecl(d)
	}
	for i, s := range m.Script {
		m.Script[i] = o.visitStmt(s)
	}
	m.Script = o.simplifyStmts(m.Script)
}

func (o *optimizer) visitDecl(d Decl) {
	switch d := d.(type) {
	case *FnDecl:
		if d.Body != nil {
			o.visitBlock(d.Body)
		}
	case *StructDecl:
		for _, f := range d.Fields {
			if f.Default != nil {
				f.Default = o.visitExpr(f.Default)
			}
		}
		for _, m := range d.Methods {
			if m.Body != nil {
				o.visitBlock(m.Body)
			}
		}
	case *EnumDecl:
		for _, m := range d.Methods {
			if m.Body != nil {
				o.visitBlock(m.Body)
			}
		}
	case *InterfaceDecl:
		for _, m := range d.Methods {
			if m.Body != nil {
				o.visitBlock(m.Body)
			}
		}
	case *LetDecl:
		if d.Value != nil {
			d.Value = o.visitExpr(d.Value)
		}
	}
}

func (o *optimizer) visitBlock(b *Block) {
	if b == nil {
		return
	}
	for i, s := range b.Stmts {
		b.Stmts[i] = o.visitStmt(s)
	}
	if b.Result != nil {
		b.Result = o.visitExpr(b.Result)
	}
	b.Stmts = o.simplifyStmts(b.Stmts)
}

// simplifyStmts applies dead-code elimination: statements following a
// terminator (return/break/continue) are unreachable and dropped.
func (o *optimizer) simplifyStmts(stmts []Stmt) []Stmt {
	if o.opts.DisableDeadCode {
		return stmts
	}
	for i, s := range stmts {
		if isTerminator(s) {
			return stmts[:i+1]
		}
	}
	return stmts
}

func isTerminator(s Stmt) bool {
	switch s.(type) {
	case *ReturnStmt, *BreakStmt, *ContinueStmt:
		return true
	}
	return false
}

func (o *optimizer) visitStmt(s Stmt) Stmt {
	switch s := s.(type) {
	case nil:
		return nil
	case *Block:
		o.visitBlock(s)
		return s
	case *LetStmt:
		if s.Value != nil {
			s.Value = o.visitExpr(s.Value)
		}
		return s
	case *ExprStmt:
		s.X = o.visitExpr(s.X)
		return s
	case *AssignStmt:
		for i, t := range s.Targets {
			s.Targets[i] = o.visitExpr(t)
		}
		s.Value = o.visitExpr(s.Value)
		return s
	case *ReturnStmt:
		if s.Value != nil {
			s.Value = o.visitExpr(s.Value)
		}
		return s
	case *IfStmt:
		s.Cond = o.visitExpr(s.Cond)
		o.visitBlock(s.Then)
		o.visitBlock(s.Else)
		// Fold `if true`/`if false` when the branch is a no-op side-effectless shape.
		if !o.opts.DisableBranch {
			if bl, ok := s.Cond.(*BoolLit); ok {
				if bl.Value {
					return s.Then
				}
				if s.Else != nil {
					return s.Else
				}
				return &Block{SpanV: s.SpanV}
			}
		}
		return s
	case *ForStmt:
		if s.Cond != nil {
			s.Cond = o.visitExpr(s.Cond)
		}
		if s.Iter != nil {
			s.Iter = o.visitExpr(s.Iter)
		}
		if s.Start != nil {
			s.Start = o.visitExpr(s.Start)
		}
		if s.End != nil {
			s.End = o.visitExpr(s.End)
		}
		o.visitBlock(s.Body)
		return s
	case *DeferStmt:
		o.visitBlock(s.Body)
		return s
	case *ChanSendStmt:
		s.Channel = o.visitExpr(s.Channel)
		s.Value = o.visitExpr(s.Value)
		return s
	case *MatchStmt:
		s.Scrutinee = o.visitExpr(s.Scrutinee)
		for _, arm := range s.Arms {
			o.visitMatchArm(arm)
		}
		return s
	}
	return s
}

func (o *optimizer) visitMatchArm(a *MatchArm) {
	if a == nil {
		return
	}
	if a.Guard != nil {
		a.Guard = o.visitExpr(a.Guard)
	}
	if a.Body != nil {
		o.visitBlock(a.Body)
	}
}

func (o *optimizer) visitExpr(e Expr) Expr {
	switch e := e.(type) {
	case nil:
		return nil
	case *UnaryExpr:
		e.X = o.visitExpr(e.X)
		if folded, ok := o.foldUnary(e); ok {
			return folded
		}
		return e
	case *BinaryExpr:
		e.Left = o.visitExpr(e.Left)
		e.Right = o.visitExpr(e.Right)
		if folded, ok := o.foldBinary(e); ok {
			return folded
		}
		if simplified, ok := o.simplifyBinary(e); ok {
			return simplified
		}
		return e
	case *CallExpr:
		e.Callee = o.visitExpr(e.Callee)
		for i := range e.Args {
			e.Args[i].Value = o.visitExpr(e.Args[i].Value)
		}
		return e
	case *IntrinsicCall:
		for i := range e.Args {
			e.Args[i].Value = o.visitExpr(e.Args[i].Value)
		}
		return e
	case *MethodCall:
		e.Receiver = o.visitExpr(e.Receiver)
		for i := range e.Args {
			e.Args[i].Value = o.visitExpr(e.Args[i].Value)
		}
		return e
	case *ListLit:
		for i, el := range e.Elems {
			e.Elems[i] = o.visitExpr(el)
		}
		return e
	case *MapLit:
		for i := range e.Entries {
			e.Entries[i].Key = o.visitExpr(e.Entries[i].Key)
			e.Entries[i].Value = o.visitExpr(e.Entries[i].Value)
		}
		return e
	case *TupleLit:
		for i, el := range e.Elems {
			e.Elems[i] = o.visitExpr(el)
		}
		return e
	case *StructLit:
		for i := range e.Fields {
			if e.Fields[i].Value != nil {
				e.Fields[i].Value = o.visitExpr(e.Fields[i].Value)
			}
		}
		if e.Spread != nil {
			e.Spread = o.visitExpr(e.Spread)
		}
		return e
	case *RangeLit:
		if e.Start != nil {
			e.Start = o.visitExpr(e.Start)
		}
		if e.End != nil {
			e.End = o.visitExpr(e.End)
		}
		return e
	case *QuestionExpr:
		e.X = o.visitExpr(e.X)
		return e
	case *CoalesceExpr:
		e.Left = o.visitExpr(e.Left)
		e.Right = o.visitExpr(e.Right)
		return e
	case *FieldExpr:
		e.X = o.visitExpr(e.X)
		return e
	case *TupleAccess:
		e.X = o.visitExpr(e.X)
		// fold (a, b).i when the base is a tuple literal.
		if tl, ok := e.X.(*TupleLit); ok && e.Index >= 0 && e.Index < len(tl.Elems) && !o.opts.DisableSimplify {
			return tl.Elems[e.Index]
		}
		return e
	case *IndexExpr:
		e.X = o.visitExpr(e.X)
		e.Index = o.visitExpr(e.Index)
		return e
	case *Closure:
		if e.Body != nil {
			o.visitBlock(e.Body)
		}
		return e
	case *VariantLit:
		for i := range e.Args {
			e.Args[i].Value = o.visitExpr(e.Args[i].Value)
		}
		return e
	case *BlockExpr:
		o.visitBlock(e.Block)
		// `{ expr }` with no statements collapses to expr when the types agree.
		if !o.opts.DisableSimplify && e.Block != nil && len(e.Block.Stmts) == 0 && e.Block.Result != nil {
			return e.Block.Result
		}
		return e
	case *IfExpr:
		e.Cond = o.visitExpr(e.Cond)
		o.visitBlock(e.Then)
		o.visitBlock(e.Else)
		if !o.opts.DisableBranch {
			if bl, ok := e.Cond.(*BoolLit); ok {
				if bl.Value {
					return blockResult(e.Then, e.T)
				}
				return blockResult(e.Else, e.T)
			}
		}
		return e
	case *IfLetExpr:
		e.Scrutinee = o.visitExpr(e.Scrutinee)
		o.visitBlock(e.Then)
		o.visitBlock(e.Else)
		return e
	case *MatchExpr:
		e.Scrutinee = o.visitExpr(e.Scrutinee)
		for _, arm := range e.Arms {
			o.visitMatchArm(arm)
		}
		return e
	case *StringLit:
		for i := range e.Parts {
			if !e.Parts[i].IsLit && e.Parts[i].Expr != nil {
				e.Parts[i].Expr = o.visitExpr(e.Parts[i].Expr)
			}
		}
		return e
	}
	return e
}

// blockResult returns the block's final expression wrapped in a
// BlockExpr when the block still has stmts, or the raw result when it
// is pure. Used by the if-const-fold rewrite.
func blockResult(b *Block, t Type) Expr {
	if b == nil {
		return &UnitLit{}
	}
	if len(b.Stmts) == 0 && b.Result != nil {
		return b.Result
	}
	return &BlockExpr{Block: b, T: t, SpanV: b.SpanV}
}

// ==== Constant folding ====

func (o *optimizer) foldUnary(e *UnaryExpr) (Expr, bool) {
	if o.opts.DisableConstFold {
		return nil, false
	}
	switch e.Op {
	case UnNeg:
		if lit, ok := e.X.(*IntLit); ok {
			if v, ok := parseIntText(lit.Text); ok {
				v.Neg(v)
				return &IntLit{Text: v.String(), T: e.T, SpanV: e.SpanV}, true
			}
		}
	case UnPlus:
		if _, ok := e.X.(*IntLit); ok {
			return e.X, true
		}
		if _, ok := e.X.(*FloatLit); ok {
			return e.X, true
		}
	case UnNot:
		if lit, ok := e.X.(*BoolLit); ok {
			return &BoolLit{Value: !lit.Value, SpanV: e.SpanV}, true
		}
	case UnBitNot:
		if lit, ok := e.X.(*IntLit); ok {
			if v, ok := parseIntText(lit.Text); ok {
				v.Not(v)
				return &IntLit{Text: v.String(), T: e.T, SpanV: e.SpanV}, true
			}
		}
	}
	return nil, false
}

func (o *optimizer) foldBinary(e *BinaryExpr) (Expr, bool) {
	if o.opts.DisableConstFold {
		return nil, false
	}
	// Integer arithmetic / comparison
	if li, ok := e.Left.(*IntLit); ok {
		if ri, ok := e.Right.(*IntLit); ok {
			return foldIntBinary(e, li, ri)
		}
	}
	// Boolean logic
	if lb, ok := e.Left.(*BoolLit); ok {
		if rb, ok := e.Right.(*BoolLit); ok {
			return foldBoolBinary(e, lb, rb)
		}
	}
	// Short-circuit on one-sided bool literal
	if e.Op == BinAnd {
		if lb, ok := e.Left.(*BoolLit); ok {
			if !lb.Value {
				return &BoolLit{Value: false, SpanV: e.SpanV}, true
			}
			return e.Right, true
		}
	}
	if e.Op == BinOr {
		if lb, ok := e.Left.(*BoolLit); ok {
			if lb.Value {
				return &BoolLit{Value: true, SpanV: e.SpanV}, true
			}
			return e.Right, true
		}
	}
	// String concatenation of two literal-only strings
	if e.Op == BinAdd {
		if ls, ok := e.Left.(*StringLit); ok && isPureStringLit(ls) {
			if rs, ok := e.Right.(*StringLit); ok && isPureStringLit(rs) {
				return &StringLit{
					Parts:    []StringPart{{IsLit: true, Lit: stringLitText(ls) + stringLitText(rs)}},
					IsRaw:    ls.IsRaw && rs.IsRaw,
					IsTriple: false,
					SpanV:    e.SpanV,
				}, true
			}
		}
	}
	return nil, false
}

func (o *optimizer) simplifyBinary(e *BinaryExpr) (Expr, bool) {
	if o.opts.DisableSimplify {
		return nil, false
	}
	// x + 0, 0 + x, x - 0
	if e.Op == BinAdd {
		if isIntLiteralZero(e.Right) {
			return e.Left, true
		}
		if isIntLiteralZero(e.Left) {
			return e.Right, true
		}
	}
	if e.Op == BinSub && isIntLiteralZero(e.Right) {
		return e.Left, true
	}
	// x * 1, 1 * x
	if e.Op == BinMul {
		if isIntLiteralOne(e.Right) {
			return e.Left, true
		}
		if isIntLiteralOne(e.Left) {
			return e.Right, true
		}
		if isIntLiteralZero(e.Right) || isIntLiteralZero(e.Left) {
			return &IntLit{Text: "0", T: e.T, SpanV: e.SpanV}, true
		}
	}
	// x / 1
	if e.Op == BinDiv && isIntLiteralOne(e.Right) {
		return e.Left, true
	}
	return nil, false
}

func foldIntBinary(e *BinaryExpr, l, r *IntLit) (Expr, bool) {
	lv, lok := parseIntText(l.Text)
	rv, rok := parseIntText(r.Text)
	if !lok || !rok {
		return nil, false
	}
	out := new(big.Int)
	switch e.Op {
	case BinAdd:
		out.Add(lv, rv)
	case BinSub:
		out.Sub(lv, rv)
	case BinMul:
		out.Mul(lv, rv)
	case BinDiv:
		if rv.Sign() == 0 {
			return nil, false
		}
		out.Quo(lv, rv)
	case BinMod:
		if rv.Sign() == 0 {
			return nil, false
		}
		out.Rem(lv, rv)
	case BinBitAnd:
		out.And(lv, rv)
	case BinBitOr:
		out.Or(lv, rv)
	case BinBitXor:
		out.Xor(lv, rv)
	case BinShl:
		if !rv.IsInt64() || rv.Int64() < 0 || rv.Int64() > 1024 {
			return nil, false
		}
		out.Lsh(lv, uint(rv.Int64()))
	case BinShr:
		if !rv.IsInt64() || rv.Int64() < 0 || rv.Int64() > 1024 {
			return nil, false
		}
		out.Rsh(lv, uint(rv.Int64()))
	case BinEq:
		return &BoolLit{Value: lv.Cmp(rv) == 0, SpanV: e.SpanV}, true
	case BinNeq:
		return &BoolLit{Value: lv.Cmp(rv) != 0, SpanV: e.SpanV}, true
	case BinLt:
		return &BoolLit{Value: lv.Cmp(rv) < 0, SpanV: e.SpanV}, true
	case BinLeq:
		return &BoolLit{Value: lv.Cmp(rv) <= 0, SpanV: e.SpanV}, true
	case BinGt:
		return &BoolLit{Value: lv.Cmp(rv) > 0, SpanV: e.SpanV}, true
	case BinGeq:
		return &BoolLit{Value: lv.Cmp(rv) >= 0, SpanV: e.SpanV}, true
	default:
		return nil, false
	}
	return &IntLit{Text: out.String(), T: e.T, SpanV: e.SpanV}, true
}

func foldBoolBinary(e *BinaryExpr, l, r *BoolLit) (Expr, bool) {
	switch e.Op {
	case BinAnd:
		return &BoolLit{Value: l.Value && r.Value, SpanV: e.SpanV}, true
	case BinOr:
		return &BoolLit{Value: l.Value || r.Value, SpanV: e.SpanV}, true
	case BinEq:
		return &BoolLit{Value: l.Value == r.Value, SpanV: e.SpanV}, true
	case BinNeq:
		return &BoolLit{Value: l.Value != r.Value, SpanV: e.SpanV}, true
	}
	return nil, false
}

// parseIntText parses an IntLit's original text (handling 0x/0o/0b
// prefixes and underscore separators) into a big.Int for arbitrary-
// precision folding.
func parseIntText(s string) (*big.Int, bool) {
	text := strings.ReplaceAll(s, "_", "")
	neg := false
	if strings.HasPrefix(text, "+") {
		text = text[1:]
	} else if strings.HasPrefix(text, "-") {
		neg = true
		text = text[1:]
	}
	base := 10
	switch {
	case strings.HasPrefix(text, "0x") || strings.HasPrefix(text, "0X"):
		base = 16
		text = text[2:]
	case strings.HasPrefix(text, "0o") || strings.HasPrefix(text, "0O"):
		base = 8
		text = text[2:]
	case strings.HasPrefix(text, "0b") || strings.HasPrefix(text, "0B"):
		base = 2
		text = text[2:]
	}
	if text == "" {
		return nil, false
	}
	if _, err := strconv.ParseInt(text, base, 64); err != nil {
		// fall back to big.Int (arbitrary precision literals)
		v, ok := new(big.Int).SetString(text, base)
		if !ok {
			return nil, false
		}
		if neg {
			v.Neg(v)
		}
		return v, true
	}
	v, _ := new(big.Int).SetString(text, base)
	if v == nil {
		return nil, false
	}
	if neg {
		v.Neg(v)
	}
	return v, true
}

// isIntLiteralZero / isIntLiteralOne recognise the common algebraic
// identity operands. Returning false on parse failure is safe: we just
// skip the simplification.
func isIntLiteralZero(e Expr) bool {
	lit, ok := e.(*IntLit)
	if !ok {
		return false
	}
	v, ok := parseIntText(lit.Text)
	return ok && v.Sign() == 0
}

func isIntLiteralOne(e Expr) bool {
	lit, ok := e.(*IntLit)
	if !ok {
		return false
	}
	v, ok := parseIntText(lit.Text)
	return ok && v.Cmp(big.NewInt(1)) == 0
}

func isPureStringLit(s *StringLit) bool {
	if s == nil {
		return false
	}
	for _, p := range s.Parts {
		if !p.IsLit {
			return false
		}
	}
	return true
}

func stringLitText(s *StringLit) string {
	var b strings.Builder
	for _, p := range s.Parts {
		if p.IsLit {
			b.WriteString(p.Lit)
		}
	}
	return b.String()
}
