package parser

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/ast"
)

// TestParseStructLitSpreadWithFields pins G26 spread-update semantics
// (LANG_SPEC_v0.5 / OSTY_GRAMMAR_v0.5 §R5: `..expr` in a struct literal
// is a spread-update, may appear at most once, and may freely combine
// with explicit fields). Until #935 the parser broke out of the field
// loop the moment it consumed a spread, silently rejecting every
// `T { ..x, field: v }` shape — including the canonical example the
// spec uses.
func TestParseStructLitSpreadWithFields(t *testing.T) {
	for _, tt := range []struct {
		name string
		src  string
	}{
		{"spread_then_field", "struct P { x: Int, y: Int }\nfn f(p: P) -> P { P { ..p, x: 1 } }\n"},
		{"spread_then_two_fields", "struct P { x: Int, y: Int, z: Int }\nfn f(p: P) -> P { P { ..p, x: 1, y: 2 } }\n"},
		{"field_then_spread", "struct P { x: Int, y: Int }\nfn f(p: P) -> P { P { x: 1, ..p } }\n"},
		{"spread_only", "struct P { x: Int }\nfn f(p: P) -> P { P { ..p } }\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			file, diags := ParseDiagnostics([]byte(tt.src))
			if len(diags) > 0 {
				t.Fatalf("unexpected diagnostics: %v", diags)
			}
			lit := findStructLit(file)
			if lit == nil {
				t.Fatal("no StructLit in parsed file")
			}
			if lit.Spread == nil {
				t.Errorf("Spread = nil, want non-nil")
			}
		})
	}
}

// TestParseStructLitDuplicateSpread enforces the "max one spread" rule
// from §R5. Two `..` operands in the same literal should surface E0204
// with an explicit "remove the duplicate spread" hint.
func TestParseStructLitDuplicateSpread(t *testing.T) {
	src := []byte("struct P { x: Int }\nfn f(p: P, q: P) -> P { P { ..p, ..q } }\n")
	_, diags := ParseDiagnostics(src)
	if len(diags) == 0 {
		t.Fatal("expected diagnostic for duplicate spread, got none")
	}
	found := false
	for _, d := range diags {
		if d.Code == "E0204" && strings.Contains(d.Message, "at most one") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected E0204 'at most one' diagnostic, got: %v", diags)
	}
}

// findStructLit returns the first StructLit it finds reachable from
// the file body. The G26 fixtures always shape the source so the
// literal is the only expression of the only function — descending
// the FnDecl → Block → ExprStmt chain is enough.
func findStructLit(file *ast.File) *ast.StructLit {
	if file == nil {
		return nil
	}
	for _, d := range file.Decls {
		fn, ok := d.(*ast.FnDecl)
		if !ok || fn.Body == nil {
			continue
		}
		for _, stmt := range fn.Body.Stmts {
			es, ok := stmt.(*ast.ExprStmt)
			if !ok {
				continue
			}
			if lit, ok := es.X.(*ast.StructLit); ok {
				return lit
			}
		}
	}
	return nil
}
