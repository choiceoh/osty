# Selfhost Front End

`internal/selfhost` contains the committed frozen Osty→Go seed for the Osty
front end plus the adapters that let the rest of the Go codebase talk to it.

Today the public entrypoints are:

- `internal/lexer` — thin Go facade over selfhost tokenization
- `internal/parser` — thin Go facade over selfhost parsing plus Go-side
  compatibility lowerings
- `internal/selfhost` — `FormatSource` / `FormatCheck` plus
  `ResolveSourceStructured` / `ResolvePackageStructured` expose stable Go
  adapters over the seed pure-Osty formatter and resolver for parity tests
  and self-host drift detection without changing the CLI formatter contract
- `internal/check` — prefers the external native checker binary and uses the
  embedded selfhost bridge as the fallback / adaptation boundary

The exact merged Osty inputs live in
[`internal/selfhost/bundle/bundle.go`](./bundle/bundle.go):

- `ToolchainCheckerFiles()` feeds the native checker binary

Notable inputs currently include:

- `toolchain/{semver,semver_parse,frontend,lexer,parser,formatter_ast,check_bridge,diagnostic,check_diag,diag_manifest,diag_examples,ty,core,check_env,solve,elab,check,resolve,lint}.osty`
- `toolchain/lsp.osty`
- `internal/selfhost/ast_lower.osty`

`internal/selfhost/generated.go` and `internal/selfhost/astbridge/generated.go`
are committed seed artifacts — the Osty→Go bootstrap transpiler that produced
them has been removed. Future updates to the toolchain must land through the
LLVM self-host path rather than a Go-transpile regen.

`internal/check` maps the checker output back onto the resolver symbols and
AST nodes consumed by codegen, LSP, and the rest of the host-side pipeline.
