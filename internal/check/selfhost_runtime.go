//go:build !selfhostgen

package check

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/selfhost"
	"github.com/osty/osty/internal/token"
)

func applySelfhostFileResult(result *Result, file *ast.File, src []byte, stdlib resolve.StdlibProvider) {
	if result == nil || len(src) == 0 {
		return
	}
	checkedSrc := selfhostFileSource(file, src, stdlib)
	checked := selfhost.CheckSourceStructured(checkedSrc)
	result.Diags = selfhostCheckerDiags(checkedSrc, checked)
}

func applySelfhostPackageResult(result *Result, pkg *resolve.Package, ws *resolve.Workspace, stdlib resolve.StdlibProvider) {
	if result == nil || pkg == nil {
		return
	}
	src := selfhostPackageSource(pkg, ws, stdlib)
	if len(src) == 0 {
		return
	}
	checked := selfhost.CheckSourceStructured(src)
	result.Diags = selfhostCheckerDiags(src, checked)
}

func applySelfhostWorkspaceResults(ws *resolve.Workspace, results map[string]*Result, stdlib resolve.StdlibProvider) {
	if ws == nil {
		return
	}
	for path, result := range results {
		pkg := ws.Packages[path]
		if isProviderStdlibPackage(ws, path, pkg) {
			continue
		}
		applySelfhostPackageResult(result, pkg, ws, stdlib)
	}
}

func selfhostCheckerDiags(src []byte, checked selfhost.CheckResult) []*diag.Diagnostic {
	if checked.Summary.Errors == 0 {
		return nil
	}
	label := "self-hosted checker reported type errors"
	if checked.Summary.Errors == 1 {
		label = "self-hosted checker reported a type error"
	}
	return []*diag.Diagnostic{
		diag.New(diag.Error, fmt.Sprintf("%s: %d error(s)", label, checked.Summary.Errors)).
			Code(diag.CodeTypeMismatch).
			Primary(fileStartSpan(src), "selfhost checker summary").
			Note(fmt.Sprintf(
				"selfhost accepted %d of %d assignment/return/call checks",
				checked.Summary.Accepted,
				checked.Summary.Assignments,
			)).
			Build(),
	}
}

func fileStartSpan(src []byte) diag.Span {
	start := token.Pos{Line: 1, Column: 1, Offset: 0}
	end := start
	if len(src) > 0 {
		end = token.Pos{Line: 1, Column: 2, Offset: 1}
	}
	return diag.Span{Start: start, End: end}
}

func selfhostFileSource(file *ast.File, src []byte, stdlib resolve.StdlibProvider) []byte {
	var b bytes.Buffer
	writeSelfhostImports(&b, nil, stdlib, fileUses(file))
	if b.Len() > 0 {
		b.WriteByte('\n')
	}
	b.Write(src)
	if !bytes.HasSuffix(src, []byte("\n")) {
		b.WriteByte('\n')
	}
	return b.Bytes()
}

func selfhostPackageSource(pkg *resolve.Package, ws *resolve.Workspace, stdlib resolve.StdlibProvider) []byte {
	var b bytes.Buffer
	writeSelfhostPackageImports(&b, pkg, ws, stdlib)
	for _, pf := range pkg.Files {
		if len(pf.Source) == 0 {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.Write(pf.Source)
		if !bytes.HasSuffix(pf.Source, []byte("\n")) {
			b.WriteByte('\n')
		}
	}
	return b.Bytes()
}

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
		if alias == "" && len(use.Path) > 0 {
			alias = use.Path[len(use.Path)-1]
		}
		if alias == "" || seen[alias] {
			continue
		}
		seen[alias] = true
		writeSelfhostPackageImport(b, alias, target)
	}
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
			fmt.Fprintf(&body, "    fn %s(", fn.Name)
			for i, param := range fn.Params {
				if i > 0 {
					body.WriteString(", ")
				}
				name := param.Name
				if name == "" {
					name = fmt.Sprintf("arg%d", i)
				}
				fmt.Fprintf(&body, "%s: %s", name, selfhostTypeSource(param.Type))
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
