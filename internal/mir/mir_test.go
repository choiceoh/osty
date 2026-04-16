package mir

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/ir"
)

// ==== construction + printer tests ====

func TestPrintEmptyModule(t *testing.T) {
	m := &Module{Package: "main", Layouts: NewLayoutTable()}
	got := Print(m)
	if !strings.Contains(got, `module "main"`) {
		t.Fatalf("print missing module header:\n%s", got)
	}
}

// TestPrintHandBuiltFunction pins the textual format. The test builds
// a function with a single block that returns 42 and asserts against
// the printer output.
func TestPrintHandBuiltFunction(t *testing.T) {
	fn := &Function{
		Name:       "answer",
		ReturnType: TInt,
	}
	fn.ReturnLocal = fn.NewLocal("_return", TInt, false, Span{})
	fn.Locals[fn.ReturnLocal].IsReturn = true
	bbID := fn.NewBlock(Span{})
	fn.Entry = bbID
	bb := fn.Block(bbID)
	bb.Append(&AssignInstr{
		Dest: Place{Local: fn.ReturnLocal},
		Src:  &UseRV{Op: &ConstOp{Const: &IntConst{Value: 42, T: TInt}, T: TInt}},
	})
	bb.SetTerminator(&ReturnTerm{})

	mod := &Module{Package: "main", Functions: []*Function{fn}, Layouts: NewLayoutTable()}
	got := Print(mod)

	for _, want := range []string{
		"fn answer() -> Int",
		"_0 = use const 42 Int",
		"return",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("printed module missing %q:\n%s", want, got)
		}
	}
}

// ==== validator tests ====

func TestValidateAcceptsWellFormed(t *testing.T) {
	fn := &Function{Name: "ok", ReturnType: TUnit}
	fn.ReturnLocal = fn.NewLocal("_return", TUnit, false, Span{})
	fn.Locals[fn.ReturnLocal].IsReturn = true
	bb := fn.NewBlock(Span{})
	fn.Entry = bb
	fn.Block(bb).SetTerminator(&ReturnTerm{})
	mod := &Module{Package: "main", Functions: []*Function{fn}, Layouts: NewLayoutTable()}
	if errs := Validate(mod); len(errs) > 0 {
		t.Fatalf("Validate returned errors on well-formed module: %v", errs)
	}
}

func TestValidateRejectsMissingTerminator(t *testing.T) {
	fn := &Function{Name: "broken", ReturnType: TUnit}
	fn.ReturnLocal = fn.NewLocal("_return", TUnit, false, Span{})
	fn.Locals[fn.ReturnLocal].IsReturn = true
	bb := fn.NewBlock(Span{})
	fn.Entry = bb
	// intentionally omit terminator
	_ = bb
	mod := &Module{Package: "main", Functions: []*Function{fn}, Layouts: NewLayoutTable()}
	errs := Validate(mod)
	if len(errs) == 0 {
		t.Fatalf("expected missing-terminator error, got none")
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "missing terminator") {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing-terminator not reported: %v", errs)
	}
}

func TestValidateRejectsEmptyPackage(t *testing.T) {
	mod := &Module{Package: "", Layouts: NewLayoutTable()}
	errs := Validate(mod)
	if len(errs) == 0 {
		t.Fatalf("expected empty-package error, got none")
	}
}

func TestValidateRejectsOutOfRangeLocal(t *testing.T) {
	fn := &Function{Name: "oops", ReturnType: TUnit}
	fn.ReturnLocal = fn.NewLocal("_return", TUnit, false, Span{})
	fn.Locals[fn.ReturnLocal].IsReturn = true
	bb := fn.NewBlock(Span{})
	fn.Entry = bb
	fn.Block(bb).Append(&AssignInstr{
		Dest: Place{Local: LocalID(99)},
		Src:  &UseRV{Op: &ConstOp{Const: &UnitConst{}, T: TUnit}},
	})
	fn.Block(bb).SetTerminator(&ReturnTerm{})
	mod := &Module{Package: "main", Functions: []*Function{fn}, Layouts: NewLayoutTable()}
	errs := Validate(mod)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "out of range") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected out-of-range error, got %v", errs)
	}
}

func TestValidateRejectsDuplicateSymbol(t *testing.T) {
	fn := func() *Function {
		f := &Function{Name: "dup", ReturnType: TUnit}
		f.ReturnLocal = f.NewLocal("_return", TUnit, false, Span{})
		f.Locals[f.ReturnLocal].IsReturn = true
		bb := f.NewBlock(Span{})
		f.Entry = bb
		f.Block(bb).SetTerminator(&ReturnTerm{})
		return f
	}
	mod := &Module{Package: "main", Functions: []*Function{fn(), fn()}, Layouts: NewLayoutTable()}
	errs := Validate(mod)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "duplicate symbol") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected duplicate-symbol error, got %v", errs)
	}
}

// ==== lowering tests ====

// helper: wrap a single statement into a main() function and lower.
func lowerHIR(t *testing.T, decl *ir.FnDecl) *Module {
	t.Helper()
	mod := &ir.Module{Package: "main", Decls: []ir.Decl{decl}}
	out := Lower(mod)
	if out == nil {
		t.Fatalf("Lower returned nil")
	}
	if errs := Validate(out); len(errs) > 0 {
		t.Fatalf("Validate on lowered module failed:\n%v\n\nprinted:\n%s", errs, Print(out))
	}
	return out
}

// mkFn builds a trivial HIR fn(name) -> returnT with the given body.
func mkFn(name string, retT ir.Type, body *ir.Block) *ir.FnDecl {
	return &ir.FnDecl{Name: name, Return: retT, Body: body}
}

func TestLowerConstReturn(t *testing.T) {
	fn := mkFn("answer", ir.TInt, &ir.Block{
		Result: &ir.IntLit{Text: "42", T: ir.TInt},
	})
	mod := lowerHIR(t, fn)
	text := Print(mod)
	for _, want := range []string{
		"fn answer() -> Int",
		"_0 = use const 42 Int",
		"return",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
}

func TestLowerBinaryArith(t *testing.T) {
	fn := mkFn("add", ir.TInt, &ir.Block{
		Result: &ir.BinaryExpr{
			Op:    ir.BinAdd,
			Left:  &ir.IntLit{Text: "1", T: ir.TInt},
			Right: &ir.IntLit{Text: "2", T: ir.TInt},
			T:     ir.TInt,
		},
	})
	mod := lowerHIR(t, fn)
	text := Print(mod)
	if !strings.Contains(text, "const 1 Int + const 2 Int") {
		t.Fatalf("expected +-rvalue, got:\n%s", text)
	}
}

func TestLowerIfExpr(t *testing.T) {
	fn := mkFn("abs", ir.TInt, &ir.Block{
		Stmts: []ir.Stmt{
			&ir.LetStmt{
				Name: "n",
				Type: ir.TInt,
				Value: &ir.IntLit{Text: "-5", T: ir.TInt},
			},
		},
		Result: &ir.IfExpr{
			Cond: &ir.BinaryExpr{
				Op:    ir.BinLt,
				Left:  &ir.Ident{Name: "n", Kind: ir.IdentLocal, T: ir.TInt},
				Right: &ir.IntLit{Text: "0", T: ir.TInt},
				T:     ir.TBool,
			},
			Then: &ir.Block{
				Result: &ir.UnaryExpr{
					Op: ir.UnNeg,
					X:  &ir.Ident{Name: "n", Kind: ir.IdentLocal, T: ir.TInt},
					T:  ir.TInt,
				},
			},
			Else: &ir.Block{
				Result: &ir.Ident{Name: "n", Kind: ir.IdentLocal, T: ir.TInt},
			},
			T: ir.TInt,
		},
	})
	mod := lowerHIR(t, fn)
	text := Print(mod)
	if !strings.Contains(text, "branch ") {
		t.Fatalf("if expected branch terminator, got:\n%s", text)
	}
	if !strings.Contains(text, "goto -> bb") {
		t.Fatalf("if expected goto, got:\n%s", text)
	}
}

func TestLowerWhileLoop(t *testing.T) {
	fn := mkFn("count", ir.TUnit, &ir.Block{
		Stmts: []ir.Stmt{
			&ir.LetStmt{
				Name:  "i",
				Type:  ir.TInt,
				Value: &ir.IntLit{Text: "0", T: ir.TInt},
				Mut:   true,
			},
			&ir.ForStmt{
				Kind: ir.ForWhile,
				Cond: &ir.BinaryExpr{
					Op:    ir.BinLt,
					Left:  &ir.Ident{Name: "i", Kind: ir.IdentLocal, T: ir.TInt},
					Right: &ir.IntLit{Text: "10", T: ir.TInt},
					T:     ir.TBool,
				},
				Body: &ir.Block{
					Stmts: []ir.Stmt{
						&ir.AssignStmt{
							Op:      ir.AssignEq,
							Targets: []ir.Expr{&ir.Ident{Name: "i", Kind: ir.IdentLocal, T: ir.TInt}},
							Value: &ir.BinaryExpr{
								Op:    ir.BinAdd,
								Left:  &ir.Ident{Name: "i", Kind: ir.IdentLocal, T: ir.TInt},
								Right: &ir.IntLit{Text: "1", T: ir.TInt},
								T:     ir.TInt,
							},
						},
					},
				},
			},
		},
	})
	mod := lowerHIR(t, fn)
	text := Print(mod)
	// Expect a branch on the condition and a back-edge goto.
	if strings.Count(text, "goto -> bb") < 2 {
		t.Fatalf("expected two gotos for while header/back-edge, got:\n%s", text)
	}
	if !strings.Contains(text, "branch ") {
		t.Fatalf("expected branch for while cond, got:\n%s", text)
	}
}

func TestLowerFnCall(t *testing.T) {
	// fn add(a: Int, b: Int) -> Int { a + b }
	addFn := &ir.FnDecl{
		Name:   "add",
		Return: ir.TInt,
		Params: []*ir.Param{
			{Name: "a", Type: ir.TInt},
			{Name: "b", Type: ir.TInt},
		},
		Body: &ir.Block{
			Result: &ir.BinaryExpr{
				Op:    ir.BinAdd,
				Left:  &ir.Ident{Name: "a", Kind: ir.IdentParam, T: ir.TInt},
				Right: &ir.Ident{Name: "b", Kind: ir.IdentParam, T: ir.TInt},
				T:     ir.TInt,
			},
		},
	}
	// fn main() -> Int { add(1, 2) }
	mainFn := &ir.FnDecl{
		Name:   "main",
		Return: ir.TInt,
		Body: &ir.Block{
			Result: &ir.CallExpr{
				Callee: &ir.Ident{Name: "add", Kind: ir.IdentFn},
				Args: []ir.Arg{
					{Value: &ir.IntLit{Text: "1", T: ir.TInt}},
					{Value: &ir.IntLit{Text: "2", T: ir.TInt}},
				},
				T: ir.TInt,
			},
		},
	}
	mod := &ir.Module{Package: "main", Decls: []ir.Decl{addFn, mainFn}}
	out := Lower(mod)
	if errs := Validate(out); len(errs) > 0 {
		t.Fatalf("validate: %v\n\n%s", errs, Print(out))
	}
	text := Print(out)
	if !strings.Contains(text, "call add(const 1 Int, const 2 Int)") {
		t.Fatalf("expected call add(...) in:\n%s", text)
	}
}

func TestLowerStructLiteralAndField(t *testing.T) {
	// struct Point { x: Int, y: Int }
	// fn sum() -> Int { let p = Point { x: 1, y: 2 }; p.x + p.y }
	pointDecl := &ir.StructDecl{
		Name: "Point",
		Fields: []*ir.Field{
			{Name: "x", Type: ir.TInt, Exported: true},
			{Name: "y", Type: ir.TInt, Exported: true},
		},
	}
	pointT := &ir.NamedType{Name: "Point"}
	sumFn := &ir.FnDecl{
		Name:   "sum",
		Return: ir.TInt,
		Body: &ir.Block{
			Stmts: []ir.Stmt{
				&ir.LetStmt{
					Name: "p",
					Type: pointT,
					Value: &ir.StructLit{
						TypeName: "Point",
						Fields: []ir.StructLitField{
							{Name: "x", Value: &ir.IntLit{Text: "1", T: ir.TInt}},
							{Name: "y", Value: &ir.IntLit{Text: "2", T: ir.TInt}},
						},
						T: pointT,
					},
				},
			},
			Result: &ir.BinaryExpr{
				Op: ir.BinAdd,
				Left: &ir.FieldExpr{
					X:    &ir.Ident{Name: "p", Kind: ir.IdentLocal, T: pointT},
					Name: "x",
					T:    ir.TInt,
				},
				Right: &ir.FieldExpr{
					X:    &ir.Ident{Name: "p", Kind: ir.IdentLocal, T: pointT},
					Name: "y",
					T:    ir.TInt,
				},
				T: ir.TInt,
			},
		},
	}
	mod := &ir.Module{Package: "main", Decls: []ir.Decl{pointDecl, sumFn}}
	out := Lower(mod)
	if errs := Validate(out); len(errs) > 0 {
		t.Fatalf("validate: %v\n\n%s", errs, Print(out))
	}
	text := Print(out)
	if !strings.Contains(text, "aggregate struct(const 1 Int, const 2 Int)") {
		t.Fatalf("expected struct aggregate, got:\n%s", text)
	}
	if !strings.Contains(text, ".x") || !strings.Contains(text, ".y") {
		t.Fatalf("expected .x/.y projections, got:\n%s", text)
	}
	if out.Layouts == nil || out.Layouts.Structs["Point"] == nil {
		t.Fatalf("expected Point layout entry")
	}
}

func TestLowerEnumMatch(t *testing.T) {
	// enum Maybe { Some(Int), None }
	// fn score(m: Maybe) -> Int { match m { Some(x) -> x, None -> 0 } }
	maybeT := &ir.NamedType{Name: "Maybe"}
	enumDecl := &ir.EnumDecl{
		Name: "Maybe",
		Variants: []*ir.Variant{
			{Name: "Some", Payload: []ir.Type{ir.TInt}},
			{Name: "None"},
		},
	}
	scrut := &ir.Ident{Name: "m", Kind: ir.IdentParam, T: maybeT}
	arms := []*ir.MatchArm{
		{
			Pattern: &ir.VariantPat{Enum: "Maybe", Variant: "Some", Args: []ir.Pattern{&ir.IdentPat{Name: "x"}}},
			Body:    &ir.Block{Result: &ir.Ident{Name: "x", Kind: ir.IdentLocal, T: ir.TInt}},
		},
		{
			Pattern: &ir.VariantPat{Enum: "Maybe", Variant: "None"},
			Body:    &ir.Block{Result: &ir.IntLit{Text: "0", T: ir.TInt}},
		},
	}
	tree := ir.CompileDecisionTree(maybeT, arms)
	if tree == nil {
		t.Fatalf("CompileDecisionTree returned nil")
	}
	scoreFn := &ir.FnDecl{
		Name:   "score",
		Return: ir.TInt,
		Params: []*ir.Param{{Name: "m", Type: maybeT}},
		Body: &ir.Block{
			Result: &ir.MatchExpr{
				Scrutinee: scrut,
				Arms:      arms,
				Tree:      tree,
				T:         ir.TInt,
			},
		},
	}
	mod := &ir.Module{Package: "main", Decls: []ir.Decl{enumDecl, scoreFn}}
	out := Lower(mod)
	if errs := Validate(out); len(errs) > 0 {
		t.Fatalf("validate: %v\n\n%s", errs, Print(out))
	}
	text := Print(out)
	if !strings.Contains(text, "discriminant ") {
		t.Fatalf("expected discriminant read, got:\n%s", text)
	}
	if !strings.Contains(text, "switchInt ") {
		t.Fatalf("expected switchInt terminator, got:\n%s", text)
	}
	if out.Layouts.Enums["Maybe"] == nil {
		t.Fatalf("expected Maybe layout entry")
	}
	// Make sure no HIR-only node (e.g. MatchExpr/IfLetExpr/QuestionExpr)
	// leaked through the printer output.
	for _, forbidden := range []string{
		"MatchExpr",
		"IfLetExpr",
		"QuestionExpr",
		"MethodCall",
		"VariantLit",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("HIR node %q leaked into MIR output:\n%s", forbidden, text)
		}
	}
}

func TestLowerMethodCall(t *testing.T) {
	// struct Point { x: Int, pub fn get(self) -> Int { self.x } }
	// fn read(p: Point) -> Int { p.get() }
	pointT := &ir.NamedType{Name: "Point"}
	getFn := &ir.FnDecl{
		Name:   "get",
		Return: ir.TInt,
		Params: []*ir.Param{{Name: "self", Type: pointT}},
		Body: &ir.Block{
			Result: &ir.FieldExpr{
				X:    &ir.Ident{Name: "self", Kind: ir.IdentParam, T: pointT},
				Name: "x",
				T:    ir.TInt,
			},
		},
	}
	pointDecl := &ir.StructDecl{
		Name:    "Point",
		Fields:  []*ir.Field{{Name: "x", Type: ir.TInt, Exported: true}},
		Methods: []*ir.FnDecl{getFn},
	}
	readFn := &ir.FnDecl{
		Name:   "read",
		Return: ir.TInt,
		Params: []*ir.Param{{Name: "p", Type: pointT}},
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "p", Kind: ir.IdentParam, T: pointT},
				Name:     "get",
				T:        ir.TInt,
			},
		},
	}
	mod := &ir.Module{Package: "main", Decls: []ir.Decl{pointDecl, readFn}}
	out := Lower(mod)
	if errs := Validate(out); len(errs) > 0 {
		t.Fatalf("validate: %v\n\n%s", errs, Print(out))
	}
	text := Print(out)
	if !strings.Contains(text, "call Point__get(") {
		t.Fatalf("expected mangled method call, got:\n%s", text)
	}
	// Ensure no MethodCall sugar survives.
	if strings.Contains(text, "MethodCall") {
		t.Fatalf("HIR MethodCall leaked:\n%s", text)
	}
}

func TestLowerIntrinsicPrintln(t *testing.T) {
	fn := mkFn("hello", ir.TUnit, &ir.Block{
		Stmts: []ir.Stmt{
			&ir.ExprStmt{X: &ir.IntrinsicCall{
				Kind: ir.IntrinsicPrintln,
				Args: []ir.Arg{{Value: &ir.StringLit{Parts: []ir.StringPart{{IsLit: true, Lit: "hello"}}}}},
			}},
		},
	})
	mod := lowerHIR(t, fn)
	text := Print(mod)
	if !strings.Contains(text, "intrinsic println(") {
		t.Fatalf("expected println intrinsic, got:\n%s", text)
	}
}

func TestLowerVariantLit(t *testing.T) {
	// enum Color { Red, Green }
	// fn pick() -> Color { Red }
	colorT := &ir.NamedType{Name: "Color"}
	enumDecl := &ir.EnumDecl{
		Name: "Color",
		Variants: []*ir.Variant{
			{Name: "Red"},
			{Name: "Green"},
		},
	}
	pickFn := &ir.FnDecl{
		Name:   "pick",
		Return: colorT,
		Body: &ir.Block{
			Result: &ir.VariantLit{
				Enum:    "Color",
				Variant: "Red",
				T:       colorT,
			},
		},
	}
	mod := &ir.Module{Package: "main", Decls: []ir.Decl{enumDecl, pickFn}}
	out := Lower(mod)
	if errs := Validate(out); len(errs) > 0 {
		t.Fatalf("validate: %v\n\n%s", errs, Print(out))
	}
	text := Print(out)
	if !strings.Contains(text, "aggregate variant Red") {
		t.Fatalf("expected aggregate variant, got:\n%s", text)
	}
	// VariantLit should not survive as a node name in output.
	if strings.Contains(text, "VariantLit") {
		t.Fatalf("HIR VariantLit leaked:\n%s", text)
	}
}

func TestLowerRejectsUnsupportedClosure(t *testing.T) {
	// The current stage-1 lowerer emits an unsupported note for
	// closures; ensure we see it rather than silently succeeding.
	fn := mkFn("withClosure", ir.TUnit, &ir.Block{
		Stmts: []ir.Stmt{
			&ir.LetStmt{
				Name: "f",
				Type: &ir.FnType{Params: []ir.Type{ir.TInt}, Return: ir.TInt},
				Value: &ir.Closure{
					Params: []*ir.Param{{Name: "x", Type: ir.TInt}},
					Return: ir.TInt,
					Body:   &ir.Block{Result: &ir.Ident{Name: "x", Kind: ir.IdentParam, T: ir.TInt}},
					T:      &ir.FnType{Params: []ir.Type{ir.TInt}, Return: ir.TInt},
				},
			},
		},
	})
	mod := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	out := Lower(mod)
	// Expect a "closure not lowered" issue.
	saw := false
	for _, issue := range out.Issues {
		if strings.Contains(issue.Error(), "closure") {
			saw = true
			break
		}
	}
	if !saw {
		t.Fatalf("expected 'closure not lowered' issue, got: %v", out.Issues)
	}
}

// TestLoweringProducesNoHIRSugarNodes cross-checks the "MIR is sugar-
// free" invariant by walking the lowered module and making sure none
// of the forbidden node shapes appear. This catches regressions where
// a future lowering pass forgets to expand a construct.
func TestLoweringProducesNoHIRSugarNodes(t *testing.T) {
	// A grab-bag program exercising many shapes.
	maybeT := &ir.NamedType{Name: "Maybe"}
	enumDecl := &ir.EnumDecl{
		Name: "Maybe",
		Variants: []*ir.Variant{
			{Name: "Some", Payload: []ir.Type{ir.TInt}},
			{Name: "None"},
		},
	}
	pointT := &ir.NamedType{Name: "Point"}
	pointDecl := &ir.StructDecl{
		Name: "Point",
		Fields: []*ir.Field{
			{Name: "x", Type: ir.TInt, Exported: true},
			{Name: "y", Type: ir.TInt, Exported: true},
		},
	}
	arms := []*ir.MatchArm{
		{
			Pattern: &ir.VariantPat{Enum: "Maybe", Variant: "Some", Args: []ir.Pattern{&ir.IdentPat{Name: "x"}}},
			Body:    &ir.Block{Result: &ir.Ident{Name: "x", Kind: ir.IdentLocal, T: ir.TInt}},
		},
		{
			Pattern: &ir.VariantPat{Enum: "Maybe", Variant: "None"},
			Body:    &ir.Block{Result: &ir.IntLit{Text: "0", T: ir.TInt}},
		},
	}
	tree := ir.CompileDecisionTree(maybeT, arms)
	mainFn := &ir.FnDecl{
		Name:   "main",
		Return: ir.TUnit,
		Body: &ir.Block{
			Stmts: []ir.Stmt{
				&ir.LetStmt{Name: "p", Type: pointT, Value: &ir.StructLit{
					TypeName: "Point",
					Fields: []ir.StructLitField{
						{Name: "x", Value: &ir.IntLit{Text: "1", T: ir.TInt}},
						{Name: "y", Value: &ir.IntLit{Text: "2", T: ir.TInt}},
					},
					T: pointT,
				}},
				&ir.LetStmt{Name: "m", Type: maybeT, Value: &ir.VariantLit{
					Enum: "Maybe", Variant: "Some",
					Args: []ir.Arg{{Value: &ir.IntLit{Text: "42", T: ir.TInt}}},
					T:    maybeT,
				}},
				&ir.ExprStmt{X: &ir.MatchExpr{
					Scrutinee: &ir.Ident{Name: "m", Kind: ir.IdentLocal, T: maybeT},
					Arms:      arms,
					Tree:      tree,
					T:         ir.TInt,
				}},
			},
		},
	}
	mod := &ir.Module{Package: "main", Decls: []ir.Decl{enumDecl, pointDecl, mainFn}}
	out := Lower(mod)
	if errs := Validate(out); len(errs) > 0 {
		t.Fatalf("validate: %v\n\n%s", errs, Print(out))
	}
	// Walk every instruction and terminator and assert it's a known MIR
	// shape. Because we constructed the MIR from pure Go types, this
	// mostly asserts the nodes dispatched through the printer — the
	// type system would catch a stray HIR node at compile time
	// anyway.
	for _, fn := range out.Functions {
		for _, bb := range fn.Blocks {
			for _, instr := range bb.Instrs {
				switch instr.(type) {
				case *AssignInstr, *CallInstr, *IntrinsicInstr,
					*StorageLiveInstr, *StorageDeadInstr:
					// ok
				default:
					t.Fatalf("unexpected MIR instr %T", instr)
				}
			}
			if bb.Term == nil {
				t.Fatalf("fn %s bb%d: missing terminator", fn.Name, bb.ID)
			}
			switch bb.Term.(type) {
			case *GotoTerm, *BranchTerm, *SwitchIntTerm,
				*ReturnTerm, *UnreachableTerm:
				// ok
			default:
				t.Fatalf("unexpected MIR terminator %T", bb.Term)
			}
		}
	}
}

// ==== regression: external / intrinsic function ====

func TestValidateAcceptsExternalFunction(t *testing.T) {
	ext := &Function{
		Name:       "std_println",
		ReturnType: TUnit,
		IsExternal: true,
	}
	// Give it a synthetic return local so other invariants hold; it
	// should still validate with no blocks.
	ext.ReturnLocal = ext.NewLocal("_return", TUnit, false, Span{})
	ext.Locals[ext.ReturnLocal].IsReturn = true
	ext.Blocks = nil
	mod := &Module{Package: "main", Functions: []*Function{ext}, Layouts: NewLayoutTable()}
	if errs := Validate(mod); len(errs) > 0 {
		t.Fatalf("validate(external): %v", errs)
	}
}

// Ensure runtime FFI / Go FFI `use` decls flow through to the Uses
// table unchanged.
func TestLowerPreservesUseDecls(t *testing.T) {
	useDecl := &ir.UseDecl{
		Path:        []string{"runtime", "strings"},
		RawPath:     "runtime.strings",
		Alias:       "strings",
		IsRuntimeFFI: true,
		RuntimePath: "strings",
	}
	mod := &ir.Module{Package: "main", Decls: []ir.Decl{useDecl}}
	out := Lower(mod)
	if len(out.Uses) != 1 {
		t.Fatalf("expected 1 use, got %d", len(out.Uses))
	}
	if out.Uses[0].Alias != "strings" || !out.Uses[0].IsRuntimeFFI {
		t.Fatalf("use decl not preserved: %+v", out.Uses[0])
	}
}

// TestLowerIfLetOption covers the common `if let Some(x) = maybe { … }`
// shape over a built-in Option.
func TestLowerIfLetOption(t *testing.T) {
	maybeT := &ir.NamedType{Name: "Maybe"}
	enumDecl := &ir.EnumDecl{
		Name: "Maybe",
		Variants: []*ir.Variant{
			{Name: "Some", Payload: []ir.Type{ir.TInt}},
			{Name: "None"},
		},
	}
	fn := &ir.FnDecl{
		Name:   "score",
		Return: ir.TInt,
		Params: []*ir.Param{{Name: "m", Type: maybeT}},
		Body: &ir.Block{
			Result: &ir.IfLetExpr{
				Pattern: &ir.VariantPat{Enum: "Maybe", Variant: "Some",
					Args: []ir.Pattern{&ir.IdentPat{Name: "x"}}},
				Scrutinee: &ir.Ident{Name: "m", Kind: ir.IdentParam, T: maybeT},
				Then: &ir.Block{
					Result: &ir.Ident{Name: "x", Kind: ir.IdentLocal, T: ir.TInt},
				},
				Else: &ir.Block{Result: &ir.IntLit{Text: "0", T: ir.TInt}},
				T:    ir.TInt,
			},
		},
	}
	mod := &ir.Module{Package: "main", Decls: []ir.Decl{enumDecl, fn}}
	out := Lower(mod)
	if errs := Validate(out); len(errs) > 0 {
		t.Fatalf("validate: %v\n\n%s", errs, Print(out))
	}
	text := Print(out)
	if !strings.Contains(text, "discriminant ") {
		t.Fatalf("expected discriminant test for if-let, got:\n%s", text)
	}
	if !strings.Contains(text, "switchInt ") {
		t.Fatalf("expected switchInt for if-let, got:\n%s", text)
	}
}
