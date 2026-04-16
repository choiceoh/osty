package resolve

import (
	"fmt"
	"path/filepath"
	"sort"
)

// LoadPackageFiles loads the provided Osty source files as one package while
// preserving their original paths for diagnostics. Paths may span a synthetic
// selection rather than a full on-disk directory listing.
func LoadPackageFiles(paths []string, stdlib StdlibProvider) (*Package, error) {
	return LoadPackageFilesWithTransform(paths, stdlib, nil)
}

// LoadPackageFilesWithTransform is LoadPackageFiles plus an optional
// pre-parse source transform.
func LoadPackageFilesWithTransform(paths []string, stdlib StdlibProvider, transform SourceTransform) (*Package, error) {
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
	loaded, err := loadPackagePaths(absPaths, dir, filepath.Base(dir), transform)
	if err != nil {
		return nil, err
	}
	pkg.Files = loaded.Files
	return pkg, nil
}
