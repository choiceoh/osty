### 10.1 Tier 1 (Core)

- `std.io` — `print`, `println`, `eprint`, `eprintln`, `readLine`
- `std.fs` — whole-file and path operations:
  `read`, `readToString`, `write`, `writeString`, `exists`,
  `create`, `remove`, `rename`, `copy`, `mkdir`, `mkdirAll`
- `std.strings` — string manipulation
- `std.collections` — `List`, `Map`, `Set`
- `std.option` — `Option`, `Some`, `None` (auto-imported)
- `std.result` — `Result`, `Ok`, `Err` (auto-imported)
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
