package lsp

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/selfhost"
	"github.com/osty/osty/internal/token"
	"github.com/osty/osty/internal/types"
)

// handleInlayHint answers `textDocument/inlayHint`. We emit a type
// hint for every `let` without an explicit annotation whose inferred
// type the checker captured, so the editor shows
//
//	let x: Int = 5
//	      ^^^^^ ghost text
//
// We skip lets that already have a `: T` annotation (no added
// information), and those where the checker errored (type unknown).
// Clients send the visible range so we only produce hints for code
// currently on screen — iterating everything would waste work on
// folded regions.
func (s *Server) handleInlayHint(req *rpcRequest) {
	var params InlayHintParams
	if err := unmarshalParams(req, &params); err != nil {
		_ = s.conn.writeError(req.ID, errInvalidParams, err.Error())
		return
	}
	doc := s.docs.get(params.TextDocument.URI)
	if doc == nil || doc.analysis == nil || doc.analysis.file == nil {
		replyJSON(s.conn, req.ID, []InlayHint{})
		return
	}
	startPos := doc.analysis.lines.lspToOsty(params.Range.Start)
	endPos := doc.analysis.lines.lspToOsty(params.Range.End)

	var hints []InlayHint
	collect := func(n ast.Node) {
		if !nodeInRange(n, startPos, endPos) {
			return
		}
		switch v := n.(type) {
		case *ast.LetStmt:
			if v.Type != nil || v.Pattern == nil {
				return
			}
			// Only emit hints for simple ident patterns — tuple /
			// struct destructuring can produce wrong positions
			// without more work.
			ip, ok := v.Pattern.(*ast.IdentPat)
			if !ok {
				return
			}
			t := lookupNodeType(doc.analysis, n, v.Value)
			if t == nil || types.IsError(t) {
				return
			}
			hints = append(hints, InlayHint{
				Position:    doc.analysis.lines.ostyToLSP(ip.End()),
				Label:       ": " + inlayTypeString(t),
				Kind:        InlayHintKindType,
				PaddingLeft: false,
			})
		case *ast.LetDecl:
			if v.Type != nil || v.Name == "" {
				return
			}
			t := lookupNodeType(doc.analysis, n, v.Value)
			if t == nil || types.IsError(t) {
				return
			}
			nameOff := findNameOffset(doc.src, v.PosV.Offset, v.EndV.Offset, v.Name)
			if nameOff < 0 {
				return
			}
			hints = append(hints, InlayHint{
				Position: doc.analysis.lines.offsetToLSP(nameOff + len(v.Name)),
				Label:    ": " + inlayTypeString(t),
				Kind:     InlayHintKindType,
			})
		}
	}
	walkLets(doc.analysis.file, collect)
	replyJSON(s.conn, req.ID, hints)
}

// lookupNodeType returns the best-known type for a let binding.
// Prefer LetTypes (tracked per declaration) and fall back to the
// RHS expression's inferred type.
func lookupNodeType(a *docAnalysis, decl ast.Node, rhs ast.Expr) types.Type {
	if a.check == nil {
		return nil
	}
	if t, ok := a.check.LetTypes[decl]; ok {
		return t
	}
	if rhs != nil {
		return a.check.LookupType(rhs)
	}
	return nil
}

func inlayTypeString(t types.Type) string {
	if u, ok := t.(*types.Untyped); ok {
		t = u.Default()
	}
	return t.String()
}

// nodeInRange keeps the traversal bounded to the editor's viewport.
// A node qualifies if its span overlaps [start, end].
func nodeInRange(n ast.Node, start, end token.Pos) bool {
	p := n.Pos()
	e := n.End()
	return selfhost.LSPSpanOverlaps(p.Offset, e.Offset, start.Offset, end.Offset)
}

// walkLets descends into every statement-carrying construct and
// invokes `f` on each LetStmt / LetDecl it finds, at any depth.
// Declaration-level walkers are cheap because Osty files are small;
// we can replace with a cached position index if that changes.
func walkLets(file *ast.File, f func(ast.Node)) {
	if file == nil {
		return
	}
	var walk func(n ast.Node)
	walk = func(n ast.Node) {
		if n == nil {
			return
		}
		switch v := n.(type) {
		case *ast.LetStmt:
			f(v)
		case *ast.LetDecl:
			f(v)
		}
		forEachChild(n, walk)
	}
	for _, d := range file.Decls {
		walk(d)
	}
	for _, st := range file.Stmts {
		walk(st)
	}
}
