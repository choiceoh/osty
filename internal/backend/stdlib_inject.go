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
func injectReachableStdlibBodies(mod *ir.Module, reg *stdlib.Registry) ([]ir.Decl, []error) {
	if mod == nil || reg == nil {
		return nil, nil
	}
	reached := ReachableStdlibFns(mod, reg)
	if len(reached) == 0 {
		return nil, nil
	}
	var out []ir.Decl
	var issues []error
	for _, r := range reached {
		res := stdlibResolveResult(reg, r.Module)
		chk := stdlibCheckResult(reg, r.Module)
		lowered, fnIssues := ir.LowerFnDecl(mod.Package, r.Fn, res, chk)
		issues = append(issues, fnIssues...)
		if lowered == nil {
			continue
		}
		lowered.Name = StdlibSymbol(r.Module, r.Fn.Name)
		out = append(out, lowered)
	}
	RewriteStdlibCallsites(mod, reached)
	return out, issues
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
