package parser

import (
	"testing"
)

func TestScopedImportExpandsToFlatUses(t *testing.T) {
	src := []byte(`use std.fs::{open, exists, remove}

fn main() -> Int { 0 }
`)
	file, diags := ParseDiagnostics(src)
	if file == nil {
		t.Fatalf("parse failed: %v", diags)
	}
	if len(file.Uses) != 3 {
		t.Fatalf("expected 3 use decls after expansion, got %d", len(file.Uses))
	}
	want := []string{"std.fs.open", "std.fs.exists", "std.fs.remove"}
	for i, u := range file.Uses {
		if u.RawPath != want[i] {
			t.Errorf("use[%d]: got path %q, want %q", i, u.RawPath, want[i])
		}
	}
}

func TestScopedImportWithAlias(t *testing.T) {
	src := []byte(`use std.collections::{List, Map as Dict, Set}
`)
	file, _ := ParseDiagnostics(src)
	if file == nil {
		t.Fatal("parse failed")
	}
	if len(file.Uses) != 3 {
		t.Fatalf("expected 3 use decls, got %d", len(file.Uses))
	}
	cases := []struct {
		path  string
		alias string
	}{
		{"std.collections.List", ""},
		{"std.collections.Map", "Dict"},
		{"std.collections.Set", ""},
	}
	for i, c := range cases {
		if file.Uses[i].RawPath != c.path {
			t.Errorf("use[%d]: path got %q, want %q", i, file.Uses[i].RawPath, c.path)
		}
		if file.Uses[i].Alias != c.alias {
			t.Errorf("use[%d]: alias got %q, want %q", i, file.Uses[i].Alias, c.alias)
		}
	}
}

func TestPubScopedImport(t *testing.T) {
	src := []byte(`pub use std.fs::{open, exists}
`)
	file, _ := ParseDiagnostics(src)
	if file == nil {
		t.Fatal("parse failed")
	}
	if len(file.Uses) != 2 {
		t.Fatalf("expected 2 use decls after pub scoped expansion, got %d", len(file.Uses))
	}
	for i, u := range file.Uses {
		if !u.IsPub {
			t.Errorf("use[%d] %q: expected IsPub=true, got false", i, u.RawPath)
		}
	}
}

func TestScopedImportTrailingComma(t *testing.T) {
	src := []byte(`use std.io::{print, println,}
`)
	file, _ := ParseDiagnostics(src)
	if file == nil {
		t.Fatal("parse failed")
	}
	if len(file.Uses) != 2 {
		t.Errorf("expected 2 use decls, got %d", len(file.Uses))
	}
}

func TestPlainUseUnaffected(t *testing.T) {
	src := []byte(`use std.fs
use std.io
`)
	file, _ := ParseDiagnostics(src)
	if file == nil {
		t.Fatal("parse failed")
	}
	if len(file.Uses) != 2 {
		t.Fatalf("expected 2 use decls, got %d", len(file.Uses))
	}
	if file.Uses[0].RawPath != "std.fs" {
		t.Errorf("use[0]: got %q, want std.fs", file.Uses[0].RawPath)
	}
	if file.Uses[1].RawPath != "std.io" {
		t.Errorf("use[1]: got %q, want std.io", file.Uses[1].RawPath)
	}
}

func TestScopedImportProvenance(t *testing.T) {
	src := []byte(`use std.fs::{open, exists}
`)
	result := ParseDetailed(src)
	if result.File == nil {
		t.Fatal("parse failed")
	}
	if result.Provenance == nil {
		t.Fatal("expected provenance for scoped expansion, got nil")
	}
	found := false
	for _, step := range result.Provenance.Lowerings {
		if step.Kind == "scoped_import_expansion" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected scoped_import_expansion provenance step")
	}
}
