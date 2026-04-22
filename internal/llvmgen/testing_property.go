package llvmgen

import (
	"fmt"

	"github.com/osty/osty/internal/ast"
)

const (
	ostyRtTestGenIntSymbol         = "osty_rt_test_gen_int"
	ostyRtTestGenIntRangeSymbol    = "osty_rt_test_gen_int_range"
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
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", continueLabel))
	g.emitGCSafepointKind(emitter, safepointKindLoop)
	llvmRangeEnd(emitter, loop)
	g.takeOstyEmitter(emitter)
	g.enterBlock(loop.endLabel)
	return nil
}

func (g *generator) emitTestingPropertyPredicate(predExpr ast.Expr, sample value) (value, error) {
	if closure, ok := predExpr.(*ast.ClosureExpr); ok && closure != nil {
		if len(closure.Params) != 1 || closure.Body == nil {
			return value{}, unsupported("call", "testing.property requires a one-argument closure predicate")
		}
		if err := g.bindTestingPropertyParam(closure.Params[0], sample); err != nil {
			return value{}, err
		}
		return g.emitExpr(closure.Body)
	}
	predFn, err := g.emitExpr(predExpr)
	if err != nil {
		return value{}, err
	}
	sig, err := requireFnValueSignature(predFn, "testing.property predicate")
	if err != nil {
		return value{}, err
	}
	if len(sig.params) != 1 {
		return value{}, unsupportedf("call", "testing.property predicate arity %d, want 1", len(sig.params))
	}
	if sig.params[0].typ != sample.typ {
		return value{}, unsupportedf("type-system", "testing.property predicate param type %s, sample %s", sig.params[0].typ, sample.typ)
	}
	return g.emitFnValueIndirectCall(predFn, sig, []*LlvmValue{toOstyValue(sample)})
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
		case "asciiString":
			return g.emitTestingPropertyAsciiStringCall(e, seed)
		case "pair":
			return g.emitTestingPropertyPairLike(e, seed, 2)
		case "triple":
			return g.emitTestingPropertyPairLike(e, seed, 3)
		case "oneOf":
			return g.emitTestingPropertyOneOfCall(e, seed)
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
	return fromOstyValue(out), nil
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
	return fromOstyValue(out), nil
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
	for _, part := range parts {
		fields = append(fields, toOstyValue(part))
		elemTypes = append(elemTypes, part.typ)
		elemListTypes = append(elemListTypes, part.listElemTyp)
	}
	info := g.registerTupleType(elemTypes, elemListTypes)
	emitter := g.toOstyEmitter()
	out := llvmStructLiteral(emitter, info.typ, fields)
	g.takeOstyEmitter(emitter)
	tupleValue := fromOstyValue(out)
	tupleValue.rootPaths = g.rootPathsForType(info.typ)
	return tupleValue, nil
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
		value{typ: "i64", ref: fmt.Sprintf("%d", len(listExpr.Elems))},
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
	return g.emitTestingPropertyAddI64(seed, value{typ: "i64", ref: fmt.Sprintf("%d", delta)})
}

func (g *generator) emitTestingPropertyAddI64(left, right value) (value, error) {
	if left.typ != "i64" || right.typ != "i64" {
		return value{}, unsupportedf("type-system", "testing property arithmetic expects i64 operands, got %s/%s", left.typ, right.typ)
	}
	emitter := g.toOstyEmitter()
	name := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = add i64 %s, %s", name, left.ref, right.ref))
	g.takeOstyEmitter(emitter)
	return value{typ: "i64", ref: name}, nil
}
