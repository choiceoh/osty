package manifest

import (
	"bytes"
	"strings"
	"testing"
)

// TestParseMinimal asserts the smallest valid manifest (just
// package.name + package.version) parses and that optional keys
// default sensibly.
func TestParseMinimal(t *testing.T) {
	src := `
[package]
name = "hello"
version = "0.1.0"
`
	m, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.Package.Name != "hello" {
		t.Errorf("name: got %q", m.Package.Name)
	}
	if m.Package.Version != "0.1.0" {
		t.Errorf("version: got %q", m.Package.Version)
	}
	if len(m.Dependencies) != 0 {
		t.Errorf("deps: got %d, want 0", len(m.Dependencies))
	}
}

// TestParseFullPackage covers every documented [package] field plus
// arrays (authors, keywords).
func TestParseFullPackage(t *testing.T) {
	src := `
[package]
name = "my-app"
version = "1.2.3"
edition = "0.3"
description = "a thing"
authors = ["Alice <a@x>", "Bob"]
license = "MIT"
repository = "https://github.com/x/y"
homepage = "https://example.com"
keywords = ["cli", "demo"]
`
	m, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	p := m.Package
	if p.Description != "a thing" || p.License != "MIT" || p.Edition != "0.3" {
		t.Errorf("missing field: %+v", p)
	}
	if len(p.Authors) != 2 || p.Authors[0] != "Alice <a@x>" {
		t.Errorf("authors: %+v", p.Authors)
	}
	if len(p.Keywords) != 2 || p.Keywords[1] != "demo" {
		t.Errorf("keywords: %+v", p.Keywords)
	}
}

// TestParseDepsShortAndLong exercises both dependency spellings:
// `name = "1.0"` and `name = { version = "1.0" }`.
func TestParseDepsShortAndLong(t *testing.T) {
	src := `
[package]
name = "a"
version = "0.1.0"

[dependencies]
simple = "1.0"
with-path = { path = "../bar" }
with-git = { git = "https://github.com/x/y", tag = "v1" }
long = { version = "2.0", features = ["a", "b"], optional = true, default-features = false }
`
	m, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(m.Dependencies) != 4 {
		t.Fatalf("deps: got %d", len(m.Dependencies))
	}
	byName := map[string]Dependency{}
	for _, d := range m.Dependencies {
		byName[d.Name] = d
	}
	if byName["simple"].VersionReq != "1.0" {
		t.Errorf("simple: %+v", byName["simple"])
	}
	if byName["with-path"].Path != "../bar" {
		t.Errorf("with-path: %+v", byName["with-path"])
	}
	g := byName["with-git"].Git
	if g == nil || g.URL != "https://github.com/x/y" || g.Tag != "v1" {
		t.Errorf("with-git: %+v", g)
	}
	long := byName["long"]
	if long.VersionReq != "2.0" || !long.Optional || long.DefaultFeats ||
		len(long.Features) != 2 {
		t.Errorf("long: %+v", long)
	}
}

// TestParseDevDeps covers the [dev-dependencies] section alongside
// the regular [dependencies] one. They must coexist without aliasing.
func TestParseDevDeps(t *testing.T) {
	src := `
[package]
name = "a"
version = "0.1.0"

[dependencies]
a = "1"

[dev-dependencies]
b = "2"
`
	m, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(m.Dependencies) != 1 || m.Dependencies[0].Name != "a" {
		t.Errorf("deps: %+v", m.Dependencies)
	}
	if len(m.DevDependencies) != 1 || m.DevDependencies[0].Name != "b" {
		t.Errorf("dev: %+v", m.DevDependencies)
	}
}

// TestParseBin exercises the single-target [bin] form. Multi-binary
// support ([[bin]] array-of-tables) is out of scope for now.
func TestParseBin(t *testing.T) {
	src := `
[package]
name = "a"
version = "0.1.0"

[bin]
name = "tool"
path = "src/tool.osty"
`
	m, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.Bin == nil || m.Bin.Name != "tool" || m.Bin.Path != "src/tool.osty" {
		t.Errorf("bin: %+v", m.Bin)
	}
}

// TestParseMissingRequired flags missing name/version when [package]
// IS present. An empty or workspace-only manifest is allowed (§13.2
// doesn't require [package] in virtual workspaces).
func TestParseMissingRequired(t *testing.T) {
	cases := []struct{ name, src string }{
		{"no name", `[package]
version = "0.1.0"`},
		{"no version", `[package]
name = "a"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse([]byte(tc.src)); err == nil {
				t.Errorf("expected error for %q", tc.name)
			}
		})
	}
}

// TestParseBadDep covers the three invariants enforced on deps:
// exactly one source, single git ref, known keys only.
func TestParseBadDep(t *testing.T) {
	cases := []struct {
		name string
		dep  string
	}{
		{"no source", `foo = {}`},
		{"two sources", `foo = { version = "1", path = "../bar" }`},
		{"two git refs", `foo = { git = "u", tag = "v", branch = "b" }`},
		{"unknown key", `foo = { version = "1", weird = true }`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "[package]\nname = \"a\"\nversion = \"0\"\n[dependencies]\n" + tc.dep
			if _, err := Parse([]byte(src)); err == nil {
				t.Errorf("expected error for %q", tc.name)
			}
		})
	}
}

// TestRoundtripIdentity asserts that marshal→parse gives back the
// same Manifest for a realistic input. A stricter byte-level
// idempotency is not promised (whitespace / key order may differ) but
// semantic content must match.
func TestRoundtripIdentity(t *testing.T) {
	m := &Manifest{
		Package: Package{
			Name:        "abc",
			Version:     "1.0.0",
			Edition:     "0.3",
			Description: "hi",
			Authors:     []string{"A <a@b>"},
			License:     "MIT",
			Keywords:    []string{"x", "y"},
		},
		Dependencies: []Dependency{
			{Name: "simple", VersionReq: "1.0", DefaultFeats: true},
			{Name: "pathdep", Path: "../sibling", DefaultFeats: true},
			{Name: "gitdep", Git: &GitSource{URL: "u", Tag: "v1"}, DefaultFeats: true},
			{Name: "complex", VersionReq: "2", Features: []string{"a", "b"}, Optional: true, DefaultFeats: false},
		},
	}
	raw := Marshal(m)
	m2, err := Parse(raw)
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, raw)
	}
	if m2.Package.Name != m.Package.Name || m2.Package.Version != m.Package.Version {
		t.Errorf("package drift: %+v vs %+v", m2.Package, m.Package)
	}
	if len(m2.Dependencies) != len(m.Dependencies) {
		t.Fatalf("deps drift: %d vs %d", len(m2.Dependencies), len(m.Dependencies))
	}
	// Marshal sorts deps by name, so compare as name-indexed maps
	// instead of positionally.
	want := map[string]Dependency{}
	for _, d := range m.Dependencies {
		want[d.Name] = d
	}
	for _, a := range m2.Dependencies {
		b, ok := want[a.Name]
		if !ok {
			t.Errorf("unexpected dep %q after roundtrip", a.Name)
			continue
		}
		if a.VersionReq != b.VersionReq || a.Path != b.Path {
			t.Errorf("dep %q drift: %+v vs %+v", a.Name, a, b)
		}
	}
}

// TestParseLintSection verifies the [lint] table round-trips allow/deny
// as plain string arrays.
func TestParseLintSection(t *testing.T) {
	src := []byte(`[package]
name = "x"
version = "0.1.0"

[lint]
allow = ["naming_value", "L0003"]
deny  = ["dead_code"]
`)
	m, err := Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.Lint == nil {
		t.Fatalf("Lint should be non-nil when [lint] is present")
	}
	if len(m.Lint.Allow) != 2 || m.Lint.Allow[0] != "naming_value" {
		t.Errorf("Allow drift: %+v", m.Lint.Allow)
	}
	if len(m.Lint.Deny) != 1 || m.Lint.Deny[0] != "dead_code" {
		t.Errorf("Deny drift: %+v", m.Lint.Deny)
	}

	// Round-trip.
	raw := Marshal(m)
	m2, err := Parse(raw)
	if err != nil {
		t.Fatalf("roundtrip parse: %v\n%s", err, raw)
	}
	if m2.Lint == nil {
		t.Fatalf("Lint lost after round-trip")
	}
}

func TestParseLintRejectsUnknownKey(t *testing.T) {
	src := []byte(`[package]
name = "x"
version = "0.1.0"

[lint]
warn = ["foo"]
`)
	if _, err := Parse(src); err == nil {
		t.Fatal("expected error for unknown [lint] key")
	}
}

// TestMarshalShortForm confirms we emit `dep = "1.0"` (not the long
// inline-table form) when nothing else is set.
func TestMarshalShortForm(t *testing.T) {
	m := &Manifest{
		Package: Package{Name: "a", Version: "0.1.0"},
		Dependencies: []Dependency{
			{Name: "s", VersionReq: "1.0", DefaultFeats: true},
		},
	}
	raw := Marshal(m)
	if !bytes.Contains(raw, []byte(`s = "1.0"`)) {
		t.Errorf("short form missing:\n%s", raw)
	}
	if bytes.Contains(raw, []byte("version = \"1.0\"")) {
		t.Errorf("unexpected long form:\n%s", raw)
	}
}

// TestCommentAndWhitespace exercises the TOML parser's tolerance for
// comments, blank lines, and indentation.
func TestCommentAndWhitespace(t *testing.T) {
	src := `
# top comment

[package]  # inline comment
name = "x"   # trailing
version = "0.1.0"

# before deps

[dependencies]
  a = "1"
`
	m, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.Package.Name != "x" || len(m.Dependencies) != 1 {
		t.Errorf("fields: %+v", m)
	}
}

// TestParseRegistries covers the [registries.<name>] subtable shape.
func TestParseRegistries(t *testing.T) {
	src := `
[package]
name = "a"
version = "0"

[registries.custom]
url = "https://custom.example"
`
	m, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(m.Registries) != 1 {
		t.Fatalf("reg: %+v", m.Registries)
	}
	if r := m.Registries[0]; r.Name != "custom" || r.URL != "https://custom.example" {
		t.Errorf("reg: %+v", r)
	}
}

// TestParseErrorLocation asserts error messages include a line
// number so misconfiguration is debuggable.
func TestParseErrorLocation(t *testing.T) {
	src := `
[package]
name = "a"
version = "1"
description = no-quotes
`
	_, err := Parse([]byte(src))
	if err == nil || !strings.Contains(err.Error(), ":5:") {
		t.Fatalf("expected :5: in %v", err)
	}
}
