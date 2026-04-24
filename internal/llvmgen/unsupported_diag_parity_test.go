package llvmgen

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// ostyEvaluatedHint applies the Osty string-literal escape rules that
// matter for the diagnostic-hint parity check: `\{` / `\}` collapse to
// literal `{` / `}` so an Osty source written with escaped braces (the
// only safe way to embed `{ ... }` in a non-interpolated message)
// compares equal to a Go snapshot that uses bare braces.
func ostyEvaluatedHint(raw string) string {
	r := strings.NewReplacer(`\{`, `{`, `\}`, `}`)
	return r.Replace(raw)
}

// TestUnsupportedDiagnosticOstySnapshotParity pins the kind → (code, hint)
// mapping agreement across the three places it lives:
//
//   - toolchain/llvmgen.osty              (declared source of truth; see
//     llvmgen.go header + toolchain/llvmgen.osty:1325 comment)
//   - support_snapshot.go                 (hand-maintained Go snapshot,
//     exported via UnsupportedDiagnosticFor)
//
// Without this gate a branch added to one file and forgotten in the
// other silently drifts. That is how LLVM018 (stdlib-body) ended up
// present in the Go snapshot but absent from toolchain/llvmgen.osty
// between commits 3b23c72 and the follow-up fix.
//
// Both branch shapes are covered:
//   - uniform call form: `llvmUnsupportedDiagnosticWith("LLVMNNN", ...)`
//     (LLVM010..LLVM018)
//   - field-literal form: `return LlvmUnsupportedDiagnostic { code:
//     "LLVMNNN", ..., hint: "..." }` (LLVM001 / LLVM002)
//
// LLVM000 (the implicit default branch at the tail of the function)
// has no `if kind == "..."` anchor so it is out of scope here; its
// code/hint contract is pinned by TestUnsupportedDiagnosticKindMapping
// + TestUnsupportedDiagnosticHintAnchors.
func TestUnsupportedDiagnosticOstySnapshotParity(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	sources := []string{
		"toolchain/llvmgen.osty",
		"toolchain/llvmgen.osty",
	}
	extracted := make(map[string]map[string][2]string) // rel path → kind → [code, hint]
	for _, rel := range sources {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		extracted[rel] = parseLlvmUnsupportedWithBranches(string(data))
		if len(extracted[rel]) == 0 {
			t.Fatalf("%s: parsed 0 llvmUnsupportedDiagnosticWith branches; parser regex is stale or the source was restructured", rel)
		}
	}
	kinds := map[string]struct{}{}
	for _, m := range extracted {
		for k := range m {
			kinds[k] = struct{}{}
		}
	}
	sortedKinds := make([]string, 0, len(kinds))
	for k := range kinds {
		sortedKinds = append(sortedKinds, k)
	}
	sort.Strings(sortedKinds)
	for _, kind := range sortedKinds {
		snap := UnsupportedDiagnosticFor(kind, "")
		for _, rel := range sources {
			br, ok := extracted[rel][kind]
			if !ok {
				t.Errorf("kind %q: missing from %s (Go snapshot maps it to %s)",
					kind, rel, snap.Code)
				continue
			}
			if br[0] != snap.Code {
				t.Errorf("kind %q: %s has code %q, Go snapshot has %q",
					kind, rel, br[0], snap.Code)
			}
			if ostyEvaluatedHint(br[1]) != snap.Hint {
				t.Errorf("kind %q: %s hint drift\n  osty:     %q\n  snapshot: %q",
					kind, rel, br[1], snap.Hint)
			}
		}
	}
}

var (
	ostyKindBranchRE      = regexp.MustCompile(`if kind == "([^"]+)"`)
	ostyUnsupportedCallRE = regexp.MustCompile(
		`llvmUnsupportedDiagnosticWith\(\s*"(LLVM\d{3})"\s*,\s*kind\s*,\s*detail\s*,\s*"((?:[^"\\]|\\.)*)"`,
	)
	ostyUnsupportedLiteralCodeRE = regexp.MustCompile(`code:\s*"(LLVM\d{3})"`)
	ostyUnsupportedLiteralHintRE = regexp.MustCompile(`hint:\s*"((?:[^"\\]|\\.)*)"`)
)

// parseLlvmUnsupportedWithBranches walks an Osty source and returns a
// kind → [code, hint] map for each `if kind == "..."` branch in
// llvmUnsupportedDiagnostic. Two branch shapes are recognized:
//
//   - uniform helper call:
//     return llvmUnsupportedDiagnosticWith("LLVMNNN", kind, detail, "hint")
//   - field-literal return (LLVM001 / LLVM002, which decorate the kind
//     they emit so they can't reuse the helper):
//     return LlvmUnsupportedDiagnostic {
//     code: "LLVMNNN", ..., hint: "hint", ...,
//     }
//
// Each branch is bounded to the source slice between its kind anchor
// and the next branch's anchor, so a field-literal hint pattern from
// branch N cannot bleed into branch N+1.
func parseLlvmUnsupportedWithBranches(src string) map[string][2]string {
	out := map[string][2]string{}
	matches := ostyKindBranchRE.FindAllStringSubmatchIndex(src, -1)
	for i, m := range matches {
		kind := src[m[2]:m[3]]
		windowEnd := len(src)
		if i+1 < len(matches) {
			windowEnd = matches[i+1][0]
		}
		window := src[m[1]:windowEnd]
		if c := ostyUnsupportedCallRE.FindStringSubmatch(window); c != nil {
			out[kind] = [2]string{c[1], c[2]}
			continue
		}
		codeMatch := ostyUnsupportedLiteralCodeRE.FindStringSubmatch(window)
		hintMatch := ostyUnsupportedLiteralHintRE.FindStringSubmatch(window)
		if codeMatch != nil && hintMatch != nil {
			out[kind] = [2]string{codeMatch[1], hintMatch[1]}
		}
	}
	return out
}
