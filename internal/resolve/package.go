package resolve

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
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
	// the parsed AST after parser-owned normalization/lowering. When empty,
	// callers should fall back to Source.
	CanonicalSource []byte
	// File is the parsed AST. Never nil, even when Parse reported errors;
	// best-effort partial trees support multi-error reporting.
	File *ast.File
	// ParseDiags are diagnostics produced during lex + parse of this
	// file. They are merged with resolver diagnostics by the package
	// walker.
	ParseDiags []*diag.Diagnostic
	// ParseProvenance records parser-owned stable alias absorption and AST
	// lowerings that were applied before resolve/check.
	ParseProvenance *parser.Provenance
	// FileScope is the file-local scope (contains this file's `use`
	// aliases). Populated by ResolvePackage. Its parent is the package
	// scope; the resolver walks per-file expression bodies rooted here.
	FileScope *Scope
	// Refs and TypeRefs record where each identifier / type-named node
	// resolved to, once ResolvePackage finishes. Together with File they
	// let downstream passes (type checker, code generator) traverse the
	// AST with name resolution already applied.
	Refs     map[*ast.Ident]*Symbol
	TypeRefs map[*ast.NamedType]*Symbol
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
