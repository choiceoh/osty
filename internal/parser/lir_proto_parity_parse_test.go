package parser

import (
	"os"
	"path/filepath"
	"testing"
)

// TestParseLirProtoParityFixtureFile guards the toolchain
// `lir_proto_parity_test.osty` source — the data table that
// drives every LIR Proto LLVM-emission parity check — against
// accidental syntax breakage from PRs that add new fixtures.
// Without this test, a stray comma/brace in a freshly-appended
// fixture function only surfaces when `osty-self` is built and
// the toolchain test suite runs, which can be hours after the
// PR lands in CI.
func TestParseLirProtoParityFixtureFile(t *testing.T) {
	candidates := []string{
		"../../toolchain/lir_proto_parity_test.osty",
		"toolchain/lir_proto_parity_test.osty",
	}
	var src []byte
	for _, rel := range candidates {
		abs, err := filepath.Abs(rel)
		if err != nil {
			continue
		}
		if data, err := os.ReadFile(abs); err == nil {
			src = data
			break
		}
	}
	if src == nil {
		t.Skip("lir_proto_parity_test.osty not found")
	}
	file, diags := ParseDiagnostics(src)
	if file == nil {
		t.Fatalf("parser returned nil file (%d diags)", len(diags))
	}
	if len(diags) != 0 {
		for i, d := range diags {
			if i >= 10 {
				t.Logf("  ... %d more", len(diags)-10)
				break
			}
			t.Errorf("parse diag[%d]: %v", i, d)
		}
		t.Fatalf("lir_proto_parity_test.osty failed to parse cleanly")
	}
}
