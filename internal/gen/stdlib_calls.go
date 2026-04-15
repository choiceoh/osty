package gen

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/resolve"
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

// resolvesToStdAlias reports whether `id` names a stdlib alias in
// scope — i.e. the resolver bound it to a SymPackage and the alias
// appears in g.stdAliases. A local `let strings = ...` that shadows
// the import returns false so the rewriter leaves the shadowing
// binding alone. When no resolver result is available (fuzz corpus,
// half-resolved AST) we fall back to the name-only match; this
// preserves the lenient behavior of the surrounding intrinsic
// rewriters (println, Ok, Err) so partial failures don't cascade.
func (g *gen) resolvesToStdAlias(id *ast.Ident) (string, bool) {
	mod, ok := g.stdAliases[id.Name]
	if !ok {
		return "", false
	}
	if sym := g.symbolFor(id); sym != nil && sym.Kind != resolve.SymPackage {
		return "", false
	}
	return mod, true
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
	mod, ok := g.resolvesToStdAlias(id)
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
// stdlib aliases that expose constants — currently only std.math.
// Returns false on miss so the generic field-access path takes over.
func (g *gen) emitStdlibField(f *ast.FieldExpr) bool {
	id, ok := f.X.(*ast.Ident)
	if !ok {
		return false
	}
	mod, ok := g.resolvesToStdAlias(id)
	if !ok {
		return false
	}
	if mod != "math" {
		return false
	}
	expr, ok := mathConstants[f.Name]
	if !ok {
		return false
	}
	g.use("math")
	g.body.write(expr)
	return true
}

// mathConstants maps each std.math constant to its Go expression.
// `TAU` has no Go stdlib constant, so we lower it to `2 * math.Pi`.
var mathConstants = map[string]string{
	"PI":       "math.Pi",
	"E":        "math.E",
	"TAU":      "(2 * math.Pi)",
	"INFINITY": "math.Inf(1)",
	"NAN":      "math.NaN()",
}

// mathFnNames maps every std.math free function with a single-Go-call
// lowering to the Go identifier. `log` is the one exception — its
// two-argument form needs a divide, handled inline in emitMathCall.
var mathFnNames = map[string]string{
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

// emitMathCall lowers `math.<fn>(args)` to the equivalent `math.<Fn>`
// call. Every function in std.math maps to a single Go math-package
// function except `log`, whose two-argument form divides natural logs
// to synthesize an arbitrary base.
func (g *gen) emitMathCall(c *ast.CallExpr, name string) bool {
	if goName, ok := mathFnNames[name]; ok {
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
			// log(x, base) = ln(x) / ln(base). The outer parens keep the
			// rewrite safe when this call is an operand of a tighter
			// operator (e.g. `-math.log(x, 2)`).
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
// `set` swallows os.Setenv's error — Osty's signature is Unit — via a
// zero-arg IIFE so the rewrite composes as both a statement and an
// expression.
func (g *gen) emitEnvCall(c *ast.CallExpr, name string) bool {
	switch name {
	case "args":
		if len(c.Args) == 0 {
			g.use("os")
			g.body.write("os.Args")
			return true
		}
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

// stringsFnNames maps each std.strings free function to the Go
// strings-package identifier. `split`/`join`/`contains` keep their
// arity; `startsWith`/`endsWith`/`replace` rename to the Go idiom;
// `trimStart`/`trimEnd` additionally synthesize a whitespace-matching
// second argument — see emitStringsCall.
var stringsFnNames = map[string]string{
	"split":      "Split",
	"join":       "Join",
	"contains":   "Contains",
	"startsWith": "HasPrefix",
	"endsWith":   "HasSuffix",
	"trim":       "TrimSpace",
	"trimStart":  "TrimLeftFunc",
	"trimEnd":    "TrimRightFunc",
	"toUpper":    "ToUpper",
	"toLower":    "ToLower",
	"repeat":     "Repeat",
	"replace":    "ReplaceAll",
}

// emitStringsCall lowers `strings.<fn>` to its Go strings-package
// counterpart. `trimStart`/`trimEnd` lower to the *Func form with
// `unicode.IsSpace`, giving spec-consistent Unicode-whitespace
// semantics that match `strings.TrimSpace`.
func (g *gen) emitStringsCall(c *ast.CallExpr, name string) bool {
	goName, ok := stringsFnNames[name]
	if !ok {
		return false
	}
	g.use("strings")
	g.body.writef("strings.%s(", goName)
	g.emitCallArgList(c.Args)
	if name == "trimStart" || name == "trimEnd" {
		g.use("unicode")
		g.body.write(", unicode.IsSpace")
	}
	g.body.write(")")
	return true
}

// emitFsCall lowers `fs.<fn>` calls to the corresponding os-package
// call, wrapping failures into Osty's Result[T, E] runtime where the
// signature demands it. `exists` is the one unwrapped case — a simple
// Bool per §10.1.
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
			g.emitFsReadToString(c)
			return true
		}
	case "writeString":
		if len(c.Args) == 2 {
			g.emitFsWriteString(c)
			return true
		}
	case "remove":
		if len(c.Args) == 1 {
			g.emitFsRemove(c)
			return true
		}
	}
	return false
}

// emitFsReadToString lowers `fs.readToString(path)` to an os.ReadFile
// call wrapped as Result[string, any].
func (g *gen) emitFsReadToString(c *ast.CallExpr) {
	g.use("os")
	g.needResult = true
	tArg, tErr := g.fsResultTypeArgs(c, "string")
	g.body.writef("func() Result[%s, %s] { b, err := os.ReadFile(", tArg, tErr)
	g.emitExpr(c.Args[0].Value)
	g.body.writef("); if err != nil { return Result[%s, %s]{Error: err} }; ", tArg, tErr)
	g.body.writef("return Result[%s, %s]{Value: string(b), IsOk: true} }()", tArg, tErr)
}

// emitFsWriteString lowers `fs.writeString(path, contents)` to
// os.WriteFile with 0o644 permissions, wrapped as Result[(), any].
func (g *gen) emitFsWriteString(c *ast.CallExpr) {
	g.use("os")
	g.needResult = true
	tArg, tErr := g.fsResultTypeArgs(c, "struct{}")
	g.body.writef("func() Result[%s, %s] { err := os.WriteFile(", tArg, tErr)
	g.emitExpr(c.Args[0].Value)
	g.body.write(", []byte(")
	g.emitExpr(c.Args[1].Value)
	g.body.writef("), 0o644); if err != nil { return Result[%s, %s]{Error: err} }; ", tArg, tErr)
	g.body.writef("return Result[%s, %s]{IsOk: true} }()", tArg, tErr)
}

// emitFsRemove lowers `fs.remove(path)` to os.Remove wrapped as
// Result[(), any].
func (g *gen) emitFsRemove(c *ast.CallExpr) {
	g.use("os")
	g.needResult = true
	tArg, tErr := g.fsResultTypeArgs(c, "struct{}")
	g.body.writef("func() Result[%s, %s] { err := os.Remove(", tArg, tErr)
	g.emitExpr(c.Args[0].Value)
	g.body.writef("); if err != nil { return Result[%s, %s]{Error: err} }; ", tArg, tErr)
	g.body.writef("return Result[%s, %s]{IsOk: true} }()", tArg, tErr)
}

// fsResultTypeArgs picks the Go (T, E) type arguments for an fs call's
// Result wrapper. Prefers the checker's inferred Result<T, E>; falls
// back to (defaultT, any) when the call's type is missing — as is
// common for stdlib calls the checker hasn't propagated through.
func (g *gen) fsResultTypeArgs(c *ast.CallExpr, defaultT string) (string, string) {
	if t := g.typeOf(c); t != nil {
		if n, ok := t.(*types.Named); ok && n.Sym != nil && n.Sym.Name == "Result" && len(n.Args) == 2 {
			return g.goType(n.Args[0]), g.goType(n.Args[1])
		}
	}
	return defaultT, "any"
}
