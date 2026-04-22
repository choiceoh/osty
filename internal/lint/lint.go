// Package lint implements style and correctness lint rules over a
// resolved Osty source tree.
//
// The pass runs after `resolve` and consumes its output (Refs, TypeRefs,
// FileScope) read-only. Every diagnostic produced by lint is emitted at
// diag.Warning severity with a stable `Lxxxx` code; lint warnings never
// block compilation. The CLI surfaces them via `osty lint`, with
// `--strict` flipping warnings into a non-zero exit for CI use.
//
// Currently implemented rules:
//
//	L0001  unused `let` binding
//	L0002  unused function / closure parameter
//	L0003  unused `use` alias (file-local)
//	L0010  inner `let` shadows an outer binding
//	L0020  statement after a terminating return / break / continue
//	L0030  type name not UpperCamelCase
//	L0031  fn / let / param name not lowerCamelCase
//	L0032  enum variant name not UpperCamelCase
//
// Additional rules (e.g. unreachable via Never, dead match arms, magic
// numbers) belong here once the underlying analyses (type checker,
// exhaustiveness) land.
package lint

import (
	"fmt"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/token"
)

// Result is the output of one lint pass. Diags is always Warning severity.
type Result struct {
	Diags []*diag.Diagnostic
}

// File runs every implemented lint rule against one resolved source file.
//
// rr must be the resolver's output for f. chk is optional — when non-nil,
// rules that need type information (Never-call dead code, ignored
// Result/Option) are enabled. The lint pass is read-only over all inputs:
// it never mutates the AST, symbol tables, or checker state.
func File(f *ast.File, rr *resolve.Result, chk *check.Result) *Result {
	if f == nil {
		return &Result{}
	}
	l := &linter{
		file:     f,
		resolved: rr,
		check:    chk,
		used:     buildUsedSet(rr),
		result:   &Result{},
	}
	l.buildMemberAccessSets()
	l.lintUnused()
	l.lintUnusedMut()
	l.lintUnusedMember()
	l.lintShadowing()
	l.lintDeadCode()
	l.extendDeadCodeWithTypeInfo()
	l.lintFlow()
	l.lintIgnoredResult()
	l.lintNaming()
	l.lintSimplify()
	l.lintComplexity()
	l.lintDocs()
	l.filterSuppressed()
	return l.result
}

// Package runs lint over every file in pkg as one analysis unit.
//
// File-by-file linting would over-report unused imports (an alias
// declared in file A but used in file B looks unused locally) and unused
// struct members. This entry point builds a package-wide "used" /
// member-access set and shares it across files, then runs the per-file
// rules.
//
// chk is the type-checker output for the whole package — a single
// shared Result covering every file. May be nil for a parse+resolve-only
// pipeline; type-info-dependent rules are skipped in that case.
func Package(pkg *resolve.Package, pr *resolve.PackageResult, chk *check.Result) *Result {
	if pkg == nil {
		return &Result{}
	}
	// Build the union of every file's resolved references first so that
	// cross-file usage of `use` aliases and top-level decls is visible to
	// each per-file lint pass.
	used := map[*resolve.Symbol]bool{}
	for _, pf := range pkg.Files {
		for _, sym := range pf.Refs {
			used[sym] = true
		}
		for _, sym := range pf.TypeRefs {
			used[sym] = true
		}
	}

	// Union every file's AST-side "names used as fields / methods" so a
	// private field read from a sibling file doesn't look unused.
	pkgMembers := &memberAccess{
		fields:  map[string]bool{},
		methods: map[string]bool{},
	}
	for _, pf := range pkg.Files {
		if pf == nil || pf.File == nil {
			continue
		}
		collectMemberAccess(pf.File, pkgMembers)
	}

	res := &Result{}
	for _, pf := range pkg.Files {
		if pf == nil || pf.File == nil {
			continue
		}
		rr := &resolve.Result{
			Refs:         pf.Refs,
			TypeRefs:     pf.TypeRefs,
			RefsByID:     pf.RefsByID,
			TypeRefsByID: pf.TypeRefsByID,
			FileScope:    pf.FileScope,
		}
		// Each file gets its own local Result so that filterSuppressed
		// only considers this file's #[allow(...)] regions against this
		// file's diagnostics — cross-file suppression via coincidental
		// line numbers is impossible by construction.
		local := &Result{}
		l := &linter{
			file:     pf.File,
			resolved: rr,
			check:    chk, // shared across the package
			used:     used,
			members:  pkgMembers,
			result:   local,
		}
		l.lintUnused()
		l.lintUnusedMut()
		l.lintUnusedMember()
		l.lintShadowing()
		l.lintDeadCode()
		l.extendDeadCodeWithTypeInfo()
		l.lintIgnoredResult()
		l.lintNaming()
		l.lintSimplify()
		l.filterSuppressed()
		diag.StampFile(local.Diags, pf.Path)
		res.Diags = append(res.Diags, local.Diags...)
	}
	return res
}

// linter is the working state for one file's lint pass.
type linter struct {
	file     *ast.File
	resolved *resolve.Result
	// check is the type-checker's result. May be nil for lightweight
	// modes (parse+resolve+lint); rules that need type info check for
	// this and no-op.
	check *check.Result
	// used is the "this symbol was referenced at least once" set, built
	// from the resolver's Refs + TypeRefs maps. In package mode it is
	// shared across all files in the package.
	used map[*resolve.Symbol]bool
	// members records every name that appears as a field or method
	// access anywhere in the current linting scope (file in File mode,
	// package in Package mode). See members.go for the type and its
	// collector.
	members *memberAccess
	result  *Result
}

// buildUsedSet collects every symbol that any reference in rr points at.
func buildUsedSet(rr *resolve.Result) map[*resolve.Symbol]bool {
	used := map[*resolve.Symbol]bool{}
	if rr == nil {
		return used
	}
	for _, sym := range rr.Refs {
		used[sym] = true
	}
	for _, sym := range rr.TypeRefs {
		used[sym] = true
	}
	return used
}

// ---- Diagnostic helpers ----

func (l *linter) emit(d *diag.Diagnostic) {
	l.result.Diags = append(l.result.Diags, d)
}

func (l *linter) warnSpan(start, end token.Pos, code, format string, args ...any) {
	l.emit(diag.New(diag.Warning, fmt.Sprintf(format, args...)).
		Code(code).
		Primary(diag.Span{Start: start, End: end}, "").
		Build())
}

func (l *linter) warnNode(n ast.Node, code, format string, args ...any) {
	l.warnSpan(n.Pos(), n.End(), code, format, args...)
}
