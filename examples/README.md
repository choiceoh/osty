# Osty Examples

These examples are part of the compiler exercise corpus. They are intentionally
kept under `go test` coverage so syntax, name resolution, type checking,
code generation, and the test harness keep exercising real programs as
the compiler evolves.

## Packages

- `calc`: small library used by the `osty test` harness smoke test.
- `ffi`: runnable Go FFI example using the Go `strings` package.
- `gc`: independent garbage-collector core written in Osty, covering
  generational collection, remembered-set barriers, incremental marking,
  cycle reclamation, compaction, pinning, and allocation telemetry.
- `concurrency`: runnable example covering channels, `spawn`,
  `parallel`, and `taskGroup`.
- `selfhost-core`: standalone package for the self-hosting algorithms,
  including the
  runnable lexer scanner, front-end seed models, and broad example-style
  tests for SemVer, registry selection, manifest features, diagnostics,
  package archive classification, a pure Osty semantic IR bridge,
  Osty-written linter and resolver cores, the shared self-hosted checker
  through a thin package bridge, plus the pure Osty formatter.
- `stdlib-tour`: front-end checked package that demonstrates Tier 1
  standard-library imports and Result-style error flow.
- `workspace`: virtual workspace with two member packages and a
  cross-package call from `cli` to `core`.
