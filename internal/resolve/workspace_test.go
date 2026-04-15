package resolve

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/diag"
)

// mkWorkspace scaffolds a workspace by writing `files` (keyed by
// "package/file.osty" relative path) under a fresh temp dir and
// returns a workspace rooted there.
func mkWorkspace(t *testing.T, files map[string]string) *Workspace {
	t.Helper()
	root := t.TempDir()
	for rel, src := range files {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(src), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	ws, err := NewWorkspace(root)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	return ws
}

// allDiags aggregates diagnostics from every loaded package in the
// workspace — convenient for "did any error show up" style tests.
func allDiags(results map[string]*PackageResult) []*diag.Diagnostic {
	var out []*diag.Diagnostic
	for _, r := range results {
		out = append(out, r.Diags...)
	}
	return out
}

func findWsDiag(results map[string]*PackageResult, code string) *diag.Diagnostic {
	for _, d := range allDiags(results) {
		if d.Code == code {
			return d
		}
	}
	return nil
}

// TestCrossPackageTypeReference verifies that `auth.User` in package
// `main` resolves to `pub struct User` in package `auth`.
func TestCrossPackageTypeReference(t *testing.T) {
	ws := mkWorkspace(t, map[string]string{
		"auth/user.osty": `pub struct User {
    pub name: String,
    pub email: String,
}`,
		"main.osty": `use auth

fn greet(u: auth.User) -> String {
    u.name
}`,
	})
	_, _ = ws.LoadPackage("")
	_, _ = ws.LoadPackage("auth")
	results := ws.ResolveAll()
	for _, d := range allDiags(results) {
		t.Errorf("unexpected diag: %s", d.Error())
	}
}

// TestCrossPackageFnCall verifies `auth.login()` call-site resolution.
func TestCrossPackageFnCall(t *testing.T) {
	ws := mkWorkspace(t, map[string]string{
		"auth/login.osty": `pub fn login(user: String, pass: String) -> Bool {
    true
}`,
		"main.osty": `use auth

fn main() {
    auth.login("alice", "secret")
}`,
	})
	_, _ = ws.LoadPackage("")
	_, _ = ws.LoadPackage("auth")
	results := ws.ResolveAll()
	for _, d := range allDiags(results) {
		t.Errorf("unexpected diag: %s", d.Error())
	}
}

// TestCrossPackagePrivateRejected verifies E0507: referring to a
// non-`pub` symbol across a package boundary is an error.
func TestCrossPackagePrivateRejected(t *testing.T) {
	ws := mkWorkspace(t, map[string]string{
		"auth/secret.osty": `fn hashPassword(p: String) -> String { p }`,
		"main.osty": `use auth

fn main() {
    auth.hashPassword("x")
}`,
	})
	_, _ = ws.LoadPackage("")
	_, _ = ws.LoadPackage("auth")
	results := ws.ResolveAll()
	if findWsDiag(results, diag.CodePrivateAcrossPackages) == nil {
		t.Fatalf("expected E0507, got %v", allDiags(results))
	}
}

// TestCrossPackageUnknownMember verifies E0508 when the name doesn't
// exist in the target package at all.
func TestCrossPackageUnknownMember(t *testing.T) {
	ws := mkWorkspace(t, map[string]string{
		"auth/user.osty": `pub fn login() {}`,
		"main.osty": `use auth

fn main() {
    auth.nonexistent()
}`,
	})
	_, _ = ws.LoadPackage("")
	_, _ = ws.LoadPackage("auth")
	results := ws.ResolveAll()
	if findWsDiag(results, diag.CodeUnknownExportedMember) == nil {
		t.Fatalf("expected E0508, got %v", allDiags(results))
	}
}

// TestUnknownPackage verifies E0505 when `use` names a non-existent
// directory.
func TestUnknownPackage(t *testing.T) {
	ws := mkWorkspace(t, map[string]string{
		"main.osty": `use missing

fn main() {}`,
	})
	_, _ = ws.LoadPackage("")
	results := ws.ResolveAll()
	if findWsDiag(results, diag.CodeUnknownPackage) == nil {
		t.Fatalf("expected E0505, got %v", allDiags(results))
	}
}

// TestCyclicImport verifies E0506 when two packages reference each
// other via `use`.
func TestCyclicImport(t *testing.T) {
	ws := mkWorkspace(t, map[string]string{
		"a/a.osty": `use b
pub fn fromA() { b.fromB() }`,
		"b/b.osty": `use a
pub fn fromB() { a.fromA() }`,
	})
	_, _ = ws.LoadPackage("a")
	results := ws.ResolveAll()
	if findWsDiag(results, diag.CodeCyclicImport) == nil {
		t.Fatalf("expected E0506, got %v", allDiags(results))
	}
}

// TestStdlibStubIsSilent verifies that `use std.fs` on a stub workspace
// (no real stdlib sources) doesn't emit warnings for every member
// access. The spec deliberately keeps stdlib accesses opaque until the
// compiler ships stdlib sources.
func TestStdlibStubIsSilent(t *testing.T) {
	ws := mkWorkspace(t, map[string]string{
		"main.osty": `use std.fs

fn main() {
    let contents = fs.readToString("/etc/passwd")
}`,
	})
	_, _ = ws.LoadPackage("")
	results := ws.ResolveAll()
	for _, d := range allDiags(results) {
		// The `std.fs` stub case should not emit warnings/errors for
		// member access since there are no sources to validate
		// against.
		if strings.Contains(d.Message, "opaque") || d.Code == diag.CodeStdlibNotAvailable {
			t.Errorf("unexpected diag against stdlib stub: %s", d.Error())
		}
	}
}

// fakeStdlib is a minimal StdlibProvider used by the Stdlib-hook tests.
// It returns the stored Package only when `dotPath` matches `path`.
type fakeStdlib struct {
	path string
	pkg  *Package
}

func (f *fakeStdlib) LookupPackage(dotPath string) *Package {
	if dotPath == f.path {
		return f.pkg
	}
	return nil
}

// TestStdlibProviderOverridesStub verifies that when a Workspace has a
// non-nil Stdlib, `use std.X` picks up the provider's Package instead of
// the opaque stub, and member access against it resolves through the
// same path as a real on-disk package.
func TestStdlibProviderOverridesStub(t *testing.T) {
	provided := &Package{
		Name:     "io",
		PkgScope: NewScope(nil, "stdlib:io"),
	}
	provided.PkgScope.DefineForce(&Symbol{
		Name: "print",
		Kind: SymFn,
		Pub:  true,
	})
	ws := mkWorkspace(t, map[string]string{
		"main.osty": `use std.io

fn main() {
    io.print("hi")
}`,
	})
	ws.Stdlib = &fakeStdlib{path: "std.io", pkg: provided}
	_, _ = ws.LoadPackage("")
	results := ws.ResolveAll()
	for _, d := range allDiags(results) {
		if d.Code == diag.CodeUnknownExportedMember || d.Code == diag.CodePrivateAcrossPackages {
			t.Errorf("unexpected diag with stdlib provider attached: %s", d.Error())
		}
	}
	cached, ok := ws.Packages["std.io"]
	if !ok {
		t.Fatal(`Workspace missing "std.io" entry after LoadPackage`)
	}
	if cached != provided {
		t.Error("Workspace did not cache the provider's Package instance")
	}
}

// TestStdlibProviderFallsBackWhenUnknown verifies that a provider
// returning nil for a given path yields the existing opaque-stub
// behavior — the hook does not hijack `std.*` paths it does not
// recognize.
func TestStdlibProviderFallsBackWhenUnknown(t *testing.T) {
	ws := mkWorkspace(t, map[string]string{
		"main.osty": `use std.fs

fn main() {
    fs.readToString("/etc/passwd")
}`,
	})
	// Provider recognizes `std.io` only; the `use std.fs` below should
	// fall through to the opaque stub.
	ws.Stdlib = &fakeStdlib{path: "std.io", pkg: nil}
	_, _ = ws.LoadPackage("")
	results := ws.ResolveAll()
	for _, d := range allDiags(results) {
		if d.Code == diag.CodeUnknownPackage {
			t.Errorf("unexpected CodeUnknownPackage for unmatched stdlib path: %s", d.Error())
		}
	}
	p, ok := ws.Packages["std.fs"]
	if !ok {
		t.Fatal(`Workspace missing "std.fs" entry`)
	}
	if !p.isStub {
		t.Error("Workspace used provider for unrecognized path instead of the opaque stub")
	}
}

// TestDiamondImportsSharePackage verifies that if packages B and C both
// import A, the resolver attaches the SAME Package pointer — not two
// copies — so diamond import patterns work correctly.
func TestDiamondImportsSharePackage(t *testing.T) {
	ws := mkWorkspace(t, map[string]string{
		"a/a.osty": `pub fn shared() {}`,
		"b/b.osty": `use a
pub fn viaB() { a.shared() }`,
		"c/c.osty": `use a
pub fn viaC() { a.shared() }`,
		"main.osty": `use b
use c

fn main() {
    b.viaB()
    c.viaC()
}`,
	})
	_, _ = ws.LoadPackage("")
	results := ws.ResolveAll()
	for _, d := range allDiags(results) {
		t.Errorf("unexpected diag: %s", d.Error())
	}
	if len(ws.Packages) == 0 {
		t.Fatal("no packages loaded")
	}
	// Exactly one `a`, one `b`, one `c`, and one root — four packages.
	want := 4
	if len(ws.Packages) != want {
		t.Errorf("packages count = %d; want %d (%v)",
			len(ws.Packages), want, pkgKeys(ws.Packages))
	}
}

func pkgKeys(m map[string]*Package) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
