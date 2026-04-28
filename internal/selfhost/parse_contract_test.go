package selfhost

import (
	"testing"

	"github.com/osty/osty/internal/ast"
)

func TestParseGrammarContracts(t *testing.T) {
	t.Run("stable aliases are parsed and reported", func(t *testing.T) {
		run := Run([]byte("import std.testing as t\nfunc main() {\n    while false { break }\n}\n"))
		if diags := run.Diagnostics(); len(diags) != 0 {
			t.Fatalf("Diagnostics = %#v, want none", diags)
		}
		if got, want := len(run.StableAliases()), 3; got != want {
			t.Fatalf("StableAliases len = %d, want %d", got, want)
		}
		file := LowerPublicFileFromRun(run)
		if file == nil || len(file.Uses) != 1 || len(file.Decls) != 1 {
			t.Fatalf("public file shape = %#v, want one use and one decl", file)
		}
	})

	t.Run("compatibility helpers lower in the semantic arena", func(t *testing.T) {
		run := Run([]byte("fn main() {\n    let mut items = [1]\n    let count = len(items)\n    items = append(items, count)\n}\n"))
		if diags := run.Diagnostics(); len(diags) != 0 {
			t.Fatalf("Diagnostics = %#v, want none", diags)
		}
		lowerings := run.StableLowerings()
		if got, want := len(lowerings), 2; got != want {
			t.Fatalf("StableLowerings len = %d, want %d: %#v", got, want, lowerings)
		}
		file := LowerPublicFileFromRun(run)
		fn := file.Decls[0].(*ast.FnDecl)
		countLet := fn.Body.Stmts[1].(*ast.LetStmt)
		call := countLet.Value.(*ast.CallExpr)
		if field, ok := call.Fn.(*ast.FieldExpr); !ok || field.Name != "len" {
			t.Fatalf("len lowering callee = %T %#v, want .len call", call.Fn, call.Fn)
		}
		if _, ok := fn.Body.Stmts[2].(*ast.ExprStmt); !ok {
			t.Fatalf("append lowering stmt type = %T, want *ast.ExprStmt", fn.Body.Stmts[2])
		}
	})

	t.Run("non associative operators report parser diagnostics", func(t *testing.T) {
		run := Run([]byte("fn main() {\n    let cmp = a < b < c\n    let range = 1..=2..3\n}\n"))
		var count int
		for _, d := range run.Diagnostics() {
			if d.Code == "E0200" {
				count++
			}
		}
		if count != 2 {
			t.Fatalf("E0200 count = %d, want 2; diagnostics=%#v", count, run.Diagnostics())
		}
	})
}
