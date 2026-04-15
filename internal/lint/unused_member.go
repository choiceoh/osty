package lint

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
)

// lintUnusedMember flags struct fields (L0005) and private methods
// (L0006) whose NAMES never appear as a member access anywhere in the
// current lint scope.
//
// This is a name-level AST heuristic: it does NOT attempt to reason
// about which type a `.name` access actually targets. Consequences:
//
//   - When two structs share a field name and one is used, the
//     heuristic treats BOTH as used. False negative: acceptable.
//   - When a field is only accessed via reflection / FFI, the
//     heuristic reports it as unused. False positive: acceptable for a
//     `pub` field nothing references; public types are exempted anyway.
//
// Methods are matched only when called — `f(x)` where `f` is a
// `FieldExpr` (i.e. `x.m()`). Function-valued method references
// (`let f = x.m`) are also counted as "used" because we descend into
// every FieldExpr, not just CallExpr targets.
//
// Exemptions:
//
//   - `_`-prefixed names
//   - `pub struct` fields: always considered used (external consumers)
//   - `pub fn` methods: always considered used
//   - interface methods (no body to lint anyway)
func (l *linter) lintUnusedMember() {
	if l.members == nil {
		l.members = &memberAccess{
			fields:  map[string]bool{},
			methods: map[string]bool{},
		}
		collectMemberAccess(l.file, l.members)
	}

	for _, d := range l.file.Decls {
		switch n := d.(type) {
		case *ast.StructDecl:
			isPub := n.Pub
			for _, f := range n.Fields {
				if f == nil || isUnderscore(f.Name) {
					continue
				}
				if isPub || f.Pub {
					continue
				}
				if l.members.fields[f.Name] {
					continue
				}
				l.warnNode(f, diag.CodeUnusedField,
					"field `%s` is never read", f.Name)
			}
			for _, m := range n.Methods {
				l.checkUnusedMethod(m, isPub)
			}
		case *ast.EnumDecl:
			for _, m := range n.Methods {
				l.checkUnusedMethod(m, n.Pub)
			}
		}
	}
}

// checkUnusedMethod flags a non-public, non-underscore method whose name
// never appears as a `.name(...)` call anywhere in scope.
func (l *linter) checkUnusedMethod(m *ast.FnDecl, enclosingPub bool) {
	if m == nil || m.Name == "" || isUnderscore(m.Name) {
		return
	}
	// A method reachable externally (pub type OR pub method) is assumed
	// to have outside callers.
	if enclosingPub || m.Pub {
		return
	}
	if l.members.methods[m.Name] {
		return
	}
	l.warnNode(m, diag.CodeUnusedMethod,
		"method `%s` is never called", m.Name)
}

// buildMemberAccessSets populates the linter's members field from the
// current file. In Package mode the field is pre-populated with the
// package-wide union before any file is linted.
func (l *linter) buildMemberAccessSets() {
	if l.members != nil {
		return
	}
	l.members = &memberAccess{
		fields:  map[string]bool{},
		methods: map[string]bool{},
	}
	collectMemberAccess(l.file, l.members)
}

// collectMemberAccess walks an AST file and records every name that
// appears as a field / method reference.
func collectMemberAccess(f *ast.File, out *memberAccess) {
	if f == nil {
		return
	}
	for _, d := range f.Decls {
		walkDeclForMembers(d, out)
	}
	for _, s := range f.Stmts {
		walkStmtForMembers(s, out)
	}
}

func walkDeclForMembers(d ast.Decl, out *memberAccess) {
	switch n := d.(type) {
	case *ast.FnDecl:
		walkBlockForMembers(n.Body, out)
	case *ast.StructDecl:
		for _, m := range n.Methods {
			walkBlockForMembers(m.Body, out)
		}
	case *ast.EnumDecl:
		for _, m := range n.Methods {
			walkBlockForMembers(m.Body, out)
		}
	case *ast.InterfaceDecl:
		for _, m := range n.Methods {
			walkBlockForMembers(m.Body, out)
		}
	case *ast.LetDecl:
		walkExprForMembers(n.Value, out)
	}
}

func walkBlockForMembers(b *ast.Block, out *memberAccess) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		walkStmtForMembers(s, out)
	}
}

func walkStmtForMembers(s ast.Stmt, out *memberAccess) {
	switch n := s.(type) {
	case *ast.LetStmt:
		walkPatternForMembers(n.Pattern, out)
		walkExprForMembers(n.Value, out)
	case *ast.ExprStmt:
		walkExprForMembers(n.X, out)
	case *ast.AssignStmt:
		for _, t := range n.Targets {
			walkExprForMembers(t, out)
		}
		walkExprForMembers(n.Value, out)
	case *ast.ReturnStmt:
		walkExprForMembers(n.Value, out)
	case *ast.DeferStmt:
		walkExprForMembers(n.X, out)
	case *ast.ForStmt:
		walkPatternForMembers(n.Pattern, out)
		walkExprForMembers(n.Iter, out)
		walkBlockForMembers(n.Body, out)
	case *ast.ChanSendStmt:
		walkExprForMembers(n.Channel, out)
		walkExprForMembers(n.Value, out)
	case *ast.Block:
		walkBlockForMembers(n, out)
	}
}

func walkExprForMembers(e ast.Expr, out *memberAccess) {
	if e == nil {
		return
	}
	switch n := e.(type) {
	case *ast.Block:
		walkBlockForMembers(n, out)
	case *ast.IfExpr:
		walkExprForMembers(n.Cond, out)
		walkBlockForMembers(n.Then, out)
		walkExprForMembers(n.Else, out)
	case *ast.MatchExpr:
		walkExprForMembers(n.Scrutinee, out)
		for _, arm := range n.Arms {
			walkPatternForMembers(arm.Pattern, out)
			walkExprForMembers(arm.Guard, out)
			walkExprForMembers(arm.Body, out)
		}
	case *ast.ClosureExpr:
		walkExprForMembers(n.Body, out)
	case *ast.UnaryExpr:
		walkExprForMembers(n.X, out)
	case *ast.BinaryExpr:
		walkExprForMembers(n.Left, out)
		walkExprForMembers(n.Right, out)
	case *ast.CallExpr:
		// Classify `x.m(args)` as a method call on `m`; plain `f(args)`
		// is a free function call.
		if fe, ok := n.Fn.(*ast.FieldExpr); ok {
			out.methods[fe.Name] = true
			// Still descend to the receiver for nested cases.
			walkExprForMembers(fe.X, out)
		} else {
			walkExprForMembers(n.Fn, out)
		}
		for _, a := range n.Args {
			walkExprForMembers(a.Value, out)
		}
	case *ast.FieldExpr:
		// Plain field read `x.name` — record as field access even if the
		// outer context turns out to be a call. (The CallExpr branch
		// above already handled the method case before recursing.)
		out.fields[n.Name] = true
		walkExprForMembers(n.X, out)
	case *ast.IndexExpr:
		walkExprForMembers(n.X, out)
		walkExprForMembers(n.Index, out)
	case *ast.ParenExpr:
		walkExprForMembers(n.X, out)
	case *ast.TupleExpr:
		for _, x := range n.Elems {
			walkExprForMembers(x, out)
		}
	case *ast.ListExpr:
		for _, x := range n.Elems {
			walkExprForMembers(x, out)
		}
	case *ast.MapExpr:
		for _, me := range n.Entries {
			walkExprForMembers(me.Key, out)
			walkExprForMembers(me.Value, out)
		}
	case *ast.StructLit:
		// Struct literal fields are named — count those names as field
		// references (so `User { name: …, age: … }` satisfies L0005 for
		// `name` and `age`).
		walkExprForMembers(n.Type, out)
		for _, f := range n.Fields {
			out.fields[f.Name] = true
			walkExprForMembers(f.Value, out)
		}
		walkExprForMembers(n.Spread, out)
	case *ast.RangeExpr:
		walkExprForMembers(n.Start, out)
		walkExprForMembers(n.Stop, out)
	case *ast.QuestionExpr:
		walkExprForMembers(n.X, out)
	case *ast.TurbofishExpr:
		walkExprForMembers(n.Base, out)
	}
}

// walkPatternForMembers records struct-pattern field names as field
// accesses — `let User { name, age } = u` reads both fields.
func walkPatternForMembers(p ast.Pattern, out *memberAccess) {
	if p == nil {
		return
	}
	switch n := p.(type) {
	case *ast.BindingPat:
		walkPatternForMembers(n.Pattern, out)
	case *ast.TuplePat:
		for _, e := range n.Elems {
			walkPatternForMembers(e, out)
		}
	case *ast.StructPat:
		for _, f := range n.Fields {
			out.fields[f.Name] = true
			walkPatternForMembers(f.Pattern, out)
		}
	case *ast.VariantPat:
		for _, a := range n.Args {
			walkPatternForMembers(a, out)
		}
	case *ast.OrPat:
		for _, a := range n.Alts {
			walkPatternForMembers(a, out)
		}
	}
}
