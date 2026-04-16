package ir

// ComputeCaptures walks a closure body and returns the free variables
// referenced inside it that are not bound by the closure itself. The
// result is stable: the order mirrors first-reference appearance in
// the body, and each capture appears at most once.
//
// params is the closure's parameter list; ComputeCaptures treats
// those names as locally bound and therefore not captures. Nested
// closures are NOT re-walked: if the nested closure already carries
// Captures, those entries are promoted to outer captures whenever the
// referenced name is not introduced by the outer closure itself. This
// matches the semantics a lambda-lifting backend needs.
//
// This helper is deliberately name-scoped rather than symbol-scoped:
// by the time lowering finishes, the IR no longer carries resolver
// symbols, and backends reason about idents by name. When two
// same-named bindings exist in nested scopes, only the innermost wins
// — which aligns with how a tree-walk interpreter resolves names.
func ComputeCaptures(body *Block, params []*Param) []*Capture {
	if body == nil {
		return nil
	}
	a := &captureAnalyzer{
		bound:   map[string]bool{},
		seen:    map[string]bool{},
		results: nil,
	}
	for _, p := range params {
		a.markParamBound(p)
	}
	a.block(body)
	return a.results
}

type captureAnalyzer struct {
	bound   map[string]bool // names introduced by the closure itself
	seen    map[string]bool // names already emitted as captures
	results []*Capture
}

func (a *captureAnalyzer) markParamBound(p *Param) {
	if p == nil {
		return
	}
	if p.Name != "" {
		a.bound[p.Name] = true
		return
	}
	if p.Pattern != nil {
		a.patternNames(p.Pattern)
	}
}

// patternNames records every binding name that `p` introduces.
func (a *captureAnalyzer) patternNames(p Pattern) {
	switch p := p.(type) {
	case nil, *WildPat, *LitPat, *RangePat, *ErrorPat:
		return
	case *IdentPat:
		a.bound[p.Name] = true
	case *BindingPat:
		a.bound[p.Name] = true
		a.patternNames(p.Pattern)
	case *TuplePat:
		for _, e := range p.Elems {
			a.patternNames(e)
		}
	case *StructPat:
		for _, f := range p.Fields {
			if f.Pattern == nil {
				a.bound[f.Name] = true
			} else {
				a.patternNames(f.Pattern)
			}
		}
	case *VariantPat:
		for _, arg := range p.Args {
			a.patternNames(arg)
		}
	case *OrPat:
		// An or-pattern's alternatives must bind the same names; walking
		// one is sufficient.
		if len(p.Alts) > 0 {
			a.patternNames(p.Alts[0])
		}
	}
}

// record emits a capture entry unless `name` is bound or already seen.
func (a *captureAnalyzer) record(name string, kind CaptureKind, t Type, mut bool, span Span) {
	if name == "" || name == "_" {
		return
	}
	if a.bound[name] {
		return
	}
	if a.seen[name] {
		return
	}
	a.seen[name] = true
	a.results = append(a.results, &Capture{
		Name:  name,
		Kind:  kind,
		T:     t,
		Mut:   mut,
		SpanV: span,
	})
}

func (a *captureAnalyzer) block(b *Block) {
	if b == nil {
		return
	}
	// Save binding set so inner `let` introductions don't leak out.
	saved := a.snapshot()
	for _, s := range b.Stmts {
		a.stmt(s)
	}
	if b.Result != nil {
		a.expr(b.Result)
	}
	a.restore(saved)
}

func (a *captureAnalyzer) snapshot() map[string]bool {
	out := make(map[string]bool, len(a.bound))
	for k, v := range a.bound {
		out[k] = v
	}
	return out
}

func (a *captureAnalyzer) restore(snap map[string]bool) {
	a.bound = snap
}

func (a *captureAnalyzer) stmt(s Stmt) {
	switch s := s.(type) {
	case nil:
		return
	case *Block:
		a.block(s)
	case *LetStmt:
		if s.Value != nil {
			a.expr(s.Value)
		}
		if s.Name != "" {
			a.bound[s.Name] = true
		} else if s.Pattern != nil {
			a.patternNames(s.Pattern)
		}
	case *ExprStmt:
		a.expr(s.X)
	case *AssignStmt:
		for _, t := range s.Targets {
			a.expr(t)
		}
		a.expr(s.Value)
	case *ReturnStmt:
		if s.Value != nil {
			a.expr(s.Value)
		}
	case *IfStmt:
		a.expr(s.Cond)
		a.block(s.Then)
		if s.Else != nil {
			a.block(s.Else)
		}
	case *ForStmt:
		saved := a.snapshot()
		if s.Var != "" {
			a.bound[s.Var] = true
		}
		if s.Pattern != nil {
			a.patternNames(s.Pattern)
		}
		if s.Cond != nil {
			a.expr(s.Cond)
		}
		if s.Iter != nil {
			a.expr(s.Iter)
		}
		if s.Start != nil {
			a.expr(s.Start)
		}
		if s.End != nil {
			a.expr(s.End)
		}
		a.block(s.Body)
		a.restore(saved)
	case *DeferStmt:
		a.block(s.Body)
	case *ChanSendStmt:
		a.expr(s.Channel)
		a.expr(s.Value)
	case *MatchStmt:
		a.expr(s.Scrutinee)
		for _, arm := range s.Arms {
			a.matchArm(arm)
		}
	case *BreakStmt, *ContinueStmt, *ErrorStmt:
		// no free variables
	}
}

func (a *captureAnalyzer) matchArm(arm *MatchArm) {
	if arm == nil {
		return
	}
	saved := a.snapshot()
	if arm.Pattern != nil {
		a.patternNames(arm.Pattern)
	}
	if arm.Guard != nil {
		a.expr(arm.Guard)
	}
	a.block(arm.Body)
	a.restore(saved)
}

func (a *captureAnalyzer) expr(e Expr) {
	switch e := e.(type) {
	case nil:
		return
	case *IntLit, *FloatLit, *BoolLit, *CharLit, *ByteLit, *UnitLit, *ErrorExpr:
		return
	case *StringLit:
		for _, p := range e.Parts {
			if !p.IsLit && p.Expr != nil {
				a.expr(p.Expr)
			}
		}
	case *Ident:
		kind := captureKindForIdent(e.Kind)
		if kind == CaptureUnknown {
			return
		}
		a.record(e.Name, kind, e.T, false, e.SpanV)
	case *UnaryExpr:
		a.expr(e.X)
	case *BinaryExpr:
		a.expr(e.Left)
		a.expr(e.Right)
	case *CallExpr:
		a.expr(e.Callee)
		for _, arg := range e.Args {
			a.expr(arg.Value)
		}
	case *IntrinsicCall:
		for _, arg := range e.Args {
			a.expr(arg.Value)
		}
	case *MethodCall:
		a.expr(e.Receiver)
		for _, arg := range e.Args {
			a.expr(arg.Value)
		}
	case *ListLit:
		for _, el := range e.Elems {
			a.expr(el)
		}
	case *MapLit:
		for _, en := range e.Entries {
			a.expr(en.Key)
			a.expr(en.Value)
		}
	case *TupleLit:
		for _, el := range e.Elems {
			a.expr(el)
		}
	case *StructLit:
		for _, f := range e.Fields {
			if f.Value != nil {
				a.expr(f.Value)
			} else {
				// shorthand `{ name }` refers to a variable in the
				// enclosing scope by that name.
				a.record(f.Name, CaptureLocal, nil, false, f.SpanV)
			}
		}
		if e.Spread != nil {
			a.expr(e.Spread)
		}
	case *RangeLit:
		if e.Start != nil {
			a.expr(e.Start)
		}
		if e.End != nil {
			a.expr(e.End)
		}
	case *QuestionExpr:
		a.expr(e.X)
	case *CoalesceExpr:
		a.expr(e.Left)
		a.expr(e.Right)
	case *FieldExpr:
		a.expr(e.X)
	case *TupleAccess:
		a.expr(e.X)
	case *IndexExpr:
		a.expr(e.X)
		a.expr(e.Index)
	case *Closure:
		// Nested closure: names it captures from outside the current
		// closure propagate up as our captures too, unless they refer
		// to a binding we introduced ourselves.
		for _, cap := range e.Captures {
			if a.bound[cap.Name] {
				continue
			}
			a.record(cap.Name, cap.Kind, cap.T, cap.Mut, cap.SpanV)
		}
	case *VariantLit:
		for _, arg := range e.Args {
			a.expr(arg.Value)
		}
	case *BlockExpr:
		a.block(e.Block)
	case *IfExpr:
		a.expr(e.Cond)
		a.block(e.Then)
		if e.Else != nil {
			a.block(e.Else)
		}
	case *IfLetExpr:
		a.expr(e.Scrutinee)
		saved := a.snapshot()
		if e.Pattern != nil {
			a.patternNames(e.Pattern)
		}
		a.block(e.Then)
		a.restore(saved)
		if e.Else != nil {
			a.block(e.Else)
		}
	case *MatchExpr:
		a.expr(e.Scrutinee)
		for _, arm := range e.Arms {
			a.matchArm(arm)
		}
	}
}

// captureKindForIdent classifies a resolved ident's kind as a capture
// origin. Types, fns, variants and builtins are not captures: they are
// globally accessible at compile time.
func captureKindForIdent(k IdentKind) CaptureKind {
	switch k {
	case IdentLocal:
		return CaptureLocal
	case IdentParam:
		return CaptureParam
	case IdentGlobal:
		return CaptureGlobal
	}
	return CaptureUnknown
}
