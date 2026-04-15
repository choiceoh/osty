package gen

import (
	"strconv"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/types"
)

// itoa is a tiny helper so type emission can build fieldX strings
// without pulling strconv into every caller.
func itoa(n int) string { return strconv.Itoa(n) }

// goType renders a semantic Type as its Go equivalent.
//
// The mapping is fixed for Phase 1:
//
//	Int, Int8..Int64            → int, int8..int64
//	UInt8..UInt64, Byte         → uint8..uint64, byte
//	Float, Float32, Float64     → float64, float32, float64
//	Bool, Char, String, Bytes   → bool, rune, string, []byte
//	()                          → struct{}
//	Never                       → struct{} (unreachable placeholder)
//	T?                          → *T
//	List<T>                     → []T
//	Map<K, V>                   → map[K]V
//	User-defined Named          → its name verbatim
//	TypeVar T                   → T
//
// Untyped literals are defaulted per §2.2 (UntypedInt→Int→int,
// UntypedFloat→Float→float64). An unresolved Error type yields "any"
// so malformed sources still produce something parseable by gofmt.
func (g *gen) goType(t types.Type) string {
	switch t := t.(type) {
	case nil:
		return "any"
	case *types.Primitive:
		return goPrimitive(t.Kind)
	case *types.Untyped:
		if p, ok := t.Default().(*types.Primitive); ok {
			return goPrimitive(p.Kind)
		}
		return "any"
	case *types.Optional:
		return "*" + g.goType(t.Inner)
	case *types.Tuple:
		// Tuples lower to Go anonymous structs with positional Fi
		// fields. Matches the shape emitTupleExpr produces.
		var b strings.Builder
		b.WriteString("struct{")
		for i, e := range t.Elems {
			if i > 0 {
				b.WriteString("; ")
			}
			b.WriteString("F")
			b.WriteString(itoa(i))
			b.WriteByte(' ')
			b.WriteString(g.goType(e))
		}
		b.WriteByte('}')
		return b.String()
	case *types.FnType:
		return g.goFnType(t)
	case *types.Named:
		return g.goNamedType(t)
	case *types.TypeVar:
		if t.Sym != nil {
			return t.Sym.Name
		}
		return "any"
	case *types.Error:
		return "any"
	}
	return "any"
}

// goPrimitive is the fixed primitive→Go identifier table.
func goPrimitive(k types.PrimitiveKind) string {
	switch k {
	case types.PInt:
		return "int"
	case types.PInt8:
		return "int8"
	case types.PInt16:
		return "int16"
	case types.PInt32:
		return "int32"
	case types.PInt64:
		return "int64"
	case types.PUInt8:
		return "uint8"
	case types.PUInt16:
		return "uint16"
	case types.PUInt32:
		return "uint32"
	case types.PUInt64:
		return "uint64"
	case types.PByte:
		return "byte"
	case types.PFloat:
		return "float64"
	case types.PFloat32:
		return "float32"
	case types.PFloat64:
		return "float64"
	case types.PBool:
		return "bool"
	case types.PChar:
		return "rune"
	case types.PString:
		return "string"
	case types.PBytes:
		return "[]byte"
	case types.PUnit, types.PNever:
		return "struct{}"
	}
	return "any"
}

// goFnType renders a semantic function type. The return component is
// omitted when the result is Unit — Go's natural "no result" form.
func (g *gen) goFnType(f *types.FnType) string {
	var b strings.Builder
	b.WriteString("func(")
	for i, p := range f.Params {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(g.goType(p))
	}
	b.WriteByte(')')
	if f.Return != nil && !types.IsUnit(f.Return) {
		b.WriteByte(' ')
		b.WriteString(g.goType(f.Return))
	}
	return b.String()
}

// goNamedType renders a Named type.
//
// Built-in compound names (List, Map, Set, Option, Result) get their
// canonical Go shape: []T, map[K]V, map[T]struct{}, *T, etc. For
// user-defined types we emit the bare identifier plus type arguments.
func (g *gen) goNamedType(n *types.Named) string {
	if n.Sym == nil {
		return "any"
	}
	switch n.Sym.Name {
	case "List":
		if len(n.Args) == 1 {
			return "[]" + g.goType(n.Args[0])
		}
		return "[]any"
	case "Map":
		if len(n.Args) == 2 {
			return "map[" + g.goType(n.Args[0]) + "]" + g.goType(n.Args[1])
		}
		return "map[any]any"
	case "Set":
		if len(n.Args) == 1 {
			return "map[" + g.goType(n.Args[0]) + "]struct{}"
		}
		return "map[any]struct{}"
	case "Option":
		if len(n.Args) == 1 {
			return "*" + g.goType(n.Args[0])
		}
		return "any"
	case "Result":
		g.needResult = true
		if len(n.Args) == 2 {
			return "Result[" + g.goType(n.Args[0]) + ", " + g.goType(n.Args[1]) + "]"
		}
		return "Result[any, any]"
	case "Error":
		// Osty's prelude `Error` is a structural interface (§7.1) with
		// .message()/.source(). Go's built-in `error` requires
		// .Error() string, which Osty types don't expose. Mapping to
		// `any` accepts widening from concrete error enums at the
		// cost of losing Go's error-interface polymorphism; a full
		// fix would generate an .Error() shim per type.
		return "any"
	}
	// User-defined: bare name + optional type args.
	if len(n.Args) == 0 {
		return n.Sym.Name
	}
	// Generic user type — emit with Go type arg brackets.
	var b strings.Builder
	b.WriteString(n.Sym.Name)
	b.WriteByte('[')
	for i, a := range n.Args {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(g.goType(a))
	}
	b.WriteByte(']')
	return b.String()
}

// goTypeExpr translates an AST type node to its Go type string. Used
// when no semantic Type is available (parameter declarations before
// the checker runs, or lost in an unchecked region of the tree).
func (g *gen) goTypeExpr(t ast.Type) string {
	switch t := t.(type) {
	case nil:
		return ""
	case *ast.NamedType:
		return g.goNamedAST(t)
	case *ast.OptionalType:
		return "*" + g.goTypeExpr(t.Inner)
	case *ast.TupleType:
		var b strings.Builder
		b.WriteString("struct{")
		for i, e := range t.Elems {
			if i > 0 {
				b.WriteString("; ")
			}
			b.WriteString("F")
			b.WriteString(itoa(i))
			b.WriteByte(' ')
			b.WriteString(g.goTypeExpr(e))
		}
		b.WriteByte('}')
		return b.String()
	case *ast.FnType:
		return g.goFnTypeAST(t)
	}
	return "any"
}

// primitiveByName is the fallback path when we only have an AST type
// reference (no resolved Symbol). Mirrors the prelude scalar set.
var primitiveByName = map[string]string{
	"Int":     "int",
	"Int8":    "int8",
	"Int16":   "int16",
	"Int32":   "int32",
	"Int64":   "int64",
	"UInt8":   "uint8",
	"UInt16":  "uint16",
	"UInt32":  "uint32",
	"UInt64":  "uint64",
	"Byte":    "byte",
	"Float":   "float64",
	"Float32": "float32",
	"Float64": "float64",
	"Bool":    "bool",
	"Char":    "rune",
	"String":  "string",
	"Bytes":   "[]byte",
	"Never":   "struct{}",
}

func (g *gen) goNamedAST(n *ast.NamedType) string {
	if len(n.Path) == 0 {
		return "any"
	}
	if len(n.Path) > 1 {
		// Qualified (pkg.Type). Phase 5 adds proper module handling.
		return strings.Join(n.Path, ".")
	}
	name := n.Path[0]
	if gt, ok := primitiveByName[name]; ok {
		return gt
	}
	switch name {
	case "List":
		if len(n.Args) == 1 {
			return "[]" + g.goTypeExpr(n.Args[0])
		}
	case "Map":
		if len(n.Args) == 2 {
			return "map[" + g.goTypeExpr(n.Args[0]) + "]" + g.goTypeExpr(n.Args[1])
		}
	case "Set":
		if len(n.Args) == 1 {
			return "map[" + g.goTypeExpr(n.Args[0]) + "]struct{}"
		}
	case "Option":
		if len(n.Args) == 1 {
			return "*" + g.goTypeExpr(n.Args[0])
		}
	case "Error":
		return "any"
	case "Result":
		g.needResult = true
		if len(n.Args) == 2 {
			return "Result[" + g.goTypeExpr(n.Args[0]) + ", " + g.goTypeExpr(n.Args[1]) + "]"
		}
		return "Result[any, any]"
	}
	if len(n.Args) == 0 {
		return name
	}
	var b strings.Builder
	b.WriteString(name)
	b.WriteByte('[')
	for i, a := range n.Args {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(g.goTypeExpr(a))
	}
	b.WriteByte(']')
	return b.String()
}

func (g *gen) goFnTypeAST(f *ast.FnType) string {
	var b strings.Builder
	b.WriteString("func(")
	for i, p := range f.Params {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(g.goTypeExpr(p))
	}
	b.WriteByte(')')
	// Unit return (`-> ()`) maps to no Go return clause — `func()`
	// rather than `func() struct{}`. The latter is technically a
	// valid Go type but is incompatible with `func() {…}` literals
	// the body emitter produces, so closures passed by value would
	// fail to compile.
	if f.ReturnType != nil && !isUnitAST(f.ReturnType) {
		b.WriteByte(' ')
		b.WriteString(g.goTypeExpr(f.ReturnType))
	}
	return b.String()
}

// isUnitAST reports whether t is the AST representation of the Osty
// unit type — a TupleType with no elements (`()`). Function-type
// returns and parameter types both check this so the synthesized Go
// signature stays compatible with `func()` closure literals.
func isUnitAST(t ast.Type) bool {
	tt, ok := t.(*ast.TupleType)
	return ok && len(tt.Elems) == 0
}
