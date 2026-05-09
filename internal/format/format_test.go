package format

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/selfhost"
)

func TestSourceDoesNotMaterializePublicAST(t *testing.T) {
	src := []byte(`fn main() {
let x = 1
x
}
`)

	selfhost.ResetAstbridgeLowerCount()
	out, diags, err := Source(src)
	if err != nil {
		t.Fatalf("Source: %v", err)
	}
	for _, d := range diags {
		if d != nil && d.Severity == diag.Error {
			t.Fatalf("Source diagnostics contained error: %#v", d)
		}
	}
	if len(out) == 0 {
		t.Fatal("Source returned empty formatted output")
	}
	if got := selfhost.AstbridgeLowerCount(); got != 0 {
		t.Fatalf("Source astbridge count = %d, want 0", got)
	}
}

func TestSourcePreservesInterfaceMethodAnnotations(t *testing.T) {
	src := []byte(`interface HashCap {
    #[pure]
    fn hash(self, value: String) -> String
}
`)

	out, diags, err := Source(src)
	if err != nil {
		t.Fatalf("Source: %v", err)
	}
	for _, d := range diags {
		if d != nil && d.Severity == diag.Error {
			t.Fatalf("Source diagnostics contained error: %#v", d)
		}
	}
	if !strings.Contains(string(out), "    #[pure]\n    fn hash(self, value: String) -> String") {
		t.Fatalf("formatted output dropped interface method annotation:\n%s", out)
	}
}
