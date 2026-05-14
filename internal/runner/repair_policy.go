// repair_policy.go is the Go snapshot of
// toolchain/repair_policy.osty. Osty is the source of truth;
// the drift test in this package keeps the two in sync.
package runner

// RepairLookupResult mirrors toolchain/repair_policy.osty's
// RepairLookupResult — the outcome of inspecting a token value
// for a known foreign-language → Osty translation.
//
// Osty: toolchain/repair_policy.osty:24
type RepairLookupResult struct {
	Value string
	Ok    bool
}

// UppercaseBasePrefix recognises numeric literals written with an
// uppercase base prefix (`0X` / `0B` / `0O`) and returns the
// lowercase replacement character. Returns "" otherwise.
//
// Osty: toolchain/repair_policy.osty:32
func UppercaseBasePrefix(value string) string {
	if len(value) < 2 || value[0] != '0' {
		return ""
	}
	switch value[1] {
	case 'X':
		return "x"
	case 'B':
		return "b"
	case 'O':
		return "o"
	}
	return ""
}

// DeclarationKeywordReplacement maps a foreign-language
// declaration keyword to Osty's `fn`. Token-context filtering
// stays in the host repair pass.
//
// Osty: toolchain/repair_policy.osty:54
func DeclarationKeywordReplacement(value string) RepairLookupResult {
	switch value {
	case "func", "function", "def":
		return RepairLookupResult{Value: "fn", Ok: true}
	}
	return RepairLookupResult{}
}

// ValueIdentifierReplacement maps a foreign-language value
// identifier to its Osty canonical form (`nil`/`null` → `None`,
// `True` → `true`, `False` → `false`).
//
// Osty: toolchain/repair_policy.osty:71
func ValueIdentifierReplacement(value string) RepairLookupResult {
	switch value {
	case "nil", "null":
		return RepairLookupResult{Value: "None", Ok: true}
	case "True":
		return RepairLookupResult{Value: "true", Ok: true}
	case "False":
		return RepairLookupResult{Value: "false", Ok: true}
	}
	return RepairLookupResult{}
}

// LineIndent returns the leading whitespace prefix of the line
// that contains byte `offset` in `src`. Only ASCII space (0x20)
// and tab (0x09) bytes count as indent.
//
// Osty: toolchain/repair_policy.osty:91
func LineIndent(src []byte, offset int) string {
	start := offset
	if start > len(src) {
		start = len(src)
	}
	for start > 0 && src[start-1] != '\n' {
		start--
	}
	end := start
	for end < len(src) {
		switch src[end] {
		case ' ', '\t':
			end++
		default:
			return string(src[start:end])
		}
	}
	return string(src[start:end])
}
