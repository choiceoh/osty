## 11. Testing

Test files use the `_test.osty` suffix and live alongside code in the
same package. Functions whose names begin with lowercase `test` and
take no arguments are discovered and run by `osty test`.

```osty
// auth/login_test.osty
use std.testing

fn testLoginSuccess() {
    let result = login("alice", "valid_pass")
    testing.assert(result.isOk())
}

fn testLoginRejectsBlankUser() {
    let result = login("", "anything")
    testing.assertEq(result, Err(InvalidInput))
}
```

Test files are excluded from production builds.

### 11.1 Assertion API

```
testing.assert(cond: Bool)
testing.assertEq<T: Equal>(actual: T, expected: T)
testing.assertNe<T: Equal>(actual: T, expected: T)
testing.expectOk<T, E>(result: Result<T, E>) -> T
testing.expectError<T, E>(result: Result<T, E>) -> E
testing.fail(msg: String) -> Never
testing.context(msg: String, body: fn())
```

### 11.2 Detailed Failure Output

The compiler recognizes the above `testing` functions specifically and
generates detailed failure output including:

- Source location (file and line)
- Textual form of the argument expressions (captured at compile time)
- Runtime values (formatted structurally)
- A structural diff for composite values

**Structural diff (shipped).** When both sides of `assertEq` share
a diffable shape, the failure message appends a trim-prefix /
trim-suffix line diff with up to 3 lines of shared context. Lines
that differ are prefixed with `- left` or `+ right`; shared context
lines are prefixed with two spaces. The diff is only computed on the
failure path — passing asserts pay zero. Today's diff coverage:

- Two `String` values — compared directly, line by line.
- Two `List<T>` values with the same primitive `T` (`Int`, `Float`,
  `Bool`, `String`) — each list is rendered as a multi-line literal
  (`[\n  elem,\n  ...\n]`) via `osty_rt_list_primitive_to_string`
  before the diff runs, so an element-level divergence surfaces as
  a single-line `-`/`+` pair.

**Still deferred.** Structs, enums, Maps, Sets, Lists of composite
elements (`List<Struct>`, `List<Map<K, V>>`), and Lists parameterised
by `Char` or `Byte` fall back to source-text-only rendering. A full
solution needs `ToString` protocol dispatch and per-shape format
rules in the backend.

For example:

```osty
let user = getUser("alice")
testing.assertEq(user, User { name: "alice", age: 30, email: "a@x.com" })
```

On failure produces:

```
assertion failed at user_test.osty:42
  testing.assertEq(user, User { name: "alice", age: 30, email: "a@x.com" })

  actual:
    User {
      name: "alice",
      age: 25,          // differs
      email: "a@x.com",
    }

  expected:
    User {
      name: "alice",
      age: 30,
      email: "a@x.com",
    }
```

This is not a general-purpose macro facility. The compiler has built-in
knowledge of the `std.testing` assertion functions only. User-defined
functions cannot access argument source text.

### 11.3 Context

`testing.context(msg, body)` attaches a prefix to any assertion failures
in the callback. Useful for table-driven tests:

```osty
fn testAdd() {
    let cases = [
        (1, 2, 3),
        (0, 0, 0),
        (-1, -1, -2),
    ]
    for (i, (a, b, expected)) in cases.enumerate() {
        testing.context("case {i}: add({a}, {b})", || {
            testing.assertEq(add(a, b), expected)
        })
    }
}
```

### 11.4 Benchmarks

Functions whose names begin with `bench` and take no arguments are
benchmark functions, run by `osty test --bench`:

```osty
fn benchParseJson() {
    testing.benchmark(1000, || {
        let _: Config = json.decode(sampleText)?
        Ok(())
    })
}
```

`testing.benchmark(iterations, body)` runs the body and reports timing
statistics.

### 11.5 Snapshots

`std.testing.snapshot` provides golden-file testing:

```osty
fn testRenderOutput() {
    let output = render(input)
    testing.snapshot("render_basic", output)
}
```

**Location.** The golden file lives at
`<source_dir>/__snapshots__/<sanitize(name)>.snap`, where
`source_dir` is the directory of the test source file and `sanitize`
follows the rule from [§11.7](#117-test-order) (letters, digits,
underscore pass through; everything else collapses to `_`; empty or
all-sanitized names fall back to the stem `snapshot`). The source
path is pinned at compile time so snapshot resolution is independent
of the process working directory.

**Lifecycle.**

| Golden state | Result |
|---|---|
| Missing | Write `output` to the golden; pass with `snapshot: created <path>` on stdout. |
| Matches `output` byte-for-byte | Pass silently. |
| Differs from `output` | Print `testing.snapshot(<name>) mismatch: <path>` + a line-level diff (same shape as §11.2) to stdout and exit 1 — same observable outcome as any failing assertion. |

**Accepting new output.** `osty test --update-snapshots` (which sets
`OSTY_UPDATE_SNAPSHOTS=1` for the emitted test binaries) overwrites
every golden encountered during the run and prints
`snapshot: updated <path>` for each — tests still pass.

**Test-harness override.** The runtime honors
`OSTY_SNAPSHOT_DIR=<path>` as a drop-in replacement for the source
directory when resolving the golden's location. This is only intended
for test harnesses that exercise the snapshot machinery itself and
want to isolate writes into a tempdir; production `osty test` runs
leave it unset.

### 11.6 Parallel Execution

Tests run in parallel by default. Use `--serial` to force sequential
execution. Tests depending on shared mutable state should use
`std.sync` primitives or opt into serial execution.

### 11.7 Test Order

Within each execution mode (parallel or serial), `osty test` chooses a
**randomized** start order. The random seed is printed at the start of
every run (and at the head of any failure output) so failures are
reproducible:

```
$ osty test
running 42 tests (seed 0x8F3A2B71)
...

$ osty test --seed 0x8F3A2B71   # reproduce the exact same order
```

Declaration-order or alphabetical execution is not provided. Tests
that accidentally share state are surfaced by the randomization; fix
the dependency rather than pinning the order.

### 11.8 Setup / Teardown

Osty does not expose `beforeEach`/`afterEach` hooks. Shared setup
belongs in helper functions called from each test, or in a
`testing.context` block. Test-local cleanup uses `defer`. Because tests
may run in parallel (§11.6), any shared fixture must be constructed
per-test or guarded with `std.sync` primitives.

---
