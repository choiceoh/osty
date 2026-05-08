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
- `url-health`: concurrent URL health checker — enum with methods,
  `Result`/`?` propagation, range patterns, `taskGroup`, `Map.update`,
  and property-based testing.
- `v06_capabilities`: v0.6-style explicit `Clock` / `Rng` capability
  parameters with deterministic fakes and stdlib protocol helpers.
- `v06_capability_adapters`: v0.6 host-boundary adapter catalog showing how
  existing global stdlib modules become explicit capability values.
- `stdlib-tour`: front-end checked package that demonstrates Tier 1
  standard-library imports and Result-style error flow.
- `gui-webview2-inspector`: Windows WebView2 GUI example using
  `std.gui.webview2`, a native C ABI shim, and a small HTML/CSS/JS UI.
- `workspace`: virtual workspace with two member packages and a
  cross-package call from `cli` to `core`.

The canonical Osty-authored compiler/tooling sources now live in the
top-level `toolchain/` directory, not under `examples/`.
