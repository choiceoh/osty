// Package llvmgen emits textual LLVM IR for the native backend.
//
// Public API surface (IR-only)
//
//   - GenerateModule(*ir.Module, Options) ([]byte, error) — the
//     primary entry point for code generation. Consumes the
//     backend-neutral IR produced by `internal/ir`.
//   - TryGenerateNativeOwnedModule(*ir.Module, Options)
//     ([]byte, bool, error) — native-owned primitive/control-flow
//     fast path only. Returns ok=false when the module still needs the
//     transitional legacy fallback.
//
// The package previously exposed Generate(*ast.File, Options) as an
// alternate entry point. That AST route has been removed: the LLVM
// backend dispatcher (internal/backend/llvm.go) no longer accepts an
// AST, and the in-package helper generateFromAST is unexported so
// only in-package tests can reach it. All external callers route
// through IR.
//
// # Supported source shapes
//
// The current implementation is deliberately small: no-arg main
// functions or script files, integer println expressions, plain ASCII
// string println literals, local string values with simple escapes,
// simple String returns/parameters, simple value-aggregate structs,
// payload-free enum tags, single-Int payload enum tags
// (`%Enum = { i64, i64 }`), payload-free enum matches, payload-enum
// match expressions with payload binding, immutable scalar lets,
// mutable scalar locals, simple Int/Bool helper functions, statement-
// and value-position if/else, simple Bool logical operators, and
// inclusive/exclusive Int range loops, plus the phase 54-63 payload
// generalisation for `Float` and `String` payload enums and the phase
// 64-73 value/control-flow smoke expansion.
//
// Unsupported shapes return ErrUnsupported so the backend dispatcher
// can fall back to inspectable skeleton IR while the backend grows.
//
// Implementation note (transitional)
//
// GenerateModule now first tries a native-owned primitive/control-flow
// slice mirrored from `toolchain/llvmgen.osty` and falls back to the
// legacy IR -> AST bridge only for shapes still outside that slice
// (see ir_module.go). TryGenerateNativeOwnedModule exposes just that
// native-owned slice without taking the fallback. The bridge remains a
// transitional implementation detail: callers never construct nor
// observe the intermediate AST. Follow-on work will keep growing the
// native path until the fallback disappears entirely.
//
// The active GC implementation path paired with this lowering lives
// in `internal/backend/runtime/osty_runtime.c`.
package llvmgen
