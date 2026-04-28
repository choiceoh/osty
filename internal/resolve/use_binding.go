package resolve

import (
	"fmt"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/selfhost"
	"github.com/osty/osty/internal/token"
)

func resolveUseBinding(workspace *Workspace, u *ast.UseDecl, name, file string) (*Symbol, *diag.Diagnostic) {
	sym := placeholderUseSymbol(u, name, SymPackage, u != nil && u.IsPub)
	if u == nil || u.IsFFI() || workspace == nil {
		return sym, nil
	}
	if u.IsScoped {
		basePath := scopedUseBaseKey(u)
		member := scopedUseMemberName(u)
		if basePath == "" || member == "" {
			return sym, nil
		}
		memberSym, memberDiag, _ := resolveUseMemberBinding(workspace, u, name, basePath, member, file)
		if memberSym != nil {
			return memberSym, memberDiag
		}
		_, baseDiag := workspace.ResolveUseTarget(basePath, u.PosV)
		return sym, baseDiag
	}

	targetPath := UseKey(u)
	target, d := workspace.ResolveUseTarget(targetPath, u.PosV)
	if d != nil {
		return sym, d
	}
	if target != nil && !target.isCycleMarker {
		sym.Package = target
	}
	return sym, nil
}

func resolveUseMemberBinding(workspace *Workspace, u *ast.UseDecl, name, basePath, member, file string) (*Symbol, *diag.Diagnostic, bool) {
	basePkg, baseDiag := workspace.ResolveUseTarget(basePath, u.PosV)
	if baseDiag != nil || basePkg == nil || basePkg.isCycleMarker {
		return nil, nil, false
	}
	if basePkg.isStub || basePkg.PkgScope == nil {
		return placeholderUseSymbol(u, name, SymUnknown, false), nil, true
	}

	source := basePkg.PkgScope.LookupLocal(member)
	switch {
	case source == nil:
		return placeholderUseSymbol(u, name, SymUnknown, false),
			packageMemberDiagnostic(basePath, member, u.PosV, file, false, false),
			true
	case !source.Pub && u.IsPub:
		return placeholderUseSymbol(u, name, source.Kind, false),
			privateReexportDiagnostic(u.PosV, basePath, member, file),
			true
	case !source.Pub:
		return placeholderUseSymbol(u, name, source.Kind, false),
			packageMemberDiagnostic(basePath, member, u.PosV, file, true, false),
			true
	default:
		return importedUseSymbol(source, u, name), nil, true
	}
}

func placeholderUseSymbol(u *ast.UseDecl, name string, kind SymbolKind, public bool) *Symbol {
	var pos token.Pos
	var decl ast.Node
	if u != nil {
		pos = u.PosV
		decl = u
	}
	return &Symbol{
		Name: name,
		Kind: kind,
		Pos:  pos,
		Decl: decl,
		Pub:  public,
	}
}

func importedUseSymbol(source *Symbol, u *ast.UseDecl, name string) *Symbol {
	if source == nil {
		return placeholderUseSymbol(u, name, SymUnknown, false)
	}
	return &Symbol{
		Name:    name,
		Kind:    source.Kind,
		Pos:     source.Pos,
		Decl:    source.Decl,
		Pub:     source.Pub,
		Package: source.Package,
	}
}

func packageMemberDiagnostic(pkgName, member string, pos token.Pos, file string, found, public bool) *diag.Diagnostic {
	res := selfhost.LookupPackageMember(pkgName, member, false, found, public)
	d := diag.New(diag.Error, res.Message).
		Code(res.Code).
		PrimaryPos(pos, res.Primary)
	if res.Note != "" {
		d.Note(res.Note)
	}
	if res.Hint != "" {
		d.Hint(res.Hint)
	}
	out := d.Build()
	if out.File == "" {
		out.File = file
	}
	return out
}

func privateReexportDiagnostic(pos token.Pos, pkgName, member, file string) *diag.Diagnostic {
	target := pkgName + "." + member
	out := diag.New(diag.Error, fmt.Sprintf("`pub use` cannot re-export private symbol `%s`", target)).
		Code(diag.CodeReexportPrivate).
		PrimaryPos(pos, "private re-export").
		Note(fmt.Sprintf("`%s` is declared without `pub` in package `%s`", member, pkgName)).
		Hint(fmt.Sprintf("make `%s` public or remove `pub` from this use", member)).
		Build()
	if out.File == "" {
		out.File = file
	}
	return out
}

func splitUseMemberPath(path string) (base string, member string, ok bool) {
	if path == "" || strings.ContainsAny(path, "/") {
		return "", "", false
	}
	i := strings.LastIndex(path, ".")
	if i <= 0 || i == len(path)-1 {
		return "", "", false
	}
	return path[:i], path[i+1:], true
}

func scopedUseBaseKey(u *ast.UseDecl) string {
	if u == nil {
		return ""
	}
	if len(u.ScopedBase) > 0 {
		return strings.Join(u.ScopedBase, ".")
	}
	base, _, ok := splitUseMemberPath(UseKey(u))
	if !ok {
		return ""
	}
	return base
}

func scopedUseMemberName(u *ast.UseDecl) string {
	if u == nil {
		return ""
	}
	if u.ScopedMember != "" {
		return u.ScopedMember
	}
	_, member, ok := splitUseMemberPath(UseKey(u))
	if !ok {
		return ""
	}
	return member
}
