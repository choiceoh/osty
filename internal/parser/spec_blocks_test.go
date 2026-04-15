package parser

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestSpecCodeBlocks walks every ```osty code block in the spec markdown
// files and attempts to parse each one. As of v0.3 the language spec is a
// directory of per-section markdown files (LANG_SPEC_v0.3/), so the test
// recursively walks that directory in addition to OSTY_GRAMMAR_v0.3.md and
// SPEC_GAPS.md.
//
// The test does not fail when an individual block fails to parse — many
// spec blocks contain pseudo-output (e.g. assertion diff format), pure
// type signatures without wrappers, or shape-illustrative fragments.
// Instead it logs a coverage ratio and only fails when the *parseable*
// fraction regresses below a floor.
//
// This guards against silent regressions when the parser changes while
// remaining tolerant of the spec's mixed prose+example style.
func TestSpecCodeBlocks(t *testing.T) {
	root := findRepoRoot(t)

	// Collect candidate markdown files: every .md inside LANG_SPEC_v0.3/
	// (the current split spec), plus the standalone companion files at
	// the repo root, plus legacy single-file names (still picked up if
	// present).
	var paths []string
	for _, dir := range []string{"LANG_SPEC_v0.3", "LANG_SPEC_v0.2"} {
		_ = filepath.WalkDir(filepath.Join(root, dir), func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				return nil
			}
			if strings.HasSuffix(p, ".md") {
				paths = append(paths, p)
			}
			return nil
		})
	}
	for _, name := range []string{
		"OSTY_GRAMMAR_v0.3.md",
		"OSTY_GRAMMAR_v0.2.md", // legacy
		"SPEC_GAPS.md",
		"LANG_SPEC_v0.2.md", // legacy single-file name
		"LANG_SPEC_v0.1.md", // legacy single-file name
		"LANG_SPEC.md",      // legacy single-file name
	} {
		paths = append(paths, filepath.Join(root, name))
	}

	var allBlocks []ostyBlock
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		// Build a label that is short but unambiguous.
		label, err := filepath.Rel(root, path)
		if err != nil {
			label = filepath.Base(path)
		}
		for _, blk := range extractOstyBlocks(string(b)) {
			blk.label = label + ":" + blk.label
			allBlocks = append(allBlocks, blk)
		}
	}
	if len(allBlocks) == 0 {
		t.Skip("no spec markdown files found")
	}

	var parsed, total int
	var failures []string
	for _, blk := range allBlocks {
		if blk.skip {
			continue
		}
		total++
		// Attempt 1: parse as a whole file.
		if _, errs := Parse([]byte(blk.body)); len(errs) == 0 {
			parsed++
			continue
		}
		// Attempt 2: wrap as a function body.
		wrapped := "fn __ostyTestWrap() {\n" + blk.body + "\n}\n"
		if _, errs := Parse([]byte(wrapped)); len(errs) == 0 {
			parsed++
			continue
		}
		failures = append(failures, blk.label)
	}

	ratio := float64(parsed) / float64(total)
	t.Logf("spec code blocks: %d/%d parsed cleanly (%.1f%%)", parsed, total, ratio*100)
	// Floor is intentionally generous because the spec includes lots of
	// pseudo-output blocks. Bump this if it should be tightened.
	const minRatio = 0.50
	if ratio < minRatio {
		t.Errorf("spec block parse ratio %.1f%% below floor %.1f%%; first failures: %v",
			ratio*100, minRatio*100, firstN(failures, 10))
	}
}

func firstN(xs []string, n int) []string {
	if len(xs) <= n {
		return xs
	}
	return xs[:n]
}

type ostyBlock struct {
	label    string // "line-N"
	line     int    // 1-based source line of the fence
	body     string
	skip     bool // marked osty-ignore
	topLevel bool // always parse at file scope
}

var fenceRE = regexp.MustCompile("(?m)^```(osty[\\w-]*)\\s*$")

func extractOstyBlocks(src string) []ostyBlock {
	var out []ostyBlock
	lines := strings.Split(src, "\n")
	for i := 0; i < len(lines); i++ {
		m := fenceRE.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		flag := m[1]
		// Find closing fence.
		start := i + 1
		end := -1
		for j := start; j < len(lines); j++ {
			if strings.TrimSpace(lines[j]) == "```" {
				end = j
				break
			}
		}
		if end < 0 {
			break
		}
		body := strings.Join(lines[start:end], "\n")
		b := ostyBlock{
			label: stringsJoin("line-", start+1),
			line:  start + 1,
			body:  body,
		}
		switch flag {
		case "osty-ignore":
			b.skip = true
		case "osty-toplevel":
			b.topLevel = true
		}
		out = append(out, b)
		i = end
	}
	return out
}

func stringsJoin(a string, n int) string {
	// Tiny helper to avoid importing strconv just here.
	const digits = "0123456789"
	if n == 0 {
		return a + "0"
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = digits[n%10]
		n /= 10
	}
	return a + string(buf[pos:])
}
