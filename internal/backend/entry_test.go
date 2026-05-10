package backend

import (
	"testing"

	"github.com/osty/osty/internal/ir"
)

func TestLowerEntryMIRAcceptsRangeLitValuePosition(t *testing.T) {
	// RangeLit in value position (not directly in for-in) used to emit
	// ErrMIRCoverageIncomplete. After the MIR lowering improvement, it
	// should succeed and produce a valid MIR module.
	rangeLit := &ir.RangeLit{
		Start:     &ir.IntLit{Text: "0", T: ir.TInt},
		End:       &ir.IntLit{Text: "10", T: ir.TInt},
		T:         &ir.NamedType{Name: "Range", Args: []ir.Type{ir.TInt}, Builtin: true},
		Inclusive: false,
	}
	mod := &ir.Module{
		Package: "main",
		Decls: []ir.Decl{
			&ir.FnDecl{
				Name:   "main",
				Return: ir.TUnit,
				Body: &ir.Block{Stmts: []ir.Stmt{
					&ir.LetStmt{Name: "r", Type: rangeLit.T, Value: rangeLit},
				}},
			},
		},
	}

	entry, err := finalizeEntryIR(Entry{PackageName: "main"}, mod)
	if err != nil {
		t.Fatalf("finalizeEntryIR returned error before MIR lowering: %v", err)
	}
	entry, err = LowerEntryMIR(entry)
	if err != nil {
		t.Fatalf("LowerEntryMIR returned error for RangeLit value position: %v", err)
	}
	if len(entry.MIRIssues) > 0 {
		for _, iss := range entry.MIRIssues {
			t.Errorf("unexpected MIR issue: %s", iss.Error())
		}
	}
	if entry.MIR == nil {
		t.Fatal("entry.MIR is nil after lowering")
	}
}
