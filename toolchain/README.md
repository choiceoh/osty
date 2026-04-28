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
`internal/selfhost`; `internal/check` prefers the external native-checker
boundary and falls back to the embedded selfhost bridge when needed.

`internal/selfhost/generated.go` is a committed seed — the Osty→Go bootstrap
transpiler that produced it has been removed. Changes to `toolchain/*.osty`
reach Go via the LLVM self-host path only; there is no `go generate` regen
pipeline.
