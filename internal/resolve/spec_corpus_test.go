package resolve

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
)

// findRepoRoot walks up from the current working directory looking for
// go.mod, so fixture globs work whether `go test` is invoked from the
// repo root or from an internal package directory.
func findRepoRoot(t *testing.T) string {
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

// TestSpecPositiveCorpus parses + resolves every file under
// testdata/spec/positive/ and asserts the full pipeline produces zero
// diagnostics. Each fixture covers a single spec chapter; failures
// surface as "chapter N has a regression" signals during refactors.
func TestSpecPositiveCorpus(t *testing.T) {
	root := findRepoRoot(t)
	pattern := filepath.Join(root, "testdata", "spec", "positive", "*.osty")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("no positive spec fixtures found at %s", pattern)
	}
	for _, path := range matches {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			file, parseDiags := parser.ParseDiagnostics(src)
			res := File(file, NewPrelude())
			all := append(append([]*diag.Diagnostic{}, parseDiags...), res.Diags...)
			for _, d := range all {
				t.Errorf("unexpected diagnostic in %s:\n  %s",
					filepath.Base(path), d.Error())
			}
		})
	}
}

// caseRegexp matches section headers in the bundled negative corpus:
//
//	// === CASE: E0105 === <hint>
//
// The code between the `===` fences is the expected diagnostic code for
// the block that follows, up to the next `// === END` line.
var caseRegexp = regexp.MustCompile(`(?m)^// === CASE: (E\d{4}) ===[^\n]*\n(.*?)\n// === END`)

// TestSpecNegativeCorpus parses each `// === CASE: Exxxx === ...` block in
// testdata/spec/negative/reject.osty and asserts the pipeline emits at
// least one diagnostic with the declared code.
func TestSpecNegativeCorpus(t *testing.T) {
	root := findRepoRoot(t)
	path := filepath.Join(root, "testdata", "spec", "negative", "reject.osty")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skip(err)
	}
	src := string(raw)

	matches := caseRegexp.FindAllStringSubmatchIndex(src, -1)
	if len(matches) == 0 {
		t.Fatalf("no cases found in %s — regex mismatch?", path)
	}

	for _, m := range matches {
		code := src[m[2]:m[3]]
		body := src[m[4]:m[5]]
		// Line number of the body start, for error-report context.
		startLine := 1 + strings.Count(src[:m[4]], "\n")
		t.Run(code+"_line"+itoa(startLine), func(t *testing.T) {
			file, parseDiags := parser.ParseDiagnostics([]byte(body))
			res := File(file, NewPrelude())
			all := append(append([]*diag.Diagnostic{}, parseDiags...), res.Diags...)
			for _, d := range all {
				if d.Code == code {
					return
				}
			}
			// Report what we got instead.
			var got []string
			for _, d := range all {
				if d.Code != "" {
					got = append(got, d.Code+": "+d.Message)
				} else {
					got = append(got, "(no code): "+d.Message)
				}
			}
			t.Errorf("expected diagnostic with code %q for block at line %d\n"+
				"  body:\n%s\n"+
				"  actual diagnostics:\n    %s",
				code, startLine, indent(body, "    "),
				strings.Join(got, "\n    "))
		})
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func indent(s, prefix string) string {
	var out strings.Builder
	for _, line := range strings.Split(s, "\n") {
		out.WriteString(prefix)
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.String()
}
