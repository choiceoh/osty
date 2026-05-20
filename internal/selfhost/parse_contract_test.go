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

	t.Run("parser canonicalizes compatibility helpers in the raw arena", func(t *testing.T) {
		run := Run([]byte("fn main() {\n    let mut items = [1]\n    let count = len(items)\n    items = append(items, count)\n}\n"))
		if diags := run.Diagnostics(); len(diags) != 0 {
			t.Fatalf("Diagnostics = %#v, want none", diags)
		}
		arena := run.parser.arena
		fnNode := arenaNodeForTest(t, arena, arena.decls[0])
		body := arenaNodeForTest(t, arena, fnNode.right)
		if got, want := len(body.children), 3; got != want {
			t.Fatalf("raw body stmt count = %d, want %d", got, want)
		}
		countLetRaw := arenaNodeForTest(t, arena, body.children[1])
		requireArenaKindForTest(t, countLetRaw, "let")
		countCallRaw := arenaNodeForTest(t, arena, countLetRaw.right)
		requireArenaKindForTest(t, countCallRaw, "call")
		countCalleeRaw := arenaNodeForTest(t, arena, countCallRaw.left)
		requireArenaKindForTest(t, countCalleeRaw, "field")
		if countCalleeRaw.text != "len" {
			t.Fatalf("raw len callee field = %q, want len", countCalleeRaw.text)
		}
		appendStmtRaw := arenaNodeForTest(t, arena, body.children[2])
		requireArenaKindForTest(t, appendStmtRaw, "exprStmt")
		appendCallRaw := arenaNodeForTest(t, arena, appendStmtRaw.left)
		requireArenaKindForTest(t, appendCallRaw, "call")
		appendCalleeRaw := arenaNodeForTest(t, arena, appendCallRaw.left)
		requireArenaKindForTest(t, appendCalleeRaw, "field")
		if appendCalleeRaw.text != "push" {
			t.Fatalf("raw append callee field = %q, want push", appendCalleeRaw.text)
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

	t.Run("parser canonicalizes append let bindings in the raw arena", func(t *testing.T) {
		run := Run([]byte("fn main() {\n    let items = [1]\n    let count = len(items)\n    let more = append(items, count)\n}\n"))
		if diags := run.Diagnostics(); len(diags) != 0 {
			t.Fatalf("Diagnostics = %#v, want none", diags)
		}
		arena := run.parser.arena
		fnNode := arenaNodeForTest(t, arena, arena.decls[0])
		body := arenaNodeForTest(t, arena, fnNode.right)
		if got, want := len(body.children), 4; got != want {
			t.Fatalf("raw body stmt count = %d, want %d", got, want)
		}
		moreLet := arenaNodeForTest(t, arena, body.children[2])
		requireArenaKindForTest(t, moreLet, "let")
		if moreLet.flags != 1 {
			t.Fatalf("append let flags = %d, want mutable canonical binding", moreLet.flags)
		}
		moreValue := arenaNodeForTest(t, arena, moreLet.right)
		requireArenaKindForTest(t, moreValue, "ident")
		if got := moreValue.text; got != "items" {
			t.Fatalf("append let value = %q, want items", got)
		}
		pushStmt := arenaNodeForTest(t, arena, body.children[3])
		requireArenaKindForTest(t, pushStmt, "exprStmt")
		pushCall := arenaNodeForTest(t, arena, pushStmt.left)
		requireArenaKindForTest(t, pushCall, "call")
		pushCallee := arenaNodeForTest(t, arena, pushCall.left)
		requireArenaKindForTest(t, pushCallee, "field")
		if pushCallee.text != "push" {
			t.Fatalf("append let follow-up callee = %q, want push", pushCallee.text)
		}
	})

	t.Run("parser canonicalizes enumerate in the raw arena", func(t *testing.T) {
		run := Run([]byte("fn main() {\n    let items = [1]\n    for (_, item) in enumerate(items) {\n        println(item)\n    }\n}\n"))
		if diags := run.Diagnostics(); len(diags) != 0 {
			t.Fatalf("Diagnostics = %#v, want none", diags)
		}
		arena := run.parser.arena
		fnNode := arenaNodeForTest(t, arena, arena.decls[0])
		body := arenaNodeForTest(t, arena, fnNode.right)
		if got, want := len(body.children), 3; got != want {
			t.Fatalf("raw body stmt count = %d, want %d", got, want)
		}
		tempLet := arenaNodeForTest(t, arena, body.children[1])
		requireArenaKindForTest(t, tempLet, "let")
		loop := arenaNodeForTest(t, arena, body.children[2])
		requireArenaKindForTest(t, loop, "for")
		iter := arenaNodeForTest(t, arena, loop.children[1])
		requireArenaKindForTest(t, iter, "range")
		stop := arenaNodeForTest(t, arena, iter.right)
		requireArenaKindForTest(t, stop, "call")
		callee := arenaNodeForTest(t, arena, stop.left)
		requireArenaKindForTest(t, callee, "field")
		if callee.text != "len" {
			t.Fatalf("enumerate range stop = %q, want len", callee.text)
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

func TestLowerPublicFileUsesSelfhostStableNodeIDs(t *testing.T) {
	run := Run([]byte("fn main() {\n    let x = len([1])\n}\n"))
	if diags := run.Diagnostics(); len(diags) != 0 {
		t.Fatalf("Diagnostics = %#v, want none", diags)
	}

	file := LowerPublicFileFromRun(run)
	if file == nil || len(file.Decls) != 1 {
		t.Fatalf("public file shape = %#v, want one decl", file)
	}
	if file.ID != 1 {
		t.Fatalf("file ID = %d, want stable public root ID 1", file.ID)
	}

	arena := run.parser.arena
	fnIdx := arena.decls[0]
	fnNode := arenaNodeForTest(t, arena, fnIdx)
	bodyNode := arenaNodeForTest(t, arena, fnNode.right)
	letIdx := bodyNode.children[0]
	letNode := arenaNodeForTest(t, arena, letIdx)
	callIdx := letNode.right

	fn := file.Decls[0].(*ast.FnDecl)
	if got, want := fn.ID, ast.NodeID(fnIdx+2); got != want {
		t.Fatalf("fn ID = %d, want selfhost arena stable ID %d", got, want)
	}
	stmt := fn.Body.Stmts[0].(*ast.LetStmt)
	if got, want := stmt.ID, ast.NodeID(letIdx+2); got != want {
		t.Fatalf("let stmt ID = %d, want selfhost arena stable ID %d", got, want)
	}
	if got, want := stmt.Value.(*ast.CallExpr).ID, ast.NodeID(callIdx+2); got != want {
		t.Fatalf("call ID = %d, want selfhost arena stable ID %d", got, want)
	}
}
