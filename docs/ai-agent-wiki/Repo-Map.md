# Repo Map

## Top-level orientation

- `cmd/osty` — main CLI
- `cmd/osty-native-checker` — external checker boundary
- `internal/` — Go host-side implementation and bridges
- `toolchain/` — Osty-authored compiler/tooling core
- `LANG_SPEC_v0.5/` — language authority
- `testdata/` — fixtures and spec corpus
- `examples/` — executable samples under compiler coverage
- `docs/` — design notes and operational docs

## Where to go by subsystem

### Front end

- lexer: `internal/lexer`, `toolchain/lexer.osty`, `toolchain/frontend.osty`
- parser: `internal/parser`, `toolchain/parser.osty`
- resolver: `internal/resolve`, `toolchain/resolve.osty`
- checker: `internal/check`, `toolchain/check.osty`, `toolchain/elab.osty`, `toolchain/check_env.osty`, `toolchain/check_diag.osty`, `toolchain/check_gates.osty`

### Formatting, lint, LSP, docs, CI

- formatter: `internal/format`, `toolchain/formatter_ast.osty`
- lint: `internal/lint`, `toolchain/lint.osty`
- LSP policy: `toolchain/lsp.osty`
- doc generation: `internal/docgen`, `toolchain/docgen.osty`
- CI policy: `internal/ci`, `internal/cihost`, `toolchain/ci.osty`

### Mid-end and backend

- IR: `internal/ir`, `toolchain/ir.osty`, `toolchain/hir*.osty`
- MIR: `internal/mir`, `toolchain/mir*.osty`
- backend dispatch/layout: `internal/backend`
- LLVM bridge/emission: `internal/llvmgen`, `internal/nativellvmgen`
- runtime ABI: `internal/backend/runtime/osty_runtime.c`

### Packages and manifests

- manifest: `internal/manifest`, `toolchain/manifest_*.osty`
- lockfile: `internal/lockfile`
- package manager: `internal/pkgmgr`, `toolchain/pkgmgr*.osty`
- registry: `internal/registry`, `toolchain/registry.osty`

## Important supporting documents

- backend work: `/LLVM_MIGRATION_PLAN.md`, `/LLVM_BACKEND_GAP_PLAN.md`, `/LLVM_BACKEND_CORPUS.md`, `/LLVM_ARTIFACT_LAYOUT.md`
- runtime/GC: `/RUNTIME_GC.md`
- shipped-vs-spec surface: `/CHANGELOG_v0.5.md`
- resolved language gaps: `/SPEC_GAPS.md`

## Heuristic for where changes belong

- parser/checker/lint/editor policy change: start in `toolchain/*.osty`
- CLI flag parsing or process/file/network behavior: start in Go
- backend ABI/runtime bridge: Go/C backend path
- spec ambiguity: document first, then implementation
