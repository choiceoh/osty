// Package ast defines the Osty abstract syntax tree.
package ast

import (
	"strings"

	"github.com/osty/osty/internal/token"
)

// Node is the base interface for every syntactic element.
type Node interface {
	Pos() token.Pos
	End() token.Pos
}

// Category marker interfaces.
type (
	Expr interface {
		Node
		exprNode()
	}
	Stmt interface {
		Node
		stmtNode()
	}
	Decl interface {
		Node
		declNode()
	}
	Type interface {
		Node
		typeNode()
	}
	Pattern interface {
		Node
		patternNode()
	}
)

// ==== Annotations ====

// Annotation is `#[Name]` or `#[Name(arg, key = "v", ...)]`. Per v0.2 R26
// the only permitted names in the v0.9 set are `json` and `deprecated`,
// enforced by the parser.
type Annotation struct {
	PosV token.Pos
	EndV token.Pos
	Name string
	Args []*AnnotationArg
}

func (a *Annotation) Pos() token.Pos { return a.PosV }
func (a *Annotation) End() token.Pos { return a.EndV }

// AnnotationArg is one argument inside `#[Name(args...)]`.
//
//	IDENT                — flag form (Key set, Value nil)
//	IDENT '=' Literal    — key/value form (Key set, Value is the literal)
type AnnotationArg struct {
	PosV  token.Pos
	Key   string
	Value Expr // nil for flag form
}

func (a *AnnotationArg) Pos() token.Pos { return a.PosV }
func (a *AnnotationArg) End() token.Pos {
	if a.Value != nil {
		return a.Value.End()
	}
	// Flag form has no value; the argument occupies just its key,
	// which we approximate by using the start position. The exact
	// end column isn't tracked separately by the parser.
	return a.PosV
}

// AnnotationTarget classifies where an annotation is permitted.
type AnnotationTarget int

const (
	// TargetStructField — annotation on a struct field.
	TargetStructField AnnotationTarget = 1 << iota
	// TargetTopLevelDecl — annotation on `fn`/`struct`/`enum`/
	// `interface`/`type`/`let` at the top level.
	TargetTopLevelDecl
	// TargetMethod — annotation on a method inside `struct`/`enum`.
	TargetMethod
	// TargetVariant — annotation on an enum variant.
	TargetVariant
)

// annotationRules lists the v0.9 permitted annotations (R26) and the
// targets each applies to.
var annotationRules = map[string]AnnotationTarget{
	"json":       TargetStructField | TargetVariant,
	"deprecated": TargetTopLevelDecl | TargetMethod,
	// `allow` suppresses lint warnings for the annotated declaration
	// (and its descendants). Broad target set: any place a lint rule
	// can trip.
	"allow": TargetTopLevelDecl | TargetMethod | TargetStructField | TargetVariant,
	// `intrinsic_methods` marks a stdlib placeholder struct whose
	// methods describe operations on one or more primitive types. The
	// stdlib loader extracts the methods into its primitive-method
	// table and discards the struct itself.
	"intrinsic_methods": TargetTopLevelDecl,
	// `requires` attaches a conditional trait bound to a stdlib
	// method — the bound is enforced only at call sites, matching the
	// spec's "T: Ordered" annotations on conditional collection ops.
	"requires": TargetMethod,
	// `no_alloc` (LANG_SPEC §19.6) forbids managed allocation in the
	// annotated function body, including any direct or transitive call
	// to a function that allocates. The body walker in
	// `internal/check/noalloc.go` enforces it (`E0772`). This is part of
	// the runtime sublanguage; spec §19.2 restricts it to privileged
	// packages, and the privilege gate lives in
	// `internal/check/privilege.go` (`E0770`).
	"no_alloc": TargetTopLevelDecl | TargetMethod,
	// `intrinsic` (LANG_SPEC §19.5 / §19.6) marks a body-less function
	// whose implementation is supplied by the lowering layer. Runtime
	// sublanguage only; privilege-gated by
	// `internal/check/privilege.go`.
	"intrinsic": TargetTopLevelDecl | TargetMethod,
	// `c_abi` (LANG_SPEC §19.6) emits the function with the platform's
	// C calling convention rather than Osty's. Almost always paired with
	// `#[export(...)]`. Runtime sublanguage only.
	"c_abi": TargetTopLevelDecl,
	// `export("symbol")` (LANG_SPEC §19.6) emits the function with the
	// exact symbol name supplied, bypassing Osty name mangling. The
	// argument is a string literal (§1.9). Runtime sublanguage only.
	"export": TargetTopLevelDecl,
	// `pod` (LANG_SPEC §19.4 / §19.6) requests the checker to verify
	// that the annotated struct is plain-old-data: every field is
	// `Pod`, generic parameters carry `T: Pod`, no managed
	// references. Rejection is `E0771`. Runtime sublanguage only.
	"pod": TargetTopLevelDecl,
	// `repr(c)` (LANG_SPEC §19.6) forces C ABI field order, padding,
	// and alignment on the annotated struct. Required on any struct
	// passed across a `#[c_abi]` boundary or used with
	// `raw.read`/`raw.write`. Runtime sublanguage only.
	"repr": TargetTopLevelDecl,
	// v0.5 (G29) §5. Conditional compilation. The annotation wraps any
	// declaration; the resolver evaluates the cfg expression in a
	// pre-resolve pass and drops declarations whose guard is false
	// before type-checking runs. Permitted keys: `os`, `target`,
	// `arch`, `feature`; composition via `all(...)` / `any(...)` /
	// `not(...)`. Unknown keys are `E0405`.
	"cfg": TargetTopLevelDecl | TargetMethod | TargetStructField | TargetVariant,
	// v0.5 (G35) §3.1. Opt-in operator overloading. The annotation
	// attaches to a method whose body implements one of the six
	// permitted operators (`+`, `-`, `*`, `/`, `%`, unary `-`).
	// Method signature validation is `E0754`; duplicate is `E0755`;
	// operator outside the permitted set is `E0756`. All other
	// operators remain primitive-only.
	"op": TargetMethod,
	// v0.5 (G32) §11. Inline test function marker. The test runner
	// collects `#[test]`-annotated `fn`s alongside functions in
	// `_test.osty` files; production builds exclude them. No
	// arguments.
	"test": TargetTopLevelDecl,
	// v0.6 A5 / A5.1 (SIMD track). As of v0.6 the vectorize hint is ON
	// by default — every function's loops get `!llvm.loop.vectorize.enable`
	// metadata and opt out of the per-iteration GC safepoint poll
	// without the user writing anything. `#[vectorize(...)]` with args
	// stays valid as the tuning knob:
	//   - `scalable`       — prefer SVE/RVV over fixed-width NEON
	//   - `predicate`      — enable tail folding (masked tail ops)
	//   - `width = N`      — force vectorization factor; unlocks
	//                        AVX-512 ZMM on Intel.
	// Bare `#[vectorize]` is accepted as a no-op (documents intent;
	// redundant with default). Use `#[no_vectorize]` to opt out.
	// §3.8.3, SPEC_GAPS `vectorize-hint`.
	"vectorize": TargetTopLevelDecl | TargetMethod,
	// v0.6 A5.2. Opt-out of the default vectorize treatment. Bare
	// flag. The annotated function's loops keep per-iteration safepoint
	// polls and receive no `!llvm.loop.vectorize.enable` metadata —
	// useful for long-running worker loops that must yield to GC
	// mid-loop. §3.8.3.
	"no_vectorize": TargetTopLevelDecl | TargetMethod,
	// v0.6 A6. Declares that memory accesses inside the annotated
	// function's loops are parallel (no loop-carried memory
	// dependencies). The LLVM backend emits a `!llvm.access.group`
	// metadata node and tags every load/store + loop-backedge with
	// `llvm.loop.parallel_accesses`, which lets the vectorizer bypass
	// its default aliasing analysis. Soundness is the programmer's
	// responsibility. §3.8.5. No arguments.
	"parallel": TargetTopLevelDecl | TargetMethod,
	// v0.6 A7. Requests LLVM loop unrolling for every loop lowered in
	// the body. Bare form (`#[unroll]`) emits
	// `llvm.loop.unroll.enable`; `#[unroll(N)]` emits
	// `llvm.loop.unroll.count, i32 N` for a fixed unroll factor.
	// Composes with `#[vectorize]` (unroll × width ≈ effective
	// throughput). §3.8.6.
	"unroll": TargetTopLevelDecl | TargetMethod,
	// v0.6 A8. Inlining hint family. Bare `#[inline]` emits LLVM's
	// `inlinehint` fn attribute, a soft suggestion. `#[inline(always)]`
	// / `#[inline(never)]` emit the hard `alwaysinline` / `noinline`
	// attributes which the inliner honors mechanically. §3.8.7.
	"inline": TargetTopLevelDecl | TargetMethod,
	// v0.6 A9. Function frequency hints. `#[hot]` emits the LLVM
	// `hot` fn attribute (aggressive optimization, `.text.hot`
	// section); `#[cold]` emits `cold` (size-optimize, move to
	// `.text.cold`, bias branch prediction away from calls). Bare
	// flags. §3.8.8.
	"hot":  TargetTopLevelDecl | TargetMethod,
	"cold": TargetTopLevelDecl | TargetMethod,
	// v0.6 A10. Per-function target feature override. Each bare-ident
	// argument names a CPU feature the backend should enable while
	// compiling this function; the LLVM emitter materialises them as
	// a single `target-features="+f1,+f2"` fn attribute. Lets a
	// library ship one SIMD-heavy function compiled for AVX-512 /
	// SVE without forcing the whole program onto that baseline.
	// §3.8.9.
	"target_feature": TargetTopLevelDecl | TargetMethod,
	// v0.6 A11. Promise that pointer-typed parameters do not alias.
	// Bare `#[noalias]` marks every pointer param; `#[noalias(p1, p2)]`
	// marks only the listed params. The LLVM emitter inserts the
	// `noalias` parameter attribute on matching params so the LLVM
	// alias analyzer can assume the pointers point at disjoint
	// memory — unlocks SROA, loop vectorization, and LICM that would
	// otherwise bail on potential aliasing. §3.8.11.
	"noalias": TargetTopLevelDecl | TargetMethod,
	// v0.6 A13. Asserts the function has no observable side effects
	// (no writes to memory the caller can see, no I/O, no calls to
	// impure functions). The LLVM emitter sets the `readnone` fn
	// attribute so callers can CSE / hoist repeated calls to the same
	// arguments. Lenient in v0.6 — the compiler trusts the
	// annotation; a checker-level enforcement pass is tracked under
	// SPEC_GAPS `pure-enforce`. §3.8.12.
	"pure": TargetTopLevelDecl | TargetMethod,
}

// IsAllowedAnnotation reports whether an annotation name is part of the
// v0.9 permitted set.
func IsAllowedAnnotation(name string) bool {
	_, ok := annotationRules[name]
	return ok
}

// AnnotationAllowedAt reports whether the named annotation may attach to
// the given target. Returns false for unknown names (unknown names are
// reported by the parser; this helper is for post-parse target checks).
func AnnotationAllowedAt(name string, target AnnotationTarget) bool {
	allowed, ok := annotationRules[name]
	if !ok {
		return false
	}
	return allowed&target != 0
}

// AnnotationTargetString returns a human-readable description of where
// an annotation is allowed. Used for diagnostics.
func AnnotationTargetString(name string) string {
	allowed, ok := annotationRules[name]
	if !ok {
		return "(unknown)"
	}
	var parts []string
	if allowed&TargetStructField != 0 {
		parts = append(parts, "struct fields")
	}
	if allowed&TargetTopLevelDecl != 0 {
		parts = append(parts, "top-level declarations")
	}
	if allowed&TargetMethod != 0 {
		parts = append(parts, "methods")
	}
	if allowed&TargetVariant != 0 {
		parts = append(parts, "enum variants")
	}
	if len(parts) == 0 {
		return "(no targets)"
	}
	return joinWithAnd(parts)
}

func joinWithAnd(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	case 2:
		return parts[0] + " and " + parts[1]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + ", and " + parts[len(parts)-1]
}

// ==== File ====

// File is an entire .osty source file.
type File struct {
	// Uses are all `use` declarations at the file's top.
	Uses []*UseDecl
	// Decls is the mix of declarations. For script files this may also
	// contain top-level statements (wrapped in FreeStmt).
	Decls []Decl
	// Stmts are top-level statements (non-empty = script file).
	Stmts []Stmt
	PosV  token.Pos
	EndV  token.Pos
}

func (f *File) Pos() token.Pos { return f.PosV }
func (f *File) End() token.Pos { return f.EndV }

// IsScript reports whether the file is a script (has top-level statements).
func (f *File) IsScript() bool { return len(f.Stmts) > 0 }

// ==== Declarations ====

// Decorated is the common interface exposed by every Decl that can
// carry a doc comment and annotations. Tools that need to walk the
// "leading trivia" of a declaration (formatter, linter, LSP hover)
// can rely on this interface instead of a per-Decl type switch.
type Decorated interface {
	Doc() string
	Annots() []*Annotation
}

// UseDecl represents `use path`, `use path as alias`, and FFI forms.
type UseDecl struct {
	PosV         token.Pos
	EndV         token.Pos
	Path         []string // dot-separated path, or single entry with slashes for URLs
	RawPath      string   // e.g. "github.com/user/lib" as written
	Alias        string   // optional `as alias`
	IsPub        bool     // v0.5 (G30) §5 — `pub use path` re-export
	IsGoFFI      bool     // legacy bootstrap-only `use go "..."`
	IsRuntimeFFI bool     // `use runtime.* { ... }`
	GoPath       string   // legacy go import path
	RuntimePath  string   // runtime ABI path, e.g. "runtime.strings"
	GoBody       []Decl   // declarations inside `{ ... }`
}

func (*UseDecl) declNode()        {}
func (u *UseDecl) Pos() token.Pos { return u.PosV }
func (u *UseDecl) End() token.Pos { return u.EndV }

func (u *UseDecl) IsFFI() bool {
	return u != nil && (u.IsGoFFI || u.IsRuntimeFFI)
}

func (u *UseDecl) FFIPath() string {
	if u == nil {
		return ""
	}
	if u.IsRuntimeFFI {
		return u.RuntimePath
	}
	if u.IsGoFFI {
		return u.GoPath
	}
	return ""
}

// FnDecl is a top-level or method function declaration.
type FnDecl struct {
	PosV        token.Pos
	EndV        token.Pos
	Pub         bool
	Name        string
	Generics    []*GenericParam
	Recv        *Receiver // non-nil for methods
	Params      []*Param
	ReturnType  Type   // nil == unit ()
	Body        *Block // nil for interface-declared methods without default
	DocComment  string
	Annotations []*Annotation
}

func (*FnDecl) declNode()               {}
func (f *FnDecl) Pos() token.Pos        { return f.PosV }
func (f *FnDecl) End() token.Pos        { return f.EndV }
func (f *FnDecl) Doc() string           { return f.DocComment }
func (f *FnDecl) Annots() []*Annotation { return f.Annotations }

// Receiver describes the `self` or `mut self` first parameter of a method.
//
// MutPos records the position of the `mut` keyword when Mut is true,
// so tools can rewrite `mut self` → `self` by deleting that token's span.
// When Mut is false, MutPos is the zero Pos.
type Receiver struct {
	PosV   token.Pos
	EndV   token.Pos
	Mut    bool
	MutPos token.Pos
}

func (r *Receiver) Pos() token.Pos { return r.PosV }
func (r *Receiver) End() token.Pos { return r.EndV }

// Param is a function parameter.
//
// For top-level functions and methods, Pattern is nil and Name carries the
// parameter identifier. For closures, Pattern may be set to a destructuring
// pattern (e.g. `|(k, v)| ...`) per SPEC_GAPS G4 — when Pattern is set,
// Name is empty.
type Param struct {
	PosV    token.Pos
	EndV    token.Pos
	Name    string
	Pattern Pattern // optional, closure params only
	Type    Type
	Default Expr // nil if no default; must be a literal per §3.1
}

func (p *Param) Pos() token.Pos { return p.PosV }
func (p *Param) End() token.Pos {
	if p.EndV.Line > 0 {
		return p.EndV
	}
	// Fallback: best-effort end derived from the most complete sub-node.
	if p.Default != nil {
		return p.Default.End()
	}
	if p.Type != nil {
		return p.Type.End()
	}
	return p.PosV
}

// GenericParam is a type parameter like `T` or `T: Ordered + Hashable`.
type GenericParam struct {
	PosV        token.Pos
	EndV        token.Pos
	Name        string
	Constraints []Type // interface types bounding the parameter
}

func (g *GenericParam) Pos() token.Pos { return g.PosV }
func (g *GenericParam) End() token.Pos {
	if g.EndV.Line > 0 {
		return g.EndV
	}
	if n := len(g.Constraints); n > 0 {
		return g.Constraints[n-1].End()
	}
	return g.PosV
}

// StructDecl — a struct declaration (possibly partial).
type StructDecl struct {
	PosV        token.Pos
	EndV        token.Pos
	Pub         bool
	Name        string
	Generics    []*GenericParam
	Fields      []*Field // may be empty for partial declarations without fields
	Methods     []*FnDecl
	DocComment  string
	Annotations []*Annotation
}

func (*StructDecl) declNode()               {}
func (s *StructDecl) Pos() token.Pos        { return s.PosV }
func (s *StructDecl) End() token.Pos        { return s.EndV }
func (s *StructDecl) Doc() string           { return s.DocComment }
func (s *StructDecl) Annots() []*Annotation { return s.Annotations }

// Field is a struct field.
type Field struct {
	PosV        token.Pos
	EndV        token.Pos
	Pub         bool
	Name        string
	Type        Type
	Default     Expr // optional, literal only
	Annotations []*Annotation
	// DocComment is the `///` block immediately above the field,
	// captured by the parser. Same convention as FnDecl.DocComment:
	// the `///` prefix is stripped and lines are joined with `\n`.
	// Empty when no doc was attached.
	DocComment string
}

func (f *Field) Pos() token.Pos { return f.PosV }
func (f *Field) End() token.Pos {
	if f.EndV.Line > 0 {
		return f.EndV
	}
	if f.Default != nil {
		return f.Default.End()
	}
	if f.Type != nil {
		return f.Type.End()
	}
	return f.PosV
}

// EnumDecl — an enum declaration.
type EnumDecl struct {
	PosV        token.Pos
	EndV        token.Pos
	Pub         bool
	Name        string
	Generics    []*GenericParam
	Variants    []*Variant
	Methods     []*FnDecl
	DocComment  string
	Annotations []*Annotation
}

func (*EnumDecl) declNode()               {}
func (e *EnumDecl) Pos() token.Pos        { return e.PosV }
func (e *EnumDecl) End() token.Pos        { return e.EndV }
func (e *EnumDecl) Doc() string           { return e.DocComment }
func (e *EnumDecl) Annots() []*Annotation { return e.Annotations }

// Variant is one enum variant: bare or tuple-like.
type Variant struct {
	PosV        token.Pos
	EndV        token.Pos
	Name        string
	Fields      []Type // empty for bare, otherwise tuple-like payload types
	Annotations []*Annotation
	DocComment  string
}

func (v *Variant) Pos() token.Pos { return v.PosV }
func (v *Variant) End() token.Pos {
	if v.EndV.Line > 0 {
		return v.EndV
	}
	if n := len(v.Fields); n > 0 {
		return v.Fields[n-1].End()
	}
	return v.PosV
}

// InterfaceDecl — an interface declaration.
type InterfaceDecl struct {
	PosV        token.Pos
	EndV        token.Pos
	Pub         bool
	Name        string
	Generics    []*GenericParam
	Extends     []Type // composed interfaces (e.g. `Reader`, `Writer` inside)
	Methods     []*FnDecl
	DocComment  string
	Annotations []*Annotation
}

func (*InterfaceDecl) declNode()               {}
func (i *InterfaceDecl) Pos() token.Pos        { return i.PosV }
func (i *InterfaceDecl) End() token.Pos        { return i.EndV }
func (i *InterfaceDecl) Doc() string           { return i.DocComment }
func (i *InterfaceDecl) Annots() []*Annotation { return i.Annotations }

// TypeAliasDecl — `type Name<T> = ...`.
type TypeAliasDecl struct {
	PosV        token.Pos
	EndV        token.Pos
	Pub         bool
	Name        string
	Generics    []*GenericParam
	Target      Type
	DocComment  string
	Annotations []*Annotation
}

func (*TypeAliasDecl) declNode()               {}
func (t *TypeAliasDecl) Pos() token.Pos        { return t.PosV }
func (t *TypeAliasDecl) End() token.Pos        { return t.EndV }
func (t *TypeAliasDecl) Doc() string           { return t.DocComment }
func (t *TypeAliasDecl) Annots() []*Annotation { return t.Annotations }

// LetDecl — `pub let NAME = ...` at top level.
//
// MutPos records the position of the `mut` keyword when Mut is true so
// lint's `unused_mut` autofix can delete the token directly. Zero Pos
// when Mut is false.
type LetDecl struct {
	PosV        token.Pos
	EndV        token.Pos
	Pub         bool
	Mut         bool
	MutPos      token.Pos
	Name        string
	Type        Type // optional annotation
	Value       Expr // required for top-level let
	DocComment  string
	Annotations []*Annotation
}

func (*LetDecl) declNode()               {}
func (l *LetDecl) Pos() token.Pos        { return l.PosV }
func (l *LetDecl) End() token.Pos        { return l.EndV }
func (l *LetDecl) Doc() string           { return l.DocComment }
func (l *LetDecl) Annots() []*Annotation { return l.Annotations }

// ==== Types ====

// NamedType is a reference to a named type, possibly with type arguments.
// Example: `User`, `List<T>`, `Map<String, Int>`, `my.pkg.Type`.
type NamedType struct {
	PosV token.Pos
	EndV token.Pos
	// Path allows qualified references: pkg.Type -> {"pkg", "Type"}.
	Path []string
	Args []Type
}

func (*NamedType) typeNode()        {}
func (n *NamedType) Pos() token.Pos { return n.PosV }
func (n *NamedType) End() token.Pos { return n.EndV }

// OptionalType wraps an inner type: T?.
type OptionalType struct {
	PosV  token.Pos
	EndV  token.Pos
	Inner Type
}

func (*OptionalType) typeNode()        {}
func (o *OptionalType) Pos() token.Pos { return o.PosV }
func (o *OptionalType) End() token.Pos { return o.EndV }

// TupleType is `(T1, T2, ...)`.
type TupleType struct {
	PosV  token.Pos
	EndV  token.Pos
	Elems []Type
}

func (*TupleType) typeNode()        {}
func (t *TupleType) Pos() token.Pos { return t.PosV }
func (t *TupleType) End() token.Pos { return t.EndV }

// FnType is `fn(A, B) -> R`.
type FnType struct {
	PosV       token.Pos
	EndV       token.Pos
	Params     []Type
	ReturnType Type // nil == unit ()
}

func (*FnType) typeNode()        {}
func (f *FnType) Pos() token.Pos { return f.PosV }
func (f *FnType) End() token.Pos { return f.EndV }

// ==== Statements ====

// Block is a sequence of statements evaluated in order. As an expression,
// the result is the final expression statement.
type Block struct {
	PosV  token.Pos
	EndV  token.Pos
	Stmts []Stmt
}

func (*Block) stmtNode()        {}
func (*Block) exprNode()        {}
func (b *Block) Pos() token.Pos { return b.PosV }
func (b *Block) End() token.Pos { return b.EndV }

// LetStmt introduces one or more bindings: `let p = e`, `let (a, b) = e`,
// `let User { name, age } = e`, etc.
//
// MutPos records the `mut` keyword's position when Mut is true, enabling
// autofix of `let mut x` → `let x`. Zero Pos otherwise.
type LetStmt struct {
	PosV    token.Pos
	EndV    token.Pos
	Pattern Pattern
	Mut     bool
	MutPos  token.Pos
	Type    Type // optional annotation
	Value   Expr
}

func (*LetStmt) stmtNode()        {}
func (s *LetStmt) Pos() token.Pos { return s.PosV }
func (s *LetStmt) End() token.Pos { return s.EndV }

// ExprStmt is an expression used in statement position.
type ExprStmt struct {
	X Expr
}

func (*ExprStmt) stmtNode()        {}
func (s *ExprStmt) Pos() token.Pos { return s.X.Pos() }
func (s *ExprStmt) End() token.Pos { return s.X.End() }

// AssignStmt covers `a = b`, compound assigns, and multiple assigns
// `(a, b) = (c, d)`.
type AssignStmt struct {
	PosV    token.Pos
	EndV    token.Pos
	Op      token.Kind // ASSIGN, PLUSEQ, ...
	Targets []Expr     // LHS; length 1 for simple, >1 for multi-assign
	Value   Expr
}

func (*AssignStmt) stmtNode()        {}
func (s *AssignStmt) Pos() token.Pos { return s.PosV }
func (s *AssignStmt) End() token.Pos { return s.EndV }

// ReturnStmt — `return` or `return expr`.
type ReturnStmt struct {
	PosV  token.Pos
	EndV  token.Pos
	Value Expr // optional
}

func (*ReturnStmt) stmtNode()        {}
func (s *ReturnStmt) Pos() token.Pos { return s.PosV }
func (s *ReturnStmt) End() token.Pos { return s.EndV }

// BreakStmt is `break`.
type BreakStmt struct {
	PosV token.Pos
	EndV token.Pos
	// Value is the optional expression on `break <expr>` — only legal
	// inside a `loop { … }` expression (G22, §A.4). Nil for a bare
	// `break` inside a `for`/`while` loop, where a value would be a
	// type error.
	Value Expr
	// Label is the optional target-loop label on `break 'name` /
	// `break 'name <expr>` (G24, §4.4). Empty for a bare `break`. The
	// leading apostrophe is stripped during lexing; the stored text
	// is just the identifier.
	Label string
}

func (*BreakStmt) stmtNode()        {}
func (s *BreakStmt) Pos() token.Pos { return s.PosV }
func (s *BreakStmt) End() token.Pos { return s.EndV }

// ContinueStmt is `continue` — optionally `continue 'label` (G24).
type ContinueStmt struct {
	PosV  token.Pos
	EndV  token.Pos
	Label string
}

func (*ContinueStmt) stmtNode()        {}
func (s *ContinueStmt) Pos() token.Pos { return s.PosV }
func (s *ContinueStmt) End() token.Pos { return s.EndV }

// ChanSendStmt is `ch <- value`, the channel send statement of §8.5.
// The spec explicitly declares channel send a statement rather than an
// expression, so send cannot be composed with `=` or other operators.
type ChanSendStmt struct {
	PosV    token.Pos
	EndV    token.Pos
	Channel Expr
	Value   Expr
}

func (*ChanSendStmt) stmtNode()        {}
func (s *ChanSendStmt) Pos() token.Pos { return s.PosV }
func (s *ChanSendStmt) End() token.Pos { return s.EndV }

// DeferStmt schedules an expression or block to run on block exit.
type DeferStmt struct {
	PosV token.Pos
	EndV token.Pos
	X    Expr // may be a Block
}

func (*DeferStmt) stmtNode()        {}
func (s *DeferStmt) Pos() token.Pos { return s.PosV }
func (s *DeferStmt) End() token.Pos { return s.EndV }

// ForStmt covers all `for` forms:
//
//	for cond { ... }                      // while-style (Pattern nil, Iter = cond)
//	for { ... }                           // infinite   (Pattern nil, Iter nil)
//	for x in xs { ... }                   // for-in     (Pattern x, Iter xs)
//	for let Some(v) = e { ... }           // for-let    (IsForLet, Pattern, Iter)
type ForStmt struct {
	PosV     token.Pos
	EndV     token.Pos
	IsForLet bool
	Pattern  Pattern // optional
	Iter     Expr    // optional (nil = infinite)
	Body     *Block
	// Label is the optional `'name:` prefix (G24, §4.4) so enclosed
	// `break 'name` / `continue 'name` can target this loop. Empty
	// for unlabeled loops.
	Label string
}

func (*ForStmt) stmtNode()        {}
func (s *ForStmt) Pos() token.Pos { return s.PosV }
func (s *ForStmt) End() token.Pos { return s.EndV }

// ==== Expressions ====

// Ident references a name. Dotted paths (foo.bar.baz) are represented as
// FieldExpr chains; a single Ident is only a bare name.
type Ident struct {
	PosV token.Pos
	EndV token.Pos
	Name string
}

func (*Ident) exprNode()        {}
func (i *Ident) Pos() token.Pos { return i.PosV }
func (i *Ident) End() token.Pos { return i.EndV }

// Literal kinds carry the raw source text; conversion is deferred.
type IntLit struct {
	PosV token.Pos
	EndV token.Pos
	Text string
}

func (*IntLit) exprNode()        {}
func (l *IntLit) Pos() token.Pos { return l.PosV }
func (l *IntLit) End() token.Pos { return l.EndV }

type FloatLit struct {
	PosV token.Pos
	EndV token.Pos
	Text string
}

func (*FloatLit) exprNode()        {}
func (l *FloatLit) Pos() token.Pos { return l.PosV }
func (l *FloatLit) End() token.Pos { return l.EndV }

type CharLit struct {
	PosV  token.Pos
	EndV  token.Pos
	Value rune
}

func (*CharLit) exprNode()        {}
func (l *CharLit) Pos() token.Pos { return l.PosV }
func (l *CharLit) End() token.Pos { return l.EndV }

type ByteLit struct {
	PosV  token.Pos
	EndV  token.Pos
	Value byte
}

func (*ByteLit) exprNode()        {}
func (l *ByteLit) Pos() token.Pos { return l.PosV }
func (l *ByteLit) End() token.Pos { return l.EndV }

// StringLit is a string literal, possibly interpolated.
//
//	"hello"        -> Parts: [Lit "hello"]
//	"hi, {name}"   -> Parts: [Lit "hi, ", Expr <Ident name>]
//	r"\d+"         -> Parts: [Lit `\d+`], IsRaw=true
//	"""\n  x\n  """ -> Parts: [Lit "x"], IsTriple=true
//
// IsTriple records the original syntactic form so the formatter can
// emit multi-line content back in triple-quoted style. Semantically it
// is redundant — content is already normalized — but preserving it
// keeps authorial intent across `osty fmt` round-trips.
type StringLit struct {
	PosV     token.Pos
	EndV     token.Pos
	IsRaw    bool
	IsTriple bool
	Parts    []StringPart
}

// StringPart is one literal segment or interpolated expression.
type StringPart struct {
	IsLit bool
	Lit   string // when IsLit
	Expr  Expr   // when !IsLit
}

func (*StringLit) exprNode()        {}
func (l *StringLit) Pos() token.Pos { return l.PosV }
func (l *StringLit) End() token.Pos { return l.EndV }

// BoolLit is `true` / `false`.
type BoolLit struct {
	PosV  token.Pos
	EndV  token.Pos
	Value bool
}

func (*BoolLit) exprNode()        {}
func (l *BoolLit) Pos() token.Pos { return l.PosV }
func (l *BoolLit) End() token.Pos { return l.EndV }

// UnaryExpr is a prefix op: `-x`, `!x`, `~x`.
type UnaryExpr struct {
	PosV token.Pos
	EndV token.Pos
	Op   token.Kind
	X    Expr
}

func (*UnaryExpr) exprNode()        {}
func (e *UnaryExpr) Pos() token.Pos { return e.PosV }
func (e *UnaryExpr) End() token.Pos { return e.EndV }

// BinaryExpr is `a op b` for arithmetic, comparison, logical, bitwise,
// and the `??` coalescing operator.
type BinaryExpr struct {
	PosV  token.Pos
	EndV  token.Pos
	Op    token.Kind
	Left  Expr
	Right Expr
}

func (*BinaryExpr) exprNode()        {}
func (e *BinaryExpr) Pos() token.Pos { return e.PosV }
func (e *BinaryExpr) End() token.Pos { return e.EndV }

// QuestionExpr is the postfix `?` error/Option propagation.
type QuestionExpr struct {
	PosV token.Pos
	EndV token.Pos
	X    Expr
}

func (*QuestionExpr) exprNode()        {}
func (e *QuestionExpr) Pos() token.Pos { return e.PosV }
func (e *QuestionExpr) End() token.Pos { return e.EndV }

// CallExpr is `f(args)`.
//
// IsAsQuestion marks the call as desugared from `expr as? T` (G27, §7.4).
// The parser lowers `expr as? T` into `expr.downcast::<T>()` and sets this
// flag so the checker can enforce the §7.4 Error-bound rules specific to
// the sugar form (E0757) without mis-attributing plain `.downcast::<T>()`
// calls.
//
// HasTrailingClosure marks the call as coming from trailing-closure sugar
// `f(a) |x| { ... }` (G23, §A.2). The parser appends the closure to the
// positional arg list; this flag lets the formatter restore the surface
// form by splitting the last arg out of the parenthesised list.
type CallExpr struct {
	PosV               token.Pos
	EndV               token.Pos
	Fn                 Expr
	Args               []*Arg
	IsAsQuestion       bool
	HasTrailingClosure bool
}

// Arg is either positional (Name == "") or keyword (Name != "").
type Arg struct {
	PosV  token.Pos
	Name  string
	Value Expr
}

func (a *Arg) Pos() token.Pos { return a.PosV }
func (a *Arg) End() token.Pos {
	if a.Value != nil {
		return a.Value.End()
	}
	return a.PosV
}

func (*CallExpr) exprNode()        {}
func (e *CallExpr) Pos() token.Pos { return e.PosV }
func (e *CallExpr) End() token.Pos { return e.EndV }

// FieldExpr is `x.field` or `x?.field` (when IsOptional).
type FieldExpr struct {
	PosV       token.Pos
	EndV       token.Pos
	X          Expr
	Name       string
	IsOptional bool
}

func (*FieldExpr) exprNode()        {}
func (e *FieldExpr) Pos() token.Pos { return e.PosV }
func (e *FieldExpr) End() token.Pos { return e.EndV }

// IndexExpr is `x[i]`.
type IndexExpr struct {
	PosV  token.Pos
	EndV  token.Pos
	X     Expr
	Index Expr
}

func (*IndexExpr) exprNode()        {}
func (e *IndexExpr) Pos() token.Pos { return e.PosV }
func (e *IndexExpr) End() token.Pos { return e.EndV }

// TurbofishExpr is `expr::<T, U>`. It wraps a base expression with explicit
// type arguments. The parser attaches type args to the base (typically an
// Ident or FieldExpr) via this wrapper.
type TurbofishExpr struct {
	PosV token.Pos
	EndV token.Pos
	Base Expr
	Args []Type
}

func (*TurbofishExpr) exprNode()        {}
func (e *TurbofishExpr) Pos() token.Pos { return e.PosV }
func (e *TurbofishExpr) End() token.Pos { return e.EndV }

// RangeExpr is `a..b` / `a..=b` / `..b` / `a..`.
type RangeExpr struct {
	PosV      token.Pos
	EndV      token.Pos
	Start     Expr // optional
	Stop      Expr // optional
	Inclusive bool
	// Step is the optional stride from a `by <expr>` suffix (G25, §A.4).
	// `0..10 by 2` parses with Start=0, Stop=10, Inclusive=false, Step=2.
	// Nil when the user wrote no `by` clause.
	Step Expr
}

func (*RangeExpr) exprNode()        {}
func (e *RangeExpr) Pos() token.Pos { return e.PosV }
func (e *RangeExpr) End() token.Pos { return e.EndV }

// ParenExpr groups `(expr)` — needed to distinguish `(a)` from `(a,)` tuple.
type ParenExpr struct {
	PosV token.Pos
	EndV token.Pos
	X    Expr
}

func (*ParenExpr) exprNode()        {}
func (e *ParenExpr) Pos() token.Pos { return e.PosV }
func (e *ParenExpr) End() token.Pos { return e.EndV }

// TupleExpr is `(a, b, c)`. The single-element form `(a,)` is a tuple.
type TupleExpr struct {
	PosV  token.Pos
	EndV  token.Pos
	Elems []Expr
}

func (*TupleExpr) exprNode()        {}
func (e *TupleExpr) Pos() token.Pos { return e.PosV }
func (e *TupleExpr) End() token.Pos { return e.EndV }

// ListExpr is `[a, b, c]`.
type ListExpr struct {
	PosV  token.Pos
	EndV  token.Pos
	Elems []Expr
}

func (*ListExpr) exprNode()        {}
func (e *ListExpr) Pos() token.Pos { return e.PosV }
func (e *ListExpr) End() token.Pos { return e.EndV }

// MapExpr is `{"k": v, ...}` or `{:}` (empty).
type MapExpr struct {
	PosV    token.Pos
	EndV    token.Pos
	Entries []*MapEntry
	Empty   bool // true iff the literal is {:} with no entries
}

type MapEntry struct {
	Key   Expr
	Value Expr
}

func (e *MapEntry) Pos() token.Pos {
	if e.Key != nil {
		return e.Key.Pos()
	}
	return token.Pos{}
}
func (e *MapEntry) End() token.Pos {
	if e.Value != nil {
		return e.Value.End()
	}
	if e.Key != nil {
		return e.Key.End()
	}
	return token.Pos{}
}

func (*MapExpr) exprNode()        {}
func (e *MapExpr) Pos() token.Pos { return e.PosV }
func (e *MapExpr) End() token.Pos { return e.EndV }

// StructLit is `User { name: v, ..rest }`.
type StructLit struct {
	PosV   token.Pos
	EndV   token.Pos
	Type   Expr // usually Ident or FieldExpr chain naming the type
	Fields []*StructLitField
	Spread Expr // optional ..expr spread source
	// IsShorthand marks the G26 §A.2 update-shorthand form
	// `x { field: v }`. When set, `Type` carries the value `x` (not a
	// type name); the checker infers the actual type from `x` and
	// treats `Spread` (which the parser sets to `x`) as the spread
	// source. Plain `T { ..x, field: v }` keeps IsShorthand=false.
	IsShorthand bool
}

// StructLitField is one `name: expr` entry (or shorthand `name`).
type StructLitField struct {
	PosV  token.Pos
	Name  string
	Value Expr // nil iff shorthand (Name only)
}

func (f *StructLitField) Pos() token.Pos { return f.PosV }
func (f *StructLitField) End() token.Pos {
	if f.Value != nil {
		return f.Value.End()
	}
	return f.PosV
}

func (*StructLit) exprNode()        {}
func (e *StructLit) Pos() token.Pos { return e.PosV }
func (e *StructLit) End() token.Pos { return e.EndV }

// BlockExpr is a block used as an expression. We reuse *Block which already
// implements both Stmt and Expr.

// IfExpr covers `if cond { .. } else if cond { .. } else { .. }` and
// `if let pat = e { .. } else { .. }`.
type IfExpr struct {
	PosV    token.Pos
	EndV    token.Pos
	IsIfLet bool
	Pattern Pattern // non-nil iff IsIfLet
	Cond    Expr    // if-let: the RHS; plain if: the boolean
	Then    *Block
	Else    Expr // may be *IfExpr or *Block or nil
}

func (*IfExpr) exprNode()        {}
func (e *IfExpr) Pos() token.Pos { return e.PosV }
func (e *IfExpr) End() token.Pos { return e.EndV }

// LoopExpr is `loop { ... }` — an infinite loop whose value is the
// `break <expr>` that exits it (G22, §A.4). A `loop` with no `break`
// that produces a value has type `Never`; otherwise the type is the
// join of every break-value branch.
type LoopExpr struct {
	PosV token.Pos
	EndV token.Pos
	Body *Block
	// Label is the optional `'name:` prefix (G24, §4.4). Empty for
	// unlabeled `loop { … }`.
	Label string
}

func (*LoopExpr) exprNode()        {}
func (e *LoopExpr) Pos() token.Pos { return e.PosV }
func (e *LoopExpr) End() token.Pos { return e.EndV }

// MatchExpr is `match scrutinee { arm, arm, ... }`.
type MatchExpr struct {
	PosV      token.Pos
	EndV      token.Pos
	Scrutinee Expr
	Arms      []*MatchArm
}

// MatchArm is `pattern [if guard] -> body`.
type MatchArm struct {
	PosV    token.Pos
	Pattern Pattern
	Guard   Expr // optional
	Body    Expr // expression or block
}

func (a *MatchArm) Pos() token.Pos { return a.PosV }
func (a *MatchArm) End() token.Pos {
	if a.Body != nil {
		return a.Body.End()
	}
	return a.PosV
}

func (*MatchExpr) exprNode()        {}
func (e *MatchExpr) Pos() token.Pos { return e.PosV }
func (e *MatchExpr) End() token.Pos { return e.EndV }

// ClosureExpr is `|params| body` or `|params| -> T { body }`.
type ClosureExpr struct {
	PosV       token.Pos
	EndV       token.Pos
	Params     []*Param // types optional (inferred)
	ReturnType Type     // optional
	Body       Expr
}

func (*ClosureExpr) exprNode()        {}
func (e *ClosureExpr) Pos() token.Pos { return e.PosV }
func (e *ClosureExpr) End() token.Pos { return e.EndV }

// ==== Patterns ====

// WildcardPat is `_`.
type WildcardPat struct {
	PosV token.Pos
	EndV token.Pos
}

func (*WildcardPat) patternNode()     {}
func (p *WildcardPat) Pos() token.Pos { return p.PosV }
func (p *WildcardPat) End() token.Pos { return p.EndV }

// LiteralPat is a literal used as a pattern.
type LiteralPat struct {
	PosV    token.Pos
	EndV    token.Pos
	Literal Expr // IntLit, FloatLit, StringLit, CharLit, BoolLit, ByteLit
}

func (*LiteralPat) patternNode()     {}
func (p *LiteralPat) Pos() token.Pos { return p.PosV }
func (p *LiteralPat) End() token.Pos { return p.EndV }

// IdentPat is a pattern that binds an identifier.
type IdentPat struct {
	PosV token.Pos
	EndV token.Pos
	Name string
}

func (*IdentPat) patternNode()     {}
func (p *IdentPat) Pos() token.Pos { return p.PosV }
func (p *IdentPat) End() token.Pos { return p.EndV }

// TuplePat is `(a, b, _)`.
type TuplePat struct {
	PosV  token.Pos
	EndV  token.Pos
	Elems []Pattern
}

func (*TuplePat) patternNode()     {}
func (p *TuplePat) Pos() token.Pos { return p.PosV }
func (p *TuplePat) End() token.Pos { return p.EndV }

// StructPat is `Point { x, y: n, .. }`.
type StructPat struct {
	PosV   token.Pos
	EndV   token.Pos
	Type   []string // qualified name (e.g. pkg.Type)
	Fields []*StructPatField
	Rest   bool // trailing `..`
}

// StructPatField is one field entry: name (shorthand), name: pattern,
// or name: literal.
type StructPatField struct {
	PosV    token.Pos
	Name    string
	Pattern Pattern // nil for shorthand (binds to Name)
}

func (f *StructPatField) Pos() token.Pos { return f.PosV }
func (f *StructPatField) End() token.Pos {
	if f.Pattern != nil {
		return f.Pattern.End()
	}
	return f.PosV
}

func (*StructPat) patternNode()     {}
func (p *StructPat) Pos() token.Pos { return p.PosV }
func (p *StructPat) End() token.Pos { return p.EndV }

// VariantPat is `Some(x)`, `Rect(w, h)`, `Empty`, `Ok(Some(n))`, with
// optional qualified name `pkg.Variant(...)`.
type VariantPat struct {
	PosV token.Pos
	EndV token.Pos
	Path []string  // e.g. ["Color", "Red"] or ["Some"]
	Args []Pattern // empty for bare variants
}

func (*VariantPat) patternNode()     {}
func (p *VariantPat) Pos() token.Pos { return p.PosV }
func (p *VariantPat) End() token.Pos { return p.EndV }

// RangePat is `0..=9`, `10..20`, `..=0`, `100..`.
type RangePat struct {
	PosV      token.Pos
	EndV      token.Pos
	Start     Expr // optional
	Stop      Expr // optional
	Inclusive bool
}

func (*RangePat) patternNode()     {}
func (p *RangePat) Pos() token.Pos { return p.PosV }
func (p *RangePat) End() token.Pos { return p.EndV }

// OrPat is `A | B | C`.
type OrPat struct {
	PosV token.Pos
	EndV token.Pos
	Alts []Pattern
}

func (*OrPat) patternNode()     {}
func (p *OrPat) Pos() token.Pos { return p.PosV }
func (p *OrPat) End() token.Pos { return p.EndV }

// BindingPat is `name @ pattern`.
type BindingPat struct {
	PosV    token.Pos
	EndV    token.Pos
	Name    string
	Pattern Pattern
}

func (*BindingPat) patternNode()     {}
func (p *BindingPat) Pos() token.Pos { return p.PosV }
func (p *BindingPat) End() token.Pos { return p.EndV }
