package gen_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
		res := resolve.File(file, resolve.NewPrelude())
		if hasErr(res.Diags) {
			t.Errorf("FAIL(resolve) %s: resolve errors (%d)", name, countErrs(res.Diags))
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
