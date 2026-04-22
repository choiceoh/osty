// Package gen translates a type-checked Osty AST into Go source code for the
// bootstrap path. It is not part of the public compiler surface; the public
// wrapper lives in internal/bootstrap/seedgen, and the thin developer CLI is
// cmd/osty-bootstrap-gen. Together they regenerate internal/selfhost/generated.go
// from the toolchain sources.
//
// The transpiler runs after the front-end pipeline (lex → parse → resolve
// → check) and consumes:
//
//   - *ast.File — the parsed syntax tree
//   - *resolve.Result — symbol resolution (Refs, TypeRefs)
//   - *check.Result — semantic types (Types, LetTypes, SymTypes, Instantiations)
//
// No AST nodes are mutated; the package is a pure read-side consumer of
// the front-end. Output is `package main` Go for the bootstrap seed compiler;
// the public compiler path uses the native LLVM backend instead.
//
// Phase 1 coverage (what's implemented):
//
//   - top-level fn declarations (params, return type, body)
//   - script mode (top-level statements wrapped into main())
//   - primitive types: Int, Int8..Int64, UInt8..UInt64, Byte, Float,
//     Float32, Float64, Bool, Char, String, Bytes
//   - primitive literals (int, float, bool, char, byte, string)
//   - string interpolation via fmt.Sprintf
//   - let bindings (simple, no destructuring)
//   - binary/unary expressions (arithmetic, comparison, logical, bitwise)
//   - if / else (statement form)
//   - for loops: integer range, while-style, infinite
//   - return statements (explicit + implicit block-final-expr)
//   - println / print / eprintln / eprint intrinsics
//   - user function calls
//   - list literals over primitive types
//
// Phases 2–4 coverage (what's implemented beyond Phase 1):
//
//   - structs, enums, interfaces, type aliases (Phase 2)
//   - match expressions, pattern destructuring (Phase 2)
//   - generics, closures, collection methods (Phase 3)
//   - Option / Result and `?` operator, defer (Phase 4)
//
// Result is lowered to a parametric Go struct (Value/Error/IsOk) and
// pattern tests read `IsOk` directly rather than using type assertions
// (see internal/bootstrap/gen/question.go and the Result helpers in match.go).
//
// The `?` operator is lifted out of any direct-evaluation position by
// preLiftQuestions: a statement that contains one or more `?` gets a
// prelude of `tmp := operand; if failure { return … }` pairs, and the
// original expression is rewritten to read `tmp.Value` / `*tmp`. This
// covers `let x = e?`, `return e?`, `e?` as a discard-statement, and
// `?` nested in call arguments, binary operators, field chains, and
// implicit block-final returns. Control-flow boundaries (closures,
// if/match arms, loop bodies) are *not* crossed by the lift — a `?`
// inside them binds to its own enclosing return context.
//
// Phases 5–6 coverage:
//
//   - use declarations and legacy/runtime FFI shims (Phase 5)
//   - channels and structured-concurrency primitives (Phase 6)
//
// Remaining TODO markers in emitted Go are bootstrap-coverage issues for
// particular AST shapes, not unresolved language semantics. The v0.4
// language-decision register lives in SPEC_GAPS.md.
package gen
