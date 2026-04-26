package check

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/resolve"
)

// TestCheckPackageStampsPerFile verifies that checker-side helpers
// (runIntrinsicBodyChecks in this case) stamp d.File with the owning
// file's path when walked as part of a multi-file package. Without it,
// the CLI can only fall back to the offset+name heuristic, which misses
// diagnostics whose message doesn't backtick the offending identifier.
func TestCheckPackageStampsPerFile(t *testing.T) {
	dir := t.TempDir()
	goodPath := filepath.Join(dir, "good.osty")
	badPath := filepath.Join(dir, "bad.osty")
	if err := os.WriteFile(goodPath, []byte(`pub fn ok() -> Int { 0 }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(badPath, []byte(`#[intrinsic]
pub fn violator() -> Int { 42 }
`), 0o644); err != nil {
		t.Fatal(err)
	}

	pkg, err := resolve.LoadPackageArenaFirst(dir)
	if err != nil {
		t.Fatalf("LoadPackage: %v", err)
	}
	pr := resolve.ResolvePackageDefault(pkg)
	res := Package(pkg, pr)

	var intrinsicDiag *diag.Diagnostic
	for _, d := range res.Diags {
		if d == nil {
			continue
		}
		if d.Code == diag.CodeIntrinsicNonEmptyBody {
			intrinsicDiag = d
			break
		}
	}
	if intrinsicDiag == nil {
		t.Fatalf("expected E0773 for #[intrinsic] body; got diags:\n%+v", res.Diags)
	}
	if intrinsicDiag.File != badPath {
		t.Fatalf("intrinsic body diag attributed to %q, want %q", intrinsicDiag.File, badPath)
	}
}
