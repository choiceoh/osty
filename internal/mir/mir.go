package mir

import (
	"fmt"

	"github.com/osty/osty/internal/ir"
)

// ==== Source positions (shared with HIR) ====

// Pos mirrors ir.Pos; kept as a local type so MIR consumers do not need
// to import `ir` for position info even though today we just alias it.
type Pos = ir.Pos

// Span is a half-open [Start, End) source range. Every MIR node carries
// one so diagnostics can still anchor back to the original source.
type Span = ir.Span

// ==== Type reuse ====

// Type is a semantic type. We reuse ir.Type directly because (a) it has
// no back-references to the AST/resolver, (b) monomorphisation runs at
// the HIR level and every type reaching MIR is already concrete, and
// (c) having one type vocabulary removes an entire translation layer
// between HIR and MIR.
type Type = ir.Type

// Canonical primitive singletons, re-exported so MIR consumers never
// need to reach into ir.T* themselves.
var (
	TInt     = ir.TInt
	TInt8    = ir.TInt8
	TInt16   = ir.TInt16
	TInt32   = ir.TInt32
	TInt64   = ir.TInt64
	TUInt8   = ir.TUInt8
	TUInt16  = ir.TUInt16
	TUInt32  = ir.TUInt32
	TUInt64  = ir.TUInt64
	TByte    = ir.TByte
	TFloat   = ir.TFloat
	TFloat32 = ir.TFloat32
	TFloat64 = ir.TFloat64
	TBool    = ir.TBool
	TChar    = ir.TChar
	TString  = ir.TString
	TBytes   = ir.TBytes
	TUnit    = ir.TUnit
	TNever   = ir.TNever
)

// ==== Module ====

// Module is the MIR form of an Osty compilation unit. It is always
// monomorphic: no TypeVar appears in any type reachable from the module
// root.
type Module struct {
	Package   string
	Functions []*Function
	Globals   []*Global
	Uses      []*Use
	Layouts   *LayoutTable
	Issues    []error
	SpanV     Span
}

// At returns the module's source span.
func (m *Module) At() Span { return m.SpanV }

// LookupFunction finds a function by mangled symbol. Returns nil when
// no match exists.
func (m *Module) LookupFunction(symbol string) *Function {
	if m == nil {
		return nil
	}
	for _, fn := range m.Functions {
		if fn != nil && fn.Name == symbol {
			return fn
		}
	}
	return nil
}

// ==== Globals / Uses ====

// Global is a top-level `let` binding. The initialiser is a MIR
// function — the global's value is whatever the function returns. This
// matches how LLVM lowers top-level state today: the backend emits an
// initialiser that runs before main.
type Global struct {
	Name  string
	Type  Type
	Init  *Function // nil when the caller has not lowered an init yet
	Mut   bool
	SpanV Span
}

// At returns the global's span.
func (g *Global) At() Span { return g.SpanV }

// Use represents an import that MIR still needs to carry for FFI
// bridging (Go FFI modules, runtime aliases). Non-FFI imports are
// fully resolved away during MIR lowering.
type Use struct {
	Path         []string
	RawPath      string
	Alias        string
	IsGoFFI      bool
	IsRuntimeFFI bool
	GoPath       string
	RuntimePath  string
	SpanV        Span
}

// At returns the import's span.
func (u *Use) At() Span { return u.SpanV }

// ==== Function ====

// LocalID identifies a local within a function. _0 is always the
// return slot; IDs are dense and contiguous from 0.
type LocalID int

// BlockID identifies a basic block within a function. The entry block
// is whatever Function.Entry points at (conventionally 0).
type BlockID int

// Function is one MIR function: parameters, return slot, a set of
// named locals, a CFG of basic blocks.
type Function struct {
	Name        string // mangled symbol
	Params      []LocalID
	ReturnType  Type
	ReturnLocal LocalID
	Locals      []*Local
	Blocks      []*BasicBlock
	Entry       BlockID
	IsExternal  bool
	IsIntrinsic bool
	Exported    bool
	SpanV       Span

	// ExportSymbol is the verbatim symbol name set by `#[export("name")]`
	// (LANG_SPEC §19.6). When non-empty, the LLVM emitter writes
	// `@<ExportSymbol>` instead of `@<Name>`, bypassing all mangling
	// so the function can satisfy the runtime ABI contract.
	ExportSymbol string

	// CABI is set when the function carries `#[c_abi]` (LANG_SPEC
	// §19.6). The LLVM emitter inserts the `ccc` calling-convention
	// keyword in the `define`/`declare` line so the function uses
	// the platform's C calling convention.
	CABI bool
}

// At returns the function's source span.
func (f *Function) At() Span { return f.SpanV }

// NewLocal appends a fresh local and returns its ID.
func (f *Function) NewLocal(name string, t Type, mut bool, sp Span) LocalID {
	id := LocalID(len(f.Locals))
	f.Locals = append(f.Locals, &Local{
		ID:    id,
		Name:  name,
		Type:  t,
		Mut:   mut,
		SpanV: sp,
	})
	return id
}

// Local returns the local with the given ID, or nil if the ID is out
// of range.
func (f *Function) Local(id LocalID) *Local {
	if int(id) < 0 || int(id) >= len(f.Locals) {
		return nil
	}
	return f.Locals[id]
}

// Block returns the block with the given ID, or nil if the ID is out
// of range.
func (f *Function) Block(id BlockID) *BasicBlock {
	if int(id) < 0 || int(id) >= len(f.Blocks) {
		return nil
	}
	return f.Blocks[id]
}

// NewBlock appends a fresh block with no instructions and a nil
// terminator. Callers must install a terminator before validation.
func (f *Function) NewBlock(sp Span) BlockID {
	id := BlockID(len(f.Blocks))
	f.Blocks = append(f.Blocks, &BasicBlock{ID: id, SpanV: sp})
	return id
}

// ==== Local ====

// Local is one slot in a function's frame. Locals cover parameters,
// the return slot, user-named bindings, and compiler temporaries.
type Local struct {
	ID       LocalID
	Name     string // empty for synthetic temporaries
	Type     Type
	Mut      bool
	IsParam  bool
	IsReturn bool
	SpanV    Span
}

// At returns the local's source span.
func (l *Local) At() Span { return l.SpanV }

// ==== Basic block ====

// BasicBlock is a straight-line run of instructions followed by
// exactly one terminator. Terminator must be non-nil after lowering.
type BasicBlock struct {
	ID     BlockID
	Instrs []Instr
	Term   Terminator
	SpanV  Span
}

// At returns the block's source span (the span of the first
// instruction, or the block header if the block is empty).
func (b *BasicBlock) At() Span { return b.SpanV }

// Append adds an instruction to the block. Callers are expected to
// respect the invariant that no instruction follows the terminator —
// the validator catches violations, but the helper does not.
func (b *BasicBlock) Append(instr Instr) {
	b.Instrs = append(b.Instrs, instr)
}

// SetTerminator installs the block's terminator. Subsequent Append
// calls are a MIR bug; the validator flags them.
func (b *BasicBlock) SetTerminator(t Terminator) {
	b.Term = t
}

// ==== Instructions ====

// Instr is the common interface for every MIR instruction.
type Instr interface {
	instrNode()
	At() Span
}

// AssignInstr is `dest = rvalue`. The dest Place must reference a
// local that already exists; projections are allowed (field / tuple /
// variant / index / deref).
type AssignInstr struct {
	Dest  Place
	Src   RValue
	SpanV Span
}

func (*AssignInstr) instrNode()  {}
func (a *AssignInstr) At() Span  { return a.SpanV }

// CallInstr is a direct or indirect call. Dest is nil when the return
// value is discarded or when the function returns unit.
type CallInstr struct {
	Dest   *Place
	Callee Callee
	Args   []Operand
	SpanV  Span
}

func (*CallInstr) instrNode() {}
func (c *CallInstr) At() Span { return c.SpanV }

// IntrinsicInstr is a call to a compiler-known intrinsic (the print
// family today). Keeping intrinsics distinct from user calls lets
// backends dispatch without matching on names.
type IntrinsicInstr struct {
	Dest  *Place
	Kind  IntrinsicKind
	Args  []Operand
	SpanV Span
}

func (*IntrinsicInstr) instrNode() {}
func (i *IntrinsicInstr) At() Span { return i.SpanV }

// IntrinsicKind enumerates the MIR-visible intrinsics. The values
// match ir.IntrinsicKind for the print family so that the lowerer can
// pass them through unchanged.
//
// Intrinsics split into broad families:
//
//   - print family: formatted output to stdout/stderr.
//   - string / runtime builders: helpers the lowerer cannot inline.
//   - concurrency: the runtime ABI for `taskGroup`, channels, spawn,
//     handles, select, cancellation. Stage 2b introduces these. The
//     runtime itself is not contract-frozen yet; backends read the
//     intrinsic name and route to whatever symbol their runtime
//     exposes.
type IntrinsicKind int

const (
	IntrinsicInvalid  IntrinsicKind = iota
	IntrinsicPrint                  // stdout, no newline
	IntrinsicPrintln                // stdout, newline
	IntrinsicEprint                 // stderr, no newline
	IntrinsicEprintln               // stderr, newline
	IntrinsicAbort                  // unreachable runtime trap
	IntrinsicStringConcat

	// ---- concurrency: channels ----

	// IntrinsicChanMake constructs a new Channel<T>. Args: [capacity
	// Int]. Dest receives the new channel value; the element type is
	// read from the destination local's type.
	IntrinsicChanMake
	// IntrinsicChanSend sends a value on a channel. Args: [channel,
	// value]. Dest is nil (the statement has no value).
	IntrinsicChanSend
	// IntrinsicChanRecv receives a value from a channel. Args:
	// [channel]. Dest receives Option<T> — None signals the channel
	// is drained and closed, or a cancellation was observed.
	IntrinsicChanRecv
	// IntrinsicChanClose closes a channel. Args: [channel]. Dest nil.
	IntrinsicChanClose
	// IntrinsicChanIsClosed returns Bool. Args: [channel].
	IntrinsicChanIsClosed

	// ---- concurrency: structured tasks ----

	// IntrinsicTaskGroup wraps a `taskGroup(|g| body)` call. Args:
	// [closure]. Dest receives the closure's return value.
	IntrinsicTaskGroup
	// IntrinsicSpawn launches a task. Args: [closure] (detached) or
	// [group, closure] (`g.spawn`). Dest receives a Handle<T>.
	IntrinsicSpawn
	// IntrinsicHandleJoin joins on a handle. Args: [handle]. Dest is
	// the handle's T value.
	IntrinsicHandleJoin
	// IntrinsicGroupCancel cancels a group. Args: [group]. Dest nil.
	IntrinsicGroupCancel
	// IntrinsicGroupIsCancelled reports whether a group is cancelled.
	// Args: [group]. Dest is Bool.
	IntrinsicGroupIsCancelled

	// ---- concurrency: high-level helpers ----

	// IntrinsicParallel runs `parallel(items, concurrency, f)`. Args:
	// [items, concurrency, f]. Dest is List<Result<R, Error>>.
	IntrinsicParallel
	// IntrinsicRace runs `race(body)`. Args: [body]. Dest is
	// Result<T, Error>.
	IntrinsicRace
	// IntrinsicCollectAll runs `collectAll(body)`. Args: [body]. Dest
	// is List<Result<T, Error>>.
	IntrinsicCollectAll

	// ---- concurrency: select ----

	// IntrinsicSelect runs `thread.select(|s| body)`. Args: [body].
	// Dest is unit; individual arms (recv/send/timeout/default) run
	// their closures through the select runtime.
	IntrinsicSelect
	// IntrinsicSelectRecv registers a `s.recv(ch, f)` arm on a
	// Select builder. Args: [select, channel, callback]. Dest nil.
	IntrinsicSelectRecv
	// IntrinsicSelectSend registers a `s.send(ch, v, f)` arm on a
	// Select builder. Args: [select, channel, value, callback].
	// Dest nil.
	IntrinsicSelectSend
	// IntrinsicSelectTimeout registers a `s.timeout(d, f)` arm on a
	// Select builder. Args: [select, duration, callback]. Dest nil.
	IntrinsicSelectTimeout
	// IntrinsicSelectDefault registers a `s.default(f)` arm on a
	// Select builder. Args: [select, callback]. Dest nil.
	IntrinsicSelectDefault

	// ---- concurrency: cancellation ----

	// IntrinsicIsCancelled returns Bool. Args: [].
	IntrinsicIsCancelled
	// IntrinsicCheckCancelled returns Result<(), Error>. Args: [].
	IntrinsicCheckCancelled
	// IntrinsicYield yields the current task. Args: [].
	IntrinsicYield
	// IntrinsicSleep sleeps for a Duration. Args: [duration].
	IntrinsicSleep

	// ---- stdlib collections: List<T> ----
	//
	// Stage 2d surfaces the common prelude/stdlib methods so a MIR-
	// consuming backend can dispatch directly to the runtime ABI
	// without re-recognising user-visible method names. The element
	// type flows through the receiver operand (and the return type
	// of methods that produce a new value); backends pick the right
	// specialised runtime symbol from the type string.

	// IntrinsicListPush appends to a list. Args: [list, elem]. Dest nil.
	IntrinsicListPush
	// IntrinsicListLen returns the list length as Int. Args: [list].
	IntrinsicListLen
	// IntrinsicListGet indexes a list and aborts on out-of-bounds
	// (matching the stdlib `list[i]` semantics). Args: [list, idx].
	// Dest is T.
	IntrinsicListGet
	// IntrinsicListIsEmpty returns Bool. Args: [list].
	IntrinsicListIsEmpty
	// IntrinsicListFirst returns the first element as T?. Args: [list].
	IntrinsicListFirst
	// IntrinsicListLast returns the last element as T?. Args: [list].
	IntrinsicListLast
	// IntrinsicListSorted returns a sorted copy. Args: [list].
	IntrinsicListSorted
	// IntrinsicListContains returns Bool. Args: [list, elem].
	IntrinsicListContains
	// IntrinsicListIndexOf returns Int?. Args: [list, elem].
	IntrinsicListIndexOf
	// IntrinsicListToSet constructs a Set<T> from a List<T>. Args: [list].
	IntrinsicListToSet

	// ---- stdlib collections: Map<K, V> ----

	// IntrinsicMapNew constructs an empty Map<K, V>. Args: []. The
	// key/value types come from the destination local's type.
	IntrinsicMapNew
	// IntrinsicMapGet returns V? for a lookup. Args: [map, key].
	IntrinsicMapGet
	// IntrinsicMapSet inserts or overwrites. Args: [map, key, value].
	// Dest nil.
	IntrinsicMapSet
	// IntrinsicMapContains returns Bool. Args: [map, key].
	IntrinsicMapContains
	// IntrinsicMapLen returns Int. Args: [map].
	IntrinsicMapLen
	// IntrinsicMapKeys returns List<K>. Args: [map].
	IntrinsicMapKeys
	// IntrinsicMapValues returns List<V>. Args: [map].
	IntrinsicMapValues
	// IntrinsicMapRemove deletes a key. Args: [map, key]. Dest nil.
	IntrinsicMapRemove

	// ---- stdlib collections: Set<T> ----

	// IntrinsicSetNew constructs an empty Set<T>. Args: []. Element
	// type flows through the destination's type.
	IntrinsicSetNew
	// IntrinsicSetInsert adds an element. Args: [set, elem]. Dest nil.
	IntrinsicSetInsert
	// IntrinsicSetContains returns Bool. Args: [set, elem].
	IntrinsicSetContains
	// IntrinsicSetLen returns Int. Args: [set].
	IntrinsicSetLen
	// IntrinsicSetToList materialises a List<T>. Args: [set].
	IntrinsicSetToList

	// ---- stdlib: String ----

	// IntrinsicStringLen returns Int (byte count). Args: [string].
	IntrinsicStringLen
	// IntrinsicStringIsEmpty returns Bool. Args: [string].
	IntrinsicStringIsEmpty
	// IntrinsicStringContains returns Bool. Args: [string, needle].
	IntrinsicStringContains
	// IntrinsicStringStartsWith returns Bool. Args: [string, prefix].
	IntrinsicStringStartsWith
	// IntrinsicStringEndsWith returns Bool. Args: [string, suffix].
	IntrinsicStringEndsWith
	// IntrinsicStringIndexOf returns Int?. Args: [string, needle].
	IntrinsicStringIndexOf
	// IntrinsicStringSplit returns List<String>. Args: [string, sep].
	IntrinsicStringSplit
	// IntrinsicStringTrim returns String with surrounding whitespace
	// stripped. Args: [string].
	IntrinsicStringTrim
	// IntrinsicStringToUpper returns uppercased String. Args: [string].
	IntrinsicStringToUpper
	// IntrinsicStringToLower returns lowercased String. Args: [string].
	IntrinsicStringToLower
	// IntrinsicStringReplace returns String with all occurrences of
	// `old` replaced by `new`. Args: [string, old, new].
	IntrinsicStringReplace
	// IntrinsicStringChars returns List<Char>. Args: [string].
	IntrinsicStringChars
	// IntrinsicStringBytes returns List<Byte>. Args: [string].
	IntrinsicStringBytes

	// ---- stdlib: Bytes ----

	// IntrinsicBytesLen returns Int. Args: [bytes].
	IntrinsicBytesLen
	// IntrinsicBytesIsEmpty returns Bool. Args: [bytes].
	IntrinsicBytesIsEmpty
	// IntrinsicBytesGet returns Byte?. Args: [bytes, idx].
	IntrinsicBytesGet

	// ---- stdlib: Option / Result ----

	// IntrinsicOptionIsSome returns Bool. Args: [option].
	IntrinsicOptionIsSome
	// IntrinsicOptionIsNone returns Bool. Args: [option].
	IntrinsicOptionIsNone
	// IntrinsicOptionUnwrap returns T, aborting on None. Args: [option].
	IntrinsicOptionUnwrap
	// IntrinsicOptionUnwrapOr returns T. Args: [option, default].
	IntrinsicOptionUnwrapOr
	// IntrinsicResultIsOk returns Bool. Args: [result].
	IntrinsicResultIsOk
	// IntrinsicResultIsErr returns Bool. Args: [result].
	IntrinsicResultIsErr
	// IntrinsicResultUnwrap returns T, aborting on Err. Args: [result].
	IntrinsicResultUnwrap
	// IntrinsicResultUnwrapOr returns T. Args: [result, default].
	IntrinsicResultUnwrapOr

	// ---- LANG_SPEC §19 runtime sublanguage ----

	// IntrinsicRawNull returns the null RawPtr. Args: [].
	// Lowers to `inttoptr i64 0 to ptr` (or `ptr null`).
	IntrinsicRawNull
)

// StorageLiveInstr marks a local as alive. Optional; backends that do
// not care may ignore it.
type StorageLiveInstr struct {
	Local LocalID
	SpanV Span
}

func (*StorageLiveInstr) instrNode() {}
func (s *StorageLiveInstr) At() Span { return s.SpanV }

// StorageDeadInstr marks a local as dead. Paired with StorageLive.
type StorageDeadInstr struct {
	Local LocalID
	SpanV Span
}

func (*StorageDeadInstr) instrNode() {}
func (s *StorageDeadInstr) At() Span { return s.SpanV }

// ==== Callee ====

// Callee is the target of a CallInstr.
type Callee interface {
	calleeNode()
}

// FnRef is a direct call to a function by its mangled symbol. Type
// carries the full function type for the validator's benefit.
type FnRef struct {
	Symbol string
	Type   Type
}

func (*FnRef) calleeNode() {}

// IndirectCall is a call through a first-class function value. The
// operand's type is expected to be an FnType.
type IndirectCall struct {
	Callee Operand
}

func (*IndirectCall) calleeNode() {}

// ==== Terminators ====

// Terminator is the control-flow edge leaving a basic block. Every
// block has exactly one.
type Terminator interface {
	termNode()
	At() Span
}

// GotoTerm is an unconditional jump.
type GotoTerm struct {
	Target BlockID
	SpanV  Span
}

func (*GotoTerm) termNode()  {}
func (g *GotoTerm) At() Span { return g.SpanV }

// BranchTerm chooses between two successors based on a boolean
// operand.
type BranchTerm struct {
	Cond  Operand
	Then  BlockID
	Else  BlockID
	SpanV Span
}

func (*BranchTerm) termNode() {}
func (b *BranchTerm) At() Span { return b.SpanV }

// SwitchIntTerm dispatches on an integer scrutinee. Cases are tried
// in order; Default is taken when no case matches.
type SwitchIntTerm struct {
	Scrutinee Operand
	Cases     []SwitchCase
	Default   BlockID
	SpanV     Span
}

func (*SwitchIntTerm) termNode() {}
func (s *SwitchIntTerm) At() Span { return s.SpanV }

// SwitchCase is one match arm of a SwitchIntTerm.
type SwitchCase struct {
	Value  int64
	Target BlockID
	Label  string // optional debug label (variant name / literal text)
}

// ReturnTerm returns from the function. The returned value is the
// contents of the function's ReturnLocal.
type ReturnTerm struct {
	SpanV Span
}

func (*ReturnTerm) termNode()  {}
func (r *ReturnTerm) At() Span { return r.SpanV }

// UnreachableTerm marks a path that the compiler believes is
// unreachable. Emitted at the "no arm matched" sink of an exhaustive
// match, after `!`-returning calls, etc.
type UnreachableTerm struct {
	SpanV Span
}

func (*UnreachableTerm) termNode() {}
func (u *UnreachableTerm) At() Span { return u.SpanV }

// ==== Places / projections ====

// Place identifies a storage location. It is a local optionally
// refined by a chain of projections.
type Place struct {
	Local       LocalID
	Projections []Projection
}

// Base returns a Place with just the root local and no projections.
// Useful as a concise way to describe the storage root.
func (p Place) Base() Place { return Place{Local: p.Local} }

// HasProjections reports whether the place has any refining
// projections.
func (p Place) HasProjections() bool { return len(p.Projections) > 0 }

// Project returns a new Place extending p with proj.
func (p Place) Project(proj Projection) Place {
	next := make([]Projection, 0, len(p.Projections)+1)
	next = append(next, p.Projections...)
	next = append(next, proj)
	return Place{Local: p.Local, Projections: next}
}

// Projection is one step refining a Place.
type Projection interface {
	projectionNode()
}

// FieldProj is a struct field access by index + name.
type FieldProj struct {
	Index int
	Name  string
	Type  Type
}

func (*FieldProj) projectionNode() {}

// TupleProj is a tuple element access by index.
type TupleProj struct {
	Index int
	Type  Type
}

func (*TupleProj) projectionNode() {}

// VariantProj descends into an enum payload. FieldIdx < 0 selects the
// whole payload tuple; otherwise it selects a single payload element.
type VariantProj struct {
	Variant  int
	Name     string
	FieldIdx int // -1 for "the whole payload"
	Type     Type
}

func (*VariantProj) projectionNode() {}

// IndexProj is `place[index]`. Index is an operand (usually an integer
// local or constant); ElemType is the resulting type.
type IndexProj struct {
	Index    Operand
	ElemType Type
}

func (*IndexProj) projectionNode() {}

// DerefProj follows a pointer / optional unwrap. Retained in the
// vocabulary for future borrow work; the current lowering does not
// emit it.
type DerefProj struct {
	Type Type
}

func (*DerefProj) projectionNode() {}

// ==== Operands ====

// Operand is a read-only value: either a Place read or a literal.
type Operand interface {
	operandNode()
	Type() Type
}

// CopyOp is a non-destructive read of a Place.
type CopyOp struct {
	Place Place
	T     Type
}

func (*CopyOp) operandNode()   {}
func (o *CopyOp) Type() Type   { return o.T }

// MoveOp is a destructive read. Under the current GC-managed runtime
// MoveOp and CopyOp behave identically; the distinction exists for
// future owning-pointer lowerings.
type MoveOp struct {
	Place Place
	T     Type
}

func (*MoveOp) operandNode() {}
func (o *MoveOp) Type() Type { return o.T }

// ConstOp is a compile-time constant.
type ConstOp struct {
	Const Const
	T     Type
}

func (*ConstOp) operandNode() {}
func (o *ConstOp) Type() Type {
	if o.T != nil {
		return o.T
	}
	if o.Const != nil {
		return o.Const.Type()
	}
	return ir.ErrTypeVal
}

// ==== Constants ====

// Const is a compile-time value. Every Const knows its type.
type Const interface {
	constNode()
	Type() Type
}

// IntConst is an integer constant. Value is the two's complement
// bit pattern reinterpreted as int64; signed vs unsigned is carried
// by the Type.
type IntConst struct {
	Value int64
	T     Type
}

func (*IntConst) constNode() {}
func (c *IntConst) Type() Type {
	if c.T == nil {
		return TInt
	}
	return c.T
}

// BoolConst is `true` / `false`.
type BoolConst struct {
	Value bool
}

func (*BoolConst) constNode() {}
func (*BoolConst) Type() Type { return TBool }

// FloatConst is a float constant.
type FloatConst struct {
	Value float64
	T     Type
}

func (*FloatConst) constNode() {}
func (c *FloatConst) Type() Type {
	if c.T == nil {
		return TFloat
	}
	return c.T
}

// StringConst is a non-interpolated string constant.
type StringConst struct {
	Value string
}

func (*StringConst) constNode() {}
func (*StringConst) Type() Type { return TString }

// CharConst is a single code point.
type CharConst struct {
	Value rune
}

func (*CharConst) constNode() {}
func (*CharConst) Type() Type { return TChar }

// ByteConst is a single byte.
type ByteConst struct {
	Value byte
}

func (*ByteConst) constNode() {}
func (*ByteConst) Type() Type { return TByte }

// UnitConst is the `()` zero-value.
type UnitConst struct{}

func (*UnitConst) constNode() {}
func (*UnitConst) Type() Type { return TUnit }

// NullConst is the canonical "none" constant used to seed optional
// locals. Backends emit their runtime's None representation when they
// see this.
type NullConst struct {
	T Type
}

func (*NullConst) constNode() {}
func (c *NullConst) Type() Type {
	if c.T == nil {
		return TUnit
	}
	return c.T
}

// FnConst is a compile-time function pointer literal. The symbol
// names an already-mangled MIR function.
type FnConst struct {
	Symbol string
	T      Type
}

func (*FnConst) constNode() {}
func (c *FnConst) Type() Type {
	if c.T == nil {
		return ir.ErrTypeVal
	}
	return c.T
}

// ==== RValues ====

// RValue is the right-hand side of an Assign. RValues never have side
// effects on their own; side effects become CallInstr or
// IntrinsicInstr.
type RValue interface {
	rvalueNode()
}

// UseRV is the identity rvalue: copy an operand into the destination.
type UseRV struct {
	Op Operand
}

func (*UseRV) rvalueNode() {}

// UnaryRV is `op(arg)`.
type UnaryRV struct {
	Op  UnaryOp
	Arg Operand
	T   Type
}

func (*UnaryRV) rvalueNode() {}

// BinaryRV is `lhs op rhs`.
type BinaryRV struct {
	Op    BinaryOp
	Left  Operand
	Right Operand
	T     Type
}

func (*BinaryRV) rvalueNode() {}

// AggregateKind enumerates the flavors of aggregate construction.
type AggregateKind int

const (
	AggTuple AggregateKind = iota + 1
	AggStruct
	AggEnumVariant
	AggList
	AggMap
	// AggClosure is a closure value: Fields[0] is a FnConst operand
	// naming the lifted closure body; Fields[1..] are the captured
	// operands, in the order given by the lifted function's prefix
	// parameters. Backends that want a flat fn-pointer fall back to
	// ignoring captures when the capture list is empty.
	AggClosure
)

// AggregateRV constructs a compound value. For EnumVariant, VariantIdx
// names the active arm and Fields is the (possibly empty) payload
// tuple. For Struct, Fields is in declaration order (the lowerer
// reorders keyword fields). For Tuple / List, Fields is positional.
type AggregateRV struct {
	Kind       AggregateKind
	Fields     []Operand
	T          Type
	VariantIdx int    // EnumVariant only
	VariantTag string // EnumVariant only (debug hint)
}

func (*AggregateRV) rvalueNode() {}

// DiscriminantRV reads the variant tag of an enum / optional value.
type DiscriminantRV struct {
	Place Place
	T     Type // the discriminant's numeric type
}

func (*DiscriminantRV) rvalueNode() {}

// LenRV reads the length of a list / string / bytes. Backends map
// this to their runtime's length query.
type LenRV struct {
	Place Place
	T     Type
}

func (*LenRV) rvalueNode() {}

// CastKind enumerates the MIR-visible casts. Stage-1 only needs
// integer sign/width and optional wrap/unwrap.
type CastKind int

const (
	CastInvalid     CastKind = iota
	CastIntResize            // widen/narrow between integer widths
	CastIntToFloat
	CastFloatToInt
	CastFloatResize
	CastOptionalWrap   // T -> T? (wrap into Some)
	CastOptionalUnwrap // T? -> T (checked earlier)
	CastBitcast
)

// CastRV is a value conversion.
type CastRV struct {
	Kind CastKind
	Arg  Operand
	From Type
	To   Type
}

func (*CastRV) rvalueNode() {}

// AddressOfRV takes the address of a Place. Unused in Stage 1;
// retained for future borrow analysis.
type AddressOfRV struct {
	Place Place
	T     Type
}

func (*AddressOfRV) rvalueNode() {}

// RefRV wraps a Place's value inside a reference-typed wrapper. Used
// by the lowerer to materialise `Some(x)` when we already have x in a
// place.
type RefRV struct {
	Place Place
	T     Type
}

func (*RefRV) rvalueNode() {}

// GlobalRefRV references a top-level `let` global by its symbol. The
// backend chooses how to materialise the value (static slot, init-on-
// first-use, etc.); MIR does not prescribe a strategy.
type GlobalRefRV struct {
	Name string
	T    Type
}

func (*GlobalRefRV) rvalueNode() {}

// NullaryRVKind enumerates nullary rvalues that backends must
// recognise by name.
type NullaryRVKind int

const (
	NullaryNone NullaryRVKind = iota + 1
)

// NullaryRV materialises a nullary value such as the canonical None
// for an Option type.
type NullaryRV struct {
	Kind NullaryRVKind
	T    Type
}

func (*NullaryRV) rvalueNode() {}

// ==== Unary / binary operators ====

// UnaryOp enumerates MIR unary operators. The vocabulary mirrors HIR.
type UnaryOp int

const (
	UnInvalid UnaryOp = iota
	UnNeg             // -x
	UnPlus            // +x (identity on numerics; kept for parity)
	UnNot             // !x (boolean)
	UnBitNot          // ~x
)

// BinaryOp enumerates MIR binary operators.
type BinaryOp int

const (
	BinInvalid BinaryOp = iota

	BinAdd
	BinSub
	BinMul
	BinDiv
	BinMod

	BinEq
	BinNeq
	BinLt
	BinLeq
	BinGt
	BinGeq

	BinAnd
	BinOr

	BinBitAnd
	BinBitOr
	BinBitXor
	BinShl
	BinShr
)

// ==== Layouts ====

// LayoutTable is the MIR-level index from type names to layout
// records. Structural types (tuples, lists) are keyed by their
// canonical string representation.
type LayoutTable struct {
	Structs map[string]*StructLayout
	Enums   map[string]*EnumLayout
	Tuples  map[string]*TupleLayout
}

// NewLayoutTable returns an empty layout table with initialised maps.
func NewLayoutTable() *LayoutTable {
	return &LayoutTable{
		Structs: map[string]*StructLayout{},
		Enums:   map[string]*EnumLayout{},
		Tuples:  map[string]*TupleLayout{},
	}
}

// StructLayout describes a single (possibly monomorphic) struct.
type StructLayout struct {
	Name    string
	Mangled string
	Fields  []FieldLayout
	Size    int // 0 when the backend computes it
	Align   int // 0 when the backend computes it
}

// FieldLayout is one entry inside a StructLayout or VariantLayout.
type FieldLayout struct {
	Index int
	Name  string
	Type  Type
}

// EnumLayout describes a single (possibly monomorphic) enum.
type EnumLayout struct {
	Name         string
	Mangled      string
	Discriminant Type
	Variants     []VariantLayout
}

// VariantLayout is one enum arm.
type VariantLayout struct {
	Index   int
	Name    string
	Payload []FieldLayout
}

// TupleLayout describes the structural layout of a tuple. Key is the
// canonical type string (e.g. "(Int, String)").
type TupleLayout struct {
	Key     string
	Mangled string
	Fields  []FieldLayout
}

// ==== Helpers for span propagation ====

// SpanOfInstr returns the span of an instruction, or a zero Span when
// nil.
func SpanOfInstr(i Instr) Span {
	if i == nil {
		return Span{}
	}
	return i.At()
}

// SpanOfTerm returns the span of a terminator, or a zero Span when
// nil.
func SpanOfTerm(t Terminator) Span {
	if t == nil {
		return Span{}
	}
	return t.At()
}

// ==== CFG helpers ====

// Successors returns the successor block IDs of a terminator in source
// order: Then before Else for BranchTerm, cases in declaration order
// followed by Default for SwitchIntTerm. ReturnTerm and UnreachableTerm
// return nil. A nil terminator also returns nil so callers can hand
// unfinished blocks in without guarding.
//
// The result is a fresh slice; callers are free to mutate it.
func Successors(t Terminator) []BlockID {
	switch x := t.(type) {
	case nil:
		return nil
	case *GotoTerm:
		return []BlockID{x.Target}
	case *BranchTerm:
		return []BlockID{x.Then, x.Else}
	case *SwitchIntTerm:
		out := make([]BlockID, 0, len(x.Cases)+1)
		for _, c := range x.Cases {
			out = append(out, c.Target)
		}
		out = append(out, x.Default)
		return out
	case *ReturnTerm, *UnreachableTerm:
		return nil
	default:
		return nil
	}
}

// ReachableBlocks returns the set of block IDs reachable from fn.Entry
// by walking terminator successors. Blocks not present in the returned
// map are orphans — either the lowerer dropped instructions after a
// terminator and never branched to the resulting block, or a later pass
// disconnected them.
//
// Returns nil when fn has no blocks or fn.Entry is out of range.
func ReachableBlocks(fn *Function) map[BlockID]bool {
	if fn == nil || len(fn.Blocks) == 0 {
		return nil
	}
	if int(fn.Entry) < 0 || int(fn.Entry) >= len(fn.Blocks) {
		return nil
	}
	seen := make(map[BlockID]bool, len(fn.Blocks))
	stack := []BlockID{fn.Entry}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[id] {
			continue
		}
		seen[id] = true
		bb := fn.Block(id)
		if bb == nil {
			continue
		}
		for _, next := range Successors(bb.Term) {
			if int(next) < 0 || int(next) >= len(fn.Blocks) {
				continue
			}
			if !seen[next] {
				stack = append(stack, next)
			}
		}
	}
	return seen
}

// ==== Diagnostics helper ====

// Unsupported returns a sentinel error signalling that MIR lowering
// has not implemented the given HIR shape. Callers use this to
// decide whether to fall back to the HIR path.
func Unsupported(format string, args ...any) error {
	return fmt.Errorf("mir: unsupported: "+format, args...)
}
