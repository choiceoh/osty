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
		if !u.IsScoped {
			t.Errorf("use[%d]: IsScoped=false, want true", i)
		}
		if got := joinPath(u.ScopedBase); got != "std.fs" {
			t.Errorf("use[%d]: scoped base got %q, want std.fs", i, got)
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
		if file.Uses[i].ScopedMember == "" {
			t.Errorf("use[%d]: ScopedMember empty, want preserved member", i)
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

func TestUseFormsShapeContract(t *testing.T) {
	src := []byte(`use foo
use foo.bar
pub use foo.Bar
use foo.baz as baz
use foo.{qux, quux as alias}
`)
	file, diags := ParseDiagnostics(src)
	if len(diags) > 0 {
		t.Fatalf("ParseDiagnostics returned %d diagnostics: %v", len(diags), diags[0])
	}
	if file == nil {
		t.Fatal("parse failed")
	}
	want := []struct {
		path   string
		alias  string
		pub    bool
		scoped bool
		base   string
		member string
	}{
		{path: "foo"},
		{path: "foo.bar"},
		{path: "foo.Bar", pub: true},
		{path: "foo.baz", alias: "baz"},
		{path: "foo.qux", scoped: true, base: "foo", member: "qux"},
		{path: "foo.quux", alias: "alias", scoped: true, base: "foo", member: "quux"},
	}
	if len(file.Uses) != len(want) {
		t.Fatalf("use count = %d, want %d", len(file.Uses), len(want))
	}
	for i, w := range want {
		got := file.Uses[i]
		if got.RawPath != w.path || got.Alias != w.alias || got.IsPub != w.pub || got.IsScoped != w.scoped || joinPath(got.ScopedBase) != w.base || got.ScopedMember != w.member {
			t.Fatalf("use[%d] = %+v, want path=%q alias=%q pub=%v scoped=%v base=%q member=%q", i, got, w.path, w.alias, w.pub, w.scoped, w.base, w.member)
		}
	}
}

func TestParseCanonicalScopedImport(t *testing.T) {
	src := []byte(`use std.fs::{open, exists}
`)
	file, diags := ParseCanonical(src)
	if len(diags) > 0 {
		t.Fatalf("ParseCanonical returned %d diagnostics: %v", len(diags), diags[0])
	}
	if file == nil {
		t.Fatal("ParseCanonical returned nil file")
	}
	if len(file.Uses) != 2 {
		t.Fatalf("expected 2 canonical use decls, got %d", len(file.Uses))
	}
	if file.Uses[0].RawPath != "std.fs.open" {
		t.Fatalf("use[0]: got %q, want std.fs.open", file.Uses[0].RawPath)
	}
	if !file.Uses[0].IsScoped || joinPath(file.Uses[0].ScopedBase) != "std.fs" || file.Uses[0].ScopedMember != "open" {
		t.Fatalf("use[0] scoped metadata = base %v member %q IsScoped %v", file.Uses[0].ScopedBase, file.Uses[0].ScopedMember, file.Uses[0].IsScoped)
	}
	if file.Uses[1].RawPath != "std.fs.exists" {
		t.Fatalf("use[1]: got %q, want std.fs.exists", file.Uses[1].RawPath)
	}
}

func joinPath(parts []string) string {
	out := ""
	for i, part := range parts {
		if i > 0 {
			out += "."
		}
		out += part
	}
	return out
}
