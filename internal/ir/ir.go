package ir

import "strings"

// ==== Source positions ====

// Pos is a 1-based line/column pair plus a byte offset into the source.
// Kept deliberately small so every IR node can embed one without a big
// memory footprint. Mirrors `token.Pos` structurally but lives in this
// package so `ir` has no dependency on `token`.
type Pos struct {
	Offset int
	Line   int
	Column int
}

// Span is a half-open source range [Start, End). Carried by every IR
// node so downstream consumers (error reporting, source maps) can point
// back at the original source without needing the AST.
type Span struct {
	Start Pos
	End   Pos
}

// Node is the common interface for every IR node. Callers that only
// need location info can walk a tree as []Node.
type Node interface {
	At() Span
}

// ==== Module ====

// Module is the IR form of a single Osty source file.
//
// Package is the emitted package name (e.g. "main" for executables).
// Decls are top-level declarations in source order. Script is non-empty
// when the file contained top-level statements that will be lowered
// into a synthetic main() body by the backend.
type Module struct {
	Package string
	Decls   []Decl
	Script  []Stmt
	SpanV   Span
}

func (m *Module) At() Span { return m.SpanV }

// ==== Types ====

// Type is a self-contained semantic type. Unlike the checker's
// `types.Type`, it holds no back-references to `resolve.Symbol`; named
// types are identified by string. Equality is by structural comparison.
type Type interface {
	typeNode()
	String() string
}

// PrimKind enumerates Osty's built-in scalar types. Mirrors the
// checker's `types.PrimitiveKind` but lives here to keep `ir`
// self-contained.
type PrimKind int

const (
	PrimInvalid PrimKind = iota

	PrimInt
	PrimInt8
	PrimInt16
	PrimInt32
	PrimInt64
	PrimUInt8
	PrimUInt16
	PrimUInt32
	PrimUInt64
	PrimByte
	PrimFloat
	PrimFloat32
	PrimFloat64

	PrimBool
	PrimChar
	PrimString
	PrimBytes

	PrimUnit  // ()
	PrimNever // !
)

// PrimType is a primitive scalar type.
type PrimType struct{ Kind PrimKind }

func (*PrimType) typeNode() {}

func (p *PrimType) String() string {
	switch p.Kind {
	case PrimInt:
		return "Int"
	case PrimInt8:
		return "Int8"
	case PrimInt16:
		return "Int16"
	case PrimInt32:
		return "Int32"
	case PrimInt64:
		return "Int64"
	case PrimUInt8:
		return "UInt8"
	case PrimUInt16:
		return "UInt16"
	case PrimUInt32:
		return "UInt32"
	case PrimUInt64:
		return "UInt64"
	case PrimByte:
		return "Byte"
	case PrimFloat:
		return "Float"
	case PrimFloat32:
		return "Float32"
	case PrimFloat64:
		return "Float64"
	case PrimBool:
		return "Bool"
	case PrimChar:
		return "Char"
	case PrimString:
		return "String"
	case PrimBytes:
		return "Bytes"
	case PrimUnit:
		return "()"
	case PrimNever:
		return "Never"
	}
	return "?"
}

// Canonical primitive singletons. Backends and the lowerer reach for
// these instead of allocating a fresh *PrimType per literal.
var (
	TInt     = &PrimType{Kind: PrimInt}
	TInt8    = &PrimType{Kind: PrimInt8}
	TInt16   = &PrimType{Kind: PrimInt16}
	TInt32   = &PrimType{Kind: PrimInt32}
	TInt64   = &PrimType{Kind: PrimInt64}
	TUInt8   = &PrimType{Kind: PrimUInt8}
	TUInt16  = &PrimType{Kind: PrimUInt16}
	TUInt32  = &PrimType{Kind: PrimUInt32}
	TUInt64  = &PrimType{Kind: PrimUInt64}
	TByte    = &PrimType{Kind: PrimByte}
	TFloat   = &PrimType{Kind: PrimFloat}
	TFloat32 = &PrimType{Kind: PrimFloat32}
	TFloat64 = &PrimType{Kind: PrimFloat64}
	TBool    = &PrimType{Kind: PrimBool}
	TChar    = &PrimType{Kind: PrimChar}
	TString  = &PrimType{Kind: PrimString}
	TBytes   = &PrimType{Kind: PrimBytes}
	TUnit    = &PrimType{Kind: PrimUnit}
	TNever   = &PrimType{Kind: PrimNever}
)

// NamedType refers to a user-declared or builtin named type by its
// source name. Args carries type arguments for generics (e.g.
// List<Int> → NamedType{Name:"List", Args:[TInt]}). The Builtin flag
// distinguishes prelude-provided names (List, Map, Set, Option, Result)
// from user declarations; backends commonly need that distinction to
// choose between a runtime primitive and a user type.
//
// Package is the qualifier preceding the name (empty for bare names).
// For multi-segment paths like `pkg.sub.Type`, the Package string
// preserves the dotted prefix so backends can route to the right package
// without ambiguity.
type NamedType struct {
	Package string
	Name    string
	Args    []Type
	Builtin bool
}

func (*NamedType) typeNode() {}

func (n *NamedType) String() string {
	var b strings.Builder
	if n.Package != "" {
		b.WriteString(n.Package)
		b.WriteByte('.')
	}
	b.WriteString(n.Name)
	if len(n.Args) > 0 {
		b.WriteByte('<')
		for i, a := range n.Args {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(typeString(a))
		}
		b.WriteByte('>')
	}
	return b.String()
}

// QualifiedName returns "pkg.Name" when Package is non-empty, otherwise
// just Name. Useful for debug output and when comparing paths.
func (n *NamedType) QualifiedName() string {
	if n.Package == "" {
		return n.Name
	}
	return n.Package + "." + n.Name
}

// OptionalType is the surface form `T?`. Kept distinct from
// NamedType{"Option"} so backends can special-case optional-chain
// lowering without matching on strings.
type OptionalType struct{ Inner Type }

func (*OptionalType) typeNode()        {}
func (o *OptionalType) String() string { return typeString(o.Inner) + "?" }

// TupleType is `(T1, T2, ...)`. Single-element tuples are legal.
type TupleType struct{ Elems []Type }

func (*TupleType) typeNode() {}

func (t *TupleType) String() string {
	var b strings.Builder
	b.WriteByte('(')
	for i, e := range t.Elems {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(typeString(e))
	}
	b.WriteByte(')')
	return b.String()
}

// FnType is `fn(A, B) -> R`.
type FnType struct {
	Params []Type
	Return Type
}

func (*FnType) typeNode() {}

func (f *FnType) String() string {
	var b strings.Builder
	b.WriteString("fn(")
	for i, p := range f.Params {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(typeString(p))
	}
	b.WriteByte(')')
	if f.Return != nil {
		if p, ok := f.Return.(*PrimType); !ok || p.Kind != PrimUnit {
			b.WriteString(" -> ")
			b.WriteString(typeString(f.Return))
		}
	}
	return b.String()
}

// TypeVar is a reference to a generic type parameter (`T` etc). Name is
// the parameter's source name; Owner is the qualified owner (e.g. the
// fn or type declaration's name) used for identity — two `T`s on
// different owners are not equal.
type TypeVar struct {
	Name  string
	Owner string
}

func (*TypeVar) typeNode()        {}
func (v *TypeVar) String() string { return v.Name }

// ErrType is the poisoned type used when lowering cannot recover a real
// type for an expression. Backends should treat it as "already
// reported" and avoid cascading diagnostics.
type ErrType struct{}

func (*ErrType) typeNode()      {}
func (*ErrType) String() string { return "<error>" }

// ErrTypeVal is the canonical poisoned singleton.
var ErrTypeVal Type = &ErrType{}

// typeString is a nil-safe wrapper around Type.String.
func typeString(t Type) string {
	if t == nil {
		return "<nil>"
	}
	return t.String()
}

// ==== Declarations ====

// Decl is the common interface for every top-level declaration.
type Decl interface {
	Node
	declNode()
	DeclName() string
}

// FnDecl is a top-level function. Receiver and Generics are nil/empty
// for free functions; methods carry a non-nil Receiver naming the
// owning type. The backend emits Methods lists inside StructDecl /
// EnumDecl instead of threading receivers through here.
type FnDecl struct {
	Name   string
	Params []*Param
	Return Type
	Body   *Block
	// ReceiverMut preserves whether an owning struct/enum method declared
	// `mut self`; top-level functions leave this false.
	ReceiverMut bool
	Exported    bool
	Generics    []*TypeParam
	SpanV       Span

	// ExportSymbol is the verbatim symbol name supplied via
	// `#[export("name")]` (LANG_SPEC §19.6). When non-empty, the
	// backend emits the function with this exact symbol instead of
	// the mangled default. Applicable only to runtime-sublanguage
	// functions in privileged packages (§19.2).
	ExportSymbol string

	// CABI is set when the function carries `#[c_abi]` (LANG_SPEC
	// §19.6). The backend emits the function with the platform's C
	// calling convention (`ccc` in LLVM) rather than Osty's calling
	// convention. Almost always paired with `#[export("name")]` so
	// the symbol can satisfy the runtime ABI contract; the two
	// annotations are independently representable in the IR.
	CABI bool
}

func (*FnDecl) declNode()          {}
func (f *FnDecl) At() Span         { return f.SpanV }
func (f *FnDecl) DeclName() string { return f.Name }

// Param is one function / method parameter. Default is an already
// lowered expression or nil when absent.
//
// Pattern is non-nil for destructured parameters such as
// `|(a, b)|` or `|User { name }|`. When Pattern is set, Name is empty
// and backends destructure the incoming value against the pattern.
// Simple name-bound params keep Pattern nil and Name populated.
type Param struct {
	Name    string
	Pattern Pattern
	Type    Type
	Default Expr
	SpanV   Span
}

func (p *Param) At() Span { return p.SpanV }

// IsDestructured reports whether the parameter uses a destructuring
// pattern rather than a bare name.
func (p *Param) IsDestructured() bool { return p != nil && p.Pattern != nil }

// TypeParam is a generic type parameter `T` with optional bounds. The
// bounds are interface names (already-lowered NamedType).
type TypeParam struct {
	Name   string
	Bounds []Type
	SpanV  Span
}

func (t *TypeParam) At() Span { return t.SpanV }

// StructDecl is a struct declaration with its field list and methods.
type StructDecl struct {
	Name     string
	Fields   []*Field
	Methods  []*FnDecl
	Generics []*TypeParam
	Exported bool
	SpanV    Span
}

func (*StructDecl) declNode()          {}
func (s *StructDecl) At() Span         { return s.SpanV }
func (s *StructDecl) DeclName() string { return s.Name }

// Field is one struct field.
type Field struct {
	Name     string
	Type     Type
	Default  Expr
	Exported bool
	SpanV    Span
}

func (f *Field) At() Span { return f.SpanV }

// EnumDecl is an enum with its variants and methods.
type EnumDecl struct {
	Name     string
	Variants []*Variant
	Methods  []*FnDecl
	Generics []*TypeParam
	Exported bool
	SpanV    Span
}

func (*EnumDecl) declNode()          {}
func (e *EnumDecl) At() Span         { return e.SpanV }
func (e *EnumDecl) DeclName() string { return e.Name }

// Variant is one enum case. Payload is the tuple of variant payload
// types; nil for bare variants.
type Variant struct {
	Name    string
	Payload []Type
	SpanV   Span
}

func (v *Variant) At() Span { return v.SpanV }

// LetDecl is a top-level `pub let NAME = value`.
type LetDecl struct {
	Name     string
	Type     Type
	Value    Expr
	Mut      bool
	Exported bool
	SpanV    Span
}

func (*LetDecl) declNode()          {}
func (l *LetDecl) At() Span         { return l.SpanV }
func (l *LetDecl) DeclName() string { return l.Name }

// ==== Statements ====

// Stmt is the common interface for every IR statement.
type Stmt interface {
	Node
	stmtNode()
}

// Block is a scoped sequence of statements with an optional final
// expression (the block's "result"). Normalising blocks this way means
// callers don't have to rescan the tail for implicit-return semantics.
type Block struct {
	Stmts  []Stmt
	Result Expr // optional final expression; nil means block yields unit
	SpanV  Span
}

func (*Block) stmtNode()  {}
func (b *Block) At() Span { return b.SpanV }

// LetStmt introduces a local binding. For the common case (`let x = e`)
// Pattern is nil and Name carries the bound identifier. For
// destructuring binds (`let (a, b) = e`, `let User { name } = e`),
// Pattern is non-nil and Name is empty; consumers dispatch on Pattern
// to emit per-binding destructuring code.
type LetStmt struct {
	Name    string
	Pattern Pattern
	Type    Type
	Value   Expr
	Mut     bool
	SpanV   Span
}

func (*LetStmt) stmtNode()  {}
func (l *LetStmt) At() Span { return l.SpanV }

// ExprStmt is an expression used in statement position (side effect).
type ExprStmt struct {
	X     Expr
	SpanV Span
}

func (*ExprStmt) stmtNode()  {}
func (e *ExprStmt) At() Span { return e.SpanV }

// AssignOp classifies the flavor of an assignment statement.
type AssignOp int

const (
	AssignEq AssignOp = iota
	AssignAdd
	AssignSub
	AssignMul
	AssignDiv
	AssignMod
	AssignAnd
	AssignOr
	AssignXor
	AssignShl
	AssignShr
)

// AssignStmt covers plain and compound assignments, including
// multi-target tuple destructuring (`(a, b) = (c, d)`). Single-target
// assignments still have len(Targets) == 1; consumers should branch on
// the length rather than maintaining two shapes.
type AssignStmt struct {
	Op      AssignOp
	Targets []Expr
	Value   Expr
	SpanV   Span
}

func (*AssignStmt) stmtNode()  {}
func (a *AssignStmt) At() Span { return a.SpanV }

// ReturnStmt is `return` or `return value`. Value is nil for a bare
// return (unit return type).
type ReturnStmt struct {
	Value Expr
	SpanV Span
}

func (*ReturnStmt) stmtNode()  {}
func (r *ReturnStmt) At() Span { return r.SpanV }

// BreakStmt exits the innermost loop.
type BreakStmt struct{ SpanV Span }

func (*BreakStmt) stmtNode()  {}
func (b *BreakStmt) At() Span { return b.SpanV }

// ContinueStmt skips to the next loop iteration.
type ContinueStmt struct{ SpanV Span }

func (*ContinueStmt) stmtNode()  {}
func (c *ContinueStmt) At() Span { return c.SpanV }

// IfStmt is a statement-position conditional. Else is nil when there
// is no else clause.
type IfStmt struct {
	Cond  Expr
	Then  *Block
	Else  *Block
	SpanV Span
}

func (*IfStmt) stmtNode()  {}
func (i *IfStmt) At() Span { return i.SpanV }

// ForKind classifies the `for` variant.
type ForKind int

const (
	ForInfinite ForKind = iota
	ForWhile            // cond-controlled
	ForRange            // numeric range: `for i in a..b`
	ForIn               // iterator: `for x in xs`
)

// ForStmt covers all four `for` forms. Fields are set selectively by
// Kind:
//
//	Infinite: Body
//	While:    Cond, Body
//	Range:    Var, Start, End, Inclusive, Body
//	In:       Var, Iter, Body
type ForStmt struct {
	Kind      ForKind
	Var       string  // loop variable for simple-name Range / In forms
	Pattern   Pattern // destructuring pattern for In / Range heads
	Cond      Expr    // While
	Iter      Expr    // In
	Start     Expr    // Range
	End       Expr    // Range
	Inclusive bool    // Range `..=`
	Body      *Block
	SpanV     Span
}

// IsDestructured reports whether the loop head uses a destructuring
// pattern rather than a bare identifier.
func (f *ForStmt) IsDestructured() bool { return f != nil && f.Pattern != nil }

func (*ForStmt) stmtNode()  {}
func (f *ForStmt) At() Span { return f.SpanV }

// ErrorStmt is the poisoned statement emitted when lowering fails. The
// Note is propagated up in the Module's issue list so backends and the
// test suite can report it.
type ErrorStmt struct {
	Note  string
	SpanV Span
}

func (*ErrorStmt) stmtNode()  {}
func (e *ErrorStmt) At() Span { return e.SpanV }

// ==== Expressions ====

// Expr is the common interface for every IR expression. Every
// expression carries its inferred Type, so backends never need to look
// up a side table.
type Expr interface {
	Node
	exprNode()
	Type() Type
}

// IntLit is an integer literal. Text is the original source form
// (preserves radix/underscores for exact emission).
type IntLit struct {
	Text  string
	T     Type
	SpanV Span
}

func (*IntLit) exprNode()    {}
func (l *IntLit) At() Span   { return l.SpanV }
func (l *IntLit) Type() Type { return l.T }

// FloatLit is a float literal.
type FloatLit struct {
	Text  string
	T     Type
	SpanV Span
}

func (*FloatLit) exprNode()    {}
func (l *FloatLit) At() Span   { return l.SpanV }
func (l *FloatLit) Type() Type { return l.T }

// BoolLit is `true` / `false`.
type BoolLit struct {
	Value bool
	SpanV Span
}

func (*BoolLit) exprNode()    {}
func (l *BoolLit) At() Span   { return l.SpanV }
func (l *BoolLit) Type() Type { return TBool }

// CharLit is a single-code-point char literal.
type CharLit struct {
	Value rune
	SpanV Span
}

func (*CharLit) exprNode()    {}
func (l *CharLit) At() Span   { return l.SpanV }
func (l *CharLit) Type() Type { return TChar }

// ByteLit is a byte literal (0..=255).
type ByteLit struct {
	Value byte
	SpanV Span
}

func (*ByteLit) exprNode()    {}
func (l *ByteLit) At() Span   { return l.SpanV }
func (l *ByteLit) Type() Type { return TByte }

// StringLit is a possibly interpolated string literal. Parts alternate
// literal text and inner expressions; backends format the whole thing
// via their own formatter.
type StringLit struct {
	Parts    []StringPart
	IsRaw    bool
	IsTriple bool
	SpanV    Span
}

// StringPart is either a literal text segment (IsLit) or an embedded
// expression (Expr).
type StringPart struct {
	IsLit bool
	Lit   string
	Expr  Expr
}

func (*StringLit) exprNode()  {}
func (s *StringLit) At() Span { return s.SpanV }
func (*StringLit) Type() Type { return TString }

// UnitLit is the `()` zero-value expression, used as an implicit block
// result when no expression was provided.
type UnitLit struct{ SpanV Span }

func (*UnitLit) exprNode()  {}
func (u *UnitLit) At() Span { return u.SpanV }
func (*UnitLit) Type() Type { return TUnit }

// IdentKind classifies what an Ident resolves to. Lowering populates
// this so backends do not need a resolver.
type IdentKind int

const (
	IdentUnknown  IdentKind = iota
	IdentLocal              // let binding or closure capture
	IdentParam              // function/method parameter
	IdentFn                 // top-level function
	IdentVariant            // enum variant
	IdentTypeName           // struct/enum/interface/alias used as value
	IdentGlobal             // top-level `let`
	IdentBuiltin            // prelude / builtin
)

// Ident is a name reference. Kind distinguishes locals from calls on
// top-level fn names so the backend can rewrite user calls without a
// second resolution pass.
//
// TypeArgs carries turbofish type arguments written without a following
// call (`f::<Int>` when used as a value — e.g. as a function pointer).
// Most idents leave TypeArgs empty; lowerCall lifts the turbofish into
// CallExpr.TypeArgs instead of leaving it on the callee ident.
type Ident struct {
	Name     string
	Kind     IdentKind
	TypeArgs []Type
	T        Type
	SpanV    Span
}

func (*Ident) exprNode()    {}
func (i *Ident) At() Span   { return i.SpanV }
func (i *Ident) Type() Type { return i.T }

// UnOp enumerates prefix operators.
type UnOp int

const (
	UnNeg    UnOp = iota // -x
	UnPlus               // +x
	UnNot                // !x
	UnBitNot             // ~x
)

// UnaryExpr is a prefix unary operation.
type UnaryExpr struct {
	Op    UnOp
	X     Expr
	T     Type
	SpanV Span
}

func (*UnaryExpr) exprNode()    {}
func (e *UnaryExpr) At() Span   { return e.SpanV }
func (e *UnaryExpr) Type() Type { return e.T }

// BinOp enumerates binary operators. Covers arithmetic, comparison,
// logical, and bitwise operators. Range and coalesce operators have
// their own expression types.
type BinOp int

const (
	BinAdd BinOp = iota
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

	BinAnd // &&
	BinOr  // ||

	BinBitAnd
	BinBitOr
	BinBitXor
	BinShl
	BinShr
)

// BinaryExpr is a binary infix operation.
type BinaryExpr struct {
	Op    BinOp
	Left  Expr
	Right Expr
	T     Type
	SpanV Span
}

func (*BinaryExpr) exprNode()    {}
func (e *BinaryExpr) At() Span   { return e.SpanV }
func (e *BinaryExpr) Type() Type { return e.T }

// Arg is one argument inside a call, method call, intrinsic call, or
// variant constructor. Name is empty for positional arguments; for
// keyword arguments it carries the parameter name written at the call
// site (`greet(name: "Ada")` yields Arg{Name:"name", Value: StringLit}).
type Arg struct {
	Name  string
	Value Expr
	SpanV Span
}

// At returns the argument's source span.
func (a Arg) At() Span { return a.SpanV }

// IsKeyword reports whether the argument was written with a `name:`
// prefix at the call site.
func (a Arg) IsKeyword() bool { return a.Name != "" }

// CallExpr is a user function / method / closure call. TypeArgs records
// the concrete type arguments supplied at this call site (from
// turbofish or propagated from the checker's monomorphisation info). It
// is empty for non-generic calls.
type CallExpr struct {
	Callee   Expr
	TypeArgs []Type
	Args     []Arg
	T        Type
	SpanV    Span
}

func (*CallExpr) exprNode()    {}
func (c *CallExpr) At() Span   { return c.SpanV }
func (c *CallExpr) Type() Type { return c.T }

// IntrinsicKind enumerates the print-family intrinsics that Lower
// recognises directly. Backends emit these via their runtime's
// formatted-print machinery.
type IntrinsicKind int

const (
	IntrinsicPrint IntrinsicKind = iota
	IntrinsicPrintln
	IntrinsicEprint
	IntrinsicEprintln
)

// IntrinsicCall is a call to one of the recognised built-in print
// intrinsics. Kept distinct from CallExpr so backends can dispatch
// directly rather than string-matching on names.
type IntrinsicCall struct {
	Kind  IntrinsicKind
	Args  []Arg
	SpanV Span
}

func (*IntrinsicCall) exprNode()  {}
func (i *IntrinsicCall) At() Span { return i.SpanV }
func (*IntrinsicCall) Type() Type { return TUnit }

// ListLit is a homogeneous list literal `[a, b, c]`. Elem is the
// declared element type (populated from the checker's view of the
// expression's type).
type ListLit struct {
	Elems []Expr
	Elem  Type
	SpanV Span
}

func (*ListLit) exprNode()    {}
func (l *ListLit) At() Span   { return l.SpanV }
func (l *ListLit) Type() Type { return &NamedType{Name: "List", Args: []Type{l.Elem}, Builtin: true} }

// BlockExpr is a block used in expression position. The block's Result
// is the value produced; backends may rewrite this into an IIFE when
// the host language lacks block-as-expression support.
type BlockExpr struct {
	Block *Block
	T     Type
	SpanV Span
}

func (*BlockExpr) exprNode()    {}
func (b *BlockExpr) At() Span   { return b.SpanV }
func (b *BlockExpr) Type() Type { return b.T }

// IfExpr is an `if` used in expression position. Both arms are
// guaranteed present; statement-form `if` without else lowers to
// IfStmt instead.
type IfExpr struct {
	Cond  Expr
	Then  *Block
	Else  *Block
	T     Type
	SpanV Span
}

func (*IfExpr) exprNode()    {}
func (i *IfExpr) At() Span   { return i.SpanV }
func (i *IfExpr) Type() Type { return i.T }

// ErrorExpr is the poisoned expression emitted when lowering fails for
// a sub-expression. Backends should render a comment and skip.
type ErrorExpr struct {
	Note  string
	T     Type
	SpanV Span
}

func (*ErrorExpr) exprNode()  {}
func (e *ErrorExpr) At() Span { return e.SpanV }
func (e *ErrorExpr) Type() Type {
	if e.T == nil {
		return ErrTypeVal
	}
	return e.T
}

// ==== Additional declarations ====

// InterfaceDecl is an interface with a set of required methods and
// optional defaults. Extends lists composed interfaces; every element
// is a NamedType.
type InterfaceDecl struct {
	Name     string
	Methods  []*FnDecl
	Extends  []Type
	Generics []*TypeParam
	Exported bool
	SpanV    Span
}

func (*InterfaceDecl) declNode()          {}
func (i *InterfaceDecl) At() Span         { return i.SpanV }
func (i *InterfaceDecl) DeclName() string { return i.Name }

// TypeAliasDecl is `type Name<T> = Target`.
type TypeAliasDecl struct {
	Name     string
	Generics []*TypeParam
	Target   Type
	Exported bool
	SpanV    Span
}

func (*TypeAliasDecl) declNode()          {}
func (t *TypeAliasDecl) At() Span         { return t.SpanV }
func (t *TypeAliasDecl) DeclName() string { return t.Name }

// UseDecl is a `use` import. Path is the source-level dotted path
// (["std","io"]), Alias is the bound name (the last path segment when
// the user didn't write `as alias`). RuntimePath/GoPath plus GoBody mirror
// FFI declarations; they are empty for ordinary imports.
type UseDecl struct {
	Path         []string
	RawPath      string
	Alias        string
	IsGoFFI      bool
	IsRuntimeFFI bool
	GoPath       string
	RuntimePath  string
	GoBody       []Decl
	SpanV        Span
}

func (*UseDecl) declNode()          {}
func (u *UseDecl) At() Span         { return u.SpanV }
func (u *UseDecl) DeclName() string { return u.Alias }
func (u *UseDecl) IsFFI() bool      { return u != nil && (u.IsGoFFI || u.IsRuntimeFFI) }

// ==== Additional statements ====

// DeferStmt schedules Body to run on scope exit.
type DeferStmt struct {
	Body  *Block
	SpanV Span
}

func (*DeferStmt) stmtNode()  {}
func (d *DeferStmt) At() Span { return d.SpanV }

// ChanSendStmt is `channel <- value`.
type ChanSendStmt struct {
	Channel Expr
	Value   Expr
	SpanV   Span
}

func (*ChanSendStmt) stmtNode()  {}
func (c *ChanSendStmt) At() Span { return c.SpanV }

// MatchStmt is a `match` scrutinee used in statement position. The
// equivalent expression form (MatchExpr) has a non-unit Type; when the
// checker determined the match is used for side effects only, the
// lowerer emits MatchStmt so backends don't synthesise a wasted value.
//
// Tree is an optional pre-compiled decision tree (see decision.go). It
// is nil when the lowerer skipped compilation (disabled, or a pattern
// shape the tree compiler does not yet handle) — backends should then
// fall back to arm-by-arm evaluation using Arms.
type MatchStmt struct {
	Scrutinee Expr
	Arms      []*MatchArm
	Tree      DecisionNode
	SpanV     Span
}

func (*MatchStmt) stmtNode()  {}
func (m *MatchStmt) At() Span { return m.SpanV }

// ==== Additional expressions ====

// FieldExpr is `x.name` or `x?.name` (when Optional is true).
type FieldExpr struct {
	X        Expr
	Name     string
	Optional bool
	T        Type
	SpanV    Span
}

func (*FieldExpr) exprNode()    {}
func (f *FieldExpr) At() Span   { return f.SpanV }
func (f *FieldExpr) Type() Type { return f.T }

// IndexExpr is `x[i]`.
type IndexExpr struct {
	X     Expr
	Index Expr
	T     Type
	SpanV Span
}

func (*IndexExpr) exprNode()    {}
func (e *IndexExpr) At() Span   { return e.SpanV }
func (e *IndexExpr) Type() Type { return e.T }

// MethodCall is a user-written method call `receiver.name(args)`. Kept
// distinct from CallExpr so backends don't have to unwrap a FieldExpr
// callee every time. TypeArgs carries turbofish arguments; empty when
// not supplied.
type MethodCall struct {
	Receiver Expr
	Name     string
	TypeArgs []Type
	Args     []Arg
	T        Type
	SpanV    Span
}

func (*MethodCall) exprNode()    {}
func (m *MethodCall) At() Span   { return m.SpanV }
func (m *MethodCall) Type() Type { return m.T }

// StructLit is `Type { field: value, ..spread }`. Spread is nil when
// no `..rest` was written.
type StructLit struct {
	TypeName string
	Fields   []StructLitField
	Spread   Expr
	T        Type
	SpanV    Span
}

// StructLitField is `name: value` (Value set) or `name` (Value nil =
// shorthand, resolve to a local binding named `name`).
type StructLitField struct {
	Name  string
	Value Expr
	SpanV Span
}

func (f StructLitField) At() Span { return f.SpanV }

func (*StructLit) exprNode()    {}
func (s *StructLit) At() Span   { return s.SpanV }
func (s *StructLit) Type() Type { return s.T }

// TupleLit is `(a, b, c)`; backends rely on len(Elems) ≥ 2.
// Single-element tuples arriving from the parser lower to just their
// inner expression (parens were informational only).
type TupleLit struct {
	Elems []Expr
	T     Type
	SpanV Span
}

func (*TupleLit) exprNode()    {}
func (t *TupleLit) At() Span   { return t.SpanV }
func (t *TupleLit) Type() Type { return t.T }

// MapLit is `{"k": v, ...}` or the empty `{:}` literal. KeyT/ValT are
// the declared key/value types (populated from the checker's view).
type MapLit struct {
	Entries []MapEntry
	KeyT    Type
	ValT    Type
	SpanV   Span
}

// MapEntry is one `key: value` in a MapLit.
type MapEntry struct {
	Key   Expr
	Value Expr
	SpanV Span
}

func (e MapEntry) At() Span { return e.SpanV }

func (*MapLit) exprNode()  {}
func (m *MapLit) At() Span { return m.SpanV }
func (m *MapLit) Type() Type {
	return &NamedType{Name: "Map", Args: []Type{m.KeyT, m.ValT}, Builtin: true}
}

// RangeLit is a range used in value position (`let r = 0..10`). Loop
// heads lower to ForStmt with Kind=ForRange instead of constructing a
// RangeLit. Start and End may be nil for unbounded sides.
type RangeLit struct {
	Start     Expr
	End       Expr
	Inclusive bool
	T         Type
	SpanV     Span
}

func (*RangeLit) exprNode()    {}
func (r *RangeLit) At() Span   { return r.SpanV }
func (r *RangeLit) Type() Type { return r.T }

// QuestionExpr is the postfix `?` error / Option propagation operator.
// X is the operand; T is the unwrapped value type. Backends expand
// this into a runtime conditional return.
type QuestionExpr struct {
	X     Expr
	T     Type
	SpanV Span
}

func (*QuestionExpr) exprNode()    {}
func (q *QuestionExpr) At() Span   { return q.SpanV }
func (q *QuestionExpr) Type() Type { return q.T }

// CoalesceExpr is `left ?? right`: yields Left when Left is non-nil,
// else Right. T is the joined (non-optional) type.
type CoalesceExpr struct {
	Left  Expr
	Right Expr
	T     Type
	SpanV Span
}

func (*CoalesceExpr) exprNode()    {}
func (c *CoalesceExpr) At() Span   { return c.SpanV }
func (c *CoalesceExpr) Type() Type { return c.T }

// Closure is `|params| body` (short form) or `|params| -> T { body }`.
// Body is always lowered as a *Block for uniformity; a single-
// expression closure is wrapped with Result set and Stmts empty.
// CaptureKind classifies how a name becomes available inside a closure
// without being a parameter of that closure.
type CaptureKind int

const (
	CaptureUnknown CaptureKind = iota
	CaptureLocal               // outer `let` binding (stack local)
	CaptureParam               // outer function / method parameter
	CaptureGlobal              // top-level `let`
	CaptureFn                  // top-level function reference
	CaptureSelf                // enclosing method's receiver
)

// Capture is one free variable referenced by a closure.
type Capture struct {
	Name  string
	Kind  CaptureKind
	T     Type
	Mut   bool
	SpanV Span
}

// At returns the capture's source span (the first reference site).
func (c *Capture) At() Span { return c.SpanV }

type Closure struct {
	Params   []*Param
	Return   Type
	Body     *Block
	Captures []*Capture
	T        Type
	SpanV    Span
}

func (*Closure) exprNode()    {}
func (c *Closure) At() Span   { return c.SpanV }
func (c *Closure) Type() Type { return c.T }

// VariantLit constructs an enum variant value: `Some(42)`, `Ok(x)`,
// `Red` (bare), `Color.Red` (qualified). Enum is the enum's source
// name when known; empty for unresolved prelude variants.
type VariantLit struct {
	Enum    string
	Variant string
	Args    []Arg
	T       Type
	SpanV   Span
}

func (*VariantLit) exprNode()    {}
func (v *VariantLit) At() Span   { return v.SpanV }
func (v *VariantLit) Type() Type { return v.T }

// MatchExpr is `match scrutinee { arm, ... }`. Tree is an optional
// pre-compiled decision tree produced by CompileDecisionTree; it is
// nil when the arm shapes are outside the compiler's current coverage.
type MatchExpr struct {
	Scrutinee Expr
	Arms      []*MatchArm
	Tree      DecisionNode
	T         Type
	SpanV     Span
}

func (*MatchExpr) exprNode()    {}
func (m *MatchExpr) At() Span   { return m.SpanV }
func (m *MatchExpr) Type() Type { return m.T }

// MatchArm is one match case. Guard is an optional `if cond` refinement
// applied after the pattern succeeds. Body is a block whose Result
// carries the arm value (nil for unit-producing arms).
type MatchArm struct {
	Pattern Pattern
	Guard   Expr
	Body    *Block
	SpanV   Span
}

func (a *MatchArm) At() Span { return a.SpanV }

// IfLetExpr is `if let pat = scrutinee { then } else { else }`. Kept
// distinct from IfExpr so backends can recognise the destructure-and-
// match form directly; the Cond/boolean form stays as IfExpr.
type IfLetExpr struct {
	Pattern   Pattern
	Scrutinee Expr
	Then      *Block
	Else      *Block
	T         Type
	SpanV     Span
}

func (*IfLetExpr) exprNode()    {}
func (i *IfLetExpr) At() Span   { return i.SpanV }
func (i *IfLetExpr) Type() Type { return i.T }

// TupleAccess is `t.0` / `t.1` — numeric field access on tuples. The
// AST spells this as FieldExpr with a numeric Name; the lowerer
// hoists tuple-indexed field access to this dedicated node so
// backends don't need to disambiguate numeric vs. alphabetic names.
type TupleAccess struct {
	X     Expr
	Index int
	T     Type
	SpanV Span
}

func (*TupleAccess) exprNode()    {}
func (t *TupleAccess) At() Span   { return t.SpanV }
func (t *TupleAccess) Type() Type { return t.T }
