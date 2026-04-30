package llvmgen

import (
	"fmt"

	"github.com/osty/osty/internal/ast"
)

const ostyRtOsExecSymbol = "osty_rt_os_exec"
const ostyRtOsExecOptionsSymbol = "osty_rt_os_exec_options"
const ostyRtOsHostnameSymbol = "osty_rt_os_hostname"
const ostyRtOsPidSymbol = "osty_rt_os_pid"
const ostyRtOsExitSymbol = "osty_rt_os_exit"
const ostyRtOsExecResultFreeSymbol = "osty_rt_os_exec_result_free"
const ostyRtOsStringResultFreeSymbol = "osty_rt_os_string_result_free"

const stdOsExecRuntimeRecordLLVMType = "{ i64, i64, i1, ptr, ptr, ptr }"
const stdOsStringRuntimeRecordLLVMType = "{ i64, ptr, ptr }"
const stdOsSyntheticOutputTypeName = "__osty_std_os_Output"
const stdOsSyntheticExecOutputTypeName = "__osty_std_os_ExecOutput"

func llvmRuntimeStructFieldPtr(emitter *LlvmEmitter, structType string, base *LlvmValue, index int) *LlvmValue {
	reg := llvmNextTemp(emitter)
	emitter.body = append(emitter.body,
		fmt.Sprintf("  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d", reg, structType, base.name, index))
	return &LlvmValue{typ: "ptr", name: reg, pointer: true}
}

func llvmLoadFromPtr(emitter *LlvmEmitter, typ string, ptr *LlvmValue) *LlvmValue {
	reg := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = load %s, ptr %s", reg, typ, ptr.name))
	return &LlvmValue{typ: typ, name: reg}
}

var stdOsOutputSourceTypeSingleton ast.Type = &ast.NamedType{
	Path: []string{stdOsSyntheticOutputTypeName},
}

var stdOsExecResultSourceTypeSingleton ast.Type = &ast.NamedType{
	Path: []string{"Result"},
	Args: []ast.Type{
		stdOsOutputSourceTypeSingleton,
		errorSourceTypeSingleton,
	},
}

var stdOsExecOutputSourceTypeSingleton ast.Type = &ast.NamedType{
	Path: []string{stdOsSyntheticExecOutputTypeName},
}

var stdOsExecOptionsResultSourceTypeSingleton ast.Type = &ast.NamedType{
	Path: []string{"Result"},
	Args: []ast.Type{
		stdOsExecOutputSourceTypeSingleton,
		errorSourceTypeSingleton,
	},
}

var stdOsHostnameResultSourceTypeSingleton ast.Type = &ast.NamedType{
	Path: []string{"Result"},
	Args: []ast.Type{
		&ast.NamedType{Path: []string{"String"}},
		errorSourceTypeSingleton,
	},
}

func collectStdOsAliases(file *ast.File) map[string]bool {
	out := map[string]bool{}
	if file == nil {
		return out
	}
	for _, use := range file.Uses {
		if use == nil || use.IsFFI() {
			continue
		}
		if len(use.Path) != 2 || use.Path[0] != "std" || use.Path[1] != "os" {
			continue
		}
		alias := use.Alias
		if alias == "" {
			alias = "os"
		}
		out[alias] = true
	}
	return out
}

func ensureStdOsSyntheticOutputStruct(g *generator) *structInfo {
	if g == nil {
		return nil
	}
	if info := g.structsByName[stdOsSyntheticOutputTypeName]; info != nil {
		return info
	}
	if g.structsByName == nil {
		g.structsByName = map[string]*structInfo{}
	}
	if g.structsByType == nil {
		g.structsByType = map[string]*structInfo{}
	}
	info := &structInfo{
		name:   stdOsSyntheticOutputTypeName,
		typ:    llvmStructTypeName(stdOsSyntheticOutputTypeName),
		byName: map[string]fieldInfo{},
	}
	fields := []fieldInfo{
		{
			name:       "exitCode",
			typ:        "i64",
			index:      0,
			sourceType: &ast.NamedType{Path: []string{"Int"}},
		},
		{
			name:       "stdout",
			typ:        "ptr",
			index:      1,
			sourceType: &ast.NamedType{Path: []string{"String"}},
		},
		{
			name:       "stderr",
			typ:        "ptr",
			index:      2,
			sourceType: &ast.NamedType{Path: []string{"String"}},
		},
	}
	info.fields = fields
	for _, field := range fields {
		info.byName[field.name] = field
	}
	g.structs = append(g.structs, info)
	g.structsByName[info.name] = info
	g.structsByType[info.typ] = info
	return info
}

func ensureStdOsSyntheticExecOutputStruct(g *generator) *structInfo {
	if g == nil {
		return nil
	}
	if info := g.structsByName[stdOsSyntheticExecOutputTypeName]; info != nil {
		return info
	}
	if g.structsByName == nil {
		g.structsByName = map[string]*structInfo{}
	}
	if g.structsByType == nil {
		g.structsByType = map[string]*structInfo{}
	}
	info := &structInfo{
		name:   stdOsSyntheticExecOutputTypeName,
		typ:    llvmStructTypeName(stdOsSyntheticExecOutputTypeName),
		byName: map[string]fieldInfo{},
	}
	fields := []fieldInfo{
		{
			name:       "exitCode",
			typ:        "i64",
			index:      0,
			sourceType: &ast.NamedType{Path: []string{"Int"}},
		},
		{
			name:       "stdout",
			typ:        "ptr",
			index:      1,
			sourceType: &ast.NamedType{Path: []string{"String"}},
		},
		{
			name:       "stderr",
			typ:        "ptr",
			index:      2,
			sourceType: &ast.NamedType{Path: []string{"String"}},
		},
		{
			name:       "timedOut",
			typ:        "i1",
			index:      3,
			sourceType: &ast.NamedType{Path: []string{"Bool"}},
		},
	}
	info.fields = fields
	for _, field := range fields {
		info.byName[field.name] = field
	}
	g.structs = append(g.structs, info)
	g.structsByName[info.name] = info
	g.structsByType[info.typ] = info
	return info
}

func (g *generator) emitStdOsCall(call *ast.CallExpr) (value, bool, error) {
	field, ok := g.stdOsCallField(call)
	if !ok {
		return value{}, false, nil
	}
	switch field.Name {
	case "exec":
		return g.emitStdOsExecCall(call)
	case "execShell":
		return g.emitStdOsExecShellCall(call)
	case "execWith":
		return g.emitStdOsExecWithCall(call)
	case "execShellWith":
		return g.emitStdOsExecShellWithCall(call)
	case "pid":
		return g.emitStdOsPidCall(call)
	case "hostname":
		return g.emitStdOsHostnameCall(call)
	case "exit":
		return g.emitStdOsExitCall(call)
	default:
		return value{}, false, nil
	}
}

func (g *generator) stdOsCallStaticResult(call *ast.CallExpr) (value, bool) {
	field, ok := g.stdOsCallField(call)
	if !ok {
		return value{}, false
	}
	switch field.Name {
	case "exec", "execShell":
		ensureStdOsSyntheticOutputStruct(g)
		info, ok := builtinResultTypeFromAST(stdOsExecResultSourceTypeSingleton, g.typeEnv())
		if !ok {
			return value{}, false
		}
		return value{
			typ:        info.typ,
			sourceType: stdOsExecResultSourceTypeSingleton,
			rootPaths:  g.rootPathsForType(info.typ),
		}, true
	case "execWith", "execShellWith":
		ensureStdOsSyntheticExecOutputStruct(g)
		info, ok := builtinResultTypeFromAST(stdOsExecOptionsResultSourceTypeSingleton, g.typeEnv())
		if !ok {
			return value{}, false
		}
		return value{
			typ:        info.typ,
			sourceType: stdOsExecOptionsResultSourceTypeSingleton,
			rootPaths:  g.rootPathsForType(info.typ),
		}, true
	case "pid":
		return value{typ: "i64"}, true
	case "hostname":
		info, ok := builtinResultTypeFromAST(stdOsHostnameResultSourceTypeSingleton, g.typeEnv())
		if !ok {
			return value{}, false
		}
		return value{
			typ:        info.typ,
			sourceType: stdOsHostnameResultSourceTypeSingleton,
			rootPaths:  g.rootPathsForType(info.typ),
		}, true
	default:
		return value{}, false
	}
}

func (g *generator) staticStdOsCallSourceType(call *ast.CallExpr) (ast.Type, bool) {
	field, ok := g.stdOsCallField(call)
	if !ok {
		return nil, false
	}
	switch field.Name {
	case "exec", "execShell":
		ensureStdOsSyntheticOutputStruct(g)
		return stdOsExecResultSourceTypeSingleton, true
	case "execWith", "execShellWith":
		ensureStdOsSyntheticExecOutputStruct(g)
		return stdOsExecOptionsResultSourceTypeSingleton, true
	case "pid":
		return &ast.NamedType{Path: []string{"Int"}}, true
	case "hostname":
		return stdOsHostnameResultSourceTypeSingleton, true
	default:
		return nil, false
	}
}

func (g *generator) stdOsCallField(call *ast.CallExpr) (*ast.FieldExpr, bool) {
	if call == nil || len(g.stdOsAliases) == 0 {
		return nil, false
	}
	field, ok := fieldExprOfCallFn(call)
	if !ok || field.IsOptional {
		return nil, false
	}
	alias, ok := field.X.(*ast.Ident)
	if !ok || alias == nil || !g.stdOsAliases[alias.Name] {
		return nil, false
	}
	return field, true
}

func (g *generator) emitStdOsExecCall(call *ast.CallExpr) (value, bool, error) {
	return g.emitStdOsExecLikeCall(call, false, false)
}

func (g *generator) emitStdOsExecShellCall(call *ast.CallExpr) (value, bool, error) {
	return g.emitStdOsExecLikeCall(call, true, false)
}

func (g *generator) emitStdOsExecWithCall(call *ast.CallExpr) (value, bool, error) {
	return g.emitStdOsExecLikeCall(call, false, true)
}

func (g *generator) emitStdOsExecShellWithCall(call *ast.CallExpr) (value, bool, error) {
	return g.emitStdOsExecLikeCall(call, true, true)
}

func (g *generator) emitStdOsExecLikeCall(call *ast.CallExpr, shell bool, withOptions bool) (value, bool, error) {
	resultSourceType := stdOsExecResultSourceTypeSingleton
	ensureStdOsSyntheticOutputStruct(g)
	if withOptions {
		resultSourceType = stdOsExecOptionsResultSourceTypeSingleton
		ensureStdOsSyntheticExecOutputStruct(g)
	}
	info, ok := builtinResultTypeFromAST(resultSourceType, g.typeEnv())
	if !ok {
		return value{}, true, unsupported("type-system", "std.os exec Result output type is unavailable")
	}
	if g.resultTypes == nil {
		g.resultTypes = map[string]builtinResultType{}
	}
	g.resultTypes[info.typ] = info
	wantArgs := 2
	minArgs := 1
	if shell {
		wantArgs = 1
	}
	if withOptions {
		if shell {
			wantArgs = 4
		} else {
			wantArgs = 5
		}
		minArgs = wantArgs
	}
	if len(call.Args) < minArgs || len(call.Args) > wantArgs {
		name := stdOsExecCallName(shell, withOptions)
		return value{}, true, unsupportedf("call", "%s received %d arguments", name, len(call.Args))
	}
	cmdArg := call.Args[0]
	if cmdArg == nil || (cmdArg.Name != "" && cmdArg.Name != "cmd" && cmdArg.Name != "command") || cmdArg.Value == nil {
		name := stdOsExecCallName(shell, withOptions)
		return value{}, true, unsupported("call", name+" requires a String command argument")
	}
	cmd, err := g.emitExpr(cmdArg.Value)
	if err != nil {
		return value{}, true, err
	}
	cmd = g.protectManagedTemporary("os.exec.cmd", cmd)
	cmd, err = g.loadIfPointer(cmd)
	if err != nil {
		return value{}, true, err
	}
	if cmd.typ != "ptr" {
		return value{}, true, unsupportedf("type-system", "os.exec command type %s, want String", cmd.typ)
	}
	argsValue := value{typ: "ptr", ref: "null"}
	if !shell && len(call.Args) == 2 {
		argsArg := call.Args[1]
		if argsArg == nil || (argsArg.Name != "" && argsArg.Name != "args") || argsArg.Value == nil {
			return value{}, true, unsupported("call", "os.exec requires args as a positional or `args:` List<String> argument")
		}
		argsValue, err = g.emitExpr(argsArg.Value)
		if err != nil {
			return value{}, true, err
		}
		argsValue = g.protectManagedTemporary("os.exec.args", argsValue)
		argsValue, err = g.loadIfPointer(argsValue)
		if err != nil {
			return value{}, true, err
		}
		if argsValue.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "os.exec args type %s, want List<String>", argsValue.typ)
		}
	}
	cwdValue := value{typ: "ptr", ref: "null"}
	envValue := value{typ: "ptr", ref: "null"}
	timeoutValue := value{typ: "i64", ref: "0"}
	if withOptions {
		argsIndex := 1
		if !shell {
			argsArg := call.Args[1]
			if argsArg == nil || (argsArg.Name != "" && argsArg.Name != "args") || argsArg.Value == nil {
				return value{}, true, unsupported("call", "os.execWith requires args as a positional or `args:` List<String> argument")
			}
			argsValue, err = g.emitExpr(argsArg.Value)
			if err != nil {
				return value{}, true, err
			}
			argsValue = g.protectManagedTemporary("os.execWith.args", argsValue)
			argsValue, err = g.loadIfPointer(argsValue)
			if err != nil {
				return value{}, true, err
			}
			if argsValue.typ != "ptr" {
				return value{}, true, unsupportedf("type-system", "os.execWith args type %s, want List<String>", argsValue.typ)
			}
			argsIndex = 2
		}
		cwdValue, err = g.emitStdOsExecPtrArg(call.Args[argsIndex], "cwd", "String", "os.execWith.cwd")
		if err != nil {
			return value{}, true, err
		}
		envValue, err = g.emitStdOsExecPtrArg(call.Args[argsIndex+1], "env", "Map<String, String>", "os.execWith.env")
		if err != nil {
			return value{}, true, err
		}
		timeoutArg := call.Args[argsIndex+2]
		if timeoutArg == nil || (timeoutArg.Name != "" && timeoutArg.Name != "timeoutMillis") || timeoutArg.Value == nil {
			return value{}, true, unsupported("call", "os.execWith requires a timeoutMillis Int argument")
		}
		timeoutValue, err = g.emitExpr(timeoutArg.Value)
		if err != nil {
			return value{}, true, err
		}
		timeoutValue, err = g.loadIfPointer(timeoutValue)
		if err != nil {
			return value{}, true, err
		}
		if timeoutValue.typ != "i64" {
			return value{}, true, unsupportedf("type-system", "os.execWith timeoutMillis type %s, want Int", timeoutValue.typ)
		}
	}
	if withOptions {
		g.declareRuntimeSymbol(ostyRtOsExecOptionsSymbol, "ptr", []paramInfo{{typ: "ptr"}, {typ: "ptr"}, {typ: "i1"}, {typ: "ptr"}, {typ: "ptr"}, {typ: "i64"}})
	} else {
		g.declareRuntimeSymbol(ostyRtOsExecSymbol, "ptr", []paramInfo{{typ: "ptr"}, {typ: "ptr"}, {typ: "i1"}})
	}
	g.declareRuntimeSymbol(ostyRtOsExecResultFreeSymbol, "void", []paramInfo{{typ: "ptr"}})
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	var raw *LlvmValue
	if withOptions {
		raw = llvmCall(emitter, "ptr", ostyRtOsExecOptionsSymbol, []*LlvmValue{
			toOstyValue(cmd),
			toOstyValue(argsValue),
			llvmStdIoBoolValue(shell),
			toOstyValue(cwdValue),
			toOstyValue(envValue),
			toOstyValue(timeoutValue),
		})
	} else {
		raw = llvmCall(emitter, "ptr", ostyRtOsExecSymbol, []*LlvmValue{
			toOstyValue(cmd),
			toOstyValue(argsValue),
			llvmStdIoBoolValue(shell),
		})
	}
	tag := llvmLoadFromPtr(emitter, "i64", llvmRuntimeStructFieldPtr(emitter, stdOsExecRuntimeRecordLLVMType, raw, 0))
	exitCode := llvmLoadFromPtr(emitter, "i64", llvmRuntimeStructFieldPtr(emitter, stdOsExecRuntimeRecordLLVMType, raw, 1))
	timedOut := llvmLoadFromPtr(emitter, "i1", llvmRuntimeStructFieldPtr(emitter, stdOsExecRuntimeRecordLLVMType, raw, 2))
	stdoutText := llvmLoadFromPtr(emitter, "ptr", llvmRuntimeStructFieldPtr(emitter, stdOsExecRuntimeRecordLLVMType, raw, 3))
	stderrText := llvmLoadFromPtr(emitter, "ptr", llvmRuntimeStructFieldPtr(emitter, stdOsExecRuntimeRecordLLVMType, raw, 4))
	errText := llvmLoadFromPtr(emitter, "ptr", llvmRuntimeStructFieldPtr(emitter, stdOsExecRuntimeRecordLLVMType, raw, 5))
	emitter.body = append(emitter.body, "  call void @"+ostyRtOsExecResultFreeSymbol+"(ptr "+raw.name+")")
	failed := llvmCompare(emitter, "eq", tag, toOstyValue(value{typ: "i64", ref: "1"}))
	errLabel := llvmNextLabel(emitter, "os.exec.err")
	okLabel := llvmNextLabel(emitter, "os.exec.ok")
	contLabel := llvmNextLabel(emitter, "os.exec.cont")
	emitter.body = append(emitter.body, mirBrCondText(failed.name, errLabel, okLabel))

	emitter.body = append(emitter.body, mirLabelText(errLabel))
	errResult := llvmStructLiteral(emitter, info.typ, []*LlvmValue{
		toOstyValue(value{typ: "i64", ref: "1"}),
		toOstyValue(llvmZeroValue(info.okTyp)),
		errText,
	})
	emitter.body = append(emitter.body, "  br label %"+contLabel)

	emitter.body = append(emitter.body, mirLabelText(okLabel))
	var outputInfo *structInfo
	var outputValue *LlvmValue
	if withOptions {
		outputInfo = ensureStdOsSyntheticExecOutputStruct(g)
		outputValue = llvmStructLiteral(emitter, outputInfo.typ, []*LlvmValue{
			exitCode,
			stdoutText,
			stderrText,
			timedOut,
		})
	} else {
		outputInfo = ensureStdOsSyntheticOutputStruct(g)
		outputValue = llvmStructLiteral(emitter, outputInfo.typ, []*LlvmValue{
			exitCode,
			stdoutText,
			stderrText,
		})
	}
	okResult := llvmStructLiteral(emitter, info.typ, []*LlvmValue{
		toOstyValue(value{typ: "i64", ref: "0"}),
		outputValue,
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
		sourceType: resultSourceType,
	}
	v.rootPaths = g.rootPathsForType(v.typ)
	return v, true, nil
}

func stdOsExecCallName(shell bool, withOptions bool) string {
	switch {
	case shell && withOptions:
		return "os.execShellWith"
	case shell:
		return "os.execShell"
	case withOptions:
		return "os.execWith"
	default:
		return "os.exec"
	}
}

func (g *generator) emitStdOsExecPtrArg(arg *ast.Arg, name string, want string, label string) (value, error) {
	if arg == nil || (arg.Name != "" && arg.Name != name) || arg.Value == nil {
		return value{}, unsupported("call", "os.execWith requires "+name+" as a positional or `"+name+":` "+want+" argument")
	}
	out, err := g.emitExpr(arg.Value)
	if err != nil {
		return value{}, err
	}
	out = g.protectManagedTemporary(label, out)
	out, err = g.loadIfPointer(out)
	if err != nil {
		return value{}, err
	}
	if out.typ != "ptr" {
		return value{}, unsupportedf("type-system", "os.execWith %s type %s, want %s", name, out.typ, want)
	}
	return out, nil
}

func (g *generator) emitStdOsPidCall(call *ast.CallExpr) (value, bool, error) {
	if len(call.Args) != 0 {
		return value{}, true, unsupportedf("call", "os.pid takes no arguments, got %d", len(call.Args))
	}
	g.declareRuntimeSymbol(ostyRtOsPidSymbol, "i64", nil)
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	out := llvmCall(emitter, "i64", ostyRtOsPidSymbol, nil)
	g.takeOstyEmitter(emitter)
	return fromOstyValue(out), true, nil
}

func (g *generator) emitStdOsHostnameCall(call *ast.CallExpr) (value, bool, error) {
	if len(call.Args) != 0 {
		return value{}, true, unsupportedf("call", "os.hostname takes no arguments, got %d", len(call.Args))
	}
	info, ok := builtinResultTypeFromAST(stdOsHostnameResultSourceTypeSingleton, g.typeEnv())
	if !ok {
		return value{}, true, unsupported("type-system", "std.os hostname Result<String, Error> type is unavailable")
	}
	if g.resultTypes == nil {
		g.resultTypes = map[string]builtinResultType{}
	}
	g.resultTypes[info.typ] = info
	g.declareRuntimeSymbol(ostyRtOsHostnameSymbol, "ptr", nil)
	g.declareRuntimeSymbol(ostyRtOsStringResultFreeSymbol, "void", []paramInfo{{typ: "ptr"}})
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	raw := llvmCall(emitter, "ptr", ostyRtOsHostnameSymbol, nil)
	tag := llvmLoadFromPtr(emitter, "i64", llvmRuntimeStructFieldPtr(emitter, stdOsStringRuntimeRecordLLVMType, raw, 0))
	valueText := llvmLoadFromPtr(emitter, "ptr", llvmRuntimeStructFieldPtr(emitter, stdOsStringRuntimeRecordLLVMType, raw, 1))
	errText := llvmLoadFromPtr(emitter, "ptr", llvmRuntimeStructFieldPtr(emitter, stdOsStringRuntimeRecordLLVMType, raw, 2))
	emitter.body = append(emitter.body, "  call void @"+ostyRtOsStringResultFreeSymbol+"(ptr "+raw.name+")")
	failed := llvmCompare(emitter, "eq", tag, toOstyValue(value{typ: "i64", ref: "1"}))
	errLabel := llvmNextLabel(emitter, "os.hostname.err")
	okLabel := llvmNextLabel(emitter, "os.hostname.ok")
	contLabel := llvmNextLabel(emitter, "os.hostname.cont")
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
		sourceType: stdOsHostnameResultSourceTypeSingleton,
	}
	v.rootPaths = g.rootPathsForType(v.typ)
	return v, true, nil
}

func (g *generator) emitStdOsExitCall(call *ast.CallExpr) (value, bool, error) {
	if len(call.Args) != 1 {
		return value{}, true, unsupportedf("call", "os.exit expects 1 argument, got %d", len(call.Args))
	}
	arg := call.Args[0]
	if arg == nil || (arg.Name != "" && arg.Name != "code") || arg.Value == nil {
		return value{}, true, unsupported("call", "os.exit requires one positional or `code:` Int argument")
	}
	code, err := g.emitExpr(arg.Value)
	if err != nil {
		return value{}, true, err
	}
	if code.typ != "i64" {
		return value{}, true, unsupportedf("type-system", "os.exit arg type %s, want Int", code.typ)
	}
	emitter := g.toOstyEmitter()
	code32 := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, "  "+code32+" = trunc i64 "+code.ref+" to i32")
	g.declareRuntimeSymbol(ostyRtOsExitSymbol, "void", []paramInfo{{typ: "i32"}})
	emitter.body = append(emitter.body, "  call void @"+ostyRtOsExitSymbol+"(i32 "+code32+")")
	emitter.body = append(emitter.body, mirUnreachableText())
	g.takeOstyEmitter(emitter)
	return value{typ: "void", ref: "undef"}, true, nil
}
