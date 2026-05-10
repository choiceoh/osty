package parser

import (
	"testing"

	"github.com/osty/osty/internal/ast"
)

func TestParseFollowOnRecoveryA2ElseNewlineTopLevelDecl(t *testing.T) {
	src := fixtureFollowOnAElseNewline("rfA2ElseNewlineTopLevelDecl", "rfA2Cond")

	result := ParseDetailed(src)
	fn, _ := requireFollowOnFnWithHelper(t, result, 0, 1, 1, 1, "rfA2Cond", "E0105", "E0204", "E0100")
	requireParseDiagnosticCodePresent(t, result, "E0100")
	stmt := requireExprStmtAt(t, fn.Body.Stmts, 0, "expression statement")
	ifExpr, ok := stmt.X.(*ast.IfExpr)
	if !ok || ifExpr.Else != nil {
		t.Fatalf("stmt[0].X = %#v, want if expr with nil else after follow-on recovery", stmt.X)
	}
}
