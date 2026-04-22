package llvmgen

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	ostyir "github.com/osty/osty/internal/ir"
)

type nativeStructFieldInfo struct {
	name     string
	llvmType string
	index    int
	irType   ostyir.Type
}

type nativeStructInfo struct {
	def    *llvmNativeStruct
	byName map[string]nativeStructFieldInfo
}

// nativeEnumVariantInfo captures the resolved payload type for a
// single monomorphized enum variant. `payloadLLVMType` is "" for
// payload-free variants (e.g. `None`).
type nativeEnumVariantInfo struct {
	name            string
	tag             int
	payloadLLVMType string
	payloadIRTypes  []ostyir.Type
}

// nativeEnumInfo mirrors nativeStructInfo but for tagged-union
// enums. The `def` is kept on the module side under
// `llvmNativeModule.enums`, and projection state is carried here
// so the IR projection layer (populated in a follow-up session)
// can route variant construction and pattern matches back to the
// same storage.
type nativeEnumInfo struct {
	def             *llvmNativeEnum
	variantsByName  map[string]*nativeEnumVariantInfo
}

type nativeTupleInfo struct {
	def           *llvmNativeStruct
	elemLLVMTypes []string
}

// nativeInterfaceMethod captures a single interface method's
// calling-convention-level shape: the LLVM return type and parameter
// types *excluding* the receiver (self). The receiver is always
// projected as a `ptr` in the vtable shim — the shim loads the
// concrete struct value before calling the underlying method.
type nativeInterfaceMethod struct {
	name       string
	returnLLVM string
	paramLLVMs []string
}

// nativeInterfaceInfo tracks a non-generic interface declaration so
// the finalization pass can scan for structural impls and emit the
// vtable + shim pair per (interface, concrete struct) match.
type nativeInterfaceInfo struct {
	name    string
	methods []*nativeInterfaceMethod
}

// nativeInterfaceImpl records a (concrete struct, interface) pair
// that structurally matches: the struct exposes every method the
// interface demands, with identical return and non-self parameter
// LLVM types. The finalization pass turns each record into a
// `@osty.vtable.<struct>__<iface>` global plus one
// `@osty.shim.<struct>__<iface>__<method>` thunk per method.
type nativeInterfaceImpl struct {
	structName string
	structLLVM string
	ifaceName  string
	methods    []nativeInterfaceImplMethod
}

type nativeInterfaceImplMethod struct {
	ifaceMethod  *nativeInterfaceMethod
	structMethod nativeMethodInfo
}

type nativeProjectionCtx struct {
	structsByName         map[string]*nativeStructInfo
	enumsByName           map[string]*nativeEnumInfo
	interfacesByName      map[string]*nativeInterfaceInfo
	interfaceOrder        []string // declaration order for deterministic emission
	structOrder           []string // declaration order for deterministic emission
	tuplesByLLVMType      map[string]*nativeTupleInfo
	tupleOrder            []string
	methodsByOwner        map[string]map[string]nativeMethodInfo
	globalConsts          map[string]nativeConstValue
	mutableGlobals        map[string]bool
	scopes                []map[string]nativeExprInfo
	needsListRT           bool
	needsMapRT            bool
	needsSetRT            bool
	needsStringRT         bool
	stringGlobals         []*LlvmStringGlobal
	nextStringID          int
	currentReturnLLVMType string
	// tempCounter mints monotone fresh names for synthetic locals
	// spilled from one-to-many statement expansions (e.g. tuple
	// destructuring, for-in over a list). The name prefix is
	// `__osty_native_t` to avoid colliding with user bindings —
	// Osty identifiers cannot start with a double underscore.
	tempCounter int
}

// freshTempName returns a new synthetic local name scoped to this
// projection context. Used by the statement-fan-out helpers (tuple
// destructure let, for-in loop) to spill intermediates without
// clashing with user identifiers.
func (ctx *nativeProjectionCtx) freshTempName(prefix string) string {
	if ctx == nil {
		return prefix
	}
	ctx.tempCounter++
	return prefix + strconv.Itoa(ctx.tempCounter)
}

type nativeExprInfoKind int

const (
	nativeExprInfoInvalid nativeExprInfoKind = iota
	nativeExprInfoScalar
	nativeExprInfoString
	nativeExprInfoList
	nativeExprInfoMap
	nativeExprInfoSet
	nativeExprInfoStruct
	nativeExprInfoOptional
)

type nativeExprInfo struct {
	kind            nativeExprInfoKind
	llvmType        string
	sourceType      ostyir.Type
	structName      string
	listElemType    string
	listElemString  bool
	listElemBytes   bool
	mapKeyType      string
	mapValueType    string
	mapKeyString    bool
	setElemType     string
	setElemString   bool
	optionInnerType string
}

func (ctx *nativeProjectionCtx) pushScope() {
	if ctx == nil {
		return
	}
	ctx.scopes = append(ctx.scopes, map[string]nativeExprInfo{})
}

func (ctx *nativeProjectionCtx) popScope() {
	if ctx == nil || len(ctx.scopes) == 0 {
		return
	}
	ctx.scopes = ctx.scopes[:len(ctx.scopes)-1]
}

func (ctx *nativeProjectionCtx) bindScopeName(name string, info nativeExprInfo) {
	if ctx == nil || name == "" || info.llvmType == "" {
		return
	}
	if len(ctx.scopes) == 0 {
		ctx.pushScope()
	}
	ctx.scopes[len(ctx.scopes)-1][name] = info
}

func (ctx *nativeProjectionCtx) lookupScopeName(name string) (nativeExprInfo, bool) {
	if ctx == nil || name == "" {
		return nativeExprInfo{}, false
	}
	for i := len(ctx.scopes) - 1; i >= 0; i-- {
		if info, ok := ctx.scopes[i][name]; ok {
			return info, true
		}
	}
	return nativeExprInfo{}, false
}

type nativeMethodInfo struct {
	irName      string
	receiverMut bool
}

type nativeConstValue struct {
	llvmType string
	init     string
}

// TryGenerateNativeOwnedModule emits textual LLVM IR only through the
// native-owned primitive/control-flow slice mirrored from
// toolchain/llvmgen.osty.
//
// ok=false means "shape not covered yet" and callers should choose a
// fallback path themselves. Unlike GenerateModule, this helper never
// falls back to the transitional IR -> AST bridge.
func TryGenerateNativeOwnedModule(mod *ostyir.Module, opts Options) ([]byte, bool, error) {
	if err := prepareModuleGeneration(mod); err != nil {
		return nil, false, err
	}
	out, ok, err := tryNativeOwnedModule(mod, opts)
	if err != nil || !ok {
		return out, ok, err
	}
	return finalizeLegacyFFISurface(out, mod), true, nil
}

// tryNativeOwnedModule projects the IR module into the native-owned
// primitive/control-flow slice mirrored from toolchain/llvmgen.osty.
// ok=false means "shape not covered yet" and callers should fall back to the
// legacy IR -> AST bridge.
func tryNativeOwnedModule(mod *ostyir.Module, opts Options) ([]byte, bool, error) {
	nativeMod, ok := nativeModuleFromIR(mod, opts)
	if !ok {
		return nil, false, nil
	}
	out := []byte(llvmNativeEmitModule(nativeMod))
	out = appendNativeInterfaceSurface(out, mod, nativeMod)
	return out, true, nil
}

func nativeModuleFromIR(mod *ostyir.Module, opts Options) (*llvmNativeModule, bool) {
	if mod == nil {
		return nil, false
	}
	ctx := &nativeProjectionCtx{
		structsByName:    map[string]*nativeStructInfo{},
		enumsByName:      map[string]*nativeEnumInfo{},
		interfacesByName: map[string]*nativeInterfaceInfo{},
		tuplesByLLVMType: map[string]*nativeTupleInfo{},
		methodsByOwner:   map[string]map[string]nativeMethodInfo{},
		globalConsts:     map[string]nativeConstValue{},
		mutableGlobals:   map[string]bool{},
	}
	out := &llvmNativeModule{
		sourcePath:    firstNonEmpty(opts.SourcePath, "<unknown>"),
		target:        opts.Target,
		globals:       make([]*llvmNativeGlobal, 0, len(mod.Decls)),
		structs:       make([]*llvmNativeStruct, 0, len(mod.Decls)),
		enums:         make([]*llvmNativeEnum, 0, len(mod.Decls)),
		stringGlobals: make([]*LlvmStringGlobal, 0),
		functions:     make([]*llvmNativeFunction, 0, len(mod.Decls)+1),
	}
	hasMain := false
	for _, decl := range mod.Decls {
		switch d := decl.(type) {
		case nil:
			continue
		case *ostyir.UseDecl:
			if d.IsFFI() {
				return nil, false
			}
		case *ostyir.StructDecl:
			info, ok := nativeRegisterStructDecl(d)
			if !ok {
				return nil, false
			}
			if _, exists := ctx.structsByName[d.Name]; exists {
				return nil, false
			}
			ctx.structsByName[d.Name] = info
			ctx.structOrder = append(ctx.structOrder, d.Name)
			out.structs = append(out.structs, info.def)
			if !nativeRegisterStructMethodHeaders(ctx, d) {
				return nil, false
			}
		case *ostyir.InterfaceDecl:
			if d == nil {
				continue
			}
			// Generic interfaces are vtable-templated by
			// specialization; we only accept the non-generic surface
			// today. `Extends` inheritance would need flattening and
			// is deferred alongside interface generics.
			if len(d.Generics) != 0 || len(d.Extends) != 0 {
				return nil, false
			}
			info, ok := nativeRegisterInterfaceDecl(ctx, d)
			if !ok {
				return nil, false
			}
			if _, exists := ctx.interfacesByName[d.Name]; exists {
				return nil, false
			}
			ctx.interfacesByName[d.Name] = info
			ctx.interfaceOrder = append(ctx.interfaceOrder, d.Name)
		case *ostyir.EnumDecl:
			if d == nil {
				continue
			}
			// Generic templates survive monomorphization alongside
			// their specializations in the output module. The
			// specializations carry the mangled `_ZTS…` names and
			// hold the concrete payload types we actually lower;
			// the templates are unreachable post-mono so we skip
			// them here rather than refusing the whole module.
			if len(d.Generics) != 0 {
				continue
			}
			info, ok := nativeRegisterEnumDecl(ctx, d)
			if !ok {
				return nil, false
			}
			if _, exists := ctx.enumsByName[d.Name]; exists {
				return nil, false
			}
			if len(d.Methods) != 0 {
				return nil, false
			}
			ctx.enumsByName[d.Name] = info
			out.enums = append(out.enums, info.def)
		case *ostyir.LetDecl:
			ctx.mutableGlobals[d.Name] = d.Mut
		}
	}
	for _, decl := range mod.Decls {
		switch d := decl.(type) {
		case nil:
			continue
		case *ostyir.UseDecl:
			continue
		case *ostyir.LetDecl:
			global, ok := nativeGlobalLetFromIR(ctx, d)
			if !ok {
				return nil, false
			}
			out.globals = append(out.globals, global)
		case *ostyir.StructDecl:
			if !nativePopulateStructDecl(ctx, d) {
				return nil, false
			}
			for _, method := range d.Methods {
				fn, ok := nativeMethodFunctionFromIR(ctx, d, method)
				if !ok {
					return nil, false
				}
				out.functions = append(out.functions, fn)
			}
		case *ostyir.EnumDecl:
			// Enum declarations are fully projected during the
			// registration phase above; nothing to populate here.
			// The explicit case keeps enum decls from falling into
			// the default arm that rejects the whole module.
			continue
		case *ostyir.InterfaceDecl:
			// Interface decls are pure signatures — the projection
			// layer already registered them. Vtable + shim
			// emission happens in the finalization pass after all
			// concrete structs are known.
			continue
		case *ostyir.FnDecl:
			fn, ok := nativeFunctionFromIR(ctx, d)
			if !ok {
				return nil, false
			}
			if fn.name == "main" {
				hasMain = true
			}
			out.functions = append(out.functions, fn)
		default:
			return nil, false
		}
	}
	out.stringGlobals = append(out.stringGlobals, ctx.stringGlobals...)
	for _, llvmType := range ctx.tupleOrder {
		if info := ctx.tuplesByLLVMType[llvmType]; info != nil {
			out.structs = append(out.structs, info.def)
		}
	}
	if len(mod.Script) != 0 {
		if hasMain {
			return nil, false
		}
		prevReturnType := ctx.currentReturnLLVMType
		ctx.currentReturnLLVMType = "i32"
		ctx.pushScope()
		mainBody, ok := nativeBlockFromStmts(ctx, mod.Script, "i32")
		ctx.popScope()
		ctx.currentReturnLLVMType = prevReturnType
		if !ok {
			return nil, false
		}
		out.functions = append(out.functions, &llvmNativeFunction{
			name:       "main",
			returnType: "i32",
			body:       mainBody,
		})
	}
	out.needsListRuntime = ctx.needsListRT
	out.needsMapRuntime = ctx.needsMapRT
	out.needsSetRuntime = ctx.needsSetRT
	out.needsStringRuntime = ctx.needsStringRT
	return out, true
}

// appendNativeInterfaceSurface scans the IR module for every
// (non-generic interface, concrete struct) pair where the struct's
// method set structurally satisfies the interface signature, and
// appends the `%osty.iface` fat-pointer type def + the
// `@osty.vtable.<struct>__<iface>` constant globals + the
// `@osty.shim.<struct>__<iface>__<method>` ABI-adapter fns to the
// emitted LLVM IR.
//
// The shim performs the interface-to-concrete ABI conversion: the
// vtable slot takes a `ptr` receiver (the boxed data pointer), but
// the underlying `@<Struct>__<method>` symbol takes the struct by
// value. The shim loads the struct through the pointer, calls the
// concrete method, and forwards the result. Call-site boxing and
// indirect dispatch through the vtable lands in a follow-up.
//
// Emission lives in Go — we walk the original IR module rather than
// thread extra state through `nativeModuleFromIR` — because the
// matching is fully declarative over the IR shape. Osty-side
// llvmNativeEmitModule stays untouched.
func appendNativeInterfaceSurface(out []byte, mod *ostyir.Module, _ *llvmNativeModule) []byte {
	if mod == nil {
		return out
	}
	impls := collectNativeInterfaceImpls(mod)
	if len(impls) == 0 {
		return out
	}
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	out = append(out, "%osty.iface = type { ptr, ptr }\n"...)
	for _, impl := range impls {
		out = append(out, emitNativeInterfaceVtable(impl)...)
		for _, m := range impl.methods {
			out = append(out, emitNativeInterfaceShim(impl, m)...)
		}
	}
	return out
}

// collectNativeInterfaceImpls walks the IR module and produces an
// ordered list of (struct, interface) structural impls. Each impl
// is deterministic — struct iteration is in declaration order and
// interface iteration also in declaration order — so vtable /
// shim symbols emit in a stable sequence across runs.
//
// A struct "implements" an interface when, for every interface
// method, the struct declares a method with the same name whose
// return + non-self parameter LLVM-ABI types match exactly.
// Struct methods beyond the interface requirement are fine.
// Receivers must be non-`mut` (the shim loads by-value through
// the data ptr — a `mut self` method would need the data ptr
// threaded end-to-end and is deferred).
func collectNativeInterfaceImpls(mod *ostyir.Module) []nativeInterfaceImpl {
	if mod == nil {
		return nil
	}
	ctx := &nativeProjectionCtx{
		structsByName:    map[string]*nativeStructInfo{},
		enumsByName:      map[string]*nativeEnumInfo{},
		interfacesByName: map[string]*nativeInterfaceInfo{},
		tuplesByLLVMType: map[string]*nativeTupleInfo{},
		methodsByOwner:   map[string]map[string]nativeMethodInfo{},
	}
	structOrder := make([]string, 0)
	structDecls := map[string]*ostyir.StructDecl{}
	ifaceOrder := make([]string, 0)
	ifaceDecls := map[string]*ostyir.InterfaceDecl{}
	for _, decl := range mod.Decls {
		switch d := decl.(type) {
		case *ostyir.StructDecl:
			if d == nil || len(d.Generics) != 0 {
				continue
			}
			info, ok := nativeRegisterStructDecl(d)
			if !ok {
				continue
			}
			if _, exists := ctx.structsByName[d.Name]; exists {
				continue
			}
			ctx.structsByName[d.Name] = info
			structOrder = append(structOrder, d.Name)
			structDecls[d.Name] = d
		case *ostyir.InterfaceDecl:
			if d == nil || len(d.Generics) != 0 || len(d.Extends) != 0 {
				continue
			}
			ifaceOrder = append(ifaceOrder, d.Name)
			ifaceDecls[d.Name] = d
		}
	}
	// Populate struct fields (needed for any interface param typed as
	// another struct in the same module) and header-level method
	// metadata (receiverMut / irName).
	for _, name := range structOrder {
		d := structDecls[name]
		if d == nil {
			continue
		}
		_ = nativePopulateStructDecl(ctx, d)
		_ = nativeRegisterStructMethodHeaders(ctx, d)
	}
	// Build interface infos after structs are populated so
	// interface methods referring to those structs resolve.
	for _, name := range ifaceOrder {
		d := ifaceDecls[name]
		if d == nil {
			continue
		}
		info, ok := nativeRegisterInterfaceDecl(ctx, d)
		if !ok {
			continue
		}
		ctx.interfacesByName[d.Name] = info
	}
	var impls []nativeInterfaceImpl
	for _, structName := range structOrder {
		sDecl := structDecls[structName]
		sInfo := ctx.structsByName[structName]
		if sDecl == nil || sInfo == nil {
			continue
		}
		structMethodSigs := collectNativeStructMethodSigs(ctx, sDecl)
		for _, ifaceName := range ifaceOrder {
			iface := ctx.interfacesByName[ifaceName]
			if iface == nil {
				continue
			}
			impl, ok := buildNativeInterfaceImpl(sInfo, structMethodSigs, iface)
			if !ok {
				continue
			}
			impls = append(impls, impl)
		}
	}
	return impls
}

// nativeStructMethodSig captures just enough of a struct method to
// compare against an interface method signature: the LLVM return
// and non-self parameter types, plus the receiver mutability flag.
// Body / intrinsic / export attributes are irrelevant here —
// structural matching only cares about the call-shape.
type nativeStructMethodSig struct {
	returnLLVM  string
	paramLLVMs  []string
	receiverMut bool
	irName      string
}

func collectNativeStructMethodSigs(ctx *nativeProjectionCtx, decl *ostyir.StructDecl) map[string]*nativeStructMethodSig {
	out := map[string]*nativeStructMethodSig{}
	if ctx == nil || decl == nil {
		return out
	}
	for _, m := range decl.Methods {
		if m == nil || m.Name == "" || m.IsIntrinsic || m.Body == nil || len(m.Generics) != 0 {
			continue
		}
		retLLVM, ok := nativeLLVMTypeFromIR(ctx, m.Return)
		if !ok {
			continue
		}
		paramLLVMs := make([]string, 0, len(m.Params))
		brokenParam := false
		for _, p := range m.Params {
			if p == nil || p.IsDestructured() || p.Default != nil {
				brokenParam = true
				break
			}
			typ, ok := nativeLLVMTypeFromIR(ctx, p.Type)
			if !ok || typ == "void" {
				brokenParam = true
				break
			}
			paramLLVMs = append(paramLLVMs, typ)
		}
		if brokenParam {
			continue
		}
		out[m.Name] = &nativeStructMethodSig{
			returnLLVM:  retLLVM,
			paramLLVMs:  paramLLVMs,
			receiverMut: m.ReceiverMut,
			irName:      llvmMethodIRName(decl.Name, m.Name),
		}
	}
	return out
}

// buildNativeInterfaceImpl succeeds iff every interface method has
// a structurally compatible struct method — same name, same return
// LLVM type, same non-self param LLVM types, non-`mut` receiver.
func buildNativeInterfaceImpl(
	sInfo *nativeStructInfo,
	structSigs map[string]*nativeStructMethodSig,
	iface *nativeInterfaceInfo,
) (nativeInterfaceImpl, bool) {
	methods := make([]nativeInterfaceImplMethod, 0, len(iface.methods))
	for _, im := range iface.methods {
		sig, ok := structSigs[im.name]
		if !ok || sig == nil {
			return nativeInterfaceImpl{}, false
		}
		if sig.receiverMut {
			return nativeInterfaceImpl{}, false
		}
		if sig.returnLLVM != im.returnLLVM {
			return nativeInterfaceImpl{}, false
		}
		if len(sig.paramLLVMs) != len(im.paramLLVMs) {
			return nativeInterfaceImpl{}, false
		}
		for i := range im.paramLLVMs {
			if sig.paramLLVMs[i] != im.paramLLVMs[i] {
				return nativeInterfaceImpl{}, false
			}
		}
		methods = append(methods, nativeInterfaceImplMethod{
			ifaceMethod: im,
			structMethod: nativeMethodInfo{
				irName:      sig.irName,
				receiverMut: sig.receiverMut,
			},
		})
	}
	return nativeInterfaceImpl{
		structName: sInfo.def.name,
		structLLVM: sInfo.def.llvmType,
		ifaceName:  iface.name,
		methods:    methods,
	}, true
}

// emitNativeInterfaceVtable emits
// `@osty.vtable.<struct>__<iface> = internal constant { ptr, ptr, ... } { ptr @osty.shim...., ... }`
// — one slot per interface method, in declaration order.
func emitNativeInterfaceVtable(impl nativeInterfaceImpl) []byte {
	if len(impl.methods) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("@osty.vtable.")
	b.WriteString(impl.structName)
	b.WriteString("__")
	b.WriteString(impl.ifaceName)
	b.WriteString(" = internal constant { ")
	for i := range impl.methods {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("ptr")
	}
	b.WriteString(" } { ")
	for i, m := range impl.methods {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("ptr @osty.shim.")
		b.WriteString(impl.structName)
		b.WriteString("__")
		b.WriteString(impl.ifaceName)
		b.WriteString("__")
		b.WriteString(m.ifaceMethod.name)
	}
	b.WriteString(" }\n")
	return []byte(b.String())
}

// emitNativeInterfaceShim emits the ABI adapter:
//
//	define <ret> @osty.shim.<S>__<I>__<M>(ptr %self_data, <params...>) {
//	  %self = load %<S>, ptr %self_data, align 8
//	  %res = call <ret> @<S>__<M>(%<S> %self, <params...>)
//	  ret <ret> %res
//	}
//
// For unit-returning methods (ret == "void") the shim ends with
// `ret void` without capturing a result value.
func emitNativeInterfaceShim(impl nativeInterfaceImpl, m nativeInterfaceImplMethod) []byte {
	var b strings.Builder
	ret := m.ifaceMethod.returnLLVM
	if ret == "" {
		ret = "void"
	}
	b.WriteString("define ")
	b.WriteString(ret)
	b.WriteString(" @osty.shim.")
	b.WriteString(impl.structName)
	b.WriteString("__")
	b.WriteString(impl.ifaceName)
	b.WriteString("__")
	b.WriteString(m.ifaceMethod.name)
	b.WriteString("(ptr %self_data")
	for i, pt := range m.ifaceMethod.paramLLVMs {
		b.WriteString(", ")
		b.WriteString(pt)
		b.WriteString(" %arg")
		b.WriteString(strconv.Itoa(i))
	}
	b.WriteString(") {\nentry:\n")
	b.WriteString("  %self = load ")
	b.WriteString(impl.structLLVM)
	b.WriteString(", ptr %self_data, align 8\n")
	callLHS := ""
	if ret != "void" {
		callLHS = "  %res = "
	} else {
		callLHS = "  "
	}
	b.WriteString(callLHS)
	b.WriteString("call ")
	b.WriteString(ret)
	b.WriteString(" @")
	b.WriteString(m.structMethod.irName)
	b.WriteString("(")
	b.WriteString(impl.structLLVM)
	b.WriteString(" %self")
	for i, pt := range m.ifaceMethod.paramLLVMs {
		b.WriteString(", ")
		b.WriteString(pt)
		b.WriteString(" %arg")
		b.WriteString(strconv.Itoa(i))
	}
	b.WriteString(")\n")
	if ret == "void" {
		b.WriteString("  ret void\n")
	} else {
		b.WriteString("  ret ")
		b.WriteString(ret)
		b.WriteString(" %res\n")
	}
	b.WriteString("}\n")
	return []byte(b.String())
}

// sortedStringKeys returns the keys of a string-keyed map in sorted
// order — gives the vtable / shim emission a stable output shape.
func sortedStringKeys[V any](m map[string]V) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// nativeRegisterInterfaceDecl captures a non-generic interface's
// method shape set. Each method signature is reduced to its
// LLVM-level return + non-self parameter types so the finalization
// pass can test structural impl compatibility against concrete
// struct methods without re-walking IR type nodes.
//
// Interfaces with default method bodies, generics, or `extends`
// clauses are rejected by the caller — stage-1 coverage only
// handles the flat, signature-only shape the Phase 6a vtable test
// exercises.
func nativeRegisterInterfaceDecl(ctx *nativeProjectionCtx, decl *ostyir.InterfaceDecl) (*nativeInterfaceInfo, bool) {
	if ctx == nil || decl == nil || decl.Name == "" {
		return nil, false
	}
	methods := make([]*nativeInterfaceMethod, 0, len(decl.Methods))
	for _, m := range decl.Methods {
		if m == nil || m.Name == "" {
			return nil, false
		}
		if m.Body != nil || len(m.Generics) != 0 {
			// Default-method bodies and per-method generics
			// require codegen we don't emit yet.
			return nil, false
		}
		retLLVM, ok := nativeLLVMTypeFromIR(ctx, m.Return)
		if !ok {
			return nil, false
		}
		paramLLVMs := make([]string, 0, len(m.Params))
		for _, p := range m.Params {
			if p == nil || p.IsDestructured() || p.Default != nil {
				return nil, false
			}
			typ, ok := nativeLLVMTypeFromIR(ctx, p.Type)
			if !ok || typ == "void" {
				return nil, false
			}
			paramLLVMs = append(paramLLVMs, typ)
		}
		methods = append(methods, &nativeInterfaceMethod{
			name:       m.Name,
			returnLLVM: retLLVM,
			paramLLVMs: paramLLVMs,
		})
	}
	return &nativeInterfaceInfo{name: decl.Name, methods: methods}, true
}

func nativeRegisterStructDecl(decl *ostyir.StructDecl) (*nativeStructInfo, bool) {
	if decl == nil || decl.Name == "" || len(decl.Generics) != 0 {
		return nil, false
	}
	return &nativeStructInfo{
		def: &llvmNativeStruct{
			name:     decl.Name,
			llvmType: llvmStructTypeName(decl.Name),
			fields:   make([]*llvmNativeStructField, 0, len(decl.Fields)),
		},
		byName: map[string]nativeStructFieldInfo{},
	}, true
}

// nativeRegisterEnumDecl projects a monomorphized enum declaration
// into the native projection context. The resulting `*nativeEnumInfo`
// owns a `*llvmNativeEnum` that the emitter turns into a
// `{ i64 tag, <payload> }` storage struct, plus a
// variant-name-to-tag/payload index the IR projection layer reads
// when lowering variant construction (`Maybe.Some(42)`) and
// pattern matches (`if let Maybe.Some(x) = m`).
//
// The routing (calling this from `nativeModuleFromIR` and wiring
// the variant-construction / pattern-match expression lowering
// paths) lives in a follow-up session — this helper exists so the
// data-model half can land first. It is intentionally not yet
// invoked; the `nativeModuleFromIR` switch still returns `nil,
// false` for `*ostyir.EnumDecl`.
//
// Returns (nil, false) for generic templates: only monomorphized
// specializations are projectable. The payload slot type is
// synthesized from the widest variant payload — today that is
// trivially the single `Int`-shaped slot used by `Option<Int>` /
// `Maybe<Int>`; the follow-up session will extend this to handle
// multi-typed and multi-field payloads by spilling into a union-
// shaped struct.
func nativeRegisterEnumDecl(ctx *nativeProjectionCtx, decl *ostyir.EnumDecl) (*nativeEnumInfo, bool) {
	if ctx == nil || decl == nil || decl.Name == "" || len(decl.Generics) != 0 {
		return nil, false
	}
	variants := make([]*llvmNativeEnumVariant, 0, len(decl.Variants))
	variantInfos := make(map[string]*nativeEnumVariantInfo, len(decl.Variants))
	payloadSlot := ""
	for i, variant := range decl.Variants {
		if variant == nil || variant.Name == "" {
			return nil, false
		}
		payloadLLVM := ""
		if len(variant.Payload) == 1 {
			llvmType, ok := nativeLLVMTypeFromIR(ctx, variant.Payload[0])
			if !ok || llvmType == "void" {
				return nil, false
			}
			payloadLLVM = llvmType
			if payloadSlot == "" {
				payloadSlot = llvmType
			} else if payloadSlot != llvmType {
				// Mixed payload shapes need a union-sized slot the
				// follow-up session will model. Bail so the legacy
				// bridge keeps handling this case for now.
				return nil, false
			}
		} else if len(variant.Payload) > 1 {
			// Multi-field payloads require a synthesized tuple
			// struct. Defer to the follow-up session.
			return nil, false
		}
		variants = append(variants, &llvmNativeEnumVariant{
			name:        variant.Name,
			tag:         i,
			payloadType: payloadLLVM,
		})
		variantInfos[variant.Name] = &nativeEnumVariantInfo{
			name:            variant.Name,
			tag:             i,
			payloadLLVMType: payloadLLVM,
			payloadIRTypes:  append([]ostyir.Type(nil), variant.Payload...),
		}
	}
	return &nativeEnumInfo{
		def: &llvmNativeEnum{
			name:            decl.Name,
			llvmType:        llvmStructTypeName(decl.Name),
			payloadSlotType: payloadSlot,
			variants:        variants,
		},
		variantsByName: variantInfos,
	}, true
}

func nativePopulateStructDecl(ctx *nativeProjectionCtx, decl *ostyir.StructDecl) bool {
	if ctx == nil || decl == nil {
		return false
	}
	info := ctx.structsByName[decl.Name]
	if info == nil || len(info.def.fields) != 0 {
		return info != nil
	}
	for i, field := range decl.Fields {
		if field == nil || field.Name == "" || field.Default != nil {
			return false
		}
		if _, exists := info.byName[field.Name]; exists {
			return false
		}
		llvmType, ok := nativeLLVMTypeFromIR(ctx, field.Type)
		if !ok || llvmType == "void" || llvmType == info.def.llvmType {
			return false
		}
		info.def.fields = append(info.def.fields, &llvmNativeStructField{
			name:     field.Name,
			llvmType: llvmType,
		})
		info.byName[field.Name] = nativeStructFieldInfo{
			name:     field.Name,
			llvmType: llvmType,
			index:    i,
			irType:   field.Type,
		}
	}
	return true
}

func nativeRegisterStructMethodHeaders(ctx *nativeProjectionCtx, decl *ostyir.StructDecl) bool {
	if ctx == nil || decl == nil {
		return false
	}
	methods := ctx.methodsByOwner[decl.Name]
	if methods == nil {
		methods = map[string]nativeMethodInfo{}
		ctx.methodsByOwner[decl.Name] = methods
	}
	for _, method := range decl.Methods {
		if method == nil || len(method.Generics) != 0 || method.IsIntrinsic || method.Body == nil {
			return false
		}
		if _, exists := methods[method.Name]; exists {
			return false
		}
		methods[method.Name] = nativeMethodInfo{
			irName:      llvmMethodIRName(decl.Name, method.Name),
			receiverMut: method.ReceiverMut,
		}
	}
	return true
}

func nativeFunctionFromIR(ctx *nativeProjectionCtx, fn *ostyir.FnDecl) (*llvmNativeFunction, bool) {
	if fn == nil || len(fn.Generics) != 0 || fn.IsIntrinsic || fn.Body == nil {
		return nil, false
	}
	ctx.pushScope()
	defer ctx.popScope()
	retType, ok := nativeFunctionReturnType(ctx, fn)
	if !ok {
		return nil, false
	}
	prevReturnType := ctx.currentReturnLLVMType
	ctx.currentReturnLLVMType = retType
	defer func() { ctx.currentReturnLLVMType = prevReturnType }()
	out := &llvmNativeFunction{
		name:       fn.Name,
		returnType: retType,
		params:     make([]*llvmNativeParam, 0, len(fn.Params)),
		vectorize:  fn.Vectorize,
	}
	for _, param := range fn.Params {
		if param == nil || param.IsDestructured() || param.Default != nil {
			return nil, false
		}
		llvmType, ok := nativeLLVMTypeFromIR(ctx, param.Type)
		if !ok || llvmType == "void" {
			return nil, false
		}
		nativeParam := &llvmNativeParam{
			name:     param.Name,
			llvmType: llvmType,
		}
		if info, ok := nativeExprInfoFromType(ctx, param.Type); ok && info.kind == nativeExprInfoList {
			nativeParam.listElemLLVMType = info.listElemType
		}
		out.params = append(out.params, nativeParam)
		ctx.bindScopeName(param.Name, nativeExprInfoFromLLVMType(llvmType))
		if info, ok := nativeExprInfoFromType(ctx, param.Type); ok {
			ctx.bindScopeName(param.Name, info)
		}
	}
	body, ok := nativeBlockFromIR(ctx, fn.Body, retType)
	if !ok {
		return nil, false
	}
	out.body = body
	return out, true
}

func nativeFunctionReturnType(ctx *nativeProjectionCtx, fn *ostyir.FnDecl) (string, bool) {
	if fn == nil {
		return "", false
	}
	if fn.Name == "main" && len(fn.Params) == 0 && nativeIsUnitType(fn.Return) {
		return "i32", true
	}
	return nativeLLVMTypeFromIR(ctx, fn.Return)
}

func nativeMethodFunctionFromIR(
	ctx *nativeProjectionCtx,
	owner *ostyir.StructDecl,
	fn *ostyir.FnDecl,
) (*llvmNativeFunction, bool) {
	if ctx == nil || owner == nil || fn == nil || len(fn.Generics) != 0 || fn.IsIntrinsic || fn.Body == nil {
		return nil, false
	}
	ownerInfo := ctx.structsByName[owner.Name]
	if ownerInfo == nil {
		return nil, false
	}
	ctx.pushScope()
	defer ctx.popScope()
	retType, ok := nativeFunctionReturnType(ctx, fn)
	if !ok {
		return nil, false
	}
	prevReturnType := ctx.currentReturnLLVMType
	ctx.currentReturnLLVMType = retType
	defer func() { ctx.currentReturnLLVMType = prevReturnType }()
	out := &llvmNativeFunction{
		name:       llvmMethodIRName(owner.Name, fn.Name),
		returnType: retType,
		params: []*llvmNativeParam{{
			name:     "self",
			llvmType: ownerInfo.def.llvmType,
			irType:   llvmMethodReceiverIRType(ownerInfo.def.llvmType, fn.ReceiverMut),
			byRef:    fn.ReceiverMut,
		}},
		vectorize: fn.Vectorize,
	}
	ctx.bindScopeName("self", nativeExprInfo{
		kind:       nativeExprInfoStruct,
		llvmType:   ownerInfo.def.llvmType,
		structName: owner.Name,
	})
	for _, param := range fn.Params {
		if param == nil || param.IsDestructured() || param.Default != nil {
			return nil, false
		}
		llvmType, ok := nativeLLVMTypeFromIR(ctx, param.Type)
		if !ok || llvmType == "void" {
			return nil, false
		}
		nativeParam := &llvmNativeParam{
			name:     param.Name,
			llvmType: llvmType,
		}
		if info, ok := nativeExprInfoFromType(ctx, param.Type); ok && info.kind == nativeExprInfoList {
			nativeParam.listElemLLVMType = info.listElemType
		}
		out.params = append(out.params, nativeParam)
		ctx.bindScopeName(param.Name, nativeExprInfoFromLLVMType(llvmType))
		if info, ok := nativeExprInfoFromType(ctx, param.Type); ok {
			ctx.bindScopeName(param.Name, info)
		}
	}
	body, ok := nativeBlockFromIR(ctx, fn.Body, retType)
	if !ok {
		return nil, false
	}
	out.body = body
	return out, true
}

func nativeBlockFromIR(ctx *nativeProjectionCtx, block *ostyir.Block, fnReturnType string) (*llvmNativeBlock, bool) {
	if block == nil {
		return &llvmNativeBlock{}, true
	}
	ctx.pushScope()
	defer ctx.popScope()
	stmts := block.Stmts
	var tailResult ostyir.Expr
	if block.Result == nil && fnReturnType != "" && fnReturnType != "void" && len(stmts) != 0 {
		if exprStmt, ok := stmts[len(stmts)-1].(*ostyir.ExprStmt); ok && exprStmt != nil && exprStmt.X != nil {
			// Don't promote a unit-typed tail expression to the
			// block result — the function-emission terminator
			// fallback (notably the `main` -> `ret i32 0` path)
			// fills in the actual return. Promoting would force
			// an unsupported value-coercion path for shapes like
			// `if let` that produce unit but live as the trailing
			// statement of `fn main()`.
			if !nativeIsUnitType(exprStmt.X.Type()) {
				tailResult = exprStmt.X
				stmts = stmts[:len(stmts)-1]
			}
		}
	}
	out := &llvmNativeBlock{
		stmts: make([]*llvmNativeStmt, 0, len(stmts)),
	}
	for _, stmt := range stmts {
		nativeStmts, ok := nativeStmtsFromIR(ctx, stmt, fnReturnType)
		if !ok {
			return nil, false
		}
		out.stmts = append(out.stmts, nativeStmts...)
	}
	resultExpr := block.Result
	if resultExpr == nil {
		resultExpr = tailResult
	}
	if resultExpr != nil {
		result, ok := nativeExprFromIR(ctx, resultExpr)
		if !ok {
			return nil, false
		}
		out.hasResult = true
		out.result = result
	}
	return out, true
}

func nativeBlockFromStmts(ctx *nativeProjectionCtx, stmts []ostyir.Stmt, fnReturnType string) (*llvmNativeBlock, bool) {
	ctx.pushScope()
	defer ctx.popScope()
	out := &llvmNativeBlock{
		stmts: make([]*llvmNativeStmt, 0, len(stmts)),
	}
	for _, stmt := range stmts {
		nativeStmts, ok := nativeStmtsFromIR(ctx, stmt, fnReturnType)
		if !ok {
			return nil, false
		}
		out.stmts = append(out.stmts, nativeStmts...)
	}
	return out, true
}

// nativeStmtsFromIR is the one-to-many wrapper around nativeStmtFromIR.
// A small set of IR-level shapes (tuple-destructuring `let`, for-in
// loops) expand into several native statements; everything else passes
// through as a single-element slice. The block iterator calls this so
// fan-out emissions land contiguously in the produced native block.
func nativeStmtsFromIR(ctx *nativeProjectionCtx, stmt ostyir.Stmt, fnReturnType string) ([]*llvmNativeStmt, bool) {
	switch s := stmt.(type) {
	case *ostyir.LetStmt:
		if _, ok := s.Pattern.(*ostyir.TuplePat); ok {
			return nativeLetTupleDestructureStmts(ctx, s)
		}
	case *ostyir.ForStmt:
		if s != nil && s.Kind == ostyir.ForIn {
			return nativeForInListStmts(ctx, s, fnReturnType)
		}
	}
	one, ok := nativeStmtFromIR(ctx, stmt, fnReturnType)
	if !ok {
		return nil, false
	}
	if one == nil {
		return nil, true
	}
	return []*llvmNativeStmt{one}, true
}

// nativeForInListStmts lowers `for x in listExpr { body }` into a
// RangeStmt bounded by 0..listExpr.len() with the element
// extraction (`let x = listExpr[i]`) prepended to the body. The
// iter expression is spilled to a synthesized `__osty_native_list<N>`
// temp when it is not already a bare ident, so side-effects fire
// once. Only covers `ForIn` with a plain loop-variable name;
// destructuring heads defer to the legacy bridge until follow-up
// coverage lands.
func nativeForInListStmts(ctx *nativeProjectionCtx, s *ostyir.ForStmt, fnReturnType string) ([]*llvmNativeStmt, bool) {
	if ctx == nil || s == nil || s.Kind != ostyir.ForIn {
		return nil, false
	}
	if s.IsDestructured() || s.Var == "" || s.Iter == nil {
		return nil, false
	}
	iterInfo, ok := nativeExprTypeInfo(ctx, s.Iter)
	if !ok || iterInfo.kind != nativeExprInfoList || iterInfo.listElemType == "" {
		return nil, false
	}
	iterExpr, ok := nativeExprFromIR(ctx, s.Iter)
	if !ok || iterExpr.llvmType != "ptr" {
		return nil, false
	}
	var listName string
	var out []*llvmNativeStmt
	if iterExpr.kind == llvmNativeExprIdent && iterExpr.name != "" {
		listName = iterExpr.name
	} else {
		listName = ctx.freshTempName("__osty_native_list")
		out = append(out, &llvmNativeStmt{
			kind:       llvmNativeStmtLet,
			name:       listName,
			childExprs: []*llvmNativeExpr{iterExpr},
		})
		ctx.bindScopeName(listName, iterInfo)
	}
	indexName := ctx.freshTempName("__osty_native_i")

	// Bind loop-index and element names in the current scope so the
	// body's IR lowering resolves them correctly. nativeBlockFromIR
	// pushes its own inner scope on top, so these outer bindings
	// shadow nothing the body might introduce.
	ctx.pushScope()
	ctx.bindScopeName(indexName, nativeExprInfoFromLLVMType("i64"))
	elemInfo, ok := nativeForInElemInfo(ctx, s.Iter, iterInfo)
	if !ok {
		ctx.popScope()
		return nil, false
	}
	ctx.bindScopeName(s.Var, elemInfo)
	bodyBlock, ok := nativeBlockFromIR(ctx, s.Body, fnReturnType)
	ctx.popScope()
	if !ok {
		return nil, false
	}

	// Prepend `let s.Var = listName[indexName]` to the body.
	listIdent := func() *llvmNativeExpr {
		return &llvmNativeExpr{kind: llvmNativeExprIdent, llvmType: "ptr", name: listName}
	}
	indexIdent := &llvmNativeExpr{kind: llvmNativeExprIdent, llvmType: "i64", name: indexName}
	elemExpr := &llvmNativeExpr{
		kind:         llvmNativeExprListIndex,
		llvmType:     iterInfo.listElemType,
		elemLLVMType: iterInfo.listElemType,
		childExprs:   []*llvmNativeExpr{listIdent(), indexIdent},
	}
	extractStmt := &llvmNativeStmt{
		kind:       llvmNativeStmtLet,
		name:       s.Var,
		childExprs: []*llvmNativeExpr{elemExpr},
	}
	bodyBlock.stmts = append([]*llvmNativeStmt{extractStmt}, bodyBlock.stmts...)

	// Build Range 0..listName.len().
	startExpr := &llvmNativeExpr{kind: llvmNativeExprInt, llvmType: "i64", text: "0"}
	ctx.needsListRT = true
	endExpr := nativeRuntimeCallExpr("i64", llvmListRuntimeLenSymbol(), listIdent())
	out = append(out, &llvmNativeStmt{
		kind:        llvmNativeStmtRange,
		name:        indexName,
		inclusive:   false,
		childExprs:  []*llvmNativeExpr{startExpr, endExpr},
		childBlocks: []*llvmNativeBlock{bodyBlock},
	})
	return out, true
}

// nativeForInElemInfo derives the element's native expr-info from
// the iter's list info. For scalar element lists the list info's
// llvm type lookup suffices; for tuple / struct elements we
// reach through to the registered info by resolving the iter's
// IR element type.
func nativeForInElemInfo(ctx *nativeProjectionCtx, iter ostyir.Expr, iterInfo nativeExprInfo) (nativeExprInfo, bool) {
	if iter == nil {
		return nativeExprInfoFromLLVMType(iterInfo.listElemType), iterInfo.listElemType != ""
	}
	named, ok := iter.Type().(*ostyir.NamedType)
	if ok && named != nil && named.Builtin && named.Name == "List" && len(named.Args) == 1 {
		if info, ok := nativeExprInfoFromType(ctx, named.Args[0]); ok {
			return info, true
		}
	}
	if iterInfo.listElemType == "" {
		return nativeExprInfo{}, false
	}
	return nativeExprInfoFromLLVMType(iterInfo.listElemType), true
}

// nativeLetTupleDestructureStmts lowers `let (a, b, c, ...) = rhs`
// into a sequence of native `let` statements — one for the spilled
// tuple temp (when the RHS is not already a bare ident) and one per
// destructured element. Element patterns must be plain `IdentPat`
// bindings; nested patterns, wildcards, and mut bindings defer to
// the legacy bridge until follow-up work extends coverage.
func nativeLetTupleDestructureStmts(ctx *nativeProjectionCtx, s *ostyir.LetStmt) ([]*llvmNativeStmt, bool) {
	if ctx == nil || s == nil || s.Value == nil {
		return nil, false
	}
	pat, ok := s.Pattern.(*ostyir.TuplePat)
	if !ok || pat == nil {
		return nil, false
	}
	info, ok := nativeTupleInfoFromType(ctx, s.Value.Type())
	if !ok {
		return nil, false
	}
	if len(pat.Elems) != len(info.elemLLVMTypes) {
		return nil, false
	}
	// Collect the element binder names up-front so we can reject any
	// non-trivial sub-pattern before emitting partial output.
	elemNames := make([]string, 0, len(pat.Elems))
	for _, elem := range pat.Elems {
		id, ok := elem.(*ostyir.IdentPat)
		if !ok || id == nil || id.Name == "" || id.Mut {
			return nil, false
		}
		elemNames = append(elemNames, id.Name)
	}
	value, ok := nativeExprFromIR(ctx, s.Value)
	if !ok {
		return nil, false
	}
	if value.llvmType != info.def.llvmType {
		return nil, false
	}
	out := make([]*llvmNativeStmt, 0, len(elemNames)+1)
	// If the RHS is already a plain ident we can skip the spill and
	// reuse the source ident directly for each field access. Complex
	// expressions spill to a synthesized temp so side-effects fire
	// exactly once.
	var baseName string
	if value.kind == llvmNativeExprIdent && value.name != "" {
		baseName = value.name
	} else {
		baseName = ctx.freshTempName("__osty_native_t")
		out = append(out, &llvmNativeStmt{
			kind:       llvmNativeStmtLet,
			name:       baseName,
			childExprs: []*llvmNativeExpr{value},
		})
		ctx.bindScopeName(baseName, nativeExprInfoFromLLVMType(info.def.llvmType))
	}
	for i, name := range elemNames {
		elemLLVM := info.elemLLVMTypes[i]
		base := &llvmNativeExpr{
			kind:     llvmNativeExprIdent,
			llvmType: info.def.llvmType,
			name:     baseName,
		}
		field := &llvmNativeExpr{
			kind:       llvmNativeExprField,
			llvmType:   elemLLVM,
			fieldIndex: i,
			childExprs: []*llvmNativeExpr{base},
		}
		out = append(out, &llvmNativeStmt{
			kind:       llvmNativeStmtLet,
			name:       name,
			childExprs: []*llvmNativeExpr{field},
		})
		ctx.bindScopeName(name, nativeExprInfoFromLLVMType(elemLLVM))
	}
	return out, true
}

func nativeStmtFromIR(ctx *nativeProjectionCtx, stmt ostyir.Stmt, fnReturnType string) (*llvmNativeStmt, bool) {
	switch s := stmt.(type) {
	case nil:
		return nil, true
	case *ostyir.LetStmt:
		if s.Pattern != nil || s.Value == nil {
			return nil, false
		}
		value, ok := nativeExprFromIR(ctx, s.Value)
		if !ok {
			return nil, false
		}
		kind := llvmNativeStmtLet
		if s.Mut {
			kind = llvmNativeStmtMutLet
		}
		if info, ok := nativeExprTypeInfo(ctx, s.Value); ok {
			ctx.bindScopeName(s.Name, info)
		} else if info, ok := nativeExprInfoFromType(ctx, firstNonNilType(s.Type, s.Value.Type())); ok {
			ctx.bindScopeName(s.Name, info)
		} else {
			ctx.bindScopeName(s.Name, nativeExprInfoFromLLVMType(value.llvmType))
		}
		return &llvmNativeStmt{
			kind:       kind,
			name:       s.Name,
			childExprs: []*llvmNativeExpr{value},
		}, true
	case *ostyir.ExprStmt:
		if ifLet, ok := s.X.(*ostyir.IfLetExpr); ok {
			if stmt, ok := nativeIfLetVariantStmt(ctx, ifLet, fnReturnType); ok {
				return stmt, true
			}
		}
		expr, ok := nativeExprFromIR(ctx, s.X)
		if !ok {
			return nil, false
		}
		return &llvmNativeStmt{
			kind:       llvmNativeStmtExpr,
			childExprs: []*llvmNativeExpr{expr},
		}, true
	case *ostyir.AssignStmt:
		if len(s.Targets) != 1 {
			return nil, false
		}
		value, ok := nativeExprFromIR(ctx, s.Value)
		if !ok {
			return nil, false
		}
		switch target := s.Targets[0].(type) {
		case *ostyir.Ident:
			if target.Kind == ostyir.IdentGlobal && !ctx.mutableGlobals[target.Name] {
				return nil, false
			}
			return &llvmNativeStmt{
				kind:       llvmNativeStmtAssign,
				name:       target.Name,
				op:         nativeAssignOpString(s.Op),
				childExprs: []*llvmNativeExpr{value},
			}, true
		case *ostyir.FieldExpr:
			return nativeFieldAssignStmtFromIR(ctx, target, value, s.Op)
		default:
			return nil, false
		}
	case *ostyir.ReturnStmt:
		out := &llvmNativeStmt{
			kind:     llvmNativeStmtReturn,
			llvmType: fnReturnType,
		}
		if s.Value != nil {
			value, ok := nativeExprFromIR(ctx, s.Value)
			if !ok {
				return nil, false
			}
			out.childExprs = []*llvmNativeExpr{value}
		}
		return out, true
	case *ostyir.IfStmt:
		cond, ok := nativeExprFromIR(ctx, s.Cond)
		if !ok {
			return nil, false
		}
		thenBlock, ok := nativeBlockFromIR(ctx, s.Then, fnReturnType)
		if !ok {
			return nil, false
		}
		out := &llvmNativeStmt{
			kind:        llvmNativeStmtIf,
			childExprs:  []*llvmNativeExpr{cond},
			childBlocks: []*llvmNativeBlock{thenBlock},
		}
		if s.Else != nil {
			elseBlock, ok := nativeBlockFromIR(ctx, s.Else, fnReturnType)
			if !ok {
				return nil, false
			}
			out.childBlocks = append(out.childBlocks, elseBlock)
		}
		return out, true
	case *ostyir.ForStmt:
		switch s.Kind {
		case ostyir.ForWhile:
			cond, ok := nativeExprFromIR(ctx, s.Cond)
			if !ok {
				return nil, false
			}
			body, ok := nativeBlockFromIR(ctx, s.Body, fnReturnType)
			if !ok {
				return nil, false
			}
			return &llvmNativeStmt{
				kind:        llvmNativeStmtWhile,
				childExprs:  []*llvmNativeExpr{cond},
				childBlocks: []*llvmNativeBlock{body},
			}, true
		case ostyir.ForRange:
			if s.IsDestructured() || s.Var == "" {
				return nil, false
			}
			start, ok := nativeExprFromIR(ctx, s.Start)
			if !ok {
				return nil, false
			}
			end, ok := nativeExprFromIR(ctx, s.End)
			if !ok {
				return nil, false
			}
			body, ok := nativeBlockFromIR(ctx, s.Body, fnReturnType)
			if !ok {
				return nil, false
			}
			return &llvmNativeStmt{
				kind:        llvmNativeStmtRange,
				name:        s.Var,
				inclusive:   s.Inclusive,
				childExprs:  []*llvmNativeExpr{start, end},
				childBlocks: []*llvmNativeBlock{body},
			}, true
		default:
			return nil, false
		}
	default:
		return nil, false
	}
}

// nativeIfLetVariantStmt lowers `if let Enum.Variant(<bindings>) =
// scrutinee { then } else { else }` (wrapped as
// `ExprStmt{IfLetExpr}`) into a native if statement that compares
// the enum's tag field against the variant's discriminant and, on
// the then-branch, synthesizes let bindings for any payload idents
// the pattern named.
//
// Today the helper only covers `VariantPat` with at most one
// `IdentPat` binding and a single-typed payload slot — the tests
// lock the `Maybe<Int>` / `Maybe.Some(x)` / `Maybe.None` shapes.
// Multi-binding patterns, wildcard payloads, and nested patterns
// return (nil, false) so the caller falls back to the legacy
// bridge (still wired via `GenerateModule`) until a follow-up
// extends this coverage.
func nativeIfLetVariantStmt(
	ctx *nativeProjectionCtx,
	ifLet *ostyir.IfLetExpr,
	fnReturnType string,
) (*llvmNativeStmt, bool) {
	if ctx == nil || ifLet == nil {
		return nil, false
	}
	varPat, ok := ifLet.Pattern.(*ostyir.VariantPat)
	if !ok {
		return nil, false
	}
	info, ok := nativeEnumInfoFromType(ctx, ifLet.Scrutinee.Type())
	if !ok {
		return nil, false
	}
	variant, ok := info.variantsByName[varPat.Variant]
	if !ok {
		return nil, false
	}
	// Pattern arg count must match the variant's payload arity
	// exactly; mixed shapes defer to the legacy bridge.
	if len(varPat.Args) != len(variant.payloadIRTypes) {
		return nil, false
	}
	scrutinee, ok := nativeExprFromIR(ctx, ifLet.Scrutinee)
	if !ok {
		return nil, false
	}
	if scrutinee.llvmType != info.def.llvmType {
		return nil, false
	}
	tagExpr := &llvmNativeExpr{
		kind:       llvmNativeExprField,
		llvmType:   "i64",
		fieldIndex: 0,
		childExprs: []*llvmNativeExpr{scrutinee},
	}
	tagConst := &llvmNativeExpr{
		kind:     llvmNativeExprInt,
		llvmType: "i64",
		text:     strconv.Itoa(variant.tag),
	}
	cond := &llvmNativeExpr{
		kind:       llvmNativeExprBinary,
		llvmType:   "i1",
		op:         "==",
		childExprs: []*llvmNativeExpr{tagExpr, tagConst},
	}

	var bindingStmts []*llvmNativeStmt
	ctx.pushScope()
	if len(varPat.Args) == 1 && variant.payloadLLVMType != "" {
		idPat, ok := varPat.Args[0].(*ostyir.IdentPat)
		if !ok || idPat.Name == "" || idPat.Mut {
			ctx.popScope()
			return nil, false
		}
		scrutineeCopy, ok := nativeExprFromIR(ctx, ifLet.Scrutinee)
		if !ok {
			ctx.popScope()
			return nil, false
		}
		payloadExpr := &llvmNativeExpr{
			kind:       llvmNativeExprField,
			llvmType:   variant.payloadLLVMType,
			fieldIndex: 1,
			childExprs: []*llvmNativeExpr{scrutineeCopy},
		}
		bindingStmts = append(bindingStmts, &llvmNativeStmt{
			kind:       llvmNativeStmtLet,
			name:       idPat.Name,
			childExprs: []*llvmNativeExpr{payloadExpr},
		})
		ctx.bindScopeName(idPat.Name, nativeExprInfoFromLLVMType(variant.payloadLLVMType))
	}
	thenBlock, ok := nativeBlockFromIR(ctx, ifLet.Then, fnReturnType)
	ctx.popScope()
	if !ok {
		return nil, false
	}
	if len(bindingStmts) != 0 {
		thenBlock.stmts = append(bindingStmts, thenBlock.stmts...)
	}

	out := &llvmNativeStmt{
		kind:        llvmNativeStmtIf,
		childExprs:  []*llvmNativeExpr{cond},
		childBlocks: []*llvmNativeBlock{thenBlock},
	}
	if ifLet.Else != nil {
		elseBlock, ok := nativeBlockFromIR(ctx, ifLet.Else, fnReturnType)
		if !ok {
			return nil, false
		}
		out.childBlocks = append(out.childBlocks, elseBlock)
	}
	return out, true
}

func nativeFieldAssignStmtFromIR(
	ctx *nativeProjectionCtx,
	target *ostyir.FieldExpr,
	value *llvmNativeExpr,
	op ostyir.AssignOp,
) (*llvmNativeStmt, bool) {
	base, fieldPath, ok := nativeFieldAssignPathFromIR(ctx, target)
	if !ok || value == nil || len(fieldPath) == 0 {
		return nil, false
	}
	if value.llvmType != fieldPath[len(fieldPath)-1].llvmType {
		return nil, false
	}
	return &llvmNativeStmt{
		kind:       llvmNativeStmtFieldAssign,
		name:       base.Name,
		op:         nativeAssignOpString(op),
		childExprs: []*llvmNativeExpr{value},
		fieldPath:  fieldPath,
	}, true
}

func nativeFieldAssignPathFromIR(
	ctx *nativeProjectionCtx,
	target *ostyir.FieldExpr,
) (*ostyir.Ident, []*llvmNativeFieldPath, bool) {
	if ctx == nil || target == nil {
		return nil, nil, false
	}
	fields := []*ostyir.FieldExpr{}
	cur := target
	for {
		if cur == nil || cur.Optional {
			return nil, nil, false
		}
		fields = append([]*ostyir.FieldExpr{cur}, fields...)
		next, ok := cur.X.(*ostyir.FieldExpr)
		if !ok {
			break
		}
		cur = next
	}
	base, ok := cur.X.(*ostyir.Ident)
	if !ok || !nativeWritableBaseIdent(ctx, base) {
		return nil, nil, false
	}
	fieldPath := make([]*llvmNativeFieldPath, 0, len(fields))
	currentType := base.Type()
	for _, fieldExpr := range fields {
		info, ok := nativeStructInfoFromType(ctx, currentType)
		if !ok {
			return nil, nil, false
		}
		field, ok := info.byName[fieldExpr.Name]
		if !ok {
			return nil, nil, false
		}
		fieldPath = append(fieldPath, &llvmNativeFieldPath{
			llvmType:   field.llvmType,
			fieldIndex: field.index,
		})
		currentType = fieldExpr.Type()
	}
	return base, fieldPath, true
}

func nativeWritableBaseIdent(ctx *nativeProjectionCtx, ident *ostyir.Ident) bool {
	if ctx == nil || ident == nil {
		return false
	}
	switch ident.Kind {
	case ostyir.IdentLocal, ostyir.IdentParam:
		return true
	case ostyir.IdentGlobal:
		return ctx.mutableGlobals[ident.Name]
	default:
		return false
	}
}

func nativeGlobalLetFromIR(ctx *nativeProjectionCtx, decl *ostyir.LetDecl) (*llvmNativeGlobal, bool) {
	if ctx == nil || decl == nil || decl.Name == "" || decl.Value == nil {
		return nil, false
	}
	llvmType, ok := nativeLLVMTypeFromIR(ctx, firstNonNilType(decl.Type, decl.Value.Type()))
	if !ok || llvmType == "void" {
		return nil, false
	}
	value, ok := nativeConstFromIR(ctx, decl.Value)
	if !ok || value.llvmType != llvmType {
		return nil, false
	}
	global := &llvmNativeGlobal{
		name:     decl.Name,
		irName:   llvmGlobalIRName(decl.Name),
		llvmType: llvmType,
		mutable:  decl.Mut,
		init:     value.init,
	}
	ctx.globalConsts[decl.Name] = value
	ctx.mutableGlobals[decl.Name] = decl.Mut
	return global, true
}

func nativeConstFromIR(ctx *nativeProjectionCtx, expr ostyir.Expr) (nativeConstValue, bool) {
	switch e := expr.(type) {
	case nil:
		return nativeConstValue{}, false
	case *ostyir.IntLit:
		text, ok := normalizeNativeIntText(e.Text)
		if !ok {
			return nativeConstValue{}, false
		}
		llvmType, ok := nativeLLVMTypeFromIR(ctx, e.Type())
		if !ok {
			return nativeConstValue{}, false
		}
		return nativeConstValue{llvmType: llvmType, init: text}, true
	case *ostyir.FloatLit:
		llvmType, ok := nativeLLVMTypeFromIR(ctx, e.Type())
		if !ok {
			return nativeConstValue{}, false
		}
		return nativeConstValue{
			llvmType: llvmType,
			init:     llvmFloatConstLiteral(mustParseNativeFloat(e.Text)),
		}, true
	case *ostyir.BoolLit:
		if e.Value {
			return nativeConstValue{llvmType: "i1", init: "true"}, true
		}
		return nativeConstValue{llvmType: "i1", init: "false"}, true
	case *ostyir.StringLit:
		text, ok := nativePlainStringText(e)
		if !ok {
			return nativeConstValue{}, false
		}
		name := fmt.Sprintf("@.str%d", ctx.nextStringID)
		ctx.nextStringID++
		cstring := llvmCString(text)
		ctx.stringGlobals = append(ctx.stringGlobals, &LlvmStringGlobal{
			name:    name,
			encoded: cstring.encoded,
			byteLen: cstring.byteLen,
		})
		return nativeConstValue{llvmType: "ptr", init: name}, true
	case *ostyir.Ident:
		value, ok := ctx.globalConsts[e.Name]
		return value, ok
	case *ostyir.UnaryExpr:
		value, ok := nativeConstFromIR(ctx, e.X)
		if !ok {
			return nativeConstValue{}, false
		}
		switch nativeUnaryOpString(e.Op) {
		case "+":
			return value, value.llvmType == "i64" || value.llvmType == "double"
		case "-":
			switch value.llvmType {
			case "i64":
				n, err := strconv.ParseInt(value.init, 10, 64)
				if err != nil {
					return nativeConstValue{}, false
				}
				return nativeConstValue{llvmType: "i64", init: strconv.FormatInt(-n, 10)}, true
			case "double":
				f, err := strconv.ParseFloat(value.init, 64)
				if err != nil {
					return nativeConstValue{}, false
				}
				return nativeConstValue{llvmType: "double", init: llvmFloatConstLiteral(-f)}, true
			default:
				return nativeConstValue{}, false
			}
		case "!":
			if value.llvmType == "i1" && (value.init == "true" || value.init == "false") {
				return nativeConstValue{llvmType: "i1", init: strconv.FormatBool(value.init != "true")}, true
			}
			return nativeConstValue{}, false
		default:
			return nativeConstValue{}, false
		}
	case *ostyir.BinaryExpr:
		left, ok := nativeConstFromIR(ctx, e.Left)
		if !ok {
			return nativeConstValue{}, false
		}
		right, ok := nativeConstFromIR(ctx, e.Right)
		if !ok {
			return nativeConstValue{}, false
		}
		return nativeBinaryConst(left, right, e.Op)
	case *ostyir.StructLit:
		info, ok := nativeStructInfoFromType(ctx, e.Type())
		if !ok || e.Spread != nil {
			return nativeConstValue{}, false
		}
		fieldsByName := make(map[string]ostyir.StructLitField, len(e.Fields))
		for _, field := range e.Fields {
			if field.Name == "" {
				return nativeConstValue{}, false
			}
			if _, exists := fieldsByName[field.Name]; exists {
				return nativeConstValue{}, false
			}
			fieldsByName[field.Name] = field
		}
		parts := make([]string, 0, len(info.def.fields))
		for _, field := range info.def.fields {
			entry, ok := fieldsByName[field.name]
			if !ok {
				return nativeConstValue{}, false
			}
			var value nativeConstValue
			if entry.Value == nil {
				value, ok = ctx.globalConsts[field.name]
			} else {
				value, ok = nativeConstFromIR(ctx, entry.Value)
			}
			if !ok || value.llvmType != field.llvmType {
				return nativeConstValue{}, false
			}
			parts = append(parts, value.llvmType+" "+value.init)
		}
		return nativeConstValue{
			llvmType: info.def.llvmType,
			init:     "{ " + strings.Join(parts, ", ") + " }",
		}, true
	default:
		return nativeConstValue{}, false
	}
}

func nativeBinaryConst(left, right nativeConstValue, op ostyir.BinOp) (nativeConstValue, bool) {
	switch op {
	case ostyir.BinAdd, ostyir.BinSub, ostyir.BinMul, ostyir.BinDiv, ostyir.BinMod:
		if left.llvmType == "double" && right.llvmType == "double" {
			lf, err := strconv.ParseFloat(left.init, 64)
			if err != nil {
				return nativeConstValue{}, false
			}
			rf, err := strconv.ParseFloat(right.init, 64)
			if err != nil {
				return nativeConstValue{}, false
			}
			var out float64
			switch op {
			case ostyir.BinAdd:
				out = lf + rf
			case ostyir.BinSub:
				out = lf - rf
			case ostyir.BinMul:
				out = lf * rf
			case ostyir.BinDiv:
				out = lf / rf
			default:
				return nativeConstValue{}, false
			}
			return nativeConstValue{llvmType: "double", init: llvmFloatConstLiteral(out)}, true
		}
		if left.llvmType != "i64" || right.llvmType != "i64" {
			return nativeConstValue{}, false
		}
		li, err := strconv.ParseInt(left.init, 10, 64)
		if err != nil {
			return nativeConstValue{}, false
		}
		ri, err := strconv.ParseInt(right.init, 10, 64)
		if err != nil {
			return nativeConstValue{}, false
		}
		var out int64
		switch op {
		case ostyir.BinAdd:
			out = li + ri
		case ostyir.BinSub:
			out = li - ri
		case ostyir.BinMul:
			out = li * ri
		case ostyir.BinDiv:
			if ri == 0 {
				return nativeConstValue{}, false
			}
			out = li / ri
		case ostyir.BinMod:
			if ri == 0 {
				return nativeConstValue{}, false
			}
			out = li % ri
		}
		return nativeConstValue{llvmType: "i64", init: strconv.FormatInt(out, 10)}, true
	case ostyir.BinAnd, ostyir.BinOr:
		if left.llvmType != "i1" || right.llvmType != "i1" {
			return nativeConstValue{}, false
		}
		lb := left.init == "true"
		rb := right.init == "true"
		out := lb && rb
		if op == ostyir.BinOr {
			out = lb || rb
		}
		return nativeConstValue{llvmType: "i1", init: strconv.FormatBool(out)}, true
	default:
		return nativeConstValue{}, false
	}
}

func firstNonNilType(primary, fallback ostyir.Type) ostyir.Type {
	if primary != nil {
		return primary
	}
	return fallback
}

func mustParseNativeFloat(text string) float64 {
	value, err := strconv.ParseFloat(strings.ReplaceAll(text, "_", ""), 64)
	if err != nil {
		return 0
	}
	return value
}

func nativeExprFromIR(ctx *nativeProjectionCtx, expr ostyir.Expr) (*llvmNativeExpr, bool) {
	switch e := expr.(type) {
	case nil:
		return nil, false
	case *ostyir.IntLit:
		text, ok := normalizeNativeIntText(e.Text)
		if !ok {
			return nil, false
		}
		llvmType, ok := nativeLLVMTypeFromIR(ctx, e.Type())
		if !ok {
			return nil, false
		}
		return &llvmNativeExpr{kind: llvmNativeExprInt, llvmType: llvmType, text: text}, true
	case *ostyir.FloatLit:
		llvmType, ok := nativeLLVMTypeFromIR(ctx, e.Type())
		if !ok {
			return nil, false
		}
		return &llvmNativeExpr{
			kind:     llvmNativeExprFloat,
			llvmType: llvmType,
			text:     strings.ReplaceAll(e.Text, "_", ""),
		}, true
	case *ostyir.BoolLit:
		return &llvmNativeExpr{kind: llvmNativeExprBool, llvmType: "i1", boolValue: e.Value}, true
	case *ostyir.StringLit:
		text, ok := nativePlainStringText(e)
		if !ok {
			return nil, false
		}
		return &llvmNativeExpr{kind: llvmNativeExprString, llvmType: "ptr", text: text}, true
	case *ostyir.CharLit:
		return &llvmNativeExpr{kind: llvmNativeExprInt, llvmType: "i32", text: strconv.FormatInt(int64(e.Value), 10)}, true
	case *ostyir.ByteLit:
		return &llvmNativeExpr{kind: llvmNativeExprInt, llvmType: "i8", text: strconv.FormatInt(int64(e.Value), 10)}, true
	case *ostyir.ListLit:
		listInfo, ok := nativeExprInfoFromType(ctx, e.Type())
		if !ok || listInfo.kind != nativeExprInfoList {
			return nil, false
		}
		out := &llvmNativeExpr{
			kind:         llvmNativeExprListLit,
			llvmType:     "ptr",
			elemLLVMType: listInfo.listElemType,
			childExprs:   make([]*llvmNativeExpr, 0, len(e.Elems)),
		}
		for _, elem := range e.Elems {
			value, ok := nativeExprFromIRWithHint(ctx, elem, listInfo.listElemType)
			if !ok || value.llvmType != listInfo.listElemType {
				return nil, false
			}
			out.childExprs = append(out.childExprs, value)
		}
		ctx.needsListRT = true
		return out, true
	case *ostyir.MapLit:
		mapInfo, ok := nativeMapLiteralInfo(ctx, e)
		if !ok || mapInfo.kind != nativeExprInfoMap {
			return nil, false
		}
		out := &llvmNativeExpr{
			kind:           llvmNativeExprMapLit,
			llvmType:       "ptr",
			mapKeyLLVMType: mapInfo.mapKeyType,
			mapKeyIsString: mapInfo.mapKeyString,
			childExprs:     make([]*llvmNativeExpr, 0, len(e.Entries)*2),
		}
		for _, entry := range e.Entries {
			key, ok := nativeExprFromIRWithHint(ctx, entry.Key, mapInfo.mapKeyType)
			if !ok || key.llvmType != mapInfo.mapKeyType {
				return nil, false
			}
			value, ok := nativeExprFromIRWithHint(ctx, entry.Value, mapInfo.mapValueType)
			if !ok || value.llvmType != mapInfo.mapValueType {
				return nil, false
			}
			out.childExprs = append(out.childExprs, key, value)
		}
		ctx.needsMapRT = true
		return out, true
	case *ostyir.TupleLit:
		info, ok := nativeTupleInfoFromType(ctx, e.Type())
		if !ok {
			return nil, false
		}
		if len(e.Elems) != len(info.elemLLVMTypes) {
			return nil, false
		}
		out := &llvmNativeExpr{
			kind:       llvmNativeExprStructLit,
			llvmType:   info.def.llvmType,
			childExprs: make([]*llvmNativeExpr, 0, len(e.Elems)),
		}
		for i, elem := range e.Elems {
			value, ok := nativeExprFromIRWithHint(ctx, elem, info.elemLLVMTypes[i])
			if !ok || value.llvmType != info.elemLLVMTypes[i] {
				return nil, false
			}
			out.childExprs = append(out.childExprs, value)
		}
		return out, true
	case *ostyir.Ident:
		llvmType, ok := nativeLLVMTypeFromIR(ctx, e.Type())
		if !ok {
			info, ok := ctx.lookupScopeName(e.Name)
			if !ok {
				return nil, false
			}
			llvmType = info.llvmType
		}
		return &llvmNativeExpr{kind: llvmNativeExprIdent, llvmType: llvmType, name: e.Name}, true
	case *ostyir.StructLit:
		info, ok := nativeStructInfoFromType(ctx, e.Type())
		if !ok && e.TypeName != "" {
			info = ctx.structsByName[e.TypeName]
			ok = info != nil
		}
		if !ok || e.Spread != nil {
			return nil, false
		}
		fieldsByName := make(map[string]ostyir.StructLitField, len(e.Fields))
		for _, field := range e.Fields {
			if field.Name == "" {
				return nil, false
			}
			if _, exists := fieldsByName[field.Name]; exists {
				return nil, false
			}
			fieldsByName[field.Name] = field
		}
		out := &llvmNativeExpr{
			kind:       llvmNativeExprStructLit,
			llvmType:   info.def.llvmType,
			childExprs: make([]*llvmNativeExpr, 0, len(info.def.fields)),
		}
		for _, field := range info.def.fields {
			entry, ok := fieldsByName[field.name]
			if !ok {
				return nil, false
			}
			if entry.Value == nil {
				out.childExprs = append(out.childExprs, &llvmNativeExpr{
					kind:     llvmNativeExprIdent,
					llvmType: field.llvmType,
					name:     field.name,
				})
				continue
			}
			value, ok := nativeExprFromIRWithHint(ctx, entry.Value, field.llvmType)
			if !ok || value.llvmType != field.llvmType {
				return nil, false
			}
			out.childExprs = append(out.childExprs, value)
		}
		return out, true
	case *ostyir.VariantLit:
		return nativeVariantLitFromIR(ctx, e)
	case *ostyir.FieldExpr:
		// Bare variant access on an enum type name — `Maybe.None` —
		// lowers to a VariantLit-equivalent zero-argument variant
		// construction. The IR represents this as
		// `FieldExpr{X: Ident{Kind: IdentTypeName, Name: <enum>},
		// Name: <variant>}` rather than a `VariantLit` because the
		// surface syntax has no parens to disambiguate from a field
		// access. Detect the shape up front and route to the same
		// variant projector that handles `Maybe.Some(x)`.
		if expr, ok := nativeBareVariantAccessFromIR(ctx, e); ok {
			return expr, true
		}
		if e.Optional {
			baseInfo, ok := nativeExprTypeInfo(ctx, e.X)
			if !ok || baseInfo.kind != nativeExprInfoOptional {
				return nil, false
			}
			baseSource, ok := unwrapOptionalIRType(nativeSourceTypeFromExprOrInfo(e.X, baseInfo))
			if !ok {
				return nil, false
			}
			info, ok := nativeStructInfoFromType(ctx, baseSource)
			if !ok {
				return nil, false
			}
			field, ok := info.byName[e.Name]
			if !ok || field.llvmType != "ptr" {
				return nil, false
			}
			base, ok := nativeExprFromIR(ctx, e.X)
			if !ok {
				return nil, false
			}
			return &llvmNativeExpr{
				kind:                llvmNativeExprOptionalField,
				llvmType:            "ptr",
				fieldIndex:          field.index,
				baseLLVMType:        info.def.llvmType,
				optionInnerLLVMType: field.llvmType,
				childExprs:          []*llvmNativeExpr{base},
			}, true
		}
		baseInfo, ok := nativeExprTypeInfo(ctx, e.X)
		if !ok || baseInfo.kind != nativeExprInfoStruct || baseInfo.structName == "" {
			return nil, false
		}
		info := ctx.structsByName[baseInfo.structName]
		if info == nil {
			return nil, false
		}
		field, ok := info.byName[e.Name]
		if !ok {
			return nil, false
		}
		base, ok := nativeExprFromIR(ctx, e.X)
		if !ok {
			return nil, false
		}
		return &llvmNativeExpr{
			kind:       llvmNativeExprField,
			llvmType:   field.llvmType,
			name:       field.name,
			fieldIndex: field.index,
			childExprs: []*llvmNativeExpr{base},
		}, true
	case *ostyir.IndexExpr:
		baseInfo, ok := nativeExprTypeInfo(ctx, e.X)
		if !ok {
			return nil, false
		}
		base, ok := nativeExprFromIR(ctx, e.X)
		if !ok {
			return nil, false
		}
		switch baseInfo.kind {
		case nativeExprInfoList:
			index, ok := nativeExprFromIRWithHint(ctx, e.Index, "i64")
			if !ok {
				return nil, false
			}
			llvmType, ok := nativeLLVMTypeFromIR(ctx, e.Type())
			if !ok {
				resultInfo, ok := nativeExprTypeInfo(ctx, e)
				if !ok || resultInfo.llvmType == "" {
					return nil, false
				}
				llvmType = resultInfo.llvmType
			}
			if llvmType == "" {
				return nil, false
			}
			ctx.needsListRT = true
			return &llvmNativeExpr{
				kind:         llvmNativeExprListIndex,
				llvmType:     llvmType,
				elemLLVMType: baseInfo.listElemType,
				childExprs:   []*llvmNativeExpr{base, index},
			}, true
		case nativeExprInfoMap:
			key, ok := nativeExprFromIRWithHint(ctx, e.Index, baseInfo.mapKeyType)
			if !ok {
				return nil, false
			}
			llvmType, ok := nativeLLVMTypeFromIR(ctx, e.Type())
			if !ok {
				resultInfo, ok := nativeExprTypeInfo(ctx, e)
				if !ok || resultInfo.llvmType == "" {
					return nil, false
				}
				llvmType = resultInfo.llvmType
			}
			if llvmType == "" {
				return nil, false
			}
			ctx.needsMapRT = true
			return &llvmNativeExpr{
				kind:           llvmNativeExprMapIndex,
				llvmType:       llvmType,
				mapKeyLLVMType: baseInfo.mapKeyType,
				mapKeyIsString: baseInfo.mapKeyString,
				childExprs:     []*llvmNativeExpr{base, key},
			}, true
		default:
			return nil, false
		}
	case *ostyir.TupleAccess:
		info, ok := nativeTupleInfoFromType(ctx, e.X.Type())
		if !ok || e.Index < 0 || e.Index >= len(info.elemLLVMTypes) {
			return nil, false
		}
		base, ok := nativeExprFromIR(ctx, e.X)
		if !ok {
			return nil, false
		}
		return &llvmNativeExpr{
			kind:       llvmNativeExprField,
			llvmType:   info.elemLLVMTypes[e.Index],
			fieldIndex: e.Index,
			childExprs: []*llvmNativeExpr{base},
		}, true
	case *ostyir.UnaryExpr:
		inner, ok := nativeExprFromIR(ctx, e.X)
		if !ok {
			return nil, false
		}
		llvmType, ok := nativeLLVMTypeFromIR(ctx, e.Type())
		if !ok {
			return nil, false
		}
		return &llvmNativeExpr{
			kind:       llvmNativeExprUnary,
			llvmType:   llvmType,
			op:         nativeUnaryOpString(e.Op),
			childExprs: []*llvmNativeExpr{inner},
		}, true
	case *ostyir.BinaryExpr:
		left, ok := nativeExprFromIR(ctx, e.Left)
		if !ok {
			return nil, false
		}
		right, ok := nativeExprFromIR(ctx, e.Right)
		if !ok {
			return nil, false
		}
		llvmType, ok := nativeLLVMTypeFromIR(ctx, e.Type())
		if !ok {
			llvmType, ok = nativeBinaryResultLLVMType(e.Op, left.llvmType, right.llvmType)
			if !ok {
				return nil, false
			}
		}
		return &llvmNativeExpr{
			kind:       llvmNativeExprBinary,
			llvmType:   llvmType,
			op:         nativeBinaryOpString(e.Op),
			childExprs: []*llvmNativeExpr{left, right},
		}, true
	case *ostyir.QuestionExpr:
		if ctx == nil || ctx.currentReturnLLVMType != "ptr" {
			return nil, false
		}
		baseInfo, ok := nativeExprTypeInfo(ctx, e.X)
		if !ok || baseInfo.kind != nativeExprInfoOptional {
			return nil, false
		}
		base, ok := nativeExprFromIR(ctx, e.X)
		if !ok {
			return nil, false
		}
		llvmType, ok := nativeLLVMTypeFromIR(ctx, e.Type())
		if !ok {
			llvmType = firstNonEmpty(baseInfo.optionInnerType, "")
			ok = llvmType != ""
		}
		if !ok || llvmType == "" || (baseInfo.optionInnerType != "" && baseInfo.optionInnerType != llvmType) {
			return nil, false
		}
		return &llvmNativeExpr{
			kind:                llvmNativeExprQuestion,
			llvmType:            llvmType,
			optionInnerLLVMType: firstNonEmpty(baseInfo.optionInnerType, llvmType),
			childExprs:          []*llvmNativeExpr{base},
		}, true
	case *ostyir.CoalesceExpr:
		leftInfo, ok := nativeExprTypeInfo(ctx, e.Left)
		if !ok || leftInfo.kind != nativeExprInfoOptional {
			return nil, false
		}
		left, ok := nativeExprFromIR(ctx, e.Left)
		if !ok {
			return nil, false
		}
		llvmType, ok := nativeLLVMTypeFromIR(ctx, e.Type())
		if !ok {
			llvmType = firstNonEmpty(leftInfo.optionInnerType, "")
			ok = llvmType != ""
		}
		if !ok || llvmType == "" || (leftInfo.optionInnerType != "" && leftInfo.optionInnerType != llvmType) {
			return nil, false
		}
		right, ok := nativeExprFromIRWithHint(ctx, e.Right, llvmType)
		if !ok || right.llvmType != llvmType {
			return nil, false
		}
		return &llvmNativeExpr{
			kind:                llvmNativeExprCoalesce,
			llvmType:            llvmType,
			optionInnerLLVMType: firstNonEmpty(leftInfo.optionInnerType, llvmType),
			childExprs:          []*llvmNativeExpr{left, right},
		}, true
	case *ostyir.CallExpr:
		callee, ok := e.Callee.(*ostyir.Ident)
		if !ok || len(e.TypeArgs) != 0 {
			return nil, false
		}
		llvmType, ok := nativeLLVMTypeFromIR(ctx, e.Type())
		if !ok {
			return nil, false
		}
		out := &llvmNativeExpr{
			kind:       llvmNativeExprCall,
			llvmType:   llvmType,
			name:       callee.Name,
			childExprs: make([]*llvmNativeExpr, 0, len(e.Args)),
		}
		for _, arg := range e.Args {
			if arg.IsKeyword() {
				return nil, false
			}
			value, ok := nativeExprFromIR(ctx, arg.Value)
			if !ok {
				return nil, false
			}
			out.childExprs = append(out.childExprs, value)
		}
		return out, true
	case *ostyir.MethodCall:
		if builtin, ok := nativeBuiltinMethodExprFromIR(ctx, e); ok {
			return builtin, true
		}
		if len(e.TypeArgs) != 0 {
			return nil, false
		}
		ownerName, ok := nativeMethodOwnerName(e.Receiver.Type())
		if !ok {
			return nil, false
		}
		methods := ctx.methodsByOwner[ownerName]
		info, ok := methods[e.Name]
		if !ok {
			return nil, false
		}
		llvmType, ok := nativeLLVMTypeFromIR(ctx, e.Type())
		if !ok {
			return nil, false
		}
		out := &llvmNativeExpr{
			kind:          llvmNativeExprCall,
			llvmType:      llvmType,
			name:          info.irName,
			firstArgByRef: info.receiverMut,
			childExprs:    make([]*llvmNativeExpr, 0, len(e.Args)+1),
		}
		if info.receiverMut {
			switch receiver := e.Receiver.(type) {
			case *ostyir.Ident:
				if !nativeWritableBaseIdent(ctx, receiver) {
					return nil, false
				}
				receiverValue, ok := nativeExprFromIR(ctx, receiver)
				if !ok {
					return nil, false
				}
				out.childExprs = append(out.childExprs, receiverValue)
			case *ostyir.FieldExpr:
				base, receiverPath, ok := nativeFieldAssignPathFromIR(ctx, receiver)
				if !ok {
					return nil, false
				}
				receiverValue, ok := nativeExprFromIR(ctx, base)
				if !ok {
					return nil, false
				}
				out.receiverPath = receiverPath
				out.childExprs = append(out.childExprs, receiverValue)
			default:
				return nil, false
			}
		} else {
			receiver, ok := nativeExprFromIR(ctx, e.Receiver)
			if !ok {
				return nil, false
			}
			out.childExprs = append(out.childExprs, receiver)
		}
		for _, arg := range e.Args {
			if arg.IsKeyword() {
				return nil, false
			}
			value, ok := nativeExprFromIR(ctx, arg.Value)
			if !ok {
				return nil, false
			}
			out.childExprs = append(out.childExprs, value)
		}
		return out, true
	case *ostyir.IntrinsicCall:
		if e.Kind != ostyir.IntrinsicPrintln || len(e.Args) != 1 || e.Args[0].IsKeyword() {
			return nil, false
		}
		value, ok := nativeExprFromIR(ctx, e.Args[0].Value)
		if !ok {
			return nil, false
		}
		return &llvmNativeExpr{
			kind:       llvmNativeExprPrintln,
			llvmType:   "void",
			childExprs: []*llvmNativeExpr{value},
		}, true
	case *ostyir.IfExpr:
		cond, ok := nativeExprFromIR(ctx, e.Cond)
		if !ok {
			return nil, false
		}
		thenBlock, ok := nativeBlockFromIR(ctx, e.Then, "")
		if !ok {
			return nil, false
		}
		elseBlock, ok := nativeBlockFromIR(ctx, e.Else, "")
		if !ok {
			return nil, false
		}
		llvmType, ok := nativeLLVMTypeFromIR(ctx, e.Type())
		if !ok {
			return nil, false
		}
		return &llvmNativeExpr{
			kind:        llvmNativeExprIf,
			llvmType:    llvmType,
			childExprs:  []*llvmNativeExpr{cond},
			childBlocks: []*llvmNativeBlock{thenBlock, elseBlock},
		}, true
	default:
		return nil, false
	}
}

func nativeBinaryResultLLVMType(op ostyir.BinOp, leftType string, rightType string) (string, bool) {
	switch op {
	case ostyir.BinEq, ostyir.BinNeq, ostyir.BinLt, ostyir.BinLeq, ostyir.BinGt, ostyir.BinGeq, ostyir.BinAnd, ostyir.BinOr:
		return "i1", leftType != "" && rightType != ""
	case ostyir.BinAdd, ostyir.BinSub, ostyir.BinMul, ostyir.BinDiv, ostyir.BinMod, ostyir.BinBitAnd, ostyir.BinBitOr, ostyir.BinBitXor, ostyir.BinShl, ostyir.BinShr:
		if leftType == "" || rightType == "" || leftType != rightType {
			return "", false
		}
		return leftType, true
	default:
		return "", false
	}
}

func nativeExprInfoFromLLVMType(llvmType string) nativeExprInfo {
	if llvmType == "" {
		return nativeExprInfo{}
	}
	return nativeExprInfo{kind: nativeExprInfoScalar, llvmType: llvmType}
}

func nativeExprInfoWithSource(info nativeExprInfo, sourceType ostyir.Type) nativeExprInfo {
	info.sourceType = sourceType
	return info
}

func nativeStringExprInfo() nativeExprInfo {
	return nativeExprInfo{kind: nativeExprInfoString, llvmType: "ptr"}
}

func nativeListExprInfo(elemType string, elemString bool, elemBytes bool) nativeExprInfo {
	return nativeExprInfo{
		kind:           nativeExprInfoList,
		llvmType:       "ptr",
		listElemType:   elemType,
		listElemString: elemString,
		listElemBytes:  elemBytes,
	}
}

func nativeMapExprInfo(keyType string, valueType string, keyString bool) nativeExprInfo {
	return nativeExprInfo{
		kind:         nativeExprInfoMap,
		llvmType:     "ptr",
		mapKeyType:   keyType,
		mapValueType: valueType,
		mapKeyString: keyString,
	}
}

func nativeSetExprInfo(elemType string, elemString bool) nativeExprInfo {
	return nativeExprInfo{
		kind:          nativeExprInfoSet,
		llvmType:      "ptr",
		setElemType:   elemType,
		setElemString: elemString,
	}
}

func nativeTypeResolved(t ostyir.Type) bool {
	switch t.(type) {
	case nil, *ostyir.ErrType, *ostyir.TypeVar:
		return false
	default:
		return true
	}
}

func nativeSourceTypeFromExprOrInfo(expr ostyir.Expr, info nativeExprInfo) ostyir.Type {
	if expr != nil && nativeTypeResolved(expr.Type()) {
		return expr.Type()
	}
	if nativeTypeResolved(info.sourceType) {
		return info.sourceType
	}
	switch info.kind {
	case nativeExprInfoString:
		return ostyir.TString
	case nativeExprInfoScalar:
		switch info.llvmType {
		case "i1":
			return ostyir.TBool
		case "i8":
			return ostyir.TByte
		case "i32":
			return ostyir.TChar
		case "i64":
			return ostyir.TInt
		case "float":
			return ostyir.TFloat32
		case "double":
			return ostyir.TFloat64
		}
	}
	return nil
}

func nativeBuiltinArgType(sourceType ostyir.Type, name string, arity int, index int) (ostyir.Type, bool) {
	named, ok := sourceType.(*ostyir.NamedType)
	if !ok || named == nil || !named.Builtin || named.Name != name || len(named.Args) != arity || index < 0 || index >= len(named.Args) {
		return nil, false
	}
	return named.Args[index], nativeTypeResolved(named.Args[index])
}

func nativeMapLiteralInfo(ctx *nativeProjectionCtx, lit *ostyir.MapLit) (nativeExprInfo, bool) {
	if lit == nil {
		return nativeExprInfo{}, false
	}
	if nativeTypeResolved(lit.KeyT) && nativeTypeResolved(lit.ValT) {
		keyType, ok := nativeLLVMTypeFromIR(ctx, lit.KeyT)
		if !ok || !nativeMapSetKeySupported(keyType, nativeTypeIsString(lit.KeyT)) {
			return nativeExprInfo{}, false
		}
		valueType, ok := nativeLLVMTypeFromIR(ctx, lit.ValT)
		if !ok || valueType == "void" {
			return nativeExprInfo{}, false
		}
		return nativeExprInfoWithSource(nativeMapExprInfo(keyType, valueType, nativeTypeIsString(lit.KeyT)), lit.Type()), true
	}
	if len(lit.Entries) == 0 {
		return nativeExprInfo{}, false
	}
	keyInfo, ok := nativeExprTypeInfo(ctx, lit.Entries[0].Key)
	if !ok || !nativeMapSetKeySupported(keyInfo.llvmType, keyInfo.kind == nativeExprInfoString) {
		return nativeExprInfo{}, false
	}
	valueInfo, ok := nativeExprTypeInfo(ctx, lit.Entries[0].Value)
	if !ok || valueInfo.llvmType == "" || valueInfo.llvmType == "void" {
		return nativeExprInfo{}, false
	}
	keySource := nativeSourceTypeFromExprOrInfo(lit.Entries[0].Key, keyInfo)
	valueSource := nativeSourceTypeFromExprOrInfo(lit.Entries[0].Value, valueInfo)
	if !nativeTypeResolved(keySource) || !nativeTypeResolved(valueSource) {
		return nativeExprInfo{}, false
	}
	for _, entry := range lit.Entries[1:] {
		otherKey, ok := nativeExprTypeInfo(ctx, entry.Key)
		if !ok || otherKey.llvmType != keyInfo.llvmType || otherKey.kind == nativeExprInfoString != (keyInfo.kind == nativeExprInfoString) {
			return nativeExprInfo{}, false
		}
		otherValue, ok := nativeExprTypeInfo(ctx, entry.Value)
		if !ok || otherValue.llvmType != valueInfo.llvmType {
			return nativeExprInfo{}, false
		}
		if otherSource := nativeSourceTypeFromExprOrInfo(entry.Key, otherKey); !nativeTypeResolved(otherSource) || otherSource.String() != keySource.String() {
			return nativeExprInfo{}, false
		}
		if otherSource := nativeSourceTypeFromExprOrInfo(entry.Value, otherValue); !nativeTypeResolved(otherSource) || otherSource.String() != valueSource.String() {
			return nativeExprInfo{}, false
		}
	}
	return nativeExprInfoWithSource(nativeMapExprInfo(keyInfo.llvmType, valueInfo.llvmType, keyInfo.kind == nativeExprInfoString), &ostyir.NamedType{
		Name:    "Map",
		Builtin: true,
		Args:    []ostyir.Type{keySource, valueSource},
	}), true
}

func nativeExprInfoFromType(ctx *nativeProjectionCtx, t ostyir.Type) (nativeExprInfo, bool) {
	switch tt := t.(type) {
	case nil:
		return nativeExprInfo{}, false
	case *ostyir.ErrType, *ostyir.TypeVar:
		return nativeExprInfo{}, false
	case *ostyir.PrimType:
		llvmType, ok := nativeLLVMTypeFromIR(ctx, tt)
		if !ok || llvmType == "void" {
			return nativeExprInfo{}, false
		}
		if tt.Kind == ostyir.PrimString {
			return nativeExprInfoWithSource(nativeStringExprInfo(), t), true
		}
		return nativeExprInfoWithSource(nativeExprInfoFromLLVMType(llvmType), t), true
	case *ostyir.NamedType:
		if tt.Builtin {
			switch tt.Name {
			case "List":
				if len(tt.Args) != 1 {
					return nativeExprInfo{}, false
				}
				elemType, ok := nativeLLVMTypeFromIR(ctx, tt.Args[0])
				if !ok {
					return nativeExprInfo{}, false
				}
				return nativeExprInfoWithSource(nativeListExprInfo(elemType, nativeTypeIsString(tt.Args[0]), nativeTypeIsBytes(tt.Args[0])), t), true
			case "Map":
				if len(tt.Args) != 2 {
					return nativeExprInfo{}, false
				}
				keyType, ok := nativeLLVMTypeFromIR(ctx, tt.Args[0])
				if !ok {
					return nativeExprInfo{}, false
				}
				valueType, _ := nativeLLVMTypeFromIR(ctx, tt.Args[1])
				return nativeExprInfoWithSource(nativeMapExprInfo(keyType, valueType, nativeTypeIsString(tt.Args[0])), t), true
			case "Set":
				if len(tt.Args) != 1 {
					return nativeExprInfo{}, false
				}
				elemType, ok := nativeLLVMTypeFromIR(ctx, tt.Args[0])
				if !ok {
					return nativeExprInfo{}, false
				}
				return nativeExprInfoWithSource(nativeSetExprInfo(elemType, nativeTypeIsString(tt.Args[0])), t), true
			default:
				return nativeExprInfo{}, false
			}
		}
		info, ok := nativeStructInfoFromType(ctx, tt)
		if !ok {
			return nativeExprInfo{}, false
		}
		return nativeExprInfo{
			kind:       nativeExprInfoStruct,
			llvmType:   info.def.llvmType,
			sourceType: t,
			structName: tt.Name,
		}, true
	case *ostyir.OptionalType:
		innerType, ok := nativeLLVMTypeFromIR(ctx, tt.Inner)
		if !ok || innerType == "" || innerType == "void" {
			return nativeExprInfo{}, false
		}
		return nativeExprInfo{
			kind:            nativeExprInfoOptional,
			llvmType:        "ptr",
			sourceType:      t,
			optionInnerType: innerType,
		}, true
	default:
		return nativeExprInfo{}, false
	}
}

func nativeExprTypeInfo(ctx *nativeProjectionCtx, expr ostyir.Expr) (nativeExprInfo, bool) {
	if expr == nil {
		return nativeExprInfo{}, false
	}
	if info, ok := nativeExprInfoFromType(ctx, expr.Type()); ok {
		return info, true
	}
	switch e := expr.(type) {
	case *ostyir.IntLit:
		llvmType, ok := nativeLLVMTypeFromIR(ctx, e.Type())
		if !ok || llvmType == "void" {
			return nativeExprInfo{}, false
		}
		return nativeExprInfoFromLLVMType(llvmType), true
	case *ostyir.FloatLit:
		llvmType, ok := nativeLLVMTypeFromIR(ctx, e.Type())
		if !ok || llvmType == "void" {
			return nativeExprInfo{}, false
		}
		return nativeExprInfoFromLLVMType(llvmType), true
	case *ostyir.BoolLit:
		return nativeExprInfoFromLLVMType("i1"), true
	case *ostyir.StringLit:
		return nativeExprInfoWithSource(nativeStringExprInfo(), ostyir.TString), true
	case *ostyir.CharLit:
		return nativeExprInfoWithSource(nativeExprInfoFromLLVMType("i32"), ostyir.TChar), true
	case *ostyir.ByteLit:
		return nativeExprInfoWithSource(nativeExprInfoFromLLVMType("i8"), ostyir.TByte), true
	case *ostyir.Ident:
		if info, ok := ctx.lookupScopeName(e.Name); ok {
			return info, true
		}
		if value, ok := ctx.globalConsts[e.Name]; ok {
			return nativeExprInfoFromLLVMType(value.llvmType), true
		}
		return nativeExprInfo{}, false
	case *ostyir.StructLit:
		if info, ok := nativeExprInfoFromType(ctx, e.Type()); ok {
			return info, true
		}
		if e.TypeName == "" {
			return nativeExprInfo{}, false
		}
		info := ctx.structsByName[e.TypeName]
		if info == nil {
			return nativeExprInfo{}, false
		}
		return nativeExprInfo{
			kind:       nativeExprInfoStruct,
			llvmType:   info.def.llvmType,
			sourceType: &ostyir.NamedType{Name: e.TypeName},
			structName: e.TypeName,
		}, true
	case *ostyir.TupleLit:
		return nativeExprInfoFromType(ctx, e.Type())
	case *ostyir.MapLit:
		return nativeMapLiteralInfo(ctx, e)
	case *ostyir.FieldExpr:
		return nativeFieldExprInfo(ctx, e)
	case *ostyir.IndexExpr:
		baseInfo, ok := nativeExprTypeInfo(ctx, e.X)
		if !ok {
			return nativeExprInfo{}, false
		}
		switch baseInfo.kind {
		case nativeExprInfoList:
			elemSource, hasElemSource := nativeBuiltinArgType(baseInfo.sourceType, "List", 1, 0)
			if hasElemSource {
				if info, ok := nativeExprInfoFromType(ctx, elemSource); ok {
					return info, true
				}
			}
			if baseInfo.listElemType == "" {
				return nativeExprInfo{}, false
			}
			if baseInfo.listElemString {
				return nativeExprInfoWithSource(nativeStringExprInfo(), ostyir.TString), true
			}
			if hasElemSource {
				return nativeExprInfoWithSource(nativeExprInfoFromLLVMType(baseInfo.listElemType), elemSource), true
			}
			return nativeExprInfoFromLLVMType(baseInfo.listElemType), true
		case nativeExprInfoMap:
			valueSource, hasValueSource := nativeBuiltinArgType(baseInfo.sourceType, "Map", 2, 1)
			if hasValueSource {
				if info, ok := nativeExprInfoFromType(ctx, valueSource); ok {
					return info, true
				}
			}
			if baseInfo.mapValueType == "" {
				return nativeExprInfo{}, false
			}
			if hasValueSource {
				return nativeExprInfoWithSource(nativeExprInfoFromLLVMType(baseInfo.mapValueType), valueSource), true
			}
			return nativeExprInfoFromLLVMType(baseInfo.mapValueType), true
		default:
			return nativeExprInfo{}, false
		}
	case *ostyir.TupleAccess:
		info, ok := nativeTupleInfoFromType(ctx, e.X.Type())
		if !ok || e.Index < 0 || e.Index >= len(info.elemLLVMTypes) {
			return nativeExprInfo{}, false
		}
		return nativeExprInfoFromLLVMType(info.elemLLVMTypes[e.Index]), true
	case *ostyir.QuestionExpr:
		baseInfo, ok := nativeExprTypeInfo(ctx, e.X)
		if !ok || baseInfo.kind != nativeExprInfoOptional {
			return nativeExprInfo{}, false
		}
		innerSource, ok := unwrapOptionalIRType(nativeSourceTypeFromExprOrInfo(e.X, baseInfo))
		if ok {
			if info, ok := nativeExprInfoFromType(ctx, innerSource); ok {
				return info, true
			}
		}
		if baseInfo.optionInnerType == "" {
			return nativeExprInfo{}, false
		}
		return nativeExprInfoFromLLVMType(baseInfo.optionInnerType), true
	case *ostyir.CoalesceExpr:
		leftInfo, ok := nativeExprTypeInfo(ctx, e.Left)
		if !ok || leftInfo.kind != nativeExprInfoOptional {
			return nativeExprInfo{}, false
		}
		innerSource, ok := unwrapOptionalIRType(nativeSourceTypeFromExprOrInfo(e.Left, leftInfo))
		if ok {
			if info, ok := nativeExprInfoFromType(ctx, innerSource); ok {
				return info, true
			}
		}
		if leftInfo.optionInnerType == "" {
			return nativeExprInfo{}, false
		}
		return nativeExprInfoFromLLVMType(leftInfo.optionInnerType), true
	case *ostyir.MethodCall:
		return nativeBuiltinMethodReturnInfo(ctx, e)
	default:
		return nativeExprInfo{}, false
	}
}

func nativeFieldExprInfo(ctx *nativeProjectionCtx, expr *ostyir.FieldExpr) (nativeExprInfo, bool) {
	if ctx == nil || expr == nil {
		return nativeExprInfo{}, false
	}
	if expr.Optional {
		baseInfo, ok := nativeExprTypeInfo(ctx, expr.X)
		if !ok || baseInfo.kind != nativeExprInfoOptional {
			return nativeExprInfo{}, false
		}
		baseSource, ok := unwrapOptionalIRType(nativeSourceTypeFromExprOrInfo(expr.X, baseInfo))
		if !ok {
			return nativeExprInfo{}, false
		}
		structInfo, ok := nativeStructInfoFromType(ctx, baseSource)
		if !ok {
			return nativeExprInfo{}, false
		}
		fieldInfo, ok := structInfo.byName[expr.Name]
		if !ok || fieldInfo.llvmType != "ptr" {
			return nativeExprInfo{}, false
		}
		optionalField := wrapOptionalIRType(fieldInfo.irType)
		if info, ok := nativeExprInfoFromType(ctx, optionalField); ok {
			return info, true
		}
		return nativeExprInfo{
			kind:            nativeExprInfoOptional,
			llvmType:        "ptr",
			sourceType:      optionalField,
			optionInnerType: fieldInfo.llvmType,
		}, true
	}
	baseInfo, ok := nativeExprTypeInfo(ctx, expr.X)
	if !ok || baseInfo.kind != nativeExprInfoStruct || baseInfo.structName == "" {
		return nativeExprInfo{}, false
	}
	structInfo := ctx.structsByName[baseInfo.structName]
	if structInfo == nil {
		return nativeExprInfo{}, false
	}
	fieldInfo, ok := structInfo.byName[expr.Name]
	if !ok {
		return nativeExprInfo{}, false
	}
	if info, ok := nativeExprInfoFromType(ctx, fieldInfo.irType); ok {
		return info, true
	}
	return nativeExprInfoFromLLVMType(fieldInfo.llvmType), true
}

func nativeBuiltinMethodReturnInfo(ctx *nativeProjectionCtx, e *ostyir.MethodCall) (nativeExprInfo, bool) {
	if ctx == nil || e == nil || e.Receiver == nil {
		return nativeExprInfo{}, false
	}
	if receiverInfo, ok := nativeExprTypeInfo(ctx, e.Receiver); ok {
		switch receiverInfo.kind {
		case nativeExprInfoOptional:
			switch e.Name {
			case "isSome", "isNone":
				return nativeExprInfoFromLLVMType("i1"), true
			}
		case nativeExprInfoString:
			switch e.Name {
			case "len", "count", "charCount":
				return nativeExprInfoFromLLVMType("i64"), true
			case "isEmpty", "startsWith", "endsWith", "contains":
				return nativeExprInfoFromLLVMType("i1"), true
			case "split", "lines":
				return nativeListExprInfo("ptr", true, false), true
			case "trim", "trimStart", "trimEnd", "trimPrefix", "trimSuffix", "join", "replace", "replaceAll", "repeat", "toString":
				return nativeStringExprInfo(), true
			case "chars":
				return nativeListExprInfo("i32", false, false), true
			case "bytes":
				return nativeListExprInfo("i8", false, false), true
			}
		case nativeExprInfoList:
			switch e.Name {
			case "len":
				return nativeExprInfoFromLLVMType("i64"), true
			case "isEmpty":
				return nativeExprInfoFromLLVMType("i1"), true
			case "sorted":
				return receiverInfo, true
			case "toSet":
				return nativeSetExprInfo(receiverInfo.listElemType, receiverInfo.listElemString), true
			case "push", "insert":
				return nativeExprInfo{}, false
			}
		case nativeExprInfoMap:
			switch e.Name {
			case "len":
				return nativeExprInfoFromLLVMType("i64"), true
			case "isEmpty", "containsKey":
				return nativeExprInfoFromLLVMType("i1"), true
			case "keys":
				return nativeListExprInfo(receiverInfo.mapKeyType, receiverInfo.mapKeyString, false), true
			}
		case nativeExprInfoSet:
			switch e.Name {
			case "len":
				return nativeExprInfoFromLLVMType("i64"), true
			case "isEmpty", "contains", "insert", "remove":
				return nativeExprInfoFromLLVMType("i1"), true
			case "toList":
				return nativeListExprInfo(receiverInfo.setElemType, receiverInfo.setElemString, false), true
			}
		}
	}
	return nativeExprInfo{}, false
}

func nativeStructInfoFromType(ctx *nativeProjectionCtx, t ostyir.Type) (*nativeStructInfo, bool) {
	if ctx == nil {
		return nil, false
	}
	named, ok := t.(*ostyir.NamedType)
	if !ok || named == nil || named.Builtin || named.Package != "" || len(named.Args) != 0 {
		return nil, false
	}
	info := ctx.structsByName[named.Name]
	return info, info != nil
}

// nativeEnumInfoFromType resolves a monomorphized enum NamedType
// (its `Name` is the mangled `_ZTS…` form post-monomorphization)
// back to the projection-side info registered by
// `nativeRegisterEnumDecl`.
func nativeEnumInfoFromType(ctx *nativeProjectionCtx, t ostyir.Type) (*nativeEnumInfo, bool) {
	if ctx == nil {
		return nil, false
	}
	named, ok := t.(*ostyir.NamedType)
	if !ok || named == nil || named.Builtin || named.Package != "" || len(named.Args) != 0 {
		return nil, false
	}
	info := ctx.enumsByName[named.Name]
	return info, info != nil
}

func nativeTupleInfoFromType(ctx *nativeProjectionCtx, t ostyir.Type) (*nativeTupleInfo, bool) {
	if ctx == nil {
		return nil, false
	}
	tuple, ok := t.(*ostyir.TupleType)
	if !ok || tuple == nil {
		return nil, false
	}
	llvmType, ok := nativeLLVMTypeFromIR(ctx, tuple)
	if !ok {
		return nil, false
	}
	info := ctx.tuplesByLLVMType[llvmType]
	return info, info != nil
}

func nativeRegisterTupleType(ctx *nativeProjectionCtx, tuple *ostyir.TupleType) (string, bool) {
	if ctx == nil || tuple == nil {
		return "", false
	}
	elemLLVMTypes := make([]string, 0, len(tuple.Elems))
	fields := make([]*llvmNativeStructField, 0, len(tuple.Elems))
	for _, elem := range tuple.Elems {
		llvmType, ok := nativeLLVMTypeFromIR(ctx, elem)
		if !ok || llvmType == "void" {
			return "", false
		}
		elemLLVMTypes = append(elemLLVMTypes, llvmType)
		fields = append(fields, &llvmNativeStructField{llvmType: llvmType})
	}
	llvmType := llvmTupleTypeName(elemLLVMTypes)
	if _, exists := ctx.tuplesByLLVMType[llvmType]; exists {
		return llvmType, true
	}
	ctx.tuplesByLLVMType[llvmType] = &nativeTupleInfo{
		def: &llvmNativeStruct{
			name:     strings.TrimPrefix(llvmType, "%"),
			llvmType: llvmType,
			fields:   fields,
		},
		elemLLVMTypes: elemLLVMTypes,
	}
	ctx.tupleOrder = append(ctx.tupleOrder, llvmType)
	return llvmType, true
}

func nativeBuiltinMethodExprFromIR(ctx *nativeProjectionCtx, e *ostyir.MethodCall) (*llvmNativeExpr, bool) {
	if ctx == nil || e == nil || e.Receiver == nil {
		return nil, false
	}
	if expr, ok := nativeOptionMethodExprFromIR(ctx, e); ok {
		return expr, true
	}
	if expr, ok := nativeStringMethodExprFromIR(ctx, e); ok {
		return expr, true
	}
	if expr, ok := nativeListMethodExprFromIR(ctx, e); ok {
		return expr, true
	}
	if expr, ok := nativeMapMethodExprFromIR(ctx, e); ok {
		return expr, true
	}
	if expr, ok := nativeSetMethodExprFromIR(ctx, e); ok {
		return expr, true
	}
	return nil, false
}

func nativeOptionMethodExprFromIR(ctx *nativeProjectionCtx, e *ostyir.MethodCall) (*llvmNativeExpr, bool) {
	receiverInfo, ok := nativeExprTypeInfo(ctx, e.Receiver)
	if !ok || receiverInfo.kind != nativeExprInfoOptional {
		return nil, false
	}
	if len(e.Args) != 0 {
		return nil, false
	}
	receiver, ok := nativeExprFromIR(ctx, e.Receiver)
	if !ok {
		return nil, false
	}
	switch e.Name {
	case "isSome":
		return &llvmNativeExpr{
			kind:       llvmNativeExprOptionCheck,
			llvmType:   "i1",
			boolValue:  true,
			childExprs: []*llvmNativeExpr{receiver},
		}, true
	case "isNone":
		return &llvmNativeExpr{
			kind:       llvmNativeExprOptionCheck,
			llvmType:   "i1",
			boolValue:  false,
			childExprs: []*llvmNativeExpr{receiver},
		}, true
	default:
		return nil, false
	}
}

func nativeStringMethodExprFromIR(ctx *nativeProjectionCtx, e *ostyir.MethodCall) (*llvmNativeExpr, bool) {
	receiverInfo, ok := nativeExprTypeInfo(ctx, e.Receiver)
	if !ok || receiverInfo.kind != nativeExprInfoString {
		return nil, false
	}
	receiver, ok := nativeExprFromIR(ctx, e.Receiver)
	if !ok {
		return nil, false
	}
	switch e.Name {
	case "len":
		if len(e.Args) != 0 {
			return nil, false
		}
		ctx.needsStringRT = true
		return nativeRuntimeCallExpr("i64", llvmStringRuntimeByteLenSymbol(), receiver), true
	case "isEmpty":
		if len(e.Args) != 0 {
			return nil, false
		}
		ctx.needsStringRT = true
		return nativeEqZeroExpr(nativeRuntimeCallExpr("i64", llvmStringRuntimeByteLenSymbol(), receiver)), true
	case "startsWith":
		arg, ok := nativeSinglePositionalArgExprWithHint(ctx, e.Args, "ptr")
		if !ok {
			return nil, false
		}
		ctx.needsStringRT = true
		return nativeRuntimeCallExpr("i1", llvmStringRuntimeHasPrefixSymbol(), receiver, arg), true
	case "endsWith":
		arg, ok := nativeSinglePositionalArgExprWithHint(ctx, e.Args, "ptr")
		if !ok {
			return nil, false
		}
		ctx.needsStringRT = true
		return nativeRuntimeCallExpr("i1", llvmStringRuntimeHasSuffixSymbol(), receiver, arg), true
	case "contains":
		arg, ok := nativeSinglePositionalArgExprWithHint(ctx, e.Args, "ptr")
		if !ok {
			return nil, false
		}
		ctx.needsStringRT = true
		return nativeRuntimeCallExpr("i1", llvmStringRuntimeContainsSymbol(), receiver, arg), true
	case "split":
		arg, ok := nativeSinglePositionalArgExprWithHint(ctx, e.Args, "ptr")
		if !ok {
			return nil, false
		}
		ctx.needsStringRT = true
		return nativeRuntimeCallExpr("ptr", llvmStringRuntimeSplitSymbol(), receiver, arg), true
	case "lines":
		if len(e.Args) != 0 {
			return nil, false
		}
		ctx.needsStringRT = true
		return nativeRuntimeCallExpr("ptr", llvmStringRuntimeSplitSymbol(), receiver, nativeStringLiteralExpr("\n")), true
	case "join":
		arg, ok := nativeSinglePositionalArgExprWithHint(ctx, e.Args, "ptr")
		if !ok {
			return nil, false
		}
		ctx.needsStringRT = true
		return nativeRuntimeCallExpr("ptr", llvmStringRuntimeJoinSymbol(), arg, receiver), true
	case "trim":
		if len(e.Args) != 0 {
			return nil, false
		}
		ctx.needsStringRT = true
		return nativeRuntimeCallExpr("ptr", llvmStringRuntimeTrimSpaceSymbol(), receiver), true
	case "trimStart":
		if len(e.Args) != 0 {
			return nil, false
		}
		ctx.needsStringRT = true
		return nativeRuntimeCallExpr("ptr", llvmStringRuntimeTrimStartSymbol(), receiver), true
	case "trimEnd":
		if len(e.Args) != 0 {
			return nil, false
		}
		ctx.needsStringRT = true
		return nativeRuntimeCallExpr("ptr", llvmStringRuntimeTrimEndSymbol(), receiver), true
	case "trimPrefix":
		arg, ok := nativeSinglePositionalArgExprWithHint(ctx, e.Args, "ptr")
		if !ok {
			return nil, false
		}
		ctx.needsStringRT = true
		return nativeRuntimeCallExpr("ptr", llvmStringRuntimeTrimPrefixSymbol(), receiver, arg), true
	case "trimSuffix":
		arg, ok := nativeSinglePositionalArgExprWithHint(ctx, e.Args, "ptr")
		if !ok {
			return nil, false
		}
		ctx.needsStringRT = true
		return nativeRuntimeCallExpr("ptr", llvmStringRuntimeTrimSuffixSymbol(), receiver, arg), true
	case "replace":
		args, ok := nativePositionalArgsFromIRWithHints(ctx, e.Args, []string{"ptr", "ptr"})
		if !ok {
			return nil, false
		}
		ctx.needsStringRT = true
		return nativeRuntimeCallExpr("ptr", llvmStringRuntimeReplaceSymbol(), receiver, args[0], args[1]), true
	case "replaceAll":
		args, ok := nativePositionalArgsFromIRWithHints(ctx, e.Args, []string{"ptr", "ptr"})
		if !ok {
			return nil, false
		}
		ctx.needsStringRT = true
		return nativeRuntimeCallExpr("ptr", llvmStringRuntimeReplaceAllSymbol(), receiver, args[0], args[1]), true
	case "repeat":
		arg, ok := nativeSinglePositionalArgExprWithHint(ctx, e.Args, "i64")
		if !ok {
			return nil, false
		}
		ctx.needsStringRT = true
		return nativeRuntimeCallExpr("ptr", llvmStringRuntimeRepeatSymbol(), receiver, arg), true
	case "count":
		arg, ok := nativeSinglePositionalArgExprWithHint(ctx, e.Args, "ptr")
		if !ok {
			return nil, false
		}
		ctx.needsStringRT = true
		return nativeRuntimeCallExpr("i64", llvmStringRuntimeCountSymbol(), receiver, arg), true
	case "chars":
		if len(e.Args) != 0 {
			return nil, false
		}
		ctx.needsStringRT = true
		return nativeRuntimeCallExpr("ptr", llvmStringRuntimeCharsSymbol(), receiver), true
	case "bytes":
		if len(e.Args) != 0 {
			return nil, false
		}
		ctx.needsStringRT = true
		return nativeRuntimeCallExpr("ptr", llvmStringRuntimeBytesSymbol(), receiver), true
	case "charCount":
		if len(e.Args) != 0 {
			return nil, false
		}
		ctx.needsStringRT = true
		ctx.needsListRT = true
		return nativeRuntimeCallExpr(
			"i64",
			llvmListRuntimeLenSymbol(),
			nativeRuntimeCallExpr("ptr", llvmStringRuntimeCharsSymbol(), receiver),
		), true
	case "toString":
		if len(e.Args) != 0 {
			return nil, false
		}
		return receiver, true
	default:
		return nil, false
	}
}

func nativeListMethodExprFromIR(ctx *nativeProjectionCtx, e *ostyir.MethodCall) (*llvmNativeExpr, bool) {
	receiverInfo, ok := nativeExprTypeInfo(ctx, e.Receiver)
	if !ok || receiverInfo.kind != nativeExprInfoList {
		return nil, false
	}
	elemType, elemString := receiverInfo.listElemType, receiverInfo.listElemString
	receiver, ok := nativeExprFromIR(ctx, e.Receiver)
	if !ok {
		return nil, false
	}
	switch e.Name {
	case "len":
		if len(e.Args) != 0 {
			return nil, false
		}
		ctx.needsListRT = true
		return nativeRuntimeCallExpr("i64", llvmListRuntimeLenSymbol(), receiver), true
	case "isEmpty":
		if len(e.Args) != 0 {
			return nil, false
		}
		ctx.needsListRT = true
		return nativeEqZeroExpr(nativeRuntimeCallExpr("i64", llvmListRuntimeLenSymbol(), receiver)), true
	case "sorted":
		if len(e.Args) != 0 {
			return nil, false
		}
		symbol := llvmListRuntimeSortedSymbol(elemType, elemString)
		if symbol == "" {
			return nil, false
		}
		ctx.needsListRT = true
		return nativeRuntimeCallExpr("ptr", symbol, receiver), true
	case "toSet":
		if len(e.Args) != 0 {
			return nil, false
		}
		if receiverInfo.listElemBytes {
			return nil, false
		}
		symbol := llvmListRuntimeToSetSymbol(elemType, elemString)
		if symbol == "" {
			return nil, false
		}
		ctx.needsListRT = true
		return nativeRuntimeCallExpr("ptr", symbol, receiver), true
	case "push":
		arg, ok := nativeSinglePositionalArgExprWithHint(ctx, e.Args, elemType)
		if !ok || !llvmListUsesTypedRuntime(elemType) {
			return nil, false
		}
		ctx.needsListRT = true
		return nativeRuntimeCallExpr("void", llvmListRuntimePushSymbol(llvmListElementSuffix(elemType)), receiver, arg), true
	case "insert":
		args, ok := nativePositionalArgsFromIRWithHints(ctx, e.Args, []string{"i64", elemType})
		if !ok || !llvmListUsesTypedRuntime(elemType) {
			return nil, false
		}
		ctx.needsListRT = true
		return nativeRuntimeCallExpr("void", llvmListRuntimeInsertSymbol(llvmListElementSuffix(elemType)), receiver, args[0], args[1]), true
	default:
		return nil, false
	}
}

func nativeMapMethodExprFromIR(ctx *nativeProjectionCtx, e *ostyir.MethodCall) (*llvmNativeExpr, bool) {
	receiverInfo, ok := nativeExprTypeInfo(ctx, e.Receiver)
	if !ok || receiverInfo.kind != nativeExprInfoMap {
		return nil, false
	}
	keyType, keyString := receiverInfo.mapKeyType, receiverInfo.mapKeyString
	if !nativeMapSetKeySupported(keyType, keyString) {
		return nil, false
	}
	receiver, ok := nativeExprFromIR(ctx, e.Receiver)
	if !ok {
		return nil, false
	}
	switch e.Name {
	case "len":
		if len(e.Args) != 0 {
			return nil, false
		}
		ctx.needsMapRT = true
		return nativeRuntimeCallExpr("i64", llvmMapRuntimeLenSymbol(), receiver), true
	case "isEmpty":
		if len(e.Args) != 0 {
			return nil, false
		}
		ctx.needsMapRT = true
		return nativeEqZeroExpr(nativeRuntimeCallExpr("i64", llvmMapRuntimeLenSymbol(), receiver)), true
	case "containsKey":
		arg, ok := nativeSinglePositionalArgExprWithHint(ctx, e.Args, keyType)
		if !ok {
			return nil, false
		}
		ctx.needsMapRT = true
		return nativeRuntimeCallExpr("i1", llvmMapRuntimeContainsSymbol(keyType, keyString), receiver, arg), true
	case "keys":
		if len(e.Args) != 0 {
			return nil, false
		}
		ctx.needsMapRT = true
		return nativeRuntimeCallExpr("ptr", llvmMapRuntimeKeysSymbol(), receiver), true
	case "remove":
		arg, ok := nativeSinglePositionalArgExprWithHint(ctx, e.Args, keyType)
		if !ok {
			return nil, false
		}
		ctx.needsMapRT = true
		return nativeRuntimeCallExpr("i1", llvmMapRuntimeRemoveSymbol(keyType, keyString), receiver, arg), true
	case "insert":
		args, ok := nativePositionalArgsFromIRWithHints(ctx, e.Args, []string{keyType, receiverInfo.mapValueType})
		if !ok {
			return nil, false
		}
		ctx.needsMapRT = true
		return &llvmNativeExpr{
			kind:            llvmNativeExprCall,
			llvmType:        "void",
			name:            llvmMapRuntimeInsertSymbol(keyType, keyString),
			spillArgIndices: []int{2},
			childExprs:      []*llvmNativeExpr{receiver, args[0], args[1]},
		}, true
	default:
		return nil, false
	}
}

func nativeSetMethodExprFromIR(ctx *nativeProjectionCtx, e *ostyir.MethodCall) (*llvmNativeExpr, bool) {
	receiverInfo, ok := nativeExprTypeInfo(ctx, e.Receiver)
	if !ok || receiverInfo.kind != nativeExprInfoSet {
		return nil, false
	}
	elemType, elemString := receiverInfo.setElemType, receiverInfo.setElemString
	if !nativeMapSetKeySupported(elemType, elemString) {
		return nil, false
	}
	receiver, ok := nativeExprFromIR(ctx, e.Receiver)
	if !ok {
		return nil, false
	}
	switch e.Name {
	case "len":
		if len(e.Args) != 0 {
			return nil, false
		}
		ctx.needsSetRT = true
		return nativeRuntimeCallExpr("i64", llvmSetRuntimeLenSymbol(), receiver), true
	case "isEmpty":
		if len(e.Args) != 0 {
			return nil, false
		}
		ctx.needsSetRT = true
		return nativeEqZeroExpr(nativeRuntimeCallExpr("i64", llvmSetRuntimeLenSymbol(), receiver)), true
	case "contains":
		arg, ok := nativeSinglePositionalArgExprWithHint(ctx, e.Args, elemType)
		if !ok {
			return nil, false
		}
		ctx.needsSetRT = true
		return nativeRuntimeCallExpr("i1", llvmSetRuntimeContainsSymbol(elemType, elemString), receiver, arg), true
	case "insert":
		arg, ok := nativeSinglePositionalArgExprWithHint(ctx, e.Args, elemType)
		if !ok {
			return nil, false
		}
		ctx.needsSetRT = true
		return nativeRuntimeCallExpr("i1", llvmSetRuntimeInsertSymbol(elemType, elemString), receiver, arg), true
	case "remove":
		arg, ok := nativeSinglePositionalArgExprWithHint(ctx, e.Args, elemType)
		if !ok {
			return nil, false
		}
		ctx.needsSetRT = true
		return nativeRuntimeCallExpr("i1", llvmSetRuntimeRemoveSymbol(elemType, elemString), receiver, arg), true
	case "toList":
		if len(e.Args) != 0 {
			return nil, false
		}
		ctx.needsSetRT = true
		return nativeRuntimeCallExpr("ptr", llvmSetRuntimeToListSymbol(), receiver), true
	default:
		return nil, false
	}
}

func nativeSinglePositionalArgExpr(ctx *nativeProjectionCtx, args []ostyir.Arg) (*llvmNativeExpr, bool) {
	values, ok := nativePositionalArgsFromIR(ctx, args, 1)
	if !ok {
		return nil, false
	}
	return values[0], true
}

func nativeSinglePositionalArgExprWithHint(ctx *nativeProjectionCtx, args []ostyir.Arg, hintLLVMType string) (*llvmNativeExpr, bool) {
	values, ok := nativePositionalArgsFromIRWithHints(ctx, args, []string{hintLLVMType})
	if !ok {
		return nil, false
	}
	return values[0], true
}

func nativePositionalArgsFromIR(ctx *nativeProjectionCtx, args []ostyir.Arg, expected int) ([]*llvmNativeExpr, bool) {
	if len(args) != expected {
		return nil, false
	}
	out := make([]*llvmNativeExpr, 0, len(args))
	for _, arg := range args {
		if arg.IsKeyword() {
			return nil, false
		}
		value, ok := nativeExprFromIR(ctx, arg.Value)
		if !ok {
			return nil, false
		}
		out = append(out, value)
	}
	return out, true
}

func nativePositionalArgsFromIRWithHints(ctx *nativeProjectionCtx, args []ostyir.Arg, hintLLVMTypes []string) ([]*llvmNativeExpr, bool) {
	if len(args) != len(hintLLVMTypes) {
		return nil, false
	}
	out := make([]*llvmNativeExpr, 0, len(args))
	for i, arg := range args {
		if arg.IsKeyword() {
			return nil, false
		}
		value, ok := nativeExprFromIRWithHint(ctx, arg.Value, hintLLVMTypes[i])
		if !ok {
			return nil, false
		}
		out = append(out, value)
	}
	return out, true
}

func nativeExprFromIRWithHint(ctx *nativeProjectionCtx, expr ostyir.Expr, hintLLVMType string) (*llvmNativeExpr, bool) {
	if value, ok := nativeExprFromIR(ctx, expr); ok {
		return value, true
	}
	switch e := expr.(type) {
	case *ostyir.IntLit:
		if hintLLVMType == "" {
			return nil, false
		}
		text, ok := normalizeNativeIntText(e.Text)
		if !ok {
			return nil, false
		}
		return &llvmNativeExpr{kind: llvmNativeExprInt, llvmType: hintLLVMType, text: text}, true
	case *ostyir.FloatLit:
		if hintLLVMType != "double" {
			return nil, false
		}
		return &llvmNativeExpr{
			kind:     llvmNativeExprFloat,
			llvmType: hintLLVMType,
			text:     strings.ReplaceAll(e.Text, "_", ""),
		}, true
	case *ostyir.BoolLit:
		if hintLLVMType != "i1" {
			return nil, false
		}
		return &llvmNativeExpr{kind: llvmNativeExprBool, llvmType: "i1", boolValue: e.Value}, true
	case *ostyir.StringLit:
		if hintLLVMType != "ptr" {
			return nil, false
		}
		text, ok := nativePlainStringText(e)
		if !ok {
			return nil, false
		}
		return &llvmNativeExpr{kind: llvmNativeExprString, llvmType: "ptr", text: text}, true
	case *ostyir.CharLit:
		if hintLLVMType != "i32" {
			return nil, false
		}
		return &llvmNativeExpr{kind: llvmNativeExprInt, llvmType: "i32", text: strconv.FormatInt(int64(e.Value), 10)}, true
	case *ostyir.ByteLit:
		if hintLLVMType != "i8" {
			return nil, false
		}
		return &llvmNativeExpr{kind: llvmNativeExprInt, llvmType: "i8", text: strconv.FormatInt(int64(e.Value), 10)}, true
	default:
		return nil, false
	}
}

// nativeVariantLitFromIR lowers `Maybe.Some(42)` / `Maybe.None` into
// a native struct literal over the monomorphized enum's storage
// type `{ i64 tag, <payloadSlotType> }`. The emitter reuses the
// struct-lit insertvalue machinery — enums have no variant-literal
// primitive of their own.
//
// Payload rules:
//
//   - All-payload-free enums (payloadSlotType == ""): storage is
//     `{ i64 }`; the struct lit carries only the tag.
//   - Single-payload variant: storage is `{ i64, <payload> }`;
//     construct with `(tag, arg)`.
//   - Payload-free variant in a with-payload enum: pad the slot
//     with a zero of `payloadSlotType` so the storage width is
//     uniform and pattern matching on the payload slot remains
//     well-typed.
func nativeVariantLitFromIR(ctx *nativeProjectionCtx, lit *ostyir.VariantLit) (*llvmNativeExpr, bool) {
	if ctx == nil || lit == nil {
		return nil, false
	}
	info, ok := nativeEnumInfoFromType(ctx, lit.Type())
	if !ok {
		return nil, false
	}
	variant, ok := info.variantsByName[lit.Variant]
	if !ok {
		return nil, false
	}
	if len(lit.Args) != len(variant.payloadIRTypes) {
		return nil, false
	}
	children := make([]*llvmNativeExpr, 0, 2)
	children = append(children, &llvmNativeExpr{
		kind:     llvmNativeExprInt,
		llvmType: "i64",
		text:     strconv.Itoa(variant.tag),
	})
	if info.def.payloadSlotType != "" {
		if variant.payloadLLVMType != "" {
			if variant.payloadLLVMType != info.def.payloadSlotType {
				return nil, false
			}
			arg := lit.Args[0]
			if arg.Name != "" {
				return nil, false
			}
			value, ok := nativeExprFromIRWithHint(ctx, arg.Value, variant.payloadLLVMType)
			if !ok || value.llvmType != variant.payloadLLVMType {
				return nil, false
			}
			children = append(children, value)
		} else {
			children = append(children, nativeZeroExprForLLVMType(info.def.payloadSlotType))
		}
	}
	return &llvmNativeExpr{
		kind:       llvmNativeExprStructLit,
		llvmType:   info.def.llvmType,
		childExprs: children,
	}, true
}

// nativeBareVariantAccessFromIR matches the `Enum.Variant` field
// access shape (no parens) and projects it as if the surface had
// written `Enum.Variant()` — a zero-argument variant construction.
// Returns (nil, false) when the field-expr does not name a known
// enum variant so the caller falls through to the regular field
// access path.
func nativeBareVariantAccessFromIR(ctx *nativeProjectionCtx, e *ostyir.FieldExpr) (*llvmNativeExpr, bool) {
	if ctx == nil || e == nil || e.Optional {
		return nil, false
	}
	id, ok := e.X.(*ostyir.Ident)
	if !ok || id == nil || id.Kind != ostyir.IdentTypeName {
		return nil, false
	}
	info, ok := ctx.enumsByName[id.Name]
	if !ok {
		return nil, false
	}
	variant, ok := info.variantsByName[e.Name]
	if !ok {
		return nil, false
	}
	if len(variant.payloadIRTypes) != 0 {
		// A payload-bearing variant referenced without parens is a
		// function-value form (the constructor as a callable). Bail
		// — only the zero-arg construction case lowers cleanly here.
		return nil, false
	}
	children := []*llvmNativeExpr{{
		kind:     llvmNativeExprInt,
		llvmType: "i64",
		text:     strconv.Itoa(variant.tag),
	}}
	if info.def.payloadSlotType != "" {
		children = append(children, nativeZeroExprForLLVMType(info.def.payloadSlotType))
	}
	return &llvmNativeExpr{
		kind:       llvmNativeExprStructLit,
		llvmType:   info.def.llvmType,
		childExprs: children,
	}, true
}

// nativeZeroExprForLLVMType produces a native literal expression
// for the zero value of a given LLVM type — used to pad the
// enum payload slot when a payload-free variant lands inside a
// with-payload enum.
func nativeZeroExprForLLVMType(llvmType string) *llvmNativeExpr {
	switch llvmType {
	case "i1":
		return &llvmNativeExpr{kind: llvmNativeExprBool, llvmType: "i1", boolValue: false}
	case "double":
		return &llvmNativeExpr{kind: llvmNativeExprFloat, llvmType: "double", text: "0.0"}
	case "ptr":
		return &llvmNativeExpr{kind: llvmNativeExprInt, llvmType: "ptr", text: "null"}
	default:
		return &llvmNativeExpr{kind: llvmNativeExprInt, llvmType: llvmType, text: "0"}
	}
}

func nativeRuntimeCallExpr(llvmType string, name string, args ...*llvmNativeExpr) *llvmNativeExpr {
	return &llvmNativeExpr{
		kind:       llvmNativeExprCall,
		llvmType:   llvmType,
		name:       name,
		childExprs: args,
	}
}

func nativeEqZeroExpr(inner *llvmNativeExpr) *llvmNativeExpr {
	return &llvmNativeExpr{
		kind:     llvmNativeExprBinary,
		llvmType: "i1",
		op:       "==",
		childExprs: []*llvmNativeExpr{
			inner,
			nativeIntLiteralExpr("0"),
		},
	}
}

func nativeIntLiteralExpr(text string) *llvmNativeExpr {
	return &llvmNativeExpr{
		kind:     llvmNativeExprInt,
		llvmType: "i64",
		text:     text,
	}
}

func nativeStringLiteralExpr(text string) *llvmNativeExpr {
	return &llvmNativeExpr{
		kind:     llvmNativeExprString,
		llvmType: "ptr",
		text:     text,
	}
}

func nativeListMethodInfo(ctx *nativeProjectionCtx, t ostyir.Type) (elemType string, elemString bool, ok bool) {
	named, ok := t.(*ostyir.NamedType)
	if !ok || named == nil || !named.Builtin || named.Name != "List" || len(named.Args) != 1 {
		return "", false, false
	}
	elemType, ok = nativeLLVMTypeFromIR(ctx, named.Args[0])
	if !ok {
		return "", false, false
	}
	return elemType, nativeTypeIsString(named.Args[0]), true
}

func nativeMapMethodInfo(ctx *nativeProjectionCtx, t ostyir.Type) (keyType string, valueType string, keyString bool, ok bool) {
	named, ok := t.(*ostyir.NamedType)
	if !ok || named == nil || !named.Builtin || named.Name != "Map" || len(named.Args) != 2 {
		return "", "", false, false
	}
	keyType, ok = nativeLLVMTypeFromIR(ctx, named.Args[0])
	if !ok {
		return "", "", false, false
	}
	if valueType, ok = nativeLLVMTypeFromIR(ctx, named.Args[1]); !ok {
		valueType = ""
	}
	return keyType, valueType, nativeTypeIsString(named.Args[0]), true
}

func nativeSetMethodInfo(ctx *nativeProjectionCtx, t ostyir.Type) (elemType string, elemString bool, ok bool) {
	named, ok := t.(*ostyir.NamedType)
	if !ok || named == nil || !named.Builtin || named.Name != "Set" || len(named.Args) != 1 {
		return "", false, false
	}
	elemType, ok = nativeLLVMTypeFromIR(ctx, named.Args[0])
	if !ok {
		return "", false, false
	}
	return elemType, nativeTypeIsString(named.Args[0]), true
}

func unwrapOptionalIRType(t ostyir.Type) (ostyir.Type, bool) {
	opt, ok := t.(*ostyir.OptionalType)
	if !ok || opt == nil || opt.Inner == nil {
		return nil, false
	}
	return opt.Inner, true
}

func wrapOptionalIRType(t ostyir.Type) ostyir.Type {
	if t == nil {
		return nil
	}
	return &ostyir.OptionalType{Inner: t}
}

func listElementType(t ostyir.Type) ostyir.Type {
	named, ok := t.(*ostyir.NamedType)
	if !ok || named == nil || !named.Builtin || named.Name != "List" || len(named.Args) != 1 {
		return nil
	}
	return named.Args[0]
}

func nativeTypeIsString(t ostyir.Type) bool {
	prim, ok := t.(*ostyir.PrimType)
	return ok && prim.Kind == ostyir.PrimString
}

func nativeTypeIsBytes(t ostyir.Type) bool {
	prim, ok := t.(*ostyir.PrimType)
	return ok && prim.Kind == ostyir.PrimBytes
}

func nativeMapSetKeySupported(llvmType string, isString bool) bool {
	if isString {
		return true
	}
	switch llvmType {
	case "i64", "i1", "double", "ptr":
		return true
	default:
		return false
	}
}

func nativeMethodOwnerName(t ostyir.Type) (string, bool) {
	named, ok := t.(*ostyir.NamedType)
	if !ok || named == nil || named.Builtin || named.Package != "" || len(named.Args) != 0 {
		return "", false
	}
	return named.Name, true
}

func nativeLLVMTypeFromIR(ctx *nativeProjectionCtx, t ostyir.Type) (string, bool) {
	switch tt := t.(type) {
	case nil:
		return "void", true
	case *ostyir.PrimType:
		switch tt.Kind {
		case ostyir.PrimUnit:
			return "void", true
		default:
			name := legacyPrimTypeName(tt.Kind)
			if name == "" {
				return "", false
			}
			llvmType := llvmBuiltinType(name)
			return llvmType, llvmType != ""
		}
	case *ostyir.NamedType:
		if tt.Builtin {
			switch tt.Name {
			case "List", "Map", "Set":
				return "ptr", true
			}
			return "", false
		}
		if info, ok := nativeStructInfoFromType(ctx, tt); ok {
			return info.def.llvmType, true
		}
		if info, ok := nativeEnumInfoFromType(ctx, tt); ok {
			return info.def.llvmType, true
		}
		return "", false
	case *ostyir.OptionalType:
		if _, ok := nativeLLVMTypeFromIR(ctx, tt.Inner); !ok {
			return "", false
		}
		return "ptr", true
	case *ostyir.TupleType:
		return nativeRegisterTupleType(ctx, tt)
	default:
		return "", false
	}
}

func nativeIsUnitType(t ostyir.Type) bool {
	if t == nil {
		return true
	}
	prim, ok := t.(*ostyir.PrimType)
	return ok && prim.Kind == ostyir.PrimUnit
}

func nativePlainStringText(lit *ostyir.StringLit) (string, bool) {
	if lit == nil {
		return "", false
	}
	var b strings.Builder
	for _, part := range lit.Parts {
		if !part.IsLit {
			return "", false
		}
		b.WriteString(part.Lit)
	}
	return b.String(), true
}

func normalizeNativeIntText(text string) (string, bool) {
	raw := strings.ReplaceAll(text, "_", "")
	base := 10
	switch {
	case strings.HasPrefix(raw, "0x") || strings.HasPrefix(raw, "0X"):
		base, raw = 16, raw[2:]
	case strings.HasPrefix(raw, "0o") || strings.HasPrefix(raw, "0O"):
		base, raw = 8, raw[2:]
	case strings.HasPrefix(raw, "0b") || strings.HasPrefix(raw, "0B"):
		base, raw = 2, raw[2:]
	}
	if raw == "" {
		return "", false
	}
	value, err := strconv.ParseInt(raw, base, 64)
	if err != nil {
		return "", false
	}
	return strconv.FormatInt(value, 10), true
}

func nativeUnaryOpString(op ostyir.UnOp) string {
	switch op {
	case ostyir.UnNeg:
		return "-"
	case ostyir.UnPlus:
		return "+"
	case ostyir.UnNot:
		return "!"
	default:
		return ""
	}
}

func nativeBinaryOpString(op ostyir.BinOp) string {
	switch op {
	case ostyir.BinAdd:
		return "+"
	case ostyir.BinSub:
		return "-"
	case ostyir.BinMul:
		return "*"
	case ostyir.BinDiv:
		return "/"
	case ostyir.BinMod:
		return "%"
	case ostyir.BinEq:
		return "=="
	case ostyir.BinNeq:
		return "!="
	case ostyir.BinLt:
		return "<"
	case ostyir.BinLeq:
		return "<="
	case ostyir.BinGt:
		return ">"
	case ostyir.BinGeq:
		return ">="
	case ostyir.BinAnd:
		return "&&"
	case ostyir.BinOr:
		return "||"
	case ostyir.BinBitAnd:
		return "&"
	case ostyir.BinBitOr:
		return "|"
	case ostyir.BinBitXor:
		return "^"
	case ostyir.BinShl:
		return "<<"
	case ostyir.BinShr:
		return ">>"
	default:
		return ""
	}
}

func nativeAssignOpString(op ostyir.AssignOp) string {
	switch op {
	case ostyir.AssignEq:
		return "="
	case ostyir.AssignAdd:
		return "+"
	case ostyir.AssignSub:
		return "-"
	case ostyir.AssignMul:
		return "*"
	case ostyir.AssignDiv:
		return "/"
	case ostyir.AssignMod:
		return "%"
	case ostyir.AssignAnd:
		return "&"
	case ostyir.AssignOr:
		return "|"
	case ostyir.AssignXor:
		return "^"
	case ostyir.AssignShl:
		return "<<"
	case ostyir.AssignShr:
		return ">>"
	default:
		return ""
	}
}
