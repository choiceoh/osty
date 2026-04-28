package selfhost

import (
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/token"
)

// StableAliasProvenance records a stable keyword alias accepted by the
// self-hosted parser. Alias parsing lives in toolchain/parser.osty; this Go
// surface only exposes the provenance event to callers that want to report it.
type StableAliasProvenance struct {
	Alias       string
	Canonical   string
	Kind        string
	SourceHabit string
	Detail      string
	Span        diag.Span
}

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

// StableAliases returns stable keyword aliases accepted by this front-end run.
// Calling it does not materialize the public *ast.File.
func (r *FrontendRun) StableAliases() []StableAliasProvenance {
	if r == nil {
		return nil
	}
	r.ensureLexAdapted()
	return collectStableAliasProvenanceFromTokens(r.toks)
}

func collectStableAliasProvenanceFromTokens(toks []token.Token) []StableAliasProvenance {
	var steps []StableAliasProvenance
	for i, tok := range toks {
		if tok.Kind != token.IDENT {
			continue
		}
		spec, ok := stableAliasSpecs[tok.Value]
		if !ok {
			continue
		}
		if i+1 < len(toks) && toks[i+1].Kind == token.COLON {
			continue
		}
		if isAliasInExpressionPosition(toks, i) {
			continue
		}
		steps = append(steps, StableAliasProvenance{
			Alias:       spec.alias,
			Canonical:   spec.canonical,
			Kind:        spec.kind,
			SourceHabit: spec.sourceHabit,
			Detail:      spec.detail,
			Span: diag.Span{
				Start: tok.Pos,
				End:   tok.End,
			},
		})
	}
	return steps
}

// isAliasInExpressionPosition reports whether toks[i] is clearly used as a
// value, binding target, or member access rather than a statement-head keyword.
// False positives only hide provenance; they do not change parsing.
func isAliasInExpressionPosition(toks []token.Token, i int) bool {
	if i+1 < len(toks) {
		next := toks[i+1].Kind
		switch next {
		case token.ASSIGN, token.PLUSEQ, token.MINUSEQ, token.STAREQ,
			token.SLASHEQ, token.PERCENTEQ, token.BITANDEQ, token.BITOREQ,
			token.BITXOREQ, token.SHLEQ, token.SHREQ:
			return true
		}
		switch next {
		case token.DOT, token.QDOT, token.COMMA, token.RPAREN,
			token.RBRACKET, token.RBRACE:
			return true
		}
	}
	if i > 0 {
		prev := toks[i-1].Kind
		switch prev {
		case token.LET, token.MUT, token.DOT, token.QDOT,
			token.COMMA, token.LPAREN, token.LBRACKET,
			token.ASSIGN, token.PLUSEQ, token.MINUSEQ, token.STAREQ,
			token.SLASHEQ, token.PERCENTEQ, token.BITANDEQ, token.BITOREQ,
			token.BITXOREQ, token.SHLEQ, token.SHREQ,
			token.RETURN, token.ARROW, token.CHANARROW:
			return true
		}
		switch prev {
		case token.PLUS, token.MINUS, token.STAR, token.SLASH,
			token.PERCENT, token.BITAND, token.BITOR, token.BITXOR,
			token.SHL, token.SHR, token.AND, token.OR,
			token.EQ, token.NEQ, token.LT, token.LEQ, token.GT, token.GEQ,
			token.DOTDOT, token.DOTDOTEQ, token.QQ, token.NOT:
			return true
		}
	}
	return false
}
