// format_escape_policy.go is the Go snapshot of
// toolchain/format_escape.osty. Osty is the source of truth;
// the drift test in this package keeps the two in sync.
package runner

import "strings"

// FormatEscapeCommon returns the Osty escape for the five
// codepoints (`\\`, `\n`, `\r`, `\t`, `\0`) that behave the
// same across Char, Byte, and String literals, or "" when `r`
// needs no shared handling.
//
// Osty: toolchain/format_escape.osty:26
func FormatEscapeCommon(r int) string {
	switch r {
	case 0x5C:
		return "\\\\"
	case 0x0A:
		return "\\n"
	case 0x0D:
		return "\\r"
	case 0x09:
		return "\\t"
	case 0:
		return "\\0"
	}
	return ""
}

// FormatUnicodeEscape renders the Osty `\u{HEX}` escape for the
// codepoint `r` with uppercase hex and no leading zeros. r == 0
// emits `\u{0}` (single digit).
//
// Osty: toolchain/format_escape.osty:47
func FormatUnicodeEscape(r int) string {
	var b strings.Builder
	AppendFormatUnicodeEscape(&b, r)
	return b.String()
}

// AppendFormatUnicodeEscape is the streaming sibling of
// FormatUnicodeEscape — writes the `\u{HEX}` form directly into
// `b` so per-rune escape loops avoid the intermediate string
// allocation. Output bytes are identical; both implementations
// share the policy rules mirrored from
// toolchain/format_escape.osty:47.
func AppendFormatUnicodeEscape(b *strings.Builder, r int) {
	if r == 0 {
		b.WriteString("\\u{0}")
		return
	}
	b.WriteString("\\u{")
	digits := 0
	for x := r; x > 0; x >>= 4 {
		digits++
	}
	for i := digits - 1; i >= 0; i-- {
		nibble := (r >> (4 * i)) & 0xF
		b.WriteByte(formatHexDigitsUpper[nibble])
	}
	b.WriteByte('}')
}

const formatHexDigitsUpper = "0123456789ABCDEF"

// FormatEscapeByteForChar emits one Char-literal-context escape
// for the byte `b` (0..255).
//
// Osty: toolchain/format_escape.osty:76
func FormatEscapeByteForChar(b int) string {
	if b == 0x27 {
		return "\\'"
	}
	if b == 0x7B {
		return "\\{"
	}
	if b == 0x7D {
		return "\\}"
	}
	if common := FormatEscapeCommon(b); common != "" {
		return common
	}
	if b < 0x20 || b > 0x7E {
		return "\\x" + formatTwoHexUpper(b)
	}
	return string(rune(b))
}

// FormatEscapeByteForBytes emits one bytes-literal-context
// escape. Identical to FormatEscapeByteForChar except `"` is
// escaped (bytes literals are double-quoted) instead of `'`
// being singled out — note: the function delegates to the char
// variant after handling `"`, so `'` is still escaped per the
// shared rule.
//
// Osty: toolchain/format_escape.osty:98
func FormatEscapeByteForBytes(b int) string {
	if b == 0x22 {
		return "\\\""
	}
	return FormatEscapeByteForChar(b)
}

func formatTwoHexUpper(b int) string {
	hi := (b >> 4) & 0xF
	lo := b & 0xF
	return string(formatHexDigitsUpper[hi]) + string(formatHexDigitsUpper[lo])
}
