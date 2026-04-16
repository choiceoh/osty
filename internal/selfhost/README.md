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
- `examples/selfhost-core/resolve.osty`
- `examples/selfhost-core/lint.osty`
- `examples/selfhost-core/lsp.osty`
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
mainstream checker diagnostics through `internal/selfhost.CheckSourceStructured`
and bridge its typed nodes, bindings, declaration symbols, and generic
instantiations onto the resolver symbols and AST nodes consumed by codegen and
editor features. LSP editor policy, including UTF-16 position/range conversion,
semantic-token classification, completion buckets and prefix context,
declaration-name lookup, code-action filtering, outline/workspace symbol kind
selection and sorting, cursor/range checks, signature labels, diagnostic
payload projection, URI/reference-location ordering, organize-import helpers,
and fix-all edit deduplication, is also authored here so editor behavior can
move with the bootstrapped front end. This package is the adaptation boundary
for bootstrapped code.
