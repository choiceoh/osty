// native_entry_snapshot.go snapshots the native-owned primitive module-entry
// slice from toolchain/llvmgen.osty into the bootstrap bridge. The Go side
// projects internal/ir.Module into these LLVM-typed structs, then falls back
// to the legacy IR -> AST bridge when a shape still sits outside this slice.

package llvmgen

import (
	"fmt"
	"strings"
)

type llvmNativeExprKind int

const (
	llvmNativeExprInvalid llvmNativeExprKind = iota
	llvmNativeExprInt
	llvmNativeExprFloat
	llvmNativeExprBool
	llvmNativeExprString
	llvmNativeExprIdent
	llvmNativeExprStructLit
	llvmNativeExprListLit
	llvmNativeExprMapLit
	llvmNativeExprField
	llvmNativeExprListIndex
	llvmNativeExprMapIndex
	llvmNativeExprUnary
	llvmNativeExprBinary
	llvmNativeExprCall
	llvmNativeExprPrintln
	llvmNativeExprIf
	llvmNativeExprOptionCheck
	llvmNativeExprCoalesce
	llvmNativeExprQuestion
	llvmNativeExprOptionalField
	// llvmNativeExprInterfaceBox boxes a concrete struct value into
	// an `%osty.iface` fat pointer. Shape:
	//
	//   childExprs[0] = concrete value expression (%<S>)
	//   name          = vtable symbol (e.g. "@osty.vtable.Vec__Sized")
	//   llvmType      = always "%osty.iface"
	//
	// Emits: spill concrete to slot, then two insertvalue calls to
	// build the (data_ptr, vtable_ptr) fat pointer.
	llvmNativeExprInterfaceBox
	// llvmNativeExprClosureEnvAlloc materializes the env pointer for
	// a no-capture closure literal:
	//
	//   %site = <string literal ptr>
	//   %env  = call ptr @osty.rt.closure_env_alloc_v2(i64 0, ptr %site, i64 0)
	//   store ptr <thunk_sym>, ptr %env
	//
	// `name` carries the thunk symbol (e.g. "@__osty_closure_thunk___osty_closure_1"),
	// `text` carries the site label string (e.g. "runtime.closure.env.ptr").
	// `llvmType` is always "ptr".
	llvmNativeExprClosureEnvAlloc
	// llvmNativeExprFnValueCall dispatches a call through a
	// fn-value env pointer. The first childExpr is the env ptr
	// (evaluated to produce an i8* / ptr); subsequent childExprs are
	// the non-env args. `text` carries the comma-separated LLVM
	// types of the non-env args so the indirect call signature
	// matches the thunk's ABI. `llvmType` is the call's return
	// type (use "void" for unit-returning fns).
	llvmNativeExprFnValueCall
	// llvmNativeExprInterfaceCall dispatches a method call through
	// an `%osty.iface` fat pointer:
	//
	//   childExprs[0]   = receiver (iface value)
	//   childExprs[1..] = non-self args
	//   fieldIndex      = vtable slot index (0-based)
	//   llvmType        = return type
	//   name            = callee method name (cosmetic; SSA names are fresh)
	//   text            = comma-separated LLVM types of non-self args
	//                     (empty when no args) — carried to emit so the
	//                     call instruction sees the exact ABI signature.
	//
	// Emits: extract data + vtable, GEP into vtable slot, load fn ptr,
	// indirect call.
	llvmNativeExprInterfaceCall
)

type llvmNativeStmtKind int

const (
	llvmNativeStmtInvalid llvmNativeStmtKind = iota
	llvmNativeStmtExpr
	llvmNativeStmtLet
	llvmNativeStmtMutLet
	llvmNativeStmtAssign
	llvmNativeStmtFieldAssign
	llvmNativeStmtReturn
	llvmNativeStmtIf
	llvmNativeStmtWhile
	llvmNativeStmtRange
)

type llvmNativeExpr struct {
	kind                llvmNativeExprKind
	llvmType            string
	text                string
	name                string
	op                  string
	fieldIndex          int
	baseLLVMType        string
	elemLLVMType        string
	mapKeyLLVMType      string
	mapKeyIsString      bool
	mapValueLLVMType    string
	mapValueIsString    bool
	optionInnerLLVMType string
	firstArgByRef       bool
	receiverPath        []*llvmNativeFieldPath
	spillArgIndices     []int
	boolValue           bool
	inclusive           bool
	childExprs          []*llvmNativeExpr
	childBlocks         []*llvmNativeBlock
}

type llvmNativeFieldPath struct {
	llvmType   string
	fieldIndex int
}

type llvmNativeStmt struct {
	kind        llvmNativeStmtKind
	name        string
	llvmType    string
	op          string
	inclusive   bool
	childExprs  []*llvmNativeExpr
	childBlocks []*llvmNativeBlock
	fieldPath   []*llvmNativeFieldPath
}

type llvmNativeBlock struct {
	stmts     []*llvmNativeStmt
	hasResult bool
	result    *llvmNativeExpr
}

type llvmNativeParam struct {
	name             string
	llvmType         string
	irType           string
	byRef            bool
	listElemLLVMType string
}

type llvmNativeGlobal struct {
	name     string
	irName   string
	llvmType string
	mutable  bool
	init     string
}

type llvmNativeStructField struct {
	name     string
	llvmType string
}

type llvmNativeStruct struct {
	name     string
	llvmType string
	fields   []*llvmNativeStructField
}

// llvmNativeEnumVariant mirrors toolchain/llvmgen.osty's
// `LlvmNativeEnumVariant`. `tag` is the 0-based discriminant in
// declaration order; `payloadType` is the LLVM type string of the
// single payload slot (empty for payload-free variants).
type llvmNativeEnumVariant struct {
	name        string
	tag         int
	payloadType string
}

// llvmNativeEnum mirrors toolchain/llvmgen.osty's `LlvmNativeEnum`.
// Storage layout is `{ i64 tag, <payloadSlotType> }` or `{ i64 tag }`.
// By convention `name` is the bare mangled identifier (e.g.
// "_ZTSN4main5MaybeIlEE") and `llvmType` is the same with a leading
// `%`. The IR projection layer owns that invariant.
type llvmNativeEnum struct {
	name            string
	llvmType        string
	payloadSlotType string
	variants        []*llvmNativeEnumVariant
}

type llvmNativeFunction struct {
	name       string
	returnType string
	params     []*llvmNativeParam
	body       *llvmNativeBlock
	vectorize  bool
	inlineMode int
}

type llvmNativeModule struct {
	sourcePath         string
	target             string
	globals            []*llvmNativeGlobal
	structs            []*llvmNativeStruct
	enums              []*llvmNativeEnum
	interfaceImpls     []nativeInterfaceImpl
	stringGlobals      []*LlvmStringGlobal
	functions          []*llvmNativeFunction
	needsListRuntime   bool
	needsMapRuntime    bool
	needsSetRuntime    bool
	needsStringRuntime bool
	// projectionCtx is a Go-only handle the finalizer reads to
	// emit per-module post-processing surfaces (closure thunks,
	// future batches). Not present in the Osty mirror — the
	// snapshot regen script drops unknown fields anyway.
	projectionCtx     *nativeProjectionCtx
	extraRuntimeDecls []string
}

type llvmNativeRenderedFunction struct {
	definition    string
	stringGlobals []*LlvmStringGlobal
	nextStringID  int
	loopMDDefs    []string
	nextLoopMD    int
}

type llvmNativeMaybeValue struct {
	hasValue bool
	value    *LlvmValue
}

func llvmNativeEmitModule(mod *llvmNativeModule) string {
	if mod == nil {
		return llvmRenderModule("", "", nil)
	}
	typeDefs := make([]string, 0, len(mod.structs)+1)
	definitions := make([]string, 0, len(mod.globals)+len(mod.functions))
	stringGlobals := append([]*LlvmStringGlobal(nil), mod.stringGlobals...)
	nextStringID := len(stringGlobals)
	loopMDDefs := make([]string, 0, len(mod.functions))
	nextLoopMD := 0
	if len(mod.interfaceImpls) != 0 {
		typeDefs = append(typeDefs, "%osty.iface = type { ptr, ptr }")
	}
	for _, st := range mod.structs {
		if st == nil {
			continue
		}
		fieldTypes := make([]string, 0, len(st.fields))
		for _, field := range st.fields {
			fieldTypes = append(fieldTypes, field.llvmType)
		}
		typeDefs = append(typeDefs, llvmStructTypeDef(strings.TrimPrefix(st.llvmType, "%"), fieldTypes))
	}
	for _, en := range mod.enums {
		if en == nil {
			continue
		}
		typeDefs = append(typeDefs, llvmNativeEmitEnumTypeDef(en))
	}
	for _, global := range mod.globals {
		if global == nil {
			continue
		}
		kind := "constant"
		if global.mutable {
			kind = "global"
		}
		definitions = append(definitions, global.irName+" = internal "+kind+" "+global.llvmType+" "+global.init)
	}
	for _, impl := range mod.interfaceImpls {
		if ir := strings.TrimSpace(string(emitNativeInterfaceVtable(impl))); ir != "" {
			definitions = append(definitions, ir)
		}
	}
	for _, fn := range mod.functions {
		rendered := llvmNativeEmitFunction(fn, mod.globals, nextStringID, nextLoopMD)
		nextStringID = rendered.nextStringID
		nextLoopMD = rendered.nextLoopMD
		definitions = append(definitions, rendered.definition)
		stringGlobals = append(stringGlobals, rendered.stringGlobals...)
		loopMDDefs = append(loopMDDefs, rendered.loopMDDefs...)
	}
	for _, impl := range mod.interfaceImpls {
		for _, method := range impl.methods {
			if ir := strings.TrimSpace(string(emitNativeInterfaceShim(impl, method))); ir != "" {
				definitions = append(definitions, ir)
			}
		}
	}
	definitions = append(definitions, loopMDDefs...)
	return llvmRenderModuleWithRuntimeDeclarations(
		mod.sourcePath,
		mod.target,
		typeDefs,
		stringGlobals,
		llvmNativeRuntimeDeclarations(mod),
		definitions,
	)
}

// llvmNativeEmitEnumTypeDef mirrors the Osty-side helper in
// toolchain/llvmgen.osty. Enums lower to tagged-union structs:
// `{ i64 tag, <payload> }`. Variant construction/destruction reuses
// the existing struct machinery (llvmStructLiteral / llvmExtractValue)
// in the projection layer, so the emit path has no variant-literal
// primitive of its own.
func llvmNativeEmitEnumTypeDef(en *llvmNativeEnum) string {
	if en == nil {
		return ""
	}
	fieldTypes := []string{"i64"}
	if en.payloadSlotType != "" {
		fieldTypes = append(fieldTypes, en.payloadSlotType)
	}
	return llvmStructTypeDef(en.name, fieldTypes)
}

func llvmNativeRuntimeDeclarations(mod *llvmNativeModule) []string {
	if mod == nil {
		return nil
	}
	out := make([]string, 0, 32)
	seen := map[string]bool{}
	appendUnique := func(decls []string) {
		for _, decl := range decls {
			if decl == "" || seen[decl] {
				continue
			}
			seen[decl] = true
			out = append(out, decl)
		}
	}
	if mod.needsListRuntime {
		appendUnique(llvmListRuntimeDeclarations())
	}
	if mod.needsMapRuntime {
		appendUnique(llvmMapRuntimeDeclarations())
	}
	if mod.needsSetRuntime {
		appendUnique(llvmSetRuntimeDeclarations())
	}
	if mod.needsStringRuntime {
		appendUnique(llvmStringRuntimeDeclarations())
	}
	appendUnique(mod.extraRuntimeDecls)
	return out
}

func llvmNativeCallPreservesScalarListParam(expr *llvmNativeExpr, paramName string) bool {
	if expr == nil || paramName == "" {
		return false
	}
	if expr.name != llvmListRuntimeLenSymbol() || len(expr.childExprs) != 1 {
		return false
	}
	arg := expr.childExprs[0]
	return arg != nil && arg.kind == llvmNativeExprIdent && arg.name == paramName
}

func llvmNativePruneScalarListParamsInExpr(expr *llvmNativeExpr, eligible map[string]string) {
	if expr == nil || len(eligible) == 0 {
		return
	}
	if expr.kind == llvmNativeExprCall {
		for _, arg := range expr.childExprs {
			if arg == nil || arg.kind != llvmNativeExprIdent {
				continue
			}
			if _, ok := eligible[arg.name]; ok && !llvmNativeCallPreservesScalarListParam(expr, arg.name) {
				delete(eligible, arg.name)
			}
		}
	}
	for _, child := range expr.childExprs {
		llvmNativePruneScalarListParamsInExpr(child, eligible)
	}
	for _, block := range expr.childBlocks {
		llvmNativePruneScalarListParamsInBlock(block, eligible)
	}
}

func llvmNativePruneScalarListParamsInStmt(stmt *llvmNativeStmt, eligible map[string]string) {
	if stmt == nil || len(eligible) == 0 {
		return
	}
	if stmt.name != "" {
		delete(eligible, stmt.name)
	}
	for _, expr := range stmt.childExprs {
		llvmNativePruneScalarListParamsInExpr(expr, eligible)
	}
	for _, block := range stmt.childBlocks {
		llvmNativePruneScalarListParamsInBlock(block, eligible)
	}
}

func llvmNativePruneScalarListParamsInBlock(block *llvmNativeBlock, eligible map[string]string) {
	if block == nil || len(eligible) == 0 {
		return
	}
	for _, stmt := range block.stmts {
		llvmNativePruneScalarListParamsInStmt(stmt, eligible)
	}
	if block.result != nil {
		llvmNativePruneScalarListParamsInExpr(block.result, eligible)
	}
}

func llvmNativeEligibleScalarListParams(fn *llvmNativeFunction) map[string]string {
	eligible := map[string]string{}
	if fn == nil {
		return eligible
	}
	for _, param := range fn.params {
		if param == nil || param.byRef || !listUsesRawDataFastPath(param.listElemLLVMType) {
			continue
		}
		eligible[param.name] = param.listElemLLVMType
	}
	if len(eligible) == 0 {
		return eligible
	}
	llvmNativePruneScalarListParamsInBlock(fn.body, eligible)
	return eligible
}

func llvmNativePrimeScalarListParamFastPath(emitter *LlvmEmitter, paramName, elemLLVM string) {
	if emitter == nil || paramName == "" || elemLLVM == "" {
		return
	}
	list := llvmIdent(emitter, paramName)
	if list == nil || list.typ != "ptr" {
		return
	}
	emitter.nativeListData[paramName] = llvmListData(emitter, list, elemLLVM)
	emitter.nativeListLens[paramName] = llvmListLen(emitter, list)
}

func llvmNativeFastScalarListIndex(emitter *LlvmEmitter, paramName, idxName string, idxAddend int, index *LlvmValue, elemLLVM string) *LlvmValue {
	if emitter == nil || paramName == "" || index == nil || index.typ != "i64" || !listUsesRawDataFastPath(elemLLVM) {
		return nil
	}
	data := emitter.nativeListData[paramName]
	length := emitter.nativeListLens[paramName]
	list := llvmIdent(emitter, paramName)
	if data == nil || length == nil || list == nil || list.typ != "ptr" {
		return nil
	}
	// Proven-safe path: bounds analysis (see helpers above) has shown
	// that `paramName[idxName + idxAddend]` cannot go out of bounds
	// inside the enclosing loop. Emit a plain GEP+load — no
	// per-iteration check, no slow-path call. This is the shape
	// LLVM's LoopVectorizer requires; clang -O2 then collapses the
	// surrounding scalar loop into vector ops on simd-friendly
	// bodies.
	if llvmNativeIsSafeListAccess(emitter, paramName, idxName, idxAddend) {
		elemPtr := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = getelementptr inbounds %s, ptr %s, i64 %s", elemPtr, elemLLVM, data.name, index.name))
		fastValue := llvmNextTemp(emitter)
		loadLine := fmt.Sprintf("  %s = load %s, ptr %s", fastValue, elemLLVM, elemPtr)
		if emitter.parallelAccessHint && emitter.parallelAccessGroupRef != "" {
			loadLine += fmt.Sprintf(", !llvm.access.group %s", emitter.parallelAccessGroupRef)
		}
		emitter.body = append(emitter.body, loadLine)
		return &LlvmValue{typ: elemLLVM, name: fastValue, pointer: false}
	}
	nonNegative := llvmCompare(emitter, "sge", index, llvmIntLiteral(0))
	beforeEnd := llvmCompare(emitter, "slt", index, length)
	inBounds := llvmLogicalI1(emitter, llvmLogicalInstruction("&&"), nonNegative, beforeEnd)
	fastLabel := llvmNextLabel(emitter, "list.raw.fast")
	slowLabel := llvmNextLabel(emitter, "list.raw.slow")
	endLabel := llvmNextLabel(emitter, "list.raw.end")
	emitter.body = append(emitter.body, fmt.Sprintf("  br i1 %s, label %%%s, label %%%s", inBounds.name, fastLabel, slowLabel))
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", fastLabel))
	elemPtr := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = getelementptr inbounds %s, ptr %s, i64 %s", elemPtr, elemLLVM, data.name, index.name))
	fastValue := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = load %s, ptr %s", fastValue, elemLLVM, elemPtr))
	emitter.body = append(emitter.body, fmt.Sprintf("  br label %%%s", endLabel))
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", slowLabel))
	slowValue := llvmListGet(emitter, list, index, elemLLVM)
	emitter.body = append(emitter.body, fmt.Sprintf("  br label %%%s", endLabel))
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", endLabel))
	phi := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = phi %s [ %s, %%%s ], [ %s, %%%s ]", phi, elemLLVM, fastValue, fastLabel, slowValue.name, slowLabel))
	return &LlvmValue{typ: elemLLVM, name: phi, pointer: false}
}

func llvmNativeEmitFunction(fn *llvmNativeFunction, globals []*llvmNativeGlobal, startStringID int, startLoopMD int) llvmNativeRenderedFunction {
	emitter := llvmEmitter()
	emitter.stringId = startStringID
	emitter.nextLoopMD = startLoopMD
	emitter.vectorizeHint = fn != nil && fn.vectorize
	llvmNativeBindGlobals(emitter, globals)
	eligibleScalarListParams := llvmNativeEligibleScalarListParams(fn)
	if emitter.vectorizeHint && len(eligibleScalarListParams) > 0 {
		emitter.parallelAccessHint = true
		emitter.parallelAccessGroupRef = llvmNextAccessGroupRef(emitter)
	}
	params := make([]*LlvmParam, 0, len(fn.params))
	for _, param := range fn.params {
		paramIRType := param.llvmType
		if param.irType != "" {
			paramIRType = param.irType
		}
		params = append(params, llvmParam(param.name, paramIRType))
		if param.byRef {
			llvmBind(emitter, param.name, &LlvmValue{
				typ:     param.llvmType,
				name:    "%" + param.name,
				pointer: true,
			})
		} else {
			llvmMutableLetSlot(emitter, param.name, &LlvmValue{
				typ:     param.llvmType,
				name:    "%" + param.name,
				pointer: false,
			})
		}
		if elemLLVM := eligibleScalarListParams[param.name]; elemLLVM != "" {
			llvmNativePrimeScalarListParamFastPath(emitter, param.name, elemLLVM)
		}
	}
	block := llvmNativeEmitBlock(emitter, fn.body)
	if !llvmNativeBodyHasTerminator(emitter.body) {
		switch {
		case fn.returnType == "i32" && fn.name == "main":
			llvmReturnI32Zero(emitter)
		case block.hasValue && fn.returnType != "" && fn.returnType != "void":
			llvmReturn(emitter, block.value)
		case fn.returnType == "" || fn.returnType == "void":
			emitter.body = append(emitter.body, "  ret void")
		default:
			llvmReturn(emitter, llvmNativeZeroValue(fn.returnType))
		}
	}
	retType := fn.returnType
	if retType == "" {
		retType = "void"
	}
	return llvmNativeRenderedFunction{
		definition:    llvmRenderFunctionWithAttrs(retType, fn.name, params, llvmNativeFnAttrString(fn), emitter.body),
		stringGlobals: append([]*LlvmStringGlobal(nil), emitter.stringGlobals...),
		nextStringID:  emitter.stringId,
		loopMDDefs:    append([]string(nil), emitter.loopMDDefs...),
		nextLoopMD:    emitter.nextLoopMD,
	}
}

// llvmNativeInlineAttrKeyword mirrors toolchain/llvmgen.osty's
// `llvmNativeInlineAttrKeyword`. Maps the v0.6 A8 `#[inline]` family
// discriminant to the matching LLVM fn-attribute keyword; unknown
// modes fall through to the empty string so the renderer produces the
// attribute-free `define ... (...) {` shape.
func llvmNativeInlineAttrKeyword(mode int) string {
	switch mode {
	case 1:
		return "inlinehint"
	case 2:
		return "alwaysinline"
	case 3:
		return "noinline"
	default:
		return ""
	}
}

// llvmNativeFnAttrString mirrors toolchain/llvmgen.osty's
// `llvmNativeFnAttrString`. Assembles the fn-attribute string spliced
// between `)` and `{` on a `llvmNativeFunction`'s `define` line.
func llvmNativeFnAttrString(fn *llvmNativeFunction) string {
	if fn == nil {
		return ""
	}
	return llvmNativeInlineAttrKeyword(fn.inlineMode)
}

// llvmRenderFunctionWithAttrs mirrors toolchain/llvmgen.osty's
// `llvmRenderFunctionWithAttrs`. Renders a function `define` line
// with an optional fn-attribute string spliced between the parameter
// list's closing paren and the opening brace. `fnAttrs == ""` produces
// output byte-identical to `llvmRenderFunction`.
func llvmRenderFunctionWithAttrs(ret string, name string, params []*LlvmParam, fnAttrs string, body []string) string {
	var header string
	if fnAttrs == "" {
		header = fmt.Sprintf("define %s @%s(%s) {", ret, name, llvmParams(params))
	} else {
		header = fmt.Sprintf("define %s @%s(%s) %s {", ret, name, llvmParams(params), fnAttrs)
	}
	lines := []string{header, "entry:"}
	lines = append(lines, body...)
	lines = append(lines, "}")
	return strings.Join(lines, "\n") + "\n"
}

func llvmNativeBindGlobals(emitter *LlvmEmitter, globals []*llvmNativeGlobal) {
	for _, global := range globals {
		if global == nil {
			continue
		}
		llvmBind(emitter, global.name, &LlvmValue{
			typ:     global.llvmType,
			name:    global.irName,
			pointer: true,
		})
	}
}

func llvmNativeEmitBlock(emitter *LlvmEmitter, block *llvmNativeBlock) llvmNativeMaybeValue {
	if block == nil {
		return llvmNativeMaybeValue{}
	}
	for _, stmt := range block.stmts {
		llvmNativeEmitStmt(emitter, stmt)
	}
	if block.hasResult && block.result != nil {
		return llvmNativeMaybeValue{hasValue: true, value: llvmNativeEvalExpr(emitter, block.result)}
	}
	return llvmNativeMaybeValue{}
}

func llvmNativeEmitStmt(emitter *LlvmEmitter, stmt *llvmNativeStmt) {
	if stmt == nil {
		return
	}
	switch stmt.kind {
	case llvmNativeStmtExpr:
		if len(stmt.childExprs) > 0 {
			_ = llvmNativeEvalExpr(emitter, stmt.childExprs[0])
		}
	case llvmNativeStmtLet:
		if len(stmt.childExprs) > 0 {
			llvmNativeRecordBoundedLen(emitter, stmt.name, stmt.childExprs[0])
			llvmImmutableLet(emitter, stmt.name, llvmNativeEvalExpr(emitter, stmt.childExprs[0]))
		}
	case llvmNativeStmtMutLet:
		if len(stmt.childExprs) > 0 {
			llvmNativeRecordBoundedLen(emitter, stmt.name, stmt.childExprs[0])
			llvmMutableLet(emitter, stmt.name, llvmNativeEvalExpr(emitter, stmt.childExprs[0]))
		}
	case llvmNativeStmtAssign:
		if len(stmt.childExprs) > 0 {
			// An assignment to a while-loop loopvar invalidates the
			// bounds-analysis safety for the rest of the body. See
			// llvmNativeStmtWhile — the condition guarantees
			// `loopvar < bound` at body *entry*; once the body writes
			// to loopvar, the guarantee may no longer hold for
			// subsequent accesses in the same iteration. The next
			// iteration's condition re-evaluation re-publishes it.
			// Only clear when the destination is actually a current
			// safe-index name, so we don't touch unrelated assigns.
			if _, tracked := emitter.nativeSafeIndices[stmt.name]; tracked {
				delete(emitter.nativeSafeIndices, stmt.name)
			}
			_ = llvmAssign(emitter, stmt.name, llvmNativeAssignValue(emitter, stmt))
		}
	case llvmNativeStmtFieldAssign:
		llvmNativeEmitFieldAssign(emitter, stmt)
	case llvmNativeStmtReturn:
		if len(stmt.childExprs) > 0 {
			llvmReturn(emitter, llvmNativeEvalExpr(emitter, stmt.childExprs[0]))
		} else if stmt.llvmType == "i32" {
			llvmReturnI32Zero(emitter)
		} else {
			emitter.body = append(emitter.body, "  ret void")
		}
	case llvmNativeStmtIf:
		if len(stmt.childExprs) == 0 || len(stmt.childBlocks) == 0 {
			return
		}
		labels := llvmIfStart(emitter, llvmNativeEvalExpr(emitter, stmt.childExprs[0]))
		_ = llvmNativeEmitBlock(emitter, stmt.childBlocks[0])
		llvmIfElse(emitter, labels)
		if len(stmt.childBlocks) > 1 {
			_ = llvmNativeEmitBlock(emitter, stmt.childBlocks[1])
		}
		llvmIfEnd(emitter, labels)
	case llvmNativeStmtWhile:
		if len(stmt.childExprs) == 0 || len(stmt.childBlocks) == 0 {
			return
		}
		condLabel := llvmNextLabel(emitter, "for.cond")
		bodyLabel := llvmNextLabel(emitter, "for.body")
		endLabel := llvmNextLabel(emitter, "for.end")
		// Bounds-analysis for while loops: if the condition is shaped
		// `loopvar < <bounded-expr>` where the bounded expression's
		// value is `<= list.len() + k` for some list set, then at the
		// top of the body `loopvar` is guaranteed to fall in
		// `[0, list.len() - k)` for those lists (assuming non-negative
		// loopvar — common for counter-style loops, checked below by
		// requiring the condition's LHS to be an ident whose current
		// binding was already shown to be integer-typed).
		//
		// Safety holds only until the body modifies `loopvar` — see
		// the assign case below which clears the entry on any write
		// to a safe-index name. A subsequent `loopvar = loopvar + K`
		// invalidates for the rest of the body; the next iteration's
		// condition re-evaluation re-publishes it.
		var whileLoopVar string
		var whileSafeLists map[string]int
		// primedLenReg, when non-nil, is the pre-hoisted length
		// register for the condition's RHS — emitted once before
		// the cond label so subsequent iterations reuse it instead
		// of re-calling osty_rt_list_len each loop.
		var primedLenReg *LlvmValue
		if cmp := stmt.childExprs[0]; cmp != nil && cmp.kind == llvmNativeExprBinary && cmp.op == "<" && len(cmp.childExprs) == 2 {
			lhs := cmp.childExprs[0]
			rhs := cmp.childExprs[1]
			if lhs != nil && lhs.kind == llvmNativeExprIdent {
				if lists := llvmNativeBoundedLensFor(emitter, rhs); len(lists) > 0 {
					whileLoopVar = lhs.name
					whileSafeLists = lists
				}
				// Separate check: if the RHS is exactly
				// `list.len()` for an already-primed scalar list
				// parameter, the cached length is loop-invariant.
				// Reuse it verbatim instead of re-calling the
				// runtime helper every iteration. Without this,
				// LLVM can't hoist because osty_rt_list_len has
				// no memory-effect attribute.
				if list := llvmNativeListLenSource(rhs); list != "" {
					if length := emitter.nativeListLens[list]; length != nil {
						primedLenReg = length
					}
				}
			}
		}
		emitter.body = append(emitter.body, "  br label %"+condLabel)
		emitter.body = append(emitter.body, condLabel+":")
		var cond *LlvmValue
		if primedLenReg != nil {
			// Inline the comparison: i < primed_len. Saves a runtime
			// call per iteration.
			lhsVal := llvmNativeEvalExpr(emitter, stmt.childExprs[0].childExprs[0])
			cond = llvmCompare(emitter, "slt", lhsVal, primedLenReg)
		} else {
			cond = llvmNativeEvalExpr(emitter, stmt.childExprs[0])
		}
		emitter.body = append(emitter.body, "  br i1 "+cond.name+", label %"+bodyLabel+", label %"+endLabel)
		emitter.body = append(emitter.body, bodyLabel+":")
		llvmNativePushSafeIndices(emitter, whileLoopVar, whileSafeLists)
		_ = llvmNativeEmitBlock(emitter, stmt.childBlocks[0])
		llvmNativePopSafeIndices(emitter, whileLoopVar)
		emitter.body = append(emitter.body, "  br label %"+condLabel)
		emitter.body = append(emitter.body, endLabel+":")
	case llvmNativeStmtRange:
		if len(stmt.childExprs) < 2 || len(stmt.childBlocks) == 0 {
			return
		}
		start := llvmNativeEvalExpr(emitter, stmt.childExprs[0])
		end := llvmNativeEvalExpr(emitter, stmt.childExprs[1])
		// Bounds-analysis hook: when this loop's upper bound is
		// derivable from `list.len()` for one or more list params (and
		// the start is a non-negative literal), publish the (loopVar,
		// list) pairs as safe so the body's `list[loopVar]` accesses
		// can skip the runtime bounds-check branch. Inclusive (..=)
		// loops let `i == N`, so they are excluded — only `..` is
		// safe for indexing.
		var safeForLists map[string]int
		if !stmt.inclusive && llvmNativeRangeStartIsNonNegative(stmt.childExprs[0]) {
			safeForLists = llvmNativeBoundedLensFor(emitter, stmt.childExprs[1])
		}
		llvmNativePushSafeIndices(emitter, stmt.name, safeForLists)
		var loop *LlvmRangeLoop
		if stmt.inclusive {
			loop = llvmInclusiveRangeStart(emitter, stmt.name, start, end)
		} else {
			loop = llvmRangeStart(emitter, stmt.name, start, end, false)
		}
		_ = llvmNativeEmitBlock(emitter, stmt.childBlocks[0])
		llvmRangeEnd(emitter, loop)
		llvmNativePopSafeIndices(emitter, stmt.name)
	}
}

// ==== bounds-analysis helpers ====
//
// Goal: prove that a particular `list[i]` access inside a `for i in
// 0..N { ... }` loop body is in-bounds, so the per-iteration
// bounds-check + slow-path call in llvmNativeFastScalarListIndex can
// be skipped. The slow-path call is what blocks LLVM's LoopVectorizer
// ("loop not vectorized: call instruction cannot be vectorized").
//
// The analysis is intentionally narrow — it pattern-matches two
// shapes that cover the canonical numeric loops:
//
//   1. `for i in 0..list.len() { ... list[i] ... }`
//
//   2. `let n = if a.len() < b.len() { a.len() } else { b.len() }`
//      `for i in 0..n { ... a[i]; b[i] ... }`  (canonical
//      `min(a.len(), b.len())`).
//
// Anything more elaborate keeps the existing branched fast-path with
// the runtime fallback so correctness is preserved.

// llvmNativeRangeStartIsNonNegative reports whether the loop start
// expression is provably ≥ 0. Today we accept literal integers — the
// common `for i in 0..N` shape.
func llvmNativeRangeStartIsNonNegative(expr *llvmNativeExpr) bool {
	if expr == nil {
		return false
	}
	if expr.kind != llvmNativeExprInt {
		return false
	}
	return !strings.HasPrefix(strings.TrimSpace(expr.text), "-")
}

// llvmNativeBoundedLensFor returns, for each list-param name that
// the expression's value is provably `<=` `name + k` of, the maximum
// such `k`. nil means "no proven bound".
func llvmNativeBoundedLensFor(emitter *LlvmEmitter, expr *llvmNativeExpr) map[string]int {
	if expr == nil {
		return nil
	}
	if list := llvmNativeListLenSource(expr); list != "" {
		if _, ok := emitter.nativeListLens[list]; ok {
			return map[string]int{list: 0}
		}
		return nil
	}
	if expr.kind == llvmNativeExprIdent {
		if set := emitter.nativeBoundedLens[expr.name]; len(set) > 0 {
			return cloneOffsetMap(set)
		}
		return nil
	}
	// `bound - K` (K a non-negative int literal): if `name` is
	// `bounded by L with offset off` then `name - K` is bounded by
	// `L with offset off + K`. Subtraction by a non-negative
	// constant only ever makes the value smaller, never larger, so
	// the inequality continues to hold.
	if expr.kind == llvmNativeExprBinary && expr.op == "-" && len(expr.childExprs) == 2 {
		k, ok := llvmNativeIntLiteralValue(expr.childExprs[1])
		if !ok || k < 0 {
			return nil
		}
		base := llvmNativeBoundedLensFor(emitter, expr.childExprs[0])
		if len(base) == 0 {
			return nil
		}
		out := make(map[string]int, len(base))
		for l, off := range base {
			out[l] = off + k
		}
		return out
	}
	return nil
}

// llvmNativeListLenSource returns the list-param name when expr is
// `list.len()` for some param (i.e., a call to the runtime length
// helper whose receiver is a plain ident). Returns "" otherwise.
func llvmNativeListLenSource(expr *llvmNativeExpr) string {
	if expr == nil || expr.kind != llvmNativeExprCall {
		return ""
	}
	if expr.name != listRuntimeLenSymbol() {
		return ""
	}
	if len(expr.childExprs) != 1 {
		return ""
	}
	recv := expr.childExprs[0]
	if recv == nil || recv.kind != llvmNativeExprIdent {
		return ""
	}
	return recv.name
}

// llvmNativeRecordBoundedLen examines a `let name = expr` RHS for
// one of the supported boundedness shapes and updates
// emitter.nativeBoundedLens. Recognised shapes: direct
// `list.len()`, identifier copy, subtraction-by-constant, and the
// if-min diamond.
func llvmNativeRecordBoundedLen(emitter *LlvmEmitter, name string, expr *llvmNativeExpr) {
	if name == "" || expr == nil {
		return
	}
	// First try the recursive helper that already knows how to read
	// `list.len()`, ident copies, and `bound - K`. It returns a
	// fully resolved offset map when applicable.
	if bounded := llvmNativeBoundedLensFor(emitter, expr); len(bounded) > 0 {
		emitter.nativeBoundedLens[name] = bounded
		return
	}
	// `if cond { thenExpr } else { elseExpr }` where cond is an
	// inequality between two `len(L1)` / `len(L2)` calls and each
	// arm is one of those calls — the canonical min/max diamond.
	// The result is `<= min(L1.len, L2.len)` which is `<=` both,
	// with offset 0 for each.
	if expr.kind != llvmNativeExprIf {
		return
	}
	if len(expr.childExprs) < 1 || len(expr.childBlocks) < 2 {
		return
	}
	cond := expr.childExprs[0]
	thenLists := llvmNativeBlockResultLenSources(expr.childBlocks[0])
	elseLists := llvmNativeBlockResultLenSources(expr.childBlocks[1])
	if len(thenLists) == 0 || len(elseLists) == 0 {
		return
	}
	condLists := llvmNativeCondLenSources(cond)
	if len(condLists) < 2 {
		return
	}
	condSet := map[string]bool{}
	for _, l := range condLists {
		condSet[l] = true
	}
	allDrawn := func(arm []string) bool {
		for _, l := range arm {
			if !condSet[l] {
				return false
			}
		}
		return len(arm) > 0
	}
	if !allDrawn(thenLists) || !allDrawn(elseLists) {
		return
	}
	bounded := map[string]int{}
	for l := range condSet {
		bounded[l] = 0
	}
	emitter.nativeBoundedLens[name] = bounded
}

// llvmNativeBlockResultLenSources returns the list-param names whose
// `.len()` produces the block's tail expression. Used to recognise
// each arm of the if-min diamond.
func llvmNativeBlockResultLenSources(block *llvmNativeBlock) []string {
	if block == nil || !block.hasResult {
		return nil
	}
	if list := llvmNativeListLenSource(block.result); list != "" {
		return []string{list}
	}
	return nil
}

// llvmNativeCondLenSources extracts both list-param names from a
// branch condition shaped as `len(L1) <op> len(L2)` for `<op>` in
// {<, <=, >, >=}.
func llvmNativeCondLenSources(expr *llvmNativeExpr) []string {
	if expr == nil || expr.kind != llvmNativeExprBinary {
		return nil
	}
	switch expr.op {
	case "<", "<=", ">", ">=":
	default:
		return nil
	}
	if len(expr.childExprs) != 2 {
		return nil
	}
	left := llvmNativeListLenSource(expr.childExprs[0])
	right := llvmNativeListLenSource(expr.childExprs[1])
	if left == "" || right == "" {
		return nil
	}
	return []string{left, right}
}

// llvmNativePushSafeIndices marks (idxName, list) pairs as bounds-
// safe for the body emission that follows. The map's value is the
// maximum addend `k` for which `list[idxName + k]` is still proven
// in-bounds; values can be 0. Pass nil to mean "no safety
// established for this loop" — still call the matching pop so the
// symbol is restored to its prior state cleanly.
func llvmNativePushSafeIndices(emitter *LlvmEmitter, idxName string, lists map[string]int) {
	if idxName == "" {
		return
	}
	if len(lists) == 0 {
		// Still record an empty entry so pop doesn't restore stale
		// safety from an outer loop with the same loop-var name.
		emitter.nativeSafeIndices[idxName] = nil
		return
	}
	emitter.nativeSafeIndices[idxName] = cloneOffsetMap(lists)
}

// llvmNativePopSafeIndices clears the safe-index entry for the given
// loop variable. The native AST has no nested scoping for loop names
// in our current shape — peeling on body-end is sufficient.
func llvmNativePopSafeIndices(emitter *LlvmEmitter, idxName string) {
	if idxName == "" {
		return
	}
	delete(emitter.nativeSafeIndices, idxName)
}

// llvmNativeIsSafeListAccess reports whether `paramName[idxName +
// addend]` is proven in-bounds by the analysis above. idxName == ""
// means the caller couldn't pin the index back to a source-level
// identifier (e.g. an expression that isn't `loopvar` or
// `loopvar + constant`); those keep the runtime check.
func llvmNativeIsSafeListAccess(emitter *LlvmEmitter, paramName, idxName string, addend int) bool {
	if paramName == "" || idxName == "" || addend < 0 {
		return false
	}
	set := emitter.nativeSafeIndices[idxName]
	maxOff, ok := set[paramName]
	if !ok {
		return false
	}
	return addend <= maxOff
}

// llvmNativeIntLiteralValue extracts the int value from a literal
// integer expression. Used to recognise constant addends and
// constant subtrahends.
func llvmNativeIntLiteralValue(expr *llvmNativeExpr) (int, bool) {
	if expr == nil || expr.kind != llvmNativeExprInt {
		return 0, false
	}
	text := strings.ReplaceAll(strings.TrimSpace(expr.text), "_", "")
	if text == "" {
		return 0, false
	}
	// Handle a leading `+` for symmetry; reject `-` because callers
	// want non-negative addends/subtrahends.
	if text[0] == '+' {
		text = text[1:]
	}
	if text == "" || text[0] == '-' {
		return 0, false
	}
	var v int
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		v = v*10 + int(c-'0')
	}
	return v, true
}

// llvmNativeIndexIdentAndAddend deconstructs the index expression of
// `list[idx]` into the source-level loop variable (if any) plus a
// constant addend. Returns ("", 0, false) for shapes the analysis
// can't lift safely. Recognised shapes:
//
//   - `i`               → ("i", 0, true)
//   - `i + K`           → ("i", K, true) for K a non-negative int literal
//   - `K + i`           → ("i", K, true)
func llvmNativeIndexIdentAndAddend(expr *llvmNativeExpr) (string, int, bool) {
	if expr == nil {
		return "", 0, false
	}
	if expr.kind == llvmNativeExprIdent {
		return expr.name, 0, true
	}
	if expr.kind != llvmNativeExprBinary || expr.op != "+" || len(expr.childExprs) != 2 {
		return "", 0, false
	}
	left, right := expr.childExprs[0], expr.childExprs[1]
	if left != nil && left.kind == llvmNativeExprIdent {
		if k, ok := llvmNativeIntLiteralValue(right); ok {
			return left.name, k, true
		}
	}
	if right != nil && right.kind == llvmNativeExprIdent {
		if k, ok := llvmNativeIntLiteralValue(left); ok {
			return right.name, k, true
		}
	}
	return "", 0, false
}

func cloneOffsetMap(in map[string]int) map[string]int {
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func llvmNativeAssignValue(emitter *LlvmEmitter, stmt *llvmNativeStmt) *LlvmValue {
	value := llvmNativeEvalExpr(emitter, stmt.childExprs[0])
	if stmt.op == "" || stmt.op == "=" {
		return value
	}
	current := llvmIdent(emitter, stmt.name)
	return llvmNativeApplyBinary(emitter, stmt.op, current, value, current.typ)
}

func llvmNativeEmitFieldAssign(emitter *LlvmEmitter, stmt *llvmNativeStmt) {
	if len(stmt.childExprs) == 0 || len(stmt.fieldPath) == 0 {
		return
	}
	lookup := llvmLookup(emitter, stmt.name)
	if lookup == nil || !lookup.found || lookup.value == nil || !lookup.value.pointer {
		return
	}
	root := llvmLoad(emitter, lookup.value)
	levels := make([]*LlvmValue, len(stmt.fieldPath))
	levels[0] = root
	for i := 1; i < len(stmt.fieldPath); i++ {
		prev := stmt.fieldPath[i-1]
		levels[i] = llvmExtractValue(emitter, levels[i-1], prev.llvmType, prev.fieldIndex)
	}
	value := llvmNativeEvalExpr(emitter, stmt.childExprs[0])
	if stmt.op != "" && stmt.op != "=" {
		leaf := stmt.fieldPath[len(stmt.fieldPath)-1]
		current := llvmExtractValue(emitter, levels[len(levels)-1], leaf.llvmType, leaf.fieldIndex)
		value = llvmNativeApplyBinary(emitter, stmt.op, current, value, current.typ)
	}
	next := value
	for i := len(stmt.fieldPath) - 1; i >= 0; i-- {
		next = llvmInsertValue(emitter, levels[i], next, stmt.fieldPath[i].fieldIndex)
	}
	llvmStore(emitter, lookup.value, next)
}

func llvmNativeEvalExpr(emitter *LlvmEmitter, expr *llvmNativeExpr) *LlvmValue {
	if expr == nil {
		return llvmNativeZeroValue("i64")
	}
	switch expr.kind {
	case llvmNativeExprInt:
		return &LlvmValue{typ: expr.llvmType, name: expr.text}
	case llvmNativeExprFloat:
		return &LlvmValue{typ: expr.llvmType, name: expr.text}
	case llvmNativeExprBool:
		name := "false"
		if expr.boolValue {
			name = "true"
		}
		return &LlvmValue{typ: expr.llvmType, name: name}
	case llvmNativeExprString:
		return llvmStringLiteral(emitter, expr.text)
	case llvmNativeExprIdent:
		return llvmIdent(emitter, expr.name)
	case llvmNativeExprStructLit:
		fields := make([]*LlvmValue, 0, len(expr.childExprs))
		for _, child := range expr.childExprs {
			fields = append(fields, llvmNativeEvalExpr(emitter, child))
		}
		return llvmStructLiteral(emitter, expr.llvmType, fields)
	case llvmNativeExprListLit:
		return llvmNativeEvalListLit(emitter, expr)
	case llvmNativeExprMapLit:
		return llvmNativeEvalMapLit(emitter, expr)
	case llvmNativeExprField:
		if len(expr.childExprs) == 0 {
			return llvmNativeZeroValue(expr.llvmType)
		}
		base := llvmNativeEvalExpr(emitter, expr.childExprs[0])
		return llvmExtractValue(emitter, base, expr.llvmType, expr.fieldIndex)
	case llvmNativeExprListIndex:
		return llvmNativeEvalListIndex(emitter, expr)
	case llvmNativeExprMapIndex:
		return llvmNativeEvalMapIndex(emitter, expr)
	case llvmNativeExprUnary:
		return llvmNativeEvalUnary(emitter, expr)
	case llvmNativeExprBinary:
		return llvmNativeEvalBinary(emitter, expr)
	case llvmNativeExprCall:
		return llvmNativeEvalCall(emitter, expr)
	case llvmNativeExprPrintln:
		return llvmNativeEvalPrintln(emitter, expr)
	case llvmNativeExprIf:
		return llvmNativeEvalIfExpr(emitter, expr)
	case llvmNativeExprOptionCheck:
		return llvmNativeEvalOptionCheck(emitter, expr)
	case llvmNativeExprCoalesce:
		return llvmNativeEvalCoalesce(emitter, expr)
	case llvmNativeExprQuestion:
		return llvmNativeEvalQuestion(emitter, expr)
	case llvmNativeExprOptionalField:
		return llvmNativeEvalOptionalField(emitter, expr)
	case llvmNativeExprInterfaceBox:
		return llvmNativeEvalInterfaceBox(emitter, expr)
	case llvmNativeExprInterfaceCall:
		return llvmNativeEvalInterfaceCall(emitter, expr)
	case llvmNativeExprClosureEnvAlloc:
		return llvmNativeEvalClosureEnvAlloc(emitter, expr)
	case llvmNativeExprFnValueCall:
		return llvmNativeEvalFnValueCall(emitter, expr)
	default:
		return llvmNativeZeroValue(expr.llvmType)
	}
}

// llvmNativeEvalInterfaceBox emits the concrete-to-interface
// boxing sequence:
//
//	<slot> = alloca %<S>
//	store %<S> <concrete>, ptr <slot>
//	<t1> = insertvalue %osty.iface zeroinitializer, ptr <slot>, 0
//	<t2> = insertvalue %osty.iface <t1>, ptr <vtable_sym>, 1
//
// and returns a value pointing at %osty.iface. `expr.name` carries
// the vtable symbol like "@osty.vtable.Vec__Sized"; caller guarantees
// it's well-formed.
func llvmNativeEvalInterfaceBox(emitter *LlvmEmitter, expr *llvmNativeExpr) *LlvmValue {
	if emitter == nil || expr == nil || len(expr.childExprs) == 0 {
		return llvmNativeZeroValue("%osty.iface")
	}
	concrete := llvmNativeEvalExpr(emitter, expr.childExprs[0])
	slot := llvmSpillToSlot(emitter, concrete)
	step1 := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = insertvalue %%osty.iface zeroinitializer, ptr %s, 0", step1, slot.name))
	step2 := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = insertvalue %%osty.iface %s, ptr %s, 1", step2, step1, expr.name))
	return &LlvmValue{typ: "%osty.iface", name: step2}
}

// llvmNativeEvalInterfaceCall emits the indirect-dispatch sequence:
//
//	<data> = extractvalue %osty.iface <recv>, 0
//	<vt>   = extractvalue %osty.iface <recv>, 1
//	<slot> = getelementptr ptr, ptr <vt>, i64 <fieldIndex>
//	<fn>   = load ptr, ptr <slot>
//	<res>  = call <ret> <fn>(ptr <data>, <arg0_type> <arg0>, ...)
//
// Returns the call result value (zero-valued `llvmType` if the
// method returns void). `expr.text` carries the comma-separated
// LLVM types of the non-self args, parsed on the emit side so the
// call instruction matches the shim ABI exactly.
func llvmNativeEvalInterfaceCall(emitter *LlvmEmitter, expr *llvmNativeExpr) *LlvmValue {
	if emitter == nil || expr == nil || len(expr.childExprs) == 0 {
		return llvmNativeZeroValue(expr.llvmType)
	}
	recv := llvmNativeEvalExpr(emitter, expr.childExprs[0])
	data := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = extractvalue %%osty.iface %s, 0", data, recv.name))
	vt := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = extractvalue %%osty.iface %s, 1", vt, recv.name))
	slotAddr := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = getelementptr ptr, ptr %s, i64 %d", slotAddr, vt, expr.fieldIndex))
	fnPtr := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = load ptr, ptr %s", fnPtr, slotAddr))

	// Build the arg list: (ptr <data>, <type> <arg>, ...)
	var argList strings.Builder
	argList.WriteString("ptr ")
	argList.WriteString(data)
	argTypes := splitArgTypes(expr.text)
	for i := 1; i < len(expr.childExprs); i++ {
		argVal := llvmNativeEvalExpr(emitter, expr.childExprs[i])
		argList.WriteString(", ")
		if i-1 < len(argTypes) && argTypes[i-1] != "" {
			argList.WriteString(argTypes[i-1])
		} else {
			argList.WriteString(argVal.typ)
		}
		argList.WriteString(" ")
		argList.WriteString(argVal.name)
	}

	ret := expr.llvmType
	if ret == "" {
		ret = "void"
	}
	if ret == "void" {
		emitter.body = append(emitter.body, fmt.Sprintf("  call void %s(%s)", fnPtr, argList.String()))
		return llvmNativeZeroValue("void")
	}
	res := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = call %s %s(%s)", res, ret, fnPtr, argList.String()))
	return &LlvmValue{typ: ret, name: res}
}

// llvmNativeEvalClosureEnvAlloc emits the env-alloc + store-thunk
// sequence used by a closure literal, plus (for capturing closures)
// per-capture stores into env slots at offset
// `closureEnvCapturesOffset + i*8`.
//
//	%site = <ptr to string literal>
//	%env  = call ptr @osty.rt.closure_env_alloc_v2(i64 <N>, ptr %site, i64 <bitmap>)
//	store ptr <thunkSym>, ptr %env
//	; for each capture i:
//	%cap<i>_slot = getelementptr i8, ptr %env, i64 <closureEnvCapturesOffset + i*8>
//	store <capType> <capVal>, ptr %cap<i>_slot
//
// `expr.text` has shape "<site>;<capType0>,<capType1>,..." — the
// site label up to the first semicolon, then comma-separated
// capture LLVM types (empty for no-capture). The capture values
// are `expr.childExprs` in declaration order. Bit i of the pointer
// bitmap is set iff `capTypes[i] == "ptr"` (RUNTIME_GC §2.4).
func llvmNativeEvalClosureEnvAlloc(emitter *LlvmEmitter, expr *llvmNativeExpr) *LlvmValue {
	if emitter == nil || expr == nil {
		return llvmNativeZeroValue("ptr")
	}
	siteLabel, capTypesRaw := expr.text, ""
	if idx := strings.Index(expr.text, ";"); idx >= 0 {
		siteLabel = expr.text[:idx]
		capTypesRaw = expr.text[idx+1:]
	}
	capTypes := splitArgTypes(capTypesRaw)
	var bitmap uint64
	for i, t := range capTypes {
		if t == "ptr" {
			bitmap |= uint64(1) << uint(i)
		}
	}
	site := llvmStringLiteral(emitter, siteLabel)
	env := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = call ptr @osty.rt.closure_env_alloc_v2(i64 %d, ptr %s, i64 %d)", env, len(capTypes), site.name, bitmap))
	emitter.body = append(emitter.body, fmt.Sprintf("  store ptr %s, ptr %s", expr.name, env))
	// Store each capture value into its env slot at offset
	// `closureEnvCapturesOffset + i*8`.
	for i, child := range expr.childExprs {
		val := llvmNativeEvalExpr(emitter, child)
		typ := "i64"
		if i < len(capTypes) && capTypes[i] != "" {
			typ = capTypes[i]
		}
		slot := fmt.Sprintf("%%cap%d_slot", i)
		// Use raw-named slot (matches legacy shape so tests can lock it).
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = getelementptr i8, ptr %s, i64 %d", slot, env, llvmClosureEnvCapturesOffset()+i*8))
		emitter.body = append(emitter.body, fmt.Sprintf("  store %s %s, ptr %s", typ, val.name, slot))
	}
	return &LlvmValue{typ: "ptr", name: env}
}

// llvmNativeEvalFnValueCall dispatches through a fn-value env. The
// trampoline fn ptr lives at env slot 0; loading it and calling
// with (env, args...) mirrors the legacy HIR emit shape:
//
//	%fn  = load ptr, ptr %env
//	%ret = call <ret> %fn(ptr %env, <typed args...>)
//
// Arg LLVM types come from `expr.text` (comma-separated). Return
// type is `expr.llvmType`; "void" suppresses the `%res =` lhs.
func llvmNativeEvalFnValueCall(emitter *LlvmEmitter, expr *llvmNativeExpr) *LlvmValue {
	if emitter == nil || expr == nil || len(expr.childExprs) == 0 {
		return llvmNativeZeroValue(expr.llvmType)
	}
	env := llvmNativeEvalExpr(emitter, expr.childExprs[0])
	fnPtr := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = load ptr, ptr %s", fnPtr, env.name))

	var argList strings.Builder
	argList.WriteString("ptr ")
	argList.WriteString(env.name)
	argTypes := splitArgTypes(expr.text)
	for i := 1; i < len(expr.childExprs); i++ {
		arg := llvmNativeEvalExpr(emitter, expr.childExprs[i])
		argList.WriteString(", ")
		if i-1 < len(argTypes) && argTypes[i-1] != "" {
			argList.WriteString(argTypes[i-1])
		} else {
			argList.WriteString(arg.typ)
		}
		argList.WriteString(" ")
		argList.WriteString(arg.name)
	}

	ret := expr.llvmType
	if ret == "" {
		ret = "void"
	}
	if ret == "void" {
		emitter.body = append(emitter.body, fmt.Sprintf("  call void %s(%s)", fnPtr, argList.String()))
		return llvmNativeZeroValue("void")
	}
	res := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = call %s %s(%s)", res, ret, fnPtr, argList.String()))
	return &LlvmValue{typ: ret, name: res}
}

// splitArgTypes parses a comma-separated list of LLVM types out of
// the `text` field. Empty input yields a nil slice. No input
// sanitization — callers control this field.
func splitArgTypes(text string) []string {
	if text == "" {
		return nil
	}
	parts := strings.Split(text, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func llvmNativeEvalListLit(emitter *LlvmEmitter, expr *llvmNativeExpr) *LlvmValue {
	list := llvmListNew(emitter)
	for _, child := range expr.childExprs {
		value := llvmNativeEvalExpr(emitter, child)
		if llvmListUsesTypedRuntime(expr.elemLLVMType) {
			llvmListPush(emitter, list, value)
			continue
		}
		slot := llvmSpillToSlot(emitter, value)
		size := llvmSizeOf(emitter, expr.elemLLVMType)
		llvmCallVoid(emitter, listRuntimePushBytesV1Symbol(), []*LlvmValue{list, slot, size})
	}
	return list
}

func llvmNativeEvalMapLit(emitter *LlvmEmitter, expr *llvmNativeExpr) *LlvmValue {
	// `osty_rt_map_new` aborts on `value_size <= 0`; calling it with
	// zero args leaves the arg registers holding garbage, so we must
	// pass the correct (key_kind, value_kind, value_size, trace)
	// tuple — matching the legacy AST emitter's `emitMapNewFor`.
	m := llvmMapNewTyped(emitter, expr.mapKeyLLVMType, expr.mapKeyIsString, expr.mapValueLLVMType, expr.mapValueIsString)
	for i := 0; i+1 < len(expr.childExprs); i += 2 {
		key := llvmNativeEvalExpr(emitter, expr.childExprs[i])
		value := llvmNativeEvalExpr(emitter, expr.childExprs[i+1])
		slot := llvmSpillToSlot(emitter, value)
		llvmMapInsert(emitter, m, key, slot, expr.mapKeyIsString)
	}
	return m
}

// llvmMapNewTyped emits a 4-arg `osty_rt_map_new(key_kind, value_kind,
// value_size, trace)` call with the actual K/V metadata derived from
// the map literal's MIR types. Value-trace is deliberately `null` —
// values are currently written through the spill-to-slot path which
// either (1) is a scalar and doesn't need GC tracing or (2) is a
// managed ptr and the slot itself is rooted elsewhere. A follow-up
// PR will wire proper slot-trace callbacks for the managed-value case.
func llvmMapNewTyped(emitter *LlvmEmitter, keyTyp string, keyIsString bool, valueTyp string, valueIsString bool) *LlvmValue {
	keyKind := llvmContainerAbiKind(keyTyp, keyIsString)
	valueKind := llvmContainerAbiKind(valueTyp, valueIsString)
	valueSize := mapValueSizeBytes(valueTyp)
	if valueSize <= 0 {
		// Fallback: GEP-based size for aggregate / unknown-width slots.
		// Matches emitTypeSize in generator.go.
		valueSize = 8
	}
	return llvmCall(emitter, "ptr", "osty_rt_map_new", []*LlvmValue{
		llvmIntLiteral(keyKind),
		llvmIntLiteral(valueKind),
		llvmIntLiteral(valueSize),
		{typ: "ptr", name: "null"},
	})
}

func llvmNativeEvalListIndex(emitter *LlvmEmitter, expr *llvmNativeExpr) *LlvmValue {
	if len(expr.childExprs) < 2 {
		return llvmNativeZeroValue(expr.llvmType)
	}
	if base := expr.childExprs[0]; base != nil && base.kind == llvmNativeExprIdent && llvmListUsesTypedRuntime(expr.elemLLVMType) {
		idxExpr := expr.childExprs[1]
		// Decompose the index into `loopvar [+ constant]` so the
		// fast-path can consult llvmNativeIsSafeListAccess(base,
		// loopvar, addend) — covers `list[i]`, `list[i + 1]`,
		// `list[i + 7]`, ... when the analysis has shown the loop's
		// upper bound leaves room for that addend.
		idxName, idxAddend, _ := llvmNativeIndexIdentAndAddend(idxExpr)
		index := llvmNativeEvalExpr(emitter, idxExpr)
		if fast := llvmNativeFastScalarListIndex(emitter, base.name, idxName, idxAddend, index, expr.elemLLVMType); fast != nil {
			return fast
		}
	}
	list := llvmNativeEvalExpr(emitter, expr.childExprs[0])
	index := llvmNativeEvalExpr(emitter, expr.childExprs[1])
	if llvmListUsesTypedRuntime(expr.elemLLVMType) {
		return llvmListGet(emitter, list, index, expr.elemLLVMType)
	}
	slot := llvmAllocaSlot(emitter, expr.elemLLVMType)
	size := llvmSizeOf(emitter, expr.elemLLVMType)
	llvmCallVoid(emitter, listRuntimeGetBytesV1Symbol(), []*LlvmValue{list, index, slot, size})
	return llvmLoadFromSlot(emitter, slot, expr.elemLLVMType)
}

func llvmNativeEvalMapIndex(emitter *LlvmEmitter, expr *llvmNativeExpr) *LlvmValue {
	if len(expr.childExprs) < 2 {
		return llvmNativeZeroValue(expr.llvmType)
	}
	m := llvmNativeEvalExpr(emitter, expr.childExprs[0])
	key := llvmNativeEvalExpr(emitter, expr.childExprs[1])
	slot := llvmAllocaSlot(emitter, expr.llvmType)
	llvmMapGetOrAbort(emitter, m, key, slot, expr.mapKeyIsString)
	return llvmLoadFromSlot(emitter, slot, expr.llvmType)
}

func llvmNativeEvalUnary(emitter *LlvmEmitter, expr *llvmNativeExpr) *LlvmValue {
	if len(expr.childExprs) == 0 {
		return llvmNativeZeroValue(expr.llvmType)
	}
	value := llvmNativeEvalExpr(emitter, expr.childExprs[0])
	switch expr.op {
	case "+":
		return value
	case "!":
		return llvmNotI1(emitter, value)
	case "~":
		return llvmBinaryI64(emitter, "xor", value, llvmIntLiteral(-1))
	default:
		if expr.llvmType == "double" {
			return llvmBinaryF64(emitter, llvmFloatBinaryInstruction("-"), llvmNativeZeroValue("double"), value)
		}
		return llvmBinaryI64(emitter, llvmIntBinaryInstruction("-"), llvmNativeZeroValue(expr.llvmType), value)
	}
}

func llvmNativeEvalBinary(emitter *LlvmEmitter, expr *llvmNativeExpr) *LlvmValue {
	if len(expr.childExprs) < 2 {
		return llvmNativeZeroValue(expr.llvmType)
	}
	left := llvmNativeEvalExpr(emitter, expr.childExprs[0])
	right := llvmNativeEvalExpr(emitter, expr.childExprs[1])
	return llvmNativeApplyBinary(emitter, expr.op, left, right, expr.llvmType)
}

func llvmNativeApplyBinary(emitter *LlvmEmitter, op string, left, right *LlvmValue, resultType string) *LlvmValue {
	switch op {
	case "&&", "||":
		return llvmLogicalI1(emitter, llvmLogicalInstruction(op), left, right)
	}
	switch resultType {
	case "i1":
		if left.typ == "double" || right.typ == "double" {
			return llvmCompareF64(emitter, llvmFloatComparePredicate(op), left, right)
		}
		return llvmCompare(emitter, llvmIntComparePredicate(op), left, right)
	case "double":
		return llvmBinaryF64(emitter, llvmFloatBinaryInstruction(op), left, right)
	default:
		return llvmBinaryI64(emitter, llvmIntBinaryInstruction(op), left, right)
	}
}

func llvmNativeEvalCall(emitter *LlvmEmitter, expr *llvmNativeExpr) *LlvmValue {
	args := make([]*LlvmValue, 0, len(expr.childExprs))
	argStart := 0
	var restoreProjectedReceiver func()
	if expr.firstArgByRef {
		if len(expr.childExprs) == 0 || expr.childExprs[0] == nil || expr.childExprs[0].kind != llvmNativeExprIdent {
			return llvmNativeZeroValue(expr.llvmType)
		}
		lookup := llvmLookup(emitter, expr.childExprs[0].name)
		if lookup == nil || !lookup.found || lookup.value == nil || !lookup.value.pointer {
			return llvmNativeZeroValue(expr.llvmType)
		}
		if len(expr.receiverPath) == 0 {
			args = append(args, &LlvmValue{typ: "ptr", name: lookup.value.name, pointer: false})
		} else {
			root := llvmLoad(emitter, lookup.value)
			aggregates := make([]*LlvmValue, 0, len(expr.receiverPath))
			current := root
			for _, step := range expr.receiverPath {
				aggregates = append(aggregates, current)
				current = llvmExtractValue(emitter, current, step.llvmType, step.fieldIndex)
			}
			recvSlot := llvmSpillToSlot(emitter, current)
			args = append(args, recvSlot)
			restoreProjectedReceiver = func() {
				next := llvmLoadFromSlot(emitter, recvSlot, current.typ)
				for i := len(expr.receiverPath) - 1; i >= 0; i-- {
					next = llvmInsertValue(emitter, aggregates[i], next, expr.receiverPath[i].fieldIndex)
				}
				llvmStore(emitter, lookup.value, next)
			}
		}
		argStart = 1
	}
	for i := argStart; i < len(expr.childExprs); i++ {
		value := llvmNativeEvalExpr(emitter, expr.childExprs[i])
		if llvmNativeCallArgShouldSpill(expr, i) {
			value = llvmSpillToSlot(emitter, value)
		}
		args = append(args, value)
	}
	var out *LlvmValue
	if expr.llvmType == "" || expr.llvmType == "void" {
		llvmCallVoid(emitter, expr.name, args)
		out = llvmNativeZeroValue("i64")
	} else {
		out = llvmCall(emitter, expr.llvmType, expr.name, args)
	}
	if restoreProjectedReceiver != nil {
		restoreProjectedReceiver()
	}
	return out
}

func llvmNativeCallArgShouldSpill(expr *llvmNativeExpr, idx int) bool {
	if expr == nil {
		return false
	}
	for _, target := range expr.spillArgIndices {
		if target == idx {
			return true
		}
	}
	return false
}

func llvmNativeEvalPrintln(emitter *LlvmEmitter, expr *llvmNativeExpr) *LlvmValue {
	if len(expr.childExprs) == 0 {
		return llvmNativeZeroValue("i64")
	}
	value := llvmNativeEvalExpr(emitter, expr.childExprs[0])
	switch value.typ {
	case "double":
		llvmPrintlnF64(emitter, value)
	case "ptr":
		llvmPrintlnString(emitter, value)
	case "i1":
		llvmPrintlnBool(emitter, value)
	default:
		llvmPrintlnI64(emitter, value)
	}
	return llvmNativeZeroValue("i64")
}

func llvmNativeEvalIfExpr(emitter *LlvmEmitter, expr *llvmNativeExpr) *LlvmValue {
	if len(expr.childExprs) == 0 || len(expr.childBlocks) < 2 {
		return llvmNativeZeroValue(expr.llvmType)
	}
	labels := llvmIfExprStart(emitter, llvmNativeEvalExpr(emitter, expr.childExprs[0]))
	thenValue := llvmNativeBlockValue(emitter, expr.childBlocks[0], expr.llvmType)
	llvmIfExprElse(emitter, labels)
	elseValue := llvmNativeBlockValue(emitter, expr.childBlocks[1], expr.llvmType)
	return llvmIfExprEnd(emitter, expr.llvmType, thenValue, elseValue, labels)
}

func llvmNativeEvalOptionCheck(emitter *LlvmEmitter, expr *llvmNativeExpr) *LlvmValue {
	if len(expr.childExprs) == 0 {
		return llvmNativeZeroValue("i1")
	}
	base := llvmNativeEvalExpr(emitter, expr.childExprs[0])
	pred := "eq"
	if expr.boolValue {
		pred = "ne"
	}
	return llvmCompare(emitter, pred, base, llvmNativeZeroValue("ptr"))
}

func llvmNativeEvalCoalesce(emitter *LlvmEmitter, expr *llvmNativeExpr) *LlvmValue {
	if len(expr.childExprs) < 2 {
		return llvmNativeZeroValue(expr.llvmType)
	}
	left := llvmNativeEvalExpr(emitter, expr.childExprs[0])
	isNil := llvmCompare(emitter, "eq", left, llvmNativeZeroValue("ptr"))
	someLabel := llvmNextLabel(emitter, "coalesce.some")
	noneLabel := llvmNextLabel(emitter, "coalesce.none")
	endLabel := llvmNextLabel(emitter, "coalesce.end")
	emitter.body = append(emitter.body, fmt.Sprintf("  br i1 %s, label %%%s, label %%%s", isNil.name, noneLabel, someLabel))
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", someLabel))
	innerType := firstNonEmpty(expr.optionInnerLLVMType, expr.llvmType)
	someValue := left
	if innerType != "ptr" {
		someValue = llvmLoadFromSlot(emitter, left, innerType)
	}
	emitter.body = append(emitter.body, fmt.Sprintf("  br label %%%s", endLabel))
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", noneLabel))
	noneValue := llvmNativeEvalExpr(emitter, expr.childExprs[1])
	emitter.body = append(emitter.body, fmt.Sprintf("  br label %%%s", endLabel))
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", endLabel))
	tmp := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = phi %s [ %s, %%%s ], [ %s, %%%s ]", tmp, expr.llvmType, someValue.name, someLabel, noneValue.name, noneLabel))
	return &LlvmValue{typ: expr.llvmType, name: tmp}
}

func llvmNativeEvalQuestion(emitter *LlvmEmitter, expr *llvmNativeExpr) *LlvmValue {
	if len(expr.childExprs) == 0 {
		return llvmNativeZeroValue(expr.llvmType)
	}
	base := llvmNativeEvalExpr(emitter, expr.childExprs[0])
	isNil := llvmCompare(emitter, "eq", base, llvmNativeZeroValue("ptr"))
	nilLabel := llvmNextLabel(emitter, "optional.return")
	contLabel := llvmNextLabel(emitter, "optional.cont")
	emitter.body = append(emitter.body, fmt.Sprintf("  br i1 %s, label %%%s, label %%%s", isNil.name, nilLabel, contLabel))
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", nilLabel))
	emitter.body = append(emitter.body, "  ret ptr null")
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", contLabel))
	innerType := firstNonEmpty(expr.optionInnerLLVMType, expr.llvmType)
	if innerType == "ptr" {
		return base
	}
	return llvmLoadFromSlot(emitter, base, innerType)
}

func llvmNativeEvalOptionalField(emitter *LlvmEmitter, expr *llvmNativeExpr) *LlvmValue {
	if len(expr.childExprs) == 0 || expr.baseLLVMType == "" {
		return llvmNativeZeroValue(expr.llvmType)
	}
	base := llvmNativeEvalExpr(emitter, expr.childExprs[0])
	isNil := llvmCompare(emitter, "eq", base, llvmNativeZeroValue("ptr"))
	someLabel := llvmNextLabel(emitter, "optional.field.some")
	noneLabel := llvmNextLabel(emitter, "optional.field.none")
	endLabel := llvmNextLabel(emitter, "optional.field.end")
	emitter.body = append(emitter.body, fmt.Sprintf("  br i1 %s, label %%%s, label %%%s", isNil.name, noneLabel, someLabel))
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", someLabel))
	fieldType := firstNonEmpty(expr.optionInnerLLVMType, expr.llvmType)
	loadedBase := llvmLoadFromSlot(emitter, base, expr.baseLLVMType)
	someValue := llvmExtractValue(emitter, loadedBase, fieldType, expr.fieldIndex)
	emitter.body = append(emitter.body, fmt.Sprintf("  br label %%%s", endLabel))
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", noneLabel))
	emitter.body = append(emitter.body, fmt.Sprintf("  br label %%%s", endLabel))
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", endLabel))
	tmp := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = phi ptr [ %s, %%%s ], [ null, %%%s ]", tmp, someValue.name, someLabel, noneLabel))
	return &LlvmValue{typ: "ptr", name: tmp}
}

func llvmNativeBlockValue(emitter *LlvmEmitter, block *llvmNativeBlock, llvmType string) *LlvmValue {
	value := llvmNativeEmitBlock(emitter, block)
	if value.hasValue {
		return value.value
	}
	return llvmNativeZeroValue(llvmType)
}

func llvmNativeZeroValue(llvmType string) *LlvmValue {
	switch llvmType {
	case "", "void":
		return llvmI64("0")
	default:
		return &LlvmValue{typ: llvmType, name: llvmZeroLiteral(llvmType)}
	}
}

func llvmNativeBodyHasTerminator(body []string) bool {
	if len(body) == 0 {
		return false
	}
	last := body[len(body)-1]
	return strings.HasPrefix(last, "  ret ") || strings.HasPrefix(last, "  br ")
}
