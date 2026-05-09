## §10.46 Capability Migration Catalog

This chapter is the **authoritative mapping** of v0.5 global effect
functions to their v0.6 capability equivalents. Use it when migrating
existing code, when implementing the `--legacy-globals` desugar, when
auditing pre-v0.6 sources, or when extending stdlib with capability
methods. Each row pins exactly one v0.5 surface and exactly one v0.6
replacement; no new mappings are added without a corresponding entry
here.

### §10.46.1 Migration timeline

| Phase | What works |
|---|---|
| v0.6.0 | Both surfaces: v0.5 globals via `--legacy-globals` (with `W0750` deprecation warning), v0.6 capability methods. |
| v0.6.x | Same. Migration is incremental — leaf functions first, callers last. |
| v0.7.0 | Only the v0.6 capability methods. The `--legacy-globals` flag is removed; v0.5 source compiled without prior migration fails. |

Packages that activate `[legacy] globals = true` in `osty.toml` are
forced to `[stability] default = "experimental"` automatically — a
package depending on legacy globals cannot promise stable APIs (§3.14).

### §10.46.2 Function-by-function mapping

#### `std.time` — `Clock` capability

| v0.5 global | v0.6 capability method | Notes |
|---|---|---|
| `time.now()` | `clock.now()` | Returns `Time` (UTC, monotonic). |
| `time.monotonic()` | `clock.monotonic()` | `Duration` since process start. |
| `time.sleep(d)` | `clock.sleep(d)` | Cancellation-aware. |
| `time.systemClock` | (no change) | Host adapter factory; returns a `Clock` instance. |

#### `std.random` — `Rng` capability

| v0.5 global | v0.6 capability method | Notes |
|---|---|---|
| `random.next()` | `rng.next()` | Pseudo-random `Int`. |
| `random.nextBytes(n)` | `rng.nextBytes(n)` | `Bytes` of length `n`. |
| `random.default()` | `random.host` | The default `Rng` host adapter. |

#### `std.env` — `Env` capability

| v0.5 global | v0.6 capability method | Notes |
|---|---|---|
| `env.get(k)` | `env.get(k)` | Same name on both sides — capability method on `Env` instance. |
| `env.set(k, v)` | `env.set(k, v)` | Same. |
| `env.args()` | `env.args()` | Same. |
| `env.vars()` | `env.vars()` | Same. |

#### `std.fs` — `Fs` capability

| v0.5 global | v0.6 capability method | Notes |
|---|---|---|
| `fs.readToString(p)` | `fs.readToString(p)` | Same. |
| `fs.write(p, c)` | `fs.write(p, c)` | Same. |
| `fs.exists(p)` | `fs.exists(p)` | Same. |
| `fs.create(p)` | `fs.create(p)` | Same. |
| `fs.remove(p)` | `fs.remove(p)` | Same. |
| `fs.mkdir(p)` | `fs.mkdir(p)` | Same. |
| `fs.host` | (factory) | Default `Fs` host adapter. |

#### `std.os` → `std.process` — `Process` capability

| v0.5 global | v0.6 capability method | Notes |
|---|---|---|
| `os.exec(c, a)` | `process.exec(c, a)` | **Module renamed** — `os` → `process`. |
| `os.exit(code)` | `process.exit(code)` | Same. |
| `os.hostname()` | `process.hostname()` | Same. |
| `os.pid()` | `process.pid()` | Same. |

#### `std.net` — `Net` capability

| v0.5 global | v0.6 capability method | Notes |
|---|---|---|
| `net.dial(h, p)` | `net.dial(h, p)` | Same name; capability method on `Net`. |
| `net.listen(p)` | `net.listen(p)` | Same. |
| `net.host` | (factory) | Default `Net` host adapter. |

#### `std.io` — `Console` capability

| v0.5 global | v0.6 capability method | Notes |
|---|---|---|
| `println(...)` | `console.println(...)` | `Console` instance per `#[ambient(console)]` or explicit parameter. |
| `print(...)` | `console.print(...)` | Same. |
| `eprintln(...)` | `console.eprintln(...)` | Stderr variant. |
| `eprint(...)` | `console.eprint(...)` | Stderr variant. |

The `println` family remains accessible as a v0.5 global under
`--legacy-globals`. New v0.6 code receives `Console` either via
`#[ambient(console)]` (entry-point only) or as an explicit parameter.

### §10.46.3 Deterministic capability variants for testing

`std.capability.testing` exposes deterministic fakes for each
non-deterministic capability. Use them in `#[test]` functions to
replace `--legacy-globals`-style implicit determinism:

| Production capability | Test fake | Determinism |
|---|---|---|
| `Clock` | `std.capability.testing.FakeClock(epoch_ms = N)` | Returns the configured epoch on every `now()`. |
| `Rng` | `std.capability.testing.FakeRng(seed = N)` | Sequence determined by `seed`. |
| `Env` | `std.capability.testing.FakeEnv(vars = {...})` | In-memory variable map. |
| `Fs` | `std.capability.testing.FakeFs(layout = {...})` | In-memory tree. |
| `Net` | `std.capability.testing.FakeNet(routes = {...})` | Pre-canned responses by host:port. |
| `Process` | `std.capability.testing.FakeProcess(...)` | Programmable exec stubs. |
| `Console` | `std.capability.testing.FakeConsole()` | Captures stdout/stderr. |

The fake set is also reachable through the convenience factory
`std.testing.capabilityFakes()` for tests that need every capability
fake at once.

### §10.46.4 Runtime adapter factories

The host adapters that produce real `Clock` / `Rng` / `Env` / `Fs`
instances live where the capability is defined:

| Capability | Factory |
|---|---|
| `Clock` | `time.systemClock` |
| `Rng` | `random.host` |
| `Env` | `env.host` |
| `Fs` | `fs.host` |
| `Net` | `capability.hostNet` |
| `Process` | `capability.hostProcess` |
| `Console` | `io.console` |

Cross-module bridge factories `net.host` / `process.host` exist as
**migration helpers** during the v0.6.x transition; they delegate to
the canonical factories in `std.capability` and will be marked
deprecated for v0.7 removal.

### §10.46.5 Migration pattern

Library code (`pub fn` outside `main` / `#[test]` / `#[bench]`):

```osty
// v0.5 (still compiles under --legacy-globals)
pub fn buildId() -> String {
    "{time.now().toEpochMillis()}-{random.next()}"
}

// v0.6 (recommended)
pub fn buildId(clock: Clock, rng: Rng) -> String {
    "{clock.now().toEpochMillis()}-{rng.next()}"
}
```

Entry point (`fn main` or scripts):

```osty
// v0.5 (still compiles under --legacy-globals)
fn main() {
    let id = buildId()
    println("{id}")
}

// v0.6 (recommended)
#[ambient(clock, rng, console)]
fn main() {
    let id = buildId(clock, rng)
    console.println("{id}")
}
```

Tests with deterministic fakes:

```osty
fn test_buildId_format() {
    let fakeClock = std.capability.testing.FakeClock(epoch_ms = 1_000_000)
    let fakeRng = std.capability.testing.FakeRng(seed = 42)
    let id = buildId(fakeClock, fakeRng)
    testing.assertEq(id, "1000000-1608637542")
}
```

### §10.46.6 Audit

`osty audit --legacy-globals` enumerates every site in the workspace
that calls a v0.5 global covered by this catalog. The audit output is
suitable for tracking migration progress in CI:

```sh
$ osty audit --legacy-globals --report=pretty
src/util.osty:42:18  time.now()       → clock.now()
src/util.osty:55:12  random.next()    → rng.next()
src/main.osty:8:5    println("ready") → console.println("ready") (auto via #[ambient])
3 deprecated sites; 0 with #[trusted_declassify]
```

When the report is empty, the package is fully migrated. Removing
`[legacy] globals = true` from `osty.toml` then unblocks `[stability]
default = "stable"`.

### §10.46.7 Migration order — recommended sequence

Migrating a non-trivial package is best done in the following order:

1. **Add capability parameters to leaf functions.** A leaf function
   is one that other code calls but that itself only calls stdlib —
   no internal callers depend on its signature change. Adding a
   `clock: Clock` parameter at the leaf is the smallest unit of
   change.

2. **Update the leaf's tests.** Replace `time.now()` usage with
   `FakeClock` injection. The tests are now deterministic; the
   transition mode (`--legacy-globals`) need not be active for
   leaf tests once they pass.

3. **Add capability parameters to one caller layer at a time.** Each
   caller of a migrated leaf takes the leaf's capability parameters
   and forwards them. Repeat until you reach `fn main`.

4. **Promote `fn main` to `#[ambient]`.** Replace the legacy
   global usages in `main` with `#[ambient(clock, rng, env, fs,
   net, console)]` (or a narrower set), and forward each
   capability into top-level callees explicitly.

5. **Remove `[legacy] globals = true` from `osty.toml`.** The package
   is now `--legacy-globals`-clean. `osty audit --legacy-globals`
   should report zero sites.

6. **Promote `[stability] default = "stable"`** if appropriate. The
   capability surface is now part of the public API contract.

A Phase 5 run of `osty fix --capability-migrate` (planned for v0.7)
will mechanize steps 1-4 by inserting capability parameters and
forwarding them, but the v0.6 baseline does the migration manually
— the leaf-first order keeps each commit small and reviewable.

### §10.46.8 Common pitfalls during migration

| Pitfall | Diagnostic | Fix |
|---|---|---|
| Library function annotated `#[ambient]` | `E0780` (entry-point only) | Remove `#[ambient]`; accept the capability as a parameter |
| Forwarding `clock` into a callee that takes `Clock` but with name `wallClock` | (no error — names don't have to match) | OK; the parameter name on the callee side is independent |
| Capability instance returned from a function | (no error) but breaks reproducibility | Returning `Net` is allowed (it's an interface value); but receivers can no longer reason about *who* created the instance — prefer to keep capabilities scoped to their construction context |
| Double `#[ambient]` (in `main` and a helper) | `E0780` on the helper | Keep `#[ambient]` only at the entry point; helpers receive parameters |
| `--legacy-globals` enabled but `[stability] default = "stable"` | `E2103` (incompatible mode) | Either migrate first, or downgrade stability to `experimental` |
| `time.now()` reaches a `#[pure]` function via `--legacy-globals` desugar | `E0785` | Migrate the leaf to a `Clock` parameter; pure functions cannot receive any capability |
