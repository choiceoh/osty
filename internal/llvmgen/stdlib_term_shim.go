package llvmgen

import "github.com/osty/osty/internal/ast"

const ostyRtTermIsTerminalSymbol = "osty_rt_term_is_terminal"
const ostyRtTermWidthSymbol = "osty_rt_term_width"
const ostyRtTermHeightSymbol = "osty_rt_term_height"
const ostyRtTermWriteSymbol = "osty_rt_term_write"
const ostyRtTermFlushSymbol = "osty_rt_term_flush"
const ostyRtTermSetRawModeSymbol = "osty_rt_term_set_raw_mode"

const stdTermSyntheticSizeTypeName = "__osty_std_term_Size"

var stdTermSizeSourceTypeSingleton ast.Type = &ast.NamedType{
	Path: []string{stdTermSyntheticSizeTypeName},
}

var stdTermSizeResultSourceTypeSingleton ast.Type = &ast.NamedType{
	Path: []string{"Result"},
	Args: []ast.Type{
		stdTermSizeSourceTypeSingleton,
		errorSourceTypeSingleton,
	},
}

var stdTermUnitErrorResultSourceTypeSingleton ast.Type = &ast.NamedType{
	Path: []string{"Result"},
	Args: []ast.Type{
		unitTupleSourceTypeSingleton,
		errorSourceTypeSingleton,
	},
}

func collectStdTermAliases(file *ast.File) map[string]bool {
	out := map[string]bool{}
	if file == nil {
		return out
	}
	for _, use := range file.Uses {
		if use == nil || use.IsFFI() {
			continue
		}
		if len(use.Path) != 2 || use.Path[0] != "std" || use.Path[1] != "term" {
			continue
		}
		alias := use.Alias
		if alias == "" {
			alias = "term"
		}
		out[alias] = true
	}
	return out
}

func ensureStdTermSyntheticSizeStruct(g *generator) *structInfo {
	if g == nil {
		return nil
	}
	if info := g.structsByName[stdTermSyntheticSizeTypeName]; info != nil {
		return info
	}
	if g.structsByName == nil {
		g.structsByName = map[string]*structInfo{}
	}
	if g.structsByType == nil {
		g.structsByType = map[string]*structInfo{}
	}
	info := &structInfo{
		name:   stdTermSyntheticSizeTypeName,
		typ:    llvmStructTypeName(stdTermSyntheticSizeTypeName),
		byName: map[string]fieldInfo{},
	}
	fields := []fieldInfo{
		{
			name:       "width",
			typ:        "i64",
			index:      0,
			sourceType: &ast.NamedType{Path: []string{"Int"}},
		},
		{
			name:       "height",
			typ:        "i64",
			index:      1,
			sourceType: &ast.NamedType{Path: []string{"Int"}},
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

func (g *generator) emitStdTermCall(call *ast.CallExpr) (value, bool, error) {
	field, ok := g.stdTermCallField(call)
	if !ok {
		return value{}, false, nil
	}
	switch field.Name {
	case "isTerminal":
		return g.emitStdTermIsTerminalCall(call)
	case "size":
		return g.emitStdTermSizeCall(call)
	case "write":
		return g.emitStdTermWriteCall(call)
	case "flush":
		return g.emitStdTermFlushCall(call)
	case "setRawMode":
		return g.emitStdTermSetRawModeCall(call)
	case "readKey", "pollKey", "readEvent":
		return value{}, true, unsupportedf("call", "std.term.%s is not supported by LLVM yet", field.Name)
	default:
		return value{}, false, nil
	}
}

func (g *generator) stdTermCallStaticResult(call *ast.CallExpr) (value, bool) {
	field, ok := g.stdTermCallField(call)
	if !ok {
		return value{}, false
	}
	switch field.Name {
	case "isTerminal":
		return value{typ: "i1", sourceType: &ast.NamedType{Path: []string{"Bool"}}}, true
	case "size":
		ensureStdTermSyntheticSizeStruct(g)
		info, ok := builtinResultTypeFromAST(stdTermSizeResultSourceTypeSingleton, g.typeEnv())
		if !ok {
			return value{}, false
		}
		return value{
			typ:        info.typ,
			sourceType: stdTermSizeResultSourceTypeSingleton,
			rootPaths:  g.rootPathsForType(info.typ),
		}, true
	case "write", "flush", "setRawMode":
		info, ok := builtinResultTypeFromAST(stdTermUnitErrorResultSourceTypeSingleton, g.typeEnv())
		if !ok {
			return value{}, false
		}
		return value{
			typ:        info.typ,
			sourceType: stdTermUnitErrorResultSourceTypeSingleton,
			rootPaths:  g.rootPathsForType(info.typ),
		}, true
	default:
		return value{}, false
	}
}

func (g *generator) staticStdTermCallSourceType(call *ast.CallExpr) (ast.Type, bool) {
	field, ok := g.stdTermCallField(call)
	if !ok {
		return nil, false
	}
	switch field.Name {
	case "isTerminal":
		return &ast.NamedType{Path: []string{"Bool"}}, true
	case "size":
		ensureStdTermSyntheticSizeStruct(g)
		return stdTermSizeResultSourceTypeSingleton, true
	case "write", "flush", "setRawMode":
		return stdTermUnitErrorResultSourceTypeSingleton, true
	default:
		return nil, false
	}
}

func (g *generator) stdTermCallField(call *ast.CallExpr) (*ast.FieldExpr, bool) {
	if call == nil || len(g.stdTermAliases) == 0 {
		return nil, false
	}
	field, ok := fieldExprOfCallFn(call)
	if !ok || field.IsOptional {
		return nil, false
	}
	alias, ok := field.X.(*ast.Ident)
	if !ok || alias == nil || !g.stdTermAliases[alias.Name] {
		return nil, false
	}
	return field, true
}

func (g *generator) emitStdTermIsTerminalCall(call *ast.CallExpr) (value, bool, error) {
	if len(call.Args) != 0 {
		return value{}, true, unsupportedf("call", "term.isTerminal takes no arguments, got %d", len(call.Args))
	}
	g.declareRuntimeSymbol(ostyRtTermIsTerminalSymbol, "i1", nil)
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	out := llvmCall(emitter, "i1", ostyRtTermIsTerminalSymbol, nil)
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.sourceType = &ast.NamedType{Path: []string{"Bool"}}
	return v, true, nil
}

func (g *generator) emitStdTermSizeCall(call *ast.CallExpr) (value, bool, error) {
	if len(call.Args) != 0 {
		return value{}, true, unsupportedf("call", "term.size takes no arguments, got %d", len(call.Args))
	}
	sizeInfo := ensureStdTermSyntheticSizeStruct(g)
	info, ok := builtinResultTypeFromAST(stdTermSizeResultSourceTypeSingleton, g.typeEnv())
	if !ok {
		return value{}, true, unsupported("type-system", "term.size Result<Size, Error> type is unavailable")
	}
	if g.resultTypes == nil {
		g.resultTypes = map[string]builtinResultType{}
	}
	g.resultTypes[info.typ] = info
	if info.okTyp != sizeInfo.typ || info.errTyp != "ptr" {
		return value{}, true, unsupportedf("type-system", "term.size currently needs Result<%s, ptr>, got ok=%s err=%s", sizeInfo.typ, info.okTyp, info.errTyp)
	}
	g.declareRuntimeSymbol(ostyRtTermWidthSymbol, "i64", nil)
	g.declareRuntimeSymbol(ostyRtTermHeightSymbol, "i64", nil)
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	width := llvmCall(emitter, "i64", ostyRtTermWidthSymbol, nil)
	height := llvmCall(emitter, "i64", ostyRtTermHeightSymbol, nil)
	sizeValue := llvmStructLiteral(emitter, sizeInfo.typ, []*LlvmValue{width, height})
	result := llvmStructLiteral(emitter, info.typ, []*LlvmValue{
		toOstyValue(value{typ: "i64", ref: "0"}),
		sizeValue,
		toOstyValue(llvmZeroValue(info.errTyp)),
	})
	g.takeOstyEmitter(emitter)
	v := value{
		typ:        info.typ,
		ref:        result.name,
		sourceType: stdTermSizeResultSourceTypeSingleton,
	}
	v.rootPaths = g.rootPathsForType(v.typ)
	return v, true, nil
}

func (g *generator) emitStdTermWriteCall(call *ast.CallExpr) (value, bool, error) {
	if len(call.Args) != 1 {
		return value{}, true, unsupportedf("call", "term.write expects 1 argument, got %d", len(call.Args))
	}
	arg := call.Args[0]
	if arg == nil || (arg.Name != "" && arg.Name != "text") || arg.Value == nil {
		return value{}, true, unsupported("call", "term.write requires one positional or `text:` String argument")
	}
	text, err := g.emitExpr(arg.Value)
	if err != nil {
		return value{}, true, err
	}
	text = g.protectManagedTemporary("term.write.text", text)
	text, err = g.loadIfPointer(text)
	if err != nil {
		return value{}, true, err
	}
	if text.typ != "ptr" {
		return value{}, true, unsupportedf("type-system", "term.write arg 1 type %s, want String", text.typ)
	}
	return g.emitStdEnvUnitErrorResultFromRuntimeCall(
		"term.write",
		stdTermUnitErrorResultSourceTypeSingleton,
		ostyRtTermWriteSymbol,
		[]paramInfo{{typ: "ptr"}},
		[]*LlvmValue{toOstyValue(text)},
	)
}

func (g *generator) emitStdTermFlushCall(call *ast.CallExpr) (value, bool, error) {
	if len(call.Args) != 0 {
		return value{}, true, unsupportedf("call", "term.flush takes no arguments, got %d", len(call.Args))
	}
	return g.emitStdEnvUnitErrorResultFromRuntimeCall(
		"term.flush",
		stdTermUnitErrorResultSourceTypeSingleton,
		ostyRtTermFlushSymbol,
		nil,
		nil,
	)
}

func (g *generator) emitStdTermSetRawModeCall(call *ast.CallExpr) (value, bool, error) {
	if len(call.Args) != 1 {
		return value{}, true, unsupportedf("call", "term.setRawMode expects 1 argument, got %d", len(call.Args))
	}
	arg := call.Args[0]
	if arg == nil || (arg.Name != "" && arg.Name != "enabled") || arg.Value == nil {
		return value{}, true, unsupported("call", "term.setRawMode requires one positional or `enabled:` Bool argument")
	}
	enabled, err := g.emitExpr(arg.Value)
	if err != nil {
		return value{}, true, err
	}
	enabled, err = g.loadIfPointer(enabled)
	if err != nil {
		return value{}, true, err
	}
	if enabled.typ != "i1" {
		return value{}, true, unsupportedf("type-system", "term.setRawMode arg 1 type %s, want Bool", enabled.typ)
	}
	return g.emitStdEnvUnitErrorResultFromRuntimeCall(
		"term.setRawMode",
		stdTermUnitErrorResultSourceTypeSingleton,
		ostyRtTermSetRawModeSymbol,
		[]paramInfo{{typ: "i1"}},
		[]*LlvmValue{toOstyValue(enabled)},
	)
}
