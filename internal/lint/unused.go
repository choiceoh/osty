package lint

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/token"
)

// lintUnused flags every bound name that is never referenced anywhere in
// the resolved source:
//
//	L0001 - let bindings (both statement lets and top-level LetDecls)
//	L0002 - function / closure parameters
//	L0003 - `use` aliases
//
// Names that intentionally go unused (`_foo`), publicly exported items
// (`pub fn`, `pub let`), and the implicit `self` / `Self` are excluded.
// In package mode the "used" set is the union across every file in the
// package, so cross-file references don't trigger false positives.
//
// Implementation note: unused bindings are by definition NOT reachable
// from the resolver's Refs/TypeRefs maps (and their enclosing scope is
// no longer live after resolve finishes). We therefore cannot look up
// the Symbol for a pattern binding directly; instead we build a set of
// "declaration AST nodes that some reference points back at" via the
// Symbol.Decl field, then check each declaration's node against that
// set.
func (l *linter) lintUnused() {
	usedDecls := buildUsedDeclSet(l.resolved, l.used)

	// ---- L0003: unused `use` aliases ----
	for _, u := range l.file.Uses {
		alias := useAlias(u)
		if alias == "" || isUnderscore(alias) {
			continue
		}
		if usedDecls[u] {
			continue
		}
		// Fallback: if we didn't record the UseDecl but the alias symbol
		// lives in the file scope and is marked used, skip. Some future
		// resolver changes may surface the alias via its scope rather
		// than its Decl pointer.
		if l.resolved != nil && l.resolved.FileScope != nil {
			if sym := l.resolved.FileScope.LookupLocal(alias); sym != nil && l.used[sym] {
				continue
			}
		}
		l.emit(diag.New(diag.Warning,
			"imported `"+alias+"` is never used").
			Code(diag.CodeUnusedImport).
			Primary(diag.Span{Start: u.PosV, End: u.EndV}, "unused import").
			Hint("remove this `use` declaration, or prefix the alias with `_` if it's kept for side effects").
			Suggest(diag.Span{Start: u.PosV, End: u.EndV}, "",
				"delete the unused import", true).
			Build())
	}

	// ---- L0001 / L0002: walk bodies for lets + params ----
	for _, d := range l.file.Decls {
		l.unusedDecl(d, usedDecls)
	}
	for _, s := range l.file.Stmts {
		l.unusedStmt(s, usedDecls)
	}
}

// buildUsedDeclSet returns the set of AST declaration nodes that at
// least one resolved reference points back at. It also includes every
// Symbol's Decl node that lives in any reachable scope (file + package),
// so top-level items declared-but-not-referenced are still distinguished
// from "we never even saw this node".
func buildUsedDeclSet(rr *resolve.Result, usedSyms map[*resolve.Symbol]bool) map[ast.Node]bool {
	used := map[ast.Node]bool{}
	if rr == nil {
		return used
	}
	for _, sym := range rr.Refs {
		if sym != nil && sym.Decl != nil {
			used[sym.Decl] = true
		}
	}
	for _, sym := range rr.TypeRefs {
		if sym != nil && sym.Decl != nil {
			used[sym.Decl] = true
		}
	}
	// Reachable top-level declarations may also be marked used via the
	// package-wide usedSyms set (in package mode, references from other
	// files feed this).
	for sym, ok := range usedSyms {
		if ok && sym != nil && sym.Decl != nil {
			used[sym.Decl] = true
		}
	}
	return used
}

func (l *linter) unusedDecl(d ast.Decl, usedDecls map[ast.Node]bool) {
	switch n := d.(type) {
	case *ast.FnDecl:
		l.checkFnParams(n.Params, usedDecls, n.Pub)
		if n.Body != nil {
			l.unusedBlock(n.Body, usedDecls)
		}
	case *ast.StructDecl:
		for _, m := range n.Methods {
			ispub := n.Pub || m.Pub
			l.checkFnParams(m.Params, usedDecls, ispub)
			if m.Body != nil {
				l.unusedBlock(m.Body, usedDecls)
			}
		}
	case *ast.EnumDecl:
		for _, m := range n.Methods {
			ispub := n.Pub || m.Pub
			l.checkFnParams(m.Params, usedDecls, ispub)
			if m.Body != nil {
				l.unusedBlock(m.Body, usedDecls)
			}
		}
	case *ast.InterfaceDecl:
		// Interface method signatures are fixed by the contract; don't
		// flag their params. Default bodies still get walked for nested
		// lets.
		for _, m := range n.Methods {
			if m.Body != nil {
				l.unusedBlock(m.Body, usedDecls)
			}
		}
	case *ast.LetDecl:
		if !n.Pub && !isUnderscore(n.Name) && !usedDecls[n] {
			l.warnNode(n, diag.CodeUnusedLet,
				"binding `%s` is never used", n.Name)
		}
		l.unusedExpr(n.Value, usedDecls)
	}
}

func (l *linter) unusedBlock(b *ast.Block, usedDecls map[ast.Node]bool) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		l.unusedStmt(s, usedDecls)
	}
}

func (l *linter) unusedStmt(s ast.Stmt, usedDecls map[ast.Node]bool) {
	switch n := s.(type) {
	case *ast.LetStmt:
		l.unusedPattern(n.Pattern, usedDecls)
		l.unusedExpr(n.Value, usedDecls)
	case *ast.ExprStmt:
		l.unusedExpr(n.X, usedDecls)
	case *ast.AssignStmt:
		for _, t := range n.Targets {
			l.unusedExpr(t, usedDecls)
		}
		l.unusedExpr(n.Value, usedDecls)
	case *ast.ReturnStmt:
		l.unusedExpr(n.Value, usedDecls)
	case *ast.DeferStmt:
		l.unusedExpr(n.X, usedDecls)
	case *ast.ForStmt:
		// A for-in pattern binds names within the loop body; flag each
		// binding that body never reads.
		l.unusedPattern(n.Pattern, usedDecls)
		l.unusedExpr(n.Iter, usedDecls)
		l.unusedBlock(n.Body, usedDecls)
	case *ast.ChanSendStmt:
		l.unusedExpr(n.Channel, usedDecls)
		l.unusedExpr(n.Value, usedDecls)
	case *ast.Block:
		l.unusedBlock(n, usedDecls)
	}
}

func (l *linter) unusedExpr(e ast.Expr, usedDecls map[ast.Node]bool) {
	if e == nil {
		return
	}
	switch n := e.(type) {
	case *ast.Block:
		l.unusedBlock(n, usedDecls)
	case *ast.ClosureExpr:
		l.checkFnParams(n.Params, usedDecls, false)
		l.unusedExpr(n.Body, usedDecls)
	case *ast.IfExpr:
		l.unusedExpr(n.Cond, usedDecls)
		l.unusedBlock(n.Then, usedDecls)
		l.unusedExpr(n.Else, usedDecls)
	case *ast.MatchExpr:
		l.unusedExpr(n.Scrutinee, usedDecls)
		for _, arm := range n.Arms {
			// Match arm patterns bind names just for the arm body; a
			// bare `_` arm is already exempt via the underscore filter.
			l.unusedPattern(arm.Pattern, usedDecls)
			l.unusedExpr(arm.Guard, usedDecls)
			l.unusedExpr(arm.Body, usedDecls)
		}
	case *ast.UnaryExpr:
		l.unusedExpr(n.X, usedDecls)
	case *ast.BinaryExpr:
		l.unusedExpr(n.Left, usedDecls)
		l.unusedExpr(n.Right, usedDecls)
	case *ast.CallExpr:
		l.unusedExpr(n.Fn, usedDecls)
		for _, a := range n.Args {
			l.unusedExpr(a.Value, usedDecls)
		}
	case *ast.FieldExpr:
		l.unusedExpr(n.X, usedDecls)
	case *ast.IndexExpr:
		l.unusedExpr(n.X, usedDecls)
		l.unusedExpr(n.Index, usedDecls)
	case *ast.ParenExpr:
		l.unusedExpr(n.X, usedDecls)
	case *ast.TupleExpr:
		for _, x := range n.Elems {
			l.unusedExpr(x, usedDecls)
		}
	case *ast.ListExpr:
		for _, x := range n.Elems {
			l.unusedExpr(x, usedDecls)
		}
	case *ast.MapExpr:
		for _, me := range n.Entries {
			l.unusedExpr(me.Key, usedDecls)
			l.unusedExpr(me.Value, usedDecls)
		}
	case *ast.StructLit:
		l.unusedExpr(n.Type, usedDecls)
		for _, f := range n.Fields {
			l.unusedExpr(f.Value, usedDecls)
		}
		l.unusedExpr(n.Spread, usedDecls)
	case *ast.RangeExpr:
		l.unusedExpr(n.Start, usedDecls)
		l.unusedExpr(n.Stop, usedDecls)
	case *ast.QuestionExpr:
		l.unusedExpr(n.X, usedDecls)
	case *ast.TurbofishExpr:
		l.unusedExpr(n.Base, usedDecls)
	}
}

// checkFnParams emits L0002 for each unused, non-public, non-underscore
// parameter. Destructuring closure params are delegated to unusedPattern.
func (l *linter) checkFnParams(params []*ast.Param, usedDecls map[ast.Node]bool, isPub bool) {
	if isPub {
		return
	}
	for _, p := range params {
		if p == nil {
			continue
		}
		if p.Pattern != nil {
			l.unusedPattern(p.Pattern, usedDecls)
			continue
		}
		name := p.Name
		if name == "" || name == "self" || isUnderscore(name) {
			continue
		}
		if usedDecls[p] {
			continue
		}
		l.emit(diag.New(diag.Warning,
			"parameter `"+name+"` is never used").
			Code(diag.CodeUnusedParam).
			Primary(diag.Span{Start: p.PosV, End: p.EndV}, "unused parameter").
			Suggest(diag.Span{Start: namePos(p.PosV, 0), End: namePos(p.PosV, 0)},
				"_", "rename to `_"+name+"` to mark intentionally unused", true).
			Build())
	}
}

// namePos returns a token.Pos at the same line/column as base but with
// an optional column delta. Used to construct insert-only spans.
func namePos(base token.Pos, deltaCol int) token.Pos {
	return token.Pos{
		Offset: base.Offset + deltaCol,
		Line:   base.Line,
		Column: base.Column + deltaCol,
	}
}

// unusedPattern flags each pattern-bound name whose declaration AST node
// is not in usedDecls.
func (l *linter) unusedPattern(p ast.Pattern, usedDecls map[ast.Node]bool) {
	if p == nil {
		return
	}
	switch n := p.(type) {
	case *ast.IdentPat:
		if isUnderscore(n.Name) {
			return
		}
		if !usedDecls[n] {
			l.emit(diag.New(diag.Warning,
				"binding `"+n.Name+"` is never used").
				Code(diag.CodeUnusedLet).
				Primary(diag.Span{Start: n.PosV, End: n.EndV}, "unused binding").
				Suggest(diag.Span{Start: n.PosV, End: n.PosV},
					"_", "rename to `_"+n.Name+"` to mark intentionally unused", true).
				Build())
		}
	case *ast.BindingPat:
		if !isUnderscore(n.Name) && !usedDecls[n] {
			l.emit(diag.New(diag.Warning,
				"binding `"+n.Name+"` is never used").
				Code(diag.CodeUnusedLet).
				Primary(diag.Span{Start: n.PosV, End: n.EndV}, "unused binding").
				Suggest(diag.Span{Start: n.PosV, End: n.PosV},
					"_", "rename to `_"+n.Name+"` to mark intentionally unused", true).
				Build())
		}
		l.unusedPattern(n.Pattern, usedDecls)
	case *ast.TuplePat:
		for _, e := range n.Elems {
			l.unusedPattern(e, usedDecls)
		}
	case *ast.StructPat:
		for _, f := range n.Fields {
			if f.Pattern != nil {
				l.unusedPattern(f.Pattern, usedDecls)
			} else if !isUnderscore(f.Name) && !usedDecls[f] {
				l.warnNode(f, diag.CodeUnusedLet,
					"binding `%s` is never used", f.Name)
			}
		}
	case *ast.VariantPat:
		for _, a := range n.Args {
			l.unusedPattern(a, usedDecls)
		}
	case *ast.OrPat:
		for _, a := range n.Alts {
			l.unusedPattern(a, usedDecls)
		}
	}
}

// useAlias computes the effective binding name for a `use` declaration,
// mirroring resolve.declareUse.
func useAlias(u *ast.UseDecl) string {
	if u.Alias != "" {
		return u.Alias
	}
	if u.IsGoFFI {
		return lastSegment(u.GoPath, '/')
	}
	if len(u.Path) > 0 {
		return u.Path[len(u.Path)-1]
	}
	return ""
}

func lastSegment(s string, sep byte) string {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == sep {
			return s[i+1:]
		}
	}
	return s
}

func isUnderscore(name string) bool {
	return len(name) > 0 && name[0] == '_'
}
