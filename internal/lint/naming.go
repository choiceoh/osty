package lint

import (
	"unicode"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/token"
)

// lintNaming flags identifiers that deviate from the project's house
// style (per v0.4 spec examples):
//
//	L0030  types (struct / enum / interface / type alias / generic) must
//	       be UpperCamelCase
//	L0031  functions, methods, let bindings, and parameters must be
//	       lowerCamelCase
//	L0032  enum variants must be UpperCamelCase
//
// FFI bindings (declarations inside `use go "…" { … }`) are exempt: their
// names mirror the host language's export convention, which is typically
// UpperCamelCase even for functions. We therefore skip descent into
// UseDecl.GoBody.
func (l *linter) lintNaming() {
	for _, d := range l.file.Decls {
		l.namingDecl(d)
	}
	// Skip file.Uses intentionally — `use` aliases follow the alias the
	// user chose (or the last path segment), and flagging them is noisy.
}

func (l *linter) namingDecl(d ast.Decl) {
	switch n := d.(type) {
	case *ast.FnDecl:
		l.checkValueName(n.Name, n.PosV, n)
		for _, p := range n.Params {
			l.checkParamName(p)
		}
		for _, g := range n.Generics {
			l.checkGenericName(g)
		}
		if n.Body != nil {
			l.namingBlock(n.Body)
		}
	case *ast.StructDecl:
		l.checkTypeName(n.Name, n.PosV, n)
		for _, g := range n.Generics {
			l.checkGenericName(g)
		}
		for _, m := range n.Methods {
			l.namingDecl(m)
		}
	case *ast.EnumDecl:
		l.checkTypeName(n.Name, n.PosV, n)
		for _, g := range n.Generics {
			l.checkGenericName(g)
		}
		for _, v := range n.Variants {
			l.checkVariantName(v)
		}
		for _, m := range n.Methods {
			l.namingDecl(m)
		}
	case *ast.InterfaceDecl:
		l.checkTypeName(n.Name, n.PosV, n)
		for _, g := range n.Generics {
			l.checkGenericName(g)
		}
		for _, m := range n.Methods {
			l.namingDecl(m)
		}
	case *ast.TypeAliasDecl:
		l.checkTypeName(n.Name, n.PosV, n)
		for _, g := range n.Generics {
			l.checkGenericName(g)
		}
	case *ast.LetDecl:
		l.checkValueName(n.Name, n.PosV, n)
		l.namingExpr(n.Value)
	}
}

func (l *linter) namingBlock(b *ast.Block) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		l.namingStmt(s)
	}
}

func (l *linter) namingStmt(s ast.Stmt) {
	switch n := s.(type) {
	case *ast.LetStmt:
		l.checkPatternNaming(n.Pattern)
		l.namingExpr(n.Value)
	case *ast.ExprStmt:
		l.namingExpr(n.X)
	case *ast.AssignStmt:
		for _, t := range n.Targets {
			l.namingExpr(t)
		}
		l.namingExpr(n.Value)
	case *ast.ReturnStmt:
		l.namingExpr(n.Value)
	case *ast.DeferStmt:
		l.namingExpr(n.X)
	case *ast.ForStmt:
		l.checkPatternNaming(n.Pattern)
		l.namingExpr(n.Iter)
		l.namingBlock(n.Body)
	case *ast.ChanSendStmt:
		l.namingExpr(n.Channel)
		l.namingExpr(n.Value)
	case *ast.Block:
		l.namingBlock(n)
	}
}

func (l *linter) namingExpr(e ast.Expr) {
	if e == nil {
		return
	}
	switch n := e.(type) {
	case *ast.Block:
		l.namingBlock(n)
	case *ast.ClosureExpr:
		for _, p := range n.Params {
			l.checkParamName(p)
		}
		l.namingExpr(n.Body)
	case *ast.IfExpr:
		l.namingExpr(n.Cond)
		l.namingBlock(n.Then)
		l.namingExpr(n.Else)
	case *ast.MatchExpr:
		l.namingExpr(n.Scrutinee)
		for _, arm := range n.Arms {
			l.checkPatternNaming(arm.Pattern)
			l.namingExpr(arm.Guard)
			l.namingExpr(arm.Body)
		}
	case *ast.UnaryExpr:
		l.namingExpr(n.X)
	case *ast.BinaryExpr:
		l.namingExpr(n.Left)
		l.namingExpr(n.Right)
	case *ast.CallExpr:
		l.namingExpr(n.Fn)
		for _, a := range n.Args {
			l.namingExpr(a.Value)
		}
	case *ast.FieldExpr:
		l.namingExpr(n.X)
	case *ast.IndexExpr:
		l.namingExpr(n.X)
		l.namingExpr(n.Index)
	case *ast.ParenExpr:
		l.namingExpr(n.X)
	case *ast.TupleExpr:
		for _, x := range n.Elems {
			l.namingExpr(x)
		}
	case *ast.ListExpr:
		for _, x := range n.Elems {
			l.namingExpr(x)
		}
	case *ast.MapExpr:
		for _, me := range n.Entries {
			l.namingExpr(me.Key)
			l.namingExpr(me.Value)
		}
	case *ast.StructLit:
		l.namingExpr(n.Type)
		for _, f := range n.Fields {
			l.namingExpr(f.Value)
		}
		l.namingExpr(n.Spread)
	case *ast.RangeExpr:
		l.namingExpr(n.Start)
		l.namingExpr(n.Stop)
	case *ast.QuestionExpr:
		l.namingExpr(n.X)
	case *ast.TurbofishExpr:
		l.namingExpr(n.Base)
	}
}

// ---- per-binding checks ----

func (l *linter) checkTypeName(name string, _ token.Pos, node ast.Node) {
	if skipNamingCheck(name) {
		return
	}
	if !isUpperCamel(name) {
		l.warnNode(node, diag.CodeNamingType,
			"type name `%s` should be UpperCamelCase", name)
	}
}

func (l *linter) checkValueName(name string, _ token.Pos, node ast.Node) {
	if skipNamingCheck(name) {
		return
	}
	if !isLowerCamel(name) {
		l.warnNode(node, diag.CodeNamingValue,
			"name `%s` should be lowerCamelCase", name)
	}
}

func (l *linter) checkVariantName(v *ast.Variant) {
	if v == nil || skipNamingCheck(v.Name) {
		return
	}
	if !isUpperCamel(v.Name) {
		l.warnNode(v, diag.CodeNamingVariant,
			"variant `%s` should be UpperCamelCase", v.Name)
	}
}

func (l *linter) checkGenericName(g *ast.GenericParam) {
	if g == nil || skipNamingCheck(g.Name) {
		return
	}
	if !isUpperCamel(g.Name) {
		l.warnNode(g, diag.CodeNamingType,
			"type parameter `%s` should be UpperCamelCase", g.Name)
	}
}

func (l *linter) checkParamName(p *ast.Param) {
	if p == nil {
		return
	}
	if p.Pattern != nil {
		l.checkPatternNaming(p.Pattern)
		return
	}
	if p.Name == "" || p.Name == "self" || skipNamingCheck(p.Name) {
		return
	}
	if !isLowerCamel(p.Name) {
		l.warnNode(p, diag.CodeNamingValue,
			"parameter `%s` should be lowerCamelCase", p.Name)
	}
}

func (l *linter) checkPatternNaming(p ast.Pattern) {
	if p == nil {
		return
	}
	switch n := p.(type) {
	case *ast.IdentPat:
		if !skipNamingCheck(n.Name) && !isLowerCamel(n.Name) {
			l.warnNode(n, diag.CodeNamingValue,
				"binding `%s` should be lowerCamelCase", n.Name)
		}
	case *ast.BindingPat:
		if !skipNamingCheck(n.Name) && !isLowerCamel(n.Name) {
			l.warnNode(n, diag.CodeNamingValue,
				"binding `%s` should be lowerCamelCase", n.Name)
		}
		l.checkPatternNaming(n.Pattern)
	case *ast.TuplePat:
		for _, e := range n.Elems {
			l.checkPatternNaming(e)
		}
	case *ast.StructPat:
		for _, f := range n.Fields {
			if f.Pattern != nil {
				l.checkPatternNaming(f.Pattern)
			}
			// Shorthand `Point { x, y }` field names follow the field
			// names declared on the struct, which already get flagged at
			// the declaration site — don't double-report here.
		}
	case *ast.VariantPat:
		for _, a := range n.Args {
			l.checkPatternNaming(a)
		}
	case *ast.OrPat:
		for _, a := range n.Alts {
			l.checkPatternNaming(a)
		}
	}
}

// skipNamingCheck returns true for names that should never be flagged:
// the underscore prefix, `self`, `Self`, and single underscores.
func skipNamingCheck(name string) bool {
	if name == "" {
		return true
	}
	if name == "self" || name == "Self" {
		return true
	}
	if isUnderscore(name) {
		return true
	}
	return false
}

// isUpperCamel returns true if `s` looks like `Foo`, `FooBar`, `Http2Client`,
// or similar — first rune upper, no `_` inside, at least one letter.
func isUpperCamel(s string) bool {
	if s == "" {
		return false
	}
	r0 := []rune(s)[0]
	if !unicode.IsUpper(r0) {
		return false
	}
	for _, r := range s {
		if r == '_' {
			return false
		}
	}
	return true
}

// isLowerCamel returns true if `s` looks like `foo`, `fooBar`, `loadHttp2`,
// or similar — first rune lower, no `_` inside, at least one letter.
func isLowerCamel(s string) bool {
	if s == "" {
		return false
	}
	r0 := []rune(s)[0]
	if !unicode.IsLower(r0) {
		return false
	}
	for _, r := range s {
		if r == '_' {
			return false
		}
	}
	return true
}
