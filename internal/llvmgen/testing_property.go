package llvmgen

import (
	"github.com/osty/osty/internal/ast"
)

const (
	ostyRtTestGenIntSymbol         = "osty_rt_test_gen_int"
	ostyRtTestGenIntRangeSymbol    = "osty_rt_test_gen_int_range"
	ostyRtTestGenFloatSymbol       = "osty_rt_test_gen_float"
	ostyRtTestGenAsciiStringSymbol = "osty_rt_test_gen_ascii_string"
)

func (g *generator) emitTestingProperty(call *ast.CallExpr, method string) error {
	nameExpr, genExpr, predExpr, iterations, seedBase, err := g.parseTestingPropertyCall(call, method)
	if err != nil {
		return err
	}
	nameValue, err := g.emitTestingStringArg(nameExpr, "testing."+method+" name")
	if err != nil {
		return err
	}
	nameSlot := g.nextHiddenLocalName("test.property.name")
	g.bindNamedLocal(nameSlot, nameValue, false)

	emitter := g.toOstyEmitter()
	loop := llvmRangeStart(emitter, g.nextHiddenLocalName("test.property.iter"), llvmIntLiteral(0), toOstyValue(iterations), false)
	g.takeOstyEmitter(emitter)
	g.enterBlock(loop.bodyLabel)
	continueLabel := g.nextNamedLabel("test.property.cont")
	scopeDepth := len(g.locals)
	g.pushScope()

	iterSeed := value{typ: "i64", ref: loop.current}
	if seedBase.ref != "" {
		iterSeed, err = g.emitTestingPropertyAddI64(seedBase, iterSeed)
		if err != nil {
			if len(g.locals) > scopeDepth {
				g.popScope()
			}
			return err
		}
	}
	sample, err := g.emitTestingPropertySample(genExpr, iterSeed)
	if err != nil {
		if len(g.locals) > scopeDepth {
			g.popScope()
		}
		return err
	}
	pred, err := g.emitTestingPropertyPredicate(predExpr, sample)
	if err != nil {
		if len(g.locals) > scopeDepth {
			g.popScope()
		}
		return err
	}
	if pred.typ != "i1" {
		if len(g.locals) > scopeDepth {
			g.popScope()
		}
		return unsupportedf("type-system", "testing.%s predicate type %s, want Bool", method, pred.typ)
	}
	if err := g.emitTestingAssertionLazy(pred, func() (value, error) {
		name, err := g.emitIdent(nameSlot)
		if err != nil {
			return value{}, err
		}
		iterText, err := g.emitRuntimeIntToString(value{typ: "i64", ref: loop.current})
		if err != nil {
			return value{}, err
		}
		return g.foldAssertionMessage(
			staticAssertPart(g.testingFailureMessage(call, method)),
			staticAssertPart(": name="),
			dynamicAssertPart(name),
			staticAssertPart(" sample="),
			dynamicAssertPart(iterText),
		)
	}); err != nil {
		if len(g.locals) > scopeDepth {
			g.popScope()
		}
		return err
	}
	if len(g.locals) > scopeDepth {
		g.popScope()
	}
	if g.currentReachable {
		g.branchTo(continueLabel)
	}
	emitter = g.toOstyEmitter()
	emitter.body = append(emitter.body, mirLabelText(continueLabel))
	g.emitGCSafepointKind(emitter, safepointKindLoop)
	llvmRangeEnd(emitter, loop)
	g.takeOstyEmitter(emitter)
	g.enterBlock(loop.endLabel)
	return nil
}

func (g *generator) emitTestingPropertyPredicate(predExpr ast.Expr, sample value) (value, error) {
	return g.emitTestingPropertyApplyUnary(predExpr, sample, "testing.property predicate")
}

func (g *generator) parseTestingPropertyCall(call *ast.CallExpr, method string) (name ast.Expr, gen ast.Expr, pred ast.Expr, iterations value, seed value, err error) {
	const defaultIterations = "100"
	args := call.Args
	iterations = value{typ: "i64", ref: defaultIterations}
	for _, arg := range args {
		if arg == nil || arg.Name != "" || arg.Value == nil {
			return nil, nil, nil, value{}, value{}, unsupportedf("call", "testing.%s requires positional arguments", method)
		}
	}
	switch method {
	case "property":
		if len(args) != 3 {
			return nil, nil, nil, value{}, value{}, unsupported("call", "testing.property requires (name, generator, predicate)")
		}
		return args[0].Value, args[1].Value, args[2].Value, iterations, value{}, nil
	case "propertyN":
		if len(args) != 4 {
			return nil, nil, nil, value{}, value{}, unsupported("call", "testing.propertyN requires (name, generator, iterations, predicate)")
		}
		iterations, err = g.emitExpr(args[2].Value)
		if err != nil {
			return nil, nil, nil, value{}, value{}, err
		}
		if iterations.typ != "i64" {
			return nil, nil, nil, value{}, value{}, unsupportedf("type-system", "testing.propertyN iterations type %s, want i64", iterations.typ)
		}
		return args[0].Value, args[1].Value, args[3].Value, iterations, value{}, nil
	case "propertySeeded":
		if len(args) != 4 {
			return nil, nil, nil, value{}, value{}, unsupported("call", "testing.propertySeeded requires (name, generator, seed, predicate)")
		}
		seed, err = g.emitExpr(args[2].Value)
		if err != nil {
			return nil, nil, nil, value{}, value{}, err
		}
		if seed.typ != "i64" {
			return nil, nil, nil, value{}, value{}, unsupportedf("type-system", "testing.propertySeeded seed type %s, want i64", seed.typ)
		}
		return args[0].Value, args[1].Value, args[3].Value, iterations, seed, nil
	default:
		return nil, nil, nil, value{}, value{}, unsupportedf("call", "testing.%s is not a property helper", method)
	}
}

func (g *generator) bindTestingPropertyParam(param *ast.Param, sample value) error {
	if param == nil {
		return unsupported("call", "testing.property requires a predicate parameter")
	}
	if param.Pattern != nil {
		return g.bindLetPattern(param.Pattern, sample, false)
	}
	if param.Name == "" {
		return unsupported("call", "testing.property predicate parameter name is empty")
	}
	g.bindNamedLocal(param.Name, sample, false)
	return nil
}

func (g *generator) testingGenCallMethod(call *ast.CallExpr) (string, bool) {
	field, ok := fieldExprOfCallFn(call)
	if !ok || field.IsOptional {
		return "", false
	}
	alias, ok := field.X.(*ast.Ident)
	if !ok || alias == nil || !g.stdTestingGenAliases[alias.Name] {
		return "", false
	}
	return field.Name, true
}

func (g *generator) emitTestingPropertySample(expr ast.Expr, seed value) (value, error) {
	switch e := expr.(type) {
	case *ast.ParenExpr:
		if e == nil || e.X == nil {
			return value{}, unsupported("call", "testing.property generator contains an empty parenthesized expression")
		}
		return g.emitTestingPropertySample(e.X, seed)
	case *ast.CallExpr:
		method, ok := g.testingGenCallMethod(e)
		if !ok {
			return value{}, unsupported("call", "testing.property currently supports std.testing.gen calls only")
		}
		switch method {
		case "int":
			return g.emitTestingPropertyInt(seed)
		case "intRange":
			return g.emitTestingPropertyIntRangeCall(e, seed)
		case "bool":
			return g.emitTestingPropertyBool(seed)
		case "float":
			return g.emitTestingPropertyFloat(seed)
		case "char":
			return g.emitTestingPropertyChar(seed)
		case "byte":
			return g.emitTestingPropertyByte(seed)
		case "asciiString":
			return g.emitTestingPropertyAsciiStringCall(e, seed)
		case "pair":
			return g.emitTestingPropertyPairLike(e, seed, 2)
		case "triple":
			return g.emitTestingPropertyPairLike(e, seed, 3)
		case "list":
			return g.emitTestingPropertyListCall(e, seed, false)
		case "listOfSize":
			return g.emitTestingPropertyListCall(e, seed, true)
		case "option":
			return g.emitTestingPropertyOptionCall(e, seed)
		case "result":
			return g.emitTestingPropertyResultCall(e, seed)
		case "map":
			return g.emitTestingPropertyMapCall(e, seed)
		case "filter":
			return g.emitTestingPropertyFilterCall(e, seed)
		case "oneOf":
			return g.emitTestingPropertyOneOfCall(e, seed)
		case "oneOfGens":
			return g.emitTestingPropertyOneOfGensCall(e, seed)
		case "constant":
			if len(e.Args) != 1 || e.Args[0] == nil || e.Args[0].Name != "" || e.Args[0].Value == nil {
				return value{}, unsupported("call", "testing.gen.constant requires one positional argument")
			}
			return g.emitExpr(e.Args[0].Value)
		default:
			return value{}, unsupportedf("call", "testing.property generator gen.%s is not supported by LLVM yet", method)
		}
	default:
		return value{}, unsupported("call", "testing.property generator must be a std.testing.gen expression")
	}
}

func (g *generator) emitTestingPropertyInt(seed value) (value, error) {
	if seed.typ != "i64" {
		return value{}, unsupportedf("type-system", "testing property seed type %s, want i64", seed.typ)
	}
	g.declareRuntimeSymbol(ostyRtTestGenIntSymbol, "i64", []paramInfo{{typ: "i64"}})
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, "i64", ostyRtTestGenIntSymbol, []*LlvmValue{toOstyValue(seed)})
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.sourceType = testingPropertyNamedSourceType("Int")
	return v, nil
}

func (g *generator) emitTestingPropertyIntRangeCall(call *ast.CallExpr, seed value) (value, error) {
	if len(call.Args) != 2 || call.Args[0] == nil || call.Args[1] == nil ||
		call.Args[0].Name != "" || call.Args[1].Name != "" ||
		call.Args[0].Value == nil || call.Args[1].Value == nil {
		return value{}, unsupported("call", "testing.gen.intRange requires (lo, hi)")
	}
	lo, err := g.emitExpr(call.Args[0].Value)
	if err != nil {
		return value{}, err
	}
	hi, err := g.emitExpr(call.Args[1].Value)
	if err != nil {
		return value{}, err
	}
	if lo.typ != "i64" || hi.typ != "i64" {
		return value{}, unsupportedf("type-system", "testing.gen.intRange expects Int bounds, got %s/%s", lo.typ, hi.typ)
	}
	return g.emitTestingPropertyIntRange(lo, hi, seed)
}

func (g *generator) emitTestingPropertyIntRange(lo, hi, seed value) (value, error) {
	if seed.typ != "i64" {
		return value{}, unsupportedf("type-system", "testing property seed type %s, want i64", seed.typ)
	}
	g.declareRuntimeSymbol(ostyRtTestGenIntRangeSymbol, "i64", []paramInfo{{typ: "i64"}, {typ: "i64"}, {typ: "i64"}})
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, "i64", ostyRtTestGenIntRangeSymbol, []*LlvmValue{
		toOstyValue(lo),
		toOstyValue(hi),
		toOstyValue(seed),
	})
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.sourceType = testingPropertyNamedSourceType("Int")
	return v, nil
}

func (g *generator) emitTestingPropertyBool(seed value) (value, error) {
	n, err := g.emitTestingPropertyIntRange(
		value{typ: "i64", ref: "0"},
		value{typ: "i64", ref: "2"},
		seed,
	)
	if err != nil {
		return value{}, err
	}
	emitter := g.toOstyEmitter()
	out := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirICmpNEI64ZeroText(out, n.ref))
	g.takeOstyEmitter(emitter)
	return value{typ: "i1", ref: out, sourceType: testingPropertyNamedSourceType("Bool")}, nil
}

func (g *generator) emitTestingPropertyFloat(seed value) (value, error) {
	if seed.typ != "i64" {
		return value{}, unsupportedf("type-system", "testing property seed type %s, want i64", seed.typ)
	}
	g.declareRuntimeSymbol(ostyRtTestGenFloatSymbol, "double", []paramInfo{{typ: "i64"}})
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, "double", ostyRtTestGenFloatSymbol, []*LlvmValue{toOstyValue(seed)})
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.sourceType = testingPropertyNamedSourceType("Float64")
	return v, nil
}

func (g *generator) emitTestingPropertyChar(seed value) (value, error) {
	n, err := g.emitTestingPropertyIntRange(
		value{typ: "i64", ref: "32"},
		value{typ: "i64", ref: "127"},
		seed,
	)
	if err != nil {
		return value{}, err
	}
	emitter := g.toOstyEmitter()
	out := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirTruncI64ToI32Text(out, n.ref))
	g.takeOstyEmitter(emitter)
	return value{typ: "i32", ref: out, sourceType: testingPropertyNamedSourceType("Char")}, nil
}

func (g *generator) emitTestingPropertyByte(seed value) (value, error) {
	n, err := g.emitTestingPropertyIntRange(
		value{typ: "i64", ref: "0"},
		value{typ: "i64", ref: "256"},
		seed,
	)
	if err != nil {
		return value{}, err
	}
	emitter := g.toOstyEmitter()
	out := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirTruncI64ToI8Text(out, n.ref))
	g.takeOstyEmitter(emitter)
	return value{typ: "i8", ref: out, sourceType: testingPropertyNamedSourceType("Byte")}, nil
}

func (g *generator) emitTestingPropertyAsciiStringCall(call *ast.CallExpr, seed value) (value, error) {
	if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
		return value{}, unsupported("call", "testing.gen.asciiString requires (maxLen)")
	}
	maxLen, err := g.emitExpr(call.Args[0].Value)
	if err != nil {
		return value{}, err
	}
	if maxLen.typ != "i64" {
		return value{}, unsupportedf("type-system", "testing.gen.asciiString maxLen type %s, want i64", maxLen.typ)
	}
	if seed.typ != "i64" {
		return value{}, unsupportedf("type-system", "testing property seed type %s, want i64", seed.typ)
	}
	g.declareRuntimeSymbol(ostyRtTestGenAsciiStringSymbol, "ptr", []paramInfo{{typ: "i64"}, {typ: "i64"}})
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, "ptr", ostyRtTestGenAsciiStringSymbol, []*LlvmValue{
		toOstyValue(maxLen),
		toOstyValue(seed),
	})
	g.takeOstyEmitter(emitter)
	text := fromOstyValue(out)
	text.gcManaged = true
	text.sourceType = &ast.NamedType{Path: []string{"String"}}
	return text, nil
}

func (g *generator) emitTestingPropertyMapCall(call *ast.CallExpr, seed value) (value, error) {
	if len(call.Args) != 2 || call.Args[0] == nil || call.Args[1] == nil ||
		call.Args[0].Name != "" || call.Args[1].Name != "" ||
		call.Args[0].Value == nil || call.Args[1].Value == nil {
		return value{}, unsupported("call", "testing.gen.map requires (generator, transform)")
	}
	sample, err := g.emitTestingPropertySample(call.Args[0].Value, seed)
	if err != nil {
		return value{}, err
	}
	return g.emitTestingPropertyApplyUnary(call.Args[1].Value, sample, "testing.gen.map transform")
}

func (g *generator) emitTestingPropertyFilterCall(call *ast.CallExpr, seed value) (value, error) {
	if len(call.Args) != 2 || call.Args[0] == nil || call.Args[1] == nil ||
		call.Args[0].Name != "" || call.Args[1].Name != "" ||
		call.Args[0].Value == nil || call.Args[1].Value == nil {
		return value{}, unsupported("call", "testing.gen.filter requires (generator, predicate)")
	}
	first, err := g.emitTestingPropertySample(call.Args[0].Value, seed)
	if err != nil {
		return value{}, err
	}
	firstPred, err := g.emitTestingPropertyBoolPredicate(call.Args[1].Value, first, "testing.gen.filter predicate")
	if err != nil {
		return value{}, err
	}
	emitter := g.toOstyEmitter()
	labels := llvmIfExprStart(emitter, toOstyValue(firstPred))
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.thenLabel
	thenValue := first
	thenPred := g.currentBlock

	emitter = g.toOstyEmitter()
	llvmIfExprElse(emitter, labels)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.elseLabel
	elseValue, err := g.emitTestingPropertyFilterRetry(call.Args[0].Value, call.Args[1].Value, seed, first)
	if err != nil {
		return value{}, err
	}
	elsePred := g.currentBlock
	return g.emitIfExprPhi(labels, thenPred, elsePred, thenValue, elseValue)
}

func (g *generator) emitTestingPropertyFilterRetry(genExpr, predExpr ast.Expr, seed value, sampleShape value) (value, error) {
	emitter := g.toOstyEmitter()
	slot := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirAllocaText(slot, sampleShape.typ))
	loop := llvmRangeStart(
		emitter,
		g.nextHiddenLocalName("test.property.filter.iter"),
		llvmIntLiteral(1),
		llvmIntLiteral(33),
		false,
	)
	g.takeOstyEmitter(emitter)
	g.enterBlock(loop.bodyLabel)

	iterSeed := value{typ: "i64", ref: loop.current}
	childSeed, err := g.emitTestingPropertyAddI64(seed, iterSeed)
	if err != nil {
		return value{}, err
	}
	candidate, err := g.emitTestingPropertySample(genExpr, childSeed)
	if err != nil {
		return value{}, err
	}
	if candidate.typ != sampleShape.typ {
		return value{}, unsupportedf("type-system", "testing.gen.filter retry sample type %s, want %s", candidate.typ, sampleShape.typ)
	}
	pred, err := g.emitTestingPropertyBoolPredicate(predExpr, candidate, "testing.gen.filter predicate")
	if err != nil {
		return value{}, err
	}

	emitter = g.toOstyEmitter()
	hitLabel := llvmNextLabel(emitter, "test.filter.hit")
	continueLabel := llvmNextLabel(emitter, "test.filter.cont")
	doneLabel := llvmNextLabel(emitter, "test.filter.done")
	emitter.body = append(emitter.body, mirBrCondText(pred.ref, hitLabel, continueLabel))
	emitter.body = append(emitter.body, mirLabelText(hitLabel))
	emitter.body = append(emitter.body, mirStoreText(candidate.typ, candidate.ref, slot))
	emitter.body = append(emitter.body, mirBrUncondText(doneLabel))
	emitter.body = append(emitter.body, mirLabelText(continueLabel))
	g.takeOstyEmitter(emitter)
	g.currentBlock = continueLabel

	emitter = g.toOstyEmitter()
	llvmRangeEnd(emitter, loop)
	g.emitTestingAbortWithEmitter(emitter, "testing.gen.filter could not produce a matching sample after 32 retries", doneLabel)
	loaded := llvmLoad(emitter, &LlvmValue{typ: sampleShape.typ, name: slot, pointer: true})
	g.takeOstyEmitter(emitter)
	g.currentBlock = doneLabel
	out := fromOstyValue(loaded)
	copyContainerMetadata(&out, sampleShape)
	out.rootPaths = cloneRootPaths(sampleShape.rootPaths)
	return out, nil
}

func (g *generator) emitTestingPropertyBoolPredicate(predExpr ast.Expr, sample value, label string) (value, error) {
	pred, err := g.emitTestingPropertyApplyUnary(predExpr, sample, label)
	if err != nil {
		return value{}, err
	}
	if pred.typ != "i1" {
		return value{}, unsupportedf("type-system", "%s type %s, want Bool", label, pred.typ)
	}
	return pred, nil
}

func (g *generator) emitTestingPropertyApplyUnary(fnExpr ast.Expr, sample value, label string) (value, error) {
	if closure, ok := fnExpr.(*ast.ClosureExpr); ok && closure != nil {
		if len(closure.Params) != 1 || closure.Body == nil {
			return value{}, unsupportedf("call", "%s requires a one-argument closure", label)
		}
		g.pushScope()
		defer g.popScope()
		if err := g.bindTestingPropertyParam(closure.Params[0], sample); err != nil {
			return value{}, err
		}
		return g.emitExpr(closure.Body)
	}
	fn, err := g.emitExpr(fnExpr)
	if err != nil {
		return value{}, err
	}
	sig, err := requireFnValueSignature(fn, label)
	if err != nil {
		return value{}, err
	}
	if len(sig.params) != 1 {
		return value{}, unsupportedf("call", "%s arity %d, want 1", label, len(sig.params))
	}
	if sig.params[0].typ != sample.typ {
		return value{}, unsupportedf("type-system", "%s param type %s, sample %s", label, sig.params[0].typ, sample.typ)
	}
	return g.emitFnValueIndirectCall(fn, sig, []*LlvmValue{toOstyValue(sample)})
}

func (g *generator) emitTestingPropertyPairLike(call *ast.CallExpr, seed value, arity int) (value, error) {
	if len(call.Args) != arity {
		return value{}, unsupportedf("call", "testing.gen.%s requires %d positional arguments", g.testingGenCallNameForArity(arity), arity)
	}
	parts := make([]value, 0, arity)
	for i, arg := range call.Args {
		if arg == nil || arg.Name != "" || arg.Value == nil {
			return value{}, unsupportedf("call", "testing.gen.%s requires positional generator arguments", g.testingGenCallNameForArity(arity))
		}
		childSeed, err := g.emitTestingPropertySeedOffset(seed, int64(i+1))
		if err != nil {
			return value{}, err
		}
		part, err := g.emitTestingPropertySample(arg.Value, childSeed)
		if err != nil {
			return value{}, err
		}
		parts = append(parts, part)
	}
	return g.emitTestingPropertyTuple(parts...)
}

func (g *generator) testingGenCallNameForArity(arity int) string {
	switch arity {
	case 2:
		return "pair"
	case 3:
		return "triple"
	default:
		return "tuple"
	}
}

func (g *generator) emitTestingPropertyTuple(parts ...value) (value, error) {
	fields := make([]*LlvmValue, 0, len(parts))
	elemTypes := make([]string, 0, len(parts))
	elemListTypes := make([]string, 0, len(parts))
	sourceElems := make([]ast.Type, 0, len(parts))
	for _, part := range parts {
		fields = append(fields, toOstyValue(part))
		elemTypes = append(elemTypes, part.typ)
		elemListTypes = append(elemListTypes, part.listElemTyp)
		sourceElems = append(sourceElems, testingPropertySourceTypeForValue(part))
	}
	info := g.registerTupleType(elemTypes, elemListTypes)
	emitter := g.toOstyEmitter()
	out := llvmStructLiteral(emitter, info.typ, fields)
	g.takeOstyEmitter(emitter)
	tupleValue := fromOstyValue(out)
	tupleValue.rootPaths = g.rootPathsForType(info.typ)
	tupleValue.sourceType = &ast.TupleType{Elems: sourceElems}
	return tupleValue, nil
}

func (g *generator) emitTestingPropertyListCall(call *ast.CallExpr, seed value, fixed bool) (value, error) {
	if len(call.Args) != 2 || call.Args[0] == nil || call.Args[1] == nil ||
		call.Args[0].Name != "" || call.Args[1].Name != "" ||
		call.Args[0].Value == nil || call.Args[1].Value == nil {
		if fixed {
			return value{}, unsupported("call", "testing.gen.listOfSize requires (item, size)")
		}
		return value{}, unsupported("call", "testing.gen.list requires (item, maxLen)")
	}
	limit, err := g.emitExpr(call.Args[1].Value)
	if err != nil {
		return value{}, err
	}
	if limit.typ != "i64" {
		return value{}, unsupportedf("type-system", "testing.gen.list size type %s, want Int", limit.typ)
	}
	length := limit
	if !fixed {
		hi, err := g.emitTestingPropertyAddI64(limit, value{typ: "i64", ref: "1"})
		if err != nil {
			return value{}, err
		}
		length, err = g.emitTestingPropertyIntRange(value{typ: "i64", ref: "0"}, hi, seed)
		if err != nil {
			return value{}, err
		}
	}

	previewSeed, err := g.emitTestingPropertySeedOffset(seed, 1)
	if err != nil {
		return value{}, err
	}
	preview, err := g.emitTestingPropertySample(call.Args[0].Value, previewSeed)
	if err != nil {
		return value{}, err
	}
	elemTyp := preview.typ
	elemString := llvmNamedTypeIsString(testingPropertySourceTypeForValue(preview))
	useAggregateABI := g.usesAggregateListABI(elemTyp)
	if !useAggregateABI && !listUsesTypedRuntime(elemTyp) {
		g.traceCallbackSymbol(elemTyp, g.rootPathsForType(elemTyp))
	}

	g.declareRuntimeSymbol(listRuntimeNewSymbol(), "ptr", nil)
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, "ptr", listRuntimeNewSymbol(), nil)
	g.takeOstyEmitter(emitter)
	listValue := fromOstyValue(out)
	listValue.gcManaged = true
	listValue.listElemTyp = elemTyp
	listValue.listElemString = elemString
	listValue.sourceType = &ast.NamedType{
		Path: []string{"List"},
		Args: []ast.Type{testingPropertySourceTypeForValue(preview)},
	}
	listValue.rootPaths = g.rootPathsForType("ptr")

	emitter = g.toOstyEmitter()
	loop := llvmRangeStart(
		emitter,
		g.nextHiddenLocalName("test.property.list.iter"),
		llvmIntLiteral(0),
		toOstyValue(length),
		false,
	)
	g.takeOstyEmitter(emitter)
	g.enterBlock(loop.bodyLabel)

	iterSeed := value{typ: "i64", ref: loop.current}
	childSeed, err := g.emitTestingPropertyAddI64(seed, iterSeed)
	if err != nil {
		return value{}, err
	}
	childSeed, err = g.emitTestingPropertySeedOffset(childSeed, 2)
	if err != nil {
		return value{}, err
	}
	elem, err := g.emitTestingPropertySample(call.Args[0].Value, childSeed)
	if err != nil {
		return value{}, err
	}
	if elem.typ != elemTyp {
		return value{}, unsupportedf("type-system", "testing.gen.list item type %s, want %s", elem.typ, elemTyp)
	}
	if err := g.emitTestingPropertyListPush(listValue, elem, elemTyp, elemString); err != nil {
		return value{}, err
	}

	emitter = g.toOstyEmitter()
	llvmRangeEnd(emitter, loop)
	g.takeOstyEmitter(emitter)
	g.enterBlock(loop.endLabel)
	return listValue, nil
}

func (g *generator) emitTestingPropertyListPush(listValue, elem value, elemTyp string, elemString bool) error {
	loaded, err := g.loadIfPointer(elem)
	if err != nil {
		return err
	}
	if g.usesAggregateListABI(elemTyp) {
		return g.emitListAggregatePush(listValue, loaded)
	}
	emitter := g.toOstyEmitter()
	if listUsesTypedRuntime(elemTyp) {
		pushSymbol := listRuntimePushSymbolFor(elemTyp, elemString)
		g.declareRuntimeSymbol(pushSymbol, "void", []paramInfo{{typ: "ptr"}, {typ: elemTyp}})
		emitter.body = append(emitter.body, mirCallRuntimeVoidOneArgText(
			pushSymbol,
			llvmCallArgs([]*LlvmValue{toOstyValue(listValue), toOstyValue(loaded)}),
		))
	} else {
		traceSymbol := g.traceCallbackSymbol(elemTyp, g.rootPathsForType(elemTyp))
		addr := g.spillValueAddress(emitter, "test.property.list.elem", loaded)
		sizeValue := g.emitTypeSize(emitter, elemTyp)
		g.declareRuntimeSymbol(listRuntimePushBytesSymbol(), "void", []paramInfo{{typ: "ptr"}, {typ: "ptr"}, {typ: "i64"}, {typ: "ptr"}})
		emitter.body = append(emitter.body, mirCallRuntimeVoidOneArgText(
			listRuntimePushBytesSymbol(),
			llvmCallArgs([]*LlvmValue{
				toOstyValue(listValue),
				{typ: "ptr", name: addr},
				sizeValue,
				{typ: "ptr", name: llvmPointerOperand(traceSymbol)},
			}),
		))
	}
	g.takeOstyEmitter(emitter)
	return nil
}

func (g *generator) emitTestingPropertyOptionCall(call *ast.CallExpr, seed value) (value, error) {
	if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
		return value{}, unsupported("call", "testing.gen.option requires one generator argument")
	}
	pick, err := g.emitTestingPropertyIntRange(value{typ: "i64", ref: "0"}, value{typ: "i64", ref: "2"}, seed)
	if err != nil {
		return value{}, err
	}
	cond, err := g.emitTestingPropertyI64EqLiteral(pick, "0")
	if err != nil {
		return value{}, err
	}
	emitter := g.toOstyEmitter()
	labels := llvmIfExprStart(emitter, toOstyValue(cond))
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.thenLabel

	childSeed, err := g.emitTestingPropertySeedOffset(seed, 1)
	if err != nil {
		return value{}, err
	}
	sample, err := g.emitTestingPropertySample(call.Args[0].Value, childSeed)
	if err != nil {
		return value{}, err
	}
	someValue, err := g.emitTestingPropertySomeValue(sample)
	if err != nil {
		return value{}, err
	}
	thenPred := g.currentBlock

	emitter = g.toOstyEmitter()
	llvmIfExprElse(emitter, labels)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.elseLabel
	noneValue := value{typ: "ptr", ref: "null"}
	elsePred := g.currentBlock

	out, err := g.emitIfExprPhi(labels, thenPred, elsePred, someValue, noneValue)
	if err != nil {
		return value{}, err
	}
	out.sourceType = &ast.OptionalType{Inner: testingPropertySourceTypeForValue(sample)}
	out.gcManaged = true
	out.rootPaths = g.rootPathsForType("ptr")
	return out, nil
}

func (g *generator) emitTestingPropertySomeValue(sample value) (value, error) {
	loaded, err := g.loadIfPointer(sample)
	if err != nil {
		return value{}, err
	}
	if loaded.typ == "ptr" {
		loaded.gcManaged = true
		loaded.rootPaths = g.rootPathsForType("ptr")
		return loaded, nil
	}
	emitter := g.toOstyEmitter()
	box, err := g.emitOptionPayloadBox(emitter, loaded, "testing.gen.option."+optionBoxSiteSuffix(loaded.typ))
	if err != nil {
		g.takeOstyEmitter(emitter)
		return value{}, err
	}
	g.takeOstyEmitter(emitter)
	return box, nil
}

func (g *generator) emitTestingPropertyResultCall(call *ast.CallExpr, seed value) (value, error) {
	if len(call.Args) != 2 || call.Args[0] == nil || call.Args[1] == nil ||
		call.Args[0].Name != "" || call.Args[1].Name != "" ||
		call.Args[0].Value == nil || call.Args[1].Value == nil {
		return value{}, unsupported("call", "testing.gen.result requires (ok, err)")
	}
	okSeed, err := g.emitTestingPropertySeedOffset(seed, 1)
	if err != nil {
		return value{}, err
	}
	okSample, err := g.emitTestingPropertySample(call.Args[0].Value, okSeed)
	if err != nil {
		return value{}, err
	}
	errSeed, err := g.emitTestingPropertySeedOffset(seed, 2)
	if err != nil {
		return value{}, err
	}
	errSample, err := g.emitTestingPropertySample(call.Args[1].Value, errSeed)
	if err != nil {
		return value{}, err
	}
	sourceType := &ast.NamedType{
		Path: []string{"Result"},
		Args: []ast.Type{testingPropertySourceTypeForValue(okSample), testingPropertySourceTypeForValue(errSample)},
	}
	info, ok := builtinResultTypeFromAST(sourceType, g.typeEnv())
	if !ok {
		return value{}, unsupported("type-system", "testing.gen.result could not build Result<T, E> source type")
	}
	if okSample.typ != info.okTyp || errSample.typ != info.errTyp {
		return value{}, unsupportedf("type-system", "testing.gen.result payload types ok=%s/%s err=%s/%s", okSample.typ, info.okTyp, errSample.typ, info.errTyp)
	}

	pick, err := g.emitTestingPropertyIntRange(value{typ: "i64", ref: "0"}, value{typ: "i64", ref: "2"}, seed)
	if err != nil {
		return value{}, err
	}
	cond, err := g.emitTestingPropertyI64EqLiteral(pick, "0")
	if err != nil {
		return value{}, err
	}
	emitter := g.toOstyEmitter()
	labels := llvmIfExprStart(emitter, toOstyValue(cond))
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.thenLabel

	okValue, err := g.emitTestingPropertyResultValue(info, true, okSample)
	if err != nil {
		return value{}, err
	}
	thenPred := g.currentBlock

	emitter = g.toOstyEmitter()
	llvmIfExprElse(emitter, labels)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.elseLabel

	errValue, err := g.emitTestingPropertyResultValue(info, false, errSample)
	if err != nil {
		return value{}, err
	}
	elsePred := g.currentBlock

	out, err := g.emitIfExprPhi(labels, thenPred, elsePred, okValue, errValue)
	if err != nil {
		return value{}, err
	}
	out.sourceType = sourceType
	out.rootPaths = g.rootPathsForType(out.typ)
	return out, nil
}

func (g *generator) emitTestingPropertyResultValue(info builtinResultType, isOk bool, payload value) (value, error) {
	payloadIndex := 1
	tag := "0"
	wantTyp := info.okTyp
	if !isOk {
		payloadIndex = 2
		tag = "1"
		wantTyp = info.errTyp
	}
	loaded, err := g.loadIfPointer(payload)
	if err != nil {
		return value{}, err
	}
	if loaded.typ != wantTyp {
		return value{}, unsupportedf("type-system", "testing.gen.result payload type %s, want %s", loaded.typ, wantTyp)
	}
	emitter := g.toOstyEmitter()
	fields := []*LlvmValue{
		toOstyValue(value{typ: "i64", ref: tag}),
		toOstyValue(llvmZeroValue(info.okTyp)),
		toOstyValue(llvmZeroValue(info.errTyp)),
	}
	fields[payloadIndex] = toOstyValue(loaded)
	out := llvmStructLiteral(emitter, info.typ, fields)
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.rootPaths = g.rootPathsForType(v.typ)
	return v, nil
}

func (g *generator) emitTestingPropertyOneOfCall(call *ast.CallExpr, seed value) (value, error) {
	if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
		return value{}, unsupported("call", "testing.gen.oneOf requires a single list literal argument")
	}
	listExpr, ok := unwrapParenExpr(call.Args[0].Value).(*ast.ListExpr)
	if !ok || listExpr == nil {
		return value{}, unsupported("call", "testing.gen.oneOf currently requires a list literal")
	}
	if len(listExpr.Elems) == 0 {
		return value{}, unsupported("call", "testing.gen.oneOf requires a non-empty list literal")
	}
	index, err := g.emitTestingPropertyIntRange(
		value{typ: "i64", ref: "0"},
		value{typ: "i64", ref: mirGenIntToString(len(listExpr.Elems))},
		seed,
	)
	if err != nil {
		return value{}, err
	}
	indexName := g.nextHiddenLocalName("test.property.oneof")
	g.bindNamedLocal(indexName, index, false)
	return g.emitExpr(&ast.IndexExpr{
		X:     listExpr,
		Index: &ast.Ident{Name: indexName},
	})
}

func (g *generator) emitTestingPropertyOneOfGensCall(call *ast.CallExpr, seed value) (value, error) {
	if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
		return value{}, unsupported("call", "testing.gen.oneOfGens requires a single list literal argument")
	}
	listExpr, ok := unwrapParenExpr(call.Args[0].Value).(*ast.ListExpr)
	if !ok || listExpr == nil {
		return value{}, unsupported("call", "testing.gen.oneOfGens currently requires a list literal")
	}
	if len(listExpr.Elems) == 0 {
		return value{}, unsupported("call", "testing.gen.oneOfGens requires a non-empty list literal")
	}
	index, err := g.emitTestingPropertyIntRange(
		value{typ: "i64", ref: "0"},
		value{typ: "i64", ref: mirGenIntToString(len(listExpr.Elems))},
		seed,
	)
	if err != nil {
		return value{}, err
	}
	return g.emitTestingPropertyOneOfGenChoice(listExpr.Elems, index, seed, 0)
}

func (g *generator) emitTestingPropertyOneOfGenChoice(choices []ast.Expr, index value, seed value, pos int) (value, error) {
	if pos >= len(choices) {
		return value{}, unsupported("call", "testing.gen.oneOfGens choice index out of range")
	}
	choiceSeed, err := g.emitTestingPropertySeedOffset(seed, int64(pos+1))
	if err != nil {
		return value{}, err
	}
	if pos == len(choices)-1 {
		return g.emitTestingPropertySample(choices[pos], choiceSeed)
	}
	cond, err := g.emitTestingPropertyI64EqLiteral(index, mirGenIntToString(pos))
	if err != nil {
		return value{}, err
	}
	emitter := g.toOstyEmitter()
	labels := llvmIfExprStart(emitter, toOstyValue(cond))
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.thenLabel
	thenValue, err := g.emitTestingPropertySample(choices[pos], choiceSeed)
	if err != nil {
		return value{}, err
	}
	thenPred := g.currentBlock

	emitter = g.toOstyEmitter()
	llvmIfExprElse(emitter, labels)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.elseLabel
	elseValue, err := g.emitTestingPropertyOneOfGenChoice(choices, index, seed, pos+1)
	if err != nil {
		return value{}, err
	}
	elsePred := g.currentBlock
	return g.emitIfExprPhi(labels, thenPred, elsePred, thenValue, elseValue)
}

func (g *generator) emitTestingPropertyI64EqLiteral(v value, lit string) (value, error) {
	if v.typ != "i64" {
		return value{}, unsupportedf("type-system", "testing property selector type %s, want Int", v.typ)
	}
	emitter := g.toOstyEmitter()
	out := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirICmpEqI64LiteralText(out, v.ref, lit))
	g.takeOstyEmitter(emitter)
	return value{typ: "i1", ref: out, sourceType: testingPropertyNamedSourceType("Bool")}, nil
}

func testingPropertyNamedSourceType(name string) ast.Type {
	return &ast.NamedType{Path: []string{name}}
}

func testingPropertySourceTypeForValue(v value) ast.Type {
	if v.sourceType != nil {
		return v.sourceType
	}
	switch v.typ {
	case "i64":
		return testingPropertyNamedSourceType("Int")
	case "i1":
		return testingPropertyNamedSourceType("Bool")
	case "double":
		return testingPropertyNamedSourceType("Float64")
	case "i32":
		return testingPropertyNamedSourceType("Char")
	case "i8":
		return testingPropertyNamedSourceType("Byte")
	}
	return nil
}

func testingPropertySourceTypeForGeneratorExpr(expr ast.Expr) ast.Type {
	call, ok := unwrapParenExpr(expr).(*ast.CallExpr)
	if !ok || call == nil {
		return nil
	}
	field, ok := fieldExprOfCallFn(call)
	if !ok || field.IsOptional {
		return nil
	}
	switch field.Name {
	case "int", "intRange":
		return testingPropertyNamedSourceType("Int")
	case "bool":
		return testingPropertyNamedSourceType("Bool")
	case "float":
		return testingPropertyNamedSourceType("Float64")
	case "char":
		return testingPropertyNamedSourceType("Char")
	case "byte":
		return testingPropertyNamedSourceType("Byte")
	case "asciiString":
		return testingPropertyNamedSourceType("String")
	}
	return nil
}

func unwrapParenExpr(expr ast.Expr) ast.Expr {
	for {
		par, ok := expr.(*ast.ParenExpr)
		if !ok || par == nil || par.X == nil {
			return expr
		}
		expr = par.X
	}
}

func (g *generator) emitTestingPropertySeedOffset(seed value, delta int64) (value, error) {
	return g.emitTestingPropertyAddI64(seed, value{typ: "i64", ref: mirGenIntToString(int(delta))})
}

func (g *generator) emitTestingPropertyAddI64(left, right value) (value, error) {
	if left.typ != "i64" || right.typ != "i64" {
		return value{}, unsupportedf("type-system", "testing property arithmetic expects i64 operands, got %s/%s", left.typ, right.typ)
	}
	emitter := g.toOstyEmitter()
	name := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirAddI64Text(name, left.ref, right.ref))
	g.takeOstyEmitter(emitter)
	return value{typ: "i64", ref: name}, nil
}
