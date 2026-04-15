package gen_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/gen"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
)

// TestSpecCorpusAudit runs every positive spec fixture through the
// full pipeline (parse → resolve → check → transpile → `go vet`).
// Check diagnostics are logged because the single-file fixture harness
// still treats some imported stdlib/package surfaces as opaque; gen
// must nevertheless lower every positive fixture to Go that type-checks.
//
// Run explicitly:
//
//	go test ./internal/gen/ -run TestSpecCorpusAudit -v
func TestSpecCorpusAudit(t *testing.T) {
	if testing.Short() {
		t.Skip("spec corpus Go vet audit (slow)")
	}
	fixtures, err := filepath.Glob("../../testdata/spec/positive/*.osty")
	if err != nil {
		t.Fatal(err)
	}
	if len(fixtures) == 0 {
		t.Skip("no fixtures found (not in repo?)")
	}

	var passed, total int
	for _, f := range fixtures {
		total++
		name := filepath.Base(f)
		src, err := os.ReadFile(f)
		if err != nil {
			t.Logf("SKIP %s: %v", name, err)
			continue
		}
		file, parseDiags := parser.ParseDiagnostics(src)
		if hasErr(parseDiags) {
			t.Errorf("FAIL(parse) %s: parse errors (%d)", name, countErrs(parseDiags))
			continue
		}
		file, res, err := resolveAuditFixture(t, src)
		if err != nil {
			t.Errorf("FAIL(resolve setup) %s: %v", name, err)
			continue
		}
		if hasErr(res.Diags) {
			t.Errorf("FAIL(resolve) %s: resolve errors (%d): %s",
				name, countErrs(res.Diags), firstErr(res.Diags))
			continue
		}
		chk := check.File(file, res)
		if hasErr(chk.Diags) {
			t.Logf("WARN(check) %s: check errors (%d)", name, countErrs(chk.Diags))
		}

		goSrc, gerr := gen.Generate("main", file, res, chk)
		if gerr != nil {
			t.Errorf("FAIL(gen) %s: %v", name, gerr)
			continue
		}

		dir := t.TempDir()
		outPath := filepath.Join(dir, "out.go")
		if err := os.WriteFile(outPath, goSrc, 0o644); err != nil {
			t.Errorf("FAIL(write) %s: %v", name, err)
			continue
		}
		// `go vet` type-checks without requiring a main function, so
		// library-shaped fixtures (no main, no script stmts) still get
		// a compile-level sanity pass. It returns non-zero on any
		// Go syntax or type error.
		cmd := exec.Command("go", "vet", outPath)
		out, err := cmd.CombinedOutput()
		if err != nil {
			lines := strings.Split(string(out), "\n")
			msg := ""
			for _, l := range lines {
				l = strings.TrimSpace(l)
				if l == "" || strings.HasPrefix(l, "#") {
					continue
				}
				if idx := strings.LastIndex(l, "/"); idx >= 0 {
					l = l[idx+1:]
				}
				msg = l
				break
			}
			t.Errorf("FAIL(compile) %s: %s", name, msg)
			continue
		}
		t.Logf("OK   %s (%d bytes)", name, len(goSrc))
		passed++
	}
	t.Logf("Spec corpus audit: %d/%d fixtures transpile to compilable Go.",
		passed, total)
}

func hasErr(ds []*diag.Diagnostic) bool {
	for _, d := range ds {
		if d.Severity == diag.Error {
			return true
		}
	}
	return false
}
func countErrs(ds []*diag.Diagnostic) int {
	n := 0
	for _, d := range ds {
		if d.Severity == diag.Error {
			n++
		}
	}
	return n
}

func firstErr(ds []*diag.Diagnostic) string {
	for _, d := range ds {
		if d.Severity == diag.Error {
			return d.Error()
		}
	}
	return ""
}

type auditDepProvider map[string]string

func (p auditDepProvider) LookupDep(rawPath string) (string, bool) {
	dir, ok := p[rawPath]
	return dir, ok
}

func resolveAuditFixture(t *testing.T, src []byte) (*ast.File, *resolve.Result, error) {
	t.Helper()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.osty"), src, 0o644); err != nil {
		return nil, nil, err
	}

	deps, err := writeAuditDeps(t.TempDir())
	if err != nil {
		return nil, nil, err
	}
	ws, err := resolve.NewWorkspace(root)
	if err != nil {
		return nil, nil, err
	}
	ws.Deps = deps
	if _, err := ws.LoadPackage(""); err != nil {
		return nil, nil, err
	}
	results := ws.ResolveAll()
	pkg := ws.Packages[""]
	if pkg == nil || len(pkg.Files) == 0 {
		return nil, nil, os.ErrNotExist
	}
	pf := pkg.Files[0]
	pr := results[""]
	if pr == nil {
		pr = &resolve.PackageResult{}
	}
	return pf.File, &resolve.Result{
		Refs:      pf.Refs,
		TypeRefs:  pf.TypeRefs,
		FileScope: pf.FileScope,
		Diags:     append(append([]*diag.Diagnostic{}, pf.ParseDiags...), pr.Diags...),
	}, nil
}

func writeAuditDeps(root string) (auditDepProvider, error) {
	provider := auditDepProvider{}
	for _, key := range []string{"github.com/user/lib", "github.com/user/lib2"} {
		dir := filepath.Join(root, strings.ReplaceAll(key, "/", "_"))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(dir, "lib.osty"), []byte("pub fn marker() -> Int { 0 }\n"), 0o644); err != nil {
			return nil, err
		}
		provider[key] = dir
	}
	return provider, nil
}
