package onb

import "fmt"

// Program is ONB's first native-owned lowering product. It is deliberately
// smaller than the final backend IR, but it already speaks in aarch64-shaped
// instructions so the pipeline can grow toward object emission one opcode at a
// time.
type Program struct {
	Target    Target
	Functions []Function
	CStrings  []CStringLiteral
}

// CStringLiteral is a null-terminated string payload referenced by ONB code.
type CStringLiteral struct {
	Label string
	Value string
}

// Function is one lowered ONB function.
//
// FrameSize is the total stack frame size in bytes (multiple of 16). 0 means
// the function uses no stack — a leaf function with no locals and no stack
// arguments. The lowerer fills this in at the end of lowering once it knows
// how many local slots and whether a printf vararg slot are needed.
type Function struct {
	Name      string
	Blocks    []Block
	FrameSize int64
}

// Block is a linear basic block.
//
// OriginalIndex is the function-relative MIR block ID this LIR block was
// lowered from. Branches reference targets by this index; the encoder maps
// original indices to byte offsets while it walks the emit order.
type Block struct {
	Label         string
	OriginalIndex int
	Instrs        []Instr
}

// Instr is one ONB aarch64 LIR instruction.
type Instr interface {
	instrNode()
}

// Reg names an aarch64 architectural register in the current minimal LIR.
type Reg string

const (
	RegX0  Reg = "x0"
	RegX1  Reg = "x1"
	RegX2  Reg = "x2"
	RegX3  Reg = "x3"
	RegX4  Reg = "x4"
	RegX5  Reg = "x5"
	RegX6  Reg = "x6"
	RegX7  Reg = "x7"
	RegX9  Reg = "x9"
	RegX10 Reg = "x10"
	RegX29 Reg = "x29"
	RegX30 Reg = "x30"
	RegSP  Reg = "sp"
	RegW0  Reg = "w0"
)

// MovImm32 lowers a small integer immediate into a 32-bit destination
// register. `fn main() {}` starts with `mov w0, #0` for the process exit code.
type MovImm32 struct {
	Dst Reg
	Imm int64
}

func (*MovImm32) instrNode() {}

// MovImm64 loads a 64-bit integer immediate into an x register. The object
// writer expands this into a movz/movk sequence as needed.
type MovImm64 struct {
	Dst Reg
	Imm int64
}

func (*MovImm64) instrNode() {}

// MovRegReg copies one 64-bit register into another (`mov Xd, Xs`).
// Used to plumb a value into x0/x1 for printf, or to refresh a scratch
// register before an arithmetic op.
type MovRegReg struct {
	Dst Reg
	Src Reg
}

func (*MovRegReg) instrNode() {}

// Store64Stack stores a 64-bit register into the current call frame. Used
// both for printf's vararg integer slot and for spilling a local variable
// to its assigned stack slot.
type Store64Stack struct {
	Src    Reg
	Offset int64
}

func (*Store64Stack) instrNode() {}

// Load64Stack loads a 64-bit slot from the current call frame into an x
// register. Used to materialise a local's value into a scratch register
// before consuming it.
type Load64Stack struct {
	Dst    Reg
	Offset int64
}

func (*Load64Stack) instrNode() {}

// AddReg / SubReg / MulReg are the three Int binary ops Slice A2 covers:
// `<op> Xd, Xn, Xm`. The lowerer emits these between scratch registers
// (typically x9/x10), with operands previously materialised via Load64Stack
// or MovImm64.
type AddReg struct {
	Dst Reg
	Lhs Reg
	Rhs Reg
}

func (*AddReg) instrNode() {}

type SubReg struct {
	Dst Reg
	Lhs Reg
	Rhs Reg
}

func (*SubReg) instrNode() {}

type MulReg struct {
	Dst Reg
	Lhs Reg
	Rhs Reg
}

func (*MulReg) instrNode() {}

// Cond is an aarch64 condition code as used by `b.cond`, `cset`, `csel`,
// etc. Only the conditions Phase A2 Week 3 emits are named here; any
// future op may extend this enum.
type Cond uint8

const (
	CondEq Cond = 0  // equal — Z set
	CondNe Cond = 1  // not equal — Z clear
	CondGe Cond = 10 // signed greater-or-equal — N == V
	CondLt Cond = 11 // signed less-than — N != V
	CondGt Cond = 12 // signed greater-than — Z clear and N == V
	CondLe Cond = 13 // signed less-or-equal — !(Z clear and N == V)
)

// AsmName returns the assembler mnemonic suffix (e.g. `eq`, `lt`).
func (c Cond) AsmName() string {
	switch c {
	case CondEq:
		return "eq"
	case CondNe:
		return "ne"
	case CondGe:
		return "ge"
	case CondLt:
		return "lt"
	case CondGt:
		return "gt"
	case CondLe:
		return "le"
	default:
		return fmt.Sprintf("cond%d", c)
	}
}

// Cmp encodes `cmp Xn, Xm` (alias for SUBS XZR, Xn, Xm). Used as the
// flag-setting half of the comparison-into-bool pattern: `cmp lhs, rhs;
// cset dst, <cond>` produces 1 if the condition holds, else 0.
type Cmp struct {
	Lhs Reg
	Rhs Reg
}

func (*Cmp) instrNode() {}

// Cset materialises the boolean result of a flags-setting operation into a
// register: `cset Xd, <cond>` writes 1 if cond holds, else 0.
type Cset struct {
	Dst  Reg
	Cond Cond
}

func (*Cset) instrNode() {}

// Branch is an unconditional branch to a target block within the same
// function. The integer is the target's index in fn.Blocks. The encoder
// resolves it to a PC-relative imm26 in the second pass.
type Branch struct {
	Target int
}

func (*Branch) instrNode() {}

// BranchCondNotZero is `cbnz Xn, <block>` — branch to the target block
// when the source register is non-zero. Used directly on a materialised
// boolean local for the BranchTerm lowering.
type BranchCondNotZero struct {
	Src    Reg
	Target int
}

func (*BranchCondNotZero) instrNode() {}

// BranchCond is `b.<cond> <block>` — branch to the target block when the
// flag-setting predecessor (typically a Cmp) leaves the flags consistent
// with the named condition. Used by the SwitchIntTerm lowering: each case
// is `cmp scrutinee, immediate; b.eq case_target`.
type BranchCond struct {
	Cond   Cond
	Target int
}

func (*BranchCond) instrNode() {}

// LoadCStringAddress materializes the address of a C string literal into a
// register using the platform's PC-relative addressing form.
type LoadCStringAddress struct {
	Dst   Reg
	Label string
}

func (*LoadCStringAddress) instrNode() {}

// BranchLink calls an external or local function symbol.
type BranchLink struct {
	Symbol string
}

func (*BranchLink) instrNode() {}

// Ret returns to the platform entry trampoline. For `main`, the return code is
// already in w0.
type Ret struct{}

func (*Ret) instrNode() {}

func functionNeedsFrame(fn Function) bool {
	if fn.FrameSize > 0 {
		return true
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if _, ok := instr.(*BranchLink); ok {
				return true
			}
		}
	}
	return false
}

// functionFrameSize returns the explicit FrameSize when set, otherwise the
// legacy 16/32-byte hello-world layout (16 for leaf-with-call, 32 for
// printf-with-vararg). Asm and Mach-O encoders use this single source of
// truth to compute prologue/epilogue offsets.
func functionFrameSize(fn Function) int64 {
	if fn.FrameSize > 0 {
		return fn.FrameSize
	}
	if !functionNeedsFrame(fn) {
		return 0
	}
	if functionNeedsStackArgs(fn) {
		return 32
	}
	return 16
}

// functionFPOffset returns the byte offset from sp where (x29, x30) live in
// the prologue/epilogue. For the legacy `[sp, #-16]!` layout this is "use the
// pre-decrement form", signalled by returning -1.
func functionFPOffset(fn Function) int64 {
	size := functionFrameSize(fn)
	if size == 0 {
		return -1
	}
	if fn.FrameSize == 0 && !functionNeedsStackArgs(fn) {
		// legacy `stp x29, x30, [sp, #-16]!` form
		return -1
	}
	return size - 16
}

func functionNeedsStackArgs(fn Function) bool {
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if _, ok := instr.(*Store64Stack); ok {
				return true
			}
		}
	}
	return false
}
