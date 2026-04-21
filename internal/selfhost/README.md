# Selfhost Front End

`internal/selfhost` contains the committed bootstrap-generated Go bridge for the
Osty front end plus the adapters that let the rest of the Go codebase talk to
it.

Today the public entrypoints are:

- `internal/lexer` — thin Go facade over selfhost tokenization
- `internal/parser` — thin Go facade over selfhost parsing plus Go-side
  compatibility lowerings
- `internal/selfhost` — `FormatSource` / `FormatCheck` expose stable Go
  adapters over the bootstrap-generated pure-Osty formatter for parity tests
  and self-host drift detection without changing the CLI formatter contract
- `internal/check` — prefers the external native checker binary and uses the
  embedded selfhost bridge as the fallback / adaptation boundary

The exact merged Osty inputs live in
[`internal/selfhost/bundle/bundle.go`](./bundle/bundle.go):

- `ToolchainCheckerFiles()` feeds both the native checker binary and the
  `internal/selfhost/generated.go` regeneration path (via `gen_selfhost.go`)

Notable inputs currently include:

- `toolchain/{semver,semver_parse,frontend,lexer,parser,formatter_ast,check_bridge,diagnostic,check_diag,diag_manifest,diag_examples,ty,core,check_env,solve,elab,check,resolve,lint}.osty`
- `internal/selfhost/ast_lower.osty`

Regenerate the Go bridge with:

```sh
go generate ./internal/selfhost
```

That flow:

- regenerates `internal/selfhost/astbridge/generated.go`
- merges the current toolchain/selfhost source bundle
- invokes `cmd/osty-bootstrap-gen` to write `internal/selfhost/generated.go`
- normalizes host-specific source markers and reapplies the small Go-side
  compatibility/hot-path patches in `gen_selfhost.go`
- installs the regenerated bridge only if `go build ./internal/selfhost/...`
  still passes; rejected output is preserved as `generated.go.broken`

`internal/check` then maps the checker output back onto the resolver symbols and
AST nodes consumed by codegen, LSP, and the rest of the host-side pipeline.
