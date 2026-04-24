// Package ir defines an independent intermediate representation (IR)
// for Osty programs. It is a self-contained, backend-agnostic tree
// that sits between the type-checked front-end and native backends.
//
// Pipeline position:
//
//	source → lexer → parser → resolve → check → ir (this package) → backend
//
// Goals:
//
//   - Independence. No node in this package references `ast`,
//     `resolve`, or `check`. Backends may depend on `ir` alone. Types
//     (`ir.Type`), declarations, statements, expressions and patterns
//     are all self-describing: names resolve as plain strings, every
//     expression stores its own type, and symbol classification is
//     carried via enum kinds (IdentKind).
//
//   - Stability. The IR is a normalised view: syntactic sugar is
//     pruned (parens unwrapped, block-final expressions hoisted into
//     Block.Result, turbofish folded into the call's type args), and
//     constructs are classified into distinct nodes rather than a
//     single polymorphic shape (MethodCall vs CallExpr, VariantLit vs
//     CallExpr, IfLetExpr vs IfExpr, TupleAccess vs FieldExpr, etc.).
//
//   - Lossy where appropriate. Doc comments, annotation metadata
//     unused downstream, parser-suppression state, etc. are dropped.
//     Every node keeps a Span so diagnostics remain anchorable.
//
// Surface area
//
//   - Module: package name, top-level Decls, script-mode Stmts.
//   - Decls: FnDecl, StructDecl, EnumDecl, InterfaceDecl, TypeAliasDecl,
//     LetDecl, UseDecl.
//   - Stmts: Block, LetStmt, ExprStmt, AssignStmt (multi-target),
//     ReturnStmt, BreakStmt, ContinueStmt, IfStmt, ForStmt (inf /
//     while / range / in, with optional destructuring pattern),
//     DeferStmt, ChanSendStmt, MatchStmt (arms + optional DecisionTree),
//     ErrorStmt.
//   - Exprs: literals (int/float/bool/char/byte/string/unit), Ident
//     (carries TypeArgs for bare-turbofish references), UnaryExpr,
//     BinaryExpr, CoalesceExpr, QuestionExpr, CallExpr (TypeArgs + Args),
//     IntrinsicCall, MethodCall, VariantLit, FieldExpr, TupleAccess,
//     IndexExpr, ListLit, MapLit, TupleLit, StructLit, RangeLit,
//     Closure (Captures), BlockExpr, IfExpr, IfLetExpr, MatchExpr (arms
//   - optional DecisionTree), ErrorExpr.
//   - Patterns: WildPat, IdentPat, LitPat, TuplePat, StructPat,
//     VariantPat, RangePat, OrPat, BindingPat, ErrorPat.
//   - Types: PrimType (with canonical singletons), NamedType (Package
//     qualifier + Name), OptionalType, TupleType, FnType, TypeVar,
//     ErrType.
//   - Arg: {Name, Value, SpanV} — keyword arguments preserved on
//     CallExpr, MethodCall, IntrinsicCall, VariantLit.
//   - Capture: {Name, Kind, T, Mut, SpanV} — per-Closure free-variable
//     list computed during lowering.
//   - DecisionNode: DecisionLeaf / DecisionFail / DecisionBind /
//     DecisionGuard / DecisionSwitch — compiled match decision tree.
//
// Tools
//
//   - Lower(pkg, file, res, chk) → (*Module, []error) — convert a
//     type-checked AST into an IR Module. Non-fatal issues are
//     returned separately; poisoned spots collapse to ErrorStmt /
//     ErrorExpr / ErrorPat rather than panicking.
//
//   - Walk(v, n) / Inspect(n, fn) — pre-order traversal over every
//     reachable Node (decl, stmt, expr, pattern).
//
//   - Print(m) / PrintNode(n) — stable S-expression-like debug
//     renderer for tests and visual inspection.
//
//   - Validate(m) — lightweight structural sanity check useful during
//     backend development. ValidateDecisionTree(n, armCount) checks a
//     compiled match decision tree in isolation.
//
//   - Optimize(m, opts) — an IR-level optimiser that performs constant
//     folding, algebraic simplification, dead-code elimination, and
//     branch-literal folding. Every pass is opt-outable via
//     OptimizeOptions.
//
//   - ComputeCaptures(body, params) — free-variable analysis over a
//     closure body.
//
//   - CompileDecisionTree(scrutineeT, arms) — build a decision tree
//     for a match.
//
// Known gaps (follow-on work)
//
//   - The native LLVM backend's public API is IR-only: the dispatcher
//     (internal/backend/llvm.go) accepts lowered IR modules and there
//     is no fallback to the AST. Internally, llvmgen still routes the
//     IR through a local IR→AST bridge (llvmgen.legacyFileFromModule)
//     before feeding the long-standing emitter. That bridge is an
//     implementation detail, not a public contract.
//
//   - The IR remains a typed tree — no SSA/CFG level. Flow-sensitive
//     analyses (escape, lifetime, nullability) would need a MIR-style
//     lowering beneath this package.
package ir
