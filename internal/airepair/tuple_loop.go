package airepair

import (
	"bytes"
	"strings"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/repair"
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
	if !bytes.Contains(src, []byte("for ")) || !bytes.Contains(src, []byte(",")) || !bytes.Contains(src, []byte(" in ")) {
		return false
	}
	for _, d := range diags {
		if d != nil && d.Severity == diag.Error {
			return true
		}
	}
	return false
}

func rewriteBareTupleForHeaders(src []byte) ([]byte, []repair.Change, bool) {
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

		rewritten, ok := rewriteBareTupleForHeader(line.trimmed)
		if !ok {
			out.WriteString(line.raw)
			continue
		}

		out.WriteString(line.indent)
		out.WriteString(rewritten)
		if line.hasNewline {
			out.WriteByte('\n')
		}
		changes = append(changes, repair.Change{
			Kind:    "tuple_loop_pattern",
			Message: "wrap a bare tuple loop binding in Osty tuple-pattern syntax",
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

	normalized, ok := normalizeTupleLoopBindings(lhs)
	if !ok {
		return "", false
	}

	return "for (" + normalized + ") in " + rhs + " {", true
}
