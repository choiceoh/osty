package check

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/resolve"
)

// The deleted bootstrap selfhost checker is intentionally not reintroduced
// here. Until the runtime/native boundary lands, the checker surface stays as
// an empty host boundary so parser/resolve-driven workflows can keep moving.
func applySelfhostFileResult(*Result, *ast.File, *resolve.Result, []byte, resolve.StdlibProvider) {}

func applySelfhostPackageResult(*Result, *resolve.Package, *resolve.PackageResult, *resolve.Workspace, resolve.StdlibProvider) {
}

func applySelfhostWorkspaceResults(*resolve.Workspace, map[string]*resolve.PackageResult, map[string]*Result, resolve.StdlibProvider) {
}
