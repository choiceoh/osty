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
//   - Sequential single-block return: zero or more AssignInstrs (each
//     writing to a unique scalar local) followed by ReturnTerm.
//     Up to two Int / Bool parameters. Each AssignInstr's source is
//     either:
//
//       UseRV { ConstOp(IntConst | BoolConst) }
//       UseRV { CopyOp(local) }            // local must already be defined
//       BinaryRV { op, operand, operand }  // op family classified below
//
//     where each operand is a const literal or a CopyOp of a
//     previously-defined scalar local (param or earlier assignment).
//     Operator families:
//
//       arithmetic Int×Int → Int : Add Sub Mul Div Mod
//       comparison Int×Int → Bool: Eq Neq Lt Leq Gt Geq
//       bitwise    Int×Int → Int : BitAnd BitOr BitXor Shl Shr
//       logical    Bool×Bool→ Bool: And Or
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
	// format string land in extraDecls before any function body.
	if scanNeedsPrintlnInt(module) {
		mctx.extraDecls.WriteString("@.fmt.stage0.println.int = private unnamed_addr constant [6 x i8] c\"%lld\\0A\\00\"\n")
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
	knownSymbols map[string]bool
	// extraDecls collects module-level lines (declares + globals)
	// the matchers emit on demand. Concatenated into the final
	// output before function bodies.
	extraDecls *strings.Builder
	// stringPool maps string-constant value → assigned global
	// symbol (without the leading `@`). Lookups are write-once: the
	// first reference allocates a fresh `@.str.<N>` global.
	stringPool   map[string]string
	nextStringID int
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
		knownSymbols: syms,
		extraDecls:   &strings.Builder{},
		stringPool:   map[string]string{},
	}
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

// scanNeedsPrintlnInt reports whether any function in `module`
// invokes `println(Int)` (the IntrinsicPrintln intrinsic with a
// single Int-typed argument). The result drives whether stage0
// emits the `@printf` declaration + format string at module top.
func scanNeedsPrintlnInt(module *mir.Module) bool {
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
				if scalarFromType(intr.Args[0].Type()) == scalarInt {
					return true
				}
			}
		}
	}
	return false
}

func emitFunction(out *strings.Builder, fn *mir.Function, mctx *moduleCtx) error {
	if fn.IsIntrinsic {
		return fmt.Errorf("%w: intrinsic declaration %q", ErrUnsupported, fn.Name)
	}
	if fn.IsExternal {
		return fmt.Errorf("%w: external declaration %q", ErrUnsupported, fn.Name)
	}
	if fn.Name == "main" {
		return emitTrivialMain(out, fn)
	}
	if pat, ok := matchSequentialReturn(fn, mctx); ok {
		return emitSequentialReturn(out, fn, pat)
	}
	if pat, ok := matchIfElseReturn(fn, mctx); ok {
		return emitIfElseReturn(out, fn, pat)
	}
	if pat, ok := matchWhileLoopReturn(fn, mctx); ok {
		return emitWhileLoopReturn(out, fn, pat)
	}
	if pat, ok := matchForInRangeReturn(fn, mctx); ok {
		return emitForInRangeReturn(out, fn, pat)
	}
	return fmt.Errorf("%w: function %q does not match any stage0 pattern", ErrUnsupported, fn.Name)
}

// ---- P1: trivial main ----

func emitTrivialMain(out *strings.Builder, fn *mir.Function) error {
	if reason := trivialMainViolation(fn); reason != "" {
		return fmt.Errorf("%w: function %q: %s", ErrUnsupported, fn.Name, reason)
	}
	out.WriteString("define i32 @main() {\n")
	out.WriteString("entry:\n")
	out.WriteString("  ret i32 0\n")
	out.WriteString("}\n\n")
	return nil
}

// trivialMainViolation accepts the canonical empty-body shape produced
// by the front-end for `fn main() {}`. The body may contain zero or
// one AssignInstr that writes a UnitConst to the return local — the
// front-end emits that exact instruction even when the source body is
// empty, since `()` is the implicit return value.
func trivialMainViolation(fn *mir.Function) string {
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
	switch len(bb.Instrs) {
	case 0:
		// Bare empty body — accept.
	case 1:
		if !isUnitAssignToReturnLocal(bb.Instrs[0], fn.ReturnLocal) {
			return fmt.Sprintf("main entry block has 1 instruction (%T); stage0 expects an empty body or a single Unit assignment", bb.Instrs[0])
		}
	default:
		return fmt.Sprintf("main entry block has %d instructions; stage0 expects 0 or 1", len(bb.Instrs))
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
	}
	return ""
}

func scalarFromType(t mir.Type) scalarType {
	prim, ok := t.(*ir.PrimType)
	if !ok || prim == nil {
		return scalarUnknown
	}
	switch prim.Kind {
	case ir.PrimString:
		return scalarString
	case ir.PrimInt:
		return scalarInt
	case ir.PrimBool:
		return scalarBool
	}
	return scalarUnknown
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
	instrIntrinsic
)

// classifyIntrinsicLine pre-renders the LLVM line for an
// IntrinsicInstr that doesn't bind a local (e.g., println). Returns
// (line, true) on success; declines for unsupported intrinsics or
// shape mismatches. Operand resolution uses the SSA-only `bindings`
// path — stack-backed locals are not visible here (they live in the
// while-loop matcher).
func classifyIntrinsicLine(ii *mir.IntrinsicInstr, bindings map[mir.LocalID]localBinding, mctx *moduleCtx) (string, bool) {
	if ii.Dest != nil {
		return "", false
	}
	switch ii.Kind {
	case mir.IntrinsicPrintln:
		if len(ii.Args) != 1 {
			return "", false
		}
		expr, ty, ok := resolveOperand(ii.Args[0], bindings, mctx)
		if !ok || ty != scalarInt {
			return "", false
		}
		return fmt.Sprintf("  call i32 (ptr, ...) @printf(ptr @.fmt.stage0.println.int, i64 %s)\n", expr), true
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
	pat.retType = scalarFromType(fn.ReturnType)
	if pat.retType == scalarUnknown {
		return pat, false
	}
	if len(fn.Params) > 2 {
		return pat, false
	}

	bindings := map[mir.LocalID]localBinding{}

	// Seed param bindings so subsequent CopyOp resolution works.
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
			pending pendingInstr
			expr    string
			destID  mir.LocalID
			destType scalarType
			okStep  bool
		)
		switch step := instr.(type) {
		case *mir.AssignInstr:
			pending, expr, destID, destType, okStep = classifyAssignStep(fn, step, bindings, mctx)
		case *mir.CallInstr:
			pending, destID, destType, okStep = classifyCallStep(fn, step, bindings, mctx)
		case *mir.IntrinsicInstr:
			line, okIntr := classifyIntrinsicLine(step, bindings, mctx)
			if !okIntr {
				return pat, false
			}
			pat.pending = append(pat.pending, pendingInstr{kind: instrIntrinsic, intrinsicLine: line})
			continue
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
		case instrBinary, instrCall:
			reg := fmt.Sprintf("%%%d", nextSSA)
			nextSSA++
			pending.binDestReg = reg
			bindings[destID] = localBinding{expr: reg, ty: destType, defined: true}
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
	destType := scalarFromType(destLocal.Type)
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
	destID := ci.Dest.Local
	destLocal := lookupLocal(fn, destID)
	if destLocal == nil {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	destType := scalarFromType(destLocal.Type)
	if destType == scalarUnknown {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	ref, ok := ci.Callee.(*mir.FnRef)
	if !ok {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	if ref.Symbol == "" {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	if !mctx.knownSymbols[ref.Symbol] {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	// Validate the callee's declared return type matches dest.
	fnTy, ok := ref.Type.(*ir.FnType)
	if !ok || fnTy == nil {
		return pendingInstr{}, 0, scalarUnknown, false
	}
	if scalarFromType(fnTy.Return) != destType {
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
		paramTy := scalarFromType(fnTy.Params[i])
		if paramTy == scalarUnknown || paramTy != argTy {
			return pendingInstr{}, 0, scalarUnknown, false
		}
		args = append(args, callArg{expr: argExpr, ty: argTy.llvm()})
	}
	return pendingInstr{
		kind:       instrCall,
		callSymbol: ref.Symbol,
		callArgs:   args,
	}, destID, destType, true
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
	return pendingInstr{}, "", false
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
		case instrIntrinsic:
			out.WriteString(pi.intrinsicLine)
		}
	}
	fmt.Fprintf(out, "  ret %s %s\n", retLLVM, pat.returnExpr)
	out.WriteString("}\n\n")
	return nil
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
		case instrIntrinsic:
			out.WriteString(pi.intrinsicLine)
		}
	}
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
		line, okIntr := classifyIntrinsicLine(step, bindings, mctx)
		if !okIntr {
			return false
		}
		emit.pending = append(emit.pending, pendingInstr{kind: instrIntrinsic, intrinsicLine: line})
		return true
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
	case instrBinary, instrCall:
		reg := fmt.Sprintf("%%%d", *nextSSA)
		*nextSSA++
		pending.binDestReg = reg
		bindings[destID] = localBinding{expr: reg, ty: destType, defined: true}
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
		fn:           fn,
		bindings:     bindings,
		stack:        stack,
		mctx:         mctx,
		nextSSA:      &nextSSA,
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
	fn           *mir.Function
	bindings     map[mir.LocalID]localBinding
	stack        map[mir.LocalID]stackDecl
	mctx *moduleCtx
	nextSSA      *int
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
		if !ok || ty != scalarInt {
			return false
		}
		fmt.Fprintf(out, "  call i32 (ptr, ...) @printf(ptr @.fmt.stage0.println.int, i64 %s)\n", expr)
		return true
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
		fn:           fn,
		bindings:     bindings,
		stack:        stack,
		mctx:         mctx,
		nextSSA:      &nextSSA,
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
