package check

import (
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/stdlib"
)

// TestNativeBoundaryExecRecognizesSetGenericIntrinsics covers a
// registration gap where the `Set` type itself was never declared via
// `checkRegisterType(name: "Set", generics: ["T"])` — only its methods
// were. The arity-unknown owner made `instSeedOwnerArgs` skip the
// receiver→generic unification, leaving T unbound on every call whose
// signature has no value argument to seed T from. The visible symptom
// was `s.len()` / `s.isEmpty()` / `s.clear()` tripping
// E0748 "cannot infer type parameter `T` of `len`" even though
// `s.contains(x)` worked (because the arg provides a second path to
// seed T).
//
// List and Map were both registered correctly; Set was the lone
// omission in the ` stdlib types that carry generics` block.
func TestNativeBoundaryExecRecognizesSetGenericIntrinsics(t *testing.T) {
	src := []byte(`fn main() {
    let xs: List<Int> = [1, 2, 3, 2, 1]
    let s: Set<Int> = xs.toSet()
    let size = s.len()
    let empty = s.isEmpty()
    let has = s.contains(2)
}
`)
	file, res := parseResolvedFile(t, src)
	mainDecl := file.Decls[0].(*ast.FnDecl)
	sizeLet := mainDecl.Body.Stmts[2].(*ast.LetStmt)
	emptyLet := mainDecl.Body.Stmts[3].(*ast.LetStmt)
	hasLet := mainDecl.Body.Stmts[4].(*ast.LetStmt)

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
		{"len", sizeLet, "Int"},
		{"isEmpty", emptyLet, "Bool"},
		{"contains", hasLet, "Bool"},
	} {
		if got := chk.LetTypes[tc.stmt]; got == nil || got.String() != tc.wantType {
			t.Errorf("%s binding type = %v, want %s", tc.name, got, tc.wantType)
		}
	}
}
