package backend

import (
	"sort"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// ReachableStdlibFn pairs a stdlib function AST with the module it came
// from, so a downstream caller can route lowering through the module's
// own resolve.Package without reconstructing the mapping.
type ReachableStdlibFn struct {
	// Module is the stdlib module name ("strings", "collections", ...).
	Module string
	// Fn is the AST declaration as held in the registry. Callers must not
	// mutate it — the registry owns a shared immutable copy.
	Fn *ast.FnDecl
}

// ReachableStdlibFns returns the stdlib `fn` declarations referenced
// directly from mod, in deterministic (module, name) order.
//
// Only first-hop references are collected: a stdlib body that itself
// calls another stdlib function does not transitively pull the callee
// in. Transitive closure is the next step once the lowering pipeline
// can accept injected stdlib decls — today it would just queue more
// work that has nowhere to land.
//
// Non-stdlib `qualifier.name` calls (runtime FFI aliases, user module
// aliases) are silently skipped: they are identified by their absence
// from reg.Modules. A nil module or nil registry returns nil.
func ReachableStdlibFns(mod *ir.Module, reg *stdlib.Registry) []ReachableStdlibFn {
	if mod == nil || reg == nil {
		return nil
	}
	type key struct{ module, name string }
	seen := map[key]struct{}{}
	var found []ReachableStdlibFn
	for ref := range ir.Reach(mod) {
		k := key{module: ref.Qualifier, name: ref.Name}
		if _, dup := seen[k]; dup {
			continue
		}
		fn := reg.LookupFnDecl(ref.Qualifier, ref.Name)
		if fn == nil {
			continue
		}
		seen[k] = struct{}{}
		found = append(found, ReachableStdlibFn{Module: ref.Qualifier, Fn: fn})
	}
	sort.Slice(found, func(i, j int) bool {
		if found[i].Module != found[j].Module {
			return found[i].Module < found[j].Module
		}
		return found[i].Fn.Name < found[j].Fn.Name
	})
	return found
}

// ReachableStdlibMethod pairs a stdlib struct/enum method AST with the
// module and owning type it came from, so a downstream caller can
// route lowering and call-site rewriting.
//
// Companion to ReachableStdlibFn: free-fn refs flow through that
// shape, type-qualified method refs flow through this one.
type ReachableStdlibMethod struct {
	// Module is the stdlib module name that owns the receiver type
	// ("encoding", "option", ...).
	Module string
	// Type is the receiver type's source name ("Hex", "Option", ...).
	Type string
	// Method is the method's source name ("encode", "isSome", ...).
	// Available redundantly via Fn.Name; kept on the struct so callers
	// can group by (Module, Type) without dereferencing Fn.
	Method string
	// Fn is the method declaration as held in the registry. Callers
	// must not mutate it — the registry owns a shared immutable copy.
	Fn *ast.FnDecl
}

// ReachableStdlibMethods returns the stdlib struct/enum methods
// referenced from mod via typed method calls, in deterministic
// (module, type, method) order.
//
// Discovery walks `ir.ReachMethods`, which only emits MethodCall
// references whose receiver has a NamedType with a non-empty Package
// — so user-defined methods are silently filtered. Each candidate is
// then validated against `Registry.LookupMethodDecl`; a missing entry
// is dropped (e.g. a method on a stdlib type that isn't actually
// declared in the module's stub).
//
// Like ReachableStdlibFns, this is first-hop only: methods called by
// an injected method body are not transitively pulled in. Transitive
// closure is the next step once the lowering pipeline can accept
// injected stdlib methods — today this surface is consumed only by
// callers that track reachability for diagnostics or planning.
func ReachableStdlibMethods(mod *ir.Module, reg *stdlib.Registry) []ReachableStdlibMethod {
	if mod == nil || reg == nil {
		return nil
	}
	type key struct{ module, typeName, method string }
	seen := map[key]struct{}{}
	var found []ReachableStdlibMethod
	for ref := range ir.ReachMethods(mod) {
		k := key{module: ref.Module, typeName: ref.Type, method: ref.Method}
		if _, dup := seen[k]; dup {
			continue
		}
		fn := reg.LookupMethodDecl(ref.Module, ref.Type, ref.Method)
		if fn == nil {
			continue
		}
		seen[k] = struct{}{}
		found = append(found, ReachableStdlibMethod{
			Module: ref.Module,
			Type:   ref.Type,
			Method: ref.Method,
			Fn:     fn,
		})
	}
	sort.Slice(found, func(i, j int) bool {
		if found[i].Module != found[j].Module {
			return found[i].Module < found[j].Module
		}
		if found[i].Type != found[j].Type {
			return found[i].Type < found[j].Type
		}
		return found[i].Method < found[j].Method
	})
	return found
}

// injectReachableStdlibBodies lowers every reachable stdlib function in
// mod, renames each lowered fn to its mangled symbol, and rewrites the
// matching call sites in mod in place. The returned []ir.Decl must be
// appended to the user module's Decls.
//
// Each stdlib module carries its own resolve.Package; this function
// constructs a lightweight resolve.Result from the file-level Refs/
// TypeRefs/FileScope so lowerer identifier-kind queries hit the correct
// stdlib scope. Checker information is not currently plumbed through —
// lowering degrades gracefully to ErrTypeVal on typed expressions,
// which the monomorphizer + MIR validator will flag if they reach a
// consumer that cannot handle the gap.
//
// A nil module or nil registry returns (nil, nil). Any lowering issue
// is propagated as a non-fatal error; callers should surface them via
// entry.IRIssues rather than treat them as fatal.
//
// Both free-fn and struct/enum-method bodies are injected; for methods,
// the lowered FnDecl gains an explicit `self` Param of the receiver's
// owning NamedType prepended to its parameter list, and is renamed to
// `StdlibMethodSymbol(...)`. The method-call rewriter
// (`RewriteStdlibMethodCallsites`) does the matching transformation on
// the call sites so each `recv.method(args)` becomes a free-fn call
// `mangled(recv, args...)` that resolves to the injected body.
func injectReachableStdlibBodies(mod *ir.Module, reg *stdlib.Registry) ([]ir.Decl, []error) {
	if mod == nil || reg == nil {
		return nil, nil
	}
	reachedFns := ReachableStdlibFns(mod, reg)
	reachedMethods := ReachableStdlibMethods(mod, reg)
	if len(reachedFns) == 0 && len(reachedMethods) == 0 {
		return nil, nil
	}
	type fnKey struct{ module, name string }
	injectedFn := map[fnKey]bool{}
	for _, r := range reachedFns {
		injectedFn[fnKey{module: r.Module, name: r.Fn.Name}] = true
	}
	type methodKey struct{ module, typeName, method string }
	injectedMethod := map[methodKey]bool{}
	for _, r := range reachedMethods {
		injectedMethod[methodKey{module: r.Module, typeName: r.Type, method: r.Method}] = true
	}

	var out []ir.Decl
	var issues []error
	// Track lowered free fns by module so we can transitively scan them
	// for additional same-module callees (the closure step below).
	type loweredFromModule struct {
		module string
		fn     *ir.FnDecl
	}
	var loweredFreeFns []loweredFromModule
	for _, r := range reachedFns {
		res := stdlibResolveResult(reg, r.Module)
		chk := stdlibCheckResult(reg, r.Module)
		lowered, fnIssues := ir.LowerFnDecl(mod.Package, r.Fn, res, chk)
		issues = append(issues, fnIssues...)
		if lowered == nil {
			continue
		}
		lowered.Name = StdlibSymbol(r.Module, r.Fn.Name)
		out = append(out, lowered)
		loweredFreeFns = append(loweredFreeFns, loweredFromModule{module: r.Module, fn: lowered})
	}
	for _, m := range reachedMethods {
		res := stdlibResolveResult(reg, m.Module)
		chk := stdlibCheckResult(reg, m.Module)
		lowered, fnIssues := ir.LowerFnDecl(mod.Package, m.Fn, res, chk)
		issues = append(issues, fnIssues...)
		if lowered == nil {
			continue
		}
		freeFn := methodToFreeFn(lowered, m.Module, m.Type, m.Method)
		out = append(out, freeFn)
		// Methods can call same-module free fns too; route them
		// through the same closure step.
		loweredFreeFns = append(loweredFreeFns, loweredFromModule{module: m.Module, fn: freeFn})
	}
	RewriteStdlibCallsites(mod, reachedFns)
	RewriteStdlibMethodCallsites(mod, reachedMethods)

	// Closure pass: an injected stdlib body can reference other free
	// fns from its own module by bare Ident (e.g. `trim` calls
	// `trimEnd(trimStart(s))`). The first-hop scan above only saw
	// `strings.trim` from the user module — `trimStart`/`trimEnd`
	// would slip through. Walk each newly lowered body, find bare
	// Ident calls that resolve to same-module stdlib fns, inject
	// those, and rewrite the call site to the mangled name. Loop to
	// fixed point so a chain (`a → b → c`) is fully closed in one
	// pass.
	queue := append([]loweredFromModule(nil), loweredFreeFns...)
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		for callName := range scanBareIdentCallNames(next.fn) {
			calleeFn := reg.LookupFnDecl(next.module, callName)
			if calleeFn == nil {
				continue
			}
			k := fnKey{module: next.module, name: callName}
			if injectedFn[k] {
				// Already injected — just rewrite this body's
				// call sites for the name.
				rewriteBareIdentCalls(next.fn, callName, StdlibSymbol(next.module, callName))
				continue
			}
			injectedFn[k] = true
			res := stdlibResolveResult(reg, next.module)
			chk := stdlibCheckResult(reg, next.module)
			lowered, fnIssues := ir.LowerFnDecl(mod.Package, calleeFn, res, chk)
			issues = append(issues, fnIssues...)
			if lowered == nil {
				continue
			}
			lowered.Name = StdlibSymbol(next.module, callName)
			out = append(out, lowered)
			rewriteBareIdentCalls(next.fn, callName, lowered.Name)
			queue = append(queue, loweredFromModule{module: next.module, fn: lowered})
		}
	}
	return out, issues
}

// scanBareIdentCallNames returns the set of names referenced in
// `bareName(args)` shaped call sites inside a function body — i.e.
// `CallExpr{Callee: *Ident}`. Method calls and qualifier calls
// (`module.fn`, `recv.method`) are intentionally excluded; those
// have their own reach paths and rewriters.
//
// Used by the closure step in `injectReachableStdlibBodies` to
// discover same-module stdlib helpers an already-injected body
// transitively depends on (e.g. `strings.trim`'s body references
// `trimStart` and `trimEnd` as bare Idents). The caller filters the
// returned names against `Registry.LookupFnDecl(module, name)` to
// keep user-defined locals / params out of the injection set.
func scanBareIdentCallNames(fn *ir.FnDecl) map[string]struct{} {
	out := map[string]struct{}{}
	if fn == nil || fn.Body == nil {
		return out
	}
	ir.Walk(ir.VisitorFunc(func(n ir.Node) bool {
		call, ok := n.(*ir.CallExpr)
		if !ok || call == nil {
			return true
		}
		ident, ok := call.Callee.(*ir.Ident)
		if !ok || ident == nil || ident.Name == "" {
			return true
		}
		out[ident.Name] = struct{}{}
		return true
	}), fn.Body)
	return out
}

// rewriteBareIdentCalls walks a function body and renames any bare
// Ident callee whose Name == old to new. Mutates the IR in place.
// Method calls and FieldExpr callees are not affected — they have
// their own rewriter paths.
//
// Used by the closure step after injecting a same-module callee:
// the caller's body still references the callee by its short name
// (`trimEnd(s)`), and this rewrites it to the mangled symbol
// (`osty_std_strings__trimEnd(s)`).
func rewriteBareIdentCalls(fn *ir.FnDecl, oldName, newName string) {
	if fn == nil || fn.Body == nil || oldName == "" || newName == "" || oldName == newName {
		return
	}
	ir.Walk(ir.VisitorFunc(func(n ir.Node) bool {
		call, ok := n.(*ir.CallExpr)
		if !ok || call == nil {
			return true
		}
		ident, ok := call.Callee.(*ir.Ident)
		if !ok || ident == nil || ident.Name != oldName {
			return true
		}
		ident.Name = newName
		ident.Kind = ir.IdentFn
		return true
	}), fn.Body)
}

// methodToFreeFn converts a lowered stdlib method declaration into a
// free-function form with `self` as the first explicit positional
// parameter. The conversion is structural:
//
//   - Rename to `StdlibMethodSymbol(module, type, method)` so call-site
//     rewriting (which emits the same mangled name) finds the body.
//   - Prepend a Param `{Name: "self", Type: NamedType{Package: module,
//     Name: typeName}}`. The original method's body references `self`
//     as a bare identifier; that identifier resolves to the new param
//     by name without further rewriting.
//   - Clear `ReceiverMut` since the function is now a top-level free
//     fn — backends that branch on receiver shape now see a regular
//     fn with self as its first arg.
//
// Generic owner types (e.g. `List<T>.foo` once method injection covers
// generics) are out of scope here — that path needs the type-parameter
// list propagated from the owning struct, which today's stdlib
// injection set deliberately excludes.
func methodToFreeFn(lowered *ir.FnDecl, module, typeName, method string) *ir.FnDecl {
	selfTy := &ir.NamedType{Package: module, Name: typeName}
	selfParam := &ir.Param{
		Name:  "self",
		Type:  selfTy,
		SpanV: lowered.SpanV,
	}
	lowered.Params = append([]*ir.Param{selfParam}, lowered.Params...)
	lowered.Name = StdlibMethodSymbol(module, typeName, method)
	lowered.ReceiverMut = false
	return lowered
}

// stdlibResolveResult projects one stdlib module's parsed package into a
// resolve.Result suitable for the lowerer. Returns nil if the module or
// its parsed file is absent, which lets the lowerer degrade gracefully
// rather than panic.
func stdlibResolveResult(reg *stdlib.Registry, module string) *resolve.Result {
	if reg == nil {
		return nil
	}
	mod, ok := reg.Modules[module]
	if !ok || mod == nil || mod.Package == nil || len(mod.Package.Files) == 0 {
		return nil
	}
	pf := mod.Package.Files[0]
	return &resolve.Result{
		Refs:      pf.Refs,
		TypeRefs:  pf.TypeRefs,
		FileScope: pf.FileScope,
	}
}
