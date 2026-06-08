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

### Self-rebuild ratchet (`scripts/verify-self-rebuild`)

End-to-end host-free parity check: host osty builds `osty-self-1`, then
stage2/stage3 rebuild `toolchain/` and compare byte-for-byte (Mach-O
UUID/signature normalized when needed).

| Recipe | What it runs |
|---|---|
| `just verify-self-rebuild` | Full ratchet with gates + `--reuse-stage1` |
| `just verify-self-rebuild-fast` | Skip gates, reuse stage1 |
| `just verify-self-rebuild-gates` | Host-side gates only (check, snapshot parity, stage0 audit, native route probes) |
| `just verify-self-rebuild-stage1` | Build `osty-self-1` only |

Contract enforced by `internal/selfhost/phase0_wiring_test.go`:

- Every stage after stage1 must be produced by the previous stage (no
  `OSTY_SELF_REBUILD_STAGE*_BIN` overrides).
- `--selfhost-doctor` must report `osty-self source compiler: enabled`
  (MIR-JSON-only backend shortcuts are forbidden).
- Gates include `OSTY_STAGE0_AUDIT=1` → `TestStage0ToolchainAudit`.

Stage1 uses `OSTY_STAGE0_FALLBACK=1` + `OSTY_SELF_REGISTRY_OFFLINE=1` so
the ratchet does not depend on the registry. See
[`docs/osty_self_artifact_design.md`](../osty_self_artifact_design.md)
for selfhostcache / `--reuse-stage1` behavior.

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
