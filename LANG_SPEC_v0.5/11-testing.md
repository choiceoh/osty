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

First run writes the snapshot file; subsequent runs compare. Update
with `osty test --update-snapshots`.

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
