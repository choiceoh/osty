package llvmgen

import (
	"testing"

	"github.com/osty/osty/internal/ast"
)

// TestDebugPatternShape locks the short-form labels emitted by the
// LLVM013 / LLVM015 family of "what did the user write here?"
// diagnostics. A bare ident (`PreNone`) collapses to `ident "PreNone"`,
// a payload variant call (`PreNumber(n)`) collapses to
// `variant PreNumber with 1 arg(s)`, and so on. The probe driver greps
// these labels to localize first walls in merged toolchain runs, so
// keeping the shape stable matters more than the exact wording.
func TestDebugPatternShape(t *testing.T) {
	cases := []struct {
		name    string
		pattern ast.Pattern
		want    string
	}{
		{"nil", nil, "<nil>"},
		{"ident", &ast.IdentPat{Name: "PreNone"}, `ident "PreNone"`},
		{"variant_no_args", &ast.VariantPat{Path: []string{"None"}}, "variant None (no args)"},
		{"variant_qualified_no_args", &ast.VariantPat{Path: []string{"Pre", "None"}}, "variant Pre.None (no args)"},
		{"variant_one_arg", &ast.VariantPat{Path: []string{"PreNumber"}, Args: []ast.Pattern{&ast.IdentPat{Name: "n"}}}, "variant PreNumber with 1 arg(s)"},
		{"variant_two_args", &ast.VariantPat{Path: []string{"Rect"}, Args: []ast.Pattern{&ast.IdentPat{Name: "w"}, &ast.IdentPat{Name: "h"}}}, "variant Rect with 2 arg(s)"},
		{"wildcard", &ast.WildcardPat{}, "wildcard"},
		{"literal", &ast.LiteralPat{}, "literal"},
		{"range", &ast.RangePat{}, "range"},
		{"or", &ast.OrPat{}, "or-pattern"},
		{"binding", &ast.BindingPat{Name: "x"}, "binding"},
		{"tuple_2", &ast.TuplePat{Elems: []ast.Pattern{&ast.IdentPat{Name: "a"}, &ast.IdentPat{Name: "b"}}}, "tuple of 2"},
		{"struct_unqualified", &ast.StructPat{Type: []string{"Point"}}, "struct Point"},
		{"struct_qualified", &ast.StructPat{Type: []string{"geom", "Point"}}, "struct geom.Point"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := debugPatternShape(c.pattern); got != c.want {
				t.Fatalf("debugPatternShape(%T) = %q, want %q", c.pattern, got, c.want)
			}
		})
	}
}
