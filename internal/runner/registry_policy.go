// registry_policy.go is the Go snapshot of
// toolchain/registry_policy.osty. Osty is the source of truth;
// the drift test in this package keeps the two in sync.
package runner

import "fmt"

// SanitizeIndexName lowercases-hex-escapes every char that isn't
// in the safe alphabet (`[A-Za-z0-9._-]`). Empty input collapses
// to `"_empty"`.
//
// Unicode chars become `_<lowercase-hex-codepoint>` — the result
// stays ASCII regardless of input.
//
// Osty: toolchain/registry_policy.osty:27
func SanitizeIndexName(name string) string {
	out := make([]byte, 0, len(name))
	for _, r := range name {
		if registrySafeRune(r) {
			// ASCII safe runes are 1 byte each; non-ASCII can't be
			// safe per the alphabet so they never reach this branch.
			out = append(out, byte(r))
			continue
		}
		out = append(out, '_')
		out = append(out, []byte(fmt.Sprintf("%x", r))...)
	}
	if len(out) == 0 {
		return "_empty"
	}
	return string(out)
}

func registrySafeRune(r rune) bool {
	return (r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') ||
		r == '-' || r == '_' || r == '.'
}
