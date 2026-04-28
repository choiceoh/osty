### 10.1 Tier 1 (Core)

- `std.io` — `print`, `println`, `eprint`, `eprintln`, `readLine`, the
  `Reader`/`Writer` protocol, in-memory `BytesReader`/`Buffer`, and core
  stream helpers such as `readAll`, `readExact`, `copy`, `writeString`
- `std.fs` — whole-file and path operations:
  `read`, `readToString`, `write`, `writeString`, `exists`,
  `create`, `remove`, `rename`, `copy`, `mkdir`, `mkdirAll`
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
