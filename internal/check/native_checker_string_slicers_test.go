package check

import (
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/stdlib"
)

// TestNativeBoundaryExecRecognizesStringSlicerMethods follows the
// pattern of TestNativeBoundaryExecRecognizesStringTransformMethods
// but covers the receiver-syntax String slicers whose intrinsic
// registration was missing: `split` / `lines` return List<String>,
// `indexOf` returns Int?, `charCount` returns Int, and
// `trimStart` / `trimEnd` return String (the symmetric partners of
// the already-registered `trim`).
//
// The LLVM backend has emitter coverage for all of these via
// `osty_rt_strings_*` runtime symbols; only the native checker's
// intrinsic list was filtering them out before the backend could see
// them.
func TestNativeBoundaryExecRecognizesStringSlicerMethods(t *testing.T) {
	src := []byte(`fn main() {
    let s = "hello world"
    let parts = s.split(" ")
    let rows = "a\nb".lines()
    let left = "  padded".trimStart()
    let right = "padded  ".trimEnd()
    let pos = s.indexOf("world")
    let count = s.charCount()
}
`)
	file, res := parseResolvedFile(t, src)
	mainDecl := file.Decls[0].(*ast.FnDecl)
	partsLet := mainDecl.Body.Stmts[1].(*ast.LetStmt)
	rowsLet := mainDecl.Body.Stmts[2].(*ast.LetStmt)
	leftLet := mainDecl.Body.Stmts[3].(*ast.LetStmt)
	rightLet := mainDecl.Body.Stmts[4].(*ast.LetStmt)
	posLet := mainDecl.Body.Stmts[5].(*ast.LetStmt)
	countLet := mainDecl.Body.Stmts[6].(*ast.LetStmt)

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
		name     string
		stmt     *ast.LetStmt
		wantType string
	}{
		{"split", partsLet, "List<String>"},
		{"lines", rowsLet, "List<String>"},
		{"trimStart", leftLet, "String"},
		{"trimEnd", rightLet, "String"},
		{"indexOf", posLet, "Int?"},
		{"charCount", countLet, "Int"},
	} {
		if got := chk.LetTypes[tc.stmt]; got == nil || got.String() != tc.wantType {
			t.Errorf("%s binding type = %v, want %s", tc.name, got, tc.wantType)
		}
	}
}
