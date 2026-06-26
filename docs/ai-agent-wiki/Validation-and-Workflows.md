# Validation and Workflows

## Preferred command loops

- fast front-end loop: `just front`
- fast broad loop: `just short`
- spec-only loop: `just spec`
- focused command tests: `just cmd <regex>`
- focused LSP tests: `just lsp <regex>`
- focused diag tests: `just diag <regex>`
- focused codegen tests: `just gen <regex>`
- broad push gate: `just prepush`

If `just` is unavailable, mirror the matching recipes from `/justfile`.

## Pre-edit baseline

- Run the narrowest existing tests that cover the area.
- Note unrelated failures before editing.
- Prefer focused package tests over whole-tree runs unless the change is broad.

## Area-specific expectations

### Front-end changes

- start with focused package tests or `just front`
- use the spec corpus when parser/checker behavior changes
- add focused diagnostic tests for new `Exxxx` codes

### CLI, toolchain, generated-output, or self-host path changes

- include `just verify-selfhost`
- include `just ci`
- consider `just repair-check`
- for `osty build` / `osty install-self` wall-clock splits (front-end vs MIR/IR vs link), opt in with `OSTY_BUILD_PHASE_TIMING=1` (stderr `phase-timing:` lines; see `README.md` and `internal/backend/phase_timing.go`)

### Backend changes

- keep unsupported shapes on structured diagnostic paths
- use focused backend/codegen tests first
- expand to broader verification when emission behavior changes
- MIR-direct tests call `requireRealLLVMEmission` — they need a cached
  `osty-self` (`just bootstrap` or `osty build toolchain/`). Without
  `osty-self`, they **skip** locally; set
  `OSTY_REQUIRE_REAL_LLVM_EMISSION=1` after bootstrap to match CI strict
  mode (`fresh-clone-source-bootstrap.yml`)
- stdlib bodied helpers are injected by default (`OSTY_STDLIB_BODY_LOWER`
  ON in `internal/backend/entry.go`). Bisect with `OSTY_STDLIB_BODY_LOWER=0`
  only when isolating injection-specific failures
- strict-mode failure baseline:
  [`docs/backend-test-failures-audit-2026-05-26.md`](../backend-test-failures-audit-2026-05-26.md)

### Self-rebuild ratchet (`verify-self-rebuild`)

`scripts/verify-self-rebuild` (also `just verify-self-rebuild`) is the
end-to-end self-host byte-parity gate. After PRs [#2022](https://github.com/choiceoh/osty/pull/2022) and the source-compiler ratchet completion, every stage must be built by the **previous** stage's `osty-self` binary — not by forwarding MIR-JSON or host-side compile drivers.

Flow (simplified):

1. Host `.bin/osty` runs gates + builds `osty-self-1` from `toolchain/`
2. `osty-self-1` → `osty-self-2-seed` → `osty-self-2` → `osty-self-3`, each rebuilding `toolchain/` through HIR → Mono → MIR → LIR Proto → LLVM IR
3. `osty-self-2` and `osty-self-3` are compared byte-for-byte (Mach-O UUID/signature normalized on Darwin)

Each stage's driver must report `osty-self source compiler: enabled` via
`--selfhost-doctor`, or fall back to a direct `lir-proto-lower` smoke probe.
Forbidden paths are enforced by `TestVerifySelfRebuildRequiresSourceCompilerStages`
in `internal/selfhost/phase0_wiring_test.go` (no `build_self_binary_with_host_mir_backend`, no MIR-JSON-only backend).

Recipes: `just verify-self-rebuild` (full), `just verify-self-rebuild-gates`
(gates only), `just verify-self-rebuild-fast` (skip gates, reuse stage1 cache).
Use `--reuse-stage1` for iteration — it consults the content-addressed
selfhostcache under `.osty/cache/self-host/`.

## Common repo recipes

- build CLI: `go build -o .bin/osty ./cmd/osty`
- build native checker: `go build -o .osty/bin/osty-native-checker ./cmd/osty-native-checker`
- verify self-host snapshots: `go test -count=1 -vet=off -run 'SnapshotParity|CoreSnapshotParity' ./internal/ci ./internal/runner`
- update diagnostic golden output: `go test ./internal/diag/ -run TestGolden -update`

## Validation strategy

1. reproduce or baseline
2. make the smallest meaningful change
3. run focused validation immediately
4. widen only as the change surface widens
5. use `just prepush` before shipping broad or risky changes
