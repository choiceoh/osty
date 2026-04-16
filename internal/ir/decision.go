package ir

import (
	"fmt"
	"strings"
)

// DecisionNode is a node in the decision tree compiled from a match
// expression or statement. The tree represents the same semantics as
// the arm list but in a form closer to what a target machine has to
// emit: a cascade of discriminating tests that eventually reach a
// leaf (an arm) or a failure sink (an unmatched value).
//
// Tree shape in a nutshell:
//
//   - DecisionLeaf   — "arm N matched; run its body with these bindings"
//   - DecisionFail   — "nothing matched" (never reachable when the
//     match is exhaustive)
//   - DecisionGuard  — "check an `if` guard; on failure, fall through
//     to another subtree"
//   - DecisionSwitch — "test the scrutinee (or a sub-projection) and
//     branch to one of several children"
//
// The tree never owns the arm bodies; DecisionLeaf only carries an
// index into MatchExpr.Arms / MatchStmt.Arms. Bindings introduced by a
// pattern are materialised as DecisionBind steps above the leaf.
type DecisionNode interface {
	decisionNode()
	String() string
}

// DecisionLeaf selects arm ArmIndex (into the owning match's Arms
// slice) and hands control to its body. Bindings accumulated along the
// path are recorded as DecisionBind chains, not on the leaf.
type DecisionLeaf struct {
	ArmIndex int
}

func (*DecisionLeaf) decisionNode()   {}
func (l *DecisionLeaf) String() string { return fmt.Sprintf("leaf(arm=%d)", l.ArmIndex) }

// DecisionFail is reached only when no arm matches. For a match proven
// exhaustive by the checker this node is unreachable at runtime, but
// backends must still emit a safe trap (e.g. a runtime abort).
type DecisionFail struct{}

func (*DecisionFail) decisionNode()   {}
func (*DecisionFail) String() string { return "fail" }

// DecisionBind adds one pattern binding (`name = projection`) to the
// current environment and continues with Next. Binding the scrutinee
// as a whole is represented by Proj == nil (and Index == 0). Tuple
// fields use Kind=ProjTuple with Index; struct fields use
// Kind=ProjField with FieldName; variant payload elements use
// Kind=ProjVariant with Variant + Index.
type DecisionBind struct {
	Name string
	Proj *Projection
	T    Type
	Next DecisionNode
}

func (*DecisionBind) decisionNode() {}
func (b *DecisionBind) String() string {
	return fmt.Sprintf("bind(%s=%s) -> %s", b.Name, projectionString(b.Proj), b.Next.String())
}

// DecisionGuard evaluates Cond; when it is true the match proceeds
// into Then, otherwise into Else. Guards run after the pattern matched
// — they refine the match, they do not short-circuit earlier tests.
type DecisionGuard struct {
	Cond Expr
	Then DecisionNode
	Else DecisionNode
}

func (*DecisionGuard) decisionNode() {}
func (g *DecisionGuard) String() string {
	return fmt.Sprintf("guard -> {%s} / {%s}", g.Then.String(), g.Else.String())
}

// DecisionSwitch tests Proj against each SwitchCase in order and
// branches to the matching child. Default is taken when no case
// matches; it is always non-nil (either another DecisionNode or a
// DecisionFail sentinel).
//
// The switch kind tells backends how to compile the test:
//
//   - SwitchVariant: Proj yields an enum value; each case carries a
//     Variant name and descends into a child where the payload has
//     been bound.
//   - SwitchLit:     Proj yields a scalar / string; each case carries
//     a Lit expression compared structurally.
//   - SwitchBool:    Proj yields a Bool; exactly two cases in {true,
//     false} (either may be collapsed into Default).
//   - SwitchTuple:   Proj yields a tuple; always exactly one case that
//     destructures the tuple into N projections.
type DecisionSwitch struct {
	Kind    SwitchKind
	Proj    *Projection
	Cases   []SwitchCase
	Default DecisionNode
}

func (*DecisionSwitch) decisionNode() {}
func (s *DecisionSwitch) String() string {
	var b strings.Builder
	b.WriteString("switch(")
	b.WriteString(switchKindName(s.Kind))
	b.WriteString(", ")
	b.WriteString(projectionString(s.Proj))
	b.WriteString(") [")
	for i, c := range s.Cases {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(c.Label)
		b.WriteString(" -> ")
		b.WriteString(c.Body.String())
	}
	b.WriteString("] else ")
	b.WriteString(s.Default.String())
	return b.String()
}

// SwitchKind enumerates the flavor of discrimination a DecisionSwitch
// performs. It drives backend lowering.
type SwitchKind int

const (
	SwitchUnknown SwitchKind = iota
	SwitchVariant
	SwitchLit
	SwitchBool
	SwitchTuple
)

// SwitchCase is one child of a DecisionSwitch. Label is a human-
// readable description (variant name, literal text, "true"/"false");
// Variant + Lit carry machine-usable payloads where applicable.
type SwitchCase struct {
	Label   string
	Variant string       // SwitchVariant: the variant name
	Lit     Expr         // SwitchLit: the literal compared against
	Body    DecisionNode // subtree taken on match
}

// Projection describes how to extract a sub-value from the match's
// scrutinee at runtime. Projections chain — a nested pattern like
// `Some((x, y))` needs to first project the variant payload, then the
// tuple's .0 and .1 fields.
//
// Base is nil for projections rooted at the scrutinee itself;
// otherwise it points at the parent projection (e.g. "tuple field 0
// of the payload of the Some variant of the scrutinee").
type Projection struct {
	Base    *Projection
	Kind    ProjectionKind
	Index   int    // tuple index / variant payload index
	Field   string // struct field name
	Variant string // variant name (for ProjVariant payloads)
}

// ProjectionKind enumerates the kinds of sub-value extraction we
// currently model. Extendable as the pattern language grows.
type ProjectionKind int

const (
	ProjScrutinee ProjectionKind = iota
	ProjTuple
	ProjField
	ProjVariant  // select the payload of a known variant
	ProjVariantN // select the Nth field inside a known variant's payload
)

func switchKindName(k SwitchKind) string {
	switch k {
	case SwitchVariant:
		return "variant"
	case SwitchLit:
		return "lit"
	case SwitchBool:
		return "bool"
	case SwitchTuple:
		return "tuple"
	}
	return "?"
}

func projectionString(p *Projection) string {
	if p == nil {
		return "."
	}
	var parts []string
	for cur := p; cur != nil; cur = cur.Base {
		switch cur.Kind {
		case ProjScrutinee:
			parts = append(parts, ".")
		case ProjTuple:
			parts = append(parts, fmt.Sprintf(".%d", cur.Index))
		case ProjField:
			parts = append(parts, "."+cur.Field)
		case ProjVariant:
			parts = append(parts, "@"+cur.Variant)
		case ProjVariantN:
			parts = append(parts, fmt.Sprintf("@%s.%d", cur.Variant, cur.Index))
		}
	}
	// parts were collected from leaf to root; reverse for display
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, "")
}

// ==== Decision tree compilation ====

// CompileDecisionTree builds a decision tree for the given scrutinee
// type and arms. It returns nil when the shape is outside the
// compiler's current coverage (unusual pattern mixes, non-obvious
// range patterns, etc.) — callers should then fall back to arm-by-arm
// lowering. The resulting tree is well-formed: every interior node
// has a terminating leaf on every path, and every DecisionLeaf index
// is within [0, len(arms)).
//
// The algorithm is intentionally conservative: it compiles the common
// shapes (bare variant switches, literal switches, wild catch-all,
// single-level tuple destructure) and leaves heterogeneous or nested
// patterns to a fallback DecisionLeaf chain that re-tests each arm in
// order. That keeps backends simple while still optimising the cases
// that matter for the Osty corpus today.
func CompileDecisionTree(scrutineeT Type, arms []*MatchArm) DecisionNode {
	if len(arms) == 0 {
		return &DecisionFail{}
	}
	root := &Projection{Kind: ProjScrutinee}
	return compileArms(arms, 0, root, scrutineeT)
}

// compileArms tries to build a specialised switch for the arms
// starting at armStart; on unsupported patterns it degrades to a
// straight chain of arm tests (DecisionGuard cascading to DecisionFail).
func compileArms(arms []*MatchArm, armStart int, proj *Projection, t Type) DecisionNode {
	if armStart >= len(arms) {
		return &DecisionFail{}
	}

	// Detect a single wildcard / ident arm at this position: that
	// short-circuits the whole cascade.
	first := arms[armStart]
	if isCatchAll(first.Pattern) {
		return compileArmBody(armStart, first, proj)
	}

	// Try a specialised variant switch when every reachable arm is a
	// VariantPat (or wildcard).
	if sw := tryCompileVariantSwitch(arms, armStart, proj); sw != nil {
		return sw
	}
	// Try a literal switch (for Bool / Int / Char / String).
	if sw := tryCompileLitSwitch(arms, armStart, proj); sw != nil {
		return sw
	}

	// Fallback: straight chain of "try this arm; else continue".
	return compileArmChain(arms, armStart, proj)
}

// compileArmBody materialises a single arm into a DecisionBind chain
// terminating in a DecisionLeaf (plus an optional DecisionGuard).
func compileArmBody(armIdx int, arm *MatchArm, proj *Projection) DecisionNode {
	leaf := DecisionNode(&DecisionLeaf{ArmIndex: armIdx})
	body := bindPattern(arm.Pattern, proj, leaf)
	if arm.Guard != nil {
		return &DecisionGuard{Cond: arm.Guard, Then: body, Else: &DecisionFail{}}
	}
	return body
}

// compileArmChain emits "try arm 0; if no match, try arm 1; …" as a
// cascade of guards + leaves. It is the safe fallback for arms whose
// patterns we cannot specialise.
func compileArmChain(arms []*MatchArm, armStart int, proj *Projection) DecisionNode {
	cur := DecisionNode(&DecisionFail{})
	for i := len(arms) - 1; i >= armStart; i-- {
		arm := arms[i]
		leaf := DecisionNode(&DecisionLeaf{ArmIndex: i})
		body := bindPattern(arm.Pattern, proj, leaf)
		if arm.Guard != nil {
			body = &DecisionGuard{Cond: arm.Guard, Then: body, Else: cur}
		}
		// A catch-all collapses everything below it.
		if isCatchAll(arm.Pattern) && arm.Guard == nil {
			cur = body
			continue
		}
		cur = &DecisionGuard{Cond: nil, Then: body, Else: cur}
	}
	return cur
}

// tryCompileVariantSwitch returns a DecisionSwitch on variant tags
// when the arms form a pure variant-dispatch. A trailing
// wildcard/ident arm becomes the default.
func tryCompileVariantSwitch(arms []*MatchArm, armStart int, proj *Projection) DecisionNode {
	cases := map[string][]int{}
	order := []string{}
	var defaultArm int = -1
	for i := armStart; i < len(arms); i++ {
		arm := arms[i]
		switch p := arm.Pattern.(type) {
		case *VariantPat:
			if _, ok := cases[p.Variant]; !ok {
				order = append(order, p.Variant)
			}
			cases[p.Variant] = append(cases[p.Variant], i)
		case *WildPat, *IdentPat:
			if defaultArm == -1 {
				defaultArm = i
			}
			// arms after a catch-all are unreachable — stop scanning
			i = len(arms)
		default:
			return nil
		}
	}
	if len(order) == 0 {
		return nil
	}
	sw := &DecisionSwitch{Kind: SwitchVariant, Proj: proj}
	for _, name := range order {
		idx := cases[name][0]
		arm := arms[idx]
		pat, _ := arm.Pattern.(*VariantPat)
		payloadProj := &Projection{Base: proj, Kind: ProjVariant, Variant: pat.Variant}
		child := compileArmBody(idx, arm, payloadProj)
		sw.Cases = append(sw.Cases, SwitchCase{
			Label:   pat.Variant,
			Variant: pat.Variant,
			Body:    child,
		})
	}
	if defaultArm >= 0 {
		sw.Default = compileArmBody(defaultArm, arms[defaultArm], proj)
	} else {
		sw.Default = &DecisionFail{}
	}
	return sw
}

// tryCompileLitSwitch compiles arms like `1 => …, 2 => …, _ => …`
// into a scalar-switch tree. Supports Int, Bool, Char, Byte and
// String literal patterns.
func tryCompileLitSwitch(arms []*MatchArm, armStart int, proj *Projection) DecisionNode {
	cases := []SwitchCase{}
	var defaultArm int = -1
	kind := SwitchUnknown
	for i := armStart; i < len(arms); i++ {
		arm := arms[i]
		switch p := arm.Pattern.(type) {
		case *LitPat:
			k := switchKindOfLit(p.Value)
			if k == SwitchUnknown {
				return nil
			}
			if kind == SwitchUnknown {
				kind = k
			} else if kind != k {
				return nil
			}
			body := compileArmBody(i, arm, proj)
			cases = append(cases, SwitchCase{
				Label: litLabel(p.Value),
				Lit:   p.Value,
				Body:  body,
			})
		case *WildPat, *IdentPat:
			if defaultArm == -1 {
				defaultArm = i
			}
			i = len(arms)
		default:
			return nil
		}
	}
	if kind == SwitchUnknown || len(cases) == 0 {
		return nil
	}
	sw := &DecisionSwitch{Kind: kind, Proj: proj, Cases: cases}
	if defaultArm >= 0 {
		sw.Default = compileArmBody(defaultArm, arms[defaultArm], proj)
	} else {
		sw.Default = &DecisionFail{}
	}
	return sw
}

func switchKindOfLit(e Expr) SwitchKind {
	switch e.(type) {
	case *BoolLit:
		return SwitchBool
	case *IntLit, *CharLit, *ByteLit, *FloatLit:
		return SwitchLit
	case *StringLit:
		return SwitchLit
	}
	return SwitchUnknown
}

func litLabel(e Expr) string {
	switch v := e.(type) {
	case *BoolLit:
		if v.Value {
			return "true"
		}
		return "false"
	case *IntLit:
		return v.Text
	case *FloatLit:
		return v.Text
	case *CharLit:
		return fmt.Sprintf("'%c'", v.Value)
	case *ByteLit:
		return fmt.Sprintf("b%d", v.Value)
	case *StringLit:
		var b strings.Builder
		b.WriteByte('"')
		for _, p := range v.Parts {
			if p.IsLit {
				b.WriteString(p.Lit)
			} else {
				b.WriteString("{…}")
			}
		}
		b.WriteByte('"')
		return b.String()
	}
	return "?"
}

// bindPattern materialises a DecisionBind chain for every name that
// the pattern introduces, then hands control to rest.
func bindPattern(p Pattern, proj *Projection, rest DecisionNode) DecisionNode {
	if p == nil {
		return rest
	}
	switch p := p.(type) {
	case *IdentPat:
		return &DecisionBind{Name: p.Name, Proj: proj, Next: rest}
	case *BindingPat:
		return &DecisionBind{Name: p.Name, Proj: proj, Next: bindPattern(p.Pattern, proj, rest)}
	case *TuplePat:
		for i, elem := range p.Elems {
			childProj := &Projection{Base: proj, Kind: ProjTuple, Index: i}
			rest = bindPattern(elem, childProj, rest)
		}
		return rest
	case *StructPat:
		for _, f := range p.Fields {
			childProj := &Projection{Base: proj, Kind: ProjField, Field: f.Name}
			if f.Pattern == nil {
				rest = &DecisionBind{Name: f.Name, Proj: childProj, Next: rest}
			} else {
				rest = bindPattern(f.Pattern, childProj, rest)
			}
		}
		return rest
	case *VariantPat:
		for i, arg := range p.Args {
			childProj := &Projection{Base: proj, Kind: ProjVariantN, Variant: p.Variant, Index: i}
			rest = bindPattern(arg, childProj, rest)
		}
		return rest
	}
	return rest
}

// isCatchAll reports whether p matches every value (wildcard or bare
// ident without further structure).
func isCatchAll(p Pattern) bool {
	switch p := p.(type) {
	case *WildPat:
		return true
	case *IdentPat:
		return !p.Mut
	case *BindingPat:
		return isCatchAll(p.Pattern)
	}
	return false
}
