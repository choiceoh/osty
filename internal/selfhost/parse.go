package selfhost

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
)

// Parse runs the bootstrapped pure-Osty lexer and parser, then lowers the
// self-hosted arena into the compiler's public AST.
func Parse(src []byte) (*ast.File, []*diag.Diagnostic) {
	run := Run(src)
	return astLowerPublicFile(run.parser.arena, run.Tokens()), run.Diagnostics()
}
