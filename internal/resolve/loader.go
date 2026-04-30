package resolve

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/manifest"
	"github.com/osty/osty/internal/selfhost"
	"github.com/osty/osty/internal/sourcemap"
	"github.com/osty/osty/internal/spanid"
)

// SourceTransform lets callers rewrite raw source bytes before the
// parser sees them. nil preserves the on-disk bytes.
type SourceTransform func(path string, src []byte) []byte

// SourceTransformResult is the structured form of SourceTransform.
// Map projects Source spans back onto the bytes supplied to the transformer.
// When MapKnown is true, Map is authoritative; a nil Map explicitly means the
// transformed source should be treated as the diagnostic source too.
type SourceTransformResult struct {
	Source   []byte
	Map      *sourcemap.Map
	MapKnown bool
}

// SourceTransformer lets callers rewrite source and provide an exact source
// map. It is useful for non-file-backed sources where deriving an on-disk
// diagnostic map would be wrong, such as LSP open-buffer overlays.
type SourceTransformer func(path string, src []byte) SourceTransformResult

// LoadOptions is the unified native loader configuration shared by package,
// test-package, selected-file, and workspace call paths.
type LoadOptions struct {
	IncludeTests bool
	Transform    SourceTransform
	Transformer  SourceTransformer
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
	return LoadPackageForNativeWithOptions(dir, LoadOptions{Transform: transform})
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
	return LoadPackageForNativeWithOptions(dir, LoadOptions{IncludeTests: true, Transform: transform})
}

func LoadPackageForNativeWithOptions(dir string, opts LoadOptions) (*Package, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	paths, err := PackageSourcePaths(abs, opts.IncludeTests)
	if err != nil {
		return nil, err
	}
	return loadPackageNativePaths(paths, abs, filepath.Base(abs), opts)
}

// PackageSourcePaths returns the source files that belong to a package root.
// Root-level .osty files are included for the traditional layout, and explicit
// manifest entry paths such as [bin].path = "src/main.osty" are added so
// scaffolded applications with assets under the root still compile.
func PackageSourcePaths(dir string, includeTests bool) ([]string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", abs, err)
	}
	var paths []string
	seen := map[string]bool{}
	addSource := func(path string) {
		path = filepath.Clean(path)
		name := filepath.Base(path)
		if !strings.HasSuffix(name, ".osty") {
			return
		}
		if !includeTests && strings.HasSuffix(name, "_test.osty") {
			return
		}
		if seen[path] {
			return
		}
		seen[path] = true
		paths = append(paths, path)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		addSource(filepath.Join(abs, e.Name()))
	}
	if m := packageManifest(abs); m != nil {
		if m.Lib != nil && m.Lib.Path != "" {
			addSource(filepath.Join(abs, m.Lib.Path))
		}
		if m.Bin != nil && m.Bin.Path != "" {
			addSource(filepath.Join(abs, m.Bin.Path))
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func packageManifest(dir string) *manifest.Manifest {
	src, err := os.ReadFile(filepath.Join(dir, manifest.ManifestFile))
	if err != nil {
		return nil
	}
	m, err := manifest.Parse(src)
	if err != nil {
		return nil
	}
	return m
}

func loadPackageNativePaths(paths []string, dir, name string, opts LoadOptions) (*Package, error) {
	pkg := &Package{Dir: dir, Name: name, RuntimeCapability: packageRuntimeCapability(dir)}
	for _, p := range paths {
		original, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", p, err)
		}
		src, transformMap := ApplySourceTransform(p, original, opts)
		transformApplied := opts.Transform != nil || opts.Transformer != nil
		transformChanged := transformApplied && !bytes.Equal(src, original)
		originalSourceID := spanid.SourceFileIDFor(p)
		sourceFileID := originalSourceID
		if transformMap != nil {
			sourceFileID = spanid.DerivedSourceFileID(originalSourceID, spanid.ProvenanceExpansion, "source-transform")
			transformMap.StampFileIDs(originalSourceID, sourceFileID)
		}
		run := selfhost.Run(src)
		diags := append([]*diag.Diagnostic(nil), run.Diagnostics()...)
		for _, d := range diags {
			if d == nil {
				continue
			}
			if d.File == "" {
				d.File = p
			}
			diag.StampDiagnosticSourceFileID(d, sourceFileID)
		}
		if transformMap != nil {
			diags = transformMap.RemapDiagnosticsProjected(diags)
		}
		pf := &PackageFile{
			Path:                   p,
			Source:                 src,
			SourceFileID:           sourceFileID,
			SourceTransformApplied: transformApplied,
			SourceTransformChanged: transformChanged,
			Run:                    run,
			ParseDiags:             diags,
		}
		if transformMap != nil {
			pf.OriginalSource = append([]byte(nil), original...)
			pf.TransformMap = transformMap
		}
		pkg.Files = append(pkg.Files, pf)
	}
	return pkg, nil
}

// ApplySourceTransform applies the loader's pre-parse source rewrite policy.
// Legacy Transform functions get a derived best-effort source map when their
// output changes. Structured Transformer results may provide an exact map, or
// set MapKnown with a nil map to opt out of original-source remapping.
func ApplySourceTransform(path string, original []byte, opts LoadOptions) ([]byte, *sourcemap.Map) {
	if opts.Transformer != nil {
		result := opts.Transformer(path, append([]byte(nil), original...))
		src := append([]byte(nil), result.Source...)
		if result.MapKnown {
			return src, result.Map
		}
		if bytes.Equal(src, original) {
			return src, nil
		}
		return src, sourcemap.FromSourceTransform(original, src)
	}
	if opts.Transform == nil {
		return append([]byte(nil), original...), nil
	}
	src := opts.Transform(path, append([]byte(nil), original...))
	if bytes.Equal(src, original) {
		return append([]byte(nil), src...), nil
	}
	return append([]byte(nil), src...), sourcemap.FromSourceTransform(original, src)
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
