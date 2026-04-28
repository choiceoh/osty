package check

import (
	"path/filepath"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/selfhost"
)

func selfhostPackageCheckInput(pkg *resolve.Package, ws *resolve.Workspace, stdlib resolve.StdlibProvider, layout selfhostCheckedSource) selfhost.PackageCheckInput {
	input := selfhost.PackageCheckInput{
		Files:   make([]selfhost.PackageCheckFile, 0, len(layout.files)),
		Imports: selfhostPackageImportSurfaces(pkg, ws, stdlib),
	}
	segmentIdx := 0
	for _, pf := range pkg.Files {
		if pf == nil {
			continue
		}
		src := pf.CheckerSource()
		if len(src) == 0 {
			continue
		}
		base := 0
		if segmentIdx < len(layout.files) {
			base = layout.files[segmentIdx].base
		}
		name := ""
		if pf.Path != "" {
			name = filepath.Base(pf.Path)
		}
		input.Files = append(input.Files, selfhost.PackageCheckFile{
			Source: append([]byte(nil), src...),
			Base:   base,
			Name:   name,
			Path:   pf.Path,
		})
		segmentIdx++
	}
	return input
}

func selfhostSingleFileCheckInput(file *ast.File, src []byte, stdlib resolve.StdlibProvider) selfhost.PackageCheckInput {
	input := selfhost.PackageCheckInput{
		Imports: selfhostUsesImportSurfaces(fileUses(file), nil, stdlib),
	}
	if len(src) == 0 {
		return input
	}
	input.Files = append(input.Files, selfhost.PackageCheckFile{
		Source: append([]byte(nil), src...),
		Base:   0,
	})
	return input
}

// selfhostPackageImportSurfaces aggregates cross-package import surfaces
// for pkg by walking each `use` decl's target package's AstArena. It
// supersedes the *ast.File.Decls-based walker that used to live in this
// file — the workspace `--native` path lands here and must not trigger
// astbridge lowering. See LLVM_MIGRATION_PLAN.md § "Workspace --native".
func selfhostPackageImportSurfaces(pkg *resolve.Package, ws *resolve.Workspace, stdlib resolve.StdlibProvider) []selfhost.PackageCheckImport {
	if pkg == nil {
		return nil
	}
	seen := map[string]string{}
	var out []selfhost.PackageCheckImport
	for _, pf := range pkg.Files {
		if pf == nil {
			continue
		}
		run := runForPackageFile(pf)
		if run == nil {
			continue
		}
		for _, use := range selfhost.PackageUsesFromRun(run) {
			if use.IsGo || use.Alias == "" {
				continue
			}
			target := selfhostLookupPackageImportByPath(use.Path, ws, stdlib)
			if target == nil {
				continue
			}
			key := use.Path
			if prev, ok := seen[use.Alias]; ok {
				if prev == key {
					continue
				}
				continue
			}
			seen[use.Alias] = key
			out = append(out, selfhost.PackageImportSurface(use.Path, use.Alias, runsForPackage(target)))
		}
	}
	return out
}

// selfhostUsesImportSurfaces serves the single-file check path (caller
// already holds a parsed *ast.File). It delegates the actual surface
// walk to selfhost.PackageImportSurface so the shape matches the
// package-mode path exactly — only the alias-resolution source differs.
func selfhostUsesImportSurfaces(uses []*ast.UseDecl, ws *resolve.Workspace, stdlib resolve.StdlibProvider) []selfhost.PackageCheckImport {
	seen := map[string]string{}
	var out []selfhost.PackageCheckImport
	for _, use := range uses {
		target := selfhostLookupPackageImport(use, ws, stdlib)
		if target == nil {
			continue
		}
		alias := selfhostUseAlias(use)
		if alias == "" {
			continue
		}
		key := strings.Join(use.Path, ".")
		if prev, ok := seen[alias]; ok {
			if prev == key {
				continue
			}
			continue
		}
		seen[alias] = key
		out = append(out, selfhost.PackageImportSurface(key, alias, runsForPackage(target)))
	}
	return out
}

func selfhostLookupPackageImport(use *ast.UseDecl, ws *resolve.Workspace, stdlib resolve.StdlibProvider) *resolve.Package {
	if use == nil {
		return nil
	}
	return selfhostLookupPackageImportByPath(strings.Join(use.Path, "."), ws, stdlib)
}

func selfhostLookupPackageImportByPath(dotPath string, ws *resolve.Workspace, stdlib resolve.StdlibProvider) *resolve.Package {
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
	}
	if stdlib != nil {
		return stdlib.LookupPackage(dotPath)
	}
	return nil
}

func selfhostUseAlias(use *ast.UseDecl) string {
	if use == nil {
		return ""
	}
	if use.Alias != "" {
		return use.Alias
	}
	if len(use.Path) == 0 {
		return ""
	}
	return use.Path[len(use.Path)-1]
}

// runForPackageFile materializes a selfhost FrontendRun for pf without
// forcing *ast.File lowering. Files loaded via resolve.LoadPackageForNative
// already carry Run; legacy-loaded files (notably stdlib fixtures) are
// re-parsed from Source here. Both cases stay astbridge-free — parse
// cost for the legacy case is comparable to the original
// *ast.File.Decls walk it replaces.
func runForPackageFile(pf *resolve.PackageFile) *selfhost.FrontendRun {
	if pf == nil {
		return nil
	}
	if pf.Run != nil {
		return pf.Run
	}
	if len(pf.Source) == 0 {
		return nil
	}
	return selfhost.Run(pf.Source)
}

func runsForPackage(pkg *resolve.Package) []*selfhost.FrontendRun {
	if pkg == nil {
		return nil
	}
	runs := make([]*selfhost.FrontendRun, 0, len(pkg.Files))
	for _, pf := range pkg.Files {
		if run := runForPackageFile(pf); run != nil {
			runs = append(runs, run)
		}
	}
	return runs
}
