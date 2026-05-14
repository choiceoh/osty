package runner

import "testing"

func TestDependencyString(t *testing.T) {
	cases := []struct {
		name, version, source string
		want                  string
	}{
		{"serde", "1.2.3", "", "serde 1.2.3"},
		{"serde", "1.2.3", "git+https://github.com/x/y", "serde 1.2.3 (git+https://github.com/x/y)"},
	}
	for _, c := range cases {
		if got := DependencyString(c.name, c.version, c.source); got != c.want {
			t.Errorf("DependencyString = %q, want %q", got, c.want)
		}
	}
}

func TestParseDependency(t *testing.T) {
	cases := []struct {
		name        string
		in          string
		wantOk      bool
		wantName    string
		wantVersion string
		wantSource  string
	}{
		{"two-fields", "serde 1.2.3", true, "serde", "1.2.3", ""},
		{"with-source", "serde 1.2.3 (path:./vendor/serde)", true, "serde", "1.2.3", "path:./vendor/serde"},
		{"trims-leading-trailing", "  serde 1.2.3  ", true, "serde", "1.2.3", ""},
		{"reject-single-field", "serde", false, "", "", ""},
		{"reject-too-many-fields", "serde 1.2.3 extra", false, "", "", ""},
		{"reject-empty", "", false, "", "", ""},
		{"reject-whitespace-only", "   ", false, "", "", ""},
		{
			"source-with-embedded-parens",
			"crate 0.1.0 (registry+(local))",
			true, "crate", "0.1.0", "registry+(local)",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ParseDependency(c.in)
			if got.Ok != c.wantOk || got.Name != c.wantName ||
				got.Version != c.wantVersion || got.Source != c.wantSource {
				t.Errorf("ParseDependency(%q) = %+v, want {%q %q %q %v}",
					c.in, got, c.wantName, c.wantVersion, c.wantSource, c.wantOk)
			}
		})
	}
}

func TestParseDependencyRoundTrip(t *testing.T) {
	original := "tokio 1.5.0 (git+https://github.com/tokio-rs/tokio)"
	p := ParseDependency(original)
	if !p.Ok {
		t.Fatalf("parse failed for %q", original)
	}
	if got := DependencyString(p.Name, p.Version, p.Source); got != original {
		t.Errorf("round-trip = %q, want %q", got, original)
	}
}
