package selfhost

import "testing"

func TestParseStableAliasesDirectly(t *testing.T) {
	src := []byte("import std.testing as t\nfunc main() {\n    while false {\n        break\n    }\n}\n")

	file, diags := Parse(src)
	if len(diags) > 0 {
		t.Fatalf("Parse returned %d diagnostics: %v", len(diags), diags[0])
	}
	if file == nil || len(file.Uses) != 1 || len(file.Decls) != 1 {
		t.Fatalf("parsed file = %#v, want one use and one decl", file)
	}
}
