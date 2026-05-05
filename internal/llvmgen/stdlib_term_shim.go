package llvmgen

import "github.com/osty/osty/internal/ast"

const ostyRtTermIsTerminalSymbol = "osty_rt_term_is_terminal"
const ostyRtTermWidthSymbol = "osty_rt_term_width"
const ostyRtTermHeightSymbol = "osty_rt_term_height"
const ostyRtTermWriteSymbol = "osty_rt_term_write"
const ostyRtTermFlushSymbol = "osty_rt_term_flush"
const ostyRtTermSetRawModeSymbol = "osty_rt_term_set_raw_mode"
const ostyRtTermReadKeyStatusSymbol = "osty_rt_term_read_key_status"
const ostyRtTermPollKeyStatusSymbol = "osty_rt_term_poll_key_status"
const ostyRtTermKeyCodeSymbol = "osty_rt_term_key_code"
const ostyRtTermKeyTextSymbol = "osty_rt_term_key_text"
const ostyRtTermKeyFunctionSymbol = "osty_rt_term_key_function"
const ostyRtTermLastErrorSymbol = "osty_rt_term_last_error"

const stdTermSyntheticSizeTypeName = "__osty_std_term_Size"
const stdTermSyntheticKeyTypeName = "__osty_std_term_Key"

var stdTermSizeSourceTypeSingleton ast.Type = &ast.NamedType{
	Path: []string{stdTermSyntheticSizeTypeName},
}

var stdTermKeySourceTypeSingleton ast.Type = &ast.NamedType{
	Path: []string{stdTermSyntheticKeyTypeName},
}

var stdTermSizeResultSourceTypeSingleton ast.Type = &ast.NamedType{
	Path: []string{"Result"},
	Args: []ast.Type{
		stdTermSizeSourceTypeSingleton,
		errorSourceTypeSingleton,
	},
}

var stdTermKeyResultSourceTypeSingleton ast.Type = &ast.NamedType{
	Path: []string{"Result"},
	Args: []ast.Type{
		stdTermKeySourceTypeSingleton,
		errorSourceTypeSingleton,
	},
}

var stdTermKeyOptionSourceTypeSingleton ast.Type = &ast.OptionalType{
	Inner: stdTermKeySourceTypeSingleton,
}

var stdTermKeyOptionResultSourceTypeSingleton ast.Type = &ast.NamedType{
	Path: []string{"Result"},
	Args: []ast.Type{
		stdTermKeyOptionSourceTypeSingleton,
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

func ensureStdTermSyntheticKeyEnum(g *generator) *enumInfo {
	if g == nil {
		return nil
	}
	if info := g.enumsByName[stdTermSyntheticKeyTypeName]; info != nil {
		return info
	}
	if g.enumsByName == nil {
		g.enumsByName = map[string]*enumInfo{}
	}
	if g.enumsByType == nil {
		g.enumsByType = map[string]*enumInfo{}
	}
	info := &enumInfo{
		name:       stdTermSyntheticKeyTypeName,
		typ:        llvmStructTypeName(stdTermSyntheticKeyTypeName),
		decl:       &ast.EnumDecl{Name: stdTermSyntheticKeyTypeName},
		hasPayload: true,
		isBoxed:    true,
		variants:   map[string]variantInfo{},
	}
	addBare := func(tag int, name string) {
		info.decl.Variants = append(info.decl.Variants, &ast.Variant{Name: name})
		info.variants[name] = variantInfo{name: name, tag: tag}
	}
	info.decl.Variants = append(info.decl.Variants, &ast.Variant{Name: "Char", Fields: []ast.Type{stringSourceTypeSingleton}})
	info.variants["Char"] = variantInfo{name: "Char", tag: 0, payloads: []string{"ptr"}, payloadSourceTypes: []ast.Type{stringSourceTypeSingleton}}
	addBare(1, "Enter")
	addBare(2, "EscapeKey")
	addBare(3, "Backspace")
	addBare(4, "Tab")
	addBare(5, "BackTab")
	addBare(6, "Up")
	addBare(7, "Down")
	addBare(8, "Left")
	addBare(9, "Right")
	addBare(10, "Home")
	addBare(11, "End")
	addBare(12, "PageUp")
	addBare(13, "PageDown")
	addBare(14, "Insert")
	addBare(15, "Delete")
	info.decl.Variants = append(info.decl.Variants, &ast.Variant{Name: "Function", Fields: []ast.Type{intSourceTypeSingleton}})
	info.variants["Function"] = variantInfo{name: "Function", tag: 16, payloads: []string{"i64"}, payloadSourceTypes: []ast.Type{intSourceTypeSingleton}}
	info.decl.Variants = append(info.decl.Variants, &ast.Variant{Name: "Unknown", Fields: []ast.Type{stringSourceTypeSingleton}})
	info.variants["Unknown"] = variantInfo{name: "Unknown", tag: 17, payloads: []string{"ptr"}, payloadSourceTypes: []ast.Type{stringSourceTypeSingleton}}
	g.enums = append(g.enums, info)
	g.enumsByName[info.name] = info
	g.enumsByType[info.typ] = info
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
	case "readKey":
		return g.emitStdTermReadKeyCall(call)
	case "pollKey":
		return g.emitStdTermPollKeyCall(call)
	case "readEvent":
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
	case "readKey":
		ensureStdTermSyntheticKeyEnum(g)
		info, ok := builtinResultTypeFromAST(stdTermKeyResultSourceTypeSingleton, g.typeEnv())
		if !ok {
			return value{}, false
		}
		return value{typ: info.typ, sourceType: stdTermKeyResultSourceTypeSingleton, rootPaths: g.rootPathsForType(info.typ)}, true
	case "pollKey":
		ensureStdTermSyntheticKeyEnum(g)
		info, ok := builtinResultTypeFromAST(stdTermKeyOptionResultSourceTypeSingleton, g.typeEnv())
		if !ok {
			return value{}, false
		}
		return value{typ: info.typ, sourceType: stdTermKeyOptionResultSourceTypeSingleton, rootPaths: g.rootPathsForType(info.typ)}, true
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
	case "readKey":
		ensureStdTermSyntheticKeyEnum(g)
		return stdTermKeyResultSourceTypeSingleton, true
	case "pollKey":
		ensureStdTermSyntheticKeyEnum(g)
		return stdTermKeyOptionResultSourceTypeSingleton, true
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

func (g *generator) emitStdTermReadKeyCall(call *ast.CallExpr) (value, bool, error) {
	if len(call.Args) != 0 {
		return value{}, true, unsupportedf("call", "term.readKey takes no arguments, got %d", len(call.Args))
	}
	return g.emitStdTermKeyResultFromStatus("term.readKey", stdTermKeyResultSourceTypeSingleton, ostyRtTermReadKeyStatusSymbol, nil, nil)
}

func (g *generator) emitStdTermPollKeyCall(call *ast.CallExpr) (value, bool, error) {
	if len(call.Args) != 1 {
		return value{}, true, unsupportedf("call", "term.pollKey expects 1 argument, got %d", len(call.Args))
	}
	arg := call.Args[0]
	if arg == nil || (arg.Name != "" && arg.Name != "timeoutMillis") || arg.Value == nil {
		return value{}, true, unsupported("call", "term.pollKey requires one positional or `timeoutMillis:` Int argument")
	}
	timeout, err := g.emitExpr(arg.Value)
	if err != nil {
		return value{}, true, err
	}
	timeout, err = g.loadIfPointer(timeout)
	if err != nil {
		return value{}, true, err
	}
	if timeout.typ != "i64" {
		return value{}, true, unsupportedf("type-system", "term.pollKey arg 1 type %s, want Int", timeout.typ)
	}
	return g.emitStdTermPollKeyResultFromStatus(timeout)
}

func (g *generator) emitStdTermKeyResultFromStatus(prefix string, sourceType ast.Type, statusSymbol string, params []paramInfo, args []*LlvmValue) (value, bool, error) {
	keyInfo := ensureStdTermSyntheticKeyEnum(g)
	info, ok := builtinResultTypeFromAST(sourceType, g.typeEnv())
	if !ok {
		return value{}, true, unsupportedf("type-system", "%s Result<Key, Error> type is unavailable", prefix)
	}
	if g.resultTypes == nil {
		g.resultTypes = map[string]builtinResultType{}
	}
	g.resultTypes[info.typ] = info
	if keyInfo == nil || info.okTyp != keyInfo.typ || info.errTyp != "ptr" {
		want := "<nil>"
		if keyInfo != nil {
			want = keyInfo.typ
		}
		return value{}, true, unsupportedf("type-system", "%s currently needs Result<%s, ptr>, got ok=%s err=%s", prefix, want, info.okTyp, info.errTyp)
	}
	g.declareRuntimeSymbol(statusSymbol, "i64", params)
	g.declareRuntimeSymbol(ostyRtTermLastErrorSymbol, "ptr", nil)
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	status := llvmCall(emitter, "i64", statusSymbol, args)
	failed := llvmCompare(emitter, "ne", status, toOstyValue(value{typ: "i64", ref: "0"}))
	errLabel := llvmNextLabel(emitter, prefix+".err")
	okLabel := llvmNextLabel(emitter, prefix+".ok")
	contLabel := llvmNextLabel(emitter, prefix+".cont")
	emitter.body = append(emitter.body, "  br i1 "+failed.name+", label %"+errLabel+", label %"+okLabel)

	emitter.body = append(emitter.body, errLabel+":")
	errText := llvmCall(emitter, "ptr", ostyRtTermLastErrorSymbol, nil)
	errResult := llvmStructLiteral(emitter, info.typ, []*LlvmValue{
		toOstyValue(value{typ: "i64", ref: "1"}),
		toOstyValue(llvmZeroValue(info.okTyp)),
		errText,
	})
	emitter.body = append(emitter.body, "  br label %"+contLabel)

	emitter.body = append(emitter.body, okLabel+":")
	keyValue, err := g.emitStdTermKeyValueFromRuntime(emitter)
	if err != nil {
		g.takeOstyEmitter(emitter)
		return value{}, true, err
	}
	okResult := llvmStructLiteral(emitter, info.typ, []*LlvmValue{
		toOstyValue(value{typ: "i64", ref: "0"}),
		toOstyValue(keyValue),
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

func (g *generator) emitStdTermPollKeyResultFromStatus(timeout value) (value, bool, error) {
	keyInfo := ensureStdTermSyntheticKeyEnum(g)
	info, ok := builtinResultTypeFromAST(stdTermKeyOptionResultSourceTypeSingleton, g.typeEnv())
	if !ok {
		return value{}, true, unsupported("type-system", "term.pollKey Result<Key?, Error> type is unavailable")
	}
	if g.resultTypes == nil {
		g.resultTypes = map[string]builtinResultType{}
	}
	g.resultTypes[info.typ] = info
	if keyInfo == nil || info.okTyp != "ptr" || info.errTyp != "ptr" {
		return value{}, true, unsupportedf("type-system", "term.pollKey currently needs Result<ptr, ptr>, got ok=%s err=%s", info.okTyp, info.errTyp)
	}
	g.declareRuntimeSymbol(ostyRtTermPollKeyStatusSymbol, "i64", []paramInfo{{typ: "i64"}})
	g.declareRuntimeSymbol(ostyRtTermLastErrorSymbol, "ptr", nil)
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	status := llvmCall(emitter, "i64", ostyRtTermPollKeyStatusSymbol, []*LlvmValue{toOstyValue(timeout)})
	failed := llvmCompare(emitter, "eq", status, toOstyValue(value{typ: "i64", ref: "2"}))
	errLabel := llvmNextLabel(emitter, "term.pollKey.err")
	okLabel := llvmNextLabel(emitter, "term.pollKey.ok")
	someLabel := llvmNextLabel(emitter, "term.pollKey.some")
	noneLabel := llvmNextLabel(emitter, "term.pollKey.none")
	contLabel := llvmNextLabel(emitter, "term.pollKey.cont")
	emitter.body = append(emitter.body, "  br i1 "+failed.name+", label %"+errLabel+", label %"+okLabel)

	emitter.body = append(emitter.body, errLabel+":")
	errText := llvmCall(emitter, "ptr", ostyRtTermLastErrorSymbol, nil)
	errResult := llvmStructLiteral(emitter, info.typ, []*LlvmValue{
		toOstyValue(value{typ: "i64", ref: "1"}),
		toOstyValue(llvmZeroValue(info.okTyp)),
		errText,
	})
	emitter.body = append(emitter.body, "  br label %"+contLabel)

	emitter.body = append(emitter.body, okLabel+":")
	hasKey := llvmCompare(emitter, "eq", status, toOstyValue(value{typ: "i64", ref: "0"}))
	emitter.body = append(emitter.body, "  br i1 "+hasKey.name+", label %"+someLabel+", label %"+noneLabel)

	emitter.body = append(emitter.body, someLabel+":")
	keyValue, err := g.emitStdTermKeyValueFromRuntime(emitter)
	if err != nil {
		g.takeOstyEmitter(emitter)
		return value{}, true, err
	}
	boxedKey, err := g.emitOptionPayloadBox(emitter, keyValue, "term.pollKey.some")
	if err != nil {
		g.takeOstyEmitter(emitter)
		return value{}, true, err
	}
	someResult := llvmStructLiteral(emitter, info.typ, []*LlvmValue{
		toOstyValue(value{typ: "i64", ref: "0"}),
		toOstyValue(boxedKey),
		toOstyValue(llvmZeroValue(info.errTyp)),
	})
	emitter.body = append(emitter.body, "  br label %"+contLabel)

	emitter.body = append(emitter.body, noneLabel+":")
	noneResult := llvmStructLiteral(emitter, info.typ, []*LlvmValue{
		toOstyValue(value{typ: "i64", ref: "0"}),
		toOstyValue(llvmZeroValue(info.okTyp)),
		toOstyValue(llvmZeroValue(info.errTyp)),
	})
	emitter.body = append(emitter.body, "  br label %"+contLabel)

	emitter.body = append(emitter.body, contLabel+":")
	phi := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, "  "+phi+" = phi "+info.typ+" [ "+errResult.name+", %"+errLabel+" ], [ "+someResult.name+", %"+someLabel+" ], [ "+noneResult.name+", %"+noneLabel+" ]")
	g.takeOstyEmitter(emitter)
	v := value{typ: info.typ, ref: phi, sourceType: stdTermKeyOptionResultSourceTypeSingleton}
	v.rootPaths = g.rootPathsForType(v.typ)
	return v, true, nil
}

func (g *generator) emitStdTermKeyValueFromRuntime(emitter *LlvmEmitter) (value, error) {
	keyInfo := ensureStdTermSyntheticKeyEnum(g)
	if keyInfo == nil {
		return value{}, unsupported("type-system", "term.Key type is unavailable")
	}
	g.declareRuntimeSymbol(ostyRtTermKeyCodeSymbol, "i64", nil)
	g.declareRuntimeSymbol(ostyRtTermKeyTextSymbol, "ptr", nil)
	g.declareRuntimeSymbol(ostyRtTermKeyFunctionSymbol, "i64", nil)
	code := llvmCall(emitter, "i64", ostyRtTermKeyCodeSymbol, nil)
	text := llvmCall(emitter, "ptr", ostyRtTermKeyTextSymbol, nil)
	fn := llvmCall(emitter, "i64", ostyRtTermKeyFunctionSymbol, nil)
	charCond := llvmCompare(emitter, "eq", code, toOstyValue(value{typ: "i64", ref: "0"}))
	charLabel := llvmNextLabel(emitter, "term.key.char")
	fnCheckLabel := llvmNextLabel(emitter, "term.key.function.check")
	fnLabel := llvmNextLabel(emitter, "term.key.function")
	unknownCheckLabel := llvmNextLabel(emitter, "term.key.unknown.check")
	unknownLabel := llvmNextLabel(emitter, "term.key.unknown")
	bareLabel := llvmNextLabel(emitter, "term.key.bare")
	contLabel := llvmNextLabel(emitter, "term.key.cont")
	emitter.body = append(emitter.body, "  br i1 "+charCond.name+", label %"+charLabel+", label %"+fnCheckLabel)

	emitter.body = append(emitter.body, charLabel+":")
	charKey := llvmEnumBoxedPayloadVariant(emitter, keyInfo.typ, 0, text, "term.key.char")
	emitter.body = append(emitter.body, "  br label %"+contLabel)

	emitter.body = append(emitter.body, fnCheckLabel+":")
	fnCond := llvmCompare(emitter, "eq", code, toOstyValue(value{typ: "i64", ref: "16"}))
	emitter.body = append(emitter.body, "  br i1 "+fnCond.name+", label %"+fnLabel+", label %"+unknownCheckLabel)

	emitter.body = append(emitter.body, fnLabel+":")
	fnKey := llvmEnumBoxedPayloadVariant(emitter, keyInfo.typ, 16, fn, "term.key.function")
	emitter.body = append(emitter.body, "  br label %"+contLabel)

	emitter.body = append(emitter.body, unknownCheckLabel+":")
	unknownCond := llvmCompare(emitter, "eq", code, toOstyValue(value{typ: "i64", ref: "17"}))
	emitter.body = append(emitter.body, "  br i1 "+unknownCond.name+", label %"+unknownLabel+", label %"+bareLabel)

	emitter.body = append(emitter.body, unknownLabel+":")
	unknownKey := llvmEnumBoxedPayloadVariant(emitter, keyInfo.typ, 17, text, "term.key.unknown")
	emitter.body = append(emitter.body, "  br label %"+contLabel)

	emitter.body = append(emitter.body, bareLabel+":")
	bareKey := llvmStructLiteral(emitter, keyInfo.typ, []*LlvmValue{code, toOstyValue(llvmZeroValue("ptr"))})
	emitter.body = append(emitter.body, "  br label %"+contLabel)

	emitter.body = append(emitter.body, contLabel+":")
	phi := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, "  "+phi+" = phi "+keyInfo.typ+" [ "+charKey.name+", %"+charLabel+" ], [ "+fnKey.name+", %"+fnLabel+" ], [ "+unknownKey.name+", %"+unknownLabel+" ], [ "+bareKey.name+", %"+bareLabel+" ]")
	g.needsGCRuntime = true
	out := value{typ: keyInfo.typ, ref: phi, sourceType: stdTermKeySourceTypeSingleton}
	out.rootPaths = g.rootPathsForType(out.typ)
	return out, nil
}
