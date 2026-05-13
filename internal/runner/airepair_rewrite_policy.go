// airepair_rewrite_policy.go is the Go snapshot of
// toolchain/airepair_rewrite.osty. Osty is the source of truth;
// the drift test in this package keeps the two in sync.
package runner

import "strings"

// AppendCallParse mirrors toolchain/airepair_rewrite.osty's
// AppendCallParse. Empty Base/Item with Ok=false signals the
// expression is not a recognisable `append(base, item)` shape.
//
// Osty: toolchain/airepair_rewrite.osty:23
type AppendCallParse struct {
	Base string
	Item string
	Ok   bool
}

// SplitTopLevelComma splits `src` on commas that sit at the
// outermost paren/bracket/brace nesting level. Empty / whitespace
// input returns nil. Returned segments are not trimmed — callers
// that care apply their own whitespace strip.
//
// Osty: toolchain/airepair_rewrite.osty:36
func SplitTopLevelComma(src string) []string {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	var (
		parts        []string
		start        int
		parenDepth   int
		bracketDepth int
		braceDepth   int
	)
	bs := []byte(src)
	for i, b := range bs {
		switch b {
		case '(':
			parenDepth++
		case ')':
			if parenDepth > 0 {
				parenDepth--
			}
		case '[':
			bracketDepth++
		case ']':
			if bracketDepth > 0 {
				bracketDepth--
			}
		case '{':
			braceDepth++
		case '}':
			if braceDepth > 0 {
				braceDepth--
			}
		case ',':
			if parenDepth == 0 && bracketDepth == 0 && braceDepth == 0 {
				parts = append(parts, string(bs[start:i]))
				start = i + 1
			}
		}
	}
	parts = append(parts, string(bs[start:]))
	return parts
}

// IsSimpleIdentifierBinding reports whether `part` is the wildcard
// `_` or an ASCII identifier (`[A-Za-z_][A-Za-z0-9_]*`). Empty
// input is rejected.
//
// Osty: toolchain/airepair_rewrite.osty:80
func IsSimpleIdentifierBinding(part string) bool {
	if part == "_" {
		return true
	}
	if part == "" {
		return false
	}
	for i := 0; i < len(part); i++ {
		b := part[i]
		isUnderscore := b == '_'
		isUpper := b >= 'A' && b <= 'Z'
		isLower := b >= 'a' && b <= 'z'
		isDigit := b >= '0' && b <= '9'
		if i == 0 {
			if !isUnderscore && !isUpper && !isLower {
				return false
			}
		} else if !isUnderscore && !isUpper && !isLower && !isDigit {
			return false
		}
	}
	return true
}

// ParseAppendCall recognises an `append(base, item)` expression
// surface so the airepair rewriter can replace it with the
// equivalent `base.push(item)` Osty form. Anything that doesn't
// match the exact `append( ... )` envelope with two top-level
// arguments returns Ok=false.
//
// Osty: toolchain/airepair_rewrite.osty:111
func ParseAppendCall(expr string) AppendCallParse {
	const prefix = "append("
	if !strings.HasPrefix(expr, prefix) || !strings.HasSuffix(expr, ")") {
		return AppendCallParse{}
	}
	inner := expr[len(prefix) : len(expr)-1]
	args := SplitTopLevelComma(strings.TrimSpace(inner))
	if len(args) != 2 {
		return AppendCallParse{}
	}
	base := strings.TrimSpace(args[0])
	item := strings.TrimSpace(args[1])
	if base == "" || item == "" {
		return AppendCallParse{}
	}
	return AppendCallParse{Base: base, Item: item, Ok: true}
}

// IsRewritableLengthTypeKind reports whether a `.length` field
// access on a receiver typed `(kind, name)` is the JS/Python habit
// we want to rewrite to `.len()`. The conservative whitelist matches
// receivers whose Osty surface guarantees a `.len()` intrinsic:
// `String`/`Bytes` primitives plus the `List`/`Map`/`Set`/
// `OrderedMap` named container types. Anything else is left alone
// so we don't replace a valid field read with a missing-method
// call.
//
// Osty: toolchain/airepair_rewrite.osty:128
func IsRewritableLengthTypeKind(kind, name string) bool {
	switch kind {
	case "primitive":
		return name == "String" || name == "Bytes"
	case "named":
		switch name {
		case "List", "Map", "Set", "OrderedMap":
			return true
		}
	}
	return false
}
