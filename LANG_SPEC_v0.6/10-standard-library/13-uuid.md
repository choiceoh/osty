### 10.13 UUID (`std.uuid`)

`Uuid` is a v0.6 sealed-construct type (§3.4.5, G40) — external
literal construction is rejected. Values are produced by
`uuid.v4(rng)` (random), `uuid.v7(clock, rng)` (time-ordered), or
`uuid.parse(text)?` (round-trip).

```osty
use std.uuid

// Construction — capability-injected (v4 = randomness only;
// v7 = randomness + monotonic timestamp).
fn mintRowId(clock: Clock, rng: CryptoRng) -> Uuid {
    uuid.v7(clock, rng)
}

#[ambient(clock, cryptoRng)]
fn main() {
    let id = uuid.v4(cryptoRng)
    let sortable = uuid.v7(clock, cryptoRng)

    // Round-trip through canonical text — pure.
    let text: String = id.toString()
    let parsed: Uuid = uuid.parse(text)?
}
```

API:

```
// `CryptoRng` capability is required for v4/v7 — UUID generation is
// security-relevant in many call sites (session ids, message ids).
uuid.v4(rng: CryptoRng) -> Uuid
uuid.v7(clock: Clock, rng: CryptoRng) -> Uuid   // preferred for database keys

// Pure — no capability.
uuid.parse(text: String) -> Result<Uuid, Error>
uuid.nil() -> Uuid                              // the all-zero UUID — pure constant

Uuid.toString(self) -> String
Uuid.toBytes(self) -> Bytes                     // 16 bytes
```

`Uuid` implements `Equal`, `Hashable`, `Ordered`. `toString()`
emits the canonical lowercase 8-4-4-4-12 form; `parse(...)` is the
exact reverse and is the **only** path to a `Uuid` from text outside
the constructor pair above (see §17.1 for the round-trip contract).

Legacy `uuid.v4()` / `uuid.v7()` (no capability args) desugar to
`std.uuid.host.v4()` / `std.uuid.host.v7()` under `--legacy-globals`
(v0.6.x only). Outside that mode the bare-arg form is `E0780` and
external `Uuid { ... }` literal is `E0420` (sealed construct
violation).

#### 10.13.1 UUID v4 vs v7

`uuid.v4(rng)` produces a fully random UUID — 122 bits of
randomness. Use for pure entropy: API keys, session IDs that
should not leak ordering information.

`uuid.v7(clock, rng)` produces a time-ordered UUID — first 48 bits
are a millisecond timestamp; remaining bits are random. Use for
database row IDs, log entries, sequence-correlated identifiers
where chronological ordering matters.

The v0.6 stdlib registers v4 with `CryptoRng` (security-relevant
randomness) and v7 with both `Clock` and `CryptoRng`. Tests use
`FakeClock` + `FakeCryptoRng` for deterministic UUID generation
across runs.

#### 10.13.2 UUID and information flow

A `Uuid` value does not carry source data — it is a synthesized
identifier. Therefore `Uuid.toString()` returns an *untagged*
`String` regardless of how the UUID was constructed. This is one
of the few cases where a value derived from a tagged input
produces an untagged output: the synthesis is not a function of
the input data, so flow tracking does not propagate.

If a UUID is *parsed from* a tainted source string, however, the
parsed `Uuid` carries the source's tag set — because `Uuid.parse`
is a structural transformation of the input.

#### 10.13.3 UUID v7 sortability

`uuid.v7(clock, rng)` produces UUIDs that sort correctly by
*timestamp first*, then random. This makes them useful as
database row IDs:

```osty
let mut ids: List<Uuid> = [...]      // collected over time
ids.sortBy(|a, b| a.cmp(b))          // chronological order
```

The sort is *byte-wise* over the UUID's 16-byte representation —
v7's first 6 bytes are a millisecond timestamp big-endian, so
byte comparison matches timestamp comparison. The remaining 10
bytes are random; identical-millisecond UUIDs sort by their
random suffix, which is acceptable for tiebreaking.

#### 10.13.4 v4 vs v7 selection guide

| Use case | Recommended | Why |
|---|---|---|
| Database row ID | v7 | Natural index ordering matches insertion |
| Session ID | v4 | No timing information leak |
| Cache key | v4 (or hash) | Collision concerns matter; ordering does not |
| Distributed log entry | v7 | Multi-node logs merge in time order |
| API token | v4 (or `CryptoRng.bytes`) | Unguessability is the priority |
| File name in user-facing UI | v4 (full random) | v7's prefix leaks creation time |

The "unguessability" criterion sits with `CryptoRng` — both v4
and v7 use `CryptoRng` under the v0.6 baseline, so both are
suitable for security-sensitive identifiers. v7 leaks the
*creation timestamp* but not the random suffix.
