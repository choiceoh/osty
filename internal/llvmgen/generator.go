// generator.go — *generator state, scope/block/label helpers, GC mechanics,
// Osty emitter bridge, and shared value helpers used by decl/stmt/expr/type
// emission paths.
//
// NOTE(osty-migration): this file owns imperative Go state that depends on
// the legacy AST emitter. Once the IR-direct emitter (see doc.go) lands in
// toolchain/llvmgen.osty, the state machine here should move to the Osty
// side and Go retains only the entry-point shim.
package llvmgen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/osty/osty/internal/ast"
)

type generator struct {
	sourcePath        string
	target            string
	functions         map[string]*fnSig
	methods           map[string]map[string]*fnSig
	structs           []*structInfo
	structsByName     map[string]*structInfo
	structsByType     map[string]*structInfo
	enums             []*enumInfo
	enumsByName       map[string]*enumInfo
	enumsByType       map[string]*enumInfo
	interfacesByName  map[string]*interfaceInfo
	typeAliasesByName map[string]*typeAliasInfo
	globals           map[string]value
	globalDefs        []string
	globalConsts      map[string]constValue
	tupleTypes        map[string]tupleTypeInfo
	resultTypes       map[string]builtinResultType
	runtimeFFI        map[string]map[string]*runtimeFFIFunction
	runtimeFFIPaths   map[string]string
	testingAliases    map[string]bool
	stdStringsAliases map[string]bool
	runtimeDecls      map[string]runtimeDecl
	runtimeDeclOrder  []string
	traceHelpers      map[string]string
	traceHelperDefs   []string
	// Closure-env thunks generated on demand when a top-level fn
	// symbol is used as a first-class value. Mirror of the MIR
	// closure ABI (internal/llvmgen/mir_generator.go:emitThunks): one
	// thunk per original symbol, signature `(ptr env, <user params>)`,
	// body delegates to the real symbol ignoring env. Rendered at the
	// end of the module so the top-down read order matches user code
	// first / machinery second. See fn_value.go.
	fnValueThunkDefs  map[string]string
	fnValueThunkOrder []string

	temp              int
	label             int
	stringID          int
	stringDefs        []*LlvmStringGlobal
	body              []string
	locals            []map[string]value
	returnType        string
	returnSourceType  ast.Type
	returnListElemTyp string
	currentBlock      string
	currentReachable  bool
	resultContexts    []builtinResultContext
	optionContexts    []builtinOptionContext

	needsGCRuntime bool
	gcRootSlots    []value
	gcRootMarks    []int
	nextSafepoint  int
	hiddenLocalID  int
	loopStack      []loopContext
}

type loopContext struct {
	continueLabel string
	breakLabel    string
	scopeDepth    int
}

type scopeState struct {
	locals      []map[string]value
	gcRootSlots []value
	gcRootMarks []int
}

type value struct {
	typ            string
	ref            string
	ptr            bool
	mutable        bool
	gcManaged      bool
	listElemTyp    string
	listElemString bool
	mapKeyTyp      string
	mapValueTyp    string
	mapKeyString   bool
	setElemTyp     string
	setElemString  bool
	sourceType     ast.Type
	rootPaths      [][]int
	// fnSigRef is non-nil when this value is a first-class function
	// value — an env pointer produced by emitFnValueEnv. The sig gives
	// the call-site indirect-call dispatcher the LLVM signature to
	// emit. See internal/llvmgen/fn_value.go.
	fnSigRef *fnSig
}

type builtinResultContext struct {
	info       builtinResultType
	sourceType ast.Type
}

type builtinOptionContext struct {
	inner      ast.Type
	sourceType ast.Type
}

const (
	llvmGcRuntimeFrameSlotKind = 5
)

func (g *generator) beginFunction() {
	g.temp = 0
	g.label = 0
	g.body = nil
	g.locals = []map[string]value{{}}
	g.returnType = ""
	g.returnSourceType = nil
	g.returnListElemTyp = ""
	g.gcRootSlots = nil
	g.gcRootMarks = []int{0}
	g.nextSafepoint = 1
	g.hiddenLocalID = 0
	g.currentBlock = "entry"
	g.currentReachable = true
	g.loopStack = nil
	g.resultContexts = nil
	g.optionContexts = nil
}

func (g *generator) bindGCRootIfManagedPointer(emitter *LlvmEmitter, slot value) {
	if slot.typ != "ptr" || !slot.gcManaged {
		return
	}
	llvmGcRootBind(emitter, toOstyValue(slot))
	g.gcRootSlots = append(g.gcRootSlots, slot)
	g.needsGCRuntime = true
}

func (g *generator) postGCWriteIfPointer(emitter *LlvmEmitter, slot, v value) {
	if slot.typ != "ptr" || !slot.gcManaged || v.typ != "ptr" || !v.gcManaged {
		return
	}
	llvmGcPostWrite(emitter, toOstyValue(slot), toOstyValue(v), llvmGcRuntimeFrameSlotKind)
	g.needsGCRuntime = true
}

func (g *generator) releaseGCRoots(emitter *LlvmEmitter) {
	for i := len(g.gcRootSlots) - 1; i >= 0; i-- {
		llvmGcRootRelease(emitter, toOstyValue(g.gcRootSlots[i]))
	}
}

func (g *generator) emitGCSafepoint(emitter *LlvmEmitter) {
	g.declareRuntimeSymbol("osty.gc.safepoint_v1", "void", []paramInfo{
		{typ: "i64"},
		{typ: "ptr"},
		{typ: "i64"},
	})
	id := g.nextSafepoint
	g.nextSafepoint++
	roots := g.visibleSafepointRoots()
	if len(roots) == 0 {
		emitter.body = append(emitter.body, fmt.Sprintf(
			"  call void @osty.gc.safepoint_v1(i64 %d, ptr null, i64 0)",
			id,
		))
		g.needsGCRuntime = true
		return
	}
	slotsPtr := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = alloca ptr, i64 %d", slotsPtr, len(roots)))
	for i, root := range roots {
		slotPtr := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = getelementptr ptr, ptr %s, i64 %d", slotPtr, slotsPtr, i))
		emitter.body = append(emitter.body, fmt.Sprintf("  store ptr %s, ptr %s", g.safepointRootAddress(emitter, root), slotPtr))
	}
	emitter.body = append(emitter.body, fmt.Sprintf(
		"  call void @osty.gc.safepoint_v1(i64 %d, ptr %s, i64 %d)",
		id,
		slotsPtr,
		len(roots),
	))
	g.needsGCRuntime = true
}

func llvmZeroValue(typ string) value {
	ref := llvmZeroLiteral(typ)
	if typ != "ptr" && typ != "i64" && typ != "i1" && typ != "double" {
		ref = "zeroinitializer"
	}
	return value{typ: typ, ref: ref}
}

func (g *generator) render(defs []string) []byte {
	if len(g.traceHelperDefs) != 0 {
		defs = append(append([]string(nil), g.traceHelperDefs...), defs...)
	}
	// Phase 1: closure thunks generated for first-class fn values
	// live at the tail of the module so the top-down read order is
	// user functions first, then machinery. Order is deterministic
	// via fnValueThunkOrder (insertion-ordered).
	if len(g.fnValueThunkOrder) != 0 {
		thunks := make([]string, 0, len(g.fnValueThunkOrder))
		for _, sym := range g.fnValueThunkOrder {
			thunks = append(thunks, g.fnValueThunkDefs[sym])
		}
		defs = append(defs, thunks...)
	}
	allDefs := make([]string, 0, len(g.globalDefs)+len(defs))
	allDefs = append(allDefs, g.globalDefs...)
	allDefs = append(allDefs, defs...)
	typeDefs := make([]string, 0, len(g.structs)+len(g.enumsByType)+len(g.tupleTypes)+len(g.resultTypes))
	for _, info := range g.structs {
		fieldTypes := make([]string, 0, len(info.fields))
		for _, field := range info.fields {
			fieldTypes = append(fieldTypes, field.typ)
		}
		typeDefs = append(typeDefs, llvmStructTypeDef(info.name, fieldTypes))
	}
	for _, info := range g.enums {
		if info.hasPayload {
			payloadSlot := info.payloadTyp
			if info.isBoxed {
				payloadSlot = "ptr"
			}
			slotCount := info.payloadCount
			if slotCount < 1 {
				slotCount = 1
			}
			fields := make([]string, 0, 1+slotCount)
			fields = append(fields, "i64")
			for s := 0; s < slotCount; s++ {
				fields = append(fields, payloadSlot)
			}
			typeDefs = append(typeDefs, llvmStructTypeDef(info.name, fields))
		}
	}
	if len(g.tupleTypes) != 0 {
		names := make([]string, 0, len(g.tupleTypes))
		for name := range g.tupleTypes {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			info := g.tupleTypes[name]
			typeDefs = append(typeDefs, llvmStructTypeDef(strings.TrimPrefix(info.typ, "%"), info.elems))
		}
	}
	if len(g.resultTypes) != 0 {
		names := make([]string, 0, len(g.resultTypes))
		for name := range g.resultTypes {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			info := g.resultTypes[name]
			typeDefs = append(typeDefs, llvmStructTypeDef(strings.TrimPrefix(info.typ, "%"), []string{"i64", info.okTyp, info.errTyp}))
		}
	}
	// Phase 6a: interface fat-pointer type + per (impl, interface) vtable
	// globals are emitted only when at least one interface has
	// implementations — avoiding LLVM IR noise in modules that don't use
	// interfaces at all.
	if vtableDefs, vtableTypeDef := g.renderInterfaceVtables(); vtableTypeDef != "" {
		typeDefs = append(typeDefs, vtableTypeDef)
		allDefs = append(allDefs, vtableDefs...)
	}
	runtimeDecls := g.runtimeDeclarationIR()
	if g.needsGCRuntime {
		runtimeDecls = append(llvmGcRuntimeDeclarations(), runtimeDecls...)
	}
	if len(runtimeDecls) > 0 {
		return []byte(llvmRenderModuleWithRuntimeDeclarations(g.sourcePath, g.target, typeDefs, g.stringDefs, runtimeDecls, allDefs))
	}
	return []byte(llvmRenderModuleWithGlobalsAndTypes(g.sourcePath, g.target, typeDefs, g.stringDefs, allDefs))
}

// renderInterfaceVtables builds the LLVM IR for the interface
// fat-pointer type (`%osty.iface`) plus one `constant [N x ptr]`
// global per (implementer, interface) pair discovered during
// declaration collection.
//
// Returns (defs, typeDef) where typeDef is empty when no vtable is
// needed (no interfaces, or no implementations of any interface).
// Phase 6a scope: the globals are emitted and addressable via
// `@osty.vtable.<impl>__<iface>`, but no boxing or dispatch path
// consumes them yet — that lands in subsequent phases.
func (g *generator) renderInterfaceVtables() ([]string, string) {
	if len(g.interfacesByName) == 0 {
		return nil, ""
	}
	// Stable iteration over interfaces for deterministic IR output.
	ifaceNames := make([]string, 0, len(g.interfacesByName))
	for name := range g.interfacesByName {
		ifaceNames = append(ifaceNames, name)
	}
	sort.Strings(ifaceNames)
	var defs []string
	haveAny := false
	for _, ifaceName := range ifaceNames {
		iface := g.interfacesByName[ifaceName]
		if iface == nil || len(iface.impls) == 0 {
			continue
		}
		haveAny = true
		for _, impl := range iface.impls {
			implType := g.ownerTypeFor(impl)
			methodsByName := g.methods[implType]
			slots := make([]string, 0, len(iface.methods))
			for _, m := range iface.methods {
				sig := methodsByName[m.name]
				if sig == nil {
					slots = append(slots, "ptr null")
					continue
				}
				// Phase 6f follow-up: the vtable slot does not point
				// at the concrete method directly because interface
				// dispatch passes `self` as `ptr %data` while the
				// underlying method expects the struct/enum value (or
				// a `ptr` for mut receivers). A per-(impl, iface,
				// method) shim adapts the calling convention: it
				// loads the concrete value from the data pointer (or
				// forwards the pointer for mut receivers) and tail-
				// calls the real implementation.
				shimSym, shimDef := renderInterfaceShim(iface, impl, m, sig, implType)
				if shimDef != "" {
					defs = append(defs, shimDef)
					slots = append(slots, "ptr "+shimSym)
				} else {
					slots = append(slots, fmt.Sprintf("ptr @%s", sig.irName))
				}
			}
			defs = append(defs, fmt.Sprintf(
				"%s = constant [%d x ptr] [%s]",
				impl.vtableSym,
				len(iface.methods),
				strings.Join(slots, ", "),
			))
		}
	}
	if !haveAny {
		return nil, ""
	}
	typeDef := "%osty.iface = type { ptr, ptr }"
	return defs, typeDef
}

// renderInterfaceShim returns the `@osty.shim.<impl>__<iface>__<method>`
// symbol and the LLVM IR definition of a shim that adapts the
// interface dispatch ABI (`ptr %data, <non-self args>`) to the
// underlying method's calling convention.
//
// For immutable receivers the shim loads the struct/enum value from
// the data pointer before calling the real method; for `mut` receivers
// (where the method already takes `ptr %self`) the pointer is
// forwarded verbatim. A nil return signals a malformed signature the
// caller must handle by falling back to a direct `ptr @<method>`
// slot, which is still safe for receiver-less methods but will
// typically yield an empty-return shim when one is actually needed.
func renderInterfaceShim(iface *interfaceInfo, impl interfaceImpl, m interfaceMethodSig, sig *fnSig, implType string) (string, string) {
	if sig == nil || implType == "" {
		return "", ""
	}
	if len(sig.params) == 0 {
		// No receiver slot — nothing to adapt.
		return "", ""
	}
	receiver := sig.params[0]
	shimSym := fmt.Sprintf("@osty.shim.%s__%s__%s", impl.implName, iface.name, m.name)
	ret := sig.ret
	if ret == "" {
		ret = "void"
	}
	// Build the shim's parameter list: data ptr followed by any
	// non-self user args. The underlying method will consume them
	// with its own declared types.
	shimParams := []string{"ptr %self.ptr"}
	callArgs := make([]string, 0, len(sig.params))
	if receiver.byRef {
		// Mut receiver already expects a pointer — forward it.
		callArgs = append(callArgs, "ptr %self.ptr")
	} else {
		// Immutable receiver expects the struct/enum value.
		callArgs = append(callArgs, fmt.Sprintf("%s %%self.val", receiver.typ))
	}
	for i, p := range sig.params[1:] {
		name := fmt.Sprintf("%%a%d", i)
		shimParams = append(shimParams, fmt.Sprintf("%s %s", p.typ, name))
		callArgs = append(callArgs, fmt.Sprintf("%s %s", p.typ, name))
	}
	var body strings.Builder
	if !receiver.byRef {
		body.WriteString(fmt.Sprintf("  %%self.val = load %s, ptr %%self.ptr\n", receiver.typ))
	}
	if ret == "void" {
		// Void call sites must NOT bind an SSA destination —
		// `%x = call void @f()` is not valid LLVM IR. Emit the bare
		// call + `ret void`.
		body.WriteString(fmt.Sprintf("  call void @%s(%s)\n", sig.irName, strings.Join(callArgs, ", ")))
		body.WriteString("  ret void\n")
	} else {
		body.WriteString(fmt.Sprintf("  %%ret.val = call %s @%s(%s)\n", ret, sig.irName, strings.Join(callArgs, ", ")))
		body.WriteString(fmt.Sprintf("  ret %s %%ret.val\n", ret))
	}
	def := fmt.Sprintf("define internal %s %s(%s) {\n%s}",
		ret, shimSym, strings.Join(shimParams, ", "), body.String())
	return shimSym, def
}

// ownerTypeFor returns the LLVM IR type symbol (`%Name`) a given
// interface implementation refers to. Struct implementations carry the
// struct's LLVM type name; enum implementations carry the enum's
// tag/payload struct name.
func (g *generator) ownerTypeFor(impl interfaceImpl) string {
	switch impl.kind {
	case 0:
		if info := g.structsByName[impl.implName]; info != nil {
			return info.typ
		}
	case 1:
		if info := g.enumsByName[impl.implName]; info != nil {
			return info.typ
		}
	}
	return ""
}

func (g *generator) runtimeDeclarationIR() []string {
	if len(g.runtimeDeclOrder) == 0 {
		return nil
	}
	out := make([]string, 0, len(g.runtimeDeclOrder))
	for _, symbol := range g.runtimeDeclOrder {
		decl, ok := g.runtimeDecls[symbol]
		if !ok {
			continue
		}
		paramTypes := make([]string, 0, len(decl.params))
		for _, param := range decl.params {
			paramTypes = append(paramTypes, param.typ)
		}
		out = append(out, fmt.Sprintf("declare %s @%s(%s)", decl.ret, decl.symbol, strings.Join(paramTypes, ", ")))
	}
	return out
}

func (g *generator) renderFunction(ret, name string, params []paramInfo) string {
	return llvmRenderFunction(ret, name, toLLVMParams(params), g.body)
}

func (g *generator) typeEnv() typeEnv {
	return typeEnv{
		structs:    g.structsByName,
		enums:      g.enumsByName,
		interfaces: g.interfacesByName,
		aliases:    g.typeAliasesByName,
	}
}

func (g *generator) lookupGlobal(name string) (value, bool) {
	if g.globals == nil {
		return value{}, false
	}
	v, ok := g.globals[name]
	return v, ok
}

func (g *generator) lookupBinding(name string) (value, bool) {
	if v, ok := g.lookupLocal(name); ok {
		return v, true
	}
	return g.lookupGlobal(name)
}

func (g *generator) toOstyEmitter() *LlvmEmitter {
	return &LlvmEmitter{
		temp:          g.temp,
		label:         g.label,
		stringId:      g.stringID,
		body:          append([]string(nil), g.body...),
		stringGlobals: append([]*LlvmStringGlobal(nil), g.stringDefs...),
	}
}

func (g *generator) takeOstyEmitter(emitter *LlvmEmitter) {
	g.temp = emitter.temp
	g.label = emitter.label
	g.stringID = emitter.stringId
	g.body = emitter.body
	g.stringDefs = emitter.stringGlobals
}

func toOstyValue(v value) *LlvmValue {
	return &LlvmValue{
		typ:     v.typ,
		name:    v.ref,
		pointer: v.ptr,
	}
}

func fromOstyValue(v *LlvmValue) value {
	return value{
		typ: v.typ,
		ref: v.name,
		ptr: v.pointer,
	}
}

func plainStringLiteral(lit *ast.StringLit) (string, bool) {
	if lit == nil || lit.IsRaw || lit.IsTriple {
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

func structTypeExprName(expr ast.Expr) (string, bool) {
	id, ok := expr.(*ast.Ident)
	if !ok || id.Name == "" {
		return "", false
	}
	return id.Name, true
}

func toLLVMParams(params []paramInfo) []*LlvmParam {
	out := make([]*LlvmParam, 0, len(params))
	for _, p := range params {
		out = append(out, llvmParam(p.name, llvmParamIRType(p)))
	}
	return out
}

func (g *generator) enterBlock(label string) {
	g.currentBlock = label
	g.currentReachable = true
}

func (g *generator) leaveBlock() {
	g.currentBlock = ""
	g.currentReachable = false
}

func (g *generator) branchTo(label string) {
	emitter := g.toOstyEmitter()
	emitter.body = append(emitter.body, fmt.Sprintf("  br label %%%s", label))
	g.takeOstyEmitter(emitter)
	g.leaveBlock()
}

func (g *generator) nextNamedLabel(prefix string) string {
	emitter := g.toOstyEmitter()
	label := llvmNextLabel(emitter, prefix)
	g.takeOstyEmitter(emitter)
	return label
}

func (g *generator) emitScopedStmtBlock(stmts []ast.Stmt) error {
	scopeDepth := len(g.locals)
	g.pushScope()
	if err := g.emitBlock(stmts); err != nil {
		if len(g.locals) > scopeDepth {
			g.popScope()
		}
		return err
	}
	if len(g.locals) > scopeDepth {
		g.popScope()
	}
	return nil
}

func (g *generator) captureScopeState() scopeState {
	locals := append([]map[string]value(nil), g.locals...)
	gcRootSlots := append([]value(nil), g.gcRootSlots...)
	gcRootMarks := append([]int(nil), g.gcRootMarks...)
	return scopeState{
		locals:      locals,
		gcRootSlots: gcRootSlots,
		gcRootMarks: gcRootMarks,
	}
}

func (g *generator) restoreScopeState(state scopeState) {
	g.locals = append([]map[string]value(nil), state.locals...)
	g.gcRootSlots = append([]value(nil), state.gcRootSlots...)
	g.gcRootMarks = append([]int(nil), state.gcRootMarks...)
}

func (g *generator) pushScope() {
	g.locals = append(g.locals, map[string]value{})
	g.gcRootMarks = append(g.gcRootMarks, len(g.gcRootSlots))
}

func (g *generator) popScope() {
	mark := 0
	if len(g.gcRootMarks) != 0 {
		mark = g.gcRootMarks[len(g.gcRootMarks)-1]
		g.gcRootMarks = g.gcRootMarks[:len(g.gcRootMarks)-1]
	}
	if mark < len(g.gcRootSlots) {
		if g.currentReachable {
			emitter := g.toOstyEmitter()
			for i := len(g.gcRootSlots) - 1; i >= mark; i-- {
				llvmGcRootRelease(emitter, toOstyValue(g.gcRootSlots[i]))
			}
			g.takeOstyEmitter(emitter)
		}
		g.gcRootSlots = g.gcRootSlots[:mark]
	}
	g.locals = g.locals[:len(g.locals)-1]
}

func (g *generator) bindNamedLocal(name string, v value, mutable bool) {
	if mutable || (v.typ == "ptr" && valueNeedsManagedRoot(v)) || len(v.rootPaths) != 0 {
		emitter := g.toOstyEmitter()
		slot := llvmMutableLetSlot(emitter, name, toOstyValue(v))
		slotValue := fromOstyValue(slot)
		copyContainerMetadata(&slotValue, v)
		slotValue.mutable = mutable
		slotValue.rootPaths = cloneRootPaths(v.rootPaths)
		g.bindGCRootIfManagedPointer(emitter, slotValue)
		g.takeOstyEmitter(emitter)
		g.bindLocal(name, slotValue)
		return
	}
	v.mutable = false
	g.bindLocal(name, v)
}

func valueNeedsManagedRoot(v value) bool {
	return v.gcManaged || v.listElemTyp != "" || v.mapKeyTyp != "" || v.setElemTyp != ""
}

func copyContainerMetadata(dst *value, src value) {
	dst.listElemTyp = src.listElemTyp
	dst.listElemString = src.listElemString
	dst.mapKeyTyp = src.mapKeyTyp
	dst.mapValueTyp = src.mapValueTyp
	dst.mapKeyString = src.mapKeyString
	dst.setElemTyp = src.setElemTyp
	dst.setElemString = src.setElemString
	dst.sourceType = src.sourceType
	dst.fnSigRef = src.fnSigRef
	dst.gcManaged = valueNeedsManagedRoot(*dst)
}

func mergeContainerMetadata(dst *value, left, right value) {
	if left.listElemTyp != "" && left.listElemTyp == right.listElemTyp {
		dst.listElemTyp = left.listElemTyp
		dst.listElemString = left.listElemString && right.listElemString
	}
	if left.mapKeyTyp != "" && left.mapKeyTyp == right.mapKeyTyp && left.mapValueTyp == right.mapValueTyp {
		dst.mapKeyTyp = left.mapKeyTyp
		dst.mapValueTyp = left.mapValueTyp
		dst.mapKeyString = left.mapKeyString && right.mapKeyString
	}
	if left.setElemTyp != "" && left.setElemTyp == right.setElemTyp {
		dst.setElemTyp = left.setElemTyp
		dst.setElemString = left.setElemString && right.setElemString
	}
	dst.gcManaged = valueNeedsManagedRoot(*dst)
}

type gcSafepointRoot struct {
	slot value
	path []int
}

func cloneRootPaths(paths [][]int) [][]int {
	if len(paths) == 0 {
		return nil
	}
	out := make([][]int, 0, len(paths))
	for _, path := range paths {
		next := append([]int(nil), path...)
		out = append(out, next)
	}
	return out
}

func prependRootIndex(index int, paths [][]int) [][]int {
	if len(paths) == 0 {
		return nil
	}
	out := make([][]int, 0, len(paths))
	for _, path := range paths {
		next := make([]int, 0, len(path)+1)
		next = append(next, index)
		next = append(next, path...)
		out = append(out, next)
	}
	return out
}

func llvmPointerOperand(name string) string {
	if name == "" || name == "null" || strings.HasPrefix(name, "@") || strings.HasPrefix(name, "%") {
		return name
	}
	return "@" + name
}

func (g *generator) visibleSafepointRoots() []gcSafepointRoot {
	seen := map[string]struct{}{}
	out := []gcSafepointRoot{}
	for i := len(g.locals) - 1; i >= 0; i-- {
		names := make([]string, 0, len(g.locals[i]))
		for name := range g.locals[i] {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if _, exists := seen[name]; exists {
				continue
			}
			seen[name] = struct{}{}
			slot := g.locals[i][name]
			if !slot.ptr {
				continue
			}
			if slot.typ == "ptr" && slot.gcManaged {
				out = append(out, gcSafepointRoot{slot: slot})
			}
			for _, path := range slot.rootPaths {
				out = append(out, gcSafepointRoot{
					slot: slot,
					path: append([]int(nil), path...),
				})
			}
		}
	}
	return out
}

func (g *generator) safepointRootAddress(emitter *LlvmEmitter, root gcSafepointRoot) string {
	if len(root.path) == 0 {
		return root.slot.ref
	}
	addr := root.slot.ref
	currentType := root.slot.typ
	for _, index := range root.path {
		fieldPtr := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, fmt.Sprintf(
			"  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d",
			fieldPtr,
			currentType,
			addr,
			index,
		))
		nextType, ok := g.aggregateFieldType(currentType, index)
		if !ok {
			return addr
		}
		addr = fieldPtr
		currentType = nextType
	}
	return addr
}

func (g *generator) traceCallbackSymbol(typ string, rootPaths [][]int) string {
	if typ == "" {
		return "null"
	}
	if typ == "ptr" {
		g.declareRuntimeSymbol("osty.gc.mark_slot_v1", "void", []paramInfo{{typ: "ptr"}})
		g.needsGCRuntime = true
		return "osty.gc.mark_slot_v1"
	}
	if len(rootPaths) == 0 {
		return "null"
	}
	key := typ + ":" + fmt.Sprint(rootPaths)
	if name, ok := g.traceHelpers[key]; ok {
		return name
	}
	name := fmt.Sprintf("osty_rt_trace_%d", len(g.traceHelpers))
	g.traceHelpers[key] = name
	g.declareRuntimeSymbol("osty.gc.mark_slot_v1", "void", []paramInfo{{typ: "ptr"}})
	g.needsGCRuntime = true
	body := []string{}
	currentType := ""
	for _, path := range rootPaths {
		addr := "%value.addr"
		currentType = typ
		for _, index := range path {
			fieldPtr := fmt.Sprintf("%%trace.field.%d.%d", len(body), index)
			body = append(body, fmt.Sprintf(
				"  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d",
				fieldPtr,
				currentType,
				addr,
				index,
			))
			nextType, ok := g.aggregateFieldType(currentType, index)
			if !ok {
				currentType = ""
				break
			}
			addr = fieldPtr
			currentType = nextType
		}
		if currentType == "" {
			continue
		}
		body = append(body, fmt.Sprintf("  call void @osty.gc.mark_slot_v1(ptr %s)", addr))
	}
	body = append(body, "  ret void")
	g.traceHelperDefs = append(g.traceHelperDefs, llvmRenderFunction("void", name, []*LlvmParam{llvmParam("value.addr", "ptr")}, body))
	return name
}

func (g *generator) spillValueAddress(emitter *LlvmEmitter, prefix string, v value) string {
	slot := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = alloca %s", slot, v.typ))
	emitter.body = append(emitter.body, fmt.Sprintf("  store %s %s, ptr %s", v.typ, v.ref, slot))
	return slot
}

func (g *generator) loadValueFromAddress(emitter *LlvmEmitter, typ, addr string) value {
	loaded := fromOstyValue(llvmLoad(emitter, &LlvmValue{typ: typ, name: addr, pointer: true}))
	return loaded
}

func (g *generator) emitTypeSize(emitter *LlvmEmitter, typ string) *LlvmValue {
	switch typ {
	case "i64", "double", "ptr":
		return llvmI64("8")
	case "i1":
		return llvmI64("1")
	}
	ptr := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = getelementptr %s, ptr null, i32 1", ptr, typ))
	out := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = ptrtoint ptr %s to i64", out, ptr))
	return llvmI64(out)
}

func (g *generator) bindLocal(name string, v value) {
	g.locals[len(g.locals)-1][name] = v
}

func (g *generator) pushLoop(loop loopContext) {
	g.loopStack = append(g.loopStack, loop)
}

func (g *generator) popLoop() {
	if len(g.loopStack) == 0 {
		return
	}
	g.loopStack = g.loopStack[:len(g.loopStack)-1]
}

func (g *generator) currentLoop() (loopContext, bool) {
	if len(g.loopStack) == 0 {
		return loopContext{}, false
	}
	return g.loopStack[len(g.loopStack)-1], true
}

func (g *generator) unwindScopesTo(scopeDepth int) {
	for len(g.locals) > scopeDepth {
		g.popScope()
	}
}

func (g *generator) emitBreak() error {
	loop, ok := g.currentLoop()
	if !ok {
		return unsupported("control-flow", "break outside of loop")
	}
	g.unwindScopesTo(loop.scopeDepth)
	g.branchTo(loop.breakLabel)
	return nil
}

func (g *generator) emitContinue() error {
	loop, ok := g.currentLoop()
	if !ok {
		return unsupported("control-flow", "continue outside of loop")
	}
	g.unwindScopesTo(loop.scopeDepth)
	g.branchTo(loop.continueLabel)
	return nil
}

func (g *generator) nextHiddenLocalName(prefix string) string {
	name := fmt.Sprintf("$%s.%d", prefix, g.hiddenLocalID)
	g.hiddenLocalID++
	return name
}

func (g *generator) needsSafepointProtection(v value) bool {
	if v.ptr {
		return false
	}
	return (v.typ == "ptr" && v.gcManaged) || len(v.rootPaths) != 0
}

func (g *generator) protectManagedTemporary(prefix string, v value) value {
	if !g.needsSafepointProtection(v) {
		return v
	}
	name := g.nextHiddenLocalName(prefix)
	g.bindNamedLocal(name, v, false)
	protected, ok := g.lookupLocal(name)
	if !ok {
		return v
	}
	return protected
}

func (g *generator) lookupLocal(name string) (value, bool) {
	for i := len(g.locals) - 1; i >= 0; i-- {
		if v, ok := g.locals[i][name]; ok {
			return v, true
		}
	}
	return value{}, false
}

func (g *generator) bindLetPattern(pattern ast.Pattern, v value, mutable bool) error {
	switch p := pattern.(type) {
	case nil:
		return unsupported("statement", "let requires a pattern")
	case *ast.WildcardPat:
		if mutable {
			return unsupported("statement", "wildcard let patterns cannot be mutable")
		}
		return nil
	case *ast.IdentPat:
		if p.Name == "" {
			return unsupported("statement", "empty let binding name")
		}
		if !llvmIsIdent(p.Name) {
			return unsupportedf("name", "let name %q", p.Name)
		}
		g.bindNamedLocal(p.Name, v, mutable)
		return nil
	case *ast.BindingPat:
		if mutable {
			return unsupported("statement", "binding let patterns cannot be mutable yet")
		}
		if p.Name == "" {
			return unsupported("statement", "empty let binding name")
		}
		if !llvmIsIdent(p.Name) {
			return unsupportedf("name", "let name %q", p.Name)
		}
		g.bindNamedLocal(p.Name, v, false)
		return g.bindLetPattern(p.Pattern, v, false)
	case *ast.TuplePat:
		if mutable {
			return unsupported("statement", "tuple let patterns cannot be mutable yet")
		}
		info, ok := g.tupleTypes[v.typ]
		if !ok {
			return unsupportedf("type-system", "tuple pattern on %s", v.typ)
		}
		if len(p.Elems) != len(info.elems) {
			return unsupportedf("statement", "tuple pattern arity %d, value %d", len(p.Elems), len(info.elems))
		}
		for i, elemPat := range p.Elems {
			elemValue, err := g.extractTupleElement(v, info, i)
			if err != nil {
				return err
			}
			if err := g.bindLetPattern(elemPat, elemValue, false); err != nil {
				return err
			}
		}
		return nil
	case *ast.StructPat:
		if mutable {
			return unsupported("statement", "struct let patterns cannot be mutable yet")
		}
		if err := g.validateStructLetPatternType(p, v.typ); err != nil {
			return err
		}
		info := g.structsByType[v.typ]
		if info == nil {
			return unsupportedf("type-system", "struct pattern on %s", v.typ)
		}
		seen := map[string]bool{}
		for _, fieldPat := range p.Fields {
			if fieldPat == nil {
				return unsupportedf("statement", "struct pattern %q has nil field", info.name)
			}
			if fieldPat.Name == "" {
				return unsupportedf("statement", "struct pattern %q has unnamed field", info.name)
			}
			if seen[fieldPat.Name] {
				return unsupportedf("statement", "struct pattern %q duplicate field %q", info.name, fieldPat.Name)
			}
			seen[fieldPat.Name] = true
			fieldValue, err := g.extractStructField(v, info, fieldPat.Name)
			if err != nil {
				return err
			}
			fieldPattern := fieldPat.Pattern
			if fieldPattern == nil {
				fieldPattern = &ast.IdentPat{Name: fieldPat.Name}
			}
			if err := g.bindLetPattern(fieldPattern, fieldValue, false); err != nil {
				return err
			}
		}
		return nil
	case *ast.VariantPat:
		return unsupported("statement", "let patterns must be irrefutable; use if let or match for enum variants")
	default:
		return unsupported("statement", "only identifier, wildcard, tuple, binding, and struct let patterns are supported")
	}
}

func (g *generator) extractTupleElement(tuple value, info tupleTypeInfo, index int) (value, error) {
	if index < 0 || index >= len(info.elems) {
		return value{}, unsupportedf("expression", "tuple index %d out of range", index)
	}
	emitter := g.toOstyEmitter()
	out := llvmExtractValue(emitter, toOstyValue(tuple), info.elems[index], index)
	g.takeOstyEmitter(emitter)
	elem := fromOstyValue(out)
	if index < len(info.elemListElemTyps) && info.elemListElemTyps[index] != "" {
		elem.listElemTyp = info.elemListElemTyps[index]
	}
	elem.gcManaged = info.elems[index] == "ptr" || elem.listElemTyp != ""
	elem.rootPaths = g.rootPathsForType(info.elems[index])
	return elem, nil
}

func (g *generator) validateStructLetPatternType(pattern *ast.StructPat, valueTyp string) error {
	if pattern == nil || len(pattern.Type) == 0 {
		return nil
	}
	name := pattern.Type[len(pattern.Type)-1]
	if name == "" {
		return nil
	}
	if info := g.structsByName[name]; info != nil {
		if info.typ != valueTyp {
			return unsupportedf("type-system", "struct pattern %q on %s", strings.Join(pattern.Type, "."), valueTyp)
		}
		return nil
	}
	resolved, ok, err := resolveAliasNamedTarget(name, g.typeEnv(), map[string]bool{})
	if err != nil {
		return err
	}
	if ok {
		if info := g.structsByName[resolved]; info != nil && info.typ != valueTyp {
			return unsupportedf("type-system", "struct pattern %q on %s", strings.Join(pattern.Type, "."), valueTyp)
		}
	}
	return nil
}

func (g *generator) extractStructField(base value, info *structInfo, name string) (value, error) {
	if info == nil {
		return value{}, unsupported("type-system", "field extraction on nil struct info")
	}
	field, ok := info.byName[name]
	if !ok {
		return value{}, unsupportedf("expression", "struct %q has no field %q", info.name, name)
	}
	emitter := g.toOstyEmitter()
	out := llvmExtractValue(emitter, toOstyValue(base), field.typ, field.index)
	g.takeOstyEmitter(emitter)
	loaded := fromOstyValue(out)
	loaded.listElemTyp = field.listElemTyp
	loaded.listElemString = field.listElemString
	loaded.mapKeyTyp = field.mapKeyTyp
	loaded.mapValueTyp = field.mapValueTyp
	loaded.mapKeyString = field.mapKeyString
	loaded.setElemTyp = field.setElemTyp
	loaded.setElemString = field.setElemString
	loaded.sourceType = field.sourceType
	loaded.gcManaged = valueNeedsManagedRoot(loaded)
	loaded.rootPaths = g.rootPathsForType(field.typ)
	return loaded, nil
}

func identPatternName(p ast.Pattern) (string, error) {
	id, ok := p.(*ast.IdentPat)
	if !ok || id.Name == "" {
		return "", unsupported("statement", "only identifier let patterns are supported")
	}
	if !llvmIsIdent(id.Name) {
		return "", unsupportedf("name", "let name %q", id.Name)
	}
	return id.Name, nil
}
