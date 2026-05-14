// codesdoc_policy.go is the Go snapshot of
// toolchain/codesdoc_policy.osty. Osty is the source of truth;
// the drift test in this package keeps the two in sync.
package runner

import "strings"

// DefaultHeadingFor invents a phase heading when the const block
// has no leading doc comment. Mapping uses the first byte:
// E → Errors, W → Warnings, L → Lint, else Miscellaneous.
//
// Osty: toolchain/codesdoc_policy.osty:32
func DefaultHeadingFor(code string) string {
	switch {
	case strings.HasPrefix(code, "E"):
		return "Errors starting at " + code
	case strings.HasPrefix(code, "W"):
		return "Warnings starting at " + code
	case strings.HasPrefix(code, "L"):
		return "Lint warnings starting at " + code
	}
	return "Miscellaneous"
}

// StripRangeSuffix removes the ` (Exxxx-Eyyyy)` (or ` (Exxxx)`)
// suffix that rangeSuffix appends to phase headings for markdown
// rendering. Used before mapping a heading to its
// DiagnosticFamily variant.
//
// Osty: toolchain/codesdoc_policy.osty:54
func StripRangeSuffix(h string) string {
	if i := strings.LastIndex(h, " ("); i >= 0 {
		return strings.TrimSpace(h[:i])
	}
	return strings.TrimSpace(h)
}

// UnsafeForBootstrapGen guards against Example blocks that the
// bootstrap-gen lexer mishandles. Fires when both a `{`/`}` byte
// and a `=>` token are present.
//
// Osty: toolchain/codesdoc_policy.osty:71
func UnsafeForBootstrapGen(example string) bool {
	hasBrace := strings.Contains(example, "{") || strings.Contains(example, "}")
	hasFatArrow := strings.Contains(example, "=>")
	return hasBrace && hasFatArrow
}

// ProseExample reports whether an Example block contains prose
// placeholders (`...`, `…`, `→`) that make it unrunnable as real
// Osty source.
//
// Osty: toolchain/codesdoc_policy.osty:83
func ProseExample(example string) bool {
	return strings.Contains(example, "...") ||
		strings.Contains(example, "…") ||
		strings.Contains(example, "→")
}

// OstyEscape escapes `s` so it's safe to splice inside an Osty
// regular-string literal. Escape set: backslash, double quote,
// LF, CR, tab, `{`, `}`. All other bytes pass through.
//
// Osty: toolchain/codesdoc_policy.osty:101
func OstyEscape(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 8)
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '{':
			b.WriteString(`\{`)
		case '}':
			b.WriteString(`\}`)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
