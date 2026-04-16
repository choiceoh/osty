// Package backend defines the small contract shared by concrete Osty
// code-generation backends.
//
// The CLI routes Go and LLVM emission through this package so both backends use
// the same artifact/cache layout contract. LLVM lowering is still small, but it
// can produce textual IR and drive a host clang toolchain for supported
// object/binary artifacts. LLVM backend meaning, including scalar instruction
// builders, plain/escaped ASCII string constants/local values/function values,
// simple struct aggregate values, payload-free and single-`Int` payload enum tag
// values, payload-free and payload-enum match expressions, and
// unsupported-source diagnostic categories, including `Float` double-subset smoke
// ownership and policy, is authored in Osty selfhost-core. `Float32`/`Float64`
// policy is deferred. This package remains the bootstrap host shim for file I/O
// and process execution.
package backend
