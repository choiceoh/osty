package resolve

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNativeResolutionRowsCrossFile(t *testing.T) {
	dir := t.TempDir()
	aPath := filepath.Join(dir, "a.osty")
	bPath := filepath.Join(dir, "b.osty")
	if err := os.WriteFile(aPath, []byte(`pub fn helper() -> Int { 1 }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bPath, []byte(`fn main() {
    let value = helper()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	pkg, err := LoadPackage(dir)
	if err != nil {
		t.Fatalf("LoadPackage: %v", err)
	}
	rows, err := NativeResolutionRows(pkg, bPath)
	if err != nil {
		t.Fatalf("NativeResolutionRows: %v", err)
	}
	if len(rows) == 0 {
		t.Fatalf("rows = %#v, want non-empty helper ref rows", rows)
	}
	found := false
	for _, row := range rows {
		if row.Name == "helper" && row.Kind == "function" && row.Def == "1:5" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("rows = %#v, want helper -> 1:5", rows)
	}
}

func TestNativeResolutionRowsCachesResult(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")
	if err := os.WriteFile(path, []byte(`fn helper() -> Int { 1 }

fn main() {
    let value = helper()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	pkg, err := LoadPackage(dir)
	if err != nil {
		t.Fatalf("LoadPackage: %v", err)
	}
	first, err := NativeResolutionRows(pkg, path)
	if err != nil {
		t.Fatalf("NativeResolutionRows first: %v", err)
	}
	pkg.Files[0].File = nil
	pkg.Files[0].CanonicalSource = []byte("fn broken(")
	second, err := NativeResolutionRows(pkg, path)
	if err != nil {
		t.Fatalf("NativeResolutionRows second: %v", err)
	}
	if len(first) != len(second) {
		t.Fatalf("row count changed across cached call: first=%#v second=%#v", first, second)
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("cached rows changed at %d: first=%#v second=%#v", i, first, second)
		}
	}
}

func TestNativeDiagnosticsSingleFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")
	if err := os.WriteFile(path, []byte(`fn main() {
    missing()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	pkg, err := LoadPackage(dir)
	if err != nil {
		t.Fatalf("LoadPackage: %v", err)
	}
	diags, err := NativeDiagnostics(pkg)
	if err != nil {
		t.Fatalf("NativeDiagnostics: %v", err)
	}
	if len(diags) != 1 {
		t.Fatalf("diag count = %d, want 1 (%#v)", len(diags), diags)
	}
	got := diags[0]
	if got.Code != "E0500" {
		t.Fatalf("code = %q, want E0500", got.Code)
	}
	if got.Message != "undefined name" {
		t.Fatalf("message = %q, want undefined name", got.Message)
	}
	if got.File != path {
		t.Fatalf("file = %q, want %q", got.File, path)
	}
	if pos := got.PrimaryPos(); pos.Line != 2 || pos.Column != 5 {
		t.Fatalf("primary pos = %v, want 2:5", pos)
	}
}
