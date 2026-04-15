// Package check implements type checking for Osty.
//
// Runs AFTER parser + resolver. Consumes the resolved AST and produces a
// *Result (per-expression types + per-symbol types + collected struct /
// enum / interface / alias shapes) plus diagnostics.
//
// The checker also records the static evidence downstream phases need
// for monomorphized generic calls, interface satisfaction, match
// exhaustiveness, and auto-derived builder/default members.
package check

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/types"
)

// symInfo is the per-symbol state the checker builds during pass 1 and
// consults during pass 2.
//
// For most symbol kinds only Type matters. Variants additionally record
// their owning enum and the payload type list, so pattern binding can
// apply the scrutinee's type arguments. Top-level functions record
// their own Generics for call-site monomorphization.
type symInfo struct {
	Type          types.Type
	Mut           bool             // `let mut` — reassignment is legal
	Enum          *typeDesc        // non-nil iff SymVariant
	VariantFields []types.Type     // payload types; empty for bare variants
	Generics      []*types.TypeVar // SymFn — type parameters for monomorphization
}

// typeDesc is the collected shape of a struct / enum / interface / type
// alias, built in pass 1 and referenced by Named.Sym during pass 2.
type typeDesc struct {
	Sym              *resolve.Symbol
	Kind             resolve.SymbolKind
	Generics         []*types.TypeVar
	Fields           []*fieldDesc // struct only
	Methods          map[string]*methodDesc
	Variants         map[string]*variantDesc // enum only
	VariantOrder     []string                // enum variant source order
	InterfaceMethods map[string]*methodDesc  // interface signatures
	InterfaceExtends []types.Type            // interface composition clauses
	Alias            types.Type              // type alias target
}

type fieldDesc struct {
	Name   string
	Type   types.Type
	Pub    bool
	HasDef bool
	Decl   *ast.Field
}

type methodDesc struct {
	Name          string
	Pub           bool
	Recv          *ast.Receiver
	Fn            *types.FnType
	HasBody       bool
	Params        []*ast.Param
	Decl          *ast.FnDecl
	Owner         *typeDesc        // enclosing type/interface, if any
	Generics      []*types.TypeVar // method's own type parameters (e.g. <U>)
	OwnerGenerics []*types.TypeVar // enclosing type's generics
}

// variantDesc is one enum variant. Sym is the resolver-installed Symbol
// for the variant name — stored here so bindVariantPattern can look it
// up in O(1) instead of scanning resolver maps.
type variantDesc struct {
	Name   string
	Fields []types.Type
	Sym    *resolve.Symbol
	Decl   *ast.Variant
}

// env is the per-function checking state. Pre-computed return-type
// shape bits keep the common `?` and implicit-return checks allocation-
// and type-assertion-free.
type env struct {
	retType     types.Type
	retIsResult bool // retType is Result<_, _>
	retIsOption bool // retType is `T?`
	// retResultErr captures the E of an enclosing Result<T, E> return
	// type so `?` can verify that a propagated Err satisfies it (or
	// that the enclosing Error interface accepts it).
	retResultErr types.Type
}
