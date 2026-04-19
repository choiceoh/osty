package selfhost

import "testing"

func TestParseScopedUseLowersToFlatUses(t *testing.T) {
	src := []byte("pub use std.fs::{open, exists as has}\n")

	file, diags := Parse(src)
	if len(diags) > 0 {
		t.Fatalf("Parse returned %d diagnostics: %v", len(diags), diags[0])
	}
	if file == nil {
		t.Fatal("Parse returned nil file")
	}
	if len(file.Uses) != 2 {
		t.Fatalf("expected 2 use decls, got %d", len(file.Uses))
	}

	if got := file.Uses[0]; got.RawPath != "std.fs.open" || got.Alias != "" || !got.IsPub {
		t.Fatalf("use[0] = %+v, want RawPath=std.fs.open Alias=\"\" IsPub=true", got)
	}
	if got := file.Uses[1]; got.RawPath != "std.fs.exists" || got.Alias != "has" || !got.IsPub {
		t.Fatalf("use[1] = %+v, want RawPath=std.fs.exists Alias=has IsPub=true", got)
	}
}
