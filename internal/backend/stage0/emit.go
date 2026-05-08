package stage0

import (
	"errors"
	"fmt"
	"strings"

	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/llvmabi"
	"github.com/osty/osty/internal/mir"
)

// ErrUnsupported marks a MIR shape outside the stage0 bootstrap subset.
// Callers (the backend dispatcher) treat this as "stage0 declines"
// rather than a hard build failure — the dispatcher then surfaces the
// usual unsupported skeleton diagnostic with route `stage0`.
var ErrUnsupported = errors.New("stage0: MIR shape outside bootstrap subset")

// EmitMIR lowers `module` to textual LLVM IR for the small MVP subset
// stage0 currently covers. It is invoked only when
// `backend.IsOstySelfMissing` reported the canonical decline; production
// builds with a working `osty-self` route through the LIR Proto
// subprocess instead and never reach this entry.
//
// Currently supported function shapes — the matcher tries each in
// order and emits the first that fits; otherwise ErrUnsupported:
//
//   - `fn main() {}` — single block, zero instructions, ReturnTerm.
//
//   - Sequential single-block return: zero or more AssignInstrs (each
//     writing to a unique scalar local) followed by ReturnTerm.
//     Up to two Int / Bool parameters. Each AssignInstr's source is
//     either:
//
//     UseRV { ConstOp(IntConst | BoolConst) }
//     UseRV { CopyOp(local) }            // local must already be defined
//     BinaryRV { op, operand, operand }  // op family classified below
//
//     where each operand is a const literal or a CopyOp of a
//     previously-defined scalar local (param or earlier assignment).
//     Operator families:
//
//     arithmetic Int×Int → Int : Add Sub Mul Div Mod
//     comparison Int×Int → Bool: Eq Neq Lt Leq Gt Geq
//     bitwise    Int×Int → Int : BitAnd BitOr BitXor Shl Shr
//     logical    Bool×Bool→ Bool: And Or
//
// Anything else (multi-block / calls / projections / non-Int-Bool
// types / local reassignment / forward references) declines so the
// next stage0 phase can pick the case up. Surface drift is held back
// by the stage0 coverage gate planned for P3.
func EmitMIR(module *mir.Module, opts llvmabi.Options) ([]byte, error) {
	if module == nil {
		return nil, fmt.Errorf("stage0: nil MIR module")
	}
	var out strings.Builder
	pkg := packageNameFor(module, opts)
	target := llvmabi.CanonicalLLVMTarget(opts.Target)

	out.WriteString("; Osty stage0 bootstrap LLVM IR\n")
	out.WriteString("; package: " + pkg + "\n")
	if target != "" {
		out.WriteString(fmt.Sprintf("target triple = %q\n", target))
	}
	out.WriteString("\n")

	// Build the per-module emit context — known function symbols,
	// the lazy string-constant pool, and a buffer that collects any
	// module-level declarations the matchers need to emit (printf
	// declare, format strings, string literal globals, …).
	mctx := newModuleCtx(module)

	// Pre-scan for printf-using intrinsics so the printf declare +
	// format strings land in extraDecls before any function body.
	needs := scanPrintlnNeeds(module)
	if needs.int {
		mctx.extraDecls.WriteString("@.fmt.stage0.println.int = private unnamed_addr constant [6 x i8] c\"%lld\\0A\\00\"\n")
	}
	if needs.str {
		mctx.extraDecls.WriteString("@.fmt.stage0.println.str = private unnamed_addr constant [4 x i8] c\"%s\\0A\\00\"\n")
	}
	if needs.int || needs.str {
		mctx.extraDecls.WriteString("declare i32 @printf(ptr, ...)\n")
	}

	var fnBodies strings.Builder
	emittedMain := false
	for _, fn := range module.Functions {
		if fn == nil {
			continue
		}
		if err := emitFunction(&fnBodies, fn, mctx); err != nil {
			return nil, err
		}
		if fn.Name == "main" {
			emittedMain = true
		}
	}
	if !emittedMain {
		return nil, fmt.Errorf("%w: module has no `main` function", ErrUnsupported)
	}

	// Module-level declarations come before function bodies. LLVM
	// accepts either order but top-level decls first reads more
	// naturally and matches what mem2reg / sccp etc. expect.
	if mctx.extraDecls.Len() > 0 {
		out.WriteString(mctx.extraDecls.String())
		out.WriteString("\n")
	}
	out.WriteString(fnBodies.String())
	return []byte(out.String()), nil
}

// moduleCtx threads per-EmitMIR module-level state through every
// matcher and emit helper. It replaces the bare `knownSymbols` map
// the older signatures used so future stage0 features (string pool,
// struct layouts, runtime declarations …) can attach without forcing
// another sweep through all matcher signatures.
type moduleCtx struct {
	module       *mir.Module
	knownSymbols map[string]bool
	// extraDecls collects module-level lines (declares + globals +
	// struct type defs) the matchers emit on demand. Concatenated
	// into the final output before function bodies.
	extraDecls *strings.Builder
	// stringPool maps string-constant value → assigned global
	// symbol (without the leading `@`). Lookups are write-once: the
	// first reference allocates a fresh `@.str.<N>` global.
	stringPool   map[string]string
	nextStringID int
	// emittedStructs records which struct names have been written
	// to extraDecls already, so multiple functions referencing the
	// same struct don't duplicate the type definition.
	emittedStructs map[string]bool
	// tuplePool maps a tuple's canonical key (the comma-separated
	// list of LLVM scalar mnemonics) to its synthetic LLVM type
	// name. Tuples don't carry a user-visible Osty name so stage0
	// invents `.tuple.<N>` IDs on first use.
	tuplePool   map[string]string
	nextTupleID int
	nextTempID  int
}

func newModuleCtx(module *mir.Module) *moduleCtx {
	syms := map[string]bool{}
	for _, fn := range module.Functions {
		if fn == nil || fn.Name == "" {
			continue
		}
		syms[fn.Name] = true
	}
	return &moduleCtx{
		module:         module,
		knownSymbols:   syms,
		extraDecls:     &strings.Builder{},
		stringPool:     map[string]string{},
		emittedStructs: map[string]bool{},
		tuplePool:      map[string]string{},
	}
}

// internTupleType returns the synthetic LLVM type name (with leading
// `%`) for the tuple `(elems...)`. Repeated calls with the same field
// list return the cached name. The type definition is emitted into
// extraDecls on first use.
func (m *moduleCtx) internTupleType(elems []scalarType) string {
	key := tupleKey(elems)
	if name, ok := m.tuplePool[key]; ok {
		return name
	}
	name := fmt.Sprintf(".tuple.%d", m.nextTupleID)
	m.nextTupleID++
	m.tuplePool[key] = name
	fmt.Fprintf(m.extraDecls, "%%%s = type { ", name)
	for i, ft := range elems {
		if i > 0 {
			m.extraDecls.WriteString(", ")
		}
		m.extraDecls.WriteString(ft.llvm())
	}
	m.extraDecls.WriteString(" }\n")
	return name
}

func tupleKey(elems []scalarType) string {
	var b strings.Builder
	for i, e := range elems {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(e.llvm())
	}
	return b.String()
}

// lookupStructFields returns the scalar field types for the named
// struct or (nil, false) if the struct isn't in the module's layout
// table or any field is non-scalar (Int / Bool / String only).
// Stage0's struct support is intentionally narrow — anything outside
// the scalar-only subset declines so future phases can decide how to
// handle aggregates inside structs.
func (m *moduleCtx) lookupStructFields(name string) ([]scalarType, bool) {
	if m == nil || m.module == nil || m.module.Layouts == nil {
		return nil, false
	}
	layout, ok := m.module.Layouts.Structs[name]
	if !ok || layout == nil {
		return nil, false
	}
	tys := make([]scalarType, len(layout.Fields))
	for i, f := range layout.Fields {
		st := m.scalarFromType(f.Type, true)
		if st == scalarUnknown {
			return nil, false
		}
		tys[i] = st
	}
	return tys, true
}

// emitStructDef ensures `%<name> = type { ... }` lands in
// extraDecls exactly once. Subsequent calls with the same name are
// no-ops.
func (m *moduleCtx) emitStructDef(name string, fields []scalarType) {
	if m.emittedStructs[name] {
		return
	}
	m.emittedStructs[name] = true
	fmt.Fprintf(m.extraDecls, "%%%s = type { ", name)
	for i, ft := range fields {
		if i > 0 {
			m.extraDecls.WriteString(", ")
		}
		m.extraDecls.WriteString(ft.llvm())
	}
	m.extraDecls.WriteString(" }\n")
}

func (m *moduleCtx) freshTempName(label string) string {
	if m == nil {
		return "%stage0.tmp"
	}
	name := fmt.Sprintf("%%stage0.%s.%d", sanitizeLLVMName(label, "tmp"), m.nextTempID)
	m.nextTempID++
	return name
}

// internStringConst interns `value` and returns the LLVM operand
// expression for it (e.g., `@.str.0`). The byte sequence is emitted
// into `mctx.extraDecls` as a private unnamed_addr constant
// terminated with `\00` so the compiled program can pass it to
// `printf`-style C ABIs without further copying.
func (m *moduleCtx) internStringConst(value string) string {
	if sym, ok := m.stringPool[value]; ok {
		return "@" + sym
	}
	sym := fmt.Sprintf(".str.%d", m.nextStringID)
	m.nextStringID++
	m.stringPool[value] = sym
	fmt.Fprintf(m.extraDecls, "@%s = private unnamed_addr constant [%d x i8] c\"%s\\00\"\n", sym, len(value)+1, escapeForLLVMConst(value))
	return "@" + sym
}

// escapeForLLVMConst escapes one byte sequence for inclusion inside
// an LLVM `c"..."` constant literal.
func escapeForLLVMConst(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		switch {
		case c == '\\':
			b.WriteString(`\5C`)
		case c == '"':
			b.WriteString(`\22`)
		case c >= 0x20 && c < 0x7F:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, `\%02X`, c)
		}
	}
	return b.String()
}

// scanPrintlnNeeds walks every IntrinsicPrintln in the module and
// reports which scalar argument types are used. The result drives
// the per-format-string decisions in EmitMIR — Int prints go through
// the `%lld\n` format, String prints through `%s\n`.
type printlnNeeds struct {
	int bool
	str bool
}

func scanPrintlnNeeds(module *mir.Module) printlnNeeds {
	needs := printlnNeeds{}
	for _, fn := range module.Functions {
		if fn == nil {
			continue
		}
		for _, bb := range fn.Blocks {
			if bb == nil {
				continue
			}
			for _, instr := range bb.Instrs {
				intr, ok := instr.(*mir.IntrinsicInstr)
				if !ok || intr.Kind != mir.IntrinsicPrintln {
					continue
				}
				if len(intr.Args) != 1 {
					continue
				}
				switch scalarFromType(intr.Args[0].Type()) {
				case scalarInt:
					needs.int = true
				case scalarString:
					needs.str = true
				}
			}
		}
	}
	return needs
}

func emitFunction(out *strings.Builder, fn *mir.Function, mctx *moduleCtx) error {
	if fn.IsIntrinsic {
		return fmt.Errorf("%w: intrinsic declaration %q", ErrUnsupported, fn.Name)
	}
	if fn.IsExternal {
		return fmt.Errorf("%w: external declaration %q", ErrUnsupported, fn.Name)
	}
	if fn.Name == "main" {
		return emitTrivialMain(out, fn, mctx)
	}
	if pat, ok := matchSequentialVoid(fn, mctx); ok {
		return emitSequentialVoid(out, fn, pat)
	}
	if pat, ok := matchSequentialReturn(fn, mctx); ok {
		return emitSequentialReturn(out, fn, pat)
	}
	if pat, ok := matchIfElseReturn(fn, mctx); ok {
		return emitIfElseReturn(out, fn, pat)
	}
	if pat, ok := matchIfElseAggregateReturn(fn, mctx); ok {
		return emitIfElseAggregateReturn(out, fn, pat, mctx)
	}
	if pat, ok := matchOrShortCircuitIfElseAggregate(fn, mctx); ok {
		return emitOrShortCircuitIfElseAggregate(out, fn, pat, mctx)
	}
	if pat, ok := matchElseIfChainAggregate(fn, mctx); ok {
		return emitElseIfChainAggregate(out, fn, pat, mctx)
	}
	if pat, ok := matchOrChainAggregate(fn, mctx); ok {
		return emitOrChainAggregate(out, fn, pat, mctx)
	}
	if pat, ok := matchWhileLoopReturn(fn, mctx); ok {
		return emitWhileLoopReturn(out, fn, pat)
	}
	if pat, ok := matchForInRangeReturn(fn, mctx); ok {
		return emitForInRangeReturn(out, fn, pat)
	}
	if pat, ok := matchStructFieldRead(fn, mctx); ok {
		return emitStructFieldRead(out, fn, pat)
	}
	if pat, ok := matchStructFieldBinaryOp(fn, mctx); ok {
		return emitStructFieldBinaryOp(out, fn, pat)
	}
	if pat, ok := matchStructFieldListLen(fn, mctx); ok {
		return emitStructFieldListLen(out, fn, pat)
	}
	if pat, ok := matchListLiteralLen(fn, mctx); ok {
		return emitListLiteralLen(out, fn, pat)
	}
	if pat, ok := matchAggregateConstructor(fn, mctx); ok {
		return emitAggregateConstructor(out, fn, pat)
	}
	if pat, ok := matchListLiteralIndexGet(fn, mctx); ok {
		return emitListLiteralIndexGet(out, fn, pat)
	}
	return fmt.Errorf("%w: function %q does not match any stage0 pattern", ErrUnsupported, fn.Name)
}

// ---- P1: trivial main ----

// emitTrivialMain handles `fn main() {}` and small extensions thereof.
// The function header is always `define i32 @main()` with `ret i32 0`;
// the body can contain:
//
//   - Storage liveness markers (skipped).
//   - An optional final AssignInstr writing UnitConst to the return
//     local (skipped — main always returns the C ABI's `i32 0`).
//   - Zero or more `IntrinsicInstr` whose Dest is nil — currently
//     limited to `IntrinsicPrintln` via classifyIntrinsicLine.
//
// Anything else inside main declines so a future stage0 phase can
// pick the case up.
func emitTrivialMain(out *strings.Builder, fn *mir.Function, mctx *moduleCtx) error {
	if reason := trivialMainShapeViolation(fn); reason != "" {
		return fmt.Errorf("%w: function %q: %s", ErrUnsupported, fn.Name, reason)
	}
	bb := fn.Blocks[0]
	var bodyBuf strings.Builder
	for _, instr := range bb.Instrs {
		switch step := instr.(type) {
		case *mir.StorageLiveInstr, *mir.StorageDeadInstr:
			continue
		case *mir.AssignInstr:
			if isUnitAssignToReturnLocal(step, fn.ReturnLocal) {
				continue
			}
			return fmt.Errorf("%w: main: AssignInstr writing %T to local#%d not supported", ErrUnsupported, step.Src, step.Dest.Local)
		case *mir.IntrinsicInstr:
			line, ok := classifyIntrinsicLine(fn, step, nil, mctx)
			if !ok {
				return fmt.Errorf("%w: main: intrinsic %v with %d args not supported", ErrUnsupported, step.Kind, len(step.Args))
			}
			bodyBuf.WriteString(line)
		default:
			return fmt.Errorf("%w: main: instruction %T not supported", ErrUnsupported, instr)
		}
	}
	out.WriteString("define i32 @main() {\n")
	out.WriteString("entry:\n")
	out.WriteString(bodyBuf.String())
	out.WriteString("  ret i32 0\n")
	out.WriteString("}\n\n")
	return nil
}

// trivialMainShapeViolation enforces the structural envelope for
// stage0 main — single block, no parameters, ReturnTerm. Body
// instruction validation lives in emitTrivialMain itself.
func trivialMainShapeViolation(fn *mir.Function) string {
	if fn.Name != "main" {
		return "stage0 expected `main`; saw " + fn.Name
	}
	if len(fn.Params) != 0 {
		return "main has parameters"
	}
	if len(fn.Blocks) != 1 {
		return fmt.Sprintf("main has %d blocks; stage0 expects exactly 1", len(fn.Blocks))
	}
	bb := fn.Blocks[0]
	if bb == nil {
		return "main entry block is nil"
	}
	if _, ok := bb.Term.(*mir.ReturnTerm); !ok {
		return fmt.Sprintf("main terminator is %T; stage0 expects ReturnTerm", bb.Term)
	}
	return ""
}

// isUnitAssignToReturnLocal reports whether `instr` is the canonical
// `ret = ()` AssignInstr the front-end emits for empty-body main.
func isUnitAssignToReturnLocal(instr mir.Instr, ret mir.LocalID) bool {
	ai, ok := instr.(*mir.AssignInstr)
	if !ok {
		return false
	}
	if ai.Dest.Local != ret || ai.Dest.HasProjections() {
		return false
	}
	use, ok := ai.Src.(*mir.UseRV)
	if !ok {
		return false
	}
	con, ok := use.Op.(*mir.ConstOp)
	if !ok {
		return false
	}
	_, ok = con.Const.(*mir.UnitConst)
	return ok
}

// ---- sequential single-block return ----
//
// `seqState` tracks per-local LLVM expressions as the matcher / emitter
// walks the block instruction by instruction. Constants and copy-only
// assignments are inlined (the matcher records the const literal /
// param register / source local's expression directly), while
// BinaryRV destinations bind a fresh `%N` SSA register reserved at
// emit time.

type scalarType int

const (
	scalarUnknown scalarType = iota
	scalarInt
	scalarBool
	scalarString
	scalarOpaquePtr
)

func (s scalarType) llvm() string {
	switch s {
	case scalarInt:
		return "i64"
	case scalarBool:
		return "i1"
	case scalarString:
		// Osty Strings reach the C ABI as null-terminated UTF-8 byte
		// sequences. LLVM 15+ uses opaque pointers for that role.
		return "ptr"
	case scalarOpaquePtr:
		return "ptr"
	}
	return ""
}

func scalarFromType(t mir.Type) scalarType {
	return scalarFromTypeInternal(t, false)
}

func scalarFromParamType(t mir.Type) scalarType {
	return scalarFromTypeInternal(t, true)
}

func scalarFromTypeInternal(t mir.Type, allowUserNamed bool) scalarType {
	prim, ok := t.(*ir.PrimType)
	if ok && prim != nil {
		switch prim.Kind {
		case ir.PrimString:
			return scalarString
		case ir.PrimInt:
			return scalarInt
		case ir.PrimBool:
			return scalarBool
		case ir.PrimBytes, ir.PrimRawPtr:
			return scalarOpaquePtr
		}
		return scalarUnknown
	}
	if named, ok := t.(*ir.NamedType); ok && named != nil {
		switch {
		case named.Builtin && isOpaqueNamedType(named.Name):
			return scalarOpaquePtr
		case allowUserNamed:
			return scalarOpaquePtr
		}
	}
	return scalarUnknown
}

func isOpaqueNamedType(name string) bool {
	switch name {
	case "List", "Map", "Set", "Option", "Result", "Channel", "Handle", "TaskGroup":
		return true
	}
	return false
}

func (m *moduleCtx) scalarFromType(t mir.Type, allowUserNamed bool) scalarType {
	if named, ok := t.(*ir.NamedType); ok && named != nil && m != nil && m.module != nil && m.module.Layouts != nil {
		if layout, ok := m.module.Layouts.Enums[named.Name]; ok && enumLayoutIsPayloadless(layout) {
			return scalarInt
		}
	}
	return scalarFromTypeInternal(t, allowUserNamed)
}

func enumLayoutIsPayloadless(layout *mir.EnumLayout) bool {
	if layout == nil {
		return false
	}
	for _, variant := range layout.Variants {
		if len(variant.Payload) != 0 {
			return false
		}
	}
	return true
}

// localBinding records, for one MIR LocalID, how to materialise the
// local in subsequent LLVM operands. Either an inlined immediate
// expression (constant literal, param register, or another local's
// already-resolved expression) or a fresh SSA register pending
// emission.
type localBinding struct {
	expr    string // LLVM operand expression (`42`, `true`, `%x`, `%3`) OR alloca slot (`%acc.slot`) when isStack
	ty      scalarType
	defined bool
	// isStack reports whether the local lives in an alloca slot (P6
	// while-loop mutable locals). Reads of stack-backed locals must
	// be lowered to a `load` instruction; writes emit `store`.
	isStack bool
}

// pendingInstr is one instruction the emitter will materialise.
// For `inline` instructions (UseRV) nothing is emitted — the matcher
// already recorded the expression in seqState.bindings. For `binary`
// and `call` instructions the emitter assigns the next SSA register
// at emit time.
type pendingInstr struct {
	kind       instrKind
	destLocal  mir.LocalID
	resultType scalarType
	// SSA register pre-assigned at match time. Always set for
	// instrBinary / instrCall; empty for instrInline / instrIntrinsic.
	binDestReg string
	// binary fields
	binOp      string
	binArgType string
	leftExpr   string
	rightExpr  string
	// call fields
	callSymbol string
	callArgs   []callArg
	// call-chain fields — currently used for N-ary String concat by
	// lowering it as left-associated calls to osty_rt_strings_Concat.
	chainRegs []string
	// list-literal fields
	listPushSymbol string
	// intrinsic fields — pre-rendered LLVM line that the emitter
	// writes verbatim. Used for IntrinsicPrintln (and future kinds).
	intrinsicLine string
}

type callArg struct {
	expr string
	ty   string // LLVM type ("i64" / "i1")
}

type instrKind int

const (
	instrInline instrKind = iota
	instrBinary
	instrCall
	instrCallChain
	instrListLiteral
	instrIntrinsic
)

// classifyIntrinsicLine pre-renders the LLVM line for an
// IntrinsicInstr that doesn't bind a local (e.g., println). Returns
// (line, true) on success; declines for unsupported intrinsics or
// shape mismatches. Operand resolution uses the SSA-only `bindings`
// path — stack-backed locals are not visible here (they live in the
// while-loop matcher).
func classifyIntrinsicLine(fn *mir.Function, ii *mir.IntrinsicInstr, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (string, bool) {
	if ii.Dest != nil {
		return "", false
	}
	switch ii.Kind {
	case mir.IntrinsicPrintln:
		if len(ii.Args) != 1 {
			return "", false
		}
		expr, ty, ok := resolveOperand(ii.Args[0], bindings, mctx)
		if !ok {
			return "", false
		}
		switch ty {
		case scalarInt:
			return fmt.Sprintf("  call i32 (ptr, ...) @printf(ptr @.fmt.stage0.println.int, i64 %s)\n", expr), true
		case scalarString:
			return fmt.Sprintf("  call i32 (ptr, ...) @printf(ptr @.fmt.stage0.println.str, ptr %s)\n", expr), true
		}
		return "", false
	case mir.IntrinsicListPush:
		if len(ii.Args) != 2 {
			return "", false
		}
		listPrelude, listExpr, ok := resolveListReceiverOperand(fn, ii.Args[0], bindings, mctx)
		if !ok {
			return "", false
		}
		elemExpr, elemTy, ok := resolveOperand(ii.Args[1], bindings, mctx)
		if !ok {
			return "", false
		}
		pushSymbol := listPushSymbolFor(elemTy)
		if pushSymbol == "" {
			return "", false
		}
		declareListRuntime(mctx)
		return fmt.Sprintf("%s  call void @%s(ptr %s, %s %s)\n", listPrelude, pushSymbol, listExpr, elemTy.llvm(), elemExpr), true
	case mir.IntrinsicListReverse:
		if len(ii.Args) != 1 {
			return "", false
		}
		expr, ty, ok := resolveOperand(ii.Args[0], bindings, mctx)
		if !ok || ty != scalarOpaquePtr {
			return "", false
		}
		declareVoidFunctionPrototype(mctx, "osty_rt_list_reverse", []callArg{{ty: "ptr"}})
		return fmt.Sprintf("  call void @osty_rt_list_reverse(ptr %s)\n", expr), true
	case mir.IntrinsicSetInsert, mir.IntrinsicSetRemove:
		if len(ii.Args) != 2 {
			return "", false
		}
		setExpr, setTy, ok := resolveOperand(ii.Args[0], bindings, mctx)
		if !ok || setTy != scalarOpaquePtr {
			return "", false
		}
		elemExpr, elemTy, ok := resolveOperand(ii.Args[1], bindings, mctx)
		if !ok {
			return "", false
		}
		symbol := setMutationSymbolFor(ii.Kind, elemTy)
		if symbol == "" {
			return "", false
		}
		args := []callArg{{ty: "ptr"}, {ty: elemTy.llvm()}}
		declareRuntimePrototype(mctx, symbol, scalarBool, args)
		return fmt.Sprintf("  call i1 @%s(ptr %s, %s %s)\n", symbol, setExpr, elemTy.llvm(), elemExpr), true
	case mir.IntrinsicYield:
		if len(ii.Args) != 0 {
			return "", false
		}
		declareVoidFunctionPrototype(mctx, "osty_rt_yield", nil)
		return "  call void @osty_rt_yield()\n", true
	case mir.IntrinsicSleep:
		if len(ii.Args) != 1 {
			return "", false
		}
		expr, ty, ok := resolveOperand(ii.Args[0], bindings, mctx)
		if !ok || ty != scalarInt {
			return "", false
		}
		declareVoidFunctionPrototype(mctx, "osty_rt_sleep", []callArg{{ty: "i64"}})
		return fmt.Sprintf("  call void @osty_rt_sleep(i64 %s)\n", expr), true
	case mir.IntrinsicCheckCancelled:
		if len(ii.Args) != 0 {
			return "", false
		}
		declareVoidFunctionPrototype(mctx, "osty_rt_check_cancelled", nil)
		return "  call void @osty_rt_check_cancelled()\n", true
	}
	return "", false
}

// sequentialPattern is what `matchSequentialReturn` produces.
type sequentialPattern struct {
	retType    scalarType
	paramIDs   []mir.LocalID
	paramTypes []scalarType
	paramNames []string // sanitised, position-disambiguated SSA names
	pending    []pendingInstr
	returnExpr string // LLVM expression for the return local at end of block
}

// matchSequentialReturn classifies `fn` against the multi-instruction
// stage0 subset described in the package docstring. `knownSymbols`
// names every function defined in the same MIR module so direct call
// classifiers can decline references to external symbols (which
// stage0 cannot declare).
func matchSequentialReturn(fn *mir.Function, mctx *moduleCtx) (sequentialPattern, bool) {
	pat := sequentialPattern{}
	pat.retType = mctx.scalarFromType(fn.ReturnType, true)
	if pat.retType == scalarUnknown {
		return pat, false
	}
	// P17: lift the historical 2-param ceiling. Toolchain audit shows
	// 4–8 param scalar/String fns dominate the "no matching pattern"
	// bucket; the limit here was a P3a artifact (only `a`/`b` fallback
	// names existed) and not load-bearing for the rest of the matcher.
	if len(fn.Params) > 8 {
		return pat, false
	}

	bindings := map[mir.LocalID]localBinding{}

	// Seed param bindings so subsequent CopyOp resolution works.
	pat.paramIDs = fn.Params
	pat.paramTypes = make([]scalarType, len(fn.Params))
	pat.paramNames = make([]string, len(fn.Params))
	fallbackNames := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	for i, pid := range fn.Params {
		loc := lookupLocal(fn, pid)
		if loc == nil || !loc.IsParam {
			return pat, false
		}
		pt := mctx.scalarFromType(loc.Type, true)
		if pt == scalarUnknown {
			return pat, false
		}
		pat.paramTypes[i] = pt
		pat.paramNames[i] = sanitizeLLVMName(loc.Name, fallbackNames[i])
	}
	disambiguateParamNames(pat.paramNames)
	for i, pid := range fn.Params {
		bindings[pid] = localBinding{
			expr:    "%" + pat.paramNames[i],
			ty:      pat.paramTypes[i],
			defined: true,
		}
	}

	bb, ok := singleBlockReturning(fn)
	if !ok {
		return pat, false
	}

	// Reserve fresh SSA register numbers as we encounter BinaryRVs.
	// Inline instructions don't claim a register, so we count
	// only `instrBinary` entries.
	nextSSA := 0

	for _, instr := range bb.Instrs {
		var (
			pending  pendingInstr
			expr     string
			destID   mir.LocalID
			destType scalarType
			okStep   bool
		)
		switch step := instr.(type) {
		case *mir.AssignInstr:
			pending, expr, destID, destType, okStep = classifyAssignStep(fn, step, bindings, mctx)
		case *mir.CallInstr:
			pending, destID, destType, okStep = classifyCallStep(fn, step, bindings, mctx)
		case *mir.IntrinsicInstr:
			if step.Dest == nil {
				line, okIntr := classifyIntrinsicLine(fn, step, bindings, mctx)
				if !okIntr {
					return pat, false
				}
				pat.pending = append(pat.pending, pendingInstr{kind: instrIntrinsic, intrinsicLine: line})
				continue
			}
			pending, destID, destType, okStep = classifyIntrinsicValueStep(fn, step, bindings, mctx)
		case *mir.StorageLiveInstr, *mir.StorageDeadInstr:
			// Storage liveness markers carry no LLVM-visible semantics
			// for stage0. Skip them and move to the next instruction.
			continue
		default:
			return pat, false
		}
		if !okStep {
			return pat, false
		}
		// Reassignment is unsupported — both for params and for
		// previously-bound locals.
		if existing, found := bindings[destID]; found && existing.defined {
			return pat, false
		}
		pending.destLocal = destID
		pending.resultType = destType

		switch pending.kind {
		case instrBinary, instrCall, instrListLiteral:
			reg := fmt.Sprintf("%%%d", nextSSA)
			nextSSA++
			pending.binDestReg = reg
			bindings[destID] = localBinding{expr: reg, ty: destType, defined: true}
		case instrCallChain:
			regs := make([]string, len(pending.callArgs)-1)
			for i := range regs {
				regs[i] = fmt.Sprintf("%%%d", nextSSA)
				nextSSA++
			}
			pending.chainRegs = regs
			bindings[destID] = localBinding{expr: regs[len(regs)-1], ty: destType, defined: true}
		default:
			bindings[destID] = localBinding{expr: expr, ty: destType, defined: true}
		}
		pat.pending = append(pat.pending, pending)
	}

	retBinding, ok := bindings[fn.ReturnLocal]
	if !ok || !retBinding.defined {
		return pat, false
	}
	if retBinding.ty != pat.retType {
		return pat, false
	}
	pat.returnExpr = retBinding.expr
	return pat, true
}

type voidPattern struct {
	paramIDs   []mir.LocalID
	paramTypes []scalarType
	paramNames []string
	pending    []pendingInstr
}

func matchSequentialVoid(fn *mir.Function, mctx *moduleCtx) (voidPattern, bool) {
	pat := voidPattern{}
	if !isUnitType(fn.ReturnType) {
		return pat, false
	}
	if len(fn.Params) > 8 {
		return pat, false
	}

	bindings := map[mir.LocalID]localBinding{}
	pat.paramIDs = fn.Params
	pat.paramTypes = make([]scalarType, len(fn.Params))
	pat.paramNames = make([]string, len(fn.Params))
	fallbackNames := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	for i, pid := range fn.Params {
		loc := lookupLocal(fn, pid)
		if loc == nil || !loc.IsParam {
			return pat, false
		}
		pt := mctx.scalarFromType(loc.Type, true)
		if pt == scalarUnknown {
			return pat, false
		}
		pat.paramTypes[i] = pt
		pat.paramNames[i] = sanitizeLLVMName(loc.Name, fallbackNames[i])
	}
	disambiguateParamNames(pat.paramNames)
	for i, pid := range fn.Params {
		bindings[pid] = localBinding{
			expr:    "%" + pat.paramNames[i],
			ty:      pat.paramTypes[i],
			defined: true,
		}
	}

	bb, ok := singleBlockReturning(fn)
	if !ok {
		return pat, false
	}
	nextSSA := 0
	for _, instr := range bb.Instrs {
		var (
			pending  pendingInstr
			expr     string
			destID   mir.LocalID
			destType scalarType
			okStep   bool
		)
		switch step := instr.(type) {
		case *mir.AssignInstr:
			if isUnitAssignToReturnLocal(step, fn.ReturnLocal) {
				continue
			}
			pending, expr, destID, destType, okStep = classifyAssignStep(fn, step, bindings, mctx)
		case *mir.CallInstr:
			if step.Dest == nil {
				line, okCall := classifyVoidCallLine(step, bindings, mctx)
				if !okCall {
					return pat, false
				}
				pat.pending = append(pat.pending, pendingInstr{kind: instrIntrinsic, intrinsicLine: line})
				continue
			}
			pending, destID, destType, okStep = classifyCallStep(fn, step, bindings, mctx)
		case *mir.IntrinsicInstr:
			if step.Dest == nil {
				line, okIntr := classifyIntrinsicLine(fn, step, bindings, mctx)
				if !okIntr {
					return pat, false
				}
				pat.pending = append(pat.pending, pendingInstr{kind: instrIntrinsic, intrinsicLine: line})
				continue
			}
			pending, destID, destType, okStep = classifyIntrinsicValueStep(fn, step, bindings, mctx)
		case *mir.StorageLiveInstr, *mir.StorageDeadInstr:
			continue
		default:
			return pat, false
		}
		if !okStep {
			return pat, false
		}
		if existing, found := bindings[destID]; found && existing.defined {
			return pat, false
		}
		pending.destLocal = destID
		pending.resultType = destType

		switch pending.kind {
		case instrBinary, instrCall, instrListLiteral:
			reg := fmt.Sprintf("%%%d", nextSSA)
			nextSSA++
			pending.binDestReg = reg
			bindings[destID] = localBinding{expr: reg, ty: destType, defined: true}
		case instrCallChain:
			regs := make([]string, len(pending.callArgs)-1)
			for i := range regs {
				regs[i] = fmt.Sprintf("%%%d", nextSSA)
				nextSSA++
			}
			pending.chainRegs = regs
			bindings[destID] = localBinding{expr: regs[len(regs)-1], ty: destType, defined: true}
		default:
			bindings[destID] = localBinding{expr: expr, ty: destType, defined: true}
		}
		pat.pending = append(pat.pending, pending)
	}
	return pat, true
}

func isUnitType(t mir.Type) bool {
	return isPrimType(t, ir.PrimUnit)
}

func emitSequentialVoid(out *strings.Builder, fn *mir.Function, pat voidPattern) error {
	fmt.Fprintf(out, "define void @%s(", fn.Name)
	for i, name := range pat.paramNames {
		if i > 0 {
			out.WriteString(", ")
		}
		fmt.Fprintf(out, "%s %%%s", pat.paramTypes[i].llvm(), name)
	}
	out.WriteString(") {\n")
	out.WriteString("entry:\n")
	for _, pi := range pat.pending {
		emitPendingInstr(out, pi)
	}
	out.WriteString("  ret void\n")
	out.WriteString("}\n\n")
	return nil
}

// classifyAssignStep adapts AssignInstr to the shared pending-step
// signature used by the matcher's per-instruction loop. Returns
// (pending, inline-expr, destID, destType, ok).
func classifyAssignStep(fn *mir.Function, ai *mir.AssignInstr, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (pendingInstr, string, mir.LocalID, scalarType, bool) {
	if ai.Dest.HasProjections() {
		return pendingInstr{}, "", 0, scalarUnknown, false
	}
	destID := ai.Dest.Local
	destLocal := lookupLocal(fn, destID)
	if destLocal == nil {
		return pendingInstr{}, "", 0, scalarUnknown, false
	}
	destType := mctx.scalarFromType(destLocal.Type, true)
	if destType == scalarUnknown {
		return pendingInstr{}, "", 0, scalarUnknown, false
	}
	pending, expr, ok := classifyAssignSrc(ai.Src, destType, bindings, mctx)
	if !ok {
		return pendingInstr{}, "", 0, scalarUnknown, false
	}
	return pending, expr, destID, destType, true
}

// classifyCallStep validates and decodes a direct call (`FnRef`) into
// a pending call instruction. Indirect calls / unit-result calls /
// projection destinations / non-scalar args decline.
func classifyCallStep(fn *mir.Function, ci *mir.CallInstr, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (pendingInstr, mir.LocalID, scalarType, bool) {
	if ci.Dest == nil {
		// Stage0 only handles calls whose result feeds a local.
		return pendingInstr{}, 0, scalarUnknown, false
	}
	if ci.Dest.HasProjections() {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	ref, ok := ci.Callee.(*mir.FnRef)
	if !ok {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	if ref.Symbol == "" {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	allowOpaqueUserNamed := !mctx.knownSymbols[ref.Symbol]
	destID := ci.Dest.Local
	destLocal := lookupLocal(fn, destID)
	if destLocal == nil {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	destType := mctx.scalarFromType(destLocal.Type, allowOpaqueUserNamed)
	if destType == scalarUnknown {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	// Validate the callee's declared return type matches dest.
	fnTy, ok := ref.Type.(*ir.FnType)
	if !ok || fnTy == nil {
		if !allowOpaqueUserNamed || !isErrType(ref.Type) {
			return pendingInstr{}, 0, scalarUnknown, false
		}
		args, ok := resolveCallArgsWithoutFnType(ci.Args, bindings, mctx)
		if !ok {
			return pendingInstr{}, 0, scalarUnknown, false
		}
		declareFunctionPrototype(mctx, ref.Symbol, destType, args)
		return pendingInstr{
			kind:       instrCall,
			callSymbol: ref.Symbol,
			callArgs:   args,
		}, destID, destType, true
	}
	if mctx.scalarFromType(fnTy.Return, allowOpaqueUserNamed) != destType {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	if len(fnTy.Params) != len(ci.Args) {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	args := make([]callArg, 0, len(ci.Args))
	for i, op := range ci.Args {
		argExpr, argTy, okOp := resolveOperand(op, bindings, mctx)
		if !okOp {
			return pendingInstr{}, 0, scalarUnknown, false
		}
		// Param type must agree with the callee's declared param.
		paramTy := mctx.scalarFromType(fnTy.Params[i], allowOpaqueUserNamed)
		if paramTy == scalarUnknown || paramTy != argTy {
			return pendingInstr{}, 0, scalarUnknown, false
		}
		args = append(args, callArg{expr: argExpr, ty: argTy.llvm()})
	}
	if !mctx.knownSymbols[ref.Symbol] {
		declareFunctionPrototype(mctx, ref.Symbol, destType, args)
	}
	return pendingInstr{
		kind:       instrCall,
		callSymbol: ref.Symbol,
		callArgs:   args,
	}, destID, destType, true
}

func classifyVoidCallLine(ci *mir.CallInstr, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (string, bool) {
	if ci == nil || ci.Dest != nil {
		return "", false
	}
	ref, ok := ci.Callee.(*mir.FnRef)
	if !ok || ref.Symbol == "" {
		return "", false
	}
	allowOpaqueUserNamed := !mctx.knownSymbols[ref.Symbol]
	var args []callArg
	if fnTy, ok := ref.Type.(*ir.FnType); ok && fnTy != nil {
		if !isUnitType(fnTy.Return) || len(fnTy.Params) != len(ci.Args) {
			return "", false
		}
		args = make([]callArg, 0, len(ci.Args))
		for i, op := range ci.Args {
			argExpr, argTy, okOp := resolveOperand(op, bindings, mctx)
			if !okOp {
				return "", false
			}
			paramTy := mctx.scalarFromType(fnTy.Params[i], allowOpaqueUserNamed)
			if paramTy == scalarUnknown || paramTy != argTy {
				return "", false
			}
			args = append(args, callArg{expr: argExpr, ty: argTy.llvm()})
		}
	} else {
		if !allowOpaqueUserNamed || !isErrType(ref.Type) {
			return "", false
		}
		var okArgs bool
		args, okArgs = resolveCallArgsWithoutFnType(ci.Args, bindings, mctx)
		if !okArgs {
			return "", false
		}
	}
	if !mctx.knownSymbols[ref.Symbol] {
		declareVoidFunctionPrototype(mctx, ref.Symbol, args)
	}
	return renderVoidCallLine(ref.Symbol, args), true
}

func isErrType(t mir.Type) bool {
	_, ok := t.(*ir.ErrType)
	return ok
}

func resolveCallArgsWithoutFnType(argsIn []mir.Operand, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) ([]callArg, bool) {
	args := make([]callArg, 0, len(argsIn))
	for _, op := range argsIn {
		argExpr, argTy, ok := resolveOperand(op, bindings, mctx)
		if !ok || argTy == scalarUnknown {
			return nil, false
		}
		args = append(args, callArg{expr: argExpr, ty: argTy.llvm()})
	}
	return args, true
}

func declareFunctionPrototype(mctx *moduleCtx, symbol string, retType scalarType, args []callArg) {
	if mctx == nil || symbol == "" {
		return
	}
	key := "__stage0.fn_decl." + symbol
	if mctx.emittedStructs == nil {
		mctx.emittedStructs = map[string]bool{}
	}
	if mctx.emittedStructs[key] {
		return
	}
	mctx.emittedStructs[key] = true
	fmt.Fprintf(mctx.extraDecls, "declare %s @%s(", retType.llvm(), symbol)
	for i, a := range args {
		if i > 0 {
			mctx.extraDecls.WriteString(", ")
		}
		mctx.extraDecls.WriteString(a.ty)
	}
	mctx.extraDecls.WriteString(")\n")
}

func declareVoidFunctionPrototype(mctx *moduleCtx, symbol string, args []callArg) {
	if mctx == nil || symbol == "" {
		return
	}
	key := "__stage0.fn_decl." + symbol
	if mctx.emittedStructs == nil {
		mctx.emittedStructs = map[string]bool{}
	}
	if mctx.emittedStructs[key] {
		return
	}
	mctx.emittedStructs[key] = true
	fmt.Fprintf(mctx.extraDecls, "declare void @%s(", symbol)
	for i, a := range args {
		if i > 0 {
			mctx.extraDecls.WriteString(", ")
		}
		mctx.extraDecls.WriteString(a.ty)
	}
	mctx.extraDecls.WriteString(")\n")
}

func renderVoidCallLine(symbol string, args []callArg) string {
	var b strings.Builder
	fmt.Fprintf(&b, "  call void @%s(", symbol)
	for i, a := range args {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s %s", a.ty, a.expr)
	}
	b.WriteString(")\n")
	return b.String()
}

type intrinsicRuntimeSpec struct {
	symbol string
	ret    scalarType
	args   []scalarType
}

func classifyIntrinsicValueStep(fn *mir.Function, ii *mir.IntrinsicInstr, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (pendingInstr, mir.LocalID, scalarType, bool) {
	if ii.Dest == nil || ii.Dest.HasProjections() {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	destID := ii.Dest.Local
	destLocal := lookupLocal(fn, destID)
	if destLocal == nil {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	destType := mctx.scalarFromType(destLocal.Type, true)
	if destType == scalarUnknown {
		return pendingInstr{}, 0, scalarUnknown, false
	}

	if ii.Kind == mir.IntrinsicStringConcat {
		return classifyStringConcatIntrinsic(ii, destID, destType, bindings, mctx)
	}

	spec, ok := intrinsicRuntimeCallSpec(ii.Kind)
	if !ok || spec.ret != destType || len(spec.args) != len(ii.Args) {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	args := make([]callArg, 0, len(ii.Args))
	for i, op := range ii.Args {
		expr, ty, ok := resolveOperand(op, bindings, mctx)
		if !ok || ty != spec.args[i] {
			return pendingInstr{}, 0, scalarUnknown, false
		}
		args = append(args, callArg{expr: expr, ty: ty.llvm()})
	}
	declareRuntimePrototype(mctx, spec.symbol, spec.ret, args)
	return pendingInstr{
		kind:       instrCall,
		callSymbol: spec.symbol,
		callArgs:   args,
	}, destID, destType, true
}

func classifyStringConcatIntrinsic(ii *mir.IntrinsicInstr, destID mir.LocalID, destType scalarType, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (pendingInstr, mir.LocalID, scalarType, bool) {
	if destType != scalarString || len(ii.Args) < 2 {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	args := make([]callArg, 0, len(ii.Args))
	for _, op := range ii.Args {
		expr, ty, ok := resolveOperand(op, bindings, mctx)
		if !ok || ty != scalarString {
			return pendingInstr{}, 0, scalarUnknown, false
		}
		args = append(args, callArg{expr: expr, ty: "ptr"})
	}
	declareStringConcatRuntime(mctx)
	if len(args) == 2 {
		return pendingInstr{
			kind:       instrCall,
			callSymbol: "osty_rt_strings_Concat",
			callArgs:   args,
		}, destID, destType, true
	}
	return pendingInstr{
		kind:       instrCallChain,
		resultType: scalarString,
		callSymbol: "osty_rt_strings_Concat",
		callArgs:   args,
	}, destID, destType, true
}

func intrinsicRuntimeCallSpec(kind mir.IntrinsicKind) (intrinsicRuntimeSpec, bool) {
	switch kind {
	case mir.IntrinsicStringSplit:
		return intrinsicRuntimeSpec{"osty_rt_strings_Split", scalarOpaquePtr, []scalarType{scalarString, scalarString}}, true
	case mir.IntrinsicStringJoin:
		return intrinsicRuntimeSpec{"osty_rt_strings_Join", scalarString, []scalarType{scalarOpaquePtr, scalarString}}, true
	case mir.IntrinsicStringLen:
		return intrinsicRuntimeSpec{"osty_rt_strings_ByteLen", scalarInt, []scalarType{scalarString}}, true
	case mir.IntrinsicStringContains:
		return intrinsicRuntimeSpec{"osty_rt_strings_Contains", scalarBool, []scalarType{scalarString, scalarString}}, true
	case mir.IntrinsicStringStartsWith:
		return intrinsicRuntimeSpec{"osty_rt_strings_HasPrefix", scalarBool, []scalarType{scalarString, scalarString}}, true
	case mir.IntrinsicStringEndsWith:
		return intrinsicRuntimeSpec{"osty_rt_strings_HasSuffix", scalarBool, []scalarType{scalarString, scalarString}}, true
	case mir.IntrinsicStringCount:
		return intrinsicRuntimeSpec{"osty_rt_strings_Count", scalarInt, []scalarType{scalarString, scalarString}}, true
	case mir.IntrinsicStringIndexOf:
		return intrinsicRuntimeSpec{"osty_rt_strings_IndexOf", scalarInt, []scalarType{scalarString, scalarString}}, true
	case mir.IntrinsicStringLastIndexOf:
		return intrinsicRuntimeSpec{"osty_rt_strings_LastIndexOf", scalarInt, []scalarType{scalarString, scalarString}}, true
	case mir.IntrinsicStringTrim:
		return intrinsicRuntimeSpec{"osty_rt_strings_TrimSpace", scalarString, []scalarType{scalarString}}, true
	case mir.IntrinsicStringTrimStart:
		return intrinsicRuntimeSpec{"osty_rt_strings_TrimStart", scalarString, []scalarType{scalarString}}, true
	case mir.IntrinsicStringTrimEnd:
		return intrinsicRuntimeSpec{"osty_rt_strings_TrimEnd", scalarString, []scalarType{scalarString}}, true
	case mir.IntrinsicStringTrimPrefix:
		return intrinsicRuntimeSpec{"osty_rt_strings_TrimPrefix", scalarString, []scalarType{scalarString, scalarString}}, true
	case mir.IntrinsicStringTrimSuffix:
		return intrinsicRuntimeSpec{"osty_rt_strings_TrimSuffix", scalarString, []scalarType{scalarString, scalarString}}, true
	case mir.IntrinsicStringToUpper:
		return intrinsicRuntimeSpec{"osty_rt_strings_ToUpper", scalarString, []scalarType{scalarString}}, true
	case mir.IntrinsicStringToLower:
		return intrinsicRuntimeSpec{"osty_rt_strings_ToLower", scalarString, []scalarType{scalarString}}, true
	case mir.IntrinsicStringReplace:
		return intrinsicRuntimeSpec{"osty_rt_strings_Replace", scalarString, []scalarType{scalarString, scalarString, scalarString}}, true
	case mir.IntrinsicStringReplaceAll:
		return intrinsicRuntimeSpec{"osty_rt_strings_ReplaceAll", scalarString, []scalarType{scalarString, scalarString, scalarString}}, true
	case mir.IntrinsicStringRepeat:
		return intrinsicRuntimeSpec{"osty_rt_strings_Repeat", scalarString, []scalarType{scalarString, scalarInt}}, true
	case mir.IntrinsicStringSubstring:
		return intrinsicRuntimeSpec{"osty_rt_strings_Slice", scalarString, []scalarType{scalarString, scalarInt, scalarInt}}, true
	case mir.IntrinsicStringSplitN:
		return intrinsicRuntimeSpec{"osty_rt_strings_SplitN", scalarOpaquePtr, []scalarType{scalarString, scalarString, scalarInt}}, true
	case mir.IntrinsicStringFields:
		return intrinsicRuntimeSpec{"osty_rt_strings_Fields", scalarOpaquePtr, []scalarType{scalarString}}, true
	case mir.IntrinsicStringChars:
		return intrinsicRuntimeSpec{"osty_rt_strings_Chars", scalarOpaquePtr, []scalarType{scalarString}}, true
	case mir.IntrinsicStringBytes:
		return intrinsicRuntimeSpec{"osty_rt_strings_Bytes", scalarOpaquePtr, []scalarType{scalarString}}, true
	case mir.IntrinsicStringNthSegment:
		return intrinsicRuntimeSpec{"osty_rt_strings_NthSegment", scalarString, []scalarType{scalarString, scalarString, scalarInt}}, true
	case mir.IntrinsicListLen:
		return intrinsicRuntimeSpec{"osty_rt_list_len", scalarInt, []scalarType{scalarOpaquePtr}}, true
	}
	return intrinsicRuntimeSpec{}, false
}

func declareRuntimePrototype(mctx *moduleCtx, symbol string, retType scalarType, args []callArg) {
	if mctx == nil || symbol == "" {
		return
	}
	key := "__stage0.runtime_decl." + symbol
	if mctx.emittedStructs == nil {
		mctx.emittedStructs = map[string]bool{}
	}
	if mctx.emittedStructs[key] {
		return
	}
	mctx.emittedStructs[key] = true
	fmt.Fprintf(mctx.extraDecls, "declare %s @%s(", retType.llvm(), symbol)
	for i, a := range args {
		if i > 0 {
			mctx.extraDecls.WriteString(", ")
		}
		mctx.extraDecls.WriteString(a.ty)
	}
	mctx.extraDecls.WriteString(")\n")
}

// classifyAssignSrc reduces an AssignInstr.Src to either a pending
// inline binding (no LLVM emission) or a pending binary op (emit a
// fresh SSA register). `expr` is the LLVM operand string for inline
// bindings; binary ops return "" (the emitter assigns a register
// number after seeing the full pending list).
func classifyAssignSrc(src mir.RValue, destType scalarType, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (pendingInstr, string, bool) {
	if use, ok := src.(*mir.UseRV); ok {
		expr, ty, ok := resolveOperand(use.Op, bindings, mctx)
		if !ok || ty != destType {
			return pendingInstr{}, "", false
		}
		return pendingInstr{kind: instrInline}, expr, true
	}
	if bin, ok := src.(*mir.BinaryRV); ok {
		// Special-case String + Add: lowers to a runtime ABI call
		// (`osty_rt_strings_Concat`) rather than a native LLVM
		// binary instruction.
		if bin.Op == mir.BinAdd && destType == scalarString {
			left, leftTy, ok := resolveOperand(bin.Left, bindings, mctx)
			if !ok || leftTy != scalarString {
				return pendingInstr{}, "", false
			}
			right, rightTy, ok := resolveOperand(bin.Right, bindings, mctx)
			if !ok || rightTy != scalarString {
				return pendingInstr{}, "", false
			}
			declareStringConcatRuntime(mctx)
			return pendingInstr{
				kind:       instrCall,
				callSymbol: "osty_rt_strings_Concat",
				callArgs: []callArg{
					{expr: left, ty: "ptr"},
					{expr: right, ty: "ptr"},
				},
			}, "", true
		}
		// P16 — Special-case String == String: lowers to a runtime
		// call (`osty_rt_strings_Equal`) returning i1. Falls through
		// to classifyBinary when the operand isn't String so Int ==
		// Int continues through `icmp eq`.
		if bin.Op == mir.BinEq && destType == scalarBool {
			if left, leftTy, ok := resolveOperand(bin.Left, bindings, mctx); ok && leftTy == scalarString {
				right, rightTy, ok := resolveOperand(bin.Right, bindings, mctx)
				if !ok || rightTy != scalarString {
					return pendingInstr{}, "", false
				}
				declareStringEqualRuntime(mctx)
				return pendingInstr{
					kind:       instrCall,
					callSymbol: "osty_rt_strings_Equal",
					callArgs: []callArg{
						{expr: left, ty: "ptr"},
						{expr: right, ty: "ptr"},
					},
				}, "", true
			}
		}
		llvmOp, resultType, operandType := classifyBinary(bin.Op)
		if llvmOp == "" || resultType != destType {
			return pendingInstr{}, "", false
		}
		left, leftTy, ok := resolveOperand(bin.Left, bindings, mctx)
		if !ok || leftTy != operandType {
			return pendingInstr{}, "", false
		}
		right, rightTy, ok := resolveOperand(bin.Right, bindings, mctx)
		if !ok || rightTy != operandType {
			return pendingInstr{}, "", false
		}
		return pendingInstr{
			kind:       instrBinary,
			binOp:      llvmOp,
			binArgType: operandType.llvm(),
			leftExpr:   left,
			rightExpr:  right,
		}, "", true
	}
	if agg, ok := src.(*mir.AggregateRV); ok {
		return classifyAggregateAssignSrc(agg, destType, bindings, mctx)
	}
	return pendingInstr{}, "", false
}

func classifyAggregateAssignSrc(agg *mir.AggregateRV, destType scalarType, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (pendingInstr, string, bool) {
	if agg != nil && agg.Kind == mir.AggEnumVariant && destType == scalarInt && len(agg.Fields) == 0 {
		return pendingInstr{kind: instrInline}, fmt.Sprintf("%d", agg.VariantIdx), true
	}
	if agg == nil || agg.Kind != mir.AggList || destType != scalarOpaquePtr {
		return pendingInstr{}, "", false
	}
	args := make([]callArg, 0, len(agg.Fields))
	elemType := scalarUnknown
	for _, field := range agg.Fields {
		expr, ty, ok := resolveOperand(field, bindings, mctx)
		if !ok || ty == scalarUnknown {
			return pendingInstr{}, "", false
		}
		if elemType == scalarUnknown {
			elemType = ty
		}
		if ty != elemType {
			return pendingInstr{}, "", false
		}
		args = append(args, callArg{expr: expr, ty: ty.llvm()})
	}
	pushSymbol := listPushSymbolFor(elemType)
	if pushSymbol == "" && len(args) > 0 {
		return pendingInstr{}, "", false
	}
	declareListRuntime(mctx)
	return pendingInstr{
		kind:           instrListLiteral,
		resultType:     scalarOpaquePtr,
		callArgs:       args,
		listPushSymbol: pushSymbol,
	}, "", true
}

func listPushSymbolFor(elemType scalarType) string {
	switch elemType {
	case scalarInt:
		return "osty_rt_list_push_i64"
	case scalarBool:
		return "osty_rt_list_push_i1"
	case scalarString:
		return "osty_rt_list_push_string"
	case scalarOpaquePtr:
		return "osty_rt_list_push_ptr"
	}
	return ""
}

func setMutationSymbolFor(kind mir.IntrinsicKind, elemType scalarType) string {
	prefix := ""
	switch kind {
	case mir.IntrinsicSetInsert:
		prefix = "osty_rt_set_insert_"
	case mir.IntrinsicSetRemove:
		prefix = "osty_rt_set_remove_"
	default:
		return ""
	}
	switch elemType {
	case scalarInt:
		return prefix + "i64"
	case scalarBool:
		return prefix + "i1"
	case scalarString, scalarOpaquePtr:
		return prefix + "ptr"
	}
	return ""
}

// declareStringConcatRuntime appends the runtime ABI declaration for
// `osty_rt_strings_Concat` to extraDecls (idempotent — sentinel
// stored alongside other emit-once flags in `emittedStructs`).
func declareStringConcatRuntime(mctx *moduleCtx) {
	if mctx.emittedStructs == nil {
		mctx.emittedStructs = map[string]bool{}
	}
	if mctx.emittedStructs["__stage0.strings_concat"] {
		return
	}
	mctx.emittedStructs["__stage0.strings_concat"] = true
	mctx.extraDecls.WriteString("declare ptr @osty_rt_strings_Concat(ptr, ptr)\n")
}

// declareStringEqualRuntime appends the runtime ABI declaration for
// `osty_rt_strings_Equal` to extraDecls (idempotent). Mirrors the
// concat helper above — used by the P16 String == String path.
func declareStringEqualRuntime(mctx *moduleCtx) {
	if mctx.emittedStructs == nil {
		mctx.emittedStructs = map[string]bool{}
	}
	if mctx.emittedStructs["__stage0.strings_equal"] {
		return
	}
	mctx.emittedStructs["__stage0.strings_equal"] = true
	mctx.extraDecls.WriteString("declare i1 @osty_rt_strings_Equal(ptr, ptr)\n")
}

// resolveOperand returns (expression, type) for one MIR Operand using
// the prior-bindings map. Forward references and projections decline.
// String constants are interned through the moduleCtx pool, producing
// a `@.str.<N>` global symbol the caller can use directly.
func resolveOperand(op mir.Operand, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (string, scalarType, bool) {
	if con, ok := op.(*mir.ConstOp); ok {
		switch c := con.Const.(type) {
		case *mir.IntConst:
			if !isPrimType(c.Type(), ir.PrimInt) {
				return "", scalarUnknown, false
			}
			return fmt.Sprintf("%d", c.Value), scalarInt, true
		case *mir.BoolConst:
			if c.Value {
				return "true", scalarBool, true
			}
			return "false", scalarBool, true
		case *mir.StringConst:
			if mctx == nil {
				return "", scalarUnknown, false
			}
			return mctx.internStringConst(c.Value), scalarString, true
		}
		return "", scalarUnknown, false
	}
	if cp, ok := op.(*mir.CopyOp); ok {
		if cp.Place.HasProjections() {
			return "", scalarUnknown, false
		}
		b, found := bindings[cp.Place.Local]
		if !found || !b.defined {
			return "", scalarUnknown, false
		}
		return b.expr, b.ty, true
	}
	return "", scalarUnknown, false
}

func resolveListReceiverOperand(fn *mir.Function, op mir.Operand, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (string, string, bool) {
	expr, ty, ok := resolveOperand(op, bindings, mctx)
	if ok && ty == scalarOpaquePtr {
		return "", expr, true
	}
	cp, ok := op.(*mir.CopyOp)
	if !ok || fn == nil || mctx == nil || len(cp.Place.Projections) != 1 {
		return "", "", false
	}
	fp, ok := cp.Place.Projections[0].(*mir.FieldProj)
	if !ok {
		return "", "", false
	}
	base, found := bindings[cp.Place.Local]
	if !found || !base.defined || base.ty != scalarOpaquePtr {
		return "", "", false
	}
	baseLocal := lookupLocal(fn, cp.Place.Local)
	if baseLocal == nil {
		return "", "", false
	}
	named, ok := baseLocal.Type.(*ir.NamedType)
	if !ok || named == nil || named.Name == "" {
		return "", "", false
	}
	fieldTypes, ok := mctx.lookupStructFields(named.Name)
	if !ok || fp.Index < 0 || fp.Index >= len(fieldTypes) || fieldTypes[fp.Index] != scalarOpaquePtr {
		return "", "", false
	}
	mctx.emitStructDef(named.Name, fieldTypes)
	slot := mctx.freshTempName("list.field.slot")
	value := mctx.freshTempName("list.field")
	prelude := fmt.Sprintf("  %s = getelementptr inbounds %%%s, ptr %s, i32 0, i32 %d\n  %s = load ptr, ptr %s\n",
		slot, named.Name, base.expr, fp.Index, value, slot)
	return prelude, value, true
}

func emitSequentialReturn(out *strings.Builder, fn *mir.Function, pat sequentialPattern) error {
	retLLVM := pat.retType.llvm()
	fmt.Fprintf(out, "define %s @%s(", retLLVM, fn.Name)
	for i, name := range pat.paramNames {
		if i > 0 {
			out.WriteString(", ")
		}
		fmt.Fprintf(out, "%s %%%s", pat.paramTypes[i].llvm(), name)
	}
	out.WriteString(") {\n")
	out.WriteString("entry:\n")

	for _, pi := range pat.pending {
		emitPendingInstr(out, pi)
	}
	fmt.Fprintf(out, "  ret %s %s\n", retLLVM, pat.returnExpr)
	out.WriteString("}\n\n")
	return nil
}

func emitPendingInstr(out *strings.Builder, pi pendingInstr) {
	switch pi.kind {
	case instrBinary:
		fmt.Fprintf(out, "  %s = %s %s %s, %s\n", pi.binDestReg, pi.binOp, pi.binArgType, pi.leftExpr, pi.rightExpr)
	case instrCall:
		fmt.Fprintf(out, "  %s = call %s @%s(", pi.binDestReg, pi.resultType.llvm(), pi.callSymbol)
		for i, a := range pi.callArgs {
			if i > 0 {
				out.WriteString(", ")
			}
			fmt.Fprintf(out, "%s %s", a.ty, a.expr)
		}
		out.WriteString(")\n")
	case instrCallChain:
		emitCallChain(out, pi)
	case instrListLiteral:
		emitListLiteral(out, pi)
	case instrIntrinsic:
		out.WriteString(pi.intrinsicLine)
	}
}

// classifyBinary returns (llvmOp, resultType, operandType) for a MIR
// binary operator the stage0 sequential pattern supports. Returns
// ("", scalarUnknown, scalarUnknown) for unsupported ops.
func classifyBinary(op mir.BinaryOp) (string, scalarType, scalarType) {
	switch op {
	case mir.BinAdd:
		return "add", scalarInt, scalarInt
	case mir.BinSub:
		return "sub", scalarInt, scalarInt
	case mir.BinMul:
		return "mul", scalarInt, scalarInt
	case mir.BinDiv:
		return "sdiv", scalarInt, scalarInt
	case mir.BinMod:
		return "srem", scalarInt, scalarInt
	case mir.BinEq:
		return "icmp eq", scalarBool, scalarInt
	case mir.BinNeq:
		return "icmp ne", scalarBool, scalarInt
	case mir.BinLt:
		return "icmp slt", scalarBool, scalarInt
	case mir.BinLeq:
		return "icmp sle", scalarBool, scalarInt
	case mir.BinGt:
		return "icmp sgt", scalarBool, scalarInt
	case mir.BinGeq:
		return "icmp sge", scalarBool, scalarInt
	case mir.BinBitAnd:
		return "and", scalarInt, scalarInt
	case mir.BinBitOr:
		return "or", scalarInt, scalarInt
	case mir.BinBitXor:
		return "xor", scalarInt, scalarInt
	case mir.BinShl:
		return "shl", scalarInt, scalarInt
	case mir.BinShr:
		return "ashr", scalarInt, scalarInt
	case mir.BinAnd:
		return "and", scalarBool, scalarBool
	case mir.BinOr:
		return "or", scalarBool, scalarBool
	}
	return "", scalarUnknown, scalarUnknown
}

// disambiguateParamNames mutates `names` in place so that no two
// entries are identical, by suffixing collisions with `.<index>`.
func disambiguateParamNames(names []string) {
	for i := 1; i < len(names); i++ {
		for j := 0; j < i; j++ {
			if names[i] == names[j] {
				names[i] = fmt.Sprintf("%s.%d", names[i], i)
				break
			}
		}
	}
}

// ---- P3c: if-else with phi-merged return ----
//
// stage0 P3c handles a tightly-restricted four-block if-else shape:
//
//	entry  : (P3a-style instructions) + BranchTerm{cond, then, else}
//	then   : (P3a-style instructions; last AssignInstr writes ret) + GotoTerm(merge)
//	else   : (P3a-style instructions; last AssignInstr writes ret) + GotoTerm(merge)
//	merge  : zero instructions + ReturnTerm
//
// Cross-block bindings: only locals that are assigned in `entry` (and
// thus already bound when the branch fires) are visible to `then` /
// `else`. Intermediate locals defined inside `then` are not visible
// in `else`, and vice versa — they live and die in their branch.
// The return local is the only value that crosses the merge; stage0
// emits a `phi` node at the start of `merge` to combine the values
// produced by the two branches.

type ifElsePattern struct {
	retType    scalarType
	paramIDs   []mir.LocalID
	paramTypes []scalarType
	paramNames []string
	entry      blockEmit
	thenBlk    blockEmit
	elseBlk    blockEmit
	condExpr   string
	condType   string // always "i1" today
	thenLabel  string
	elseLabel  string
	mergeLabel string
	thenRet    string
	elseRet    string
}

type blockEmit struct {
	label   string
	pending []pendingInstr
}

func matchIfElseReturn(fn *mir.Function, mctx *moduleCtx) (ifElsePattern, bool) {
	pat := ifElsePattern{}
	pat.retType = scalarFromType(fn.ReturnType)
	if pat.retType == scalarUnknown {
		return pat, false
	}
	if len(fn.Params) > 2 {
		return pat, false
	}
	if len(fn.Blocks) != 4 {
		return pat, false
	}

	// Seed param bindings.
	pat.paramIDs = fn.Params
	pat.paramTypes = make([]scalarType, len(fn.Params))
	pat.paramNames = make([]string, len(fn.Params))
	fallbackNames := []string{"a", "b"}
	for i, pid := range fn.Params {
		loc := lookupLocal(fn, pid)
		if loc == nil || !loc.IsParam {
			return pat, false
		}
		pt := scalarFromType(loc.Type)
		if pt == scalarUnknown {
			return pat, false
		}
		pat.paramTypes[i] = pt
		pat.paramNames[i] = sanitizeLLVMName(loc.Name, fallbackNames[i])
	}
	disambiguateParamNames(pat.paramNames)

	entryBindings := map[mir.LocalID]localBinding{}
	for i, pid := range fn.Params {
		entryBindings[pid] = localBinding{
			expr:    "%" + pat.paramNames[i],
			ty:      pat.paramTypes[i],
			defined: true,
		}
	}

	// Identify entry / then / else / merge by structural shape.
	entryBlock := blockByID(fn, fn.Entry)
	if entryBlock == nil {
		return pat, false
	}
	branch, ok := entryBlock.Term.(*mir.BranchTerm)
	if !ok {
		return pat, false
	}
	thenBlock := blockByID(fn, branch.Then)
	elseBlock := blockByID(fn, branch.Else)
	if thenBlock == nil || elseBlock == nil || thenBlock.ID == elseBlock.ID {
		return pat, false
	}
	thenGoto, ok := thenBlock.Term.(*mir.GotoTerm)
	if !ok {
		return pat, false
	}
	elseGoto, ok := elseBlock.Term.(*mir.GotoTerm)
	if !ok {
		return pat, false
	}
	if thenGoto.Target != elseGoto.Target {
		return pat, false
	}
	mergeBlock := blockByID(fn, thenGoto.Target)
	if mergeBlock == nil || mergeBlock.ID == entryBlock.ID || mergeBlock.ID == thenBlock.ID || mergeBlock.ID == elseBlock.ID {
		return pat, false
	}
	if len(mergeBlock.Instrs) != 0 {
		return pat, false
	}
	if _, ok := mergeBlock.Term.(*mir.ReturnTerm); !ok {
		return pat, false
	}

	// SSA register counter spans the whole function.
	nextSSA := 0

	// Walk entry block.
	entryEmit, condExpr, condTy, ok := classifyEntryBlock(fn, entryBlock, entryBindings, mctx, &nextSSA)
	if !ok || condTy != scalarBool {
		return pat, false
	}
	pat.entry = entryEmit
	pat.condExpr = condExpr
	pat.condType = condTy.llvm()

	// Walk then / else, each forks a copy of entryBindings.
	thenEmit, thenRet, ok := classifyBranchBlock(fn, thenBlock, copyBindings(entryBindings), mctx, &nextSSA, fn.ReturnLocal, pat.retType)
	if !ok {
		return pat, false
	}
	elseEmit, elseRet, ok := classifyBranchBlock(fn, elseBlock, copyBindings(entryBindings), mctx, &nextSSA, fn.ReturnLocal, pat.retType)
	if !ok {
		return pat, false
	}
	pat.thenBlk = thenEmit
	pat.elseBlk = elseEmit
	pat.thenRet = thenRet
	pat.elseRet = elseRet

	pat.thenLabel = blockLabelName(thenBlock.ID, "then")
	pat.elseLabel = blockLabelName(elseBlock.ID, "else")
	pat.mergeLabel = blockLabelName(mergeBlock.ID, "merge")
	pat.entry.label = "entry"
	pat.thenBlk.label = pat.thenLabel
	pat.elseBlk.label = pat.elseLabel
	return pat, true
}

func emitIfElseReturn(out *strings.Builder, fn *mir.Function, pat ifElsePattern) error {
	retLLVM := pat.retType.llvm()
	fmt.Fprintf(out, "define %s @%s(", retLLVM, fn.Name)
	for i, name := range pat.paramNames {
		if i > 0 {
			out.WriteString(", ")
		}
		fmt.Fprintf(out, "%s %%%s", pat.paramTypes[i].llvm(), name)
	}
	out.WriteString(") {\n")

	emitBlock(out, pat.entry)
	fmt.Fprintf(out, "  br %s %s, label %%%s, label %%%s\n", pat.condType, pat.condExpr, pat.thenLabel, pat.elseLabel)

	out.WriteString("\n")
	emitBlock(out, pat.thenBlk)
	fmt.Fprintf(out, "  br label %%%s\n", pat.mergeLabel)

	out.WriteString("\n")
	emitBlock(out, pat.elseBlk)
	fmt.Fprintf(out, "  br label %%%s\n", pat.mergeLabel)

	out.WriteString("\n")
	fmt.Fprintf(out, "%s:\n", pat.mergeLabel)
	// phi node combines the return-local values produced by the two branches.
	fmt.Fprintf(out, "  %%retval = phi %s [ %s, %%%s ], [ %s, %%%s ]\n", retLLVM, pat.thenRet, pat.thenLabel, pat.elseRet, pat.elseLabel)
	fmt.Fprintf(out, "  ret %s %%retval\n", retLLVM)
	out.WriteString("}\n\n")
	return nil
}

func emitBlock(out *strings.Builder, blk blockEmit) {
	fmt.Fprintf(out, "%s:\n", blk.label)
	for _, pi := range blk.pending {
		emitPendingInstr(out, pi)
	}
}

func emitCallChain(out *strings.Builder, pi pendingInstr) {
	if len(pi.callArgs) < 2 || len(pi.chainRegs) != len(pi.callArgs)-1 {
		return
	}
	prev := pi.callArgs[0]
	for i, next := range pi.callArgs[1:] {
		reg := pi.chainRegs[i]
		fmt.Fprintf(out, "  %s = call %s @%s(%s %s, %s %s)\n",
			reg, pi.resultType.llvm(), pi.callSymbol,
			prev.ty, prev.expr, next.ty, next.expr)
		prev = callArg{expr: reg, ty: pi.resultType.llvm()}
	}
}

func emitListLiteral(out *strings.Builder, pi pendingInstr) {
	fmt.Fprintf(out, "  %s = call ptr @osty_rt_list_new()\n", pi.binDestReg)
	for _, a := range pi.callArgs {
		fmt.Fprintf(out, "  call void @%s(ptr %s, %s %s)\n", pi.listPushSymbol, pi.binDestReg, a.ty, a.expr)
	}
}

// ---- P17: if-else with phi-merged struct/tuple return ----
//
// stage0 P17 covers the canonical "branch returns one of two struct
// literals" shape — the front-end emits this for code like:
//
//	fn parseAiRepairMode(value: String) -> AiRepairModeResult {
//	    if value == "auto" {
//	        AiRepairModeResult { mode: "auto", ok: true }
//	    } else {
//	        AiRepairModeResult { mode: "", ok: false }
//	    }
//	}
//
// MIR shape (4-block if-else identical to P3c, struct/tuple return):
//
//	bb0(entry):  scalar instructions producing Bool cond + BranchTerm
//	bb1(then):   AssignInstr ReturnLocal = AggregateRV{Struct|Tuple} + GotoTerm(merge)
//	bb2(else):   AssignInstr ReturnLocal = AggregateRV{Struct|Tuple} + GotoTerm(merge)
//	bb3(merge):  zero instrs + ReturnTerm
//
// Each branch arm contains exactly one AssignInstr whose Src is a
// struct- or tuple-kind AggregateRV. Each Aggregate's field operands
// are scalar ConstOps or non-projection CopyOps of params (reuse
// classifyAggregateField). The two arms agree on the aggregate's type
// name + field layout (enforced by classifyAggregateReturnType against
// the function's declared return type).
//
// Output:
//
//	define %T @name(<params>) {
//	entry:
//	  ; entry-block scalar instructions
//	  br i1 %cond, label %then.B, label %else.C
//	then.B:
//	  ; insertvalue chain for then's aggregate
//	  br label %merge.D
//	else.C:
//	  ; insertvalue chain for else's aggregate
//	  br label %merge.D
//	merge.D:
//	  %retval = phi %T [ %thenAgg, %then.B ], [ %elseAgg, %else.C ]
//	  ret %T %retval
//	}

type ifElseAggregateBranch struct {
	label        string
	fieldExprs   []string
	startSSA     int    // SSA index of the first insertvalue in this branch
	resultExpr   string // SSA register that holds the fully-built aggregate
	predLabelOut string // label name used in the merge phi (matches `label`)
}

type ifElseAggregatePattern struct {
	typeName   string
	fieldTypes []scalarType
	paramIDs   []mir.LocalID
	paramTypes []scalarType
	paramNames []string

	entry      blockEmit
	condExpr   string
	condType   string
	thenLabel  string
	elseLabel  string
	mergeLabel string

	thenBranch ifElseAggregateBranch
	elseBranch ifElseAggregateBranch
}

func matchIfElseAggregateReturn(fn *mir.Function, mctx *moduleCtx) (ifElseAggregatePattern, bool) {
	pat := ifElseAggregatePattern{}

	typeName, fieldTypes, ok := classifyAggregateReturnType(fn.ReturnType, mctx)
	if !ok {
		return pat, false
	}
	pat.typeName = typeName
	pat.fieldTypes = fieldTypes

	if len(fn.Params) > 2 {
		return pat, false
	}
	if len(fn.Blocks) != 4 {
		return pat, false
	}

	pat.paramIDs = fn.Params
	pat.paramTypes = make([]scalarType, len(fn.Params))
	pat.paramNames = make([]string, len(fn.Params))
	fallbackNames := []string{"a", "b"}
	for i, pid := range fn.Params {
		loc := lookupLocal(fn, pid)
		if loc == nil || !loc.IsParam {
			return pat, false
		}
		pt := scalarFromType(loc.Type)
		if pt == scalarUnknown {
			return pat, false
		}
		pat.paramTypes[i] = pt
		pat.paramNames[i] = sanitizeLLVMName(loc.Name, fallbackNames[i])
	}
	disambiguateParamNames(pat.paramNames)

	entryBindings := map[mir.LocalID]localBinding{}
	for i, pid := range fn.Params {
		entryBindings[pid] = localBinding{
			expr:    "%" + pat.paramNames[i],
			ty:      pat.paramTypes[i],
			defined: true,
		}
	}

	entryBlock := blockByID(fn, fn.Entry)
	if entryBlock == nil {
		return pat, false
	}
	branch, ok := entryBlock.Term.(*mir.BranchTerm)
	if !ok {
		return pat, false
	}
	thenBlock := blockByID(fn, branch.Then)
	elseBlock := blockByID(fn, branch.Else)
	if thenBlock == nil || elseBlock == nil || thenBlock.ID == elseBlock.ID {
		return pat, false
	}
	thenGoto, ok := thenBlock.Term.(*mir.GotoTerm)
	if !ok {
		return pat, false
	}
	elseGoto, ok := elseBlock.Term.(*mir.GotoTerm)
	if !ok {
		return pat, false
	}
	if thenGoto.Target != elseGoto.Target {
		return pat, false
	}
	mergeBlock := blockByID(fn, thenGoto.Target)
	if mergeBlock == nil || mergeBlock.ID == entryBlock.ID || mergeBlock.ID == thenBlock.ID || mergeBlock.ID == elseBlock.ID {
		return pat, false
	}
	if len(mergeBlock.Instrs) != 0 {
		return pat, false
	}
	if _, ok := mergeBlock.Term.(*mir.ReturnTerm); !ok {
		return pat, false
	}

	nextSSA := 0

	// Walk entry block; produce scalar Bool cond.
	entryEmit, condExpr, condTy, ok := classifyEntryBlock(fn, entryBlock, entryBindings, mctx, &nextSSA)
	if !ok || condTy != scalarBool {
		return pat, false
	}
	pat.entry = entryEmit
	pat.condExpr = condExpr
	pat.condType = condTy.llvm()

	pat.thenLabel = blockLabelName(thenBlock.ID, "then")
	pat.elseLabel = blockLabelName(elseBlock.ID, "else")
	pat.mergeLabel = blockLabelName(mergeBlock.ID, "merge")
	pat.entry.label = "entry"

	thenBranch, ok := classifyAggregateBranch(fn, thenBlock, pat.thenLabel, pat.fieldTypes, &nextSSA, mctx)
	if !ok {
		return pat, false
	}
	pat.thenBranch = thenBranch

	elseBranch, ok := classifyAggregateBranch(fn, elseBlock, pat.elseLabel, pat.fieldTypes, &nextSSA, mctx)
	if !ok {
		return pat, false
	}
	pat.elseBranch = elseBranch

	return pat, true
}

// classifyAggregateBranch validates one branch arm of P17:
// exactly one AssignInstr writing AggregateRV{Struct|Tuple} to the
// return local, then GotoTerm to the merge block. Reserves SSA numbers
// for the insertvalue chain and remembers the final aggregate register
// name for the merge-phi emission.
func classifyAggregateBranch(fn *mir.Function, bb *mir.BasicBlock, label string, fieldTypes []scalarType, nextSSA *int, mctx *moduleCtx) (ifElseAggregateBranch, bool) {
	out := ifElseAggregateBranch{label: label, predLabelOut: label}
	if len(bb.Instrs) != 1 {
		return out, false
	}
	ai, ok := bb.Instrs[0].(*mir.AssignInstr)
	if !ok {
		return out, false
	}
	if ai.Dest.Local != fn.ReturnLocal || ai.Dest.HasProjections() {
		return out, false
	}
	agg, ok := ai.Src.(*mir.AggregateRV)
	if !ok {
		return out, false
	}
	if agg.Kind != mir.AggStruct && agg.Kind != mir.AggTuple {
		return out, false
	}
	if len(agg.Fields) != len(fieldTypes) {
		return out, false
	}
	paramRegs := map[mir.LocalID]string{}
	for _, pid := range fn.Params {
		loc := lookupLocal(fn, pid)
		if loc == nil {
			return out, false
		}
		paramRegs[pid] = "%" + sanitizeLLVMName(loc.Name, "a")
	}
	out.fieldExprs = make([]string, len(agg.Fields))
	for i, f := range agg.Fields {
		expr, ty, ok := classifyAggregateField(f, paramRegs, fn, mctx)
		if !ok || ty != fieldTypes[i] {
			return out, false
		}
		out.fieldExprs[i] = expr
	}
	out.startSSA = *nextSSA
	*nextSSA += len(fieldTypes)
	out.resultExpr = fmt.Sprintf("%%%d", out.startSSA+len(fieldTypes)-1)
	return out, true
}

func emitIfElseAggregateReturn(out *strings.Builder, fn *mir.Function, pat ifElseAggregatePattern, mctx *moduleCtx) error {
	mctx.emitStructDef(pat.typeName, pat.fieldTypes)
	fmt.Fprintf(out, "define %%%s @%s(", pat.typeName, fn.Name)
	for i, name := range pat.paramNames {
		if i > 0 {
			out.WriteString(", ")
		}
		fmt.Fprintf(out, "%s %%%s", pat.paramTypes[i].llvm(), name)
	}
	out.WriteString(") {\n")

	// Entry block.
	emitBlock(out, pat.entry)
	fmt.Fprintf(out, "  br %s %s, label %%%s, label %%%s\n", pat.condType, pat.condExpr, pat.thenLabel, pat.elseLabel)
	out.WriteString("\n")

	// Branch arm emit helpers.
	emitArm := func(arm ifElseAggregateBranch) {
		fmt.Fprintf(out, "%s:\n", arm.label)
		prev := "poison"
		for i, fieldExpr := range arm.fieldExprs {
			reg := fmt.Sprintf("%%%d", arm.startSSA+i)
			fmt.Fprintf(out, "  %s = insertvalue %%%s %s, %s %s, %d\n", reg, pat.typeName, prev, pat.fieldTypes[i].llvm(), fieldExpr, i)
			prev = reg
		}
		fmt.Fprintf(out, "  br label %%%s\n", pat.mergeLabel)
	}

	emitArm(pat.thenBranch)
	out.WriteString("\n")
	emitArm(pat.elseBranch)
	out.WriteString("\n")

	// Merge block: phi + ret.
	fmt.Fprintf(out, "%s:\n", pat.mergeLabel)
	fmt.Fprintf(out, "  %%retval = phi %%%s [ %s, %%%s ], [ %s, %%%s ]\n",
		pat.typeName,
		pat.thenBranch.resultExpr, pat.thenBranch.predLabelOut,
		pat.elseBranch.resultExpr, pat.elseBranch.predLabelOut,
	)
	fmt.Fprintf(out, "  ret %%%s %%retval\n", pat.typeName)
	out.WriteString("}\n\n")
	return nil
}

// ---- P18: `||` short-circuit + if-else struct/tuple return ----
//
// stage0 P18 covers the canonical "if A || B { S{..} } else { S{..} }"
// shape — the front-end lowers `||` as a 3-block boolean phi so the
// resulting CFG is 7 blocks (3 for the OR, 4 for the if-else):
//
//	bb0(entry):       AssignInstr Bool L = leftCond ; BranchTerm(L → bb1, bb2)
//	bb1(short_true):  AssignInstr Bool R = UseRV(BoolConst{true}) ; GotoTerm(bb3)
//	bb2(right_eval):  AssignInstr Bool R = rightCond              ; GotoTerm(bb3)
//	bb3(or_merge):    BranchTerm(R → bb4, bb5)                     [empty body]
//	bb4(then):        AssignInstr ReturnLocal = AggregateRV ; GotoTerm(bb6)
//	bb5(else):        AssignInstr ReturnLocal = AggregateRV ; GotoTerm(bb6)
//	bb6(merge):       ReturnTerm                            [empty body]
//
// bb0 owns the leftCond computation (any scalar P3a-style instructions
// + a final AssignInstr to the same Bool local that bb1 / bb2 also
// write). bb2 owns the rightCond computation (same shape). bb3 must
// be empty so the merged Bool flows directly from the implicit phi.
//
// Output:
//
//	define %T @name(<params>) {
//	entry:
//	  ; leftCond instructions
//	  br i1 %L, label %or_short.K, label %or_right.M
//	or_short.K:
//	  br label %or_merge.N
//	or_right.M:
//	  ; rightCond instructions
//	  br label %or_merge.N
//	or_merge.N:
//	  %or = phi i1 [ true, %or_short.K ], [ %R, %or_right.M ]
//	  br i1 %or, label %then.X, label %else.Y
//	then.X:
//	  ; insertvalue chain for then's aggregate
//	  br label %merge.Z
//	else.Y:
//	  ; insertvalue chain for else's aggregate
//	  br label %merge.Z
//	merge.Z:
//	  %retval = phi %T [ %thenAgg, %then.X ], [ %elseAgg, %else.Y ]
//	  ret %T %retval
//	}

type orShortCircuitPattern struct {
	typeName   string
	fieldTypes []scalarType
	paramIDs   []mir.LocalID
	paramTypes []scalarType
	paramNames []string

	entry        blockEmit
	leftCondExpr string
	leftCondType string

	orShortLabel  string
	orRightLabel  string
	orMergeLabel  string
	rightEvalEmit blockEmit
	rightCondExpr string

	thenLabel  string
	elseLabel  string
	mergeLabel string

	thenBranch ifElseAggregateBranch
	elseBranch ifElseAggregateBranch
}

func matchOrShortCircuitIfElseAggregate(fn *mir.Function, mctx *moduleCtx) (orShortCircuitPattern, bool) {
	pat := orShortCircuitPattern{}

	typeName, fieldTypes, ok := classifyAggregateReturnType(fn.ReturnType, mctx)
	if !ok {
		return pat, false
	}
	pat.typeName = typeName
	pat.fieldTypes = fieldTypes

	if len(fn.Params) > 2 {
		return pat, false
	}
	if len(fn.Blocks) != 7 {
		return pat, false
	}

	pat.paramIDs = fn.Params
	pat.paramTypes = make([]scalarType, len(fn.Params))
	pat.paramNames = make([]string, len(fn.Params))
	fallbackNames := []string{"a", "b"}
	for i, pid := range fn.Params {
		loc := lookupLocal(fn, pid)
		if loc == nil || !loc.IsParam {
			return pat, false
		}
		pt := scalarFromType(loc.Type)
		if pt == scalarUnknown {
			return pat, false
		}
		pat.paramTypes[i] = pt
		pat.paramNames[i] = sanitizeLLVMName(loc.Name, fallbackNames[i])
	}
	disambiguateParamNames(pat.paramNames)

	entryBindings := map[mir.LocalID]localBinding{}
	for i, pid := range fn.Params {
		entryBindings[pid] = localBinding{
			expr:    "%" + pat.paramNames[i],
			ty:      pat.paramTypes[i],
			defined: true,
		}
	}

	entryBlock := blockByID(fn, fn.Entry)
	if entryBlock == nil {
		return pat, false
	}
	leftBranch, ok := entryBlock.Term.(*mir.BranchTerm)
	if !ok {
		return pat, false
	}
	shortBlock := blockByID(fn, leftBranch.Then)
	rightEvalBlock := blockByID(fn, leftBranch.Else)
	if shortBlock == nil || rightEvalBlock == nil || shortBlock.ID == rightEvalBlock.ID {
		return pat, false
	}
	shortGoto, ok := shortBlock.Term.(*mir.GotoTerm)
	if !ok {
		return pat, false
	}
	rightGoto, ok := rightEvalBlock.Term.(*mir.GotoTerm)
	if !ok {
		return pat, false
	}
	if shortGoto.Target != rightGoto.Target {
		return pat, false
	}
	orMergeBlock := blockByID(fn, shortGoto.Target)
	if orMergeBlock == nil || len(orMergeBlock.Instrs) != 0 {
		return pat, false
	}
	orMergeBranch, ok := orMergeBlock.Term.(*mir.BranchTerm)
	if !ok {
		return pat, false
	}
	thenBlock := blockByID(fn, orMergeBranch.Then)
	elseBlock := blockByID(fn, orMergeBranch.Else)
	if thenBlock == nil || elseBlock == nil || thenBlock.ID == elseBlock.ID {
		return pat, false
	}
	thenGoto, ok := thenBlock.Term.(*mir.GotoTerm)
	if !ok {
		return pat, false
	}
	elseGoto, ok := elseBlock.Term.(*mir.GotoTerm)
	if !ok {
		return pat, false
	}
	if thenGoto.Target != elseGoto.Target {
		return pat, false
	}
	mergeBlock := blockByID(fn, thenGoto.Target)
	if mergeBlock == nil || len(mergeBlock.Instrs) != 0 {
		return pat, false
	}
	if _, ok := mergeBlock.Term.(*mir.ReturnTerm); !ok {
		return pat, false
	}

	// Reject overlapping IDs.
	ids := []mir.BlockID{entryBlock.ID, shortBlock.ID, rightEvalBlock.ID, orMergeBlock.ID, thenBlock.ID, elseBlock.ID, mergeBlock.ID}
	seen := map[mir.BlockID]bool{}
	for _, id := range ids {
		if seen[id] {
			return pat, false
		}
		seen[id] = true
	}

	nextSSA := 0

	// Walk entry: left-cond instructions ending in BranchTerm.
	entryEmit, leftCondExpr, leftCondTy, ok := classifyEntryBlock(fn, entryBlock, entryBindings, mctx, &nextSSA)
	if !ok || leftCondTy != scalarBool {
		return pat, false
	}
	pat.entry = entryEmit
	pat.leftCondExpr = leftCondExpr
	pat.leftCondType = leftCondTy.llvm()

	// short-circuit-true block must contain a single AssignInstr writing
	// `true` to a Bool local — that local is the merged Bool the or_merge
	// block branches on.
	if len(shortBlock.Instrs) != 1 {
		return pat, false
	}
	shortAi, ok := shortBlock.Instrs[0].(*mir.AssignInstr)
	if !ok || shortAi.Dest.HasProjections() {
		return pat, false
	}
	shortUse, ok := shortAi.Src.(*mir.UseRV)
	if !ok {
		return pat, false
	}
	shortConst, ok := shortUse.Op.(*mir.ConstOp)
	if !ok {
		return pat, false
	}
	shortBool, ok := shortConst.Const.(*mir.BoolConst)
	if !ok || !shortBool.Value {
		return pat, false
	}
	mergedLocal := shortAi.Dest.Local
	mergedLocalRec := lookupLocal(fn, mergedLocal)
	if mergedLocalRec == nil || scalarFromType(mergedLocalRec.Type) != scalarBool {
		return pat, false
	}

	// or_merge.Cond must Copy the same merged Bool local.
	orMergeCond, ok := orMergeBranch.Cond.(*mir.CopyOp)
	if !ok || orMergeCond.Place.HasProjections() || orMergeCond.Place.Local != mergedLocal {
		return pat, false
	}

	// right-eval block: walk instructions producing the merged Bool
	// local. The final AssignInstr must write to mergedLocal; only its
	// value contributes to the OR phi.
	rightBindings := copyBindings(entryBindings)
	rightEmit := blockEmit{}
	for _, instr := range rightEvalBlock.Instrs {
		if !applyStep(fn, instr, rightBindings, mctx, &nextSSA, &rightEmit) {
			return pat, false
		}
	}
	rightBinding, ok := rightBindings[mergedLocal]
	if !ok || !rightBinding.defined || rightBinding.ty != scalarBool {
		return pat, false
	}
	pat.rightEvalEmit = rightEmit
	pat.rightCondExpr = rightBinding.expr

	pat.orShortLabel = blockLabelName(shortBlock.ID, "or_short")
	pat.orRightLabel = blockLabelName(rightEvalBlock.ID, "or_right")
	pat.orMergeLabel = blockLabelName(orMergeBlock.ID, "or_merge")
	pat.thenLabel = blockLabelName(thenBlock.ID, "then")
	pat.elseLabel = blockLabelName(elseBlock.ID, "else")
	pat.mergeLabel = blockLabelName(mergeBlock.ID, "merge")
	pat.entry.label = "entry"
	pat.rightEvalEmit.label = pat.orRightLabel

	thenBranch, ok := classifyAggregateBranch(fn, thenBlock, pat.thenLabel, pat.fieldTypes, &nextSSA, mctx)
	if !ok {
		return pat, false
	}
	pat.thenBranch = thenBranch

	elseBranch, ok := classifyAggregateBranch(fn, elseBlock, pat.elseLabel, pat.fieldTypes, &nextSSA, mctx)
	if !ok {
		return pat, false
	}
	pat.elseBranch = elseBranch

	return pat, true
}

func emitOrShortCircuitIfElseAggregate(out *strings.Builder, fn *mir.Function, pat orShortCircuitPattern, mctx *moduleCtx) error {
	mctx.emitStructDef(pat.typeName, pat.fieldTypes)
	fmt.Fprintf(out, "define %%%s @%s(", pat.typeName, fn.Name)
	for i, name := range pat.paramNames {
		if i > 0 {
			out.WriteString(", ")
		}
		fmt.Fprintf(out, "%s %%%s", pat.paramTypes[i].llvm(), name)
	}
	out.WriteString(") {\n")

	// entry: left-cond + branch to short_true / right_eval
	emitBlock(out, pat.entry)
	fmt.Fprintf(out, "  br %s %s, label %%%s, label %%%s\n", pat.leftCondType, pat.leftCondExpr, pat.orShortLabel, pat.orRightLabel)
	out.WriteString("\n")

	// or_short: just goto or_merge (the `true` constant flows through phi)
	fmt.Fprintf(out, "%s:\n", pat.orShortLabel)
	fmt.Fprintf(out, "  br label %%%s\n", pat.orMergeLabel)
	out.WriteString("\n")

	// or_right: right-cond instructions + goto or_merge
	emitBlock(out, pat.rightEvalEmit)
	fmt.Fprintf(out, "  br label %%%s\n", pat.orMergeLabel)
	out.WriteString("\n")

	// or_merge: phi i1 + branch to then / else
	fmt.Fprintf(out, "%s:\n", pat.orMergeLabel)
	fmt.Fprintf(out, "  %%or = phi i1 [ true, %%%s ], [ %s, %%%s ]\n", pat.orShortLabel, pat.rightCondExpr, pat.orRightLabel)
	fmt.Fprintf(out, "  br i1 %%or, label %%%s, label %%%s\n", pat.thenLabel, pat.elseLabel)
	out.WriteString("\n")

	// then / else aggregate insertvalue chains
	emitArm := func(arm ifElseAggregateBranch) {
		fmt.Fprintf(out, "%s:\n", arm.label)
		prev := "poison"
		for i, fieldExpr := range arm.fieldExprs {
			reg := fmt.Sprintf("%%%d", arm.startSSA+i)
			fmt.Fprintf(out, "  %s = insertvalue %%%s %s, %s %s, %d\n", reg, pat.typeName, prev, pat.fieldTypes[i].llvm(), fieldExpr, i)
			prev = reg
		}
		fmt.Fprintf(out, "  br label %%%s\n", pat.mergeLabel)
	}
	emitArm(pat.thenBranch)
	out.WriteString("\n")
	emitArm(pat.elseBranch)
	out.WriteString("\n")

	// merge: phi %T + ret
	fmt.Fprintf(out, "%s:\n", pat.mergeLabel)
	fmt.Fprintf(out, "  %%retval = phi %%%s [ %s, %%%s ], [ %s, %%%s ]\n",
		pat.typeName,
		pat.thenBranch.resultExpr, pat.thenBranch.predLabelOut,
		pat.elseBranch.resultExpr, pat.elseBranch.predLabelOut,
	)
	fmt.Fprintf(out, "  ret %%%s %%retval\n", pat.typeName)
	out.WriteString("}\n\n")
	return nil
}

// ---- P19: N-arm else-if chain with struct/tuple return ----
//
// stage0 P19 covers `if A { S{..} } else if B { S{..} } else if C { S{..} }
// ... else { S{..} }` — an N-arm chain (N >= 2) where each arm assigns a
// struct/tuple AggregateRV to the return local. The front-end lowers this
// as a ladder of cond blocks where each block's `else` edge points either
// at the next rung or at the final-else arm. Matched arms and the final
// else flow into a single ReturnTerm block, possibly through one or more
// empty / storage-only intermediate goto blocks (Osty's lowerer emits
// these but the empty-goto-collapse pass leaves them alone when they
// carry StorageDead markers).
//
// Block budget for an N-arm chain (no `||` head, no nested cond exprs):
//
//	2*N + 1 cond/arm blocks (each rung: cond + matched-arm)
//	+ 1 final-else arm block
//	+ 1 ReturnTerm block
//	+ up to N intermediate goto blocks (storage-dead chain)
//
// The matcher walks the ladder explicitly: starting from entry, each
// cond block becomes a rung; the rung's `then` edge is a matched arm
// (AggregateRV → goto-chain → ReturnTerm), and the rung's `else` edge
// is either the next rung or the final-else arm (also an AggregateRV →
// goto-chain → ReturnTerm). The chain terminates when the else edge
// reaches a non-branch block.
//
// Output: classic phi over N+1 incoming arms.

type elseIfArm struct {
	label      string
	fieldExprs []string
	startSSA   int
	resultExpr string
}

type elseIfRung struct {
	condEmit blockEmit
	condExpr string
	condType string
	armLabel string // label of the matched arm block
}

type elseIfChainPattern struct {
	typeName   string
	fieldTypes []scalarType
	paramIDs   []mir.LocalID
	paramTypes []scalarType
	paramNames []string

	rungs      []elseIfRung
	arms       []elseIfArm // len == len(rungs) + 1; the last entry is the final else arm
	mergeLabel string
}

func matchElseIfChainAggregate(fn *mir.Function, mctx *moduleCtx) (elseIfChainPattern, bool) {
	pat := elseIfChainPattern{}

	typeName, fieldTypes, ok := classifyAggregateReturnType(fn.ReturnType, mctx)
	if !ok {
		return pat, false
	}
	pat.typeName = typeName
	pat.fieldTypes = fieldTypes

	if len(fn.Params) > 2 {
		return pat, false
	}
	pat.paramIDs = fn.Params
	pat.paramTypes = make([]scalarType, len(fn.Params))
	pat.paramNames = make([]string, len(fn.Params))
	fallbackNames := []string{"a", "b"}
	for i, pid := range fn.Params {
		loc := lookupLocal(fn, pid)
		if loc == nil || !loc.IsParam {
			return pat, false
		}
		pt := scalarFromType(loc.Type)
		if pt == scalarUnknown {
			return pat, false
		}
		pat.paramTypes[i] = pt
		pat.paramNames[i] = sanitizeLLVMName(loc.Name, fallbackNames[i])
	}
	disambiguateParamNames(pat.paramNames)

	// Locate the unique ReturnTerm block. Multiple return blocks would
	// require a more elaborate phi join; defer that to a later phase.
	var returnBlock *mir.BasicBlock
	for _, bb := range fn.Blocks {
		if _, ok := bb.Term.(*mir.ReturnTerm); ok {
			if returnBlock != nil {
				return pat, false
			}
			returnBlock = bb
		}
	}
	if returnBlock == nil {
		return pat, false
	}

	entryBindings := map[mir.LocalID]localBinding{}
	for i, pid := range fn.Params {
		entryBindings[pid] = localBinding{
			expr:    "%" + pat.paramNames[i],
			ty:      pat.paramTypes[i],
			defined: true,
		}
	}

	nextSSA := 0
	current := blockByID(fn, fn.Entry)
	if current == nil {
		return pat, false
	}

	rungIndex := 0
	for {
		// Each rung is a cond block ending in a BranchTerm.
		branch, ok := current.Term.(*mir.BranchTerm)
		if !ok {
			break
		}
		matchedBlock := blockByID(fn, branch.Then)
		nextOrElse := blockByID(fn, branch.Else)
		if matchedBlock == nil || nextOrElse == nil || matchedBlock.ID == nextOrElse.ID {
			return pat, false
		}

		// Run the cond block's instructions to compute the Bool cond.
		condBindings := copyBindings(entryBindings)
		condEmit := blockEmit{}
		for _, instr := range current.Instrs {
			if !applyStep(fn, instr, condBindings, mctx, &nextSSA, &condEmit) {
				return pat, false
			}
		}
		condExpr, condTy, ok := resolveOperand(branch.Cond, condBindings, mctx)
		if !ok || condTy != scalarBool {
			return pat, false
		}

		// Validate the matched arm (must reach returnBlock through goto chain).
		armFields, armStart, armReg, ok := classifyChainAggregateArm(fn, matchedBlock, returnBlock, fieldTypes, &nextSSA, mctx)
		if !ok {
			return pat, false
		}

		armLabel := blockLabelName(matchedBlock.ID, fmt.Sprintf("arm%d", rungIndex))
		if rungIndex == 0 {
			condEmit.label = "entry"
		} else {
			condEmit.label = blockLabelName(current.ID, fmt.Sprintf("rung%d", rungIndex))
		}

		pat.rungs = append(pat.rungs, elseIfRung{
			condEmit: condEmit,
			condExpr: condExpr,
			condType: condTy.llvm(),
			armLabel: armLabel,
		})
		pat.arms = append(pat.arms, elseIfArm{
			label:      armLabel,
			fieldExprs: armFields,
			startSSA:   armStart,
			resultExpr: armReg,
		})

		rungIndex++

		// Advance to the next rung (or accept nextOrElse as the final-else
		// aggregate arm if it is no longer a branch block).
		if _, ok := nextOrElse.Term.(*mir.BranchTerm); ok {
			current = nextOrElse
			continue
		}
		// Final-else arm: must be aggregate → returnBlock.
		armFields, armStart, armReg, ok = classifyChainAggregateArm(fn, nextOrElse, returnBlock, fieldTypes, &nextSSA, mctx)
		if !ok {
			return pat, false
		}
		pat.arms = append(pat.arms, elseIfArm{
			label:      blockLabelName(nextOrElse.ID, "fallback"),
			fieldExprs: armFields,
			startSSA:   armStart,
			resultExpr: armReg,
		})
		break
	}

	// Need at least 2 rungs (1 rung == standard if-else, handled by P17).
	if len(pat.rungs) < 2 || len(pat.arms) != len(pat.rungs)+1 {
		return pat, false
	}
	pat.mergeLabel = blockLabelName(returnBlock.ID, "merge")
	return pat, true
}

// classifyChainAggregateArm validates that `arm` is an aggregate-arm
// block — exactly one AssignInstr writing AggregateRV{Struct|Tuple} to
// the return local — and that its GotoTerm reaches the unique
// returnBlock through zero or more empty / storage-dead-only goto
// blocks. Returns the aggregate's field expressions, the SSA index of
// the first insertvalue, and the SSA register that holds the final
// aggregate value.
func classifyChainAggregateArm(fn *mir.Function, arm, returnBlock *mir.BasicBlock, fieldTypes []scalarType, nextSSA *int, mctx *moduleCtx) ([]string, int, string, bool) {
	if len(arm.Instrs) != 1 {
		return nil, 0, "", false
	}
	ai, ok := arm.Instrs[0].(*mir.AssignInstr)
	if !ok || ai.Dest.Local != fn.ReturnLocal || ai.Dest.HasProjections() {
		return nil, 0, "", false
	}
	agg, ok := ai.Src.(*mir.AggregateRV)
	if !ok {
		return nil, 0, "", false
	}
	if agg.Kind != mir.AggStruct && agg.Kind != mir.AggTuple {
		return nil, 0, "", false
	}
	if len(agg.Fields) != len(fieldTypes) {
		return nil, 0, "", false
	}
	gotoT, ok := arm.Term.(*mir.GotoTerm)
	if !ok {
		return nil, 0, "", false
	}
	// Walk the empty / storage-dead-only goto chain to returnBlock.
	cursor := gotoT.Target
	visited := map[mir.BlockID]bool{}
	for {
		if visited[cursor] {
			return nil, 0, "", false
		}
		visited[cursor] = true
		if cursor == returnBlock.ID {
			break
		}
		next := blockByID(fn, cursor)
		if next == nil {
			return nil, 0, "", false
		}
		// Allow storage-dead-only blocks in the chain.
		for _, instr := range next.Instrs {
			switch instr.(type) {
			case *mir.StorageLiveInstr, *mir.StorageDeadInstr:
				continue
			default:
				return nil, 0, "", false
			}
		}
		nextGoto, ok := next.Term.(*mir.GotoTerm)
		if !ok {
			return nil, 0, "", false
		}
		cursor = nextGoto.Target
	}

	// Build the field-expression list using existing aggregate helpers.
	paramRegs := map[mir.LocalID]string{}
	for _, pid := range fn.Params {
		loc := lookupLocal(fn, pid)
		if loc == nil {
			return nil, 0, "", false
		}
		paramRegs[pid] = "%" + sanitizeLLVMName(loc.Name, "a")
	}
	fieldExprs := make([]string, len(agg.Fields))
	for i, f := range agg.Fields {
		expr, ty, ok := classifyAggregateField(f, paramRegs, fn, mctx)
		if !ok || ty != fieldTypes[i] {
			return nil, 0, "", false
		}
		fieldExprs[i] = expr
	}
	startSSA := *nextSSA
	*nextSSA += len(fieldTypes)
	resultReg := fmt.Sprintf("%%%d", startSSA+len(fieldTypes)-1)
	return fieldExprs, startSSA, resultReg, true
}

func emitElseIfChainAggregate(out *strings.Builder, fn *mir.Function, pat elseIfChainPattern, mctx *moduleCtx) error {
	mctx.emitStructDef(pat.typeName, pat.fieldTypes)
	fmt.Fprintf(out, "define %%%s @%s(", pat.typeName, fn.Name)
	for i, name := range pat.paramNames {
		if i > 0 {
			out.WriteString(", ")
		}
		fmt.Fprintf(out, "%s %%%s", pat.paramTypes[i].llvm(), name)
	}
	out.WriteString(") {\n")

	// Emit each rung: cond + br to its arm vs the next rung label.
	for i, rung := range pat.rungs {
		emitBlock(out, rung.condEmit)
		// Else target is either the next rung's cond label or the final-else arm.
		var elseLabel string
		if i+1 < len(pat.rungs) {
			elseLabel = pat.rungs[i+1].condEmit.label
		} else {
			elseLabel = pat.arms[len(pat.arms)-1].label
		}
		fmt.Fprintf(out, "  br %s %s, label %%%s, label %%%s\n", rung.condType, rung.condExpr, rung.armLabel, elseLabel)
		out.WriteString("\n")
	}

	// Emit each arm: insertvalue chain + br to merge.
	for _, arm := range pat.arms {
		fmt.Fprintf(out, "%s:\n", arm.label)
		prev := "poison"
		for i, fieldExpr := range arm.fieldExprs {
			reg := fmt.Sprintf("%%%d", arm.startSSA+i)
			fmt.Fprintf(out, "  %s = insertvalue %%%s %s, %s %s, %d\n", reg, pat.typeName, prev, pat.fieldTypes[i].llvm(), fieldExpr, i)
			prev = reg
		}
		fmt.Fprintf(out, "  br label %%%s\n", pat.mergeLabel)
		out.WriteString("\n")
	}

	// Merge block: phi over all arms + ret.
	fmt.Fprintf(out, "%s:\n", pat.mergeLabel)
	out.WriteString("  %retval = phi %" + pat.typeName)
	for i, arm := range pat.arms {
		if i > 0 {
			out.WriteString(",")
		}
		fmt.Fprintf(out, " [ %s, %%%s ]", arm.resultExpr, arm.label)
	}
	out.WriteString("\n")
	fmt.Fprintf(out, "  ret %%%s %%retval\n", pat.typeName)
	out.WriteString("}\n\n")
	return nil
}

// ---- P20: `||` short-circuit head + N-arm else-if chain ----
//
// stage0 P20 covers the canonical `parseAiRepairMode`-class shape —
// `if A || B { S } else if C { S } ... else { S }`. The front-end
// fuses P18's 3-block `||` head with P19's N-rung else-if chain;
// neither matches the combined shape on its own. The combined CFG
// for an N-arm chain (counting the `||` arm as the first) is
// 2*N + 5 blocks ignoring storage-only goto chain blocks.
//
// Block topology (`||` head + chain ladder):
//
//	bb_entry:        leftCond + BranchTerm(L → bb_short, bb_right)
//	bb_short:        AssignInstr Bool R = true ; GotoTerm(bb_orMerge)
//	bb_right:        rightCond ; GotoTerm(bb_orMerge)
//	bb_orMerge:      [empty] BranchTerm(R → arm0, rung1_or_final)
//	arm0:            AggregateRV → goto chain → return
//	rung1_or_final:  another cond block (rung 1) OR aggregate fallback
//	... (rest of chain identical to P19)
//	bb_return:       ReturnTerm

type orChainPattern struct {
	typeName   string
	fieldTypes []scalarType
	paramIDs   []mir.LocalID
	paramTypes []scalarType
	paramNames []string

	entry        blockEmit
	leftCondExpr string
	leftCondType string

	orShortLabel  string
	orRightLabel  string
	orMergeLabel  string
	rightEvalEmit blockEmit
	rightCondExpr string

	rungs      []elseIfRung
	arms       []elseIfArm // len == len(rungs) + 2; arm0 driven by `||`-merged Bool, plus N-1 rung arms, plus final fallback
	mergeLabel string
}

func matchOrChainAggregate(fn *mir.Function, mctx *moduleCtx) (orChainPattern, bool) {
	pat := orChainPattern{}

	typeName, fieldTypes, ok := classifyAggregateReturnType(fn.ReturnType, mctx)
	if !ok {
		return pat, false
	}
	pat.typeName = typeName
	pat.fieldTypes = fieldTypes

	if len(fn.Params) > 2 {
		return pat, false
	}
	pat.paramIDs = fn.Params
	pat.paramTypes = make([]scalarType, len(fn.Params))
	pat.paramNames = make([]string, len(fn.Params))
	fallbackNames := []string{"a", "b"}
	for i, pid := range fn.Params {
		loc := lookupLocal(fn, pid)
		if loc == nil || !loc.IsParam {
			return pat, false
		}
		pt := scalarFromType(loc.Type)
		if pt == scalarUnknown {
			return pat, false
		}
		pat.paramTypes[i] = pt
		pat.paramNames[i] = sanitizeLLVMName(loc.Name, fallbackNames[i])
	}
	disambiguateParamNames(pat.paramNames)

	// Locate the unique ReturnTerm block.
	var returnBlock *mir.BasicBlock
	for _, bb := range fn.Blocks {
		if _, ok := bb.Term.(*mir.ReturnTerm); ok {
			if returnBlock != nil {
				return pat, false
			}
			returnBlock = bb
		}
	}
	if returnBlock == nil {
		return pat, false
	}

	entryBindings := map[mir.LocalID]localBinding{}
	for i, pid := range fn.Params {
		entryBindings[pid] = localBinding{
			expr:    "%" + pat.paramNames[i],
			ty:      pat.paramTypes[i],
			defined: true,
		}
	}

	nextSSA := 0

	// Detect `||` head at fn.Entry.
	entryBlock := blockByID(fn, fn.Entry)
	if entryBlock == nil {
		return pat, false
	}
	leftBranch, ok := entryBlock.Term.(*mir.BranchTerm)
	if !ok {
		return pat, false
	}
	shortBlock := blockByID(fn, leftBranch.Then)
	rightEvalBlock := blockByID(fn, leftBranch.Else)
	if shortBlock == nil || rightEvalBlock == nil || shortBlock.ID == rightEvalBlock.ID {
		return pat, false
	}
	shortGoto, ok := shortBlock.Term.(*mir.GotoTerm)
	if !ok {
		return pat, false
	}
	rightGoto, ok := rightEvalBlock.Term.(*mir.GotoTerm)
	if !ok {
		return pat, false
	}
	if shortGoto.Target != rightGoto.Target {
		return pat, false
	}
	orMergeBlock := blockByID(fn, shortGoto.Target)
	if orMergeBlock == nil || len(orMergeBlock.Instrs) != 0 {
		return pat, false
	}
	orMergeBranch, ok := orMergeBlock.Term.(*mir.BranchTerm)
	if !ok {
		return pat, false
	}

	// short_true must contain a single AssignInstr writing `true` to the merged Bool local.
	if len(shortBlock.Instrs) != 1 {
		return pat, false
	}
	shortAi, ok := shortBlock.Instrs[0].(*mir.AssignInstr)
	if !ok || shortAi.Dest.HasProjections() {
		return pat, false
	}
	shortUse, ok := shortAi.Src.(*mir.UseRV)
	if !ok {
		return pat, false
	}
	shortConst, ok := shortUse.Op.(*mir.ConstOp)
	if !ok {
		return pat, false
	}
	shortBool, ok := shortConst.Const.(*mir.BoolConst)
	if !ok || !shortBool.Value {
		return pat, false
	}
	mergedLocal := shortAi.Dest.Local
	mergedRec := lookupLocal(fn, mergedLocal)
	if mergedRec == nil || scalarFromType(mergedRec.Type) != scalarBool {
		return pat, false
	}

	// orMerge.Cond must reference mergedLocal (no projections).
	orCond, ok := orMergeBranch.Cond.(*mir.CopyOp)
	if !ok || orCond.Place.HasProjections() || orCond.Place.Local != mergedLocal {
		return pat, false
	}

	// Walk entry block's left-cond instructions.
	entryEmit, leftCondExpr, leftCondTy, ok := classifyEntryBlock(fn, entryBlock, entryBindings, mctx, &nextSSA)
	if !ok || leftCondTy != scalarBool {
		return pat, false
	}
	pat.entry = entryEmit
	pat.entry.label = "entry"
	pat.leftCondExpr = leftCondExpr
	pat.leftCondType = leftCondTy.llvm()

	// Walk right-eval block to compute the right-side Bool.
	rightBindings := copyBindings(entryBindings)
	rightEmit := blockEmit{}
	for _, instr := range rightEvalBlock.Instrs {
		if !applyStep(fn, instr, rightBindings, mctx, &nextSSA, &rightEmit) {
			return pat, false
		}
	}
	rightBinding, ok := rightBindings[mergedLocal]
	if !ok || !rightBinding.defined || rightBinding.ty != scalarBool {
		return pat, false
	}
	pat.rightEvalEmit = rightEmit
	pat.rightCondExpr = rightBinding.expr
	pat.orShortLabel = blockLabelName(shortBlock.ID, "or_short")
	pat.orRightLabel = blockLabelName(rightEvalBlock.ID, "or_right")
	pat.orMergeLabel = blockLabelName(orMergeBlock.ID, "or_merge")
	pat.rightEvalEmit.label = pat.orRightLabel

	// arm 0 = orMerge.Then aggregate arm.
	arm0Block := blockByID(fn, orMergeBranch.Then)
	if arm0Block == nil {
		return pat, false
	}
	arm0Fields, arm0Start, arm0Reg, ok := classifyChainAggregateArm(fn, arm0Block, returnBlock, fieldTypes, &nextSSA, mctx)
	if !ok {
		return pat, false
	}
	arm0Label := blockLabelName(arm0Block.ID, "arm0")
	pat.arms = append(pat.arms, elseIfArm{
		label:      arm0Label,
		fieldExprs: arm0Fields,
		startSSA:   arm0Start,
		resultExpr: arm0Reg,
	})

	// orMerge acts as the "entry rung" — its arm-label is arm0Label, its
	// else target is the next-rung-or-final block.
	pat.rungs = append(pat.rungs, elseIfRung{
		condEmit: blockEmit{label: pat.orMergeLabel},
		condExpr: "%or",
		condType: "i1",
		armLabel: arm0Label,
	})

	// Walk the rest of the chain (rung 1..N or final-else aggregate).
	current := blockByID(fn, orMergeBranch.Else)
	rungIndex := 1
	for {
		if current == nil {
			return pat, false
		}
		// If current is an aggregate-arm block, treat as final fallback.
		if _, ok := current.Term.(*mir.BranchTerm); !ok {
			armFields, armStart, armReg, ok := classifyChainAggregateArm(fn, current, returnBlock, fieldTypes, &nextSSA, mctx)
			if !ok {
				return pat, false
			}
			pat.arms = append(pat.arms, elseIfArm{
				label:      blockLabelName(current.ID, "fallback"),
				fieldExprs: armFields,
				startSSA:   armStart,
				resultExpr: armReg,
			})
			break
		}
		// Otherwise current is another cond rung.
		branch, _ := current.Term.(*mir.BranchTerm)
		matchedBlock := blockByID(fn, branch.Then)
		nextOrElse := blockByID(fn, branch.Else)
		if matchedBlock == nil || nextOrElse == nil || matchedBlock.ID == nextOrElse.ID {
			return pat, false
		}
		condBindings := copyBindings(entryBindings)
		condEmit := blockEmit{}
		for _, instr := range current.Instrs {
			if !applyStep(fn, instr, condBindings, mctx, &nextSSA, &condEmit) {
				return pat, false
			}
		}
		condExpr, condTy, ok := resolveOperand(branch.Cond, condBindings, mctx)
		if !ok || condTy != scalarBool {
			return pat, false
		}
		armFields, armStart, armReg, ok := classifyChainAggregateArm(fn, matchedBlock, returnBlock, fieldTypes, &nextSSA, mctx)
		if !ok {
			return pat, false
		}
		armLabel := blockLabelName(matchedBlock.ID, fmt.Sprintf("arm%d", rungIndex))
		condEmit.label = blockLabelName(current.ID, fmt.Sprintf("rung%d", rungIndex))
		pat.rungs = append(pat.rungs, elseIfRung{
			condEmit: condEmit,
			condExpr: condExpr,
			condType: condTy.llvm(),
			armLabel: armLabel,
		})
		pat.arms = append(pat.arms, elseIfArm{
			label:      armLabel,
			fieldExprs: armFields,
			startSSA:   armStart,
			resultExpr: armReg,
		})
		rungIndex++
		current = nextOrElse
	}

	if len(pat.arms) != len(pat.rungs)+1 {
		return pat, false
	}
	pat.mergeLabel = blockLabelName(returnBlock.ID, "merge")
	return pat, true
}

func emitOrChainAggregate(out *strings.Builder, fn *mir.Function, pat orChainPattern, mctx *moduleCtx) error {
	mctx.emitStructDef(pat.typeName, pat.fieldTypes)
	fmt.Fprintf(out, "define %%%s @%s(", pat.typeName, fn.Name)
	for i, name := range pat.paramNames {
		if i > 0 {
			out.WriteString(", ")
		}
		fmt.Fprintf(out, "%s %%%s", pat.paramTypes[i].llvm(), name)
	}
	out.WriteString(") {\n")

	// entry: leftCond + branch(short_true / right_eval).
	emitBlock(out, pat.entry)
	fmt.Fprintf(out, "  br %s %s, label %%%s, label %%%s\n", pat.leftCondType, pat.leftCondExpr, pat.orShortLabel, pat.orRightLabel)
	out.WriteString("\n")

	// or_short: just goto or_merge.
	fmt.Fprintf(out, "%s:\n", pat.orShortLabel)
	fmt.Fprintf(out, "  br label %%%s\n", pat.orMergeLabel)
	out.WriteString("\n")

	// or_right: right-cond instructions + goto or_merge.
	emitBlock(out, pat.rightEvalEmit)
	fmt.Fprintf(out, "  br label %%%s\n", pat.orMergeLabel)
	out.WriteString("\n")

	// or_merge: phi i1 + branch to arm0 / next rung label.
	fmt.Fprintf(out, "%s:\n", pat.orMergeLabel)
	fmt.Fprintf(out, "  %%or = phi i1 [ true, %%%s ], [ %s, %%%s ]\n", pat.orShortLabel, pat.rightCondExpr, pat.orRightLabel)
	// First rung's else target = next rung's cond label or final fallback.
	var firstElseLabel string
	if len(pat.rungs) > 1 {
		firstElseLabel = pat.rungs[1].condEmit.label
	} else {
		firstElseLabel = pat.arms[len(pat.arms)-1].label
	}
	fmt.Fprintf(out, "  br i1 %%or, label %%%s, label %%%s\n", pat.rungs[0].armLabel, firstElseLabel)
	out.WriteString("\n")

	// Subsequent rungs.
	for i := 1; i < len(pat.rungs); i++ {
		emitBlock(out, pat.rungs[i].condEmit)
		var elseLabel string
		if i+1 < len(pat.rungs) {
			elseLabel = pat.rungs[i+1].condEmit.label
		} else {
			elseLabel = pat.arms[len(pat.arms)-1].label
		}
		fmt.Fprintf(out, "  br %s %s, label %%%s, label %%%s\n", pat.rungs[i].condType, pat.rungs[i].condExpr, pat.rungs[i].armLabel, elseLabel)
		out.WriteString("\n")
	}

	// Arms.
	for _, arm := range pat.arms {
		fmt.Fprintf(out, "%s:\n", arm.label)
		prev := "poison"
		for j, fieldExpr := range arm.fieldExprs {
			reg := fmt.Sprintf("%%%d", arm.startSSA+j)
			fmt.Fprintf(out, "  %s = insertvalue %%%s %s, %s %s, %d\n", reg, pat.typeName, prev, pat.fieldTypes[j].llvm(), fieldExpr, j)
			prev = reg
		}
		fmt.Fprintf(out, "  br label %%%s\n", pat.mergeLabel)
		out.WriteString("\n")
	}

	// Merge: phi over all arms + ret.
	fmt.Fprintf(out, "%s:\n", pat.mergeLabel)
	out.WriteString("  %retval = phi %" + pat.typeName)
	for i, arm := range pat.arms {
		if i > 0 {
			out.WriteString(",")
		}
		fmt.Fprintf(out, " [ %s, %%%s ]", arm.resultExpr, arm.label)
	}
	out.WriteString("\n")
	fmt.Fprintf(out, "  ret %%%s %%retval\n", pat.typeName)
	out.WriteString("}\n\n")
	return nil
}

// classifyEntryBlock walks the entry block of an if-else: zero or more
// AssignInstr/CallInstr followed by a BranchTerm whose Cond is a
// resolvable Bool operand. Returns the resolved condition expression
// + scalar type so the caller can emit the `br`.
func classifyEntryBlock(fn *mir.Function, bb *mir.BasicBlock, bindings map[mir.LocalID]localBinding, mctx *moduleCtx, nextSSA *int) (blockEmit, string, scalarType, bool) {
	emit := blockEmit{label: "entry"}
	for _, instr := range bb.Instrs {
		if !applyStep(fn, instr, bindings, mctx, nextSSA, &emit) {
			return blockEmit{}, "", scalarUnknown, false
		}
	}
	branch, ok := bb.Term.(*mir.BranchTerm)
	if !ok {
		return blockEmit{}, "", scalarUnknown, false
	}
	condExpr, condTy, ok := resolveOperand(branch.Cond, bindings, mctx)
	if !ok {
		return blockEmit{}, "", scalarUnknown, false
	}
	return emit, condExpr, condTy, true
}

// classifyBranchBlock walks a `then` / `else` block: zero or more
// AssignInstr/CallInstr followed by GotoTerm. The block's last
// AssignInstr to the return local provides the value contributed to
// the merge phi; the helper returns that value's LLVM expression.
func classifyBranchBlock(fn *mir.Function, bb *mir.BasicBlock, bindings map[mir.LocalID]localBinding, mctx *moduleCtx, nextSSA *int, retLocal mir.LocalID, retType scalarType) (blockEmit, string, bool) {
	emit := blockEmit{}
	for _, instr := range bb.Instrs {
		if !applyStep(fn, instr, bindings, mctx, nextSSA, &emit) {
			return blockEmit{}, "", false
		}
	}
	if _, ok := bb.Term.(*mir.GotoTerm); !ok {
		return blockEmit{}, "", false
	}
	retBinding, ok := bindings[retLocal]
	if !ok || !retBinding.defined || retBinding.ty != retType {
		return blockEmit{}, "", false
	}
	return emit, retBinding.expr, true
}

// applyStep advances one MIR instruction inside a block during P3c
// matching: it threads the binding map + SSA counter and appends a
// pending entry to the block emit when the instruction needs an LLVM
// line.
func applyStep(fn *mir.Function, instr mir.Instr, bindings map[mir.LocalID]localBinding, mctx *moduleCtx, nextSSA *int, emit *blockEmit) bool {
	var (
		pending  pendingInstr
		expr     string
		destID   mir.LocalID
		destType scalarType
		okStep   bool
	)
	switch step := instr.(type) {
	case *mir.AssignInstr:
		pending, expr, destID, destType, okStep = classifyAssignStep(fn, step, bindings, mctx)
	case *mir.CallInstr:
		pending, destID, destType, okStep = classifyCallStep(fn, step, bindings, mctx)
	case *mir.IntrinsicInstr:
		if step.Dest == nil {
			line, okIntr := classifyIntrinsicLine(fn, step, bindings, mctx)
			if !okIntr {
				return false
			}
			emit.pending = append(emit.pending, pendingInstr{kind: instrIntrinsic, intrinsicLine: line})
			return true
		}
		pending, destID, destType, okStep = classifyIntrinsicValueStep(fn, step, bindings, mctx)
	case *mir.StorageLiveInstr, *mir.StorageDeadInstr:
		// Storage liveness markers are opt-in metadata; stage0
		// has nothing to emit for them.
		return true
	default:
		return false
	}
	if !okStep {
		return false
	}
	if existing, found := bindings[destID]; found && existing.defined {
		return false
	}
	pending.destLocal = destID
	pending.resultType = destType
	switch pending.kind {
	case instrBinary, instrCall, instrListLiteral:
		reg := fmt.Sprintf("%%%d", *nextSSA)
		*nextSSA++
		pending.binDestReg = reg
		bindings[destID] = localBinding{expr: reg, ty: destType, defined: true}
	case instrCallChain:
		regs := make([]string, len(pending.callArgs)-1)
		for i := range regs {
			regs[i] = fmt.Sprintf("%%%d", *nextSSA)
			*nextSSA++
		}
		pending.chainRegs = regs
		bindings[destID] = localBinding{expr: regs[len(regs)-1], ty: destType, defined: true}
	default:
		bindings[destID] = localBinding{expr: expr, ty: destType, defined: true}
	}
	emit.pending = append(emit.pending, pending)
	return true
}

func blockByID(fn *mir.Function, id mir.BlockID) *mir.BasicBlock {
	for _, bb := range fn.Blocks {
		if bb != nil && bb.ID == id {
			return bb
		}
	}
	return nil
}

func blockLabelName(id mir.BlockID, prefix string) string {
	return fmt.Sprintf("%s.%d", prefix, id)
}

func copyBindings(src map[mir.LocalID]localBinding) map[mir.LocalID]localBinding {
	dst := make(map[mir.LocalID]localBinding, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// ---- shared helpers ----

func singleBlockReturning(fn *mir.Function) (*mir.BasicBlock, bool) {
	if len(fn.Blocks) != 1 {
		return nil, false
	}
	bb := fn.Blocks[0]
	if bb == nil {
		return nil, false
	}
	if _, ok := bb.Term.(*mir.ReturnTerm); !ok {
		return nil, false
	}
	return bb, true
}

func lookupLocal(fn *mir.Function, id mir.LocalID) *mir.Local {
	for _, l := range fn.Locals {
		if l != nil && l.ID == id {
			return l
		}
	}
	return nil
}

// sanitizeLLVMName returns `name` if it is a non-empty valid LLVM
// identifier (`[A-Za-z._][A-Za-z._0-9]*`), otherwise `fallback`.
func sanitizeLLVMName(name, fallback string) string {
	if name == "" {
		return fallback
	}
	for i, r := range name {
		if i == 0 {
			if !isLLVMIdentStart(r) {
				return fallback
			}
			continue
		}
		if !isLLVMIdentRest(r) {
			return fallback
		}
	}
	return name
}

func isLLVMIdentStart(r rune) bool {
	switch {
	case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z':
		return true
	case r == '_' || r == '.':
		return true
	}
	return false
}

func isLLVMIdentRest(r rune) bool {
	if isLLVMIdentStart(r) {
		return true
	}
	return r >= '0' && r <= '9'
}

func isPrimType(t mir.Type, want ir.PrimKind) bool {
	prim, ok := t.(*ir.PrimType)
	if !ok || prim == nil {
		return false
	}
	return prim.Kind == want
}

func packageNameFor(module *mir.Module, opts llvmabi.Options) string {
	if module != nil && module.Package != "" {
		return module.Package
	}
	if opts.PackageName != "" {
		return opts.PackageName
	}
	return "main"
}

// ---- P6: while-loop with stack-allocated mutable locals ----
//
// stage0 P6 introduces multi-block MIR with a back-edge — the canonical
// 4-block while-loop shape produced by the front-end:
//
//	entry  : pre-loop instructions + GotoTerm(header)
//	header : cond computation + BranchTerm(cond, body, exit)
//	body   : loop body + GotoTerm(header)            ← back-edge
//	exit   : post-loop + AssignInstr(ret) + ReturnTerm
//
// To handle locals that are reassigned across iterations (the loop
// counter, accumulators), stage0 lowers each `mir.Local` with
// `Mut == true` (and that is neither a parameter nor the return
// local) to an LLVM alloca slot in the function entry. Reads emit
// `load`, writes emit `store`. The LLVM mem2reg pass converts these
// back to SSA + phi at -O1+, so the bootstrap output stays compact
// after optimisation despite the verbose emit shape.

type whileLoopPattern struct {
	retType    scalarType
	paramIDs   []mir.LocalID
	paramTypes []scalarType
	paramNames []string
	stackDecls []stackDecl
	// Pre-rendered block bodies (without label line, without terminator).
	entryBody  string
	headerBody string
	bodyBody   string
	exitBody   string
	// Header condition LLVM expression (already loaded if stack-backed).
	headerCondExpr string
	// Final return expression (the value emitted to `ret`).
	finalRetExpr string
	headerLabel  string
	bodyLabel    string
	exitLabel    string
}

type stackDecl struct {
	id   mir.LocalID
	name string // SSA-style register name without leading %
	ty   scalarType
}

func matchWhileLoopReturn(fn *mir.Function, mctx *moduleCtx) (whileLoopPattern, bool) {
	pat := whileLoopPattern{}
	pat.retType = scalarFromType(fn.ReturnType)
	if pat.retType == scalarUnknown {
		return pat, false
	}
	if len(fn.Params) > 2 {
		return pat, false
	}
	if len(fn.Blocks) != 4 {
		return pat, false
	}

	// Param SSA registers.
	pat.paramIDs = fn.Params
	pat.paramTypes = make([]scalarType, len(fn.Params))
	pat.paramNames = make([]string, len(fn.Params))
	fallbackNames := []string{"a", "b"}
	for i, pid := range fn.Params {
		loc := lookupLocal(fn, pid)
		if loc == nil || !loc.IsParam {
			return pat, false
		}
		pt := scalarFromType(loc.Type)
		if pt == scalarUnknown {
			return pat, false
		}
		pat.paramTypes[i] = pt
		pat.paramNames[i] = sanitizeLLVMName(loc.Name, fallbackNames[i])
	}
	disambiguateParamNames(pat.paramNames)

	// Block layout: entry → header → (body | exit).
	entry := blockByID(fn, fn.Entry)
	if entry == nil {
		return pat, false
	}
	entryGoto, ok := entry.Term.(*mir.GotoTerm)
	if !ok {
		return pat, false
	}
	header := blockByID(fn, entryGoto.Target)
	if header == nil || header.ID == entry.ID {
		return pat, false
	}
	branch, ok := header.Term.(*mir.BranchTerm)
	if !ok {
		return pat, false
	}
	body := blockByID(fn, branch.Then)
	exit := blockByID(fn, branch.Else)
	if body == nil || exit == nil {
		return pat, false
	}
	if body.ID == exit.ID || body.ID == entry.ID || body.ID == header.ID || exit.ID == header.ID || exit.ID == entry.ID {
		return pat, false
	}
	bodyGoto, ok := body.Term.(*mir.GotoTerm)
	if !ok || bodyGoto.Target != header.ID {
		return pat, false
	}
	if _, ok := exit.Term.(*mir.ReturnTerm); !ok {
		return pat, false
	}

	// Discover stack-allocated locals (Mut and not param/return).
	stack := map[mir.LocalID]stackDecl{}
	for _, l := range fn.Locals {
		if l == nil {
			continue
		}
		if l.IsParam || l.IsReturn {
			continue
		}
		if !l.Mut {
			continue
		}
		ty := scalarFromType(l.Type)
		if ty == scalarUnknown {
			return pat, false
		}
		decl := stackDecl{id: l.ID, name: sanitizeLLVMName(l.Name, fmt.Sprintf("local%d", l.ID)) + ".slot", ty: ty}
		stack[l.ID] = decl
		pat.stackDecls = append(pat.stackDecls, decl)
	}

	// Bindings shared across blocks. Param + stack locals seeded;
	// SSA-bound (immutable) locals get added as their AssignInstrs
	// are walked.
	bindings := map[mir.LocalID]localBinding{}
	for i, pid := range fn.Params {
		bindings[pid] = localBinding{
			expr:    "%" + pat.paramNames[i],
			ty:      pat.paramTypes[i],
			defined: true,
		}
	}
	for _, sd := range pat.stackDecls {
		bindings[sd.id] = localBinding{
			expr:    "%" + sd.name,
			ty:      sd.ty,
			defined: true,
			// stack-allocated; reads must `load` first.
			isStack: true,
		}
	}

	nextSSA := 0
	emitCtx := &whileLoopEmitCtx{
		fn:       fn,
		bindings: bindings,
		stack:    stack,
		mctx:     mctx,
		nextSSA:  &nextSSA,
	}

	if body, ok := emitWhileBlock(emitCtx, entry, false); ok {
		pat.entryBody = body
	} else {
		return pat, false
	}
	headerCondExpr, headerBody, ok := emitWhileHeader(emitCtx, header, branch.Cond)
	if !ok {
		return pat, false
	}
	pat.headerBody = headerBody
	pat.headerCondExpr = headerCondExpr
	if body, ok := emitWhileBlock(emitCtx, body, false); ok {
		pat.bodyBody = body
	} else {
		return pat, false
	}
	if exitBody, finalExpr, ok := emitWhileExit(emitCtx, exit, fn.ReturnLocal, pat.retType); ok {
		pat.exitBody = exitBody
		pat.finalRetExpr = finalExpr
	} else {
		return pat, false
	}

	pat.headerLabel = blockLabelName(header.ID, "header")
	pat.bodyLabel = blockLabelName(body.ID, "body")
	pat.exitLabel = blockLabelName(exit.ID, "exit")
	return pat, true
}

type whileLoopEmitCtx struct {
	fn       *mir.Function
	bindings map[mir.LocalID]localBinding
	stack    map[mir.LocalID]stackDecl
	mctx     *moduleCtx
	nextSSA  *int
}

// emitWhileBlock walks an entry / body block and returns the rendered
// LLVM body (without the label line, without terminator). Returns
// false if any instruction declines.
func emitWhileBlock(ctx *whileLoopEmitCtx, bb *mir.BasicBlock, _ bool) (string, bool) {
	var out strings.Builder
	for _, instr := range bb.Instrs {
		if !emitWhileStep(ctx, &out, instr) {
			return "", false
		}
	}
	return out.String(), true
}

// emitWhileHeader renders the header block. The header has 0+
// AssignInstrs (typically the cond computation) followed by a
// BranchTerm whose Cond operand is resolved to an LLVM expression.
func emitWhileHeader(ctx *whileLoopEmitCtx, bb *mir.BasicBlock, cond mir.Operand) (string, string, bool) {
	var out strings.Builder
	for _, instr := range bb.Instrs {
		if !emitWhileStep(ctx, &out, instr) {
			return "", "", false
		}
	}
	expr, ty, ok := resolveOperandWithLoad(ctx, &out, cond)
	if !ok || ty != scalarBool {
		return "", "", false
	}
	return expr, out.String(), true
}

// emitWhileExit renders the exit block. The block ends with a
// ReturnTerm; the final value emitted to `ret <retType>` is the
// expression bound to the return local at the end.
func emitWhileExit(ctx *whileLoopEmitCtx, bb *mir.BasicBlock, retLocal mir.LocalID, retType scalarType) (string, string, bool) {
	var out strings.Builder
	for _, instr := range bb.Instrs {
		if !emitWhileStep(ctx, &out, instr) {
			return "", "", false
		}
	}
	binding, ok := ctx.bindings[retLocal]
	if !ok || !binding.defined {
		return "", "", false
	}
	if binding.ty != retType {
		return "", "", false
	}
	if binding.isStack {
		expr, _, okLoad := loadFromStack(ctx, &out, retLocal)
		if !okLoad {
			return "", "", false
		}
		return out.String(), expr, true
	}
	return out.String(), binding.expr, true
}

// emitWhileStep handles one MIR instruction in a while-loop block.
// AssignInstrs are rendered with stack-aware reads / writes; storage
// markers are skipped.
func emitWhileStep(ctx *whileLoopEmitCtx, out *strings.Builder, instr mir.Instr) bool {
	switch step := instr.(type) {
	case *mir.AssignInstr:
		return emitWhileAssign(ctx, out, step)
	case *mir.CallInstr:
		return emitWhileCall(ctx, out, step)
	case *mir.IntrinsicInstr:
		return emitWhileIntrinsic(ctx, out, step)
	case *mir.StorageLiveInstr, *mir.StorageDeadInstr:
		return true
	}
	return false
}

// emitWhileIntrinsic handles the small set of intrinsics stage0
// understands inside any block (entry / header / body / post / exit).
// Currently the only supported intrinsic is `IntrinsicPrintln` with a
// single Int-typed argument; everything else declines.
func emitWhileIntrinsic(ctx *whileLoopEmitCtx, out *strings.Builder, ii *mir.IntrinsicInstr) bool {
	if ii.Dest != nil {
		// Stage0 only handles intrinsics whose result is unit
		// (no destination). Println / abort / etc. fit this shape.
		return false
	}
	switch ii.Kind {
	case mir.IntrinsicPrintln:
		if len(ii.Args) != 1 {
			return false
		}
		expr, ty, ok := resolveOperandWithLoad(ctx, out, ii.Args[0])
		if !ok {
			return false
		}
		switch ty {
		case scalarInt:
			fmt.Fprintf(out, "  call i32 (ptr, ...) @printf(ptr @.fmt.stage0.println.int, i64 %s)\n", expr)
			return true
		case scalarString:
			fmt.Fprintf(out, "  call i32 (ptr, ...) @printf(ptr @.fmt.stage0.println.str, ptr %s)\n", expr)
			return true
		}
		return false
	}
	return false
}

func emitWhileAssign(ctx *whileLoopEmitCtx, out *strings.Builder, ai *mir.AssignInstr) bool {
	if ai.Dest.HasProjections() {
		return false
	}
	destID := ai.Dest.Local
	destLocal := lookupLocal(ctx.fn, destID)
	if destLocal == nil {
		return false
	}
	destType := scalarFromType(destLocal.Type)
	if destType == scalarUnknown {
		return false
	}

	// Stage0 distinguishes stack-backed dests (alloca slot) from
	// SSA-bound dests. Stack writes emit `store`; SSA writes bind
	// the local to a fresh expression.
	isStackDest := false
	if _, ok := ctx.stack[destID]; ok {
		isStackDest = true
	}

	// Resolve src.
	var rhsExpr string
	var rhsTy scalarType
	switch src := ai.Src.(type) {
	case *mir.UseRV:
		expr, ty, ok := resolveOperandWithLoad(ctx, out, src.Op)
		if !ok || ty != destType {
			return false
		}
		rhsExpr = expr
		rhsTy = ty
	case *mir.BinaryRV:
		llvmOp, resultType, operandType := classifyBinary(src.Op)
		if llvmOp == "" || resultType != destType {
			return false
		}
		left, leftTy, ok := resolveOperandWithLoad(ctx, out, src.Left)
		if !ok || leftTy != operandType {
			return false
		}
		right, rightTy, ok := resolveOperandWithLoad(ctx, out, src.Right)
		if !ok || rightTy != operandType {
			return false
		}
		reg := freshReg(ctx)
		fmt.Fprintf(out, "  %s = %s %s %s, %s\n", reg, llvmOp, operandType.llvm(), left, right)
		rhsExpr = reg
		rhsTy = resultType
	default:
		return false
	}
	_ = rhsTy

	if isStackDest {
		fmt.Fprintf(out, "  store %s %s, ptr %%%s\n", destType.llvm(), rhsExpr, ctx.stack[destID].name)
		// stack binding stays the same (always loaded fresh).
		return true
	}
	// SSA dest: bind expression. If RHS is a constant literal /
	// param register, bind directly. If RHS is a fresh reg, that's
	// already its expression.
	if existing, found := ctx.bindings[destID]; found && existing.defined && !existing.isStack {
		// Disallow SSA reassignment.
		return false
	}
	ctx.bindings[destID] = localBinding{expr: rhsExpr, ty: destType, defined: true}
	return true
}

func emitWhileCall(ctx *whileLoopEmitCtx, out *strings.Builder, ci *mir.CallInstr) bool {
	if ci.Dest == nil || ci.Dest.HasProjections() {
		return false
	}
	destID := ci.Dest.Local
	destLocal := lookupLocal(ctx.fn, destID)
	if destLocal == nil {
		return false
	}
	destType := scalarFromType(destLocal.Type)
	if destType == scalarUnknown {
		return false
	}
	ref, ok := ci.Callee.(*mir.FnRef)
	if !ok || ref.Symbol == "" || !ctx.mctx.knownSymbols[ref.Symbol] {
		return false
	}
	fnTy, ok := ref.Type.(*ir.FnType)
	if !ok || fnTy == nil {
		return false
	}
	if scalarFromType(fnTy.Return) != destType {
		return false
	}
	if len(fnTy.Params) != len(ci.Args) {
		return false
	}
	args := make([]callArg, 0, len(ci.Args))
	for i, op := range ci.Args {
		expr, ty, okOp := resolveOperandWithLoad(ctx, out, op)
		if !okOp {
			return false
		}
		paramTy := scalarFromType(fnTy.Params[i])
		if paramTy == scalarUnknown || paramTy != ty {
			return false
		}
		args = append(args, callArg{expr: expr, ty: ty.llvm()})
	}
	reg := freshReg(ctx)
	fmt.Fprintf(out, "  %s = call %s @%s(", reg, destType.llvm(), ref.Symbol)
	for i, a := range args {
		if i > 0 {
			out.WriteString(", ")
		}
		fmt.Fprintf(out, "%s %s", a.ty, a.expr)
	}
	out.WriteString(")\n")
	if _, isStack := ctx.stack[destID]; isStack {
		fmt.Fprintf(out, "  store %s %s, ptr %%%s\n", destType.llvm(), reg, ctx.stack[destID].name)
		return true
	}
	if existing, found := ctx.bindings[destID]; found && existing.defined && !existing.isStack {
		return false
	}
	ctx.bindings[destID] = localBinding{expr: reg, ty: destType, defined: true}
	return true
}

// resolveOperandWithLoad is the while-loop counterpart of
// resolveOperand: it understands stack-backed locals and emits a load
// on demand. The SSA register that holds the load result becomes the
// returned expression.
func resolveOperandWithLoad(ctx *whileLoopEmitCtx, out *strings.Builder, op mir.Operand) (string, scalarType, bool) {
	if con, ok := op.(*mir.ConstOp); ok {
		switch c := con.Const.(type) {
		case *mir.IntConst:
			return fmt.Sprintf("%d", c.Value), scalarInt, true
		case *mir.BoolConst:
			if c.Value {
				return "true", scalarBool, true
			}
			return "false", scalarBool, true
		case *mir.StringConst:
			if ctx == nil || ctx.mctx == nil {
				return "", scalarUnknown, false
			}
			return ctx.mctx.internStringConst(c.Value), scalarString, true
		}
		return "", scalarUnknown, false
	}
	if cp, ok := op.(*mir.CopyOp); ok {
		if cp.Place.HasProjections() {
			return "", scalarUnknown, false
		}
		b, found := ctx.bindings[cp.Place.Local]
		if !found || !b.defined {
			return "", scalarUnknown, false
		}
		if b.isStack {
			return loadFromStack(ctx, out, cp.Place.Local)
		}
		return b.expr, b.ty, true
	}
	return "", scalarUnknown, false
}

func loadFromStack(ctx *whileLoopEmitCtx, out *strings.Builder, id mir.LocalID) (string, scalarType, bool) {
	sd, ok := ctx.stack[id]
	if !ok {
		return "", scalarUnknown, false
	}
	reg := freshReg(ctx)
	fmt.Fprintf(out, "  %s = load %s, ptr %%%s\n", reg, sd.ty.llvm(), sd.name)
	return reg, sd.ty, true
}

func freshReg(ctx *whileLoopEmitCtx) string {
	reg := fmt.Sprintf("%%%d", *ctx.nextSSA)
	*ctx.nextSSA++
	return reg
}

func emitWhileLoopReturn(out *strings.Builder, fn *mir.Function, pat whileLoopPattern) error {
	retLLVM := pat.retType.llvm()
	fmt.Fprintf(out, "define %s @%s(", retLLVM, fn.Name)
	for i, name := range pat.paramNames {
		if i > 0 {
			out.WriteString(", ")
		}
		fmt.Fprintf(out, "%s %%%s", pat.paramTypes[i].llvm(), name)
	}
	out.WriteString(") {\n")

	// Entry: alloca declarations + entry body + branch to header.
	out.WriteString("entry:\n")
	for _, sd := range pat.stackDecls {
		fmt.Fprintf(out, "  %%%s = alloca %s\n", sd.name, sd.ty.llvm())
	}
	out.WriteString(pat.entryBody)
	fmt.Fprintf(out, "  br label %%%s\n", pat.headerLabel)

	// Header.
	out.WriteString("\n")
	fmt.Fprintf(out, "%s:\n", pat.headerLabel)
	out.WriteString(pat.headerBody)
	fmt.Fprintf(out, "  br i1 %s, label %%%s, label %%%s\n", pat.headerCondExpr, pat.bodyLabel, pat.exitLabel)

	// Body (back-edge to header).
	out.WriteString("\n")
	fmt.Fprintf(out, "%s:\n", pat.bodyLabel)
	out.WriteString(pat.bodyBody)
	fmt.Fprintf(out, "  br label %%%s\n", pat.headerLabel)

	// Exit.
	out.WriteString("\n")
	fmt.Fprintf(out, "%s:\n", pat.exitLabel)
	out.WriteString(pat.exitBody)
	fmt.Fprintf(out, "  ret %s %s\n", retLLVM, pat.finalRetExpr)
	out.WriteString("}\n\n")
	return nil
}

// ---- P7: for-in-range loop (5-block shape) ----
//
// The front-end lowers `for i in 0..n { body }` to a 5-block CFG:
//
//	entry    : pre-loop init (acc/i/_end) + GotoTerm(header)
//	header   : cond = i < _end + BranchTerm(cond, body, exit)
//	body     : loop body + GotoTerm(post)
//	post     : i = i + 1 + GotoTerm(header)         ← back-edge
//	exit     : post-loop instructions + ReturnTerm
//
// Stage0 P7 reuses the alloca/store/load machinery introduced in P6;
// all mutable locals (the iterator + any user mut-bindings touched by
// the body) become alloca slots. The pattern differs from `while`
// only in the extra `post` block sitting between body and back-edge.

type forInRangePattern struct {
	retType    scalarType
	paramIDs   []mir.LocalID
	paramTypes []scalarType
	paramNames []string
	stackDecls []stackDecl

	entryBody  string
	headerBody string
	bodyBody   string
	postBody   string
	exitBody   string

	headerCondExpr string
	finalRetExpr   string

	headerLabel string
	bodyLabel   string
	postLabel   string
	exitLabel   string
}

func matchForInRangeReturn(fn *mir.Function, mctx *moduleCtx) (forInRangePattern, bool) {
	pat := forInRangePattern{}
	pat.retType = scalarFromType(fn.ReturnType)
	if pat.retType == scalarUnknown {
		return pat, false
	}
	if len(fn.Params) > 2 {
		return pat, false
	}
	if len(fn.Blocks) != 5 {
		return pat, false
	}

	// Param SSA registers.
	pat.paramIDs = fn.Params
	pat.paramTypes = make([]scalarType, len(fn.Params))
	pat.paramNames = make([]string, len(fn.Params))
	fallbackNames := []string{"a", "b"}
	for i, pid := range fn.Params {
		loc := lookupLocal(fn, pid)
		if loc == nil || !loc.IsParam {
			return pat, false
		}
		pt := scalarFromType(loc.Type)
		if pt == scalarUnknown {
			return pat, false
		}
		pat.paramTypes[i] = pt
		pat.paramNames[i] = sanitizeLLVMName(loc.Name, fallbackNames[i])
	}
	disambiguateParamNames(pat.paramNames)

	// Block layout: entry → header → (body → post → header | exit).
	entry := blockByID(fn, fn.Entry)
	if entry == nil {
		return pat, false
	}
	entryGoto, ok := entry.Term.(*mir.GotoTerm)
	if !ok {
		return pat, false
	}
	header := blockByID(fn, entryGoto.Target)
	if header == nil || header.ID == entry.ID {
		return pat, false
	}
	branch, ok := header.Term.(*mir.BranchTerm)
	if !ok {
		return pat, false
	}
	body := blockByID(fn, branch.Then)
	exit := blockByID(fn, branch.Else)
	if body == nil || exit == nil {
		return pat, false
	}
	bodyGoto, ok := body.Term.(*mir.GotoTerm)
	if !ok {
		return pat, false
	}
	post := blockByID(fn, bodyGoto.Target)
	if post == nil {
		return pat, false
	}
	if post.ID == header.ID || post.ID == body.ID || post.ID == exit.ID || post.ID == entry.ID {
		return pat, false
	}
	postGoto, ok := post.Term.(*mir.GotoTerm)
	if !ok || postGoto.Target != header.ID {
		return pat, false
	}
	if _, ok := exit.Term.(*mir.ReturnTerm); !ok {
		return pat, false
	}

	// Stack-allocate every Mut local that isn't param/return.
	stack := map[mir.LocalID]stackDecl{}
	for _, l := range fn.Locals {
		if l == nil || l.IsParam || l.IsReturn || !l.Mut {
			continue
		}
		ty := scalarFromType(l.Type)
		if ty == scalarUnknown {
			return pat, false
		}
		decl := stackDecl{id: l.ID, name: sanitizeLLVMName(l.Name, fmt.Sprintf("local%d", l.ID)) + ".slot", ty: ty}
		stack[l.ID] = decl
		pat.stackDecls = append(pat.stackDecls, decl)
	}

	bindings := map[mir.LocalID]localBinding{}
	for i, pid := range fn.Params {
		bindings[pid] = localBinding{
			expr:    "%" + pat.paramNames[i],
			ty:      pat.paramTypes[i],
			defined: true,
		}
	}
	for _, sd := range pat.stackDecls {
		bindings[sd.id] = localBinding{
			expr:    "%" + sd.name,
			ty:      sd.ty,
			defined: true,
			isStack: true,
		}
	}

	nextSSA := 0
	ctx := &whileLoopEmitCtx{
		fn:       fn,
		bindings: bindings,
		stack:    stack,
		mctx:     mctx,
		nextSSA:  &nextSSA,
	}

	// entry
	if body, ok := emitWhileBlock(ctx, entry, false); ok {
		pat.entryBody = body
	} else {
		return pat, false
	}
	// header
	condExpr, headerBody, ok := emitWhileHeader(ctx, header, branch.Cond)
	if !ok {
		return pat, false
	}
	pat.headerBody = headerBody
	pat.headerCondExpr = condExpr
	// body
	if body, ok := emitWhileBlock(ctx, body, false); ok {
		pat.bodyBody = body
	} else {
		return pat, false
	}
	// post (increment)
	if post, ok := emitWhileBlock(ctx, post, false); ok {
		pat.postBody = post
	} else {
		return pat, false
	}
	// exit
	if exitBody, finalExpr, ok := emitWhileExit(ctx, exit, fn.ReturnLocal, pat.retType); ok {
		pat.exitBody = exitBody
		pat.finalRetExpr = finalExpr
	} else {
		return pat, false
	}

	pat.headerLabel = blockLabelName(header.ID, "header")
	pat.bodyLabel = blockLabelName(body.ID, "body")
	pat.postLabel = blockLabelName(post.ID, "post")
	pat.exitLabel = blockLabelName(exit.ID, "exit")
	return pat, true
}

func emitForInRangeReturn(out *strings.Builder, fn *mir.Function, pat forInRangePattern) error {
	retLLVM := pat.retType.llvm()
	fmt.Fprintf(out, "define %s @%s(", retLLVM, fn.Name)
	for i, name := range pat.paramNames {
		if i > 0 {
			out.WriteString(", ")
		}
		fmt.Fprintf(out, "%s %%%s", pat.paramTypes[i].llvm(), name)
	}
	out.WriteString(") {\n")

	out.WriteString("entry:\n")
	for _, sd := range pat.stackDecls {
		fmt.Fprintf(out, "  %%%s = alloca %s\n", sd.name, sd.ty.llvm())
	}
	out.WriteString(pat.entryBody)
	fmt.Fprintf(out, "  br label %%%s\n", pat.headerLabel)

	out.WriteString("\n")
	fmt.Fprintf(out, "%s:\n", pat.headerLabel)
	out.WriteString(pat.headerBody)
	fmt.Fprintf(out, "  br i1 %s, label %%%s, label %%%s\n", pat.headerCondExpr, pat.bodyLabel, pat.exitLabel)

	out.WriteString("\n")
	fmt.Fprintf(out, "%s:\n", pat.bodyLabel)
	out.WriteString(pat.bodyBody)
	fmt.Fprintf(out, "  br label %%%s\n", pat.postLabel)

	out.WriteString("\n")
	fmt.Fprintf(out, "%s:\n", pat.postLabel)
	out.WriteString(pat.postBody)
	fmt.Fprintf(out, "  br label %%%s\n", pat.headerLabel)

	out.WriteString("\n")
	fmt.Fprintf(out, "%s:\n", pat.exitLabel)
	out.WriteString(pat.exitBody)
	fmt.Fprintf(out, "  ret %s %s\n", retLLVM, pat.finalRetExpr)
	out.WriteString("}\n\n")
	return nil
}

// ---- P10: struct field accessor ----
//
// stage0 P10 handles a tightly-restricted "field reader" function:
//
//	fn name(p: SomeStruct) -> Int { p.<field> }
//
// MIR shape: 1 struct param (NamedType registered in
// `module.Layouts.Structs`), single block, one AssignInstr writing
// UseRV{CopyOp{Place: paramID, Projections: [FieldProj]}} to the
// return local, ReturnTerm. All struct fields must be scalar
// (Int / Bool / String) — anything outside that subset declines.
//
// Output:
//
//	%StructName = type { i64, i64, ... }
//
//	define i64 @name(%StructName %p) {
//	entry:
//	  %0 = extractvalue %StructName %p, <fieldIndex>
//	  ret i64 %0
//	}

type structFieldReadPattern struct {
	structName string
	fieldTypes []scalarType
	paramName  string
	fieldIndex int
	resultType scalarType
}

func matchStructFieldRead(fn *mir.Function, mctx *moduleCtx) (structFieldReadPattern, bool) {
	pat := structFieldReadPattern{}
	pat.resultType = scalarFromType(fn.ReturnType)
	if pat.resultType == scalarUnknown {
		return pat, false
	}
	if len(fn.Params) != 1 {
		return pat, false
	}
	paramID := fn.Params[0]
	paramLocal := lookupLocal(fn, paramID)
	if paramLocal == nil || !paramLocal.IsParam {
		return pat, false
	}
	named, ok := paramLocal.Type.(*ir.NamedType)
	if !ok || named == nil || named.Name == "" {
		return pat, false
	}
	fieldTypes, ok := mctx.lookupStructFields(named.Name)
	if !ok {
		return pat, false
	}
	bb, ok := singleBlockReturning(fn)
	if !ok || len(bb.Instrs) != 1 {
		return pat, false
	}
	ai, ok := bb.Instrs[0].(*mir.AssignInstr)
	if !ok {
		return pat, false
	}
	if ai.Dest.Local != fn.ReturnLocal || ai.Dest.HasProjections() {
		return pat, false
	}
	use, ok := ai.Src.(*mir.UseRV)
	if !ok {
		return pat, false
	}
	cp, ok := use.Op.(*mir.CopyOp)
	if !ok {
		return pat, false
	}
	if cp.Place.Local != paramID || len(cp.Place.Projections) != 1 {
		return pat, false
	}
	fieldProj, ok := cp.Place.Projections[0].(*mir.FieldProj)
	if !ok {
		return pat, false
	}
	if fieldProj.Index < 0 || fieldProj.Index >= len(fieldTypes) {
		return pat, false
	}
	if fieldTypes[fieldProj.Index] != pat.resultType {
		return pat, false
	}
	pat.structName = named.Name
	pat.fieldTypes = fieldTypes
	pat.paramName = sanitizeLLVMName(paramLocal.Name, "p")
	pat.fieldIndex = fieldProj.Index
	mctx.emitStructDef(named.Name, fieldTypes)
	return pat, true
}

func emitStructFieldRead(out *strings.Builder, fn *mir.Function, pat structFieldReadPattern) error {
	resultLLVM := pat.resultType.llvm()
	fmt.Fprintf(out, "define %s @%s(%%%s %%%s) {\n", resultLLVM, fn.Name, pat.structName, pat.paramName)
	out.WriteString("entry:\n")
	fmt.Fprintf(out, "  %%0 = extractvalue %%%s %%%s, %d\n", pat.structName, pat.paramName, pat.fieldIndex)
	fmt.Fprintf(out, "  ret %s %%0\n", resultLLVM)
	out.WriteString("}\n\n")
	return nil
}

// ---- P15: struct field binary-op return ----
//
// stage0 P15 handles the inlined "two-field arithmetic" shape produced
// by the front-end for code like:
//
//	fn pointSum(p: Point) -> Int { p.x + p.y }
//
// MIR shape:
//
//	fn name(p: Struct) -> T {
//	  bb0:
//	    AssignInstr ReturnLocal = BinaryRV(op,
//	        Copy(p.fieldI) | ConstOp,
//	        Copy(p.fieldJ) | ConstOp)
//	    ReturnTerm
//	}
//
// One struct param, all-scalar fields, single block, single AssignInstr
// to ReturnLocal whose Src is a BinaryRV. Each operand is independently
// either a single FieldProj on the struct param or a scalar ConstOp.
// Operator families: same as matchSequentialReturn (arithmetic,
// comparison, bitwise, shift, logical).
//
// Output:
//
//	define <retT> @<name>(%<Struct> %<paramName>) {
//	entry:
//	  %0 = extractvalue %<Struct> %<paramName>, <I>     ; only when needed
//	  %1 = extractvalue %<Struct> %<paramName>, <J>     ; only when needed
//	  %2 = <llvmOp> <opT> %0, %1
//	  ret <retT> %2
//	}

type p15FieldOperand struct {
	isField    bool
	fieldIndex int
	constText  string // formatted scalar literal when !isField
}

type structFieldBinaryOpPattern struct {
	structName  string
	fieldTypes  []scalarType
	paramName   string
	left        p15FieldOperand
	right       p15FieldOperand
	llvmOp      string
	resultType  scalarType
	operandType scalarType
}

func matchStructFieldBinaryOp(fn *mir.Function, mctx *moduleCtx) (structFieldBinaryOpPattern, bool) {
	pat := structFieldBinaryOpPattern{}
	pat.resultType = scalarFromType(fn.ReturnType)
	if pat.resultType == scalarUnknown {
		return pat, false
	}
	if len(fn.Params) != 1 {
		return pat, false
	}
	paramID := fn.Params[0]
	paramLocal := lookupLocal(fn, paramID)
	if paramLocal == nil || !paramLocal.IsParam {
		return pat, false
	}
	named, ok := paramLocal.Type.(*ir.NamedType)
	if !ok || named == nil || named.Name == "" {
		return pat, false
	}
	fieldTypes, ok := mctx.lookupStructFields(named.Name)
	if !ok {
		return pat, false
	}
	bb, ok := singleBlockReturning(fn)
	if !ok || len(bb.Instrs) != 1 {
		return pat, false
	}
	ai, ok := bb.Instrs[0].(*mir.AssignInstr)
	if !ok {
		return pat, false
	}
	if ai.Dest.Local != fn.ReturnLocal || ai.Dest.HasProjections() {
		return pat, false
	}
	bin, ok := ai.Src.(*mir.BinaryRV)
	if !ok {
		return pat, false
	}
	llvmOp, resultType, operandType := classifyBinary(bin.Op)
	if llvmOp == "" || resultType != pat.resultType {
		return pat, false
	}
	left, ok := classifyP15Operand(bin.Left, paramID, fieldTypes, operandType)
	if !ok {
		return pat, false
	}
	right, ok := classifyP15Operand(bin.Right, paramID, fieldTypes, operandType)
	if !ok {
		return pat, false
	}
	if !left.isField && !right.isField {
		// At least one operand must reference the struct param —
		// otherwise matchSequentialReturn already covers this shape.
		return pat, false
	}
	pat.structName = named.Name
	pat.fieldTypes = fieldTypes
	pat.paramName = sanitizeLLVMName(paramLocal.Name, "p")
	pat.left = left
	pat.right = right
	pat.llvmOp = llvmOp
	pat.operandType = operandType
	mctx.emitStructDef(named.Name, fieldTypes)
	return pat, true
}

// classifyP15Operand accepts either a scalar ConstOp or a CopyOp with a
// single FieldProj on the struct param. The field's type must equal
// the binary op's required operand type.
func classifyP15Operand(op mir.Operand, paramID mir.LocalID, fieldTypes []scalarType, operandType scalarType) (p15FieldOperand, bool) {
	if con, ok := op.(*mir.ConstOp); ok {
		switch c := con.Const.(type) {
		case *mir.IntConst:
			if operandType != scalarInt {
				return p15FieldOperand{}, false
			}
			return p15FieldOperand{constText: fmt.Sprintf("%d", c.Value)}, true
		case *mir.BoolConst:
			if operandType != scalarBool {
				return p15FieldOperand{}, false
			}
			if c.Value {
				return p15FieldOperand{constText: "true"}, true
			}
			return p15FieldOperand{constText: "false"}, true
		}
		return p15FieldOperand{}, false
	}
	cp, ok := op.(*mir.CopyOp)
	if !ok {
		return p15FieldOperand{}, false
	}
	if cp.Place.Local != paramID || len(cp.Place.Projections) != 1 {
		return p15FieldOperand{}, false
	}
	fp, ok := cp.Place.Projections[0].(*mir.FieldProj)
	if !ok {
		return p15FieldOperand{}, false
	}
	if fp.Index < 0 || fp.Index >= len(fieldTypes) {
		return p15FieldOperand{}, false
	}
	if fieldTypes[fp.Index] != operandType {
		return p15FieldOperand{}, false
	}
	return p15FieldOperand{isField: true, fieldIndex: fp.Index}, true
}

func emitStructFieldBinaryOp(out *strings.Builder, fn *mir.Function, pat structFieldBinaryOpPattern) error {
	resultLLVM := pat.resultType.llvm()
	operandLLVM := pat.operandType.llvm()
	fmt.Fprintf(out, "define %s @%s(%%%s %%%s) {\n", resultLLVM, fn.Name, pat.structName, pat.paramName)
	out.WriteString("entry:\n")
	nextSSA := 0
	leftExpr := pat.left.constText
	if pat.left.isField {
		leftExpr = fmt.Sprintf("%%%d", nextSSA)
		fmt.Fprintf(out, "  %s = extractvalue %%%s %%%s, %d\n", leftExpr, pat.structName, pat.paramName, pat.left.fieldIndex)
		nextSSA++
	}
	rightExpr := pat.right.constText
	if pat.right.isField {
		rightExpr = fmt.Sprintf("%%%d", nextSSA)
		fmt.Fprintf(out, "  %s = extractvalue %%%s %%%s, %d\n", rightExpr, pat.structName, pat.paramName, pat.right.fieldIndex)
		nextSSA++
	}
	resultReg := fmt.Sprintf("%%%d", nextSSA)
	fmt.Fprintf(out, "  %s = %s %s %s, %s\n", resultReg, pat.llvmOp, operandLLVM, leftExpr, rightExpr)
	fmt.Fprintf(out, "  ret %s %s\n", resultLLVM, resultReg)
	out.WriteString("}\n\n")
	return nil
}

// ---- P25: struct List field + len ----
//
// stage0 P25 handles a narrow but common audit shape:
//
//	fn count(result: FrontCheckResult) -> Int {
//	    result.typedNodes.len()
//	}
//
// MIR lowers this as one IntrinsicInstr{IntrinsicListLen} whose
// argument is a CopyOp of the struct param with a single FieldProj.
// This is safe to lower for value-struct params because LLVM can
// extract the field from the aggregate value before calling the list
// runtime. It deliberately does not generalise projections on opaque
// pointer values.

type structFieldListLenPattern struct {
	structName string
	fieldTypes []scalarType
	paramName  string
	fieldIndex int
}

func matchStructFieldListLen(fn *mir.Function, mctx *moduleCtx) (structFieldListLenPattern, bool) {
	pat := structFieldListLenPattern{}
	if scalarFromType(fn.ReturnType) != scalarInt {
		return pat, false
	}
	if len(fn.Params) != 1 {
		return pat, false
	}
	paramID := fn.Params[0]
	paramLocal := lookupLocal(fn, paramID)
	if paramLocal == nil || !paramLocal.IsParam {
		return pat, false
	}
	named, ok := paramLocal.Type.(*ir.NamedType)
	if !ok || named == nil || named.Name == "" {
		return pat, false
	}
	fieldTypes, ok := mctx.lookupStructFields(named.Name)
	if !ok {
		return pat, false
	}
	bb, ok := singleBlockReturning(fn)
	if !ok {
		return pat, false
	}

	var matched bool
	for _, instr := range bb.Instrs {
		switch step := instr.(type) {
		case *mir.StorageLiveInstr, *mir.StorageDeadInstr:
			continue
		case *mir.IntrinsicInstr:
			if matched {
				return pat, false
			}
			if step.Kind != mir.IntrinsicListLen || len(step.Args) != 1 || step.Dest == nil {
				return pat, false
			}
			if step.Dest.Local != fn.ReturnLocal || step.Dest.HasProjections() {
				return pat, false
			}
			cp, ok := step.Args[0].(*mir.CopyOp)
			if !ok || cp.Place.Local != paramID || len(cp.Place.Projections) != 1 {
				return pat, false
			}
			fp, ok := cp.Place.Projections[0].(*mir.FieldProj)
			if !ok || fp.Index < 0 || fp.Index >= len(fieldTypes) {
				return pat, false
			}
			if fieldTypes[fp.Index] != scalarOpaquePtr {
				return pat, false
			}
			pat.fieldIndex = fp.Index
			matched = true
		default:
			return pat, false
		}
	}
	if !matched {
		return pat, false
	}
	pat.structName = named.Name
	pat.fieldTypes = fieldTypes
	pat.paramName = sanitizeLLVMName(paramLocal.Name, "p")
	mctx.emitStructDef(named.Name, fieldTypes)
	declareListRuntime(mctx)
	return pat, true
}

func emitStructFieldListLen(out *strings.Builder, fn *mir.Function, pat structFieldListLenPattern) error {
	fmt.Fprintf(out, "define i64 @%s(%%%s %%%s) {\n", fn.Name, pat.structName, pat.paramName)
	out.WriteString("entry:\n")
	fmt.Fprintf(out, "  %%0 = extractvalue %%%s %%%s, %d\n", pat.structName, pat.paramName, pat.fieldIndex)
	out.WriteString("  %1 = call i64 @osty_rt_list_len(ptr %0)\n")
	out.WriteString("  ret i64 %1\n")
	out.WriteString("}\n\n")
	return nil
}

// ---- P12: list literal + len ----
//
// stage0 P12 handles the canonical "build a List<Int> literal then
// read its length" shape:
//
//	fn name() -> Int {
//	    let xs: List<Int> = [N1, N2, ...]
//	    xs.len()
//	}
//
// MIR shape: 0 params, single block, instructions = (StorageLive +
// AssignInstr writing AggregateRV{AggList} to a List<Int> local +
// IntrinsicInstr{IntrinsicListLen} reading that local), ReturnTerm.
//
// Output: declare the runtime ABI (list_new / list_push_i64 / list_len)
// at module top, emit a call sequence that allocates the list, pushes
// each element, and returns the length.

type listLiteralLenPattern struct {
	elements []int64 // currently only IntConst literals supported
}

func matchListLiteralLen(fn *mir.Function, mctx *moduleCtx) (listLiteralLenPattern, bool) {
	pat := listLiteralLenPattern{}
	if scalarFromType(fn.ReturnType) != scalarInt {
		return pat, false
	}
	if len(fn.Params) != 0 {
		return pat, false
	}
	bb, ok := singleBlockReturning(fn)
	if !ok {
		return pat, false
	}

	// Walk the instructions, skipping storage markers, recording
	// the first AssignInstr (must be AggregateRV{AggList} writing
	// IntConsts to a List<Int> local) and the IntrinsicListLen that
	// reads the same local into the return local.
	var (
		listLocal mir.LocalID
		listSet   bool
		lenSet    bool
	)
	for _, instr := range bb.Instrs {
		switch step := instr.(type) {
		case *mir.StorageLiveInstr, *mir.StorageDeadInstr:
			continue
		case *mir.AssignInstr:
			if listSet {
				return pat, false
			}
			if step.Dest.HasProjections() {
				return pat, false
			}
			loc := lookupLocal(fn, step.Dest.Local)
			if loc == nil {
				return pat, false
			}
			named, ok := loc.Type.(*ir.NamedType)
			if !ok || named == nil || named.Name != "List" {
				return pat, false
			}
			agg, ok := step.Src.(*mir.AggregateRV)
			if !ok || agg.Kind != mir.AggList {
				return pat, false
			}
			elements := make([]int64, 0, len(agg.Fields))
			for _, f := range agg.Fields {
				con, ok := f.(*mir.ConstOp)
				if !ok {
					return pat, false
				}
				ic, ok := con.Const.(*mir.IntConst)
				if !ok {
					return pat, false
				}
				elements = append(elements, ic.Value)
			}
			pat.elements = elements
			listLocal = step.Dest.Local
			listSet = true
		case *mir.IntrinsicInstr:
			if !listSet || lenSet {
				return pat, false
			}
			if step.Kind != mir.IntrinsicListLen || len(step.Args) != 1 || step.Dest == nil {
				return pat, false
			}
			if step.Dest.Local != fn.ReturnLocal || step.Dest.HasProjections() {
				return pat, false
			}
			cp, ok := step.Args[0].(*mir.CopyOp)
			if !ok || cp.Place.Local != listLocal || cp.Place.HasProjections() {
				return pat, false
			}
			lenSet = true
		default:
			return pat, false
		}
	}
	if !listSet || !lenSet {
		return pat, false
	}
	declareListRuntime(mctx)
	return pat, true
}

// declareListRuntime appends the small runtime ABI declarations
// stage0's list-literal pattern depends on. Idempotent — the
// declarations are guarded by a sentinel in mctx.emittedStructs (the
// flag map already exists for struct types and serves equally well
// here for "have the list runtime decls been emitted").
func declareListRuntime(mctx *moduleCtx) {
	if mctx.emittedStructs == nil {
		mctx.emittedStructs = map[string]bool{}
	}
	if mctx.emittedStructs["__stage0.list_runtime"] {
		return
	}
	mctx.emittedStructs["__stage0.list_runtime"] = true
	mctx.extraDecls.WriteString("declare ptr @osty_rt_list_new()\n")
	mctx.extraDecls.WriteString("declare void @osty_rt_list_push_i64(ptr, i64)\n")
	mctx.extraDecls.WriteString("declare void @osty_rt_list_push_i1(ptr, i1)\n")
	mctx.extraDecls.WriteString("declare void @osty_rt_list_push_string(ptr, ptr)\n")
	mctx.extraDecls.WriteString("declare void @osty_rt_list_push_ptr(ptr, ptr)\n")
	mctx.extraDecls.WriteString("declare i64 @osty_rt_list_len(ptr)\n")
}

func emitListLiteralLen(out *strings.Builder, fn *mir.Function, pat listLiteralLenPattern) error {
	fmt.Fprintf(out, "define i64 @%s() {\n", fn.Name)
	out.WriteString("entry:\n")
	out.WriteString("  %0 = call ptr @osty_rt_list_new()\n")
	for _, v := range pat.elements {
		fmt.Fprintf(out, "  call void @osty_rt_list_push_i64(ptr %%0, i64 %d)\n", v)
	}
	fmt.Fprintf(out, "  %%1 = call i64 @osty_rt_list_len(ptr %%0)\n")
	out.WriteString("  ret i64 %1\n")
	out.WriteString("}\n\n")
	return nil
}

// ---- P14: aggregate constructor (struct + tuple) ----
//
// stage0 P14 handles the simplest "build aggregate, return it" shape:
//
//	struct Point { x: Int, y: Int }
//	fn origin() -> Point { Point { x: 0, y: 0 } }
//	fn make(x: Int, y: Int) -> Point { Point { x: x, y: y } }
//	fn pair() -> (Int, Int) { (1, 2) }
//
// MIR shape: 0~2 scalar params, single block, single AssignInstr
// writing AggregateRV{AggStruct or AggTuple} to the return local,
// ReturnTerm. Each field is a ConstOp (Int / Bool / String) or a
// CopyOp on a function parameter.
//
// Output: emit the aggregate's type definition once at module top
// (struct uses its source name; tuple gets a synthetic `.tuple.<N>`),
// then a chain of `insertvalue` instructions inside the function.

type aggregateConstructorPattern struct {
	typeName   string       // LLVM type name without `%`
	paramTypes []scalarType // function parameter scalar types
	paramNames []string     // sanitised parameter names
	paramIDs   []mir.LocalID
	pending    []pendingInstr
	fieldExprs []string     // ordered LLVM operand expressions
	fieldTypes []scalarType // matching scalar type per field
	insertBase int
}

func matchAggregateConstructor(fn *mir.Function, mctx *moduleCtx) (aggregateConstructorPattern, bool) {
	pat := aggregateConstructorPattern{}

	// Determine the aggregate type the function returns.
	typeName, fieldTypes, ok := classifyAggregateReturnType(fn.ReturnType, mctx)
	if !ok {
		return pat, false
	}
	pat.typeName = typeName
	pat.fieldTypes = fieldTypes

	if len(fn.Params) > 8 {
		return pat, false
	}
	pat.paramIDs = fn.Params
	pat.paramTypes = make([]scalarType, len(fn.Params))
	pat.paramNames = make([]string, len(fn.Params))
	fallbackNames := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	bindings := map[mir.LocalID]localBinding{}
	for i, pid := range fn.Params {
		loc := lookupLocal(fn, pid)
		if loc == nil || !loc.IsParam {
			return pat, false
		}
		st := mctx.scalarFromType(loc.Type, true)
		if st == scalarUnknown {
			return pat, false
		}
		pat.paramTypes[i] = st
		pat.paramNames[i] = sanitizeLLVMName(loc.Name, fallbackNames[i])
		bindings[pid] = localBinding{expr: "%" + pat.paramNames[i], ty: st, defined: true}
	}
	disambiguateParamNames(pat.paramNames)
	for i, pid := range fn.Params {
		bindings[pid] = localBinding{expr: "%" + pat.paramNames[i], ty: pat.paramTypes[i], defined: true}
	}

	bb, ok := singleBlockReturning(fn)
	if !ok {
		return pat, false
	}

	nextSSA := 0
	sawAggregateReturn := false
	for _, instr := range bb.Instrs {
		if _, ok := instr.(*mir.StorageLiveInstr); ok {
			continue
		}
		if _, ok := instr.(*mir.StorageDeadInstr); ok {
			continue
		}
		if ai, ok := instr.(*mir.AssignInstr); ok && ai.Dest.Local == fn.ReturnLocal && !ai.Dest.HasProjections() {
			agg, ok := ai.Src.(*mir.AggregateRV)
			if !ok {
				return pat, false
			}
			if agg.Kind != mir.AggStruct && agg.Kind != mir.AggTuple {
				return pat, false
			}
			if len(agg.Fields) != len(fieldTypes) {
				return pat, false
			}
			pat.fieldExprs = make([]string, len(agg.Fields))
			for i, f := range agg.Fields {
				expr, ty, ok := classifyAggregateFieldWithBindings(f, bindings, mctx)
				if !ok {
					return pat, false
				}
				if ty != fieldTypes[i] {
					return pat, false
				}
				pat.fieldExprs[i] = expr
			}
			sawAggregateReturn = true
			continue
		}
		if sawAggregateReturn {
			return pat, false
		}
		emit := blockEmit{}
		if !applyStep(fn, instr, bindings, mctx, &nextSSA, &emit) {
			return pat, false
		}
		pat.pending = append(pat.pending, emit.pending...)
	}
	if !sawAggregateReturn {
		return pat, false
	}
	pat.insertBase = nextSSA
	return pat, true
}

// classifyAggregateReturnType maps the function's return type to a
// (typeName, fieldTypes) pair when the type is an aggregate stage0
// can lower. Struct types use the source name and emit their type
// definition through emitStructDef; tuple types use a synthetic
// `.tuple.<N>` id allocated by mctx.internTupleType.
func classifyAggregateReturnType(retT mir.Type, mctx *moduleCtx) (string, []scalarType, bool) {
	switch t := retT.(type) {
	case *ir.NamedType:
		if t == nil || t.Name == "" {
			return "", nil, false
		}
		fields, ok := mctx.lookupStructFields(t.Name)
		if !ok {
			return "", nil, false
		}
		mctx.emitStructDef(t.Name, fields)
		return t.Name, fields, true
	case *ir.TupleType:
		if t == nil {
			return "", nil, false
		}
		fields := make([]scalarType, len(t.Elems))
		for i, e := range t.Elems {
			st := mctx.scalarFromType(e, true)
			if st == scalarUnknown {
				return "", nil, false
			}
			fields[i] = st
		}
		name := mctx.internTupleType(fields)
		return name, fields, true
	}
	return "", nil, false
}

// classifyAggregateField resolves one operand inside an aggregate
// constructor. Constants (Int / Bool / String) inline directly; a
// CopyOp must reference a parameter local without projections.
func classifyAggregateField(op mir.Operand, paramRegs map[mir.LocalID]string, fn *mir.Function, mctx *moduleCtx) (string, scalarType, bool) {
	if con, ok := op.(*mir.ConstOp); ok {
		switch c := con.Const.(type) {
		case *mir.IntConst:
			return fmt.Sprintf("%d", c.Value), scalarInt, true
		case *mir.BoolConst:
			if c.Value {
				return "true", scalarBool, true
			}
			return "false", scalarBool, true
		case *mir.StringConst:
			if mctx == nil {
				return "", scalarUnknown, false
			}
			return mctx.internStringConst(c.Value), scalarString, true
		}
		return "", scalarUnknown, false
	}
	if cp, ok := op.(*mir.CopyOp); ok {
		if cp.Place.HasProjections() {
			return "", scalarUnknown, false
		}
		reg, found := paramRegs[cp.Place.Local]
		if !found {
			return "", scalarUnknown, false
		}
		loc := lookupLocal(fn, cp.Place.Local)
		if loc == nil {
			return "", scalarUnknown, false
		}
		return reg, mctx.scalarFromType(loc.Type, true), true
	}
	return "", scalarUnknown, false
}

func classifyAggregateFieldWithBindings(op mir.Operand, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (string, scalarType, bool) {
	if con, ok := op.(*mir.ConstOp); ok {
		switch c := con.Const.(type) {
		case *mir.IntConst:
			return fmt.Sprintf("%d", c.Value), scalarInt, true
		case *mir.BoolConst:
			if c.Value {
				return "true", scalarBool, true
			}
			return "false", scalarBool, true
		case *mir.StringConst:
			if mctx == nil {
				return "", scalarUnknown, false
			}
			return mctx.internStringConst(c.Value), scalarString, true
		}
		return "", scalarUnknown, false
	}
	return resolveOperand(op, bindings, mctx)
}

func emitAggregateConstructor(out *strings.Builder, fn *mir.Function, pat aggregateConstructorPattern) error {
	// Function signature.
	fmt.Fprintf(out, "define %%%s @%s(", pat.typeName, fn.Name)
	for i, name := range pat.paramNames {
		if i > 0 {
			out.WriteString(", ")
		}
		fmt.Fprintf(out, "%s %%%s", pat.paramTypes[i].llvm(), name)
	}
	out.WriteString(") {\n")
	out.WriteString("entry:\n")
	for _, pi := range pat.pending {
		emitPendingInstr(out, pi)
	}

	// `insertvalue` chain. Start from `poison` (LLVM's "undefined"
	// sentinel) and write each field in order. Final register holds
	// the fully populated aggregate.
	prev := "poison"
	for i, fieldExpr := range pat.fieldExprs {
		reg := pat.insertBase + i
		fmt.Fprintf(out, "  %%%d = insertvalue %%%s %s, %s %s, %d\n", reg, pat.typeName, prev, pat.fieldTypes[i].llvm(), fieldExpr, i)
		prev = fmt.Sprintf("%%%d", reg)
	}
	fmt.Fprintf(out, "  ret %%%s %s\n", pat.typeName, prev)
	out.WriteString("}\n\n")
	return nil
}

// ---- P15: list literal + indexed read ----
//
// `let xs: List<Int> = [10, 20, 30]; xs[0]` lowers to a 3-instruction
// block: an AggregateRV{AggList} writing the list, followed by an
// AssignInstr whose Src is `UseRV(CopyOp(list_local, [IndexProj]))`.
// stage0 P15 emits this as a list_new + N pushes + list_get_i64 chain.

type listLiteralIndexPattern struct {
	elements []int64
	index    int64
}

func matchListLiteralIndexGet(fn *mir.Function, mctx *moduleCtx) (listLiteralIndexPattern, bool) {
	pat := listLiteralIndexPattern{}
	if scalarFromType(fn.ReturnType) != scalarInt {
		return pat, false
	}
	if len(fn.Params) != 0 {
		return pat, false
	}
	bb, ok := singleBlockReturning(fn)
	if !ok {
		return pat, false
	}

	var (
		listLocal mir.LocalID
		listSet   bool
		readSet   bool
	)
	for _, instr := range bb.Instrs {
		switch step := instr.(type) {
		case *mir.StorageLiveInstr, *mir.StorageDeadInstr:
			continue
		case *mir.AssignInstr:
			if !listSet {
				if step.Dest.HasProjections() {
					return pat, false
				}
				loc := lookupLocal(fn, step.Dest.Local)
				if loc == nil {
					return pat, false
				}
				named, ok := loc.Type.(*ir.NamedType)
				if !ok || named == nil || named.Name != "List" {
					return pat, false
				}
				agg, ok := step.Src.(*mir.AggregateRV)
				if !ok || agg.Kind != mir.AggList {
					return pat, false
				}
				elements := make([]int64, 0, len(agg.Fields))
				for _, f := range agg.Fields {
					con, ok := f.(*mir.ConstOp)
					if !ok {
						return pat, false
					}
					ic, ok := con.Const.(*mir.IntConst)
					if !ok {
						return pat, false
					}
					elements = append(elements, ic.Value)
				}
				pat.elements = elements
				listLocal = step.Dest.Local
				listSet = true
				continue
			}
			// Second AssignInstr: must be the indexed read into ret.
			if readSet {
				return pat, false
			}
			if step.Dest.Local != fn.ReturnLocal || step.Dest.HasProjections() {
				return pat, false
			}
			use, ok := step.Src.(*mir.UseRV)
			if !ok {
				return pat, false
			}
			cp, ok := use.Op.(*mir.CopyOp)
			if !ok || cp.Place.Local != listLocal {
				return pat, false
			}
			if len(cp.Place.Projections) != 1 {
				return pat, false
			}
			idxProj, ok := cp.Place.Projections[0].(*mir.IndexProj)
			if !ok {
				return pat, false
			}
			idxConst, ok := idxProj.Index.(*mir.ConstOp)
			if !ok {
				return pat, false
			}
			ic, ok := idxConst.Const.(*mir.IntConst)
			if !ok {
				return pat, false
			}
			pat.index = ic.Value
			readSet = true
		default:
			return pat, false
		}
	}
	if !listSet || !readSet {
		return pat, false
	}
	declareListRuntime(mctx)
	declareListGetRuntime(mctx)
	return pat, true
}

// declareListGetRuntime adds the `osty_rt_list_get_i64` declare to
// extraDecls (idempotent — sentinel via emittedStructs).
func declareListGetRuntime(mctx *moduleCtx) {
	if mctx.emittedStructs == nil {
		mctx.emittedStructs = map[string]bool{}
	}
	if mctx.emittedStructs["__stage0.list_get_i64"] {
		return
	}
	mctx.emittedStructs["__stage0.list_get_i64"] = true
	mctx.extraDecls.WriteString("declare i64 @osty_rt_list_get_i64(ptr, i64)\n")
}

func emitListLiteralIndexGet(out *strings.Builder, fn *mir.Function, pat listLiteralIndexPattern) error {
	fmt.Fprintf(out, "define i64 @%s() {\n", fn.Name)
	out.WriteString("entry:\n")
	out.WriteString("  %0 = call ptr @osty_rt_list_new()\n")
	for _, v := range pat.elements {
		fmt.Fprintf(out, "  call void @osty_rt_list_push_i64(ptr %%0, i64 %d)\n", v)
	}
	fmt.Fprintf(out, "  %%1 = call i64 @osty_rt_list_get_i64(ptr %%0, i64 %d)\n", pat.index)
	out.WriteString("  ret i64 %1\n")
	out.WriteString("}\n\n")
	return nil
}
