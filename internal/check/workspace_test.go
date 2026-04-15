package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/resolve"
)

// mkWorkspace writes each `files` entry (rel path → source) under a
// fresh temp dir, loads every present subdirectory as a package, and
// runs resolve + type-check. Returns the workspace and the per-package
// checker results keyed by dotted import path.
func mkWorkspace(t *testing.T, files map[string]string) (*resolve.Workspace, map[string]*Result) {
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
	ws, err := resolve.NewWorkspace(root)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	// Seed root plus every immediate subdirectory that carries .osty
	// sources.
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if !e.IsDir() {
			if filepath.Ext(e.Name()) == ".osty" {
				_, _ = ws.LoadPackage("")
			}
			continue
		}
		subs, _ := os.ReadDir(filepath.Join(root, e.Name()))
		for _, s := range subs {
			if !s.IsDir() && filepath.Ext(s.Name()) == ".osty" {
				_, _ = ws.LoadPackage(e.Name())
				break
			}
		}
	}
	resolved := ws.ResolveAll()
	checks := Workspace(ws, resolved)
	return ws, checks
}

// allCheckDiags collects every checker diagnostic across a workspace,
// useful for "did any error surface anywhere" assertions.
func allCheckDiags(results map[string]*Result) []*diag.Diagnostic {
	var out []*diag.Diagnostic
	for _, r := range results {
		out = append(out, r.Diags...)
	}
	return out
}

func findDiagCode(results map[string]*Result, code string) *diag.Diagnostic {
	for _, d := range allCheckDiags(results) {
		if d.Code == code {
			return d
		}
	}
	return nil
}

// TestWorkspaceCrossPackageFnCall verifies that a function call into
// another package type-checks against the callee's signature.
func TestWorkspaceCrossPackageFnCall(t *testing.T) {
	_, results := mkWorkspace(t, map[string]string{
		"auth/login.osty": `pub fn login(user: String, pass: String) -> Bool {
    true
}`,
		"main.osty": `use auth
fn main() {
    auth.login("alice", "secret")
}`,
	})
	for _, d := range allCheckDiags(results) {
		if d.Severity == diag.Error {
			t.Errorf("unexpected error: %s", d.Error())
		}
	}
}

// TestWorkspaceCrossPackageArgCountTooMany verifies that cross-package
// calls catch extra arguments (the checker detects "too many"; the
// "too few" case is a pre-existing gap not specific to cross-package
// calls and lives on the checker TODO list).
func TestWorkspaceCrossPackageArgCountTooMany(t *testing.T) {
	_, results := mkWorkspace(t, map[string]string{
		"auth/login.osty": `pub fn login(user: String) -> Bool { true }`,
		"main.osty": `use auth
fn main() {
    auth.login("a", "extra", "extra")
}`,
	})
	if findDiagCode(results, diag.CodeWrongArgCount) == nil {
		t.Fatalf("expected E0701, got %v", allCheckDiags(results))
	}
}

// TestWorkspaceCrossPackageArgTypeMismatch verifies cross-package calls
// enforce argument types.
func TestWorkspaceCrossPackageArgTypeMismatch(t *testing.T) {
	_, results := mkWorkspace(t, map[string]string{
		"auth/login.osty": `pub fn login(user: String, pass: String) -> Bool { true }`,
		"main.osty": `use auth
fn main() {
    auth.login(42, "secret")
}`,
	})
	if findDiagCode(results, diag.CodeTypeMismatch) == nil {
		t.Fatalf("expected E0700, got %v", allCheckDiags(results))
	}
}

// TestWorkspaceCrossPackageTypeInAnnotation verifies that using a type
// from another package — e.g. `auth.User` as a fn parameter — compiles
// without complaint.
func TestWorkspaceCrossPackageTypeInAnnotation(t *testing.T) {
	_, results := mkWorkspace(t, map[string]string{
		"auth/user.osty": `pub struct User {
    pub name: String,
    pub age: Int,
}`,
		"main.osty": `use auth
fn describe(u: auth.User) -> String {
    u.name
}`,
	})
	for _, d := range allCheckDiags(results) {
		if d.Severity == diag.Error {
			t.Errorf("unexpected error: %s", d.Error())
		}
	}
}

// TestWorkspaceCrossPackageReturnPropagates verifies that the return
// type of a cross-package call flows into the caller's `let` type.
func TestWorkspaceCrossPackageReturnPropagates(t *testing.T) {
	_, results := mkWorkspace(t, map[string]string{
		"auth/user.osty": `pub struct User { pub name: String }
pub fn newUser(name: String) -> User {
    User { name }
}`,
		"main.osty": `use auth
fn main() {
    let u = auth.newUser("alice")
    let bad: Int = u
}`,
	})
	// The User returned by auth.newUser must NOT coerce to Int.
	if findDiagCode(results, diag.CodeTypeMismatch) == nil {
		var msgs []string
		for _, d := range allCheckDiags(results) {
			msgs = append(msgs, d.Error())
		}
		t.Fatalf("expected E0700 for `let bad: Int = <User>`; got:\n  %s",
			strings.Join(msgs, "\n  "))
	}
}
