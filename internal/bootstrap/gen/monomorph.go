package gen

import (
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/resolve"
)

// Generic monomorphization (§2.7.3).
//
// The checker records every generic call site's concrete type-argument
// list on Result.Instantiations. This file drives the lowering: for
// each reachable (generic fn × concrete type tuple) pair we emit one
// specialized copy, rewriting its body's generic calls recursively so
// the transform reaches transitive instantiations as well. A generic
// fn with no reachable instantiations emits nothing — matching the
// spec's "header-like" demand-driven framing.
//
// Scope (out of scope here): generic struct / enum / alias types and
// methods on generic types still flow through the Go-native generics
// path. Tightening those is a separate, larger refactor.

// instRecord is one specialized instantiation of a generic fn — the
// concrete Go type rendering of each type argument plus the mangled
// Go name the specialization is emitted under.
type instRecord struct {
	Fn      *ast.FnDecl
	Sym     *resolve.Symbol
	GoTypes []string
	Mangled string
}

// requestInstance records that `(sym, goTypes)` is reachable and must
// be emitted. Returns the mangled name the caller should emit at the
// call site. Idempotent: repeated requests for the same (sym, tuple)
// dedupe to the same name without re-enqueueing.
func (g *gen) requestInstance(sym *resolve.Symbol, goTypes []string) string {
	fn, ok := sym.Decl.(*ast.FnDecl)
	if !ok || fn == nil {
		return ""
	}
	key := instDedupeKey(sym, goTypes)
	if name, ok := g.instByKey[key]; ok {
		return name
	}
	mangled := mangleInst(fn.Name, goTypes)
	rec := instRecord{Fn: fn, Sym: sym, GoTypes: goTypes, Mangled: mangled}
	g.instByKey[key] = mangled
	g.instQueue = append(g.instQueue, rec)
	return mangled
}

// instDedupeKey builds a map key that identifies a (symbol, type-tuple)
// pair. Uses a unit-separator byte between the pointer repr and each
// type fragment so `id_int` vs `id_int*` can't collide.
func instDedupeKey(sym *resolve.Symbol, goTypes []string) string {
	var b strings.Builder
	b.WriteString(sym.Name)
	b.WriteByte(0x1f)
	b.WriteString(sym.Pos.String())
	for _, t := range goTypes {
		b.WriteByte(0x1f)
		b.WriteString(t)
	}
	return b.String()
}

// initInstances resets the per-file monomorphization state. Called
// once per gen.run() before any decl is visited.
func (g *gen) initInstances() {
	g.instByKey = map[string]string{}
	g.instQueue = nil
}

// drainInstances emits every queued specialization and every further
// specialization triggered by their bodies. Runs to a fixed point;
// bounded by the call graph which is finite for any well-typed input.
func (g *gen) drainInstances() {
	// Index-based loop so appends during emission are picked up.
	for i := 0; i < len(g.instQueue); i++ {
		rec := g.instQueue[i]
		cleanup := g.pushSubst(rec.Fn.Generics, rec.GoTypes)
		g.emitFnDeclBody(rec.Fn, rec.Mangled)
		cleanup()
	}
}

// calleeSymbol resolves the function expression of a CallExpr to its
// declaring Symbol, or nil when the callee isn't a direct user-defined
// function reference (variable holding a closure, method call, etc.).
func (g *gen) calleeSymbol(fn ast.Expr) *resolve.Symbol {
	switch e := fn.(type) {
	case *ast.Ident:
		return g.symbolFor(e)
	case *ast.TurbofishExpr:
		if id, ok := e.Base.(*ast.Ident); ok {
			return g.symbolFor(id)
		}
	}
	return nil
}

// callMonoName returns the mangled specialization name for a generic
// call site, or "" when the call isn't a generic fn call (or the
// callee isn't a user-defined fn). Records the instantiation on the
// pending queue as a side effect so downstream emission picks it up.
//
// The current substEnv is implicitly consumed: g.goType on a TypeVar
// whose name is in substEnv substitutes its concrete Go type, so a
// call `id(y)` inside `wrap<U>` being monomorphized at U=Int yields
// goTypes=["int"] here. This is how transitive instantiation works —
// a generic fn's body is visited once per outer monomorph, and each
// inner generic call is re-specialized under that monomorph's subst.
func (g *gen) callMonoName(c *ast.CallExpr) string {
	if g.chk == nil || c == nil {
		return ""
	}
	args, ok := g.chk.InstantiationsByID[c.ID]
	if !ok || len(args) == 0 {
		return ""
	}
	sym := g.calleeSymbol(c.Fn)
	if sym == nil || sym.Kind != resolve.SymFn {
		return ""
	}
	fn, ok := sym.Decl.(*ast.FnDecl)
	if !ok || fn == nil || len(fn.Generics) == 0 {
		return ""
	}
	if len(args) != len(fn.Generics) {
		return ""
	}
	goTypes := make([]string, len(args))
	for i, a := range args {
		goTypes[i] = g.goType(a)
	}
	// If any arg resolved to a symbolic name still (unhandled TypeVar)
	// we bail — the call can't be safely monomorphized, fall through
	// to the generic path. This catches bugs rather than emitting
	// invalid Go silently.
	for _, t := range goTypes {
		if t == "" {
			return ""
		}
	}
	return g.requestInstance(sym, goTypes)
}

// mangleInst builds the specialized fn name. Illegal identifier
// characters in a Go type rendering (`[`, `]`, `.`, space, `*`) are
// flattened so e.g. `[]int` → `Ofint` — uniqueness within the set of
// types Osty currently emits is preserved; human readability is a
// non-goal here.
func mangleInst(name string, goTypes []string) string {
	var b strings.Builder
	b.WriteString(name)
	for _, t := range goTypes {
		b.WriteByte('_')
		b.WriteString(sanitizeTypeName(t))
	}
	return b.String()
}

// sanitizeTypeName collapses a Go type string into a conservative
// identifier fragment. `[` / `]` become `Of`, `*` → `Ptr`, spaces
// dropped, anything else non-alphanumeric → `_`. The mapping is
// injective over the set of Go type strings Osty currently emits,
// which is what monomorphization-name distinctness requires.
func sanitizeTypeName(t string) string {
	var b strings.Builder
	for _, r := range t {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '[' || r == ']':
			b.WriteString("Of")
		case r == '*':
			b.WriteString("Ptr")
		case r == ' ':
			// drop
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "T"
	}
	return b.String()
}

// pushSubst binds each generic parameter name to its Go rendering for
// the duration of a monomorphized emission. The returned cleanup
// restores the previous env; call it with `defer`.
func (g *gen) pushSubst(params []*ast.GenericParam, goTypes []string) func() {
	prev := g.substEnv
	next := map[string]string{}
	for k, v := range prev {
		next[k] = v
	}
	for i, p := range params {
		if i >= len(goTypes) {
			break
		}
		next[p.Name] = goTypes[i]
	}
	g.substEnv = next
	return func() { g.substEnv = prev }
}

// lookupSubst reports whether `name` is a generic type-parameter
// currently in scope and returns its concrete Go rendering if so.
func (g *gen) lookupSubst(name string) (string, bool) {
	if g.substEnv == nil {
		return "", false
	}
	v, ok := g.substEnv[name]
	return v, ok
}
