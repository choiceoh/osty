// runtime_ffi.go — `use runtime.*` surface for the Osty runtime C ABI.
// Owns signature collection (collectRuntimeFFI, runtimeFFISignature),
// alias / symbol derivation, runtime-FFI call emission (value + stmt),
// per-symbol forward declarations, and the container runtime symbol tables
// (osty_rt_list_* / osty_rt_map_* / osty_rt_set_*) + their ABI-kind policy.
//
// NOTE(osty-migration): the container runtime policy (`*RuntimeSymbol`,
// `containerAbiKind`, `mapSetKeySuffix`, `listUsesTypedRuntime`,
// `listRuntimeSymbolSuffix`) is Osty-owned — every function below is a
// thin wrapper over the `llvm*` helpers generated from
// toolchain/llvmgen.osty and mirrored in support_snapshot.go. The
// wrappers stay here as a stable call surface for MIR generator code;
// changing the policy means editing the Osty source.
package llvmgen

import (
	"fmt"

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
	g.emitCallSafepointIfNeeded(emitter)
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
	g.emitCallSafepointIfNeeded(emitter)
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
		v, err := g.emitExprWithHintAndSourceType(arg.Value, param.sourceType, param.listElemTyp, param.listElemString, param.mapKeyTyp, param.mapValueTyp, param.mapKeyString, param.setElemTyp, param.setElemString)
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

func listRuntimePopDiscardSymbol() string {
	return "osty_rt_list_pop_discard"
}

func listRuntimeClearSymbol() string {
	return "osty_rt_list_clear"
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
	return llvmListRuntimePushSymbol(llvmListElementSuffix(elemTyp))
}

func listRuntimeGetSymbol(elemTyp string) string {
	return llvmListRuntimeGetSymbol(llvmListElementSuffix(elemTyp))
}

func listRuntimeSetSymbol(elemTyp string) string {
	return llvmListRuntimeSetSymbol(llvmListElementSuffix(elemTyp))
}

func listRuntimeInsertSymbol(elemTyp string) string {
	return llvmListRuntimeInsertSymbol(llvmListElementSuffix(elemTyp))
}

func listRuntimeInsertBytesV1Symbol() string {
	return "osty_rt_list_insert_bytes_v1"
}

func listRuntimeInsertBytesRootsSymbol() string {
	return "osty_rt_list_insert_bytes_roots_v1"
}

func listRuntimeSortedSymbol(elemTyp string, elemString bool) string {
	return llvmListRuntimeSortedSymbol(elemTyp, elemString)
}

func listRuntimeToSetSymbol(elemTyp string, elemString bool) string {
	return llvmListRuntimeToSetSymbol(elemTyp, elemString)
}

// listRuntimeSliceSymbol returns the element-agnostic slice helper that
// backs `list[a..b]` and `list[a..=b]`. Unlike get/push/sorted/to_set,
// the slice is a pure byte-level copy — the elem_size stored on the
// list header is enough, so one symbol covers every T.
func listRuntimeSliceSymbol() string {
	return "osty_rt_list_slice"
}

func mapRuntimeNewSymbol() string {
	return llvmMapRuntimeNewSymbol()
}

func mapRuntimeContainsSymbol(keyTyp string, keyString bool) string {
	return llvmMapRuntimeContainsSymbol(keyTyp, keyString)
}

func mapRuntimeInsertSymbol(keyTyp string, keyString bool) string {
	return llvmMapRuntimeInsertSymbol(keyTyp, keyString)
}

func mapRuntimeRemoveSymbol(keyTyp string, keyString bool) string {
	return llvmMapRuntimeRemoveSymbol(keyTyp, keyString)
}

func mapRuntimeGetOrAbortSymbol(keyTyp string, keyString bool) string {
	return llvmMapRuntimeGetOrAbortSymbol(keyTyp, keyString)
}

// mapRuntimeGetSymbol backs the Option-returning `Map.get(key) -> V?`
// intrinsic. The runtime helper returns i1 (present) and writes V into
// an out-param, so the backend can lift it into the Option<V> ABI
// (null ptr = None, boxed payload ptr = Some) without inlining the
// stdlib body per callsite.
func mapRuntimeGetSymbol(keyTyp string, keyString bool) string {
	return "osty_rt_map_get_" + llvmMapKeySuffix(keyTyp, keyString)
}

// mapRuntimeKeyAtSymbol returns the K-at-slot accessor used by
// `for (k, v) in m` iteration. `osty_rt_map_key_at_<ksuf>(map, i) -> K`.
func mapRuntimeKeyAtSymbol(keyTyp string, keyString bool) string {
	return "osty_rt_map_key_at_" + llvmMapKeySuffix(keyTyp, keyString)
}

// mapRuntimeValueAtSymbol returns the V-at-slot accessor — V-agnostic,
// takes an out-pointer. `osty_rt_map_value_at(map, i, out_ptr)`.
func mapRuntimeValueAtSymbol() string {
	return "osty_rt_map_value_at"
}

// mapRuntimeLockSymbol / mapRuntimeUnlockSymbol expose the per-map
// recursive mutex as a pair. Emitted by `update` so the get + callback
// + insert composition is a single critical section; recursive so the
// callback can re-enter the same map (e.g. read self.len()) without
// self-deadlock.
func mapRuntimeLockSymbol() string {
	return "osty_rt_map_lock"
}

func mapRuntimeUnlockSymbol() string {
	return "osty_rt_map_unlock"
}

func mapRuntimeKeysSymbol() string {
	return llvmMapRuntimeKeysSymbol()
}

func mapRuntimeLenSymbol() string {
	return llvmMapRuntimeLenSymbol()
}

func mapRuntimeClearSymbol() string {
	return "osty_rt_map_clear"
}

func setRuntimeNewSymbol() string {
	return llvmSetRuntimeNewSymbol()
}

func setRuntimeLenSymbol() string {
	return llvmSetRuntimeLenSymbol()
}

func setRuntimeContainsSymbol(elemTyp string, elemString bool) string {
	return llvmSetRuntimeContainsSymbol(elemTyp, elemString)
}

func setRuntimeInsertSymbol(elemTyp string, elemString bool) string {
	return llvmSetRuntimeInsertSymbol(elemTyp, elemString)
}

func setRuntimeRemoveSymbol(elemTyp string, elemString bool) string {
	return llvmSetRuntimeRemoveSymbol(elemTyp, elemString)
}

func setRuntimeToListSymbol() string {
	return llvmSetRuntimeToListSymbol()
}

func containerAbiKind(typ string, isString bool) int {
	return llvmContainerAbiKind(typ, isString)
}

func mapSetKeySuffix(typ string, isString bool) string {
	return llvmMapKeySuffix(typ, isString)
}

func listUsesTypedRuntime(elemTyp string) bool {
	return llvmListUsesTypedRuntime(elemTyp)
}

func listRuntimeSymbolSuffix(typ string) string {
	return llvmListElementSuffix(typ)
}
