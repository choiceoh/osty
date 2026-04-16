// Package llvmgen emits textual LLVM IR for the native backend.
//
// The first implementation is deliberately small: it supports no-arg main
// functions or script files, integer println expressions, plain ASCII string
// println literals, local string values with simple escapes, simple String
// returns/parameters, simple value-aggregate structs, payload-free enum tags,
// single-Int payload enum tags (`%Enum = { i64, i64 }`), payload-free enum
// matches, payload-enum match expressions with payload binding, immutable
// scalar lets, mutable scalar locals, simple Int/Bool helper functions,
// statement-position if/else,
// value-position if/else, simple Bool logical operators, and inclusive/
// exclusive Int range loops. This phase also includes the `Float` smoke
// subset as a `double`-backed subset, plus the phase 64-73 value/control-flow
// smoke expansion for `if`, `Bool`, exclusive range, unary/modulo, and struct
// field coverage.
// Unsupported shapes return ErrUnsupported so callers can fall back to
// inspectable skeleton IR while the backend grows. The module/function/skeleton
// renderers, scalar/string/struct/enum instruction builders, compare/binary/
// logical instruction policies, identifier/ASCII-string/fallback policies,
// zero-literal defaults, declaration/type/signature naming policies,
// declaration-layout/runtime-FFI validation policies, LLVM toolchain command
// plans, executable parity plans, and categorized
// unsupported/backend-capability diagnostics are generated from
// toolchain/llvmgen.osty so the backend meaning is owned by Osty
// source. This includes phase-54~63 payload generalization for `Float` and
// `String` payload enums (return/param/mut/reversed/wildcard match coverage).
// Qualified constructor/pattern forms remain for a later phase.
package llvmgen
