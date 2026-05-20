package resolve

import "github.com/osty/osty/internal/ast"

// walkIdentAndNamedType is the hand-rolled replacement for the
// reflect-based `walkReflect` used by `buildIdentAndNamedTypeIndex`.
// It visits every `*ast.Ident` and `*ast.NamedType` reachable from a
// file by switching on each Node type — no reflect type assertions, no
// reflect.Value allocations per field. The reflect walker was the
// dominant cost in `resolve.native.bridgeLoop` on toolchain-scale
// install-self builds; the hand-rolled traversal cuts the per-node
// overhead to a single virtual call plus a small switch.
//
// Coverage matches `walkReflect`'s semantics:
//   - Every Ident node fires `onIdent` and is treated as a leaf
//     (matching the original walker's `return` after the Ident match).
//   - Every NamedType node fires `onType` and then recurses into its
//     `Args` so nested generic type arguments still get visited.
//   - All other Node kinds recurse into their child Nodes following
//     the schema in `internal/ast/ast.go`.
//
// Nil checks are explicit at each child slot so missing optional
// subtrees (e.g. `LetStmt.Type`, `IfExpr.Else`) are skipped without
// panicking, mirroring the reflect walker's nil-guard behaviour.
func walkIdentAndNamedType(file *ast.File, onIdent func(*ast.Ident), onType func(*ast.NamedType)) {
	w := identTypeWalker{onIdent: onIdent, onType: onType}
	w.walkFile(file)
}

type identTypeWalker struct {
	onIdent func(*ast.Ident)
	onType  func(*ast.NamedType)
}

func (w *identTypeWalker) walkFile(f *ast.File) {
	if f == nil {
		return
	}
	for _, u := range f.Uses {
		w.walkDecl(u)
	}
	for _, d := range f.Decls {
		w.walkDecl(d)
	}
	for _, s := range f.Stmts {
		w.walkStmt(s)
	}
}

func (w *identTypeWalker) walkDecl(d ast.Decl) {
	switch d := d.(type) {
	case nil:
		return
	case *ast.UseDecl:
		for _, sub := range d.GoBody {
			w.walkDecl(sub)
		}
	case *ast.FnDecl:
		w.walkFnDecl(d)
	case *ast.StructDecl:
		for _, g := range d.Generics {
			w.walkGenericParam(g)
		}
		for _, f := range d.Fields {
			w.walkField(f)
		}
		for _, m := range d.Methods {
			w.walkFnDecl(m)
		}
		for _, a := range d.Annotations {
			w.walkAnnotation(a)
		}
	case *ast.EnumDecl:
		for _, g := range d.Generics {
			w.walkGenericParam(g)
		}
		for _, v := range d.Variants {
			w.walkVariant(v)
		}
		for _, m := range d.Methods {
			w.walkFnDecl(m)
		}
		for _, a := range d.Annotations {
			w.walkAnnotation(a)
		}
	case *ast.InterfaceDecl:
		for _, g := range d.Generics {
			w.walkGenericParam(g)
		}
		for _, t := range d.Extends {
			w.walkType(t)
		}
		for _, m := range d.Methods {
			w.walkFnDecl(m)
		}
		for _, a := range d.Annotations {
			w.walkAnnotation(a)
		}
	case *ast.TypeAliasDecl:
		for _, g := range d.Generics {
			w.walkGenericParam(g)
		}
		w.walkType(d.Target)
		for _, a := range d.Annotations {
			w.walkAnnotation(a)
		}
	case *ast.LetDecl:
		w.walkType(d.Type)
		w.walkExpr(d.Value)
		for _, a := range d.Annotations {
			w.walkAnnotation(a)
		}
	}
}

func (w *identTypeWalker) walkFnDecl(d *ast.FnDecl) {
	if d == nil {
		return
	}
	for _, g := range d.Generics {
		w.walkGenericParam(g)
	}
	for _, p := range d.Params {
		w.walkParam(p)
	}
	w.walkType(d.ReturnType)
	if d.Body != nil {
		w.walkBlock(d.Body)
	}
	for _, a := range d.Annotations {
		w.walkAnnotation(a)
	}
}

func (w *identTypeWalker) walkParam(p *ast.Param) {
	if p == nil {
		return
	}
	w.walkPattern(p.Pattern)
	w.walkType(p.Type)
	w.walkExpr(p.Default)
}

func (w *identTypeWalker) walkGenericParam(g *ast.GenericParam) {
	if g == nil {
		return
	}
	for _, c := range g.Constraints {
		w.walkType(c)
	}
}

func (w *identTypeWalker) walkField(f *ast.Field) {
	if f == nil {
		return
	}
	w.walkType(f.Type)
	w.walkExpr(f.Default)
	for _, a := range f.Annotations {
		w.walkAnnotation(a)
	}
}

func (w *identTypeWalker) walkVariant(v *ast.Variant) {
	if v == nil {
		return
	}
	for _, f := range v.Fields {
		w.walkType(f)
	}
	for _, a := range v.Annotations {
		w.walkAnnotation(a)
	}
}

func (w *identTypeWalker) walkAnnotation(a *ast.Annotation) {
	if a == nil {
		return
	}
	for _, arg := range a.Args {
		w.walkAnnotationArg(arg)
	}
}

func (w *identTypeWalker) walkAnnotationArg(a *ast.AnnotationArg) {
	if a == nil {
		return
	}
	w.walkExpr(a.Value)
	for _, c := range a.Compose {
		w.walkAnnotationArg(c)
	}
}

func (w *identTypeWalker) walkType(t ast.Type) {
	switch t := t.(type) {
	case nil:
		return
	case *ast.NamedType:
		if w.onType != nil && t.ID != 0 {
			w.onType(t)
		}
		for _, arg := range t.Args {
			w.walkType(arg)
		}
	case *ast.OptionalType:
		w.walkType(t.Inner)
	case *ast.TupleType:
		for _, e := range t.Elems {
			w.walkType(e)
		}
	case *ast.FnType:
		for _, p := range t.Params {
			w.walkType(p)
		}
		w.walkType(t.ReturnType)
	}
}

func (w *identTypeWalker) walkBlock(b *ast.Block) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		w.walkStmt(s)
	}
}

func (w *identTypeWalker) walkStmt(s ast.Stmt) {
	switch s := s.(type) {
	case nil:
		return
	case *ast.Block:
		w.walkBlock(s)
	case *ast.LetStmt:
		w.walkPattern(s.Pattern)
		w.walkType(s.Type)
		w.walkExpr(s.Value)
	case *ast.ExprStmt:
		w.walkExpr(s.X)
	case *ast.AssignStmt:
		for _, t := range s.Targets {
			w.walkExpr(t)
		}
		w.walkExpr(s.Value)
	case *ast.ReturnStmt:
		w.walkExpr(s.Value)
	case *ast.BreakStmt:
		w.walkExpr(s.Value)
	case *ast.ContinueStmt:
		// nothing
	case *ast.ChanSendStmt:
		w.walkExpr(s.Channel)
		w.walkExpr(s.Value)
	case *ast.DeferStmt:
		w.walkExpr(s.X)
	case *ast.ForStmt:
		w.walkPattern(s.Pattern)
		w.walkExpr(s.Iter)
		if s.Body != nil {
			w.walkBlock(s.Body)
		}
	}
}

func (w *identTypeWalker) walkExpr(e ast.Expr) {
	switch e := e.(type) {
	case nil:
		return
	case *ast.Ident:
		if w.onIdent != nil && e.ID != 0 {
			w.onIdent(e)
		}
	case *ast.IntLit, *ast.FloatLit, *ast.CharLit, *ast.ByteLit, *ast.BytesLit, *ast.BoolLit:
		// leaves
	case *ast.StringLit:
		for i := range e.Parts {
			if !e.Parts[i].IsLit {
				w.walkExpr(e.Parts[i].Expr)
			}
		}
	case *ast.UnaryExpr:
		w.walkExpr(e.X)
	case *ast.BinaryExpr:
		w.walkExpr(e.Left)
		w.walkExpr(e.Right)
	case *ast.QuestionExpr:
		w.walkExpr(e.X)
	case *ast.CallExpr:
		w.walkExpr(e.Fn)
		for _, a := range e.Args {
			if a != nil {
				w.walkExpr(a.Value)
			}
		}
	case *ast.FieldExpr:
		w.walkExpr(e.X)
	case *ast.IndexExpr:
		w.walkExpr(e.X)
		w.walkExpr(e.Index)
	case *ast.TurbofishExpr:
		w.walkExpr(e.Base)
		for _, a := range e.Args {
			w.walkType(a)
		}
	case *ast.RangeExpr:
		w.walkExpr(e.Start)
		w.walkExpr(e.Stop)
		w.walkExpr(e.Step)
	case *ast.ParenExpr:
		w.walkExpr(e.X)
	case *ast.TupleExpr:
		for _, el := range e.Elems {
			w.walkExpr(el)
		}
	case *ast.ListExpr:
		for _, el := range e.Elems {
			w.walkExpr(el)
		}
	case *ast.MapExpr:
		for _, en := range e.Entries {
			if en != nil {
				w.walkExpr(en.Key)
				w.walkExpr(en.Value)
			}
		}
	case *ast.StructLit:
		w.walkExpr(e.Type)
		for _, f := range e.Fields {
			if f != nil {
				w.walkExpr(f.Value)
			}
		}
		w.walkExpr(e.Spread)
	case *ast.Block:
		w.walkBlock(e)
	case *ast.IfExpr:
		w.walkPattern(e.Pattern)
		w.walkExpr(e.Cond)
		if e.Then != nil {
			w.walkBlock(e.Then)
		}
		w.walkExpr(e.Else)
	case *ast.LoopExpr:
		if e.Body != nil {
			w.walkBlock(e.Body)
		}
	case *ast.MatchExpr:
		w.walkExpr(e.Scrutinee)
		for _, arm := range e.Arms {
			if arm == nil {
				continue
			}
			w.walkPattern(arm.Pattern)
			w.walkExpr(arm.Guard)
			w.walkExpr(arm.Body)
		}
	case *ast.ClosureExpr:
		for _, p := range e.Params {
			w.walkParam(p)
		}
		w.walkType(e.ReturnType)
		w.walkExpr(e.Body)
	}
}

func (w *identTypeWalker) walkPattern(p ast.Pattern) {
	switch p := p.(type) {
	case nil:
		return
	case *ast.WildcardPat, *ast.IdentPat:
		// leaves
	case *ast.LiteralPat:
		w.walkExpr(p.Literal)
	case *ast.TuplePat:
		for _, el := range p.Elems {
			w.walkPattern(el)
		}
	case *ast.StructPat:
		for _, f := range p.Fields {
			if f != nil {
				w.walkPattern(f.Pattern)
			}
		}
	case *ast.VariantPat:
		for _, a := range p.Args {
			w.walkPattern(a)
		}
	case *ast.RangePat:
		w.walkExpr(p.Start)
		w.walkExpr(p.Stop)
	case *ast.OrPat:
		for _, alt := range p.Alts {
			w.walkPattern(alt)
		}
	case *ast.BindingPat:
		w.walkPattern(p.Pattern)
	}
}
