### 10.12 Cryptography (`std.crypto`)

Hashing, message authentication, and cryptographically secure random
bytes. Asymmetric cryptography (RSA, Ed25519) is out of scope.

```osty
use std.crypto

let digest = crypto.sha256(bytes)                    // Bytes (32 bytes)
let hex = encoding.hex.encode(digest)

let mac = crypto.hmac.sha256(key, message)

let secret = crypto.randomBytes(32)                  // CSPRNG
```

API:

```
crypto.sha256(data: Bytes) -> Bytes
crypto.sha512(data: Bytes) -> Bytes
crypto.sha1(data: Bytes) -> Bytes         // legacy compatibility only
crypto.md5(data: Bytes) -> Bytes          // legacy compatibility only

crypto.hmac.sha256(key: Bytes, message: Bytes) -> Bytes
crypto.hmac.sha512(key: Bytes, message: Bytes) -> Bytes

crypto.randomBytes(n: Int) -> Bytes
crypto.constantTimeEq(a: Bytes, b: Bytes) -> Bool
```

Use `crypto.randomBytes` for tokens, keys, and IVs. Use `std.random`
(§10.14) for simulation, games, and non-security use.
