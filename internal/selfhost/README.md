# Selfhost Front End

`internal/selfhost` is the default lexer and parser implementation used by the
Go toolchain. The implementation is authored in Osty and compiled into Go.

The source of truth is:

- `examples/dogfood/semver.osty`
- `examples/dogfood/semver_parse.osty`
- `examples/dogfood/frontend.osty`
- `examples/dogfood/lexer.osty`
- `examples/dogfood/parser.osty`
- `examples/dogfood/checker.osty`
- `internal/selfhost/ast_lower.osty`

Regenerate the Go bridge with:

```sh
go generate ./internal/selfhost
```

The generator merges the dogfood sources, emits `generated.go` through
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
