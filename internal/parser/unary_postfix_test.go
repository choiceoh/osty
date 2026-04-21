package parser

import (
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/token"
)

// parseExprFromFn parses `fn __test() -> X { <expr> }` (or a statement-ish
// wrapper) and returns the final expression. Used to pin down exactly what
// the post-lowering AST shape looks like for a given source fragment.
func parseExprInMain(t *testing.T, src string) ast.Expr {
	t.Helper()
	file, diags := ParseDiagnostics([]byte("fn __test() { let _probe = " + src + "\n}\n"))
	if len(diags) > 0 {
		t.Fatalf("ParseDiagnostics returned %d diagnostics: %v", len(diags), diags[0])
	}
	if file == nil {
		t.Fatalf("ParseDiagnostics returned nil file")
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FnDecl)
		if !ok || fn.Name != "__test" || fn.Body == nil {
			continue
		}
		for _, stmt := range fn.Body.Stmts {
			letStmt, ok := stmt.(*ast.LetStmt)
			if !ok {
				continue
			}
			return letStmt.Value
		}
	}
	t.Fatalf("failed to locate probe expression in parsed AST")
	return nil
}

// TestUnaryPostfixHoistingField is the canonical regression for the
// `!pmOut.exhaustive` wall. Per grammar UnaryExpr sits above
// PostfixExpr, so the parsed tree must be `Unary{!, Field{pmOut,
// exhaustive}}` not `Field{Unary{!, pmOut}, exhaustive}`. The
// self-hosted front-end emits the second shape; lower.go's
// `hoistUnaryOverPostfix` fixes it up during the stable-AST lowering
// pass.
func TestUnaryPostfixHoistingField(t *testing.T) {
	expr := parseExprInMain(t, "!foo.bar")
	u, ok := expr.(*ast.UnaryExpr)
	if !ok {
		t.Fatalf("root type = %T, want *ast.UnaryExpr", expr)
	}
	if u.Op != token.NOT {
		t.Fatalf("root op = %s, want !", u.Op)
	}
	field, ok := u.X.(*ast.FieldExpr)
	if !ok {
		t.Fatalf("inner type = %T, want *ast.FieldExpr", u.X)
	}
	if field.Name != "bar" {
		t.Fatalf("inner field name = %q, want %q", field.Name, "bar")
	}
	if id, ok := field.X.(*ast.Ident); !ok || id.Name != "foo" {
		t.Fatalf("deepest type = %T (%v), want Ident(foo)", field.X, field.X)
	}
}

func TestUnaryPostfixHoistingIndex(t *testing.T) {
	expr := parseExprInMain(t, "!items[0]")
	u, ok := expr.(*ast.UnaryExpr)
	if !ok {
		t.Fatalf("root type = %T, want *ast.UnaryExpr", expr)
	}
	if _, ok := u.X.(*ast.IndexExpr); !ok {
		t.Fatalf("inner type = %T, want *ast.IndexExpr", u.X)
	}
}

func TestUnaryPostfixHoistingCall(t *testing.T) {
	expr := parseExprInMain(t, "!check()")
	u, ok := expr.(*ast.UnaryExpr)
	if !ok {
		t.Fatalf("root type = %T, want *ast.UnaryExpr", expr)
	}
	if _, ok := u.X.(*ast.CallExpr); !ok {
		t.Fatalf("inner type = %T, want *ast.CallExpr", u.X)
	}
}

// TestUnaryPostfixHoistingFieldMethodCall covers the compound shape
// `!obj.method()` — the front-end first builds
// `Call{Field{Unary{!, obj}, method}}`; hoisting fires twice (once for
// the Field, once for the Call) to produce `Unary{!, Call{Field{obj,
// method}}}`.
func TestUnaryPostfixHoistingFieldMethodCall(t *testing.T) {
	expr := parseExprInMain(t, "!outcome.isEmpty()")
	u, ok := expr.(*ast.UnaryExpr)
	if !ok {
		t.Fatalf("root type = %T, want *ast.UnaryExpr", expr)
	}
	call, ok := u.X.(*ast.CallExpr)
	if !ok {
		t.Fatalf("inner type = %T, want *ast.CallExpr", u.X)
	}
	field, ok := call.Fn.(*ast.FieldExpr)
	if !ok {
		t.Fatalf("call.Fn type = %T, want *ast.FieldExpr", call.Fn)
	}
	if field.Name != "isEmpty" {
		t.Fatalf("field name = %q, want %q", field.Name, "isEmpty")
	}
	if id, ok := field.X.(*ast.Ident); !ok || id.Name != "outcome" {
		t.Fatalf("receiver = %T (%v), want Ident(outcome)", field.X, field.X)
	}
}

// TestUnaryPostfixHoistingMinus and TestUnaryPostfixHoistingBitNot pin
// the same behaviour for the other prefix operators so future regressions
// in `-x.y` / `~x.y` surface immediately.
func TestUnaryPostfixHoistingMinus(t *testing.T) {
	expr := parseExprInMain(t, "-point.x")
	u, ok := expr.(*ast.UnaryExpr)
	if !ok {
		t.Fatalf("root type = %T, want *ast.UnaryExpr", expr)
	}
	if u.Op != token.MINUS {
		t.Fatalf("op = %s, want -", u.Op)
	}
	if _, ok := u.X.(*ast.FieldExpr); !ok {
		t.Fatalf("inner type = %T, want *ast.FieldExpr", u.X)
	}
}

func TestUnaryPostfixHoistingBitNot(t *testing.T) {
	expr := parseExprInMain(t, "~mask.bits")
	u, ok := expr.(*ast.UnaryExpr)
	if !ok {
		t.Fatalf("root type = %T, want *ast.UnaryExpr", expr)
	}
	if u.Op != token.BITNOT {
		t.Fatalf("op = %s, want ~", u.Op)
	}
}
