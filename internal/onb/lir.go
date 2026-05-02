package onb

import "fmt"

// Program is ONB's first native-owned lowering product. It is deliberately
// smaller than the final backend IR, but it already speaks in aarch64-shaped
// instructions so the pipeline can grow toward object emission one opcode at a
// time.
//
// SourcePath and Package carry the source-of-record metadata the DWARF
// emitter needs for the line-program file table and the compile-unit DIE.
// Both stay empty when the front end didn't supply them — the line emitter
// then falls back to a synthetic `<unknown>` filename.
type Program struct {
	Target     Target
	Functions  []Function
	CStrings   []CStringLiteral
	SourcePath string
	Package    string
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
//
// DebugLocals lists user-named locals (parameters + `let` bindings) that
// the lowerer placed in stack slots. The DWARF emitter uses this to
// produce one DW_TAG_variable DIE per local so lldb's `frame variable`
// can recover the value.
type Function struct {
	Name        string
	Blocks      []Block
	FrameSize   int64
	DebugLocals []DebugLocal
}

// DebugLocal binds a user-readable name to an stack-slot offset and a
// primitive type kind. Anonymous compiler temps are excluded — they
// would clutter `frame variable` output without helping the user.
//
// StructName / StructFields populate when TypeKind == DebugTypeStruct;
// the DWARF emitter de-duplicates struct types by StructName across
// every function in a CU. StructFields lists each field's name and
// primitive kind in source order — only all-scalar structs are
// describable today, which matches what the lowerer accepts.
type DebugLocal struct {
	Name         string
	SlotOffset   int64
	TypeKind     DebugTypeKind
	StructName   string
	StructFields []DebugStructField
}

// DebugStructField is one entry inside DebugLocal.StructFields.
// FieldKind enumerates the same primitive kinds DebugTypeKind covers; a
// nested struct field would need an additional path that points back at
// another struct type, which the lowerer doesn't lower today.
type DebugStructField struct {
	Name      string
	FieldKind DebugTypeKind
}

// DebugTypeKind enumerates the primitive types the DWARF emitter can
// describe today. The order matches dwarfBaseTypeKind in dwarf.go and
// keeps the LIR layer free of DWARF-spec constants.
type DebugTypeKind int

const (
	DebugTypeNone DebugTypeKind = iota
	DebugTypeInt
	DebugTypeBool
	DebugTypeFloat  // Float / Float64 (IEEE 754 double)
	DebugTypeString // String — pointer to UTF-8 bytes at the ABI boundary
	DebugTypeStruct // user-defined small struct described via DebugLocal.StructName/Fields
)

// Block is a linear basic block.
//
// OriginalIndex is the function-relative MIR block ID this LIR block was
// lowered from. Branches reference targets by this index; the encoder maps
// original indices to byte offsets while it walks the emit order.
//
// LineSpans, when populated, parallels Instrs: LineSpans[i] is the source
// position the i-th LIR instruction was lowered from. The DWARF line
// emitter consumes this to produce one PC→source row per change in line.
type Block struct {
	Label         string
	OriginalIndex int
	Instrs        []Instr
	LineSpans     []LineSpan
}

// LineSpan captures one source-position record per emitted LIR instruction.
// Zero Line means "no source info" — the encoder treats it as
// unchanged-from-previous, preserving the previous mapping.
type LineSpan struct {
	Line   int
	Column int
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

	// AAPCS64 floating-point argument registers d0..d7 are used for
	// Float64 arguments and return values. d8/d9 act as the
	// stack-everything model's FP scratch slot, mirroring x9/x10 on
	// the integer side.
	RegD0 Reg = "d0"
	RegD1 Reg = "d1"
	RegD2 Reg = "d2"
	RegD3 Reg = "d3"
	RegD4 Reg = "d4"
	RegD5 Reg = "d5"
	RegD6 Reg = "d6"
	RegD7 Reg = "d7"
	RegD8 Reg = "d8"
	RegD9 Reg = "d9"
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

// BranchLinkReg is the indirect call form: `blr Xn`. Reads the target
// PC from the named register, pushes the return address into x30, and
// branches. The dev backend uses this for closure dispatch — the env
// pointer's first slot holds the lifted-fn address; we load it into a
// scratch register and `blr` into it. The closure ABI also requires
// the env pointer itself to be in x0 before the branch (per the Phase
// A4 fn-value runtime contract); the lowerer stages that explicitly.
type BranchLinkReg struct {
	Reg Reg
}

func (*BranchLinkReg) instrNode() {}

// LoadSymbolAddress materialises the runtime address of a symbol
// (function or data) into an x-register via the platform's
// PC-relative addressing form (`adrp` + `add` on Mach-O/ELF). Used
// to land the lifted fn pointer at offset 0 of a freshly-allocated
// closure env. Distinct from `LoadCStringAddress` because that one
// is hard-wired to `__cstring` entries; symbols here can be local
// fns or runtime exports.
type LoadSymbolAddress struct {
	Dst    Reg
	Symbol string
}

func (*LoadSymbolAddress) instrNode() {}

// Ret returns to the platform entry trampoline. For `main`, the return code is
// already in w0.
type Ret struct{}

func (*Ret) instrNode() {}

// Brk emits the aarch64 `brk #imm16` software breakpoint. The lowerer
// uses this for `mir.UnreachableTerm` — match exhaustiveness adds an
// unreachable default arm whose PC must abort rather than fall through
// into the next function. `brk #1` is the conventional "trap" value;
// userspace receives SIGTRAP / SIGILL depending on the platform.
type Brk struct {
	Imm int64 // 16-bit unsigned immediate (0..65535)
}

func (*Brk) instrNode() {}

// LoadStackAddress materialises the address of a stack slot into an
// x-register: `add Xd, sp, #imm`. The dev backend uses this for the
// AAPCS64 indirect (sret) ABI — the caller stages the address of the
// caller-allocated return buffer or the address of an
// indirect-by-reference argument slot here before branching.
type LoadStackAddress struct {
	Dst    Reg
	Offset int64
}

func (*LoadStackAddress) instrNode() {}

// LoadFromReg loads a 64-bit word from `[Src + Offset]` into Dst
// (`ldr Xt, [Xn, #imm]`). The dev backend uses this for the indirect
// ABI's caller-side memcpy: the prologue receives a pointer in an arg
// register, then loads each field through it before storing back into
// the local slot.
type LoadFromReg struct {
	Dst    Reg
	Src    Reg
	Offset int64
}

func (*LoadFromReg) instrNode() {}

// StoreToReg stores a 64-bit word from Src into `[Base + Offset]`
// (`str Xt, [Xn, #imm]`). Used by sret epilogues to write each
// return-slot half through the caller's buffer pointer in x8.
type StoreToReg struct {
	Src    Reg
	Base   Reg
	Offset int64
}

func (*StoreToReg) instrNode() {}

// LoadFloat64Stack loads a Float64 (8-byte slot) from the call frame
// into a d-register. The encoder picks the `ldr d, [sp, #N]` form
// when N is 8-byte aligned and ≤ 32760.
type LoadFloat64Stack struct {
	Dst    Reg
	Offset int64
}

func (*LoadFloat64Stack) instrNode() {}

// StoreFloat64Stack stores a d-register's 8 bytes into the call frame.
// Mirrors `Store64Stack` for FP values.
type StoreFloat64Stack struct {
	Src    Reg
	Offset int64
}

func (*StoreFloat64Stack) instrNode() {}

// FmovDFromX bitcasts a 64-bit integer register into a d-register
// (`fmov d, x`). Used to materialise an arbitrary Float64 immediate:
// the encoder emits a movz/movk chain into x9 followed by `fmov d9, x9`.
type FmovDFromX struct {
	Dst Reg // a d register
	Src Reg // an x register
}

func (*FmovDFromX) instrNode() {}

// FmovXFromD bitcasts a d-register into a 64-bit integer register
// (`fmov x, d`). Used to land a Float64 value into the printf vararg
// slot, which on darwin/aarch64 reads from the integer x register
// stored at [sp+0].
type FmovXFromD struct {
	Dst Reg
	Src Reg
}

func (*FmovXFromD) instrNode() {}

// FaddReg / FsubReg / FmulReg / FdivReg are the four IEEE 754 binary
// ops the dev backend covers today: `f<op> Dd, Dn, Dm`. Operands are
// already in d-registers via LoadFloat64Stack or FmovDFromX.
type FaddReg struct {
	Dst Reg
	Lhs Reg
	Rhs Reg
}

func (*FaddReg) instrNode() {}

type FsubReg struct {
	Dst Reg
	Lhs Reg
	Rhs Reg
}

func (*FsubReg) instrNode() {}

type FmulReg struct {
	Dst Reg
	Lhs Reg
	Rhs Reg
}

func (*FmulReg) instrNode() {}

type FdivReg struct {
	Dst Reg
	Lhs Reg
	Rhs Reg
}

func (*FdivReg) instrNode() {}

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

// functionPrologueWords returns the number of 4-byte prologue instructions
// the encoder inserts at the start of a function. Lives next to its
// frame-size siblings so any future change to the prologue layout is
// expressed in one place — both the Mach-O encoder (which emits the
// prologue) and the DWARF line emitter (which has to step past it for
// PC→source rows) read this single source of truth.
func functionPrologueWords(fn Function) int {
	if functionFrameSize(fn) == 0 {
		return 0
	}
	if functionFPOffset(fn) < 0 {
		return 2 // stp x29, x30, [sp, #-16]!  +  mov x29, sp
	}
	return 3 // sub sp, sp, #N  +  stp x29, x30, [sp, #N-16]  +  add x29, sp, #N-16
}
