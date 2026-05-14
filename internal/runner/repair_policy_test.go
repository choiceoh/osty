package runner

import "testing"

func TestUppercaseBasePrefix(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"0XFF", "x"},
		{"0X0", "x"},
		{"0B1010", "b"},
		{"0O777", "o"},
		{"0xFF", ""},
		{"0b10", ""},
		{"0o7", ""},
		{"1XFF", ""},
		{"XX", ""},
		{"", ""},
		{"0", ""},
		{"0Z123", ""},
		{"0123", ""},
	}
	for _, c := range cases {
		if got := UppercaseBasePrefix(c.in); got != c.want {
			t.Errorf("UppercaseBasePrefix(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDeclarationKeywordReplacement(t *testing.T) {
	known := []string{"func", "function", "def"}
	for _, v := range known {
		r := DeclarationKeywordReplacement(v)
		if !r.Ok || r.Value != "fn" {
			t.Errorf("DeclarationKeywordReplacement(%q) = %+v, want {fn true}", v, r)
		}
	}
	unknown := []string{"fn", "let", "", "FUNC"}
	for _, v := range unknown {
		r := DeclarationKeywordReplacement(v)
		if r.Ok {
			t.Errorf("DeclarationKeywordReplacement(%q).Ok = true, want false", v)
		}
	}
}

func TestValueIdentifierReplacement(t *testing.T) {
	cases := []struct {
		in        string
		wantOk    bool
		wantValue string
	}{
		{"nil", true, "None"},
		{"null", true, "None"},
		{"True", true, "true"},
		{"False", true, "false"},
		{"None", false, ""},
		{"true", false, ""},
		{"false", false, ""},
		{"", false, ""},
	}
	for _, c := range cases {
		r := ValueIdentifierReplacement(c.in)
		if r.Ok != c.wantOk || r.Value != c.wantValue {
			t.Errorf("ValueIdentifierReplacement(%q) = %+v, want {%q %v}",
				c.in, r, c.wantValue, c.wantOk)
		}
	}
}

func TestLineIndent(t *testing.T) {
	cases := []struct {
		name   string
		src    string
		offset int
		want   string
	}{
		{"mid-line", "    hello", 8, "    "},
		{"tab-indent", "\t\thello", 5, "\t\t"},
		{"no-indent", "hello", 3, ""},
		{"second-line", "first\n    second", 10, "    "},
		{"blank-line", "first\n\n    third", 6, ""},
		{"offset-past-end", "ab", 100, ""},
		{"mixed-tab-then-non-ws", " \tcode", 4, " \t"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := LineIndent([]byte(c.src), c.offset); got != c.want {
				t.Errorf("LineIndent(%q, %d) = %q, want %q",
					c.src, c.offset, got, c.want)
			}
		})
	}
}
