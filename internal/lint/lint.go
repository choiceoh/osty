// Package lint emits lint warnings (`Lxxxx` codes) at diag.Warning
// severity; lint warnings never block compilation. The CLI surfaces
// them via `osty lint`, with `--strict` flipping warnings into a
// non-zero exit for CI use.
//
// Nearly every rule runs in toolchain/lint.osty. This package is now a
// thin Go adapter: it dispatches to the self-hosted pass via
// mergeSelfhostLint, applies #[allow(...)] suppression, stamps file
// paths on diagnostics, and implements a single Go-only rule:
//
//	L0060  missing doc comment
//
// L0060 stays Go-side while Osty grows a doc-comment inspection
// surface; when the Osty version lands this package can collapse to a
// pure adapter.
package lint

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/selfhost"
)

// Result is the output of one lint pass. Diags is always Warning severity.
type Result struct {
	Diags []*diag.Diagnostic
}

// File runs the Go-side L0060 doc rule plus the self-hosted lint pass
// over one resolved source file and returns the merged diagnostic
// list. rr is the resolver's output for f; chk is optional. The pass
// is read-only over every input.
func File(f *ast.File, src []byte, rr *resolve.Result, chk *check.Result) *Result {
	if f == nil {
		return &Result{}
	}
	_ = rr
	_ = chk
	l := &linter{
		file:   f,
		result: &Result{},
	}
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
// Each file gets its own local Result so filterSuppressed only matches
// #[allow(...)] regions against that file's diagnostics.
func Package(pkg *resolve.Package, pr *resolve.PackageResult, chk *check.Result) *Result {
	_ = pr
	_ = chk
	if pkg == nil {
		return &Result{}
	}
	res := &Result{}
	for _, pf := range pkg.Files {
		if pf == nil || pf.File == nil {
			continue
		}
		local := &Result{}
		l := &linter{
			file:   pf.File,
			result: local,
		}
		l.filterSuppressed()
		mergeSelfhostLint(local, pf.Source)
		diag.StampFile(local.Diags, pf.Path)
		res.Diags = append(res.Diags, local.Diags...)
	}
	return res
}

// linter is the working state for one file's lint pass.
type linter struct {
	file   *ast.File
	result *Result
}

func (l *linter) emit(d *diag.Diagnostic) {
	l.result.Diags = append(l.result.Diags, d)
}
