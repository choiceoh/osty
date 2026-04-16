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

// TestMIRDumpClosure prints the MIR for a closure with captures so
// the Stage 2 lifting + aggregate shape is visible for inspection.
func TestMIRDumpClosure(t *testing.T) {
	fnType := &ir.FnType{Params: []ir.Type{ir.TInt}, Return: ir.TInt}
	body := &ir.Block{Result: &ir.BinaryExpr{
		Op:    ir.BinAdd,
		Left:  &ir.Ident{Name: "x", Kind: ir.IdentParam, T: ir.TInt},
		Right: &ir.Ident{Name: "n", Kind: ir.IdentLocal, T: ir.TInt},
		T:     ir.TInt,
	}}
	cl := &ir.Closure{
		Params: []*ir.Param{{Name: "x", Type: ir.TInt}},
		Return: ir.TInt,
		Body:   body,
		Captures: []*ir.Capture{
			{Name: "n", Kind: ir.CaptureLocal, T: ir.TInt},
		},
		T: fnType,
	}
	fn := &ir.FnDecl{
		Name:   "make_adder",
		Return: fnType,
		Body: &ir.Block{
			Stmts: []ir.Stmt{
				&ir.LetStmt{Name: "n", Type: ir.TInt, Value: &ir.IntLit{Text: "10", T: ir.TInt}},
			},
			Result: cl,
		},
	}
	mod := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	out := Lower(mod)
	if errs := Validate(out); len(errs) > 0 {
		t.Fatalf("validate: %v", errs)
	}
	t.Logf("MIR:\n%s", Print(out))
}

// TestMIRDumpConcurrency prints MIR for a program exercising the
// Stage 2b concurrency intrinsics: thread.chan, chan_send, chan_close,
// and for-in over a channel.
func TestMIRDumpConcurrency(t *testing.T) {
	chanInt := &ir.NamedType{Name: "Channel", Args: []ir.Type{ir.TInt}}
	use := &ir.UseDecl{
		Path: []string{"std", "thread"}, RawPath: "std.thread", Alias: "thread",
	}
	fn := &ir.FnDecl{
		Name:   "pump",
		Return: ir.TUnit,
		Body: &ir.Block{
			Stmts: []ir.Stmt{
				&ir.LetStmt{
					Name: "ch",
					Type: chanInt,
					Value: &ir.MethodCall{
						Receiver: &ir.Ident{Name: "thread"},
						Name:     "chan",
						TypeArgs: []ir.Type{ir.TInt},
						Args:     []ir.Arg{{Value: &ir.IntLit{Text: "4", T: ir.TInt}}},
						T:        chanInt,
					},
				},
				&ir.ChanSendStmt{
					Channel: &ir.Ident{Name: "ch", Kind: ir.IdentLocal, T: chanInt},
					Value:   &ir.IntLit{Text: "42", T: ir.TInt},
				},
				&ir.ExprStmt{X: &ir.MethodCall{
					Receiver: &ir.Ident{Name: "ch", Kind: ir.IdentLocal, T: chanInt},
					Name:     "close",
					T:        ir.TUnit,
				}},
				&ir.ForStmt{
					Kind: ir.ForIn,
					Var:  "x",
					Iter: &ir.Ident{Name: "ch", Kind: ir.IdentLocal, T: chanInt},
					Body: &ir.Block{
						Stmts: []ir.Stmt{
							&ir.ExprStmt{X: &ir.IntrinsicCall{
								Kind: ir.IntrinsicPrintln,
								Args: []ir.Arg{{Value: &ir.Ident{Name: "x", Kind: ir.IdentLocal, T: ir.TInt}}},
							}},
						},
					},
				},
			},
		},
	}
	mod := &ir.Module{Package: "main", Decls: []ir.Decl{use, fn}}
	out := Lower(mod)
	if errs := Validate(out); len(errs) > 0 {
		t.Fatalf("validate: %v", errs)
	}
	t.Logf("MIR:\n%s", Print(out))
}
