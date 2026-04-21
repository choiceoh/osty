// fn_value.go — first-class function value lowering for the legacy AST
// emitter. Mirrors the MIR closure ABI so fn values have a single
// uniform representation regardless of which emitter produced them:
//
//	fn value  ≡  `ptr` to env struct
//	env       ≡  { ptr fn_or_thunk, cap0, cap1, ... }
//	call      ≡  `load ptr from env[0]; call ret (ptr, P...) %fn(env, args)`
//
// Top-level fn references (no captures) are wrapped in a thunk that
// takes env as an implicit first arg and delegates to the real symbol.
// Captured closures will fill in `Fields[1..]` in a later phase and
// reuse the same call-site shape.
package llvmgen

import (
	"fmt"

	"github.com/osty/osty/internal/ast"
)

// closureThunkParamTypes extracts the LLVM IR types of a fnSig's
// parameters in source order. The thunk consumes these verbatim:
// `ptr %env` plus `P_i %arg_i` for each original param.
func closureThunkParamTypes(sig *fnSig) []string {
	out := make([]string, 0, len(sig.params))
	for _, p := range sig.params {
		out = append(out, llvmParamIRType(p))
	}
	return out
}

// ensureFnValueThunk registers a closure thunk for `sig` if not yet
// seen and returns its LLVM symbol name. The thunk is
// `define private <ret> @__osty_closure_thunk_<sym>(ptr %env, P0, P1, ...)`
// whose body discards env and forwards to the original symbol.
//
// The thunk IR is accumulated on the generator and flushed at module
// render time — see generator.render().
func (g *generator) ensureFnValueThunk(sig *fnSig) string {
	symbol := llvmClosureThunkName(sig.irName)
	if g.fnValueThunkDefs == nil {
		g.fnValueThunkDefs = map[string]string{}
	}
	if _, ok := g.fnValueThunkDefs[symbol]; ok {
		return symbol
	}
	g.fnValueThunkDefs[symbol] = llvmClosureThunkDefinition(
		sig.irName, sig.ret, closureThunkParamTypes(sig),
	)
	g.fnValueThunkOrder = append(g.fnValueThunkOrder, symbol)
	return symbol
}

// fnValuePhase1CaptureCount is the capture count the current lowering
// emits for every closure env. Phase 1 closures have no captures yet;
// Phase 4 will drive this from the closure AST. The constant lives at
// this boundary so the call-site change is one-line when captures
// land.
var fnValuePhase1CaptureCount = llvmClosureEnvPhase1CaptureCount()

// emitFnValueEnv materialises a closure env holding the thunk pointer
// for the given top-level fn. Returns a `ptr`-typed value tagged with
// fnSigRef so a subsequent call site can do indirect dispatch with
// the correct signature.
//
// Allocation goes through `osty.rt.closure_env_alloc_v1` (Phase A4
// dedicated entry, RUNTIME_GC_DELTA §2.4). That helper attaches the
// capture-tracing callback at construction so any managed pointers
// placed in capture slots by a Phase 4 lowering are immediately
// reachable from GC mark without revisiting the emit site.
//
// The env layout is `{ ptr fn, i64 capture_count, ptr captures[] }`.
// `fn` at offset 0 is unchanged from the Phase 1 ABI, so existing
// thunk call sites (`load ptr, ptr %env`) continue to work. For
// Phase 1 the capture count is zero — the bulge only grows when the
// capture-aware lowering lands.
//
// Callers must already be inside a function — there is no
// module-level fn-value literal path (globals would need a separate
// constant initialiser ABI; deferred until a user-visible need
// appears).
func (g *generator) emitFnValueEnv(sig *fnSig) (value, error) {
	if sig == nil {
		return value{}, unsupportedf("call", "fn-value env for nil signature")
	}
	thunk := g.ensureFnValueThunk(sig)
	g.declareRuntimeSymbol("osty.rt.closure_env_alloc_v1", "ptr", []paramInfo{
		{typ: "i64"},
		{typ: "ptr"},
	})
	emitter := g.toOstyEmitter()
	site := llvmStringLiteral(emitter, "runtime.closure.env.ptr")
	env := llvmEmitClosureEnvAllocRuntime(emitter, fnValuePhase1CaptureCount, site.name, thunk)
	g.takeOstyEmitter(emitter)
	g.needsGCRuntime = true
	return value{
		typ:       "ptr",
		ref:       env.name,
		fnSigRef:  sig,
		gcManaged: true,
	}, nil
}

// emitFnValueIndirectCall lowers a call through a fn-value env.
// `envVal` is the env pointer (already loaded if it came from a
// local slot); `sig` is the fn's signature inherited from the
// original top-level declaration; `args` are the user arguments
// already lowered.
//
// Emitted sequence:
//
//	%fn = load ptr, ptr %env
//	(%result = )? call <ret> (ptr, P...) %fn(ptr %env, args...)
//
// Returns the call's LLVM result value (or an empty value when the
// fn has no return).
func (g *generator) emitFnValueIndirectCall(envVal value, sig *fnSig, args []*LlvmValue) (value, error) {
	if sig == nil {
		return value{}, unsupportedf("call", "indirect call with nil signature")
	}
	if envVal.typ != "ptr" {
		return value{}, unsupportedf("call", "indirect call needs ptr env, got %s", envVal.typ)
	}
	retLLVM := sig.ret
	if retLLVM == "" {
		retLLVM = "void"
	}

	emitter := g.toOstyEmitter()
	outValue := llvmFnValueCallIndirect(
		emitter,
		retLLVM,
		&LlvmValue{typ: "ptr", name: envVal.ref, pointer: false},
		args,
	)
	g.takeOstyEmitter(emitter)
	if outValue.typ == "void" {
		return value{}, nil
	}
	out := value{typ: outValue.typ, ref: outValue.name}
	out.listElemTyp = sig.retListElemTyp
	out.listElemString = sig.retListString
	out.mapKeyTyp = sig.retMapKeyTyp
	out.mapValueTyp = sig.retMapValueTyp
	out.mapKeyString = sig.retMapKeyString
	out.setElemTyp = sig.retSetElemTyp
	out.setElemString = sig.retSetElemString
	out.sourceType = sig.returnSourceType
	out.gcManaged = valueNeedsManagedRoot(out)
	out.rootPaths = g.rootPathsForType(out.typ)
	return out, nil
}

// synthFnSigFromFnType builds a call-site-only *fnSig from an
// `ast.FnType`. Used when a fn value arrives through a parameter
// slot or a struct field — the original *fnSig from the defining
// decl is out of reach, so we reconstruct just what the
// indirect-call emitter reads: return type, param types, and
// source-type metadata.
//
// Fields not populated here (name, irName, receiverType, decl)
// intentionally stay empty — the indirect-call path never touches
// them. A future site that needs direct-call dispatch through a
// synthesised sig must fill those in then.
func synthFnSigFromFnType(ft *ast.FnType, env typeEnv) (*fnSig, error) {
	if ft == nil {
		return nil, unsupportedf("type-system", "nil fn type")
	}
	params := make([]paramInfo, 0, len(ft.Params))
	for i, pt := range ft.Params {
		if pt == nil {
			return nil, unsupportedf("type-system", "fn-type param %d missing type", i)
		}
		typ, err := llvmType(pt, env)
		if err != nil {
			return nil, err
		}
		meta, err := containerMetadataFromSourceType(pt, env)
		if err != nil {
			return nil, err
		}
		info := paramInfo{
			name:  fmt.Sprintf("arg%d", i),
			typ:   typ,
			irTyp: typ,
		}
		meta.applyToParam(&info)
		params = append(params, info)
	}
	retLLVM := "void"
	if ft.ReturnType != nil {
		typ, err := llvmType(ft.ReturnType, env)
		if err != nil {
			return nil, err
		}
		retLLVM = typ
	}
	sig := &fnSig{
		ret:    retLLVM,
		params: params,
	}
	retMeta, err := containerMetadataFromSourceType(ft.ReturnType, env)
	if err != nil {
		return nil, err
	}
	retMeta.applyToReturn(sig)
	return sig, nil
}

func synthFnSigFromSourceType(sourceType ast.Type, env typeEnv) (*fnSig, bool, error) {
	ft, ok := sourceType.(*ast.FnType)
	if !ok {
		return nil, false, nil
	}
	sig, err := synthFnSigFromFnType(ft, env)
	if err != nil {
		return nil, true, err
	}
	return sig, true, nil
}

func fnValueSignature(v value) (*fnSig, bool) {
	if v.typ != "ptr" || v.fnSigRef == nil {
		return nil, false
	}
	return v.fnSigRef, true
}

func requireFnValueSignature(v value, label string) (*fnSig, error) {
	sig, ok := fnValueSignature(v)
	if !ok {
		return nil, unsupportedf("call", "%s must be a fn value (got typ=%s, sig=%v)", label, v.typ, v.fnSigRef != nil)
	}
	return sig, nil
}

// emitIndirectUserCall attempts to dispatch `call` as an indirect call
// through a first-class fn-value. Returns found=true when it
// recognised the callee shape and produced (or failed to produce) a
// call; found=false lets the main dispatcher fall through to its
// LLVM015 diagnostic.
//
// Recognised callee shapes:
//
//   - Bare `*ast.Ident` bound to a local/global whose value carries
//     fnSigRef (phase 1 for top-level fn refs, phase 3 for fn-typed
//     params — both tag their binding at bind time).
//   - `*ast.FieldExpr` on a struct whose field's source type is an
//     `*ast.FnType`. The field value extracts to a ptr env; the sig
//     is reconstructed on-the-fly from the field's source type.
func (g *generator) emitIndirectUserCall(call *ast.CallExpr) (value, bool, error) {
	if call == nil {
		return value{}, false, nil
	}
	envVal, sig, found, err := g.indirectCallCallee(call)
	if err != nil || !found {
		return value{}, found, err
	}
	args, err := g.userCallArgs(sig, nil, call)
	if err != nil {
		return value{}, true, err
	}
	// Safepoint + scope handling mirrors the direct-call path so GC
	// roots coming out of arg evaluation are released at the same
	// cadence.
	emitter := g.toOstyEmitter()
	g.emitGCSafepointKind(emitter, safepointKindCall)
	g.takeOstyEmitter(emitter)
	g.pushScope()
	ret, callErr := g.emitFnValueIndirectCall(envVal, sig, args)
	g.popScope()
	if callErr != nil {
		return value{}, true, callErr
	}
	return ret, true, nil
}

// indirectCallCallee resolves the callee side of an indirect call.
// Returns (envVal, sig, true) on match; (_, _, false) otherwise so
// the caller falls through to the direct-call diagnostic.
func (g *generator) indirectCallCallee(call *ast.CallExpr) (value, *fnSig, bool, error) {
	switch fn := call.Fn.(type) {
	case *ast.Ident:
		return g.indirectIdentCallCallee(fn)
	case *ast.FieldExpr:
		return g.indirectFieldCallCallee(fn)
	}
	return value{}, nil, false, nil
}

func (g *generator) indirectIdentCallCallee(fn *ast.Ident) (value, *fnSig, bool, error) {
	if fn == nil {
		return value{}, nil, false, nil
	}
	slot, ok := g.lookupBinding(fn.Name)
	if !ok {
		return value{}, nil, false, nil
	}
	sig, ok := fnValueSignature(slot)
	if !ok {
		return value{}, nil, false, nil
	}
	envVal, err := g.loadIfPointer(slot)
	if err != nil {
		return value{}, nil, true, err
	}
	return envVal, sig, true, nil
}

func (g *generator) indirectFieldCallCallee(fn *ast.FieldExpr) (value, *fnSig, bool, error) {
	if fn == nil || fn.IsOptional {
		return value{}, nil, false, nil
	}
	// Only attempt this path when the receiver's static type is a
	// known struct and the field's source type is an FnType.
	// Everything else (method calls, optional chains, module aliases)
	// remains owned by the upstream hooks.
	baseInfo, ok := g.staticExprInfo(fn.X)
	if !ok {
		return value{}, nil, false, nil
	}
	structInfo := g.structsByType[baseInfo.typ]
	if structInfo == nil {
		return value{}, nil, false, nil
	}
	field, ok := structInfo.byName[fn.Name]
	if !ok {
		return value{}, nil, false, nil
	}
	sig, ok, err := synthFnSigFromSourceType(field.sourceType, g.typeEnv())
	if err != nil {
		return value{}, nil, true, err
	}
	if !ok {
		return value{}, nil, false, nil
	}
	envVal, err := g.emitFieldExpr(fn)
	if err != nil {
		return value{}, nil, true, err
	}
	if envVal.typ != "ptr" {
		return value{}, nil, true, unsupportedf("type-system", "fn-typed field %q.%s expected ptr env, got %s", structInfo.name, fn.Name, envVal.typ)
	}
	return envVal, sig, true, nil
}
