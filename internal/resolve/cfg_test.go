package resolve

import (
	"testing"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
)

func TestEvaluateCfgOnDeclSuggestsNearestSupportedKey(t *testing.T) {
	src := []byte(`#[cfg(taret = "linux")]
fn main() {}
`)
	file, diags := parser.ParseCanonical(src)
	if len(diags) != 0 {
		t.Fatalf("ParseCanonical diagnostics = %#v, want none", diags)
	}
	if file == nil || len(file.Decls) != 1 {
		t.Fatalf("parsed file = %#v, want one declaration", file)
	}
	pass, ds := evaluateCfgOnDecl(file.Decls[0], &CfgEnv{
		OS:       "linux",
		Arch:     "amd64",
		Target:   "linux",
		Features: map[string]bool{},
	})
	if pass {
		t.Fatal("evaluateCfgOnDecl passed, want false for unknown cfg key")
	}
	if len(ds) != 1 {
		t.Fatalf("diagnostics = %#v, want 1", ds)
	}
	if got := ds[0].Code; got != diag.CodeCfgUnknownKey {
		t.Fatalf("diag code = %q, want %q", got, diag.CodeCfgUnknownKey)
	}
	if got := ds[0].Hint; got != "did you mean `target`?" {
		t.Fatalf("diag hint = %q, want did-you-mean target", got)
	}
}
