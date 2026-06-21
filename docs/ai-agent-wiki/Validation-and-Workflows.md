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
- for toolchain/backend self-host regressions, run `just verify-self-rebuild`
  (full gates + stage1→stage2→stage3 byte parity) or the faster
  `just verify-self-rebuild-fast` (skips gates, reuses cached stage1)
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

## Self-rebuild ratchet (`scripts/verify-self-rebuild`)

End-to-end check that the LLVM self-host chain is wired and reproducible.
Stage1 is built by the host `.bin/osty` (stage0 fallback when no
`osty-self` exists). Stages 2–3 must be built by the previous stage's
`osty-self` with the **source compiler** pipeline
(`source → HIR → Mono → MIR → LIR Proto → LLVM IR`), not a MIR-JSON-only
shortcut. Each stage runs `osty-self --selfhost-doctor` and must report
`osty-self source compiler: enabled`; stage2 and stage3 binaries are
compared byte-for-byte (Mach-O UUID/signature noise is normalized on
macOS).

| Recipe | What it runs |
|---|---|
| `just verify-self-rebuild` | Gates + full ratchet with `--reuse-stage1` |
| `just verify-self-rebuild-fast` | Ratchet only (`--skip-gates --reuse-stage1`) |
| `just verify-self-rebuild-gates` | Gates only (no stage builds) |
| `just verify-self-rebuild-stage1` | Build `osty-self-1` only |
| `just verify-self-rebuild-ir` | Emit stage1 LLVM IR to `.osty/self-rebuild/stage1.ll` |
| `just backend-loop` | Backend tests + fast ratchet |
| `just backend-loop-gates` | Backend tests + gates + fast ratchet |

Gates (when not skipped) run, in order: `osty check toolchain/`,
`SnapshotParity|CoreSnapshotParity`, `OSTY_STAGE0_AUDIT=1
TestStage0ToolchainAudit`, and native LLVM route probe tests under
`internal/llvmabi`, `cmd/osty-native-llvmgen`, `internal/nativelirproto`,
`internal/toolchain`, and `internal/backend`.

Stage overrides (`OSTY_SELF_REBUILD_STAGE1_BIN` and siblings) are
**rejected** — every stage must be produced by the previous stage so the
ratchet cannot be short-circuited with a hand-placed binary.

Useful env vars: `OSTY_SELF_REBUILD_DIR` (staging dir, default
`.osty/self-rebuild`), `OSTY_SELF_REBUILD_STAGE1_CACHE` (mtime cache for
stage1), and `OSTY_SELF_REBUILD_TOOLCHAIN_DIR` (default `toolchain/`).
`--reuse-stage1` prefers the content-addressed selfhostcache
(`osty cache-self --check`); `--no-selfhostcache` falls back to the
legacy mtime cache only.

## Common repo recipes

- build CLI: `go build -o .bin/osty ./cmd/osty`
- build native checker: `go build -o .osty/bin/osty-native-checker ./cmd/osty-native-checker`
- verify self-host snapshots: `go test -count=1 -vet=off -run 'SnapshotParity|CoreSnapshotParity' ./internal/ci ./internal/runner`
- stage0 toolchain audit: `OSTY_STAGE0_AUDIT=1 go test -count=1 -vet=off -run TestStage0ToolchainAudit -v ./internal/backend/`
- update diagnostic golden output: `go test ./internal/diag/ -run TestGolden -update`

## Validation strategy

1. reproduce or baseline
2. make the smallest meaningful change
3. run focused validation immediately
4. widen only as the change surface widens
5. use `just prepush` before shipping broad or risky changes
