### 10.14 Random (`std.random`)

Non-cryptographic pseudorandom numbers.

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
