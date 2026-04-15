package manifest

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/diag"
)

// TestValidateProfileOptLevelRange asserts opt-level outside [0,3] is
// rejected with an error-severity diagnostic.
func TestValidateProfileOptLevelRange(t *testing.T) {
	src := `
[package]
name = "p"
version = "0.1.0"

[profile.release]
opt-level = 5
`
	m, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	diags := Validate(m)
	if !hasErrorContaining(diags, "opt-level") {
		t.Errorf("expected opt-level out-of-range error, got %v", diags)
	}
}

// TestValidateInheritsUnknown surfaces a missing inherits target.
func TestValidateInheritsUnknown(t *testing.T) {
	src := `
[package]
name = "p"
version = "0.1.0"

[profile.bench]
inherits = "ghost"
`
	m, _ := Parse([]byte(src))
	diags := Validate(m)
	if !hasErrorContaining(diags, "ghost") {
		t.Errorf("expected unknown-inherits error, got %v", diags)
	}
}

// TestValidateInheritsCycle catches A -> B -> A cycles in the
// declared profile graph.
func TestValidateInheritsCycle(t *testing.T) {
	src := `
[package]
name = "p"
version = "0.1.0"

[profile.a]
inherits = "b"

[profile.b]
inherits = "a"
`
	m, _ := Parse([]byte(src))
	diags := Validate(m)
	if !hasErrorContaining(diags, "cycle") {
		t.Errorf("expected cycle error, got %v", diags)
	}
}

// TestValidateInheritsBuiltinAccepted confirms that inheriting from a
// built-in name (release) is fine even though it isn't in m.Profiles.
func TestValidateInheritsBuiltinAccepted(t *testing.T) {
	src := `
[package]
name = "p"
version = "0.1.0"

[profile.bench]
inherits = "release"
opt-level = 3
`
	m, _ := Parse([]byte(src))
	for _, d := range Validate(m) {
		if d.Severity == diag.Error {
			t.Errorf("unexpected error: %v", d.Message)
		}
	}
}

// TestValidateTargetTriple rejects malformed triples.
func TestValidateTargetTriple(t *testing.T) {
	src := `
[package]
name = "p"
version = "0.1.0"

[target.amd64]
cgo = false
`
	m, _ := Parse([]byte(src))
	diags := Validate(m)
	if !hasErrorContaining(diags, "triple") {
		t.Errorf("expected triple error, got %v", diags)
	}
}

// TestValidateFeaturesDefaultUndefined catches a default that
// references a feature the manifest never declared.
func TestValidateFeaturesDefaultUndefined(t *testing.T) {
	src := `
[package]
name = "p"
version = "0.1.0"

[features]
default = ["nope"]
real = []
`
	m, _ := Parse([]byte(src))
	diags := Validate(m)
	if !hasErrorContaining(diags, "nope") {
		t.Errorf("expected undefined-default error, got %v", diags)
	}
}

// TestValidateFeatureSelfReference catches `tls = ["tls"]`.
func TestValidateFeatureSelfReference(t *testing.T) {
	src := `
[package]
name = "p"
version = "0.1.0"

[features]
tls = ["tls"]
`
	m, _ := Parse([]byte(src))
	diags := Validate(m)
	if !hasErrorContaining(diags, "lists itself") {
		t.Errorf("expected self-reference error, got %v", diags)
	}
}

// hasErrorContaining is a small helper — fewer scaffolding lines per
// case keeps the table-driven feel above readable.
func hasErrorContaining(diags []*diag.Diagnostic, substr string) bool {
	for _, d := range diags {
		if d.Severity == diag.Error && strings.Contains(d.Message, substr) {
			return true
		}
	}
	return false
}
