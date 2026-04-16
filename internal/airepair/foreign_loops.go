package airepair

import (
	"bytes"
	"strings"
	"unicode"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/repair"
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

		rewritten, kind, msg, ok := rewriteForeignLoopHeader(line.trimmed)
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
			Kind:    kind,
			Message: msg,
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

func rewriteForeignLoopHeader(trimmed string) (string, string, string, bool) {
	if rewritten, ok := rewriteJSForOfHeader(trimmed); ok {
		return rewritten, "js_for_of_loop", "replace JS-style `for (... of ...)` loop with Osty `for ... in` syntax", true
	}
	if rewritten, ok := rewritePythonEnumerateLoopHeader(trimmed); ok {
		return rewritten, "python_enumerate_loop", "replace Python `enumerate(...)` loop with Osty `.enumerate()` iteration", true
	}
	if rewritten, ok := rewritePythonRangeLoopHeader(trimmed); ok {
		return rewritten, "python_range_loop", "replace Python `range(...)` loop with Osty range syntax", true
	}
	return "", "", "", false
}

func rewriteJSForOfHeader(trimmed string) (string, bool) {
	if !strings.HasPrefix(trimmed, "for ") || !strings.HasSuffix(trimmed, "{") {
		return "", false
	}
	body := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "for "), "{"))
	if !strings.HasPrefix(body, "(") || !strings.HasSuffix(body, ")") {
		return "", false
	}
	inner := strings.TrimSpace(body[1 : len(body)-1])
	ofIdx := strings.Index(inner, " of ")
	if ofIdx <= 0 {
		return "", false
	}

	lhs := strings.TrimSpace(inner[:ofIdx])
	rhs := strings.TrimSpace(inner[ofIdx+4:])
	switch {
	case strings.HasPrefix(lhs, "const "):
		lhs = strings.TrimSpace(strings.TrimPrefix(lhs, "const "))
	case strings.HasPrefix(lhs, "let "):
		lhs = strings.TrimSpace(strings.TrimPrefix(lhs, "let "))
	case strings.HasPrefix(lhs, "var "):
		lhs = strings.TrimSpace(strings.TrimPrefix(lhs, "var "))
	}
	if lhs == "" || rhs == "" {
		return "", false
	}

	if strings.HasPrefix(lhs, "[") && strings.HasSuffix(lhs, "]") {
		normalized, ok := normalizeTupleLoopBindings(strings.TrimSpace(lhs[1 : len(lhs)-1]))
		if !ok {
			return "", false
		}
		return "for (" + normalized + ") in " + rhs + " {", true
	}
	if !isSimpleIdentifierBinding(lhs) {
		return "", false
	}
	return "for " + lhs + " in " + rhs + " {", true
}

func rewritePythonRangeLoopHeader(trimmed string) (string, bool) {
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
	if !isSimpleIdentifierBinding(lhs) || !strings.HasPrefix(rhs, "range(") || !strings.HasSuffix(rhs, ")") {
		return "", false
	}

	args := splitTopLevelComma(strings.TrimSpace(rhs[len("range(") : len(rhs)-1]))
	var start, end string
	switch len(args) {
	case 1:
		start = "0"
		end = strings.TrimSpace(args[0])
	case 2:
		start = strings.TrimSpace(args[0])
		end = strings.TrimSpace(args[1])
	case 3:
		start = strings.TrimSpace(args[0])
		end = strings.TrimSpace(args[1])
		if strings.TrimSpace(args[2]) != "1" {
			return "", false
		}
	default:
		return "", false
	}
	if start == "" || end == "" {
		return "", false
	}
	return "for " + lhs + " in " + start + ".." + end + " {", true
}

func rewritePythonEnumerateLoopHeader(trimmed string) (string, bool) {
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
	if !strings.HasPrefix(rhs, "enumerate(") || !strings.HasSuffix(rhs, ")") {
		return "", false
	}
	iterable := strings.TrimSpace(rhs[len("enumerate(") : len(rhs)-1])
	if iterable == "" {
		return "", false
	}

	if strings.HasPrefix(lhs, "(") && strings.HasSuffix(lhs, ")") {
		normalized, ok := normalizeTupleLoopBindings(strings.TrimSpace(lhs[1 : len(lhs)-1]))
		if !ok {
			return "", false
		}
		return "for (" + normalized + ") in " + iterable + ".enumerate() {", true
	}

	normalized, ok := normalizeTupleLoopBindings(lhs)
	if !ok {
		return "", false
	}
	return "for (" + normalized + ") in " + iterable + ".enumerate() {", true
}

func splitTopLevelComma(src string) []string {
	if strings.TrimSpace(src) == "" {
		return nil
	}

	var (
		parts        []string
		start        int
		parenDepth   int
		bracketDepth int
		braceDepth   int
	)

	for i, r := range src {
		switch r {
		case '(':
			parenDepth++
		case ')':
			if parenDepth > 0 {
				parenDepth--
			}
		case '[':
			bracketDepth++
		case ']':
			if bracketDepth > 0 {
				bracketDepth--
			}
		case '{':
			braceDepth++
		case '}':
			if braceDepth > 0 {
				braceDepth--
			}
		case ',':
			if parenDepth == 0 && bracketDepth == 0 && braceDepth == 0 {
				parts = append(parts, src[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, src[start:])
	return parts
}

func normalizeTupleLoopBindings(src string) (string, bool) {
	parts := splitTopLevelComma(src)
	if len(parts) < 2 {
		return "", false
	}
	normalized := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if !isSimpleIdentifierBinding(part) {
			return "", false
		}
		normalized = append(normalized, part)
	}
	return strings.Join(normalized, ", "), true
}

func isSimpleIdentifierBinding(part string) bool {
	if part == "_" {
		return true
	}
	for i, r := range part {
		if i == 0 {
			if r != '_' && !unicode.IsLetter(r) {
				return false
			}
			continue
		}
		if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return part != ""
}
