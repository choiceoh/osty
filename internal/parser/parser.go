// Package parser exposes Osty's parser API.
package parser

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/docgen"
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

// ParseDiagnostics is the structured form of Parse. It returns the rich
// diagnostic objects so callers can render snippets, hints, and codes.
func ParseDiagnostics(src []byte) (*ast.File, []*diag.Diagnostic) {
	return docgen.Parse(src)
}
