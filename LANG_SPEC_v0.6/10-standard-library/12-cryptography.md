### 10.12 Cryptography (`std.crypto`)

Hashing, message authentication, and cryptographically secure random
bytes. Asymmetric cryptography (RSA, Ed25519) is out of scope.

> **v0.6 capability split**: hashes (`sha256`, `hmac.sha256`,
> `constantTimeEq`) are pure functions over `Bytes` — no capability
> required. CSPRNG output is an *effect* (it consults the OS
> entropy pool) and goes through a dedicated `CryptoRng` capability
> distinct from `Rng` (§20.9.2). Ordinary `Rng` is non-cryptographic;
> using it for tokens, keys, or IVs is a security bug, so the two
> capabilities are typed apart.

```osty
use std.crypto

// Pure — no capability.
let digest = crypto.sha256(bytes)                    // Bytes (32 bytes)
let hex = encoding.hex.encode(digest)
let mac = crypto.hmac.sha256(key, message)

// Effectful — receives `CryptoRng` capability.
fn newSessionToken(rng: CryptoRng) -> Bytes {
    rng.randomBytes(32)
}

#[ambient(cryptoRng)]
fn main() {
    let token = newSessionToken(cryptoRng)
}
```

API:

```
// Pure — capability-free.
crypto.sha256(data: Bytes) -> Bytes
crypto.sha512(data: Bytes) -> Bytes
crypto.sha1(data: Bytes) -> Bytes         // legacy compatibility only
crypto.md5(data: Bytes) -> Bytes          // legacy compatibility only

crypto.hmac.sha256(key: Bytes, message: Bytes) -> Bytes
crypto.hmac.sha512(key: Bytes, message: Bytes) -> Bytes

crypto.constantTimeEq(a: Bytes, b: Bytes) -> Bool

// `CryptoRng` capability methods (canonical v0.6 surface).
CryptoRng.randomBytes(self, n: Int) -> Bytes
CryptoRng.uuid4(self) -> Uuid             // shortcut for §10.13 v4
```

Legacy `crypto.randomBytes(n)` desugars to
`std.crypto.host.randomBytes(n)` under `--legacy-globals` (v0.6.x
only). Outside that mode it is `E0780`.

Use `CryptoRng.randomBytes` for tokens, keys, IVs, and any other
security-sensitive randomness. Use `Rng` (§10.14) for simulation,
games, and non-security uses — `Rng` is seedable and reproducible,
which is exactly the wrong property for cryptographic purposes.

#### 10.12.1 Hash determinism

The hash functions (`sha256`, `sha512`, `sha1`, `md5`, `hmac.*`)
are deterministic — same input always produces same output bytes.
This makes them safe inside `#[pure]`
contexts. Specifically:

```osty
#[pure]
fn cacheKey(payload: Bytes) -> Bytes {
    crypto.sha256(payload)
}
```

`cacheKey` produces byte-identical hashes on every supported
target. The hash output is ordinary `Bytes`; flow tags ride
through (a tainted input produces a tainted hash output).

#### 10.12.2 HMAC and constant-time comparison

`crypto.hmac.sha256(key, message)` computes the keyed message
authentication code. The function is *not* constant-time on the
key — the `key` length affects the inner padding step. This is
the standard HMAC contract; constant-time *comparison* of two
HMAC outputs uses `crypto.constantTimeEq`:

```osty
fn verifySignature(secret: Bytes, payload: Bytes, sig: Bytes) -> Bool {
    let computed = crypto.hmac.sha256(secret, payload)
    crypto.constantTimeEq(computed, sig)
}
```

The naive `computed == sig` is a timing attack — bytewise `==`
short-circuits on the first mismatch, leaking position
information that attackers can use to forge signatures one byte
at a time. `constantTimeEq` always inspects every byte regardless
of mismatches found.

#### 10.12.3 Hash and information flow

The hashing functions preserve flow tags — a tainted input
produces a tainted hash output. The hash is *not* a sanitizer:

- `crypto.sha256(taintedBytes)` returns tainted `Bytes`.
- The hash output may flow into a sink only after appropriate
  sanitization for that sink (e.g. `crypto.constantTimeEq` for
  HMAC verification doesn't require sanitization since `Bool` is
  primitive).

This conservative approach catches a class of bugs where authors
assume hashing "anonymizes" sensitive input — the hash output
still carries the source's trust set, even though it does not
carry the source bytes.

#### 10.12.4 Hash algorithm policy

The v0.6 stdlib provides:

- **`sha256`, `sha512`** — recommended for new code. Collision-
  resistant under cryptographic assumptions.
- **`sha1`** — legacy compatibility only. SHA-1 is deprecated for
  new use; collision attacks are practical. Available for
  protocols that still require it (Git, older HMAC).
- **`md5`** — legacy compatibility only. Cryptographically broken;
  collision attacks are trivial. Use only for non-security
  identifiers (cache keys where collision is acceptable).

`osty lint` flags new uses of `sha1` and `md5` for security-
sensitive contexts (`L0060`). The flag is a hint — author
discretion overrides for known-safe cases.
