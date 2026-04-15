package resolve

import (
	"fmt"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/token"
)

// mergedDecl accumulates every file's copy of a partial struct or enum
// declaration and records the canonical Symbol installed in the package
// scope. When the resolver later walks the body of each copy (to resolve
// field types, method bodies, etc.) it uses this struct to detect
// cross-file violations — duplicate fields, duplicate methods, pub
// disagreement, generic-parameter mismatch.
type mergedDecl struct {
	// kind is "struct" or "enum". Interfaces/types/fns/lets can't be
	// partial per v0.2 R19, so they aren't tracked here.
	kind string
	// first is the earliest declaration seen. Its symbol is what we
	// installed in the package scope.
	first ast.Decl
	// firstSym is the Symbol already in the package scope.
	firstSym *Symbol
	// methodPositions records the source position at which each method
	// name was first seen across every partial declaration of this
	// type. R19 requires method names to be unique across partial
	// declarations; the resolver scans each new declaration against
	// this map to flag cross-file collisions.
	methodPositions map[string]token.Pos
}

// mergePartial classifies a newly-seen top-level declaration:
//
//   - If this is the first time the name appears in the package, it is
//     installed in pkgScope and tracked for possible later merges.
//   - If a prior declaration of the same name exists AND both are
//     partial-mergeable (struct or enum, same kind), the new copy is
//     checked against the first for consistency and the symbol is
//     reused (no duplicate diagnostic).
//   - Otherwise the normal duplicate-declaration diagnostic fires.
//
// The returned Symbol is the canonical one the caller should use when
// recording per-file references or when passing `selfType` to method
// walkers.
func (r *resolver) mergePartial(
	pkgScope *Scope,
	merged map[string]*mergedDecl,
	name string,
	sym *Symbol,
	decl ast.Decl,
) *Symbol {
	// Fast path: name is fresh.
	prev := pkgScope.LookupLocal(name)
	if prev == nil {
		pkgScope.DefineForce(sym)
		if k := partialKind(decl); k != "" {
			m := &mergedDecl{
				kind:            k,
				first:           decl,
				firstSym:        sym,
				methodPositions: map[string]token.Pos{},
			}
			recordPartialMethods(m, decl)
			merged[name] = m
		}
		return sym
	}

	// Name already bound — may or may not be a legal partial merge.
	kind := partialKind(decl)
	prevMerge, wasMergeable := merged[name]
	if kind == "" || !wasMergeable || prevMerge.kind != kind {
		r.emitDuplicatePackageDecl(prev, sym, decl)
		return sym
	}

	// Validate v0.2 R19 invariants: pub-ness, generic-parameter list,
	// and cross-declaration method-name uniqueness.
	r.checkPartialConsistency(prevMerge.first, decl)
	r.checkPartialMethodNames(prevMerge, decl)
	return prevMerge.firstSym
}

// recordPartialMethods seeds mergedDecl.methodPositions from a
// declaration's method list. Called on the first partial so subsequent
// partials can detect cross-file duplicates.
func recordPartialMethods(m *mergedDecl, decl ast.Decl) {
	for _, md := range methodsOf(decl) {
		if _, have := m.methodPositions[md.Name]; !have {
			m.methodPositions[md.Name] = md.PosV
		}
	}
}

// checkPartialMethodNames walks a new partial's methods against the
// methods seen so far in earlier partials of the same type, reporting
// cross-declaration duplicates (v0.2 R19).
func (r *resolver) checkPartialMethodNames(m *mergedDecl, decl ast.Decl) {
	for _, md := range methodsOf(decl) {
		if prev, dup := m.methodPositions[md.Name]; dup {
			r.emit(diag.New(diag.Error,
				fmt.Sprintf("method `%s` is declared more than once on type `%s`",
					md.Name, declName(decl))).
				Code(diag.CodeDuplicateDecl).
				Primary(diag.Span{Start: md.PosV, End: md.EndV},
					"duplicate method here").
				Secondary(diag.Span{Start: prev, End: prev},
					"previous declaration here").
				Note("v0.2 R19: methods spread across partial declarations must have unique names").
				Build())
			continue
		}
		m.methodPositions[md.Name] = md.PosV
	}
}

// methodsOf returns the method list for a partial-mergeable declaration.
func methodsOf(d ast.Decl) []*ast.FnDecl {
	switch n := d.(type) {
	case *ast.StructDecl:
		return n.Methods
	case *ast.EnumDecl:
		return n.Methods
	}
	return nil
}

// declName extracts the name of a partial-mergeable declaration (empty
// string for unsupported kinds — only struct/enum reach here).
func declName(d ast.Decl) string {
	switch n := d.(type) {
	case *ast.StructDecl:
		return n.Name
	case *ast.EnumDecl:
		return n.Name
	}
	return ""
}

// partialKind returns "struct" or "enum" for the declaration kinds that
// may legally span multiple declarations (§3.4). Everything else returns
// "" so duplicate rules apply normally.
func partialKind(d ast.Decl) string {
	switch d.(type) {
	case *ast.StructDecl:
		return "struct"
	case *ast.EnumDecl:
		return "enum"
	}
	return ""
}

// emitDuplicatePackageDecl reports a cross-file duplicate with both the
// new location and the earlier one highlighted.
func (r *resolver) emitDuplicatePackageDecl(prev *Symbol, newSym *Symbol, _ ast.Decl) {
	d := diag.New(diag.Error,
		fmt.Sprintf("`%s` is already defined as a %s in this package",
			newSym.Name, prev.Kind)).
		Code(diag.CodeDuplicateDecl).
		PrimaryPos(newSym.Pos, "duplicate declaration here")
	if prev.Pos.Line > 0 {
		d.Secondary(diag.Span{Start: prev.Pos, End: prev.Pos},
			"previous declaration here")
	}
	d.Hint("rename one of the declarations or remove the duplicate")
	r.emit(d.Build())
}

// checkPartialConsistency enforces R19: partial declarations of the same
// type must agree on visibility and on generic parameter lists, and the
// field (struct) / variant (enum) list must live in exactly one of them.
func (r *resolver) checkPartialConsistency(first, next ast.Decl) {
	switch a := first.(type) {
	case *ast.StructDecl:
		b, ok := next.(*ast.StructDecl)
		if !ok {
			return
		}
		r.checkPubConsistency(a.Name, "struct", a.Pub, a.PosV, b.Pub, b.PosV)
		r.checkGenericConsistency(a.Name, a.Generics, b.Generics, b.PosV)
		if len(a.Fields) > 0 && len(b.Fields) > 0 {
			r.emitPartialFieldDup(a.Name, "struct", "fields",
				a.Fields[0].PosV, b.Fields[0].PosV)
		}
	case *ast.EnumDecl:
		b, ok := next.(*ast.EnumDecl)
		if !ok {
			return
		}
		r.checkPubConsistency(a.Name, "enum", a.Pub, a.PosV, b.Pub, b.PosV)
		r.checkGenericConsistency(a.Name, a.Generics, b.Generics, b.PosV)
		if len(a.Variants) > 0 && len(b.Variants) > 0 {
			r.emitPartialFieldDup(a.Name, "enum", "variants",
				a.Variants[0].PosV, b.Variants[0].PosV)
		}
	}
}

// emitPartialFieldDup reports a fields-in-multiple-partials violation.
// `what` is "fields" for structs or "variants" for enums so the message
// matches the spec vocabulary.
func (r *resolver) emitPartialFieldDup(name, kind, what string, firstPos, nextPos token.Pos) {
	r.emit(diag.New(diag.Error,
		fmt.Sprintf("%s %s may declare %s in exactly one partial declaration",
			kind, "`"+name+"`", what)).
		Code(diag.CodeDuplicateDecl).
		PrimaryPos(nextPos, fmt.Sprintf("%s declared here too", what)).
		Secondary(diag.Span{Start: firstPos, End: firstPos},
			fmt.Sprintf("%s first declared here", what)).
		Note("v0.2 R19: a partial struct/enum may spread methods across files, but its fields or variants must live in exactly one declaration").
		Build())
}

// checkPubConsistency reports when partial declarations disagree on
// whether the type is `pub`.
func (r *resolver) checkPubConsistency(
	name, kind string,
	firstPub bool, firstPos token.Pos,
	nextPub bool, nextPos token.Pos,
) {
	if firstPub == nextPub {
		return
	}
	r.emit(diag.New(diag.Error,
		fmt.Sprintf("partial declarations of %s `%s` disagree on visibility",
			kind, name)).
		Code(diag.CodeDuplicateDecl).
		PrimaryPos(nextPos, fmt.Sprintf("here: %s", pubWord(nextPub))).
		Secondary(diag.Span{Start: firstPos, End: firstPos},
			fmt.Sprintf("first declaration: %s", pubWord(firstPub))).
		Note("v0.2 R19: all partial declarations of the same type must have matching `pub` modifiers").
		Build())
}

func pubWord(pub bool) string {
	if pub {
		return "`pub`"
	}
	return "package-private"
}

// checkGenericConsistency reports when partial declarations have
// differently-named or differently-sized generic-parameter lists.
// (Per R19, bounds must also match; this implementation compares names
// and arity only — bound equivalence is type-level and will be refined
// by the checker.)
func (r *resolver) checkGenericConsistency(
	name string,
	first, next []*ast.GenericParam,
	nextPos token.Pos,
) {
	mismatch := len(first) != len(next)
	if !mismatch {
		for i := range first {
			if first[i].Name != next[i].Name {
				mismatch = true
				break
			}
		}
	}
	if !mismatch {
		return
	}
	r.emit(diag.New(diag.Error,
		fmt.Sprintf("partial declarations of `%s` disagree on type parameters",
			name)).
		Code(diag.CodeDuplicateDecl).
		PrimaryPos(nextPos, "mismatched type parameters here").
		Note("v0.2 R19: all partial declarations must have the same type-parameter list").
		Build())
}
