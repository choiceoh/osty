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
// Phase 5 (Go FFI — §12):
//
//   - `use go "path"` / `as alias` emit real Go imports.
//   - FFI struct declarations emit a Go type alias (`type Foo = pkg.Foo`)
//     so value-carrying calls and field access bind to the real type.
//   - Struct methods and free fns inside the block are schema-only
//     signatures; validated by the parser and used by the checker, but
//     no Osty body is emitted. Dispatch happens through the package
//     import plus the Go type alias.
//   - `Result<T, Error>` returns are lifted at the call site: the Go
//     `(T, error)` tuple becomes an Osty Result, and Go errors are
//     wrapped in `basicFFIError` so `.message()` stays callable. See
//     internal/gen/ffi.go.
//   - `T?` returns pass through directly — Osty Optional already lowers
//     to Go `*T`.
//   - The prelude `Error` interface lowers to a runtime `ostyError`
//     interface with `message() string`; concrete Osty error types
//     satisfy it structurally.
//
// §12.5 (Go goroutines/channels) and §12.6 (panic abort) are spec-hard
// boundaries, not deferred features: channel-typed FFI declarations
// are rejected at parse time (E0103) and Go panics abort the process
// with their default behaviour.
package gen
