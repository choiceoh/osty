package lint

import (
	"testing"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
)

func TestFileMissingDocComesFromSelfhost(t *testing.T) {
	src := []byte("pub let apiVersion = 1\n")
	file, parseDiags := parser.ParseDiagnostics(src)
	if len(parseDiags) != 0 {
		t.Fatalf("parse diags = %#v", parseDiags)
	}

	got := File(file, src, nil, nil)
	if got == nil {
		t.Fatal("File returned nil result")
	}
	if len(got.Diags) != 1 {
		t.Fatalf("lint diags = %d, want 1: %#v", len(got.Diags), got.Diags)
	}
	d := got.Diags[0]
	if d.Code != diag.CodeMissingDoc {
		t.Fatalf("diag code = %q, want %q", d.Code, diag.CodeMissingDoc)
	}
	if d.Message != "public binding has no doc comment `apiVersion`" {
		t.Fatalf("diag message = %q", d.Message)
	}
}

func TestResolveAllowNameUsesRuleRegistry(t *testing.T) {
	for _, tc := range []struct {
		name string
		want string
	}{
		{name: "docs", want: diag.CodeMissingDoc},
		{name: "missing_doc", want: diag.CodeMissingDoc},
		{name: "complexity", want: diag.CodeTooManyParams},
		{name: "double_negation", want: diag.CodeDoubleNegation},
	} {
		if !containsCode(resolveAllowName(tc.name), tc.want) {
			t.Fatalf("resolveAllowName(%q) missing %q", tc.name, tc.want)
		}
	}
}

func containsCode(codes []string, want string) bool {
	for _, code := range codes {
		if code == want {
			return true
		}
	}
	return false
}
