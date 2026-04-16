package selfhost

import (
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/token"
)

// scanBadNumericSeparators enforces LANG_SPEC_v0.4 §1.6.1: underscores in
// numeric literals may appear only between two digits of the same base. A
// literal may not start with `_` (maximal munch turns that into an IDENT
// anyway), end with `_`, contain `__`, or place `_` immediately after a
// base prefix. `_` adjacent to `.`, `e`/`E`, or an exponent sign is
// likewise rejected.
//
// The lexer core (examples/selfhost-core/frontend.osty:frontNumberScan)
// has matching logic emitting FrontDiagBadNumericSeparator, but the
// self-hosted regeneration path in this tree is blocked by pre-existing
// issues (see resolve.osty `while` loops). Until regen is fixed this
// Go-side post-processor mirrors the spec so the behavior reaches users
// today; once regen lands the Osty-side diagnostic will cover it and this
// helper becomes redundant (but harmless — duplicate diagnostics are
// deduped by the adapter).
func scanBadNumericSeparators(rt runeTable) []*diag.Diagnostic {
	runes := rt.runes
	var out []*diag.Diagnostic
	for i := 0; i < len(runes); {
		r := runes[i]
		if r < '0' || r > '9' {
			i++
			continue
		}
		// Identify base and body range [bodyStart, bodyEnd).
		base := 10
		bodyStart := i
		bodyEnd := i
		if r == '0' && i+1 < len(runes) {
			switch runes[i+1] {
			case 'x', 'X':
				base = 16
				bodyStart = i + 2
			case 'b', 'B':
				base = 2
				bodyStart = i + 2
			case 'o', 'O':
				base = 8
				bodyStart = i + 2
			}
		}
		// Consume the literal body.
		j := bodyStart
		for j < len(runes) && isNumericContinuation(runes[j], base) {
			j++
		}
		bodyEnd = j
		// For decimal, extend through the fractional part and exponent so
		// adjacent-to-dot / adjacent-to-e underscores fall inside the
		// scanned range.
		if base == 10 {
			if j < len(runes) && runes[j] == '.' && j+1 < len(runes) && runes[j+1] != '.' && isDecimalDigit(runes[j+1]) {
				j++
				for j < len(runes) && isNumericContinuation(runes[j], 10) {
					j++
				}
				bodyEnd = j
			}
			if j < len(runes) && (runes[j] == 'e' || runes[j] == 'E') {
				k := j + 1
				if k < len(runes) && (runes[k] == '+' || runes[k] == '-') {
					k++
				}
				if k < len(runes) && isDecimalDigit(runes[k]) {
					j = k
					for j < len(runes) && isNumericContinuation(runes[j], 10) {
						j++
					}
					bodyEnd = j
				}
			}
		}
		// Look for any underscore whose neighbors are not both base digits.
		bad := false
		for k := bodyStart; k < bodyEnd; k++ {
			if runes[k] != '_' {
				continue
			}
			var prev, next rune = -1, -1
			if k > 0 {
				prev = runes[k-1]
			}
			if k+1 < len(runes) {
				next = runes[k+1]
			}
			if !isBaseDigit(prev, base) || !isBaseDigit(next, base) {
				bad = true
				break
			}
		}
		if bad {
			start := posAtRune(runes, i)
			start.Offset = rt.byteOffset(i)
			end := posAtRune(runes, bodyEnd)
			end.Offset = rt.byteOffset(bodyEnd)
			out = append(out, diag.New(diag.Error, "numeric separator `_` must appear between two digits").
				Code("E0008").
				Primary(diag.Span{Start: start, End: end}, "").
				Hint("place `_` only between two digits").
				Build())
		}
		if j > i {
			i = j
		} else {
			i++
		}
	}
	return out
}

func isNumericContinuation(r rune, base int) bool {
	if r == '_' {
		return true
	}
	return isBaseDigit(r, base)
}

func isBaseDigit(r rune, base int) bool {
	switch base {
	case 2:
		return r == '0' || r == '1'
	case 8:
		return r >= '0' && r <= '7'
	case 10:
		return r >= '0' && r <= '9'
	case 16:
		return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
	}
	return false
}

func isDecimalDigit(r rune) bool {
	return r >= '0' && r <= '9'
}

// posAtRune computes a line/column position for the given rune index.
// Byte offset is filled in by the caller via runeTable.byteOffset.
func posAtRune(runes []rune, target int) token.Pos {
	if target < 0 {
		target = 0
	}
	if target > len(runes) {
		target = len(runes)
	}
	line, col := 1, 1
	for i := 0; i < target; i++ {
		if runes[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return token.Pos{Line: line, Column: col}
}
