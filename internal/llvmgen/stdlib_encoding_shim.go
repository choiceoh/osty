package llvmgen

import (
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/mir"
)

// std.encoding shim routes stdlib encoding methods through the runtime until
// the Osty bodies in internal/stdlib/modules/encoding.osty are fully lowerable
// by the LLVM backend. Keep the runtime ABI in stdEncodingRuntimeLowerings so
// AST and MIR dispatch do not grow separate prefix tables as codecs are added.

const (
	ostyRtBytesToHexSymbol           = "osty_rt_bytes_to_hex"
	ostyRtBytesFromHexSymbol         = "osty_rt_bytes_from_hex"
	ostyRtBytesIsValidHexSymbol      = "osty_rt_bytes_is_valid_hex"
	ostyRtEncodingBase64EncodeSymbol = "osty_rt_encoding_base64_encode"
	ostyRtEncodingBase64UrlEncSymbol = "osty_rt_encoding_base64url_encode"
)

type stdEncodingRuntimeDecodeKind int

const (
	stdEncodingDecodeUnsupported stdEncodingRuntimeDecodeKind = iota
	stdEncodingDecodeHexBytesResult
)

type stdEncodingRuntimeLowering struct {
	Variant              string
	ASTPath              []string
	MIRPrefix            string
	EncodeSymbol         string
	DecodeMIRKind        stdEncodingRuntimeDecodeKind
	DecodeMIRUnsupported string
	DecodeASTUnsupported string
}

var stdEncodingRuntimeLowerings = []stdEncodingRuntimeLowering{
	{
		Variant:       "hex",
		ASTPath:       []string{"hex"},
		MIRPrefix:     "Hex__",
		EncodeSymbol:  ostyRtBytesToHexSymbol,
		DecodeMIRKind: stdEncodingDecodeHexBytesResult,
	},
	{
		Variant:              "base64",
		ASTPath:              []string{"base64"},
		MIRPrefix:            "Base64__",
		EncodeSymbol:         ostyRtEncodingBase64EncodeSymbol,
		DecodeMIRUnsupported: "encoding.base64.decode requires AST lowering route (MIR Result<Bytes, Error> handling pending)",
		DecodeASTUnsupported: "encoding.base64.decode requires MIR Result<Bytes, Error> handling (separate cycle)",
	},
	{
		Variant:              "base64url",
		ASTPath:              []string{"base64", "url"},
		MIRPrefix:            "Base64Url__",
		EncodeSymbol:         ostyRtEncodingBase64UrlEncSymbol,
		DecodeMIRUnsupported: "encoding.base64url.decode requires AST lowering route (MIR Result<Bytes, Error> handling pending)",
		DecodeASTUnsupported: "encoding.base64url.decode requires MIR Result<Bytes, Error> handling (separate cycle)",
	},
}

func stdEncodingRuntimeLoweringByVariant(variant string) (*stdEncodingRuntimeLowering, bool) {
	for i := range stdEncodingRuntimeLowerings {
		if stdEncodingRuntimeLowerings[i].Variant == variant {
			return &stdEncodingRuntimeLowerings[i], true
		}
	}
	return nil, false
}

func stdEncodingRuntimeLoweringForMIRSymbol(symbol string) (*stdEncodingRuntimeLowering, string, bool) {
	for i := range stdEncodingRuntimeLowerings {
		spec := &stdEncodingRuntimeLowerings[i]
		if strings.HasPrefix(symbol, spec.MIRPrefix) {
			return spec, strings.TrimPrefix(symbol, spec.MIRPrefix), true
		}
	}
	return nil, "", false
}

func collectStdEncodingAliases(file *ast.File) map[string]bool {
	out := map[string]bool{}
	if file == nil {
		return out
	}
	for _, use := range file.Uses {
		if use == nil || use.IsFFI() {
			continue
		}
		if len(use.Path) != 2 || use.Path[0] != "std" || use.Path[1] != "encoding" {
			continue
		}
		alias := use.Alias
		if alias == "" {
			alias = "encoding"
		}
		out[alias] = true
	}
	return out
}

// stdEncodingHexCallInfo matches `<encoding-alias>.hex.<method>(...)`.
func (g *generator) stdEncodingHexCallInfo(call *ast.CallExpr) (*ast.FieldExpr, bool) {
	field, spec, ok := g.stdEncodingRuntimeCallInfo(call)
	return field, ok && spec != nil && spec.Variant == "hex"
}

func (g *generator) stdEncodingBase64CallInfo(call *ast.CallExpr) (*ast.FieldExpr, string, bool) {
	field, spec, ok := g.stdEncodingRuntimeCallInfo(call)
	if !ok || spec == nil || (spec.Variant != "base64" && spec.Variant != "base64url") {
		return nil, "", false
	}
	return field, spec.Variant, true
}

func (g *generator) stdEncodingRuntimeCallInfo(call *ast.CallExpr) (*ast.FieldExpr, *stdEncodingRuntimeLowering, bool) {
	for i := range stdEncodingRuntimeLowerings {
		spec := &stdEncodingRuntimeLowerings[i]
		field, ok := g.stdEncodingMethodInfo(call, spec.ASTPath...)
		if ok {
			return field, spec, true
		}
	}
	return nil, nil, false
}

func (g *generator) stdEncodingMethodInfo(call *ast.CallExpr, path ...string) (*ast.FieldExpr, bool) {
	if call == nil || len(g.stdEncodingAliases) == 0 || len(path) == 0 {
		return nil, false
	}
	field, ok := fieldExprOfCallFn(call)
	if !ok || field.IsOptional {
		return nil, false
	}
	switch field.Name {
	case "encode", "decode":
	default:
		return nil, false
	}
	var x ast.Expr = field.X
	for i := len(path) - 1; i >= 0; i-- {
		pathField, ok := x.(*ast.FieldExpr)
		if !ok || pathField == nil || pathField.IsOptional || pathField.Name != path[i] {
			return nil, false
		}
		x = pathField.X
	}
	alias, ok := x.(*ast.Ident)
	if !ok || alias == nil || !g.stdEncodingAliases[alias.Name] {
		return nil, false
	}
	return field, true
}

func hexDecodeResultSourceType() ast.Type {
	return &ast.NamedType{
		Path: []string{"Result"},
		Args: []ast.Type{
			&ast.NamedType{Path: []string{"Bytes"}},
			&ast.NamedType{Path: []string{"Error"}},
		},
	}
}

func (g *generator) emitStdEncodingCall(call *ast.CallExpr) (value, bool, error) {
	field, spec, ok := g.stdEncodingRuntimeCallInfo(call)
	if !ok || spec == nil {
		return value{}, false, nil
	}
	switch field.Name {
	case "encode":
		if len(call.Args) != 1 {
			return value{}, true, unsupportedf("call", "encoding.%s.encode expects 1 argument, got %d", spec.Variant, len(call.Args))
		}
		data, err := g.emitStdBytesArg(call.Args[0], "encoding."+spec.Variant+".encode", 0)
		if err != nil {
			return value{}, true, err
		}
		out, err := g.emitStdEncodingEncodeRuntime(data, spec.EncodeSymbol)
		return out, true, err
	case "decode":
		if len(call.Args) != 1 {
			return value{}, true, unsupportedf("call", "encoding.%s.decode expects 1 argument, got %d", spec.Variant, len(call.Args))
		}
		if spec.DecodeASTUnsupported != "" {
			return value{}, true, unsupported("call", spec.DecodeASTUnsupported)
		}
		text, err := g.emitStdStringsArg(call.Args[0], "encoding."+spec.Variant+".decode", 0)
		if err != nil {
			return value{}, true, err
		}
		out, err := g.emitEncodingHexDecodeResult(text)
		return out, true, err
	}
	return value{}, false, nil
}

func (g *generator) stdEncodingCallStaticResult(call *ast.CallExpr) (value, bool) {
	field, _, ok := g.stdEncodingRuntimeCallInfo(call)
	if !ok {
		return value{}, false
	}
	switch field.Name {
	case "encode":
		return value{typ: "ptr", gcManaged: true, sourceType: &ast.NamedType{Path: []string{"String"}}}, true
	case "decode":
		if info, ok := builtinResultTypeFromAST(hexDecodeResultSourceType(), g.typeEnv()); ok {
			return value{typ: info.typ, sourceType: hexDecodeResultSourceType(), rootPaths: g.rootPathsForType(info.typ)}, true
		}
		return value{}, false
	}
	return value{}, false
}

func (g *generator) staticStdEncodingCallSourceType(call *ast.CallExpr) (ast.Type, bool) {
	field, _, ok := g.stdEncodingRuntimeCallInfo(call)
	if !ok {
		return nil, false
	}
	switch field.Name {
	case "encode":
		return &ast.NamedType{Path: []string{"String"}}, true
	case "decode":
		return hexDecodeResultSourceType(), true
	}
	return nil, false
}

func (g *generator) emitStdEncodingEncodeRuntime(data value, symbol string) (value, error) {
	g.declareRuntimeSymbol(symbol, "ptr", []paramInfo{{typ: "ptr"}})
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, "ptr", symbol, []*LlvmValue{toOstyValue(data)})
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.gcManaged = true
	v.sourceType = &ast.NamedType{Path: []string{"String"}}
	return v, nil
}

func (g *generator) emitEncodingHexEncodeRuntime(data value) (value, error) {
	return g.emitStdEncodingEncodeRuntime(data, ostyRtBytesToHexSymbol)
}

func (g *generator) emitEncodingBase64EncodeRuntime(data value, urlSafe bool) (value, error) {
	variant := "base64"
	if urlSafe {
		variant = "base64url"
	}
	spec, ok := stdEncodingRuntimeLoweringByVariant(variant)
	if !ok {
		return value{}, unsupported("call", "missing std.encoding runtime lowering for "+variant)
	}
	return g.emitStdEncodingEncodeRuntime(data, spec.EncodeSymbol)
}

func (g *generator) emitEncodingHexDecodeResult(text value) (value, error) {
	sourceType := hexDecodeResultSourceType()
	info, ok := builtinResultTypeFromAST(sourceType, g.typeEnv())
	if !ok {
		return value{}, unsupported("type-system", "encoding.hex.decode Result<Bytes, Error> type is unavailable")
	}
	if g.resultTypes == nil {
		g.resultTypes = map[string]builtinResultType{}
	}
	g.resultTypes[info.typ] = info
	g.declareRuntimeSymbol(ostyRtBytesIsValidHexSymbol, "i1", []paramInfo{{typ: "ptr"}})
	g.declareRuntimeSymbol(ostyRtBytesFromHexSymbol, "ptr", []paramInfo{{typ: "ptr"}})

	// `osty_rt_bytes_from_hex` aborts on invalid input, so we must
	// validate before calling. The same pattern as `bytes.fromHex`
	// (`emitBytesFromHexResult`).
	emitter := g.toOstyEmitter()
	valid := llvmCall(emitter, "i1", ostyRtBytesIsValidHexSymbol, []*LlvmValue{toOstyValue(text)})
	labels := llvmIfExprStart(emitter, valid)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.thenLabel

	emitter = g.toOstyEmitter()
	decoded := llvmCall(emitter, "ptr", ostyRtBytesFromHexSymbol, []*LlvmValue{toOstyValue(text)})
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

// emitStdEncodingBase64CallMIR is kept as the base64-specific entry point used
// by mir_generator.go; the actual ABI selection is table-driven.
func (g *mirGen) emitStdEncodingBase64CallMIR(c *mir.CallInstr, fnRef *mir.FnRef) (bool, error) {
	return g.emitStdEncodingRuntimeCallMIR(c, fnRef)
}

// emitStdEncodingCallMIR is the MIR-level dispatch for std.encoding methods.
func (g *mirGen) emitStdEncodingCallMIR(c *mir.CallInstr, fnRef *mir.FnRef) (bool, error) {
	return g.emitStdEncodingRuntimeCallMIR(c, fnRef)
}

func (g *mirGen) emitStdEncodingRuntimeCallMIR(c *mir.CallInstr, fnRef *mir.FnRef) (bool, error) {
	spec, method, ok := stdEncodingRuntimeLoweringForMIRSymbol(fnRef.Symbol)
	if !ok || spec == nil {
		return false, nil
	}
	switch method {
	case "encode", "decode":
	default:
		return false, nil
	}
	args := c.Args
	if len(args) == 0 {
		return true, unsupported("mir-mvp", "encoding."+spec.Variant+" method without receiver")
	}
	args = args[1:]
	switch method {
	case "encode":
		return true, g.emitStdEncodingEncodeCallMIR(c, spec, args)
	case "decode":
		return true, g.emitStdEncodingDecodeCallMIR(c, spec, args)
	}
	return false, nil
}

func (g *mirGen) emitStdEncodingEncodeCallMIR(c *mir.CallInstr, spec *stdEncodingRuntimeLowering, args []mir.Operand) error {
	if len(args) != 1 {
		return unsupported("mir-mvp", "encoding."+spec.Variant+".encode requires one Bytes argument")
	}
	data, err := g.evalBytesArg(args[0], "encoding."+spec.Variant+".encode", 0)
	if err != nil {
		return err
	}
	return g.emitRuntimeCallToDest(c, spec.EncodeSymbol, "ptr", []mirRuntimeArg{data})
}

func (g *mirGen) emitStdEncodingDecodeCallMIR(c *mir.CallInstr, spec *stdEncodingRuntimeLowering, args []mir.Operand) error {
	if spec.DecodeMIRKind == stdEncodingDecodeUnsupported {
		return unsupported("mir-mvp", spec.DecodeMIRUnsupported)
	}
	if len(args) != 1 {
		return unsupported("mir-mvp", "encoding."+spec.Variant+".decode requires one String argument")
	}
	text, err := g.evalStringArg(args[0], "encoding."+spec.Variant+".decode", 0)
	if err != nil {
		return err
	}
	switch spec.DecodeMIRKind {
	case stdEncodingDecodeHexBytesResult:
		return g.emitEncodingHexDecodeMIR(c, text)
	default:
		return unsupported("mir-mvp", "encoding."+spec.Variant+".decode has no MIR lowering strategy")
	}
}

// emitEncodingHexDecodeMIR validates input first (since
// `osty_rt_bytes_from_hex` aborts on invalid hex) then constructs the
// `Result<Bytes, Error>` struct directly. Mirrors `emitBytesFromHexResult`
// at the AST layer; cannot use `emitPtrResultFromNullable` because the
// runtime call itself aborts rather than returning null.
func (g *mirGen) emitEncodingHexDecodeMIR(c *mir.CallInstr, text mirRuntimeArg) error {
	if c.Dest == nil {
		// No dest; caller drops the result. Still validate to honor
		// the no-abort contract; from_hex itself is skipped.
		g.declareRuntime(ostyRtBytesIsValidHexSymbol, mirRuntimeDeclareLine("i1", ostyRtBytesIsValidHexSymbol, "ptr"))
		g.fnBuf.WriteString(mirCallStmtLine("i1", ostyRtBytesIsValidHexSymbol, mirRuntimeArgList([]mirRuntimeArg{text})))
		return nil
	}
	destLoc := g.fn.Local(c.Dest.Local)
	if destLoc == nil {
		return unsupported("mir-mvp", "encoding.hex.decode dest into unknown local")
	}
	resultLLVM := g.llvmType(destLoc.Type)

	g.declareRuntime(ostyRtBytesIsValidHexSymbol, mirRuntimeDeclareLine("i1", ostyRtBytesIsValidHexSymbol, "ptr"))
	g.declareRuntime(ostyRtBytesFromHexSymbol, mirRuntimeDeclareLine("ptr", ostyRtBytesFromHexSymbol, "ptr"))

	valid := g.fresh()
	g.fnBuf.WriteString(mirCallValueLine(valid, "i1", ostyRtBytesIsValidHexSymbol, mirRuntimeArgList([]mirRuntimeArg{text})))

	okLabel := g.freshLabel("hex.decode.ok")
	errLabel := g.freshLabel("hex.decode.err")
	contLabel := g.freshLabel("hex.decode.cont")
	g.fnBuf.WriteString(mirBrCondLine(valid, okLabel, errLabel))

	// Ok arm: call from_hex (safe; validated above), wrap in Ok struct.
	g.fnBuf.WriteString(mirLabelLine(okLabel))
	decoded := g.fresh()
	g.fnBuf.WriteString(mirCallValueLine(decoded, "ptr", ostyRtBytesFromHexSymbol, mirRuntimeArgList([]mirRuntimeArg{text})))
	okPayload, err := g.toI64Slot(decoded, mir.TBytes)
	if err != nil {
		return err
	}
	okValue := g.emitResultValue(resultLLVM, true, okPayload)
	g.fnBuf.WriteString(mirBrUncondLine(contLabel))

	// Err arm: zero payload (no error data; matches bytes.fromHex).
	g.fnBuf.WriteString(mirLabelLine(errLabel))
	errValue := g.emitResultValue(resultLLVM, false, "0")
	g.fnBuf.WriteString(mirBrUncondLine(contLabel))

	g.fnBuf.WriteString(mirLabelLine(contLabel))
	phi := g.fresh()
	g.fnBuf.WriteString(mirPhiTwoLine(phi, resultLLVM, okValue, okLabel, errValue, errLabel))
	g.fnBuf.WriteString(mirStoreLine(resultLLVM, phi, g.localSlots[c.Dest.Local]))
	return nil
}
