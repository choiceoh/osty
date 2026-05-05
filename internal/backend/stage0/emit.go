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
//   - `fn name() -> T { value }` for T ∈ {Int, Bool} — single block,
//     one AssignInstr writing a const / param-copy operand to the
//     return local, ReturnTerm. Up to two Int / Bool parameters.
//
//   - `fn name(a, b: T) -> R { a OP b }` — single block, one
//     AssignInstr writing a binary op to the return local, ReturnTerm.
//     Each operand is independently a const literal or a CopyOp on
//     one of the params (no projections). Operator families:
//
//     arithmetic Int×Int → Int : Add Sub Mul Div Mod
//     comparison Int×Int → Bool: Eq Neq Lt Leq Gt Geq
//     bitwise    Int×Int → Int : BitAnd BitOr BitXor Shl Shr
//     logical    Bool×Bool→ Bool: And Or
//
// Anything else (multi-instruction, calls, control flow, non-Int/Bool
// types, projections, …) declines so the next stage0 phase can pick
// the case up. Surface drift is held back by the stage0 coverage gate
// planned for P3.
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
	if pat, ok := matchSingleInstrReturn(fn); ok {
		return emitSingleInstrReturn(out, fn, pat)
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

// ---- single-instruction return pattern ----
//
// Covers everything from `fn x() -> Int { N }` (P2a) through
// `fn cmp(a, b: Int) -> Bool { a < b }` (P2e bitwise / comparison /
// logical / mixed const+var). The matcher classifies each operand
// independently as a const or a param copy, so any 0/1/2-param
// function with a single AssignInstr that writes a supported scalar
// expression to the return local is covered.

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

// operandSrc records how to materialise one operand in LLVM. Either
// a literal constant (int or bool) or a reference to a parameter local
// (resolved to its position-indexed sanitised SSA name at emit time).
type operandSrc struct {
	kind    operandKind
	intVal  int64
	boolVal bool
	paramID mir.LocalID
}

type operandKind int

const (
	operandUnknown operandKind = iota
	operandIntConst
	operandBoolConst
	operandParamCopy
)

// singleInstrPattern carries everything emitSingleInstrReturn needs.
// `body` is nil for use-only / passthrough, non-nil for binary ops.
type singleInstrPattern struct {
	retType    scalarType
	paramIDs   []mir.LocalID
	paramTypes []scalarType
	paramNames []string // sanitised, position-disambiguated SSA names
	source     valueSource
}

// valueSource is the right-hand side of the AssignInstr. Either a
// single operand (UseRV) or a binary op.
type valueSource struct {
	binary bool
	// Single-operand mode (UseRV).
	single operandSrc
	// Binary mode (BinaryRV).
	op    mir.BinaryOp
	left  operandSrc
	right operandSrc
	// llvmOp is the LLVM mnemonic resolved by classifyBinary; "" when binary=false.
	llvmOp string
	// resultType is the result type the binary op produces (Int or Bool).
	resultType scalarType
}

// matchSingleInstrReturn matches the return-only single-instruction
// pattern described in `EmitMIR`'s docstring.
func matchSingleInstrReturn(fn *mir.Function) (singleInstrPattern, bool) {
	pat := singleInstrPattern{}
	pat.retType = scalarFromType(fn.ReturnType)
	if pat.retType == scalarUnknown {
		return pat, false
	}
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

	bb, ok := singleBlockReturning(fn)
	if !ok || len(bb.Instrs) != 1 {
		return pat, false
	}
	assign, ok := writeToReturnLocal(bb.Instrs[0], fn.ReturnLocal)
	if !ok {
		return pat, false
	}
	src, ok := classifyAssignSrc(assign.Src, pat)
	if !ok {
		return pat, false
	}
	pat.source = src
	return pat, true
}

func emitSingleInstrReturn(out *strings.Builder, fn *mir.Function, pat singleInstrPattern) error {
	retLLVM := pat.retType.llvm()

	// Function header.
	fmt.Fprintf(out, "define %s @%s(", retLLVM, fn.Name)
	for i, name := range pat.paramNames {
		if i > 0 {
			out.WriteString(", ")
		}
		fmt.Fprintf(out, "%s %%%s", pat.paramTypes[i].llvm(), name)
	}
	out.WriteString(") {\n")
	out.WriteString("entry:\n")

	// Body.
	if pat.source.binary {
		opTypeLLVM := pat.source.resultType
		// For comparison ops, operand type is Int even though result is Bool.
		// Use the operand's actual scalar type instead.
		opOperandType := operandScalarType(pat.source.left, pat).llvm()
		left := operandLLVM(pat.source.left, pat)
		right := operandLLVM(pat.source.right, pat)
		fmt.Fprintf(out, "  %%0 = %s %s %s, %s\n", pat.source.llvmOp, opOperandType, left, right)
		fmt.Fprintf(out, "  ret %s %%0\n", opTypeLLVM.llvm())
	} else {
		fmt.Fprintf(out, "  ret %s %s\n", retLLVM, operandLLVM(pat.source.single, pat))
	}
	out.WriteString("}\n\n")
	return nil
}

// classifyAssignSrc inspects an AssignInstr.Src and decodes it to a
// valueSource the emitter can consume.
func classifyAssignSrc(src mir.RValue, pat singleInstrPattern) (valueSource, bool) {
	if use, ok := src.(*mir.UseRV); ok {
		op, ok := classifyOperand(use.Op, pat)
		if !ok {
			return valueSource{}, false
		}
		// Operand result type must match the return type.
		if operandScalarTypeFromKind(op.kind, pat, op) != pat.retType {
			return valueSource{}, false
		}
		return valueSource{single: op}, true
	}
	if bin, ok := src.(*mir.BinaryRV); ok {
		llvmOp, resultType, operandType := classifyBinary(bin.Op)
		if llvmOp == "" || resultType != pat.retType {
			return valueSource{}, false
		}
		left, ok := classifyOperand(bin.Left, pat)
		if !ok || operandScalarTypeFromKind(left.kind, pat, left) != operandType {
			return valueSource{}, false
		}
		right, ok := classifyOperand(bin.Right, pat)
		if !ok || operandScalarTypeFromKind(right.kind, pat, right) != operandType {
			return valueSource{}, false
		}
		return valueSource{
			binary:     true,
			op:         bin.Op,
			left:       left,
			right:      right,
			llvmOp:     llvmOp,
			resultType: resultType,
		}, true
	}
	return valueSource{}, false
}

// classifyOperand decodes a single MIR Operand into an operandSrc.
func classifyOperand(op mir.Operand, pat singleInstrPattern) (operandSrc, bool) {
	if con, ok := op.(*mir.ConstOp); ok {
		switch c := con.Const.(type) {
		case *mir.IntConst:
			if !isPrimType(c.Type(), ir.PrimInt) {
				return operandSrc{}, false
			}
			return operandSrc{kind: operandIntConst, intVal: c.Value}, true
		case *mir.BoolConst:
			return operandSrc{kind: operandBoolConst, boolVal: c.Value}, true
		}
		return operandSrc{}, false
	}
	if cp, ok := op.(*mir.CopyOp); ok {
		if cp.Place.HasProjections() {
			return operandSrc{}, false
		}
		idx := paramIndex(pat, cp.Place.Local)
		if idx < 0 {
			return operandSrc{}, false
		}
		return operandSrc{kind: operandParamCopy, paramID: cp.Place.Local}, true
	}
	return operandSrc{}, false
}

// classifyBinary returns (llvmOp, resultType, operandType) for a MIR
// binary operator the stage0 single-instruction pattern supports.
// Operator families:
//
//	arith    Int×Int → Int : Add Sub Mul Div Mod
//	cmp      Int×Int → Bool: Eq Neq Lt Leq Gt Geq
//	bitwise  Int×Int → Int : BitAnd BitOr BitXor Shl Shr
//	logical  Bool×Bool→Bool: And Or
//
// Returns ("", scalarUnknown, scalarUnknown) for unsupported ops.
func classifyBinary(op mir.BinaryOp) (string, scalarType, scalarType) {
	switch op {
	// arithmetic
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
	// comparison (signed Int)
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
	// bitwise / shift on Int
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
	// logical on Bool
	case mir.BinAnd:
		return "and", scalarBool, scalarBool
	case mir.BinOr:
		return "or", scalarBool, scalarBool
	}
	return "", scalarUnknown, scalarUnknown
}

// operandScalarType / operandScalarTypeFromKind report the LLVM scalar
// type of an operand. operandParamCopy resolves through pat.paramTypes;
// constants carry their own type.
func operandScalarType(op operandSrc, pat singleInstrPattern) scalarType {
	return operandScalarTypeFromKind(op.kind, pat, op)
}

func operandScalarTypeFromKind(kind operandKind, pat singleInstrPattern, op operandSrc) scalarType {
	switch kind {
	case operandIntConst:
		return scalarInt
	case operandBoolConst:
		return scalarBool
	case operandParamCopy:
		idx := paramIndex(pat, op.paramID)
		if idx < 0 || idx >= len(pat.paramTypes) {
			return scalarUnknown
		}
		return pat.paramTypes[idx]
	}
	return scalarUnknown
}

func operandLLVM(op operandSrc, pat singleInstrPattern) string {
	switch op.kind {
	case operandIntConst:
		return formatInt(op.intVal)
	case operandBoolConst:
		if op.boolVal {
			return "true"
		}
		return "false"
	case operandParamCopy:
		idx := paramIndex(pat, op.paramID)
		if idx >= 0 && idx < len(pat.paramNames) {
			return "%" + pat.paramNames[idx]
		}
	}
	return "<invalid>"
}

func paramIndex(pat singleInstrPattern, id mir.LocalID) int {
	for i, pid := range pat.paramIDs {
		if pid == id {
			return i
		}
	}
	return -1
}

// disambiguateParamNames mutates `names` in place so that no two
// entries are identical, by suffixing collisions with `.<index>`.
// Sanitiser fallbacks (`a`, `b`) are already position-distinct, but a
// user could legitimately name two parameters the same after
// sanitisation collapses them.
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

func formatInt(v int64) string {
	return fmt.Sprintf("%d", v)
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
