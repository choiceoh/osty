package selfhost

import "testing"

func TestParsePubUseSetsIsPubFlag(t *testing.T) {
	src := []byte("pub use std.fs as filesystem\n")

	file, diags := Parse(src)
	if len(diags) > 0 {
		t.Fatalf("Parse returned %d diagnostics: %v", len(diags), diags[0])
	}
	if file == nil {
		t.Fatal("Parse returned nil file")
	}
	if len(file.Uses) != 1 {
		t.Fatalf("expected 1 use decl, got %d", len(file.Uses))
	}
	use := file.Uses[0]
	if !use.IsPub {
		t.Fatalf("expected pub use to set IsPub=true")
	}
	if use.Alias != "filesystem" {
		t.Fatalf("expected alias=filesystem, got %q", use.Alias)
	}
}
