package gen

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/token"
	"github.com/osty/osty/internal/types"
)

// emitStmts writes a series of statements.
func (g *gen) emitStmts(stmts []ast.Stmt) {
	for _, s := range stmts {
		g.emitStmt(s)
	}
}

// emitStmt dispatches on statement kind. Each emitter is responsible
// for writing its own trailing newline.
//
// The pre-lift pass for `?` runs inside the per-kind emitters that
// accept free-form expressions (LetStmt, ReturnStmt, AssignStmt,
// ExprStmt). It must run before the statement's trailing expression
// is emitted so the lift blocks land above it; the subs map is
// cleared after the statement so it doesn't leak into the next.
func (g *gen) emitStmt(s ast.Stmt) {
	g.sourceMarker(s)
	switch s := s.(type) {
	case *ast.Block:
		g.emitBlock(s)
	case *ast.LetStmt:
		g.emitLetStmt(s)
	case *ast.ExprStmt:
		// If / match at statement position lower to Go statements,
		// not expression-IIFEs. Their value is discarded anyway, so
		// the IIFE would just allocate a closure for nothing.
		switch e := s.X.(type) {
		case *ast.IfExpr:
			g.emitIfStmt(e)
			return
		case *ast.MatchExpr:
			// Match used only for side effects — lower to a direct
			// sequence of if blocks, no IIFE wrapper.
			g.emitMatchStmt(e)
			return
		case *ast.QuestionExpr:
			// A bare `expr?` at statement position is equivalent to
			// `let _ = expr?` — propagate on failure, discard the
			// unwrapped value.
			g.emitQuestionLift("_", e)
			return
		}
		g.preLiftQuestions(s.X)
		g.emitExpr(s.X)
		g.body.nl()
		g.resetQuestionSubs()
	case *ast.AssignStmt:
		g.emitAssign(s)
	case *ast.ReturnStmt:
		g.emitReturn(s)
	case *ast.BreakStmt:
		g.body.writeln("break")
	case *ast.ContinueStmt:
		g.body.writeln("continue")
	case *ast.ForStmt:
		g.emitFor(s)
	case *ast.DeferStmt:
		g.body.write("defer ")
		// `defer <expr>` in Osty maps to Go's `defer <callish-expr>`;
		// Go requires a call expression, which Osty-side users already
		// write (defer close(h)).
		g.emitExpr(s.X)
		g.body.nl()
	case *ast.ChanSendStmt:
		g.emitExpr(s.Channel)
		g.body.write(" <- ")
		g.emitExpr(s.Value)
		g.body.nl()
	default:
		g.body.writef("/* TODO: stmt %T */\n", s)
	}
}

// emitBlock writes a brace-delimited block in statement position,
// including a trailing newline after the closing brace.
func (g *gen) emitBlock(b *ast.Block) {
	g.emitBlockInline(b)
	g.body.nl()
}

// emitBlockInline writes `{ ... }` without a trailing newline, so that
// the caller can place `else`, `, ` (closure body), etc. immediately
// after the closing brace.
func (g *gen) emitBlockInline(b *ast.Block) {
	g.body.writeln("{")
	g.body.indent()
	g.emitStmts(b.Stmts)
	g.body.dedent()
	g.body.write("}")
}

// emitLetStmt handles `let p = e`. Phase 1 supports bare identifier
// patterns only — destructuring (tuple / struct / variant) is Phase 2.
//
// Mutability flag (`let mut`) is discarded: Go variables are always
// mutable; the type checker already enforced Osty's immutability.
func (g *gen) emitLetStmt(l *ast.LetStmt) {
	// Tuple destructure: `let (a, b) = expr` → temp-and-project.
	if tp, ok := l.Pattern.(*ast.TuplePat); ok {
		g.emitLetTupleDestructure(tp, l)
		return
	}
	// Struct destructure: `let Point { x, y } = p` → per-field bindings.
	if sp, ok := l.Pattern.(*ast.StructPat); ok {
		g.emitLetStructDestructure(sp, l)
		return
	}

	name := identPatternName(l.Pattern)
	if name == "" {
		g.body.writef("/* TODO: destructuring let %T */\n", l.Pattern)
		return
	}
	safe := mangleIdent(name)

	// `let x = expr?` lifts the `?` into control flow. Two shapes:
	//
	//   Option operand: test for nil, return nil on miss, deref.
	//   Result operand: test IsOk, return the same Result on miss,
	//                   take Value on success.
	if q, ok := l.Value.(*ast.QuestionExpr); ok {
		g.emitQuestionLift(safe, q)
		return
	}

	// Any `?` nested inside a compound RHS (e.g. `let x = foo(bar()?)`)
	// gets hoisted out via preLiftQuestions so the remaining expression
	// is a straight substitution on the lifted temps.
	g.preLiftQuestions(l.Value)
	defer g.resetQuestionSubs()

	// Silence Go's "declared and not used" for the bound name. Go
	// enforces usage per scope; Osty doesn't. Adding `_ = x` after
	// every let is the cheapest way to keep the transpiled output
	// compiling even when the user declares-but-never-reads.
	defer func() {
		if safe != "_" {
			g.body.writef("_ = %s\n", safe)
		}
	}()

	// Prefer short form (name := expr) when the checker inferred the
	// type and no annotation is given. Explicit annotations get the
	// `var name T = expr` long form so the declared type is preserved
	// across literal coercion (e.g. `let x: Float32 = 1.5`).
	if l.Type != nil {
		g.body.writef("var %s %s", safe, g.goTypeExpr(l.Type))
		if l.Value != nil {
			g.body.write(" = ")
			g.emitExprAsType(l.Value, g.typeOf(l.Value))
		}
		g.body.nl()
		return
	}
	if l.Value == nil {
		// `let x` with no value and no type is unusual; fall back to
		// `any` so gofmt parses something.
		g.body.writef("var %s any\n", safe)
		return
	}
	// `let _ = expr`: Go forbids `_ :=`, so emit plain blank-ident
	// assignment. Keeps side effects of the RHS while explicitly
	// discarding the value.
	if safe == "_" {
		g.body.write("_ = ")
		g.emitExpr(l.Value)
		g.body.nl()
		return
	}
	g.body.writef("%s := ", safe)
	g.emitExpr(l.Value)
	g.body.nl()
}

// identPatternName extracts a bare identifier name from a pattern, or
// "" when the pattern is a destructuring form we haven't yet lowered.
func identPatternName(p ast.Pattern) string {
	switch p := p.(type) {
	case *ast.IdentPat:
		return p.Name
	case *ast.WildcardPat:
		return "_"
	}
	return ""
}

// emitQuestionLift lowers `let <name> = <expr>?` into the pair:
//
//	_qN := <expr>
//	if /* failure test */ { return /* failure value */ }
//	<name> := /* success value */
//
// Dispatch between Option and Result is driven by the operand's
// checked type. Result's failure-return must be reconstructed with the
// enclosing function's Result[T, E] signature — Go treats
// Result[bool, E] and Result[string, E] as unrelated types.
//
// A name of "_" skips the user-facing binding — used for the
// statement-position `expr?` form where the success value is
// discarded.
func (g *gen) emitQuestionLift(name string, q *ast.QuestionExpr) {
	tmp := g.freshVar("_q")
	operandT := g.typeOf(q.X)
	isResult := false
	if n, ok := operandT.(*types.Named); ok && n.Sym != nil && n.Sym.Name == "Result" {
		isResult = true
	}
	g.body.writef("%s := ", tmp)
	g.emitExpr(q.X)
	g.body.nl()
	if isResult {
		// Reconstruct a Result of the enclosing function's signature.
		retGo := "any"
		if g.currentRetGo != "" {
			retGo = g.currentRetGo
		} else if g.currentRetType != nil {
			retGo = g.goTypeExpr(g.currentRetType)
		}
		g.body.writef("if !%s.IsOk { return %s{Error: %s.Error} }\n", tmp, retGo, tmp)
		if name != "_" {
			g.body.writef("%s := %s.Value\n_ = %s\n", name, tmp, name)
		} else {
			g.body.writef("_ = %s.Value\n", tmp)
		}
		return
	}
	g.body.writef("if %s == nil { return nil }\n", tmp)
	if name != "_" {
		g.body.writef("%s := *%s\n_ = %s\n", name, tmp, name)
	} else {
		g.body.writef("_ = *%s\n", tmp)
	}
}

// emitLetTupleDestructure lowers `let (a, b, ...) = expr` to:
//
//	_tmp := expr
//	a := _tmp.F0
//	b := _tmp.F1
//
// Wildcard elements contribute no binding. Nested patterns fall back
// to a temp + recursive destructure.
func (g *gen) emitLetTupleDestructure(tp *ast.TuplePat, l *ast.LetStmt) {
	tmp := g.freshVar("_t")
	g.body.writef("%s := ", tmp)
	g.emitExpr(l.Value)
	g.body.writef("\n_ = %s\n", tmp)
	for i, elem := range tp.Elems {
		switch e := elem.(type) {
		case *ast.WildcardPat:
			// nothing
		case *ast.IdentPat:
			g.body.writef("%s := %s.F%d; _ = %s\n",
				mangleIdent(e.Name), tmp, i, mangleIdent(e.Name))
		default:
			g.body.writef("/* TODO: nested tuple pattern %T at index %d */\n", e, i)
		}
	}
}

// emitLetStructDestructure lowers `let Point { x, y, .. } = p` to:
//
//	_tmp := p
//	x := _tmp.x
//	y := _tmp.y
func (g *gen) emitLetStructDestructure(sp *ast.StructPat, l *ast.LetStmt) {
	tmp := g.freshVar("_s")
	g.body.writef("%s := ", tmp)
	g.emitExpr(l.Value)
	g.body.writef("\n_ = %s\n", tmp)
	for _, f := range sp.Fields {
		bindName := f.Name
		if f.Pattern != nil {
			if id, ok := f.Pattern.(*ast.IdentPat); ok {
				bindName = id.Name
			} else {
				// Nested pattern — fall back to binding the field to
				// a fresh temp and recursing, but we don't have a full
				// recursive let lowering yet.
				g.body.writef("/* TODO: nested struct pattern on field %s */\n", f.Name)
				continue
			}
		}
		safe := mangleIdent(bindName)
		g.body.writef("%s := %s.%s; _ = %s\n",
			safe, tmp, mangleIdent(f.Name), safe)
	}
}

// emitAssign covers `a = b`, compound assigns (`+=`, `-=`, ...) and
// multi-assign `(a, b) = (c, d)`. Multi-assign with a tuple RHS is
// rewritten to Go's parallel assignment.
func (g *gen) emitAssign(a *ast.AssignStmt) {
	// Pre-lift any `?` in targets or value. Targets rarely contain `?`
	// in practice, but we walk them for completeness.
	for _, t := range a.Targets {
		g.preLiftQuestions(t)
	}
	g.preLiftQuestions(a.Value)
	defer g.resetQuestionSubs()

	op := assignOp(a.Op)
	if len(a.Targets) > 1 {
		for i, t := range a.Targets {
			if i > 0 {
				g.body.write(", ")
			}
			g.emitExpr(t)
		}
		g.body.writef(" %s ", op)
		// Multi-target with tuple RHS: lay out elems side-by-side.
		if tup, ok := a.Value.(*ast.TupleExpr); ok && len(tup.Elems) == len(a.Targets) {
			for i, e := range tup.Elems {
				if i > 0 {
					g.body.write(", ")
				}
				g.emitExpr(e)
			}
		} else {
			g.emitExpr(a.Value)
		}
		g.body.nl()
		return
	}
	g.emitExpr(a.Targets[0])
	g.body.writef(" %s ", op)
	g.emitExpr(a.Value)
	g.body.nl()
}

// assignOp maps an Osty assignment token to its Go operator string.
func assignOp(k token.Kind) string {
	switch k {
	case token.ASSIGN:
		return "="
	case token.PLUSEQ:
		return "+="
	case token.MINUSEQ:
		return "-="
	case token.STAREQ:
		return "*="
	case token.SLASHEQ:
		return "/="
	case token.PERCENTEQ:
		return "%="
	case token.BITANDEQ:
		return "&="
	case token.BITOREQ:
		return "|="
	case token.BITXOREQ:
		return "^="
	case token.SHLEQ:
		return "<<="
	case token.SHREQ:
		return ">>="
	}
	return "="
}

// emitReturn writes `return` or `return <expr>`.
//
// `return expr?` lifts the `?` so the return's expression is the
// unwrapped success value. Nested `?` inside a compound return
// expression is handled the same way via preLiftQuestions.
func (g *gen) emitReturn(r *ast.ReturnStmt) {
	if r.Value == nil {
		g.body.writeln("return")
		return
	}
	g.preLiftQuestions(r.Value)
	defer g.resetQuestionSubs()
	g.body.write("return ")
	g.emitExpr(r.Value)
	g.body.nl()
}

// emitFor covers all `for` forms.
//
//	for cond { ... }        → for cond { ... }
//	for { ... }             → for { ... }
//	for i in 0..10 { ... }  → for i := 0; i < 10; i++ { ... }
//	for i in 0..=10 { ... } → for i := 0; i <= 10; i++ { ... }
//	for x in xs { ... }     → for _, x := range xs { ... }
//	for let Some(v) = e {}  → Phase 4 (optional iteration)
func (g *gen) emitFor(f *ast.ForStmt) {
	g.loopDepth++
	defer func() { g.loopDepth-- }()

	if f.IsForLet {
		g.emitForLet(f)
		return
	}
	// Infinite loop.
	if f.Iter == nil {
		g.body.write("for ")
		g.emitBlock(f.Body)
		return
	}
	// for cond { ... } — Pattern is nil, Iter is the condition.
	if f.Pattern == nil {
		g.body.write("for ")
		g.emitExpr(f.Iter)
		g.body.write(" ")
		g.emitBlock(f.Body)
		return
	}
	// Tuple pattern `for (k, v) in map` → `for k, v := range map`.
	// Go flags unused variables so any pattern-bound name that the
	// body doesn't touch gets a `_ = name` prelude. Cheaper than
	// analysing the body.
	if tp, ok := f.Pattern.(*ast.TuplePat); ok && len(tp.Elems) == 2 {
		k := forPatternName(tp.Elems[0], "_")
		v := forPatternName(tp.Elems[1], "_")
		iter := f.Iter
		if recv, ok := enumerateReceiver(f.Iter); ok {
			iter = recv
		}
		if v == "_" {
			if _, wildcard := tp.Elems[1].(*ast.WildcardPat); !wildcard {
				v = g.freshVar("_it")
			}
		}
		g.body.writef("for %s, %s := range ", k, v)
		g.emitExpr(iter)
		g.body.writeln(" {")
		g.body.indent()
		if k != "_" {
			g.body.writef("_ = %s\n", k)
		}
		if v != "_" {
			g.body.writef("_ = %s\n", v)
		}
		if _, id := tp.Elems[1].(*ast.IdentPat); !id {
			if _, wildcard := tp.Elems[1].(*ast.WildcardPat); !wildcard && v != "_" {
				synth := &ast.LetStmt{Pattern: tp.Elems[1], Value: &ast.Ident{Name: v}}
				switch p := tp.Elems[1].(type) {
				case *ast.TuplePat:
					g.emitLetTupleDestructure(p, synth)
				case *ast.StructPat:
					g.emitLetStructDestructure(p, synth)
				default:
					g.body.writef("/* TODO: for tuple value pattern %T */\n", tp.Elems[1])
				}
			}
		}
		g.emitStmts(f.Body.Stmts)
		g.body.dedent()
		g.body.writeln("}")
		return
	}
	// for pattern in iter — single binding.
	name := identPatternName(f.Pattern)
	if name == "" {
		// Non-ident pattern: bind each iteration to a fresh temp and
		// unpack it inside the body, reusing the let-destructure
		// emitters so the pattern shapes stay in sync.
		tmp := g.freshVar("_it")
		g.body.writef("for _, %s := range ", tmp)
		g.emitExpr(f.Iter)
		g.body.writeln(" {")
		g.body.indent()
		g.body.writef("_ = %s\n", tmp)
		synth := &ast.LetStmt{Pattern: f.Pattern, Value: &ast.Ident{Name: tmp}}
		switch p := f.Pattern.(type) {
		case *ast.TuplePat:
			g.emitLetTupleDestructure(p, synth)
		case *ast.StructPat:
			g.emitLetStructDestructure(p, synth)
		default:
			g.body.writef("/* TODO: for with %T pattern */\n", f.Pattern)
		}
		g.emitStmts(f.Body.Stmts)
		g.body.dedent()
		g.body.writeln("}")
		return
	}
	// Integer range gets a C-style `for i := a; i <op> b; i++`.
	if r, ok := f.Iter.(*ast.RangeExpr); ok && r.Start != nil && r.Stop != nil {
		safe := mangleIdent(name)
		cmp := "<"
		if r.Inclusive {
			cmp = "<="
		}
		g.body.writef("for %s := ", safe)
		g.emitExpr(r.Start)
		g.body.writef("; %s %s ", safe, cmp)
		g.emitExpr(r.Stop)
		g.body.writef("; %s++ ", safe)
		g.emitBlock(f.Body)
		return
	}
	// Channel iteration — Go's `for x := range ch` form (single value).
	if iterT := g.typeOf(f.Iter); iterT != nil {
		if n, ok := iterT.(*types.Named); ok && n.Sym != nil &&
			(n.Sym.Name == "Chan" || n.Sym.Name == "Channel") {
			safe := mangleIdent(name)
			g.body.writef("for %s := range ", safe)
			g.emitExpr(f.Iter)
			g.body.write(" ")
			g.emitBlock(f.Body)
			return
		}
	}
	// Generic: `for _, name := range iter`.
	safe := mangleIdent(name)
	g.body.writef("for _, %s := range ", safe)
	g.emitExpr(f.Iter)
	g.body.write(" ")
	g.emitBlock(f.Body)
}

// emitForLet lowers `for let pat = expr { body }` — iterate while
// `expr` still matches `pat`. The canonical shape is
// `for let Some(x) = queue.pop() { ... }`, i.e. pop until the queue
// runs dry. The lowering is:
//
//	for {
//	    _t := <expr>
//	    if _t == nil { break }         // or: if !_t.IsOk { break }
//	    x := *_t                       // bind(s) from the pattern
//	    <body>
//	}
//
// Only `Some(…)` / `Ok(…)` / `Err(…)` variant patterns are supported
// here; more exotic enum patterns reuse the match-arm lowering path
// inside a `for` so arbitrary user enums keep working.
func (g *gen) emitForLet(f *ast.ForStmt) {
	g.body.writeln("for {")
	g.body.indent()
	tmp := g.freshVar("_ol")
	if iterT := g.typeOf(f.Iter); iterT != nil && !types.IsError(iterT) {
		g.body.writef("var %s %s = ", tmp, g.goType(iterT))
	} else if id, ok := f.Iter.(*ast.Ident); ok && id.Name == "None" {
		g.body.writef("var %s *any = ", tmp)
	} else {
		g.body.writef("%s := ", tmp)
	}
	g.emitExpr(f.Iter)
	g.body.writef("\n_ = %s\n", tmp)
	vp, ok := f.Pattern.(*ast.VariantPat)
	if !ok || len(vp.Path) == 0 {
		g.body.writef("/* TODO: for-let with %T pattern */\nbreak\n", f.Pattern)
		g.body.dedent()
		g.body.writeln("}")
		return
	}
	head := vp.Path[len(vp.Path)-1]
	switch head {
	case "Some":
		// Option<T> is represented as *T in gen. Break on nil; deref on match.
		g.body.writef("if %s == nil { break }\n", tmp)
		if len(vp.Args) == 1 {
			if id, ok := vp.Args[0].(*ast.IdentPat); ok {
				g.body.writef("%s := *%s; _ = %s\n",
					mangleIdent(id.Name), tmp, mangleIdent(id.Name))
			}
		}
	case "None":
		// Iterates only while expr is None — typically a guard form. Once
		// we bind here we don't extract a value.
		g.body.writef("if %s != nil { break }\n", tmp)
	case "Ok":
		g.body.writef("if !%s.IsOk { break }\n", tmp)
		if len(vp.Args) == 1 {
			if id, ok := vp.Args[0].(*ast.IdentPat); ok {
				g.body.writef("%s := %s.Value; _ = %s\n",
					mangleIdent(id.Name), tmp, mangleIdent(id.Name))
			}
		}
	case "Err":
		g.body.writef("if %s.IsOk { break }\n", tmp)
		if len(vp.Args) == 1 {
			if id, ok := vp.Args[0].(*ast.IdentPat); ok {
				g.body.writef("%s := %s.Error; _ = %s\n",
					mangleIdent(id.Name), tmp, mangleIdent(id.Name))
			}
		}
	default:
		// Generic user-enum variant: type-assert to the variant struct.
		owner, isUserVariant := g.variantOwner[head]
		if isUserVariant {
			variantT := owner + "_" + head
			alt := g.freshVar("_v")
			g.body.writef("%s, _ok := %s.(%s)\nif !_ok { break }\n_ = %s\n",
				alt, tmp, variantT, alt)
			for i, a := range vp.Args {
				if id, ok := a.(*ast.IdentPat); ok {
					g.body.writef("%s := %s.F%d; _ = %s\n",
						mangleIdent(id.Name), alt, i, mangleIdent(id.Name))
				}
			}
		} else {
			g.body.writef("/* TODO: for-let variant %s */\nbreak\n", head)
		}
	}
	g.emitStmts(f.Body.Stmts)
	g.body.dedent()
	g.body.writeln("}")
}

// forPatternName returns a Go-safe name for a pattern element in a
// destructuring `for` loop, substituting `fallback` when the pattern
// is a wildcard or an unsupported form.
func forPatternName(p ast.Pattern, fallback string) string {
	switch p := p.(type) {
	case *ast.IdentPat:
		return mangleIdent(p.Name)
	case *ast.WildcardPat:
		return "_"
	}
	return fallback
}

func enumerateReceiver(e ast.Expr) (ast.Expr, bool) {
	call, ok := e.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return nil, false
	}
	field, ok := call.Fn.(*ast.FieldExpr)
	if !ok || field.Name != "enumerate" {
		return nil, false
	}
	return field.X, true
}

// emitExprAsType emits an expression with a target type context. For
// numeric literals this rewrites them into the target's Go form so
// gofmt doesn't complain about untyped constant overflow; for other
// expressions it falls back to bare emission.
func (g *gen) emitExprAsType(e ast.Expr, _ types.Type) {
	// Phase 1: no special handling — the base emitter renders each
	// literal with its source text which is already Go-compatible.
	g.emitExpr(e)
}
