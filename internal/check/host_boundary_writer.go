package check

import (
	"bytes"
	"fmt"
	"strings"
	"sync"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/types"
)

func writeSelfhostPackageImports(b *bytes.Buffer, pkg *resolve.Package, ws *resolve.Workspace, stdlib resolve.StdlibProvider) {
	if pkg == nil {
		return
	}
	var uses []*ast.UseDecl
	for _, pf := range pkg.Files {
		if pf == nil || pf.File == nil {
			continue
		}
		uses = append(uses, pf.File.Uses...)
	}
	writeSelfhostImports(b, ws, stdlib, uses)
}

func writeSelfhostImports(b *bytes.Buffer, ws *resolve.Workspace, stdlib resolve.StdlibProvider, uses []*ast.UseDecl) {
	seen := map[string]bool{}
	for _, use := range uses {
		dotPath := strings.Join(use.Path, ".")
		if use.IsScoped {
			dotPath = strings.Join(use.ScopedBase, ".")
		}
		target := (*resolve.Package)(nil)
		if ws != nil {
			target = ws.Packages[dotPath]
			if target == nil && ws.Stdlib != nil {
				target = ws.Stdlib.LookupPackage(dotPath)
			}
		}
		if target == nil && stdlib != nil {
			target = stdlib.LookupPackage(dotPath)
		}
		if target == nil {
			continue
		}
		alias := use.Alias
		if use.IsScoped {
			alias = lastPathSegment(dotPath)
		} else if alias == "" && len(use.Path) > 0 {
			alias = use.Path[len(use.Path)-1]
		}
		if alias == "" || seen[alias] {
			continue
		}
		seen[alias] = true
		writeSelfhostPackageImport(b, alias, target)
	}
}

func lastPathSegment(path string) string {
	if path == "" {
		return ""
	}
	if i := strings.LastIndex(path, "."); i >= 0 {
		return path[i+1:]
	}
	return path
}

func fileUses(file *ast.File) []*ast.UseDecl {
	if file == nil {
		return nil
	}
	return file.Uses
}

func writeSelfhostPackageImport(b *bytes.Buffer, alias string, pkg *resolve.Package) {
	var body bytes.Buffer
	for _, pf := range pkg.Files {
		if pf == nil || pf.File == nil {
			continue
		}
		for _, decl := range pf.File.Decls {
			fn, ok := decl.(*ast.FnDecl)
			if !ok || !fn.Pub || fn.Recv != nil {
				continue
			}
			fmt.Fprintf(&body, "    fn %s", fn.Name)
			if generics := selfhostGenericParams(fn.Generics); generics != "" {
				body.WriteString(generics)
			}
			body.WriteByte('(')
			for i, param := range fn.Params {
				if i > 0 {
					body.WriteString(", ")
				}
				name := param.Name
				if name == "" {
					name = fmt.Sprintf("arg%d", i)
				}
				fmt.Fprintf(&body, "%s: %s", name, selfhostTypeSource(param.Type))
				// Preserve default-availability for the arity check. The
				// concrete literal value doesn't matter — collectFnDecl
				// only looks at paramNode.left >= 0 to set the "?"
				// paramName prefix. `= ()` is a valid literal default
				// (spec §R18) across all param types.
				if param.Default != nil {
					body.WriteString(" = ()")
				}
			}
			body.WriteByte(')')
			if ret := selfhostTypeSource(fn.ReturnType); ret != "()" {
				fmt.Fprintf(&body, " -> %s", ret)
			}
			body.WriteByte('\n')
		}
	}
	if body.Len() == 0 {
		return
	}
	fmt.Fprintf(b, "use go %q as %s {\n", alias, alias)
	b.Write(body.Bytes())
	b.WriteString("}\n")
}

// selfhostGenericParams formats a fn's generic parameter list for the
// boundary writer. Empty list returns "" (no `<>` emitted). Per
// LANG_SPEC §19.5, runtime intrinsic stubs declare generics like
// `fn read<T: Pod>(p: RawPtr) -> T`; without this the boundary
// dropped `<T: Pod>` and the native checker saw `T` as undeclared,
// producing `ErrType` at every turbofish call site.
func selfhostGenericParams(gps []*ast.GenericParam) string {
	if len(gps) == 0 {
		return ""
	}
	parts := make([]string, 0, len(gps))
	for _, gp := range gps {
		if gp == nil {
			continue
		}
		entry := gp.Name
		if len(gp.Constraints) > 0 {
			bounds := make([]string, 0, len(gp.Constraints))
			for _, c := range gp.Constraints {
				bounds = append(bounds, selfhostTypeSource(c))
			}
			entry += ": " + strings.Join(bounds, " + ")
		}
		parts = append(parts, entry)
	}
	if len(parts) == 0 {
		return ""
	}
	return "<" + strings.Join(parts, ", ") + ">"
}

func selfhostTypeSource(t ast.Type) string {
	switch x := t.(type) {
	case nil:
		return "()"
	case *ast.NamedType:
		name := strings.Join(x.Path, ".")
		if name == "" {
			name = "Invalid"
		}
		if len(x.Args) == 0 {
			return name
		}
		args := make([]string, 0, len(x.Args))
		for _, arg := range x.Args {
			args = append(args, selfhostTypeSource(arg))
		}
		return name + "<" + strings.Join(args, ", ") + ">"
	case *ast.OptionalType:
		return selfhostTypeSource(x.Inner) + "?"
	case *ast.TupleType:
		elems := make([]string, 0, len(x.Elems))
		for _, elem := range x.Elems {
			elems = append(elems, selfhostTypeSource(elem))
		}
		return "(" + strings.Join(elems, ", ") + ")"
	case *ast.FnType:
		params := make([]string, 0, len(x.Params))
		for _, param := range x.Params {
			params = append(params, selfhostTypeSource(param))
		}
		out := "fn(" + strings.Join(params, ", ") + ")"
		if x.ReturnType != nil {
			out += " -> " + selfhostTypeSource(x.ReturnType)
		}
		return out
	default:
		return "Invalid"
	}
}

var scalarByName = map[string]types.Type{
	"Int":     types.Int,
	"Int8":    types.Int8,
	"Int16":   types.Int16,
	"Int32":   types.Int32,
	"Int64":   types.Int64,
	"UInt8":   types.UInt8,
	"UInt16":  types.UInt16,
	"UInt32":  types.UInt32,
	"UInt64":  types.UInt64,
	"Byte":    types.Byte,
	"Float":   types.Float,
	"Float32": types.Float32,
	"Float64": types.Float64,
	"Bool":    types.Bool,
	"Char":    types.Char,
	"String":  types.String,
	"Bytes":   types.Bytes,
	"Never":   types.Never,
}

// syntheticBuiltinSym returns a process-wide Symbol that stands in for a
// builtin when the native checker response names a type outside the current
// resolver scope.
func syntheticBuiltinSym(name string) *resolve.Symbol {
	if sym, ok := syntheticBuiltinsRead(name); ok {
		return sym
	}
	sym := &resolve.Symbol{Name: name, Kind: resolve.SymBuiltin}
	syntheticBuiltinsStore(name, sym)
	return sym
}

var (
	syntheticBuiltinsMu sync.RWMutex
	syntheticBuiltins   = map[string]*resolve.Symbol{}
)

func syntheticBuiltinsRead(name string) (*resolve.Symbol, bool) {
	syntheticBuiltinsMu.RLock()
	defer syntheticBuiltinsMu.RUnlock()
	sym, ok := syntheticBuiltins[name]
	return sym, ok
}

func syntheticBuiltinsStore(name string, sym *resolve.Symbol) {
	syntheticBuiltinsMu.Lock()
	defer syntheticBuiltinsMu.Unlock()
	if _, ok := syntheticBuiltins[name]; ok {
		return
	}
	syntheticBuiltins[name] = sym
}
