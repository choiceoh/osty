package llvmgen

import (
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/mir"
)

// std.encoding shim — intercepts the `encoding.hex.{encode,decode}`
// instance-method dispatch at the LLVM AST + MIR level. The Osty body
// in `internal/stdlib/modules/encoding.osty` defines `Hex` / `Base64` /
// `UrlEncoding` structs whose methods loop over `data.get(i)` with
// recursive private helpers — none of which the LLVM backend can lower
// today. This shim routes the hex method calls to the runtime functions
// already present (`osty_rt_bytes_{to,from,is_valid}_hex`); base64 and
// url paths are intentionally left for follow-up cycles that add the
// missing C runtime entries (Phase 2 / Phase 3).

const (
	ostyRtBytesToHexSymbol           = "osty_rt_bytes_to_hex"
	ostyRtBytesFromHexSymbol         = "osty_rt_bytes_from_hex"
	ostyRtBytesIsValidHexSymbol      = "osty_rt_bytes_is_valid_hex"
	ostyRtEncodingBase64EncodeSymbol = "osty_rt_encoding_base64_encode"
	ostyRtEncodingBase64UrlEncSymbol = "osty_rt_encoding_base64url_encode"
)

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
	return g.stdEncodingMethodInfo(call, "hex")
}

func (g *generator) stdEncodingBase64CallInfo(call *ast.CallExpr) (*ast.FieldExpr, string, bool) {
	if field, ok := g.stdEncodingMethodInfo(call, "base64"); ok {
		return field, "base64", true
	}
	// Nested form: `encoding.base64.url.<method>(...)`.
	field, ok := fieldExprOfCallFn(call)
	if !ok || field.IsOptional {
		return nil, "", false
	}
	urlField, ok := field.X.(*ast.FieldExpr)
	if !ok || urlField == nil || urlField.IsOptional || urlField.Name != "url" {
		return nil, "", false
	}
	base64Field, ok := urlField.X.(*ast.FieldExpr)
	if !ok || base64Field == nil || base64Field.IsOptional || base64Field.Name != "base64" {
		return nil, "", false
	}
	alias, ok := base64Field.X.(*ast.Ident)
	if !ok || alias == nil || !g.stdEncodingAliases[alias.Name] {
		return nil, "", false
	}
	switch field.Name {
	case "encode", "decode":
		return field, "base64url", true
	}
	return nil, "", false
}

func (g *generator) stdEncodingMethodInfo(call *ast.CallExpr, sub string) (*ast.FieldExpr, bool) {
	if call == nil || len(g.stdEncodingAliases) == 0 {
		return nil, false
	}
	field, ok := fieldExprOfCallFn(call)
	if !ok || field.IsOptional {
		return nil, false
	}
	subField, ok := field.X.(*ast.FieldExpr)
	if !ok || subField == nil || subField.IsOptional || subField.Name != sub {
		return nil, false
	}
	alias, ok := subField.X.(*ast.Ident)
	if !ok || alias == nil || !g.stdEncodingAliases[alias.Name] {
		return nil, false
	}
	switch field.Name {
	case "encode", "decode":
		return field, true
	default:
		return nil, false
	}
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
	if field, ok := g.stdEncodingHexCallInfo(call); ok {
		switch field.Name {
		case "encode":
			if len(call.Args) != 1 {
				return value{}, true, unsupportedf("call", "encoding.hex.encode expects 1 argument, got %d", len(call.Args))
			}
			data, err := g.emitStdBytesArg(call.Args[0], "encoding.hex.encode", 0)
			if err != nil {
				return value{}, true, err
			}
			out, err := g.emitEncodingHexEncodeRuntime(data)
			return out, true, err
		case "decode":
			if len(call.Args) != 1 {
				return value{}, true, unsupportedf("call", "encoding.hex.decode expects 1 argument, got %d", len(call.Args))
			}
			text, err := g.emitStdStringsArg(call.Args[0], "encoding.hex.decode", 0)
			if err != nil {
				return value{}, true, err
			}
			out, err := g.emitEncodingHexDecodeResult(text)
			return out, true, err
		}
	}
	if field, variant, ok := g.stdEncodingBase64CallInfo(call); ok {
		switch field.Name {
		case "encode":
			if len(call.Args) != 1 {
				return value{}, true, unsupportedf("call", "encoding.%s.encode expects 1 argument, got %d", variant, len(call.Args))
			}
			data, err := g.emitStdBytesArg(call.Args[0], "encoding."+variant+".encode", 0)
			if err != nil {
				return value{}, true, err
			}
			out, err := g.emitEncodingBase64EncodeRuntime(data, variant == "base64url")
			return out, true, err
		case "decode":
			// AST-route decode for base64 awaits the same Result<Bytes,
			// Error>-via-MIR fix as hex.decode (separate cycle).
			return value{}, true, unsupportedf("call", "encoding.%s.decode requires MIR Result<Bytes, Error> handling (separate cycle)", variant)
		}
	}
	return value{}, false, nil
}

func (g *generator) stdEncodingCallStaticResult(call *ast.CallExpr) (value, bool) {
	if field, ok := g.stdEncodingHexCallInfo(call); ok {
		switch field.Name {
		case "encode":
			return value{typ: "ptr", gcManaged: true, sourceType: &ast.NamedType{Path: []string{"String"}}}, true
		case "decode":
			if info, ok := builtinResultTypeFromAST(hexDecodeResultSourceType(), g.typeEnv()); ok {
				return value{typ: info.typ, sourceType: hexDecodeResultSourceType(), rootPaths: g.rootPathsForType(info.typ)}, true
			}
			return value{}, false
		}
	}
	if field, _, ok := g.stdEncodingBase64CallInfo(call); ok {
		switch field.Name {
		case "encode":
			return value{typ: "ptr", gcManaged: true, sourceType: &ast.NamedType{Path: []string{"String"}}}, true
		case "decode":
			if info, ok := builtinResultTypeFromAST(hexDecodeResultSourceType(), g.typeEnv()); ok {
				return value{typ: info.typ, sourceType: hexDecodeResultSourceType(), rootPaths: g.rootPathsForType(info.typ)}, true
			}
			return value{}, false
		}
	}
	return value{}, false
}

func (g *generator) staticStdEncodingCallSourceType(call *ast.CallExpr) (ast.Type, bool) {
	if field, ok := g.stdEncodingHexCallInfo(call); ok {
		switch field.Name {
		case "encode":
			return &ast.NamedType{Path: []string{"String"}}, true
		case "decode":
			return hexDecodeResultSourceType(), true
		}
	}
	if field, _, ok := g.stdEncodingBase64CallInfo(call); ok {
		switch field.Name {
		case "encode":
			return &ast.NamedType{Path: []string{"String"}}, true
		case "decode":
			return hexDecodeResultSourceType(), true
		}
	}
	return nil, false
}

func (g *generator) emitEncodingHexEncodeRuntime(data value) (value, error) {
	g.declareRuntimeSymbol(ostyRtBytesToHexSymbol, "ptr", []paramInfo{{typ: "ptr"}})
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, "ptr", ostyRtBytesToHexSymbol, []*LlvmValue{toOstyValue(data)})
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.gcManaged = true
	v.sourceType = &ast.NamedType{Path: []string{"String"}}
	return v, nil
}

func (g *generator) emitEncodingBase64EncodeRuntime(data value, urlSafe bool) (value, error) {
	sym := ostyRtEncodingBase64EncodeSymbol
	if urlSafe {
		sym = ostyRtEncodingBase64UrlEncSymbol
	}
	g.declareRuntimeSymbol(sym, "ptr", []paramInfo{{typ: "ptr"}})
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, "ptr", sym, []*LlvmValue{toOstyValue(data)})
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.gcManaged = true
	v.sourceType = &ast.NamedType{Path: []string{"String"}}
	return v, nil
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

// emitStdEncodingBase64CallMIR routes `Base64__encode(self, data)` /
// `Base64Url__encode(self, data)` MIR symbols to the runtime. Decode
// is intentionally left to the AST path (which is also currently
// gated until the MIR Result<Bytes, Error> wrapping lands).
func (g *mirGen) emitStdEncodingBase64CallMIR(c *mir.CallInstr, fnRef *mir.FnRef) (bool, error) {
	var prefix, runtime string
	switch {
	case strings.HasPrefix(fnRef.Symbol, "Base64Url__"):
		prefix, runtime = "Base64Url__", ostyRtEncodingBase64UrlEncSymbol
	case strings.HasPrefix(fnRef.Symbol, "Base64__"):
		prefix, runtime = "Base64__", ostyRtEncodingBase64EncodeSymbol
	default:
		return false, nil
	}
	method := strings.TrimPrefix(fnRef.Symbol, prefix)
	if method != "encode" {
		// decode -> AST path / pending
		if method == "decode" {
			return true, unsupported("mir-mvp", "encoding.base64.decode requires AST lowering route (MIR Result<Bytes, Error> handling pending)")
		}
		return false, nil
	}
	args := c.Args
	if len(args) == 0 {
		return true, unsupported("mir-mvp", "encoding.base64 method without receiver")
	}
	args = args[1:]
	if len(args) != 1 {
		return true, unsupported("mir-mvp", "encoding.base64.encode requires one Bytes argument")
	}
	data, err := g.evalBytesArg(args[0], "encoding.base64.encode", 0)
	if err != nil {
		return true, err
	}
	return true, g.emitRuntimeCallToDest(c, runtime, "ptr", []mirRuntimeArg{data})
}

// emitStdEncodingCallMIR is the MIR-level dispatch. The call site
// presents as a method on the `Hex` struct, so the symbol lands as
// `Hex__encode` / `Hex__decode` after the resolver normalizes it.
func (g *mirGen) emitStdEncodingCallMIR(c *mir.CallInstr, fnRef *mir.FnRef) (bool, error) {
	method := strings.TrimPrefix(fnRef.Symbol, "Hex__")
	if method != "encode" && method != "decode" {
		return false, nil
	}
	args := c.Args
	if len(args) == 0 {
		return true, unsupported("mir-mvp", "encoding.hex method without receiver")
	}
	// Skip receiver (`self: Hex`).
	args = args[1:]
	switch method {
	case "encode":
		if len(args) != 1 {
			return true, unsupported("mir-mvp", "encoding.hex.encode requires one Bytes argument")
		}
		data, err := g.evalBytesArg(args[0], "encoding.hex.encode", 0)
		if err != nil {
			return true, err
		}
		return true, g.emitRuntimeCallToDest(c, ostyRtBytesToHexSymbol, "ptr", []mirRuntimeArg{data})
	case "decode":
		// Decode goes through the MIR Result<Bytes, Error> path. The
		// AST-level helper handles it cleanly (see
		// `emitEncodingHexDecodeResult`), but the MIR pipeline's
		// auto-allocated GC root frame for ptr-typed return values
		// conflicts with the Result struct construction here. Treat as
		// unsupported on the MIR route until that conflict is resolved
		// (separate cycle); callers that go through the AST path still
		// get the proper Result wrapping.
		return true, unsupported("mir-mvp", "encoding.hex.decode requires AST lowering route (MIR Result<Bytes, Error> handling pending)")
	}
	return false, nil
}

// emitEncodingHexDecodeMIR validates input first (since
// `osty_rt_bytes_from_hex` aborts on invalid hex) then constructs the
// `Result<Bytes, Error>` struct directly. Mirrors `emitBytesFromHexResult`
// at the AST layer; cannot use `emitPtrResultFromNullable` because the
// runtime call itself aborts rather than returning null.
func (g *mirGen) emitEncodingHexDecodeMIR(c *mir.CallInstr, text mirRuntimeArg) error {
	if c.Dest == nil {
		// No dest — caller drops the result. Still validate to honor
		// the no-abort contract; from_hex itself is skipped.
		g.declareRuntime(ostyRtBytesIsValidHexSymbol, mirRuntimeDeclareLine("i1", ostyRtBytesIsValidHexSymbol, "ptr"))
		_ = g.fresh()
		g.fnBuf.WriteString(mirCallVoidLine(ostyRtBytesIsValidHexSymbol, mirRuntimeArgList([]mirRuntimeArg{text})))
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

	// Ok arm: call from_hex (safe — validated above), wrap in Ok struct.
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
