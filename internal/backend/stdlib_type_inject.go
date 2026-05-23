package backend

import (
	"sort"
	"strings"
	"sync"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/stdlib"
)

type stdlibTypeKey struct {
	Module string
	Name   string
}

// loweredStdlibTypeCache memoizes one per-registry snapshot of the
// stdlib type decls. Lowering stdlib modules is the expensive step; once
// lowered, each user module
// just deep-clones the subset it references. The cache is keyed by
// stdlib.Registry pointer so a new registry (e.g. a fresh test
// fixture) gets its own entry.
var loweredStdlibTypeCache sync.Map // map[*stdlib.Registry]*loweredStdlibTypesEntry

type loweredStdlibTypesEntry struct {
	once sync.Once
	// decls maps (module, surface type name) to the StructDecl /
	// EnumDecl extracted from the lowered stdlib module. Nil values
	// mean the lower pass couldn't find the decl (e.g. a stdlib
	// refactor that moved it) — those are silently skipped at
	// injection time so a partial stdlib doesn't block user builds.
	decls map[stdlibTypeKey]ir.Decl
}

func (e *loweredStdlibTypesEntry) moduleTypeNames(module string) map[string]bool {
	if e == nil || module == "" || len(e.decls) == 0 {
		return nil
	}
	out := map[string]bool{}
	for key := range e.decls {
		if key.Module == module && key.Name != "" {
			out[key.Name] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// injectReachableStdlibTypes appends stdlib built-in type decls
// (`Map<K, V>`, `Option<T>`, …) to mod.Decls when user code
// references them. After this pass, ir.Monomorphize sees the generic
// templates alongside the user's concrete type references and emits
// specializations (e.g. `Map$String$Int` with its update / getOr
// methods pre-substituted) — retiring the need for per-helper
// hand-emit at the LLVM emission boundary.
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
	moduleRefs := collectInjectedStdlibModules(mod)
	for module := range collectImportedStdlibModules(mod) {
		if moduleRefs == nil {
			moduleRefs = map[string]bool{}
		}
		moduleRefs[module] = true
	}
	var out []ir.Decl
	seen := map[stdlibTypeKey]bool{}
	appendKey := func(key stdlibTypeKey) {
		if seen[key] {
			return
		}
		decl, ok := entry.decls[key]
		if !ok || decl == nil {
			return
		}
		seen[key] = true
		out = append(out, cloneStdlibTypeDecl(key, decl, entry.moduleTypeNames(key.Module)))
	}
	for _, key := range referenced {
		appendKey(key)
	}
	if len(moduleRefs) > 0 {
		keys := make([]stdlibTypeKey, 0, len(entry.decls))
		for key := range entry.decls {
			if moduleRefs[key.Module] {
				keys = append(keys, key)
			}
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i].Module != keys[j].Module {
				return keys[i].Module < keys[j].Module
			}
			return keys[i].Name < keys[j].Name
		})
		for _, key := range keys {
			appendKey(key)
		}
	}
	return out, nil
}

func collectInjectedStdlibModules(mod *ir.Module) map[string]bool {
	if mod == nil {
		return nil
	}
	out := map[string]bool{}
	record := func(name string) {
		module, ok := stdlibModuleFromInjectedSymbol(name)
		if ok {
			out[module] = true
		}
	}
	for _, d := range mod.Decls {
		switch x := d.(type) {
		case *ir.FnDecl:
			if x != nil {
				record(x.Name)
			}
		case *ir.StructDecl:
			for _, m := range x.Methods {
				if m != nil {
					record(m.Name)
				}
			}
		case *ir.EnumDecl:
			for _, m := range x.Methods {
				if m != nil {
					record(m.Name)
				}
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func collectImportedStdlibModules(mod *ir.Module) map[string]bool {
	if mod == nil {
		return nil
	}
	out := map[string]bool{}
	for _, d := range mod.Decls {
		u, ok := d.(*ir.UseDecl)
		if !ok || u == nil || len(u.Path) < 2 || u.Path[0] != "std" {
			continue
		}
		out[u.Path[1]] = true
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func stdlibModuleFromInjectedSymbol(name string) (string, bool) {
	const prefix = "osty_std_"
	if !strings.HasPrefix(name, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(name, prefix)
	idx := strings.Index(rest, "__")
	if idx <= 0 {
		return "", false
	}
	return rest[:idx], true
}

// collectReferencedStdlibTypes walks mod's type surfaces (param types,
// return types, field types, enum-variant payloads, let bindings) and
// expression result types and returns the set of stdlib-provided type
// decls that appear.
// Order is deterministic (first-appearance) so the injection output is
// reproducible across runs.
func collectReferencedStdlibTypes(mod *ir.Module) []stdlibTypeKey {
	if mod == nil {
		return nil
	}
	builtinModule := map[string]string{}
	for _, surface := range stdlib.BuiltinTypeSurfaces() {
		if surface.Injectable {
			builtinModule[surface.Name] = surface.Module
		}
	}
	seen := map[stdlibTypeKey]bool{}
	var out []stdlibTypeKey
	record := func(key stdlibTypeKey) {
		key.Module = normalizeStdlibTypeModule(key.Module)
		if key.Module == "" || key.Name == "" || seen[key] {
			return
		}
		seen[key] = true
		out = append(out, key)
	}
	var walk func(t ir.Type)
	walk = func(t ir.Type) {
		switch tt := t.(type) {
		case *ir.NamedType:
			if tt.Package != "" {
				record(stdlibTypeKey{Module: tt.Package, Name: tt.Name})
			} else if module := builtinModule[tt.Name]; module != "" {
				record(stdlibTypeKey{Module: module, Name: tt.Name})
			}
			for _, a := range tt.Args {
				walk(a)
			}
		case *ir.OptionalType:
			// `T?` is surface form for Option<T>; monomorphization of
			// Option as an enum also triggers isSome / isNone body
			// specialization, so opt-chains in user code pull in the
			// Option decl via this branch.
			record(stdlibTypeKey{Module: "option", Name: "Option"})
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
		if expr, ok := n.(ir.Expr); ok && expr != nil {
			walk(expr.Type())
		}
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

func normalizeStdlibTypeModule(module string) string {
	module = strings.TrimPrefix(module, "std.")
	return module
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

// lowerStdlibTypesFromRegistry walks stdlib modules and returns a
// (module, name) → Decl map containing every Struct/Enum surface. The
// returned decls are fresh clones from a one-shot `ir.Lower` per
// module so the cache can safely hand them out by reference (each
// caller deep-clones again before appending to user mods).
func lowerStdlibTypesFromRegistry(reg *stdlib.Registry) map[stdlibTypeKey]ir.Decl {
	out := map[stdlibTypeKey]ir.Decl{}
	if reg == nil {
		return out
	}
	for module := range reg.Modules {
		loweredDecls := lowerStdlibModule(reg, module)
		for _, d := range loweredDecls {
			switch x := d.(type) {
			case *ir.StructDecl:
				out[stdlibTypeKey{Module: module, Name: x.Name}] = stripStdlibTypeForInjection(x)
			case *ir.EnumDecl:
				out[stdlibTypeKey{Module: module, Name: x.Name}] = stripStdlibTypeForInjection(x)
			}
		}
	}
	return out
}

func stripStdlibTypeForInjection(d ir.Decl) ir.Decl {
	switch x := d.(type) {
	case *ir.StructDecl:
		switch x.Name {
		case "List", "Set":
			x.Methods = nil
			return x
		}
		if len(x.Generics) == 0 {
			x.Methods = nil
			return x
		}
	case *ir.EnumDecl:
		switch x.Name {
		case "Option", "Result":
			x.Methods = nil
			return x
		}
		if len(x.Generics) == 0 {
			x.Methods = nil
			return x
		}
	}
	return stripMethodsForInjection(d)
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

// filterMethodsAvoidingOwnerRecursion drops methods the backend cannot
// safely specialize from an injected template:
//
//  1. Owner-recursive sigs (`List<T>.chunked(self) -> List<List<T>>`) —
//     eagerly specialising the owner would queue `List<List<Foo>>` and
//     diverge. See methodRecursesOwner for the exact shape test.
//
//  2. **Dispatch cascade:** bodied methods that call a body-less
//     intrinsic method on `self` whose dispatch the backend has not
//     whitelisted. Their bodies would survive into the specialization
//     but the `self.<missing>(...)` site has no lowering target, so
//     the LLVM emission boundary walls on `*ast.TurbofishExpr` /
//     `self.<missing>` when it later tries to emit them. The canonical
//     List example: `List<T>.contains { self.indexOf(item).isSome() }`
//     — `indexOf` is body-less and not in `listMethodInfo`, so the
//     specialized `contains` can never resolve the call and the
//     enclosing `.isSome()` chain loses its source type. Map's shape
//     is the same but `mapMethodInfo` whitelists every body-less
//     intrinsic (`get`, `insert`, …), so its bodied helpers all
//     survive.
//
// Body-less declarations themselves are preserved on the specialized
// struct — ir.Validate is loosened to allow them for builtin-source
// specializations — so source-type propagation stays intact for any
// remaining caller. Only the bodied methods whose emission is
// guaranteed to fail get dropped.
//
// Generic methods (`List<T>.map<R>`) are preserved here unchanged;
// monomorphize's `keepNonGenericMethods` skips generic methods anyway
// and defers their specialization to actual call sites.
func filterMethodsAvoidingOwnerRecursion(owner string, generics []string, methods []*ir.FnDecl) []*ir.FnDecl {
	if len(methods) == 0 {
		return methods
	}
	// Pass 1: strip owner-recursive methods. Body-less declarations stay
	// so their signatures remain visible for source-type propagation
	// (e.g. `self.get(k) -> V?` feeding a later `.isSome()` dispatch).
	ownerUndispatchable := ownerUndispatchableMethodSet(owner)
	droppedSelfCall := map[string]bool{}
	survived := make([]*ir.FnDecl, 0, len(methods))
	for _, m := range methods {
		if m == nil {
			continue
		}
		if m.Body != nil && len(m.Generics) == 0 && methodHasUnsupportedLLVMShape(owner, m) {
			droppedSelfCall[m.Name] = true
			continue
		}
		if m.Body != nil && len(m.Generics) == 0 && methodRecursesOwner(owner, generics, m) {
			droppedSelfCall[m.Name] = true
			continue
		}
		survived = append(survived, m)
	}
	// Pass 2: cascade-drop bodied methods whose body calls any
	// owner-undispatchable (or already-dropped) method on `self`.
	// Repeat until the set is stable — a bodied helper may only touch
	// an undispatchable intrinsic via another bodied helper that we
	// strip in this pass.
	for {
		before := len(droppedSelfCall)
		next := survived[:0:0]
		for _, m := range survived {
			if m == nil {
				continue
			}
			if m.Body != nil && methodBodyCallsUndispatchableSelfMethod(m, ownerUndispatchable, droppedSelfCall) {
				droppedSelfCall[m.Name] = true
				continue
			}
			next = append(next, m)
		}
		survived = next
		if len(droppedSelfCall) == before {
			break
		}
	}
	return survived
}

// methodHasUnsupportedLLVMShape drops stdlib helpers that are valid
// Osty but still outside the legacy AST LLVM emitter's return-shape
// support. Keeping these on every injected specialization makes
// unrelated user programs compile unused helper bodies and wall before
// they reach the method they actually called.
func methodHasUnsupportedLLVMShape(owner string, m *ir.FnDecl) bool {
	if m == nil {
		return false
	}
	if owner == "List" && m.Name == "reverse" {
		return true
	}
	// Map.find returns `(K, V)?`. The legacy AST LLVM path currently
	// treats optional tuple returns as pointer-shaped at the function
	// boundary while expression lowering produces the concrete optional
	// aggregate, so eager specialization fails even when `find` is not
	// called. Demand-driven method emission can remove this guard.
	return owner == "Map" && m.Name == "find"
}

// ownerUndispatchableMethodSet returns the set of **body-less
// intrinsic** method names the backend cannot lower from a bare
// `self.<name>(...)` call site on a specialization of `owner`. The
// cascade only uses these as starting points — bodied methods that
// only call dispatched intrinsics stay.
//
// Kept in sync with the backend's `listMethodInfo` / `mapMethodInfo` /
// `setMethodInfo` whitelists owned by the native LLVM generator
// (`cmd/osty-native-lirproto`). When a new dispatch lands there, drop
// the corresponding name here so its callers stop getting
// cascade-stripped.
func ownerUndispatchableMethodSet(owner string) map[string]bool {
	switch owner {
	case "List":
		// Body-less declarations on List<T> (see
		// `internal/stdlib/modules/collections.osty`): len, get,
		// indexOf, sorted, push, pop, insert, removeAt, sort,
		// reverse, clear. listMethodInfo whitelist covers all except
		// indexOf / removeAt / sort / reverse.
		return map[string]bool{
			"indexOf":  true,
			"removeAt": true,
			"sort":     true,
			"reverse":  true,
		}
	}
	// Map: every body-less intrinsic (get, insert, remove, keys,
	// entries, len, isEmpty, clear) is in mapMethodInfo.
	// Set: every body-less intrinsic (len, isEmpty, contains, insert,
	// remove, toList) is in setMethodInfo.
	// Option / Result: isSome / isNone are handled via
	// emitOptionMethodCall directly off ptr source types.
	return nil
}

// methodBodyCallsUndispatchableSelfMethod walks m.Body for any
// `self.<name>(...)` call whose name is in either `undispatchable`
// (hard-coded owner gap) or `cascade` (already-dropped by a previous
// pass). Returns true on the first match. Non-self receivers are
// ignored; this is about the specialized owner's own dispatch surface
// only.
func methodBodyCallsUndispatchableSelfMethod(m *ir.FnDecl, undispatchable, cascade map[string]bool) bool {
	if m == nil || m.Body == nil {
		return false
	}
	if len(undispatchable) == 0 && len(cascade) == 0 {
		return false
	}
	missing := func(name string) bool {
		return undispatchable[name] || cascade[name]
	}
	found := false
	ir.Walk(ir.VisitorFunc(func(n ir.Node) bool {
		if found {
			return false
		}
		switch x := n.(type) {
		case *ir.MethodCall:
			if id, ok := x.Receiver.(*ir.Ident); ok && id.Name == "self" && missing(x.Name) {
				found = true
				return false
			}
		case *ir.CallExpr:
			if fx, ok := x.Callee.(*ir.FieldExpr); ok {
				if id, ok := fx.X.(*ir.Ident); ok && id.Name == "self" && missing(fx.Name) {
					found = true
					return false
				}
			}
		}
		return true
	}), m.Body)
	return found
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
	surface, ok := stdlib.BuiltinTypeSurfaceByName(name)
	return ok && surface.Injectable
}

// cloneStdlibTypeDecl deep-clones a StructDecl or EnumDecl so the
// per-user-module appended copy can be rewritten by the monomorphizer
// without disturbing the shared cache. Non-builtin stdlib-local types
// are qualified as "<module>.<Type>" so they cannot collide with a
// user's ordinary top-level type named Reply, Header, Image, etc.
func cloneStdlibTypeDecl(key stdlibTypeKey, d ir.Decl, moduleTypes map[string]bool) ir.Decl {
	if d == nil {
		return nil
	}
	cp, _ := ir.Clone(d).(ir.Decl)
	qualifyStdlibDeclTypes(cp, key.Module, moduleTypes)
	switch x := cp.(type) {
	case *ir.StructDecl:
		if shouldQualifyStdlibTypeName(key.Module, x.Name, moduleTypes) {
			x.Name = qualifiedStdlibTypeName(key.Module, x.Name)
		}
		// Builtin reference types (`Map<K,V>`, `Set<T>`, …) route their
		// primitive operations through runtime intrinsics — `Map.get` /
		// `Map.insert` / `Map.clear` / `Set.insert` / etc. carry no
		// `.osty` body. When monomorph specializes the type, those
		// bodyless methods produce empty `_ZTS…<Type>__<method>` fn
		// decls that the LIR Proto backend rejects with "no blocks".
		// Strip them from the injected clone so monomorph never sees
		// them; call sites still resolve via the `check_env.osty`
		// registry and the MIR lowerer's intrinsic dispatch.
		if isBuiltinReferenceType(x.Name) {
			x.Methods = keepBodiedMethods(x.Methods)
		}
	case *ir.EnumDecl:
		if shouldQualifyStdlibTypeName(key.Module, x.Name, moduleTypes) {
			x.Name = qualifiedStdlibTypeName(key.Module, x.Name)
		}
	}
	return cp
}

// isBuiltinReferenceType reports whether name is a pointer-shaped
// builtin generic whose primitive operations are runtime intrinsics
// rather than bodied Osty methods. Used by `cloneStdlibTypeDecl` to
// drop bodyless method declarations so monomorph does not emit empty
// `_ZTS…__<method>` fn decls.
func isBuiltinReferenceType(name string) bool {
	switch name {
	case "Map", "Set":
		return true
	}
	return false
}

func keepBodiedMethods(methods []*ir.FnDecl) []*ir.FnDecl {
	if len(methods) == 0 {
		return methods
	}
	// Allocate a fresh slice instead of compacting in place with
	// `methods[:0]` so the filtered-out method decls are not held
	// alive by the backing array, and so callers that retain a
	// reference to `methods` (or reslice it later) cannot see the
	// stripped entries reappear past `len(out)` via reslicing.
	out := make([]*ir.FnDecl, 0, len(methods))
	for _, m := range methods {
		if m == nil {
			continue
		}
		if m.Body == nil {
			continue
		}
		out = append(out, m)
	}
	return out
}

func qualifyStdlibDeclTypes(d ir.Decl, module string, moduleTypes map[string]bool) {
	if d == nil || module == "" || len(moduleTypes) == 0 {
		return
	}
	ir.Walk(ir.VisitorFunc(func(n ir.Node) bool {
		switch x := n.(type) {
		case *ir.FnDecl:
			x.Return = qualifyStdlibType(x.Return, module, moduleTypes)
		case *ir.Param:
			x.Type = qualifyStdlibType(x.Type, module, moduleTypes)
		case *ir.Field:
			x.Type = qualifyStdlibType(x.Type, module, moduleTypes)
		case *ir.LetDecl:
			x.Type = qualifyStdlibType(x.Type, module, moduleTypes)
		case *ir.LetStmt:
			x.Type = qualifyStdlibType(x.Type, module, moduleTypes)
		case *ir.IntLit:
			x.T = qualifyStdlibType(x.T, module, moduleTypes)
		case *ir.FloatLit:
			x.T = qualifyStdlibType(x.T, module, moduleTypes)
		case *ir.Ident:
			x.T = qualifyStdlibType(x.T, module, moduleTypes)
			qualifyStdlibTypeList(x.TypeArgs, module, moduleTypes)
		case *ir.UnaryExpr:
			x.T = qualifyStdlibType(x.T, module, moduleTypes)
		case *ir.BinaryExpr:
			x.T = qualifyStdlibType(x.T, module, moduleTypes)
		case *ir.CallExpr:
			x.T = qualifyStdlibType(x.T, module, moduleTypes)
			qualifyStdlibTypeList(x.TypeArgs, module, moduleTypes)
		case *ir.MethodCall:
			x.T = qualifyStdlibType(x.T, module, moduleTypes)
			qualifyStdlibTypeList(x.TypeArgs, module, moduleTypes)
		case *ir.ListLit:
			x.Elem = qualifyStdlibType(x.Elem, module, moduleTypes)
		case *ir.MapLit:
			x.KeyT = qualifyStdlibType(x.KeyT, module, moduleTypes)
			x.ValT = qualifyStdlibType(x.ValT, module, moduleTypes)
		case *ir.TupleLit:
			x.T = qualifyStdlibType(x.T, module, moduleTypes)
		case *ir.StructLit:
			x.T = qualifyStdlibType(x.T, module, moduleTypes)
			if shouldQualifyStdlibTypeName(module, x.TypeName, moduleTypes) {
				x.TypeName = qualifiedStdlibTypeName(module, x.TypeName)
			}
		case *ir.VariantLit:
			x.T = qualifyStdlibType(x.T, module, moduleTypes)
			if shouldQualifyStdlibTypeName(module, x.Enum, moduleTypes) {
				x.Enum = qualifiedStdlibTypeName(module, x.Enum)
			}
		case *ir.BlockExpr:
			x.T = qualifyStdlibType(x.T, module, moduleTypes)
		case *ir.IfExpr:
			x.T = qualifyStdlibType(x.T, module, moduleTypes)
		case *ir.IfLetExpr:
			x.T = qualifyStdlibType(x.T, module, moduleTypes)
		case *ir.MatchExpr:
			x.T = qualifyStdlibType(x.T, module, moduleTypes)
		case *ir.FieldExpr:
			x.T = qualifyStdlibType(x.T, module, moduleTypes)
		case *ir.IndexExpr:
			x.T = qualifyStdlibType(x.T, module, moduleTypes)
		case *ir.TupleAccess:
			x.T = qualifyStdlibType(x.T, module, moduleTypes)
		case *ir.RangeLit:
			x.T = qualifyStdlibType(x.T, module, moduleTypes)
		case *ir.QuestionExpr:
			x.T = qualifyStdlibType(x.T, module, moduleTypes)
		case *ir.CoalesceExpr:
			x.T = qualifyStdlibType(x.T, module, moduleTypes)
		case *ir.Closure:
			x.T = qualifyStdlibType(x.T, module, moduleTypes)
			x.Return = qualifyStdlibType(x.Return, module, moduleTypes)
		}
		return true
	}), d)
}

func qualifyStdlibTypeList(types []ir.Type, module string, moduleTypes map[string]bool) {
	for i, t := range types {
		types[i] = qualifyStdlibType(t, module, moduleTypes)
	}
}

func qualifyStdlibType(t ir.Type, module string, moduleTypes map[string]bool) ir.Type {
	switch x := t.(type) {
	case *ir.NamedType:
		for i, a := range x.Args {
			x.Args[i] = qualifyStdlibType(a, module, moduleTypes)
		}
		if shouldQualifyStdlibTypeName(module, x.Name, moduleTypes) && x.Package == "" {
			x.Package = module
		}
	case *ir.OptionalType:
		x.Inner = qualifyStdlibType(x.Inner, module, moduleTypes)
	case *ir.TupleType:
		for i, e := range x.Elems {
			x.Elems[i] = qualifyStdlibType(e, module, moduleTypes)
		}
	case *ir.FnType:
		for i, p := range x.Params {
			x.Params[i] = qualifyStdlibType(p, module, moduleTypes)
		}
		x.Return = qualifyStdlibType(x.Return, module, moduleTypes)
	}
	return t
}

func shouldQualifyStdlibTypeName(module, name string, moduleTypes map[string]bool) bool {
	if module == "" || name == "" || !moduleTypes[name] {
		return false
	}
	return !isInjectableTypeName(name)
}

func qualifiedStdlibTypeName(module, name string) string {
	if module == "" || name == "" || strings.Contains(name, ".") {
		return name
	}
	return module + "." + name
}

// moduleForStdlibType is a lookup helper used by tests and
// diagnostics: given a built-in type name, returns the stdlib module
// that declares it, or "" if unknown.
func moduleForStdlibType(name string) string {
	surface, ok := stdlib.BuiltinTypeSurfaceByName(name)
	if !ok || !surface.Injectable {
		return ""
	}
	return surface.Module
}

// The walker relies on ir.Walk visiting concrete types via their
// enclosing nodes. Suppress the unused warning on moduleForStdlibType
// (only used in tests) with an unused reference.
var _ = moduleForStdlibType
var _ ast.Node = (*ast.File)(nil)
