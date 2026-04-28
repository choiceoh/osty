package selfhost

import "testing"

func TestParseStableAliasesDirectly(t *testing.T) {
	src := []byte("import std.testing as t\nfunc main() {\n    while false {\n        break\n    }\n}\n")

	run := Run(src)
	aliases := run.StableAliases()
	if got, want := len(aliases), 3; got != want {
		t.Fatalf("StableAliases len = %d, want %d: %#v", got, want, aliases)
	}
	if aliases[0].Alias != "import" || aliases[0].Canonical != "use" {
		t.Fatalf("first alias = %#v, want import -> use", aliases[0])
	}
	if aliases[1].Alias != "func" || aliases[1].Canonical != "fn" {
		t.Fatalf("second alias = %#v, want func -> fn", aliases[1])
	}
	if aliases[2].Alias != "while" || aliases[2].Canonical != "for" {
		t.Fatalf("third alias = %#v, want while -> for", aliases[2])
	}

	file, diags := run.File(), run.Diagnostics()
	if len(diags) > 0 {
		t.Fatalf("Parse returned %d diagnostics: %v", len(diags), diags[0])
	}
	if file == nil || len(file.Uses) != 1 || len(file.Decls) != 1 {
		t.Fatalf("parsed file = %#v, want one use and one decl", file)
	}
}

func TestStableAliasesPreserveIdentifierPositions(t *testing.T) {
	src := []byte("fn main(def: Int) {\n    let mut while = def\n    let field = item.func\n}\n")
	run := Run(src)
	aliases := run.StableAliases()
	if got, want := len(aliases), 0; got != want {
		t.Fatalf("StableAliases len = %d, want %d: %#v", got, want, aliases)
	}
}
