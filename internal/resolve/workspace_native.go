package resolve

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/osty/osty/internal/ast"
)

// LoadPackageNative loads PackageFile.Run for each source file and discovers
// transitive package uses from the selfhost arena. It deliberately leaves
// PackageFile.File nil; legacy consumers must call
// MaterializePublicCompatibility at their own boundary.
func (w *Workspace) LoadPackageNative(dotPath string) (*Package, error) {
	if pkg, ok := w.Packages[dotPath]; ok {
		return pkg, nil
	}
	if w.loading[dotPath] {
		return cycleMarker(dotPath), nil
	}
	if isURLStyle(dotPath) {
		return w.loadExternalDepNative(dotPath)
	}

	dir := w.dirFor(dotPath)
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) && strings.HasPrefix(dotPath, StdPrefix) {
			if w.Stdlib != nil {
				if pkg := w.Stdlib.LookupPackage(dotPath); pkg != nil {
					w.Packages[dotPath] = pkg
					return pkg, nil
				}
			}
			if w.stdlibStub {
				stub := stdlibStub(dotPath)
				w.Packages[dotPath] = stub
				return stub, nil
			}
		}
		if os.IsNotExist(err) && w.Deps != nil && !strings.ContainsAny(dotPath, ".") {
			if extDir, ok := w.Deps.LookupDep(dotPath); ok {
				return w.loadFromExternalDirNative(dotPath, extDir)
			}
		}
		return nil, fmt.Errorf("package %q: %w", dotPath, err)
	}

	w.loading[dotPath] = true
	defer delete(w.loading, dotPath)

	pkg, err := LoadPackageForNativeWithOptions(dir, w.loadOptions())
	if err != nil {
		return nil, err
	}
	pkg.Name = lastDotSeg(dotPath)
	w.Packages[dotPath] = pkg

	w.loadNativePackageDependencies(pkg)
	return pkg, nil
}

func (w *Workspace) loadExternalDepNative(rawPath string) (*Package, error) {
	if w.Deps == nil {
		return nil, fmt.Errorf("package %q: no dependency provider configured (did you forget `osty add`?)", rawPath)
	}
	dir, ok := w.Deps.LookupDep(rawPath)
	if !ok {
		return nil, fmt.Errorf("package %q: not found among declared dependencies", rawPath)
	}
	return w.loadFromExternalDirNative(rawPath, dir)
}

func (w *Workspace) loadFromExternalDirNative(key, dir string) (*Package, error) {
	w.loading[key] = true
	defer delete(w.loading, key)

	pkg, err := LoadPackageForNativeWithOptions(dir, w.loadOptions())
	if err != nil {
		return nil, err
	}
	pkg.Name = lastSegment(key)
	pkg.isExternalDep = true
	w.Packages[key] = pkg

	w.loadNativePackageDependencies(pkg)
	return pkg, nil
}

func (w *Workspace) loadNativePackageDependencies(pkg *Package) {
	for _, target := range packageUseDependencyKeys(pkg) {
		w.loadUseDependencyNative(target)
	}
}

func (w *Workspace) loadUseDependencyNative(target string) {
	if target == "" || w.loading[target] {
		return
	}
	if _, alreadyLoaded := w.Packages[target]; alreadyLoaded {
		return
	}
	if _, err := w.LoadPackageNative(target); err == nil {
		return
	}
}

func useDependencyKey(u *ast.UseDecl) string {
	if u == nil || u.IsFFI() {
		return ""
	}
	if u.IsScoped {
		return scopedUseBaseKey(u)
	}
	return UseKey(u)
}

func packageUseDependencyKeys(pkg *Package) []string {
	if pkg == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, ref := range packageImportUseRefs(pkg) {
		if ref.isGo {
			continue
		}
		target := ref.path
		if ref.isScoped {
			target = ref.scopedBase
		}
		if target == "" || seen[target] {
			continue
		}
		seen[target] = true
		out = append(out, target)
	}
	sort.Strings(out)
	return out
}
