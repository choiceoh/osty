package llvmgen

import (
	"fmt"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/mir"
)

const ostyRtKeychainBackendSymbol = "osty_rt_keychain_backend"
const ostyRtKeychainIsAvailableSymbol = "osty_rt_keychain_is_available"
const ostyRtKeychainGetSymbol = "osty_rt_keychain_get"
const ostyRtKeychainSetSymbol = "osty_rt_keychain_set"
const ostyRtKeychainDeleteSymbol = "osty_rt_keychain_delete"

const stdSecretsDefaultService = "osty.api"

var stdKeychainStringSourceTypeSingleton ast.Type = &ast.NamedType{
	Path: []string{"String"},
}

var stdKeychainStringResultSourceTypeSingleton ast.Type = &ast.NamedType{
	Path: []string{"Result"},
	Args: []ast.Type{
		stdKeychainStringSourceTypeSingleton,
		errorSourceTypeSingleton,
	},
}

func collectStdKeychainAliases(file *ast.File) map[string]bool {
	out := map[string]bool{}
	if file == nil {
		return out
	}
	for _, use := range file.Uses {
		if use == nil || use.IsFFI() {
			continue
		}
		if len(use.Path) != 2 || use.Path[0] != "std" || use.Path[1] != "keychain" {
			continue
		}
		alias := use.Alias
		if alias == "" {
			alias = "keychain"
		}
		out[alias] = true
	}
	return out
}

func collectStdSecretsAliases(file *ast.File) map[string]bool {
	out := map[string]bool{}
	if file == nil {
		return out
	}
	for _, use := range file.Uses {
		if use == nil || use.IsFFI() {
			continue
		}
		if len(use.Path) != 2 || use.Path[0] != "std" || use.Path[1] != "secrets" {
			continue
		}
		alias := use.Alias
		if alias == "" {
			alias = "secrets"
		}
		out[alias] = true
	}
	return out
}

func (g *generator) emitStdKeychainCall(call *ast.CallExpr) (value, bool, error) {
	field, ok := g.stdKeychainCallField(call)
	if !ok {
		return value{}, false, nil
	}
	switch field.Name {
	case "backend":
		return g.emitStdKeychainBackendCall(call)
	case "isAvailable":
		return g.emitStdKeychainIsAvailableCall(call)
	case "get":
		return g.emitStdKeychainGetCall(call)
	case "set":
		return g.emitStdKeychainSetCall(call)
	case "delete":
		return g.emitStdKeychainDeleteCall(call)
	case "getApiKey":
		return g.emitStdSecretsGetLikeCall(call, field.Name)
	case "setApiKey":
		return g.emitStdSecretsSetLikeCall(call, field.Name)
	case "deleteApiKey":
		return g.emitStdSecretsDeleteLikeCall(call, field.Name)
	default:
		return value{}, false, nil
	}
}

func (g *generator) stdKeychainCallStaticResult(call *ast.CallExpr) (value, bool) {
	field, ok := g.stdKeychainCallField(call)
	if !ok {
		return value{}, false
	}
	switch field.Name {
	case "backend":
		return value{
			typ:        "ptr",
			gcManaged:  true,
			sourceType: stdKeychainStringSourceTypeSingleton,
		}, true
	case "isAvailable":
		return value{typ: "i1"}, true
	case "get", "getApiKey":
		info, ok := builtinResultTypeFromAST(stdKeychainStringResultSourceTypeSingleton, g.typeEnv())
		if !ok {
			return value{}, false
		}
		return value{
			typ:        info.typ,
			sourceType: stdKeychainStringResultSourceTypeSingleton,
			rootPaths:  g.rootPathsForType(info.typ),
		}, true
	case "set", "delete", "setApiKey", "deleteApiKey":
		info, ok := builtinResultTypeFromAST(stdEnvUnitErrorResultSourceTypeSingleton, g.typeEnv())
		if !ok {
			return value{}, false
		}
		return value{
			typ:        info.typ,
			sourceType: stdEnvUnitErrorResultSourceTypeSingleton,
			rootPaths:  g.rootPathsForType(info.typ),
		}, true
	default:
		return value{}, false
	}
}

func (g *generator) staticStdKeychainCallSourceType(call *ast.CallExpr) (ast.Type, bool) {
	field, ok := g.stdKeychainCallField(call)
	if !ok {
		return nil, false
	}
	switch field.Name {
	case "backend":
		return stdKeychainStringSourceTypeSingleton, true
	case "isAvailable":
		return &ast.NamedType{Path: []string{"Bool"}}, true
	case "get", "getApiKey":
		return stdKeychainStringResultSourceTypeSingleton, true
	case "set", "delete", "setApiKey", "deleteApiKey":
		return stdEnvUnitErrorResultSourceTypeSingleton, true
	default:
		return nil, false
	}
}

func (g *generator) stdKeychainCallField(call *ast.CallExpr) (*ast.FieldExpr, bool) {
	if call == nil || len(g.stdKeychainAliases) == 0 {
		return nil, false
	}
	field, ok := fieldExprOfCallFn(call)
	if !ok || field.IsOptional {
		return nil, false
	}
	alias, ok := field.X.(*ast.Ident)
	if !ok || alias == nil || !g.stdKeychainAliases[alias.Name] {
		return nil, false
	}
	return field, true
}

func (g *generator) emitStdSecretsCall(call *ast.CallExpr) (value, bool, error) {
	field, ok := g.stdSecretsCallField(call)
	if !ok {
		return value{}, false, nil
	}
	switch field.Name {
	case "backend":
		return g.emitStdKeychainBackendCall(call)
	case "isAvailable":
		return g.emitStdKeychainIsAvailableCall(call)
	case "get", "getApiKey":
		return g.emitStdSecretsGetLikeCall(call, field.Name)
	case "set", "setApiKey":
		return g.emitStdSecretsSetLikeCall(call, field.Name)
	case "delete", "deleteApiKey":
		return g.emitStdSecretsDeleteLikeCall(call, field.Name)
	default:
		return value{}, false, nil
	}
}

func (g *generator) stdSecretsCallStaticResult(call *ast.CallExpr) (value, bool) {
	field, ok := g.stdSecretsCallField(call)
	if !ok {
		return value{}, false
	}
	switch field.Name {
	case "backend":
		return value{
			typ:        "ptr",
			gcManaged:  true,
			sourceType: stdKeychainStringSourceTypeSingleton,
		}, true
	case "isAvailable":
		return value{typ: "i1"}, true
	case "get", "getApiKey":
		info, ok := builtinResultTypeFromAST(stdKeychainStringResultSourceTypeSingleton, g.typeEnv())
		if !ok {
			return value{}, false
		}
		return value{
			typ:        info.typ,
			sourceType: stdKeychainStringResultSourceTypeSingleton,
			rootPaths:  g.rootPathsForType(info.typ),
		}, true
	case "set", "setApiKey", "delete", "deleteApiKey":
		info, ok := builtinResultTypeFromAST(stdEnvUnitErrorResultSourceTypeSingleton, g.typeEnv())
		if !ok {
			return value{}, false
		}
		return value{
			typ:        info.typ,
			sourceType: stdEnvUnitErrorResultSourceTypeSingleton,
			rootPaths:  g.rootPathsForType(info.typ),
		}, true
	default:
		return value{}, false
	}
}

func (g *generator) staticStdSecretsCallSourceType(call *ast.CallExpr) (ast.Type, bool) {
	field, ok := g.stdSecretsCallField(call)
	if !ok {
		return nil, false
	}
	switch field.Name {
	case "backend":
		return stdKeychainStringSourceTypeSingleton, true
	case "isAvailable":
		return &ast.NamedType{Path: []string{"Bool"}}, true
	case "get", "getApiKey":
		return stdKeychainStringResultSourceTypeSingleton, true
	case "set", "setApiKey", "delete", "deleteApiKey":
		return stdEnvUnitErrorResultSourceTypeSingleton, true
	default:
		return nil, false
	}
}

func (g *generator) stdSecretsCallField(call *ast.CallExpr) (*ast.FieldExpr, bool) {
	if call == nil || len(g.stdSecretsAliases) == 0 {
		return nil, false
	}
	field, ok := fieldExprOfCallFn(call)
	if !ok || field.IsOptional {
		return nil, false
	}
	alias, ok := field.X.(*ast.Ident)
	if !ok || alias == nil || !g.stdSecretsAliases[alias.Name] {
		return nil, false
	}
	return field, true
}

func (g *generator) emitStdKeychainBackendCall(call *ast.CallExpr) (value, bool, error) {
	if len(call.Args) != 0 {
		return value{}, true, unsupportedf("call", "keychain.backend takes no arguments, got %d", len(call.Args))
	}
	g.declareRuntimeSymbol(ostyRtKeychainBackendSymbol, "ptr", nil)
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	out := llvmCall(emitter, "ptr", ostyRtKeychainBackendSymbol, nil)
	g.takeOstyEmitter(emitter)
	return value{typ: "ptr", ref: out.name, gcManaged: true, sourceType: stdKeychainStringSourceTypeSingleton}, true, nil
}

func (g *generator) emitStdKeychainIsAvailableCall(call *ast.CallExpr) (value, bool, error) {
	if len(call.Args) != 0 {
		return value{}, true, unsupportedf("call", "keychain.isAvailable takes no arguments, got %d", len(call.Args))
	}
	g.declareRuntimeSymbol(ostyRtKeychainIsAvailableSymbol, "i1", nil)
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	out := llvmCall(emitter, "i1", ostyRtKeychainIsAvailableSymbol, nil)
	g.takeOstyEmitter(emitter)
	return fromOstyValue(out), true, nil
}

func (g *generator) emitStdSecretsGetLikeCall(call *ast.CallExpr, method string) (value, bool, error) {
	name, err := g.emitStdSecretsNameArg(call, "secrets."+method)
	if err != nil {
		return value{}, true, err
	}
	return g.emitStdKeychainStringResultCall(
		"secrets."+method,
		ostyRtKeychainGetSymbol,
		[]paramInfo{{typ: "ptr"}, {typ: "ptr"}},
		[]*LlvmValue{g.emitStdSecretsServiceLiteral(), name},
	)
}

func (g *generator) emitStdSecretsSetLikeCall(call *ast.CallExpr, method string) (value, bool, error) {
	args, err := g.emitStdSecretsNameSecretArgs(call, "secrets."+method)
	if err != nil {
		return value{}, true, err
	}
	return g.emitStdEnvUnitErrorResultFromRuntimeCall(
		"secrets."+method,
		stdEnvUnitErrorResultSourceTypeSingleton,
		ostyRtKeychainSetSymbol,
		[]paramInfo{{typ: "ptr"}, {typ: "ptr"}, {typ: "ptr"}},
		append([]*LlvmValue{g.emitStdSecretsServiceLiteral()}, args...),
	)
}

func (g *generator) emitStdSecretsDeleteLikeCall(call *ast.CallExpr, method string) (value, bool, error) {
	name, err := g.emitStdSecretsNameArg(call, "secrets."+method)
	if err != nil {
		return value{}, true, err
	}
	return g.emitStdEnvUnitErrorResultFromRuntimeCall(
		"secrets."+method,
		stdEnvUnitErrorResultSourceTypeSingleton,
		ostyRtKeychainDeleteSymbol,
		[]paramInfo{{typ: "ptr"}, {typ: "ptr"}},
		[]*LlvmValue{g.emitStdSecretsServiceLiteral(), name},
	)
}

func (g *generator) emitStdSecretsServiceLiteral() *LlvmValue {
	emitter := g.toOstyEmitter()
	out := llvmStringLiteral(emitter, stdSecretsDefaultService)
	g.takeOstyEmitter(emitter)
	return out
}

func (g *generator) emitStdSecretsNameArg(call *ast.CallExpr, label string) (*LlvmValue, error) {
	args, err := g.emitStdSecretsStringArgs(call, label, []string{"name"})
	if err != nil {
		return nil, err
	}
	return args[0], nil
}

func (g *generator) emitStdSecretsNameSecretArgs(call *ast.CallExpr, label string) ([]*LlvmValue, error) {
	return g.emitStdSecretsStringArgs(call, label, []string{"name", "secret"})
}

func (g *generator) emitStdSecretsStringArgs(call *ast.CallExpr, label string, names []string) ([]*LlvmValue, error) {
	if len(call.Args) != len(names) {
		return nil, unsupportedf("call", "%s expects %d arguments, got %d", label, len(names), len(call.Args))
	}
	out := make([]*LlvmValue, 0, len(names))
	for i, name := range names {
		arg := call.Args[i]
		acceptedName := name
		if name == "name" && (strings.HasSuffix(label, "ApiKey") || strings.Contains(label, "ApiKey")) {
			acceptedName = "provider"
		}
		if arg == nil || arg.Value == nil || (arg.Name != "" && arg.Name != name && arg.Name != acceptedName) {
			return nil, unsupportedf("call", "%s requires `%s` as a positional or named String argument", label, acceptedName)
		}
		v, err := g.emitExpr(arg.Value)
		if err != nil {
			return nil, err
		}
		v = g.protectManagedTemporary(label+"."+name, v)
		v, err = g.loadIfPointer(v)
		if err != nil {
			return nil, err
		}
		if v.typ != "ptr" {
			return nil, unsupportedf("type-system", "%s arg %d type %s, want String", label, i+1, v.typ)
		}
		out = append(out, toOstyValue(v))
	}
	return out, nil
}

func (g *generator) emitStdKeychainGetCall(call *ast.CallExpr) (value, bool, error) {
	args, err := g.emitStdKeychainStringArgs(call, "keychain.get", []string{"service", "account"})
	if err != nil {
		return value{}, true, err
	}
	return g.emitStdKeychainStringResultCall(
		"keychain.get",
		ostyRtKeychainGetSymbol,
		[]paramInfo{{typ: "ptr"}, {typ: "ptr"}},
		args,
	)
}

func (g *generator) emitStdKeychainSetCall(call *ast.CallExpr) (value, bool, error) {
	args, err := g.emitStdKeychainStringArgs(call, "keychain.set", []string{"service", "account", "secret"})
	if err != nil {
		return value{}, true, err
	}
	return g.emitStdEnvUnitErrorResultFromRuntimeCall(
		"keychain.set",
		stdEnvUnitErrorResultSourceTypeSingleton,
		ostyRtKeychainSetSymbol,
		[]paramInfo{{typ: "ptr"}, {typ: "ptr"}, {typ: "ptr"}},
		args,
	)
}

func (g *generator) emitStdKeychainDeleteCall(call *ast.CallExpr) (value, bool, error) {
	args, err := g.emitStdKeychainStringArgs(call, "keychain.delete", []string{"service", "account"})
	if err != nil {
		return value{}, true, err
	}
	return g.emitStdEnvUnitErrorResultFromRuntimeCall(
		"keychain.delete",
		stdEnvUnitErrorResultSourceTypeSingleton,
		ostyRtKeychainDeleteSymbol,
		[]paramInfo{{typ: "ptr"}, {typ: "ptr"}},
		args,
	)
}

func (g *generator) emitStdKeychainStringArgs(call *ast.CallExpr, label string, names []string) ([]*LlvmValue, error) {
	if len(call.Args) != len(names) {
		return nil, unsupportedf("call", "%s expects %d arguments, got %d", label, len(names), len(call.Args))
	}
	out := make([]*LlvmValue, 0, len(names))
	for i, name := range names {
		arg := call.Args[i]
		if arg == nil || arg.Value == nil || (arg.Name != "" && arg.Name != name) {
			return nil, unsupportedf("call", "%s requires `%s` as a positional or named String argument", label, name)
		}
		v, err := g.emitExpr(arg.Value)
		if err != nil {
			return nil, err
		}
		v = g.protectManagedTemporary(label+"."+name, v)
		v, err = g.loadIfPointer(v)
		if err != nil {
			return nil, err
		}
		if v.typ != "ptr" {
			return nil, unsupportedf("type-system", "%s arg %d type %s, want String", label, i+1, v.typ)
		}
		out = append(out, toOstyValue(v))
	}
	return out, nil
}

func (g *generator) emitStdKeychainStringResultCall(prefix string, symbol string, params []paramInfo, args []*LlvmValue) (value, bool, error) {
	info, ok := builtinResultTypeFromAST(stdKeychainStringResultSourceTypeSingleton, g.typeEnv())
	if !ok {
		return value{}, true, unsupportedf("type-system", "%s Result<String, Error> type is unavailable", prefix)
	}
	if g.resultTypes == nil {
		g.resultTypes = map[string]builtinResultType{}
	}
	g.resultTypes[info.typ] = info
	g.declareRuntimeSymbol(symbol, "ptr", params)
	g.declareRuntimeSymbol(ostyRtOsStringResultFreeSymbol, "void", []paramInfo{{typ: "ptr"}})
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	raw := llvmCall(emitter, "ptr", symbol, args)
	tag := llvmLoadFromPtr(emitter, "i64", llvmRuntimeStructFieldPtr(emitter, stdOsStringRuntimeRecordLLVMType, raw, 0))
	valueText := llvmLoadFromPtr(emitter, "ptr", llvmRuntimeStructFieldPtr(emitter, stdOsStringRuntimeRecordLLVMType, raw, 1))
	errText := llvmLoadFromPtr(emitter, "ptr", llvmRuntimeStructFieldPtr(emitter, stdOsStringRuntimeRecordLLVMType, raw, 2))
	emitter.body = append(emitter.body, "  call void @"+ostyRtOsStringResultFreeSymbol+"(ptr "+raw.name+")")
	failed := llvmCompare(emitter, "eq", tag, toOstyValue(value{typ: "i64", ref: "1"}))
	label := llvmBuiltinAggregatePart(prefix)
	errLabel := llvmNextLabel(emitter, label+".err")
	okLabel := llvmNextLabel(emitter, label+".ok")
	contLabel := llvmNextLabel(emitter, label+".cont")
	emitter.body = append(emitter.body, mirBrCondText(failed.name, errLabel, okLabel))

	emitter.body = append(emitter.body, mirLabelText(errLabel))
	errResult := llvmStructLiteral(emitter, info.typ, []*LlvmValue{
		toOstyValue(value{typ: "i64", ref: "1"}),
		toOstyValue(llvmZeroValue(info.okTyp)),
		errText,
	})
	emitter.body = append(emitter.body, "  br label %"+contLabel)

	emitter.body = append(emitter.body, mirLabelText(okLabel))
	okResult := llvmStructLiteral(emitter, info.typ, []*LlvmValue{
		toOstyValue(value{typ: "i64", ref: "0"}),
		valueText,
		toOstyValue(llvmZeroValue(info.errTyp)),
	})
	emitter.body = append(emitter.body, "  br label %"+contLabel)

	emitter.body = append(emitter.body, mirLabelText(contLabel))
	phi := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, "  "+phi+" = phi "+info.typ+" [ "+errResult.name+", %"+errLabel+" ], [ "+okResult.name+", %"+okLabel+" ]")
	g.takeOstyEmitter(emitter)
	v := value{
		typ:        info.typ,
		ref:        phi,
		sourceType: stdKeychainStringResultSourceTypeSingleton,
	}
	v.rootPaths = g.rootPathsForType(v.typ)
	return v, true, nil
}

func (g *mirGen) emitStdKeychainCall(c *mir.CallInstr, fnRef *mir.FnRef) (bool, error) {
	method := strings.TrimPrefix(fnRef.Symbol, "std.keychain.")
	switch method {
	case "backend":
		if len(c.Args) != 0 {
			return true, unsupported("mir-mvp", "std.keychain.backend requires no arguments")
		}
		return true, g.emitRuntimeCallToDest(c, ostyRtKeychainBackendSymbol, "ptr", nil)
	case "isAvailable":
		if len(c.Args) != 0 {
			return true, unsupported("mir-mvp", "std.keychain.isAvailable requires no arguments")
		}
		return true, g.emitRuntimeCallToDest(c, ostyRtKeychainIsAvailableSymbol, "i1", nil)
	case "get":
		if len(c.Args) != 2 {
			return true, unsupported("mir-mvp", "std.keychain.get requires service and account")
		}
		service, err := g.evalStringArg(c.Args[0], "std.keychain.get", 0)
		if err != nil {
			return true, err
		}
		account, err := g.evalStringArg(c.Args[1], "std.keychain.get", 1)
		if err != nil {
			return true, err
		}
		return true, g.emitStdKeychainStringResultMIR(c, ostyRtKeychainGetSymbol, "keychain.get", []mirRuntimeArg{service, account})
	case "set":
		if len(c.Args) != 3 {
			return true, unsupported("mir-mvp", "std.keychain.set requires service, account, and secret")
		}
		service, err := g.evalStringArg(c.Args[0], "std.keychain.set", 0)
		if err != nil {
			return true, err
		}
		account, err := g.evalStringArg(c.Args[1], "std.keychain.set", 1)
		if err != nil {
			return true, err
		}
		secret, err := g.evalStringArg(c.Args[2], "std.keychain.set", 2)
		if err != nil {
			return true, err
		}
		return true, g.emitUnitResultFromNullableError(c, ostyRtKeychainSetSymbol, []mirRuntimeArg{service, account, secret})
	case "delete":
		if len(c.Args) != 2 {
			return true, unsupported("mir-mvp", "std.keychain.delete requires service and account")
		}
		service, err := g.evalStringArg(c.Args[0], "std.keychain.delete", 0)
		if err != nil {
			return true, err
		}
		account, err := g.evalStringArg(c.Args[1], "std.keychain.delete", 1)
		if err != nil {
			return true, err
		}
		return true, g.emitUnitResultFromNullableError(c, ostyRtKeychainDeleteSymbol, []mirRuntimeArg{service, account})
	case "getApiKey":
		if len(c.Args) != 1 {
			return true, unsupported("mir-mvp", "std.keychain.getApiKey requires one provider argument")
		}
		provider, err := g.evalStringArg(c.Args[0], "std.keychain.getApiKey", 0)
		if err != nil {
			return true, err
		}
		return true, g.emitStdKeychainStringResultMIR(c, ostyRtKeychainGetSymbol, "keychain.getApiKey", []mirRuntimeArg{g.stdSecretsServiceArg(), provider})
	case "setApiKey":
		if len(c.Args) != 2 {
			return true, unsupported("mir-mvp", "std.keychain.setApiKey requires provider and secret")
		}
		provider, err := g.evalStringArg(c.Args[0], "std.keychain.setApiKey", 0)
		if err != nil {
			return true, err
		}
		secret, err := g.evalStringArg(c.Args[1], "std.keychain.setApiKey", 1)
		if err != nil {
			return true, err
		}
		return true, g.emitUnitResultFromNullableError(c, ostyRtKeychainSetSymbol, []mirRuntimeArg{g.stdSecretsServiceArg(), provider, secret})
	case "deleteApiKey":
		if len(c.Args) != 1 {
			return true, unsupported("mir-mvp", "std.keychain.deleteApiKey requires one provider argument")
		}
		provider, err := g.evalStringArg(c.Args[0], "std.keychain.deleteApiKey", 0)
		if err != nil {
			return true, err
		}
		return true, g.emitUnitResultFromNullableError(c, ostyRtKeychainDeleteSymbol, []mirRuntimeArg{g.stdSecretsServiceArg(), provider})
	default:
		return false, nil
	}
}

func (g *mirGen) emitStdSecretsCall(c *mir.CallInstr, fnRef *mir.FnRef) (bool, error) {
	method := strings.TrimPrefix(fnRef.Symbol, "std.secrets.")
	switch method {
	case "backend":
		if len(c.Args) != 0 {
			return true, unsupported("mir-mvp", "std.secrets.backend requires no arguments")
		}
		return true, g.emitRuntimeCallToDest(c, ostyRtKeychainBackendSymbol, "ptr", nil)
	case "isAvailable":
		if len(c.Args) != 0 {
			return true, unsupported("mir-mvp", "std.secrets.isAvailable requires no arguments")
		}
		return true, g.emitRuntimeCallToDest(c, ostyRtKeychainIsAvailableSymbol, "i1", nil)
	case "get", "getApiKey":
		if len(c.Args) != 1 {
			return true, unsupported("mir-mvp", "std.secrets."+method+" requires one name/provider argument")
		}
		name, err := g.evalStringArg(c.Args[0], "std.secrets."+method, 0)
		if err != nil {
			return true, err
		}
		return true, g.emitStdKeychainStringResultMIR(c, ostyRtKeychainGetSymbol, "secrets."+method, []mirRuntimeArg{g.stdSecretsServiceArg(), name})
	case "set", "setApiKey":
		if len(c.Args) != 2 {
			return true, unsupported("mir-mvp", "std.secrets."+method+" requires name/provider and secret")
		}
		name, err := g.evalStringArg(c.Args[0], "std.secrets."+method, 0)
		if err != nil {
			return true, err
		}
		secret, err := g.evalStringArg(c.Args[1], "std.secrets."+method, 1)
		if err != nil {
			return true, err
		}
		return true, g.emitUnitResultFromNullableError(c, ostyRtKeychainSetSymbol, []mirRuntimeArg{g.stdSecretsServiceArg(), name, secret})
	case "delete", "deleteApiKey":
		if len(c.Args) != 1 {
			return true, unsupported("mir-mvp", "std.secrets."+method+" requires one name/provider argument")
		}
		name, err := g.evalStringArg(c.Args[0], "std.secrets."+method, 0)
		if err != nil {
			return true, err
		}
		return true, g.emitUnitResultFromNullableError(c, ostyRtKeychainDeleteSymbol, []mirRuntimeArg{g.stdSecretsServiceArg(), name})
	default:
		return false, nil
	}
}

func (g *mirGen) stdSecretsServiceArg() mirRuntimeArg {
	return mirRuntimeArg{typ: "ptr", val: g.stringLiteral(stdSecretsDefaultService)}
}

func (g *mirGen) emitStdKeychainStringResultMIR(c *mir.CallInstr, symbol string, label string, args []mirRuntimeArg) error {
	g.declareRuntime(symbol, mirRuntimeDeclareLine("ptr", symbol, mirRuntimeParamList(args)))
	g.declareRuntime(ostyRtOsStringResultFreeSymbol, mirRuntimeDeclareLine("void", ostyRtOsStringResultFreeSymbol, "ptr"))
	raw := g.fresh()
	g.fnBuf.WriteString(mirCallValueLine(raw, "ptr", symbol, mirRuntimeArgList(args)))
	tag := g.emitRecordFieldLoad(raw, stdOsStringRuntimeRecordLLVMType, "i64", 0)
	valueText := g.emitRecordFieldLoad(raw, stdOsStringRuntimeRecordLLVMType, "ptr", 1)
	errText := g.emitRecordFieldLoad(raw, stdOsStringRuntimeRecordLLVMType, "ptr", 2)
	g.fnBuf.WriteString(mirCallVoidLine(ostyRtOsStringResultFreeSymbol, mirArgSlotPtr(raw)))
	if c.Dest == nil {
		return nil
	}
	destLoc := g.fn.Local(c.Dest.Local)
	if destLoc == nil {
		return fmt.Errorf("mir-mvp: %s dest into unknown local %d", label, c.Dest.Local)
	}
	resultLLVM := g.llvmType(destLoc.Type)
	failed := g.fresh()
	g.fnBuf.WriteString(mirICmpEqI64Line(failed, tag, "1"))
	errLabel := g.freshLabel(label + ".err")
	okLabel := g.freshLabel(label + ".ok")
	contLabel := g.freshLabel(label + ".cont")
	g.fnBuf.WriteString(mirBrCondLine(failed, errLabel, okLabel))

	g.fnBuf.WriteString(mirLabelLine(errLabel))
	errPayload := g.fresh()
	g.fnBuf.WriteString(mirPtrToIntLine(errPayload, errText, "i64"))
	errValue := g.emitResultValue(resultLLVM, false, errPayload)
	g.fnBuf.WriteString(mirBrUncondLine(contLabel))

	g.fnBuf.WriteString(mirLabelLine(okLabel))
	okPayload := g.fresh()
	g.fnBuf.WriteString(mirPtrToIntLine(okPayload, valueText, "i64"))
	okValue := g.emitResultValue(resultLLVM, true, okPayload)
	g.fnBuf.WriteString(mirBrUncondLine(contLabel))

	g.fnBuf.WriteString(mirLabelLine(contLabel))
	phi := g.fresh()
	g.fnBuf.WriteString(mirPhiTwoLine(phi, resultLLVM, errValue, errLabel, okValue, okLabel))
	g.fnBuf.WriteString(mirStoreLine(resultLLVM, phi, g.localSlots[c.Dest.Local]))
	return nil
}
