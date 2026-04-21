package diag

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// Formatter renders Diagnostics with source-snippet caret underlines.
//
// Output format (Rust-inspired):
//
//	error[E0002]: expected `}`, got `else`
//	  --> auth.osty:7:5
//	   |
//	 5 |     let x = 1
//	   |         - previous declaration here
//	   ...
//	 7 |     else {
//	   |     ^^^^ expected `}` here
//	   |
//	   = note: the closing `}` of the `if` block was reached
//	   = help: place `else` on the same line as `}` (v0.2 O2)
//
// Color is enabled when Color is true; the ANSI escapes are still safe to
// pipe through `less -R`.
type Formatter struct {
	// Filename is shown in the location header when a diagnostic has no
	// File of its own. Multi-file renderers can leave this empty and
	// rely on d.File + Sources for accurate routing.
	Filename string
	// Source is the file's raw bytes; used to slice the line for the
	// snippet when the diagnostic has no File or File matches Filename.
	// May be nil — snippets will be omitted in that case.
	Source []byte
	// Sources maps filesystem paths to their raw bytes. When a
	// diagnostic carries d.File, the renderer uses this map to pull the
	// right file's source for the snippet. Empty/nil falls back to
	// Source + Filename, matching the single-file rendering path.
	Sources map[string][]byte
	// Color enables ANSI escape codes for severity, code, and caret.
	Color bool
}

// sourceFor returns the bytes the renderer should use when slicing
// snippets for d. When d.File is set and present in Sources the matching
// bytes are returned; otherwise Source is returned as-is. The second
// return value is the filename to display in the location header.
func (f *Formatter) sourceFor(d *Diagnostic) ([]byte, string) {
	if d != nil && d.File != "" {
		if f.Sources != nil {
			if src, ok := f.Sources[d.File]; ok {
				return src, d.File
			}
		}
		if d.File == f.Filename {
			return f.Source, f.Filename
		}
		// d.File is set but we don't have the source bytes — show the
		// header with the right path and skip the snippet rather than
		// quoting the wrong file's content.
		return nil, d.File
	}
	return f.Source, f.Filename
}

// Format renders a single diagnostic to a string.
func (f *Formatter) Format(d *Diagnostic) string {
	var b bytes.Buffer
	f.write(&b, d)
	return b.String()
}

// FormatAll renders multiple diagnostics, separated by blank lines. The
// diagnostics are first sorted by primary position so output is
// deterministic even when the caller emits them in internal-data order.
func (f *Formatter) FormatAll(ds []*Diagnostic) string {
	sorted := make([]*Diagnostic, len(ds))
	copy(sorted, ds)
	sort.SliceStable(sorted, func(i, j int) bool {
		pi, pj := sorted[i].PrimaryPos(), sorted[j].PrimaryPos()
		if pi.Line != pj.Line {
			return pi.Line < pj.Line
		}
		return pi.Column < pj.Column
	})
	var b bytes.Buffer
	for i, d := range sorted {
		if i > 0 {
			b.WriteByte('\n')
		}
		f.write(&b, d)
	}
	return b.String()
}

// ANSI helpers — color codes degrade silently when Color is false.
const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiBlue   = "\x1b[34m"
	ansiCyan   = "\x1b[36m"
)

func (f *Formatter) col(code, s string) string {
	if !f.Color {
		return s
	}
	return code + s + ansiReset
}

func (f *Formatter) sevColor(sev Severity) string {
	switch sev {
	case Error:
		return ansiRed
	case Warning:
		return ansiYellow
	case Note:
		return ansiCyan
	}
	return ""
}

func (f *Formatter) write(b *bytes.Buffer, d *Diagnostic) {
	// Headline.
	sev := f.col(ansiBold+f.sevColor(d.Severity), d.Severity.String())
	if d.Code != "" {
		sev += f.col(ansiBold, "["+d.Code+"]")
	}
	fmt.Fprintf(b, "%s: %s\n", sev, f.col(ansiBold, d.Message))

	// Location header — pulls file path and bytes via sourceFor so
	// multi-file renderers (pipeline, workspace) render each diagnostic
	// against its own file even when they share a single Formatter.
	pos := d.PrimaryPos()
	src, fname := f.sourceFor(d)
	if pos.Line > 0 {
		display := fname
		if display == "" {
			display = "<input>"
		}
		fmt.Fprintf(b, " %s %s:%d:%d\n",
			f.col(ansiBlue+ansiBold, "-->"),
			display, pos.Line, pos.Column)
	}

	// Source snippet with caret(s).
	f.writeSnippet(b, d, src)

	// Notes.
	for _, note := range d.Notes {
		fmt.Fprintf(b, " %s %s: %s\n",
			f.col(ansiBlue+ansiBold, "="),
			f.col(ansiCyan+ansiBold, "note"),
			note)
	}
	// Hint.
	if d.Hint != "" {
		fmt.Fprintf(b, " %s %s: %s\n",
			f.col(ansiBlue+ansiBold, "="),
			f.col(ansiCyan+ansiBold, "help"),
			d.Hint)
	}
	// Structured suggestions (auto-applicable patches).
	for _, sug := range d.Suggestions {
		label := sug.Label
		if label == "" {
			label = "suggested fix"
		}
		tag := "suggest"
		if sug.MachineApplicable {
			tag = "fix"
		}
		fmt.Fprintf(b, " %s %s: %s\n",
			f.col(ansiBlue+ansiBold, "="),
			f.col(ansiGreen+ansiBold, tag),
			label)
		// Show the proposed replacement on the next line. An empty
		// replacement is rendered as "(delete)" for clarity. Suggestions
		// with a CopyFrom span expand the "%s" placeholder using the
		// original source when available, so the user sees the concrete
		// rewrite rather than the raw template.
		repl := f.renderReplacement(sug)
		if repl == "" {
			repl = "(delete)"
		}
		fmt.Fprintf(b, "   %s %s\n",
			f.col(ansiGreen, "→"),
			repl)
	}
}

// renderReplacement resolves a Suggestion into the human-readable text
// shown after the `→` marker. For plain replacements this is just the
// literal string. For CopyFrom suggestions it expands the "%s" template
// using the source bytes covered by CopyFrom — falling back to a
// placeholder ("<expr>") when Source is unavailable or the span is
// out of range.
func (f *Formatter) renderReplacement(sug Suggestion) string {
	if sug.CopyFrom == nil {
		return sug.Replacement
	}
	var excerpt string
	cs := sug.CopyFrom.Start.Offset
	ce := sug.CopyFrom.End.Offset
	if len(f.Source) > 0 && cs >= 0 && ce >= cs && ce <= len(f.Source) {
		excerpt = string(f.Source[cs:ce])
	} else {
		excerpt = "<expr>"
	}
	if sug.Replacement == "" || !strings.Contains(sug.Replacement, "%s") {
		return excerpt
	}
	return strings.Replace(sug.Replacement, "%s", excerpt, 1)
}

// writeSnippet renders the source lines for every span in the diagnostic.
// The primary span uses severity color and `^`; secondary spans use blue
// and `-` and are labeled with their role. Spans sharing a line are
// merged onto a single caret line; spans on different lines render
// separate source lines.
//
// If the source isn't available, the snippet is omitted.
func (f *Formatter) writeSnippet(b *bytes.Buffer, d *Diagnostic, src []byte) {
	if len(d.Spans) == 0 || src == nil {
		return
	}

	// Gutter width is sized to the largest line number we'll render.
	maxLine := 0
	for _, s := range d.Spans {
		if s.Span.Start.Line > maxLine {
			maxLine = s.Span.Start.Line
		}
	}
	gutterW := digitWidth(maxLine)
	if gutterW < 2 {
		gutterW = 2
	}
	gutterPad := strings.Repeat(" ", gutterW)
	pipe := f.col(ansiBlue+ansiBold, "|")

	// Group spans by line so we can print a single source line with
	// one caret underline even if multiple spans land on it.
	byLine := map[int][]LabeledSpan{}
	var lines []int
	for _, s := range d.Spans {
		l := s.Span.Start.Line
		if l <= 0 {
			continue
		}
		if _, ok := byLine[l]; !ok {
			lines = append(lines, l)
		}
		byLine[l] = append(byLine[l], s)
	}
	sort.Ints(lines)
	if len(lines) == 0 {
		return
	}

	// Leading separator.
	fmt.Fprintf(b, " %s %s\n", gutterPad, pipe)

	for i, line := range lines {
		if i > 0 {
			// Visual separator between non-contiguous source lines.
			fmt.Fprintf(b, " %s %s\n", gutterPad, f.col(ansiBlue+ansiBold, "..."))
		}
		lineStart, lineEnd := lineBounds(src, line)
		if lineStart < 0 {
			continue
		}
		lineText := string(src[lineStart:lineEnd])
		lineNum := f.col(ansiBlue+ansiBold, padInt(line, gutterW))
		fmt.Fprintf(b, " %s %s %s\n", lineNum, pipe, lineText)

		f.writeCaretLine(b, gutterPad, pipe, lineText, byLine[line], d.Severity)
	}

	// Trailing separator before notes.
	if len(d.Notes) > 0 || d.Hint != "" {
		fmt.Fprintf(b, " %s %s\n", gutterPad, pipe)
	}
}

// writeCaretLine prints a single underline row for all spans that fell on
// the same source line. Primary spans get `^` in severity color; secondary
// spans get `-` in blue. The first span's label (primary preferred) is
// appended after the carets.
func (f *Formatter) writeCaretLine(
	b *bytes.Buffer,
	gutterPad, pipe, lineText string,
	spans []LabeledSpan,
	sev Severity,
) {
	// Column widths are Unicode code-point counts; convert the source
	// line to runes so padding matches what a terminal renders.
	runes := []rune(lineText)
	width := len(runes)
	row := make([]byte, width)
	for i := range row {
		row[i] = ' '
	}
	hasPrimary := false
	var primaryLabel, secondaryLabel string
	for _, s := range spans {
		startCol := colToRuneIndex(runes, s.Span.Start.Column)
		endCol := colToRuneIndex(runes, s.Span.End.Column)
		if endCol <= startCol {
			endCol = startCol + 1
		}
		if endCol > width {
			endCol = width
		}
		glyph := byte('-')
		if s.Primary {
			glyph = '^'
			hasPrimary = true
			if s.Label != "" && primaryLabel == "" {
				primaryLabel = s.Label
			}
		} else if s.Label != "" && secondaryLabel == "" {
			secondaryLabel = s.Label
		}
		for i := startCol; i < endCol; i++ {
			row[i] = glyph
		}
	}

	// Trim trailing spaces so the line doesn't carry whitespace for the
	// full viewport.
	row = bytes.TrimRight(row, " ")
	if len(row) == 0 {
		return
	}

	caretStr := string(row)
	// Colorize: primary carets get severity color; secondary dashes get
	// blue. If the row contains both, split into runs by glyph.
	colored := f.colorizeCaretRow(caretStr, sev, hasPrimary)
	label := primaryLabel
	if label == "" {
		label = secondaryLabel
	}
	if label != "" {
		colored += " " + label
	}
	fmt.Fprintf(b, " %s %s %s\n", gutterPad, pipe, colored)
}

// colorizeCaretRow colors contiguous runs of `^` and `-` distinctly. A
// row that contains both ends up with two color switches.
func (f *Formatter) colorizeCaretRow(row string, sev Severity, _ bool) string {
	if !f.Color || row == "" {
		return row
	}
	var out strings.Builder
	prevGlyph := byte(0)
	runStart := 0
	flush := func(end int) {
		seg := row[runStart:end]
		if seg == "" {
			return
		}
		switch prevGlyph {
		case '^':
			out.WriteString(ansiBold + f.sevColor(sev) + seg + ansiReset)
		case '-':
			out.WriteString(ansiBold + ansiBlue + seg + ansiReset)
		default:
			out.WriteString(seg)
		}
	}
	for i := 0; i < len(row); i++ {
		g := row[i]
		if g != prevGlyph {
			flush(i)
			prevGlyph = g
			runStart = i
		}
	}
	flush(len(row))
	return out.String()
}

// colToRuneIndex converts a 1-based column (counted in Unicode code
// points) to a 0-based index into the given rune slice.
func colToRuneIndex(runes []rune, col int) int {
	if col <= 1 {
		return 0
	}
	idx := col - 1
	if idx > len(runes) {
		idx = len(runes)
	}
	return idx
}

// lineBounds returns the [start, end) byte offsets of the given 1-based
// line in src, exclusive of the trailing newline. Returns (-1, -1) if
// out of range.
func lineBounds(src []byte, line int) (int, int) {
	if line <= 0 {
		return -1, -1
	}
	cur := 1
	start := 0
	for i := 0; i < len(src); i++ {
		if cur == line && src[i] == '\n' {
			return start, i
		}
		if src[i] == '\n' {
			cur++
			start = i + 1
		}
	}
	if cur == line {
		return start, len(src)
	}
	return -1, -1
}

func digitWidth(n int) int {
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

func padInt(n, width int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) >= width {
		return s
	}
	return strings.Repeat(" ", width-len(s)) + s
}

// Ensure utf8 remains imported; used by colToRuneIndex when the caller
// passes a byte column (handled implicitly via []rune).
var _ = utf8.RuneLen
