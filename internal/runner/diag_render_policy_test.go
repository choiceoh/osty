package runner

import "testing"

func TestColToRuneIndex(t *testing.T) {
	cases := []struct {
		name      string
		runeCount int
		col       int
		want      int
	}{
		{"col<=1 returns 0", 10, 1, 0},
		{"col=0 returns 0", 10, 0, 0},
		{"negative col returns 0", 10, -5, 0},
		{"mid column", 10, 5, 4},
		{"last index", 10, 10, 9},
		{"past end clamps", 10, 11, 10},
		{"way past end clamps", 10, 100, 10},
		{"empty seq col 1", 0, 1, 0},
		{"empty seq col 5", 0, 5, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ColToRuneIndex(c.runeCount, c.col); got != c.want {
				t.Errorf("ColToRuneIndex(%d, %d) = %d, want %d",
					c.runeCount, c.col, got, c.want)
			}
		})
	}
}

func TestDigitWidth(t *testing.T) {
	cases := []struct {
		n    int
		want int
	}{
		{0, 1}, {1, 1}, {9, 1},
		{10, 2}, {99, 2},
		{100, 3}, {1234, 4}, {99999, 5},
	}
	for _, c := range cases {
		if got := DigitWidth(c.n); got != c.want {
			t.Errorf("DigitWidth(%d) = %d, want %d", c.n, got, c.want)
		}
	}
}

func TestPadInt(t *testing.T) {
	cases := []struct {
		n     int
		width int
		want  string
	}{
		{5, 3, "  5"},
		{42, 4, "  42"},
		{123, 3, "123"},
		{1234, 3, "1234"},
		{7, 0, "7"},
	}
	for _, c := range cases {
		if got := PadInt(c.n, c.width); got != c.want {
			t.Errorf("PadInt(%d, %d) = %q, want %q", c.n, c.width, got, c.want)
		}
	}
}

func TestLineBounds(t *testing.T) {
	cases := []struct {
		name      string
		src       string
		line      int
		wantStart int
		wantEnd   int
	}{
		{"first line", "alpha\nbeta\ngamma", 1, 0, 5},
		{"mid line", "alpha\nbeta\ngamma", 2, 6, 10},
		{"last no newline", "alpha\nbeta\ngamma", 3, 11, 16},
		{"last with newline", "alpha\nbeta\n", 2, 6, 10},
		{"out of range", "alpha\nbeta\n", 5, -1, -1},
		{"line zero", "alpha\nbeta\n", 0, -1, -1},
		{"negative line", "alpha\nbeta\n", -3, -1, -1},
		{"blank line", "a\n\nb", 2, 2, 2},
		{"empty source line 1 spans empty range", "", 1, 0, 0},
		{"empty source line 2 misses", "", 2, -1, -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := LineBounds([]byte(c.src), c.line)
			if got.Start != c.wantStart || got.End != c.wantEnd {
				t.Errorf("LineBounds(%q, %d) = {%d, %d}, want {%d, %d}",
					c.src, c.line, got.Start, got.End, c.wantStart, c.wantEnd)
			}
		})
	}
}

func TestDiagRenderReplacement(t *testing.T) {
	cases := []struct {
		name         string
		replacement  string
		excerpt      string
		haveExcerpt  bool
		want         string
	}{
		{"empty-replacement-uses-excerpt", "", "foo()", true, "foo()"},
		{"empty-replacement-fallback", "", "", false, "<expr>"},
		{"literal-without-template", "replacement-only", "foo()", true, "foo()"},
		{"template-substitution", "wrap(%s)", "expr", true, "wrap(expr)"},
		{"template-at-start", "%s.field", "obj", true, "obj.field"},
		{"template-at-end", "prefix:%s", "tail", true, "prefix:tail"},
		{"only-first-occurrence", "%s vs %s", "expr", true, "expr vs %s"},
		{"fallback-with-template", "wrap(%s)", "", false, "wrap(<expr>)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := DiagRenderReplacement(c.replacement, c.excerpt, c.haveExcerpt)
			if got != c.want {
				t.Errorf("DiagRenderReplacement(%q, %q, %v) = %q, want %q",
					c.replacement, c.excerpt, c.haveExcerpt, got, c.want)
			}
		})
	}
}

func TestDiagSeverityString(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want string
	}{
		{"error", DiagSeverityError, "error"},
		{"warning", DiagSeverityWarning, "warning"},
		{"note", DiagSeverityNote, "note"},
		{"unknown-positive", 99, "unknown"},
		{"unknown-negative", -1, "unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DiagSeverityString(c.in); got != c.want {
				t.Errorf("DiagSeverityString(%d) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestDiagShortError(t *testing.T) {
	cases := []struct {
		name     string
		code     string
		severity int
		line     int
		column   int
		message  string
		want     string
	}{
		{"with-code", "E0500", DiagSeverityError, 12, 3, "name not found", "E0500: error at 12:3: name not found"},
		{"without-code", "", DiagSeverityWarning, 1, 5, "unused let", "warning at 1:5: unused let"},
		{"note-severity", "E0703", DiagSeverityNote, 42, 1, "see also", "E0703: note at 42:1: see also"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := DiagShortError(c.code, c.severity, c.line, c.column, c.message)
			if got != c.want {
				t.Errorf("DiagShortError = %q, want %q", got, c.want)
			}
		})
	}
}

func TestDiagSeverityAnsiColor(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want string
	}{
		{"error", DiagSeverityError, "\x1b[31m"},
		{"warning", DiagSeverityWarning, "\x1b[33m"},
		{"note", DiagSeverityNote, "\x1b[36m"},
		{"unknown-99", 99, ""},
		{"unknown-neg", -1, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DiagSeverityAnsiColor(c.in); got != c.want {
				t.Errorf("DiagSeverityAnsiColor(%d) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
