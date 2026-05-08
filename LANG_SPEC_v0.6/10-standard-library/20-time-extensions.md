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

let now = time.now()                               // Instant
let iso = now.format(time.ISO_8601)                // "2024-01-15T10:30:00Z"
let custom = now.format("yyyy-MM-dd HH:mm:ss")

let parsed: Instant = time.parse(iso, time.ISO_8601)?

let later = now.add(5.minutes)
let diff: Duration = later - now

time.sleep(100.ms)            // returns Err(Cancelled) if task cancelled

let tz = time.zone("Asia/Seoul")?
let local: ZonedTime = now.inZone(tz)
```

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

---
