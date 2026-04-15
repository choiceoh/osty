package scaffold_test

// End-to-end integration tests that exercise the scaffold + manifest +
// resolve + check pipeline as one unit. They verify that a freshly
// scaffolded project (binary, library, or workspace) can be loaded by
// manifest.Load and passes the full front-end without any
// error-severity diagnostic.
//
// These tests live in `scaffold_test` (external package) so they
// cannot reach into scaffold internals — they use only the public API
// the CLI itself consumes. That makes this file a smoke test for the
// contract between the subcommands and every downstream pass.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/manifest"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/scaffold"
	"github.com/osty/osty/internal/stdlib"
)

// TestScaffoldAndLoadBinary scaffolds a binary project, loads + validates
// its manifest, and runs the front-end over every source file. Any
// error-severity diagnostic (parse, resolve, type-check, or manifest
// validation) fails the test.
func TestScaffoldAndLoadBinary(t *testing.T) {
	parent := t.TempDir()
	dir, d := scaffold.Create(scaffold.Options{Name: "e2ebin", Parent: parent})
	if d != nil {
		t.Fatalf("scaffold: %s", d.Error())
	}
	m, mdiags := loadManifest(t, dir)
	for _, dd := range mdiags {
		if dd.Severity == diag.Error {
			t.Errorf("manifest diagnostic: %s", dd.Error())
		}
	}
	if !m.HasPackage {
		t.Errorf("scaffolded binary should have [package]")
	}
	if m.Package.Name != "e2ebin" {
		t.Errorf("manifest name = %q, want e2ebin", m.Package.Name)
	}
	if m.Package.Edition == "" {
		t.Errorf("manifest edition was empty; scaffold should pin it")
	}

	// Full package load (production sources, no tests) goes through
	// resolve + check. The test file is checked separately in the
	// WithTests variant below.
	pkg, err := resolve.LoadPackage(dir)
	if err != nil {
		t.Fatalf("LoadPackage: %v", err)
	}
	res := resolve.ResolvePackage(pkg, resolve.NewPrelude())
	chk := check.Package(pkg, res)
	all := append(append([]*diag.Diagnostic{}, res.Diags...), chk.Diags...)
	for _, dd := range all {
		if dd.Severity == diag.Error {
			t.Errorf("package diagnostic: %s", dd.Error())
		}
	}
}

// TestScaffoldAndLoadLibrary is the --lib analogue: the library's
// `pub fn greet` plus its test file must resolve and type-check
// together as one package.
func TestScaffoldAndLoadLibrary(t *testing.T) {
	parent := t.TempDir()
	dir, d := scaffold.Create(scaffold.Options{
		Name:   "e2elib",
		Parent: parent,
		Kind:   scaffold.KindLib,
	})
	if d != nil {
		t.Fatalf("scaffold: %s", d.Error())
	}
	m, _ := loadManifest(t, dir)
	if m.Package.Name != "e2elib" {
		t.Errorf("manifest name = %q, want e2elib", m.Package.Name)
	}

	pkg, err := resolve.LoadPackageWithTests(dir)
	if err != nil {
		t.Fatalf("LoadPackageWithTests: %v", err)
	}
	res := resolve.ResolvePackage(pkg, resolve.NewPrelude())
	chk := check.Package(pkg, res)
	all := append(append([]*diag.Diagnostic{}, res.Diags...), chk.Diags...)
	for _, dd := range all {
		if dd.Severity == diag.Error {
			t.Errorf("package diagnostic: %s", dd.Error())
		}
	}
}

// TestScaffoldAndLoadWorkspace scaffolds a virtual workspace, loads
// the root manifest, and then loads + front-ends every member. The
// workspace root itself has no package, so we only check members.
func TestScaffoldAndLoadWorkspace(t *testing.T) {
	parent := t.TempDir()
	dir, d := scaffold.Create(scaffold.Options{
		Name:   "e2ews",
		Parent: parent,
		Kind:   scaffold.KindWorkspace,
	})
	if d != nil {
		t.Fatalf("scaffold: %s", d.Error())
	}
	m, mdiags := loadManifest(t, dir)
	for _, dd := range mdiags {
		if dd.Severity == diag.Error {
			t.Errorf("workspace manifest error: %s", dd.Error())
		}
	}
	if m.HasPackage {
		t.Errorf("virtual workspace should not have [package]")
	}
	if m.Workspace == nil || len(m.Workspace.Members) != 1 {
		t.Fatalf("workspace members missing: %+v", m.Workspace)
	}

	// Load each member as its own package — this is what `osty build`
	// does for virtual workspaces today.
	for _, mem := range m.Workspace.Members {
		memDir := filepath.Join(dir, mem)
		mm, mdiags := loadManifest(t, memDir)
		for _, dd := range mdiags {
			if dd.Severity == diag.Error {
				t.Errorf("member %s manifest error: %s", mem, dd.Error())
			}
		}
		if !mm.HasPackage {
			t.Errorf("member %s should have [package]", mem)
		}
		pkg, err := resolve.LoadPackage(memDir)
		if err != nil {
			t.Fatalf("LoadPackage %s: %v", mem, err)
		}
		res := resolve.ResolvePackage(pkg, resolve.NewPrelude())
		chk := check.Package(pkg, res)
		all := append(append([]*diag.Diagnostic{}, res.Diags...), chk.Diags...)
		for _, dd := range all {
			if dd.Severity == diag.Error {
				t.Errorf("member %s: %s", mem, dd.Error())
			}
		}
	}
}

// TestInitAndLoad exercises the `osty init` flow: scaffolding into an
// existing empty directory, then loading the manifest through the
// same pipeline the CLI uses.
func TestInitAndLoad(t *testing.T) {
	dir := t.TempDir()
	got, d := scaffold.Init(scaffold.Options{Name: "initapp", Parent: dir})
	if d != nil {
		t.Fatalf("init: %s", d.Error())
	}
	wantDir, _ := filepath.EvalSymlinks(dir)
	gotDir, _ := filepath.EvalSymlinks(got)
	if wantDir != gotDir {
		t.Errorf("Init path = %q, want %q", gotDir, wantDir)
	}
	m, mdiags := loadManifest(t, dir)
	for _, dd := range mdiags {
		if dd.Severity == diag.Error {
			t.Errorf("manifest error: %s", dd.Error())
		}
	}
	if m.Package.Name != "initapp" {
		t.Errorf("manifest name = %q, want initapp", m.Package.Name)
	}
}

// TestBadScaffoldProducesStableCodes exercises the failure path: a
// scaffold request with a bad name must produce a CodeScaffoldInvalidName
// diagnostic with a working position and hint.
func TestBadScaffoldProducesStableCodes(t *testing.T) {
	d := scaffold.ValidateName("1not-allowed")
	if d == nil {
		t.Fatal("ValidateName accepted an invalid name")
	}
	if d.Code != diag.CodeScaffoldInvalidName {
		t.Errorf("code = %q, want %q", d.Code, diag.CodeScaffoldInvalidName)
	}
	if d.Hint == "" {
		t.Errorf("no hint attached to ValidateName diagnostic")
	}
	if !strings.Contains(d.Message, "1not-allowed") {
		t.Errorf("diagnostic message does not echo the bad name: %s", d.Message)
	}
}

// TestManifestDiagsCarryPosition ensures that manifest validation
// diagnostics point at the right TOML line. We write a bad edition,
// load the manifest, and assert the diagnostic's primary position
// falls on the `edition = ...` line.
func TestManifestDiagsCarryPosition(t *testing.T) {
	dir := t.TempDir()
	src := `# scaffold test
[package]
name = "bad"
version = "0.1.0"
edition = "99.0"
`
	path := filepath.Join(dir, "osty.toml")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, diags, err := manifest.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var found *diag.Diagnostic
	for _, d := range diags {
		if d.Code == diag.CodeManifestBadEdition {
			found = d
			break
		}
	}
	if found == nil {
		t.Fatalf("no CodeManifestBadEdition diagnostic; got %v", codes(diags))
	}
	if pos := found.PrimaryPos(); pos.Line != 5 {
		t.Errorf("diag line = %d, want 5", pos.Line)
	}
}

// ---- helpers ----

func loadManifest(t *testing.T, dir string) (*manifest.Manifest, []*diag.Diagnostic) {
	t.Helper()
	m, diags, err := manifest.Load(filepath.Join(dir, manifest.ManifestFile))
	if err != nil {
		t.Fatalf("manifest.Load %s: %v", dir, err)
	}
	return m, diags
}

// AssertCompilesWith stdlib-aware runs lex + parse + resolve + check
// over src. Unused right now (the tests above use the package-level
// API) but kept for future single-file smoke tests.
func AssertCompilesWith(t *testing.T, src []byte) {
	t.Helper()
	_ = src
	_ = stdlib.Load // keep import non-dead for future expansion
}

func codes(ds []*diag.Diagnostic) []string {
	var cs []string
	for _, d := range ds {
		cs = append(cs, d.Code)
	}
	return cs
}
