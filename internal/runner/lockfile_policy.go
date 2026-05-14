// lockfile_policy.go is the Go snapshot of
// toolchain/lockfile_policy.osty. Osty is the source of truth;
// the drift test in this package keeps the two in sync.
package runner

import "strings"

// DependencyParts mirrors toolchain/lockfile_policy.osty's
// DependencyParts. Ok=false signals a malformed line; the
// host emits the precise `invalid dependency "..."` diagnostic.
//
// Osty: toolchain/lockfile_policy.osty:22
type DependencyParts struct {
	Name    string
	Version string
	Source  string
	Ok      bool
}

// DependencyString renders one lockfile entry as the canonical
// single-line form. Empty source → `<name> <version>`; otherwise
// `<name> <version> (<source>)`.
//
// Osty: toolchain/lockfile_policy.osty:36
func DependencyString(name, version, source string) string {
	if source == "" {
		return name + " " + version
	}
	return name + " " + version + " (" + source + ")"
}

// ParseDependency is the inverse of DependencyString. Returns
// Ok=false for malformed shapes (wrong field count, etc.); the
// host blames the line with `invalid dependency "..."`.
//
// Osty: toolchain/lockfile_policy.osty:51
func ParseDependency(line string) DependencyParts {
	trimmed := strings.TrimSpace(line)
	head := trimmed
	source := ""
	if strings.HasSuffix(trimmed, ")") {
		if open := strings.Index(trimmed, "("); open > 0 {
			source = trimmed[open+1 : len(trimmed)-1]
			head = strings.TrimSpace(trimmed[:open])
		}
	}
	parts := parseDependencyFields(head)
	if len(parts) != 2 {
		return DependencyParts{}
	}
	return DependencyParts{Name: parts[0], Version: parts[1], Source: source, Ok: true}
}

// parseDependencyFields splits `head` on runs of ASCII space /
// tab. Matches Go's `strings.Fields` for the lockfile's limited
// alphabet (names/versions never carry CR/LF/VT/FF).
//
// Osty: toolchain/lockfile_policy.osty:81
func parseDependencyFields(head string) []string {
	var out []string
	start := -1
	for i := 0; i < len(head); i++ {
		b := head[i]
		isWS := b == ' ' || b == '\t'
		if isWS {
			if start >= 0 {
				out = append(out, head[start:i])
				start = -1
			}
		} else if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		out = append(out, head[start:])
	}
	return out
}
