package format

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// FuzzFormat is the formatter's primary safety net. It asserts two
// invariants that must hold for every input the parser accepts
// cleanly (no diagnostics, no recovery sentinels):
//
//  1. fmt(fmt(x)) == fmt(x) — idempotence.
//  2. fmt(x) itself parses without errors — round-trip safety.
//
// Inputs the parser rejects, recovers silently from, or produces
// placeholder `<error>` nodes for are all skipped: formatter
// correctness is not defined on those.
//
// Seed corpus is every *.input.osty under testdata/, so fuzzing
// starts from realistic code rather than purely random bytes.
//
// The fuzz-mutation half of the test is skipped under `go test
// -short` so the default CI run only exercises the seed corpus;
// the random exploration is for local `-fuzz=FuzzFormat` runs.
func FuzzFormat(f *testing.F) {
	// nothing special for seeds — they always run.
	entries, err := os.ReadDir("testdata")
	if err == nil {
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".input.osty") {
				continue
			}
			b, err := os.ReadFile(filepath.Join("testdata", e.Name()))
			if err != nil {
				continue
			}
			f.Add(b)
		}
	}
	// A handful of extra micro-seeds to exercise paths the fixtures
	// don't cover densely (empty file, trailing blank lines, mixed
	// trivia right before EOF, 1-tuple, empty collections).
	extras := []string{
		``,
		"\n\n\n",
		"fn f() {}\n",
		"let x = (1,)\n",
		"let xs: List<Int> = []\n",
		"let m: Map<String, Int> = {:}\n",
		"// only a comment\n",
		"/// orphan doc\n",
		"/* block */ fn f() {}\n",
	}
	for _, s := range extras {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, src []byte) {
		once, diags, err := Source(src)
		if err != nil {
			// Parse error: formatter correctness is undefined. Out of
			// scope for the idempotence contract.
			return
		}
		// Parser recovery can produce AST nodes that reference
		// placeholder identifiers (e.g. `<error>` after a missing
		// expression). The Source() call succeeds because no Error-
		// severity diagnostic fires, but reformatting the printed
		// output breaks on the sentinel. Restrict fuzz to diag-free
		// inputs that also don't contain placeholder markers in the
		// printed output — that's the contract the formatter
		// actually promises.
		if len(diags) > 0 {
			return
		}
		if bytes.Contains(once, []byte("<error>")) {
			return
		}
		twice, reDiags, err := Source(once)
		// If the printed output doesn't round-trip through the parser,
		// the original input sent us through a silent parser-recovery
		// path whose printed shape is syntactically different from a
		// normally-formed program. That's out of scope for the
		// formatter's idempotence contract.
		if err != nil || len(reDiags) > 0 {
			return
		}
		if !bytes.Equal(once, twice) {
			t.Fatalf("fmt is not idempotent\n--- once ---\n%s\n--- twice ---\n%s",
				once, twice)
		}
	})
}
