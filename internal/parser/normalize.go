package parser

import (
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

func collectStableAliasProvenance(src []byte) []ProvenanceStep {
	toks, _, _ := selfhost.Lex(src)
	var steps []ProvenanceStep

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
		// Preserve the identifier when context makes it clear we're in
		// expression / binding position rather than at a statement head.
		// Without this, code like `let mut def = ...` (with `def` as a
		// variable name) gets rewritten to `let mut fn = ...` and the
		// parser promptly explodes.
		//
		// The rule is stricter than "any expression position" because
		// some alias source habits (e.g. `while cond { ... }`) appear
		// in statement position where prev is TERMINATOR or LBRACE.
		// What we care about is: does the token immediately before /
		// after make keyword interpretation impossible?
		if isAliasInExpressionPosition(toks, i) {
			continue
		}
		steps = append(steps, ProvenanceStep{
			Kind:        spec.kind,
			SourceHabit: spec.sourceHabit,
			Span: diag.Span{
				Start: tok.Pos,
				End:   tok.End,
			},
			Detail: spec.detail,
		})
	}
	return steps
}

// isAliasInExpressionPosition reports whether the identifier at
// toks[i] is clearly used as a value / binding target / member access
// rather than a statement-head keyword. When true, stable-alias provenance
// must be suppressed to avoid flagging user identifiers that happen
// to share a spelling with a stable-alias source habit (common for
// `def`, `func`, `while`, etc. used as variable names in toolchain
// code).
//
// The rule set targets the concrete patterns observed in the
// toolchain source; it is intentionally conservative — false
// positives here only degrade alias convenience, never miscompile.
func isAliasInExpressionPosition(toks []token.Token, i int) bool {
	// Next token: if it's an assignment, this is the LHS of `let [mut]
	// NAME = ...` or `NAME = ...` — never a keyword.
	if i+1 < len(toks) {
		next := toks[i+1].Kind
		switch next {
		case token.ASSIGN, token.PLUSEQ, token.MINUSEQ, token.STAREQ,
			token.SLASHEQ, token.PERCENTEQ, token.BITANDEQ, token.BITOREQ,
			token.BITXOREQ, token.SHLEQ, token.SHREQ:
			return true
		}
		// `NAME.field` / `NAME?.field` / `NAME,` / `NAME)` / `NAME]` —
		// expression use, never a keyword.
		switch next {
		case token.DOT, token.QDOT, token.COMMA, token.RPAREN,
			token.RBRACKET, token.RBRACE:
			return true
		}
	}
	// Previous token indicates expression / binding context:
	//   let / let mut NAME   — LET or MUT precedes
	//   fn foo(..., NAME)    — COMMA or LPAREN precedes
	//   expr.NAME / expr?.NAME — DOT / QDOT precedes
	//   return NAME / -> NAME — RETURN / ARROW precedes
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
		// Binary operators place us in RHS expression territory.
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
