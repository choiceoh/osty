# First-publish playbook — bootstrapping the rolling release

> **Scope**: maintainer manual operations to seed the
> `osty-self-snapshots` rolling release for the first time.
> **Prerequisite**: PRs 1–4 of the publish-infra series have been
> merged. After this playbook completes, the workflow self-perpetuates.
> **Owner**: maintainer.
> **Time**: ~1 hour wall-clock, mostly waiting on builds.

This is a one-time procedure. Re-running it is harmless — the
same-key first-publish-wins policy (see `osty_self_artifact_design.md`
§8.8) means existing cache entries stay untouched.

## 0. Pre-flight checklist

- [ ] PR 1 (workflow infra) merged on `main`.
- [ ] PR 2 (security docs + auto-verify) merged on `main`.
- [ ] PR 3 (smoke-test workflow) merged on `main` — will fail until this
      playbook completes; that is expected.
- [ ] PR 4 (governance docs) merged on `main`.
- [ ] You are on the maintainer machine (Apple Silicon Mac assumed
      below; substitute as needed if your dev machine differs).
- [ ] Docker Desktop is running and can target both `linux/amd64` and
      `linux/arm64` (verify with `docker buildx ls`).
- [ ] `gh` CLI is authenticated: `gh auth status`.

## 1. Generate the signing key (skip if already done)

```sh
osty sign-self genkey --out /tmp/osty-self-signing.key
```

This produces a hex-encoded ed25519 keypair. **Private** half goes to
GitHub Secrets, **public** half goes into the source tree.

```sh
# 1.1 Upload the private key.
gh secret set OSTY_SELF_SIGNING_KEY < /tmp/osty-self-signing.key

# 1.2 Stash a recovery copy in 1Password (the only off-GitHub copy).
#     1Password → New Item → Password → name: osty-self-signing-<YYYY-MM-DD>

# 1.3 Read the public hex (last 64 chars of the file).
public_hex=$(tail -c 65 /tmp/osty-self-signing.key | head -c 64)
echo "Public key: $public_hex"

# 1.4 Update the constant in source.
#     File: internal/toolchain/selfhostcache/default_trusted_key.go
#     Replace `const DefaultTrustedKeyHex = ""` with `const DefaultTrustedKeyHex = "<public_hex>"`.
#
#     File: docs/security/trusted-keys.md §1
#     Replace the placeholder row with:
#       | active | <first 16 chars> | <full 64 chars> | <today YYYY-MM-DD> | initial production key |

# 1.5 Commit + push.
git add internal/toolchain/selfhostcache/default_trusted_key.go docs/security/trusted-keys.md
git commit -m "chore(security): activate initial osty-self signing key"
git push

# 1.6 Wipe the temp file.
shred -u /tmp/osty-self-signing.key
```

Skip this section if you've already done it for a prior round —
keys live for the project lifetime by default.

## 2. Build the bootstrap seeds

You need one `osty-self-latest-<triple>.bin` per matrix triple. Build
them on the maintainer machine where possible, dispatch to hosted
runners where not.

### 2.1 darwin-arm64 — native on the maintainer Mac

```sh
cd ~/code/osty   # or wherever your dev clone lives
git checkout main
git pull
just build-all
.bin/osty install-self --force
key=$(.bin/osty cache-self --key)
triple="darwin-arm64"
bin=".osty/cache/self-host/${key}/osty-self"
mkdir -p /tmp/osty-self-seeds
cp "$bin" "/tmp/osty-self-seeds/osty-self-latest-${triple}.bin"
echo "darwin-arm64 seed staged at /tmp/osty-self-seeds/osty-self-latest-${triple}.bin"
```

### 2.2 linux-amd64 — Docker

Docker on Apple Silicon honors `--platform=linux/amd64` via QEMU.
Slower than native (~3× build time), but works for a one-shot seed.

```sh
docker run --rm --platform=linux/amd64 \
    -v "$PWD:/src" -w /src \
    -e CGO_ENABLED=0 \
    golang:1.26 \
    sh -c '
        apt-get update -qq && apt-get install -y -qq --no-install-recommends clang lld
        go build -o .bin/osty ./cmd/osty
        go build -o .osty/bin/osty-native-checker ./cmd/osty-native-checker
        go build -o .osty/bin/osty-native-lirproto ./cmd/osty-native-lirproto
        OSTY_SELF_BIN=/src/.osty/cache/self-host/seed/osty-self ./.bin/osty install-self --force || \
            OSTY_STAGE0_FALLBACK=1 ./.bin/osty install-self --force
    '

# Copy the seed out.
key=$(.bin/osty cache-self --key)   # same key — toolchain SHA is host-independent
triple="linux-amd64"
cp ".osty/cache/self-host/${key}/osty-self" \
    "/tmp/osty-self-seeds/osty-self-latest-${triple}.bin"
```

### 2.3 linux-arm64 — Docker (native on Apple Silicon)

```sh
docker run --rm --platform=linux/arm64 \
    -v "$PWD:/src" -w /src \
    -e CGO_ENABLED=0 \
    golang:1.26 \
    sh -c '
        apt-get update -qq && apt-get install -y -qq --no-install-recommends clang lld
        go build -o .bin/osty ./cmd/osty
        go build -o .osty/bin/osty-native-checker ./cmd/osty-native-checker
        go build -o .osty/bin/osty-native-lirproto ./cmd/osty-native-lirproto
        OSTY_SELF_BIN=/src/.osty/cache/self-host/seed/osty-self ./.bin/osty install-self --force || \
            OSTY_STAGE0_FALLBACK=1 ./.bin/osty install-self --force
    '

triple="linux-arm64"
cp ".osty/cache/self-host/${key}/osty-self" \
    "/tmp/osty-self-seeds/osty-self-latest-${triple}.bin"
```

### 2.4 darwin-amd64 — `macos-15-intel` hosted runner

There is no Docker shortcut for darwin builds. Trigger one round of
the build workflow with `skip_seed_fetch=true`, scoped to the Intel
runner via a temporary matrix override is overkill — the easier path
is to dispatch the existing matrix and ignore the four other triples'
artifacts.

```sh
gh workflow run build-osty-self.yml \
    --field osty_version=initial-bootstrap \
    --field skip_seed_fetch=true
```

`skip_seed_fetch=true` tells the workflow to skip the prior-seed
download step. With no seed available, the build falls through to
`OSTY_STAGE0_FALLBACK=1`. Stage0 **audit** covers ~100% of
`toolchain/*.osty`, but the workflow still may fail on **build-pass**
walls (link, cross-pkg symbols, LIR Proto subprocess) — see
[`docs/llvm-selfhost-plan.md`](../llvm-selfhost-plan.md). **A failed
dispatch is often expected** on a seedless first run; it still leaves an
`actions/upload-artifact` bundle of partial state for diagnosis.

For darwin-amd64 specifically you have two real options:

- **Option A — Find a darwin-amd64 collaborator**. Send them
  `docs/operations/first-publish-playbook.md` and ask them to follow
  §2.1 substituting `darwin-amd64`. Cleanest path if you have a
  collaborator with an Intel Mac.

- **Option B — Defer darwin-amd64**. Skip the seed for this triple
  and accept that darwin-amd64 fresh-clone users hit the
  `OSTY_STAGE0_FALLBACK=1` / `OSTY_SELF_BIN` paths until a future
  workflow run produces the seed organically. (The
  `bootstrap-smoke-test.yml` smoke runs on `ubuntu-latest`, so this
  does not break the smoke test gate.)

Both are reasonable. Option B is simpler and what the maintainer
should pick if no Intel-Mac collaborator is immediately available.
Add a `bootstrap-smoke-failure` issue stub manually to track the
darwin-amd64 gap so it doesn't get forgotten.

### 2.5 windows-amd64 — `windows-latest` hosted runner

Same problem as §2.4 — no Docker shortcut. Same options apply:

- **Option A** — collaborator with a Windows dev box runs §2.1
  substituting `windows-amd64`.
- **Option B** — defer; users on windows-amd64 fall through to
  `OSTY_SELF_BIN` until organic recovery.

Recommendation: pick A or B per triple based on collaborator access.
The maintainer can always come back later and run §2.4 / §2.5 for any
deferred triple.

## 3. Upload the seeds to the rolling release

```sh
# 3.1 Create the rolling release if it doesn't exist.
gh release view osty-self-snapshots >/dev/null 2>&1 || \
    gh release create osty-self-snapshots \
        --title "osty-self snapshots (rolling)" \
        --notes "Rolling cache of per-host osty-self artifacts. See docs/operations/self_host_registry_setup.md."

# 3.2 Upload every seed you successfully built.
cd /tmp/osty-self-seeds
for f in osty-self-latest-*.bin; do
    gh release upload osty-self-snapshots "$f" --clobber
    echo "uploaded seed: $f"
done
```

The `--clobber` flag is the convention for seed assets only — they
are designed to be force-updated each round.

## 4. Set the repo variable

```sh
# Owner here matches `git remote get-url origin`.
gh variable set OSTY_SELF_REGISTRY_URL \
    --body "https://github.com/choiceoh/osty/releases/download/osty-self-snapshots"
```

This flips the workflow's `publish` job from no-op to active.

## 5. Trigger the first real publish

```sh
gh workflow run build-osty-self.yml --field osty_version=v0.5-bootstrap-1
```

Watch the run. Each runner now finds its `osty-self-latest-<triple>.bin`
seed in §3, installs it as `OSTY_SELF_BIN`, and uses it to drive a
real `osty install-self` against the current toolchain. The runner
publishes `<sha>-<triple>.json` + binary to the rolling release, plus
a refreshed seed.

After the run completes:

```sh
# 5.1 Sanity-check the release contents.
gh release view osty-self-snapshots --json assets \
    --jq '.assets[].name' | sort

# Expected: roughly 5 manifests (.json), 5 binaries, 5 fresh seeds,
# and 5 signatures (.json.sig) if the signing key from §1 is in place.
```

## 6. Verify with the smoke test

```sh
gh workflow run bootstrap-smoke-test.yml
```

This runs the same path a fresh contributor walks. If it passes, the
playbook is complete — close out any tracking issue from §0 and
update the maintainer journal.

If it fails, walk `docs/security/bootstrap-recovery.md` §3 (DR1) —
publish was not the issue; something about the registry → resolver
→ build path is broken. The smoke test's run log will name the layer.

## 7. Quiesce period

For the first week post-bootstrap, monitor `bootstrap-smoke-failure`
issues daily. After a clean week, drop to weekly per the schedule in
`self_host_registry_setup.md` §4.
