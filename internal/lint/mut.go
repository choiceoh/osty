package lint

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/token"
)

// lintUnusedMut flags `let mut` bindings that are never reassigned or
// have their contents mutated. Analogous to Rust's `unused_mut`.
//
// The rule considers the following as a "mutation" of a binding `x`:
//
//	x = y           — simple assign, including compound forms (+=, -=, …)
//	x[i] = v        — index assignment (mutates the backing collection)
//	x.field = v     — field assignment
//	x?.field = v    — optional-chained field assignment
//
// For each of these targets we walk down to the root identifier; that
// identifier's referenced declaration is marked as mutated.
//
// Bindings introduced via pattern destructuring (`let mut (a, b) = …`)
// are all treated uniformly — each inner IdentPat is flagged individually
// if that name is never mutated in any of the above forms.
func (l *linter) lintUnusedMut() {
	if l.resolved == nil {
		return
	}
	// Step 1: find every `mut` binding's declaration node.
	//
	// mutPos is the position of the `mut` keyword in the enclosing let;
	// zero when we don't have it (e.g. destructured struct fields whose
	// enclosing LetStmt carries the token). The autofix uses this span
	// to delete just `mut ` from the source.
	type mutBinding struct {
		decl   ast.Node // the IdentPat or LetDecl node
		name   string
		mutPos token.Pos
	}
	var mutBindings []mutBinding

	var collectPattern func(p ast.Pattern, mutPos token.Pos)
	collectPattern = func(p ast.Pattern, mutPos token.Pos) {
		if p == nil {
			return
		}
		switch n := p.(type) {
		case *ast.IdentPat:
			if !isUnderscore(n.Name) {
				mutBindings = append(mutBindings, mutBinding{decl: n, name: n.Name, mutPos: mutPos})
			}
		case *ast.BindingPat:
			if !isUnderscore(n.Name) {
				mutBindings = append(mutBindings, mutBinding{decl: n, name: n.Name, mutPos: mutPos})
			}
			collectPattern(n.Pattern, mutPos)
		case *ast.TuplePat:
			for _, e := range n.Elems {
				collectPattern(e, mutPos)
			}
		case *ast.StructPat:
			for _, f := range n.Fields {
				if f.Pattern != nil {
					collectPattern(f.Pattern, mutPos)
				} else if !isUnderscore(f.Name) {
					mutBindings = append(mutBindings, mutBinding{decl: f, name: f.Name, mutPos: mutPos})
				}
			}
		case *ast.VariantPat:
			for _, a := range n.Args {
				collectPattern(a, mutPos)
			}
		case *ast.OrPat:
			for _, a := range n.Alts {
				collectPattern(a, mutPos)
			}
		}
	}

	var walkBlockForMut func(b *ast.Block)
	var walkStmtForMut func(s ast.Stmt)
	var walkExprForMut func(e ast.Expr)

	walkBlockForMut = func(b *ast.Block) {
		if b == nil {
			return
		}
		for _, s := range b.Stmts {
			walkStmtForMut(s)
		}
	}
	walkStmtForMut = func(s ast.Stmt) {
		switch n := s.(type) {
		case *ast.LetStmt:
			if n.Mut {
				collectPattern(n.Pattern, n.MutPos)
			}
			walkExprForMut(n.Value)
		case *ast.ExprStmt:
			walkExprForMut(n.X)
		case *ast.AssignStmt:
			walkExprForMut(n.Value)
			for _, t := range n.Targets {
				walkExprForMut(t)
			}
		case *ast.ReturnStmt:
			walkExprForMut(n.Value)
		case *ast.DeferStmt:
			walkExprForMut(n.X)
		case *ast.ForStmt:
			walkExprForMut(n.Iter)
			walkBlockForMut(n.Body)
		case *ast.ChanSendStmt:
			walkExprForMut(n.Channel)
			walkExprForMut(n.Value)
		case *ast.Block:
			walkBlockForMut(n)
		}
	}
	walkExprForMut = func(e ast.Expr) {
		if e == nil {
			return
		}
		switch n := e.(type) {
		case *ast.Block:
			walkBlockForMut(n)
		case *ast.IfExpr:
			walkExprForMut(n.Cond)
			walkBlockForMut(n.Then)
			walkExprForMut(n.Else)
		case *ast.MatchExpr:
			walkExprForMut(n.Scrutinee)
			for _, arm := range n.Arms {
				walkExprForMut(arm.Guard)
				walkExprForMut(arm.Body)
			}
		case *ast.ClosureExpr:
			walkExprForMut(n.Body)
		case *ast.UnaryExpr:
			walkExprForMut(n.X)
		case *ast.BinaryExpr:
			walkExprForMut(n.Left)
			walkExprForMut(n.Right)
		case *ast.CallExpr:
			walkExprForMut(n.Fn)
			for _, a := range n.Args {
				walkExprForMut(a.Value)
			}
		case *ast.FieldExpr:
			walkExprForMut(n.X)
		case *ast.IndexExpr:
			walkExprForMut(n.X)
			walkExprForMut(n.Index)
		case *ast.ParenExpr:
			walkExprForMut(n.X)
		case *ast.TupleExpr:
			for _, x := range n.Elems {
				walkExprForMut(x)
			}
		case *ast.ListExpr:
			for _, x := range n.Elems {
				walkExprForMut(x)
			}
		case *ast.MapExpr:
			for _, me := range n.Entries {
				walkExprForMut(me.Key)
				walkExprForMut(me.Value)
			}
		case *ast.StructLit:
			walkExprForMut(n.Type)
			for _, f := range n.Fields {
				walkExprForMut(f.Value)
			}
			walkExprForMut(n.Spread)
		case *ast.RangeExpr:
			walkExprForMut(n.Start)
			walkExprForMut(n.Stop)
		case *ast.QuestionExpr:
			walkExprForMut(n.X)
		case *ast.TurbofishExpr:
			walkExprForMut(n.Base)
		}
	}

	// Collect top-level `let mut` (LetDecl with Mut=true).
	for _, d := range l.file.Decls {
		if ld, ok := d.(*ast.LetDecl); ok && ld.Mut && !isUnderscore(ld.Name) {
			mutBindings = append(mutBindings, mutBinding{decl: ld, name: ld.Name, mutPos: ld.MutPos})
		}
	}
	// Collect method and fn bodies' `let mut` bindings.
	for _, d := range l.file.Decls {
		switch n := d.(type) {
		case *ast.FnDecl:
			if n.Body != nil {
				walkBlockForMut(n.Body)
			}
		case *ast.StructDecl:
			for _, m := range n.Methods {
				if m.Body != nil {
					walkBlockForMut(m.Body)
				}
			}
		case *ast.EnumDecl:
			for _, m := range n.Methods {
				if m.Body != nil {
					walkBlockForMut(m.Body)
				}
			}
		case *ast.InterfaceDecl:
			for _, m := range n.Methods {
				if m.Body != nil {
					walkBlockForMut(m.Body)
				}
			}
		}
	}
	for _, s := range l.file.Stmts {
		walkStmtForMut(s)
	}

	if len(mutBindings) == 0 {
		return
	}

	// Step 2: find every mutation site — AssignStmt targets, channel
	// sends do NOT count (they're reads on the channel, not writes into
	// the variable holding the channel).
	mutated := map[ast.Node]bool{}
	var scanBlock func(b *ast.Block)
	var scanStmt func(s ast.Stmt)
	var scanExpr func(e ast.Expr)
	scanBlock = func(b *ast.Block) {
		if b == nil {
			return
		}
		for _, s := range b.Stmts {
			scanStmt(s)
		}
	}
	scanStmt = func(s ast.Stmt) {
		switch n := s.(type) {
		case *ast.LetStmt:
			scanExpr(n.Value)
		case *ast.ExprStmt:
			scanExpr(n.X)
		case *ast.AssignStmt:
			// Record each target's root-identifier declaration as
			// mutated. Then scan the value / targets for nested
			// assignments.
			for _, t := range n.Targets {
				if decl := l.rootIdentDecl(t); decl != nil {
					mutated[decl] = true
				}
				scanExpr(t)
			}
			scanExpr(n.Value)
		case *ast.ReturnStmt:
			scanExpr(n.Value)
		case *ast.DeferStmt:
			scanExpr(n.X)
		case *ast.ForStmt:
			scanExpr(n.Iter)
			scanBlock(n.Body)
		case *ast.ChanSendStmt:
			scanExpr(n.Channel)
			scanExpr(n.Value)
		case *ast.Block:
			scanBlock(n)
		}
	}
	scanExpr = func(e ast.Expr) {
		if e == nil {
			return
		}
		switch n := e.(type) {
		case *ast.Block:
			scanBlock(n)
		case *ast.IfExpr:
			scanExpr(n.Cond)
			scanBlock(n.Then)
			scanExpr(n.Else)
		case *ast.MatchExpr:
			scanExpr(n.Scrutinee)
			for _, arm := range n.Arms {
				scanExpr(arm.Guard)
				scanExpr(arm.Body)
			}
		case *ast.ClosureExpr:
			scanExpr(n.Body)
		case *ast.UnaryExpr:
			scanExpr(n.X)
		case *ast.BinaryExpr:
			scanExpr(n.Left)
			scanExpr(n.Right)
		case *ast.CallExpr:
			// Method calls (`x.method(...)`) may invoke a `mut self`
			// method, which mutates the binding through the receiver.
			// Without type info we can't tell whether the specific
			// method requires mut, so we conservatively mark the
			// root-ident receiver as mutated. This has false negatives
			// (read-only methods like `.len()` keep the mut alive) but
			// avoids false positives — the worst case is that unused
			// mut on a binding only ever used via method calls isn't
			// flagged, which matches Rust's behavior in the same case.
			if fe, ok := n.Fn.(*ast.FieldExpr); ok {
				if decl := l.rootIdentDecl(fe.X); decl != nil {
					mutated[decl] = true
				}
			}
			scanExpr(n.Fn)
			for _, a := range n.Args {
				scanExpr(a.Value)
			}
		case *ast.FieldExpr:
			scanExpr(n.X)
		case *ast.IndexExpr:
			scanExpr(n.X)
			scanExpr(n.Index)
		case *ast.ParenExpr:
			scanExpr(n.X)
		case *ast.TupleExpr:
			for _, x := range n.Elems {
				scanExpr(x)
			}
		case *ast.ListExpr:
			for _, x := range n.Elems {
				scanExpr(x)
			}
		case *ast.MapExpr:
			for _, me := range n.Entries {
				scanExpr(me.Key)
				scanExpr(me.Value)
			}
		case *ast.StructLit:
			scanExpr(n.Type)
			for _, f := range n.Fields {
				scanExpr(f.Value)
			}
			scanExpr(n.Spread)
		case *ast.RangeExpr:
			scanExpr(n.Start)
			scanExpr(n.Stop)
		case *ast.QuestionExpr:
			scanExpr(n.X)
		case *ast.TurbofishExpr:
			scanExpr(n.Base)
		}
	}
	for _, d := range l.file.Decls {
		switch n := d.(type) {
		case *ast.FnDecl:
			scanBlock(n.Body)
		case *ast.StructDecl:
			for _, m := range n.Methods {
				scanBlock(m.Body)
			}
		case *ast.EnumDecl:
			for _, m := range n.Methods {
				scanBlock(m.Body)
			}
		case *ast.InterfaceDecl:
			for _, m := range n.Methods {
				scanBlock(m.Body)
			}
		case *ast.LetDecl:
			scanExpr(n.Value)
		}
	}
	for _, s := range l.file.Stmts {
		scanStmt(s)
	}

	// Step 3: emit for each mut binding whose decl node is not in `mutated`.
	//
	// When we captured the `mut` keyword's position (via MutPos on
	// LetStmt/LetDecl), attach a machine-applicable fix that deletes the
	// token plus its single trailing space — rewriting `let mut x` to
	// `let x`. The +4 end offset covers the four bytes `mut ` exactly;
	// if parser layout ever changes, ApplyFixes will simply skip the
	// suggestion as out-of-range rather than producing garbage.
	for _, mb := range mutBindings {
		if mutated[mb.decl] {
			continue
		}
		b := diag.New(diag.Warning,
			"binding `"+mb.name+"` is declared `mut` but never reassigned").
			Code(diag.CodeUnusedMut).
			Primary(diag.Span{Start: mb.decl.Pos(), End: mb.decl.End()}, "")
		if mb.mutPos.Offset > 0 || mb.mutPos.Line > 0 {
			// Delete `mut ` — four bytes. The ASCII `mut` keyword is
			// always followed by whitespace in the surface syntax, so
			// taking one extra byte is safe.
			delSpan := diag.Span{
				Start: mb.mutPos,
				End:   token.Pos{Offset: mb.mutPos.Offset + 4, Line: mb.mutPos.Line, Column: mb.mutPos.Column + 4},
			}
			b = b.Hint("remove the `mut` — the binding is immutable as written").
				Suggest(delSpan, "", "remove `mut`", true)
		}
		l.emit(b.Build())
	}

	// ---- L0008: dead store ----
	//
	// Pattern: `let mut x = E1; ... x = E2; ... read(x)` where the block
	// between `let` and the first reassign never reads `x`. E1's value is
	// then "dead" — it's overwritten before ever being observed.
	//
	// We limit the analysis to one block at a time (no inter-block flow),
	// which catches the common clippy-equivalent case while avoiding the
	// full data-flow machinery.
	for _, d := range l.file.Decls {
		l.deadStoreDecl(d)
	}
	for _, s := range l.file.Stmts {
		l.deadStoreStmt(s)
	}
}

// deadStoreEntry tracks one `let mut` binding that hasn't been read yet.
type deadStoreEntry struct {
	name     string
	decl     ast.Node
	initNode ast.Node // the LetStmt, used for span
	pending  bool
}

// deadStoreDecl recurses into decl bodies where blocks live.
func (l *linter) deadStoreDecl(d ast.Decl) {
	switch n := d.(type) {
	case *ast.FnDecl:
		l.deadStoreBlock(n.Body)
	case *ast.StructDecl:
		for _, m := range n.Methods {
			l.deadStoreBlock(m.Body)
		}
	case *ast.EnumDecl:
		for _, m := range n.Methods {
			l.deadStoreBlock(m.Body)
		}
	case *ast.InterfaceDecl:
		for _, m := range n.Methods {
			l.deadStoreBlock(m.Body)
		}
	}
}

// deadStoreBlock scans a single block for `let mut` followed later by an
// assignment that hasn't been preceded by a read.
func (l *linter) deadStoreBlock(b *ast.Block) {
	if b == nil {
		return
	}
	pending := map[ast.Node]*deadStoreEntry{}

	for _, s := range b.Stmts {
		switch n := s.(type) {
		case *ast.LetStmt:
			if n.Mut {
				collectMutBindingsForDeadStore(n.Pattern, n, pending)
			}
			l.deadStoreExpr(n.Value)
		case *ast.AssignStmt:
			if n.Op == token.ASSIGN && len(n.Targets) == 1 {
				if decl := l.rootIdentDecl(n.Targets[0]); decl != nil {
					if t, ok := pending[decl]; ok && t.pending {
						// RHS may read the old value — check first.
						if exprReadsDecl(n.Value, decl, l.resolved) {
							t.pending = false
						} else {
							l.emit(diag.New(diag.Warning,
								"value of `"+t.name+"` is overwritten before being read").
								Code(diag.CodeDeadStore).
								Primary(diag.Span{Start: t.initNode.Pos(), End: t.initNode.End()},
									"this value is never read").
								Secondary(diag.Span{Start: n.PosV, End: n.EndV},
									"overwritten here").
								Hint("remove the initial assignment, or read the value before overwriting").
								Build())
							t.pending = false
						}
					}
				}
			}
			l.deadStoreExpr(n.Value)
			for _, tgt := range n.Targets {
				markReadsForDeadStore(tgt, pending, l.resolved)
			}
		case *ast.ExprStmt:
			markReadsForDeadStore(n.X, pending, l.resolved)
			l.deadStoreExpr(n.X)
		case *ast.ReturnStmt:
			markReadsForDeadStore(n.Value, pending, l.resolved)
			l.deadStoreExpr(n.Value)
		case *ast.DeferStmt:
			markReadsForDeadStore(n.X, pending, l.resolved)
			l.deadStoreExpr(n.X)
		case *ast.ForStmt:
			markReadsForDeadStore(n.Iter, pending, l.resolved)
			l.deadStoreBlock(n.Body)
		case *ast.ChanSendStmt:
			markReadsForDeadStore(n.Channel, pending, l.resolved)
			markReadsForDeadStore(n.Value, pending, l.resolved)
		case *ast.Block:
			l.deadStoreBlock(n)
		}
	}
}

// deadStoreStmt recurses into a single statement and its nested blocks.
func (l *linter) deadStoreStmt(s ast.Stmt) {
	switch n := s.(type) {
	case *ast.Block:
		l.deadStoreBlock(n)
	case *ast.ForStmt:
		l.deadStoreBlock(n.Body)
	case *ast.ExprStmt:
		l.deadStoreExpr(n.X)
	case *ast.LetStmt:
		l.deadStoreExpr(n.Value)
	case *ast.AssignStmt:
		l.deadStoreExpr(n.Value)
	case *ast.ReturnStmt:
		l.deadStoreExpr(n.Value)
	case *ast.DeferStmt:
		l.deadStoreExpr(n.X)
	}
}

func (l *linter) deadStoreExpr(e ast.Expr) {
	if e == nil {
		return
	}
	switch n := e.(type) {
	case *ast.Block:
		l.deadStoreBlock(n)
	case *ast.IfExpr:
		l.deadStoreBlock(n.Then)
		l.deadStoreExpr(n.Else)
	case *ast.MatchExpr:
		for _, arm := range n.Arms {
			l.deadStoreExpr(arm.Body)
		}
	case *ast.ClosureExpr:
		l.deadStoreExpr(n.Body)
	}
}

// collectMutBindingsForDeadStore walks a `let mut (p, q) = …` pattern
// and records every IdentPat as a fresh dead-store candidate.
func collectMutBindingsForDeadStore(p ast.Pattern, initStmt ast.Node, out map[ast.Node]*deadStoreEntry) {
	switch n := p.(type) {
	case *ast.IdentPat:
		if !isUnderscore(n.Name) {
			out[n] = &deadStoreEntry{name: n.Name, decl: n, initNode: initStmt, pending: true}
		}
	case *ast.BindingPat:
		if !isUnderscore(n.Name) {
			out[n] = &deadStoreEntry{name: n.Name, decl: n, initNode: initStmt, pending: true}
		}
		collectMutBindingsForDeadStore(n.Pattern, initStmt, out)
	case *ast.TuplePat:
		for _, e := range n.Elems {
			collectMutBindingsForDeadStore(e, initStmt, out)
		}
	}
}

// markReadsForDeadStore walks an expression and clears the `pending`
// flag on every tracked binding it reads.
func markReadsForDeadStore(e ast.Expr, pending map[ast.Node]*deadStoreEntry, rr *resolve.Result) {
	if e == nil || rr == nil {
		return
	}
	// Walk every Ident in e; for each, resolve to its decl and clear
	// any matching entry in `pending`.
	var walk func(e ast.Expr)
	walk = func(e ast.Expr) {
		if e == nil {
			return
		}
		switch n := e.(type) {
		case *ast.Ident:
			if sym := rr.RefsByID[n.ID]; sym != nil && sym.Decl != nil {
				if t, ok := pending[sym.Decl]; ok {
					t.pending = false
				}
			}
		case *ast.FieldExpr:
			walk(n.X)
		case *ast.IndexExpr:
			walk(n.X)
			walk(n.Index)
		case *ast.ParenExpr:
			walk(n.X)
		case *ast.UnaryExpr:
			walk(n.X)
		case *ast.BinaryExpr:
			walk(n.Left)
			walk(n.Right)
		case *ast.CallExpr:
			walk(n.Fn)
			for _, a := range n.Args {
				walk(a.Value)
			}
		case *ast.TupleExpr:
			for _, x := range n.Elems {
				walk(x)
			}
		case *ast.ListExpr:
			for _, x := range n.Elems {
				walk(x)
			}
		case *ast.MapExpr:
			for _, me := range n.Entries {
				walk(me.Key)
				walk(me.Value)
			}
		case *ast.StructLit:
			walk(n.Type)
			for _, f := range n.Fields {
				walk(f.Value)
			}
			walk(n.Spread)
		case *ast.RangeExpr:
			walk(n.Start)
			walk(n.Stop)
		case *ast.QuestionExpr:
			walk(n.X)
		case *ast.TurbofishExpr:
			walk(n.Base)
		}
	}
	walk(e)
}

// exprReadsDecl reports whether an expression reads a particular decl
// via any Ident reference. Used to decide if `x = x + 1` reads the old x.
func exprReadsDecl(e ast.Expr, decl ast.Node, rr *resolve.Result) bool {
	if e == nil || rr == nil {
		return false
	}
	found := false
	var walk func(e ast.Expr)
	walk = func(e ast.Expr) {
		if found || e == nil {
			return
		}
		if id, ok := e.(*ast.Ident); ok {
			if sym := rr.RefsByID[id.ID]; sym != nil && sym.Decl == decl {
				found = true
				return
			}
		}
		switch n := e.(type) {
		case *ast.FieldExpr:
			walk(n.X)
		case *ast.IndexExpr:
			walk(n.X)
			walk(n.Index)
		case *ast.ParenExpr:
			walk(n.X)
		case *ast.UnaryExpr:
			walk(n.X)
		case *ast.BinaryExpr:
			walk(n.Left)
			walk(n.Right)
		case *ast.CallExpr:
			walk(n.Fn)
			for _, a := range n.Args {
				walk(a.Value)
			}
		case *ast.TupleExpr:
			for _, x := range n.Elems {
				walk(x)
			}
		case *ast.ListExpr:
			for _, x := range n.Elems {
				walk(x)
			}
		case *ast.MapExpr:
			for _, me := range n.Entries {
				walk(me.Key)
				walk(me.Value)
			}
		case *ast.StructLit:
			walk(n.Type)
			for _, f := range n.Fields {
				walk(f.Value)
			}
			walk(n.Spread)
		case *ast.RangeExpr:
			walk(n.Start)
			walk(n.Stop)
		case *ast.QuestionExpr:
			walk(n.X)
		case *ast.TurbofishExpr:
			walk(n.Base)
		}
	}
	walk(e)
	return found
}

// rootIdentDecl walks down a target expression (e.g. `x.a.b[i]`) to its
// root identifier and returns the declaration node that identifier
// refers to. Returns nil if the target isn't rooted at a resolved
// identifier.
func (l *linter) rootIdentDecl(e ast.Expr) ast.Node {
	for e != nil {
		switch n := e.(type) {
		case *ast.Ident:
			if l.resolved == nil {
				return nil
			}
			if sym := l.resolved.RefsByID[n.ID]; sym != nil {
				return sym.Decl
			}
			return nil
		case *ast.FieldExpr:
			e = n.X
		case *ast.IndexExpr:
			e = n.X
		case *ast.ParenExpr:
			e = n.X
		case *ast.QuestionExpr:
			e = n.X
		case *ast.TurbofishExpr:
			e = n.Base
		default:
			return nil
		}
	}
	return nil
}
