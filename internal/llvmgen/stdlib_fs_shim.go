package llvmgen

import (
	"fmt"
	"strings"

	"github.com/osty/osty/internal/ast"
	mir "github.com/osty/osty/internal/mir"
)

const ostyRtFsReadSymbol = "osty_rt_fs_read"
const ostyRtFsReadErrorSymbol = "osty_rt_fs_read_error"
const ostyRtFsReadStringSymbol = "osty_rt_fs_read_string"
const ostyRtFsReadStringErrorSymbol = "osty_rt_fs_read_string_error"
const ostyRtFsWriteBytesSymbol = "osty_rt_fs_write_bytes"
const ostyRtFsWriteStringSymbol = "osty_rt_fs_write_string"
const ostyRtFsExistsSymbol = "osty_rt_fs_exists"
const ostyRtFsCreateSymbol = "osty_rt_fs_create"
const ostyRtFsRemoveSymbol = "osty_rt_fs_remove"
const ostyRtFsRenameSymbol = "osty_rt_fs_rename"
const ostyRtFsCopySymbol = "osty_rt_fs_copy"
const ostyRtFsMkdirSymbol = "osty_rt_fs_mkdir"
const ostyRtFsMkdirAllSymbol = "osty_rt_fs_mkdir_all"

var fsBytesSourceTypeSingleton ast.Type = &ast.NamedType{Path: []string{"Bytes"}}

var stdFsReadResultSourceTypeSingleton ast.Type = &ast.NamedType{
	Path: []string{"Result"},
	Args: []ast.Type{
		fsBytesSourceTypeSingleton,
		errorSourceTypeSingleton,
	},
}

var stdFsReadStringResultSourceTypeSingleton ast.Type = &ast.NamedType{
	Path: []string{"Result"},
	Args: []ast.Type{
		&ast.NamedType{Path: []string{"String"}},
		errorSourceTypeSingleton,
	},
}

var stdFsUnitSourceTypeSingleton ast.Type = &ast.TupleType{}

var stdFsUnitErrorResultSourceTypeSingleton ast.Type = &ast.NamedType{
	Path: []string{"Result"},
	Args: []ast.Type{
		stdFsUnitSourceTypeSingleton,
		errorSourceTypeSingleton,
	},
}

func collectStdFsAliases(file *ast.File) map[string]bool {
	out := map[string]bool{}
	if file == nil {
		return out
	}
	for _, use := range file.Uses {
		if use == nil || use.IsFFI() {
			continue
		}
		if len(use.Path) != 2 || use.Path[0] != "std" || use.Path[1] != "fs" {
			continue
		}
		alias := use.Alias
		if alias == "" {
			alias = "fs"
		}
		out[alias] = true
	}
	return out
}

func (g *generator) emitStdFsCall(call *ast.CallExpr) (value, bool, error) {
	field, ok := g.stdFsCallField(call)
	if !ok {
		return value{}, false, nil
	}
	switch field.Name {
	case "read":
		return g.emitStdFsReadCall(call)
	case "readToString":
		return g.emitStdFsReadStringCall(call)
	case "write":
		return g.emitStdFsWriteCall(call)
	case "writeString":
		return g.emitStdFsWriteStringCall(call)
	case "exists":
		return g.emitStdFsExistsCall(call)
	case "create":
		return g.emitStdFsCreateCall(call)
	case "remove":
		return g.emitStdFsRemoveCall(call)
	case "rename":
		return g.emitStdFsRenameCall(call)
	case "copy":
		return g.emitStdFsCopyCall(call)
	case "mkdir":
		return g.emitStdFsMkdirCall(call)
	case "mkdirAll":
		return g.emitStdFsMkdirAllCall(call)
	default:
		return value{}, false, nil
	}
}

func (g *generator) stdFsCallStaticResult(call *ast.CallExpr) (value, bool) {
	field, ok := g.stdFsCallField(call)
	if !ok {
		return value{}, false
	}
	switch field.Name {
	case "read":
		info, ok := builtinResultTypeFromAST(stdFsReadResultSourceTypeSingleton, g.typeEnv())
		if !ok {
			return value{}, false
		}
		return value{typ: info.typ, sourceType: stdFsReadResultSourceTypeSingleton}, true
	case "readToString":
		info, ok := builtinResultTypeFromAST(stdFsReadStringResultSourceTypeSingleton, g.typeEnv())
		if !ok {
			return value{}, false
		}
		return value{typ: info.typ, sourceType: stdFsReadStringResultSourceTypeSingleton}, true
	case "write", "writeString", "create", "remove", "rename", "copy", "mkdir", "mkdirAll":
		info, ok := builtinResultTypeFromAST(stdFsUnitErrorResultSourceTypeSingleton, g.typeEnv())
		if !ok {
			return value{}, false
		}
		return value{typ: info.typ, sourceType: stdFsUnitErrorResultSourceTypeSingleton}, true
	case "exists":
		return value{typ: "i1", sourceType: &ast.NamedType{Path: []string{"Bool"}}}, true
	default:
		return value{}, false
	}
}

func (g *generator) staticStdFsCallSourceType(call *ast.CallExpr) (ast.Type, bool) {
	field, ok := g.stdFsCallField(call)
	if !ok {
		return nil, false
	}
	switch field.Name {
	case "read":
		return stdFsReadResultSourceTypeSingleton, true
	case "readToString":
		return stdFsReadStringResultSourceTypeSingleton, true
	case "write", "writeString", "create", "remove", "rename", "copy", "mkdir", "mkdirAll":
		return stdFsUnitErrorResultSourceTypeSingleton, true
	case "exists":
		return &ast.NamedType{Path: []string{"Bool"}}, true
	default:
		return nil, false
	}
}

func (g *generator) emitStdFsReadCall(call *ast.CallExpr) (value, bool, error) {
	if len(call.Args) != 1 {
		return value{}, true, unsupportedf("call", "fs.read expects 1 argument, got %d", len(call.Args))
	}
	path, err := g.emitStdFsStringArg(call.Args[0], "read", 0)
	if err != nil {
		return value{}, true, err
	}
	return g.emitStdFsPtrResultFromRuntimeCall(
		"fs.read",
		stdFsReadResultSourceTypeSingleton,
		ostyRtFsReadSymbol,
		ostyRtFsReadErrorSymbol,
		[]paramInfo{{typ: "ptr"}},
		[]*LlvmValue{toOstyValue(path)},
	)
}

func (g *generator) emitStdFsReadStringCall(call *ast.CallExpr) (value, bool, error) {
	if len(call.Args) != 1 {
		return value{}, true, unsupportedf("call", "fs.readToString expects 1 argument, got %d", len(call.Args))
	}
	path, err := g.emitStdFsStringArg(call.Args[0], "readToString", 0)
	if err != nil {
		return value{}, true, err
	}
	return g.emitStdFsPtrResultFromRuntimeCall(
		"fs.readToString",
		stdFsReadStringResultSourceTypeSingleton,
		ostyRtFsReadStringSymbol,
		ostyRtFsReadStringErrorSymbol,
		[]paramInfo{{typ: "ptr"}},
		[]*LlvmValue{toOstyValue(path)},
	)
}

func (g *generator) emitStdFsWriteCall(call *ast.CallExpr) (value, bool, error) {
	if len(call.Args) != 2 {
		return value{}, true, unsupportedf("call", "fs.write expects 2 arguments, got %d", len(call.Args))
	}
	path, err := g.emitStdFsStringArg(call.Args[0], "write", 0)
	if err != nil {
		return value{}, true, err
	}
	contents, err := g.emitStdFsBytesArg(call.Args[1], "write", 1)
	if err != nil {
		return value{}, true, err
	}
	v, _, err := g.emitStdEnvUnitErrorResultFromRuntimeCall(
		"fs.write",
		stdFsUnitErrorResultSourceTypeSingleton,
		ostyRtFsWriteBytesSymbol,
		[]paramInfo{{typ: "ptr"}, {typ: "ptr"}},
		[]*LlvmValue{toOstyValue(path), toOstyValue(contents)},
	)
	return v, true, err
}

func (g *generator) emitStdFsWriteStringCall(call *ast.CallExpr) (value, bool, error) {
	if len(call.Args) != 2 {
		return value{}, true, unsupportedf("call", "fs.writeString expects 2 arguments, got %d", len(call.Args))
	}
	path, err := g.emitStdFsStringArg(call.Args[0], "writeString", 0)
	if err != nil {
		return value{}, true, err
	}
	contents, err := g.emitStdFsStringArg(call.Args[1], "writeString", 1)
	if err != nil {
		return value{}, true, err
	}
	v, _, err := g.emitStdEnvUnitErrorResultFromRuntimeCall(
		"fs.writeString",
		stdFsUnitErrorResultSourceTypeSingleton,
		ostyRtFsWriteStringSymbol,
		[]paramInfo{{typ: "ptr"}, {typ: "ptr"}},
		[]*LlvmValue{toOstyValue(path), toOstyValue(contents)},
	)
	return v, true, err
}

func (g *generator) emitStdFsExistsCall(call *ast.CallExpr) (value, bool, error) {
	if len(call.Args) != 1 {
		return value{}, true, unsupportedf("call", "fs.exists expects 1 argument, got %d", len(call.Args))
	}
	path, err := g.emitStdFsStringArg(call.Args[0], "exists", 0)
	if err != nil {
		return value{}, true, err
	}
	g.declareRuntimeSymbol(ostyRtFsExistsSymbol, "i1", []paramInfo{{typ: "ptr"}})
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	out := llvmCall(emitter, "i1", ostyRtFsExistsSymbol, []*LlvmValue{toOstyValue(path)})
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.sourceType = &ast.NamedType{Path: []string{"Bool"}}
	return v, true, nil
}

func (g *generator) emitStdFsCreateCall(call *ast.CallExpr) (value, bool, error) {
	return g.emitStdFsUnitCall(call, "create", ostyRtFsCreateSymbol)
}

func (g *generator) emitStdFsRemoveCall(call *ast.CallExpr) (value, bool, error) {
	return g.emitStdFsUnitCall(call, "remove", ostyRtFsRemoveSymbol)
}

func (g *generator) emitStdFsRenameCall(call *ast.CallExpr) (value, bool, error) {
	if len(call.Args) != 2 {
		return value{}, true, unsupportedf("call", "fs.rename expects 2 arguments, got %d", len(call.Args))
	}
	from, err := g.emitStdFsStringArg(call.Args[0], "rename", 0)
	if err != nil {
		return value{}, true, err
	}
	to, err := g.emitStdFsStringArg(call.Args[1], "rename", 1)
	if err != nil {
		return value{}, true, err
	}
	v, _, err := g.emitStdEnvUnitErrorResultFromRuntimeCall(
		"fs.rename",
		stdFsUnitErrorResultSourceTypeSingleton,
		ostyRtFsRenameSymbol,
		[]paramInfo{{typ: "ptr"}, {typ: "ptr"}},
		[]*LlvmValue{toOstyValue(from), toOstyValue(to)},
	)
	return v, true, err
}

func (g *generator) emitStdFsCopyCall(call *ast.CallExpr) (value, bool, error) {
	if len(call.Args) != 2 {
		return value{}, true, unsupportedf("call", "fs.copy expects 2 arguments, got %d", len(call.Args))
	}
	from, err := g.emitStdFsStringArg(call.Args[0], "copy", 0)
	if err != nil {
		return value{}, true, err
	}
	to, err := g.emitStdFsStringArg(call.Args[1], "copy", 1)
	if err != nil {
		return value{}, true, err
	}
	v, _, err := g.emitStdEnvUnitErrorResultFromRuntimeCall(
		"fs.copy",
		stdFsUnitErrorResultSourceTypeSingleton,
		ostyRtFsCopySymbol,
		[]paramInfo{{typ: "ptr"}, {typ: "ptr"}},
		[]*LlvmValue{toOstyValue(from), toOstyValue(to)},
	)
	return v, true, err
}

func (g *generator) emitStdFsMkdirCall(call *ast.CallExpr) (value, bool, error) {
	return g.emitStdFsUnitCall(call, "mkdir", ostyRtFsMkdirSymbol)
}

func (g *generator) emitStdFsMkdirAllCall(call *ast.CallExpr) (value, bool, error) {
	return g.emitStdFsUnitCall(call, "mkdirAll", ostyRtFsMkdirAllSymbol)
}

func (g *generator) emitStdFsUnitCall(call *ast.CallExpr, name, symbol string) (value, bool, error) {
	if len(call.Args) != 1 {
		return value{}, true, unsupportedf("call", "fs.%s expects 1 argument, got %d", name, len(call.Args))
	}
	path, err := g.emitStdFsStringArg(call.Args[0], name, 0)
	if err != nil {
		return value{}, true, err
	}
	v, _, err := g.emitStdEnvUnitErrorResultFromRuntimeCall(
		"fs."+name,
		stdFsUnitErrorResultSourceTypeSingleton,
		symbol,
		[]paramInfo{{typ: "ptr"}},
		[]*LlvmValue{toOstyValue(path)},
	)
	return v, true, err
}

func (g *generator) emitStdFsPtrResultFromRuntimeCall(prefix string, sourceType ast.Type, valueSymbol, errorSymbol string, params []paramInfo, args []*LlvmValue) (value, bool, error) {
	info, ok := builtinResultTypeFromAST(sourceType, g.typeEnv())
	if !ok {
		return value{}, true, unsupportedf("type-system", "%s result type unavailable", prefix)
	}
	if g.resultTypes == nil {
		g.resultTypes = map[string]builtinResultType{}
	}
	g.resultTypes[info.typ] = info
	if info.okTyp != "ptr" || info.errTyp != "ptr" {
		return value{}, true, unsupportedf("type-system", "%s currently needs ptr-backed Result, got ok=%s err=%s", prefix, info.okTyp, info.errTyp)
	}
	g.declareRuntimeSymbol(valueSymbol, "ptr", params)
	g.declareRuntimeSymbol(errorSymbol, "ptr", nil)
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	out := llvmCall(emitter, "ptr", valueSymbol, args)
	failed := llvmCompare(emitter, "eq", out, toOstyValue(value{typ: "ptr", ref: "null"}))
	errLabel := llvmNextLabel(emitter, prefix+".err")
	okLabel := llvmNextLabel(emitter, prefix+".ok")
	contLabel := llvmNextLabel(emitter, prefix+".cont")
	emitter.body = append(emitter.body, "  br i1 "+failed.name+", label %"+errLabel+", label %"+okLabel)

	emitter.body = append(emitter.body, errLabel+":")
	errText := llvmCall(emitter, "ptr", errorSymbol, nil)
	errResult := llvmStructLiteral(emitter, info.typ, []*LlvmValue{
		toOstyValue(value{typ: "i64", ref: "1"}),
		toOstyValue(llvmZeroValue(info.okTyp)),
		errText,
	})
	emitter.body = append(emitter.body, "  br label %"+contLabel)

	emitter.body = append(emitter.body, okLabel+":")
	okResult := llvmStructLiteral(emitter, info.typ, []*LlvmValue{
		toOstyValue(value{typ: "i64", ref: "0"}),
		out,
		toOstyValue(llvmZeroValue(info.errTyp)),
	})
	emitter.body = append(emitter.body, "  br label %"+contLabel)

	emitter.body = append(emitter.body, contLabel+":")
	phi := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, "  "+phi+" = phi "+info.typ+" [ "+errResult.name+", %"+errLabel+" ], [ "+okResult.name+", %"+okLabel+" ]")
	g.takeOstyEmitter(emitter)
	v := value{typ: info.typ, ref: phi, sourceType: sourceType}
	v.rootPaths = g.rootPathsForType(v.typ)
	return v, true, nil
}

func (g *generator) emitStdFsStringArg(arg *ast.Arg, name string, index int) (value, error) {
	if arg == nil || arg.Name != "" || arg.Value == nil {
		return value{}, unsupportedf("call", "fs.%s requires positional arguments", name)
	}
	src, ok := g.staticExprSourceType(arg.Value)
	if !ok {
		return value{}, unsupportedf("type-system", "fs.%s arg %d source type unknown, want String", name, index+1)
	}
	resolved, err := llvmResolveAliasType(src, g.typeEnv(), map[string]bool{})
	if err != nil || !llvmNamedTypeIsString(resolved) {
		return value{}, unsupportedf("type-system", "fs.%s arg %d source type is not String", name, index+1)
	}
	v, err := g.emitExpr(arg.Value)
	if err != nil {
		return value{}, err
	}
	v = g.protectManagedTemporary("fs."+name+".arg", v)
	loaded, err := g.loadIfPointer(v)
	if err != nil {
		return value{}, err
	}
	if loaded.typ != "ptr" {
		return value{}, unsupportedf("type-system", "fs.%s arg %d type %s, want String", name, index+1, loaded.typ)
	}
	return loaded, nil
}

func (g *generator) emitStdFsBytesArg(arg *ast.Arg, name string, index int) (value, error) {
	if arg == nil || arg.Name != "" || arg.Value == nil {
		return value{}, unsupportedf("call", "fs.%s requires positional arguments", name)
	}
	src, ok := g.staticExprSourceType(arg.Value)
	if !ok {
		return value{}, unsupportedf("type-system", "fs.%s arg %d source type unknown, want Bytes", name, index+1)
	}
	resolved, err := llvmResolveAliasType(src, g.typeEnv(), map[string]bool{})
	if err != nil || !llvmNamedTypeIsBytes(resolved) {
		return value{}, unsupportedf("type-system", "fs.%s arg %d source type is not Bytes", name, index+1)
	}
	v, err := g.emitExpr(arg.Value)
	if err != nil {
		return value{}, err
	}
	v = g.protectManagedTemporary("fs."+name+".arg", v)
	loaded, err := g.loadIfPointer(v)
	if err != nil {
		return value{}, err
	}
	if loaded.typ != "ptr" {
		return value{}, unsupportedf("type-system", "fs.%s arg %d type %s, want Bytes", name, index+1, loaded.typ)
	}
	return loaded, nil
}

func (g *generator) stdFsCallField(call *ast.CallExpr) (*ast.FieldExpr, bool) {
	if call == nil || len(g.stdFsAliases) == 0 {
		return nil, false
	}
	field, ok := fieldExprOfCallFn(call)
	if !ok || field.IsOptional {
		return nil, false
	}
	alias, ok := field.X.(*ast.Ident)
	if !ok || !g.stdFsAliases[alias.Name] {
		return nil, false
	}
	return field, true
}

func (g *mirGen) emitStdFsCall(c *mir.CallInstr, fnRef *mir.FnRef) (bool, error) {
	method := strings.TrimPrefix(fnRef.Symbol, "std.fs.")
	switch method {
	case "read":
		return true, g.emitStdFsPtrResultMIR(c, "read", ostyRtFsReadSymbol, ostyRtFsReadErrorSymbol)
	case "readToString":
		return true, g.emitStdFsPtrResultMIR(c, "readToString", ostyRtFsReadStringSymbol, ostyRtFsReadStringErrorSymbol)
	case "write":
		return true, g.emitStdFsWriteMIR(c)
	case "writeString":
		return true, g.emitStdFsWriteStringMIR(c)
	case "exists":
		return true, g.emitStdFsExistsMIR(c)
	case "create":
		return true, g.emitStdFsUnitResultMIR(c, "create", ostyRtFsCreateSymbol)
	case "remove":
		return true, g.emitStdFsUnitResultMIR(c, "remove", ostyRtFsRemoveSymbol)
	case "rename":
		return true, g.emitStdFsRenameMIR(c)
	case "copy":
		return true, g.emitStdFsCopyMIR(c)
	case "mkdir":
		return true, g.emitStdFsUnitResultMIR(c, "mkdir", ostyRtFsMkdirSymbol)
	case "mkdirAll":
		return true, g.emitStdFsUnitResultMIR(c, "mkdirAll", ostyRtFsMkdirAllSymbol)
	default:
		return false, nil
	}
}

func (g *mirGen) emitStdFsWriteMIR(c *mir.CallInstr) error {
	if len(c.Args) != 2 {
		return unsupported("mir-mvp", fmt.Sprintf("std.fs.write requires two positional arguments, got %d", len(c.Args)))
	}
	path, err := g.emitStdFsStringOperandMIR(c.Args[0], "write", 0)
	if err != nil {
		return err
	}
	contents, err := g.emitStdFsBytesOperandMIR(c.Args[1], "write", 1)
	if err != nil {
		return err
	}
	return g.emitStdFsUnitResultMIRArgs(c, ostyRtFsWriteBytesSymbol, []string{mirArgSlotPtr(path), mirArgSlotPtr(contents)})
}

func (g *mirGen) emitStdFsWriteStringMIR(c *mir.CallInstr) error {
	if len(c.Args) != 2 {
		return unsupported("mir-mvp", fmt.Sprintf("std.fs.writeString requires two positional arguments, got %d", len(c.Args)))
	}
	path, err := g.emitStdFsStringOperandMIR(c.Args[0], "writeString", 0)
	if err != nil {
		return err
	}
	contents, err := g.emitStdFsStringOperandMIR(c.Args[1], "writeString", 1)
	if err != nil {
		return err
	}
	return g.emitStdFsUnitResultMIRArgs(c, ostyRtFsWriteStringSymbol, []string{mirArgSlotPtr(path), mirArgSlotPtr(contents)})
}

func (g *mirGen) emitStdFsExistsMIR(c *mir.CallInstr) error {
	if len(c.Args) != 1 {
		return unsupported("mir-mvp", fmt.Sprintf("std.fs.exists requires one positional argument, got %d", len(c.Args)))
	}
	path, err := g.emitStdFsStringOperandMIR(c.Args[0], "exists", 0)
	if err != nil {
		return err
	}
	g.declareRuntime(ostyRtFsExistsSymbol, mirRuntimeDeclareLine("i1", ostyRtFsExistsSymbol, "ptr"))
	return g.emitCallSiteByName(c, ostyRtFsExistsSymbol, "i1", []string{mirArgSlotPtr(path)})
}

func (g *mirGen) emitStdFsRenameMIR(c *mir.CallInstr) error {
	if len(c.Args) != 2 {
		return unsupported("mir-mvp", fmt.Sprintf("std.fs.rename requires two positional arguments, got %d", len(c.Args)))
	}
	from, err := g.emitStdFsStringOperandMIR(c.Args[0], "rename", 0)
	if err != nil {
		return err
	}
	to, err := g.emitStdFsStringOperandMIR(c.Args[1], "rename", 1)
	if err != nil {
		return err
	}
	return g.emitStdFsUnitResultMIRArgs(c, ostyRtFsRenameSymbol, []string{mirArgSlotPtr(from), mirArgSlotPtr(to)})
}

func (g *mirGen) emitStdFsCopyMIR(c *mir.CallInstr) error {
	if len(c.Args) != 2 {
		return unsupported("mir-mvp", fmt.Sprintf("std.fs.copy requires two positional arguments, got %d", len(c.Args)))
	}
	from, err := g.emitStdFsStringOperandMIR(c.Args[0], "copy", 0)
	if err != nil {
		return err
	}
	to, err := g.emitStdFsStringOperandMIR(c.Args[1], "copy", 1)
	if err != nil {
		return err
	}
	return g.emitStdFsUnitResultMIRArgs(c, ostyRtFsCopySymbol, []string{mirArgSlotPtr(from), mirArgSlotPtr(to)})
}

func (g *mirGen) emitStdFsUnitResultMIR(c *mir.CallInstr, method, symbol string) error {
	if len(c.Args) != 1 {
		return unsupported("mir-mvp", fmt.Sprintf("std.fs.%s requires one positional argument, got %d", method, len(c.Args)))
	}
	path, err := g.emitStdFsStringOperandMIR(c.Args[0], method, 0)
	if err != nil {
		return err
	}
	return g.emitStdFsUnitResultMIRArgs(c, symbol, []string{mirArgSlotPtr(path)})
}

func (g *mirGen) emitStdFsUnitResultMIRArgs(c *mir.CallInstr, symbol string, argStrs []string) error {
	g.declareRuntime(symbol, mirRuntimeDeclareLine("ptr", symbol, strings.Join(mirPtrParamList(len(argStrs)), ", ")))
	if c.Dest == nil {
		g.fnBuf.WriteString(mirCallStmtLine("ptr", symbol, strings.Join(argStrs, ", ")))
		return nil
	}
	destLoc := g.fn.Local(c.Dest.Local)
	if destLoc == nil {
		return fmt.Errorf("mir-mvp: std.fs unit-result dest into unknown local %d", c.Dest.Local)
	}
	aggLLVM := g.llvmType(destLoc.Type)
	errReg := g.fresh()
	g.fnBuf.WriteString(mirCallValueLine(errReg, "ptr", symbol, strings.Join(argStrs, ", ")))
	isNil := g.fresh()
	g.fnBuf.WriteString(mirICmpEqLine(isNil, "ptr", errReg, "null"))
	okLabel := g.freshLabel("fs.unit.ok")
	errLabel := g.freshLabel("fs.unit.err")
	contLabel := g.freshLabel("fs.unit.cont")
	g.fnBuf.WriteString(mirBrCondLine(isNil, okLabel, errLabel))

	g.fnBuf.WriteString(mirLabelLine(okLabel))
	ok1 := g.fresh()
	g.fnBuf.WriteString(mirInsertValueI64Line(ok1, aggLLVM, "undef", "0", "0"))
	ok2 := g.fresh()
	g.fnBuf.WriteString(mirInsertValueI64Line(ok2, aggLLVM, ok1, "0", "1"))
	g.fnBuf.WriteString(mirBrUncondLine(contLabel))

	g.fnBuf.WriteString(mirLabelLine(errLabel))
	errPayload := g.fresh()
	g.fnBuf.WriteString(mirPtrToIntLine(errPayload, errReg, "i64"))
	err1 := g.fresh()
	g.fnBuf.WriteString(mirInsertValueI64Line(err1, aggLLVM, "undef", "1", "0"))
	err2 := g.fresh()
	g.fnBuf.WriteString(mirInsertValueI64Line(err2, aggLLVM, err1, errPayload, "1"))
	g.fnBuf.WriteString(mirBrUncondLine(contLabel))

	g.fnBuf.WriteString(mirLabelLine(contLabel))
	phi := g.fresh()
	g.fnBuf.WriteString(mirPhiTwoLine(phi, aggLLVM, ok2, okLabel, err2, errLabel))
	g.fnBuf.WriteString(mirStoreLine(aggLLVM, phi, g.localSlots[c.Dest.Local]))
	return nil
}

func (g *mirGen) emitStdFsPtrResultMIR(c *mir.CallInstr, method, valueSymbol, errorSymbol string) error {
	if len(c.Args) != 1 {
		return unsupported("mir-mvp", fmt.Sprintf("std.fs.%s requires one positional argument, got %d", method, len(c.Args)))
	}
	path, err := g.emitStdFsStringOperandMIR(c.Args[0], method, 0)
	if err != nil {
		return err
	}
	g.declareRuntime(valueSymbol, mirRuntimeDeclareLine("ptr", valueSymbol, "ptr"))
	if c.Dest == nil {
		g.fnBuf.WriteString(mirCallStmtLine("ptr", valueSymbol, mirArgSlotPtr(path)))
		return nil
	}
	destLoc := g.fn.Local(c.Dest.Local)
	if destLoc == nil {
		return fmt.Errorf("mir-mvp: std.fs.%s dest into unknown local %d", method, c.Dest.Local)
	}
	aggLLVM := g.llvmType(destLoc.Type)
	g.declareRuntime(errorSymbol, mirRuntimeDeclarePtrNoArgsLine(errorSymbol))
	valReg := g.fresh()
	g.fnBuf.WriteString(mirCallValueLine(valReg, "ptr", valueSymbol, mirArgSlotPtr(path)))
	isNil := g.fresh()
	g.fnBuf.WriteString(mirICmpEqLine(isNil, "ptr", valReg, "null"))
	errLabel := g.freshLabel("fs.ptr.err")
	okLabel := g.freshLabel("fs.ptr.ok")
	contLabel := g.freshLabel("fs.ptr.cont")
	g.fnBuf.WriteString(mirBrCondLine(isNil, errLabel, okLabel))

	g.fnBuf.WriteString(mirLabelLine(errLabel))
	errText := g.fresh()
	g.fnBuf.WriteString(mirCallValueNoArgsLine(errText, "ptr", errorSymbol))
	errPayload := g.fresh()
	g.fnBuf.WriteString(mirPtrToIntLine(errPayload, errText, "i64"))
	err1 := g.fresh()
	g.fnBuf.WriteString(mirInsertValueI64Line(err1, aggLLVM, "undef", "1", "0"))
	err2 := g.fresh()
	g.fnBuf.WriteString(mirInsertValueI64Line(err2, aggLLVM, err1, errPayload, "1"))
	g.fnBuf.WriteString(mirBrUncondLine(contLabel))

	g.fnBuf.WriteString(mirLabelLine(okLabel))
	okPayload := g.fresh()
	g.fnBuf.WriteString(mirPtrToIntLine(okPayload, valReg, "i64"))
	ok1 := g.fresh()
	g.fnBuf.WriteString(mirInsertValueI64Line(ok1, aggLLVM, "undef", "0", "0"))
	ok2 := g.fresh()
	g.fnBuf.WriteString(mirInsertValueI64Line(ok2, aggLLVM, ok1, okPayload, "1"))
	g.fnBuf.WriteString(mirBrUncondLine(contLabel))

	g.fnBuf.WriteString(mirLabelLine(contLabel))
	phi := g.fresh()
	g.fnBuf.WriteString(mirPhiTwoLine(phi, aggLLVM, err2, errLabel, ok2, okLabel))
	g.fnBuf.WriteString(mirStoreLine(aggLLVM, phi, g.localSlots[c.Dest.Local]))
	return nil
}

func (g *mirGen) emitStdFsStringOperandMIR(op mir.Operand, method string, index int) (string, error) {
	if op == nil || op.Type() == nil || !nativeTypeIsString(op.Type()) {
		return "", unsupported("mir-mvp", fmt.Sprintf("std.fs.%s arg %d type %s, want String", method, index+1, mirTypeString(op.Type())))
	}
	return g.evalOperand(op, op.Type())
}

func (g *mirGen) emitStdFsBytesOperandMIR(op mir.Operand, method string, index int) (string, error) {
	if op == nil || op.Type() == nil || !nativeTypeIsBytes(op.Type()) {
		return "", unsupported("mir-mvp", fmt.Sprintf("std.fs.%s arg %d type %s, want Bytes", method, index+1, mirTypeString(op.Type())))
	}
	return g.evalOperand(op, op.Type())
}

func mirPtrParamList(n int) []string {
	if n <= 0 {
		return nil
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, "ptr")
	}
	return out
}
