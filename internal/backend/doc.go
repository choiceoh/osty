// Package backend defines the small contract shared by concrete Osty
// code-generation backends.
//
// The CLI routes Go and LLVM emission through this package so both backends use
// the same artifact/cache layout contract. LLVM lowering is still small, but it
// can produce textual IR and drive a host clang toolchain for supported
// object/binary artifacts. LLVM backend meaning, including scalar instruction
// builders, plain/escaped ASCII string constants/local values/function values,
// simple struct aggregate values, payload-free enum tag values, and
// unsupported-source diagnostic categories, is authored in Osty
// selfhost-core; this package remains the bootstrap host shim for file I/O and
// process execution.
package backend
