package parser

import (
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/selfhost"
)

func requireNoParseDiagnostics(t *testing.T, result Result) {
	t.Helper()
	if len(result.Diagnostics) > 0 {
		t.Fatalf("ParseDetailed diagnostics = %#v, want none", result.Diagnostics)
	}
}

func requireSingleParseDiagnosticCode(t *testing.T, result Result, want string) {
	t.Helper()
	if len(result.Diagnostics) != 1 {
		t.Fatalf("ParseDetailed diagnostics = %#v, want exactly one recovery diagnostic", result.Diagnostics)
	}
	if got := result.Diagnostics[0].Code; got != want {
		t.Fatalf("first diagnostic code = %q, want %s", got, want)
	}
}

func requireParseDiagnosticCount(t *testing.T, result Result, want int, context string) {
	t.Helper()
	if len(result.Diagnostics) != want {
		t.Fatalf("ParseDetailed diagnostics = %#v, want %d %s", result.Diagnostics, want, context)
	}
}

func requireParseDiagnosticCodes(t *testing.T, result Result, want ...string) {
	t.Helper()
	if len(result.Diagnostics) != len(want) {
		t.Fatalf("ParseDetailed diagnostics = %#v, want exactly %d diagnostics", result.Diagnostics, len(want))
	}
	seen := make(map[string]int, len(result.Diagnostics))
	for _, d := range result.Diagnostics {
		seen[d.Code]++
	}
	for _, code := range want {
		if seen[code] == 0 {
			t.Fatalf("diagnostics = %#v, want code %s", result.Diagnostics, code)
		}
		seen[code]--
	}
	for code, count := range seen {
		if count != 0 {
			t.Fatalf("diagnostics = %#v, got unexpected multiplicity for %s", result.Diagnostics, code)
		}
	}
}

func requireParseDiagnosticWithCodeAndMessage(t *testing.T, result Result, code string, message string) {
	t.Helper()
	for _, d := range result.Diagnostics {
		if d.Code == code && d.Message == message {
			return
		}
	}
	t.Fatalf("diagnostics = %#v, want %s with message %q", result.Diagnostics, code, message)
}

func requireParseDiagnosticCodePresent(t *testing.T, result Result, code string) {
	t.Helper()
	for _, d := range result.Diagnostics {
		if d.Code == code {
			return
		}
	}
	t.Fatalf("diagnostics = %#v, want code %s", result.Diagnostics, code)
}

func requireFnDeclAt(t *testing.T, file *ast.File, idx int, wantStmts int, context string) *ast.FnDecl {
	t.Helper()
	if file == nil || idx < 0 || idx >= len(file.Decls) {
		t.Fatalf("parsed file = %#v, want %s", file, context)
	}
	fn, ok := file.Decls[idx].(*ast.FnDecl)
	if !ok || fn.Body == nil || len(fn.Body.Stmts) != wantStmts {
		t.Fatalf("decl[%d] = %#v, want %s", idx, file.Decls[idx], context)
	}
	return fn
}

func requireExprStmtAt(t *testing.T, stmts []ast.Stmt, idx int, context string) *ast.ExprStmt {
	t.Helper()
	if idx < 0 || idx >= len(stmts) {
		t.Fatalf("stmts = %#v, want %s", stmts, context)
	}
	stmt, ok := stmts[idx].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("stmt[%d] = %#v, want %s", idx, stmts[idx], context)
	}
	return stmt
}

func requireLetStmtAt(t *testing.T, stmts []ast.Stmt, idx int, context string) *ast.LetStmt {
	t.Helper()
	if idx < 0 || idx >= len(stmts) {
		t.Fatalf("stmts = %#v, want %s", stmts, context)
	}
	stmt, ok := stmts[idx].(*ast.LetStmt)
	if !ok {
		t.Fatalf("stmt[%d] = %#v, want %s", idx, stmts[idx], context)
	}
	return stmt
}

func requireForStmtAt(t *testing.T, stmts []ast.Stmt, idx int, context string) *ast.ForStmt {
	t.Helper()
	if idx < 0 || idx >= len(stmts) {
		t.Fatalf("stmts = %#v, want %s", stmts, context)
	}
	stmt, ok := stmts[idx].(*ast.ForStmt)
	if !ok || stmt.Body == nil {
		t.Fatalf("stmt[%d] = %#v, want %s", idx, stmts[idx], context)
	}
	return stmt
}

func requireBreakStmtAt(t *testing.T, stmts []ast.Stmt, idx int, context string) *ast.BreakStmt {
	t.Helper()
	if idx < 0 || idx >= len(stmts) {
		t.Fatalf("stmts = %#v, want %s", stmts, context)
	}
	stmt, ok := stmts[idx].(*ast.BreakStmt)
	if !ok {
		t.Fatalf("stmt[%d] = %#v, want %s", idx, stmts[idx], context)
	}
	return stmt
}

func requireContinueStmtAt(t *testing.T, stmts []ast.Stmt, idx int, context string) *ast.ContinueStmt {
	t.Helper()
	if idx < 0 || idx >= len(stmts) {
		t.Fatalf("stmts = %#v, want %s", stmts, context)
	}
	stmt, ok := stmts[idx].(*ast.ContinueStmt)
	if !ok {
		t.Fatalf("stmt[%d] = %#v, want %s", idx, stmts[idx], context)
	}
	return stmt
}

func requireDeferStmtAt(t *testing.T, stmts []ast.Stmt, idx int, context string) *ast.DeferStmt {
	t.Helper()
	if idx < 0 || idx >= len(stmts) {
		t.Fatalf("stmts = %#v, want %s", stmts, context)
	}
	stmt, ok := stmts[idx].(*ast.DeferStmt)
	if !ok {
		t.Fatalf("stmt[%d] = %#v, want %s", idx, stmts[idx], context)
	}
	return stmt
}

func requireIdentPatName(t *testing.T, pat ast.Pattern, want string, context string) {
	t.Helper()
	ident, ok := pat.(*ast.IdentPat)
	if !ok || ident.Name != want {
		t.Fatalf("pattern = %#v, want %s", pat, context)
	}
}

const (
	fixtureTrailingLet4 = "    let z = 2\n"
	fixtureTrailingLet8 = "        let z = 2\n"
)

func fixtureFn(body string) []byte {
	return []byte("fn f() {\n" + body + "}\n")
}

func fixtureFnWithTrailingLet(body string) []byte {
	return fixtureFn(body + fixtureTrailingLet4)
}

func fixtureLoopFnWithTrailingLet(body string) []byte {
	return fixtureFn("    for i in 0..10 {\n" + body + fixtureTrailingLet8 + "    }\n")
}

func fixtureLabeledLoopFnWithTrailingLet(label string, body string) []byte {
	return fixtureFn("    '" + label + ": for i in 0..10 {\n" + body + fixtureTrailingLet8 + "    }\n")
}

func fixtureFollowOnElseNewline(fnName string, condName string) []byte {
	return []byte("fn " + fnName + "() -> Int {\n" +
		"    if " + condName + "() {\n" +
		"        1\n" +
		"    }\n" +
		"    else {\n" +
		"        2\n" +
		"    }\n" +
		"}\n" +
		"fn " + condName + "() -> Bool { false }\n")
}

func fixtureMatchAssignTopLevelDecl(fnName string) []byte {
	return []byte("fn " + fnName + "(x: Int) {\n" +
		"    match x {\n" +
		"        0 -> out.path =\n" +
		"        _ -> 1,\n" +
		"    }\n" +
		fixtureTrailingLet4 +
		"}\n")
}

func fixtureIfThenMissingRhs(condName string) []byte {
	return []byte("fn " + condName + "() -> Bool { false }\n\n" +
		"fn f() {\n" +
		"    if " + condName + "() {\n" +
		"        let x =\n" +
		"    } else {\n" +
		"        let y = 1\n" +
		"    }\n" +
		fixtureTrailingLet4 +
		"}\n")
}

func fixtureMatchArmMissingBody() []byte {
	return []byte("fn f(x: Int) {\n" +
		"    match x {\n" +
		"        0 ->\n" +
		"        _ -> 1,\n" +
		"    }\n" +
		fixtureTrailingLet4 +
		"}\n")
}

func fixtureDeferBlockMalformedBody() []byte {
	return []byte("fn f() {\n" +
		"    defer {\n" +
		"        let x =\n" +
		"    }\n" +
		fixtureTrailingLet4 +
		"}\n")
}

func fixtureIfLetMalformedScrutinee() []byte {
	return []byte("fn f() {\n" +
		"    if let value = + {\n" +
		"        1\n" +
		"    } else {\n" +
		"        2\n" +
		"    }\n" +
		fixtureTrailingLet4 +
		"}\n")
}

// --- Baseline authority tests ---

func TestParseDetailedKeepsFrontendRunAndUsesExplicitPublicCompatibility(t *testing.T) {
	src := []byte(`fn main() {
    let items = [1]
    let count = len(items)
}
`)

	selfhost.ResetAstbridgeLowerCount()
	result := ParseDetailed(src)
	requireNoParseDiagnostics(t, result)
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
	requireNoParseDiagnostics(t, result)
	fn := requireFnDeclAt(t, result.File, 1, 2, "struct and function declarations")
	stmt := requireExprStmtAt(t, fn.Body.Stmts, 1, "expression statement")
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
	src := fixtureFollowOnElseNewline("rfA1ElseNewlinePrimary", "rfA1Cond")

	result := ParseDetailed(src)
	requireParseDiagnosticCodes(t, result, "E0105", "E0204", "E0100")
	fn := requireFnDeclAt(t, result.File, 0, 1, "primary fn plus preserved helper fn")
	stmt := requireExprStmtAt(t, fn.Body.Stmts, 0, "expression statement")
	ifExpr, ok := stmt.X.(*ast.IfExpr)
	if !ok || ifExpr.Else != nil {
		t.Fatalf("stmt[0].X = %#v, want if expr with nil else after follow-on recovery", stmt.X)
	}
	helper := requireFnDeclAt(t, result.File, 1, 1, "preserved helper fn rfA1Cond")
	if helper.Name != "rfA1Cond" {
		t.Fatalf("decl[1] = %#v, want preserved helper fn rfA1Cond", result.File.Decls[1])
	}
}

// Axis B — declaration-layer fallout after expression recovery.

func TestParseFollowOnRecoveryB1MatchAssignTopLevelDecl(t *testing.T) {
	src := fixtureMatchAssignTopLevelDecl("rfB1MatchAssignTopLevelDecl")

	result := ParseDetailed(src)
	requireParseDiagnosticCount(t, result, 5, "follow-on diagnostics")
	requireParseDiagnosticCodePresent(t, result, "E0100")
	requireParseDiagnosticWithCodeAndMessage(t, result, "E0204", "expected match arm body before `->`")
	fn := requireFnDeclAt(t, result.File, 0, 1, "one damaged function decl")
	stmt := requireExprStmtAt(t, fn.Body.Stmts, 0, "expression statement")
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
	topLet := requireLetStmtAt(t, result.File.Stmts, 0, "top-level let z after fallout")
	requireIdentPatName(t, topLet.Pattern, "z", "let z = 2")
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
	src := fixtureIfThenMissingRhs("cond")

	result := ParseDetailed(src)
	requireSingleParseDiagnosticCode(t, result, "E0204")
	fn := requireFnDeclAt(t, result.File, 1, 2, "cond and f declarations")
	stmt := requireExprStmtAt(t, fn.Body.Stmts, 0, "expression statement")
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
	letStmt := requireLetStmtAt(t, fn.Body.Stmts, 1, "trailing let statement")
	requireIdentPatName(t, letStmt.Pattern, "z", "let z = 2")
}

func TestParseRecoveryA2MatchArmPreservesNextArmAndTrailingStmt(t *testing.T) {
	src := fixtureMatchArmMissingBody()

	result := ParseDetailed(src)
	requireSingleParseDiagnosticCode(t, result, "E0204")
	fn := requireFnDeclAt(t, result.File, 0, 2, "single function declaration")
	stmt := requireExprStmtAt(t, fn.Body.Stmts, 0, "expression statement")
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
	letStmt := requireLetStmtAt(t, fn.Body.Stmts, 1, "trailing let statement")
	requireIdentPatName(t, letStmt.Pattern, "z", "let z = 2")
}

// Axis B — required-rhs / next-statement boundary.

func TestParseRecoveryB1LetMissingInitPreservesFollowingStmt(t *testing.T) {
	src := fixtureFnWithTrailingLet("    let x =\n")

	result := ParseDetailed(src)
	requireSingleParseDiagnosticCode(t, result, "E0204")
	fn := requireFnDeclAt(t, result.File, 0, 2, "two let statements")
	first := requireLetStmtAt(t, fn.Body.Stmts, 0, "let with nil value placeholder")
	if first.Value != nil {
		t.Fatalf("stmt[0] = %#v, want let with nil value placeholder", fn.Body.Stmts[0])
	}
	second := requireLetStmtAt(t, fn.Body.Stmts, 1, "trailing let statement")
	requireIdentPatName(t, second.Pattern, "z", "let z = 2")
}

func TestParseRecoveryB3AssignMissingRhsPreservesFollowingStmt(t *testing.T) {
	src := fixtureFnWithTrailingLet("    x =\n")

	result := ParseDetailed(src)
	requireSingleParseDiagnosticCode(t, result, "E0204")
	fn := requireFnDeclAt(t, result.File, 0, 2, "assignment plus trailing let")
	assign, ok := fn.Body.Stmts[0].(*ast.AssignStmt)
	if !ok || assign.Value != nil {
		t.Fatalf("stmt[0] = %#v, want assignment with nil rhs placeholder", fn.Body.Stmts[0])
	}
	second := requireLetStmtAt(t, fn.Body.Stmts, 1, "trailing let statement")
	requireIdentPatName(t, second.Pattern, "z", "let z = 2")
}

func TestParseRecoveryB6ReturnMalformedExprPreservesFollowingStmt(t *testing.T) {
	src := fixtureFnWithTrailingLet("    return +\n")

	result := ParseDetailed(src)
	requireSingleParseDiagnosticCode(t, result, "E0204")
	fn := requireFnDeclAt(t, result.File, 0, 2, "return plus trailing let")
	ret, ok := fn.Body.Stmts[0].(*ast.ReturnStmt)
	if !ok || ret.Value != nil {
		t.Fatalf("stmt[0] = %#v, want return with nil value after recovery", fn.Body.Stmts[0])
	}
	second := requireLetStmtAt(t, fn.Body.Stmts, 1, "trailing let statement")
	requireIdentPatName(t, second.Pattern, "z", "let z = 2")
}

func TestParseRecoveryB7ChanSendMissingRhsPreservesFollowingStmt(t *testing.T) {
	src := fixtureFnWithTrailingLet("    ch <-\n")

	result := ParseDetailed(src)
	requireSingleParseDiagnosticCode(t, result, "E0204")
	fn := requireFnDeclAt(t, result.File, 0, 2, "chan send plus trailing let")
	send, ok := fn.Body.Stmts[0].(*ast.ChanSendStmt)
	if !ok || send.Value != nil {
		t.Fatalf("stmt[0] = %#v, want chan send with nil rhs placeholder", fn.Body.Stmts[0])
	}
	second := requireLetStmtAt(t, fn.Body.Stmts, 1, "trailing let statement")
	requireIdentPatName(t, second.Pattern, "z", "let z = 2")
}

func TestParseRecoveryB9DeferMissingExprPreservesFollowingStmt(t *testing.T) {
	src := fixtureFnWithTrailingLet("    defer\n")

	result := ParseDetailed(src)
	requireSingleParseDiagnosticCode(t, result, "E0204")
	fn := requireFnDeclAt(t, result.File, 0, 2, "defer plus trailing let")
	deferStmt := requireDeferStmtAt(t, fn.Body.Stmts, 0, "defer with nil expr after recovery")
	if deferStmt.X != nil {
		t.Fatalf("stmt[0] = %#v, want defer with nil expr after recovery", fn.Body.Stmts[0])
	}
	second := requireLetStmtAt(t, fn.Body.Stmts, 1, "trailing let statement")
	requireIdentPatName(t, second.Pattern, "z", "let z = 2")
}

// Axis C — control-flow tail recovery.

func TestParseRecoveryC1BreakMalformedValuePreservesFollowingStmt(t *testing.T) {
	src := fixtureLoopFnWithTrailingLet("        break +\n")

	result := ParseDetailed(src)
	requireSingleParseDiagnosticCode(t, result, "E0204")
	fn := requireFnDeclAt(t, result.File, 0, 1, "single for statement")
	forStmt := requireForStmtAt(t, fn.Body.Stmts, 0, "for body with break plus trailing let")
	if len(forStmt.Body.Stmts) != 2 {
		t.Fatalf("stmt[0] = %#v, want for body with break plus trailing let", fn.Body.Stmts[0])
	}
	brk := requireBreakStmtAt(t, forStmt.Body.Stmts, 0, "break with nil value after recovery")
	if brk.Value != nil {
		t.Fatalf("for body stmt[0] = %#v, want break with nil value after recovery", forStmt.Body.Stmts[0])
	}
	second := requireLetStmtAt(t, forStmt.Body.Stmts, 1, "trailing let statement")
	requireIdentPatName(t, second.Pattern, "z", "let z = 2")
}

func TestParseRecoveryC3ContinueMalformedSuffixPreservesFollowingStmt(t *testing.T) {
	src := fixtureLoopFnWithTrailingLet("        continue +\n")

	result := ParseDetailed(src)
	requireSingleParseDiagnosticCode(t, result, "E0204")
	fn := requireFnDeclAt(t, result.File, 0, 1, "single for statement")
	forStmt := requireForStmtAt(t, fn.Body.Stmts, 0, "for body with continue plus trailing let")
	if len(forStmt.Body.Stmts) != 2 {
		t.Fatalf("stmt[0] = %#v, want for body with continue plus trailing let", fn.Body.Stmts[0])
	}
	cont := requireContinueStmtAt(t, forStmt.Body.Stmts, 0, "unlabeled continue")
	if cont.Label != "" {
		t.Fatalf("for body stmt[0] = %#v, want unlabeled continue", forStmt.Body.Stmts[0])
	}
	second := requireLetStmtAt(t, forStmt.Body.Stmts, 1, "trailing let statement")
	requireIdentPatName(t, second.Pattern, "z", "let z = 2")
}

func TestParseRecoveryC4LabeledContinueMalformedSuffixPreservesFollowingStmt(t *testing.T) {
	src := fixtureLabeledLoopFnWithTrailingLet("outer", "        continue 'outer +\n")

	result := ParseDetailed(src)
	requireSingleParseDiagnosticCode(t, result, "E0204")
	fn := requireFnDeclAt(t, result.File, 0, 1, "single labeled for statement")
	forStmt := requireForStmtAt(t, fn.Body.Stmts, 0, "for body with continue plus trailing let")
	if len(forStmt.Body.Stmts) != 2 {
		t.Fatalf("stmt[0] = %#v, want for body with continue plus trailing let", fn.Body.Stmts[0])
	}
	cont := requireContinueStmtAt(t, forStmt.Body.Stmts, 0, "labeled continue 'outer")
	if cont.Label != "outer" {
		t.Fatalf("for body stmt[0] = %#v, want labeled continue 'outer", forStmt.Body.Stmts[0])
	}
	second := requireLetStmtAt(t, forStmt.Body.Stmts, 1, "trailing let statement")
	requireIdentPatName(t, second.Pattern, "z", "let z = 2")
}

// Axis D — scoped / nested body recovery.

func TestParseRecoveryD1DeferBlockMalformedBodyPreservesFollowingStmt(t *testing.T) {
	src := fixtureDeferBlockMalformedBody()

	result := ParseDetailed(src)
	requireSingleParseDiagnosticCode(t, result, "E0204")
	fn := requireFnDeclAt(t, result.File, 0, 2, "defer plus trailing let")
	deferStmt := requireDeferStmtAt(t, fn.Body.Stmts, 0, "defer statement")
	block, ok := deferStmt.X.(*ast.Block)
	if !ok || len(block.Stmts) != 1 {
		t.Fatalf("deferStmt.X = %#v, want deferred block with one stmt", deferStmt.X)
	}
	inner := requireLetStmtAt(t, block.Stmts, 0, "let with nil initializer")
	if inner.Value != nil {
		t.Fatalf("defer block stmt[0] = %#v, want let with nil initializer", block.Stmts[0])
	}
	second := requireLetStmtAt(t, fn.Body.Stmts, 1, "trailing let statement")
	requireIdentPatName(t, second.Pattern, "z", "let z = 2")
}

// Axis E — conditional pattern recovery.

func TestParseRecoveryE1IfLetMalformedScrutineePreservesElseAndFollowingStmt(t *testing.T) {
	src := fixtureIfLetMalformedScrutinee()

	result := ParseDetailed(src)
	requireSingleParseDiagnosticCode(t, result, "E0204")
	fn := requireFnDeclAt(t, result.File, 0, 2, "if-let plus trailing let")
	stmt := requireExprStmtAt(t, fn.Body.Stmts, 0, "expression statement")
	ifExpr, ok := stmt.X.(*ast.IfExpr)
	if !ok || !ifExpr.IsIfLet || ifExpr.Cond != nil {
		t.Fatalf("stmt[0].X = %#v, want if-let with nil recovered condition", stmt.X)
	}
	elseBlock, ok := ifExpr.Else.(*ast.Block)
	if !ok || len(elseBlock.Stmts) != 1 {
		t.Fatalf("ifExpr.Else = %#v, want preserved else block", ifExpr.Else)
	}
	second := requireLetStmtAt(t, fn.Body.Stmts, 1, "trailing let statement")
	requireIdentPatName(t, second.Pattern, "z", "let z = 2")
}
