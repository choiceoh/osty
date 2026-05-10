package parser

import (
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/selfhost"
)

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

func TestParseBlockRecoveryResetsSyncThresholdPerStatement(t *testing.T) {
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

func TestParseMatchRecoveryPreservesNextArmAndTrailingStmt(t *testing.T) {
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

func TestParseLetMissingRhsPreservesFollowingStmt(t *testing.T) {
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

func TestParseAssignMissingRhsPreservesFollowingStmt(t *testing.T) {
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

func TestParseReturnMalformedExprPreservesFollowingStmt(t *testing.T) {
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
