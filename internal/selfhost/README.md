# Selfhost Front End

`internal/selfhost` contains the committed bootstrap-generated Go bridge for the
Osty front end plus the adapters that let the rest of the Go codebase talk to
it.

Today the public entrypoints are:

- `internal/lexer` — thin Go facade over selfhost tokenization
- `internal/parser` — thin Go facade over selfhost parsing plus Go-side
  compatibility lowerings
- `internal/check` — prefers the external native checker binary and uses the
  embedded selfhost bridge as the fallback / adaptation boundary

The exact merged Osty inputs live in
[`internal/selfhost/bundle/bundle.go`](./bundle/bundle.go):

- `GeneratedFiles()` feeds `internal/selfhost/generated.go`
- `ToolchainCheckerFiles()` feeds the broader checker/bootstrap regeneration path

Notable inputs currently include:

- `examples/selfhost-core/{semver,semver_parse,frontend,formatter_ast,check_bridge,check,resolve,lint}.osty`
- `toolchain/{frontend,lexer,parser,check_bridge,diagnostic,check_diag,ty,core,check_env,solve,elab,check}.osty`
- `internal/selfhost/ast_lower.osty`

Regenerate the Go bridge with:

```sh
go generate ./internal/selfhost
```

That flow:

- regenerates `internal/selfhost/astbridge/generated.go`
- merges the current toolchain/selfhost source bundle
- builds a temporary `cmd/osty-native-checker`
- invokes `cmd/osty-bootstrap-gen` to write `internal/selfhost/generated.go`
- reapplies the small Go hot-path patches in `gen_selfhost.go`

`internal/check` then maps the checker output back onto the resolver symbols and
AST nodes consumed by codegen, LSP, and the rest of the host-side pipeline.
