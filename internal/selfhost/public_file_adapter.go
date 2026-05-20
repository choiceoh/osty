package selfhost

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/token"
)

// LowerPublicFileFromRun lowers a FrontendRun's semantic arena to the public
// *ast.File surface. Use this explicit compatibility API only where host-side
// AST inspection is still required while the caller remains on the native
// parser path.
func LowerPublicFileFromRun(run *FrontendRun) *ast.File {
	if run == nil || run.parser == nil {
		return nil
	}
	arena := run.parser.arena
	if semantic := run.semanticAstFile(); semantic != nil && semantic.arena != nil {
		arena = semantic.arena
	}
	return lowerPublicFileFromArena(arena, run.Tokens())
}

func lowerPublicFileFromArena(arena *AstArena, toks []token.Token) *ast.File {
	file := astLowerPublicFile(arena, toks)
	assignPublicStableIDs(file, arena, toks)
	ast.AssignIDs(file)
	return file
}
