package resolve

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/osty/osty/internal/parser"
)

// LoadPackage discovers and parses every `.osty` file directly under
// dir (non-recursive) and returns them as a single Package ready for
// ResolvePackage. Test files (`*_test.osty`) are excluded per v0.3 §11
// so production builds don't drag test-only declarations into scope.
//
// The returned Package has Files sorted lexicographically by path for
// deterministic diagnostic ordering.
func LoadPackage(dir string) (*Package, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", abs, err)
	}

	var paths []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".osty") {
			continue
		}
		if strings.HasSuffix(name, "_test.osty") {
			continue
		}
		paths = append(paths, filepath.Join(abs, name))
	}
	sort.Strings(paths)

	pkg := &Package{
		Dir:  abs,
		Name: filepath.Base(abs),
	}
	for _, p := range paths {
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

// LoadPackageWithTests is like LoadPackage but also includes every
// `*_test.osty` file. Used by `osty test` and similar commands.
func LoadPackageWithTests(dir string) (*Package, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", abs, err)
	}
	var paths []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".osty") {
			paths = append(paths, filepath.Join(abs, name))
		}
	}
	sort.Strings(paths)

	pkg := &Package{Dir: abs, Name: filepath.Base(abs)}
	for _, p := range paths {
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
