package llvmgen

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/parser"
)

// TestEmitExprDefaultWallCarriesSourcePos locks in that the emitExpr
// fallthrough wall (LLVM013 "expression: expression %T") is tagged
// with the offending node's line:col. The native-toolchain probe
// (TestProbeNativeToolchainMerged) relies on this tag to point
// investigators at the exact Tier A blocker in the merged buffer
// without re-running a second diagnostic pass.
//
// Positive-position reproduction: a value-position RangeExpr. For
// the synthetic source below the RangeExpr literal starts at line 2,
// column 13 (`0..10`), so the wall must include `at 2:13` and the
// corresponding byte offset.
func TestEmitExprDefaultWallCarriesSourcePos(t *testing.T) {
	src := []byte("fn main() {\n    let r = 0..10\n    println(r)\n}\n")
	file, _ := parser.ParseDiagnostics(src)
	if file == nil {
		t.Fatalf("parse returned nil file")
	}
	_, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/pos_probe.osty"})
	if err == nil {
		t.Fatalf("expected LLVM013 wall for value-position RangeExpr; got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "LLVM013") {
		t.Fatalf("expected LLVM013 wall; got: %s", msg)
	}
	if !strings.Contains(msg, "*ast.RangeExpr") {
		t.Fatalf("expected wall to mention *ast.RangeExpr; got: %s", msg)
	}
	if !strings.Contains(msg, "at 2:13") {
		t.Fatalf("expected wall to pin `at 2:13`; got: %s", msg)
	}
	if !strings.Contains(msg, "offset ") {
		t.Fatalf("expected wall to include byte offset; got: %s", msg)
	}
}
