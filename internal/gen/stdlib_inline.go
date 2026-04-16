//go:build selfhostgen

package gen

import (
	"fmt"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

var stdlibOstyInlineModules = map[string]bool{
	"strings": true,
	"fmt":     true,
}

func stdlibOstyModulePath(path []string) (string, bool) {
	if len(path) != 2 || path[0] != "std" || !stdlibOstyInlineModules[path[1]] {
		return "", false
	}
	return path[1], true
}

func (g *gen) requestStdlibOsty(module string) {
	if stdlibOstyInlineModules[module] {
		g.needStdlibOsty[module] = true
	}
}

func (g *gen) emitNeededStdlibOstyModules() {
	for {
		progress := false
		for _, module := range []string{"strings", "fmt"} {
			if !g.needStdlibOsty[module] || g.emittedStdlibOsty[module] {
				continue
			}
			g.emittedStdlibOsty[module] = true
			g.emitStdlibOstyModule(module)
			progress = true
		}
		if !progress {
			return
		}
	}
}

func (g *gen) emitStdlibOstyModule(module string) {
	reg := stdlib.LoadCached()
	mod := reg.Modules[module]
	if mod == nil || mod.File == nil {
		g.errs = append(g.errs, fmt.Errorf("gen: std.%s source module not found", module))
		return
	}

	res := resolve.FileWithStdlib(mod.File, resolve.NewPrelude(), reg)
	chk := check.File(mod.File, res, check.Opts{
		UseSelfhost:   true,
		Source:        mod.Source,
		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
	})
	for _, d := range append(append([]*diag.Diagnostic{}, res.Diags...), chk.Diags...) {
		if d.Severity == diag.Error {
			g.errs = append(g.errs, fmt.Errorf("gen: std.%s inline check: %s", module, d.Error()))
			return
		}
	}

	sub := newGen(g.pkgName, mod.File, res, chk)
	sub.stdlibInlineModule = module
	sub.stdlibInlineRenames = collectStdlibInlineRenames(module, mod.File)
	sub.emitGenericFnDecls = true
	for _, u := range sub.file.Uses {
		sub.emitUseDecl(u)
	}
	for _, d := range sub.file.Decls {
		sub.emitDecl(d)
	}
	sub.drainInstances()

	g.body.nl()
	g.body.writef("// std.%s inlined from bundled Osty source.\n", module)
	g.body.write(string(sub.body.bytes()))
	g.mergeInlineGen(sub)
}

func collectStdlibInlineRenames(module string, file *ast.File) map[string]string {
	out := map[string]string{}
	if file == nil {
		return out
	}
	for _, d := range file.Decls {
		switch d := d.(type) {
		case *ast.FnDecl:
			if d.Recv != nil {
				continue
			}
			out[d.Name] = stdlibOstyFuncName(module, d.Name)
		case *ast.LetDecl:
			out[d.Name] = stdlibOstyFuncName(module, d.Name)
		}
	}
	return out
}

func stdlibOstyFuncName(module, name string) string {
	return "_ostyStd" + titleIdent(module) + "_" + mangleIdent(name)
}

func titleIdent(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func (g *gen) renamedFnName(name string) string {
	if g.stdlibInlineRenames == nil {
		return name
	}
	if renamed, ok := g.stdlibInlineRenames[name]; ok {
		return renamed
	}
	return name
}

func (g *gen) renamedIdent(id *ast.Ident) string {
	if id == nil || g.stdlibInlineRenames == nil {
		return ""
	}
	renamed, ok := g.stdlibInlineRenames[id.Name]
	if !ok {
		return ""
	}
	if sym := g.symbolFor(id); sym != nil {
		switch sym.Kind {
		case resolve.SymFn:
			fn, ok := sym.Decl.(*ast.FnDecl)
			if !ok || fn.Recv != nil {
				return ""
			}
		case resolve.SymLet:
			if _, ok := sym.Decl.(*ast.LetDecl); !ok {
				return ""
			}
		default:
			return ""
		}
	}
	return renamed
}

func (g *gen) emitStdlibOstyCall(c *ast.CallExpr) bool {
	id, parts, ok := stdlibFieldChain(c.Fn)
	if !ok || id == nil || len(parts) != 1 {
		return false
	}
	for module := range stdlibOstyInlineModules {
		if !g.isStdlibPackageAlias(id, module) {
			continue
		}
		fn := stdlibOstyFnDecl(module, parts[0])
		if fn == nil {
			return false
		}
		args := newWriter()
		prev := g.body
		g.body = args
		ok := g.emitArgsForParams(fn.Params, c.Args)
		g.body = prev
		if !ok {
			return false
		}
		g.requestStdlibOsty(module)
		g.body.write(stdlibOstyFuncName(module, parts[0]))
		g.body.write("(")
		g.body.write(string(args.bytes()))
		g.body.write(")")
		return true
	}
	return false
}

func (g *gen) emitStdlibOstyField(f *ast.FieldExpr) bool {
	id, parts, ok := stdlibFieldChain(f)
	if !ok || id == nil || len(parts) != 1 {
		return false
	}
	for module := range stdlibOstyInlineModules {
		if !g.isStdlibPackageAlias(id, module) {
			continue
		}
		if stdlibOstyFnDecl(module, parts[0]) == nil {
			return false
		}
		g.requestStdlibOsty(module)
		g.body.write(stdlibOstyFuncName(module, parts[0]))
		return true
	}
	return false
}

func stdlibOstyFnDecl(module, name string) *ast.FnDecl {
	reg := stdlib.LoadCached()
	mod := reg.Modules[module]
	if mod == nil || mod.File == nil {
		return nil
	}
	for _, d := range mod.File.Decls {
		if fn, ok := d.(*ast.FnDecl); ok && fn.Recv == nil && fn.Name == name {
			return fn
		}
	}
	return nil
}

func (g *gen) emitArgsForParams(params []*ast.Param, args []*ast.Arg) bool {
	slots := make([]ast.Expr, len(params))
	paramIndex := map[string]int{}
	for i, p := range params {
		paramIndex[p.Name] = i
	}
	pos := 0
	for _, a := range args {
		if a.Name == "" {
			if pos >= len(slots) {
				return false
			}
			slots[pos] = a.Value
			pos++
			continue
		}
		idx, ok := paramIndex[a.Name]
		if !ok || idx < 0 || idx >= len(slots) || slots[idx] != nil {
			return false
		}
		slots[idx] = a.Value
	}
	for i, p := range params {
		if i > 0 {
			g.body.write(", ")
		}
		arg := slots[i]
		if arg == nil {
			arg = p.Default
		}
		if arg == nil {
			return false
		}
		if p.Type != nil {
			g.emitExprAsTypeExpr(arg, p.Type)
		} else {
			g.emitExpr(arg)
		}
	}
	return true
}

func (g *gen) mergeInlineGen(sub *gen) {
	for path, alias := range sub.imports {
		if alias != "" {
			g.useAs(path, alias)
		} else {
			g.use(path)
		}
	}
	for module := range sub.needStdlibOsty {
		g.requestStdlibOsty(module)
	}
	g.needResult = g.needResult || sub.needResult
	g.needErrorRuntime = g.needErrorRuntime || sub.needErrorRuntime
	g.needStringRuntime = g.needStringRuntime || sub.needStringRuntime
	g.needRange = g.needRange || sub.needRange
	g.needHandle = g.needHandle || sub.needHandle
	g.needTaskGroup = g.needTaskGroup || sub.needTaskGroup
	g.needSelect = g.needSelect || sub.needSelect
	g.needFS = g.needFS || sub.needFS
	g.needRandomRuntime = g.needRandomRuntime || sub.needRandomRuntime
	g.needURLRuntime = g.needURLRuntime || sub.needURLRuntime
	g.needEncoding = g.needEncoding || sub.needEncoding
	g.needEnv = g.needEnv || sub.needEnv
	g.needCsvRuntime = g.needCsvRuntime || sub.needCsvRuntime
	g.needJSON = g.needJSON || sub.needJSON
	g.needCompress = g.needCompress || sub.needCompress
	g.needCrypto = g.needCrypto || sub.needCrypto
	g.needUUID = g.needUUID || sub.needUUID
	g.needBytesRuntime = g.needBytesRuntime || sub.needBytesRuntime
	g.needRegex = g.needRegex || sub.needRegex
	g.needRefRuntime = g.needRefRuntime || sub.needRefRuntime
	g.needEqualRuntime = g.needEqualRuntime || sub.needEqualRuntime
	if g.fsOSAlias == "" {
		g.fsOSAlias = sub.fsOSAlias
	}
	if g.fsUTF8Alias == "" {
		g.fsUTF8Alias = sub.fsUTF8Alias
	}
	g.errs = append(g.errs, sub.errs...)
}
