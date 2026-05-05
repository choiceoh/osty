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
// Currently supported function shapes (each gated by an explicit
// `match*` predicate; non-matching shapes return ErrUnsupported):
//
//   - P1: `fn main() {}` — single block, zero instructions, ReturnTerm.
//   - P2a: `fn name() -> Int { N }` — non-main, single block, one
//     AssignInstr writing IntConst to the return local, ReturnTerm.
//   - P2b: `fn name(x: Int) -> Int { x }` — non-main, single Int
//     parameter, single block, one AssignInstr writing CopyOp(param)
//     to the return local, ReturnTerm.
//   - P2c/P2d: `fn name(a: Int, b: Int) -> Int { a OP b }` — non-main,
//     two Int parameters, single block, one AssignInstr writing
//     BinaryRV{op, CopyOp(p0), CopyOp(p1)} to the return local,
//     ReturnTerm. OP is one of Add (P2c) / Sub / Mul / Div / Mod (P2d).
//
// Each P2x extension adds one match predicate + emit closure +
// regression test. Surface drift is held back by the stage0 coverage
// gate planned for P3.
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

	emittedMain := false
	for _, fn := range module.Functions {
		if fn == nil {
			continue
		}
		if err := emitFunction(&out, fn); err != nil {
			return nil, err
		}
		if fn.Name == "main" {
			emittedMain = true
		}
	}
	if !emittedMain {
		return nil, fmt.Errorf("%w: module has no `main` function", ErrUnsupported)
	}
	return []byte(out.String()), nil
}

func emitFunction(out *strings.Builder, fn *mir.Function) error {
	if fn.IsIntrinsic {
		return fmt.Errorf("%w: intrinsic declaration %q", ErrUnsupported, fn.Name)
	}
	if fn.IsExternal {
		return fmt.Errorf("%w: external declaration %q", ErrUnsupported, fn.Name)
	}
	if fn.Name == "main" {
		return emitTrivialMain(out, fn)
	}
	if pat, ok := matchIntLiteralReturn(fn); ok {
		return emitIntLiteralReturn(out, fn, pat)
	}
	if pat, ok := matchIntParamPassthrough(fn); ok {
		return emitIntParamPassthrough(out, fn, pat)
	}
	if pat, ok := matchIntBinaryArith(fn); ok {
		return emitIntBinaryArith(out, fn, pat)
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
	if len(bb.Instrs) != 0 {
		return fmt.Sprintf("main entry block has %d instructions; stage0 expects 0", len(bb.Instrs))
	}
	if _, ok := bb.Term.(*mir.ReturnTerm); !ok {
		return fmt.Sprintf("main terminator is %T; stage0 expects ReturnTerm", bb.Term)
	}
	return ""
}

// ---- P2a: int literal return ----

type intLiteralPattern struct {
	value int64
}

func matchIntLiteralReturn(fn *mir.Function) (intLiteralPattern, bool) {
	if !isPrimType(fn.ReturnType, ir.PrimInt) {
		return intLiteralPattern{}, false
	}
	if len(fn.Params) != 0 {
		return intLiteralPattern{}, false
	}
	bb, ok := singleBlockReturning(fn)
	if !ok || len(bb.Instrs) != 1 {
		return intLiteralPattern{}, false
	}
	assign, ok := writeToReturnLocal(bb.Instrs[0], fn.ReturnLocal)
	if !ok {
		return intLiteralPattern{}, false
	}
	use, ok := assign.Src.(*mir.UseRV)
	if !ok {
		return intLiteralPattern{}, false
	}
	con, ok := use.Op.(*mir.ConstOp)
	if !ok {
		return intLiteralPattern{}, false
	}
	intc, ok := con.Const.(*mir.IntConst)
	if !ok || !isPrimType(intc.Type(), ir.PrimInt) {
		return intLiteralPattern{}, false
	}
	return intLiteralPattern{value: intc.Value}, true
}

func emitIntLiteralReturn(out *strings.Builder, fn *mir.Function, pat intLiteralPattern) error {
	fmt.Fprintf(out, "define i64 @%s() {\n", fn.Name)
	out.WriteString("entry:\n")
	fmt.Fprintf(out, "  ret i64 %d\n", pat.value)
	out.WriteString("}\n\n")
	return nil
}

// ---- P2b: single Int param passthrough ----

type intParamPassthroughPattern struct {
	paramName string
}

func matchIntParamPassthrough(fn *mir.Function) (intParamPassthroughPattern, bool) {
	if !isPrimType(fn.ReturnType, ir.PrimInt) {
		return intParamPassthroughPattern{}, false
	}
	if len(fn.Params) != 1 {
		return intParamPassthroughPattern{}, false
	}
	paramID := fn.Params[0]
	paramLocal := lookupLocal(fn, paramID)
	if paramLocal == nil || !paramLocal.IsParam || !isPrimType(paramLocal.Type, ir.PrimInt) {
		return intParamPassthroughPattern{}, false
	}
	bb, ok := singleBlockReturning(fn)
	if !ok || len(bb.Instrs) != 1 {
		return intParamPassthroughPattern{}, false
	}
	assign, ok := writeToReturnLocal(bb.Instrs[0], fn.ReturnLocal)
	if !ok {
		return intParamPassthroughPattern{}, false
	}
	use, ok := assign.Src.(*mir.UseRV)
	if !ok {
		return intParamPassthroughPattern{}, false
	}
	cp, ok := use.Op.(*mir.CopyOp)
	if !ok {
		return intParamPassthroughPattern{}, false
	}
	if cp.Place.Local != paramID || cp.Place.HasProjections() {
		return intParamPassthroughPattern{}, false
	}
	return intParamPassthroughPattern{paramName: sanitizeLLVMName(paramLocal.Name, "p")}, true
}

func emitIntParamPassthrough(out *strings.Builder, fn *mir.Function, pat intParamPassthroughPattern) error {
	fmt.Fprintf(out, "define i64 @%s(i64 %%%s) {\n", fn.Name, pat.paramName)
	out.WriteString("entry:\n")
	fmt.Fprintf(out, "  ret i64 %%%s\n", pat.paramName)
	out.WriteString("}\n\n")
	return nil
}

// ---- P2c/P2d: two-Int-param binary arithmetic ----

type intBinaryArithPattern struct {
	op        mir.BinaryOp
	leftName  string
	rightName string
}

// matchIntBinaryArith matches `fn name(a: Int, b: Int) -> Int { a OP b }`
// where OP is one of the supported integer arithmetic operators (Add /
// Sub / Mul / Div / Mod — see `intArithLLVMOp`). MIR shape: 2 Int
// params, single block, single AssignInstr writing BinaryRV{op,
// CopyOp(p0), CopyOp(p1)} to the return local, ReturnTerm. Operand
// order must match Params order — `b + a` lowers to swapped CopyOps
// and currently declines.
func matchIntBinaryArith(fn *mir.Function) (intBinaryArithPattern, bool) {
	if !isPrimType(fn.ReturnType, ir.PrimInt) {
		return intBinaryArithPattern{}, false
	}
	if len(fn.Params) != 2 {
		return intBinaryArithPattern{}, false
	}
	leftID, rightID := fn.Params[0], fn.Params[1]
	leftLocal, rightLocal := lookupLocal(fn, leftID), lookupLocal(fn, rightID)
	if leftLocal == nil || rightLocal == nil {
		return intBinaryArithPattern{}, false
	}
	if !leftLocal.IsParam || !rightLocal.IsParam {
		return intBinaryArithPattern{}, false
	}
	if !isPrimType(leftLocal.Type, ir.PrimInt) || !isPrimType(rightLocal.Type, ir.PrimInt) {
		return intBinaryArithPattern{}, false
	}
	bb, ok := singleBlockReturning(fn)
	if !ok || len(bb.Instrs) != 1 {
		return intBinaryArithPattern{}, false
	}
	assign, ok := writeToReturnLocal(bb.Instrs[0], fn.ReturnLocal)
	if !ok {
		return intBinaryArithPattern{}, false
	}
	bin, ok := assign.Src.(*mir.BinaryRV)
	if !ok {
		return intBinaryArithPattern{}, false
	}
	if !isPrimType(bin.T, ir.PrimInt) {
		return intBinaryArithPattern{}, false
	}
	if intArithLLVMOp(bin.Op) == "" {
		return intBinaryArithPattern{}, false
	}
	if !copiesParam(bin.Left, leftID) || !copiesParam(bin.Right, rightID) {
		return intBinaryArithPattern{}, false
	}
	return intBinaryArithPattern{
		op:        bin.Op,
		leftName:  sanitizeLLVMName(leftLocal.Name, "a"),
		rightName: sanitizeLLVMName(rightLocal.Name, "b"),
	}, true
}

func emitIntBinaryArith(out *strings.Builder, fn *mir.Function, pat intBinaryArithPattern) error {
	left, right := pat.leftName, pat.rightName
	if left == right {
		// Sanitiser hands back the same fallback when both source
		// names are unrenderable; disambiguate so SSA stays valid.
		right = right + ".1"
	}
	llvmOp := intArithLLVMOp(pat.op)
	fmt.Fprintf(out, "define i64 @%s(i64 %%%s, i64 %%%s) {\n", fn.Name, left, right)
	out.WriteString("entry:\n")
	fmt.Fprintf(out, "  %%0 = %s i64 %%%s, %%%s\n", llvmOp, left, right)
	out.WriteString("  ret i64 %0\n")
	out.WriteString("}\n\n")
	return nil
}

// intArithLLVMOp returns the LLVM signed-integer instruction mnemonic
// for `op`, or "" when the operator is outside the stage0 arithmetic
// subset. Comparison / logical / bitwise ops decline so the matcher
// gives the next pattern (when one is added) a chance.
func intArithLLVMOp(op mir.BinaryOp) string {
	switch op {
	case mir.BinAdd:
		return "add"
	case mir.BinSub:
		return "sub"
	case mir.BinMul:
		return "mul"
	case mir.BinDiv:
		return "sdiv"
	case mir.BinMod:
		return "srem"
	}
	return ""
}

// copiesParam reports whether `op` is a CopyOp reading the entire
// param local with id `paramID` (no projections).
func copiesParam(op mir.Operand, paramID mir.LocalID) bool {
	cp, ok := op.(*mir.CopyOp)
	if !ok {
		return false
	}
	if cp.Place.Local != paramID || cp.Place.HasProjections() {
		return false
	}
	return true
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

func writeToReturnLocal(instr mir.Instr, ret mir.LocalID) (*mir.AssignInstr, bool) {
	assign, ok := instr.(*mir.AssignInstr)
	if !ok {
		return nil, false
	}
	if assign.Dest.Local != ret || assign.Dest.HasProjections() {
		return nil, false
	}
	return assign, true
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
// Stage0 only emits SSA registers from clean Osty parameter names; the
// guard exists so synthetic locals (`""`, generated suffixes) cannot
// produce malformed IR.
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
