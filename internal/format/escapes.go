package format

import (
	"strings"
	"unicode"

	"github.com/osty/osty/internal/runner"
)

// Escape helpers render AST literal values (rune / byte / string
// segment) back to source form. The byte-level + universal-escape
// pieces live in toolchain/format_escape.osty; this file owns the
// rune-level escapers that still need `unicode.IsPrint` for
// invisible-rune detection.

// escapeCommon delegates to runner.FormatEscapeCommon (mirror of
// toolchain/format_escape.osty). The 5-case mapping (`\\`, `\n`,
// `\r`, `\t`, `\0`) is shared with the byte escapers.
func escapeCommon(r rune) string {
	return runner.FormatEscapeCommon(int(r))
}

// appendUnicodeEscape writes the Osty `\u{XXXX}` hex escape for r
// to b. Delegates to runner.AppendFormatUnicodeEscape — the
// streaming sibling of FormatUnicodeEscape — to keep the
// zero-allocation path for string-escape hot loops while policy
// ownership stays in toolchain/format_escape.osty.
func appendUnicodeEscape(b *strings.Builder, r rune) {
	runner.AppendFormatUnicodeEscape(b, int(r))
}

// unicodeEscape is the string-returning sibling of
// appendUnicodeEscape; both call into the same runner policy.
func unicodeEscape(r rune) string {
	return runner.FormatUnicodeEscape(int(r))
}

func escapeForChar(r rune) string {
	if r == '\'' {
		return `\'`
	}
	if r == '{' {
		return `\{`
	}
	if r == '}' {
		return `\}`
	}
	if s := escapeCommon(r); s != "" {
		return s
	}
	if !unicode.IsPrint(r) {
		return unicodeEscape(r)
	}
	return string(r)
}

func escapeForByte(b byte) string {
	return runner.FormatEscapeByteForChar(int(b))
}

func escapeForBytesLit(b byte) string {
	return runner.FormatEscapeByteForBytes(int(b))
}

// writeDefaultRune is the shared tail of the string escapers: an
// escapeCommon match wins, otherwise unicode.IsPrint gates whether the
// rune goes out literally or as `\u{...}`. IsPrint catches the invisible
// troublemakers a bare `< 0x20` check would miss (line separators,
// bidi/format controls, tag characters).
func writeDefaultRune(b *strings.Builder, r rune) {
	if esc := escapeCommon(r); esc != "" {
		b.WriteString(esc)
		return
	}
	if !unicode.IsPrint(r) {
		appendUnicodeEscape(b, r)
		return
	}
	b.WriteRune(r)
}

// escapeTripleStringText escapes a segment of triple-quoted non-raw
// string content. Raw `\n` and `\t` stay literal (the point of triple),
// and `"` needs no escape (the lexer only closes on a full `"""`).
func escapeTripleStringText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\n', '\t':
			b.WriteRune(r)
		case '{':
			b.WriteString(`\{`)
		case '}':
			b.WriteString(`\}`)
		default:
			writeDefaultRune(&b, r)
		}
	}
	return b.String()
}

// escapeStringText escapes a segment of single-quoted string content.
// `\{` / `\}` guard against an interpolation being reintroduced on the
// next parse pass — the lexer decodes them into bare braces in PartText.
func escapeStringText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '{':
			b.WriteString(`\{`)
		case '}':
			b.WriteString(`\}`)
		default:
			writeDefaultRune(&b, r)
		}
	}
	return b.String()
}
