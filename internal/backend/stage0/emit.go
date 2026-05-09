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
//
//   - P21 — single-block thin-wrapper direct call → aggregate return:
//     `fn wrap(a: T1, ...) -> Struct { innerFn(a, ...) }`. Exactly one
//     CallInstr writing the return local; return type must be a struct or
//     tuple (classifyAggregateReturnType). All params scalar. The callee
//     prototype is declared if it is not a locally-defined symbol.
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
	if pat, ok := matchStructFieldRead(fn, mctx); ok {
		return emitStructFieldRead(out, fn, pat)
	}
	if pat, ok := matchStructFieldBinaryOp(fn, mctx); ok {
		return emitStructFieldBinaryOp(out, fn, pat)
	}
	if pat, ok := matchStructFieldListLen(fn, mctx); ok {
		return emitStructFieldListLen(out, fn, pat)
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
	if pat, ok := matchShortCircuitGuardReturn(fn, mctx); ok {
		return emitShortCircuitGuardReturn(out, fn, pat)
	}
	if pat, ok := matchShortCircuitBoolReturn(fn, mctx); ok {
		return emitShortCircuitBoolReturn(out, fn, pat)
	}
	if pat, ok := matchScalarReturnChain(fn, mctx); ok {
		return emitScalarReturnChain(out, fn, pat)
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
	if pat, ok := matchDirectAggregateCall(fn, mctx); ok {
		return emitDirectAggregateCall(out, fn, pat, mctx)
	}
	if pat, ok := matchForInListReturn(fn, mctx); ok {
		return emitForInListReturn(out, fn, pat)
	}
	if pat, ok := matchForInListEarlyExit(fn, mctx); ok {
		return emitForInListEarlyExit(out, fn, pat)
	}
	if pat, ok := matchWhileLoopReturn(fn, mctx); ok {
		return emitWhileLoopReturn(out, fn, pat)
	}
	if pat, ok := matchForInRangeReturn(fn, mctx); ok {
		return emitForInRangeReturn(out, fn, pat)
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
	// prelude contains LLVM lines that must run immediately before
	// the main instruction, typically field loads used to materialise
	// projected call/intrinsic operands.
	prelude string
	// SSA register pre-assigned at match time. Always set for
	// instrBinary / instrCall; for instrIntrinsic it may hold the
	// result expression produced by a multi-line intrinsic lowering.
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

type resolvedCallArgs struct {
	prelude string
	args    []callArg
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
		prelude, expr, ty, ok := resolveOperandWithPrelude(fn, ii.Args[0], bindings, mctx)
		if !ok {
			return "", false
		}
		switch ty {
		case scalarInt:
			return prelude + fmt.Sprintf("  call i32 (ptr, ...) @printf(ptr @.fmt.stage0.println.int, i64 %s)\n", expr), true
		case scalarString:
			return prelude + fmt.Sprintf("  call i32 (ptr, ...) @printf(ptr @.fmt.stage0.println.str, ptr %s)\n", expr), true
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
		elemPrelude, elemExpr, elemTy, ok := resolveOperandWithPrelude(fn, ii.Args[1], bindings, mctx)
		if !ok {
			return "", false
		}
		pushSymbol := listPushSymbolFor(elemTy)
		if pushSymbol == "" {
			return "", false
		}
		declareListRuntime(mctx)
		return fmt.Sprintf("%s%s  call void @%s(ptr %s, %s %s)\n", listPrelude, elemPrelude, pushSymbol, listExpr, elemTy.llvm(), elemExpr), true
	case mir.IntrinsicListInsert:
		if len(ii.Args) != 3 {
			return "", false
		}
		listPrelude, listExpr, ok := resolveListReceiverOperand(fn, ii.Args[0], bindings, mctx)
		if !ok {
			return "", false
		}
		indexPrelude, indexExpr, indexTy, ok := resolveOperandWithPrelude(fn, ii.Args[1], bindings, mctx)
		if !ok || indexTy != scalarInt {
			return "", false
		}
		elemPrelude, elemExpr, elemTy, ok := resolveOperandWithPrelude(fn, ii.Args[2], bindings, mctx)
		if !ok {
			return "", false
		}
		symbol := listInsertSymbolFor(elemTy)
		if symbol == "" {
			return "", false
		}
		declareVoidFunctionPrototype(mctx, symbol, []callArg{{ty: "ptr"}, {ty: "i64"}, {ty: elemTy.llvm()}})
		return fmt.Sprintf("%s%s%s  call void @%s(ptr %s, i64 %s, %s %s)\n", listPrelude, indexPrelude, elemPrelude, symbol, listExpr, indexExpr, elemTy.llvm(), elemExpr), true
	case mir.IntrinsicListClear:
		if len(ii.Args) != 1 {
			return "", false
		}
		prelude, expr, ty, ok := resolveOperandWithPrelude(fn, ii.Args[0], bindings, mctx)
		if !ok || ty != scalarOpaquePtr {
			return "", false
		}
		declareVoidFunctionPrototype(mctx, "osty_rt_list_clear", []callArg{{ty: "ptr"}})
		return prelude + fmt.Sprintf("  call void @osty_rt_list_clear(ptr %s)\n", expr), true
	case mir.IntrinsicListReverse:
		if len(ii.Args) != 1 {
			return "", false
		}
		prelude, expr, ty, ok := resolveOperandWithPrelude(fn, ii.Args[0], bindings, mctx)
		if !ok || ty != scalarOpaquePtr {
			return "", false
		}
		declareVoidFunctionPrototype(mctx, "osty_rt_list_reverse", []callArg{{ty: "ptr"}})
		return prelude + fmt.Sprintf("  call void @osty_rt_list_reverse(ptr %s)\n", expr), true
	case mir.IntrinsicSetInsert, mir.IntrinsicSetRemove:
		if len(ii.Args) != 2 {
			return "", false
		}
		setPrelude, setExpr, setTy, ok := resolveOperandWithPrelude(fn, ii.Args[0], bindings, mctx)
		if !ok || setTy != scalarOpaquePtr {
			return "", false
		}
		elemPrelude, elemExpr, elemTy, ok := resolveOperandWithPrelude(fn, ii.Args[1], bindings, mctx)
		if !ok {
			return "", false
		}
		symbol := setMutationSymbolFor(ii.Kind, elemTy)
		if symbol == "" {
			return "", false
		}
		args := []callArg{{ty: "ptr"}, {ty: elemTy.llvm()}}
		declareRuntimePrototype(mctx, symbol, scalarBool, args)
		return fmt.Sprintf("%s%s  call i1 @%s(ptr %s, %s %s)\n", setPrelude, elemPrelude, symbol, setExpr, elemTy.llvm(), elemExpr), true
	case mir.IntrinsicMapSet:
		if len(ii.Args) != 3 {
			return "", false
		}
		mapPrelude, mapExpr, mapTy, ok := resolveOperandWithPrelude(fn, ii.Args[0], bindings, mctx)
		if !ok || mapTy != scalarOpaquePtr {
			return "", false
		}
		keyPrelude, keyExpr, keyTy, ok := resolveOperandWithPrelude(fn, ii.Args[1], bindings, mctx)
		if !ok {
			return "", false
		}
		valuePrelude, valueExpr, valueTy, ok := resolveOperandWithPrelude(fn, ii.Args[2], bindings, mctx)
		if !ok {
			return "", false
		}
		symbol := mapInsertSymbolFor(keyTy)
		if symbol == "" {
			return "", false
		}
		valueSlot := mctx.freshTempName("map.value")
		declareVoidFunctionPrototype(mctx, symbol, []callArg{{ty: "ptr"}, {ty: keyTy.llvm()}, {ty: "ptr"}})
		return fmt.Sprintf("%s%s%s  %s = alloca %s\n  store %s %s, ptr %s\n  call void @%s(ptr %s, %s %s, ptr %s)\n",
			mapPrelude, keyPrelude, valuePrelude,
			valueSlot, valueTy.llvm(),
			valueTy.llvm(), valueExpr, valueSlot,
			symbol, mapExpr, keyTy.llvm(), keyExpr, valueSlot), true
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
		prelude, expr, ty, ok := resolveOperandWithPrelude(fn, ii.Args[0], bindings, mctx)
		if !ok || ty != scalarInt {
			return "", false
		}
		declareVoidFunctionPrototype(mctx, "osty_rt_sleep", []callArg{{ty: "i64"}})
		return prelude + fmt.Sprintf("  call void @osty_rt_sleep(i64 %s)\n", expr), true
	case mir.IntrinsicCheckCancelled:
		if len(ii.Args) != 0 {
			return "", false
		}
		declareVoidFunctionPrototype(mctx, "osty_rt_check_cancelled", nil)
		return "  call void @osty_rt_check_cancelled()\n", true
	case mir.IntrinsicMapClear:
		if len(ii.Args) != 1 {
			return "", false
		}
		prelude, expr, ty, ok := resolveOperandWithPrelude(fn, ii.Args[0], bindings, mctx)
		if !ok || ty != scalarOpaquePtr {
			return "", false
		}
		declareVoidFunctionPrototype(mctx, "osty_rt_map_clear", []callArg{{ty: "ptr"}})
		return prelude + fmt.Sprintf("  call void @osty_rt_map_clear(ptr %s)\n", expr), true
	case mir.IntrinsicSetClear:
		if len(ii.Args) != 1 {
			return "", false
		}
		prelude, expr, ty, ok := resolveOperandWithPrelude(fn, ii.Args[0], bindings, mctx)
		if !ok || ty != scalarOpaquePtr {
			return "", false
		}
		declareVoidFunctionPrototype(mctx, "osty_rt_set_clear", []callArg{{ty: "ptr"}})
		return prelude + fmt.Sprintf("  call void @osty_rt_set_clear(ptr %s)\n", expr), true
	case mir.IntrinsicStringSplitInto:
		if len(ii.Args) != 3 {
			return "", false
		}
		outPrelude, outExpr, outTy, ok := resolveOperandWithPrelude(fn, ii.Args[0], bindings, mctx)
		if !ok || outTy != scalarOpaquePtr {
			return "", false
		}
		valuePrelude, valueExpr, valueTy, ok := resolveOperandWithPrelude(fn, ii.Args[1], bindings, mctx)
		if !ok || valueTy != scalarString {
			return "", false
		}
		sepPrelude, sepExpr, sepTy, ok := resolveOperandWithPrelude(fn, ii.Args[2], bindings, mctx)
		if !ok || sepTy != scalarString {
			return "", false
		}
		declareVoidFunctionPrototype(mctx, "osty_rt_strings_SplitInto", []callArg{{ty: "ptr"}, {ty: "ptr"}, {ty: "ptr"}})
		return fmt.Sprintf("%s%s%s  call void @osty_rt_strings_SplitInto(ptr %s, ptr %s, ptr %s)\n", outPrelude, valuePrelude, sepPrelude, outExpr, valueExpr, sepExpr), true
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
			if step.Dest.HasProjections() {
				fieldWrite, okField := classifyFieldWriteStep(fn, step, bindings, mctx)
				if !okField {
					return pat, false
				}
				pat.pending = append(pat.pending, fieldWrite)
				continue
			}
			pending, expr, destID, destType, okStep = classifyAssignStep(fn, step, bindings, mctx)
		case *mir.CallInstr:
			if step.Dest == nil {
				line, okCall := classifyVoidCallLine(fn, step, bindings, mctx)
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
		case instrIntrinsic:
			if pending.binDestReg != "" {
				bindings[destID] = localBinding{expr: pending.binDestReg, ty: destType, defined: true}
			} else {
				bindings[destID] = localBinding{expr: expr, ty: destType, defined: true}
			}
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
			if step.Dest.HasProjections() {
				fieldWrite, okField := classifyFieldWriteStep(fn, step, bindings, mctx)
				if !okField {
					return pat, false
				}
				pat.pending = append(pat.pending, fieldWrite)
				continue
			}
			pending, expr, destID, destType, okStep = classifyAssignStep(fn, step, bindings, mctx)
		case *mir.CallInstr:
			if step.Dest == nil {
				line, okCall := classifyVoidCallLine(fn, step, bindings, mctx)
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
		case instrIntrinsic:
			if pending.binDestReg != "" {
				bindings[destID] = localBinding{expr: pending.binDestReg, ty: destType, defined: true}
			} else {
				bindings[destID] = localBinding{expr: expr, ty: destType, defined: true}
			}
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
	pending, expr, ok := classifyAssignSrc(fn, ai.Src, destType, bindings, mctx)
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
		resolved, ok := resolveCallArgsWithoutFnType(fn, ci.Args, bindings, mctx)
		if !ok {
			return pendingInstr{}, 0, scalarUnknown, false
		}
		declareFunctionPrototype(mctx, ref.Symbol, destType, resolved.args)
		return pendingInstr{
			kind:       instrCall,
			prelude:    resolved.prelude,
			callSymbol: ref.Symbol,
			callArgs:   resolved.args,
		}, destID, destType, true
	}
	if mctx.scalarFromType(fnTy.Return, allowOpaqueUserNamed) != destType {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	if len(fnTy.Params) != len(ci.Args) {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	args := make([]callArg, 0, len(ci.Args))
	var prelude strings.Builder
	for i, op := range ci.Args {
		argPrelude, argExpr, argTy, okOp := resolveOperandWithPrelude(fn, op, bindings, mctx)
		if !okOp {
			return pendingInstr{}, 0, scalarUnknown, false
		}
		// Param type must agree with the callee's declared param.
		paramTy := mctx.scalarFromType(fnTy.Params[i], allowOpaqueUserNamed)
		if paramTy == scalarUnknown || paramTy != argTy {
			return pendingInstr{}, 0, scalarUnknown, false
		}
		prelude.WriteString(argPrelude)
		args = append(args, callArg{expr: argExpr, ty: argTy.llvm()})
	}
	if !mctx.knownSymbols[ref.Symbol] {
		declareFunctionPrototype(mctx, ref.Symbol, destType, args)
	}
	return pendingInstr{
		kind:       instrCall,
		prelude:    prelude.String(),
		callSymbol: ref.Symbol,
		callArgs:   args,
	}, destID, destType, true
}

func classifyVoidCallLine(fn *mir.Function, ci *mir.CallInstr, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (string, bool) {
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
		var prelude strings.Builder
		for i, op := range ci.Args {
			argPrelude, argExpr, argTy, okOp := resolveOperandWithPrelude(fn, op, bindings, mctx)
			if !okOp {
				return "", false
			}
			paramTy := mctx.scalarFromType(fnTy.Params[i], allowOpaqueUserNamed)
			if paramTy == scalarUnknown || paramTy != argTy {
				return "", false
			}
			prelude.WriteString(argPrelude)
			args = append(args, callArg{expr: argExpr, ty: argTy.llvm()})
		}
		if !mctx.knownSymbols[ref.Symbol] {
			declareVoidFunctionPrototype(mctx, ref.Symbol, args)
		}
		return prelude.String() + renderVoidCallLine(ref.Symbol, args), true
	} else {
		if !allowOpaqueUserNamed || !isErrType(ref.Type) {
			return "", false
		}
		var okArgs bool
		resolved, okArgs := resolveCallArgsWithoutFnType(fn, ci.Args, bindings, mctx)
		if !okArgs {
			return "", false
		}
		args = resolved.args
		if !mctx.knownSymbols[ref.Symbol] {
			declareVoidFunctionPrototype(mctx, ref.Symbol, args)
		}
		return resolved.prelude + renderVoidCallLine(ref.Symbol, args), true
	}
}

func isErrType(t mir.Type) bool {
	_, ok := t.(*ir.ErrType)
	return ok
}

func resolveCallArgsWithoutFnType(fn *mir.Function, argsIn []mir.Operand, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (resolvedCallArgs, bool) {
	args := make([]callArg, 0, len(argsIn))
	var prelude strings.Builder
	for _, op := range argsIn {
		argPrelude, argExpr, argTy, ok := resolveOperandWithPrelude(fn, op, bindings, mctx)
		if !ok || argTy == scalarUnknown {
			return resolvedCallArgs{}, false
		}
		prelude.WriteString(argPrelude)
		args = append(args, callArg{expr: argExpr, ty: argTy.llvm()})
	}
	return resolvedCallArgs{prelude: prelude.String(), args: args}, true
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
		return classifyStringConcatIntrinsic(fn, ii, destID, destType, bindings, mctx)
	}
	if ii.Kind == mir.IntrinsicListIsEmpty {
		return classifyListIsEmptyIntrinsic(fn, ii, destID, destType, bindings, mctx)
	}
	if ii.Kind == mir.IntrinsicStringIsEmpty {
		return classifyStringIsEmptyIntrinsic(fn, ii, destID, destType, bindings, mctx)
	}
	if ii.Kind == mir.IntrinsicBytesContains {
		return classifyBytesContainsIntrinsic(fn, ii, destID, destType, bindings, mctx)
	}
	if ii.Kind == mir.IntrinsicListGet {
		return classifyListGetIntrinsic(fn, ii, destID, destType, bindings, mctx)
	}
	if ii.Kind == mir.IntrinsicListSorted || ii.Kind == mir.IntrinsicListToSet || ii.Kind == mir.IntrinsicListToString {
		return classifyTypedListUnaryIntrinsic(fn, ii, destID, destType, bindings, mctx)
	}
	if ii.Kind == mir.IntrinsicMapContains {
		return classifyMapContainsIntrinsic(fn, ii, destID, destType, bindings, mctx)
	}
	if ii.Kind == mir.IntrinsicMapNew {
		return classifyMapNewIntrinsic(fn, ii, destID, destType, mctx)
	}
	if ii.Kind == mir.IntrinsicMapKeysSorted {
		return classifyMapKeysSortedIntrinsic(fn, ii, destID, destType, bindings, mctx)
	}
	if ii.Kind == mir.IntrinsicMapIncr {
		return classifyMapIncrIntrinsic(fn, ii, destID, destType, bindings, mctx)
	}
	if ii.Kind == mir.IntrinsicSetContains {
		return classifySetContainsIntrinsic(fn, ii, destID, destType, bindings, mctx)
	}
	if ii.Kind == mir.IntrinsicSetNew {
		return classifySetNewIntrinsic(fn, ii, destID, destType, mctx)
	}
	if ii.Kind == mir.IntrinsicRawNull {
		return classifyRawNullIntrinsic(ii, destID, destType)
	}
	if ii.Kind == mir.IntrinsicLikely || ii.Kind == mir.IntrinsicUnlikely {
		return classifyBranchHintIntrinsic(fn, ii, destID, destType, bindings, mctx)
	}

	spec, ok := intrinsicRuntimeCallSpec(ii.Kind)
	if !ok || spec.ret != destType || len(spec.args) != len(ii.Args) {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	args := make([]callArg, 0, len(ii.Args))
	var prelude strings.Builder
	for i, op := range ii.Args {
		argPrelude, expr, ty, ok := resolveOperandWithPrelude(fn, op, bindings, mctx)
		if !ok || ty != spec.args[i] {
			return pendingInstr{}, 0, scalarUnknown, false
		}
		prelude.WriteString(argPrelude)
		args = append(args, callArg{expr: expr, ty: ty.llvm()})
	}
	declareRuntimePrototype(mctx, spec.symbol, spec.ret, args)
	return pendingInstr{
		kind:       instrCall,
		prelude:    prelude.String(),
		callSymbol: spec.symbol,
		callArgs:   args,
	}, destID, destType, true
}

func classifyListIsEmptyIntrinsic(fn *mir.Function, ii *mir.IntrinsicInstr, destID mir.LocalID, destType scalarType, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (pendingInstr, mir.LocalID, scalarType, bool) {
	if destType != scalarBool || len(ii.Args) != 1 {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	prelude, expr, ty, ok := resolveOperandWithPrelude(fn, ii.Args[0], bindings, mctx)
	if !ok || ty != scalarOpaquePtr {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	declareListRuntime(mctx)
	lenReg := mctx.freshTempName("list.is_empty.len")
	boolReg := mctx.freshTempName("list.is_empty")
	line := prelude +
		fmt.Sprintf("  %s = call i64 @osty_rt_list_len(ptr %s)\n", lenReg, expr) +
		fmt.Sprintf("  %s = icmp eq i64 %s, 0\n", boolReg, lenReg)
	return pendingInstr{kind: instrIntrinsic, binDestReg: boolReg, intrinsicLine: line}, destID, scalarBool, true
}

func classifyStringIsEmptyIntrinsic(fn *mir.Function, ii *mir.IntrinsicInstr, destID mir.LocalID, destType scalarType, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (pendingInstr, mir.LocalID, scalarType, bool) {
	if destType != scalarBool || len(ii.Args) != 1 {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	prelude, expr, ty, ok := resolveOperandWithPrelude(fn, ii.Args[0], bindings, mctx)
	if !ok || ty != scalarString {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	declareRuntimePrototype(mctx, "osty_rt_strings_ByteLen", scalarInt, []callArg{{ty: "ptr"}})
	lenReg := mctx.freshTempName("string.is_empty.len")
	boolReg := mctx.freshTempName("string.is_empty")
	line := prelude +
		fmt.Sprintf("  %s = call i64 @osty_rt_strings_ByteLen(ptr %s)\n", lenReg, expr) +
		fmt.Sprintf("  %s = icmp eq i64 %s, 0\n", boolReg, lenReg)
	return pendingInstr{kind: instrIntrinsic, binDestReg: boolReg, intrinsicLine: line}, destID, scalarBool, true
}

func classifyBytesContainsIntrinsic(fn *mir.Function, ii *mir.IntrinsicInstr, destID mir.LocalID, destType scalarType, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (pendingInstr, mir.LocalID, scalarType, bool) {
	if destType != scalarBool || len(ii.Args) != 2 {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	valuePrelude, valueExpr, valueTy, ok := resolveOperandWithPrelude(fn, ii.Args[0], bindings, mctx)
	if !ok || valueTy != scalarOpaquePtr {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	needlePrelude, needleExpr, needleTy, ok := resolveOperandWithPrelude(fn, ii.Args[1], bindings, mctx)
	if !ok || needleTy != scalarOpaquePtr {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	declareRuntimePrototype(mctx, "osty_rt_bytes_index_of", scalarInt, []callArg{{ty: "ptr"}, {ty: "ptr"}})
	indexReg := mctx.freshTempName("bytes.contains.index")
	boolReg := mctx.freshTempName("bytes.contains")
	line := valuePrelude + needlePrelude +
		fmt.Sprintf("  %s = call i64 @osty_rt_bytes_index_of(ptr %s, ptr %s)\n", indexReg, valueExpr, needleExpr) +
		fmt.Sprintf("  %s = icmp ne i64 %s, -1\n", boolReg, indexReg)
	return pendingInstr{kind: instrIntrinsic, binDestReg: boolReg, intrinsicLine: line}, destID, scalarBool, true
}

func classifyListGetIntrinsic(fn *mir.Function, ii *mir.IntrinsicInstr, destID mir.LocalID, destType scalarType, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (pendingInstr, mir.LocalID, scalarType, bool) {
	if len(ii.Args) != 2 {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	listPrelude, listExpr, ok := resolveListReceiverOperand(fn, ii.Args[0], bindings, mctx)
	if !ok {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	indexPrelude, indexExpr, indexTy, ok := resolveOperandWithPrelude(fn, ii.Args[1], bindings, mctx)
	if !ok || indexTy != scalarInt {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	symbol := listGetSymbolFor(destType)
	if symbol == "" {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	declareListGetRuntimeFor(mctx, destType)
	return pendingInstr{
		kind:       instrCall,
		prelude:    listPrelude + indexPrelude,
		callSymbol: symbol,
		callArgs: []callArg{
			{expr: listExpr, ty: "ptr"},
			{expr: indexExpr, ty: "i64"},
		},
	}, destID, destType, true
}

func classifyTypedListUnaryIntrinsic(fn *mir.Function, ii *mir.IntrinsicInstr, destID mir.LocalID, destType scalarType, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (pendingInstr, mir.LocalID, scalarType, bool) {
	if len(ii.Args) != 1 {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	prelude, expr, ok := resolveListReceiverOperand(fn, ii.Args[0], bindings, mctx)
	if !ok {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	elemType := collectionArgScalar(ii.Args[0], "List", 0, mctx)
	if elemType == scalarUnknown {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	var symbol string
	switch ii.Kind {
	case mir.IntrinsicListSorted:
		if destType != scalarOpaquePtr {
			return pendingInstr{}, 0, scalarUnknown, false
		}
		symbol = listSortedSymbolFor(elemType)
	case mir.IntrinsicListToSet:
		if destType != scalarOpaquePtr {
			return pendingInstr{}, 0, scalarUnknown, false
		}
		symbol = listToSetSymbolFor(elemType)
	case mir.IntrinsicListToString:
		if destType != scalarString {
			return pendingInstr{}, 0, scalarUnknown, false
		}
		symbol = listToStringSymbolFor(elemType)
	default:
		return pendingInstr{}, 0, scalarUnknown, false
	}
	if symbol == "" {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	declareRuntimePrototype(mctx, symbol, destType, []callArg{{ty: "ptr"}})
	return pendingInstr{
		kind:       instrCall,
		prelude:    prelude,
		callSymbol: symbol,
		callArgs:   []callArg{{expr: expr, ty: "ptr"}},
	}, destID, destType, true
}

func classifyMapContainsIntrinsic(fn *mir.Function, ii *mir.IntrinsicInstr, destID mir.LocalID, destType scalarType, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (pendingInstr, mir.LocalID, scalarType, bool) {
	if destType != scalarBool || len(ii.Args) != 2 {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	mapPrelude, mapExpr, mapTy, ok := resolveOperandWithPrelude(fn, ii.Args[0], bindings, mctx)
	if !ok || mapTy != scalarOpaquePtr {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	keyPrelude, keyExpr, keyTy, ok := resolveOperandWithPrelude(fn, ii.Args[1], bindings, mctx)
	if !ok {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	symbol := mapContainsSymbolFor(keyTy)
	if symbol == "" {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	args := []callArg{{expr: mapExpr, ty: "ptr"}, {expr: keyExpr, ty: keyTy.llvm()}}
	declareRuntimePrototype(mctx, symbol, scalarBool, args)
	return pendingInstr{kind: instrCall, prelude: mapPrelude + keyPrelude, callSymbol: symbol, callArgs: args}, destID, scalarBool, true
}

func classifyMapNewIntrinsic(fn *mir.Function, ii *mir.IntrinsicInstr, destID mir.LocalID, destType scalarType, mctx *moduleCtx) (pendingInstr, mir.LocalID, scalarType, bool) {
	if destType != scalarOpaquePtr || len(ii.Args) != 0 {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	keyType := localCollectionArgScalar(fn, destID, "Map", 0, mctx)
	valueType := localCollectionArgScalar(fn, destID, "Map", 1, mctx)
	keyKind, ok := runtimeKindForScalar(keyType)
	if !ok {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	valueKind, ok := runtimeKindForScalar(valueType)
	if !ok {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	valueSize, ok := runtimeSizeForScalar(valueType)
	if !ok {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	args := []callArg{
		{expr: fmt.Sprintf("%d", keyKind), ty: "i64"},
		{expr: fmt.Sprintf("%d", valueKind), ty: "i64"},
		{expr: fmt.Sprintf("%d", valueSize), ty: "i64"},
		{expr: "null", ty: "ptr"},
	}
	declareRuntimePrototype(mctx, "osty_rt_map_new", scalarOpaquePtr, args)
	return pendingInstr{kind: instrCall, callSymbol: "osty_rt_map_new", callArgs: args}, destID, scalarOpaquePtr, true
}

func classifyMapKeysSortedIntrinsic(fn *mir.Function, ii *mir.IntrinsicInstr, destID mir.LocalID, destType scalarType, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (pendingInstr, mir.LocalID, scalarType, bool) {
	if destType != scalarOpaquePtr || len(ii.Args) != 1 {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	prelude, expr, ty, ok := resolveOperandWithPrelude(fn, ii.Args[0], bindings, mctx)
	if !ok || ty != scalarOpaquePtr {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	keyType := collectionArgScalar(ii.Args[0], "Map", 0, mctx)
	suffix := sortableRuntimeSuffixFor(keyType)
	if suffix == "" {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	symbol := "osty_rt_map_keys_sorted_" + suffix
	declareRuntimePrototype(mctx, symbol, scalarOpaquePtr, []callArg{{ty: "ptr"}})
	return pendingInstr{kind: instrCall, prelude: prelude, callSymbol: symbol, callArgs: []callArg{{expr: expr, ty: "ptr"}}}, destID, scalarOpaquePtr, true
}

func classifyMapIncrIntrinsic(fn *mir.Function, ii *mir.IntrinsicInstr, destID mir.LocalID, destType scalarType, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (pendingInstr, mir.LocalID, scalarType, bool) {
	if destType != scalarInt || len(ii.Args) != 3 {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	mapPrelude, mapExpr, mapTy, ok := resolveOperandWithPrelude(fn, ii.Args[0], bindings, mctx)
	if !ok || mapTy != scalarOpaquePtr {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	keyPrelude, keyExpr, keyTy, ok := resolveOperandWithPrelude(fn, ii.Args[1], bindings, mctx)
	if !ok {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	deltaPrelude, deltaExpr, deltaTy, ok := resolveOperandWithPrelude(fn, ii.Args[2], bindings, mctx)
	if !ok || deltaTy != scalarInt {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	suffix := runtimeSuffixForScalar(keyTy)
	if suffix == "" {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	symbol := "osty_rt_map_incr_i64_" + suffix
	args := []callArg{{expr: mapExpr, ty: "ptr"}, {expr: keyExpr, ty: keyTy.llvm()}, {expr: deltaExpr, ty: "i64"}}
	declareRuntimePrototype(mctx, symbol, scalarInt, args)
	return pendingInstr{kind: instrCall, prelude: mapPrelude + keyPrelude + deltaPrelude, callSymbol: symbol, callArgs: args}, destID, scalarInt, true
}

func classifySetContainsIntrinsic(fn *mir.Function, ii *mir.IntrinsicInstr, destID mir.LocalID, destType scalarType, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (pendingInstr, mir.LocalID, scalarType, bool) {
	if destType != scalarBool || len(ii.Args) != 2 {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	setPrelude, setExpr, setTy, ok := resolveOperandWithPrelude(fn, ii.Args[0], bindings, mctx)
	if !ok || setTy != scalarOpaquePtr {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	elemPrelude, elemExpr, elemTy, ok := resolveOperandWithPrelude(fn, ii.Args[1], bindings, mctx)
	if !ok {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	symbol := setContainsSymbolFor(elemTy)
	if symbol == "" {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	args := []callArg{{expr: setExpr, ty: "ptr"}, {expr: elemExpr, ty: elemTy.llvm()}}
	declareRuntimePrototype(mctx, symbol, scalarBool, args)
	return pendingInstr{kind: instrCall, prelude: setPrelude + elemPrelude, callSymbol: symbol, callArgs: args}, destID, scalarBool, true
}

func classifySetNewIntrinsic(fn *mir.Function, ii *mir.IntrinsicInstr, destID mir.LocalID, destType scalarType, mctx *moduleCtx) (pendingInstr, mir.LocalID, scalarType, bool) {
	if destType != scalarOpaquePtr || len(ii.Args) != 0 {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	elemType := localCollectionArgScalar(fn, destID, "Set", 0, mctx)
	elemKind, ok := runtimeKindForScalar(elemType)
	if !ok {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	args := []callArg{{expr: fmt.Sprintf("%d", elemKind), ty: "i64"}}
	declareRuntimePrototype(mctx, "osty_rt_set_new", scalarOpaquePtr, args)
	return pendingInstr{kind: instrCall, callSymbol: "osty_rt_set_new", callArgs: args}, destID, scalarOpaquePtr, true
}

func classifyRawNullIntrinsic(ii *mir.IntrinsicInstr, destID mir.LocalID, destType scalarType) (pendingInstr, mir.LocalID, scalarType, bool) {
	if destType != scalarOpaquePtr || len(ii.Args) != 0 {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	return pendingInstr{kind: instrIntrinsic, binDestReg: "null"}, destID, scalarOpaquePtr, true
}

func classifyBranchHintIntrinsic(fn *mir.Function, ii *mir.IntrinsicInstr, destID mir.LocalID, destType scalarType, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (pendingInstr, mir.LocalID, scalarType, bool) {
	if destType != scalarBool || len(ii.Args) != 1 {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	prelude, expr, ty, ok := resolveOperandWithPrelude(fn, ii.Args[0], bindings, mctx)
	if !ok || ty != scalarBool {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	expected := "true"
	if ii.Kind == mir.IntrinsicUnlikely {
		expected = "false"
	}
	declareRuntimePrototype(mctx, "llvm.expect.i1", scalarBool, []callArg{{ty: "i1"}, {ty: "i1"}})
	return pendingInstr{
		kind:       instrCall,
		prelude:    prelude,
		callSymbol: "llvm.expect.i1",
		callArgs: []callArg{
			{expr: expr, ty: "i1"},
			{expr: expected, ty: "i1"},
		},
	}, destID, scalarBool, true
}

func classifyStringConcatIntrinsic(fn *mir.Function, ii *mir.IntrinsicInstr, destID mir.LocalID, destType scalarType, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (pendingInstr, mir.LocalID, scalarType, bool) {
	if destType != scalarString || len(ii.Args) < 2 {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	args := make([]callArg, 0, len(ii.Args))
	var prelude strings.Builder
	for _, op := range ii.Args {
		argPrelude, expr, ty, ok := resolveOperandWithPrelude(fn, op, bindings, mctx)
		if !ok || ty != scalarString {
			return pendingInstr{}, 0, scalarUnknown, false
		}
		prelude.WriteString(argPrelude)
		args = append(args, callArg{expr: expr, ty: "ptr"})
	}
	declareStringConcatRuntime(mctx)
	if len(args) == 2 {
		return pendingInstr{
			kind:       instrCall,
			prelude:    prelude.String(),
			callSymbol: "osty_rt_strings_Concat",
			callArgs:   args,
		}, destID, destType, true
	}
	return pendingInstr{
		kind:       instrCallChain,
		resultType: scalarString,
		prelude:    prelude.String(),
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
	case mir.IntrinsicListReversed:
		return intrinsicRuntimeSpec{"osty_rt_list_reversed", scalarOpaquePtr, []scalarType{scalarOpaquePtr}}, true
	case mir.IntrinsicListSlice:
		return intrinsicRuntimeSpec{"osty_rt_list_slice", scalarOpaquePtr, []scalarType{scalarOpaquePtr, scalarInt, scalarInt}}, true
	case mir.IntrinsicMapLen:
		return intrinsicRuntimeSpec{"osty_rt_map_len", scalarInt, []scalarType{scalarOpaquePtr}}, true
	case mir.IntrinsicMapKeys:
		return intrinsicRuntimeSpec{"osty_rt_map_keys", scalarOpaquePtr, []scalarType{scalarOpaquePtr}}, true
	case mir.IntrinsicMapValues:
		return intrinsicRuntimeSpec{"osty_rt_map_values", scalarOpaquePtr, []scalarType{scalarOpaquePtr}}, true
	case mir.IntrinsicMapToString:
		return intrinsicRuntimeSpec{"osty_rt_map_to_string", scalarString, []scalarType{scalarOpaquePtr}}, true
	case mir.IntrinsicSetLen:
		return intrinsicRuntimeSpec{"osty_rt_set_len", scalarInt, []scalarType{scalarOpaquePtr}}, true
	case mir.IntrinsicSetToList:
		return intrinsicRuntimeSpec{"osty_rt_set_to_list", scalarOpaquePtr, []scalarType{scalarOpaquePtr}}, true
	case mir.IntrinsicSetToString:
		return intrinsicRuntimeSpec{"osty_rt_set_to_string", scalarString, []scalarType{scalarOpaquePtr}}, true
	case mir.IntrinsicBytesLen:
		return intrinsicRuntimeSpec{"osty_rt_bytes_len", scalarInt, []scalarType{scalarOpaquePtr}}, true
	case mir.IntrinsicBytesIsEmpty:
		return intrinsicRuntimeSpec{"osty_rt_bytes_is_empty", scalarBool, []scalarType{scalarOpaquePtr}}, true
	case mir.IntrinsicBytesIndexOf:
		return intrinsicRuntimeSpec{"osty_rt_bytes_index_of", scalarInt, []scalarType{scalarOpaquePtr, scalarOpaquePtr}}, true
	case mir.IntrinsicBytesLastIndexOf:
		return intrinsicRuntimeSpec{"osty_rt_bytes_last_index_of", scalarInt, []scalarType{scalarOpaquePtr, scalarOpaquePtr}}, true
	case mir.IntrinsicBytesSplit:
		return intrinsicRuntimeSpec{"osty_rt_bytes_split", scalarOpaquePtr, []scalarType{scalarOpaquePtr, scalarOpaquePtr}}, true
	case mir.IntrinsicBytesConcat:
		return intrinsicRuntimeSpec{"osty_rt_bytes_concat", scalarOpaquePtr, []scalarType{scalarOpaquePtr, scalarOpaquePtr}}, true
	case mir.IntrinsicBytesRepeat:
		return intrinsicRuntimeSpec{"osty_rt_bytes_repeat", scalarOpaquePtr, []scalarType{scalarOpaquePtr, scalarInt}}, true
	case mir.IntrinsicBytesReplace:
		return intrinsicRuntimeSpec{"osty_rt_bytes_replace", scalarOpaquePtr, []scalarType{scalarOpaquePtr, scalarOpaquePtr, scalarOpaquePtr}}, true
	case mir.IntrinsicBytesReplaceAll:
		return intrinsicRuntimeSpec{"osty_rt_bytes_replace_all", scalarOpaquePtr, []scalarType{scalarOpaquePtr, scalarOpaquePtr, scalarOpaquePtr}}, true
	case mir.IntrinsicBytesTrimLeft:
		return intrinsicRuntimeSpec{"osty_rt_bytes_trim_left", scalarOpaquePtr, []scalarType{scalarOpaquePtr, scalarOpaquePtr}}, true
	case mir.IntrinsicBytesTrimRight:
		return intrinsicRuntimeSpec{"osty_rt_bytes_trim_right", scalarOpaquePtr, []scalarType{scalarOpaquePtr, scalarOpaquePtr}}, true
	case mir.IntrinsicBytesTrim:
		return intrinsicRuntimeSpec{"osty_rt_bytes_trim", scalarOpaquePtr, []scalarType{scalarOpaquePtr, scalarOpaquePtr}}, true
	case mir.IntrinsicBytesTrimSpace:
		return intrinsicRuntimeSpec{"osty_rt_bytes_trim_space", scalarOpaquePtr, []scalarType{scalarOpaquePtr}}, true
	case mir.IntrinsicBytesToUpper:
		return intrinsicRuntimeSpec{"osty_rt_bytes_to_upper", scalarOpaquePtr, []scalarType{scalarOpaquePtr}}, true
	case mir.IntrinsicBytesToLower:
		return intrinsicRuntimeSpec{"osty_rt_bytes_to_lower", scalarOpaquePtr, []scalarType{scalarOpaquePtr}}, true
	case mir.IntrinsicBytesToHex:
		return intrinsicRuntimeSpec{"osty_rt_bytes_to_hex", scalarString, []scalarType{scalarOpaquePtr}}, true
	case mir.IntrinsicBytesSlice:
		return intrinsicRuntimeSpec{"osty_rt_bytes_slice", scalarOpaquePtr, []scalarType{scalarOpaquePtr, scalarInt, scalarInt}}, true
	case mir.IntrinsicBytesFromList:
		return intrinsicRuntimeSpec{"osty_rt_bytes_from_list", scalarOpaquePtr, []scalarType{scalarOpaquePtr}}, true
	case mir.IntrinsicBytesFromString:
		return intrinsicRuntimeSpec{"osty_rt_strings_ToBytes", scalarOpaquePtr, []scalarType{scalarString}}, true
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
func classifyAssignSrc(fn *mir.Function, src mir.RValue, destType scalarType, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (pendingInstr, string, bool) {
	if use, ok := src.(*mir.UseRV); ok {
		prelude, expr, ty, ok := resolveOperandWithPrelude(fn, use.Op, bindings, mctx)
		if !ok || ty != destType {
			return pendingInstr{}, "", false
		}
		if prelude != "" {
			return pendingInstr{kind: instrIntrinsic, intrinsicLine: prelude}, expr, true
		}
		return pendingInstr{kind: instrInline}, expr, true
	}
	if bin, ok := src.(*mir.BinaryRV); ok {
		// Special-case String + Add: lowers to a runtime ABI call
		// (`osty_rt_strings_Concat`) rather than a native LLVM
		// binary instruction.
		if bin.Op == mir.BinAdd && destType == scalarString {
			leftPrelude, left, leftTy, ok := resolveOperandWithPrelude(fn, bin.Left, bindings, mctx)
			if !ok || leftTy != scalarString {
				return pendingInstr{}, "", false
			}
			rightPrelude, right, rightTy, ok := resolveOperandWithPrelude(fn, bin.Right, bindings, mctx)
			if !ok || rightTy != scalarString {
				return pendingInstr{}, "", false
			}
			declareStringConcatRuntime(mctx)
			return pendingInstr{
				kind:       instrCall,
				prelude:    leftPrelude + rightPrelude,
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
			if leftPrelude, left, leftTy, ok := resolveOperandWithPrelude(fn, bin.Left, bindings, mctx); ok && leftTy == scalarString {
				rightPrelude, right, rightTy, ok := resolveOperandWithPrelude(fn, bin.Right, bindings, mctx)
				if !ok || rightTy != scalarString {
					return pendingInstr{}, "", false
				}
				declareStringEqualRuntime(mctx)
				return pendingInstr{
					kind:       instrCall,
					prelude:    leftPrelude + rightPrelude,
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
		leftPrelude, left, leftTy, ok := resolveOperandWithPrelude(fn, bin.Left, bindings, mctx)
		if !ok || leftTy != operandType {
			return pendingInstr{}, "", false
		}
		rightPrelude, right, rightTy, ok := resolveOperandWithPrelude(fn, bin.Right, bindings, mctx)
		if !ok || rightTy != operandType {
			return pendingInstr{}, "", false
		}
		return pendingInstr{
			kind:       instrBinary,
			prelude:    leftPrelude + rightPrelude,
			binOp:      llvmOp,
			binArgType: operandType.llvm(),
			leftExpr:   left,
			rightExpr:  right,
		}, "", true
	}
	if un, ok := src.(*mir.UnaryRV); ok {
		return classifyUnaryAssignSrc(fn, un, destType, bindings, mctx)
	}
	if agg, ok := src.(*mir.AggregateRV); ok {
		return classifyAggregateAssignSrc(fn, agg, destType, bindings, mctx)
	}
	if lenRV, ok := src.(*mir.LenRV); ok {
		return classifyLenRV(fn, lenRV, destType, bindings, mctx)
	}
	return pendingInstr{}, "", false
}

func classifyUnaryAssignSrc(fn *mir.Function, un *mir.UnaryRV, destType scalarType, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (pendingInstr, string, bool) {
	if un == nil {
		return pendingInstr{}, "", false
	}
	prelude, expr, ty, ok := resolveOperandWithPrelude(fn, un.Arg, bindings, mctx)
	if !ok {
		return pendingInstr{}, "", false
	}
	switch un.Op {
	case mir.UnPlus:
		if destType != scalarInt || ty != scalarInt {
			return pendingInstr{}, "", false
		}
		if prelude != "" {
			return pendingInstr{kind: instrIntrinsic, intrinsicLine: prelude}, expr, true
		}
		return pendingInstr{kind: instrInline}, expr, true
	case mir.UnNeg:
		if destType != scalarInt || ty != scalarInt {
			return pendingInstr{}, "", false
		}
		return pendingInstr{
			kind:       instrBinary,
			prelude:    prelude,
			binOp:      "sub",
			binArgType: "i64",
			leftExpr:   "0",
			rightExpr:  expr,
		}, "", true
	case mir.UnNot:
		if destType != scalarBool || ty != scalarBool {
			return pendingInstr{}, "", false
		}
		return pendingInstr{
			kind:       instrBinary,
			prelude:    prelude,
			binOp:      "xor",
			binArgType: "i1",
			leftExpr:   expr,
			rightExpr:  "true",
		}, "", true
	case mir.UnBitNot:
		if destType != scalarInt || ty != scalarInt {
			return pendingInstr{}, "", false
		}
		return pendingInstr{
			kind:       instrBinary,
			prelude:    prelude,
			binOp:      "xor",
			binArgType: "i64",
			leftExpr:   expr,
			rightExpr:  "-1",
		}, "", true
	default:
		return pendingInstr{}, "", false
	}
}

func classifyLenRV(fn *mir.Function, lenRV *mir.LenRV, destType scalarType, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (pendingInstr, string, bool) {
	if lenRV == nil || destType != scalarInt {
		return pendingInstr{}, "", false
	}
	placeTy := placeResultType(fn, lenRV.Place)
	if placeTy == nil {
		return pendingInstr{}, "", false
	}
	prelude, expr, ty, ok := resolveOperandWithPrelude(fn, &mir.CopyOp{Place: lenRV.Place, T: placeTy}, bindings, mctx)
	if !ok {
		return pendingInstr{}, "", false
	}
	switch ty {
	case scalarOpaquePtr:
		declareListRuntime(mctx)
		return pendingInstr{
			kind:       instrCall,
			prelude:    prelude,
			callSymbol: "osty_rt_list_len",
			callArgs:   []callArg{{expr: expr, ty: "ptr"}},
		}, "", true
	case scalarString:
		declareRuntimePrototype(mctx, "osty_rt_strings_ByteLen", scalarInt, []callArg{{ty: "ptr"}})
		return pendingInstr{
			kind:       instrCall,
			prelude:    prelude,
			callSymbol: "osty_rt_strings_ByteLen",
			callArgs:   []callArg{{expr: expr, ty: "ptr"}},
		}, "", true
	default:
		return pendingInstr{}, "", false
	}
}

func classifyAggregateAssignSrc(fn *mir.Function, agg *mir.AggregateRV, destType scalarType, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (pendingInstr, string, bool) {
	if agg != nil && agg.Kind == mir.AggEnumVariant && destType == scalarInt && len(agg.Fields) == 0 {
		return pendingInstr{kind: instrInline}, fmt.Sprintf("%d", agg.VariantIdx), true
	}
	if agg == nil || agg.Kind != mir.AggList || destType != scalarOpaquePtr {
		return pendingInstr{}, "", false
	}
	args := make([]callArg, 0, len(agg.Fields))
	elemType := scalarUnknown
	var prelude strings.Builder
	for _, field := range agg.Fields {
		fieldPrelude, expr, ty, ok := resolveOperandWithPrelude(fn, field, bindings, mctx)
		if !ok || ty == scalarUnknown {
			return pendingInstr{}, "", false
		}
		prelude.WriteString(fieldPrelude)
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
		prelude:        prelude.String(),
		callArgs:       args,
		listPushSymbol: pushSymbol,
	}, "", true
}

func classifyFieldWriteStep(fn *mir.Function, ai *mir.AssignInstr, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (pendingInstr, bool) {
	if ai == nil || !ai.Dest.HasProjections() {
		return pendingInstr{}, false
	}
	slotPrelude, slot, fieldTy, ok := resolveProjectedFieldSlot(fn, ai.Dest, bindings, mctx, "field.store.slot")
	if !ok {
		return pendingInstr{}, false
	}
	valuePrelude, valueExpr, valueTy, ok := resolveStoreRValue(fn, ai.Src, fieldTy, bindings, mctx)
	if !ok || valueTy != fieldTy {
		return pendingInstr{}, false
	}
	line := slotPrelude + valuePrelude + fmt.Sprintf("  store %s %s, ptr %s\n", fieldTy.llvm(), valueExpr, slot)
	return pendingInstr{kind: instrIntrinsic, intrinsicLine: line}, true
}

func resolveStoreRValue(fn *mir.Function, src mir.RValue, destType scalarType, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (string, string, scalarType, bool) {
	if use, ok := src.(*mir.UseRV); ok {
		return resolveOperandWithPrelude(fn, use.Op, bindings, mctx)
	}
	if agg, ok := src.(*mir.AggregateRV); ok && agg.Kind == mir.AggEnumVariant && destType == scalarInt && len(agg.Fields) == 0 {
		return "", fmt.Sprintf("%d", agg.VariantIdx), scalarInt, true
	}
	return "", "", scalarUnknown, false
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

func listGetSymbolFor(elemType scalarType) string {
	switch elemType {
	case scalarInt:
		return "osty_rt_list_get_i64"
	case scalarBool:
		return "osty_rt_list_get_i1"
	case scalarString:
		return "osty_rt_list_get_string"
	case scalarOpaquePtr:
		return "osty_rt_list_get_ptr"
	}
	return ""
}

func listInsertSymbolFor(elemType scalarType) string {
	switch elemType {
	case scalarInt:
		return "osty_rt_list_insert_i64"
	case scalarBool:
		return "osty_rt_list_insert_i1"
	case scalarString:
		return "osty_rt_list_insert_string"
	case scalarOpaquePtr:
		return "osty_rt_list_insert_ptr"
	}
	return ""
}

func runtimeSuffixForScalar(elemType scalarType) string {
	switch elemType {
	case scalarInt:
		return "i64"
	case scalarBool:
		return "i1"
	case scalarString:
		return "string"
	case scalarOpaquePtr:
		return "ptr"
	}
	return ""
}

func sortableRuntimeSuffixFor(elemType scalarType) string {
	switch elemType {
	case scalarInt, scalarBool, scalarString:
		return runtimeSuffixForScalar(elemType)
	}
	return ""
}

func listSortedSymbolFor(elemType scalarType) string {
	suffix := sortableRuntimeSuffixFor(elemType)
	if suffix == "" {
		return ""
	}
	return "osty_rt_list_sorted_" + suffix
}

func listToSetSymbolFor(elemType scalarType) string {
	suffix := runtimeSuffixForScalar(elemType)
	if suffix == "" {
		return ""
	}
	return "osty_rt_list_to_set_" + suffix
}

func listToStringSymbolFor(elemType scalarType) string {
	suffix := sortableRuntimeSuffixFor(elemType)
	if suffix == "" {
		return ""
	}
	return "osty_rt_list_to_string_" + suffix
}

func mapContainsSymbolFor(keyType scalarType) string {
	suffix := runtimeSuffixForScalar(keyType)
	if suffix == "" {
		return ""
	}
	return "osty_rt_map_contains_" + suffix
}

func mapInsertSymbolFor(keyType scalarType) string {
	suffix := runtimeSuffixForScalar(keyType)
	if suffix == "" {
		return ""
	}
	return "osty_rt_map_insert_" + suffix
}

func setContainsSymbolFor(elemType scalarType) string {
	suffix := runtimeSuffixForScalar(elemType)
	if suffix == "" {
		return ""
	}
	return "osty_rt_set_contains_" + suffix
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

func collectionArgScalar(op mir.Operand, name string, index int, mctx *moduleCtx) scalarType {
	if op == nil || index < 0 || mctx == nil {
		return scalarUnknown
	}
	named, ok := op.Type().(*ir.NamedType)
	if !ok || named == nil || named.Name != name || index >= len(named.Args) {
		return scalarUnknown
	}
	return mctx.scalarFromType(named.Args[index], true)
}

func localCollectionArgScalar(fn *mir.Function, id mir.LocalID, name string, index int, mctx *moduleCtx) scalarType {
	if fn == nil || index < 0 || mctx == nil {
		return scalarUnknown
	}
	loc := lookupLocal(fn, id)
	if loc == nil {
		return scalarUnknown
	}
	named, ok := loc.Type.(*ir.NamedType)
	if !ok || named == nil || named.Name != name || index >= len(named.Args) {
		return scalarUnknown
	}
	return mctx.scalarFromType(named.Args[index], true)
}

func runtimeKindForScalar(st scalarType) (int64, bool) {
	switch st {
	case scalarInt:
		return 1, true
	case scalarBool:
		return 2, true
	case scalarOpaquePtr:
		return 4, true
	case scalarString:
		return 5, true
	}
	return 0, false
}

func runtimeSizeForScalar(st scalarType) (int64, bool) {
	switch st {
	case scalarInt, scalarString, scalarOpaquePtr:
		return 8, true
	case scalarBool:
		return 1, true
	}
	return 0, false
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

func resolveOperandWithPrelude(fn *mir.Function, op mir.Operand, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (string, string, scalarType, bool) {
	if cp, ok := op.(*mir.CopyOp); ok && cp.Place.HasProjections() {
		if _, ok := cp.Place.Projections[len(cp.Place.Projections)-1].(*mir.IndexProj); ok {
			return resolveIndexedOperand(fn, cp.Place, bindings, mctx)
		}
		return resolveProjectedFieldOperand(fn, cp.Place, bindings, mctx)
	}
	expr, ty, ok := resolveOperand(op, bindings, mctx)
	return "", expr, ty, ok
}

func resolveIndexedOperand(fn *mir.Function, place mir.Place, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (string, string, scalarType, bool) {
	if fn == nil || mctx == nil || len(place.Projections) == 0 {
		return "", "", scalarUnknown, false
	}
	idxProj, ok := place.Projections[len(place.Projections)-1].(*mir.IndexProj)
	if !ok {
		return "", "", scalarUnknown, false
	}
	elemTy := mctx.scalarFromType(idxProj.ElemType, true)
	if elemTy == scalarUnknown {
		return "", "", scalarUnknown, false
	}
	listPlace := mir.Place{
		Local:       place.Local,
		Projections: append([]mir.Projection(nil), place.Projections[:len(place.Projections)-1]...),
	}
	listTy := placeResultType(fn, listPlace)
	if listTy == nil {
		return "", "", scalarUnknown, false
	}
	listPrelude, listExpr, listScalarTy, ok := resolveOperandWithPrelude(fn, &mir.CopyOp{Place: listPlace, T: listTy}, bindings, mctx)
	if !ok || listScalarTy != scalarOpaquePtr {
		return "", "", scalarUnknown, false
	}
	indexPrelude, indexExpr, indexTy, ok := resolveOperandWithPrelude(fn, idxProj.Index, bindings, mctx)
	if !ok || indexTy != scalarInt {
		return "", "", scalarUnknown, false
	}
	symbol := listGetSymbolFor(elemTy)
	if symbol == "" {
		return "", "", scalarUnknown, false
	}
	declareListGetRuntimeFor(mctx, elemTy)
	value := mctx.freshTempName("list.get")
	prelude := listPrelude + indexPrelude + fmt.Sprintf("  %s = call %s @%s(ptr %s, i64 %s)\n", value, elemTy.llvm(), symbol, listExpr, indexExpr)
	return prelude, value, elemTy, true
}

func resolveProjectedFieldOperand(fn *mir.Function, place mir.Place, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (string, string, scalarType, bool) {
	prelude, slot, fieldTy, ok := resolveProjectedFieldSlot(fn, place, bindings, mctx, "field.slot")
	if !ok {
		return "", "", scalarUnknown, false
	}
	value := mctx.freshTempName("field")
	prelude += fmt.Sprintf("  %s = load %s, ptr %s\n", value, fieldTy.llvm(), slot)
	return prelude, value, fieldTy, true
}

func resolveProjectedFieldSlot(fn *mir.Function, place mir.Place, bindings map[mir.LocalID]localBinding, mctx *moduleCtx, label string) (string, string, scalarType, bool) {
	if fn == nil || mctx == nil || len(place.Projections) == 0 {
		return "", "", scalarUnknown, false
	}
	base, found := bindings[place.Local]
	if !found || !base.defined || base.ty != scalarOpaquePtr {
		return "", "", scalarUnknown, false
	}
	baseLocal := lookupLocal(fn, place.Local)
	if baseLocal == nil {
		return "", "", scalarUnknown, false
	}
	named, ok := baseLocal.Type.(*ir.NamedType)
	if !ok || named == nil || named.Name == "" {
		return "", "", scalarUnknown, false
	}
	currentStruct := named.Name
	currentPtr := base.expr
	var prelude strings.Builder
	for i, proj := range place.Projections {
		fp, ok := proj.(*mir.FieldProj)
		if !ok {
			return "", "", scalarUnknown, false
		}
		fieldTypes, ok := mctx.lookupStructFields(currentStruct)
		if !ok || fp.Index < 0 || fp.Index >= len(fieldTypes) {
			return "", "", scalarUnknown, false
		}
		fieldTy := fieldTypes[fp.Index]
		mctx.emitStructDef(currentStruct, fieldTypes)
		slot := mctx.freshTempName(label)
		fmt.Fprintf(&prelude, "  %s = getelementptr inbounds %%%s, ptr %s, i32 0, i32 %d\n", slot, currentStruct, currentPtr, fp.Index)
		if i == len(place.Projections)-1 {
			return prelude.String(), slot, fieldTy, true
		}
		if fieldTy != scalarOpaquePtr {
			return "", "", scalarUnknown, false
		}
		nextNamed, ok := fp.Type.(*ir.NamedType)
		if !ok || nextNamed == nil || nextNamed.Name == "" {
			return "", "", scalarUnknown, false
		}
		nextPtr := mctx.freshTempName("field.base")
		fmt.Fprintf(&prelude, "  %s = load ptr, ptr %s\n", nextPtr, slot)
		currentStruct = nextNamed.Name
		currentPtr = nextPtr
	}
	return "", "", scalarUnknown, false
}

func placeResultType(fn *mir.Function, place mir.Place) mir.Type {
	if fn == nil {
		return nil
	}
	if len(place.Projections) == 0 {
		loc := lookupLocal(fn, place.Local)
		if loc == nil {
			return nil
		}
		return loc.Type
	}
	switch proj := place.Projections[len(place.Projections)-1].(type) {
	case *mir.FieldProj:
		return proj.Type
	case *mir.TupleProj:
		return proj.Type
	case *mir.IndexProj:
		return proj.ElemType
	case *mir.VariantProj:
		return proj.Type
	case *mir.DerefProj:
		return proj.Type
	default:
		return nil
	}
}

func resolveListReceiverOperand(fn *mir.Function, op mir.Operand, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (string, string, bool) {
	prelude, expr, ty, ok := resolveOperandWithPrelude(fn, op, bindings, mctx)
	if ok && ty == scalarOpaquePtr {
		return prelude, expr, true
	}
	return "", "", false
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
	out.WriteString(pi.prelude)
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

// ---- scalar return chain ----
//
// This matcher covers a common generated CFG for scalar decision
// tables:
//
//	cond0 ? return A : goto cond1
//	cond1 ? return B : goto cond2
//	...
//	return fallback
//
// Unlike P3c, every arm returns directly instead of merging through a
// phi. The false edge may pass through empty goto blocks; condition
// and return blocks may contain regular stage0 scalar steps.

type scalarReturnChainPattern struct {
	retType    scalarType
	paramIDs   []mir.LocalID
	paramTypes []scalarType
	paramNames []string
	rungs      []scalarReturnRung
	arms       []scalarReturnArm
	final      scalarReturnArm
}

type scalarReturnRung struct {
	condEmit  blockEmit
	condExpr  string
	condType  string
	thenLabel string
	elseLabel string
}

type scalarReturnArm struct {
	label   string
	pending []pendingInstr
	retExpr string
}

func matchScalarReturnChain(fn *mir.Function, mctx *moduleCtx) (scalarReturnChainPattern, bool) {
	pat := scalarReturnChainPattern{}
	pat.retType = mctx.scalarFromType(fn.ReturnType, true)
	if pat.retType == scalarUnknown {
		return pat, false
	}
	if len(fn.Params) > 8 || len(fn.Blocks) < 3 {
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
		bindings[pid] = localBinding{expr: "%" + pat.paramNames[i], ty: pat.paramTypes[i], defined: true}
	}

	current := blockByID(fn, fn.Entry)
	if current == nil {
		return pat, false
	}
	nextSSA := 0
	visited := map[mir.BlockID]bool{}
	for {
		if current == nil || visited[current.ID] {
			return pat, false
		}
		visited[current.ID] = true
		branch, ok := current.Term.(*mir.BranchTerm)
		if !ok {
			return pat, false
		}

		condBindings := copyBindings(bindings)
		condEmit := blockEmit{label: scalarChainCondLabel(current, len(pat.rungs))}
		for _, instr := range current.Instrs {
			if !applyStep(fn, instr, condBindings, mctx, &nextSSA, &condEmit) {
				return pat, false
			}
		}
		condExpr, condTy, ok := resolveOperand(branch.Cond, condBindings, mctx)
		if !ok || condTy != scalarBool {
			return pat, false
		}

		thenBlock := blockByID(fn, branch.Then)
		if thenBlock == nil {
			return pat, false
		}
		thenArm, ok := classifyScalarReturnArm(fn, thenBlock, copyBindings(condBindings), mctx, &nextSSA, pat.retType)
		if !ok {
			return pat, false
		}

		rung := scalarReturnRung{
			condEmit:  condEmit,
			condExpr:  condExpr,
			condType:  condTy.llvm(),
			thenLabel: thenArm.label,
		}
		pat.arms = append(pat.arms, thenArm)
		elseBlock := blockByID(fn, branch.Else)
		if elseBlock == nil {
			return pat, false
		}
		trialSSA := nextSSA
		if finalArm, ok := classifyScalarReturnArm(fn, elseBlock, copyBindings(bindings), mctx, &trialSSA, pat.retType); ok {
			nextSSA = trialSSA
			rung.elseLabel = finalArm.label
			pat.rungs = append(pat.rungs, rung)
			pat.final = finalArm
			break
		}
		nextBlock, ok := followScalarChainFalseEdge(fn, branch.Else)
		if !ok || nextBlock == nil {
			return pat, false
		}
		rung.elseLabel = scalarChainCondLabel(nextBlock, len(pat.rungs)+1)
		pat.rungs = append(pat.rungs, rung)
		current = nextBlock
	}
	if len(pat.rungs) == 0 || pat.final.label == "" {
		return pat, false
	}
	return pat, true
}

func scalarChainCondLabel(bb *mir.BasicBlock, index int) string {
	if index == 0 {
		return "entry"
	}
	return blockLabelName(bb.ID, "chain")
}

func followScalarChainFalseEdge(fn *mir.Function, target mir.BlockID) (*mir.BasicBlock, bool) {
	visited := map[mir.BlockID]bool{}
	cursor := target
	for {
		if visited[cursor] {
			return nil, false
		}
		visited[cursor] = true
		bb := blockByID(fn, cursor)
		if bb == nil {
			return nil, false
		}
		switch term := bb.Term.(type) {
		case *mir.BranchTerm:
			return bb, true
		case *mir.GotoTerm:
			if !blockHasOnlyStorageMarkers(bb) {
				return nil, false
			}
			cursor = term.Target
		default:
			return nil, false
		}
	}
}

func blockHasOnlyStorageMarkers(bb *mir.BasicBlock) bool {
	if bb == nil {
		return false
	}
	for _, instr := range bb.Instrs {
		switch instr.(type) {
		case *mir.StorageLiveInstr, *mir.StorageDeadInstr:
			continue
		default:
			return false
		}
	}
	return true
}

func classifyScalarReturnArm(fn *mir.Function, bb *mir.BasicBlock, bindings map[mir.LocalID]localBinding, mctx *moduleCtx, nextSSA *int, retType scalarType) (scalarReturnArm, bool) {
	arm := scalarReturnArm{}
	if bb == nil {
		return arm, false
	}
	switch term := bb.Term.(type) {
	case *mir.ReturnTerm:
	case *mir.GotoTerm:
		target := blockByID(fn, term.Target)
		if target == nil {
			return arm, false
		}
		if _, ok := target.Term.(*mir.ReturnTerm); !ok || !blockHasOnlyStorageMarkers(target) {
			return arm, false
		}
	default:
		return arm, false
	}
	arm.label = blockLabelName(bb.ID, "return")
	emit := blockEmit{label: arm.label}
	for _, instr := range bb.Instrs {
		if !applyStep(fn, instr, bindings, mctx, nextSSA, &emit) {
			return scalarReturnArm{}, false
		}
	}
	retBinding, ok := bindings[fn.ReturnLocal]
	if !ok || !retBinding.defined || retBinding.ty != retType {
		return scalarReturnArm{}, false
	}
	arm.pending = emit.pending
	arm.retExpr = retBinding.expr
	return arm, true
}

func emitScalarReturnChain(out *strings.Builder, fn *mir.Function, pat scalarReturnChainPattern) error {
	retLLVM := pat.retType.llvm()
	fmt.Fprintf(out, "define %s @%s(", retLLVM, fn.Name)
	for i, name := range pat.paramNames {
		if i > 0 {
			out.WriteString(", ")
		}
		fmt.Fprintf(out, "%s %%%s", pat.paramTypes[i].llvm(), name)
	}
	out.WriteString(") {\n")
	for i, rung := range pat.rungs {
		if i > 0 {
			out.WriteString("\n")
		}
		emitBlock(out, rung.condEmit)
		fmt.Fprintf(out, "  br %s %s, label %%%s, label %%%s\n", rung.condType, rung.condExpr, rung.thenLabel, rung.elseLabel)
		out.WriteString("\n")
		emitScalarReturnArm(out, pat.arms[i], retLLVM)
	}
	out.WriteString("\n")
	emitScalarReturnArm(out, pat.final, retLLVM)
	out.WriteString("}\n\n")
	return nil
}

func emitScalarReturnArm(out *strings.Builder, arm scalarReturnArm, retLLVM string) {
	fmt.Fprintf(out, "%s:\n", arm.label)
	for _, pi := range arm.pending {
		emitPendingInstr(out, pi)
	}
	fmt.Fprintf(out, "  ret %s %s\n", retLLVM, arm.retExpr)
}

// ---- short-circuit guard return ----
//
// The lowerer represents `if a || b { return fallback }; value` as a
// short-circuit diamond that computes a temporary Bool, then branches
// to two direct-return arms. This accepts that narrow seven-block
// shape and emits an explicit LLVM phi at the guard merge.

type shortCircuitGuardPattern struct {
	retType    scalarType
	paramIDs   []mir.LocalID
	paramTypes []scalarType
	paramNames []string

	entry        blockEmit
	entryCond    string
	entryCondTyp string
	thenBlk      blockEmit
	elseBlk      blockEmit
	thenExpr     string
	elseExpr     string
	mergeLabel   string
	mergePhi     string
	trueArm      scalarReturnArm
	falseArm     scalarReturnArm
}

func matchShortCircuitGuardReturn(fn *mir.Function, mctx *moduleCtx) (shortCircuitGuardPattern, bool) {
	pat := shortCircuitGuardPattern{}
	pat.retType = mctx.scalarFromType(fn.ReturnType, true)
	if pat.retType == scalarUnknown {
		return pat, false
	}
	if len(fn.Params) > 8 || len(fn.Blocks) != 7 {
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
		bindings[pid] = localBinding{expr: "%" + pat.paramNames[i], ty: pat.paramTypes[i], defined: true}
	}

	entry := blockByID(fn, fn.Entry)
	if entry == nil {
		return pat, false
	}
	entryBranch, ok := entry.Term.(*mir.BranchTerm)
	if !ok {
		return pat, false
	}
	nextSSA := 0
	entryEmit, entryCond, entryCondTy, ok := classifyEntryBlock(fn, entry, bindings, mctx, &nextSSA)
	if !ok || entryCondTy != scalarBool {
		return pat, false
	}

	thenBlock := blockByID(fn, entryBranch.Then)
	elseBlock := blockByID(fn, entryBranch.Else)
	if thenBlock == nil || elseBlock == nil || thenBlock.ID == elseBlock.ID {
		return pat, false
	}
	thenGoto, ok := thenBlock.Term.(*mir.GotoTerm)
	if !ok {
		return pat, false
	}
	elseGoto, ok := elseBlock.Term.(*mir.GotoTerm)
	if !ok || elseGoto.Target != thenGoto.Target {
		return pat, false
	}
	merge := blockByID(fn, thenGoto.Target)
	if merge == nil || merge.ID == entry.ID || merge.ID == thenBlock.ID || merge.ID == elseBlock.ID {
		return pat, false
	}
	if len(merge.Instrs) != 0 {
		return pat, false
	}
	mergeBranch, ok := merge.Term.(*mir.BranchTerm)
	if !ok {
		return pat, false
	}
	mergeCondLocal, ok := copyOperandLocal(mergeBranch.Cond)
	if !ok {
		return pat, false
	}

	thenEmit, thenExpr, ok := classifyGuardValueBlock(fn, thenBlock, copyBindings(bindings), mctx, &nextSSA, mergeCondLocal)
	if !ok {
		return pat, false
	}
	elseEmit, elseExpr, ok := classifyGuardValueBlock(fn, elseBlock, copyBindings(bindings), mctx, &nextSSA, mergeCondLocal)
	if !ok {
		return pat, false
	}

	trueArm, ok := classifyReturnArmFollowingGotos(fn, mergeBranch.Then, copyBindings(bindings), mctx, &nextSSA, pat.retType)
	if !ok {
		return pat, false
	}
	falseArm, ok := classifyReturnArmFollowingGotos(fn, mergeBranch.Else, copyBindings(bindings), mctx, &nextSSA, pat.retType)
	if !ok {
		return pat, false
	}

	pat.entry = entryEmit
	pat.entry.label = "entry"
	pat.entryCond = entryCond
	pat.entryCondTyp = entryCondTy.llvm()
	pat.thenBlk = thenEmit
	pat.thenBlk.label = blockLabelName(thenBlock.ID, "guard.then")
	pat.elseBlk = elseEmit
	pat.elseBlk.label = blockLabelName(elseBlock.ID, "guard.else")
	pat.thenExpr = thenExpr
	pat.elseExpr = elseExpr
	pat.mergeLabel = blockLabelName(merge.ID, "guard.merge")
	pat.mergePhi = mctx.freshTempName("guard")
	pat.trueArm = trueArm
	pat.falseArm = falseArm
	return pat, true
}

func copyOperandLocal(op mir.Operand) (mir.LocalID, bool) {
	cp, ok := op.(*mir.CopyOp)
	if !ok || cp.Place.HasProjections() {
		return 0, false
	}
	return cp.Place.Local, true
}

func classifyGuardValueBlock(fn *mir.Function, bb *mir.BasicBlock, bindings map[mir.LocalID]localBinding, mctx *moduleCtx, nextSSA *int, condLocal mir.LocalID) (blockEmit, string, bool) {
	emit := blockEmit{}
	if bb == nil {
		return emit, "", false
	}
	if _, ok := bb.Term.(*mir.GotoTerm); !ok {
		return emit, "", false
	}
	for _, instr := range bb.Instrs {
		if !applyStep(fn, instr, bindings, mctx, nextSSA, &emit) {
			return blockEmit{}, "", false
		}
	}
	cond, ok := bindings[condLocal]
	if !ok || !cond.defined || cond.ty != scalarBool {
		return blockEmit{}, "", false
	}
	return emit, cond.expr, true
}

func classifyReturnArmFollowingGotos(fn *mir.Function, start mir.BlockID, bindings map[mir.LocalID]localBinding, mctx *moduleCtx, nextSSA *int, retType scalarType) (scalarReturnArm, bool) {
	visited := map[mir.BlockID]bool{}
	cursor := start
	for {
		if visited[cursor] {
			return scalarReturnArm{}, false
		}
		visited[cursor] = true
		bb := blockByID(fn, cursor)
		if bb == nil {
			return scalarReturnArm{}, false
		}
		if _, ok := bb.Term.(*mir.ReturnTerm); ok {
			arm := scalarReturnArm{label: blockLabelName(bb.ID, "return")}
			emit := blockEmit{label: arm.label}
			for _, instr := range bb.Instrs {
				if !applyStep(fn, instr, bindings, mctx, nextSSA, &emit) {
					return scalarReturnArm{}, false
				}
			}
			retBinding, ok := bindings[fn.ReturnLocal]
			if !ok || !retBinding.defined || retBinding.ty != retType {
				return scalarReturnArm{}, false
			}
			arm.pending = emit.pending
			arm.retExpr = retBinding.expr
			return arm, true
		}
		gotoTerm, ok := bb.Term.(*mir.GotoTerm)
		if !ok || !blockHasOnlyStorageMarkers(bb) {
			return scalarReturnArm{}, false
		}
		cursor = gotoTerm.Target
	}
}

func emitShortCircuitGuardReturn(out *strings.Builder, fn *mir.Function, pat shortCircuitGuardPattern) error {
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
	fmt.Fprintf(out, "  br %s %s, label %%%s, label %%%s\n\n", pat.entryCondTyp, pat.entryCond, pat.thenBlk.label, pat.elseBlk.label)

	emitBlock(out, pat.thenBlk)
	fmt.Fprintf(out, "  br label %%%s\n\n", pat.mergeLabel)
	emitBlock(out, pat.elseBlk)
	fmt.Fprintf(out, "  br label %%%s\n\n", pat.mergeLabel)

	fmt.Fprintf(out, "%s:\n", pat.mergeLabel)
	fmt.Fprintf(out, "  %s = phi i1 [%s, %%%s], [%s, %%%s]\n", pat.mergePhi, pat.thenExpr, pat.thenBlk.label, pat.elseExpr, pat.elseBlk.label)
	fmt.Fprintf(out, "  br i1 %s, label %%%s, label %%%s\n\n", pat.mergePhi, pat.trueArm.label, pat.falseArm.label)
	emitScalarReturnArm(out, pat.trueArm, retLLVM)
	out.WriteString("\n")
	emitScalarReturnArm(out, pat.falseArm, retLLVM)
	out.WriteString("}\n\n")
	return nil
}

// ---- short-circuit Bool return ----
//
// Handles the generated shape for `a || b || c` when the expression is
// returned directly. Each short-circuit rung writes the same Bool temp
// from both arms and a merge block branches on it; the final merge
// returns a phi of the last two arms.

type shortCircuitBoolReturnPattern struct {
	paramIDs   []mir.LocalID
	paramTypes []scalarType
	paramNames []string
	entry      blockEmit
	entryCond  string
	rungs      []shortCircuitBoolRung
}

type shortCircuitBoolRung struct {
	thenBlk    blockEmit
	elseBlk    blockEmit
	thenExpr   string
	elseExpr   string
	mergeLabel string
	phiReg     string
	final      bool
}

func matchShortCircuitBoolReturn(fn *mir.Function, mctx *moduleCtx) (shortCircuitBoolReturnPattern, bool) {
	pat := shortCircuitBoolReturnPattern{}
	if mctx.scalarFromType(fn.ReturnType, true) != scalarBool {
		return pat, false
	}
	if len(fn.Params) > 8 || len(fn.Blocks) < 4 {
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
		bindings[pid] = localBinding{expr: "%" + pat.paramNames[i], ty: pat.paramTypes[i], defined: true}
	}

	current := blockByID(fn, fn.Entry)
	if current == nil {
		return pat, false
	}
	nextSSA := 0
	entryEmit, entryCond, entryCondTy, ok := classifyEntryBlock(fn, current, bindings, mctx, &nextSSA)
	if !ok || entryCondTy != scalarBool {
		return pat, false
	}
	pat.entry = entryEmit
	pat.entry.label = "entry"
	pat.entryCond = entryCond

	visited := map[mir.BlockID]bool{}
	for {
		if current == nil || visited[current.ID] {
			return pat, false
		}
		visited[current.ID] = true
		branch, ok := current.Term.(*mir.BranchTerm)
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
		if !ok || elseGoto.Target != thenGoto.Target {
			return pat, false
		}
		merge := blockByID(fn, thenGoto.Target)
		if merge == nil || merge.ID == current.ID || merge.ID == thenBlock.ID || merge.ID == elseBlock.ID {
			return pat, false
		}
		if len(merge.Instrs) != 0 {
			return pat, false
		}

		var (
			targetLocal mir.LocalID
			final       bool
		)
		switch term := merge.Term.(type) {
		case *mir.BranchTerm:
			targetLocal, ok = copyOperandLocal(term.Cond)
			if !ok {
				return pat, false
			}
		case *mir.ReturnTerm:
			targetLocal = fn.ReturnLocal
			final = true
		default:
			return pat, false
		}

		thenEmit, thenExpr, ok := classifyGuardValueBlock(fn, thenBlock, copyBindings(bindings), mctx, &nextSSA, targetLocal)
		if !ok {
			return pat, false
		}
		elseEmit, elseExpr, ok := classifyGuardValueBlock(fn, elseBlock, copyBindings(bindings), mctx, &nextSSA, targetLocal)
		if !ok {
			return pat, false
		}
		rung := shortCircuitBoolRung{
			thenBlk:    thenEmit,
			elseBlk:    elseEmit,
			thenExpr:   thenExpr,
			elseExpr:   elseExpr,
			mergeLabel: blockLabelName(merge.ID, "or.merge"),
			phiReg:     mctx.freshTempName("or"),
			final:      final,
		}
		rung.thenBlk.label = blockLabelName(thenBlock.ID, "or.then")
		rung.elseBlk.label = blockLabelName(elseBlock.ID, "or.else")
		pat.rungs = append(pat.rungs, rung)
		if final {
			break
		}
		current = merge
	}
	if len(pat.rungs) == 0 {
		return pat, false
	}
	return pat, true
}

func emitShortCircuitBoolReturn(out *strings.Builder, fn *mir.Function, pat shortCircuitBoolReturnPattern) error {
	fmt.Fprintf(out, "define i1 @%s(", fn.Name)
	for i, name := range pat.paramNames {
		if i > 0 {
			out.WriteString(", ")
		}
		fmt.Fprintf(out, "%s %%%s", pat.paramTypes[i].llvm(), name)
	}
	out.WriteString(") {\n")
	emitBlock(out, pat.entry)
	fmt.Fprintf(out, "  br i1 %s, label %%%s, label %%%s\n\n", pat.entryCond, pat.rungs[0].thenBlk.label, pat.rungs[0].elseBlk.label)

	for i, rung := range pat.rungs {
		emitBlock(out, rung.thenBlk)
		fmt.Fprintf(out, "  br label %%%s\n\n", rung.mergeLabel)
		emitBlock(out, rung.elseBlk)
		fmt.Fprintf(out, "  br label %%%s\n\n", rung.mergeLabel)
		fmt.Fprintf(out, "%s:\n", rung.mergeLabel)
		fmt.Fprintf(out, "  %s = phi i1 [%s, %%%s], [%s, %%%s]\n", rung.phiReg, rung.thenExpr, rung.thenBlk.label, rung.elseExpr, rung.elseBlk.label)
		if rung.final {
			fmt.Fprintf(out, "  ret i1 %s\n", rung.phiReg)
			break
		}
		next := pat.rungs[i+1]
		fmt.Fprintf(out, "  br i1 %s, label %%%s, label %%%s\n\n", rung.phiReg, next.thenBlk.label, next.elseBlk.label)
	}
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

// ---- P21: blocks=1 multi-param direct call → aggregate return ----
//
// stage0 P21 covers single-block thin-wrapper functions whose body is
// exactly one direct call that returns a struct or tuple value:
//
//	fn wrap(a: T1, b: T2, ...) -> Struct { innerFn(a, b, ...) }
//
// Constraints:
//   - exactly 1 block, terminated with ReturnTerm
//   - return type is struct or tuple (classifyAggregateReturnType)
//   - all params are scalar (Int / Bool / String / opaque-ptr)
//   - exactly 1 non-storage instruction: CallInstr dest=ReturnLocal
//   - callee is a direct FnRef (not indirect / intrinsic)
//   - every call arg resolves to a scalar operand

type directAggregateCallPattern struct {
	typeName   string
	fieldTypes []scalarType
	paramNames []string
	paramTypes []scalarType
	callSymbol string
	callArgs   []callArg
	callPrelude string // multi-line LLVM prelude (e.g. field-read extracts)
}

func matchDirectAggregateCall(fn *mir.Function, mctx *moduleCtx) (directAggregateCallPattern, bool) {
	pat := directAggregateCallPattern{}

	// Return type must be aggregate (struct or tuple).
	typeName, fieldTypes, ok := classifyAggregateReturnType(fn.ReturnType, mctx)
	if !ok {
		return pat, false
	}
	pat.typeName = typeName
	pat.fieldTypes = fieldTypes

	// Single block with ReturnTerm.
	bb, ok := singleBlockReturning(fn)
	if !ok {
		return pat, false
	}

	// Params: all scalar, up to 8.
	if len(fn.Params) > 8 {
		return pat, false
	}
	pat.paramNames = make([]string, len(fn.Params))
	pat.paramTypes = make([]scalarType, len(fn.Params))
	fallbackNames := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	bindings := make(map[mir.LocalID]localBinding, len(fn.Params))
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
		bindings[pid] = localBinding{expr: "%" + pat.paramNames[i], ty: pat.paramTypes[i], defined: true}
	}

	// Exactly one non-storage instruction: CallInstr.
	var callInstr *mir.CallInstr
	for _, instr := range bb.Instrs {
		switch step := instr.(type) {
		case *mir.StorageLiveInstr, *mir.StorageDeadInstr:
			// Skip storage-liveness markers; they carry no LLVM semantics.
			continue
		case *mir.CallInstr:
			if callInstr != nil {
				return pat, false // more than one call
			}
			callInstr = step
		default:
			return pat, false
		}
	}
	if callInstr == nil {
		return pat, false
	}

	// Dest must be the return local (no projections).
	if callInstr.Dest == nil || callInstr.Dest.Local != fn.ReturnLocal || callInstr.Dest.HasProjections() {
		return pat, false
	}

	// Callee must be a direct FnRef.
	ref, ok := callInstr.Callee.(*mir.FnRef)
	if !ok || ref.Symbol == "" {
		return pat, false
	}

	// If the callee has a declared FnType, param count must match.
	if fnTy, ok := ref.Type.(*ir.FnType); ok && fnTy != nil {
		if len(fnTy.Params) != len(callInstr.Args) {
			return pat, false
		}
	}

	// Resolve each argument to a scalar operand.
	var prelude strings.Builder
	args := make([]callArg, 0, len(callInstr.Args))
	for _, op := range callInstr.Args {
		argPrelude, argExpr, argTy, okOp := resolveOperandWithPrelude(fn, op, bindings, mctx)
		if !okOp || argTy == scalarUnknown {
			return pat, false
		}
		prelude.WriteString(argPrelude)
		args = append(args, callArg{expr: argExpr, ty: argTy.llvm()})
	}

	// Declare the callee prototype if it is not a symbol defined in this module.
	if !mctx.knownSymbols[ref.Symbol] {
		declareAggregateFunctionPrototype(mctx, ref.Symbol, typeName, args)
	}

	pat.callSymbol = ref.Symbol
	pat.callArgs = args
	pat.callPrelude = prelude.String()
	return pat, true
}

// declareAggregateFunctionPrototype emits a `declare %TypeName @symbol(...)`
// line into extraDecls for callee functions whose return type is an aggregate
// (struct or tuple) rather than a scalar.
func declareAggregateFunctionPrototype(mctx *moduleCtx, symbol, typeName string, args []callArg) {
	if mctx == nil || symbol == "" || typeName == "" {
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
	fmt.Fprintf(mctx.extraDecls, "declare %%%s @%s(", typeName, symbol)
	for i, a := range args {
		if i > 0 {
			mctx.extraDecls.WriteString(", ")
		}
		mctx.extraDecls.WriteString(a.ty)
	}
	mctx.extraDecls.WriteString(")\n")
}

func emitDirectAggregateCall(out *strings.Builder, fn *mir.Function, pat directAggregateCallPattern, mctx *moduleCtx) error {
	mctx.emitStructDef(pat.typeName, pat.fieldTypes)
	fmt.Fprintf(out, "define %%%s @%s(", pat.typeName, fn.Name)
	for i, name := range pat.paramNames {
		if i > 0 {
			out.WriteString(", ")
		}
		fmt.Fprintf(out, "%s %%%s", pat.paramTypes[i].llvm(), name)
	}
	out.WriteString(") {\n")
	out.WriteString("entry:\n")

	out.WriteString(pat.callPrelude)
	out.WriteString("  %0 = call %")
	out.WriteString(pat.typeName)
	fmt.Fprintf(out, " @%s(", pat.callSymbol)
	for i, a := range pat.callArgs {
		if i > 0 {
			out.WriteString(", ")
		}
		fmt.Fprintf(out, "%s %s", a.ty, a.expr)
	}
	out.WriteString(")\n")
	fmt.Fprintf(out, "  ret %%%s %%0\n", pat.typeName)
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
		if step.Dest.HasProjections() {
			fieldWrite, okField := classifyFieldWriteStep(fn, step, bindings, mctx)
			if !okField {
				return false
			}
			emit.pending = append(emit.pending, fieldWrite)
			return true
		}
		pending, expr, destID, destType, okStep = classifyAssignStep(fn, step, bindings, mctx)
	case *mir.CallInstr:
		if step.Dest == nil {
			line, okCall := classifyVoidCallLine(fn, step, bindings, mctx)
			if !okCall {
				return false
			}
			emit.pending = append(emit.pending, pendingInstr{kind: instrIntrinsic, intrinsicLine: line})
			return true
		}
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
	case instrIntrinsic:
		if pending.binDestReg != "" {
			bindings[destID] = localBinding{expr: pending.binDestReg, ty: destType, defined: true}
		} else {
			bindings[destID] = localBinding{expr: expr, ty: destType, defined: true}
		}
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

// ---- P22: for-in-list loop with counter/accumulator ----
//
// Covers functions whose body is a `for elem in list { ... }` loop that
// either counts matching elements, clones/transforms a list, or does a
// simple scan. Canonical toolchain shapes:
//
//	fn hirCloneStringList(src: List<String>) -> List<String>
//	fn checkHasImportAlias(env: CheckEnv, alias: String) -> Bool
//	fn coreCloneMapLookup(map: CoreCloneMap, oldI: Int) -> Int
//
// 5-block CFG (identical to for-in-range but uses LenRV for bound):
//
//	bb[init]   (GotoTerm → header):  setup; _iter = <list>; _len = LenRV; _idx = 0
//	bb[header] (BranchTerm):         _idx < _len
//	bb[body]   (GotoTerm → post):    _elem = _iter[_idx]; body work
//	bb[post]   (GotoTerm → header):  _idx = _idx + 1
//	bb[exit]   (ReturnTerm):         return accumulator
//
// Parameters and return type may be scalar OR opaque-ptr (List<T>, named
// structs — both lower to `ptr`).
//
// Body work supported:
//   - list_push intrinsic (accumulator pattern)
//   - BinaryRV on scalars (counter/accumulator pattern)
//   - CallInstr returning scalar or opaque ptr (transform pattern)
//   - Field reads (UseRV/BinaryRV of _elem+field or param+field)
//
// Mutable locals that are re-assigned (e.g. counter, _idx) get alloca
// slots; single-assignment locals (e.g. the out-list pointer, _iter,
// _len, _elem) are SSA-bound.

type forInListPattern struct {
	retType    scalarType
	paramIDs   []mir.LocalID
	paramTypes []scalarType
	paramNames []string
	stackDecls []stackDecl // multi-assigned scalars (idx counter, etc.)

	entryBody      string
	headerBody     string
	headerCondExpr string
	bodyBody       string
	postBody       string
	exitBody       string
	finalRetExpr   string

	headerLabel string
	bodyLabel   string
	postLabel   string
	exitLabel   string
}

// multiWrittenLocals returns the set of local IDs that receive more than
// one AssignInstr across all blocks. These need alloca stack slots.
func multiWrittenLocals(fn *mir.Function) map[mir.LocalID]bool {
	counts := map[mir.LocalID]int{}
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		for _, instr := range bb.Instrs {
			if ai, ok := instr.(*mir.AssignInstr); ok && !ai.Dest.HasProjections() {
				counts[ai.Dest.Local]++
			}
		}
	}
	out := map[mir.LocalID]bool{}
	for id, n := range counts {
		if n > 1 {
			out[id] = true
		}
	}
	return out
}

func matchForInListReturn(fn *mir.Function, mctx *moduleCtx) (forInListPattern, bool) {
	pat := forInListPattern{}

	// Return type: scalar or opaque ptr.
	pat.retType = mctx.scalarFromType(fn.ReturnType, true)
	if pat.retType == scalarUnknown {
		return pat, false
	}

	// Params: scalar or opaque ptr, up to 8.
	if len(fn.Params) > 8 {
		return pat, false
	}
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

	// 5-block CFG: init → header → body → post → exit.
	if len(fn.Blocks) != 5 {
		return pat, false
	}
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
	if body.ID == exit.ID || body.ID == entry.ID || body.ID == header.ID {
		return pat, false
	}
	bodyGoto, ok := body.Term.(*mir.GotoTerm)
	if !ok {
		return pat, false
	}
	post := blockByID(fn, bodyGoto.Target)
	if post == nil || post.ID == header.ID || post.ID == body.ID || post.ID == exit.ID || post.ID == entry.ID {
		return pat, false
	}
	postGoto, ok := post.Term.(*mir.GotoTerm)
	if !ok || postGoto.Target != header.ID {
		return pat, false
	}
	if _, ok := exit.Term.(*mir.ReturnTerm); !ok {
		return pat, false
	}

	// Entry block must contain at least one LenRV instruction — this
	// distinguishes for-in-list from for-in-range (range uses BinaryRV
	// for the upper bound).
	hasLenRV := false
	for _, instr := range entry.Instrs {
		if ai, ok := instr.(*mir.AssignInstr); ok {
			if _, ok := ai.Src.(*mir.LenRV); ok {
				hasLenRV = true
				break
			}
		}
	}
	if !hasLenRV {
		return pat, false
	}

	// Stack-allocate scalar locals that are written more than once (idx
	// counter, etc.). Opaque-ptr locals are SSA-bound even when Mut.
	multiWritten := multiWrittenLocals(fn)
	stack := map[mir.LocalID]stackDecl{}
	for _, l := range fn.Locals {
		if l == nil || l.IsParam || l.IsReturn {
			continue
		}
		if !multiWritten[l.ID] {
			continue
		}
		ty := mctx.scalarFromType(l.Type, false) // strict scalar only for stack
		if ty == scalarUnknown || ty == scalarOpaquePtr {
			continue
		}
		decl := stackDecl{
			id:   l.ID,
			name: sanitizeLLVMName(l.Name, fmt.Sprintf("local%d", l.ID)) + ".slot",
			ty:   ty,
		}
		stack[l.ID] = decl
		pat.stackDecls = append(pat.stackDecls, decl)
	}

	// Initial bindings: params + stack pseudo-bindings.
	bindings := make(map[mir.LocalID]localBinding, len(fn.Params)+len(stack))
	for i, pid := range fn.Params {
		bindings[pid] = localBinding{expr: "%" + pat.paramNames[i], ty: pat.paramTypes[i], defined: true}
	}
	for _, sd := range pat.stackDecls {
		bindings[sd.id] = localBinding{expr: "%" + sd.name, ty: sd.ty, defined: true, isStack: true}
	}

	nextSSA := 0
	ctx := &whileLoopEmitCtx{fn: fn, bindings: bindings, stack: stack, mctx: mctx, nextSSA: &nextSSA}

	// Render each block.
	entryStr, ok := forInListBlock(ctx, entry)
	if !ok {
		return pat, false
	}
	pat.entryBody = entryStr

	condExpr, headerStr, ok := forInListHeader(ctx, header, branch.Cond)
	if !ok {
		return pat, false
	}
	pat.headerBody = headerStr
	pat.headerCondExpr = condExpr

	bodyStr, ok := forInListBlock(ctx, body)
	if !ok {
		return pat, false
	}
	pat.bodyBody = bodyStr

	postStr, ok := forInListBlock(ctx, post)
	if !ok {
		return pat, false
	}
	pat.postBody = postStr

	exitStr, finalExpr, ok := forInListExit(ctx, exit, fn.ReturnLocal, pat.retType)
	if !ok {
		return pat, false
	}
	pat.exitBody = exitStr
	pat.finalRetExpr = finalExpr

	pat.headerLabel = blockLabelName(header.ID, "header")
	pat.bodyLabel = blockLabelName(body.ID, "body")
	pat.postLabel = blockLabelName(post.ID, "post")
	pat.exitLabel = blockLabelName(exit.ID, "exit")
	return pat, true
}

// forInListBlock renders all instructions in a basic block using the
// for-in-list extended step handler.
func forInListBlock(ctx *whileLoopEmitCtx, bb *mir.BasicBlock) (string, bool) {
	var out strings.Builder
	for _, instr := range bb.Instrs {
		if !forInListStep(ctx, &out, instr) {
			return "", false
		}
	}
	return out.String(), true
}

// forInListHeader renders the loop header block and resolves the branch
// condition operand.
func forInListHeader(ctx *whileLoopEmitCtx, bb *mir.BasicBlock, cond mir.Operand) (string, string, bool) {
	var out strings.Builder
	for _, instr := range bb.Instrs {
		if !forInListStep(ctx, &out, instr) {
			return "", "", false
		}
	}
	expr, ty, ok := resolveForInListOperand(ctx, &out, cond)
	if !ok || ty != scalarBool {
		return "", "", false
	}
	return expr, out.String(), true
}

// forInListExit renders the exit block and resolves the final return value.
func forInListExit(ctx *whileLoopEmitCtx, bb *mir.BasicBlock, retLocal mir.LocalID, retType scalarType) (string, string, bool) {
	var out strings.Builder
	for _, instr := range bb.Instrs {
		if !forInListStep(ctx, &out, instr) {
			return "", "", false
		}
	}
	b, ok := ctx.bindings[retLocal]
	if !ok || !b.defined {
		return "", "", false
	}
	if b.ty != retType {
		return "", "", false
	}
	if b.isStack {
		expr, _, okLoad := loadFromStack(ctx, &out, retLocal)
		if !okLoad {
			return "", "", false
		}
		return out.String(), expr, true
	}
	return out.String(), b.expr, true
}

// forInListStep dispatches one MIR instruction in the for-in-list context.
// It extends emitWhileStep with LenRV, AggregateRV{AggList}, opaque-ptr
// calls, indexed element access, and list_push intrinsic.
func forInListStep(ctx *whileLoopEmitCtx, out *strings.Builder, instr mir.Instr) bool {
	switch step := instr.(type) {
	case *mir.AssignInstr:
		return forInListAssign(ctx, out, step)
	case *mir.CallInstr:
		return forInListCall(ctx, out, step)
	case *mir.IntrinsicInstr:
		return forInListIntrinsic(ctx, out, step)
	case *mir.StorageLiveInstr, *mir.StorageDeadInstr:
		return true
	}
	return false
}

// forInListAssign handles one AssignInstr in the for-in-list context.
// Extends the while-loop version to support:
//   - LenRV(list) → call i64 @osty_rt_list_len(ptr ...)
//   - AggregateRV{AggList, []} → call ptr @osty_rt_list_new()
//   - UseRV with projection operands (CopyOp + IndexProj / FieldProj)
//   - Opaque-ptr dest types
func forInListAssign(ctx *whileLoopEmitCtx, out *strings.Builder, ai *mir.AssignInstr) bool {
	if ai.Dest.HasProjections() {
		// Field-write through projection — emit as GEP+store if layout known.
		pi, ok := classifyFieldWriteStep(ctx.fn, ai, ctx.bindings, ctx.mctx)
		if !ok {
			return false
		}
		out.WriteString(pi.intrinsicLine)
		return true
	}
	destID := ai.Dest.Local
	destLocal := lookupLocal(ctx.fn, destID)
	if destLocal == nil {
		return false
	}
	destType := ctx.mctx.scalarFromType(destLocal.Type, true)
	if destType == scalarUnknown {
		return false
	}
	isStackDest := false
	if _, ok := ctx.stack[destID]; ok {
		isStackDest = true
	}

	switch src := ai.Src.(type) {
	case *mir.UseRV:
		expr, ty, ok := resolveForInListOperand(ctx, out, src.Op)
		if !ok {
			return false
		}
		// Allow type widening: opaque ptr is compatible with any named type.
		if ty != destType && !(ty == scalarOpaquePtr && destType == scalarOpaquePtr) {
			if ty != destType {
				return false
			}
		}
		if isStackDest {
			fmt.Fprintf(out, "  store %s %s, ptr %%%s\n", destType.llvm(), expr, ctx.stack[destID].name)
			return true
		}
		if existing, found := ctx.bindings[destID]; found && existing.defined && !existing.isStack {
			return false // SSA reassignment
		}
		ctx.bindings[destID] = localBinding{expr: expr, ty: destType, defined: true}
		return true

	case *mir.BinaryRV:
		// String == String → osty_rt_strings_Equal.
		if src.Op == mir.BinEq && destType == scalarBool {
			lPre, lExpr, lTy, lok := resolveOperandWithPrelude(ctx.fn, src.Left, ctx.bindings, ctx.mctx)
			rPre, rExpr, rTy, rok := resolveOperandWithPrelude(ctx.fn, src.Right, ctx.bindings, ctx.mctx)
			if lok && rok && lTy == scalarString && rTy == scalarString {
				declareStringEqualRuntime(ctx.mctx)
				out.WriteString(lPre)
				out.WriteString(rPre)
				reg := freshReg(ctx)
				fmt.Fprintf(out, "  %s = call i1 @osty_rt_strings_Equal(ptr %s, ptr %s)\n", reg, lExpr, rExpr)
				if isStackDest {
					fmt.Fprintf(out, "  store i1 %s, ptr %%%s\n", reg, ctx.stack[destID].name)
					return true
				}
				if existing, found := ctx.bindings[destID]; found && existing.defined && !existing.isStack {
					return false
				}
				ctx.bindings[destID] = localBinding{expr: reg, ty: scalarBool, defined: true}
				return true
			}
		}
		llvmOp, resultType, operandType := classifyBinary(src.Op)
		if llvmOp == "" || resultType != destType {
			return false
		}
		left, leftTy, ok := resolveForInListOperand(ctx, out, src.Left)
		if !ok || leftTy != operandType {
			return false
		}
		right, rightTy, ok := resolveForInListOperand(ctx, out, src.Right)
		if !ok || rightTy != operandType {
			return false
		}
		reg := freshReg(ctx)
		fmt.Fprintf(out, "  %s = %s %s %s, %s\n", reg, llvmOp, operandType.llvm(), left, right)
		if isStackDest {
			fmt.Fprintf(out, "  store %s %s, ptr %%%s\n", destType.llvm(), reg, ctx.stack[destID].name)
			return true
		}
		if existing, found := ctx.bindings[destID]; found && existing.defined && !existing.isStack {
			return false
		}
		ctx.bindings[destID] = localBinding{expr: reg, ty: resultType, defined: true}
		return true

	case *mir.LenRV:
		// _len = LenRV(list) → call i64 @osty_rt_list_len(ptr %list)
		if destType != scalarInt {
			return false
		}
		placeTy := placeResultType(ctx.fn, src.Place)
		if placeTy == nil {
			return false
		}
		listPrelude, listExpr, listTy, ok := resolveOperandWithPrelude(ctx.fn, &mir.CopyOp{Place: src.Place, T: placeTy}, ctx.bindings, ctx.mctx)
		if !ok || listTy != scalarOpaquePtr {
			return false
		}
		declareListRuntime(ctx.mctx)
		out.WriteString(listPrelude)
		reg := freshReg(ctx)
		fmt.Fprintf(out, "  %s = call i64 @osty_rt_list_len(ptr %s)\n", reg, listExpr)
		if isStackDest {
			fmt.Fprintf(out, "  store i64 %s, ptr %%%s\n", reg, ctx.stack[destID].name)
			return true
		}
		if existing, found := ctx.bindings[destID]; found && existing.defined && !existing.isStack {
			return false
		}
		ctx.bindings[destID] = localBinding{expr: reg, ty: scalarInt, defined: true}
		return true

	case *mir.AggregateRV:
		// AggregateRV{AggList, []} → call ptr @osty_rt_list_new()
		if src.Kind != mir.AggList || len(src.Fields) != 0 || destType != scalarOpaquePtr {
			return false
		}
		declareListRuntime(ctx.mctx)
		reg := freshReg(ctx)
		fmt.Fprintf(out, "  %s = call ptr @osty_rt_list_new()\n", reg)
		if existing, found := ctx.bindings[destID]; found && existing.defined && !existing.isStack {
			return false
		}
		ctx.bindings[destID] = localBinding{expr: reg, ty: scalarOpaquePtr, defined: true}
		return true
	}
	return false
}

// forInListCall handles a CallInstr in the for-in-list context. Extends
// emitWhileCall to allow opaque-ptr return types and unknown (external)
// callees whose return type can be inferred from the dest local.
func forInListCall(ctx *whileLoopEmitCtx, out *strings.Builder, ci *mir.CallInstr) bool {
	if ci.Dest == nil || ci.Dest.HasProjections() {
		// Void call or projection dest — decline for now.
		return false
	}
	destID := ci.Dest.Local
	destLocal := lookupLocal(ctx.fn, destID)
	if destLocal == nil {
		return false
	}
	destType := ctx.mctx.scalarFromType(destLocal.Type, true)
	if destType == scalarUnknown {
		return false
	}
	ref, ok := ci.Callee.(*mir.FnRef)
	if !ok || ref.Symbol == "" {
		return false
	}
	// Resolve args — uses the extended operand resolver.
	var argExprs []callArg
	if fnTy, ok := ref.Type.(*ir.FnType); ok && fnTy != nil {
		if len(fnTy.Params) != len(ci.Args) {
			return false
		}
		argExprs = make([]callArg, 0, len(ci.Args))
		for i, op := range ci.Args {
			expr, ty, ok := resolveForInListOperand(ctx, out, op)
			if !ok {
				return false
			}
			paramTy := ctx.mctx.scalarFromType(fnTy.Params[i], true)
			if paramTy == scalarUnknown || paramTy != ty {
				return false
			}
			argExprs = append(argExprs, callArg{expr: expr, ty: ty.llvm()})
		}
		// Declare prototype for unknown symbols.
		if !ctx.mctx.knownSymbols[ref.Symbol] {
			declareFunctionPrototype(ctx.mctx, ref.Symbol, destType, argExprs)
		}
	} else {
		// No FnType (e.g. ErrType callee) — attempt arg resolution.
		argExprs = make([]callArg, 0, len(ci.Args))
		for _, op := range ci.Args {
			expr, ty, ok := resolveForInListOperand(ctx, out, op)
			if !ok || ty == scalarUnknown {
				return false
			}
			argExprs = append(argExprs, callArg{expr: expr, ty: ty.llvm()})
		}
		if !ctx.mctx.knownSymbols[ref.Symbol] {
			declareFunctionPrototype(ctx.mctx, ref.Symbol, destType, argExprs)
		}
	}
	reg := freshReg(ctx)
	fmt.Fprintf(out, "  %s = call %s @%s(", reg, destType.llvm(), ref.Symbol)
	for i, a := range argExprs {
		if i > 0 {
			out.WriteString(", ")
		}
		fmt.Fprintf(out, "%s %s", a.ty, a.expr)
	}
	out.WriteString(")\n")
	if isStack := ctx.stack[destID]; isStack.id != 0 {
		fmt.Fprintf(out, "  store %s %s, ptr %%%s\n", destType.llvm(), reg, isStack.name)
		return true
	}
	if existing, found := ctx.bindings[destID]; found && existing.defined && !existing.isStack {
		return false
	}
	ctx.bindings[destID] = localBinding{expr: reg, ty: destType, defined: true}
	return true
}

// forInListIntrinsic handles IntrinsicInstr in the for-in-list context.
// Supports both void intrinsics (list_push, println) and value-returning
// intrinsics (string_byte_len, list_len, etc.) by delegating to
// classifyIntrinsicValueStep for the latter.
func forInListIntrinsic(ctx *whileLoopEmitCtx, out *strings.Builder, ii *mir.IntrinsicInstr) bool {
	// Value-returning intrinsic: delegate to the shared sequential classifier,
	// then bind the result in the while-loop context.
	if ii.Dest != nil {
		pending, destID, destType, ok := classifyIntrinsicValueStep(ctx.fn, ii, ctx.bindings, ctx.mctx)
		if !ok {
			return false
		}
		reg := freshReg(ctx)
		pending.binDestReg = reg
		var buf strings.Builder
		emitPendingInstr(&buf, pending)
		out.WriteString(buf.String())
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

	// Void intrinsics.
	switch ii.Kind {
	case mir.IntrinsicPrintln:
		if len(ii.Args) != 1 {
			return false
		}
		expr, ty, ok := resolveForInListOperand(ctx, out, ii.Args[0])
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

	case mir.IntrinsicListPush:
		// list_push args = [list_local, elem].
		if len(ii.Args) != 2 {
			return false
		}
		listExpr, listTy, ok := resolveForInListOperand(ctx, out, ii.Args[0])
		if !ok || listTy != scalarOpaquePtr {
			return false
		}
		elemExpr, elemTy, ok := resolveForInListOperand(ctx, out, ii.Args[1])
		if !ok || elemTy == scalarUnknown {
			return false
		}
		sym := listPushSymbolFor(elemTy)
		if sym == "" {
			return false
		}
		declareListRuntime(ctx.mctx)
		declareListPushRuntimeFor(ctx.mctx, elemTy)
		fmt.Fprintf(out, "  call void @%s(ptr %s, %s %s)\n", sym, listExpr, elemTy.llvm(), elemExpr)
		return true
	}
	return false
}

// resolveForInListOperand resolves a MIR operand in the for-in-list
// context. Extends resolveOperandWithLoad to handle CopyOp with
// projections (field reads and indexed element access) using the
// while-loop-aware load machinery so stack-backed locals (e.g. _idx)
// are emitted as `load` instructions rather than returning the slot ptr.
func resolveForInListOperand(ctx *whileLoopEmitCtx, out *strings.Builder, op mir.Operand) (string, scalarType, bool) {
	cp, ok := op.(*mir.CopyOp)
	if !ok || !cp.Place.HasProjections() {
		return resolveOperandWithLoad(ctx, out, op)
	}
	// Determine projection kind from the last projection.
	last := cp.Place.Projections[len(cp.Place.Projections)-1]
	idxProj, isIndex := last.(*mir.IndexProj)
	if !isIndex {
		// Field projection: build a temp bindings map with stack locals
		// materialised so the sequential resolver can see them.
		tempBindings := forInListMaterialiseStack(ctx, out)
		prelude, expr, ty, ok := resolveProjectedFieldOperand(ctx.fn, cp.Place, tempBindings, ctx.mctx)
		if !ok {
			return "", scalarUnknown, false
		}
		out.WriteString(prelude)
		return expr, ty, true
	}
	// Index projection: _iter[_idx].  Resolve both list ptr and index value
	// using the stack-aware loader so _idx gets a `load` if needed.
	listPlace := mir.Place{
		Local:       cp.Place.Local,
		Projections: append([]mir.Projection(nil), cp.Place.Projections[:len(cp.Place.Projections)-1]...),
	}
	listLocalTy := placeResultType(ctx.fn, listPlace)
	if listLocalTy == nil {
		return "", scalarUnknown, false
	}
	listExpr, listTy, ok := resolveOperandWithLoad(ctx, out, &mir.CopyOp{Place: listPlace, T: listLocalTy})
	if !ok || listTy != scalarOpaquePtr {
		return "", scalarUnknown, false
	}
	idxExpr, idxTy, ok := resolveForInListOperand(ctx, out, idxProj.Index)
	if !ok || idxTy != scalarInt {
		return "", scalarUnknown, false
	}
	elemTy := ctx.mctx.scalarFromType(idxProj.ElemType, true)
	if elemTy == scalarUnknown {
		return "", scalarUnknown, false
	}
	sym := listGetSymbolFor(elemTy)
	if sym == "" {
		return "", scalarUnknown, false
	}
	declareListGetRuntimeFor(ctx.mctx, elemTy)
	reg := freshReg(ctx)
	fmt.Fprintf(out, "  %s = call %s @%s(ptr %s, i64 %s)\n", reg, elemTy.llvm(), sym, listExpr, idxExpr)
	return reg, elemTy, true
}

// forInListMaterialiseStack builds a bindings snapshot where every
// stack-backed local has been loaded into a fresh SSA register, so that
// the sequential resolvers (resolveProjectedFieldOperand etc.) can see
// the loaded value rather than the slot pointer.
func forInListMaterialiseStack(ctx *whileLoopEmitCtx, out *strings.Builder) map[mir.LocalID]localBinding {
	snap := copyBindings(ctx.bindings)
	for id, b := range snap {
		if !b.isStack {
			continue
		}
		expr, ty, ok := loadFromStack(ctx, out, id)
		if !ok {
			continue
		}
		snap[id] = localBinding{expr: expr, ty: ty, defined: true}
	}
	return snap
}

func emitForInListReturn(out *strings.Builder, fn *mir.Function, pat forInListPattern) error {
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

// declareListPushRuntimeFor emits a `declare void @osty_rt_list_push_*(ptr, T)`
// prototype for the given element type. declareListRuntime must have been
// called first to emit the list_new / list_len decls.
func declareListPushRuntimeFor(mctx *moduleCtx, elemTy scalarType) {
	sym := listPushSymbolFor(elemTy)
	if sym == "" || mctx == nil {
		return
	}
	key := "__stage0.fn_decl." + sym
	if mctx.emittedStructs[key] {
		return
	}
	mctx.emittedStructs[key] = true
	fmt.Fprintf(mctx.extraDecls, "declare void @%s(ptr, %s)\n", sym, elemTy.llvm())
}

// ---- P23: for-in-list with inner branch → early exit (8-block) ----
//
// Extends P22 to the "scan with early return" shape — the loop body
// evaluates a condition on the current element and immediately returns a
// value when it is true, otherwise advances the index counter.
//
// 8-block CFG:
//
//	bb[init]        (GotoTerm → header):   setup; _iter; _len = LenRV; _idx = 0
//	bb[header]      (BranchTerm):           _idx < _len → {body | loop-exit}
//	bb[body]        (BranchTerm):           _elem = iter[_idx]; inner-cond → {bridge | false-prep}
//	bb[bridge]      (GotoTerm, ≤2 instrs):  (storage markers only) → early-exit
//	bb[early-exit]  (ReturnTerm):           return <found-value>
//	bb[false-prep]  (GotoTerm → post):      optional work (accumulator bump, StorageDead)
//	bb[post]        (GotoTerm → header):    _idx++
//	bb[loop-exit]   (ReturnTerm):           return <default-value>
//
// Both return values (early-exit and loop-exit) must be scalar or
// opaque-ptr and of the same type as the function return.

type forInListEarlyExitPattern struct {
	retType    scalarType
	paramIDs   []mir.LocalID
	paramTypes []scalarType
	paramNames []string
	stackDecls []stackDecl

	// Pre-rendered LLVM for each block (no label line, no terminator).
	entryBody      string
	headerBody     string
	headerCondExpr string
	bodyBody       string
	innerCondExpr  string // i1 expr for the inner branch in body
	falsePrepBody  string // optional work in false path
	postBody       string
	loopExitBody   string
	earlyRetExpr   string // what `ret retType` in the early-exit arm
	loopRetExpr    string // what `ret retType` after the loop

	headerLabel    string
	bodyLabel      string
	falsePrepLabel string
	postLabel      string
	loopExitLabel  string
	earlyExitLabel string
}

func matchForInListEarlyExit(fn *mir.Function, mctx *moduleCtx) (forInListEarlyExitPattern, bool) {
	pat := forInListEarlyExitPattern{}

	pat.retType = mctx.scalarFromType(fn.ReturnType, true)
	if pat.retType == scalarUnknown {
		return pat, false
	}
	if len(fn.Params) > 8 || len(fn.Blocks) != 8 {
		return pat, false
	}

	// Params: scalar or opaque ptr.
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

	// ---- CFG topology ----
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
	headerBranch, ok := header.Term.(*mir.BranchTerm)
	if !ok {
		return pat, false
	}
	// header.Then → body, header.Else → loop-exit.
	body := blockByID(fn, headerBranch.Then)
	loopExit := blockByID(fn, headerBranch.Else)
	if body == nil || loopExit == nil {
		return pat, false
	}
	if _, ok := loopExit.Term.(*mir.ReturnTerm); !ok {
		return pat, false
	}
	bodyBranch, ok := body.Term.(*mir.BranchTerm)
	if !ok {
		return pat, false
	}
	// body.Then → early-exit (ReturnTerm) directly.
	// body.Else → false-prep (GotoTerm) → post (GotoTerm → header).
	// There is typically one additional dead/unreachable GotoTerm block
	// in the 8-block layout; we ignore it after verifying the live CFG.
	earlyExit := blockByID(fn, bodyBranch.Then)
	falsePrep := blockByID(fn, bodyBranch.Else)
	if earlyExit == nil || falsePrep == nil {
		return pat, false
	}
	if _, ok := earlyExit.Term.(*mir.ReturnTerm); !ok {
		return pat, false
	}
	falsePrepGoto, ok := falsePrep.Term.(*mir.GotoTerm)
	if !ok {
		return pat, false
	}
	post := blockByID(fn, falsePrepGoto.Target)
	if post == nil {
		return pat, false
	}
	postGoto, ok := post.Term.(*mir.GotoTerm)
	if !ok || postGoto.Target != header.ID {
		return pat, false
	}

	// Entry must contain LenRV (for-in-list marker).
	hasLenRV := false
	for _, instr := range entry.Instrs {
		if ai, ok := instr.(*mir.AssignInstr); ok {
			if _, ok := ai.Src.(*mir.LenRV); ok {
				hasLenRV = true
				break
			}
		}
	}
	if !hasLenRV {
		return pat, false
	}

	// Stack slots for multi-written scalar locals.
	// The return local (fn.ReturnLocal) is excluded even if written in
	// multiple blocks (e.g., early-exit AND loop-exit) — it is never a
	// mutable loop variable, so it must not get an alloca slot.
	multiWritten := multiWrittenLocals(fn)
	stack := map[mir.LocalID]stackDecl{}
	for _, l := range fn.Locals {
		if l == nil || l.IsParam || l.IsReturn || l.ID == fn.ReturnLocal {
			continue
		}
		if !multiWritten[l.ID] {
			continue
		}
		ty := mctx.scalarFromType(l.Type, false)
		if ty == scalarUnknown || ty == scalarOpaquePtr {
			continue
		}
		decl := stackDecl{
			id:   l.ID,
			name: sanitizeLLVMName(l.Name, fmt.Sprintf("local%d", l.ID)) + ".slot",
			ty:   ty,
		}
		stack[l.ID] = decl
		pat.stackDecls = append(pat.stackDecls, decl)
	}

	// Initial bindings.
	bindings := make(map[mir.LocalID]localBinding, len(fn.Params)+len(stack))
	for i, pid := range fn.Params {
		bindings[pid] = localBinding{expr: "%" + pat.paramNames[i], ty: pat.paramTypes[i], defined: true}
	}
	for _, sd := range pat.stackDecls {
		bindings[sd.id] = localBinding{expr: "%" + sd.name, ty: sd.ty, defined: true, isStack: true}
	}

	nextSSA := 0
	ctx := &whileLoopEmitCtx{fn: fn, bindings: bindings, stack: stack, mctx: mctx, nextSSA: &nextSSA}

	// Render entry block.
	entryStr, ok := forInListBlock(ctx, entry)
	if !ok {
		return pat, false
	}
	pat.entryBody = entryStr

	// Render header (cond block).
	condExpr, headerStr, ok := forInListHeader(ctx, header, headerBranch.Cond)
	if !ok {
		return pat, false
	}
	pat.headerBody = headerStr
	pat.headerCondExpr = condExpr

	// Render body block: element access + inner cond.
	bodyCtxBindings := copyBindings(ctx.bindings)
	bodyCtx := &whileLoopEmitCtx{fn: fn, bindings: bodyCtxBindings, stack: stack, mctx: mctx, nextSSA: ctx.nextSSA}
	var bodyBuf strings.Builder
	for _, instr := range body.Instrs {
		if !forInListStep(bodyCtx, &bodyBuf, instr) {
			return pat, false
		}
	}
	innerExpr, innerTy, ok := resolveForInListOperand(bodyCtx, &bodyBuf, bodyBranch.Cond)
	if !ok || innerTy != scalarBool {
		return pat, false
	}
	// Propagate body bindings (excluding return local) back to main ctx.
	for k, v := range bodyCtxBindings {
		if k != fn.ReturnLocal {
			ctx.bindings[k] = v
		}
	}
	pat.bodyBody = bodyBuf.String()
	pat.innerCondExpr = innerExpr

	// Render early-exit block.
	earlyCtxBindings := copyBindings(ctx.bindings)
	earlyCtx := &whileLoopEmitCtx{fn: fn, bindings: earlyCtxBindings, stack: stack, mctx: mctx, nextSSA: ctx.nextSSA}
	var earlyBuf strings.Builder
	for _, instr := range earlyExit.Instrs {
		if !forInListStep(earlyCtx, &earlyBuf, instr) {
			return pat, false
		}
	}
	earlyBinding, ok := earlyCtxBindings[fn.ReturnLocal]
	if !ok || !earlyBinding.defined || earlyBinding.ty != pat.retType {
		return pat, false
	}
	earlyRetExpr := earlyBinding.expr
	pat.earlyExitLabel = blockLabelName(earlyExit.ID, "early")

	// Render false-prep block (connects body-false → post).
	falsePrepCtxBindings := copyBindings(ctx.bindings)
	falsePrepCtx := &whileLoopEmitCtx{fn: fn, bindings: falsePrepCtxBindings, stack: stack, mctx: mctx, nextSSA: ctx.nextSSA}
	falsePrepStr, ok := forInListBlock(falsePrepCtx, falsePrep)
	if !ok {
		return pat, false
	}
	// Propagate false-prep bindings.
	for k, v := range falsePrepCtxBindings {
		ctx.bindings[k] = v
	}
	pat.falsePrepBody = falsePrepStr
	pat.falsePrepLabel = blockLabelName(falsePrep.ID, "else")

	// Render post block.
	postStr, ok := forInListBlock(ctx, post)
	if !ok {
		return pat, false
	}
	pat.postBody = postStr

	// Render loop-exit block.
	loopExitStr, loopRetExpr, ok := forInListExit(ctx, loopExit, fn.ReturnLocal, pat.retType)
	if !ok {
		return pat, false
	}
	pat.loopExitBody = loopExitStr
	pat.loopRetExpr = loopRetExpr
	pat.earlyRetExpr = earlyRetExpr

	pat.headerLabel = blockLabelName(header.ID, "header")
	pat.bodyLabel = blockLabelName(body.ID, "body")
	pat.postLabel = blockLabelName(post.ID, "post")
	pat.loopExitLabel = blockLabelName(loopExit.ID, "exit")
	return pat, true
}

func emitForInListEarlyExit(out *strings.Builder, fn *mir.Function, pat forInListEarlyExitPattern) error {
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
	fmt.Fprintf(out, "  br i1 %s, label %%%s, label %%%s\n", pat.headerCondExpr, pat.bodyLabel, pat.loopExitLabel)

	out.WriteString("\n")
	fmt.Fprintf(out, "%s:\n", pat.bodyLabel)
	out.WriteString(pat.bodyBody)
	fmt.Fprintf(out, "  br i1 %s, label %%%s, label %%%s\n", pat.innerCondExpr, pat.earlyExitLabel, pat.falsePrepLabel)

	out.WriteString("\n")
	fmt.Fprintf(out, "%s:\n", pat.earlyExitLabel)
	fmt.Fprintf(out, "  ret %s %s\n", retLLVM, pat.earlyRetExpr)

	out.WriteString("\n")
	fmt.Fprintf(out, "%s:\n", pat.falsePrepLabel)
	out.WriteString(pat.falsePrepBody)
	fmt.Fprintf(out, "  br label %%%s\n", pat.postLabel)

	out.WriteString("\n")
	fmt.Fprintf(out, "%s:\n", pat.postLabel)
	out.WriteString(pat.postBody)
	fmt.Fprintf(out, "  br label %%%s\n", pat.headerLabel)

	out.WriteString("\n")
	fmt.Fprintf(out, "%s:\n", pat.loopExitLabel)
	out.WriteString(pat.loopExitBody)
	fmt.Fprintf(out, "  ret %s %s\n", retLLVM, pat.loopRetExpr)
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
	typeName     string       // LLVM type name without `%`
	paramTypes   []scalarType // function parameter scalar types
	paramNames   []string     // sanitised parameter names
	paramIDs     []mir.LocalID
	pending      []pendingInstr
	fieldPrelude string
	fieldExprs   []string     // ordered LLVM operand expressions
	fieldTypes   []scalarType // matching scalar type per field
	insertBase   int
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
			var fieldPrelude strings.Builder
			for i, f := range agg.Fields {
				prelude, expr, ty, ok := classifyAggregateFieldWithBindings(fn, f, bindings, mctx)
				if !ok {
					return pat, false
				}
				if ty != fieldTypes[i] {
					return pat, false
				}
				fieldPrelude.WriteString(prelude)
				pat.fieldExprs[i] = expr
			}
			pat.fieldPrelude = fieldPrelude.String()
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

func classifyAggregateFieldWithBindings(fn *mir.Function, op mir.Operand, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (string, string, scalarType, bool) {
	if con, ok := op.(*mir.ConstOp); ok {
		switch c := con.Const.(type) {
		case *mir.IntConst:
			return "", fmt.Sprintf("%d", c.Value), scalarInt, true
		case *mir.BoolConst:
			if c.Value {
				return "", "true", scalarBool, true
			}
			return "", "false", scalarBool, true
		case *mir.StringConst:
			if mctx == nil {
				return "", "", scalarUnknown, false
			}
			return "", mctx.internStringConst(c.Value), scalarString, true
		}
		return "", "", scalarUnknown, false
	}
	return resolveOperandWithPrelude(fn, op, bindings, mctx)
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
	out.WriteString(pat.fieldPrelude)

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
	declareListGetRuntimeFor(mctx, scalarInt)
	return pat, true
}

// declareListGetRuntimeFor adds the typed `osty_rt_list_get_*`
// declaration to extraDecls (idempotent — sentinel via emittedStructs).
func declareListGetRuntimeFor(mctx *moduleCtx, elemType scalarType) {
	if mctx.emittedStructs == nil {
		mctx.emittedStructs = map[string]bool{}
	}
	symbol := listGetSymbolFor(elemType)
	if symbol == "" {
		return
	}
	key := "__stage0." + symbol
	if mctx.emittedStructs[key] {
		return
	}
	mctx.emittedStructs[key] = true
	fmt.Fprintf(mctx.extraDecls, "declare %s @%s(ptr, i64)\n", elemType.llvm(), symbol)
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
