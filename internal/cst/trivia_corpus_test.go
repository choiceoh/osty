package cst_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/osty/osty/internal/cst"
	"github.com/osty/osty/internal/selfhost"
	"github.com/osty/osty/internal/token"
)

// corpus mirrors the parser snapshot oracle so byte-coverage and round-trip
// are verified on the same set of inputs the parser snapshot locks.
var corpus = []string{
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

// TestByteCoverage asserts the Phase 1 invariant: tokens ∪ trivias partition
// the normalized source. Every byte is covered exactly once.
func TestByteCoverage(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, rel := range corpus {
		rel := rel
		t.Run(rel, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(root, rel))
			if err != nil {
				t.Skipf("corpus missing: %v", err)
				return
			}
			src := cst.Normalize(raw)
			toks, _, _ := selfhost.Lex(src)
			trivias := cst.Extract(src, toks)
			if err := checkByteCoverage(src, toks, trivias); err != nil {
				t.Fatalf("%s: %v", rel, err)
			}
		})
	}
}

// TestRoundTrip asserts that tokens ∪ trivias can be ordered and concatenated
// to reconstruct the normalized source exactly.
func TestRoundTrip(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, rel := range corpus {
		rel := rel
		t.Run(rel, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(root, rel))
			if err != nil {
				t.Skipf("corpus missing: %v", err)
				return
			}
			src := cst.Normalize(raw)
			toks, _, _ := selfhost.Lex(src)
			trivias := cst.Extract(src, toks)
			rebuilt := reconstruct(src, toks, trivias)
			if string(rebuilt) != string(src) {
				pos := firstDiff(src, rebuilt)
				t.Fatalf("%s: round-trip mismatch at byte %d\nwant: %q\n got: %q",
					rel, pos, preview(src, pos), preview(rebuilt, pos))
			}
		})
	}
}

// TestNormalize exercises CRLF / CR collapse on synthetic inputs.
func TestNormalize(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"abc", "abc"},
		{"a\nb", "a\nb"},
		{"a\r\nb", "a\nb"},
		{"a\rb", "a\nb"},
		{"\r\n\r\n", "\n\n"},
		{"mixed\r\nis\rok", "mixed\nis\nok"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			got := string(cst.Normalize([]byte(tc.in)))
			if got != tc.want {
				t.Fatalf("Normalize(%q) = %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}

// checkByteCoverage walks all tokens and trivias, asserts each source byte is
// covered by exactly one of them.
func checkByteCoverage(src []byte, toks []token.Token, trivias []cst.Trivia) error {
	type span struct {
		lo, hi int
		label  string
	}
	spans := make([]span, 0, len(toks)+len(trivias))
	for _, tr := range trivias {
		if tr.Length <= 0 {
			continue
		}
		spans = append(spans, span{tr.Offset, tr.Offset + tr.Length, "trivia:" + tr.Kind.String()})
	}
	for _, tk := range toks {
		if tk.Kind == token.EOF {
			continue
		}
		lo, hi := tk.Pos.Offset, tk.End.Offset
		if hi <= lo {
			continue
		}
		spans = append(spans, span{lo, hi, "token:" + tk.Kind.String()})
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].lo != spans[j].lo {
			return spans[i].lo < spans[j].lo
		}
		return spans[i].hi < spans[j].hi
	})
	pos := 0
	for _, s := range spans {
		if s.lo < pos {
			return fmt.Errorf("overlap at byte %d: %s covers [%d,%d) but previous span ended at %d", s.lo, s.label, s.lo, s.hi, pos)
		}
		if s.lo > pos {
			return fmt.Errorf("gap at bytes [%d,%d): next span %s starts at %d; uncovered text: %q", pos, s.lo, s.label, s.lo, preview(src, pos))
		}
		pos = s.hi
	}
	if pos != len(src) {
		return fmt.Errorf("tail uncovered: stopped at %d, source length %d; tail: %q", pos, len(src), preview(src, pos))
	}
	return nil
}

// reconstruct orders all source slices and concatenates them.
func reconstruct(src []byte, toks []token.Token, trivias []cst.Trivia) []byte {
	type piece struct {
		lo, hi int
	}
	pieces := make([]piece, 0, len(toks)+len(trivias))
	for _, tr := range trivias {
		if tr.Length <= 0 {
			continue
		}
		pieces = append(pieces, piece{tr.Offset, tr.Offset + tr.Length})
	}
	for _, tk := range toks {
		if tk.Kind == token.EOF {
			continue
		}
		if tk.End.Offset <= tk.Pos.Offset {
			continue
		}
		pieces = append(pieces, piece{tk.Pos.Offset, tk.End.Offset})
	}
	sort.Slice(pieces, func(i, j int) bool { return pieces[i].lo < pieces[j].lo })
	out := make([]byte, 0, len(src))
	for _, p := range pieces {
		if p.lo >= len(src) {
			continue
		}
		hi := p.hi
		if hi > len(src) {
			hi = len(src)
		}
		out = append(out, src[p.lo:hi]...)
	}
	return out
}

func firstDiff(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return n
	}
	return -1
}

func preview(src []byte, start int) string {
	const span = 20
	if start < 0 {
		start = 0
	}
	if start >= len(src) {
		return ""
	}
	end := start + span
	if end > len(src) {
		end = len(src)
	}
	return string(src[start:end])
}
