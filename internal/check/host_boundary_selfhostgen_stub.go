//go:build selfhostgen

package check

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/resolve"
)

// selfhost generation only needs cmd/osty to compile; the native checker
// boundary is not executed while regenerating the selfhost package.
func applyNativeFileResult(result *Result, file *ast.File, rr *resolve.Result, src []byte, stdlib resolve.StdlibProvider) {
}

func applyNativePackageResult(result *Result, pkg *resolve.Package, pr *resolve.PackageResult, ws *resolve.Workspace, stdlib resolve.StdlibProvider) {
}

func applyNativeWorkspaceResults(ws *resolve.Workspace, resolved map[string]*resolve.PackageResult, results map[string]*Result, stdlib resolve.StdlibProvider) {
}
