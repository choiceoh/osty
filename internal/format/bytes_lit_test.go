package format

import (
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/token"
)

func TestFormatBytesLit(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  string
	}{
		{"empty", "", "b\"\""},
		{"single_quote_bug", "\"", "b\"\\\"\""},
		{"hello", "hello", "b\"hello\""},
		{"with_null", "\x00", "b\"\\0\""},
		{"with_bytes", "\x00\x01\xff", "b\"\\0\\x01\\xFF\""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lit := &ast.BytesLit{Value: tc.value, PosV: token.Pos{Offset: 0}, EndV: token.Pos{Offset: 10}}
			got := string(File(&ast.File{Decls: []ast.Decl{&ast.FnDecl{
				Name: "test",
				Body: &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{X: lit}}},
			}}}))
			if !contains(got, tc.want) {
				t.Errorf("got %q, want to contain %q", got, tc.want)
			}
		})
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		(len(s) > 0 && len(sub) > 0 && findSub(s, sub)))
}

func findSub(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
