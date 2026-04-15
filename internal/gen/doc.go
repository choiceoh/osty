// Package gen translates a type-checked Osty AST into Go source code.
//
// The transpiler runs after the front-end pipeline (lex → parse → resolve
// → check) and consumes:
//
//   - *ast.File — the parsed syntax tree
//   - *resolve.Result — symbol resolution (Refs, TypeRefs)
//   - *check.Result — semantic types (Types, LetTypes, SymTypes, Descs)
//
// No AST nodes are mutated; the package is a pure read-side consumer of
// the front-end. Output is `package main` Go that can be fed directly to
// `go run` / `go build`.
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
// (see internal/gen/question.go and the Result helpers in match.go).
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
// Phase 5 coverage (Go FFI — §12):
//
//   - `use go "path"` and `use go "path" as alias` emit real Go
//     imports; call sites to an imported fn resolve through the Go
//     package.
//   - Return-type bridging for `Result<T, Error>` (§12.4): the Go
//     side's `(T, error)` tuple is wrapped into the Osty Result
//     runtime at the call site. The `Result<(), Error>` shape maps
//     the bare-error Go convention `func(...) error`. See
//     internal/gen/ffi.go.
//   - Return-type bridging for `T?` (§12.3): Osty Optional lowers to
//     `*T`, which matches the Go nullable convention directly — no
//     call-site rewrite needed.
//   - The parser enforces §12.7: fn-typed (closure) parameters or
//     return types inside `use go` blocks are rejected with E0103.
//
// Out of scope (future work):
//
//   - §12.4 `BasicError` wrapping for concrete Osty Error types
//     (currently the Go `error` value flows through as `any`)
//   - channels, concurrency primitives (Phase 6)
package gen
