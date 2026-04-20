package parser

import "testing"

// TestParseUseCDesugarsToRuntimeCabi verifies the v0.5 surface
// `use c "libname" { ... }` parses to the same AST shape as
// `use runtime.cabi.libname { ... }`: IsRuntimeFFI=true, the
// RuntimePath is the canonical `runtime.cabi.<lib>` form, and the
// declarations inside the block are preserved. (LANG_SPEC §12.8.)
func TestParseUseCDesugarsToRuntimeCabi(t *testing.T) {
	src := []byte(`use c "libdemo" as demo {
    fn osty_demo_double(x: Int) -> Int
    fn osty_demo_is_zero(x: Int) -> Bool
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
		t.Fatalf("`use c` must not be marked as Go FFI")
	}
	if !use.IsRuntimeFFI {
		t.Fatalf("`use c` must desugar to runtime FFI; IsRuntimeFFI = false")
	}
	if got, want := use.RuntimePath, "runtime.cabi.libdemo"; got != want {
		t.Fatalf("RuntimePath = %q, want %q", got, want)
	}
	if got, want := use.Alias, "demo"; got != want {
		t.Fatalf("Alias = %q, want %q", got, want)
	}
	if got, want := len(use.GoBody), 2; got != want {
		t.Fatalf("FFI body len = %d, want %d", got, want)
	}
}

// TestParseUseCPathStillWorks ensures the lookahead guards a regular
// `use c.foo` path-style import — the `c` keyword path only triggers
// when followed by a string literal.
func TestParseUseCPathStillWorks(t *testing.T) {
	src := []byte(`use c.foo
`)
	file, diags := ParseDiagnostics(src)
	if len(diags) > 0 {
		t.Fatalf("ParseDiagnostics returned %d diagnostics: %v", len(diags), diags[0])
	}
	if file == nil || len(file.Uses) != 1 {
		t.Fatalf("parsed uses = %d, want 1", len(file.Uses))
	}
	use := file.Uses[0]
	if use.IsRuntimeFFI || use.IsGoFFI {
		t.Fatalf("`use c.foo` must not be classified as FFI; IsRuntimeFFI=%v IsGoFFI=%v", use.IsRuntimeFFI, use.IsGoFFI)
	}
	if got, want := use.RawPath, "c.foo"; got != want {
		t.Fatalf("RawPath = %q, want %q", got, want)
	}
}

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
