package selfhost

import "testing"

func TestParserPrefixUnaryOwnsPostfixArena(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
		op   FrontTokenKind
	}{
		{name: "field", src: "!x.y", want: "field"},
		{name: "index", src: "!x[0]", want: "index"},
		{name: "call", src: "!x()", want: "call"},
		{name: "question", src: "!x?", want: "question"},
		{name: "method call", src: "!x.m()", want: "methodCall"},
		{name: "deref field", src: "*x.y", want: "field", op: FrontTokenKind(&FrontTokenKind_FrontStar{})},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			arena, exprIdx := parseArenaProbeExpr(t, tc.src)
			root := arenaNodeForTest(t, arena, exprIdx)
			if got := arenaKindForTest(root); got != "unary" {
				t.Fatalf("root kind = %s, want unary", got)
			}
			wantOp := FrontTokenKind(&FrontTokenKind_FrontNot{})
			if tc.op != nil {
				wantOp = tc.op
			}
			if !ostyEqual(root.op, wantOp) {
				t.Fatalf("root op = %#v, want %#v", root.op, wantOp)
			}
			inner := arenaNodeForTest(t, arena, root.left)
			switch tc.want {
			case "field":
				requireArenaKindForTest(t, inner, "field")
				requireArenaIdentForTest(t, arena, inner.left, "x")
			case "index":
				requireArenaKindForTest(t, inner, "index")
				requireArenaIdentForTest(t, arena, inner.left, "x")
			case "call":
				requireArenaKindForTest(t, inner, "call")
				requireArenaIdentForTest(t, arena, inner.left, "x")
			case "question":
				requireArenaKindForTest(t, inner, "question")
				requireArenaIdentForTest(t, arena, inner.left, "x")
			case "methodCall":
				requireArenaKindForTest(t, inner, "call")
				callee := arenaNodeForTest(t, arena, inner.left)
				requireArenaKindForTest(t, callee, "field")
				requireArenaIdentForTest(t, arena, callee.left, "x")
				if callee.text != "m" {
					t.Fatalf("method name = %q, want m", callee.text)
				}
			default:
				t.Fatalf("unknown case %q", tc.want)
			}
		})
	}
}

func parseArenaProbeExpr(t *testing.T, expr string) (*AstArena, int) {
	t.Helper()
	run := Run([]byte("fn __test() {\n    let _probe = " + expr + "\n}\n"))
	if diags := run.Diagnostics(); len(diags) > 0 {
		t.Fatalf("Run diagnostics = %#v", diags)
	}
	arena := run.parser.arena
	if arena == nil || len(arena.decls) == 0 {
		t.Fatalf("missing parser arena decls")
	}
	fn := arenaNodeForTest(t, arena, arena.decls[0])
	requireArenaKindForTest(t, fn, "fn")
	body := arenaNodeForTest(t, arena, fn.right)
	requireArenaKindForTest(t, body, "block")
	if len(body.children) == 0 {
		t.Fatalf("probe function body is empty")
	}
	stmt := arenaNodeForTest(t, arena, body.children[0])
	requireArenaKindForTest(t, stmt, "let")
	return arena, stmt.right
}

func requireArenaIdentForTest(t *testing.T, arena *AstArena, idx int, want string) {
	t.Helper()
	n := arenaNodeForTest(t, arena, idx)
	requireArenaKindForTest(t, n, "ident")
	if n.text != want {
		t.Fatalf("ident text = %q, want %q", n.text, want)
	}
}

func requireArenaKindForTest(t *testing.T, n *AstNode, want string) {
	t.Helper()
	if got := arenaKindForTest(n); got != want {
		t.Fatalf("node kind = %s, want %s", got, want)
	}
}

func arenaNodeForTest(t *testing.T, arena *AstArena, idx int) *AstNode {
	t.Helper()
	if arena == nil || idx < 0 || idx >= len(arena.nodes) || arena.nodes[idx] == nil {
		t.Fatalf("invalid arena node index %d", idx)
	}
	return arena.nodes[idx]
}

func arenaKindForTest(n *AstNode) string {
	if n == nil {
		return "<nil>"
	}
	switch n.kind.(type) {
	case *AstNodeKind_AstNIdent:
		return "ident"
	case *AstNodeKind_AstNUnary:
		return "unary"
	case *AstNodeKind_AstNCall:
		return "call"
	case *AstNodeKind_AstNField:
		return "field"
	case *AstNodeKind_AstNIndex:
		return "index"
	case *AstNodeKind_AstNQuestion:
		return "question"
	case *AstNodeKind_AstNBlock:
		return "block"
	case *AstNodeKind_AstNLet:
		return "let"
	case *AstNodeKind_AstNFnDecl:
		return "fn"
	case *AstNodeKind_AstNExprStmt:
		return "exprStmt"
	case *AstNodeKind_AstNFor:
		return "for"
	case *AstNodeKind_AstNRange:
		return "range"
	default:
		return "other"
	}
}
