package resolve

import (
	"fmt"
	"os"
	"strings"

	"github.com/osty/osty/internal/selfhost"
)

// LoadPackageNative is the astbridge-counter-free sibling of LoadPackage.
// Packages are still lowered to public *ast.File nodes for workspace import
// discovery and import-surface stitching, but that lowering happens through
// selfhost.LowerPublicFileFromRun instead of FrontendRun.File(), so the native
// CLI workspace path can stay off the runtime.golegacy.astbridge counter.
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

	pkg, err := LoadPackageForNativeWithTransform(dir, w.SourceTransform)
	if err != nil {
		return nil, err
	}
	pkg.Name = lastDotSeg(dotPath)
	nativeMaterializePackageFiles(pkg)
	w.Packages[dotPath] = pkg

	for _, f := range pkg.Files {
		if f == nil || f.File == nil {
			continue
		}
		for _, u := range f.File.Uses {
			if u.IsFFI() {
				continue
			}
			target := UseKey(u)
			if target == "" {
				continue
			}
			if w.loading[target] {
				continue
			}
			if _, alreadyLoaded := w.Packages[target]; alreadyLoaded {
				continue
			}
			_, _ = w.LoadPackageNative(target)
		}
	}
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

	pkg, err := LoadPackageForNativeWithTransform(dir, w.SourceTransform)
	if err != nil {
		return nil, err
	}
	pkg.Name = lastSegment(key)
	nativeMaterializePackageFiles(pkg)
	w.Packages[key] = pkg

	for _, f := range pkg.Files {
		if f == nil || f.File == nil {
			continue
		}
		for _, u := range f.File.Uses {
			if u.IsFFI() {
				continue
			}
			target := UseKey(u)
			if target == "" || w.loading[target] {
				continue
			}
			if _, alreadyLoaded := w.Packages[target]; alreadyLoaded {
				continue
			}
			_, _ = w.LoadPackageNative(target)
		}
	}
	return pkg, nil
}

func nativeMaterializePackageFiles(pkg *Package) {
	if pkg == nil {
		return
	}
	for _, pf := range pkg.Files {
		if pf == nil || pf.File != nil || pf.Run == nil {
			continue
		}
		pf.File = selfhost.LowerPublicFileFromRun(pf.Run)
	}
}
