# Self-rebuild ratchet (`verify-self-rebuild`)

- **Scope**: Operational runbook for the toolchain self-rebuild validation script
- **Type**: Operations / developer workflow
- **Covers**: `scripts/verify-self-rebuild`, `just verify-self-rebuild*`, stage0 bootstrap path used by stage1

## Purpose

The self-rebuild ratchet proves that the Osty compiler can **rebuild itself from
source** and that the result is **stable** (byte-identical across one more
rebuild). It is the end-to-end gate for:

- Stage0 bootstrap emit coverage (`OSTY_STAGE0_FALLBACK=1` on stage1 only)
- Source-compiler stages (`osty-self-2` / `osty-self-3` must not delegate back
  to the host Go-built `osty`)
- Toolchain MIR → LIR Proto → LLVM link for `toolchain/`
- Reproducibility of the `osty-self` artifact

Fresh clones use `just bootstrap` (registry fetch or stage0 `install-self`) to
populate `.osty/cache/self-host/`. The ratchet is the heavier check you run when
changing `toolchain/*.osty`, backend emit, or bootstrap wiring.

## Pipeline

```
host osty (.bin/osty, Go-built)
  └─ gates (optional): osty check toolchain/, snapshot parity, stage0 audit, route probes
  └─ stage1: host builds toolchain/  →  osty-self-1
       env: OSTY_SELF_REGISTRY_OFFLINE=1 OSTY_STAGE0_FALLBACK=1
  └─ stage2-seed: osty-self-1 builds toolchain/  (host compiler forbidden)
  └─ stage2:     osty-self-2-seed builds toolchain/
  └─ stage3:     osty-self-2 builds toolchain/
  └─ assert byte_eq(osty-self-2, osty-self-3)
       Mach-O UUID / code-signature metadata normalized when raw bytes differ
```

**Stage1** is the only step that may use the Go host compiler and stage0
fallback. Stages 2 and 3 install a **host guard** (`exit 86`) so
`OSTY_SELF_REBUILD_HOST_BIN` cannot forward to `.bin/osty` — each stage must be
produced entirely by the previous `osty-self` binary. External stage overrides
(`OSTY_SELF_REBUILD_STAGE{1,2,3}_BIN`) are rejected.

After each stage build, the script runs a **smoke probe**:

- Prefer `osty-self --selfhost-doctor` (expects host compiler disabled, source
  compiler enabled, self-rebuild probe OK)
- If doctor declines in stage0 partial mode, fall back to
  `lir-proto-lower` on a tiny probe file and check for `define` + `ret`

Contract tests in `internal/selfhost/phase0_wiring_test.go` lock this
behavior (`TestVerifySelfRebuildRequiresSourceCompilerStages`, etc.).

## `just` recipes

| Recipe | What it runs |
|---|---|
| `just verify-self-rebuild` | Full gates + ratchet with `--reuse-stage1` |
| `just verify-self-rebuild-fast` | Ratchet only (`--skip-gates --reuse-stage1`) |
| `just verify-self-rebuild-gates` | Preflight gates only (`--gates-only`) |
| `just verify-self-rebuild-ir` | Emit stage1 LLVM IR to `.osty/self-rebuild/stage1.ll` |
| `just verify-self-rebuild-stage1` | Build/cache stage1 only |
| `just backend-loop` | `go test` backend packages + fast ratchet |
| `just backend-loop-gates` | Backend tests + gates + fast ratchet |

All ratchet recipes except `verify-self-rebuild-ir` / `verify-self-rebuild-stage1`
depend on `just build-all` (CLI + native checker + native lirproto).

## Script flags

```sh
scripts/verify-self-rebuild [--gates-only|--ir-only|--stage1-only]
                            [--skip-gates] [--reuse-stage1] [--no-selfhostcache]
                            [HOST_OSTY_BIN]
```

| Flag | Stops after |
|---|---|
| `--gates-only` | Preflight gates (no stage builds) |
| `--ir-only` | Stage1 LLVM IR copy to `$OSTY_SELF_REBUILD_DIR/stage1.ll` |
| `--stage1-only` | Stage1 binary at `$OSTY_SELF_REBUILD_DIR/osty-self-1` |
| `--skip-gates` | Jumps straight to stage builds |
| `--reuse-stage1` | Reuse selfhostcache or mtime cache (see below) |
| `--no-selfhostcache` | Legacy mtime cache only; skip `osty cache-self` integration |

Default host binary: `.bin/osty` (repo-relative).

## Environment overrides

| Var | Default | Purpose |
|---|---|---|
| `OSTY_SELF_REBUILD_DIR` | `.osty/self-rebuild` | Staging dir for stage binaries |
| `OSTY_SELF_REBUILD_TOOLCHAIN_DIR` | `toolchain` | Package dir to rebuild |
| `OSTY_SELF_REBUILD_STAGE1_CACHE` | `.osty/self-rebuild-cache/osty-self-1` | Mtime-based stage1 cache |

Stage1 build also sets (inside the script, not for manual export):

- `OSTY_SELF_REGISTRY_OFFLINE=1` — force offline bootstrap for stage1
- `OSTY_STAGE0_FALLBACK=1` — stage0 emitter when `osty-self` is missing
- `OSTY_STAGE0_LIST_ALL_DECLINES=1` — verbose decline listing during stage1

`OSTY_SELF_REBUILD_FORWARD_ARGS` and `OSTY_SELF_REBUILD_HOST_BIN` are internal
forwarding hooks used by the build subprocess; do not set them manually.

## Stage1 reuse and cache promotion

With `--reuse-stage1`, lookup order is:

1. **Content-addressed selfhostcache** — `osty cache-self --check` →
   `.osty/cache/self-host/<sha>-<triple>/osty-self` (same key as
   `install-self` and README bootstrap lookup §3)
2. **Mtime cache** — `.osty/self-rebuild-cache/osty-self-1` if newer than all
   `toolchain/*.osty` (excluding `toolchain/.osty/`)
3. **Fresh stage1 build** — then promote into both caches

`--no-selfhostcache` disables step 1 and promotion into the content-addressed
cache; mtime cache still works.

Successful stage1 builds also call `install_into_selfhostcache`, so a ratchet
run can warm the same cache `just bootstrap` uses on the next invocation.

## Preflight gates (`run_gates`)

When gates are enabled (default), the script runs:

1. `osty check --airepair=false toolchain/`
2. `go test ./internal/ci ./internal/runner -run 'SnapshotParity|CoreSnapshotParity'`
3. `OSTY_STAGE0_AUDIT=1 go test ./internal/backend -run TestStage0ToolchainAudit`
4. Native llvmgen / lirproto / toolchain / backend route probe tests (fixed list
   in `scripts/verify-self-rebuild`)

`just verify-selfhost` covers only step 2. The ratchet gates are strictly
broader.

## Troubleshooting

### Stage1 fails with emit / stage0 declines

- Run `OSTY_STAGE0_AUDIT=1 go test -run TestStage0ToolchainAudit -v ./internal/backend/`
  to see uncovered toolchain functions.
- For install-self decline counts vs audit %, see
  [`docs/stage0_p24_scope.md`](../stage0_p24_scope.md) (audit measures checker
  bundle; install-self monomorphizes more instances).

### Stage2+ fails with "host compiler forwarding is forbidden"

Expected — the ratchet detected a regression where a self-built `osty-self`
still routes MIR emit through the Go host. Inspect recent changes to
`internal/backend/llvm.go`, `toolchain/main.osty`, and
`toolchain/selfhost_driver.osty`.

### Byte parity failed (stage2 vs stage3)

- Compare normalized hashes printed on failure (Mach-O link metadata).
- If normalized hashes also differ, the compiler is non-deterministic or
  stage2/stage3 builds picked different candidates — check
  `$OSTY_SELF_REBUILD_DIR/stage*.candidates` for multiple binary outputs.

### "produced multiple binary candidates"

The build left more than one new executable under `.osty/`. Clean
`toolchain/.osty` and retry. External `OSTY_SELF_REBUILD_STAGE*_BIN` overrides
are intentionally disabled.

### Slow iteration

```sh
just verify-self-rebuild-fast          # skip gates, reuse stage1
just verify-self-rebuild-stage1        # refresh stage1 only
just verify-self-rebuild-gates         # cheap preflight before a full ratchet
```

`backend-loop` deletes `toolchain/.osty` before backend tests to avoid stale
artifacts interfering with candidate detection.

## Related docs

- Bootstrap paths and env vars: [`README.md`](../../README.md) (Bootstrap
  env-var reference)
- Stage0 design and retirement policy: [`docs/osty_self_bootstrap_design.md`](../osty_self_bootstrap_design.md)
- Artifact cache (A7): [`docs/osty_self_artifact_design.md`](../osty_self_artifact_design.md)
- LLVM self-host milestones: [`docs/llvm-selfhost-plan.md`](../llvm-selfhost-plan.md)
- Strict backend test gate: [`docs/backend-test-failures-audit-2026-05-26.md`](../backend-test-failures-audit-2026-05-26.md)
