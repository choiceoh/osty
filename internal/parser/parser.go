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
	normalized, aliases := normalizeStableAliases(src)
	// v0.5 (G28): expand `use path::{a, b as c}` into flat `use
	// path.a; use path.b as c` lines before the self-hosted parser
	// runs. Pure source rewrite — no AST shape change, offsets shift
	// downward from the expansion point and provenance records each
	// expansion.
	expanded, scopedSteps := expandScopedImports(normalized)
	file, diags := selfhost.Parse(expanded)
	prov := &Provenance{}
	if len(aliases) > 0 {
		prov.Aliases = aliases
	}
	if len(scopedSteps) > 0 {
		prov.Lowerings = append(prov.Lowerings, scopedSteps...)
	}
	if file != nil {
		if lowerings := lowerStableAST(file); len(lowerings) > 0 {
			prov.Lowerings = append(prov.Lowerings, lowerings...)
		}
		// v0.5 (G30): the self-hosted parser silently drops `pub`
		// before `use`; flip IsPub on affected UseDecls here until
		// the bootstrap regen pipeline is restored and the flag can
		// be carried through the AST lowerer.
		markPubUseDecls(expanded, file)
	}
	if prov.Empty() {
		prov = nil
	}
	return Result{
		File:        file,
		Diagnostics: diags,
		Provenance:  prov,
	}
}

// ParseDiagnostics lexes and parses src, returning the AST and rich
// diagnostics. This is the primary entry point for all compiler passes.
func ParseDiagnostics(src []byte) (*ast.File, []*diag.Diagnostic) {
	result := ParseDetailed(src)
	return result.File, result.Diagnostics
}
