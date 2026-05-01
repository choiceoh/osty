package llvmgen

import (
	"github.com/osty/osty/internal/ast"
)

const (
	ostyRtRegexCompileSymbol      = "osty_rt_regex_compile"
	ostyRtRegexCompileErrorSymbol = "osty_rt_regex_compile_error"
	ostyRtRegexMatchesSymbol      = "osty_rt_regex_matches"
	ostyRtRegexCapturesSymbol     = "osty_rt_regex_captures"
	ostyRtRegexCapturesAllSymbol  = "osty_rt_regex_captures_all"
	ostyRtRegexCapturesGetSymbol  = "osty_rt_regex_captures_get"
)

var stdRegexRegexSourceTypeSingleton ast.Type = &ast.NamedType{
	Path: []string{"Regex"},
}

var stdRegexCapturesSourceTypeSingleton ast.Type = &ast.NamedType{
	Path: []string{"Captures"},
}

var stdRegexCapturesOptionSourceTypeSingleton ast.Type = &ast.OptionalType{
	Inner: stdRegexCapturesSourceTypeSingleton,
}

var stdRegexStringOptionSourceTypeSingleton ast.Type = &ast.OptionalType{
	Inner: stringSourceTypeSingleton,
}

var stdRegexCapturesListSourceTypeSingleton ast.Type = &ast.NamedType{
	Path: []string{"List"},
	Args: []ast.Type{stdRegexCapturesSourceTypeSingleton},
}

var stdRegexCompileResultSourceTypeSingleton ast.Type = &ast.NamedType{
	Path: []string{"Result"},
	Args: []ast.Type{
		stdRegexRegexSourceTypeSingleton,
		errorSourceTypeSingleton,
	},
}

func collectStdRegexAliases(file *ast.File) map[string]bool {
	out := map[string]bool{}
	if file == nil {
		return out
	}
	for _, use := range file.Uses {
		if use == nil || use.IsFFI() {
			continue
		}
		if len(use.Path) != 2 || use.Path[0] != "std" || use.Path[1] != "regex" {
			continue
		}
		alias := use.Alias
		if alias == "" {
			alias = "regex"
		}
		out[alias] = true
	}
	return out
}

func (g *generator) stdRegexCallField(call *ast.CallExpr) (*ast.FieldExpr, bool) {
	if call == nil || len(g.stdRegexAliases) == 0 {
		return nil, false
	}
	field, ok := fieldExprOfCallFn(call)
	if !ok || field.IsOptional {
		return nil, false
	}
	alias, ok := field.X.(*ast.Ident)
	if !ok || !g.stdRegexAliases[alias.Name] {
		return nil, false
	}
	return field, true
}

func (g *generator) emitStdRegexCall(call *ast.CallExpr) (value, bool, error) {
	field, ok := g.stdRegexCallField(call)
	if !ok {
		return value{}, false, nil
	}
	switch field.Name {
	case "compile":
		return g.emitStdRegexCompileCall(call)
	default:
		return value{}, false, nil
	}
}

func (g *generator) stdRegexCallStaticResult(call *ast.CallExpr) (value, bool) {
	field, ok := g.stdRegexCallField(call)
	if !ok {
		return value{}, false
	}
	switch field.Name {
	case "compile":
		info, ok := builtinResultTypeFromAST(stdRegexCompileResultSourceTypeSingleton, g.typeEnv())
		if !ok {
			return value{}, false
		}
		return value{typ: info.typ, sourceType: stdRegexCompileResultSourceTypeSingleton}, true
	default:
		return value{}, false
	}
}

func (g *generator) staticStdRegexCallSourceType(call *ast.CallExpr) (ast.Type, bool) {
	field, ok := g.stdRegexCallField(call)
	if !ok {
		return nil, false
	}
	switch field.Name {
	case "compile":
		return stdRegexCompileResultSourceTypeSingleton, true
	default:
		return nil, false
	}
}

func (g *generator) emitStdRegexCompileCall(call *ast.CallExpr) (value, bool, error) {
	if len(call.Args) != 1 {
		return value{}, true, unsupportedf("call", "regex.compile expects 1 argument, got %d", len(call.Args))
	}
	arg := call.Args[0]
	if arg == nil || arg.Name != "" || arg.Value == nil {
		return value{}, true, unsupported("call", "regex.compile requires one positional String argument")
	}
	src, ok := g.staticExprSourceType(arg.Value)
	if !ok {
		return value{}, true, unsupported("type-system", "regex.compile arg 1 source type unknown, want String")
	}
	resolved, err := llvmResolveAliasType(src, g.typeEnv(), map[string]bool{})
	if err != nil || !llvmNamedTypeIsString(resolved) {
		return value{}, true, unsupported("type-system", "regex.compile arg 1 source type is not String")
	}
	pattern, err := g.emitExpr(arg.Value)
	if err != nil {
		return value{}, true, err
	}
	pattern = g.protectManagedTemporary("regex.compile.arg", pattern)
	loaded, err := g.loadIfPointer(pattern)
	if err != nil {
		return value{}, true, err
	}
	if loaded.typ != "ptr" {
		return value{}, true, unsupportedf("type-system", "regex.compile arg 1 type %s, want String", loaded.typ)
	}

	info, ok := builtinResultTypeFromAST(stdRegexCompileResultSourceTypeSingleton, g.typeEnv())
	if !ok {
		return value{}, true, unsupported("type-system", "regex.compile Result<Regex, Error> type unavailable")
	}
	if g.resultTypes == nil {
		g.resultTypes = map[string]builtinResultType{}
	}
	g.resultTypes[info.typ] = info
	if info.okTyp != "ptr" || info.errTyp != "ptr" {
		return value{}, true, unsupportedf("type-system", "regex.compile Result must be ptr-backed, got ok=%s err=%s", info.okTyp, info.errTyp)
	}

	g.declareRuntimeSymbol(ostyRtRegexCompileSymbol, "ptr", []paramInfo{{typ: "ptr"}})
	g.declareRuntimeSymbol(ostyRtRegexCompileErrorSymbol, "ptr", nil)
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	out := llvmCall(emitter, "ptr", ostyRtRegexCompileSymbol, []*LlvmValue{toOstyValue(loaded)})
	failed := llvmCompare(emitter, "eq", out, toOstyValue(value{typ: "ptr", ref: "null"}))
	errLabel := llvmNextLabel(emitter, "regex.compile.err")
	okLabel := llvmNextLabel(emitter, "regex.compile.ok")
	contLabel := llvmNextLabel(emitter, "regex.compile.cont")
	emitter.body = append(emitter.body, "  br i1 "+failed.name+", label %"+errLabel+", label %"+okLabel)

	emitter.body = append(emitter.body, errLabel+":")
	errText := llvmCall(emitter, "ptr", ostyRtRegexCompileErrorSymbol, nil)
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
	v := value{typ: info.typ, ref: phi, sourceType: stdRegexCompileResultSourceTypeSingleton}
	v.rootPaths = g.rootPathsForType(v.typ)
	return v, true, nil
}

func (g *generator) emitStdRegexMethodCall(call *ast.CallExpr) (value, bool, error) {
	field, ok := fieldExprOfCallFn(call)
	if !ok || field.IsOptional {
		return value{}, false, nil
	}
	if g.isStdRegexValueExpr(field.X) {
		switch field.Name {
		case "matches":
			return g.emitStdRegexMatchesCall(call, field)
		case "captures":
			return g.emitStdRegexCapturesCall(call, field)
		case "capturesAll":
			return g.emitStdRegexCapturesAllCall(call, field)
		}
		return value{}, false, nil
	}
	if g.isStdRegexCapturesValueExpr(field.X) {
		switch field.Name {
		case "get":
			return g.emitStdRegexCapturesGetCall(call, field)
		}
		return value{}, false, nil
	}
	return value{}, false, nil
}

func (g *generator) stdRegexMethodStaticResult(call *ast.CallExpr) (value, bool) {
	field, ok := fieldExprOfCallFn(call)
	if !ok || field.IsOptional {
		return value{}, false
	}
	if g.isStdRegexValueExpr(field.X) {
		switch field.Name {
		case "matches":
			return value{typ: "i1", sourceType: boolSourceTypeSingleton}, true
		case "captures":
			return value{typ: "ptr", gcManaged: true, sourceType: stdRegexCapturesOptionSourceTypeSingleton}, true
		case "capturesAll":
			return value{
				typ:         "ptr",
				gcManaged:   true,
				listElemTyp: "ptr",
				sourceType:  stdRegexCapturesListSourceTypeSingleton,
			}, true
		}
		return value{}, false
	}
	if g.isStdRegexCapturesValueExpr(field.X) {
		switch field.Name {
		case "get":
			return value{typ: "ptr", gcManaged: true, sourceType: stdRegexStringOptionSourceTypeSingleton}, true
		}
		return value{}, false
	}
	return value{}, false
}

func (g *generator) staticStdRegexMethodSourceType(call *ast.CallExpr) (ast.Type, bool) {
	field, ok := fieldExprOfCallFn(call)
	if !ok || field.IsOptional {
		return nil, false
	}
	if g.isStdRegexValueExpr(field.X) {
		switch field.Name {
		case "matches":
			return boolSourceTypeSingleton, true
		case "captures":
			return stdRegexCapturesOptionSourceTypeSingleton, true
		case "capturesAll":
			return stdRegexCapturesListSourceTypeSingleton, true
		}
		return nil, false
	}
	if g.isStdRegexCapturesValueExpr(field.X) {
		switch field.Name {
		case "get":
			return stdRegexStringOptionSourceTypeSingleton, true
		}
		return nil, false
	}
	return nil, false
}

func (g *generator) emitStdRegexMatchesCall(call *ast.CallExpr, field *ast.FieldExpr) (value, bool, error) {
	if len(call.Args) != 1 {
		return value{}, true, unsupportedf("call", "Regex.matches expects 1 argument, got %d", len(call.Args))
	}
	arg := call.Args[0]
	if arg == nil || arg.Name != "" || arg.Value == nil {
		return value{}, true, unsupported("call", "Regex.matches requires one positional String argument")
	}
	src, ok := g.staticExprSourceType(arg.Value)
	if !ok {
		return value{}, true, unsupported("type-system", "Regex.matches arg 1 source type unknown, want String")
	}
	resolved, err := llvmResolveAliasType(src, g.typeEnv(), map[string]bool{})
	if err != nil || !llvmNamedTypeIsString(resolved) {
		return value{}, true, unsupported("type-system", "Regex.matches arg 1 source type is not String")
	}
	recv, err := g.emitExpr(field.X)
	if err != nil {
		return value{}, true, err
	}
	recv, err = g.loadIfPointer(recv)
	if err != nil {
		return value{}, true, err
	}
	if recv.typ != "ptr" {
		return value{}, true, unsupportedf("type-system", "Regex receiver type %s", recv.typ)
	}
	text, err := g.emitExpr(arg.Value)
	if err != nil {
		return value{}, true, err
	}
	text = g.protectManagedTemporary("regex.matches.arg", text)
	textLoaded, err := g.loadIfPointer(text)
	if err != nil {
		return value{}, true, err
	}
	if textLoaded.typ != "ptr" {
		return value{}, true, unsupportedf("type-system", "Regex.matches arg 1 type %s, want String", textLoaded.typ)
	}
	g.declareRuntimeSymbol(ostyRtRegexMatchesSymbol, "i1", []paramInfo{{typ: "ptr"}, {typ: "ptr"}})
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	out := llvmCall(emitter, "i1", ostyRtRegexMatchesSymbol, []*LlvmValue{toOstyValue(recv), toOstyValue(textLoaded)})
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.sourceType = boolSourceTypeSingleton
	return v, true, nil
}

func (g *generator) isStdRegexValueExpr(expr ast.Expr) bool {
	src, ok := g.staticExprSourceType(expr)
	if !ok {
		return false
	}
	named, ok := src.(*ast.NamedType)
	return ok && len(named.Path) == 1 && named.Path[0] == "Regex"
}

func (g *generator) isStdRegexCapturesValueExpr(expr ast.Expr) bool {
	src, ok := g.staticExprSourceType(expr)
	if !ok {
		return false
	}
	named, ok := src.(*ast.NamedType)
	return ok && len(named.Path) == 1 && named.Path[0] == "Captures"
}

func (g *generator) emitStdRegexCapturesCall(call *ast.CallExpr, field *ast.FieldExpr) (value, bool, error) {
	if len(call.Args) != 1 {
		return value{}, true, unsupportedf("call", "Regex.captures expects 1 argument, got %d", len(call.Args))
	}
	arg := call.Args[0]
	if arg == nil || arg.Name != "" || arg.Value == nil {
		return value{}, true, unsupported("call", "Regex.captures requires one positional String argument")
	}
	src, ok := g.staticExprSourceType(arg.Value)
	if !ok {
		return value{}, true, unsupported("type-system", "Regex.captures arg 1 source type unknown, want String")
	}
	resolved, err := llvmResolveAliasType(src, g.typeEnv(), map[string]bool{})
	if err != nil || !llvmNamedTypeIsString(resolved) {
		return value{}, true, unsupported("type-system", "Regex.captures arg 1 source type is not String")
	}
	recv, err := g.emitExpr(field.X)
	if err != nil {
		return value{}, true, err
	}
	recv, err = g.loadIfPointer(recv)
	if err != nil {
		return value{}, true, err
	}
	if recv.typ != "ptr" {
		return value{}, true, unsupportedf("type-system", "Regex receiver type %s", recv.typ)
	}
	text, err := g.emitExpr(arg.Value)
	if err != nil {
		return value{}, true, err
	}
	text = g.protectManagedTemporary("regex.captures.arg", text)
	textLoaded, err := g.loadIfPointer(text)
	if err != nil {
		return value{}, true, err
	}
	if textLoaded.typ != "ptr" {
		return value{}, true, unsupportedf("type-system", "Regex.captures arg 1 type %s, want String", textLoaded.typ)
	}
	g.declareRuntimeSymbol(ostyRtRegexCapturesSymbol, "ptr", []paramInfo{{typ: "ptr"}, {typ: "ptr"}})
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	out := llvmCall(emitter, "ptr", ostyRtRegexCapturesSymbol, []*LlvmValue{toOstyValue(recv), toOstyValue(textLoaded)})
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.gcManaged = true
	v.sourceType = stdRegexCapturesOptionSourceTypeSingleton
	v.rootPaths = g.rootPathsForType(v.typ)
	return v, true, nil
}

func (g *generator) emitStdRegexCapturesAllCall(call *ast.CallExpr, field *ast.FieldExpr) (value, bool, error) {
	if len(call.Args) != 1 {
		return value{}, true, unsupportedf("call", "Regex.capturesAll expects 1 argument, got %d", len(call.Args))
	}
	arg := call.Args[0]
	if arg == nil || arg.Name != "" || arg.Value == nil {
		return value{}, true, unsupported("call", "Regex.capturesAll requires one positional String argument")
	}
	src, ok := g.staticExprSourceType(arg.Value)
	if !ok {
		return value{}, true, unsupported("type-system", "Regex.capturesAll arg 1 source type unknown, want String")
	}
	resolved, err := llvmResolveAliasType(src, g.typeEnv(), map[string]bool{})
	if err != nil || !llvmNamedTypeIsString(resolved) {
		return value{}, true, unsupported("type-system", "Regex.capturesAll arg 1 source type is not String")
	}
	recv, err := g.emitExpr(field.X)
	if err != nil {
		return value{}, true, err
	}
	recv, err = g.loadIfPointer(recv)
	if err != nil {
		return value{}, true, err
	}
	if recv.typ != "ptr" {
		return value{}, true, unsupportedf("type-system", "Regex receiver type %s", recv.typ)
	}
	text, err := g.emitExpr(arg.Value)
	if err != nil {
		return value{}, true, err
	}
	text = g.protectManagedTemporary("regex.capturesAll.arg", text)
	textLoaded, err := g.loadIfPointer(text)
	if err != nil {
		return value{}, true, err
	}
	if textLoaded.typ != "ptr" {
		return value{}, true, unsupportedf("type-system", "Regex.capturesAll arg 1 type %s, want String", textLoaded.typ)
	}
	g.declareRuntimeSymbol(ostyRtRegexCapturesAllSymbol, "ptr", []paramInfo{{typ: "ptr"}, {typ: "ptr"}})
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	out := llvmCall(emitter, "ptr", ostyRtRegexCapturesAllSymbol, []*LlvmValue{toOstyValue(recv), toOstyValue(textLoaded)})
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.gcManaged = true
	v.listElemTyp = "ptr"
	v.sourceType = stdRegexCapturesListSourceTypeSingleton
	v.rootPaths = g.rootPathsForType(v.typ)
	return v, true, nil
}

func (g *generator) emitStdRegexCapturesGetCall(call *ast.CallExpr, field *ast.FieldExpr) (value, bool, error) {
	if len(call.Args) != 1 {
		return value{}, true, unsupportedf("call", "Captures.get expects 1 argument, got %d", len(call.Args))
	}
	arg := call.Args[0]
	if arg == nil || arg.Name != "" || arg.Value == nil {
		return value{}, true, unsupported("call", "Captures.get requires one positional Int argument")
	}
	recv, err := g.emitExpr(field.X)
	if err != nil {
		return value{}, true, err
	}
	recv, err = g.loadIfPointer(recv)
	if err != nil {
		return value{}, true, err
	}
	if recv.typ != "ptr" {
		return value{}, true, unsupportedf("type-system", "Captures receiver type %s", recv.typ)
	}
	idx, err := g.emitExpr(arg.Value)
	if err != nil {
		return value{}, true, err
	}
	idx, err = g.loadIfPointer(idx)
	if err != nil {
		return value{}, true, err
	}
	if idx.typ != "i64" {
		return value{}, true, unsupportedf("type-system", "Captures.get arg 1 type %s, want Int", idx.typ)
	}
	g.declareRuntimeSymbol(ostyRtRegexCapturesGetSymbol, "ptr", []paramInfo{{typ: "ptr"}, {typ: "i64"}})
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	out := llvmCall(emitter, "ptr", ostyRtRegexCapturesGetSymbol, []*LlvmValue{toOstyValue(recv), toOstyValue(idx)})
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.gcManaged = true
	v.sourceType = stdRegexStringOptionSourceTypeSingleton
	v.rootPaths = g.rootPathsForType(v.typ)
	return v, true, nil
}
