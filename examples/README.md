# Osty Examples

These examples are part of the compiler exercise corpus. They are intentionally
kept under `go test` coverage so syntax, name resolution, type checking,
code generation, and the test harness keep exercising real programs as
the compiler evolves.

## Packages

- `calc`: small library used by the `osty test` harness smoke test.
- `ffi`: legacy Go FFI example kept for bootstrap/checker coverage.
- `gc`: large executable GC model kept for prototype/invariant coverage.
  It is no longer the primary implementation path for native runtime GC;
  see [`../RUNTIME_GC.md`](../RUNTIME_GC.md).
- `concurrency`: runnable example covering channels, `spawn`,
  `parallel`, and `taskGroup`.
- `stdlib-tour`: front-end checked package that demonstrates Tier 1
  standard-library imports and Result-style error flow.
- `workspace`: virtual workspace with two member packages and a
  cross-package call from `cli` to `core`.

The canonical Osty-authored compiler/tooling sources now live in the
top-level `toolchain/` directory, not under `examples/`.
