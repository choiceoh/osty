// Package lint implements style and correctness lint rules over a
// resolved Osty source tree.
//
// The pass runs after `resolve` and consumes its output (Refs, TypeRefs,
// FileScope) read-only. Every diagnostic produced by lint is emitted at
// diag.Warning severity with a stable `Lxxxx` code; lint warnings never
// block compilation. The CLI surfaces them via `osty lint`, with
// `--strict` flipping warnings into a non-zero exit for CI use.
//
// Rules that still run on the Go side (either because they need the Go
// type checker or have no Osty counterpart yet):
//
//	L0005  unused struct field
//	L0006  unused method
//	L0007  ignored Result / Option (type-info driven)
//	L0025  identical if/else branches
//	L0040  redundant boolean expression
//	L0041  self-compare
//	L0042  self-assign
//	L0043  double negation
//	L0044  boolean literal compare
//	L0045  negated boolean literal
//	L0046  unnecessary Ok/Some wrap
//	L0047  let-then-return could be one expression
//	L0048  needless parens around condition
//	L0049  infinite loop literal
//	L0060  missing doc comment
//	L0070  missing test assertion
//
// The remaining rules (L0001–L0004, L0008, L0010, L0020–L0024, L0026,
// L0030–L0032, L0050, L0052, L0053) are emitted by toolchain/lint.osty
// and merged in by mergeSelfhostLint.
package lint

import (
	"fmt"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/selfhost"
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
// Result/Option) are enabled. src carries the original source bytes; the
// self-hosted linter runs over them in parallel and its findings are merged
// into the returned result, so selfhost-authored rules (toolchain/lint.osty)
// are part of the CLI output. The lint pass is read-only over all inputs: it
// never mutates the AST, symbol tables, or checker state.
func File(f *ast.File, src []byte, rr *resolve.Result, chk *check.Result) *Result {
	if f == nil {
		return &Result{}
	}
	l := &linter{
		file:     f,
		resolved: rr,
		check:    chk,
		result:   &Result{},
	}
	l.buildMemberAccessSets()
	l.lintUnusedMember()
	l.lintIgnoredResult()
	l.lintSimplify()
	l.lintDocs()
	l.filterSuppressed()
	mergeSelfhostLint(l.result, src)
	return l.result
}

// mergeSelfhostLint appends diagnostics from the Osty-authored lint pass
// (toolchain/lint.osty) that the Go pass did not already emit at the same
// code + position. Dedupe is by (Code, start offset) because the two
// implementations occasionally phrase the same finding differently.
func mergeSelfhostLint(result *Result, src []byte) {
	if result == nil || len(src) == 0 {
		return
	}
	seen := map[selfhostLintKey]bool{}
	for _, d := range result.Diags {
		seen[selfhostLintKeyOf(d)] = true
	}
	for _, d := range selfhost.LintDiagnostics(src) {
		if d == nil {
			continue
		}
		k := selfhostLintKeyOf(d)
		if seen[k] {
			continue
		}
		seen[k] = true
		result.Diags = append(result.Diags, d)
	}
}

type selfhostLintKey struct {
	code  string
	start int
}

func selfhostLintKeyOf(d *diag.Diagnostic) selfhostLintKey {
	if d == nil {
		return selfhostLintKey{}
	}
	key := selfhostLintKey{code: d.Code}
	if len(d.Spans) > 0 {
		key.start = d.Spans[0].Span.Start.Offset
	}
	return key
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
			RefsByID:      pf.RefsByID,
			TypeRefsByID:  pf.TypeRefsByID,
			RefIdents:     pf.RefIdents,
			TypeRefIdents: pf.TypeRefIdents,
			FileScope:     pf.FileScope,
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
			members:  pkgMembers,
			result:   local,
		}
		l.lintUnusedMember()
		l.lintIgnoredResult()
		l.lintSimplify()
		l.filterSuppressed()
		mergeSelfhostLint(local, pf.Source)
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
	// members records every name that appears as a field or method
	// access anywhere in the current linting scope (file in File mode,
	// package in Package mode). See members.go for the type and its
	// collector.
	members *memberAccess
	result  *Result
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
