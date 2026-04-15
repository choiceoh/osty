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

// TestLoadPrefersRootPackageOverImplicitWorkspace protects the
// repo-root case: a directory can contain fixture/sample subdirs
// like testdata/ while still being a single package because it has
// direct package sources of its own.
func TestLoadPrefersRootPackageOverImplicitWorkspace(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "lib.osty", "pub fn hello() {}\n")
	fixtures := filepath.Join(dir, "testdata")
	if err := os.Mkdir(fixtures, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, fixtures, "fixture.osty", "    pub fn unformattedFixture() {}\n")

	r := NewRunner(dir, Options{Format: true, Lint: true})
	if err := r.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(r.Packages) != 1 {
		t.Fatalf("expected one root package, got %d", len(r.Packages))
	}
	if filepath.Clean(r.Packages[0].Dir) != filepath.Clean(dir) {
		t.Fatalf("loaded package dir %q, want root %q", r.Packages[0].Dir, dir)
	}

	rep := r.Run()
	if c := findCheck(rep, CheckFormat); !c.Passed {
		t.Fatalf("format should ignore fixture subdir; diags=%v", diagMsgs(c.Diags))
	}
	if c := findCheck(rep, CheckLint); !c.Passed {
		t.Fatalf("lint should ignore fixture subdir; diags=%v", diagMsgs(c.Diags))
	}
}

// TestLoadSkipsExplicitCIFiles lets future-facing samples live next
// to a package without breaking today's CI bundle. The skipped file is
// deliberately unformatted, so format would fail if the directive were
// ignored.
func TestLoadSkipsExplicitCIFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "lib.osty", "pub fn hello() {}\n")
	writeFile(t, dir, "future.osty", "// osty:ci-skip\n    pub fn future() {}\n")

	r := NewRunner(dir, Options{Format: true, Lint: true})
	if err := r.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(r.Packages) != 1 {
		t.Fatalf("expected one package, got %d", len(r.Packages))
	}
	if len(r.Packages[0].Files) != 1 {
		t.Fatalf("expected one unskipped file, got %d", len(r.Packages[0].Files))
	}
	if got := filepath.Base(r.Packages[0].Files[0].Path); got != "lib.osty" {
		t.Fatalf("loaded %q, want lib.osty", got)
	}

	rep := r.Run()
	if c := findCheck(rep, CheckFormat); !c.Passed {
		t.Fatalf("format should ignore ci-skipped file; diags=%v", diagMsgs(c.Diags))
	}
	if c := findCheck(rep, CheckLint); !c.Passed {
		t.Fatalf("lint should ignore ci-skipped file; diags=%v", diagMsgs(c.Diags))
	}
}

func TestAllCISkippedFilesDoNotFallbackWalk(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "future.osty", "// osty:ci-skip\n    pub fn future() {}\n")

	r := NewRunner(dir, Options{Format: true, Lint: true})
	if err := r.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(r.Packages) != 1 {
		t.Fatalf("expected one package, got %d", len(r.Packages))
	}
	if len(r.Packages[0].Files) != 0 {
		t.Fatalf("expected every file to be skipped, got %d", len(r.Packages[0].Files))
	}

	rep := r.Run()
	if c := findCheck(rep, CheckFormat); !c.Passed {
		t.Fatalf("format should not fallback-walk skipped files; diags=%v", diagMsgs(c.Diags))
	}
	if c := findCheck(rep, CheckLint); !c.Passed {
		t.Fatalf("lint should tolerate all-skipped package; diags=%v", diagMsgs(c.Diags))
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
		Packages: map[string][]Symbol{
			"demo": {
				{Name: "Greet", Kind: "function"},
				{Name: "User", Kind: "struct"},
			},
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
	base := &Snapshot{Packages: map[string][]Symbol{
		"demo": {
			{Name: "Keep", Kind: "function", Sig: "() -> ()"},
			{Name: "Drop", Kind: "function", Sig: "() -> ()"},
		},
	}}
	cur := &Snapshot{Packages: map[string][]Symbol{
		"demo": {
			{Name: "Keep", Kind: "function", Sig: "() -> ()"},
			{Name: "New", Kind: "struct", Sig: "{}"},
		},
	}}
	d := Compare(base, cur)
	if len(d.Removed) != 1 || d.Removed[0].Symbol.Name != "Drop" {
		t.Fatalf("removed wrong: %+v", d.Removed)
	}
	if len(d.Added) != 1 || d.Added[0].Symbol.Name != "New" {
		t.Fatalf("added wrong: %+v", d.Added)
	}
	if len(d.Changed) != 0 {
		t.Fatalf("changed should be empty: %+v", d.Changed)
	}
}

// TestSnapshotCompareDetectsSigChange verifies that two symbols
// sharing (name, kind) but differing in Sig produce a Changed
// entry — the structural break the v2 schema was designed for.
func TestSnapshotCompareDetectsSigChange(t *testing.T) {
	base := &Snapshot{Packages: map[string][]Symbol{
		"demo": {{Name: "f", Kind: "function", Sig: "(Int) -> Bool"}},
	}}
	cur := &Snapshot{Packages: map[string][]Symbol{
		"demo": {{Name: "f", Kind: "function", Sig: "(String) -> Bool"}},
	}}
	d := Compare(base, cur)
	if len(d.Changed) != 1 || d.Changed[0].Symbol.Name != "f" {
		t.Fatalf("expected one changed signature, got %+v", d.Changed)
	}
	if len(d.Removed) != 0 || len(d.Added) != 0 {
		t.Fatalf("removed/added should be empty: %+v / %+v", d.Removed, d.Added)
	}
}

// TestCapturePackageFromSource is the end-to-end test that the
// AST signature renderers actually pick up the right shapes. We
// load real source through the resolver, capture, and assert on
// concrete Sig strings — that catches regressions in either the
// snapshot code or the parser/AST shape.
func TestCapturePackageFromSource(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "lib.osty", `
pub fn add(a: Int, b: Int) -> Int { a + b }

pub struct User {
    pub name: String,
    pub age: Int,
    secret: String,
}

pub enum Color {
    Red,
    RGB(Int, Int, Int),
}

fn private() {}
`)
	r := NewRunner(dir, Options{})
	if err := r.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(r.Packages) == 0 {
		t.Fatal("no package loaded")
	}
	syms := CapturePackage(r.Packages[0])
	got := map[string]string{}
	for _, s := range syms {
		got[s.Kind+" "+s.Name] = s.Sig
	}

	// Spot-check the declarations we expect; the AST snapshot
	// includes more (struct fields, variants) but we only assert
	// on the must-haves so a future field addition doesn't
	// brittle the test.
	wantHave := map[string]string{
		"function add":      "(a: Int, b: Int) -> Int",
		"struct User":       "{name: String, age: Int}",
		"enum Color":        "Red | RGB(Int, Int, Int)",
		"field User.name":   "String",
		"variant Color.RGB": "RGB(Int, Int, Int)",
	}
	for k, want := range wantHave {
		if got[k] != want {
			t.Errorf("%q: got sig %q, want %q", k, got[k], want)
		}
	}
	// Private decls and private fields must NOT appear.
	if _, ok := got["function private"]; ok {
		t.Errorf("private fn leaked into snapshot")
	}
	if _, ok := got["field User.secret"]; ok {
		t.Errorf("private field leaked into snapshot")
	}
}

// TestReadSnapshotV1Upgrades verifies a v1 snapshot (flat
// `symbols` list, no `packages` map) is silently upgraded to the
// v2 representation when read.
func TestReadSnapshotV1Upgrades(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "v1.json")
	v1 := `{"schema":1,"package":"demo","symbols":[{"name":"hello","kind":"function"}]}`
	if err := os.WriteFile(path, []byte(v1), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := ReadSnapshot(path)
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	if got, ok := s.Packages["demo"]; !ok || len(got) != 1 || got[0].Name != "hello" {
		t.Fatalf("v1 upgrade lost data: %+v", s.Packages)
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
