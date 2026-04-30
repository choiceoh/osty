package llvmgen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/mir"
)

const ostyRtCmdShellEscapeSymbol = "osty_rt_cmd_shell_escape"
const ostyRtCmdShellLineSymbol = "osty_rt_cmd_shell_line"
const ostyRtCmdPipelineShellLineSymbol = "osty_rt_cmd_pipeline_shell_line"

func (g *mirGen) emitStdCmdCall(c *mir.CallInstr, fnRef *mir.FnRef) (bool, error) {
	if fnRef == nil {
		return false, nil
	}
	switch fnRef.Symbol {
	case "std.cmd.command", "cmd.command":
		return true, g.emitStdCmdConstruct(c, false, false)
	case "std.cmd.commandArgs", "cmd.commandArgs":
		return true, g.emitStdCmdConstruct(c, false, true)
	case "std.cmd.shell", "cmd.shell":
		return true, g.emitStdCmdConstruct(c, true, false)
	case "std.cmd.pipeline", "cmd.pipeline":
		return true, g.emitStdCmdPipelineConstruct(c)
	case "std.cmd.pipe", "cmd.pipe":
		return true, g.emitStdCmdPipe(c)
	case "std.cmd.shellEscape", "cmd.shellEscape":
		return true, g.emitStdCmdShellEscape(c)
	case "std.cmd.joinEscaped", "cmd.joinEscaped":
		return true, g.emitStdCmdJoinEscaped(c)
	}
	if method, ok := g.stdCmdMethodName(fnRef.Symbol, "Command", c); ok {
		return true, g.emitStdCmdCommandMethod(c, method)
	}
	if method, ok := g.stdCmdMethodName(fnRef.Symbol, "RunOutput", c); ok {
		return true, g.emitStdCmdRunOutputMethod(c, method)
	}
	if method, ok := g.stdCmdMethodName(fnRef.Symbol, "Pipeline", c); ok {
		return true, g.emitStdCmdPipelineMethod(c, method)
	}
	return false, nil
}

func (g *mirGen) stdCmdMethodName(symbol, typeName string, c *mir.CallInstr) (string, bool) {
	marker := typeName + "__"
	idx := strings.LastIndex(symbol, marker)
	if idx < 0 {
		return "", false
	}
	if idx > 0 && symbol[idx-1] != '.' {
		return "", false
	}
	if len(c.Args) == 0 {
		return "", false
	}
	nt, _ := c.Args[0].Type().(*ir.NamedType)
	switch typeName {
	case "Command":
		if !g.isStdCmdCommandType(nt) {
			return "", false
		}
	case "RunOutput":
		if !g.isStdCmdRunOutputType(nt) {
			return "", false
		}
	case "Pipeline":
		if !g.isStdCmdPipelineType(nt) {
			return "", false
		}
	default:
		return "", false
	}
	return symbol[idx+len(marker):], true
}

func (g *mirGen) emitStdCmdPipelineConstruct(c *mir.CallInstr) error {
	if len(c.Args) != 1 {
		return unsupported("mir-mvp", "std.cmd.pipeline requires one List<Command> argument")
	}
	if c.Dest == nil {
		return nil
	}
	destLoc := g.fn.Local(c.Dest.Local)
	if destLoc == nil {
		return fmt.Errorf("mir-mvp: std.cmd.pipeline dest into unknown local %d", c.Dest.Local)
	}
	stages, err := g.evalTypedArg(c.Args[0], c.Args[0].Type())
	if err != nil {
		return err
	}
	if stages.typ != "ptr" {
		return unsupported("mir-mvp", "std.cmd.pipeline stages must be List<Command>")
	}
	value, err := g.buildAggregateValue(destLoc.Type, []mirRuntimeArg{
		stages,
		{typ: "ptr", val: g.stringLiteral("")},
		{typ: "ptr", val: g.emitStdCmdEmptyStringMap()},
		{typ: "i64", val: "0"},
	})
	if err != nil {
		return err
	}
	return g.storeLLVMValueIntoPlace(*c.Dest, &LlvmValue{typ: g.llvmType(destLoc.Type), name: value}, "std.cmd.pipeline")
}

func (g *mirGen) emitStdCmdPipe(c *mir.CallInstr) error {
	if len(c.Args) != 2 {
		return unsupported("mir-mvp", "std.cmd.pipe requires two Command arguments")
	}
	listReg := g.emitStdCmdEmptyStringList()
	for _, arg := range c.Args {
		cmdVal, err := g.evalOperand(arg, arg.Type())
		if err != nil {
			return err
		}
		g.emitStdCmdListPushCommand(listReg, cmdVal, g.llvmType(arg.Type()))
	}
	if c.Dest == nil {
		return nil
	}
	destLoc := g.fn.Local(c.Dest.Local)
	if destLoc == nil {
		return fmt.Errorf("mir-mvp: std.cmd.pipe dest into unknown local %d", c.Dest.Local)
	}
	value, err := g.buildAggregateValue(destLoc.Type, []mirRuntimeArg{
		{typ: "ptr", val: listReg},
		{typ: "ptr", val: g.stringLiteral("")},
		{typ: "ptr", val: g.emitStdCmdEmptyStringMap()},
		{typ: "i64", val: "0"},
	})
	if err != nil {
		return err
	}
	return g.storeLLVMValueIntoPlace(*c.Dest, &LlvmValue{typ: g.llvmType(destLoc.Type), name: value}, "std.cmd.pipe")
}

func (g *mirGen) emitStdCmdListPushCommand(listReg, value, commandLLVM string) {
	sym := listRuntimePushBytesV1Symbol()
	g.declareRuntime(sym, mirRuntimeDeclareBytesV1PushLine(sym))
	em := g.ostyEmitter()
	slot := llvmSpillToSlot(em, &LlvmValue{typ: commandLLVM, name: value})
	size := llvmSizeOf(em, commandLLVM)
	llvmCallVoid(em, sym, []*LlvmValue{
		{typ: "ptr", name: listReg},
		slot,
		size,
	})
	g.flushOstyEmitter(em)
}

func (g *mirGen) emitStdCmdConstruct(c *mir.CallInstr, useShell bool, hasArgs bool) error {
	wantArgs := 1
	if hasArgs {
		wantArgs = 2
	}
	if len(c.Args) != wantArgs {
		return unsupported("mir-mvp", "std.cmd command constructor argument shape")
	}
	if c.Dest == nil {
		return nil
	}
	destLoc := g.fn.Local(c.Dest.Local)
	if destLoc == nil {
		return fmt.Errorf("mir-mvp: std.cmd constructor dest into unknown local %d", c.Dest.Local)
	}
	program, err := g.evalStringArg(c.Args[0], "std.cmd.command", 0)
	if err != nil {
		return err
	}
	args := mirRuntimeArg{typ: "ptr", val: g.emitStdCmdEmptyStringList()}
	if hasArgs {
		args, err = g.evalTypedArg(c.Args[1], c.Args[1].Type())
		if err != nil {
			return err
		}
		if args.typ != "ptr" {
			return unsupported("mir-mvp", "std.cmd.commandArgs args must be List<String>")
		}
	}
	shellText := llvmStdIoI1Text(useShell)
	value, err := g.buildAggregateValue(destLoc.Type, []mirRuntimeArg{
		program,
		args,
		{typ: "ptr", val: g.stringLiteral("")},
		{typ: "ptr", val: g.emitStdCmdEmptyStringMap()},
		{typ: "i64", val: "0"},
		{typ: "i1", val: shellText},
	})
	if err != nil {
		return err
	}
	return g.storeLLVMValueIntoPlace(*c.Dest, &LlvmValue{typ: g.llvmType(destLoc.Type), name: value}, "std.cmd constructor")
}

func (g *mirGen) emitStdCmdEmptyStringList() string {
	g.declareRuntime(listRuntimeNewSymbol(), mirRuntimeDeclareLine("ptr", listRuntimeNewSymbol(), ""))
	em := g.ostyEmitter()
	listVal := llvmListNew(em)
	g.flushOstyEmitter(em)
	return listVal.name
}

func (g *mirGen) emitStdCmdEmptyStringMap() string {
	keyKind := containerAbiKind("ptr", true)
	valKind := containerAbiKind("ptr", true)
	valueSize := mapValueSizeBytes("ptr")
	sym := mirRtMapNewSymbol()
	g.declareRuntime(sym, mirRuntimeDeclareLine("ptr", sym, "i64, i64, i64, ptr"))
	out := g.fresh()
	g.fnBuf.WriteString(mirCallValueLine(out, "ptr", sym, mirRuntimeArgList([]mirRuntimeArg{
		{typ: "i64", val: strconv.Itoa(keyKind)},
		{typ: "i64", val: strconv.Itoa(valKind)},
		{typ: "i64", val: strconv.Itoa(valueSize)},
		{typ: "ptr", val: "null"},
	})))
	return out
}

func (g *mirGen) emitStdCmdShellEscape(c *mir.CallInstr) error {
	if len(c.Args) != 1 {
		return unsupported("mir-mvp", "std.cmd.shellEscape requires one String argument")
	}
	arg, err := g.evalStringArg(c.Args[0], "std.cmd.shellEscape", 0)
	if err != nil {
		return err
	}
	g.declareRuntime(ostyRtCmdShellEscapeSymbol, mirRuntimeDeclareLine("ptr", ostyRtCmdShellEscapeSymbol, "ptr"))
	out := g.fresh()
	g.fnBuf.WriteString(mirCallValueLine(out, "ptr", ostyRtCmdShellEscapeSymbol, mirRuntimeArgList([]mirRuntimeArg{arg})))
	return g.storeStdCmdPtrResult(c, out, "std.cmd.shellEscape")
}

func (g *mirGen) emitStdCmdJoinEscaped(c *mir.CallInstr) error {
	if len(c.Args) != 2 {
		return unsupported("mir-mvp", "std.cmd.joinEscaped requires program and args")
	}
	program, err := g.evalStringArg(c.Args[0], "std.cmd.joinEscaped", 0)
	if err != nil {
		return err
	}
	args, err := g.evalTypedArg(c.Args[1], c.Args[1].Type())
	if err != nil {
		return err
	}
	if args.typ != "ptr" {
		return unsupported("mir-mvp", "std.cmd.joinEscaped args must be List<String>")
	}
	out := g.emitStdCmdShellLineCall(program.val, args.val, "false")
	return g.storeStdCmdPtrResult(c, out, "std.cmd.joinEscaped")
}

func (g *mirGen) emitStdCmdShellLineCall(program, args, useShell string) string {
	g.declareRuntime(ostyRtCmdShellLineSymbol, mirRuntimeDeclareLine("ptr", ostyRtCmdShellLineSymbol, "ptr, ptr, i1"))
	out := g.fresh()
	g.fnBuf.WriteString(mirCallValueLine(out, "ptr", ostyRtCmdShellLineSymbol, mirRuntimeArgList([]mirRuntimeArg{
		{typ: "ptr", val: program},
		{typ: "ptr", val: args},
		{typ: "i1", val: useShell},
	})))
	return out
}

func (g *mirGen) storeStdCmdPtrResult(c *mir.CallInstr, value, ctx string) error {
	if c.Dest == nil {
		return nil
	}
	return g.storeLLVMValueIntoPlace(*c.Dest, &LlvmValue{typ: "ptr", name: value}, ctx)
}

func (g *mirGen) emitStdCmdCommandMethod(c *mir.CallInstr, method string) error {
	switch method {
	case "arg":
		return g.emitStdCmdCommandArg(c)
	case "withArgs":
		return g.emitStdCmdCommandReplacePtrField(c, method, 1, 1)
	case "withCwd":
		return g.emitStdCmdCommandReplacePtrField(c, method, 2, 1)
	case "withEnv":
		return g.emitStdCmdCommandWithEnv(c)
	case "withEnvMap":
		return g.emitStdCmdCommandReplacePtrField(c, method, 3, 1)
	case "withTimeoutMillis":
		return g.emitStdCmdCommandReplaceIntField(c, method, 4, 1)
	case "run":
		return g.emitStdCmdCommandRun(c)
	case "shellLine":
		return g.emitStdCmdCommandShellLine(c)
	default:
		return unsupported("mir-mvp", "std.cmd Command method "+method)
	}
}

func (g *mirGen) evalStdCmdCommandArg(c *mir.CallInstr) (string, string, error) {
	if len(c.Args) == 0 {
		return "", "", unsupported("mir-mvp", "std.cmd Command method missing receiver")
	}
	selfT := c.Args[0].Type()
	selfLLVM := g.llvmType(selfT)
	self, err := g.evalOperand(c.Args[0], selfT)
	if err != nil {
		return "", "", err
	}
	return self, selfLLVM, nil
}

func (g *mirGen) storeStdCmdCommandResult(c *mir.CallInstr, self, selfLLVM, ctx string) error {
	if c.Dest == nil {
		return nil
	}
	return g.storeLLVMValueIntoPlace(*c.Dest, &LlvmValue{typ: selfLLVM, name: self}, ctx)
}

func (g *mirGen) extractStdCmdCommandField(self, selfLLVM string, idx int) string {
	out := g.fresh()
	g.fnBuf.WriteString(mirExtractValueLine(out, selfLLVM, self, strconv.Itoa(idx)))
	return out
}

func (g *mirGen) replaceStdCmdCommandField(self, selfLLVM, fieldLLVM, val string, idx int) string {
	out := g.fresh()
	g.fnBuf.WriteString(mirInsertValueAggLine(out, selfLLVM, self, fieldLLVM, val, strconv.Itoa(idx)))
	return out
}

func (g *mirGen) emitStdCmdCommandArg(c *mir.CallInstr) error {
	if len(c.Args) != 2 {
		return unsupported("mir-mvp", "std.cmd.Command.arg arity")
	}
	self, selfLLVM, err := g.evalStdCmdCommandArg(c)
	if err != nil {
		return err
	}
	value, err := g.evalStringArg(c.Args[1], "std.cmd.Command.arg", 1)
	if err != nil {
		return err
	}
	args := g.extractStdCmdCommandField(self, selfLLVM, 1)
	g.emitStdCmdListPushString(args, value.val)
	return g.storeStdCmdCommandResult(c, self, selfLLVM, "std.cmd.Command.arg")
}

func (g *mirGen) emitStdCmdListPushString(listReg, value string) {
	sym := listRuntimePushSymbolFor("ptr", true)
	g.declareRuntime(sym, mirRuntimeDeclareLine("void", sym, "ptr, ptr"))
	em := g.ostyEmitter()
	llvmListPushString(em,
		&LlvmValue{typ: "ptr", name: listReg, pointer: false},
		&LlvmValue{typ: "ptr", name: value, pointer: false})
	g.flushOstyEmitter(em)
}

func (g *mirGen) emitStdCmdCommandReplacePtrField(c *mir.CallInstr, method string, fieldIdx int, argIdx int) error {
	if len(c.Args) != argIdx+1 {
		return unsupported("mir-mvp", "std.cmd.Command."+method+" arity")
	}
	self, selfLLVM, err := g.evalStdCmdCommandArg(c)
	if err != nil {
		return err
	}
	value, err := g.evalTypedArg(c.Args[argIdx], c.Args[argIdx].Type())
	if err != nil {
		return err
	}
	if value.typ != "ptr" {
		return unsupported("mir-mvp", "std.cmd.Command."+method+" value must lower to ptr")
	}
	next := g.replaceStdCmdCommandField(self, selfLLVM, "ptr", value.val, fieldIdx)
	return g.storeStdCmdCommandResult(c, next, selfLLVM, "std.cmd.Command."+method)
}

func (g *mirGen) emitStdCmdCommandReplaceIntField(c *mir.CallInstr, method string, fieldIdx int, argIdx int) error {
	if len(c.Args) != argIdx+1 {
		return unsupported("mir-mvp", "std.cmd.Command."+method+" arity")
	}
	self, selfLLVM, err := g.evalStdCmdCommandArg(c)
	if err != nil {
		return err
	}
	value, err := g.evalIntArg(c.Args[argIdx], "std.cmd.Command."+method, argIdx)
	if err != nil {
		return err
	}
	next := g.replaceStdCmdCommandField(self, selfLLVM, "i64", value.val, fieldIdx)
	return g.storeStdCmdCommandResult(c, next, selfLLVM, "std.cmd.Command."+method)
}

func (g *mirGen) emitStdCmdCommandWithEnv(c *mir.CallInstr) error {
	if len(c.Args) != 3 {
		return unsupported("mir-mvp", "std.cmd.Command.withEnv arity")
	}
	self, selfLLVM, err := g.evalStdCmdCommandArg(c)
	if err != nil {
		return err
	}
	name, err := g.evalStringArg(c.Args[1], "std.cmd.Command.withEnv", 1)
	if err != nil {
		return err
	}
	value, err := g.evalStringArg(c.Args[2], "std.cmd.Command.withEnv", 2)
	if err != nil {
		return err
	}
	env := g.extractStdCmdCommandField(self, selfLLVM, 3)
	g.emitStdCmdMapInsertString(env, name.val, value.val)
	return g.storeStdCmdCommandResult(c, self, selfLLVM, "std.cmd.Command.withEnv")
}

func (g *mirGen) emitStdCmdMapInsertString(mapReg, key, value string) {
	sym := mapRuntimeInsertSymbol("ptr", true)
	g.declareRuntime(sym, mirRuntimeDeclareMapInsertLine(sym, "ptr"))
	em := g.ostyEmitter()
	slot := llvmSpillToSlot(em, &LlvmValue{typ: "ptr", name: value})
	llvmMapInsert(em,
		&LlvmValue{typ: "ptr", name: mapReg},
		&LlvmValue{typ: "ptr", name: key},
		slot,
		true)
	g.flushOstyEmitter(em)
}

func (g *mirGen) emitStdCmdCommandShellLine(c *mir.CallInstr) error {
	if len(c.Args) != 1 {
		return unsupported("mir-mvp", "std.cmd.Command.shellLine arity")
	}
	fields, err := g.evalStdCmdCommandFields(c)
	if err != nil {
		return err
	}
	out := g.emitStdCmdShellLineCall(fields.program, fields.args, fields.useShell)
	return g.storeStdCmdPtrResult(c, out, "std.cmd.Command.shellLine")
}

type stdCmdCommandFields struct {
	program  string
	args     string
	cwd      string
	env      string
	timeout  string
	useShell string
}

func (g *mirGen) evalStdCmdCommandFields(c *mir.CallInstr) (stdCmdCommandFields, error) {
	self, selfLLVM, err := g.evalStdCmdCommandArg(c)
	if err != nil {
		return stdCmdCommandFields{}, err
	}
	return stdCmdCommandFields{
		program:  g.extractStdCmdCommandField(self, selfLLVM, 0),
		args:     g.extractStdCmdCommandField(self, selfLLVM, 1),
		cwd:      g.extractStdCmdCommandField(self, selfLLVM, 2),
		env:      g.extractStdCmdCommandField(self, selfLLVM, 3),
		timeout:  g.extractStdCmdCommandField(self, selfLLVM, 4),
		useShell: g.extractStdCmdCommandField(self, selfLLVM, 5),
	}, nil
}

func (g *mirGen) emitStdCmdCommandRun(c *mir.CallInstr) error {
	if len(c.Args) != 1 {
		return unsupported("mir-mvp", "std.cmd.Command.run arity")
	}
	fields, err := g.evalStdCmdCommandFields(c)
	if err != nil {
		return err
	}
	return g.emitStdCmdRunWithFields(c, fields.program, fields.args, fields.useShell, fields.cwd, fields.env, fields.timeout, "std.cmd.Command.run")
}

func (g *mirGen) emitStdCmdRunWithFields(c *mir.CallInstr, program, args, useShell, cwd, env, timeout, label string) error {
	g.declareRuntime(ostyRtOsExecOptionsSymbol, mirRuntimeDeclareLine("ptr", ostyRtOsExecOptionsSymbol, "ptr, ptr, i1, ptr, ptr, i64"))
	g.declareRuntime(ostyRtOsExecResultFreeSymbol, mirRuntimeDeclareLine("void", ostyRtOsExecResultFreeSymbol, "ptr"))
	raw := g.fresh()
	g.fnBuf.WriteString(mirCallValueLine(raw, "ptr", ostyRtOsExecOptionsSymbol, mirRuntimeArgList([]mirRuntimeArg{
		{typ: "ptr", val: program},
		{typ: "ptr", val: args},
		{typ: "i1", val: useShell},
		{typ: "ptr", val: cwd},
		{typ: "ptr", val: env},
		{typ: "i64", val: timeout},
	})))
	tag := g.emitRecordFieldLoad(raw, stdOsExecRuntimeRecordLLVMType, "i64", 0)
	exitCode := g.emitRecordFieldLoad(raw, stdOsExecRuntimeRecordLLVMType, "i64", 1)
	timedOut := g.emitRecordFieldLoad(raw, stdOsExecRuntimeRecordLLVMType, "i1", 2)
	stdoutText := g.emitRecordFieldLoad(raw, stdOsExecRuntimeRecordLLVMType, "ptr", 3)
	stderrText := g.emitRecordFieldLoad(raw, stdOsExecRuntimeRecordLLVMType, "ptr", 4)
	errText := g.emitRecordFieldLoad(raw, stdOsExecRuntimeRecordLLVMType, "ptr", 5)
	g.fnBuf.WriteString(mirCallVoidLine(ostyRtOsExecResultFreeSymbol, mirArgSlotPtr(raw)))
	if c.Dest == nil {
		return nil
	}
	destLoc := g.fn.Local(c.Dest.Local)
	if destLoc == nil {
		return fmt.Errorf("mir-mvp: %s dest into unknown local %d", label, c.Dest.Local)
	}
	okT, _, ok := g.resultSubtypes(destLoc.Type)
	if !ok {
		return unsupported("mir-mvp", "std.cmd.Command.run dest is not Result<RunOutput, Error>")
	}
	resultLLVM := g.llvmType(destLoc.Type)
	failed := g.fresh()
	g.fnBuf.WriteString(mirICmpEqI64Line(failed, tag, "1"))
	errLabel := g.freshLabel("cmd.run.err")
	okLabel := g.freshLabel("cmd.run.ok")
	contLabel := g.freshLabel("cmd.run.cont")
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
		{typ: "i1", val: timedOut},
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

func (g *mirGen) emitStdCmdPipelineMethod(c *mir.CallInstr, method string) error {
	switch method {
	case "run":
		return g.emitStdCmdPipelineRun(c)
	case "shellLine":
		return g.emitStdCmdPipelineShellLine(c)
	case "withCwd":
		return g.emitStdCmdPipelineReplacePtrField(c, method, 1, 1)
	case "withEnvMap":
		return g.emitStdCmdPipelineReplacePtrField(c, method, 2, 1)
	case "withTimeoutMillis":
		return g.emitStdCmdPipelineReplaceIntField(c, method, 3, 1)
	case "withEnv":
		return g.emitStdCmdPipelineWithEnv(c)
	case "stage":
		return g.emitStdCmdPipelineStage(c)
	default:
		return unsupported("mir-mvp", "std.cmd Pipeline method "+method)
	}
}

func (g *mirGen) evalStdCmdPipelineArg(c *mir.CallInstr) (string, string, error) {
	if len(c.Args) == 0 {
		return "", "", unsupported("mir-mvp", "std.cmd Pipeline method missing receiver")
	}
	selfT := c.Args[0].Type()
	selfLLVM := g.llvmType(selfT)
	self, err := g.evalOperand(c.Args[0], selfT)
	if err != nil {
		return "", "", err
	}
	return self, selfLLVM, nil
}

func (g *mirGen) extractStdCmdPipelineField(self, selfLLVM string, idx int) string {
	out := g.fresh()
	g.fnBuf.WriteString(mirExtractValueLine(out, selfLLVM, self, strconv.Itoa(idx)))
	return out
}

func (g *mirGen) replaceStdCmdPipelineField(self, selfLLVM, fieldLLVM, val string, idx int) string {
	out := g.fresh()
	g.fnBuf.WriteString(mirInsertValueAggLine(out, selfLLVM, self, fieldLLVM, val, strconv.Itoa(idx)))
	return out
}

func (g *mirGen) storeStdCmdPipelineResult(c *mir.CallInstr, self, selfLLVM, ctx string) error {
	if c.Dest == nil {
		return nil
	}
	return g.storeLLVMValueIntoPlace(*c.Dest, &LlvmValue{typ: selfLLVM, name: self}, ctx)
}

func (g *mirGen) emitStdCmdPipelineShellLineValue(stages string) string {
	g.declareRuntime(ostyRtCmdPipelineShellLineSymbol, mirRuntimeDeclareLine("ptr", ostyRtCmdPipelineShellLineSymbol, "ptr"))
	out := g.fresh()
	g.fnBuf.WriteString(mirCallValueLine(out, "ptr", ostyRtCmdPipelineShellLineSymbol, mirRuntimeArgList([]mirRuntimeArg{{typ: "ptr", val: stages}})))
	return out
}

func (g *mirGen) emitStdCmdPipelineRun(c *mir.CallInstr) error {
	if len(c.Args) != 1 {
		return unsupported("mir-mvp", "std.cmd.Pipeline.run arity")
	}
	self, selfLLVM, err := g.evalStdCmdPipelineArg(c)
	if err != nil {
		return err
	}
	stages := g.extractStdCmdPipelineField(self, selfLLVM, 0)
	cwd := g.extractStdCmdPipelineField(self, selfLLVM, 1)
	env := g.extractStdCmdPipelineField(self, selfLLVM, 2)
	timeout := g.extractStdCmdPipelineField(self, selfLLVM, 3)
	line := g.emitStdCmdPipelineShellLineValue(stages)
	return g.emitStdCmdRunWithFields(c, line, "null", "true", cwd, env, timeout, "std.cmd.Pipeline.run")
}

func (g *mirGen) emitStdCmdPipelineShellLine(c *mir.CallInstr) error {
	if len(c.Args) != 1 {
		return unsupported("mir-mvp", "std.cmd.Pipeline.shellLine arity")
	}
	self, selfLLVM, err := g.evalStdCmdPipelineArg(c)
	if err != nil {
		return err
	}
	stages := g.extractStdCmdPipelineField(self, selfLLVM, 0)
	line := g.emitStdCmdPipelineShellLineValue(stages)
	if c.Dest == nil {
		return nil
	}
	destLoc := g.fn.Local(c.Dest.Local)
	if destLoc == nil {
		return fmt.Errorf("mir-mvp: std.cmd.Pipeline.shellLine dest into unknown local %d", c.Dest.Local)
	}
	resultLLVM := g.llvmType(destLoc.Type)
	payload, err := g.toI64Slot(line, ir.TString)
	if err != nil {
		return err
	}
	okValue := g.emitResultValue(resultLLVM, true, payload)
	g.fnBuf.WriteString(mirStoreLine(resultLLVM, okValue, g.localSlots[c.Dest.Local]))
	return nil
}

func (g *mirGen) emitStdCmdPipelineReplacePtrField(c *mir.CallInstr, method string, fieldIdx int, argIdx int) error {
	if len(c.Args) != argIdx+1 {
		return unsupported("mir-mvp", "std.cmd.Pipeline."+method+" arity")
	}
	self, selfLLVM, err := g.evalStdCmdPipelineArg(c)
	if err != nil {
		return err
	}
	value, err := g.evalTypedArg(c.Args[argIdx], c.Args[argIdx].Type())
	if err != nil {
		return err
	}
	if value.typ != "ptr" {
		return unsupported("mir-mvp", "std.cmd.Pipeline."+method+" value must lower to ptr")
	}
	next := g.replaceStdCmdPipelineField(self, selfLLVM, "ptr", value.val, fieldIdx)
	return g.storeStdCmdPipelineResult(c, next, selfLLVM, "std.cmd.Pipeline."+method)
}

func (g *mirGen) emitStdCmdPipelineReplaceIntField(c *mir.CallInstr, method string, fieldIdx int, argIdx int) error {
	if len(c.Args) != argIdx+1 {
		return unsupported("mir-mvp", "std.cmd.Pipeline."+method+" arity")
	}
	self, selfLLVM, err := g.evalStdCmdPipelineArg(c)
	if err != nil {
		return err
	}
	value, err := g.evalIntArg(c.Args[argIdx], "std.cmd.Pipeline."+method, argIdx)
	if err != nil {
		return err
	}
	next := g.replaceStdCmdPipelineField(self, selfLLVM, "i64", value.val, fieldIdx)
	return g.storeStdCmdPipelineResult(c, next, selfLLVM, "std.cmd.Pipeline."+method)
}

func (g *mirGen) emitStdCmdPipelineWithEnv(c *mir.CallInstr) error {
	if len(c.Args) != 3 {
		return unsupported("mir-mvp", "std.cmd.Pipeline.withEnv arity")
	}
	self, selfLLVM, err := g.evalStdCmdPipelineArg(c)
	if err != nil {
		return err
	}
	name, err := g.evalStringArg(c.Args[1], "std.cmd.Pipeline.withEnv", 1)
	if err != nil {
		return err
	}
	value, err := g.evalStringArg(c.Args[2], "std.cmd.Pipeline.withEnv", 2)
	if err != nil {
		return err
	}
	env := g.extractStdCmdPipelineField(self, selfLLVM, 2)
	g.emitStdCmdMapInsertString(env, name.val, value.val)
	return g.storeStdCmdPipelineResult(c, self, selfLLVM, "std.cmd.Pipeline.withEnv")
}

func (g *mirGen) emitStdCmdPipelineStage(c *mir.CallInstr) error {
	if len(c.Args) != 2 {
		return unsupported("mir-mvp", "std.cmd.Pipeline.stage arity")
	}
	self, selfLLVM, err := g.evalStdCmdPipelineArg(c)
	if err != nil {
		return err
	}
	next, err := g.evalOperand(c.Args[1], c.Args[1].Type())
	if err != nil {
		return err
	}
	stages := g.extractStdCmdPipelineField(self, selfLLVM, 0)
	g.emitStdCmdListPushCommand(stages, next, g.llvmType(c.Args[1].Type()))
	return g.storeStdCmdPipelineResult(c, self, selfLLVM, "std.cmd.Pipeline.stage")
}

func (g *mirGen) emitStdCmdRunOutputMethod(c *mir.CallInstr, method string) error {
	switch method {
	case "ok":
		return g.emitStdCmdRunOutputOk(c)
	default:
		return unsupported("mir-mvp", "std.cmd RunOutput method "+method)
	}
}

func (g *mirGen) emitStdCmdRunOutputOk(c *mir.CallInstr) error {
	if len(c.Args) != 1 {
		return unsupported("mir-mvp", "std.cmd.RunOutput.ok arity")
	}
	selfT := c.Args[0].Type()
	selfLLVM := g.llvmType(selfT)
	self, err := g.evalOperand(c.Args[0], selfT)
	if err != nil {
		return err
	}
	exitCode := g.fresh()
	g.fnBuf.WriteString(mirExtractValueLine(exitCode, selfLLVM, self, "0"))
	timedOut := g.fresh()
	g.fnBuf.WriteString(mirExtractValueLine(timedOut, selfLLVM, self, "3"))
	zero := g.fresh()
	g.fnBuf.WriteString(mirICmpEqI64Line(zero, exitCode, "0"))
	notTimedOut := g.fresh()
	g.fnBuf.WriteString(mirXorI1Line(notTimedOut, timedOut, "true"))
	ok := g.fresh()
	g.fnBuf.WriteString(mirBinaryOpLine(ok, "and", "i1", zero, notTimedOut))
	if c.Dest == nil {
		return nil
	}
	return g.storeLLVMValueIntoPlace(*c.Dest, &LlvmValue{typ: "i1", name: ok}, "std.cmd.RunOutput.ok")
}
