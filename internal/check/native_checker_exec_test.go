package check

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/stdlib"
)

func TestNativeBoundaryExecutesRepoCheckerBinary(t *testing.T) {
	src := []byte(`fn id<T>(value: T) -> T { value }

fn main() {
    let answer = id::<Int>(1)
}
`)
	file, res := parseResolvedFile(t, src)
	mainDecl := file.Decls[1].(*ast.FnDecl)
	letStmt := mainDecl.Body.Stmts[0].(*ast.LetStmt)
	call := letStmt.Value.(*ast.CallExpr)

	bin := buildRepoNativeChecker(t)
	t.Setenv(nativeCheckerEnv, bin)

	oldFactory := nativeCheckerFactory
	nativeCheckerFactory = defaultNativeChecker
	t.Cleanup(func() { nativeCheckerFactory = oldFactory })

	chk := File(file, res, Opts{Source: src, Stdlib: stdlib.LoadCached()})
	if len(chk.Diags) != 0 {
		t.Fatalf("expected no diagnostics, got %v", chk.Diags)
	}
	if got := chk.LookupType(call); got == nil || got.String() != "Int" {
		t.Fatalf("call type = %v, want Int", got)
	}
	if got := chk.LetTypes[letStmt]; got == nil || got.String() != "Int" {
		t.Fatalf("binding type = %v, want Int", got)
	}
	if got := chk.LookupSymType(res.FileScope.Lookup("id")); got == nil || got.String() != "fn(T) -> T" {
		t.Fatalf("symbol type = %v, want fn(T) -> T", got)
	}
	if got := chk.Instantiations[call]; len(got) != 1 || got[0].String() != "Int" {
		t.Fatalf("call instantiation = %#v, want [Int]", got)
	}
}

func TestDefaultNativeCheckerUsesManagedArtifactWhenEnvUnset(t *testing.T) {
	t.Setenv(nativeCheckerEnv, "")
	bin := buildRepoNativeChecker(t)

	oldEnsure := ensureManagedNativeChecker
	ensureManagedNativeChecker = func() (string, error) { return bin, nil }
	t.Cleanup(func() { ensureManagedNativeChecker = oldEnsure })

	runner, note := defaultNativeChecker()
	if runner == nil {
		t.Fatalf("defaultNativeChecker returned nil runner: %s", note)
	}
	checked, err := runner.CheckSourceStructured([]byte("fn main() { let answer = 1 }\n"))
	if err != nil {
		t.Fatalf("CheckSourceStructured error: %v", err)
	}
	if checked.Summary.Errors != 0 {
		t.Fatalf("summary errors = %d, want 0", checked.Summary.Errors)
	}
	if len(checked.TypedNodes) == 0 {
		t.Fatalf("typed nodes = %#v, want non-empty result", checked.TypedNodes)
	}
}

func buildRepoNativeChecker(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	root := filepath.Clean(filepath.Join(cwd, "..", ".."))
	bin := filepath.Join(t.TempDir(), "osty-native-checker")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/osty-native-checker")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build osty-native-checker: %v\n%s", err, out)
	}
	return bin
}
