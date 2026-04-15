package check

import (
	"fmt"
	"sort"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/types"
)

// checkExhaustive verifies that a match expression covers every
// constructor of its scrutinee type (§4.3) and flags arms that are
// unreachable because earlier arms already covered them.
//
// The top-level routine delegates to a specialized diagnostic path
// for well-known scrutinee shapes (enum, Option, Result, Bool) so the
// error messages can name the missing variants. For tuple and struct
// scrutinees — where the constructor space is product-structured and
// any diagnostic would have to reconstruct a missing tuple or struct
// pattern — we fall back to the general usefulness algorithm and emit
// a generic "non-exhaustive match" when it reports a gap.
//
// Emits E0731 for non-exhaustive match, E0740 for unreachable arms.
func (c *checker) checkExhaustive(m *ast.MatchExpr, scrutT types.Type) {
	if types.IsError(scrutT) {
		return
	}
	armHeads := make([]armHead, 0, len(m.Arms))
	hasCatchAll := false
	catchAllIdx := -1
	for i, arm := range m.Arms {
		guarded := arm.Guard != nil
		if !guarded && patternIsCatchAll(arm.Pattern) {
			if hasCatchAll && catchAllIdx >= 0 {
				c.errNode(arm, diag.CodeUnreachableArm,
					"this arm is unreachable: an earlier catch-all already covers every value")
			}
			hasCatchAll = true
			if catchAllIdx == -1 {
				catchAllIdx = i
			}
		} else if hasCatchAll {
			c.errNode(arm, diag.CodeUnreachableArm,
				"this arm is unreachable: an earlier catch-all already covers every value")
		}
		armHeads = append(armHeads, classifyArmPattern(arm.Pattern, guarded))
	}
	c.checkVariantReachability(m, armHeads)
	if hasCatchAll {
		return
	}

	// Enum
	if n, ok := types.AsNamed(scrutT); ok {
		if desc, ok := c.result.Descs[n.Sym]; ok && desc.Kind == resolve.SymEnum {
			c.verifyEnumCoverage(m, desc, armHeads, scrutT)
			return
		}
		// Result<T, E>: checked as a builtin shape with recursive Ok/Err
		// payload coverage when the payload's type is a closed shape.
		if n.Sym != nil && n.Sym.Name == "Result" && len(n.Args) == 2 {
			c.verifyResultCoverage(m, scrutT)
			return
		}
	}
	// Optional
	if inner, ok := types.AsOptional(scrutT); ok {
		c.verifyOptionCoverage(m, scrutT, inner)
		return
	}
	// Bool
	if p, ok := scrutT.(*types.Primitive); ok && p.Kind == types.PBool {
		c.verifyBoolCoverage(m, armHeads)
		return
	}
	// Tuple / struct / other compound shapes use the witness-producing
	// variant so diagnostics can quote a concrete missing pattern.
	if _, ok := scrutT.(*types.Tuple); ok {
		c.reportNonExhaustiveWithWitness(m, scrutT, fmt.Sprintf("tuple type `%s`", scrutT))
		return
	}
	if n, ok := types.AsNamed(scrutT); ok {
		if desc, have := c.result.Descs[n.Sym]; have && desc.Kind == resolve.SymStruct {
			c.reportNonExhaustiveWithWitness(m, scrutT, fmt.Sprintf("struct `%s`", n.Sym.Name))
			return
		}
	}
	// Any other scalar (String, Int, Char, Float, Bytes) requires a
	// catch-all — we've returned above if one exists.
	c.errNode(m, diag.CodeNonExhaustiveMatch,
		"non-exhaustive match on `%s`: add a wildcard `_` arm to cover remaining values",
		scrutT)
}

// reportNonExhaustiveWithWitness runs the usefulness algorithm and,
// when it reports a gap, synthesizes a concrete missing pattern via
// findWitness and mentions it in the diagnostic. Mirrors Rust's
// "pattern `_` not covered" style.
func (c *checker) reportNonExhaustiveWithWitness(m *ast.MatchExpr, scrutT types.Type, label string) {
	rows := matrixFromArms(m)
	if witness, missing := c.findWitness(rows, []types.Type{scrutT}); missing {
		c.errNode(m, diag.CodeNonExhaustiveMatch,
			"non-exhaustive match on %s: pattern `%s` not covered",
			label, witness)
	}
}

// matrixFromArms converts unguarded arm patterns into rows for the
// usefulness algorithm, splitting any or-patterns into separate rows.
func matrixFromArms(m *ast.MatchExpr) [][]ast.Pattern {
	rows := make([][]ast.Pattern, 0, len(m.Arms))
	for _, arm := range m.Arms {
		if arm.Guard != nil {
			continue
		}
		for _, alt := range splitOr(arm.Pattern) {
			rows = append(rows, []ast.Pattern{alt})
		}
	}
	return rows
}

// checkVariantReachability scans the arm list and flags arms whose
// variant has already been fully covered by an earlier unguarded arm
// of the same variant with a catch-all sub-pattern.
func (c *checker) checkVariantReachability(m *ast.MatchExpr, arms []armHead) {
	fullyCovered := map[string]bool{}
	for i, ah := range arms {
		if ah.guarded {
			continue
		}
		if ah.variant == "" {
			continue
		}
		if fullyCovered[ah.variant] {
			c.errNode(m.Arms[i], diag.CodeUnreachableArm,
				"this arm is unreachable: `%s` is already fully covered by an earlier arm",
				ah.variant)
			continue
		}
		if ah.catchAllPayload {
			fullyCovered[ah.variant] = true
		}
	}
}

// armHead is a pattern's top-level shape for exhaustiveness. The
// catchAllPayload flag is set when the variant's payload pattern(s)
// match anything (wildcards or plain ident bindings), which signals
// that the arm fully covers its variant.
type armHead struct {
	guarded          bool
	variant          string   // "" when no top-level variant (tuple/struct/literal)
	orAlts           []string // alternatives in an or-pattern
	catchAllPayload  bool     // variant arm whose payload is all catch-all
	pattern          ast.Pattern
}

func classifyArmPattern(p ast.Pattern, guarded bool) armHead {
	ah := armHead{guarded: guarded, pattern: p}
	switch x := p.(type) {
	case *ast.IdentPat:
		// Uppercase = bare variant like `Empty`, lowercase = binding.
		if isUpperFirst(x.Name) {
			ah.variant = x.Name
			ah.catchAllPayload = true // bare variant has no payload
		}
	case *ast.VariantPat:
		if len(x.Path) > 0 {
			ah.variant = x.Path[len(x.Path)-1]
		}
		ah.catchAllPayload = allCatchAllPatterns(x.Args)
	case *ast.LiteralPat:
		// Bool literal patterns contribute to bool coverage.
		if b, ok := x.Literal.(*ast.BoolLit); ok {
			if b.Value {
				ah.variant = "true"
			} else {
				ah.variant = "false"
			}
			ah.catchAllPayload = true
		}
	case *ast.BindingPat:
		// `name @ pattern` — the inner pattern drives coverage.
		inner := classifyArmPattern(x.Pattern, guarded)
		return armHead{
			guarded:         guarded,
			variant:         inner.variant,
			orAlts:          inner.orAlts,
			catchAllPayload: inner.catchAllPayload,
			pattern:         p,
		}
	case *ast.OrPat:
		for _, alt := range x.Alts {
			inner := classifyArmPattern(alt, guarded)
			if inner.variant != "" {
				ah.orAlts = append(ah.orAlts, inner.variant)
			}
		}
	}
	return ah
}

// allCatchAllPatterns reports whether every pattern in the slice is a
// wildcard or plain ident binding (i.e. would match any payload value).
func allCatchAllPatterns(ps []ast.Pattern) bool {
	for _, p := range ps {
		if !patternIsCatchAll(p) {
			return false
		}
	}
	return true
}

// patternIsCatchAll reports whether a pattern is `_` or a bare binding
// that matches anything. Guarded bindings are still considered
// catch-alls by this function; the guard check is applied separately.
func patternIsCatchAll(p ast.Pattern) bool {
	switch x := p.(type) {
	case *ast.WildcardPat:
		return true
	case *ast.IdentPat:
		// Lowercase identifier = unrestricted binding → catch-all.
		return !isUpperFirst(x.Name)
	case *ast.BindingPat:
		return patternIsCatchAll(x.Pattern)
	}
	return false
}

func isUpperFirst(s string) bool {
	if s == "" {
		return false
	}
	c := s[0]
	return c >= 'A' && c <= 'Z'
}

// verifyEnumCoverage checks that every variant of `desc` appears among
// the unguarded arms (possibly via or-patterns). When top-level variant
// coverage is complete, the general usefulness/witness algorithm runs
// so that gaps inside variant payloads (e.g. `Circle(true)` missing
// `Circle(false)`) are also reported.
func (c *checker) verifyEnumCoverage(m *ast.MatchExpr, desc *typeDesc, arms []armHead, scrutT types.Type) {
	covered := map[string]bool{}
	for _, ah := range arms {
		if ah.guarded {
			continue
		}
		if ah.variant != "" {
			covered[ah.variant] = true
		}
		for _, alt := range ah.orAlts {
			covered[alt] = true
		}
	}
	var missing []string
	for _, name := range desc.VariantOrder {
		if !covered[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		c.errNode(m, diag.CodeNonExhaustiveMatch,
			"non-exhaustive match on enum `%s`: missing variant(s): `%s`",
			desc.Sym.Name, strings.Join(missing, "`, `"))
		return
	}
	// Top-level variants are all present; check whether any variant
	// payload has an uncovered sub-pattern.
	c.reportNonExhaustiveWithWitness(m, scrutT, fmt.Sprintf("enum `%s`", desc.Sym.Name))
}

// verifyBoolCoverage requires both true and false to appear.
func (c *checker) verifyBoolCoverage(m *ast.MatchExpr, arms []armHead) {
	var sawTrue, sawFalse bool
	for _, ah := range arms {
		if ah.guarded {
			continue
		}
		if ah.variant == "true" {
			sawTrue = true
		}
		if ah.variant == "false" {
			sawFalse = true
		}
		for _, alt := range ah.orAlts {
			if alt == "true" {
				sawTrue = true
			}
			if alt == "false" {
				sawFalse = true
			}
		}
	}
	if sawTrue && sawFalse {
		return
	}
	missing := []string{}
	if !sawTrue {
		missing = append(missing, "true")
	}
	if !sawFalse {
		missing = append(missing, "false")
	}
	c.errNode(m, diag.CodeNonExhaustiveMatch,
		"non-exhaustive match on `Bool`: missing `%s`",
		strings.Join(missing, "`, `"))
}

// verifyOptionCoverage checks that `Some(...)` and `None` are both
// covered. When both top-level variants are present, the full
// usefulness/witness algorithm runs so gaps inside `Some(...)` (e.g.
// `Some(true)` missing `Some(false)`) are also reported.
func (c *checker) verifyOptionCoverage(m *ast.MatchExpr, scrutT, innerT types.Type) {
	_ = innerT
	noneCovered, someCovered := false, false
	for _, arm := range m.Arms {
		if arm.Guard != nil {
			continue
		}
		head := variantHeadName(arm.Pattern)
		switch head {
		case "Some":
			someCovered = true
		case "None":
			noneCovered = true
		}
	}
	var missing []string
	if !someCovered {
		missing = append(missing, "Some(_)")
	}
	if !noneCovered {
		missing = append(missing, "None")
	}
	if len(missing) > 0 {
		c.errNode(m, diag.CodeNonExhaustiveMatch,
			"non-exhaustive match on `Option`: missing `%s`",
			strings.Join(missing, "`, `"))
		return
	}
	c.reportNonExhaustiveWithWitness(m, scrutT, "`Option`")
}

// verifyResultCoverage mirrors verifyOptionCoverage for Result<T, E>.
// When both Ok and Err variants are present at top-level, the witness
// algorithm catches any sub-pattern gaps inside their payloads.
func (c *checker) verifyResultCoverage(m *ast.MatchExpr, scrutT types.Type) {
	okCovered, errCovered := false, false
	for _, arm := range m.Arms {
		if arm.Guard != nil {
			continue
		}
		switch variantHeadName(arm.Pattern) {
		case "Ok":
			okCovered = true
		case "Err":
			errCovered = true
		}
	}
	var missing []string
	if !okCovered {
		missing = append(missing, "Ok(_)")
	}
	if !errCovered {
		missing = append(missing, "Err(_)")
	}
	if len(missing) > 0 {
		c.errNode(m, diag.CodeNonExhaustiveMatch,
			"non-exhaustive match on `Result`: missing `%s`",
			strings.Join(missing, "`, `"))
		return
	}
	c.reportNonExhaustiveWithWitness(m, scrutT, "`Result`")
}

// variantHeadName returns the top-level variant name for a pattern,
// descending through BindingPat. Returns "" when the head isn't a
// variant.
func variantHeadName(p ast.Pattern) string {
	switch x := p.(type) {
	case *ast.IdentPat:
		if isUpperFirst(x.Name) {
			return x.Name
		}
	case *ast.VariantPat:
		if len(x.Path) > 0 {
			return x.Path[len(x.Path)-1]
		}
	case *ast.BindingPat:
		return variantHeadName(x.Pattern)
	}
	return ""
}

