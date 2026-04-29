// Package backend defines the small contract shared by concrete Osty
// code-generation backends.
//
// The CLI routes native emission through this package so every backend
// uses the same artifact / cache layout contract. The LLVM dispatcher
// (llvm.go) consumes IR exclusively: Request.Entry.IR is the sole
// semantic input it hands to llvmgen. Request.Entry.MIR is the required
// MIR projection prepared from that same IR for the MIR-direct emitter.
// The dispatcher never falls back to Request.Entry.File when lowering
// hits an unsupported shape.
//
// LLVM dispatch is driven by CapabilityMatrix, an explicit per-entry
// contract with rows for HIR node coverage, MIR lowering issues, MIR
// emitter support, runtime ABI requirements, route coverage, and fallback
// policy. Dispatch order is intentionally small and observable:
//
//   - `unsupported-preflight`: capability preflight rejects backend gaps
//     such as Go FFI or unknown runtime FFI before any concrete emitter is
//     selected.
//   - `native-owned`: the native-owned llvmgen slice gets the first chance
//     when the feature set allows it and no injected stdlib bodies are present.
//   - `mir-direct`: every remaining normal backend request routes through
//     MIR-direct emission; MIR route blockers are read from the same
//     capability matrix before emission.
//
// Unsupported input is not a fatal compiler crash and is not silently
// retried through an older AST path. The backend writes an inspectable
// skeleton artifact, returns ErrLLVMNotImplemented, and includes the
// structured LLVM00x diagnostic plus the backend route in Result.Warnings.
// Set OSTY_BACKEND_TRACE=1 to stream the selected LLVM dispatch route to
// stderr while debugging backend selection.
//
// LLVM lowering is still small, but it can produce textual IR and
// drive a host clang toolchain for supported object / binary
// artifacts. LLVM backend meaning, including scalar instruction
// builders, plain / escaped ASCII string constants / local values /
// function values, simple struct aggregate values, payload-free and
// single-`Int` payload enum tag values, payload-free and payload-enum
// match expressions, and unsupported-source diagnostic categories,
// including `Float` / `String` payload enum smoke generalisation
// ownership and policy (Phase 54-63) and the phase 64-73 value /
// control-flow smoke expansion, is authored in Osty toolchain sources.
// `Float32` / `Float64` policy is deferred. LLVM binary emission also
// links a local backend runtime ABI object for the `osty.gc.*` surface
// from the backend runtime directory at native link time rather than
// relying on Go GC parity. This package remains the bootstrap host
// shim for file I/O and process execution.
package backend
