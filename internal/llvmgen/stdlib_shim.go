// stdlib_shim.go — targeted backend shims for Osty surface the pure-Osty
// lowering path can't yet handle.
//
//   1. Qualified calls through `use std.strings as X` routed to the
//      `osty_rt_strings_*` C runtime helpers. The pure Osty bodies (see
//      internal/stdlib/modules/strings.osty) depend on Char iteration /
//      List<Char> indexing which the backend doesn't lower yet.
//
//   2. Bare `None` / `Some(x)` construction for ptr-backed Option<T>. The
//      backend already encodes `T?` as a nullable `ptr` for every `T` (see
//      llvmType), so `None` → `null` and `Some(x)` → pass-through when x
//      is `ptr`. Scalar-backed Option<Int> / Option<Bool> still need
//      boxing and stay unsupported here.
//
// Both shims retire once the flag-gated stdlib-body injection + richer
// Option codegen land.
package llvmgen

import (
	"github.com/osty/osty/internal/ast"
)

func collectStdStringsAliases(file *ast.File) map[string]bool {
	out := map[string]bool{}
	if file == nil {
		return out
	}
	for _, use := range file.Uses {
		if use == nil || use.IsFFI() {
			continue
		}
		if len(use.Path) != 2 || use.Path[0] != "std" || use.Path[1] != "strings" {
			continue
		}
		alias := use.Alias
		if alias == "" {
			alias = "strings"
		}
		out[alias] = true
	}
	return out
}

func (g *generator) emitStdStringsCall(call *ast.CallExpr) (value, bool, error) {
	if call == nil || len(g.stdStringsAliases) == 0 {
		return value{}, false, nil
	}
	field, ok := call.Fn.(*ast.FieldExpr)
	if !ok {
		return value{}, false, nil
	}
	alias, ok := field.X.(*ast.Ident)
	if !ok || !g.stdStringsAliases[alias.Name] {
		return value{}, false, nil
	}
	switch field.Name {
	case "compare":
		v, err := g.emitStdStringsBinary(call, "compare", "i64", llvmStringRuntimeCompareSymbol())
		return v, true, err
	case "hasPrefix":
		v, err := g.emitStdStringsBinary(call, "hasPrefix", "i1", llvmStringRuntimeHasPrefixSymbol())
		return v, true, err
	case "join":
		v, err := g.emitStdStringsJoin(call)
		return v, true, err
	case "split":
		v, err := g.emitStdStringsSplit(call)
		return v, true, err
	case "trim", "trimSpace":
		v, err := g.emitStdStringsUnary(call, field.Name, llvmStringRuntimeTrimSpaceSymbol())
		return v, true, err
	}
	return value{}, false, nil
}

func (g *generator) emitStdStringsSplit(call *ast.CallExpr) (value, error) {
	if len(call.Args) != 2 {
		return value{}, unsupportedf("call", "strings.split expects 2 arguments, got %d", len(call.Args))
	}
	s, err := g.emitStdStringsArg(call.Args[0], "split", 0)
	if err != nil {
		return value{}, err
	}
	sep, err := g.emitStdStringsArg(call.Args[1], "split", 1)
	if err != nil {
		return value{}, err
	}
	symbol := "osty_rt_strings_Split"
	g.declareRuntimeSymbol(symbol, "ptr", []paramInfo{{typ: "ptr"}, {typ: "ptr"}})
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, "ptr", symbol, []*LlvmValue{toOstyValue(s), toOstyValue(sep)})
	g.takeOstyEmitter(emitter)
	parts := fromOstyValue(out)
	parts.gcManaged = true
	parts.listElemTyp = "ptr"
	parts.listElemString = true
	return parts, nil
}

// stdStringsCallStaticResult mirrors runtimeFFICallTarget for the std.strings
// shim — returns the static `value` shape for a call we know we'll route to
// the runtime, so callers like staticExprInfo can downstream-classify the
// result (List<String>, Bool, Int, ...). Returns ok=false for calls we don't
// shim.
func (g *generator) stdStringsCallStaticResult(call *ast.CallExpr) (value, bool) {
	if call == nil || len(g.stdStringsAliases) == 0 {
		return value{}, false
	}
	field, ok := call.Fn.(*ast.FieldExpr)
	if !ok {
		return value{}, false
	}
	alias, ok := field.X.(*ast.Ident)
	if !ok || !g.stdStringsAliases[alias.Name] {
		return value{}, false
	}
	switch field.Name {
	case "compare":
		return value{typ: "i64"}, true
	case "hasPrefix":
		return value{typ: "i1"}, true
	case "join", "trim", "trimSpace":
		return value{typ: "ptr", gcManaged: true}, true
	case "split":
		return value{typ: "ptr", gcManaged: true, listElemTyp: "ptr", listElemString: true}, true
	}
	return value{}, false
}

// coerceToInterpolationString converts an interpolated subexpression into a
// String (ptr) value. ptr values pass through; i64 / i1 are funneled through
// `osty_rt_int_to_string` / `osty_rt_bool_to_string`. Aggregate / collection
// types stay rejected — they need real toString() lowering.
func (g *generator) coerceToInterpolationString(v value) (value, bool) {
	if v.typ == "ptr" && v.listElemTyp == "" && v.mapKeyTyp == "" && v.setElemTyp == "" {
		return v, true
	}
	switch v.typ {
	case "i64":
		symbol := llvmIntToStringSymbol()
		g.declareRuntimeSymbol(symbol, "ptr", []paramInfo{{typ: "i64"}})
		emitter := g.toOstyEmitter()
		out := llvmIntToString(emitter, toOstyValue(v))
		g.takeOstyEmitter(emitter)
		coerced := fromOstyValue(out)
		coerced.gcManaged = true
		return coerced, true
	case "i1":
		symbol := llvmBoolToStringSymbol()
		g.declareRuntimeSymbol(symbol, "ptr", []paramInfo{{typ: "i1"}})
		emitter := g.toOstyEmitter()
		out := llvmBoolToString(emitter, toOstyValue(v))
		g.takeOstyEmitter(emitter)
		coerced := fromOstyValue(out)
		coerced.gcManaged = true
		return coerced, true
	}
	return value{}, false
}

func (g *generator) emitStdStringsUnary(call *ast.CallExpr, name, symbol string) (value, error) {
	if len(call.Args) != 1 {
		return value{}, unsupportedf("call", "strings.%s expects 1 argument, got %d", name, len(call.Args))
	}
	s, err := g.emitStdStringsArg(call.Args[0], name, 0)
	if err != nil {
		return value{}, err
	}
	g.declareRuntimeSymbol(symbol, "ptr", []paramInfo{{typ: "ptr"}})
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, "ptr", symbol, []*LlvmValue{toOstyValue(s)})
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.gcManaged = true
	return v, nil
}

func (g *generator) emitStdStringsJoin(call *ast.CallExpr) (value, error) {
	if len(call.Args) != 2 {
		return value{}, unsupportedf("call", "strings.join expects 2 arguments, got %d", len(call.Args))
	}
	if call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil ||
		call.Args[1] == nil || call.Args[1].Name != "" || call.Args[1].Value == nil {
		return value{}, unsupportedf("call", "strings.join requires positional arguments")
	}
	parts, err := g.emitExprWithHintAndSourceType(call.Args[0].Value, nil, "ptr", true, "", "", false, "", false)
	if err != nil {
		return value{}, err
	}
	parts, err = g.loadIfPointer(parts)
	if err != nil {
		return value{}, err
	}
	if parts.typ != "ptr" {
		return value{}, unsupportedf("type-system", "strings.join arg 1 type %s, want List<String>", parts.typ)
	}
	sep, err := g.emitStdStringsArg(call.Args[1], "join", 1)
	if err != nil {
		return value{}, err
	}
	symbol := llvmStringRuntimeJoinSymbol()
	g.declareRuntimeSymbol(symbol, "ptr", []paramInfo{{typ: "ptr"}, {typ: "ptr"}})
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, "ptr", symbol, []*LlvmValue{toOstyValue(parts), toOstyValue(sep)})
	g.takeOstyEmitter(emitter)
	joined := fromOstyValue(out)
	joined.gcManaged = true
	return joined, nil
}

func (g *generator) emitStdStringsBinary(call *ast.CallExpr, name, retTyp, symbol string) (value, error) {
	if len(call.Args) != 2 {
		return value{}, unsupportedf("call", "strings.%s expects 2 arguments, got %d", name, len(call.Args))
	}
	left, err := g.emitStdStringsArg(call.Args[0], name, 0)
	if err != nil {
		return value{}, err
	}
	right, err := g.emitStdStringsArg(call.Args[1], name, 1)
	if err != nil {
		return value{}, err
	}
	g.declareRuntimeSymbol(symbol, retTyp, []paramInfo{{typ: "ptr"}, {typ: "ptr"}})
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, retTyp, symbol, []*LlvmValue{toOstyValue(left), toOstyValue(right)})
	g.takeOstyEmitter(emitter)
	return fromOstyValue(out), nil
}

func (g *generator) emitStdStringsArg(arg *ast.Arg, name string, index int) (value, error) {
	if arg == nil || arg.Name != "" || arg.Value == nil {
		return value{}, unsupportedf("call", "strings.%s requires positional arguments", name)
	}
	v, err := g.emitExpr(arg.Value)
	if err != nil {
		return value{}, err
	}
	loaded, err := g.loadIfPointer(v)
	if err != nil {
		return value{}, err
	}
	if loaded.typ != "ptr" {
		return value{}, unsupportedf("type-system", "strings.%s arg %d type %s, want String", name, index+1, loaded.typ)
	}
	return loaded, nil
}

func (g *generator) currentBuiltinOptionContext() (builtinOptionContext, bool) {
	if n := len(g.optionContexts); n != 0 {
		return g.optionContexts[n-1], true
	}
	return builtinOptionContext{}, false
}

// emitBuiltinOptionNone lowers a bare `None` identifier to a null ptr when
// the enclosing sourceType is `T?`. Returns found=true iff name=="None" and
// context is ptr-backed Option.
func (g *generator) emitBuiltinOptionNone(name string) (value, bool, error) {
	if name != "None" {
		return value{}, false, nil
	}
	ctx, ok := g.currentBuiltinOptionContext()
	if !ok {
		return value{}, false, nil
	}
	out := value{typ: "ptr", ref: "null", sourceType: ctx.sourceType}
	out.rootPaths = g.rootPathsForType(out.typ)
	return out, true, nil
}

// emitBuiltinOptionSomeCall handles `Some(x)` calls in a ptr-backed Option
// context. x must already be a ptr; the backend pass-throughs since T? and T
// share the `ptr` LLVM type (scalar Option<Int> stays unsupported).
func (g *generator) emitBuiltinOptionSomeCall(call *ast.CallExpr) (value, bool, error) {
	if call == nil {
		return value{}, false, nil
	}
	id, ok := call.Fn.(*ast.Ident)
	if !ok || id.Name != "Some" {
		return value{}, false, nil
	}
	ctx, ok := g.currentBuiltinOptionContext()
	if !ok {
		return value{}, false, nil
	}
	if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
		return value{}, true, unsupportedf("call", "Some requires one positional argument")
	}
	v, err := g.emitExprWithHintAndSourceType(call.Args[0].Value, ctx.inner, "", false, "", "", false, "", false)
	if err != nil {
		return value{}, true, err
	}
	loaded, err := g.loadIfPointer(v)
	if err != nil {
		return value{}, true, err
	}
	if loaded.typ != "ptr" {
		return value{}, true, unsupportedf("type-system", "Some payload type %s requires boxed Option; only ptr-backed Some(...) is lowered", loaded.typ)
	}
	loaded.sourceType = ctx.sourceType
	return loaded, true, nil
}
