// registry_policy.go is the Go snapshot of
// toolchain/registry_policy.osty. Osty is the source of truth;
// the drift test in this package keeps the two in sync.
package runner

import (
	"fmt"
	"strconv"
	"strings"
)

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

// RegistrySearchScore is the structured outcome of ranking one
// package against the user's search query. Ok=false means
// "no match"; lower Score ranks better.
//
// Osty: toolchain/registry_policy.osty:79
type RegistrySearchScore struct {
	Score int
	Ok    bool
}

// RegistrySearchScoreOf ranks one package against query q.
// Tiered match: 0 exact-name / 1 prefix / 2 contains-name /
// 3 contains-description / 4 contains-keyword. Callers
// pre-lowercase q so the host doesn't re-pay trim cost.
//
// Osty: toolchain/registry_policy.osty:95
func RegistrySearchScoreOf(name, description string, keywords []string, q string) RegistrySearchScore {
	lowerName := strings.ToLower(name)
	if lowerName == q {
		return RegistrySearchScore{Score: 0, Ok: true}
	}
	if strings.HasPrefix(lowerName, q) {
		return RegistrySearchScore{Score: 1, Ok: true}
	}
	if strings.Contains(lowerName, q) {
		return RegistrySearchScore{Score: 2, Ok: true}
	}
	if strings.Contains(strings.ToLower(description), q) {
		return RegistrySearchScore{Score: 3, Ok: true}
	}
	for _, kw := range keywords {
		if strings.Contains(strings.ToLower(kw), q) {
			return RegistrySearchScore{Score: 4, Ok: true}
		}
	}
	return RegistrySearchScore{Score: 0, Ok: false}
}

// RegistryStatusErrorMessage builds the error string emitted
// when an HTTP response from the registry has an unexpected
// status code. Format: `registry <url>: HTTP <code>: <msg>`.
// When the trimmed body is empty, statusText (typically
// `resp.Status`) is used as the message.
//
// Osty: toolchain/registry_policy.osty:137
func RegistryStatusErrorMessage(url string, statusCode int, body, statusText string) string {
	trimmed := strings.TrimSpace(body)
	msg := trimmed
	if msg == "" {
		msg = statusText
	}
	return "registry " + url + ": HTTP " + strconv.Itoa(statusCode) + ": " + msg
}

// ValidateRegistryPackageName checks `name` against the registry's
// accepted name alphabet. Returns the empty string on success;
// otherwise an error message the host wraps in badRequest:
//
//   - empty input → `package name is empty`
//   - any other rejection → `invalid package name <quoted>` where
//     <quoted> is TomlBasicString(name) — Go `%q`-style escapes
//     keep embedded quotes / backslashes / control characters from
//     breaking log lines or HTTP error responses.
//
// Rules: [A-Za-z_] everywhere, [0-9-] for non-first positions.
//
// Osty: toolchain/registry_policy.osty:158
func ValidateRegistryPackageName(name string) string {
	if name == "" {
		return "package name is empty"
	}
	for i := 0; i < len(name); i++ {
		b := name[i]
		isUpper := b >= 'A' && b <= 'Z'
		isLower := b >= 'a' && b <= 'z'
		isUnderscore := b == '_'
		isDigit := b >= '0' && b <= '9'
		isDash := b == '-'
		always := isUpper || isLower || isUnderscore
		nonFirst := i > 0 && (isDigit || isDash)
		if !(always || nonFirst) {
			return "invalid package name " + TomlBasicString(name)
		}
	}
	return ""
}

// RegistrySearchRequest is the normalised inputs the registry's
// search handler operates on. ErrorMessage == "" signals
// "proceed"; any non-empty value is the user-facing error
// string to wrap in badRequest.
//
// Osty: toolchain/registry_policy.osty:191
type RegistrySearchRequest struct {
	Query        string
	Limit        int
	ErrorMessage string
}

// RegistrySearchRequestNormalize handles the per-request policy:
// query trim+lowercase, default page size 20, and the
// empty-query rejection message.
//
// Osty: toolchain/registry_policy.osty:207
func RegistrySearchRequestNormalize(rawQuery string, rawLimit int) RegistrySearchRequest {
	q := strings.ToLower(strings.TrimSpace(rawQuery))
	if q == "" {
		return RegistrySearchRequest{ErrorMessage: "registry search query is empty"}
	}
	limit := rawLimit
	if limit <= 0 {
		limit = 20
	}
	return RegistrySearchRequest{Query: q, Limit: limit}
}
