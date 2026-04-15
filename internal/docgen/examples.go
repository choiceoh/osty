package docgen

import (
	"fmt"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
)

// Example is one `Example:` snippet extracted from a decl's doc
// comment, paired with the context needed to report errors back at the
// author (module path + decl name).
type Example struct {
	Module string // source file the example was attached to
	Decl   string // kind+name (e.g. "function add")
	Line   int    // 1-based line of the host decl, for error output
	Code   string // the cleaned snippet (shared indent stripped)
}

// Examples walks every decl in pkg and returns its Example snippets in
// source order. Walks methods nested under structs/enums/interfaces
// too, so a class's per-method examples are surfaced alongside the
// top-level decls'.
func Examples(pkg *Package) []Example {
	var out []Example
	for _, m := range pkg.Modules {
		for _, d := range m.Decls {
			out = append(out, collectExamples(m.Path, d)...)
		}
	}
	return out
}

func collectExamples(modulePath string, d *Decl) []Example {
	var out []Example
	for _, ex := range d.Info.Examples {
		out = append(out, Example{
			Module: modulePath,
			Decl:   d.Kind.String() + " " + d.Name,
			Line:   d.Line,
			Code:   ex,
		})
	}
	for _, m := range d.Methods {
		out = append(out, collectExamples(modulePath, m)...)
	}
	return out
}

// ExampleError is one verifier failure: the example that refused to
// parse plus the parse diagnostics that describe why. Positions inside
// each diag are relative to the snippet's own byte offsets.
type ExampleError struct {
	Example Example
	Diags   []*diag.Diagnostic
}

// Format renders an ExampleError to a single multi-line string for
// stderr / report output. Uses the first diag's primary-position
// column so the user can find the broken line inside the snippet
// without dumping the whole AST.
func (e ExampleError) Format() string {
	head := fmt.Sprintf("%s: example in %s failed:", e.Example.Module, e.Example.Decl)
	if len(e.Diags) == 0 {
		return head + " unknown parse failure"
	}
	lines := []byte(head)
	for _, d := range e.Diags {
		pos := d.PrimaryPos()
		lines = append(lines, '\n')
		lines = append(lines,
			[]byte(fmt.Sprintf("  %d:%d: %s", pos.Line, pos.Column, d.Message))...)
	}
	return string(lines)
}

// VerifyExamples runs the lexer + parser against every extracted
// Example snippet and returns one ExampleError per snippet that fails
// to parse. Parse-only on purpose: resolve / type-check need a prelude
// and a project context the doc generator doesn't carry. Syntactic
// drift is by far the most common example-rot failure mode in
// practice, so parse coverage catches the overwhelming majority
// without pulling in the full front-end.
//
// An empty return value means every example parsed clean.
func VerifyExamples(pkg *Package) []ExampleError {
	var errs []ExampleError
	for _, ex := range Examples(pkg) {
		if es := verifyOne(ex); es != nil {
			errs = append(errs, ExampleError{Example: ex, Diags: es})
		}
	}
	return errs
}

// verifyOne parses a single snippet. Returns non-nil diags only when
// at least one is error-severity — warnings alone don't fail
// verification (users may legitimately author examples with unused
// bindings, and we don't want doc-example verification to become a
// stricter lint than `osty check`).
func verifyOne(ex Example) []*diag.Diagnostic {
	_, diags := parser.ParseDiagnostics([]byte(ex.Code))
	var errs []*diag.Diagnostic
	for _, d := range diags {
		if d.Severity == diag.Error {
			errs = append(errs, d)
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return errs
}
