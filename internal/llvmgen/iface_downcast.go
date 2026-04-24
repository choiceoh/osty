package llvmgen

// Interface downcast IR lowering.
//
// Lays down the `recv.downcast::<T>()` code path — the mechanism that
// backs the spec's `Error.downcast::<T>()` nominal-tag recovery
// (LANG_SPEC_v0.5 §7.4) and its `err as? T` shorthand (G27). The
// Error interface is the only user-facing consumer, but nothing in
// the emitter here is Error-specific: the compare-vtable trick works
// for any interface because the backend already emits one unique
// `@osty.vtable.<impl>__<iface>` per (impl, iface) pair.
//
// Each `%osty.iface = type { ptr, ptr }` value carries `(data_ptr,
// vtable_ptr)` and the vtable pointer itself serves as the runtime
// nominal tag. `expr.downcast::<T>()` lowers to four LLVM
// instructions:
//
//   %vt   = extractvalue %osty.iface <recv>, 1
//   %data = extractvalue %osty.iface <recv>, 0
//   %is_t = icmp eq ptr %vt, <target-vtable-sym>
//   %opt  = select i1 %is_t, ptr %data, ptr null
//
// The `T?` result is represented as `ptr` (matching the existing
// optional lowering in stdlib_shim.go): non-null when the tag
// matches, null when it doesn't. `Some(x)` / `None` construction is
// erased — callers pattern-match on the ptr value.
//
// Infrastructure note. This file is the backend half of the feature
// only. The AST recognizer (`emitInterfaceDowncastCall`) is wired
// into the `emitCall` dispatcher but will not fire until the
// self-hosted checker (toolchain/elab.osty) teaches users'
// `recv.downcast::<T>()` calls to type-check at the front end. Until
// then the path ships "ready to serve" with test coverage that drives
// it directly rather than through the full compilation of a source
// program that uses the syntax.

import (
	"fmt"

	"github.com/osty/osty/internal/ast"
)

// emitIfaceDowncastIR emits the four-instruction lowering described
// above and returns a ptr-typed value holding the `T?` result.
//
// Preconditions:
//   - ifaceVal.typ == "%osty.iface"
//   - targetVtableSym is a non-empty LLVM global symbol name (e.g.
//     `@osty.vtable.FsError__Error`); the caller is responsible for
//     ensuring the symbol is actually emitted for the enclosing
//     module (i.e. the target type implements the interface).
//
// The returned value's sourceType is left unset; callers that need
// it (e.g. to thread the optional-context lookup in stdlib_shim)
// must populate it based on the call-site's declared `T?`.
func (g *generator) emitIfaceDowncastIR(ifaceVal value, targetVtableSym string) (value, error) {
	if ifaceVal.typ != "%osty.iface" {
		return value{}, unsupportedf("type-system",
			"downcast receiver must be %%osty.iface, got %s", ifaceVal.typ)
	}
	if targetVtableSym == "" {
		return value{}, unsupportedf("type-system",
			"downcast target has no vtable symbol")
	}
	emitter := g.toOstyEmitter()
	vt := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf(
		"  %s = extractvalue %%osty.iface %s, 1", vt, ifaceVal.ref))
	data := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf(
		"  %s = extractvalue %%osty.iface %s, 0", data, ifaceVal.ref))
	isT := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf(
		"  %s = icmp eq ptr %s, %s", isT, vt, targetVtableSym))
	opt := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf(
		"  %s = select i1 %s, ptr %s, ptr null", opt, isT, data))
	g.takeOstyEmitter(emitter)
	return value{typ: "ptr", ref: opt}, nil
}

// emitInterfaceDowncastCall recognises a `recv.downcast::<T>()` call
// shape and dispatches to emitIfaceDowncastIR. Returns found=false
// when the call shape doesn't match so the surrounding emitCall
// dispatcher can fall through to the next handler.
//
// Recognition walks:
//
//	CallExpr
//	  Fn: TurbofishExpr
//	    Base: FieldExpr { Name: "downcast", X: <recv> }
//	    Args: [ NamedType{ Path: ["T"] } ]
//	  Args: []                       // no value args
//
// The receiver's static type must resolve to %osty.iface (any
// interface — downcast is Error-specific in the spec but the
// lowering generalises). The target type arg must name a
// struct/enum that implements the same interface as the receiver.
//
// When the match fails (wrong shape, zero type args, receiver not
// an interface, target not implementing the interface), the
// function returns found=true with an unsupportedf diagnostic so
// the caller can surface a stable-ish error rather than falling
// through to the generic "no field downcast" path.
func (g *generator) emitInterfaceDowncastCall(call *ast.CallExpr) (value, bool, error) {
	if call == nil {
		return value{}, false, nil
	}
	tf, ok := call.Fn.(*ast.TurbofishExpr)
	if !ok || tf == nil {
		return value{}, false, nil
	}
	fx, ok := tf.Base.(*ast.FieldExpr)
	if !ok || fx == nil || fx.Name != "downcast" {
		return value{}, false, nil
	}
	if len(tf.Args) != 1 {
		return value{}, false, nil
	}
	if len(call.Args) != 0 {
		return value{}, true, unsupportedf("call",
			"downcast takes no value arguments, got %d", len(call.Args))
	}
	recvInfo, ok := g.staticExprInfo(fx.X)
	if !ok || recvInfo.typ != "%osty.iface" {
		return value{}, false, nil
	}
	targetName := namedTypeSingleSegment(tf.Args[0])
	if targetName == "" {
		return value{}, true, unsupportedf("call",
			"downcast target must be a single-segment named type")
	}
	// Prefer the receiver's exact interface when we can recover it from
	// the static source type — that catches the spec-relevant class of
	// mistake where `T` implements a *different* interface than the
	// receiver (e.g. `Printable.downcast::<FsError>()` when FsError
	// only implements Error). Falls through to the any-interface match
	// when the receiver's source type isn't available (common in the
	// legacy-bridge path where the receiver is a synthesized AST node
	// with no attached source type).
	ifaceName := ""
	if src, ok := g.staticExprSourceType(fx.X); ok {
		ifaceName = namedTypeSingleSegment(src)
	}
	vtableSym, ok := g.lookupVtableSymForDowncast(targetName, ifaceName)
	if !ok {
		if ifaceName != "" {
			return value{}, true, unsupportedf("call",
				"downcast target %q does not implement interface %q",
				targetName, ifaceName)
		}
		return value{}, true, unsupportedf("call",
			"downcast target %q does not implement any registered interface", targetName)
	}
	recv, err := g.emitExpr(fx.X)
	if err != nil {
		return value{}, true, err
	}
	out, err := g.emitIfaceDowncastIR(recv, vtableSym)
	if err != nil {
		return value{}, true, err
	}
	return out, true, nil
}

// lookupVtableSymForImpl returns the vtable symbol emitted for the
// named impl across any registered interface. The generator emits
// one vtable per (impl, iface) pair; when an impl satisfies multiple
// interfaces the first hit wins. This is the coarse lookup used when
// the downcast receiver's interface can't be recovered statically —
// callers that do know the interface name should prefer
// lookupVtableSymForDowncast for stronger mismatch detection.
func (g *generator) lookupVtableSymForImpl(implName string) (string, bool) {
	for _, iface := range g.interfacesByName {
		if iface == nil {
			continue
		}
		for _, impl := range iface.impls {
			if impl.implName == implName {
				return impl.vtableSym, true
			}
		}
	}
	return "", false
}

// lookupVtableSymForDowncast resolves the (impl, iface) vtable symbol
// that should back `recv.downcast::<T>()` where `recv` is statically
// known to satisfy `ifaceName`. When `ifaceName` is empty (source
// type unavailable) the lookup falls back to the any-interface match,
// preserving the behavior the backend shipped with when the checker
// still blocked downcast end-to-end.
//
// The receiver-interface-aware form catches the mistake of
// downcasting to a type that implements an *unrelated* interface:
// e.g. `printable.downcast::<FsError>()` where FsError only impls
// Error, not Printable. Without this check the lowering would still
// emit a vtable compare, but against FsError's Error vtable — a
// pointer that never matches the runtime Printable vtable embedded
// in `printable`, so the result is always None. Raising an error at
// compile time surfaces the bug at its source instead.
func (g *generator) lookupVtableSymForDowncast(implName, ifaceName string) (string, bool) {
	if ifaceName == "" {
		return g.lookupVtableSymForImpl(implName)
	}
	iface := g.interfacesByName[ifaceName]
	if iface == nil {
		return "", false
	}
	for _, impl := range iface.impls {
		if impl.implName == implName {
			return impl.vtableSym, true
		}
	}
	return "", false
}

// namedTypeSingleSegment returns the single-segment name of a type
// AST node, or "" if the node isn't a `*ast.NamedType` with a one-
// segment path. downcast only resolves to top-level named types at
// the lowering level; generic downcast targets (`downcast::<List<T>>`)
// are intentionally out of scope and fall through.
func namedTypeSingleSegment(t ast.Type) string {
	nt, ok := t.(*ast.NamedType)
	if !ok || nt == nil {
		return ""
	}
	if len(nt.Path) != 1 {
		return ""
	}
	return nt.Path[0]
}
