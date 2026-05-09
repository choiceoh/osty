### 10.1 Tier 1 (Core)

- `std.io` — `print`, `println`, `eprint`, `eprintln`, `readLine`, the
  `Reader`/`Writer` protocol, in-memory `BytesReader`/`Buffer`, and core
  stream helpers such as `readAll`, `readExact`, `copy`, `writeString`
- `std.fs` — whole-file and path operations:
  `read`, `readToString`, `write`, `writeString`, `exists`,
  `walk`, `glob`, `watch`, `create`, `remove`, `rename`, `copy`,
  `copyDir`, `mkdir`, `mkdirAll`, `atomicWrite`, `atomicWriteString`,
  `lockFile`, `hashFile`, `diffFiles`
- `std.strings` — string manipulation
- `std.collections` — `List`, `Map`, `Set`
- `std.option` — `Option`, `Some`, `None` (auto-imported), plus rich
  combinators (`count`, `forEach`, `toList`, `zipWith`, `reduce`) and
  nested-shape, composition, and batch helpers `flatten`, `transpose`,
  `unzip`, `values`, `any`, `all`, `traverse`, `filterMap`, `findMap`,
  `map2`, `map3`
- `std.result` — `Result`, `Ok`, `Err` (auto-imported), plus rich
  combinators (`count`, `forEach`, `toList`, `zip`, `zipWith`) and
  nested-shape, composition, and batch helpers `flatten`, `transpose`,
  `values`, `errors`, `partition`, `all`, `traverse`, `map2`, `map3`,
  `allErrors`, `traverseErrors`
- `std.error` — `Error`, `BasicError`, `Error.new` (auto-imported)
- `std.cmp` — `Equal`, `Ordered`, `Hashable` (auto-imported)
- `std.ref` — `same(a, b)`
- `std.process` — `abort(msg: String) -> Never`,
  `unreachable() -> Never`, `todo(msg: String) -> Never`,
  `ignoreError`, `logError`. Signatures of the latter two:

  ```
  fn ignoreError<T, E>(result: Result<T, E>)
  fn logError<T>(result: Result<T, Error>, msg: String)
  ```

  `ignoreError` consumes the result and discards both the value and any
  error. `logError` consumes the result; on `Err(e)`, emits a warning
  via `std.log` (§10.10) of the form `"{msg}: {e.message()}"`. Both are
  the canonical helpers for use inside `defer` (§4.12).
- `std.debug` — `dbg(value)`

#### 10.1.1 Tier 1 capability split

Most of Tier 1 is *pure* — no capability is required. The exceptions
are filesystem and console I/O, which route through `Fs` / `Console`
capabilities (§20.9). The split:

| Module | Pure | Effectful |
|---|---|---|
| `std.io` | `Reader`/`Writer` protocol, `BytesReader`, `Buffer`, `readAll`, `copy`, `writeString` | `Console.print`, `Console.println`, `Console.eprint`, `Console.eprintln`, `Console.readLine` |
| `std.fs` | `Path.parse`, path manipulation helpers | `Fs.read`, `Fs.write`, `Fs.exists`, `Fs.walk`, `Fs.create`, `Fs.atomicWrite`, etc. |
| `std.strings` | All | (none) |
| `std.collections` | All | (none) |
| `std.option` | All | (none) |
| `std.result` | All | (none) |
| `std.error` | All | (none) |
| `std.cmp` | All | (none) |
| `std.ref` | `same(a, b)` | (none) |
| `std.process` | `ignoreError`, `logError` | `abort`, `unreachable`, `todo` (process termination — not capability-bound but still effectful) |
| `std.debug` | `dbg(value)` (writes via ambient `Console`) | implicit `Console` use |

A function that uses only the *pure* part of Tier 1 — `std.strings`,
`std.collections`, `std.option`, `std.result`, `std.cmp` — is
acceptable inside `#[pure]` without
modification. `std.io` / `std.fs` callers must accept a capability
parameter.

#### 10.1.2 Tier 1 evolution policy

Tier 1 is the most stable surface in the standard library. The v0.6
evolution rule:

- New methods may be added (additive, minor bump).
- New types may be added (additive, minor bump).
- Existing method signatures may not change without a major bump.
- Existing types may not be removed without a major bump and a full
  release-cycle deprecation.

Consequence: Tier 1 grows monotonically across minor versions. A
v0.6 program continues to compile against v0.7's Tier 1 without
modification.
