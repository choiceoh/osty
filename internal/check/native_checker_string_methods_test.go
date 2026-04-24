package check

import (
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/stdlib"
)

// TestNativeBoundaryExecRecognizesStringTransformMethods guards the
// checkInstallBuiltinMethods registration for receiver-syntax String
// methods that the LLVM backend has had runtime + emitter coverage
// for, but that the native checker's hardcoded intrinsic list was
// missing — `s.toUpper()` / `s.toLower()` / `s.trim()` /
// `s.replace(old, new)` / `s.repeat(n)` all tripped E0703 "no method
// on type `String`" before reaching the backend.
//
// CLAUDE.md §B.5 lists these as canonical String methods and the
// qualified `strings.toUpper(s)` form already works, so rejecting
// the receiver form forced users into asymmetric call-site syntax
// for no semantic reason.
func TestNativeBoundaryExecRecognizesStringTransformMethods(t *testing.T) {
	src := []byte(`fn main() {
    let s = "Hello"
    let upper = s.toUpper()
    let lower = s.toLower()
    let trimmed = "  padded  ".trim()
    let replaced = s.replace("ell", "AY")
    let repeated = "ab".repeat(3)
}
`)
	file, res := parseResolvedFile(t, src)
	mainDecl := file.Decls[0].(*ast.FnDecl)
	upperLet := mainDecl.Body.Stmts[1].(*ast.LetStmt)
	lowerLet := mainDecl.Body.Stmts[2].(*ast.LetStmt)
	trimmedLet := mainDecl.Body.Stmts[3].(*ast.LetStmt)
	replacedLet := mainDecl.Body.Stmts[4].(*ast.LetStmt)
	repeatedLet := mainDecl.Body.Stmts[5].(*ast.LetStmt)

	bin := buildRepoNativeChecker(t)
	t.Setenv(nativeCheckerEnv, bin)

	oldFactory := nativeCheckerFactory
	nativeCheckerFactory = defaultNativeChecker
	t.Cleanup(func() { nativeCheckerFactory = oldFactory })

	chk := SelfhostFile(file, res, Opts{Source: src, Stdlib: stdlib.LoadCached()})
	if len(chk.Diags) != 0 {
		t.Fatalf("expected no diagnostics, got %v", chk.Diags)
	}
	for _, tc := range []struct {
		name string
		stmt *ast.LetStmt
	}{
		{"toUpper", upperLet},
		{"toLower", lowerLet},
		{"trim", trimmedLet},
		{"replace", replacedLet},
		{"repeat", repeatedLet},
	} {
		if got := chk.LetTypes[tc.stmt]; got == nil || got.String() != "String" {
			t.Errorf("%s binding type = %v, want String", tc.name, got)
		}
	}
}
