package format

import (
	"fmt"
	"strings"
	"unicode"
)

// Escape helpers render AST literal values (rune / byte / string
// segment) back to source form. No existing lexer helper is exported
// for the inverse direction, and strconv.Quote doesn't speak Osty's
// `\u{...}` and `\{` `\}` conventions — hence this file.

// escapeCommon returns the Osty escape for the five runes that behave
// the same across Char, Byte, and String literals, or "" when r needs
// no shared handling.
func escapeCommon(r rune) string {
	switch r {
	case '\\':
		return `\\`
	case '\n':
		return `\n`
	case '\r':
		return `\r`
	case '\t':
		return `\t`
	case 0:
		return `\0`
	}
	return ""
}

// appendUnicodeEscape writes the Osty `\u{XXXX}` hex escape for r to b
// with uppercase hex and no leading zeros. Used from per-rune escape
// loops in place of fmt.Fprintf, which would allocate on every call.
func appendUnicodeEscape(b *strings.Builder, r rune) {
	const hex = "0123456789ABCDEF"
	b.WriteString(`\u{`)
	if r == 0 {
		b.WriteByte('0')
	} else {
		// Most-significant hex digit first; strip leading zeros.
		digits := 0
		for x := r; x > 0; x >>= 4 {
			digits++
		}
		for i := digits - 1; i >= 0; i-- {
			b.WriteByte(hex[(r>>(4*i))&0xF])
		}
	}
	b.WriteByte('}')
}

// unicodeEscape is the string-returning sibling of appendUnicodeEscape,
// for callers (escapeForChar) that package one rune into a literal body
// at a time rather than streaming into a shared builder.
func unicodeEscape(r rune) string {
	var b strings.Builder
	appendUnicodeEscape(&b, r)
	return b.String()
}

func escapeForChar(r rune) string {
	if r == '\'' {
		return `\'`
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
	if b == '\'' {
		return `\'`
	}
	if s := escapeCommon(rune(b)); s != "" {
		return s
	}
	if b < 0x20 || b > 0x7E {
		return fmt.Sprintf(`\x%02X`, b)
	}
	return string(b)
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
