package parser

import (
	"fmt"
	"testing"

	"github.com/osty/osty/internal/ast"
)

// TestParseCharLiteralAllAsciiLetters guards against a regression where
// `'b'` (the ASCII letter b as a Char literal) decoded to U+FFFD.
// Root cause: astLowerDecodedLiteral unconditionally trimmed a leading
// "b" from the already-decoded token value, which collapsed the single
// character "b" to "" before firstRune ran. The fix requires the prefix
// to be followed by `'` so only byte-literal source (`b'A'`) is trimmed.
func TestParseCharLiteralAllAsciiLetters(t *testing.T) {
	for r := 'a'; r <= 'z'; r++ {
		r := r
		t.Run(fmt.Sprintf("lower_%c", r), func(t *testing.T) {
			got := parseCharLitValue(t, fmt.Sprintf("pub let c: Char = '%c'\n", r))
			if got != r {
				t.Fatalf("CharLit('%c') = U+%04X, want U+%04X", r, got, r)
			}
		})
	}
	for r := 'A'; r <= 'Z'; r++ {
		r := r
		t.Run(fmt.Sprintf("upper_%c", r), func(t *testing.T) {
			got := parseCharLitValue(t, fmt.Sprintf("pub let c: Char = '%c'\n", r))
			if got != r {
				t.Fatalf("CharLit('%c') = U+%04X, want U+%04X", r, got, r)
			}
		})
	}
}

func TestParseByteLiteralAllAsciiLetters(t *testing.T) {
	for r := 'a'; r <= 'z'; r++ {
		r := r
		t.Run(fmt.Sprintf("lower_%c", r), func(t *testing.T) {
			got := parseByteLitValue(t, fmt.Sprintf("pub let c: Byte = b'%c'\n", r))
			if got != byte(r) {
				t.Fatalf("ByteLit(b'%c') = %d, want %d", r, got, byte(r))
			}
		})
	}
}

func parseCharLitValue(t *testing.T, src string) rune {
	t.Helper()
	lit, ok := firstLetValue(t, src).(*ast.CharLit)
	if !ok {
		t.Fatalf("let value %T, want *ast.CharLit", firstLetValue(t, src))
	}
	return lit.Value
}

func parseByteLitValue(t *testing.T, src string) byte {
	t.Helper()
	lit, ok := firstLetValue(t, src).(*ast.ByteLit)
	if !ok {
		t.Fatalf("let value %T, want *ast.ByteLit", firstLetValue(t, src))
	}
	return lit.Value
}

func firstLetValue(t *testing.T, src string) ast.Expr {
	t.Helper()
	file, diags := ParseDiagnostics([]byte(src))
	if len(diags) > 0 {
		t.Fatalf("unexpected diagnostics: %v", diags[0])
	}
	if file == nil || len(file.Decls) == 0 {
		t.Fatalf("no decls parsed from %q", src)
	}
	let, ok := file.Decls[0].(*ast.LetDecl)
	if !ok {
		t.Fatalf("decl %T, want *ast.LetDecl", file.Decls[0])
	}
	return let.Value
}
