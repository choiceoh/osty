// Package diag provides structured diagnostics for the Osty toolchain.
//
// A Diagnostic carries enough information to produce a Rust-style report:
// a severity, a code, the primary span pointing into the source, optional
// secondary spans, explanatory notes, and a hint that suggests a concrete
// fix. The Formatter renders Diagnostics with caret underlines, line
// numbers, and (optionally) ANSI colors.
//
// The lexer and parser populate Diagnostics as they go; downstream tools
// (CLI, LSP) consume them via the formatter or by walking the structured
// fields directly.
package diag

import "github.com/osty/osty/internal/token"

// Severity classifies a diagnostic.
type Severity int

const (
	// Error is a recoverable parse/lex error. The toolchain may continue
	// processing to surface multiple errors at once but should not emit
	// a successful build.
	Error Severity = iota
	// Warning is a style or pedantic concern that does not prevent
	// further processing.
	Warning
	// Note is supplementary information attached to another diagnostic.
	Note
)

func (s Severity) String() string {
	switch s {
	case Error:
		return "error"
	case Warning:
		return "warning"
	case Note:
		return "note"
	}
	return "unknown"
}

// Span is a half-open range [Start, End) within a single source file.
type Span struct {
	Start token.Pos
	End   token.Pos
}

// LabeledSpan is a span with an attached label, used to highlight the
// individual sources of a diagnostic. The primary span is rendered
// distinctly from secondary spans.
type LabeledSpan struct {
	Span    Span
	Label   string // short label rendered next to the caret line
	Primary bool   // true for the main location, false for context
}

// Diagnostic is a single problem report.
//
// The fields are deliberately granular so callers can render diagnostics in
// multiple formats (terminal, LSP, JSON) without re-parsing the message.
type Diagnostic struct {
	Severity Severity
	// Code is an optional stable identifier (e.g. "E0007"). Empty when the
	// diagnostic doesn't have a code yet.
	Code string
	// Message is the one-line headline. It SHOULD start with a lowercase
	// verb phrase ("expected `}`", "non-associative operator chain") and
	// SHOULD NOT end with a period.
	Message string
	// Spans points at the locations in the source that the diagnostic
	// concerns. The first primary span (if any) is used as the canonical
	// location for sorting and short-form rendering.
	Spans []LabeledSpan
	// Notes are additional explanatory paragraphs printed after the source
	// snippet. Each note is one short paragraph.
	Notes []string
	// Hint is an optional one-line fix suggestion in prose form,
	// rendered as "help: ..." after the notes.
	Hint string
	// Suggestions are structured, potentially machine-applicable patches.
	// A tool (LSP quick-fix, --fix flag) may apply these automatically when
	// MachineApplicable is true.
	Suggestions []Suggestion
}

// Suggestion is a structured fix: replace the text covered by Span with
// Replacement. An empty Span (zero Start/End) means "insert at that
// position"; an empty Replacement means "delete the span". Label is the
// human-readable description.
//
// CopyFrom, when non-nil, tells the fix applier to use the source text
// covered by that span as the replacement body, optionally wrapped by
// Replacement's Prefix/Suffix split on a literal "%s" placeholder. This
// lets rules propose "replace `!!x` with `x`" or "replace `x == true`
// with `x`" without needing to read the source at lint time:
//
//	Replacement = "%s"     → copy the inner span verbatim
//	Replacement = "!(%s)"  → copy wrapped in `!( ... )`
//
// Exactly one "%s" marker is expected when CopyFrom is set; if missing
// the applier falls back to a plain copy.
type Suggestion struct {
	Span              Span
	Replacement       string
	CopyFrom          *Span
	Label             string
	MachineApplicable bool
}

// PrimaryPos returns the start position of the first primary span, or the
// first span's start if no primary span is present, or the zero Pos if
// there are no spans.
func (d *Diagnostic) PrimaryPos() token.Pos {
	for _, s := range d.Spans {
		if s.Primary {
			return s.Span.Start
		}
	}
	if len(d.Spans) > 0 {
		return d.Spans[0].Span.Start
	}
	return token.Pos{}
}

// Error implements the error interface so a Diagnostic can be returned
// from functions that conventionally return error.
func (d *Diagnostic) Error() string {
	pos := d.PrimaryPos()
	if d.Code != "" {
		return d.Code + ": " + d.Severity.String() + " at " + pos.String() + ": " + d.Message
	}
	return d.Severity.String() + " at " + pos.String() + ": " + d.Message
}

// Builder offers a fluent API for assembling a Diagnostic.
//
// Typical use:
//
//	diag.New(diag.Error, "expected `}`, got `else`").
//	    Code("E0002").
//	    Primary(span, "expected `}` here").
//	    Note("the closing `}` of the `if` block was reached").
//	    Hint("place `else` on the same line as `}` (v0.2 O2)")
type Builder struct {
	d *Diagnostic
}

// New starts a Diagnostic with the given severity and headline.
func New(sev Severity, message string) *Builder {
	return &Builder{d: &Diagnostic{Severity: sev, Message: message}}
}

// Code sets the stable identifier.
func (b *Builder) Code(code string) *Builder { b.d.Code = code; return b }

// Primary attaches a primary span with an optional label.
func (b *Builder) Primary(span Span, label string) *Builder {
	b.d.Spans = append(b.d.Spans, LabeledSpan{Span: span, Label: label, Primary: true})
	return b
}

// PrimaryPos is a convenience for span = [pos, pos).
func (b *Builder) PrimaryPos(pos token.Pos, label string) *Builder {
	return b.Primary(Span{Start: pos, End: pos}, label)
}

// Secondary attaches a secondary span (context, not the main culprit).
func (b *Builder) Secondary(span Span, label string) *Builder {
	b.d.Spans = append(b.d.Spans, LabeledSpan{Span: span, Label: label, Primary: false})
	return b
}

// Note appends an explanatory note.
func (b *Builder) Note(text string) *Builder {
	b.d.Notes = append(b.d.Notes, text)
	return b
}

// Hint sets the suggestion line.
func (b *Builder) Hint(text string) *Builder { b.d.Hint = text; return b }

// Suggest attaches a structured fix: replace the text at span with
// replacement, labelled for the user. MachineApplicable=true marks it
// safe for tools to auto-apply.
func (b *Builder) Suggest(span Span, replacement, label string, machineApplicable bool) *Builder {
	b.d.Suggestions = append(b.d.Suggestions, Suggestion{
		Span:              span,
		Replacement:       replacement,
		Label:             label,
		MachineApplicable: machineApplicable,
	})
	return b
}

// SuggestCopy attaches a structured fix that replaces the text at span
// with the source text covered by copyFrom, optionally wrapped via a
// template such as "!(%s)" or "%s".
//
// Use this when a rule wants to rewrite a construct to one of its
// sub-expressions (e.g. `!!x` → `x`, `x == true` → `x`) without
// the lint pass needing access to the raw source bytes.
func (b *Builder) SuggestCopy(span, copyFrom Span, template, label string, machineApplicable bool) *Builder {
	cf := copyFrom
	b.d.Suggestions = append(b.d.Suggestions, Suggestion{
		Span:              span,
		Replacement:       template,
		CopyFrom:          &cf,
		Label:             label,
		MachineApplicable: machineApplicable,
	})
	return b
}

// Build returns the finished Diagnostic.
func (b *Builder) Build() *Diagnostic { return b.d }
