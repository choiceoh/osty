package llvmgen

import (
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/mir"
)

// std.log shim — intercepts the four canonical entry points
// (`log.{debug,info,warn,error}(msg)`) at the AST level and lowers
// each call to `osty_rt_io_write("[level] " + msg, newline=true,
// stderr=true)`. The Osty body in `internal/stdlib/modules/log.osty`
// remains the source of truth for surface, types, and richer paths
// (Logger, Handler interface, fields rendering); the shim provides
// the spec-mandated stderr output for the canonical 1-argument
// callers until the LLVM backend grows enough type/struct support
// to lower the Osty body directly.

func collectStdLogAliases(file *ast.File) map[string]bool {
	out := map[string]bool{}
	if file == nil {
		return out
	}
	for _, use := range file.Uses {
		if use == nil || use.IsFFI() {
			continue
		}
		if len(use.Path) != 2 || use.Path[0] != "std" || use.Path[1] != "log" {
			continue
		}
		alias := use.Alias
		if alias == "" {
			alias = "log"
		}
		out[alias] = true
	}
	return out
}

func isStdLogLevelMethod(name string) bool {
	switch name {
	case "debug", "info", "warn", "error":
		return true
	}
	return false
}

func (g *generator) stdLogCallMethod(call *ast.CallExpr) (string, bool) {
	field, ok := g.stdLogCallField(call)
	if !ok || !isStdLogLevelMethod(field.Name) {
		return "", false
	}
	return field.Name, true
}

func (g *generator) stdLogCallField(call *ast.CallExpr) (*ast.FieldExpr, bool) {
	field, ok := fieldExprOfCallFn(call)
	if !ok || field.IsOptional {
		return nil, false
	}
	alias, ok := field.X.(*ast.Ident)
	if !ok || alias == nil || !g.stdLogAliases[alias.Name] {
		return nil, false
	}
	return field, true
}

func (g *generator) emitStdLogCallStmt(call *ast.CallExpr) (bool, error) {
	method, ok := g.stdLogCallMethod(call)
	if !ok {
		return false, nil
	}
	return true, g.emitStdLogWriteCall(call, method)
}

// emitStdLogWriteCall lowers `log.<level>(msg)` (1-arg form). The
// 2-arg form `log.<level>(msg, fields)` is intentionally not
// intercepted here — it falls through to the regular call path so
// the Osty body in `internal/stdlib/modules/log.osty` (which knows
// how to render fields through `TextHandler.handle`) is exercised
// once the backend supports the constructs it uses.
func (g *generator) emitStdLogWriteCall(call *ast.CallExpr, method string) error {
	if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
		return nil
	}
	msgVal, err := g.emitExpr(call.Args[0].Value)
	if err != nil {
		return err
	}
	msgStr, err := g.emitStdIoStringValue(msgVal, "log."+method)
	if err != nil {
		return err
	}
	emitter := g.toOstyEmitter()
	prefixOut := llvmStringLiteral(emitter, "["+method+"] ")
	g.takeOstyEmitter(emitter)
	prefixVal := fromOstyValue(prefixOut)
	prefixVal.sourceType = &ast.NamedType{Path: []string{"String"}}
	full, err := g.emitRuntimeStringConcat(prefixVal, msgStr)
	if err != nil {
		return err
	}
	g.declareRuntimeSymbol(ostyRtIOWriteSymbol, "void", []paramInfo{{typ: "ptr"}, {typ: "i1"}, {typ: "i1"}})
	emitter2 := g.toOstyEmitter()
	llvmCallVoid(emitter2, ostyRtIOWriteSymbol, []*LlvmValue{
		toOstyValue(full),
		llvmStdIoBoolValue(true),
		llvmStdIoBoolValue(true),
	})
	g.takeOstyEmitter(emitter2)
	return nil
}

// emitStdLogCallMIR is the MIR-level analogue of emitStdLogCallStmt.
// `osty test` and `osty build` lower through MIR rather than direct
// AST emission, so the same intercept must exist at this level. The
// 1-arg variant is handled here; richer call shapes return
// `handled=false` so the MIR generator can fall through and emit the
// regular `unresolved symbol` diagnostic, surfacing the gap honestly.
func (g *mirGen) emitStdLogCallMIR(c *mir.CallInstr, fnRef *mir.FnRef) (bool, error) {
	method := strings.TrimPrefix(fnRef.Symbol, "std.log.")
	if !isStdLogLevelMethod(method) {
		return false, nil
	}
	if len(c.Args) != 1 {
		return false, nil
	}
	if err := g.emitStdLogWriteArgsMIR(c.Args, method); err != nil {
		return true, err
	}
	return true, g.storeUnitDestIfAny(c)
}

func (g *mirGen) emitStdLogWriteArgsMIR(args []mir.Operand, method string) error {
	text, err := g.emitStdIoStringOperandMIR(args[0], "log."+method)
	if err != nil {
		return err
	}
	prefix := g.testingStringLiteral("[" + method + "] ")
	msgVal := &LlvmValue{typ: "ptr", name: text}
	full := g.emitStringConcatN([]*LlvmValue{prefix, msgVal})
	g.declareRuntime(ostyRtIOWriteSymbol, mirRuntimeDeclareLine("void", ostyRtIOWriteSymbol, "ptr, i1, i1"))
	g.fnBuf.WriteString(mirCallVoidLine(ostyRtIOWriteSymbol,
		mirArgSlotPtr(full.name)+", "+mirArgSlotI1(llvmStdIoI1Text(true))+", "+mirArgSlotI1(llvmStdIoI1Text(true))))
	return nil
}
