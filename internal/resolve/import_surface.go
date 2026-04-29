package resolve

import (
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/selfhost"
	"github.com/osty/osty/internal/selfhost/api"
)

type importUseRef struct {
	path       string
	alias      string
	isGo       bool
	isScoped   bool
	scopedBase string
}

// PackageImportSurfaces returns the resolver-owned import contract for pkg.
// The selfhost resolver and checker both consume this surface so package
// export shape is decided in one place instead of being rebuilt by downstream
// passes from resolver internals.
func PackageImportSurfaces(pkg *Package, ws *Workspace, stdlib StdlibProvider) []api.PackageCheckImport {
	if pkg == nil {
		return nil
	}
	return packageImportSurfacesFromRefs(packageImportUseRefs(pkg), ws, stdlib)
}

// PackageImportSurfacesForUses is the single-file sibling of
// PackageImportSurfaces. It exists for file-mode checker calls that already
// hold a parsed public AST rather than a Package.
func PackageImportSurfacesForUses(uses []*ast.UseDecl, ws *Workspace, stdlib StdlibProvider) []api.PackageCheckImport {
	if len(uses) == 0 {
		return nil
	}
	refs := make([]importUseRef, 0, len(uses))
	for _, use := range uses {
		if ref, ok := importUseRefFromAST(use); ok {
			refs = append(refs, ref)
		}
	}
	return packageImportSurfacesFromRefs(refs, ws, stdlib)
}

// PackageExportSurface projects one resolved package into the selfhost
// package-import surface used by both resolve and check.
func PackageExportSurface(importPath, alias string, pkg *Package) api.PackageCheckImport {
	return selfhost.PackageImportSurface(importPath, alias, packageFrontendRuns(pkg))
}

func packageImportUseRefs(pkg *Package) []importUseRef {
	var refs []importUseRef
	for _, pf := range pkg.Files {
		if pf == nil {
			continue
		}
		if pf.Run != nil {
			for _, use := range selfhost.PackageUsesFromRun(pf.Run) {
				if ref, ok := importUseRefFromSelfhost(use); ok {
					refs = append(refs, ref)
				}
			}
			continue
		}
		if pf.File != nil {
			for _, use := range pf.File.Uses {
				if ref, ok := importUseRefFromAST(use); ok {
					refs = append(refs, ref)
				}
			}
			continue
		}
		if len(pf.Source) > 0 {
			run := selfhost.Run(pf.Source)
			for _, use := range selfhost.PackageUsesFromRun(run) {
				if ref, ok := importUseRefFromSelfhost(use); ok {
					refs = append(refs, ref)
				}
			}
		}
	}
	return refs
}

func importUseRefFromSelfhost(use selfhost.PackageUseRef) (importUseRef, bool) {
	if use.IsGo || use.Path == "" {
		return importUseRef{}, false
	}
	ref := importUseRef{
		path:       use.Path,
		alias:      use.Alias,
		isGo:       use.IsGo,
		isScoped:   use.IsScoped,
		scopedBase: use.ScopedBase,
	}
	if ref.alias == "" {
		ref.alias = lastSegment(ref.path)
	}
	return ref, ref.alias != ""
}

func importUseRefFromAST(use *ast.UseDecl) (importUseRef, bool) {
	if use == nil || use.IsFFI() {
		return importUseRef{}, false
	}
	ref := importUseRef{
		path:     UseKey(use),
		alias:    useAliasName(use),
		isScoped: use.IsScoped,
	}
	if ref.path == "" || ref.alias == "" {
		return importUseRef{}, false
	}
	if use.IsScoped {
		ref.scopedBase = scopedUseBaseKey(use)
	}
	return ref, true
}

func packageImportSurfacesFromRefs(refs []importUseRef, ws *Workspace, stdlib StdlibProvider) []api.PackageCheckImport {
	if len(refs) == 0 {
		return nil
	}
	seen := map[string]string{}
	var out []api.PackageCheckImport
	for _, ref := range refs {
		if ref.isGo || ref.alias == "" {
			continue
		}
		targetPath := ref.path
		importAlias := ref.alias
		if ref.isScoped {
			targetPath = ref.scopedBase
			importAlias = lastDotSeg(targetPath)
		}
		if targetPath == "" || importAlias == "" {
			continue
		}
		target := LookupPackageImportByPath(targetPath, ws, stdlib)
		if target == nil {
			continue
		}
		if prev, ok := seen[importAlias]; ok {
			if prev == targetPath {
				continue
			}
			continue
		}
		seen[importAlias] = targetPath
		out = append(out, PackageExportSurface(targetPath, importAlias, target))
	}
	return out
}

// LookupPackageImportByPath resolves an imported package for import-surface
// construction. It prefers already loaded workspace packages, then stdlib
// providers, then native workspace loading for lazy package edges.
func LookupPackageImportByPath(dotPath string, ws *Workspace, stdlib StdlibProvider) *Package {
	if dotPath == "" {
		return nil
	}
	if ws != nil {
		if target := ws.Packages[dotPath]; target != nil {
			return target
		}
		if ws.Stdlib != nil {
			if target := ws.Stdlib.LookupPackage(dotPath); target != nil {
				return target
			}
		}
		if ws.Root != "" {
			target, err := ws.LoadPackageNative(dotPath)
			if err == nil && target != nil && !target.isCycleMarker {
				return target
			}
		}
	}
	if stdlib != nil {
		return stdlib.LookupPackage(dotPath)
	}
	return nil
}

func packageFrontendRuns(pkg *Package) []*selfhost.FrontendRun {
	if pkg == nil {
		return nil
	}
	runs := make([]*selfhost.FrontendRun, 0, len(pkg.Files))
	for _, pf := range pkg.Files {
		if pf == nil {
			continue
		}
		if pf.Run != nil {
			runs = append(runs, pf.Run)
			continue
		}
		if len(pf.Source) > 0 {
			runs = append(runs, selfhost.Run(pf.Source))
		}
	}
	return runs
}

func useAliasName(use *ast.UseDecl) string {
	if use == nil {
		return ""
	}
	if use.Alias != "" {
		return use.Alias
	}
	if use.RawPath != "" && strings.ContainsAny(use.RawPath, "/") {
		return lastSegment(use.RawPath)
	}
	if len(use.Path) == 0 {
		return ""
	}
	return lastSegment(use.Path[len(use.Path)-1])
}
