package gen

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/types"
)

// stdModuleWithRewrites reports whether a `use std.<mod>` path refers
// to a module whose call and field surface is lowered by the
// per-module rewriters in this file. Returns the bare module name
// ("math", "env", "strings", "fs") or "" when no rewriter applies —
// the caller then falls back to the generic stdlibBridge path.
func stdModuleWithRewrites(path []string) string {
	if len(path) < 2 || path[0] != "std" {
		return ""
	}
	switch path[1] {
	case "math", "env", "strings", "fs":
		return path[1]
	}
	return ""
}

// emitStdlibCall intercepts `<alias>.<fn>(args)` when <alias> was bound
// by a `use std.math/env/strings/fs` declaration and rewrites it to the
// equivalent Go stdlib call. Returns false when the call shape doesn't
// match, letting the generic emitter take over.
func (g *gen) emitStdlibCall(c *ast.CallExpr, f *ast.FieldExpr) bool {
	id, ok := f.X.(*ast.Ident)
	if !ok {
		return false
	}
	mod, ok := g.stdAliases[id.Name]
	if !ok {
		return false
	}
	switch mod {
	case "math":
		return g.emitMathCall(c, f.Name)
	case "env":
		return g.emitEnvCall(c, f.Name)
	case "strings":
		return g.emitStringsCall(c, f.Name)
	case "fs":
		return g.emitFsCall(c, f.Name)
	}
	return false
}

// emitStdlibField intercepts `<alias>.<const>` accesses (no call) for
// stdlib aliases that expose constants — currently only std.math. The
// constants lower to their Go `math.*` equivalents.
func (g *gen) emitStdlibField(f *ast.FieldExpr) bool {
	id, ok := f.X.(*ast.Ident)
	if !ok {
		return false
	}
	mod, ok := g.stdAliases[id.Name]
	if !ok {
		return false
	}
	if mod != "math" {
		return false
	}
	switch f.Name {
	case "PI":
		g.use("math")
		g.body.write("math.Pi")
		return true
	case "E":
		g.use("math")
		g.body.write("math.E")
		return true
	case "TAU":
		g.use("math")
		g.body.write("(2 * math.Pi)")
		return true
	case "INFINITY":
		g.use("math")
		g.body.write("math.Inf(1)")
		return true
	case "NAN":
		g.use("math")
		g.body.write("math.NaN()")
		return true
	}
	return false
}

// emitMathCall lowers `math.<fn>(args)` to the equivalent `math.<Fn>`
// call. Every function in std.math maps to a single Go math-package
// function except `log`, whose two-argument form divides natural logs
// to synthesize an arbitrary base.
func (g *gen) emitMathCall(c *ast.CallExpr, name string) bool {
	simple := map[string]string{
		"sin": "Sin", "cos": "Cos", "tan": "Tan",
		"asin": "Asin", "acos": "Acos", "atan": "Atan",
		"atan2": "Atan2",
		"sinh":  "Sinh", "cosh": "Cosh", "tanh": "Tanh",
		"exp":  "Exp",
		"log2": "Log2", "log10": "Log10",
		"sqrt": "Sqrt", "cbrt": "Cbrt",
		"pow":   "Pow",
		"floor": "Floor", "ceil": "Ceil", "round": "Round", "trunc": "Trunc",
		"abs": "Abs",
		"min": "Min", "max": "Max",
		"hypot": "Hypot",
	}
	if goName, ok := simple[name]; ok {
		g.use("math")
		g.body.writef("math.%s(", goName)
		g.emitCallArgList(c.Args)
		g.body.write(")")
		return true
	}
	if name == "log" {
		g.use("math")
		switch len(c.Args) {
		case 1:
			g.body.write("math.Log(")
			g.emitExpr(c.Args[0].Value)
			g.body.write(")")
			return true
		case 2:
			// log(x, base) = ln(x) / ln(base)
			g.body.write("(math.Log(")
			g.emitExpr(c.Args[0].Value)
			g.body.write(") / math.Log(")
			g.emitExpr(c.Args[1].Value)
			g.body.write("))")
			return true
		}
	}
	return false
}

// emitEnvCall lowers `env.args/get/set` to their os-package analogues.
func (g *gen) emitEnvCall(c *ast.CallExpr, name string) bool {
	switch name {
	case "args":
		g.use("os")
		g.body.write("os.Args")
		return true
	case "get":
		if len(c.Args) == 1 {
			g.use("os")
			g.body.write("os.Getenv(")
			g.emitExpr(c.Args[0].Value)
			g.body.write(")")
			return true
		}
	case "set":
		if len(c.Args) == 2 {
			g.use("os")
			// os.Setenv returns an error. std.env.set returns Unit, so we
			// discard the error via a blank assignment inside an IIFE to
			// preserve expression position. At statement position the
			// IIFE collapses into a no-op call after gofmt.
			g.body.write("func() { _ = os.Setenv(")
			g.emitExpr(c.Args[0].Value)
			g.body.write(", ")
			g.emitExpr(c.Args[1].Value)
			g.body.write(") }()")
			return true
		}
	}
	return false
}

// emitStringsCall lowers `strings.<fn>` to `strings.<Fn>` with a handful
// of name remappings for the non-obvious cases.
func (g *gen) emitStringsCall(c *ast.CallExpr, name string) bool {
	g.use("strings")
	switch name {
	case "split":
		g.body.write("strings.Split(")
	case "join":
		g.body.write("strings.Join(")
	case "contains":
		g.body.write("strings.Contains(")
	case "startsWith":
		g.body.write("strings.HasPrefix(")
	case "endsWith":
		g.body.write("strings.HasSuffix(")
	case "trim":
		g.body.write("strings.TrimSpace(")
	case "trimStart":
		g.body.write("strings.TrimLeft(")
	case "trimEnd":
		g.body.write("strings.TrimRight(")
	case "toUpper":
		g.body.write("strings.ToUpper(")
	case "toLower":
		g.body.write("strings.ToLower(")
	case "repeat":
		g.body.write("strings.Repeat(")
	case "replace":
		g.body.write("strings.ReplaceAll(")
	default:
		return false
	}
	g.emitCallArgList(c.Args)
	// trimStart / trimEnd take a cutset as second arg in Go's API; the
	// Osty surface takes just the string, so we append a standard
	// whitespace cutset to keep the call total-function.
	if name == "trimStart" || name == "trimEnd" {
		g.body.write(", \" \\t\\n\\r\\v\\f\"")
	}
	g.body.write(")")
	return true
}

// emitFsCall lowers `fs.<fn>` calls to the corresponding os package
// call, wrapping failures into Osty's Result[T, E] runtime where the
// signature demands it.
func (g *gen) emitFsCall(c *ast.CallExpr, name string) bool {
	switch name {
	case "exists":
		if len(c.Args) == 1 {
			g.use("os")
			g.body.write("func() bool { _, err := os.Stat(")
			g.emitExpr(c.Args[0].Value)
			g.body.write("); return err == nil }()")
			return true
		}
	case "readToString":
		if len(c.Args) == 1 {
			g.use("os")
			g.needResult = true
			tArg, tErr := g.fsResultTypeArgs(c, "string")
			g.body.writef("func() Result[%s, %s] { b, err := os.ReadFile(", tArg, tErr)
			g.emitExpr(c.Args[0].Value)
			g.body.writef("); if err != nil { return Result[%s, %s]{Error: err} }; ", tArg, tErr)
			g.body.writef("return Result[%s, %s]{Value: string(b), IsOk: true} }()", tArg, tErr)
			return true
		}
	case "writeString":
		if len(c.Args) == 2 {
			g.use("os")
			g.needResult = true
			tArg, tErr := g.fsResultTypeArgs(c, "struct{}")
			g.body.writef("func() Result[%s, %s] { err := os.WriteFile(", tArg, tErr)
			g.emitExpr(c.Args[0].Value)
			g.body.write(", []byte(")
			g.emitExpr(c.Args[1].Value)
			g.body.writef("), 0o644); if err != nil { return Result[%s, %s]{Error: err} }; ", tArg, tErr)
			g.body.writef("return Result[%s, %s]{IsOk: true} }()", tArg, tErr)
			return true
		}
	case "remove":
		if len(c.Args) == 1 {
			g.use("os")
			g.needResult = true
			tArg, tErr := g.fsResultTypeArgs(c, "struct{}")
			g.body.writef("func() Result[%s, %s] { err := os.Remove(", tArg, tErr)
			g.emitExpr(c.Args[0].Value)
			g.body.writef("); if err != nil { return Result[%s, %s]{Error: err} }; ", tArg, tErr)
			g.body.writef("return Result[%s, %s]{IsOk: true} }()", tArg, tErr)
			return true
		}
	}
	return false
}

// fsResultTypeArgs picks the Go (T, E) type arguments for an fs call's
// Result wrapper. Prefers the checker's inferred Result<T, E>; falls
// back to (defaultT, any) when the call's type is missing.
func (g *gen) fsResultTypeArgs(c *ast.CallExpr, defaultT string) (string, string) {
	if t := g.typeOf(c); t != nil {
		if n, ok := t.(*types.Named); ok && n.Sym != nil && n.Sym.Name == "Result" && len(n.Args) == 2 {
			return g.goType(n.Args[0]), g.goType(n.Args[1])
		}
	}
	return defaultT, "any"
}
