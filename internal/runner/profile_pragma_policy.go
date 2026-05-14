// profile_pragma_policy.go is the Go snapshot of
// toolchain/profile_pragma.osty. Osty is the source of truth;
// the drift test in this package keeps the two in sync.
package runner

import "strings"

// TripleParseResult mirrors toolchain/profile_pragma.osty's
// TripleParseResult. Ok=false leaves Arch/OS empty.
//
// Osty: toolchain/profile_pragma.osty:31
type TripleParseResult struct {
	Arch string
	OS   string
	Ok   bool
}

// PragmaStripResult is the outcome of `pragmaStrip`. The Rest
// field carries the line content after the matched prefix when
// Ok=true, otherwise empty.
//
// Osty: toolchain/profile_pragma.osty (internal helper)
type PragmaStripResult struct {
	Rest string
	Ok   bool
}

// ParseTriple splits an `<arch>-<os>` target triple on the first
// `-` so the OS half can carry further dashes
// (`x86_64-apple-darwin` → arch=`x86_64`, os=`apple-darwin`).
// Both halves must be non-empty.
//
// Osty: toolchain/profile_pragma.osty:40
func ParseTriple(triple string) TripleParseResult {
	parts := strings.SplitN(triple, "-", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return TripleParseResult{}
	}
	return TripleParseResult{Arch: parts[0], OS: parts[1], Ok: true}
}

// ReadFeaturePragma scans the first 32 logical lines of `src` for
// a `// @feature: a, b c` pragma. Returns an empty slice when no
// pragma is found in the prefix window or when a non-comment,
// non-blank line appears before one. Both `// @feature:` and
// `//@feature:` prefixes are accepted.
//
// Osty: toolchain/profile_pragma.osty:60
func ReadFeaturePragma(src []byte) []string {
	const maxLines = 32
	line := 0
	start := 0
	for i := 0; i <= len(src) && line < maxLines; i++ {
		if i < len(src) && src[i] != '\n' {
			continue
		}
		raw := string(src[start:i])
		trimmed := pragmaTrim(raw)
		if r := pragmaStrip(trimmed, "// @feature:"); r.Ok {
			return ParsePragmaFeatureList(r.Rest)
		}
		if r := pragmaStrip(trimmed, "//@feature:"); r.Ok {
			return ParsePragmaFeatureList(r.Rest)
		}
		if trimmed != "" && !strings.HasPrefix(trimmed, "//") && !strings.HasPrefix(trimmed, "/*") {
			return nil
		}
		start = i + 1
		line++
	}
	return nil
}

// ParsePragmaFeatureList splits a pragma payload on any of
// `,` / ` ` / `\t`, trims each token (ASCII space, tab, CR), and
// drops empty entries.
//
// Osty: toolchain/profile_pragma.osty:96
func ParsePragmaFeatureList(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ',' || c == ' ' || c == '\t' {
			if start < i {
				token := pragmaTrim(s[start:i])
				if token != "" {
					out = append(out, token)
				}
			}
			start = i + 1
		}
	}
	if start < len(s) {
		token := pragmaTrim(s[start:])
		if token != "" {
			out = append(out, token)
		}
	}
	return out
}

// pragmaTrim matches Go's `featTrim` — strips leading/trailing
// ASCII space (0x20), tab (0x09), and CR (0x0D).
//
// Osty: toolchain/profile_pragma.osty (internal helper)
func pragmaTrim(s string) string {
	i, j := 0, len(s)
	for i < j && (s[i] == ' ' || s[i] == '\t' || s[i] == '\r') {
		i++
	}
	for j > i && (s[j-1] == ' ' || s[j-1] == '\t' || s[j-1] == '\r') {
		j--
	}
	return s[i:j]
}

// pragmaStrip matches Go's `featStrip` — returns rest-if-prefix
// shape so callers can branch on Ok and consume Rest.
func pragmaStrip(s, p string) PragmaStripResult {
	if strings.HasPrefix(s, p) {
		return PragmaStripResult{Rest: s[len(p):], Ok: true}
	}
	return PragmaStripResult{}
}
