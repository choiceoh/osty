package examples_test

import (
	"bytes"
	"fmt"
	goparser "go/parser"
	gotoken "go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/gen"
	"github.com/osty/osty/internal/manifest"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
	"github.com/osty/osty/internal/testgen"
)

func TestExampleManifestsValidate(t *testing.T) {
	root := repoRoot(t)
	examples := filepath.Join(root, "examples")
	err := filepath.WalkDir(examples, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != manifest.ManifestFile {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		m, err := manifest.Parse(src)
		if err != nil {
			t.Errorf("%s: parse manifest: %v", rel(root, path), err)
			return nil
		}
		for _, d := range manifest.Validate(m) {
			if d.Severity == diag.Error {
				t.Errorf("%s: manifest diagnostic: %s", rel(root, path), d.Error())
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk examples: %v", err)
	}
}

func TestRunnableExamplesGenerateAndExecute(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go binary not on PATH")
	}
	root := repoRoot(t)
	cases := []struct {
		path string
		want string
	}{
		{"examples/ffi/main.osty", "OSTYOSTY\nffi ok\n"},
		{"examples/concurrency/main.osty", "sum=39\ngrouped=61\n"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.path, func(t *testing.T) {
			goSrc := transpileFile(t, filepath.Join(root, c.path))
			got := runGo(t, goSrc)
			if got != c.want {
				t.Fatalf("output mismatch for %s\ngot:\n%q\nwant:\n%q\n--- go ---\n%s",
					c.path, got, c.want, goSrc)
			}
		})
	}
}

func TestDogfoodExampleTestsExecute(t *testing.T) {
	executeExampleTests(t, "dogfood")
}

func TestSelfhostCoreExampleTestsExecute(t *testing.T) {
	executeExampleTests(t, "selfhost-core")
}

func executeExampleTests(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go binary not on PATH")
	}
	root := repoRoot(t)
	dir := filepath.Join(root, "examples", name)
	pkg, chk := checkPackageDir(t, dir, true)
	entries := discoverEntries(pkg)
	if len(entries) == 0 {
		t.Fatalf("%s example exposed no test or benchmark entries", name)
	}
	srcs, err := testgen.GenerateHarness(pkg, chk, entries)
	if err != nil {
		t.Fatalf("GenerateHarness: %v", err)
	}
	assertParsesAsGo(t, "main.go", srcs.Main)
	assertParsesAsGo(t, "harness.go", srcs.Harness)

	out := runGoPackage(t, map[string][]byte{
		"main.go":    srcs.Main,
		"harness.go": srcs.Harness,
	})
	want := fmt.Sprintf("%d passed, 0 failed, %d total", len(entries), len(entries))
	if !strings.Contains(out, want) {
		t.Fatalf("missing %s test summary %q in:\n%s", name, want, out)
	}
}

func TestStdlibTourExampleChecks(t *testing.T) {
	root := repoRoot(t)
	checkPackageDir(t, filepath.Join(root, "examples", "stdlib-tour"), false)
}

func TestWorkspaceExampleChecks(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "examples", "workspace")
	raw, err := os.ReadFile(filepath.Join(dir, manifest.ManifestFile))
	if err != nil {
		t.Fatalf("read workspace manifest: %v", err)
	}
	m, err := manifest.Parse(raw)
	if err != nil {
		t.Fatalf("parse workspace manifest: %v", err)
	}
	failOnErrors(t, "workspace manifest", manifest.Validate(m))
	if m.Workspace == nil {
		t.Fatal("workspace example has no [workspace] table")
	}

	ws, err := resolve.NewWorkspace(dir)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	ws.Stdlib = stdlib.LoadCached()
	for _, member := range m.Workspace.Members {
		if _, err := ws.LoadPackage(member); err != nil {
			t.Fatalf("load workspace member %s: %v", member, err)
		}
	}
	results := ws.ResolveAll()
	checks := check.Workspace(ws, results, check.Opts{Primitives: stdlib.LoadCached().Primitives})
	paths := make([]string, 0, len(ws.Packages))
	for p := range ws.Packages {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		pkg := ws.Packages[p]
		if pkg == nil {
			continue
		}
		var parseDiags []*diag.Diagnostic
		for _, pf := range pkg.Files {
			parseDiags = append(parseDiags, pf.ParseDiags...)
		}
		failOnErrors(t, "workspace "+p+" parse", parseDiags)
		if r := results[p]; r != nil {
			failOnErrors(t, "workspace "+p+" resolve", r.Diags)
		}
		if c := checks[p]; c != nil {
			failOnErrors(t, "workspace "+p+" check", c.Diags)
		}
	}
}

func checkPackageDir(t *testing.T, dir string, includeTests bool) (*resolve.Package, *check.Result) {
	t.Helper()
	var (
		pkg *resolve.Package
		err error
	)
	if includeTests {
		pkg, err = resolve.LoadPackageWithTests(dir)
	} else {
		pkg, err = resolve.LoadPackage(dir)
	}
	if err != nil {
		t.Fatalf("load package %s: %v", dir, err)
	}
	var parseDiags []*diag.Diagnostic
	for _, pf := range pkg.Files {
		parseDiags = append(parseDiags, pf.ParseDiags...)
	}
	failOnErrors(t, rel(repoRoot(t), dir)+" parse", parseDiags)

	ws, err := resolve.NewWorkspace(dir)
	if err != nil {
		t.Fatalf("workspace %s: %v", dir, err)
	}
	ws.Stdlib = stdlib.LoadCached()
	ws.Packages[""] = pkg
	pkg.Name = filepath.Base(dir)
	preloadPackageUses(ws, pkg)
	results := ws.ResolveAll()
	pr := results[""]
	if pr == nil {
		t.Fatalf("missing root package result for %s", dir)
	}
	failOnErrors(t, rel(repoRoot(t), dir)+" resolve", pr.Diags)
	chk := check.Package(pkg, pr, check.Opts{Primitives: stdlib.LoadCached().Primitives})
	failOnErrors(t, rel(repoRoot(t), dir)+" check", chk.Diags)
	return pkg, chk
}

func preloadPackageUses(ws *resolve.Workspace, pkg *resolve.Package) {
	for _, pf := range pkg.Files {
		if pf.File == nil {
			continue
		}
		for _, u := range pf.File.Uses {
			if u.IsGoFFI {
				continue
			}
			target := strings.Join(u.Path, ".")
			if target == "" {
				continue
			}
			if _, ok := ws.Packages[target]; ok {
				continue
			}
			_, _ = ws.LoadPackage(target)
		}
	}
}

func transpileFile(t *testing.T, path string) []byte {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	file, parseDiags := parser.ParseDiagnostics(src)
	failOnErrors(t, rel(repoRoot(t), path)+" parse", parseDiags)
	reg := stdlib.LoadCached()
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), reg)
	failOnErrors(t, rel(repoRoot(t), path)+" resolve", res.Diags)
	chk := check.File(file, res, check.Opts{Primitives: reg.Primitives})
	failOnErrors(t, rel(repoRoot(t), path)+" check", chk.Diags)
	goSrc, err := gen.Generate("main", file, res, chk)
	if err != nil {
		t.Fatalf("gen %s: %v\n%s", rel(repoRoot(t), path), err, goSrc)
	}
	assertParsesAsGo(t, filepath.Base(path)+".go", goSrc)
	return goSrc
}

func discoverEntries(pkg *resolve.Package) []testgen.Entry {
	files := append([]*resolve.PackageFile(nil), pkg.Files...)
	sort.SliceStable(files, func(i, j int) bool {
		return files[i].Path < files[j].Path
	})
	var out []testgen.Entry
	for _, pf := range files {
		if pf.File == nil {
			continue
		}
		for _, d := range pf.File.Decls {
			fn, ok := d.(*ast.FnDecl)
			if !ok || !isDiscoverable(fn) {
				continue
			}
			switch {
			case strings.HasPrefix(fn.Name, "test"):
				out = append(out, testgen.Entry{
					Name: fn.Name,
					Kind: testgen.KindTest,
					File: filepath.Base(pf.Path),
					Line: fn.PosV.Line,
				})
			case strings.HasPrefix(fn.Name, "bench"):
				out = append(out, testgen.Entry{
					Name: fn.Name,
					Kind: testgen.KindBench,
					File: filepath.Base(pf.Path),
					Line: fn.PosV.Line,
				})
			}
		}
	}
	return out
}

func isDiscoverable(fn *ast.FnDecl) bool {
	return fn.Recv == nil &&
		len(fn.Params) == 0 &&
		len(fn.Generics) == 0 &&
		fn.ReturnType == nil
}

func failOnErrors(t *testing.T, label string, diags []*diag.Diagnostic) {
	t.Helper()
	for _, d := range diags {
		if d.Severity == diag.Error {
			t.Errorf("%s: %s", label, d.Error())
		}
	}
}

func assertParsesAsGo(t *testing.T, name string, src []byte) {
	t.Helper()
	if _, err := goparser.ParseFile(gotoken.NewFileSet(), name, src, 0); err != nil {
		t.Fatalf("%s does not parse as Go: %v\n%s", name, err, src)
	}
}

func runGo(t *testing.T, src []byte) string {
	t.Helper()
	return runGoPackage(t, map[string][]byte{"main.go": src})
}

func runGoPackage(t *testing.T, files map[string][]byte) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), src, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if _, ok := files["go.mod"]; !ok {
		if err := os.WriteFile(filepath.Join(dir, "go.mod"),
			[]byte("module example\n\ngo 1.22\n"), 0o644); err != nil {
			t.Fatalf("write go.mod: %v", err)
		}
	}
	cmd := exec.Command("go", "run", ".")
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("go run failed: %v\n%s", err, out.String())
	}
	return out.String()
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("go.mod not found above %s", dir)
		}
		dir = parent
	}
}

func rel(root, path string) string {
	if r, err := filepath.Rel(root, path); err == nil {
		return r
	}
	return path
}
