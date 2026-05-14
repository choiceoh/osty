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
