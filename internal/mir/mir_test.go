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
				Name:  "n",
				Type:  ir.TInt,
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

func TestLowerClosureNoCapture(t *testing.T) {
	// `let f = |x| x + 1` should lift the closure body into a fresh
	// top-level MIR function and materialise f as an AggClosure
	// aggregate. No HIR Closure node survives.
	fnType := &ir.FnType{Params: []ir.Type{ir.TInt}, Return: ir.TInt}
	addOne := &ir.Closure{
		Params: []*ir.Param{{Name: "x", Type: ir.TInt}},
		Return: ir.TInt,
		Body: &ir.Block{Result: &ir.BinaryExpr{
			Op:    ir.BinAdd,
			Left:  &ir.Ident{Name: "x", Kind: ir.IdentParam, T: ir.TInt},
			Right: &ir.IntLit{Text: "1", T: ir.TInt},
			T:     ir.TInt,
		}},
		T: fnType,
	}
	fn := mkFn("withClosure", ir.TUnit, &ir.Block{
		Stmts: []ir.Stmt{
			&ir.LetStmt{Name: "f", Type: fnType, Value: addOne},
		},
	})
	mod := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	out := Lower(mod)
	if errs := Validate(out); len(errs) > 0 {
		t.Fatalf("validate: %v\n\n%s", errs, Print(out))
	}
	text := Print(out)
	if !strings.Contains(text, "aggregate closure(") {
		t.Fatalf("expected aggregate closure, got:\n%s", text)
	}
	// There should now be two MIR functions: the outer `withClosure`
	// and the lifted closure body.
	if len(out.Functions) != 2 {
		t.Fatalf("expected 2 lifted functions, got %d:\n%s", len(out.Functions), text)
	}
}

func TestLowerClosureWithCaptures(t *testing.T) {
	// `let n = 10; let f = |x| x + n` — the closure captures n. The
	// lifted function's first parameter is the capture; the aggregate
	// value carries [fnConst, copy(n)].
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
	fn := mkFn("withCapture", ir.TUnit, &ir.Block{
		Stmts: []ir.Stmt{
			&ir.LetStmt{Name: "n", Type: ir.TInt, Value: &ir.IntLit{Text: "10", T: ir.TInt}},
			&ir.LetStmt{Name: "f", Type: fnType, Value: cl},
		},
	})
	mod := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	out := Lower(mod)
	if errs := Validate(out); len(errs) > 0 {
		t.Fatalf("validate: %v\n\n%s", errs, Print(out))
	}
	text := Print(out)
	// The lifted closure should take the capture as its first param.
	if !strings.Contains(text, "withCapture__closure") {
		t.Fatalf("expected lifted closure function, got:\n%s", text)
	}
	if !strings.Contains(text, "aggregate closure(const fn withCapture__closure") {
		t.Fatalf("expected aggregate closure with fn symbol, got:\n%s", text)
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
		Path:         []string{"runtime", "strings"},
		RawPath:      "runtime.strings",
		Alias:        "strings",
		IsRuntimeFFI: true,
		RuntimePath:  "strings",
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

// ==== Stage 2 additions ====

func TestLowerDeferRunsAtReturn(t *testing.T) {
	// fn work() { defer cleanup(); return }
	// → at the return site the defer body is inlined before ReturnTerm.
	fn := mkFn("work", ir.TUnit, &ir.Block{
		Stmts: []ir.Stmt{
			&ir.DeferStmt{Body: &ir.Block{
				Stmts: []ir.Stmt{
					&ir.ExprStmt{X: &ir.IntrinsicCall{
						Kind: ir.IntrinsicPrintln,
						Args: []ir.Arg{{Value: &ir.StringLit{Parts: []ir.StringPart{{IsLit: true, Lit: "cleanup"}}}}},
					}},
				},
			}},
			&ir.ReturnStmt{},
		},
	})
	mod := lowerHIR(t, fn)
	text := Print(mod)
	if !strings.Contains(text, `intrinsic println(const "cleanup")`) {
		t.Fatalf("expected defer body inlined before return, got:\n%s", text)
	}
	// Ensure the defer is emitted on the return path, not as a
	// lingering DeferStmt.
	if strings.Contains(text, "DeferStmt") {
		t.Fatalf("HIR DeferStmt leaked into MIR:\n%s", text)
	}
}

func TestLowerDeferRunsLIFO(t *testing.T) {
	// defer A(); defer B(); return — expected replay order is B, A.
	mkDeferCall := func(name string) *ir.DeferStmt {
		return &ir.DeferStmt{Body: &ir.Block{
			Stmts: []ir.Stmt{
				&ir.ExprStmt{X: &ir.IntrinsicCall{
					Kind: ir.IntrinsicPrintln,
					Args: []ir.Arg{{Value: &ir.StringLit{Parts: []ir.StringPart{{IsLit: true, Lit: name}}}}},
				}},
			},
		}}
	}
	fn := mkFn("twoDefers", ir.TUnit, &ir.Block{
		Stmts: []ir.Stmt{
			mkDeferCall("A"),
			mkDeferCall("B"),
			&ir.ReturnStmt{},
		},
	})
	mod := lowerHIR(t, fn)
	text := Print(mod)
	ai := strings.Index(text, `const "A"`)
	bi := strings.Index(text, `const "B"`)
	if ai < 0 || bi < 0 {
		t.Fatalf("expected both defers in output:\n%s", text)
	}
	// LIFO: B runs first — so B appears before A in the dump.
	if bi > ai {
		t.Fatalf("expected LIFO replay (B before A), got A at %d, B at %d:\n%s", ai, bi, text)
	}
}

func TestLowerGlobalRead(t *testing.T) {
	// pub let version = 42
	// fn get() -> Int { version }
	globalT := ir.TInt
	globalDecl := &ir.LetDecl{
		Name:     "version",
		Type:     globalT,
		Value:    &ir.IntLit{Text: "42", T: globalT},
		Exported: true,
	}
	getFn := &ir.FnDecl{
		Name:   "get",
		Return: ir.TInt,
		Body: &ir.Block{
			Result: &ir.Ident{Name: "version", Kind: ir.IdentGlobal, T: globalT},
		},
	}
	mod := &ir.Module{Package: "main", Decls: []ir.Decl{globalDecl, getFn}}
	out := Lower(mod)
	if errs := Validate(out); len(errs) > 0 {
		t.Fatalf("validate: %v\n\n%s", errs, Print(out))
	}
	text := Print(out)
	if !strings.Contains(text, "global version Int") {
		t.Fatalf("expected GlobalRefRV in lowered body, got:\n%s", text)
	}
	if len(out.Globals) != 1 || out.Globals[0].Name != "version" {
		t.Fatalf("expected 1 global named version, got %+v", out.Globals)
	}
}

func TestLowerCompoundAssign(t *testing.T) {
	// fn add_one(mut x: Int) -> Int { x += 1; x }
	fn := &ir.FnDecl{
		Name:   "add_one",
		Return: ir.TInt,
		Params: []*ir.Param{{Name: "x", Type: ir.TInt}},
		Body: &ir.Block{
			Stmts: []ir.Stmt{
				&ir.AssignStmt{
					Op:      ir.AssignAdd,
					Targets: []ir.Expr{&ir.Ident{Name: "x", Kind: ir.IdentParam, T: ir.TInt}},
					Value:   &ir.IntLit{Text: "1", T: ir.TInt},
				},
			},
			Result: &ir.Ident{Name: "x", Kind: ir.IdentParam, T: ir.TInt},
		},
	}
	mod := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	out := Lower(mod)
	if errs := Validate(out); len(errs) > 0 {
		t.Fatalf("validate: %v\n\n%s", errs, Print(out))
	}
	text := Print(out)
	if !strings.Contains(text, "_1 = _1 + const 1 Int") {
		t.Fatalf("expected expanded compound assign, got:\n%s", text)
	}
}

func TestLowerMultiTargetAssign(t *testing.T) {
	// fn swap(mut a: Int, mut b: Int) { (a, b) = (b, a) }
	fn := &ir.FnDecl{
		Name:   "swap",
		Return: ir.TUnit,
		Params: []*ir.Param{
			{Name: "a", Type: ir.TInt},
			{Name: "b", Type: ir.TInt},
		},
		Body: &ir.Block{
			Stmts: []ir.Stmt{
				&ir.AssignStmt{
					Op: ir.AssignEq,
					Targets: []ir.Expr{
						&ir.Ident{Name: "a", Kind: ir.IdentParam, T: ir.TInt},
						&ir.Ident{Name: "b", Kind: ir.IdentParam, T: ir.TInt},
					},
					Value: &ir.TupleLit{
						Elems: []ir.Expr{
							&ir.Ident{Name: "b", Kind: ir.IdentParam, T: ir.TInt},
							&ir.Ident{Name: "a", Kind: ir.IdentParam, T: ir.TInt},
						},
						T: &ir.TupleType{Elems: []ir.Type{ir.TInt, ir.TInt}},
					},
				},
			},
		},
	}
	mod := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	out := Lower(mod)
	if errs := Validate(out); len(errs) > 0 {
		t.Fatalf("validate: %v\n\n%s", errs, Print(out))
	}
	text := Print(out)
	// Expect a tuple scratch and two projection reads into a/b.
	if !strings.Contains(text, "_mtuple") {
		t.Fatalf("expected scratch tuple for multi-assign, got:\n%s", text)
	}
	if !strings.Contains(text, ".0") || !strings.Contains(text, ".1") {
		t.Fatalf("expected tuple projections for multi-assign, got:\n%s", text)
	}
}

// Regression for the code-review catch on the `?` error path: when the
// operand type differs from the enclosing return type, the lowerer
// must rebuild the error value in the return type's shape (None for
// Option, Err(payload) for Result) instead of copying the operand
// straight into the return slot.
func TestLowerQuestionOperatorRebuildsErrorForResult(t *testing.T) {
	resultAE := &ir.NamedType{Name: "Result", Args: []ir.Type{ir.TInt, ir.TString}}
	resultBE := &ir.NamedType{Name: "Result", Args: []ir.Type{ir.TBool, ir.TString}}
	// fn widen(x: Result<Int, String>) -> Result<Bool, String> {
	//   let n = x?
	//   Ok(n > 0)
	// }
	fn := &ir.FnDecl{
		Name:   "widen",
		Return: resultBE,
		Params: []*ir.Param{{Name: "x", Type: resultAE}},
		Body: &ir.Block{
			Stmts: []ir.Stmt{
				&ir.LetStmt{
					Name: "n",
					Type: ir.TInt,
					Value: &ir.QuestionExpr{
						X: &ir.Ident{Name: "x", Kind: ir.IdentParam, T: resultAE},
						T: ir.TInt,
					},
				},
			},
			Result: &ir.VariantLit{
				Enum:    "Result",
				Variant: "Ok",
				Args: []ir.Arg{{Value: &ir.BinaryExpr{
					Op:    ir.BinGt,
					Left:  &ir.Ident{Name: "n", Kind: ir.IdentLocal, T: ir.TInt},
					Right: &ir.IntLit{Text: "0", T: ir.TInt},
					T:     ir.TBool,
				}}},
				T: resultBE,
			},
		},
	}
	mod := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	out := Lower(mod)
	if errs := Validate(out); len(errs) > 0 {
		t.Fatalf("validate: %v\n\n%s", errs, Print(out))
	}
	text := Print(out)
	if !strings.Contains(text, "aggregate variant Err") {
		t.Fatalf("expected Err to be rebuilt in return type, got:\n%s", text)
	}
	// And the return local type in the aggregate must be the widened
	// result type — the string rendering will show "Result<Bool, String>".
	if !strings.Contains(text, "Result<Bool, String>") {
		t.Fatalf("expected Err aggregate typed as return type, got:\n%s", text)
	}
}

func TestLowerQuestionOperatorNoneForOption(t *testing.T) {
	// fn widen(x: Int?) -> Bool? {
	//   let n = x?
	//   Some(n > 0)
	// }
	intOpt := &ir.OptionalType{Inner: ir.TInt}
	boolOpt := &ir.OptionalType{Inner: ir.TBool}
	fn := &ir.FnDecl{
		Name:   "widen",
		Return: boolOpt,
		Params: []*ir.Param{{Name: "x", Type: intOpt}},
		Body: &ir.Block{
			Stmts: []ir.Stmt{
				&ir.LetStmt{
					Name: "n",
					Type: ir.TInt,
					Value: &ir.QuestionExpr{
						X: &ir.Ident{Name: "x", Kind: ir.IdentParam, T: intOpt},
						T: ir.TInt,
					},
				},
			},
			Result: &ir.VariantLit{
				Enum:    "Option",
				Variant: "Some",
				Args: []ir.Arg{{Value: &ir.BinaryExpr{
					Op:    ir.BinGt,
					Left:  &ir.Ident{Name: "n", Kind: ir.IdentLocal, T: ir.TInt},
					Right: &ir.IntLit{Text: "0", T: ir.TInt},
					T:     ir.TBool,
				}}},
				T: boolOpt,
			},
		},
	}
	mod := &ir.Module{Package: "main", Decls: []ir.Decl{fn}}
	out := Lower(mod)
	if errs := Validate(out); len(errs) > 0 {
		t.Fatalf("validate: %v\n\n%s", errs, Print(out))
	}
	text := Print(out)
	if !strings.Contains(text, "none Bool?") {
		t.Fatalf("expected NullaryNone rebuilt as return-type None, got:\n%s", text)
	}
}

// Regression for the code-review catch on package-qualified calls.
// `strings.Split(s, ",")` must lower as a direct FnRef to a
// qualified symbol with NO synthetic receiver argument.
func TestLowerPackageQualifiedCall(t *testing.T) {
	useDecl := &ir.UseDecl{
		Path:         []string{"runtime", "strings"},
		RawPath:      "runtime.strings",
		Alias:        "strings",
		IsRuntimeFFI: true,
		RuntimePath:  "runtime.strings",
	}
	// fn first_part(s: String) -> List<String> {
	//   strings.Split(s, ",")
	// }
	listStr := &ir.NamedType{Name: "List", Args: []ir.Type{ir.TString}, Builtin: true}
	fn := &ir.FnDecl{
		Name:   "first_part",
		Return: listStr,
		Params: []*ir.Param{{Name: "s", Type: ir.TString}},
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "strings"},
				Name:     "Split",
				Args: []ir.Arg{
					{Value: &ir.Ident{Name: "s", Kind: ir.IdentParam, T: ir.TString}},
					{Value: &ir.StringLit{Parts: []ir.StringPart{{IsLit: true, Lit: ","}}}},
				},
				T: listStr,
			},
		},
	}
	mod := &ir.Module{Package: "main", Decls: []ir.Decl{useDecl, fn}}
	out := Lower(mod)
	if errs := Validate(out); len(errs) > 0 {
		t.Fatalf("validate: %v\n\n%s", errs, Print(out))
	}
	text := Print(out)
	// The call must NOT have the alias prepended as a receiver.
	if !strings.Contains(text, "call runtime.strings.Split(") {
		t.Fatalf("expected qualified runtime.strings.Split symbol, got:\n%s", text)
	}
	// Argument list must be exactly 2 operands (s, ",") — no receiver.
	if strings.Contains(text, "call runtime.strings.Split(_1, _1,") {
		t.Fatalf("receiver was incorrectly prepended as an argument:\n%s", text)
	}
}

// Same regression via a CallExpr{FieldExpr} shape — even if the HIR
// lowerer normally routes via MethodCall, the CallExpr path must
// still classify package aliases as qualified direct calls.
func TestLowerCallExprWithFieldCallee(t *testing.T) {
	useDecl := &ir.UseDecl{
		Path:         []string{"runtime", "strings"},
		RawPath:      "runtime.strings",
		Alias:        "strings",
		IsRuntimeFFI: true,
		RuntimePath:  "runtime.strings",
	}
	listStr := &ir.NamedType{Name: "List", Args: []ir.Type{ir.TString}, Builtin: true}
	fn := &ir.FnDecl{
		Name:   "first_part",
		Return: listStr,
		Params: []*ir.Param{{Name: "s", Type: ir.TString}},
		Body: &ir.Block{
			Result: &ir.CallExpr{
				Callee: &ir.FieldExpr{
					X:    &ir.Ident{Name: "strings"},
					Name: "Split",
					T:    ir.ErrTypeVal,
				},
				Args: []ir.Arg{
					{Value: &ir.Ident{Name: "s", Kind: ir.IdentParam, T: ir.TString}},
					{Value: &ir.StringLit{Parts: []ir.StringPart{{IsLit: true, Lit: ","}}}},
				},
				T: listStr,
			},
		},
	}
	mod := &ir.Module{Package: "main", Decls: []ir.Decl{useDecl, fn}}
	out := Lower(mod)
	if errs := Validate(out); len(errs) > 0 {
		t.Fatalf("validate: %v\n\n%s", errs, Print(out))
	}
	text := Print(out)
	if !strings.Contains(text, "call runtime.strings.Split(") {
		t.Fatalf("expected qualified FieldExpr call, got:\n%s", text)
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

// ==== Stage 2b: concurrency ====

// threadUse builds a `use std.thread as thread` decl; most
// concurrency tests start with it so the alias is registered in the
// lowerer's useAliases index.
func threadUse() *ir.UseDecl {
	return &ir.UseDecl{
		Path:    []string{"std", "thread"},
		RawPath: "std.thread",
		Alias:   "thread",
	}
}

func TestLowerChanSendStmt(t *testing.T) {
	// fn push(ch: Channel<Int>, value: Int) { ch <- value }
	chanInt := &ir.NamedType{Name: "Channel", Args: []ir.Type{ir.TInt}}
	fn := &ir.FnDecl{
		Name:   "push",
		Return: ir.TUnit,
		Params: []*ir.Param{
			{Name: "ch", Type: chanInt},
			{Name: "value", Type: ir.TInt},
		},
		Body: &ir.Block{
			Stmts: []ir.Stmt{
				&ir.ChanSendStmt{
					Channel: &ir.Ident{Name: "ch", Kind: ir.IdentParam, T: chanInt},
					Value:   &ir.Ident{Name: "value", Kind: ir.IdentParam, T: ir.TInt},
				},
			},
		},
	}
	mod := lowerHIR(t, fn)
	text := Print(mod)
	if !strings.Contains(text, "intrinsic chan_send(_1, _2)") {
		t.Fatalf("expected chan_send intrinsic, got:\n%s", text)
	}
}

func TestLowerChannelRecvMethod(t *testing.T) {
	// fn pop(ch: Channel<Int>) -> Int? { ch.recv() }
	chanInt := &ir.NamedType{Name: "Channel", Args: []ir.Type{ir.TInt}}
	optInt := &ir.OptionalType{Inner: ir.TInt}
	fn := &ir.FnDecl{
		Name:   "pop",
		Return: optInt,
		Params: []*ir.Param{{Name: "ch", Type: chanInt}},
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "ch", Kind: ir.IdentParam, T: chanInt},
				Name:     "recv",
				T:        optInt,
			},
		},
	}
	mod := lowerHIR(t, fn)
	text := Print(mod)
	if !strings.Contains(text, "intrinsic chan_recv(_1)") {
		t.Fatalf("expected chan_recv intrinsic, got:\n%s", text)
	}
}

func TestLowerChannelCloseMethod(t *testing.T) {
	chanInt := &ir.NamedType{Name: "Channel", Args: []ir.Type{ir.TInt}}
	fn := &ir.FnDecl{
		Name:   "shut",
		Return: ir.TUnit,
		Params: []*ir.Param{{Name: "ch", Type: chanInt}},
		Body: &ir.Block{
			Stmts: []ir.Stmt{
				&ir.ExprStmt{X: &ir.MethodCall{
					Receiver: &ir.Ident{Name: "ch", Kind: ir.IdentParam, T: chanInt},
					Name:     "close",
					T:        ir.TUnit,
				}},
			},
		},
	}
	mod := lowerHIR(t, fn)
	text := Print(mod)
	if !strings.Contains(text, "intrinsic chan_close(_1)") {
		t.Fatalf("expected chan_close intrinsic, got:\n%s", text)
	}
}

func TestLowerThreadChanMake(t *testing.T) {
	// use std.thread as thread
	// fn make() -> Channel<Int> { thread.chan::<Int>(10) }
	chanInt := &ir.NamedType{Name: "Channel", Args: []ir.Type{ir.TInt}}
	fn := &ir.FnDecl{
		Name:   "make",
		Return: chanInt,
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "thread"},
				Name:     "chan",
				TypeArgs: []ir.Type{ir.TInt},
				Args: []ir.Arg{
					{Value: &ir.IntLit{Text: "10", T: ir.TInt}},
				},
				T: chanInt,
			},
		},
	}
	mod := &ir.Module{Package: "main", Decls: []ir.Decl{threadUse(), fn}}
	out := Lower(mod)
	if errs := Validate(out); len(errs) > 0 {
		t.Fatalf("validate: %v\n\n%s", errs, Print(out))
	}
	text := Print(out)
	if !strings.Contains(text, "intrinsic chan_make(const 10 Int)") {
		t.Fatalf("expected chan_make intrinsic, got:\n%s", text)
	}
}

func TestLowerThreadSpawn(t *testing.T) {
	// use std.thread as thread
	// fn run(f: fn() -> Int) { thread.spawn(f) }
	fnType := &ir.FnType{Return: ir.TInt}
	handleT := &ir.NamedType{Name: "Handle", Args: []ir.Type{ir.TInt}}
	fn := &ir.FnDecl{
		Name:   "run",
		Return: ir.TUnit,
		Params: []*ir.Param{{Name: "f", Type: fnType}},
		Body: &ir.Block{
			Stmts: []ir.Stmt{
				&ir.ExprStmt{X: &ir.MethodCall{
					Receiver: &ir.Ident{Name: "thread"},
					Name:     "spawn",
					Args:     []ir.Arg{{Value: &ir.Ident{Name: "f", Kind: ir.IdentParam, T: fnType}}},
					T:        handleT,
				}},
			},
		},
	}
	mod := &ir.Module{Package: "main", Decls: []ir.Decl{threadUse(), fn}}
	out := Lower(mod)
	if errs := Validate(out); len(errs) > 0 {
		t.Fatalf("validate: %v\n\n%s", errs, Print(out))
	}
	text := Print(out)
	if !strings.Contains(text, "intrinsic spawn(_1)") {
		t.Fatalf("expected spawn intrinsic, got:\n%s", text)
	}
}

func TestLowerGroupSpawnMethod(t *testing.T) {
	// Group.spawn() call — the receiver is a Group value, method
	// "spawn". Expect IntrinsicSpawn with [group, closure].
	groupT := &ir.NamedType{Name: "Group"}
	fnType := &ir.FnType{Return: ir.TInt}
	handleT := &ir.NamedType{Name: "Handle", Args: []ir.Type{ir.TInt}}
	fn := &ir.FnDecl{
		Name:   "launch",
		Return: ir.TUnit,
		Params: []*ir.Param{
			{Name: "g", Type: groupT},
			{Name: "f", Type: fnType},
		},
		Body: &ir.Block{
			Stmts: []ir.Stmt{
				&ir.ExprStmt{X: &ir.MethodCall{
					Receiver: &ir.Ident{Name: "g", Kind: ir.IdentParam, T: groupT},
					Name:     "spawn",
					Args:     []ir.Arg{{Value: &ir.Ident{Name: "f", Kind: ir.IdentParam, T: fnType}}},
					T:        handleT,
				}},
			},
		},
	}
	mod := lowerHIR(t, fn)
	text := Print(mod)
	if !strings.Contains(text, "intrinsic spawn(_1, _2)") {
		t.Fatalf("expected spawn intrinsic with [group, closure], got:\n%s", text)
	}
}

func TestLowerHandleJoin(t *testing.T) {
	// fn wait(h: Handle<Int>) -> Int { h.join() }
	handleT := &ir.NamedType{Name: "Handle", Args: []ir.Type{ir.TInt}}
	fn := &ir.FnDecl{
		Name:   "wait",
		Return: ir.TInt,
		Params: []*ir.Param{{Name: "h", Type: handleT}},
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "h", Kind: ir.IdentParam, T: handleT},
				Name:     "join",
				T:        ir.TInt,
			},
		},
	}
	mod := lowerHIR(t, fn)
	text := Print(mod)
	if !strings.Contains(text, "intrinsic handle_join(_1)") {
		t.Fatalf("expected handle_join intrinsic, got:\n%s", text)
	}
}

func TestLowerPreludeTaskGroup(t *testing.T) {
	// fn run(f: fn(Group) -> Int) -> Int { taskGroup(f) }
	groupT := &ir.NamedType{Name: "Group"}
	closureT := &ir.FnType{Params: []ir.Type{groupT}, Return: ir.TInt}
	fn := &ir.FnDecl{
		Name:   "run",
		Return: ir.TInt,
		Params: []*ir.Param{{Name: "f", Type: closureT}},
		Body: &ir.Block{
			Result: &ir.CallExpr{
				Callee: &ir.Ident{Name: "taskGroup", Kind: ir.IdentFn},
				Args:   []ir.Arg{{Value: &ir.Ident{Name: "f", Kind: ir.IdentParam, T: closureT}}},
				T:      ir.TInt,
			},
		},
	}
	mod := lowerHIR(t, fn)
	text := Print(mod)
	if !strings.Contains(text, "intrinsic task_group(_1)") {
		t.Fatalf("expected task_group intrinsic, got:\n%s", text)
	}
}

func TestLowerPreludeParallel(t *testing.T) {
	// parallel(items, 4, f) — intrinsic with 3 args, positional.
	listInt := &ir.NamedType{Name: "List", Args: []ir.Type{ir.TInt}, Builtin: true}
	fnType := &ir.FnType{Params: []ir.Type{ir.TInt}, Return: ir.TInt}
	fn := &ir.FnDecl{
		Name:   "compute",
		Return: listInt,
		Params: []*ir.Param{
			{Name: "items", Type: listInt},
			{Name: "f", Type: fnType},
		},
		Body: &ir.Block{
			Result: &ir.CallExpr{
				Callee: &ir.Ident{Name: "parallel", Kind: ir.IdentFn},
				Args: []ir.Arg{
					{Value: &ir.Ident{Name: "items", Kind: ir.IdentParam, T: listInt}},
					{Value: &ir.IntLit{Text: "4", T: ir.TInt}},
					{Value: &ir.Ident{Name: "f", Kind: ir.IdentParam, T: fnType}},
				},
				T: listInt,
			},
		},
	}
	mod := lowerHIR(t, fn)
	text := Print(mod)
	if !strings.Contains(text, "intrinsic parallel(_1, const 4 Int, _2)") {
		t.Fatalf("expected parallel intrinsic, got:\n%s", text)
	}
}

func TestLowerThreadCancellationHelpers(t *testing.T) {
	// fn check() -> Bool { thread.isCancelled() }
	fn := &ir.FnDecl{
		Name:   "check",
		Return: ir.TBool,
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "thread"},
				Name:     "isCancelled",
				T:        ir.TBool,
			},
		},
	}
	mod := &ir.Module{Package: "main", Decls: []ir.Decl{threadUse(), fn}}
	out := Lower(mod)
	if errs := Validate(out); len(errs) > 0 {
		t.Fatalf("validate: %v\n\n%s", errs, Print(out))
	}
	text := Print(out)
	if !strings.Contains(text, "intrinsic is_cancelled()") {
		t.Fatalf("expected is_cancelled intrinsic, got:\n%s", text)
	}
}

func TestLowerForInChannel(t *testing.T) {
	// use std.thread
	// fn drain(ch: Channel<Int>) { for x in ch { println(x) } }
	chanInt := &ir.NamedType{Name: "Channel", Args: []ir.Type{ir.TInt}}
	fn := &ir.FnDecl{
		Name:   "drain",
		Return: ir.TUnit,
		Params: []*ir.Param{{Name: "ch", Type: chanInt}},
		Body: &ir.Block{
			Stmts: []ir.Stmt{
				&ir.ForStmt{
					Kind: ir.ForIn,
					Var:  "x",
					Iter: &ir.Ident{Name: "ch", Kind: ir.IdentParam, T: chanInt},
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
	mod := lowerHIR(t, fn)
	text := Print(mod)
	if !strings.Contains(text, "intrinsic chan_recv(") {
		t.Fatalf("expected chan_recv intrinsic in for-in, got:\n%s", text)
	}
	if !strings.Contains(text, "switchInt ") {
		t.Fatalf("expected switchInt (Some vs None) for recv loop, got:\n%s", text)
	}
}

// Regression: package-qualified `thread.chan` must go to the intrinsic
// path, NOT to the plain qualified-call path that Stage 2a shipped.
// Without the Stage 2b intrinsic fast-path, it would lower as
// `call std.thread.chan(const 10 Int)` — useless to the backend.
func TestLowerThreadChanIsIntrinsicNotCall(t *testing.T) {
	chanInt := &ir.NamedType{Name: "Channel", Args: []ir.Type{ir.TInt}}
	fn := &ir.FnDecl{
		Name:   "make",
		Return: chanInt,
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "thread"},
				Name:     "chan",
				TypeArgs: []ir.Type{ir.TInt},
				Args:     []ir.Arg{{Value: &ir.IntLit{Text: "10", T: ir.TInt}}},
				T:        chanInt,
			},
		},
	}
	mod := &ir.Module{Package: "main", Decls: []ir.Decl{threadUse(), fn}}
	out := Lower(mod)
	if errs := Validate(out); len(errs) > 0 {
		t.Fatalf("validate: %v\n\n%s", errs, Print(out))
	}
	text := Print(out)
	if strings.Contains(text, "call std.thread.chan") || strings.Contains(text, "call thread.chan") {
		t.Fatalf("thread.chan must lower as an intrinsic, not a qualified call:\n%s", text)
	}
	if !strings.Contains(text, "intrinsic chan_make") {
		t.Fatalf("expected chan_make intrinsic, got:\n%s", text)
	}
}

// ==== Stage 2c: inner-block defer scoping ====

// mkCallStmt is a tiny helper that wraps a print intrinsic in an
// ExprStmt so tests can name defer bodies by their printed argument.
func mkCallStmt(name string) ir.Stmt {
	return &ir.ExprStmt{X: &ir.IntrinsicCall{
		Kind: ir.IntrinsicPrintln,
		Args: []ir.Arg{{Value: &ir.StringLit{Parts: []ir.StringPart{{IsLit: true, Lit: name}}}}},
	}}
}

func mkDefer(name string) *ir.DeferStmt {
	return &ir.DeferStmt{Body: &ir.Block{Stmts: []ir.Stmt{mkCallStmt(name)}}}
}

// TestLowerDeferInnerBlockRunsAtBlockExit asserts that a defer
// declared inside an inner `{ … }` block runs at that block's exit,
// not only at the enclosing function's return.
func TestLowerDeferInnerBlockRunsAtBlockExit(t *testing.T) {
	// fn f() {
	//   { defer inner(); body() }
	//   after()
	// }
	// Expected order in the MIR dump: inner tag appears before after
	// tag, because the inner block's defer replay happens before the
	// function falls through to the `after()` call.
	fn := mkFn("f", ir.TUnit, &ir.Block{
		Stmts: []ir.Stmt{
			&ir.Block{
				Stmts: []ir.Stmt{
					mkDefer("inner"),
					mkCallStmt("body"),
				},
			},
			mkCallStmt("after"),
		},
	})
	mod := lowerHIR(t, fn)
	text := Print(mod)
	bodyI := strings.Index(text, `const "body"`)
	innerI := strings.Index(text, `const "inner"`)
	afterI := strings.Index(text, `const "after"`)
	if bodyI < 0 || innerI < 0 || afterI < 0 {
		t.Fatalf("expected body, inner, after in output:\n%s", text)
	}
	if !(bodyI < innerI && innerI < afterI) {
		t.Fatalf("expected ordering body < inner < after, got %d/%d/%d:\n%s",
			bodyI, innerI, afterI, text)
	}
}

// TestLowerDeferMixedFunctionAndBlockScope confirms LIFO semantics
// across an outer function-scoped defer and an inner block-scoped
// defer. Both must replay; the inner one runs when its block exits,
// the outer one runs at the function's return.
func TestLowerDeferMixedFunctionAndBlockScope(t *testing.T) {
	// fn f() {
	//   defer outer()
	//   { defer inner(); body() }
	//   after()
	// }
	fn := mkFn("f", ir.TUnit, &ir.Block{
		Stmts: []ir.Stmt{
			mkDefer("outer"),
			&ir.Block{
				Stmts: []ir.Stmt{
					mkDefer("inner"),
					mkCallStmt("body"),
				},
			},
			mkCallStmt("after"),
			&ir.ReturnStmt{},
		},
	})
	mod := lowerHIR(t, fn)
	text := Print(mod)
	body := strings.Index(text, `const "body"`)
	inner := strings.Index(text, `const "inner"`)
	after := strings.Index(text, `const "after"`)
	outer := strings.Index(text, `const "outer"`)
	if body < 0 || inner < 0 || after < 0 || outer < 0 {
		t.Fatalf("expected all four markers, got:\n%s", text)
	}
	// body -> inner (block exit) -> after (outer block stmt) -> outer (return)
	if !(body < inner && inner < after && after < outer) {
		t.Fatalf("expected body < inner < after < outer, got %d/%d/%d/%d:\n%s",
			body, inner, after, outer, text)
	}
}

// TestLowerDeferInLoopReplaysOnBreak ensures a defer declared inside
// a loop body replays on break — not just on return.
func TestLowerDeferInLoopReplaysOnBreak(t *testing.T) {
	// fn f() {
	//   for {
	//     defer loop()
	//     break
	//   }
	//   after()
	// }
	fn := mkFn("f", ir.TUnit, &ir.Block{
		Stmts: []ir.Stmt{
			&ir.ForStmt{
				Kind: ir.ForInfinite,
				Body: &ir.Block{
					Stmts: []ir.Stmt{
						mkDefer("loop"),
						&ir.BreakStmt{},
					},
				},
			},
			mkCallStmt("after"),
		},
	})
	mod := lowerHIR(t, fn)
	text := Print(mod)
	loopI := strings.Index(text, `const "loop"`)
	afterI := strings.Index(text, `const "after"`)
	if loopI < 0 {
		t.Fatalf("expected loop defer to be inlined on break:\n%s", text)
	}
	if afterI < 0 {
		t.Fatalf("expected after to appear:\n%s", text)
	}
	if loopI > afterI {
		t.Fatalf("loop defer must run before after (break inlines defers): got %d > %d:\n%s",
			loopI, afterI, text)
	}
}

// TestLowerDeferInLoopReplaysOnContinue ensures a defer declared
// inside a loop body replays on continue too.
func TestLowerDeferInLoopReplaysOnContinue(t *testing.T) {
	fn := mkFn("f", ir.TUnit, &ir.Block{
		Stmts: []ir.Stmt{
			&ir.ForStmt{
				Kind: ir.ForInfinite,
				Body: &ir.Block{
					Stmts: []ir.Stmt{
						mkDefer("loop"),
						&ir.ContinueStmt{},
					},
				},
			},
		},
	})
	mod := lowerHIR(t, fn)
	text := Print(mod)
	if !strings.Contains(text, `const "loop"`) {
		t.Fatalf("expected loop defer to be inlined on continue:\n%s", text)
	}
}

func TestLowerBlockScopedManagedLocalEmitsStorageDead(t *testing.T) {
	fn := &ir.FnDecl{
		Name:   "keep",
		Return: ir.TString,
		Params: []*ir.Param{{Name: "s", Type: ir.TString}},
		Body: &ir.Block{
			Stmts: []ir.Stmt{
				&ir.Block{
					Stmts: []ir.Stmt{
						&ir.LetStmt{
							Name:  "t",
							Type:  ir.TString,
							Value: &ir.Ident{Name: "s", Kind: ir.IdentParam, T: ir.TString},
						},
					},
				},
			},
			Result: &ir.Ident{Name: "s", Kind: ir.IdentParam, T: ir.TString},
		},
	}
	mod := lowerHIR(t, fn)
	text := Print(mod)
	live := strings.Index(text, "storage_live _2")
	dead := strings.Index(text, "storage_dead _2")
	if live < 0 || dead < 0 {
		t.Fatalf("expected block-scoped managed local to get storage markers:\n%s", text)
	}
	if live >= dead {
		t.Fatalf("expected storage_dead after storage_live, got %d >= %d:\n%s", live, dead, text)
	}
}

// TestLowerDeferInnerBlockDoesNotLeakToOuterReturn proves the fix for
// the Stage 2a limitation: a defer in an inner block must NOT also
// re-run at the function's return. If it did, the "inner" marker
// would appear twice in the dump.
func TestLowerDeferInnerBlockDoesNotLeakToOuterReturn(t *testing.T) {
	fn := mkFn("f", ir.TUnit, &ir.Block{
		Stmts: []ir.Stmt{
			&ir.Block{
				Stmts: []ir.Stmt{mkDefer("inner")},
			},
			&ir.ReturnStmt{},
		},
	})
	mod := lowerHIR(t, fn)
	text := Print(mod)
	if n := strings.Count(text, `const "inner"`); n != 1 {
		t.Fatalf("expected exactly one inner defer inline, got %d:\n%s", n, text)
	}
}

// ==== Stage 2c: thread.select arm expansion ====

func TestLowerSelectRecvArm(t *testing.T) {
	// fn arm(s: Select, ch: Channel<Int>, f: fn(Int) -> ()) -> Select {
	//   s.recv(ch, f)
	// }
	selectT := &ir.NamedType{Name: "Select"}
	chanInt := &ir.NamedType{Name: "Channel", Args: []ir.Type{ir.TInt}}
	cbT := &ir.FnType{Params: []ir.Type{ir.TInt}, Return: ir.TUnit}
	fn := &ir.FnDecl{
		Name:   "arm",
		Return: selectT,
		Params: []*ir.Param{
			{Name: "s", Type: selectT},
			{Name: "ch", Type: chanInt},
			{Name: "f", Type: cbT},
		},
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "s", Kind: ir.IdentParam, T: selectT},
				Name:     "recv",
				Args: []ir.Arg{
					{Value: &ir.Ident{Name: "ch", Kind: ir.IdentParam, T: chanInt}},
					{Value: &ir.Ident{Name: "f", Kind: ir.IdentParam, T: cbT}},
				},
				T: selectT,
			},
		},
	}
	mod := lowerHIR(t, fn)
	text := Print(mod)
	if !strings.Contains(text, "intrinsic select_recv(_1, _2, _3)") {
		t.Fatalf("expected select_recv intrinsic, got:\n%s", text)
	}
}

func TestLowerSelectSendArm(t *testing.T) {
	selectT := &ir.NamedType{Name: "Select"}
	chanInt := &ir.NamedType{Name: "Channel", Args: []ir.Type{ir.TInt}}
	cbT := &ir.FnType{Return: ir.TUnit}
	fn := &ir.FnDecl{
		Name:   "arm",
		Return: selectT,
		Params: []*ir.Param{
			{Name: "s", Type: selectT},
			{Name: "ch", Type: chanInt},
			{Name: "v", Type: ir.TInt},
			{Name: "f", Type: cbT},
		},
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "s", Kind: ir.IdentParam, T: selectT},
				Name:     "send",
				Args: []ir.Arg{
					{Value: &ir.Ident{Name: "ch", Kind: ir.IdentParam, T: chanInt}},
					{Value: &ir.Ident{Name: "v", Kind: ir.IdentParam, T: ir.TInt}},
					{Value: &ir.Ident{Name: "f", Kind: ir.IdentParam, T: cbT}},
				},
				T: selectT,
			},
		},
	}
	mod := lowerHIR(t, fn)
	text := Print(mod)
	if !strings.Contains(text, "intrinsic select_send(_1, _2, _3, _4)") {
		t.Fatalf("expected select_send intrinsic, got:\n%s", text)
	}
}

func TestLowerSelectTimeoutAndDefaultArms(t *testing.T) {
	selectT := &ir.NamedType{Name: "Select"}
	durT := &ir.NamedType{Name: "Duration"}
	cbT := &ir.FnType{Return: ir.TUnit}
	fn := &ir.FnDecl{
		Name:   "arms",
		Return: selectT,
		Params: []*ir.Param{
			{Name: "s", Type: selectT},
			{Name: "d", Type: durT},
			{Name: "f", Type: cbT},
		},
		Body: &ir.Block{
			Stmts: []ir.Stmt{
				&ir.ExprStmt{X: &ir.MethodCall{
					Receiver: &ir.Ident{Name: "s", Kind: ir.IdentParam, T: selectT},
					Name:     "timeout",
					Args: []ir.Arg{
						{Value: &ir.Ident{Name: "d", Kind: ir.IdentParam, T: durT}},
						{Value: &ir.Ident{Name: "f", Kind: ir.IdentParam, T: cbT}},
					},
					T: selectT,
				}},
			},
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "s", Kind: ir.IdentParam, T: selectT},
				Name:     "default",
				Args: []ir.Arg{
					{Value: &ir.Ident{Name: "f", Kind: ir.IdentParam, T: cbT}},
				},
				T: selectT,
			},
		},
	}
	mod := lowerHIR(t, fn)
	text := Print(mod)
	if !strings.Contains(text, "intrinsic select_timeout(") {
		t.Fatalf("expected select_timeout intrinsic, got:\n%s", text)
	}
	if !strings.Contains(text, "intrinsic select_default(") {
		t.Fatalf("expected select_default intrinsic, got:\n%s", text)
	}
}

// ==== Stage 2d: stdlib method intrinsics ====

// stdlibMethodTest is a tiny driver: build a method call on receiverT,
// lower it, and assert the rendered output contains want.
type stdlibMethodTest struct {
	name      string
	receiverT ir.Type
	method    string
	args      []ir.Arg
	retT      ir.Type
	want      string // substring the printer output must contain
}

func runStdlibMethodTest(t *testing.T, tc stdlibMethodTest) {
	t.Helper()
	fnRet := tc.retT
	if fnRet == nil {
		fnRet = ir.TUnit
	}
	fn := &ir.FnDecl{
		Name:   "call",
		Return: fnRet,
		Params: []*ir.Param{{Name: "r", Type: tc.receiverT}},
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "r", Kind: ir.IdentParam, T: tc.receiverT},
				Name:     tc.method,
				Args:     tc.args,
				T:        fnRet,
			},
		},
	}
	mod := lowerHIR(t, fn)
	text := Print(mod)
	if !strings.Contains(text, tc.want) {
		t.Fatalf("[%s] expected %q in:\n%s", tc.name, tc.want, text)
	}
}

func TestLowerStdlibListMethods(t *testing.T) {
	listInt := &ir.NamedType{Name: "List", Args: []ir.Type{ir.TInt}, Builtin: true}
	optInt := &ir.OptionalType{Inner: ir.TInt}
	setInt := &ir.NamedType{Name: "Set", Args: []ir.Type{ir.TInt}, Builtin: true}
	cases := []stdlibMethodTest{
		{
			name: "len", receiverT: listInt, method: "len", retT: ir.TInt,
			want: "intrinsic list_len(_1)",
		},
		{
			name: "push", receiverT: listInt, method: "push", retT: ir.TUnit,
			args: []ir.Arg{{Value: &ir.IntLit{Text: "42", T: ir.TInt}}},
			want: "intrinsic list_push(_1, const 42 Int)",
		},
		{
			name: "get", receiverT: listInt, method: "get", retT: ir.TInt,
			args: []ir.Arg{{Value: &ir.IntLit{Text: "0", T: ir.TInt}}},
			want: "intrinsic list_get(_1, const 0 Int)",
		},
		{
			name: "isEmpty", receiverT: listInt, method: "isEmpty", retT: ir.TBool,
			want: "intrinsic list_is_empty(_1)",
		},
		{
			name: "first", receiverT: listInt, method: "first", retT: optInt,
			want: "intrinsic list_first(_1)",
		},
		{
			name: "sorted", receiverT: listInt, method: "sorted", retT: listInt,
			want: "intrinsic list_sorted(_1)",
		},
		{
			name: "contains", receiverT: listInt, method: "contains", retT: ir.TBool,
			args: []ir.Arg{{Value: &ir.IntLit{Text: "1", T: ir.TInt}}},
			want: "intrinsic list_contains(_1, const 1 Int)",
		},
		{
			name: "indexOf", receiverT: listInt, method: "indexOf", retT: optInt,
			args: []ir.Arg{{Value: &ir.IntLit{Text: "5", T: ir.TInt}}},
			want: "intrinsic list_index_of(_1, const 5 Int)",
		},
		{
			name: "toSet", receiverT: listInt, method: "toSet", retT: setInt,
			want: "intrinsic list_to_set(_1)",
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) { runStdlibMethodTest(t, c) })
	}
}

func TestLowerStdlibMapMethods(t *testing.T) {
	mapType := &ir.NamedType{Name: "Map", Args: []ir.Type{ir.TString, ir.TInt}, Builtin: true}
	optInt := &ir.OptionalType{Inner: ir.TInt}
	listKey := &ir.NamedType{Name: "List", Args: []ir.Type{ir.TString}, Builtin: true}
	cases := []stdlibMethodTest{
		{
			name: "get", receiverT: mapType, method: "get", retT: optInt,
			args: []ir.Arg{{Value: &ir.StringLit{Parts: []ir.StringPart{{IsLit: true, Lit: "k"}}}}},
			want: `intrinsic map_get(_1, const "k")`,
		},
		{
			name: "set", receiverT: mapType, method: "set", retT: ir.TUnit,
			args: []ir.Arg{
				{Value: &ir.StringLit{Parts: []ir.StringPart{{IsLit: true, Lit: "k"}}}},
				{Value: &ir.IntLit{Text: "1", T: ir.TInt}},
			},
			want: `intrinsic map_set(_1, const "k", const 1 Int)`,
		},
		{
			name: "contains", receiverT: mapType, method: "contains", retT: ir.TBool,
			args: []ir.Arg{{Value: &ir.StringLit{Parts: []ir.StringPart{{IsLit: true, Lit: "k"}}}}},
			want: `intrinsic map_contains(_1, const "k")`,
		},
		{
			name: "len", receiverT: mapType, method: "len", retT: ir.TInt,
			want: "intrinsic map_len(_1)",
		},
		{
			name: "keys", receiverT: mapType, method: "keys", retT: listKey,
			want: "intrinsic map_keys(_1)",
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) { runStdlibMethodTest(t, c) })
	}
}

func TestLowerStdlibSetMethods(t *testing.T) {
	setInt := &ir.NamedType{Name: "Set", Args: []ir.Type{ir.TInt}, Builtin: true}
	listInt := &ir.NamedType{Name: "List", Args: []ir.Type{ir.TInt}, Builtin: true}
	cases := []stdlibMethodTest{
		{
			name: "insert", receiverT: setInt, method: "insert", retT: ir.TUnit,
			args: []ir.Arg{{Value: &ir.IntLit{Text: "1", T: ir.TInt}}},
			want: "intrinsic set_insert(_1, const 1 Int)",
		},
		{
			name: "contains", receiverT: setInt, method: "contains", retT: ir.TBool,
			args: []ir.Arg{{Value: &ir.IntLit{Text: "2", T: ir.TInt}}},
			want: "intrinsic set_contains(_1, const 2 Int)",
		},
		{
			name: "len", receiverT: setInt, method: "len", retT: ir.TInt,
			want: "intrinsic set_len(_1)",
		},
		{
			name: "toList", receiverT: setInt, method: "toList", retT: listInt,
			want: "intrinsic set_to_list(_1)",
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) { runStdlibMethodTest(t, c) })
	}
}

func TestLowerStdlibStringMethods(t *testing.T) {
	listStr := &ir.NamedType{Name: "List", Args: []ir.Type{ir.TString}, Builtin: true}
	listChar := &ir.NamedType{Name: "List", Args: []ir.Type{ir.TChar}, Builtin: true}
	listByte := &ir.NamedType{Name: "List", Args: []ir.Type{ir.TByte}, Builtin: true}
	optInt := &ir.OptionalType{Inner: ir.TInt}
	sep := &ir.StringLit{Parts: []ir.StringPart{{IsLit: true, Lit: ","}}}
	cases := []stdlibMethodTest{
		{
			name: "len", receiverT: ir.TString, method: "len", retT: ir.TInt,
			want: "intrinsic string_len(_1)",
		},
		{
			name: "contains", receiverT: ir.TString, method: "contains", retT: ir.TBool,
			args: []ir.Arg{{Value: sep}},
			want: `intrinsic string_contains(_1, const ",")`,
		},
		{
			name: "startsWith", receiverT: ir.TString, method: "startsWith", retT: ir.TBool,
			args: []ir.Arg{{Value: sep}},
			want: `intrinsic string_starts_with(_1, const ",")`,
		},
		{
			name: "endsWith", receiverT: ir.TString, method: "endsWith", retT: ir.TBool,
			args: []ir.Arg{{Value: sep}},
			want: `intrinsic string_ends_with(_1, const ",")`,
		},
		{
			name: "indexOf", receiverT: ir.TString, method: "indexOf", retT: optInt,
			args: []ir.Arg{{Value: sep}},
			want: `intrinsic string_index_of(_1, const ",")`,
		},
		{
			name: "split", receiverT: ir.TString, method: "split", retT: listStr,
			args: []ir.Arg{{Value: sep}},
			want: `intrinsic string_split(_1, const ",")`,
		},
		{
			name: "trim", receiverT: ir.TString, method: "trim", retT: ir.TString,
			want: "intrinsic string_trim(_1)",
		},
		{
			name: "toUpper", receiverT: ir.TString, method: "toUpper", retT: ir.TString,
			want: "intrinsic string_to_upper(_1)",
		},
		{
			name: "chars", receiverT: ir.TString, method: "chars", retT: listChar,
			want: "intrinsic string_chars(_1)",
		},
		{
			name: "bytes", receiverT: ir.TString, method: "bytes", retT: listByte,
			want: "intrinsic string_bytes(_1)",
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) { runStdlibMethodTest(t, c) })
	}
}

func TestLowerStdlibOptionMethods(t *testing.T) {
	// Test both surface forms: Option<T> named and T? optional.
	optInt := &ir.OptionalType{Inner: ir.TInt}
	namedOpt := &ir.NamedType{Name: "Option", Args: []ir.Type{ir.TInt}}
	cases := []stdlibMethodTest{
		{
			name: "isSome_optional", receiverT: optInt, method: "isSome", retT: ir.TBool,
			want: "intrinsic option_is_some(_1)",
		},
		{
			name: "isNone_optional", receiverT: optInt, method: "isNone", retT: ir.TBool,
			want: "intrinsic option_is_none(_1)",
		},
		{
			name: "unwrap_optional", receiverT: optInt, method: "unwrap", retT: ir.TInt,
			want: "intrinsic option_unwrap(_1)",
		},
		{
			name: "unwrapOr_optional", receiverT: optInt, method: "unwrapOr", retT: ir.TInt,
			args: []ir.Arg{{Value: &ir.IntLit{Text: "0", T: ir.TInt}}},
			want: "intrinsic option_unwrap_or(_1, const 0 Int)",
		},
		{
			name: "isSome_named", receiverT: namedOpt, method: "isSome", retT: ir.TBool,
			want: "intrinsic option_is_some(_1)",
		},
		{
			name: "unwrap_named", receiverT: namedOpt, method: "unwrap", retT: ir.TInt,
			want: "intrinsic option_unwrap(_1)",
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) { runStdlibMethodTest(t, c) })
	}
}

func TestLowerStdlibResultMethods(t *testing.T) {
	resT := &ir.NamedType{Name: "Result", Args: []ir.Type{ir.TInt, ir.TString}}
	cases := []stdlibMethodTest{
		{
			name: "isOk", receiverT: resT, method: "isOk", retT: ir.TBool,
			want: "intrinsic result_is_ok(_1)",
		},
		{
			name: "isErr", receiverT: resT, method: "isErr", retT: ir.TBool,
			want: "intrinsic result_is_err(_1)",
		},
		{
			name: "unwrap", receiverT: resT, method: "unwrap", retT: ir.TInt,
			want: "intrinsic result_unwrap(_1)",
		},
		{
			name: "unwrapOr", receiverT: resT, method: "unwrapOr", retT: ir.TInt,
			args: []ir.Arg{{Value: &ir.IntLit{Text: "0", T: ir.TInt}}},
			want: "intrinsic result_unwrap_or(_1, const 0 Int)",
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) { runStdlibMethodTest(t, c) })
	}
}

// Regression: user-defined struct methods must NOT get swallowed by
// the stdlib recogniser. `Point.sorted()` for a user Point struct
// should route through the regular method call path, not emit a
// `list_sorted` intrinsic.
func TestLowerStdlibRecognizerDoesNotShadowUserMethods(t *testing.T) {
	pointT := &ir.NamedType{Name: "Point"}
	pointDecl := &ir.StructDecl{
		Name:   "Point",
		Fields: []*ir.Field{{Name: "x", Type: ir.TInt, Exported: true}},
		Methods: []*ir.FnDecl{{
			Name:   "len",
			Return: ir.TInt,
			Params: []*ir.Param{{Name: "self", Type: pointT}},
			Body:   &ir.Block{Result: &ir.IntLit{Text: "0", T: ir.TInt}},
		}},
	}
	caller := &ir.FnDecl{
		Name:   "caller",
		Return: ir.TInt,
		Params: []*ir.Param{{Name: "p", Type: pointT}},
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "p", Kind: ir.IdentParam, T: pointT},
				Name:     "len",
				T:        ir.TInt,
			},
		},
	}
	mod := &ir.Module{Package: "main", Decls: []ir.Decl{pointDecl, caller}}
	out := Lower(mod)
	if errs := Validate(out); len(errs) > 0 {
		t.Fatalf("validate: %v\n\n%s", errs, Print(out))
	}
	text := Print(out)
	if strings.Contains(text, "intrinsic list_len") || strings.Contains(text, "intrinsic string_len") {
		t.Fatalf("user Point.len() must not become a stdlib intrinsic:\n%s", text)
	}
	if !strings.Contains(text, "call Point__len(") {
		t.Fatalf("expected regular method call Point__len, got:\n%s", text)
	}
}

// Regression: the stdlib recogniser must not fire on concurrency
// receivers. `ch.contains(x)` isn't a real Channel method but a
// receiver-type match on Channel must still beat the stdlib check.
// (This also guards against a hypothetical name collision.)
func TestLowerStdlibRecognizerDoesNotShadowConcurrencyMethods(t *testing.T) {
	chInt := &ir.NamedType{Name: "Channel", Args: []ir.Type{ir.TInt}}
	optInt := &ir.OptionalType{Inner: ir.TInt}
	fn := &ir.FnDecl{
		Name:   "pop",
		Return: optInt,
		Params: []*ir.Param{{Name: "ch", Type: chInt}},
		Body: &ir.Block{
			Result: &ir.MethodCall{
				Receiver: &ir.Ident{Name: "ch", Kind: ir.IdentParam, T: chInt},
				Name:     "recv",
				T:        optInt,
			},
		},
	}
	mod := lowerHIR(t, fn)
	text := Print(mod)
	if !strings.Contains(text, "intrinsic chan_recv(") {
		t.Fatalf("expected chan_recv, got:\n%s", text)
	}
}

// ==== CFG helper tests ====

func TestSuccessorsCoversAllTerminators(t *testing.T) {
	cases := []struct {
		name string
		term Terminator
		want []BlockID
	}{
		{"nil", nil, nil},
		{"goto", &GotoTerm{Target: 3}, []BlockID{3}},
		{"branch", &BranchTerm{Then: 1, Else: 2}, []BlockID{1, 2}},
		{
			"switchInt",
			&SwitchIntTerm{
				Cases: []SwitchCase{
					{Value: 0, Target: 4},
					{Value: 1, Target: 5},
				},
				Default: 6,
			},
			[]BlockID{4, 5, 6},
		},
		{"return", &ReturnTerm{}, nil},
		{"unreachable", &UnreachableTerm{}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Successors(tc.term)
			if len(got) != len(tc.want) {
				t.Fatalf("Successors(%s) length: got %v, want %v", tc.name, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("Successors(%s)[%d] = %d, want %d", tc.name, i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestReachableBlocksFollowsCFG(t *testing.T) {
	fn := &Function{Name: "reach", ReturnType: TUnit}
	fn.ReturnLocal = fn.NewLocal("_return", TUnit, false, Span{})
	fn.Locals[fn.ReturnLocal].IsReturn = true
	b0 := fn.NewBlock(Span{})
	b1 := fn.NewBlock(Span{})
	b2 := fn.NewBlock(Span{}) // orphan — never reached
	fn.Entry = b0
	fn.Block(b0).SetTerminator(&GotoTerm{Target: b1})
	fn.Block(b1).SetTerminator(&ReturnTerm{})
	fn.Block(b2).SetTerminator(&ReturnTerm{})

	seen := ReachableBlocks(fn)
	if !seen[b0] || !seen[b1] {
		t.Fatalf("entry / goto target not reachable: %v", seen)
	}
	if seen[b2] {
		t.Fatalf("orphan block _%d marked reachable: %v", b2, seen)
	}
}

func TestReachableBlocksHandlesMissingEntry(t *testing.T) {
	fn := &Function{Name: "bad", ReturnType: TUnit}
	if got := ReachableBlocks(fn); got != nil {
		t.Fatalf("expected nil for empty fn, got %v", got)
	}
}

// ==== new validator checks ====

func TestValidateRejectsReturnTypeMismatch(t *testing.T) {
	fn := &Function{Name: "mismatch", ReturnType: TInt}
	// Return local is Bool, not Int — the lowerer should never emit this
	// shape; the validator must catch it.
	fn.ReturnLocal = fn.NewLocal("_return", TBool, false, Span{})
	fn.Locals[fn.ReturnLocal].IsReturn = true
	bb := fn.NewBlock(Span{})
	fn.Entry = bb
	fn.Block(bb).SetTerminator(&ReturnTerm{})
	mod := &Module{Package: "main", Functions: []*Function{fn}, Layouts: NewLayoutTable()}
	errs := Validate(mod)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "does not match declared return type") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected return-type-mismatch error, got %v", errs)
	}
}

func TestValidateRejectsDuplicateSwitchCaseValue(t *testing.T) {
	fn := &Function{Name: "dup_switch", ReturnType: TUnit}
	fn.ReturnLocal = fn.NewLocal("_return", TUnit, false, Span{})
	fn.Locals[fn.ReturnLocal].IsReturn = true
	scrut := fn.NewLocal("s", TInt, false, Span{})
	b0 := fn.NewBlock(Span{})
	b1 := fn.NewBlock(Span{})
	b2 := fn.NewBlock(Span{})
	bDef := fn.NewBlock(Span{})
	fn.Entry = b0
	fn.Block(b0).SetTerminator(&SwitchIntTerm{
		Scrutinee: &CopyOp{Place: Place{Local: scrut}, T: TInt},
		Cases: []SwitchCase{
			{Value: 0, Target: b1},
			{Value: 0, Target: b2}, // duplicate value
		},
		Default: bDef,
	})
	fn.Block(b1).SetTerminator(&ReturnTerm{})
	fn.Block(b2).SetTerminator(&ReturnTerm{})
	fn.Block(bDef).SetTerminator(&ReturnTerm{})

	mod := &Module{Package: "main", Functions: []*Function{fn}, Layouts: NewLayoutTable()}
	errs := Validate(mod)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "duplicates Cases") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected duplicate-switch-case error, got %v", errs)
	}
}

func TestValidateRejectsStrayIsParam(t *testing.T) {
	fn := &Function{Name: "strayparam", ReturnType: TUnit}
	fn.ReturnLocal = fn.NewLocal("_return", TUnit, false, Span{})
	fn.Locals[fn.ReturnLocal].IsReturn = true
	// A local that claims to be a param but isn't listed in fn.Params.
	stray := fn.NewLocal("x", TInt, false, Span{})
	fn.Locals[stray].IsParam = true
	bb := fn.NewBlock(Span{})
	fn.Entry = bb
	fn.Block(bb).SetTerminator(&ReturnTerm{})
	mod := &Module{Package: "main", Functions: []*Function{fn}, Layouts: NewLayoutTable()}
	errs := Validate(mod)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "marked IsParam but not in fn.Params") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected stray-IsParam error, got %v", errs)
	}
}

func TestValidateRejectsMultipleIsReturn(t *testing.T) {
	fn := &Function{Name: "doublereturn", ReturnType: TUnit}
	fn.ReturnLocal = fn.NewLocal("_return", TUnit, false, Span{})
	fn.Locals[fn.ReturnLocal].IsReturn = true
	// A second local mis-flagged as IsReturn.
	extra := fn.NewLocal("also_return", TUnit, false, Span{})
	fn.Locals[extra].IsReturn = true
	bb := fn.NewBlock(Span{})
	fn.Entry = bb
	fn.Block(bb).SetTerminator(&ReturnTerm{})
	mod := &Module{Package: "main", Functions: []*Function{fn}, Layouts: NewLayoutTable()}
	errs := Validate(mod)
	foundMulti := false
	foundWrongLocal := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "more than one local marked IsReturn") {
			foundMulti = true
		}
		if strings.Contains(e.Error(), "marked IsReturn but ReturnLocal=_") {
			foundWrongLocal = true
		}
	}
	if !foundMulti {
		t.Fatalf("expected multi-IsReturn error, got %v", errs)
	}
	if !foundWrongLocal {
		t.Fatalf("expected wrong-ReturnLocal error for stray IsReturn, got %v", errs)
	}
}

// ==== stringer tests ====

func TestEnumStringers(t *testing.T) {
	cases := []struct {
		got, want string
	}{
		{IntrinsicPrintln.String(), "println"},
		{IntrinsicChanRecv.String(), "chan_recv"},
		{IntrinsicListPush.String(), "list_push"},
		{IntrinsicRawNull.String(), "raw_null"},
		{IntrinsicInvalid.String(), "invalid"},
		{IntrinsicKind(9999).String(), "invalid"},

		{UnNeg.String(), "-"},
		{UnNot.String(), "!"},
		{UnaryOp(9999).String(), "?"},

		{BinAdd.String(), "+"},
		{BinLeq.String(), "<="},
		{BinShr.String(), ">>"},
		{BinaryOp(9999).String(), "?"},

		{AggTuple.String(), "tuple"},
		{AggClosure.String(), "closure"},
		{AggregateKind(9999).String(), "?"},

		{CastIntToFloat.String(), "int_to_float"},
		{CastBitcast.String(), "bitcast"},
		{CastKind(9999).String(), "?"},

		{NullaryNone.String(), "none"},
		{NullaryRVKind(9999).String(), "?"},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("stringer: got %q, want %q", tc.got, tc.want)
		}
	}
}

// TestPrintUsesStringers confirms the printer still emits the expected
// tokens now that it delegates to the enum Stringers.
func TestPrintUsesStringers(t *testing.T) {
	fn := &Function{Name: "show", ReturnType: TUnit}
	fn.ReturnLocal = fn.NewLocal("_return", TUnit, false, Span{})
	fn.Locals[fn.ReturnLocal].IsReturn = true
	bb := fn.NewBlock(Span{})
	fn.Entry = bb
	fn.Block(bb).Append(&IntrinsicInstr{
		Kind: IntrinsicPrintln,
		Args: []Operand{&ConstOp{Const: &StringConst{Value: "hi"}, T: TString}},
	})
	fn.Block(bb).SetTerminator(&ReturnTerm{})
	mod := &Module{Package: "main", Functions: []*Function{fn}, Layouts: NewLayoutTable()}
	out := Print(mod)
	if !strings.Contains(out, "intrinsic println(") {
		t.Fatalf("printer should emit intrinsic println(...):\n%s", out)
	}
}

// ==== storage-marker target tests ====

func TestValidateRejectsStorageMarkerOnParam(t *testing.T) {
	fn := &Function{Name: "bad_live", ReturnType: TUnit}
	fn.ReturnLocal = fn.NewLocal("_return", TUnit, false, Span{})
	fn.Locals[fn.ReturnLocal].IsReturn = true
	pid := fn.NewLocal("x", TInt, false, Span{})
	fn.Locals[pid].IsParam = true
	fn.Params = append(fn.Params, pid)
	bb := fn.NewBlock(Span{})
	fn.Entry = bb
	fn.Block(bb).Append(&StorageLiveInstr{Local: pid})
	fn.Block(bb).SetTerminator(&ReturnTerm{})
	mod := &Module{Package: "main", Functions: []*Function{fn}, Layouts: NewLayoutTable()}
	errs := Validate(mod)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "StorageLive on parameter") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected storage-on-param error, got %v", errs)
	}
}

func TestValidateRejectsStorageMarkerOnReturnSlot(t *testing.T) {
	fn := &Function{Name: "bad_dead", ReturnType: TUnit}
	fn.ReturnLocal = fn.NewLocal("_return", TUnit, false, Span{})
	fn.Locals[fn.ReturnLocal].IsReturn = true
	bb := fn.NewBlock(Span{})
	fn.Entry = bb
	fn.Block(bb).Append(&StorageDeadInstr{Local: fn.ReturnLocal})
	fn.Block(bb).SetTerminator(&ReturnTerm{})
	mod := &Module{Package: "main", Functions: []*Function{fn}, Layouts: NewLayoutTable()}
	errs := Validate(mod)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "StorageDead on return slot") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected storage-on-return error, got %v", errs)
	}
}

func TestValidateRejectsDuplicateParamID(t *testing.T) {
	fn := &Function{Name: "dupparam", ReturnType: TUnit}
	fn.ReturnLocal = fn.NewLocal("_return", TUnit, false, Span{})
	fn.Locals[fn.ReturnLocal].IsReturn = true
	pid := fn.NewLocal("x", TInt, false, Span{})
	fn.Locals[pid].IsParam = true
	fn.Params = []LocalID{pid, pid}
	bb := fn.NewBlock(Span{})
	fn.Entry = bb
	fn.Block(bb).SetTerminator(&ReturnTerm{})
	mod := &Module{Package: "main", Functions: []*Function{fn}, Layouts: NewLayoutTable()}
	errs := Validate(mod)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "appears more than once") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected duplicate-param error, got %v", errs)
	}
}
