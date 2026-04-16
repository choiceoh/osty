# Toolchain Sources

`toolchain` no longer contains the generated Go front-end package.
That bridge was removed as part of the bootstrap cutover.

The Osty-authored source of truth for the front end and checker still lives in:

- `toolchain/semver.osty`
- `toolchain/semver_parse.osty`
- `toolchain/frontend.osty`
- `toolchain/lexer.osty`
- `toolchain/parser.osty`
- `toolchain/formatter_ast.osty`
- `toolchain/check_bridge.osty`
- `toolchain/check.osty`
- `toolchain/ast_lower.osty`

Mainstream Go packages should call `internal/lexer`, `internal/parser`, and
`internal/check`. Those entrypoints currently route through the committed
bootstrap-generated `internal/golegacy` snapshot plus `internal/golegacy/astbridge`.

There is no longer a wired `go generate ./toolchain` refresh path in
the repository. This directory now exists only for the remaining Osty source
artifacts that have not yet been folded into the final native toolchain pipeline.
