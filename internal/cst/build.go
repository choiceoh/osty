package cst

import (
	"sort"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/token"
)

// BuildFromParsed lifts an already-parsed *ast.File plus its token stream
// into a Green tree wrapped in a Red Tree. The resulting tree carries:
//
//  1. Structural Green nodes for the file and every top-level entity
//     (GkFile, GkFnDecl, GkStructDecl, …). Nested declarations inside
//     struct/enum bodies are NOT yet broken out — that is a follow-up phase.
//  2. Flat token runs inside each top-level entity: expressions and
//     statements are not yet structured. The public Red API does not depend
//     on that structuring, so consumers can migrate when the native Green
//     parser (blocked on the self-host generator) lands.
//  3. Trivia attached as leading runs on tokens. Conservative policy: all
//     trivia preceding a token is leading for that token. Tail trivia sits
//     under the root via a zero-width GkErrorMissing sentinel.
//
// Byte coverage: every source byte is reachable from the tree via either a
// token's text or a trivia record. TestBuildRoundTrip enforces this.
//
// This builder is the adapter path while toolchain/lossless_lex.osty and a
// native Green parser remain blocked on the self-host generator. A native
// parser can replace this call without changing Red consumers.
func BuildFromParsed(src []byte, file *ast.File, toks []token.Token, trivias []Trivia) *Tree {
	b := NewBuilder(nil)
	arena := b.Arena()

	triviaIDs := make([]int, len(trivias))
	for i, tr := range trivias {
		triviaIDs[i] = arena.AddTrivia(tr)
	}
	leading, tailTrivia := pairTriviaToTokens(toks, trivias)

	entities := collectEntities(file)

	b.StartNode(GkFile)
	tokIdx := 0
	for _, ent := range entities {
		startOff := ent.node.Pos().Offset
		endOff := ent.node.End().Offset

		// Orphan tokens before the entity attach to the file root.
		for tokIdx < len(toks) && !isEOF(toks[tokIdx]) && toks[tokIdx].Pos.Offset < startOff {
			emitToken(b, toks[tokIdx], src, leading[tokIdx], triviaIDs)
			tokIdx++
		}
		b.StartNode(ent.kind)
		for tokIdx < len(toks) && !isEOF(toks[tokIdx]) && toks[tokIdx].Pos.Offset < endOff {
			emitToken(b, toks[tokIdx], src, leading[tokIdx], triviaIDs)
			tokIdx++
		}
		b.FinishNode()
	}
	for tokIdx < len(toks) {
		tk := toks[tokIdx]
		if isEOF(tk) {
			break
		}
		emitToken(b, tk, src, leading[tokIdx], triviaIDs)
		tokIdx++
	}

	if len(tailTrivia) > 0 {
		// File-tail trivia (e.g. final newline with no successor token).
		// Wrap it in a zero-width leaf under the file root so traversal
		// reaches it. A dedicated GkEndOfFile kind would read better and
		// is a future refinement.
		b.Token(GkErrorMissing, 0, "", 0, translateTriviaIDs(tailTrivia, triviaIDs), nil)
	}

	b.FinishNode() // GkFile
	_, root := b.Finish()
	return NewTreeFromSource(arena, root, src)
}

// entity pairs an AST node with the Green kind it should lift into.
type entity struct {
	node ast.Node
	kind GreenKind
}

func collectEntities(file *ast.File) []entity {
	list := make([]entity, 0, len(file.Uses)+len(file.Decls)+len(file.Stmts))
	for _, u := range file.Uses {
		list = append(list, entity{u, GkUseDecl})
	}
	for _, d := range file.Decls {
		list = append(list, entity{d, declKind(d)})
	}
	for _, s := range file.Stmts {
		list = append(list, entity{s, stmtKind(s)})
	}
	sort.SliceStable(list, func(i, j int) bool {
		return list[i].node.Pos().Offset < list[j].node.Pos().Offset
	})
	return list
}

// declKind maps a top-level Decl to its Green kind. Unknown shapes fall back
// to GkError so the structural level stays honest — an unmapped decl is a
// signal to extend the table, not a silent ok.
func declKind(d ast.Decl) GreenKind {
	switch d.(type) {
	case *ast.FnDecl:
		return GkFnDecl
	case *ast.StructDecl:
		return GkStructDecl
	case *ast.EnumDecl:
		return GkEnumDecl
	case *ast.InterfaceDecl:
		return GkInterfaceDecl
	case *ast.TypeAliasDecl:
		return GkTypeAlias
	case *ast.LetDecl:
		return GkLetDecl
	case *ast.UseDecl:
		return GkUseDecl
	}
	// Script-file FreeStmt etc. are Decl AND Stmt; route to the Stmt kind.
	if st, ok := d.(ast.Stmt); ok {
		return stmtKind(st)
	}
	return GkError
}

func stmtKind(s ast.Stmt) GreenKind {
	switch s.(type) {
	case *ast.LetStmt:
		return GkLetStmt
	case *ast.ReturnStmt:
		return GkReturnStmt
	case *ast.BreakStmt:
		return GkBreakStmt
	case *ast.ContinueStmt:
		return GkContinueStmt
	case *ast.DeferStmt:
		return GkDeferStmt
	case *ast.ForStmt:
		return GkForStmt
	case *ast.AssignStmt:
		return GkAssignStmt
	case *ast.ChanSendStmt:
		return GkChanSendStmt
	case *ast.ExprStmt:
		return GkExprStmt
	case *ast.Block:
		return GkBlock
	}
	return GkError
}

func isEOF(tk token.Token) bool { return tk.Kind == token.EOF }

// pairTriviaToTokens assigns each trivia as leading for exactly one token.
// Policy: every trivia whose span ends at or before a token's start becomes
// that token's leading trivia. Trivia remaining after the last token is
// returned separately as tailTrivia for root-level emission.
//
// This is the conservative Phase-4 rule. A future "same-line trailing"
// refinement will split runs on the token side of an intervening newline.
func pairTriviaToTokens(toks []token.Token, trivias []Trivia) (leading [][]int, tailTrivia []int) {
	leading = make([][]int, len(toks))
	triIdx := 0
	for ti, tk := range toks {
		if isEOF(tk) {
			break
		}
		start := tk.Pos.Offset
		for triIdx < len(trivias) && trivias[triIdx].Offset+trivias[triIdx].Length <= start {
			leading[ti] = append(leading[ti], triIdx)
			triIdx++
		}
	}
	for ; triIdx < len(trivias); triIdx++ {
		tailTrivia = append(tailTrivia, triIdx)
	}
	return leading, tailTrivia
}

func emitToken(b *GreenBuilder, tk token.Token, src []byte, leadingIdx []int, triviaIDs []int) {
	width := tk.End.Offset - tk.Pos.Offset
	if width < 0 {
		width = 0
	}
	text := ""
	if tk.Pos.Offset >= 0 && tk.End.Offset <= len(src) && tk.Pos.Offset <= tk.End.Offset {
		text = string(src[tk.Pos.Offset:tk.End.Offset])
	}
	b.Token(GkToken, int(tk.Kind), text, width, translateTriviaIDs(leadingIdx, triviaIDs), nil)
}

func translateTriviaIDs(indices []int, triviaIDs []int) []int {
	if len(indices) == 0 {
		return nil
	}
	out := make([]int, len(indices))
	for i, idx := range indices {
		out[i] = triviaIDs[idx]
	}
	return out
}
