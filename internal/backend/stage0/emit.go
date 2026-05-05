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
	if pat, ok := matchSequentialReturn(fn); ok {
		return emitSequentialReturn(out, fn, pat)
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
)

func (s scalarType) llvm() string {
	switch s {
	case scalarInt:
		return "i64"
	case scalarBool:
		return "i1"
	}
	return ""
}

func scalarFromType(t mir.Type) scalarType {
	prim, ok := t.(*ir.PrimType)
	if !ok || prim == nil {
		return scalarUnknown
	}
	switch prim.Kind {
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
	expr      string // full LLVM operand expression (`42`, `true`, `%x`, `%3`)
	ty        scalarType
	defined   bool
}

// pendingInstr is one instruction the emitter will materialise.
// For `inline` instructions (UseRV) nothing is emitted — the matcher
// already recorded the expression in seqState.bindings. For `binary`
// instructions the emitter assigns the next SSA register at emit time.
type pendingInstr struct {
	kind        instrKind
	destLocal   mir.LocalID
	resultType  scalarType
	// binary fields
	binOp       string
	binArgType  string
	leftExpr    string
	rightExpr   string
}

type instrKind int

const (
	instrInline instrKind = iota
	instrBinary
)

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
func matchSequentialReturn(fn *mir.Function) (sequentialPattern, bool) {
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
		assign, ok := instr.(*mir.AssignInstr)
		if !ok {
			return pat, false
		}
		if assign.Dest.HasProjections() {
			return pat, false
		}
		destID := assign.Dest.Local
		destLocal := lookupLocal(fn, destID)
		if destLocal == nil {
			return pat, false
		}
		destType := scalarFromType(destLocal.Type)
		if destType == scalarUnknown {
			return pat, false
		}
		// Reassignment is unsupported — both for params and for
		// previously-bound locals.
		if existing, found := bindings[destID]; found && existing.defined {
			return pat, false
		}

		pending, expr, okSrc := classifyAssignSrc(assign.Src, destType, bindings)
		if !okSrc {
			return pat, false
		}
		pending.destLocal = destID
		pending.resultType = destType

		if pending.kind == instrBinary {
			reg := fmt.Sprintf("%%%d", nextSSA)
			nextSSA++
			pending.leftExpr = pending.leftExpr // already set
			pending.rightExpr = pending.rightExpr
			bindings[destID] = localBinding{expr: reg, ty: destType, defined: true}
			expr = reg
		} else {
			bindings[destID] = localBinding{expr: expr, ty: destType, defined: true}
		}
		pat.pending = append(pat.pending, pending)
		_ = expr
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

// classifyAssignSrc reduces an AssignInstr.Src to either a pending
// inline binding (no LLVM emission) or a pending binary op (emit a
// fresh SSA register). `expr` is the LLVM operand string for inline
// bindings; binary ops return "" (the emitter assigns a register
// number after seeing the full pending list).
func classifyAssignSrc(src mir.RValue, destType scalarType, bindings map[mir.LocalID]localBinding) (pendingInstr, string, bool) {
	if use, ok := src.(*mir.UseRV); ok {
		expr, ty, ok := resolveOperand(use.Op, bindings)
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
		left, leftTy, ok := resolveOperand(bin.Left, bindings)
		if !ok || leftTy != operandType {
			return pendingInstr{}, "", false
		}
		right, rightTy, ok := resolveOperand(bin.Right, bindings)
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
func resolveOperand(op mir.Operand, bindings map[mir.LocalID]localBinding) (string, scalarType, bool) {
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

	ssa := 0
	for _, pi := range pat.pending {
		if pi.kind != instrBinary {
			continue
		}
		fmt.Fprintf(out, "  %%%d = %s %s %s, %s\n", ssa, pi.binOp, pi.binArgType, pi.leftExpr, pi.rightExpr)
		ssa++
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
