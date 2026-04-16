<<<<<<<< HEAD:internal/docgen/frontend_parse.go
package docgen
========
package golegacy
>>>>>>>> b3eba2c (Rename selfhost paths to toolchain and golegacy):internal/golegacy/parse.go

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
