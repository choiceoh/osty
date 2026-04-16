// Baseline parser oracle.
//
// This test serializes the output of the current self-hosted parser for a
// representative corpus and compares it against checked-in golden files. Any
// change to the parser or lowering that would perturb the AST or diagnostics
// shows up as a diff in `testdata/parse_snapshots/`.
//
// This exists to lock present behavior before the Red/Green tree refactor so
// the shadow-mode parser rewrite (Phase 5) has a precise definition of
// "equivalent".
//
// Running:
//   go test ./internal/selfhost/... -run TestParseSnapshot
//   UPDATE_SNAPSHOT=1 go test ./internal/selfhost/... -run TestParseSnapshot
//
// Update only after a deliberate, reviewed behavior change.

package selfhost

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/osty/osty/internal/diag"
)

// corpus lists the source files the oracle covers. Paths are relative to the
// repo root and resolved via ../../ from the selfhost package directory.
var snapshotCorpus = []string{
	"testdata/spec/positive/01-lexical.osty",
	"testdata/spec/positive/02-types.osty",
	"testdata/spec/positive/03-declarations.osty",
	"testdata/spec/positive/04-expressions.osty",
	"testdata/spec/positive/05-modules.osty",
	"testdata/spec/positive/06-scripts.osty",
	"testdata/spec/positive/07-errors.osty",
	"testdata/spec/positive/08-concurrency.osty",
	"testdata/spec/positive/11-testing.osty",
	"testdata/spec/negative/reject.osty",
	"testdata/full.osty",
	"testdata/hello.osty",
	"testdata/resolve_ok.osty",
	"word_freq.osty",
	"word_freq_test.osty",
}

func TestParseSnapshot(t *testing.T) {
	update := os.Getenv("UPDATE_SNAPSHOT") == "1"
	repoRoot := filepath.Join("..", "..")
	snapshotDir := filepath.Join("testdata", "parse_snapshots")

	if update {
		if err := os.MkdirAll(snapshotDir, 0o755); err != nil {
			t.Fatalf("mkdir snapshots: %v", err)
		}
	}

	for _, rel := range snapshotCorpus {
		rel := rel
		t.Run(snapshotName(rel), func(t *testing.T) {
			srcPath := filepath.Join(repoRoot, rel)
			src, err := os.ReadFile(srcPath)
			if err != nil {
				t.Skipf("corpus file missing: %v", err)
				return
			}

			file, diags := Parse(src)
			got := dumpParseResult(file, diags)

			goldenPath := filepath.Join(snapshotDir, snapshotName(rel)+".txt")
			if update {
				if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}

			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden (set UPDATE_SNAPSHOT=1 to seed): %v", err)
			}
			if string(want) != got {
				t.Fatalf("parse snapshot drift for %s\nfirst diff line: %s\n(re-run with UPDATE_SNAPSHOT=1 after a deliberate behavior change)", rel, firstDiffLine(string(want), got))
			}
		})
	}
}

// TestParseSnapshotDeterministic guards against non-determinism in the
// dumper. Any drift here means the serializer itself is broken; the parser
// snapshot test cannot be trusted.
func TestParseSnapshotDeterministic(t *testing.T) {
	srcPath := filepath.Join("..", "..", "testdata", "spec", "positive", "01-lexical.osty")
	src, err := os.ReadFile(srcPath)
	if err != nil {
		t.Skipf("corpus missing: %v", err)
	}
	file1, d1 := Parse(src)
	file2, d2 := Parse(src)
	a := dumpParseResult(file1, d1)
	b := dumpParseResult(file2, d2)
	if a != b {
		t.Fatalf("parse dumper is non-deterministic; first diff: %s", firstDiffLine(a, b))
	}
}

// snapshotName converts a corpus-relative path to a stable snapshot filename.
func snapshotName(rel string) string {
	name := strings.ReplaceAll(rel, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.TrimSuffix(name, ".osty")
	return name
}

func firstDiffLine(want, got string) string {
	wl := strings.Split(want, "\n")
	gl := strings.Split(got, "\n")
	n := len(wl)
	if len(gl) < n {
		n = len(gl)
	}
	for i := 0; i < n; i++ {
		if wl[i] != gl[i] {
			return fmt.Sprintf("line %d:\n  want: %s\n  got:  %s", i+1, wl[i], gl[i])
		}
	}
	if len(wl) != len(gl) {
		return fmt.Sprintf("length diff: want=%d got=%d", len(wl), len(gl))
	}
	return "<identical>"
}

// dumpParseResult renders a *ast.File and its diagnostics deterministically.
// The output is plain text so drifts show up as line diffs in the golden file.
func dumpParseResult(file any, diags []*diag.Diagnostic) string {
	var b strings.Builder
	b.WriteString("===ast===\n")
	dumpValue(&b, reflect.ValueOf(file), 0)
	b.WriteString("\n===diagnostics===\n")
	dumpDiagnostics(&b, diags)
	return b.String()
}

func dumpDiagnostics(b *strings.Builder, diags []*diag.Diagnostic) {
	sorted := append([]*diag.Diagnostic(nil), diags...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		ao, bo := -1, -1
		for _, s := range a.Spans {
			if s.Primary {
				ao = s.Span.Start.Offset
				break
			}
		}
		for _, s := range b.Spans {
			if s.Primary {
				bo = s.Span.Start.Offset
				break
			}
		}
		if ao != bo {
			return ao < bo
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		return a.Message < b.Message
	})
	for _, d := range sorted {
		fmt.Fprintf(b, "%s %s %q", d.Severity, d.Code, d.Message)
		for _, s := range d.Spans {
			marker := "-"
			if s.Primary {
				marker = "^"
			}
			fmt.Fprintf(b, " %s[%d:%d-%d:%d]",
				marker,
				s.Span.Start.Line, s.Span.Start.Column,
				s.Span.End.Line, s.Span.End.Column,
			)
			if s.Label != "" {
				fmt.Fprintf(b, "=%q", s.Label)
			}
		}
		if d.Hint != "" {
			fmt.Fprintf(b, " hint=%q", d.Hint)
		}
		for _, n := range d.Notes {
			fmt.Fprintf(b, " note=%q", n)
		}
		b.WriteByte('\n')
	}
}

// dumpValue writes a reflect.Value in a stable, indented textual form. Key
// determinism properties:
//   - struct fields are written in declaration order (always stable),
//   - slices print length + per-element content,
//   - maps are sorted by key string form,
//   - pointers dereference silently (no addresses),
//   - nil interfaces / nil pointers render as "<nil>".
//
// The depth is capped to defend against accidental cycles; the AST has no
// cycles so this is belt-and-suspenders.
func dumpValue(b *strings.Builder, v reflect.Value, depth int) {
	if depth > 256 {
		b.WriteString("<depth-cap>")
		return
	}
	if !v.IsValid() {
		b.WriteString("<invalid>")
		return
	}
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			b.WriteString("<nil>")
			return
		}
		dumpValue(b, v.Elem(), depth)
	case reflect.Interface:
		if v.IsNil() {
			b.WriteString("<nil>")
			return
		}
		elem := v.Elem()
		fmt.Fprintf(b, "(%s)", typeLabel(elem.Type()))
		dumpValue(b, elem, depth)
	case reflect.Struct:
		fmt.Fprintf(b, "%s{", typeLabel(v.Type()))
		t := v.Type()
		first := true
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			if !first {
				b.WriteString(", ")
			}
			first = false
			b.WriteString(f.Name)
			b.WriteString(":")
			dumpValue(b, v.Field(i), depth+1)
		}
		b.WriteString("}")
	case reflect.Slice:
		if v.IsNil() {
			b.WriteString("[]")
			return
		}
		fmt.Fprintf(b, "[%d:", v.Len())
		for i := 0; i < v.Len(); i++ {
			if i > 0 {
				b.WriteString(", ")
			}
			dumpValue(b, v.Index(i), depth+1)
		}
		b.WriteString("]")
	case reflect.Array:
		fmt.Fprintf(b, "[%d:", v.Len())
		for i := 0; i < v.Len(); i++ {
			if i > 0 {
				b.WriteString(", ")
			}
			dumpValue(b, v.Index(i), depth+1)
		}
		b.WriteString("]")
	case reflect.Map:
		if v.IsNil() || v.Len() == 0 {
			b.WriteString("{}")
			return
		}
		keys := v.MapKeys()
		sort.Slice(keys, func(i, j int) bool {
			return fmt.Sprintf("%v", keys[i].Interface()) < fmt.Sprintf("%v", keys[j].Interface())
		})
		b.WriteString("{")
		for i, k := range keys {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(b, "%v:", k.Interface())
			dumpValue(b, v.MapIndex(k), depth+1)
		}
		b.WriteString("}")
	case reflect.String:
		fmt.Fprintf(b, "%q", v.String())
	case reflect.Bool:
		fmt.Fprintf(b, "%t", v.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		fmt.Fprintf(b, "%d", v.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		fmt.Fprintf(b, "%d", v.Uint())
	case reflect.Float32, reflect.Float64:
		fmt.Fprintf(b, "%g", v.Float())
	case reflect.Chan, reflect.Func, reflect.UnsafePointer:
		// not expected in AST; render opaquely if ever encountered
		fmt.Fprintf(b, "<%s>", v.Kind())
	default:
		fmt.Fprintf(b, "%v", v.Interface())
	}
}

// typeLabel returns a short, stable name for a type. For named types it is
// the bare type name; for slices / maps it prints their element kinds so
// snapshots distinguish e.g. []Decl from []Stmt.
func typeLabel(t reflect.Type) string {
	if t == nil {
		return "<nil-type>"
	}
	if t.Name() != "" {
		return t.Name()
	}
	switch t.Kind() {
	case reflect.Pointer:
		return "*" + typeLabel(t.Elem())
	case reflect.Slice:
		return "[]" + typeLabel(t.Elem())
	case reflect.Array:
		return fmt.Sprintf("[%d]%s", t.Len(), typeLabel(t.Elem()))
	case reflect.Map:
		return "map[" + typeLabel(t.Key()) + "]" + typeLabel(t.Elem())
	}
	return t.String()
}
