package backend

import (
	"errors"
	"strings"
	"testing"

	"github.com/osty/osty/internal/ir"
)

func TestFinalizeEntryModuleRejectsMIRLoweringIssues(t *testing.T) {
	rangeLit := &ir.RangeLit{
		Start: &ir.IntLit{Text: "0", T: ir.TInt},
		End:   &ir.IntLit{Text: "1", T: ir.TInt},
		T:     ir.TUnit,
	}
	mod := &ir.Module{
		Package: "main",
		Decls: []ir.Decl{
			&ir.FnDecl{
				Name:   "main",
				Return: ir.TUnit,
				Body: &ir.Block{Stmts: []ir.Stmt{
					&ir.LetStmt{Name: "r", Type: ir.TUnit, Value: rangeLit},
				}},
			},
		},
	}

	entry, err := finalizeEntryModule(Entry{PackageName: "main"}, mod)
	if err == nil {
		t.Fatal("finalizeEntryModule returned nil error for MIR lowering issue")
	}
	if !errors.Is(err, ErrMIRCoverageIncomplete) {
		t.Fatalf("error = %v, want ErrMIRCoverageIncomplete", err)
	}
	if len(entry.MIRIssues) == 0 {
		t.Fatal("entry.MIRIssues empty, want recorded MIR coverage issue")
	}
	if got := entry.MIRIssues[0].Error(); !strings.Contains(got, "range literal in value position") {
		t.Fatalf("first MIR issue = %q, want range literal coverage issue", got)
	}
}
