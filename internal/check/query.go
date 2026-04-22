package check

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/token"
	"github.com/osty/osty/internal/types"
)

// Position queries are how an IDE (LSP, hover server, language-aware
// refactoring tool) reads the checker's output. They map a
// {line, column} into the innermost typed construct at that point.

// TypeAt returns the narrowest type recorded at the given source
// position. Walks the expression-type map in descending span size so
// inner expressions always win over their enclosing ones. Returns
// nil when no expression at the position was typed (the position
// lies in whitespace, comment, or an untyped fragment).
func (r *Result) TypeAt(pos token.Pos) types.Type {
	if r == nil {
		return nil
	}
	var best ast.Expr
	var bestSpan int
	for expr, t := range r.Types {
		if t == nil {
			continue
		}
		if !spanContains(expr.Pos(), expr.End(), pos) {
			continue
		}
		size := spanSize(expr.Pos(), expr.End())
		if best == nil || size < bestSpan {
			best = expr
			bestSpan = size
		}
	}
	if best == nil {
		return nil
	}
	return r.Types[best]
}

// SymbolAt returns the resolver Symbol referenced at the given
// position, when the position falls on an Ident that the resolver
// linked to a declaration. Powers goto-definition.
func (r *Result) SymbolAt(pos token.Pos, rr *resolve.Result) *resolve.Symbol {
	if rr == nil {
		return nil
	}
	for _, id := range rr.RefIdents {
		if spanContains(id.PosV, id.EndV, pos) {
			return rr.RefsByID[id.ID]
		}
	}
	for _, nt := range rr.TypeRefIdents {
		if spanContains(nt.PosV, nt.EndV, pos) {
			return rr.TypeRefsByID[nt.ID]
		}
	}
	return nil
}

// LetTypeAt returns the declared binding type for a let / fn-param
// identifier at the position, walking the LetTypes map. Powers
// hover-on-binding.
func (r *Result) LetTypeAt(pos token.Pos) types.Type {
	if r == nil {
		return nil
	}
	for node, t := range r.LetTypes {
		if spanContains(node.Pos(), node.End(), pos) {
			return t
		}
	}
	return nil
}

// HoverInfo bundles the information a hover request usually surfaces:
// the concrete type at a point, the bound symbol (for goto-def /
// rename), and the symbol's declared type (e.g. a fn's full FnType
// for doc popups). All fields are optional — a hover on whitespace
// returns the zero value.
type HoverInfo struct {
	Type       types.Type      // expression type at pos (nil when none)
	Symbol     *resolve.Symbol // resolved symbol at pos (nil when not an ident)
	SymbolType types.Type      // declared type of Symbol (nil when unavailable)
}

// Hover collects the hover-relevant information for a source position
// by querying TypeAt, SymbolAt, and LookupSymType in sequence.
func (r *Result) Hover(pos token.Pos, rr *resolve.Result) HoverInfo {
	info := HoverInfo{Type: r.TypeAt(pos)}
	if info.Type == nil {
		info.Type = r.LetTypeAt(pos)
	}
	if sym := r.SymbolAt(pos, rr); sym != nil {
		info.Symbol = sym
		info.SymbolType = r.LookupSymType(sym)
	}
	return info
}

// spanContains reports whether pos lies within [start, end).
// Positions are ordered line-then-column; the check treats a zero
// position as outside any real span.
func spanContains(start, end, pos token.Pos) bool {
	if pos.Line == 0 {
		return false
	}
	if pos.Line < start.Line || (pos.Line == start.Line && pos.Column < start.Column) {
		return false
	}
	if pos.Line > end.Line || (pos.Line == end.Line && pos.Column > end.Column) {
		return false
	}
	return true
}

// spanSize is a rough ordering key for picking the narrowest span
// that contains a position. One line-change contributes a large
// multiplier so cross-line spans always lose to within-line ones.
func spanSize(start, end token.Pos) int {
	lineDelta := end.Line - start.Line
	colDelta := end.Column - start.Column
	return lineDelta*10000 + colDelta
}
