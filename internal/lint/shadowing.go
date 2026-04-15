package lint

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/token"
)

// lintShadowing flags bindings inside nested scopes that reuse a name
// from an enclosing scope. Covers:
//
//   - `let` inside a block that hides an outer `let`
//   - `let` that hides a function / method parameter
//   - closure parameter (`|x|`) that hides an outer binding
//   - for-loop pattern (`for x in …`) that hides an outer binding
//   - match arm pattern (`Some(x) -> …`) that hides an outer binding
//   - same-block re-declaration (`let x = 1; let x = 2` in the same
//     block), which the resolver permits but is almost always a bug
//
// Top-level function parameters are NOT considered shadowing relative
// to package/file-scope `let`s: hiding a package-scope name with a
// parameter is how functions normally work. Shadowing a builtin name
// (`List`, `String`, `true`) is also not flagged — rare, usually
// intentional.
func (l *linter) lintShadowing() {
	w := &shadowWalker{
		linter: l,
	}
	// Top-level `let`s go on the file-scope frame so nested blocks can see them.
	w.push()
	for _, d := range l.file.Decls {
		if ld, ok := d.(*ast.LetDecl); ok && !isUnderscore(ld.Name) {
			w.declareTop(ld.Name, ld.PosV, ld)
		}
	}
	for _, d := range l.file.Decls {
		w.walkDecl(d)
	}
	for _, s := range l.file.Stmts {
		w.walkStmt(s)
	}
	w.pop()
}

// shadowWalker maintains its own lexical scope stack over the AST.
type shadowWalker struct {
	linter *linter
	// stack frames — each frame maps name -> position of the prior
	// declaration in that scope. Most recently pushed frame is stack[0];
	// we iterate outward for shadow detection.
	stack []map[string]token.Pos
}

func (w *shadowWalker) push() {
	w.stack = append([]map[string]token.Pos{{}}, w.stack...)
}

func (w *shadowWalker) pop() {
	if len(w.stack) == 0 {
		return
	}
	w.stack = w.stack[1:]
}

// declare records `name` in the current (innermost) frame. If the name
// exists in any outer frame, emit a shadowing warning. Builtin names are
// never reported as shadowed.
func (w *shadowWalker) declare(name string, pos token.Pos, decl ast.Node) {
	if isUnderscore(name) || name == "self" || name == "Self" {
		return
	}
	if len(w.stack) == 0 {
		w.push()
	}
	// Scan outer frames (skip the current innermost one).
	for i := 1; i < len(w.stack); i++ {
		if priorPos, ok := w.stack[i][name]; ok {
			w.emitShadow(name, pos, priorPos, decl)
			break
		}
	}
	// Even if no outer frame had it, this scope may itself already have
	// a binding (same-block `let x = 1; let x = 2`). The resolver treats
	// this as shadowing via DefineForce, and we mirror that.
	if priorPos, ok := w.stack[0][name]; ok {
		w.emitShadow(name, pos, priorPos, decl)
	}
	w.stack[0][name] = pos
}

// declareTop is like declare but skips shadow detection — top-level lets
// are the outermost user scope; a warning against the prelude builtin
// would be noisy (see lintShadowing doc comment).
func (w *shadowWalker) declareTop(name string, pos token.Pos, decl ast.Node) {
	if isUnderscore(name) || len(w.stack) == 0 {
		return
	}
	w.stack[0][name] = pos
}

func (w *shadowWalker) emitShadow(name string, pos, priorPos token.Pos, decl ast.Node) {
	// If the "prior" is actually a builtin we recorded (shouldn't happen —
	// we never push builtins into the stack), bail.
	if priorPos.Line == 0 {
		return
	}
	b := diag.New(diag.Warning,
		"`"+name+"` shadows an outer binding").
		Code(diag.CodeShadowedBinding).
		Primary(diag.Span{Start: pos, End: pos}, "shadows outer binding of `"+name+"`").
		Secondary(diag.Span{Start: priorPos, End: priorPos}, "outer binding here").
		Hint("rename the inner binding, or use `_" + name + "` if the shadow is intentional")
	w.linter.emit(b.Build())
}

// walkDecl descends into decl bodies where scoped lets can appear.
func (w *shadowWalker) walkDecl(d ast.Decl) {
	switch n := d.(type) {
	case *ast.FnDecl:
		if n.Body != nil {
			w.walkFnBody(n.Params, n.Body)
		}
	case *ast.StructDecl:
		for _, m := range n.Methods {
			if m.Body != nil {
				w.walkFnBody(m.Params, m.Body)
			}
		}
	case *ast.EnumDecl:
		for _, m := range n.Methods {
			if m.Body != nil {
				w.walkFnBody(m.Params, m.Body)
			}
		}
	case *ast.InterfaceDecl:
		for _, m := range n.Methods {
			if m.Body != nil {
				w.walkFnBody(m.Params, m.Body)
			}
		}
	case *ast.LetDecl:
		w.walkExpr(n.Value)
	}
}

// walkFnBody opens a frame for the parameter list (purely so nested lets
// can detect shadowing of params) and then walks the body as one frame.
// We record params with declareTop so that shadowing by an inner let
// doesn't fire (per v1 policy: let-by-let only).
func (w *shadowWalker) walkFnBody(params []*ast.Param, body *ast.Block) {
	w.push()
	defer w.pop()
	// Record params only as "known names at this frame" so future let
	// declarations inside the body don't interpret them as shadowing
	// anything else. We do NOT fire shadow warnings when a param
	// shadows an outer let per v1 policy.
	for _, p := range params {
		if p == nil || p.Name == "" || p.Name == "self" {
			continue
		}
		w.stack[0][p.Name] = p.PosV
	}
	w.walkBlock(body)
}

func (w *shadowWalker) walkBlock(b *ast.Block) {
	if b == nil {
		return
	}
	w.push()
	defer w.pop()
	for _, s := range b.Stmts {
		w.walkStmt(s)
	}
}

func (w *shadowWalker) walkStmt(s ast.Stmt) {
	switch n := s.(type) {
	case *ast.LetStmt:
		// Walk the RHS BEFORE introducing the binding, so that
		// `let x = x + 1` sees the outer `x` rather than the new one.
		w.walkExpr(n.Value)
		w.declarePattern(n.Pattern)
	case *ast.ExprStmt:
		w.walkExpr(n.X)
	case *ast.AssignStmt:
		for _, t := range n.Targets {
			w.walkExpr(t)
		}
		w.walkExpr(n.Value)
	case *ast.ReturnStmt:
		w.walkExpr(n.Value)
	case *ast.DeferStmt:
		w.walkExpr(n.X)
	case *ast.ForStmt:
		w.walkExpr(n.Iter)
		// For-loop variables live in a fresh frame for the body.
		w.push()
		w.declarePattern(n.Pattern)
		if n.Body != nil {
			for _, s := range n.Body.Stmts {
				w.walkStmt(s)
			}
		}
		w.pop()
	case *ast.ChanSendStmt:
		w.walkExpr(n.Channel)
		w.walkExpr(n.Value)
	case *ast.Block:
		w.walkBlock(n)
	}
}

func (w *shadowWalker) walkExpr(e ast.Expr) {
	if e == nil {
		return
	}
	switch n := e.(type) {
	case *ast.Block:
		w.walkBlock(n)
	case *ast.IfExpr:
		w.walkExpr(n.Cond)
		w.walkBlock(n.Then)
		w.walkExpr(n.Else)
	case *ast.MatchExpr:
		w.walkExpr(n.Scrutinee)
		for _, arm := range n.Arms {
			w.push()
			w.declarePattern(arm.Pattern)
			w.walkExpr(arm.Guard)
			w.walkExpr(arm.Body)
			w.pop()
		}
	case *ast.ClosureExpr:
		w.push()
		for _, p := range n.Params {
			if p == nil {
				continue
			}
			if p.Pattern != nil {
				w.declarePattern(p.Pattern)
			} else if p.Name != "" && !isUnderscore(p.Name) {
				// Fire shadow warning if this closure param hides an
				// outer binding. (Top-level fn params go through a
				// different path that silences this by choice — see
				// walkFnBody; closures are nested, so hiding is worth
				// flagging.)
				w.declare(p.Name, p.PosV, p)
			}
		}
		w.walkExpr(n.Body)
		w.pop()
	case *ast.UnaryExpr:
		w.walkExpr(n.X)
	case *ast.BinaryExpr:
		w.walkExpr(n.Left)
		w.walkExpr(n.Right)
	case *ast.CallExpr:
		w.walkExpr(n.Fn)
		for _, a := range n.Args {
			w.walkExpr(a.Value)
		}
	case *ast.FieldExpr:
		w.walkExpr(n.X)
	case *ast.IndexExpr:
		w.walkExpr(n.X)
		w.walkExpr(n.Index)
	case *ast.ParenExpr:
		w.walkExpr(n.X)
	case *ast.TupleExpr:
		for _, x := range n.Elems {
			w.walkExpr(x)
		}
	case *ast.ListExpr:
		for _, x := range n.Elems {
			w.walkExpr(x)
		}
	case *ast.MapExpr:
		for _, me := range n.Entries {
			w.walkExpr(me.Key)
			w.walkExpr(me.Value)
		}
	case *ast.StructLit:
		w.walkExpr(n.Type)
		for _, f := range n.Fields {
			w.walkExpr(f.Value)
		}
		w.walkExpr(n.Spread)
	case *ast.RangeExpr:
		w.walkExpr(n.Start)
		w.walkExpr(n.Stop)
	case *ast.QuestionExpr:
		w.walkExpr(n.X)
	case *ast.TurbofishExpr:
		w.walkExpr(n.Base)
	}
}

// declarePattern walks a pattern and declares each binding site. It fires
// shadow warnings for let-by-let via declare.
func (w *shadowWalker) declarePattern(p ast.Pattern) {
	if p == nil {
		return
	}
	switch n := p.(type) {
	case *ast.IdentPat:
		w.declare(n.Name, n.PosV, n)
	case *ast.BindingPat:
		w.declare(n.Name, n.PosV, n)
		w.declarePattern(n.Pattern)
	case *ast.TuplePat:
		for _, e := range n.Elems {
			w.declarePattern(e)
		}
	case *ast.StructPat:
		for _, f := range n.Fields {
			if f.Pattern != nil {
				w.declarePattern(f.Pattern)
			} else if !isUnderscore(f.Name) {
				w.declare(f.Name, f.PosV, f)
			}
		}
	case *ast.VariantPat:
		for _, a := range n.Args {
			w.declarePattern(a)
		}
	case *ast.OrPat:
		for _, a := range n.Alts {
			w.declarePattern(a)
		}
	}
}
