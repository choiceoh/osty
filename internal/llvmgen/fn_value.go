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
	"strings"

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

// emitClosureMakerCall recognises calls to the synthesized
// `__osty_make_closure_<n>` markers the IR→AST bridge stamps in
// place of capturing-closure literals (see closure_lift.go) and
// materialises the closure env:
//
//   - register a Phase 4 capturing thunk for the lifted fn that
//     loads each capture from env at runtime and reorders args
//     before calling the lifted fn
//   - allocate env via `osty.rt.closure_env_alloc_v1(N, site, thunk)`;
//     the helper writes the supplied symbol at slot 0
//   - eval each capture in the call site's lexical scope and
//     store the result at env's capture slot i+1
//
// The resulting value is a `ptr` tagged with a synthesised fnSig
// covering only the closure's *original* params (not the captures
// the lifted fn appends), so downstream `emitIndirectUserCall`
// passes the right argument count when the user later calls the
// fn-value.
//
// Returns (_, false, _) when the call's name doesn't match a
// registered maker — the dispatcher falls through to the regular
// fn-value or direct-call paths.
func (g *generator) emitClosureMakerCall(call *ast.CallExpr) (value, bool, error) {
	if call == nil {
		return value{}, false, nil
	}
	id, ok := call.Fn.(*ast.Ident)
	if !ok || id == nil || !strings.HasPrefix(id.Name, closureMakerNamePrefix) {
		return value{}, false, nil
	}
	rec := liftedClosureByMaker(id.Name)
	if rec == nil {
		return value{}, true, unsupportedf("call", "unknown closure maker %q", id.Name)
	}
	if len(call.Args) != len(rec.captures) {
		return value{}, true, unsupportedf("call", "closure maker %q expects %d capture(s), got %d", id.Name, len(rec.captures), len(call.Args))
	}
	sig, ok := g.functions[rec.name]
	if !ok || sig == nil {
		return value{}, true, unsupportedf("call", "lifted closure %q missing fnSig", rec.name)
	}
	// origParamCount = len(sig.params) - len(captures). The lifted
	// fn appends each capture as an extra param (see
	// buildLiftedClosureFnDecl), so the original closure params
	// occupy the prefix.
	origParamCount := len(sig.params) - len(rec.captures)
	if origParamCount < 0 {
		return value{}, true, unsupportedf("call", "lifted closure %q sig param count mismatch", rec.name)
	}
	// The fn-value the user's code sees only takes the original
	// closure params — the thunk hides the capture extraction. Tag
	// the env value with a sig that exposes only that prefix so
	// indirect-call arg counts match.
	thunkSig := &fnSig{
		ret:              sig.ret,
		params:           append([]paramInfo(nil), sig.params[:origParamCount]...),
		retListElemTyp:   sig.retListElemTyp,
		retListString:    sig.retListString,
		retMapKeyTyp:     sig.retMapKeyTyp,
		retMapValueTyp:   sig.retMapValueTyp,
		retMapKeyString:  sig.retMapKeyString,
		retSetElemTyp:    sig.retSetElemTyp,
		retSetElemString: sig.retSetElemString,
		returnSourceType: sig.returnSourceType,
	}
	captureVals := make([]value, len(rec.captures))
	for i, cap := range rec.captures {
		raw, err := g.emitExpr(call.Args[i].Value)
		if err != nil {
			return value{}, true, err
		}
		loaded, err := g.loadIfPointer(raw)
		if err != nil {
			return value{}, true, err
		}
		if loaded.typ != cap.llvmTyp {
			return value{}, true, unsupportedf("type-system", "closure capture %q expected %s, got %s", cap.name, cap.llvmTyp, loaded.typ)
		}
		captureVals[i] = loaded
	}
	thunkSym := g.ensureCapturingClosureThunk(rec, origParamCount, sig)
	g.declareRuntimeSymbol("osty.rt.closure_env_alloc_v1", "ptr", []paramInfo{
		{typ: "i64"},
		{typ: "ptr"},
	})
	emitter := g.toOstyEmitter()
	site := llvmStringLiteral(emitter, "runtime.closure.env.captures")
	env := llvmEmitClosureEnvAllocRuntime(emitter, len(rec.captures), site.name, thunkSym)
	g.takeOstyEmitter(emitter)
	g.needsGCRuntime = true
	// Store each capture at offset 16 + i*8. Header is
	// `{ ptr fn, i64 cap_count }` (16 bytes); captures payload
	// follows. The 8-byte stride keeps slot indexing uniform across
	// scalar widths.
	emitter = g.toOstyEmitter()
	for i, cv := range captureVals {
		offset := 16 + i*8
		slotPtr := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = getelementptr i8, ptr %s, i64 %d", slotPtr, env.name, offset))
		emitter.body = append(emitter.body, fmt.Sprintf("  store %s %s, ptr %s", cv.typ, cv.ref, slotPtr))
	}
	g.takeOstyEmitter(emitter)
	return value{
		typ:       "ptr",
		ref:       env.name,
		fnSigRef:  thunkSig,
		gcManaged: true,
	}, true, nil
}

// ensureCapturingClosureThunk registers a Phase 4 capturing thunk
// for the lifted fn `sig.irName` and returns its symbol name.
//
//	define private RET @__osty_closure_thunk_<sym>(ptr %env, P0 %arg0, ...) {
//	  %cap0_slot = getelementptr i8, ptr %env, i64 16
//	  %cap0 = load <CT0>, ptr %cap0_slot
//	  ...
//	  (%ret = )? call RET @<sym>(P0 %arg0, ..., <CT0> %cap0, ...)
//	  ret RET %ret
//	}
//
// Memoised on `g.fnValueThunkDefs` keyed by the same
// `__osty_closure_thunk_<symbol>` name Phase 1 uses, so the two
// thunk shapes share the storage map but never collide (different
// lifted fns ⇒ different symbols).
func (g *generator) ensureCapturingClosureThunk(rec *liftedClosure, origParamCount int, sig *fnSig) string {
	symbol := llvmClosureThunkName(sig.irName)
	if g.fnValueThunkDefs == nil {
		g.fnValueThunkDefs = map[string]string{}
	}
	if _, ok := g.fnValueThunkDefs[symbol]; ok {
		return symbol
	}
	origParamTypes := make([]string, origParamCount)
	for i := 0; i < origParamCount; i++ {
		origParamTypes[i] = llvmParamIRType(sig.params[i])
	}
	captureTypes := make([]string, 0, len(rec.captures))
	for _, cap := range rec.captures {
		captureTypes = append(captureTypes, cap.llvmTyp)
	}
	g.fnValueThunkDefs[symbol] = llvmCapturingClosureThunkDefinition(
		sig.irName, sig.ret, origParamTypes, captureTypes,
	)
	g.fnValueThunkOrder = append(g.fnValueThunkOrder, symbol)
	return symbol
}

// llvmCapturingClosureThunkDefinition emits the LLVM IR for a
// Phase 4 capturing thunk. See ensureCapturingClosureThunk for the
// shape and call ABI.
func llvmCapturingClosureThunkDefinition(symbol string, returnType string, origParamTypes []string, captureTypes []string) string {
	ret := returnType
	if ret == "" {
		ret = "void"
	}
	headerParts := []string{"ptr %env"}
	origArgParts := make([]string, 0, len(origParamTypes))
	for i, p := range origParamTypes {
		headerParts = append(headerParts, fmt.Sprintf("%s %%arg%d", p, i))
		origArgParts = append(origArgParts, fmt.Sprintf("%s %%arg%d", p, i))
	}
	header := strings.Join(headerParts, ", ")
	thunk := llvmClosureThunkName(symbol)
	lines := []string{
		fmt.Sprintf("define private %s @%s(%s) {", ret, thunk, header),
		"entry:",
	}
	captureArgParts := make([]string, 0, len(captureTypes))
	for i, ct := range captureTypes {
		offset := 16 + i*8
		slotName := fmt.Sprintf("%%cap%d_slot", i)
		valName := fmt.Sprintf("%%cap%d", i)
		lines = append(lines, fmt.Sprintf("  %s = getelementptr i8, ptr %%env, i64 %d", slotName, offset))
		lines = append(lines, fmt.Sprintf("  %s = load %s, ptr %s", valName, ct, slotName))
		captureArgParts = append(captureArgParts, fmt.Sprintf("%s %s", ct, valName))
	}
	allArgs := append(origArgParts, captureArgParts...)
	callArgs := strings.Join(allArgs, ", ")
	if ret == "void" {
		lines = append(lines, fmt.Sprintf("  call void @%s(%s)", symbol, callArgs))
		lines = append(lines, "  ret void")
	} else {
		lines = append(lines, fmt.Sprintf("  %%ret = call %s @%s(%s)", ret, symbol, callArgs))
		lines = append(lines, fmt.Sprintf("  ret %s %%ret", ret))
	}
	lines = append(lines, "}")
	return strings.Join(lines, "\n")
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
	g.emitCallSafepointIfNeeded(emitter)
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
