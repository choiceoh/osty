package speccorpus_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/pipeline"
)

// positiveWaivers documents spec fixtures where the current front-end
// emits diagnostics that have not yet been reconciled with the spec.
// Each entry is keyed by the filename (basename) and lists every
// error-severity code (and "" for codeless errors) the parser is
// expected to emit today. The test fails if a waived file produces a
// different set of codes — either because a gap was closed (shrink or
// drop the waiver) or because a new regression appeared.
//
// Empty waiver list = the file must parse with zero errors.
var positiveWaivers = map[string][]string{}

// negativeWaivers documents CASE blocks in testdata/spec/negative/
// reject.osty where the front-end currently does NOT emit the declared
// Exxxx code — typically because the check that would produce it lives
// in a pass that hasn't reached this construct yet, or a different
// error recovers before the intended check fires.
//
// Key: "<Code>/<Hint>" where hint is the text after `===` in the CASE
// header. Value: the set of codes the pipeline currently produces, with
// severity prefix (e.g. "error:E0030", "warning:L0003").
//
// Each entry is a live gap — when the compiler starts emitting the
// declared code the test will fail, prompting waiver removal. This map
// is intentionally churn-friendly: adding or deleting entries is the
// normal way to track Exxxx migration work.
var negativeWaivers = map[string][]string{}

// TestSpecPositive enforces that every file under
// testdata/spec/positive/ parses with the expected diagnostic set. New
// files are picked up via filesystem discovery.
func TestSpecPositive(t *testing.T) {
	root := findRepoRoot(t)
	dir := filepath.Join(root, "testdata", "spec", "positive")
	entries, err := filepath.Glob(filepath.Join(dir, "*.osty"))
	if err != nil {
		t.Fatalf("glob positive corpus: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("no positive corpus files discovered under %s", dir)
	}
	sort.Strings(entries)

	seen := make(map[string]bool, len(entries))
	// Wrap the parallel per-file subtests in a non-parallel group so
	// `waivers_are_live` below only runs after every file has been
	// checked (Go waits for a parent's parallel children to finish
	// before moving to the next sibling).
	t.Run("cases", func(t *testing.T) {
		for _, path := range entries {
			base := filepath.Base(path)
			seen[base] = true
			t.Run(base, func(t *testing.T) {
				t.Parallel()
				src, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("read %s: %v", path, err)
				}
				_, diags := parser.ParseDiagnostics(src)
				got := errorCodes(diags)

				if want, isWaived := positiveWaivers[base]; isWaived {
					if !codesMatch(got, want) {
						t.Errorf("%s: waived error set drifted\n  want: %v\n   got: %v\n"+
							"If the gap closed, shrink the entry in positiveWaivers.",
							base, want, got)
					}
					return
				}

				if len(got) == 0 {
					return
				}
				var details strings.Builder
				for _, d := range diags {
					if d.Severity != diag.Error {
						continue
					}
					fmt.Fprintf(&details, "\n  %s %q at %s",
						codeOrDash(d.Code), d.Message, primarySpanLoc(d))
				}
				t.Errorf("%s: %d parser error(s) — positive corpus must parse clean.%s\n"+
					"If this is a known gap, add %q to positiveWaivers with a comment.",
					base, len(got), details.String(), base)
			})
		}
	})

	t.Run("waivers_are_live", func(t *testing.T) {
		for name := range positiveWaivers {
			if !seen[name] {
				t.Errorf("positiveWaivers entry %q has no matching file under %s — "+
					"remove the stale waiver", name, dir)
			}
		}
	})
}

// TestSpecNegative splits testdata/spec/negative/reject.osty into CASE
// blocks and asserts each block emits at least one diagnostic with the
// declared error code. This is the contract documented in CLAUDE.md
// ("`// === CASE: Exxxx ===` 블록별로 해당 코드 발화") which previously
// was only an unchecked convention.
func TestSpecNegative(t *testing.T) {
	root := findRepoRoot(t)
	path := filepath.Join(root, "testdata", "spec", "negative", "reject.osty")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	cases := splitCases(raw)
	if len(cases) == 0 {
		t.Fatal("no `// === CASE: ===` blocks found in reject.osty")
	}

	var seenMu sync.Mutex
	seenWaivers := make(map[string]bool, len(negativeWaivers))
	t.Run("cases", func(t *testing.T) {
		for _, c := range cases {
			t.Run(c.testName(), func(t *testing.T) {
				t.Parallel()
				// Run the full front-end (lex → parse → resolve → check
				// → lint) because most Exxxx codes are surfaced by
				// resolve or check, not the parser alone.
				r := pipeline.Run(c.Source, nil)
				for _, d := range r.AllDiags {
					if d.Code == c.Code {
						return
					}
				}

				got := diagCodes(r.AllDiags, false)
				key := c.waiverKey()
				if want, ok := negativeWaivers[key]; ok {
					seenMu.Lock()
					seenWaivers[key] = true
					seenMu.Unlock()
					if !codesMatch(got, want) {
						t.Errorf("case at line %d (%s / %q): waived code set drifted\n"+
							"  want: %v\n   got: %v\n"+
							"If the gap closed, drop this entry from negativeWaivers.",
							c.LineStart, c.Code, c.Hint, want, got)
					}
					return
				}
				t.Errorf("case at line %d (%q) did not emit %s; produced codes %v\n"+
					"If this is a known gap, add %q to negativeWaivers with a comment.",
					c.LineStart, c.Hint, c.Code, got, key)
			})
		}
	})

	t.Run("waivers_are_live", func(t *testing.T) {
		for key := range negativeWaivers {
			if !seenWaivers[key] {
				t.Errorf("negativeWaivers entry %q has no matching CASE block in reject.osty — "+
					"remove the stale waiver (or fix its key)", key)
			}
		}
	})
}

// ---- helpers ----

type corpusCase struct {
	Code      string
	Hint      string
	LineStart int // 1-based line of first body byte inside reject.osty
	Source    []byte
}

func (c corpusCase) testName() string {
	hint := sanitize(c.Hint)
	if hint == "" {
		return fmt.Sprintf("%s_L%d", c.Code, c.LineStart)
	}
	return fmt.Sprintf("%s_L%d_%s", c.Code, c.LineStart, hint)
}

// waiverKey is the negativeWaivers map key. It intentionally does NOT
// include LineStart so reordering reject.osty blocks doesn't churn the
// waiver list; (Code, Hint) is stable across edits and the hint is
// usually unique per case.
func (c corpusCase) waiverKey() string {
	return c.Code + "/" + c.Hint
}

// caseHeaderRe intentionally matches only `E\d{4}` codes — the negative
// corpus is an error-code contract. Warnings/lints have their own
// coverage and should not live as CASE blocks here.
var caseHeaderRe = regexp.MustCompile(`^// === CASE: (E\d{4}) ===\s*(.*?)\s*$`)

// splitCases parses the negative corpus into individual CASE blocks. A
// body runs until the next `// === END`, the next CASE header, or EOF
// (the final case deliberately omits END — its body is an unterminated
// block comment that swallows everything after it).
func splitCases(raw []byte) []corpusCase {
	lines := strings.Split(string(raw), "\n")
	var cases []corpusCase
	for i := 0; i < len(lines); i++ {
		m := caseHeaderRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		bodyStart := i + 1
		j := bodyStart
		for j < len(lines) {
			if strings.HasPrefix(strings.TrimSpace(lines[j]), "// === END") {
				break
			}
			if caseHeaderRe.MatchString(lines[j]) {
				break
			}
			j++
		}
		cases = append(cases, corpusCase{
			Code:      m[1],
			Hint:      strings.TrimSpace(m[2]),
			LineStart: bodyStart + 1,
			Source:    []byte(strings.Join(lines[bodyStart:j], "\n") + "\n"),
		})
		// Skip past the body so the outer scan doesn't re-match its
		// lines as CASE headers.
		if j < len(lines) {
			i = j
		} else {
			i = j - 1
		}
	}
	return cases
}

// diagCodes renders a diagnostic slice as string tags for comparison
// and display. If errorsOnly is true the result is bare codes filtered
// to Error severity (positive corpus: the waiver value-set); otherwise
// every diag is tagged with its severity (negative corpus: the drift
// report includes warnings that explain why Exxxx wasn't reached).
func diagCodes(ds []*diag.Diagnostic, errorsOnly bool) []string {
	out := make([]string, 0, len(ds))
	for _, d := range ds {
		if errorsOnly {
			if d.Severity != diag.Error {
				continue
			}
			out = append(out, d.Code)
			continue
		}
		out = append(out, fmt.Sprintf("%s:%s", d.Severity, codeOrDash(d.Code)))
	}
	return out
}

func errorCodes(ds []*diag.Diagnostic) []string { return diagCodes(ds, true) }

func codesMatch(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	g := append([]string(nil), got...)
	w := append([]string(nil), want...)
	sort.Strings(g)
	sort.Strings(w)
	for i := range g {
		if g[i] != w[i] {
			return false
		}
	}
	return true
}

func codeOrDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// primarySpanLoc is named to avoid colliding with lsp.primarySpan,
// which returns a *diag.Span rather than a formatted location.
func primarySpanLoc(d *diag.Diagnostic) string {
	for _, s := range d.Spans {
		if s.Primary {
			return fmt.Sprintf("%d:%d", s.Span.Start.Line, s.Span.Start.Column)
		}
	}
	return "?"
}

var sanitizeRe = regexp.MustCompile(`[^A-Za-z0-9]+`)

func sanitize(s string) string {
	s = sanitizeRe.ReplaceAllString(s, "_")
	s = strings.Trim(s, "_")
	if len(s) > 40 {
		s = s[:40]
		s = strings.TrimRight(s, "_")
	}
	return s
}

// findRepoRoot walks upward from the test's working directory to the
// nearest go.mod. Keeps the driver agnostic of its own package path.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	cur := wd
	for {
		if _, err := os.Stat(filepath.Join(cur, "go.mod")); err == nil {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			t.Fatalf("no go.mod ancestor from %s", wd)
		}
		cur = parent
	}
}
