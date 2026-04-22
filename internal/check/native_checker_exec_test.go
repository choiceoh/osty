package check

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/selfhost"
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
	if got := chk.InstantiationsByID[call.ID]; len(got) != 1 || got[0].String() != "Int" {
		t.Fatalf("call instantiation = %#v, want [Int]", got)
	}
}

func TestDefaultNativeCheckerUsesEmbeddedCheckerWhenEnvUnset(t *testing.T) {
	t.Setenv(nativeCheckerEnv, "")

	runner, note := defaultNativeChecker()
	if runner == nil {
		t.Fatalf("defaultNativeChecker returned nil runner: %s", note)
	}
	if _, ok := runner.(embeddedNativeChecker); !ok {
		t.Fatalf("defaultNativeChecker runner = %T, want embeddedNativeChecker", runner)
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

func TestNativeBoundaryExecKeepsUnusedDeclarationTypes(t *testing.T) {
	src := []byte(`fn unused(value: Int) -> Int {
    0
}

fn main() {}
`)
	file, res := parseResolvedFile(t, src)

	bin := buildRepoNativeChecker(t)
	t.Setenv(nativeCheckerEnv, bin)

	oldFactory := nativeCheckerFactory
	nativeCheckerFactory = defaultNativeChecker
	t.Cleanup(func() { nativeCheckerFactory = oldFactory })

	chk := File(file, res, Opts{Source: src, Stdlib: stdlib.LoadCached()})
	if len(chk.Diags) != 0 {
		t.Fatalf("expected no diagnostics, got %v", chk.Diags)
	}
	if got := chk.LookupSymType(res.FileScope.Lookup("unused")); got == nil || got.String() != "fn(Int) -> Int" {
		t.Fatalf("unused fn type = %v, want fn(Int) -> Int", got)
	}
}

func TestNativeBoundaryExecRecognizesBytesIntrinsics(t *testing.T) {
	src := []byte(`fn main() {
    let a: Byte = 65
    let b: Byte = 66
    let data = Bytes.from([a, b])
    let size = data.len()
    let first = data.get(0)
    let sameSize = Bytes.len(data)
    let second = Bytes.get(data, 1)
}
`)
	file, res := parseResolvedFile(t, src)
	mainDecl := file.Decls[0].(*ast.FnDecl)
	dataLet := mainDecl.Body.Stmts[2].(*ast.LetStmt)
	sizeLet := mainDecl.Body.Stmts[3].(*ast.LetStmt)
	firstLet := mainDecl.Body.Stmts[4].(*ast.LetStmt)
	sameSizeLet := mainDecl.Body.Stmts[5].(*ast.LetStmt)
	secondLet := mainDecl.Body.Stmts[6].(*ast.LetStmt)

	bin := buildRepoNativeChecker(t)
	t.Setenv(nativeCheckerEnv, bin)

	oldFactory := nativeCheckerFactory
	nativeCheckerFactory = defaultNativeChecker
	t.Cleanup(func() { nativeCheckerFactory = oldFactory })

	chk := File(file, res, Opts{Source: src, Stdlib: stdlib.LoadCached()})
	if len(chk.Diags) != 0 {
		t.Fatalf("expected no diagnostics, got %v", chk.Diags)
	}
	if got := chk.LetTypes[dataLet]; got == nil || got.String() != "Bytes" {
		t.Fatalf("data type = %v, want Bytes", got)
	}
	if got := chk.LetTypes[sizeLet]; got == nil || got.String() != "Int" {
		t.Fatalf("size type = %v, want Int", got)
	}
	if got := chk.LetTypes[firstLet]; got == nil || got.String() != "Byte?" {
		t.Fatalf("first type = %v, want Byte?", got)
	}
	if got := chk.LetTypes[sameSizeLet]; got == nil || got.String() != "Int" {
		t.Fatalf("sameSize type = %v, want Int", got)
	}
	if got := chk.LetTypes[secondLet]; got == nil || got.String() != "Byte?" {
		t.Fatalf("second type = %v, want Byte?", got)
	}
}

func TestNativeBoundaryExecChecksStructuredPackageInput(t *testing.T) {
	fileA := []byte("fn helper() -> Int { 1 }\n")
	fileB := []byte("fn main() { let value = helper() }\n")

	bin := buildRepoNativeChecker(t)
	runner := nativeCheckerExec{path: bin}
	checked, err := runner.CheckPackageStructured(selfhost.PackageCheckInput{
		Files: []selfhost.PackageCheckFile{
			{Source: fileA, Base: 0, Name: "a.osty"},
			{Source: fileB, Base: len(fileA) + 1, Name: "b.osty"},
		},
	})
	if err != nil {
		t.Fatalf("CheckPackageStructured error: %v", err)
	}
	if checked.Summary.Errors != 0 {
		t.Fatalf("summary errors = %d, want 0", checked.Summary.Errors)
	}
	found := false
	for _, binding := range checked.Bindings {
		if binding.Name == "value" && binding.TypeName == "Int" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("bindings = %#v, want value:Int from package request", checked.Bindings)
	}
}

func buildRepoNativeChecker(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	root := filepath.Clean(filepath.Join(cwd, "..", ".."))
	name := "osty-native-checker"
	if runtime.GOOS == "windows" {
		// `go build -o NAME` appends .exe automatically on Windows;
		// match it here so callers that pass the path directly to
		// exec.Command (without going through exec.LookPath) find the
		// binary. The env-var path below goes through LookPath and
		// handles the extension transparently, so either form works.
		name += ".exe"
	}
	bin := filepath.Join(t.TempDir(), name)
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/osty-native-checker")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build osty-native-checker: %v\n%s", err, out)
	}
	return bin
}
