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
This makes them safe inside `#[reproducible(scope = "portable")]`
contexts. Specifically:

```osty
#[reproducible(scope = "portable")]
fn cacheKey(payload: Bytes) -> Bytes {
    crypto.sha256(payload)
}
```

`cacheKey` produces byte-identical hashes on every supported
target. The hash output is ordinary `Bytes`; flow tags ride
through (a tainted input produces a tainted hash output).
