package selfhost

// Position-based queries over the structured check result — hover,
// goto-definition, and LSP inlay-hint sources. Mirrors the Go-side
// API in internal/check/query.go (`TypeAt` / `LetTypeAt` / `SymbolAt`
// / `Hover`) but operates on the `api.CheckResult` offset-keyed
// arrays the bootstrapped Osty checker produces. The canonical
// algorithm lives in `toolchain/check.osty::selfCheckTypeNameAtOffset`
// et al.; this Go implementation is a line-for-line reimplementation
// so existing Go consumers can query without the check.Result maps.
//
// Input offsets are byte offsets (start-inclusive, end-exclusive) to
// match the `Start` / `End` slots stamped on every `CheckedNode`
// record by the adapter.

// HoverInfo bundles the three pieces an IDE hover usually surfaces:
// the narrowest typed-expression type at the cursor, the declared
// binding type if the cursor sits on a let/param ident, and the
// resolved symbol triple (name + kind + declared type) when the
// cursor falls on a declaration site. Empty strings mean "no match"
// so callers can branch with idiomatic `if info.ExprType != "" {}`
// checks.
type HoverInfo struct {
	ExprType   string
	LetType    string
	SymbolName string
	SymbolKind string
	SymbolType string
}

// TypeAtOffset returns the type-name of the narrowest typed expression
// whose source span covers `offset`. Returns "" when no typed node
// contains the offset (whitespace, comment, untyped fragment).
func TypeAtOffset(r CheckResult, offset int) string {
	bestIdx := -1
	bestSize := 0
	for i, node := range r.TypedNodes {
		if !offsetContains(node.Start, node.End, offset) {
			continue
		}
		size := node.End - node.Start
		if bestIdx < 0 || size < bestSize {
			bestIdx = i
			bestSize = size
		}
	}
	if bestIdx < 0 {
		return ""
	}
	return r.TypedNodes[bestIdx].Type.String()
}

// LetTypeAtOffset returns the declared type-name of the let/param
// binding whose ident span covers `offset`. Returns "" when the
// offset isn't on a binding site.
func LetTypeAtOffset(r CheckResult, offset int) string {
	for _, b := range r.Bindings {
		if offsetContains(b.Start, b.End, offset) {
			return b.Type.String()
		}
	}
	return ""
}

// SymbolNameAtOffset returns the name of the narrowest declared
// symbol whose span covers `offset`. Callers wanting the symbol kind
// or declared type alongside the name should use `HoverAtOffset` to
// avoid a second walk.
func SymbolNameAtOffset(r CheckResult, offset int) string {
	bestIdx := -1
	bestSize := 0
	for i, s := range r.Symbols {
		if !offsetContains(s.Start, s.End, offset) {
			continue
		}
		size := s.End - s.Start
		if bestIdx < 0 || size < bestSize {
			bestIdx = i
			bestSize = size
		}
	}
	if bestIdx < 0 {
		return ""
	}
	return r.Symbols[bestIdx].Name
}

// HoverAtOffset collects every hover-relevant datum in one pass —
// expression type, let-binding type, and symbol triple — mirroring the
// Go check.Result.Hover combinator.
func HoverAtOffset(r CheckResult, offset int) HoverInfo {
	info := HoverInfo{
		ExprType: TypeAtOffset(r, offset),
		LetType:  LetTypeAtOffset(r, offset),
	}
	bestIdx := -1
	bestSize := 0
	for i, s := range r.Symbols {
		if !offsetContains(s.Start, s.End, offset) {
			continue
		}
		size := s.End - s.Start
		if bestIdx < 0 || size < bestSize {
			bestIdx = i
			bestSize = size
		}
	}
	if bestIdx >= 0 {
		sym := r.Symbols[bestIdx]
		info.SymbolName = sym.Name
		info.SymbolKind = sym.Kind
		info.SymbolType = sym.Type.String()
	}
	return info
}

// offsetContains tests whether `offset` lies in the half-open
// `[start, end)` span. Zero-or-negative-length spans never contain
// anything, matching the Osty-side `selfCheckOffsetContains`
// semantics.
func offsetContains(start, end, offset int) bool {
	if end <= start {
		return false
	}
	return offset >= start && offset < end
}
