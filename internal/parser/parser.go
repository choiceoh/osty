// Package parser parses an Osty source file into an AST.
// This package is a thin facade over internal/selfhost.
package parser

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
)

// Error is retained as an alias for back-compat. New code should use
// diag.Diagnostic directly.
type Error = diag.Diagnostic

// Result is the full parse pipeline output, including parser-level
// normalization/lowering provenance for callers that need to surface or retain
// how foreign syntax was absorbed into canonical Osty.
type Result struct {
	File        *ast.File
	Diagnostics []*diag.Diagnostic
	Provenance  *Provenance
}

// Parse lexes src and returns the parsed File along with collected errors.
func Parse(src []byte) (*ast.File, []error) {
	file, diags := ParseDiagnostics(src)
	errs := make([]error, len(diags))
	for i, d := range diags {
		errs[i] = d
	}
	return file, errs
}

// ParseDetailed lexes, parses, and canonicalizes src, returning the semantic
// AST plus parser-level provenance.
func ParseDetailed(src []byte) Result {
	pipeline := newParsePipeline(src)
	pipeline.applySourceCompat()
	file, diags := pipeline.parse()
	pipeline.applyASTFixups(file)
	return pipeline.result(file, diags)
}

// ParseCanonical parses already-canonical source without running the
// source-level compatibility rewrites that ParseDetailed applies for
// user-authored code. Callers should use this only for trusted inputs that do
// not rely on stable-alias keywords or scoped-import expansion.
//
// Unlike calling selfhost.Parse directly, this still applies the parser's
// shared AST fixups so canonical sources that use lowered surface forms such
// as builtin `len(...)` continue to match the rest of the compiler pipeline.
func ParseCanonical(src []byte) (*ast.File, []*diag.Diagnostic) {
	pipeline := newParsePipeline(src)
	file, diags := pipeline.parse()
	pipeline.applyASTFixups(file)
	return file, diags
}

// ParseDiagnostics lexes and parses src, returning the AST and rich
// diagnostics. This is the primary entry point for all compiler passes.
func ParseDiagnostics(src []byte) (*ast.File, []*diag.Diagnostic) {
	result := ParseDetailed(src)
	return result.File, result.Diagnostics
}
