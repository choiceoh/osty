// closure_lift.go — pre-pass over the IR module that hoists
// `*ostyir.Closure` literals into top-level synthetic fn declarations
// so the legacy AST emitter never has to lower a `*ast.ClosureExpr`
// directly.
//
// Two lift shapes share the same infrastructure:
//
//   - **No-capture closures** (`Captures == 0`): replaced at the
//     bridge with a bare `*ast.Ident` referring to the lifted fn.
//     The existing `emitIdent` path materialises the closure as a
//     fn-value env via `g.emitFnValueEnv(sig)` (Phase 1 thunk in
//     fn_value.go), reusing the same call ABI as bare top-level fn
//     references.
//
//   - **Capturing closures** (`Captures != 0`): the lifted fn takes
//     `__env: ptr` as its first parameter; the env at runtime holds
//     the lifted-fn pointer at slot 0 followed by capture values at
//     slots 1..N. The bridge replaces the closure literal with a
//     synthesized `CallExpr` to a marker name `__osty_make_closure_<n>`
//     whose args are bare Idents of the captured names. The call site
//     emitter (`emitClosureMakerCall` in fn_value.go) recognises the
//     marker, allocates the env, and stores the lifted fn pointer +
//     evaluated captures. Inside the lifted fn body, captures are
//     pre-bound by `applyClosureCaptureBindings` after the parameter
//     binding loop in `emitUserFunction`.
//
// Capture support covers scalar IR primitives (Int / Bool / Char /
// Byte / Float and their typed cousins) that fit in a single machine
// word, plus managed pointer-typed captures (String / Bytes / List /
// Map / Set) that lower to `ptr`. The runtime side is already set up
// for this: `osty.rt.closure_env_alloc_v1` installs
// `osty_rt_closure_env_trace`, which walks every capture slot via
// `osty_gc_mark_slot_v1` — that helper filters through `find_header`,
// so scalar bit patterns in a slot are safely skipped and real managed
// pointers get marked. User struct captures and other ptr-by-value
// shapes still fall through to the existing LLVM013 wall.
package llvmgen

import (
	"fmt"

	"github.com/osty/osty/internal/ast"
	ostyir "github.com/osty/osty/internal/ir"
)

// closureCapture is one captured variable's metadata, in env slot
// order. The slot index is `pos+1` (slot 0 is the lifted fn ptr).
//
// `name` is the variable's source name in the closure body — the
// emitter binds the loaded value under this name so references
// inside the body resolve naturally.
//
// `llvmTyp` is the LLVM IR type for the load (e.g. "i64" / "i32" /
// "i1" / "double"). Stored here rather than reconstructed from the
// AST type so the emitter doesn't need to re-walk the type lookup
// at body-emit time.
//
// `astTyp` is the AST type the lifted-fn synthesizer needs when
// adding the capture as an additional fn parameter (the no-thunk
// design carries captures via env, but we still record astTyp for
// type checking + diagnostic clarity).
type closureCapture struct {
	name    string
	llvmTyp string
	astTyp  ast.Type
}

// liftedClosure is the per-closure record produced by the lift pass.
// `decl` is the synthesized AST fn (with `__env` prepended for
// capturing closures); `captures` is empty for no-capture closures
// and matches env slots 1..N otherwise; `makerName` is the
// synthesized marker name only present for capturing closures.
type liftedClosure struct {
	name      string
	decl      *ast.FnDecl
	captures  []closureCapture
	makerName string
}

// currentLiftedClosures maps each lifted IR Closure pointer to its
// record. Set by `liftClosuresFromModule` and consumed by
// `legacyClosureFromIR`. Reset by `GenerateModule` after the AST
// emitter consumes the side channel — same lifecycle as
// `currentSpecializedBuiltinSurfaces`.
var currentLiftedClosures map[*ostyir.Closure]*liftedClosure

// currentLiftedClosuresByName mirrors currentLiftedClosures keyed by
// the synthesized lifted-fn name. Used by the body emitter
// (`liftedClosureCapturesByName`) to look up capture metadata when
// emitting the lifted fn — the bridge has long discarded the
// IR-pointer key by then.
var currentLiftedClosuresByName map[string]*liftedClosure

// currentLiftedClosuresByMaker mirrors currentLiftedClosures keyed by
// the synthesized maker call name. Used by `emitClosureMakerCall` to
// look up the env-construction info from a `__osty_make_closure_*`
// CallExpr.
var currentLiftedClosuresByMaker map[string]*liftedClosure

// closureLiftCounter is a process-monotonic counter for synthesized
// closure fn names. A monotonic counter (rather than per-module
// reset) keeps names unique across the entire build so a future
// caller that compiles multiple files in one process never sees a
// name collision.
var closureLiftCounter int

// closureMakerNamePrefix is the marker prefix the bridge stamps on
// the constructor call replacing a capturing closure literal. The
// emitter dispatcher (emitClosureMakerCall) matches on this prefix
// to route the call into env materialization.
const closureMakerNamePrefix = "__osty_make_closure_"

// liftClosuresFromModule walks `mod` and assigns a synthesized
// top-level fn name to every `*ostyir.Closure` it can lift.
// Returns a slice of synthesized AST FnDecls in lift order so the
// caller can prepend them to the bridged file's Decls.
//
// Closures with captures the lifter can't represent (e.g. ptr
// captures, or any unrecognised CaptureKind) are skipped — they
// fall through to the existing LLVM013 wall.
func liftClosuresFromModule(mod *ostyir.Module) []ast.Decl {
	currentLiftedClosures = map[*ostyir.Closure]*liftedClosure{}
	currentLiftedClosuresByName = map[string]*liftedClosure{}
	currentLiftedClosuresByMaker = map[string]*liftedClosure{}
	if mod == nil {
		return nil
	}
	var lifted []ast.Decl
	ostyir.Walk(ostyir.VisitorFunc(func(n ostyir.Node) bool {
		c, ok := n.(*ostyir.Closure)
		if !ok || c == nil {
			return true
		}
		captures, ok := captureSlotsFromIR(c.Captures)
		if !ok {
			// One or more captures is a shape we don't support yet.
			// Leave the closure intact; the existing LLVM013 wall
			// fires.
			return true
		}
		fnDecl, err := buildLiftedClosureFnDecl(c, captures)
		if err != nil || fnDecl == nil {
			return true
		}
		closureLiftCounter++
		fnName := fmt.Sprintf("__osty_closure_%d", closureLiftCounter)
		fnDecl.Name = fnName
		rec := &liftedClosure{
			name:     fnName,
			decl:     fnDecl,
			captures: captures,
		}
		if len(captures) > 0 {
			rec.makerName = fmt.Sprintf("%s%d", closureMakerNamePrefix, closureLiftCounter)
			currentLiftedClosuresByMaker[rec.makerName] = rec
		}
		currentLiftedClosures[c] = rec
		currentLiftedClosuresByName[fnName] = rec
		lifted = append(lifted, fnDecl)
		return true
	}), mod)
	return lifted
}

// captureSlotsFromIR projects an IR closure's Captures into the
// per-slot metadata the emitter needs. Returns ok=false when any
// capture is unsupported (Kind, type, or shape) — the caller treats
// that as a signal to skip the lift entirely.
//
// Supported shapes:
//   - Kind: CaptureLocal / CaptureParam (bare reads of names from
//     the enclosing fn's scope). CaptureGlobal / CaptureFn /
//     CaptureSelf still bail.
//   - LLVM type: scalar primitives (i64 / i1 / i32 / i8 / double /
//     float) or managed pointer types (String / Bytes / List / Map /
//     Set → ptr). User struct captures still bail.
func captureSlotsFromIR(captures []*ostyir.Capture) ([]closureCapture, bool) {
	if len(captures) == 0 {
		return nil, true
	}
	out := make([]closureCapture, 0, len(captures))
	for _, c := range captures {
		if c == nil || c.Name == "" {
			return nil, false
		}
		switch c.Kind {
		case ostyir.CaptureLocal, ostyir.CaptureParam:
		default:
			return nil, false
		}
		llvmTyp, ok := scalarLLVMTypeForIR(c.T)
		if !ok {
			llvmTyp, ok = managedPtrLLVMTypeForIR(c.T)
			if !ok {
				return nil, false
			}
		}
		astTyp := legacyTypeFromIR(c.T)
		if astTyp == nil {
			return nil, false
		}
		out = append(out, closureCapture{
			name:    c.Name,
			llvmTyp: llvmTyp,
			astTyp:  astTyp,
		})
	}
	return out, true
}

// managedPtrLLVMTypeForIR returns `"ptr"` for IR types the GC traces
// as managed pointers — String, Bytes, and the prelude containers
// List / Map / Set. The env slot stores the pointer verbatim; the
// runtime's `osty_rt_closure_env_trace` walks each slot through
// `mark_slot_v1`, which filters to real heap headers, so storing a
// managed payload pointer keeps the captured value alive while the
// env is reachable.
//
// User structs and other pointer-shaped aggregates return ok=false
// for now: they can appear by-value or by-pointer depending on where
// they're sourced, and the lifter can't safely assume the capture
// site loaded a canonical ptr. That distinction is the follow-up.
func managedPtrLLVMTypeForIR(t ostyir.Type) (string, bool) {
	switch typed := t.(type) {
	case *ostyir.PrimType:
		if typed == nil {
			return "", false
		}
		switch typed.Kind {
		case ostyir.PrimString, ostyir.PrimBytes:
			return "ptr", true
		}
	case *ostyir.NamedType:
		if typed == nil || !typed.Builtin {
			return "", false
		}
		switch typed.Name {
		case "List", "Map", "Set":
			return "ptr", true
		}
	}
	return "", false
}

// scalarLLVMTypeForIR returns the LLVM IR type string for a scalar
// IR primitive type, or ok=false for anything that doesn't fit in
// a single machine word (ptr, struct, etc.).
func scalarLLVMTypeForIR(t ostyir.Type) (string, bool) {
	prim, ok := t.(*ostyir.PrimType)
	if !ok || prim == nil {
		return "", false
	}
	switch prim.Kind {
	case ostyir.PrimInt, ostyir.PrimInt64, ostyir.PrimUInt64:
		return "i64", true
	case ostyir.PrimInt32, ostyir.PrimUInt32, ostyir.PrimChar:
		return "i32", true
	case ostyir.PrimInt16, ostyir.PrimUInt16:
		return "i16", true
	case ostyir.PrimInt8, ostyir.PrimUInt8, ostyir.PrimByte:
		return "i8", true
	case ostyir.PrimBool:
		return "i1", true
	case ostyir.PrimFloat, ostyir.PrimFloat64:
		return "double", true
	case ostyir.PrimFloat32:
		return "float", true
	}
	return "", false
}

// buildLiftedClosureFnDecl converts an IR Closure into an AST FnDecl
// suitable for the legacy emitter.
//
// For no-capture closures the signature mirrors the source: original
// closure params + return type. The existing fn-value Env path
// wraps it in a thunk (Phase 1).
//
// For capturing closures each capture is appended to the lifted
// fn's parameter list, so a closure `|n| n + outer` with `outer:
// Int` lifts to `fn __osty_closure_<id>(n: Int, outer: Int)`. The
// body's references to `outer` resolve naturally to that
// extra param — no special name-resolution path needed in the
// emitter. The capturing thunk (emitted alongside in
// `llvmCapturingClosureThunkDefinition`) reads each capture out
// of the env at runtime and reorders args before calling the
// lifted fn.
//
// Returns (nil, nil) when the IR shape isn't lift-friendly. The
// caller skips silently — the legacy emitter falls back to the
// LLVM013 wall.
func buildLiftedClosureFnDecl(c *ostyir.Closure, captures []closureCapture) (*ast.FnDecl, error) {
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
	// Append each capture as an additional named parameter. The
	// body's bare-Ident references (`outer`) bind naturally to
	// these params during the regular emitUserFunction param-bind
	// loop — no special closure-aware lookup needed.
	for _, cap := range captures {
		out.Params = append(out.Params, &ast.Param{
			PosV: start,
			EndV: start,
			Name: cap.name,
			Type: cap.astTyp,
		})
	}
	body, err := legacyBlockFromIR(c.Body)
	if err != nil {
		return nil, err
	}
	out.Body = body
	return out, nil
}

// liftedClosureFor returns the lifted record for `c` if the pre-pass
// scheduled it, or nil otherwise.
func liftedClosureFor(c *ostyir.Closure) *liftedClosure {
	if currentLiftedClosures == nil || c == nil {
		return nil
	}
	return currentLiftedClosures[c]
}

// liftedClosureByName returns the lifted record for the synthesized
// fn name. Used by the body emitter to look up capture metadata
// for pre-binding.
func liftedClosureByName(name string) *liftedClosure {
	if currentLiftedClosuresByName == nil || name == "" {
		return nil
	}
	return currentLiftedClosuresByName[name]
}

// liftedClosureByMaker returns the lifted record for a synthesized
// maker call name (`__osty_make_closure_<n>`). Used by the
// emitter's call dispatcher to route the marker call into env
// materialization.
func liftedClosureByMaker(name string) *liftedClosure {
	if currentLiftedClosuresByMaker == nil || name == "" {
		return nil
	}
	return currentLiftedClosuresByMaker[name]
}
