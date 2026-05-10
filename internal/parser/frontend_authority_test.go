package parser

import (
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/selfhost"
)

// --- Baseline authority tests ---

func TestParseDetailedKeepsFrontendRunAndUsesExplicitPublicCompatibility(t *testing.T) {
	src := []byte(`fn main() {
    let items = [1]
    let count = len(items)
}
`)

	selfhost.ResetAstbridgeLowerCount()
	result := ParseDetailed(src)
	if len(result.Diagnostics) > 0 {
		t.Fatalf("ParseDetailed diagnostics = %#v, want none", result.Diagnostics)
	}
	if result.Run == nil {
		t.Fatal("ParseDetailed Run = nil, want retained frontend run")
	}
	if result.File == nil {
		t.Fatal("ParseDetailed File = nil, want public compatibility AST")
	}
	if got := selfhost.AstbridgeLowerCount(); got != 0 {
		t.Fatalf("ParseDetailed astbridge count = %d, want 0", got)
	}
}

func TestParseMatchArmAllowsBareAssignmentBody(t *testing.T) {
	src := []byte(`struct Cookie { path: String }

fn update(value: String?) {
    let mut out = Cookie { path: "" }
    match value {
        Some(v) -> out.path = v,
        None -> {},
    }
}
`)

	result := ParseDetailed(src)
	if len(result.Diagnostics) > 0 {
		t.Fatalf("ParseDetailed diagnostics = %#v, want none", result.Diagnostics)
	}
	if result.File == nil || len(result.File.Decls) < 2 {
		t.Fatalf("parsed file = %#v, want struct and function declarations", result.File)
	}
	fn, ok := result.File.Decls[1].(*ast.FnDecl)
	if !ok || fn.Body == nil || len(fn.Body.Stmts) < 2 {
		t.Fatalf("decl[1] = %#v, want function with match statement", result.File.Decls[1])
	}
	stmt, ok := fn.Body.Stmts[1].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("stmt[1] = %#v, want expression statement", fn.Body.Stmts[1])
	}
	match, ok := stmt.X.(*ast.MatchExpr)
	if !ok || len(match.Arms) == 0 {
		t.Fatalf("stmt[1].X = %#v, want match expression with arms", stmt.X)
	}
	body, ok := match.Arms[0].Body.(*ast.Block)
	if !ok || len(body.Stmts) != 1 {
		t.Fatalf("arm body = %#v, want single-statement block", match.Arms[0].Body)
	}
	assign, ok := body.Stmts[0].(*ast.AssignStmt)
	if !ok || len(assign.Targets) != 1 {
		t.Fatalf("arm body stmt = %#v, want assignment statement", body.Stmts[0])
	}
	target, ok := assign.Targets[0].(*ast.FieldExpr)
	if !ok || target.Name != "path" {
		t.Fatalf("assignment target = %#v, want .path field assignment", assign.Targets[0])
	}
}

// --- Follow-on recovery parity tests ---
// Keep these tests in the same axis/ordinal order as the follow-on
// recovery diagnostics matrix in `testdata/spec/negative/reject.osty`.

// Axis A — newline-else fallout.

func TestParseFollowOnRecoveryA1ElseNewlinePrimary(t *testing.T) {
	src := []byte(`fn rfA1ElseNewlinePrimary() -> Int {
    if rfA1Cond() {
        1
    }
    else {
        2
    }
}
fn rfA1Cond() -> Bool { false }
`)

	result := ParseDetailed(src)
	if len(result.Diagnostics) != 3 {
		t.Fatalf("ParseDetailed diagnostics = %#v, want three follow-on diagnostics", result.Diagnostics)
	}
	want := map[string]bool{"E0105": false, "E0204": false, "E0100": false}
	for _, d := range result.Diagnostics {
		if _, ok := want[d.Code]; ok {
			want[d.Code] = true
		}
	}
	for code, seen := range want {
		if !seen {
			t.Fatalf("diagnostics = %#v, want code %s", result.Diagnostics, code)
		}
	}
	if result.File == nil || len(result.File.Decls) != 2 {
		t.Fatalf("parsed file = %#v, want primary fn plus preserved helper fn", result.File)
	}
	fn, ok := result.File.Decls[0].(*ast.FnDecl)
	if !ok || fn.Body == nil || len(fn.Body.Stmts) != 1 {
		t.Fatalf("decl[0] = %#v, want primary fn with one recovered if stmt", result.File.Decls[0])
	}
	stmt, ok := fn.Body.Stmts[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("stmt[0] = %#v, want expression statement", fn.Body.Stmts[0])
	}
	ifExpr, ok := stmt.X.(*ast.IfExpr)
	if !ok || ifExpr.Else != nil {
		t.Fatalf("stmt[0].X = %#v, want if expr with nil else after follow-on recovery", stmt.X)
	}
	helper, ok := result.File.Decls[1].(*ast.FnDecl)
	if !ok || helper.Name != "rfA1Cond" {
		t.Fatalf("decl[1] = %#v, want preserved helper fn rfA1Cond", result.File.Decls[1])
	}
}

// Axis B — declaration-layer fallout after expression recovery.

func TestParseFollowOnRecoveryB1MatchAssignTopLevelDecl(t *testing.T) {
	src := []byte(`fn rfB1MatchAssignTopLevelDecl(x: Int) {
    match x {
        0 -> out.path =
        _ -> 1,
    }
    let z = 2
}
`)

	result := ParseDetailed(src)
	if len(result.Diagnostics) != 5 {
		t.Fatalf("ParseDetailed diagnostics = %#v, want five follow-on diagnostics", result.Diagnostics)
	}
	hasE0100 := false
	hasArmRecovery := false
	for _, d := range result.Diagnostics {
		if d.Code == "E0100" {
			hasE0100 = true
		}
		if d.Code == "E0204" && d.Message == "expected match arm body before `->`" {
			hasArmRecovery = true
		}
	}
	if !hasE0100 || !hasArmRecovery {
		t.Fatalf("diagnostics = %#v, want E0100 plus match-arm follow-on recovery", result.Diagnostics)
	}
	if result.File == nil || len(result.File.Decls) != 1 {
		t.Fatalf("parsed file = %#v, want one damaged function decl", result.File)
	}
	fn, ok := result.File.Decls[0].(*ast.FnDecl)
	if !ok || fn.Body == nil || len(fn.Body.Stmts) != 1 {
		t.Fatalf("decl[0] = %#v, want one recovered match stmt in fn body", result.File.Decls[0])
	}
	stmt, ok := fn.Body.Stmts[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("stmt[0] = %#v, want expression statement", fn.Body.Stmts[0])
	}
	match, ok := stmt.X.(*ast.MatchExpr)
	if !ok || len(match.Arms) != 2 {
		t.Fatalf("stmt[0].X = %#v, want recovered match with two damaged arms", stmt.X)
	}
	if match.Arms[0].Body != nil || match.Arms[1].Body != nil {
		t.Fatalf("match arms = %#v, want both arm bodies nil after declaration-layer drift", match.Arms)
	}
	if len(result.File.Stmts) != 1 {
		t.Fatalf("top-level stmts = %#v, want trailing let to drift to file scope", result.File.Stmts)
	}
	topLet, ok := result.File.Stmts[0].(*ast.LetStmt)
	if !ok {
		t.Fatalf("file stmt[0] = %#v, want top-level let z after fallout", result.File.Stmts[0])
	}
	pat, ok := topLet.Pattern.(*ast.IdentPat)
	if !ok || pat.Name != "z" {
		t.Fatalf("file stmt[0] pattern = %#v, want let z = 2", topLet.Pattern)
	}
}

// --- Recovery matrix parity tests ---
// Axis parity rule with `testdata/spec/negative/reject.osty`:
//   - axis letters and their order must match the corpus sections
//   - these tests are a focused subset of the denser corpus matrix, so
//     missing ordinals here are allowed when the corpus already covers
//     the shape well enough
//   - when a test does mirror a corpus case, keep the same
//     `<Axis><Ordinal>` label in the test name
//   - add new axes to `reject.osty` first, then mirror the section
//     header order here
//
// Axis A — branch / arm continuity.

func TestParseRecoveryA1IfThenPreservesElseAndTrailingStmt(t *testing.T) {
	src := []byte(`fn cond() -> Bool { false }

fn f() {
    if cond() {
        let x =
    } else {
        let y = 1
    }
    let z = 2
}
`)

	result := ParseDetailed(src)
	if len(result.Diagnostics) != 1 {
		t.Fatalf("ParseDetailed diagnostics = %#v, want exactly one recovery diagnostic", result.Diagnostics)
	}
	if got := result.Diagnostics[0].Code; got != "E0204" {
		t.Fatalf("first diagnostic code = %q, want E0204", got)
	}
	if result.File == nil || len(result.File.Decls) < 2 {
		t.Fatalf("parsed file = %#v, want cond and f declarations", result.File)
	}
	fn, ok := result.File.Decls[1].(*ast.FnDecl)
	if !ok || fn.Body == nil || len(fn.Body.Stmts) != 2 {
		t.Fatalf("decl[1] = %#v, want function with if expr and trailing let", result.File.Decls[1])
	}
	stmt, ok := fn.Body.Stmts[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("stmt[0] = %#v, want expression statement", fn.Body.Stmts[0])
	}
	ifExpr, ok := stmt.X.(*ast.IfExpr)
	if !ok {
		t.Fatalf("stmt[0].X = %#v, want if expression", stmt.X)
	}
	if ifExpr.Else == nil {
		t.Fatalf("ifExpr.Else = nil, want else block preserved after recovery")
	}
	elseBlock, ok := ifExpr.Else.(*ast.Block)
	if !ok || len(elseBlock.Stmts) != 1 {
		t.Fatalf("ifExpr.Else = %#v, want one-statement else block", ifExpr.Else)
	}
	letStmt, ok := fn.Body.Stmts[1].(*ast.LetStmt)
	if !ok {
		t.Fatalf("stmt[1] = %#v, want trailing let statement", fn.Body.Stmts[1])
	}
	pat, ok := letStmt.Pattern.(*ast.IdentPat)
	if !ok || pat.Name != "z" {
		t.Fatalf("stmt[1] pattern = %#v, want let z = 2", letStmt.Pattern)
	}
}

func TestParseRecoveryA2MatchArmPreservesNextArmAndTrailingStmt(t *testing.T) {
	src := []byte(`fn f(x: Int) {
    match x {
        0 ->
        _ -> 1,
    }
    let z = 2
}
`)

	result := ParseDetailed(src)
	if len(result.Diagnostics) != 1 {
		t.Fatalf("ParseDetailed diagnostics = %#v, want exactly one recovery diagnostic", result.Diagnostics)
	}
	if got := result.Diagnostics[0].Code; got != "E0204" {
		t.Fatalf("first diagnostic code = %q, want E0204", got)
	}
	if result.File == nil || len(result.File.Decls) != 1 {
		t.Fatalf("parsed file = %#v, want single function declaration", result.File)
	}
	fn, ok := result.File.Decls[0].(*ast.FnDecl)
	if !ok || fn.Body == nil || len(fn.Body.Stmts) != 2 {
		t.Fatalf("decl[0] = %#v, want function with match expr and trailing let", result.File.Decls[0])
	}
	stmt, ok := fn.Body.Stmts[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("stmt[0] = %#v, want expression statement", fn.Body.Stmts[0])
	}
	match, ok := stmt.X.(*ast.MatchExpr)
	if !ok || len(match.Arms) != 2 {
		t.Fatalf("stmt[0].X = %#v, want match expression with two arms", stmt.X)
	}
	if match.Arms[0].Body != nil {
		t.Fatalf("arm[0].Body = %#v, want nil body placeholder after recovery", match.Arms[0].Body)
	}
	if lit, ok := match.Arms[1].Body.(*ast.IntLit); !ok || lit.Text != "1" {
		t.Fatalf("arm[1].Body = %#v, want preserved second arm body `1`", match.Arms[1].Body)
	}
	letStmt, ok := fn.Body.Stmts[1].(*ast.LetStmt)
	if !ok {
		t.Fatalf("stmt[1] = %#v, want trailing let statement", fn.Body.Stmts[1])
	}
	pat, ok := letStmt.Pattern.(*ast.IdentPat)
	if !ok || pat.Name != "z" {
		t.Fatalf("stmt[1] pattern = %#v, want let z = 2", letStmt.Pattern)
	}
}

// Axis B — required-rhs / next-statement boundary.

func TestParseRecoveryB1LetMissingInitPreservesFollowingStmt(t *testing.T) {
	src := []byte(`fn f() {
    let x =
    let z = 2
}
`)

	result := ParseDetailed(src)
	if len(result.Diagnostics) != 1 {
		t.Fatalf("ParseDetailed diagnostics = %#v, want exactly one recovery diagnostic", result.Diagnostics)
	}
	if got := result.Diagnostics[0].Code; got != "E0204" {
		t.Fatalf("first diagnostic code = %q, want E0204", got)
	}
	fn, ok := result.File.Decls[0].(*ast.FnDecl)
	if !ok || fn.Body == nil || len(fn.Body.Stmts) != 2 {
		t.Fatalf("decl[0] = %#v, want two let statements", result.File.Decls[0])
	}
	first, ok := fn.Body.Stmts[0].(*ast.LetStmt)
	if !ok || first.Value != nil {
		t.Fatalf("stmt[0] = %#v, want let with nil value placeholder", fn.Body.Stmts[0])
	}
	second, ok := fn.Body.Stmts[1].(*ast.LetStmt)
	if !ok {
		t.Fatalf("stmt[1] = %#v, want trailing let statement", fn.Body.Stmts[1])
	}
	pat, ok := second.Pattern.(*ast.IdentPat)
	if !ok || pat.Name != "z" {
		t.Fatalf("stmt[1] pattern = %#v, want let z = 2", second.Pattern)
	}
}

func TestParseRecoveryB3AssignMissingRhsPreservesFollowingStmt(t *testing.T) {
	src := []byte(`fn f() {
    x =
    let z = 2
}
`)

	result := ParseDetailed(src)
	if len(result.Diagnostics) != 1 {
		t.Fatalf("ParseDetailed diagnostics = %#v, want exactly one recovery diagnostic", result.Diagnostics)
	}
	if got := result.Diagnostics[0].Code; got != "E0204" {
		t.Fatalf("first diagnostic code = %q, want E0204", got)
	}
	fn, ok := result.File.Decls[0].(*ast.FnDecl)
	if !ok || fn.Body == nil || len(fn.Body.Stmts) != 2 {
		t.Fatalf("decl[0] = %#v, want assignment plus trailing let", result.File.Decls[0])
	}
	assign, ok := fn.Body.Stmts[0].(*ast.AssignStmt)
	if !ok || assign.Value != nil {
		t.Fatalf("stmt[0] = %#v, want assignment with nil rhs placeholder", fn.Body.Stmts[0])
	}
	second, ok := fn.Body.Stmts[1].(*ast.LetStmt)
	if !ok {
		t.Fatalf("stmt[1] = %#v, want trailing let statement", fn.Body.Stmts[1])
	}
	pat, ok := second.Pattern.(*ast.IdentPat)
	if !ok || pat.Name != "z" {
		t.Fatalf("stmt[1] pattern = %#v, want let z = 2", second.Pattern)
	}
}

func TestParseRecoveryB6ReturnMalformedExprPreservesFollowingStmt(t *testing.T) {
	src := []byte(`fn f() {
    return +
    let z = 2
}
`)

	result := ParseDetailed(src)
	if len(result.Diagnostics) != 1 {
		t.Fatalf("ParseDetailed diagnostics = %#v, want exactly one recovery diagnostic", result.Diagnostics)
	}
	if got := result.Diagnostics[0].Code; got != "E0204" {
		t.Fatalf("first diagnostic code = %q, want E0204", got)
	}
	fn, ok := result.File.Decls[0].(*ast.FnDecl)
	if !ok || fn.Body == nil || len(fn.Body.Stmts) != 2 {
		t.Fatalf("decl[0] = %#v, want return plus trailing let", result.File.Decls[0])
	}
	ret, ok := fn.Body.Stmts[0].(*ast.ReturnStmt)
	if !ok || ret.Value != nil {
		t.Fatalf("stmt[0] = %#v, want return with nil value after recovery", fn.Body.Stmts[0])
	}
	second, ok := fn.Body.Stmts[1].(*ast.LetStmt)
	if !ok {
		t.Fatalf("stmt[1] = %#v, want trailing let statement", fn.Body.Stmts[1])
	}
	pat, ok := second.Pattern.(*ast.IdentPat)
	if !ok || pat.Name != "z" {
		t.Fatalf("stmt[1] pattern = %#v, want let z = 2", second.Pattern)
	}
}

func TestParseRecoveryB7ChanSendMissingRhsPreservesFollowingStmt(t *testing.T) {
	src := []byte(`fn f() {
    ch <-
    let z = 2
}
`)

	result := ParseDetailed(src)
	if len(result.Diagnostics) != 1 {
		t.Fatalf("ParseDetailed diagnostics = %#v, want exactly one recovery diagnostic", result.Diagnostics)
	}
	if got := result.Diagnostics[0].Code; got != "E0204" {
		t.Fatalf("first diagnostic code = %q, want E0204", got)
	}
	fn, ok := result.File.Decls[0].(*ast.FnDecl)
	if !ok || fn.Body == nil || len(fn.Body.Stmts) != 2 {
		t.Fatalf("decl[0] = %#v, want chan send plus trailing let", result.File.Decls[0])
	}
	send, ok := fn.Body.Stmts[0].(*ast.ChanSendStmt)
	if !ok || send.Value != nil {
		t.Fatalf("stmt[0] = %#v, want chan send with nil rhs placeholder", fn.Body.Stmts[0])
	}
	second, ok := fn.Body.Stmts[1].(*ast.LetStmt)
	if !ok {
		t.Fatalf("stmt[1] = %#v, want trailing let statement", fn.Body.Stmts[1])
	}
	pat, ok := second.Pattern.(*ast.IdentPat)
	if !ok || pat.Name != "z" {
		t.Fatalf("stmt[1] pattern = %#v, want let z = 2", second.Pattern)
	}
}

func TestParseRecoveryB9DeferMissingExprPreservesFollowingStmt(t *testing.T) {
	src := []byte(`fn f() {
    defer
    let z = 2
}
`)

	result := ParseDetailed(src)
	if len(result.Diagnostics) != 1 {
		t.Fatalf("ParseDetailed diagnostics = %#v, want exactly one recovery diagnostic", result.Diagnostics)
	}
	if got := result.Diagnostics[0].Code; got != "E0204" {
		t.Fatalf("first diagnostic code = %q, want E0204", got)
	}
	fn, ok := result.File.Decls[0].(*ast.FnDecl)
	if !ok || fn.Body == nil || len(fn.Body.Stmts) != 2 {
		t.Fatalf("decl[0] = %#v, want defer plus trailing let", result.File.Decls[0])
	}
	deferStmt, ok := fn.Body.Stmts[0].(*ast.DeferStmt)
	if !ok || deferStmt.X != nil {
		t.Fatalf("stmt[0] = %#v, want defer with nil expr after recovery", fn.Body.Stmts[0])
	}
	second, ok := fn.Body.Stmts[1].(*ast.LetStmt)
	if !ok {
		t.Fatalf("stmt[1] = %#v, want trailing let statement", fn.Body.Stmts[1])
	}
	pat, ok := second.Pattern.(*ast.IdentPat)
	if !ok || pat.Name != "z" {
		t.Fatalf("stmt[1] pattern = %#v, want let z = 2", second.Pattern)
	}
}

// Axis C — control-flow tail recovery.

func TestParseRecoveryC1BreakMalformedValuePreservesFollowingStmt(t *testing.T) {
	src := []byte(`fn f() {
    for i in 0..10 {
        break +
        let z = 2
    }
}
`)

	result := ParseDetailed(src)
	if len(result.Diagnostics) != 1 {
		t.Fatalf("ParseDetailed diagnostics = %#v, want exactly one recovery diagnostic", result.Diagnostics)
	}
	if got := result.Diagnostics[0].Code; got != "E0204" {
		t.Fatalf("first diagnostic code = %q, want E0204", got)
	}
	fn, ok := result.File.Decls[0].(*ast.FnDecl)
	if !ok || fn.Body == nil || len(fn.Body.Stmts) != 1 {
		t.Fatalf("decl[0] = %#v, want single for statement", result.File.Decls[0])
	}
	forStmt, ok := fn.Body.Stmts[0].(*ast.ForStmt)
	if !ok || forStmt.Body == nil || len(forStmt.Body.Stmts) != 2 {
		t.Fatalf("stmt[0] = %#v, want for body with break plus trailing let", fn.Body.Stmts[0])
	}
	brk, ok := forStmt.Body.Stmts[0].(*ast.BreakStmt)
	if !ok || brk.Value != nil {
		t.Fatalf("for body stmt[0] = %#v, want break with nil value after recovery", forStmt.Body.Stmts[0])
	}
	second, ok := forStmt.Body.Stmts[1].(*ast.LetStmt)
	if !ok {
		t.Fatalf("for body stmt[1] = %#v, want trailing let statement", forStmt.Body.Stmts[1])
	}
	pat, ok := second.Pattern.(*ast.IdentPat)
	if !ok || pat.Name != "z" {
		t.Fatalf("for body stmt[1] pattern = %#v, want let z = 2", second.Pattern)
	}
}

func TestParseRecoveryC3ContinueMalformedSuffixPreservesFollowingStmt(t *testing.T) {
	src := []byte(`fn f() {
    for i in 0..10 {
        continue +
        let z = 2
    }
}
`)

	result := ParseDetailed(src)
	if len(result.Diagnostics) != 1 {
		t.Fatalf("ParseDetailed diagnostics = %#v, want exactly one recovery diagnostic", result.Diagnostics)
	}
	if got := result.Diagnostics[0].Code; got != "E0204" {
		t.Fatalf("first diagnostic code = %q, want E0204", got)
	}
	fn, ok := result.File.Decls[0].(*ast.FnDecl)
	if !ok || fn.Body == nil || len(fn.Body.Stmts) != 1 {
		t.Fatalf("decl[0] = %#v, want single for statement", result.File.Decls[0])
	}
	forStmt, ok := fn.Body.Stmts[0].(*ast.ForStmt)
	if !ok || forStmt.Body == nil || len(forStmt.Body.Stmts) != 2 {
		t.Fatalf("stmt[0] = %#v, want for body with continue plus trailing let", fn.Body.Stmts[0])
	}
	cont, ok := forStmt.Body.Stmts[0].(*ast.ContinueStmt)
	if !ok || cont.Label != "" {
		t.Fatalf("for body stmt[0] = %#v, want unlabeled continue", forStmt.Body.Stmts[0])
	}
	second, ok := forStmt.Body.Stmts[1].(*ast.LetStmt)
	if !ok {
		t.Fatalf("for body stmt[1] = %#v, want trailing let statement", forStmt.Body.Stmts[1])
	}
	pat, ok := second.Pattern.(*ast.IdentPat)
	if !ok || pat.Name != "z" {
		t.Fatalf("for body stmt[1] pattern = %#v, want let z = 2", second.Pattern)
	}
}

func TestParseRecoveryC4LabeledContinueMalformedSuffixPreservesFollowingStmt(t *testing.T) {
	src := []byte("fn f() {\n    'outer: for i in 0..10 {\n        continue 'outer +\n        let z = 2\n    }\n}\n")

	result := ParseDetailed(src)
	if len(result.Diagnostics) != 1 {
		t.Fatalf("ParseDetailed diagnostics = %#v, want exactly one recovery diagnostic", result.Diagnostics)
	}
	if got := result.Diagnostics[0].Code; got != "E0204" {
		t.Fatalf("first diagnostic code = %q, want E0204", got)
	}
	fn, ok := result.File.Decls[0].(*ast.FnDecl)
	if !ok || fn.Body == nil || len(fn.Body.Stmts) != 1 {
		t.Fatalf("decl[0] = %#v, want single labeled for statement", result.File.Decls[0])
	}
	forStmt, ok := fn.Body.Stmts[0].(*ast.ForStmt)
	if !ok || forStmt.Body == nil || len(forStmt.Body.Stmts) != 2 {
		t.Fatalf("stmt[0] = %#v, want for body with continue plus trailing let", fn.Body.Stmts[0])
	}
	cont, ok := forStmt.Body.Stmts[0].(*ast.ContinueStmt)
	if !ok || cont.Label != "outer" {
		t.Fatalf("for body stmt[0] = %#v, want labeled continue 'outer", forStmt.Body.Stmts[0])
	}
	second, ok := forStmt.Body.Stmts[1].(*ast.LetStmt)
	if !ok {
		t.Fatalf("for body stmt[1] = %#v, want trailing let statement", forStmt.Body.Stmts[1])
	}
	pat, ok := second.Pattern.(*ast.IdentPat)
	if !ok || pat.Name != "z" {
		t.Fatalf("for body stmt[1] pattern = %#v, want let z = 2", second.Pattern)
	}
}

// Axis D — scoped / nested body recovery.

func TestParseRecoveryD1DeferBlockMalformedBodyPreservesFollowingStmt(t *testing.T) {
	src := []byte(`fn f() {
    defer {
        let x =
    }
    let z = 2
}
`)

	result := ParseDetailed(src)
	if len(result.Diagnostics) != 1 {
		t.Fatalf("ParseDetailed diagnostics = %#v, want exactly one recovery diagnostic", result.Diagnostics)
	}
	if got := result.Diagnostics[0].Code; got != "E0204" {
		t.Fatalf("first diagnostic code = %q, want E0204", got)
	}
	fn, ok := result.File.Decls[0].(*ast.FnDecl)
	if !ok || fn.Body == nil || len(fn.Body.Stmts) != 2 {
		t.Fatalf("decl[0] = %#v, want defer plus trailing let", result.File.Decls[0])
	}
	deferStmt, ok := fn.Body.Stmts[0].(*ast.DeferStmt)
	if !ok {
		t.Fatalf("stmt[0] = %#v, want defer statement", fn.Body.Stmts[0])
	}
	block, ok := deferStmt.X.(*ast.Block)
	if !ok || len(block.Stmts) != 1 {
		t.Fatalf("deferStmt.X = %#v, want deferred block with one stmt", deferStmt.X)
	}
	inner, ok := block.Stmts[0].(*ast.LetStmt)
	if !ok || inner.Value != nil {
		t.Fatalf("defer block stmt[0] = %#v, want let with nil initializer", block.Stmts[0])
	}
	second, ok := fn.Body.Stmts[1].(*ast.LetStmt)
	if !ok {
		t.Fatalf("stmt[1] = %#v, want trailing let statement", fn.Body.Stmts[1])
	}
	pat, ok := second.Pattern.(*ast.IdentPat)
	if !ok || pat.Name != "z" {
		t.Fatalf("stmt[1] pattern = %#v, want let z = 2", second.Pattern)
	}
}

// Axis E — conditional pattern recovery.

func TestParseRecoveryE1IfLetMalformedScrutineePreservesElseAndFollowingStmt(t *testing.T) {
	src := []byte(`fn f() {
    if let value = + {
        1
    } else {
        2
    }
    let z = 2
}
`)

	result := ParseDetailed(src)
	if len(result.Diagnostics) != 1 {
		t.Fatalf("ParseDetailed diagnostics = %#v, want exactly one recovery diagnostic", result.Diagnostics)
	}
	if got := result.Diagnostics[0].Code; got != "E0204" {
		t.Fatalf("first diagnostic code = %q, want E0204", got)
	}
	fn, ok := result.File.Decls[0].(*ast.FnDecl)
	if !ok || fn.Body == nil || len(fn.Body.Stmts) != 2 {
		t.Fatalf("decl[0] = %#v, want if-let plus trailing let", result.File.Decls[0])
	}
	stmt, ok := fn.Body.Stmts[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("stmt[0] = %#v, want expression statement", fn.Body.Stmts[0])
	}
	ifExpr, ok := stmt.X.(*ast.IfExpr)
	if !ok || !ifExpr.IsIfLet || ifExpr.Cond != nil {
		t.Fatalf("stmt[0].X = %#v, want if-let with nil recovered condition", stmt.X)
	}
	elseBlock, ok := ifExpr.Else.(*ast.Block)
	if !ok || len(elseBlock.Stmts) != 1 {
		t.Fatalf("ifExpr.Else = %#v, want preserved else block", ifExpr.Else)
	}
	second, ok := fn.Body.Stmts[1].(*ast.LetStmt)
	if !ok {
		t.Fatalf("stmt[1] = %#v, want trailing let statement", fn.Body.Stmts[1])
	}
	pat, ok := second.Pattern.(*ast.IdentPat)
	if !ok || pat.Name != "z" {
		t.Fatalf("stmt[1] pattern = %#v, want let z = 2", second.Pattern)
	}
}
