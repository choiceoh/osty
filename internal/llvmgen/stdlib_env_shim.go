// stdlib_env_shim.go — backend shim for `std.env` surface.
//
// The pure-Osty stdlib body in internal/stdlib/modules/env.osty is a
// placeholder returning an empty list; this shim bypasses it so real
// process environment reaches the program. `env.args()`,
// `env.get(name)`, `env.require(name)`, `env.vars()`,
// `env.currentDir()`, and `env.setCurrentDir(path)` are covered; the
// remaining std.env surface (set, unset, …)
// still falls through to LLVM015 and needs its own runtime helper + switch arm.
package llvmgen

import (
	"github.com/osty/osty/internal/ast"
)

const ostyRtEnvArgsSymbol = "osty_rt_env_args"
const ostyRtEnvArgsInitSymbol = "osty_rt_env_args_init"
const ostyRtEnvGetSymbol = "osty_rt_env_get"
const ostyRtEnvVarsSymbol = "osty_rt_env_vars"
const ostyRtEnvCurrentDirSymbol = "osty_rt_env_current_dir"
const ostyRtEnvCurrentDirErrorSymbol = "osty_rt_env_current_dir_error"
const ostyRtEnvSetCurrentDirSymbol = "osty_rt_env_set_current_dir"

// Shared across every env.args() call site so type inference doesn't
// re-allocate an identical `List<String>` AST tree per reference.
var stdEnvArgsSourceTypeSingleton ast.Type = &ast.NamedType{
	Path: []string{"List"},
	Args: []ast.Type{&ast.NamedType{Path: []string{"String"}}},
}

var stdEnvGetSourceTypeSingleton ast.Type = &ast.OptionalType{
	Inner: &ast.NamedType{Path: []string{"String"}},
}

var stdEnvRequireResultSourceTypeSingleton ast.Type = &ast.NamedType{
	Path: []string{"Result"},
	Args: []ast.Type{
		&ast.NamedType{Path: []string{"String"}},
		errorSourceTypeSingleton,
	},
}

var unitTupleSourceTypeSingleton ast.Type = &ast.TupleType{}

var stdEnvSetCurrentDirResultSourceTypeSingleton ast.Type = &ast.NamedType{
	Path: []string{"Result"},
	Args: []ast.Type{
		unitTupleSourceTypeSingleton,
		errorSourceTypeSingleton,
	},
}

var stdEnvVarsSourceTypeSingleton ast.Type = &ast.NamedType{
	Path: []string{"Map"},
	Args: []ast.Type{
		&ast.NamedType{Path: []string{"String"}},
		&ast.NamedType{Path: []string{"String"}},
	},
}

func collectStdEnvAliases(file *ast.File) map[string]bool {
	out := map[string]bool{}
	if file == nil {
		return out
	}
	for _, use := range file.Uses {
		if use == nil || use.IsFFI() {
			continue
		}
		if len(use.Path) != 2 || use.Path[0] != "std" || use.Path[1] != "env" {
			continue
		}
		alias := use.Alias
		if alias == "" {
			alias = "env"
		}
		out[alias] = true
	}
	return out
}

func (g *generator) emitStdEnvCall(call *ast.CallExpr) (value, bool, error) {
	field, ok := g.stdEnvCallField(call)
	if !ok {
		return value{}, false, nil
	}
	switch field.Name {
	case "args":
		return g.emitStdEnvArgsCall(call)
	case "get":
		return g.emitStdEnvGetCall(call)
	case "require":
		return g.emitStdEnvRequireCall(call)
	case "vars":
		return g.emitStdEnvVarsCall(call)
	case "currentDir":
		return g.emitStdEnvCurrentDirCall(call)
	case "setCurrentDir":
		return g.emitStdEnvSetCurrentDirCall(call)
	default:
		return value{}, false, nil
	}
}

func (g *generator) stdEnvCallStaticResult(call *ast.CallExpr) (value, bool) {
	field, ok := g.stdEnvCallField(call)
	if !ok {
		return value{}, false
	}
	switch field.Name {
	case "args":
		return value{
			typ:            "ptr",
			gcManaged:      true,
			listElemTyp:    "ptr",
			listElemString: true,
			sourceType:     stdEnvArgsSourceTypeSingleton,
		}, true
	case "get":
		return value{
			typ:        "ptr",
			gcManaged:  true,
			sourceType: stdEnvGetSourceTypeSingleton,
		}, true
	case "require":
		info, ok := builtinResultTypeFromAST(stdEnvRequireResultSourceTypeSingleton, g.typeEnv())
		if !ok {
			return value{}, false
		}
		return value{
			typ:        info.typ,
			sourceType: stdEnvRequireResultSourceTypeSingleton,
		}, true
	case "vars":
		return value{
			typ:          "ptr",
			gcManaged:    true,
			mapKeyTyp:    "ptr",
			mapValueTyp:  "ptr",
			mapKeyString: true,
			sourceType:   stdEnvVarsSourceTypeSingleton,
		}, true
	case "currentDir":
		info, ok := builtinResultTypeFromAST(stdEnvRequireResultSourceTypeSingleton, g.typeEnv())
		if !ok {
			return value{}, false
		}
		return value{
			typ:        info.typ,
			sourceType: stdEnvRequireResultSourceTypeSingleton,
		}, true
	case "setCurrentDir":
		info, ok := builtinResultTypeFromAST(stdEnvSetCurrentDirResultSourceTypeSingleton, g.typeEnv())
		if !ok {
			return value{}, false
		}
		return value{
			typ:        info.typ,
			sourceType: stdEnvSetCurrentDirResultSourceTypeSingleton,
		}, true
	default:
		return value{}, false
	}
}

// staticStdEnvCallSourceType recovers the source-level env call result
// so downstream Optional/List-aware emitters (`??`, `.get(i)`, …) can
// keep threading the right source type.
func (g *generator) staticStdEnvCallSourceType(call *ast.CallExpr) (ast.Type, bool) {
	field, ok := g.stdEnvCallField(call)
	if !ok {
		return nil, false
	}
	switch field.Name {
	case "args":
		return stdEnvArgsSourceTypeSingleton, true
	case "get":
		return stdEnvGetSourceTypeSingleton, true
	case "require":
		return stdEnvRequireResultSourceTypeSingleton, true
	case "vars":
		return stdEnvVarsSourceTypeSingleton, true
	case "currentDir":
		return stdEnvRequireResultSourceTypeSingleton, true
	case "setCurrentDir":
		return stdEnvSetCurrentDirResultSourceTypeSingleton, true
	default:
		return nil, false
	}
}

func (g *generator) emitStdEnvArgsCall(call *ast.CallExpr) (value, bool, error) {
	if len(call.Args) != 0 {
		return value{}, true, unsupportedf("call", "env.args takes no arguments, got %d", len(call.Args))
	}
	g.declareRuntimeSymbol(ostyRtEnvArgsSymbol, "ptr", nil)
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	out := llvmCall(emitter, "ptr", ostyRtEnvArgsSymbol, nil)
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.gcManaged = true
	v.listElemTyp = "ptr"
	v.listElemString = true
	v.sourceType = stdEnvArgsSourceTypeSingleton
	v.rootPaths = g.rootPathsForType(v.typ)
	return v, true, nil
}

func (g *generator) emitStdEnvGetCall(call *ast.CallExpr) (value, bool, error) {
	if len(call.Args) != 1 {
		return value{}, true, unsupportedf("call", "env.get expects 1 argument, got %d", len(call.Args))
	}
	arg := call.Args[0]
	if arg == nil || arg.Name != "" || arg.Value == nil {
		return value{}, true, unsupported("call", "env.get requires one positional String argument")
	}
	name, err := g.emitExpr(arg.Value)
	if err != nil {
		return value{}, true, err
	}
	name, err = g.loadIfPointer(name)
	if err != nil {
		return value{}, true, err
	}
	if name.typ != "ptr" {
		return value{}, true, unsupportedf("type-system", "env.get arg 1 type %s, want String", name.typ)
	}
	g.declareRuntimeSymbol(ostyRtEnvGetSymbol, "ptr", []paramInfo{{typ: "ptr"}})
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	out := llvmCall(emitter, "ptr", ostyRtEnvGetSymbol, []*LlvmValue{toOstyValue(name)})
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.gcManaged = true
	v.sourceType = stdEnvGetSourceTypeSingleton
	v.rootPaths = g.rootPathsForType(v.typ)
	return v, true, nil
}

func (g *generator) emitStdEnvRequireCall(call *ast.CallExpr) (value, bool, error) {
	if len(call.Args) != 1 {
		return value{}, true, unsupportedf("call", "env.require expects 1 argument, got %d", len(call.Args))
	}
	arg := call.Args[0]
	if arg == nil || arg.Name != "" || arg.Value == nil {
		return value{}, true, unsupported("call", "env.require requires one positional String argument")
	}
	info, ok := builtinResultTypeFromAST(stdEnvRequireResultSourceTypeSingleton, g.typeEnv())
	if !ok {
		return value{}, true, unsupported("type-system", "env.require Result<String, Error> type is unavailable")
	}
	if g.resultTypes == nil {
		g.resultTypes = map[string]builtinResultType{}
	}
	g.resultTypes[info.typ] = info
	if info.okTyp != "ptr" || info.errTyp != "ptr" {
		return value{}, true, unsupportedf("type-system", "env.require currently needs ptr-backed Result<String, Error>, got ok=%s err=%s", info.okTyp, info.errTyp)
	}
	name, err := g.emitExpr(arg.Value)
	if err != nil {
		return value{}, true, err
	}
	name = g.protectManagedTemporary("env.require.name", name)
	name, err = g.loadIfPointer(name)
	if err != nil {
		return value{}, true, err
	}
	if name.typ != "ptr" {
		return value{}, true, unsupportedf("type-system", "env.require arg 1 type %s, want String", name.typ)
	}
	g.declareRuntimeSymbol(ostyRtEnvGetSymbol, "ptr", []paramInfo{{typ: "ptr"}})
	g.declareRuntimeSymbol(llvmStringRuntimeConcatSymbol(), "ptr", []paramInfo{{typ: "ptr"}, {typ: "ptr"}})
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	found := llvmCall(emitter, "ptr", ostyRtEnvGetSymbol, []*LlvmValue{toOstyValue(name)})
	missing := llvmCompare(emitter, "eq", found, toOstyValue(value{typ: "ptr", ref: "null"}))
	missingLabel := llvmNextLabel(emitter, "env.require.missing")
	okLabel := llvmNextLabel(emitter, "env.require.ok")
	contLabel := llvmNextLabel(emitter, "env.require.cont")
	emitter.body = append(emitter.body, "  br i1 "+missing.name+", label %"+missingLabel+", label %"+okLabel)

	emitter.body = append(emitter.body, missingLabel+":")
	prefix := llvmStringLiteral(emitter, "environment variable not set: ")
	message := llvmStringConcat(emitter, prefix, toOstyValue(name))
	errResult := llvmStructLiteral(emitter, info.typ, []*LlvmValue{
		toOstyValue(value{typ: "i64", ref: "1"}),
		toOstyValue(llvmZeroValue(info.okTyp)),
		message,
	})
	emitter.body = append(emitter.body, "  br label %"+contLabel)

	emitter.body = append(emitter.body, okLabel+":")
	okResult := llvmStructLiteral(emitter, info.typ, []*LlvmValue{
		toOstyValue(value{typ: "i64", ref: "0"}),
		found,
		toOstyValue(llvmZeroValue(info.errTyp)),
	})
	emitter.body = append(emitter.body, "  br label %"+contLabel)

	emitter.body = append(emitter.body, contLabel+":")
	phi := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, "  "+phi+" = phi "+info.typ+" [ "+errResult.name+", %"+missingLabel+" ], [ "+okResult.name+", %"+okLabel+" ]")
	g.takeOstyEmitter(emitter)
	v := value{
		typ:        info.typ,
		ref:        phi,
		sourceType: stdEnvRequireResultSourceTypeSingleton,
	}
	v.rootPaths = g.rootPathsForType(v.typ)
	return v, true, nil
}

func (g *generator) emitStdEnvVarsCall(call *ast.CallExpr) (value, bool, error) {
	if len(call.Args) != 0 {
		return value{}, true, unsupportedf("call", "env.vars takes no arguments, got %d", len(call.Args))
	}
	g.declareRuntimeSymbol(ostyRtEnvVarsSymbol, "ptr", nil)
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	out := llvmCall(emitter, "ptr", ostyRtEnvVarsSymbol, nil)
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.gcManaged = true
	v.mapKeyTyp = "ptr"
	v.mapValueTyp = "ptr"
	v.mapKeyString = true
	v.sourceType = stdEnvVarsSourceTypeSingleton
	v.rootPaths = g.rootPathsForType(v.typ)
	return v, true, nil
}

func (g *generator) emitStdEnvCurrentDirCall(call *ast.CallExpr) (value, bool, error) {
	if len(call.Args) != 0 {
		return value{}, true, unsupportedf("call", "env.currentDir takes no arguments, got %d", len(call.Args))
	}
	info, ok := builtinResultTypeFromAST(stdEnvRequireResultSourceTypeSingleton, g.typeEnv())
	if !ok {
		return value{}, true, unsupported("type-system", "env.currentDir Result<String, Error> type is unavailable")
	}
	if g.resultTypes == nil {
		g.resultTypes = map[string]builtinResultType{}
	}
	g.resultTypes[info.typ] = info
	if info.okTyp != "ptr" || info.errTyp != "ptr" {
		return value{}, true, unsupportedf("type-system", "env.currentDir currently needs ptr-backed Result<String, Error>, got ok=%s err=%s", info.okTyp, info.errTyp)
	}
	g.declareRuntimeSymbol(ostyRtEnvCurrentDirSymbol, "ptr", nil)
	g.declareRuntimeSymbol(ostyRtEnvCurrentDirErrorSymbol, "ptr", nil)
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	dir := llvmCall(emitter, "ptr", ostyRtEnvCurrentDirSymbol, nil)
	missing := llvmCompare(emitter, "eq", dir, toOstyValue(value{typ: "ptr", ref: "null"}))
	missingLabel := llvmNextLabel(emitter, "env.current_dir.err")
	okLabel := llvmNextLabel(emitter, "env.current_dir.ok")
	contLabel := llvmNextLabel(emitter, "env.current_dir.cont")
	emitter.body = append(emitter.body, "  br i1 "+missing.name+", label %"+missingLabel+", label %"+okLabel)

	emitter.body = append(emitter.body, missingLabel+":")
	errText := llvmCall(emitter, "ptr", ostyRtEnvCurrentDirErrorSymbol, nil)
	errResult := llvmStructLiteral(emitter, info.typ, []*LlvmValue{
		toOstyValue(value{typ: "i64", ref: "1"}),
		toOstyValue(llvmZeroValue(info.okTyp)),
		errText,
	})
	emitter.body = append(emitter.body, "  br label %"+contLabel)

	emitter.body = append(emitter.body, okLabel+":")
	okResult := llvmStructLiteral(emitter, info.typ, []*LlvmValue{
		toOstyValue(value{typ: "i64", ref: "0"}),
		dir,
		toOstyValue(llvmZeroValue(info.errTyp)),
	})
	emitter.body = append(emitter.body, "  br label %"+contLabel)

	emitter.body = append(emitter.body, contLabel+":")
	phi := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, "  "+phi+" = phi "+info.typ+" [ "+errResult.name+", %"+missingLabel+" ], [ "+okResult.name+", %"+okLabel+" ]")
	g.takeOstyEmitter(emitter)
	v := value{
		typ:        info.typ,
		ref:        phi,
		sourceType: stdEnvRequireResultSourceTypeSingleton,
	}
	v.rootPaths = g.rootPathsForType(v.typ)
	return v, true, nil
}

func (g *generator) emitStdEnvSetCurrentDirCall(call *ast.CallExpr) (value, bool, error) {
	if len(call.Args) != 1 {
		return value{}, true, unsupportedf("call", "env.setCurrentDir expects 1 argument, got %d", len(call.Args))
	}
	arg := call.Args[0]
	if arg == nil || arg.Name != "" || arg.Value == nil {
		return value{}, true, unsupported("call", "env.setCurrentDir requires one positional String argument")
	}
	unitInfo := g.registerTupleType(nil, nil)
	info, ok := builtinResultTypeFromAST(stdEnvSetCurrentDirResultSourceTypeSingleton, g.typeEnv())
	if !ok {
		return value{}, true, unsupported("type-system", "env.setCurrentDir Result<(), Error> type is unavailable")
	}
	if g.resultTypes == nil {
		g.resultTypes = map[string]builtinResultType{}
	}
	g.resultTypes[info.typ] = info
	if info.okTyp != unitInfo.typ || info.errTyp != "ptr" {
		return value{}, true, unsupportedf("type-system", "env.setCurrentDir currently needs Result<%s, ptr>, got ok=%s err=%s", unitInfo.typ, info.okTyp, info.errTyp)
	}
	path, err := g.emitExpr(arg.Value)
	if err != nil {
		return value{}, true, err
	}
	path = g.protectManagedTemporary("env.setCurrentDir.path", path)
	path, err = g.loadIfPointer(path)
	if err != nil {
		return value{}, true, err
	}
	if path.typ != "ptr" {
		return value{}, true, unsupportedf("type-system", "env.setCurrentDir arg 1 type %s, want String", path.typ)
	}
	g.declareRuntimeSymbol(ostyRtEnvSetCurrentDirSymbol, "ptr", []paramInfo{{typ: "ptr"}})
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	errText := llvmCall(emitter, "ptr", ostyRtEnvSetCurrentDirSymbol, []*LlvmValue{toOstyValue(path)})
	failed := llvmCompare(emitter, "ne", errText, toOstyValue(value{typ: "ptr", ref: "null"}))
	errLabel := llvmNextLabel(emitter, "env.set_current_dir.err")
	okLabel := llvmNextLabel(emitter, "env.set_current_dir.ok")
	contLabel := llvmNextLabel(emitter, "env.set_current_dir.cont")
	emitter.body = append(emitter.body, "  br i1 "+failed.name+", label %"+errLabel+", label %"+okLabel)

	emitter.body = append(emitter.body, errLabel+":")
	errResult := llvmStructLiteral(emitter, info.typ, []*LlvmValue{
		toOstyValue(value{typ: "i64", ref: "1"}),
		toOstyValue(llvmZeroValue(info.okTyp)),
		errText,
	})
	emitter.body = append(emitter.body, "  br label %"+contLabel)

	emitter.body = append(emitter.body, okLabel+":")
	okResult := llvmStructLiteral(emitter, info.typ, []*LlvmValue{
		toOstyValue(value{typ: "i64", ref: "0"}),
		toOstyValue(llvmZeroValue(info.okTyp)),
		toOstyValue(llvmZeroValue(info.errTyp)),
	})
	emitter.body = append(emitter.body, "  br label %"+contLabel)

	emitter.body = append(emitter.body, contLabel+":")
	phi := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, "  "+phi+" = phi "+info.typ+" [ "+errResult.name+", %"+errLabel+" ], [ "+okResult.name+", %"+okLabel+" ]")
	g.takeOstyEmitter(emitter)
	v := value{
		typ:        info.typ,
		ref:        phi,
		sourceType: stdEnvSetCurrentDirResultSourceTypeSingleton,
	}
	v.rootPaths = g.rootPathsForType(v.typ)
	return v, true, nil
}

func (g *generator) stdEnvCallField(call *ast.CallExpr) (*ast.FieldExpr, bool) {
	if call == nil || len(g.stdEnvAliases) == 0 {
		return nil, false
	}
	field, ok := fieldExprOfCallFn(call)
	if !ok {
		return nil, false
	}
	alias, ok := field.X.(*ast.Ident)
	if !ok || !g.stdEnvAliases[alias.Name] {
		return nil, false
	}
	return field, true
}
