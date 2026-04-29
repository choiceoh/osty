package selfhost

import (
	"github.com/osty/osty/internal/cst"
	"github.com/osty/osty/internal/diag"
)

// ParseCST lexes and parses src, then feeds the token stream into the native
// concrete-syntax Green parser. The returned *cst.Tree is lossless: every
// source byte (after CRLF normalization) is reachable from the tree.
//
// Relationship to Parse:
//
//   - Parse returns the semantic *ast.File used by resolver, checker, and
//     generators. It is NOT lossless (no trivia).
//   - ParseCST adds the trivia-preserving tree. Consumers that need to
//     reproduce source text (formatter, LSP hover highlights, incremental
//     reparse candidates) use this variant.
//
// Diagnostics are identical to Parse: the semantic parser still owns language
// diagnostics, while the CST parser owns only the lossless tree.
func ParseCST(src []byte) (*cst.Tree, []*diag.Diagnostic) {
	normalized := cst.Normalize(src)
	run := runFrontend(normalized, true)
	return ParseCSTFromRun(run, normalized)
}

// ParseCSTFromRun builds the native CST from an existing front-end run.
// normalized must be the CRLF-normalized source bytes used to create run.
func ParseCSTFromRun(run *FrontendRun, normalized []byte) (*cst.Tree, []*diag.Diagnostic) {
	if run == nil {
		return nil, nil
	}
	toks := run.Tokens()
	trivias := cst.Extract(normalized, toks)
	tree := cst.ParseGreen(normalized, toks, trivias)
	return tree, run.Diagnostics()
}
