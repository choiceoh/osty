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
reach production front-end checks via the **LLVM-built** managed
`osty-native-checker` subprocess (`toolchain.EnsureNativeChecker`, PR #1954),
not by regenerating `generated.go`. The seed remains for Go-bootstrap builds and
tests only. There is no `go generate` regen pipeline. Trajectory:
[`docs/llvm-selfhost-plan.md`](../docs/llvm-selfhost-plan.md),
[`cmd/osty-native-checker/README.md`](../cmd/osty-native-checker/README.md).
