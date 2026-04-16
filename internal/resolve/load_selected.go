package resolve

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/osty/osty/internal/parser"
)

// LoadPackageFiles loads the provided Osty source files as one package while
// preserving their original paths for diagnostics. Paths may span a synthetic
// selection rather than a full on-disk directory listing.
func LoadPackageFiles(paths []string, stdlib StdlibProvider) (*Package, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("load selected package: no files")
	}
	absPaths := make([]string, 0, len(paths))
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, err
		}
		absPaths = append(absPaths, abs)
	}
	sort.Strings(absPaths)
	dir := filepath.Dir(absPaths[0])
	pkg := &Package{
		Dir:  dir,
		Name: filepath.Base(dir),
	}
	if stdlib != nil {
		pkg.workspace = newStdlibOnlyWorkspace(stdlib)
	}
	for _, p := range absPaths {
		src, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", p, err)
		}
		file, parseDiags := parser.ParseDiagnostics(src)
		pkg.Files = append(pkg.Files, &PackageFile{
			Path:       p,
			Source:     src,
			File:       file,
			ParseDiags: parseDiags,
		})
	}
	return pkg, nil
}
