// lint_codes_policy.go is the Go snapshot of
// toolchain/lint_codes.osty. Osty is the source of truth;
// the drift test in this package keeps the two in sync.
package runner

// IsLintCode reports whether `code` belongs to the Lxxxx
// namespace — exactly one `L` followed by one or more ASCII
// digits. Empty strings and short tokens are rejected.
//
// Osty: toolchain/lint_codes.osty:25
func IsLintCode(code string) bool {
	if len(code) < 2 || code[0] != 'L' {
		return false
	}
	for i := 1; i < len(code); i++ {
		c := code[i]
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// LintMergeStringSlices returns the union of two string slices
// with dedup. Entries from `a` come first, then entries from `b`
// that haven't been seen yet. Returns nil when both inputs are
// empty.
//
// Osty: toolchain/lint_codes.osty:49
func LintMergeStringSlices(a, b []string) []string {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, s := range a {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, s := range b {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
