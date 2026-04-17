package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/runner"
	"github.com/osty/osty/internal/token"
)

// FixtureOptions configures the table-test fixture generator.
//
// The generator emits a self-contained `_test.osty` file built around
// a table-driven shape: a small `Case` struct, a function returning a
// `List<Case>`, and one `test*` driver that walks the table. The
// intent is to give users the structural skeleton they would type by
// hand for any non-trivial test suite, rather than the single smoke
// function the project starter ships with.
type FixtureOptions struct {
	// Name is used to derive both the filename (`{name}_test.osty`)
	// and the symbols in the generated source. It must satisfy the
	// same identifier rules ValidateName enforces; non-conforming
	// names are rejected before any file is written.
	Name string
	// Cases is the number of placeholder rows generated in the table.
	// A zero value defaults to 3 — enough rows to make the table
	// shape visible without padding the file with copies.
	Cases int
}

// RenderFixture returns the generated `_test.osty` source for opts.
// Callers that just want the bytes (e.g. dry-run, tests) can use this
// directly; WriteFixture is the path-aware helper for the CLI.
func RenderFixture(opts FixtureOptions) (string, *diag.Diagnostic) {
	if d := ValidateName(opts.Name); d != nil {
		return "", d
	}
	// Row-count policy lives in toolchain/scaffold_policy.osty so
	// the CLI flag default (3) and the readability cap (64) have
	// a single source of truth.
	resolved := runner.ResolveFixtureCases(opts.Cases)
	if resolved.OverCap {
		return "", diag.New(diag.Error,
			fmt.Sprintf("fixture cases=%d exceeds the %d row cap", opts.Cases, runner.ScaffoldFixtureCasesMax)).
			Code(diag.CodeScaffoldInvalidName).
			PrimaryPos(token.Pos{Line: 1, Column: 1}, "").
			Hint("pick a smaller --cases value; you can always add rows by hand").
			Build()
	}
	cases := resolved.Count

	pkgName := identForName(opts.Name)
	caseType := titleCase(pkgName) + "Case"
	tableFn := pkgName + "Cases"
	testFn := "test" + titleCase(pkgName) + "Table"

	var b strings.Builder
	fmt.Fprintf(&b, "// %s_test.osty — table-driven tests for %s.\n", opts.Name, opts.Name)
	b.WriteString("//\n")
	b.WriteString("// Each row in the table describes one scenario. Replace the\n")
	b.WriteString("// placeholder `let _ = ...` lines with `testing.assertEq` (or\n")
	b.WriteString("// the eventual std.testing equivalent) once the assertion\n")
	b.WriteString("// surface lands (spec §10 / §11).\n\n")

	fmt.Fprintf(&b, "struct %s {\n", caseType)
	b.WriteString("    name: String,\n")
	b.WriteString("    input: String,\n")
	b.WriteString("    expected: String,\n")
	b.WriteString("}\n\n")

	fmt.Fprintf(&b, "fn %s() -> List<%s> {\n", tableFn, caseType)
	b.WriteString("    [\n")
	for i := 1; i <= cases; i++ {
		fmt.Fprintf(&b, "        %s { name: \"case%d\", input: \"in%d\", expected: \"out%d\" },\n",
			caseType, i, i, i)
	}
	b.WriteString("    ]\n")
	b.WriteString("}\n\n")

	fmt.Fprintf(&b, "fn %s() {\n", testFn)
	fmt.Fprintf(&b, "    for c in %s() {\n", tableFn)
	b.WriteString("        let _ = c.name\n")
	b.WriteString("        let _ = c.input\n")
	b.WriteString("        let _ = c.expected\n")
	b.WriteString("    }\n")
	b.WriteString("}\n")

	return b.String(), nil
}

// WriteFixture renders the fixture and writes it to
// `<dir>/<name>_test.osty`. Like the rest of the scaffolder, it
// refuses to overwrite an existing file — call sites must remove the
// conflict explicitly.
func WriteFixture(dir string, opts FixtureOptions) (string, *diag.Diagnostic) {
	src, d := RenderFixture(opts)
	if d != nil {
		return "", d
	}
	abs, derr := filepath.Abs(dir)
	if derr != nil {
		return "", ioErr(dir, derr)
	}
	path := filepath.Join(abs, opts.Name+"_test.osty")
	if _, err := os.Stat(path); err == nil {
		return "", existsDiag(path)
	} else if !os.IsNotExist(err) {
		return "", ioErr(path, err)
	}
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		return "", ioErr(path, err)
	}
	return path, nil
}

// identForName lower-cases the first rune of name so it forms a valid
// camelCase Osty identifier. ValidateName already accepts both
// `myApp` and `MyApp`; the generator normalises to `myApp` so the
// generated symbol style matches the rest of the language conventions
// (spec §1.4).
func identForName(name string) string {
	if name == "" {
		return name
	}
	r := []rune(name)
	r[0] = unicode.ToLower(r[0])
	// Strip hyphens — fixture names like "my-thing" become `myThing`
	// in the source. Hyphens are valid as filenames but not as
	// identifiers, so we have to fold them out.
	out := make([]rune, 0, len(r))
	upper := false
	for _, c := range r {
		if c == '-' {
			upper = true
			continue
		}
		if upper {
			out = append(out, unicode.ToUpper(c))
			upper = false
			continue
		}
		out = append(out, c)
	}
	return string(out)
}

// titleCase upper-cases the first rune. Used to derive Type-style
// names from a value-style identifier.
func titleCase(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}
