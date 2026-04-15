# Osty Examples

These examples are part of the dogfood corpus. They are intentionally
kept under `go test` coverage so syntax, name resolution, type checking,
code generation, and the test harness keep exercising real programs as
the compiler evolves.

## Packages

- `calc`: small library used by the `osty test` harness smoke test.
- `dogfood`: self-dogfooding library that summarizes Osty source text,
  runs std.testing tests, calls Go FFI, and uses concurrency primitives.
- `ffi`: runnable Go FFI example using the Go `strings` package.
- `concurrency`: runnable example covering channels, `spawn`,
  `parallel`, and `taskGroup`.
- `stdlib-tour`: front-end checked package that demonstrates Tier 1
  standard-library imports and Result-style error flow.
- `workspace`: virtual workspace with two member packages and a
  cross-package call from `cli` to `core`.
