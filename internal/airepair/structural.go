package airepair

import (
	"sort"
	"strings"

	"github.com/osty/osty/internal/lexer"
	"github.com/osty/osty/internal/repair"
	"github.com/osty/osty/internal/token"
)

type structuralEdit struct {
	start int
	end   int
	text  string
}

func structuralSource(src []byte) repair.Result {
	toks := lexer.New(src).Lex()

	var edits []structuralEdit
	var changes []repair.Change
	add := func(pos token.Pos, kind, msg string, phaseEdits ...structuralEdit) {
		edits = append(edits, phaseEdits...)
		changes = append(changes, repair.Change{
			Kind:    kind,
			Message: msg,
			Pos:     pos,
		})
	}

	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if t.Kind == token.EQ {
			if next, ok := nextSignificantIndex(toks, i+1); ok && toks[next].Kind == token.ASSIGN &&
				toks[next].Pos.Offset == t.End.Offset {
				add(t.Pos, "strict_equality", "replace JS strict equality with Osty equality",
					structuralEdit{start: t.Pos.Offset, end: toks[next].End.Offset, text: "=="})
			}
			continue
		}
		if t.Kind == token.NEQ {
			if next, ok := nextSignificantIndex(toks, i+1); ok && toks[next].Kind == token.ASSIGN &&
				toks[next].Pos.Offset == t.End.Offset {
				add(t.Pos, "strict_inequality", "replace JS strict inequality with Osty inequality",
					structuralEdit{start: t.Pos.Offset, end: toks[next].End.Offset, text: "!="})
			}
			continue
		}
		if t.Kind != token.IDENT || !atStatementStart(toks, i) {
			continue
		}
		switch t.Value {
		case "import":
			if looksLikeUseImport(toks, i) {
				add(t.Pos, "import_keyword", "replace `import` with Osty `use`",
					structuralEdit{start: t.Pos.Offset, end: t.End.Offset, text: "use"})
			}
		case "from":
			if repl, end, ok := fromImportRewrite(toks, i); ok {
				add(t.Pos, "from_import", "replace Python-style `from ... import ...` with Osty `use`",
					structuralEdit{start: t.Pos.Offset, end: end, text: repl})
			}
		}
	}

	out, skipped := applyStructuralEdits(src, edits)
	return repair.Result{
		Source:  out,
		Changes: changes,
		Skipped: skipped,
	}
}

func looksLikeUseImport(toks []token.Token, i int) bool {
	next, ok := nextSignificantIndex(toks, i+1)
	if !ok || toks[next].Pos.Line != toks[i].Pos.Line {
		return false
	}
	switch toks[next].Kind {
	case token.IDENT, token.STRING:
		return true
	default:
		return false
	}
}

func fromImportRewrite(toks []token.Token, i int) (string, int, bool) {
	if toks[i].Kind != token.IDENT || toks[i].Value != "from" {
		return "", 0, false
	}
	line := toks[i].Pos.Line
	modulePath, next, moduleEnd, ok := consumeDottedPath(toks, i+1, line)
	if !ok {
		return "", 0, false
	}
	importIdx, ok := nextSignificantIndex(toks, next)
	if !ok || toks[importIdx].Pos.Line != line || toks[importIdx].Kind != token.IDENT || toks[importIdx].Value != "import" {
		return "", 0, false
	}
	importedPath, next, importedEnd, ok := consumeDottedPath(toks, importIdx+1, line)
	if !ok {
		return "", 0, false
	}
	end := importedEnd
	alias := ""
	if asIdx, ok := nextSignificantIndex(toks, next); ok && toks[asIdx].Pos.Line == line {
		if toks[asIdx].Kind != token.IDENT || toks[asIdx].Value != "as" {
			return "", 0, false
		}
		aliasIdx, ok := nextSignificantIndex(toks, asIdx+1)
		if !ok || toks[aliasIdx].Pos.Line != line || toks[aliasIdx].Kind != token.IDENT {
			return "", 0, false
		}
		alias = toks[aliasIdx].Value
		end = toks[aliasIdx].End.Offset
		next = aliasIdx + 1
	}
	if extra, ok := nextSignificantIndex(toks, next); ok && toks[extra].Pos.Line == line {
		return "", 0, false
	}
	repl := "use " + modulePath + "." + importedPath
	if alias != "" {
		repl += " as " + alias
	}
	_ = moduleEnd
	return repl, end, true
}

func consumeDottedPath(toks []token.Token, start, line int) (string, int, int, bool) {
	idx, ok := nextSignificantIndex(toks, start)
	if !ok || toks[idx].Pos.Line != line || toks[idx].Kind != token.IDENT {
		return "", start, 0, false
	}
	var parts []string
	end := toks[idx].End.Offset
	for idx < len(toks) && toks[idx].Pos.Line == line {
		if toks[idx].Kind != token.IDENT {
			return "", start, 0, false
		}
		parts = append(parts, toks[idx].Value)
		end = toks[idx].End.Offset
		dotIdx, ok := nextSignificantIndex(toks, idx+1)
		if !ok || toks[dotIdx].Pos.Line != line || toks[dotIdx].Kind != token.DOT {
			return strings.Join(parts, "."), idx + 1, end, true
		}
		idx, ok = nextSignificantIndex(toks, dotIdx+1)
		if !ok || toks[idx].Pos.Line != line {
			return "", start, 0, false
		}
	}
	return strings.Join(parts, "."), idx, end, len(parts) > 0
}

func atStatementStart(toks []token.Token, i int) bool {
	prev := prevSignificant(toks, i-1)
	switch prev.Kind {
	case token.EOF, token.NEWLINE, token.LBRACE, token.RBRACE:
		return true
	default:
		return false
	}
}

func prevSignificant(toks []token.Token, start int) token.Token {
	for i := start; i >= 0; i-- {
		if toks[i].Kind != token.NEWLINE {
			return toks[i]
		}
	}
	return token.Token{Kind: token.EOF}
}

func nextSignificantIndex(toks []token.Token, start int) (int, bool) {
	for i := start; i < len(toks); i++ {
		if toks[i].Kind != token.NEWLINE {
			return i, true
		}
	}
	return 0, false
}

func applyStructuralEdits(src []byte, edits []structuralEdit) ([]byte, int) {
	if len(edits) == 0 {
		return src, 0
	}
	sort.SliceStable(edits, func(i, j int) bool {
		if edits[i].start != edits[j].start {
			return edits[i].start > edits[j].start
		}
		return edits[i].end > edits[j].end
	})
	out := append([]byte(nil), src...)
	lastStart := len(src) + 1
	skipped := 0
	for _, e := range edits {
		if e.start < 0 || e.end < e.start || e.end > len(src) || e.end > lastStart {
			skipped++
			continue
		}
		out = append(append([]byte(nil), out[:e.start]...), append([]byte(e.text), out[e.end:]...)...)
		lastStart = e.start
	}
	return out, skipped
}
