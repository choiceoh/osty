package backend

import (
	"testing"

	"github.com/osty/osty/internal/ir"
)

func TestLowerEntryMIRAcceptsRangeLitValuePosition(t *testing.T) {
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

func TestLowerEntryMIRAcceptsIfLetIrrefutable(t *testing.T) {
	ifLetIdent := &ir.IfLetExpr{
		Pattern:   &ir.IdentPat{Name: "x"},
		Scrutinee: &ir.IntLit{Text: "42", T: ir.TInt},
		Then: &ir.Block{
			Result: &ir.Ident{Name: "x", Kind: ir.IdentLocal, T: ir.TInt},
		},
		T:     ir.TInt,
		SpanV: ir.Span{Start: ir.Pos{Line: 1, Column: 1, Offset: 0}, End: ir.Pos{Line: 1, Column: 20, Offset: 19}},
	}
	mod := &ir.Module{
		Package: "main",
		Decls: []ir.Decl{
			&ir.FnDecl{
				Name:   "main",
				Return: ir.TInt,
				Body:   &ir.Block{Stmts: []ir.Stmt{&ir.ExprStmt{X: ifLetIdent}}},
			},
		},
	}
	entry, err := finalizeEntryIR(Entry{PackageName: "main"}, mod)
	if err != nil {
		t.Fatalf("finalizeEntryIR: %v", err)
	}
	entry, err = LowerEntryMIR(entry)
	if err != nil {
		t.Fatalf("LowerEntryMIR: %v", err)
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

func TestLowerEntryMIRAcceptsIfLetLitPat(t *testing.T) {
	ifLet := &ir.IfLetExpr{
		Pattern:   &ir.LitPat{Value: &ir.IntLit{Text: "42", T: ir.TInt}},
		Scrutinee: &ir.IntLit{Text: "42", T: ir.TInt},
		Then: &ir.Block{
			Stmts:  []ir.Stmt{},
			Result: &ir.IntLit{Text: "1", T: ir.TInt},
		},
		Else: &ir.Block{
			Result: &ir.IntLit{Text: "0", T: ir.TInt},
		},
		T:     ir.TInt,
		SpanV: ir.Span{Start: ir.Pos{Line: 1, Column: 1, Offset: 0}, End: ir.Pos{Line: 1, Column: 20, Offset: 19}},
	}
	mod := &ir.Module{
		Package: "main",
		Decls: []ir.Decl{
			&ir.FnDecl{
				Name:   "main",
				Return: ir.TInt,
				Body:   &ir.Block{Stmts: []ir.Stmt{&ir.ExprStmt{X: ifLet}}},
			},
		},
	}
	entry, err := finalizeEntryIR(Entry{PackageName: "main"}, mod)
	if err != nil {
		t.Fatalf("finalizeEntryIR: %v", err)
	}
	entry, err = LowerEntryMIR(entry)
	if err != nil {
		t.Fatalf("LowerEntryMIR: %v", err)
	}
	if len(entry.MIRIssues) > 0 {
		for _, iss := range entry.MIRIssues {
			t.Errorf("unexpected MIR issue: %s", iss.Error())
		}
	}
	if entry.MIR == nil {
		t.Fatal("entry.MIR is nil")
	}
}

func TestLowerEntryMIRAcceptsIfLetRangePat(t *testing.T) {
	ifLet := &ir.IfLetExpr{
		Pattern: &ir.RangePat{
			Low:       &ir.IntLit{Text: "0", T: ir.TInt},
			High:      &ir.IntLit{Text: "10", T: ir.TInt},
			Inclusive: true,
		},
		Scrutinee: &ir.IntLit{Text: "5", T: ir.TInt},
		Then: &ir.Block{
			Result: &ir.IntLit{Text: "1", T: ir.TInt},
		},
		Else: &ir.Block{
			Result: &ir.IntLit{Text: "0", T: ir.TInt},
		},
		T:     ir.TInt,
		SpanV: ir.Span{Start: ir.Pos{Line: 1, Column: 1, Offset: 0}, End: ir.Pos{Line: 1, Column: 20, Offset: 19}},
	}
	mod := &ir.Module{
		Package: "main",
		Decls: []ir.Decl{
			&ir.FnDecl{
				Name:   "main",
				Return: ir.TInt,
				Body:   &ir.Block{Stmts: []ir.Stmt{&ir.ExprStmt{X: ifLet}}},
			},
		},
	}
	entry, err := finalizeEntryIR(Entry{PackageName: "main"}, mod)
	if err != nil {
		t.Fatalf("finalizeEntryIR: %v", err)
	}
	entry, err = LowerEntryMIR(entry)
	if err != nil {
		t.Fatalf("LowerEntryMIR: %v", err)
	}
	if len(entry.MIRIssues) > 0 {
		for _, iss := range entry.MIRIssues {
			t.Errorf("unexpected MIR issue: %s", iss.Error())
		}
	}
	if entry.MIR == nil {
		t.Fatal("entry.MIR is nil")
	}
}

func TestLowerEntryMIRAcceptsIfLetOrPat(t *testing.T) {
	ifLet := &ir.IfLetExpr{
		Pattern: &ir.OrPat{
			Alts: []ir.Pattern{
				&ir.LitPat{Value: &ir.IntLit{Text: "1", T: ir.TInt}},
				&ir.LitPat{Value: &ir.IntLit{Text: "2", T: ir.TInt}},
			},
		},
		Scrutinee: &ir.IntLit{Text: "2", T: ir.TInt},
		Then: &ir.Block{
			Result: &ir.IntLit{Text: "1", T: ir.TInt},
		},
		Else: &ir.Block{
			Result: &ir.IntLit{Text: "0", T: ir.TInt},
		},
		T:     ir.TInt,
		SpanV: ir.Span{Start: ir.Pos{Line: 1, Column: 1, Offset: 0}, End: ir.Pos{Line: 1, Column: 20, Offset: 19}},
	}
	mod := &ir.Module{
		Package: "main",
		Decls: []ir.Decl{
			&ir.FnDecl{
				Name:   "main",
				Return: ir.TInt,
				Body:   &ir.Block{Stmts: []ir.Stmt{&ir.ExprStmt{X: ifLet}}},
			},
		},
	}
	entry, err := finalizeEntryIR(Entry{PackageName: "main"}, mod)
	if err != nil {
		t.Fatalf("finalizeEntryIR: %v", err)
	}
	entry, err = LowerEntryMIR(entry)
	if err != nil {
		t.Fatalf("LowerEntryMIR: %v", err)
	}
	if len(entry.MIRIssues) > 0 {
		for _, iss := range entry.MIRIssues {
			t.Errorf("unexpected MIR issue: %s", iss.Error())
		}
	}
	if entry.MIR == nil {
		t.Fatal("entry.MIR is nil")
	}
}

func TestLowerEntryMIRAcceptsForInIterator(t *testing.T) {
	iterT := &ir.NamedType{Name: "Iterator", Args: []ir.Type{ir.TInt}, Builtin: true}
	body := &ir.Block{
		Stmts: []ir.Stmt{
			&ir.ExprStmt{X: &ir.Ident{Name: "x", Kind: ir.IdentLocal, T: ir.TInt}},
		},
	}
	forIn := &ir.ForStmt{
		Kind:  ir.ForIn,
		Iter:  &ir.Ident{Name: "it", Kind: ir.IdentLocal, T: iterT},
		Var:   "x",
		Body:  body,
		SpanV: ir.Span{Start: ir.Pos{Line: 1, Column: 1, Offset: 0}, End: ir.Pos{Line: 1, Column: 20, Offset: 19}},
	}
	mod := &ir.Module{
		Package: "main",
		Decls: []ir.Decl{
			&ir.FnDecl{
				Name:   "main",
				Return: ir.TUnit,
				Body: &ir.Block{
					Stmts: []ir.Stmt{
						&ir.LetStmt{Name: "it", Type: iterT, Value: &ir.Ident{Name: "it", Kind: ir.IdentParam, T: iterT}},
						forIn,
					},
				},
			},
		},
	}
	entry, err := finalizeEntryIR(Entry{PackageName: "main"}, mod)
	if err != nil {
		t.Fatalf("finalizeEntryIR: %v", err)
	}
	entry, err = LowerEntryMIR(entry)
	if err != nil {
		t.Fatalf("LowerEntryMIR: %v", err)
	}
	if len(entry.MIRIssues) > 0 {
		for _, iss := range entry.MIRIssues {
			t.Errorf("unexpected MIR issue: %s", iss.Error())
		}
	}
	if entry.MIR == nil {
		t.Fatal("entry.MIR is nil")
	}
}

func TestLowerEntryMIRAcceptsForInCountableIterator(t *testing.T) {
	arrayT := &ir.NamedType{Name: "Array", Args: []ir.Type{ir.TInt}, Builtin: true}
	body := &ir.Block{
		Stmts: []ir.Stmt{
			&ir.ExprStmt{X: &ir.Ident{Name: "x", Kind: ir.IdentLocal, T: ir.TInt}},
		},
	}
	forIn := &ir.ForStmt{
		Kind:  ir.ForIn,
		Iter:  &ir.Ident{Name: "arr", Kind: ir.IdentLocal, T: arrayT},
		Var:   "x",
		Body:  body,
		SpanV: ir.Span{Start: ir.Pos{Line: 1, Column: 1, Offset: 0}, End: ir.Pos{Line: 1, Column: 20, Offset: 19}},
	}
	mod := &ir.Module{
		Package: "main",
		Decls: []ir.Decl{
			&ir.FnDecl{
				Name:   "main",
				Return: ir.TUnit,
				Body: &ir.Block{
					Stmts: []ir.Stmt{
						&ir.LetStmt{Name: "arr", Type: arrayT, Value: &ir.Ident{Name: "arr", Kind: ir.IdentParam, T: arrayT}},
						forIn,
					},
				},
			},
		},
	}
	entry, err := finalizeEntryIR(Entry{PackageName: "main"}, mod)
	if err != nil {
		t.Fatalf("finalizeEntryIR: %v", err)
	}
	entry, err = LowerEntryMIR(entry)
	if err != nil {
		t.Fatalf("LowerEntryMIR: %v", err)
	}
	if len(entry.MIRIssues) > 0 {
		for _, iss := range entry.MIRIssues {
			t.Errorf("unexpected MIR issue: %s", iss.Error())
		}
	}
	if entry.MIR == nil {
		t.Fatal("entry.MIR is nil")
	}
}
