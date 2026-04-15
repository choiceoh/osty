package check

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/types"
)

// containsCapability reports whether a value type carries a
// structured-concurrency capability. Function signatures are not treated
// as capability values; a closure only becomes illegal when it captures
// an existing capability from an escaping scope.
func containsCapability(t types.Type) bool {
	if t == nil || types.IsError(t) {
		return false
	}
	switch x := t.(type) {
	case *types.Named:
		if x.Sym != nil && (x.Sym.Name == "Handle" || x.Sym.Name == "TaskGroup") {
			return true
		}
		for _, a := range x.Args {
			if containsCapability(a) {
				return true
			}
		}
	case *types.Tuple:
		for _, e := range x.Elems {
			if containsCapability(e) {
				return true
			}
		}
	case *types.Optional:
		return containsCapability(x.Inner)
	}
	return false
}

func (c *checker) rejectCapabilityEscape(n ast.Node, t types.Type, action string) bool {
	if n == nil || !containsCapability(t) {
		return false
	}
	c.errNode(n, diag.CodeCapabilityEscape,
		"non-escaping capability value of type `%s` cannot %s", t, action)
	return true
}

func (c *checker) firstCapabilityCapture(e ast.Expr, scope *env) (*ast.Ident, string, bool) {
	if e == nil || scope == nil || len(scope.capabilitySyms) == 0 {
		return nil, "", false
	}
	if id, ok := c.firstCapabilityCaptureExpr(e, scope); ok {
		if sym := c.symbol(id); sym != nil {
			return id, sym.Name, true
		}
		return id, id.Name, true
	}
	return nil, "", false
}

func (c *checker) firstCapabilityCaptureStmt(s ast.Stmt, scope *env) (*ast.Ident, bool) {
	switch x := s.(type) {
	case *ast.LetStmt:
		return c.firstCapabilityCaptureExpr(x.Value, scope)
	case *ast.ExprStmt:
		return c.firstCapabilityCaptureExpr(x.X, scope)
	case *ast.AssignStmt:
		if id, ok := c.firstCapabilityCaptureExpr(x.Value, scope); ok {
			return id, true
		}
		for _, t := range x.Targets {
			if id, ok := c.firstCapabilityCaptureExpr(t, scope); ok {
				return id, true
			}
		}
	case *ast.ChanSendStmt:
		if id, ok := c.firstCapabilityCaptureExpr(x.Channel, scope); ok {
			return id, true
		}
		return c.firstCapabilityCaptureExpr(x.Value, scope)
	case *ast.ReturnStmt:
		return c.firstCapabilityCaptureExpr(x.Value, scope)
	case *ast.DeferStmt:
		return c.firstCapabilityCaptureExpr(x.X, scope)
	case *ast.ForStmt:
		if id, ok := c.firstCapabilityCaptureExpr(x.Iter, scope); ok {
			return id, true
		}
		if x.Body != nil {
			return c.firstCapabilityCaptureExpr(x.Body, scope)
		}
	case *ast.Block:
		return c.firstCapabilityCaptureExpr(x, scope)
	}
	return nil, false
}

func (c *checker) firstCapabilityCaptureExpr(e ast.Expr, scope *env) (*ast.Ident, bool) {
	if e == nil {
		return nil, false
	}
	switch x := e.(type) {
	case *ast.Ident:
		if scope.hasCapability(c.symbol(x)) {
			return x, true
		}
	case *ast.StringLit:
		for _, p := range x.Parts {
			if !p.IsLit {
				if id, ok := c.firstCapabilityCaptureExpr(p.Expr, scope); ok {
					return id, true
				}
			}
		}
	case *ast.UnaryExpr:
		return c.firstCapabilityCaptureExpr(x.X, scope)
	case *ast.BinaryExpr:
		if id, ok := c.firstCapabilityCaptureExpr(x.Left, scope); ok {
			return id, true
		}
		return c.firstCapabilityCaptureExpr(x.Right, scope)
	case *ast.QuestionExpr:
		return c.firstCapabilityCaptureExpr(x.X, scope)
	case *ast.CallExpr:
		if id, ok := c.firstCapabilityCaptureExpr(x.Fn, scope); ok {
			return id, true
		}
		for _, a := range x.Args {
			if a != nil {
				if id, ok := c.firstCapabilityCaptureExpr(a.Value, scope); ok {
					return id, true
				}
			}
		}
	case *ast.FieldExpr:
		return c.firstCapabilityCaptureExpr(x.X, scope)
	case *ast.IndexExpr:
		if id, ok := c.firstCapabilityCaptureExpr(x.X, scope); ok {
			return id, true
		}
		return c.firstCapabilityCaptureExpr(x.Index, scope)
	case *ast.TurbofishExpr:
		return c.firstCapabilityCaptureExpr(x.Base, scope)
	case *ast.RangeExpr:
		if id, ok := c.firstCapabilityCaptureExpr(x.Start, scope); ok {
			return id, true
		}
		return c.firstCapabilityCaptureExpr(x.Stop, scope)
	case *ast.ParenExpr:
		return c.firstCapabilityCaptureExpr(x.X, scope)
	case *ast.TupleExpr:
		for _, elem := range x.Elems {
			if id, ok := c.firstCapabilityCaptureExpr(elem, scope); ok {
				return id, true
			}
		}
	case *ast.ListExpr:
		for _, elem := range x.Elems {
			if id, ok := c.firstCapabilityCaptureExpr(elem, scope); ok {
				return id, true
			}
		}
	case *ast.MapExpr:
		for _, ent := range x.Entries {
			if ent == nil {
				continue
			}
			if id, ok := c.firstCapabilityCaptureExpr(ent.Key, scope); ok {
				return id, true
			}
			if id, ok := c.firstCapabilityCaptureExpr(ent.Value, scope); ok {
				return id, true
			}
		}
	case *ast.StructLit:
		for _, f := range x.Fields {
			if f != nil {
				if id, ok := c.firstCapabilityCaptureExpr(f.Value, scope); ok {
					return id, true
				}
			}
		}
		return c.firstCapabilityCaptureExpr(x.Spread, scope)
	case *ast.IfExpr:
		if id, ok := c.firstCapabilityCaptureExpr(x.Cond, scope); ok {
			return id, true
		}
		if id, ok := c.firstCapabilityCaptureExpr(x.Then, scope); ok {
			return id, true
		}
		return c.firstCapabilityCaptureExpr(x.Else, scope)
	case *ast.MatchExpr:
		if id, ok := c.firstCapabilityCaptureExpr(x.Scrutinee, scope); ok {
			return id, true
		}
		for _, arm := range x.Arms {
			if arm == nil {
				continue
			}
			if id, ok := c.firstCapabilityCaptureExpr(arm.Guard, scope); ok {
				return id, true
			}
			if id, ok := c.firstCapabilityCaptureExpr(arm.Body, scope); ok {
				return id, true
			}
		}
	case *ast.ClosureExpr:
		return c.firstCapabilityCaptureExpr(x.Body, scope)
	case *ast.Block:
		for _, s := range x.Stmts {
			if id, ok := c.firstCapabilityCaptureStmt(s, scope); ok {
				return id, true
			}
		}
	}
	return nil, false
}
