package runner

import "testing"

func TestDefaultHeadingFor(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{"E0001", "Errors starting at E0001"},
		{"E0500", "Errors starting at E0500"},
		{"W0750", "Warnings starting at W0750"},
		{"L0001", "Lint warnings starting at L0001"},
		{"X1234", "Miscellaneous"},
		{"", "Miscellaneous"},
	}
	for _, c := range cases {
		if got := DefaultHeadingFor(c.code); got != c.want {
			t.Errorf("DefaultHeadingFor(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

func TestStripRangeSuffix(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"Lexical (E0001-E0099)", "Lexical"},
		{"Manifest (E2017)", "Manifest"},
		{"Lexical", "Lexical"},
		{"", ""},
		{"  Lexical  ", "Lexical"},
		{"Foo (bar) (E0001)", "Foo (bar)"},
	}
	for _, c := range cases {
		if got := StripRangeSuffix(c.in); got != c.want {
			t.Errorf("StripRangeSuffix(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestUnsafeForBootstrapGen(t *testing.T) {
	yes := []string{
		"match x { A => B }",
		"let f = fn (x) => { x }",
	}
	for _, in := range yes {
		if !UnsafeForBootstrapGen(in) {
			t.Errorf("UnsafeForBootstrapGen(%q) = false, want true", in)
		}
	}
	no := []string{
		"fn main() {}", // brace only
		"{ a, b }",     // brace only
		"x => y",       // fat arrow only
		"let x = 1",    // neither
		"",
	}
	for _, in := range no {
		if UnsafeForBootstrapGen(in) {
			t.Errorf("UnsafeForBootstrapGen(%q) = true, want false", in)
		}
	}
}

func TestProseExample(t *testing.T) {
	yes := []string{"foo(...)", "a ... b", "a … b", "x → y"}
	for _, in := range yes {
		if !ProseExample(in) {
			t.Errorf("ProseExample(%q) = false, want true", in)
		}
	}
	no := []string{"let x = 1", "", "a -> b"}
	for _, in := range no {
		if ProseExample(in) {
			t.Errorf("ProseExample(%q) = true, want false", in)
		}
	}
}

func TestOstyEscape(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"hello", "hello"},
		{"", ""},
		{`a\b`, `a\\b`},
		{`a"b`, `a\"b`},
		{"a\nb", `a\nb`},
		{"a\tb", `a\tb`},
		{"a\rb", `a\rb`},
		{"a{b}c", `a\{b\}c`},
		{"한글", "한글"},
	}
	for _, c := range cases {
		if got := OstyEscape(c.in); got != c.want {
			t.Errorf("OstyEscape(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
