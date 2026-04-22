// closure_lift.go — pre-pass over the IR module that hoists no-capture
// `*ostyir.Closure` literals into top-level synthetic fn declarations
// so the legacy AST emitter never has to lower a `*ast.ClosureExpr`
// directly. Each lifted closure is replaced (during the IR→AST
// bridge) with a bare `*ast.Ident` referring to the synthesized fn.
// The existing `emitIdent` path then materialises the closure as a
// fn-value env via `g.emitFnValueEnv(sig)` (Phase 1, see fn_value.go),
// reusing the same call ABI as bare top-level fn references.
//
// Scope (intentionally narrow):
//   - Only closures with `len(c.Captures) == 0` are lifted. Captures
//     mean the body reads names from the enclosing scope; lifting
//     those requires a Phase 4 capture env layout (see MIR's
//     `emitClosureEnv`) which this PR doesn't tackle. Closures with
//     captures are left as `*ast.ClosureExpr` for the existing
//     LLVM013 wall to handle (with a clearer error message — they
//     can't be inlined as testing.context/benchmark are).
//   - Lifted fns are appended to `file.Decls`. Their names follow
//     `__osty_closure_<id>` with a per-bridge counter so two
//     successive `legacyFileFromModule` calls don't collide.
//
// The lift table is keyed by `*ostyir.Closure` pointer and consumed
// once during `legacyClosureFromIR`. After bridge conversion finishes
// the table is cleared (mirroring the
// `currentSpecializedBuiltinSurfaces` pattern at the top of
// ir_module.go).
package llvmgen

import (
	"fmt"

	"github.com/osty/osty/internal/ast"
	ostyir "github.com/osty/osty/internal/ir"
)

// liftedClosure is the per-closure record produced by the lift pass.
// Holds the synthesized name and the lowered AST FnDecl that the
// bridge will append to the file. The decl is built once during the
// pre-pass so subsequent `legacyClosureFromIR` lookups are O(1).
type liftedClosure struct {
	name string
	decl *ast.FnDecl
}

// currentLiftedClosures maps each lifted IR Closure pointer to its
// synthesized name + AST FnDecl. Set by `liftClosuresFromModule` at
// the start of each bridge conversion and consulted by
// `legacyClosureFromIR`. Reset by `GenerateModule` after the AST
// emitter consumes the side channel — same lifecycle as
// `currentSpecializedBuiltinSurfaces`.
var currentLiftedClosures map[*ostyir.Closure]*liftedClosure

// closureLiftCounter is a process-monotonic counter for synthesized
// closure fn names. A monotonic counter (rather than per-module
// reset) keeps names unique across the entire build so a future
// caller that compiles multiple files in one process never sees a
// name collision.
var closureLiftCounter int

// liftClosuresFromModule walks `mod` and assigns a synthesized
// top-level fn name to every `*ostyir.Closure` with no captures.
// Returns a slice of synthesized AST FnDecls in lift order so the
// caller can prepend them to the bridged file's Decls.
//
// Closures with captures are skipped — they fall through to the
// existing LLVM013 wall. A future capture-aware lifter can extend
// this same map with a richer record (env struct layout etc.).
func liftClosuresFromModule(mod *ostyir.Module) []ast.Decl {
	currentLiftedClosures = map[*ostyir.Closure]*liftedClosure{}
	if mod == nil {
		return nil
	}
	var lifted []ast.Decl
	ostyir.Walk(ostyir.VisitorFunc(func(n ostyir.Node) bool {
		c, ok := n.(*ostyir.Closure)
		if !ok || c == nil {
			return true
		}
		if len(c.Captures) != 0 {
			// Capture-bearing closures are out of scope for this PR.
			// Leave them in place; the bridge returns the original
			// ClosureExpr and the legacy emitter walls.
			return true
		}
		fnDecl, err := buildLiftedClosureFnDecl(c)
		if err != nil || fnDecl == nil {
			// Best-effort: if we can't build the lifted fn cleanly
			// (e.g. an unexpected param shape), skip it and let the
			// downstream emitter wall the way it would have without
			// lifting.
			return true
		}
		closureLiftCounter++
		fnDecl.Name = fmt.Sprintf("__osty_closure_%d", closureLiftCounter)
		currentLiftedClosures[c] = &liftedClosure{name: fnDecl.Name, decl: fnDecl}
		lifted = append(lifted, fnDecl)
		return true
	}), mod)
	return lifted
}

// buildLiftedClosureFnDecl converts an IR Closure into an AST FnDecl
// suitable for the legacy emitter. The closure's params, return
// type, and body block are translated through the existing
// IR→AST bridge helpers. The result has empty Annotations and
// Pub=false — lifted closures are private to the compilation unit.
//
// Returns (nil, nil) when the IR shape isn't lift-friendly (e.g. an
// unnamed pattern param). The caller should skip silently in that
// case so the legacy emitter retains the original ClosureExpr and
// surfaces the regular wall.
func buildLiftedClosureFnDecl(c *ostyir.Closure) (*ast.FnDecl, error) {
	if c == nil {
		return nil, nil
	}
	start, end := legacySpan(c.At())
	out := &ast.FnDecl{
		PosV:       start,
		EndV:       end,
		ReturnType: legacyTypeFromIR(c.Return),
	}
	for _, p := range c.Params {
		if p == nil {
			return nil, nil
		}
		// Lifted closures need explicit param types — the AST FnDecl
		// is consumed by `collectDeclarations` and the type-driven
		// signature builder which both fail on a nil Type. Inline
		// closures with inferred-only param types now backfill in
		// `lowerClosure` (see lower.go), so by the time we land
		// here every lift candidate has a concrete IR Type.
		if p.Type == nil {
			return nil, nil
		}
		legacyParam, err := legacyParamFromIR(p)
		if err != nil {
			return nil, err
		}
		out.Params = append(out.Params, legacyParam)
	}
	body, err := legacyBlockFromIR(c.Body)
	if err != nil {
		return nil, err
	}
	out.Body = body
	return out, nil
}

// liftedClosureFor returns the lifted record for `c` if the pre-pass
// scheduled it, or nil otherwise. Called by `legacyClosureFromIR`
// to decide between returning a bare Ident reference (lifted) or
// the original ClosureExpr (not lifted — bound for the existing
// wall).
func liftedClosureFor(c *ostyir.Closure) *liftedClosure {
	if currentLiftedClosures == nil || c == nil {
		return nil
	}
	return currentLiftedClosures[c]
}
