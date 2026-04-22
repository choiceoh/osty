// Package parser parses an Osty source file into an AST.
// This package is a thin facade over internal/selfhost.
package parser

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/selfhost"
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

// ParseCanonical parses trusted source without collecting the parser-owned
// compatibility provenance that ParseDetailed records for user-authored code.
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

// ParseRun lexes and parses src and returns the underlying selfhost
// FrontendRun without lowering the result to the *ast.File semantic AST.
// Callers that only need the Osty-native parser arena (native resolver,
// native checker, native llvmgen) should use this entry point so the
// astbridge-based *ast.File lowering is not triggered. Calling
// run.File() afterwards remains valid if the *ast.File is eventually
// needed — it is computed lazily on first access.
func ParseRun(src []byte) *selfhost.FrontendRun {
	return selfhost.Run(src)
}
