package backend

import (
	"sort"
	"strings"

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
	// ValuePath is set for calls through exported stdlib singleton
	// values such as `encoding.base64.encode(...)` or
	// `encoding.base64.url.encode(...)`. It is empty for ordinary typed
	// receiver calls like `h.encode(...)`.
	ValuePath string
}

type stdlibMethodReachKey struct{ module, typeName, method string }

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
	seen := map[stdlibMethodReachKey]struct{}{}
	var found []ReachableStdlibMethod
	for ref := range ir.ReachMethods(mod) {
		k := stdlibMethodReachKey{module: ref.Module, typeName: ref.Type, method: ref.Method}
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
	ir.Walk(ir.VisitorFunc(func(n ir.Node) bool {
		var (
			receiver ir.Expr
			method   string
		)
		switch call := n.(type) {
		case *ir.CallExpr:
			if call == nil {
				return true
			}
			field, ok := call.Callee.(*ir.FieldExpr)
			if !ok || field == nil || field.Name == "" {
				return true
			}
			receiver = field.X
			method = field.Name
		case *ir.MethodCall:
			if call == nil || call.Receiver == nil || call.Name == "" {
				return true
			}
			receiver = call.Receiver
			method = call.Name
		default:
			return true
		}
		addStdlibValuePathMethod(reg, &found, seen, receiver, method)
		return true
	}), mod)
	// Interface-method expansion: when a method ref points at an
	// InterfaceDecl method (e.g. `Error.message` from std.error), the
	// interface signature itself usually has no body — the real impl
	// lives on a concrete StructDecl/EnumDecl that satisfies the
	// interface (e.g. `BasicError.message`). Without expansion, the
	// concrete impl method never reaches injection, no
	// `@BasicError__message` definition is emitted, and any
	// downstream forwarder synthesis from `@Error__message` has
	// nothing to forward to.
	//
	// Expansion: for each ref whose `Type` matches an InterfaceDecl
	// in the same stdlib module, scan the module's StructDecl /
	// EnumDecl for any that declare a method with the same name and
	// add that concrete impl method to the reach set. This is a
	// conservative match (method-name only) — the MIR-side
	// `interfaceSatisfiedByStruct` does the full coverage check at
	// layout build time. For injection-time reach we just need to
	// pull in the bodies that COULD satisfy the call.
	expandInterfaceMethodReach(reg, &found, seen, mod)
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

// expandInterfaceMethodReach scans the existing `found` set for any
// method ref whose `Type` is an InterfaceDecl in the stdlib module,
// and adds the same-name method from any concrete StructDecl /
// EnumDecl in that module that declares it. The expansion is
// conservative (method-name match, no full coverage check) — the
// MIR-side `interfaceSatisfiedByStruct` does the strict check at
// layout build time.
//
// Without this, a call like `e.message()` where `e: Error` only
// reaches the InterfaceDecl's bodyless `Error.message` signature.
// `bodyfulStdlibMethods` filters that out → no method body injected
// → cross-pkg link fails when the user calls a method on a value
// typed as the interface.
func expandInterfaceMethodReach(
	reg *stdlib.Registry,
	found *[]ReachableStdlibMethod,
	seen map[stdlibMethodReachKey]struct{},
	mod *ir.Module,
) {
	_ = mod
	if reg == nil || found == nil {
		return
	}
	// Snapshot the initial refs because we mutate *found below.
	initial := make([]ReachableStdlibMethod, len(*found))
	copy(initial, *found)
	for _, ref := range initial {
		regMod, ok := reg.Modules[ref.Module]
		if !ok || regMod == nil || regMod.File == nil {
			continue
		}
		// Is ref.Type an interface in this stdlib module?
		isInterface := false
		for _, decl := range regMod.File.Decls {
			if iface, ok := decl.(*ast.InterfaceDecl); ok && iface.Name == ref.Type {
				isInterface = true
				break
			}
		}
		if !isInterface {
			continue
		}
		// Scan struct + enum decls for a same-name method.
		for _, decl := range regMod.File.Decls {
			var (
				implName string
				methods  []*ast.FnDecl
			)
			switch d := decl.(type) {
			case *ast.StructDecl:
				implName = d.Name
				methods = d.Methods
			case *ast.EnumDecl:
				implName = d.Name
				methods = d.Methods
			default:
				continue
			}
			for _, m := range methods {
				if m == nil || m.Name != ref.Method {
					continue
				}
				k := stdlibMethodReachKey{module: ref.Module, typeName: implName, method: ref.Method}
				if _, dup := seen[k]; dup {
					continue
				}
				seen[k] = struct{}{}
				*found = append(*found, ReachableStdlibMethod{
					Module: ref.Module,
					Type:   implName,
					Method: ref.Method,
					Fn:     m,
				})
				break
			}
		}
	}
}

func addStdlibValuePathMethod(
	reg *stdlib.Registry,
	found *[]ReachableStdlibMethod,
	seen map[stdlibMethodReachKey]struct{},
	receiver ir.Expr,
	method string,
) {
	module, path, ok := stdlibFieldPath(receiver)
	if !ok || module == "" || len(path) == 0 || method == "" {
		return
	}
	typeName, ok := stdlibValuePathTypeName(reg, module, path)
	if !ok || typeName == "" {
		return
	}
	valuePath := stdlibJoinFieldPath(path)
	k := stdlibMethodReachKey{module: module, typeName: typeName, method: method}
	if _, dup := seen[k]; dup {
		for i := range *found {
			entry := &(*found)[i]
			if entry.Module == module && entry.Type == typeName && entry.Method == method && entry.ValuePath == "" {
				entry.ValuePath = valuePath
				return
			}
		}
		return
	}
	fn := reg.LookupMethodDecl(module, typeName, method)
	if fn == nil {
		return
	}
	seen[k] = struct{}{}
	*found = append(*found, ReachableStdlibMethod{
		Module:    module,
		Type:      typeName,
		Method:    method,
		Fn:        fn,
		ValuePath: valuePath,
	})
}

func stdlibValuePathTypeName(reg *stdlib.Registry, module string, path []string) (string, bool) {
	if reg == nil || module == "" || len(path) == 0 {
		return "", false
	}
	mod := reg.Modules[module]
	if mod == nil || mod.Package == nil {
		return "", false
	}
	typeName, ok := stdlibTopLevelLetTypeName(mod, path[0])
	if !ok {
		return "", false
	}
	for _, field := range path[1:] {
		typeName, ok = stdlibStructFieldTypeName(mod, typeName, field)
		if !ok {
			return "", false
		}
	}
	return typeName, true
}

func stdlibTopLevelLetTypeName(mod *stdlib.Module, name string) (string, bool) {
	if mod == nil || mod.Package == nil || mod.Package.PkgScope == nil || name == "" {
		return "", false
	}
	sym := mod.Package.PkgScope.LookupLocal(name)
	if sym == nil {
		return "", false
	}
	decl, ok := sym.Decl.(*ast.LetDecl)
	if !ok || decl == nil {
		return "", false
	}
	return stdlibNamedTypeName(decl.Type)
}

func stdlibStructFieldTypeName(mod *stdlib.Module, owner, fieldName string) (string, bool) {
	if mod == nil || mod.Package == nil || owner == "" || fieldName == "" {
		return "", false
	}
	for _, pf := range mod.Package.Files {
		if pf == nil || pf.File == nil {
			continue
		}
		for _, decl := range pf.File.Decls {
			st, ok := decl.(*ast.StructDecl)
			if !ok || st == nil || st.Name != owner {
				continue
			}
			for _, field := range st.Fields {
				if field != nil && field.Name == fieldName {
					return stdlibNamedTypeName(field.Type)
				}
			}
		}
	}
	return "", false
}

func stdlibNamedTypeName(t ast.Type) (string, bool) {
	named, ok := t.(*ast.NamedType)
	if !ok || named == nil || len(named.Path) == 0 {
		return "", false
	}
	return named.Path[len(named.Path)-1], true
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
	reachedFns := bodyfulStdlibFns(ReachableStdlibFns(mod, reg))
	reachedMethods := bodyfulStdlibMethods(ReachableStdlibMethods(mod, reg))
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
		qualifyLoweredStdlibFnTypes(lowered, reg, r.Module)
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
		freeFn := methodToFreeFn(lowered, m.Module, m.Type, m.Method, reg.LookupTypeGenerics(m.Module, m.Type))
		qualifyLoweredStdlibFnTypes(freeFn, reg, m.Module)
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
		for _, callName := range sortedBareIdentCallNames(next.fn) {
			calleeFn := reg.LookupFnDecl(next.module, callName)
			if calleeFn == nil {
				continue
			}
			if calleeFn.Body == nil {
				rewriteBareIdentCalls(next.fn, callName, CanonicalStdlibSymbol(next.module, callName))
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
			qualifyLoweredStdlibFnTypes(lowered, reg, next.module)
			lowered.Name = StdlibSymbol(next.module, callName)
			out = append(out, lowered)
			rewriteBareIdentCalls(next.fn, callName, lowered.Name)
			queue = append(queue, loweredFromModule{module: next.module, fn: lowered})
			loweredFreeFns = append(loweredFreeFns, loweredFromModule{module: next.module, fn: lowered})
		}
		for _, ref := range sortedQualifiedStdlibCallRefs(next.fn) {
			calleeModule := stdlibModuleNameFromQualifier(ref.Qualifier)
			if calleeModule == "" || calleeModule == "error" {
				continue
			}
			calleeFn := reg.LookupFnDecl(calleeModule, ref.Name)
			if calleeFn == nil {
				continue
			}
			if calleeFn.Body == nil {
				rewriteQualifiedStdlibCalls(next.fn, ref.Qualifier, ref.Name, CanonicalStdlibSymbol(calleeModule, ref.Name))
				continue
			}
			k := fnKey{module: calleeModule, name: ref.Name}
			if injectedFn[k] {
				rewriteQualifiedStdlibCalls(next.fn, ref.Qualifier, ref.Name, StdlibSymbol(calleeModule, ref.Name))
				continue
			}
			injectedFn[k] = true
			res := stdlibResolveResult(reg, calleeModule)
			chk := stdlibCheckResult(reg, calleeModule)
			lowered, fnIssues := ir.LowerFnDecl(mod.Package, calleeFn, res, chk)
			issues = append(issues, fnIssues...)
			if lowered == nil {
				continue
			}
			qualifyLoweredStdlibFnTypes(lowered, reg, calleeModule)
			lowered.Name = StdlibSymbol(calleeModule, ref.Name)
			out = append(out, lowered)
			rewriteQualifiedStdlibCalls(next.fn, ref.Qualifier, ref.Name, lowered.Name)
			queue = append(queue, loweredFromModule{module: calleeModule, fn: lowered})
			loweredFreeFns = append(loweredFreeFns, loweredFromModule{module: calleeModule, fn: lowered})
		}
		for _, m := range reachableStdlibMethodsInFn(next.fn, reg) {
			if m.Fn == nil || m.Fn.Body == nil {
				continue
			}
			k := methodKey{module: m.Module, typeName: m.Type, method: m.Method}
			if injectedMethod[k] {
				rewriteStdlibMethodCallsitesInFn(next.fn, []ReachableStdlibMethod{m})
				continue
			}
			injectedMethod[k] = true
			res := stdlibResolveResult(reg, m.Module)
			chk := stdlibCheckResult(reg, m.Module)
			lowered, fnIssues := ir.LowerFnDecl(mod.Package, m.Fn, res, chk)
			issues = append(issues, fnIssues...)
			if lowered == nil {
				continue
			}
			freeFn := methodToFreeFn(lowered, m.Module, m.Type, m.Method, reg.LookupTypeGenerics(m.Module, m.Type))
			qualifyLoweredStdlibFnTypes(freeFn, reg, m.Module)
			out = append(out, freeFn)
			rewriteStdlibMethodCallsitesInFn(next.fn, []ReachableStdlibMethod{m})
			queue = append(queue, loweredFromModule{module: m.Module, fn: freeFn})
			loweredFreeFns = append(loweredFreeFns, loweredFromModule{module: m.Module, fn: freeFn})
		}
	}
	// Globals closure: every injected fn body that reads a stdlib
	// top-level `let` (e.g. std.strings' `graphemeBreakCR` Int
	// constant referenced by `graphemeBreakProperty`) needs the
	// matching definition in the user module — otherwise the LLVM
	// emitter emits an unresolved `@<name>` reference and clang fails
	// at link time. The pass mirrors the fn closure: walk each
	// injected body, find IdentGlobal references whose Name maps to a
	// stdlib `pub let`, lower the let, mangle to
	// `osty_std_<module>__<name>`, append, and rewrite the body's
	// reference. Globals can transitively reference other globals
	// through their initialiser, so the discovery loops to a fixed
	// point.
	type letKey struct{ module, name string }
	injectedLet := map[letKey]bool{}
	letQueue := append([]loweredFromModule(nil), loweredFreeFns...)
	for len(letQueue) > 0 {
		next := letQueue[0]
		letQueue = letQueue[1:]
		for _, name := range sortedBareIdentGlobalReadNames(next.fn) {
			letDecl := reg.LookupLetDecl(next.module, name)
			if letDecl == nil {
				continue
			}
			k := letKey{module: next.module, name: name}
			mangled := StdlibSymbol(next.module, name)
			if injectedLet[k] {
				rewriteBareIdentGlobalReads(next.fn, name, mangled)
				continue
			}
			injectedLet[k] = true
			res := stdlibResolveResult(reg, next.module)
			chk := stdlibCheckResult(reg, next.module)
			lowered, ldIssues := ir.LowerLetDecl(mod.Package, letDecl, res, chk)
			issues = append(issues, ldIssues...)
			if lowered == nil {
				continue
			}
			lowered.Name = mangled
			out = append(out, lowered)
			rewriteBareIdentGlobalReads(next.fn, name, mangled)
			// Globals' initialisers can reference other lets in the
			// same module — wrap the LetDecl in a synthetic FnDecl
			// shape just enough to drive the same scanner.
			if lowered.Value != nil {
				letQueue = append(letQueue, loweredFromModule{
					module: next.module,
					fn:     letInitAsFnShim(lowered),
				})
			}
		}
	}
	qualifyStdlibCallsiteTypes(mod, out)
	return out, issues
}

// letInitAsFnShim wraps a LetDecl's initialiser in a synthetic FnDecl
// so the existing fn-shaped scanners (sortedBareIdentGlobalReadNames /
// rewriteBareIdentGlobalReads) can walk it. The wrapper isn't appended
// to the user module — it only exists so the discovery loop can
// transitively scan a global's initialiser the same way it scans
// injected fn bodies.
func letInitAsFnShim(ld *ir.LetDecl) *ir.FnDecl {
	if ld == nil || ld.Value == nil {
		return nil
	}
	return &ir.FnDecl{
		Name: ld.Name,
		Body: &ir.Block{
			Stmts:  []ir.Stmt{&ir.ExprStmt{X: ld.Value}},
			Result: ld.Value,
			SpanV:  ld.SpanV,
		},
		SpanV: ld.SpanV,
	}
}

func bodyfulStdlibFns(in []ReachableStdlibFn) []ReachableStdlibFn {
	if len(in) == 0 {
		return nil
	}
	out := make([]ReachableStdlibFn, 0, len(in))
	for _, r := range in {
		if r.Fn == nil || r.Fn.Body == nil {
			continue
		}
		out = append(out, r)
	}
	return out
}

func bodyfulStdlibMethods(in []ReachableStdlibMethod) []ReachableStdlibMethod {
	if len(in) == 0 {
		return nil
	}
	out := make([]ReachableStdlibMethod, 0, len(in))
	for _, r := range in {
		if r.Fn == nil || r.Fn.Body == nil {
			continue
		}
		out = append(out, r)
	}
	return out
}

func reachableStdlibMethodsInFn(fn *ir.FnDecl, reg *stdlib.Registry) []ReachableStdlibMethod {
	if fn == nil || reg == nil {
		return nil
	}
	return ReachableStdlibMethods(&ir.Module{Decls: []ir.Decl{fn}}, reg)
}

func rewriteStdlibMethodCallsitesInFn(fn *ir.FnDecl, reached []ReachableStdlibMethod) {
	if fn == nil || len(reached) == 0 {
		return
	}
	RewriteStdlibMethodCallsites(&ir.Module{Decls: []ir.Decl{fn}}, reached)
}

func qualifyLoweredStdlibFnTypes(fn *ir.FnDecl, reg *stdlib.Registry, module string) {
	if fn == nil || reg == nil || module == "" {
		return
	}
	entry := loweredStdlibTypesFor(reg)
	if entry == nil {
		return
	}
	qualifyStdlibDeclTypes(fn, module, entry.moduleTypeNames(module))
}

func qualifyStdlibCallsiteTypes(mod *ir.Module, injected []ir.Decl) {
	if mod == nil || len(injected) == 0 {
		return
	}
	returns := map[string]ir.Type{}
	for _, d := range injected {
		fn, ok := d.(*ir.FnDecl)
		if !ok || fn == nil || fn.Name == "" || fn.Return == nil {
			continue
		}
		returns[fn.Name] = ir.CloneType(fn.Return)
	}
	if len(returns) == 0 {
		return
	}
	ir.Walk(ir.VisitorFunc(func(n ir.Node) bool {
		call, ok := n.(*ir.CallExpr)
		if !ok || call == nil {
			return true
		}
		id, ok := call.Callee.(*ir.Ident)
		if !ok || id == nil {
			return true
		}
		if ret := returns[id.Name]; ret != nil {
			call.T = ir.CloneType(ret)
			if ft, ok := id.T.(*ir.FnType); ok && ft != nil {
				ft.Return = ir.CloneType(ret)
			}
		}
		return true
	}), mod)
	ir.Walk(ir.VisitorFunc(func(n ir.Node) bool {
		mc, ok := n.(*ir.MethodCall)
		if !ok || mc == nil || mc.Receiver == nil {
			return true
		}
		if ret := builtinReceiverMethodReturnType(mc.Receiver.Type(), mc.Name); ret != nil {
			mc.T = ret
		}
		return true
	}), mod)
	qualifyStdlibLetTypes(mod)
	qualifyStdlibIdentBindings(mod)
	qualifyStdlibCollectionLiteralTypes(mod)
	qualifyStdlibLetTypes(mod)
	qualifyStdlibIdentBindings(mod)
}

func qualifyStdlibCollectionLiteralTypes(mod *ir.Module) {
	ir.Walk(ir.VisitorFunc(func(n ir.Node) bool {
		switch x := n.(type) {
		case *ir.ListLit:
			if typeContainsQualifiedNamed(x.Elem) {
				return true
			}
			for _, elem := range x.Elems {
				if t := qualifiedValueType(elem); t != nil {
					x.Elem = t
					break
				}
			}
		case *ir.MapLit:
			if !typeContainsQualifiedNamed(x.KeyT) {
				for _, entry := range x.Entries {
					if t := qualifiedValueType(entry.Key); t != nil {
						x.KeyT = t
						break
					}
				}
			}
			if !typeContainsQualifiedNamed(x.ValT) {
				for _, entry := range x.Entries {
					if t := qualifiedValueType(entry.Value); t != nil {
						x.ValT = t
						break
					}
				}
			}
		}
		return true
	}), mod)
}

func qualifyStdlibLetTypes(mod *ir.Module) {
	ir.Walk(ir.VisitorFunc(func(n ir.Node) bool {
		let, ok := n.(*ir.LetStmt)
		if !ok || let == nil || let.Value == nil {
			return true
		}
		if t := qualifiedValueType(let.Value); t != nil {
			let.Type = t
		}
		return true
	}), mod)
}

func qualifyStdlibIdentBindings(mod *ir.Module) {
	bindings := map[string]ir.Type{}
	ir.Walk(ir.VisitorFunc(func(n ir.Node) bool {
		let, ok := n.(*ir.LetStmt)
		if !ok || let == nil || let.Name == "" || !typeContainsQualifiedNamed(let.Type) {
			return true
		}
		bindings[let.Name] = ir.CloneType(let.Type)
		return true
	}), mod)
	if len(bindings) > 0 {
		ir.Walk(ir.VisitorFunc(func(n ir.Node) bool {
			id, ok := n.(*ir.Ident)
			if !ok || id == nil {
				return true
			}
			if t := bindings[id.Name]; t != nil {
				id.T = ir.CloneType(t)
			}
			return true
		}), mod)
	}
}

func builtinReceiverMethodReturnType(receiver ir.Type, method string) ir.Type {
	switch t := receiver.(type) {
	case *ir.OptionalType:
		switch method {
		case "unwrap", "unwrapOr":
			return ir.CloneType(t.Inner)
		case "isSome", "isNone":
			return ir.TBool
		}
	case *ir.NamedType:
		switch t.Name {
		case "Option", "Maybe":
			switch method {
			case "unwrap", "unwrapOr":
				if len(t.Args) >= 1 {
					return ir.CloneType(t.Args[0])
				}
			case "isSome", "isNone":
				return ir.TBool
			}
		case "Result":
			switch method {
			case "unwrap", "unwrapOr":
				if len(t.Args) >= 1 {
					return ir.CloneType(t.Args[0])
				}
			case "unwrapErr":
				if len(t.Args) >= 2 {
					return ir.CloneType(t.Args[1])
				}
			case "isOk", "isErr":
				return ir.TBool
			}
		}
	}
	return nil
}

func qualifiedValueType(e ir.Expr) ir.Type {
	if e == nil {
		return nil
	}
	t := e.Type()
	if !typeContainsQualifiedNamed(t) {
		return nil
	}
	return ir.CloneType(t)
}

func typeContainsQualifiedNamed(t ir.Type) bool {
	switch x := t.(type) {
	case *ir.NamedType:
		if x.Package != "" {
			return true
		}
		for _, a := range x.Args {
			if typeContainsQualifiedNamed(a) {
				return true
			}
		}
	case *ir.OptionalType:
		return typeContainsQualifiedNamed(x.Inner)
	case *ir.TupleType:
		for _, e := range x.Elems {
			if typeContainsQualifiedNamed(e) {
				return true
			}
		}
	case *ir.FnType:
		for _, p := range x.Params {
			if typeContainsQualifiedNamed(p) {
				return true
			}
		}
		return typeContainsQualifiedNamed(x.Return)
	}
	return false
}

func sortedBareIdentCallNames(fn *ir.FnDecl) []string {
	seen := scanBareIdentCallNames(fn)
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func sortedQualifiedStdlibCallRefs(fn *ir.FnDecl) []ir.QualifiedRef {
	seen := scanQualifiedCallRefs(fn)
	out := make([]ir.QualifiedRef, 0, len(seen))
	for ref := range seen {
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Qualifier != out[j].Qualifier {
			return out[i].Qualifier < out[j].Qualifier
		}
		return out[i].Name < out[j].Name
	})
	return out
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

func scanQualifiedCallRefs(fn *ir.FnDecl) map[ir.QualifiedRef]struct{} {
	out := map[ir.QualifiedRef]struct{}{}
	if fn == nil || fn.Body == nil {
		return out
	}
	ir.Walk(ir.VisitorFunc(func(n ir.Node) bool {
		call, ok := n.(*ir.CallExpr)
		if !ok || call == nil {
			return true
		}
		field, ok := call.Callee.(*ir.FieldExpr)
		if !ok || field == nil || field.Name == "" {
			return true
		}
		ident, ok := field.X.(*ir.Ident)
		if !ok || ident == nil || ident.Name == "" {
			return true
		}
		out[ir.QualifiedRef{Qualifier: ident.Name, Name: field.Name}] = struct{}{}
		return true
	}), fn.Body)
	return out
}

func stdlibModuleNameFromQualifier(qualifier string) string {
	if qualifier == "" {
		return ""
	}
	const prefix = "std."
	if strings.HasPrefix(qualifier, prefix) {
		return strings.TrimPrefix(qualifier, prefix)
	}
	return qualifier
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

// sortedBareIdentGlobalReadNames returns the set of distinct names
// referenced by `Ident{Kind: IdentGlobal}` reads inside a function
// body, in deterministic order. Idents in call-callee position are
// excluded — those are the fn-closure path. The result drives the
// globals-closure step: each name is checked against
// `Registry.LookupLetDecl` to confirm it's a stdlib top-level let
// before injection.
func sortedBareIdentGlobalReadNames(fn *ir.FnDecl) []string {
	if fn == nil || fn.Body == nil {
		return nil
	}
	seen := map[string]struct{}{}
	ir.Walk(ir.VisitorFunc(func(n ir.Node) bool {
		ident, ok := n.(*ir.Ident)
		if !ok || ident == nil || ident.Kind != ir.IdentGlobal || ident.Name == "" {
			return true
		}
		seen[ident.Name] = struct{}{}
		return true
	}), fn.Body)
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// rewriteBareIdentGlobalReads renames every `Ident{Kind: IdentGlobal,
// Name: oldName}` inside fn.Body to newName. Mirrors
// `rewriteBareIdentCalls` but for value reads instead of call
// callees. The Kind stays IdentGlobal so the MIR lowerer keeps
// emitting GlobalRefRV for the rewritten name.
func rewriteBareIdentGlobalReads(fn *ir.FnDecl, oldName, newName string) {
	if fn == nil || fn.Body == nil || oldName == "" || newName == "" || oldName == newName {
		return
	}
	ir.Walk(ir.VisitorFunc(func(n ir.Node) bool {
		ident, ok := n.(*ir.Ident)
		if !ok || ident == nil || ident.Kind != ir.IdentGlobal || ident.Name != oldName {
			return true
		}
		ident.Name = newName
		return true
	}), fn.Body)
}

func rewriteQualifiedStdlibCalls(fn *ir.FnDecl, qualifier, name, newName string) {
	if fn == nil || fn.Body == nil || qualifier == "" || name == "" || newName == "" {
		return
	}
	ir.Walk(ir.VisitorFunc(func(n ir.Node) bool {
		call, ok := n.(*ir.CallExpr)
		if !ok || call == nil {
			return true
		}
		field, ok := call.Callee.(*ir.FieldExpr)
		if !ok || field == nil || field.Name != name {
			return true
		}
		ident, ok := field.X.(*ir.Ident)
		if !ok || ident == nil || ident.Name != qualifier {
			return true
		}
		call.Callee = &ir.Ident{
			Name:  newName,
			Kind:  ir.IdentFn,
			T:     field.T,
			SpanV: field.SpanV,
		}
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
func methodToFreeFn(lowered *ir.FnDecl, module, typeName, method string, ownerGenerics []*ast.GenericParam) *ir.FnDecl {
	// Propagate the owner type's generics onto the free fn so the
	// receiver param's NamedType carries the right Args and the body
	// can substitute them at monomorph time. Without this, `List<T>.
	// map<R>` becomes a free fn `osty_std_collections__List__map(self:
	// List, f)` — the body's `for item in self` then has nothing to
	// bind `T` to, and the call-site arity check rejects the [T, R]
	// type-arg pair against a [R]-only generics list.
	var ownerTypeArgs []ir.Type
	for _, g := range ownerGenerics {
		if g == nil || g.Name == "" {
			continue
		}
		ownerTypeArgs = append(ownerTypeArgs, &ir.TypeVar{Name: g.Name})
	}
	selfTy := &ir.NamedType{Package: module, Name: typeName, Args: ownerTypeArgs, Builtin: ir.BuiltinTypeOwningModule(typeName) != ""}
	selfParam := &ir.Param{
		Name:  "self",
		Type:  selfTy,
		SpanV: lowered.SpanV,
	}
	lowered.Params = append([]*ir.Param{selfParam}, lowered.Params...)
	if len(ownerTypeArgs) > 0 {
		ownerTypeParams := make([]*ir.TypeParam, 0, len(ownerGenerics))
		for _, g := range ownerGenerics {
			if g == nil || g.Name == "" {
				continue
			}
			ownerTypeParams = append(ownerTypeParams, &ir.TypeParam{Name: g.Name, SpanV: lowered.SpanV})
		}
		lowered.Generics = append(ownerTypeParams, lowered.Generics...)
	}
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
		RefsByID:      pf.RefsByID,
		TypeRefsByID:  pf.TypeRefsByID,
		RefIdents:     pf.RefIdents,
		TypeRefIdents: pf.TypeRefIdents,
		FileScope:     pf.FileScope,
	}
}
