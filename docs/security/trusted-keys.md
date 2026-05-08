# Trusted keys — `osty-self` artifact verification

> **Scope**: ed25519 public keys that fresh clones use to verify `osty-self`
> manifests fetched from the rolling `osty-self-snapshots` release.
> **Authority**: this file is the source of truth for active key fingerprints.
> **Owner**: backend / toolchain / security.

## 1. Active keys

| Status | Key ID (first 16 hex) | Full hex (64 chars) | Activated | Notes |
|---|---|---|---|---|
| _none_ | _no signing key configured yet_ | — | — | warn-only mode (A6 default) |

The first row will be replaced once a maintainer runs the bootstrap
key generation flow (see [`signing-rotation.md`](signing-rotation.md)).

## 2. Verification — what fresh clones do

`selfhostcache.TrustedKey()` (in `internal/toolchain/selfhostcache/signing.go`)
consults two sources in this order:

1. **`OSTY_SELF_TRUSTED_KEY` env var** — explicit override. Set this when
   running against a private fork or staging registry.
2. **`DefaultTrustedKeyHex` constant** in
   `internal/toolchain/selfhostcache/default_trusted_key.go` — the
   maintainer-published key. Updated in the same commit that activates
   a new key (see §4 below).

If both are empty, the resolver runs in warn-only mode — unsigned
manifests are accepted. This is the bootstrap default until the first
key is published.

## 3. Manual verification — operator workflow

For the rare case where you want to verify a downloaded artifact by hand
(e.g. air-gapped review, or after a key-rotation incident):

```sh
# 1. Pull the manifest + signature side-by-side from the rolling release.
gh release download osty-self-snapshots \
    --pattern '<sha>-<triple>.json' \
    --pattern '<sha>-<triple>.json.sig'

# 2. Verify with the active public key from the table above.
osty sign-self verify \
    --manifest <sha>-<triple>.json \
    --signature <sha>-<triple>.json.sig \
    --pubkey-hex <FULL_HEX_FROM_TABLE>
```

A non-zero exit code means the signature does not match — do **not**
trust the binary; report on the issue tracker (see §5).

## 4. Activating a new key — maintainer

Detailed procedure: [`signing-rotation.md`](signing-rotation.md). High level:

1. Generate the keypair: `osty sign-self genkey --out /tmp/osty-self-signing.key`.
2. Upload the private half to GitHub repository secret
   `OSTY_SELF_SIGNING_KEY` (Settings → Secrets and variables → Actions).
3. Replace `DefaultTrustedKeyHex` in
   `internal/toolchain/selfhostcache/default_trusted_key.go` with the
   public half (hex, 64 chars).
4. Update §1 above: add a row with `Status: active`, key id, full hex,
   activation date.
5. Commit + push.
6. The next `Build osty-self` workflow run signs all manifests — fresh
   clones consume the signed artifacts automatically.

## 5. Reporting a suspected compromise

If a manifest verification fails or you find an artifact whose hash
doesn't match its manifest's `binarySHA256`:

- Open a `security` issue on the `choiceoh/osty` repository **without**
  posting the suspect bytes; link to the failing artifact URL instead.
- Mention the key id from §1 the artifact appeared to be signed under.
- The maintainer will follow [`signing-rotation.md`](signing-rotation.md) §3
  (incident response) to invalidate the suspected key and publish a new one.

## 6. Threat model

This signing layer protects against **registry compromise** —
specifically, an attacker who replaces a published artifact on the
GitHub Release without obtaining the maintainer's signing key. It does
**not** defend against:

- A maintainer-machine compromise (the private key + signing flow live
  there). For that, see [`bootstrap-recovery.md`](bootstrap-recovery.md).
- A compromised binary supply chain at `clang`/`lld`/`go` toolchain
  level on the build runner. Reproducibility env (`SOURCE_DATE_EPOCH`,
  `-ffile-prefix-map=`) reduces incidental drift but is not a defence
  against a hostile build host.
- Replay of an old (legitimately-signed) artifact: the cache key is
  `(toolchain SHA, triple)`; an old artifact for an old SHA is benign,
  while replay against a current SHA fails because the SHA in the
  manifest envelope must match the one being requested.
