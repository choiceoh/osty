//go:build selfhostgen

package check

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/resolve"
)

func applySelfhostFileResult(result *Result, file *ast.File, src []byte, stdlib resolve.StdlibProvider) {
}

func applySelfhostPackageResult(result *Result, pkg *resolve.Package, ws *resolve.Workspace, stdlib resolve.StdlibProvider) {
}

func applySelfhostWorkspaceResults(ws *resolve.Workspace, results map[string]*Result, stdlib resolve.StdlibProvider) {
}
