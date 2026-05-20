package resolve

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeSemanticDBPopulatesResolveFacts(t *testing.T) {
	dir := t.TempDir()
	helperPath := filepath.Join(dir, "helper.osty")
	mainPath := filepath.Join(dir, "main.osty")
	if err := os.WriteFile(helperPath, []byte(`pub fn helper() -> Int {
    1
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mainPath, []byte(`fn main() -> Int {
    helper()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	pkg, err := LoadPackageForNative(dir)
	if err != nil {
		t.Fatalf("LoadPackageForNative: %v", err)
	}
	db, err := NativeSemanticDB(pkg)
	if err != nil {
		t.Fatalf("NativeSemanticDB: %v", err)
	}
	if db == nil {
		t.Fatal("NativeSemanticDB returned nil")
	}
	if db.PackageID == "" {
		t.Fatal("SemanticDB PackageID is empty")
	}
	if len(db.Files) != 2 {
		t.Fatalf("SemanticDB files = %d, want 2", len(db.Files))
	}

	var helperID string
	for _, sym := range db.Resolve.Symbols {
		if sym.Name == "helper" && sym.Kind == "fn" {
			helperID = sym.ID
			break
		}
	}
	if helperID == "" {
		t.Fatalf("missing helper symbol in SemanticDB: %#v", db.Resolve.Symbols)
	}
	refs := db.ResolveIndex().RefsByTargetSymbolID[helperID]
	if len(refs) == 0 {
		t.Fatalf("SemanticDB refs by helper target are empty; refs=%#v", db.Resolve.Refs)
	}
	if refs[0].File != mainPath {
		t.Fatalf("helper ref file = %q, want %q", refs[0].File, mainPath)
	}
}

func TestResolvePackageAttachesSemanticDB(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")
	if err := os.WriteFile(path, []byte(`fn main() -> Int {
    1
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	pkg, err := LoadPackageForNative(dir)
	if err != nil {
		t.Fatalf("LoadPackageForNative: %v", err)
	}
	pr := ResolvePackage(pkg, NewPrelude())
	if pr == nil || pr.SemanticDB == nil {
		t.Fatalf("ResolvePackage SemanticDB = %#v, want non-nil", pr)
	}
	if pr.SemanticDB.Resolve.Summary.Symbols == 0 {
		t.Fatalf("SemanticDB resolve summary = %#v, want symbols", pr.SemanticDB.Resolve.Summary)
	}
}

func TestSemanticDBKeepsOriginalSourceForSourceMappedSpans(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")
	raw := []byte(`fn main() {
    let mut items = [1]
    let count = len(items)
    items = append(items, count)
}
`)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	pkg, err := LoadPackageForNative(dir)
	if err != nil {
		t.Fatalf("LoadPackageForNative: %v", err)
	}
	pr := ResolvePackage(pkg, NewPrelude())
	if pr == nil || pr.SemanticDB == nil {
		t.Fatalf("ResolvePackage SemanticDB = %#v, want non-nil", pr)
	}
	file := pr.SemanticDB.FileForPath(path)
	if file == nil {
		t.Fatalf("SemanticDB missing file %q", path)
	}
	if !bytes.Equal(file.OriginalSourceBytes(), raw) {
		t.Fatalf("OriginalSourceBytes = %q, want raw source", file.OriginalSourceBytes())
	}
	if file.SourceMap == nil {
		t.Fatal("SemanticDB SourceMap is nil, want canonical-to-original map")
	}
	if bytes.Equal(file.Source, file.OriginalSourceBytes()) {
		t.Fatal("SemanticDB canonical source unexpectedly equals original source")
	}
}
