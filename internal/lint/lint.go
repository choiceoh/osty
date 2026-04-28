// Package lint emits lint warnings (`Lxxxx` codes) at diag.Warning
// severity; lint warnings never block compilation. The CLI surfaces
// them via `osty lint`, with `--strict` flipping warnings into a
// non-zero exit for CI use.
//
// The rules run in toolchain/lint.osty. This package is a thin Go adapter:
// it dispatches to the self-hosted pass, applies project-level lint config,
// and stamps file paths on package diagnostics.
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

// File runs the self-hosted lint pass over one resolved source file. rr and
// chk are retained in the signature for callers that already have a complete
// front-end result; the lint rules read the source through selfhost.
func File(f *ast.File, src []byte, rr *resolve.Result, chk *check.Result) *Result {
	if f == nil {
		return &Result{}
	}
	_ = rr
	_ = chk
	result := &Result{}
	mergeSelfhostLint(result, src)
	return result
}

// mergeSelfhostLint appends diagnostics from the Osty-authored lint pass
// (toolchain/lint.osty). #[allow(...)] suppression is handled inside the
// self-hosted pass; project-level allow/deny config is applied by Config.Apply.
func mergeSelfhostLint(result *Result, src []byte) {
	if result == nil || len(src) == 0 {
		return
	}
	for _, d := range selfhost.LintDiagnostics(src) {
		if d == nil {
			continue
		}
		result.Diags = append(result.Diags, d)
	}
}

// Package runs lint over every file in pkg as one analysis unit.
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
		mergeSelfhostLint(local, pf.Source)
		diag.StampFile(local.Diags, pf.Path)
		res.Diags = append(res.Diags, local.Diags...)
	}
	return res
}
