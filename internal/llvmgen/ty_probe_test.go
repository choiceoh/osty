package llvmgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/parser"
)

func TestProbeToolchainTyLower(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	path := filepath.Join(root, "toolchain/ty.osty")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	file, diags := parser.ParseDiagnostics(src)
	if file == nil {
		t.Fatalf("parse returned nil (%d diags)", len(diags))
	}
	if _, err := generateFromAST(file, Options{PackageName: "main", SourcePath: path}); err != nil {
		msg := err.Error()
		if strings.Contains(msg, "LLVM012") && strings.Contains(msg, "nextVarId") {
			t.Errorf("ty.osty regressed on LLVM012 nextVarId immutable-field assignment: %v", err)
		} else {
			t.Logf("ty.osty lowered past LLVM012 (remaining wall: %v)", err)
		}
	} else {
		t.Log("ty.osty lowered cleanly")
	}
}
