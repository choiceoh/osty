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
	if len(in) <= 1 {
		if len(in) == 1 && in[0] == nil {
			return in[:0]
		}
		return in
	}
	out := in[:0]
	if len(in) <= 4 {
		for _, d := range in {
			if d == nil {
				continue
			}
			pos := d.PrimaryPos()
			dup := false
			for _, seen := range out {
				if seen != nil && seen.PrimaryPos() == pos {
					dup = true
					break
				}
			}
			if !dup {
				out = append(out, d)
			}
		}
		return out
	}
	seen := map[token.Pos]bool{}
	for _, d := range in {
		if d == nil {
			continue
		}
		pos := d.PrimaryPos()
		if seen[pos] {
			continue
		}
		seen[pos] = true
		out = append(out, d)
	}
	return out
}

func parseDiagnosticsFromArena(arena *AstArena, stream *FrontLexStream, rt runeTable) []*diag.Diagnostic {
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
	if e == nil || e.tokenIndex < 0 || e.tokenIndex >= len(stream.tokens) {
		return diag.Span{Start: pos, End: pos}
	}
	tok := stream.tokens[e.tokenIndex]
	span := rt.span(tok.start, tok.end)
	if span.End.Offset < span.Start.Offset {
		span.End = span.Start
	}
	return span
}
