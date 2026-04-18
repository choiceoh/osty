package backend

import (
	"github.com/osty/osty/internal/ir"
)

// StdlibSymbol returns the mangled IR name for a stdlib function injected
// alongside user code. The scheme is `osty_std_<module>__<name>` — the
// `osty_std_` prefix makes the provenance obvious in generated IR, the
// double underscore separates the module segment from the function name
// so a future `module_with_underscore` cannot collide with a function
// name, and only ASCII identifier characters are used so backends that
// are strict about symbol charsets accept the result unchanged.
//
// StdlibSymbol does not validate its inputs — the caller has already
// looked up the module and fn via stdlib.Registry.
func StdlibSymbol(module, name string) string {
	return "osty_std_" + module + "__" + name
}

// RewriteStdlibCallsites walks mod and rewrites every `module.name(...)`
// call whose (module, name) pair is in reached to a bare call against
// the mangled symbol. reached is the set produced by
// ReachableStdlibFns — only calls with a matching entry are rewritten.
// Other qualified calls (runtime FFI, unknown user aliases) are left
// alone so their own diagnostics remain accurate.
//
// Rewriting happens in place on mod. Returns the number of call sites
// that were updated; 0 means no change.
func RewriteStdlibCallsites(mod *ir.Module, reached []ReachableStdlibFn) int {
	if mod == nil || len(reached) == 0 {
		return 0
	}
	set := map[ir.QualifiedRef]string{}
	for _, r := range reached {
		set[ir.QualifiedRef{Qualifier: r.Module, Name: r.Fn.Name}] = StdlibSymbol(r.Module, r.Fn.Name)
	}
	rw := &stdlibCallsiteRewriter{set: set}
	ir.Walk(rw, mod)
	return rw.count
}

type stdlibCallsiteRewriter struct {
	set   map[ir.QualifiedRef]string
	count int
}

func (r *stdlibCallsiteRewriter) Visit(n ir.Node) ir.Visitor {
	call, ok := n.(*ir.CallExpr)
	if !ok {
		return r
	}
	field, ok := call.Callee.(*ir.FieldExpr)
	if !ok {
		return r
	}
	ident, ok := field.X.(*ir.Ident)
	if !ok || ident.Name == "" || field.Name == "" {
		return r
	}
	mangled, hit := r.set[ir.QualifiedRef{Qualifier: ident.Name, Name: field.Name}]
	if !hit {
		return r
	}
	call.Callee = &ir.Ident{
		Name:  mangled,
		Kind:  ir.IdentFn,
		T:     field.T,
		SpanV: field.SpanV,
	}
	r.count++
	return r
}
