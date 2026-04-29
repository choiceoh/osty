package check

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/osty/osty/internal/resolve"
)

func TestPackageResultCarriesCombinedSemanticDB(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")
	if err := os.WriteFile(path, []byte(`fn id<T>(x: T) -> T {
    x
}

fn main() -> Int {
    id::<Int>(1)
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	pkg, err := resolve.LoadPackageForNative(dir)
	if err != nil {
		t.Fatalf("LoadPackageForNative: %v", err)
	}
	pr := resolve.ResolvePackage(pkg, resolve.NewPrelude())
	if pr == nil || pr.SemanticDB == nil {
		t.Fatalf("ResolvePackage SemanticDB = %#v, want non-nil", pr)
	}

	result := Package(pkg, pr)
	if result == nil || result.SemanticDB == nil {
		t.Fatalf("Package SemanticDB = %#v, want non-nil", result)
	}
	if result.SemanticDB.Resolve.PackageID != pr.SemanticDB.Resolve.PackageID {
		t.Fatalf("resolve PackageID = %q, want %q", result.SemanticDB.Resolve.PackageID, pr.SemanticDB.Resolve.PackageID)
	}
	if result.SemanticDB.Check == nil {
		t.Fatal("SemanticDB Check is nil")
	}
	if len(result.SemanticDB.Check.TypedNodes) == 0 {
		t.Fatalf("SemanticDB typed nodes are empty: %#v", result.SemanticDB.Check)
	}
	if len(result.SemanticDB.CheckIndex().InstantiationsByStableID) == 0 {
		t.Fatalf("SemanticDB instantiation index is empty: %#v", result.SemanticDB.Check.Instantiations)
	}
	if pr.SemanticDB.Check != nil {
		t.Fatal("checker attachment mutated the resolve PackageResult DB")
	}
}
