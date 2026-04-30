// Package lirproto contains the isolated prototype for a low-level IR plan.
// It is deliberately not wired into production backend dispatch.
package lirproto

import "strconv"

// Config carries the LLVM text-shape options LIR Proto needs while lowering
// from MIR. It intentionally does not reuse llvmgen.Options to avoid coupling
// this package back to its parent package.
type Config struct {
	PackageName string
	SourcePath  string
	Target      string
	EmitGC      bool
	FeatureGate map[string]bool
}

// TypeClass is the small semantic tag carried next to the LLVM spelling.
type TypeClass int

const (
	TypeUnknown TypeClass = iota
	TypeVoid
	TypeInt
	TypeFloat
	TypePtr
	TypeAggregate
	TypeFunction
)

// Type is a hybrid type representation: LLVM keeps the exact renderer
// spelling, while Class carries the minimal semantic bucket needed for early
// validation and future ABI planning.
type Type struct {
	LLVM  string
	Class TypeClass
}

// String returns the LLVM spelling.
func (t Type) String() string {
	return t.LLVM
}

// IsZero reports whether no type was supplied.
func (t Type) IsZero() bool {
	return t.LLVM == "" && t.Class == TypeUnknown
}

// RawType keeps an exact LLVM spelling when the class is not known yet.
func RawType(llvm string) Type {
	return Type{LLVM: llvm}
}

// IntType returns an integer type with the given bit width.
func IntType(bits int) Type {
	return Type{LLVM: "i" + strconv.Itoa(bits), Class: TypeInt}
}

// FloatType returns a floating-point type with the given LLVM spelling.
func FloatType(llvm string) Type {
	return Type{LLVM: llvm, Class: TypeFloat}
}

// PtrType returns LLVM's opaque pointer type.
func PtrType() Type {
	return Type{LLVM: "ptr", Class: TypePtr}
}

// VoidType returns LLVM void.
func VoidType() Type {
	return Type{LLVM: "void", Class: TypeVoid}
}

// AggregateType returns a named or literal aggregate type.
func AggregateType(llvm string) Type {
	return Type{LLVM: llvm, Class: TypeAggregate}
}

// Module is the deterministic, renderable LIR Proto unit.
type Module struct {
	PackageName  string
	SourcePath   string
	Target       string
	TypeDefs     []TypeDef
	Globals      []Global
	Functions    []Function
	RuntimeDecls RuntimeDecls
	StringPool   StringPool
	Metadata     []string
}

// TypeDef is a module-level LLVM type definition.
type TypeDef struct {
	Name string
	Body string
}

// Global is a module-level LLVM global definition.
type Global struct {
	Name  string
	Type  Type
	Init  string
	Attrs []string
}

// Function is a renderable LLVM function definition.
type Function struct {
	Name        string
	Return      Type
	Params      []Param
	Blocks      []Block
	Linkage     string
	CallingConv string
	Attrs       []string
}

// Param is an LLVM function parameter.
type Param struct {
	Name string
	Type Type
}

// Block is a basic block in render order.
type Block struct {
	Label  string
	Instrs []Instr
	Term   Term
}

// Operand is a typed LLVM value operand. Phase 1 keeps Value as the LLVM-level
// spelling (`%reg`, `@global`, literal text) while the instruction nodes own
// the typed shape; split value identity into richer node types only once MIR
// lowering proves which invariants matter.
type Operand struct {
	Type  Type
	Value string
}

// Instr is one typed low-level instruction. The unexported method seals the
// vocabulary to this package; add new instruction variants here instead of
// implementing them from callers.
type Instr interface {
	instrNode()
	renderLLVM() string
}

// Alloca reserves a stack slot.
type Alloca struct {
	Dest  string
	Type  Type
	Align int
}

// Load reads from a pointer.
type Load struct {
	Dest  string
	Type  Type
	Ptr   string
	Align int
}

// Store writes a value to a pointer.
type Store struct {
	Value Operand
	Ptr   string
	Align int
}

// Binary computes a binary operation on same-typed operands.
type Binary struct {
	Dest  string
	Op    string
	Type  Type
	Left  string
	Right string
}

// Unary computes a unary operation on one typed operand.
type Unary struct {
	Dest  string
	Op    string
	Type  Type
	Value string
}

// Call invokes a direct or indirect callee. Callee includes the LLVM spelling,
// such as "@fn" or a function pointer register.
type Call struct {
	Dest        string
	Return      Type
	Callee      string
	Args        []Operand
	CallingConv string
	Attrs       []string
}

// Cast converts a value between LLVM types.
type Cast struct {
	Dest string
	Op   string
	From Operand
	To   Type
}

// InsertValue inserts a field into an aggregate SSA value.
type InsertValue struct {
	Dest      string
	Type      Type
	Aggregate string
	Value     Operand
	Indices   []int
}

// ExtractValue extracts a field from an aggregate SSA value.
type ExtractValue struct {
	Dest      string
	Type      Type
	Aggregate string
	Indices   []int
}

// Gep computes a getelementptr result.
type Gep struct {
	Dest     string
	ElemType Type
	Ptr      string
	Indices  []Operand
	Inbounds bool
}

// Comment renders an LLVM comment line.
type Comment struct {
	Text string
}

func (Alloca) instrNode()       {}
func (Load) instrNode()         {}
func (Store) instrNode()        {}
func (Binary) instrNode()       {}
func (Unary) instrNode()        {}
func (Call) instrNode()         {}
func (Cast) instrNode()         {}
func (InsertValue) instrNode()  {}
func (ExtractValue) instrNode() {}
func (Gep) instrNode()          {}
func (Comment) instrNode()      {}

// Term is one typed block terminator. Like Instr, it is sealed so validation
// and rendering can exhaustively own the control-flow vocabulary.
type Term interface {
	termNode()
	renderLLVM() string
}

// Ret returns from the current function. Empty Type and Value render as
// `ret void`.
type Ret struct {
	Type  Type
	Value string
}

// Br jumps to another block.
type Br struct {
	Target string
}

// CondBr branches on an i1 condition.
type CondBr struct {
	Cond string
	Then string
	Else string
}

// SwitchCase is one switch edge.
type SwitchCase struct {
	Value string
	Label string
}

// Switch branches by integer-like value.
type Switch struct {
	Type      Type
	Scrutinee string
	Default   string
	Cases     []SwitchCase
}

// Unreachable marks control flow that cannot continue.
type Unreachable struct{}

func (Ret) termNode()         {}
func (Br) termNode()          {}
func (CondBr) termNode()      {}
func (Switch) termNode()      {}
func (Unreachable) termNode() {}

// RuntimeDecl is a forward declaration for a runtime symbol.
type RuntimeDecl struct {
	Symbol string
	Return Type
	Params []Type
	Vararg bool
}

// RuntimeDecls owns declaration deduplication while preserving first-seen
// order.
type RuntimeDecls struct {
	order []string
	bySym map[string]RuntimeDecl
}

// Declare records a runtime declaration. It returns false when the symbol was
// already declared.
func (d *RuntimeDecls) Declare(decl RuntimeDecl) bool {
	if decl.Symbol == "" {
		return false
	}
	key := decl.Symbol
	if d.bySym == nil {
		d.bySym = map[string]RuntimeDecl{}
	}
	if _, ok := d.bySym[key]; ok {
		return false
	}
	d.bySym[key] = decl
	d.order = append(d.order, key)
	return true
}

// Ordered returns declarations in first-seen order.
func (d RuntimeDecls) Ordered() []RuntimeDecl {
	out := make([]RuntimeDecl, 0, len(d.order))
	for _, key := range d.order {
		out = append(out, d.bySym[key])
	}
	return out
}

// IsEmpty reports whether no declarations were recorded.
func (d RuntimeDecls) IsEmpty() bool {
	return len(d.order) == 0
}

// StringGlobal is one interned string constant.
type StringGlobal struct {
	Symbol  string
	Content string
	Encoded string
	ByteLen int
}

// StringPool owns string interning while preserving first-seen order.
type StringPool struct {
	byContent map[string]string
	order     []string
}

// Intern returns the stable symbol for content.
func (p *StringPool) Intern(content string) string {
	if p.byContent == nil {
		p.byContent = map[string]string{}
	}
	if sym, ok := p.byContent[content]; ok {
		return sym
	}
	sym := "@.str." + strconv.Itoa(len(p.order))
	p.byContent[content] = sym
	p.order = append(p.order, content)
	return sym
}

// Symbol returns the previously interned symbol for content.
func (p StringPool) Symbol(content string) string {
	if p.byContent == nil {
		return ""
	}
	return p.byContent[content]
}

// Ordered returns string globals in first-seen order.
func (p StringPool) Ordered() []StringGlobal {
	out := make([]StringGlobal, 0, len(p.order))
	for _, content := range p.order {
		out = append(out, StringGlobal{
			Symbol:  p.byContent[content],
			Content: content,
			Encoded: llvmCString(content),
			ByteLen: len([]byte(content)) + 1,
		})
	}
	return out
}

// IsEmpty reports whether no strings were interned.
func (p StringPool) IsEmpty() bool {
	return len(p.order) == 0
}
