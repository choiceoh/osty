package llvmgen

import (
	"github.com/osty/osty/internal/ast"
)

const (
	ostyRtUuidV4Symbol         = "osty_rt_uuid_v4"
	ostyRtUuidV7Symbol         = "osty_rt_uuid_v7"
	ostyRtUuidNilSymbol        = "osty_rt_uuid_nil"
	ostyRtUuidToStringSymbol   = "osty_rt_uuid_to_string"
	ostyRtUuidToBytesSymbol    = "osty_rt_uuid_to_bytes"
	ostyRtUuidParseSymbol      = "osty_rt_uuid_parse"
	ostyRtUuidParseErrorSymbol = "osty_rt_uuid_parse_error"
)

var stdUuidUuidSourceTypeSingleton ast.Type = &ast.NamedType{
	Path: []string{"Uuid"},
}

var stdUuidBytesSourceTypeSingleton ast.Type = &ast.NamedType{
	Path: []string{"Bytes"},
}

var stdUuidParseResultSourceTypeSingleton ast.Type = &ast.NamedType{
	Path: []string{"Result"},
	Args: []ast.Type{
		stdUuidUuidSourceTypeSingleton,
		errorSourceTypeSingleton,
	},
}

func collectStdUuidAliases(file *ast.File) map[string]bool {
	out := map[string]bool{}
	if file == nil {
		return out
	}
	for _, use := range file.Uses {
		if use == nil || use.IsFFI() {
			continue
		}
		if len(use.Path) != 2 || use.Path[0] != "std" || use.Path[1] != "uuid" {
			continue
		}
		alias := use.Alias
		if alias == "" {
			alias = "uuid"
		}
		out[alias] = true
	}
	return out
}

func (g *generator) stdUuidCallField(call *ast.CallExpr) (*ast.FieldExpr, bool) {
	if call == nil || len(g.stdUuidAliases) == 0 {
		return nil, false
	}
	field, ok := fieldExprOfCallFn(call)
	if !ok || field.IsOptional {
		return nil, false
	}
	alias, ok := field.X.(*ast.Ident)
	if !ok || !g.stdUuidAliases[alias.Name] {
		return nil, false
	}
	return field, true
}

func (g *generator) emitStdUuidCall(call *ast.CallExpr) (value, bool, error) {
	field, ok := g.stdUuidCallField(call)
	if !ok {
		return value{}, false, nil
	}
	switch field.Name {
	case "v4":
		return g.emitStdUuidNoArgCall(call, "uuid.v4", ostyRtUuidV4Symbol)
	case "v7":
		return g.emitStdUuidNoArgCall(call, "uuid.v7", ostyRtUuidV7Symbol)
	case "nil":
		return g.emitStdUuidNoArgCall(call, "uuid.nil", ostyRtUuidNilSymbol)
	case "parse":
		return g.emitStdUuidParseCall(call)
	default:
		return value{}, false, nil
	}
}

func (g *generator) stdUuidCallStaticResult(call *ast.CallExpr) (value, bool) {
	field, ok := g.stdUuidCallField(call)
	if !ok {
		return value{}, false
	}
	switch field.Name {
	case "v4", "v7", "nil":
		return value{
			typ:        "ptr",
			gcManaged:  true,
			sourceType: stdUuidUuidSourceTypeSingleton,
		}, true
	case "parse":
		info, ok := builtinResultTypeFromAST(stdUuidParseResultSourceTypeSingleton, g.typeEnv())
		if !ok {
			return value{}, false
		}
		return value{typ: info.typ, sourceType: stdUuidParseResultSourceTypeSingleton}, true
	default:
		return value{}, false
	}
}

func (g *generator) staticStdUuidCallSourceType(call *ast.CallExpr) (ast.Type, bool) {
	field, ok := g.stdUuidCallField(call)
	if !ok {
		return nil, false
	}
	switch field.Name {
	case "v4", "v7", "nil":
		return stdUuidUuidSourceTypeSingleton, true
	case "parse":
		return stdUuidParseResultSourceTypeSingleton, true
	default:
		return nil, false
	}
}

func (g *generator) emitStdUuidNoArgCall(call *ast.CallExpr, opname, symbol string) (value, bool, error) {
	if len(call.Args) != 0 {
		return value{}, true, unsupportedf("call", "%s takes no arguments, got %d", opname, len(call.Args))
	}
	g.declareRuntimeSymbol(symbol, "ptr", nil)
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	out := llvmCall(emitter, "ptr", symbol, nil)
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.gcManaged = true
	v.sourceType = stdUuidUuidSourceTypeSingleton
	v.rootPaths = g.rootPathsForType(v.typ)
	return v, true, nil
}

func (g *generator) emitStdUuidParseCall(call *ast.CallExpr) (value, bool, error) {
	if len(call.Args) != 1 {
		return value{}, true, unsupportedf("call", "uuid.parse expects 1 argument, got %d", len(call.Args))
	}
	arg := call.Args[0]
	if arg == nil || arg.Name != "" || arg.Value == nil {
		return value{}, true, unsupported("call", "uuid.parse requires one positional String argument")
	}
	src, ok := g.staticExprSourceType(arg.Value)
	if !ok {
		return value{}, true, unsupported("type-system", "uuid.parse arg 1 source type unknown, want String")
	}
	resolved, err := llvmResolveAliasType(src, g.typeEnv(), map[string]bool{})
	if err != nil || !llvmNamedTypeIsString(resolved) {
		return value{}, true, unsupported("type-system", "uuid.parse arg 1 source type is not String")
	}
	text, err := g.emitExpr(arg.Value)
	if err != nil {
		return value{}, true, err
	}
	text = g.protectManagedTemporary("uuid.parse.arg", text)
	loaded, err := g.loadIfPointer(text)
	if err != nil {
		return value{}, true, err
	}
	if loaded.typ != "ptr" {
		return value{}, true, unsupportedf("type-system", "uuid.parse arg 1 type %s, want String", loaded.typ)
	}

	return g.emitPtrBackedResultFromRuntimeCall(
		"uuid.parse",
		stdUuidParseResultSourceTypeSingleton,
		ostyRtUuidParseSymbol,
		ostyRtUuidParseErrorSymbol,
		[]paramInfo{{typ: "ptr"}},
		[]*LlvmValue{toOstyValue(loaded)},
	)
}

func (g *generator) emitStdUuidMethodCall(call *ast.CallExpr) (value, bool, error) {
	field, ok := fieldExprOfCallFn(call)
	if !ok || field.IsOptional {
		return value{}, false, nil
	}
	if !g.isStdUuidValueExpr(field.X) {
		return value{}, false, nil
	}
	switch field.Name {
	case "toString":
		return g.emitStdUuidToStringCall(call, field)
	case "toBytes":
		return g.emitStdUuidToBytesCall(call, field)
	default:
		return value{}, false, nil
	}
}

func (g *generator) stdUuidMethodStaticResult(call *ast.CallExpr) (value, bool) {
	field, ok := fieldExprOfCallFn(call)
	if !ok || field.IsOptional || !g.isStdUuidValueExpr(field.X) {
		return value{}, false
	}
	switch field.Name {
	case "toString":
		return value{
			typ:        "ptr",
			gcManaged:  true,
			sourceType: stringSourceTypeSingleton,
		}, true
	case "toBytes":
		return value{
			typ:        "ptr",
			gcManaged:  true,
			sourceType: stdUuidBytesSourceTypeSingleton,
		}, true
	default:
		return value{}, false
	}
}

func (g *generator) staticStdUuidMethodSourceType(call *ast.CallExpr) (ast.Type, bool) {
	field, ok := fieldExprOfCallFn(call)
	if !ok || field.IsOptional || !g.isStdUuidValueExpr(field.X) {
		return nil, false
	}
	switch field.Name {
	case "toString":
		return stringSourceTypeSingleton, true
	case "toBytes":
		return stdUuidBytesSourceTypeSingleton, true
	default:
		return nil, false
	}
}

func (g *generator) emitStdUuidToStringCall(call *ast.CallExpr, field *ast.FieldExpr) (value, bool, error) {
	if len(call.Args) != 0 {
		return value{}, true, unsupportedf("call", "Uuid.toString takes no arguments, got %d", len(call.Args))
	}
	recv, err := g.emitStdUuidReceiver(field.X)
	if err != nil {
		return value{}, true, err
	}
	g.declareRuntimeSymbol(ostyRtUuidToStringSymbol, "ptr", []paramInfo{{typ: "ptr"}})
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	out := llvmCall(emitter, "ptr", ostyRtUuidToStringSymbol, []*LlvmValue{toOstyValue(recv)})
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.gcManaged = true
	v.sourceType = stringSourceTypeSingleton
	v.rootPaths = g.rootPathsForType(v.typ)
	return v, true, nil
}

func (g *generator) emitStdUuidToBytesCall(call *ast.CallExpr, field *ast.FieldExpr) (value, bool, error) {
	if len(call.Args) != 0 {
		return value{}, true, unsupportedf("call", "Uuid.toBytes takes no arguments, got %d", len(call.Args))
	}
	recv, err := g.emitStdUuidReceiver(field.X)
	if err != nil {
		return value{}, true, err
	}
	g.declareRuntimeSymbol(ostyRtUuidToBytesSymbol, "ptr", []paramInfo{{typ: "ptr"}})
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	out := llvmCall(emitter, "ptr", ostyRtUuidToBytesSymbol, []*LlvmValue{toOstyValue(recv)})
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.gcManaged = true
	v.sourceType = stdUuidBytesSourceTypeSingleton
	v.rootPaths = g.rootPathsForType(v.typ)
	return v, true, nil
}

func (g *generator) emitStdUuidReceiver(expr ast.Expr) (value, error) {
	recv, err := g.emitExpr(expr)
	if err != nil {
		return value{}, err
	}
	recv, err = g.loadIfPointer(recv)
	if err != nil {
		return value{}, err
	}
	if recv.typ != "ptr" {
		return value{}, unsupportedf("type-system", "Uuid receiver type %s", recv.typ)
	}
	return recv, nil
}

func (g *generator) isStdUuidValueExpr(expr ast.Expr) bool {
	src, ok := g.staticExprSourceType(expr)
	if !ok {
		return false
	}
	named, ok := src.(*ast.NamedType)
	return ok && len(named.Path) == 1 && named.Path[0] == "Uuid"
}
