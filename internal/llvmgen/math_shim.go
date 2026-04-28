package llvmgen

import (
	"math"

	"github.com/osty/osty/internal/ast"
)

const (
	ostyRtMathLogSymbol = "osty_rt_math_log"

	ostyRtFloatAbsSymbol        = "osty_rt_float_abs"
	ostyRtFloatMinSymbol        = "osty_rt_float_min"
	ostyRtFloatMaxSymbol        = "osty_rt_float_max"
	ostyRtFloatClampSymbol      = "osty_rt_float_clamp"
	ostyRtFloatSignumSymbol     = "osty_rt_float_signum"
	ostyRtFloatFloorSymbol      = "osty_rt_float_floor"
	ostyRtFloatCeilSymbol       = "osty_rt_float_ceil"
	ostyRtFloatRoundSymbol      = "osty_rt_float_round"
	ostyRtFloatTruncSymbol      = "osty_rt_float_trunc"
	ostyRtFloatFractSymbol      = "osty_rt_float_fract"
	ostyRtFloatSqrtSymbol       = "osty_rt_float_sqrt"
	ostyRtFloatCbrtSymbol       = "osty_rt_float_cbrt"
	ostyRtFloatLnSymbol         = "osty_rt_float_ln"
	ostyRtFloatLog2Symbol       = "osty_rt_float_log2"
	ostyRtFloatLog10Symbol      = "osty_rt_float_log10"
	ostyRtFloatExpSymbol        = "osty_rt_float_exp"
	ostyRtFloatSinhSymbol       = "osty_rt_float_sinh"
	ostyRtFloatCoshSymbol       = "osty_rt_float_cosh"
	ostyRtFloatTanhSymbol       = "osty_rt_float_tanh"
	ostyRtFloatSinSymbol        = "osty_rt_float_sin"
	ostyRtFloatCosSymbol        = "osty_rt_float_cos"
	ostyRtFloatTanSymbol        = "osty_rt_float_tan"
	ostyRtFloatAsinSymbol       = "osty_rt_float_asin"
	ostyRtFloatAcosSymbol       = "osty_rt_float_acos"
	ostyRtFloatAtanSymbol       = "osty_rt_float_atan"
	ostyRtFloatAtan2Symbol      = "osty_rt_float_atan2"
	ostyRtFloatPowSymbol        = "osty_rt_float_pow"
	ostyRtFloatHypotSymbol      = "osty_rt_float_hypot"
	ostyRtFloatIsNaNSymbol      = "osty_rt_float_is_nan"
	ostyRtFloatIsInfiniteSymbol = "osty_rt_float_is_infinite"
	ostyRtFloatIsFiniteSymbol   = "osty_rt_float_is_finite"
	ostyRtFloatToFixedSymbol    = "osty_rt_float_to_fixed"
	ostyRtFloatToBits64Symbol   = "osty_rt_float64_to_bits"
	ostyRtFloatToBits32Symbol   = "osty_rt_float32_to_bits"

	ostyRtFloatToIntTruncSymbol = "osty_rt_float_to_int_trunc"
	ostyRtFloatToIntRoundSymbol = "osty_rt_float_to_int_round"
	ostyRtFloatToIntFloorSymbol = "osty_rt_float_to_int_floor"
	ostyRtFloatToIntCeilSymbol  = "osty_rt_float_to_int_ceil"

	ostyRtFloatToIntLossySymbol   = "osty_rt_float_to_int_lossy"
	ostyRtFloatToInt32LossySymbol = "osty_rt_float_to_int32_lossy"
	ostyRtFloatToInt64LossySymbol = "osty_rt_float_to_int64_lossy"
)

var (
	floatSourceTypeSingleton   = &ast.NamedType{Path: []string{"Float"}}
	float32SourceTypeSingleton = &ast.NamedType{Path: []string{"Float32"}}
	float64SourceTypeSingleton = &ast.NamedType{Path: []string{"Float64"}}
	intSourceTypeSingleton     = &ast.NamedType{Path: []string{"Int"}}
	int32SourceTypeSingleton   = &ast.NamedType{Path: []string{"Int32"}}
	int64SourceTypeSingleton   = &ast.NamedType{Path: []string{"Int64"}}
	uint64SourceTypeSingleton  = &ast.NamedType{Path: []string{"UInt64"}}
	boolSourceTypeSingleton    = &ast.NamedType{Path: []string{"Bool"}}
	stringSourceTypeSingleton  = &ast.NamedType{Path: []string{"String"}}
)

func floatToIntResultSourceType() ast.Type {
	return &ast.NamedType{
		Path: []string{"Result"},
		Args: []ast.Type{
			intSourceTypeSingleton,
			errorSourceTypeSingleton,
		},
	}
}

func collectStdMathAliases(file *ast.File) map[string]bool {
	out := map[string]bool{}
	if file == nil {
		return out
	}
	for _, use := range file.Uses {
		if use == nil || use.IsFFI() {
			continue
		}
		if len(use.Path) != 2 || use.Path[0] != "std" || use.Path[1] != "math" {
			continue
		}
		alias := use.Alias
		if alias == "" {
			alias = "math"
		}
		out[alias] = true
	}
	return out
}

func (g *generator) stdMathField(expr *ast.FieldExpr) (*ast.FieldExpr, bool) {
	if expr == nil || len(g.stdMathAliases) == 0 {
		return nil, false
	}
	alias, ok := expr.X.(*ast.Ident)
	if !ok || !g.stdMathAliases[alias.Name] {
		return nil, false
	}
	return expr, true
}

func staticStdMathFieldValue(name string) (value, ast.Type, bool) {
	switch name {
	case "PI":
		return value{typ: "double", ref: llvmFloatConstLiteral(3.141592653589793)}, floatSourceTypeSingleton, true
	case "E":
		return value{typ: "double", ref: llvmFloatConstLiteral(2.718281828459045)}, floatSourceTypeSingleton, true
	case "TAU":
		return value{typ: "double", ref: llvmFloatConstLiteral(6.283185307179586)}, floatSourceTypeSingleton, true
	case "INFINITY":
		return value{typ: "double", ref: llvmFloatConstLiteral(math.Inf(1))}, floatSourceTypeSingleton, true
	case "NAN":
		return value{typ: "double", ref: llvmFloatConstLiteral(math.NaN())}, floatSourceTypeSingleton, true
	default:
		return value{}, nil, false
	}
}

func (g *generator) emitStdMathField(expr *ast.FieldExpr) (value, bool, error) {
	field, ok := g.stdMathField(expr)
	if !ok {
		return value{}, false, nil
	}
	out, sourceType, ok := staticStdMathFieldValue(field.Name)
	if !ok {
		return value{}, false, nil
	}
	out.sourceType = sourceType
	return out, true, nil
}

func (g *generator) staticStdMathFieldSourceType(expr *ast.FieldExpr) (ast.Type, bool) {
	field, ok := g.stdMathField(expr)
	if !ok {
		return nil, false
	}
	_, sourceType, ok := staticStdMathFieldValue(field.Name)
	return sourceType, ok
}

func (g *generator) staticStdMathFieldResult(expr *ast.FieldExpr) (value, bool) {
	field, ok := g.stdMathField(expr)
	if !ok {
		return value{}, false
	}
	out, sourceType, ok := staticStdMathFieldValue(field.Name)
	if !ok {
		return value{}, false
	}
	out.sourceType = sourceType
	return out, true
}

func (g *generator) stdMathCallField(call *ast.CallExpr) (*ast.FieldExpr, bool) {
	if call == nil || len(g.stdMathAliases) == 0 {
		return nil, false
	}
	field, ok := fieldExprOfCallFn(call)
	if !ok || field == nil || field.IsOptional {
		return nil, false
	}
	alias, ok := field.X.(*ast.Ident)
	if !ok || !g.stdMathAliases[alias.Name] {
		return nil, false
	}
	return field, true
}

func (g *generator) staticStdMathCallSourceType(call *ast.CallExpr) (ast.Type, bool) {
	field, ok := g.stdMathCallField(call)
	if !ok {
		return nil, false
	}
	switch field.Name {
	case "sin", "cos", "tan",
		"asin", "acos", "atan", "atan2",
		"sinh", "cosh", "tanh",
		"exp", "log", "log2", "log10",
		"sqrt", "cbrt", "pow",
		"floor", "ceil", "round", "trunc",
		"abs", "min", "max", "hypot":
		return floatSourceTypeSingleton, true
	default:
		return nil, false
	}
}

func (g *generator) staticStdMathCallResult(call *ast.CallExpr) (value, bool) {
	sourceType, ok := g.staticStdMathCallSourceType(call)
	if !ok {
		return value{}, false
	}
	return value{typ: "double", sourceType: sourceType}, true
}

func (g *generator) staticFloatMethodSourceType(call *ast.CallExpr) (ast.Type, bool) {
	field, ok := fieldExprOfCallFn(call)
	if !ok || field == nil || field.IsOptional {
		return nil, false
	}
	baseSource, ok := g.staticExprSourceType(field.X)
	if !ok {
		return nil, false
	}
	baseName := namedTypeSingleSegment(baseSource)
	if baseName != "Float" && baseName != "Float32" && baseName != "Float64" {
		return nil, false
	}
	switch field.Name {
	case "abs", "min", "max", "clamp", "signum",
		"floor", "ceil", "round", "trunc", "fract",
		"sqrt", "cbrt", "ln", "log2", "log10", "exp",
		"sin", "cos", "tan", "asin", "acos", "atan",
		"atan2", "pow":
		return baseSource, true
	case "isNaN", "isInfinite", "isFinite":
		return boolSourceTypeSingleton, true
	case "toBits":
		return uint64SourceTypeSingleton, true
	case "toFixed", "toString":
		return stringSourceTypeSingleton, true
	case "toIntTrunc", "toIntRound", "toIntFloor", "toIntCeil":
		return floatToIntResultSourceType(), true
	case "toInt":
		return intSourceTypeSingleton, true
	case "toInt32":
		return int32SourceTypeSingleton, true
	case "toInt64":
		return int64SourceTypeSingleton, true
	case "toFloat":
		return floatSourceTypeSingleton, true
	case "toFloat32":
		return float32SourceTypeSingleton, true
	case "toFloat64":
		return float64SourceTypeSingleton, true
	default:
		return nil, false
	}
}

func (g *generator) staticFloatMethodResult(call *ast.CallExpr) (value, bool) {
	sourceType, ok := g.staticFloatMethodSourceType(call)
	if !ok {
		return value{}, false
	}
	switch namedTypeSingleSegment(sourceType) {
	case "Float", "Float64":
		return value{typ: "double", sourceType: sourceType}, true
	case "Float32":
		return value{typ: "float", sourceType: sourceType}, true
	case "Bool":
		return value{typ: "i1", sourceType: sourceType}, true
	case "String":
		return value{typ: "ptr", gcManaged: true, sourceType: sourceType}, true
	case "UInt64":
		return value{typ: "i64", sourceType: sourceType}, true
	case "Int":
		return value{typ: "i64", sourceType: sourceType}, true
	case "Int32":
		return value{typ: "i32", sourceType: sourceType}, true
	case "Int64":
		return value{typ: "i64", sourceType: sourceType}, true
	case "Result":
		if info, ok := builtinResultTypeFromAST(sourceType, g.typeEnv()); ok {
			if g.resultTypes == nil {
				g.resultTypes = map[string]builtinResultType{}
			}
			g.resultTypes[info.typ] = info
			return value{typ: info.typ, sourceType: sourceType, rootPaths: g.rootPathsForType(info.typ)}, true
		}
	}
	return value{}, false
}

func (g *generator) emitStdMathCall(call *ast.CallExpr) (value, bool, error) {
	field, ok := g.stdMathCallField(call)
	if !ok {
		return value{}, false, nil
	}
	switch field.Name {
	case "sin":
		return g.emitStdMathUnaryCall(call, "sin", ostyRtFloatSinSymbol)
	case "cos":
		return g.emitStdMathUnaryCall(call, "cos", ostyRtFloatCosSymbol)
	case "tan":
		return g.emitStdMathUnaryCall(call, "tan", ostyRtFloatTanSymbol)
	case "asin":
		return g.emitStdMathUnaryCall(call, "asin", ostyRtFloatAsinSymbol)
	case "acos":
		return g.emitStdMathUnaryCall(call, "acos", ostyRtFloatAcosSymbol)
	case "atan":
		return g.emitStdMathUnaryCall(call, "atan", ostyRtFloatAtanSymbol)
	case "atan2":
		return g.emitStdMathBinaryCall(call, "atan2", ostyRtFloatAtan2Symbol)
	case "sinh":
		return g.emitStdMathUnaryCall(call, "sinh", ostyRtFloatSinhSymbol)
	case "cosh":
		return g.emitStdMathUnaryCall(call, "cosh", ostyRtFloatCoshSymbol)
	case "tanh":
		return g.emitStdMathUnaryCall(call, "tanh", ostyRtFloatTanhSymbol)
	case "exp":
		return g.emitStdMathUnaryCall(call, "exp", ostyRtFloatExpSymbol)
	case "log":
		return g.emitStdMathLogCall(call)
	case "log2":
		return g.emitStdMathUnaryCall(call, "log2", ostyRtFloatLog2Symbol)
	case "log10":
		return g.emitStdMathUnaryCall(call, "log10", ostyRtFloatLog10Symbol)
	case "sqrt":
		return g.emitStdMathUnaryCall(call, "sqrt", ostyRtFloatSqrtSymbol)
	case "cbrt":
		return g.emitStdMathUnaryCall(call, "cbrt", ostyRtFloatCbrtSymbol)
	case "pow":
		return g.emitStdMathBinaryCall(call, "pow", ostyRtFloatPowSymbol)
	case "floor":
		return g.emitStdMathUnaryCall(call, "floor", ostyRtFloatFloorSymbol)
	case "ceil":
		return g.emitStdMathUnaryCall(call, "ceil", ostyRtFloatCeilSymbol)
	case "round":
		return g.emitStdMathUnaryCall(call, "round", ostyRtFloatRoundSymbol)
	case "trunc":
		return g.emitStdMathUnaryCall(call, "trunc", ostyRtFloatTruncSymbol)
	case "abs":
		return g.emitStdMathUnaryCall(call, "abs", ostyRtFloatAbsSymbol)
	case "min":
		return g.emitStdMathBinaryCall(call, "min", ostyRtFloatMinSymbol)
	case "max":
		return g.emitStdMathBinaryCall(call, "max", ostyRtFloatMaxSymbol)
	case "hypot":
		return g.emitStdMathBinaryCall(call, "hypot", ostyRtFloatHypotSymbol)
	default:
		return value{}, false, nil
	}
}

func (g *generator) emitFloatMethodCall(call *ast.CallExpr) (value, bool, error) {
	field, ok := fieldExprOfCallFn(call)
	if !ok || field == nil || field.IsOptional {
		return value{}, false, nil
	}
	baseInfo, ok := g.staticExprInfo(field.X)
	if !ok || (baseInfo.typ != "double" && baseInfo.typ != "float") {
		return value{}, false, nil
	}
	baseSource, ok := g.staticExprSourceType(field.X)
	if !ok {
		return value{}, false, nil
	}
	baseName := namedTypeSingleSegment(baseSource)
	if baseName != "Float" && baseName != "Float32" && baseName != "Float64" {
		return value{}, false, nil
	}
	switch field.Name {
	case "abs":
		return g.emitFloatUnaryMethodCall(call, ostyRtFloatAbsSymbol)
	case "min":
		return g.emitFloatBinaryMethodCall(call, ostyRtFloatMinSymbol)
	case "max":
		return g.emitFloatBinaryMethodCall(call, ostyRtFloatMaxSymbol)
	case "clamp":
		return g.emitFloatClampMethodCall(call)
	case "signum":
		return g.emitFloatUnaryMethodCall(call, ostyRtFloatSignumSymbol)
	case "floor":
		return g.emitFloatUnaryMethodCall(call, ostyRtFloatFloorSymbol)
	case "ceil":
		return g.emitFloatUnaryMethodCall(call, ostyRtFloatCeilSymbol)
	case "round":
		return g.emitFloatUnaryMethodCall(call, ostyRtFloatRoundSymbol)
	case "trunc":
		return g.emitFloatUnaryMethodCall(call, ostyRtFloatTruncSymbol)
	case "fract":
		return g.emitFloatUnaryMethodCall(call, ostyRtFloatFractSymbol)
	case "sqrt":
		return g.emitFloatUnaryMethodCall(call, ostyRtFloatSqrtSymbol)
	case "cbrt":
		return g.emitFloatUnaryMethodCall(call, ostyRtFloatCbrtSymbol)
	case "ln":
		return g.emitFloatUnaryMethodCall(call, ostyRtFloatLnSymbol)
	case "log2":
		return g.emitFloatUnaryMethodCall(call, ostyRtFloatLog2Symbol)
	case "log10":
		return g.emitFloatUnaryMethodCall(call, ostyRtFloatLog10Symbol)
	case "exp":
		return g.emitFloatUnaryMethodCall(call, ostyRtFloatExpSymbol)
	case "sin":
		return g.emitFloatUnaryMethodCall(call, ostyRtFloatSinSymbol)
	case "cos":
		return g.emitFloatUnaryMethodCall(call, ostyRtFloatCosSymbol)
	case "tan":
		return g.emitFloatUnaryMethodCall(call, ostyRtFloatTanSymbol)
	case "asin":
		return g.emitFloatUnaryMethodCall(call, ostyRtFloatAsinSymbol)
	case "acos":
		return g.emitFloatUnaryMethodCall(call, ostyRtFloatAcosSymbol)
	case "atan":
		return g.emitFloatUnaryMethodCall(call, ostyRtFloatAtanSymbol)
	case "atan2":
		return g.emitFloatBinaryMethodCall(call, ostyRtFloatAtan2Symbol)
	case "pow":
		return g.emitFloatBinaryMethodCall(call, ostyRtFloatPowSymbol)
	case "isNaN":
		return g.emitFloatPredicateMethodCall(call, ostyRtFloatIsNaNSymbol)
	case "isInfinite":
		return g.emitFloatPredicateMethodCall(call, ostyRtFloatIsInfiniteSymbol)
	case "isFinite":
		return g.emitFloatPredicateMethodCall(call, ostyRtFloatIsFiniteSymbol)
	case "toBits":
		return g.emitFloatToBitsMethodCall(call)
	case "toFixed":
		return g.emitFloatToFixedMethodCall(call)
	case "toIntTrunc":
		return g.emitFloatCheckedIntMethodCall(call, ostyRtFloatToIntTruncSymbol)
	case "toIntRound":
		return g.emitFloatCheckedIntMethodCall(call, ostyRtFloatToIntRoundSymbol)
	case "toIntFloor":
		return g.emitFloatCheckedIntMethodCall(call, ostyRtFloatToIntFloorSymbol)
	case "toIntCeil":
		return g.emitFloatCheckedIntMethodCall(call, ostyRtFloatToIntCeilSymbol)
	case "toInt":
		return g.emitFloatLossyIntMethodCall(call, ostyRtFloatToIntLossySymbol, "i64", intSourceTypeSingleton)
	case "toInt32":
		return g.emitFloatLossyIntMethodCall(call, ostyRtFloatToInt32LossySymbol, "i32", int32SourceTypeSingleton)
	case "toInt64":
		return g.emitFloatLossyIntMethodCall(call, ostyRtFloatToInt64LossySymbol, "i64", int64SourceTypeSingleton)
	case "toFloat":
		return g.emitFloatResizeMethodCall(call, floatSourceTypeSingleton, "double")
	case "toFloat32":
		return g.emitFloatResizeMethodCall(call, float32SourceTypeSingleton, "float")
	case "toFloat64":
		return g.emitFloatResizeMethodCall(call, float64SourceTypeSingleton, "double")
	case "toString":
		return g.emitFloatToStringMethodCall(call)
	default:
		return value{}, false, nil
	}
}

func (g *generator) emitStdMathUnaryCall(call *ast.CallExpr, name, symbol string) (value, bool, error) {
	if len(call.Args) != 1 {
		return value{}, true, unsupportedf("call", "math.%s expects 1 argument, got %d", name, len(call.Args))
	}
	arg, err := g.emitFloatArg(call.Args[0], "math."+name, 0)
	if err != nil {
		return value{}, true, err
	}
	out, err := g.emitUnaryDoubleRuntimeCall(symbol, arg, floatSourceTypeSingleton)
	if err != nil {
		return value{}, true, err
	}
	return out, true, nil
}

func (g *generator) emitStdMathBinaryCall(call *ast.CallExpr, name, symbol string) (value, bool, error) {
	if len(call.Args) != 2 {
		return value{}, true, unsupportedf("call", "math.%s expects 2 arguments, got %d", name, len(call.Args))
	}
	left, err := g.emitFloatArg(call.Args[0], "math."+name, 0)
	if err != nil {
		return value{}, true, err
	}
	right, err := g.emitFloatArg(call.Args[1], "math."+name, 1)
	if err != nil {
		return value{}, true, err
	}
	out, err := g.emitBinaryDoubleRuntimeCall(symbol, left, right, floatSourceTypeSingleton)
	if err != nil {
		return value{}, true, err
	}
	return out, true, nil
}

func (g *generator) emitStdMathLogCall(call *ast.CallExpr) (value, bool, error) {
	if len(call.Args) != 1 && len(call.Args) != 2 {
		return value{}, true, unsupportedf("call", "math.log expects 1 or 2 arguments, got %d", len(call.Args))
	}
	x, err := g.emitFloatArg(call.Args[0], "math.log", 0)
	if err != nil {
		return value{}, true, err
	}
	base := value{typ: "double", ref: "0.0"}
	if len(call.Args) == 2 {
		base, err = g.emitFloatArg(call.Args[1], "math.log", 1)
		if err != nil {
			return value{}, true, err
		}
	}
	out, err := g.emitBinaryDoubleRuntimeCall(ostyRtMathLogSymbol, x, base, floatSourceTypeSingleton)
	if err != nil {
		return value{}, true, err
	}
	return out, true, nil
}

func (g *generator) emitFloatUnaryMethodCall(call *ast.CallExpr, symbol string) (value, bool, error) {
	field, _ := fieldExprOfCallFn(call)
	if len(call.Args) != 0 {
		return value{}, true, unsupportedf("call", "%s takes no arguments, got %d", field.Name, len(call.Args))
	}
	recv, recvTyp, recvSource, err := g.emitFloatMethodReceiver(field)
	if err != nil {
		return value{}, true, err
	}
	out, err := g.emitUnaryDoubleRuntimeCall(symbol, recv, recvSource)
	if err != nil {
		return value{}, true, err
	}
	return g.narrowDoubleResult(out, recvTyp, recvSource)
}

func (g *generator) emitFloatBinaryMethodCall(call *ast.CallExpr, symbol string) (value, bool, error) {
	field, _ := fieldExprOfCallFn(call)
	if len(call.Args) != 1 {
		return value{}, true, unsupportedf("call", "%s expects 1 argument, got %d", field.Name, len(call.Args))
	}
	recv, recvTyp, recvSource, err := g.emitFloatMethodReceiver(field)
	if err != nil {
		return value{}, true, err
	}
	other, err := g.emitFloatArg(call.Args[0], field.Name, 0)
	if err != nil {
		return value{}, true, err
	}
	out, err := g.emitBinaryDoubleRuntimeCall(symbol, recv, other, recvSource)
	if err != nil {
		return value{}, true, err
	}
	return g.narrowDoubleResult(out, recvTyp, recvSource)
}

func (g *generator) emitFloatClampMethodCall(call *ast.CallExpr) (value, bool, error) {
	field, _ := fieldExprOfCallFn(call)
	if len(call.Args) != 2 {
		return value{}, true, unsupportedf("call", "clamp expects 2 arguments, got %d", len(call.Args))
	}
	recv, recvTyp, recvSource, err := g.emitFloatMethodReceiver(field)
	if err != nil {
		return value{}, true, err
	}
	lo, err := g.emitFloatArg(call.Args[0], "clamp", 0)
	if err != nil {
		return value{}, true, err
	}
	hi, err := g.emitFloatArg(call.Args[1], "clamp", 1)
	if err != nil {
		return value{}, true, err
	}
	g.declareRuntimeSymbol(ostyRtFloatClampSymbol, "double", []paramInfo{{typ: "double"}, {typ: "double"}, {typ: "double"}})
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	outRef := llvmCall(emitter, "double", ostyRtFloatClampSymbol, []*LlvmValue{
		toOstyValue(recv),
		toOstyValue(lo),
		toOstyValue(hi),
	})
	g.takeOstyEmitter(emitter)
	out := fromOstyValue(outRef)
	out.sourceType = recvSource
	return g.narrowDoubleResult(out, recvTyp, recvSource)
}

func (g *generator) emitFloatPredicateMethodCall(call *ast.CallExpr, symbol string) (value, bool, error) {
	field, _ := fieldExprOfCallFn(call)
	if len(call.Args) != 0 {
		return value{}, true, unsupportedf("call", "%s takes no arguments, got %d", field.Name, len(call.Args))
	}
	recv, _, _, err := g.emitFloatMethodReceiver(field)
	if err != nil {
		return value{}, true, err
	}
	g.declareRuntimeSymbol(symbol, "i1", []paramInfo{{typ: "double"}})
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	outRef := llvmCall(emitter, "i1", symbol, []*LlvmValue{toOstyValue(recv)})
	g.takeOstyEmitter(emitter)
	out := fromOstyValue(outRef)
	out.sourceType = boolSourceTypeSingleton
	return out, true, nil
}

func (g *generator) emitFloatToBitsMethodCall(call *ast.CallExpr) (value, bool, error) {
	field, _ := fieldExprOfCallFn(call)
	if len(call.Args) != 0 {
		return value{}, true, unsupportedf("call", "toBits takes no arguments, got %d", len(call.Args))
	}
	recv, err := g.emitExpr(field.X)
	if err != nil {
		return value{}, true, err
	}
	recv, err = g.loadIfPointer(recv)
	if err != nil {
		return value{}, true, err
	}
	symbol := ostyRtFloatToBits64Symbol
	params := []paramInfo{{typ: "double"}}
	if recv.typ == "float" {
		symbol = ostyRtFloatToBits32Symbol
		params = []paramInfo{{typ: "float"}}
	} else if recv.typ != "double" {
		return value{}, true, unsupportedf("type-system", "toBits receiver type %s, want Float/Float32/Float64", recv.typ)
	}
	g.declareRuntimeSymbol(symbol, "i64", params)
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	outRef := llvmCall(emitter, "i64", symbol, []*LlvmValue{toOstyValue(recv)})
	g.takeOstyEmitter(emitter)
	out := fromOstyValue(outRef)
	out.sourceType = uint64SourceTypeSingleton
	return out, true, nil
}

func (g *generator) emitFloatToFixedMethodCall(call *ast.CallExpr) (value, bool, error) {
	field, _ := fieldExprOfCallFn(call)
	if len(call.Args) != 1 {
		return value{}, true, unsupportedf("call", "toFixed expects 1 argument, got %d", len(call.Args))
	}
	recv, _, _, err := g.emitFloatMethodReceiver(field)
	if err != nil {
		return value{}, true, err
	}
	precision, err := g.emitIntArg(call.Args[0], "toFixed", 0)
	if err != nil {
		return value{}, true, err
	}
	g.declareRuntimeSymbol(ostyRtFloatToFixedSymbol, "ptr", []paramInfo{{typ: "double"}, {typ: "i64"}})
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	outRef := llvmCall(emitter, "ptr", ostyRtFloatToFixedSymbol, []*LlvmValue{
		toOstyValue(recv),
		toOstyValue(precision),
	})
	g.takeOstyEmitter(emitter)
	out := fromOstyValue(outRef)
	out.gcManaged = true
	out.sourceType = stringSourceTypeSingleton
	out.rootPaths = g.rootPathsForType(out.typ)
	return out, true, nil
}

func (g *generator) emitFloatCheckedIntMethodCall(call *ast.CallExpr, symbol string) (value, bool, error) {
	field, _ := fieldExprOfCallFn(call)
	if len(call.Args) != 0 {
		return value{}, true, unsupportedf("call", "%s takes no arguments, got %d", field.Name, len(call.Args))
	}
	recv, _, _, err := g.emitFloatMethodReceiver(field)
	if err != nil {
		return value{}, true, err
	}
	out, err := g.emitFloatCheckedIntResultCall(field.Name, recv, symbol)
	if err != nil {
		return value{}, true, err
	}
	return out, true, nil
}

func (g *generator) emitFloatLossyIntMethodCall(call *ast.CallExpr, symbol, retTyp string, sourceType ast.Type) (value, bool, error) {
	field, _ := fieldExprOfCallFn(call)
	if len(call.Args) != 0 {
		return value{}, true, unsupportedf("call", "%s takes no arguments, got %d", field.Name, len(call.Args))
	}
	recv, _, _, err := g.emitFloatMethodReceiver(field)
	if err != nil {
		return value{}, true, err
	}
	g.declareRuntimeSymbol(symbol, retTyp, []paramInfo{{typ: "double"}})
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	outRef := llvmCall(emitter, retTyp, symbol, []*LlvmValue{toOstyValue(recv)})
	g.takeOstyEmitter(emitter)
	out := fromOstyValue(outRef)
	out.sourceType = sourceType
	return out, true, nil
}

func (g *generator) emitFloatResizeMethodCall(call *ast.CallExpr, sourceType ast.Type, wantTyp string) (value, bool, error) {
	field, _ := fieldExprOfCallFn(call)
	if len(call.Args) != 0 {
		return value{}, true, unsupportedf("call", "%s takes no arguments, got %d", field.Name, len(call.Args))
	}
	recv, err := g.emitExpr(field.X)
	if err != nil {
		return value{}, true, err
	}
	recv, err = g.loadIfPointer(recv)
	if err != nil {
		return value{}, true, err
	}
	switch {
	case recv.typ == wantTyp:
		recv.sourceType = sourceType
		return recv, true, nil
	case recv.typ == "float" && wantTyp == "double":
		emitter := g.toOstyEmitter()
		tmp := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, mirFPExtFloatToDoubleLine(tmp, recv.ref))
		g.takeOstyEmitter(emitter)
		return value{typ: "double", ref: tmp, sourceType: sourceType}, true, nil
	case recv.typ == "double" && wantTyp == "float":
		emitter := g.toOstyEmitter()
		tmp := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, mirFPTruncDoubleToFloatLine(tmp, recv.ref))
		g.takeOstyEmitter(emitter)
		return value{typ: "float", ref: tmp, sourceType: sourceType}, true, nil
	default:
		return value{}, true, unsupportedf("type-system", "%s receiver type %s, want float-compatible value", field.Name, recv.typ)
	}
}

func (g *generator) emitFloatToStringMethodCall(call *ast.CallExpr) (value, bool, error) {
	field, _ := fieldExprOfCallFn(call)
	if len(call.Args) != 0 {
		return value{}, true, unsupportedf("call", "toString takes no arguments, got %d", len(call.Args))
	}
	recv, _, _, err := g.emitFloatMethodReceiver(field)
	if err != nil {
		return value{}, true, err
	}
	out, err := g.emitRuntimeFloatToString(recv)
	if err != nil {
		return value{}, true, err
	}
	return out, true, nil
}

func (g *generator) emitFloatMethodReceiver(field *ast.FieldExpr) (value, string, ast.Type, error) {
	recv, err := g.emitExpr(field.X)
	if err != nil {
		return value{}, "", nil, err
	}
	recv, err = g.loadIfPointer(recv)
	if err != nil {
		return value{}, "", nil, err
	}
	sourceType, ok := g.staticExprSourceType(field.X)
	if !ok {
		return value{}, "", nil, unsupported("type-system", "float method receiver type is unknown")
	}
	recvTyp := recv.typ
	recv, err = g.floatValueAsDouble(recv)
	if err != nil {
		return value{}, "", nil, err
	}
	return recv, recvTyp, sourceType, nil
}

func (g *generator) emitFloatArg(arg *ast.Arg, owner string, index int) (value, error) {
	if arg == nil || arg.Value == nil {
		return value{}, unsupportedf("call", "%s arg %d is missing", owner, index+1)
	}
	v, err := g.emitExpr(arg.Value)
	if err != nil {
		return value{}, err
	}
	v, err = g.loadIfPointer(v)
	if err != nil {
		return value{}, err
	}
	return g.floatValueAsDouble(v)
}

func (g *generator) emitIntArg(arg *ast.Arg, owner string, index int) (value, error) {
	if arg == nil || arg.Value == nil {
		return value{}, unsupportedf("call", "%s arg %d is missing", owner, index+1)
	}
	v, err := g.emitExpr(arg.Value)
	if err != nil {
		return value{}, err
	}
	v, err = g.loadIfPointer(v)
	if err != nil {
		return value{}, err
	}
	if v.typ != "i64" {
		return value{}, unsupportedf("type-system", "%s arg %d type %s, want Int", owner, index+1, v.typ)
	}
	return v, nil
}

func (g *generator) floatValueAsDouble(v value) (value, error) {
	switch v.typ {
	case "double":
		return v, nil
	case "float":
		emitter := g.toOstyEmitter()
		tmp := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, mirFPExtFloatToDoubleLine(tmp, v.ref))
		g.takeOstyEmitter(emitter)
		return value{typ: "double", ref: tmp}, nil
	default:
		return value{}, unsupportedf("type-system", "float-like value type %s, want Float/Float32/Float64", v.typ)
	}
}

func (g *generator) narrowDoubleResult(v value, wantTyp string, sourceType ast.Type) (value, bool, error) {
	if wantTyp == "double" {
		v.sourceType = sourceType
		return v, true, nil
	}
	if wantTyp != "float" {
		return value{}, true, unsupportedf("type-system", "cannot narrow double result to %s", wantTyp)
	}
	emitter := g.toOstyEmitter()
	tmp := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirFPTruncDoubleToFloatLine(tmp, v.ref))
	g.takeOstyEmitter(emitter)
	return value{typ: "float", ref: tmp, sourceType: sourceType}, true, nil
}

func (g *generator) emitUnaryDoubleRuntimeCall(symbol string, arg value, sourceType ast.Type) (value, error) {
	g.declareRuntimeSymbol(symbol, "double", []paramInfo{{typ: "double"}})
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	outRef := llvmCall(emitter, "double", symbol, []*LlvmValue{toOstyValue(arg)})
	g.takeOstyEmitter(emitter)
	out := fromOstyValue(outRef)
	out.sourceType = sourceType
	return out, nil
}

func (g *generator) emitBinaryDoubleRuntimeCall(symbol string, left, right value, sourceType ast.Type) (value, error) {
	g.declareRuntimeSymbol(symbol, "double", []paramInfo{{typ: "double"}, {typ: "double"}})
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	outRef := llvmCall(emitter, "double", symbol, []*LlvmValue{toOstyValue(left), toOstyValue(right)})
	g.takeOstyEmitter(emitter)
	out := fromOstyValue(outRef)
	out.sourceType = sourceType
	return out, nil
}

func (g *generator) emitFloatCheckedIntResultCall(prefix string, arg value, symbol string) (value, error) {
	sourceType := floatToIntResultSourceType()
	info, ok := builtinResultTypeFromAST(sourceType, g.typeEnv())
	if !ok {
		return value{}, unsupportedf("type-system", "%s Result<Int, Error> type is unavailable", prefix)
	}
	if g.resultTypes == nil {
		g.resultTypes = map[string]builtinResultType{}
	}
	g.resultTypes[info.typ] = info
	if info.okTyp != "i64" || info.errTyp != "ptr" {
		return value{}, unsupportedf("type-system", "%s currently needs Result<i64, ptr>, got ok=%s err=%s", prefix, info.okTyp, info.errTyp)
	}
	g.declareRuntimeSymbol(symbol, "ptr", []paramInfo{{typ: "double"}, {typ: "ptr"}})
	emitter := g.toOstyEmitter()
	outSlot := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirAllocaText(outSlot, "i64"))
	emitter.body = append(emitter.body, mirStoreText("i64", "0", outSlot))
	g.emitCallSafepointIfNeeded(emitter)
	errText := llvmCall(emitter, "ptr", symbol, []*LlvmValue{
		toOstyValue(arg),
		{typ: "ptr", name: outSlot},
	})
	failed := llvmCompare(emitter, "ne", errText, toOstyValue(value{typ: "ptr", ref: "null"}))
	errLabel := llvmNextLabel(emitter, llvmBuiltinAggregatePart(prefix)+".err")
	okLabel := llvmNextLabel(emitter, llvmBuiltinAggregatePart(prefix)+".ok")
	contLabel := llvmNextLabel(emitter, llvmBuiltinAggregatePart(prefix)+".cont")
	emitter.body = append(emitter.body, "  br i1 "+failed.name+", label %"+errLabel+", label %"+okLabel)

	emitter.body = append(emitter.body, errLabel+":")
	errResult := llvmStructLiteral(emitter, info.typ, []*LlvmValue{
		toOstyValue(value{typ: "i64", ref: "1"}),
		toOstyValue(llvmZeroValue(info.okTyp)),
		errText,
	})
	emitter.body = append(emitter.body, "  br label %"+contLabel)

	emitter.body = append(emitter.body, okLabel+":")
	okValue := g.loadValueFromAddress(emitter, "i64", outSlot)
	okResult := llvmStructLiteral(emitter, info.typ, []*LlvmValue{
		toOstyValue(value{typ: "i64", ref: "0"}),
		toOstyValue(okValue),
		toOstyValue(llvmZeroValue(info.errTyp)),
	})
	emitter.body = append(emitter.body, "  br label %"+contLabel)

	emitter.body = append(emitter.body, contLabel+":")
	phi := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, "  "+phi+" = phi "+info.typ+" [ "+errResult.name+", %"+errLabel+" ], [ "+okResult.name+", %"+okLabel+" ]")
	g.takeOstyEmitter(emitter)
	out := value{
		typ:        info.typ,
		ref:        phi,
		sourceType: sourceType,
		rootPaths:  g.rootPathsForType(info.typ),
	}
	return out, nil
}
