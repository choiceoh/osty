package resolve

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/manifest"
	"github.com/osty/osty/internal/selfhost"
)

// SourceTransform lets callers rewrite raw source bytes before the
// parser sees them. nil preserves the on-disk bytes.
type SourceTransform func(path string, src []byte) []byte

// LoadPackageForNative is the astbridge-free sibling of LoadPackage: it
// discovers every `.osty` file under dir (non-recursive, test files
// excluded) and parses each into a selfhost FrontendRun, but does NOT
// lower the arena to *ast.File and does NOT compute CanonicalSource.
// Use this from call sites that intend to drive resolve / check /
// llvmgen through the native Osty toolchain (toolchain/resolve.osty
// etc.) and only need *ast.File as a lazy fallback — pf.EnsureFile()
// on demand triggers exactly one astbridge lowering per file.
func LoadPackageForNative(dir string) (*Package, error) {
	return LoadPackageForNativeWithTransform(dir, nil)
}

// LoadPackageForNativeWithTransform is LoadPackageForNative plus an
// optional pre-parse source transform.
func LoadPackageForNativeWithTransform(dir string, transform SourceTransform) (*Package, error) {
	return loadPackageForNativeWithTransform(dir, transform, false)
}

// LoadPackageForNativeWithTests is like LoadPackageForNative but also includes
// every `*_test.osty` file. Used by `osty test` so test discovery starts from
// the selfhost arena instead of the old Go parser loader.
func LoadPackageForNativeWithTests(dir string) (*Package, error) {
	return LoadPackageForNativeWithTestsTransform(dir, nil)
}

// LoadPackageForNativeWithTestsTransform is LoadPackageForNativeWithTests plus
// an optional pre-parse source transform.
func LoadPackageForNativeWithTestsTransform(dir string, transform SourceTransform) (*Package, error) {
	return loadPackageForNativeWithTransform(dir, transform, true)
}

func loadPackageForNativeWithTransform(dir string, transform SourceTransform, includeTests bool) (*Package, error) {
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
		if !includeTests && strings.HasSuffix(name, "_test.osty") {
			continue
		}
		paths = append(paths, filepath.Join(abs, name))
	}
	sort.Strings(paths)
	return loadPackageNativePaths(paths, abs, filepath.Base(abs), transform)
}

func loadPackageNativePaths(paths []string, dir, name string, transform SourceTransform) (*Package, error) {
	pkg := &Package{Dir: dir, Name: name, RuntimeCapability: packageRuntimeCapability(dir)}
	for _, p := range paths {
		src, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", p, err)
		}
		if transform != nil {
			src = transform(p, src)
		}
		run := selfhost.Run(src)
		pkg.Files = append(pkg.Files, &PackageFile{
			Path:       p,
			Source:     src,
			Run:        run,
			ParseDiags: append([]*diag.Diagnostic(nil), run.Diagnostics()...),
		})
	}
	return pkg, nil
}

func packageRuntimeCapability(dir string) bool {
	src, err := os.ReadFile(filepath.Join(dir, manifest.ManifestFile))
	if err != nil {
		return false
	}
	m, err := manifest.Parse(src)
	if err != nil || m == nil || m.Capabilities == nil {
		return false
	}
	return m.Capabilities.Runtime
}
