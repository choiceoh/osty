//go:build selfhostgen

package check

import (
	"math/big"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/types"
)

// intPrimBounds returns (min, max, ok) for a sized integer primitive.
// Float primitives return ok=false; the caller skips the bounds check
// because any representable integer literal fits a float (with possible
// precision loss, which §2.2 tolerates for literal inference).
func intPrimBounds(k types.PrimitiveKind) (min, max *big.Int, ok bool) {
	switch k {
	case types.PInt, types.PInt64:
		return big.NewInt(-1 << 63), new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 63), big.NewInt(1)), true
	case types.PInt32:
		return big.NewInt(-1 << 31), big.NewInt(1<<31 - 1), true
	case types.PInt16:
		return big.NewInt(-1 << 15), big.NewInt(1<<15 - 1), true
	case types.PInt8:
		return big.NewInt(-1 << 7), big.NewInt(1<<7 - 1), true
	case types.PUInt64:
		return big.NewInt(0), new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 64), big.NewInt(1)), true
	case types.PUInt32:
		return big.NewInt(0), big.NewInt(1<<32 - 1), true
	case types.PUInt16:
		return big.NewInt(0), big.NewInt(1<<16 - 1), true
	case types.PUInt8, types.PByte:
		return big.NewInt(0), big.NewInt(255), true
	}
	return nil, nil, false
}

// checkIntLitFits verifies that an integer literal's numeric value
// inhabits the target sized primitive (§2.2: "A literal must fit in its
// inferred type"). Emits E0721 at the literal's position when the value
// exceeds the range; floats are deliberately permissive.
func (c *checker) checkIntLitFits(lit *ast.IntLit, target *types.Primitive) {
	if lit == nil || target == nil {
		return
	}
	min, max, ok := intPrimBounds(target.Kind)
	if !ok {
		return
	}
	v, ok := parseIntLitBig(lit.Text)
	if !ok {
		return // malformed literal — parser already reported
	}
	if v.Cmp(min) < 0 || v.Cmp(max) > 0 {
		c.errNode(lit, diag.CodeNumericLitRange,
			"literal `%s` does not fit in `%s` (range [%s, %s])",
			lit.Text, target, min, max)
	}
}

// checkNegatedIntLitFits handles `-lit` against a sized primitive — the
// literal itself passes the positive-range test but its negation may
// overflow (typically on unsigned types or at the signed minimum's
// boundary).
func (c *checker) checkNegatedIntLitFits(lit *ast.IntLit, target *types.Primitive, pos ast.Node) {
	if lit == nil || target == nil {
		return
	}
	min, max, ok := intPrimBounds(target.Kind)
	if !ok {
		return
	}
	v, ok := parseIntLitBig(lit.Text)
	if !ok {
		return
	}
	v.Neg(v)
	if v.Cmp(min) < 0 || v.Cmp(max) > 0 {
		c.errNode(pos, diag.CodeNumericLitRange,
			"literal `-%s` does not fit in `%s` (range [%s, %s])",
			lit.Text, target, min, max)
	}
}

// parseIntLitBig parses an Osty integer literal (decimal, hex, octal,
// or binary with the usual 0x / 0o / 0b prefixes and digit separator
// `_`) into a big.Int. The parser already validated syntax; this helper
// just extracts the numeric value.
func parseIntLitBig(text string) (*big.Int, bool) {
	s := strings.ReplaceAll(text, "_", "")
	if s == "" {
		return nil, false
	}
	neg := false
	if s[0] == '-' {
		neg = true
		s = s[1:]
	}
	base := 10
	switch {
	case strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X"):
		base = 16
		s = s[2:]
	case strings.HasPrefix(s, "0o") || strings.HasPrefix(s, "0O"):
		base = 8
		s = s[2:]
	case strings.HasPrefix(s, "0b") || strings.HasPrefix(s, "0B"):
		base = 2
		s = s[2:]
	}
	v, ok := new(big.Int).SetString(s, base)
	if !ok {
		return nil, false
	}
	if neg {
		v.Neg(v)
	}
	return v, true
}
