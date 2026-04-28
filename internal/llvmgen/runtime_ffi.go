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
	listElemString   bool
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
		if listElemTyp, listElemString, ok, err := llvmListElementInfo(fn.ReturnType, env); err != nil {
			out.unsupported = llvmRuntimeFfiReturnUnsupported(unsupportedMessage(err))
			return out
		} else if ok {
			out.listElemTyp = listElemTyp
			out.listElemString = listElemString
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
		if listElemTyp, listElemString, ok, err := llvmListElementInfo(p.Type, env); err != nil {
			out.unsupported = llvmRuntimeFfiParamUnsupported(name, false, false, unsupportedMessage(err))
			return out
		} else if ok {
			info.listElemTyp = listElemTyp
			info.listElemString = listElemString
		}
		out.params = append(out.params, info)
	}
	out.returnSourceType = fn.ReturnType
	runtimeStringsCanonicalizeDeclaredFFI(out)
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
	ret.listElemString = fn.listElemString
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
		fn = runtimeStringsKnownFFIFunction(field.Name)
		if fn == nil || fn.path != path {
			return nil, true, unsupported("runtime-ffi", path+"."+field.Name)
		}
	}
	if fn.unsupported != "" {
		return nil, true, unsupported("runtime-ffi", fn.path+"."+fn.sourceName+" signature: "+fn.unsupported)
	}
	return fn, true, nil
}

func runtimeStringsCanonicalizeDeclaredFFI(fn *runtimeFFIFunction) {
	if fn == nil || fn.path != "runtime.strings" {
		return
	}
	known := runtimeStringsKnownFFIFunction(fn.sourceName)
	if known == nil {
		return
	}
	fn.symbol = known.symbol
	if fn.ret == known.ret && fn.returnSourceType == nil {
		fn.returnSourceType = known.returnSourceType
		fn.listElemTyp = known.listElemTyp
		fn.listElemString = known.listElemString
	}
	if fn.ret == known.ret && fn.listElemTyp == "" && known.listElemTyp != "" {
		fn.listElemTyp = known.listElemTyp
		fn.listElemString = known.listElemString
	}
	for i := range fn.params {
		if i >= len(known.params) {
			break
		}
		if fn.params[i].typ != known.params[i].typ {
			continue
		}
		if fn.params[i].sourceType == nil {
			fn.params[i].sourceType = known.params[i].sourceType
		}
		if fn.params[i].listElemTyp == "" && known.params[i].listElemTyp != "" {
			fn.params[i].listElemTyp = known.params[i].listElemTyp
			fn.params[i].listElemString = known.params[i].listElemString
		}
	}
}

func runtimeStringsKnownFFIFunction(name string) *runtimeFFIFunction {
	canonical, ok := runtimeStringsCanonicalFFIName(name)
	if !ok {
		return nil
	}
	stringType := runtimeFFIStringSourceType()
	intType := runtimeFFIIntSourceType()
	boolType := runtimeFFIBoolSourceType()
	charType := runtimeFFICharSourceType()
	floatType := runtimeFFIFloatSourceType()
	listStringType := runtimeFFIListSourceType(stringType)
	listCharType := runtimeFFIListSourceType(charType)
	listByteType := runtimeFFIListSourceType(runtimeFFIByteSourceType())
	bytesType := runtimeFFIBytesSourceType()
	fn := &runtimeFFIFunction{
		path:       "runtime.strings",
		sourceName: name,
	}
	p := func(paramName, typ string, source ast.Type) paramInfo {
		info := paramInfo{name: paramName, typ: typ, sourceType: source}
		if list, ok := source.(*ast.NamedType); ok && len(list.Path) == 1 && list.Path[0] == "List" && len(list.Args) == 1 {
			switch elem := list.Args[0].(type) {
			case *ast.NamedType:
				if len(elem.Path) == 1 {
					switch elem.Path[0] {
					case "String":
						info.listElemTyp = "ptr"
						info.listElemString = true
					case "Char":
						info.listElemTyp = "i32"
					case "Byte":
						info.listElemTyp = "i8"
					case "Int":
						info.listElemTyp = "i64"
					case "Bool":
						info.listElemTyp = "i1"
					case "Float":
						info.listElemTyp = "double"
					}
				}
			}
		}
		return info
	}
	ret := func(symbol, typ string, source ast.Type, params ...paramInfo) *runtimeFFIFunction {
		fn.symbol = symbol
		fn.ret = typ
		fn.returnSourceType = source
		fn.params = params
		if list, ok := source.(*ast.NamedType); ok && len(list.Path) == 1 && list.Path[0] == "List" && len(list.Args) == 1 {
			switch elem := list.Args[0].(type) {
			case *ast.NamedType:
				if len(elem.Path) == 1 {
					switch elem.Path[0] {
					case "String":
						fn.listElemTyp = "ptr"
						fn.listElemString = true
					case "Char":
						fn.listElemTyp = "i32"
					case "Byte":
						fn.listElemTyp = "i8"
					case "Int":
						fn.listElemTyp = "i64"
					case "Bool":
						fn.listElemTyp = "i1"
					case "Float":
						fn.listElemTyp = "double"
					}
				}
			}
		}
		return fn
	}
	switch canonical {
	case "Equal":
		return ret("osty_rt_strings_Equal", "i1", boolType, p("left", "ptr", stringType), p("right", "ptr", stringType))
	case "Compare":
		return ret(llvmStringRuntimeCompareSymbol(), "i64", intType, p("left", "ptr", stringType), p("right", "ptr", stringType))
	case "Count":
		return ret(llvmStringRuntimeCountSymbol(), "i64", intType, p("value", "ptr", stringType), p("substr", "ptr", stringType))
	case "IndexOf":
		return ret(llvmStringRuntimeIndexOfSymbol(), "i64", intType, p("value", "ptr", stringType), p("substr", "ptr", stringType))
	case "LastIndexOf":
		return ret(mirRtStringLastIndexOfSymbol(), "i64", intType, p("value", "ptr", stringType), p("substr", "ptr", stringType))
	case "CharAt":
		return ret(mirRtStringSymbol("CharAt"), "i32", charType, p("value", "ptr", stringType), p("index", "i64", intType))
	case "ByteLen":
		return ret(llvmStringRuntimeByteLenSymbol(), "i64", intType, p("value", "ptr", stringType))
	case "Contains":
		return ret(llvmStringRuntimeContainsSymbol(), "i1", boolType, p("value", "ptr", stringType), p("substr", "ptr", stringType))
	case "HasPrefix":
		return ret(llvmStringRuntimeHasPrefixSymbol(), "i1", boolType, p("value", "ptr", stringType), p("prefix", "ptr", stringType))
	case "HasSuffix":
		return ret(llvmStringRuntimeHasSuffixSymbol(), "i1", boolType, p("value", "ptr", stringType), p("suffix", "ptr", stringType))
	case "Split":
		return ret(llvmStringRuntimeSplitSymbol(), "ptr", listStringType, p("value", "ptr", stringType), p("sep", "ptr", stringType))
	case "SplitN":
		return ret(llvmStringRuntimeSplitNSymbol(), "ptr", listStringType, p("value", "ptr", stringType), p("sep", "ptr", stringType), p("n", "i64", intType))
	case "Fields":
		return ret(mirRtStringSymbol("Fields"), "ptr", listStringType, p("value", "ptr", stringType))
	case "Join":
		return ret(llvmStringRuntimeJoinSymbol(), "ptr", stringType, p("parts", "ptr", listStringType), p("sep", "ptr", stringType))
	case "Concat":
		return ret(llvmStringRuntimeConcatSymbol(), "ptr", stringType, p("left", "ptr", stringType), p("right", "ptr", stringType))
	case "Repeat":
		return ret(llvmStringRuntimeRepeatSymbol(), "ptr", stringType, p("value", "ptr", stringType), p("n", "i64", intType))
	case "Replace":
		return ret(llvmStringRuntimeReplaceSymbol(), "ptr", stringType, p("value", "ptr", stringType), p("old", "ptr", stringType), p("new", "ptr", stringType))
	case "ReplaceAll":
		return ret(llvmStringRuntimeReplaceAllSymbol(), "ptr", stringType, p("value", "ptr", stringType), p("old", "ptr", stringType), p("new", "ptr", stringType))
	case "Slice":
		return ret(llvmStringRuntimeSliceSymbol(), "ptr", stringType, p("value", "ptr", stringType), p("start", "i64", intType), p("end", "i64", intType))
	case "ToUpper":
		return ret(llvmStringRuntimeToUpperSymbol(), "ptr", stringType, p("value", "ptr", stringType))
	case "ToLower":
		return ret(llvmStringRuntimeToLowerSymbol(), "ptr", stringType, p("value", "ptr", stringType))
	case "IsValidInt":
		return ret(llvmStringRuntimeIsValidIntSymbol(), "i1", boolType, p("value", "ptr", stringType))
	case "ToInt":
		return ret(llvmStringRuntimeToIntSymbol(), "i64", intType, p("value", "ptr", stringType))
	case "IsValidFloat":
		return ret(llvmStringRuntimeIsValidFloatSymbol(), "i1", boolType, p("value", "ptr", stringType))
	case "ToFloat":
		return ret(llvmStringRuntimeToFloatSymbol(), "double", floatType, p("value", "ptr", stringType))
	case "TrimStart":
		return ret(llvmStringRuntimeTrimStartSymbol(), "ptr", stringType, p("value", "ptr", stringType))
	case "TrimEnd":
		return ret(llvmStringRuntimeTrimEndSymbol(), "ptr", stringType, p("value", "ptr", stringType))
	case "TrimPrefix":
		return ret(llvmStringRuntimeTrimPrefixSymbol(), "ptr", stringType, p("value", "ptr", stringType), p("prefix", "ptr", stringType))
	case "TrimSuffix":
		return ret(llvmStringRuntimeTrimSuffixSymbol(), "ptr", stringType, p("value", "ptr", stringType), p("suffix", "ptr", stringType))
	case "TrimSpace":
		return ret(llvmStringRuntimeTrimSpaceSymbol(), "ptr", stringType, p("value", "ptr", stringType))
	case "Chars":
		return ret(llvmStringRuntimeCharsSymbol(), "ptr", listCharType, p("value", "ptr", stringType))
	case "Bytes":
		return ret(llvmStringRuntimeBytesSymbol(), "ptr", listByteType, p("value", "ptr", stringType))
	case "ToBytes":
		return ret(llvmStringRuntimeToBytesSymbol(), "ptr", bytesType, p("value", "ptr", stringType))
	}
	return nil
}

func runtimeStringsCanonicalFFIName(name string) (string, bool) {
	switch canonicalStdStringsCallName(name) {
	case "Equal", "equal":
		return "Equal", true
	case "compare":
		return "Compare", true
	case "count":
		return "Count", true
	case "indexOf", "Index", "IndexOf":
		return "IndexOf", true
	case "lastIndexOf", "LastIndex", "LastIndexOf":
		return "LastIndexOf", true
	case "charAt", "CharAt":
		return "CharAt", true
	case "len", "Len", "byteLen", "ByteLen":
		return "ByteLen", true
	case "contains":
		return "Contains", true
	case "hasPrefix":
		return "HasPrefix", true
	case "hasSuffix":
		return "HasSuffix", true
	case "split":
		return "Split", true
	case "splitN":
		return "SplitN", true
	case "fields":
		return "Fields", true
	case "join":
		return "Join", true
	case "concat":
		return "Concat", true
	case "repeat":
		return "Repeat", true
	case "replace":
		return "Replace", true
	case "replaceAll":
		return "ReplaceAll", true
	case "slice", "substring", "Substring":
		return "Slice", true
	case "toUpper":
		return "ToUpper", true
	case "toLower":
		return "ToLower", true
	case "isValidInt", "IsValidInt":
		return "IsValidInt", true
	case "toInt":
		return "ToInt", true
	case "isValidFloat", "IsValidFloat":
		return "IsValidFloat", true
	case "toFloat":
		return "ToFloat", true
	case "trimStart", "trimLeft", "TrimLeft":
		return "TrimStart", true
	case "trimEnd", "trimRight", "TrimRight":
		return "TrimEnd", true
	case "trimPrefix":
		return "TrimPrefix", true
	case "trimSuffix":
		return "TrimSuffix", true
	case "trim", "trimSpace":
		return "TrimSpace", true
	case "chars", "Chars":
		return "Chars", true
	case "bytes", "Bytes":
		return "Bytes", true
	case "toBytes":
		return "ToBytes", true
	default:
		return "", false
	}
}

func runtimeFFINamedSourceType(name string) ast.Type {
	return &ast.NamedType{Path: []string{name}}
}

func runtimeFFIStringSourceType() ast.Type { return runtimeFFINamedSourceType("String") }
func runtimeFFIIntSourceType() ast.Type    { return runtimeFFINamedSourceType("Int") }
func runtimeFFIBoolSourceType() ast.Type   { return runtimeFFINamedSourceType("Bool") }
func runtimeFFICharSourceType() ast.Type   { return runtimeFFINamedSourceType("Char") }
func runtimeFFIByteSourceType() ast.Type   { return runtimeFFINamedSourceType("Byte") }
func runtimeFFIFloatSourceType() ast.Type  { return runtimeFFINamedSourceType("Float") }
func runtimeFFIBytesSourceType() ast.Type  { return runtimeFFINamedSourceType("Bytes") }

func runtimeFFIListSourceType(elem ast.Type) ast.Type {
	return &ast.NamedType{Path: []string{"List"}, Args: []ast.Type{elem}}
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
	return mirRtListSymbol("push_bytes")
}

func listRuntimeGetBytesSymbol() string {
	return mirRtListSymbol("get_bytes")
}

func listRuntimeSetBytesSymbol() string {
	return mirRtListSymbol("set_bytes")
}

func listRuntimeLenSymbol() string {
	return llvmListRuntimeLenSymbol()
}

func listRuntimeDataSymbol(elemTyp string) string {
	return mirRtListSymbol("data_" + llvmListElementSuffix(elemTyp))
}

func listRuntimePopDiscardSymbol() string {
	return mirRtListPopDiscardSymbol()
}

func listRuntimeClearSymbol() string {
	return mirRtListClearSymbol()
}

func listRuntimePushBytesV1Symbol() string {
	return mirRtListSymbol("push_bytes_v1")
}

func listRuntimePushBytesRootsSymbol() string {
	return mirRtListSymbol("push_bytes_roots_v1")
}

func listRuntimeGetBytesV1Symbol() string {
	return mirRtListGetBytesV1Symbol()
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
	return mirRtListSymbol("insert_bytes_v1")
}

func listRuntimeInsertBytesRootsSymbol() string {
	return mirRtListSymbol("insert_bytes_roots_v1")
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
	return mirRtListSliceSymbol()
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
	return mirRtMapSymbol("get_" + llvmMapKeySuffix(keyTyp, keyString))
}

// mapRuntimeIncrI64Symbol returns the runtime symbol for the
// fused `m[k] = (m.get(k) ?? 0) + delta` operation.
// `osty_rt_map_incr_i64_<ksuf>(map, key, delta) -> i64` does one
// probe + in-place update on hit, falls through to insert on miss.
// Targeted by the `IntrinsicMapIncr` lowering.
func mapRuntimeIncrI64Symbol(keyTyp string, keyString bool) string {
	return mirRtMapSymbol("incr_i64_" + llvmMapKeySuffix(keyTyp, keyString))
}

// mapRuntimeKeyAtSymbol returns the K-at-slot accessor used by
// `for (k, v) in m` iteration. `osty_rt_map_key_at_<ksuf>(map, i) -> K`.
func mapRuntimeKeyAtSymbol(keyTyp string, keyString bool) string {
	return mirRtMapSymbol("key_at_" + llvmMapKeySuffix(keyTyp, keyString))
}

// mapRuntimeValueAtSymbol returns the V-at-slot accessor — V-agnostic,
// takes an out-pointer. `osty_rt_map_value_at(map, i, out_ptr)`.
func mapRuntimeValueAtSymbol() string {
	return mirRtMapSymbol("value_at")
}

// mapRuntimeEntryAtSymbol returns the combined K+V accessor used by
// generic `for (k, v) in m` lowering. The helper snapshots the key
// under the map lock and memcpy-writes the value into the caller's
// out-slot in the same critical section.
func mapRuntimeEntryAtSymbol(keyTyp string, keyString bool) string {
	return mirRtMapSymbol("entry_at_" + llvmMapKeySuffix(keyTyp, keyString))
}

// mapRuntimeLockSymbol / mapRuntimeUnlockSymbol expose the per-map
// recursive mutex as a pair. Emitted by `update` so the get + callback
// + insert composition is a single critical section; recursive so the
// callback can re-enter the same map (e.g. read self.len()) without
// self-deadlock.
func mapRuntimeLockSymbol() string {
	return mirRtMapSymbol("lock")
}

func mapRuntimeUnlockSymbol() string {
	return mirRtMapSymbol("unlock")
}

func mapRuntimeKeysSymbol() string {
	return llvmMapRuntimeKeysSymbol()
}

func mapRuntimeLenSymbol() string {
	return llvmMapRuntimeLenSymbol()
}

func mapRuntimeClearSymbol() string {
	return mirRtMapClearSymbol()
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

// listUsesRawDataFastPath delegates to the Osty-sourced
// `mirListUsesRawDataFastPath` (`toolchain/mir_generator.osty`). The
// fast set (i64 / i1 / double) is the same source-of-truth for both
// the snapshot read path and the typed-runtime dispatch.
func listUsesRawDataFastPath(elemTyp string) bool {
	return mirListUsesRawDataFastPath(elemTyp)
}

func listRuntimeSymbolSuffix(typ string) string {
	return llvmListElementSuffix(typ)
}
