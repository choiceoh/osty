package llvmgen

import "github.com/osty/osty/internal/ast"

const ostyRtCryptoSHA256Symbol = "osty_rt_crypto_sha256"
const ostyRtCryptoSHA512Symbol = "osty_rt_crypto_sha512"
const ostyRtCryptoSHA1Symbol = "osty_rt_crypto_sha1"
const ostyRtCryptoMD5Symbol = "osty_rt_crypto_md5"
const ostyRtCryptoHMACSHA256Symbol = "osty_rt_crypto_hmac_sha256"
const ostyRtCryptoHMACSHA512Symbol = "osty_rt_crypto_hmac_sha512"
const ostyRtCryptoRandomBytesSymbol = "osty_rt_crypto_random_bytes"
const ostyRtCryptoConstantTimeEqSymbol = "osty_rt_crypto_constant_time_eq"

var stdCryptoBytesSourceTypeSingleton ast.Type = &ast.NamedType{Path: []string{"Bytes"}}
var stdCryptoBoolSourceTypeSingleton ast.Type = &ast.NamedType{Path: []string{"Bool"}}

func collectStdCryptoAliases(file *ast.File) map[string]bool {
	out := map[string]bool{}
	if file == nil {
		return out
	}
	for _, use := range file.Uses {
		if use == nil || use.IsFFI() {
			continue
		}
		if len(use.Path) != 2 || use.Path[0] != "std" || use.Path[1] != "crypto" {
			continue
		}
		alias := use.Alias
		if alias == "" {
			alias = "crypto"
		}
		out[alias] = true
	}
	return out
}

func (g *generator) emitStdCryptoCall(call *ast.CallExpr) (value, bool, error) {
	kind, ok := g.stdCryptoCallKind(call)
	if !ok {
		return value{}, false, nil
	}
	switch kind {
	case "sha256":
		return g.emitStdCryptoHashCall(call, "crypto.sha256", ostyRtCryptoSHA256Symbol)
	case "sha512":
		return g.emitStdCryptoHashCall(call, "crypto.sha512", ostyRtCryptoSHA512Symbol)
	case "sha1":
		return g.emitStdCryptoHashCall(call, "crypto.sha1", ostyRtCryptoSHA1Symbol)
	case "md5":
		return g.emitStdCryptoHashCall(call, "crypto.md5", ostyRtCryptoMD5Symbol)
	case "hmac.sha256":
		return g.emitStdCryptoHMACCall(call, "crypto.hmac.sha256", ostyRtCryptoHMACSHA256Symbol)
	case "hmac.sha512":
		return g.emitStdCryptoHMACCall(call, "crypto.hmac.sha512", ostyRtCryptoHMACSHA512Symbol)
	case "randomBytes":
		return g.emitStdCryptoRandomBytesCall(call)
	case "constantTimeEq":
		return g.emitStdCryptoConstantTimeEqCall(call)
	default:
		return value{}, false, nil
	}
}

func (g *generator) stdCryptoCallKind(call *ast.CallExpr) (string, bool) {
	if call == nil || len(g.stdCryptoAliases) == 0 {
		return "", false
	}
	field, ok := fieldExprOfCallFn(call)
	if !ok || field.IsOptional {
		return "", false
	}
	if alias, ok := field.X.(*ast.Ident); ok && alias != nil && g.stdCryptoAliases[alias.Name] {
		switch field.Name {
		case "sha256", "sha512", "sha1", "md5", "randomBytes", "constantTimeEq":
			return field.Name, true
		}
		return "", false
	}
	recv, ok := field.X.(*ast.FieldExpr)
	if !ok || recv == nil || recv.IsOptional || recv.Name != "hmac" {
		return "", false
	}
	alias, ok := recv.X.(*ast.Ident)
	if !ok || alias == nil || !g.stdCryptoAliases[alias.Name] {
		return "", false
	}
	switch field.Name {
	case "sha256":
		return "hmac.sha256", true
	case "sha512":
		return "hmac.sha512", true
	default:
		return "", false
	}
}

func (g *generator) emitStdCryptoHashCall(call *ast.CallExpr, opname, symbol string) (value, bool, error) {
	if len(call.Args) != 1 {
		return value{}, true, unsupportedf("call", "%s expects 1 argument, got %d", opname, len(call.Args))
	}
	data, err := g.emitStdCryptoBytesArg(call.Args[0], opname, 0)
	if err != nil {
		return value{}, true, err
	}
	out, err := g.emitStdCryptoBytesRuntime(symbol, []*LlvmValue{toOstyValue(data)})
	if err != nil {
		return value{}, true, err
	}
	return out, true, nil
}

func (g *generator) emitStdCryptoHMACCall(call *ast.CallExpr, opname, symbol string) (value, bool, error) {
	if len(call.Args) != 2 {
		return value{}, true, unsupportedf("call", "%s expects 2 arguments, got %d", opname, len(call.Args))
	}
	key, err := g.emitStdCryptoBytesArg(call.Args[0], opname, 0)
	if err != nil {
		return value{}, true, err
	}
	message, err := g.emitStdCryptoBytesArg(call.Args[1], opname, 1)
	if err != nil {
		return value{}, true, err
	}
	out, err := g.emitStdCryptoBytesRuntime(symbol, []*LlvmValue{toOstyValue(key), toOstyValue(message)})
	if err != nil {
		return value{}, true, err
	}
	return out, true, nil
}

func (g *generator) emitStdCryptoRandomBytesCall(call *ast.CallExpr) (value, bool, error) {
	if len(call.Args) != 1 {
		return value{}, true, unsupportedf("call", "crypto.randomBytes expects 1 argument, got %d", len(call.Args))
	}
	n, err := g.emitStdCryptoIntArg(call.Args[0], "crypto.randomBytes", 0)
	if err != nil {
		return value{}, true, err
	}
	g.declareRuntimeSymbol(ostyRtCryptoRandomBytesSymbol, "ptr", []paramInfo{{typ: "i64"}})
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	out := llvmCall(emitter, "ptr", ostyRtCryptoRandomBytesSymbol, []*LlvmValue{toOstyValue(n)})
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.gcManaged = true
	v.sourceType = stdCryptoBytesSourceTypeSingleton
	v.rootPaths = g.rootPathsForType(v.typ)
	return v, true, nil
}

func (g *generator) emitStdCryptoConstantTimeEqCall(call *ast.CallExpr) (value, bool, error) {
	if len(call.Args) != 2 {
		return value{}, true, unsupportedf("call", "crypto.constantTimeEq expects 2 arguments, got %d", len(call.Args))
	}
	left, err := g.emitStdCryptoBytesArg(call.Args[0], "crypto.constantTimeEq", 0)
	if err != nil {
		return value{}, true, err
	}
	right, err := g.emitStdCryptoBytesArg(call.Args[1], "crypto.constantTimeEq", 1)
	if err != nil {
		return value{}, true, err
	}
	g.declareRuntimeSymbol(ostyRtCryptoConstantTimeEqSymbol, "i1", []paramInfo{{typ: "ptr"}, {typ: "ptr"}})
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	out := llvmCall(emitter, "i1", ostyRtCryptoConstantTimeEqSymbol, []*LlvmValue{toOstyValue(left), toOstyValue(right)})
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.sourceType = stdCryptoBoolSourceTypeSingleton
	return v, true, nil
}

func (g *generator) emitStdCryptoBytesRuntime(symbol string, args []*LlvmValue) (value, error) {
	params := make([]paramInfo, len(args))
	for i := range params {
		params[i] = paramInfo{typ: "ptr"}
	}
	g.declareRuntimeSymbol(symbol, "ptr", params)
	emitter := g.toOstyEmitter()
	g.emitCallSafepointIfNeeded(emitter)
	out := llvmCall(emitter, "ptr", symbol, args)
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.gcManaged = true
	v.sourceType = stdCryptoBytesSourceTypeSingleton
	v.rootPaths = g.rootPathsForType(v.typ)
	return v, nil
}

func (g *generator) emitStdCryptoBytesArg(arg *ast.Arg, opname string, index int) (value, error) {
	if arg == nil || arg.Name != "" || arg.Value == nil {
		return value{}, unsupportedf("call", "%s arg %d requires a positional Bytes value", opname, index+1)
	}
	v, err := g.emitExpr(arg.Value)
	if err != nil {
		return value{}, err
	}
	v, err = g.loadIfPointer(v)
	if err != nil {
		return value{}, err
	}
	if v.typ != "ptr" {
		return value{}, unsupportedf("type-system", "%s arg %d type %s, want Bytes", opname, index+1, v.typ)
	}
	return v, nil
}

func (g *generator) emitStdCryptoIntArg(arg *ast.Arg, opname string, index int) (value, error) {
	if arg == nil || arg.Name != "" || arg.Value == nil {
		return value{}, unsupportedf("call", "%s arg %d requires a positional Int value", opname, index+1)
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
		return value{}, unsupportedf("type-system", "%s arg %d type %s, want Int", opname, index+1, v.typ)
	}
	return v, nil
}

func (g *generator) stdCryptoCallStaticResult(call *ast.CallExpr) (value, bool) {
	kind, ok := g.stdCryptoCallKind(call)
	if !ok {
		return value{}, false
	}
	switch kind {
	case "sha256", "sha512", "sha1", "md5", "hmac.sha256", "hmac.sha512", "randomBytes":
		return value{
			typ:        "ptr",
			gcManaged:  true,
			sourceType: stdCryptoBytesSourceTypeSingleton,
		}, true
	case "constantTimeEq":
		return value{
			typ:        "i1",
			sourceType: stdCryptoBoolSourceTypeSingleton,
		}, true
	default:
		return value{}, false
	}
}

func (g *generator) staticStdCryptoCallSourceType(call *ast.CallExpr) (ast.Type, bool) {
	kind, ok := g.stdCryptoCallKind(call)
	if !ok {
		return nil, false
	}
	switch kind {
	case "sha256", "sha512", "sha1", "md5", "hmac.sha256", "hmac.sha512", "randomBytes":
		return stdCryptoBytesSourceTypeSingleton, true
	case "constantTimeEq":
		return stdCryptoBoolSourceTypeSingleton, true
	default:
		return nil, false
	}
}
