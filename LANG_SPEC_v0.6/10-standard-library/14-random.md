### 10.14 Random (`std.random`)

Non-cryptographic pseudorandom numbers.

> **v0.6 migration**: the v0.5 globals `random.next()` / `random.nextBytes(n)` /
> `random.default()` move to `Rng` capability methods (§20.9.2). The
> production host adapter is `random.host`; for deterministic tests use
> `std.capability.testing.FakeRng(seed = N)`. The legacy globals stay
> available under `--legacy-globals` (v0.6.x only); v0.7 removes them.
> See §10.46 for the full migration table.

```osty
use std.random

let rng = random.default()                           // thread-local, auto-seeded
let n = rng.int(0, 100)
let f = rng.float()                                  // [0.0, 1.0)
let picked = rng.choice(items)?

let seeded = random.seeded(42)                       // reproducible
```

API:

```
random.default() -> Rng
random.seeded(seed: Int64) -> Rng

Rng.int(self, min: Int, max: Int) -> Int              // [min, max)
Rng.intInclusive(self, min: Int, max: Int) -> Int
Rng.float(self) -> Float                              // [0.0, 1.0)
Rng.bool(self) -> Bool
Rng.bytes(self, n: Int) -> Bytes
Rng.choice<T>(self, items: List<T>) -> T?
Rng.shuffle<T>(self, items: mut List<T>)
```
