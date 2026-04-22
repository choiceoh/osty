// Package token defines tokens produced by the Osty lexer.
package token

import "fmt"

// Kind identifies the lexical category of a token.
type Kind int

const (
	EOF Kind = iota
	ILLEGAL
	NEWLINE // implicit statement terminator

	// Literals
	IDENT
	LABEL // 'label
	INT
	FLOAT
	CHAR
	BYTE
	// STRING represents a string literal. When the literal contains no
	// interpolation it has a single Part of kind PartText. When it contains
	// interpolations, the Parts alternate between PartText (literal text with
	// escapes already processed) and PartExpr (tokens of the embedded
	// expression, already lexed).
	STRING
	RAWSTRING

	// Keywords (17).
	FN
	STRUCT
	ENUM
	INTERFACE
	TYPE
	LET
	MUT
	PUB
	IF
	ELSE
	MATCH
	FOR
	BREAK
	CONTINUE
	RETURN
	USE
	DEFER

	// Punctuation.
	LPAREN    // (
	RPAREN    // )
	LBRACE    // {
	RBRACE    // }
	LBRACKET  // [
	RBRACKET  // ]
	COMMA     // ,
	COLON     // :
	SEMICOLON // ; — not used for statements, present for future.
	DOT       // .

	// Operators.
	PLUS    // +
	MINUS   // -
	STAR    // *
	SLASH   // /
	PERCENT // %

	EQ  // ==
	NEQ // !=
	LT  // <
	GT  // >
	LEQ // <=
	GEQ // >=

	AND // &&
	OR  // ||
	NOT // !

	BITAND // &
	BITOR  // |
	BITXOR // ^
	BITNOT // ~
	SHL    // <<
	SHR    // >>

	ASSIGN    // =
	PLUSEQ    // +=
	MINUSEQ   // -=
	STAREQ    // *=
	SLASHEQ   // /=
	PERCENTEQ // %=
	BITANDEQ  // &=
	BITOREQ   // |=
	BITXOREQ  // ^=
	SHLEQ     // <<=
	SHREQ     // >>=

	QUESTION // ?
	ASQUESTION // as?
	QDOT     // ?.
	QQ       // ??

	DOTDOT   // ..
	DOTDOTEQ // ..=

	ARROW     // ->
	CHANARROW // <-

	COLONCOLON // ::
	UNDERSCORE // _ (standalone)
	AT         // @
	HASH       // # (annotation prefix; v0.2 R26/O1)
)

// Names for debug/pretty-printing.
var kindNames = [...]string{
	EOF:        "EOF",
	ILLEGAL:    "ILLEGAL",
	NEWLINE:    "NEWLINE",
	IDENT:      "IDENT",
	LABEL:      "LABEL",
	INT:        "INT",
	FLOAT:      "FLOAT",
	CHAR:       "CHAR",
	BYTE:       "BYTE",
	STRING:     "STRING",
	RAWSTRING:  "RAWSTRING",
	FN:         "fn",
	STRUCT:     "struct",
	ENUM:       "enum",
	INTERFACE:  "interface",
	TYPE:       "type",
	LET:        "let",
	MUT:        "mut",
	PUB:        "pub",
	IF:         "if",
	ELSE:       "else",
	MATCH:      "match",
	FOR:        "for",
	BREAK:      "break",
	CONTINUE:   "continue",
	RETURN:     "return",
	USE:        "use",
	DEFER:      "defer",
	LPAREN:     "(",
	RPAREN:     ")",
	LBRACE:     "{",
	RBRACE:     "}",
	LBRACKET:   "[",
	RBRACKET:   "]",
	COMMA:      ",",
	COLON:      ":",
	SEMICOLON:  ";",
	DOT:        ".",
	PLUS:       "+",
	MINUS:      "-",
	STAR:       "*",
	SLASH:      "/",
	PERCENT:    "%",
	EQ:         "==",
	NEQ:        "!=",
	LT:         "<",
	GT:         ">",
	LEQ:        "<=",
	GEQ:        ">=",
	AND:        "&&",
	OR:         "||",
	NOT:        "!",
	BITAND:     "&",
	BITOR:      "|",
	BITXOR:     "^",
	BITNOT:     "~",
	SHL:        "<<",
	SHR:        ">>",
	ASSIGN:     "=",
	PLUSEQ:     "+=",
	MINUSEQ:    "-=",
	STAREQ:     "*=",
	SLASHEQ:    "/=",
	PERCENTEQ:  "%=",
	BITANDEQ:   "&=",
	BITOREQ:    "|=",
	BITXOREQ:   "^=",
	SHLEQ:      "<<=",
	SHREQ:      ">>=",
	QUESTION:   "?",
	ASQUESTION: "as?",
	QDOT:       "?.",
	QQ:         "??",
	DOTDOT:     "..",
	DOTDOTEQ:   "..=",
	ARROW:      "->",
	CHANARROW:  "<-",
	COLONCOLON: "::",
	UNDERSCORE: "_",
	AT:         "@",
	HASH:       "#",
}

func (k Kind) String() string {
	if int(k) < 0 || int(k) >= len(kindNames) {
		return fmt.Sprintf("Kind(%d)", int(k))
	}
	if name := kindNames[k]; name != "" {
		return name
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// Pos is a byte offset + line + column in a source file.
type Pos struct {
	Offset int
	Line   int // 1-based
	Column int // 1-based, in Unicode code points
}

func (p Pos) String() string { return fmt.Sprintf("%d:%d", p.Line, p.Column) }

// StringPartKind distinguishes literal segments from interpolation expressions.
type StringPartKind int

const (
	PartText StringPartKind = iota
	PartExpr
)

// StringPart is one component of a STRING literal. A literal segment carries
// its processed text (with escape sequences resolved). An expression segment
// carries the tokens that were lexed inside `{ ... }`.
type StringPart struct {
	Kind StringPartKind
	Text string  // for PartText
	Expr []Token // for PartExpr
}

// Token is a single lexical unit.
type Token struct {
	Kind  Kind
	Pos   Pos
	End   Pos
	Value string // lexeme for non-string tokens
	// Parts is populated when Kind == STRING or RAWSTRING.
	Parts []StringPart
	// Triple is true iff this STRING/RAWSTRING was written in the
	// triple-quoted form (§1.6.3). The lexer has already normalized
	// content (indent stripping, leading-newline removal), but the
	// formatter uses this flag to preserve authorial style.
	Triple bool
	// LeadingDoc holds any `///` doc-comment lines that immediately
	// precede this token with no blank line between. The content is the
	// raw comment text with the `///` prefix and a single optional space
	// already stripped, and lines joined by `\n`.
	LeadingDoc string
}

func (t Token) String() string {
	if t.Value != "" {
		return fmt.Sprintf("%s(%q)@%s", t.Kind, t.Value, t.Pos)
	}
	return fmt.Sprintf("%s@%s", t.Kind, t.Pos)
}

// CommentKind distinguishes the three comment forms the lexer may
// encounter. Doc comments (`///`) are additionally attached to the
// following token's LeadingDoc field; line and block comments are
// retrievable only via Lexer.Comments(). Kept here so consumers
// outside the lexer package can type-switch without a circular import.
type CommentKind int

const (
	// CommentLine is `// ...` through end-of-line.
	CommentLine CommentKind = iota
	// CommentBlock is `/* ... */`, possibly spanning lines. Non-nesting
	// per §1.5.
	CommentBlock
	// CommentDoc is `/// ...` through end-of-line, possibly a
	// multi-line run joined by `\n` in the attached token.
	CommentDoc
)

// Comment is a single comment recovered from source. Text excludes the
// `//` / `///` / `/*` / `*/` delimiters and a single optional leading
// space (matching the lexer's doc-comment convention in §1.5). EndLine
// equals Pos.Line for `//` and `///`; for `/* */` it is the final line
// the block covers, used by the formatter for blank-line preservation.
type Comment struct {
	Kind    CommentKind
	Pos     Pos
	Text    string
	EndLine int
}

// Keywords maps the 17 reserved keywords to their kinds. Contextual
// identifiers (self, Self, true, false, Some, None, Ok, Err) are NOT listed
// here — they are lexed as IDENT and resolved by the parser/checker.
var Keywords = map[string]Kind{
	"fn":        FN,
	"struct":    STRUCT,
	"enum":      ENUM,
	"interface": INTERFACE,
	"type":      TYPE,
	"let":       LET,
	"mut":       MUT,
	"pub":       PUB,
	"if":        IF,
	"else":      ELSE,
	"match":     MATCH,
	"for":       FOR,
	"break":     BREAK,
	"continue":  CONTINUE,
	"return":    RETURN,
	"use":       USE,
	"defer":     DEFER,
}

// LookupKeyword returns the keyword kind for the given identifier text, or
// IDENT if it is not a keyword.
func LookupKeyword(s string) Kind {
	if k, ok := Keywords[s]; ok {
		return k
	}
	return IDENT
}
