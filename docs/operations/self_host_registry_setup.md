# `osty-self` registry — operator setup

> **Scope**: GitHub Actions repository variables, secrets, runner pins,
> and ongoing maintainer checks for the `Build osty-self` workflow.
> **Authority**: this file is the source of truth for what the
> upstream `choiceoh/osty` registry needs to be running. Fork users
> mirror these settings against their own repository.
> **Owner**: backend / toolchain / operator.

For the **first-time bootstrap** (when the rolling release is empty
and the workflow has nothing to fetch), follow
[`first-publish-playbook.md`](first-publish-playbook.md). This file
covers the steady-state configuration that the playbook expects to
already be in place.

## 1. Repository configuration

### 1.1 `vars.OSTY_SELF_REGISTRY_URL` (required for publish)

| Field | Value |
|---|---|
| Where | `https://github.com/<owner>/osty/settings/variables/actions` |
| Name | `OSTY_SELF_REGISTRY_URL` |
| Value | `https://github.com/<owner>/osty/releases/download/osty-self-snapshots` |

The `Build osty-self` workflow's `publish` job is `if: ${{ vars.OSTY_SELF_REGISTRY_URL != '' }}`,
so leaving this variable unset turns the workflow into an
**advisory-only build matrix** — runners still produce manifests,
sign them, and upload `actions/upload-artifact` bundles, but the
publish step is a no-op. Useful for testing the build chain before
flipping publishing on.

The same URL is hard-coded as `selfhostcache.DefaultRegistryURL` in
`internal/toolchain/selfhostcache/default_registry.go`, so fresh
clones consult the same registry without the user having to export
the env var. Keep these two values in sync: a maintainer who changes
the variable but forgets the constant produces a repo where the
workflow publishes to one URL and the resolver fetches from another.

### 1.2 `secrets.OSTY_SELF_SIGNING_KEY` (optional)

| Field | Value |
|---|---|
| Where | `https://github.com/<owner>/osty/settings/secrets/actions` |
| Name | `OSTY_SELF_SIGNING_KEY` |
| Value | hex-encoded ed25519 private key (96 hex chars) |

Generated via `osty sign-self genkey` (see
[`docs/security/signing-rotation.md`](../security/signing-rotation.md)).
When set, every workflow run produces detached `.json.sig` files
alongside the manifests; when unset, manifests are unsigned and the
workflow skips the signing step gracefully.

The corresponding **public** key is committed in
`internal/toolchain/selfhostcache/default_trusted_key.go` as
`DefaultTrustedKeyHex` and recorded in
[`docs/security/trusted-keys.md`](../security/trusted-keys.md).

### 1.3 Runner pins

The build matrix uses pinned hosted runner labels rather than the
floating `*-latest` aliases (with the exception of `ubuntu-latest`
and `windows-latest` which Microsoft updates conservatively). When a
GitHub-hosted image rotates, edit
`.github/workflows/build-osty-self.yml` directly and bump the
`matrix.runner` value. Keep the pin one image release behind the
floating tag while you check that LLVM/clang versions still produce
byte-identical output for unchanged toolchain SHAs.

Current pins (Q1):

| triple | runner | LLVM/clang notes |
|---|---|---|
| linux-amd64 | `ubuntu-latest` | apt clang/lld |
| linux-arm64 | `ubuntu-22.04-arm` | apt clang/lld |
| darwin-amd64 | `macos-15-intel` | Apple Xcode clang — Intel sunset trajectory |
| darwin-arm64 | `macos-14` | Apple Xcode clang |
| windows-amd64 | `windows-latest` | LLVM Windows distribution |

## 2. Reproducibility env

The workflow exports two variables before any build step:

```yaml
SOURCE_DATE_EPOCH: ${{ github.event.head_commit.timestamp != '' && github.event.head_commit.timestamp || '0' }}
OSTY_BUILD_FLAGS: "-ffile-prefix-map=${{ github.workspace }}=. -fdebug-prefix-map=${{ github.workspace }}=."
```

`SOURCE_DATE_EPOCH` is the standard Reproducible Builds env clang and
lld both honour — it clamps `__DATE__`/`__TIME__` macros, object
archive headers, and debug-info timestamps. The two prefix maps strip
the runner CWD from debug paths so workspaces on different runner VMs
produce the same bytes.

`workflow_dispatch` overrides default to the commit timestamp of the
ref being built. Schedule-driven runs use the same expression — the
schedule fires against the latest commit on `main`, which has a
timestamp.

### 2.1 Adding a new reproducibility flag

If you discover a new source of build non-determinism:

1. Add the env var to the matrix `env:` block in
   `.github/workflows/build-osty-self.yml`.
2. Verify locally that two consecutive `osty install-self --force`
   runs against the same toolchain SHA produce byte-identical
   binaries (`shasum -a 256` round-trip).
3. Commit + push. The next workflow run validates the change at scale.
4. Note the variable in this file's §2 list.

## 3. Per-publish lifecycle

A successful workflow run uploads four asset families per triple to
the rolling release:

| Asset | Mutability | Purpose |
|---|---|---|
| `<sha>-<triple>.json` | **Immutable** (first-publish-wins) | Manifest consumed by `selfhostcache.HTTPFetcher`. |
| `<sha>-<triple>.json.sig` | Immutable (matches manifest) | Detached ed25519 signature; emitted only when signing key is set. |
| `osty-self-<triple>` (or `.exe`) | Immutable | Binary referenced by manifest's `binaryURL`. |
| `osty-self-latest-<triple>.bin` | **Force-updated** | Bootstrap seed for the next workflow run. |

The publish job's same-key skip protects the first three. The seed is
deliberately rolling — the next round needs the latest osty-self,
not the historical one.

## 4. Monthly maintainer checklist

Run through these before the start of each month — about 10 minutes:

1. **Smoke test passed?**
   ```sh
   gh issue list --label bootstrap-smoke-failure --state open
   ```
   Empty output = the weekly fresh-clone smoke test
   (`bootstrap-smoke-test.yml`) has been green. Otherwise: walk
   `docs/security/bootstrap-recovery.md` §3 to recover.

2. **Rolling release sane?**
   ```sh
   gh release view osty-self-snapshots --json assets \
       --jq '.assets | length'
   ```
   You should see at minimum `(N_recent_SHAs * 5 triples) + 5 seeds + N sigs`.
   A drop suggests an asset deletion incident.

3. **Cache GC**: locally, `osty gc-self --keep 30 --older-than 90d --dry-run`
   to inspect. The repo-side rolling release does not yet have an
   automated GC — entries accumulate. Schedule a manual cleanup by
   tag (e.g., once per quarter) if storage starts to matter; for the
   foreseeable future, GitHub Releases storage is unlimited for
   public repos.

4. **Signing key rotation due?** `docs/security/signing-rotation.md`
   says no scheduled rotation, only incident-driven. Skip unless
   §3 of that document fired.

5. **Toolchain SHA churn rate**: how many distinct
   `<sha>-<triple>.json` are in the rolling release? Cross-check with
   `git log --oneline -- toolchain/` for the same period — they
   should roughly match. If publish ran more often than toolchain
   changes, look for `paths-filter` regressions.

## 5. Disabling publishing temporarily

To quiesce publishing without removing the workflow (for example,
during a multi-PR refactor that would publish many transient SHAs):

```sh
gh variable set OSTY_SELF_REGISTRY_URL --body ""
```

The next run becomes advisory-only. Restore by setting the variable
back to its canonical value (§1.1).

This is also the right lever for fork users running their own
private osty-self registry — set the variable to their own URL and
the workflow publishes there instead of upstream.
