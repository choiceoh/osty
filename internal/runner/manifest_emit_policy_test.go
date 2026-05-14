package runner

import (
	"testing"
)

func defaultManifestDep(name string) ManifestDepEmitSpec {
	return ManifestDepEmitSpec{
		Name:         name,
		DefaultFeats: true,
	}
}

func TestRenderManifestDepLine(t *testing.T) {
	cases := []struct {
		name string
		in   ManifestDepEmitSpec
		want string
	}{
		{
			name: "short-form",
			in: func() ManifestDepEmitSpec {
				d := defaultManifestDep("serde")
				d.VersionReq = "1.0.0"
				return d
			}(),
			want: "serde = \"1.0.0\"\n",
		},
		{
			name: "short-form-special-char",
			in: func() ManifestDepEmitSpec {
				d := defaultManifestDep("name")
				d.VersionReq = ">=1.2 <2.0"
				return d
			}(),
			want: "name = \">=1.2 <2.0\"\n",
		},
		{
			name: "long-with-path",
			in: func() ManifestDepEmitSpec {
				d := defaultManifestDep("local")
				d.Path = "../local"
				return d
			}(),
			want: "local = { path = \"../local\" }\n",
		},
		{
			name: "long-version-plus-path",
			in: func() ManifestDepEmitSpec {
				d := defaultManifestDep("local")
				d.VersionReq = "1.0.0"
				d.Path = "../local"
				return d
			}(),
			want: "local = { version = \"1.0.0\", path = \"../local\" }\n",
		},
		{
			name: "git-tag-branch-rev",
			in: func() ManifestDepEmitSpec {
				d := defaultManifestDep("x")
				d.Git = ManifestGitSpec{
					URL:    "https://example.com/x.git",
					Tag:    "v1.0.0",
					Branch: "main",
					Rev:    "abc1234",
				}
				return d
			}(),
			want: "x = { git = \"https://example.com/x.git\", tag = \"v1.0.0\", branch = \"main\", rev = \"abc1234\" }\n",
		},
		{
			name: "package-rename",
			in: func() ManifestDepEmitSpec {
				d := defaultManifestDep("alias")
				d.VersionReq = "1.0.0"
				d.PackageName = "real-name"
				return d
			}(),
			want: "alias = { version = \"1.0.0\", package = \"real-name\" }\n",
		},
		{
			name: "registry-named",
			in: func() ManifestDepEmitSpec {
				d := defaultManifestDep("x")
				d.VersionReq = "1.0.0"
				d.Registry = "custom"
				return d
			}(),
			want: "x = { version = \"1.0.0\", registry = \"custom\" }\n",
		},
		{
			name: "optional-true",
			in: func() ManifestDepEmitSpec {
				d := defaultManifestDep("x")
				d.VersionReq = "1.0.0"
				d.Optional = true
				return d
			}(),
			want: "x = { version = \"1.0.0\", optional = true }\n",
		},
		{
			name: "features",
			in: func() ManifestDepEmitSpec {
				d := defaultManifestDep("x")
				d.VersionReq = "1.0.0"
				d.Features = []string{"a", "b"}
				return d
			}(),
			want: "x = { version = \"1.0.0\", features = [\"a\", \"b\"] }\n",
		},
		{
			name: "default-features-false",
			in: func() ManifestDepEmitSpec {
				d := defaultManifestDep("x")
				d.VersionReq = "1.0.0"
				d.DefaultFeats = false
				return d
			}(),
			want: "x = { version = \"1.0.0\", default-features = false }\n",
		},
		{
			name: "combined",
			in: func() ManifestDepEmitSpec {
				d := defaultManifestDep("x")
				d.VersionReq = "1.0.0"
				d.Optional = true
				d.Features = []string{"foo"}
				d.DefaultFeats = false
				return d
			}(),
			want: "x = { version = \"1.0.0\", optional = true, features = [\"foo\"], default-features = false }\n",
		},
		{
			name: "no-fields-long",
			in:   defaultManifestDep("x"),
			want: "x = {  }\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := RenderManifestDepLine(c.in); got != c.want {
				t.Errorf("RenderManifestDepLine =\n  %q\n want\n  %q", got, c.want)
			}
		})
	}
}

func TestRenderManifestStringField(t *testing.T) {
	cases := []struct {
		name, key, val, want string
	}{
		{"plain", "name", "demo", `name = "demo"` + "\n"},
		{"version", "version", "1.0.0", `version = "1.0.0"` + "\n"},
		{"quoting-special", "desc", `needs "quotes"`, `desc = "needs \"quotes\""` + "\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := RenderManifestStringField(c.key, c.val); got != c.want {
				t.Errorf("RenderManifestStringField = %q, want %q", got, c.want)
			}
		})
	}
}

func TestRenderManifestStringArrayField(t *testing.T) {
	cases := []struct {
		name, key string
		vals      []string
		want      string
	}{
		{"two-values", "authors", []string{"Alice", "Bob"}, `authors = ["Alice", "Bob"]` + "\n"},
		{"empty", "authors", []string{}, "authors = []\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := RenderManifestStringArrayField(c.key, c.vals); got != c.want {
				t.Errorf("RenderManifestStringArrayField = %q, want %q", got, c.want)
			}
		})
	}
}

func TestRenderManifestBoolField(t *testing.T) {
	cases := []struct {
		name, key string
		val       bool
		want      string
	}{
		{"true", "runtime", true, "runtime = true\n"},
		{"false", "runtime", false, "runtime = false\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := RenderManifestBoolField(c.key, c.val); got != c.want {
				t.Errorf("RenderManifestBoolField = %q, want %q", got, c.want)
			}
		})
	}
}

func TestRenderManifestDepsSection(t *testing.T) {
	t.Run("empty-returns-empty", func(t *testing.T) {
		if got := RenderManifestDepsSection("dependencies", nil); got != "" {
			t.Errorf("empty deps must return empty string, got %q", got)
		}
	})
	t.Run("sorts-by-name", func(t *testing.T) {
		a := defaultManifestDep("alpha")
		a.VersionReq = "1.0.0"
		z := defaultManifestDep("zed")
		z.VersionReq = "2.0.0"
		got := RenderManifestDepsSection("dependencies", []ManifestDepEmitSpec{z, a})
		want := "\n[dependencies]\nalpha = \"1.0.0\"\nzed = \"2.0.0\"\n"
		if got != want {
			t.Errorf("=\n%q\nwant\n%q", got, want)
		}
	})
	t.Run("mixed-short-long-forms", func(t *testing.T) {
		s := defaultManifestDep("a")
		s.VersionReq = "1.0.0"
		l := defaultManifestDep("b")
		l.Path = "../b"
		got := RenderManifestDepsSection("dev-dependencies", []ManifestDepEmitSpec{s, l})
		want := "\n[dev-dependencies]\na = \"1.0.0\"\nb = { path = \"../b\" }\n"
		if got != want {
			t.Errorf("=\n%q\nwant\n%q", got, want)
		}
	})
}

func TestRenderManifestBinSection(t *testing.T) {
	cases := []struct {
		name string
		in   ManifestBinEmitSpec
		want string
	}{
		{"both-empty", ManifestBinEmitSpec{}, ""},
		{"name-only", ManifestBinEmitSpec{Name: "demo"}, "\n[bin]\nname = \"demo\"\n"},
		{"path-only", ManifestBinEmitSpec{Path: "src/main.osty"}, "\n[bin]\npath = \"src/main.osty\"\n"},
		{"both", ManifestBinEmitSpec{Name: "demo", Path: "src/main.osty"}, "\n[bin]\nname = \"demo\"\npath = \"src/main.osty\"\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := RenderManifestBinSection(c.in); got != c.want {
				t.Errorf("=\n%q\nwant\n%q", got, c.want)
			}
		})
	}
}

func TestRenderManifestLibSection(t *testing.T) {
	cases := []struct {
		name string
		in   ManifestLibEmitSpec
		want string
	}{
		{"empty", ManifestLibEmitSpec{}, ""},
		{"with-path", ManifestLibEmitSpec{Path: "src/lib.osty"}, "\n[lib]\npath = \"src/lib.osty\"\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := RenderManifestLibSection(c.in); got != c.want {
				t.Errorf("=\n%q\nwant\n%q", got, c.want)
			}
		})
	}
}

func TestRenderManifestWorkspaceSection(t *testing.T) {
	cases := []struct {
		name string
		in   ManifestWorkspaceEmitSpec
		want string
	}{
		{"two-members", ManifestWorkspaceEmitSpec{Members: []string{"pkg-a", "pkg-b"}}, "\n[workspace]\nmembers = [\"pkg-a\", \"pkg-b\"]\n"},
		{"empty-members", ManifestWorkspaceEmitSpec{}, "\n[workspace]\nmembers = []\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := RenderManifestWorkspaceSection(c.in); got != c.want {
				t.Errorf("=\n%q\nwant\n%q", got, c.want)
			}
		})
	}
}

func TestRenderManifestCapabilitiesSection(t *testing.T) {
	cases := []struct {
		name string
		in   ManifestCapabilitiesEmitSpec
		want string
	}{
		{"runtime-true", ManifestCapabilitiesEmitSpec{Runtime: true}, "\n[capabilities]\nruntime = true\n"},
		{"runtime-false", ManifestCapabilitiesEmitSpec{Runtime: false}, "\n[capabilities]\nruntime = false\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := RenderManifestCapabilitiesSection(c.in); got != c.want {
				t.Errorf("=\n%q\nwant\n%q", got, c.want)
			}
		})
	}
}
