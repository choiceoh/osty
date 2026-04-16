package mir

import (
	"testing"

	"github.com/osty/osty/internal/ir"
)

// TestMIRDumpExample exercises the printer on a richer module so the
// canonical output format is recorded alongside the lowering tests.
// Running `go test -run TestMIRDumpExample -v ./internal/mir/` prints
// the MIR for manual inspection.
func TestMIRDumpExample(t *testing.T) {
	maybeT := &ir.NamedType{Name: "Maybe"}
	enumDecl := &ir.EnumDecl{
		Name: "Maybe",
		Variants: []*ir.Variant{
			{Name: "Some", Payload: []ir.Type{ir.TInt}},
			{Name: "None"},
		},
	}
	arms := []*ir.MatchArm{
		{
			Pattern: &ir.VariantPat{Enum: "Maybe", Variant: "Some",
				Args: []ir.Pattern{&ir.IdentPat{Name: "x"}}},
			Body: &ir.Block{Result: &ir.Ident{Name: "x", Kind: ir.IdentLocal, T: ir.TInt}},
		},
		{
			Pattern: &ir.VariantPat{Enum: "Maybe", Variant: "None"},
			Body:    &ir.Block{Result: &ir.IntLit{Text: "0", T: ir.TInt}},
		},
	}
	tree := ir.CompileDecisionTree(maybeT, arms)
	scoreFn := &ir.FnDecl{
		Name:   "score",
		Return: ir.TInt,
		Params: []*ir.Param{{Name: "m", Type: maybeT}},
		Body: &ir.Block{
			Result: &ir.MatchExpr{
				Scrutinee: &ir.Ident{Name: "m", Kind: ir.IdentParam, T: maybeT},
				Arms:      arms,
				Tree:      tree,
				T:         ir.TInt,
			},
		},
	}
	mod := &ir.Module{Package: "main", Decls: []ir.Decl{enumDecl, scoreFn}}
	out := Lower(mod)
	if errs := Validate(out); len(errs) > 0 {
		t.Fatalf("validate: %v", errs)
	}
	t.Logf("MIR:\n%s", Print(out))
}
