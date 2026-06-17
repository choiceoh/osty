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

- include `just verify-selfhost` for snapshot parity (narrow — see below)
- include `just verify-self-rebuild` (or `just verify-self-rebuild-gates` only) for the full self-rebuild ratchet when touching toolchain emit / LIR Proto / `osty-self` wiring
- include `just ci`
- consider `just repair-check`
- for `osty build` / `osty install-self` wall-clock splits (front-end vs MIR/IR vs link), opt in with `OSTY_BUILD_PHASE_TIMING=1` (stderr `phase-timing:` lines; see `README.md` and `internal/backend/phase_timing.go`)

**`verify-selfhost` vs `verify-self-rebuild`**

| Recipe | Scope |
|---|---|
| `just verify-selfhost` | `SnapshotParity` / `CoreSnapshotParity` only (`internal/ci`, `internal/runner`) — fast policy drift guard |
| `just verify-self-rebuild-gates` | Above gates **plus** `osty check toolchain/`, stage0 audit, native LIR Proto route probes — no binary rebuild |
| `just verify-self-rebuild` | Full ratchet: gates (unless `--skip-gates`), stage1 build with stage0 fallback, stage2/stage3 byte parity, `--selfhost-doctor` smoke at each stage |

Details: [`docs/osty_self_bootstrap_design.md`](../osty_self_bootstrap_design.md) Appendix A, `scripts/verify-self-rebuild --help`.

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
