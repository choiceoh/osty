package backend

import (
	"sync"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/stdlib"
)

// stdlibInjectableTypes lists the built-in generic types that live in
// stdlib modules and whose bodied methods should be monomorphized
// alongside user code. Each entry pairs the surface name (as written
// by user code) with the stdlib module that holds its StructDecl or
// EnumDecl.
//
// Option B's foundation: once these decls land in the user's IR
// module, `ir.Monomorphize` specializes them automatically (the
// existing generic-struct machinery at internal/ir/monomorph.go's
// emitStructSpecialization + emitMethodSpecialization already handles
// the heavy lifting — we just need to feed it the templates).
var stdlibInjectableTypes = []struct {
	Name   string // e.g. "Map", "Option"
	Module string // stdlib module that declares it
	Kind   string // "struct" | "enum"
}{
	{"Map", "collections", "struct"},
	{"List", "collections", "struct"},
	{"Set", "collections", "struct"},
	{"Option", "option", "enum"},
	{"Result", "result", "enum"},
}

// loweredStdlibTypeCache memoizes one per-registry snapshot of the
// generic stdlib type decls. Lowering collections.osty / option.osty
// / result.osty is the expensive step; once lowered, each user module
// just deep-clones the subset it references. The cache is keyed by
// stdlib.Registry pointer so a new registry (e.g. a fresh test
// fixture) gets its own entry.
var loweredStdlibTypeCache sync.Map // map[*stdlib.Registry]*loweredStdlibTypesEntry

type loweredStdlibTypesEntry struct {
	once sync.Once
	// decls maps the surface type name ("Map", "Option", …) to the
	// generic StructDecl / EnumDecl extracted from the lowered stdlib
	// module. Nil values mean the lower pass couldn't find the decl
	// (e.g. a stdlib refactor that moved it) — those are silently
	// skipped at injection time so a partial stdlib doesn't block
	// user builds.
	decls map[string]ir.Decl
}

// injectReachableStdlibTypes appends stdlib built-in type decls
// (`Map<K, V>`, `Option<T>`, …) to mod.Decls when user code
// references them. After this pass, ir.Monomorphize sees the generic
// templates alongside the user's concrete type references and emits
// specializations (e.g. `Map$String$Int` with its update / getOr
// methods pre-substituted) — retiring the need for per-helper
// hand-emit in llvmgen.
//
// Only referenced types are injected; the cache is shared so repeated
// compiles in the same process don't re-lower collections.osty.
//
// Returns the appended decl slice and any non-fatal lowering issues.
// A nil module or nil registry returns (nil, nil).
func injectReachableStdlibTypes(mod *ir.Module, reg *stdlib.Registry) ([]ir.Decl, []error) {
	if mod == nil || reg == nil {
		return nil, nil
	}
	referenced := collectReferencedStdlibTypes(mod)
	if len(referenced) == 0 {
		return nil, nil
	}
	entry := loweredStdlibTypesFor(reg)
	if entry == nil {
		return nil, nil
	}
	var out []ir.Decl
	seen := map[string]bool{}
	for _, name := range referenced {
		if seen[name] {
			continue
		}
		decl, ok := entry.decls[name]
		if !ok || decl == nil {
			continue
		}
		seen[name] = true
		out = append(out, cloneStdlibTypeDecl(decl))
	}
	return out, nil
}

// collectReferencedStdlibTypes walks mod's type surfaces (param types,
// return types, field types, enum-variant payloads, let bindings) and
// returns the set of stdlib-provided built-in type names that appear.
// Order is deterministic (first-appearance) so the injection output is
// reproducible across runs.
func collectReferencedStdlibTypes(mod *ir.Module) []string {
	if mod == nil {
		return nil
	}
	wanted := map[string]bool{}
	for _, t := range stdlibInjectableTypes {
		wanted[t.Name] = true
	}
	seen := map[string]bool{}
	var out []string
	record := func(name string) {
		if !wanted[name] || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
	}
	var walk func(t ir.Type)
	walk = func(t ir.Type) {
		switch tt := t.(type) {
		case *ir.NamedType:
			record(tt.Name)
			for _, a := range tt.Args {
				walk(a)
			}
		case *ir.OptionalType:
			// `T?` is surface form for Option<T>; monomorphization of
			// Option as an enum also triggers isSome / isNone body
			// specialization, so opt-chains in user code pull in the
			// Option decl via this branch.
			record("Option")
			walk(tt.Inner)
		case *ir.TupleType:
			for _, e := range tt.Elems {
				walk(e)
			}
		case *ir.FnType:
			for _, p := range tt.Params {
				walk(p)
			}
			walk(tt.Return)
		}
	}
	ir.Walk(ir.VisitorFunc(func(n ir.Node) bool {
		switch x := n.(type) {
		case *ir.FnDecl:
			for _, p := range x.Params {
				walk(p.Type)
			}
			walk(x.Return)
		case *ir.StructDecl:
			for _, f := range x.Fields {
				walk(f.Type)
			}
		case *ir.EnumDecl:
			for _, v := range x.Variants {
				for _, p := range v.Payload {
					walk(p)
				}
			}
		case *ir.Param:
			walk(x.Type)
		case *ir.Field:
			walk(x.Type)
		case *ir.LetStmt:
			if x.Type != nil {
				walk(x.Type)
			}
		}
		return true
	}), mod)
	return out
}

// loweredStdlibTypesFor returns the cached lowered stdlib type decls
// for reg, loading them on first access. Safe for concurrent callers
// via sync.Once.
func loweredStdlibTypesFor(reg *stdlib.Registry) *loweredStdlibTypesEntry {
	if reg == nil {
		return nil
	}
	entryAny, _ := loweredStdlibTypeCache.LoadOrStore(reg, &loweredStdlibTypesEntry{})
	entry := entryAny.(*loweredStdlibTypesEntry)
	entry.once.Do(func() {
		entry.decls = lowerStdlibTypesFromRegistry(reg)
	})
	return entry
}

// lowerStdlibTypesFromRegistry walks every stdlib module relevant to
// the built-in type injection set (collections, option, result) and
// returns a name → Decl map containing each generic Struct/Enum. The
// returned decls are fresh clones from a one-shot `ir.Lower` per
// module so the cache can safely hand them out by reference (each
// caller deep-clones again before appending to user mods).
func lowerStdlibTypesFromRegistry(reg *stdlib.Registry) map[string]ir.Decl {
	out := map[string]ir.Decl{}
	if reg == nil {
		return out
	}
	// Gather unique stdlib modules to lower.
	modules := map[string]bool{}
	for _, t := range stdlibInjectableTypes {
		modules[t.Module] = true
	}
	for mod := range modules {
		loweredDecls := lowerStdlibModule(reg, mod)
		for _, d := range loweredDecls {
			switch x := d.(type) {
			case *ir.StructDecl:
				if len(x.Generics) > 0 && isInjectableTypeName(x.Name) {
					out[x.Name] = stripMethodsForInjection(x)
				}
			case *ir.EnumDecl:
				if len(x.Generics) > 0 && isInjectableTypeName(x.Name) {
					out[x.Name] = stripMethodsForInjection(x)
				}
			}
		}
	}
	return out
}

// stripMethodsForInjection drops methods whose signatures would drive
// monomorphization into an unbounded spec chain. The culprit shape is
// any `owner<X>` reference in a param or return type where X is not
// exactly the owner's own generic parameters (as TypeVars) — once X
// differs, specializing `owner<Foo>` queues `owner<F(Foo)>`, whose own
// method queues `owner<F(F(Foo))>`, ad infinitum.
//
// Canonical triggers from stdlib collections:
//
//   - `List<T>.chunked(self) -> List<List<T>>`
//     Args [List<T>] ≠ [T] → strip.
//   - `List<T>.enumerate(self) -> List<(Int, T)>`
//     Args [(Int, T)] ≠ [T] → strip.
//   - `List<T>.windowed(...) -> List<List<T>>`, same shape as chunked.
//
// Safe shapes: methods whose every `owner<...>` occurrence has Args
// that match the owner's declared generics verbatim (TypeVars by name
// in order) — `filter(pred) -> List<T>`, `concat(other: List<T>) ->
// List<T>`, `len() -> Int`, `Map.containsKey(k: K) -> Bool`, … all
// survive, so the backend's bodied-helper specialization path keeps
// working for them.
//
// Generic methods (`List<T>.map<R>`) are preserved here unchanged;
// monomorphize's `keepNonGenericMethods` skips generic methods anyway,
// deferring their specialization to actual call sites.
//
// Making monomorphize demand-driven per method would let the skipped
// methods come back; that's a larger refactor than required to unblock
// stdlib-body injection for user code that does not touch the
// structurally-recursive helpers.
func stripMethodsForInjection(d ir.Decl) ir.Decl {
	switch x := d.(type) {
	case *ir.StructDecl:
		x.Methods = filterMethodsAvoidingOwnerRecursion(x.Name, genericParamNames(x.Generics), x.Methods)
	case *ir.EnumDecl:
		x.Methods = filterMethodsAvoidingOwnerRecursion(x.Name, genericParamNames(x.Generics), x.Methods)
	}
	return d
}

func genericParamNames(params []*ir.TypeParam) []string {
	if len(params) == 0 {
		return nil
	}
	out := make([]string, len(params))
	for i, p := range params {
		if p != nil {
			out[i] = p.Name
		}
	}
	return out
}

// filterMethodsAvoidingOwnerRecursion drops methods whose param or
// return types reintroduce owner with modified type args, and drops
// body-less intrinsic declarations (`fn len(self) -> Int` with no
// body) since their specializations would fail ir.Validate ("nil
// Body") — those intrinsics are lowered by the backend directly
// (`osty_rt_list_len` et al.), not from an injected template. The
// returned slice preserves every safe method in its original order.
func filterMethodsAvoidingOwnerRecursion(owner string, generics []string, methods []*ir.FnDecl) []*ir.FnDecl {
	if len(methods) == 0 {
		return methods
	}
	out := methods[:0:0]
	for _, m := range methods {
		if m == nil {
			continue
		}
		if m.Body == nil {
			continue
		}
		// Generic methods (`List<T>.map<R>`) are preserved as templates
		// by keepNonGenericMethods and only specialized at actual call
		// sites, so their signatures never drive eager recursion. Only
		// non-generic methods on the owner participate in the eager
		// sig-scanning path, so the recursion filter runs against them.
		if len(m.Generics) == 0 && methodRecursesOwner(owner, generics, m) {
			continue
		}
		out = append(out, m)
	}
	return out
}

// methodRecursesOwner reports whether any param or return type of m
// contains a `owner<...>` occurrence whose type args are not the
// identity form `<T, U, …>` for owner's declared generics.
func methodRecursesOwner(owner string, generics []string, m *ir.FnDecl) bool {
	if m == nil {
		return false
	}
	for _, p := range m.Params {
		if p != nil && typeRecursesOwner(p.Type, owner, generics) {
			return true
		}
	}
	return typeRecursesOwner(m.Return, owner, generics)
}

func typeRecursesOwner(t ir.Type, owner string, generics []string) bool {
	switch x := t.(type) {
	case *ir.NamedType:
		if x.Name == owner && !argsAreIdentityGenerics(x.Args, generics) {
			return true
		}
		for _, a := range x.Args {
			if typeRecursesOwner(a, owner, generics) {
				return true
			}
		}
		return false
	case *ir.OptionalType:
		return typeRecursesOwner(x.Inner, owner, generics)
	case *ir.TupleType:
		for _, e := range x.Elems {
			if typeRecursesOwner(e, owner, generics) {
				return true
			}
		}
		return false
	case *ir.FnType:
		for _, p := range x.Params {
			if typeRecursesOwner(p, owner, generics) {
				return true
			}
		}
		return typeRecursesOwner(x.Return, owner, generics)
	}
	return false
}

// argsAreIdentityGenerics reports whether args is exactly the identity
// form for the owner's declared generics: each Args[i] is a TypeVar
// whose Name matches generics[i]. A match means the `owner<...>`
// reference is just the method's self-type, which specializes to the
// already-queued concrete owner and terminates.
func argsAreIdentityGenerics(args []ir.Type, generics []string) bool {
	if len(args) != len(generics) {
		return false
	}
	for i, a := range args {
		tv, ok := a.(*ir.TypeVar)
		if !ok || tv.Name != generics[i] {
			return false
		}
	}
	return true
}

// lowerStdlibModule runs ir.Lower on one stdlib module's file, reusing
// the resolve/check machinery the free-fn injector already uses. A nil
// or partial module returns nil.
func lowerStdlibModule(reg *stdlib.Registry, module string) []ir.Decl {
	if reg == nil {
		return nil
	}
	mod, ok := reg.Modules[module]
	if !ok || mod == nil || mod.File == nil {
		return nil
	}
	res := stdlibResolveResult(reg, module)
	chk := stdlibCheckResult(reg, module)
	lowered, _ := ir.Lower(module, mod.File, res, chk)
	if lowered == nil {
		return nil
	}
	return lowered.Decls
}

func isInjectableTypeName(name string) bool {
	for _, t := range stdlibInjectableTypes {
		if t.Name == name {
			return true
		}
	}
	return false
}

// cloneStdlibTypeDecl deep-clones a StructDecl or EnumDecl so the
// per-user-module appended copy can be rewritten by the monomorphizer
// without disturbing the shared cache. Uses the public ir.Clone and
// type-asserts back to Decl.
func cloneStdlibTypeDecl(d ir.Decl) ir.Decl {
	if d == nil {
		return nil
	}
	cp, _ := ir.Clone(d).(ir.Decl)
	return cp
}

// moduleForStdlibType is a lookup helper used by tests and
// diagnostics: given a built-in type name, returns the stdlib module
// that declares it, or "" if unknown.
func moduleForStdlibType(name string) string {
	for _, t := range stdlibInjectableTypes {
		if t.Name == name {
			return t.Module
		}
	}
	return ""
}

// The walker relies on ir.Walk visiting concrete types via their
// enclosing nodes. Suppress the unused warning on moduleForStdlibType
// (only used in tests) with an unused reference.
var _ = moduleForStdlibType
var _ ast.Node = (*ast.File)(nil)
