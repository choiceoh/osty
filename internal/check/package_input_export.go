package check

import (
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/selfhost"
)

// PackageImportSurfacesForSelfhost exposes the package-import surface adapter
// used by the host/native checker boundary so CLI-native workspace runners can
// stitch sibling-package exports into CheckPackageStructured without copying
// the surface-building logic.
func PackageImportSurfacesForSelfhost(pkg *resolve.Package, ws *resolve.Workspace, stdlib resolve.StdlibProvider) []selfhost.PackageCheckImport {
	return selfhostPackageImportSurfaces(pkg, ws, stdlib)
}
