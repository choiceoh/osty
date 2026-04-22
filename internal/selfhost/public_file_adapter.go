package selfhost

import "github.com/osty/osty/internal/ast"

// LowerPublicFileFromRun lowers a FrontendRun's parser arena to the public
// *ast.File surface without bumping AstbridgeLowerCount. Use this only for
// compatibility seams that still need host-side AST inspection while the
// caller remains on the native parser path.
func LowerPublicFileFromRun(run *FrontendRun) *ast.File {
	if run == nil || run.parser == nil {
		return nil
	}
	file := astLowerPublicFile(run.parser.arena, run.Tokens())
	ast.AssignIDs(file)
	return file
}
