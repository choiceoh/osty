package stage0

import (
	"errors"
	"fmt"
	"os"
	"strconv"
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

// ListAllDeclinesEnv opts EmitMIR into surveying all declined functions
// in one pass instead of stopping at the first.
//
// Default behaviour: emit functions in order; on the first decline
// return the (single) wrapped ErrUnsupported. Each `osty install-self`
// iteration therefore reveals one blocking site at a time, costing a
// fresh ~10–15 min toolchain build to learn the next.
//
// With `OSTY_STAGE0_LIST_ALL_DECLINES=1`: continue past declines,
// collect every function name + reason, and return a single
// ErrUnsupported-wrapping error that lists them all. The full picture
// arrives in one build; consumers can plan multi-PR unblock waves
// instead of serializing them.
//
// The env var is read once per EmitMIR call. No effect on production
// builds (stage0 is only consulted under OSTY_STAGE0_FALLBACK=1 in the
// first place — see internal/backend/bootstrap.go).
const ListAllDeclinesEnv = "OSTY_STAGE0_LIST_ALL_DECLINES"

func listAllDeclinesEnabled() bool {
	switch strings.TrimSpace(os.Getenv(ListAllDeclinesEnv)) {
	case "1", "true", "TRUE", "True", "on", "ON", "On", "yes", "YES", "Yes":
		return true
	}
	return false
}

// declineReason renders the per-function decline explanation that
// `emitFunction` produced, stripped of the common ErrUnsupported prefix
// so the aggregated diagnostic stays readable. Inputs that are not
// `ErrUnsupported`-shaped fall through to their plain Error() text.
func declineReason(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	prefix := ErrUnsupported.Error() + ": "
	if strings.HasPrefix(msg, prefix) {
		return msg[len(prefix):]
	}
	return msg
}

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

	// Pre-scan for print-family intrinsics so printf/fprintf declares
	// and format strings land in extraDecls before any function body.
	needs := scanPrintNeeds(module, mctx)
	if needs.int {
		mctx.extraDecls.WriteString("@.fmt.stage0.print.int = private unnamed_addr constant [5 x i8] c\"%lld\\00\"\n")
		mctx.extraDecls.WriteString("@.fmt.stage0.println.int = private unnamed_addr constant [6 x i8] c\"%lld\\0A\\00\"\n")
	}
	if needs.str {
		mctx.extraDecls.WriteString("@.fmt.stage0.print.str = private unnamed_addr constant [3 x i8] c\"%s\\00\"\n")
		mctx.extraDecls.WriteString("@.fmt.stage0.println.str = private unnamed_addr constant [4 x i8] c\"%s\\0A\\00\"\n")
	}
	if needs.stdout {
		mctx.extraDecls.WriteString("declare i32 @printf(ptr, ...)\n")
	}
	if needs.stderr {
		mctx.extraDecls.WriteString("@stderr = external global ptr\n")
		mctx.extraDecls.WriteString("declare i32 @fprintf(ptr, ptr, ...)\n")
	}

	listAll := listAllDeclinesEnabled()

	var fnBodies strings.Builder
	emittedMain := false
	var declines []string // function names that declined when listAll is on
	for _, fn := range module.Functions {
		if fn == nil {
			continue
		}
		// Trial-emit into a scratch buffer so a decline doesn't leave
		// half-emitted IR in `fnBodies`.
		var probe strings.Builder
		err := emitFunction(&probe, fn, mctx)
		if err != nil {
			if !listAll {
				return nil, err
			}
			declines = append(declines, fmt.Sprintf("%s: %s", fn.Name, declineReason(err)))
			continue
		}
		fnBodies.WriteString(probe.String())
		if fn.Name == "main" {
			emittedMain = true
		}
	}
	// Aggregated-declines path is taken on `OSTY_STAGE0_LIST_ALL_DECLINES=1`
	// when at least one function declined. We still build the (partial)
	// IR and return it alongside the error so callers that want to
	// validate emit-correctness on the surviving functions (e.g. the
	// audit harness's module-level clang verify) can do so. Pre-existing
	// callers that ignore bytes on err == non-nil are unaffected.
	aggregated := listAll && len(declines) > 0
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
	if aggregated {
		// Return partial IR + aggregated declines error so audit
		// callers can clang-verify whatever did emit while still
		// surfacing the full decline list.
		return []byte(out.String()), fmt.Errorf("%w: %d function(s) declined: %s", ErrUnsupported, len(declines), strings.Join(declines, "; "))
	}
	return []byte(out.String()), nil
}

// moduleCtx threads per-EmitMIR module-level state through every
// matcher and emit helper so future stage0 features (string pool,
// struct layouts, runtime declarations …) can attach without forcing
// another sweep through all matcher signatures.
type moduleCtx struct {
	module *mir.Module
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
	return &moduleCtx{
		module:         module,
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
			// Some structs in the toolchain embed tuple/fn-typed fields
			// that are boxed/pointer-represented at runtime. Stage0 only
			// needs a conservative LLVM type for GEP/loads of scalar
			// fields; treat these as opaque pointers instead of declining.
			switch f.Type.(type) {
			case *ir.TupleType, *ir.FnType:
				st = scalarOpaquePtr
			default:
				return nil, false
			}
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

func (m *moduleCtx) emitOptionBoxDef(payload scalarType) (string, bool) {
	if m == nil || payload == scalarUnknown {
		return "", false
	}
	suffix := scalarBoxSuffixFor(payload)
	if suffix == "" {
		return "", false
	}
	name := "stage0.Option." + suffix
	key := "__stage0.option_box." + name
	if m.emittedStructs[key] {
		return name, true
	}
	m.emittedStructs[key] = true
	fmt.Fprintf(m.extraDecls, "%%%s = type { i64, %s }\n", name, payload.llvm())
	return name, true
}

func (m *moduleCtx) emitResultBoxDef(okTy, errTy scalarType) (string, bool) {
	if m == nil || okTy == scalarUnknown || errTy == scalarUnknown {
		return "", false
	}
	okSuffix := scalarBoxSuffixFor(okTy)
	errSuffix := scalarBoxSuffixFor(errTy)
	if okSuffix == "" || errSuffix == "" {
		return "", false
	}
	name := "stage0.Result." + okSuffix + "." + errSuffix
	key := "__stage0.result_box." + name
	if m.emittedStructs[key] {
		return name, true
	}
	m.emittedStructs[key] = true
	fmt.Fprintf(m.extraDecls, "%%%s = type { i64, %s, %s }\n", name, okTy.llvm(), errTy.llvm())
	return name, true
}

func (m *moduleCtx) emitEnumBoxDef(enumName string, variantIdx int, payload []scalarType) (string, bool) {
	if m == nil || enumName == "" || variantIdx < 0 {
		return "", false
	}
	for _, ty := range payload {
		if ty == scalarUnknown {
			return "", false
		}
	}
	name := fmt.Sprintf("stage0.Enum.%s.%d", sanitizeLLVMName(enumName, "Enum"), variantIdx)
	key := "__stage0.enum_box." + name
	if m.emittedStructs[key] {
		return name, true
	}
	m.emittedStructs[key] = true
	fmt.Fprintf(m.extraDecls, "%%%s = type { i64", name)
	for _, ty := range payload {
		fmt.Fprintf(m.extraDecls, ", %s", ty.llvm())
	}
	m.extraDecls.WriteString(" }\n")
	return name, true
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

// scanPrintNeeds walks every print-family intrinsic in the module and
// reports which scalar argument types and streams are used. The result
// drives the per-format-string decisions in EmitMIR.
type printNeeds struct {
	int    bool
	str    bool
	stdout bool
	stderr bool
}

func scanPrintNeeds(module *mir.Module, mctx *moduleCtx) printNeeds {
	needs := printNeeds{}
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
				if !ok || !isPrintIntrinsic(intr.Kind) {
					continue
				}
				if len(intr.Args) != 1 {
					continue
				}
				if isStderrPrintIntrinsic(intr.Kind) {
					needs.stderr = true
				} else {
					needs.stdout = true
				}
				switch mctx.scalarFromType(intr.Args[0].Type(), true) {
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

func isPrintIntrinsic(kind mir.IntrinsicKind) bool {
	switch kind {
	case mir.IntrinsicPrint, mir.IntrinsicPrintln, mir.IntrinsicEprint, mir.IntrinsicEprintln:
		return true
	}
	return false
}

func isStderrPrintIntrinsic(kind mir.IntrinsicKind) bool {
	return kind == mir.IntrinsicEprint || kind == mir.IntrinsicEprintln
}

func emitFunction(out *strings.Builder, fn *mir.Function, mctx *moduleCtx) error {
	if fn.IsIntrinsic {
		return fmt.Errorf("%w: intrinsic declaration %q", ErrUnsupported, fn.Name)
	}
	if fn.IsExternal {
		return fmt.Errorf("%w: external declaration %q", ErrUnsupported, fn.Name)
	}
	if fn.Name == "main" {
		// Try trivial single-block main first; if it declines (e.g.
		// multi-block main with calls), fall through to the general
		// matchers below so matchGenericScalarCFG can handle it.
		err := emitTrivialMain(out, fn, mctx)
		if err == nil {
			return nil
		}
		// Only fall through for multi-block decline; intrinsic/external
		// errors are real.
		if len(fn.Blocks) <= 1 {
			return err
		}
	}
	// Match attempts

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
	if pat, ok := matchShortCircuitCallFallbackBoolReturn(fn, mctx); ok {
		return emitShortCircuitCallFallbackBoolReturn(out, fn, pat)
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
	if pat, ok := matchGenericScalarCFG(fn, mctx); ok {
		return emitGenericScalarCFG(out, fn, pat)
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
	scalarByte
	scalarChar
	scalarFloat
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
	case scalarByte:
		return "i8"
	case scalarChar:
		return "i32"
	case scalarFloat:
		return "double"
	}
	return ""
}

func (s scalarType) zeroValue() (string, bool) {
	switch s {
	case scalarInt:
		return "0", true
	case scalarBool:
		return "false", true
	case scalarString, scalarOpaquePtr:
		return "null", true
	case scalarFloat:
		return "0.0", true
	}
	return "", false
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
		case ir.PrimByte:
			return scalarByte
		case ir.PrimChar:
			return scalarChar
		case ir.PrimBytes, ir.PrimRawPtr:
			return scalarOpaquePtr
		case ir.PrimFloat, ir.PrimFloat32, ir.PrimFloat64:
			return scalarFloat
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
	if opt, ok := t.(*ir.OptionalType); ok && opt != nil {
		return scalarOpaquePtr
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

func fnConstPayloadlessEnumVariantIndex(op mir.Operand, enumType mir.Type, mctx *moduleCtx) (int, bool) {
	if mctx == nil || mctx.module == nil || mctx.module.Layouts == nil {
		return 0, false
	}
	con, ok := op.(*mir.ConstOp)
	if !ok {
		return 0, false
	}
	fc, ok := con.Const.(*mir.FnConst)
	if !ok || fc == nil {
		return 0, false
	}
	named, ok := enumType.(*ir.NamedType)
	if !ok || named == nil {
		return 0, false
	}
	layout := mctx.module.Layouts.Enums[named.Name]
	if !enumLayoutIsPayloadless(layout) {
		return 0, false
	}
	for _, variant := range layout.Variants {
		if fnConstSymbolMatchesEnumVariant(fc.Symbol, named.Name, variant.Name) {
			return variant.Index, true
		}
	}
	return 0, false
}

func fnConstSymbolMatchesEnumVariant(symbol, enumName, variantName string) bool {
	if symbol == "" || variantName == "" {
		return false
	}
	switch symbol {
	case variantName, enumName + "__" + variantName, enumName + "." + variantName:
		return true
	}
	tail := symbol
	for _, sep := range []string{"__", ".", "/"} {
		if idx := strings.LastIndex(tail, sep); idx >= 0 {
			tail = tail[idx+len(sep):]
		}
	}
	return tail == variantName
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

type aggregateBinding struct {
	typeName string
	reg      string
	fields   []scalarType
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
	case mir.IntrinsicPrint, mir.IntrinsicPrintln, mir.IntrinsicEprint, mir.IntrinsicEprintln:
		return classifyPrintIntrinsicLine(fn, ii, bindings, mctx)
	case mir.IntrinsicAbort:
		if len(ii.Args) != 1 {
			return "", false
		}
		prelude, expr, ty, ok := resolveOperandWithPrelude(fn, ii.Args[0], bindings, mctx)
		if !ok || ty != scalarString {
			return "", false
		}
		declareVoidFunctionPrototype(mctx, "osty_rt_panic", []callArg{{ty: "ptr"}})
		return prelude + fmt.Sprintf("  call void @osty_rt_panic(ptr %s)\n", expr), true
	case mir.IntrinsicListIsEmpty:
		if len(ii.Args) != 1 {
			return "", false
		}
		prelude, expr, ty, ok := resolveOperandWithPrelude(fn, ii.Args[0], bindings, mctx)
		if !ok || ty != scalarOpaquePtr {
			return "", false
		}
		declareListRuntime(mctx)
		lenReg := mctx.freshTempName("list.is_empty.len")
		boolReg := mctx.freshTempName("list.is_empty")
		return prelude +
			fmt.Sprintf("  %s = call i64 @osty_rt_list_len(ptr %s)\n", lenReg, expr) +
			fmt.Sprintf("  %s = icmp eq i64 %s, 0\n", boolReg, lenReg), true
	case mir.IntrinsicStringIsEmpty:
		if len(ii.Args) != 1 {
			return "", false
		}
		prelude, expr, ty, ok := resolveOperandWithPrelude(fn, ii.Args[0], bindings, mctx)
		if !ok || ty != scalarString {
			return "", false
		}
		declareRuntimePrototype(mctx, "osty_rt_strings_ByteLen", scalarInt, []callArg{{ty: "ptr"}})
		lenReg := mctx.freshTempName("string.is_empty.len")
		boolReg := mctx.freshTempName("string.is_empty")
		return prelude +
			fmt.Sprintf("  %s = call i64 @osty_rt_strings_ByteLen(ptr %s)\n", lenReg, expr) +
			fmt.Sprintf("  %s = icmp eq i64 %s, 0\n", boolReg, lenReg), true
	case mir.IntrinsicBytesContains:
		if len(ii.Args) != 2 {
			return "", false
		}
		valuePrelude, valueExpr, valueTy, ok := resolveOperandWithPrelude(fn, ii.Args[0], bindings, mctx)
		if !ok || valueTy != scalarOpaquePtr {
			return "", false
		}
		needlePrelude, needleExpr, needleTy, ok := resolveOperandWithPrelude(fn, ii.Args[1], bindings, mctx)
		if !ok || needleTy != scalarOpaquePtr {
			return "", false
		}
		declareRuntimePrototype(mctx, "osty_rt_bytes_index_of", scalarInt, []callArg{{ty: "ptr"}, {ty: "ptr"}})
		indexReg := mctx.freshTempName("bytes.contains.index")
		boolReg := mctx.freshTempName("bytes.contains")
		return valuePrelude + needlePrelude +
			fmt.Sprintf("  %s = call i64 @osty_rt_bytes_index_of(ptr %s, ptr %s)\n", indexReg, valueExpr, needleExpr) +
			fmt.Sprintf("  %s = icmp ne i64 %s, -1\n", boolReg, indexReg), true
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
		if line, ok := renderListPushBytesIntrinsic(mctx, listExpr, elemExpr, elemTy); ok {
			return listPrelude + elemPrelude + line, true
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
		if line, ok := renderListInsertBytesIntrinsic(mctx, listExpr, indexExpr, elemExpr, elemTy); ok {
			return listPrelude + indexPrelude + elemPrelude + line, true
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
	case mir.IntrinsicListPop:
		if len(ii.Args) != 1 {
			return "", false
		}
		prelude, expr, ty, ok := resolveOperandWithPrelude(fn, ii.Args[0], bindings, mctx)
		if !ok || ty != scalarOpaquePtr {
			return "", false
		}
		declareVoidFunctionPrototype(mctx, "osty_rt_list_pop_discard", []callArg{{ty: "ptr"}})
		return prelude + fmt.Sprintf("  call void @osty_rt_list_pop_discard(ptr %s)\n", expr), true
	case mir.IntrinsicListRemoveAt:
		if len(ii.Args) != 2 {
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
		declareVoidFunctionPrototype(mctx, "osty_rt_list_remove_at_discard", []callArg{{ty: "ptr"}, {ty: "i64"}})
		return listPrelude + indexPrelude + fmt.Sprintf("  call void @osty_rt_list_remove_at_discard(ptr %s, i64 %s)\n", listExpr, indexExpr), true
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
	case mir.IntrinsicMapRemove:
		if len(ii.Args) != 2 {
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
		symbol := mapRemoveSymbolFor(keyTy)
		if symbol == "" {
			return "", false
		}
		args := []callArg{{ty: "ptr"}, {ty: keyTy.llvm()}}
		declareRuntimePrototype(mctx, symbol, scalarBool, args)
		return fmt.Sprintf("%s%s  call i1 @%s(ptr %s, %s %s)\n", mapPrelude, keyPrelude, symbol, mapExpr, keyTy.llvm(), keyExpr), true
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
	case mir.IntrinsicChanSend:
		if len(ii.Args) != 2 {
			return "", false
		}
		chanPrelude, chanExpr, chanTy, ok := resolveOperandWithPrelude(fn, ii.Args[0], bindings, mctx)
		if !ok || chanTy != scalarOpaquePtr {
			return "", false
		}
		valuePrelude, valueExpr, valueTy, ok := resolveOperandWithPrelude(fn, ii.Args[1], bindings, mctx)
		if !ok {
			return "", false
		}
		symbol := chanSendSymbolFor(valueTy)
		if symbol == "" {
			return "", false
		}
		declareVoidFunctionPrototype(mctx, symbol, []callArg{{ty: "ptr"}, {ty: valueTy.llvm()}})
		return fmt.Sprintf("%s%s  call void @%s(ptr %s, %s %s)\n", chanPrelude, valuePrelude, symbol, chanExpr, valueTy.llvm(), valueExpr), true
	case mir.IntrinsicChanClose:
		if len(ii.Args) != 1 {
			return "", false
		}
		prelude, expr, ty, ok := resolveOperandWithPrelude(fn, ii.Args[0], bindings, mctx)
		if !ok || ty != scalarOpaquePtr {
			return "", false
		}
		declareVoidFunctionPrototype(mctx, "osty_rt_thread_chan_close", []callArg{{ty: "ptr"}})
		return prelude + fmt.Sprintf("  call void @osty_rt_thread_chan_close(ptr %s)\n", expr), true
	case mir.IntrinsicGroupCancel:
		if len(ii.Args) != 1 {
			return "", false
		}
		prelude, expr, ty, ok := resolveOperandWithPrelude(fn, ii.Args[0], bindings, mctx)
		if !ok || ty != scalarOpaquePtr {
			return "", false
		}
		declareVoidFunctionPrototype(mctx, "osty_rt_task_group_cancel", []callArg{{ty: "ptr"}})
		return prelude + fmt.Sprintf("  call void @osty_rt_task_group_cancel(ptr %s)\n", expr), true
	case mir.IntrinsicSelectRecv:
		if len(ii.Args) != 3 {
			return "", false
		}
		args, prelude, ok := resolveFixedScalarArgs(fn, ii.Args, bindings, mctx, []scalarType{scalarOpaquePtr, scalarOpaquePtr, scalarOpaquePtr})
		if !ok {
			return "", false
		}
		declareVoidFunctionPrototype(mctx, "osty_rt_select_recv", []callArg{{ty: "ptr"}, {ty: "ptr"}, {ty: "ptr"}})
		return prelude + renderVoidCallLine("osty_rt_select_recv", args), true
	case mir.IntrinsicSelectSend:
		if len(ii.Args) != 4 {
			return "", false
		}
		selectPrelude, selectExpr, selectTy, ok := resolveOperandWithPrelude(fn, ii.Args[0], bindings, mctx)
		if !ok || selectTy != scalarOpaquePtr {
			return "", false
		}
		chanPrelude, chanExpr, chanTy, ok := resolveOperandWithPrelude(fn, ii.Args[1], bindings, mctx)
		if !ok || chanTy != scalarOpaquePtr {
			return "", false
		}
		valuePrelude, valueExpr, valueTy, ok := resolveOperandWithPrelude(fn, ii.Args[2], bindings, mctx)
		if !ok {
			return "", false
		}
		armPrelude, armExpr, armTy, ok := resolveOperandWithPrelude(fn, ii.Args[3], bindings, mctx)
		if !ok || armTy != scalarOpaquePtr {
			return "", false
		}
		symbol := selectSendSymbolFor(valueTy)
		if symbol == "" {
			return "", false
		}
		declareVoidFunctionPrototype(mctx, symbol, []callArg{{ty: "ptr"}, {ty: "ptr"}, {ty: valueTy.llvm()}, {ty: "ptr"}})
		return fmt.Sprintf("%s%s%s%s  call void @%s(ptr %s, ptr %s, %s %s, ptr %s)\n", selectPrelude, chanPrelude, valuePrelude, armPrelude, symbol, selectExpr, chanExpr, valueTy.llvm(), valueExpr, armExpr), true
	case mir.IntrinsicSelectTimeout:
		if len(ii.Args) != 3 {
			return "", false
		}
		args, prelude, ok := resolveFixedScalarArgs(fn, ii.Args, bindings, mctx, []scalarType{scalarOpaquePtr, scalarInt, scalarOpaquePtr})
		if !ok {
			return "", false
		}
		declareVoidFunctionPrototype(mctx, "osty_rt_select_timeout", []callArg{{ty: "ptr"}, {ty: "i64"}, {ty: "ptr"}})
		return prelude + renderVoidCallLine("osty_rt_select_timeout", args), true
	case mir.IntrinsicSelectDefault:
		if len(ii.Args) != 2 {
			return "", false
		}
		args, prelude, ok := resolveFixedScalarArgs(fn, ii.Args, bindings, mctx, []scalarType{scalarOpaquePtr, scalarOpaquePtr})
		if !ok {
			return "", false
		}
		declareVoidFunctionPrototype(mctx, "osty_rt_select_default", []callArg{{ty: "ptr"}, {ty: "ptr"}})
		return prelude + renderVoidCallLine("osty_rt_select_default", args), true
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
	return classifyDiscardedValueIntrinsicLine(fn, ii, bindings, mctx)
}

func classifyDiscardedValueIntrinsicLine(fn *mir.Function, ii *mir.IntrinsicInstr, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (string, bool) {
	if ii == nil || ii.Dest != nil {
		return "", false
	}
	spec, ok := intrinsicRuntimeCallSpec(ii.Kind)
	if !ok || len(spec.args) != len(ii.Args) {
		return "", false
	}
	args, prelude, ok := resolveFixedScalarArgs(fn, ii.Args, bindings, mctx, spec.args)
	if !ok {
		return "", false
	}
	declareRuntimePrototype(mctx, spec.symbol, spec.ret, args)
	return prelude + renderDiscardValueCallLine(spec.symbol, spec.ret, args), true
}

func classifyPrintIntrinsicLine(fn *mir.Function, ii *mir.IntrinsicInstr, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (string, bool) {
	if len(ii.Args) != 1 {
		return "", false
	}
	prelude, expr, ty, ok := resolveOperandWithPrelude(fn, ii.Args[0], bindings, mctx)
	if !ok {
		return "", false
	}
	fmtGlobal, argTy, ok := printFormatFor(ii.Kind, ty)
	if !ok {
		return "", false
	}
	if isStderrPrintIntrinsic(ii.Kind) {
		stderrReg := mctx.freshTempName("stderr")
		return prelude +
			fmt.Sprintf("  %s = load ptr, ptr @stderr\n", stderrReg) +
			fmt.Sprintf("  call i32 (ptr, ptr, ...) @fprintf(ptr %s, ptr %s, %s %s)\n", stderrReg, fmtGlobal, argTy, expr), true
	}
	return prelude + fmt.Sprintf("  call i32 (ptr, ...) @printf(ptr %s, %s %s)\n", fmtGlobal, argTy, expr), true
}

func printFormatFor(kind mir.IntrinsicKind, ty scalarType) (string, string, bool) {
	prefix := "@.fmt.stage0.print"
	if kind == mir.IntrinsicPrintln || kind == mir.IntrinsicEprintln {
		prefix = "@.fmt.stage0.println"
	}
	switch ty {
	case scalarInt:
		return prefix + ".int", "i64", true
	case scalarString:
		return prefix + ".str", "ptr", true
	}
	return "", "", false
}

func resolveFixedScalarArgs(fn *mir.Function, ops []mir.Operand, bindings map[mir.LocalID]localBinding, mctx *moduleCtx, want []scalarType) ([]callArg, string, bool) {
	if len(ops) != len(want) {
		return nil, "", false
	}
	args := make([]callArg, 0, len(ops))
	var prelude strings.Builder
	for i, op := range ops {
		argPrelude, expr, ty, ok := resolveOperandWithPrelude(fn, op, bindings, mctx)
		if !ok || ty != want[i] {
			return nil, "", false
		}
		prelude.WriteString(argPrelude)
		args = append(args, callArg{expr: expr, ty: ty.llvm()})
	}
	return args, prelude.String(), true
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
// stage0 subset described in the package docstring.
func matchSequentialReturn(fn *mir.Function, mctx *moduleCtx) (sequentialPattern, bool) {
	pat := sequentialPattern{}

	// Reject aggregate return types — these belong to P21/matchDirectAggregateCall
	// or the if-else aggregate matchers.
	if _, _, ok := classifyAggregateReturnType(fn.ReturnType, mctx); ok {
		return pat, false
	}

	pat.retType = mctx.scalarFromType(fn.ReturnType, true)
	if pat.retType == scalarUnknown {
		return pat, false
	}
	// P17: lift the historical 2-param ceiling. Toolchain audit shows
	// 4–8 param scalar/String fns dominate the "no matching pattern"
	// bucket; the limit here was a P3a artifact (only `a`/`b` fallback
	// names existed) and not load-bearing for the rest of the matcher.
	if len(fn.Params) > 48 {
		return pat, false
	}

	bindings := map[mir.LocalID]localBinding{}

	// Seed param bindings so subsequent CopyOp resolution works.
	pat.paramIDs = fn.Params
	pat.paramTypes = make([]scalarType, len(fn.Params))
	pat.paramNames = make([]string, len(fn.Params))
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
		pat.paramNames[i] = sanitizeLLVMName(loc.Name, paramFallbackName(i))
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
			if step.Dest.HasProjections() {
				line, okCall := classifyProjectedCallLine(fn, step, bindings, mctx)
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
			if step.Dest.HasProjections() {
				line, okIntr := classifyProjectedIntrinsicLine(fn, step, bindings, mctx)
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
	if len(fn.Params) > 48 {
		return pat, false
	}

	bindings := map[mir.LocalID]localBinding{}
	pat.paramIDs = fn.Params
	pat.paramTypes = make([]scalarType, len(fn.Params))
	pat.paramNames = make([]string, len(fn.Params))
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
		pat.paramNames[i] = sanitizeLLVMName(loc.Name, paramFallbackName(i))
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
			if step.Dest.HasProjections() {
				line, okCall := classifyProjectedCallLine(fn, step, bindings, mctx)
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
			if step.Dest.HasProjections() {
				line, okIntr := classifyProjectedIntrinsicLine(fn, step, bindings, mctx)
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

// isEmptyTupleType reports whether t is an empty TupleType, which
// represents the unit type () in MIR.
func isEmptyTupleType(t mir.Type) bool {
	if tt, ok := t.(*ir.TupleType); ok && tt != nil && len(tt.Elems) == 0 {
		return true
	}
	return false
}

func isUnitOperand(op mir.Operand) bool {
	if op == nil {
		return false
	}
	return isUnitType(op.Type())
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
	allowOpaqueUserNamed := true
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
		if !isErrType(ref.Type) && mctx.scalarFromType(ref.Type, allowOpaqueUserNamed) != destType {
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
	declareFunctionPrototype(mctx, ref.Symbol, destType, args)
	return pendingInstr{
		kind:       instrCall,
		prelude:    prelude.String(),
		callSymbol: ref.Symbol,
		callArgs:   args,
	}, destID, destType, true
}

func classifyProjectedCallLine(fn *mir.Function, ci *mir.CallInstr, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (string, bool) {
	if ci == nil || ci.Dest == nil || !ci.Dest.HasProjections() {
		return "", false
	}
	ref, ok := ci.Callee.(*mir.FnRef)
	if !ok || ref.Symbol == "" {
		return "", false
	}
	destIRType := placeResultType(fn, *ci.Dest)
	if destIRType == nil || isUnitType(destIRType) {
		return "", destIRType != nil
	}
	allowOpaqueUserNamed := true
	destType := mctx.scalarFromType(destIRType, allowOpaqueUserNamed)
	if destType == scalarUnknown {
		return "", false
	}

	var resolved resolvedCallArgs
	if fnTy, ok := ref.Type.(*ir.FnType); ok && fnTy != nil {
		if mctx.scalarFromType(fnTy.Return, allowOpaqueUserNamed) != destType || len(fnTy.Params) != len(ci.Args) {
			return "", false
		}
		args := make([]callArg, 0, len(ci.Args))
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
		resolved = resolvedCallArgs{prelude: prelude.String(), args: args}
	} else {
		if !isErrType(ref.Type) && mctx.scalarFromType(ref.Type, allowOpaqueUserNamed) != destType {
			return "", false
		}
		var okArgs bool
		resolved, okArgs = resolveCallArgsWithoutFnType(fn, ci.Args, bindings, mctx)
		if !okArgs {
			return "", false
		}
	}

	slotPrelude, slot, fieldTy, ok := resolveProjectedFieldSlot(fn, *ci.Dest, bindings, mctx, "call.field.store.slot")
	if !ok || fieldTy != destType {
		return "", false
	}
	declareFunctionPrototype(mctx, ref.Symbol, destType, resolved.args)
	reg := mctx.freshTempName("call.field")
	var line strings.Builder
	line.WriteString(slotPrelude)
	line.WriteString(resolved.prelude)
	fmt.Fprintf(&line, "  %s = call %s @%s(", reg, destType.llvm(), ref.Symbol)
	for i, a := range resolved.args {
		if i > 0 {
			line.WriteString(", ")
		}
		fmt.Fprintf(&line, "%s %s", a.ty, a.expr)
	}
	line.WriteString(")\n")
	fmt.Fprintf(&line, "  store %s %s, ptr %s\n", destType.llvm(), reg, slot)
	return line.String(), true
}

func classifyProjectedIntrinsicLine(fn *mir.Function, ii *mir.IntrinsicInstr, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (string, bool) {
	if ii == nil || ii.Dest == nil || !ii.Dest.HasProjections() {
		return "", false
	}
	destIRType := placeResultType(fn, *ii.Dest)
	if destIRType == nil || isUnitType(destIRType) {
		return "", destIRType != nil
	}
	destType := mctx.scalarFromType(destIRType, true)
	if destType == scalarUnknown {
		return "", false
	}
	spec, ok := intrinsicRuntimeCallSpec(ii.Kind)
	if !ok || spec.ret != destType || len(spec.args) != len(ii.Args) {
		return "", false
	}
	args, prelude, ok := resolveFixedScalarArgs(fn, ii.Args, bindings, mctx, spec.args)
	if !ok {
		return "", false
	}
	slotPrelude, slot, fieldTy, ok := resolveProjectedFieldSlot(fn, *ii.Dest, bindings, mctx, "intrinsic.field.store.slot")
	if !ok || fieldTy != destType {
		return "", false
	}
	declareRuntimePrototype(mctx, spec.symbol, spec.ret, args)
	reg := mctx.freshTempName("intrinsic.field")
	var line strings.Builder
	line.WriteString(slotPrelude)
	line.WriteString(prelude)
	fmt.Fprintf(&line, "  %s = call %s @%s(", reg, destType.llvm(), spec.symbol)
	for i, a := range args {
		if i > 0 {
			line.WriteString(", ")
		}
		fmt.Fprintf(&line, "%s %s", a.ty, a.expr)
	}
	line.WriteString(")\n")
	fmt.Fprintf(&line, "  store %s %s, ptr %s\n", destType.llvm(), reg, slot)
	return line.String(), true
}

func classifyVoidCallLine(fn *mir.Function, ci *mir.CallInstr, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (string, bool) {
	if ci == nil || ci.Dest != nil {
		return "", false
	}
	ref, ok := ci.Callee.(*mir.FnRef)
	if !ok || ref.Symbol == "" {
		return "", false
	}
	allowOpaqueUserNamed := true
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
		declareVoidFunctionPrototype(mctx, ref.Symbol, args)
		return prelude.String() + renderVoidCallLine(ref.Symbol, args), true
	} else {
		resolved, okArgs := resolveCallArgsWithoutFnType(fn, ci.Args, bindings, mctx)
		if !okArgs {
			return "", false
		}
		args = resolved.args
		if !isErrType(ref.Type) {
			if isUnitType(ref.Type) {
				declareVoidFunctionPrototype(mctx, ref.Symbol, args)
				return resolved.prelude + renderVoidCallLine(ref.Symbol, args), true
			}
			retType := mctx.scalarFromType(ref.Type, allowOpaqueUserNamed)
			if retType == scalarUnknown {
				return "", false
			}
			declareFunctionPrototype(mctx, ref.Symbol, retType, args)
			return resolved.prelude + renderDiscardValueCallLine(ref.Symbol, retType, args), true
		}
		declareVoidFunctionPrototype(mctx, ref.Symbol, args)
		return resolved.prelude + renderVoidCallLine(ref.Symbol, args), true
	}
}

func isErrType(t mir.Type) bool {
	_, ok := t.(*ir.ErrType)
	return ok
}

func formatFloatConst(v float64) string {
	s := strconv.FormatFloat(v, 'e', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
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

func declareFunctionPrototypeLLVM(mctx *moduleCtx, symbol string, retLLVM string, args []callArg) {
	if mctx == nil || symbol == "" || retLLVM == "" {
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
	fmt.Fprintf(mctx.extraDecls, "declare %s @%s(", retLLVM, symbol)
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

func renderDiscardValueCallLine(symbol string, retType scalarType, args []callArg) string {
	var b strings.Builder
	fmt.Fprintf(&b, "  call %s @%s(", retType.llvm(), symbol)
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
	if pi, id, ty, ok := classifyPrimitiveConversionIntrinsic(fn, ii, destID, destType, bindings, mctx); ok {
		return pi, id, ty, true
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
	if destType != scalarString || len(ii.Args) == 0 {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	args := make([]callArg, 0, len(ii.Args))
	var prelude strings.Builder
	hasInt := false
	convertInt := len(ii.Args) == 1
	for _, op := range ii.Args {
		argPrelude, arg, originalTy, ok := stringConcatSequentialArg(fn, op, convertInt, bindings, mctx)
		if !ok {
			return pendingInstr{}, 0, scalarUnknown, false
		}
		if originalTy == scalarInt && !convertInt {
			hasInt = true
		}
		prelude.WriteString(argPrelude)
		args = append(args, arg)
	}
	if len(args) == 1 {
		return pendingInstr{
			kind:       instrIntrinsic,
			prelude:    prelude.String(),
			binDestReg: args[0].expr,
		}, destID, destType, true
	}
	declareStringConcatRuntime(mctx)
	if hasInt {
		declareStringConcatI64Runtime(mctx)
	}
	prevTy := args[0].ty
	for _, next := range args[1:] {
		if _, ok := stringConcatSymbolForArgs(prevTy, next.ty); !ok {
			return pendingInstr{}, 0, scalarUnknown, false
		}
		prevTy = scalarString.llvm()
	}
	if len(args) == 2 && !hasInt {
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
	case mir.IntrinsicStringToInt:
		return intrinsicRuntimeSpec{"osty_rt_strings_to_int", scalarOpaquePtr, []scalarType{scalarString}}, true
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
	case mir.IntrinsicListContains:
		return intrinsicRuntimeSpec{"osty_rt_list_contains_str", scalarBool, []scalarType{scalarOpaquePtr, scalarString}}, true
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
	case mir.IntrinsicBytesGet:
		return intrinsicRuntimeSpec{"osty_rt_bytes_get", scalarByte, []scalarType{scalarOpaquePtr, scalarInt}}, true
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
	case mir.IntrinsicBytesToString:
		return intrinsicRuntimeSpec{"osty_rt_bytes_to_string", scalarOpaquePtr, []scalarType{scalarOpaquePtr}}, true
	case mir.IntrinsicResultIsOk:
		return intrinsicRuntimeSpec{"osty_rt_result_is_ok", scalarBool, []scalarType{scalarOpaquePtr}}, true
	case mir.IntrinsicResultIsErr:
		return intrinsicRuntimeSpec{"osty_rt_result_is_err", scalarBool, []scalarType{scalarOpaquePtr}}, true
	case mir.IntrinsicResultUnwrapOr:
		return intrinsicRuntimeSpec{"osty_rt_result_unwrap_or_string", scalarString, []scalarType{scalarOpaquePtr, scalarString}}, true
	case mir.IntrinsicChanMake:
		return intrinsicRuntimeSpec{"osty_rt_thread_chan_make", scalarOpaquePtr, []scalarType{scalarInt}}, true
	case mir.IntrinsicChanIsClosed:
		return intrinsicRuntimeSpec{"osty_rt_thread_chan_is_closed", scalarBool, []scalarType{scalarOpaquePtr}}, true
	case mir.IntrinsicIsCancelled:
		return intrinsicRuntimeSpec{"osty_rt_cancel_is_cancelled", scalarBool, nil}, true
	case mir.IntrinsicGroupIsCancelled:
		return intrinsicRuntimeSpec{"osty_rt_task_group_is_cancelled", scalarBool, []scalarType{scalarOpaquePtr}}, true
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
	if global, ok := src.(*mir.GlobalRefRV); ok {
		expr, ty, ok := resolveGlobalRefRValue(mctx, global)
		if !ok || ty != destType {
			return pendingInstr{}, "", false
		}
		return pendingInstr{kind: instrInline}, expr, true
	}
	if bin, ok := src.(*mir.BinaryRV); ok {
		// The current MIR builder represents negative integer
		// literals in expression position as `Unit - value`. The
		// Unit operand carries no runtime value, so lower it as
		// `0 - value`.
		if bin.Op == mir.BinSub && destType == scalarInt && isUnitOperand(bin.Left) {
			rightPrelude, right, rightTy, ok := resolveOperandWithPrelude(fn, bin.Right, bindings, mctx)
			if !ok || rightTy != scalarInt {
				return pendingInstr{}, "", false
			}
			return pendingInstr{
				kind:       instrBinary,
				prelude:    rightPrelude,
				binOp:      "sub",
				binArgType: "i64",
				leftExpr:   "0",
				rightExpr:  right,
			}, "", true
		}
		// Special-case String + Add: lowers to a runtime ABI call
		// (`osty_rt_strings_Concat`) rather than a native LLVM
		// binary instruction.
		if bin.Op == mir.BinAdd && destType == scalarString {
			leftPrelude, leftArg, leftOriginalTy, ok := stringConcatSequentialArg(fn, bin.Left, false, bindings, mctx)
			if !ok {
				return pendingInstr{}, "", false
			}
			rightPrelude, rightArg, rightOriginalTy, ok := stringConcatSequentialArg(fn, bin.Right, false, bindings, mctx)
			if !ok {
				return pendingInstr{}, "", false
			}
			symbol, ok := stringConcatSymbolForArgs(leftArg.ty, rightArg.ty)
			if !ok {
				return pendingInstr{}, "", false
			}
			declareStringConcatRuntime(mctx)
			if leftOriginalTy == scalarInt || rightOriginalTy == scalarInt {
				declareStringConcatI64Runtime(mctx)
			}
			return pendingInstr{
				kind:       instrCall,
				prelude:    leftPrelude + rightPrelude,
				callSymbol: symbol,
				callArgs:   []callArg{leftArg, rightArg},
			}, "", true
		}
		// P16 — Special-case String ==/!= String: lowers to a runtime
		// call (`osty_rt_strings_Equal`) returning i1. Falls through
		// to classifyBinary when the operand isn't String so Int ==
		// Int continues through `icmp eq`.
		if (bin.Op == mir.BinEq || bin.Op == mir.BinNeq) && destType == scalarBool {
			if pending, ok := classifyPayloadlessEnumFnConstCompare(fn, bin, bindings, mctx); ok {
				return pending, "", true
			}
			if leftPrelude, left, leftTy, ok := resolveOperandWithPrelude(fn, bin.Left, bindings, mctx); ok && leftTy == scalarString {
				rightPrelude, right, rightTy, ok := resolveOperandWithPrelude(fn, bin.Right, bindings, mctx)
				if !ok || rightTy != scalarString {
					return pendingInstr{}, "", false
				}
				declareStringEqualRuntime(mctx)
				if bin.Op == mir.BinNeq {
					eqReg := mctx.freshTempName("string.neq.eq")
					neqReg := mctx.freshTempName("string.neq")
					return pendingInstr{
						kind:       instrIntrinsic,
						binDestReg: neqReg,
						intrinsicLine: leftPrelude + rightPrelude +
							fmt.Sprintf("  %s = call i1 @osty_rt_strings_Equal(ptr %s, ptr %s)\n", eqReg, left, right) +
							fmt.Sprintf("  %s = xor i1 %s, true\n", neqReg, eqReg),
					}, "", true
				}
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
			if leftPrelude, left, leftTy, ok := resolveOperandWithPrelude(fn, bin.Left, bindings, mctx); ok && leftTy == scalarBool {
				rightPrelude, right, rightTy, ok := resolveOperandWithPrelude(fn, bin.Right, bindings, mctx)
				if !ok || rightTy != scalarBool {
					return pendingInstr{}, "", false
				}
				op := "icmp eq"
				if bin.Op == mir.BinNeq {
					op = "icmp ne"
				}
				return pendingInstr{
					kind:       instrBinary,
					prelude:    leftPrelude + rightPrelude,
					binOp:      op,
					binArgType: "i1",
					leftExpr:   left,
					rightExpr:  right,
				}, "", true
			}
		}
		if pred := stringComparePredicate(bin.Op); pred != "" && destType == scalarBool && mctx.scalarFromType(bin.Left.Type(), true) == scalarString {
			leftPrelude, left, leftTy, ok := resolveOperandWithPrelude(fn, bin.Left, bindings, mctx)
			if !ok || leftTy != scalarString {
				return pendingInstr{}, "", false
			}
			rightPrelude, right, rightTy, ok := resolveOperandWithPrelude(fn, bin.Right, bindings, mctx)
			if !ok || rightTy != scalarString {
				return pendingInstr{}, "", false
			}
			declareStringCompareRuntime(mctx)
			cmpReg := mctx.freshTempName("string.cmp")
			predReg := mctx.freshTempName("string.cmp.pred")
			return pendingInstr{
				kind:       instrIntrinsic,
				binDestReg: predReg,
				intrinsicLine: leftPrelude + rightPrelude +
					fmt.Sprintf("  %s = call i64 @osty_rt_strings_Compare(ptr %s, ptr %s)\n", cmpReg, left, right) +
					fmt.Sprintf("  %s = icmp %s i64 %s, 0\n", predReg, pred, cmpReg),
			}, "", true
		}
		if pred := byteComparePredicate(bin.Op); pred != "" && destType == scalarBool && isUnsignedOrdinalScalar(mctx.scalarFromType(bin.Left.Type(), true)) {
			leftPrelude, left, leftTy, ok := resolveOperandWithPrelude(fn, bin.Left, bindings, mctx)
			if !ok || !isUnsignedOrdinalScalar(leftTy) {
				return pendingInstr{}, "", false
			}
			rightPrelude, right, rightTy, ok := resolveOperandWithPrelude(fn, bin.Right, bindings, mctx)
			if !ok || rightTy != leftTy {
				return pendingInstr{}, "", false
			}
			return pendingInstr{
				kind:       instrBinary,
				prelude:    leftPrelude + rightPrelude,
				binOp:      "icmp " + pred,
				binArgType: leftTy.llvm(),
				leftExpr:   left,
				rightExpr:  right,
			}, "", true
		}
		llvmOp, resultType, operandType := classifyBinary(bin.Op)
		if llvmOp != "" && resultType != destType && destType == scalarFloat {
			llvmOp2, resultType2, operandType2 := classifyBinaryForType(bin.Op, scalarFloat)
			if llvmOp2 != "" && resultType2 == destType {
				llvmOp, resultType, operandType = llvmOp2, resultType2, operandType2
			}
		}
		if llvmOp == "" || resultType != destType {
			return pendingInstr{}, "", false
		}
		leftPrelude, left, leftTy, ok := resolveOperandWithPrelude(fn, bin.Left, bindings, mctx)
		if !ok || leftTy != operandType {
			if ok && leftTy == scalarFloat && operandType == scalarInt {
				llvmOp2, resultType2, operandType2 := classifyBinaryForType(bin.Op, scalarFloat)
				if llvmOp2 == "" || resultType2 != destType {
					return pendingInstr{}, "", false
				}
				if rightPrelude2, right2, rightTy2, ok2 := resolveOperandWithPrelude(fn, bin.Right, bindings, mctx); ok2 && rightTy2 == operandType2 {
					return pendingInstr{
						kind:       instrBinary,
						prelude:    leftPrelude + rightPrelude2,
						binOp:      llvmOp2,
						binArgType: operandType2.llvm(),
						leftExpr:   left,
						rightExpr:  right2,
					}, "", true
				}
			}
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

func classifyPayloadlessEnumFnConstCompare(fn *mir.Function, bin *mir.BinaryRV, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (pendingInstr, bool) {
	if bin == nil || (bin.Op != mir.BinEq && bin.Op != mir.BinNeq) {
		return pendingInstr{}, false
	}
	leftPrelude, left, leftTy, ok := resolveOperandWithPrelude(fn, bin.Left, bindings, mctx)
	if ok && leftTy == scalarInt {
		if idx, ok := fnConstPayloadlessEnumVariantIndex(bin.Right, bin.Left.Type(), mctx); ok {
			op := "icmp eq"
			if bin.Op == mir.BinNeq {
				op = "icmp ne"
			}
			return pendingInstr{
				kind:       instrBinary,
				prelude:    leftPrelude,
				binOp:      op,
				binArgType: "i64",
				leftExpr:   left,
				rightExpr:  fmt.Sprintf("%d", idx),
			}, true
		}
	}
	rightPrelude, right, rightTy, ok := resolveOperandWithPrelude(fn, bin.Right, bindings, mctx)
	if ok && rightTy == scalarInt {
		if idx, ok := fnConstPayloadlessEnumVariantIndex(bin.Left, bin.Right.Type(), mctx); ok {
			op := "icmp eq"
			if bin.Op == mir.BinNeq {
				op = "icmp ne"
			}
			return pendingInstr{
				kind:       instrBinary,
				prelude:    rightPrelude,
				binOp:      op,
				binArgType: "i64",
				leftExpr:   fmt.Sprintf("%d", idx),
				rightExpr:  right,
			}, true
		}
	}
	return pendingInstr{}, false
}

func resolveGlobalRefRValue(mctx *moduleCtx, rv *mir.GlobalRefRV) (string, scalarType, bool) {
	if mctx == nil || mctx.module == nil || rv == nil || rv.Name == "" {
		return "", scalarUnknown, false
	}
	var global *mir.Global
	for _, g := range mctx.module.Globals {
		if g != nil && g.Name == rv.Name {
			global = g
			break
		}
	}
	if global == nil || global.Init == nil {
		return "", scalarUnknown, false
	}
	expected := mctx.scalarFromType(rv.T, true)
	if expected == scalarUnknown {
		expected = mctx.scalarFromType(global.Type, true)
	}
	con, ok := globalInitConst(global.Init)
	if !ok {
		return "", scalarUnknown, false
	}
	switch c := con.Const.(type) {
	case *mir.StringConst:
		if expected != scalarString && expected != scalarOpaquePtr {
			return "", scalarUnknown, false
		}
		return mctx.internStringConst(c.Value), expected, true
	case *mir.IntConst:
		if expected != scalarInt {
			return "", scalarUnknown, false
		}
		return fmt.Sprintf("%d", c.Value), scalarInt, true
	case *mir.BoolConst:
		if expected != scalarBool {
			return "", scalarUnknown, false
		}
		if c.Value {
			return "true", scalarBool, true
		}
		return "false", scalarBool, true
	case *mir.ByteConst:
		if expected != scalarByte {
			return "", scalarUnknown, false
		}
		return fmt.Sprintf("%d", c.Value), scalarByte, true
	case *mir.CharConst:
		if expected != scalarChar {
			return "", scalarUnknown, false
		}
		return fmt.Sprintf("%d", c.Value), scalarChar, true
	case *mir.FloatConst:
		if expected != scalarFloat {
			return "", scalarUnknown, false
		}
		return formatFloatConst(c.Value), scalarFloat, true
	default:
		return "", scalarUnknown, false
	}
}

func globalInitConst(fn *mir.Function) (*mir.ConstOp, bool) {
	if fn == nil {
		return nil, false
	}
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		for _, instr := range bb.Instrs {
			ai, ok := instr.(*mir.AssignInstr)
			if !ok || ai.Dest.Local != fn.ReturnLocal || ai.Dest.HasProjections() {
				continue
			}
			use, ok := ai.Src.(*mir.UseRV)
			if !ok {
				return nil, false
			}
			con, ok := use.Op.(*mir.ConstOp)
			return con, ok
		}
	}
	return nil, false
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
	if isPrimType(placeTy, ir.PrimBytes) && ty == scalarOpaquePtr {
		declareRuntimePrototype(mctx, "osty_rt_bytes_len", scalarInt, []callArg{{ty: "ptr"}})
		return pendingInstr{
			kind:       instrCall,
			prelude:    prelude,
			callSymbol: "osty_rt_bytes_len",
			callArgs:   []callArg{{expr: expr, ty: "ptr"}},
		}, "", true
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

func listSetSymbolFor(elemType scalarType) string {
	switch elemType {
	case scalarInt:
		return "osty_rt_list_set_i64"
	case scalarBool:
		return "osty_rt_list_set_i1"
	case scalarString:
		return "osty_rt_list_set_string"
	case scalarOpaquePtr:
		return "osty_rt_list_set_ptr"
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

func mapRemoveSymbolFor(keyType scalarType) string {
	suffix := runtimeSuffixForScalar(keyType)
	if suffix == "" {
		return ""
	}
	return "osty_rt_map_remove_" + suffix
}

func mapGetSymbolFor(keyType scalarType) string {
	suffix := runtimeSuffixForScalar(keyType)
	if suffix == "" {
		return ""
	}
	return "osty_rt_map_get_" + suffix
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
	suffix := runtimeSuffixForScalar(elemType)
	if suffix == "" {
		return ""
	}
	return prefix + suffix
}

func chanSendSymbolFor(elemType scalarType) string {
	suffix := scalarSlotSuffixFor(elemType)
	if suffix == "" {
		return ""
	}
	return "osty_rt_thread_chan_send_" + suffix
}

func selectSendSymbolFor(elemType scalarType) string {
	suffix := scalarSlotSuffixFor(elemType)
	if suffix == "" {
		return ""
	}
	return "osty_rt_select_send_" + suffix
}

func scalarSlotSuffixFor(elemType scalarType) string {
	switch elemType {
	case scalarInt:
		return "i64"
	case scalarBool:
		return "i1"
	case scalarString, scalarOpaquePtr:
		return "ptr"
	}
	return ""
}

func scalarBoxSuffixFor(elemType scalarType) string {
	switch elemType {
	case scalarByte:
		return "i8"
	case scalarChar:
		return "i32"
	default:
		return scalarSlotSuffixFor(elemType)
	}
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

func declareStringConcatI64Runtime(mctx *moduleCtx) {
	declareRuntimePrototype(mctx, "osty_rt_strings_ConcatI64Right", scalarString, []callArg{{ty: "ptr"}, {ty: "i64"}})
	declareRuntimePrototype(mctx, "osty_rt_strings_ConcatI64Left", scalarString, []callArg{{ty: "i64"}, {ty: "ptr"}})
}

func stringConcatSymbolForArgs(leftTy, rightTy string) (string, bool) {
	switch {
	case leftTy == "ptr" && rightTy == "ptr":
		return "osty_rt_strings_Concat", true
	case leftTy == "ptr" && rightTy == "i64":
		return "osty_rt_strings_ConcatI64Right", true
	case leftTy == "i64" && rightTy == "ptr":
		return "osty_rt_strings_ConcatI64Left", true
	default:
		return "", false
	}
}

func stringConcatSymbolForScalars(leftTy, rightTy scalarType) (string, bool) {
	return stringConcatSymbolForArgs(leftTy.llvm(), rightTy.llvm())
}

func scalarToStringSymbolFor(ty scalarType) string {
	switch ty {
	case scalarInt:
		return "osty_rt_int_to_string"
	case scalarBool:
		return "osty_rt_bool_to_string"
	case scalarByte:
		return "osty_rt_byte_to_string"
	case scalarChar:
		return "osty_rt_char_to_string"
	}
	return ""
}

func declareScalarToStringRuntime(mctx *moduleCtx, ty scalarType) bool {
	symbol := scalarToStringSymbolFor(ty)
	if symbol == "" {
		return false
	}
	declareRuntimePrototype(mctx, symbol, scalarString, []callArg{{ty: ty.llvm()}})
	return true
}

func stringConcatSequentialArg(fn *mir.Function, op mir.Operand, convertInt bool, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (string, callArg, scalarType, bool) {
	if symbol, ok := stringFnConstCallSymbol(op, mctx); ok {
		reg := mctx.freshTempName("fnconst.string")
		line := fmt.Sprintf("  %s = call ptr @%s()\n", reg, symbol)
		return line, callArg{expr: reg, ty: scalarString.llvm()}, scalarString, true
	}
	prelude, expr, ty, ok := resolveOperandWithPrelude(fn, op, bindings, mctx)
	if !ok {
		return "", callArg{}, scalarUnknown, false
	}
	if ty == scalarString || (ty == scalarInt && !convertInt) {
		return prelude, callArg{expr: expr, ty: ty.llvm()}, ty, true
	}
	if !declareScalarToStringRuntime(mctx, ty) {
		return "", callArg{}, scalarUnknown, false
	}
	reg := mctx.freshTempName("to.string")
	prelude += fmt.Sprintf("  %s = call ptr @%s(%s %s)\n", reg, scalarToStringSymbolFor(ty), ty.llvm(), expr)
	return prelude, callArg{expr: reg, ty: scalarString.llvm()}, scalarString, true
}

func stringConcatWhileArg(ctx *whileLoopEmitCtx, out *strings.Builder, op mir.Operand, convertInt bool) (callArg, scalarType, bool) {
	if symbol, ok := stringFnConstCallSymbol(op, ctx.mctx); ok {
		reg := freshReg(ctx)
		fmt.Fprintf(out, "  %s = call ptr @%s()\n", reg, symbol)
		return callArg{expr: reg, ty: scalarString.llvm()}, scalarString, true
	}
	expr, ty, ok := resolveOperandWithLoad(ctx, out, op)
	if !ok {
		return callArg{}, scalarUnknown, false
	}
	if ty == scalarString || (ty == scalarInt && !convertInt) {
		return callArg{expr: expr, ty: ty.llvm()}, ty, true
	}
	if !declareScalarToStringRuntime(ctx.mctx, ty) {
		return callArg{}, scalarUnknown, false
	}
	reg := freshReg(ctx)
	fmt.Fprintf(out, "  %s = call ptr @%s(%s %s)\n", reg, scalarToStringSymbolFor(ty), ty.llvm(), expr)
	return callArg{expr: reg, ty: scalarString.llvm()}, scalarString, true
}

func stringFnConstCallSymbol(op mir.Operand, mctx *moduleCtx) (string, bool) {
	con, ok := op.(*mir.ConstOp)
	if !ok || con == nil || mctx == nil {
		return "", false
	}
	fc, ok := con.Const.(*mir.FnConst)
	if !ok || fc == nil || fc.Symbol == "" {
		return "", false
	}
	if fnTy, ok := fc.Type().(*ir.FnType); ok && fnTy != nil {
		if len(fnTy.Params) == 0 && mctx.scalarFromType(fnTy.Return, true) == scalarString {
			declareFunctionPrototype(mctx, fc.Symbol, scalarString, nil)
			return fc.Symbol, true
		}
	}
	// FnConst may have ErrType (MIR lowering lost type info). Try
	// module lookup first; if the function is not in this module
	// (e.g. audit tests use a reduced module), trust the string
	// concat context and declare as fn() -> String.
	if mctx.module != nil {
		fn := mctx.module.LookupFunction(fc.Symbol)
		if fn != nil && len(fn.Params) == 0 && mctx.scalarFromType(fn.ReturnType, true) == scalarString {
			return fc.Symbol, true
		}
	}
	if isErrType(fc.Type()) {
		declareFunctionPrototype(mctx, fc.Symbol, scalarString, nil)
		return fc.Symbol, true
	}
	return "", false
}

func primitiveConversionSpec(kind mir.IntrinsicKind) (string, scalarType, scalarType, bool) {
	switch kind {
	case mir.IntrinsicByteToInt:
		return "zext", scalarByte, scalarInt, true
	case mir.IntrinsicCharToInt:
		return "zext", scalarChar, scalarInt, true
	case mir.IntrinsicIntToByte:
		return "trunc", scalarInt, scalarByte, true
	case mir.IntrinsicIntToChar:
		return "trunc", scalarInt, scalarChar, true
	case mir.IntrinsicByteToChar:
		return "zext", scalarByte, scalarChar, true
	case mir.IntrinsicCharToByte:
		return "trunc", scalarChar, scalarByte, true
	}
	return "", scalarUnknown, scalarUnknown, false
}

func classifyPrimitiveConversionIntrinsic(fn *mir.Function, ii *mir.IntrinsicInstr, destID mir.LocalID, destType scalarType, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (pendingInstr, mir.LocalID, scalarType, bool) {
	op, fromTy, toTy, ok := primitiveConversionSpec(ii.Kind)
	if !ok || destType != toTy || len(ii.Args) != 1 {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	prelude, expr, ty, ok := resolveOperandWithPrelude(fn, ii.Args[0], bindings, mctx)
	if !ok || ty != fromTy {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	reg := mctx.freshTempName("prim.conv")
	return pendingInstr{
		kind:          instrIntrinsic,
		binDestReg:    reg,
		intrinsicLine: prelude + fmt.Sprintf("  %s = %s %s %s to %s\n", reg, op, fromTy.llvm(), expr, toTy.llvm()),
	}, destID, toTy, true
}

func emitWhilePrimitiveConversionIntrinsic(ctx *whileLoopEmitCtx, out *strings.Builder, ii *mir.IntrinsicInstr, destID mir.LocalID, destType scalarType) bool {
	op, fromTy, toTy, ok := primitiveConversionSpec(ii.Kind)
	if !ok || destType != toTy || len(ii.Args) != 1 {
		return false
	}
	expr, ty, ok := resolveOperandWithLoad(ctx, out, ii.Args[0])
	if !ok || ty != fromTy {
		return false
	}
	reg := freshReg(ctx)
	fmt.Fprintf(out, "  %s = %s %s %s to %s\n", reg, op, fromTy.llvm(), expr, toTy.llvm())
	return bindWhileResult(ctx, out, destID, toTy, reg)
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

func declareStringCompareRuntime(mctx *moduleCtx) {
	declareRuntimePrototype(mctx, "osty_rt_strings_Compare", scalarInt, []callArg{{ty: "ptr"}, {ty: "ptr"}})
}

func stringComparePredicate(op mir.BinaryOp) string {
	switch op {
	case mir.BinLt:
		return "slt"
	case mir.BinLeq:
		return "sle"
	case mir.BinGt:
		return "sgt"
	case mir.BinGeq:
		return "sge"
	}
	return ""
}

func byteComparePredicate(op mir.BinaryOp) string {
	switch op {
	case mir.BinEq:
		return "eq"
	case mir.BinNeq:
		return "ne"
	case mir.BinLt:
		return "ult"
	case mir.BinLeq:
		return "ule"
	case mir.BinGt:
		return "ugt"
	case mir.BinGeq:
		return "uge"
	}
	return ""
}

func isUnsignedOrdinalScalar(st scalarType) bool {
	return st == scalarByte || st == scalarChar
}

func listBytesElementSize(st scalarType) int64 {
	switch st {
	case scalarByte:
		return 1
	case scalarChar:
		return 4
	}
	return 0
}

func renderListPushBytesIntrinsic(mctx *moduleCtx, listExpr, elemExpr string, elemTy scalarType) (string, bool) {
	elemSize := listBytesElementSize(elemTy)
	if elemSize == 0 {
		return "", false
	}
	declareListPushBytesRuntime(mctx)
	slot := mctx.freshTempName("list.push.bytes.slot")
	return fmt.Sprintf("  %s = alloca %s\n  store %s %s, ptr %s\n  call void @osty_rt_list_push_bytes_v1(ptr %s, ptr %s, i64 %d)\n",
		slot, elemTy.llvm(), elemTy.llvm(), elemExpr, slot, listExpr, slot, elemSize), true
}

func renderListInsertBytesIntrinsic(mctx *moduleCtx, listExpr, indexExpr, elemExpr string, elemTy scalarType) (string, bool) {
	elemSize := listBytesElementSize(elemTy)
	if elemSize == 0 {
		return "", false
	}
	declareListInsertBytesRuntime(mctx)
	slot := mctx.freshTempName("list.insert.bytes.slot")
	return fmt.Sprintf("  %s = alloca %s\n  store %s %s, ptr %s\n  call void @osty_rt_list_insert_bytes_v1(ptr %s, i64 %s, ptr %s, i64 %d)\n",
		slot, elemTy.llvm(), elemTy.llvm(), elemExpr, slot, listExpr, indexExpr, slot, elemSize), true
}

func emitWhileListPushBytes(ctx *whileLoopEmitCtx, out *strings.Builder, listExpr, elemExpr string, elemTy scalarType) bool {
	elemSize := listBytesElementSize(elemTy)
	if elemSize == 0 {
		return false
	}
	declareListPushBytesRuntime(ctx.mctx)
	slot := freshReg(ctx)
	fmt.Fprintf(out, "  %s = alloca %s\n", slot, elemTy.llvm())
	fmt.Fprintf(out, "  store %s %s, ptr %s\n", elemTy.llvm(), elemExpr, slot)
	fmt.Fprintf(out, "  call void @osty_rt_list_push_bytes_v1(ptr %s, ptr %s, i64 %d)\n", listExpr, slot, elemSize)
	return true
}

func emitWhileListInsertBytes(ctx *whileLoopEmitCtx, out *strings.Builder, listExpr, indexExpr, elemExpr string, elemTy scalarType) bool {
	elemSize := listBytesElementSize(elemTy)
	if elemSize == 0 {
		return false
	}
	declareListInsertBytesRuntime(ctx.mctx)
	slot := freshReg(ctx)
	fmt.Fprintf(out, "  %s = alloca %s\n", slot, elemTy.llvm())
	fmt.Fprintf(out, "  store %s %s, ptr %s\n", elemTy.llvm(), elemExpr, slot)
	fmt.Fprintf(out, "  call void @osty_rt_list_insert_bytes_v1(ptr %s, i64 %s, ptr %s, i64 %d)\n", listExpr, indexExpr, slot, elemSize)
	return true
}

func emitWhileListSetBytes(ctx *whileLoopEmitCtx, out *strings.Builder, listExpr, indexExpr, elemExpr string, elemTy scalarType) bool {
	elemSize := listBytesElementSize(elemTy)
	if elemSize == 0 {
		return false
	}
	declareListSetBytesRuntime(ctx.mctx)
	slot := freshReg(ctx)
	fmt.Fprintf(out, "  %s = alloca %s\n", slot, elemTy.llvm())
	fmt.Fprintf(out, "  store %s %s, ptr %s\n", elemTy.llvm(), elemExpr, slot)
	fmt.Fprintf(out, "  call void @osty_rt_list_set_bytes_v1(ptr %s, i64 %s, ptr %s, i64 %d, ptr null)\n", listExpr, indexExpr, slot, elemSize)
	return true
}

// resolveOperand returns (expression, type) for one MIR Operand using
// the prior-bindings map. Forward references and projections decline.
// String constants are interned through the moduleCtx pool, producing
// a `@.str.<N>` global symbol the caller can use directly.
func resolveOperand(op mir.Operand, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (string, scalarType, bool) {
	if con, ok := op.(*mir.ConstOp); ok {
		switch c := con.Const.(type) {
		case *mir.IntConst:
			switch {
			case isPrimType(c.Type(), ir.PrimInt):
				return fmt.Sprintf("%d", c.Value), scalarInt, true
			case isPrimType(c.Type(), ir.PrimByte):
				return fmt.Sprintf("%d", byte(c.Value)), scalarByte, true
			case isErrType(c.Type()):
				return fmt.Sprintf("%d", c.Value), scalarInt, true
			}
			return "", scalarUnknown, false
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
		case *mir.ByteConst:
			return fmt.Sprintf("%d", c.Value), scalarByte, true
		case *mir.CharConst:
			return fmt.Sprintf("%d", c.Value), scalarChar, true
		case *mir.FloatConst:
			return formatFloatConst(c.Value), scalarFloat, true
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
	if isPrimType(listTy, ir.PrimBytes) && elemTy == scalarByte {
		declareRuntimePrototype(mctx, "osty_rt_bytes_get", scalarByte, []callArg{{ty: "ptr"}, {ty: "i64"}})
		value := mctx.freshTempName("bytes.get")
		prelude := listPrelude + indexPrelude + fmt.Sprintf("  %s = call i8 @osty_rt_bytes_get(ptr %s, i64 %s)\n", value, listExpr, indexExpr)
		return prelude, value, scalarByte, true
	}
	if elemSize := listBytesElementSize(elemTy); elemSize > 0 {
		declareListGetBytesRuntime(mctx)
		slotLabel := "list.get.bytes.slot"
		valueLabel := "list.get.bytes"
		if elemTy == scalarByte {
			slotLabel = "list.get.byte.slot"
			valueLabel = "list.get.byte"
		}
		slot := mctx.freshTempName(slotLabel)
		value := mctx.freshTempName(valueLabel)
		prelude := listPrelude + indexPrelude +
			fmt.Sprintf("  %s = alloca %s\n", slot, elemTy.llvm()) +
			fmt.Sprintf("  call void @osty_rt_list_get_bytes_v1(ptr %s, i64 %s, ptr %s, i64 %d)\n", listExpr, indexExpr, slot, elemSize) +
			fmt.Sprintf("  %s = load %s, ptr %s\n", value, elemTy.llvm(), slot)
		return prelude, value, elemTy, true
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
	if _, ok := place.Projections[0].(*mir.VariantProj); ok {
		if prelude, slot, ty, ok := resolveProjectedOptionPayloadSlot(fn, place, base.expr, mctx, label); ok {
			return prelude, slot, ty, true
		}
		return resolveProjectedResultPayloadSlot(fn, place, base.expr, mctx, label)
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
		if nextIdx, ok := place.Projections[i+1].(*mir.IndexProj); ok {
			elemStruct, ok := listProjectionElementStructName(fp.Type, nextIdx.ElemType)
			if !ok {
				return "", "", scalarUnknown, false
			}
			if _, ok := mctx.lookupStructFields(elemStruct); !ok {
				return "", "", scalarUnknown, false
			}
			listPtr := mctx.freshTempName("field.list.base")
			fmt.Fprintf(&prelude, "  %s = load ptr, ptr %s\n", listPtr, slot)
			indexPrelude, indexExpr, indexTy, ok := resolveOperandWithPrelude(fn, nextIdx.Index, bindings, mctx)
			if !ok || indexTy != scalarInt {
				return "", "", scalarUnknown, false
			}
			prelude.WriteString(indexPrelude)
			elemTy := mctx.scalarFromType(nextIdx.ElemType, true)
			if elemTy != scalarOpaquePtr {
				return "", "", scalarUnknown, false
			}
			symbol := listGetSymbolFor(elemTy)
			if symbol == "" {
				return "", "", scalarUnknown, false
			}
			declareListGetRuntimeFor(mctx, elemTy)
			elemPtr := mctx.freshTempName("list.get.struct")
			fmt.Fprintf(&prelude, "  %s = call ptr @%s(ptr %s, i64 %s)\n", elemPtr, symbol, listPtr, indexExpr)
			currentStruct = elemStruct
			currentPtr = elemPtr
			i++
			continue
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

func resolveProjectedOptionPayloadSlot(fn *mir.Function, place mir.Place, baseExpr string, mctx *moduleCtx, label string) (string, string, scalarType, bool) {
	if fn == nil || mctx == nil || len(place.Projections) == 0 || baseExpr == "" {
		return "", "", scalarUnknown, false
	}
	baseLocal := lookupLocal(fn, place.Local)
	if baseLocal == nil {
		return "", "", scalarUnknown, false
	}
	vp, ok := place.Projections[0].(*mir.VariantProj)
	if !ok {
		return "", "", scalarUnknown, false
	}
	payloadTy, ok := optionPayloadScalar(baseLocal.Type, mctx)
	if !ok {
		return "", "", scalarUnknown, false
	}
	typeName, ok := mctx.emitOptionBoxDef(payloadTy)
	if !ok {
		return "", "", scalarUnknown, false
	}
	var prelude strings.Builder
	slot := mctx.freshTempName(label)
	fmt.Fprintf(&prelude, "  %s = getelementptr inbounds %%%s, ptr %s, i32 0, i32 1\n", slot, typeName, baseExpr)
	if len(place.Projections) == 1 {
		return prelude.String(), slot, payloadTy, true
	}
	if payloadTy != scalarOpaquePtr {
		return "", "", scalarUnknown, false
	}
	nextNamed, ok := vp.Type.(*ir.NamedType)
	if !ok || nextNamed == nil || nextNamed.Name == "" {
		return "", "", scalarUnknown, false
	}
	currentPtr := mctx.freshTempName("option.payload.base")
	fmt.Fprintf(&prelude, "  %s = load ptr, ptr %s\n", currentPtr, slot)
	currentStruct := nextNamed.Name
	for i := 1; i < len(place.Projections); i++ {
		fp, ok := place.Projections[i].(*mir.FieldProj)
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

func resolveProjectedResultPayloadSlot(fn *mir.Function, place mir.Place, baseExpr string, mctx *moduleCtx, label string) (string, string, scalarType, bool) {
	if fn == nil || mctx == nil || len(place.Projections) == 0 || baseExpr == "" {
		return "", "", scalarUnknown, false
	}
	baseLocal := lookupLocal(fn, place.Local)
	if baseLocal == nil {
		return "", "", scalarUnknown, false
	}
	vp, ok := place.Projections[0].(*mir.VariantProj)
	if !ok {
		return "", "", scalarUnknown, false
	}
	payloadTy, okTy, errTy, payloadIndex, ok := resultVariantPayloadScalar(baseLocal.Type, vp.Variant, mctx)
	if !ok {
		return "", "", scalarUnknown, false
	}
	typeName, ok := mctx.emitResultBoxDef(okTy, errTy)
	if !ok {
		return "", "", scalarUnknown, false
	}
	var prelude strings.Builder
	slot := mctx.freshTempName(label)
	fmt.Fprintf(&prelude, "  %s = getelementptr inbounds %%%s, ptr %s, i32 0, i32 %d\n", slot, typeName, baseExpr, payloadIndex)
	if len(place.Projections) == 1 {
		return prelude.String(), slot, payloadTy, true
	}
	if payloadTy != scalarOpaquePtr {
		return "", "", scalarUnknown, false
	}
	nextNamed, ok := vp.Type.(*ir.NamedType)
	if !ok || nextNamed == nil || nextNamed.Name == "" {
		return "", "", scalarUnknown, false
	}
	currentPtr := mctx.freshTempName("result.payload.base")
	fmt.Fprintf(&prelude, "  %s = load ptr, ptr %s\n", currentPtr, slot)
	currentStruct := nextNamed.Name
	for i := 1; i < len(place.Projections); i++ {
		fp, ok := place.Projections[i].(*mir.FieldProj)
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

func listProjectionElementStructName(listType mir.Type, elemType mir.Type) (string, bool) {
	listNamed, ok := listType.(*ir.NamedType)
	if !ok || listNamed == nil || !listNamed.Builtin || listNamed.Name != "List" || len(listNamed.Args) == 0 {
		return "", false
	}
	elemNamed, ok := elemType.(*ir.NamedType)
	if !ok || elemNamed == nil || elemNamed.Name == "" {
		return "", false
	}
	argNamed, ok := listNamed.Args[0].(*ir.NamedType)
	if !ok || argNamed == nil || argNamed.Name != elemNamed.Name {
		return "", false
	}
	return elemNamed.Name, true
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
	return classifyBinaryForType(op, scalarInt)
}

func classifyBinaryForType(op mir.BinaryOp, hint scalarType) (string, scalarType, scalarType) {
	isFloat := hint == scalarFloat
	if hint == scalarString || hint == scalarOpaquePtr || hint == scalarUnknown {
		// Fall back to Int-based classification; string concatenation
		// is handled separately by the caller.
		isFloat = false
	}
	switch op {
	case mir.BinAdd:
		if isFloat {
			return "fadd", scalarFloat, scalarFloat
		}
		return "add", scalarInt, scalarInt
	case mir.BinSub:
		if isFloat {
			return "fsub", scalarFloat, scalarFloat
		}
		return "sub", scalarInt, scalarInt
	case mir.BinMul:
		if isFloat {
			return "fmul", scalarFloat, scalarFloat
		}
		return "mul", scalarInt, scalarInt
	case mir.BinDiv:
		if isFloat {
			return "fdiv", scalarFloat, scalarFloat
		}
		return "sdiv", scalarInt, scalarInt
	case mir.BinMod:
		return "srem", scalarInt, scalarInt
	case mir.BinEq:
		if isFloat {
			return "fcmp oeq", scalarBool, scalarFloat
		}
		return "icmp eq", scalarBool, scalarInt
	case mir.BinNeq:
		if isFloat {
			return "fcmp une", scalarBool, scalarFloat
		}
		return "icmp ne", scalarBool, scalarInt
	case mir.BinLt:
		if isFloat {
			return "fcmp olt", scalarBool, scalarFloat
		}
		return "icmp slt", scalarBool, scalarInt
	case mir.BinLeq:
		if isFloat {
			return "fcmp ole", scalarBool, scalarFloat
		}
		return "icmp sle", scalarBool, scalarInt
	case mir.BinGt:
		if isFloat {
			return "fcmp ogt", scalarBool, scalarFloat
		}
		return "icmp sgt", scalarBool, scalarInt
	case mir.BinGeq:
		if isFloat {
			return "fcmp oge", scalarBool, scalarFloat
		}
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
// entries are identical and none collide with stage0-reserved block
// labels such as `entry:`.
func paramFallbackName(i int) string {
	if i < 26 {
		return string(rune('a' + i))
	}
	return fmt.Sprintf("p%d", i)
}

func disambiguateParamNames(names []string) {
	for i := range names {
		if isReservedStage0Label(names[i]) {
			names[i] = fmt.Sprintf("%s.%d", names[i], i)
		}
	}
	for i := 1; i < len(names); i++ {
		for j := 0; j < i; j++ {
			if names[i] == names[j] {
				names[i] = fmt.Sprintf("%s.%d", names[i], i)
				break
			}
		}
	}
}

// isReservedStage0Label reports whether `name` collides with a bare
// block label that stage0 emits directly. Numbered labels such as
// `then.1` or `bb.4` are not valid Osty source identifiers, so the
// unnumbered `entry:` label is the only collision class we need here.
func isReservedStage0Label(name string) bool {
	switch name {
	case "entry":
		return true
	}
	return false
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
	if len(fn.Params) > 8 {
		return pat, false
	}
	if len(fn.Blocks) != 4 {
		return pat, false
	}

	// Seed param bindings.
	pat.paramIDs = fn.Params
	pat.paramTypes = make([]scalarType, len(fn.Params))
	pat.paramNames = make([]string, len(fn.Params))
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
		pat.paramNames[i] = sanitizeLLVMName(loc.Name, paramFallbackName(i))
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

	// Reject aggregate return types.
	if _, _, ok := classifyAggregateReturnType(fn.ReturnType, mctx); ok {
		return pat, false
	}

	pat.retType = mctx.scalarFromType(fn.ReturnType, true)
	if pat.retType == scalarUnknown {
		return pat, false
	}
	if len(fn.Params) > 48 || len(fn.Blocks) < 3 {
		return pat, false
	}

	bindings := map[mir.LocalID]localBinding{}
	pat.paramIDs = fn.Params
	pat.paramTypes = make([]scalarType, len(fn.Params))
	pat.paramNames = make([]string, len(fn.Params))
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
		pat.paramNames[i] = sanitizeLLVMName(loc.Name, paramFallbackName(i))
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
		pat.paramNames[i] = sanitizeLLVMName(loc.Name, paramFallbackName(i))
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
		pat.paramNames[i] = sanitizeLLVMName(loc.Name, paramFallbackName(i))
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

// ---- short-circuit Bool return with discarded fallback call ----
//
// Handles the partial-MIR shape for `predA(x) || predB(x)`: the first
// call feeds a BranchTerm, while the false arm still contains the
// second predicate call but records it as Dest=nil before flowing into
// an UnreachableTerm sink. The branch topology preserves enough
// information to return true on the short-circuit arm and the second
// call's Bool result on the fallback arm.

type shortCircuitCallFallbackBoolPattern struct {
	paramTypes []scalarType
	paramNames []string
	entry      blockEmit
	entryCond  string
	trueLabel  string
	callLabel  string
	callReg    string
	callLine   string
}

func matchShortCircuitCallFallbackBoolReturn(fn *mir.Function, mctx *moduleCtx) (shortCircuitCallFallbackBoolPattern, bool) {
	pat := shortCircuitCallFallbackBoolPattern{}
	if mctx.scalarFromType(fn.ReturnType, true) != scalarBool {
		return pat, false
	}
	if len(fn.Params) > 8 || len(fn.Blocks) != 4 {
		return pat, false
	}

	bindings := map[mir.LocalID]localBinding{}
	pat.paramTypes = make([]scalarType, len(fn.Params))
	pat.paramNames = make([]string, len(fn.Params))
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
		pat.paramNames[i] = sanitizeLLVMName(loc.Name, paramFallbackName(i))
	}
	disambiguateParamNames(pat.paramNames)
	for i, pid := range fn.Params {
		bindings[pid] = localBinding{expr: "%" + pat.paramNames[i], ty: pat.paramTypes[i], defined: true}
	}

	entry := blockByID(fn, fn.Entry)
	if entry == nil {
		return pat, false
	}
	branch, ok := entry.Term.(*mir.BranchTerm)
	if !ok {
		return pat, false
	}
	nextSSA := 0
	entryEmit, entryCond, entryCondTy, ok := classifyEntryBlock(fn, entry, bindings, mctx, &nextSSA)
	if !ok || entryCondTy != scalarBool {
		return pat, false
	}

	trueBlock := blockByID(fn, branch.Then)
	callBlock := blockByID(fn, branch.Else)
	if trueBlock == nil || callBlock == nil || trueBlock.ID == callBlock.ID {
		return pat, false
	}
	callGoto, ok := callBlock.Term.(*mir.GotoTerm)
	if !ok {
		return pat, false
	}
	exitID, ok := shortCircuitTrueExitTarget(fn, trueBlock)
	if !ok || callGoto.Target != exitID {
		return pat, false
	}
	exit := blockByID(fn, exitID)
	if exit == nil || !blockHasOnlyStorageMarkers(exit) {
		return pat, false
	}
	if _, ok := exit.Term.(*mir.UnreachableTerm); !ok {
		return pat, false
	}
	callReg := fmt.Sprintf("%%%d", nextSSA)
	var callLine string
	if call, ok := singleDiscardedCallInstr(callBlock); ok {
		var okLine bool
		callLine, okLine = classifyDiscardedBoolCallLine(fn, call, bindings, mctx, callReg)
		if !okLine {
			return pat, false
		}
	} else if intrinsic, ok := singleDiscardedIntrinsicInstr(callBlock); ok {
		var okLine bool
		callLine, okLine = classifyDiscardedBoolIntrinsicLine(fn, intrinsic, bindings, mctx, callReg)
		if !okLine {
			return pat, false
		}
	} else {
		return pat, false
	}

	pat.entry = entryEmit
	pat.entry.label = "entry"
	pat.entryCond = entryCond
	pat.trueLabel = blockLabelName(trueBlock.ID, "or.true")
	pat.callLabel = blockLabelName(callBlock.ID, "or.call")
	pat.callReg = callReg
	pat.callLine = callLine
	return pat, true
}

func shortCircuitTrueExitTarget(fn *mir.Function, bb *mir.BasicBlock) (mir.BlockID, bool) {
	if bb == nil || !blockHasOnlyStorageMarkers(bb) {
		return 0, false
	}
	if _, ok := bb.Term.(*mir.UnreachableTerm); ok {
		return bb.ID, true
	}
	if gotoTerm, ok := bb.Term.(*mir.GotoTerm); ok {
		exit := blockByID(fn, gotoTerm.Target)
		if exit == nil || !blockHasOnlyStorageMarkers(exit) {
			return 0, false
		}
		if _, ok := exit.Term.(*mir.UnreachableTerm); !ok {
			return 0, false
		}
		return exit.ID, true
	}
	return 0, false
}

func singleDiscardedCallInstr(bb *mir.BasicBlock) (*mir.CallInstr, bool) {
	var call *mir.CallInstr
	for _, instr := range bb.Instrs {
		switch step := instr.(type) {
		case *mir.StorageLiveInstr, *mir.StorageDeadInstr:
			continue
		case *mir.CallInstr:
			if step.Dest != nil || call != nil {
				return nil, false
			}
			call = step
		default:
			return nil, false
		}
	}
	return call, call != nil
}

func singleDiscardedIntrinsicInstr(bb *mir.BasicBlock) (*mir.IntrinsicInstr, bool) {
	var intrinsic *mir.IntrinsicInstr
	for _, instr := range bb.Instrs {
		switch step := instr.(type) {
		case *mir.StorageLiveInstr, *mir.StorageDeadInstr:
			continue
		case *mir.IntrinsicInstr:
			if step.Dest != nil || intrinsic != nil {
				return nil, false
			}
			intrinsic = step
		default:
			return nil, false
		}
	}
	return intrinsic, intrinsic != nil
}

func classifyDiscardedBoolCallLine(fn *mir.Function, ci *mir.CallInstr, bindings map[mir.LocalID]localBinding, mctx *moduleCtx, resultReg string) (string, bool) {
	if ci == nil || ci.Dest != nil || resultReg == "" {
		return "", false
	}
	ref, ok := ci.Callee.(*mir.FnRef)
	if !ok || ref.Symbol == "" {
		return "", false
	}
	var resolved resolvedCallArgs
	if fnTy, ok := ref.Type.(*ir.FnType); ok && fnTy != nil {
		if mctx.scalarFromType(fnTy.Return, true) != scalarBool || len(fnTy.Params) != len(ci.Args) {
			return "", false
		}
		args := make([]callArg, 0, len(ci.Args))
		var prelude strings.Builder
		for i, op := range ci.Args {
			argPrelude, argExpr, argTy, okOp := resolveOperandWithPrelude(fn, op, bindings, mctx)
			if !okOp {
				return "", false
			}
			paramTy := mctx.scalarFromType(fnTy.Params[i], true)
			if paramTy == scalarUnknown || paramTy != argTy {
				return "", false
			}
			prelude.WriteString(argPrelude)
			args = append(args, callArg{expr: argExpr, ty: argTy.llvm()})
		}
		resolved = resolvedCallArgs{prelude: prelude.String(), args: args}
	} else {
		if !isErrType(ref.Type) && mctx.scalarFromType(ref.Type, true) != scalarBool {
			return "", false
		}
		var okArgs bool
		resolved, okArgs = resolveCallArgsWithoutFnType(fn, ci.Args, bindings, mctx)
		if !okArgs {
			return "", false
		}
	}
	declareFunctionPrototype(mctx, ref.Symbol, scalarBool, resolved.args)
	var line strings.Builder
	line.WriteString(resolved.prelude)
	fmt.Fprintf(&line, "  %s = call i1 @%s(", resultReg, ref.Symbol)
	for i, a := range resolved.args {
		if i > 0 {
			line.WriteString(", ")
		}
		fmt.Fprintf(&line, "%s %s", a.ty, a.expr)
	}
	line.WriteString(")\n")
	return line.String(), true
}

func classifyDiscardedBoolIntrinsicLine(fn *mir.Function, ii *mir.IntrinsicInstr, bindings map[mir.LocalID]localBinding, mctx *moduleCtx, resultReg string) (string, bool) {
	if ii == nil || ii.Dest != nil || resultReg == "" {
		return "", false
	}
	spec, ok := intrinsicRuntimeCallSpec(ii.Kind)
	if !ok || spec.ret != scalarBool {
		return "", false
	}
	args, prelude, ok := resolveFixedScalarArgs(fn, ii.Args, bindings, mctx, spec.args)
	if !ok {
		return "", false
	}
	declareRuntimePrototype(mctx, spec.symbol, spec.ret, args)
	var line strings.Builder
	line.WriteString(prelude)
	fmt.Fprintf(&line, "  %s = call i1 @%s(", resultReg, spec.symbol)
	for i, a := range args {
		if i > 0 {
			line.WriteString(", ")
		}
		fmt.Fprintf(&line, "%s %s", a.ty, a.expr)
	}
	line.WriteString(")\n")
	return line.String(), true
}

func emitShortCircuitCallFallbackBoolReturn(out *strings.Builder, fn *mir.Function, pat shortCircuitCallFallbackBoolPattern) error {
	fmt.Fprintf(out, "define i1 @%s(", fn.Name)
	for i, name := range pat.paramNames {
		if i > 0 {
			out.WriteString(", ")
		}
		fmt.Fprintf(out, "%s %%%s", pat.paramTypes[i].llvm(), name)
	}
	out.WriteString(") {\n")
	emitBlock(out, pat.entry)
	fmt.Fprintf(out, "  br i1 %s, label %%%s, label %%%s\n\n", pat.entryCond, pat.trueLabel, pat.callLabel)
	fmt.Fprintf(out, "%s:\n", pat.trueLabel)
	out.WriteString("  ret i1 true\n\n")
	fmt.Fprintf(out, "%s:\n", pat.callLabel)
	out.WriteString(pat.callLine)
	fmt.Fprintf(out, "  ret i1 %s\n", pat.callReg)
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
		symbol := pi.callSymbol
		if pi.resultType == scalarString && pi.callSymbol == "osty_rt_strings_Concat" {
			var ok bool
			symbol, ok = stringConcatSymbolForArgs(prev.ty, next.ty)
			if !ok {
				return
			}
		}
		fmt.Fprintf(out, "  %s = call %s @%s(%s %s, %s %s)\n",
			reg, pi.resultType.llvm(), symbol,
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
		pat.paramNames[i] = sanitizeLLVMName(loc.Name, paramFallbackName(i))
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
		pat.paramNames[i] = sanitizeLLVMName(loc.Name, paramFallbackName(i))
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
		pat.paramNames[i] = sanitizeLLVMName(loc.Name, paramFallbackName(i))
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
		pat.paramNames[i] = sanitizeLLVMName(loc.Name, paramFallbackName(i))
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
	// Reassign arm SSA ranges so they come after all rung cond emits.
	// The original classification assigns arm0 before rung1, which
	// gives arm0 lower SSA numbers than rung blocks — but LLVM
	// requires SSA definitions to be in textual order. Reassign from
	// the current nextSSA value (past all rung instructions).
	for i := range pat.arms {
		nFields := len(pat.arms[i].fieldExprs)
		if nFields == 0 {
			continue
		}
		pat.arms[i].startSSA = nextSSA
		pat.arms[i].resultExpr = fmt.Sprintf("%%%d", nextSSA+nFields-1)
		nextSSA += nFields
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
	typeName    string
	fieldTypes  []scalarType
	paramNames  []string
	paramTypes  []scalarType
	callSymbol  string
	callArgs    []callArg
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
		pat.paramNames[i] = sanitizeLLVMName(loc.Name, paramFallbackName(i))
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

	// Always declare the callee prototype before the call site. This keeps
	// partial/list-all-declines modules parseable even when the referenced
	// helper function declined and therefore has no local `define` body.
	declareAggregateFunctionPrototype(mctx, ref.Symbol, typeName, args)

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
		if step.Dest.HasProjections() {
			line, okCall := classifyProjectedCallLine(fn, step, bindings, mctx)
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
		if step.Dest.HasProjections() {
			line, okIntr := classifyProjectedIntrinsicLine(fn, step, bindings, mctx)
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
	pat.retType = mctx.scalarFromType(fn.ReturnType, true)
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
		pat.paramNames[i] = sanitizeLLVMName(loc.Name, paramFallbackName(i))
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
		ty := mctx.scalarFromType(l.Type, true)
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
		fn:         fn,
		bindings:   bindings,
		stack:      stack,
		mctx:       mctx,
		nextSSA:    &nextSSA,
		aggregates: map[mir.LocalID]aggregateBinding{},
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
	fn         *mir.Function
	bindings   map[mir.LocalID]localBinding
	stack      map[mir.LocalID]stackDecl
	mctx       *moduleCtx
	nextSSA    *int
	aggregates map[mir.LocalID]aggregateBinding

	// aggRetSRet is true when the function uses sret for aggregate
	// return. In this mode, the return local is a ptr sret slot and
	// AggregateRV writes go through the enum/struct aggregate emitter.
	aggRetSRet bool
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
	targetFn := ""
	if ctx.fn != nil && (ctx.fn.Name == "Runner__Run" || strings.HasPrefix(ctx.fn.Name, "Runner__check") || ctx.fn.Name == "resolveFixtureCases") {
		targetFn = ctx.fn.Name
	}
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
	if targetFn != "" {
		fmt.Printf("[emitWhileStep %s] reject instr %T at line %d\n", targetFn, instr, 1)
	}
	return false
}

// emitWhileStepInUnreachable is like emitWhileStep but for blocks
// terminated by UnreachableTerm. It emits calls and intrinsics
// (which may have side effects like os.exit or process.abort) but
// skips pure assignments that only bind ErrType/unit values.
func emitWhileStepInUnreachable(ctx *whileLoopEmitCtx, out *strings.Builder, instr mir.Instr) bool {
	switch step := instr.(type) {
	case *mir.CallInstr:
		return emitWhileCall(ctx, out, step)
	case *mir.IntrinsicInstr:
		return emitWhileIntrinsic(ctx, out, step)
	case *mir.StorageLiveInstr, *mir.StorageDeadInstr:
		return true
	case *mir.AssignInstr:
		// Skip assignments to ErrType or unit locals in unreachable
		// blocks — they have no side effects and their locals may
		// not have valid stack allocations.
		destLocal := lookupLocal(ctx.fn, step.Dest.Local)
		if destLocal != nil && (isErrType(destLocal.Type) || isUnitType(destLocal.Type)) {
			return true
		}
		return emitWhileAssign(ctx, out, step)
	}
	return false
}

// emitWhileIntrinsic mirrors the sequential intrinsic surface for loop
// blocks while resolving operands through the stack-aware loop bindings.
func emitWhileIntrinsic(ctx *whileLoopEmitCtx, out *strings.Builder, ii *mir.IntrinsicInstr) bool {
	if ii.Dest != nil {
		return emitWhileValueIntrinsic(ctx, out, ii)
	}
	switch ii.Kind {
	case mir.IntrinsicPrint, mir.IntrinsicPrintln, mir.IntrinsicEprint, mir.IntrinsicEprintln:
		return emitWhilePrintIntrinsic(ctx, out, ii)
	case mir.IntrinsicAbort:
		if len(ii.Args) != 1 {
			return false
		}
		expr, ty, ok := resolveOperandWithLoad(ctx, out, ii.Args[0])
		if !ok || ty != scalarString {
			return false
		}
		declareVoidFunctionPrototype(ctx.mctx, "osty_rt_panic", []callArg{{ty: "ptr"}})
		fmt.Fprintf(out, "  call void @osty_rt_panic(ptr %s)\n", expr)
		return true
	case mir.IntrinsicListIsEmpty:
		args, ok := resolveWhileFixedScalarArgs(ctx, out, ii.Args, []scalarType{scalarOpaquePtr})
		if !ok {
			return false
		}
		declareListRuntime(ctx.mctx)
		lenReg := freshReg(ctx)
		boolReg := freshReg(ctx)
		fmt.Fprintf(out, "  %s = call i64 @osty_rt_list_len(ptr %s)\n", lenReg, args[0].expr)
		fmt.Fprintf(out, "  %s = icmp eq i64 %s, 0\n", boolReg, lenReg)
		return true
	case mir.IntrinsicStringIsEmpty:
		args, ok := resolveWhileFixedScalarArgs(ctx, out, ii.Args, []scalarType{scalarString})
		if !ok {
			return false
		}
		declareRuntimePrototype(ctx.mctx, "osty_rt_strings_ByteLen", scalarInt, []callArg{{ty: "ptr"}})
		lenReg := freshReg(ctx)
		boolReg := freshReg(ctx)
		fmt.Fprintf(out, "  %s = call i64 @osty_rt_strings_ByteLen(ptr %s)\n", lenReg, args[0].expr)
		fmt.Fprintf(out, "  %s = icmp eq i64 %s, 0\n", boolReg, lenReg)
		return true
	case mir.IntrinsicBytesContains:
		args, ok := resolveWhileFixedScalarArgs(ctx, out, ii.Args, []scalarType{scalarOpaquePtr, scalarOpaquePtr})
		if !ok {
			return false
		}
		declareRuntimePrototype(ctx.mctx, "osty_rt_bytes_index_of", scalarInt, []callArg{{ty: "ptr"}, {ty: "ptr"}})
		indexReg := freshReg(ctx)
		boolReg := freshReg(ctx)
		fmt.Fprintf(out, "  %s = call i64 @osty_rt_bytes_index_of(ptr %s, ptr %s)\n", indexReg, args[0].expr, args[1].expr)
		fmt.Fprintf(out, "  %s = icmp ne i64 %s, -1\n", boolReg, indexReg)
		return true
	case mir.IntrinsicListPush:
		if len(ii.Args) != 2 {
			return false
		}
		listExpr, listTy, ok := resolveOperandWithLoad(ctx, out, ii.Args[0])
		if !ok || listTy != scalarOpaquePtr {
			return false
		}
		elemExpr, elemTy, ok := resolveOperandWithLoad(ctx, out, ii.Args[1])
		if !ok {
			return false
		}
		if emitWhileListPushBytes(ctx, out, listExpr, elemExpr, elemTy) {
			return true
		}
		symbol := listPushSymbolFor(elemTy)
		if symbol == "" {
			return false
		}
		declareListRuntime(ctx.mctx)
		fmt.Fprintf(out, "  call void @%s(ptr %s, %s %s)\n", symbol, listExpr, elemTy.llvm(), elemExpr)
		return true
	case mir.IntrinsicListInsert:
		if len(ii.Args) != 3 {
			return false
		}
		listExpr, listTy, ok := resolveOperandWithLoad(ctx, out, ii.Args[0])
		if !ok || listTy != scalarOpaquePtr {
			return false
		}
		indexExpr, indexTy, ok := resolveOperandWithLoad(ctx, out, ii.Args[1])
		if !ok || indexTy != scalarInt {
			return false
		}
		elemExpr, elemTy, ok := resolveOperandWithLoad(ctx, out, ii.Args[2])
		if !ok {
			return false
		}
		if emitWhileListInsertBytes(ctx, out, listExpr, indexExpr, elemExpr, elemTy) {
			return true
		}
		symbol := listInsertSymbolFor(elemTy)
		if symbol == "" {
			return false
		}
		declareVoidFunctionPrototype(ctx.mctx, symbol, []callArg{{ty: "ptr"}, {ty: "i64"}, {ty: elemTy.llvm()}})
		fmt.Fprintf(out, "  call void @%s(ptr %s, i64 %s, %s %s)\n", symbol, listExpr, indexExpr, elemTy.llvm(), elemExpr)
		return true
	case mir.IntrinsicListClear:
		return emitWhileUnaryVoid(ctx, out, ii, "osty_rt_list_clear")
	case mir.IntrinsicListReverse:
		return emitWhileUnaryVoid(ctx, out, ii, "osty_rt_list_reverse")
	case mir.IntrinsicListPop:
		return emitWhileUnaryVoid(ctx, out, ii, "osty_rt_list_pop_discard")
	case mir.IntrinsicListRemoveAt:
		args, ok := resolveWhileFixedScalarArgs(ctx, out, ii.Args, []scalarType{scalarOpaquePtr, scalarInt})
		if !ok {
			return false
		}
		declareVoidFunctionPrototype(ctx.mctx, "osty_rt_list_remove_at_discard", []callArg{{ty: "ptr"}, {ty: "i64"}})
		out.WriteString(renderVoidCallLine("osty_rt_list_remove_at_discard", args))
		return true
	case mir.IntrinsicSetInsert, mir.IntrinsicSetRemove:
		if len(ii.Args) != 2 {
			return false
		}
		setExpr, setTy, ok := resolveOperandWithLoad(ctx, out, ii.Args[0])
		if !ok || setTy != scalarOpaquePtr {
			return false
		}
		elemExpr, elemTy, ok := resolveOperandWithLoad(ctx, out, ii.Args[1])
		if !ok {
			return false
		}
		symbol := setMutationSymbolFor(ii.Kind, elemTy)
		if symbol == "" {
			return false
		}
		declareRuntimePrototype(ctx.mctx, symbol, scalarBool, []callArg{{ty: "ptr"}, {ty: elemTy.llvm()}})
		fmt.Fprintf(out, "  call i1 @%s(ptr %s, %s %s)\n", symbol, setExpr, elemTy.llvm(), elemExpr)
		return true
	case mir.IntrinsicMapSet:
		if len(ii.Args) != 3 {
			return false
		}
		mapExpr, mapTy, ok := resolveOperandWithLoad(ctx, out, ii.Args[0])
		if !ok || mapTy != scalarOpaquePtr {
			return false
		}
		keyExpr, keyTy, ok := resolveOperandWithLoad(ctx, out, ii.Args[1])
		if !ok {
			return false
		}
		valueExpr, valueTy, ok := resolveOperandWithLoad(ctx, out, ii.Args[2])
		if !ok {
			return false
		}
		symbol := mapInsertSymbolFor(keyTy)
		if symbol == "" {
			return false
		}
		valueSlot := ctx.mctx.freshTempName("map.value")
		declareVoidFunctionPrototype(ctx.mctx, symbol, []callArg{{ty: "ptr"}, {ty: keyTy.llvm()}, {ty: "ptr"}})
		fmt.Fprintf(out, "  %s = alloca %s\n", valueSlot, valueTy.llvm())
		fmt.Fprintf(out, "  store %s %s, ptr %s\n", valueTy.llvm(), valueExpr, valueSlot)
		fmt.Fprintf(out, "  call void @%s(ptr %s, %s %s, ptr %s)\n", symbol, mapExpr, keyTy.llvm(), keyExpr, valueSlot)
		return true
	case mir.IntrinsicMapRemove:
		if len(ii.Args) != 2 {
			return false
		}
		mapExpr, mapTy, ok := resolveOperandWithLoad(ctx, out, ii.Args[0])
		if !ok || mapTy != scalarOpaquePtr {
			return false
		}
		keyExpr, keyTy, ok := resolveOperandWithLoad(ctx, out, ii.Args[1])
		if !ok {
			return false
		}
		symbol := mapRemoveSymbolFor(keyTy)
		if symbol == "" {
			return false
		}
		declareRuntimePrototype(ctx.mctx, symbol, scalarBool, []callArg{{ty: "ptr"}, {ty: keyTy.llvm()}})
		fmt.Fprintf(out, "  call i1 @%s(ptr %s, %s %s)\n", symbol, mapExpr, keyTy.llvm(), keyExpr)
		return true
	case mir.IntrinsicMapClear:
		return emitWhileUnaryVoid(ctx, out, ii, "osty_rt_map_clear")
	case mir.IntrinsicSetClear:
		return emitWhileUnaryVoid(ctx, out, ii, "osty_rt_set_clear")
	case mir.IntrinsicStringSplitInto:
		args, ok := resolveWhileFixedScalarArgs(ctx, out, ii.Args, []scalarType{scalarOpaquePtr, scalarString, scalarString})
		if !ok {
			return false
		}
		declareVoidFunctionPrototype(ctx.mctx, "osty_rt_strings_SplitInto", []callArg{{ty: "ptr"}, {ty: "ptr"}, {ty: "ptr"}})
		out.WriteString(renderVoidCallLine("osty_rt_strings_SplitInto", args))
		return true
	case mir.IntrinsicYield:
		if len(ii.Args) != 0 {
			return false
		}
		declareVoidFunctionPrototype(ctx.mctx, "osty_rt_yield", nil)
		out.WriteString("  call void @osty_rt_yield()\n")
		return true
	case mir.IntrinsicSleep:
		args, ok := resolveWhileFixedScalarArgs(ctx, out, ii.Args, []scalarType{scalarInt})
		if !ok {
			return false
		}
		declareVoidFunctionPrototype(ctx.mctx, "osty_rt_sleep", []callArg{{ty: "i64"}})
		out.WriteString(renderVoidCallLine("osty_rt_sleep", args))
		return true
	case mir.IntrinsicCheckCancelled:
		if len(ii.Args) != 0 {
			return false
		}
		declareVoidFunctionPrototype(ctx.mctx, "osty_rt_check_cancelled", nil)
		out.WriteString("  call void @osty_rt_check_cancelled()\n")
		return true
	case mir.IntrinsicChanSend:
		if len(ii.Args) != 2 {
			return false
		}
		chanExpr, chanTy, ok := resolveOperandWithLoad(ctx, out, ii.Args[0])
		if !ok || chanTy != scalarOpaquePtr {
			return false
		}
		valueExpr, valueTy, ok := resolveOperandWithLoad(ctx, out, ii.Args[1])
		if !ok {
			return false
		}
		symbol := chanSendSymbolFor(valueTy)
		if symbol == "" {
			return false
		}
		declareVoidFunctionPrototype(ctx.mctx, symbol, []callArg{{ty: "ptr"}, {ty: valueTy.llvm()}})
		fmt.Fprintf(out, "  call void @%s(ptr %s, %s %s)\n", symbol, chanExpr, valueTy.llvm(), valueExpr)
		return true
	case mir.IntrinsicChanClose:
		return emitWhileUnaryVoid(ctx, out, ii, "osty_rt_thread_chan_close")
	case mir.IntrinsicGroupCancel:
		return emitWhileUnaryVoid(ctx, out, ii, "osty_rt_task_group_cancel")
	case mir.IntrinsicSelectRecv:
		args, ok := resolveWhileFixedScalarArgs(ctx, out, ii.Args, []scalarType{scalarOpaquePtr, scalarOpaquePtr, scalarOpaquePtr})
		if !ok {
			return false
		}
		declareVoidFunctionPrototype(ctx.mctx, "osty_rt_select_recv", []callArg{{ty: "ptr"}, {ty: "ptr"}, {ty: "ptr"}})
		out.WriteString(renderVoidCallLine("osty_rt_select_recv", args))
		return true
	case mir.IntrinsicSelectSend:
		if len(ii.Args) != 4 {
			return false
		}
		selectExpr, selectTy, ok := resolveOperandWithLoad(ctx, out, ii.Args[0])
		if !ok || selectTy != scalarOpaquePtr {
			return false
		}
		chanExpr, chanTy, ok := resolveOperandWithLoad(ctx, out, ii.Args[1])
		if !ok || chanTy != scalarOpaquePtr {
			return false
		}
		valueExpr, valueTy, ok := resolveOperandWithLoad(ctx, out, ii.Args[2])
		if !ok {
			return false
		}
		armExpr, armTy, ok := resolveOperandWithLoad(ctx, out, ii.Args[3])
		if !ok || armTy != scalarOpaquePtr {
			return false
		}
		symbol := selectSendSymbolFor(valueTy)
		if symbol == "" {
			return false
		}
		declareVoidFunctionPrototype(ctx.mctx, symbol, []callArg{{ty: "ptr"}, {ty: "ptr"}, {ty: valueTy.llvm()}, {ty: "ptr"}})
		fmt.Fprintf(out, "  call void @%s(ptr %s, ptr %s, %s %s, ptr %s)\n", symbol, selectExpr, chanExpr, valueTy.llvm(), valueExpr, armExpr)
		return true
	case mir.IntrinsicSelectTimeout:
		args, ok := resolveWhileFixedScalarArgs(ctx, out, ii.Args, []scalarType{scalarOpaquePtr, scalarInt, scalarOpaquePtr})
		if !ok {
			return false
		}
		declareVoidFunctionPrototype(ctx.mctx, "osty_rt_select_timeout", []callArg{{ty: "ptr"}, {ty: "i64"}, {ty: "ptr"}})
		out.WriteString(renderVoidCallLine("osty_rt_select_timeout", args))
		return true
	case mir.IntrinsicSelectDefault:
		args, ok := resolveWhileFixedScalarArgs(ctx, out, ii.Args, []scalarType{scalarOpaquePtr, scalarOpaquePtr})
		if !ok {
			return false
		}
		declareVoidFunctionPrototype(ctx.mctx, "osty_rt_select_default", []callArg{{ty: "ptr"}, {ty: "ptr"}})
		out.WriteString(renderVoidCallLine("osty_rt_select_default", args))
		return true
	}
	return emitWhileDiscardedValueIntrinsic(ctx, out, ii)
}

func emitWhileDiscardedValueIntrinsic(ctx *whileLoopEmitCtx, out *strings.Builder, ii *mir.IntrinsicInstr) bool {
	if ii == nil || ii.Dest != nil {
		return false
	}
	spec, ok := intrinsicRuntimeCallSpec(ii.Kind)
	if !ok || len(spec.args) != len(ii.Args) {
		return false
	}
	args, ok := resolveWhileFixedScalarArgs(ctx, out, ii.Args, spec.args)
	if !ok {
		return false
	}
	declareRuntimePrototype(ctx.mctx, spec.symbol, spec.ret, args)
	out.WriteString(renderDiscardValueCallLine(spec.symbol, spec.ret, args))
	return true
}

func emitWhilePrintIntrinsic(ctx *whileLoopEmitCtx, out *strings.Builder, ii *mir.IntrinsicInstr) bool {
	if len(ii.Args) != 1 {
		return false
	}
	expr, ty, ok := resolveOperandWithLoad(ctx, out, ii.Args[0])
	if !ok {
		return false
	}
	fmtGlobal, argTy, ok := printFormatFor(ii.Kind, ty)
	if !ok {
		return false
	}
	if isStderrPrintIntrinsic(ii.Kind) {
		stderrReg := ctx.mctx.freshTempName("stderr")
		fmt.Fprintf(out, "  %s = load ptr, ptr @stderr\n", stderrReg)
		fmt.Fprintf(out, "  call i32 (ptr, ptr, ...) @fprintf(ptr %s, ptr %s, %s %s)\n", stderrReg, fmtGlobal, argTy, expr)
		return true
	}
	fmt.Fprintf(out, "  call i32 (ptr, ...) @printf(ptr %s, %s %s)\n", fmtGlobal, argTy, expr)
	return true
}

func emitWhileUnaryVoid(ctx *whileLoopEmitCtx, out *strings.Builder, ii *mir.IntrinsicInstr, symbol string) bool {
	args, ok := resolveWhileFixedScalarArgs(ctx, out, ii.Args, []scalarType{scalarOpaquePtr})
	if !ok {
		return false
	}
	declareVoidFunctionPrototype(ctx.mctx, symbol, []callArg{{ty: "ptr"}})
	out.WriteString(renderVoidCallLine(symbol, args))
	return true
}

func resolveWhileFixedScalarArgs(ctx *whileLoopEmitCtx, out *strings.Builder, ops []mir.Operand, want []scalarType) ([]callArg, bool) {
	if len(ops) != len(want) {
		return nil, false
	}
	args := make([]callArg, 0, len(ops))
	for i, op := range ops {
		expr, ty, ok := resolveOperandWithLoad(ctx, out, op)
		if !ok || ty != want[i] {
			return nil, false
		}
		args = append(args, callArg{expr: expr, ty: ty.llvm()})
	}
	return args, true
}

func emitWhileValueIntrinsic(ctx *whileLoopEmitCtx, out *strings.Builder, ii *mir.IntrinsicInstr) bool {
	if ii.Dest == nil {
		return false
	}
	destIRType := placeResultType(ctx.fn, *ii.Dest)
	if destIRType == nil {
		return false
	}
	if isUnitType(destIRType) {
		return true
	}
	destType := ctx.mctx.scalarFromType(destIRType, true)
	if destType == scalarUnknown {
		return false
	}
	if ii.Dest.HasProjections() {
		return emitWhileProjectedValueIntrinsic(ctx, out, ii, destType)
	}
	destID := ii.Dest.Local

	switch ii.Kind {
	case mir.IntrinsicStringConcat:
		return emitWhileStringConcatIntrinsic(ctx, out, ii, destID, destType)
	case mir.IntrinsicByteToInt, mir.IntrinsicCharToInt, mir.IntrinsicIntToByte, mir.IntrinsicIntToChar, mir.IntrinsicByteToChar, mir.IntrinsicCharToByte:
		return emitWhilePrimitiveConversionIntrinsic(ctx, out, ii, destID, destType)
	case mir.IntrinsicListIsEmpty:
		if destType != scalarBool {
			return false
		}
		args, ok := resolveWhileFixedScalarArgs(ctx, out, ii.Args, []scalarType{scalarOpaquePtr})
		if !ok {
			return false
		}
		declareListRuntime(ctx.mctx)
		lenReg := freshReg(ctx)
		boolReg := freshReg(ctx)
		fmt.Fprintf(out, "  %s = call i64 @osty_rt_list_len(ptr %s)\n", lenReg, args[0].expr)
		fmt.Fprintf(out, "  %s = icmp eq i64 %s, 0\n", boolReg, lenReg)
		return bindWhileResult(ctx, out, destID, scalarBool, boolReg)
	case mir.IntrinsicStringIsEmpty:
		if destType != scalarBool {
			return false
		}
		args, ok := resolveWhileFixedScalarArgs(ctx, out, ii.Args, []scalarType{scalarString})
		if !ok {
			return false
		}
		declareRuntimePrototype(ctx.mctx, "osty_rt_strings_ByteLen", scalarInt, []callArg{{ty: "ptr"}})
		lenReg := freshReg(ctx)
		boolReg := freshReg(ctx)
		fmt.Fprintf(out, "  %s = call i64 @osty_rt_strings_ByteLen(ptr %s)\n", lenReg, args[0].expr)
		fmt.Fprintf(out, "  %s = icmp eq i64 %s, 0\n", boolReg, lenReg)
		return bindWhileResult(ctx, out, destID, scalarBool, boolReg)
	case mir.IntrinsicBytesContains:
		if destType != scalarBool {
			return false
		}
		args, ok := resolveWhileFixedScalarArgs(ctx, out, ii.Args, []scalarType{scalarOpaquePtr, scalarOpaquePtr})
		if !ok {
			return false
		}
		declareRuntimePrototype(ctx.mctx, "osty_rt_bytes_index_of", scalarInt, []callArg{{ty: "ptr"}, {ty: "ptr"}})
		indexReg := freshReg(ctx)
		boolReg := freshReg(ctx)
		fmt.Fprintf(out, "  %s = call i64 @osty_rt_bytes_index_of(ptr %s, ptr %s)\n", indexReg, args[0].expr, args[1].expr)
		fmt.Fprintf(out, "  %s = icmp ne i64 %s, -1\n", boolReg, indexReg)
		return bindWhileResult(ctx, out, destID, scalarBool, boolReg)
	case mir.IntrinsicListGet:
		if len(ii.Args) != 2 {
			return false
		}
		listExpr, listTy, ok := resolveOperandWithLoad(ctx, out, ii.Args[0])
		if !ok || listTy != scalarOpaquePtr {
			return false
		}
		indexExpr, indexTy, ok := resolveOperandWithLoad(ctx, out, ii.Args[1])
		if !ok || indexTy != scalarInt {
			return false
		}
		symbol := listGetSymbolFor(destType)
		if symbol == "" {
			return false
		}
		declareListGetRuntimeFor(ctx.mctx, destType)
		return emitWhileCallResult(ctx, out, destID, destType, symbol, []callArg{{expr: listExpr, ty: "ptr"}, {expr: indexExpr, ty: "i64"}})
	case mir.IntrinsicListSorted, mir.IntrinsicListToSet, mir.IntrinsicListToString:
		return emitWhileTypedListUnaryIntrinsic(ctx, out, ii, destID, destType)
	case mir.IntrinsicMapContains:
		return emitWhileMapContainsIntrinsic(ctx, out, ii, destID, destType)
	case mir.IntrinsicMapGet:
		return emitWhileMapGetIntrinsic(ctx, out, ii, destID, destType)
	case mir.IntrinsicMapNew:
		return emitWhileMapNewIntrinsic(ctx, out, destID, destType)
	case mir.IntrinsicMapKeysSorted:
		return emitWhileMapKeysSortedIntrinsic(ctx, out, ii, destID, destType)
	case mir.IntrinsicMapIncr:
		return emitWhileMapIncrIntrinsic(ctx, out, ii, destID, destType)
	case mir.IntrinsicSetContains:
		return emitWhileSetContainsIntrinsic(ctx, out, ii, destID, destType)
	case mir.IntrinsicSetNew:
		return emitWhileSetNewIntrinsic(ctx, out, destID, destType)
	case mir.IntrinsicOptionIsSome, mir.IntrinsicOptionIsNone:
		return emitWhileOptionIsIntrinsic(ctx, out, ii, destID, destType)
	case mir.IntrinsicOptionUnwrap, mir.IntrinsicOptionUnwrapOr:
		return emitWhileOptionUnwrapIntrinsic(ctx, out, ii, destID, destType)
	case mir.IntrinsicRawNull:
		if destType != scalarOpaquePtr || len(ii.Args) != 0 {
			return false
		}
		return bindWhileResult(ctx, out, destID, scalarOpaquePtr, "null")
	case mir.IntrinsicLikely, mir.IntrinsicUnlikely:
		if destType != scalarBool {
			return false
		}
		args, ok := resolveWhileFixedScalarArgs(ctx, out, ii.Args, []scalarType{scalarBool})
		if !ok {
			return false
		}
		expected := "true"
		if ii.Kind == mir.IntrinsicUnlikely {
			expected = "false"
		}
		declareRuntimePrototype(ctx.mctx, "llvm.expect.i1", scalarBool, []callArg{{ty: "i1"}, {ty: "i1"}})
		return emitWhileCallResult(ctx, out, destID, scalarBool, "llvm.expect.i1", []callArg{args[0], {expr: expected, ty: "i1"}})
	case mir.IntrinsicStringIndexOf, mir.IntrinsicStringLastIndexOf:
		// Runtime returns i64 (-1 = not found), but MIR dest is Option<Int> (opaque ptr).
		if destType != scalarOpaquePtr || len(ii.Args) != 2 {
			return false
		}
		symbol := "osty_rt_strings_IndexOf"
		if ii.Kind == mir.IntrinsicStringLastIndexOf {
			symbol = "osty_rt_strings_LastIndexOf"
		}
		args, ok := resolveWhileFixedScalarArgs(ctx, out, ii.Args, []scalarType{scalarString, scalarString})
		if !ok {
			return false
		}
		declareRuntimePrototype(ctx.mctx, symbol, scalarInt, []callArg{{ty: "ptr"}, {ty: "ptr"}})
		return emitWhileOptionI64FromRuntime(ctx, out, destID, symbol, args)
	}

	spec, ok := intrinsicRuntimeCallSpec(ii.Kind)
	if !ok || spec.ret != destType || len(spec.args) != len(ii.Args) {
		return false
	}
	args, ok := resolveWhileFixedScalarArgs(ctx, out, ii.Args, spec.args)
	if !ok {
		return false
	}
	declareRuntimePrototype(ctx.mctx, spec.symbol, spec.ret, args)
	return emitWhileCallResult(ctx, out, destID, destType, spec.symbol, args)
}

func emitWhileProjectedValueIntrinsic(ctx *whileLoopEmitCtx, out *strings.Builder, ii *mir.IntrinsicInstr, destType scalarType) bool {
	if ii == nil || ii.Dest == nil || !ii.Dest.HasProjections() {
		return false
	}
	spec, ok := intrinsicRuntimeCallSpec(ii.Kind)
	if !ok || spec.ret != destType || len(spec.args) != len(ii.Args) {
		return false
	}
	args, ok := resolveWhileFixedScalarArgs(ctx, out, ii.Args, spec.args)
	if !ok {
		return false
	}
	declareRuntimePrototype(ctx.mctx, spec.symbol, spec.ret, args)
	return emitWhileCallToPlace(ctx, out, *ii.Dest, destType, spec.symbol, args)
}

func emitWhileStringConcatIntrinsic(ctx *whileLoopEmitCtx, out *strings.Builder, ii *mir.IntrinsicInstr, destID mir.LocalID, destType scalarType) bool {
	if destType != scalarString || len(ii.Args) == 0 {
		return false
	}
	parts := make([]callArg, 0, len(ii.Args))
	hasInt := false
	convertInt := len(ii.Args) == 1
	for _, op := range ii.Args {
		arg, originalTy, ok := stringConcatWhileArg(ctx, out, op, convertInt)
		if !ok {
			return false
		}
		if originalTy == scalarInt && !convertInt {
			hasInt = true
		}
		parts = append(parts, arg)
	}
	if len(parts) == 1 {
		return bindWhileResult(ctx, out, destID, scalarString, parts[0].expr)
	}
	declareStringConcatRuntime(ctx.mctx)
	if hasInt {
		declareStringConcatI64Runtime(ctx.mctx)
	}
	current := parts[0]
	for _, next := range parts[1:] {
		symbol, ok := stringConcatSymbolForArgs(current.ty, next.ty)
		if !ok {
			return false
		}
		reg := freshReg(ctx)
		fmt.Fprintf(out, "  %s = call ptr @%s(%s %s, %s %s)\n", reg, symbol, current.ty, current.expr, next.ty, next.expr)
		current = callArg{expr: reg, ty: scalarString.llvm()}
	}
	return bindWhileResult(ctx, out, destID, scalarString, current.expr)
}

func emitWhileTypedListUnaryIntrinsic(ctx *whileLoopEmitCtx, out *strings.Builder, ii *mir.IntrinsicInstr, destID mir.LocalID, destType scalarType) bool {
	if len(ii.Args) != 1 {
		return false
	}
	expr, ty, ok := resolveOperandWithLoad(ctx, out, ii.Args[0])
	if !ok || ty != scalarOpaquePtr {
		return false
	}
	elemType := collectionArgScalar(ii.Args[0], "List", 0, ctx.mctx)
	if elemType == scalarUnknown {
		return false
	}
	var symbol string
	switch ii.Kind {
	case mir.IntrinsicListSorted:
		if destType != scalarOpaquePtr {
			return false
		}
		symbol = listSortedSymbolFor(elemType)
	case mir.IntrinsicListToSet:
		if destType != scalarOpaquePtr {
			return false
		}
		symbol = listToSetSymbolFor(elemType)
	case mir.IntrinsicListToString:
		if destType != scalarString {
			return false
		}
		symbol = listToStringSymbolFor(elemType)
	default:
		return false
	}
	if symbol == "" {
		return false
	}
	declareRuntimePrototype(ctx.mctx, symbol, destType, []callArg{{ty: "ptr"}})
	return emitWhileCallResult(ctx, out, destID, destType, symbol, []callArg{{expr: expr, ty: "ptr"}})
}

func emitWhileMapContainsIntrinsic(ctx *whileLoopEmitCtx, out *strings.Builder, ii *mir.IntrinsicInstr, destID mir.LocalID, destType scalarType) bool {
	if destType != scalarBool || len(ii.Args) != 2 {
		return false
	}
	mapExpr, mapTy, ok := resolveOperandWithLoad(ctx, out, ii.Args[0])
	if !ok || mapTy != scalarOpaquePtr {
		return false
	}
	keyExpr, keyTy, ok := resolveOperandWithLoad(ctx, out, ii.Args[1])
	if !ok {
		return false
	}
	symbol := mapContainsSymbolFor(keyTy)
	if symbol == "" {
		return false
	}
	args := []callArg{{expr: mapExpr, ty: "ptr"}, {expr: keyExpr, ty: keyTy.llvm()}}
	declareRuntimePrototype(ctx.mctx, symbol, scalarBool, args)
	return emitWhileCallResult(ctx, out, destID, scalarBool, symbol, args)
}

func emitWhileMapGetIntrinsic(ctx *whileLoopEmitCtx, out *strings.Builder, ii *mir.IntrinsicInstr, destID mir.LocalID, destType scalarType) bool {
	if destType != scalarOpaquePtr || len(ii.Args) != 2 {
		return false
	}
	destLocal := lookupLocal(ctx.fn, destID)
	if destLocal == nil {
		return false
	}
	payloadTy, ok := optionPayloadScalar(destLocal.Type, ctx.mctx)
	if !ok {
		return false
	}
	mapExpr, mapTy, ok := resolveOperandWithLoad(ctx, out, ii.Args[0])
	if !ok || mapTy != scalarOpaquePtr {
		return false
	}
	keyExpr, keyTy, ok := resolveOperandWithLoad(ctx, out, ii.Args[1])
	if !ok || keyTy == scalarUnknown {
		return false
	}
	symbol := mapGetSymbolFor(keyTy)
	if symbol == "" {
		return false
	}
	typeName, ok := ctx.mctx.emitOptionBoxDef(payloadTy)
	if !ok {
		return false
	}
	valueSlot := freshReg(ctx)
	found := freshReg(ctx)
	sizePtr := freshReg(ctx)
	size := freshReg(ctx)
	obj := freshReg(ctx)
	tag := freshReg(ctx)
	tagSlot := ctx.mctx.freshTempName("map.get.option.tag.slot")
	payloadSlot := ctx.mctx.freshTempName("map.get.option.payload.slot")
	payload := freshReg(ctx)
	zero, ok := payloadTy.zeroValue()
	if !ok {
		return false
	}
	fmt.Fprintf(out, "  %s = alloca %s\n", valueSlot, payloadTy.llvm())
	fmt.Fprintf(out, "  store %s %s, ptr %s\n", payloadTy.llvm(), zero, valueSlot)
	declareRuntimePrototype(ctx.mctx, symbol, scalarBool, []callArg{{ty: "ptr"}, {ty: keyTy.llvm()}, {ty: "ptr"}})
	fmt.Fprintf(out, "  %s = call i1 @%s(ptr %s, %s %s, ptr %s)\n", found, symbol, mapExpr, keyTy.llvm(), keyExpr, valueSlot)
	fmt.Fprintf(out, "  %s = getelementptr %%%s, ptr null, i32 1\n", sizePtr, typeName)
	fmt.Fprintf(out, "  %s = ptrtoint ptr %s to i64\n", size, sizePtr)
	declareRuntimePrototype(ctx.mctx, "osty_rt_stage0_alloc", scalarOpaquePtr, []callArg{{ty: "i64"}})
	fmt.Fprintf(out, "  %s = call ptr @osty_rt_stage0_alloc(i64 %s)\n", obj, size)
	fmt.Fprintf(out, "  %s = select i1 %s, i64 0, i64 1\n", tag, found)
	fmt.Fprintf(out, "  %s = getelementptr inbounds %%%s, ptr %s, i32 0, i32 0\n", tagSlot, typeName, obj)
	fmt.Fprintf(out, "  store i64 %s, ptr %s\n", tag, tagSlot)
	fmt.Fprintf(out, "  %s = load %s, ptr %s\n", payload, payloadTy.llvm(), valueSlot)
	fmt.Fprintf(out, "  %s = getelementptr inbounds %%%s, ptr %s, i32 0, i32 1\n", payloadSlot, typeName, obj)
	fmt.Fprintf(out, "  store %s %s, ptr %s\n", payloadTy.llvm(), payload, payloadSlot)
	return bindWhileResult(ctx, out, destID, scalarOpaquePtr, obj)
}

func emitWhileMapNewIntrinsic(ctx *whileLoopEmitCtx, out *strings.Builder, destID mir.LocalID, destType scalarType) bool {
	if destType != scalarOpaquePtr {
		return false
	}
	keyType := localCollectionArgScalar(ctx.fn, destID, "Map", 0, ctx.mctx)
	valueType := localCollectionArgScalar(ctx.fn, destID, "Map", 1, ctx.mctx)
	if keyType == scalarUnknown || valueType == scalarUnknown {
		if k, v, ok := recoverMapNewArgScalarsFromStructAggregate(ctx.fn, destID, ctx.mctx); ok {
			keyType = k
			valueType = v
		}
	}
	keyKind, ok := runtimeKindForScalar(keyType)
	if !ok {
		return false
	}
	valueKind, ok := runtimeKindForScalar(valueType)
	if !ok {
		return false
	}
	valueSize, ok := runtimeSizeForScalar(valueType)
	if !ok {
		return false
	}
	args := []callArg{
		{expr: fmt.Sprintf("%d", keyKind), ty: "i64"},
		{expr: fmt.Sprintf("%d", valueKind), ty: "i64"},
		{expr: fmt.Sprintf("%d", valueSize), ty: "i64"},
		{expr: "null", ty: "ptr"},
	}
	declareRuntimePrototype(ctx.mctx, "osty_rt_map_new", scalarOpaquePtr, args)
	return emitWhileCallResult(ctx, out, destID, scalarOpaquePtr, "osty_rt_map_new", args)
}

func recoverMapNewArgScalarsFromStructAggregate(fn *mir.Function, mapID mir.LocalID, mctx *moduleCtx) (scalarType, scalarType, bool) {
	if fn == nil || mctx == nil || mctx.module == nil || mctx.module.Layouts == nil {
		return scalarUnknown, scalarUnknown, false
	}
	key := scalarUnknown
	value := scalarUnknown
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		for _, instr := range bb.Instrs {
			ai, ok := instr.(*mir.AssignInstr)
			if !ok || ai.Dest.HasProjections() {
				continue
			}
			agg, ok := ai.Src.(*mir.AggregateRV)
			if !ok || agg == nil || agg.Kind != mir.AggStruct {
				continue
			}
			st, ok := agg.T.(*ir.NamedType)
			if !ok || st == nil || st.Name == "" {
				continue
			}
			layout := mctx.module.Layouts.Structs[st.Name]
			if layout == nil {
				continue
			}
			for i, field := range agg.Fields {
				cp, ok := field.(*mir.CopyOp)
				if !ok || cp.Place.HasProjections() || cp.Place.Local != mapID {
					continue
				}
				if i < 0 || i >= len(layout.Fields) {
					return scalarUnknown, scalarUnknown, false
				}
				mt, ok := layout.Fields[i].Type.(*ir.NamedType)
				if !ok || mt == nil || mt.Name != "Map" || len(mt.Args) < 2 {
					return scalarUnknown, scalarUnknown, false
				}
				k := mctx.scalarFromType(mt.Args[0], true)
				v := mctx.scalarFromType(mt.Args[1], true)
				if k == scalarUnknown || v == scalarUnknown {
					return scalarUnknown, scalarUnknown, false
				}
				if key != scalarUnknown && (key != k || value != v) {
					return scalarUnknown, scalarUnknown, false
				}
				key = k
				value = v
			}
		}
	}
	if key == scalarUnknown || value == scalarUnknown {
		return scalarUnknown, scalarUnknown, false
	}
	return key, value, true
}

func emitWhileMapKeysSortedIntrinsic(ctx *whileLoopEmitCtx, out *strings.Builder, ii *mir.IntrinsicInstr, destID mir.LocalID, destType scalarType) bool {
	if destType != scalarOpaquePtr || len(ii.Args) != 1 {
		return false
	}
	expr, ty, ok := resolveOperandWithLoad(ctx, out, ii.Args[0])
	if !ok || ty != scalarOpaquePtr {
		return false
	}
	keyType := collectionArgScalar(ii.Args[0], "Map", 0, ctx.mctx)
	suffix := sortableRuntimeSuffixFor(keyType)
	if suffix == "" {
		return false
	}
	symbol := "osty_rt_map_keys_sorted_" + suffix
	declareRuntimePrototype(ctx.mctx, symbol, scalarOpaquePtr, []callArg{{ty: "ptr"}})
	return emitWhileCallResult(ctx, out, destID, scalarOpaquePtr, symbol, []callArg{{expr: expr, ty: "ptr"}})
}

func emitWhileMapIncrIntrinsic(ctx *whileLoopEmitCtx, out *strings.Builder, ii *mir.IntrinsicInstr, destID mir.LocalID, destType scalarType) bool {
	if destType != scalarInt || len(ii.Args) != 3 {
		return false
	}
	mapExpr, mapTy, ok := resolveOperandWithLoad(ctx, out, ii.Args[0])
	if !ok || mapTy != scalarOpaquePtr {
		return false
	}
	keyExpr, keyTy, ok := resolveOperandWithLoad(ctx, out, ii.Args[1])
	if !ok {
		return false
	}
	deltaExpr, deltaTy, ok := resolveOperandWithLoad(ctx, out, ii.Args[2])
	if !ok || deltaTy != scalarInt {
		return false
	}
	suffix := runtimeSuffixForScalar(keyTy)
	if suffix == "" {
		return false
	}
	symbol := "osty_rt_map_incr_i64_" + suffix
	args := []callArg{{expr: mapExpr, ty: "ptr"}, {expr: keyExpr, ty: keyTy.llvm()}, {expr: deltaExpr, ty: "i64"}}
	declareRuntimePrototype(ctx.mctx, symbol, scalarInt, args)
	return emitWhileCallResult(ctx, out, destID, scalarInt, symbol, args)
}

func emitWhileSetContainsIntrinsic(ctx *whileLoopEmitCtx, out *strings.Builder, ii *mir.IntrinsicInstr, destID mir.LocalID, destType scalarType) bool {
	if destType != scalarBool || len(ii.Args) != 2 {
		return false
	}
	setExpr, setTy, ok := resolveOperandWithLoad(ctx, out, ii.Args[0])
	if !ok || setTy != scalarOpaquePtr {
		return false
	}
	elemExpr, elemTy, ok := resolveOperandWithLoad(ctx, out, ii.Args[1])
	if !ok {
		return false
	}
	symbol := setContainsSymbolFor(elemTy)
	if symbol == "" {
		return false
	}
	args := []callArg{{expr: setExpr, ty: "ptr"}, {expr: elemExpr, ty: elemTy.llvm()}}
	declareRuntimePrototype(ctx.mctx, symbol, scalarBool, args)
	return emitWhileCallResult(ctx, out, destID, scalarBool, symbol, args)
}

func emitWhileSetNewIntrinsic(ctx *whileLoopEmitCtx, out *strings.Builder, destID mir.LocalID, destType scalarType) bool {
	if destType != scalarOpaquePtr {
		return false
	}
	elemType := localCollectionArgScalar(ctx.fn, destID, "Set", 0, ctx.mctx)
	elemKind, ok := runtimeKindForScalar(elemType)
	if !ok {
		return false
	}
	args := []callArg{{expr: fmt.Sprintf("%d", elemKind), ty: "i64"}}
	declareRuntimePrototype(ctx.mctx, "osty_rt_set_new", scalarOpaquePtr, args)
	return emitWhileCallResult(ctx, out, destID, scalarOpaquePtr, "osty_rt_set_new", args)
}

func emitWhileOptionIsIntrinsic(ctx *whileLoopEmitCtx, out *strings.Builder, ii *mir.IntrinsicInstr, destID mir.LocalID, destType scalarType) bool {
	if destType != scalarBool || len(ii.Args) != 1 {
		return false
	}
	payloadTy, ok := optionPayloadScalar(ii.Args[0].Type(), ctx.mctx)
	if !ok {
		return false
	}
	typeName, ok := ctx.mctx.emitOptionBoxDef(payloadTy)
	if !ok {
		return false
	}
	optExpr, optTy, ok := resolveOperandWithLoad(ctx, out, ii.Args[0])
	if !ok || optTy != scalarOpaquePtr {
		return false
	}
	tagSlot := freshReg(ctx)
	tag := freshReg(ctx)
	result := freshReg(ctx)
	fmt.Fprintf(out, "  %s = getelementptr inbounds %%%s, ptr %s, i32 0, i32 0\n", tagSlot, typeName, optExpr)
	fmt.Fprintf(out, "  %s = load i64, ptr %s\n", tag, tagSlot)
	pred := "eq"
	if ii.Kind == mir.IntrinsicOptionIsNone {
		pred = "ne"
	}
	fmt.Fprintf(out, "  %s = icmp %s i64 %s, 0\n", result, pred, tag)
	return bindWhileResult(ctx, out, destID, scalarBool, result)
}

func emitWhileOptionUnwrapIntrinsic(ctx *whileLoopEmitCtx, out *strings.Builder, ii *mir.IntrinsicInstr, destID mir.LocalID, destType scalarType) bool {
	if len(ii.Args) == 0 || len(ii.Args) > 2 {
		return false
	}
	payloadTy, ok := optionPayloadScalar(ii.Args[0].Type(), ctx.mctx)
	if !ok || payloadTy != destType {
		return false
	}
	if ii.Kind == mir.IntrinsicOptionUnwrap && len(ii.Args) != 1 {
		return false
	}
	if ii.Kind == mir.IntrinsicOptionUnwrapOr && len(ii.Args) != 2 {
		return false
	}
	typeName, ok := ctx.mctx.emitOptionBoxDef(payloadTy)
	if !ok {
		return false
	}
	optExpr, optTy, ok := resolveOperandWithLoad(ctx, out, ii.Args[0])
	if !ok || optTy != scalarOpaquePtr {
		return false
	}
	payloadSlot := freshReg(ctx)
	payload := freshReg(ctx)
	fmt.Fprintf(out, "  %s = getelementptr inbounds %%%s, ptr %s, i32 0, i32 1\n", payloadSlot, typeName, optExpr)
	fmt.Fprintf(out, "  %s = load %s, ptr %s\n", payload, payloadTy.llvm(), payloadSlot)
	if ii.Kind == mir.IntrinsicOptionUnwrap {
		return bindWhileResult(ctx, out, destID, payloadTy, payload)
	}
	tagSlot := freshReg(ctx)
	tag := freshReg(ctx)
	isSome := freshReg(ctx)
	result := freshReg(ctx)
	fallback, fallbackTy, ok := resolveOperandWithLoad(ctx, out, ii.Args[1])
	if !ok || fallbackTy != payloadTy {
		return false
	}
	fmt.Fprintf(out, "  %s = getelementptr inbounds %%%s, ptr %s, i32 0, i32 0\n", tagSlot, typeName, optExpr)
	fmt.Fprintf(out, "  %s = load i64, ptr %s\n", tag, tagSlot)
	fmt.Fprintf(out, "  %s = icmp eq i64 %s, 0\n", isSome, tag)
	fmt.Fprintf(out, "  %s = select i1 %s, %s %s, %s %s\n", result, isSome, payloadTy.llvm(), payload, payloadTy.llvm(), fallback)
	return bindWhileResult(ctx, out, destID, payloadTy, result)
}

func emitWhileCallResult(ctx *whileLoopEmitCtx, out *strings.Builder, destID mir.LocalID, destType scalarType, symbol string, args []callArg) bool {
	reg := freshReg(ctx)
	fmt.Fprintf(out, "  %s = call %s @%s(", reg, destType.llvm(), symbol)
	for i, a := range args {
		if i > 0 {
			out.WriteString(", ")
		}
		fmt.Fprintf(out, "%s %s", a.ty, a.expr)
	}
	out.WriteString(")\n")
	return bindWhileResult(ctx, out, destID, destType, reg)
}

func bindWhileResult(ctx *whileLoopEmitCtx, out *strings.Builder, destID mir.LocalID, destType scalarType, expr string) bool {
	if destType == scalarUnknown || expr == "" {
		return false
	}
	if _, isStack := ctx.stack[destID]; isStack {
		fmt.Fprintf(out, "  store %s %s, ptr %%%s\n", destType.llvm(), expr, ctx.stack[destID].name)
		return true
	}
	if existing, found := ctx.bindings[destID]; found && existing.defined && !existing.isStack {
		return false
	}
	ctx.bindings[destID] = localBinding{expr: expr, ty: destType, defined: true}
	return true
}

func emitWhileOptionI64FromRuntime(ctx *whileLoopEmitCtx, out *strings.Builder, destID mir.LocalID, symbol string, args []callArg) bool {
	// Call the i64 runtime, then allocate an option box and store tag + value.
	resultReg := freshReg(ctx)
	fmt.Fprintf(out, "  %s = call i64 @%s(", resultReg, symbol)
	for i, a := range args {
		if i > 0 {
			out.WriteString(", ")
		}
		fmt.Fprintf(out, "%s %s", a.ty, a.expr)
	}
	out.WriteString(")\n")

	// Emit option box type: { i64 tag, i64 value }
	typeName, ok := ctx.mctx.emitOptionBoxDef(scalarInt)
	if !ok {
		return false
	}

	// Allocate box using same pattern as emitWhileMapGetIntrinsic
	found := freshReg(ctx)
	fmt.Fprintf(out, "  %s = icmp ne i64 %s, -1\n", found, resultReg)
	sizePtr := freshReg(ctx)
	size := freshReg(ctx)
	obj := freshReg(ctx)
	tag := freshReg(ctx)
	tagSlot := ctx.mctx.freshTempName("indexOf.tag.slot")
	payloadSlot := ctx.mctx.freshTempName("indexOf.payload.slot")
	fmt.Fprintf(out, "  %s = getelementptr %%%s, ptr null, i32 1\n", sizePtr, typeName)
	fmt.Fprintf(out, "  %s = ptrtoint ptr %s to i64\n", size, sizePtr)
	declareRuntimePrototype(ctx.mctx, "osty_rt_stage0_alloc", scalarOpaquePtr, []callArg{{ty: "i64"}})
	fmt.Fprintf(out, "  %s = call ptr @osty_rt_stage0_alloc(i64 %s)\n", obj, size)
	fmt.Fprintf(out, "  %s = select i1 %s, i64 0, i64 1\n", tag, found)
	fmt.Fprintf(out, "  %s = getelementptr inbounds %%%s, ptr %s, i32 0, i32 0\n", tagSlot, typeName, obj)
	fmt.Fprintf(out, "  store i64 %s, ptr %s\n", tag, tagSlot)
	fmt.Fprintf(out, "  %s = getelementptr inbounds %%%s, ptr %s, i32 0, i32 1\n", payloadSlot, typeName, obj)
	fmt.Fprintf(out, "  store i64 %s, ptr %s\n", resultReg, payloadSlot)

	return bindWhileResult(ctx, out, destID, scalarOpaquePtr, obj)
}

func emitWhileCallToPlace(ctx *whileLoopEmitCtx, out *strings.Builder, dest mir.Place, destType scalarType, symbol string, args []callArg) bool {
	if destType == scalarUnknown || symbol == "" {
		return false
	}
	reg := freshReg(ctx)
	fmt.Fprintf(out, "  %s = call %s @%s(", reg, destType.llvm(), symbol)
	for i, a := range args {
		if i > 0 {
			out.WriteString(", ")
		}
		fmt.Fprintf(out, "%s %s", a.ty, a.expr)
	}
	out.WriteString(")\n")
	if dest.HasProjections() {
		if _, ok := dest.Projections[len(dest.Projections)-1].(*mir.IndexProj); ok {
			return emitWhileStoreValueToIndexedPlace(ctx, out, dest, destType, reg)
		}
		slot, fieldTy, ok := resolveWhileProjectedFieldSlot(ctx, out, dest, "call.field.store.slot")
		if !ok || fieldTy != destType {
			return false
		}
		fmt.Fprintf(out, "  store %s %s, ptr %s\n", destType.llvm(), reg, slot)
		return true
	}
	return bindWhileResult(ctx, out, dest.Local, destType, reg)
}

func emitWhileStoreValueToIndexedPlace(ctx *whileLoopEmitCtx, out *strings.Builder, dest mir.Place, elemTy scalarType, valueExpr string) bool {
	if ctx == nil || out == nil || !dest.HasProjections() || elemTy == scalarUnknown || valueExpr == "" {
		return false
	}
	idxProj, ok := dest.Projections[len(dest.Projections)-1].(*mir.IndexProj)
	if !ok {
		return false
	}
	if idxElemTy := ctx.mctx.scalarFromType(idxProj.ElemType, true); idxElemTy != elemTy {
		return false
	}
	listPlace := mir.Place{
		Local:       dest.Local,
		Projections: append([]mir.Projection(nil), dest.Projections[:len(dest.Projections)-1]...),
	}
	listTy := placeResultType(ctx.fn, listPlace)
	if listTy == nil {
		return false
	}
	listExpr, listScalarTy, ok := resolveOperandWithLoad(ctx, out, &mir.CopyOp{Place: listPlace, T: listTy})
	if !ok || listScalarTy != scalarOpaquePtr {
		return false
	}
	indexExpr, indexTy, ok := resolveOperandWithLoad(ctx, out, idxProj.Index)
	if !ok || indexTy != scalarInt {
		return false
	}
	if emitWhileListSetBytes(ctx, out, listExpr, indexExpr, valueExpr, elemTy) {
		return true
	}
	symbol := listSetSymbolFor(elemTy)
	if symbol == "" {
		return false
	}
	declareListSetRuntimeFor(ctx.mctx, elemTy)
	fmt.Fprintf(out, "  call void @%s(ptr %s, i64 %s, %s %s)\n", symbol, listExpr, indexExpr, elemTy.llvm(), valueExpr)
	return true
}

func emitWhileAssign(ctx *whileLoopEmitCtx, out *strings.Builder, ai *mir.AssignInstr) bool {
	if ai.Dest.HasProjections() {
		if _, ok := ai.Dest.Projections[len(ai.Dest.Projections)-1].(*mir.IndexProj); ok {
			return emitWhileIndexedWrite(ctx, out, ai)
		}
		return emitWhileFieldWrite(ctx, out, ai)
	}
	destID := ai.Dest.Local
	destLocal := lookupLocal(ctx.fn, destID)
	if destLocal == nil {
		return false
	}
	if isUnitType(destLocal.Type) {
		return true
	}

	// Aggregate return local: dest is an opaque-ptr sret slot.
	// Handle AggregateRV writes by emitting the aggregate value
	// and storing it to the sret pointer.
	if ctx.aggRetSRet && destID == ctx.fn.ReturnLocal {
		sd, ok := ctx.stack[destID]
		if !ok || sd.ty != scalarOpaquePtr {
			return false
		}
		agg, ok := ai.Src.(*mir.AggregateRV)
		if !ok {
			return false
		}
		// For tuple aggregates the value is built inline (not
		// heap-allocated), so emitWhileAggregateRValue stores it
		// directly to the sret slot internally.
		if agg.Kind == mir.AggTuple {
			_, _, ok := emitWhileAggregateRValue(ctx, out, agg, scalarOpaquePtr, destLocal.Type)
			return ok
		}
		expr, ty, ok := emitWhileAggregateRValue(ctx, out, agg, scalarOpaquePtr, destLocal.Type)
		if !ok || ty != scalarOpaquePtr {
			return false
		}
		fmt.Fprintf(out, "  store ptr %s, ptr %%%s\n", expr, sd.name)
		return true
	}

	destType := ctx.mctx.scalarFromType(destLocal.Type, true)
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
		expr, ty, ok := emitWhileBinaryRValue(ctx, out, src, destType)
		if !ok {
			return false
		}
		rhsExpr = expr
		rhsTy = ty
	case *mir.UnaryRV:
		expr, ty, ok := emitWhileUnaryRValue(ctx, out, src, destType)
		if !ok {
			return false
		}
		rhsExpr = expr
		rhsTy = ty
	case *mir.DiscriminantRV:
		expr, ty, ok := emitWhileDiscriminantRValue(ctx, out, src, destType)
		if !ok {
			return false
		}
		rhsExpr = expr
		rhsTy = ty
	case *mir.AggregateRV:
		expr, ty, ok := emitWhileAggregateRValue(ctx, out, src, destType, destLocal.Type)
		if !ok {
			return false
		}
		rhsExpr = expr
		rhsTy = ty
	case *mir.NullaryRV:
		expr, ty, ok := emitWhileNullaryRValue(ctx, out, src, destType)
		if !ok {
			return false
		}
		rhsExpr = expr
		rhsTy = ty
	case *mir.GlobalRefRV:
		expr, ty, ok := resolveGlobalRefRValue(ctx.mctx, src)
		if !ok {
			return false
		}
		rhsExpr = expr
		rhsTy = ty
	case *mir.LenRV:
		expr, ty, ok := emitWhileLenRValue(ctx, out, src, destType)
		if !ok {
			return false
		}
		rhsExpr = expr
		rhsTy = ty
	default:
		return false
	}
	if rhsTy != destType {
		return false
	}

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

func emitWhileFieldWrite(ctx *whileLoopEmitCtx, out *strings.Builder, ai *mir.AssignInstr) bool {
	if ai == nil || !ai.Dest.HasProjections() {
		return false
	}
	slot, fieldTy, ok := resolveWhileProjectedFieldSlot(ctx, out, ai.Dest, "field.store.slot")
	if !ok {
		return false
	}
	valueExpr, valueTy, ok := resolveWhileStoreRValue(ctx, out, ai.Src, fieldTy)
	if !ok || valueTy != fieldTy {
		return false
	}
	fmt.Fprintf(out, "  store %s %s, ptr %s\n", fieldTy.llvm(), valueExpr, slot)
	return true
}

func emitWhileIndexedWrite(ctx *whileLoopEmitCtx, out *strings.Builder, ai *mir.AssignInstr) bool {
	if ai == nil || !ai.Dest.HasProjections() {
		return false
	}
	idxProj, ok := ai.Dest.Projections[len(ai.Dest.Projections)-1].(*mir.IndexProj)
	if !ok {
		return false
	}
	elemTy := ctx.mctx.scalarFromType(idxProj.ElemType, true)
	if elemTy == scalarUnknown {
		return false
	}
	listPlace := mir.Place{
		Local:       ai.Dest.Local,
		Projections: append([]mir.Projection(nil), ai.Dest.Projections[:len(ai.Dest.Projections)-1]...),
	}
	listTy := placeResultType(ctx.fn, listPlace)
	if listTy == nil {
		return false
	}
	listExpr, listScalarTy, ok := resolveOperandWithLoad(ctx, out, &mir.CopyOp{Place: listPlace, T: listTy})
	if !ok || listScalarTy != scalarOpaquePtr {
		return false
	}
	indexExpr, indexTy, ok := resolveOperandWithLoad(ctx, out, idxProj.Index)
	if !ok || indexTy != scalarInt {
		return false
	}
	valueExpr, valueTy, ok := resolveWhileStoreRValue(ctx, out, ai.Src, elemTy)
	if !ok || valueTy != elemTy {
		return false
	}
	if emitWhileListSetBytes(ctx, out, listExpr, indexExpr, valueExpr, elemTy) {
		return true
	}
	symbol := listSetSymbolFor(elemTy)
	if symbol == "" {
		return false
	}
	declareListSetRuntimeFor(ctx.mctx, elemTy)
	fmt.Fprintf(out, "  call void @%s(ptr %s, i64 %s, %s %s)\n", symbol, listExpr, indexExpr, elemTy.llvm(), valueExpr)
	return true
}

func resolveWhileStoreRValue(ctx *whileLoopEmitCtx, out *strings.Builder, src mir.RValue, destType scalarType) (string, scalarType, bool) {
	if use, ok := src.(*mir.UseRV); ok {
		return resolveOperandWithLoad(ctx, out, use.Op)
	}
	if bin, ok := src.(*mir.BinaryRV); ok {
		return emitWhileBinaryRValue(ctx, out, bin, destType)
	}
	if unary, ok := src.(*mir.UnaryRV); ok {
		return emitWhileUnaryRValue(ctx, out, unary, destType)
	}
	if discr, ok := src.(*mir.DiscriminantRV); ok {
		return emitWhileDiscriminantRValue(ctx, out, discr, destType)
	}
	if agg, ok := src.(*mir.AggregateRV); ok && agg.Kind == mir.AggEnumVariant && destType == scalarInt && len(agg.Fields) == 0 {
		return fmt.Sprintf("%d", agg.VariantIdx), scalarInt, true
	}
	if agg, ok := src.(*mir.AggregateRV); ok {
		return emitWhileAggregateRValue(ctx, out, agg, destType, nil)
	}
	if rv, ok := src.(*mir.NullaryRV); ok {
		return emitWhileNullaryRValue(ctx, out, rv, destType)
	}
	if global, ok := src.(*mir.GlobalRefRV); ok {
		return resolveGlobalRefRValue(ctx.mctx, global)
	}
	if rv, ok := src.(*mir.LenRV); ok {
		return emitWhileLenRValue(ctx, out, rv, destType)
	}
	return "", scalarUnknown, false
}

func emitWhileBinaryRValue(ctx *whileLoopEmitCtx, out *strings.Builder, bin *mir.BinaryRV, destType scalarType) (string, scalarType, bool) {
	if bin == nil {
		return "", scalarUnknown, false
	}
	if bin.Op == mir.BinSub && destType == scalarInt && isUnitOperand(bin.Left) {
		right, rightTy, ok := resolveOperandWithLoad(ctx, out, bin.Right)
		if !ok || rightTy != scalarInt {
			return "", scalarUnknown, false
		}
		reg := freshReg(ctx)
		fmt.Fprintf(out, "  %s = sub i64 0, %s\n", reg, right)
		return reg, scalarInt, true
	}
	if bin.Op == mir.BinAdd && destType == scalarString {
		leftArg, leftOriginalTy, ok := stringConcatWhileArg(ctx, out, bin.Left, false)
		if !ok {
			return "", scalarUnknown, false
		}
		rightArg, rightOriginalTy, ok := stringConcatWhileArg(ctx, out, bin.Right, false)
		if !ok {
			return "", scalarUnknown, false
		}
		symbol, ok := stringConcatSymbolForArgs(leftArg.ty, rightArg.ty)
		if !ok {
			return "", scalarUnknown, false
		}
		declareStringConcatRuntime(ctx.mctx)
		if leftOriginalTy == scalarInt || rightOriginalTy == scalarInt {
			declareStringConcatI64Runtime(ctx.mctx)
		}
		reg := freshReg(ctx)
		fmt.Fprintf(out, "  %s = call ptr @%s(%s %s, %s %s)\n", reg, symbol, leftArg.ty, leftArg.expr, rightArg.ty, rightArg.expr)
		return reg, scalarString, true
	}
	if (bin.Op == mir.BinEq || bin.Op == mir.BinNeq) && destType == scalarBool {
		if expr, ok := emitWhilePayloadlessEnumFnConstCompare(ctx, out, bin); ok {
			return expr, scalarBool, true
		}
		if ctx.mctx.scalarFromType(bin.Left.Type(), true) == scalarString {
			left, leftTy, ok := resolveOperandWithLoad(ctx, out, bin.Left)
			if !ok || leftTy != scalarString {
				return "", scalarUnknown, false
			}
			right, rightTy, ok := resolveOperandWithLoad(ctx, out, bin.Right)
			if !ok || rightTy != scalarString {
				return "", scalarUnknown, false
			}
			declareStringEqualRuntime(ctx.mctx)
			eqReg := freshReg(ctx)
			fmt.Fprintf(out, "  %s = call i1 @osty_rt_strings_Equal(ptr %s, ptr %s)\n", eqReg, left, right)
			if bin.Op == mir.BinEq {
				return eqReg, scalarBool, true
			}
			neqReg := freshReg(ctx)
			fmt.Fprintf(out, "  %s = xor i1 %s, true\n", neqReg, eqReg)
			return neqReg, scalarBool, true
		}
		if ctx.mctx.scalarFromType(bin.Left.Type(), true) == scalarBool {
			left, leftTy, ok := resolveOperandWithLoad(ctx, out, bin.Left)
			if !ok || leftTy != scalarBool {
				return "", scalarUnknown, false
			}
			right, rightTy, ok := resolveOperandWithLoad(ctx, out, bin.Right)
			if !ok || rightTy != scalarBool {
				return "", scalarUnknown, false
			}
			op := "icmp eq"
			if bin.Op == mir.BinNeq {
				op = "icmp ne"
			}
			reg := freshReg(ctx)
			fmt.Fprintf(out, "  %s = %s i1 %s, %s\n", reg, op, left, right)
			return reg, scalarBool, true
		}
	}
	if pred := stringComparePredicate(bin.Op); pred != "" && destType == scalarBool && ctx.mctx.scalarFromType(bin.Left.Type(), true) == scalarString {
		left, leftTy, ok := resolveOperandWithLoad(ctx, out, bin.Left)
		if !ok || leftTy != scalarString {
			return "", scalarUnknown, false
		}
		right, rightTy, ok := resolveOperandWithLoad(ctx, out, bin.Right)
		if !ok || rightTy != scalarString {
			return "", scalarUnknown, false
		}
		declareStringCompareRuntime(ctx.mctx)
		cmpReg := freshReg(ctx)
		resultReg := freshReg(ctx)
		fmt.Fprintf(out, "  %s = call i64 @osty_rt_strings_Compare(ptr %s, ptr %s)\n", cmpReg, left, right)
		fmt.Fprintf(out, "  %s = icmp %s i64 %s, 0\n", resultReg, pred, cmpReg)
		return resultReg, scalarBool, true
	}
	if pred := byteComparePredicate(bin.Op); pred != "" && destType == scalarBool && isUnsignedOrdinalScalar(ctx.mctx.scalarFromType(bin.Left.Type(), true)) {
		left, leftTy, ok := resolveOperandWithLoad(ctx, out, bin.Left)
		if !ok || !isUnsignedOrdinalScalar(leftTy) {
			return "", scalarUnknown, false
		}
		right, rightTy, ok := resolveOperandWithLoad(ctx, out, bin.Right)
		if !ok || rightTy != leftTy {
			return "", scalarUnknown, false
		}
		reg := freshReg(ctx)
		fmt.Fprintf(out, "  %s = icmp %s %s %s, %s\n", reg, pred, leftTy.llvm(), left, right)
		return reg, scalarBool, true
	}
	llvmOp, resultType, operandType := classifyBinary(bin.Op)
	if llvmOp != "" && resultType != destType && destType == scalarFloat {
		llvmOp2, resultType2, operandType2 := classifyBinaryForType(bin.Op, scalarFloat)
		if llvmOp2 != "" && resultType2 == destType {
			llvmOp, resultType, operandType = llvmOp2, resultType2, operandType2
		}
	}
	if llvmOp == "" || resultType != destType {
		return "", scalarUnknown, false
	}
	left, leftTy, ok := resolveOperandWithLoad(ctx, out, bin.Left)
	if !ok || leftTy != operandType {
		// Byte → Int promotion: if both operands are byte and result is int,
		// extend both to i64 then perform the int operation.
		if ok && leftTy == scalarByte && operandType == scalarInt && destType == scalarInt {
			right, rightTy, okR := resolveOperandWithLoad(ctx, out, bin.Right)
			if !okR || rightTy != scalarByte {
				return "", scalarUnknown, false
			}
			extLeft := freshReg(ctx)
			fmt.Fprintf(out, "  %s = zext i8 %s to i64\n", extLeft, left)
			extRight := freshReg(ctx)
			fmt.Fprintf(out, "  %s = zext i8 %s to i64\n", extRight, right)
			reg := freshReg(ctx)
			fmt.Fprintf(out, "  %s = %s i64 %s, %s\n", reg, llvmOp, extLeft, extRight)
			return reg, scalarInt, true
		}
		if ok && leftTy == scalarFloat && operandType == scalarInt {
			llvmOp2, resultType2, operandType2 := classifyBinaryForType(bin.Op, scalarFloat)
			if llvmOp2 == "" || resultType2 != destType {
				return "", scalarUnknown, false
			}
			right, rightTy, ok := resolveOperandWithLoad(ctx, out, bin.Right)
			if !ok || rightTy != operandType2 {
				return "", scalarUnknown, false
			}
			reg := freshReg(ctx)
			fmt.Fprintf(out, "  %s = %s %s %s, %s\n", reg, llvmOp2, operandType2.llvm(), left, right)
			return reg, resultType2, true
		}
		return "", scalarUnknown, false
	}
	right, rightTy, ok := resolveOperandWithLoad(ctx, out, bin.Right)
	if !ok || rightTy != operandType {
		return "", scalarUnknown, false
	}
	reg := freshReg(ctx)
	fmt.Fprintf(out, "  %s = %s %s %s, %s\n", reg, llvmOp, operandType.llvm(), left, right)
	return reg, resultType, true
}

func emitWhilePayloadlessEnumFnConstCompare(ctx *whileLoopEmitCtx, out *strings.Builder, bin *mir.BinaryRV) (string, bool) {
	if ctx == nil || bin == nil || (bin.Op != mir.BinEq && bin.Op != mir.BinNeq) {
		return "", false
	}
	if left, leftTy, ok := resolveOperandWithLoad(ctx, out, bin.Left); ok && leftTy == scalarInt {
		if idx, ok := fnConstPayloadlessEnumVariantIndex(bin.Right, bin.Left.Type(), ctx.mctx); ok {
			op := "icmp eq"
			if bin.Op == mir.BinNeq {
				op = "icmp ne"
			}
			reg := freshReg(ctx)
			fmt.Fprintf(out, "  %s = %s i64 %s, %d\n", reg, op, left, idx)
			return reg, true
		}
	}
	if right, rightTy, ok := resolveOperandWithLoad(ctx, out, bin.Right); ok && rightTy == scalarInt {
		if idx, ok := fnConstPayloadlessEnumVariantIndex(bin.Left, bin.Right.Type(), ctx.mctx); ok {
			op := "icmp eq"
			if bin.Op == mir.BinNeq {
				op = "icmp ne"
			}
			reg := freshReg(ctx)
			fmt.Fprintf(out, "  %s = %s i64 %d, %s\n", reg, op, idx, right)
			return reg, true
		}
	}
	return "", false
}

func emitWhileUnaryRValue(ctx *whileLoopEmitCtx, out *strings.Builder, un *mir.UnaryRV, destType scalarType) (string, scalarType, bool) {
	if un == nil {
		return "", scalarUnknown, false
	}
	expr, ty, ok := resolveOperandWithLoad(ctx, out, un.Arg)
	if !ok {
		return "", scalarUnknown, false
	}
	switch un.Op {
	case mir.UnPlus:
		if destType != scalarInt || ty != scalarInt {
			return "", scalarUnknown, false
		}
		return expr, scalarInt, true
	case mir.UnNeg:
		if destType != scalarInt || ty != scalarInt {
			return "", scalarUnknown, false
		}
		reg := freshReg(ctx)
		fmt.Fprintf(out, "  %s = sub i64 0, %s\n", reg, expr)
		return reg, scalarInt, true
	case mir.UnNot:
		if destType != scalarBool || ty != scalarBool {
			return "", scalarUnknown, false
		}
		reg := freshReg(ctx)
		fmt.Fprintf(out, "  %s = xor i1 %s, true\n", reg, expr)
		return reg, scalarBool, true
	case mir.UnBitNot:
		if destType != scalarInt || ty != scalarInt {
			return "", scalarUnknown, false
		}
		reg := freshReg(ctx)
		fmt.Fprintf(out, "  %s = xor i64 %s, -1\n", reg, expr)
		return reg, scalarInt, true
	}
	return "", scalarUnknown, false
}

func emitWhileDiscriminantRValue(ctx *whileLoopEmitCtx, out *strings.Builder, discr *mir.DiscriminantRV, destType scalarType) (string, scalarType, bool) {
	if discr == nil || destType != scalarInt {
		return "", scalarUnknown, false
	}
	placeTy := placeResultType(ctx.fn, discr.Place)
	if placeTy == nil {
		return "", scalarUnknown, false
	}
	if named, ok := placeTy.(*ir.NamedType); ok && named != nil {
		if layout := ctx.mctx.module.Layouts.Enums[named.Name]; layout != nil && !enumLayoutIsPayloadless(layout) && len(layout.Variants) > 0 {
			expr, ty, ok := resolveOperandWithLoad(ctx, out, &mir.CopyOp{Place: discr.Place, T: placeTy})
			if !ok {
				return "", scalarUnknown, false
			}
			if ty == scalarOpaquePtr {
				tagSlot := freshReg(ctx)
				tag := freshReg(ctx)
				fmt.Fprintf(out, "  %s = getelementptr i64, ptr %s, i64 0\n", tagSlot, expr)
				fmt.Fprintf(out, "  %s = load i64, ptr %s\n", tag, tagSlot)
				return tag, scalarInt, true
			}
			if ty == scalarInt {
				return expr, scalarInt, true
			}
			return "", scalarUnknown, false
		}
	}
	if payloadTy, ok := optionPayloadScalar(placeTy, ctx.mctx); ok {
		typeName, ok := ctx.mctx.emitOptionBoxDef(payloadTy)
		if !ok {
			return "", scalarUnknown, false
		}
		expr, ty, ok := resolveOperandWithLoad(ctx, out, &mir.CopyOp{Place: discr.Place, T: placeTy})
		if !ok || ty != scalarOpaquePtr {
			return "", scalarUnknown, false
		}
		tagSlot := freshReg(ctx)
		tag := freshReg(ctx)
		fmt.Fprintf(out, "  %s = getelementptr inbounds %%%s, ptr %s, i32 0, i32 0\n", tagSlot, typeName, expr)
		fmt.Fprintf(out, "  %s = load i64, ptr %s\n", tag, tagSlot)
		return tag, scalarInt, true
	}
	if okTy, errTy, ok := resultPayloadScalars(placeTy, ctx.mctx); ok {
		typeName, ok := ctx.mctx.emitResultBoxDef(okTy, errTy)
		if !ok {
			return "", scalarUnknown, false
		}
		expr, ty, ok := resolveOperandWithLoad(ctx, out, &mir.CopyOp{Place: discr.Place, T: placeTy})
		if !ok || ty != scalarOpaquePtr {
			return "", scalarUnknown, false
		}
		tagSlot := freshReg(ctx)
		tag := freshReg(ctx)
		fmt.Fprintf(out, "  %s = getelementptr inbounds %%%s, ptr %s, i32 0, i32 0\n", tagSlot, typeName, expr)
		fmt.Fprintf(out, "  %s = load i64, ptr %s\n", tag, tagSlot)
		return tag, scalarInt, true
	}
	expr, ty, ok := resolveOperandWithLoad(ctx, out, &mir.CopyOp{Place: discr.Place, T: placeTy})
	if !ok {
		return "", scalarUnknown, false
	}
	if ty == scalarInt {
		return expr, scalarInt, true
	}
	return "", scalarUnknown, false
}

func emitWhileAggregateRValue(ctx *whileLoopEmitCtx, out *strings.Builder, agg *mir.AggregateRV, destType scalarType, destIRType mir.Type) (string, scalarType, bool) {
	if ctx == nil || ctx.mctx == nil || agg == nil {
		return "", scalarUnknown, false
	}
	if isErrType(agg.T) && destIRType != nil && !isErrType(destIRType) {
		cp := *agg
		cp.T = destIRType
		agg = &cp
	}
	if agg != nil && agg.Kind == mir.AggEnumVariant && destType == scalarInt && len(agg.Fields) == 0 {
		return fmt.Sprintf("%d", agg.VariantIdx), scalarInt, true
	}
	if agg != nil && agg.Kind == mir.AggEnumVariant && destType == scalarOpaquePtr {
		if expr, ty, ok := emitWhileOptionAggregateRValue(ctx, out, agg); ok {
			return expr, ty, true
		}
		if expr, ty, ok := emitWhileResultAggregateRValue(ctx, out, agg); ok {
			return expr, ty, true
		}
		return emitWhileEnumAggregateRValue(ctx, out, agg)
	}
	if agg != nil && agg.Kind == mir.AggStruct && destType == scalarOpaquePtr {
		return emitWhileStructAggregateRValue(ctx, out, agg)
	}
	// Tuple construction: emit insertvalue chain when the dest is an
	// sret aggregate-return slot, or build an SSA value for
	// intermediate tuple locals.
	if agg != nil && agg.Kind == mir.AggTuple {
		tupleTy, ok := agg.T.(*ir.TupleType)
		if !ok || tupleTy == nil {
			return "", scalarUnknown, false
		}
		fields := make([]scalarType, len(tupleTy.Elems))
		for i, e := range tupleTy.Elems {
			st := ctx.mctx.scalarFromType(e, true)
			if st == scalarUnknown {
				return "", scalarUnknown, false
			}
			fields[i] = st
		}
		typeN := ctx.mctx.internTupleType(fields)
		if len(agg.Fields) != len(fields) {
			return "", scalarUnknown, false
		}
		fieldExprs := make([]string, len(agg.Fields))
		for i, f := range agg.Fields {
			expr, ty, ok := resolveOperandWithLoad(ctx, out, f)
			if !ok || ty != fields[i] {
				return "", scalarUnknown, false
			}
			fieldExprs[i] = expr
		}
		// Build the tuple via insertvalue chain.
		result := "undef"
		for i, fe := range fieldExprs {
			reg := freshReg(ctx)
			fmt.Fprintf(out, "  %s = insertvalue %%%s %s, %s %s, %d\n", reg, typeN, result, fields[i].llvm(), fe, i)
			result = reg
		}
		// Store the built tuple value directly to the sret slot.
		if ctx.aggRetSRet {
			for _, sd := range ctx.stack {
				if sd.id == ctx.fn.ReturnLocal && sd.ty == scalarOpaquePtr {
					fmt.Fprintf(out, "  store %%%s %s, ptr %%%s\n", typeN, result, sd.name)
					break
				}
			}
		}
		return result, scalarOpaquePtr, true
	}
	if agg == nil || agg.Kind != mir.AggList || destType != scalarOpaquePtr {
		return "", scalarUnknown, false
	}
	values := make([]callArg, 0, len(agg.Fields))
	elemType := scalarUnknown
	for _, field := range agg.Fields {
		expr, ty, ok := resolveOperandWithLoad(ctx, out, field)
		if !ok || ty == scalarUnknown {
			return "", scalarUnknown, false
		}
		if elemType == scalarUnknown {
			elemType = ty
		}
		if ty != elemType {
			return "", scalarUnknown, false
		}
		values = append(values, callArg{expr: expr, ty: ty.llvm()})
	}
	pushSymbol := listPushSymbolFor(elemType)
	if pushSymbol == "" && len(values) > 0 {
		return "", scalarUnknown, false
	}
	declareListRuntime(ctx.mctx)
	listReg := freshReg(ctx)
	fmt.Fprintf(out, "  %s = call ptr @osty_rt_list_new()\n", listReg)
	for _, value := range values {
		fmt.Fprintf(out, "  call void @%s(ptr %s, %s %s)\n", pushSymbol, listReg, value.ty, value.expr)
	}
	return listReg, scalarOpaquePtr, true
}

func enumVariantPayloadScalars(t mir.Type, variantIdx int, mctx *moduleCtx) (string, []scalarType, bool) {
	if mctx == nil || mctx.module == nil || mctx.module.Layouts == nil || variantIdx < 0 {
		return "", nil, false
	}
	named, ok := t.(*ir.NamedType)
	if !ok || named == nil || named.Name == "" {
		return "", nil, false
	}
	layout := mctx.module.Layouts.Enums[named.Name]
	if layout == nil || variantIdx >= len(layout.Variants) {
		return "", nil, false
	}
	variant := layout.Variants[variantIdx]
	payload := make([]scalarType, len(variant.Payload))
	for i, field := range variant.Payload {
		st := mctx.scalarFromType(field.Type, true)
		if st == scalarUnknown {
			return "", nil, false
		}
		payload[i] = st
	}
	return named.Name, payload, true
}

func emitWhileEnumAggregateRValue(ctx *whileLoopEmitCtx, out *strings.Builder, agg *mir.AggregateRV) (string, scalarType, bool) {
	enumName, payloadTypes, ok := enumVariantPayloadScalars(agg.T, agg.VariantIdx, ctx.mctx)
	if !ok || len(payloadTypes) != len(agg.Fields) {
		return "", scalarUnknown, false
	}
	typeName, ok := ctx.mctx.emitEnumBoxDef(enumName, agg.VariantIdx, payloadTypes)
	if !ok {
		return "", scalarUnknown, false
	}
	sizePtr := freshReg(ctx)
	size := freshReg(ctx)
	obj := freshReg(ctx)
	fmt.Fprintf(out, "  %s = getelementptr %%%s, ptr null, i32 1\n", sizePtr, typeName)
	fmt.Fprintf(out, "  %s = ptrtoint ptr %s to i64\n", size, sizePtr)
	declareRuntimePrototype(ctx.mctx, "osty_rt_stage0_alloc", scalarOpaquePtr, []callArg{{ty: "i64"}})
	fmt.Fprintf(out, "  %s = call ptr @osty_rt_stage0_alloc(i64 %s)\n", obj, size)
	tagSlot := ctx.mctx.freshTempName("enum.tag.slot")
	fmt.Fprintf(out, "  %s = getelementptr inbounds %%%s, ptr %s, i32 0, i32 0\n", tagSlot, typeName, obj)
	fmt.Fprintf(out, "  store i64 %d, ptr %s\n", agg.VariantIdx, tagSlot)
	for i, field := range agg.Fields {
		expr, ty, ok := resolveOperandWithLoad(ctx, out, field)
		if !ok || ty != payloadTypes[i] {
			return "", scalarUnknown, false
		}
		slot := ctx.mctx.freshTempName("enum.payload.slot")
		fmt.Fprintf(out, "  %s = getelementptr inbounds %%%s, ptr %s, i32 0, i32 %d\n", slot, typeName, obj, i+1)
		fmt.Fprintf(out, "  store %s %s, ptr %s\n", ty.llvm(), expr, slot)
	}
	return obj, scalarOpaquePtr, true
}

func optionPayloadScalar(t mir.Type, mctx *moduleCtx) (scalarType, bool) {
	switch ty := t.(type) {
	case *ir.OptionalType:
		if ty == nil {
			return scalarUnknown, false
		}
		st := mctx.scalarFromType(ty.Inner, true)
		return st, st != scalarUnknown
	case *ir.NamedType:
		if ty == nil || !ty.Builtin || (ty.Name != "Option" && ty.Name != "Maybe") || len(ty.Args) == 0 {
			return scalarUnknown, false
		}
		st := mctx.scalarFromType(ty.Args[0], true)
		return st, st != scalarUnknown
	}
	return scalarUnknown, false
}

func resultPayloadScalars(t mir.Type, mctx *moduleCtx) (scalarType, scalarType, bool) {
	named, ok := t.(*ir.NamedType)
	if !ok || named == nil || !named.Builtin || named.Name != "Result" || len(named.Args) < 2 {
		return scalarUnknown, scalarUnknown, false
	}
	okTy := mctx.scalarFromType(named.Args[0], true)
	errTy := mctx.scalarFromType(named.Args[1], true)
	if okTy == scalarUnknown && (isUnitType(named.Args[0]) || isEmptyTupleType(named.Args[0])) {
		okTy = scalarInt
	}
	if errTy == scalarUnknown && (isUnitType(named.Args[1]) || isEmptyTupleType(named.Args[1])) {
		errTy = scalarInt
	}
	if okTy == scalarUnknown || errTy == scalarUnknown {
		return scalarUnknown, scalarUnknown, false
	}
	return okTy, errTy, true
}

func resultVariantPayloadScalar(t mir.Type, variantIdx int, mctx *moduleCtx) (scalarType, scalarType, scalarType, int, bool) {
	okTy, errTy, ok := resultPayloadScalars(t, mctx)
	if !ok {
		return scalarUnknown, scalarUnknown, scalarUnknown, 0, false
	}
	switch variantIdx {
	case 0:
		return okTy, okTy, errTy, 1, true
	case 1:
		return errTy, okTy, errTy, 2, true
	default:
		return scalarUnknown, scalarUnknown, scalarUnknown, 0, false
	}
}

func emitWhileOptionAggregateRValue(ctx *whileLoopEmitCtx, out *strings.Builder, agg *mir.AggregateRV) (string, scalarType, bool) {
	if ctx == nil || ctx.mctx == nil || agg == nil {
		return "", scalarUnknown, false
	}
	payloadTy, ok := optionPayloadScalar(agg.T, ctx.mctx)
	if !ok {
		return "", scalarUnknown, false
	}
	typeName, ok := ctx.mctx.emitOptionBoxDef(payloadTy)
	if !ok {
		return "", scalarUnknown, false
	}
	sizePtr := freshReg(ctx)
	size := freshReg(ctx)
	obj := freshReg(ctx)
	fmt.Fprintf(out, "  %s = getelementptr %%%s, ptr null, i32 1\n", sizePtr, typeName)
	fmt.Fprintf(out, "  %s = ptrtoint ptr %s to i64\n", size, sizePtr)
	declareRuntimePrototype(ctx.mctx, "osty_rt_stage0_alloc", scalarOpaquePtr, []callArg{{ty: "i64"}})
	fmt.Fprintf(out, "  %s = call ptr @osty_rt_stage0_alloc(i64 %s)\n", obj, size)
	tagSlot := ctx.mctx.freshTempName("option.tag.slot")
	fmt.Fprintf(out, "  %s = getelementptr inbounds %%%s, ptr %s, i32 0, i32 0\n", tagSlot, typeName, obj)
	fmt.Fprintf(out, "  store i64 %d, ptr %s\n", agg.VariantIdx, tagSlot)
	if len(agg.Fields) == 0 {
		return obj, scalarOpaquePtr, true
	}
	if len(agg.Fields) != 1 {
		return "", scalarUnknown, false
	}
	expr, ty, ok := resolveOperandWithLoad(ctx, out, agg.Fields[0])
	if !ok || ty != payloadTy {
		return "", scalarUnknown, false
	}
	payloadSlot := ctx.mctx.freshTempName("option.payload.slot")
	fmt.Fprintf(out, "  %s = getelementptr inbounds %%%s, ptr %s, i32 0, i32 1\n", payloadSlot, typeName, obj)
	fmt.Fprintf(out, "  store %s %s, ptr %s\n", payloadTy.llvm(), expr, payloadSlot)
	return obj, scalarOpaquePtr, true
}

func emitWhileResultAggregateRValue(ctx *whileLoopEmitCtx, out *strings.Builder, agg *mir.AggregateRV) (string, scalarType, bool) {
	if ctx == nil || ctx.mctx == nil || agg == nil {
		return "", scalarUnknown, false
	}
	payloadTy, okTy, errTy, payloadIndex, ok := resultVariantPayloadScalar(agg.T, agg.VariantIdx, ctx.mctx)
	if !ok {
		return "", scalarUnknown, false
	}
	// Unit Ok variant: no payload fields.
	if len(agg.Fields) == 0 && agg.VariantIdx == 0 {
		okTy2, _, ok2 := resultPayloadScalars(agg.T, ctx.mctx)
		if !ok2 {
			return "", scalarUnknown, false
		}
		// Check that Ok type is unit.
		if named, ok := agg.T.(*ir.NamedType); ok && len(named.Args) >= 1 && !isUnitType(named.Args[0]) {
			return "", scalarUnknown, false
		}
		typeName, ok := ctx.mctx.emitResultBoxDef(okTy2, errTy)
		if !ok {
			return "", scalarUnknown, false
		}
		sizePtr := freshReg(ctx)
		size := freshReg(ctx)
		obj := freshReg(ctx)
		fmt.Fprintf(out, "  %s = getelementptr %%%s, ptr null, i32 1\n", sizePtr, typeName)
		fmt.Fprintf(out, "  %s = ptrtoint ptr %s to i64\n", size, sizePtr)
		declareRuntimePrototype(ctx.mctx, "osty_rt_stage0_alloc", scalarOpaquePtr, []callArg{{ty: "i64"}})
		fmt.Fprintf(out, "  %s = call ptr @osty_rt_stage0_alloc(i64 %s)\n", obj, size)
		tagSlot := ctx.mctx.freshTempName("result.tag.slot")
		fmt.Fprintf(out, "  %s = getelementptr inbounds %%%s, ptr %s, i32 0, i32 0\n", tagSlot, typeName, obj)
		fmt.Fprintf(out, "  store i64 0, ptr %s\n", tagSlot)
		return obj, scalarOpaquePtr, true
	}
	if len(agg.Fields) != 1 {
		return "", scalarUnknown, false
	}
	isUnitPayload := len(agg.Fields) == 1 && isUnitOperand(agg.Fields[0])
	if payloadTy == scalarUnknown && isUnitPayload {
		payloadTy = scalarInt
	}
	if payloadTy == scalarUnknown {
		return "", scalarUnknown, false
	}
	typeName, ok := ctx.mctx.emitResultBoxDef(okTy, errTy)
	if !ok {
		return "", scalarUnknown, false
	}
	sizePtr := freshReg(ctx)
	size := freshReg(ctx)
	obj := freshReg(ctx)
	fmt.Fprintf(out, "  %s = getelementptr %%%s, ptr null, i32 1\n", sizePtr, typeName)
	fmt.Fprintf(out, "  %s = ptrtoint ptr %s to i64\n", size, sizePtr)
	declareRuntimePrototype(ctx.mctx, "osty_rt_stage0_alloc", scalarOpaquePtr, []callArg{{ty: "i64"}})
	fmt.Fprintf(out, "  %s = call ptr @osty_rt_stage0_alloc(i64 %s)\n", obj, size)
	tagSlot := ctx.mctx.freshTempName("result.tag.slot")
	fmt.Fprintf(out, "  %s = getelementptr inbounds %%%s, ptr %s, i32 0, i32 0\n", tagSlot, typeName, obj)
	fmt.Fprintf(out, "  store i64 %d, ptr %s\n", agg.VariantIdx, tagSlot)
	payloadSlot := ctx.mctx.freshTempName("result.payload.slot")
	fmt.Fprintf(out, "  %s = getelementptr inbounds %%%s, ptr %s, i32 0, i32 %d\n", payloadSlot, typeName, obj, payloadIndex)
	if isUnitPayload {
		zero, ok := payloadTy.zeroValue()
		if !ok {
			return "", scalarUnknown, false
		}
		fmt.Fprintf(out, "  store %s %s, ptr %s\n", payloadTy.llvm(), zero, payloadSlot)
		return obj, scalarOpaquePtr, true
	}
	expr, ty, ok := resolveOperandWithLoad(ctx, out, agg.Fields[0])
	if !ok || ty != payloadTy {
		return "", scalarUnknown, false
	}
	fmt.Fprintf(out, "  store %s %s, ptr %s\n", payloadTy.llvm(), expr, payloadSlot)
	return obj, scalarOpaquePtr, true
}

func emitWhileNullaryRValue(ctx *whileLoopEmitCtx, out *strings.Builder, rv *mir.NullaryRV, destType scalarType) (string, scalarType, bool) {
	if rv == nil || rv.Kind != mir.NullaryNone || destType != scalarOpaquePtr {
		return "", scalarUnknown, false
	}
	payloadTy, ok := optionPayloadScalar(rv.T, ctx.mctx)
	if !ok {
		return "", scalarUnknown, false
	}
	typeName, ok := ctx.mctx.emitOptionBoxDef(payloadTy)
	if !ok {
		return "", scalarUnknown, false
	}
	sizePtr := freshReg(ctx)
	size := freshReg(ctx)
	obj := freshReg(ctx)
	fmt.Fprintf(out, "  %s = getelementptr %%%s, ptr null, i32 1\n", sizePtr, typeName)
	fmt.Fprintf(out, "  %s = ptrtoint ptr %s to i64\n", size, sizePtr)
	declareRuntimePrototype(ctx.mctx, "osty_rt_stage0_alloc", scalarOpaquePtr, []callArg{{ty: "i64"}})
	fmt.Fprintf(out, "  %s = call ptr @osty_rt_stage0_alloc(i64 %s)\n", obj, size)
	tagSlot := ctx.mctx.freshTempName("option.tag.slot")
	fmt.Fprintf(out, "  %s = getelementptr inbounds %%%s, ptr %s, i32 0, i32 0\n", tagSlot, typeName, obj)
	fmt.Fprintf(out, "  store i64 1, ptr %s\n", tagSlot)
	return obj, scalarOpaquePtr, true
}

func emitWhileStructAggregateRValue(ctx *whileLoopEmitCtx, out *strings.Builder, agg *mir.AggregateRV) (string, scalarType, bool) {
	if ctx == nil || ctx.mctx == nil || agg == nil {
		return "", scalarUnknown, false
	}
	named, ok := agg.T.(*ir.NamedType)
	if !ok || named == nil || named.Name == "" {
		return "", scalarUnknown, false
	}
	fieldTypes, ok := ctx.mctx.lookupStructFields(named.Name)
	if !ok || len(fieldTypes) != len(agg.Fields) {
		return "", scalarUnknown, false
	}
	ctx.mctx.emitStructDef(named.Name, fieldTypes)
	sizePtr := freshReg(ctx)
	size := freshReg(ctx)
	obj := freshReg(ctx)
	fmt.Fprintf(out, "  %s = getelementptr %%%s, ptr null, i32 1\n", sizePtr, named.Name)
	fmt.Fprintf(out, "  %s = ptrtoint ptr %s to i64\n", size, sizePtr)
	declareRuntimePrototype(ctx.mctx, "osty_rt_stage0_alloc", scalarOpaquePtr, []callArg{{ty: "i64"}})
	fmt.Fprintf(out, "  %s = call ptr @osty_rt_stage0_alloc(i64 %s)\n", obj, size)
	for i, field := range agg.Fields {
		expr, ty, ok := resolveOperandWithLoad(ctx, out, field)
		if !ok || ty != fieldTypes[i] {
			return "", scalarUnknown, false
		}
		slot := ctx.mctx.freshTempName("agg.field.slot")
		fmt.Fprintf(out, "  %s = getelementptr inbounds %%%s, ptr %s, i32 0, i32 %d\n", slot, named.Name, obj, i)
		fmt.Fprintf(out, "  store %s %s, ptr %s\n", ty.llvm(), expr, slot)
	}
	return obj, scalarOpaquePtr, true
}

func emitWhileLenRValue(ctx *whileLoopEmitCtx, out *strings.Builder, lenRV *mir.LenRV, destType scalarType) (string, scalarType, bool) {
	if lenRV == nil || destType != scalarInt {
		return "", scalarUnknown, false
	}
	placeTy := placeResultType(ctx.fn, lenRV.Place)
	if placeTy == nil {
		return "", scalarUnknown, false
	}
	expr, ty, ok := resolveOperandWithLoad(ctx, out, &mir.CopyOp{Place: lenRV.Place, T: placeTy})
	if !ok {
		return "", scalarUnknown, false
	}
	if isPrimType(placeTy, ir.PrimBytes) && ty == scalarOpaquePtr {
		declareRuntimePrototype(ctx.mctx, "osty_rt_bytes_len", scalarInt, []callArg{{ty: "ptr"}})
		reg := freshReg(ctx)
		fmt.Fprintf(out, "  %s = call i64 @osty_rt_bytes_len(ptr %s)\n", reg, expr)
		return reg, scalarInt, true
	}
	switch ty {
	case scalarOpaquePtr:
		declareListRuntime(ctx.mctx)
		reg := freshReg(ctx)
		fmt.Fprintf(out, "  %s = call i64 @osty_rt_list_len(ptr %s)\n", reg, expr)
		return reg, scalarInt, true
	case scalarString:
		declareRuntimePrototype(ctx.mctx, "osty_rt_strings_ByteLen", scalarInt, []callArg{{ty: "ptr"}})
		reg := freshReg(ctx)
		fmt.Fprintf(out, "  %s = call i64 @osty_rt_strings_ByteLen(ptr %s)\n", reg, expr)
		return reg, scalarInt, true
	}
	return "", scalarUnknown, false
}

func emitWhileCall(ctx *whileLoopEmitCtx, out *strings.Builder, ci *mir.CallInstr) bool {
	if ci.Dest == nil {
		return emitWhileVoidCall(ctx, out, ci)
	}
	destIRType := placeResultType(ctx.fn, *ci.Dest)
	if destIRType == nil {
		return false
	}
	if tupleTy, ok := destIRType.(*ir.TupleType); ok && tupleTy != nil {
		ref, ok := ci.Callee.(*mir.FnRef)
		if !ok || ref.Symbol == "" {
			return false
		}
		if fnTy, ok := ref.Type.(*ir.FnType); ok && fnTy != nil {
			if !sameTypeString(fnTy.Return, destIRType) {
				return false
			}
			if len(fnTy.Params) != len(ci.Args) {
				return false
			}
		}
		fields := make([]scalarType, len(tupleTy.Elems))
		for i, e := range tupleTy.Elems {
			st := ctx.mctx.scalarFromType(e, true)
			if st == scalarUnknown {
				return false
			}
			fields[i] = st
		}
		typeName := ctx.mctx.internTupleType(fields)
		args := make([]callArg, 0, len(ci.Args))
		for _, op := range ci.Args {
			expr, argTy, ok := resolveOperandWithLoad(ctx, out, op)
			if !ok || argTy == scalarUnknown {
				return false
			}
			args = append(args, callArg{expr: expr, ty: argTy.llvm()})
		}
		declareFunctionPrototypeLLVM(ctx.mctx, ref.Symbol, "%"+typeName, args)
		reg := freshReg(ctx)
		fmt.Fprintf(out, "  %s = call %%%s @%s(", reg, typeName, ref.Symbol)
		for i, a := range args {
			if i > 0 {
				out.WriteString(", ")
			}
			fmt.Fprintf(out, "%s %s", a.ty, a.expr)
		}
		out.WriteString(")\n")
		ctx.aggregates[ci.Dest.Local] = aggregateBinding{typeName: typeName, reg: reg, fields: fields}
		return true
	}
	destType := ctx.mctx.scalarFromType(destIRType, true)
	if destType == scalarUnknown {
		return false
	}
	ref, ok := ci.Callee.(*mir.FnRef)
	if !ok || ref.Symbol == "" {
		return false
	}
	allowOpaqueUserNamed := true
	fnTy, ok := ref.Type.(*ir.FnType)
	if !ok || fnTy == nil {
		if !isErrType(ref.Type) && ctx.mctx.scalarFromType(ref.Type, allowOpaqueUserNamed) != destType {
			return false
		}
		args := make([]callArg, 0, len(ci.Args))
		for _, op := range ci.Args {
			expr, argTy, ok := resolveOperandWithLoad(ctx, out, op)
			if !ok || argTy == scalarUnknown {
				return false
			}
			args = append(args, callArg{expr: expr, ty: argTy.llvm()})
		}
		declareFunctionPrototype(ctx.mctx, ref.Symbol, destType, args)
		return emitWhileCallToPlace(ctx, out, *ci.Dest, destType, ref.Symbol, args)
	}
	if ctx.mctx.scalarFromType(fnTy.Return, allowOpaqueUserNamed) != destType {
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
		paramTy := ctx.mctx.scalarFromType(fnTy.Params[i], allowOpaqueUserNamed)
		if paramTy == scalarUnknown || paramTy != ty {
			return false
		}
		args = append(args, callArg{expr: expr, ty: ty.llvm()})
	}
	declareFunctionPrototype(ctx.mctx, ref.Symbol, destType, args)
	return emitWhileCallToPlace(ctx, out, *ci.Dest, destType, ref.Symbol, args)
}

func emitWhileVoidCall(ctx *whileLoopEmitCtx, out *strings.Builder, ci *mir.CallInstr) bool {
	ref, ok := ci.Callee.(*mir.FnRef)
	if !ok || ref.Symbol == "" {
		return false
	}
	args := make([]callArg, 0, len(ci.Args))
	if fnTy, ok := ref.Type.(*ir.FnType); ok && fnTy != nil {
		if len(fnTy.Params) != len(ci.Args) {
			return false
		}
		for i, op := range ci.Args {
			expr, argTy, ok := resolveOperandWithLoad(ctx, out, op)
			if !ok {
				return false
			}
			paramTy := ctx.mctx.scalarFromType(fnTy.Params[i], true)
			if paramTy == scalarUnknown || paramTy != argTy {
				return false
			}
			args = append(args, callArg{expr: expr, ty: argTy.llvm()})
		}
		if !isUnitType(fnTy.Return) {
			retType := ctx.mctx.scalarFromType(fnTy.Return, true)
			if retType == scalarUnknown {
				return false
			}
			declareFunctionPrototype(ctx.mctx, ref.Symbol, retType, args)
			out.WriteString(renderDiscardValueCallLine(ref.Symbol, retType, args))
			return true
		}
	} else {
		for _, op := range ci.Args {
			expr, argTy, ok := resolveOperandWithLoad(ctx, out, op)
			if !ok || argTy == scalarUnknown {
				return false
			}
			args = append(args, callArg{expr: expr, ty: argTy.llvm()})
		}
		if !isErrType(ref.Type) && !isUnitType(ref.Type) {
			retType := ctx.mctx.scalarFromType(ref.Type, true)
			if retType == scalarUnknown {
				return false
			}
			declareFunctionPrototype(ctx.mctx, ref.Symbol, retType, args)
			out.WriteString(renderDiscardValueCallLine(ref.Symbol, retType, args))
			return true
		}
	}
	declareVoidFunctionPrototype(ctx.mctx, ref.Symbol, args)
	out.WriteString(renderVoidCallLine(ref.Symbol, args))
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
			switch {
			case isPrimType(c.Type(), ir.PrimInt):
				return fmt.Sprintf("%d", c.Value), scalarInt, true
			case isPrimType(c.Type(), ir.PrimByte):
				return fmt.Sprintf("%d", byte(c.Value)), scalarByte, true
			case isErrType(c.Type()):
				return fmt.Sprintf("%d", c.Value), scalarInt, true
			}
			return "", scalarUnknown, false
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
		case *mir.ByteConst:
			return fmt.Sprintf("%d", c.Value), scalarByte, true
		case *mir.CharConst:
			return fmt.Sprintf("%d", c.Value), scalarChar, true
		case *mir.FloatConst:
			return formatFloatConst(c.Value), scalarFloat, true
		}
		return "", scalarUnknown, false
	}
	if cp, ok := op.(*mir.CopyOp); ok {
		if cp.Place.HasProjections() {
			if _, ok := cp.Place.Projections[len(cp.Place.Projections)-1].(*mir.IndexProj); ok {
				return resolveWhileIndexedOperand(ctx, out, cp.Place)
			}
			if expr, ty, ok := resolveWhileTupleProjectedOperand(ctx, out, cp.Place); ok {
				return expr, ty, true
			}
			return resolveWhileProjectedFieldOperand(ctx, out, cp.Place)
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

func resolveWhileTupleProjectedOperand(ctx *whileLoopEmitCtx, out *strings.Builder, place mir.Place) (string, scalarType, bool) {
	if ctx == nil || ctx.mctx == nil || out == nil || len(place.Projections) == 0 {
		return "", scalarUnknown, false
	}
	tp, ok := place.Projections[len(place.Projections)-1].(*mir.TupleProj)
	if !ok {
		return "", scalarUnknown, false
	}
	agg, ok := ctx.aggregates[place.Local]
	if !ok {
		return "", scalarUnknown, false
	}
	if tp.Index < 0 || tp.Index >= len(agg.fields) {
		return "", scalarUnknown, false
	}
	fieldTy := agg.fields[tp.Index]
	reg := freshReg(ctx)
	fmt.Fprintf(out, "  %s = extractvalue %%%s %s, %d\n", reg, agg.typeName, agg.reg, tp.Index)
	return reg, fieldTy, true
}

func resolveWhileIndexedOperand(ctx *whileLoopEmitCtx, out *strings.Builder, place mir.Place) (string, scalarType, bool) {
	if ctx == nil || ctx.mctx == nil || len(place.Projections) == 0 {
		return "", scalarUnknown, false
	}
	idxProj, ok := place.Projections[len(place.Projections)-1].(*mir.IndexProj)
	if !ok {
		return "", scalarUnknown, false
	}
	elemTy := ctx.mctx.scalarFromType(idxProj.ElemType, true)
	if elemTy == scalarUnknown {
		return "", scalarUnknown, false
	}
	listPlace := mir.Place{
		Local:       place.Local,
		Projections: append([]mir.Projection(nil), place.Projections[:len(place.Projections)-1]...),
	}
	listTy := placeResultType(ctx.fn, listPlace)
	if listTy == nil {
		return "", scalarUnknown, false
	}
	listExpr, listScalarTy, ok := resolveOperandWithLoad(ctx, out, &mir.CopyOp{Place: listPlace, T: listTy})
	if !ok {
		return "", scalarUnknown, false
	}
	// String→Char subscript: call runtime to decode i-th code point.
	if listScalarTy == scalarString && elemTy == scalarChar {
		indexExpr, indexTy, ok := resolveOperandWithLoad(ctx, out, idxProj.Index)
		if !ok || indexTy != scalarInt {
			return "", scalarUnknown, false
		}
		declareRuntimePrototype(ctx.mctx, "osty_rt_stage0_string_char_at", scalarChar, []callArg{{ty: "ptr"}, {ty: "i64"}})
		value := freshReg(ctx)
		fmt.Fprintf(out, "  %s = call i64 @osty_rt_stage0_string_char_at(ptr %s, i64 %s)\n", value, listExpr, indexExpr)
		return value, scalarChar, true
	}
	if listScalarTy != scalarOpaquePtr {
		return "", scalarUnknown, false
	}
	indexExpr, indexTy, ok := resolveOperandWithLoad(ctx, out, idxProj.Index)
	if !ok || indexTy != scalarInt {
		return "", scalarUnknown, false
	}
	if isPrimType(listTy, ir.PrimBytes) && elemTy == scalarByte {
		declareRuntimePrototype(ctx.mctx, "osty_rt_bytes_get", scalarByte, []callArg{{ty: "ptr"}, {ty: "i64"}})
		value := freshReg(ctx)
		fmt.Fprintf(out, "  %s = call i8 @osty_rt_bytes_get(ptr %s, i64 %s)\n", value, listExpr, indexExpr)
		return value, scalarByte, true
	}
	if elemSize := listBytesElementSize(elemTy); elemSize > 0 {
		declareListGetBytesRuntime(ctx.mctx)
		slot := freshReg(ctx)
		value := freshReg(ctx)
		fmt.Fprintf(out, "  %s = alloca %s\n", slot, elemTy.llvm())
		fmt.Fprintf(out, "  call void @osty_rt_list_get_bytes_v1(ptr %s, i64 %s, ptr %s, i64 %d)\n", listExpr, indexExpr, slot, elemSize)
		fmt.Fprintf(out, "  %s = load %s, ptr %s\n", value, elemTy.llvm(), slot)
		return value, elemTy, true
	}
	symbol := listGetSymbolFor(elemTy)
	if symbol == "" {
		return "", scalarUnknown, false
	}
	declareListGetRuntimeFor(ctx.mctx, elemTy)
	value := freshReg(ctx)
	fmt.Fprintf(out, "  %s = call %s @%s(ptr %s, i64 %s)\n", value, elemTy.llvm(), symbol, listExpr, indexExpr)
	return value, elemTy, true
}

func resolveWhileProjectedFieldOperand(ctx *whileLoopEmitCtx, out *strings.Builder, place mir.Place) (string, scalarType, bool) {
	slot, fieldTy, ok := resolveWhileProjectedFieldSlot(ctx, out, place, "field.slot")
	if !ok {
		return "", scalarUnknown, false
	}
	value := freshReg(ctx)
	fmt.Fprintf(out, "  %s = load %s, ptr %s\n", value, fieldTy.llvm(), slot)
	return value, fieldTy, true
}

func resolveWhileProjectedFieldSlot(ctx *whileLoopEmitCtx, out *strings.Builder, place mir.Place, label string) (string, scalarType, bool) {
	if ctx == nil || ctx.mctx == nil || len(place.Projections) == 0 {
		return "", scalarUnknown, false
	}
	if _, ok := place.Projections[0].(*mir.VariantProj); ok {
		if slot, ty, ok := resolveWhileProjectedOptionPayloadSlot(ctx, out, place, label); ok {
			return slot, ty, true
		}
		if slot, ty, ok := resolveWhileProjectedResultPayloadSlot(ctx, out, place, label); ok {
			return slot, ty, true
		}
		return resolveWhileProjectedEnumPayloadSlot(ctx, out, place, label)
	}
	currentStruct, currentPtr, start, ok := resolveWhileProjectedStructBase(ctx, out, place)
	if !ok {
		return "", scalarUnknown, false
	}
	for i := start; i < len(place.Projections); i++ {
		proj := place.Projections[i]
		fp, ok := proj.(*mir.FieldProj)
		if !ok {
			return "", scalarUnknown, false
		}
		fieldTypes, ok := ctx.mctx.lookupStructFields(currentStruct)
		if !ok || fp.Index < 0 || fp.Index >= len(fieldTypes) {
			return "", scalarUnknown, false
		}
		fieldTy := fieldTypes[fp.Index]
		ctx.mctx.emitStructDef(currentStruct, fieldTypes)
		slot := ctx.mctx.freshTempName(label)
		fmt.Fprintf(out, "  %s = getelementptr inbounds %%%s, ptr %s, i32 0, i32 %d\n", slot, currentStruct, currentPtr, fp.Index)
		if i == len(place.Projections)-1 {
			return slot, fieldTy, true
		}
		if fieldTy != scalarOpaquePtr {
			return "", scalarUnknown, false
		}
		if nextIdx, ok := place.Projections[i+1].(*mir.IndexProj); ok {
			elemStruct, ok := listProjectionElementStructName(fp.Type, nextIdx.ElemType)
			if !ok {
				return "", scalarUnknown, false
			}
			if _, ok := ctx.mctx.lookupStructFields(elemStruct); !ok {
				return "", scalarUnknown, false
			}
			listPtr := ctx.mctx.freshTempName("field.list.base")
			fmt.Fprintf(out, "  %s = load ptr, ptr %s\n", listPtr, slot)
			indexExpr, indexTy, ok := resolveOperandWithLoad(ctx, out, nextIdx.Index)
			if !ok || indexTy != scalarInt {
				return "", scalarUnknown, false
			}
			elemTy := ctx.mctx.scalarFromType(nextIdx.ElemType, true)
			if elemTy != scalarOpaquePtr {
				return "", scalarUnknown, false
			}
			symbol := listGetSymbolFor(elemTy)
			if symbol == "" {
				return "", scalarUnknown, false
			}
			declareListGetRuntimeFor(ctx.mctx, elemTy)
			elemPtr := freshReg(ctx)
			fmt.Fprintf(out, "  %s = call ptr @%s(ptr %s, i64 %s)\n", elemPtr, symbol, listPtr, indexExpr)
			currentStruct = elemStruct
			currentPtr = elemPtr
			i++
			continue
		}
		nextNamed, ok := fp.Type.(*ir.NamedType)
		if !ok || nextNamed == nil || nextNamed.Name == "" {
			return "", scalarUnknown, false
		}
		nextPtr := ctx.mctx.freshTempName("field.base")
		fmt.Fprintf(out, "  %s = load ptr, ptr %s\n", nextPtr, slot)
		currentStruct = nextNamed.Name
		currentPtr = nextPtr
	}
	return "", scalarUnknown, false
}

func resolveWhileProjectedOptionPayloadSlot(ctx *whileLoopEmitCtx, out *strings.Builder, place mir.Place, label string) (string, scalarType, bool) {
	if ctx == nil || ctx.mctx == nil || len(place.Projections) == 0 {
		return "", scalarUnknown, false
	}
	baseLocal := lookupLocal(ctx.fn, place.Local)
	if baseLocal == nil {
		return "", scalarUnknown, false
	}
	vp, ok := place.Projections[0].(*mir.VariantProj)
	if !ok {
		return "", scalarUnknown, false
	}
	payloadTy, ok := optionPayloadScalar(baseLocal.Type, ctx.mctx)
	if !ok {
		return "", scalarUnknown, false
	}
	typeName, ok := ctx.mctx.emitOptionBoxDef(payloadTy)
	if !ok {
		return "", scalarUnknown, false
	}
	baseExpr, baseTy, ok := resolveOperandWithLoad(ctx, out, &mir.CopyOp{Place: mir.Place{Local: place.Local}, T: baseLocal.Type})
	if !ok || baseTy != scalarOpaquePtr {
		return "", scalarUnknown, false
	}
	slot := ctx.mctx.freshTempName(label)
	fmt.Fprintf(out, "  %s = getelementptr inbounds %%%s, ptr %s, i32 0, i32 1\n", slot, typeName, baseExpr)
	if len(place.Projections) == 1 {
		return slot, payloadTy, true
	}
	if payloadTy != scalarOpaquePtr {
		return "", scalarUnknown, false
	}
	nextNamed, ok := vp.Type.(*ir.NamedType)
	if !ok || nextNamed == nil || nextNamed.Name == "" {
		return "", scalarUnknown, false
	}
	currentPtr := ctx.mctx.freshTempName("option.payload.base")
	fmt.Fprintf(out, "  %s = load ptr, ptr %s\n", currentPtr, slot)
	currentStruct := nextNamed.Name
	for i := 1; i < len(place.Projections); i++ {
		fp, ok := place.Projections[i].(*mir.FieldProj)
		if !ok {
			return "", scalarUnknown, false
		}
		fieldTypes, ok := ctx.mctx.lookupStructFields(currentStruct)
		if !ok || fp.Index < 0 || fp.Index >= len(fieldTypes) {
			return "", scalarUnknown, false
		}
		fieldTy := fieldTypes[fp.Index]
		ctx.mctx.emitStructDef(currentStruct, fieldTypes)
		slot := ctx.mctx.freshTempName(label)
		fmt.Fprintf(out, "  %s = getelementptr inbounds %%%s, ptr %s, i32 0, i32 %d\n", slot, currentStruct, currentPtr, fp.Index)
		if i == len(place.Projections)-1 {
			return slot, fieldTy, true
		}
		if fieldTy != scalarOpaquePtr {
			return "", scalarUnknown, false
		}
		nextNamed, ok := fp.Type.(*ir.NamedType)
		if !ok || nextNamed == nil || nextNamed.Name == "" {
			return "", scalarUnknown, false
		}
		nextPtr := ctx.mctx.freshTempName("field.base")
		fmt.Fprintf(out, "  %s = load ptr, ptr %s\n", nextPtr, slot)
		currentStruct = nextNamed.Name
		currentPtr = nextPtr
	}
	return "", scalarUnknown, false
}

func resolveWhileProjectedResultPayloadSlot(ctx *whileLoopEmitCtx, out *strings.Builder, place mir.Place, label string) (string, scalarType, bool) {
	if ctx == nil || ctx.mctx == nil || len(place.Projections) == 0 {
		return "", scalarUnknown, false
	}
	baseLocal := lookupLocal(ctx.fn, place.Local)
	if baseLocal == nil {
		return "", scalarUnknown, false
	}
	vp, ok := place.Projections[0].(*mir.VariantProj)
	if !ok {
		return "", scalarUnknown, false
	}
	payloadTy, okTy, errTy, payloadIndex, ok := resultVariantPayloadScalar(baseLocal.Type, vp.Variant, ctx.mctx)
	if !ok {
		return "", scalarUnknown, false
	}
	typeName, ok := ctx.mctx.emitResultBoxDef(okTy, errTy)
	if !ok {
		return "", scalarUnknown, false
	}
	baseExpr, baseTy, ok := resolveOperandWithLoad(ctx, out, &mir.CopyOp{Place: mir.Place{Local: place.Local}, T: baseLocal.Type})
	if !ok || baseTy != scalarOpaquePtr {
		return "", scalarUnknown, false
	}
	slot := ctx.mctx.freshTempName(label)
	fmt.Fprintf(out, "  %s = getelementptr inbounds %%%s, ptr %s, i32 0, i32 %d\n", slot, typeName, baseExpr, payloadIndex)
	if len(place.Projections) == 1 {
		return slot, payloadTy, true
	}
	if payloadTy != scalarOpaquePtr {
		return "", scalarUnknown, false
	}
	nextNamed, ok := vp.Type.(*ir.NamedType)
	if !ok || nextNamed == nil || nextNamed.Name == "" {
		return "", scalarUnknown, false
	}
	currentPtr := ctx.mctx.freshTempName("result.payload.base")
	fmt.Fprintf(out, "  %s = load ptr, ptr %s\n", currentPtr, slot)
	currentStruct := nextNamed.Name
	for i := 1; i < len(place.Projections); i++ {
		fp, ok := place.Projections[i].(*mir.FieldProj)
		if !ok {
			return "", scalarUnknown, false
		}
		fieldTypes, ok := ctx.mctx.lookupStructFields(currentStruct)
		if !ok || fp.Index < 0 || fp.Index >= len(fieldTypes) {
			return "", scalarUnknown, false
		}
		fieldTy := fieldTypes[fp.Index]
		ctx.mctx.emitStructDef(currentStruct, fieldTypes)
		slot := ctx.mctx.freshTempName(label)
		fmt.Fprintf(out, "  %s = getelementptr inbounds %%%s, ptr %s, i32 0, i32 %d\n", slot, currentStruct, currentPtr, fp.Index)
		if i == len(place.Projections)-1 {
			return slot, fieldTy, true
		}
		if fieldTy != scalarOpaquePtr {
			return "", scalarUnknown, false
		}
		nextNamed, ok := fp.Type.(*ir.NamedType)
		if !ok || nextNamed == nil || nextNamed.Name == "" {
			return "", scalarUnknown, false
		}
		nextPtr := ctx.mctx.freshTempName("field.base")
		fmt.Fprintf(out, "  %s = load ptr, ptr %s\n", nextPtr, slot)
		currentStruct = nextNamed.Name
		currentPtr = nextPtr
	}
	return "", scalarUnknown, false
}

// resolveWhileProjectedEnumPayloadSlot handles VariantProj on user-defined
// enum types (not Option/Result). The enum box layout is {i64 disc, payload...}
// so the variant payload field(s) start at index 1.
func resolveWhileProjectedEnumPayloadSlot(ctx *whileLoopEmitCtx, out *strings.Builder, place mir.Place, label string) (string, scalarType, bool) {
	if ctx == nil || ctx.mctx == nil || len(place.Projections) == 0 {
		return "", scalarUnknown, false
	}
	baseLocal := lookupLocal(ctx.fn, place.Local)
	if baseLocal == nil {
		return "", scalarUnknown, false
	}
	named, ok := baseLocal.Type.(*ir.NamedType)
	if !ok || named == nil || named.Name == "" {
		return "", scalarUnknown, false
	}
	vp, ok := place.Projections[0].(*mir.VariantProj)
	if !ok {
		return "", scalarUnknown, false
	}
	// Look up the enum layout to find the variant's payload types.
	if ctx.mctx.module == nil || ctx.mctx.module.Layouts == nil {
		return "", scalarUnknown, false
	}
	layout := ctx.mctx.module.Layouts.Enums[named.Name]
	if layout == nil || enumLayoutIsPayloadless(layout) {
		return "", scalarUnknown, false
	}
	if vp.Variant < 0 || vp.Variant >= len(layout.Variants) {
		return "", scalarUnknown, false
	}
	variant := layout.Variants[vp.Variant]
	if len(variant.Payload) == 0 {
		return "", scalarUnknown, false
	}
	// Map payload fields to scalar types.
	payloadScalars := make([]scalarType, len(variant.Payload))
	for i, f := range variant.Payload {
		st := ctx.mctx.scalarFromType(f.Type, true)
		if st == scalarUnknown {
			return "", scalarUnknown, false
		}
		payloadScalars[i] = st
	}
	payloadIndex := vp.Variant
	typeName, ok := ctx.mctx.emitEnumBoxDef(named.Name, payloadIndex, payloadScalars)
	if !ok {
		return "", scalarUnknown, false
	}
	baseExpr, baseTy, ok := resolveOperandWithLoad(ctx, out, &mir.CopyOp{Place: mir.Place{Local: place.Local}, T: baseLocal.Type})
	if !ok || baseTy != scalarOpaquePtr {
		return "", scalarUnknown, false
	}
	if len(place.Projections) == 1 {
		// Single-variant projection: if the variant has exactly one payload
		// field, return its slot directly. Otherwise fall through to multi-field.
		if len(payloadScalars) == 1 {
			slot := ctx.mctx.freshTempName(label)
			fmt.Fprintf(out, "  %s = getelementptr inbounds %%%s, ptr %s, i32 0, i32 1\n", slot, typeName, baseExpr)
			return slot, payloadScalars[0], true
		}
		return "", scalarUnknown, false
	}
	return "", scalarUnknown, false
}

func resolveWhileProjectedStructBase(ctx *whileLoopEmitCtx, out *strings.Builder, place mir.Place) (string, string, int, bool) {
	baseLocal := lookupLocal(ctx.fn, place.Local)
	if baseLocal == nil {
		return "", "", 0, false
	}
	if firstIdx, ok := place.Projections[0].(*mir.IndexProj); ok {
		elemNamed, ok := firstIdx.ElemType.(*ir.NamedType)
		if !ok || elemNamed == nil || elemNamed.Name == "" {
			return "", "", 0, false
		}
		if _, ok := ctx.mctx.lookupStructFields(elemNamed.Name); !ok {
			return "", "", 0, false
		}
		listExpr, listTy, ok := resolveOperandWithLoad(ctx, out, &mir.CopyOp{Place: mir.Place{Local: place.Local}, T: baseLocal.Type})
		if !ok || listTy != scalarOpaquePtr {
			return "", "", 0, false
		}
		indexExpr, indexTy, ok := resolveOperandWithLoad(ctx, out, firstIdx.Index)
		if !ok || indexTy != scalarInt {
			return "", "", 0, false
		}
		elemTy := ctx.mctx.scalarFromType(firstIdx.ElemType, true)
		if elemTy != scalarOpaquePtr {
			return "", "", 0, false
		}
		symbol := listGetSymbolFor(elemTy)
		if symbol == "" {
			return "", "", 0, false
		}
		declareListGetRuntimeFor(ctx.mctx, elemTy)
		currentPtr := freshReg(ctx)
		fmt.Fprintf(out, "  %s = call ptr @%s(ptr %s, i64 %s)\n", currentPtr, symbol, listExpr, indexExpr)
		return elemNamed.Name, currentPtr, 1, true
	}
	named, ok := baseLocal.Type.(*ir.NamedType)
	if !ok || named == nil || named.Name == "" {
		return "", "", 0, false
	}
	baseExpr, baseTy, ok := resolveOperandWithLoad(ctx, out, &mir.CopyOp{Place: mir.Place{Local: place.Local}, T: baseLocal.Type})
	if !ok || baseTy != scalarOpaquePtr {
		return "", "", 0, false
	}
	return named.Name, baseExpr, 0, true
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
	pat.retType = mctx.scalarFromType(fn.ReturnType, true)
	if pat.retType == scalarUnknown {
		return pat, false
	}
	if len(fn.Params) > 8 {
		return pat, false
	}
	if len(fn.Blocks) != 5 {
		return pat, false
	}

	// Param SSA registers.
	pat.paramIDs = fn.Params
	pat.paramTypes = make([]scalarType, len(fn.Params))
	pat.paramNames = make([]string, len(fn.Params))
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
		pat.paramNames[i] = sanitizeLLVMName(loc.Name, paramFallbackName(i))
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
		ty := mctx.scalarFromType(l.Type, true)
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
		fn:         fn,
		bindings:   bindings,
		stack:      stack,
		mctx:       mctx,
		nextSSA:    &nextSSA,
		aggregates: map[mir.LocalID]aggregateBinding{},
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
		pat.paramNames[i] = sanitizeLLVMName(loc.Name, paramFallbackName(i))
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
		if llvmOp != "" && resultType != destType && destType == scalarFloat {
			llvmOp2, resultType2, operandType2 := classifyBinaryForType(src.Op, scalarFloat)
			if llvmOp2 != "" && resultType2 == destType {
				llvmOp, resultType, operandType = llvmOp2, resultType2, operandType2
			}
		}
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
		declareFunctionPrototype(ctx.mctx, ref.Symbol, destType, argExprs)
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
		declareFunctionPrototype(ctx.mctx, ref.Symbol, destType, argExprs)
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
		pat.paramNames[i] = sanitizeLLVMName(loc.Name, paramFallbackName(i))
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
	if llvmOp != "" && resultType != pat.resultType && pat.resultType == scalarFloat {
		llvmOp2, resultType2, operandType2 := classifyBinaryForType(bin.Op, scalarFloat)
		if llvmOp2 != "" && resultType2 == pat.resultType {
			llvmOp, resultType, operandType = llvmOp2, resultType2, operandType2
		}
	}
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
	// Use declareRuntimePrototype for list_len so its per-symbol
	// dedup key is set and other callers don't emit duplicates.
	declareRuntimePrototype(mctx, "osty_rt_list_len", scalarInt, []callArg{{ty: "ptr"}})
	// Set per-symbol dedup keys for push functions so
	// declareListPushRuntimeFor doesn't emit duplicates.
	mctx.emittedStructs["__stage0.fn_decl.osty_rt_list_push_i64"] = true
	mctx.emittedStructs["__stage0.fn_decl.osty_rt_list_push_i1"] = true
	mctx.emittedStructs["__stage0.fn_decl.osty_rt_list_push_string"] = true
	mctx.emittedStructs["__stage0.fn_decl.osty_rt_list_push_ptr"] = true
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
	fieldIRTypes, ok := aggregateReturnFieldIRTypes(fn.ReturnType, mctx)
	if !ok || len(fieldIRTypes) != len(fieldTypes) {
		return pat, false
	}

	if len(fn.Params) > 8 {
		return pat, false
	}
	pat.paramIDs = fn.Params
	pat.paramTypes = make([]scalarType, len(fn.Params))
	pat.paramNames = make([]string, len(fn.Params))
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
		pat.paramNames[i] = sanitizeLLVMName(loc.Name, paramFallbackName(i))
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
	fnConstBindings := map[mir.LocalID]*mir.FnConst{}
	emptyOnbInstrBindings := map[mir.LocalID]bool{}
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
				prelude, expr, ty, ok := classifyAggregateFieldWithAggregateBindings(fn, f, i, fieldIRTypes[i], bindings, fnConstBindings, emptyOnbInstrBindings, mctx)
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
		if ai, ok := instr.(*mir.AssignInstr); ok {
			if fc, ok := assignInstrFnConst(ai); ok {
				fnConstBindings[ai.Dest.Local] = fc
				continue
			}
		}
		if ci, ok := instr.(*mir.CallInstr); ok {
			if localID, ok := callInstrEmptyOnbInstrDest(ci); ok {
				emptyOnbInstrBindings[localID] = true
				continue
			}
			if pending, destID, destType, ok := classifyErrTypedEnumPayloadCall(fn, ci, bindings, mctx); ok {
				if existing, found := bindings[destID]; found && existing.defined {
					return pat, false
				}
				reg := fmt.Sprintf("%%%d", nextSSA)
				nextSSA++
				pending.destLocal = destID
				pending.resultType = destType
				pending.binDestReg = reg
				bindings[destID] = localBinding{expr: reg, ty: destType, defined: true}
				pat.pending = append(pat.pending, pending)
				continue
			}
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

func assignInstrFnConst(ai *mir.AssignInstr) (*mir.FnConst, bool) {
	if ai == nil || ai.Dest.HasProjections() {
		return nil, false
	}
	use, ok := ai.Src.(*mir.UseRV)
	if !ok {
		return nil, false
	}
	con, ok := use.Op.(*mir.ConstOp)
	if !ok {
		return nil, false
	}
	fc, ok := con.Const.(*mir.FnConst)
	return fc, ok && fc != nil
}

func callInstrEmptyOnbInstrDest(ci *mir.CallInstr) (mir.LocalID, bool) {
	if ci == nil || ci.Dest == nil || ci.Dest.HasProjections() || len(ci.Args) != 0 {
		return 0, false
	}
	ref, ok := ci.Callee.(*mir.FnRef)
	if !ok || ref.Symbol != "emptyOnbInstr" {
		return 0, false
	}
	return ci.Dest.Local, true
}

func classifyErrTypedEnumPayloadCall(fn *mir.Function, ci *mir.CallInstr, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (pendingInstr, mir.LocalID, scalarType, bool) {
	if ci == nil || ci.Dest == nil || ci.Dest.HasProjections() || len(ci.Args) != 2 {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	destLocal := lookupLocal(fn, ci.Dest.Local)
	if destLocal == nil || !isErrType(destLocal.Type) {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	ref, ok := ci.Callee.(*mir.FnRef)
	if !ok || ref.Symbol == "" || !isErrType(ref.Type) {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	if !operandIsFnConst(ci.Args[0]) {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	prelude, expr, ty, ok := resolveOperandWithPrelude(fn, ci.Args[1], bindings, mctx)
	if !ok || ty == scalarUnknown {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	args := []callArg{{expr: expr, ty: ty.llvm()}}
	declareFunctionPrototype(mctx, ref.Symbol, scalarOpaquePtr, args)
	return pendingInstr{
		kind:       instrCall,
		prelude:    prelude,
		callSymbol: ref.Symbol,
		callArgs:   args,
	}, ci.Dest.Local, scalarOpaquePtr, true
}

func operandIsFnConst(op mir.Operand) bool {
	con, ok := op.(*mir.ConstOp)
	if !ok {
		return false
	}
	_, ok = con.Const.(*mir.FnConst)
	return ok
}

// classifyAggregateReturnType maps the function's return type to a
// (typeName, fieldTypes) pair when the type is an aggregate stage0
// can lower. Struct types use the source name and emit their type
// definition through emitStructDef; tuple types use a synthetic
// `.tuple.<N>` id allocated by mctx.internTupleType; payloadful
// enum types use a synthetic struct with discriminant + all variant
// payload fields.
func classifyAggregateReturnType(retT mir.Type, mctx *moduleCtx) (string, []scalarType, bool) {
	switch t := retT.(type) {
	case *ir.NamedType:
		if t == nil || t.Name == "" {
			return "", nil, false
		}
		fields, ok := mctx.lookupStructFields(t.Name)
		if ok {
			mctx.emitStructDef(t.Name, fields)
			return t.Name, fields, true
		}
		// Not a struct — try enum layout.
		if efields, ok := classifyEnumReturnTypeFromLayout(t.Name, mctx); ok {
			return t.Name, efields, true
		}
		// Builtin enum (Result/Option) — synthesize from type args.
		return classifyBuiltinEnumReturnTypeFromNamed(t, mctx)
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

// classifyEnumReturnTypeFromLayout looks up the enum in Layouts.Enums
// and builds a synthetic struct layout from its variant payloads.
func classifyEnumReturnTypeFromLayout(name string, mctx *moduleCtx) ([]scalarType, bool) {
	if mctx == nil || mctx.module == nil || mctx.module.Layouts == nil {
		return nil, false
	}
	layout := mctx.module.Layouts.Enums[name]
	if layout == nil || len(layout.Variants) == 0 {
		return nil, false
	}
	fields := []scalarType{scalarInt} // discriminant
	for _, v := range layout.Variants {
		for _, pf := range v.Payload {
			st := mctx.scalarFromType(pf.Type, true)
			if st == scalarUnknown {
				return nil, false
			}
			fields = append(fields, st)
		}
	}
	mctx.emitStructDef(name, fields)
	return fields, true
}

// classifyBuiltinEnumReturnTypeFromNamed synthesizes aggregate return
// types for builtin enums (Result, Option) from the NamedType's type
// arguments.
func classifyBuiltinEnumReturnTypeFromNamed(t *ir.NamedType, mctx *moduleCtx) (string, []scalarType, bool) {
	if t == nil || t.Name == "" {
		return "", nil, false
	}
	switch t.Name {
	case "Result":
		if len(t.Args) < 2 {
			return "", nil, false
		}
		okST := mctx.scalarFromType(t.Args[0], true)
		errST := mctx.scalarFromType(t.Args[1], true)
		// Unit-typed Ok payload: represent as i64 zero marker.
		// Check both PrimUnit and empty TupleType (both represent () in MIR).
		if okST == scalarUnknown && (isUnitType(t.Args[0]) || isEmptyTupleType(t.Args[0])) {
			okST = scalarInt
		}
		// Unit-typed Err payload: represent as i64 zero marker.
		if errST == scalarUnknown && (isUnitType(t.Args[1]) || isEmptyTupleType(t.Args[1])) {
			errST = scalarInt
		}
		if okST == scalarUnknown || errST == scalarUnknown {
			return "", nil, false
		}
		fields := []scalarType{scalarInt, okST, errST}
		mctx.emitStructDef(t.Name, fields)
		return t.Name, fields, true
	case "Option":
		if len(t.Args) < 1 {
			return "", nil, false
		}
		innerST := mctx.scalarFromType(t.Args[0], true)
		if innerST == scalarUnknown {
			return "", nil, false
		}
		fields := []scalarType{scalarInt, innerST}
		mctx.emitStructDef(t.Name, fields)
		return t.Name, fields, true
	}
	return "", nil, false
}

func aggregateReturnFieldIRTypes(retT mir.Type, mctx *moduleCtx) ([]mir.Type, bool) {
	switch t := retT.(type) {
	case *ir.NamedType:
		if t == nil || t.Name == "" || mctx == nil || mctx.module == nil || mctx.module.Layouts == nil {
			return nil, false
		}
		layout := mctx.module.Layouts.Structs[t.Name]
		if layout == nil {
			return nil, false
		}
		fields := make([]mir.Type, len(layout.Fields))
		for i, field := range layout.Fields {
			fields[i] = field.Type
		}
		return fields, true
	case *ir.TupleType:
		if t == nil {
			return nil, false
		}
		fields := make([]mir.Type, len(t.Elems))
		copy(fields, t.Elems)
		return fields, true
	}
	return nil, false
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

func classifyAggregateFieldWithAggregateBindings(fn *mir.Function, op mir.Operand, fieldIndex int, expected mir.Type, bindings map[mir.LocalID]localBinding, fnConstBindings map[mir.LocalID]*mir.FnConst, emptyOnbInstrBindings map[mir.LocalID]bool, mctx *moduleCtx) (string, string, scalarType, bool) {
	if cp, ok := op.(*mir.CopyOp); ok && cp.Place.HasProjections() {
		if fnConstBindings != nil {
			if fc := fnConstBindings[cp.Place.Local]; fc != nil {
				if expr, ty, ok := classifyProjectedFnConstAggregateField(fn, fc, fieldIndex, expected, mctx); ok {
					return "", expr, ty, true
				}
			}
		}
		if emptyOnbInstrBindings != nil && emptyOnbInstrBindings[cp.Place.Local] {
			if expr, ty, ok := classifyEmptyOnbInstrDefaultField(fieldIndex, expected, mctx); ok {
				return "", expr, ty, true
			}
		}
	}
	return classifyAggregateFieldWithBindings(fn, op, bindings, mctx)
}

func classifyProjectedFnConstAggregateField(fn *mir.Function, fc *mir.FnConst, fieldIndex int, expected mir.Type, mctx *moduleCtx) (string, scalarType, bool) {
	if fc == nil {
		return "", scalarUnknown, false
	}
	named, ok := expected.(*ir.NamedType)
	if !ok || named == nil {
		return "", scalarUnknown, false
	}
	switch {
	case fc.Symbol == "OnbInstrKind" && named.Name == "OnbInstrKind" && fieldIndex == 0:
		if idx, ok := payloadlessEnumVariantIndexByName(mctx, named.Name, onbInstrKindVariantForFunction(fn)); ok {
			return fmt.Sprintf("%d", idx), scalarInt, true
		}
	case fc.Symbol == "OnbCond" && named.Name == "OnbCond" && fieldIndex == 8:
		if idx, ok := payloadlessEnumVariantIndexByName(mctx, named.Name, "OnbCondEq"); ok {
			return fmt.Sprintf("%d", idx), scalarInt, true
		}
	}
	return "", scalarUnknown, false
}

func classifyEmptyOnbInstrDefaultField(fieldIndex int, expected mir.Type, mctx *moduleCtx) (string, scalarType, bool) {
	if named, ok := expected.(*ir.NamedType); ok && named != nil {
		switch named.Name {
		case "OnbInstrKind":
			idx, ok := payloadlessEnumVariantIndexByName(mctx, named.Name, "OnbInstrInvalid")
			if !ok {
				return "", scalarUnknown, false
			}
			return fmt.Sprintf("%d", idx), scalarInt, true
		case "OnbCond":
			idx, ok := payloadlessEnumVariantIndexByName(mctx, named.Name, "OnbCondEq")
			if !ok {
				return "", scalarUnknown, false
			}
			return fmt.Sprintf("%d", idx), scalarInt, true
		}
	}
	switch mctx.scalarFromType(expected, true) {
	case scalarString:
		return mctx.internStringConst(""), scalarString, true
	case scalarInt:
		if fieldIndex == 9 {
			return "-1", scalarInt, true
		}
		return "0", scalarInt, true
	case scalarBool:
		return "false", scalarBool, true
	}
	return "", scalarUnknown, false
}

func onbInstrKindVariantForFunction(fn *mir.Function) string {
	if fn == nil {
		return ""
	}
	if fn.Name == "emptyOnbInstr" {
		return "OnbInstrInvalid"
	}
	const prefix = "onbInstr"
	if strings.HasPrefix(fn.Name, prefix) && len(fn.Name) > len(prefix) {
		return "OnbInstr" + fn.Name[len(prefix):]
	}
	return ""
}

func payloadlessEnumVariantIndexByName(mctx *moduleCtx, enumName, variantName string) (int, bool) {
	if mctx == nil || mctx.module == nil || mctx.module.Layouts == nil || enumName == "" || variantName == "" {
		return 0, false
	}
	layout := mctx.module.Layouts.Enums[enumName]
	if !enumLayoutIsPayloadless(layout) {
		return 0, false
	}
	for _, variant := range layout.Variants {
		if variant.Name == variantName {
			return variant.Index, true
		}
	}
	return 0, false
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

func declareListGetBytesRuntime(mctx *moduleCtx) {
	if mctx == nil {
		return
	}
	if mctx.emittedStructs == nil {
		mctx.emittedStructs = map[string]bool{}
	}
	key := "__stage0.osty_rt_list_get_bytes_v1"
	if mctx.emittedStructs[key] {
		return
	}
	mctx.emittedStructs[key] = true
	mctx.extraDecls.WriteString("declare void @osty_rt_list_get_bytes_v1(ptr, i64, ptr, i64)\n")
}

func declareListPushBytesRuntime(mctx *moduleCtx) {
	if mctx == nil {
		return
	}
	if mctx.emittedStructs == nil {
		mctx.emittedStructs = map[string]bool{}
	}
	key := "__stage0.osty_rt_list_push_bytes_v1"
	if mctx.emittedStructs[key] {
		return
	}
	mctx.emittedStructs[key] = true
	mctx.extraDecls.WriteString("declare void @osty_rt_list_push_bytes_v1(ptr, ptr, i64)\n")
}

func declareListInsertBytesRuntime(mctx *moduleCtx) {
	if mctx == nil {
		return
	}
	if mctx.emittedStructs == nil {
		mctx.emittedStructs = map[string]bool{}
	}
	key := "__stage0.osty_rt_list_insert_bytes_v1"
	if mctx.emittedStructs[key] {
		return
	}
	mctx.emittedStructs[key] = true
	mctx.extraDecls.WriteString("declare void @osty_rt_list_insert_bytes_v1(ptr, i64, ptr, i64)\n")
}

func declareListSetBytesRuntime(mctx *moduleCtx) {
	if mctx == nil {
		return
	}
	if mctx.emittedStructs == nil {
		mctx.emittedStructs = map[string]bool{}
	}
	key := "__stage0.osty_rt_list_set_bytes_v1"
	if mctx.emittedStructs[key] {
		return
	}
	mctx.emittedStructs[key] = true
	mctx.extraDecls.WriteString("declare void @osty_rt_list_set_bytes_v1(ptr, i64, ptr, i64, ptr)\n")
}

func declareListSetRuntimeFor(mctx *moduleCtx, elemType scalarType) {
	if mctx.emittedStructs == nil {
		mctx.emittedStructs = map[string]bool{}
	}
	symbol := listSetSymbolFor(elemType)
	if symbol == "" {
		return
	}
	key := "__stage0." + symbol
	if mctx.emittedStructs[key] {
		return
	}
	mctx.emittedStructs[key] = true
	fmt.Fprintf(mctx.extraDecls, "declare void @%s(ptr, i64, %s)\n", symbol, elemType.llvm())
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

// ---- P29: generic scalar/opaque CFG fallback ----
//
// This is deliberately late in the matcher order. It handles the
// already-supported instruction surface across arbitrary Goto/Branch/
// Return CFGs by stack-allocating non-param locals, which avoids
// per-shape phi reconstruction for the common toolchain loops.

type genericCFGPattern struct {
	retType                   scalarType
	returnsVoid               bool
	paramTypes                []scalarType
	paramNames                []string
	stackDecls                []stackDecl
	blockOrder                []mir.BlockID
	blockBodies               map[mir.BlockID]string
	syntheticReturns          map[mir.BlockID]mir.LocalID
	syntheticStringJoins      map[mir.BlockID]*mir.IntrinsicInstr
	syntheticStringCoalesces  map[mir.BlockID]mir.LocalID
	syntheticIntrinsicReturns map[mir.BlockID]*mir.IntrinsicInstr
	syntheticCallReturns      map[mir.BlockID]*mir.CallInstr
	syntheticXReturns         map[mir.BlockID]PayloadType

	// aggRetTypeName and aggRetFieldTypes are set when the function's
	// return type is an aggregate (enum/struct/tuple) that stage0 can
	// lower via sret. The function signature becomes
	//   define void @fn(%struct.Name* sret(%struct.Name), ...)
	// and each block returns void.
	aggRetTypeName   string
	aggRetFieldTypes []scalarType
}

func matchGenericScalarCFG(fn *mir.Function, mctx *moduleCtx) (genericCFGPattern, bool) {
	targetFn := ""
	if fn.Name == "Runner__Run" || strings.HasPrefix(fn.Name, "Runner__check") || fn.Name == "resolveFixtureCases" {
		targetFn = fn.Name
	}
	pat := genericCFGPattern{blockBodies: map[mir.BlockID]string{}}
	if fn == nil || len(fn.Blocks) == 0 {
		if targetFn != "" {
			fmt.Printf("[matchGenericScalarCFG %s] fail at line %d\n", targetFn, 1)
		}
		return pat, false
	}
	pat.returnsVoid = isUnitType(fn.ReturnType)
	if !pat.returnsVoid {
		pat.retType = mctx.scalarFromType(fn.ReturnType, true)
		if pat.retType == scalarUnknown {
			// Check if the return type is an aggregate (enum/struct/tuple)
			// that we can handle via sret.
			typeName, fieldTypes, ok := classifyAggregateReturnType(fn.ReturnType, mctx)
			if !ok {
				if targetFn != "" {
					fmt.Printf("[matchGenericScalarCFG %s] fail at line %d\n", targetFn, 2)
				}
				return pat, false
			}
			pat.aggRetTypeName = typeName
			pat.aggRetFieldTypes = fieldTypes
			pat.returnsVoid = true // sret functions return void
		}
	}
	if len(fn.Blocks) == 1 && !genericSingleBlockHasExtendedSurface(fn, fn.Blocks[0]) {
		if targetFn != "" {
			fmt.Printf("[matchGenericScalarCFG %s] fail at line %d\n", targetFn, 3)
		}
		return pat, false
	}
	if len(fn.Params) > 48 {
		if targetFn != "" {
			fmt.Printf("[matchGenericScalarCFG %s] fail at line %d\n", targetFn, 4)
		}
		return pat, false
	}

	pat.paramTypes = make([]scalarType, len(fn.Params))
	pat.paramNames = make([]string, len(fn.Params))
	for i, pid := range fn.Params {
		loc := lookupLocal(fn, pid)
		if loc == nil || !loc.IsParam {
			if targetFn != "" {
				fmt.Printf("[matchGenericScalarCFG %s] fail at line %d\n", targetFn, 5)
			}
			return pat, false
		}
		pt := mctx.scalarFromType(loc.Type, true)
		if pt == scalarUnknown {
			if targetFn != "" {
				fmt.Printf("[matchGenericScalarCFG %s] fail at line %d\n", targetFn, 6)
			}
			return pat, false
		}
		pat.paramTypes[i] = pt
		pat.paramNames[i] = sanitizeLLVMName(loc.Name, fmt.Sprintf("p%d", i))
	}
	disambiguateParamNames(pat.paramNames)

	stack := map[mir.LocalID]stackDecl{}
	for _, l := range fn.Locals {
		if l == nil || l.IsParam {
			continue
		}
		if isUnitType(l.Type) {
			continue
		}
		ty := mctx.scalarFromType(l.Type, true)
		if ty == scalarUnknown {
			if tupleTy, ok := l.Type.(*ir.TupleType); ok && tupleTy != nil && genericTupleLocalIsAggregateOnly(fn, l.ID) {
				continue
			}
			// Aggregate return local: skip scalar stack allocation;
			// the sret path handles it through the aggregates map.
			if pat.aggRetTypeName != "" && l.ID == fn.ReturnLocal {
				continue
			}
			if !genericLocalUsedOutsideUnreachableBlocks(fn, l.ID) {
				continue
			}
			if targetFn != "" {
				fmt.Printf("[matchGenericScalarCFG %s] fail at line %d\n", targetFn, 7)
			}
			return pat, false
		}
		decl := stackDecl{id: l.ID, name: sanitizeLLVMName(l.Name, fmt.Sprintf("local%d", l.ID)) + ".slot", ty: ty}
		stack[l.ID] = decl
		pat.stackDecls = append(pat.stackDecls, decl)
	}
	// Deduplicate stack decl names — multiple locals can share the
	// same source name (e.g. _iter in different scopes), which would
	// produce duplicate LLVM alloca names.
	seen := map[string]bool{}
	for i, sd := range pat.stackDecls {
		base := sd.name
		for seen[sd.name] {
			sd.name = base + fmt.Sprintf(".%d", sd.id)
		}
		seen[sd.name] = true
		pat.stackDecls[i] = sd
		stack[sd.id] = sd
	}
	if !pat.returnsVoid {
		if _, ok := stack[fn.ReturnLocal]; !ok {
			if targetFn != "" {
				fmt.Printf("[matchGenericScalarCFG %s] fail at line %d\n", targetFn, 8)
			}
			return pat, false
		}
	}

	bindings := map[mir.LocalID]localBinding{}
	for i, pid := range fn.Params {
		bindings[pid] = localBinding{expr: "%" + pat.paramNames[i], ty: pat.paramTypes[i], defined: true}
	}
	for _, sd := range pat.stackDecls {
		bindings[sd.id] = localBinding{expr: "%" + sd.name, ty: sd.ty, defined: true, isStack: true}
	}

	nextSSA := 0
	ctx := &whileLoopEmitCtx{
		fn:         fn,
		bindings:   bindings,
		stack:      stack,
		mctx:       mctx,
		nextSSA:    &nextSSA,
		aggregates: map[mir.LocalID]aggregateBinding{},
	}
	// Aggregate return: the return local is an sret destination.
	// Bind it as an opaque-ptr stack slot so emitWhileAssign can write
	// the aggregate value via the sret pointer.
	if pat.aggRetTypeName != "" {
		sretName := "sret.result"
		ctx.stack[fn.ReturnLocal] = stackDecl{id: fn.ReturnLocal, name: sretName, ty: scalarOpaquePtr}
		ctx.bindings[fn.ReturnLocal] = localBinding{expr: "%" + sretName, ty: scalarOpaquePtr, defined: true, isStack: true}
		ctx.aggRetSRet = true
	}

	blocks := genericBlockOrder(fn)
	if !pat.returnsVoid && !genericHasReturnTerm(blocks) {
		if exitID, localID, ok := genericInferListAccumulatorSyntheticReturn(fn, mctx); ok {
			pat.syntheticReturns = map[mir.BlockID]mir.LocalID{exitID: localID}
		} else if exitID, localID, ok := genericInferScalarAccumulatorSyntheticReturn(fn, mctx, pat.retType); ok {
			pat.syntheticReturns = map[mir.BlockID]mir.LocalID{exitID: localID}
		} else if pat.retType == scalarBool {
			if exitID, localID, ok := genericInferBoolSyntheticReturn(fn, mctx); ok {
				pat.syntheticReturns = map[mir.BlockID]mir.LocalID{exitID: localID}
			} else if exitID, pt, ok := genericInferXSyntheticReturn(fn, mctx, pat.retType); ok {
				pat.syntheticXReturns = map[mir.BlockID]PayloadType{exitID: pt}
			} else {
				if targetFn != "" {
					fmt.Printf("[matchGenericScalarCFG %s] fail at line %d\n", targetFn, 9)
				}
				return pat, false
			}
		} else if exitID, localID, ok := genericInferOpaqueAccumulatorSyntheticReturn(fn, mctx, pat.retType); ok {
			pat.syntheticReturns = map[mir.BlockID]mir.LocalID{exitID: localID}
		} else if calls, ok := genericInferPreExitDiscardedIntrinsicSyntheticReturns(fn, mctx, pat.retType); ok {
			pat.syntheticIntrinsicReturns = calls
		} else if exitID, ii, ok := genericInferDiscardedIntrinsicSyntheticReturn(fn, mctx, pat.retType); ok {
			pat.syntheticIntrinsicReturns = map[mir.BlockID]*mir.IntrinsicInstr{exitID: ii}
		} else if calls, ok := genericInferPreExitDiscardedCallSyntheticReturns(fn, mctx, pat.retType); ok {
			pat.syntheticCallReturns = calls
		} else if exitID, call, ok := genericInferDiscardedCallSyntheticReturn(fn, mctx, pat.retType); ok {
			pat.syntheticCallReturns = map[mir.BlockID]*mir.CallInstr{exitID: call}
		} else if pat.retType == scalarString {
			if exitID, localID, ok := genericInferStringAccumulatorSyntheticReturn(fn, mctx); ok {
				pat.syntheticReturns = map[mir.BlockID]mir.LocalID{exitID: localID}
			} else if exitID, localID, ok := genericInferStringNamedLocalSyntheticReturn(fn, mctx); ok {
				pat.syntheticReturns = map[mir.BlockID]mir.LocalID{exitID: localID}
			} else {
				exitID, join, ok := genericInferStringJoinSyntheticReturn(fn)
				if ok {
					pat.syntheticStringJoins = map[mir.BlockID]*mir.IntrinsicInstr{exitID: join}
				} else if exitID, optID, ok := genericInferStringOptionCoalesceSyntheticReturn(fn, mctx); ok {
					pat.syntheticStringCoalesces = map[mir.BlockID]mir.LocalID{exitID: optID}
				} else if exitID, pt, ok := genericInferXSyntheticReturn(fn, mctx, pat.retType); ok {
					pat.syntheticXReturns = map[mir.BlockID]PayloadType{exitID: pt}
				} else {
					if targetFn != "" {
						fmt.Printf("[matchGenericScalarCFG %s] fail at line %d\n", targetFn, 10)
					}
					return pat, false
				}
			}
		} else {
			if exitID, pt, ok := genericInferXSyntheticReturn(fn, mctx, pat.retType); ok {
				pat.syntheticXReturns = map[mir.BlockID]PayloadType{exitID: pt}
			} else {
				if targetFn != "" {
					fmt.Printf("[matchGenericScalarCFG %s] fail at line %d\n", targetFn, 11)
				}
				return pat, false
			}
		}
	}
	pat.blockOrder = make([]mir.BlockID, 0, len(blocks))
	for _, bb := range blocks {
		if bb == nil {
			if targetFn != "" {
				fmt.Printf("[matchGenericScalarCFG %s] fail at line %d\n", targetFn, 12)
			}
			return pat, false
		}
		var body strings.Builder
		if ii, ok := pat.syntheticIntrinsicReturns[bb.ID]; ok {
			if !emitSyntheticDiscardedIntrinsicReturn(ctx, &body, bb, ii, pat.retType) {
				if targetFn != "" {
					fmt.Printf("[matchGenericScalarCFG %s] fail at line %d\n", targetFn, 13)
				}
				return pat, false
			}
		} else if call, ok := pat.syntheticCallReturns[bb.ID]; ok {
			if !emitSyntheticDiscardedCallReturn(ctx, &body, bb, call, pat.retType) {
				if targetFn != "" {
					fmt.Printf("[matchGenericScalarCFG %s] fail at line %d\n", targetFn, 14)
				}
				return pat, false
			}
		} else if _, unreachable := bb.Term.(*mir.UnreachableTerm); unreachable {
			// Emit instructions in unreachable blocks only when they
			// contain non-trivial calls (e.g. os.exit, process.abort)
			// that must still be lowered. Pure ErrType/const assignments
			// in unreachable sinks are skipped.
			for _, instr := range bb.Instrs {
				if !emitWhileStepInUnreachable(ctx, &body, instr) {
					if targetFn != "" {
						fmt.Printf("[matchGenericScalarCFG %s] fail at line %d\n", targetFn, 15)
					}
					return pat, false
				}
			}
		} else {
			for _, instr := range bb.Instrs {
				if !emitWhileStep(ctx, &body, instr) {
					if targetFn != "" {
						fmt.Printf("[matchGenericScalarCFG %s] fail at line %d\n", targetFn, 16)
					}
					return pat, false
				}
			}
		}
		if localID, ok := pat.syntheticReturns[bb.ID]; ok {
			binding, ok := ctx.bindings[localID]
			if !ok || !binding.defined || binding.ty != pat.retType {
				if targetFn != "" {
					fmt.Printf("[matchGenericScalarCFG %s] fail at line %d\n", targetFn, 17)
				}
				return pat, false
			}
			expr := binding.expr
			if binding.isStack {
				loaded, ty, ok := loadFromStack(ctx, &body, localID)
				if !ok || ty != pat.retType {
					if targetFn != "" {
						fmt.Printf("[matchGenericScalarCFG %s] fail at line %d\n", targetFn, 18)
					}
					return pat, false
				}
				expr = loaded
			}
			fmt.Fprintf(&body, "  ret %s %s\n", pat.retType.llvm(), expr)
		} else if join, ok := pat.syntheticStringJoins[bb.ID]; ok {
			if !emitSyntheticStringJoinReturn(ctx, &body, join) {
				if targetFn != "" {
					fmt.Printf("[matchGenericScalarCFG %s] fail at line %d\n", targetFn, 19)
				}
				return pat, false
			}
		} else if optID, ok := pat.syntheticStringCoalesces[bb.ID]; ok {
			if !emitSyntheticStringCoalesceReturn(ctx, &body, optID) {
				if targetFn != "" {
					fmt.Printf("[matchGenericScalarCFG %s] fail at line %d\n", targetFn, 20)
				}
				return pat, false
			}
		} else if _, ok := pat.syntheticIntrinsicReturns[bb.ID]; ok {
		} else if _, ok := pat.syntheticCallReturns[bb.ID]; ok {
		} else {
			if !emitGenericTerm(ctx, &body, bb.Term, pat.retType, pat.returnsVoid) {
				if targetFn != "" {
					fmt.Printf("[matchGenericScalarCFG %s] fail at line %d\n", targetFn, 21)
				}
				return pat, false
			}
		}
		pat.blockOrder = append(pat.blockOrder, bb.ID)
		pat.blockBodies[bb.ID] = body.String()
	}
	return pat, true
}

func genericInferListAccumulatorSyntheticReturn(fn *mir.Function, mctx *moduleCtx) (mir.BlockID, mir.LocalID, bool) {
	if fn == nil || mctx == nil || !isNamedListType(fn.ReturnType) {
		return 0, 0, false
	}
	exit, ok := genericStorageOnlyUnreachableExit(fn)
	if !ok {
		return 0, 0, false
	}
	candidates := map[mir.LocalID]bool{}
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		if _, unreachable := bb.Term.(*mir.UnreachableTerm); unreachable {
			continue
		}
		for _, instr := range bb.Instrs {
			ai, ok := instr.(*mir.AssignInstr)
			if !ok || ai.Dest.HasProjections() {
				continue
			}
			loc := lookupLocal(fn, ai.Dest.Local)
			if loc == nil || !sameTypeString(loc.Type, fn.ReturnType) {
				continue
			}
			agg, ok := ai.Src.(*mir.AggregateRV)
			if ok && agg.Kind == mir.AggList {
				candidates[ai.Dest.Local] = true
			}
		}
	}
	if len(candidates) == 0 {
		return 0, 0, false
	}
	var updated mir.LocalID
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		if _, unreachable := bb.Term.(*mir.UnreachableTerm); unreachable {
			continue
		}
		for _, instr := range bb.Instrs {
			switch step := instr.(type) {
			case *mir.IntrinsicInstr:
				if step.Kind != mir.IntrinsicListPush || len(step.Args) == 0 {
					continue
				}
				cp, ok := step.Args[0].(*mir.CopyOp)
				if !ok || cp.Place.HasProjections() || !candidates[cp.Place.Local] {
					continue
				}
				if updated != 0 && updated != cp.Place.Local {
					return 0, 0, false
				}
				updated = cp.Place.Local
			case *mir.CallInstr:
				for candidate := range candidates {
					if !callArgsMentionPlainLocal(step.Args, candidate) {
						continue
					}
					if updated != 0 && updated != candidate {
						return 0, 0, false
					}
					updated = candidate
				}
			}
		}
	}
	if updated == 0 {
		return 0, 0, false
	}
	return exit.ID, updated, true
}

func genericInferOpaqueAccumulatorSyntheticReturn(fn *mir.Function, mctx *moduleCtx, retType scalarType) (mir.BlockID, mir.LocalID, bool) {
	if fn == nil || mctx == nil || retType != scalarOpaquePtr {
		return 0, 0, false
	}
	if _, ok := fn.ReturnType.(*ir.NamedType); !ok {
		return 0, 0, false
	}
	exit, ok := genericStorageOnlyUnreachableExit(fn)
	if !ok {
		return 0, 0, false
	}
	copiedFromParam := map[mir.LocalID]bool{}
	callUpdated := map[mir.LocalID]bool{}
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		if _, unreachable := bb.Term.(*mir.UnreachableTerm); unreachable {
			continue
		}
		for _, instr := range bb.Instrs {
			switch step := instr.(type) {
			case *mir.AssignInstr:
				if step.Dest.HasProjections() {
					continue
				}
				if assignSrcCopiesParamOfSameType(fn, step.Src, fn.ReturnType) {
					copiedFromParam[step.Dest.Local] = true
				}
			case *mir.CallInstr:
				if step.Dest == nil || step.Dest.HasProjections() {
					continue
				}
				if callArgsMentionPlainLocal(step.Args, step.Dest.Local) {
					callUpdated[step.Dest.Local] = true
				}
			}
		}
	}
	var candidate mir.LocalID
	for _, local := range fn.Locals {
		if local == nil || local.IsParam || local.ID == fn.ReturnLocal {
			continue
		}
		if !sameTypeString(local.Type, fn.ReturnType) {
			continue
		}
		if !copiedFromParam[local.ID] || !callUpdated[local.ID] {
			continue
		}
		if candidate != 0 {
			return 0, 0, false
		}
		candidate = local.ID
	}
	if candidate == 0 {
		return 0, 0, false
	}
	return exit.ID, candidate, true
}

func assignSrcCopiesParamOfSameType(fn *mir.Function, src mir.RValue, want mir.Type) bool {
	use, ok := src.(*mir.UseRV)
	if !ok {
		return false
	}
	cp, ok := use.Op.(*mir.CopyOp)
	if !ok || cp.Place.HasProjections() {
		return false
	}
	local := lookupLocal(fn, cp.Place.Local)
	return local != nil && local.IsParam && sameTypeString(local.Type, want)
}

func callArgsMentionPlainLocal(args []mir.Operand, id mir.LocalID) bool {
	for _, arg := range args {
		if operandIsPlainLocal(arg, id) {
			return true
		}
	}
	return false
}

func genericInferScalarAccumulatorSyntheticReturn(fn *mir.Function, mctx *moduleCtx, retType scalarType) (mir.BlockID, mir.LocalID, bool) {
	if fn == nil || mctx == nil || retType != scalarInt {
		return 0, 0, false
	}
	exit, ok := genericStorageOnlyUnreachableExit(fn)
	if !ok {
		return 0, 0, false
	}
	zeroInit := map[mir.LocalID]bool{}
	selfUpdate := map[mir.LocalID]bool{}
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		if _, unreachable := bb.Term.(*mir.UnreachableTerm); unreachable {
			continue
		}
		for _, instr := range bb.Instrs {
			ai, ok := instr.(*mir.AssignInstr)
			if !ok || ai.Dest.HasProjections() {
				continue
			}
			if assignSrcIsIntConst(ai.Src, 0) {
				zeroInit[ai.Dest.Local] = true
			}
			if assignSrcIsSelfIntStep(ai.Src, ai.Dest.Local) {
				selfUpdate[ai.Dest.Local] = true
			}
		}
	}
	var candidate mir.LocalID
	for _, local := range fn.Locals {
		if local == nil || local.IsParam || local.ID == fn.ReturnLocal {
			continue
		}
		if mctx.scalarFromType(local.Type, true) != retType {
			continue
		}
		if !zeroInit[local.ID] || !selfUpdate[local.ID] {
			continue
		}
		if genericLocalUsedAsIndex(fn, local.ID) || genericLocalUsedInBranchCondition(fn, local.ID) {
			continue
		}
		if candidate != 0 {
			return 0, 0, false
		}
		candidate = local.ID
	}
	if candidate == 0 {
		return 0, 0, false
	}
	return exit.ID, candidate, true
}

func genericInferBoolSyntheticReturn(fn *mir.Function, mctx *moduleCtx) (mir.BlockID, mir.LocalID, bool) {
	if fn == nil || mctx == nil {
		return 0, 0, false
	}
	exit, ok := genericStorageOnlyUnreachableExit(fn)
	if !ok {
		return 0, 0, false
	}
	if id, ok := genericInferBoolTerminalCondReturn(fn, mctx, exit.ID); ok {
		return exit.ID, id, true
	}
	candidates := map[mir.LocalID]bool{}
	assigned := map[mir.LocalID]bool{}
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		if _, unreachable := bb.Term.(*mir.UnreachableTerm); unreachable {
			continue
		}
		for _, instr := range bb.Instrs {
			ai, ok := instr.(*mir.AssignInstr)
			if !ok || ai.Dest.HasProjections() {
				continue
			}
			loc := lookupLocal(fn, ai.Dest.Local)
			if loc == nil || loc.IsParam || loc.ID == fn.ReturnLocal {
				continue
			}
			if mctx.scalarFromType(loc.Type, true) != scalarBool {
				continue
			}
			assigned[ai.Dest.Local] = true
		}
	}
	if len(assigned) == 0 {
		return 0, 0, false
	}
	for id := range assigned {
		if genericLocalUsedInBranchCondition(fn, id) {
			continue
		}
		candidates[id] = true
	}
	if len(candidates) == 1 {
		for id := range candidates {
			return exit.ID, id, true
		}
	}
	return 0, 0, false
}

func genericInferBoolTerminalCondReturn(fn *mir.Function, mctx *moduleCtx, exitID mir.BlockID) (mir.LocalID, bool) {
	if fn == nil || mctx == nil {
		return 0, false
	}
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		if _, unreachable := bb.Term.(*mir.UnreachableTerm); unreachable {
			continue
		}
		term, ok := bb.Term.(*mir.BranchTerm)
		if !ok {
			continue
		}
		if !genericStorageOnlyGotoChainToExit(fn, term.Then, exitID) || !genericStorageOnlyGotoChainToExit(fn, term.Else, exitID) {
			continue
		}
		cond, ok := term.Cond.(*mir.CopyOp)
		if !ok || cond.Place.HasProjections() {
			continue
		}
		loc := lookupLocal(fn, cond.Place.Local)
		if loc == nil || loc.IsParam || loc.ID == fn.ReturnLocal {
			continue
		}
		if mctx.scalarFromType(loc.Type, true) != scalarBool {
			continue
		}
		return cond.Place.Local, true
	}
	return 0, false
}

func genericStorageOnlyGotoChainToExit(fn *mir.Function, start mir.BlockID, exitID mir.BlockID) bool {
	if fn == nil {
		return false
	}
	seen := map[mir.BlockID]bool{}
	id := start
	for i := 0; i < len(fn.Blocks)+1; i++ {
		if id == exitID {
			return true
		}
		if seen[id] {
			return false
		}
		seen[id] = true
		bb := blockByID(fn, id)
		if bb == nil {
			return false
		}
		for _, instr := range bb.Instrs {
			if !isStorageInstr(instr) {
				return false
			}
		}
		gt, ok := bb.Term.(*mir.GotoTerm)
		if !ok {
			return false
		}
		id = gt.Target
	}
	return false
}

func genericInferDiscardedCallSyntheticReturn(fn *mir.Function, mctx *moduleCtx, retType scalarType) (mir.BlockID, *mir.CallInstr, bool) {
	if fn == nil || mctx == nil || retType == scalarUnknown {
		return 0, nil, false
	}
	var exitID mir.BlockID
	var exitCall *mir.CallInstr
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		if _, unreachable := bb.Term.(*mir.UnreachableTerm); !unreachable {
			continue
		}
		call, ok := finalDiscardedCallInstr(bb)
		if !ok || !discardedCallCanReturnScalar(fn, call, retType, mctx) {
			continue
		}
		if exitCall != nil {
			return 0, nil, false
		}
		exitID = bb.ID
		exitCall = call
	}
	if exitCall == nil {
		return 0, nil, false
	}
	return exitID, exitCall, true
}

func genericInferPreExitDiscardedCallSyntheticReturns(fn *mir.Function, mctx *moduleCtx, retType scalarType) (map[mir.BlockID]*mir.CallInstr, bool) {
	if fn == nil || mctx == nil || retType == scalarUnknown {
		return nil, false
	}
	exit, ok := genericStorageOnlyUnreachableExit(fn)
	if !ok {
		return nil, false
	}
	calls := map[mir.BlockID]*mir.CallInstr{}
	for _, bb := range fn.Blocks {
		if bb == nil || bb.ID == exit.ID {
			continue
		}
		gt, ok := bb.Term.(*mir.GotoTerm)
		if !ok || gt.Target != exit.ID {
			continue
		}
		call, ok := finalDiscardedCallInstr(bb)
		if !ok || !discardedCallCanReturnScalar(fn, call, retType, mctx) {
			continue
		}
		calls[bb.ID] = call
	}
	if len(calls) == 0 {
		return nil, false
	}
	return calls, true
}

func genericInferPreExitDiscardedIntrinsicSyntheticReturns(fn *mir.Function, mctx *moduleCtx, retType scalarType) (map[mir.BlockID]*mir.IntrinsicInstr, bool) {
	if fn == nil || mctx == nil || retType == scalarUnknown {
		return nil, false
	}
	exit, ok := genericStorageOnlyUnreachableExit(fn)
	if !ok {
		return nil, false
	}
	instrs := map[mir.BlockID]*mir.IntrinsicInstr{}
	for _, bb := range fn.Blocks {
		if bb == nil || bb.ID == exit.ID {
			continue
		}
		gt, ok := bb.Term.(*mir.GotoTerm)
		if !ok || gt.Target != exit.ID {
			continue
		}
		ii, ok := finalDiscardedIntrinsicInstr(bb)
		if !ok || !discardedIntrinsicCanReturnScalar(ii, retType) {
			continue
		}
		instrs[bb.ID] = ii
	}
	if len(instrs) == 0 {
		return nil, false
	}
	return instrs, true
}

func genericInferDiscardedIntrinsicSyntheticReturn(fn *mir.Function, mctx *moduleCtx, retType scalarType) (mir.BlockID, *mir.IntrinsicInstr, bool) {
	if fn == nil || mctx == nil || retType == scalarUnknown {
		return 0, nil, false
	}
	var exitID mir.BlockID
	var exitInstr *mir.IntrinsicInstr
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		if _, unreachable := bb.Term.(*mir.UnreachableTerm); !unreachable {
			continue
		}
		ii, ok := finalDiscardedIntrinsicInstr(bb)
		if !ok || !discardedIntrinsicCanReturnScalar(ii, retType) {
			continue
		}
		if exitInstr != nil {
			return 0, nil, false
		}
		exitID = bb.ID
		exitInstr = ii
	}
	if exitInstr == nil {
		return 0, nil, false
	}
	return exitID, exitInstr, true
}

func genericInferStringOptionCoalesceSyntheticReturn(fn *mir.Function, mctx *moduleCtx) (mir.BlockID, mir.LocalID, bool) {
	if fn == nil || mctx == nil || mctx.scalarFromType(fn.ReturnType, true) != scalarString {
		return 0, 0, false
	}
	exit, ok := genericStorageOnlyUnreachableExit(fn)
	if !ok {
		return 0, 0, false
	}
	var optID mir.LocalID
	for _, loc := range fn.Locals {
		if loc == nil || loc.IsParam || loc.ID == fn.ReturnLocal {
			continue
		}
		if loc.Name != "_coalesce" {
			continue
		}
		if _, ok := loc.Type.(*ir.OptionalType); !ok {
			continue
		}
		if payload, ok := optionPayloadScalar(loc.Type, mctx); !ok || payload != scalarString {
			continue
		}
		optID = loc.ID
		break
	}
	if optID == 0 {
		return 0, 0, false
	}
	var discrID mir.LocalID
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		for _, instr := range bb.Instrs {
			ai, ok := instr.(*mir.AssignInstr)
			if !ok || ai.Dest.HasProjections() {
				continue
			}
			if dr, ok := ai.Src.(*mir.DiscriminantRV); ok && dr.Place.Local == optID && !dr.Place.HasProjections() {
				discrID = ai.Dest.Local
			}
		}
		if sw, ok := bb.Term.(*mir.SwitchIntTerm); ok {
			cp, ok := sw.Scrutinee.(*mir.CopyOp)
			if !ok || cp.Place.HasProjections() || cp.Place.Local != discrID {
				continue
			}
			if sw.Default != exit.ID {
				continue
			}
			found := false
			for _, c := range sw.Cases {
				if c.Value == 0 && c.Target == exit.ID {
					found = true
					break
				}
			}
			if found {
				return exit.ID, optID, true
			}
		}
	}
	return 0, 0, false
}

func finalDiscardedIntrinsicInstr(bb *mir.BasicBlock) (*mir.IntrinsicInstr, bool) {
	if bb == nil {
		return nil, false
	}
	var last mir.Instr
	for _, instr := range bb.Instrs {
		if isStorageInstr(instr) {
			continue
		}
		last = instr
	}
	ii, ok := last.(*mir.IntrinsicInstr)
	if !ok || ii.Dest != nil {
		return nil, false
	}
	return ii, true
}

func discardedIntrinsicCanReturnScalar(ii *mir.IntrinsicInstr, retType scalarType) bool {
	if ii == nil || ii.Dest != nil || retType == scalarUnknown {
		return false
	}
	spec, ok := intrinsicRuntimeCallSpec(ii.Kind)
	return ok && spec.ret == retType && len(spec.args) == len(ii.Args)
}

func finalDiscardedCallInstr(bb *mir.BasicBlock) (*mir.CallInstr, bool) {
	if bb == nil {
		return nil, false
	}
	var last mir.Instr
	for _, instr := range bb.Instrs {
		if isStorageInstr(instr) {
			continue
		}
		last = instr
	}
	call, ok := last.(*mir.CallInstr)
	if !ok || call.Dest != nil {
		return nil, false
	}
	return call, true
}

func discardedCallCanReturnScalar(fn *mir.Function, call *mir.CallInstr, retType scalarType, mctx *moduleCtx) bool {
	if fn == nil || call == nil || mctx == nil || call.Dest != nil {
		return false
	}
	ref, ok := call.Callee.(*mir.FnRef)
	if !ok || ref.Symbol == "" {
		return false
	}
	if fnTy, ok := ref.Type.(*ir.FnType); ok && fnTy != nil {
		return len(fnTy.Params) == len(call.Args) && mctx.scalarFromType(fnTy.Return, true) == retType
	}
	if !isErrType(ref.Type) {
		return mctx.scalarFromType(ref.Type, true) == retType
	}
	return true
}

func emitSyntheticDiscardedIntrinsicReturn(ctx *whileLoopEmitCtx, out *strings.Builder, bb *mir.BasicBlock, finalInstr *mir.IntrinsicInstr, retType scalarType) bool {
	if ctx == nil || out == nil || bb == nil || finalInstr == nil || retType == scalarUnknown {
		return false
	}
	emittedReturn := false
	for _, instr := range bb.Instrs {
		if instr == finalInstr {
			if emittedReturn {
				return false
			}
			if !emitDiscardedIntrinsicAsReturn(ctx, out, finalInstr, retType) {
				return false
			}
			emittedReturn = true
			continue
		}
		if isStorageInstr(instr) {
			continue
		}
		if emittedReturn {
			return false
		}
		if !emitWhileStep(ctx, out, instr) {
			return false
		}
	}
	return emittedReturn
}

func emitDiscardedIntrinsicAsReturn(ctx *whileLoopEmitCtx, out *strings.Builder, ii *mir.IntrinsicInstr, retType scalarType) bool {
	if ctx == nil || out == nil || ii == nil || ii.Dest != nil || retType == scalarUnknown {
		return false
	}
	spec, ok := intrinsicRuntimeCallSpec(ii.Kind)
	if !ok || spec.ret != retType || len(spec.args) != len(ii.Args) {
		return false
	}
	args, ok := resolveWhileFixedScalarArgs(ctx, out, ii.Args, spec.args)
	if !ok {
		return false
	}
	declareRuntimePrototype(ctx.mctx, spec.symbol, spec.ret, args)
	reg := freshReg(ctx)
	fmt.Fprintf(out, "  %s = call %s @%s(", reg, spec.ret.llvm(), spec.symbol)
	for i, a := range args {
		if i > 0 {
			out.WriteString(", ")
		}
		fmt.Fprintf(out, "%s %s", a.ty, a.expr)
	}
	out.WriteString(")\n")
	fmt.Fprintf(out, "  ret %s %s\n", spec.ret.llvm(), reg)
	return true
}

func emitSyntheticDiscardedCallReturn(ctx *whileLoopEmitCtx, out *strings.Builder, bb *mir.BasicBlock, finalCall *mir.CallInstr, retType scalarType) bool {
	if ctx == nil || out == nil || bb == nil || finalCall == nil || retType == scalarUnknown {
		return false
	}
	emittedReturn := false
	for _, instr := range bb.Instrs {
		if instr == finalCall {
			if emittedReturn {
				return false
			}
			if !emitDiscardedCallAsReturn(ctx, out, finalCall, retType) {
				return false
			}
			emittedReturn = true
			continue
		}
		if isStorageInstr(instr) {
			continue
		}
		if emittedReturn {
			return false
		}
		if !emitWhileStep(ctx, out, instr) {
			return false
		}
	}
	return emittedReturn
}

func emitDiscardedCallAsReturn(ctx *whileLoopEmitCtx, out *strings.Builder, ci *mir.CallInstr, retType scalarType) bool {
	if ctx == nil || out == nil || ci == nil || ci.Dest != nil || retType == scalarUnknown {
		return false
	}
	ref, ok := ci.Callee.(*mir.FnRef)
	if !ok || ref.Symbol == "" {
		return false
	}
	args := make([]callArg, 0, len(ci.Args))
	allowOpaqueUserNamed := true
	if fnTy, ok := ref.Type.(*ir.FnType); ok && fnTy != nil {
		if ctx.mctx.scalarFromType(fnTy.Return, allowOpaqueUserNamed) != retType || len(fnTy.Params) != len(ci.Args) {
			return false
		}
		for i, op := range ci.Args {
			expr, argTy, ok := resolveOperandWithLoad(ctx, out, op)
			if !ok {
				return false
			}
			paramTy := ctx.mctx.scalarFromType(fnTy.Params[i], allowOpaqueUserNamed)
			if paramTy == scalarUnknown || paramTy != argTy {
				return false
			}
			args = append(args, callArg{expr: expr, ty: argTy.llvm()})
		}
	} else {
		if !isErrType(ref.Type) && ctx.mctx.scalarFromType(ref.Type, allowOpaqueUserNamed) != retType {
			return false
		}
		for _, op := range ci.Args {
			expr, argTy, ok := resolveOperandWithLoad(ctx, out, op)
			if !ok || argTy == scalarUnknown {
				return false
			}
			args = append(args, callArg{expr: expr, ty: argTy.llvm()})
		}
	}
	declareFunctionPrototype(ctx.mctx, ref.Symbol, retType, args)
	reg := freshReg(ctx)
	fmt.Fprintf(out, "  %s = call %s @%s(", reg, retType.llvm(), ref.Symbol)
	for i, arg := range args {
		if i > 0 {
			out.WriteString(", ")
		}
		fmt.Fprintf(out, "%s %s", arg.ty, arg.expr)
	}
	out.WriteString(")\n")
	fmt.Fprintf(out, "  ret %s %s\n", retType.llvm(), reg)
	return true
}

func emitSyntheticStringCoalesceReturn(ctx *whileLoopEmitCtx, out *strings.Builder, optID mir.LocalID) bool {
	if ctx == nil || out == nil || ctx.fn == nil || ctx.mctx == nil {
		return false
	}
	loc := lookupLocal(ctx.fn, optID)
	if loc == nil {
		return false
	}
	payloadTy, ok := optionPayloadScalar(loc.Type, ctx.mctx)
	if !ok || payloadTy != scalarString {
		return false
	}
	typeName, ok := ctx.mctx.emitOptionBoxDef(payloadTy)
	if !ok {
		return false
	}
	binding, ok := ctx.bindings[optID]
	if !ok || !binding.defined || binding.ty != scalarOpaquePtr {
		return false
	}
	optExpr := binding.expr
	if binding.isStack {
		loaded, ty, ok := loadFromStack(ctx, out, optID)
		if !ok || ty != scalarOpaquePtr {
			return false
		}
		optExpr = loaded
	}
	payloadSlot := freshReg(ctx)
	payload := freshReg(ctx)
	tagSlot := freshReg(ctx)
	tag := freshReg(ctx)
	isSome := freshReg(ctx)
	empty := ctx.mctx.internStringConst("")
	result := freshReg(ctx)
	fmt.Fprintf(out, "  %s = getelementptr inbounds %%%s, ptr %s, i32 0, i32 1\n", payloadSlot, typeName, optExpr)
	fmt.Fprintf(out, "  %s = load %s, ptr %s\n", payload, payloadTy.llvm(), payloadSlot)
	fmt.Fprintf(out, "  %s = getelementptr inbounds %%%s, ptr %s, i32 0, i32 0\n", tagSlot, typeName, optExpr)
	fmt.Fprintf(out, "  %s = load i64, ptr %s\n", tag, tagSlot)
	fmt.Fprintf(out, "  %s = icmp eq i64 %s, 0\n", isSome, tag)
	fmt.Fprintf(out, "  %s = select i1 %s, %s %s, %s %s\n", result, isSome, payloadTy.llvm(), payload, payloadTy.llvm(), empty)
	fmt.Fprintf(out, "  ret %s %s\n", payloadTy.llvm(), result)
	return true
}

func emitSyntheticXReturn(
	ctx *whileLoopEmitCtx, out *strings.Builder, payload mir.LocalID, retType scalarType,
) bool {
	if ctx == nil || out == nil || ctx.fn == nil || ctx.mctx == nil {
		return false
	}
	if retType == scalarUnknown {
		return false
	}
	loc := lookupLocal(ctx.fn, payload)
	if loc == nil {
		return false
	}
	payloadTy, ok := optionPayloadScalar(loc.Type, ctx.mctx)
	if !ok || payloadTy != retType {
		return false
	}
	typeName, ok := ctx.mctx.emitOptionBoxDef(payloadTy)
	if !ok {
		return false
	}
	binding, ok := ctx.bindings[payload]
	if !ok || !binding.defined || binding.ty != scalarOpaquePtr {
		return false
	}
	optExpr := binding.expr
	if binding.isStack {
		loaded, ty, ok := loadFromStack(ctx, out, payload)
		if !ok || ty != scalarOpaquePtr {
			return false
		}
		optExpr = loaded
	}
	payloadSlot := freshReg(ctx)
	payloadReg := freshReg(ctx)
	fmt.Fprintf(out, "  %s = getelementptr inbounds %%%s, ptr %s, i32 0, i32 1\n", payloadSlot, typeName, optExpr)
	fmt.Fprintf(out, "  %s = load %s, ptr %s\n", payloadReg, payloadTy.llvm(), payloadSlot)
	fmt.Fprintf(out, "  ret %s %s\n", payloadTy.llvm(), payloadReg)
	return true
}

func emitXAsReturn(ctx *whileLoopEmitCtx, out *strings.Builder, payload mir.LocalID, retType scalarType) bool {
	if ctx == nil || out == nil {
		return false
	}
	if retType == scalarUnknown {
		return false
	}
	loc := lookupLocal(ctx.fn, payload)
	if loc == nil {
		return false
	}
	payloadTy, ok := optionPayloadScalar(loc.Type, ctx.mctx)
	if !ok || payloadTy != retType {
		return false
	}
	typeName, ok := ctx.mctx.emitOptionBoxDef(payloadTy)
	if !ok {
		return false
	}
	binding, ok := ctx.bindings[payload]
	if !ok || !binding.defined || binding.ty != scalarOpaquePtr {
		return false
	}
	optExpr := binding.expr
	if binding.isStack {
		loaded, ty, ok := loadFromStack(ctx, out, payload)
		if !ok || ty != scalarOpaquePtr {
			return false
		}
		optExpr = loaded
	}
	payloadSlot := freshReg(ctx)
	payloadReg := freshReg(ctx)
	fmt.Fprintf(out, "  %s = getelementptr inbounds %%%s, ptr %s, i32 0, i32 1\n", payloadSlot, typeName, optExpr)
	fmt.Fprintf(out, "  %s = load %s, ptr %s\n", payloadReg, payloadTy.llvm(), payloadSlot)
	fmt.Fprintf(out, "  ret %s %s\n", payloadTy.llvm(), payloadReg)
	return true
}

func isStorageInstr(instr mir.Instr) bool {
	switch instr.(type) {
	case *mir.StorageLiveInstr, *mir.StorageDeadInstr:
		return true
	default:
		return false
	}
}

func assignSrcIsIntConst(src mir.RValue, want int64) bool {
	use, ok := src.(*mir.UseRV)
	if !ok {
		return false
	}
	con, ok := use.Op.(*mir.ConstOp)
	if !ok {
		return false
	}
	ic, ok := con.Const.(*mir.IntConst)
	return ok && ic.Value == want
}

func assignSrcIsSelfIntStep(src mir.RValue, id mir.LocalID) bool {
	bin, ok := src.(*mir.BinaryRV)
	if !ok || bin.Op != mir.BinAdd {
		return false
	}
	return operandIsPlainLocal(bin.Left, id) && operandIsIntConst(bin.Right) ||
		operandIsPlainLocal(bin.Right, id) && operandIsIntConst(bin.Left)
}

func operandIsPlainLocal(op mir.Operand, id mir.LocalID) bool {
	cp, ok := op.(*mir.CopyOp)
	return ok && !cp.Place.HasProjections() && cp.Place.Local == id
}

func operandIsIntConst(op mir.Operand) bool {
	con, ok := op.(*mir.ConstOp)
	if !ok {
		return false
	}
	_, ok = con.Const.(*mir.IntConst)
	return ok
}

func genericLocalUsedAsIndex(fn *mir.Function, id mir.LocalID) bool {
	if fn == nil {
		return false
	}
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		for _, instr := range bb.Instrs {
			if instrUsesLocalAsIndex(instr, id) {
				return true
			}
		}
	}
	return false
}

func instrUsesLocalAsIndex(instr mir.Instr, id mir.LocalID) bool {
	switch step := instr.(type) {
	case *mir.AssignInstr:
		return placeUsesLocalAsIndex(step.Dest, id) || rvalueUsesLocalAsIndex(step.Src, id)
	case *mir.CallInstr:
		if step.Dest != nil && placeUsesLocalAsIndex(*step.Dest, id) {
			return true
		}
		for _, arg := range step.Args {
			if operandUsesLocalAsIndex(arg, id) {
				return true
			}
		}
	case *mir.IntrinsicInstr:
		if step.Dest != nil && placeUsesLocalAsIndex(*step.Dest, id) {
			return true
		}
		for _, arg := range step.Args {
			if operandUsesLocalAsIndex(arg, id) {
				return true
			}
		}
	}
	return false
}

func rvalueUsesLocalAsIndex(rv mir.RValue, id mir.LocalID) bool {
	switch r := rv.(type) {
	case *mir.UseRV:
		return operandUsesLocalAsIndex(r.Op, id)
	case *mir.UnaryRV:
		return operandUsesLocalAsIndex(r.Arg, id)
	case *mir.BinaryRV:
		return operandUsesLocalAsIndex(r.Left, id) || operandUsesLocalAsIndex(r.Right, id)
	case *mir.AggregateRV:
		for _, field := range r.Fields {
			if operandUsesLocalAsIndex(field, id) {
				return true
			}
		}
	case *mir.DiscriminantRV:
		return placeUsesLocalAsIndex(r.Place, id)
	case *mir.LenRV:
		return placeUsesLocalAsIndex(r.Place, id)
	case *mir.CastRV:
		return operandUsesLocalAsIndex(r.Arg, id)
	case *mir.AddressOfRV:
		return placeUsesLocalAsIndex(r.Place, id)
	case *mir.RefRV:
		return placeUsesLocalAsIndex(r.Place, id)
	}
	return false
}

func genericLocalUsedInBranchCondition(fn *mir.Function, id mir.LocalID) bool {
	if fn == nil {
		return false
	}
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		branch, ok := bb.Term.(*mir.BranchTerm)
		if !ok {
			continue
		}
		if operandMentionsLocal(branch.Cond, id) {
			return true
		}
		condLocal, ok := copyOperandLocal(branch.Cond)
		if !ok {
			continue
		}
		for _, instr := range bb.Instrs {
			ai, ok := instr.(*mir.AssignInstr)
			if !ok || ai.Dest.HasProjections() || ai.Dest.Local != condLocal {
				continue
			}
			if rvalueMentionsLocal(ai.Src, id) {
				return true
			}
		}
	}
	return false
}

func operandUsesLocalAsIndex(op mir.Operand, id mir.LocalID) bool {
	cp, ok := op.(*mir.CopyOp)
	if !ok {
		return false
	}
	return placeUsesLocalAsIndex(cp.Place, id)
}

func placeUsesLocalAsIndex(place mir.Place, id mir.LocalID) bool {
	for _, proj := range place.Projections {
		if idx, ok := proj.(*mir.IndexProj); ok && operandMentionsLocal(idx.Index, id) {
			return true
		}
	}
	return false
}

func genericInferStringJoinSyntheticReturn(fn *mir.Function) (mir.BlockID, *mir.IntrinsicInstr, bool) {
	if fn == nil {
		return 0, nil, false
	}
	var exitID mir.BlockID
	var exitJoin *mir.IntrinsicInstr
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		if _, unreachable := bb.Term.(*mir.UnreachableTerm); !unreachable {
			continue
		}
		var join *mir.IntrinsicInstr
		valid := true
		for _, instr := range bb.Instrs {
			switch step := instr.(type) {
			case *mir.StorageLiveInstr, *mir.StorageDeadInstr:
				continue
			case *mir.IntrinsicInstr:
				if step.Kind != mir.IntrinsicStringJoin || step.Dest != nil || len(step.Args) != 2 || join != nil {
					valid = false
					break
				}
				join = step
			default:
				valid = false
			}
			if !valid {
				break
			}
		}
		if !valid || join == nil {
			continue
		}
		if exitJoin != nil {
			return 0, nil, false
		}
		exitID = bb.ID
		exitJoin = join
	}
	if exitJoin == nil {
		return 0, nil, false
	}
	return exitID, exitJoin, true
}

func emitSyntheticStringJoinReturn(ctx *whileLoopEmitCtx, out *strings.Builder, ii *mir.IntrinsicInstr) bool {
	if ii == nil || ii.Kind != mir.IntrinsicStringJoin || ii.Dest != nil {
		return false
	}
	spec, ok := intrinsicRuntimeCallSpec(ii.Kind)
	if !ok || spec.ret != scalarString || len(spec.args) != len(ii.Args) {
		return false
	}
	args, ok := resolveWhileFixedScalarArgs(ctx, out, ii.Args, spec.args)
	if !ok {
		return false
	}
	declareRuntimePrototype(ctx.mctx, spec.symbol, spec.ret, args)
	reg := freshReg(ctx)
	fmt.Fprintf(out, "  %s = call ptr @%s(ptr %s, ptr %s)\n", reg, spec.symbol, args[0].expr, args[1].expr)
	fmt.Fprintf(out, "  ret ptr %s\n", reg)
	return true
}

func genericInferStringAccumulatorSyntheticReturn(fn *mir.Function, mctx *moduleCtx) (mir.BlockID, mir.LocalID, bool) {
	if fn == nil || mctx == nil || mctx.scalarFromType(fn.ReturnType, true) != scalarString {
		return 0, 0, false
	}
	exit, ok := genericStorageOnlyUnreachableExit(fn)
	if !ok {
		return 0, 0, false
	}
	init := map[mir.LocalID]bool{}
	selfUpdate := map[mir.LocalID]bool{}
	concatMentions := map[mir.LocalID]map[mir.LocalID]bool{}
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		if _, unreachable := bb.Term.(*mir.UnreachableTerm); unreachable {
			continue
		}
		for _, instr := range bb.Instrs {
			ii, ok := instr.(*mir.IntrinsicInstr)
			if !ok || ii.Dest == nil || ii.Dest.HasProjections() || ii.Kind != mir.IntrinsicStringConcat {
				continue
			}
			mentioned := map[mir.LocalID]bool{}
			for _, arg := range ii.Args {
				if cp, ok := arg.(*mir.CopyOp); ok && !cp.Place.HasProjections() {
					if loc := lookupLocal(fn, cp.Place.Local); loc != nil && isPrimType(loc.Type, ir.PrimString) {
						mentioned[cp.Place.Local] = true
					}
				}
			}
			if len(mentioned) > 0 {
				concatMentions[ii.Dest.Local] = mentioned
			}
		}
	}
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		if _, unreachable := bb.Term.(*mir.UnreachableTerm); unreachable {
			continue
		}
		for _, instr := range bb.Instrs {
			switch step := instr.(type) {
			case *mir.AssignInstr:
				if step.Dest.HasProjections() {
					continue
				}
				loc := lookupLocal(fn, step.Dest.Local)
				if loc == nil || !isPrimType(loc.Type, ir.PrimString) {
					continue
				}
				if assignSrcIsStringSelfStep(step.Src, step.Dest.Local, concatMentions) || assignSrcIsStringBinarySelfStep(step.Src, step.Dest.Local) {
					selfUpdate[step.Dest.Local] = true
				} else {
					init[step.Dest.Local] = true
				}
			case *mir.IntrinsicInstr:
				if step.Dest == nil || step.Dest.HasProjections() || step.Kind != mir.IntrinsicStringConcat {
					continue
				}
				if concatOperandsMentionPlainLocal(step.Args, step.Dest.Local) {
					selfUpdate[step.Dest.Local] = true
				}
			}
		}
	}
	var candidate mir.LocalID
	for _, local := range fn.Locals {
		if local == nil || local.IsParam || local.ID == fn.ReturnLocal {
			continue
		}
		if !isPrimType(local.Type, ir.PrimString) {
			continue
		}
		if !init[local.ID] || !selfUpdate[local.ID] {
			continue
		}
		if candidate != 0 {
			return 0, 0, false
		}
		candidate = local.ID
	}
	if candidate == 0 {
		return 0, 0, false
	}
	return exit.ID, candidate, true
}

func genericInferStringNamedLocalSyntheticReturn(fn *mir.Function, mctx *moduleCtx) (mir.BlockID, mir.LocalID, bool) {
	if fn == nil || mctx == nil || mctx.scalarFromType(fn.ReturnType, true) != scalarString {
		return 0, 0, false
	}
	exit, ok := genericStorageOnlyUnreachableExit(fn)
	if !ok {
		return 0, 0, false
	}
	named := []mir.LocalID{}
	for _, loc := range fn.Locals {
		if loc == nil || loc.IsParam || loc.ID == fn.ReturnLocal {
			continue
		}
		if loc.Name == "" || loc.Name == "_return" {
			continue
		}
		if mctx.scalarFromType(loc.Type, true) != scalarString {
			continue
		}
		named = append(named, loc.ID)
	}
	if len(named) != 1 {
		return 0, 0, false
	}
	target := named[0]
	assigned := false
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		if _, unreachable := bb.Term.(*mir.UnreachableTerm); unreachable {
			continue
		}
		for _, instr := range bb.Instrs {
			ai, ok := instr.(*mir.AssignInstr)
			if !ok || ai.Dest.HasProjections() || ai.Dest.Local != target {
				continue
			}
			assigned = true
			break
		}
		if assigned {
			break
		}
	}
	if !assigned {
		return 0, 0, false
	}
	return exit.ID, target, true
}

func assignSrcIsStringSelfStep(src mir.RValue, id mir.LocalID, concatMentions map[mir.LocalID]map[mir.LocalID]bool) bool {
	if src == nil {
		return false
	}
	use, ok := src.(*mir.UseRV)
	if !ok {
		return false
	}
	cp, ok := use.Op.(*mir.CopyOp)
	if !ok || cp.Place.HasProjections() {
		return false
	}
	return concatMentions[cp.Place.Local][id]
}

func assignSrcIsStringBinarySelfStep(src mir.RValue, id mir.LocalID) bool {
	bin, ok := src.(*mir.BinaryRV)
	if !ok || bin.Op != mir.BinAdd {
		return false
	}
	return operandIsPlainLocal(bin.Left, id) || operandIsPlainLocal(bin.Right, id)
}

func concatOperandsMentionPlainLocal(args []mir.Operand, id mir.LocalID) bool {
	for _, arg := range args {
		if operandIsPlainLocal(arg, id) {
			return true
		}
	}
	return false
}

func genericStorageOnlyUnreachableExit(fn *mir.Function) (*mir.BasicBlock, bool) {
	var exit *mir.BasicBlock
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		if _, unreachable := bb.Term.(*mir.UnreachableTerm); !unreachable {
			continue
		}
		if !blockHasOnlyStorageMarkers(bb) {
			continue
		}
		if exit != nil {
			return nil, false
		}
		exit = bb
	}
	return exit, exit != nil
}

type PayloadType struct {
	LocalID   mir.LocalID
	CallInstr *mir.CallInstr
	Instr     *mir.IntrinsicInstr
	Kind      payloadKind
}

type payloadKind int

const (
	payloadNone payloadKind = iota
	payloadLocal
	payloadCall
	payloadIntrinsic
)

func isRecoverableReturnShape(fn *mir.Function) bool {
	if fn == nil {
		return false
	}
	if fn.ReturnType == nil {
		return false
	}
	switch fn.ReturnType.(type) {
	case *ir.NamedType:
		return true
	default:
		return false
	}
}

func genericInferXSyntheticReturn(fn *mir.Function, mctx *moduleCtx, retType scalarType) (mir.BlockID, PayloadType, bool) {
	if fn == nil || mctx == nil {
		return 0, PayloadType{}, false
	}
	if !isRecoverableReturnShape(fn) {
		return 0, PayloadType{}, false
	}
	exit, ok := genericStorageOnlyUnreachableExit(fn)
	if !ok {
		return 0, PayloadType{}, false
	}
	candidates := map[mir.LocalID]bool{}
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		if _, unreachable := bb.Term.(*mir.UnreachableTerm); unreachable {
			continue
		}
		for _, instr := range bb.Instrs {
			ai, ok := instr.(*mir.AssignInstr)
			if !ok || ai.Dest.HasProjections() {
				continue
			}
			loc := lookupLocal(fn, ai.Dest.Local)
			if loc == nil || !sameTypeString(loc.Type, fn.ReturnType) {
				continue
			}
			if ai.Src != nil {
				candidates[ai.Dest.Local] = true
			}
		}
	}
	if len(candidates) == 0 {
		return 0, PayloadType{}, false
	}
	var selected mir.LocalID
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		if _, unreachable := bb.Term.(*mir.UnreachableTerm); unreachable {
			continue
		}
		for _, instr := range bb.Instrs {
			switch step := instr.(type) {
			case *mir.AssignInstr:
				if step.Dest.HasProjections() {
					continue
				}
				if candidates[step.Dest.Local] {
					if selected != 0 && selected != step.Dest.Local {
						return 0, PayloadType{}, false
					}
					selected = step.Dest.Local
				}
			case *mir.CallInstr:
				if step.Dest != nil && !step.Dest.HasProjections() && candidates[step.Dest.Local] {
					if selected != 0 && selected != step.Dest.Local {
						return 0, PayloadType{}, false
					}
					selected = step.Dest.Local
				}
			}
		}
	}
	if selected == 0 {
		return 0, PayloadType{}, false
	}
	return exit.ID, PayloadType{LocalID: selected, Kind: payloadLocal}, true
}

func isNamedListType(t mir.Type) bool {
	named, ok := t.(*ir.NamedType)
	return ok && named != nil && named.Name == "List"
}

func sameTypeString(a, b mir.Type) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.String() == b.String()
}

func genericLocalUsedOutsideUnreachableBlocks(fn *mir.Function, id mir.LocalID) bool {
	if fn == nil {
		return false
	}
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		if _, unreachable := bb.Term.(*mir.UnreachableTerm); unreachable {
			continue
		}
		if genericBlockMentionsLocal(bb, id) {
			return true
		}
	}
	return false
}

func genericTupleLocalIsAggregateOnly(fn *mir.Function, id mir.LocalID) bool {
	if fn == nil {
		return false
	}
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		if _, unreachable := bb.Term.(*mir.UnreachableTerm); unreachable {
			continue
		}
		for _, instr := range bb.Instrs {
			switch step := instr.(type) {
			case *mir.AssignInstr:
				if placeMentionsLocal(step.Dest, id) {
					return false
				}
				if !genericTupleRValueUsesLocalSafely(step.Src, id) {
					return false
				}
			case *mir.CallInstr:
				if step.Dest != nil && step.Dest.Local == id {
					if step.Dest.HasProjections() {
						return false
					}
					continue
				}
				for _, arg := range step.Args {
					if !genericTupleOperandUsesLocalSafely(arg, id) {
						return false
					}
				}
			case *mir.IntrinsicInstr:
				if step.Dest != nil && placeMentionsLocal(*step.Dest, id) {
					return false
				}
				for _, arg := range step.Args {
					if !genericTupleOperandUsesLocalSafely(arg, id) {
						return false
					}
				}
			case *mir.StorageLiveInstr, *mir.StorageDeadInstr:
			default:
				if instrMentionsLocal(instr, id) {
					return false
				}
			}
		}
		if termMentionsLocal(bb.Term, id) {
			return false
		}
	}
	return true
}

func genericTupleRValueUsesLocalSafely(rv mir.RValue, id mir.LocalID) bool {
	switch r := rv.(type) {
	case *mir.UseRV:
		return genericTupleOperandUsesLocalSafely(r.Op, id)
	case *mir.UnaryRV:
		return genericTupleOperandUsesLocalSafely(r.Arg, id)
	case *mir.BinaryRV:
		return genericTupleOperandUsesLocalSafely(r.Left, id) && genericTupleOperandUsesLocalSafely(r.Right, id)
	case *mir.AggregateRV:
		for _, field := range r.Fields {
			if !genericTupleOperandUsesLocalSafely(field, id) {
				return false
			}
		}
		return true
	case *mir.DiscriminantRV:
		return genericTuplePlaceUsesLocalSafely(r.Place, id)
	case *mir.LenRV:
		return genericTuplePlaceUsesLocalSafely(r.Place, id)
	case *mir.CastRV:
		return genericTupleOperandUsesLocalSafely(r.Arg, id)
	case *mir.AddressOfRV:
		return genericTuplePlaceUsesLocalSafely(r.Place, id)
	case *mir.RefRV:
		return genericTuplePlaceUsesLocalSafely(r.Place, id)
	default:
		return !rvalueMentionsLocal(rv, id)
	}
}

func genericTupleOperandUsesLocalSafely(op mir.Operand, id mir.LocalID) bool {
	cp, ok := op.(*mir.CopyOp)
	if !ok {
		return true
	}
	return genericTuplePlaceUsesLocalSafely(cp.Place, id)
}

func genericTuplePlaceUsesLocalSafely(place mir.Place, id mir.LocalID) bool {
	if place.Local == id {
		if len(place.Projections) == 0 {
			return false
		}
		for _, proj := range place.Projections {
			if _, ok := proj.(*mir.TupleProj); !ok {
				return false
			}
		}
		return true
	}
	for _, proj := range place.Projections {
		if idx, ok := proj.(*mir.IndexProj); ok && !genericTupleOperandUsesLocalSafely(idx.Index, id) {
			return false
		}
	}
	return true
}

func genericBlockMentionsLocal(bb *mir.BasicBlock, id mir.LocalID) bool {
	if bb == nil {
		return false
	}
	for _, instr := range bb.Instrs {
		if instrMentionsLocal(instr, id) {
			return true
		}
	}
	return termMentionsLocal(bb.Term, id)
}

func instrMentionsLocal(instr mir.Instr, id mir.LocalID) bool {
	switch step := instr.(type) {
	case *mir.AssignInstr:
		return placeMentionsLocal(step.Dest, id) || rvalueMentionsLocal(step.Src, id)
	case *mir.CallInstr:
		if step.Dest != nil && placeMentionsLocal(*step.Dest, id) {
			return true
		}
		for _, arg := range step.Args {
			if operandMentionsLocal(arg, id) {
				return true
			}
		}
	case *mir.IntrinsicInstr:
		if step.Dest != nil && placeMentionsLocal(*step.Dest, id) {
			return true
		}
		for _, arg := range step.Args {
			if operandMentionsLocal(arg, id) {
				return true
			}
		}
	case *mir.StorageLiveInstr:
		return step.Local == id
	case *mir.StorageDeadInstr:
		return step.Local == id
	}
	return false
}

func rvalueMentionsLocal(rv mir.RValue, id mir.LocalID) bool {
	switch r := rv.(type) {
	case *mir.UseRV:
		return operandMentionsLocal(r.Op, id)
	case *mir.UnaryRV:
		return operandMentionsLocal(r.Arg, id)
	case *mir.BinaryRV:
		return operandMentionsLocal(r.Left, id) || operandMentionsLocal(r.Right, id)
	case *mir.AggregateRV:
		for _, field := range r.Fields {
			if operandMentionsLocal(field, id) {
				return true
			}
		}
	case *mir.DiscriminantRV:
		return placeMentionsLocal(r.Place, id)
	case *mir.LenRV:
		return placeMentionsLocal(r.Place, id)
	case *mir.CastRV:
		return operandMentionsLocal(r.Arg, id)
	case *mir.AddressOfRV:
		return placeMentionsLocal(r.Place, id)
	case *mir.RefRV:
		return placeMentionsLocal(r.Place, id)
	}
	return false
}

func termMentionsLocal(term mir.Terminator, id mir.LocalID) bool {
	switch t := term.(type) {
	case *mir.BranchTerm:
		return operandMentionsLocal(t.Cond, id)
	case *mir.SwitchIntTerm:
		return operandMentionsLocal(t.Scrutinee, id)
	}
	return false
}

func operandMentionsLocal(op mir.Operand, id mir.LocalID) bool {
	cp, ok := op.(*mir.CopyOp)
	if !ok {
		return false
	}
	return placeMentionsLocal(cp.Place, id)
}

func placeMentionsLocal(place mir.Place, id mir.LocalID) bool {
	if place.Local == id {
		return true
	}
	for _, proj := range place.Projections {
		if idx, ok := proj.(*mir.IndexProj); ok && operandMentionsLocal(idx.Index, id) {
			return true
		}
	}
	return false
}

func genericSingleBlockHasExtendedSurface(fn *mir.Function, bb *mir.BasicBlock) bool {
	if bb == nil {
		return false
	}
	stringAssigns := map[mir.LocalID]int{}
	for _, instr := range bb.Instrs {
		switch step := instr.(type) {
		case *mir.CallInstr, *mir.IntrinsicInstr:
			return true
		case *mir.AssignInstr:
			if !step.Dest.HasProjections() {
				if loc := lookupLocal(fn, step.Dest.Local); loc != nil && !loc.IsParam && isPrimType(loc.Type, ir.PrimString) {
					stringAssigns[step.Dest.Local]++
					if stringAssigns[step.Dest.Local] > 1 {
						return true
					}
				}
			}
			if step.Dest.HasProjections() {
				return true
			}
			switch src := step.Src.(type) {
			case *mir.AggregateRV, *mir.DiscriminantRV, *mir.LenRV, *mir.NullaryRV:
				return true
			case *mir.UseRV:
				if copyOperandHasProjection(src.Op) {
					return true
				}
			case *mir.BinaryRV:
				if copyOperandHasProjection(src.Left) || copyOperandHasProjection(src.Right) {
					return true
				}
			case *mir.UnaryRV:
				if copyOperandHasProjection(src.Arg) {
					return true
				}
			}
		}
	}
	return false
}

func copyOperandHasProjection(op mir.Operand) bool {
	cp, ok := op.(*mir.CopyOp)
	return ok && cp.Place.HasProjections()
}

func genericHasReturnTerm(blocks []*mir.BasicBlock) bool {
	for _, bb := range blocks {
		if bb == nil {
			continue
		}
		if _, ok := bb.Term.(*mir.ReturnTerm); ok {
			return true
		}
	}
	return false
}

func genericBlockOrder(fn *mir.Function) []*mir.BasicBlock {
	ordered := make([]*mir.BasicBlock, 0, len(fn.Blocks))
	seen := map[mir.BlockID]bool{}
	if entry := blockByID(fn, fn.Entry); entry != nil {
		ordered = append(ordered, entry)
		seen[entry.ID] = true
	}
	for _, bb := range fn.Blocks {
		if bb == nil || seen[bb.ID] {
			continue
		}
		ordered = append(ordered, bb)
		seen[bb.ID] = true
	}
	return ordered
}

func emitGenericTerm(ctx *whileLoopEmitCtx, out *strings.Builder, term mir.Terminator, retType scalarType, returnsVoid bool) bool {
	switch t := term.(type) {
	case *mir.ReturnTerm:
		if returnsVoid {
			out.WriteString("  ret void\n")
			return true
		}
		binding, ok := ctx.bindings[ctx.fn.ReturnLocal]
		if !ok || !binding.defined || binding.ty != retType {
			return false
		}
		expr := binding.expr
		if binding.isStack {
			loaded, ty, ok := loadFromStack(ctx, out, ctx.fn.ReturnLocal)
			if !ok || ty != retType {
				return false
			}
			expr = loaded
		}
		fmt.Fprintf(out, "  ret %s %s\n", retType.llvm(), expr)
		return true
	case *mir.GotoTerm:
		fmt.Fprintf(out, "  br label %%%s\n", genericBlockLabel(ctx.fn, t.Target))
		return true
	case *mir.BranchTerm:
		cond, ty, ok := resolveOperandWithLoad(ctx, out, t.Cond)
		if !ok || ty != scalarBool {
			return false
		}
		fmt.Fprintf(out, "  br i1 %s, label %%%s, label %%%s\n", cond, genericBlockLabel(ctx.fn, t.Then), genericBlockLabel(ctx.fn, t.Else))
		return true
	case *mir.SwitchIntTerm:
		scrutinee, ty, ok := resolveOperandWithLoad(ctx, out, t.Scrutinee)
		if !ok || ty != scalarInt {
			return false
		}
		fmt.Fprintf(out, "  switch i64 %s, label %%%s [\n", scrutinee, genericBlockLabel(ctx.fn, t.Default))
		for _, c := range t.Cases {
			fmt.Fprintf(out, "    i64 %d, label %%%s\n", c.Value, genericBlockLabel(ctx.fn, c.Target))
		}
		out.WriteString("  ]\n")
		return true
	case *mir.UnreachableTerm:
		out.WriteString("  unreachable\n")
		return true
	}
	return false
}

func emitGenericScalarCFG(out *strings.Builder, fn *mir.Function, pat genericCFGPattern) error {
	retLLVM := "void"
	if !pat.returnsVoid {
		retLLVM = pat.retType.llvm()
	}

	if pat.aggRetTypeName != "" {
		// Aggregate return: emit sret parameter.
		fmt.Fprintf(out, "define void @%s(ptr sret(%%%s) %%sret.result", fn.Name, pat.aggRetTypeName)
		for i, name := range pat.paramNames {
			fmt.Fprintf(out, ", %s %%%s", pat.paramTypes[i].llvm(), name)
		}
		out.WriteString(") {\n")
	} else {
		fmt.Fprintf(out, "define %s @%s(", retLLVM, fn.Name)
		for i, name := range pat.paramNames {
			if i > 0 {
				out.WriteString(", ")
			}
			fmt.Fprintf(out, "%s %%%s", pat.paramTypes[i].llvm(), name)
		}
		out.WriteString(") {\n")
	}
	for i, id := range pat.blockOrder {
		if i > 0 {
			out.WriteString("\n")
		}
		fmt.Fprintf(out, "%s:\n", genericBlockLabel(fn, id))
		if id == fn.Entry {
			for _, sd := range pat.stackDecls {
				fmt.Fprintf(out, "  %%%s = alloca %s\n", sd.name, sd.ty.llvm())
			}
		}
		out.WriteString(pat.blockBodies[id])
	}
	out.WriteString("}\n\n")
	return nil
}

func genericBlockLabel(fn *mir.Function, id mir.BlockID) string {
	if fn != nil && id == fn.Entry {
		return "entry"
	}
	return blockLabelName(id, "bb")
}
