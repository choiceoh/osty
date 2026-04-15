// Package ir defines an independent intermediate representation (IR)
// for Osty programs. It is a self-contained, backend-agnostic tree
// that sits between the type-checked front-end and the Go transpiler /
// future backends.
//
// Pipeline position:
//
//	source → lexer → parser → resolve → check → ir (this package) → gen
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
//     while / range / in), DeferStmt, ChanSendStmt, MatchStmt,
//     ErrorStmt.
//   - Exprs: literals (int/float/bool/char/byte/string/unit), Ident,
//     UnaryExpr, BinaryExpr, CoalesceExpr, QuestionExpr, CallExpr,
//     IntrinsicCall (print-family), MethodCall, VariantLit, FieldExpr,
//     TupleAccess, IndexExpr, ListLit, MapLit, TupleLit, StructLit,
//     RangeLit, Closure, BlockExpr, IfExpr, IfLetExpr, MatchExpr,
//     ErrorExpr.
//   - Patterns: WildPat, IdentPat, LitPat, TuplePat, StructPat,
//     VariantPat, RangePat, OrPat, BindingPat, ErrorPat.
//   - Types: PrimType (with canonical singletons), NamedType,
//     OptionalType, TupleType, FnType, TypeVar, ErrType.
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
//     backend development.
//
// Known gaps (follow-on work)
//
//   - The Go transpiler (internal/gen) still consumes the AST
//     directly. Rewriting it against `ir` is a substantial refactor
//     tracked as a separate effort.
//
//   - Generic monomorphisation info (check.Result.Instantiations) is
//     not yet threaded through; backends that need per-call-site
//     type arguments should consult the checker until the IR
//     surfaces them.
//
//   - Destructuring `for` heads lower to ForIn with an empty Var and
//     a note; a ForDestructure variant may emerge when a consumer
//     actually needs it.
package ir
