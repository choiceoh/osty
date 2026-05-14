package runner

import (
	"reflect"
	"testing"
)

func TestParseTriple(t *testing.T) {
	cases := []struct {
		name   string
		triple string
		ok     bool
		arch   string
		os     string
	}{
		{"simple", "x86_64-linux", true, "x86_64", "linux"},
		{"internal-dashes", "x86_64-apple-darwin", true, "x86_64", "apple-darwin"},
		{"no-dash", "x86_64", false, "", ""},
		{"empty-arch", "-linux", false, "", ""},
		{"empty-os", "x86_64-", false, "", ""},
		{"empty", "", false, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ParseTriple(c.triple)
			if got.Ok != c.ok || got.Arch != c.arch || got.OS != c.os {
				t.Errorf("ParseTriple(%q) = %+v, want {%q %q %v}",
					c.triple, got, c.arch, c.os, c.ok)
			}
		})
	}
}

func TestReadFeaturePragma(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"line-comment", "// @feature: a, b\nlet x = 1\n", []string{"a", "b"}},
		{"tight-comment", "//@feature: foo bar\n", []string{"foo", "bar"}},
		{"skips-blank-and-comments", "\n// preamble comment\n// @feature: x\n", []string{"x"}},
		{"stops-at-first-code", "let x = 1\n// @feature: ignored\n", nil},
		{"no-pragma", "let x = 1\nlet y = 2\n", nil},
		{"mixed-separators", "// @feature: a, b c\td\n", []string{"a", "b", "c", "d"}},
		{"crlf-line", "// @feature: a\r, b\r\n", []string{"a", "b"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ReadFeaturePragma([]byte(c.src))
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("ReadFeaturePragma(%q) = %v, want %v", c.src, got, c.want)
			}
		})
	}
}

func TestParsePragmaFeatureList(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"a,b,c", []string{"a", "b", "c"}},
		{"a b c", []string{"a", "b", "c"}},
		{"a\tb\tc", []string{"a", "b", "c"}},
		{"a,,b , c", []string{"a", "b", "c"}},
		{"", nil},
		{"   ", nil},
	}
	for _, c := range cases {
		got := ParsePragmaFeatureList(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParsePragmaFeatureList(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
