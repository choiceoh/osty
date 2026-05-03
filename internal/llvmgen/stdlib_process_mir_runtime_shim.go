package llvmgen

import (
	"fmt"
	"strings"

	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/mir"
)

const ostyRtProcessPipelineShellLineSymbol = "osty_rt_process_pipeline_shell_line"

func (g *mirGen) emitStdProcessFacadeCall(c *mir.CallInstr, fnRef *mir.FnRef) (bool, error) {
	if fnRef == nil {
		return false, nil
	}
	switch fnRef.Symbol {
	case "std.process.command", "process.command":
		return true, g.emitStdProcessCommandConstruct(c, false, false)
	case "std.process.commandArgs", "process.commandArgs":
		return true, g.emitStdProcessCommandConstruct(c, false, true)
	case "std.process.shell", "process.shell":
		return true, g.emitStdProcessCommandConstruct(c, true, false)
	case "std.process.pipeline", "process.pipeline":
		return true, g.emitStdProcessPipelineConstruct(c)
	case "std.process.pipe", "process.pipe":
		return true, g.emitStdProcessPipe(c)
	case "std.process.run", "process.run":
		return true, g.emitStdProcessRunFree(c, false)
	case "std.process.runShell", "process.runShell":
		return true, g.emitStdProcessRunFree(c, true)
	case "std.process.shellEscape", "process.shellEscape":
		return true, g.emitStdCmdShellEscape(c)
	case "std.process.joinEscaped", "process.joinEscaped":
		return true, g.emitStdCmdJoinEscaped(c)
	}
	if method, ok := g.stdProcessMethodName(fnRef.Symbol, "ProcessCommand", c); ok {
		return true, g.emitStdProcessCommandMethod(c, method)
	}
	if method, ok := g.stdProcessMethodName(fnRef.Symbol, "ProcessOutput", c); ok {
		return true, g.emitStdProcessOutputMethod(c, method)
	}
	if method, ok := g.stdProcessMethodName(fnRef.Symbol, "ProcessPipeline", c); ok {
		return true, g.emitStdProcessPipelineMethod(c, method)
	}
	return false, nil
}

func (g *mirGen) stdProcessMethodName(symbol, typeName string, c *mir.CallInstr) (string, bool) {
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
	case "ProcessCommand":
		if !g.isStdProcessCommandType(nt) {
			return "", false
		}
	case "ProcessOutput":
		if !g.isStdProcessOutputType(nt) {
			return "", false
		}
	case "ProcessPipeline":
		if !g.isStdProcessPipelineType(nt) {
			return "", false
		}
	default:
		return "", false
	}
	return symbol[idx+len(marker):], true
}

func (g *mirGen) emitStdProcessCommandConstruct(c *mir.CallInstr, useShell bool, hasArgs bool) error {
	wantArgs := 1
	if hasArgs {
		wantArgs = 2
	}
	if len(c.Args) != wantArgs {
		return unsupported("mir-mvp", "std.process command constructor argument shape")
	}
	if c.Dest == nil {
		return nil
	}
	destLoc := g.fn.Local(c.Dest.Local)
	if destLoc == nil {
		return fmt.Errorf("mir-mvp: std.process constructor dest into unknown local %d", c.Dest.Local)
	}
	program, err := g.evalStringArg(c.Args[0], "std.process.command", 0)
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
			return unsupported("mir-mvp", "std.process.commandArgs args must be List<String>")
		}
	}
	value, err := g.buildAggregateValue(destLoc.Type, []mirRuntimeArg{
		program,
		args,
		{typ: "ptr", val: g.stringLiteral("")},
		{typ: "ptr", val: g.emitStdCmdEmptyStringMap()},
		{typ: "i64", val: "0"},
		{typ: "i1", val: llvmStdIoI1Text(useShell)},
		{typ: "ptr", val: g.stringLiteral("")},
		{typ: "i1", val: "false"},
	})
	if err != nil {
		return err
	}
	return g.storeLLVMValueIntoPlace(*c.Dest, &LlvmValue{typ: g.llvmType(destLoc.Type), name: value}, "std.process constructor")
}

func (g *mirGen) emitStdProcessPipelineConstruct(c *mir.CallInstr) error {
	if len(c.Args) != 1 {
		return unsupported("mir-mvp", "std.process.pipeline requires one List<ProcessCommand> argument")
	}
	if c.Dest == nil {
		return nil
	}
	destLoc := g.fn.Local(c.Dest.Local)
	if destLoc == nil {
		return fmt.Errorf("mir-mvp: std.process.pipeline dest into unknown local %d", c.Dest.Local)
	}
	stages, err := g.evalTypedArg(c.Args[0], c.Args[0].Type())
	if err != nil {
		return err
	}
	if stages.typ != "ptr" {
		return unsupported("mir-mvp", "std.process.pipeline stages must be List<ProcessCommand>")
	}
	value, err := g.buildAggregateValue(destLoc.Type, []mirRuntimeArg{
		stages,
		{typ: "ptr", val: g.stringLiteral("")},
		{typ: "ptr", val: g.emitStdCmdEmptyStringMap()},
		{typ: "i64", val: "0"},
		{typ: "ptr", val: g.stringLiteral("")},
		{typ: "i1", val: "false"},
	})
	if err != nil {
		return err
	}
	return g.storeLLVMValueIntoPlace(*c.Dest, &LlvmValue{typ: g.llvmType(destLoc.Type), name: value}, "std.process.pipeline")
}

func (g *mirGen) emitStdProcessPipe(c *mir.CallInstr) error {
	if len(c.Args) != 2 {
		return unsupported("mir-mvp", "std.process.pipe requires two ProcessCommand arguments")
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
		return fmt.Errorf("mir-mvp: std.process.pipe dest into unknown local %d", c.Dest.Local)
	}
	value, err := g.buildAggregateValue(destLoc.Type, []mirRuntimeArg{
		{typ: "ptr", val: listReg},
		{typ: "ptr", val: g.stringLiteral("")},
		{typ: "ptr", val: g.emitStdCmdEmptyStringMap()},
		{typ: "i64", val: "0"},
		{typ: "ptr", val: g.stringLiteral("")},
		{typ: "i1", val: "false"},
	})
	if err != nil {
		return err
	}
	return g.storeLLVMValueIntoPlace(*c.Dest, &LlvmValue{typ: g.llvmType(destLoc.Type), name: value}, "std.process.pipe")
}

func (g *mirGen) emitStdProcessCommandMethod(c *mir.CallInstr, method string) error {
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
	case "withStdin":
		return g.emitStdProcessCommandWithStdin(c)
	case "pipe":
		return g.emitStdProcessCommandPipe(c)
	case "run":
		return g.emitStdProcessCommandRun(c)
	case "shellLine":
		return g.emitStdCmdCommandShellLine(c)
	default:
		return unsupported("mir-mvp", "std.process ProcessCommand method "+method)
	}
}

func (g *mirGen) emitStdProcessCommandWithStdin(c *mir.CallInstr) error {
	if len(c.Args) != 2 {
		return unsupported("mir-mvp", "std.process.ProcessCommand.withStdin arity")
	}
	self, selfLLVM, err := g.evalStdCmdCommandArg(c)
	if err != nil {
		return err
	}
	text, err := g.evalStringArg(c.Args[1], "std.process.ProcessCommand.withStdin", 1)
	if err != nil {
		return err
	}
	next := g.replaceStdCmdCommandField(self, selfLLVM, "ptr", text.val, 6)
	next = g.replaceStdCmdCommandField(next, selfLLVM, "i1", "true", 7)
	return g.storeStdCmdCommandResult(c, next, selfLLVM, "std.process.ProcessCommand.withStdin")
}

func (g *mirGen) emitStdProcessCommandPipe(c *mir.CallInstr) error {
	if len(c.Args) != 2 {
		return unsupported("mir-mvp", "std.process.ProcessCommand.pipe arity")
	}
	return g.emitStdProcessPipe(c)
}

func (g *mirGen) evalStdProcessCommandFields(c *mir.CallInstr) (stdProcessCommandFields, error) {
	self, selfLLVM, err := g.evalStdCmdCommandArg(c)
	if err != nil {
		return stdProcessCommandFields{}, err
	}
	return stdProcessCommandFields{
		program:  g.extractStdCmdCommandField(self, selfLLVM, 0),
		args:     g.extractStdCmdCommandField(self, selfLLVM, 1),
		cwd:      g.extractStdCmdCommandField(self, selfLLVM, 2),
		env:      g.extractStdCmdCommandField(self, selfLLVM, 3),
		timeout:  g.extractStdCmdCommandField(self, selfLLVM, 4),
		useShell: g.extractStdCmdCommandField(self, selfLLVM, 5),
		stdin:    g.extractStdCmdCommandField(self, selfLLVM, 6),
		hasStdin: g.extractStdCmdCommandField(self, selfLLVM, 7),
	}, nil
}

type stdProcessCommandFields struct {
	program  string
	args     string
	cwd      string
	env      string
	timeout  string
	useShell string
	stdin    string
	hasStdin string
}

func (g *mirGen) emitStdProcessCommandRun(c *mir.CallInstr) error {
	if len(c.Args) != 1 {
		return unsupported("mir-mvp", "std.process.ProcessCommand.run arity")
	}
	fields, err := g.evalStdProcessCommandFields(c)
	if err != nil {
		return err
	}
	return g.emitStdProcessRunWithFields(c, fields.program, fields.args, fields.useShell, fields.cwd, fields.env, fields.timeout, fields.stdin, fields.hasStdin, "std.process.ProcessCommand.run")
}

func (g *mirGen) emitStdProcessRunFree(c *mir.CallInstr, shell bool) error {
	if shell {
		if len(c.Args) != 1 {
			return unsupported("mir-mvp", "std.process.runShell requires one String argument")
		}
		line, err := g.evalStringArg(c.Args[0], "std.process.runShell", 0)
		if err != nil {
			return err
		}
		return g.emitStdProcessRunWithFields(c, line.val, "null", "true", g.stringLiteral(""), g.emitStdCmdEmptyStringMap(), "0", g.stringLiteral(""), "false", "std.process.runShell")
	}
	if len(c.Args) < 1 || len(c.Args) > 2 {
		return unsupported("mir-mvp", "std.process.run argument shape")
	}
	program, err := g.evalStringArg(c.Args[0], "std.process.run", 0)
	if err != nil {
		return err
	}
	args := g.emitStdCmdEmptyStringList()
	if len(c.Args) == 2 {
		argVal, err := g.evalTypedArg(c.Args[1], c.Args[1].Type())
		if err != nil {
			return err
		}
		if argVal.typ != "ptr" {
			return unsupported("mir-mvp", "std.process.run args must be List<String>")
		}
		args = argVal.val
	}
	return g.emitStdProcessRunWithFields(c, program.val, args, "false", g.stringLiteral(""), g.emitStdCmdEmptyStringMap(), "0", g.stringLiteral(""), "false", "std.process.run")
}

func (g *mirGen) emitStdProcessRunWithFields(c *mir.CallInstr, program, args, useShell, cwd, env, timeout, stdin, hasStdin, label string) error {
	g.declareRuntime(ostyRtOsExecInputOptionsSymbol, mirRuntimeDeclareLine("ptr", ostyRtOsExecInputOptionsSymbol, "ptr, ptr, i1, ptr, ptr, i64, ptr"))
	g.declareRuntime(ostyRtOsExecResultFreeSymbol, mirRuntimeDeclareLine("void", ostyRtOsExecResultFreeSymbol, "ptr"))
	stdinArg := g.fresh()
	g.fnBuf.WriteString(fmt.Sprintf("  %s = select i1 %s, ptr %s, ptr null\n", stdinArg, hasStdin, stdin))
	raw := g.fresh()
	g.fnBuf.WriteString(mirCallValueLine(raw, "ptr", ostyRtOsExecInputOptionsSymbol, mirRuntimeArgList([]mirRuntimeArg{
		{typ: "ptr", val: program},
		{typ: "ptr", val: args},
		{typ: "i1", val: useShell},
		{typ: "ptr", val: cwd},
		{typ: "ptr", val: env},
		{typ: "i64", val: timeout},
		{typ: "ptr", val: stdinArg},
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
		return unsupported("mir-mvp", label+" dest is not Result<ProcessOutput, Error>")
	}
	resultLLVM := g.llvmType(destLoc.Type)
	failed := g.fresh()
	g.fnBuf.WriteString(mirICmpEqI64Line(failed, tag, "1"))
	errLabel := g.freshLabel("process.run.err")
	okLabel := g.freshLabel("process.run.ok")
	contLabel := g.freshLabel("process.run.cont")
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

func (g *mirGen) emitStdProcessPipelineMethod(c *mir.CallInstr, method string) error {
	switch method {
	case "run":
		return g.emitStdProcessPipelineRun(c)
	case "shellLine":
		return g.emitStdProcessPipelineShellLine(c)
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
	case "withStdin":
		return g.emitStdProcessPipelineWithStdin(c)
	default:
		return unsupported("mir-mvp", "std.process ProcessPipeline method "+method)
	}
}

func (g *mirGen) emitStdProcessPipelineRun(c *mir.CallInstr) error {
	if len(c.Args) != 1 {
		return unsupported("mir-mvp", "std.process.ProcessPipeline.run arity")
	}
	self, selfLLVM, err := g.evalStdCmdPipelineArg(c)
	if err != nil {
		return err
	}
	stages := g.extractStdCmdPipelineField(self, selfLLVM, 0)
	cwd := g.extractStdCmdPipelineField(self, selfLLVM, 1)
	env := g.extractStdCmdPipelineField(self, selfLLVM, 2)
	timeout := g.extractStdCmdPipelineField(self, selfLLVM, 3)
	stdin := g.extractStdCmdPipelineField(self, selfLLVM, 4)
	hasStdin := g.extractStdCmdPipelineField(self, selfLLVM, 5)
	line := g.emitStdProcessPipelineShellLineValue(stages)
	return g.emitStdProcessRunWithFields(c, line, "null", "true", cwd, env, timeout, stdin, hasStdin, "std.process.ProcessPipeline.run")
}

func (g *mirGen) emitStdProcessPipelineShellLine(c *mir.CallInstr) error {
	if len(c.Args) != 1 {
		return unsupported("mir-mvp", "std.process.ProcessPipeline.shellLine arity")
	}
	self, selfLLVM, err := g.evalStdCmdPipelineArg(c)
	if err != nil {
		return err
	}
	stages := g.extractStdCmdPipelineField(self, selfLLVM, 0)
	line := g.emitStdProcessPipelineShellLineValue(stages)
	if c.Dest == nil {
		return nil
	}
	destLoc := g.fn.Local(c.Dest.Local)
	if destLoc == nil {
		return fmt.Errorf("mir-mvp: std.process.ProcessPipeline.shellLine dest into unknown local %d", c.Dest.Local)
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

func (g *mirGen) emitStdProcessPipelineShellLineValue(stages string) string {
	g.declareRuntime(ostyRtProcessPipelineShellLineSymbol, mirRuntimeDeclareLine("ptr", ostyRtProcessPipelineShellLineSymbol, "ptr"))
	out := g.fresh()
	g.fnBuf.WriteString(mirCallValueLine(out, "ptr", ostyRtProcessPipelineShellLineSymbol, mirRuntimeArgList([]mirRuntimeArg{{typ: "ptr", val: stages}})))
	return out
}

func (g *mirGen) emitStdProcessPipelineWithStdin(c *mir.CallInstr) error {
	if len(c.Args) != 2 {
		return unsupported("mir-mvp", "std.process.ProcessPipeline.withStdin arity")
	}
	self, selfLLVM, err := g.evalStdCmdPipelineArg(c)
	if err != nil {
		return err
	}
	text, err := g.evalStringArg(c.Args[1], "std.process.ProcessPipeline.withStdin", 1)
	if err != nil {
		return err
	}
	next := g.replaceStdCmdPipelineField(self, selfLLVM, "ptr", text.val, 4)
	next = g.replaceStdCmdPipelineField(next, selfLLVM, "i1", "true", 5)
	return g.storeStdCmdPipelineResult(c, next, selfLLVM, "std.process.ProcessPipeline.withStdin")
}

func (g *mirGen) emitStdProcessOutputMethod(c *mir.CallInstr, method string) error {
	switch method {
	case "ok":
		return g.emitStdCmdRunOutputOk(c)
	default:
		return unsupported("mir-mvp", "std.process ProcessOutput method "+method)
	}
}
