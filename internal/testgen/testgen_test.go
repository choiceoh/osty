package testgen

import (
	goparser "go/parser"
	gotoken "go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// TestGenerateHarness_CalcExample runs the full pipeline against the
// examples/calc package — the canonical end-to-end fixture for the
// Phase-1 test runner. We don't check exact bytes (the gen output is
// large and changes when gen evolves) but we assert that:
//
//   - main.go and harness.go both parse as Go;
//   - main() calls _harness.run for every entry we handed in;
//   - the harness compiles & runs cleanly via `go run`, exits 0,
//     and prints a summary line whose pass count matches our entries.
func TestGenerateHarness_CalcExample(t *testing.T) {
	dir := repoPath(t, "examples/calc")
	pkg, chk := loadCalc(t, dir)

	entries := []Entry{
		{Name: "testAddBasic", Kind: KindTest, File: "lib_test.osty", Line: 19},
		{Name: "testIsEven", Kind: KindTest, File: "lib_test.osty", Line: 65},
		{Name: "benchAddHotPath", Kind: KindBench, File: "lib_test.osty", Line: 102},
	}

	srcs, err := GenerateHarness(pkg, chk, entries)
	if err != nil {
		t.Fatalf("GenerateHarness: %v", err)
	}
	if len(srcs.Main) == 0 || len(srcs.Harness) == 0 {
		t.Fatalf("empty sources: main=%d harness=%d", len(srcs.Main), len(srcs.Harness))
	}

	fset := gotoken.NewFileSet()
	if _, err := goparser.ParseFile(fset, "main.go", srcs.Main, 0); err != nil {
		t.Fatalf("main.go does not parse: %v\n--- main.go ---\n%s", err, srcs.Main)
	}
	if _, err := goparser.ParseFile(fset, "harness.go", srcs.Harness, 0); err != nil {
		t.Fatalf("harness.go does not parse: %v", err)
	}

	main := string(srcs.Main)
	for _, e := range entries {
		needle := `_harness.run("` + e.Name + `"`
		if !strings.Contains(main, needle) {
			t.Errorf("main.go missing harness.run call for %s\n--- main.go ---\n%s", e.Name, main)
		}
	}

	// End-to-end smoke: write both files into a scratch dir with a
	// minimal go.mod and confirm `go run .` exits 0 and prints the
	// expected summary. Skips automatically if `go` isn't on PATH.
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go binary not on PATH; skipping end-to-end run")
	}
	out := t.TempDir()
	mustWrite(t, filepath.Join(out, "main.go"), srcs.Main)
	mustWrite(t, filepath.Join(out, "harness.go"), srcs.Harness)
	mustWrite(t, filepath.Join(out, "go.mod"), []byte("module ostytest\ngo 1.22\n"))

	cmd := exec.Command("go", "run", ".")
	cmd.Dir = out
	combined, runErr := cmd.CombinedOutput()
	if runErr != nil {
		t.Fatalf("go run failed: %v\n%s", runErr, combined)
	}
	want := "3 passed, 0 failed, 3 total"
	if !strings.Contains(string(combined), want) {
		t.Fatalf("missing summary %q in:\n%s", want, combined)
	}
}

// TestGenerateHarness_DedupesResultRuntime verifies that when multiple
// files in a package emit the Result[T, E] runtime type, only one
// definition survives the merge — duplicate type definitions are a
// `go build` error. We assert by counting `type Result[` occurrences
// in the printed Main.
func TestGenerateHarness_DedupesResultRuntime(t *testing.T) {
	dir := repoPath(t, "examples/calc")
	pkg, chk := loadCalc(t, dir)

	srcs, err := GenerateHarness(pkg, chk, nil)
	if err != nil {
		t.Fatalf("GenerateHarness: %v", err)
	}
	count := strings.Count(string(srcs.Main), "type Result[T any, E any] struct")
	if count != 1 {
		t.Errorf("expected exactly 1 Result type definition, got %d", count)
	}
	count = strings.Count(string(srcs.Main), "func ostyToString(v any) string")
	if count != 1 {
		t.Errorf("expected exactly 1 ostyToString helper, got %d", count)
	}
}

// TestGenerateHarness_StripsTestingStub checks that the no-op `var
// testing = struct{…}{…}` gen emits for `use std.testing` is removed
// from the merged output — the runtime in harness.go owns that name
// and a duplicate var declaration would not compile.
func TestGenerateHarness_StripsTestingStub(t *testing.T) {
	dir := repoPath(t, "examples/calc")
	pkg, chk := loadCalc(t, dir)

	srcs, err := GenerateHarness(pkg, chk, nil)
	if err != nil {
		t.Fatalf("GenerateHarness: %v", err)
	}
	// gen's stub assigns `testing = struct{ assert func(...any) ... }`.
	// The harness's real definition is in harness.go (separate file),
	// so the merged main.go must NOT redeclare `testing`.
	if strings.Contains(string(srcs.Main), "var testing = struct") {
		t.Errorf("main.go still contains the no-op testing stub\n%s", srcs.Main)
	}
}

func TestGenerateHarness_RenamesBinaryMain(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "main.osty"), []byte(`fn main() {
    println("Hello, Osty!")
}
`))
	mustWrite(t, filepath.Join(dir, "main_test.osty"), []byte(`fn testMainRuns() {
    main()
}
`))
	pkg, err := resolve.LoadPackageWithTests(dir)
	if err != nil {
		t.Fatalf("load package: %v", err)
	}
	res := resolve.ResolvePackage(pkg, resolve.NewPrelude())
	chk := check.Package(pkg, res)
	srcs, err := GenerateHarness(pkg, chk, []Entry{{
		Name: "testMainRuns",
		Kind: KindTest,
		File: "main_test.osty",
		Line: 1,
	}})
	if err != nil {
		t.Fatalf("GenerateHarness: %v", err)
	}
	main := string(srcs.Main)
	if strings.Count(main, "func main()") != 1 {
		t.Fatalf("expected only harness main, got:\n%s", main)
	}
	if !strings.Contains(main, "func _ostyProgramMain()") {
		t.Fatalf("binary entry point was not preserved under _ostyProgramMain:\n%s", main)
	}
	if !strings.Contains(main, "\t_ostyProgramMain()\n") {
		t.Fatalf("test call to main() was not rewritten to _ostyProgramMain():\n%s", main)
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go binary not on PATH; skipping end-to-end run")
	}
	out := t.TempDir()
	mustWrite(t, filepath.Join(out, "main.go"), srcs.Main)
	mustWrite(t, filepath.Join(out, "harness.go"), srcs.Harness)
	mustWrite(t, filepath.Join(out, "go.mod"), []byte("module ostytest\ngo 1.22\n"))
	cmd := exec.Command("go", "run", ".")
	cmd.Dir = out
	combined, runErr := cmd.CombinedOutput()
	if runErr != nil {
		t.Fatalf("go run failed: %v\n%s", runErr, combined)
	}
	outText := string(combined)
	if !strings.Contains(outText, "Hello, Osty!") {
		t.Fatalf("rewritten main() call did not execute program entry point:\n%s", combined)
	}
	if !strings.Contains(outText, "1 passed, 0 failed, 1 total") {
		t.Fatalf("missing summary in:\n%s", combined)
	}
}

// TestGenerateHarness_NilPackage ensures the bad-input path returns
// an error rather than panicking. Defensive — the CLI always passes a
// non-nil package today, but keeping the contract explicit guards
// against future call-site refactors.
func TestGenerateHarness_NilPackage(t *testing.T) {
	if _, err := GenerateHarness(nil, nil, nil); err == nil {
		t.Fatal("expected error for nil package")
	}
}

// loadCalc runs the front-end pipeline used by `osty test` so the
// returned package + check result mirror what the CLI passes to
// testgen at runtime. Test-only helper kept here so individual tests
// don't have to repeat the boilerplate.
func loadCalc(t *testing.T, dir string) (*resolve.Package, *check.Result) {
	t.Helper()
	pkg, err := resolve.LoadPackageWithTests(dir)
	if err != nil {
		t.Fatalf("load %s: %v", dir, err)
	}
	ws, err := resolve.NewWorkspace(dir)
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	reg := stdlib.LoadCached()
	ws.Stdlib = reg
	ws.Packages[""] = pkg
	pkg.Name = filepath.Base(dir)
	for _, pf := range pkg.Files {
		if pf.File == nil {
			continue
		}
		for _, u := range pf.File.Uses {
			if u.IsGoFFI {
				continue
			}
			target := strings.Join(u.Path, ".")
			if _, has := ws.Packages[target]; has {
				continue
			}
			_, _ = ws.LoadPackage(target)
		}
	}
	results := ws.ResolveAll()
	chk := check.Package(pkg, results[""], check.Opts{Primitives: reg.Primitives, ResultMethods: reg.ResultMethods})
	return pkg, chk
}

// repoPath joins p onto the repo root (two levels up from this
// internal package) so tests can reference fixtures by their
// repo-relative path. Skips the test if the resolved path doesn't
// exist — useful when running ./internal/testgen in isolation from a
// vendored snapshot.
func repoPath(t *testing.T, p string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "..", p))
	if err != nil {
		t.Fatalf("abs %s: %v", p, err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Skipf("fixture %s missing: %v", abs, err)
	}
	return abs
}

func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
