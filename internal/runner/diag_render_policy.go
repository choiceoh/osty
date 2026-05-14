// diag_render_policy.go is the Go snapshot of
// toolchain/diag_render.osty. Osty is the source of truth;
// the drift test in this package keeps the two in sync.
package runner

import (
	"fmt"
	"strconv"
	"strings"
)

// LineBoundsResult mirrors toolchain/diag_render.osty's
// LineBoundsResult. Start/End are -1 when the requested line is
// out of range; callers test `Start < 0` to detect the miss.
//
// Osty: toolchain/diag_render.osty:19
type LineBoundsResult struct {
	Start int
	End   int
}

// ColToRuneIndex converts a 1-based column (counted in Unicode
// scalar values) to a 0-based index into a rune sequence of
// `runeCount` elements. Columns <= 1 map to 0; columns past the
// end clamp to runeCount.
//
// Osty: toolchain/diag_render.osty:29
func ColToRuneIndex(runeCount, col int) int {
	if col <= 1 {
		return 0
	}
	idx := col - 1
	if idx > runeCount {
		idx = runeCount
	}
	return idx
}

// DigitWidth returns the number of decimal digits needed to
// render `n`. `n < 10` returns 1 (covers 0 and any small
// negative); otherwise count by repeated division.
//
// Osty: toolchain/diag_render.osty:45
func DigitWidth(n int) int {
	if n < 10 {
		return 1
	}
	w := 0
	for n > 0 {
		w++
		n /= 10
	}
	return w
}

// PadInt renders `n` and left-pads it with spaces to `width`
// columns. When the rendered number already has at least `width`
// characters the unpadded form is returned.
//
// Osty: toolchain/diag_render.osty:62
func PadInt(n, width int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) >= width {
		return s
	}
	return strings.Repeat(" ", width-len(s)) + s
}

// LineBounds returns the [start, end) byte offsets of the 1-based
// `line` in `src`, exclusive of the trailing newline. Returns
// (-1, -1) when line <= 0 or the source has fewer than `line`
// lines. The final line is reported even when it has no trailing
// newline.
//
// Osty: toolchain/diag_render.osty:78
func LineBounds(src []byte, line int) LineBoundsResult {
	if line <= 0 {
		return LineBoundsResult{Start: -1, End: -1}
	}
	cur := 1
	start := 0
	for i := 0; i < len(src); i++ {
		if cur == line && src[i] == '\n' {
			return LineBoundsResult{Start: start, End: i}
		}
		if src[i] == '\n' {
			cur++
			start = i + 1
		}
	}
	if cur == line {
		return LineBoundsResult{Start: start, End: len(src)}
	}
	return LineBoundsResult{Start: -1, End: -1}
}

// DiagRenderReplacement resolves a Suggestion's replacement
// template into the human-readable text shown after the `→`
// marker. The host computes whether the CopyFrom span fits the
// source (haveExcerpt) and extracts the bytes (excerpt); this
// helper decides:
//
//   - the fallback placeholder ("<expr>") when the excerpt is
//     unavailable,
//   - the precedence between literal replacements and the `%s`
//     template form (empty replacement OR no `%s` → just emit
//     the excerpt/fallback),
//   - first-occurrence substitution `%s` → excerpt (matches
//     Go's `strings.Replace(.., 1)`).
//
// Osty: toolchain/diag_render.osty:120
func DiagRenderReplacement(replacement, excerpt string, haveExcerpt bool) string {
	body := excerpt
	if !haveExcerpt {
		body = "<expr>"
	}
	if replacement == "" {
		return body
	}
	idx := strings.Index(replacement, "%s")
	if idx < 0 {
		return body
	}
	return replacement[:idx] + body + replacement[idx+2:]
}

// DiagSeverity enum constants. Order matches
// toolchain/diag_render.osty + internal/diag.Severity iota.
// The diag package guards against drift via a compile-time
// assertion in init() — see internal/diag/diag.go.
const (
	DiagSeverityError   int = 0
	DiagSeverityWarning int = 1
	DiagSeverityNote    int = 2
)

// DiagSeverityString maps a Severity int back to its
// human-readable label. Unknown values collapse to "unknown".
//
// Osty: toolchain/diag_render.osty:148
func DiagSeverityString(sev int) string {
	switch sev {
	case DiagSeverityError:
		return "error"
	case DiagSeverityWarning:
		return "warning"
	case DiagSeverityNote:
		return "note"
	}
	return "unknown"
}

// DiagShortError renders the compact (*Diagnostic).Error() form:
// `<code>: <severity> at <line>:<col>: <message>` when code is
// set, otherwise the prefix is dropped.
//
// Hot path — (*Diagnostic).Error() is called from any code that
// treats a diagnostic as an `error`, including logging. Uses a
// single strings.Builder to avoid the intermediate string
// allocations a `+` chain would produce.
//
// Osty: toolchain/diag_render.osty:164
func DiagShortError(code string, severity, line, column int, message string) string {
	var b strings.Builder
	if code != "" {
		b.Grow(len(code) + len(message) + 24)
		b.WriteString(code)
		b.WriteString(": ")
	} else {
		b.Grow(len(message) + 24)
	}
	b.WriteString(DiagSeverityString(severity))
	b.WriteString(" at ")
	b.WriteString(strconv.Itoa(line))
	b.WriteByte(':')
	b.WriteString(strconv.Itoa(column))
	b.WriteString(": ")
	b.WriteString(message)
	return b.String()
}

// DiagSeverityAnsiColor maps a Severity int to its terminal ANSI
// escape sequence — the lead-in code only; callers append the
// reset themselves. Returns "" for unknown severities.
//
// Osty: toolchain/diag_render.osty:176
func DiagSeverityAnsiColor(sev int) string {
	switch sev {
	case DiagSeverityError:
		return "\x1b[31m"
	case DiagSeverityWarning:
		return "\x1b[33m"
	case DiagSeverityNote:
		return "\x1b[36m"
	}
	return ""
}

// DiagSuggestionDisplay packages the three labels the formatter
// emits per Suggestion: the inline tag, the human label, and
// the rendered replacement.
//
// Osty: toolchain/diag_render.osty:193
type DiagSuggestionDisplay struct {
	Tag         string
	Label       string
	Replacement string
}

// DiagSuggestionDisplayOf resolves a Suggestion's display
// strings (see Osty doc for the table). Named `-Of` so the
// default capitalization rule doesn't collide with the
// DiagSuggestionDisplay struct in the drift table.
//
// Osty: toolchain/diag_render.osty:210
func DiagSuggestionDisplayOf(userLabel string, machineApplicable bool, rendered string) DiagSuggestionDisplay {
	tag := "suggest"
	if machineApplicable {
		tag = "fix"
	}
	label := userLabel
	if label == "" {
		label = "suggested fix"
	}
	replacement := rendered
	if replacement == "" {
		replacement = "(delete)"
	}
	return DiagSuggestionDisplay{Tag: tag, Label: label, Replacement: replacement}
}

// DiagNoteLabel returns the literal "note" used in the formatter's
// `= note: <text>` line.
//
// Osty: toolchain/diag_render.osty:234
func DiagNoteLabel() string { return "note" }

// DiagHelpLabel returns the literal "help" used in the formatter's
// `= help: <hint>` line.
//
// Osty: toolchain/diag_render.osty:239
func DiagHelpLabel() string { return "help" }
