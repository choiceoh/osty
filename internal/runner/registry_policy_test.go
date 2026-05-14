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

func TestRegistryStatusErrorMessage(t *testing.T) {
	cases := []struct {
		name       string
		url        string
		statusCode int
		body       string
		statusText string
		want       string
	}{
		{
			"with-body",
			"https://r.dev/v1/crates/serde", 500, "internal error", "500 Internal Server Error",
			"registry https://r.dev/v1/crates/serde: HTTP 500: internal error",
		},
		{
			"empty-body-uses-status",
			"https://r.dev/x", 404, "", "404 Not Found",
			"registry https://r.dev/x: HTTP 404: 404 Not Found",
		},
		{
			"whitespace-body-uses-status",
			"https://r.dev/x", 404, "   \n\t  ", "404 Not Found",
			"registry https://r.dev/x: HTTP 404: 404 Not Found",
		},
		{
			"trims-body",
			"https://r.dev/x", 403, "  not allowed  ", "403 Forbidden",
			"registry https://r.dev/x: HTTP 403: not allowed",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RegistryStatusErrorMessage(c.url, c.statusCode, c.body, c.statusText)
			if got != c.want {
				t.Errorf("RegistryStatusErrorMessage =\n%q\nwant\n%q", got, c.want)
			}
		})
	}
}

func TestValidateRegistryPackageName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"valid-serde", "serde", ""},
		{"valid-underscore-mid", "my_lib", ""},
		{"valid-dash-mid", "json-ext", ""},
		{"valid-digits", "abc123", ""},
		{"valid-camel", "CamelCase", ""},
		{"valid-leading-underscore", "_foo", ""},
		{"empty", "", "package name is empty"},
		{"leading-digit", "1abc", "invalid package name \"1abc\""},
		{"leading-dash", "-abc", "invalid package name \"-abc\""},
		{"dot-rejected", "serde.json", "invalid package name \"serde.json\""},
		{"space-rejected", "hello world", "invalid package name \"hello world\""},
		{"unicode-rejected", "한글", "invalid package name \"한글\""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ValidateRegistryPackageName(c.in); got != c.want {
				t.Errorf("ValidateRegistryPackageName(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestValidateRegistryPackageNameEscapesEchoedName(t *testing.T) {
	// Embedded characters that commonly cause ambiguity in logs/strings
	// (`"`, `\`, control chars) MUST be echoed back escaped — otherwise
	// a malicious name could break log lines or HTTP responses.
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"embedded-quote", "a\"b", `invalid package name "a\"b"`},
		{"embedded-backslash", "a\\b", `invalid package name "a\\b"`},
		{"embedded-newline", "a\nb", `invalid package name "a\nb"`},
		{"embedded-tab", "a\tb", `invalid package name "a\tb"`},
		{"embedded-cr", "a\rb", `invalid package name "a\rb"`},
		{"low-control", "a\x01b", `invalid package name "a\x01b"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ValidateRegistryPackageName(c.in); got != c.want {
				t.Errorf("ValidateRegistryPackageName(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestRegistrySearchRequestNormalize(t *testing.T) {
	cases := []struct {
		name           string
		rawQuery       string
		rawLimit       int
		wantQuery      string
		wantLimit      int
		wantErrMessage string
	}{
		{"basic", "Serde", 50, "serde", 50, ""},
		{"trim-then-lower", "  JSON  ", 10, "json", 10, ""},
		{"empty-query", "", 50, "", 50, "registry search query is empty"},
		{"whitespace-only-query", "   \t\n  ", 5, "", 5, "registry search query is empty"},
		{"empty-query-default-limit", "", 0, "", 20, "registry search query is empty"},
		{"default-limit-zero", "foo", 0, "foo", 20, ""},
		{"default-limit-negative", "foo", -5, "foo", 20, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RegistrySearchRequestNormalize(c.rawQuery, c.rawLimit)
			if got.Query != c.wantQuery || got.Limit != c.wantLimit || got.ErrorMessage != c.wantErrMessage {
				t.Errorf("RegistrySearchRequestNormalize(%q, %d) = %+v, want {%q %d %q}",
					c.rawQuery, c.rawLimit, got,
					c.wantQuery, c.wantLimit, c.wantErrMessage)
			}
		})
	}
}

func TestClassifyPublishDeps(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		normal := []PublishDepSpec{
			{Name: "serde", VersionReq: "1.0"},
			{Name: "log", VersionReq: "0.4"},
		}
		r := ClassifyPublishDeps(normal, nil)
		if r.ErrorMessage != "" {
			t.Fatalf("unexpected err: %q", r.ErrorMessage)
		}
		if len(r.Deps) != 2 || r.Deps[0].Name != "serde" || r.Deps[0].Req != "1.0" || r.Deps[0].Kind != "normal" {
			t.Errorf("unexpected: %+v", r.Deps)
		}
	})
	t.Run("dev-kind", func(t *testing.T) {
		dev := []PublishDepSpec{{Name: "testing", VersionReq: "0.1"}}
		r := ClassifyPublishDeps(nil, dev)
		if len(r.Deps) != 1 || r.Deps[0].Kind != "dev" {
			t.Errorf("unexpected: %+v", r.Deps)
		}
	})
	t.Run("package-rename", func(t *testing.T) {
		normal := []PublishDepSpec{{Name: "alias", PackageName: "real-name", VersionReq: "1.0"}}
		r := ClassifyPublishDeps(normal, nil)
		if r.Deps[0].Name != "real-name" {
			t.Errorf("name = %q, want real-name", r.Deps[0].Name)
		}
	})
	t.Run("default-req-star", func(t *testing.T) {
		normal := []PublishDepSpec{{Name: "anything"}}
		r := ClassifyPublishDeps(normal, nil)
		if r.Deps[0].Req != "*" {
			t.Errorf("req = %q, want *", r.Deps[0].Req)
		}
	})
	t.Run("skips-git", func(t *testing.T) {
		normal := []PublishDepSpec{
			{Name: "serde", VersionReq: "1.0"},
			{Name: "git-dep", IsGit: true},
		}
		r := ClassifyPublishDeps(normal, nil)
		if r.ErrorMessage != "" {
			t.Fatalf("unexpected err: %q", r.ErrorMessage)
		}
		if len(r.Deps) != 1 || r.Deps[0].Name != "serde" {
			t.Errorf("unexpected: %+v", r.Deps)
		}
	})
	t.Run("rejects-path", func(t *testing.T) {
		normal := []PublishDepSpec{{Name: "local", Path: "../local"}}
		r := ClassifyPublishDeps(normal, nil)
		want := `dependency "local" uses path="../local"; path dependencies cannot be published`
		if r.ErrorMessage != want {
			t.Errorf("err = %q, want %q", r.ErrorMessage, want)
		}
		if len(r.Deps) != 0 {
			t.Errorf("deps must be empty on rejection, got %+v", r.Deps)
		}
	})
	t.Run("rejects-path-in-dev", func(t *testing.T) {
		dev := []PublishDepSpec{{Name: "local", Path: "../local"}}
		r := ClassifyPublishDeps(nil, dev)
		want := `dependency "local" uses path="../local"; path dependencies cannot be published`
		if r.ErrorMessage != want {
			t.Errorf("err = %q, want %q", r.ErrorMessage, want)
		}
	})
	t.Run("both-sections", func(t *testing.T) {
		normal := []PublishDepSpec{{Name: "a", VersionReq: "1.0"}}
		dev := []PublishDepSpec{{Name: "b", VersionReq: "2.0"}}
		r := ClassifyPublishDeps(normal, dev)
		if len(r.Deps) != 2 || r.Deps[0].Kind != "normal" || r.Deps[1].Kind != "dev" {
			t.Errorf("unexpected: %+v", r.Deps)
		}
	})
	t.Run("empty", func(t *testing.T) {
		r := ClassifyPublishDeps(nil, nil)
		if r.ErrorMessage != "" || len(r.Deps) != 0 {
			t.Errorf("unexpected: %+v", r)
		}
	})
}
