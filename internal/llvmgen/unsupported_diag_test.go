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

// TestUnsupportedDiagnosticHintAnchors pins each LLVM0xx hint to stable
// anchor substrings. The intent is to catch silent regressions where a
// refactor of the Osty source in toolchain/llvmgen.osty (or its Go
// snapshot in support_snapshot.go) weakens a hint back to the generic
// "reduce to smoke subset" fallback. Each anchor is picked to survive
// minor rewording while pinning the actionable content — the specific
// migration path (runtime.cabi, runtime ABI shim), the supported
// subset (Int or Bool, range loops, ASCII identifiers, …), or the
// gap category (stdlib + runtime shim).
func TestUnsupportedDiagnosticHintAnchors(t *testing.T) {
	cases := []struct {
		kind    string
		code    string
		anchors []string
	}{
		{"go-ffi", "LLVM001", []string{"runtime.cabi"}},
		{"runtime-ffi", "LLVM002", []string{"runtime ABI shim"}},
		{"source-layout", "LLVM010", []string{"main function"}},
		{"type-system", "LLVM011", []string{"Int or Bool"}},
		{"statement", "LLVM012", []string{"range-for"}},
		{"expression", "LLVM013", []string{"value-if"}},
		{"control-flow", "LLVM014", []string{"range loops"}},
		{"call", "LLVM015", []string{"positional Int/Bool"}},
		{"name", "LLVM016", []string{"ASCII identifiers"}},
		{"function-signature", "LLVM017", []string{"non-generic"}},
		{"stdlib-body", "LLVM018", []string{"stdlib", "runtime shim"}},
		{"anything-else", "LLVM000", []string{"LLVM smoke subset"}},
	}
	for _, tc := range cases {
		got := UnsupportedDiagnosticFor(tc.kind, "detail")
		if got.Code != tc.code {
			t.Errorf("kind=%q: Code = %q, want %q", tc.kind, got.Code, tc.code)
		}
		if got.Hint == "" {
			t.Errorf("kind=%q (%s): Hint is empty", tc.kind, got.Code)
			continue
		}
		for _, anchor := range tc.anchors {
			if !strings.Contains(got.Hint, anchor) {
				t.Errorf("kind=%q (%s): Hint = %q, missing anchor %q",
					tc.kind, got.Code, got.Hint, anchor)
			}
		}
	}
}
