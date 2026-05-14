package runner

import "testing"

func TestLintMatchGlob(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		path    string
		want    bool
	}{
		{"literal-match", "foo/bar", "foo/bar", true},
		{"literal-miss", "foo/bar", "foo/baz", false},
		{"length-mismatch-short-pattern", "foo", "foo/bar", false},
		{"length-mismatch-short-path", "foo/bar", "foo", false},
		{"trailing-doublestar-one-segment", "gen/**", "gen/a.osty", true},
		{"trailing-doublestar-deep", "gen/**", "gen/a/b/c.osty", true},
		{"trailing-doublestar-zero-segments", "gen/**", "gen", true},
		{"trailing-doublestar-other-prefix", "gen/**", "other/a.osty", false},
		{"middle-doublestar-zero-segs", "gen/**/x.osty", "gen/x.osty", true},
		{"middle-doublestar-one-seg", "gen/**/x.osty", "gen/a/x.osty", true},
		{"middle-doublestar-many-segs", "gen/**/x.osty", "gen/a/b/x.osty", true},
		{"middle-doublestar-wrong-tail", "gen/**/x.osty", "gen/a/b/y.osty", false},
		{"leading-doublestar-zero", "**/x.osty", "x.osty", true},
		{"leading-doublestar-one", "**/x.osty", "a/x.osty", true},
		{"leading-doublestar-many", "**/x.osty", "a/b/x.osty", true},
		{"leading-doublestar-wrong-tail", "**/x.osty", "a/b/y.osty", false},
		{"star-segment-match", "gen/*.osty", "gen/a.osty", true},
		{"star-segment-multichar", "gen/*.osty", "gen/aaa.osty", true},
		{"star-segment-no-cross-segment", "gen/*.osty", "gen/a/b.osty", false},
		{"star-no-cross-segment", "gen/*", "gen/a/b", false},
		{"question-mark-match", "?.osty", "a.osty", true},
		{"question-mark-too-many", "?.osty", "ab.osty", false},
		{"only-doublestar-matches-deep", "**", "a/b/c", true},
		{"only-doublestar-empty-path-accepted", "**", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := LintMatchGlob(c.pattern, c.path); got != c.want {
				t.Errorf("LintMatchGlob(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
			}
		})
	}
}

func TestLintMatchSegment(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		seg     string
		want    bool
	}{
		{"literal-match", "abc", "abc", true},
		{"literal-miss", "abc", "abd", false},
		{"literal-short", "abc", "ab", false},
		{"literal-long", "abc", "abcd", false},
		{"star-empty", "*", "", true},
		{"star-anything", "*", "anything", true},
		{"star-ext-short", "*.osty", "x.osty", true},
		{"star-ext-long", "*.osty", "xxxxxxxx.osty", true},
		{"a-star-c-match", "a*c", "abc", true},
		{"a-star-c-many", "a*c", "abbbbbc", true},
		{"a-star-c-miss", "a*c", "ad", false},
		{"question-one", "?", "a", true},
		{"question-empty", "?", "", false},
		{"a-q-c-match", "a?c", "abc", true},
		{"a-q-c-miss", "a?c", "ac", false},
		{"class-a", "[abc]", "a", true},
		{"class-b", "[abc]", "b", true},
		{"class-miss", "[abc]", "d", false},
		{"class-range-mid", "[a-z]", "m", true},
		{"class-range-out", "[a-z]", "A", false},
		{"class-digits-in", "[0-9]", "5", true},
		{"class-digits-out", "[0-9]", "a", false},
		{"class-negated-caret-hit", "[^abc]", "d", true},
		{"class-negated-caret-miss", "[^abc]", "a", false},
		{"class-negated-bang-hit", "[!abc]", "d", true},
		{"class-negated-bang-miss", "[!abc]", "a", false},
		{"escape-literal-star", "a\\*b", "a*b", true},
		{"escape-literal-star-miss", "a\\*b", "axb", false},
		{"empty-both", "", "", true},
		{"empty-pattern-nonempty-name", "", "x", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := LintMatchSegment(c.pattern, c.seg); got != c.want {
				t.Errorf("LintMatchSegment(%q, %q) = %v, want %v", c.pattern, c.seg, got, c.want)
			}
		})
	}
}
