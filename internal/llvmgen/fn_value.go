// fn_value.go — first-class function value lowering for the legacy AST
// emitter. Mirrors the MIR closure ABI so fn values have a single
// uniform representation regardless of which emitter produced them:
//
//   fn value  ≡  `ptr` to env struct
//   env       ≡  { ptr fn_or_thunk, cap0, cap1, ... }
//   call      ≡  `load ptr from env[0]; call ret (ptr, P...) %fn(env, args)`
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

func fnValueThunkSymbol(realSymbol string) string {
	return "__osty_closure_thunk_" + realSymbol
}

// ensureFnValueThunk registers a closure thunk for `sig` if not yet
// seen and returns its LLVM symbol name. The thunk is
// `define private <ret> @__osty_closure_thunk_<sym>(ptr %env, P0, P1, ...)`
// whose body discards env and forwards to the original symbol.
//
// The thunk IR is accumulated on the generator and flushed at module
// render time — see generator.render().
func (g *generator) ensureFnValueThunk(sig *fnSig) string {
	symbol := fnValueThunkSymbol(sig.irName)
	if g.fnValueThunkDefs == nil {
		g.fnValueThunkDefs = map[string]string{}
	}
	if _, ok := g.fnValueThunkDefs[symbol]; ok {
		return symbol
	}
	retLLVM := sig.ret
	if retLLVM == "" {
		retLLVM = "void"
	}
	// Build param list for the thunk: `ptr %env, <P0> %arg0, ...`.
	paramParts := make([]string, 0, 1+len(sig.params))
	paramParts = append(paramParts, "ptr %env")
	argParts := make([]string, 0, len(sig.params))
	for i, p := range sig.params {
		pLLVM := llvmParamIRType(p)
		paramParts = append(paramParts, fmt.Sprintf("%s %%arg%d", pLLVM, i))
		argParts = append(argParts, fmt.Sprintf("%s %%arg%d", pLLVM, i))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "define private %s @%s(%s) {\n",
		retLLVM, symbol, strings.Join(paramParts, ", "))
	b.WriteString("entry:\n")
	if retLLVM == "void" {
		fmt.Fprintf(&b, "  call void @%s(%s)\n  ret void\n",
			sig.irName, strings.Join(argParts, ", "))
	} else {
		fmt.Fprintf(&b, "  %%__ret = call %s @%s(%s)\n  ret %s %%__ret\n",
			retLLVM, sig.irName, strings.Join(argParts, ", "), retLLVM)
	}
	b.WriteString("}\n")
	g.fnValueThunkDefs[symbol] = b.String()
	g.fnValueThunkOrder = append(g.fnValueThunkOrder, symbol)
	return symbol
}

// fnValueEnvKind is the GC kind tag for closure envs. Uses the
// generic kind (1) for now — phase 1 envs have no captures so the
// default trace-none/destroy-none layout works. Phase 4 will want
// its own kind with a trace that marks captured ptrs.
const fnValueEnvKind = 1

// fnValueEnvByteSize is the size in bytes of the bare 1-field env.
// Stays a compile-time literal because the env layout is fixed: one
// ptr slot, 8 bytes on every target this backend supports (LLVM
// target triple is 64-bit).
const fnValueEnvByteSize = 8

// emitFnValueEnv materialises a 1-field closure env holding the
// thunk pointer for the given top-level fn. Returns a `ptr`-typed
// value tagged with fnSigRef so a subsequent call site can do
// indirect dispatch with the correct signature.
//
// Allocation goes through the GC (`osty.gc.alloc_v1`) rather than a
// stack alloca so the env can safely outlive the enclosing frame —
// e.g. when stored in a `List<fn(...)>`, a struct field, or passed
// into a higher-order fn that memoises it. The returned value is
// tagged `gcManaged: true` so downstream `bindNamedLocal` registers
// it as a frame root.
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
	emitter := g.toOstyEmitter()
	env := llvmGcAlloc(emitter, fnValueEnvKind, fnValueEnvByteSize, "runtime.closure.env.ptr")
	emitter.body = append(emitter.body, fmt.Sprintf("  store ptr @%s, ptr %s", thunk, env.name))
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
//   %fn = load ptr, ptr %env
//   (%result = )? call <ret> (ptr, P...) %fn(ptr %env, args...)
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
	// Build the LLVM call-type string: `ret (ptr, P0, P1, ...)`.
	paramParts := make([]string, 0, 1+len(sig.params))
	paramParts = append(paramParts, "ptr")
	for _, p := range sig.params {
		paramParts = append(paramParts, llvmParamIRType(p))
	}
	callType := retLLVM + " (" + strings.Join(paramParts, ", ") + ")"

	emitter := g.toOstyEmitter()
	fnPtr := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = load ptr, ptr %s", fnPtr, envVal.ref))

	// Assemble argument list with env prefix.
	argStrs := make([]string, 0, 1+len(args))
	argStrs = append(argStrs, fmt.Sprintf("ptr %s", envVal.ref))
	for _, a := range args {
		argStrs = append(argStrs, fmt.Sprintf("%s %s", a.typ, a.name))
	}

	if retLLVM == "void" {
		emitter.body = append(emitter.body, fmt.Sprintf("  call %s %s(%s)", callType, fnPtr, strings.Join(argStrs, ", ")))
		g.takeOstyEmitter(emitter)
		return value{}, nil
	}
	result := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = call %s %s(%s)", result, callType, fnPtr, strings.Join(argStrs, ", ")))
	g.takeOstyEmitter(emitter)
	out := value{typ: retLLVM, ref: result}
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
		info := paramInfo{
			name:       fmt.Sprintf("arg%d", i),
			typ:        typ,
			irTyp:      typ,
			sourceType: pt,
		}
		if listElemTyp, listElemString, ok, err := llvmListElementInfo(pt, env); err != nil {
			return nil, err
		} else if ok {
			info.listElemTyp = listElemTyp
			info.listElemString = listElemString
		}
		if mapKeyTyp, mapValueTyp, mapKeyString, ok, err := llvmMapTypes(pt, env); err != nil {
			return nil, err
		} else if ok {
			info.mapKeyTyp = mapKeyTyp
			info.mapValueTyp = mapValueTyp
			info.mapKeyString = mapKeyString
		}
		if setElemTyp, setElemString, ok, err := llvmSetElementType(pt, env); err != nil {
			return nil, err
		} else if ok {
			info.setElemTyp = setElemTyp
			info.setElemString = setElemString
		}
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
		ret:              retLLVM,
		returnSourceType: ft.ReturnType,
		params:           params,
	}
	if ft.ReturnType != nil {
		if listElemTyp, listElemString, ok, err := llvmListElementInfo(ft.ReturnType, env); err != nil {
			return nil, err
		} else if ok {
			sig.retListElemTyp = listElemTyp
			sig.retListString = listElemString
		}
		if mapKeyTyp, mapValueTyp, mapKeyString, ok, err := llvmMapTypes(ft.ReturnType, env); err != nil {
			return nil, err
		} else if ok {
			sig.retMapKeyTyp = mapKeyTyp
			sig.retMapValueTyp = mapValueTyp
			sig.retMapKeyString = mapKeyString
		}
		if setElemTyp, setElemString, ok, err := llvmSetElementType(ft.ReturnType, env); err != nil {
			return nil, err
		} else if ok {
			sig.retSetElemTyp = setElemTyp
			sig.retSetElemString = setElemString
		}
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
	g.emitGCSafepoint(emitter)
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
		slot, ok := g.lookupBinding(fn.Name)
		if !ok || slot.fnSigRef == nil {
			return value{}, nil, false, nil
		}
		envVal, err := g.loadIfPointer(slot)
		if err != nil {
			return value{}, nil, true, err
		}
		return envVal, slot.fnSigRef, true, nil
	case *ast.FieldExpr:
		if fn.IsOptional {
			return value{}, nil, false, nil
		}
		// Only attempt this path when the receiver's static type is
		// a known struct and the field's source type is an FnType.
		// Everything else (method calls, optional chains, module
		// aliases) remains owned by the upstream hooks.
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
		ft, ok := field.sourceType.(*ast.FnType)
		if !ok {
			return value{}, nil, false, nil
		}
		sig, err := synthFnSigFromFnType(ft, g.typeEnv())
		if err != nil {
			return value{}, nil, true, err
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
	return value{}, nil, false, nil
}
