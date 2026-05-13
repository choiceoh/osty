package airepair

import (
	"bytes"
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
	if !bytes.Contains(src, []byte("for ")) {
		return false
	}
	if !bytes.Contains(src, []byte(" of ")) && !bytes.Contains(src, []byte("range(")) && !bytes.Contains(src, []byte("enumerate(")) {
		return false
	}
	for _, d := range diags {
		if d != nil && d.Severity == diag.Error {
			return true
		}
	}
	return false
}

func rewriteForeignLoopHeaders(src []byte) ([]byte, []repair.Change, bool) {
	lines := splitSourceLines(src)
	if len(lines) == 0 {
		return src, nil, false
	}

	var (
		out     strings.Builder
		changes []repair.Change
		changed bool
	)

	for _, line := range lines {
		if line.trimmed == "" || isIgnorablePythonLine(line.trimmed) {
			out.WriteString(line.raw)
			continue
		}

		r := runner.RewriteForeignLoopHeader(line.trimmed)
		if !r.Ok {
			out.WriteString(line.raw)
			continue
		}

		out.WriteString(line.indent)
		out.WriteString(r.Rewritten)
		if line.hasNewline {
			out.WriteByte('\n')
		}
		changes = append(changes, repair.Change{
			Kind:    r.Kind,
			Message: r.Message,
			Pos: token.Pos{
				Offset: line.start + len(line.indent),
				Line:   line.lineNo,
				Column: len([]rune(line.indent)) + 1,
			},
		})
		changed = true
	}

	if !changed {
		return src, nil, false
	}
	return []byte(out.String()), changes, true
}

