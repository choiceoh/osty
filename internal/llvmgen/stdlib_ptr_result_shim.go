package llvmgen

import "github.com/osty/osty/internal/ast"

func (g *generator) ptrBackedResultInfo(prefix string, sourceType ast.Type) (builtinResultType, error) {
	info, ok := builtinResultTypeFromAST(sourceType, g.typeEnv())
	if !ok {
		return builtinResultType{}, unsupportedf("type-system", "%s result type unavailable", prefix)
	}
	if g.resultTypes == nil {
		g.resultTypes = map[string]builtinResultType{}
	}
	g.resultTypes[info.typ] = info
	if info.okTyp != "ptr" || info.errTyp != "ptr" {
		return builtinResultType{}, unsupportedf("type-system", "%s currently needs ptr-backed Result, got ok=%s err=%s", prefix, info.okTyp, info.errTyp)
	}
	return info, nil
}

func (g *generator) emitPtrBackedResultFromRuntimeCall(prefix string, sourceType ast.Type, valueSymbol, errorSymbol string, params []paramInfo, args []*LlvmValue) (value, bool, error) {
	info, err := g.ptrBackedResultInfo(prefix, sourceType)
	if err != nil {
		return value{}, true, err
	}
	g.declareRuntimeSymbol(valueSymbol, "ptr", params)
	g.declareRuntimeSymbol(errorSymbol, "ptr", nil)
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	out := llvmCall(emitter, "ptr", valueSymbol, args)
	v := g.emitPtrBackedResultFromNullablePtr(emitter, prefix, sourceType, info, out, func(emitter *LlvmEmitter) *LlvmValue {
		return llvmCall(emitter, "ptr", errorSymbol, nil)
	})
	g.takeOstyEmitter(emitter)
	return v, true, nil
}

func (g *generator) emitPtrBackedResultFromNullablePtr(emitter *LlvmEmitter, prefix string, sourceType ast.Type, info builtinResultType, out *LlvmValue, emitErrText func(*LlvmEmitter) *LlvmValue) value {
	failed := llvmCompare(emitter, "eq", out, toOstyValue(value{typ: "ptr", ref: "null"}))
	errLabel := llvmNextLabel(emitter, prefix+".err")
	okLabel := llvmNextLabel(emitter, prefix+".ok")
	contLabel := llvmNextLabel(emitter, prefix+".cont")
	emitter.body = append(emitter.body, "  br i1 "+failed.name+", label %"+errLabel+", label %"+okLabel)

	emitter.body = append(emitter.body, errLabel+":")
	errText := emitErrText(emitter)
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
	v := value{typ: info.typ, ref: phi, sourceType: sourceType}
	v.rootPaths = g.rootPathsForType(v.typ)
	return v
}
