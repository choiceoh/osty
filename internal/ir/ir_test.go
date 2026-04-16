package ir

import (
	"testing"
)

// ==== Constant folding ====

func TestOptimizeFoldsIntArithmetic(t *testing.T) {
	// (1 + 2) * 3
	e := &BinaryExpr{
		Op:   BinMul,
		Left: &BinaryExpr{Op: BinAdd, Left: intLit("1"), Right: intLit("2"), T: TInt},
		Right: intLit("3"),
		T:    TInt,
	}
	m := moduleWithExpr(e)
	Optimize(m, OptimizeOptions{})
	got := m.Script[0].(*ExprStmt).X
	lit, ok := got.(*IntLit)
	if !ok {
		t.Fatalf("expected IntLit after folding, got %T", got)
	}
	if lit.Text != "9" {
		t.Fatalf("expected 9, got %q", lit.Text)
	}
}

func TestOptimizeFoldsBoolLogic(t *testing.T) {
	// true && false
	e := &BinaryExpr{
		Op:    BinAnd,
		Left:  &BoolLit{Value: true},
		Right: &BoolLit{Value: false},
		T:     TBool,
	}
	m := moduleWithExpr(e)
	Optimize(m, OptimizeOptions{})
	got := m.Script[0].(*ExprStmt).X
	lit, ok := got.(*BoolLit)
	if !ok {
		t.Fatalf("expected BoolLit after folding, got %T", got)
	}
	if lit.Value != false {
		t.Fatalf("expected false, got %v", lit.Value)
	}
}

func TestOptimizeFoldsStringConcat(t *testing.T) {
	// "hi, " + "world"
	e := &BinaryExpr{
		Op: BinAdd,
		Left: &StringLit{
			Parts: []StringPart{{IsLit: true, Lit: "hi, "}},
		},
		Right: &StringLit{
			Parts: []StringPart{{IsLit: true, Lit: "world"}},
		},
		T: TString,
	}
	m := moduleWithExpr(e)
	Optimize(m, OptimizeOptions{})
	got := m.Script[0].(*ExprStmt).X
	lit, ok := got.(*StringLit)
	if !ok {
		t.Fatalf("expected StringLit after folding, got %T", got)
	}
	if text := stringLitText(lit); text != "hi, world" {
		t.Fatalf("expected %q, got %q", "hi, world", text)
	}
}

func TestOptimizeSimplifiesIdentities(t *testing.T) {
	// x + 0 → x
	e := &BinaryExpr{
		Op:    BinAdd,
		Left:  &Ident{Name: "x", Kind: IdentLocal, T: TInt},
		Right: intLit("0"),
		T:     TInt,
	}
	m := moduleWithExpr(e)
	Optimize(m, OptimizeOptions{})
	got := m.Script[0].(*ExprStmt).X
	if id, ok := got.(*Ident); !ok || id.Name != "x" {
		t.Fatalf("expected bare Ident x, got %T (%v)", got, got)
	}
}

func TestOptimizeDeadCodeEliminatesAfterReturn(t *testing.T) {
	blk := &Block{
		Stmts: []Stmt{
			&ReturnStmt{Value: intLit("1")},
			&ExprStmt{X: intLit("2")}, // unreachable
			&ExprStmt{X: intLit("3")}, // unreachable
		},
	}
	fn := &FnDecl{Name: "f", Return: TInt, Body: blk}
	m := &Module{Package: "main", Decls: []Decl{fn}}
	Optimize(m, OptimizeOptions{})
	if got := len(fn.Body.Stmts); got != 1 {
		t.Fatalf("expected 1 stmt after DCE, got %d", got)
	}
	if _, ok := fn.Body.Stmts[0].(*ReturnStmt); !ok {
		t.Fatalf("expected ReturnStmt, got %T", fn.Body.Stmts[0])
	}
}

func TestOptimizeFoldsConstantIf(t *testing.T) {
	// if true { 1 } else { 2 }
	e := &IfExpr{
		Cond: &BoolLit{Value: true},
		Then: &Block{Result: intLit("1")},
		Else: &Block{Result: intLit("2")},
		T:    TInt,
	}
	m := moduleWithExpr(e)
	Optimize(m, OptimizeOptions{})
	got := m.Script[0].(*ExprStmt).X
	if lit, ok := got.(*IntLit); !ok || lit.Text != "1" {
		t.Fatalf("expected IntLit 1 after if-fold, got %T (%v)", got, got)
	}
}

func TestOptimizeRespectsDisableFlags(t *testing.T) {
	e := &BinaryExpr{Op: BinAdd, Left: intLit("1"), Right: intLit("2"), T: TInt}
	m := moduleWithExpr(e)
	Optimize(m, OptimizeOptions{DisableConstFold: true})
	got := m.Script[0].(*ExprStmt).X
	if _, ok := got.(*BinaryExpr); !ok {
		t.Fatalf("expected BinaryExpr preserved with DisableConstFold, got %T", got)
	}
}

// ==== Decision tree compilation ====

func TestCompileDecisionTreeVariantSwitch(t *testing.T) {
	// match x { Some(v) => v, None => 0 }
	arms := []*MatchArm{
		{
			Pattern: &VariantPat{Variant: "Some", Args: []Pattern{&IdentPat{Name: "v"}}},
			Body:    &Block{Result: &Ident{Name: "v", Kind: IdentLocal, T: TInt}},
		},
		{
			Pattern: &VariantPat{Variant: "None"},
			Body:    &Block{Result: intLit("0")},
		},
	}
	tree := CompileDecisionTree(&NamedType{Name: "Option", Args: []Type{TInt}, Builtin: true}, arms)
	sw, ok := tree.(*DecisionSwitch)
	if !ok {
		t.Fatalf("expected DecisionSwitch, got %T (%v)", tree, tree)
	}
	if sw.Kind != SwitchVariant {
		t.Fatalf("expected SwitchVariant, got %v", sw.Kind)
	}
	if len(sw.Cases) != 2 {
		t.Fatalf("expected 2 cases, got %d", len(sw.Cases))
	}
	if sw.Cases[0].Variant != "Some" || sw.Cases[1].Variant != "None" {
		t.Fatalf("unexpected case order: %+v", sw.Cases)
	}
}

func TestCompileDecisionTreeLitSwitch(t *testing.T) {
	// match x { 1 => "a", 2 => "b", _ => "?" }
	arms := []*MatchArm{
		{Pattern: &LitPat{Value: intLit("1")}, Body: &Block{Result: strLit("a")}},
		{Pattern: &LitPat{Value: intLit("2")}, Body: &Block{Result: strLit("b")}},
		{Pattern: &WildPat{}, Body: &Block{Result: strLit("?")}},
	}
	tree := CompileDecisionTree(TInt, arms)
	sw, ok := tree.(*DecisionSwitch)
	if !ok {
		t.Fatalf("expected DecisionSwitch, got %T", tree)
	}
	if sw.Kind != SwitchLit {
		t.Fatalf("expected SwitchLit, got %v", sw.Kind)
	}
	if len(sw.Cases) != 2 {
		t.Fatalf("expected 2 cases, got %d", len(sw.Cases))
	}
	if _, ok := sw.Default.(*DecisionLeaf); !ok {
		t.Fatalf("expected catch-all leaf as Default, got %T", sw.Default)
	}
}

func TestCompileDecisionTreeCatchAllShortCircuits(t *testing.T) {
	arms := []*MatchArm{
		{Pattern: &IdentPat{Name: "x"}, Body: &Block{Result: intLit("1")}},
		{Pattern: &VariantPat{Variant: "Some", Args: []Pattern{&IdentPat{Name: "v"}}}, Body: &Block{}},
	}
	tree := CompileDecisionTree(nil, arms)
	// first arm is a catch-all; we should reach leaf 0 with a binding.
	bind, ok := tree.(*DecisionBind)
	if !ok {
		t.Fatalf("expected DecisionBind at root, got %T", tree)
	}
	leaf, ok := bind.Next.(*DecisionLeaf)
	if !ok || leaf.ArmIndex != 0 {
		t.Fatalf("expected leaf(arm=0), got %T (%v)", bind.Next, bind.Next)
	}
}

func TestValidateDecisionTreeRejectsOutOfRangeLeaf(t *testing.T) {
	tree := &DecisionLeaf{ArmIndex: 99}
	errs := ValidateDecisionTree(tree, 2)
	if len(errs) == 0 {
		t.Fatal("expected out-of-range error")
	}
}

// ==== Capture analysis ====

func TestComputeCapturesBasic(t *testing.T) {
	// |y| y + x   — x is captured from outer scope
	body := &Block{
		Result: &BinaryExpr{
			Op:    BinAdd,
			Left:  &Ident{Name: "y", Kind: IdentParam, T: TInt},
			Right: &Ident{Name: "x", Kind: IdentLocal, T: TInt},
			T:     TInt,
		},
	}
	params := []*Param{{Name: "y", Type: TInt}}
	caps := ComputeCaptures(body, params)
	if len(caps) != 1 {
		t.Fatalf("expected 1 capture, got %d (%+v)", len(caps), caps)
	}
	if caps[0].Name != "x" {
		t.Fatalf("expected x, got %s", caps[0].Name)
	}
	if caps[0].Kind != CaptureLocal {
		t.Fatalf("expected CaptureLocal, got %v", caps[0].Kind)
	}
}

func TestComputeCapturesIgnoresGlobalsAndBuiltins(t *testing.T) {
	// Identifiers resolved to top-level fns / builtins are not captures.
	body := &Block{
		Result: &CallExpr{
			Callee: &Ident{Name: "println", Kind: IdentBuiltin, T: nil},
			Args:   []Arg{{Value: &Ident{Name: "helper", Kind: IdentFn, T: nil}}},
			T:      TUnit,
		},
	}
	caps := ComputeCaptures(body, nil)
	if len(caps) != 0 {
		t.Fatalf("expected no captures, got %+v", caps)
	}
}

func TestComputeCapturesShadowingInnerLet(t *testing.T) {
	// { let x = 1; x + 1 } — x is shadowed, no capture
	body := &Block{
		Stmts: []Stmt{
			&LetStmt{Name: "x", Value: intLit("1"), Type: TInt},
		},
		Result: &BinaryExpr{
			Op:    BinAdd,
			Left:  &Ident{Name: "x", Kind: IdentLocal, T: TInt},
			Right: intLit("1"),
			T:     TInt,
		},
	}
	caps := ComputeCaptures(body, nil)
	if len(caps) != 0 {
		t.Fatalf("expected 0 captures (inner let shadows), got %+v", caps)
	}
}

func TestComputeCapturesNestedClosurePromotes(t *testing.T) {
	// outer body contains a closure that captures x; outer doesn't bind x,
	// so x should propagate up.
	nested := &Closure{
		Params:   nil,
		Body:     &Block{Result: &Ident{Name: "x", Kind: IdentLocal, T: TInt}},
		Captures: []*Capture{{Name: "x", Kind: CaptureLocal, T: TInt}},
		T:        &FnType{Return: TInt},
	}
	outer := &Block{Result: nested}
	caps := ComputeCaptures(outer, nil)
	if len(caps) != 1 || caps[0].Name != "x" {
		t.Fatalf("expected propagated capture x, got %+v", caps)
	}
}

// ==== Qualified NamedType ====

func TestNamedTypeQualifiedName(t *testing.T) {
	n := &NamedType{Package: "std.io", Name: "Reader"}
	if got := n.QualifiedName(); got != "std.io.Reader" {
		t.Fatalf("unexpected QualifiedName: %q", got)
	}
	if got := n.String(); got != "std.io.Reader" {
		t.Fatalf("unexpected String: %q", got)
	}
}

func TestNamedTypeBareNoPackage(t *testing.T) {
	n := &NamedType{Name: "List", Args: []Type{TInt}, Builtin: true}
	if got := n.QualifiedName(); got != "List" {
		t.Fatalf("unexpected QualifiedName: %q", got)
	}
	if got := n.String(); got != "List<Int>" {
		t.Fatalf("unexpected String: %q", got)
	}
}

// ==== Arg keyword preservation ====

func TestArgIsKeyword(t *testing.T) {
	pos := Arg{Value: intLit("1")}
	kw := Arg{Name: "x", Value: intLit("1")}
	if pos.IsKeyword() {
		t.Fatal("positional Arg should not report IsKeyword")
	}
	if !kw.IsKeyword() {
		t.Fatal("keyword Arg should report IsKeyword")
	}
}

// ==== Validate covers new invariants ====

func TestValidateRejectsParamWithBothNameAndPattern(t *testing.T) {
	fn := &FnDecl{
		Name:   "f",
		Return: TUnit,
		Params: []*Param{{Name: "x", Pattern: &IdentPat{Name: "x"}, Type: TInt}},
		Body:   &Block{},
	}
	m := &Module{Package: "main", Decls: []Decl{fn}}
	errs := Validate(m)
	if len(errs) == 0 {
		t.Fatal("expected validation error for param with both Name and Pattern")
	}
}

func TestValidateRejectsForWithBothVarAndPattern(t *testing.T) {
	fn := &FnDecl{
		Name:   "f",
		Return: TUnit,
		Body: &Block{Stmts: []Stmt{
			&ForStmt{Kind: ForIn, Var: "x", Pattern: &IdentPat{Name: "x"}, Iter: &Ident{Name: "xs", T: TInt}, Body: &Block{}},
		}},
	}
	m := &Module{Package: "main", Decls: []Decl{fn}}
	errs := Validate(m)
	if len(errs) == 0 {
		t.Fatal("expected validation error for ForStmt with both Var and Pattern")
	}
}

func TestValidateRejectsDuplicateGenericParam(t *testing.T) {
	fn := &FnDecl{
		Name:     "id",
		Generics: []*TypeParam{{Name: "T"}, {Name: "T"}},
		Return:   TInt,
		Body:     &Block{},
	}
	m := &Module{Package: "main", Decls: []Decl{fn}}
	errs := Validate(m)
	if len(errs) == 0 {
		t.Fatal("expected validation error for duplicate generic params")
	}
}

func TestValidateRejectsNilCallTypeArg(t *testing.T) {
	e := &CallExpr{
		Callee:   &Ident{Name: "id", Kind: IdentFn, T: &FnType{Params: []Type{TInt}, Return: TInt}},
		TypeArgs: []Type{nil},
		Args:     []Arg{{Value: intLit("1")}},
		T:        TInt,
	}
	m := moduleWithExpr(e)
	errs := Validate(m)
	if len(errs) == 0 {
		t.Fatal("expected validation error for nil call TypeArg")
	}
}

func TestValidateRejectsInvalidMatchTree(t *testing.T) {
	e := &MatchExpr{
		Scrutinee: intLit("1"),
		Arms: []*MatchArm{
			{Pattern: &WildPat{}, Body: &Block{Result: intLit("1")}},
		},
		Tree: &DecisionLeaf{ArmIndex: 99},
		T:    TInt,
	}
	m := moduleWithExpr(e)
	errs := Validate(m)
	if len(errs) == 0 {
		t.Fatal("expected validation error for invalid match tree")
	}
}

// ==== Helpers ====

func moduleWithExpr(e Expr) *Module {
	return &Module{
		Package: "main",
		Script:  []Stmt{&ExprStmt{X: e}},
	}
}

func intLit(text string) *IntLit  { return &IntLit{Text: text, T: TInt} }
func strLit(s string) *StringLit  { return &StringLit{Parts: []StringPart{{IsLit: true, Lit: s}}} }
