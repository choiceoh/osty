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
// full pipeline (parse → resolve → check → transpile → `go build`) and
// reports which ones survive. This is NOT an assertion — a hard fail
// would block iteration — but it's tracked so we can see progress.
//
// Run explicitly:
//
//	go test ./internal/gen/ -run TestSpecCorpusAudit -v
func TestSpecCorpusAudit(t *testing.T) {
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
			t.Logf("SKIP %s: parse errors", name)
			continue
		}
		res := resolve.File(file, resolve.NewPrelude())
		if hasErr(res.Diags) {
			t.Logf("SKIP %s: resolve errors (%d)", name, countErrs(res.Diags))
			continue
		}
		chk := check.File(file, res)
		// We don't skip on check errors — the transpiler is still
		// expected to produce SOMETHING for incomplete programs.

		goSrc, gerr := gen.Generate("main", file, res, chk)
		if gerr != nil {
			t.Logf("FAIL(gen) %s: %v", name, gerr)
			continue
		}

		dir := t.TempDir()
		outPath := filepath.Join(dir, "out.go")
		if err := os.WriteFile(outPath, goSrc, 0o644); err != nil {
			t.Logf("FAIL(write) %s: %v", name, err)
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
			t.Logf("FAIL(compile) %s: %s", name, msg)
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
