//go:build selfhostgen

package gen

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/types"
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
		if g.emitGenericFnDecls {
			g.emitFnDeclBody(fn, g.renamedFnName(fn.Name))
			return
		}
		// Demand-driven: generic fns never emit inline. A call site
		// (possibly inside another generic body being specialized)
		// requests an instantiation via requestInstance, and the
		// post-pass drainInstances materializes it.
		return
	}

	g.emitFnDeclBody(fn, g.renamedFnName(fn.Name))
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
	g.sourceMarker(fn)
	g.body.write("func ")
	g.body.write(name)
	if g.emitGenericFnDecls && len(fn.Generics) > 0 {
		g.emitGenericParamList(fn.Generics)
	}

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
	prevRetGo := g.currentRetGo
	g.currentRetType = fn.ReturnType
	g.currentRetGo = ""
	if fn.ReturnType != nil {
		g.currentRetGo = g.goTypeExpr(fn.ReturnType)
	}
	defer func() {
		g.currentRetType = prevRet
		g.currentRetGo = prevRetGo
	}()

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
	g.sourceMarker(l)
	g.body.write("var ")
	name := g.renamedFnName(l.Name)
	if name == l.Name {
		name = mangleIdent(l.Name)
	}
	g.body.write(name)
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
	g.sourceMarker(s)
	if len(s.Generics) > 0 {
		g.body.writef("type %s", s.Name)
		g.emitGenericParamList(s.Generics)
		g.body.write(" struct {")
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
	g.emitStructJSONMarshal(s)
	g.emitStructJSONUnmarshal(s)
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
	g.sourceMarker(e)
	g.body.writef("type %s interface{ _is%s() }\n", e.Name, e.Name)
	for _, v := range e.Variants {
		g.emitVariant(e, v)
	}
	for _, m := range e.Methods {
		g.emitMethod(e.Name, m, true)
	}
	g.emitEnumJSONDecoder(e)
	for _, m := range e.Methods {
		g.emitEnumVariantMethodWrappers(e, m)
	}
}

// emitVariant writes the struct + marker-method for one enum variant.
func (g *gen) emitVariant(e *ast.EnumDecl, v *ast.Variant) {
	name := e.Name + "_" + v.Name
	if len(v.Fields) == 0 {
		g.body.writef("type %s struct{ _ref byte }\n", name)
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
	g.emitVariantJSONMarshal(e, v, name)
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
	g.sourceMarker(m)

	prevSelf := g.selfType
	g.selfType = typeName

	free := m.Recv == nil || enumMethod

	if free {
		g.body.writef("func %s_%s", typeName, m.Name)
	} else {
		g.body.writef("func (self *%s) %s", typeName, m.Name)
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

	prevRet := g.currentRetType
	prevRetGo := g.currentRetGo
	g.currentRetType = m.ReturnType
	g.currentRetGo = ""
	if m.ReturnType != nil {
		g.currentRetGo = g.resolveSelfType(m.ReturnType, typeName)
	}
	defer func() {
		g.selfType = prevSelf
		g.currentRetType = prevRet
		g.currentRetGo = prevRetGo
	}()

	if m.Body == nil {
		g.body.writeln(" {}")
		return
	}
	g.body.write(" ")
	g.emitBlockAsReturn(m.Body, m.ReturnType != nil)
	g.body.nl()
}

func (g *gen) emitEnumVariantMethodWrappers(e *ast.EnumDecl, m *ast.FnDecl) {
	if m.Recv == nil || len(m.Generics) != 0 {
		return
	}
	for _, v := range e.Variants {
		recvType := e.Name + "_" + v.Name
		g.body.nl()
		g.sourceMarker(m)
		g.body.writef("func (self *%s) %s(", recvType, m.Name)
		paramNames := make([]string, len(m.Params))
		for i, p := range m.Params {
			if i > 0 {
				g.body.write(", ")
			}
			name := p.Name
			if name == "" {
				name = "_p" + itoa(i)
			}
			paramNames[i] = mangleIdent(name)
			g.body.write(paramNames[i])
			if p.Type != nil {
				g.body.write(" ")
				g.body.write(g.resolveSelfType(p.Type, e.Name))
			} else {
				g.body.write(" any")
			}
		}
		g.body.write(")")
		hasReturn := m.ReturnType != nil && !isUnitAST(m.ReturnType)
		if hasReturn {
			g.body.write(" ")
			g.body.write(g.resolveSelfType(m.ReturnType, e.Name))
		}
		g.body.write(" { ")
		if hasReturn {
			g.body.write("return ")
		}
		g.body.writef("%s_%s(%s(self)", e.Name, m.Name, e.Name)
		for _, name := range paramNames {
			g.body.write(", ")
			g.body.write(name)
		}
		g.body.writeln(") }")
	}
}

// resolveSelfType rewrites a bare `Self` type annotation to the
// enclosing type's name; other type expressions pass through unchanged.
func (g *gen) resolveSelfType(t ast.Type, typeName string) string {
	if n, ok := t.(*ast.NamedType); ok && len(n.Path) == 1 && n.Path[0] == "Self" {
		return g.goSelfTypeAST(n.Args)
	}
	return g.goTypeExpr(t)
}

// emitInterfaceDecl writes a Go interface. Osty allows composed
// interfaces (`interface ReadWriter { Reader; Writer }`) which map to
// Go's interface embedding.
func (g *gen) emitInterfaceDecl(i *ast.InterfaceDecl) {
	g.body.nl()
	g.sourceMarker(i)
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

// emitTypeAliasDecl writes a Go type alias. Generic aliases are emitted
// with Go type parameters; constraints currently lower to `any`, matching
// the existing generic struct/function backend surface.
func (g *gen) emitTypeAliasDecl(a *ast.TypeAliasDecl) {
	g.body.nl()
	g.sourceMarker(a)
	g.body.writef("type %s", a.Name)
	g.emitGenericParamList(a.Generics)
	g.body.writef(" = %s\n", g.goTypeExpr(a.Target))
}

func (g *gen) emitGenericParamList(params []*ast.GenericParam) {
	if len(params) == 0 {
		return
	}
	g.body.write("[")
	for i, p := range params {
		if i > 0 {
			g.body.write(", ")
		}
		g.body.writef("%s any", p.Name)
	}
	g.body.write("]")
}

func (g *gen) emitUseDecl(u *ast.UseDecl) {
	if u.IsFFI() {
		importPath := u.GoPath
		if u.IsRuntimeFFI {
			importPath = runtimeFFIGoImport(u.RuntimePath)
		}
		// FFI declarations carry signatures in Osty source. The bootstrap Go
		// emitter maps runtime FFI paths onto temporary host imports until the
		// native runtime owns those calls.
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
		defaultAlias := lastPathComponent(importPath)
		if alias == "" {
			alias = defaultAlias
		}
		if !g.aliasUsedAsSelector(alias) {
			g.emitUseStub(alias, "struct{}{}", importPath)
			return
		}
		if alias == defaultAlias {
			g.use(importPath)
		} else {
			g.useAs(importPath, alias)
		}
		return
	}
	// Regular `use pkg.path [as alias]` — Osty module system. The
	// embedded stdlib is a resolved signature surface, not a runtime
	// implementation, so general gen may still emit `std.*` package
	// stubs. A few modules have temporary Go bridges for fixture
	// compatibility; well-known shims (std.testing, std.thread) get
	// mock structs when they need a callable shape. The test harness
	// separately replaces the std.testing stub with a real runtime.
	alias := useDeclAlias(u)
	if alias == "" {
		return
	}
	full := useFullPath(u)
	if module, ok := stdlibOstyModulePath(u.Path); ok {
		if g.aliasUsedAsSelector(alias) {
			g.requestStdlibOsty(module)
		}
		return
	}
	if g.emitStdlibRuntimeBridge(alias, u.Path, full) {
		return
	}
	if bridge := stdlibBridge(u.Path); bridge != "" && g.aliasUsedAsSelector(alias) {
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
	g.emitUseStub(alias, stub, full)
}

func (g *gen) emitStdlibRuntimeBridge(alias string, path []string, full string) bool {
	if len(path) != 2 || path[0] != "std" {
		return false
	}
	switch path[1] {
	case "fs":
		if !g.aliasUsedAsSelector(alias) {
			return false
		}
		osAlias, utf8Alias := g.stdFSImportAliases()
		g.needFS = true
		g.emitUseStub(alias, fmt.Sprintf(`struct {
			readToString func(path string) Result[string, any]
			writeString  func(path string, contents string) Result[struct{}, any]
			exists       func(path string) bool
			remove       func(path string) Result[struct{}, any]
			}{
				readToString: func(path string) Result[string, any] {
					data, err := %[1]s.ReadFile(path)
					if err != nil {
						return resultErr[string, any](err)
					}
					if !%[2]s.Valid(data) {
						return resultErr[string, any]("fs.readToString: invalid UTF-8 in " + path)
					}
					return resultOk[string, any](string(data))
				},
				writeString: func(path string, contents string) Result[struct{}, any] {
					if err := %[1]s.WriteFile(path, []byte(contents), 0o644); err != nil {
						return resultErr[struct{}, any](err)
					}
					return resultOk[struct{}, any](struct{}{})
				},
				exists: func(path string) bool {
					_, err := %[1]s.Stat(path)
					return err == nil
				},
				remove: func(path string) Result[struct{}, any] {
					if err := %[1]s.Remove(path); err != nil {
						return resultErr[struct{}, any](err)
					}
					return resultOk[struct{}, any](struct{}{})
				},
			}`, osAlias, utf8Alias), full)
		return true
	case "ref":
		g.needRefRuntime = true
		g.emitUseStub(alias, `struct {
			same func(any, any) bool
		}{
			same: refSame,
		}`, full)
		return true
	case "json":
		g.needJSON = true
		g.emitUseStub(alias, `struct {
			encode         func(value any) string
			stringify      func(value any) string
			Object         func(value map[string]Json) Json
			Array          func(value []Json) Json
			String         func(value string) Json
			Number         func(value float64) Json
			Bool           func(value bool) Json
			Null           Json
			stringifyValue func(value Json) string
			parseValue     func(text string) Result[Json, any]
			isNull         func(value Json) bool
			isBool         func(value Json) bool
			isNumber       func(value Json) bool
			isString       func(value Json) bool
			isArray        func(value Json) bool
			isObject       func(value Json) bool
			asBool         func(value Json) Result[bool, any]
			asNumber       func(value Json) Result[float64, any]
			asString       func(value Json) Result[string, any]
			asArray        func(value Json) Result[[]Json, any]
			asObject       func(value Json) Result[map[string]Json, any]
			getField       func(value Json, key string) Result[Json, any]
			getIndex       func(value Json, index int) Result[Json, any]
		}{
			encode:         jsonEncode,
			stringify:      jsonStringify,
			Object:         jsonObject,
			Array:          jsonArray,
			String:         jsonString,
			Number:         jsonNumber,
			Bool:           jsonBool,
			Null:           nil,
			stringifyValue: jsonStringifyValue,
			parseValue:     jsonParseValueResult,
			isNull:         jsonIsNullVal,
			isBool:         jsonIsBoolVal,
			isNumber:       jsonIsNumberVal,
			isString:       jsonIsStringVal,
			isArray:        jsonIsArrayVal,
			isObject:       jsonIsObjectVal,
			asBool:         jsonAsBoolResult,
			asNumber:       jsonAsNumberResult,
			asString:       jsonAsStringResult,
			asArray:        jsonAsArrayResult,
			asObject:       jsonAsObjectResult,
			getField:       jsonGetFieldResult,
			getIndex:       jsonGetIndexResult,
		}`, full)
		return true
	case "regex":
		if !g.aliasUsedAsSelector(alias) {
			return false
		}
		g.needRegex = true
		g.emitUseStub(alias, `struct {
			compile func(pattern string) Result[Regex, error]
		}{
			compile: regexCompile,
		}`, full)
		return true
	case "error":
		// std.error calls and qualified literals are lowered directly by
		// expr.go; no package-shaped Go value is needed here. Avoid
		// emitting `var error ...`, which would shadow Go's predeclared
		// error type for any runtime helper that still uses it.
		return true
	case "random":
		g.needRandomRuntime = true
		g.emitUseStub(alias, `struct {
			default_ func() Rng
			seeded   func(seed int64) Rng
		}{
			default_: randomDefault,
			seeded:   randomSeeded,
		}`, full)
		return true
	case "url":
		g.needURLRuntime = true
		g.emitUseStub(alias, `struct {
			parse func(text string) Result[Url, any]
			join  func(base string, relative string) Result[string, any]
		}{
			parse: urlParse,
			join:  urlJoin,
		}`, full)
		return true
	case "encoding":
		if !g.aliasUsedAsSelector(alias) {
			return false
		}
		g.needEncoding = true
		g.needResult = true
		g.emitUseStub(alias, `struct {
			base64 struct {
				encode func(data []byte) string
				decode func(text string) Result[[]byte, any]
				url    struct {
					encode func(data []byte) string
					decode func(text string) Result[[]byte, any]
				}
			}
			hex struct {
				encode func(data []byte) string
				decode func(text string) Result[[]byte, any]
			}
			url struct {
				encode func(text string) string
				decode func(text string) Result[string, any]
			}
		}{
			base64: struct {
				encode func(data []byte) string
				decode func(text string) Result[[]byte, any]
				url    struct {
					encode func(data []byte) string
					decode func(text string) Result[[]byte, any]
				}
			}{
				encode: encodingBase64Encode,
				decode: encodingBase64Decode,
				url: struct {
					encode func(data []byte) string
					decode func(text string) Result[[]byte, any]
				}{
					encode: encodingBase64URLEncode,
					decode: encodingBase64URLDecode,
				},
			},
			hex: struct {
				encode func(data []byte) string
				decode func(text string) Result[[]byte, any]
			}{
				encode: encodingHexEncode,
				decode: encodingHexDecode,
			},
			url: struct {
				encode func(text string) string
				decode func(text string) Result[string, any]
			}{
				encode: encodingURLEncode,
				decode: encodingURLDecode,
			},
		}`, full)
		return true
	case "bytes":
		if !g.aliasUsedAsSelector(alias) {
			return false
		}
		g.needBytesRuntime = true
		g.needResult = true
		g.emitUseStub(alias, `struct {
			from        func(items []byte) []byte
			fromString  func(s string) []byte
			toString    func(b []byte) Result[string, any]
			len         func(b []byte) int
			isEmpty     func(b []byte) bool
			get         func(b []byte, i int) *byte
			slice       func(b []byte, start int, end int) []byte
			equal       func(a []byte, b []byte) bool
			contains    func(b []byte, sub []byte) bool
			startsWith  func(b []byte, prefix []byte) bool
			endsWith    func(b []byte, suffix []byte) bool
			indexOf     func(b []byte, sub []byte) *int
			lastIndexOf func(b []byte, sub []byte) *int
			split       func(b []byte, sep []byte) [][]byte
			join        func(parts [][]byte, sep []byte) []byte
			concat      func(a []byte, b []byte) []byte
			repeat      func(b []byte, n int) []byte
			replace     func(b []byte, old []byte, new []byte) []byte
			replaceAll  func(b []byte, old []byte, new []byte) []byte
			trimLeft    func(b []byte, strip []byte) []byte
			trimRight   func(b []byte, strip []byte) []byte
			trim        func(b []byte, strip []byte) []byte
			trimSpace   func(b []byte) []byte
			toUpper     func(b []byte) []byte
			toLower     func(b []byte) []byte
			toHex       func(b []byte) string
			fromHex     func(s string) Result[[]byte, any]
		}{
			from:        bytesFrom,
			fromString:  bytesFromString,
			toString:    bytesToString,
			len:         bytesLen,
			isEmpty:     bytesIsEmpty,
			get:         bytesGet,
			slice:       bytesSlice,
			equal:       bytesEqual,
			contains:    bytesContains,
			startsWith:  bytesStartsWith,
			endsWith:    bytesEndsWith,
			indexOf:     bytesIndexOf,
			lastIndexOf: bytesLastIndexOf,
			split:       bytesSplit,
			join:        bytesJoin,
			concat:      bytesConcat,
			repeat:      bytesRepeat,
			replace:     bytesReplace,
			replaceAll:  bytesReplaceAll,
			trimLeft:    bytesTrimLeft,
			trimRight:   bytesTrimRight,
			trim:        bytesTrim,
			trimSpace:   bytesTrimSpace,
			toUpper:     bytesToUpper,
			toLower:     bytesToLower,
			toHex:       bytesToHex,
			fromHex:     bytesFromHex,
		}`, full)
		return true
	case "csv":
		if !g.aliasUsedAsSelector(alias) {
			return false
		}
		g.needResult = true
		g.needCsvRuntime = true
		g.use("fmt")
		g.useAs("strings", "_ostyCsvStrings")
		g.emitUseStub(alias, `struct {
			CsvOptions    func(_delimiter rune, _quote rune, _trimSpace bool) _ostyCsvOptions
			encode        func(rows [][]string) string
			encodeWith    func(rows [][]string, opts _ostyCsvOptions) string
			decode        func(text string) Result[[][]string, any]
			decodeHeaders func(text string) Result[[]map[string]string, any]
			decodeWith    func(text string, opts _ostyCsvOptions) Result[[][]string, any]
		}{
			CsvOptions: func(_delimiter rune, _quote rune, _trimSpace bool) _ostyCsvOptions {
				return _ostyCsvOptions{delimiter: _delimiter, quote: _quote, trimSpace: _trimSpace}
			},
			encode: func(rows [][]string) string {
				return _ostyCsvEncodeWith(rows, _ostyCsvOptions{delimiter: ',', quote: '"', trimSpace: false})
			},
			encodeWith: _ostyCsvEncodeWith,
			decode: func(text string) Result[[][]string, any] {
				return _ostyCsvDecodeWith(text, _ostyCsvOptions{delimiter: ',', quote: '"', trimSpace: false})
			},
			decodeHeaders: _ostyCsvDecodeHeaders,
			decodeWith:    _ostyCsvDecodeWith,
		}`, full)
		return true
	}
	return false
}

func (g *gen) emitCsvRuntime(out *bytes.Buffer) {
	out.WriteString(`
type _ostyCsvOptions struct {
	delimiter rune
	quote     rune
	trimSpace bool
}

func _ostyCsvEncodeField(field string, delimiter rune, quote rune) string {
	needsQuote := field == "" ||
		_ostyCsvStrings.ContainsRune(field, delimiter) ||
		_ostyCsvStrings.ContainsRune(field, quote) ||
		_ostyCsvStrings.ContainsAny(field, "\r\n") ||
		field != _ostyCsvStrings.TrimSpace(field)
	if !needsQuote {
		return field
	}
	var b _ostyCsvStrings.Builder
	b.WriteRune(quote)
	for _, r := range field {
		if r == quote {
			b.WriteRune(quote)
		}
		b.WriteRune(r)
	}
	b.WriteRune(quote)
	return b.String()
}

func _ostyCsvEncodeWith(rows [][]string, options _ostyCsvOptions) string {
	var b _ostyCsvStrings.Builder
	for _, row := range rows {
		for fi, field := range row {
			if fi > 0 {
				b.WriteRune(options.delimiter)
			}
			b.WriteString(_ostyCsvEncodeField(field, options.delimiter, options.quote))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func _ostyCsvDecodeWith(text string, options _ostyCsvOptions) Result[[][]string, any] {
	var rows [][]string
	var row []string
	var field _ostyCsvStrings.Builder
	var inQuote, quoted, sawAny, endedRecord bool
	chars := []rune(text)
	length := len(chars)
	i := 0
	for i < length {
		c := chars[i]
		sawAny = true
		if inQuote {
			if c == options.quote {
				if i+1 < length && chars[i+1] == options.quote {
					field.WriteRune(options.quote)
					i++
				} else {
					inQuote = false
					quoted = true
				}
			} else {
				field.WriteRune(c)
			}
			endedRecord = false
			i++
			continue
		}
		if quoted && c != options.delimiter && c != '\r' && c != '\n' {
			return resultErr[[][]string, any](fmt.Errorf("csv: data after closing quote"))
		}
		if c == options.quote {
			if field.Len() == 0 {
				inQuote = true
			} else {
				return resultErr[[][]string, any](fmt.Errorf("csv: bare quote in unquoted field"))
			}
			endedRecord = false
		} else if c == options.delimiter {
			value := field.String()
			if options.trimSpace && !quoted {
				value = _ostyCsvStrings.TrimSpace(value)
			}
			row = append(row, value)
			field.Reset()
			quoted = false
			endedRecord = false
		} else if c == '\r' {
			if i+1 < length && chars[i+1] == '\n' {
				i++
			}
			value := field.String()
			if options.trimSpace && !quoted {
				value = _ostyCsvStrings.TrimSpace(value)
			}
			row = append(row, value)
			field.Reset()
			quoted = false
			rows = append(rows, row)
			row = nil
			endedRecord = true
		} else if c == '\n' {
			value := field.String()
			if options.trimSpace && !quoted {
				value = _ostyCsvStrings.TrimSpace(value)
			}
			row = append(row, value)
			field.Reset()
			quoted = false
			rows = append(rows, row)
			row = nil
			endedRecord = true
		} else {
			field.WriteRune(c)
			endedRecord = false
		}
		i++
	}
	if inQuote {
		return resultErr[[][]string, any](fmt.Errorf("csv: unterminated quoted field"))
	}
	if sawAny && !endedRecord {
		value := field.String()
		if options.trimSpace && !quoted {
			value = _ostyCsvStrings.TrimSpace(value)
		}
		row = append(row, value)
		rows = append(rows, row)
	}
	return resultOk[[][]string, any](rows)
}

func _ostyCsvDecodeHeaders(text string) Result[[]map[string]string, any] {
	rowsRes := _ostyCsvDecodeWith(text, _ostyCsvOptions{delimiter: ',', quote: '"', trimSpace: false})
	if !rowsRes.IsOk {
		return resultErr[[]map[string]string, any](rowsRes.Error)
	}
	rows := rowsRes.Value
	if len(rows) == 0 {
		return resultOk[[]map[string]string, any]([]map[string]string{})
	}
	headers := rows[0]
	out := make([]map[string]string, 0, len(rows)-1)
	for _, row := range rows[1:] {
		record := map[string]string{}
		for i, header := range headers {
			if i < len(row) {
				record[header] = row[i]
			} else {
				record[header] = ""
			}
		}
		out = append(out, record)
	}
	return resultOk[[]map[string]string, any](out)
}
`)
}

func (g *gen) stdFSImportAliases() (string, string) {
	if g.fsOSAlias == "" {
		g.fsOSAlias = g.freshFileIdent("_ostyStdFSOS")
	}
	if g.fsUTF8Alias == "" {
		g.fsUTF8Alias = g.freshFileIdent("_ostyStdFSUTF8")
	}
	return g.fsOSAlias, g.fsUTF8Alias
}

func (g *gen) freshFileIdent(base string) string {
	candidate := base
	for i := 2; g.fileIdentUsed(candidate); i++ {
		candidate = fmt.Sprintf("%s%d", base, i)
	}
	return candidate
}

func (g *gen) fileIdentUsed(name string) bool {
	for _, alias := range g.imports {
		if alias == name {
			return true
		}
	}
	for _, u := range g.file.Uses {
		alias := useDeclAlias(u)
		if mangleIdent(alias) == name {
			return true
		}
	}
	for _, d := range g.file.Decls {
		switch d := d.(type) {
		case *ast.FnDecl:
			if mangleIdent(d.Name) == name {
				return true
			}
		case *ast.LetDecl:
			if mangleIdent(d.Name) == name {
				return true
			}
		case *ast.StructDecl:
			if mangleIdent(d.Name) == name {
				return true
			}
		case *ast.EnumDecl:
			if mangleIdent(d.Name) == name {
				return true
			}
		case *ast.InterfaceDecl:
			if mangleIdent(d.Name) == name {
				return true
			}
		case *ast.TypeAliasDecl:
			if mangleIdent(d.Name) == name {
				return true
			}
		}
	}
	return false
}

func (g *gen) emitUseStub(alias, stub, full string) {
	g.body.writef("\nvar %s = %s // stub for `use %s`\n",
		mangleIdent(alias), stub, full)
}

func (g *gen) aliasUsedAsSelector(alias string) bool {
	for _, d := range g.file.Decls {
		if g.declUsesAliasSelector(d, alias) {
			return true
		}
	}
	for _, s := range g.file.Stmts {
		if g.stmtUsesAliasSelector(s, alias) {
			return true
		}
	}
	return false
}

func (g *gen) declUsesAliasSelector(d ast.Decl, alias string) bool {
	switch d := d.(type) {
	case *ast.FnDecl:
		return d.Body != nil && g.stmtUsesAliasSelector(d.Body, alias)
	case *ast.LetDecl:
		return g.exprUsesAliasSelector(d.Value, alias)
	case *ast.StructDecl:
		for _, m := range d.Methods {
			if m.Body != nil && g.stmtUsesAliasSelector(m.Body, alias) {
				return true
			}
		}
	case *ast.EnumDecl:
		for _, m := range d.Methods {
			if m.Body != nil && g.stmtUsesAliasSelector(m.Body, alias) {
				return true
			}
		}
	}
	return false
}

func (g *gen) stmtUsesAliasSelector(s ast.Stmt, alias string) bool {
	switch s := s.(type) {
	case *ast.Block:
		for _, st := range s.Stmts {
			if g.stmtUsesAliasSelector(st, alias) {
				return true
			}
		}
	case *ast.LetStmt:
		return g.exprUsesAliasSelector(s.Value, alias)
	case *ast.ExprStmt:
		return g.exprUsesAliasSelector(s.X, alias)
	case *ast.AssignStmt:
		if g.exprUsesAliasSelector(s.Value, alias) {
			return true
		}
		for _, t := range s.Targets {
			if g.exprUsesAliasSelector(t, alias) {
				return true
			}
		}
	case *ast.ReturnStmt:
		return g.exprUsesAliasSelector(s.Value, alias)
	case *ast.ForStmt:
		return g.exprUsesAliasSelector(s.Iter, alias) ||
			(s.Body != nil && g.stmtUsesAliasSelector(s.Body, alias))
	case *ast.DeferStmt:
		return g.exprUsesAliasSelector(s.X, alias)
	case *ast.ChanSendStmt:
		return g.exprUsesAliasSelector(s.Channel, alias) ||
			g.exprUsesAliasSelector(s.Value, alias)
	}
	return false
}

func (g *gen) exprUsesAliasSelector(e ast.Expr, alias string) bool {
	switch e := e.(type) {
	case nil:
		return false
	case *ast.FieldExpr:
		if id, ok := e.X.(*ast.Ident); ok && id.Name == alias {
			return true
		}
		return g.exprUsesAliasSelector(e.X, alias)
	case *ast.CallExpr:
		if g.exprUsesAliasSelector(e.Fn, alias) {
			return true
		}
		for _, a := range e.Args {
			if g.exprUsesAliasSelector(a.Value, alias) {
				return true
			}
		}
	case *ast.StringLit:
		for _, p := range e.Parts {
			if !p.IsLit && g.exprUsesAliasSelector(p.Expr, alias) {
				return true
			}
		}
	case *ast.ParenExpr:
		return g.exprUsesAliasSelector(e.X, alias)
	case *ast.UnaryExpr:
		return g.exprUsesAliasSelector(e.X, alias)
	case *ast.BinaryExpr:
		return g.exprUsesAliasSelector(e.Left, alias) ||
			g.exprUsesAliasSelector(e.Right, alias)
	case *ast.QuestionExpr:
		return g.exprUsesAliasSelector(e.X, alias)
	case *ast.IndexExpr:
		return g.exprUsesAliasSelector(e.X, alias) ||
			g.exprUsesAliasSelector(e.Index, alias)
	case *ast.TurbofishExpr:
		return g.exprUsesAliasSelector(e.Base, alias)
	case *ast.RangeExpr:
		return g.exprUsesAliasSelector(e.Start, alias) ||
			g.exprUsesAliasSelector(e.Stop, alias)
	case *ast.TupleExpr:
		for _, x := range e.Elems {
			if g.exprUsesAliasSelector(x, alias) {
				return true
			}
		}
	case *ast.ListExpr:
		for _, x := range e.Elems {
			if g.exprUsesAliasSelector(x, alias) {
				return true
			}
		}
	case *ast.MapExpr:
		for _, ent := range e.Entries {
			if g.exprUsesAliasSelector(ent.Key, alias) ||
				g.exprUsesAliasSelector(ent.Value, alias) {
				return true
			}
		}
	case *ast.StructLit:
		if g.exprUsesAliasSelector(e.Type, alias) ||
			g.exprUsesAliasSelector(e.Spread, alias) {
			return true
		}
		for _, f := range e.Fields {
			if g.exprUsesAliasSelector(f.Value, alias) {
				return true
			}
		}
	case *ast.IfExpr:
		return g.exprUsesAliasSelector(e.Cond, alias) ||
			(e.Then != nil && g.stmtUsesAliasSelector(e.Then, alias)) ||
			g.exprUsesAliasSelector(e.Else, alias)
	case *ast.MatchExpr:
		if g.exprUsesAliasSelector(e.Scrutinee, alias) {
			return true
		}
		for _, a := range e.Arms {
			if g.exprUsesAliasSelector(a.Guard, alias) ||
				g.exprUsesAliasSelector(a.Body, alias) {
				return true
			}
		}
	case *ast.ClosureExpr:
		return g.exprUsesAliasSelector(e.Body, alias)
	case *ast.Block:
		return g.stmtUsesAliasSelector(e, alias)
	}
	return false
}

// stdlibBridge maps a small subset of Osty stdlib module paths to Go
// packages for fixture compatibility. This is not the stdlib runtime;
// the embedded stdlib under internal/stdlib is a signature-stub surface.
// Returns "" when no bridge is available (caller falls back to a stub).
func stdlibBridge(path []string) string {
	if len(path) < 2 || path[0] != "std" {
		return ""
	}
	switch path[1] {
	case "os":
		return "os"
	case "io":
		return "io"
	case "time":
		return "time"
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
			snapshot  func(...any)
		}{
			assert:    func(...any) {},
			assertEq:  func(...any) {},
			context:   func(...any) {},
			benchmark: func(...any) {},
			snapshot:  func(...any) {},
		}`
	case "thread":
		return `struct {
			collectAll func(...any) any
			race       func(...any) any
			chan_      func(...any) any
			select_    func(...any) any
			isCancelled func() bool
		}{
			collectAll: func(...any) any { return nil },
			race:       func(...any) any { return nil },
			chan_:      func(...any) any { return nil },
			select_:    func(...any) any { return nil },
			isCancelled: func() bool { return false },
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
	if i := strings.LastIndexByte(p, '.'); i >= 0 {
		return p[i+1:]
	}
	return p
}

func runtimeFFIGoImport(path string) string {
	switch path {
	case "runtime.strings":
		return "strings"
	case "runtime.path.filepath":
		return "path/filepath"
	case "runtime.net.http":
		return "net/http"
	case "runtime.selfhost.astbridge":
		return "github.com/osty/osty/internal/selfhost/astbridge"
	default:
		return path
	}
}

func useFullPath(u *ast.UseDecl) string {
	if u == nil {
		return ""
	}
	if u.RawPath != "" {
		return u.RawPath
	}
	return strings.Join(u.Path, ".")
}

func useDeclAlias(u *ast.UseDecl) string {
	if u == nil {
		return ""
	}
	if u.Alias != "" {
		return u.Alias
	}
	if u.IsFFI() {
		return lastPathComponent(u.FFIPath())
	}
	if u.RawPath != "" && strings.Contains(u.RawPath, "/") {
		return lastPathComponent(u.RawPath)
	}
	if len(u.Path) > 0 {
		return lastPathComponent(u.Path[len(u.Path)-1])
	}
	return ""
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
		if es, ok := last.(*ast.ExprStmt); ok && !g.isVoidExpr(es.X) {
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

// isVoidExpr extends isVoidCall with type-checker and resolver
// awareness. A call is void when any of these hold:
//
//   - The checker typed it as Unit (user fn with no return type, or a
//     stdlib module that was also checked).
//   - It's `pkg.fn(...)` where `pkg` is a resolved SymPackage and the
//     target fn's declaration has no return type annotation. This is
//     the fallback path for stdlib packages that the driver loaded and
//     resolved but didn't type-check (e.g. `osty test` wires up just
//     the user package through the checker, leaving `std.testing`
//     resolved-only — `testing.assertEq` still needs to be recognised
//     as void so a trailing call in a closure doesn't get wrapped in
//     an invalid `return`).
//   - It's a user-declared FnCall (`foo(args)`) whose resolved symbol's
//     FnDecl has no return type — same problem as above but for in-package
//     calls.
func (g *gen) isVoidExpr(e ast.Expr) bool {
	if isVoidCall(e) {
		return true
	}
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	if t := g.typeOf(call); t != nil && types.IsUnit(t) {
		return true
	}
	if f, ok := call.Fn.(*ast.FieldExpr); ok {
		if id, ok := f.X.(*ast.Ident); ok && id.Name == "testing" {
			switch f.Name {
			case "assert", "assertEq", "assertNe", "context", "benchmark", "snapshot":
				return true
			}
		}
	}
	// Callee introspection: walk Fn → find the referenced FnDecl, check
	// that it has no declared return type.
	fnDecl := g.resolvedCalleeFnDecl(call.Fn)
	if fnDecl != nil && fnDecl.ReturnType == nil {
		return true
	}
	return false
}

// resolvedCalleeFnDecl returns the FnDecl that a call expression's Fn
// resolves to, consulting both the in-package resolver and (for package
// calls) the imported package's PkgScope. Returns nil when the callee
// is a closure, a generic turbofish, or otherwise can't be statically
// attributed to a single FnDecl.
func (g *gen) resolvedCalleeFnDecl(fn ast.Expr) *ast.FnDecl {
	switch f := fn.(type) {
	case *ast.Ident:
		if sym := g.symbolFor(f); sym != nil {
			if d, ok := sym.Decl.(*ast.FnDecl); ok {
				return d
			}
		}
	case *ast.FieldExpr:
		id, ok := f.X.(*ast.Ident)
		if !ok {
			return nil
		}
		sym := g.symbolFor(id)
		if sym == nil || sym.Package == nil || sym.Package.PkgScope == nil {
			return nil
		}
		tgt := sym.Package.PkgScope.LookupLocal(f.Name)
		if tgt == nil {
			return nil
		}
		if d, ok := tgt.Decl.(*ast.FnDecl); ok {
			return d
		}
	}
	return nil
}
