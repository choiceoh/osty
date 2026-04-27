// stdlib_method_lookup.go — AST-level lookup of stdlib struct/enum
// methods (e.g. Map.containsKey, Option.isSome). First-stage groundwork
// for built-in generic method monomorphization: turning bodied stdlib
// methods into concrete LLVM functions per (K, V) instantiation
// instead of hand-emitting the equivalent shape per callsite in Go.
//
// # Why this file exists
//
// The current llvmgen path for Map's canonical helpers (`getOr`,
// `update`, `retainIf`, `mergeWith`, `mapValues`) emits the body shape
// directly in Go — see expr.go (emitMapGetOr, emitMapMapValues, …) and
// stmt.go (emitMapUpdateStmt, emitMapRetainIfStmt). Each new helper is
// another special-case; the intrinsic-only runtime stack does not
// actually compile the stdlib body. That's the "still special-case"
// gap the user flagged.
//
// The principled fix is monomorphization: Map's stdlib bodied methods
// compile once per (K, V) pair into mangled LLVM functions (e.g.
// `Map$getOr$string$i64`) the way user generic methods already do
// (internal/ir/monomorph.go — rewriteGenericMethodCall +
// emitMethodSpecialization).
//
// # Honest scope note
//
// This file ships the **first** step: robust AST-level method lookup
// against the stdlib registry. Full end-to-end retirement of the
// hand-emit paths in llvmgen requires **four additional subsystems**
// that are all separate projects:
//
//  1. **AST-path backend monomorphization.** The legacy AST backend
//     (generateASTFile) never runs the IR monomorphizer — only the MIR
//     path does, and toolchain code typically falls back to the AST
//     path. Either (a) extend the AST path with its own mini-
//     monomorphizer, or (b) reroute Map-heavy code through MIR
//     exclusively. Both are multi-hundred-line refactors.
//
//  2. **AST-level type substitution.** `ir.SubstituteTypes` operates on
//     IR nodes, not AST. For an AST-path lowering we need an
//     equivalent pass that walks `ast.FnDecl` and rewrites K/V refs in
//     every `*ast.NamedType`, `*ast.OptionalType`, `*ast.FnType`,
//     plus every expression's inferred-type annotation.
//
//  3. **Recursive body-dependency resolution.** `Map.containsKey`'s
//     body is `self.get(key).isSome()`. `isSome()` is itself a bodied
//     method on Option<T>, whose body is `match self { Some(_) -> true,
//     None -> false }`. Monomorphizing containsKey requires
//     Option<Int> as a concrete enum in the generated module, with
//     its `isSome`/`match` logic lowered. Same compounding applies to
//     every other Map helper.
//
//  4. **Intrinsic / specialized-method coexistence.** The runtime-
//     backed built-ins (`osty_rt_map_get_<K>`, `map_insert_<K>` etc.)
//     remain the only way to talk to the C runtime. A specialized
//     `Map$getOr$string$i64` body must still dispatch its internal
//     `self.get(key)` through the intrinsic, not through "another
//     specialized method," or we end up with an infinite-recursion
//     trap.
//
// The lookup helpers in this file make (3) tractable (we need body
// ASTs to do the dependency walk) but do not themselves implement it.
//
// # What's usable today
//
//   - `LookupStructMethod` / `LookupEnumMethod` — return the cached
//     stdlib AST FnDecl for a built-in method. Callers get a read-only
//     view; cloning is the caller's responsibility (use
//     `internal/ir/clone.go` once converted to IR).
//
//   - `MapCanonicalHelperNames` — the §B.9.1.64 canonical set. Useful
//     for the future dispatcher that decides whether a given callsite
//     should route to monomorphization or intrinsic.
//
// Tests in `stdlib_method_lookup_test.go` pin the registry has the
// Map canonical helpers plus Option's isSome/isNone, so a stdlib
// refactor that drops one surfaces here immediately.
package llvmgen

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/stdlib"
)

// MapCanonicalHelperNames lists the bodied Map helpers from
// CLAUDE.md §B.9.1.64 that are candidates for monomorphization —
// i.e. they have a non-empty `Body` in collections.osty and are
// currently hand-emitted in llvmgen.
//
// Order matches the spec table (update, getOr, mergeWith, groupBy,
// mapValues, retainIf). `containsKey` is included because its body
// (`self.get(key).isSome()`) is the simplest proof-of-concept
// candidate for the first end-to-end monomorphization attempt.
var MapCanonicalHelperNames = []string{
	"containsKey",
	"getOr",
	"getOrInsert",
	"getOrInsertWith",
	"update",
	"mergeWith",
	"mapValues",
	"retainIf",
	"filter",
	"merge",
	"any",
	"all",
	"count",
	"find",
	"forEach",
	"values",
	"entries",
	"insertAll",
	// Note: `groupBy` is on `List<T>`, not `Map<K, V>` — it returns
	// Map<K, List<T>>. Tracked separately in ListCanonicalHelperNames.
}

// ListCanonicalHelperNames lists bodied helpers on `List<T>` that
// parallel the Map set. Present as declarations so the future
// monomorphization dispatcher treats List<T> uniformly with Map<K,V>.
// Only methods with actual bodies in collections.osty are listed —
// `any`/`all`/`count` etc. are declared elsewhere (iter module) or
// intrinsic-only on List.
var ListCanonicalHelperNames = []string{
	"groupBy",
	"map",
	"filter",
	"fold",
	"reduce",
	"find",
	"zip",
	"enumerate",
	"chunked",
	"windowed",
	"partition",
	"flatMap",
}

// LookupStructMethod returns the bodied AST method `methodName` on the
// stdlib struct `structName` (e.g. "Map", "List", "Set"), searching
// every loaded module. Returns nil if the struct or method is missing
// or if the method has no body.
//
// The returned *ast.FnDecl is the shared immutable copy the stdlib
// registry owns. Callers must not mutate it — if you need a mutable
// version (for type substitution), deep-clone via IR lowering +
// `ir.cloneFnDecl` first, or add an AST-level clone once the
// substitution pass exists.
func LookupStructMethod(reg *stdlib.Registry, structName, methodName string) *ast.FnDecl {
	if reg == nil {
		return nil
	}
	for _, mod := range reg.Modules {
		if mod == nil || mod.File == nil {
			continue
		}
		for _, decl := range mod.File.Decls {
			sd, ok := decl.(*ast.StructDecl)
			if !ok || sd == nil || sd.Name != structName {
				continue
			}
			for _, m := range sd.Methods {
				if m == nil || m.Name != methodName {
					continue
				}
				if m.Body == nil {
					return nil // declaration-only (intrinsic surface)
				}
				return m
			}
		}
	}
	return nil
}

// LookupEnumMethod is the enum-side mirror of LookupStructMethod.
// Used for Option<T> / Result<T, E> methods whose bodies feed
// the dependency walk: `Map.containsKey` → `Option.isSome` →
// `match Some/None`.
func LookupEnumMethod(reg *stdlib.Registry, enumName, methodName string) *ast.FnDecl {
	if reg == nil {
		return nil
	}
	for _, mod := range reg.Modules {
		if mod == nil || mod.File == nil {
			continue
		}
		for _, decl := range mod.File.Decls {
			ed, ok := decl.(*ast.EnumDecl)
			if !ok || ed == nil || ed.Name != enumName {
				continue
			}
			for _, m := range ed.Methods {
				if m == nil || m.Name != methodName {
					continue
				}
				if m.Body == nil {
					return nil
				}
				return m
			}
		}
	}
	return nil
}

// structMethodGenericBody is the shape a monomorphizer entry point
// needs: the original method's AST plus the enclosing struct's
// generic parameters (so K/V can be substituted). The callsite-
// specific type arguments (e.g. K=String, V=Int) flow in separately.
type structMethodGenericBody struct {
	Method         *ast.FnDecl
	StructName     string
	StructGenerics []*ast.GenericParam
}

// LookupStructMethodWithGenerics extends LookupStructMethod by also
// returning the enclosing struct's generic parameter list. This is
// what a future `substituteStructMethodAST(body, {K→String, V→Int})`
// pass consumes: for each `*ast.NamedType` in the method body/signature
// whose name matches one of `StructGenerics`, replace it with the
// corresponding concrete type from the callsite.
func LookupStructMethodWithGenerics(reg *stdlib.Registry, structName, methodName string) *structMethodGenericBody {
	if reg == nil {
		return nil
	}
	for _, mod := range reg.Modules {
		if mod == nil || mod.File == nil {
			continue
		}
		for _, decl := range mod.File.Decls {
			sd, ok := decl.(*ast.StructDecl)
			if !ok || sd == nil || sd.Name != structName {
				continue
			}
			for _, m := range sd.Methods {
				if m == nil || m.Name != methodName || m.Body == nil {
					continue
				}
				return &structMethodGenericBody{
					Method:         m,
					StructName:     sd.Name,
					StructGenerics: sd.Generics,
				}
			}
		}
	}
	return nil
}
