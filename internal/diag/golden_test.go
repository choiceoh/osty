package diag

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/token"
)

// -update rewrites the golden files with the current output. Use
// `go test ./internal/diag/ -run TestGolden -update` when you
// intentionally changed the rendering format.
var updateGolden = flag.Bool("update", false, "rewrite golden snapshots")

// TestGoldenSnapshots exercises the formatter against a small fixed set
// of diagnostic shapes and compares the output to files in testdata/golden.
// Catches silent regressions in the rendering format.
func TestGoldenSnapshots(t *testing.T) {
	src := []byte(`fn broken() {
    let x = unknown
}
fn other() {
    x + y
}
`)

	cases := []struct {
		name string
		diag *Diagnostic
	}{
		{
			name: "basic",
			diag: New(Error, "undefined name `unknown`").
				Code("E0500").
				Primary(Span{
					Start: token.Pos{Offset: 26, Line: 2, Column: 13},
					End:   token.Pos{Offset: 33, Line: 2, Column: 20},
				}, "not in scope").
				Hint("did you mean `unknownField`?").
				Build(),
		},
		{
			name: "with_note",
			diag: New(Error, "expected `}`, got `else`").
				Code("E0105").
				Primary(Span{
					Start: token.Pos{Line: 5, Column: 5},
					End:   token.Pos{Line: 5, Column: 9},
				}, "orphaned `else` here").
				Note("the `if` block was already closed on the previous line").
				Hint("move `else` onto the same line as `}`").
				Build(),
		},
		{
			name: "dup_with_secondary",
			diag: New(Error, "`foo` is already defined").
				Code("E0501").
				Primary(Span{
					Start: token.Pos{Line: 4, Column: 1},
					End:   token.Pos{Line: 4, Column: 10},
				}, "duplicate declaration").
				Secondary(Span{
					Start: token.Pos{Line: 1, Column: 1},
					End:   token.Pos{Line: 1, Column: 10},
				}, "previous declaration here").
				Hint("rename or remove one").
				Build(),
		},
		{
			name: "no_source",
			diag: New(Warning, "deprecated API").
				Code("E0400").
				Primary(Span{
					Start: token.Pos{Line: 10, Column: 5},
					End:   token.Pos{Line: 10, Column: 15},
				}, "").
				Build(),
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			f := &Formatter{Filename: "test.osty", Source: src, Color: false}
			if c.name == "no_source" {
				f.Source = nil
			}
			got := f.Format(c.diag)
			goldenPath := filepath.Join("testdata", "golden", c.name+".txt")

			if *updateGolden {
				if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}

			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden %s: %v (run with -update to create)", goldenPath, err)
			}
			if string(want) != got {
				t.Errorf("golden mismatch for %s\n--- want ---\n%s--- got ---\n%s",
					c.name, indent(string(want)), indent(got))
			}
		})
	}
}

func indent(s string) string {
	var out strings.Builder
	for _, line := range strings.Split(s, "\n") {
		out.WriteString("    ")
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.String()
}
