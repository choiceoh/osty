package format

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/diag"
)

func TestSourceDoesNotMaterializePublicAST(t *testing.T) {
	src := []byte(`fn main() {
let x = 1
x
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
	if len(out) == 0 {
		t.Fatal("Source returned empty formatted output")
	}
}

func TestSourcePreservesInterfaceMethodAnnotations(t *testing.T) {
	src := []byte(`#[reproducible_capability]
interface HashCap {
    #[reproducible]
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
	if !strings.Contains(string(out), "    #[reproducible]\n    fn hash(self, value: String) -> String") {
		t.Fatalf("formatted output dropped interface method annotation:\n%s", out)
	}
}
