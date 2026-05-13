package airepair

import (
	"strings"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/repair"
	"github.com/osty/osty/internal/runner"
	"github.com/osty/osty/internal/token"
)

func diagnosticForeignLoopSource(src []byte, diags []*diag.Diagnostic) repair.Result {
	if !wantsForeignLoopRepair(src, diags) {
		return repair.Result{Source: src}
	}
	out, changes, ok := rewriteForeignLoopHeaders(src)
	if !ok || len(changes) == 0 {
		return repair.Result{Source: src}
	}
	return repair.Result{
		Source:  out,
		Changes: changes,
	}
}

func wantsForeignLoopRepair(src []byte, diags []*diag.Diagnostic) bool {
	return runner.SrcWantsForeignLoopRepair(src) && diagsHaveError(diags)
}

func rewriteForeignLoopHeaders(src []byte) ([]byte, []repair.Change, bool) {
	lines := runner.SplitSourceLines(src)
	if len(lines) == 0 {
		return src, nil, false
	}

	var (
		out     strings.Builder
		changes []repair.Change
		changed bool
	)

	for _, line := range lines {
		if line.Trimmed == "" || runner.IsIgnorablePythonLine(line.Trimmed) {
			out.WriteString(line.Raw)
			continue
		}

		r := runner.RewriteForeignLoopHeader(line.Trimmed)
		if !r.Ok {
			out.WriteString(line.Raw)
			continue
		}

		out.WriteString(line.Indent)
		out.WriteString(r.Rewritten)
		if line.HasNewline {
			out.WriteByte('\n')
		}
		changes = append(changes, repair.Change{
			Kind:    r.Kind,
			Message: r.Message,
			Pos: token.Pos{
				Offset: line.Start + len(line.Indent),
				Line:   line.LineNo,
				Column: len([]rune(line.Indent)) + 1,
			},
		})
		changed = true
	}

	if !changed {
		return src, nil, false
	}
	return []byte(out.String()), changes, true
}

