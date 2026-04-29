package llvmgen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/mir"
)

type mirRuntimeArg struct {
	typ string
	val string
}

func mirRuntimeArgList(args []mirRuntimeArg) string {
	if len(args) == 0 {
		return ""
	}
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		parts = append(parts, arg.typ+" "+arg.val)
	}
	return strings.Join(parts, ", ")
}

func mirRuntimeParamList(args []mirRuntimeArg) string {
	if len(args) == 0 {
		return ""
	}
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		parts = append(parts, arg.typ)
	}
	return strings.Join(parts, ", ")
}

func (g *mirGen) emitRuntimeCallToDest(c *mir.CallInstr, symbol, retLLVM string, args []mirRuntimeArg) error {
	g.declareRuntime(symbol, mirRuntimeDeclareLine(retLLVM, symbol, mirRuntimeParamList(args)))
	argList := mirRuntimeArgList(args)
	if c.Dest == nil {
		if retLLVM == "void" {
			g.fnBuf.WriteString(mirCallVoidLine(symbol, argList))
		} else {
			g.fnBuf.WriteString(mirCallStmtLine(retLLVM, symbol, argList))
		}
		return nil
	}
	destLoc := g.fn.Local(c.Dest.Local)
	if destLoc == nil {
		return fmt.Errorf("mir-mvp: runtime call dest into unknown local %d", c.Dest.Local)
	}
	if isUnitType(destLoc.Type) {
		if retLLVM == "void" {
			g.fnBuf.WriteString(mirCallVoidLine(symbol, argList))
		} else {
			g.fnBuf.WriteString(mirCallStmtLine(retLLVM, symbol, argList))
		}
		return nil
	}
	tmp := g.fresh()
	g.fnBuf.WriteString(mirCallValueLine(tmp, retLLVM, symbol, argList))
	return g.storeCallValue(c, retLLVM, tmp)
}

func (g *mirGen) storeCallValue(c *mir.CallInstr, valueLLVM, valueReg string) error {
	if c.Dest == nil {
		return nil
	}
	destLoc := g.fn.Local(c.Dest.Local)
	if destLoc == nil || isUnitType(destLoc.Type) {
		return nil
	}
	destLLVM := g.llvmType(destLoc.Type)
	stored := valueReg
	if destLLVM != valueLLVM {
		if coerced, err := g.coerceValue(valueReg, valueLLVM, destLLVM); err == nil {
			stored = coerced
		}
	}
	g.fnBuf.WriteString(mirStoreLine(destLLVM, stored, g.localSlots[c.Dest.Local]))
	return nil
}

func (g *mirGen) storeZeroDest(c *mir.CallInstr) error {
	if c.Dest == nil {
		return nil
	}
	destLoc := g.fn.Local(c.Dest.Local)
	if destLoc == nil || isUnitType(destLoc.Type) {
		return nil
	}
	destLLVM := g.llvmType(destLoc.Type)
	g.fnBuf.WriteString(mirStoreLine(destLLVM, mirZeroOfType(destLLVM), g.localSlots[c.Dest.Local]))
	return nil
}

func (g *mirGen) evalTypedArg(op mir.Operand, want mir.Type) (mirRuntimeArg, error) {
	if want == nil {
		want = op.Type()
	}
	val, err := g.evalOperand(op, want)
	if err != nil {
		return mirRuntimeArg{}, err
	}
	return mirRuntimeArg{typ: g.llvmType(want), val: val}, nil
}

func (g *mirGen) evalStringArg(op mir.Operand, name string, index int) (mirRuntimeArg, error) {
	if op == nil || !nativeTypeIsString(op.Type()) {
		return mirRuntimeArg{}, unsupportedf("mir-mvp", "%s arg %d type %s, want String", name, index+1, mirTypeString(op.Type()))
	}
	return g.evalTypedArg(op, op.Type())
}

func (g *mirGen) evalBytesArg(op mir.Operand, name string, index int) (mirRuntimeArg, error) {
	if op == nil || !nativeTypeIsBytes(op.Type()) {
		return mirRuntimeArg{}, unsupportedf("mir-mvp", "%s arg %d type %s, want Bytes", name, index+1, mirTypeString(op.Type()))
	}
	return g.evalTypedArg(op, op.Type())
}

func (g *mirGen) evalIntArg(op mir.Operand, name string, index int) (mirRuntimeArg, error) {
	if op == nil || g.llvmType(op.Type()) != "i64" {
		return mirRuntimeArg{}, unsupportedf("mir-mvp", "%s arg %d type %s, want Int", name, index+1, mirTypeString(op.Type()))
	}
	return g.evalTypedArg(op, op.Type())
}

func (g *mirGen) evalBoolArg(op mir.Operand, name string, index int) (mirRuntimeArg, error) {
	if op == nil || g.llvmType(op.Type()) != "i1" {
		return mirRuntimeArg{}, unsupportedf("mir-mvp", "%s arg %d type %s, want Bool", name, index+1, mirTypeString(op.Type()))
	}
	return g.evalTypedArg(op, op.Type())
}

func (g *mirGen) emitResultValue(resultLLVM string, ok bool, payloadI64 string) string {
	step := g.fresh()
	value := g.fresh()
	if ok {
		g.fnBuf.WriteString(mirOkAggregateLines(step, value, resultLLVM, payloadI64))
	} else {
		g.fnBuf.WriteString(mirErrAggregateLines(step, value, resultLLVM, payloadI64))
	}
	return value
}

func (g *mirGen) emitOptionValue(optionLLVM string, some bool, payloadI64 string) string {
	if !some {
		step := g.fresh()
		value := g.fresh()
		g.fnBuf.WriteString(mirNoneAggregateLines(step, value, optionLLVM))
		return value
	}
	step := g.fresh()
	value := g.fresh()
	g.fnBuf.WriteString(mirInsertValueI64Line(step, optionLLVM, "undef", "1", "0"))
	g.fnBuf.WriteString(mirInsertValueI64Line(value, optionLLVM, step, payloadI64, "1"))
	return value
}

func (g *mirGen) storeOptionPtrFromNullable(c *mir.CallInstr, nullablePtr string) error {
	if c.Dest == nil {
		return nil
	}
	destLoc := g.fn.Local(c.Dest.Local)
	if destLoc == nil {
		return fmt.Errorf("mir-mvp: option ptr dest into unknown local %d", c.Dest.Local)
	}
	optLLVM := g.llvmType(destLoc.Type)
	notNil := g.fresh()
	g.fnBuf.WriteString(mirICmpLine(notNil, "ne", "ptr", nullablePtr, "null"))
	disc := g.fresh()
	g.fnBuf.WriteString(mirZExtLine(disc, "i1", notNil, "i64"))
	payload := g.fresh()
	g.fnBuf.WriteString(mirPtrToIntLine(payload, nullablePtr, "i64"))
	step := g.fresh()
	full := g.fresh()
	g.fnBuf.WriteString(mirInsertValueI64Line(step, optLLVM, "undef", disc, "0"))
	g.fnBuf.WriteString(mirInsertValueI64Line(full, optLLVM, step, payload, "1"))
	g.fnBuf.WriteString(mirStoreLine(optLLVM, full, g.localSlots[c.Dest.Local]))
	return nil
}

func (g *mirGen) emitUnitResultFromNullableError(c *mir.CallInstr, symbol string, args []mirRuntimeArg) error {
	g.declareRuntime(symbol, mirRuntimeDeclareLine("ptr", symbol, mirRuntimeParamList(args)))
	argList := mirRuntimeArgList(args)
	if c.Dest == nil {
		g.fnBuf.WriteString(mirCallStmtLine("ptr", symbol, argList))
		return nil
	}
	destLoc := g.fn.Local(c.Dest.Local)
	if destLoc == nil {
		return fmt.Errorf("mir-mvp: unit-result dest into unknown local %d", c.Dest.Local)
	}
	resultLLVM := g.llvmType(destLoc.Type)
	errReg := g.fresh()
	g.fnBuf.WriteString(mirCallValueLine(errReg, "ptr", symbol, argList))
	isNil := g.fresh()
	g.fnBuf.WriteString(mirICmpEqLine(isNil, "ptr", errReg, "null"))
	okLabel := g.freshLabel("result.unit.ok")
	errLabel := g.freshLabel("result.unit.err")
	contLabel := g.freshLabel("result.unit.cont")
	g.fnBuf.WriteString(mirBrCondLine(isNil, okLabel, errLabel))

	g.fnBuf.WriteString(mirLabelLine(okLabel))
	okValue := g.emitResultValue(resultLLVM, true, "0")
	g.fnBuf.WriteString(mirBrUncondLine(contLabel))

	g.fnBuf.WriteString(mirLabelLine(errLabel))
	errPayload := g.fresh()
	g.fnBuf.WriteString(mirPtrToIntLine(errPayload, errReg, "i64"))
	errValue := g.emitResultValue(resultLLVM, false, errPayload)
	g.fnBuf.WriteString(mirBrUncondLine(contLabel))

	g.fnBuf.WriteString(mirLabelLine(contLabel))
	phi := g.fresh()
	g.fnBuf.WriteString(mirPhiTwoLine(phi, resultLLVM, okValue, okLabel, errValue, errLabel))
	g.fnBuf.WriteString(mirStoreLine(resultLLVM, phi, g.localSlots[c.Dest.Local]))
	return nil
}

func (g *mirGen) emitPtrResultFromNullable(c *mir.CallInstr, valueReg string, valueT mir.Type, errReg string) error {
	if c.Dest == nil {
		return nil
	}
	destLoc := g.fn.Local(c.Dest.Local)
	if destLoc == nil {
		return fmt.Errorf("mir-mvp: ptr-result dest into unknown local %d", c.Dest.Local)
	}
	resultLLVM := g.llvmType(destLoc.Type)
	isNil := g.fresh()
	g.fnBuf.WriteString(mirICmpEqLine(isNil, "ptr", valueReg, "null"))
	errLabel := g.freshLabel("result.ptr.err")
	okLabel := g.freshLabel("result.ptr.ok")
	contLabel := g.freshLabel("result.ptr.cont")
	g.fnBuf.WriteString(mirBrCondLine(isNil, errLabel, okLabel))

	g.fnBuf.WriteString(mirLabelLine(errLabel))
	errPayload := "0"
	if errReg != "" {
		errPayload = g.fresh()
		g.fnBuf.WriteString(mirPtrToIntLine(errPayload, errReg, "i64"))
	}
	errValue := g.emitResultValue(resultLLVM, false, errPayload)
	g.fnBuf.WriteString(mirBrUncondLine(contLabel))

	g.fnBuf.WriteString(mirLabelLine(okLabel))
	okPayload, err := g.toI64Slot(valueReg, valueT)
	if err != nil {
		return err
	}
	okValue := g.emitResultValue(resultLLVM, true, okPayload)
	g.fnBuf.WriteString(mirBrUncondLine(contLabel))

	g.fnBuf.WriteString(mirLabelLine(contLabel))
	phi := g.fresh()
	g.fnBuf.WriteString(mirPhiTwoLine(phi, resultLLVM, errValue, errLabel, okValue, okLabel))
	g.fnBuf.WriteString(mirStoreLine(resultLLVM, phi, g.localSlots[c.Dest.Local]))
	return nil
}

func (g *mirGen) buildAggregateValue(t mir.Type, fields []mirRuntimeArg) (string, error) {
	llvmT := g.llvmType(t)
	acc := "undef"
	for i, field := range fields {
		tmp := g.fresh()
		g.fnBuf.WriteString(mirInsertValueAggLine(tmp, llvmT, acc, field.typ, field.val, strconv.Itoa(i)))
		acc = tmp
	}
	return acc, nil
}

func (g *mirGen) emitRecordFieldLoad(raw, recordLLVM, fieldLLVM string, idx int) string {
	ptr := g.fresh()
	g.fnBuf.WriteString(mirGEPStructFieldLine(ptr, recordLLVM, raw, strconv.Itoa(idx)))
	val := g.fresh()
	g.fnBuf.WriteString(mirLoadLine(val, fieldLLVM, ptr))
	return val
}

func (g *mirGen) resultSubtypes(t mir.Type) (mir.Type, mir.Type, bool) {
	info, ok := testingResultType(t)
	if !ok {
		return nil, nil, false
	}
	return info.ok, info.err, true
}

func (g *mirGen) emitStdEnvCall(c *mir.CallInstr, fnRef *mir.FnRef) (bool, error) {
	method := strings.TrimPrefix(fnRef.Symbol, "std.env.")
	switch method {
	case "args":
		if len(c.Args) != 0 {
			return true, unsupported("mir-mvp", "std.env.args requires no arguments")
		}
		return true, g.emitRuntimeCallToDest(c, ostyRtEnvArgsSymbol, "ptr", nil)
	case "get":
		if len(c.Args) != 1 {
			return true, unsupported("mir-mvp", "std.env.get requires one argument")
		}
		name, err := g.evalStringArg(c.Args[0], "std.env.get", 0)
		if err != nil {
			return true, err
		}
		g.declareRuntime(ostyRtEnvGetSymbol, mirRuntimeDeclareLine("ptr", ostyRtEnvGetSymbol, "ptr"))
		out := g.fresh()
		g.fnBuf.WriteString(mirCallValueLine(out, "ptr", ostyRtEnvGetSymbol, mirRuntimeArgList([]mirRuntimeArg{name})))
		return true, g.storeOptionPtrFromNullable(c, out)
	case "require":
		return true, g.emitStdEnvRequireMIR(c)
	case "vars":
		if len(c.Args) != 0 {
			return true, unsupported("mir-mvp", "std.env.vars requires no arguments")
		}
		return true, g.emitRuntimeCallToDest(c, ostyRtEnvVarsSymbol, "ptr", nil)
	case "currentDir":
		return true, g.emitStdEnvCurrentDirMIR(c)
	case "setCurrentDir":
		if len(c.Args) != 1 {
			return true, unsupported("mir-mvp", "std.env.setCurrentDir requires one argument")
		}
		path, err := g.evalStringArg(c.Args[0], "std.env.setCurrentDir", 0)
		if err != nil {
			return true, err
		}
		return true, g.emitUnitResultFromNullableError(c, ostyRtEnvSetCurrentDirSymbol, []mirRuntimeArg{path})
	case "set":
		if len(c.Args) != 2 {
			return true, unsupported("mir-mvp", "std.env.set requires two arguments")
		}
		name, err := g.evalStringArg(c.Args[0], "std.env.set", 0)
		if err != nil {
			return true, err
		}
		value, err := g.evalStringArg(c.Args[1], "std.env.set", 1)
		if err != nil {
			return true, err
		}
		return true, g.emitUnitResultFromNullableError(c, ostyRtEnvSetSymbol, []mirRuntimeArg{name, value})
	case "unset":
		if len(c.Args) != 1 {
			return true, unsupported("mir-mvp", "std.env.unset requires one argument")
		}
		name, err := g.evalStringArg(c.Args[0], "std.env.unset", 0)
		if err != nil {
			return true, err
		}
		return true, g.emitUnitResultFromNullableError(c, ostyRtEnvUnsetSymbol, []mirRuntimeArg{name})
	}
	return false, nil
}

func (g *mirGen) emitStdEnvRequireMIR(c *mir.CallInstr) error {
	if len(c.Args) != 1 {
		return unsupported("mir-mvp", "std.env.require requires one argument")
	}
	name, err := g.evalStringArg(c.Args[0], "std.env.require", 0)
	if err != nil {
		return err
	}
	g.declareRuntime(ostyRtEnvGetSymbol, mirRuntimeDeclareLine("ptr", ostyRtEnvGetSymbol, "ptr"))
	g.declareRuntime(llvmStringRuntimeConcatSymbol(), mirRuntimeDeclareLine("ptr", llvmStringRuntimeConcatSymbol(), "ptr, ptr"))
	found := g.fresh()
	g.fnBuf.WriteString(mirCallValueLine(found, "ptr", ostyRtEnvGetSymbol, mirRuntimeArgList([]mirRuntimeArg{name})))
	if c.Dest == nil {
		return nil
	}
	destLoc := g.fn.Local(c.Dest.Local)
	if destLoc == nil {
		return fmt.Errorf("mir-mvp: env.require dest into unknown local %d", c.Dest.Local)
	}
	resultLLVM := g.llvmType(destLoc.Type)
	missing := g.fresh()
	g.fnBuf.WriteString(mirICmpEqLine(missing, "ptr", found, "null"))
	missingLabel := g.freshLabel("env.require.missing")
	okLabel := g.freshLabel("env.require.ok")
	contLabel := g.freshLabel("env.require.cont")
	g.fnBuf.WriteString(mirBrCondLine(missing, missingLabel, okLabel))

	g.fnBuf.WriteString(mirLabelLine(missingLabel))
	prefix := g.stringLiteral("environment variable not set: ")
	msg := g.fresh()
	g.fnBuf.WriteString(mirCallValueLine(msg, "ptr", llvmStringRuntimeConcatSymbol(), mirArgSlotPtr(prefix)+", "+mirArgSlotPtr(name.val)))
	errPayload := g.fresh()
	g.fnBuf.WriteString(mirPtrToIntLine(errPayload, msg, "i64"))
	errValue := g.emitResultValue(resultLLVM, false, errPayload)
	g.fnBuf.WriteString(mirBrUncondLine(contLabel))

	g.fnBuf.WriteString(mirLabelLine(okLabel))
	okPayload := g.fresh()
	g.fnBuf.WriteString(mirPtrToIntLine(okPayload, found, "i64"))
	okValue := g.emitResultValue(resultLLVM, true, okPayload)
	g.fnBuf.WriteString(mirBrUncondLine(contLabel))

	g.fnBuf.WriteString(mirLabelLine(contLabel))
	phi := g.fresh()
	g.fnBuf.WriteString(mirPhiTwoLine(phi, resultLLVM, errValue, missingLabel, okValue, okLabel))
	g.fnBuf.WriteString(mirStoreLine(resultLLVM, phi, g.localSlots[c.Dest.Local]))
	return nil
}

func (g *mirGen) emitStdEnvCurrentDirMIR(c *mir.CallInstr) error {
	if len(c.Args) != 0 {
		return unsupported("mir-mvp", "std.env.currentDir requires no arguments")
	}
	g.declareRuntime(ostyRtEnvCurrentDirSymbol, mirRuntimeDeclarePtrNoArgsLine(ostyRtEnvCurrentDirSymbol))
	g.declareRuntime(ostyRtEnvCurrentDirErrorSymbol, mirRuntimeDeclarePtrNoArgsLine(ostyRtEnvCurrentDirErrorSymbol))
	dir := g.fresh()
	g.fnBuf.WriteString(mirCallValueNoArgsLine(dir, "ptr", ostyRtEnvCurrentDirSymbol))
	errReg := g.fresh()
	// The error string is only consumed on the nil branch; computing it
	// eagerly keeps the two-branch result builder small and deterministic.
	g.fnBuf.WriteString(mirCallValueNoArgsLine(errReg, "ptr", ostyRtEnvCurrentDirErrorSymbol))
	return g.emitPtrResultFromNullable(c, dir, mir.TString, errReg)
}

func (g *mirGen) emitStdCryptoCall(c *mir.CallInstr, fnRef *mir.FnRef) (bool, error) {
	symbol := fnRef.Symbol
	method := strings.TrimPrefix(symbol, "std.crypto.")
	skipReceiver := false
	if strings.HasPrefix(symbol, "Hmac__") {
		method = "hmac." + strings.TrimPrefix(symbol, "Hmac__")
		skipReceiver = true
	}
	args := c.Args
	if skipReceiver {
		if len(args) == 0 {
			return true, unsupported("mir-mvp", "crypto.hmac method without receiver")
		}
		args = args[1:]
	}
	switch method {
	case "sha256", "sha512", "sha1", "md5":
		if len(args) != 1 {
			return true, unsupportedf("mir-mvp", "std.crypto.%s requires one Bytes argument", method)
		}
		data, err := g.evalBytesArg(args[0], "std.crypto."+method, 0)
		if err != nil {
			return true, err
		}
		rt := map[string]string{
			"sha256": ostyRtCryptoSHA256Symbol,
			"sha512": ostyRtCryptoSHA512Symbol,
			"sha1":   ostyRtCryptoSHA1Symbol,
			"md5":    ostyRtCryptoMD5Symbol,
		}[method]
		return true, g.emitRuntimeCallToDest(c, rt, "ptr", []mirRuntimeArg{data})
	case "hmac.sha256", "hmac.sha512":
		if len(args) != 2 {
			return true, unsupportedf("mir-mvp", "std.crypto.%s requires key and message Bytes arguments", method)
		}
		key, err := g.evalBytesArg(args[0], "std.crypto."+method, 0)
		if err != nil {
			return true, err
		}
		msg, err := g.evalBytesArg(args[1], "std.crypto."+method, 1)
		if err != nil {
			return true, err
		}
		rt := ostyRtCryptoHMACSHA256Symbol
		if method == "hmac.sha512" {
			rt = ostyRtCryptoHMACSHA512Symbol
		}
		return true, g.emitRuntimeCallToDest(c, rt, "ptr", []mirRuntimeArg{key, msg})
	case "randomBytes":
		if len(args) != 1 {
			return true, unsupported("mir-mvp", "std.crypto.randomBytes requires one Int argument")
		}
		n, err := g.evalIntArg(args[0], "std.crypto.randomBytes", 0)
		if err != nil {
			return true, err
		}
		return true, g.emitRuntimeCallToDest(c, ostyRtCryptoRandomBytesSymbol, "ptr", []mirRuntimeArg{n})
	case "constantTimeEq":
		if len(args) != 2 {
			return true, unsupported("mir-mvp", "std.crypto.constantTimeEq requires two Bytes arguments")
		}
		left, err := g.evalBytesArg(args[0], "std.crypto.constantTimeEq", 0)
		if err != nil {
			return true, err
		}
		right, err := g.evalBytesArg(args[1], "std.crypto.constantTimeEq", 1)
		if err != nil {
			return true, err
		}
		return true, g.emitRuntimeCallToDest(c, ostyRtCryptoConstantTimeEqSymbol, "i1", []mirRuntimeArg{left, right})
	}
	return false, nil
}

func (g *mirGen) emitStdCompressCall(c *mir.CallInstr, fnRef *mir.FnRef) (bool, error) {
	method := strings.TrimPrefix(fnRef.Symbol, "std.compress.")
	args := c.Args
	if strings.HasPrefix(fnRef.Symbol, "Gzip__") {
		method = strings.TrimPrefix(fnRef.Symbol, "Gzip__")
		if len(args) == 0 {
			return true, unsupported("mir-mvp", "compress.gzip method without receiver")
		}
		args = args[1:]
	}
	if strings.HasSuffix(method, ".encode") {
		method = "encode"
	}
	if strings.HasSuffix(method, ".decode") {
		method = "decode"
	}
	switch method {
	case "encode":
		if len(args) != 1 {
			return true, unsupported("mir-mvp", "compress.gzip.encode requires one Bytes argument")
		}
		data, err := g.evalBytesArg(args[0], "compress.gzip.encode", 0)
		if err != nil {
			return true, err
		}
		return true, g.emitRuntimeCallToDest(c, ostyRtCompressGzipEncodeSymbol, "ptr", []mirRuntimeArg{data})
	case "decode":
		if len(args) != 1 {
			return true, unsupported("mir-mvp", "compress.gzip.decode requires one Bytes argument")
		}
		data, err := g.evalBytesArg(args[0], "compress.gzip.decode", 0)
		if err != nil {
			return true, err
		}
		g.declareRuntime(ostyRtCompressGzipDecodeSymbol, mirRuntimeDeclareLine("ptr", ostyRtCompressGzipDecodeSymbol, "ptr"))
		decoded := g.fresh()
		g.fnBuf.WriteString(mirCallValueLine(decoded, "ptr", ostyRtCompressGzipDecodeSymbol, mirRuntimeArgList([]mirRuntimeArg{data})))
		return true, g.emitPtrResultFromNullable(c, decoded, mir.TBytes, "")
	}
	return false, nil
}

func (g *mirGen) emitStdOsCall(c *mir.CallInstr, fnRef *mir.FnRef) (bool, error) {
	method := strings.TrimPrefix(fnRef.Symbol, "std.os.")
	switch method {
	case "exec":
		return true, g.emitStdOsExecMIR(c, false)
	case "execShell":
		return true, g.emitStdOsExecMIR(c, true)
	case "pid":
		if len(c.Args) != 0 {
			return true, unsupported("mir-mvp", "std.os.pid requires no arguments")
		}
		return true, g.emitRuntimeCallToDest(c, ostyRtOsPidSymbol, "i64", nil)
	case "hostname":
		return true, g.emitStdOsHostnameMIR(c)
	case "exit":
		if len(c.Args) != 1 {
			return true, unsupported("mir-mvp", "std.os.exit requires one Int argument")
		}
		code, err := g.evalIntArg(c.Args[0], "std.os.exit", 0)
		if err != nil {
			return true, err
		}
		code32 := g.fresh()
		g.fnBuf.WriteString(mirTruncLine(code32, "i64", code.val, "i32"))
		g.declareRuntime(ostyRtOsExitSymbol, mirRuntimeDeclareNoReturn("void", ostyRtOsExitSymbol, "i32", false))
		g.fnBuf.WriteString(mirCallVoidLine(ostyRtOsExitSymbol, mirArgSlotI32(code32)))
		return true, nil
	}
	return false, nil
}

func (g *mirGen) emitStdOsExecMIR(c *mir.CallInstr, shell bool) error {
	if len(c.Args) == 0 || (!shell && len(c.Args) > 2) || (shell && len(c.Args) != 1) {
		return unsupported("mir-mvp", "std.os.exec/execShell argument shape")
	}
	cmdArg, err := g.evalStringArg(c.Args[0], "std.os.exec", 0)
	if err != nil {
		return err
	}
	argsArg := mirRuntimeArg{typ: "ptr", val: "null"}
	if !shell && len(c.Args) == 2 {
		argsArg, err = g.evalTypedArg(c.Args[1], c.Args[1].Type())
		if err != nil {
			return err
		}
		if argsArg.typ != "ptr" {
			return unsupported("mir-mvp", "std.os.exec args must be List<String>")
		}
	}
	shellArg := mirRuntimeArg{typ: "i1", val: "false"}
	if shell {
		shellArg.val = "true"
	}
	g.declareRuntime(ostyRtOsExecSymbol, mirRuntimeDeclareLine("ptr", ostyRtOsExecSymbol, "ptr, ptr, i1"))
	g.declareRuntime(ostyRtOsExecResultFreeSymbol, mirRuntimeDeclareLine("void", ostyRtOsExecResultFreeSymbol, "ptr"))
	raw := g.fresh()
	g.fnBuf.WriteString(mirCallValueLine(raw, "ptr", ostyRtOsExecSymbol, mirRuntimeArgList([]mirRuntimeArg{cmdArg, argsArg, shellArg})))
	tag := g.emitRecordFieldLoad(raw, stdOsExecRuntimeRecordLLVMType, "i64", 0)
	exitCode := g.emitRecordFieldLoad(raw, stdOsExecRuntimeRecordLLVMType, "i64", 1)
	stdoutText := g.emitRecordFieldLoad(raw, stdOsExecRuntimeRecordLLVMType, "ptr", 2)
	stderrText := g.emitRecordFieldLoad(raw, stdOsExecRuntimeRecordLLVMType, "ptr", 3)
	errText := g.emitRecordFieldLoad(raw, stdOsExecRuntimeRecordLLVMType, "ptr", 4)
	g.fnBuf.WriteString(mirCallVoidLine(ostyRtOsExecResultFreeSymbol, mirArgSlotPtr(raw)))
	if c.Dest == nil {
		return nil
	}
	destLoc := g.fn.Local(c.Dest.Local)
	if destLoc == nil {
		return fmt.Errorf("mir-mvp: os.exec dest into unknown local %d", c.Dest.Local)
	}
	okT, _, ok := g.resultSubtypes(destLoc.Type)
	if !ok {
		return unsupported("mir-mvp", "std.os.exec dest is not Result<Output, Error>")
	}
	resultLLVM := g.llvmType(destLoc.Type)
	failed := g.fresh()
	g.fnBuf.WriteString(mirICmpEqI64Line(failed, tag, "1"))
	errLabel := g.freshLabel("os.exec.err")
	okLabel := g.freshLabel("os.exec.ok")
	contLabel := g.freshLabel("os.exec.cont")
	g.fnBuf.WriteString(mirBrCondLine(failed, errLabel, okLabel))

	g.fnBuf.WriteString(mirLabelLine(errLabel))
	errPayload := g.fresh()
	g.fnBuf.WriteString(mirPtrToIntLine(errPayload, errText, "i64"))
	errValue := g.emitResultValue(resultLLVM, false, errPayload)
	g.fnBuf.WriteString(mirBrUncondLine(contLabel))

	g.fnBuf.WriteString(mirLabelLine(okLabel))
	output, err := g.buildAggregateValue(okT, []mirRuntimeArg{
		{typ: "i64", val: exitCode},
		{typ: "ptr", val: stdoutText},
		{typ: "ptr", val: stderrText},
	})
	if err != nil {
		return err
	}
	okPayload, err := g.toI64Slot(output, okT)
	if err != nil {
		return err
	}
	okValue := g.emitResultValue(resultLLVM, true, okPayload)
	g.fnBuf.WriteString(mirBrUncondLine(contLabel))

	g.fnBuf.WriteString(mirLabelLine(contLabel))
	phi := g.fresh()
	g.fnBuf.WriteString(mirPhiTwoLine(phi, resultLLVM, errValue, errLabel, okValue, okLabel))
	g.fnBuf.WriteString(mirStoreLine(resultLLVM, phi, g.localSlots[c.Dest.Local]))
	return nil
}

func (g *mirGen) emitStdOsHostnameMIR(c *mir.CallInstr) error {
	if len(c.Args) != 0 {
		return unsupported("mir-mvp", "std.os.hostname requires no arguments")
	}
	g.declareRuntime(ostyRtOsHostnameSymbol, mirRuntimeDeclarePtrNoArgsLine(ostyRtOsHostnameSymbol))
	g.declareRuntime(ostyRtOsStringResultFreeSymbol, mirRuntimeDeclareLine("void", ostyRtOsStringResultFreeSymbol, "ptr"))
	raw := g.fresh()
	g.fnBuf.WriteString(mirCallValueNoArgsLine(raw, "ptr", ostyRtOsHostnameSymbol))
	tag := g.emitRecordFieldLoad(raw, stdOsStringRuntimeRecordLLVMType, "i64", 0)
	valueText := g.emitRecordFieldLoad(raw, stdOsStringRuntimeRecordLLVMType, "ptr", 1)
	errText := g.emitRecordFieldLoad(raw, stdOsStringRuntimeRecordLLVMType, "ptr", 2)
	g.fnBuf.WriteString(mirCallVoidLine(ostyRtOsStringResultFreeSymbol, mirArgSlotPtr(raw)))
	if c.Dest == nil {
		return nil
	}
	destLoc := g.fn.Local(c.Dest.Local)
	if destLoc == nil {
		return fmt.Errorf("mir-mvp: os.hostname dest into unknown local %d", c.Dest.Local)
	}
	resultLLVM := g.llvmType(destLoc.Type)
	failed := g.fresh()
	g.fnBuf.WriteString(mirICmpEqI64Line(failed, tag, "1"))
	errLabel := g.freshLabel("os.hostname.err")
	okLabel := g.freshLabel("os.hostname.ok")
	contLabel := g.freshLabel("os.hostname.cont")
	g.fnBuf.WriteString(mirBrCondLine(failed, errLabel, okLabel))

	g.fnBuf.WriteString(mirLabelLine(errLabel))
	errPayload := g.fresh()
	g.fnBuf.WriteString(mirPtrToIntLine(errPayload, errText, "i64"))
	errValue := g.emitResultValue(resultLLVM, false, errPayload)
	g.fnBuf.WriteString(mirBrUncondLine(contLabel))

	g.fnBuf.WriteString(mirLabelLine(okLabel))
	okPayload := g.fresh()
	g.fnBuf.WriteString(mirPtrToIntLine(okPayload, valueText, "i64"))
	okValue := g.emitResultValue(resultLLVM, true, okPayload)
	g.fnBuf.WriteString(mirBrUncondLine(contLabel))

	g.fnBuf.WriteString(mirLabelLine(contLabel))
	phi := g.fresh()
	g.fnBuf.WriteString(mirPhiTwoLine(phi, resultLLVM, errValue, errLabel, okValue, okLabel))
	g.fnBuf.WriteString(mirStoreLine(resultLLVM, phi, g.localSlots[c.Dest.Local]))
	return nil
}

func (g *mirGen) emitStdTermCall(c *mir.CallInstr, fnRef *mir.FnRef) (bool, error) {
	method := strings.TrimPrefix(fnRef.Symbol, "std.term.")
	switch method {
	case "isTerminal":
		if len(c.Args) != 0 {
			return true, unsupported("mir-mvp", "std.term.isTerminal requires no arguments")
		}
		return true, g.emitRuntimeCallToDest(c, ostyRtTermIsTerminalSymbol, "i1", nil)
	case "size":
		return true, g.emitStdTermSizeMIR(c)
	case "write":
		if len(c.Args) != 1 {
			return true, unsupported("mir-mvp", "std.term.write requires one String argument")
		}
		text, err := g.evalStringArg(c.Args[0], "std.term.write", 0)
		if err != nil {
			return true, err
		}
		return true, g.emitUnitResultFromNullableError(c, ostyRtTermWriteSymbol, []mirRuntimeArg{text})
	case "flush":
		if len(c.Args) != 0 {
			return true, unsupported("mir-mvp", "std.term.flush requires no arguments")
		}
		return true, g.emitUnitResultFromNullableError(c, ostyRtTermFlushSymbol, nil)
	case "setRawMode":
		if len(c.Args) != 1 {
			return true, unsupported("mir-mvp", "std.term.setRawMode requires one Bool argument")
		}
		enabled, err := g.evalBoolArg(c.Args[0], "std.term.setRawMode", 0)
		if err != nil {
			return true, err
		}
		return true, g.emitUnitResultFromNullableError(c, ostyRtTermSetRawModeSymbol, []mirRuntimeArg{enabled})
	}
	return false, nil
}

func (g *mirGen) emitStdTermSizeMIR(c *mir.CallInstr) error {
	if len(c.Args) != 0 {
		return unsupported("mir-mvp", "std.term.size requires no arguments")
	}
	if c.Dest == nil {
		return nil
	}
	destLoc := g.fn.Local(c.Dest.Local)
	if destLoc == nil {
		return fmt.Errorf("mir-mvp: term.size dest into unknown local %d", c.Dest.Local)
	}
	okT, _, ok := g.resultSubtypes(destLoc.Type)
	if !ok {
		return unsupported("mir-mvp", "std.term.size dest is not Result<Size, Error>")
	}
	g.declareRuntime(ostyRtTermWidthSymbol, mirRuntimeDeclareLine("i64", ostyRtTermWidthSymbol, ""))
	g.declareRuntime(ostyRtTermHeightSymbol, mirRuntimeDeclareLine("i64", ostyRtTermHeightSymbol, ""))
	width := g.fresh()
	height := g.fresh()
	g.fnBuf.WriteString(mirCallValueNoArgsLine(width, "i64", ostyRtTermWidthSymbol))
	g.fnBuf.WriteString(mirCallValueNoArgsLine(height, "i64", ostyRtTermHeightSymbol))
	sizeValue, err := g.buildAggregateValue(okT, []mirRuntimeArg{
		{typ: "i64", val: width},
		{typ: "i64", val: height},
	})
	if err != nil {
		return err
	}
	payload, err := g.toI64Slot(sizeValue, okT)
	if err != nil {
		return err
	}
	resultLLVM := g.llvmType(destLoc.Type)
	okValue := g.emitResultValue(resultLLVM, true, payload)
	g.fnBuf.WriteString(mirStoreLine(resultLLVM, okValue, g.localSlots[c.Dest.Local]))
	return nil
}

func (g *mirGen) emitStdRandomCall(c *mir.CallInstr, fnRef *mir.FnRef) (bool, error) {
	symbol := fnRef.Symbol
	method := strings.TrimPrefix(symbol, "std.random.")
	args := c.Args
	if strings.HasPrefix(symbol, "Rng__") {
		method = strings.TrimPrefix(symbol, "Rng__")
	}
	switch method {
	case "default":
		if len(args) != 0 {
			return true, unsupported("mir-mvp", "std.random.default requires no arguments")
		}
		return true, g.emitRuntimeCallToDest(c, ostyRtRandomDefaultSymbol, "ptr", nil)
	case "seeded":
		if len(args) != 1 {
			return true, unsupported("mir-mvp", "std.random.seeded requires one Int64 argument")
		}
		seed, err := g.evalTypedArg(args[0], args[0].Type())
		if err != nil {
			return true, err
		}
		return true, g.emitRuntimeCallToDest(c, ostyRtRandomSeededSymbol, "ptr", []mirRuntimeArg{seed})
	case "int", "intInclusive":
		if len(args) != 3 {
			return true, unsupported("mir-mvp", "random.Rng.int/intInclusive requires receiver, min, max")
		}
		rng, err := g.evalTypedArg(args[0], args[0].Type())
		if err != nil {
			return true, err
		}
		minArg, err := g.evalIntArg(args[1], "random.Rng."+method, 0)
		if err != nil {
			return true, err
		}
		maxArg, err := g.evalIntArg(args[2], "random.Rng."+method, 1)
		if err != nil {
			return true, err
		}
		rt := ostyRtRandomIntSymbol
		if method == "intInclusive" {
			rt = ostyRtRandomIntInclusiveSymbol
		}
		return true, g.emitRuntimeCallToDest(c, rt, "i64", []mirRuntimeArg{rng, minArg, maxArg})
	case "float":
		if len(args) != 1 {
			return true, unsupported("mir-mvp", "random.Rng.float requires receiver")
		}
		rng, err := g.evalTypedArg(args[0], args[0].Type())
		if err != nil {
			return true, err
		}
		return true, g.emitRuntimeCallToDest(c, ostyRtRandomFloatSymbol, "double", []mirRuntimeArg{rng})
	case "bool":
		if len(args) != 1 {
			return true, unsupported("mir-mvp", "random.Rng.bool requires receiver")
		}
		rng, err := g.evalTypedArg(args[0], args[0].Type())
		if err != nil {
			return true, err
		}
		return true, g.emitRuntimeCallToDest(c, ostyRtRandomBoolSymbol, "i1", []mirRuntimeArg{rng})
	case "bytes":
		if len(args) != 2 {
			return true, unsupported("mir-mvp", "random.Rng.bytes requires receiver and count")
		}
		rng, err := g.evalTypedArg(args[0], args[0].Type())
		if err != nil {
			return true, err
		}
		n, err := g.evalIntArg(args[1], "random.Rng.bytes", 0)
		if err != nil {
			return true, err
		}
		return true, g.emitRuntimeCallToDest(c, ostyRtRandomBytesSymbol, "ptr", []mirRuntimeArg{rng, n})
	case "shuffle":
		if len(args) != 2 {
			return true, unsupported("mir-mvp", "random.Rng.shuffle requires receiver and list")
		}
		rng, err := g.evalTypedArg(args[0], args[0].Type())
		if err != nil {
			return true, err
		}
		items, err := g.evalTypedArg(args[1], args[1].Type())
		if err != nil {
			return true, err
		}
		if items.typ != "ptr" {
			return true, unsupported("mir-mvp", "random.Rng.shuffle item argument must be List<T>")
		}
		return true, g.emitRuntimeCallToDest(c, ostyRtRandomShuffleSymbol, "void", []mirRuntimeArg{rng, items})
	case "choice":
		return true, g.emitStdRandomChoiceMIR(c)
	}
	return false, nil
}

func (g *mirGen) emitStdRandomChoiceMIR(c *mir.CallInstr) error {
	if len(c.Args) != 2 {
		return unsupported("mir-mvp", "random.Rng.choice requires receiver and list")
	}
	if c.Dest == nil {
		return nil
	}
	rng, err := g.evalTypedArg(c.Args[0], c.Args[0].Type())
	if err != nil {
		return err
	}
	listOp := c.Args[1]
	elemT := listElemType(listOp.Type())
	if elemT == nil {
		return unsupported("mir-mvp", "random.Rng.choice requires List<T>")
	}
	destLoc := g.fn.Local(c.Dest.Local)
	if destLoc == nil {
		return fmt.Errorf("mir-mvp: random.choice dest into unknown local %d", c.Dest.Local)
	}
	optLLVM := g.llvmType(destLoc.Type)
	if mirChoiceUnknownElement(elemT) {
		noneValue := g.emitOptionValue(optLLVM, false, "0")
		g.fnBuf.WriteString(mirStoreLine(optLLVM, noneValue, g.localSlots[c.Dest.Local]))
		return nil
	}
	listReg, err := g.evalOperand(listOp, listOp.Type())
	if err != nil {
		return err
	}
	lenSym := listRuntimeLenSymbol()
	g.declareRuntime(lenSym, mirRuntimeDeclareI64FromPtrLine(lenSym))
	g.declareRuntime(ostyRtRandomIntSymbol, mirRuntimeDeclareLine("i64", ostyRtRandomIntSymbol, "ptr, i64, i64"))
	lenReg := g.fresh()
	g.fnBuf.WriteString(mirCallValueLine(lenReg, "i64", lenSym, mirArgSlotPtr(listReg)))
	empty := g.fresh()
	g.fnBuf.WriteString(mirICmpEqI64Line(empty, lenReg, "0"))
	noneLabel := g.freshLabel("random.choice.none")
	someLabel := g.freshLabel("random.choice.some")
	contLabel := g.freshLabel("random.choice.cont")
	g.fnBuf.WriteString(mirBrCondLine(empty, noneLabel, someLabel))

	g.fnBuf.WriteString(mirLabelLine(noneLabel))
	noneValue := g.emitOptionValue(optLLVM, false, "0")
	g.fnBuf.WriteString(mirBrUncondLine(contLabel))

	g.fnBuf.WriteString(mirLabelLine(someLabel))
	idx := g.fresh()
	g.fnBuf.WriteString(mirCallValueLine(idx, "i64", ostyRtRandomIntSymbol,
		mirRuntimeArgList([]mirRuntimeArg{rng, {typ: "i64", val: "0"}, {typ: "i64", val: lenReg}})))
	elemLLVM := g.llvmType(elemT)
	elemReg, err := g.emitListLoadElement(listReg, idx, elemLLVM)
	if err != nil {
		return err
	}
	payload, err := g.toI64Slot(elemReg, elemT)
	if err != nil {
		return err
	}
	someValue := g.emitOptionValue(optLLVM, true, payload)
	g.fnBuf.WriteString(mirBrUncondLine(contLabel))

	g.fnBuf.WriteString(mirLabelLine(contLabel))
	phi := g.fresh()
	g.fnBuf.WriteString(mirPhiTwoLine(phi, optLLVM, noneValue, noneLabel, someValue, someLabel))
	g.fnBuf.WriteString(mirStoreLine(optLLVM, phi, g.localSlots[c.Dest.Local]))
	return nil
}

func mirChoiceUnknownElement(t mir.Type) bool {
	switch t.(type) {
	case *ir.ErrType, *ir.TypeVar:
		return true
	default:
		return false
	}
}

func (g *mirGen) emitStdMathCall(c *mir.CallInstr, fnRef *mir.FnRef) (bool, error) {
	method := strings.TrimPrefix(fnRef.Symbol, "std.math.")
	switch method {
	case "sin", "cos", "tan", "asin", "acos", "atan", "sinh", "cosh", "tanh", "exp", "log2", "log10", "sqrt", "cbrt", "floor", "ceil", "round", "trunc", "abs":
		if len(c.Args) != 1 {
			return true, unsupportedf("mir-mvp", "std.math.%s requires one Float argument", method)
		}
		arg, err := g.evalFloatAsDouble(c.Args[0])
		if err != nil {
			return true, err
		}
		rt := mathUnaryRuntimeSymbol(method)
		return true, g.emitRuntimeCallToDest(c, rt, "double", []mirRuntimeArg{{typ: "double", val: arg}})
	case "atan2", "pow", "min", "max", "hypot":
		if len(c.Args) != 2 {
			return true, unsupportedf("mir-mvp", "std.math.%s requires two Float arguments", method)
		}
		left, err := g.evalFloatAsDouble(c.Args[0])
		if err != nil {
			return true, err
		}
		right, err := g.evalFloatAsDouble(c.Args[1])
		if err != nil {
			return true, err
		}
		rt := mathBinaryRuntimeSymbol(method)
		return true, g.emitRuntimeCallToDest(c, rt, "double", []mirRuntimeArg{{typ: "double", val: left}, {typ: "double", val: right}})
	case "log":
		if len(c.Args) != 1 && len(c.Args) != 2 {
			return true, unsupported("mir-mvp", "std.math.log requires one or two Float arguments")
		}
		x, err := g.evalFloatAsDouble(c.Args[0])
		if err != nil {
			return true, err
		}
		base := "0.0"
		if len(c.Args) == 2 {
			base, err = g.evalFloatAsDouble(c.Args[1])
			if err != nil {
				return true, err
			}
		}
		return true, g.emitRuntimeCallToDest(c, ostyRtMathLogSymbol, "double", []mirRuntimeArg{{typ: "double", val: x}, {typ: "double", val: base}})
	}
	return false, nil
}

func (g *mirGen) emitFloatPrimitiveMethodCall(c *mir.CallInstr, method, recv, recvLLVM string) (bool, error) {
	recvDouble := recv
	if recvLLVM == "float" {
		recvDouble = g.fresh()
		g.fnBuf.WriteString(mirFPExtFloatToDoubleLine(recvDouble, recv))
	}
	switch method {
	case "abs", "floor", "ceil", "round", "trunc", "fract", "sqrt", "cbrt", "ln", "log2", "log10", "exp", "sin", "cos", "tan", "asin", "acos", "atan", "signum":
		rt := mathFloatMethodRuntimeSymbol(method)
		return true, g.emitFloatRuntimeCallToDest(c, rt, "double", []mirRuntimeArg{{typ: "double", val: recvDouble}})
	case "min", "max", "atan2", "pow":
		if len(c.Args) < 2 {
			return true, unsupported("mir-mvp", "float binary method missing argument")
		}
		other, err := g.evalFloatAsDouble(c.Args[1])
		if err != nil {
			return true, err
		}
		rt := mathFloatMethodRuntimeSymbol(method)
		return true, g.emitFloatRuntimeCallToDest(c, rt, "double", []mirRuntimeArg{{typ: "double", val: recvDouble}, {typ: "double", val: other}})
	case "clamp":
		if len(c.Args) < 3 {
			return true, unsupported("mir-mvp", "float clamp missing arguments")
		}
		lo, err := g.evalFloatAsDouble(c.Args[1])
		if err != nil {
			return true, err
		}
		hi, err := g.evalFloatAsDouble(c.Args[2])
		if err != nil {
			return true, err
		}
		return true, g.emitFloatRuntimeCallToDest(c, ostyRtFloatClampSymbol, "double", []mirRuntimeArg{{typ: "double", val: recvDouble}, {typ: "double", val: lo}, {typ: "double", val: hi}})
	case "isNaN", "isInfinite", "isFinite":
		rt := map[string]string{
			"isNaN":      ostyRtFloatIsNaNSymbol,
			"isInfinite": ostyRtFloatIsInfiniteSymbol,
			"isFinite":   ostyRtFloatIsFiniteSymbol,
		}[method]
		return true, g.emitFloatRuntimeCallToDest(c, rt, "i1", []mirRuntimeArg{{typ: "double", val: recvDouble}})
	case "toFixed":
		if len(c.Args) < 2 {
			return true, unsupported("mir-mvp", "float toFixed missing precision")
		}
		precision, err := g.evalIntArg(c.Args[1], "Float.toFixed", 0)
		if err != nil {
			return true, err
		}
		return true, g.emitFloatRuntimeCallToDest(c, ostyRtFloatToFixedSymbol, "ptr", []mirRuntimeArg{{typ: "double", val: recvDouble}, precision})
	case "toInt":
		return true, g.emitFloatRuntimeCallToDest(c, ostyRtFloatToIntLossySymbol, "i64", []mirRuntimeArg{{typ: "double", val: recvDouble}})
	case "toInt32":
		return true, g.emitFloatRuntimeCallToDest(c, ostyRtFloatToInt32LossySymbol, "i32", []mirRuntimeArg{{typ: "double", val: recvDouble}})
	case "toInt64":
		return true, g.emitFloatRuntimeCallToDest(c, ostyRtFloatToInt64LossySymbol, "i64", []mirRuntimeArg{{typ: "double", val: recvDouble}})
	case "toString":
		return true, g.emitFloatRuntimeCallToDest(c, mirRtFloatToStringSymbol(), "ptr", []mirRuntimeArg{{typ: "double", val: recvDouble}})
	case "toFloat", "toFloat64":
		return true, g.storeCallValue(c, "double", recvDouble)
	case "toFloat32":
		return true, g.storeCallValue(c, recvLLVM, recv)
	case "toBits":
		if recvLLVM == "float" {
			return true, g.emitFloatRuntimeCallToDest(c, ostyRtFloatToBits32Symbol, "i64", []mirRuntimeArg{{typ: "float", val: recv}})
		}
		return true, g.emitFloatRuntimeCallToDest(c, ostyRtFloatToBits64Symbol, "i64", []mirRuntimeArg{{typ: "double", val: recvDouble}})
	}
	return false, nil
}

func (g *mirGen) emitFloatRuntimeCallToDest(c *mir.CallInstr, symbol, retLLVM string, args []mirRuntimeArg) error {
	return g.emitRuntimeCallToDest(c, symbol, retLLVM, args)
}

func (g *mirGen) evalFloatAsDouble(op mir.Operand) (string, error) {
	val, err := g.evalOperand(op, op.Type())
	if err != nil {
		return "", err
	}
	llvmT := g.llvmType(op.Type())
	switch llvmT {
	case "double":
		return val, nil
	case "float":
		tmp := g.fresh()
		g.fnBuf.WriteString(mirFPExtFloatToDoubleLine(tmp, val))
		return tmp, nil
	default:
		return "", unsupportedf("mir-mvp", "float argument type %s", mirTypeString(op.Type()))
	}
}

func mathUnaryRuntimeSymbol(method string) string {
	switch method {
	case "sin":
		return ostyRtFloatSinSymbol
	case "cos":
		return ostyRtFloatCosSymbol
	case "tan":
		return ostyRtFloatTanSymbol
	case "asin":
		return ostyRtFloatAsinSymbol
	case "acos":
		return ostyRtFloatAcosSymbol
	case "atan":
		return ostyRtFloatAtanSymbol
	case "sinh":
		return ostyRtFloatSinhSymbol
	case "cosh":
		return ostyRtFloatCoshSymbol
	case "tanh":
		return ostyRtFloatTanhSymbol
	case "exp":
		return ostyRtFloatExpSymbol
	case "log2":
		return ostyRtFloatLog2Symbol
	case "log10":
		return ostyRtFloatLog10Symbol
	case "sqrt":
		return ostyRtFloatSqrtSymbol
	case "cbrt":
		return ostyRtFloatCbrtSymbol
	case "floor":
		return ostyRtFloatFloorSymbol
	case "ceil":
		return ostyRtFloatCeilSymbol
	case "round":
		return ostyRtFloatRoundSymbol
	case "trunc":
		return ostyRtFloatTruncSymbol
	case "abs":
		return ostyRtFloatAbsSymbol
	}
	return ""
}

func mathBinaryRuntimeSymbol(method string) string {
	switch method {
	case "atan2":
		return ostyRtFloatAtan2Symbol
	case "pow":
		return ostyRtFloatPowSymbol
	case "min":
		return ostyRtFloatMinSymbol
	case "max":
		return ostyRtFloatMaxSymbol
	case "hypot":
		return ostyRtFloatHypotSymbol
	}
	return ""
}

func mathFloatMethodRuntimeSymbol(method string) string {
	switch method {
	case "ln":
		return ostyRtFloatLnSymbol
	case "fract":
		return ostyRtFloatFractSymbol
	case "signum":
		return ostyRtFloatSignumSymbol
	}
	if rt := mathUnaryRuntimeSymbol(method); rt != "" {
		return rt
	}
	return mathBinaryRuntimeSymbol(method)
}

func (g *mirGen) emitStdTestingGenCall(c *mir.CallInstr, fnRef *mir.FnRef) (bool, error) {
	method := strings.TrimPrefix(fnRef.Symbol, "std.testing.gen.")
	switch method {
	case "int", "intRange", "bool", "float", "char", "byte", "asciiString",
		"oneOf", "map", "filter", "pair", "triple", "list", "listOfSize",
		"option", "result", "constant":
		return true, g.storeZeroDest(c)
	}
	return false, nil
}

func (g *mirGen) emitStaticBuiltinTypeCall(c *mir.CallInstr, fnRef *mir.FnRef) (bool, error) {
	if c == nil || fnRef == nil || len(c.Args) == 0 {
		return false, nil
	}
	owner := staticFnConstSymbol(c.Args[0])
	if owner == "" {
		return false, nil
	}
	switch owner {
	case "Bytes":
		kind := bytesStaticIntrinsic(fnRef.Symbol)
		if kind == mir.IntrinsicInvalid {
			return false, nil
		}
		args := append([]mir.Operand(nil), c.Args[1:]...)
		intr := &mir.IntrinsicInstr{Dest: c.Dest, Kind: kind, Args: args, SpanV: c.SpanV}
		return true, g.emitIntrinsic(intr)
	case "String":
		kind := stringStaticIntrinsic(fnRef.Symbol)
		if kind == mir.IntrinsicInvalid {
			return false, nil
		}
		args := append([]mir.Operand(nil), c.Args[1:]...)
		intr := &mir.IntrinsicInstr{Dest: c.Dest, Kind: kind, Args: args, SpanV: c.SpanV}
		return true, g.emitIntrinsic(intr)
	}
	return false, nil
}

func staticFnConstSymbol(op mir.Operand) string {
	co, ok := op.(*mir.ConstOp)
	if !ok || co == nil {
		return ""
	}
	fc, ok := co.Const.(*mir.FnConst)
	if !ok || fc == nil {
		return ""
	}
	return fc.Symbol
}

func bytesStaticIntrinsic(name string) mir.IntrinsicKind {
	switch name {
	case "from":
		return mir.IntrinsicBytesFromList
	case "fromString", "toBytes":
		return mir.IntrinsicBytesFromString
	case "fromHex":
		return mir.IntrinsicBytesFromHex
	case "len":
		return mir.IntrinsicBytesLen
	case "isEmpty":
		return mir.IntrinsicBytesIsEmpty
	case "get":
		return mir.IntrinsicBytesGet
	case "contains":
		return mir.IntrinsicBytesContains
	case "startsWith":
		return mir.IntrinsicBytesStartsWith
	case "endsWith":
		return mir.IntrinsicBytesEndsWith
	case "indexOf":
		return mir.IntrinsicBytesIndexOf
	case "lastIndexOf":
		return mir.IntrinsicBytesLastIndexOf
	case "split":
		return mir.IntrinsicBytesSplit
	case "join":
		return mir.IntrinsicBytesJoin
	case "concat":
		return mir.IntrinsicBytesConcat
	case "repeat":
		return mir.IntrinsicBytesRepeat
	case "replace":
		return mir.IntrinsicBytesReplace
	case "replaceAll":
		return mir.IntrinsicBytesReplaceAll
	case "trimLeft":
		return mir.IntrinsicBytesTrimLeft
	case "trimRight":
		return mir.IntrinsicBytesTrimRight
	case "trim":
		return mir.IntrinsicBytesTrim
	case "trimSpace":
		return mir.IntrinsicBytesTrimSpace
	case "toUpper":
		return mir.IntrinsicBytesToUpper
	case "toLower":
		return mir.IntrinsicBytesToLower
	case "toHex":
		return mir.IntrinsicBytesToHex
	case "slice":
		return mir.IntrinsicBytesSlice
	case "toString":
		return mir.IntrinsicBytesToString
	}
	return mir.IntrinsicInvalid
}

func stringStaticIntrinsic(name string) mir.IntrinsicKind {
	switch name {
	case "len":
		return mir.IntrinsicStringLen
	case "isEmpty":
		return mir.IntrinsicStringIsEmpty
	case "contains":
		return mir.IntrinsicStringContains
	case "count":
		return mir.IntrinsicStringCount
	case "startsWith", "hasPrefix":
		return mir.IntrinsicStringStartsWith
	case "endsWith", "hasSuffix":
		return mir.IntrinsicStringEndsWith
	case "indexOf":
		return mir.IntrinsicStringIndexOf
	case "lastIndexOf":
		return mir.IntrinsicStringLastIndexOf
	case "split":
		return mir.IntrinsicStringSplit
	case "splitN":
		return mir.IntrinsicStringSplitN
	case "fields":
		return mir.IntrinsicStringFields
	case "join":
		return mir.IntrinsicStringJoin
	case "concat":
		return mir.IntrinsicStringConcat
	case "substring", "slice":
		return mir.IntrinsicStringSubstring
	case "trim", "trimSpace":
		return mir.IntrinsicStringTrim
	case "trimStart":
		return mir.IntrinsicStringTrimStart
	case "trimEnd":
		return mir.IntrinsicStringTrimEnd
	case "trimPrefix":
		return mir.IntrinsicStringTrimPrefix
	case "trimSuffix":
		return mir.IntrinsicStringTrimSuffix
	case "toUpper":
		return mir.IntrinsicStringToUpper
	case "toLower":
		return mir.IntrinsicStringToLower
	case "toInt":
		return mir.IntrinsicStringToInt
	case "toFloat":
		return mir.IntrinsicStringToFloat
	case "replace":
		return mir.IntrinsicStringReplace
	case "replaceAll":
		return mir.IntrinsicStringReplaceAll
	case "repeat":
		return mir.IntrinsicStringRepeat
	case "chars":
		return mir.IntrinsicStringChars
	case "bytes":
		return mir.IntrinsicStringBytes
	case "toBytes":
		return mir.IntrinsicBytesFromString
	}
	return mir.IntrinsicInvalid
}

func (g *mirGen) emitStdErrorCall(c *mir.CallInstr, fnRef *mir.FnRef) (bool, error) {
	method := strings.TrimPrefix(fnRef.Symbol, "Error__")
	switch method {
	case "message":
		if len(c.Args) != 1 {
			return true, unsupported("mir-mvp", "Error.message requires receiver")
		}
		errPtr, err := g.evalTypedArg(c.Args[0], c.Args[0].Type())
		if err != nil {
			return true, err
		}
		if errPtr.typ != "ptr" {
			return true, unsupported("mir-mvp", "Error.message receiver must lower to ptr")
		}
		return true, g.storeCallValue(c, "ptr", errPtr.val)
	case "source":
		if len(c.Args) != 1 {
			return true, unsupported("mir-mvp", "Error.source requires receiver")
		}
		if c.Dest == nil {
			return true, nil
		}
		destLoc := g.fn.Local(c.Dest.Local)
		if destLoc == nil {
			return true, fmt.Errorf("mir-mvp: Error.source dest into unknown local %d", c.Dest.Local)
		}
		optLLVM := g.llvmType(destLoc.Type)
		none := g.emitOptionValue(optLLVM, false, "0")
		g.fnBuf.WriteString(mirStoreLine(optLLVM, none, g.localSlots[c.Dest.Local]))
		return true, nil
	}
	return false, nil
}

func (g *mirGen) emitTestingPropertyMIR(c *mir.CallInstr, method string) error {
	switch method {
	case "property":
		if len(c.Args) != 3 {
			return unsupported("mir-mvp", "std.testing.property requires name, generator, predicate")
		}
	case "propertyN", "propertySeeded":
		if len(c.Args) != 4 {
			return unsupported("mir-mvp", "std.testing."+method+" requires four arguments")
		}
	}
	return g.storeUnitDestIfAny(c)
}

func (g *mirGen) emitMapUpdateCall(c *mir.CallInstr, fnRef *mir.FnRef) (bool, error) {
	if len(c.Args) != 3 {
		return true, unsupported("mir-mvp", "Map.update requires receiver, key, closure")
	}
	mapT := c.Args[0].Type()
	keyT, valT := mapKeyValueTypes(mapT)
	if keyT == nil || valT == nil {
		return true, unsupported("mir-mvp", "Map.update receiver is not Map<K,V>")
	}
	mapReg, err := g.evalOperand(c.Args[0], mapT)
	if err != nil {
		return true, err
	}
	keyReg, err := g.evalOperand(c.Args[1], keyT)
	if err != nil {
		return true, err
	}
	closureEnv, err := g.evalOperand(c.Args[2], c.Args[2].Type())
	if err != nil {
		return true, err
	}
	fnT, ok := c.Args[2].Type().(*ir.FnType)
	if !ok || len(fnT.Params) != 1 {
		return true, unsupported("mir-mvp", "Map.update closure must be fn(V?) -> V")
	}
	optionT := fnT.Params[0]
	optionLLVM := g.llvmType(optionT)
	keyLLVM := g.llvmType(keyT)
	valLLVM := g.llvmType(valT)
	keyString := isStringLLVMType(keyT)

	lockSym := mapRuntimeLockSymbol()
	unlockSym := mapRuntimeUnlockSymbol()
	g.declareRuntime(lockSym, mirRuntimeDeclareVoidFromPtrLine(lockSym))
	g.declareRuntime(unlockSym, mirRuntimeDeclareVoidFromPtrLine(unlockSym))
	g.fnBuf.WriteString(mirCallVoidLine(lockSym, mirArgSlotPtr(mapReg)))

	present, outSlot := g.emitMapGetProbe(mapReg, keyReg, keyLLVM, keyString, valLLVM)
	someLabel := g.freshLabel("map.update.some")
	noneLabel := g.freshLabel("map.update.none")
	contLabel := g.freshLabel("map.update.cont")
	g.fnBuf.WriteString(mirBrCondLine(present, someLabel, noneLabel))

	g.fnBuf.WriteString(mirLabelLine(someLabel))
	oldVal := g.fresh()
	g.fnBuf.WriteString(mirLoadLine(oldVal, valLLVM, outSlot))
	payload, err := g.toI64Slot(oldVal, valT)
	if err != nil {
		return true, err
	}
	someValue := g.emitOptionValue(optionLLVM, true, payload)
	g.fnBuf.WriteString(mirBrUncondLine(contLabel))

	g.fnBuf.WriteString(mirLabelLine(noneLabel))
	noneValue := g.emitOptionValue(optionLLVM, false, "0")
	g.fnBuf.WriteString(mirBrUncondLine(contLabel))

	g.fnBuf.WriteString(mirLabelLine(contLabel))
	currentOpt := g.fresh()
	g.fnBuf.WriteString(mirPhiTwoLine(currentOpt, optionLLVM, someValue, someLabel, noneValue, noneLabel))

	fnPtr := g.fresh()
	g.fnBuf.WriteString(mirLoadLine(fnPtr, "ptr", closureEnv))
	retLLVM := g.llvmType(valT)
	callType := retLLVM + " (ptr, " + optionLLVM + ")"
	nextVal := g.fresh()
	g.fnBuf.WriteString(mirCallIndirectValueLine(nextVal, callType, fnPtr, mirArgSlotPtr(closureEnv)+", "+optionLLVM+" "+currentOpt))

	insertSym := mapRuntimeInsertSymbol(keyLLVM, keyString)
	g.declareRuntime(insertSym, mirRuntimeDeclareMapInsertLine(insertSym, keyLLVM))
	valueSlot := g.spillToSlot(nextVal, retLLVM)
	g.fnBuf.WriteString(mirCallVoidLine(insertSym, mirArgSlotPtr(mapReg)+", "+keyLLVM+" "+keyReg+", "+mirArgSlotPtr(valueSlot)))
	g.fnBuf.WriteString(mirCallVoidLine(unlockSym, mirArgSlotPtr(mapReg)))
	return true, g.storeUnitDestIfAny(c)
}
