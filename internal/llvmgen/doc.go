// Package llvmgen emits textual LLVM IR for the native backend.
//
// The first implementation is deliberately small: it supports no-arg main
// functions or script files, integer println expressions, plain ASCII string
// println literals with simple escapes, immutable scalar lets, mutable scalar
// locals, simple Int/Bool helper functions, statement-position if/else,
// value-position if/else, simple Bool logical operators, and inclusive/exclusive
// Int range loops.
// Unsupported shapes return ErrUnsupported so callers can fall back to
// inspectable skeleton IR while the backend grows. The module/function/skeleton
// renderers, scalar/string instruction builders, LLVM toolchain command plans,
// executable parity plans, and categorized unsupported/backend-capability
// diagnostics are generated from examples/selfhost-core/llvmgen.osty so the
// backend meaning is owned by Osty source.
package llvmgen
