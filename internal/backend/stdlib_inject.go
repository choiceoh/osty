package backend

import (
	"sort"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/ir"
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
