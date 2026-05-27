# Toolchain Sources

`toolchain/` holds the Osty-authored sources that still feed the bootstrapped
front end, checker, and LLVM pipeline. It no longer contains generated Go
output; the committed bridge now lives under `internal/selfhost/`.

The exact merge inputs are defined in
[`internal/selfhost/bundle/bundle.go`](../internal/selfhost/bundle/bundle.go).
Today they pull from:

- `toolchain/*.osty` for the front-end/checker/backend-facing logic
- including `toolchain/lsp.osty` for the self-hosted LSP pure-policy surface

The native checker bundle deliberately excludes
`internal/selfhost/ast_lower.osty`. That file is still present under
`internal/selfhost/` as a public-AST compatibility adapter for legacy Go
callers, but it imports the Go `astbridge` surface and must not be part of the
native-selfhost checker input set.

Mainstream Go packages should call `internal/lexer`, `internal/parser`, and
`internal/check`. `internal/lexer` and `internal/parser` are thin facades over
`internal/selfhost`; `internal/check` routes through the native checker
subprocess in production (managed binary or `OSTY_NATIVE_CHECKER_BIN`), not an
embedded in-process fallback (`SUBPROCESS_SWITCHOVER.md`).

`internal/selfhost/generated.go` is a committed seed — the Osty→Go bootstrap
transpiler that produced it has been removed. Changes to `toolchain/*.osty`
reach Go via the LLVM self-host path only; there is no `go generate` regen
pipeline.

## Wire-format bridges (Go subprocess ↔ Osty toolchain)

Several modules exist only to decode or serialize JSON shapes that cross the
Go bridge. They are part of the native checker and `osty-self` pipelines, not
user-facing language surface:

- **`mir_json.osty`** — consumes MIR JSON from `internal/mirjson` and
  reconstructs the self-host MIR module used by LIR Proto lowering (no
  front-end re-run).
- **`check_json.osty`** — serializes checker output to the `CheckResult` wire
  format consumed by `internal/selfhost/api` (LLVM-built `osty-native-checker`
  uses this for real check results instead of a stub).
- **`check_imports.osty`** — decodes `PackageCheckImport` requests and installs
  imported surfaces into the elaboration environment so cross-package references
  resolve in multi-file / package-mode checks.

See the module table in [`ARCHITECTURE.md`](../ARCHITECTURE.md) (self-hosted
toolchain section) for approximate sizes and the file headers in each `.osty`
for pipeline ordering notes.
