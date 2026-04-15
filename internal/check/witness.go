package check

import (
	"fmt"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/types"
)

// A witness is a concrete un-covered pattern the checker synthesizes
// when a match isn't exhaustive. The goal is to reproduce the exact
// shape a user must write to close the gap — e.g. `Some(None)` for a
// match over `Option<Option<T>>` that missed the inner-none case.
//
// The algorithm walks the same specialize/default tree as the
// usefulness check; wherever a constructor is missing it emits a
// witness for THAT constructor, and recurses on sub-matrices to
// refine the witness's payload.

// findWitness returns (witness, true) when the matrix `rows` fails to
// cover every value of `cols`. Returns ("", false) when coverage is
// complete. Witnesses are stringified for user-facing messages; the
// spelling matches Osty pattern syntax.
func (c *checker) findWitness(rows [][]ast.Pattern, cols []types.Type) (string, bool) {
	wits, ok := c.buildWitnesses(rows, cols)
	if ok {
		return "", false
	}
	if len(wits) == 0 {
		return "…", true
	}
	return renderWitness(wits), true
}

// buildWitnesses returns a list of witness pattern strings (one per
// column) that together describe an un-covered value. The bool is
// true when coverage is complete (in which case the witness list is
// empty).
func (c *checker) buildWitnesses(rows [][]ast.Pattern, cols []types.Type) ([]string, bool) {
	if len(cols) == 0 {
		return nil, len(rows) > 0
	}
	t0 := cols[0]
	tail := cols[1:]
	ctors, enumerable := c.ctorsOfType(t0)

	if !enumerable {
		// Open space: need a wildcard row in column 0. If none, the
		// witness starts with a placeholder "_" and recurses.
		hasWild := false
		for _, r := range rows {
			if patternIsCatchAll(r[0]) {
				hasWild = true
				break
			}
		}
		if !hasWild {
			return append([]string{"_"}, repeatWildcards(len(tail))...), false
		}
		sub, ok := c.buildWitnesses(c.defaultRows(rows), tail)
		if ok {
			return nil, true
		}
		return append([]string{"_"}, sub...), false
	}

	// Enumerable: for each constructor check specialized coverage.
	for _, ct := range ctors {
		spec := c.specialize(rows, ct)
		newCols := append(append([]types.Type{}, ct.argTypes...), tail...)
		if sub, ok := c.buildWitnesses(spec, newCols); !ok {
			// Take the first N entries of `sub` as the ctor's payload
			// witnesses, then render the ctor and append the tail
			// witnesses (after the payload columns).
			n := len(ct.argTypes)
			payload := sub[:n]
			rest := sub[n:]
			head := renderCtor(ct, payload)
			return append([]string{head}, rest...), false
		}
	}
	return nil, true
}

// renderWitness joins per-column witness pattern strings.
func renderWitness(ws []string) string {
	if len(ws) == 0 {
		return "_"
	}
	if len(ws) == 1 {
		return ws[0]
	}
	return "(" + strings.Join(ws, ", ") + ")"
}

// renderCtor prints a constructor name applied to its payload
// witnesses in Osty pattern-syntax. `_` payloads collapse so
// `Some(_)` reads natural, while `Rect(_, _)` lists both slots.
func renderCtor(ct ctor, payload []string) string {
	switch ct.kind {
	case ctorBoolTrue:
		return "true"
	case ctorBoolFalse:
		return "false"
	case ctorUnit:
		return "()"
	case ctorTupleK:
		return "(" + strings.Join(payload, ", ") + ")"
	case ctorStructK:
		// Pretty struct: `Name { f1: p1, f2: p2 }`; rest collapses
		// to `{ .. }` when everything is a wildcard.
		if allWildcard(payload) {
			return ct.name + " { .. }"
		}
		parts := make([]string, 0, len(ct.fieldOrder))
		for i, f := range ct.fieldOrder {
			parts = append(parts, fmt.Sprintf("%s: %s", f, payload[i]))
		}
		return ct.name + " { " + strings.Join(parts, ", ") + " }"
	case ctorVariantK:
		if len(payload) == 0 {
			return ct.name
		}
		return ct.name + "(" + strings.Join(payload, ", ") + ")"
	}
	return "_"
}

// repeatWildcards returns n "_" strings for tail witnesses whose
// column was never visited (the first column's witness made the
// matrix un-coverable).
func repeatWildcards(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "_"
	}
	return out
}

// allWildcard reports whether every entry in `ws` is "_".
func allWildcard(ws []string) bool {
	for _, w := range ws {
		if w != "_" {
			return false
		}
	}
	return true
}

// unused-suppression helpers referenced by future witness extensions.
var _ = fmt.Sprintf
var _ resolve.SymbolKind
