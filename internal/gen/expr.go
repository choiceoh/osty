package gen

import (
	"strconv"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/token"
	"github.com/osty/osty/internal/types"
)

// emitExpr writes an expression inline (no trailing newline).
func (g *gen) emitExpr(e ast.Expr) {
	switch e := e.(type) {
	case *ast.Ident:
		g.emitIdent(e)
	case *ast.IntLit:
		g.body.write(e.Text)
	case *ast.FloatLit:
		g.body.write(e.Text)
	case *ast.BoolLit:
		if e.Value {
			g.body.write("true")
		} else {
			g.body.write("false")
		}
	case *ast.CharLit:
		g.body.write(strconv.QuoteRune(e.Value))
	case *ast.ByteLit:
		g.body.writef("byte(%d)", e.Value)
	case *ast.StringLit:
		g.emitStringLit(e)
	case *ast.ParenExpr:
		g.body.write("(")
		g.emitExpr(e.X)
		g.body.write(")")
	case *ast.UnaryExpr:
		g.emitUnary(e)
	case *ast.BinaryExpr:
		g.emitBinary(e)
	case *ast.CallExpr:
		g.emitCall(e)
	case *ast.FieldExpr:
		g.emitField(e)
	case *ast.IndexExpr:
		g.emitExpr(e.X)
		g.body.write("[")
		g.emitExpr(e.Index)
		g.body.write("]")
	case *ast.RangeExpr:
		g.emitRangeExpr(e)
	case *ast.ListExpr:
		g.emitList(e)
	case *ast.MapExpr:
		g.emitMap(e)
	case *ast.TupleExpr:
		g.emitTupleExpr(e)
	case *ast.IfExpr:
		g.emitIfExpr(e)
	case *ast.Block:
		g.emitBlockAsExpr(e)
	case *ast.MatchExpr:
		g.emitMatch(e)
	case *ast.ClosureExpr:
		g.emitClosure(e)
	case *ast.StructLit:
		g.emitStructLit(e)
	case *ast.TurbofishExpr:
		g.emitExpr(e.Base)
	case *ast.QuestionExpr:
		g.emitQuestion(e)
	default:
		g.body.writef("/* TODO: expr %T */", e)
	}
}

// emitIdent writes an identifier reference. Osty prelude builtins with
// no Go equivalent are rewritten inline (true/false handled by BoolLit;
// None becomes a typed nil; Some/Ok/Err are emitted as calls at the
// CallExpr layer). Bare enum variants (no payload) are emitted as
// zero-value struct literals here; tuple variants flow through
// emitCall instead.
func (g *gen) emitIdent(id *ast.Ident) {
	switch id.Name {
	case "true", "false":
		g.body.write(id.Name)
		return
	case "None":
		g.body.write("nil")
		return
	case "Self":
		if g.selfType != "" {
			g.body.write(g.selfType)
			return
		}
	}
	// Bare variant reference (no call) — emit a zero-value struct
	// wrapped in the enum interface conversion so type assertions at
	// use sites work (a bare struct value cannot be the scrutinee of a
	// `v.(Enum_Variant)` type assertion).
	if owner, ok := g.variantOwner[id.Name]; ok {
		g.body.writef("%s(&%s_%s{})", owner, owner, id.Name)
		return
	}
	g.body.write(mangleIdent(id.Name))
}

// emitStructLit writes a struct literal. `Self { ... }` is rewritten
// to the enclosing type while emitting a method body.
//
// Functional-update form `Point { x: 1, ..other }` is lowered to an
// IIFE that seeds a temp from the spread source and overrides the
// explicit fields, since Go has no direct equivalent:
//
//	func() Point { _r := other; _r.x = 1; return _r }()
//
// This preserves the Osty semantics (overrides win, untouched fields
// come from the spread value) without needing the gen to know every
// field name on the struct type.
func (g *gen) emitStructLit(s *ast.StructLit) {
	var typeName string
	switch t := s.Type.(type) {
	case *ast.Ident:
		if t.Name == "Self" && g.selfType != "" {
			typeName = g.selfType
		} else {
			typeName = t.Name
		}
	case *ast.FieldExpr:
		if name, ok := g.stdlibStructLitType(t); ok {
			typeName = name
		} else {
			// Qualified type (pkg.Type) — Phase 5.
			g.emitExpr(s.Type)
		}
	default:
		g.emitExpr(s.Type)
	}
	refStruct := false
	if n, ok := g.typeOf(s).(*types.Named); ok && g.isReferenceStructSym(n.Sym) {
		refStruct = true
		if got := g.goType(n); strings.HasPrefix(got, "*") {
			typeName = strings.TrimPrefix(got, "*")
		}
	} else if id, ok := s.Type.(*ast.Ident); ok && g.structTypes[id.Name] {
		refStruct = true
	}
	if typeName != "" && g.structTypes[typeName] {
		refStruct = true
	}
	if s.Spread != nil && typeName != "" {
		g.emitStructSpreadLit(s, typeName, refStruct)
		return
	}
	if typeName != "" {
		if refStruct {
			g.body.write("&")
		}
		g.body.write(typeName)
	}
	g.body.write("{")
	for i, f := range s.Fields {
		if i > 0 {
			g.body.write(", ")
		}
		name := structLitFieldName(typeName, f.Name)
		g.body.writef("%s: ", name)
		if f.Value == nil {
			// Shorthand `Point { name }` → `Point{name: name}`.
			g.body.write(mangleIdent(f.Name))
		} else {
			g.emitExpr(f.Value)
		}
	}
	if s.Spread != nil {
		// Qualified-type path with a spread is uncommon enough that we
		// leave a visible TODO rather than guess at the emitted name.
		g.body.write(" /* TODO(phase3): ..spread on qualified type */ ")
	}
	g.body.write("}")
}

// emitStructSpreadLit lowers `Type { f: v, ..base }` to an IIFE that
// copies base then assigns each explicit field, matching Osty's
// "overrides win" semantics (§3.4).
func (g *gen) emitStructSpreadLit(s *ast.StructLit, typeName string, refStruct bool) {
	tmp := g.freshVar("_r")
	retType := typeName
	if refStruct {
		retType = "*" + typeName
	}
	g.body.writef("func() %s { %s := ", retType, tmp)
	if refStruct {
		g.body.write("*")
	}
	g.emitExpr(s.Spread)
	g.body.write("; ")
	for _, f := range s.Fields {
		g.body.writef("%s.%s = ", tmp, structLitFieldName(typeName, f.Name))
		if f.Value == nil {
			g.body.write(mangleIdent(f.Name))
		} else {
			g.emitExpr(f.Value)
		}
		g.body.write("; ")
	}
	if refStruct {
		g.body.writef("return &%s }()", tmp)
		return
	}
	g.body.writef("return %s }()", tmp)
}

// emitStringLit writes a string literal.
//
// Plain strings (no interpolation) are emitted as Go string literals
// via strconv.Quote. Interpolated strings are rewritten to a
// `fmt.Sprintf` call: each literal segment becomes a quoted run, each
// expression segment is first routed through the Osty toString bridge.
func (g *gen) emitStringLit(s *ast.StringLit) {
	// Fast path: no interpolation.
	plain := true
	for _, p := range s.Parts {
		if !p.IsLit {
			plain = false
			break
		}
	}
	if plain {
		var b strings.Builder
		for _, p := range s.Parts {
			b.WriteString(p.Lit)
		}
		g.body.write(strconv.Quote(b.String()))
		return
	}

	// Interpolated: fmt.Sprintf("...", ostyToString(args)...).
	g.use("fmt")
	g.needStringRuntime = true
	var format strings.Builder
	var args []ast.Expr
	for _, p := range s.Parts {
		if p.IsLit {
			// Escape `%` in literal runs so fmt treats them as literal.
			format.WriteString(strings.ReplaceAll(p.Lit, "%", "%%"))
		} else {
			format.WriteString("%s")
			args = append(args, p.Expr)
		}
	}
	g.body.writef("fmt.Sprintf(%s", strconv.Quote(format.String()))
	for _, a := range args {
		g.body.write(", ostyToString(")
		g.emitExpr(a)
		g.body.write(")")
	}
	g.body.write(")")
}

// emitUnary writes `op X`.
func (g *gen) emitUnary(u *ast.UnaryExpr) {
	g.body.write(unaryOp(u.Op))
	g.emitExpr(u.X)
}

func unaryOp(k token.Kind) string {
	switch k {
	case token.MINUS:
		return "-"
	case token.PLUS:
		return "+"
	case token.NOT:
		return "!"
	case token.BITNOT:
		return "^"
	}
	return "/*?*/"
}

// emitBinary writes `a op b` with the Osty operator mapped to Go.
//
// The `??` null-coalescing operator has no native Go equivalent; we
// rewrite it to an IIFE that tests nil and substitutes the default.
// Since that requires knowing the value type, we emit a conservative
// pattern that works for pointer operands (which is how Phase 1 models
// Option<T>).
func (g *gen) emitBinary(b *ast.BinaryExpr) {
	// Coalescing is the only special case.
	if b.Op == token.QQ {
		g.emitCoalesce(b)
		return
	}
	if (b.Op == token.EQ || b.Op == token.NEQ) && g.needsOstyEqual(b.Left, b.Right) {
		if b.Op == token.NEQ {
			g.body.write("!")
		}
		g.needEqualRuntime = true
		g.body.write("ostyEqual(")
		g.emitExpr(b.Left)
		g.body.write(", ")
		g.emitExpr(b.Right)
		g.body.write(")")
		return
	}
	op := binaryOp(b.Op)
	g.emitExpr(b.Left)
	g.body.writef(" %s ", op)
	g.emitExpr(b.Right)
}

func (g *gen) needsOstyEqual(left, right ast.Expr) bool {
	return needsOstyEqualType(g.typeOf(left)) || needsOstyEqualType(g.typeOf(right))
}

func needsOstyEqualType(t types.Type) bool {
	switch t := t.(type) {
	case nil:
		return false
	case *types.Primitive:
		return t.Kind == types.PBytes
	case *types.Untyped:
		return false
	default:
		return true
	}
}

func binaryOp(k token.Kind) string {
	switch k {
	case token.PLUS:
		return "+"
	case token.MINUS:
		return "-"
	case token.STAR:
		return "*"
	case token.SLASH:
		return "/"
	case token.PERCENT:
		return "%"
	case token.EQ:
		return "=="
	case token.NEQ:
		return "!="
	case token.LT:
		return "<"
	case token.GT:
		return ">"
	case token.LEQ:
		return "<="
	case token.GEQ:
		return ">="
	case token.AND:
		return "&&"
	case token.OR:
		return "||"
	case token.BITAND:
		return "&"
	case token.BITOR:
		return "|"
	case token.BITXOR:
		return "^"
	case token.SHL:
		return "<<"
	case token.SHR:
		return ">>"
	}
	return "/*?*/"
}

// emitCoalesce lowers `a ?? b`. When the checker tells us `a` is an
// Optional (*T), we emit a branchy lookup. When the checker lost the
// type (e.g. calls into unchecked stdlib) we fall back to a runtime
// nil-probe via reflect-free Go: `func() T { if v := a; v != nil { return *v }; return b }()`.
// When `a` is not optional, `??` is a no-op — the right operand is
// unreachable by construction, so we just emit the left.
func (g *gen) emitCoalesce(b *ast.BinaryExpr) {
	lt := g.typeOf(b.Left)
	if opt, ok := lt.(*types.Optional); ok {
		g.body.write("func() ")
		inner := g.goType(opt.Inner)
		g.body.writef("%s { if v := ", inner)
		g.emitExpr(b.Left)
		g.body.writef("; v != nil { return *v }; return ")
		g.emitExpr(b.Right)
		g.body.write(" }()")
		return
	}
	// Non-optional left: `a ?? b` ≡ `a` (the RHS is dead). The checker
	// already issued a hint-level diagnostic when this path is reachable
	// from user source; just emit the LHS.
	g.emitExpr(b.Left)
}

// emitCall writes a function call, applying special rewrites for
// prelude intrinsics (println, print, ...), builtin variant
// constructors (Some, Ok, Err), user enum variant construction
// (`Circle(3.14)` → `Shape_Circle{F0: 3.14}`), enum-method dispatch
// (`shape.area()` → `Shape_area(shape)`), and static/associated
// function calls (`User.new("a")` → `User_new("a")`).
func (g *gen) emitCall(c *ast.CallExpr) {
	// Monomorphized generic-fn call. callMonoName consults the current
	// substEnv so an inner call inside a generic body (e.g. `id(y)`
	// appearing in `wrap<U>`) resolves transitively to the concrete
	// type of the enclosing monomorph (`id_int` when emitting
	// wrap_int). The rewrite runs before the construct-specific paths
	// so the mangled name wins over any source-level collision with
	// a variant constructor etc.
	if mangled := g.callMonoName(c); mangled != "" {
		g.body.write(mangled)
		g.body.write("(")
		for i, a := range c.Args {
			if i > 0 {
				g.body.write(", ")
			}
			if a.Name != "" {
				g.body.writef("/* %s = */ ", a.Name)
			}
			g.emitExpr(a.Value)
		}
		g.body.write(")")
		return
	}
	if id, ok := c.Fn.(*ast.Ident); ok {
		if g.emitBuiltinCall(id.Name, c.Args, c) {
			return
		}
		// User-defined variant construction: Fn is a bare variant name.
		if owner, ok := g.variantOwner[id.Name]; ok {
			g.emitVariantCtor(owner, id.Name, c.Args)
			return
		}
	}
	if g.emitStdlibJSONCall(c) {
		return
	}
	if f, ok := c.Fn.(*ast.FieldExpr); ok {
		if g.emitQualifiedOptionCall(c, f) {
			return
		}
		if g.emitStdlibErrorCall(c, f) {
			return
		}
		if g.emitErrorMethodCall(c, f) {
			return
		}
		if g.emitStdlibRefCall(c, f) {
			return
		}
		if g.emitStdlibEncodingCall(c, f) {
			return
		}
		if g.emitStdlibEnvCall(c, f) {
			return
		}
		if g.emitStdlibCompressCall(c, f) {
			return
		}
		if g.emitStdlibCryptoCall(c, f) {
			return
		}
		if g.emitStdlibUUIDCall(c, f) {
			return
		}
		if g.emitStdlibRegexCall(c, f) {
			return
		}
		if g.emitStdlibMathCall(c, f) {
			return
		}
		if g.emitThreadSelect(c, f) {
			return
		}
		if g.emitThreadCall(c, f) {
			return
		}
		if g.emitErrorMethodCall(c, f) {
			return
		}
		if g.emitResultMethodCall(c, f) {
			return
		}
		if g.emitOptionalMethodCall(c, f) {
			return
		}
		if g.emitConcurrencyMethod(c, f) {
			return
		}
		if g.emitListPushCall(c, f) {
			return
		}
		if g.emitRandomGenericMethod(c, f) {
			return
		}
		if g.emitCollectionMethod(c, f) {
			return
		}
		if g.emitStaticCall(f, c.Args) {
			return
		}
		if g.emitEnumMethodCall(f, c.Args) {
			return
		}
	}
	if tf, ok := c.Fn.(*ast.TurbofishExpr); ok {
		if g.emitErrorDowncastCall(c, tf) {
			return
		}
		if g.emitTurbofishCall(c, tf) {
			return
		}
	}
	g.emitExpr(c.Fn)
	g.body.write("(")
	for i, a := range c.Args {
		if i > 0 {
			g.body.write(", ")
		}
		if a.Name != "" {
			// Keyword arg: Phase 3 will desugar by matching against
			// the resolved parameter order. For now emit positional
			// with a comment so the mismatch is visible.
			g.body.writef("/* %s = */ ", a.Name)
		}
		g.emitExpr(a.Value)
	}
	g.body.write(")")
}

// emitListPushCall lowers `xs.push(v)` to an append-backed mutation. The
// checker already verifies the receiver is List<T> and the argument matches T;
// this keeps generated Go in sync with that accepted stdlib surface.
func (g *gen) emitListPushCall(c *ast.CallExpr, f *ast.FieldExpr) bool {
	if f.Name != "push" || len(c.Args) != 1 {
		return false
	}
	n, ok := g.typeOf(f.X).(*types.Named)
	if !ok || n.Sym == nil || n.Sym.Name != "List" || len(n.Args) != 1 {
		return false
	}
	g.body.write("func() struct{} { ")
	g.emitExpr(f.X)
	g.body.write(" = append(")
	g.emitExpr(f.X)
	g.body.write(", ")
	g.emitExprAsType(c.Args[0].Value, n.Args[0])
	g.body.write("); return struct{}{} }()")
	return true
}

func (g *gen) emitStdlibJSONCall(c *ast.CallExpr) bool {
	base := c.Fn
	var explicit []ast.Type
	if tf, ok := base.(*ast.TurbofishExpr); ok {
		base = tf.Base
		explicit = tf.Args
	}
	id, parts, ok := stdlibFieldChain(base)
	if !ok || id == nil || len(parts) != 1 || !g.isStdlibPackageAlias(id, "json") {
		return false
	}
	if len(c.Args) != 1 {
		return false
	}
	switch parts[0] {
	case "encode":
		g.body.write("jsonEncode(")
		g.emitExpr(c.Args[0].Value)
		g.body.write(")")
		return true
	case "stringify":
		g.body.write("jsonStringify(")
		g.emitExpr(c.Args[0].Value)
		g.body.write(")")
		return true
	case "decode", "parse":
		g.body.writef("jsonDecode[%s](", g.jsonDecodeTargetGo(c, explicit))
		g.emitExpr(c.Args[0].Value)
		g.body.write(")")
		return true
	}
	return false
}

func (g *gen) jsonDecodeTargetGo(c *ast.CallExpr, explicit []ast.Type) string {
	if len(explicit) == 1 {
		return g.goTypeExpr(explicit[0])
	}
	if n, ok := types.AsNamedByName(g.typeOf(c), "Result"); ok && len(n.Args) == 2 {
		return g.goType(n.Args[0])
	}
	return "any"
}

func (g *gen) emitStdlibErrorCall(c *ast.CallExpr, f *ast.FieldExpr) bool {
	if f.Name == "new" && g.isPreludeErrorTypeExpr(f.X) && len(c.Args) == 1 {
		g.emitErrorNew(c.Args)
		return true
	}
	parts, ok := g.stdlibCallPath(c, "error")
	if !ok || len(c.Args) != 1 {
		return false
	}
	switch strings.Join(parts, ".") {
	case "new", "Error.new":
		g.emitErrorNew(c.Args)
		return true
	}
	return false
}

func (g *gen) emitErrorNew(args []*ast.Arg) {
	g.needErrorRuntime = true
	g.body.write("ostyErrorNew(")
	g.emitExpr(args[0].Value)
	g.body.write(")")
}

func (g *gen) emitErrorMethodCall(c *ast.CallExpr, f *ast.FieldExpr) bool {
	if len(c.Args) != 0 || !isErrorType(g.typeOf(f.X)) {
		return false
	}
	switch f.Name {
	case "message":
		g.needErrorRuntime = true
		g.body.write("ostyErrorMessage(")
		g.emitExpr(f.X)
		g.body.write(")")
		return true
	case "source":
		g.needErrorRuntime = true
		g.body.write("ostyErrorSource(")
		g.emitExpr(f.X)
		g.body.write(")")
		return true
	}
	return false
}

func (g *gen) emitErrorDowncastCall(c *ast.CallExpr, tf *ast.TurbofishExpr) bool {
	f, ok := tf.Base.(*ast.FieldExpr)
	if !ok || f.Name != "downcast" || len(tf.Args) != 1 || len(c.Args) != 0 || !isErrorType(g.typeOf(f.X)) {
		return false
	}
	g.needErrorRuntime = true
	g.body.writef("ostyErrorDowncast[%s](", g.goTypeExpr(tf.Args[0]))
	g.emitExpr(f.X)
	g.body.write(")")
	return true
}

func (g *gen) isPreludeErrorTypeExpr(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	if !ok || id.Name != "Error" {
		return false
	}
	sym := g.symbolFor(id)
	return sym != nil && sym.Kind == resolve.SymBuiltin && sym.Name == "Error"
}

func isErrorType(t types.Type) bool {
	n, ok := t.(*types.Named)
	return ok && n.Sym != nil && n.Sym.Name == "Error"
}

func (g *gen) emitStdlibRefCall(c *ast.CallExpr, f *ast.FieldExpr) bool {
	id, ok := f.X.(*ast.Ident)
	if !ok || !g.isStdlibPackageAlias(id, "ref") || f.Name != "same" || len(c.Args) != 2 {
		return false
	}
	if t := g.typeOf(c.Args[0].Value); hasValueSemantics(t) {
		g.body.write("false")
		return true
	}
	g.needRefRuntime = true
	g.body.write("refSame(")
	g.emitExpr(c.Args[0].Value)
	g.body.write(", ")
	g.emitExpr(c.Args[1].Value)
	g.body.write(")")
	return true
}

func hasValueSemantics(t types.Type) bool {
	switch t.(type) {
	case *types.Primitive, *types.Untyped, *types.Tuple:
		return true
	}
	return false
}

func (g *gen) emitStdlibEncodingCall(c *ast.CallExpr, _ *ast.FieldExpr) bool {
	// Pure Osty implementation — delegate to Go helpers that mirror
	// the Osty algorithm.  The call-path matching still applies so
	// that we inject the right runtime; only the runtime bodies differ
	// from the previous hardcoded Go-stdlib version.
	parts, ok := g.stdlibCallPath(c, "encoding")
	if !ok {
		return false
	}
	var helper string
	switch strings.Join(parts, ".") {
	case "base64.encode":
		if len(c.Args) == 1 {
			helper = "encodingBase64Encode"
		}
	case "base64.decode":
		if len(c.Args) == 1 {
			helper = "encodingBase64Decode"
		}
	case "base64.url.encode":
		if len(c.Args) == 1 {
			helper = "encodingBase64URLEncode"
		}
	case "base64.url.decode":
		if len(c.Args) == 1 {
			helper = "encodingBase64URLDecode"
		}
	case "hex.encode":
		if len(c.Args) == 1 {
			helper = "encodingHexEncode"
		}
	case "hex.decode":
		if len(c.Args) == 1 {
			helper = "encodingHexDecode"
		}
	case "url.encode":
		if len(c.Args) == 1 {
			helper = "encodingURLEncode"
		}
	case "url.decode":
		if len(c.Args) == 1 {
			helper = "encodingURLDecode"
		}
	}
	if helper == "" {
		return false
	}
	g.needEncoding = true
	if strings.HasSuffix(helper, "Decode") {
		g.needResult = true
	}
	g.emitStdlibHelperCall(helper, c.Args)
	return true
}

func (g *gen) emitStdlibEnvCall(c *ast.CallExpr, _ *ast.FieldExpr) bool {
	parts, ok := g.stdlibCallPath(c, "env")
	if !ok || len(parts) != 1 {
		return false
	}
	var helper string
	switch parts[0] {
	case "args":
		if len(c.Args) == 0 {
			helper = "envArgs"
		}
	case "get":
		if len(c.Args) == 1 {
			helper = "envGet"
		}
	case "require":
		if len(c.Args) == 1 {
			helper = "envRequire"
		}
	case "set":
		if len(c.Args) == 2 {
			helper = "envSet"
		}
	case "unset":
		if len(c.Args) == 1 {
			helper = "envUnset"
		}
	case "vars":
		if len(c.Args) == 0 {
			helper = "envVars"
		}
	case "currentDir":
		if len(c.Args) == 0 {
			helper = "envCurrentDir"
		}
	case "setCurrentDir":
		if len(c.Args) == 1 {
			helper = "envSetCurrentDir"
		}
	}
	if helper == "" {
		return false
	}
	g.needEnv = true
	switch helper {
	case "envRequire", "envSet", "envUnset", "envCurrentDir", "envSetCurrentDir":
		g.needResult = true
	}
	g.emitStdlibHelperCall(helper, c.Args)
	return true
}

func (g *gen) emitStdlibCompressCall(c *ast.CallExpr, _ *ast.FieldExpr) bool {
	parts, ok := g.stdlibCallPath(c, "compress")
	if !ok {
		return false
	}
	var helper string
	switch strings.Join(parts, ".") {
	case "gzip.encode":
		if len(c.Args) == 1 {
			helper = "compressGzipEncode"
		}
	case "gzip.decode":
		if len(c.Args) == 1 {
			helper = "compressGzipDecode"
		}
	}
	if helper == "" {
		return false
	}
	g.needCompress = true
	if helper == "compressGzipDecode" {
		g.needResult = true
	}
	g.emitStdlibHelperCall(helper, c.Args)
	return true
}

func (g *gen) emitStdlibCryptoCall(c *ast.CallExpr, _ *ast.FieldExpr) bool {
	parts, ok := g.stdlibCallPath(c, "crypto")
	if !ok {
		return false
	}
	var helper string
	switch strings.Join(parts, ".") {
	case "sha256":
		if len(c.Args) == 1 {
			helper = "cryptoSHA256"
		}
	case "sha512":
		if len(c.Args) == 1 {
			helper = "cryptoSHA512"
		}
	case "sha1":
		if len(c.Args) == 1 {
			helper = "cryptoSHA1"
		}
	case "md5":
		if len(c.Args) == 1 {
			helper = "cryptoMD5"
		}
	case "hmac.sha256":
		if len(c.Args) == 2 {
			helper = "cryptoHMACSHA256"
		}
	case "hmac.sha512":
		if len(c.Args) == 2 {
			helper = "cryptoHMACSHA512"
		}
	case "randomBytes":
		if len(c.Args) == 1 {
			helper = "cryptoRandomBytes"
		}
	case "constantTimeEq":
		if len(c.Args) == 2 {
			helper = "cryptoConstantTimeEq"
		}
	}
	if helper == "" {
		return false
	}
	g.needCrypto = true
	g.emitStdlibHelperCall(helper, c.Args)
	return true
}

func (g *gen) emitStdlibUUIDCall(c *ast.CallExpr, _ *ast.FieldExpr) bool {
	parts, ok := g.stdlibCallPath(c, "uuid")
	if !ok || len(parts) != 1 {
		return false
	}
	var helper string
	switch parts[0] {
	case "v4":
		if len(c.Args) == 0 {
			helper = "uuidV4"
		}
	case "v7":
		if len(c.Args) == 0 {
			helper = "uuidV7"
		}
	case "parse":
		if len(c.Args) == 1 {
			helper = "uuidParse"
		}
	case "nil":
		if len(c.Args) == 0 {
			helper = "uuidNil"
		}
	}
	if helper == "" {
		return false
	}
	g.needUUID = true
	if helper == "uuidParse" {
		g.needResult = true
	}
	g.emitStdlibHelperCall(helper, c.Args)
	return true
}

func (g *gen) emitQualifiedOptionCall(c *ast.CallExpr, f *ast.FieldExpr) bool {
	id, ok := f.X.(*ast.Ident)
	if !ok || !g.isStdlibPackageAlias(id, "option") {
		return false
	}
	switch f.Name {
	case "Some":
		return g.emitBuiltinCall("Some", c.Args, c)
	case "None":
		if len(c.Args) == 0 {
			g.body.write("nil")
			return true
		}
	}
	return false
}

func (g *gen) emitStdlibHelperCall(helper string, args []*ast.Arg) {
	g.body.write(helper)
	g.body.write("(")
	for i, a := range args {
		if i > 0 {
			g.body.write(", ")
		}
		g.emitExpr(a.Value)
	}
	g.body.write(")")
}

func (g *gen) emitStdlibRegexCall(c *ast.CallExpr, f *ast.FieldExpr) bool {
	id, ok := f.X.(*ast.Ident)
	if !ok || !g.isStdlibPackageAlias(id, "regex") || f.Name != "compile" {
		return false
	}
	if len(c.Args) != 1 {
		return false
	}
	g.needRegex = true
	g.needResult = true
	g.body.write("regexCompile(")
	g.emitExpr(c.Args[0].Value)
	g.body.write(")")
	return true
}

func (g *gen) stdlibCallPath(c *ast.CallExpr, module string) ([]string, bool) {
	id, parts, ok := stdlibFieldChain(c.Fn)
	if !ok || id == nil || len(parts) == 0 {
		return nil, false
	}
	if !g.isStdlibPackageAlias(id, module) {
		return nil, false
	}
	return parts, true
}

func stdlibFieldChain(e ast.Expr) (*ast.Ident, []string, bool) {
	switch x := e.(type) {
	case *ast.Ident:
		return x, nil, true
	case *ast.FieldExpr:
		id, parts, ok := stdlibFieldChain(x.X)
		if !ok {
			return nil, nil, false
		}
		return id, append(parts, x.Name), true
	}
	return nil, nil, false
}

func (g *gen) stdlibStructLitType(e ast.Expr) (string, bool) {
	f, ok := e.(*ast.FieldExpr)
	if !ok {
		return "", false
	}
	id, ok := f.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	if g.isStdlibPackageAlias(id, "csv") && f.Name == "CsvOptions" {
		g.needCSV = true
		return "_ostyCsvOptions", true
	}
	if g.isStdlibPackageAlias(id, "error") && f.Name == "BasicError" {
		g.needErrorRuntime = true
		return "ostyBasicError", true
	}
	return "", false
}

func (g *gen) emitStdlibMathCall(c *ast.CallExpr, f *ast.FieldExpr) bool {
	id, ok := f.X.(*ast.Ident)
	if !ok || !g.isStdlibPackageAlias(id, "math") {
		return false
	}
	alias := g.useStdlibMath(id.Name)
	if f.Name == "log" {
		switch len(c.Args) {
		case 1:
			g.body.writef("%s.Log(", alias)
			g.emitExpr(c.Args[0].Value)
			g.body.write(")")
			return true
		case 2:
			g.body.write("(")
			g.body.writef("%s.Log(", alias)
			g.emitExpr(c.Args[0].Value)
			g.body.write(") / ")
			g.body.writef("%s.Log(", alias)
			g.emitExpr(c.Args[1].Value)
			g.body.write("))")
			return true
		}
		return false
	}
	name, ok := stdlibMathFuncs[f.Name]
	if !ok {
		return false
	}
	g.body.writef("%s.%s(", alias, name)
	for i, a := range c.Args {
		if i > 0 {
			g.body.write(", ")
		}
		g.emitExpr(a.Value)
	}
	g.body.write(")")
	return true
}

func (g *gen) emitStdlibMathField(f *ast.FieldExpr) bool {
	id, ok := f.X.(*ast.Ident)
	if !ok || !g.isStdlibPackageAlias(id, "math") {
		return false
	}
	alias := g.useStdlibMath(id.Name)
	switch f.Name {
	case "PI":
		g.body.writef("%s.Pi", alias)
	case "E":
		g.body.writef("%s.E", alias)
	case "TAU":
		g.body.writef("(2 * %s.Pi)", alias)
	case "INFINITY":
		g.body.writef("%s.Inf(1)", alias)
	case "NAN":
		g.body.writef("%s.NaN()", alias)
	default:
		return false
	}
	return true
}

var stdlibMathFuncs = map[string]string{
	"sin":   "Sin",
	"cos":   "Cos",
	"tan":   "Tan",
	"asin":  "Asin",
	"acos":  "Acos",
	"atan":  "Atan",
	"atan2": "Atan2",
	"sinh":  "Sinh",
	"cosh":  "Cosh",
	"tanh":  "Tanh",
	"exp":   "Exp",
	"log2":  "Log2",
	"log10": "Log10",
	"sqrt":  "Sqrt",
	"cbrt":  "Cbrt",
	"pow":   "Pow",
	"floor": "Floor",
	"ceil":  "Ceil",
	"round": "Round",
	"trunc": "Trunc",
	"abs":   "Abs",
	"min":   "Min",
	"max":   "Max",
	"hypot": "Hypot",
}

func (g *gen) useStdlibMath(alias string) string {
	goAlias := mangleIdent(alias)
	if goAlias == "math" {
		g.use("math")
	} else {
		g.useAs("math", goAlias)
	}
	return goAlias
}

func (g *gen) isStdlibPackageAlias(id *ast.Ident, module string) bool {
	if sym := g.symbolFor(id); sym != nil && sym.Kind == resolve.SymPackage {
		if u, ok := sym.Decl.(*ast.UseDecl); ok {
			return stdlibUseMatchesAlias(u, id.Name, module)
		}
	}
	if g.res != nil {
		return false
	}
	for _, u := range g.file.Uses {
		if stdlibUseMatchesAlias(u, id.Name, module) {
			return true
		}
	}
	return false
}

func (g *gen) isStdlibAliasName(alias, module string) bool {
	for _, u := range g.file.Uses {
		if stdlibUseMatchesAlias(u, alias, module) {
			return true
		}
	}
	return false
}

func stdlibUseMatchesAlias(u *ast.UseDecl, alias, module string) bool {
	if u == nil || u.IsGoFFI || len(u.Path) != 2 || u.Path[0] != "std" || u.Path[1] != module {
		return false
	}
	name := u.Alias
	if name == "" {
		name = u.Path[len(u.Path)-1]
	}
	return name == alias
}

// emitConcurrencyMethod recognizes the small set of channel / handle
// method calls from §8 and rewrites them to Go primitives.
//
//	ch.recv()     → func() *T { v, ok := <-ch; if !ok { return nil }; return &v }()
//	ch.close()    → close(ch)
//	h.join()      → h.Join()
//
// The receiver must have a Chan / Channel / Handle named type; when the
// checker lost that info, we fall through so the generic path takes over.
func (g *gen) emitConcurrencyMethod(c *ast.CallExpr, f *ast.FieldExpr) bool {
	recvT := g.typeOf(f.X)
	n, ok := recvT.(*types.Named)
	if !ok || n.Sym == nil {
		return false
	}
	switch n.Sym.Name {
	case "Chan", "Channel":
		switch f.Name {
		case "recv":
			inner := "any"
			if len(n.Args) == 1 {
				inner = g.goType(n.Args[0])
			}
			g.body.writef("func() *%s { v, ok := <-", inner)
			g.emitExpr(f.X)
			g.body.write("; if !ok { return nil }; return &v }()")
			return true
		case "close":
			g.body.write("close(")
			g.emitExpr(f.X)
			g.body.write(")")
			return true
		case "send":
			// Defensive: `ch.send(v)` surface form; spec uses `ch <- v`
			// but a fluent helper is natural to emit through.
			if len(c.Args) == 1 {
				g.emitExpr(f.X)
				g.body.write(" <- ")
				g.emitExpr(c.Args[0].Value)
				return true
			}
		}
	case "Handle":
		if f.Name == "join" {
			g.emitExpr(f.X)
			g.body.write(".Join()")
			return true
		}
	case "TaskGroup":
		switch f.Name {
		case "spawn":
			if len(c.Args) == 1 {
				g.needTaskGroup = true
				g.needHandle = true
				inner, isUnit := g.handleInnerTypeFromCall(c)
				g.body.writef("spawnInGroup[%s](", inner)
				g.emitExpr(f.X)
				g.body.write(", ")
				g.emitSpawnClosure(c.Args[0].Value, isUnit)
				g.body.write(")")
				return true
			}
		case "cancel":
			g.emitExpr(f.X)
			g.body.write(".Cancel()")
			return true
		case "isCancelled":
			g.emitExpr(f.X)
			g.body.write(".IsCancelled()")
			return true
		}
	}
	return false
}

// emitOptionalMethodCall lowers Option<T> methods to pointer checks.
// Option<T> / T? is represented as *T in generated Go, so these methods
// cannot be emitted as ordinary selector calls.
func (g *gen) emitOptionalMethodCall(c *ast.CallExpr, f *ast.FieldExpr) bool {
	if f.IsOptional {
		return false
	}
	inner, ok := optionInnerType(g.typeOf(f.X))
	if !ok {
		return false
	}
	innerGo := "any"
	if inner != nil {
		innerGo = g.goType(inner)
	}
	switch f.Name {
	case "isSome", "isNone":
		if len(c.Args) != 0 {
			return false
		}
		g.body.write("(")
		g.emitExpr(f.X)
		if f.Name == "isSome" {
			g.body.write(" != nil)")
		} else {
			g.body.write(" == nil)")
		}
		return true
	case "unwrap":
		if len(c.Args) != 0 {
			return false
		}
		g.body.writef("func() %s { opt := ", innerGo)
		g.emitExpr(f.X)
		g.body.write(`; if opt == nil { panic("called unwrap on None") }; return *opt }()`)
		return true
	case "unwrapOr":
		if len(c.Args) != 1 {
			return false
		}
		g.body.writef("func() %s { opt := ", innerGo)
		g.emitExpr(f.X)
		g.body.writef("; var fallback %s = ", innerGo)
		g.emitExpr(c.Args[0].Value)
		g.body.write("; if opt != nil { return *opt }; return fallback }()")
		return true
	case "orElse":
		if len(c.Args) != 1 {
			return false
		}
		g.body.writef("func() *%s { opt := ", innerGo)
		g.emitExpr(f.X)
		g.body.write("; fallback := ")
		g.emitExpr(c.Args[0].Value)
		g.body.write("; if opt != nil { return opt }; return fallback() }()")
		return true
	case "map":
		if len(c.Args) != 1 {
			return false
		}
		retGo := "any"
		if retInner, ok := optionInnerType(g.typeOf(c)); ok && retInner != nil {
			retGo = g.goType(retInner)
		} else if fn, ok := g.typeOf(c.Args[0].Value).(*types.FnType); ok && fn.Return != nil {
			retGo = g.goType(fn.Return)
		}
		g.body.writef("func() *%s { opt := ", retGo)
		g.emitExpr(f.X)
		g.body.write("; f := ")
		g.emitExpr(c.Args[0].Value)
		g.body.write("; if opt == nil { return nil }; var value ")
		g.body.write(retGo)
		g.body.write(" = f(*opt); return &value }()")
		return true
	case "orError":
		if len(c.Args) != 1 {
			return false
		}
		g.needResult = true
		okGo, errGo := innerGo, "any"
		if n, ok := types.AsNamedByName(g.typeOf(c), "Result"); ok && len(n.Args) == 2 {
			okGo = g.goType(n.Args[0])
			errGo = g.goType(n.Args[1])
		}
		resultGo := "Result[" + okGo + ", " + errGo + "]"
		g.body.writef("func() %s { opt := ", resultGo)
		g.emitExpr(f.X)
		g.body.write("; var msg string = ")
		g.emitExpr(c.Args[0].Value)
		g.body.writef("; if opt != nil { return %s{Value: *opt, IsOk: true} }; return %s{Error: msg} }()", resultGo, resultGo)
		return true
	case "toString":
		if len(c.Args) != 0 {
			return false
		}
		g.use("fmt")
		g.body.write("func() string { opt := ")
		g.emitExpr(f.X)
		g.body.write(`; if opt == nil { return "None" }; return fmt.Sprintf("Some(%v)", *opt) }()`)
		return true
	}
	return false
}

func optionInnerType(t types.Type) (types.Type, bool) {
	switch v := t.(type) {
	case *types.Optional:
		return v.Inner, true
	case *types.Named:
		if v.Sym != nil && v.Sym.Name == "Option" && len(v.Args) == 1 {
			return v.Args[0], true
		}
	}
	return nil, false
}

func (g *gen) emitRandomGenericMethod(c *ast.CallExpr, f *ast.FieldExpr) bool {
	if f.Name != "choice" && f.Name != "shuffle" {
		return false
	}
	recvT := g.typeOf(f.X)
	n, ok := recvT.(*types.Named)
	if !ok || n.Sym == nil || n.Sym.Name != "Rng" {
		return false
	}
	if len(c.Args) != 1 {
		return false
	}
	elemGo := "any"
	if itemsT := g.typeOf(c.Args[0].Value); itemsT != nil {
		if list, ok := itemsT.(*types.Named); ok && list.Sym != nil && list.Sym.Name == "List" && len(list.Args) == 1 {
			elemGo = g.goType(list.Args[0])
		}
	}
	switch f.Name {
	case "choice":
		g.body.writef("rngChoice[%s](", elemGo)
	case "shuffle":
		g.body.writef("rngShuffle[%s](", elemGo)
	}
	g.emitExpr(f.X)
	g.body.write(", ")
	g.emitExpr(c.Args[0].Value)
	g.body.write(")")
	return true
}

func (g *gen) emitCollectionMethod(c *ast.CallExpr, f *ast.FieldExpr) bool {
	n, ok := g.typeOf(f.X).(*types.Named)
	if !ok || n.Sym == nil {
		return false
	}
	switch n.Sym.Name {
	case "List":
		return g.emitListMethod(c, f, n)
	case "Map":
		return g.emitMapMethod(c, f, n)
	case "Set":
		return g.emitSetMethod(c, f, n)
	}
	return false
}

func (g *gen) emitListMethod(c *ast.CallExpr, f *ast.FieldExpr, n *types.Named) bool {
	if len(n.Args) != 1 {
		return false
	}
	elemGo := g.goType(n.Args[0])
	listGo := g.goType(n)
	target := g.renderExpr(f.X)
	switch f.Name {
	case "len":
		if len(c.Args) != 0 {
			return false
		}
		g.body.write("len(")
		g.emitExpr(f.X)
		g.body.write(")")
	case "isEmpty":
		if len(c.Args) != 0 {
			return false
		}
		g.body.write("(len(")
		g.emitExpr(f.X)
		g.body.write(") == 0)")
	case "iter":
		if len(c.Args) != 0 {
			return false
		}
		g.emitExpr(f.X)
	case "toList":
		if len(c.Args) != 0 {
			return false
		}
		retGo := g.callReturnGo(c, listGo)
		g.emitCollectionIIFE(retGo, f.X, func(xs string) {
			g.body.writef("out := make(%s, len(%s))\n", retGo, xs)
			g.body.writef("copy(out, %s)\n", xs)
			g.body.writeln("return out")
		})
	case "first":
		if len(c.Args) != 0 {
			return false
		}
		g.emitCollectionIIFE("*"+elemGo, f.X, func(xs string) {
			g.body.writef("if len(%s) == 0 { return nil }\n", xs)
			g.body.writef("v := %s[0]\n", xs)
			g.body.writeln("return &v")
		})
	case "last":
		if len(c.Args) != 0 {
			return false
		}
		g.emitCollectionIIFE("*"+elemGo, f.X, func(xs string) {
			g.body.writef("if len(%s) == 0 { return nil }\n", xs)
			g.body.writef("v := %s[len(%s)-1]\n", xs, xs)
			g.body.writeln("return &v")
		})
	case "get":
		if len(c.Args) != 1 {
			return false
		}
		g.emitCollectionIIFE("*"+elemGo, f.X, func(xs string) {
			idx := g.freshVar("_idx")
			g.body.writef("%s := ", idx)
			g.emitExpr(c.Args[0].Value)
			g.body.nl()
			g.body.writef("if %s < 0 || %s >= len(%s) { return nil }\n", idx, idx, xs)
			g.body.writef("v := %s[%s]\n", xs, idx)
			g.body.writeln("return &v")
		})
	case "contains":
		if len(c.Args) != 1 {
			return false
		}
		g.use("reflect")
		g.emitCollectionIIFE("bool", f.X, func(xs string) {
			item := g.freshVar("_item")
			g.body.writef("%s := ", item)
			g.emitExpr(c.Args[0].Value)
			g.body.nl()
			g.body.writef("for _, v := range %s { if reflect.DeepEqual(v, %s) { return true } }\n", xs, item)
			g.body.writeln("return false")
		})
	case "indexOf":
		if len(c.Args) != 1 {
			return false
		}
		g.use("reflect")
		g.emitCollectionIIFE("*int", f.X, func(xs string) {
			item := g.freshVar("_item")
			g.body.writef("%s := ", item)
			g.emitExpr(c.Args[0].Value)
			g.body.nl()
			g.body.writef("for i, v := range %s { if reflect.DeepEqual(v, %s) { idx := i; return &idx } }\n", xs, item)
			g.body.writeln("return nil")
		})
	case "find":
		if len(c.Args) != 1 {
			return false
		}
		g.emitCollectionIIFE("*"+elemGo, f.X, func(xs string) {
			pred := g.freshVar("_pred")
			g.body.writef("%s := ", pred)
			g.emitExpr(c.Args[0].Value)
			g.body.nl()
			g.body.writef("for _, v := range %s { if %s(v) { found := v; return &found } }\n", xs, pred)
			g.body.writeln("return nil")
		})
	case "map":
		if len(c.Args) != 1 {
			return false
		}
		retGo := g.callReturnGo(c, "[]any")
		g.emitCollectionIIFE(retGo, f.X, func(xs string) {
			fn := g.freshVar("_fn")
			g.body.writef("%s := ", fn)
			g.emitExpr(c.Args[0].Value)
			g.body.nl()
			g.body.writef("out := make(%s, len(%s))\n", retGo, xs)
			g.body.writef("for i, v := range %s { out[i] = %s(v) }\n", xs, fn)
			g.body.writeln("return out")
		})
	case "filter":
		if len(c.Args) != 1 {
			return false
		}
		retGo := g.callReturnGo(c, listGo)
		g.emitCollectionIIFE(retGo, f.X, func(xs string) {
			pred := g.freshVar("_pred")
			g.body.writef("%s := ", pred)
			g.emitExpr(c.Args[0].Value)
			g.body.nl()
			g.body.writef("out := make(%s, 0, len(%s))\n", retGo, xs)
			g.body.writef("for _, v := range %s { if %s(v) { out = append(out, v) } }\n", xs, pred)
			g.body.writeln("return out")
		})
	case "fold":
		if len(c.Args) != 2 {
			return false
		}
		retGo := g.callReturnGo(c, "any")
		g.emitCollectionIIFE(retGo, f.X, func(xs string) {
			acc := g.freshVar("_acc")
			fn := g.freshVar("_fn")
			g.body.writef("%s := ", acc)
			g.emitExpr(c.Args[0].Value)
			g.body.nl()
			g.body.writef("%s := ", fn)
			g.emitExpr(c.Args[1].Value)
			g.body.nl()
			g.body.writef("for _, v := range %s { %s = %s(%s, v) }\n", xs, acc, fn, acc)
			g.body.writef("return %s\n", acc)
		})
	case "sorted":
		if len(c.Args) != 0 {
			return false
		}
		g.use("sort")
		retGo := g.callReturnGo(c, listGo)
		g.emitCollectionIIFE(retGo, f.X, func(xs string) {
			g.body.writef("out := make(%s, len(%s))\n", retGo, xs)
			g.body.writef("copy(out, %s)\n", xs)
			g.body.write("sort.Slice(out, func(i, j int) bool { return ")
			g.emitCollectionLess("out[i]", "out[j]", n.Args[0])
			g.body.writeln(" })")
			g.body.writeln("return out")
		})
	case "sortedBy":
		if len(c.Args) != 1 {
			return false
		}
		g.use("sort")
		retGo := g.callReturnGo(c, listGo)
		keyRet := g.collectionCallbackReturnType(c)
		g.emitCollectionIIFE(retGo, f.X, func(xs string) {
			key := g.freshVar("_key")
			g.body.writef("%s := ", key)
			g.emitExpr(c.Args[0].Value)
			g.body.nl()
			g.body.writef("out := make(%s, len(%s))\n", retGo, xs)
			g.body.writef("copy(out, %s)\n", xs)
			g.body.write("sort.Slice(out, func(i, j int) bool { return ")
			g.emitCollectionLess(key+"(out[i])", key+"(out[j])", keyRet)
			g.body.writeln(" })")
			g.body.writeln("return out")
		})
	case "reversed":
		if len(c.Args) != 0 {
			return false
		}
		retGo := g.callReturnGo(c, listGo)
		g.emitCollectionIIFE(retGo, f.X, func(xs string) {
			g.body.writef("out := make(%s, len(%s))\n", retGo, xs)
			g.body.writef("copy(out, %s)\n", xs)
			g.body.writeln("for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 { out[i], out[j] = out[j], out[i] }")
			g.body.writeln("return out")
		})
	case "appended":
		if len(c.Args) != 1 {
			return false
		}
		retGo := g.callReturnGo(c, listGo)
		g.emitCollectionIIFE(retGo, f.X, func(xs string) {
			item := g.freshVar("_item")
			g.body.writef("%s := ", item)
			g.emitExpr(c.Args[0].Value)
			g.body.nl()
			g.body.writef("out := make(%s, 0, len(%s)+1)\n", retGo, xs)
			g.body.writef("out = append(out, %s...)\n", xs)
			g.body.writef("out = append(out, %s)\n", item)
			g.body.writeln("return out")
		})
	case "concat":
		if len(c.Args) != 1 {
			return false
		}
		retGo := g.callReturnGo(c, listGo)
		g.emitCollectionIIFE(retGo, f.X, func(xs string) {
			other := g.freshVar("_other")
			g.body.writef("%s := ", other)
			g.emitExpr(c.Args[0].Value)
			g.body.nl()
			g.body.writef("out := make(%s, 0, len(%s)+len(%s))\n", retGo, xs, other)
			g.body.writef("out = append(out, %s...)\n", xs)
			g.body.writef("out = append(out, %s...)\n", other)
			g.body.writeln("return out")
		})
	case "zip":
		if len(c.Args) != 1 {
			return false
		}
		retGo := g.callReturnGo(c, "[]struct{F0 any; F1 any}")
		elemRetGo := g.collectionListElemGo(g.typeOf(c), "struct{F0 any; F1 any}")
		g.emitCollectionIIFE(retGo, f.X, func(xs string) {
			other := g.freshVar("_other")
			g.body.writef("%s := ", other)
			g.emitExpr(c.Args[0].Value)
			g.body.nl()
			g.body.writef("n := len(%s)\n", xs)
			g.body.writef("if len(%s) < n { n = len(%s) }\n", other, other)
			g.body.writef("out := make(%s, n)\n", retGo)
			g.body.writef("for i := 0; i < n; i++ { out[i] = %s{F0: %s[i], F1: %s[i]} }\n", elemRetGo, xs, other)
			g.body.writeln("return out")
		})
	case "enumerate":
		if len(c.Args) != 0 {
			return false
		}
		retGo := g.callReturnGo(c, "[]struct{F0 int; F1 "+elemGo+"}")
		elemRetGo := g.collectionListElemGo(g.typeOf(c), "struct{F0 int; F1 "+elemGo+"}")
		g.emitCollectionIIFE(retGo, f.X, func(xs string) {
			g.body.writef("out := make(%s, len(%s))\n", retGo, xs)
			g.body.writef("for i, v := range %s { out[i] = %s{F0: i, F1: v} }\n", xs, elemRetGo)
			g.body.writeln("return out")
		})
	case "take":
		if len(c.Args) != 1 {
			return false
		}
		retGo := g.callReturnGo(c, listGo)
		g.emitCollectionIIFE(retGo, f.X, func(xs string) {
			limit := g.freshVar("_n")
			g.body.writef("%s := ", limit)
			g.emitExpr(c.Args[0].Value)
			g.body.nl()
			g.body.writef("if %s < 0 { %s = 0 }\n", limit, limit)
			g.body.writef("if %s > len(%s) { %s = len(%s) }\n", limit, xs, limit, xs)
			g.body.writef("out := make(%s, %s)\n", retGo, limit)
			g.body.writef("copy(out, %s[:%s])\n", xs, limit)
			g.body.writeln("return out")
		})
	case "toSet":
		if len(c.Args) != 0 {
			return false
		}
		retGo := g.callReturnGo(c, "map["+elemGo+"]struct{}")
		g.emitCollectionIIFE(retGo, f.X, func(xs string) {
			g.body.writef("out := make(%s, len(%s))\n", retGo, xs)
			g.body.writef("for _, v := range %s { out[v] = struct{}{} }\n", xs)
			g.body.writeln("return out")
		})
	case "push":
		if len(c.Args) != 1 {
			return false
		}
		g.body.write("func() { ")
		g.body.write(target)
		g.body.write(" = append(")
		g.body.write(target)
		g.body.write(", ")
		g.emitExpr(c.Args[0].Value)
		g.body.write(") }()")
	case "pop":
		if len(c.Args) != 0 {
			return false
		}
		g.body.writef("func() *%s { if len(%s) == 0 { return nil }; v := %s[len(%s)-1]; var zero %s; %s[len(%s)-1] = zero; %s = %s[:len(%s)-1]; return &v }()",
			elemGo, target, target, target, elemGo, target, target, target, target, target)
	case "insert":
		if len(c.Args) != 2 {
			return false
		}
		idx := g.freshVar("_idx")
		item := g.freshVar("_item")
		g.body.writef("func() { %s := ", idx)
		g.emitExpr(c.Args[0].Value)
		g.body.writef("; %s := ", item)
		g.emitExpr(c.Args[1].Value)
		g.body.writef("; if %s < 0 || %s > len(%s) { panic(\"List.insert index out of range\") }; var zero %s; %s = append(%s, zero); copy(%s[%s+1:], %s[%s:]); %s[%s] = %s }()",
			idx, idx, target, elemGo, target, target, target, idx, target, idx, target, idx, item)
	case "removeAt":
		if len(c.Args) != 1 {
			return false
		}
		idx := g.freshVar("_idx")
		g.body.writef("func() %s { %s := ", elemGo, idx)
		g.emitExpr(c.Args[0].Value)
		g.body.writef("; v := %s[%s]; copy(%s[%s:], %s[%s+1:]); var zero %s; %s[len(%s)-1] = zero; %s = %s[:len(%s)-1]; return v }()",
			target, idx, target, idx, target, idx, elemGo, target, target, target, target, target)
	case "sort":
		if len(c.Args) != 0 {
			return false
		}
		g.use("sort")
		g.body.writef("func() { xs := %s; sort.Slice(xs, func(i, j int) bool { return ", target)
		g.emitCollectionLess("xs[i]", "xs[j]", n.Args[0])
		g.body.write(" }) }()")
	case "reverse":
		if len(c.Args) != 0 {
			return false
		}
		g.body.writef("func() { xs := %s; for i, j := 0, len(xs)-1; i < j; i, j = i+1, j-1 { xs[i], xs[j] = xs[j], xs[i] } }()", target)
	case "clear":
		if len(c.Args) != 0 {
			return false
		}
		g.body.writef("func() { %s = %s[:0] }()", target, target)
	default:
		return false
	}
	return true
}

func (g *gen) emitMapMethod(c *ast.CallExpr, f *ast.FieldExpr, n *types.Named) bool {
	if len(n.Args) != 2 {
		return false
	}
	keyGo := g.goType(n.Args[0])
	valGo := g.goType(n.Args[1])
	mapGo := g.goType(n)
	target := g.renderExpr(f.X)
	switch f.Name {
	case "len":
		if len(c.Args) != 0 {
			return false
		}
		g.body.write("len(")
		g.emitExpr(f.X)
		g.body.write(")")
	case "isEmpty":
		if len(c.Args) != 0 {
			return false
		}
		g.body.write("(len(")
		g.emitExpr(f.X)
		g.body.write(") == 0)")
	case "get":
		if len(c.Args) != 1 {
			return false
		}
		g.emitCollectionIIFE("*"+valGo, f.X, func(m string) {
			key := g.freshVar("_key")
			g.body.writef("%s := ", key)
			g.emitExpr(c.Args[0].Value)
			g.body.nl()
			g.body.writef("v, ok := %s[%s]\n", m, key)
			g.body.writeln("if !ok { return nil }")
			g.body.writeln("return &v")
		})
	case "containsKey":
		if len(c.Args) != 1 {
			return false
		}
		g.emitCollectionIIFE("bool", f.X, func(m string) {
			key := g.freshVar("_key")
			g.body.writef("%s := ", key)
			g.emitExpr(c.Args[0].Value)
			g.body.nl()
			g.body.writef("_, ok := %s[%s]\n", m, key)
			g.body.writeln("return ok")
		})
	case "keys":
		if len(c.Args) != 0 {
			return false
		}
		retGo := g.callReturnGo(c, "[]"+keyGo)
		g.emitCollectionIIFE(retGo, f.X, func(m string) {
			g.body.writef("out := make(%s, 0, len(%s))\n", retGo, m)
			g.body.writef("for k := range %s { out = append(out, k) }\n", m)
			g.body.writeln("return out")
		})
	case "values":
		if len(c.Args) != 0 {
			return false
		}
		retGo := g.callReturnGo(c, "[]"+valGo)
		g.emitCollectionIIFE(retGo, f.X, func(m string) {
			g.body.writef("out := make(%s, 0, len(%s))\n", retGo, m)
			g.body.writef("for _, v := range %s { out = append(out, v) }\n", m)
			g.body.writeln("return out")
		})
	case "entries", "iter", "toList":
		if len(c.Args) != 0 {
			return false
		}
		retGo := g.callReturnGo(c, "[]struct{F0 "+keyGo+"; F1 "+valGo+"}")
		elemRetGo := g.collectionListElemGo(g.typeOf(c), "struct{F0 "+keyGo+"; F1 "+valGo+"}")
		g.emitCollectionIIFE(retGo, f.X, func(m string) {
			g.body.writef("out := make(%s, 0, len(%s))\n", retGo, m)
			g.body.writef("for k, v := range %s { out = append(out, %s{F0: k, F1: v}) }\n", m, elemRetGo)
			g.body.writeln("return out")
		})
	case "insert":
		if len(c.Args) != 2 {
			return false
		}
		key := g.freshVar("_key")
		val := g.freshVar("_val")
		g.body.writef("func() { if %s == nil { %s = %s{} }; %s := ", target, target, mapGo, key)
		g.emitExpr(c.Args[0].Value)
		g.body.writef("; %s := ", val)
		g.emitExpr(c.Args[1].Value)
		g.body.writef("; %s[%s] = %s }()", target, key, val)
	case "remove":
		if len(c.Args) != 1 {
			return false
		}
		key := g.freshVar("_key")
		g.body.writef("func() *%s { %s := ", valGo, key)
		g.emitExpr(c.Args[0].Value)
		g.body.writef("; v, ok := %s[%s]; if !ok { return nil }; delete(%s, %s); return &v }()", target, key, target, key)
	case "clear":
		if len(c.Args) != 0 {
			return false
		}
		g.body.writef("func() { clear(%s) }()", target)
	case "toMap":
		if len(c.Args) != 0 {
			return false
		}
		retGo := g.callReturnGo(c, mapGo)
		g.emitCollectionIIFE(retGo, f.X, func(m string) {
			g.body.writef("out := make(%s, len(%s))\n", retGo, m)
			g.body.writef("for k, v := range %s { out[k] = v }\n", m)
			g.body.writeln("return out")
		})
	default:
		return false
	}
	return true
}

func (g *gen) emitSetMethod(c *ast.CallExpr, f *ast.FieldExpr, n *types.Named) bool {
	if len(n.Args) != 1 {
		return false
	}
	elemGo := g.goType(n.Args[0])
	setGo := g.goType(n)
	target := g.renderExpr(f.X)
	switch f.Name {
	case "len":
		if len(c.Args) != 0 {
			return false
		}
		g.body.write("len(")
		g.emitExpr(f.X)
		g.body.write(")")
	case "isEmpty":
		if len(c.Args) != 0 {
			return false
		}
		g.body.write("(len(")
		g.emitExpr(f.X)
		g.body.write(") == 0)")
	case "contains":
		if len(c.Args) != 1 {
			return false
		}
		g.emitCollectionIIFE("bool", f.X, func(s string) {
			item := g.freshVar("_item")
			g.body.writef("%s := ", item)
			g.emitExpr(c.Args[0].Value)
			g.body.nl()
			g.body.writef("_, ok := %s[%s]\n", s, item)
			g.body.writeln("return ok")
		})
	case "union", "intersect", "difference":
		if len(c.Args) != 1 {
			return false
		}
		retGo := g.callReturnGo(c, setGo)
		g.emitCollectionIIFE(retGo, f.X, func(s string) {
			other := g.freshVar("_other")
			g.body.writef("%s := ", other)
			g.emitExpr(c.Args[0].Value)
			g.body.nl()
			switch f.Name {
			case "union":
				g.body.writef("out := make(%s, len(%s)+len(%s))\n", retGo, s, other)
				g.body.writef("for v := range %s { out[v] = struct{}{} }\n", s)
				g.body.writef("for v := range %s { out[v] = struct{}{} }\n", other)
			case "intersect":
				g.body.writef("out := make(%s)\n", retGo)
				g.body.writef("for v := range %s { if _, ok := %s[v]; ok { out[v] = struct{}{} } }\n", s, other)
			case "difference":
				g.body.writef("out := make(%s)\n", retGo)
				g.body.writef("for v := range %s { if _, ok := %s[v]; !ok { out[v] = struct{}{} } }\n", s, other)
			}
			g.body.writeln("return out")
		})
	case "insert":
		if len(c.Args) != 1 {
			return false
		}
		g.body.writef("func() { if %s == nil { %s = %s{} }; %s[", target, target, setGo, target)
		g.emitExpr(c.Args[0].Value)
		g.body.write("] = struct{}{} }()")
	case "remove":
		if len(c.Args) != 1 {
			return false
		}
		item := g.freshVar("_item")
		g.body.writef("func() bool { %s := ", item)
		g.emitExpr(c.Args[0].Value)
		g.body.writef("; _, ok := %s[%s]; if ok { delete(%s, %s) }; return ok }()", target, item, target, item)
	case "clear":
		if len(c.Args) != 0 {
			return false
		}
		g.body.writef("func() { clear(%s) }()", target)
	case "iter", "toList":
		if len(c.Args) != 0 {
			return false
		}
		retGo := g.callReturnGo(c, "[]"+elemGo)
		g.emitCollectionIIFE(retGo, f.X, func(s string) {
			g.body.writef("out := make(%s, 0, len(%s))\n", retGo, s)
			g.body.writef("for v := range %s { out = append(out, v) }\n", s)
			g.body.writeln("return out")
		})
	case "toSet":
		if len(c.Args) != 0 {
			return false
		}
		retGo := g.callReturnGo(c, setGo)
		g.emitCollectionIIFE(retGo, f.X, func(s string) {
			g.body.writef("out := make(%s, len(%s))\n", retGo, s)
			g.body.writef("for v := range %s { out[v] = struct{}{} }\n", s)
			g.body.writeln("return out")
		})
	default:
		return false
	}
	return true
}

func (g *gen) emitCollectionIIFE(retGo string, recv ast.Expr, body func(recvVar string)) {
	recvVar := g.freshVar("_col")
	g.body.write("func()")
	if retGo != "" {
		g.body.write(" ")
		g.body.write(retGo)
	}
	g.body.writeln(" {")
	g.body.indent()
	g.body.writef("%s := ", recvVar)
	g.emitExpr(recv)
	g.body.nl()
	body(recvVar)
	g.body.dedent()
	g.body.write("}()")
}

func (g *gen) renderExpr(e ast.Expr) string {
	old := g.body
	w := newWriter()
	g.body = w
	defer func() { g.body = old }()
	g.emitExpr(e)
	return string(w.bytes())
}

func (g *gen) callReturnGo(c *ast.CallExpr, fallback string) string {
	if t := g.typeOf(c); t != nil && !types.IsError(t) && !types.IsUnit(t) {
		return g.goType(t)
	}
	return fallback
}

func (g *gen) collectionCallbackReturnType(c *ast.CallExpr) types.Type {
	if len(c.Args) != 1 {
		return nil
	}
	if fn, ok := types.AsFn(g.typeOf(c.Args[0].Value)); ok {
		return fn.Return
	}
	return nil
}

func (g *gen) emitCollectionLess(left, right string, t types.Type) {
	if p, ok := t.(*types.Primitive); ok {
		switch p.Kind {
		case types.PBool:
			g.body.writef("(!%s && %s)", left, right)
			return
		case types.PBytes:
			g.use("bytes")
			g.body.writef("bytes.Compare(%s, %s) < 0", left, right)
			return
		}
	}
	g.body.writef("%s < %s", left, right)
}

func (g *gen) collectionListElemGo(t types.Type, fallback string) string {
	if n, ok := t.(*types.Named); ok && n.Sym != nil && n.Sym.Name == "List" && len(n.Args) == 1 {
		return g.goType(n.Args[0])
	}
	return strings.TrimPrefix(fallback, "[]")
}

// emitTurbofishCall handles the two concurrency intrinsics that use the
// turbofish syntax to pin a type argument:
//
//	thread.chan::<T>(cap)  → make(chan T, cap)
//	thread.spawn::<T>(f)   → spawnHandle[T](f)
//
// Returns false when the turbofish base isn't a recognized intrinsic.
// The base has already been confirmed as the Fn of a CallExpr.
func (g *gen) emitTurbofishCall(c *ast.CallExpr, tf *ast.TurbofishExpr) bool {
	fe, ok := tf.Base.(*ast.FieldExpr)
	if !ok {
		return false
	}
	head, ok := fe.X.(*ast.Ident)
	if !ok || head.Name != "thread" {
		return false
	}
	switch fe.Name {
	case "chan":
		inner := "any"
		if len(tf.Args) == 1 {
			inner = g.goTypeExpr(tf.Args[0])
		}
		g.body.writef("make(chan %s", inner)
		if len(c.Args) >= 1 {
			g.body.write(", ")
			g.emitExpr(c.Args[0].Value)
		}
		g.body.write(")")
		return true
	case "spawn":
		g.needHandle = true
		inner := "any"
		if len(tf.Args) == 1 {
			inner = g.goTypeExpr(tf.Args[0])
		}
		if len(c.Args) != 1 {
			return false
		}
		g.body.writef("spawnHandle[%s](", inner)
		g.emitExpr(c.Args[0].Value)
		g.body.write(")")
		return true
	}
	return false
}

// emitThreadCall intercepts non-turbofish thread.* helpers like
// `thread.sleep(dur)` and `thread.yield()`.
func (g *gen) emitThreadCall(c *ast.CallExpr, f *ast.FieldExpr) bool {
	head, ok := f.X.(*ast.Ident)
	if !ok || head.Name != "thread" {
		return false
	}
	switch f.Name {
	case "collectAll":
		if g.emitThreadCollectAll(c) {
			return true
		}
	case "race":
		if g.emitThreadRace(c) {
			return true
		}
	case "isCancelled":
		if len(c.Args) == 0 {
			g.body.write("false")
			return true
		}
	case "sleep":
		// thread.sleep(dur) → time.Sleep(dur)
		if len(c.Args) != 1 {
			return false
		}
		g.use("time")
		g.body.write("time.Sleep(")
		g.emitDurationExpr(c.Args[0].Value)
		g.body.write(")")
		return true
	case "yield":
		// thread.yield() → runtime.Gosched()
		g.use("runtime")
		g.body.write("runtime.Gosched()")
		return true
	}
	return false
}

func (g *gen) emitThreadCollectAll(c *ast.CallExpr) bool {
	param, spawns, ok := g.threadGroupSpawnCalls(c)
	if !ok || len(spawns) == 0 {
		return false
	}
	g.needTaskGroup = true
	g.needHandle = true
	inner := g.spawnCallInnerGo(spawns[0])
	ret := g.listReturnGo(c, inner)
	g.body.writef("runTaskGroup[%s](func(%s *TaskGroup) %s {\n", ret, param, ret)
	g.body.indent()
	handles := make([]string, len(spawns))
	for i, sp := range spawns {
		h := g.freshVar("_h")
		handles[i] = h
		g.body.writef("%s := spawnInGroup[%s](%s, ", h, inner, param)
		g.emitSpawnClosure(sp.Args[0].Value, types.IsUnit(g.closureReturnType(sp.Args[0].Value)))
		g.body.writeln(")")
	}
	g.body.writef("return %s{", ret)
	for i, h := range handles {
		if i > 0 {
			g.body.write(", ")
		}
		g.body.writef("%s.Join()", h)
	}
	g.body.writeln("}")
	g.body.dedent()
	g.body.write("})")
	return true
}

func (g *gen) emitThreadRace(c *ast.CallExpr) bool {
	param, spawns, ok := g.threadGroupSpawnCalls(c)
	if !ok || len(spawns) == 0 {
		return false
	}
	g.needTaskGroup = true
	g.needHandle = true
	inner := g.spawnCallInnerGo(spawns[0])
	g.body.writef("runTaskGroup[%s](func(%s *TaskGroup) %s {\n", inner, param, inner)
	g.body.indent()
	handles := make([]string, len(spawns))
	for i, sp := range spawns {
		h := g.freshVar("_h")
		handles[i] = h
		g.body.writef("%s := spawnInGroup[%s](%s, ", h, inner, param)
		g.emitSpawnClosure(sp.Args[0].Value, types.IsUnit(g.closureReturnType(sp.Args[0].Value)))
		g.body.writeln(")")
	}
	g.body.writeln("select {")
	g.body.indent()
	for _, h := range handles {
		g.body.writef("case v := <-%s.result:\n", h)
		g.body.indent()
		g.body.writeln("return v")
		g.body.dedent()
	}
	g.body.dedent()
	g.body.writeln("}")
	g.body.dedent()
	g.body.write("})")
	return true
}

func (g *gen) threadGroupSpawnCalls(c *ast.CallExpr) (string, []*ast.CallExpr, bool) {
	if c == nil || len(c.Args) != 1 {
		return "", nil, false
	}
	cl, ok := c.Args[0].Value.(*ast.ClosureExpr)
	if !ok || len(cl.Params) == 0 {
		return "", nil, false
	}
	param := "g"
	if cl.Params[0].Name != "" {
		param = mangleIdent(cl.Params[0].Name)
	}
	list, ok := cl.Body.(*ast.ListExpr)
	if !ok {
		return "", nil, false
	}
	spawns := make([]*ast.CallExpr, 0, len(list.Elems))
	for _, e := range list.Elems {
		call, ok := e.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return "", nil, false
		}
		fx, ok := call.Fn.(*ast.FieldExpr)
		if !ok || fx.Name != "spawn" {
			return "", nil, false
		}
		if id, ok := fx.X.(*ast.Ident); !ok || mangleIdent(id.Name) != param {
			return "", nil, false
		}
		spawns = append(spawns, call)
	}
	return param, spawns, true
}

func (g *gen) spawnCallInnerGo(call *ast.CallExpr) string {
	if call == nil || len(call.Args) != 1 {
		return "any"
	}
	t := g.closureReturnType(call.Args[0].Value)
	if t == nil || types.IsError(t) {
		return "any"
	}
	if types.IsUnit(t) {
		return "struct{}"
	}
	return g.goType(t)
}

func (g *gen) closureReturnType(e ast.Expr) types.Type {
	if t := g.typeOf(e); t != nil {
		if fn, ok := t.(*types.FnType); ok {
			return fn.Return
		}
	}
	return nil
}

func (g *gen) listReturnGo(call *ast.CallExpr, elemGo string) string {
	if t := g.typeOf(call); t != nil {
		if n, ok := t.(*types.Named); ok && n.Sym != nil && n.Sym.Name == "List" && len(n.Args) == 1 {
			return g.goType(t)
		}
	}
	return "[]" + elemGo
}

// emitVariantCtor writes `Shape(Shape_Circle{F0: a0, F1: a1})` for
// `Circle(a0, a1)`. The outer `Enum(...)` conversion forces the value
// to the enum interface type so downstream type assertions work even
// when the value flows through a generic position like a `let` short-form.
func (g *gen) emitVariantCtor(owner, name string, args []*ast.Arg) {
	g.body.writef("%s(&%s_%s{", owner, owner, name)
	for i, a := range args {
		if i > 0 {
			g.body.write(", ")
		}
		g.body.writef("F%d: ", i)
		g.emitExpr(a.Value)
	}
	g.body.write("})")
}

// emitStaticCall rewrites `TypeName.fnName(args)` as `TypeName_fnName(args)`
// when TypeName is a struct or enum declared in this file. `Self.new(...)`
// inside a method body also flows here. Returns false when the head does
// not look like a type reference, letting the default path handle it.
func (g *gen) emitStaticCall(f *ast.FieldExpr, args []*ast.Arg) bool {
	id, ok := f.X.(*ast.Ident)
	if !ok {
		return false
	}
	typeName := id.Name
	if typeName == "Self" {
		if g.selfType == "" {
			return false
		}
		typeName = g.selfType
	}
	methods, known := g.methodNames[typeName]
	if !known {
		return false
	}
	if !methods[f.Name] {
		return false
	}
	// Is this actually an associated (static) function, or an instance
	// method being called on an instance? If Ident resolves to a type
	// symbol it's static; otherwise we fall through.
	sym := g.symbolFor(id)
	if sym == nil {
		return false
	}
	if sym.Kind != resolve.SymStruct && sym.Kind != resolve.SymEnum && typeName != g.selfType {
		return false
	}
	g.body.writef("%s_%s(", typeName, f.Name)
	for i, a := range args {
		if i > 0 {
			g.body.write(", ")
		}
		g.emitExpr(a.Value)
	}
	g.body.write(")")
	return true
}

func (g *gen) emitResultMethodCall(c *ast.CallExpr, f *ast.FieldExpr) bool {
	n, ok := types.AsNamedBuiltin(g.typeOf(f.X), "Result")
	if !ok || len(n.Args) != 2 {
		return false
	}
	switch f.Name {
	case "map":
		if len(c.Args) != 1 {
			return false
		}
		g.needResult = true
		g.body.write("resultMap(")
		g.emitExpr(f.X)
		g.body.write(", ")
		g.emitExpr(c.Args[0].Value)
		g.body.write(")")
		return true
	case "mapErr":
		if len(c.Args) != 1 {
			return false
		}
		g.needResult = true
		g.body.write("resultMapErr(")
		g.emitExpr(f.X)
		g.body.write(", ")
		g.emitExpr(c.Args[0].Value)
		g.body.write(")")
		return true
	}
	return false
}

// emitEnumMethodCall rewrites `enumValue.method(args)` as
// `EnumName_method(enumValue, args)` when the value's type is a
// user-declared enum in this file.
func (g *gen) emitEnumMethodCall(f *ast.FieldExpr, args []*ast.Arg) bool {
	t := g.typeOf(f.X)
	n, ok := t.(*types.Named)
	if !ok || n.Sym == nil {
		return false
	}
	if !g.enumTypes[n.Sym.Name] {
		return false
	}
	methods := g.methodNames[n.Sym.Name]
	if methods == nil || !methods[f.Name] {
		return false
	}
	g.body.writef("%s_%s(", n.Sym.Name, f.Name)
	g.emitExpr(f.X)
	for _, a := range args {
		g.body.write(", ")
		g.emitExpr(a.Value)
	}
	g.body.write(")")
	return true
}

// resultTypeArgsAt returns the (T, E) Go type strings for the Result
// that contains expression `at`. Prefers the checker-inferred type of
// the call expression; falls back to any when no info is available.
func (g *gen) resultTypeArgsAt(callType types.Type, payloadType types.Type, isErr bool) (string, string) {
	tArg, tErr := "any", "any"
	if n, ok := callType.(*types.Named); ok && n.Sym != nil && n.Sym.Name == "Result" && len(n.Args) == 2 {
		tArg = g.goType(n.Args[0])
		tErr = g.goType(n.Args[1])
		return tArg, tErr
	}
	// Fallback to payload type on the known side.
	if payloadType != nil {
		if isErr {
			tErr = g.goType(payloadType)
		} else {
			tArg = g.goType(payloadType)
		}
	}
	return tArg, tErr
}

// emitBuiltinCall handles prelude intrinsics. Returns true when it
// produced output; false lets the generic path take over.
func (g *gen) emitBuiltinCall(name string, args []*ast.Arg, call *ast.CallExpr) bool {
	switch name {
	case "println":
		g.use("fmt")
		g.body.write("fmt.Println(")
		g.emitCallArgList(args)
		g.body.write(")")
		return true
	case "print":
		g.use("fmt")
		g.body.write("fmt.Print(")
		g.emitCallArgList(args)
		g.body.write(")")
		return true
	case "eprintln":
		g.use("fmt")
		g.use("os")
		g.body.write("fmt.Fprintln(os.Stderr, ")
		g.emitCallArgList(args)
		g.body.write(")")
		return true
	case "eprint":
		g.use("fmt")
		g.use("os")
		g.body.write("fmt.Fprint(os.Stderr, ")
		g.emitCallArgList(args)
		g.body.write(")")
		return true
	case "dbg":
		// Osty `dbg(x)` prints `[file:line] x = <value>` and returns x.
		// Phase 1 simplification: route through fmt.Println and return
		// the argument via an IIFE so it's still usable as an expression.
		g.use("fmt")
		g.body.write("func() any { v := ")
		if len(args) > 0 {
			g.emitExpr(args[0].Value)
		} else {
			g.body.write("nil")
		}
		g.body.write("; fmt.Println(\"dbg:\", v); return v }()")
		return true
	case "spawn":
		// §8: spawn(|| body) → spawnHandle[T](body). The checker
		// inferred the closure's return type; we pull it from the call
		// expression's Handle<T> type to pin the type parameter.
		//
		// Unit-returning closures need a trampoline — Go treats
		// `func()` and `func() struct{}` as distinct types — so we
		// wrap with `func() struct{} { <closure>(); return struct{}{} }`
		// to satisfy spawnHandle's `func() T` signature.
		if len(args) == 1 {
			g.needHandle = true
			inner, isUnit := g.handleInnerTypeFromCall(call)
			g.body.writef("spawnHandle[%s](", inner)
			g.emitSpawnClosure(args[0].Value, isUnit)
			g.body.write(")")
			return true
		}
	case "taskGroup":
		// §8.1: taskGroup(|g| body) → runTaskGroup[T](body).
		// The outer call's type is T; the inner closure receives a
		// *TaskGroup.
		if len(args) == 1 {
			g.needTaskGroup = true
			g.needHandle = true
			inner := "any"
			if call != nil {
				if t := g.typeOf(call); t != nil && !types.IsUnit(t) && !types.IsError(t) {
					inner = g.goType(t)
				} else if t != nil && types.IsUnit(t) {
					inner = "struct{}"
				}
			}
			g.body.writef("runTaskGroup[%s](", inner)
			g.emitExpr(args[0].Value)
			g.body.write(")")
			return true
		}
	case "parallel":
		if len(args) == 3 {
			if itemGo, resultGo, ok := g.parallelMapTypes(call); ok {
				g.needTaskGroup = true
				g.body.writef("runParallelMap[%s, %s](", itemGo, resultGo)
				g.emitExpr(args[0].Value)
				g.body.write(", ")
				g.emitExpr(args[1].Value)
				g.body.write(", ")
				g.emitExpr(args[2].Value)
				g.body.write(")")
				return true
			}
		}
		// §8.3: parallel(|| a, || b, ...) → runParallel[T](bodies...).
		// Every closure must return the same T; we pull T from the
		// first argument's inferred FnType return.
		if len(args) > 0 {
			g.needTaskGroup = true
			g.needHandle = true
			inner := "any"
			isUnit := false
			if t := g.typeOf(args[0].Value); t != nil {
				if fn, ok := t.(*types.FnType); ok && fn.Return != nil {
					if types.IsUnit(fn.Return) {
						inner = "struct{}"
						isUnit = true
					} else if !types.IsError(fn.Return) {
						inner = g.goType(fn.Return)
					}
				}
			}
			g.body.writef("runParallel[%s](", inner)
			for i, a := range args {
				if i > 0 {
					g.body.write(", ")
				}
				g.emitSpawnClosure(a.Value, isUnit)
			}
			g.body.write(")")
			return true
		}
	case "Some":
		if len(args) == 1 {
			// Some(x) lowers to a typed pointer-to-copy so the result
			// flows naturally as *T at the use site. The inner type T
			// comes from the argument's checked type.
			inner := "any"
			if t := g.typeOf(args[0].Value); t != nil {
				inner = g.goType(t)
			}
			g.body.writef("func() *%s { v := ", inner)
			g.emitExpr(args[0].Value)
			g.body.write("; return &v }()")
			return true
		}
	case "Ok":
		if len(args) == 1 {
			g.needResult = true
			var callType types.Type
			if call != nil {
				callType = g.typeOf(call)
			}
			tArg, tErr := g.resultTypeArgsAt(callType, g.typeOf(args[0].Value), false)
			g.body.writef("resultOk[%s, %s](", tArg, tErr)
			g.emitExpr(args[0].Value)
			g.body.write(")")
			return true
		}
	case "Err":
		if len(args) == 1 {
			g.needResult = true
			var callType types.Type
			if call != nil {
				callType = g.typeOf(call)
			}
			tArg, tErr := g.resultTypeArgsAt(callType, g.typeOf(args[0].Value), true)
			g.body.writef("resultErr[%s, %s](", tArg, tErr)
			g.emitExpr(args[0].Value)
			g.body.write(")")
			return true
		}
	}
	return false
}

// emitCallArgList writes a comma-separated list of call arguments
// without surrounding parens. Used by intrinsic rewrites.
func (g *gen) emitCallArgList(args []*ast.Arg) {
	for i, a := range args {
		if i > 0 {
			g.body.write(", ")
		}
		g.emitExpr(a.Value)
	}
}

func (g *gen) parallelMapTypes(call *ast.CallExpr) (itemGo, resultGo string, ok bool) {
	if call == nil || len(call.Args) != 3 {
		return "", "", false
	}
	itemsT := g.typeOf(call.Args[0].Value)
	n, ok := itemsT.(*types.Named)
	if !ok || n.Sym == nil || n.Sym.Name != "List" || len(n.Args) != 1 {
		return "", "", false
	}
	itemGo = g.goType(n.Args[0])
	resultGo = "any"
	if t := g.typeOf(call); t != nil {
		if ln, ok := t.(*types.Named); ok && ln.Sym != nil && ln.Sym.Name == "List" && len(ln.Args) == 1 {
			resultGo = g.goType(ln.Args[0])
		}
	}
	return itemGo, resultGo, true
}

// handleInnerTypeFromCall inspects a spawn/spawn-like call's inferred
// Handle<T> return and returns (goType for T, isUnit). isUnit==true
// means the caller must wrap the closure in a struct{}-returning
// trampoline.
func (g *gen) handleInnerTypeFromCall(call *ast.CallExpr) (string, bool) {
	if call == nil {
		return "any", false
	}
	t := g.typeOf(call)
	if t == nil {
		return "any", false
	}
	n, ok := t.(*types.Named)
	if !ok || n.Sym == nil || n.Sym.Name != "Handle" || len(n.Args) != 1 {
		return "any", false
	}
	if types.IsUnit(n.Args[0]) {
		return "struct{}", true
	}
	return g.goType(n.Args[0]), false
}

// emitSpawnClosure emits the `body` argument of spawn / parallel,
// wrapping unit-returning closures in a `func() struct{} { ...(); return struct{}{} }`
// trampoline so it satisfies the runtime's `func() T` signature.
func (g *gen) emitSpawnClosure(e ast.Expr, isUnit bool) {
	if !isUnit {
		g.emitExpr(e)
		return
	}
	g.body.write("func() struct{} { ")
	g.emitExpr(e)
	g.body.write("(); return struct{}{} }")
}

// emitThreadSelect lowers `thread.select(|s| { ... })` to a Go
// `select { ... }` statement wrapped in an IIFE.
//
// Each statement in the closure body is expected to be a method call
// on the selector binding (`s.recv`, `s.send`, `s.timeout`, `s.default`).
// Anything else is preserved verbatim as a Go stmt inside the IIFE,
// which may or may not be what the author intended — but forbidding
// it outright would be too strict for a v0 MVP.
//
// Returns false when the call shape doesn't match `thread.select(...)`,
// letting the generic call emitter take over.
func (g *gen) emitThreadSelect(c *ast.CallExpr, f *ast.FieldExpr) bool {
	head, ok := f.X.(*ast.Ident)
	if !ok || head.Name != "thread" || f.Name != "select" {
		return false
	}
	if len(c.Args) != 1 {
		return false
	}
	cl, ok := c.Args[0].Value.(*ast.ClosureExpr)
	if !ok {
		return false
	}
	blk, ok := cl.Body.(*ast.Block)
	if !ok {
		return false
	}
	g.body.writeln("func() {")
	g.body.indent()
	g.body.writeln("select {")
	for _, s := range blk.Stmts {
		g.emitSelectArm(s)
	}
	g.body.writeln("}")
	g.body.dedent()
	g.body.write("}()")
	return true
}

// emitSelectArm translates one `s.<kind>(...)` statement inside a
// thread.select closure into a `case .../default:` arm.
func (g *gen) emitSelectArm(s ast.Stmt) {
	es, ok := s.(*ast.ExprStmt)
	if !ok {
		return
	}
	call, ok := es.X.(*ast.CallExpr)
	if !ok {
		return
	}
	fx, ok := call.Fn.(*ast.FieldExpr)
	if !ok {
		return
	}
	switch fx.Name {
	case "recv":
		// s.recv(ch, |v| body) — case v, ok := <-ch: if ok { body(v) }
		if len(call.Args) != 2 {
			return
		}
		tmp := g.freshVar("_v")
		okV := g.freshVar("_ok")
		g.body.writef("case %s, %s := <-", tmp, okV)
		g.emitExpr(call.Args[0].Value)
		g.body.writef(":\n")
		g.body.indent()
		g.body.writef("if %s {\n", okV)
		g.body.indent()
		g.emitInvokeClosureArg(call.Args[1].Value, tmp)
		g.body.dedent()
		g.body.writeln("}")
		g.body.dedent()
	case "send":
		// s.send(ch, val, || body) — case ch <- val: body()
		if len(call.Args) != 3 {
			return
		}
		g.body.write("case ")
		g.emitExpr(call.Args[0].Value)
		g.body.write(" <- ")
		g.emitExpr(call.Args[1].Value)
		g.body.writeln(":")
		g.body.indent()
		g.emitInvokeClosureArg(call.Args[2].Value)
		g.body.dedent()
	case "timeout":
		// s.timeout(dur, || body) — case <-time.After(dur): body()
		if len(call.Args) != 2 {
			return
		}
		g.use("time")
		g.body.write("case <-time.After(")
		g.emitDurationExpr(call.Args[0].Value)
		g.body.writeln("):")
		g.body.indent()
		g.emitInvokeClosureArg(call.Args[1].Value)
		g.body.dedent()
	case "default":
		// s.default(|| body) — default: body()
		if len(call.Args) != 1 {
			return
		}
		g.body.writeln("default:")
		g.body.indent()
		g.emitInvokeClosureArg(call.Args[0].Value)
		g.body.dedent()
	}
}

// emitInvokeClosureArg emits `(<closure>)(args...)` — synthesising an
// immediate call of the closure expression passed as an argument.
// If the closure is a literal (ClosureExpr), its body is inlined
// directly to avoid a redundant trampoline. Additional arg names are
// passed positionally.
func (g *gen) emitInvokeClosureArg(e ast.Expr, argNames ...string) {
	if cl, ok := e.(*ast.ClosureExpr); ok {
		// Inline: bind each closure param to the supplied arg name,
		// then emit the body as statements.
		for i, p := range cl.Params {
			if i < len(argNames) && p.Name != "" {
				g.body.writef("%s := %s\n_ = %s\n",
					mangleIdent(p.Name), argNames[i], mangleIdent(p.Name))
			}
		}
		if b, ok := cl.Body.(*ast.Block); ok {
			g.emitStmts(b.Stmts)
			return
		}
		g.emitExpr(cl.Body)
		g.body.nl()
		return
	}
	// Generic: treat as a callable value.
	g.body.write("(")
	g.emitExpr(e)
	g.body.write(")(")
	for i, n := range argNames {
		if i > 0 {
			g.body.write(", ")
		}
		g.body.write(n)
	}
	g.body.writeln(")")
}

// emitDurationExpr emits an expression expected to evaluate to a
// time.Duration. Osty's `N.s` / `N.ms` / `N.us` / `N.ns` duration-
// literal shorthand is rewritten to `time.Second` / `time.Millisecond`
// / `time.Microsecond` / `time.Nanosecond` here so the Go code
// compiles. Everything else passes through verbatim.
func (g *gen) emitDurationExpr(e ast.Expr) {
	if f, ok := e.(*ast.FieldExpr); ok {
		if lit, ok := f.X.(*ast.IntLit); ok {
			unit := ""
			switch f.Name {
			case "s", "sec", "seconds":
				unit = "time.Second"
			case "ms", "millis":
				unit = "time.Millisecond"
			case "us", "micros":
				unit = "time.Microsecond"
			case "ns", "nanos":
				unit = "time.Nanosecond"
			case "min", "minutes":
				unit = "time.Minute"
			case "h", "hours":
				unit = "time.Hour"
			}
			if unit != "" {
				g.use("time")
				g.body.writef("%s*%s", lit.Text, unit)
				return
			}
		}
	}
	g.emitExpr(e)
}

// emitRangeExpr lowers a standalone range literal (`0..10`, `..=N`,
// `100..`, `..`) to a Range value. The runtime Range type is injected
// at the top of the file by the gen driver; for-in heads bypass this
// and lower to C-style loops directly.
func (g *gen) emitRangeExpr(r *ast.RangeExpr) {
	g.needRange = true
	g.body.write("Range{")
	first := true
	if r.Start != nil {
		g.body.write("Start: ")
		g.emitExpr(r.Start)
		g.body.write(", HasStart: true")
		first = false
	}
	if r.Stop != nil {
		if !first {
			g.body.write(", ")
		}
		g.body.write("Stop: ")
		g.emitExpr(r.Stop)
		g.body.write(", HasStop: true")
		first = false
	}
	if r.Inclusive {
		if !first {
			g.body.write(", ")
		}
		g.body.write("Inclusive: true")
	}
	g.body.write("}")
}

// emitTupleExpr lowers `(a, b, c)` to a Go anonymous struct literal
// `struct{F0 T0; F1 T1; F2 T2}{F0: a, F1: b, F2: c}`. Field access at
// the tuple type is the checker's job; at the expression level we rely
// on positional Fi naming, which matches what enum variant structs
// already use.
func (g *gen) emitTupleExpr(tup *ast.TupleExpr) {
	// Prefer element types from the checker, fall back to any.
	types_ := make([]string, len(tup.Elems))
	for i, e := range tup.Elems {
		if t := g.typeOf(e); t != nil {
			types_[i] = g.goType(t)
		} else {
			types_[i] = "any"
		}
	}
	g.body.write("struct{")
	for i, tp := range types_ {
		if i > 0 {
			g.body.write("; ")
		}
		g.body.writef("F%d %s", i, tp)
	}
	g.body.write("}{")
	for i, e := range tup.Elems {
		if i > 0 {
			g.body.write(", ")
		}
		g.body.writef("F%d: ", i)
		g.emitExpr(e)
	}
	g.body.write("}")
}

// emitBlockAsExpr lowers a block used in value position (e.g.
// `let x = { ...; last }`) to an IIFE whose return type follows the
// checker's inferred type for the block. The final expression of the
// block becomes the IIFE's return.
func (g *gen) emitBlockAsExpr(b *ast.Block) {
	retType := "any"
	if t := g.typeOf(b); t != nil && !types.IsError(t) && !types.IsUnit(t) {
		retType = g.goType(t)
	}
	g.body.writef("func() %s ", retType)
	g.emitBlockAsReturn(b, true)
	g.body.write("()")
}

// closurePatternFallbackType synthesises a Go type for a closure param
// whose pattern lacks an annotation and the checker couldn't infer. A
// tuple pattern maps to an anonymous struct of int fields (matches the
// int-default fallback for scalar closure params); anything else falls
// back to `int`.
func closurePatternFallbackType(p ast.Pattern) string {
	tp, ok := p.(*ast.TuplePat)
	if !ok {
		return "int"
	}
	var b strings.Builder
	b.WriteString("struct{")
	for i := range tp.Elems {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString("F")
		b.WriteString(strconv.Itoa(i))
		b.WriteString(" int")
	}
	b.WriteByte('}')
	return b.String()
}

// emitQuestion lowers `expr?` used in expression position.
//
// When the enclosing statement has run the pre-lift pass, it records
// a substitution (`tmp.Value` for Result, `*tmp` for Option) in
// g.questionSubs and we write that directly. Otherwise we fall back
// to an IIFE that panics on failure — only correct inside a context
// where the caller has proved the branch is unreachable.
func (g *gen) emitQuestion(q *ast.QuestionExpr) {
	if sub, ok := g.questionSubs[q]; ok {
		g.body.write(sub)
		return
	}
	inner := g.typeOf(q)
	innerType := "any"
	if inner != nil {
		innerType = g.goType(inner)
	}
	// Heuristic fallback: the operand is assumed to be an Optional.
	// This is safe only when caller code has already handled the None
	// branch (typically via statement-position lifting).
	g.body.writef("func() %s { r := ", innerType)
	g.emitExpr(q.X)
	g.body.writef(`; if r == nil { panic("? propagation at non-lifted position") }; return *r }()`)
}

// emitField writes `x.Name` or `x?.Name`.
//
// Optional chaining (`x?.field`) lowers to a guarded dereference whose
// result is still an Option. The Go form is:
//
//	func() *Field {
//	    if x != nil {
//	        v := (*x).field
//	        return &v
//	    }
//	    return nil
//	}()
//
// Field-type lookup comes from the checker when available.
func (g *gen) emitField(f *ast.FieldExpr) {
	if !f.IsOptional {
		if g.emitQualifiedOptionField(f) {
			return
		}
		if g.emitStdlibMathField(f) {
			return
		}
		// Numeric literals need parens to disambiguate from float
		// literals: `5.s` would be lexed as `5.` + `s` by Go. The
		// Osty spec uses `5.s` for duration-literal shorthand
		// (§10.time); the rest of the semantics are Phase 5.
		needParen := false
		switch f.X.(type) {
		case *ast.IntLit, *ast.FloatLit:
			needParen = true
		}
		if needParen {
			g.body.write("(")
			g.emitExpr(f.X)
			g.body.write(")")
		} else {
			g.emitExpr(f.X)
		}
		g.body.write(".")
		g.body.write(mangleIdent(f.Name))
		return
	}
	inner := "any"
	if t := g.typeOf(f); t != nil {
		if opt, ok := t.(*types.Optional); ok {
			inner = g.goType(opt.Inner)
		} else {
			inner = g.goType(t)
		}
	}
	g.body.writef("func() *%s { if ", inner)
	g.emitExpr(f.X)
	g.body.write(" != nil { v := (*")
	g.emitExpr(f.X)
	g.body.writef(").%s; return &v }; return nil }()", mangleIdent(f.Name))
}

func (g *gen) emitQualifiedOptionField(f *ast.FieldExpr) bool {
	id, ok := f.X.(*ast.Ident)
	if !ok || !g.isStdlibPackageAlias(id, "option") || f.Name != "None" {
		return false
	}
	g.body.write("nil")
	return true
}

// emitList writes a list literal. Element type comes from the checker
// when available; otherwise we default to `any`.
func (g *gen) emitList(l *ast.ListExpr) {
	elemType := "any"
	if t := g.typeOf(l); t != nil {
		if n, ok := t.(*types.Named); ok && n.Sym != nil && n.Sym.Name == "List" && len(n.Args) == 1 {
			elemType = g.goType(n.Args[0])
		}
	}
	if len(l.Elems) == 0 {
		g.body.writef("make([]%s, 0, 1)", elemType)
		return
	}
	g.body.writef("[]%s{", elemType)
	for i, e := range l.Elems {
		if i > 0 {
			g.body.write(", ")
		}
		g.emitExpr(e)
	}
	g.body.write("}")
}

// emitMap writes a map literal.
func (g *gen) emitMap(m *ast.MapExpr) {
	kType, vType := "any", "any"
	if t := g.typeOf(m); t != nil {
		if n, ok := t.(*types.Named); ok && n.Sym != nil && n.Sym.Name == "Map" && len(n.Args) == 2 {
			kType = g.goType(n.Args[0])
			vType = g.goType(n.Args[1])
		}
	}
	g.body.writef("map[%s]%s{", kType, vType)
	for i, e := range m.Entries {
		if i > 0 {
			g.body.write(", ")
		}
		g.emitExpr(e.Key)
		g.body.write(": ")
		g.emitExpr(e.Value)
	}
	g.body.write("}")
}

// emitIfLetExpr lowers `if let pattern = scrut { then } else { else }`
// used in expression position. Delegates to the match infrastructure
// for the pattern test + bindings so every pattern form (Some, Ok,
// VariantPat, ...) comes for free.
func (g *gen) emitIfLetExpr(ie *ast.IfExpr) {
	retType := "any"
	if t := g.typeOf(ie); t != nil && !types.IsError(t) && !types.IsUnit(t) {
		retType = g.goType(t)
	}
	g.body.writef("func() %s {\n", retType)
	g.body.indent()
	g.emitIfLetInner(ie, true)
	g.body.dedent()
	g.body.write("}()")
}

func structLitFieldName(typeName, field string) string {
	if typeName == "ostyBasicError" && field == "message" {
		return "messageText"
	}
	return mangleIdent(field)
}

// emitIfLetStmt lowers `if let ... = ... { ... } else { ... }` at
// statement position (no return lift).
func (g *gen) emitIfLetStmt(ie *ast.IfExpr) {
	g.emitIfLetInner(ie, false)
}

// emitIfLetInner writes the `if …; bindings; …` block. When asExpr is
// true both the then and else branches lift their final expression
// into `return`.
func (g *gen) emitIfLetInner(ie *ast.IfExpr, asExpr bool) {
	tmp := g.freshVar("_il")
	g.body.writef("%s := ", tmp)
	g.emitExpr(ie.Cond)
	g.body.writef("\n_ = %s\n", tmp)
	scrutT := g.typeOf(ie.Cond)
	g.body.write("if ")
	g.emitPatternTest(tmp, scrutT, ie.Pattern)
	g.body.writeln(" {")
	g.body.indent()
	g.emitPatternBindings(tmp, scrutT, ie.Pattern)
	if asExpr {
		g.body.write("return ")
		g.emitArmBody(ie.Then)
		g.body.nl()
	} else {
		for _, s := range ie.Then.Stmts {
			g.emitStmt(s)
		}
	}
	g.body.dedent()
	switch els := ie.Else.(type) {
	case nil:
		g.body.writeln("}")
	case *ast.Block:
		g.body.writeln("} else {")
		g.body.indent()
		if asExpr {
			g.body.write("return ")
			g.emitArmBody(els)
			g.body.nl()
		} else {
			for _, s := range els.Stmts {
				g.emitStmt(s)
			}
		}
		g.body.dedent()
		g.body.writeln("}")
	case *ast.IfExpr:
		g.body.writeln("} else {")
		g.body.indent()
		g.emitIfLetInner(els, asExpr)
		g.body.dedent()
		g.body.writeln("}")
	default:
		g.body.writeln("} else {")
		g.body.indent()
		if asExpr {
			g.body.write("return ")
			g.emitExpr(els)
			g.body.nl()
		} else {
			g.emitExpr(els)
			g.body.nl()
		}
		g.body.dedent()
		g.body.writeln("}")
	}
}

// emitIfExpr lowers an Osty `if` used as an expression. The result is
// an IIFE whose return type comes from the type checker (or `any` when
// we have no type info).
//
//	if c { 1 } else { 2 }
//
// becomes
//
//	func() int {
//	    if c { return 1 }
//	    return 2
//	}()
//
// Plain `if c { ... }` without `else` in expression position defaults
// to returning the Go zero value of the inferred type.
func (g *gen) emitIfExpr(ie *ast.IfExpr) {
	if ie.IsIfLet {
		g.emitIfLetExpr(ie)
		return
	}
	retType := "any"
	if t := g.typeOf(ie); t != nil && !types.IsError(t) && !types.IsUnit(t) {
		retType = g.goType(t)
	}
	g.body.writef("func() %s {", retType)
	g.emitIfChain(ie, true)
	g.body.write("}()")
}

// emitIfChain writes an if (possibly with else-if / else branches).
// When retAsExpr is true, each terminal block's final expression is
// lifted into `return <expr>`. When false, the block runs as a
// plain statement group.
func (g *gen) emitIfChain(ie *ast.IfExpr, retAsExpr bool) {
	g.body.write(" if ")
	g.emitExpr(ie.Cond)
	g.body.write(" ")
	g.emitBlockMaybeReturn(ie.Then, retAsExpr)
	switch els := ie.Else.(type) {
	case nil:
		// no else
	case *ast.IfExpr:
		g.body.write(" else")
		g.emitIfChain(els, retAsExpr)
	case *ast.Block:
		g.body.write(" else ")
		g.emitBlockMaybeReturn(els, retAsExpr)
	default:
		g.body.write(" else { return ")
		g.emitExpr(els)
		g.body.write(" }")
	}
}

// emitBlockMaybeReturn writes `{ stmts; [return lastExpr] }` without a
// trailing newline. When retAsExpr is false, the block emits stmts only.
func (g *gen) emitBlockMaybeReturn(b *ast.Block, retAsExpr bool) {
	if !retAsExpr {
		g.emitBlockInline(b)
		return
	}
	g.body.writeln("{")
	g.body.indent()
	stmts := b.Stmts
	if len(stmts) > 0 {
		last := stmts[len(stmts)-1]
		if es, ok := last.(*ast.ExprStmt); ok {
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

// emitIfStmt renders an Osty `if` as a Go `if` statement — used when
// the `if` appears at statement position (its value is discarded).
func (g *gen) emitIfStmt(ie *ast.IfExpr) {
	if ie.IsIfLet {
		g.emitIfLetStmt(ie)
		return
	}
	g.body.write("if ")
	g.emitExpr(ie.Cond)
	g.body.write(" ")
	g.emitBlockInline(ie.Then)
	switch els := ie.Else.(type) {
	case nil:
		g.body.nl()
	case *ast.IfExpr:
		g.body.write(" else ")
		g.emitIfStmt(els)
	case *ast.Block:
		g.body.write(" else ")
		g.emitBlockInline(els)
		g.body.nl()
	default:
		g.body.write(" else { ")
		g.emitExpr(els)
		g.body.writeln(" }")
	}
}

// emitClosure writes a Go closure. Simple cases map cleanly:
//
//	|x| x * 2                → func(x any) any { return x * 2 }
//	|a: Int, b: Int| a + b   → func(a int, b int) int { return a + b }
//	|x| { let y = 1; x+y }   → func(x any) any { y := 1; return x+y }
//
// Destructuring params (`|(k, v)| k+v`) and inferred parameter types
// from context are Phase 3 work.
func (g *gen) emitClosure(c *ast.ClosureExpr) {
	// Look up the checker's inferred FnType for this closure so
	// untyped params get a real Go type instead of `any`.
	var inferred *types.FnType
	if t := g.typeOf(c); t != nil {
		if fn, ok := t.(*types.FnType); ok {
			inferred = fn
		}
	}

	// Destructured params get synthetic outer names; bindings for the
	// pattern elements are emitted as the first statements of the
	// body. This keeps the Go signature simple and lets pattern
	// complexity accumulate inside the function.
	type paramPlan struct {
		outerName string
		pattern   ast.Pattern
		typ       types.Type
	}
	plans := make([]paramPlan, len(c.Params))

	g.body.write("func(")
	for i, p := range c.Params {
		if i > 0 {
			g.body.write(", ")
		}
		name := p.Name
		if name == "" && p.Pattern != nil {
			name = g.freshVar("_tup")
			plans[i].pattern = p.Pattern
		}
		if name == "" {
			name = "_"
		}
		plans[i].outerName = name
		g.body.write(mangleIdent(name))
		g.body.write(" ")
		switch {
		case p.Type != nil:
			g.body.write(g.goTypeExpr(p.Type))
		case inferred != nil && i < len(inferred.Params) && !types.IsError(inferred.Params[i]):
			plans[i].typ = inferred.Params[i]
			g.body.write(g.goType(inferred.Params[i]))
		case plans[i].pattern != nil:
			// Pattern-without-annotation: synthesise a shape from the
			// pattern (tuple → struct with int fields as default).
			g.body.write(closurePatternFallbackType(plans[i].pattern))
		default:
			// Checker couldn't pin the param type (no hint, no
			// annotation). Default to int — matches Osty's untyped-
			// numeric default and covers the spec's closure examples.
			// A principled fix would infer from body usage.
			g.body.write("int")
		}
	}
	_ = plans // used below when emitting body destructure bindings
	g.body.write(") ")
	// bodyIsVoid reports whether the closure body's tail expression
	// resolves to Unit — used when the checker couldn't pin the return
	// type (e.g. calls into an opaque stdlib package) but the tail is
	// clearly a void-returning call. Without this, the default fallback
	// emits `int` on a closure whose body actually returns nothing,
	// producing invalid Go (`return <void-call>`).
	bodyIsVoid := false
	if blk, ok := c.Body.(*ast.Block); ok {
		if len(blk.Stmts) == 0 {
			bodyIsVoid = true
		} else if es, ok := blk.Stmts[len(blk.Stmts)-1].(*ast.ExprStmt); ok {
			if g.isVoidExpr(es.X) {
				bodyIsVoid = true
			}
		} else {
			bodyIsVoid = true
		}
	}
	switch {
	case c.ReturnType != nil:
		g.body.write(g.goTypeExpr(c.ReturnType))
		g.body.write(" ")
	case inferred != nil && inferred.Return != nil && types.IsUnit(inferred.Return):
		// Unit return — leave the signature as `func(args)`.
	case inferred != nil && inferred.Return != nil && !types.IsError(inferred.Return):
		g.body.write(g.goType(inferred.Return))
		g.body.write(" ")
	case bodyIsVoid:
		// Checker lost the return type but the body is void — treat as unit.
	default:
		// No inferred type and no annotation — default to int for
		// arithmetic-shaped closure bodies (`|x| x * 2`). A truly
		// untyped closure body is a corner case; `int` matches the
		// rest of Osty's untyped-numeric default (§2.2).
		g.body.write("int ")
	}
	closureRetGo := ""
	if c.ReturnType != nil {
		closureRetGo = g.goTypeExpr(c.ReturnType)
	} else if inferred != nil && inferred.Return != nil &&
		!types.IsUnit(inferred.Return) && !types.IsError(inferred.Return) {
		closureRetGo = g.goType(inferred.Return)
	}
	prevRetType := g.currentRetType
	prevRetGo := g.currentRetGo
	g.currentRetType = c.ReturnType
	g.currentRetGo = closureRetGo
	defer func() {
		g.currentRetType = prevRetType
		g.currentRetGo = prevRetGo
	}()
	// Body may be a Block (return needs to come from its final expr)
	// or a single expression (wrap in { return <expr> }). In either
	// case, emit destructure bindings first.
	hasDestructure := false
	for _, pl := range plans {
		if pl.pattern != nil {
			hasDestructure = true
			break
		}
	}
	wantReturn := true
	if c.ReturnType == nil && inferred != nil && inferred.Return != nil &&
		types.IsUnit(inferred.Return) {
		wantReturn = false
	}
	// When the inferred return type is Error/missing but the body is
	// clearly void, treat the closure as unit-returning so we don't
	// wrap the trailing void call in a bogus `return`.
	if c.ReturnType == nil && bodyIsVoid &&
		(inferred == nil || inferred.Return == nil || types.IsError(inferred.Return)) {
		wantReturn = false
	}
	if blk, ok := c.Body.(*ast.Block); ok && !hasDestructure {
		g.emitBlockAsReturn(blk, wantReturn)
		return
	}
	g.body.writeln("{")
	g.body.indent()
	for _, pl := range plans {
		if pl.pattern == nil {
			continue
		}
		g.emitPatternBindings(mangleIdent(pl.outerName), pl.typ, pl.pattern)
	}
	if blk, ok := c.Body.(*ast.Block); ok {
		// Inline the block stmts then return final expr (unless the
		// inferred return is Unit — then just run the stmts).
		stmts := blk.Stmts
		if wantReturn && len(stmts) > 0 {
			last := stmts[len(stmts)-1]
			if es, ok := last.(*ast.ExprStmt); ok {
				for _, s := range stmts[:len(stmts)-1] {
					g.emitStmt(s)
				}
				g.body.write("return ")
				g.emitExpr(es.X)
				g.body.nl()
				g.body.dedent()
				g.body.write("}")
				return
			}
		}
		g.emitStmts(stmts)
		g.body.dedent()
		g.body.write("}")
		return
	}
	if wantReturn {
		g.body.write("return ")
	}
	g.emitExpr(c.Body)
	g.body.nl()
	g.body.dedent()
	g.body.write("}")
}
