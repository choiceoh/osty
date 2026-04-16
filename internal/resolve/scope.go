// Package resolve implements name resolution for Osty source files.
//
// The resolver walks a parsed *ast.File, builds a chain of lexical scopes,
// and for every identifier reference records the symbol it refers to.
// Errors include duplicate declarations, undefined names, and misuse of
// the contextual identifiers `self` and `Self`.
//
// The resolver does NOT perform type checking, generic instantiation, or
// member access resolution (`.field`/`.method`). Those happen in later
// phases over the resolved AST.
package resolve

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/token"
)

// SymbolKind classifies a symbol so downstream phases (type checker,
// backend emitter) can branch on what was declared.
type SymbolKind int

const (
	SymUnknown   SymbolKind = iota
	SymFn                   // top-level or method `fn`
	SymStruct               // `struct Name`
	SymEnum                 // `enum Name`
	SymInterface            // `interface Name`
	SymTypeAlias            // `type Name = ...`
	SymLet                  // `let` binding (immutable or mutable)
	SymParam                // function/closure parameter
	SymVariant              // enum variant (`Some`, `None`, `Ok`, `Err`, ...)
	SymGeneric              // `T` type parameter
	SymPackage              // alias bound by a `use` declaration
	SymBuiltin              // primitive type or prelude name
)

func (k SymbolKind) String() string {
	switch k {
	case SymFn:
		return "function"
	case SymStruct:
		return "struct"
	case SymEnum:
		return "enum"
	case SymInterface:
		return "interface"
	case SymTypeAlias:
		return "type alias"
	case SymLet:
		return "binding"
	case SymParam:
		return "parameter"
	case SymVariant:
		return "variant"
	case SymGeneric:
		return "type parameter"
	case SymPackage:
		return "package"
	case SymBuiltin:
		return "builtin"
	}
	return "unknown"
}

// IsType reports whether the symbol can appear in a type position.
func (k SymbolKind) IsType() bool {
	switch k {
	case SymStruct, SymEnum, SymInterface, SymTypeAlias, SymGeneric, SymBuiltin:
		return true
	}
	return false
}

// IsTypeHead reports whether the symbol can appear as the first segment
// of a type reference. Packages are accepted here only so `pkg.Type`
// can resolve the tail in the package's exported scope.
func (k SymbolKind) IsTypeHead() bool {
	return k.IsType() || k == SymPackage
}

// IsValue reports whether the symbol can appear in an expression position
// (function call target, variable reference, variant constructor).
func (k SymbolKind) IsValue() bool {
	switch k {
	case SymFn, SymLet, SymParam, SymVariant, SymBuiltin:
		return true
	}
	return false
}

// Symbol is a single declared name.
type Symbol struct {
	Name string
	Kind SymbolKind
	// Pos is the position of the declaration. Builtin symbols return the
	// zero Pos.
	Pos token.Pos
	// Decl is the AST node that introduced the symbol. Nil for builtins.
	// Every AST sub-type that can appear here implements ast.Node, so
	// callers may safely read Pos()/End() without type-asserting first.
	Decl ast.Node
	// Pub mirrors the visibility of the declaration. Always true for
	// builtins.
	Pub bool
	// Package is non-nil for SymPackage symbols bound by a `use`
	// declaration. It points at the resolved Package whose exported
	// scope (PkgScope + Pub filter) backs `pkg.Name` lookups. When a
	// package is imported but not loadable (stdlib stub, URL import
	// deferred to the package manager), Package stays nil and the
	// member-access code falls back to a permissive diagnostic.
	Package *Package
}

// IsBuiltin returns true for prelude/primitive symbols.
func (s *Symbol) IsBuiltin() bool { return s.Kind == SymBuiltin }

// Scope is a lexical scope. The chain of parent scopes is walked on
// lookup; definitions are only inserted into the current scope.
//
// Children are retained (appended in NewScope) so tooling can walk the
// full scope tree after resolution finishes — `osty resolve --scopes`
// relies on this. The resolver never discards a scope it enters via
// withScope; the only transient scopes are the per-alternative scopes
// in bindOrPattern, which the resolver trims explicitly.
type Scope struct {
	parent   *Scope
	children []*Scope
	syms     map[string]*Symbol
	// kind is a label used in diagnostics (e.g. "function body", "if
	// then-branch"). Optional.
	kind string
}

// NewScope allocates a child scope. Pass parent=nil for the root.
// The new scope is automatically registered as a child of parent so
// scope-tree walkers see it without further bookkeeping.
func NewScope(parent *Scope, kind string) *Scope {
	s := &Scope{parent: parent, syms: map[string]*Symbol{}, kind: kind}
	if parent != nil {
		parent.children = append(parent.children, s)
	}
	return s
}

// Parent returns the enclosing scope, or nil for the root.
func (s *Scope) Parent() *Scope { return s.parent }

// Children returns the scopes nested directly inside s, in creation
// order. The returned slice must not be mutated.
func (s *Scope) Children() []*Scope { return s.children }

// Kind returns the scope's human-readable label (e.g. "package:foo",
// "fn:greet", "block", "match-arm"). Empty for scopes created without
// one.
func (s *Scope) Kind() string { return s.kind }

// NearbyNames walks the scope chain and returns every visible name
// whose Levenshtein edit distance from `target` is at most
// `maxDistance`. Results are deduplicated (first definition wins on
// shadowing), sorted by distance ascending then by name. Used by
// code-action quick-fixes that mirror the resolver's own typo hints.
func (s *Scope) NearbyNames(target string, maxDistance int) []string {
	if maxDistance <= 0 {
		return nil
	}
	type candidate struct {
		name string
		dist int
	}
	seen := map[string]bool{target: true}
	var cands []candidate
	var buf1, buf2 []int
	for cur := s; cur != nil; cur = cur.parent {
		for n := range cur.syms {
			if seen[n] {
				continue
			}
			seen[n] = true
			if diff := len(n) - len(target); diff > maxDistance || -diff > maxDistance {
				continue
			}
			d := levenshteinBounded(target, n, maxDistance+1, &buf1, &buf2)
			if d <= maxDistance {
				cands = append(cands, candidate{name: n, dist: d})
			}
		}
	}
	// Sort by distance ascending then name, so the caller's first
	// item is always the strongest suggestion.
	for i := 1; i < len(cands); i++ {
		for j := i; j > 0; j-- {
			if cands[j-1].dist < cands[j].dist ||
				(cands[j-1].dist == cands[j].dist && cands[j-1].name <= cands[j].name) {
				break
			}
			cands[j-1], cands[j] = cands[j], cands[j-1]
		}
	}
	out := make([]string, len(cands))
	for i, c := range cands {
		out[i] = c.name
	}
	return out
}

// Define inserts sym into the current scope. Returns the existing symbol
// (and false) if a symbol with the same name was already defined here, in
// which case sym is NOT inserted — the caller decides how to report the
// duplicate.
func (s *Scope) Define(sym *Symbol) (existing *Symbol, ok bool) {
	if prev, dup := s.syms[sym.Name]; dup {
		return prev, false
	}
	s.syms[sym.Name] = sym
	return nil, true
}

// DefineForce inserts sym, overwriting any existing entry. Useful for
// shadowing inside nested block scopes (where `let x = 1; let x = 2` is
// allowed because each `let` opens a fresh sub-scope).
func (s *Scope) DefineForce(sym *Symbol) {
	s.syms[sym.Name] = sym
}

// Lookup walks the scope chain searching for name. Returns nil if not
// found anywhere up to the root.
func (s *Scope) Lookup(name string) *Symbol {
	for cur := s; cur != nil; cur = cur.parent {
		if sym, ok := cur.syms[name]; ok {
			return sym
		}
	}
	return nil
}

// LookupType walks the scope chain searching for a symbol that can appear in
// type position. Value-only symbols with the same spelling, such as enum
// variants, do not shadow outer/prelude types.
func (s *Scope) LookupType(name string) *Symbol {
	for cur := s; cur != nil; cur = cur.parent {
		if sym, ok := cur.syms[name]; ok {
			if sym.Kind.IsType() || sym.Kind == SymPackage {
				return sym
			}
			if sym.Kind == SymVariant {
				continue
			}
			return sym
		}
	}
	return nil
}

// LookupTypeHead resolves a name in type position. Osty keeps type and
// value lookup separate enough that a local value or variant named
// `String` should not prevent the builtin type `String` from being used
// in a field or annotation. The returned shadow is the first non-type
// symbol seen; callers use it only when no type head exists anywhere.
func (s *Scope) LookupTypeHead(name string) (typ *Symbol, shadow *Symbol) {
	for cur := s; cur != nil; cur = cur.parent {
		if sym, ok := cur.syms[name]; ok {
			if sym.Kind.IsTypeHead() {
				return sym, shadow
			}
			if shadow == nil {
				shadow = sym
			}
		}
	}
	return nil, shadow
}

// LookupLocal returns a symbol only if it is defined in THIS scope (not
// in any parent). Used to detect shadowing.
func (s *Scope) LookupLocal(name string) *Symbol {
	return s.syms[name]
}

// Symbols returns all symbols defined directly in this scope, in
// arbitrary order. The result must not be mutated.
func (s *Scope) Symbols() map[string]*Symbol { return s.syms }
