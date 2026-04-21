// stdlib_env_shim.go — backend shim for `std.env` surface.
//
// The pure-Osty stdlib body in internal/stdlib/modules/env.osty is a
// placeholder returning an empty list; this shim bypasses it so real
// argv reaches the program. Only `env.args()` is covered — other
// std.env surface (get, require, vars, currentDir, …) still falls
// through to LLVM015 and needs its own runtime helper + switch arm.
package llvmgen

import (
	"github.com/osty/osty/internal/ast"
)

const ostyRtEnvArgsSymbol = "osty_rt_env_args"
const ostyRtEnvArgsInitSymbol = "osty_rt_env_args_init"

// Shared across every env.args() call site so type inference doesn't
// re-allocate an identical `List<String>` AST tree per reference.
var stdEnvArgsSourceTypeSingleton ast.Type = &ast.NamedType{
	Path: []string{"List"},
	Args: []ast.Type{&ast.NamedType{Path: []string{"String"}}},
}

func collectStdEnvAliases(file *ast.File) map[string]bool {
	out := map[string]bool{}
	if file == nil {
		return out
	}
	for _, use := range file.Uses {
		if use == nil || use.IsFFI() {
			continue
		}
		if len(use.Path) != 2 || use.Path[0] != "std" || use.Path[1] != "env" {
			continue
		}
		alias := use.Alias
		if alias == "" {
			alias = "env"
		}
		out[alias] = true
	}
	return out
}

func (g *generator) emitStdEnvCall(call *ast.CallExpr) (value, bool, error) {
	if !g.isStdEnvArgsCall(call) {
		return value{}, false, nil
	}
	if len(call.Args) != 0 {
		return value{}, true, unsupportedf("call", "env.args takes no arguments, got %d", len(call.Args))
	}
	g.declareRuntimeSymbol(ostyRtEnvArgsSymbol, "ptr", nil)
	emitter := g.toOstyEmitter()
	g.emitGCSafepointKind(emitter, safepointKindCall)
	out := llvmCall(emitter, "ptr", ostyRtEnvArgsSymbol, nil)
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.gcManaged = true
	v.listElemTyp = "ptr"
	v.listElemString = true
	v.sourceType = stdEnvArgsSourceTypeSingleton
	v.rootPaths = g.rootPathsForType(v.typ)
	return v, true, nil
}

func (g *generator) stdEnvCallStaticResult(call *ast.CallExpr) (value, bool) {
	if !g.isStdEnvArgsCall(call) {
		return value{}, false
	}
	return value{
		typ:            "ptr",
		gcManaged:      true,
		listElemTyp:    "ptr",
		listElemString: true,
		sourceType:     stdEnvArgsSourceTypeSingleton,
	}, true
}

// staticStdEnvCallSourceType recovers the source-level `List<String>`
// so `args.get(i) ?? "x"` reaches the Option<String> the `??` emitter
// demands. Without it the coalesce path bails with
// "?? left source type unknown".
func (g *generator) staticStdEnvCallSourceType(call *ast.CallExpr) (ast.Type, bool) {
	if !g.isStdEnvArgsCall(call) {
		return nil, false
	}
	return stdEnvArgsSourceTypeSingleton, true
}

func (g *generator) isStdEnvArgsCall(call *ast.CallExpr) bool {
	if call == nil || len(g.stdEnvAliases) == 0 {
		return false
	}
	field, ok := call.Fn.(*ast.FieldExpr)
	if !ok {
		return false
	}
	alias, ok := field.X.(*ast.Ident)
	if !ok || !g.stdEnvAliases[alias.Name] {
		return false
	}
	return field.Name == "args"
}
