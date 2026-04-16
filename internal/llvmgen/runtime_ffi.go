// runtime_ffi.go — `use runtime.*` surface for the Osty runtime C ABI.
// Owns signature collection (collectRuntimeFFI, runtimeFFISignature),
// alias / symbol derivation, runtime-FFI call emission (value + stmt),
// per-symbol forward declarations, and the container runtime symbol tables
// (osty_rt_list_* / osty_rt_map_* / osty_rt_set_*) + their ABI-kind policy.
//
// NOTE(osty-migration): the runtime symbol tables and `containerAbiKind` /
// `mapSetKeySuffix` / `listUsesTypedRuntime` / `listRuntimeSymbolSuffix` are
// all pure-policy (no AST dependency) and are the **first Phase B Osty
// migration target**. See toolchain/llvmgen.osty and the hand-synced Go
// mirrors in internal/llvmgen/support_snapshot.go.
package llvmgen

import (
	"fmt"
	"strings"

	"github.com/osty/osty/internal/ast"
)

type runtimeFFIFunction struct {
	path             string
	sourceName       string
	symbol           string
	ret              string
	listElemTyp      string
	returnSourceType ast.Type
	params           []paramInfo
	unsupported      string
}

type runtimeDecl struct {
	symbol string
	ret    string
	params []paramInfo
}

func collectRuntimeFFI(file *ast.File, env typeEnv) map[string]map[string]*runtimeFFIFunction {
	out := map[string]map[string]*runtimeFFIFunction{}
	if file == nil {
		return out
	}
	for _, use := range file.Uses {
		if use == nil || !use.IsRuntimeFFI || !llvmIsKnownRuntimeFfiPath(use.RuntimePath) {
			continue
		}
		alias := runtimeFFIAlias(use)
		if alias == "" {
			continue
		}
		funcs := out[alias]
		if funcs == nil {
			funcs = map[string]*runtimeFFIFunction{}
			out[alias] = funcs
		}
		for _, decl := range use.GoBody {
			fn, ok := decl.(*ast.FnDecl)
			if !ok || fn == nil || fn.Name == "" {
				continue
			}
			funcs[fn.Name] = runtimeFFISignature(use.RuntimePath, fn, env)
		}
	}
	return out
}

func collectRuntimeFFIPaths(file *ast.File) map[string]string {
	out := map[string]string{}
	if file == nil {
		return out
	}
	for _, use := range file.Uses {
		if use == nil || !use.IsRuntimeFFI || !llvmIsKnownRuntimeFfiPath(use.RuntimePath) {
			continue
		}
		if alias := runtimeFFIAlias(use); alias != "" {
			out[alias] = use.RuntimePath
		}
	}
	return out
}

func runtimeFFISignature(path string, fn *ast.FnDecl, env typeEnv) *runtimeFFIFunction {
	out := &runtimeFFIFunction{
		path:       path,
		sourceName: fn.Name,
		symbol:     runtimeFFISymbol(path, fn.Name),
	}
	if msg := llvmRuntimeFfiHeaderUnsupported(fn.Recv != nil, len(fn.Generics)); msg != "" {
		out.unsupported = msg
		return out
	}
	if fn.ReturnType == nil {
		out.ret = "void"
	} else {
		ret, err := llvmRuntimeABIType(fn.ReturnType, env)
		if err != nil {
			out.unsupported = llvmRuntimeFfiReturnUnsupported(unsupportedMessage(err))
			return out
		}
		out.ret = ret
		if listElemTyp, ok, err := llvmListElementType(fn.ReturnType, env); err != nil {
			out.unsupported = llvmRuntimeFfiReturnUnsupported(unsupportedMessage(err))
			return out
		} else if ok {
			out.listElemTyp = listElemTyp
		}
	}
	for _, p := range fn.Params {
		if p == nil {
			out.unsupported = llvmRuntimeFfiParamUnsupported("", true, false, "")
			return out
		}
		if p.Pattern != nil || p.Default != nil {
			out.unsupported = llvmRuntimeFfiParamUnsupported("", false, true, "")
			return out
		}
		name := llvmSignatureParamName(p.Name, len(out.params))
		typ, err := llvmRuntimeABIType(p.Type, env)
		if err != nil {
			out.unsupported = llvmRuntimeFfiParamUnsupported(name, false, false, unsupportedMessage(err))
			return out
		}
		info := paramInfo{name: name, typ: typ, sourceType: p.Type}
		if listElemTyp, ok, err := llvmListElementType(p.Type, env); err != nil {
			out.unsupported = llvmRuntimeFfiParamUnsupported(name, false, false, unsupportedMessage(err))
			return out
		} else if ok {
			info.listElemTyp = listElemTyp
		}
		out.params = append(out.params, info)
	}
	out.returnSourceType = fn.ReturnType
	return out
}

func runtimeFFIAlias(use *ast.UseDecl) string {
	if use == nil {
		return ""
	}
	lastPath := ""
	if len(use.Path) > 0 {
		lastPath = use.Path[len(use.Path)-1]
	}
	return llvmRuntimeFfiAlias(use.Alias, lastPath, use.RuntimePath)
}

func runtimeFFISymbol(path, name string) string {
	return llvmRuntimeFfiSymbol(path, name)
}

func (g *generator) emitRuntimeFFICall(call *ast.CallExpr) (value, bool, error) {
	fn, found, err := g.runtimeFFICallTarget(call)
	if !found || err != nil {
		return value{}, found, err
	}
	if fn.ret == "void" {
		return value{}, true, unsupportedf("call", "runtime FFI %s.%s has no return value", fn.path, fn.sourceName)
	}
	emitter := g.toOstyEmitter()
	g.emitGCSafepoint(emitter)
	g.takeOstyEmitter(emitter)
	g.pushScope()
	args, err := g.runtimeFFICallArgs(fn, call.Args)
	if err != nil {
		g.popScope()
		return value{}, true, err
	}
	g.declareRuntimeFFI(fn)
	emitter = g.toOstyEmitter()
	out := llvmCall(emitter, fn.ret, fn.symbol, args)
	g.takeOstyEmitter(emitter)
	g.popScope()
	ret := fromOstyValue(out)
	ret.listElemTyp = fn.listElemTyp
	ret.sourceType = fn.returnSourceType
	ret.gcManaged = fn.ret == "ptr" || fn.listElemTyp != ""
	ret.rootPaths = g.rootPathsForType(fn.ret)
	return ret, true, nil
}

func (g *generator) emitRuntimeFFICallStmt(call *ast.CallExpr) (bool, error) {
	fn, found, err := g.runtimeFFICallTarget(call)
	if !found || err != nil {
		return found, err
	}
	emitter := g.toOstyEmitter()
	g.emitGCSafepoint(emitter)
	g.takeOstyEmitter(emitter)
	g.pushScope()
	args, err := g.runtimeFFICallArgs(fn, call.Args)
	if err != nil {
		g.popScope()
		return true, err
	}
	g.declareRuntimeFFI(fn)
	if fn.ret == "void" {
		g.body = append(g.body, fmt.Sprintf("  call void @%s(%s)", fn.symbol, llvmCallArgs(args)))
		g.popScope()
		return true, nil
	}
	emitter = g.toOstyEmitter()
	llvmCall(emitter, fn.ret, fn.symbol, args)
	g.takeOstyEmitter(emitter)
	g.popScope()
	return true, nil
}

func (g *generator) runtimeFFICallTarget(call *ast.CallExpr) (*runtimeFFIFunction, bool, error) {
	if call == nil {
		return nil, false, nil
	}
	field, ok := call.Fn.(*ast.FieldExpr)
	if !ok {
		return nil, false, nil
	}
	alias, ok := field.X.(*ast.Ident)
	if !ok {
		return nil, false, nil
	}
	path, ok := g.runtimeFFIPaths[alias.Name]
	if !ok {
		return nil, false, nil
	}
	funcs := g.runtimeFFI[alias.Name]
	fn := funcs[field.Name]
	if fn == nil {
		return nil, true, unsupported("runtime-ffi", path+"."+field.Name)
	}
	if fn.unsupported != "" {
		return nil, true, unsupported("runtime-ffi", fn.path+"."+fn.sourceName+" signature: "+fn.unsupported)
	}
	return fn, true, nil
}

func (g *generator) runtimeFFICallArgs(fn *runtimeFFIFunction, callArgs []*ast.Arg) ([]*LlvmValue, error) {
	if len(callArgs) != len(fn.params) {
		return nil, unsupportedf("call", "runtime FFI %s.%s argument count", fn.path, fn.sourceName)
	}
	values := make([]value, 0, len(callArgs))
	for i, arg := range callArgs {
		if arg == nil || arg.Name != "" || arg.Value == nil {
			return nil, unsupportedf("call", "runtime FFI %s.%s requires positional arguments", fn.path, fn.sourceName)
		}
		param := fn.params[i]
		v, err := g.emitExprWithHint(arg.Value, param.listElemTyp, false, "", "", false, "", false)
		if err != nil {
			return nil, err
		}
		if v.typ != param.typ {
			return nil, unsupportedf("type-system", "runtime FFI %s.%s arg %d type %s, want %s", fn.path, fn.sourceName, i+1, v.typ, param.typ)
		}
		values = append(values, g.protectManagedTemporary(fn.symbol+".arg", v))
	}
	args := make([]*LlvmValue, 0, len(values))
	for _, v := range values {
		loaded, err := g.loadIfPointer(v)
		if err != nil {
			return nil, err
		}
		args = append(args, toOstyValue(loaded))
	}
	return args, nil
}

func (g *generator) declareRuntimeFFI(fn *runtimeFFIFunction) {
	if fn == nil {
		return
	}
	g.declareRuntimeSymbol(fn.symbol, fn.ret, fn.params)
}

func (g *generator) declareRuntimeSymbol(symbol, ret string, params []paramInfo) {
	if _, exists := g.runtimeDecls[symbol]; exists {
		return
	}
	g.runtimeDecls[symbol] = runtimeDecl{symbol: symbol, ret: ret, params: params}
	g.runtimeDeclOrder = append(g.runtimeDeclOrder, symbol)
}

func listRuntimeNewSymbol() string {
	return llvmListRuntimeNewSymbol()
}

func listRuntimePushBytesSymbol() string {
	return "osty_rt_list_push_bytes"
}

func listRuntimeGetBytesSymbol() string {
	return "osty_rt_list_get_bytes"
}

func listRuntimeSetBytesSymbol() string {
	return "osty_rt_list_set_bytes"
}

func listRuntimeLenSymbol() string {
	return llvmListRuntimeLenSymbol()
}

func listRuntimePushBytesV1Symbol() string {
	return "osty_rt_list_push_bytes_v1"
}

func listRuntimePushBytesRootsSymbol() string {
	return "osty_rt_list_push_bytes_roots_v1"
}

func listRuntimeGetBytesV1Symbol() string {
	return "osty_rt_list_get_bytes_v1"
}

func listRuntimePushSymbol(elemTyp string) string {
	return "osty_rt_list_push_" + listRuntimeSymbolSuffix(elemTyp)
}

func listRuntimeGetSymbol(elemTyp string) string {
	return "osty_rt_list_get_" + listRuntimeSymbolSuffix(elemTyp)
}

func listRuntimeSetSymbol(elemTyp string) string {
	return "osty_rt_list_set_" + listRuntimeSymbolSuffix(elemTyp)
}

func listRuntimeSortedI64Symbol() string {
	return llvmListRuntimeSortedI64Symbol()
}

func listRuntimeToSetI64Symbol() string {
	return llvmListRuntimeToSetI64Symbol()
}

func mapRuntimeNewSymbol() string {
	return llvmMapRuntimeNewSymbol()
}

func mapRuntimeContainsSymbol(keyTyp string, keyString bool) string {
	return "osty_rt_map_contains_" + mapSetKeySuffix(keyTyp, keyString)
}

func mapRuntimeInsertSymbol(keyTyp string, keyString bool) string {
	return "osty_rt_map_insert_" + mapSetKeySuffix(keyTyp, keyString)
}

func mapRuntimeRemoveSymbol(keyTyp string, keyString bool) string {
	return "osty_rt_map_remove_" + mapSetKeySuffix(keyTyp, keyString)
}

func mapRuntimeGetOrAbortSymbol(keyTyp string, keyString bool) string {
	return "osty_rt_map_get_or_abort_" + mapSetKeySuffix(keyTyp, keyString)
}

func mapRuntimeKeysSymbol() string {
	return llvmMapRuntimeKeysSymbol()
}

func setRuntimeNewSymbol() string {
	return llvmSetRuntimeNewSymbol()
}

func setRuntimeLenSymbol() string {
	return llvmSetRuntimeLenSymbol()
}

func setRuntimeContainsSymbol(elemTyp string, elemString bool) string {
	return "osty_rt_set_contains_" + mapSetKeySuffix(elemTyp, elemString)
}

func setRuntimeInsertSymbol(elemTyp string, elemString bool) string {
	return "osty_rt_set_insert_" + mapSetKeySuffix(elemTyp, elemString)
}

func setRuntimeRemoveSymbol(elemTyp string, elemString bool) string {
	return "osty_rt_set_remove_" + mapSetKeySuffix(elemTyp, elemString)
}

func setRuntimeToListSymbol() string {
	return llvmSetRuntimeToListSymbol()
}

func containerAbiKind(typ string, isString bool) int {
	return llvmContainerAbiKind(typ, isString)
}

func mapSetKeySuffix(typ string, isString bool) string {
	if isString {
		return "string"
	}
	switch typ {
	case "i64":
		return "i64"
	case "i1":
		return "i1"
	case "double":
		return "f64"
	case "ptr":
		return "ptr"
	default:
		return "bytes"
	}
}

func listUsesTypedRuntime(elemTyp string) bool {
	switch elemTyp {
	case "i64", "i1", "double", "ptr":
		return true
	default:
		return false
	}
}

func listRuntimeSymbolSuffix(typ string) string {
	switch typ {
	case "i64", "i1", "ptr":
		return typ
	case "double":
		return "f64"
	}
	var b strings.Builder
	for i := 0; i < len(typ); i++ {
		c := typ[i]
		if c == '_' || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9') {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('_')
	}
	if b.Len() == 0 {
		return "ptr"
	}
	return b.String()
}
