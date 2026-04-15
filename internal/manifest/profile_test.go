package manifest

import (
	"testing"
)

// TestParseProfileSection verifies that [profile.release] fields are
// parsed into *Profile with Has* flags set where declared, and that
// unset fields leave Has* false (so the downstream merge can fall
// back to the built-in default).
func TestParseProfileSection(t *testing.T) {
	src := `
[package]
name = "p"
version = "0.1.0"

[profile.release]
opt-level = 3
strip = true
go-flags = ["-race", "-trimpath"]

[profile.bench]
inherits = "release"
debug = true
`
	m, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(m.Profiles) != 2 {
		t.Fatalf("want 2 profiles, got %d", len(m.Profiles))
	}
	rel := m.Profiles["release"]
	if rel == nil {
		t.Fatalf("release profile missing")
	}
	if !rel.HasOptLevel || rel.OptLevel != 3 {
		t.Errorf("OptLevel: %+v", rel)
	}
	if !rel.HasStrip || !rel.Strip {
		t.Errorf("Strip: %+v", rel)
	}
	if rel.HasDebug {
		t.Errorf("Debug was not declared but HasDebug=true")
	}
	if len(rel.GoFlags) != 2 || rel.GoFlags[0] != "-race" {
		t.Errorf("GoFlags: %+v", rel.GoFlags)
	}
	bench := m.Profiles["bench"]
	if bench == nil || bench.Inherits != "release" {
		t.Errorf("bench.inherits: %+v", bench)
	}
	if !bench.HasDebug || !bench.Debug {
		t.Errorf("bench.debug: %+v", bench)
	}
}

// TestParseProfileEnv covers the optional env = {...} subtable.
func TestParseProfileEnv(t *testing.T) {
	src := `
[package]
name = "p"
version = "0.1.0"

[profile.debug]
env = { FOO = "1", BAR = "baz" }
`
	m, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	p := m.Profiles["debug"]
	if p == nil || len(p.Env) != 2 {
		t.Fatalf("env table not populated: %+v", p)
	}
	if p.Env["FOO"] != "1" || p.Env["BAR"] != "baz" {
		t.Errorf("env keys wrong: %+v", p.Env)
	}
}

// TestParseProfileBadType rejects non-boolean values for bool keys.
func TestParseProfileBadType(t *testing.T) {
	src := `
[package]
name = "p"
version = "0.1.0"

[profile.release]
strip = "yes"
`
	if _, err := Parse([]byte(src)); err == nil {
		t.Fatalf("expected type error, got nil")
	}
}

// TestParseProfileUnknownKey catches typos inside a profile table.
func TestParseProfileUnknownKey(t *testing.T) {
	src := `
[package]
name = "p"
version = "0.1.0"

[profile.release]
opt_level = 3
`
	if _, err := Parse([]byte(src)); err == nil {
		t.Fatalf("expected unknown-key error, got nil")
	}
}

// TestParseTargetSection handles [target.<triple>] tables.
func TestParseTargetSection(t *testing.T) {
	src := `
[package]
name = "p"
version = "0.1.0"

[target.amd64-linux]
cgo = false
env = { CC = "clang" }

[target.arm64-darwin]
`
	m, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(m.Targets) != 2 {
		t.Fatalf("want 2 targets, got %d", len(m.Targets))
	}
	var lin *Target
	for _, t := range m.Targets {
		if t.Triple == "amd64-linux" {
			lin = t
		}
	}
	if lin == nil {
		t.Fatalf("amd64-linux missing")
	}
	if !lin.HasCGO || lin.CGO {
		t.Errorf("CGO: %+v", lin)
	}
	if lin.Env["CC"] != "clang" {
		t.Errorf("env missing: %+v", lin.Env)
	}
}

// TestParseFeaturesSection covers [features] including the `default`
// array (which lands in DefaultFeatures, not Features) and regular
// features.
func TestParseFeaturesSection(t *testing.T) {
	src := `
[package]
name = "p"
version = "0.1.0"

[features]
default = ["tls"]
tls = ["crypto"]
net = ["async"]
async = []
crypto = []
`
	m, err := Parse([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(m.DefaultFeatures) != 1 || m.DefaultFeatures[0] != "tls" {
		t.Errorf("default features: %+v", m.DefaultFeatures)
	}
	if len(m.Features) != 4 {
		t.Errorf("expected 4 features (default stripped), got %d: %+v",
			len(m.Features), m.Features)
	}
	if m.Features["tls"][0] != "crypto" {
		t.Errorf("tls feature contents: %+v", m.Features["tls"])
	}
}
