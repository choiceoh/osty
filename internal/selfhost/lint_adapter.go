package selfhost

import (
	"fmt"

	"github.com/osty/osty/internal/diag"
)

// LintDiagnostics adapts the bootstrapped Osty lint report into the compiler's
// public diagnostic surface, including structured machine-applicable fixes.
func LintDiagnostics(src []byte) []*diag.Diagnostic {
	text := string(src)
	rt := newRuneTable(text)
	stream := frontendLexStream(text)
	report := selfLintSource(text)
	if report == nil {
		return nil
	}
	out := make([]*diag.Diagnostic, 0, len(report.diagnostics))
	for _, d := range report.diagnostics {
		if d == nil {
			continue
		}
		out = append(out, adaptSelfLintDiagnostic(d, rt, stream))
	}
	return out
}

func adaptSelfLintDiagnostic(d *SelfLintDiagnostic, rt runeTable, stream *FrontLexStream) *diag.Diagnostic {
	msg := d.message
	if d.name != "" {
		msg = fmt.Sprintf("%s `%s`", msg, d.name)
	}
	b := diag.New(diag.Warning, msg).
		Code(d.code).
		Primary(selfLintDiagSpan(rt, stream, d.start, d.end), "")
	for _, fix := range d.fixes {
		if fix == nil {
			continue
		}
		span := selfLintDiagSpan(rt, stream, fix.start, fix.end)
		if fix.copyStart >= 0 {
			copyFrom := selfLintDiagSpan(rt, stream, fix.copyStart, fix.copyEnd)
			b.SuggestCopy(span, copyFrom, fix.template, fix.label, fix.machineApplicable)
		} else {
			b.Suggest(span, fix.replacement, fix.label, fix.machineApplicable)
		}
	}
	return b.Build()
}

func selfLintDiagSpan(rt runeTable, stream *FrontLexStream, start, end int) diag.Span {
	count := frontLexTokenCount(stream)
	if count <= 0 {
		pos := rt.pos(&FrontPos{offset: 0, line: 1, column: 1})
		return diag.Span{Start: pos, End: pos}
	}
	startIdx := clampSelfLintTokenIndex(start, count)
	endIdx := startIdx
	if end > start {
		endIdx = clampSelfLintTokenIndex(end-1, count)
	}
	first := frontLexTokenAt(stream, startIdx)
	last := frontLexTokenAt(stream, endIdx)
	startPos := rt.pos(first.start)
	endPos := rt.pos(last.end)
	if end <= start {
		endPos = startPos
	}
	return diag.Span{Start: startPos, End: endPos}
}

func clampSelfLintTokenIndex(idx, count int) int {
	if count <= 0 {
		return 0
	}
	if idx < 0 {
		return 0
	}
	if idx >= count {
		return count - 1
	}
	return idx
}
