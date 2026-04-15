package ci

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/osty/osty/internal/diag"
)

// writeFile is a tiny test helper: panics on error so call sites
// stay short. Each test calls t.Fatal via the wrapping closure if
// something goes wrong.
func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// TestFormatCheckPasses captures the happy path: a file produced
// by `format.Source` round-trips cleanly, so the format check
// must report no diagnostics.
func TestFormatCheckPasses(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "osty.toml", `[package]
name = "demo"
version = "0.1.0"
edition = "0.3"
license = "MIT"
description = "demo project"
`)
	writeFile(t, dir, "lib.osty", "pub fn hello() {}\n")

	r := NewRunner(dir, Options{Format: true})
	if err := r.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	rep := r.Run()
	c := findCheck(rep, CheckFormat)
	if !c.Passed {
		t.Fatalf("format check failed; diags=%v", diagMsgs(c.Diags))
	}
}

// TestFormatCheckCatchesUnformatted writes a file with extra
// whitespace the canonical formatter would normalise. The check
// must emit CI003.
func TestFormatCheckCatchesUnformatted(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "osty.toml", `[package]
name = "demo"
version = "0.1.0"
`)
	// Four leading spaces on a declaration is non-canonical.
	writeFile(t, dir, "lib.osty", "    pub fn hello() {}\n")

	r := NewRunner(dir, Options{Format: true})
	if err := r.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	rep := r.Run()
	c := findCheck(rep, CheckFormat)
	if c.Passed {
		t.Fatalf("format check should have failed")
	}
	if !containsCode(c.Diags, "CI003") {
		t.Fatalf("expected CI003; got %v", diagMsgs(c.Diags))
	}
}

// TestPolicyFlagsMissingLicense confirms the policy pass warns
// when license is missing and errors when the workspace member
// points at a nonexistent dir.
func TestPolicyFlagsMissingLicense(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "osty.toml", `[package]
name = "demo"
version = "0.1.0"
`)
	writeFile(t, dir, "lib.osty", "pub fn hello() {}\n")

	r := NewRunner(dir, Options{Policy: true})
	if err := r.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	rep := r.Run()
	c := findCheck(rep, CheckPolicy)
	if !containsCode(c.Diags, "CI105") {
		t.Fatalf("expected CI105 for missing license; got %v", diagMsgs(c.Diags))
	}
}

// TestPolicyBadWorkspaceMember is the analogue for workspace
// members: a declared member that doesn't exist on disk is CI112.
func TestPolicyBadWorkspaceMember(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "osty.toml", `[workspace]
members = ["missing"]
`)
	r := NewRunner(dir, Options{Policy: true})
	if err := r.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	rep := r.Run()
	c := findCheck(rep, CheckPolicy)
	if !containsCode(c.Diags, "CI112") {
		t.Fatalf("expected CI112 for missing member; got %v", diagMsgs(c.Diags))
	}
	if c.Passed {
		t.Fatalf("policy check should have failed")
	}
}

// TestLockfileMissing covers the straightforward case: a project
// with declared deps but no osty.lock must produce CI202.
func TestLockfileMissing(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "osty.toml", `[package]
name = "demo"
version = "0.1.0"

[dependencies]
other = "1.0"
`)
	writeFile(t, dir, "lib.osty", "pub fn hello() {}\n")
	r := NewRunner(dir, Options{Lockfile: true})
	if err := r.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	rep := r.Run()
	c := findCheck(rep, CheckLockfile)
	if !containsCode(c.Diags, "CI202") {
		t.Fatalf("expected CI202; got %v", diagMsgs(c.Diags))
	}
}

// TestReleaseBlocksPathDeps verifies the release check promotes
// a path dep to an error — the exact constraint that makes a
// package publishable.
func TestReleaseBlocksPathDeps(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "osty.toml", `[package]
name = "demo"
version = "0.1.0"
license = "MIT"

[dependencies]
local = { path = "../local" }
`)
	writeFile(t, dir, "lib.osty", "pub fn hello() {}\n")
	r := NewRunner(dir, Options{Release: true})
	if err := r.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	rep := r.Run()
	c := findCheck(rep, CheckRelease)
	if !containsCode(c.Diags, "CI306") {
		t.Fatalf("expected CI306; got %v", diagMsgs(c.Diags))
	}
}

// TestSnapshotRoundTrip captures, writes, reads, and compares a
// snapshot — the full path of the semver diffing primitive.
func TestSnapshotRoundTrip(t *testing.T) {
	base := &Snapshot{
		Schema:  SnapshotSchemaVersion,
		Package: "demo",
		Version: "0.1.0",
		Symbols: []Symbol{
			{Name: "Greet", Kind: "function"},
			{Name: "User", Kind: "struct"},
		},
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "api.json")
	if err := WriteSnapshot(path, base); err != nil {
		t.Fatalf("WriteSnapshot: %v", err)
	}
	got, err := ReadSnapshot(path)
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	// Pretty-print both to JSON to check structural equality —
	// avoids depending on slice ordering internal to the struct.
	bbs, _ := json.Marshal(base)
	gbs, _ := json.Marshal(got)
	if string(bbs) != string(gbs) {
		t.Fatalf("snapshot round-trip mismatch:\nwant %s\ngot  %s", bbs, gbs)
	}
}

// TestSnapshotCompareDetectsRemoval is the heart of the semver
// check: a symbol present in baseline but missing from current is
// classified as Removed.
func TestSnapshotCompareDetectsRemoval(t *testing.T) {
	base := &Snapshot{Symbols: []Symbol{
		{Name: "Keep", Kind: "function"},
		{Name: "Drop", Kind: "function"},
	}}
	cur := &Snapshot{Symbols: []Symbol{
		{Name: "Keep", Kind: "function"},
		{Name: "New", Kind: "struct"},
	}}
	d := Compare(base, cur)
	if len(d.Removed) != 1 || d.Removed[0].Name != "Drop" {
		t.Fatalf("removed wrong: %+v", d.Removed)
	}
	if len(d.Added) != 1 || d.Added[0].Name != "New" {
		t.Fatalf("added wrong: %+v", d.Added)
	}
	if len(d.Changed) != 0 {
		t.Fatalf("changed should be empty: %+v", d.Changed)
	}
}

// TestMajorBumpedSuppressesError verifies breakages downgrade to
// Warning when the major version was bumped — the covenant
// semver.org codifies and CI enforces.
func TestMajorBumpedSuppressesError(t *testing.T) {
	if !majorBumped("0.1.0", "1.0.0") {
		t.Fatalf("1.0.0 after 0.1.0 should count as major bump")
	}
	if majorBumped("1.0.0", "1.2.0") {
		t.Fatalf("minor bump incorrectly flagged as major")
	}
	if majorBumped("invalid", "1.0.0") {
		t.Fatalf("unparseable version should disable the bump heuristic")
	}
}

// findCheck fetches a check by name from a Report or fails the
// test if the check is missing.
func findCheck(rep *Report, name CheckName) *Check {
	for _, c := range rep.Checks {
		if c.Name == name {
			return c
		}
	}
	return &Check{Name: name}
}

// containsCode reports whether any diagnostic in ds carries the
// given code. Used in assertions instead of substring-matching on
// messages so copy edits to the text don't break the tests.
func containsCode(ds []*diag.Diagnostic, code string) bool {
	for _, d := range ds {
		if d.Code == code {
			return true
		}
	}
	return false
}

// diagMsgs renders a list of diagnostics as a flat string for
// test failure output. Avoids pulling in the real formatter
// (which wants source bytes) inside unit tests.
func diagMsgs(ds []*diag.Diagnostic) []string {
	out := make([]string, 0, len(ds))
	for _, d := range ds {
		out = append(out, d.Code+": "+d.Message)
	}
	return out
}
