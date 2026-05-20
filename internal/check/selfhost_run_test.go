package check

import (
	"testing"

	"github.com/osty/osty/internal/parser"
)

func TestSelfhostRunStaysOffPublicASTCompatibility(t *testing.T) {
	src := []byte("fn main() {\n    let value = 1\n}\n")
	run := parser.ParseRun(src)
	if run == nil {
		t.Fatal("ParseRun returned nil")
	}

	result := SelfhostRun(run, Opts{Source: src})
	if result == nil || result.NativeCheckResult == nil {
		t.Fatalf("SelfhostRun result = %#v, want structured native check result", result)
	}
	if len(result.NativeCheckResult.TypedNodes) == 0 && len(result.NativeCheckResult.Bindings) == 0 {
		t.Fatalf("SelfhostRun native result is empty: %#v", result.NativeCheckResult)
	}
}
