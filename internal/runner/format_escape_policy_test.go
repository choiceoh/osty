package runner

import "testing"

func TestFormatEscapeCommon(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want string
	}{
		{"backslash", 0x5C, "\\\\"},
		{"newline", 0x0A, "\\n"},
		{"cr", 0x0D, "\\r"},
		{"tab", 0x09, "\\t"},
		{"nul", 0, "\\0"},
		{"other-printable", 0x41, ""},
		{"other-del", 0x7F, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := FormatEscapeCommon(c.in); got != c.want {
				t.Errorf("FormatEscapeCommon(%#x) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestFormatUnicodeEscape(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want string
	}{
		{"zero", 0, "\\u{0}"},
		{"single-digit", 0xA, "\\u{A}"},
		{"two-digits", 0xFF, "\\u{FF}"},
		{"four-digits", 0xD55C, "\\u{D55C}"},
		{"strips-leading-zeros", 0x100, "\\u{100}"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := FormatUnicodeEscape(c.in); got != c.want {
				t.Errorf("FormatUnicodeEscape(%#x) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestFormatEscapeByteForChar(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want string
	}{
		{"single-quote", 0x27, "\\'"},
		{"open-brace", 0x7B, "\\{"},
		{"close-brace", 0x7D, "\\}"},
		{"newline", 0x0A, "\\n"},
		{"backslash", 0x5C, "\\\\"},
		{"printable-A", 0x41, "A"},
		{"printable-space", 0x20, " "},
		{"printable-tilde", 0x7E, "~"},
		{"low-control-01", 0x01, "\\x01"},
		{"low-control-1F", 0x1F, "\\x1F"},
		{"high-80", 0x80, "\\x80"},
		{"high-FF", 0xFF, "\\xFF"},
		{"del-7F", 0x7F, "\\x7F"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := FormatEscapeByteForChar(c.in); got != c.want {
				t.Errorf("FormatEscapeByteForChar(%#x) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestFormatEscapeByteForBytes(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want string
	}{
		{"double-quote", 0x22, "\\\""},
		{"single-quote-still-escaped", 0x27, "\\'"},
		{"printable", 0x41, "A"},
		{"newline", 0x0A, "\\n"},
		{"high", 0xFF, "\\xFF"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := FormatEscapeByteForBytes(c.in); got != c.want {
				t.Errorf("FormatEscapeByteForBytes(%#x) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
