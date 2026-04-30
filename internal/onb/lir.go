package onb

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
type Function struct {
	Name   string
	Blocks []Block
}

// Block is a linear basic block.
type Block struct {
	Label  string
	Instrs []Instr
}

// Instr is one ONB aarch64 LIR instruction.
type Instr interface {
	instrNode()
}

// Reg names an aarch64 architectural register in the current minimal LIR.
type Reg string

const (
	RegX0 Reg = "x0"
	RegX1 Reg = "x1"
	RegW0 Reg = "w0"
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

// Store64Stack stores a 64-bit register into the current call frame. Darwin
// aarch64 uses this for C varargs such as printf's integer argument area.
type Store64Stack struct {
	Src    Reg
	Offset int64
}

func (*Store64Stack) instrNode() {}

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
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if _, ok := instr.(*BranchLink); ok {
				return true
			}
		}
	}
	return false
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
