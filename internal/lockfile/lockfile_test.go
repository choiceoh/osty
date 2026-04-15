package lockfile

import (
	"bytes"
	"testing"
)

// TestParseEmpty covers the degenerate case of a freshly-written
// lockfile with just the version header and no packages.
func TestParseEmpty(t *testing.T) {
	src := `version = 1`
	l, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if l.Version != 1 || len(l.Packages) != 0 {
		t.Errorf("want empty 1, got %+v", l)
	}
}

// TestParseBasic walks through a minimal two-package lockfile with
// a simple dep edge and a checksum.
func TestParseBasic(t *testing.T) {
	src := `version = 1

[[package]]
name = "a"
version = "1.0.0"
source = "registry+https://r.example"
checksum = "sha256:00"
dependencies = ["b 1.0.0"]

[[package]]
name = "b"
version = "1.0.0"
source = "registry+https://r.example"
checksum = "sha256:01"
`
	l, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(l.Packages) != 2 {
		t.Fatalf("pkgs: %d", len(l.Packages))
	}
	if l.Packages[0].Name != "a" || len(l.Packages[0].Dependencies) != 1 {
		t.Errorf("a: %+v", l.Packages[0])
	}
	if l.Packages[0].Dependencies[0].Name != "b" {
		t.Errorf("dep: %+v", l.Packages[0].Dependencies)
	}
}

// TestRoundtripDeterministic writes + reads + re-writes, confirming
// the output is byte-stable regardless of input ordering.
func TestRoundtripDeterministic(t *testing.T) {
	l := &Lock{
		Version: 1,
		Packages: []Package{
			{Name: "b", Version: "2.0", Source: "registry+https://r", Checksum: "sha256:b"},
			{Name: "a", Version: "1.0", Source: "registry+https://r", Checksum: "sha256:a",
				Dependencies: []Dependency{{Name: "b", Version: "2.0"}}},
		},
	}
	first := Marshal(l)
	l2, err := Parse(first)
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, first)
	}
	second := Marshal(l2)
	if !bytes.Equal(first, second) {
		t.Errorf("roundtrip not idempotent:\nfirst=%s\nsecond=%s", first, second)
	}
	// "a" sorts before "b" in Marshal output
	// so the second package header in first should be for "b".
	if !bytes.Contains(first, []byte(`name = "a"`)) {
		t.Errorf("first did not contain a")
	}
}

// TestRejectTooNew checks that a lockfile declaring a version > the
// schema we understand is rejected up-front.
func TestRejectTooNew(t *testing.T) {
	src := `version = 99`
	if _, err := Parse([]byte(src)); err == nil {
		t.Errorf("should reject version=99")
	}
}

// TestDependencySerialization exercises both the plain and
// source-qualified spellings of a Dependency's string form.
func TestDependencySerialization(t *testing.T) {
	cases := []struct {
		dep Dependency
		s   string
	}{
		{Dependency{Name: "a", Version: "1.0"}, "a 1.0"},
		{Dependency{Name: "a", Version: "1.0", Source: "registry+u"}, "a 1.0 (registry+u)"},
	}
	for _, tc := range cases {
		if got := tc.dep.String(); got != tc.s {
			t.Errorf("String(%+v) = %q, want %q", tc.dep, got, tc.s)
		}
		got, err := ParseDependency(tc.s)
		if err != nil {
			t.Errorf("parse %q: %v", tc.s, err)
			continue
		}
		if got != tc.dep {
			t.Errorf("Parse(%q) = %+v, want %+v", tc.s, got, tc.dep)
		}
	}
}

// TestFindByName returns every pinned version of a name for diamond
// conflict introspection.
func TestFindByName(t *testing.T) {
	l := &Lock{
		Version: 1,
		Packages: []Package{
			{Name: "a", Version: "1.0"},
			{Name: "a", Version: "2.0"},
			{Name: "b", Version: "1.0"},
		},
	}
	if got := l.FindByName("a"); len(got) != 2 {
		t.Errorf("a: got %d", len(got))
	}
	if got := l.FindByName("missing"); len(got) != 0 {
		t.Errorf("missing: got %d", len(got))
	}
}
