package resolve

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/osty/osty/internal/canonical"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/selfhost"
)

// SourceTransform lets callers rewrite raw source bytes before the
// parser sees them. nil preserves the on-disk bytes.
type SourceTransform func(path string, src []byte) []byte

// LoadPackage discovers and parses every `.osty` file directly under
// dir (non-recursive) and returns them as a single Package ready for
// ResolvePackage. Test files (`*_test.osty`) are excluded per v0.4 §11
// so production builds don't drag test-only declarations into scope.
//
// The returned Package has Files sorted lexicographically by path for
// deterministic diagnostic ordering.
func LoadPackage(dir string) (*Package, error) {
	return LoadPackageWithTransform(dir, nil)
}

// LoadPackageWithTransform is LoadPackage plus an optional pre-parse
// source transform. The callback can normalize foreign syntax or inject
// generated source before diagnostics are computed.
func LoadPackageWithTransform(dir string, transform SourceTransform) (*Package, error) {
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
	return loadPackagePaths(paths, abs, filepath.Base(abs), transform)
}

// LoadPackageWithTests is like LoadPackage but also includes every
// `*_test.osty` file. Used by `osty test` and similar commands.
func LoadPackageWithTests(dir string) (*Package, error) {
	return LoadPackageWithTestsTransform(dir, nil)
}

// LoadPackageWithTestsTransform is LoadPackageWithTests plus an
// optional pre-parse source transform.
func LoadPackageWithTestsTransform(dir string, transform SourceTransform) (*Package, error) {
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
	return loadPackagePaths(paths, abs, filepath.Base(abs), transform)
}

func loadPackagePaths(paths []string, dir, name string, transform SourceTransform) (*Package, error) {
	pkg := &Package{Dir: dir, Name: name}
	for _, p := range paths {
		src, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", p, err)
		}
		if transform != nil {
			src = transform(p, src)
		}
		parsed := parser.ParseDetailed(src)
		canonicalSrc, canonicalMap := canonical.SourceWithMap(src, parsed.File)
		pkg.Files = append(pkg.Files, &PackageFile{
			Path:            p,
			Source:          src,
			CanonicalSource: canonicalSrc,
			CanonicalMap:    canonicalMap,
			File:            parsed.File,
			ParseDiags:      parsed.Diagnostics,
			ParseProvenance: parsed.Provenance,
		})
	}
	return pkg, nil
}

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
	return loadPackageNativePaths(paths, abs, filepath.Base(abs), transform)
}

func loadPackageNativePaths(paths []string, dir, name string, transform SourceTransform) (*Package, error) {
	pkg := &Package{Dir: dir, Name: name}
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
