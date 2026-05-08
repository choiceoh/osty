## 11. Testing

Osty v0.6 ships a built-in testing surface. Tests live alongside code
in `_test.osty` files or as `#[test]`-annotated functions in
production sources; the runner is `osty test`, with assertions
provided by `std.testing`. The v0.6 surface adds: declarative golden
tests via `#[golden]` (§11.5.2, G45) which reuse the v0.5 snapshot
on-disk format, executable specification clauses through `spec { }`
blocks (§3.13, G43) which run as tests under `osty test --spec`, and
the `#[example]` annotation (§3.12, G42) which auto-checks
`input → output` declarations under `osty test --example`.

Test files use the `_test.osty` suffix and live alongside code in the
same package. Functions whose names begin with lowercase `test` and
take no arguments are discovered and run by `osty test`. A top-level
zero-arity function annotated with `#[test]` is also discovered, even in
a production source file; annotated test functions are excluded from
production builds.

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

`testing.benchmark(iterations, body)` runs the closure and prints two
lines per call site:

```
bench <abs-path>:<line> iter=<N> total=<T>ns avg=<A>ns
  min=<Mn>ns p50=<M50>ns p99=<M99>ns max=<Mx>ns
```

`T` is the monotonic-clock elapsed time around the loop, `A` is
`T / N` (or `0` when `N <= 0`). Values are nanoseconds throughout.
The distribution line is computed from per-iteration samples.
`<abs-path>` is the source file containing the `testing.benchmark(...)`
call (`<bench>` when the compiler has no source path), and `<line>` is
that call's 1-based line number.

Before the timed loop the compiler inserts a warmup pass of
`clamp(N/10, 1, 1000)` iterations that is **not** counted in `iter=`;
this reduces cold-cache / branch-predictor noise on short bodies.

`?` inside the closure body is allowed. If it sees `Err(e)` or `None`
on any iteration the benchmark prints `bench \`?\` propagated failure
at <abs-path>:<line>` to stdout and exits the bench with failure
status; the summary line is not emitted for that benchmark.

In bench mode:

- Only `bench*`-named, zero-arity functions are discovered. `#[test]`
  annotations have no effect on bench discovery, and `test*` functions
  are skipped.
- `--bench` and `--doc` are mutually exclusive.
- Assertion failures inside the closure abort the bench with the
  normal `testing.*` diagnostic; no summary line is printed for a
  failed bench.
- `osty test --bench --benchtime <duration>` activates auto-tuning:
  the declared `N` is replaced by the output of a 10-iteration probe
  scaled to hit `<duration>` (clamped to `[10, 100_000_000]` with 20%
  headroom). Without the flag the declared `N` is authoritative. The
  flag uses Go-style durations (`500ms`, `2s`, `1m`) and requires
  `--bench`.

### 11.5 Snapshots / Golden Tests

Two surface forms are provided: a *runtime form* via
`std.testing.snapshot()` (§11.5.1, baseline) and a *declarative form*
via the `#[golden]` annotation (§11.5.2, v0.6 G45). Both share the
same on-disk format — a snapshot file written by one form can be
consumed by the other.

#### 11.5.1 Runtime form — `testing.snapshot()`

`std.testing.snapshot` provides golden-file testing within a regular
test function:

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

#### 11.5.2 Declarative form — `#[golden]` (G45)

A function may carry `#[golden(path, mode)]` to declare its output is
compared against an explicit snapshot file. The annotation form is
designed for *compiler / formatter / docgen / diagnostic* output
testing — exactly the workloads where the language's own toolchain is
exercised.

```osty
#[golden("fixtures/format_expr.snap")]
fn testFormatBinaryOp() {
    let result = formatExpr(parseExpr("1 + 2 * 3"))
    testing.assertGolden(result)
}

#[golden("fixtures/diag_E0765.snap", mode = "ast")]
fn testNumericNarrowingDiag() {
    let diag = checkSnippet("let x: Int8 = bigInt")
    testing.assertGolden(diag.toString())
}
```

**Modes.**

| Mode | Comparison |
|---|---|
| `"text"` *(default)* | Byte-exact. |
| `"ast"` | Reparse both sides as Osty source, compare normalised AST. Whitespace / comments / formatter idiosyncrasies are ignored. |
| `"json"` | Parse as JSON, compare structurally (key order ignored). |
| `"diag"` | Osty diagnostic format — same code/message comparable across `Span` deltas. |

**Reproducibility.** A `#[golden]` function is implicitly
`#[reproducible(scope = "target")]` (§3.11). Calling non-deterministic
capabilities or unordered iteration is `E0444`. AST mode applied to
output that is not valid Osty source is `E0446`. Missing snapshot on
first run is `E0445` (run `osty test --update-golden`). A snapshot's
embedded `source-hash` header that disagrees with the function's
current definition is `W0444`.

**Snapshot file format.**

```
# osty-golden-v1
# function: TestFormatBinaryOp
# mode: ast
# fixture: sampleBinaryExpr   (optional; references #[fixture(name)])
# generated: 2026-05-07T12:34:56Z
# source-hash: abc123...

fn add(x: Int, y: Int) -> Int { x + y }
```

Header lines start with `#`; the body begins after one blank line.

**Tooling.** `osty test --golden` runs the comparison; `osty test
--update-golden[=<path>]` rewrites snapshots. See §13.8.

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
