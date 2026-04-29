package check

import (
	"testing"

	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/selfhost"
)

func TestSelfhostRunStaysOffPublicASTCompatibility(t *testing.T) {
	src := []byte("fn main() {\n    let value = 1\n}\n")
	run := parser.ParseRun(src)
	if run == nil {
		t.Fatal("ParseRun returned nil")
	}

	selfhost.ResetAstbridgeLowerCount()
	result := SelfhostRun(run, Opts{Source: src})
	if got := selfhost.AstbridgeLowerCount(); got != 0 {
		t.Fatalf("SelfhostRun astbridge count = %d, want 0", got)
	}
	if result == nil || result.NativeCheckResult == nil {
		t.Fatalf("SelfhostRun result = %#v, want structured native check result", result)
	}
	if len(result.NativeCheckResult.TypedNodes) == 0 && len(result.NativeCheckResult.Bindings) == 0 {
		t.Fatalf("SelfhostRun native result is empty: %#v", result.NativeCheckResult)
	}
}
