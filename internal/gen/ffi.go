package gen

import (
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/resolve"
)

// emitFFICall intercepts calls of the form `pkg.Fn(args)` where `pkg`
// is bound to a `use go "path" { ... }` block and applies the spec §12
// bridging rewrites when the declared return type needs them:
//
//   - `Result<T, Error>` — the Go function's `(T, error)` tuple is
//     converted into the Osty Result runtime struct. A non-nil Go
//     `error` becomes `Err(error)`; nil becomes `Ok(value)`.
//   - Unit-returning Result (`Result<(), Error>`) — the Go function
//     returns a bare `error`; we lift it into a `Result[struct{}, any]`.
//   - `T?` — Osty Optional already lowers to `*T`, which matches the
//     Go nullable return exactly, so the plain call flows through.
//   - Anything else — falls back to a direct `pkg.Fn(args)` emission;
//     the caller's default path continues as before.
//
// Returns true when this helper produced output; false when the call
// doesn't match the FFI bridge shape (letting `emitCall` fall through
// to its generic path).
func (g *gen) emitFFICall(c *ast.CallExpr, f *ast.FieldExpr) bool {
	u, fn := g.lookupFFIFn(f)
	if u == nil || fn == nil || fn.ReturnType == nil {
		return false
	}
	switch {
	case isResultAST(fn.ReturnType):
		g.emitFFIResultCall(c, f, fn.ReturnType.(*ast.NamedType))
		return true
	}
	return false
}

// lookupFFIFn resolves the FFI UseDecl that owns `pkg` (if any) and
// returns the matching fn declaration from its body. Both may be nil
// when the reference is not to a Go-FFI package or the name isn't
// declared inside the block.
func (g *gen) lookupFFIFn(f *ast.FieldExpr) (*ast.UseDecl, *ast.FnDecl) {
	id, ok := f.X.(*ast.Ident)
	if !ok {
		return nil, nil
	}
	sym := g.symbolFor(id)
	if sym == nil || sym.Kind != resolve.SymPackage {
		return nil, nil
	}
	u, ok := sym.Decl.(*ast.UseDecl)
	if !ok || !u.IsGoFFI {
		return nil, nil
	}
	for _, gd := range u.GoBody {
		if fn, ok := gd.(*ast.FnDecl); ok && fn.Name == f.Name {
			return u, fn
		}
	}
	return u, nil
}

// emitFFIResultCall emits the §12.4 `(T, error)` → `Result<T, Error>`
// bridge. The generated Go form is an immediately-invoked func that
// performs the call, inspects the error, and rebuilds the Osty Result
// struct.
//
// The T-is-Unit case is special-cased because the corresponding Go
// function signature is `func(...) error` (no first return value); we
// can't destructure `v, err := pkg.Fn(...)` in that shape.
//
// When the Osty-declared error slot is the prelude `Error` interface,
// Go's `error` value is wrapped in a `basicFFIError` adapter so the
// Err-arm binding behaves as a real Osty error: `.message()` is
// callable, `fmt.Println` still prints the underlying Go message via
// `Error()`.
func (g *gen) emitFFIResultCall(c *ast.CallExpr, f *ast.FieldExpr, ret *ast.NamedType) {
	g.needResult = true

	// Determine the Go spellings for T and E. goTypeExpr on a Named
	// `Error` maps to the runtime `ostyError` interface and flips the
	// needOstyError flag, which turns `basicFFIError` into a legal
	// value for the Result's E slot below.
	tGo := "struct{}"
	if len(ret.Args) >= 1 {
		tGo = g.goTypeExpr(ret.Args[0])
	}
	eGo := "any"
	wrapErr := false
	if len(ret.Args) >= 2 {
		eGo = g.goTypeExpr(ret.Args[1])
		if isErrorAST(ret.Args[1]) {
			g.needFFIBasicError = true
			g.needOstyError = true
			wrapErr = true
		}
	}

	isUnit := len(ret.Args) >= 1 && isUnitAST(ret.Args[0])

	callPrefix := g.ffiCallPrefix(f)
	var callBuf strings.Builder
	callBuf.WriteString(callPrefix)
	callBuf.WriteString("(")
	for i, a := range c.Args {
		if i > 0 {
			callBuf.WriteString(", ")
		}
		callBuf.WriteString(g.exprToString(a.Value))
	}
	callBuf.WriteString(")")
	callExpr := callBuf.String()

	errExpr := "__err"
	if wrapErr {
		errExpr = "basicFFIError{err: __err}"
	}

	resultType := "Result[" + tGo + ", " + eGo + "]"
	g.body.writef("func() %s { ", resultType)
	if isUnit {
		g.body.writef("__err := %s; ", callExpr)
		g.body.writef("if __err != nil { return %s{Error: %s} }; ", resultType, errExpr)
		g.body.writef("return %s{IsOk: true}", resultType)
	} else {
		g.body.writef("__v, __err := %s; ", callExpr)
		g.body.writef("if __err != nil { return %s{Error: %s} }; ", resultType, errExpr)
		g.body.writef("return %s{Value: __v, IsOk: true}", resultType)
	}
	g.body.write(" }()")
}

// isErrorAST reports whether t is the AST form of Osty's prelude
// `Error` interface. The qualified-path cases (`std.error.Error`,
// etc.) are rejected; the FFI body's inline types never qualify.
func isErrorAST(t ast.Type) bool {
	n, ok := t.(*ast.NamedType)
	if !ok {
		return false
	}
	return len(n.Path) == 1 && n.Path[0] == "Error" && len(n.Args) == 0
}

// ffiCallPrefix renders the qualified Go name for an FFI call
// (`pkg.Name`) without needing a full expression walker. FFI packages
// are always bound to simple identifiers, so stringifying the ident is
// sufficient — no interpolation, no nested field chains.
func (g *gen) ffiCallPrefix(f *ast.FieldExpr) string {
	id, _ := f.X.(*ast.Ident)
	return mangleIdent(id.Name) + "." + f.Name
}

// exprToString renders an expression into a string via a private writer
// so the FFI bridge can embed the arguments inside a template literal
// without disturbing the current body writer's indentation or state.
func (g *gen) exprToString(e ast.Expr) string {
	saved := g.body
	g.body = newWriter()
	g.emitExpr(e)
	out := string(g.body.bytes())
	g.body = saved
	return strings.TrimSpace(out)
}

// isResultAST reports whether t is the AST form of Osty's
// `Result<T, E>`. Path length is checked explicitly so a qualified
// name like `std.result.Result` doesn't accidentally match.
func isResultAST(t ast.Type) bool {
	n, ok := t.(*ast.NamedType)
	if !ok {
		return false
	}
	return len(n.Path) == 1 && n.Path[0] == "Result" && len(n.Args) == 2
}
