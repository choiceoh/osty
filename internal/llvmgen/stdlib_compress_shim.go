package llvmgen

import "github.com/osty/osty/internal/ast"

const ostyRtCompressGzipEncodeSymbol = "osty_rt_compress_gzip_encode"
const ostyRtCompressGzipDecodeSymbol = "osty_rt_compress_gzip_decode"

func collectStdCompressAliases(file *ast.File) map[string]bool {
	out := map[string]bool{}
	if file == nil {
		return out
	}
	for _, use := range file.Uses {
		if use == nil || use.IsFFI() {
			continue
		}
		if len(use.Path) != 2 || use.Path[0] != "std" || use.Path[1] != "compress" {
			continue
		}
		alias := use.Alias
		if alias == "" {
			alias = "compress"
		}
		out[alias] = true
	}
	return out
}

func gzipDecodeResultSourceType() ast.Type {
	return &ast.NamedType{
		Path: []string{"Result"},
		Args: []ast.Type{
			&ast.NamedType{Path: []string{"Bytes"}},
			&ast.NamedType{Path: []string{"Error"}},
		},
	}
}

func (g *generator) emitStdCompressCall(call *ast.CallExpr) (value, bool, error) {
	field, ok := g.stdCompressGzipCallInfo(call)
	if !ok {
		return value{}, false, nil
	}
	switch field.Name {
	case "encode":
		if len(call.Args) != 1 {
			return value{}, true, unsupportedf("call", "compress.gzip.encode expects 1 argument, got %d", len(call.Args))
		}
		data, err := g.emitStdBytesArg(call.Args[0], "gzip.encode", 0)
		if err != nil {
			return value{}, true, err
		}
		out, err := g.emitGzipEncodeRuntime(data)
		return out, true, err
	case "decode":
		if len(call.Args) != 1 {
			return value{}, true, unsupportedf("call", "compress.gzip.decode expects 1 argument, got %d", len(call.Args))
		}
		data, err := g.emitStdBytesArg(call.Args[0], "gzip.decode", 0)
		if err != nil {
			return value{}, true, err
		}
		out, err := g.emitGzipDecodeResult(data)
		return out, true, err
	default:
		return value{}, false, nil
	}
}

func (g *generator) stdCompressCallStaticResult(call *ast.CallExpr) (value, bool) {
	field, ok := g.stdCompressGzipCallInfo(call)
	if !ok {
		return value{}, false
	}
	switch field.Name {
	case "encode":
		return value{typ: "ptr", gcManaged: true, sourceType: &ast.NamedType{Path: []string{"Bytes"}}}, true
	case "decode":
		if info, ok := builtinResultTypeFromAST(gzipDecodeResultSourceType(), g.typeEnv()); ok {
			return value{typ: info.typ, sourceType: gzipDecodeResultSourceType(), rootPaths: g.rootPathsForType(info.typ)}, true
		}
		return value{}, false
	default:
		return value{}, false
	}
}

func (g *generator) staticStdCompressCallSourceType(call *ast.CallExpr) (ast.Type, bool) {
	field, ok := g.stdCompressGzipCallInfo(call)
	if !ok {
		return nil, false
	}
	switch field.Name {
	case "encode":
		return &ast.NamedType{Path: []string{"Bytes"}}, true
	case "decode":
		return gzipDecodeResultSourceType(), true
	default:
		return nil, false
	}
}

func (g *generator) stdCompressGzipCallInfo(call *ast.CallExpr) (*ast.FieldExpr, bool) {
	if call == nil || len(g.stdCompressAliases) == 0 {
		return nil, false
	}
	field, ok := fieldExprOfCallFn(call)
	if !ok || field.IsOptional {
		return nil, false
	}
	gzipField, ok := field.X.(*ast.FieldExpr)
	if !ok || gzipField == nil || gzipField.IsOptional || gzipField.Name != "gzip" {
		return nil, false
	}
	alias, ok := gzipField.X.(*ast.Ident)
	if !ok || alias == nil || !g.stdCompressAliases[alias.Name] {
		return nil, false
	}
	switch field.Name {
	case "encode", "decode":
		return field, true
	default:
		return nil, false
	}
}

func (g *generator) emitGzipEncodeRuntime(data value) (value, error) {
	g.declareRuntimeSymbol(ostyRtCompressGzipEncodeSymbol, "ptr", []paramInfo{{typ: "ptr"}})
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, "ptr", ostyRtCompressGzipEncodeSymbol, []*LlvmValue{toOstyValue(data)})
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.gcManaged = true
	v.sourceType = &ast.NamedType{Path: []string{"Bytes"}}
	return v, nil
}

func (g *generator) emitGzipDecodeResult(data value) (value, error) {
	sourceType := gzipDecodeResultSourceType()
	info, ok := builtinResultTypeFromAST(sourceType, g.typeEnv())
	if !ok {
		return value{}, unsupported("type-system", "compress.gzip.decode Result<Bytes, Error> type is unavailable")
	}
	if g.resultTypes == nil {
		g.resultTypes = map[string]builtinResultType{}
	}
	g.resultTypes[info.typ] = info
	g.declareRuntimeSymbol(ostyRtCompressGzipDecodeSymbol, "ptr", []paramInfo{{typ: "ptr"}})

	emitter := g.toOstyEmitter()
	decoded := llvmCall(emitter, "ptr", ostyRtCompressGzipDecodeSymbol, []*LlvmValue{toOstyValue(data)})
	notNil := llvmCompare(emitter, "ne", decoded, &LlvmValue{typ: "ptr", name: "null"})
	labels := llvmIfExprStart(emitter, notNil)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.thenLabel

	emitter = g.toOstyEmitter()
	okResult := llvmStructLiteral(emitter, info.typ, []*LlvmValue{
		toOstyValue(value{typ: "i64", ref: "0"}),
		decoded,
		toOstyValue(llvmZeroValue(info.errTyp)),
	})
	g.takeOstyEmitter(emitter)
	thenValue := value{
		typ:        info.typ,
		ref:        okResult.name,
		sourceType: sourceType,
		rootPaths:  g.rootPathsForType(info.typ),
	}
	thenPred := g.currentBlock

	emitter = g.toOstyEmitter()
	llvmIfExprElse(emitter, labels)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.elseLabel

	emitter = g.toOstyEmitter()
	errResult := llvmStructLiteral(emitter, info.typ, []*LlvmValue{
		toOstyValue(value{typ: "i64", ref: "1"}),
		toOstyValue(llvmZeroValue(info.okTyp)),
		toOstyValue(llvmZeroValue(info.errTyp)),
	})
	g.takeOstyEmitter(emitter)
	elseValue := value{
		typ:        info.typ,
		ref:        errResult.name,
		sourceType: sourceType,
		rootPaths:  g.rootPathsForType(info.typ),
	}
	elsePred := g.currentBlock

	out, err := g.emitIfExprPhi(labels, thenPred, elsePred, thenValue, elseValue)
	if err != nil {
		return value{}, err
	}
	out.sourceType = sourceType
	out.rootPaths = g.rootPathsForType(info.typ)
	return out, nil
}
