// Package parser parses an Osty source file into an AST.
// This package is a thin facade over internal/selfhost.
package parser

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/cst"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/selfhost"
)

// Error is retained as an alias for back-compat. New code should use
// diag.Diagnostic directly.
type Error = diag.Diagnostic

// Result is the full parse pipeline output, including parser-level
// normalization provenance for callers that need to surface or retain how
// foreign syntax was absorbed into canonical Osty.
type Result struct {
	File        *ast.File
	Run         *selfhost.FrontendRun
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

// ParseDetailed lexes, parses, and canonicalizes src, returning the public
// semantic AST plus parser-level compatibility provenance. Compatibility
// helper syntax is canonicalized in the parser core; this facade only records
// source alias provenance that remains useful at legacy boundaries. The public
// AST is produced through the explicit compatibility adapter so ParseDetailed
// does not exercise FrontendRun.File's legacy astbridge entry point.
func ParseDetailed(src []byte) Result {
	pipeline := newParsePipeline(src)
	run := pipeline.parseRun()
	pipeline.applySourceCompat(run)
	file, diags := selfhost.LowerPublicFileFromRun(run), run.Diagnostics()
	return pipeline.result(run, file, diags)
}

// ParseCanonical parses trusted source and returns the public semantic AST
// without collecting the compatibility provenance that ParseDetailed records for
// user-authored code.
//
// The returned file is lowered from the same semantic arena used by ParseRun's
// native consumers; no host-side AST fixup pass runs after astbridge lowering.
func ParseCanonical(src []byte) (*ast.File, []*diag.Diagnostic) {
	pipeline := newParsePipeline(src)
	run := pipeline.parseRun()
	return selfhost.LowerPublicFileFromRun(run), run.Diagnostics()
}

// ParseDiagnostics lexes and parses src, returning the AST and rich
// diagnostics. This is the primary entry point for all compiler passes.
func ParseDiagnostics(src []byte) (*ast.File, []*diag.Diagnostic) {
	result := ParseDetailed(src)
	return result.File, result.Diagnostics
}

// ParseRun lexes and parses src and returns the underlying selfhost
// FrontendRun without lowering the result to the public *ast.File semantic AST.
// Callers that only need the Osty-native parser arena (native resolver, native
// checker, native llvmgen) should use this entry point so astbridge-based
// public lowering is not triggered. Calling run.File() afterwards remains valid
// if the *ast.File is eventually needed; it is computed lazily from the
// semantic arena on first access.
func ParseRun(src []byte) *selfhost.FrontendRun {
	return selfhost.Run(src)
}

// ParseCST lexes and parses src, returning the lossless Red/Green concrete
// syntax tree plus the same diagnostics reported by the semantic parse path.
// Use this entry point for formatter, repair, and LSP features that need
// byte-for-byte source coverage instead of only the semantic AST.
func ParseCST(src []byte) (*cst.Tree, []*diag.Diagnostic) {
	return selfhost.ParseCST(src)
}
