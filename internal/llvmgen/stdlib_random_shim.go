package llvmgen

import (
	"github.com/osty/osty/internal/ast"
)

const ostyRtRandomDefaultSymbol = "osty_rt_random_default"
const ostyRtRandomSeededSymbol = "osty_rt_random_seeded"
const ostyRtRandomIntSymbol = "osty_rt_random_int"
const ostyRtRandomIntInclusiveSymbol = "osty_rt_random_int_inclusive"
const ostyRtRandomFloatSymbol = "osty_rt_random_float"
const ostyRtRandomBoolSymbol = "osty_rt_random_bool"
const ostyRtRandomBytesSymbol = "osty_rt_random_bytes"
const ostyRtRandomShuffleSymbol = "osty_rt_random_shuffle"

var stdRandomRngSourceTypeSingleton ast.Type = &ast.NamedType{
	Path: []string{"Rng"},
}

var stdRandomBytesSourceTypeSingleton ast.Type = &ast.NamedType{
	Path: []string{"Bytes"},
}

var stdRandomEmptyChoiceSourceTypeSingleton ast.Type = &ast.OptionalType{
	Inner: &ast.NamedType{Path: []string{"Int"}},
}

func collectStdRandomAliases(file *ast.File) map[string]bool {
	out := map[string]bool{}
	if file == nil {
		return out
	}
	for _, use := range file.Uses {
		if use == nil || use.IsFFI() {
			continue
		}
		if len(use.Path) != 2 || use.Path[0] != "std" || use.Path[1] != "random" {
			continue
		}
		alias := use.Alias
		if alias == "" {
			alias = "random"
		}
		out[alias] = true
	}
	return out
}

func (g *generator) emitStdRandomCall(call *ast.CallExpr) (value, bool, error) {
	field, ok := g.stdRandomCallField(call)
	if !ok {
		return value{}, false, nil
	}
	switch field.Name {
	case "default":
		return g.emitStdRandomDefaultCall(call)
	case "seeded":
		return g.emitStdRandomSeededCall(call)
	default:
		return value{}, false, nil
	}
}

func (g *generator) stdRandomCallStaticResult(call *ast.CallExpr) (value, bool) {
	field, ok := g.stdRandomCallField(call)
	if !ok {
		return value{}, false
	}
	switch field.Name {
	case "default", "seeded":
		return value{
			typ:        "ptr",
			gcManaged:  true,
			sourceType: stdRandomRngSourceTypeSingleton,
		}, true
	default:
		return value{}, false
	}
}

func (g *generator) staticStdRandomCallSourceType(call *ast.CallExpr) (ast.Type, bool) {
	field, ok := g.stdRandomCallField(call)
	if !ok {
		return nil, false
	}
	switch field.Name {
	case "default", "seeded":
		return stdRandomRngSourceTypeSingleton, true
	default:
		return nil, false
	}
}

func (g *generator) emitStdRandomMethodCall(call *ast.CallExpr) (value, bool, error) {
	field, ok := fieldExprOfCallFn(call)
	if !ok || field.IsOptional {
		return value{}, false, nil
	}
	if !g.isStdRandomRngExpr(field.X) {
		return value{}, false, nil
	}
	switch field.Name {
	case "int":
		return g.emitStdRandomIntCall(call, field)
	case "intInclusive":
		return g.emitStdRandomIntInclusiveCall(call, field)
	case "float":
		return g.emitStdRandomFloatCall(call, field)
	case "bool":
		return g.emitStdRandomBoolCall(call, field)
	case "bytes":
		return g.emitStdRandomBytesCall(call, field)
	case "choice":
		return g.emitStdRandomChoiceCall(call, field)
	default:
		return value{}, false, nil
	}
}

func (g *generator) stdRandomMethodStaticResult(call *ast.CallExpr) (value, bool) {
	field, ok := fieldExprOfCallFn(call)
	if !ok || field.IsOptional || !g.isStdRandomRngExpr(field.X) {
		return value{}, false
	}
	switch field.Name {
	case "int", "intInclusive":
		return value{typ: "i64"}, true
	case "float":
		return value{typ: "double"}, true
	case "bool":
		return value{typ: "i1"}, true
	case "bytes":
		return value{
			typ:        "ptr",
			gcManaged:  true,
			sourceType: stdRandomBytesSourceTypeSingleton,
		}, true
	case "choice":
		source, ok := g.staticStdRandomChoiceSourceType(call)
		if !ok {
			return value{}, false
		}
		return value{
			typ:        "ptr",
			gcManaged:  true,
			sourceType: source,
		}, true
	default:
		return value{}, false
	}
}

func (g *generator) staticStdRandomMethodSourceType(call *ast.CallExpr) (ast.Type, bool) {
	field, ok := fieldExprOfCallFn(call)
	if !ok || field.IsOptional || !g.isStdRandomRngExpr(field.X) {
		return nil, false
	}
	switch field.Name {
	case "bytes":
		return stdRandomBytesSourceTypeSingleton, true
	case "choice":
		return g.staticStdRandomChoiceSourceType(call)
	default:
		return nil, false
	}
}

func (g *generator) emitStdRandomCallStmt(call *ast.CallExpr) (bool, error) {
	field, ok := fieldExprOfCallFn(call)
	if !ok || field.IsOptional || field.Name != "shuffle" || !g.isStdRandomRngExpr(field.X) {
		return false, nil
	}
	if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
		return true, unsupported("call", "random.Rng.shuffle requires one positional List argument")
	}
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	g.takeOstyEmitter(emitter)

	rng, err := g.emitExpr(field.X)
	if err != nil {
		return true, err
	}
	rng, err = g.loadIfPointer(rng)
	if err != nil {
		return true, err
	}
	if rng.typ != "ptr" {
		return true, unsupportedf("type-system", "random.Rng receiver type %s", rng.typ)
	}
	items, err := g.emitExpr(call.Args[0].Value)
	if err != nil {
		return true, err
	}
	items, err = g.loadIfPointer(items)
	if err != nil {
		return true, err
	}
	if items.typ != "ptr" || items.listElemTyp == "" {
		return true, unsupported("type-system", "random.Rng.shuffle requires List<T>")
	}
	g.declareRuntimeSymbol(ostyRtRandomShuffleSymbol, "void", []paramInfo{{typ: "ptr"}, {typ: "ptr"}})
	emitter = g.toOstyEmitter()
	emitter.body = append(emitter.body, mirCallRuntimeVoidOneArgText(
		ostyRtRandomShuffleSymbol,
		llvmCallArgs([]*LlvmValue{toOstyValue(rng), toOstyValue(items)}),
	))
	g.takeOstyEmitter(emitter)
	return true, nil
}

func (g *generator) stdRandomCallField(call *ast.CallExpr) (*ast.FieldExpr, bool) {
	if call == nil || len(g.stdRandomAliases) == 0 {
		return nil, false
	}
	field, ok := fieldExprOfCallFn(call)
	if !ok || field.IsOptional {
		return nil, false
	}
	alias, ok := field.X.(*ast.Ident)
	if !ok || !g.stdRandomAliases[alias.Name] {
		return nil, false
	}
	return field, true
}

func (g *generator) emitStdRandomDefaultCall(call *ast.CallExpr) (value, bool, error) {
	if len(call.Args) != 0 {
		return value{}, true, unsupportedf("call", "random.default takes no arguments, got %d", len(call.Args))
	}
	g.declareRuntimeSymbol(ostyRtRandomDefaultSymbol, "ptr", nil)
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	out := llvmCall(emitter, "ptr", ostyRtRandomDefaultSymbol, nil)
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.gcManaged = true
	v.sourceType = stdRandomRngSourceTypeSingleton
	v.rootPaths = g.rootPathsForType(v.typ)
	return v, true, nil
}

func (g *generator) emitStdRandomSeededCall(call *ast.CallExpr) (value, bool, error) {
	if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
		return value{}, true, unsupported("call", "random.seeded requires one positional Int64 seed")
	}
	seed, err := g.emitExpr(call.Args[0].Value)
	if err != nil {
		return value{}, true, err
	}
	seed, err = g.loadIfPointer(seed)
	if err != nil {
		return value{}, true, err
	}
	if seed.typ != "i64" {
		return value{}, true, unsupportedf("type-system", "random.seeded arg 1 type %s, want Int64", seed.typ)
	}
	g.declareRuntimeSymbol(ostyRtRandomSeededSymbol, "ptr", []paramInfo{{typ: "i64"}})
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	out := llvmCall(emitter, "ptr", ostyRtRandomSeededSymbol, []*LlvmValue{toOstyValue(seed)})
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.gcManaged = true
	v.sourceType = stdRandomRngSourceTypeSingleton
	v.rootPaths = g.rootPathsForType(v.typ)
	return v, true, nil
}

func (g *generator) emitStdRandomIntCall(call *ast.CallExpr, field *ast.FieldExpr) (value, bool, error) {
	if len(call.Args) != 2 || call.Args[0] == nil || call.Args[1] == nil || call.Args[0].Name != "" || call.Args[1].Name != "" || call.Args[0].Value == nil || call.Args[1].Value == nil {
		return value{}, true, unsupported("call", "random.Rng.int requires two positional Int arguments")
	}
	rng, lo, hi, err := g.emitStdRandomI64Args(field.X, call.Args[0].Value, call.Args[1].Value)
	if err != nil {
		return value{}, true, err
	}
	g.declareRuntimeSymbol(ostyRtRandomIntSymbol, "i64", []paramInfo{{typ: "ptr"}, {typ: "i64"}, {typ: "i64"}})
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	out := llvmCall(emitter, "i64", ostyRtRandomIntSymbol, []*LlvmValue{toOstyValue(rng), toOstyValue(lo), toOstyValue(hi)})
	g.takeOstyEmitter(emitter)
	return fromOstyValue(out), true, nil
}

func (g *generator) emitStdRandomIntInclusiveCall(call *ast.CallExpr, field *ast.FieldExpr) (value, bool, error) {
	if len(call.Args) != 2 || call.Args[0] == nil || call.Args[1] == nil || call.Args[0].Name != "" || call.Args[1].Name != "" || call.Args[0].Value == nil || call.Args[1].Value == nil {
		return value{}, true, unsupported("call", "random.Rng.intInclusive requires two positional Int arguments")
	}
	rng, lo, hi, err := g.emitStdRandomI64Args(field.X, call.Args[0].Value, call.Args[1].Value)
	if err != nil {
		return value{}, true, err
	}
	g.declareRuntimeSymbol(ostyRtRandomIntInclusiveSymbol, "i64", []paramInfo{{typ: "ptr"}, {typ: "i64"}, {typ: "i64"}})
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	out := llvmCall(emitter, "i64", ostyRtRandomIntInclusiveSymbol, []*LlvmValue{toOstyValue(rng), toOstyValue(lo), toOstyValue(hi)})
	g.takeOstyEmitter(emitter)
	return fromOstyValue(out), true, nil
}

func (g *generator) emitStdRandomFloatCall(call *ast.CallExpr, field *ast.FieldExpr) (value, bool, error) {
	if len(call.Args) != 0 {
		return value{}, true, unsupportedf("call", "random.Rng.float takes no arguments, got %d", len(call.Args))
	}
	rng, err := g.emitStdRandomReceiver(field.X)
	if err != nil {
		return value{}, true, err
	}
	g.declareRuntimeSymbol(ostyRtRandomFloatSymbol, "double", []paramInfo{{typ: "ptr"}})
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	out := llvmCall(emitter, "double", ostyRtRandomFloatSymbol, []*LlvmValue{toOstyValue(rng)})
	g.takeOstyEmitter(emitter)
	return fromOstyValue(out), true, nil
}

func (g *generator) emitStdRandomBoolCall(call *ast.CallExpr, field *ast.FieldExpr) (value, bool, error) {
	if len(call.Args) != 0 {
		return value{}, true, unsupportedf("call", "random.Rng.bool takes no arguments, got %d", len(call.Args))
	}
	rng, err := g.emitStdRandomReceiver(field.X)
	if err != nil {
		return value{}, true, err
	}
	g.declareRuntimeSymbol(ostyRtRandomBoolSymbol, "i1", []paramInfo{{typ: "ptr"}})
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	out := llvmCall(emitter, "i1", ostyRtRandomBoolSymbol, []*LlvmValue{toOstyValue(rng)})
	g.takeOstyEmitter(emitter)
	return fromOstyValue(out), true, nil
}

func (g *generator) emitStdRandomBytesCall(call *ast.CallExpr, field *ast.FieldExpr) (value, bool, error) {
	if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
		return value{}, true, unsupported("call", "random.Rng.bytes requires one positional Int argument")
	}
	rng, err := g.emitStdRandomReceiver(field.X)
	if err != nil {
		return value{}, true, err
	}
	n, err := g.emitExpr(call.Args[0].Value)
	if err != nil {
		return value{}, true, err
	}
	n, err = g.loadIfPointer(n)
	if err != nil {
		return value{}, true, err
	}
	if n.typ != "i64" {
		return value{}, true, unsupportedf("type-system", "random.Rng.bytes arg 1 type %s, want Int", n.typ)
	}
	g.declareRuntimeSymbol(ostyRtRandomBytesSymbol, "ptr", []paramInfo{{typ: "ptr"}, {typ: "i64"}})
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	out := llvmCall(emitter, "ptr", ostyRtRandomBytesSymbol, []*LlvmValue{toOstyValue(rng), toOstyValue(n)})
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.gcManaged = true
	v.sourceType = stdRandomBytesSourceTypeSingleton
	v.rootPaths = g.rootPathsForType(v.typ)
	return v, true, nil
}

func (g *generator) emitStdRandomChoiceCall(call *ast.CallExpr, field *ast.FieldExpr) (value, bool, error) {
	if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
		return value{}, true, unsupported("call", "random.Rng.choice requires one positional List argument")
	}
	if list, ok := call.Args[0].Value.(*ast.ListExpr); ok && len(list.Elems) == 0 {
		return value{
			typ:        "ptr",
			ref:        "null",
			gcManaged:  true,
			sourceType: stdRandomEmptyChoiceSourceTypeSingleton,
			rootPaths:  g.rootPathsForType("ptr"),
		}, true, nil
	}
	rng, err := g.emitStdRandomReceiver(field.X)
	if err != nil {
		return value{}, true, err
	}
	items, err := g.emitExpr(call.Args[0].Value)
	if err != nil {
		return value{}, true, err
	}
	items, err = g.loadIfPointer(items)
	if err != nil {
		return value{}, true, err
	}
	if items.typ != "ptr" || items.listElemTyp == "" {
		return value{}, true, unsupported("type-system", "random.Rng.choice requires List<T>")
	}
	optionSource, ok := g.staticStdRandomChoiceSourceType(call)
	if !ok {
		return value{}, true, unsupported("type-system", "random.Rng.choice requires a concrete List<T> element type")
	}
	byteSize, scalar, ok := listGetBoxByteSize(items.listElemTyp)
	if !ok {
		return value{}, true, unsupportedf("type-system", "random.Rng.choice on List<%s>: Option lowering not yet wired for this element type", items.listElemTyp)
	}

	g.declareRuntimeSymbol(listRuntimeLenSymbol(), "i64", []paramInfo{{typ: "ptr"}})
	g.declareRuntimeSymbol(ostyRtRandomIntSymbol, "i64", []paramInfo{{typ: "ptr"}, {typ: "i64"}, {typ: "i64"}})
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	lenVal := llvmCall(emitter, "i64", listRuntimeLenSymbol(), []*LlvmValue{toOstyValue(items)})
	hasItems := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirICmpSGTI64ZeroText(hasItems, lenVal.name))
	inLabel := llvmNextLabel(emitter, "random.choice.some")
	outLabel := llvmNextLabel(emitter, "random.choice.none")
	endLabel := llvmNextLabel(emitter, "random.choice.end")
	emitter.body = append(emitter.body, mirBrCondText(hasItems, inLabel, outLabel))
	emitter.body = append(emitter.body, mirLabelText(inLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(inLabel)

	emitter = g.toOstyEmitter()
	idxVal := llvmCall(emitter, "i64", ostyRtRandomIntSymbol, []*LlvmValue{
		toOstyValue(rng),
		{typ: "i64", name: "0"},
		lenVal,
	})
	g.takeOstyEmitter(emitter)
	idx := fromOstyValue(idxVal)
	got, err := g.emitListElementValue(items, idx)
	if err != nil {
		return value{}, true, err
	}
	emitter = g.toOstyEmitter()
	var someRef string
	if scalar {
		site := "random.choice.box." + items.listElemTyp
		box := llvmGcAlloc(emitter, 1, byteSize, site)
		emitter.body = append(emitter.body, mirStoreText(got.typ, got.ref, box.name))
		someRef = box.name
		g.needsGCRuntime = true
	} else {
		someRef = got.ref
	}
	inPred := g.currentBlock
	emitter.body = append(emitter.body, mirBrUncondText(endLabel))
	emitter.body = append(emitter.body, mirLabelText(outLabel))
	emitter.body = append(emitter.body, mirBrUncondText(endLabel))
	emitter.body = append(emitter.body, mirLabelText(endLabel))
	tmp := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirPhiPtrFromValueOrNullText(tmp, someRef, inPred, outLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(endLabel)
	out := value{typ: "ptr", ref: tmp, gcManaged: true, sourceType: optionSource}
	out.rootPaths = g.rootPathsForType(out.typ)
	return out, true, nil
}

func (g *generator) emitStdRandomReceiver(expr ast.Expr) (value, error) {
	rng, err := g.emitExpr(expr)
	if err != nil {
		return value{}, err
	}
	rng, err = g.loadIfPointer(rng)
	if err != nil {
		return value{}, err
	}
	if rng.typ != "ptr" {
		return value{}, unsupportedf("type-system", "random.Rng receiver type %s", rng.typ)
	}
	return rng, nil
}

func (g *generator) emitStdRandomI64Args(receiver ast.Expr, loExpr, hiExpr ast.Expr) (value, value, value, error) {
	rng, err := g.emitStdRandomReceiver(receiver)
	if err != nil {
		return value{}, value{}, value{}, err
	}
	lo, err := g.emitExpr(loExpr)
	if err != nil {
		return value{}, value{}, value{}, err
	}
	lo, err = g.loadIfPointer(lo)
	if err != nil {
		return value{}, value{}, value{}, err
	}
	if lo.typ != "i64" {
		return value{}, value{}, value{}, unsupportedf("type-system", "random bound type %s, want Int", lo.typ)
	}
	hi, err := g.emitExpr(hiExpr)
	if err != nil {
		return value{}, value{}, value{}, err
	}
	hi, err = g.loadIfPointer(hi)
	if err != nil {
		return value{}, value{}, value{}, err
	}
	if hi.typ != "i64" {
		return value{}, value{}, value{}, unsupportedf("type-system", "random bound type %s, want Int", hi.typ)
	}
	return rng, lo, hi, nil
}

func (g *generator) isStdRandomRngExpr(expr ast.Expr) bool {
	src, ok := g.staticExprSourceType(expr)
	if !ok {
		return false
	}
	named, ok := src.(*ast.NamedType)
	return ok && len(named.Path) == 1 && named.Path[0] == "Rng"
}

func (g *generator) staticStdRandomChoiceSourceType(call *ast.CallExpr) (ast.Type, bool) {
	if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Value == nil {
		return nil, false
	}
	if list, ok := call.Args[0].Value.(*ast.ListExpr); ok && len(list.Elems) == 0 {
		return stdRandomEmptyChoiceSourceTypeSingleton, true
	}
	argType, ok := g.staticExprSourceType(call.Args[0].Value)
	if !ok {
		return nil, false
	}
	list, ok := argType.(*ast.NamedType)
	if !ok || len(list.Path) != 1 || list.Path[0] != "List" || len(list.Args) != 1 {
		return nil, false
	}
	return &ast.OptionalType{Inner: list.Args[0]}, true
}
