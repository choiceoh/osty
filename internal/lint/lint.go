// Package lint is a thin Go adapter over toolchain/lint.osty. Every
// rule is emitted by the self-hosted pass; this package just dispatches
// to it via mergeSelfhostLint, applies #[allow(...)] suppression, and
// stamps file paths on the resulting diagnostics. Warnings never block
// compilation; `osty lint --strict` flips them into a non-zero exit.
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

// File runs the self-hosted lint pass over one resolved source file
// and returns the merged diagnostic list. rr / chk are accepted for
// API stability; the adapter no longer reads them. The pass is
// read-only over every input.
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
