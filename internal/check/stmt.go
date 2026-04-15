package check

import (
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/token"
	"github.com/osty/osty/internal/types"
)

// checkDecl is pass 2 for a single top-level declaration: it walks the
// declaration's body with the signature information collected in pass 1.
func (c *checker) checkDecl(d ast.Decl) {
	switch n := d.(type) {
	case *ast.FnDecl:
		c.checkFnDecl(n, nil)
	case *ast.StructDecl:
		if desc, ok := c.result.Descs[c.declSymbol(n)]; ok {
			for _, m := range n.Methods {
				c.checkFnDecl(m, desc)
			}
		}
	case *ast.EnumDecl:
		if desc, ok := c.result.Descs[c.declSymbol(n)]; ok {
			for _, m := range n.Methods {
				c.checkFnDecl(m, desc)
			}
		}
	case *ast.InterfaceDecl:
		if desc, ok := c.result.Descs[c.declSymbol(n)]; ok {
			for _, m := range n.Methods {
				if m.Body != nil {
					c.checkFnDecl(m, desc)
				}
			}
		}
	case *ast.LetDecl:
		c.checkTopLet(n)
	}
}

// declSymbol fetches the file-scope symbol for a top-level declaration.
func (c *checker) declSymbol(d ast.Decl) *resolve.Symbol {
	switch n := d.(type) {
	case *ast.FnDecl:
		return c.topLevelSym(n.Name)
	case *ast.StructDecl:
		return c.topLevelSym(n.Name)
	case *ast.EnumDecl:
		return c.topLevelSym(n.Name)
	case *ast.InterfaceDecl:
		return c.topLevelSym(n.Name)
	case *ast.TypeAliasDecl:
		return c.topLevelSym(n.Name)
	case *ast.LetDecl:
		return c.topLevelSym(n.Name)
	}
	return nil
}

// checkFnDecl walks the body of a function / method, type-checking each
// statement against the declared return type.
func (c *checker) checkFnDecl(n *ast.FnDecl, owner *typeDesc) {
	// Register parameter types into the symbol table.
	for _, p := range n.Params {
		if p.Name == "" {
			continue
		}
		sym := c.symByDecl(p)
		if sym != nil {
			c.setSymType(sym, c.typeOf(p.Type))
		}
		if p.Default != nil {
			// §3.1: default arguments must be literals.
			if !isAllowedDefaultExpr(p.Default) {
				c.errNode(p.Default, diag.CodeDefaultNotLiteral,
					"default argument for `%s` must be a literal", p.Name)
			}
			c.checkExpr(p.Default, c.typeOf(p.Type), &env{retType: types.Unit})
		}
	}

	// Register the `self` receiver's type (if method) so body lookups
	// of `self` resolve to the enclosing Named.
	if owner != nil && n.Recv != nil {
		selfSym := c.fnReceiverSymbol(n)
		if selfSym != nil {
			selfT := types.Type(&types.Named{
				Sym:  owner.Sym,
				Args: argsOfGenerics(owner.Generics),
			})
			if owner.Sym != nil && owner.Sym.Name == "Option" && len(owner.Generics) == 1 {
				selfT = &types.Optional{Inner: owner.Generics[0]}
			}
			c.setSymType(selfSym, selfT)
		}
	}

	if n.Body == nil {
		return
	}

	var ret types.Type = types.Unit
	if n.ReturnType != nil {
		ret = c.typeOf(n.ReturnType)
	}
	e := &env{retType: ret}
	if rn, ok := types.AsNamedByName(ret, "Result"); ok && len(rn.Args) == 2 {
		e.retIsResult = true
		e.retResultErr = rn.Args[1]
	}
	if _, ok := ret.(*types.Optional); ok {
		e.retIsOption = true
	}
	for _, p := range n.Params {
		if p.Name == "" {
			continue
		}
		sym := c.symByDecl(p)
		if sym != nil && containsCapability(c.symTypeOrError(sym)) {
			e.rememberCapability(sym)
		}
	}
	if owner != nil && n.Recv != nil {
		selfSym := c.fnReceiverSymbol(n)
		if selfSym != nil && containsCapability(c.symTypeOrError(selfSym)) {
			e.rememberCapability(selfSym)
		}
	}

	// Body block: the final expression's value is the implicit return.
	bodyT := c.blockAsExprType(n.Body, ret, e)
	if !types.IsUnit(ret) && !blockEndsInReturn(n.Body) {
		c.rejectCapabilityEscape(n.Body, bodyT, "be returned")
	}
	if !types.IsUnit(ret) && !types.IsError(ret) && !c.accepts(ret, bodyT, n.Body) {
		// Suppress the error when the body ends in an explicit `return`
		// or when the whole body is `Never` (diverges).
		if !blockEndsInReturn(n.Body) && !types.IsNever(bodyT) {
			c.errMismatch(n.Body, ret, bodyT)
		}
	}
	// E0761 missing-return: a non-unit function whose body yields
	// Unit (no trailing expression) and doesn't explicitly return can
	// reach its end without producing a value.
	if !types.IsUnit(ret) && !types.IsError(ret) && types.IsUnit(bodyT) &&
		!blockEndsInReturn(n.Body) && !blockEndsInDivergent(n.Body, c.result.Types) {
		c.errNode(n.Body, diag.CodeMissingReturn,
			"function `%s` declared return type `%s` but its body may fall off the end without a value",
			n.Name, ret)
	}
}

// blockEndsInDivergent reports whether a block's last statement always
// transfers control out (return, break, continue, Never expression).
// Pairs with blockEndsInReturn for the missing-return diagnostic.
func blockEndsInDivergent(b *ast.Block, exprTypes map[ast.Expr]types.Type) bool {
	if b == nil || len(b.Stmts) == 0 {
		return false
	}
	last := b.Stmts[len(b.Stmts)-1]
	return stmtDiverges(last, exprTypes)
}

// blockEndsInReturn reports whether the last statement of a block is a
// `return` — in that case the block's own expression type doesn't need
// to match the return type.
func blockEndsInReturn(b *ast.Block) bool {
	if b == nil || len(b.Stmts) == 0 {
		return false
	}
	_, ok := b.Stmts[len(b.Stmts)-1].(*ast.ReturnStmt)
	return ok
}

// fnReceiverSymbol returns the synthetic `self` Symbol the resolver
// installed for a method, or nil when the method has no receiver.
func (c *checker) fnReceiverSymbol(n *ast.FnDecl) *resolve.Symbol {
	if n.Recv == nil {
		return nil
	}
	return c.symByDecl(n.Recv)
}

// checkTopLet walks a top-level `pub let NAME = value`.
func (c *checker) checkTopLet(n *ast.LetDecl) {
	if n.Value == nil {
		return
	}
	var want types.Type
	if n.Type != nil {
		want = c.typeOf(n.Type)
	}
	e := &env{retType: types.Unit}
	got := c.checkExpr(n.Value, want, e)
	if want != nil && !c.accepts(want, got, n.Value) {
		c.errMismatch(n.Value, want, got)
	}
	final := want
	if final == nil {
		if u, ok := got.(*types.Untyped); ok {
			final = u.Default()
		} else {
			final = got
		}
	}
	c.rejectCapabilityEscape(n.Value, final, "be stored at top level")
	if sym := c.declSymbol(n); sym != nil {
		c.setSymType(sym, final)
	}
}

// ---- Statement checking ----

func (c *checker) checkStmts(ss []ast.Stmt, env *env) {
	for _, s := range ss {
		c.checkStmt(s, env)
	}
}

func (c *checker) checkStmt(s ast.Stmt, env *env) {
	if s == nil {
		return
	}
	switch n := s.(type) {
	case *ast.LetStmt:
		c.checkLetStmt(n, env)
	case *ast.ExprStmt:
		c.checkExprStmt(n.X, env)
	case *ast.AssignStmt:
		c.checkAssignStmt(n, env)
	case *ast.ChanSendStmt:
		c.checkChanSend(n, env)
	case *ast.ReturnStmt:
		c.checkReturnStmt(n, env)
	case *ast.BreakStmt, *ast.ContinueStmt:
		// Resolver already validated loop context.
	case *ast.DeferStmt:
		c.checkExpr(n.X, nil, env)
	case *ast.ForStmt:
		c.checkForStmt(n, env)
	case *ast.Block:
		c.blockAsExprType(n, nil, env)
	}
}

func (c *checker) checkExprStmt(e ast.Expr, env *env) {
	switch x := e.(type) {
	case *ast.IfExpr:
		c.checkIfStmt(x, env)
	case *ast.MatchExpr:
		c.checkMatchStmt(x, env)
	case *ast.Block:
		c.checkStmts(x.Stmts, env)
		c.recordExpr(x, types.Unit)
	default:
		c.checkExpr(e, nil, env)
	}
}

func (c *checker) checkIfStmt(e *ast.IfExpr, env *env) {
	if e.IsIfLet {
		condT := c.checkExpr(e.Cond, nil, env)
		c.bindPatternTypes(e.Pattern, condT, env)
	} else {
		cond := c.checkExpr(e.Cond, types.Bool, env)
		if !types.IsBool(cond) && !types.IsError(cond) {
			c.errNode(e.Cond, diag.CodeConditionNotBool,
				"`if` condition must be `Bool`, got `%s`", cond)
		}
	}
	if e.Then != nil {
		c.checkStmts(e.Then.Stmts, env)
	}
	if e.Else != nil {
		c.checkExprStmt(e.Else, env)
	}
	c.recordExpr(e, types.Unit)
}

func (c *checker) checkMatchStmt(e *ast.MatchExpr, env *env) {
	scrutT := c.checkExpr(e.Scrutinee, nil, env)
	for _, arm := range e.Arms {
		c.bindPatternTypes(arm.Pattern, scrutT, env)
		if arm.Guard != nil {
			gT := c.checkExpr(arm.Guard, types.Bool, env)
			if !types.IsBool(gT) && !types.IsError(gT) {
				c.errNode(arm.Guard, diag.CodeConditionNotBool,
					"match guard must be `Bool`, got `%s`", gT)
			}
		}
		c.checkExprStmt(arm.Body, env)
	}
	c.recordExpr(e, types.Unit)
}

func (c *checker) checkLetStmt(n *ast.LetStmt, env *env) {
	var want types.Type
	if n.Type != nil {
		want = c.typeOf(n.Type)
	}
	var got types.Type
	if n.Value != nil {
		got = c.checkExpr(n.Value, want, env)
	}
	if want != nil && got != nil && !c.accepts(want, got, n.Value) {
		if n.Type != nil {
			c.errMismatchWithSource(n.Value, n.Type, want, got,
				"expected because of this annotation")
		} else {
			c.errMismatch(n.Value, want, got)
		}
	}
	final := want
	if final == nil {
		if u, ok := got.(*types.Untyped); ok {
			final = u.Default()
		} else {
			final = got
		}
	}
	if final != nil {
		c.result.LetTypes[n] = final
	}
	if n.Pattern != nil {
		c.bindPatternTypes(n.Pattern, final, env)
		// Let-binding mutability applies to the whole pattern.
		c.markPatternMut(n.Pattern, n.Mut)
	}
}

func (c *checker) checkAssignStmt(n *ast.AssignStmt, env *env) {
	// Multi-assign: treat as tuple vs tuple.
	if len(n.Targets) > 1 {
		got := c.checkExpr(n.Value, nil, env)
		tup, ok := got.(*types.Tuple)
		if !ok {
			// If RHS isn't a tuple, error individually per target.
			for _, t := range n.Targets {
				c.checkExpr(t, nil, env)
			}
			c.errNode(n.Value, diag.CodeTypeMismatch,
				"multiple-assignment RHS must be a tuple, got `%s`", got)
			return
		}
		if len(tup.Elems) != len(n.Targets) {
			c.errNode(n, diag.CodeTypeMismatch,
				"multiple-assignment shape mismatch: %d target(s), %d value(s)",
				len(n.Targets), len(tup.Elems))
			return
		}
		for i, tgt := range n.Targets {
			c.checkAssignTarget(tgt, tup.Elems[i], env)
		}
		return
	}
	// Single target. dstT drives numeric-literal inference on the RHS
	// (so `let mut x: Int64 = 0; x = 5` gives 5 the Int64 type).
	tgt := n.Targets[0]
	dstT := c.checkExpr(tgt, nil, env)
	valT := c.checkExpr(n.Value, dstT, env)
	c.checkAssignTarget(tgt, valT, env)
}

// checkAssignTarget verifies that a value of type vt can flow into
// the assignment target. For Ident targets it also enforces let-mut.
func (c *checker) checkAssignTarget(tgt ast.Expr, vt types.Type, env *env) {
	switch x := tgt.(type) {
	case *ast.Ident:
		sym := c.symbol(x)
		if sym == nil {
			return
		}
		info := c.info(sym)
		if sym.Kind == resolve.SymLet && info != nil && !info.Mut {
			c.errNode(tgt, diag.CodeMutabilityMismatch,
				"cannot assign to immutable binding `%s`", sym.Name)
			return
		}
		want := c.symTypeOrError(sym)
		if want != nil && !types.IsError(want) && !c.accepts(want, vt, tgt) {
			c.errMismatch(tgt, want, vt)
		}
	case *ast.FieldExpr:
		// Field assignment requires receiver to be mutable; we can't
		// check origin ownership precisely here, but we DO check that
		// the field type accepts the value.
		c.rejectCapabilityEscape(tgt, vt, "be stored in a field")
		ft := c.checkExpr(x, nil, env)
		if !c.accepts(ft, vt, tgt) {
			c.errMismatch(tgt, ft, vt)
		}
	case *ast.IndexExpr:
		c.rejectCapabilityEscape(tgt, vt, "be stored in a collection")
		it := c.checkExpr(x, nil, env)
		if !c.accepts(it, vt, tgt) {
			c.errMismatch(tgt, it, vt)
		}
	default:
		c.errNode(tgt, diag.CodeAssignTarget,
			"cannot assign to this expression")
	}
}

func (c *checker) checkReturnStmt(n *ast.ReturnStmt, env *env) {
	if n.Value == nil {
		if !types.IsUnit(env.retType) && !types.IsError(env.retType) {
			c.errNode(n, diag.CodeReturnTypeMismatch,
				"return without a value in a function returning `%s`", env.retType)
		}
		return
	}
	got := c.checkExpr(n.Value, env.retType, env)
	c.rejectCapabilityEscape(n.Value, got, "be returned")
	if !c.accepts(env.retType, got, n.Value) {
		c.errMismatch(n.Value, env.retType, got)
	}
}

// checkChanSend validates `ch <- v`: `ch` must be a Chan/Channel<T>
// and `v` must be assignable to T. Either failure is a type error at
// the statement's position.
func (c *checker) checkChanSend(n *ast.ChanSendStmt, env *env) {
	chT := c.checkExpr(n.Channel, nil, env)
	if types.IsError(chT) {
		c.checkExpr(n.Value, nil, env)
		return
	}
	named, ok := types.AsNamed(chT)
	if !ok || named.Sym == nil || (named.Sym.Name != "Chan" && named.Sym.Name != "Channel") {
		c.errNode(n.Channel, diag.CodeChannelNotChan,
			"cannot send on `%s`: expected a `Chan<T>` or `Channel<T>`", chT)
		c.checkExpr(n.Value, nil, env)
		return
	}
	var elem types.Type
	if len(named.Args) == 1 {
		elem = named.Args[0]
	}
	vt := c.checkExpr(n.Value, elem, env)
	c.rejectCapabilityEscape(n.Value, vt, "be sent over a channel")
	if elem != nil && !types.IsError(elem) && !c.accepts(elem, vt, n.Value) {
		c.errNode(n.Value, diag.CodeChannelWrongValue,
			"cannot send `%s` on `%s`", vt, chT)
	}
}

func (c *checker) checkForStmt(n *ast.ForStmt, env *env) {
	if n.Iter == nil {
		// infinite loop
		c.blockAsExprType(n.Body, nil, env)
		return
	}
	if n.IsForLet {
		iterT := c.checkExpr(n.Iter, nil, env)
		// Pattern binds against the value's inner type for Option/Result.
		c.bindPatternTypes(n.Pattern, iterT, env)
	} else if n.Pattern != nil {
		// for x in xs
		iterT := c.checkExpr(n.Iter, nil, env)
		c.bindPatternTypes(n.Pattern, c.iterElement(iterT, n.Iter), env)
	} else {
		// for cond  (while-style)
		cond := c.checkExpr(n.Iter, types.Bool, env)
		if !types.IsBool(cond) && !types.IsError(cond) {
			c.errNode(n.Iter, diag.CodeConditionNotBool,
				"`for` condition must be `Bool`, got `%s`", cond)
		}
	}
	if n.Body != nil {
		c.blockAsExprType(n.Body, nil, env)
	}
}

// ---- Pattern type binding ----

// bindPatternTypes installs types for every binding introduced by the
// pattern, and verifies structural and literal compatibility against
// the scrutinee type `t`.
func (c *checker) bindPatternTypes(p ast.Pattern, t types.Type, env *env) {
	if p == nil {
		return
	}
	switch x := p.(type) {
	case *ast.WildcardPat:
		// nothing to bind
	case *ast.LiteralPat:
		lt := c.checkExpr(x.Literal, t, env)
		if !c.accepts(t, lt, x) && !types.IsError(t) && !types.IsError(lt) {
			c.errNode(x, diag.CodeLitPatternMismatch,
				"literal pattern `%s` does not match scrutinee of type `%s`",
				lt, t)
		}
	case *ast.IdentPat:
		sym := c.patBindingSym(x, x.Name, x.PosV)
		if sym.Kind == resolve.SymVariant {
			// Bare variant match — no binding to set. Verify it matches
			// the scrutinee shape.
			c.verifyBareVariant(x, sym, t)
			return
		}
		c.setSymType(sym, t)
		if containsCapability(t) {
			env.rememberCapability(sym)
		}
	case *ast.TuplePat:
		tt, ok := t.(*types.Tuple)
		if !ok {
			for _, e := range x.Elems {
				c.bindPatternTypes(e, types.ErrorType, env)
			}
			if !types.IsError(t) {
				c.errNode(x, diag.CodeTypeMismatch,
					"tuple pattern does not match type `%s`", t)
			}
			return
		}
		if len(tt.Elems) != len(x.Elems) {
			c.errNode(x, diag.CodeTypeMismatch,
				"tuple pattern has %d element(s), type `%s` has %d",
				len(x.Elems), t, len(tt.Elems))
			return
		}
		for i, e := range x.Elems {
			c.bindPatternTypes(e, tt.Elems[i], env)
		}
	case *ast.StructPat:
		c.bindStructPattern(x, t, env)
	case *ast.VariantPat:
		c.bindVariantPattern(x, t, env)
	case *ast.RangePat:
		if !types.IsOrdered(t) && !types.IsError(t) {
			c.errNode(x, diag.CodeRangePatternNonOrd,
				"range pattern requires an `Ordered` scrutinee, got `%s`", t)
		}
		if x.Start != nil {
			c.checkExpr(x.Start, t, env)
		}
		if x.Stop != nil {
			c.checkExpr(x.Stop, t, env)
		}
	case *ast.OrPat:
		for _, alt := range x.Alts {
			c.bindPatternTypes(alt, t, env)
		}
	case *ast.BindingPat:
		sym := c.patBindingSym(x, x.Name, x.PosV)
		c.setSymType(sym, t)
		if containsCapability(t) {
			env.rememberCapability(sym)
		}
		c.bindPatternTypes(x.Pattern, t, env)
	}
}

// markPatternMut toggles the Mut flag on every binding introduced by the
// pattern. Used by `let mut`.
func (c *checker) markPatternMut(p ast.Pattern, mut bool) {
	if !mut || p == nil {
		return
	}
	switch x := p.(type) {
	case *ast.IdentPat:
		c.info(c.patBindingSym(x, x.Name, x.PosV)).Mut = true
	case *ast.TuplePat:
		for _, e := range x.Elems {
			c.markPatternMut(e, true)
		}
	case *ast.StructPat:
		for _, f := range x.Fields {
			if f.Pattern != nil {
				c.markPatternMut(f.Pattern, true)
			} else {
				c.info(c.patBindingSym(f, f.Name, f.PosV)).Mut = true
			}
		}
	case *ast.BindingPat:
		c.info(c.patBindingSym(x, x.Name, x.PosV)).Mut = true
		c.markPatternMut(x.Pattern, true)
	}
}

// bindStructPattern matches struct fields.
func (c *checker) bindStructPattern(p *ast.StructPat, t types.Type, env *env) {
	n, ok := types.AsNamed(t)
	if !ok {
		if !types.IsError(t) {
			c.errNode(p, diag.CodeTypeMismatch,
				"struct pattern does not match type `%s`", t)
		}
		for _, f := range p.Fields {
			if f.Pattern != nil {
				c.bindPatternTypes(f.Pattern, types.ErrorType, env)
			}
		}
		return
	}
	desc, ok := c.result.Descs[n.Sym]
	if !ok || desc.Kind != resolve.SymStruct {
		c.errNode(p, diag.CodeNotAStruct,
			"`%s` is not a struct", n.Sym.Name)
		return
	}
	want := map[string]*fieldDesc{}
	for _, f := range desc.Fields {
		want[f.Name] = f
	}
	sub := bindArgs(desc.Generics, n.Args)
	for _, f := range p.Fields {
		fd, ok := want[f.Name]
		if !ok {
			c.errNode(f, diag.CodeUnknownStructField,
				"struct `%s` has no field `%s`", n.Sym.Name, f.Name)
			continue
		}
		ft := fd.Type
		if len(sub) > 0 {
			ft = substituteTypeVars(ft, sub)
		}
		if f.Pattern != nil {
			c.bindPatternTypes(f.Pattern, ft, env)
		} else {
			// Shorthand: binds a fresh name of the same spelling.
			sym := c.patBindingSym(f, f.Name, f.PosV)
			c.setSymType(sym, ft)
			if containsCapability(ft) {
				env.rememberCapability(sym)
			}
		}
	}
}

// bindVariantPattern matches Some(x), Ok(v), Err(e), Color.Red, etc.
//
// Dispatch:
//  1. `Some` / `None` against the Optional sugar type get dedicated
//     handling because they don't flow through a typeDesc.
//  2. Everything else is looked up as a variant symbol on the
//     scrutinee's enum (or any visible enum for bare-name patterns),
//     with TypeVar substitution applied for generic enums like
//     `Result<T, E>`.
func (c *checker) bindVariantPattern(p *ast.VariantPat, t types.Type, env *env) {
	if len(p.Path) == 0 {
		return
	}
	head := p.Path[0]
	// Option sugar: Some / None against Optional[T].
	if head == "Some" {
		inner, ok := types.AsOptional(t)
		if !ok {
			if !types.IsError(t) {
				c.errNode(p, diag.CodeTypeMismatch,
					"`Some(...)` pattern against non-optional type `%s`", t)
			}
			inner = types.ErrorType
		}
		if len(p.Args) != 1 {
			c.errNode(p, diag.CodeVariantShape,
				"`Some` takes exactly 1 pattern argument, got %d", len(p.Args))
			return
		}
		c.bindPatternTypes(p.Args[0], inner, env)
		return
	}
	if head == "None" {
		if _, ok := types.AsOptional(t); !ok && !types.IsError(t) {
			c.errNode(p, diag.CodeTypeMismatch,
				"`None` pattern against non-optional type `%s`", t)
		}
		return
	}

	// Result sugar: Ok(v) / Err(e) against Result<T, E>. Like Some/None,
	// Result is a prelude builtin without a typeDesc entry — we resolve
	// the inner types from the scrutinee's Named type args directly.
	//
	// A user-defined `enum Result { ... }` shadowing the prelude would
	// still hit this branch first. That's intentional: Osty's spec treats
	// Ok/Err as canonical Result constructors, and shadowing at the enum-
	// declaration level is already flagged elsewhere.
	if head == "Ok" || head == "Err" {
		if n, ok := types.AsNamedByName(t, "Result"); ok && len(n.Args) == 2 {
			inner := n.Args[0]
			if head == "Err" {
				inner = n.Args[1]
			}
			if len(p.Args) != 1 {
				c.errNode(p, diag.CodeVariantShape,
					"`%s` takes exactly 1 pattern argument, got %d", head, len(p.Args))
				return
			}
			c.bindPatternTypes(p.Args[0], inner, env)
			return
		}
		if !types.IsError(t) {
			c.errNode(p, diag.CodeTypeMismatch,
				"`%s(...)` pattern against non-Result type `%s`", head, t)
		}
		for _, a := range p.Args {
			c.bindPatternTypes(a, types.ErrorType, env)
		}
		return
	}

	// Prefer the variant declared on the scrutinee's enum (handles
	// user-defined Result<T, E> whose `Ok` shadows the prelude builtin),
	// falling back to scope lookup for bare names like `Red`.
	var varSym *resolve.Symbol
	var enumDesc *typeDesc
	variantName := head
	if len(p.Path) >= 2 {
		if sym := c.topLevelSym(p.Path[len(p.Path)-2]); sym != nil && sym.Kind == resolve.SymEnum {
			if desc, ok := c.result.Descs[sym]; ok && desc != nil && desc.Kind == resolve.SymEnum {
				if vd, found := desc.Variants[p.Path[len(p.Path)-1]]; found {
					enumDesc = desc
					variantName = vd.Name
					varSym = vd.Sym
				}
			}
		}
	}
	if varSym == nil {
		if n, ok := types.AsNamed(t); ok {
			if desc, ok := c.result.Descs[n.Sym]; ok && desc.Kind == resolve.SymEnum {
				if v, found := desc.Variants[variantName]; found {
					enumDesc = desc
					varSym = v.Sym
				}
			}
		}
	}
	if varSym == nil {
		if sym := c.topLevelSym(variantName); sym != nil && sym.Kind == resolve.SymVariant {
			varSym = sym
			if info := c.info(sym); info != nil {
				enumDesc = info.Enum
			}
		}
	}

	if varSym == nil || enumDesc == nil {
		// The head may be a prelude builtin (Ok/Err against user Result
		// overrides). At this point we couldn't resolve it; emit a
		// diagnostic only when the scrutinee isn't already Error.
		if !types.IsError(t) {
			c.errNode(p, diag.CodeUnknownVariant, "unknown variant `%s`", strings.Join(p.Path, "."))
		}
		for _, a := range p.Args {
			c.bindPatternTypes(a, types.ErrorType, env)
		}
		return
	}
	info := c.info(varSym)
	if info == nil {
		return
	}
	// Check scrutinee's enum matches the variant's owning enum.
	if n, ok := types.AsNamed(t); ok {
		if n.Sym != enumDesc.Sym && !types.IsError(t) {
			c.errNode(p, diag.CodeTypeMismatch,
				"variant `%s` belongs to enum `%s`, not `%s`",
				variantName, enumDesc.Sym.Name, n.Sym.Name)
		}
	}
	if len(p.Args) != len(info.VariantFields) {
		c.errNode(p, diag.CodeVariantShape,
			"variant `%s` takes %d payload pattern(s), got %d",
			variantName, len(info.VariantFields), len(p.Args))
		return
	}
	// Apply scrutinee's type-arg substitutions to variant fields.
	var sub map[*resolve.Symbol]types.Type
	if n, ok := types.AsNamed(t); ok {
		sub = bindArgs(enumDesc.Generics, n.Args)
	}
	for i, a := range p.Args {
		ft := info.VariantFields[i]
		if len(sub) > 0 {
			ft = substituteTypeVars(ft, sub)
		}
		c.bindPatternTypes(a, ft, env)
	}
}

// verifyBareVariant checks that a bare identifier pattern (e.g. `Empty`
// when an enum Shape has a bare `Empty` variant) matches the scrutinee
// type's enum.
func (c *checker) verifyBareVariant(p *ast.IdentPat, sym *resolve.Symbol, t types.Type) {
	info := c.info(sym)
	if info == nil || info.Enum == nil {
		return
	}
	if n, ok := types.AsNamed(t); ok {
		if n.Sym != info.Enum.Sym && !types.IsError(t) {
			c.errNode(p, diag.CodeTypeMismatch,
				"variant `%s` belongs to enum `%s`, not `%s`",
				sym.Name, info.Enum.Sym.Name, n.Sym.Name)
		}
	}
}

// patBindingSym resolves a pattern-binding Symbol through the Decl→Sym
// reverse index. A synthesized fallback is returned for bindings that
// the resolver installed but that are never referenced: since no later
// ref lookup will find that binding, writing to the synthesized pointer
// via setSymType is silently discarded — equivalent to not writing at
// all, which is the right behavior for unused bindings.
func (c *checker) patBindingSym(n ast.Node, name string, pos token.Pos) *resolve.Symbol {
	if s := c.symByDecl(n); s != nil {
		return s
	}
	return &resolve.Symbol{Name: name, Kind: resolve.SymLet, Pos: pos, Decl: n}
}
