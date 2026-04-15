# Selfhost Front End

`internal/selfhost` is the default lexer and parser implementation used by the
Go toolchain. The implementation is authored in Osty and compiled into Go.

The source of truth is:

- `examples/dogfood/semver.osty`
- `examples/dogfood/semver_parse.osty`
- `examples/dogfood/frontend.osty`
- `examples/dogfood/parser.osty`

Regenerate the Go bridge with:

```sh
go generate ./internal/selfhost
```

The generator merges the dogfood sources, emits `generated.go` through
`cmd/osty gen`, and reapplies the small Go hot-path overrides that keep lexing
position lookups linear. Public compiler packages should call
`internal/lexer` and `internal/parser`; this package is the adaptation boundary
for bootstrapped code.
