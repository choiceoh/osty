// lint_glob_policy.go is the Go snapshot of
// toolchain/lint_glob.osty. Osty is the source of truth;
// the drift test in this package keeps the two in sync.
package runner

import "strings"

// LintMatchGlob reports whether `path` matches glob `pattern`.
// Both arguments use forward-slash separators; callers normalise
// platform-specific separators before invoking. `**` matches zero
// or more path segments. Within a segment, `*`, `?`, `[class]`
// and `\c` follow byte-level filepath.Match semantics.
//
// Osty: toolchain/lint_glob.osty:28
func LintMatchGlob(pattern, path string) bool {
	patParts := strings.Split(pattern, "/")
	pParts := strings.Split(path, "/")
	return lintGlobPartsAt(patParts, 0, pParts, 0)
}

func lintGlobPartsAt(pat []string, pi0 int, p []string, ni0 int) bool {
	pi, ni := pi0, ni0
	for pi < len(pat) && ni < len(p) {
		if pat[pi] == "**" {
			if pi+1 == len(pat) {
				return true
			}
			remaining := len(p) - ni
			for j := 0; j <= remaining; j++ {
				if lintGlobPartsAt(pat, pi+1, p, ni+j) {
					return true
				}
			}
			return false
		}
		if !LintMatchSegment(pat[pi], p[ni]) {
			return false
		}
		pi++
		ni++
	}
	for pi < len(pat) && pat[pi] == "**" {
		pi++
	}
	return pi == len(pat) && ni == len(p)
}

// LintMatchSegment runs the per-segment matcher. Byte-level; no
// path-separator handling inside (segments are already
// separator-free by construction).
//
// Osty: toolchain/lint_glob.osty:68
func LintMatchSegment(pattern, name string) bool {
	return lintMatchSegmentAt([]byte(pattern), 0, []byte(name), 0)
}

func lintMatchSegmentAt(pat []byte, pi0 int, name []byte, ni0 int) bool {
	pi, ni := pi0, ni0
	for pi < len(pat) {
		c := pat[pi]
		if c == 0x2A {
			for pi+1 < len(pat) && pat[pi+1] == 0x2A {
				pi++
			}
			if pi+1 == len(pat) {
				return true
			}
			for k := ni; k <= len(name); k++ {
				if lintMatchSegmentAt(pat, pi+1, name, k) {
					return true
				}
			}
			return false
		}
		if ni >= len(name) {
			return false
		}
		if c == 0x3F {
			pi++
			ni++
			continue
		}
		if c == 0x5B {
			r := lintMatchClass(pat, pi+1, int(name[ni]))
			if !r.Matched {
				return false
			}
			pi = r.Next
			ni++
			continue
		}
		if c == 0x5C {
			pi++
			if pi >= len(pat) {
				return false
			}
		}
		if pat[pi] != name[ni] {
			return false
		}
		pi++
		ni++
	}
	return ni == len(name)
}

// LintClassMatch is the structured outcome of a `[class]` scan:
// whether the candidate byte was in the class and the index
// immediately after the closing `]`. Matched == false with Next ==
// pi0 indicates a malformed (unterminated) class — callers treat
// that as no-match.
//
// Osty: toolchain/lint_glob.osty:132
type LintClassMatch struct {
	Matched bool
	Next    int
}

func lintMatchClass(pat []byte, pi0 int, ch int) LintClassMatch {
	pi := pi0
	negated := false
	if pi < len(pat) {
		lead := pat[pi]
		if lead == 0x5E || lead == 0x21 {
			negated = true
			pi++
		}
	}
	matched := false
	consumedAny := false
	for pi < len(pat) {
		b := pat[pi]
		if b == 0x5D && consumedAny {
			pi++
			final := matched
			if negated {
				final = !matched
			}
			return LintClassMatch{Matched: final, Next: pi}
		}
		lo := int(b)
		if lo == 0x5C {
			pi++
			if pi >= len(pat) {
				return LintClassMatch{Matched: false, Next: pi0}
			}
			lo = int(pat[pi])
		}
		pi++
		hi := lo
		if pi < len(pat) && pat[pi] == 0x2D {
			pi++
			if pi >= len(pat) {
				return LintClassMatch{Matched: false, Next: pi0}
			}
			hi = int(pat[pi])
			if hi == 0x5C {
				pi++
				if pi >= len(pat) {
					return LintClassMatch{Matched: false, Next: pi0}
				}
				hi = int(pat[pi])
			}
			pi++
		}
		if lo <= ch && ch <= hi {
			matched = true
		}
		consumedAny = true
	}
	return LintClassMatch{Matched: false, Next: pi0}
}
