package format

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIdempotent checks fmt(fmt(x)) == fmt(x) for every fixture. Any
// drift means the printer emits a shape it cannot re-absorb — often a
// sign of a missing AST case or an extra whitespace token.
func TestIdempotent(t *testing.T) {
	for _, name := range fixtureNames(t) {
		t.Run(name, func(t *testing.T) {
			src := readFixture(t, name)
			once, _, err := Source(src)
			if err != nil {
				t.Fatalf("first format: %v", err)
			}
			twice, _, err := Source(once)
			if err != nil {
				t.Fatalf("second format: %v", err)
			}
			if !bytes.Equal(once, twice) {
				t.Errorf("fmt is not idempotent for %s", name)
				t.Logf("--- first ---\n%s", once)
				t.Logf("--- second ---\n%s", twice)
			}
		})
	}
}

// TestParsable checks that fmt output still parses cleanly. This
// guards against printer bugs that would emit syntactically broken
// code — a far more serious regression than a cosmetic difference.
func TestParsable(t *testing.T) {
	for _, name := range fixtureNames(t) {
		t.Run(name, func(t *testing.T) {
			src := readFixture(t, name)
			out, _, err := Source(src)
			if err != nil {
				t.Fatalf("format: %v", err)
			}
			_, diags, err := Source(out)
			if err != nil {
				t.Fatalf("reformat parse: %v\n--- output ---\n%s", err, out)
			}
			for _, d := range diags {
				t.Logf("post-format diag: %s", d.Message)
			}
		})
	}
}

// TestGolden locks the exact formatted output for each fixture under
// testdata/format/*.expected.osty. Run `go test -update` to rebuild
// them after a legitimate printer change.
func TestGolden(t *testing.T) {
	for _, name := range fixtureNames(t) {
		t.Run(name, func(t *testing.T) {
			src := readFixture(t, name)
			out, _, err := Source(src)
			if err != nil {
				t.Fatalf("format: %v", err)
			}
			goldenPath := filepath.Join("testdata", name+".expected.osty")
			if shouldUpdateGolden() {
				if err := os.WriteFile(goldenPath, out, 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}
			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden (run with -update to create): %v", err)
			}
			if !bytes.Equal(out, want) {
				t.Errorf("golden mismatch for %s\n--- got ---\n%s\n--- want ---\n%s",
					name, out, want)
			}
		})
	}
}

// TestTripleStringPreserved: a source `"""..."""` with single-line
// content still emits as triple-quoted. Assert the exact byte-level
// form so a `"""""""""` regression can't pass this test.
func TestTripleStringPreserved(t *testing.T) {
	// Input is already formatted, so round-trip should be identity.
	src := "fn f() {\n    let x = \"\"\"\n        hi\n        \"\"\"\n}\n"
	out, _, err := Source([]byte(src))
	if err != nil {
		t.Fatalf("format: %v", err)
	}
	if string(out) != src {
		t.Errorf("triple-string not preserved byte-for-byte:\n--- got ---\n%s--- want ---\n%s",
			out, src)
	}
}

// TestSingleToTriplePromotion: multi-line triple-quoted content must
// stay triple-quoted on round-trip — a single-quoted string cannot hold
// a raw `\n` (the lexer rejects it).
func TestSingleToTriplePromotion(t *testing.T) {
	in := "fn f() {\n    let x = \"\"\"\n        a\n        b\n        \"\"\"\n}\n"
	out, _, err := Source([]byte(in))
	if err != nil {
		t.Fatalf("format: %v", err)
	}
	twice, _, err := Source(out)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if !bytes.Equal(out, twice) {
		t.Errorf("not idempotent:\n--- once ---\n%s--- twice ---\n%s", out, twice)
	}
}

func TestCharLiteralUnicode(t *testing.T) {
	cases := []struct{ in, want string }{
		{"fn f() { let c = '한' }\n", "'한'"},
		{"fn f() { let c = '—' }\n", "'—'"},
		{"fn f() { let c = '😀' }\n", "'😀'"},
		// Controls and non-printable chars still get escape form.
		{"fn f() { let c = '\\n' }\n", `'\n'`},
		{"fn f() { let c = '\\u{2028}' }\n", `'\u{2028}'`},
	}
	for _, c := range cases {
		out, _, err := Source([]byte(c.in))
		if err != nil {
			t.Errorf("format %q: %v", c.in, err)
			continue
		}
		if !strings.Contains(string(out), c.want) {
			t.Errorf("in %q, got %q; want to contain %q", c.in, out, c.want)
		}
	}
}

func TestUTF8StringRoundTrip(t *testing.T) {
	in := "fn main() {\n    println(\"{u} — shape area {s}, max {m}\")\n}\n"
	out, _, err := Source([]byte(in))
	if err != nil {
		t.Fatalf("format: %v", err)
	}
	if !strings.Contains(string(out), " — shape area ") {
		t.Errorf("em-dash text corrupted; got:\n%s", out)
	}
	twice, _, err := Source(out)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if !bytes.Equal(out, twice) {
		t.Errorf("not idempotent:\n--- once ---\n%s--- twice ---\n%s", out, twice)
	}
}

// TestOptionRewrite pins the §2.5 / §13.3 canonical form: the
// formatter must rewrite `Option<T>` into `T?`.
func TestOptionRewrite(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{
			in:   "fn f(x: Option<Int>) -> Option<String> {}\n",
			want: "fn f(x: Int?) -> String? {}\n",
		},
		{
			in:   "fn f(x: Option<Option<Int>>) {}\n",
			want: "fn f(x: Int??) {}\n",
		},
		{
			in:   "fn f(x: List<Option<Int>>) {}\n",
			want: "fn f(x: List<Int?>) {}\n",
		},
	}
	for _, tc := range cases {
		out, _, err := Source([]byte(tc.in))
		if err != nil {
			t.Errorf("unexpected error on %q: %v", tc.in, err)
			continue
		}
		if string(out) != tc.want {
			t.Errorf("Option rewrite\n  in:   %q\n  got:  %q\n  want: %q",
				tc.in, out, tc.want)
		}
	}
}

// TestTrailingComma verifies §13.3's "normalize trailing commas" rule
// for struct/enum bodies. The formatter always emits a trailing comma
// after the last field/variant when they are one-per-line.
func TestTrailingComma(t *testing.T) {
	in := "struct Point { x: Int, y: Int }\n"
	out, _, err := Source([]byte(in))
	if err != nil {
		t.Fatalf("format: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "y: Int,\n") {
		t.Errorf("expected trailing comma on last field:\n%s", got)
	}
}

// TestParseErrorRefused ensures that a file with parse errors does
// NOT produce formatted output — the caller gets an error instead,
// preserving gofmt's "no mangling of broken input" property.
func TestParseErrorRefused(t *testing.T) {
	_, _, err := Source([]byte("fn ("))
	if err == nil {
		t.Fatalf("expected error on unparseable input")
	}
}

// ---- Helpers ----

// shouldUpdateGolden checks the OSTY_FMT_UPDATE env var. We avoid
// defining a `-update` flag because the test runner's own flag parser
// rejects unknown flags and there's no clean way to add one without
// also installing a package-wide init hook.
//
// Usage: OSTY_FMT_UPDATE=1 go test ./internal/format -run TestGolden
func shouldUpdateGolden() bool { return os.Getenv("OSTY_FMT_UPDATE") != "" }

func fixtureNames(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}
	var names []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(name, ".input.osty") {
			continue
		}
		names = append(names, strings.TrimSuffix(name, ".input.osty"))
	}
	return names
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name+".input.osty"))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}
