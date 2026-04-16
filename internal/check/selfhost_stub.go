//go:build selfhostgen

package check

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/resolve"
)

func selfhostRuntimeAvailable() bool { return false }

func applySelfhostFileResult(result *Result, file *ast.File, rr *resolve.Result, src []byte, stdlib resolve.StdlibProvider) {
}

func applySelfhostPackageResult(result *Result, pkg *resolve.Package, pr *resolve.PackageResult, ws *resolve.Workspace, stdlib resolve.StdlibProvider) {
}

func applySelfhostWorkspaceResults(ws *resolve.Workspace, resolved map[string]*resolve.PackageResult, results map[string]*Result, stdlib resolve.StdlibProvider) {
}
