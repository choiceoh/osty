package runner

import "testing"

func TestUseGroupOrder(t *testing.T) {
	cases := []struct {
		name     string
		isFFI    bool
		firstSeg string
		want     int
	}{
		{"stdlib", false, "std", 0},
		{"external-github", false, "github.com", 1},
		{"external-other", false, "mypkg", 1},
		{"external-empty", false, "", 1},
		{"ffi", true, "go", 2},
		{"ffi-with-std-segment", true, "std", 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := UseGroupOrder(c.isFFI, c.firstSeg); got != c.want {
				t.Errorf("UseGroupOrder(%v, %q) = %d, want %d", c.isFFI, c.firstSeg, got, c.want)
			}
		})
	}
}

func TestUseSortKey(t *testing.T) {
	cases := []struct {
		name   string
		isFFI  bool
		ffi    string
		raw    string
		dotted string
		want   string
	}{
		{"ffi-precedence", true, "net/http", "ignored", "ignored.too", "net/http"},
		{"raw-path", false, "", "github.com/x/y", "x.y", "github.com/x/y"},
		{"dotted-path", false, "", "", "std.io", "std.io"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := UseSortKey(c.isFFI, c.ffi, c.raw, c.dotted); got != c.want {
				t.Errorf("UseSortKey = %q, want %q", got, c.want)
			}
		})
	}
}

func TestUseCSurfaceLibName(t *testing.T) {
	cases := []struct {
		name string
		path string
		ok   bool
		lib  string
	}{
		{"simple", "runtime.cabi.libc", true, "libc"},
		{"reject-no-prefix", "runtime.go.libc", false, ""},
		{"reject-multi-segment", "runtime.cabi.libc.subgroup", false, ""},
		{"reject-slash", "runtime.cabi.libc/extra", false, ""},
		{"reject-empty-lib", "runtime.cabi.", false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := UseCSurfaceLibName(c.path)
			if got.Ok != c.ok {
				t.Errorf("UseCSurfaceLibName(%q).Ok = %v, want %v", c.path, got.Ok, c.ok)
			}
			if got.Lib != c.lib {
				t.Errorf("UseCSurfaceLibName(%q).Lib = %q, want %q", c.path, got.Lib, c.lib)
			}
		})
	}
}

func TestFinalizeFormatted(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"adds-trailing-newline", "hello", "hello\n"},
		{"keeps-single-newline", "hello\n", "hello\n"},
		{"collapses-trailing-newlines", "hello\n\n\n", "hello\n"},
		{"trims-trailing-spaces", "a  \nb\t\n", "a\nb\n"},
		{"collapses-blank-lines", "a\n\n\nb\n", "a\n\nb\n"},
		{"drops-leading-blanks", "\n\nhello\n", "hello\n"},
		{"all-blanks-reduces", "\n\n\n", "\n"},
		{"empty", "", "\n"},
		{"preserves-intra-line-ws", "a   b\n", "a   b\n"},
		{"trims-mixed-tabs-spaces", "line \t \nnext", "line\nnext\n"},
		{"no-trailing-newline-body", "a\nb", "a\nb\n"},
		{"preserves-non-ascii", "한글 \n", "한글\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := string(FinalizeFormatted([]byte(c.in)))
			if got != c.want {
				t.Errorf("FinalizeFormatted(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestShouldBreakChain(t *testing.T) {
	cases := []struct {
		name        string
		baseEndLine int
		segs        []ChainSegLines
		want        bool
	}{
		{"empty", 1, nil, false},
		{
			"short-same-line", 1,
			[]ChainSegLines{
				{NameEndLine: 1, EndLine: 1},
				{NameEndLine: 1, EndLine: 1},
			},
			false,
		},
		{
			"three-or-more", 1,
			[]ChainSegLines{
				{NameEndLine: 1, EndLine: 1},
				{NameEndLine: 1, EndLine: 1},
				{NameEndLine: 1, EndLine: 1},
			},
			true,
		},
		{
			"source-break", 1,
			[]ChainSegLines{
				{NameEndLine: 2, EndLine: 2},
			},
			true,
		},
		{
			"preserves-prior-break", 1,
			[]ChainSegLines{
				{NameEndLine: 1, EndLine: 1},
				{NameEndLine: 2, EndLine: 2},
			},
			true,
		},
		{
			"multi-line-call-stays-flat", 1,
			[]ChainSegLines{
				{NameEndLine: 1, EndLine: 5},
			},
			false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ShouldBreakChain(c.baseEndLine, c.segs); got != c.want {
				t.Errorf("ShouldBreakChain = %v, want %v", got, c.want)
			}
		})
	}
}
