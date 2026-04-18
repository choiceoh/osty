package parser

import (
	"fmt"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/token"
)

type stableLowerer struct {
	counter    int
	provenance []ProvenanceStep
}

func lowerStableAST(file *ast.File) []ProvenanceStep {
	if file == nil {
		return nil
	}
	l := &stableLowerer{}
	for _, decl := range file.Decls {
		l.lowerDecl(decl)
	}
	file.Stmts = l.lowerStmtList(file.Stmts)
	return l.provenance
}

func (l *stableLowerer) emit(kind, habit, detail string, start, end token.Pos) {
	l.provenance = append(l.provenance, ProvenanceStep{
		Kind:        kind,
		SourceHabit: habit,
		Span:        diag.Span{Start: start, End: end},
		Detail:      detail,
	})
}

func (l *stableLowerer) nextTemp(prefix string) string {
	name := fmt.Sprintf("_osty_%s%d", prefix, l.counter)
	l.counter++
	return name
}

func (l *stableLowerer) lowerDecl(decl ast.Decl) {
	switch n := decl.(type) {
	case *ast.FnDecl:
		n.Body = l.lowerBlock(n.Body)
	case *ast.StructDecl:
		for _, m := range n.Methods {
			if m != nil {
				m.Body = l.lowerBlock(m.Body)
			}
		}
	case *ast.EnumDecl:
		for _, m := range n.Methods {
			if m != nil {
				m.Body = l.lowerBlock(m.Body)
			}
		}
	case *ast.InterfaceDecl:
		for _, m := range n.Methods {
			if m != nil {
				m.Body = l.lowerBlock(m.Body)
			}
		}
	case *ast.LetDecl:
		n.Value = l.lowerExpr(n.Value)
	}
}

func (l *stableLowerer) lowerBlock(block *ast.Block) *ast.Block {
	if block == nil {
		return nil
	}
	block.Stmts = l.lowerStmtList(block.Stmts)
	return block
}

func (l *stableLowerer) lowerStmtList(stmts []ast.Stmt) []ast.Stmt {
	if len(stmts) == 0 {
		return stmts
	}
	out := make([]ast.Stmt, 0, len(stmts))
	for _, stmt := range stmts {
		out = append(out, l.lowerStmt(stmt)...)
	}
	return out
}

func (l *stableLowerer) lowerStmt(stmt ast.Stmt) []ast.Stmt {
	switch n := stmt.(type) {
	case *ast.Block:
		return []ast.Stmt{l.lowerBlock(n)}
	case *ast.LetStmt:
		n.Pattern = l.lowerPattern(n.Pattern)
		n.Type = l.lowerType(n.Type)
		n.Value = l.lowerExpr(n.Value)
		if lowered, ok := l.lowerAppendLetStmt(n); ok {
			return lowered
		}
		return []ast.Stmt{n}
	case *ast.ExprStmt:
		n.X = l.lowerExpr(n.X)
		if lowered, ok := l.lowerAppendExprStmt(n); ok {
			return []ast.Stmt{lowered}
		}
		return []ast.Stmt{n}
	case *ast.AssignStmt:
		for i := range n.Targets {
			n.Targets[i] = l.lowerExpr(n.Targets[i])
		}
		n.Value = l.lowerExpr(n.Value)
		if lowered, ok := l.lowerAppendAssignStmt(n); ok {
			return []ast.Stmt{lowered}
		}
		return []ast.Stmt{n}
	case *ast.ReturnStmt:
		n.Value = l.lowerExpr(n.Value)
		return []ast.Stmt{n}
	case *ast.DeferStmt:
		n.X = l.lowerExpr(n.X)
		return []ast.Stmt{n}
	case *ast.ForStmt:
		n.Pattern = l.lowerPattern(n.Pattern)
		n.Iter = l.lowerExpr(n.Iter)
		n.Body = l.lowerBlock(n.Body)
		if lowered, ok := l.lowerEnumerateForStmt(n); ok {
			return lowered
		}
		return []ast.Stmt{n}
	case *ast.ChanSendStmt:
		n.Channel = l.lowerExpr(n.Channel)
		n.Value = l.lowerExpr(n.Value)
		return []ast.Stmt{n}
	default:
		return []ast.Stmt{stmt}
	}
}

func (l *stableLowerer) lowerExpr(expr ast.Expr) ast.Expr {
	switch n := expr.(type) {
	case nil:
		return nil
	case *ast.Block:
		return l.lowerBlock(n)
	case *ast.UnaryExpr:
		n.X = l.lowerExpr(n.X)
		return n
	case *ast.BinaryExpr:
		n.Left = l.lowerExpr(n.Left)
		n.Right = l.lowerExpr(n.Right)
		return n
	case *ast.QuestionExpr:
		n.X = l.lowerExpr(n.X)
		return n
	case *ast.CallExpr:
		n.Fn = l.lowerExpr(n.Fn)
		for i := range n.Args {
			if n.Args[i] != nil {
				n.Args[i].Value = l.lowerExpr(n.Args[i].Value)
			}
		}
		if lowered, ok := l.lowerBuiltinLenCall(n); ok {
			return lowered
		}
		return n
	case *ast.FieldExpr:
		n.X = l.lowerExpr(n.X)
		return n
	case *ast.IndexExpr:
		n.X = l.lowerExpr(n.X)
		n.Index = l.lowerExpr(n.Index)
		return n
	case *ast.TurbofishExpr:
		n.Base = l.lowerExpr(n.Base)
		for i := range n.Args {
			n.Args[i] = l.lowerType(n.Args[i])
		}
		return n
	case *ast.RangeExpr:
		n.Start = l.lowerExpr(n.Start)
		n.Stop = l.lowerExpr(n.Stop)
		return n
	case *ast.ParenExpr:
		n.X = l.lowerExpr(n.X)
		return n
	case *ast.TupleExpr:
		for i := range n.Elems {
			n.Elems[i] = l.lowerExpr(n.Elems[i])
		}
		return n
	case *ast.ListExpr:
		for i := range n.Elems {
			n.Elems[i] = l.lowerExpr(n.Elems[i])
		}
		return n
	case *ast.MapExpr:
		for _, entry := range n.Entries {
			if entry == nil {
				continue
			}
			entry.Key = l.lowerExpr(entry.Key)
			entry.Value = l.lowerExpr(entry.Value)
		}
		return n
	case *ast.StructLit:
		n.Type = l.lowerExpr(n.Type)
		for _, field := range n.Fields {
			if field != nil {
				field.Value = l.lowerExpr(field.Value)
			}
		}
		n.Spread = l.lowerExpr(n.Spread)
		return n
	case *ast.IfExpr:
		n.Pattern = l.lowerPattern(n.Pattern)
		n.Cond = l.lowerExpr(n.Cond)
		n.Then = l.lowerBlock(n.Then)
		n.Else = l.lowerExpr(n.Else)
		return n
	case *ast.MatchExpr:
		n.Scrutinee = l.lowerExpr(n.Scrutinee)
		for _, arm := range n.Arms {
			if arm == nil {
				continue
			}
			arm.Pattern = l.lowerPattern(arm.Pattern)
			arm.Guard = l.lowerExpr(arm.Guard)
			arm.Body = l.lowerExpr(arm.Body)
		}
		return n
	case *ast.ClosureExpr:
		for _, p := range n.Params {
			if p == nil {
				continue
			}
			p.Pattern = l.lowerPattern(p.Pattern)
			p.Type = l.lowerType(p.Type)
			p.Default = l.lowerExpr(p.Default)
		}
		n.ReturnType = l.lowerType(n.ReturnType)
		n.Body = l.lowerExpr(n.Body)
		return n
	default:
		return n
	}
}

func (l *stableLowerer) lowerType(ty ast.Type) ast.Type {
	switch n := ty.(type) {
	case nil:
		return nil
	case *ast.OptionalType:
		n.Inner = l.lowerType(n.Inner)
		return n
	case *ast.TupleType:
		for i := range n.Elems {
			n.Elems[i] = l.lowerType(n.Elems[i])
		}
		return n
	case *ast.FnType:
		for i := range n.Params {
			n.Params[i] = l.lowerType(n.Params[i])
		}
		n.ReturnType = l.lowerType(n.ReturnType)
		return n
	case *ast.NamedType:
		for i := range n.Args {
			n.Args[i] = l.lowerType(n.Args[i])
		}
		return n
	default:
		return n
	}
}

func (l *stableLowerer) lowerPattern(pat ast.Pattern) ast.Pattern {
	switch n := pat.(type) {
	case nil:
		return nil
	case *ast.LiteralPat:
		n.Literal = l.lowerExpr(n.Literal)
		return n
	case *ast.TuplePat:
		for i := range n.Elems {
			n.Elems[i] = l.lowerPattern(n.Elems[i])
		}
		return n
	case *ast.StructPat:
		for _, field := range n.Fields {
			if field != nil {
				field.Pattern = l.lowerPattern(field.Pattern)
			}
		}
		return n
	case *ast.VariantPat:
		for i := range n.Args {
			n.Args[i] = l.lowerPattern(n.Args[i])
		}
		return n
	case *ast.RangePat:
		n.Start = l.lowerExpr(n.Start)
		n.Stop = l.lowerExpr(n.Stop)
		return n
	case *ast.OrPat:
		for i := range n.Alts {
			n.Alts[i] = l.lowerPattern(n.Alts[i])
		}
		return n
	case *ast.BindingPat:
		n.Pattern = l.lowerPattern(n.Pattern)
		return n
	default:
		return n
	}
}

func (l *stableLowerer) lowerBuiltinLenCall(call *ast.CallExpr) (ast.Expr, bool) {
	if call == nil || len(call.Args) != 1 {
		return nil, false
	}
	name, ok := call.Fn.(*ast.Ident)
	if !ok || name.Name != "len" {
		return nil, false
	}
	target := call.Args[0].Value
	if target == nil {
		return nil, false
	}
	l.emit("builtin_len_call", "foreign_len_helper", "lower `len(...)` into `.len()`", call.Pos(), call.End())
	return &ast.CallExpr{
		PosV: call.PosV,
		EndV: call.EndV,
		Fn: &ast.FieldExpr{
			PosV: call.PosV,
			EndV: call.EndV,
			X:    target,
			Name: "len",
		},
	}, true
}

func (l *stableLowerer) lowerAppendAssignStmt(stmt *ast.AssignStmt) (ast.Stmt, bool) {
	if stmt == nil || len(stmt.Targets) != 1 {
		return nil, false
	}
	targetIdent, ok := stmt.Targets[0].(*ast.Ident)
	if !ok {
		return nil, false
	}
	base, item, ok := appendCallParts(stmt.Value)
	if !ok {
		return nil, false
	}
	baseIdent, ok := base.(*ast.Ident)
	if !ok || baseIdent.Name != targetIdent.Name {
		return nil, false
	}
	l.emit("builtin_append_call", "foreign_append_helper", "lower `append(x, y)` assignment into `x.push(y)`", stmt.Pos(), stmt.End())
	return pushExprStmt(stmt.Pos(), stmt.End(), targetIdent, item), true
}

func (l *stableLowerer) lowerAppendExprStmt(stmt *ast.ExprStmt) (ast.Stmt, bool) {
	base, item, ok := appendCallParts(stmt.X)
	if !ok {
		return nil, false
	}
	baseIdent, ok := base.(*ast.Ident)
	if !ok {
		return nil, false
	}
	l.emit("builtin_append_call", "foreign_append_helper", "lower statement-form `append(x, y)` into `x.push(y)`", stmt.Pos(), stmt.End())
	return pushExprStmt(stmt.Pos(), stmt.End(), baseIdent, item), true
}

func (l *stableLowerer) lowerAppendLetStmt(stmt *ast.LetStmt) ([]ast.Stmt, bool) {
	name, ok := simpleIdentPatternName(stmt.Pattern)
	if !ok {
		return nil, false
	}
	base, item, ok := appendCallParts(stmt.Value)
	if !ok {
		return nil, false
	}
	baseIdent, ok := base.(*ast.Ident)
	if !ok || baseIdent.Name == name {
		return nil, false
	}
	binding := &ast.LetStmt{
		PosV:    stmt.PosV,
		EndV:    stmt.EndV,
		Pattern: &ast.IdentPat{PosV: stmt.PosV, EndV: stmt.EndV, Name: name},
		Mut:     true,
		MutPos:  stmt.MutPos,
		Type:    stmt.Type,
		Value:   base,
	}
	push := pushExprStmt(stmt.Pos(), stmt.End(), &ast.Ident{PosV: stmt.PosV, EndV: stmt.EndV, Name: name}, item)
	l.emit("builtin_append_call", "foreign_append_helper", "lower `let x = append(y, z)` into `let mut x = y` plus `x.push(z)`", stmt.Pos(), stmt.End())
	return []ast.Stmt{binding, push}, true
}

func (l *stableLowerer) lowerEnumerateForStmt(stmt *ast.ForStmt) ([]ast.Stmt, bool) {
	if stmt == nil || stmt.IsForLet {
		return nil, false
	}
	tuple, ok := stmt.Pattern.(*ast.TuplePat)
	if !ok || len(tuple.Elems) != 2 {
		return nil, false
	}
	iterExpr, ok := enumerateIterable(stmt.Iter)
	if !ok || iterExpr == nil {
		return nil, false
	}
	indexPattern, valuePattern := tuple.Elems[0], tuple.Elems[1]
	loopPattern, indexExpr, ok := l.enumerateIndexPattern(indexPattern, stmt.Pos(), stmt.End())
	if !ok {
		return nil, false
	}

	tempName := l.nextTemp("enumerate")
	tempBinding := &ast.LetStmt{
		PosV:    stmt.PosV,
		EndV:    stmt.EndV,
		Pattern: &ast.IdentPat{PosV: stmt.PosV, EndV: stmt.EndV, Name: tempName},
		Value:   iterExpr,
	}

	prelude := &ast.LetStmt{
		PosV:    stmt.PosV,
		EndV:    stmt.EndV,
		Pattern: valuePattern,
		Value: &ast.IndexExpr{
			PosV:  stmt.PosV,
			EndV:  stmt.EndV,
			X:     &ast.Ident{PosV: stmt.PosV, EndV: stmt.EndV, Name: tempName},
			Index: indexExpr,
		},
	}
	body := stmt.Body
	if body == nil {
		body = &ast.Block{PosV: stmt.PosV, EndV: stmt.EndV}
	}
	body.Stmts = append([]ast.Stmt{prelude}, body.Stmts...)

	lowered := &ast.ForStmt{
		PosV:    stmt.PosV,
		EndV:    stmt.EndV,
		Pattern: loopPattern,
		Iter: &ast.RangeExpr{
			PosV:  stmt.PosV,
			EndV:  stmt.EndV,
			Start: &ast.IntLit{PosV: stmt.PosV, EndV: stmt.PosV, Text: "0"},
			Stop: &ast.CallExpr{
				PosV: stmt.PosV,
				EndV: stmt.EndV,
				Fn: &ast.FieldExpr{
					PosV: stmt.PosV,
					EndV: stmt.EndV,
					X:    &ast.Ident{PosV: stmt.PosV, EndV: stmt.EndV, Name: tempName},
					Name: "len",
				},
			},
		},
		Body: body,
	}

	l.emit("enumerate_index_loop", "python_enumerate_loop", "lower enumerate iteration into an index-based Osty loop", stmt.Pos(), stmt.End())
	return []ast.Stmt{tempBinding, lowered}, true
}

func (l *stableLowerer) enumerateIndexPattern(pat ast.Pattern, start, end token.Pos) (ast.Pattern, ast.Expr, bool) {
	switch p := pat.(type) {
	case *ast.IdentPat:
		id := &ast.Ident{PosV: p.PosV, EndV: p.EndV, Name: p.Name}
		return pat, id, true
	case *ast.WildcardPat:
		name := l.nextTemp("index")
		idPat := &ast.IdentPat{PosV: start, EndV: end, Name: name}
		idExpr := &ast.Ident{PosV: start, EndV: end, Name: name}
		return idPat, idExpr, true
	default:
		return nil, nil, false
	}
}

func enumerateIterable(expr ast.Expr) (ast.Expr, bool) {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return nil, false
	}
	switch fn := call.Fn.(type) {
	case *ast.Ident:
		if fn.Name != "enumerate" || len(call.Args) != 1 {
			return nil, false
		}
		return call.Args[0].Value, call.Args[0].Value != nil
	case *ast.FieldExpr:
		if fn.Name != "enumerate" || len(call.Args) != 0 {
			return nil, false
		}
		return fn.X, fn.X != nil
	default:
		return nil, false
	}
}

func appendCallParts(expr ast.Expr) (ast.Expr, ast.Expr, bool) {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return nil, nil, false
	}
	name, ok := call.Fn.(*ast.Ident)
	if !ok || name.Name != "append" || len(call.Args) != 2 {
		return nil, nil, false
	}
	return call.Args[0].Value, call.Args[1].Value, call.Args[0].Value != nil && call.Args[1].Value != nil
}

func simpleIdentPatternName(pat ast.Pattern) (string, bool) {
	id, ok := pat.(*ast.IdentPat)
	if !ok {
		return "", false
	}
	return id.Name, true
}

func pushExprStmt(start, end token.Pos, base *ast.Ident, item ast.Expr) ast.Stmt {
	return &ast.ExprStmt{
		X: &ast.CallExpr{
			PosV: start,
			EndV: end,
			Fn: &ast.FieldExpr{
				PosV: start,
				EndV: end,
				X:    &ast.Ident{PosV: base.PosV, EndV: base.EndV, Name: base.Name},
				Name: "push",
			},
			Args: []*ast.Arg{{
				PosV:  item.Pos(),
				Value: item,
			}},
		},
	}
}
