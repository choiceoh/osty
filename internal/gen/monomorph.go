package gen

import (
	"sort"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/resolve"
)

// Generic monomorphization (§2.7.3).
//
// The checker records every generic call site's concrete type-argument
// list on Result.Instantiations. This file consumes that map to emit
// one specialized copy of each generic function definition per distinct
// instantiation. Name mangling follows `fn_T1_T2` where each Ti is the
// Go type rendering with identifier-unsafe characters flattened.
//
// Scope (Phase-bounded): top-level fn declarations only. Generic
// struct / enum types and generic methods still round-trip through
// the Go-native generics path — the spec permits this because
// monomorphization is observable only via emitted binary size, not
// behavior, and tightening those cases is a separate, larger refactor.

// instKey is the canonical textual key for one instantiation — a
// tab-joined concatenation of the Go type strings. Using a tab avoids
// clashing with the `_` used by the mangled name.
type instKey string

// instRecord captures one specialized instantiation of a generic fn.
type instRecord struct {
	// Key is the canonical instKey — identifies the instantiation in
	// the genericInstances map.
	Key instKey
	// GoTypes is the Go rendering of each type argument, used both to
	// mangle the specialized name and to populate the substitution
	// environment when emitting the body.
	GoTypes []string
	// Mangled is the Go function name the specialized copy is emitted
	// under, and the name every call site rewrites to.
	Mangled string
}

// buildInstances scans chk.Instantiations and groups the concrete
// type-argument lists by the generic fn symbol they instantiate. The
// result map is keyed by the fn's resolver Symbol; each value is a
// deterministically-ordered slice of unique instantiations.
//
// Call sites that don't resolve to a user-defined fn symbol (variant
// constructors, method calls through a value, builtins, etc.) are
// skipped — those either have no generic body to specialize or ride
// through a separate lowering path.
func (g *gen) buildInstances() {
	g.genericInstances = map[*resolve.Symbol][]instRecord{}
	g.callMono = map[*ast.CallExpr]string{}
	if g.chk == nil || g.res == nil {
		return
	}

	for call, args := range g.chk.Instantiations {
		sym := g.calleeSymbol(call.Fn)
		if sym == nil || sym.Kind != resolve.SymFn {
			continue
		}
		fn, ok := sym.Decl.(*ast.FnDecl)
		if !ok || fn == nil || len(fn.Generics) == 0 {
			continue
		}
		if len(args) != len(fn.Generics) {
			// Checker produced a shape that doesn't line up with the
			// declaration — skip rather than emit broken code.
			continue
		}

		goTypes := make([]string, len(args))
		parts := make([]string, len(args))
		for i, a := range args {
			goTypes[i] = g.goType(a)
			parts[i] = goTypes[i]
		}
		key := instKey(strings.Join(parts, "\t"))

		rec := instRecord{
			Key:     key,
			GoTypes: goTypes,
			Mangled: mangleInst(fn.Name, goTypes),
		}

		existing := g.genericInstances[sym]
		found := false
		for _, r := range existing {
			if r.Key == key {
				rec.Mangled = r.Mangled
				found = true
				break
			}
		}
		if !found {
			g.genericInstances[sym] = append(existing, rec)
		}
		g.callMono[call] = rec.Mangled
	}

	// Stable output order so repeated runs produce identical source.
	for sym, recs := range g.genericInstances {
		sort.SliceStable(recs, func(i, j int) bool {
			return recs[i].Mangled < recs[j].Mangled
		})
		g.genericInstances[sym] = recs
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

// mangleInst builds the specialized fn name. Illegal identifier
// characters in a Go type rendering (`[`, `]`, `.`, space, `*`) are
// flattened so `id[[]int]` becomes `id_SliceInt`-ish — we keep it simple
// and replace with `_` / drop — the goal is only uniqueness per type
// tuple within one file, not human readability.
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
// identifier fragment. The resulting fragment loses structural
// information (we can't distinguish `[]int` from `[]int8` … wait, we
// keep digits, just drop brackets). Distinctness is preserved because
// mapping is injective within the set of types Osty currently emits:
// brackets → `Of`, `*` → `Ptr`, `.` → `_`, space → ``, anything else
// non-alphanumeric → `_`.
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

// isGenericFnSym reports whether `sym` is a user-declared generic
// function — the set whose calls need name rewriting.
func (g *gen) isGenericFnSym(sym *resolve.Symbol) bool {
	if sym == nil || sym.Kind != resolve.SymFn {
		return false
	}
	fn, ok := sym.Decl.(*ast.FnDecl)
	return ok && fn != nil && len(fn.Generics) > 0
}

// emitMonomorphizedFn emits every specialized copy of a generic fn.
// Each copy substitutes the generic parameters with their concrete
// types via the pushed substEnv — the rest of the emit path is
// unchanged because goTypeExpr / goType now consult substEnv.
func (g *gen) emitMonomorphizedFn(fn *ast.FnDecl, recs []instRecord) {
	for _, rec := range recs {
		cleanup := g.pushSubst(fn.Generics, rec.GoTypes)
		g.emitFnDeclBody(fn, rec.Mangled)
		cleanup()
	}
}

