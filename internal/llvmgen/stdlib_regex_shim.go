package llvmgen

import (
	"github.com/osty/osty/internal/ast"
)

const (
	ostyRtRegexCompileSymbol       = "osty_rt_regex_compile"
	ostyRtRegexCompileErrorSymbol  = "osty_rt_regex_compile_error"
	ostyRtRegexMatchesSymbol       = "osty_rt_regex_matches"
	ostyRtRegexCapturesSymbol      = "osty_rt_regex_captures"
	ostyRtRegexCapturesAllSymbol   = "osty_rt_regex_captures_all"
	ostyRtRegexCapturesGetSymbol   = "osty_rt_regex_captures_get"
	ostyRtRegexReplaceSymbol       = "osty_rt_regex_replace"
	ostyRtRegexReplaceAllSymbol    = "osty_rt_regex_replace_all"
	ostyRtRegexSplitSymbol         = "osty_rt_regex_split"
	ostyRtRegexFindSymbol          = "osty_rt_regex_find"
	ostyRtRegexFindAllSymbol       = "osty_rt_regex_find_all"
	ostyRtRegexMatchFreeSymbol     = "osty_rt_regex_match_free"
	ostyRtRegexCapturesNamedSymbol = "osty_rt_regex_captures_named"
)

// Synthetic struct name mirroring the user-facing `Match` shape from
// regex.osty (`{text: String, start: Int, end: Int}`). MIR allocates
// it as a value-typed aggregate; Option<Match> boxes the aggregate
// onto the GC heap and stores ptrtoint(box) into the enum payload —
// same pattern os.exec uses for Result<Output, Error>.
const stdRegexSyntheticMatchTypeName = "__osty_std_regex_Match"

// stdRegexMatchRuntimeRecordLLVMType is the inline {ptr, i64, i64}
// layout the runtime allocates via xmalloc. The MIR shim does
// getelementptr/load on the three offsets before freeing the raw
// pointer and lifting the values into the synthetic Match aggregate.
const stdRegexMatchRuntimeRecordLLVMType = "{ ptr, i64, i64 }"

var stdRegexStringListSourceTypeSingleton ast.Type = &ast.NamedType{
	Path: []string{"List"},
	Args: []ast.Type{stringSourceTypeSingleton},
}

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

var stdRegexMatchSourceTypeSingleton ast.Type = &ast.NamedType{
	Path: []string{stdRegexSyntheticMatchTypeName},
}

var stdRegexMatchListSourceTypeSingleton ast.Type = &ast.NamedType{
	Path: []string{"List"},
	Args: []ast.Type{stdRegexMatchSourceTypeSingleton},
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
	return g.emitPtrBackedResultFromRuntimeCall(
		"regex.compile",
		stdRegexCompileResultSourceTypeSingleton,
		ostyRtRegexCompileSymbol,
		ostyRtRegexCompileErrorSymbol,
		[]paramInfo{{typ: "ptr"}},
		[]*LlvmValue{toOstyValue(loaded)},
	)
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
		case "replace":
			return g.emitStdRegexReplaceCall(call, field, false)
		case "replaceAll":
			return g.emitStdRegexReplaceCall(call, field, true)
		case "split":
			return g.emitStdRegexSplitCall(call, field)
		case "findAll":
			return g.emitStdRegexFindAllCall(call, field)
		}
		return value{}, false, nil
	}
	if g.isStdRegexCapturesValueExpr(field.X) {
		switch field.Name {
		case "get":
			return g.emitStdRegexCapturesGetCall(call, field)
		case "named":
			return g.emitStdRegexCapturesNamedCall(call, field)
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
		case "replace", "replaceAll":
			return value{typ: "ptr", gcManaged: true, sourceType: stringSourceTypeSingleton}, true
		case "split":
			return value{
				typ:            "ptr",
				gcManaged:      true,
				listElemTyp:    "ptr",
				listElemString: true,
				sourceType:     stdRegexStringListSourceTypeSingleton,
			}, true
		case "findAll":
			return value{
				typ:         "ptr",
				gcManaged:   true,
				listElemTyp: llvmStructTypeName(stdRegexSyntheticMatchTypeName),
				sourceType:  stdRegexMatchListSourceTypeSingleton,
			}, true
		}
		return value{}, false
	}
	if g.isStdRegexCapturesValueExpr(field.X) {
		switch field.Name {
		case "get":
			return value{typ: "ptr", gcManaged: true, sourceType: stdRegexStringOptionSourceTypeSingleton}, true
		case "named":
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
		case "replace", "replaceAll":
			return stringSourceTypeSingleton, true
		case "split":
			return stdRegexStringListSourceTypeSingleton, true
		case "findAll":
			return stdRegexMatchListSourceTypeSingleton, true
		}
		return nil, false
	}
	if g.isStdRegexCapturesValueExpr(field.X) {
		switch field.Name {
		case "get":
			return stdRegexStringOptionSourceTypeSingleton, true
		case "named":
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

func (g *generator) emitStdRegexCapturesNamedCall(call *ast.CallExpr, field *ast.FieldExpr) (value, bool, error) {
	if len(call.Args) != 1 {
		return value{}, true, unsupportedf("call", "Captures.named expects 1 argument, got %d", len(call.Args))
	}
	arg := call.Args[0]
	if arg == nil || arg.Name != "" || arg.Value == nil {
		return value{}, true, unsupported("call", "Captures.named requires one positional String argument")
	}
	src, ok := g.staticExprSourceType(arg.Value)
	if !ok {
		return value{}, true, unsupported("type-system", "Captures.named arg 1 source type unknown, want String")
	}
	resolved, err := llvmResolveAliasType(src, g.typeEnv(), map[string]bool{})
	if err != nil || !llvmNamedTypeIsString(resolved) {
		return value{}, true, unsupported("type-system", "Captures.named arg 1 source type is not String")
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
	name, err := g.emitExpr(arg.Value)
	if err != nil {
		return value{}, true, err
	}
	name = g.protectManagedTemporary("regex.captures_named.arg", name)
	nameLoaded, err := g.loadIfPointer(name)
	if err != nil {
		return value{}, true, err
	}
	if nameLoaded.typ != "ptr" {
		return value{}, true, unsupportedf("type-system", "Captures.named arg 1 type %s, want String", nameLoaded.typ)
	}
	g.declareRuntimeSymbol(ostyRtRegexCapturesNamedSymbol, "ptr", []paramInfo{{typ: "ptr"}, {typ: "ptr"}})
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	out := llvmCall(emitter, "ptr", ostyRtRegexCapturesNamedSymbol, []*LlvmValue{toOstyValue(recv), toOstyValue(nameLoaded)})
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.gcManaged = true
	v.sourceType = stdRegexStringOptionSourceTypeSingleton
	v.rootPaths = g.rootPathsForType(v.typ)
	return v, true, nil
}

// emitStdRegexReplaceCall lowers Regex.replace / Regex.replaceAll. Both
// share the same shape — three String args (receiver, text, replacement)
// returning a single String — so the dispatch picks the runtime symbol
// via the `all` flag.
func (g *generator) emitStdRegexReplaceCall(call *ast.CallExpr, field *ast.FieldExpr, all bool) (value, bool, error) {
	method := "Regex.replace"
	symbol := ostyRtRegexReplaceSymbol
	if all {
		method = "Regex.replaceAll"
		symbol = ostyRtRegexReplaceAllSymbol
	}
	if len(call.Args) != 2 {
		return value{}, true, unsupportedf("call", "%s expects 2 arguments, got %d", method, len(call.Args))
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
	text, err := g.emitStdRegexStringArg(call.Args[0], method, 0, "regex.replace.text")
	if err != nil {
		return value{}, true, err
	}
	repl, err := g.emitStdRegexStringArg(call.Args[1], method, 1, "regex.replace.replacement")
	if err != nil {
		return value{}, true, err
	}
	g.declareRuntimeSymbol(symbol, "ptr", []paramInfo{{typ: "ptr"}, {typ: "ptr"}, {typ: "ptr"}})
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	out := llvmCall(emitter, "ptr", symbol, []*LlvmValue{toOstyValue(recv), toOstyValue(text), toOstyValue(repl)})
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.gcManaged = true
	v.sourceType = stringSourceTypeSingleton
	v.rootPaths = g.rootPathsForType(v.typ)
	return v, true, nil
}

func (g *generator) emitStdRegexSplitCall(call *ast.CallExpr, field *ast.FieldExpr) (value, bool, error) {
	if len(call.Args) != 1 {
		return value{}, true, unsupportedf("call", "Regex.split expects 1 argument, got %d", len(call.Args))
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
	text, err := g.emitStdRegexStringArg(call.Args[0], "Regex.split", 0, "regex.split.text")
	if err != nil {
		return value{}, true, err
	}
	g.declareRuntimeSymbol(ostyRtRegexSplitSymbol, "ptr", []paramInfo{{typ: "ptr"}, {typ: "ptr"}})
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	out := llvmCall(emitter, "ptr", ostyRtRegexSplitSymbol, []*LlvmValue{toOstyValue(recv), toOstyValue(text)})
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.gcManaged = true
	v.listElemTyp = "ptr"
	v.listElemString = true
	v.sourceType = stdRegexStringListSourceTypeSingleton
	v.rootPaths = g.rootPathsForType(v.typ)
	return v, true, nil
}

// emitStdRegexStringArg validates an argument is statically a String,
// emits its expression, parks managed temporaries against safepoints,
// and returns the loaded ptr value. Shared by replace/replaceAll/split.
func (g *generator) emitStdRegexStringArg(arg *ast.Arg, method string, index int, parkSite string) (value, error) {
	if arg == nil || arg.Name != "" || arg.Value == nil {
		return value{}, unsupportedf("call", "%s requires positional String arguments", method)
	}
	src, ok := g.staticExprSourceType(arg.Value)
	if !ok {
		return value{}, unsupportedf("type-system", "%s arg %d source type unknown, want String", method, index+1)
	}
	resolved, err := llvmResolveAliasType(src, g.typeEnv(), map[string]bool{})
	if err != nil || !llvmNamedTypeIsString(resolved) {
		return value{}, unsupportedf("type-system", "%s arg %d source type is not String", method, index+1)
	}
	v, err := g.emitExpr(arg.Value)
	if err != nil {
		return value{}, err
	}
	v = g.protectManagedTemporary(parkSite, v)
	loaded, err := g.loadIfPointer(v)
	if err != nil {
		return value{}, err
	}
	if loaded.typ != "ptr" {
		return value{}, unsupportedf("type-system", "%s arg %d type %s, want String", method, index+1, loaded.typ)
	}
	return loaded, nil
}

func (g *generator) emitStdRegexFindAllCall(call *ast.CallExpr, field *ast.FieldExpr) (value, bool, error) {
	if len(call.Args) != 1 {
		return value{}, true, unsupportedf("call", "Regex.findAll expects 1 argument, got %d", len(call.Args))
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
	text, err := g.emitStdRegexStringArg(call.Args[0], "Regex.findAll", 0, "regex.findAll.arg")
	if err != nil {
		return value{}, true, err
	}
	g.declareRuntimeSymbol(ostyRtRegexFindAllSymbol, "ptr", []paramInfo{{typ: "ptr"}, {typ: "ptr"}})
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	out := llvmCall(emitter, "ptr", ostyRtRegexFindAllSymbol, []*LlvmValue{toOstyValue(recv), toOstyValue(text)})
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.gcManaged = true
	v.listElemTyp = llvmStructTypeName(stdRegexSyntheticMatchTypeName)
	v.sourceType = stdRegexMatchListSourceTypeSingleton
	v.rootPaths = g.rootPathsForType(v.typ)
	return v, true, nil
}
