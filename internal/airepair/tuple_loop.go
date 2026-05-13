package airepair

import (
	"strings"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/repair"
	"github.com/osty/osty/internal/runner"
	"github.com/osty/osty/internal/token"
)

func diagnosticTupleLoopSource(src []byte, diags []*diag.Diagnostic) repair.Result {
	if !wantsTupleLoopRepair(src, diags) {
		return repair.Result{Source: src}
	}
	out, changes, ok := rewriteBareTupleForHeaders(src)
	if !ok || len(changes) == 0 {
		return repair.Result{Source: src}
	}
	return repair.Result{
		Source:  out,
		Changes: changes,
	}
}

func wantsTupleLoopRepair(src []byte, diags []*diag.Diagnostic) bool {
	return runner.SrcWantsTupleLoopRepair(src) && diagsHaveError(diags)
}

func rewriteBareTupleForHeaders(src []byte) ([]byte, []repair.Change, bool) {
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

		rewritten, ok := rewriteBareTupleForHeader(line.Trimmed)
		if !ok {
			out.WriteString(line.Raw)
			continue
		}

		out.WriteString(line.Indent)
		out.WriteString(rewritten)
		if line.HasNewline {
			out.WriteByte('\n')
		}
		changes = append(changes, repair.Change{
			Kind:    "tuple_loop_pattern",
			Message: "wrap a bare tuple loop binding in Osty tuple-pattern syntax",
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

func rewriteBareTupleForHeader(trimmed string) (string, bool) {
	if !strings.HasPrefix(trimmed, "for ") || !strings.HasSuffix(trimmed, "{") {
		return "", false
	}
	body := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "for "), "{"))
	inIdx := strings.Index(body, " in ")
	if inIdx <= 0 {
		return "", false
	}
	lhs := strings.TrimSpace(body[:inIdx])
	rhs := strings.TrimSpace(body[inIdx+4:])
	if lhs == "" || rhs == "" || strings.HasPrefix(lhs, "(") || !strings.Contains(lhs, ",") {
		return "", false
	}

	normalized := runner.NormalizeTupleLoopBindings(lhs)
	if !normalized.Ok {
		return "", false
	}

	return "for (" + normalized.Joined + ") in " + rhs + " {", true
}
