package resolve

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/canonical"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/selfhost"
	"github.com/osty/osty/internal/sourcemap"
)

// Package is the resolver's view of one Osty package — i.e. one directory
// of `.osty` source files (§5.1 "package = directory"). All top-level
// declarations across the package share a single namespace, while
// file-local `use` aliases live in each file's own scope.
type Package struct {
	// Dir is the package directory, typically an absolute path. Used as
	// the stable key in loader caches and as a human-readable tag in
	// diagnostics.
	Dir string
	// Name is the package name — conventionally the base name of Dir.
	Name string
	// Files is every `.osty` source file in the package, in
	// discovery-stable order (lexicographic by path).
	Files []*PackageFile
	// PkgScope is the shared top-level scope populated by
	// ResolvePackage. After resolution finishes, it contains every
	// `Symbol` declared anywhere in the package (pub or not). Consumers
	// that need exported-only members should filter with Symbol.Pub.
	PkgScope *Scope

	// workspace is set by Workspace.ResolveAll so each package's
	// `use` declarations can navigate to siblings. Outside the workspace
	// path it stays nil (single-package resolution still works).
	workspace *Workspace
	// isStub marks a package that exists for `SymPackage` purposes but
	// has no source files (stdlib placeholder). Member access on a
	// SymPackage whose `Package.isStub` is true is permissive.
	isStub bool
	// isCycleMarker flags the sentinel Package returned by LoadPackage
	// when a cycle is detected mid-load. The resolver emits
	// CodeCyclicImport against the use site that completed the cycle.
	isCycleMarker bool
	// nativeResolve caches the read-only selfhost resolve projection so
	// host-side tools can reuse one native run across several queries.
	nativeResolve nativeResolveCache
	// RuntimeCapability is true when this package opted into the runtime
	// sublanguage via `[capabilities] runtime = true` in osty.toml.
	RuntimeCapability bool
}

// PackageFile bundles one parsed source file with the resolver outputs
// produced when walking it.
type PackageFile struct {
	// Path is the file's filesystem path.
	Path string
	// Source is the raw UTF-8 bytes. Retained so diagnostics can render
	// source snippets without re-reading the file.
	Source []byte
	// CanonicalSource is the checker-facing canonical Osty source produced from
	// the parsed AST after parser-owned canonicalization. When empty,
	// callers should fall back to Source.
	CanonicalSource []byte
	// CanonicalMap projects canonical source spans back onto the original source
	// spans carried by the parsed AST. Nil when the canonical source is a direct
	// passthrough or no map was produced.
	CanonicalMap *sourcemap.Map
	// File is the public AST compatibility surface. Native-loaded packages
	// populate Run instead and leave this nil until EnsureFile is called.
	// Legacy passes that still traverse Go AST should materialize it at the
	// explicit boundary rather than relying on loader side effects.
	File *ast.File
	// Run is the self-host FrontendRun that produced this file's arena.
	// Populated by LoadPackageForNative so the native resolver / checker /
	// llvmgen can consume the arena directly, skipping public AST
	// materialization unless a compatibility consumer asks for it.
	Run *selfhost.FrontendRun
	// ParseDiags are diagnostics produced during lex + parse of this
	// file. They are merged with resolver diagnostics by the package
	// walker.
	ParseDiags []*diag.Diagnostic
	// ParseProvenance records parser-owned stable alias absorption that remains
	// useful at legacy boundaries. Helper syntax is canonicalized in the parser
	// core before resolve/check.
	ParseProvenance *parser.Provenance
	// FileScope is the file-local scope (contains this file's `use`
	// aliases). Populated by ResolvePackage. Its parent is the package
	// scope; the resolver walks per-file expression bodies rooted here.
	FileScope *Scope
	// RefsByID and TypeRefsByID record where each identifier /
	// type-named node resolved to, once ResolvePackage finishes. Keys
	// are NodeIDs assigned by the parser; values are the resolver's
	// symbol table entries. Together with File these let downstream
	// passes (type checker, code generator) traverse the AST with name
	// resolution already applied.
	RefsByID     map[ast.NodeID]*Symbol
	TypeRefsByID map[ast.NodeID]*Symbol
	// RefIdents and TypeRefIdents enumerate the node references the
	// resolver observed, in no particular order. Callers that need to
	// walk resolved identifiers iterate these (not the maps) so the
	// pattern maps cleanly onto List<&Ident> / List<&NamedType> when
	// the pass is ported to the self-hosted compiler.
	RefIdents     []*ast.Ident
	TypeRefIdents []*ast.NamedType
}

func (pf *PackageFile) CheckerSource() []byte {
	if pf == nil {
		return nil
	}
	if len(pf.CanonicalSource) > 0 {
		return pf.CanonicalSource
	}
	return pf.Source
}

// CanMaterializeFile reports whether EnsureFile can produce the public AST
// compatibility surface without stitching package source into a synthetic
// single file. Native-loaded packages carry Run instead of File until a legacy
// boundary explicitly asks for it.
func (pf *PackageFile) CanMaterializeFile() bool {
	return pf != nil && (pf.File != nil || pf.Run != nil)
}

// EnsureFile materializes the *ast.File for this PackageFile. Packages loaded
// via LoadPackageForNative populate Run but leave File nil; calling EnsureFile
// uses the explicit public-AST compatibility adapter on demand. Returns the
// File, or nil if neither File nor Run is set.
func (pf *PackageFile) EnsureFile() *ast.File {
	if pf == nil {
		return nil
	}
	if pf.File != nil {
		return pf.File
	}
	if pf.Run != nil {
		pf.File = selfhost.LowerPublicFileFromRun(pf.Run)
	}
	return pf.File
}

// MaterializePublicCompatibility forces File materialization on every
// PackageFile in pkg through the explicit public-AST compatibility adapter.
// Call this before a legacy code path that still reads pf.File directly.
func (pkg *Package) MaterializePublicCompatibility() {
	if pkg == nil {
		return
	}
	for _, pf := range pkg.Files {
		pf.EnsureFile()
	}
}

// MaterializeCanonicalSources populates pf.CanonicalSource /
// pf.CanonicalMap on every file in pkg when either is missing and a
// lowered *ast.File is available. Needed after arena-first loading
// (LoadPackageForNative → MaterializePublicCompatibility) so consumers that project
// canonical-source spans (native checker bridge, seedgen, LSP query
// engine) see the same shape LoadPackage's eager loader produces.
//
// Idempotent: files that already carry CanonicalSource are skipped.
// Safe to call before or after ResolvePackage / ResolveAll.
func (pkg *Package) MaterializeCanonicalSources() {
	if pkg == nil {
		return
	}
	for _, pf := range pkg.Files {
		if pf == nil || pf.File == nil || len(pf.CanonicalSource) > 0 {
			continue
		}
		pf.CanonicalSource, pf.CanonicalMap = canonical.SourceWithMap(pf.Source, pf.File)
	}
}

// PackageResult is returned by ResolvePackage. It contains one
// FileScope per input file (via pkg.Files[i].FileScope) plus the shared
// package scope and a merged diagnostic list.
type PackageResult struct {
	// PackageScope is the shared top-level scope for the package. All
	// non-`use` top-level declarations live here regardless of which
	// file they were parsed from.
	PackageScope *Scope
	// Diags is every diagnostic produced by the parser (across all
	// files) and the resolver, in a deterministic order.
	Diags []*diag.Diagnostic
}
