// Package resolve owns the Go-side projection of name resolution. The
// authoritative resolver is `toolchain/resolve.osty` (compiled into
// `internal/selfhost/generated.go`); this file only exposes the public
// entry points and projects the selfhost result back onto Go's
// PackageResult / Scope / Symbol shapes via internal/resolve/resolve_bridge.go.
package resolve

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
)

// Result is the output of resolving one Osty file.
type Result struct {
	// RefsByID maps each resolved Ident's NodeID to the symbol it
	// refers to. Idents that failed resolution are not present in the
	// map (a corresponding diagnostic is emitted instead).
	RefsByID map[ast.NodeID]*Symbol
	// TypeRefsByID maps each NamedType's NodeID to the symbol the head
	// name refers to (e.g. for `Map<String, Int>` it records the ref
	// for `Map`). Only the head symbol is recorded; type arguments are
	// resolved recursively under their own NamedType keys.
	TypeRefsByID map[ast.NodeID]*Symbol
	// RefIdents / TypeRefIdents enumerate the nodes behind RefsByID /
	// TypeRefsByID, in no particular order. Callers that need to walk
	// every resolved identifier iterate these instead of the maps so
	// ports to the self-hosted compiler map cleanly onto
	// List<&Ident> / List<&NamedType>.
	RefIdents     []*ast.Ident
	TypeRefIdents []*ast.NamedType
	// FileScope is the file-level scope (children of the prelude). All
	// top-level declarations live here.
	FileScope *Scope
	// Diags collects every diagnostic produced during resolution.
	Diags []*diag.Diagnostic
}

// ResolvePackage runs name resolution over every file in pkg as a single
// namespace (§5.1). Top-level declarations share a package scope that is
// a child of the given prelude; each file gets its own file-scope child
// for `use` aliases.
//
// The resolver mutates pkg.Files[i].FileScope / Refs / TypeRefs in place.
// Existing parser diagnostics on each file are also merged into the
// returned Diags list.
//
// The authoritative execution path is the selfhost resolver
// (`toolchain/resolve.osty`) projected back onto Go's PackageResult /
// Scope / Symbol shapes.
func ResolvePackage(pkg *Package, prelude *Scope) *PackageResult {
	if pkg == nil {
		return &PackageResult{}
	}
	pkg.MaterializePublicCompatibility()
	pkg.MaterializeCanonicalSources()
	return resolvePackageViaNative(pkg, prelude)
}

// ResolvePackageDefault resolves a single package using the standard
// prelude (NewPrelude). This is the convenience entry point for callers
// that do not share a prelude across multiple packages; internally it
// allocates a fresh prelude on each call.
//
// Callers that resolve many packages in a loop (workspace mode, CI,
// stdlib loading) should prefer Workspace.ResolveAll or explicitly
// reuse a single prelude with ResolvePackage to avoid redundant
// allocations.
func ResolvePackageDefault(pkg *Package) *PackageResult {
	return ResolvePackage(pkg, NewPrelude())
}

// ResolveFileSourceDefault runs single-file resolution from source bytes
// and the already-parsed AST, using the selfhost resolver as the source
// of truth while still projecting refs and scopes back onto the Go AST.
func ResolveFileSourceDefault(src []byte, file *ast.File, stdlib StdlibProvider) *Result {
	graph := NewSingleFilePackageGraph(src, file, stdlib)
	results := ResolveGraph(graph)
	pkg := graph.Package("")
	if pkg == nil {
		return &Result{}
	}
	pr := results[""]
	if pr == nil {
		pr = &PackageResult{}
	}
	pf := pkg.Files[0]
	return &Result{
		RefsByID:      pf.RefsByID,
		TypeRefsByID:  pf.TypeRefsByID,
		RefIdents:     pf.RefIdents,
		TypeRefIdents: pf.TypeRefIdents,
		FileScope:     pf.FileScope,
		Diags:         pr.Diags,
	}
}

// newStdlibOnlyWorkspace builds a Workspace whose sole purpose is to
// route `std.*` imports through the given provider. It has no on-disk
// Root, so non-std `use` targets still report as unknown packages —
// exactly the behavior a single-file compile should have.
func newStdlibOnlyWorkspace(stdlib StdlibProvider) *Workspace {
	return &Workspace{
		Root:       "",
		Packages:   map[string]*Package{},
		Stdlib:     stdlib,
		stdlibStub: true,
		loading:    map[string]bool{},
	}
}
