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

// `Rng` itself is the v0.6 capability (§20.9.2). Library code receives
// it as a parameter; entry points bind it ambiently.
fn pickWinner(rng: Rng, candidates: List<User>) -> Result<User, Error> {
    let n = rng.int(0, 100)
    let f = rng.float()                              // [0.0, 1.0)
    let picked = rng.choice(candidates)?
    Ok(picked)
}

#[ambient(rng)]
fn main() {
    let _ = pickWinner(rng, loadCandidates())
}

// Reproducible derivation — useful in deterministic tests / benches.
let seeded: Rng = random.seeded(42)
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

#### 10.14.1 `Rng` vs `CryptoRng`

The v0.6 stdlib distinguishes two random capabilities:

| Capability | Source | Use cases | Reproducible? |
|---|---|---|---|
| `Rng` | xorshift64 (seedable) | simulation, games, sampling | yes (with `random.seeded(N)`) |
| `CryptoRng` | OS entropy (`/dev/urandom`, `BCryptGenRandom`) | tokens, keys, IVs, UUIDs | no |

Mixing them is a security bug — using `Rng` for a session token
gives an adversary who learns the seed full prediction power. The
v0.6 type system separates them into distinct interfaces so the
checker catches the mismatch.

#### 10.14.2 Seeded Rng for derivation

`random.seeded(seed)` returns an `Rng` whose sequence is
deterministic for the given seed. This is the canonical pattern
for *derivation* — generating a deterministic stream of random
values from a known starting point:

```osty
fn deriveScores(seed: Int64, n: Int) -> List<Int> {
    let rng = random.seeded(seed)
    let mut out: List<Int> = []
    for _ in 0..n {
        out.push(rng.int(0, 100))
    }
    out
}
```

`deriveScores(42, 10)` always returns the same list. This is
acceptable inside `#[reproducible(scope = "portable")]` — the
seeded `Rng`'s output is determined by its input.

The seeded `Rng` is *not* received as a capability parameter —
it's constructed locally from a deterministic seed, so the
function takes the seed as a plain `Int64` rather than the `Rng`
itself.
