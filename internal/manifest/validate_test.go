package manifest

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/diag"
)

// TestValidateAcceptsGoodManifest confirms a well-formed package
// manifest produces zero error-severity diagnostics. Warnings may
// still be returned (e.g. missing edition), so the assertion is
// specifically about errors.
func TestValidateAcceptsGoodManifest(t *testing.T) {
	m, err := Parse([]byte(`
[package]
name = "myapp"
version = "0.1.0"
edition = "0.3"
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	diags := Validate(m)
	for _, d := range diags {
		if d.Severity == diag.Error {
			t.Errorf("unexpected error diagnostic: %s", d.Error())
		}
	}
}

// TestValidateMissingEditionWarns checks the soft-miss behavior:
// missing edition produces a warning (not error) with a fix hint.
// Rationale: pre-edition manifests should still load so users aren't
// locked out of upgrading their tooling.
func TestValidateMissingEditionWarns(t *testing.T) {
	m, err := Parse([]byte(`
[package]
name = "noedition"
version = "1.0.0"
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	diags := Validate(m)
	var gotWarn bool
	for _, d := range diags {
		if d.Severity == diag.Warning && d.Code == diag.CodeManifestMissingField {
			gotWarn = true
			if d.Hint == "" {
				t.Errorf("missing-edition warning has no hint")
			}
		}
		if d.Severity == diag.Error {
			t.Errorf("missing edition should be a warning, got error: %s", d.Error())
		}
	}
	if !gotWarn {
		t.Errorf("expected missing-edition warning; got %d diag(s)", len(diags))
	}
}

// TestValidateBadNameFormat asserts names that don't match the
// identifier-ish regex are rejected with CodeManifestBadName.
func TestValidateBadNameFormat(t *testing.T) {
	cases := []string{
		"1leading-digit",
		"has space",
		"has.dot",
		"한글",
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			src := `
[package]
name = "` + name + `"
version = "0.1.0"
edition = "0.3"
`
			m, err := Parse([]byte(src))
			if err != nil {
				// Some of these might even fail at parse (e.g. unknown
				// escape); that's fine — the test only cares that invalid
				// names don't slip through. Skip.
				t.Skipf("parse rejected input: %v", err)
			}
			diags := Validate(m)
			if !containsCode(diags, diag.CodeManifestBadName) {
				t.Errorf("want E2014 (bad name) in diags; got: %v", codes(diags))
			}
		})
	}
}

// TestValidateBadSemver rejects versions outside strict X.Y.Z(-pre)(+build).
func TestValidateBadSemver(t *testing.T) {
	cases := []string{
		"1",
		"1.2",
		"1.2.x",
		"v1.0.0", // leading `v` not allowed in the stored version
		"01.2.3", // leading zero per semver 2.0 is invalid
	}
	for _, v := range cases {
		t.Run(v, func(t *testing.T) {
			src := `
[package]
name = "abc"
version = "` + v + `"
edition = "0.3"
`
			m, err := Parse([]byte(src))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			diags := Validate(m)
			if !containsCode(diags, diag.CodeManifestBadVersion) {
				t.Errorf("want E2015 (bad version) for %q; got: %v", v, codes(diags))
			}
		})
	}
}

// TestValidateUnknownEdition rejects editions that aren't in the
// KnownEditions table. This is the mechanism by which tooling refuses
// to build a project from a future spec version it doesn't understand.
func TestValidateUnknownEdition(t *testing.T) {
	m, err := Parse([]byte(`
[package]
name = "abc"
version = "1.0.0"
edition = "99.0"
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	diags := Validate(m)
	if !containsCode(diags, diag.CodeManifestBadEdition) {
		t.Errorf("want E2016 (bad edition); got: %v", codes(diags))
	}
}

// TestValidateWorkspaceEmpty asserts a [workspace] with no members
// fails validation — such a manifest would resolve to zero packages
// and almost certainly represents an author mistake.
func TestValidateWorkspaceEmpty(t *testing.T) {
	m, err := Parse([]byte(`
[workspace]
members = []
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	diags := Validate(m)
	if !containsCode(diags, diag.CodeManifestWorkspaceEmpty) {
		t.Errorf("want E2018 (empty workspace); got: %v", codes(diags))
	}
}

// TestValidateWorkspaceAcceptsOnlyWorkspace checks that a manifest
// with only [workspace] (no [package]) is valid — virtual workspaces
// are a supported shape.
func TestValidateWorkspaceAcceptsOnlyWorkspace(t *testing.T) {
	m, err := Parse([]byte(`
[workspace]
members = ["a", "b"]
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.HasPackage {
		t.Errorf("virtual workspace should have HasPackage=false")
	}
	if m.Workspace == nil || len(m.Workspace.Members) != 2 {
		t.Errorf("members not parsed: %+v", m.Workspace)
	}
	diags := Validate(m)
	for _, d := range diags {
		if d.Severity == diag.Error {
			t.Errorf("virtual workspace produced error: %s", d.Error())
		}
	}
}

// TestParseDiagnosticsCarriesCode confirms that ParseDiagnostics routes
// a raw Parse error into a Diagnostic with a specific E2xxx code —
// not just the generic CodeManifestSyntax fallback.
func TestParseDiagnosticsCarriesCode(t *testing.T) {
	// Missing required field `name` — should map to CodeManifestMissingField.
	src := `
[package]
version = "0.1.0"
`
	_, diags := ParseDiagnostics([]byte(src), "osty.toml")
	if len(diags) == 0 {
		t.Fatalf("want at least one diagnostic")
	}
	if diags[0].Code != diag.CodeManifestMissingField {
		t.Errorf("code = %q, want %q (E2011)", diags[0].Code, diag.CodeManifestMissingField)
	}
	if diags[0].PrimaryPos().Line == 0 {
		t.Errorf("diagnostic has no source line")
	}
}

// TestParseDiagnosticsBadDep confirms unknown-key-in-dep routes to
// CodeManifestUnknownKey (E2012) — one of the more useful
// finer-grained codes.
func TestParseDiagnosticsBadDep(t *testing.T) {
	src := `
[package]
name = "a"
version = "1.0.0"

[dependencies]
foo = { version = "1", weird = true }
`
	_, diags := ParseDiagnostics([]byte(src), "osty.toml")
	if len(diags) == 0 || diags[0].Code != diag.CodeManifestUnknownKey {
		t.Errorf("code = %v, want %q", codes(diags), diag.CodeManifestUnknownKey)
	}
}

// TestParseDiagnosticsOKReturnsNoErrors guarantees a well-formed
// manifest returns a non-nil *Manifest and zero diagnostics.
func TestParseDiagnosticsOKReturnsNoErrors(t *testing.T) {
	m, diags := ParseDiagnostics([]byte(`
[package]
name = "x"
version = "0.1.0"
`), "osty.toml")
	if m == nil {
		t.Errorf("manifest is nil")
	}
	if len(diags) != 0 {
		t.Errorf("unexpected diagnostics: %v", diags)
	}
}

// TestParseDiagUnterminatedString maps the parser's "unterminated
// string" error to CodeManifestUnterminated (E2001).
func TestParseDiagUnterminatedString(t *testing.T) {
	src := `[package]
name = "oops
version = "0.1.0"
`
	_, diags := ParseDiagnostics([]byte(src), "osty.toml")
	if len(diags) == 0 || diags[0].Code != diag.CodeManifestUnterminated {
		t.Errorf("code = %v, want %q", codes(diags), diag.CodeManifestUnterminated)
	}
}

// TestParseDiagDuplicateKey maps "duplicate key" to E2002.
func TestParseDiagDuplicateKey(t *testing.T) {
	src := `[package]
name = "a"
name = "b"
version = "0.1.0"
`
	_, diags := ParseDiagnostics([]byte(src), "osty.toml")
	if len(diags) == 0 || diags[0].Code != diag.CodeManifestDuplicateKey {
		t.Errorf("code = %v, want %q", codes(diags), diag.CodeManifestDuplicateKey)
	}
}

// TestParseDiagBadEscape maps "unknown escape" to E2004.
func TestParseDiagBadEscape(t *testing.T) {
	src := `[package]
name = "\q"
version = "0.1.0"
`
	_, diags := ParseDiagnostics([]byte(src), "osty.toml")
	if len(diags) == 0 || diags[0].Code != diag.CodeManifestBadEscape {
		t.Errorf("code = %v, want %q", codes(diags), diag.CodeManifestBadEscape)
	}
}

// TestLoadMissingFileReturnsNotFoundCode: Load should return an
// E2030 diagnostic — not an os.PathError surfaced as loadErr — when
// the manifest file isn't there. That distinction matters for CLI
// callers: they treat E2030 as a polite "run `osty init`" prompt
// rather than a hard I/O failure.
func TestLoadMissingFileReturnsNotFoundCode(t *testing.T) {
	_, diags, _ := Load("/does-not-exist/osty.toml")
	var found *diag.Diagnostic
	for _, d := range diags {
		if d.Code == diag.CodeManifestNotFound {
			found = d
			break
		}
	}
	if found == nil {
		t.Errorf("no CodeManifestNotFound; got: %v", codes(diags))
	} else if found.Hint == "" {
		t.Errorf("not-found diagnostic carries no hint")
	}
}

// TestValidateBadVersionReq asserts that a nonsense version
// requirement in [dependencies] surfaces as CodeManifestBadDepSpec
// during validation — before `osty build` tries to resolve it.
// Examples of what should be rejected: random garbage, malformed
// operators, or incomplete ranges.
func TestValidateBadVersionReq(t *testing.T) {
	cases := []string{
		"not-semver",
		"^",
		">=",
		"1.2.x.y",
	}
	for _, bad := range cases {
		t.Run(bad, func(t *testing.T) {
			src := `
[package]
name = "a"
version = "1.0.0"
edition = "0.3"

[dependencies]
foo = "` + bad + `"
`
			m, err := Parse([]byte(src))
			if err != nil {
				// Some strings might fail at parse before even reaching
				// Validate (e.g. quoting weirdness) — that's acceptable.
				t.Skipf("parser rejected the input: %v", err)
			}
			diags := Validate(m)
			if !containsCode(diags, diag.CodeManifestBadDepSpec) {
				t.Errorf("want E2017 for version req %q; got %v", bad, codes(diags))
			}
		})
	}
}

// TestValidateAcceptsGoodVersionReq is the inverse: a spread of
// well-formed requirement strings should NOT produce E2017.
func TestValidateAcceptsGoodVersionReq(t *testing.T) {
	cases := []string{
		"1.0.0",
		"^1.2",
		"*",
	}
	for _, good := range cases {
		t.Run(good, func(t *testing.T) {
			src := `
[package]
name = "a"
version = "1.0.0"
edition = "0.3"

[dependencies]
foo = "` + good + `"
`
			m, err := Parse([]byte(src))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			diags := Validate(m)
			for _, d := range diags {
				if d.Code == diag.CodeManifestBadDepSpec {
					t.Errorf("unexpected E2017 for %q: %s", good, d.Error())
				}
			}
		})
	}
}

// TestMarshalWorkspace round-trips a manifest with [workspace] members.
func TestMarshalWorkspace(t *testing.T) {
	m := &Manifest{
		HasPackage: true,
		Package:    Package{Name: "root", Version: "0.1.0", Edition: "0.3"},
		Workspace:  &Workspace{Members: []string{"pkg/a", "pkg/b"}},
	}
	raw := Marshal(m)
	if !strings.Contains(string(raw), "[workspace]") {
		t.Errorf("workspace header missing:\n%s", raw)
	}
	if !strings.Contains(string(raw), `"pkg/a"`) {
		t.Errorf("member pkg/a missing:\n%s", raw)
	}
	// Roundtrip check.
	m2, err := Parse(raw)
	if err != nil {
		t.Fatalf("roundtrip parse: %v", err)
	}
	if m2.Workspace == nil || len(m2.Workspace.Members) != 2 {
		t.Errorf("workspace lost on roundtrip: %+v", m2.Workspace)
	}
}

// ---- helpers ----

func containsCode(ds []*diag.Diagnostic, code string) bool {
	for _, d := range ds {
		if d.Code == code {
			return true
		}
	}
	return false
}

func codes(ds []*diag.Diagnostic) []string {
	var cs []string
	for _, d := range ds {
		cs = append(cs, d.Code)
	}
	return cs
}
