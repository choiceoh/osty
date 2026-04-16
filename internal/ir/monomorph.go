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
// (LANG_SPEC_v0.4/02-type-system.md §2.7.3).
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
		pkg:                  mod.Package,
		out:                  &Module{Package: mod.Package, SpanV: mod.SpanV},
		genericsByName:       map[string]*FnDecl{},
		seen:                 map[string]string{},
		genericStructsByName: map[string]*StructDecl{},
		genericEnumsByName:   map[string]*EnumDecl{},
		typeSeen:             map[string]string{},
	}

	// Pass 1: index every generic top-level declaration so the scanner
	// can resolve references against the original forms without
	// revisiting mod.Decls for each call/type site. Generic free fns go
	// in genericsByName; generic struct/enum declarations go in
	// genericStructsByName / genericEnumsByName (Phase 2).
	for _, d := range mod.Decls {
		switch x := d.(type) {
		case *FnDecl:
			if len(x.Generics) > 0 {
				state.genericsByName[x.Name] = x
			}
		case *StructDecl:
			if len(x.Generics) > 0 {
				state.genericStructsByName[x.Name] = x
			}
		case *EnumDecl:
			if len(x.Generics) > 0 {
				state.genericEnumsByName[x.Name] = x
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
		state.scanDecl(out)
	}
	state.pushLocalTypeScope()
	for _, s := range mod.Script {
		cloned := cloneStmt(s)
		state.out.Script = append(state.out.Script, cloned)
		state.scanStmt(cloned)
	}
	state.popLocalTypeScope()

	// Pass 3+4 (interleaved): drain both queues until neither grows.
	// Emitting a specialization may discover further generic call sites
	// (grows fn queue) or generic type references (grows type queue),
	// so we loop until both reach a fixed point.
	for len(state.queue) > 0 || len(state.typeQueue) > 0 {
		for i := 0; i < len(state.queue); i++ {
			state.emitSpecialization(state.queue[i])
		}
		state.queue = state.queue[:0]
		for i := 0; i < len(state.typeQueue); i++ {
			state.emitTypeSpecialization(state.typeQueue[i])
		}
		state.typeQueue = state.typeQueue[:0]
	}

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

	// Phase 2: generic nominal-type bookkeeping.
	//
	// genericStructsByName / genericEnumsByName index the original
	// generic declarations so scanning can recognize a generic
	// NamedType reference by source name.
	genericStructsByName map[string]*StructDecl
	genericEnumsByName   map[string]*EnumDecl
	// typeSeen maps a MonomorphTypeDedupeKey to the mangled type-symbol
	// so duplicate requests short-circuit.
	typeSeen map[string]string
	// typeQueue is the worklist of pending struct/enum specializations.
	typeQueue []monoTypeInstance
	// localTypeScopes tracks statement-local bindings while scanning a
	// concrete function or script body so unresolved Idents can recover
	// a type from an earlier let-binding.
	localTypeScopes []map[string]Type
}

// monoInstance is one pending free-fn specialization.
type monoInstance struct {
	fn       *FnDecl
	typeArgs []Type
	mangled  string
}

// monoTypeInstance is one pending nominal-type specialization. Exactly
// one of structDecl / enumDecl is non-nil; the other distinguishes the
// emitter branch. typeArgs are the concrete types matching the
// declaration's generics, and mangled is the `_ZTS…` symbol the engine
// has already assigned and written to typeSeen.
type monoTypeInstance struct {
	structDecl *StructDecl
	enumDecl   *EnumDecl
	typeArgs   []Type
	mangled    string
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
	s.typeQueue = append(s.typeQueue, monoTypeInstance{
		enumDecl: decl,
		typeArgs: append([]Type(nil), typeArgs...),
		mangled:  mangled,
	})
	return mangled
}

// emitTypeSpecialization dispatches one queued nominal-type instance
// to the appropriate struct/enum emitter.
func (s *monoState) emitTypeSpecialization(rec monoTypeInstance) {
	switch {
	case rec.structDecl != nil:
		s.emitStructSpecialization(rec)
	case rec.enumDecl != nil:
		s.emitEnumSpecialization(rec)
	}
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
}

// emitEnumSpecialization is the enum counterpart of emitStructSpecialization.
func (s *monoState) emitEnumSpecialization(rec monoTypeInstance) {
	clone := cloneEnumDecl(rec.enumDecl)
	clone.Name = rec.mangled
	clone.Generics = nil
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
		if x.Builtin {
			return x
		}
		// Only user-declared nominal types drive specialization.
		// Concrete (non-generic) references like `Point` fall through
		// unchanged — they were not indexed in Pass 1.
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
func (s *monoState) keepNonGenericMethods(owner string, methods []*FnDecl) []*FnDecl {
	if len(methods) == 0 {
		return methods
	}
	kept := methods[:0]
	for _, m := range methods {
		if m == nil {
			continue
		}
		if len(m.Generics) > 0 {
			s.addErr("monomorph: method %s.%s has method-local generics; skipped (Phase 3+ scope)",
				owner, m.Name)
			continue
		}
		// Rewrite the post-substitution signature so any user-generic
		// references surviving `SubstituteTypes` (nested or built-in
		// wrapping like `Option<Pair<T, T>>`) become mangled symbols,
		// then scan the body for call/type sites nested inside.
		s.rewriteFnSignature(m)
		s.scanFnBody(m)
		kept = append(kept, m)
	}
	return kept
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
			s.rewriteFnSignature(m)
			s.scanFnBody(m)
		}
	case *LetDecl:
		d.Type = s.rewriteType(d.Type)
		if d.Value != nil {
			s.seedVariantTypeFromContext(d.Type, d.Value)
			s.scanExpr(d.Value)
			if isUnresolvedType(d.Type) {
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
			if isUnresolvedType(st.Type) {
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
		// Generic method monomorphization is out of scope for Phase 1.
		// Non-generic method calls still need their args walked for
		// generic calls nested inside.
		s.scanExpr(e.Receiver)
		for i := range e.Args {
			s.scanExpr(e.Args[i].Value)
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
