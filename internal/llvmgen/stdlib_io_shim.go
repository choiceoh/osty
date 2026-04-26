package llvmgen

import "github.com/osty/osty/internal/ast"

const ostyRtIOWriteSymbol = "osty_rt_io_write"
const ostyRtIOReadLineSymbol = "osty_rt_io_read_line"

func collectStdIoAliases(file *ast.File) map[string]bool {
	out := map[string]bool{}
	if file == nil {
		return out
	}
	for _, use := range file.Uses {
		if use == nil || use.IsFFI() {
			continue
		}
		if len(use.Path) != 2 || use.Path[0] != "std" || use.Path[1] != "io" {
			continue
		}
		alias := use.Alias
		if alias == "" {
			alias = "io"
		}
		out[alias] = true
	}
	return out
}

// isStdIoOutputMethod reports whether `name` is one of the four
// std.io output methods. Delegates to the Osty-sourced
// `mirIsStdIoOutputMethod` (`toolchain/mir_generator.osty`).
func isStdIoOutputMethod(name string) bool {
	return mirIsStdIoOutputMethod(name)
}

func stdIoWriteFlags(name string) (newline bool, toStderr bool, ok bool) {
	switch name {
	case "print":
		return false, false, true
	case "println":
		return true, false, true
	case "eprint":
		return false, true, true
	case "eprintln":
		return true, true, true
	default:
		return false, false, false
	}
}

func (g *generator) stdIoCallMethod(call *ast.CallExpr) (string, bool) {
	field, ok := g.stdIoCallField(call)
	if !ok || !isStdIoOutputMethod(field.Name) {
		return "", false
	}
	return field.Name, true
}

func (g *generator) stdIoCallField(call *ast.CallExpr) (*ast.FieldExpr, bool) {
	field, ok := fieldExprOfCallFn(call)
	if !ok || field.IsOptional {
		return nil, false
	}
	alias, ok := field.X.(*ast.Ident)
	if !ok || alias == nil || !g.stdIoAliases[alias.Name] {
		return nil, false
	}
	return field, true
}

func stdIoBareCallName(call *ast.CallExpr) (string, bool) {
	if call == nil {
		return "", false
	}
	id, ok := call.Fn.(*ast.Ident)
	if !ok || id == nil || !isStdIoOutputMethod(id.Name) {
		return "", false
	}
	return id.Name, true
}

func (g *generator) emitStdIoCallStmt(call *ast.CallExpr) (bool, error) {
	method, ok := g.stdIoCallMethod(call)
	if !ok {
		method, ok = stdIoBareCallName(call)
		if !ok {
			return false, nil
		}
	}
	return true, g.emitStdIoWriteCall(call, method)
}

func (g *generator) emitStdIoCall(call *ast.CallExpr) (value, bool, error) {
	field, ok := g.stdIoCallField(call)
	if !ok || field.Name != "readLine" {
		return value{}, false, nil
	}
	v, err := g.emitStdIoReadLineCall(call)
	return v, true, err
}

func (g *generator) emitStdIoWriteCall(call *ast.CallExpr, method string) error {
	if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
		return unsupportedf("call", "%s requires one positional argument", method)
	}
	v, err := g.emitExpr(call.Args[0].Value)
	if err != nil {
		return err
	}
	text, err := g.emitStdIoStringValue(v, method)
	if err != nil {
		return err
	}
	newline, toStderr, ok := stdIoWriteFlags(method)
	if !ok {
		return unsupportedf("call", "std.io.%s is not supported by LLVM yet", method)
	}
	g.declareRuntimeSymbol(ostyRtIOWriteSymbol, "void", []paramInfo{{typ: "ptr"}, {typ: "i1"}, {typ: "i1"}})
	emitter := g.toOstyEmitter()
	llvmCallVoid(emitter, ostyRtIOWriteSymbol, []*LlvmValue{
		toOstyValue(text),
		llvmStdIoBoolValue(newline),
		llvmStdIoBoolValue(toStderr),
	})
	g.takeOstyEmitter(emitter)
	return nil
}

func (g *generator) emitStdIoReadLineCall(call *ast.CallExpr) (value, error) {
	field, ok := g.stdIoCallField(call)
	if !ok || field.Name != "readLine" {
		return value{}, unsupported("call", "std.io.readLine requires a std.io alias receiver")
	}
	if len(call.Args) != 0 {
		return value{}, unsupported("call", "std.io.readLine requires no arguments")
	}
	g.declareRuntimeSymbol(ostyRtIOReadLineSymbol, "ptr", nil)
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, "ptr", ostyRtIOReadLineSymbol, nil)
	g.takeOstyEmitter(emitter)
	text := fromOstyValue(out)
	text.gcManaged = true
	text.sourceType = &ast.NamedType{Path: []string{"String"}}
	return text, nil
}

func llvmStdIoBoolValue(v bool) *LlvmValue {
	if v {
		return &LlvmValue{typ: "i1", name: "true"}
	}
	return &LlvmValue{typ: "i1", name: "false"}
}

func (g *generator) emitStdIoStringValue(v value, method string) (value, error) {
	switch v.typ {
	case "ptr":
		return v, nil
	case "i64":
		return g.emitRuntimeIntToString(v)
	case "double":
		return g.emitRuntimeFloatToString(v)
	case "i1":
		return g.emitRuntimeBoolToString(v)
	case "i32":
		return g.emitRuntimeCharToString(v)
	case "i8":
		return g.emitRuntimeByteToString(v)
	default:
		return value{}, unsupportedf("type-system", "%s currently supports String, Int, Float, Bool, Char, and Byte values only (got %s)", method, v.typ)
	}
}
