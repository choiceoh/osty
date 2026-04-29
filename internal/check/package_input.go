package check

import (
	"path/filepath"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/selfhost/api"
)

func selfhostPackageCheckInput(pkg *resolve.Package, ws *resolve.Workspace, stdlib resolve.StdlibProvider, layout selfhostCheckedSource) api.PackageCheckInput {
	input := api.PackageCheckInput{
		Files:   make([]api.PackageCheckFile, 0, len(layout.files)),
		Imports: resolve.PackageImportSurfaces(pkg, ws, stdlib),
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
		input.Files = append(input.Files, api.PackageCheckFile{
			Source:       append([]byte(nil), src...),
			Base:         base,
			Name:         name,
			Path:         pf.Path,
			SourceFileID: string(pf.SourceFileID),
		})
		segmentIdx++
	}
	return input
}

func selfhostSingleFileCheckInput(file *ast.File, src []byte, stdlib resolve.StdlibProvider) api.PackageCheckInput {
	input := api.PackageCheckInput{
		Imports: resolve.PackageImportSurfacesForUses(fileUses(file), nil, stdlib),
	}
	if len(src) == 0 {
		return input
	}
	input.Files = append(input.Files, api.PackageCheckFile{
		Source: append([]byte(nil), src...),
		Base:   0,
	})
	return input
}

// selfhostPackageImportSurfaces is kept for tests and external adapters in
// this package; the resolver owns the actual import-surface contract.
func selfhostPackageImportSurfaces(pkg *resolve.Package, ws *resolve.Workspace, stdlib resolve.StdlibProvider) []api.PackageCheckImport {
	return resolve.PackageImportSurfaces(pkg, ws, stdlib)
}

// selfhostUsesImportSurfaces serves older package-local call sites; new code
// should call resolve.PackageImportSurfacesForUses directly.
func selfhostUsesImportSurfaces(uses []*ast.UseDecl, ws *resolve.Workspace, stdlib resolve.StdlibProvider) []api.PackageCheckImport {
	return resolve.PackageImportSurfacesForUses(uses, ws, stdlib)
}
