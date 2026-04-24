package resolve

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/osty/osty/internal/diag"
)

// TestWorkspaceDiagsCarryFilePathAcrossPackages verifies that resolver
// diagnostics emitted while walking a multi-package workspace carry the
// owning file's path, even when several packages share an offset range.
func TestWorkspaceDiagsCarryFilePathAcrossPackages(t *testing.T) {
	root := t.TempDir()
	pkgA := filepath.Join(root, "alpha")
	pkgB := filepath.Join(root, "beta")
	if err := os.MkdirAll(pkgA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(pkgB, 0o755); err != nil {
		t.Fatal(err)
	}
	aFile := filepath.Join(pkgA, "lib.osty")
	bFile := filepath.Join(pkgB, "lib.osty")
	if err := os.WriteFile(aFile, []byte(`pub fn alphaOk() -> Int { 0 }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bFile, []byte(`pub fn betaBroken() -> Int { missingSymbolHere() }
`), 0o644); err != nil {
		t.Fatal(err)
	}

	ws, err := NewWorkspace(root)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	for _, p := range WorkspacePackagePaths(root) {
		if _, err := ws.LoadPackageArenaFirst(p); err != nil {
			t.Fatalf("LoadPackage %s: %v", p, err)
		}
	}
	results := ws.ResolveAll()

	var undef *diag.Diagnostic
	for _, pr := range results {
		if pr == nil {
			continue
		}
		for _, d := range pr.Diags {
			if d.Code == diag.CodeUndefinedName {
				undef = d
				break
			}
		}
		if undef != nil {
			break
		}
	}
	if undef == nil {
		t.Fatalf("expected E0500 for undefined name in workspace")
	}
	if undef.File != bFile {
		t.Fatalf("workspace E0500 attributed to %q, want %q", undef.File, bFile)
	}
}
