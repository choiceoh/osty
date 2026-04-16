// Package backend defines the small contract shared by concrete Osty
// code-generation backends.
//
// The CLI routes native emission through this package so every backend uses the
// same artifact/cache layout contract. LLVM lowering is still small, but it can
// produce textual IR and drive a host clang toolchain for supported
// object/binary artifacts. LLVM backend meaning, including scalar instruction
// builders, plain/escaped ASCII string constants/local values/function values,
// simple struct aggregate values, payload-free and single-`Int` payload enum tag
// values, payload-free and payload-enum match expressions, and
// unsupported-source diagnostic categories, including `Float`/`String` payload
// enum smoke generalization ownership and policy (Phase 54-63), is authored in
// Osty toolchain sources. `Float32`/`Float64` policy is deferred. This package
// remains the host shim for file I/O and process execution.
package backend
