package ir

import "strings"

// Pattern is the common interface for every destructuring pattern.
// Patterns appear in `let` bindings, `match` arms, `if let`/`for let`
// heads, and closure parameter lists.
//
// Every Pattern carries a Span (via At) and self-describes its shape
// — no back-reference to the AST or resolver is retained.
type Pattern interface {
	Node
	patternNode()
}

// WildPat is `_`.
type WildPat struct{ SpanV Span }

func (*WildPat) patternNode() {}
func (p *WildPat) At() Span   { return p.SpanV }

// IdentPat binds a name. Mut reflects a `mut` modifier at the binding
// site (used by closures and let stmts).
type IdentPat struct {
	Name  string
	Mut   bool
	SpanV Span
}

func (*IdentPat) patternNode() {}
func (p *IdentPat) At() Span   { return p.SpanV }

// LitPat matches an exact literal value. Value is an already-lowered
// expression, always one of the primitive-literal nodes (IntLit,
// FloatLit, StringLit, CharLit, BoolLit, ByteLit).
type LitPat struct {
	Value Expr
	SpanV Span
}

func (*LitPat) patternNode() {}
func (p *LitPat) At() Span   { return p.SpanV }

// TuplePat matches tuples positionally.
type TuplePat struct {
	Elems []Pattern
	SpanV Span
}

func (*TuplePat) patternNode() {}
func (p *TuplePat) At() Span   { return p.SpanV }

// StructPat matches a struct shape. Rest is true when the pattern ends
// with `..` (accept any remaining fields). TypeName is the struct's
// bare name; a backend that needs a qualified form resolves it off the
// type parameter on the scrutinee.
type StructPat struct {
	TypeName string
	Fields   []StructPatField
	Rest     bool
	SpanV    Span
}

func (*StructPat) patternNode() {}
func (p *StructPat) At() Span   { return p.SpanV }

// StructPatField is one entry inside StructPat. When Pattern is nil the
// entry is a shorthand binding (`name` binds to the field of the same
// name).
type StructPatField struct {
	Name    string
	Pattern Pattern
	SpanV   Span
}

func (f StructPatField) At() Span { return f.SpanV }

// VariantPat matches a specific enum variant. Enum is the owning enum
// name when known (empty for prelude variants like `Some`/`None` whose
// owner may be Option<T>). Args is empty for bare variants.
type VariantPat struct {
	Enum    string
	Variant string
	Args    []Pattern
	SpanV   Span
}

func (*VariantPat) patternNode() {}
func (p *VariantPat) At() Span   { return p.SpanV }

// RangePat matches a range of ordered values (`0..=9`, `..10`, `100..`).
// Low and High may be nil for unbounded sides.
type RangePat struct {
	Low       Expr
	High      Expr
	Inclusive bool
	SpanV     Span
}

func (*RangePat) patternNode() {}
func (p *RangePat) At() Span   { return p.SpanV }

// OrPat is `A | B | C`.
type OrPat struct {
	Alts  []Pattern
	SpanV Span
}

func (*OrPat) patternNode() {}
func (p *OrPat) At() Span   { return p.SpanV }

// BindingPat is `name @ pattern`: succeeds iff Pattern matches, and in
// that case also binds `name` to the scrutinee.
type BindingPat struct {
	Name    string
	Pattern Pattern
	SpanV   Span
}

func (*BindingPat) patternNode() {}
func (p *BindingPat) At() Span   { return p.SpanV }

// ErrorPat is the poisoned pattern, used when lowering can't recover.
type ErrorPat struct {
	Note  string
	SpanV Span
}

func (*ErrorPat) patternNode() {}
func (p *ErrorPat) At() Span   { return p.SpanV }

// ==== Pattern helpers ====

// PatternString returns a source-like rendering of p. Useful for
// diagnostics and printer output.
func PatternString(p Pattern) string {
	var b strings.Builder
	writePattern(&b, p)
	return b.String()
}

func writePattern(b *strings.Builder, p Pattern) {
	switch p := p.(type) {
	case nil:
		b.WriteString("<nil>")
	case *WildPat:
		b.WriteByte('_')
	case *IdentPat:
		if p.Mut {
			b.WriteString("mut ")
		}
		b.WriteString(p.Name)
	case *LitPat:
		b.WriteString(exprLiteralText(p.Value))
	case *TuplePat:
		b.WriteByte('(')
		for i, e := range p.Elems {
			if i > 0 {
				b.WriteString(", ")
			}
			writePattern(b, e)
		}
		b.WriteByte(')')
	case *StructPat:
		b.WriteString(p.TypeName)
		b.WriteString(" { ")
		for i, f := range p.Fields {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(f.Name)
			if f.Pattern != nil {
				b.WriteString(": ")
				writePattern(b, f.Pattern)
			}
		}
		if p.Rest {
			if len(p.Fields) > 0 {
				b.WriteString(", ")
			}
			b.WriteString("..")
		}
		b.WriteString(" }")
	case *VariantPat:
		if p.Enum != "" {
			b.WriteString(p.Enum)
			b.WriteByte('.')
		}
		b.WriteString(p.Variant)
		if len(p.Args) > 0 {
			b.WriteByte('(')
			for i, a := range p.Args {
				if i > 0 {
					b.WriteString(", ")
				}
				writePattern(b, a)
			}
			b.WriteByte(')')
		}
	case *RangePat:
		if p.Low != nil {
			b.WriteString(exprLiteralText(p.Low))
		}
		if p.Inclusive {
			b.WriteString("..=")
		} else {
			b.WriteString("..")
		}
		if p.High != nil {
			b.WriteString(exprLiteralText(p.High))
		}
	case *OrPat:
		for i, a := range p.Alts {
			if i > 0 {
				b.WriteString(" | ")
			}
			writePattern(b, a)
		}
	case *BindingPat:
		b.WriteString(p.Name)
		b.WriteString(" @ ")
		writePattern(b, p.Pattern)
	case *ErrorPat:
		b.WriteString("<error:")
		b.WriteString(p.Note)
		b.WriteByte('>')
	}
}

// exprLiteralText renders a simple literal-ish expression for use in
// pattern debug output. Non-literal expressions render via their
// underlying type, which is sufficient for our debug printer.
func exprLiteralText(e Expr) string {
	switch e := e.(type) {
	case *IntLit:
		return e.Text
	case *FloatLit:
		return e.Text
	case *BoolLit:
		if e.Value {
			return "true"
		}
		return "false"
	case *CharLit:
		return "'" + string(e.Value) + "'"
	case *ByteLit:
		return "b'?'"
	case *StringLit:
		var sb strings.Builder
		sb.WriteByte('"')
		for _, part := range e.Parts {
			if part.IsLit {
				sb.WriteString(part.Lit)
			} else {
				sb.WriteByte('{')
				sb.WriteString("expr")
				sb.WriteByte('}')
			}
		}
		sb.WriteByte('"')
		return sb.String()
	case *Ident:
		return e.Name
	}
	return "<expr>"
}
