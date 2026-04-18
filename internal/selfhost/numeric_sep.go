package selfhost

import (
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/token"
)

// scanBadNumericSeparators enforces LANG_SPEC_v0.5 §1.6.1: underscores in
// numeric literals must sit between two digits of the same base.
//
// Walks INT/FLOAT tokens (top-level and interpolation bodies), so string
// bodies, comments, and identifiers never trip the check. Bridge until the
// selfhost lexer emits FrontDiagBadNumericSeparator directly; duplicate
// diagnostics are deduped by the adapter.
func scanBadNumericSeparators(rt runeTable, stream *FrontLexStream) []*diag.Diagnostic {
	var out []*diag.Diagnostic
	check := func(ft *FrontLexToken) {
		if ft == nil || ft.start == nil {
			return
		}
		switch ft.kind.(type) {
		case *FrontTokenKind_FrontInt, *FrontTokenKind_FrontFloat:
		default:
			return
		}
		if d := checkNumericLiteral(rt, ft.start.offset, ft.length); d != nil {
			out = append(out, d)
		}
	}
	for _, ft := range stream.tokens {
		check(ft)
	}
	for _, it := range stream.interpolationTokens {
		if it != nil {
			check(it.token)
		}
	}
	return out
}

func checkNumericLiteral(rt runeTable, startRune, length int) *diag.Diagnostic {
	runes := rt.runes
	end := startRune + length
	if end > len(runes) {
		end = len(runes)
	}
	base := 10
	bodyStart := startRune
	if length >= 2 && runes[startRune] == '0' {
		switch runes[startRune+1] {
		case 'x', 'X':
			base = 16
			bodyStart = startRune + 2
		case 'b', 'B':
			base = 2
			bodyStart = startRune + 2
		case 'o', 'O':
			base = 8
			bodyStart = startRune + 2
		}
	}
	for k := bodyStart; k < end; k++ {
		if runes[k] != '_' {
			continue
		}
		var prev, next rune = -1, -1
		if k > startRune {
			prev = runes[k-1]
		}
		if k+1 < end {
			next = runes[k+1]
		}
		if !isBaseDigit(prev, base) || !isBaseDigit(next, base) {
			startPos := posAtRune(runes, startRune)
			startPos.Offset = rt.byteOffset(startRune)
			endPos := posAtRune(runes, end)
			endPos.Offset = rt.byteOffset(end)
			return diag.New(diag.Error, "numeric separator `_` must appear between two digits").
				Code("E0008").
				Primary(diag.Span{Start: startPos, End: endPos}, "").
				Hint("place `_` only between two digits").
				Build()
		}
	}
	return nil
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
