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

// LoopRewrite mirrors toolchain/airepair_rewrite.osty's LoopRewrite.
// Empty Rewritten with Ok=false means the candidate header is not a
// shape this rewriter recognises.
//
// Osty: toolchain/airepair_rewrite.osty:158
type LoopRewrite struct {
	Rewritten string
	Ok        bool
}

// ForeignLoopRewrite mirrors toolchain/airepair_rewrite.osty's
// ForeignLoopRewrite. Kind / Message are populated only when the
// dispatcher matched a sub-rewriter so the host can shape the
// resulting diag without re-running the dispatch.
//
// Osty: toolchain/airepair_rewrite.osty:166
type ForeignLoopRewrite struct {
	Rewritten string
	Kind      string
	Message   string
	Ok        bool
}

// TupleBindingNormalize mirrors toolchain/airepair_rewrite.osty's
// TupleBindingNormalize. Joined is the trimmed-and-rejoined form a
// caller can splice into the generated header.
//
// Osty: toolchain/airepair_rewrite.osty:176
type TupleBindingNormalize struct {
	Joined string
	Ok     bool
}

// NormalizeTupleLoopBindings parses a `binding, binding, ...`
// payload from a tuple-loop header.
//
// Osty: toolchain/airepair_rewrite.osty:185
func NormalizeTupleLoopBindings(src string) TupleBindingNormalize {
	parts := SplitTopLevelComma(src)
	if len(parts) < 2 {
		return TupleBindingNormalize{}
	}
	normalized := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if !IsSimpleIdentifierBinding(trimmed) {
			return TupleBindingNormalize{}
		}
		normalized = append(normalized, trimmed)
	}
	return TupleBindingNormalize{Joined: strings.Join(normalized, ", "), Ok: true}
}

// RewriteJSForOfHeader recognises a JS `for (... of ...) {` loop
// header and emits the Osty equivalent. Recognises bare `let` /
// `const` / `var` keyword prefixes on the binding side and
// `[a, b]` array-destructure shapes (rewritten to Osty tuple
// destructuring).
//
// Osty: toolchain/airepair_rewrite.osty:228
func RewriteJSForOfHeader(trimmed string) LoopRewrite {
	if !strings.HasPrefix(trimmed, "for ") || !strings.HasSuffix(trimmed, "{") {
		return LoopRewrite{}
	}
	body := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "for "), "{"))
	if !strings.HasPrefix(body, "(") || !strings.HasSuffix(body, ")") {
		return LoopRewrite{}
	}
	inner := strings.TrimSpace(body[1 : len(body)-1])
	ofIdx := strings.Index(inner, " of ")
	if ofIdx <= 0 {
		return LoopRewrite{}
	}
	lhs := strings.TrimSpace(inner[:ofIdx])
	rhs := strings.TrimSpace(inner[ofIdx+4:])
	switch {
	case strings.HasPrefix(lhs, "const "):
		lhs = strings.TrimSpace(strings.TrimPrefix(lhs, "const "))
	case strings.HasPrefix(lhs, "let "):
		lhs = strings.TrimSpace(strings.TrimPrefix(lhs, "let "))
	case strings.HasPrefix(lhs, "var "):
		lhs = strings.TrimSpace(strings.TrimPrefix(lhs, "var "))
	}
	if lhs == "" || rhs == "" {
		return LoopRewrite{}
	}
	if strings.HasPrefix(lhs, "[") && strings.HasSuffix(lhs, "]") {
		inside := strings.TrimSpace(lhs[1 : len(lhs)-1])
		normalized := NormalizeTupleLoopBindings(inside)
		if !normalized.Ok {
			return LoopRewrite{}
		}
		return LoopRewrite{Rewritten: "for (" + normalized.Joined + ") in " + rhs + " {", Ok: true}
	}
	if !IsSimpleIdentifierBinding(lhs) {
		return LoopRewrite{}
	}
	return LoopRewrite{Rewritten: "for " + lhs + " in " + rhs + " {", Ok: true}
}

// RewritePythonRangeLoopHeader recognises a `for X in range(...) {`
// loop header and emits an Osty `for X in start..end` form. The
// 3-arg form (`range(start, end, step)`) is only accepted when
// `step == 1`.
//
// Osty: toolchain/airepair_rewrite.osty:272
func RewritePythonRangeLoopHeader(trimmed string) LoopRewrite {
	if !strings.HasPrefix(trimmed, "for ") || !strings.HasSuffix(trimmed, "{") {
		return LoopRewrite{}
	}
	body := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "for "), "{"))
	inIdx := strings.Index(body, " in ")
	if inIdx <= 0 {
		return LoopRewrite{}
	}
	lhs := strings.TrimSpace(body[:inIdx])
	rhs := strings.TrimSpace(body[inIdx+4:])
	if !IsSimpleIdentifierBinding(lhs) || !strings.HasPrefix(rhs, "range(") || !strings.HasSuffix(rhs, ")") {
		return LoopRewrite{}
	}
	args := SplitTopLevelComma(strings.TrimSpace(rhs[len("range(") : len(rhs)-1]))
	var start, end string
	switch len(args) {
	case 1:
		start = "0"
		end = strings.TrimSpace(args[0])
	case 2:
		start = strings.TrimSpace(args[0])
		end = strings.TrimSpace(args[1])
	case 3:
		start = strings.TrimSpace(args[0])
		end = strings.TrimSpace(args[1])
		if strings.TrimSpace(args[2]) != "1" {
			return LoopRewrite{}
		}
	default:
		return LoopRewrite{}
	}
	if start == "" || end == "" {
		return LoopRewrite{}
	}
	return LoopRewrite{Rewritten: "for " + lhs + " in " + start + ".." + end + " {", Ok: true}
}

// RewritePythonEnumerateLoopHeader recognises
// `for i, v in enumerate(xs) {` and emits an Osty enumerate-shape
// header. Both bare-tuple and parenthesised binding shapes are
// accepted.
//
// Osty: toolchain/airepair_rewrite.osty:316
func RewritePythonEnumerateLoopHeader(trimmed string) LoopRewrite {
	if !strings.HasPrefix(trimmed, "for ") || !strings.HasSuffix(trimmed, "{") {
		return LoopRewrite{}
	}
	body := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "for "), "{"))
	inIdx := strings.Index(body, " in ")
	if inIdx <= 0 {
		return LoopRewrite{}
	}
	lhs := strings.TrimSpace(body[:inIdx])
	rhs := strings.TrimSpace(body[inIdx+4:])
	if !strings.HasPrefix(rhs, "enumerate(") || !strings.HasSuffix(rhs, ")") {
		return LoopRewrite{}
	}
	iterable := strings.TrimSpace(rhs[len("enumerate(") : len(rhs)-1])
	if iterable == "" {
		return LoopRewrite{}
	}
	if strings.HasPrefix(lhs, "(") && strings.HasSuffix(lhs, ")") {
		inside := strings.TrimSpace(lhs[1 : len(lhs)-1])
		normalized := NormalizeTupleLoopBindings(inside)
		if !normalized.Ok {
			return LoopRewrite{}
		}
		return LoopRewrite{Rewritten: "for (" + normalized.Joined + ") in " + iterable + ".enumerate() {", Ok: true}
	}
	normalized := NormalizeTupleLoopBindings(lhs)
	if !normalized.Ok {
		return LoopRewrite{}
	}
	return LoopRewrite{Rewritten: "for (" + normalized.Joined + ") in " + iterable + ".enumerate() {", Ok: true}
}

// RewriteForeignLoopHeader is the dispatcher that asks each
// shape-specific rewriter in turn. Order matters: JS for-of first
// (parenthesised body), then enumerate (so `enumerate(...)` is not
// partially matched by range), then range.
//
// Osty: toolchain/airepair_rewrite.osty:367
func RewriteForeignLoopHeader(trimmed string) ForeignLoopRewrite {
	if r := RewriteJSForOfHeader(trimmed); r.Ok {
		return ForeignLoopRewrite{
			Rewritten: r.Rewritten,
			Kind:      "js_for_of_loop",
			Message:   "replace JS-style `for (... of ...)` loop with Osty `for ... in` syntax",
			Ok:        true,
		}
	}
	if r := RewritePythonEnumerateLoopHeader(trimmed); r.Ok {
		return ForeignLoopRewrite{
			Rewritten: r.Rewritten,
			Kind:      "python_enumerate_loop",
			Message:   "replace Python `enumerate(...)` loop with Osty `.enumerate()` iteration",
			Ok:        true,
		}
	}
	if r := RewritePythonRangeLoopHeader(trimmed); r.Ok {
		return ForeignLoopRewrite{
			Rewritten: r.Rewritten,
			Kind:      "python_range_loop",
			Message:   "replace Python `range(...)` loop with Osty range syntax",
			Ok:        true,
		}
	}
	return ForeignLoopRewrite{}
}
