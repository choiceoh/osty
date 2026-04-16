# Selfhost Front End

`internal/selfhost` is the default lexer and parser implementation used by the
Go toolchain. The implementation is authored in Osty and compiled into Go.

The source of truth is:

- `examples/selfhost-core/semver.osty`
- `examples/selfhost-core/semver_parse.osty`
- `toolchain/frontend.osty`
- `toolchain/lexer.osty`
- `toolchain/parser.osty`
- `examples/selfhost-core/formatter_ast.osty`
- `toolchain/check_bridge.osty`
- `toolchain/diagnostic.osty`
- `toolchain/check_diag.osty`
- `toolchain/ty.osty`
- `toolchain/core.osty`
- `toolchain/check_env.osty`
- `toolchain/solve.osty`
- `toolchain/elab.osty`
- `toolchain/check.osty`
- `internal/selfhost/ast_lower.osty`

`toolchain/check_bridge.osty` supplies the small parser adapter needed by the
shared checker API, and the rest of the checker now comes directly from the
same `toolchain/*.osty` sources the native checker path exercises.

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
mainstream checker diagnostics through `internal/selfhost.CheckSourceStructured`
and bridge its typed nodes, bindings, declaration symbols, and generic
instantiations onto the resolver symbols and AST nodes consumed by codegen and
editor features. This package is the adaptation boundary for bootstrapped code.
