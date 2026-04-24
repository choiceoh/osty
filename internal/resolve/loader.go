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

// LoadPackageWithTransform discovers and parses every `.osty` file
// directly under dir (non-recursive) and returns them as a single
// Package ready for ResolvePackage. Test files (`*_test.osty`) are
// excluded per v0.4 §11 so production builds don't drag test-only
// declarations into scope. Accepts an optional pre-parse source
// transform — callers can normalize foreign syntax or inject generated
// source before diagnostics are computed.
//
// The returned Package has Files sorted lexicographically by path for
// deterministic diagnostic ordering.
//
// Public callers should prefer LoadPackageArenaFirst, which routes
// through the selfhost arena-first loader. This function is retained
// as the internal primitive workspace.go uses for its own loading
// machinery.
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

// LoadPackageArenaFirst is the recommended loader for CLI / pipeline /
// LSP / cihost consumers that want the astbridge-free parse while still
// handing downstream passes a Package with pf.File / pf.CanonicalSource
// populated in the same shape LoadPackage produced. Internally it runs
// LoadPackageForNative, EnsureFiles (lazy astbridge lowering per file),
// and MaterializeCanonicalSources. Phase 1c.2 migration target — see
// SELFHOST_PORT_MATRIX.md.
func LoadPackageArenaFirst(dir string) (*Package, error) {
	return LoadPackageArenaFirstWithTransform(dir, nil)
}

// LoadPackageArenaFirstWithTransform is LoadPackageArenaFirst plus an
// optional pre-parse source transform (e.g. airepair).
func LoadPackageArenaFirstWithTransform(dir string, transform SourceTransform) (*Package, error) {
	pkg, err := LoadPackageForNativeWithTransform(dir, transform)
	if err != nil {
		return nil, err
	}
	pkg.EnsureFiles()
	pkg.MaterializeCanonicalSources()
	return pkg, nil
}

// LoadPackageArenaFirst is the Workspace-level sibling of the
// package-level LoadPackageArenaFirst. Delegates to LoadPackageNative
// (arena-first parse + lazy *ast.File lowering) and then materializes
// canonical sources so downstream consumers observe the full legacy
// Package shape.
func (w *Workspace) LoadPackageArenaFirst(dotPath string) (*Package, error) {
	pkg, err := w.LoadPackageNative(dotPath)
	if err != nil {
		return nil, err
	}
	if pkg != nil {
		pkg.EnsureFiles()
		pkg.MaterializeCanonicalSources()
	}
	return pkg, nil
}
