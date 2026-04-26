package lint

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/osty/osty/internal/resolve"
)

// TestLintPackageStampsPerFile verifies the package-mode lint walker
// stamps d.File on every diagnostic with the owning file's path, so
// multi-file CLI runs route each lint finding to the right source
// snippet.
func TestLintPackageStampsPerFile(t *testing.T) {
	dir := t.TempDir()
	aPath := filepath.Join(dir, "alpha.osty")
	bPath := filepath.Join(dir, "beta.osty")
	// `alpha.osty` has an unused let binding — L0001.
	if err := os.WriteFile(aPath, []byte(`fn main() {
    let unusedHere = 1
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	// `beta.osty` is clean.
	if err := os.WriteFile(bPath, []byte(`pub fn greet() -> Int { 0 }
`), 0o644); err != nil {
		t.Fatal(err)
	}

	pkg, err := resolve.LoadPackageArenaFirst(dir)
	if err != nil {
		t.Fatalf("LoadPackage: %v", err)
	}
	pr := resolve.ResolvePackageDefault(pkg)
	res := Package(pkg, pr, nil)

	if len(res.Diags) == 0 {
		t.Fatalf("expected at least one lint diagnostic for unused binding")
	}
	for _, d := range res.Diags {
		if d == nil {
			continue
		}
		if d.File == "" {
			t.Fatalf("lint diagnostic emitted without d.File: %+v", d)
		}
		if d.File != aPath && d.File != bPath {
			t.Fatalf("lint diag attributed to unknown file %q", d.File)
		}
	}
}
