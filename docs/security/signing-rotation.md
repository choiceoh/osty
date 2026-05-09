# Signing key — generation, rotation, incident response

> **Scope**: ed25519 keypair lifecycle for `osty-self` manifest signing.
> **Authority**: this file is the canonical procedure; `trusted-keys.md`
> records the resulting public-key fingerprints.
> **Owner**: backend / toolchain / security.

## 1. Custody (Q4-a)

The active private key lives in **two and only two** places:

1. **GitHub repository secret `OSTY_SELF_SIGNING_KEY`** — consumed by
   the `Build osty-self` workflow's signing step. Read-only access for
   the workflow context.
2. **Maintainer's password manager** (1Password personal vault) —
   recovery copy. Plain-text private-key backup, named
   `osty-self-signing-<rotation-date>`.

Bus factor is intentionally 1: this is OSS solo-maintainer scale.
Mitigation lives in §3 (incident response) and
[`bootstrap-recovery.md`](bootstrap-recovery.md) — disaster recovery
does **not** depend on the signing key.

## 2. Initial generation (Q4-b: incident-driven only)

There is no scheduled rotation. The key generated in this step lives
until §3 fires. The choice trades the operational simplicity of a
fixed key for a slightly larger blast radius if the key leaks — the
same pattern Homebrew, Cargo, and similar OSS distribution channels use.

```sh
# 1. Generate the keypair on the maintainer's machine.
#    Output is two strings: private (96 hex chars) + public (64 hex chars).
osty sign-self genkey --out /tmp/osty-self-signing.key

# 2. Upload the private half to GitHub Secrets.
gh secret set OSTY_SELF_SIGNING_KEY < /tmp/osty-self-signing.key
# Settings → Secrets and variables → Actions → New repository secret
# is the equivalent UI path.

# 3. Stash the private key in 1Password (recovery copy).
#    1Password → New Item → Password → name: osty-self-signing-<YYYY-MM-DD>

# 4. Update DefaultTrustedKeyHex with the *public* half.
#    File: internal/toolchain/selfhostcache/default_trusted_key.go
#    Replace `const DefaultTrustedKeyHex = ""` with the 64-char hex.

# 5. Append to trusted-keys.md §1 (Active keys) and commit.

# 6. Wipe the temp file.
shred -u /tmp/osty-self-signing.key
```

After this commit the next workflow run signs all manifests, and fresh
clones automatically verify — no user action required.

## 3. Incident response — suspected compromise

Triggers:

- A suspected leak of the private key (e.g. accidental commit, secret
  store breach).
- A user reports a verification failure they cannot explain.
- Routine audit finds the GitHub Secret value (or the 1Password copy)
  was accessed by an unexpected actor.

### 3.1 Procedure

```sh
# 1. Generate a fresh keypair.
osty sign-self genkey --out /tmp/osty-self-signing.key

# 2. Replace the GitHub secret. The old value is overwritten in place.
gh secret set OSTY_SELF_SIGNING_KEY < /tmp/osty-self-signing.key

# 3. Replace the public half in DefaultTrustedKeyHex.
#    Mark the *old* key as REVOKED in trusted-keys.md §1, append the new
#    row as ACTIVE, include the activation date.

# 4. Commit + push. The first push triggers `Build osty-self` because
#    the workflow file is in the paths-filter; that run produces newly
#    signed manifests under the new key.

# 5. Purge artifacts signed by the compromised key.
#    These are .json.sig files under the rolling release; deleting them
#    forces resolvers (which now have the *new* DefaultTrustedKeyHex)
#    to refuse to verify those entries until they are re-signed.
gh release view osty-self-snapshots --json assets --jq '.assets[].name' \
    | grep '\.json\.sig$' \
    | xargs -I{} gh release delete-asset osty-self-snapshots {} -y

# 6. Re-publish via workflow_dispatch — one run is enough; the
#    matrix re-signs manifest assets without re-uploading the
#    immutable cache binaries (Q5-c first-publish-wins).

# 7. Post a note to CHANGELOG_v0.5.md or similar — short, factual, no
#    secrets.
```

### 3.2 What does NOT need to change

- The cache binary contents (`<sha>-<triple>.json` and binaries) are
  not invalidated — the threat is signature spoofing, not binary
  tampering. Same SHA → same trustable bytes.
- The bootstrap seeds (`osty-self-latest-<triple>.bin`) — these are
  always force-overwritten on the next workflow run anyway.
- `OSTY_SELF_REGISTRY_URL` repo variable — unaffected.

## 4. Why no scheduled rotation?

A periodic rotation cadence (e.g. every 6 months) would mean
`trusted-keys.md` accumulates a trusted-key set rather than a single
active key. That trust set must then be merged into
`DefaultTrustedKeyHex` — the constant becomes a comma-separated list
or moves to a parsed file, the resolver loop tries each, and operator
errors that turn off old entries silently downgrade to warn-only. The
incident-only model keeps a single active key visible and auditable.

If the project ever reaches a scale where signed timestamping or HSM
custody is warranted, that change comes with its own design doc and
supersedes this one.
