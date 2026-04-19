package llvmgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/parser"
)

// TestProbeDiagnosticMergedWithLexer checks whether the LLVM011 wall on
// diagnostic.osty is a real backend gap or just the probe's single-file
// limitation. We concatenate diagnostic.osty + lexer.osty (which defines
// FrontLexStream) and feed the joined source through the same lowering
// path. If diagnostic alone fails but the merged source succeeds, the
// wall is purely cross-file scope.
func TestProbeDiagnosticMergedWithLexer(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	a, err := os.ReadFile(filepath.Join(root, "toolchain/lexer.osty"))
	if err != nil {
		t.Fatalf("read lexer: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(root, "toolchain/diagnostic.osty"))
	if err != nil {
		t.Fatalf("read diagnostic: %v", err)
	}
	merged := string(a) + "\n" + string(b)
	file, _ := parser.ParseDiagnostics([]byte(merged))
	if file == nil {
		t.Fatalf("parse merged returned nil")
	}
	_, err = generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/merged.osty"})
	if err != nil {
		t.Logf("merged source still errors: %v", err)
	} else {
		t.Logf("merged source lowers cleanly — diagnostic.osty wall is cross-file scope")
	}
	// Also classify the error so we know whether it shifted to a deeper wall.
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "FrontLexStream") {
			t.Logf("still complains about FrontLexStream — probably needs more deps")
		}
	}
}
