package parser

import (
	"bytes"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/selfhost"
	"github.com/osty/osty/internal/token"
)

type stableAliasSpec struct {
	alias       string
	canonical   string
	kind        string
	sourceHabit string
	detail      string
}

var stableAliasSpecs = map[string]stableAliasSpec{
	"func": {
		alias:       "func",
		canonical:   "fn",
		kind:        "stable_function_keyword",
		sourceHabit: "foreign_function_keyword",
		detail:      "accept `func` as a stable parser alias for `fn`",
	},
	"def": {
		alias:       "def",
		canonical:   "fn",
		kind:        "stable_function_keyword",
		sourceHabit: "foreign_function_keyword",
		detail:      "accept `def` as a stable parser alias for `fn`",
	},
	"function": {
		alias:       "function",
		canonical:   "fn",
		kind:        "stable_function_keyword",
		sourceHabit: "foreign_function_keyword",
		detail:      "accept `function` as a stable parser alias for `fn`",
	},
	"import": {
		alias:       "import",
		canonical:   "use",
		kind:        "stable_use_keyword",
		sourceHabit: "import_keyword",
		detail:      "accept `import` as a stable parser alias for `use`",
	},
	"while": {
		alias:       "while",
		canonical:   "for",
		kind:        "stable_while_keyword",
		sourceHabit: "while_condition_loop",
		detail:      "accept `while` as a stable parser alias for `for` loops",
	},
}

type stableAliasEdit struct {
	start int
	end   int
	text  []byte
	step  ProvenanceStep
}

func normalizeStableAliases(src []byte) ([]byte, []ProvenanceStep) {
	toks, _, _ := selfhost.Lex(src)
	var edits []stableAliasEdit

	for i, tok := range toks {
		if tok.Kind != token.IDENT {
			continue
		}
		spec, ok := stableAliasSpecs[tok.Value]
		if !ok {
			continue
		}
		// Preserve the identifier when it sits in `name : type` position
		// (parameter, struct field, keyword argument, struct-literal field,
		// map entry). Keyword aliases never legitimately appear there.
		if i+1 < len(toks) && toks[i+1].Kind == token.COLON {
			continue
		}
		replacement := aliasReplacement(spec.alias, spec.canonical)
		edits = append(edits, stableAliasEdit{
			start: tok.Pos.Offset,
			end:   tok.End.Offset,
			text:  replacement,
			step: ProvenanceStep{
				Kind:        spec.kind,
				SourceHabit: spec.sourceHabit,
				Span: diag.Span{
					Start: tok.Pos,
					End:   tok.End,
				},
				Detail: spec.detail,
			},
		})
	}
	if len(edits) == 0 {
		return src, nil
	}

	out := append([]byte(nil), src...)
	steps := make([]ProvenanceStep, 0, len(edits))
	lastStart := len(src) + 1
	for i := len(edits) - 1; i >= 0; i-- {
		edit := edits[i]
		if edit.start < 0 || edit.end < edit.start || edit.end > len(src) || edit.end > lastStart {
			continue
		}
		out = append(append([]byte(nil), out[:edit.start]...), append(edit.text, out[edit.end:]...)...)
		lastStart = edit.start
		steps = append(steps, edit.step)
	}
	reverseProvenanceSteps(steps)
	return out, steps
}

func aliasReplacement(alias, canonical string) []byte {
	buf := bytes.NewBuffer(make([]byte, 0, len(alias)))
	buf.WriteString(canonical)
	for i := len(canonical); i < len(alias); i++ {
		buf.WriteByte(' ')
	}
	return buf.Bytes()
}

func reverseProvenanceSteps(steps []ProvenanceStep) {
	for i, j := 0, len(steps)-1; i < j; i, j = i+1, j-1 {
		steps[i], steps[j] = steps[j], steps[i]
	}
}
