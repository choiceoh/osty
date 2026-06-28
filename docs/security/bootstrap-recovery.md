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
| **L5** `OSTY_STAGE0_FALLBACK=1` | Go-side stage0 emergency emitter | Checker-bundle audit reached **100%** (PR #1858); full-binary `install-self` still hits LIR Proto / monomorph gaps outside that scope. `just bootstrap` bakes this in for offline fresh clones. |
| **DR1** Manual hand-publish | This document, §3 below | Recovery procedure for a registry outage. |
| **DR2** Toolchain rewrite | `docs/osty_self_b2_1_audit.md` v2 master plan | Last-resort reconstruction (~2–4 weeks). |

## 2. Diagnosis — which layer am I on?

```sh
osty install-self --dry-run 2>&1 | tail -20
```

The verbose output prints which layer fired. Common signals:

- `cache hit at .osty/cache/self-host/...` → L1, all good.
- `env override OSTY_SELF_BIN=...` → L2.
- `in-tree build .../debug/llvm/osty-self` → L3.
- `fetched from <URL>` → L4 OK.
- `stage0 fallback declined: ...` → L5 active but cannot cover the
  current toolchain. Time to invoke DR1 if the registry is also down.

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
- L5 (`OSTY_STAGE0_FALLBACK=1`) cannot cover the current toolchain.

This is the worst case. The procedure pulls together two work streams
already documented:

1. **Toolchain simplification** — `docs/osty_self_b2_1_audit.md`
   v2 master plan, batches B2.2–B2.3. ~244 source-level mechanical
   `match → if-else` rewrites. ~10–15 hours.
2. **Stage0 unlocks** — same document, batches B2.4–B2.7. ~4 stage0
   phases (P21–P24) covering the basic-shape gaps that block 88.7% of
   toolchain functions today. ~1500 lines of Go in
   `internal/backend/stage0/`. ~1–2 weeks dedicated work.

Combined, these two streams reach ~40% MIR coverage, which is enough
to bootstrap a build of the current toolchain into a fresh osty-self
binary on a single host. From that point, DR1 applies normally.

DR2 is a last-resort plan, not an operational recipe. Most outages
recover via DR1.

## 5. Why these layers exist

This file deliberately lists every fallback so a future contributor
considering "let's simplify by removing X" sees the recovery cost.
The layers are cheap to keep — they only fire when the prior layer
fails — and expensive to re-create after deletion.

In particular:

- **L5** (`stage0`) is the only layer that requires no network and no
  prior binary. Its 11.3% coverage is "almost useless" for production
  but invaluable for diagnosis: when the bootstrap is broken, stage0's
  decline message tells you exactly which MIR shape is missing
  (`docs/osty_self_b2_1_audit.md` §3 enumerates the dominant ones).
  Do not retire stage0 just because it cannot fully bootstrap; its
  diagnostic value alone justifies keeping the ~6K lines in
  `internal/backend/stage0/`.

- **DR2** is the only path that does not depend on any maintainer
  asset — neither the GitHub Release, nor the maintainer's machine,
  nor the 1Password vault. It is slow, but it cannot be wiped out by
  any single party.
