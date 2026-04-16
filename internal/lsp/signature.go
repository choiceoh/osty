package lsp

import (
	"fmt"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/token"
	"github.com/osty/osty/internal/types"
)

// handleSignatureHelp answers `textDocument/signatureHelp`. The
// response carries one SignatureInformation for the enclosing call
// (Osty has no overloading today) plus an `activeParameter` index
// derived from how many commas the user has typed.
//
// If the cursor isn't inside a call or the callee has no resolvable
// function type, we return null — the client then hides the popup.
func (s *Server) handleSignatureHelp(req *rpcRequest) {
	var params SignatureHelpParams
	if err := unmarshalParams(req, &params); err != nil {
		_ = s.conn.writeError(req.ID, errInvalidParams, err.Error())
		return
	}
	doc := s.docs.get(params.TextDocument.URI)
	if doc == nil || doc.analysis == nil {
		replyJSON(s.conn, req.ID, nil)
		return
	}
	pos := doc.analysis.lines.lspToOsty(params.Position)
	call := enclosingCall(doc.analysis.file, pos)
	if call == nil {
		replyJSON(s.conn, req.ID, nil)
		return
	}
	info, ok := buildSignatureInfo(call, doc.analysis)
	if !ok {
		replyJSON(s.conn, req.ID, nil)
		return
	}
	active := activeParamFor(call, pos)
	replyJSON(s.conn, req.ID, &SignatureHelp{
		Signatures:      []SignatureInformation{info},
		ActiveSignature: 0,
		ActiveParameter: active,
	})
}

// enclosingCall walks the file tree for the narrowest CallExpr whose
// argument list contains pos. Returning the call means the cursor is
// between the `(` and the matching `)`, which is where signatureHelp
// should surface.
func enclosingCall(file *ast.File, pos token.Pos) *ast.CallExpr {
	if file == nil {
		return nil
	}
	var best *ast.CallExpr
	var walk func(n ast.Node)
	walk = func(n ast.Node) {
		if n == nil {
			return
		}
		if call, ok := n.(*ast.CallExpr); ok {
			if inCallArgs(call, pos) {
				if best == nil || spanWidth(call) < spanWidth(best) {
					best = call
				}
			}
		}
		forEachChild(n, walk)
	}
	for _, d := range file.Decls {
		walk(d)
	}
	for _, st := range file.Stmts {
		walk(st)
	}
	return best
}

// inCallArgs reports whether pos sits between the `(` and `)` of a
// call. We approximate the paren with the byte after Fn.End() so the
// check works without a dedicated token lookup.
func inCallArgs(call *ast.CallExpr, pos token.Pos) bool {
	if call == nil || call.Fn == nil {
		return false
	}
	start := call.Fn.End()
	end := call.End()
	if pos.Offset < start.Offset || pos.Offset > end.Offset {
		return false
	}
	return true
}

// buildSignatureInfo renders the callee's signature. We look up the
// callee via the checker (for SymFn this gives us a *types.FnType
// with parameter types). Parameter NAMES come from the resolver's
// AST node when available — `types.FnType` is structural and carries
// types only.
func buildSignatureInfo(call *ast.CallExpr, a *docAnalysis) (SignatureInformation, bool) {
	sym, fnDecl := calleeSymbol(call, a)
	if sym == nil {
		return SignatureInformation{}, false
	}
	t := a.check.LookupSymType(sym)
	fnType, ok := t.(*types.FnType)
	if !ok {
		return SignatureInformation{}, false
	}
	// Build param labels. Names come from the AST decl when we have
	// it; otherwise fall back to `argN` placeholders so the shape is
	// still useful.
	paramNames := parameterNames(fnDecl, len(fnType.Params))
	params := make([]LSPSignatureParam, 0, len(fnType.Params))
	for i, pt := range fnType.Params {
		params = append(params, LSPSignatureParam{
			Name:     paramNames[i],
			TypeName: pt.String(),
		})
	}
	returnType := ""
	if fnType.Return != nil && !types.IsUnit(fnType.Return) {
		returnType = fnType.Return.String()
	}
	rendered := LSPBuildSignatureText(sym.Name, params, returnType)
	paramInfo := make([]ParameterInformation, 0, len(rendered.ParameterLabels))
	for _, paramLabel := range rendered.ParameterLabels {
		paramInfo = append(paramInfo, ParameterInformation{Label: paramLabel})
	}
	info := SignatureInformation{
		Label:      rendered.Label,
		Parameters: paramInfo,
	}
	if doc := symbolDoc(sym); doc != "" {
		info.Documentation = &MarkupContent{Kind: MarkupKindMarkdown, Value: doc}
	}
	return info, true
}

// calleeSymbol resolves the `Fn` side of a call expression to a
// resolver symbol + (optionally) the FnDecl AST that declared it.
// Returns (nil, nil) when the callee isn't a direct identifier
// reference the resolver captured.
func calleeSymbol(call *ast.CallExpr, a *docAnalysis) (*resolve.Symbol, *ast.FnDecl) {
	if call == nil || a.resolve == nil {
		return nil, nil
	}
	if id, ok := call.Fn.(*ast.Ident); ok {
		sym := a.resolve.Refs[id]
		if sym == nil {
			return nil, nil
		}
		if fd, ok := sym.Decl.(*ast.FnDecl); ok {
			return sym, fd
		}
		return sym, nil
	}
	return nil, nil
}

// parameterNames returns a slice of `count` names pulled from the
// declaration when possible, filled with `argN` placeholders
// otherwise.
func parameterNames(fd *ast.FnDecl, count int) []string {
	names := make([]string, count)
	if fd != nil {
		for i, p := range fd.Params {
			if i >= count {
				break
			}
			if p.Name != "" {
				names[i] = p.Name
			}
		}
	}
	for i := range names {
		if names[i] == "" {
			names[i] = fmt.Sprintf("arg%d", i+1)
		}
	}
	return names
}

// activeParamFor computes which parameter index the cursor sits in by
// counting Args whose end precedes pos. A cursor past every arg ends
// up pointing at "the next slot" which is still the right index to
// highlight in the popup.
func activeParamFor(call *ast.CallExpr, pos token.Pos) uint32 {
	argEnds := make([]int, 0, len(call.Args))
	for _, arg := range call.Args {
		argEnds = append(argEnds, arg.End().Offset)
	}
	return LSPActiveParameter(argEnds, pos.Offset)
}

// forEachChild is a tiny AST walker that visits the children of
// interest for enclosingCall — expression and statement positions.
// Keeping it minimal (not visiting decl bodies, types, patterns)
// is fine because CallExpr only appears in expression context.
func forEachChild(n ast.Node, f func(ast.Node)) {
	switch v := n.(type) {
	case *ast.FnDecl:
		if v.Body != nil {
			f(v.Body)
		}
	case *ast.StructDecl:
		for _, m := range v.Methods {
			f(m)
		}
	case *ast.EnumDecl:
		for _, m := range v.Methods {
			f(m)
		}
	case *ast.InterfaceDecl:
		for _, m := range v.Methods {
			f(m)
		}
	case *ast.LetDecl:
		if v.Value != nil {
			f(v.Value)
		}
	case *ast.Block:
		for _, st := range v.Stmts {
			f(st)
		}
	case *ast.LetStmt:
		if v.Value != nil {
			f(v.Value)
		}
	case *ast.ExprStmt:
		f(v.X)
	case *ast.AssignStmt:
		for _, t := range v.Targets {
			f(t)
		}
		if v.Value != nil {
			f(v.Value)
		}
	case *ast.ReturnStmt:
		if v.Value != nil {
			f(v.Value)
		}
	case *ast.CallExpr:
		f(v.Fn)
		for _, a := range v.Args {
			if a.Value != nil {
				f(a.Value)
			}
		}
	case *ast.BinaryExpr:
		f(v.Left)
		f(v.Right)
	case *ast.UnaryExpr:
		f(v.X)
	case *ast.FieldExpr:
		f(v.X)
	case *ast.IndexExpr:
		f(v.X)
		f(v.Index)
	case *ast.ParenExpr:
		f(v.X)
	case *ast.IfExpr:
		f(v.Cond)
		if v.Then != nil {
			f(v.Then)
		}
		if v.Else != nil {
			f(v.Else)
		}
	case *ast.MatchExpr:
		f(v.Scrutinee)
		for _, arm := range v.Arms {
			if arm.Body != nil {
				f(arm.Body)
			}
		}
	case *ast.ForStmt:
		if v.Iter != nil {
			f(v.Iter)
		}
		if v.Body != nil {
			f(v.Body)
		}
	}
}
