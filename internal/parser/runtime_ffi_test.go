package parser

import "testing"

func TestParseRuntimeFFIUse(t *testing.T) {
	src := []byte(`use runtime.strings as strings {
    fn HasPrefix(s: String, prefix: String) -> Bool
}
`)
	file, diags := ParseDiagnostics(src)
	if len(diags) > 0 {
		t.Fatalf("ParseDiagnostics returned %d diagnostics: %v", len(diags), diags[0])
	}
	if file == nil || len(file.Uses) != 1 {
		t.Fatalf("parsed uses = %d, want 1", len(file.Uses))
	}
	use := file.Uses[0]
	if use.IsGoFFI {
		t.Fatalf("runtime FFI use was marked as Go FFI")
	}
	if !use.IsRuntimeFFI {
		t.Fatalf("runtime FFI use was not marked as runtime FFI")
	}
	if got, want := use.RuntimePath, "runtime.strings"; got != want {
		t.Fatalf("RuntimePath = %q, want %q", got, want)
	}
	if got, want := use.Alias, "strings"; got != want {
		t.Fatalf("Alias = %q, want %q", got, want)
	}
	if got, want := len(use.GoBody), 1; got != want {
		t.Fatalf("FFI body len = %d, want %d", got, want)
	}
}
