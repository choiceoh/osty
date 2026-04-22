// stdlib_shim.go — targeted backend shims for Osty surface the pure-Osty
// lowering path can't yet handle.
//
//  1. Qualified calls through `use std.strings as X` / `use std.bytes
//     as X` routed to the runtime helpers the backend already knows how
//     to lower. The pure Osty bodies (see internal/stdlib/modules) lean
//     on method surfaces the legacy backend still only partially
//     handles.
//
//  2. Bare `None` / `Some(x)` construction for ptr-backed Option<T>. The
//     backend already encodes `T?` as a nullable `ptr` for every `T` (see
//     llvmType), so `None` → `null` and `Some(x)` → pass-through when x
//     is `ptr`. Scalar-backed Option<Int> / Option<Bool> still need
//     boxing and stay unsupported here.
//
// Both shims retire once the flag-gated stdlib-body injection + richer
// Option codegen land.
package llvmgen

import (
	"fmt"
	"strings"

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

func collectStdBytesAliases(file *ast.File) map[string]bool {
	out := map[string]bool{}
	if file == nil {
		return out
	}
	for _, use := range file.Uses {
		if use == nil || use.IsFFI() {
			continue
		}
		if len(use.Path) != 2 || use.Path[0] != "std" || use.Path[1] != "bytes" {
			continue
		}
		alias := use.Alias
		if alias == "" {
			alias = "bytes"
		}
		out[alias] = true
	}
	return out
}

func (g *generator) emitStdBytesCall(call *ast.CallExpr) (value, bool, error) {
	if call == nil || len(g.stdBytesAliases) == 0 {
		return value{}, false, nil
	}
	field, ok := call.Fn.(*ast.FieldExpr)
	if !ok {
		return value{}, false, nil
	}
	alias, ok := field.X.(*ast.Ident)
	if !ok || !g.stdBytesAliases[alias.Name] {
		return value{}, false, nil
	}
	switch field.Name {
	case "from":
		if len(call.Args) != 1 {
			return value{}, true, unsupportedf("call", "bytes.from expects 1 argument, got %d", len(call.Args))
		}
		items, err := g.emitStdBytesListArg(call.Args[0], "from", 0)
		if err != nil {
			return value{}, true, err
		}
		out, err := g.emitBytesFromListRuntime(items)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "concat":
		if len(call.Args) != 2 {
			return value{}, true, unsupportedf("call", "bytes.concat expects 2 arguments, got %d", len(call.Args))
		}
		left, err := g.emitStdBytesArg(call.Args[0], "concat", 0)
		if err != nil {
			return value{}, true, err
		}
		right, err := g.emitStdBytesArg(call.Args[1], "concat", 1)
		if err != nil {
			return value{}, true, err
		}
		out, err := g.emitBytesConcatRuntime(left, right)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "repeat":
		if len(call.Args) != 2 {
			return value{}, true, unsupportedf("call", "bytes.repeat expects 2 arguments, got %d", len(call.Args))
		}
		b, err := g.emitStdBytesArg(call.Args[0], "repeat", 0)
		if err != nil {
			return value{}, true, err
		}
		n, err := g.emitStdBytesIntArg(call.Args[1], "repeat", 1)
		if err != nil {
			return value{}, true, err
		}
		out, err := g.emitBytesRepeatRuntime(b, n)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "contains":
		if len(call.Args) != 2 {
			return value{}, true, unsupportedf("call", "bytes.contains expects 2 arguments, got %d", len(call.Args))
		}
		b, err := g.emitStdBytesArg(call.Args[0], "contains", 0)
		if err != nil {
			return value{}, true, err
		}
		sub, err := g.emitStdBytesArg(call.Args[1], "contains", 1)
		if err != nil {
			return value{}, true, err
		}
		out, err := g.emitBytesContainsRuntime(b, sub)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "startsWith":
		if len(call.Args) != 2 {
			return value{}, true, unsupportedf("call", "bytes.startsWith expects 2 arguments, got %d", len(call.Args))
		}
		b, err := g.emitStdBytesArg(call.Args[0], "startsWith", 0)
		if err != nil {
			return value{}, true, err
		}
		prefix, err := g.emitStdBytesArg(call.Args[1], "startsWith", 1)
		if err != nil {
			return value{}, true, err
		}
		out, err := g.emitBytesStartsWithRuntime(b, prefix)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "indexOf":
		if len(call.Args) != 2 {
			return value{}, true, unsupportedf("call", "bytes.indexOf expects 2 arguments, got %d", len(call.Args))
		}
		b, err := g.emitStdBytesArg(call.Args[0], "indexOf", 0)
		if err != nil {
			return value{}, true, err
		}
		sub, err := g.emitStdBytesArg(call.Args[1], "indexOf", 1)
		if err != nil {
			return value{}, true, err
		}
		out, err := g.emitBytesIndexOfRuntime(b, sub)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "toString":
		if len(call.Args) != 1 {
			return value{}, true, unsupportedf("call", "bytes.toString expects 1 argument, got %d", len(call.Args))
		}
		b, err := g.emitStdBytesArg(call.Args[0], "toString", 0)
		if err != nil {
			return value{}, true, err
		}
		out, err := g.emitBytesToStringResult(b)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "len":
		if len(call.Args) != 1 {
			return value{}, true, unsupportedf("call", "bytes.len expects 1 argument, got %d", len(call.Args))
		}
		b, err := g.emitStdBytesArg(call.Args[0], "len", 0)
		if err != nil {
			return value{}, true, err
		}
		symbol := "osty_rt_bytes_len"
		g.declareRuntimeSymbol(symbol, "i64", []paramInfo{{typ: "ptr"}})
		emitter := g.toOstyEmitter()
		out := llvmCall(emitter, "i64", symbol, []*LlvmValue{toOstyValue(b)})
		g.takeOstyEmitter(emitter)
		return fromOstyValue(out), true, nil
	case "isEmpty":
		if len(call.Args) != 1 {
			return value{}, true, unsupportedf("call", "bytes.isEmpty expects 1 argument, got %d", len(call.Args))
		}
		b, err := g.emitStdBytesArg(call.Args[0], "isEmpty", 0)
		if err != nil {
			return value{}, true, err
		}
		symbol := "osty_rt_bytes_is_empty"
		g.declareRuntimeSymbol(symbol, "i1", []paramInfo{{typ: "ptr"}})
		emitter := g.toOstyEmitter()
		out := llvmCall(emitter, "i1", symbol, []*LlvmValue{toOstyValue(b)})
		g.takeOstyEmitter(emitter)
		return fromOstyValue(out), true, nil
	case "get":
		if len(call.Args) != 2 {
			return value{}, true, unsupportedf("call", "bytes.get expects 2 arguments, got %d", len(call.Args))
		}
		b, err := g.emitStdBytesArg(call.Args[0], "get", 0)
		if err != nil {
			return value{}, true, err
		}
		index, err := g.emitStdBytesIntArg(call.Args[1], "get", 1)
		if err != nil {
			return value{}, true, err
		}
		out, err := g.emitBytesGetRuntime(b, index)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "fromString":
		if len(call.Args) != 1 {
			return value{}, true, unsupportedf("call", "bytes.fromString expects 1 argument, got %d", len(call.Args))
		}
		s, err := g.emitStdBytesStringArg(call.Args[0], "fromString", 0)
		if err != nil {
			return value{}, true, err
		}
		g.declareRuntimeSymbol(llvmStringRuntimeToBytesSymbol(), "ptr", []paramInfo{{typ: "ptr"}})
		emitter := g.toOstyEmitter()
		out := llvmStringToBytes(emitter, toOstyValue(s))
		g.takeOstyEmitter(emitter)
		v := fromOstyValue(out)
		v.gcManaged = true
		v.sourceType = &ast.NamedType{Path: []string{"Bytes"}}
		return v, true, nil
	}
	return value{}, false, nil
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
	case "count":
		v, err := g.emitStdStringsBinary(call, "count", "i64", llvmStringRuntimeCountSymbol())
		return v, true, err
	case "indexOf":
		v, err := g.emitStdStringsIndexOf(call)
		return v, true, err
	case "concat":
		v, err := g.emitStdStringsBinaryString(call, "concat", llvmStringRuntimeConcatSymbol())
		return v, true, err
	case "contains":
		v, err := g.emitStdStringsBinary(call, "contains", "i1", llvmStringRuntimeContainsSymbol())
		return v, true, err
	case "hasPrefix":
		v, err := g.emitStdStringsBinary(call, "hasPrefix", "i1", llvmStringRuntimeHasPrefixSymbol())
		return v, true, err
	case "hasSuffix":
		v, err := g.emitStdStringsBinary(call, "hasSuffix", "i1", llvmStringRuntimeHasSuffixSymbol())
		return v, true, err
	case "join":
		v, err := g.emitStdStringsJoin(call)
		return v, true, err
	case "repeat":
		v, err := g.emitStdStringsRepeat(call)
		return v, true, err
	case "replace":
		v, err := g.emitStdStringsReplace(call)
		return v, true, err
	case "replaceAll":
		v, err := g.emitStdStringsReplaceAll(call)
		return v, true, err
	case "split":
		v, err := g.emitStdStringsSplit(call)
		return v, true, err
	case "splitN":
		v, err := g.emitStdStringsSplitN(call)
		return v, true, err
	case "slice":
		v, err := g.emitStdStringsSlice(call)
		return v, true, err
	case "toBytes":
		v, err := g.emitStdStringsToBytes(call)
		return v, true, err
	case "trimPrefix":
		v, err := g.emitStdStringsBinaryString(call, "trimPrefix", llvmStringRuntimeTrimPrefixSymbol())
		return v, true, err
	case "trimSuffix":
		v, err := g.emitStdStringsBinaryString(call, "trimSuffix", llvmStringRuntimeTrimSuffixSymbol())
		return v, true, err
	case "trimStart":
		v, err := g.emitStdStringsUnary(call, "trimStart", llvmStringRuntimeTrimStartSymbol())
		return v, true, err
	case "trimEnd":
		v, err := g.emitStdStringsUnary(call, "trimEnd", llvmStringRuntimeTrimEndSymbol())
		return v, true, err
	case "trim", "trimSpace":
		v, err := g.emitStdStringsUnary(call, field.Name, llvmStringRuntimeTrimSpaceSymbol())
		return v, true, err
	}
	return value{}, false, nil
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
	case "count":
		return value{typ: "i64"}, true
	case "indexOf":
		return value{
			typ:       "ptr",
			gcManaged: true,
			sourceType: &ast.OptionalType{
				Inner: &ast.NamedType{Path: []string{"Int"}},
			},
		}, true
	case "contains", "hasPrefix", "hasSuffix":
		return value{typ: "i1"}, true
	case "toBytes":
		return value{typ: "ptr", gcManaged: true, sourceType: &ast.NamedType{Path: []string{"Bytes"}}}, true
	case "concat", "join", "repeat", "replace", "replaceAll", "slice", "trim", "trimSpace", "trimStart", "trimEnd", "trimPrefix", "trimSuffix":
		return value{typ: "ptr", gcManaged: true}, true
	case "split", "splitN":
		return value{typ: "ptr", gcManaged: true, listElemTyp: "ptr", listElemString: true}, true
	}
	return value{}, false
}

func (g *generator) stdBytesCallStaticResult(call *ast.CallExpr) (value, bool) {
	if call == nil || len(g.stdBytesAliases) == 0 {
		return value{}, false
	}
	field, ok := call.Fn.(*ast.FieldExpr)
	if !ok {
		return value{}, false
	}
	alias, ok := field.X.(*ast.Ident)
	if !ok || !g.stdBytesAliases[alias.Name] {
		return value{}, false
	}
	switch field.Name {
	case "len":
		return value{typ: "i64"}, true
	case "isEmpty":
		return value{typ: "i1"}, true
	case "contains", "startsWith":
		return value{typ: "i1"}, true
	case "indexOf":
		return value{
			typ:       "ptr",
			gcManaged: true,
			sourceType: &ast.OptionalType{
				Inner: &ast.NamedType{Path: []string{"Int"}},
			},
		}, true
	case "get":
		return value{
			typ:       "ptr",
			gcManaged: true,
			sourceType: &ast.OptionalType{
				Inner: &ast.NamedType{Path: []string{"Byte"}},
			},
		}, true
	case "fromString":
		return value{typ: "ptr", gcManaged: true, sourceType: &ast.NamedType{Path: []string{"Bytes"}}}, true
	case "from":
		return value{typ: "ptr", gcManaged: true, sourceType: &ast.NamedType{Path: []string{"Bytes"}}}, true
	case "concat", "repeat":
		return value{typ: "ptr", gcManaged: true, sourceType: &ast.NamedType{Path: []string{"Bytes"}}}, true
	case "toString":
		if info, ok := builtinResultTypeFromAST(bytesToStringResultSourceType(), g.typeEnv()); ok {
			return value{typ: info.typ, sourceType: bytesToStringResultSourceType(), rootPaths: g.rootPathsForType(info.typ)}, true
		}
		return value{}, false
	}
	return value{}, false
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

// emitStdStringsSplitN mirrors emitStdStringsSplit but threads a third
// Int argument to the runtime cap. The runtime body lives in
// osty_rt_strings_SplitN; semantics match the pure-Osty stdlib body.
// `n` bypasses emitStdStringsArg because that helper enforces ptr
// (String) for every argument — splitN's count is the one outlier.
func (g *generator) emitStdStringsSplitN(call *ast.CallExpr) (value, error) {
	if len(call.Args) != 3 {
		return value{}, unsupportedf("call", "strings.splitN expects 3 arguments, got %d", len(call.Args))
	}
	s, err := g.emitStdStringsArg(call.Args[0], "splitN", 0)
	if err != nil {
		return value{}, err
	}
	sep, err := g.emitStdStringsArg(call.Args[1], "splitN", 1)
	if err != nil {
		return value{}, err
	}
	nArg := call.Args[2]
	if nArg == nil || nArg.Name != "" || nArg.Value == nil {
		return value{}, unsupportedf("call", "strings.splitN requires positional arguments")
	}
	nVal, err := g.emitExpr(nArg.Value)
	if err != nil {
		return value{}, err
	}
	nLoaded, err := g.loadIfPointer(nVal)
	if err != nil {
		return value{}, err
	}
	if nLoaded.typ != "i64" {
		return value{}, unsupportedf("type-system", "strings.splitN arg 3 type %s, want Int", nLoaded.typ)
	}
	symbol := llvmStringRuntimeSplitNSymbol()
	g.declareRuntimeSymbol(symbol, "ptr", []paramInfo{{typ: "ptr"}, {typ: "ptr"}, {typ: "i64"}})
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, "ptr", symbol, []*LlvmValue{toOstyValue(s), toOstyValue(sep), toOstyValue(nLoaded)})
	g.takeOstyEmitter(emitter)
	parts := fromOstyValue(out)
	parts.gcManaged = true
	parts.listElemTyp = "ptr"
	parts.listElemString = true
	return parts, nil
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

func (g *generator) emitStdStringsRepeat(call *ast.CallExpr) (value, error) {
	if len(call.Args) != 2 {
		return value{}, unsupportedf("call", "strings.repeat expects 2 arguments, got %d", len(call.Args))
	}
	s, err := g.emitStdStringsArg(call.Args[0], "repeat", 0)
	if err != nil {
		return value{}, err
	}
	n, err := g.emitStdStringsIntArg(call.Args[1], "repeat", 1)
	if err != nil {
		return value{}, err
	}
	symbol := llvmStringRuntimeRepeatSymbol()
	g.declareRuntimeSymbol(symbol, "ptr", []paramInfo{{typ: "ptr"}, {typ: "i64"}})
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, "ptr", symbol, []*LlvmValue{toOstyValue(s), toOstyValue(n)})
	g.takeOstyEmitter(emitter)
	repeated := fromOstyValue(out)
	repeated.gcManaged = true
	return repeated, nil
}

func (g *generator) emitStdStringsIndexOf(call *ast.CallExpr) (value, error) {
	if len(call.Args) != 2 {
		return value{}, unsupportedf("call", "strings.indexOf expects 2 arguments, got %d", len(call.Args))
	}
	s, err := g.emitStdStringsArg(call.Args[0], "indexOf", 0)
	if err != nil {
		return value{}, err
	}
	substr, err := g.emitStdStringsArg(call.Args[1], "indexOf", 1)
	if err != nil {
		return value{}, err
	}
	return g.emitStringIndexOfRuntime(s, substr)
}

func (g *generator) emitStdStringsReplace(call *ast.CallExpr) (value, error) {
	if len(call.Args) != 3 {
		return value{}, unsupportedf("call", "strings.replace expects 3 arguments, got %d", len(call.Args))
	}
	s, err := g.emitStdStringsArg(call.Args[0], "replace", 0)
	if err != nil {
		return value{}, err
	}
	old, err := g.emitStdStringsArg(call.Args[1], "replace", 1)
	if err != nil {
		return value{}, err
	}
	newValue, err := g.emitStdStringsArg(call.Args[2], "replace", 2)
	if err != nil {
		return value{}, err
	}
	return g.emitStringReplaceRuntime(s, old, newValue)
}

func (g *generator) emitStdStringsToBytes(call *ast.CallExpr) (value, error) {
	if len(call.Args) != 1 {
		return value{}, unsupportedf("call", "strings.toBytes expects 1 argument, got %d", len(call.Args))
	}
	s, err := g.emitStdStringsArg(call.Args[0], "toBytes", 0)
	if err != nil {
		return value{}, err
	}
	g.declareRuntimeSymbol(llvmStringRuntimeToBytesSymbol(), "ptr", []paramInfo{{typ: "ptr"}})
	emitter := g.toOstyEmitter()
	out := llvmStringToBytes(emitter, toOstyValue(s))
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.gcManaged = true
	v.sourceType = &ast.NamedType{Path: []string{"Bytes"}}
	return v, nil
}

func (g *generator) emitStdStringsReplaceAll(call *ast.CallExpr) (value, error) {
	if len(call.Args) != 3 {
		return value{}, unsupportedf("call", "strings.replaceAll expects 3 arguments, got %d", len(call.Args))
	}
	s, err := g.emitStdStringsArg(call.Args[0], "replaceAll", 0)
	if err != nil {
		return value{}, err
	}
	old, err := g.emitStdStringsArg(call.Args[1], "replaceAll", 1)
	if err != nil {
		return value{}, err
	}
	newValue, err := g.emitStdStringsArg(call.Args[2], "replaceAll", 2)
	if err != nil {
		return value{}, err
	}
	symbol := llvmStringRuntimeReplaceAllSymbol()
	g.declareRuntimeSymbol(symbol, "ptr", []paramInfo{{typ: "ptr"}, {typ: "ptr"}, {typ: "ptr"}})
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, "ptr", symbol, []*LlvmValue{toOstyValue(s), toOstyValue(old), toOstyValue(newValue)})
	g.takeOstyEmitter(emitter)
	replaced := fromOstyValue(out)
	replaced.gcManaged = true
	return replaced, nil
}

func (g *generator) emitStdStringsSlice(call *ast.CallExpr) (value, error) {
	if len(call.Args) != 3 {
		return value{}, unsupportedf("call", "strings.slice expects 3 arguments, got %d", len(call.Args))
	}
	s, err := g.emitStdStringsArg(call.Args[0], "slice", 0)
	if err != nil {
		return value{}, err
	}
	start, err := g.emitStdStringsIntArg(call.Args[1], "slice", 1)
	if err != nil {
		return value{}, err
	}
	end, err := g.emitStdStringsIntArg(call.Args[2], "slice", 2)
	if err != nil {
		return value{}, err
	}
	symbol := llvmStringRuntimeSliceSymbol()
	g.declareRuntimeSymbol(symbol, "ptr", []paramInfo{{typ: "ptr"}, {typ: "i64"}, {typ: "i64"}})
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, "ptr", symbol, []*LlvmValue{toOstyValue(s), toOstyValue(start), toOstyValue(end)})
	g.takeOstyEmitter(emitter)
	sliced := fromOstyValue(out)
	sliced.gcManaged = true
	return sliced, nil
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

func (g *generator) emitStdStringsBinaryString(call *ast.CallExpr, name, symbol string) (value, error) {
	v, err := g.emitStdStringsBinary(call, name, "ptr", symbol)
	if err != nil {
		return value{}, err
	}
	v.gcManaged = true
	return v, nil
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

func (g *generator) emitStdStringsIntArg(arg *ast.Arg, name string, index int) (value, error) {
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
	if loaded.typ != "i64" {
		return value{}, unsupportedf("type-system", "strings.%s arg %d type %s, want Int", name, index+1, loaded.typ)
	}
	return loaded, nil
}

func (g *generator) emitStdBytesArg(arg *ast.Arg, name string, index int) (value, error) {
	if arg == nil || arg.Name != "" || arg.Value == nil {
		return value{}, unsupportedf("call", "bytes.%s requires positional arguments", name)
	}
	src, ok := g.staticExprSourceType(arg.Value)
	if !ok {
		return value{}, unsupportedf("type-system", "bytes.%s arg %d source type unknown, want Bytes", name, index+1)
	}
	resolved, err := llvmResolveAliasType(src, g.typeEnv(), map[string]bool{})
	if err != nil || !llvmNamedTypeIsBytes(resolved) {
		return value{}, unsupportedf("type-system", "bytes.%s arg %d source type is not Bytes", name, index+1)
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
		return value{}, unsupportedf("type-system", "bytes.%s arg %d type %s, want Bytes", name, index+1, loaded.typ)
	}
	return loaded, nil
}

func (g *generator) emitStdBytesListArg(arg *ast.Arg, name string, index int) (value, error) {
	if arg == nil || arg.Name != "" || arg.Value == nil {
		return value{}, unsupportedf("call", "bytes.%s requires positional arguments", name)
	}
	if !g.staticExprListElemIsByte(arg.Value) {
		return value{}, unsupportedf("type-system", "bytes.%s arg %d source type is not List<Byte>", name, index+1)
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
		return value{}, unsupportedf("type-system", "bytes.%s arg %d type %s, want List<Byte>", name, index+1, loaded.typ)
	}
	return loaded, nil
}

func (g *generator) emitStdBytesStringArg(arg *ast.Arg, name string, index int) (value, error) {
	if arg == nil || arg.Name != "" || arg.Value == nil {
		return value{}, unsupportedf("call", "bytes.%s requires positional arguments", name)
	}
	src, ok := g.staticExprSourceType(arg.Value)
	if !ok {
		return value{}, unsupportedf("type-system", "bytes.%s arg %d source type unknown, want String", name, index+1)
	}
	resolved, err := llvmResolveAliasType(src, g.typeEnv(), map[string]bool{})
	if err != nil || !llvmNamedTypeIsString(resolved) {
		return value{}, unsupportedf("type-system", "bytes.%s arg %d source type is not String", name, index+1)
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
		return value{}, unsupportedf("type-system", "bytes.%s arg %d type %s, want String", name, index+1, loaded.typ)
	}
	return loaded, nil
}

func (g *generator) emitStdBytesIntArg(arg *ast.Arg, name string, index int) (value, error) {
	if arg == nil || arg.Name != "" || arg.Value == nil {
		return value{}, unsupportedf("call", "bytes.%s requires positional arguments", name)
	}
	v, err := g.emitExpr(arg.Value)
	if err != nil {
		return value{}, err
	}
	loaded, err := g.loadIfPointer(v)
	if err != nil {
		return value{}, err
	}
	if loaded.typ != "i64" {
		return value{}, unsupportedf("type-system", "bytes.%s arg %d type %s, want Int", name, index+1, loaded.typ)
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

// emitBuiltinOptionSomeCall handles `Some(x)` calls in a ptr-backed
// Option context. For ptr-typed payloads (String, List, etc.) the
// backend passes through — T? and T share the `ptr` LLVM type. For
// aggregate struct payloads (`%StructName`) we box the value into a
// GC-managed heap cell: allocate sizeof(T) via osty.gc.alloc_v1,
// memcpy the struct bytes in, return the heap pointer. None stays
// null ptr so downstream null-check / `?.` paths work unchanged.
//
// NOTE (GC hazard): the box uses OSTY_GC_KIND_GENERIC with no tracer.
// Managed pointers embedded in the struct aren't marked when reached
// only through this Option, so they could be collected if no other
// root holds them. In practice toolchain usage (`CrossCompileOutcome`
// etc.) keeps those strings alive via the construction-site locals,
// but a proper tracer is a follow-up (see TODO in runtime).
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
	if loaded.typ == "ptr" {
		loaded.sourceType = ctx.sourceType
		return loaded, true, nil
	}
	// Aggregate struct payload: box it on the GC heap.
	if strings.HasPrefix(loaded.typ, "%") {
		emitter := g.toOstyEmitter()
		size := g.emitAggregateByteSize(emitter, loaded.typ)
		siteName := "runtime.option.some." + strings.TrimPrefix(loaded.typ, "%")
		sitePtr := llvmStringLiteral(emitter, siteName)
		box := llvmCall(emitter, "ptr", "osty.gc.alloc_v1", []*LlvmValue{
			toOstyValue(value{typ: "i64", ref: "1"}), // OSTY_GC_KIND_GENERIC
			toOstyValue(size),
			sitePtr,
		})
		emitter.body = append(emitter.body, fmt.Sprintf(
			"  store %s %s, ptr %s",
			loaded.typ, loaded.ref, box.name,
		))
		g.takeOstyEmitter(emitter)
		g.needsGCRuntime = true
		out := fromOstyValue(box)
		out.gcManaged = true
		out.sourceType = ctx.sourceType
		return out, true, nil
	}
	return value{}, true, unsupportedf("type-system", "Some payload type %s requires boxed Option; only ptr-backed or aggregate-struct Some(...) is lowered", loaded.typ)
}
