package runner

import "testing"

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
