// Package stdlib loads the Osty standard library signatures into the
// compiler's symbol tables.
//
// Stdlib modules are authored as .osty stub files under `modules/`
// (and, once primitive method stubs land, `primitives/`). Each stub
// declares types, interfaces, and body-less `fn` signatures that mirror
// the language specification in `LANG_SPEC_v0.3/10-standard-library/`.
// The stubs are embedded at build time and parsed by the existing
// compiler front-end so the stdlib uses the same syntax rules as user
// code.
//
// The package both parses and resolves every stub at Load time and
// exposes a Registry that satisfies resolve.StdlibProvider. A workspace
// with `ws.Stdlib = registry` routes `use std.*` imports through the
// registry so member access (`io.print`, `error.Error`) validates
// against the module's PkgScope rather than an opaque stub.
package stdlib

import (
	"embed"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/types"
)

// Compile-time check: Registry satisfies resolve.StdlibProvider so a
// Workspace can attach it directly via `ws.Stdlib = registry`.
var _ resolve.StdlibProvider = (*Registry)(nil)

// stubExt is the extension every stdlib stub file uses.
const stubExt = ".osty"

// stubs holds every stdlib signature stub embedded at build time. The
// `modules/` tree contains per-module interfaces and types; the
// `primitives/` tree contains `#[intrinsic_methods]`-annotated
// placeholder structs that expand into primitive method tables.
//
//go:embed modules primitives
var stubs embed.FS

// Registry is the result of Load: one entry per parsed stdlib stub,
// plus any diagnostics produced during parsing. The struct exposes
// three read-only lookup surfaces — Modules, Primitives, and Diags —
// that downstream passes consume independently.
type Registry struct {
	// Modules is keyed by the stub's logical name ("io", "collections",
	// ...), derived from the filename without its `.osty` extension.
	// Primitive stubs under `primitives/` do not appear here; they feed
	// Primitives instead.
	Modules map[string]*Module
	// Primitives holds methods declared on primitive-type placeholder
	// structs (via `#[intrinsic_methods(...)]`). Outer key is the
	// primitive kind; inner key is the method name. The method's AST
	// carries signature and body; the type checker consumes this map
	// when resolving `x.abs()`-style calls where `x` is a primitive.
	Primitives map[types.PrimitiveKind]map[string]*ast.FnDecl
	// Diags aggregates parse diagnostics from every stub. A well-formed
	// Registry has zero error-severity entries.
	Diags []*diag.Diagnostic
}

// Module is one parsed stdlib stub file.
type Module struct {
	// Name is the logical module name (stub basename minus extension),
	// e.g. "io" or "collections".
	Name string
	// Path is the embed.FS path of the stub, e.g. "modules/io.osty".
	// Useful for diagnostic context and tests.
	Path string
	// Source is the raw bytes of the stub as embedded.
	Source []byte
	// File is the parsed AST. Nil only if parsing failed catastrophically
	// (which should not happen in a well-formed stub set).
	File *ast.File
	// Package is the resolved view of the module, produced by running
	// the resolver over File. Its PkgScope holds every top-level
	// symbol (pub or not), which is exactly what the workspace consumes
	// when a user writes `use std.X; X.member`.
	Package *resolve.Package
}

// Load reads every embedded stub, parses it, resolves it against a
// fresh prelude, and returns a Registry. Parse and resolve diagnostics
// from every stub are aggregated in Registry.Diags; a well-formed stub
// set produces none.
//
// Eager resolution means stubs' signatures are validated once at Load
// time rather than lazily on first import, so drift in a stub is
// surfaced as a failing test rather than a cryptic runtime error.
//
// Long-running tools (CLI, LSP) should prefer LoadCached to avoid
// re-parsing on every invocation.
func Load() *Registry {
	r := &Registry{
		Modules:    map[string]*Module{},
		Primitives: map[types.PrimitiveKind]map[string]*ast.FnDecl{},
	}

	// One prelude shared across every stub's resolve pass. The prelude
	// is read-only after NewPrelude returns, so handing the same
	// instance to every ResolvePackage call is equivalent to allocating
	// fresh copies — just cheaper.
	prelude := resolve.NewPrelude()

	for _, p := range collectStubPaths() {
		src, err := stubs.ReadFile(p)
		if err != nil {
			r.Diags = append(r.Diags, diag.New(diag.Error,
				"stdlib: failed to read embedded stub "+p+": "+err.Error()).
				Build())
			continue
		}
		file, diags := parser.ParseDiagnostics(src)
		r.Diags = append(r.Diags, diags...)
		if file == nil {
			continue
		}
		pkg := &resolve.Package{
			Name: moduleName(p),
			Files: []*resolve.PackageFile{{
				Path:   p,
				Source: src,
				File:   file,
			}},
		}
		result := resolve.ResolvePackage(pkg, prelude)
		r.Diags = append(r.Diags, result.Diags...)

		// Primitive stubs fan their methods out into the primitive-kind
		// table; their placeholder struct is never visible as a module.
		// Everything else lands in Modules as a conventional stdlib
		// import target.
		if strings.HasPrefix(p, "primitives/") {
			r.absorbPrimitiveStub(file, p)
			continue
		}
		r.Modules[moduleName(p)] = &Module{
			Name:    moduleName(p),
			Path:    p,
			Source:  src,
			File:    file,
			Package: pkg,
		}
	}
	return r
}

// absorbPrimitiveStub walks the `#[intrinsic_methods(...)]`-annotated
// struct inside a `primitives/` file and copies each of its methods
// into r.Primitives under every primitive kind named in the
// annotation. A parse-error diagnostic is emitted when the annotation
// names an unknown primitive; the method is silently skipped for that
// kind so the rest of the registry stays usable.
func (r *Registry) absorbPrimitiveStub(file *ast.File, path string) {
	for _, decl := range file.Decls {
		sd, ok := decl.(*ast.StructDecl)
		if !ok {
			continue
		}
		kinds := primitiveKindsFromAnnotations(sd.Annotations)
		if len(kinds) == 0 {
			continue
		}
		for _, m := range sd.Methods {
			for _, k := range kinds {
				if r.Primitives[k] == nil {
					r.Primitives[k] = map[string]*ast.FnDecl{}
				}
				r.Primitives[k][m.Name] = m
			}
		}
		_ = path // reserved for richer diagnostics in a later step
	}
}

// primitiveKindsFromAnnotations extracts the primitive kinds named as
// positional args to a `#[intrinsic_methods(...)]` annotation. The
// annotation parser stores each positional name in arg.Key with no
// Value; unknown kinds are ignored so stdlib loading remains best-
// effort in the face of forward-compatible stubs.
func primitiveKindsFromAnnotations(anns []*ast.Annotation) []types.PrimitiveKind {
	var out []types.PrimitiveKind
	for _, a := range anns {
		if a.Name != "intrinsic_methods" {
			continue
		}
		for _, arg := range a.Args {
			if k, ok := primitiveKindByName(arg.Key); ok {
				out = append(out, k)
			}
		}
	}
	return out
}

// primitiveKindByName looks up the PrimitiveKind that the
// `#[intrinsic_methods(Int, Int8, ...)]` annotation names. Delegates to
// types.PrimitiveByName so the primitive-name → type mapping stays in
// one place.
func primitiveKindByName(name string) (types.PrimitiveKind, bool) {
	p := types.PrimitiveByName(name)
	if p == nil {
		return 0, false
	}
	return p.Kind, true
}

// LoadCached returns a Registry loaded once per process. Callers that
// only read the result (the common case) can safely share this instance
// across goroutines — the Registry is read-only after Load completes.
// Tests that need a fresh Registry should call Load directly.
func LoadCached() *Registry {
	cachedOnce.Do(func() {
		cached = Load()
	})
	return cached
}

var (
	cachedOnce sync.Once
	cached     *Registry
)

// LookupPackage returns the resolved Package for the dotted stdlib
// import path, or nil when the path does not correspond to a bundled
// module. Satisfies resolve.StdlibProvider so a Workspace can plug the
// registry straight into its `std.*` resolution path.
func (r *Registry) LookupPackage(dotPath string) *resolve.Package {
	name, ok := strings.CutPrefix(dotPath, resolve.StdPrefix)
	if !ok {
		return nil
	}
	if mod, ok := r.Modules[name]; ok {
		return mod.Package
	}
	return nil
}

// collectStubPaths walks the embedded file system and returns every
// .osty path in lexical order. Deterministic ordering keeps diagnostic
// output stable across runs.
func collectStubPaths() []string {
	var paths []string
	_ = fs.WalkDir(stubs, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.HasSuffix(p, stubExt) {
			paths = append(paths, p)
		}
		return nil
	})
	sort.Strings(paths)
	return paths
}

// moduleName derives a module's logical name from its stub path.
// "modules/io.osty" -> "io"; "primitives/int.osty" -> "int".
func moduleName(p string) string {
	return strings.TrimSuffix(path.Base(p), stubExt)
}
