package gen

import (
	"strings"

	"github.com/osty/osty/internal/ast"
)

// emitDecl dispatches to the per-decl emitter. Phase 1 handles fn and
// let; struct/enum/interface/type-alias/use are stubbed with TODOs so
// files that mix supported and unsupported constructs still emit.
func (g *gen) emitDecl(d ast.Decl) {
	switch d := d.(type) {
	case *ast.FnDecl:
		g.emitFnDecl(d)
	case *ast.LetDecl:
		g.emitLetDecl(d)
	case *ast.StructDecl:
		g.emitStructDecl(d)
	case *ast.EnumDecl:
		g.emitEnumDecl(d)
	case *ast.InterfaceDecl:
		g.emitInterfaceDecl(d)
	case *ast.TypeAliasDecl:
		g.emitTypeAliasDecl(d)
	case *ast.UseDecl:
		g.emitUseDecl(d)
	default:
		g.body.writef("// unsupported decl: %T\n", d)
	}
}

// emitFnDecl writes a top-level function or method.
//
// Methods (with a receiver) are emitted in Phase 2 alongside their
// struct/enum; a standalone FnDecl with a non-nil Recv here would be
// a parser accident, so we skip it with a TODO.
//
// Generic functions with recorded instantiations are monomorphized
// (§2.7.3): instead of emitting a single Go-generic definition, we
// emit one specialized copy per distinct type-argument list collected
// by the checker. A generic fn with no observed instantiations emits
// nothing — its body becomes live only when a downstream package
// references it with a concrete type tuple. Non-generic fns take the
// standard single-emission path.
func (g *gen) emitFnDecl(fn *ast.FnDecl) {
	if fn.Recv != nil {
		// Methods are emitted by their enclosing type in Phase 2.
		// A parser FnDecl with Recv at top level is treated as data
		// for the owner; skip here.
		return
	}

	if len(fn.Generics) > 0 {
		sym := g.res.FileScope.Lookup(fn.Name)
		recs := g.genericInstances[sym]
		if len(recs) == 0 {
			// No instantiations: emit nothing. Monomorphization is a
			// demand-driven transform — a generic body with no call
			// sites has no lowered form in the Go output.
			return
		}
		g.emitMonomorphizedFn(fn, recs)
		return
	}

	g.emitFnDeclBody(fn, fn.Name)
}

// emitFnDeclBody writes a single function definition under `name`. It
// is the shared emission path used by both the non-generic fast path
// and the per-instantiation loop over a monomorphized generic. Generic
// type parameters are never emitted as Go `[T any]` brackets here —
// when we reach this function for a generic source fn, the caller has
// already pushed a substitution env so every type-param reference
// resolves to a concrete Go type.
func (g *gen) emitFnDeclBody(fn *ast.FnDecl, name string) {
	g.body.nl()
	g.body.write("func ")
	g.body.write(name)

	g.emitParamList(fn.Params)

	if fn.ReturnType != nil {
		g.body.write(" ")
		g.body.write(g.goTypeExpr(fn.ReturnType))
	}

	if fn.Body == nil {
		g.body.writeln(" {}")
		return
	}
	g.body.write(" ")

	prevRet := g.currentRetType
	g.currentRetType = fn.ReturnType
	defer func() { g.currentRetType = prevRet }()

	g.emitBlockAsReturn(fn.Body, fn.ReturnType != nil)
	g.body.nl()
}

// emitParamList emits `(a type1, b type2, ...)`. Closure params with
// destructuring aren't reachable here — top-level params always have
// Name set.
func (g *gen) emitParamList(params []*ast.Param) {
	g.body.write("(")
	for i, p := range params {
		if i > 0 {
			g.body.write(", ")
		}
		name := p.Name
		if name == "" {
			name = "_"
		}
		g.body.write(mangleIdent(name))
		if p.Type != nil {
			g.body.write(" ")
			g.body.write(g.goTypeExpr(p.Type))
		} else {
			g.body.write(" any")
		}
	}
	g.body.write(")")
}

// emitLetDecl handles top-level `let` / `pub let`. Osty `let` is
// immutable-by-default; Go has no equivalent, so we emit `var` and
// let the type checker police reassignment.
func (g *gen) emitLetDecl(l *ast.LetDecl) {
	g.body.nl()
	g.body.write("var ")
	g.body.write(mangleIdent(l.Name))
	if l.Type != nil {
		g.body.write(" ")
		g.body.write(g.goTypeExpr(l.Type))
	} else if t := g.symTypeOf(g.res.FileScope.Lookup(l.Name)); t != nil {
		g.body.write(" ")
		g.body.write(g.goType(t))
	}
	if l.Value != nil {
		g.body.write(" = ")
		g.emitExpr(l.Value)
	}
	g.body.nl()
}

// emitStructDecl writes `type Name struct { ... }` plus every method
// declared inside the struct body. Methods with a `self` receiver
// become Go methods on the struct; associated functions (no receiver)
// become package-level `TypeName_funcName`.
func (g *gen) emitStructDecl(s *ast.StructDecl) {
	g.body.nl()
	// Generic type parameters land in Phase 3; for now we still emit
	// the bracket block with `any` constraints so downstream code at
	// least type-checks syntactically.
	if len(s.Generics) > 0 {
		g.body.writef("type %s[", s.Name)
		for i, gp := range s.Generics {
			if i > 0 {
				g.body.write(", ")
			}
			g.body.writef("%s any", gp.Name)
		}
		g.body.write("] struct {")
	} else {
		g.body.writef("type %s struct {", s.Name)
	}
	if len(s.Fields) == 0 {
		g.body.writeln("}")
	} else {
		g.body.nl()
		g.body.indent()
		for _, f := range s.Fields {
			g.body.writef("%s %s\n", mangleIdent(f.Name), g.goTypeExpr(f.Type))
		}
		g.body.dedent()
		g.body.writeln("}")
	}
	// Methods — emitted after the type so Go's "declaration before use"
	// rule doesn't bite when one method calls another within the same
	// type (it doesn't, but being explicit about order is cheap).
	for _, m := range s.Methods {
		g.emitMethod(s.Name, m, false)
	}
}

// emitEnumDecl writes an enum as an interface plus one struct per
// variant, wired together with a marker method. Methods on the enum
// are lowered to free functions `EnumName_methodName(self EnumName, ...)`
// which every call site rewrites; see emitCall.
//
//	enum Shape { Circle(Float), Empty; fn area(self) -> Float { ... } }
//
// becomes
//
//	type Shape interface { _isShape() }
//	type Shape_Circle struct { F0 float64 }
//	func (Shape_Circle) _isShape() {}
//	type Shape_Empty struct{}
//	func (Shape_Empty) _isShape() {}
//	func Shape_area(self Shape) float64 { ... }
func (g *gen) emitEnumDecl(e *ast.EnumDecl) {
	g.body.nl()
	g.body.writef("type %s interface{ _is%s() }\n", e.Name, e.Name)
	for _, v := range e.Variants {
		g.emitVariant(e, v)
	}
	for _, m := range e.Methods {
		g.emitMethod(e.Name, m, true)
	}
}

// emitVariant writes the struct + marker-method for one enum variant.
func (g *gen) emitVariant(e *ast.EnumDecl, v *ast.Variant) {
	name := e.Name + "_" + v.Name
	if len(v.Fields) == 0 {
		g.body.writef("type %s struct{}\n", name)
	} else {
		g.body.writef("type %s struct {\n", name)
		g.body.indent()
		for i, f := range v.Fields {
			g.body.writef("F%d %s\n", i, g.goTypeExpr(f))
		}
		g.body.dedent()
		g.body.writeln("}")
	}
	g.body.writef("func (%s) _is%s() {}\n", name, e.Name)
}

// emitMethod writes a single method.
//
// For structs (enumMethod=false) and a receiver-bearing fn we emit as
// a Go method on the struct. For enums, Go's interface can't carry a
// default implementation, so we lower to `TypeName_method(self, ...)`
// and call sites are rewritten in emitCall.
//
// Associated functions (no receiver) always lower to package-level
// `TypeName_fn(...)`.
func (g *gen) emitMethod(typeName string, m *ast.FnDecl, enumMethod bool) {
	g.body.nl()

	free := m.Recv == nil || enumMethod

	if free {
		g.body.writef("func %s_%s", typeName, m.Name)
	} else {
		// Pointer receiver for `mut self`, value for `self`.
		if m.Recv.Mut {
			g.body.writef("func (self *%s) %s", typeName, m.Name)
		} else {
			g.body.writef("func (self %s) %s", typeName, m.Name)
		}
	}
	g.body.write("(")
	first := true
	if free && m.Recv != nil {
		g.body.writef("self %s", typeName)
		first = false
	}
	for _, p := range m.Params {
		if !first {
			g.body.write(", ")
		}
		first = false
		name := p.Name
		if name == "" {
			name = "_"
		}
		g.body.write(mangleIdent(name))
		if p.Type != nil {
			g.body.write(" ")
			g.body.write(g.resolveSelfType(p.Type, typeName))
		} else {
			g.body.write(" any")
		}
	}
	g.body.write(")")

	if m.ReturnType != nil {
		g.body.write(" ")
		g.body.write(g.resolveSelfType(m.ReturnType, typeName))
	}

	prev := g.selfType
	g.selfType = typeName
	prevRet := g.currentRetType
	g.currentRetType = m.ReturnType
	defer func() {
		g.selfType = prev
		g.currentRetType = prevRet
	}()

	if m.Body == nil {
		g.body.writeln(" {}")
		return
	}
	g.body.write(" ")
	g.emitBlockAsReturn(m.Body, m.ReturnType != nil)
	g.body.nl()
}

// resolveSelfType rewrites a bare `Self` type annotation to the
// enclosing type's name; other type expressions pass through unchanged.
func (g *gen) resolveSelfType(t ast.Type, typeName string) string {
	if n, ok := t.(*ast.NamedType); ok && len(n.Path) == 1 && n.Path[0] == "Self" {
		return typeName
	}
	return g.goTypeExpr(t)
}

// emitInterfaceDecl writes a Go interface. Osty allows composed
// interfaces (`interface ReadWriter { Reader; Writer }`) which map to
// Go's interface embedding.
func (g *gen) emitInterfaceDecl(i *ast.InterfaceDecl) {
	g.body.nl()
	g.body.writef("type %s interface {\n", i.Name)
	g.body.indent()
	for _, ext := range i.Extends {
		g.body.writeln(g.goTypeExpr(ext))
	}
	for _, m := range i.Methods {
		g.body.write(m.Name)
		g.body.write("(")
		for j, p := range m.Params {
			if j > 0 {
				g.body.write(", ")
			}
			name := p.Name
			if name == "" {
				name = "_"
			}
			g.body.writef("%s %s", mangleIdent(name), g.goTypeExpr(p.Type))
		}
		g.body.write(")")
		if m.ReturnType != nil {
			g.body.write(" ")
			g.body.write(g.goTypeExpr(m.ReturnType))
		}
		g.body.nl()
	}
	g.body.dedent()
	g.body.writeln("}")
}

// emitTypeAliasDecl writes `type Name = Target` (Go type alias) for
// non-generic aliases. Generic aliases require Go 1.24+ features
// and are Phase 3 work; for now we emit a commented note.
func (g *gen) emitTypeAliasDecl(a *ast.TypeAliasDecl) {
	g.body.nl()
	if len(a.Generics) > 0 {
		g.body.writef("// TODO(phase3): generic type alias %s\n", a.Name)
		return
	}
	g.body.writef("type %s = %s\n", a.Name, g.goTypeExpr(a.Target))
}

func (g *gen) emitUseDecl(u *ast.UseDecl) {
	if u.IsGoFFI {
		// `use go "path" [as alias] { fn Foo(...); struct Bar { ... } }`
		//
		// Emit a real Go import. When the Osty alias matches the Go
		// package's default name (last path component), a bare import
		// suffices. Otherwise aliased imports use Go's `import alias "path"`
		// form via the aliased-import map.
		//
		// The FFI body is a schema for the checker — it declares the
		// signatures we expect from the Go package. No code is emitted
		// for it; call sites like `fmt.Println(x)` resolve to the real
		// Go symbol via the package import.
		alias := u.Alias
		defaultAlias := lastPathComponent(u.GoPath)
		if alias == "" {
			alias = defaultAlias
		}
		if alias == defaultAlias {
			g.use(u.GoPath)
		} else {
			g.useAs(u.GoPath, alias)
		}
		return
	}
	// Regular `use pkg.path [as alias]` — Osty module system, not yet
	// backed by a loader. For well-known stdlib shims (std.testing,
	// std.thread) we emit a mock struct so spec fixtures that *call*
	// those helpers compile. Real stdlib usage bridges through Go's
	// own packages; see stdlibBridge for the mapping.
	alias := u.Alias
	if alias == "" && len(u.Path) > 0 {
		alias = u.Path[len(u.Path)-1]
	}
	if alias == "" {
		return
	}
	full := strings.Join(u.Path, ".")
	if bridge := stdlibBridge(u.Path); bridge != "" {
		g.use(bridge)
		// When the Go bridge's package name already matches the
		// Osty alias, no rebinding is needed.
		if lastPathComponent(bridge) == alias {
			return
		}
		g.useAs(bridge, alias)
		return
	}
	stub := knownStdlibStub(u.Path)
	if stub == "" {
		stub = "struct{}{}"
	}
	g.body.writef("\nvar %s = %s // stub for `use %s`\n",
		mangleIdent(alias), stub, full)
}

// stdlibBridge maps an Osty stdlib module path to a Go package whose
// exported surface matches closely enough for typical spec use. Returns
// "" when no bridge is available (caller falls back to a mock stub).
func stdlibBridge(path []string) string {
	if len(path) < 2 || path[0] != "std" {
		return ""
	}
	switch path[1] {
	case "os":
		return "os"
	case "fs":
		return "os"
	case "io":
		return "io"
	case "time":
		return "time"
	case "strings":
		return "strings"
	case "math":
		return "math"
	case "errors":
		return "errors"
	}
	return ""
}

// knownStdlibStub returns a Go expression that emulates just enough of
// a stdlib module's shape for fixtures to compile. Empty string means
// "use the default empty-struct stub".
func knownStdlibStub(path []string) string {
	if len(path) < 2 || path[0] != "std" {
		return ""
	}
	switch path[len(path)-1] {
	case "testing":
		return `struct {
			assert    func(...any)
			assertEq  func(...any)
			context   func(...any)
			benchmark func(...any)
		}{
			assert:    func(...any) {},
			assertEq:  func(...any) {},
			context:   func(...any) {},
			benchmark: func(...any) {},
		}`
	case "thread":
		return `struct {
			collectAll func(...any) any
			race       func(...any) any
			chan_      func(...any) any
			select_    func(...any) any
		}{
			collectAll: func(...any) any { return nil },
			race:       func(...any) any { return nil },
			chan_:      func(...any) any { return nil },
			select_:    func(...any) any { return nil },
		}`
	}
	return ""
}

// lastPathComponent returns the last slash-delimited segment of a Go
// import path, used as the default alias when the user didn't spell
// one out.
func lastPathComponent(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// goConstraint maps a list of Osty generic bounds to a single Go
// type-parameter constraint. Prelude interfaces get Go built-in
// analogues; user-defined interfaces are joined with `|` isn't
// idiomatic, so we union-with-embedding into a single name when one
// applies and fall back to `any` when the set is empty.
func (g *gen) goConstraint(bounds []ast.Type) string {
	if len(bounds) == 0 {
		return "any"
	}
	parts := make([]string, 0, len(bounds))
	for _, b := range bounds {
		n, ok := b.(*ast.NamedType)
		if !ok || len(n.Path) == 0 {
			parts = append(parts, "any")
			continue
		}
		switch n.Path[len(n.Path)-1] {
		case "Ordered":
			g.use("cmp")
			parts = append(parts, "cmp.Ordered")
		case "Equal", "Hashable":
			parts = append(parts, "comparable")
		default:
			parts = append(parts, g.goTypeExpr(b))
		}
	}
	// Multiple bounds: intersect via Go's interface-embedding only
	// works as an interface definition, which is heavier than a
	// one-liner. Pick the first — good enough for spec fixtures.
	return parts[0]
}

// emitBlockAsReturn writes a block with the final expression optionally
// lifted into an implicit `return`. Osty allows the last expression of
// a function body to serve as the return value — Go requires an explicit
// `return`, so we rewrite when the function has a declared return type.
//
// If the final expression contains a `?`, the pre-lift pass runs first
// so the return expression reduces to a straight substitution on the
// lifted temps.
//
// The closing `}` has no trailing newline; callers add one as needed.
func (g *gen) emitBlockAsReturn(b *ast.Block, wantReturn bool) {
	g.body.writeln("{")
	g.body.indent()
	stmts := b.Stmts
	if wantReturn && len(stmts) > 0 {
		last := stmts[len(stmts)-1]
		if es, ok := last.(*ast.ExprStmt); ok && !isVoidCall(es.X) {
			for _, s := range stmts[:len(stmts)-1] {
				g.emitStmt(s)
			}
			g.preLiftQuestions(es.X)
			g.body.write("return ")
			g.emitExpr(es.X)
			g.body.nl()
			g.resetQuestionSubs()
			g.body.dedent()
			g.body.write("}")
			return
		}
	}
	g.emitStmts(stmts)
	g.body.dedent()
	g.body.write("}")
}

// isVoidCall heuristically reports whether an expression is a
// call whose result is discarded (e.g. `println(x)`). These should NOT
// be lifted into a `return` even when they appear last. We can't fully
// decide this without the type checker, so we treat common void builtins
// as void and lift everything else.
func isVoidCall(e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	id, ok := call.Fn.(*ast.Ident)
	if !ok {
		return false
	}
	switch id.Name {
	case "println", "print", "eprintln", "eprint", "dbg":
		return true
	}
	return false
}
