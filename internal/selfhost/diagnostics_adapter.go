package selfhost

import (
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/token"
)

// Diagnostics returns lexer and parser diagnostics from this run.
func (r *FrontendRun) Diagnostics() []*diag.Diagnostic {
	if r.diags != nil {
		return r.diags
	}
	lexDiags := r.LexDiagnostics()
	parseDiags := parseDiagnosticsFromArena(r.parser.arena, r.stream, r.rt)
	diags := make([]*diag.Diagnostic, 0, len(lexDiags)+len(parseDiags))
	diags = append(diags, lexDiags...)
	diags = append(diags, parseDiags...)
	r.diags = dedupeDiagnostics(diags)
	return r.diags
}

func dedupeDiagnostics(in []*diag.Diagnostic) []*diag.Diagnostic {
	out := in[:0]
	seen := map[diagnosticDedupeKey]bool{}
	for _, d := range in {
		if d == nil {
			continue
		}
		key := diagnosticKey(d)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, d)
	}
	return out
}

type diagnosticDedupeKey struct {
	severity diag.Severity
	code     string
	message  string
	hint     string
	notes    string
	start    token.Pos
	end      token.Pos
}

func diagnosticKey(d *diag.Diagnostic) diagnosticDedupeKey {
	span := diag.Span{}
	for _, s := range d.Spans {
		if s.Primary {
			span = s.Span
			break
		}
	}
	if span.Start == (token.Pos{}) && len(d.Spans) > 0 {
		span = d.Spans[0].Span
	}
	return diagnosticDedupeKey{
		severity: d.Severity,
		code:     d.Code,
		message:  d.Message,
		hint:     d.Hint,
		notes:    diagnosticNotesKey(d.Notes),
		start:    span.Start,
		end:      span.End,
	}
}

func diagnosticNotesKey(notes []string) string {
	if len(notes) == 0 {
		return ""
	}
	out := notes[0]
	for _, note := range notes[1:] {
		out += "\x00" + note
	}
	return out
}

func parseDiagnosticsFromArena(arena *AstArena, stream *FrontLexStream, rt runeTable) []*diag.Diagnostic {
	if arena == nil {
		return nil
	}
	out := make([]*diag.Diagnostic, 0, len(arena.errors))
	for _, e := range arena.errors {
		b := diag.New(diag.Error, e.message).Primary(parseErrorSpan(e, stream, rt), "")
		if e.code != "" {
			b.Code(e.code)
		}
		if e.hint != "" {
			b.Hint(e.hint)
		}
		if e.note != "" {
			b.Note(e.note)
		}
		out = append(out, b.Build())
	}
	return out
}

func parseErrorSpan(e *AstParseError, stream *FrontLexStream, rt runeTable) diag.Span {
	pos := token.Pos{Line: 1, Column: 1}
	if e == nil || stream == nil || e.tokenIndex < 0 || e.tokenIndex >= len(stream.tokens) {
		return diag.Span{Start: pos, End: pos}
	}
	tok := stream.tokens[e.tokenIndex]
	span := rt.span(tok.start, tok.end)
	if span.End.Offset < span.Start.Offset {
		span.End = span.Start
	}
	return span
}
