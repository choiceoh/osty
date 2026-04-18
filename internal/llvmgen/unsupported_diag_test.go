package llvmgen

import (
	"strings"
	"testing"
)

// TestUnsupportedDiagnosticKindMapping asserts every "kind" string that the
// LLVM backend routes through llvmUnsupportedDiagnostic yields the right
// stable code. This is the contract downstream callers (tests, CLI
// renderer) depend on, so a regression here would silently demote a
// structured error into LLVM000.
func TestUnsupportedDiagnosticKindMapping(t *testing.T) {
	cases := []struct {
		kind string
		want string
	}{
		{"go-ffi", "LLVM001"},
		{"runtime-ffi", "LLVM002"},
		{"source-layout", "LLVM010"},
		{"type-system", "LLVM011"},
		{"statement", "LLVM012"},
		{"expression", "LLVM013"},
		{"control-flow", "LLVM014"},
		{"call", "LLVM015"},
		{"name", "LLVM016"},
		{"function-signature", "LLVM017"},
		{"stdlib-body", "LLVM018"},
		{"anything-else", "LLVM000"},
	}
	for _, tc := range cases {
		got := UnsupportedDiagnosticFor(tc.kind, "detail")
		if got.Code != tc.want {
			t.Errorf("UnsupportedDiagnosticFor(%q).Code = %q, want %q", tc.kind, got.Code, tc.want)
		}
	}
}

// TestUnsupportedStdlibBodyHint verifies the LLVM018 hint is specific enough
// to direct users to either the stdlib lowering backlog or a runtime shim
// workaround rather than offering the generic "reduce to smoke subset"
// fallback.
func TestUnsupportedStdlibBodyHint(t *testing.T) {
	got := UnsupportedDiagnosticFor("stdlib-body", "strings.compare body")
	if got.Code != "LLVM018" {
		t.Fatalf("Code = %q, want LLVM018", got.Code)
	}
	if got.Hint == "" {
		t.Fatalf("Hint is empty")
	}
	for _, want := range []string{"stdlib", "runtime shim"} {
		if !strings.Contains(got.Hint, want) {
			t.Errorf("Hint = %q, missing %q", got.Hint, want)
		}
	}
}
