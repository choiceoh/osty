### 10.20 Time Extensions (`std.time`)

Beyond basic instants and durations, `std.time` provides formatting,
parsing, and timezone handling.

> **v0.6 migration**: the v0.5 globals `time.now()` / `time.monotonic()` /
> `time.sleep(d)` move to `Clock` capability methods (§20.9.1). The
> production host adapter is `time.systemClock`; for deterministic
> tests use `std.capability.testing.FakeClock(epoch_ms = N)`. Legacy
> globals stay under `--legacy-globals` (v0.6.x only); v0.7 removes
> them. The non-effectful `Time` / `Duration` types and the formatting
> /parsing helpers below are unchanged. See §10.46 for the full
> migration table.

```osty
use std.time

// Effectful access flows through a `Clock` capability (§20.9.1).
fn snapshot(clock: Clock) -> Result<String, Error> {
    let now = clock.now()                          // Instant
    let iso = now.format(time.ISO_8601)            // "2024-01-15T10:30:00Z"
    let custom = now.format("yyyy-MM-dd HH:mm:ss")

    let parsed: Instant = time.parse(iso, time.ISO_8601)?

    let later = now.add(5.minutes)
    let diff: Duration = later - now

    clock.sleep(100.ms)?       // returns Err(Cancelled) if task cancelled

    let tz = time.zone("Asia/Seoul")?
    let local: ZonedTime = now.inZone(tz)
    Ok(custom)
}

#[ambient(clock)]
fn main() {
    let _ = snapshot(clock)?
}
```

The pure types (`Instant`, `Duration`, `Zone`, `ZonedTime`) and the
formatting / parsing helpers below are not `Clock`-bound — they operate
on already-captured values. Only `now` / `monotonic` / `sleep` are
capability-gated.

**Cancellation.** `time.sleep(d)` is a cancellation point per §8.4.2.
If the surrounding task's cancel signal fires, the sleep returns
`Err(Cancelled { cause })` immediately — no matter how much of the
duration is left. The signature is therefore
`time.sleep(d: Duration) -> Result<(), Error>`; use `time.sleep(d).ok()`
or `_ = time.sleep(d)` when cancellation is irrelevant.

API additions:

```
Instant.format(self, layout: String) -> String
Instant.inZone(self, zone: Zone) -> ZonedTime
time.parse(text: String, layout: String) -> Result<Instant, Error>

time.zone(name: String) -> Result<Zone, Error>     // IANA timezone
time.local() -> Zone
time.utc() -> Zone

time.ISO_8601
time.RFC_3339
time.RFC_2822
```

**Types.**

```osty
pub struct Zone {
    pub name: String,           // e.g. "Asia/Seoul", "UTC"
    pub offset: Duration,       // signed offset from UTC
    pub isFixed: Bool,          // true for fixed-offset zones (UTC, +09:00),
                                // false for IANA-rule zones (DST-aware)
}

pub struct ZonedTime {
    pub instant: Instant,
    pub zone: Zone,

    pub fn year(self) -> Int
    pub fn month(self) -> Int
    pub fn day(self) -> Int
    pub fn hour(self) -> Int
    pub fn minute(self) -> Int
    pub fn second(self) -> Int
    pub fn nanosecond(self) -> Int
    pub fn weekday(self) -> Weekday
    pub fn format(self, layout: String) -> String
}

pub enum Weekday { Mon, Tue, Wed, Thu, Fri, Sat, Sun }
```

**`Duration` is `ToString`** (resolves G2):

```osty
Duration.toString(self) -> String
// Adaptive output: "1.23s", "15ms", "120µs", "2m30s", "1h05m".
```

Whole-unit conversion helpers return truncated integer counts:

```osty
Duration.abs(self) -> Duration
Duration.micros(self) -> Int
Duration.millis(self) -> Int
Duration.seconds(self) -> Int
Instant.since(self, earlier: Instant) -> Duration
```

`Duration` is `Equal`, `Ordered`, `Hashable`. It supports `+`, `-`,
`*` (by `Int`), `/` (by `Int`).

**Duration literals.** The forms `5.s`, `100.ms`, `1.h`, `30.minutes`,
`7.days` are **not special syntax**. They are ordinary method calls on
integer literals. The compiler recognizes the following methods on the
`Int` type as Duration-producing constructors (defined in `std.time`):

| Method | Returns |
|---|---|
| `Int.ns(self)` | `Duration` (nanoseconds) |
| `Int.us(self)` | `Duration` (microseconds) |
| `Int.ms(self)` | `Duration` (milliseconds) |
| `Int.s(self)`  | `Duration` (seconds) |
| `Int.minutes(self)` | `Duration` (minutes) |
| `Int.h(self)`  | `Duration` (hours) |
| `Int.days(self)` | `Duration` (days, 24h) |
| `Int.weeks(self)` | `Duration` (weeks, 7d) |

The minutes constructor is named `minutes` rather than `min` to avoid
shadowing `Int.min(self, other) -> Self` from §10.5.

These are compile-time recognized so they do not require an explicit
`use std.time` to appear in source — but they desugar to method calls
that the type checker sees normally. The float forms (`1.5.s`) are
analogous methods on `Float`.

#### 10.20.1 Clock determinism and reproducibility

`Clock.now()` is non-deterministic — the system time advances
between calls. A `#[reproducible]` function therefore cannot
receive `Clock` as a parameter (`E0784`).

The pattern for "I need a timestamp inside reproducible code":

```osty
// Outer (non-reproducible) captures the clock value.
fn writeSnapshot(clock: Clock, fs: Fs, payload: Bytes) -> Result<(), Error> {
    let timestamp = clock.now().toEpochMillis()
    let key = computeKey(timestamp, payload)
    fs.write("snap/{key.toHex()}.bin", payload)
}

// Inner (reproducible) takes the captured timestamp.
#[reproducible(scope = "target")]
fn computeKey(timestamp: Int64, payload: Bytes) -> Bytes32 {
    sha256(payload + timestamp.toBytes())
}
```

`FakeClock(epoch_ms = N)` returns the configured `N` on every
`now()` call — a deterministic sequence for tests. `clock.sleep`
in production blocks; `FakeClock.sleep` returns immediately while
honoring the surrounding `taskGroup` cancel.

#### 10.20.2 Cancellation and timeouts

`clock.sleep(d)` is a cancellation point (§8.4.2). When the
surrounding task is cancelled, the sleep returns
`Err(Cancelled { ... })` regardless of how much time remains. This
is what enables `withTimeout` patterns (§8.4.5):

```osty
fn withTimeout<T>(clock: Clock, d: Duration,
                  body: fn(Group) -> Result<T, Error>) -> Result<T, Error> {
    taskGroup(|g| {
        g.spawn(|| {
            clock.sleep(d)?
            g.cancel(Cancelled.New("timeout"))
            Ok(())
        })
        body(g)
    })
}
```

The timer task sleeps; the body runs in parallel. When the timer
fires, it cancels the group; the body's blocking calls return
`Cancelled`. When the body completes first, the timer's
`clock.sleep(d)?` itself returns `Cancelled` (because the group is
cancelled by the body's success).

#### 10.20.3 Duration arithmetic

`Duration` supports `+`, `-`, `*` (by Int), `/` (by Int):

```osty
let total = 5.minutes + 30.seconds        // 5:30
let half = total / 2                       // 2:45
let triple = total * 3                     // 16:30
```

Duration overflow follows the integer overflow rules (§2.3) — a
`Duration` representing a value that exceeds `Int64` nanoseconds
aborts. Practical durations (sub-century) never approach the
limit.

`Instant - Instant` returns a `Duration` (signed); `Instant +
Duration` returns a new `Instant`. Adding a `Duration` to an
`Instant` that would overflow `Int64` epoch nanoseconds aborts.

#### 10.20.4 Timezone caveats

`time.zone(name)` looks up an IANA tz database entry. The lookup
is host-dependent — the runtime queries the system tz database.
The set of valid `name` values is therefore platform-specific in
edge cases (custom zones, unusual aliases).

For maximum portability, use UTC (`time.utc()`) for all internal
timestamps and apply zone conversion only at user-display
boundaries.

---
