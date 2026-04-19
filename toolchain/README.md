# Toolchain Sources

`toolchain/` holds the Osty-authored sources that still feed the bootstrapped
front end, checker, and LLVM pipeline. It no longer contains generated Go
output; the committed bridge now lives under `internal/selfhost/`.

The exact merge inputs are defined in
[`internal/selfhost/bundle/bundle.go`](../internal/selfhost/bundle/bundle.go).
Today they pull from:

- `toolchain/*.osty` for the front-end/checker/backend-facing logic
- `examples/selfhost-core/*.osty` for the committed selfhost bundle pieces
- `internal/selfhost/ast_lower.osty` for the Go-bridge lowering step

Mainstream Go packages should call `internal/lexer`, `internal/parser`, and
`internal/check`. `internal/lexer` and `internal/parser` are thin facades over
`internal/selfhost`; `internal/check` prefers the external native-checker
boundary and falls back to the embedded selfhost bridge when needed.

There is no `go generate ./toolchain` path anymore. To refresh the committed
Go bridge, run:

```sh
go generate ./internal/selfhost
```

That regeneration flow is implemented in
[`internal/selfhost/gen_selfhost.go`](../internal/selfhost/gen_selfhost.go) and
invokes `cmd/osty-bootstrap-gen` plus the `astbridge` generator.
