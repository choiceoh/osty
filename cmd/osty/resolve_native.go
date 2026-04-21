package main

import (
	"fmt"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/resolve"
)

func nativeResolveSingleFileRows(path string, src []byte, file *ast.File) ([]resolve.NativeResolutionRow, error) {
	if file == nil {
		return nil, fmt.Errorf("native resolve: missing parsed file")
	}
	return resolve.NativeResolutionRowsForSource(path, src, file)
}

func nativeResolvePackageRows(pkg *resolve.Package, path string) ([]resolve.NativeResolutionRow, error) {
	return resolve.NativeResolutionRows(pkg, path)
}

func nativeResolveSingleFileDiagnostics(path string, src []byte, file *ast.File) ([]*diag.Diagnostic, error) {
	if file == nil {
		return nil, fmt.Errorf("native resolve: missing parsed file")
	}
	return resolve.NativeDiagnosticsForSource(path, src, file)
}

func nativeResolvePackageDiagnostics(pkg *resolve.Package) ([]*diag.Diagnostic, error) {
	return resolve.NativeDiagnostics(pkg)
}

func printNativeResolutionRows(rows []resolve.NativeResolutionRow) {
	for _, row := range rows {
		fmt.Printf("%d:%d\t%-20s\t%-12s\t->%s\n", row.Line, row.Column, row.Name, row.Kind, row.Def)
	}
}
