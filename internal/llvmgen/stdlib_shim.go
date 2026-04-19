// stdlib_shim.go — routes qualified calls to `use std.strings as X` aliases
// through the `osty_rt_strings_*` C runtime helpers. The pure Osty bodies for
// these functions (see internal/stdlib/modules/strings.osty) depend on
// features the native backend still can't lower (Char iteration, List<Char>
// indexing); shimming directly to the runtime keeps toolchain/*.osty
// callsites working until the stdlib-body injection path matures.
package llvmgen

import (
	"github.com/osty/osty/internal/ast"
)

func collectStdStringsAliases(file *ast.File) map[string]bool {
	out := map[string]bool{}
	if file == nil {
		return out
	}
	for _, use := range file.Uses {
		if use == nil || use.IsFFI() {
			continue
		}
		if len(use.Path) != 2 || use.Path[0] != "std" || use.Path[1] != "strings" {
			continue
		}
		alias := use.Alias
		if alias == "" {
			alias = "strings"
		}
		out[alias] = true
	}
	return out
}

func (g *generator) emitStdStringsCall(call *ast.CallExpr) (value, bool, error) {
	if call == nil || len(g.stdStringsAliases) == 0 {
		return value{}, false, nil
	}
	field, ok := call.Fn.(*ast.FieldExpr)
	if !ok {
		return value{}, false, nil
	}
	alias, ok := field.X.(*ast.Ident)
	if !ok || !g.stdStringsAliases[alias.Name] {
		return value{}, false, nil
	}
	switch field.Name {
	case "compare":
		v, err := g.emitStdStringsBinary(call, "compare", "i64", llvmStringRuntimeCompareSymbol())
		return v, true, err
	case "hasPrefix":
		v, err := g.emitStdStringsBinary(call, "hasPrefix", "i1", llvmStringRuntimeHasPrefixSymbol())
		return v, true, err
	}
	return value{}, false, nil
}

func (g *generator) emitStdStringsBinary(call *ast.CallExpr, name, retTyp, symbol string) (value, error) {
	if len(call.Args) != 2 {
		return value{}, unsupportedf("call", "strings.%s expects 2 arguments, got %d", name, len(call.Args))
	}
	left, err := g.emitStdStringsArg(call.Args[0], name, 0)
	if err != nil {
		return value{}, err
	}
	right, err := g.emitStdStringsArg(call.Args[1], name, 1)
	if err != nil {
		return value{}, err
	}
	g.declareRuntimeSymbol(symbol, retTyp, []paramInfo{{typ: "ptr"}, {typ: "ptr"}})
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, retTyp, symbol, []*LlvmValue{toOstyValue(left), toOstyValue(right)})
	g.takeOstyEmitter(emitter)
	return fromOstyValue(out), nil
}

func (g *generator) emitStdStringsArg(arg *ast.Arg, name string, index int) (value, error) {
	if arg == nil || arg.Name != "" || arg.Value == nil {
		return value{}, unsupportedf("call", "strings.%s requires positional arguments", name)
	}
	v, err := g.emitExpr(arg.Value)
	if err != nil {
		return value{}, err
	}
	loaded, err := g.loadIfPointer(v)
	if err != nil {
		return value{}, err
	}
	if loaded.typ != "ptr" {
		return value{}, unsupportedf("type-system", "strings.%s arg %d type %s, want String", name, index+1, loaded.typ)
	}
	return loaded, nil
}
