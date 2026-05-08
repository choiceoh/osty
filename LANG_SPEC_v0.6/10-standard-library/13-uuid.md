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
