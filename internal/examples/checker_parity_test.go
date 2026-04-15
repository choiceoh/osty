package examples_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
	"github.com/osty/osty/internal/testgen"
)

type checkerParityCase struct {
	Name string
	Src  string
}

func TestDogfoodCheckerParityWithGoChecker(t *testing.T) {
	if testing.Short() {
		t.Skip("dogfood checker parity harness (slow)")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go binary not on PATH")
	}

	cases := []checkerParityCase{
		{
			Name: "PrimitiveProgram",
			Src: `fn add(a: Int, b: Int) -> Int {
    let mut x: Int = a + b
    x = x + 1
    return x
}
`,
		},
		{
			Name: "BadAssignmentAndReturn",
			Src: `fn bad() -> Int {
    let x: Bool = 1
    return "no"
}
`,
		},
		{
			Name: "FunctionCallMismatch",
			Src: `fn id(x: Int) -> Int { x }

fn main() {
    let good: Int = id(1)
    let bad: Bool = id("no")
}
`,
		},
		{
			Name: "BoolMatchExhaustiveness",
			Src: `fn classify(flag: Bool) -> Int {
    match flag {
        true -> 1,
    }
}
`,
		},
	}

	root := repoRoot(t)
	srcDir := filepath.Join(root, "examples", "dogfood")
	dstDir := filepath.Join(t.TempDir(), "dogfood")
	copyTree(t, srcDir, dstDir)

	entries := make([]testgen.Entry, 0, len(cases))
	var b strings.Builder
	b.WriteString("use std.testing\n\n")
	for _, c := range cases {
		want := goCheckerErrorCount(t, c.Src)
		fnName := "testCheckerParity" + c.Name
		entries = append(entries, testgen.Entry{
			Name: fnName,
			Kind: testgen.KindTest,
			File: "checker_parity_generated_test.osty",
			Line: 1,
		})
		fmt.Fprintf(&b, "fn %s() {\n", fnName)
		fmt.Fprintf(&b, "    let checked = frontendCheckSource(%s)\n", ostyStringLiteral(c.Src))
		fmt.Fprintf(&b, "    testing.assertEq(checked.errors, %d)\n", want)
		b.WriteString("}\n\n")
	}
	if err := os.WriteFile(filepath.Join(dstDir, "checker_parity_generated_test.osty"), []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write generated parity test: %v", err)
	}

	pkg, chk := checkPackageDir(t, dstDir, true)
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
		t.Fatalf("missing checker parity summary %q in:\n%s", want, out)
	}
}

func goCheckerErrorCount(t *testing.T, src string) int {
	t.Helper()
	file, parseDiags := parser.ParseDiagnostics([]byte(src))
	errs := countErrorDiags(parseDiags)
	if file == nil {
		return errs
	}
	reg := stdlib.LoadCached()
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), reg)
	errs += countErrorDiags(res.Diags)
	chk := check.File(file, res, check.Opts{Primitives: reg.Primitives, ResultMethods: reg.ResultMethods})
	errs += countErrorDiags(chk.Diags)
	return errs
}

func countErrorDiags(diags []*diag.Diagnostic) int {
	count := 0
	for _, d := range diags {
		if d.Severity == diag.Error {
			count++
		}
	}
	return count
}

func ostyStringLiteral(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString("\\\\")
		case '"':
			b.WriteString("\\\"")
		case '\n':
			b.WriteString("\\n")
		case '\r':
			b.WriteString("\\r")
		case '\t':
			b.WriteString("\\t")
		case '{':
			b.WriteString("\\{")
		case '}':
			b.WriteString("\\}")
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func copyTree(t *testing.T, srcDir, dstDir string) {
	t.Helper()
	err := filepath.WalkDir(srcDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(dstDir, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, info.Mode())
	})
	if err != nil {
		t.Fatalf("copy %s to %s: %v", srcDir, dstDir, err)
	}
}
