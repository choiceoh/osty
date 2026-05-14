package runner

import "testing"

func TestSanitizeIndexName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"all-safe-lower", "serde", "serde"},
		{"hyphen", "json-ext", "json-ext"},
		{"underscore", "my_lib", "my_lib"},
		{"dot", "serde.json", "serde.json"},
		{"digits", "abc123", "abc123"},
		{"camel-case", "CamelCase", "CamelCase"},
		{"empty-collapses", "", "_empty"},
		{"slash-escaped", "a/b", "a_2fb"},
		{"space-escaped", "hello world", "hello_20world"},
		{"colon", "foo:bar", "foo_3abar"},
		{"at-sign", "a@b", "a_40b"},
		{"parens", "a()", "a_28_29"},
		{"korean", "한글", "_d55c_ae00"},
		{"mixed-ascii-unicode", "a한b", "a_d55cb"},
		{"existing-underscore-not-doubled", "a_b/c", "a_b_2fc"},
		{"only-unsafe", "//", "_2f_2f"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := SanitizeIndexName(c.in); got != c.want {
				t.Errorf("SanitizeIndexName(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestRegistrySearchScoreOf(t *testing.T) {
	cases := []struct {
		name        string
		pkgName     string
		description string
		keywords    []string
		query       string
		wantOk      bool
		wantScore   int
	}{
		{"exact-name", "serde", "JSON lib", nil, "serde", true, 0},
		{"case-insensitive-exact", "Serde", "", nil, "serde", true, 0},
		{"prefix", "serdex", "", nil, "serde", true, 1},
		{"contains-name", "my-serde-helper", "", nil, "serde", true, 2},
		{"description", "foo", "A JSON serde library", nil, "serde", true, 3},
		{"keyword", "foo", "bar", []string{"serialization", "serde"}, "serde", true, 4},
		{"keyword-case-insensitive", "foo", "", []string{"JSON"}, "json", true, 4},
		{"no-match", "foo", "bar", []string{"baz"}, "serde", false, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RegistrySearchScoreOf(c.pkgName, c.description, c.keywords, c.query)
			if got.Ok != c.wantOk {
				t.Fatalf("Ok = %v, want %v", got.Ok, c.wantOk)
			}
			if c.wantOk && got.Score != c.wantScore {
				t.Errorf("Score = %d, want %d", got.Score, c.wantScore)
			}
		})
	}
}
