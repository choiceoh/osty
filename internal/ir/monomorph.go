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
		pkg:            mod.Package,
		out:            &Module{Package: mod.Package, SpanV: mod.SpanV},
		genericsByName: map[string]*FnDecl{},
		seen:           map[string]string{},
	}

	// Pass 1: index generic free fns so the scanner can resolve
	// callees against the original declarations without revisiting
	// mod.Decls for each call site.
	for _, d := range mod.Decls {
		if fn, ok := d.(*FnDecl); ok && len(fn.Generics) > 0 {
			state.genericsByName[fn.Name] = fn
		}
	}

	// Pass 2: copy non-generic decls into the output module (cloning so
	// rewrites are isolated from the input), scan their bodies for
	// initial instantiation requests, and append the script.
	for _, d := range mod.Decls {
		if fn, ok := d.(*FnDecl); ok && len(fn.Generics) > 0 {
			// generic free fn — drop from output; specializations will
			// be appended at the end.
			continue
		}
		out := cloneDecl(d)
		state.out.Decls = append(state.out.Decls, out)
		state.scanDecl(out)
	}
	for _, s := range mod.Script {
		cloned := cloneStmt(s)
		state.out.Script = append(state.out.Script, cloned)
		state.scanStmt(cloned)
	}

	// Pass 3: drain the worklist. Each iteration clones an original
	// generic fn, substitutes type parameters, scans its body for
	// further instantiations (which append to the queue), and adds the
	// specialization to the output.
	for i := 0; i < len(state.queue); i++ {
		rec := state.queue[i]
		state.emitSpecialization(rec)
	}

	return state.out, state.errs
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
}

// monoInstance is one pending free-fn specialization.
type monoInstance struct {
	fn       *FnDecl
	typeArgs []Type
	mangled  string
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

// emitSpecialization materializes one queued free-fn instance: clones
// the original body, substitutes type parameters, rewrites nested
// generic calls, and appends the result to out.Decls.
func (s *monoState) emitSpecialization(rec monoInstance) {
	clone := cloneFnDecl(rec.fn)
	clone.Name = rec.mangled
	clone.Generics = nil
	env := buildSubstEnv(rec.fn.Generics, rec.typeArgs)
	SubstituteTypes(clone, env)
	s.scanFnBody(clone)
	s.out.Decls = append(s.out.Decls, clone)
}

// scanDecl walks a top-level declaration looking for generic call sites
// that need rewriting in-place. Only non-generic bodies are scanned here
// — generic bodies are handled by emitSpecialization after substitution.
func (s *monoState) scanDecl(d Decl) {
	switch d := d.(type) {
	case *FnDecl:
		s.scanFnBody(d)
	case *StructDecl:
		for _, m := range d.Methods {
			s.scanFnBody(m)
		}
	case *EnumDecl:
		for _, m := range d.Methods {
			s.scanFnBody(m)
		}
	case *LetDecl:
		if d.Value != nil {
			s.scanExpr(d.Value)
		}
	}
}

// scanFnBody walks a function body in place rewriting every generic
// call site to point at its mangled specialization. Non-generic calls
// are left untouched. No-op for decls without a body (interface stubs).
func (s *monoState) scanFnBody(fn *FnDecl) {
	if fn == nil || fn.Body == nil {
		return
	}
	s.scanBlock(fn.Body)
}

func (s *monoState) scanBlock(b *Block) {
	if b == nil {
		return
	}
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
		if st.Value != nil {
			s.scanExpr(st.Value)
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
			if a.Guard != nil {
				s.scanExpr(a.Guard)
			}
			s.scanBlock(a.Body)
		}
	}
}

func (s *monoState) scanExpr(e Expr) {
	if e == nil {
		return
	}
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
		s.scanBlock(e.Then)
		s.scanBlock(e.Else)
	case *MatchExpr:
		s.scanExpr(e.Scrutinee)
		for _, a := range e.Arms {
			if a == nil {
				continue
			}
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
