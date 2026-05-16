package resolve

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/osty/osty/internal/diag"
)

// TestReexportPrivateEmitsE0553 verifies the Path A surface for
// `pub use` re-export visibility — a bare (non-scoped) re-export of a
// private member must surface E0553 (CodeReexportPrivate) on the
// importing package.
func TestReexportPrivateEmitsE0553(t *testing.T) {
	root := t.TempDir()
	libDir := filepath.Join(root, "lib")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(libDir, "lib.osty"), []byte(`fn hidden() -> Int { 1 }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.osty"), []byte(`pub use lib.hidden

fn main() {}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	ws, err := NewWorkspace(root)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	if _, err := ws.LoadPackageNative(""); err != nil {
		t.Fatalf("LoadPackageNative root: %v", err)
	}
	if _, err := ws.LoadPackageNative("lib"); err != nil {
		t.Fatalf("LoadPackageNative lib: %v", err)
	}

	results := ws.ResolveAll()
	rootResult, ok := results[""]
	if !ok {
		t.Fatalf("results[\"\"] missing; got keys %v", keysOf(results))
	}
	if !containsCode(rootResult.Diags, diag.CodeReexportPrivate) {
		t.Fatalf("root result diags missing E0553; got codes %v", codesOf(rootResult.Diags))
	}
}

// TestReexportPubMemberHasNoE0553 confirms a `pub use` of a member that
// IS marked pub in its origin does not trip the Path A check.
func TestReexportPubMemberHasNoE0553(t *testing.T) {
	root := t.TempDir()
	libDir := filepath.Join(root, "lib")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(libDir, "lib.osty"), []byte(`pub fn shown() -> Int { 1 }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.osty"), []byte(`pub use lib.shown

fn main() {}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	ws, err := NewWorkspace(root)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	if _, err := ws.LoadPackageNative(""); err != nil {
		t.Fatalf("LoadPackageNative root: %v", err)
	}
	if _, err := ws.LoadPackageNative("lib"); err != nil {
		t.Fatalf("LoadPackageNative lib: %v", err)
	}

	results := ws.ResolveAll()
	rootResult, ok := results[""]
	if !ok {
		t.Fatalf("results[\"\"] missing; got keys %v", keysOf(results))
	}
	if containsCode(rootResult.Diags, diag.CodeReexportPrivate) {
		t.Fatalf("root result diags unexpectedly contains E0553; codes %v", codesOf(rootResult.Diags))
	}
}

func containsCode(diags []*diag.Diagnostic, code string) bool {
	for _, d := range diags {
		if d != nil && d.Code == code {
			return true
		}
	}
	return false
}

func codesOf(diags []*diag.Diagnostic) []string {
	out := make([]string, 0, len(diags))
	for _, d := range diags {
		if d != nil {
			out = append(out, d.Code)
		}
	}
	return out
}

func keysOf(m map[string]*PackageResult) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
