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

// Parse lexes src and returns the parsed File along with collected errors.
func Parse(src []byte) (*ast.File, []error) {
	file, diags := ParseDiagnostics(src)
	errs := make([]error, len(diags))
	for i, d := range diags {
		errs[i] = d
	}
	return file, errs
}

// ParseDiagnostics lexes and parses src, returning the AST and rich
// diagnostics. This is the primary entry point for all compiler passes.
func ParseDiagnostics(src []byte) (*ast.File, []*diag.Diagnostic) {
	return selfhost.Parse(src)
}

// ParseCST lexes and parses src, then lifts the result into a lossless
// Red/Green concrete syntax tree. Every source byte (after CRLF
// normalization) is reachable from the returned tree — trivia is preserved
// with same-line trailing / next-line leading semantics suitable for
// formatters and LSP consumers.
//
// The diagnostics slice matches ParseDiagnostics; no additional analysis is
// performed by lifting the tree.
func ParseCST(src []byte) (*cst.Tree, []*diag.Diagnostic) {
	return selfhost.ParseCST(src)
}
