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
// Out of scope for Phase 1 (future work):
//
//   - structs, enums, interfaces, type aliases (Phase 2)
//   - match expressions, pattern destructuring (Phase 2)
//   - generics, closures, collection methods (Phase 3)
//   - Option/Result and `?` operator, defer (Phase 4)
//   - use declarations, Go FFI (Phase 5)
//   - channels, concurrency primitives (Phase 6)
package gen
