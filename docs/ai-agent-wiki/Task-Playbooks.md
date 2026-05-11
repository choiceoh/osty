# Task Playbooks

## If you are changing the parser

- read `OSTY_GRAMMAR_v0.5.md`
- confirm the surface is already spec-authorized
- inspect `toolchain/parser.osty` before assuming the change belongs in Go
- check recovery behavior in `ARCHITECTURE.md`
- verify with parser-focused tests and the spec corpus

## If you are changing the checker or language rules

- read the matching section in `LANG_SPEC_v0.5/`
- inspect `toolchain/check.osty`, `toolchain/elab.osty`, `toolchain/check_env.osty`, and `toolchain/check_gates.osty`
- remember the self-host checker path is the default CLI path
- add focused `Exxxx` coverage when diagnostics change

## If you are changing lints or formatter policy

- policy belongs in `toolchain/*.osty`
- keep host Go code as glue only
- verify with focused lint/format tests and then `just front`

## If you are changing LSP behavior

- pure editor policy belongs in `toolchain/lsp.osty`
- Go should stay at JSON-RPC and traversal/bridge boundaries
- use `just lsp <regex>` before wider runs

## If you are changing backend behavior

- inspect `internal/backend`, `internal/llvmgen`, `internal/nativellvmgen`, and runtime ABI docs first
- keep fallback policy strict: no silent production routing through stage0
- preserve structured unsupported diagnostics for uncovered shapes
- use focused backend/codegen tests before broader runs

## If you are changing manifests, package manager, or registry behavior

- inspect `internal/manifest`, `internal/lockfile`, `internal/pkgmgr`, `internal/registry`
- read the matching `toolchain/manifest_*`, `toolchain/pkgmgr*`, or `toolchain/registry.osty` files
- keep lockfile and manifest behavior deterministic

## If you are changing docs only

- prefer adding routing help rather than duplicating full source-of-truth prose
- link to canonical docs instead of restating them
- keep short “read this if touching X” guidance

## If you are asked for a new language feature

- first verify it is already in `LANG_SPEC_v0.5/` and `OSTY_GRAMMAR_v0.5.md`
- if not, stop and route through spec work instead of implementation-only changes
