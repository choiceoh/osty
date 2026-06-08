# Bootstrap recovery — disaster scenarios for `osty-self`

> **Scope**: how to re-bootstrap the `osty-self` toolchain when the
> normal `OSTY_SELF_REGISTRY_URL` → cache → fetch flow is broken.
> **Authority**: this file is the canonical recovery playbook. Update
> before retiring fallbacks; never silently delete a recovery layer.
> **Owner**: backend / toolchain / security.

## 1. Layered fallbacks — what the resolver tries

The fresh-clone resolver chain (in
`internal/toolchain/selfhostcache/`) walks these layers in order, top
to bottom. If a layer below is reachable, the system recovers
without operator action:

| Layer | Source | Failure mode |
|---|---|---|
| **L1** Local cache | `.osty/cache/self-host/<sha>-<triple>/` | Empty on a fresh clone or a fresh runner. |
| **L2** `OSTY_SELF_BIN` env override | User-supplied path | Set explicitly; never fails by surprise. |
| **L3** In-tree dev build | `toolchain/.osty/out/{debug,release}/llvm/osty-self` | Present only in active dev worktrees. |
| **L4** Network fetch | `<OSTY_SELF_REGISTRY_URL>` (or `DefaultRegistryURL`) | Registry down, network blocked, key rotation in flight. |
| **L5** `OSTY_STAGE0_FALLBACK=1` | Go-side stage0 emergency emitter | `TestStage0ToolchainAudit` covers **~100%** of `toolchain/*.osty` functions (8240/8241 after PR #1858). Emits LLVM IR for audited shapes via `internal/backend/stage0/`. **Audit-pass ≠ build-pass** — monomorphized `install-self` / LIR Proto / link walls can still decline after audit passes. See [`docs/llvm-selfhost-plan.md`](../llvm-selfhost-plan.md) and [`SPEC_GAPS.md`](../../SPEC_GAPS.md). |
| **DR1** Manual hand-publish | This document, §3 below | Recovery procedure for a registry outage. |
| **DR2** Maintainer reconstruction | [`docs/llvm-selfhost-plan.md`](../llvm-selfhost-plan.md), `scripts/verify-self-rebuild` | Last resort when L4 and L5 both fail to produce a linkable `osty-self`. No longer a mechanical "rewrite 88% of toolchain" plan — stage0 audit is complete; remaining work is production-path link / cross-pkg / LIR Proto gaps. |

## 2. Diagnosis — which layer am I on?

```sh
osty install-self --dry-run 2>&1 | tail -20
```

The verbose output prints which layer fired. Common signals:

- `cache hit at .osty/cache/self-host/...` → L1, all good.
- `env override OSTY_SELF_BIN=...` → L2.
- `in-tree build .../debug/llvm/osty-self` → L3.
- `fetched from <URL>` → L4 OK.
- `stage0 fallback declined: ...` → L5 active but a **build-pass**
  shape is missing (monomorph specialization, link symbol, or LIR Proto
  wall — not necessarily an audit gap). Capture the decline message;
  cross-check [`docs/backend-test-failures-audit-2026-05-26.md`](../backend-test-failures-audit-2026-05-26.md).
  Invoke DR1 if the registry is also down and you need users online
  immediately.

## 3. DR1 — Registry outage manual hand-publish

Trigger when L4 fetch is failing for hours and the maintainer needs
fresh-clone users back online.

### 3.1 Maintainer has a working `osty-self` already

```sh
# 1. Promote the local osty-self into the cache layout that
#    `manifest-self` expects.
osty install-self --force

# 2. Read off the key the resolver computed.
key=$(osty cache-self --key)
triple=$(osty cache-self --triple)
bin=$(osty cache-self --check)   # cache path

# 3. Build the manifest.
osty manifest-self \
    --bin "$bin" \
    --binary-url "osty-self-${triple}" \
    --triple "$triple" \
    --osty-version "manual-recovery-$(date -u +%Y%m%dT%H%M%S)" \
    --out "/tmp/${triple}.json"

# 4. (Optional) sign with the private key from 1Password.
osty sign-self \
    --manifest "/tmp/${triple}.json" \
    --key /path/to/private-key

# 5. Upload to the rolling release. Cache entries are immutable — gh
#    refuses to overwrite, so this succeeds only if the asset doesn't
#    already exist. (Use the `--clobber` flag on the seed only.)
cp "$bin" "/tmp/osty-self-${triple}"
gh release upload osty-self-snapshots \
    "/tmp/${key}.json" \
    "/tmp/osty-self-${triple}"

# 6. Force-update the bootstrap seed for the next CI round.
gh release upload osty-self-snapshots \
    "/tmp/osty-self-${triple}" \
    --clobber  # the seed file is force-updated by convention
```

Repeat steps 1–6 from the corresponding triple's environment for each
host the maintainer has access to (Docker for linux-{amd64, arm64};
native macOS for darwin-arm64; native macOS Intel or `macos-15-intel`
hosted runner for darwin-amd64; Windows VM/dev box for windows-amd64).

### 3.2 Maintainer machine is fresh too

Use the documented first-publish playbook in
`docs/operations/first-publish-playbook.md` — the procedure is
identical to the very first round.

## 4. DR2 — Reconstruction from scratch

Trigger when:

- The rolling release is wiped or otherwise unreachable for an extended
  period AND
- No maintainer-side `osty-self` binary exists AND
- `OSTY_STAGE0_FALLBACK=1 install-self` (or
  `scripts/verify-self-rebuild`) still cannot produce a linkable
  `osty-self` after exhausting decline diagnostics.

This is the worst case. Historical DR2 notes in
`docs/osty_self_b2_1_audit.md` (11.3% stage0 coverage, mechanical
`match → if-else` rewrites) are **obsolete** — stage0 audit reached
100% in May 2026 (PR #1858). Today's reconstruction path is:

1. **Diagnose the build-pass wall** — run
   `OSTY_STAGE0_LIST_ALL_DECLINES=1 OSTY_STAGE0_FALLBACK=1 .bin/osty install-self`
   and/or `just verify-self-rebuild-gates` (includes
   `OSTY_STAGE0_AUDIT=1 go test -run TestStage0ToolchainAudit`).
   Decline text names the missing MIR/LIR/link shape.
2. **Close the specific gap** — follow the active trajectory in
   [`docs/llvm-selfhost-plan.md`](../llvm-selfhost-plan.md) (cross-pkg
   link, native checker LLVM build, LIR Proto subprocess). Audit
   histograms alone are no longer the bottleneck.
3. **Ratchet** — `just verify-self-rebuild` enforces host-free
   stage2/stage3 byte parity and requires the **source compiler**
   pipeline (`osty-self source compiler: enabled` from
   `--selfhost-doctor`); MIR-JSON-only backend shortcuts are rejected
   by `internal/selfhost/phase0_wiring_test.go`.

From a working `osty-self` on any host, DR1 applies normally.

DR2 is a last-resort engineering plan, not an operator recipe. Most
outages recover via DR1.

## 5. Why these layers exist

This file deliberately lists every fallback so a future contributor
considering "let's simplify by removing X" sees the recovery cost.
The layers are cheap to keep — they only fire when the prior layer
fails — and expensive to re-create after deletion.

In particular:

- **L5** (`stage0`) is the only layer that requires no network and no
  prior binary. Audit coverage is ~100%, but production bootstrap still
  depends on link/LIR Proto paths that can decline after audit passes.
  Stage0 remains invaluable for diagnosis: decline messages name the
  exact MIR shape or emit stub that blocked progress. Do not retire
  stage0 while `OSTY_STAGE0_FALLBACK=1` is the fresh-clone escape hatch
  documented in `README.md` and `just bootstrap`.

- **DR2** is the only path that does not depend on any maintainer
  asset — neither the GitHub Release, nor the maintainer's machine,
  nor the 1Password vault. It is slow, but it cannot be wiped out by
  any single party.
