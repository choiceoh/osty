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
		g.body.writef("%s(%s_%s{})", owner, owner, id.Name)
		return
	}
	g.body.write(mangleIdent(id.Name))
}

// emitStructLit writes a struct literal. `Self { ... }` is rewritten
// to the enclosing type while emitting a method body.
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
		// Qualified type (pkg.Type) — Phase 5.
		g.emitExpr(s.Type)
	default:
		g.emitExpr(s.Type)
	}
	if typeName != "" {
		g.body.write(typeName)
	}
	g.body.write("{")
	for i, f := range s.Fields {
		if i > 0 {
			g.body.write(", ")
		}
		name := mangleIdent(f.Name)
		g.body.writef("%s: ", name)
		if f.Value == nil {
			// Shorthand `Point { name }` → `Point{name: name}`.
			g.body.write(name)
		} else {
			g.emitExpr(f.Value)
		}
	}
	if s.Spread != nil {
		g.body.write(" /* TODO(phase3): ..spread */ ")
	}
	g.body.write("}")
}

// emitStringLit writes a string literal.
//
// Plain strings (no interpolation) are emitted as Go string literals
// via strconv.Quote. Interpolated strings are rewritten to a
// `fmt.Sprintf` call: each literal segment becomes a quoted run, each
// expression segment becomes a `%v` verb and a trailing argument.
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

	// Interpolated: fmt.Sprintf("...", args...).
	g.use("fmt")
	var format strings.Builder
	var args []ast.Expr
	for _, p := range s.Parts {
		if p.IsLit {
			// Escape `%` in literal runs so fmt treats them as literal.
			format.WriteString(strings.ReplaceAll(p.Lit, "%", "%%"))
		} else {
			format.WriteString("%v")
			args = append(args, p.Expr)
		}
	}
	g.body.writef("fmt.Sprintf(%s", strconv.Quote(format.String()))
	for _, a := range args {
		g.body.write(", ")
		g.emitExpr(a)
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
	op := binaryOp(b.Op)
	g.emitExpr(b.Left)
	g.body.writef(" %s ", op)
	g.emitExpr(b.Right)
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
// Optional (*T), we emit a branchy lookup; otherwise we emit the raw
// expression with a TODO marker so the user can see where the rewrite
// wasn't applied.
func (g *gen) emitCoalesce(b *ast.BinaryExpr) {
	lt := g.typeOf(b.Left)
	if _, ok := lt.(*types.Optional); ok {
		g.body.write("func() ")
		inner := g.goType(lt.(*types.Optional).Inner)
		g.body.writef("%s { if v := ", inner)
		g.emitExpr(b.Left)
		g.body.writef("; v != nil { return *v }; return ")
		g.emitExpr(b.Right)
		g.body.write(" }()")
		return
	}
	// Fallback: ternary-equivalent using a helper. For Phase 1 just
	// emit `a` (assuming non-nil) with a TODO marker.
	g.body.write("/* TODO(phase4): ?? on non-optional */ ")
	g.emitExpr(b.Left)
}

// emitCall writes a function call, applying special rewrites for
// prelude intrinsics (println, print, ...), builtin variant
// constructors (Some, Ok, Err), user enum variant construction
// (`Circle(3.14)` → `Shape_Circle{F0: 3.14}`), enum-method dispatch
// (`shape.area()` → `Shape_area(shape)`), and static/associated
// function calls (`User.new("a")` → `User_new("a")`).
func (g *gen) emitCall(c *ast.CallExpr) {
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
	if f, ok := c.Fn.(*ast.FieldExpr); ok {
		if g.emitStaticCall(f, c.Args) {
			return
		}
		if g.emitEnumMethodCall(f, c.Args) {
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

// emitVariantCtor writes `Shape(Shape_Circle{F0: a0, F1: a1})` for
// `Circle(a0, a1)`. The outer `Enum(...)` conversion forces the value
// to the enum interface type so downstream type assertions work even
// when the value flows through a generic position like a `let` short-form.
func (g *gen) emitVariantCtor(owner, name string, args []*ast.Arg) {
	g.body.writef("%s(%s_%s{", owner, owner, name)
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
			g.body.writef("Result[%s, %s]{Value: ", tArg, tErr)
			g.emitExpr(args[0].Value)
			g.body.write(", IsOk: true}")
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
			g.body.writef("Result[%s, %s]{Error: ", tArg, tErr)
			g.emitExpr(args[0].Value)
			g.body.write("}")
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

// emitList writes a list literal. Element type comes from the checker
// when available; otherwise we default to `any`.
func (g *gen) emitList(l *ast.ListExpr) {
	elemType := "any"
	if t := g.typeOf(l); t != nil {
		if n, ok := t.(*types.Named); ok && n.Sym != nil && n.Sym.Name == "List" && len(n.Args) == 1 {
			elemType = g.goType(n.Args[0])
		}
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
	switch {
	case c.ReturnType != nil:
		g.body.write(g.goTypeExpr(c.ReturnType))
		g.body.write(" ")
	case inferred != nil && inferred.Return != nil && !types.IsUnit(inferred.Return) && !types.IsError(inferred.Return):
		g.body.write(g.goType(inferred.Return))
		g.body.write(" ")
	default:
		g.body.write("int ")
	}
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
	if blk, ok := c.Body.(*ast.Block); ok && !hasDestructure {
		g.emitBlockAsReturn(blk, true)
		return
	}
	g.body.writeln("{")
	g.body.indent()
	for _, pl := range plans {
		if pl.pattern == nil {
			continue
		}
		// Only simple tuple patterns supported here; nested ones fall
		// through to TODO in emitPatternBindings (unused for the spec).
		if tp, ok := pl.pattern.(*ast.TuplePat); ok {
			for i, elem := range tp.Elems {
				switch e := elem.(type) {
				case *ast.WildcardPat:
					// skip
				case *ast.IdentPat:
					g.body.writef("%s := %s.F%d; _ = %s\n",
						mangleIdent(e.Name), pl.outerName, i, mangleIdent(e.Name))
				}
			}
		}
	}
	if blk, ok := c.Body.(*ast.Block); ok {
		// Inline the block stmts then return final expr.
		stmts := blk.Stmts
		if len(stmts) > 0 {
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
	g.body.write("return ")
	g.emitExpr(c.Body)
	g.body.nl()
	g.body.dedent()
	g.body.write("}")
}
