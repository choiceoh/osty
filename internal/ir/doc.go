// Package ir defines an independent intermediate representation (IR) for
// Osty programs. It is a self-contained, backend-agnostic tree that sits
// between the type-checked front-end and the Go transpiler / future
// backends.
//
// Pipeline position:
//
//	source → lexer → parser → resolve → check → ir (this package) → gen
//
// Goals:
//
//   - Independence. No node in this package references `ast`, `resolve`,
//     or `check`. Backends may depend on `ir` alone. Types (`ir.Type`),
//     declarations, statements and expressions are all self-describing:
//     names resolve as plain strings, symbol references carry the kind
//     they point at, and every expression stores its own type.
//
//   - Stability. The IR is a normalised view: syntactic sugar is pruned
//     (parens unwrapped, `ExprStmt` around block-final expressions turned
//     into `Block.Result`, turbofish dropped into the call's type args),
//     untyped literals are resolved to their concrete primitive type,
//     and source-order-only variants are merged (e.g. there is one
//     `ForStmt` with a variant kind field instead of three AST kinds).
//
//   - Lossy where appropriate. Things irrelevant to later stages (doc
//     comments, annotations other than `deprecated`/`json`, parser-
//     suppression state) are dropped. Source positions are retained in a
//     `Span` field on every node so diagnostics remain anchorable.
//
// Lower(file, res, chk) is the canonical entry point. It returns a
// *Module carrying every top-level declaration, script-mode statements
// (if any), and a list of issues encountered during lowering. Lowering
// is best-effort: constructs that are not yet supported collapse to an
// IR `ErrorExpr` / `ErrorStmt` and append a note to the issue list — the
// same "keep going so later problems surface" philosophy used by the
// parser and resolver.
//
// Phase 1 of the backend needs only a modest subset of Osty, so Lower
// implements that subset today (primitives, fn decls, let bindings,
// if/for/return, list literals, print intrinsics, user calls). Larger
// surface area (structs, enums, match, `?`, generics, closures) is
// staged in follow-on work; the IR types already carry nodes for those
// so backends written against `ir` stay forward-compatible.
package ir
