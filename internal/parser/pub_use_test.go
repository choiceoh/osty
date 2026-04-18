package parser

import (
	"testing"

	"github.com/osty/osty/internal/ast"
)

func TestPubUseSetsIsPubFlag(t *testing.T) {
	src := []byte(`pub use std.fs
use std.io
pub use github.com/x/lib as lib
`)
	file, _ := ParseDiagnostics(src)
	if file == nil {
		t.Fatal("parse returned nil file")
	}
	if len(file.Uses) != 3 {
		t.Fatalf("expected 3 use decls, got %d", len(file.Uses))
	}

	cases := []struct {
		path  string
		isPub bool
	}{
		{"std.fs", true},
		{"std.io", false},
		{"github.com/x/lib", true},
	}
	for i, want := range cases {
		got := file.Uses[i]
		if got.RawPath != want.path {
			t.Errorf("[%d] path: got %q, want %q", i, got.RawPath, want.path)
		}
		if got.IsPub != want.isPub {
			t.Errorf("[%d] path %q IsPub: got %v, want %v", i, got.RawPath, got.IsPub, want.isPub)
		}
	}
}

func TestPlainUseHasIsPubFalse(t *testing.T) {
	src := []byte(`use std.fs
use std.io as io
`)
	file, _ := ParseDiagnostics(src)
	if file == nil {
		t.Fatal("parse returned nil file")
	}
	for _, u := range file.Uses {
		if u.IsPub {
			t.Errorf("plain use %q got IsPub=true", u.RawPath)
		}
	}
}

func TestPubUseAcceptsAlias(t *testing.T) {
	src := []byte(`pub use std.fs as filesystem
`)
	file, _ := ParseDiagnostics(src)
	if file == nil {
		t.Fatal("parse returned nil file")
	}
	if len(file.Uses) != 1 {
		t.Fatalf("expected 1 use decl, got %d", len(file.Uses))
	}
	u := file.Uses[0]
	if !u.IsPub {
		t.Errorf("expected IsPub=true on pub use, got false")
	}
	if u.Alias != "filesystem" {
		t.Errorf("expected alias=filesystem, got %q", u.Alias)
	}
}

// Ensure `pub` on a non-use decl doesn't mistakenly mark anything.
func TestPubFnDoesNotMarkFollowingUse(t *testing.T) {
	src := []byte(`pub fn foo() -> Int { 42 }
use std.fs
`)
	file, _ := ParseDiagnostics(src)
	if file == nil {
		t.Fatal("parse returned nil file")
	}
	for _, u := range file.Uses {
		if u.IsPub {
			t.Errorf("plain use after pub fn: got IsPub=true, should be false")
		}
	}
	// Belt and braces: the fn is still pub.
	var fn *ast.FnDecl
	for _, d := range file.Decls {
		if f, ok := d.(*ast.FnDecl); ok {
			fn = f
			break
		}
	}
	if fn == nil {
		t.Fatal("expected fn decl in file")
	}
	if !fn.Pub {
		t.Errorf("pub fn should still have Pub=true")
	}
}
