package gen

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/resolve"
)

// emitFFICall rewrites `pkg.Fn(args)` calls into the §12 FFI bridge
// when the declared return type needs it (today: Result<T, Error>).
// `T?` and plain values flow through the generic call path unchanged.
// Returns true when output was produced.
func (g *gen) emitFFICall(c *ast.CallExpr, f *ast.FieldExpr) bool {
	fn := g.lookupFFIFn(f)
	if fn == nil || fn.ReturnType == nil {
		return false
	}
	if isResultAST(fn.ReturnType) {
		g.emitFFIResultCall(c, f, fn.ReturnType.(*ast.NamedType))
		return true
	}
	return false
}

// lookupFFIFn returns the fn declaration matching `f.Name` inside the
// FFI block that binds `f.X`, or nil when f isn't an FFI call shape.
func (g *gen) lookupFFIFn(f *ast.FieldExpr) *ast.FnDecl {
	id, ok := f.X.(*ast.Ident)
	if !ok {
		return nil
	}
	sym := g.symbolFor(id)
	if sym == nil || sym.Kind != resolve.SymPackage {
		return nil
	}
	u, ok := sym.Decl.(*ast.UseDecl)
	if !ok || !u.IsGoFFI {
		return nil
	}
	for _, gd := range u.GoBody {
		if fn, ok := gd.(*ast.FnDecl); ok && fn.Name == f.Name {
			return fn
		}
	}
	return nil
}

// emitFFIResultCall lifts Go's `(T, error)` tuple into the Osty Result
// runtime. The T-is-Unit case drops the value binding because the
// corresponding Go signature is `func(...) error`. When the Osty error
// slot is the prelude `Error` interface, the Go error is wrapped in
// `basicFFIError` so `.message()` binds against a real method set.
func (g *gen) emitFFIResultCall(c *ast.CallExpr, f *ast.FieldExpr, ret *ast.NamedType) {
	g.needResult = true

	tGo := "struct{}"
	if len(ret.Args) >= 1 {
		tGo = g.goTypeExpr(ret.Args[0])
	}
	eGo := "any"
	wrapErr := false
	if len(ret.Args) >= 2 {
		// goTypeExpr on the `Error` named type already sets
		// needOstyError via typ.go; no need to flip it again here.
		eGo = g.goTypeExpr(ret.Args[1])
		if isErrorAST(ret.Args[1]) {
			g.needFFIBasicError = true
			wrapErr = true
		}
	}

	isUnit := len(ret.Args) >= 1 && isUnitAST(ret.Args[0])

	errExpr := "__err"
	if wrapErr {
		errExpr = "basicFFIError{err: __err}"
	}
	resultType := "Result[" + tGo + ", " + eGo + "]"

	id, _ := f.X.(*ast.Ident)
	callee := mangleIdent(id.Name) + "." + f.Name

	g.body.writef("func() %s { ", resultType)
	if isUnit {
		g.body.writef("__err := %s(", callee)
	} else {
		g.body.writef("__v, __err := %s(", callee)
	}
	for i, a := range c.Args {
		if i > 0 {
			g.body.write(", ")
		}
		g.emitExpr(a.Value)
	}
	g.body.write("); ")
	g.body.writef("if __err != nil { return %s{Error: %s} }; ", resultType, errExpr)
	if isUnit {
		g.body.writef("return %s{IsOk: true}", resultType)
	} else {
		g.body.writef("return %s{Value: __v, IsOk: true}", resultType)
	}
	g.body.write(" }()")
}

// isResultAST matches `Result<T, E>` from an FFI body; the single-seg
// path check filters qualified names like `std.result.Result`.
func isResultAST(t ast.Type) bool {
	n, ok := t.(*ast.NamedType)
	if !ok {
		return false
	}
	return len(n.Path) == 1 && n.Path[0] == "Result" && len(n.Args) == 2
}

// isErrorAST matches the Osty prelude `Error` interface spelled bare
// inside an FFI body; qualified references don't match.
func isErrorAST(t ast.Type) bool {
	n, ok := t.(*ast.NamedType)
	if !ok {
		return false
	}
	return len(n.Path) == 1 && n.Path[0] == "Error" && len(n.Args) == 0
}
