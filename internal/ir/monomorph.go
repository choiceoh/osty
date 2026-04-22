package ir

import (
	"fmt"
	"strings"
)

// Monomorphize performs generic monomorphization over an IR Module,
// returning a new Module in which:
//
//   - every generic free FnDecl has been replaced by its set of
//     concrete specializations (one per (fn, type-args) tuple reachable
//     from non-generic callers);
//   - every generic CallExpr has been retargeted at the mangled
//     specialization name, with TypeArgs cleared;
//   - every TypeVar has been substituted with its concrete type.
//
// Generic fns that are never called are dropped entirely — matching the
// language spec's "header-like" demand-driven framing
// (LANG_SPEC_v0.5/02-type-system.md §2.7.3).
//
// The returned []error collects non-fatal issues (unresolved type
// parameters, arity mismatches). A nil module yields (nil, nil).
//
// Phase 1 scope:
//   - generic free functions
//   - type-args may include builtin aggregates (List<T>, Option<T>, …);
//     mangled symbol names reflect that, and the existing emitter paths
//     handle the aggregate runtime layout
//
// Out of scope (Phase 2+):
//   - generic struct / enum / interface declarations
//   - generic methods (they stay under the existing unsupported
//     diagnostic)
//   - turbofish on bare function-pointer expressions (`let f = id::<Int>`)
func Monomorphize(mod *Module) (*Module, []error) {
	if mod == nil {
		return nil, nil
	}
	state := &monoState{
		pkg:                     mod.Package,
		out:                     &Module{Package: mod.Package, SpanV: mod.SpanV},
		genericsByName:          map[string]*FnDecl{},
		seen:                    map[string]string{},
		genericStructsByName:    map[string]*StructDecl{},
		genericEnumsByName:      map[string]*EnumDecl{},
		genericInterfacesByName: map[string]*InterfaceDecl{},
		typeSeen:                map[string]string{},
		structsByName:           map[string]*StructDecl{},
		enumsByName:             map[string]*EnumDecl{},
		structsByMangled:        map[string]*StructDecl{},
		enumsByMangled:          map[string]*EnumDecl{},
		originalStructOf:        map[string]*StructDecl{},
		originalEnumOf:          map[string]*EnumDecl{},
		receiverEnvOf:           map[string]SubstEnv{},
		methodSeen:              map[string]string{},
	}

	// Pass 1: index every generic top-level declaration so the scanner
	// can resolve references against the original forms without
	// revisiting mod.Decls for each call/type site. Generic free fns go
	// in genericsByName; generic struct/enum declarations go in
	// genericStructsByName / genericEnumsByName (Phase 2). Every struct
	// and enum is *also* registered in structsByName / enumsByName so
	// Phase 4 method-specialization can resolve an owner from a bare
	// source name (non-generic owner path).
	for _, d := range mod.Decls {
		switch x := d.(type) {
		case *FnDecl:
			if len(x.Generics) > 0 {
				state.genericsByName[x.Name] = x
			}
		case *StructDecl:
			state.structsByName[x.Name] = x
			if len(x.Generics) > 0 {
				state.genericStructsByName[x.Name] = x
			}
		case *EnumDecl:
			state.enumsByName[x.Name] = x
			if len(x.Generics) > 0 {
				state.genericEnumsByName[x.Name] = x
			}
		case *InterfaceDecl:
			if len(x.Generics) > 0 {
				state.genericInterfacesByName[x.Name] = x
			}
		}
	}

	// Pass 2: copy non-generic decls into the output module (cloning so
	// rewrites are isolated from the input), scan their bodies for
	// initial instantiation requests, and append the script. Generic
	// top-level declarations are dropped here; their specializations
	// will be appended during worklist drain.
	for _, d := range mod.Decls {
		if isGenericTopLevel(d) {
			continue
		}
		out := cloneDecl(d)
		state.out.Decls = append(state.out.Decls, out)
		// Phase 4: register the cloned non-generic owner so method
		// specialization can append onto the output struct/enum directly.
		switch cp := out.(type) {
		case *StructDecl:
			state.structsByName[cp.Name] = cp
		case *EnumDecl:
			state.enumsByName[cp.Name] = cp
		}
		state.scanDecl(out)
	}
	state.pushLocalTypeScope()
	for _, s := range mod.Script {
		cloned := cloneStmt(s)
		state.out.Script = append(state.out.Script, cloned)
		state.scanStmt(cloned)
	}
	state.popLocalTypeScope()

	// Pass 3+4+5 (interleaved): drain all three queues until none grows.
	// Emitting a fn specialization may discover generic call sites
	// (grows fn queue) or generic type references (grows type queue);
	// emitting a type specialization may discover generic method calls
	// (grows method queue). We loop until every queue reaches its fixed
	// point.
	for len(state.queue) > 0 || len(state.typeQueue) > 0 || len(state.methodQueue) > 0 {
		for i := 0; i < len(state.queue); i++ {
			state.emitSpecialization(state.queue[i])
		}
		state.queue = state.queue[:0]
		for i := 0; i < len(state.typeQueue); i++ {
			state.emitTypeSpecialization(state.typeQueue[i])
		}
		state.typeQueue = state.typeQueue[:0]
		for i := 0; i < len(state.methodQueue); i++ {
			state.emitMethodSpecialization(state.methodQueue[i])
		}
		state.methodQueue = state.methodQueue[:0]
	}

	// Pass 6: drop any preserved generic methods left on struct/enum
	// specializations. These originals served as templates for
	// `rewriteGenericMethodCall`; the concrete specializations have
	// already been appended to their owners' Methods lists.
	state.dropPreservedGenericMethods()

	return state.out, state.errs
}

// isGenericTopLevel reports whether a top-level declaration carries
// generic parameters and therefore must be replaced by concrete
// specializations rather than copied verbatim into the output.
func isGenericTopLevel(d Decl) bool {
	switch x := d.(type) {
	case *FnDecl:
		return len(x.Generics) > 0
	case *StructDecl:
		return len(x.Generics) > 0
	case *EnumDecl:
		return len(x.Generics) > 0
	case *InterfaceDecl:
		return len(x.Generics) > 0
	}
	return false
}

// monoState carries the per-module monomorphization bookkeeping.
type monoState struct {
	pkg            string
	out            *Module
	genericsByName map[string]*FnDecl // generic free fn by source name
	// seen maps a dedup key to the mangled symbol name so duplicate
	// requests short-circuit to the same specialization.
	seen map[string]string
	// queue is the worklist of pending specializations. Index-based
	// drain lets new entries appended during scanning be picked up.
	queue []monoInstance
	errs  []error

	// Phase 2/5: generic nominal-type bookkeeping.
	//
	// genericStructsByName / genericEnumsByName / genericInterfacesByName
	// index the original generic declarations so scanning can recognize
	// a generic NamedType reference by source name.
	genericStructsByName    map[string]*StructDecl
	genericEnumsByName      map[string]*EnumDecl
	genericInterfacesByName map[string]*InterfaceDecl
	// typeSeen maps a MonomorphTypeDedupeKey to the mangled type-symbol
	// so duplicate requests short-circuit.
	typeSeen map[string]string
	// typeQueue is the worklist of pending struct/enum/interface specializations.
	typeQueue []monoTypeInstance
	// localTypeScopes tracks statement-local bindings while scanning a
	// concrete function or script body so unresolved Idents can recover
	// a type from an earlier let-binding.
	localTypeScopes []map[string]Type

	// Phase 4: method-specialization bookkeeping.
	//
	// structsByName / enumsByName index every owner (generic AND
	// non-generic) by source name. After Pass 2 the non-generic entries
	// point at the *output* clones so method-specialization emitters can
	// append directly onto the emitted struct/enum.
	structsByName map[string]*StructDecl
	enumsByName   map[string]*EnumDecl
	// structsByMangled / enumsByMangled index emitted specialization
	// clones by their mangled nominal symbol (`_ZTSN…E`).
	structsByMangled map[string]*StructDecl
	enumsByMangled   map[string]*EnumDecl
	// originalStructOf / originalEnumOf map a mangled nominal back to
	// the original generic declaration in the input module so method
	// specialization can find the still-generic method template.
	originalStructOf map[string]*StructDecl
	originalEnumOf   map[string]*EnumDecl
	// receiverEnvOf remembers the owner-level substitution env used
	// when the nominal specialization was emitted. Method bodies may
	// reference receiver-level TypeVars, so method specialization
	// substitutes both envs at once.
	receiverEnvOf map[string]SubstEnv
	// methodSeen maps MonomorphMethodDedupeKey(ownerMangled, method, args)
	// to the mangled method-local name.
	methodSeen map[string]string
	// methodQueue is the worklist of pending method specializations.
	methodQueue []monoMethodInstance
}

// monoMethodInstance is one pending method specialization. ownerKind
// selects the struct (0) / enum (1) bucket used at emit time to look
// the final owner clone up in structsByMangled / enumsByMangled (with
// a fallback to structsByName / enumsByName for non-generic owners).
// Both the ref and the receiver-level env are resolved at emit time so
// a method request can be queued before its owner has been emitted.
// methodEnv holds the method-local substitution (U → concrete, …);
// the receiver-level env is fetched from receiverEnvOf during emit and
// merged on top.
type monoMethodInstance struct {
	ownerKind         int // 0=struct, 1=enum
	ownerMangled      string
	origMethod        *FnDecl
	mangledMethodName string
	methodEnv         SubstEnv
}

// monoInstance is one pending free-fn specialization.
type monoInstance struct {
	fn       *FnDecl
	typeArgs []Type
	mangled  string
}

// monoTypeInstance is one pending nominal-type specialization. Exactly
// one of structDecl / enumDecl / interfaceDecl is non-nil; the
// populated slot distinguishes the emitter branch. typeArgs are the
// concrete types matching the declaration's generics, and mangled is
// the `_ZTS…` symbol the engine has already assigned and written to
// typeSeen.
type monoTypeInstance struct {
	structDecl    *StructDecl
	enumDecl      *EnumDecl
	interfaceDecl *InterfaceDecl
	typeArgs      []Type
	mangled       string
}

// addErr appends a non-fatal issue.
func (s *monoState) addErr(format string, args ...any) {
	s.errs = append(s.errs, fmt.Errorf(format, args...))
}

// request enqueues a (generic fn, concrete type-args) pair for
// specialization and returns the mangled name the caller should use at
// the call site. Duplicate requests short-circuit via the seen map.
// Returns "" when the request is malformed (arity mismatch, unresolved
// type parameters).
func (s *monoState) request(fn *FnDecl, typeArgs []Type) string {
	if fn == nil {
		return ""
	}
	if !MonomorphShouldInstantiate(len(typeArgs), len(fn.Generics)) {
		s.addErr("monomorph: arity mismatch for %s: %d type args vs %d generics",
			fn.Name, len(typeArgs), len(fn.Generics))
		return ""
	}
	// Concrete type args must not contain lingering TypeVars — that
	// means an upstream substitution missed a scope. Bail so we don't
	// emit an invalid symbol.
	for i, ta := range typeArgs {
		if containsTypeVar(ta) {
			s.addErr("monomorph: type arg %d of %s still contains a type variable (%s)",
				i, fn.Name, typeString(ta))
			return ""
		}
	}
	typeArgCodes := make([]string, len(typeArgs))
	for i, ta := range typeArgs {
		typeArgCodes[i] = typeCodeOf(ta, s.pkg)
	}
	key := MonomorphDedupeKey(fn.Name, s.pkg, typeArgCodes)
	if mangled, ok := s.seen[key]; ok {
		return mangled
	}
	// Encode param types for the symbol tail, substituting this
	// instance's env so the parameter codes reflect concrete types.
	paramCodes := make([]string, 0, len(fn.Params))
	env := buildSubstEnv(fn.Generics, typeArgs)
	for _, p := range fn.Params {
		substituted := cloneAndSubstType(p.Type, env)
		paramCodes = append(paramCodes, typeCodeOf(substituted, s.pkg))
	}
	returnCode := ""
	if fn.Return != nil {
		substituted := cloneAndSubstType(fn.Return, env)
		returnCode = typeCodeOf(substituted, s.pkg)
	}
	req := NewMonomorphRequest(s.pkg, fn.Name, typeArgCodes, paramCodes, returnCode)
	mangled := MonomorphMangleFn(req).Symbol()
	s.seen[key] = mangled
	s.queue = append(s.queue, monoInstance{
		fn:       fn,
		typeArgs: append([]Type(nil), typeArgs...),
		mangled:  mangled,
	})
	return mangled
}

// requestStructType enqueues a (generic struct decl, concrete type-args)
// pair for specialization and returns the mangled type-symbol the
// caller should substitute at the reference site. Duplicate requests
// short-circuit via the typeSeen map. Returns "" when the request is
// malformed (arity mismatch, unresolved type parameters).
func (s *monoState) requestStructType(decl *StructDecl, typeArgs []Type) string {
	if decl == nil {
		return ""
	}
	if !MonomorphShouldInstantiate(len(typeArgs), len(decl.Generics)) {
		s.addErr("monomorph: arity mismatch for struct %s: %d type args vs %d generics",
			decl.Name, len(typeArgs), len(decl.Generics))
		return ""
	}
	for i, ta := range typeArgs {
		if containsTypeVar(ta) {
			s.addErr("monomorph: type arg %d of struct %s still contains a type variable (%s)",
				i, decl.Name, typeString(ta))
			return ""
		}
	}
	typeArgCodes := make([]string, len(typeArgs))
	for i, ta := range typeArgs {
		typeArgCodes[i] = typeCodeOf(ta, s.pkg)
	}
	key := MonomorphTypeDedupeKey(decl.Name, s.pkg, typeArgCodes)
	if mangled, ok := s.typeSeen[key]; ok {
		return mangled
	}
	req := NewMonomorphTypeRequest(s.pkg, decl.Name, typeArgCodes)
	mangled := MonomorphMangleType(req).Symbol()
	// Important: record the mangled name *before* enqueueing so that a
	// recursive field type (e.g. `struct List<T> { next: Option<List<T>>? }`)
	// that requests the same specialization from inside the emitter hits
	// the seen cache and terminates instead of looping.
	s.typeSeen[key] = mangled
	// Phase 4: expose the mangled→original mapping as soon as the
	// request is recognized so method-call rewriting that happens
	// before the owner specialization has actually been emitted still
	// resolves its owner through originalStructOf.
	s.originalStructOf[mangled] = decl
	s.typeQueue = append(s.typeQueue, monoTypeInstance{
		structDecl: decl,
		typeArgs:   append([]Type(nil), typeArgs...),
		mangled:    mangled,
	})
	return mangled
}

// requestEnumType is the enum counterpart of requestStructType.
func (s *monoState) requestEnumType(decl *EnumDecl, typeArgs []Type) string {
	if decl == nil {
		return ""
	}
	if !MonomorphShouldInstantiate(len(typeArgs), len(decl.Generics)) {
		s.addErr("monomorph: arity mismatch for enum %s: %d type args vs %d generics",
			decl.Name, len(typeArgs), len(decl.Generics))
		return ""
	}
	for i, ta := range typeArgs {
		if containsTypeVar(ta) {
			s.addErr("monomorph: type arg %d of enum %s still contains a type variable (%s)",
				i, decl.Name, typeString(ta))
			return ""
		}
	}
	typeArgCodes := make([]string, len(typeArgs))
	for i, ta := range typeArgs {
		typeArgCodes[i] = typeCodeOf(ta, s.pkg)
	}
	key := MonomorphTypeDedupeKey(decl.Name, s.pkg, typeArgCodes)
	if mangled, ok := s.typeSeen[key]; ok {
		return mangled
	}
	req := NewMonomorphTypeRequest(s.pkg, decl.Name, typeArgCodes)
	mangled := MonomorphMangleType(req).Symbol()
	s.typeSeen[key] = mangled
	// Phase 4: see requestStructType for the rationale.
	s.originalEnumOf[mangled] = decl
	s.typeQueue = append(s.typeQueue, monoTypeInstance{
		enumDecl: decl,
		typeArgs: append([]Type(nil), typeArgs...),
		mangled:  mangled,
	})
	return mangled
}

// emitTypeSpecialization dispatches one queued nominal-type instance
// to the appropriate struct/enum/interface emitter.
func (s *monoState) emitTypeSpecialization(rec monoTypeInstance) {
	switch {
	case rec.structDecl != nil:
		s.emitStructSpecialization(rec)
	case rec.enumDecl != nil:
		s.emitEnumSpecialization(rec)
	case rec.interfaceDecl != nil:
		s.emitInterfaceSpecialization(rec)
	}
}

// requestInterfaceType is the interface counterpart of
// requestStructType / requestEnumType.
func (s *monoState) requestInterfaceType(decl *InterfaceDecl, typeArgs []Type) string {
	if decl == nil {
		return ""
	}
	if !MonomorphShouldInstantiate(len(typeArgs), len(decl.Generics)) {
		s.addErr("monomorph: arity mismatch for interface %s: %d type args vs %d generics",
			decl.Name, len(typeArgs), len(decl.Generics))
		return ""
	}
	for i, ta := range typeArgs {
		if containsTypeVar(ta) {
			s.addErr("monomorph: type arg %d of interface %s still contains a type variable (%s)",
				i, decl.Name, typeString(ta))
			return ""
		}
	}
	typeArgCodes := make([]string, len(typeArgs))
	for i, ta := range typeArgs {
		typeArgCodes[i] = typeCodeOf(ta, s.pkg)
	}
	key := MonomorphTypeDedupeKey(decl.Name, s.pkg, typeArgCodes)
	if mangled, ok := s.typeSeen[key]; ok {
		return mangled
	}
	req := NewMonomorphTypeRequest(s.pkg, decl.Name, typeArgCodes)
	mangled := MonomorphMangleType(req).Symbol()
	s.typeSeen[key] = mangled
	s.typeQueue = append(s.typeQueue, monoTypeInstance{
		interfaceDecl: decl,
		typeArgs:      append([]Type(nil), typeArgs...),
		mangled:       mangled,
	})
	return mangled
}

// emitInterfaceSpecialization materializes one queued interface
// instance. Mirrors emitStructSpecialization: the interface is cloned,
// Extends + method signatures are substituted, and the specialization
// is appended to out.Decls. Method-local generic parameters on
// interface methods are preserved as templates (matching struct/enum
// policy); the Pass-6 cleanup strips them from the output.
func (s *monoState) emitInterfaceSpecialization(rec monoTypeInstance) {
	clone := cloneInterfaceDecl(rec.interfaceDecl)
	clone.Name = rec.mangled
	clone.Generics = nil
	env := buildSubstEnv(rec.interfaceDecl.Generics, rec.typeArgs)
	SubstituteTypes(clone, env)
	for i, ext := range clone.Extends {
		clone.Extends[i] = s.rewriteType(ext)
	}
	for _, m := range clone.Methods {
		if m == nil || len(m.Generics) > 0 {
			continue
		}
		s.rewriteFnSignature(m)
		s.scanFnBody(m)
	}
	s.out.Decls = append(s.out.Decls, clone)
}

// emitStructSpecialization materializes one queued struct instance:
// clones the original declaration, renames it to the mangled symbol,
// substitutes type parameters inside fields and method signatures/bodies,
// rewrites any surviving user-generic references (nested generics like
// `Option<Pair<T, T>>` become `Option<_ZTSN…E>` after `T` is concretized),
// drops any method carrying its own generics (Phase 3+ scope), scans
// surviving method bodies for further generic call sites, and appends
// the result to out.Decls.
func (s *monoState) emitStructSpecialization(rec monoTypeInstance) {
	clone := cloneStructDecl(rec.structDecl)
	clone.Name = rec.mangled
	clone.Generics = nil
	// Option B Phase 2c: preserve the original surface name + concrete
	// type-args so downstream stages (LLVM backend, AST bridge) can
	// re-associate a `_ZTS…` mangled struct back with its surface
	// Map<String, Int> / Option<T> form for intrinsic dispatch.
	// Recorded BEFORE SubstituteTypes rewrites typeArgs in place.
	if isStdlibBuiltinName(rec.structDecl.Name) {
		clone.BuiltinSource = rec.structDecl.Name
		if len(rec.typeArgs) > 0 {
			clone.BuiltinSourceArgs = make([]Type, len(rec.typeArgs))
			for i, ta := range rec.typeArgs {
				clone.BuiltinSourceArgs[i] = CloneType(ta)
			}
		}
	}
	env := buildSubstEnv(rec.structDecl.Generics, rec.typeArgs)
	SubstituteTypes(clone, env)
	for _, f := range clone.Fields {
		if f == nil {
			continue
		}
		f.Type = s.rewriteType(f.Type)
	}
	clone.Methods = s.keepNonGenericMethods(clone.Name, clone.Methods)
	s.out.Decls = append(s.out.Decls, clone)
	// Phase 4: record the mangled nominal ↔ original/clone mapping so
	// method-specialization can resolve owners and substitute properly.
	s.structsByMangled[clone.Name] = clone
	s.originalStructOf[clone.Name] = rec.structDecl
	s.receiverEnvOf[clone.Name] = env
}

// isStdlibBuiltinName reports whether a name identifies a stdlib
// built-in generic template that the llvm backend's intrinsic
// dispatch needs to re-recognize after monomorphization. Kept as a
// focused whitelist rather than a prefix check so user types that
// happen to share names with stdlib modules aren't accidentally
// tagged.
func isStdlibBuiltinName(name string) bool {
	switch name {
	case "Map", "List", "Set", "Option", "Result":
		return true
	}
	return false
}

// emitEnumSpecialization is the enum counterpart of emitStructSpecialization.
func (s *monoState) emitEnumSpecialization(rec monoTypeInstance) {
	clone := cloneEnumDecl(rec.enumDecl)
	clone.Name = rec.mangled
	clone.Generics = nil
	if isStdlibBuiltinName(rec.enumDecl.Name) {
		clone.BuiltinSource = rec.enumDecl.Name
		if len(rec.typeArgs) > 0 {
			clone.BuiltinSourceArgs = make([]Type, len(rec.typeArgs))
			for i, ta := range rec.typeArgs {
				clone.BuiltinSourceArgs[i] = CloneType(ta)
			}
		}
	}
	env := buildSubstEnv(rec.enumDecl.Generics, rec.typeArgs)
	SubstituteTypes(clone, env)
	for _, v := range clone.Variants {
		if v == nil {
			continue
		}
		for i, p := range v.Payload {
			v.Payload[i] = s.rewriteType(p)
		}
	}
	clone.Methods = s.keepNonGenericMethods(clone.Name, clone.Methods)
	s.out.Decls = append(s.out.Decls, clone)
	// Phase 4: mirror the struct index (see emitStructSpecialization).
	s.enumsByMangled[clone.Name] = clone
	s.originalEnumOf[clone.Name] = rec.enumDecl
	s.receiverEnvOf[clone.Name] = env
}

// rewriteType walks a Type tree top-down rewriting every generic
// user-declared NamedType into a concrete `_ZTS…` mangled reference
// and enqueueing the matching specialization on typeQueue. Because Go's
// Type is an interface, caller-side reassignment at the parent slot
// drives the traversal — the returned Type should be stored back into
// the slot the original came from (e.g. `p.Type = s.rewriteType(p.Type)`).
//
// NamedType.Args are rewritten first (bottom-up) so nested generic
// references (`Pair<Maybe<Bool>, …>`) encode their inner mangled names
// into the outer request's type-arg codes.
func (s *monoState) rewriteType(t Type) Type {
	if t == nil {
		return nil
	}
	switch x := t.(type) {
	case *NamedType:
		for i, a := range x.Args {
			x.Args[i] = s.rewriteType(a)
		}
		// Builtin nominal types (Map, Option, List, Set, Result, …)
		// are skipped unless a generic template with the same source
		// name has been registered in Pass 1 — which happens when the
		// backend's stdlib-type injector has fed in collections.osty /
		// option.osty / result.osty ahead of monomorphization. In that
		// case we fall through to the normal specialization path so
		// bodied helpers like Map.getOr compile as concrete Map$K$V
		// methods instead of staying hand-emitted in llvmgen.
		if x.Builtin {
			if _, hasStruct := s.genericStructsByName[x.Name]; !hasStruct {
				if _, hasEnum := s.genericEnumsByName[x.Name]; !hasEnum {
					return x
				}
			}
		}
		// Only nominal types with generic templates drive
		// specialization. Concrete (non-generic) references like
		// `Point` fall through unchanged — they were not indexed in
		// Pass 1.
		if len(x.Args) == 0 {
			return x
		}
		if sd, ok := s.genericStructsByName[x.Name]; ok {
			if mangled := s.requestStructType(sd, x.Args); mangled != "" {
				return &NamedType{Name: mangled}
			}
		}
		if ed, ok := s.genericEnumsByName[x.Name]; ok {
			if mangled := s.requestEnumType(ed, x.Args); mangled != "" {
				return &NamedType{Name: mangled}
			}
		}
		if id, ok := s.genericInterfacesByName[x.Name]; ok {
			if mangled := s.requestInterfaceType(id, x.Args); mangled != "" {
				return &NamedType{Name: mangled}
			}
		}
		return x
	case *OptionalType:
		x.Inner = s.rewriteType(x.Inner)
		return x
	case *TupleType:
		for i, e := range x.Elems {
			x.Elems[i] = s.rewriteType(e)
		}
		return x
	case *FnType:
		for i, p := range x.Params {
			x.Params[i] = s.rewriteType(p)
		}
		x.Return = s.rewriteType(x.Return)
		return x
	}
	return t
}

// rewriteExprType rewrites every Type-valued field on an expression
// node (e.g. Expr.T, ListLit.Elem, MapLit.KeyT/ValT, Closure.Return/T,
// Ident.TypeArgs, CallExpr.TypeArgs, …). For StructLit/VariantLit it
// also syncs TypeName/Enum with the mangled nominal name so later
// lowering stages agree on the specialization to dispatch against.
//
// Called at the top of scanExpr so every visited expression has its
// type slots brought up to date before the recursion drills further —
// including deeply nested expressions inside closures and match arms.
func (s *monoState) rewriteExprType(e Expr) {
	if e == nil {
		return
	}
	switch x := e.(type) {
	case *IntLit:
		x.T = s.rewriteType(x.T)
	case *FloatLit:
		x.T = s.rewriteType(x.T)
	case *Ident:
		if isUnresolvedType(x.T) && (x.Kind == IdentLocal || x.Kind == IdentParam) {
			if inferred, ok := s.lookupLocalType(x.Name); ok {
				x.T = CloneType(inferred)
			}
		}
		x.T = s.rewriteType(x.T)
		for i, ta := range x.TypeArgs {
			x.TypeArgs[i] = s.rewriteType(ta)
		}
	case *UnaryExpr:
		x.T = s.rewriteType(x.T)
	case *BinaryExpr:
		x.T = s.rewriteType(x.T)
	case *CallExpr:
		x.T = s.rewriteType(x.T)
		for i, ta := range x.TypeArgs {
			x.TypeArgs[i] = s.rewriteType(ta)
		}
	case *MethodCall:
		x.T = s.rewriteType(x.T)
		for i, ta := range x.TypeArgs {
			x.TypeArgs[i] = s.rewriteType(ta)
		}
	case *ListLit:
		x.Elem = s.rewriteType(x.Elem)
	case *MapLit:
		x.KeyT = s.rewriteType(x.KeyT)
		x.ValT = s.rewriteType(x.ValT)
	case *TupleLit:
		x.T = s.rewriteType(x.T)
	case *StructLit:
		x.T = s.rewriteType(x.T)
		if nt, ok := x.T.(*NamedType); ok && nt.Name != "" {
			// Only override TypeName when the rewrite actually changed
			// the nominal: a non-generic struct lit keeps its source
			// name so existing llvmgen dispatch stays intact.
			if x.TypeName != nt.Name {
				x.TypeName = nt.Name
			}
		}
	case *VariantLit:
		if isUnresolvedType(x.T) {
			if inferred := s.inferVariantLiteralType(x); inferred != nil {
				x.T = inferred
			}
		}
		x.T = s.rewriteType(x.T)
		if nt, ok := x.T.(*NamedType); ok && nt.Name != "" {
			if x.Enum != "" && x.Enum != nt.Name {
				x.Enum = nt.Name
			}
		}
	case *BlockExpr:
		x.T = s.rewriteType(x.T)
	case *IfExpr:
		x.T = s.rewriteType(x.T)
	case *IfLetExpr:
		x.T = s.rewriteType(x.T)
	case *MatchExpr:
		x.T = s.rewriteType(x.T)
	case *FieldExpr:
		if field, base, ok := s.enumVariantFieldExpr(x); ok {
			if isUnresolvedType(field.T) {
				if named, ok := base.T.(*NamedType); ok && named != nil && named.Name != "" && !containsTypeVar(named) {
					field.T = CloneType(named)
				}
			}
			field.T = s.rewriteType(field.T)
			if nt, ok := field.T.(*NamedType); ok && nt.Name != "" {
				base.T = CloneType(nt)
				if base.Name != nt.Name {
					base.Name = nt.Name
				}
			}
			return
		}
		x.T = s.rewriteType(x.T)
	case *IndexExpr:
		x.T = s.rewriteType(x.T)
	case *TupleAccess:
		x.T = s.rewriteType(x.T)
	case *RangeLit:
		x.T = s.rewriteType(x.T)
	case *QuestionExpr:
		x.T = s.rewriteType(x.T)
	case *CoalesceExpr:
		x.T = s.rewriteType(x.T)
	case *Closure:
		x.Return = s.rewriteType(x.Return)
		x.T = s.rewriteType(x.T)
		for _, p := range x.Params {
			if p == nil {
				continue
			}
			p.Type = s.rewriteType(p.Type)
		}
		for _, c := range x.Captures {
			if c == nil {
				continue
			}
			c.T = s.rewriteType(c.T)
		}
	case *ErrorExpr:
		x.T = s.rewriteType(x.T)
	}
}

// keepNonGenericMethods filters a specialization's method list: methods
// carrying their own generic parameters are dropped with a warning
// (method-local generics are Phase 3+ scope). Surviving methods have
// their bodies scanned so any nested generic call sites they contain
// still drive the worklist.
// keepNonGenericMethods processes a specialization's methods.
// Phase 4: method-local generic methods are *preserved* as templates
// for `rewriteGenericMethodCall`; they are dropped from the final
// output by `dropPreservedGenericMethods` once all call sites have
// been rewritten. Non-generic methods are rewritten + scanned in
// place as before.
func (s *monoState) keepNonGenericMethods(owner string, methods []*FnDecl) []*FnDecl {
	if len(methods) == 0 {
		return methods
	}
	for _, m := range methods {
		if m == nil {
			continue
		}
		if len(m.Generics) > 0 {
			// Preserve — a concrete specialization may need this
			// method body as a template during method-queue drain.
			// Scanning the body now would walk TypeVar-carrying
			// expressions and trip `containsTypeVar`.
			continue
		}
		s.rewriteFnSignature(m)
		s.scanFnBody(m)
	}
	return methods
}

// mergeEnv builds a SubstEnv that stacks the method-local bindings on
// top of the receiver-level bindings. Method-local names override
// when they collide (user-visible shadowing).
func mergeEnv(receiver, method SubstEnv) SubstEnv {
	if len(receiver) == 0 && len(method) == 0 {
		return SubstEnv{}
	}
	out := make(SubstEnv, len(receiver)+len(method))
	for k, v := range receiver {
		out[k] = v
	}
	for k, v := range method {
		out[k] = v
	}
	return out
}

// extractOwnerNominal recovers the source-level nominal owner for a
// method receiver type. OptionalType is the surface form of Option<T>,
// so its owner is `Option` rather than the wrapped inner type. Returns
// "" for types that don't resolve to a nominal owner (function
// pointers, tuples, etc.) — callers should treat that as "not a
// method-on-nominal" and bail.
func extractOwnerNominal(t Type) string {
	switch x := t.(type) {
	case *NamedType:
		return x.Name
	case *OptionalType:
		return "Option"
	default:
		return ""
	}
}

var builtinNonGenericMethods = map[string]map[string]bool{
	"List": {
		"len":     true,
		"isEmpty": true,
		"get":     true,
		"push":   true,
		"pop":    true,
		"insert": true,
		"sorted": true,
		"toSet":  true,
		"clear":  true,
	},
	"Map": {
		"len":         true,
		"isEmpty":     true,
		"get":         true,
		"getOr":       true,
		"containsKey": true,
		"keys":        true,
		"values":      true,
		"entries":     true,
		"forEach":     true,
		"any":         true,
		"all":         true,
		"count":       true,
		"find":        true,
		"filter":      true,
		"merge":       true,
		"mergeWith":   true,
		"insert":      true,
		"remove":      true,
		"clear":       true,
		"update":      true,
		"insertAll":   true,
		"retainIf":    true,
	},
	"Set": {
		"len":        true,
		"isEmpty":    true,
		"contains":   true,
		"union":      true,
		"intersect":  true,
		"difference": true,
		"insert":     true,
		"remove":     true,
		"toList":     true,
		"clear":      true,
	},
	"Option": {
		"isSome": true,
		"isNone": true,
	},
	"Result": {
		"isOk":       true,
		"isErr":      true,
		"contains":   true,
		"containsErr": true,
		"unwrap":     true,
		"expect":     true,
		"unwrapErr":  true,
		"expectErr":  true,
		"unwrapOr":   true,
		"ok":         true,
		"err":        true,
		"and":        true,
		"or":         true,
		"inspect":    true,
		"inspectErr": true,
		"toString":   true,
	},
}

var builtinGenericParamNames = map[string][]string{
	"List":   []string{"T"},
	"Set":    []string{"T"},
	"Option": []string{"T"},
	"Map":    []string{"K", "V"},
	"Result": []string{"T", "E"},
}

// builtinReceiverOwnerAndArity reports the source-level builtin owner
// name plus its generic arity for a receiver type. Unlike
// extractOwnerNominal, this understands already-monomorphized `_ZTS…`
// receiver names by bouncing through originalStructOf/originalEnumOf.
func (s *monoState) builtinReceiverOwnerAndArity(receiver Type) (string, int, bool) {
	switch x := receiver.(type) {
	case *OptionalType:
		return "Option", 1, true
	case *NamedType:
		switch x.Name {
		case "List", "Set", "Option":
			return x.Name, 1, true
		case "Map", "Result":
			return x.Name, 2, true
		}
		if orig, has := s.originalStructOf[x.Name]; has && orig != nil && isStdlibBuiltinName(orig.Name) {
			return orig.Name, len(orig.Generics), true
		}
		if orig, has := s.originalEnumOf[x.Name]; has && orig != nil && isStdlibBuiltinName(orig.Name) {
			return orig.Name, len(orig.Generics), true
		}
	}
	return "", 0, false
}

func cloneSubstEnv(in SubstEnv) SubstEnv {
	if len(in) == 0 {
		return nil
	}
	out := make(SubstEnv, len(in))
	for k, v := range in {
		out[k] = CloneType(v)
	}
	return out
}

func (s *monoState) builtinReceiverSubstEnv(receiver Type) (SubstEnv, bool) {
	switch x := receiver.(type) {
	case *OptionalType:
		return SubstEnv{"T": CloneType(x.Inner)}, true
	case *NamedType:
		if params := builtinGenericParamNames[x.Name]; len(params) > 0 && len(x.Args) == len(params) {
			env := make(SubstEnv, len(params))
			for i, name := range params {
				env[name] = CloneType(x.Args[i])
			}
			return env, true
		}
		if orig, has := s.originalStructOf[x.Name]; has && orig != nil && isStdlibBuiltinName(orig.Name) {
			if env := cloneSubstEnv(s.receiverEnvOf[x.Name]); len(env) > 0 {
				return env, true
			}
		}
		if orig, has := s.originalEnumOf[x.Name]; has && orig != nil && isStdlibBuiltinName(orig.Name) {
			if env := cloneSubstEnv(s.receiverEnvOf[x.Name]); len(env) > 0 {
				return env, true
			}
		}
	}
	return nil, false
}

func (s *monoState) builtinMethodCarriesReceiverTypeArgsOnly(receiver Type, method string, got int) bool {
	if got == 0 {
		return false
	}
	owner, arity, ok := s.builtinReceiverOwnerAndArity(receiver)
	if !ok {
		return false
	}
	methods := builtinNonGenericMethods[owner]
	if !methods[method] {
		return false
	}
	return arity == got
}

func (s *monoState) rewriteBuiltinReceiverMethodCall(c *MethodCall) {
	if c == nil || c.Receiver == nil {
		return
	}
	owner, _, ok := s.builtinReceiverOwnerAndArity(c.Receiver.Type())
	if !ok {
		return
	}
	if !builtinNonGenericMethods[owner][c.Name] {
		return
	}
	if env, ok := s.builtinReceiverSubstEnv(c.Receiver.Type()); ok {
		SubstituteTypes(c, env)
	}
	if s.builtinMethodCarriesReceiverTypeArgsOnly(c.Receiver.Type(), c.Name, len(c.TypeArgs)) {
		c.TypeArgs = nil
	}
}

// resolveOwner turns an owner nominal (either the mangled `_ZTSN…E`
// symbol of a requested/emitted specialization, or a bare source name
// like `Box` for a non-generic owner) into:
//   - ownerKind: 0 = struct, 1 = enum
//   - ownerMangled: canonical owner identifier (same as nominal)
//   - origStruct / origEnum: the original declaration whose method
//     should be cloned as the specialization template
//   - receiverEnv: the substitution env that produced the owner
//     specialization (nil for non-generic owners)
//
// Returns ok=false when the nominal doesn't match any known owner.
// Importantly this succeeds for mangled nominals even before the
// owner specialization has been emitted, because requestStructType /
// requestEnumType populate originalStructOf / originalEnumOf as soon
// as the request is queued.
func (s *monoState) resolveOwner(nominal string) (
	ownerKind int,
	ownerMangled string,
	origStruct *StructDecl,
	origEnum *EnumDecl,
	receiverEnv SubstEnv,
	ok bool,
) {
	if nominal == "" {
		return
	}
	if orig, has := s.originalStructOf[nominal]; has {
		return 0, nominal, orig, nil, s.receiverEnvOf[nominal], true
	}
	if orig, has := s.originalEnumOf[nominal]; has {
		return 1, nominal, nil, orig, s.receiverEnvOf[nominal], true
	}
	if sd, has := s.structsByName[nominal]; has {
		return 0, nominal, sd, nil, nil, true
	}
	if ed, has := s.enumsByName[nominal]; has {
		return 1, nominal, nil, ed, nil, true
	}
	return
}

// findMethod returns the first method with the given source name, or
// nil. Used by `rewriteGenericMethodCall` to recover the original
// generic method template for a call site.
func findMethod(methods []*FnDecl, name string) *FnDecl {
	for _, m := range methods {
		if m != nil && m.Name == name {
			return m
		}
	}
	return nil
}

// rewriteGenericMethodCall handles the Phase 4 method-call path. When
// a MethodCall carries method-local type args, we resolve the owner,
// find the generic method template, and enqueue a specialization. The
// call site is then rewritten to point at the mangled method-local
// symbol so the LLVM backend sees a non-generic method.
//
// This must be called AFTER the receiver has been scanned so
// receiver.Type() reflects any type-level rewrite (generic owner →
// mangled nominal).
func (s *monoState) rewriteGenericMethodCall(c *MethodCall) {
	if c == nil || len(c.TypeArgs) == 0 || c.Receiver == nil {
		return
	}
	builtinReceiverOnly := s.builtinMethodCarriesReceiverTypeArgsOnly(c.Receiver.Type(), c.Name, len(c.TypeArgs))
	nominal := extractOwnerNominal(c.Receiver.Type())
	if nominal == "" {
		return
	}
	kind, ownerMangled, origStruct, origEnum, receiverEnv, ok := s.resolveOwner(nominal)
	if !ok {
		if builtinReceiverOnly {
			c.TypeArgs = nil
		}
		return
	}
	var origMethods []*FnDecl
	switch {
	case origStruct != nil:
		origMethods = origStruct.Methods
	case origEnum != nil:
		origMethods = origEnum.Methods
	}
	origMethod := findMethod(origMethods, c.Name)
	if origMethod == nil {
		if builtinReceiverOnly {
			c.TypeArgs = nil
		}
		return
	}
	if len(origMethod.Generics) == 0 {
		// Checker-instantiation metadata on a method call can carry the
		// owner's concrete receiver args even when the method itself has
		// no method-local generics (e.g. `Map<String, Int>.containsKey`,
		// `Option<String>.unwrap()` inside a specialized body). Once the
		// receiver type has been rewritten to the concrete owner
		// specialization, those TypeArgs are semantically redundant and
		// must be cleared so the IR→AST bridge doesn't wrap the call
		// site in a stray TurbofishExpr that no llvmgen dispatcher
		// recognises.
		c.TypeArgs = nil
		return
	}
	// The checker's Instantiations table records the full call-site
	// substitution. For generic owners (e.g. `List<T>.fold<A>` called
	// from inside a `List<String>` body) the recorded args include the
	// owner-level prefix (`[String, A]`) while the method decl carries
	// only method-local generics (`[A]`). The receiver-level env is
	// already applied via receiverEnvOf when the clone is emitted, so
	// the trailing `len(method.Generics)` entries are what this spec
	// actually needs. Only trim when the shape matches
	// `owner.Generics + method.Generics` — a raw mismatch (pure user
	// turbofish error) still fails the arity check below.
	methodTypeArgs := c.TypeArgs
	ownerGenericCount := 0
	switch {
	case origStruct != nil:
		ownerGenericCount = len(origStruct.Generics)
	case origEnum != nil:
		ownerGenericCount = len(origEnum.Generics)
	}
	if ownerGenericCount > 0 && len(methodTypeArgs) == ownerGenericCount+len(origMethod.Generics) {
		methodTypeArgs = methodTypeArgs[ownerGenericCount:]
	}
	if !MonomorphShouldInstantiate(len(methodTypeArgs), len(origMethod.Generics)) {
		s.addErr("monomorph: arity mismatch for method %s.%s: %d type args vs %d generics",
			ownerMangled, origMethod.Name, len(c.TypeArgs), len(origMethod.Generics))
		return
	}
	for i, ta := range methodTypeArgs {
		if containsTypeVar(ta) {
			s.addErr("monomorph: type arg %d of method %s.%s still contains a type variable (%s)",
				i, ownerMangled, origMethod.Name, typeString(ta))
			return
		}
	}
	typeArgCodes := make([]string, len(methodTypeArgs))
	for i, ta := range methodTypeArgs {
		typeArgCodes[i] = typeCodeOf(ta, s.pkg)
	}
	key := MonomorphMethodDedupeKey(ownerMangled, origMethod.Name, typeArgCodes)
	mangledMethod, seen := s.methodSeen[key]
	if !seen {
		req := NewMonomorphMethodRequest(ownerMangled, origMethod.Name, typeArgCodes)
		mangledMethod = MonomorphMangleMethod(req).Symbol()
		s.methodSeen[key] = mangledMethod
		s.methodQueue = append(s.methodQueue, monoMethodInstance{
			ownerKind:         kind,
			ownerMangled:      ownerMangled,
			origMethod:        origMethod,
			mangledMethodName: mangledMethod,
			methodEnv:         buildSubstEnv(origMethod.Generics, methodTypeArgs),
		})
	}
	_ = receiverEnv // receiverEnv is resolved at emit time via receiverEnvOf
	c.Name = mangledMethod
	c.TypeArgs = nil
}

// emitMethodSpecialization materializes one queued method instance.
// The original generic method is cloned, type-substituted through the
// merged receiver+method env, rewritten, and appended onto the owner
// specialization's Methods list so the LLVM backend dispatches to it
// just like any other concrete method. The owner clone is looked up
// here (rather than at request time) so methods queued before their
// owner specialization has emitted still land on the correct decl.
func (s *monoState) emitMethodSpecialization(rec monoMethodInstance) {
	if rec.origMethod == nil {
		return
	}
	clone := cloneFnDecl(rec.origMethod)
	clone.Name = rec.mangledMethodName
	clone.Generics = nil
	// Resolve the owner-level env *now* so a method call that was
	// queued before the owner specialization existed picks up the
	// receiver substitution that the owner emitter has since recorded.
	fullEnv := mergeEnv(s.receiverEnvOf[rec.ownerMangled], rec.methodEnv)
	SubstituteTypes(clone, fullEnv)
	s.rewriteFnSignature(clone)
	s.scanFnBody(clone)
	switch rec.ownerKind {
	case 0:
		if sd := s.structsByMangled[rec.ownerMangled]; sd != nil {
			sd.Methods = append(sd.Methods, clone)
			return
		}
		if sd := s.structsByName[rec.ownerMangled]; sd != nil {
			sd.Methods = append(sd.Methods, clone)
		}
	case 1:
		if ed := s.enumsByMangled[rec.ownerMangled]; ed != nil {
			ed.Methods = append(ed.Methods, clone)
			return
		}
		if ed := s.enumsByName[rec.ownerMangled]; ed != nil {
			ed.Methods = append(ed.Methods, clone)
		}
	}
}

// dropPreservedGenericMethods removes any method-local generic methods
// that survived until after the worklist drained. These were kept on
// the owner's Methods list as templates for rewriteGenericMethodCall;
// they must not leak into the final IR because the LLVM backend
// rejects methods carrying generic parameters.
func (s *monoState) dropPreservedGenericMethods() {
	for _, d := range s.out.Decls {
		switch x := d.(type) {
		case *StructDecl:
			x.Methods = filterOutGenericMethods(x.Methods)
		case *EnumDecl:
			x.Methods = filterOutGenericMethods(x.Methods)
		case *InterfaceDecl:
			x.Methods = filterOutGenericMethods(x.Methods)
		}
	}
}

func filterOutGenericMethods(methods []*FnDecl) []*FnDecl {
	out := methods[:0]
	for _, m := range methods {
		if m == nil {
			continue
		}
		if len(m.Generics) > 0 {
			continue
		}
		out = append(out, m)
	}
	return out
}

// emitSpecialization materializes one queued free-fn instance: clones
// the original body, substitutes type parameters, rewrites nested
// generic references in the post-substitution signature (so a return
// type like `Pair<T, T>` mangled into `Pair<Int, Int>` further lowers
// to the `_ZTS…` nominal), scans the body, and appends the result.
func (s *monoState) emitSpecialization(rec monoInstance) {
	clone := cloneFnDecl(rec.fn)
	clone.Name = rec.mangled
	clone.Generics = nil
	env := buildSubstEnv(rec.fn.Generics, rec.typeArgs)
	SubstituteTypes(clone, env)
	s.rewriteFnSignature(clone)
	s.scanFnBody(clone)
	s.out.Decls = append(s.out.Decls, clone)
}

// scanDecl walks a top-level declaration looking for generic call sites
// and generic type references that need rewriting in-place. Only
// non-generic bodies are scanned here — generic bodies are handled by
// emitSpecialization / emitStructSpecialization / emitEnumSpecialization
// after substitution. Declaration-level Type fields (function signatures,
// struct fields, enum payloads) are not visited by scanExpr, so we
// rewrite them explicitly here.
func (s *monoState) scanDecl(d Decl) {
	switch d := d.(type) {
	case *FnDecl:
		s.rewriteFnSignature(d)
		s.scanFnBody(d)
	case *StructDecl:
		for _, f := range d.Fields {
			if f != nil {
				f.Type = s.rewriteType(f.Type)
			}
		}
		for _, m := range d.Methods {
			// Phase 4: method-local generic methods stay preserved as
			// templates. Scanning their body would walk TypeVars.
			if m == nil || len(m.Generics) > 0 {
				continue
			}
			s.rewriteFnSignature(m)
			s.scanFnBody(m)
		}
	case *EnumDecl:
		for _, v := range d.Variants {
			if v == nil {
				continue
			}
			for i, p := range v.Payload {
				v.Payload[i] = s.rewriteType(p)
			}
		}
		for _, m := range d.Methods {
			if m == nil || len(m.Generics) > 0 {
				continue
			}
			s.rewriteFnSignature(m)
			s.scanFnBody(m)
		}
	case *LetDecl:
		d.Type = s.rewriteType(d.Type)
		if d.Value != nil {
			s.seedVariantTypeFromContext(d.Type, d.Value)
			s.scanExpr(d.Value)
			if isUnresolvedType(d.Type) || containsTypeVar(d.Type) {
				d.Type = cloneResolvedType(d.Value.Type())
			}
		}
	}
}

// rewriteFnSignature rewrites the param and return type slots of a
// function declaration. Used by scanDecl and by the struct/enum
// specialization emitters to keep method signatures concrete once their
// owner has been substituted.
func (s *monoState) rewriteFnSignature(fn *FnDecl) {
	if fn == nil {
		return
	}
	for _, p := range fn.Params {
		if p == nil {
			continue
		}
		p.Type = s.rewriteType(p.Type)
	}
	fn.Return = s.rewriteType(fn.Return)
}

// scanFnBody walks a function body in place rewriting every generic
// call site to point at its mangled specialization. Non-generic calls
// are left untouched. No-op for decls without a body (interface stubs).
func (s *monoState) scanFnBody(fn *FnDecl) {
	if fn == nil || fn.Body == nil {
		return
	}
	s.pushLocalTypeScope()
	for _, p := range fn.Params {
		if p == nil || p.Name == "" || isUnresolvedType(p.Type) {
			continue
		}
		s.bindLocalType(p.Name, p.Type)
	}
	s.scanBlock(fn.Body)
	s.popLocalTypeScope()
}

func (s *monoState) scanBlock(b *Block) {
	if b == nil {
		return
	}
	s.pushLocalTypeScope()
	defer s.popLocalTypeScope()
	for _, st := range b.Stmts {
		s.scanStmt(st)
	}
	if b.Result != nil {
		s.scanExpr(b.Result)
	}
}

func (s *monoState) scanStmt(st Stmt) {
	switch st := st.(type) {
	case *Block:
		s.scanBlock(st)
	case *LetStmt:
		st.Type = s.rewriteType(st.Type)
		if st.Value != nil {
			s.seedVariantTypeFromContext(st.Type, st.Value)
			s.scanExpr(st.Value)
			if isUnresolvedType(st.Type) || containsTypeVar(st.Type) {
				st.Type = cloneResolvedType(st.Value.Type())
			}
		}
		if st.Name != "" && !isUnresolvedType(st.Type) {
			s.bindLocalType(st.Name, st.Type)
		}
	case *ExprStmt:
		s.scanExpr(st.X)
	case *AssignStmt:
		for _, t := range st.Targets {
			s.scanExpr(t)
		}
		s.scanExpr(st.Value)
	case *ReturnStmt:
		if st.Value != nil {
			s.scanExpr(st.Value)
		}
	case *IfStmt:
		s.scanExpr(st.Cond)
		s.scanBlock(st.Then)
		s.scanBlock(st.Else)
	case *ForStmt:
		if st.Cond != nil {
			s.scanExpr(st.Cond)
		}
		if st.Iter != nil {
			s.scanExpr(st.Iter)
		}
		if st.Start != nil {
			s.scanExpr(st.Start)
		}
		if st.End != nil {
			s.scanExpr(st.End)
		}
		s.scanBlock(st.Body)
	case *DeferStmt:
		s.scanBlock(st.Body)
	case *ChanSendStmt:
		s.scanExpr(st.Channel)
		s.scanExpr(st.Value)
	case *MatchStmt:
		s.scanExpr(st.Scrutinee)
		for _, a := range st.Arms {
			if a == nil {
				continue
			}
			s.rewritePatternFromScrutinee(a.Pattern, st.Scrutinee.Type())
			if a.Guard != nil {
				s.scanExpr(a.Guard)
			}
			s.scanBlock(a.Body)
		}
	}
}

func (s *monoState) seedVariantTypeFromContext(expect Type, expr Expr) {
	if expect == nil || expr == nil {
		return
	}
	if field, base, ok := s.enumVariantFieldExpr(expr); ok {
		named, ok := expect.(*NamedType)
		if !ok || named == nil || named.Name == "" {
			return
		}
		field.T = expect
		if base != nil {
			base.T = expect
		}
		return
	}
	lit, ok := expr.(*VariantLit)
	if !ok || lit == nil {
		return
	}
	if lit.T != nil {
		if _, unresolved := lit.T.(*ErrType); !unresolved {
			return
		}
	}
	named, ok := expect.(*NamedType)
	if !ok || named == nil || named.Name == "" {
		return
	}
	if lit.Enum == "" {
		return
	}
	if _, generic := s.genericEnumsByName[lit.Enum]; !generic && lit.Enum != named.Name {
		return
	}
	lit.T = expect
}

func (s *monoState) enumVariantFieldExpr(expr Expr) (*FieldExpr, *Ident, bool) {
	field, ok := expr.(*FieldExpr)
	if !ok || field == nil || field.Optional {
		return nil, nil, false
	}
	base, ok := field.X.(*Ident)
	if !ok || base == nil || base.Kind != IdentTypeName {
		return nil, nil, false
	}
	decl := s.genericEnumsByName[base.Name]
	if decl == nil {
		return nil, nil, false
	}
	for _, variant := range decl.Variants {
		if variant != nil && variant.Name == field.Name && len(variant.Payload) == 0 {
			return field, base, true
		}
	}
	return nil, nil, false
}

func (s *monoState) pushLocalTypeScope() {
	s.localTypeScopes = append(s.localTypeScopes, map[string]Type{})
}

func (s *monoState) popLocalTypeScope() {
	if len(s.localTypeScopes) == 0 {
		return
	}
	s.localTypeScopes = s.localTypeScopes[:len(s.localTypeScopes)-1]
}

func (s *monoState) bindLocalType(name string, t Type) {
	if name == "" || isUnresolvedType(t) || len(s.localTypeScopes) == 0 {
		return
	}
	s.localTypeScopes[len(s.localTypeScopes)-1][name] = CloneType(t)
}

func (s *monoState) lookupLocalType(name string) (Type, bool) {
	for i := len(s.localTypeScopes) - 1; i >= 0; i-- {
		if t, ok := s.localTypeScopes[i][name]; ok && !isUnresolvedType(t) {
			return t, true
		}
	}
	return nil, false
}

func (s *monoState) inferVariantLiteralType(lit *VariantLit) Type {
	if lit == nil || lit.Enum == "" {
		return nil
	}
	decl := s.genericEnumsByName[lit.Enum]
	if decl == nil || len(decl.Generics) == 0 {
		return nil
	}
	var variant *Variant
	for _, candidate := range decl.Variants {
		if candidate != nil && candidate.Name == lit.Variant {
			variant = candidate
			break
		}
	}
	if variant == nil || len(variant.Payload) != len(lit.Args) {
		return nil
	}
	env := SubstEnv{}
	for i, payload := range variant.Payload {
		arg := lit.Args[i].Value
		argType := s.resolvedExprType(arg)
		if isUnresolvedType(argType) {
			return nil
		}
		if !bindInferredTypeArgs(env, payload, argType) {
			return nil
		}
	}
	args := make([]Type, 0, len(decl.Generics))
	for _, param := range decl.Generics {
		if param == nil {
			return nil
		}
		arg, ok := env[param.Name]
		if !ok || isUnresolvedType(arg) || containsTypeVar(arg) {
			return nil
		}
		args = append(args, CloneType(arg))
	}
	return &NamedType{Name: decl.Name, Args: args}
}

func bindInferredTypeArgs(env SubstEnv, pattern Type, actual Type) bool {
	if pattern == nil || actual == nil || isUnresolvedType(actual) {
		return false
	}
	switch p := pattern.(type) {
	case *TypeVar:
		if p.Name == "" {
			return false
		}
		if bound, ok := env[p.Name]; ok {
			return typesEqual(bound, actual)
		}
		env[p.Name] = CloneType(actual)
		return true
	case *PrimType:
		a, ok := actual.(*PrimType)
		return ok && p.Kind == a.Kind
	case *NamedType:
		a, ok := actual.(*NamedType)
		if !ok || p.Name != a.Name || p.Package != a.Package || p.Builtin != a.Builtin || len(p.Args) != len(a.Args) {
			return false
		}
		for i := range p.Args {
			if !bindInferredTypeArgs(env, p.Args[i], a.Args[i]) {
				return false
			}
		}
		return true
	case *OptionalType:
		a, ok := actual.(*OptionalType)
		return ok && bindInferredTypeArgs(env, p.Inner, a.Inner)
	case *TupleType:
		a, ok := actual.(*TupleType)
		if !ok || len(p.Elems) != len(a.Elems) {
			return false
		}
		for i := range p.Elems {
			if !bindInferredTypeArgs(env, p.Elems[i], a.Elems[i]) {
				return false
			}
		}
		return true
	case *FnType:
		a, ok := actual.(*FnType)
		if !ok || len(p.Params) != len(a.Params) {
			return false
		}
		for i := range p.Params {
			if !bindInferredTypeArgs(env, p.Params[i], a.Params[i]) {
				return false
			}
		}
		return bindInferredTypeArgs(env, p.Return, a.Return)
	default:
		return false
	}
}

func typesEqual(left Type, right Type) bool {
	switch l := left.(type) {
	case nil:
		return right == nil
	case *PrimType:
		r, ok := right.(*PrimType)
		return ok && l.Kind == r.Kind
	case *NamedType:
		r, ok := right.(*NamedType)
		if !ok || l.Name != r.Name || l.Package != r.Package || l.Builtin != r.Builtin || len(l.Args) != len(r.Args) {
			return false
		}
		for i := range l.Args {
			if !typesEqual(l.Args[i], r.Args[i]) {
				return false
			}
		}
		return true
	case *OptionalType:
		r, ok := right.(*OptionalType)
		return ok && typesEqual(l.Inner, r.Inner)
	case *TupleType:
		r, ok := right.(*TupleType)
		if !ok || len(l.Elems) != len(r.Elems) {
			return false
		}
		for i := range l.Elems {
			if !typesEqual(l.Elems[i], r.Elems[i]) {
				return false
			}
		}
		return true
	case *FnType:
		r, ok := right.(*FnType)
		if !ok || len(l.Params) != len(r.Params) {
			return false
		}
		for i := range l.Params {
			if !typesEqual(l.Params[i], r.Params[i]) {
				return false
			}
		}
		return typesEqual(l.Return, r.Return)
	case *TypeVar:
		r, ok := right.(*TypeVar)
		return ok && l.Name == r.Name && l.Owner == r.Owner
	case *ErrType:
		_, ok := right.(*ErrType)
		return ok
	default:
		return false
	}
}

func isUnresolvedType(t Type) bool {
	if t == nil {
		return true
	}
	_, ok := t.(*ErrType)
	return ok
}

func cloneResolvedType(t Type) Type {
	if isUnresolvedType(t) {
		return t
	}
	return CloneType(t)
}

func (s *monoState) resolvedExprType(e Expr) Type {
	if e == nil {
		return nil
	}
	if t := e.Type(); !isUnresolvedType(t) {
		return t
	}
	switch x := e.(type) {
	case *IntLit:
		return TInt
	case *FloatLit:
		return TFloat
	case *Ident:
		if inferred, ok := s.lookupLocalType(x.Name); ok {
			return inferred
		}
	case *VariantLit:
		if inferred := s.inferVariantLiteralType(x); inferred != nil {
			return inferred
		}
	}
	return e.Type()
}

func (s *monoState) rewritePatternFromScrutinee(pattern Pattern, scrutinee Type) {
	if pattern == nil {
		return
	}
	switch p := pattern.(type) {
	case *BindingPat:
		s.rewritePatternFromScrutinee(p.Pattern, scrutinee)
	case *OrPat:
		for _, alt := range p.Alts {
			s.rewritePatternFromScrutinee(alt, scrutinee)
		}
	case *StructPat:
		if named, ok := scrutinee.(*NamedType); ok && named != nil && named.Name != "" {
			if _, generic := s.genericStructsByName[p.TypeName]; generic || p.TypeName == named.Name {
				p.TypeName = named.Name
			}
		}
		for _, field := range p.Fields {
			s.rewritePatternFromScrutinee(field.Pattern, nil)
		}
	case *VariantPat:
		if named, ok := scrutinee.(*NamedType); ok && named != nil && named.Name != "" && p.Enum != "" {
			if _, generic := s.genericEnumsByName[p.Enum]; generic || p.Enum == named.Name {
				p.Enum = named.Name
			}
		}
		for _, arg := range p.Args {
			s.rewritePatternFromScrutinee(arg, nil)
		}
	case *TuplePat:
		for _, elem := range p.Elems {
			s.rewritePatternFromScrutinee(elem, nil)
		}
	}
}

func (s *monoState) scanExpr(e Expr) {
	if e == nil {
		return
	}
	// Phase 2: rewrite every generic NamedType reference buried in the
	// expression's own type fields (e.g. StructLit.T, CallExpr.T,
	// Ident.T). Safe to run before the case dispatch — idempotent on
	// already-mangled references.
	s.rewriteExprType(e)
	switch e := e.(type) {
	case *CallExpr:
		// Rewrite generic call to mangled specialization.
		if len(e.TypeArgs) > 0 {
			s.rewriteGenericCall(e)
		}
		s.scanExpr(e.Callee)
		for i := range e.Args {
			s.scanExpr(e.Args[i].Value)
		}
	case *MethodCall:
		// Phase 4: rewrite the call site to a mangled method
		// specialization when method-local type args are present.
		// Walk receiver/args FIRST so their types end up in mangled
		// form before we peek at receiver.Type() to resolve the owner.
		s.scanExpr(e.Receiver)
		for i := range e.Args {
			s.scanExpr(e.Args[i].Value)
		}
		s.rewriteBuiltinReceiverMethodCall(e)
		if len(e.TypeArgs) > 0 {
			s.rewriteGenericMethodCall(e)
		}
	case *IntrinsicCall:
		for i := range e.Args {
			s.scanExpr(e.Args[i].Value)
		}
	case *UnaryExpr:
		s.scanExpr(e.X)
	case *BinaryExpr:
		s.scanExpr(e.Left)
		s.scanExpr(e.Right)
	case *ListLit:
		for _, el := range e.Elems {
			s.scanExpr(el)
		}
	case *MapLit:
		for i := range e.Entries {
			s.scanExpr(e.Entries[i].Key)
			s.scanExpr(e.Entries[i].Value)
		}
	case *TupleLit:
		for _, el := range e.Elems {
			s.scanExpr(el)
		}
	case *StructLit:
		for i := range e.Fields {
			if e.Fields[i].Value != nil {
				s.scanExpr(e.Fields[i].Value)
			}
		}
		if e.Spread != nil {
			s.scanExpr(e.Spread)
		}
	case *VariantLit:
		for i := range e.Args {
			s.scanExpr(e.Args[i].Value)
		}
	case *BlockExpr:
		s.scanBlock(e.Block)
	case *IfExpr:
		s.scanExpr(e.Cond)
		s.scanBlock(e.Then)
		s.scanBlock(e.Else)
	case *IfLetExpr:
		s.scanExpr(e.Scrutinee)
		s.rewritePatternFromScrutinee(e.Pattern, e.Scrutinee.Type())
		s.scanBlock(e.Then)
		s.scanBlock(e.Else)
	case *MatchExpr:
		s.scanExpr(e.Scrutinee)
		for _, a := range e.Arms {
			if a == nil {
				continue
			}
			s.rewritePatternFromScrutinee(a.Pattern, e.Scrutinee.Type())
			if a.Guard != nil {
				s.scanExpr(a.Guard)
			}
			s.scanBlock(a.Body)
		}
	case *FieldExpr:
		s.scanExpr(e.X)
	case *IndexExpr:
		s.scanExpr(e.X)
		s.scanExpr(e.Index)
	case *TupleAccess:
		s.scanExpr(e.X)
	case *RangeLit:
		if e.Start != nil {
			s.scanExpr(e.Start)
		}
		if e.End != nil {
			s.scanExpr(e.End)
		}
	case *QuestionExpr:
		s.scanExpr(e.X)
	case *CoalesceExpr:
		s.scanExpr(e.Left)
		s.scanExpr(e.Right)
	case *Closure:
		s.scanBlock(e.Body)
	case *StringLit:
		for _, p := range e.Parts {
			if !p.IsLit && p.Expr != nil {
				s.scanExpr(p.Expr)
			}
		}
	}
}

// rewriteGenericCall translates a generic free-fn call site into its
// mangled specialization. Only calls whose callee resolves to a
// top-level generic FnDecl are rewritten; others are left alone (the
// checker should have rejected anything else, but we stay defensive).
func (s *monoState) rewriteGenericCall(c *CallExpr) {
	id, ok := c.Callee.(*Ident)
	if !ok {
		return
	}
	orig, ok := s.genericsByName[id.Name]
	if !ok {
		return
	}
	mangled := s.request(orig, c.TypeArgs)
	if mangled == "" {
		return
	}
	id.Name = mangled
	id.TypeArgs = nil
	c.TypeArgs = nil
}

// ==== Helpers ====

// buildSubstEnv pairs up a generic parameter list with its concrete
// type arguments. Out-of-range indices are skipped so malformed input
// degrades gracefully.
func buildSubstEnv(params []*TypeParam, args []Type) SubstEnv {
	env := make(SubstEnv, len(params))
	for i, p := range params {
		if p == nil || i >= len(args) {
			break
		}
		env[p.Name] = args[i]
	}
	return env
}

// cloneAndSubstType returns a substituted copy of t without mutating
// the original.
func cloneAndSubstType(t Type, env SubstEnv) Type {
	cloned := CloneType(t)
	return substType(cloned, env)
}

// containsTypeVar reports whether a Type still contains a TypeVar
// anywhere in its structure.
func containsTypeVar(t Type) bool {
	switch t := t.(type) {
	case *TypeVar:
		return true
	case *NamedType:
		for _, a := range t.Args {
			if containsTypeVar(a) {
				return true
			}
		}
		return false
	case *OptionalType:
		return containsTypeVar(t.Inner)
	case *TupleType:
		for _, e := range t.Elems {
			if containsTypeVar(e) {
				return true
			}
		}
		return false
	case *FnType:
		for _, p := range t.Params {
			if containsTypeVar(p) {
				return true
			}
		}
		return containsTypeVar(t.Return)
	}
	return false
}

// typeCodeOf produces an Itanium-compatible encoding for an IR Type,
// routing through the Osty-authored snapshot for primitives and
// builtin aggregates. Unknown / poisoned types return "?" so callers
// can detect and bail.
func typeCodeOf(t Type, pkg string) string {
	if t == nil {
		return "?"
	}
	switch t := t.(type) {
	case *PrimType:
		if code := MonomorphPrimCode(t.String()); code != "" {
			return code
		}
		return "?"
	case *NamedType:
		// Bare primitive reference (the checker occasionally lowers
		// `Int` as a NamedType). Defer to the primitive table.
		if t.Package == "" && len(t.Args) == 0 {
			if code := MonomorphPrimCode(t.Name); code != "" {
				return code
			}
		}
		if t.Builtin {
			return builtinTypeCode(t, pkg)
		}
		// Phase 2: a user-declared generic reference carries its concrete
		// type arguments via NamedType.Args. Encode them through the
		// template-nested form so a generic free function whose type arg
		// is itself user-generic (e.g. `id::<Pair<Int,Int>>`) gets a
		// unique mangled fn symbol per concrete Pair instantiation.
		if len(t.Args) > 0 {
			var sb strings.Builder
			for _, a := range t.Args {
				sb.WriteString(typeCodeOf(a, pkg))
			}
			return MonomorphUserTemplateNested(firstNonEmpty(pkg, "main"), t.Name, sb.String())
		}
		return MonomorphUserNested(firstNonEmpty(pkg, "main"), t.Name)
	case *OptionalType:
		inner := typeCodeOf(t.Inner, pkg)
		return MonomorphBuiltinTemplate("Option", inner)
	case *TupleType:
		var sb strings.Builder
		for _, e := range t.Elems {
			sb.WriteString(typeCodeOf(e, pkg))
		}
		return MonomorphBuiltinTemplate("Tuple", sb.String())
	case *FnType:
		// Itanium pointer-to-function form `PF<ret><params>E`. Rare
		// enough in generics that we approximate; the dedup key only
		// needs uniqueness so this is fine for Phase 1.
		var sb strings.Builder
		sb.WriteString("PF")
		if t.Return != nil {
			sb.WriteString(typeCodeOf(t.Return, pkg))
		} else {
			sb.WriteString("v")
		}
		for _, p := range t.Params {
			sb.WriteString(typeCodeOf(p, pkg))
		}
		sb.WriteByte('E')
		return sb.String()
	case *TypeVar:
		return "?"
	case *ErrType:
		return "?"
	}
	return "?"
}

// builtinTypeCode encodes prelude aggregates (List, Map, Set, Option,
// Result, Bytes, String, …). When the type has no template args it
// collapses to the simple nested form.
func builtinTypeCode(n *NamedType, pkg string) string {
	if n == nil {
		return "?"
	}
	if len(n.Args) == 0 {
		return MonomorphBuiltinNested(n.Name)
	}
	var sb strings.Builder
	for _, a := range n.Args {
		sb.WriteString(typeCodeOf(a, pkg))
	}
	return MonomorphBuiltinTemplate(n.Name, sb.String())
}

// firstNonEmpty returns a when non-empty, else b.
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
