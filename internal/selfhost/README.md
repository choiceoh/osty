# Selfhost Front End

`internal/selfhost` is the default lexer and parser implementation used by the
Go toolchain. The implementation is authored in Osty and compiled into Go.

The source of truth is:

- `examples/selfhost-core/semver.osty`
- `examples/selfhost-core/semver_parse.osty`
- `examples/selfhost-core/frontend.osty`
- `examples/selfhost-core/lexer.osty`
- `examples/selfhost-core/parser.osty`
- `examples/selfhost-core/formatter_ast.osty`
- `examples/selfhost-core/check_bridge.osty`
- `examples/selfhost-core/check.osty`
- `internal/selfhost/ast_lower.osty`

`examples/selfhost-core/check_bridge.osty` supplies only the small parser
adapter needed by the shared checker API, so the self-hosted front end and
checker logic have one implementation.

Regenerate the Go bridge with:

```sh
go generate ./internal/selfhost
```

The generator merges the selfhost-core sources, emits `generated.go` through
`cmd/osty gen`, regenerates `internal/selfhost/astbridge/generated.go` from
`internal/ast`, and reapplies the small Go hot-path overrides that keep lexing
position lookups linear. Public compiler packages should call
`internal/lexer` and `internal/parser` for the canonical front end, and
`internal/check` for type checking. The `internal/check` entrypoints route
mainstream checker diagnostics through `internal/selfhost.CheckSource` while
the legacy Go checker continues to populate structural maps for codegen and
editor features. `internal/selfhost.CheckSourceStructured` exposes typed
nodes, bindings, symbols, and generic instantiations for replacing those maps
incrementally. This package is the adaptation boundary for bootstrapped code.
