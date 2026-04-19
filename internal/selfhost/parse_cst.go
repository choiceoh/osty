package selfhost

import (
	"github.com/osty/osty/internal/cst"
	"github.com/osty/osty/internal/diag"
)

// ParseCST lexes and parses src, then lifts the parsed AST plus its token
// stream into a concrete-syntax Red/Green tree. The returned *cst.Tree is a
// lossless projection — every source byte (after CRLF normalization) is
// reachable from the tree.
//
// Relationship to Parse:
//
//   - Parse returns the semantic *ast.File used by resolver, checker, and
//     generators. It is NOT lossless (no trivia).
//   - ParseCST adds the trivia-preserving tree. Consumers that need to
//     reproduce source text (formatter, LSP hover highlights, incremental
//     reparse candidates) use this variant.
//
// Diagnostics are identical to Parse — no new analysis is performed.
func ParseCST(src []byte) (*cst.Tree, []*diag.Diagnostic) {
	normalized := cst.Normalize(src)
	run := runFrontend(normalized, true)
	toks := run.Tokens()
	trivias := cst.Extract(normalized, toks)
	file := run.File()
	tree := cst.BuildFromParsed(normalized, file, toks, trivias)
	return tree, run.Diagnostics()
}
