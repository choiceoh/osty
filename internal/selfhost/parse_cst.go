package selfhost

import (
	"sort"

	"github.com/osty/osty/internal/cst"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/token"
)

// ParseCST lexes and parses src, then lifts the parser run plus its token
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
// This is still the compatibility CST path: structure is projected from the
// selfhost parser arena's top-level spans until the parser emits a native
// Green tree or lossless event stream. Keep that dependency explicit and local
// to this adapter.
//
// Diagnostics are identical to Parse — no new analysis is performed.
func ParseCST(src []byte) (*cst.Tree, []*diag.Diagnostic) {
	normalized := cst.Normalize(src)
	run := runFrontend(normalized, true)
	return ParseCSTFromRun(run, normalized)
}

// ParseCSTFromRun builds the compatibility CST from an existing front-end run.
// normalized must be the CRLF-normalized source bytes used to create run.
func ParseCSTFromRun(run *FrontendRun, normalized []byte) (*cst.Tree, []*diag.Diagnostic) {
	if run == nil {
		return nil, nil
	}
	toks := run.Tokens()
	trivias := cst.Extract(normalized, toks)
	entities := cstEntitySpansFromRun(run, toks)
	tree := cst.BuildFromEntitySpans(normalized, entities, toks, trivias)
	return tree, run.Diagnostics()
}

func cstEntitySpansFromRun(run *FrontendRun, toks []token.Token) []cst.EntitySpan {
	if run == nil || run.parser == nil || run.parser.arena == nil {
		return nil
	}
	return cstEntitySpansFromArena(run.parser.arena, toks)
}

func cstEntitySpansFromArena(arena *AstArena, toks []token.Token) []cst.EntitySpan {
	if arena == nil {
		return nil
	}
	entities := make([]cst.EntitySpan, 0, len(arena.decls))
	for _, idx := range arena.decls {
		appendCSTEntitySpan(&entities, arena, toks, idx)
	}
	sort.SliceStable(entities, func(i, j int) bool {
		if entities[i].Start == entities[j].Start {
			return entities[i].End < entities[j].End
		}
		return entities[i].Start < entities[j].Start
	})
	return entities
}

func appendCSTEntitySpan(out *[]cst.EntitySpan, arena *AstArena, toks []token.Token, idx int) {
	n := cstArenaNodeAt(arena, idx)
	if n == nil {
		return
	}
	if _, ok := n.kind.(*AstNodeKind_AstNUseDecl); ok && astUseDeclIsGroup(n) {
		for _, child := range n.children {
			appendCSTEntitySpan(out, arena, toks, child)
		}
		return
	}
	span, ok := cstEntitySpanForArenaNode(toks, n, cstKindForArenaTopLevel(n, toks))
	if ok {
		*out = append(*out, span)
	}
}

func cstKindForArenaTopLevel(n *AstNode, toks []token.Token) cst.GreenKind {
	if n == nil {
		return cst.GkError
	}
	switch n.kind.(type) {
	case *AstNodeKind_AstNFnDecl:
		return cst.GkFnDecl
	case *AstNodeKind_AstNStructDecl:
		return cst.GkStructDecl
	case *AstNodeKind_AstNEnumDecl:
		return cst.GkEnumDecl
	case *AstNodeKind_AstNInterfaceDecl:
		return cst.GkInterfaceDecl
	case *AstNodeKind_AstNTypeAlias:
		return cst.GkTypeAlias
	case *AstNodeKind_AstNUseDecl:
		return cst.GkUseDecl
	case *AstNodeKind_AstNLet, *AstNodeKind_AstNLetDecl:
		if cstArenaLetIsPub(n, toks) {
			return cst.GkLetDecl
		}
		return cst.GkLetStmt
	case *AstNodeKind_AstNReturn:
		return cst.GkReturnStmt
	case *AstNodeKind_AstNBreak:
		return cst.GkBreakStmt
	case *AstNodeKind_AstNContinue:
		return cst.GkContinueStmt
	case *AstNodeKind_AstNDefer:
		return cst.GkDeferStmt
	case *AstNodeKind_AstNFor:
		return cst.GkForStmt
	case *AstNodeKind_AstNAssign:
		return cst.GkAssignStmt
	case *AstNodeKind_AstNChanSend:
		return cst.GkChanSendStmt
	case *AstNodeKind_AstNExprStmt:
		return cst.GkExprStmt
	case *AstNodeKind_AstNBlock:
		return cst.GkBlock
	}
	return cst.GkError
}

func cstArenaLetIsPub(n *AstNode, toks []token.Token) bool {
	if n == nil || n.start <= 0 || n.start-1 >= len(toks) {
		return false
	}
	return toks[n.start-1].Kind == token.PUB
}

func cstEntitySpanForArenaNode(toks []token.Token, n *AstNode, kind cst.GreenKind) (cst.EntitySpan, bool) {
	if n == nil || len(toks) == 0 {
		return cst.EntitySpan{}, false
	}
	startIdx := n.start
	if startIdx < 0 {
		startIdx = 0
	}
	if startIdx >= len(toks) {
		return cst.EntitySpan{}, false
	}
	endIdx := n.end - 1
	if endIdx < startIdx {
		endIdx = startIdx
	}
	if endIdx >= len(toks) {
		endIdx = len(toks) - 1
	}
	for endIdx >= startIdx && toks[endIdx].Kind == token.EOF {
		endIdx--
	}
	if endIdx < startIdx {
		return cst.EntitySpan{}, false
	}
	return cst.EntitySpan{
		Start: toks[startIdx].Pos.Offset,
		End:   toks[endIdx].End.Offset,
		Kind:  kind,
	}, true
}

func cstArenaNodeAt(arena *AstArena, idx int) *AstNode {
	if arena == nil || idx < 0 || idx >= len(arena.nodes) {
		return nil
	}
	return arena.nodes[idx]
}
