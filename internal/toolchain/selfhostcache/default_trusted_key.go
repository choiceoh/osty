package selfhostcache

// DefaultTrustedKeyHex is the maintainer-published ed25519 public key
// (hex-encoded, 64 chars / 32 bytes) that fresh clones use to verify
// downloaded manifests when OSTY_SELF_TRUSTED_KEY is not set.
//
// Empty string (the default) preserves A6's warn-only behaviour:
// resolvers accept unsigned manifests and skip verification when
// neither env nor default is configured. Once a maintainer publishes
// a key (typically the first time signing is enabled), this constant
// is updated in a single commit and fresh clones automatically
// switch to verify-by-default.
//
// Maintainer rotation flow (see docs/security/signing-rotation.md):
//
//  1. `osty sign-self genkey` produces a (private, public) pair.
//  2. Private key → repository secret `OSTY_SELF_SIGNING_KEY`.
//  3. Public key (hex) → this constant; commit + push.
//  4. Document fingerprint in docs/security/trusted-keys.md.
//
// Override on the consumer side: set OSTY_SELF_TRUSTED_KEY (env wins
// over default). Disable verification entirely: blank both. The env
// var path also lets a fork user run a private registry without
// touching this constant.
const DefaultTrustedKeyHex = ""
