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
		g.preLiftMatches(s.X)
		g.preLiftQuestions(s.X)
		g.emitExpr(s.X)
		g.body.nl()
		g.resetQuestionSubs()
		g.resetMatchSubs()
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

// emitLetStmt handles `let p = e`.
//
// Mutability flag (`let mut`) is discarded: Go variables are always
// mutable; the type checker already enforced Osty's immutability.
func (g *gen) emitLetStmt(l *ast.LetStmt) {
	name := identPatternName(l.Pattern)
	if name == "" {
		g.emitLetPatternDestructure(l.Pattern, l.Value)
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
	// is a straight substitution on the lifted temps. The match lift
	// runs first so a tainted match in the operand of a `?` is already
	// substituted by the time the question lift evaluates the operand.
	g.preLiftMatches(l.Value)
	defer g.resetMatchSubs()
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
			elem := g.listElemGoTypeExpr(l.Type)
			if elem == "" {
				elem = g.listElemGoType(g.typeOf(l.Value))
			}
			if g.emitExprWithExpectedListElem(l.Value, elem) {
				// handled — list literal coerced to annotated element type
			} else {
				g.emitExpr(l.Value)
			}
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
		g.body.writef("if !%s.IsOk { return %s{Error: %s.Error, ref: resultRef()} }\n", tmp, retGo, tmp)
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

// emitLetPatternDestructure lowers an irrefutable pattern binding by
// first evaluating the RHS once and then reusing the match binding
// machinery for tuple / struct / nested subpatterns.
func (g *gen) emitLetPatternDestructure(p ast.Pattern, value ast.Expr) {
	tmp := g.freshVar("_p")
	g.preLiftMatches(value)
	defer g.resetMatchSubs()
	g.preLiftQuestions(value)
	defer g.resetQuestionSubs()
	g.body.writef("%s := ", tmp)
	g.emitExpr(value)
	g.body.writef("\n_ = %s\n", tmp)
	g.emitPatternBindings(tmp, g.typeOf(value), p)
}

// emitAssign covers `a = b`, compound assigns (`+=`, `-=`, ...) and
// multi-assign `(a, b) = (c, d)`. Multi-assign with a tuple RHS is
// rewritten to Go's parallel assignment.
func (g *gen) emitAssign(a *ast.AssignStmt) {
	// Pre-lift any escaping match / `?` in targets or value. Targets
	// rarely contain either in practice, but we walk them for
	// completeness. Match lift runs before the question lift so a
	// tainted match feeding a `?` is already substituted.
	for _, t := range a.Targets {
		g.preLiftMatches(t)
		g.preLiftQuestions(t)
	}
	g.preLiftMatches(a.Value)
	defer g.resetMatchSubs()
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
	if a.Op == token.ASSIGN {
		if g.emitCheckedIntegerSimpleAssign(a.Targets[0], a.Value) {
			return
		}
	}
	if binOp, ok := compoundBinaryOp(a.Op); ok {
		if g.emitCheckedIntegerCompoundAssign(a.Targets[0], a.Value, binOp) {
			return
		}
	}
	g.emitExpr(a.Targets[0])
	g.body.writef(" %s ", op)
	g.emitExpr(a.Value)
	g.body.nl()
}

func (g *gen) emitCheckedIntegerSimpleAssign(target ast.Expr, value ast.Expr) bool {
	if _, ok := target.(*ast.Ident); !ok {
		return false
	}
	targetK, ok := integerKindOf(g.typeOf(target))
	if !ok {
		return false
	}
	bin, ok := value.(*ast.BinaryExpr)
	if !ok {
		return false
	}
	if _, ok := integerKindOf(g.typeOf(bin)); !ok {
		return false
	}
	switch bin.Op {
	case token.PLUS, token.MINUS, token.STAR, token.SLASH, token.PERCENT, token.SHL, token.SHR:
	default:
		return false
	}
	g.body.writeln("func() {")
	g.body.indent()
	g.emitCheckedIntegerCompoundAssignBody(bin.Op, bin.Right, targetK, g.typeOf(bin.Right), func() {
		g.emitExpr(bin.Left)
	}, func() {
		g.emitExpr(target)
	})
	g.body.dedent()
	g.body.writeln("}()")
	return true
}

func (g *gen) emitCheckedIntegerCompoundAssign(target ast.Expr, value ast.Expr, op token.Kind) bool {
	targetT := g.typeOf(target)
	targetK, ok := integerKindOf(targetT)
	if !ok {
		return false
	}
	g.body.writeln("func() {")
	g.body.indent()
	if idx, ok := target.(*ast.IndexExpr); ok && isMapType(g.typeOf(idx.X)) {
		mapVar := g.freshVar("_map")
		keyVar := g.freshVar("_key")
		g.body.writef("%s := ", mapVar)
		g.emitExpr(idx.X)
		g.body.nl()
		g.body.writef("%s := ", keyVar)
		g.emitExpr(idx.Index)
		g.body.nl()
		g.emitCheckedIntegerCompoundAssignBody(op, value, targetK, g.typeOf(value), func() {
			g.body.writef("%s[%s]", mapVar, keyVar)
		}, func() {
			g.body.writef("%s[%s]", mapVar, keyVar)
		})
	} else {
		slotVar := g.freshVar("_slot")
		g.body.writef("%s := &", slotVar)
		g.emitExpr(target)
		g.body.nl()
		g.emitCheckedIntegerCompoundAssignBody(op, value, targetK, g.typeOf(value), func() {
			g.body.writef("*%s", slotVar)
		}, func() {
			g.body.writef("*%s", slotVar)
		})
	}
	g.body.dedent()
	g.body.writeln("}()")
	return true
}

func (g *gen) emitCheckedIntegerCompoundAssignBody(op token.Kind, value ast.Expr, targetK types.PrimitiveKind, valueT types.Type, emitCurrent, emitDest func()) {
	goT := goPrimitive(targetK)
	info := runtimeIntInfo(targetK)
	cur := g.freshVar("_cur")
	rhs := g.freshVar("_rhs")
	g.body.writef("var %s %s = ", cur, goT)
	emitCurrent()
	g.body.nl()
	switch op {
	case token.PLUS, token.MINUS, token.STAR, token.SLASH, token.PERCENT:
		if op == token.PLUS || op == token.MINUS || op == token.STAR || info.signed {
			g.use("math")
		}
		g.body.writef("var %s %s = ", rhs, goT)
		g.emitExpr(value)
		g.body.nl()
		switch op {
		case token.PLUS:
			g.emitIntAddOverflowGuard(info, cur, rhs, "panic(\"integer overflow\")", "panic(\"integer overflow\")")
			emitDest()
			g.body.writef(" = %s + %s\n", cur, rhs)
		case token.MINUS:
			g.emitIntSubOverflowGuard(info, cur, rhs, "panic(\"integer overflow\")", "panic(\"integer overflow\")")
			emitDest()
			g.body.writef(" = %s - %s\n", cur, rhs)
		case token.STAR:
			g.emitIntMulOverflowGuard(info, goT, cur, rhs, "panic(\"integer overflow\")", "panic(\"integer overflow\")")
			emitDest()
			g.body.writef(" = %s * %s\n", cur, rhs)
		case token.SLASH:
			g.body.writef("if %s == 0 { panic(%q) }\n", rhs, "integer division by zero")
			if info.signed {
				g.body.writef("if %s == %s && %s == %s(-1) { panic(%q) }\n", cur, info.min, rhs, goT, "integer overflow")
			}
			emitDest()
			g.body.writef(" = %s / %s\n", cur, rhs)
		case token.PERCENT:
			g.body.writef("if %s == 0 { panic(%q) }\n", rhs, "integer modulo by zero")
			if info.signed {
				g.body.writef("if %s == %s && %s == %s(-1) { panic(%q) }\n", cur, info.min, rhs, goT, "integer overflow")
			}
			emitDest()
			g.body.writef(" = %s %% %s\n", cur, rhs)
		}
	case token.SHL, token.SHR:
		rightK, ok := integerKindOf(valueT)
		if !ok {
			rightK = targetK
		}
		rightGo := goPrimitive(rightK)
		rightInfo := runtimeIntInfo(rightK)
		if targetK == types.PInt {
			g.use("strconv")
		}
		g.body.writef("var %s %s = ", rhs, rightGo)
		g.emitExpr(value)
		g.body.nl()
		if rightInfo.signed {
			g.body.writef("if %s < 0 || %s >= %s(%s) { panic(%q) }\n", rhs, rhs, rightGo, info.bits, "invalid shift count")
		} else {
			g.body.writef("if %s >= %s(%s) { panic(%q) }\n", rhs, rightGo, info.bits, "invalid shift count")
		}
		emitDest()
		shiftOp := "<<"
		if op == token.SHR {
			shiftOp = ">>"
		}
		g.body.writef(" = %s %s uint(%s)\n", cur, shiftOp, rhs)
	}
}

func compoundBinaryOp(k token.Kind) (token.Kind, bool) {
	switch k {
	case token.PLUSEQ:
		return token.PLUS, true
	case token.MINUSEQ:
		return token.MINUS, true
	case token.STAREQ:
		return token.STAR, true
	case token.SLASHEQ:
		return token.SLASH, true
	case token.PERCENTEQ:
		return token.PERCENT, true
	case token.SHLEQ:
		return token.SHL, true
	case token.SHREQ:
		return token.SHR, true
	default:
		return token.ILLEGAL, false
	}
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
	g.preLiftMatches(r.Value)
	defer g.resetMatchSubs()
	g.preLiftQuestions(r.Value)
	defer g.resetQuestionSubs()
	g.body.write("return ")
	prevHintType, prevHintGo := g.retHintType, g.retHintGo
	g.retHintType, g.retHintGo = g.currentRetType, g.currentRetGo
	g.emitExpr(r.Value)
	g.retHintType, g.retHintGo = prevHintType, prevHintGo
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
	// Tuple pattern over a Go two-value range source:
	//
	//   for (k, v) in map
	//   for (i, x) in xs.enumerate()
	//
	// Plain List<(A, B)> values intentionally skip this path; Go range
	// yields index + tuple there, so the generic destructuring branch
	// below must bind the tuple element instead.
	if tp, ok := f.Pattern.(*ast.TuplePat); ok && len(tp.Elems) == 2 {
		iter := f.Iter
		var keyType, valueType types.Type
		twoValueRange := false
		if recv, ok := enumerateReceiver(f.Iter); ok {
			iter = recv
			keyType = types.Int
			valueType = iterElementType(g.typeOf(recv))
			twoValueRange = true
		} else if isMapType(g.typeOf(f.Iter)) {
			keyType, valueType = mapElemTypes(g.typeOf(f.Iter))
			twoValueRange = true
		}
		if twoValueRange {
			k := g.freshVar("_k")
			v := g.freshVar("_v")
			g.body.writef("for %s, %s := range ", k, v)
			g.emitExpr(iter)
			g.body.writeln(" {")
			g.body.indent()
			g.body.writef("_ = %s\n_ = %s\n", k, v)
			g.emitPatternBindings(k, keyType, tp.Elems[0])
			g.emitPatternBindings(v, valueType, tp.Elems[1])
			g.emitStmts(f.Body.Stmts)
			g.body.dedent()
			g.body.writeln("}")
			return
		}
	}
	// for pattern in iter — single binding.
	name := identPatternName(f.Pattern)
	if name == "" {
		// Non-ident pattern: bind each iteration to a fresh temp and
		// unpack it inside the body through the same recursive pattern
		// binding path used by match / if-let.
		tmp := g.freshVar("_it")
		g.body.writef("for _, %s := range ", tmp)
		g.emitExpr(f.Iter)
		g.body.writeln(" {")
		g.body.indent()
		g.body.writef("_ = %s\n", tmp)
		g.emitPatternBindings(tmp, iterElementType(g.typeOf(f.Iter)), f.Pattern)
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
//	    if !matches(_t, pat) { break }
//	    // bind(s) from the pattern
//	    <body>
//	}
//
// The pattern test/binding code is shared with match and if-let, so
// Option, Result, user enums, tuple, struct, range and binding
// patterns stay in one lowering path.
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
	scrutT := g.typeOf(f.Iter)
	g.body.write("if !(")
	g.emitPatternTest(tmp, scrutT, f.Pattern)
	g.body.writeln(") { break }")
	g.emitPatternBindings(tmp, scrutT, f.Pattern)
	g.emitStmts(f.Body.Stmts)
	g.body.dedent()
	g.body.writeln("}")
}

func isMapType(t types.Type) bool {
	n, ok := t.(*types.Named)
	return ok && n.Sym != nil && n.Sym.Name == "Map" && len(n.Args) == 2
}

func mapElemTypes(t types.Type) (types.Type, types.Type) {
	if n, ok := t.(*types.Named); ok && n.Sym != nil && n.Sym.Name == "Map" && len(n.Args) == 2 {
		return n.Args[0], n.Args[1]
	}
	return nil, nil
}

func iterElementType(t types.Type) types.Type {
	switch n := t.(type) {
	case *types.Named:
		if n.Sym == nil {
			return nil
		}
		switch n.Sym.Name {
		case "List", "Set", "Chan", "Channel":
			if len(n.Args) == 1 {
				return n.Args[0]
			}
		case "Map":
			if len(n.Args) == 2 {
				return &types.Tuple{Elems: []types.Type{n.Args[0], n.Args[1]}}
			}
		}
	case *types.Primitive:
		if n.Kind == types.PString || n.Kind == types.PBytes {
			return types.Byte
		}
	}
	return nil
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
// list literals this preserves the element type even when checker info
// is missing on the literal itself; other expressions fall back to
// bare emission.
func (g *gen) emitExprAsType(e ast.Expr, target types.Type) {
	if g.emitExprWithExpectedListElem(e, g.listElemGoType(target)) {
		return
	}
	g.emitExpr(e)
}

func (g *gen) emitExprAsTypeExpr(e ast.Expr, target ast.Type) {
	if g.emitExprWithExpectedListElem(e, g.listElemGoTypeExpr(target)) {
		return
	}
	g.emitExpr(e)
}

func (g *gen) emitExprWithExpectedListElem(e ast.Expr, elemGo string) bool {
	if elemGo == "" {
		return false
	}
	list, ok := e.(*ast.ListExpr)
	if !ok {
		return false
	}
	g.emitListWithElemType(list, elemGo)
	return true
}

func (g *gen) listElemGoType(target types.Type) string {
	n, ok := target.(*types.Named)
	if !ok || n.Sym == nil || n.Sym.Name != "List" || len(n.Args) != 1 {
		return ""
	}
	return g.goType(n.Args[0])
}

func (g *gen) listElemGoTypeExpr(target ast.Type) string {
	n, ok := target.(*ast.NamedType)
	if !ok || len(n.Path) == 0 || n.Path[len(n.Path)-1] != "List" || len(n.Args) != 1 {
		return ""
	}
	return g.goTypeExpr(n.Args[0])
}
