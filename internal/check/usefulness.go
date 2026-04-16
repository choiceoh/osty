//go:build selfhostgen

package check

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/types"
)

// This file holds constructor and matrix-specialization helpers shared by
// match exhaustiveness diagnostics and witness synthesis.

// ctor describes one constructor of a type. An empty argTypes slice
// denotes a nullary ctor (true, false, None, Empty, Unit).
type ctor struct {
	kind     ctorKind
	name     string // variant / bool tag
	arity    int    // tuple/struct field count
	argTypes []types.Type
	// For struct ctors, fieldOrder provides the column ordering used
	// during specialization so field-name lookups in StructPat can
	// find their slot.
	fieldOrder []string
	// For struct ctors, mapping from field name → index in argTypes.
	fieldIdx map[string]int
	// For enum variants, sym is the variant Symbol so specialize can
	// match on name.
	variantSym *resolve.Symbol
}

// ctorKind tags constructor shapes so specialize knows how to read
// the row's column-0 pattern.
type ctorKind int

const (
	ctorBoolTrue ctorKind = iota
	ctorBoolFalse
	ctorVariantK
	ctorTupleK
	ctorStructK
	ctorUnit
)

// ctorsOfType enumerates the constructors of `t` if its space is
// finite (enumerable=true). Types whose values can't be enumerated
// (Int, String, Char, Float, Bytes, unknown shapes) return false.
func (c *checker) ctorsOfType(t types.Type) (ctors []ctor, enumerable bool) {
	switch v := t.(type) {
	case *types.Primitive:
		switch v.Kind {
		case types.PBool:
			return []ctor{
				{kind: ctorBoolTrue, name: "true"},
				{kind: ctorBoolFalse, name: "false"},
			}, true
		case types.PUnit:
			return []ctor{{kind: ctorUnit, name: "()"}}, true
		case types.PNever:
			return []ctor{}, true
		}
		return nil, false
	case *types.Optional:
		return []ctor{
			{kind: ctorVariantK, name: "Some", argTypes: []types.Type{v.Inner}},
			{kind: ctorVariantK, name: "None"},
		}, true
	case *types.Tuple:
		return []ctor{{
			kind:     ctorTupleK,
			arity:    len(v.Elems),
			argTypes: append([]types.Type{}, v.Elems...),
		}}, true
	case *types.Named:
		if v.Sym == nil {
			return nil, false
		}
		// Result is enumerable: two constructors.
		if v.Sym.Name == "Result" && len(v.Args) == 2 {
			return []ctor{
				{kind: ctorVariantK, name: "Ok", argTypes: []types.Type{v.Args[0]}},
				{kind: ctorVariantK, name: "Err", argTypes: []types.Type{v.Args[1]}},
			}, true
		}
		if desc, ok := c.result.Descs[v.Sym]; ok {
			switch desc.Kind {
			case resolve.SymEnum:
				sub := types.BindArgs(desc.Generics, v.Args)
				out := make([]ctor, 0, len(desc.VariantOrder))
				for _, vname := range desc.VariantOrder {
					vd := desc.Variants[vname]
					fields := make([]types.Type, len(vd.Fields))
					for i, f := range vd.Fields {
						fields[i] = substIfNeeded(f, sub)
					}
					out = append(out, ctor{
						kind:       ctorVariantK,
						name:       vname,
						argTypes:   fields,
						variantSym: vd.Sym,
					})
				}
				return out, true
			case resolve.SymStruct:
				sub := types.BindArgs(desc.Generics, v.Args)
				fields := make([]types.Type, len(desc.Fields))
				order := make([]string, len(desc.Fields))
				idx := make(map[string]int, len(desc.Fields))
				for i, f := range desc.Fields {
					fields[i] = substIfNeeded(f.Type, sub)
					order[i] = f.Name
					idx[f.Name] = i
				}
				return []ctor{{
					kind:       ctorStructK,
					name:       v.Sym.Name,
					arity:      len(desc.Fields),
					argTypes:   fields,
					fieldOrder: order,
					fieldIdx:   idx,
				}}, true
			}
		}
	}
	return nil, false
}

// specialize builds S(c, P): for each row, if the row's column-0
// pattern matches ctor c, produce a new row whose leading entries are
// c's sub-patterns (wildcards for wildcard/binding rows). Rows that
// match a different ctor are dropped.
func (c *checker) specialize(rows [][]ast.Pattern, ct ctor) [][]ast.Pattern {
	out := make([][]ast.Pattern, 0, len(rows))
	for _, r := range rows {
		head := r[0]
		rest := r[1:]
		if patternIsCatchAll(head) {
			// Wildcard row expands to ct's arity of wildcards.
			newRow := make([]ast.Pattern, 0, len(ct.argTypes)+len(rest))
			for range ct.argTypes {
				newRow = append(newRow, &ast.WildcardPat{})
			}
			newRow = append(newRow, rest...)
			out = append(out, newRow)
			continue
		}
		if bp, ok := head.(*ast.BindingPat); ok {
			// `name @ pat` — unwrap and recurse with the inner head.
			reroute := make([]ast.Pattern, 0, len(r))
			reroute = append(reroute, bp.Pattern)
			reroute = append(reroute, rest...)
			r = reroute
			head = r[0]
			rest = r[1:]
			if patternIsCatchAll(head) {
				newRow := make([]ast.Pattern, 0, len(ct.argTypes)+len(rest))
				for range ct.argTypes {
					newRow = append(newRow, &ast.WildcardPat{})
				}
				newRow = append(newRow, rest...)
				out = append(out, newRow)
				continue
			}
		}
		switch ct.kind {
		case ctorBoolTrue, ctorBoolFalse:
			if lit, ok := head.(*ast.LiteralPat); ok {
				if b, ok := lit.Literal.(*ast.BoolLit); ok {
					if (ct.kind == ctorBoolTrue && b.Value) || (ct.kind == ctorBoolFalse && !b.Value) {
						out = append(out, rest)
					}
				}
			}
		case ctorVariantK:
			if headMatchesVariant(head, ct) {
				// Emit payload sub-patterns into the column list.
				payload := payloadPatterns(head, len(ct.argTypes))
				newRow := append([]ast.Pattern{}, payload...)
				newRow = append(newRow, rest...)
				out = append(out, newRow)
			}
		case ctorTupleK:
			if tp, ok := head.(*ast.TuplePat); ok && len(tp.Elems) == ct.arity {
				newRow := append([]ast.Pattern{}, tp.Elems...)
				newRow = append(newRow, rest...)
				out = append(out, newRow)
			}
		case ctorStructK:
			if sp, ok := head.(*ast.StructPat); ok {
				newRow := make([]ast.Pattern, 0, ct.arity+len(rest))
				for _, fname := range ct.fieldOrder {
					newRow = append(newRow, structFieldSubPattern(sp, fname))
				}
				newRow = append(newRow, rest...)
				out = append(out, newRow)
			}
		case ctorUnit:
			// Unit has exactly one value; any catch-all already handled.
			out = append(out, rest)
		}
	}
	return out
}

// defaultRows builds D(P): rows whose column-0 matches anything (a
// wildcard or plain binding) contribute their tail; others are
// dropped.
func (c *checker) defaultRows(rows [][]ast.Pattern) [][]ast.Pattern {
	out := make([][]ast.Pattern, 0, len(rows))
	for _, r := range rows {
		if patternIsCatchAll(r[0]) {
			out = append(out, r[1:])
		}
	}
	return out
}

// headMatchesVariant reports whether `head` is a pattern that matches
// the named variant. Handles `Some(x)`, bare `None`, qualified paths.
func headMatchesVariant(head ast.Pattern, ct ctor) bool {
	switch p := head.(type) {
	case *ast.IdentPat:
		return p.Name == ct.name && len(ct.argTypes) == 0
	case *ast.VariantPat:
		if len(p.Path) == 0 {
			return false
		}
		return p.Path[len(p.Path)-1] == ct.name
	case *ast.BindingPat:
		return headMatchesVariant(p.Pattern, ct)
	}
	return false
}

// payloadPatterns extracts the sub-patterns of a variant head,
// padding with wildcards when a bare IdentPat is used in place of a
// unit variant.
func payloadPatterns(head ast.Pattern, arity int) []ast.Pattern {
	switch p := head.(type) {
	case *ast.VariantPat:
		if len(p.Args) == arity {
			return append([]ast.Pattern{}, p.Args...)
		}
	case *ast.BindingPat:
		return payloadPatterns(p.Pattern, arity)
	}
	// Bare IdentPat (nullary variant) or arity mismatch: fall back to
	// wildcards of the requested width.
	out := make([]ast.Pattern, arity)
	for i := range out {
		out[i] = &ast.WildcardPat{}
	}
	return out
}

// structFieldSubPattern returns the sub-pattern a struct pattern
// assigns to field `name`, or a fresh wildcard when the field isn't
// mentioned (legal only when the pattern has a `..` rest).
func structFieldSubPattern(sp *ast.StructPat, name string) ast.Pattern {
	for _, f := range sp.Fields {
		if f.Name != name {
			continue
		}
		if f.Pattern != nil {
			return f.Pattern
		}
		// Shorthand binding: `{ name }` — treats the field as an
		// unconstrained catch-all (it binds, not narrows).
		return &ast.WildcardPat{}
	}
	// Field unmentioned: only legal with `..` — treat as wildcard.
	return &ast.WildcardPat{}
}

// splitOr flattens an or-pattern into its alternatives; non-or
// patterns return a single-element slice containing the pattern
// itself. Nested or-patterns unfold transparently.
func splitOr(p ast.Pattern) []ast.Pattern {
	if op, ok := p.(*ast.OrPat); ok {
		out := make([]ast.Pattern, 0, len(op.Alts))
		for _, alt := range op.Alts {
			out = append(out, splitOr(alt)...)
		}
		return out
	}
	return []ast.Pattern{p}
}

// substIfNeeded is types.Substitute with a nil-map short-circuit.
func substIfNeeded(t types.Type, sub map[*resolve.Symbol]types.Type) types.Type {
	if len(sub) == 0 {
		return t
	}
	return types.Substitute(t, sub)
}
