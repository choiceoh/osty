## 12. Foreign Function Interface

### 12.1 Importing Go Packages

```osty
use go "net/http" {
    fn Get(url: String) -> Result<Response, Error>
    fn Post(url: String, contentType: String, body: Reader) -> Result<Response, Error>

    struct Response {
        StatusCode: Int,
        Body: Reader,
    }
}

use go "github.com/foo/bar" as bar {
    fn DoThing(x: Int) -> Int
}
```

The imported package is bound to the last path segment (or the alias).

### 12.2 Type Mapping

| Osty | Go |
|---|---|
| `Int` | `int64` |
| `Int32` | `int32` |
| `UInt8` | `uint8` |
| `Float` | `float64` |
| `Bool` | `bool` |
| `String` | `string` |
| `Bytes` | `[]byte` |
| `List<T>` | `[]T` |
| `Map<K, V>` | `map[K]V` |
| `T?` | `*T` (`nil` ↔ `None`) |
| `Result<T, Error>` | Go function returning `(T, error)` |
| Osty `struct` in `use go` block | Go `struct` (field-by-field) |

### 12.3 Nullability

Go's nullable types are exposed as `T?` if and only if the declaration
uses the optional form. Non-optional declarations abort the FFI bridge
on `nil`.

### 12.4 Error Mapping

`(T, error)` → `Result<T, Error>`. A non-nil Go `error` wraps as a
`BasicError` whose `message()` returns `error.Error()`. The wrapping
is **best-effort**: the returned `Error` preserves the top-level
message string but does not walk Go's `errors.Unwrap` chain, and
concrete Go error types cannot be recovered via
`Error.downcast::<T>()` (T must be an Osty type). If structured error
information from Go is required, expose an explicit accessor on the
FFI declaration (e.g. `fn StatusCode(err: error) -> Int`).

### 12.5 Goroutines and Channels

Go goroutines and channels obtained via FFI are not integrated with
Osty's structured concurrency or channel types.

### 12.6 Panics

A Go function invoked via FFI that triggers a Go `panic` **aborts the
Osty process**. Panics do not cross the FFI boundary as recoverable
errors: Go's panic/recover model is incompatible with Osty's Result-
based handling, and silently translating panics would hide bugs in the
Go code. Author FFI wrappers that always return `error` for recoverable
failures.

### 12.7 Constraints on FFI Declarations

The following Osty features may **not** appear in `use go "..."` blocks:

- **Generic type parameters.** Osty generics are monomorphized (§2.7.3);
  the bridge needs a concrete Go symbol at link time. A generic Osty
  function can wrap an FFI call, but the declaration itself must be
  monomorphic. Calling a generic Osty function from Go is not
  supported.
- **Closures.** Osty closures capture Osty-side bindings and cannot
  cross the FFI boundary as Go `func` values. Expose the wanted
  behavior as a named `fn` instead.
- **`interface{}` and empty Go interfaces.** Osty requires concrete
  types on both sides. If the Go API expects `interface{}`, write a
  typed wrapper on the Go side.
- **Go channels typed in the declaration.** Use message-passing via
  function calls instead; see §12.5 for the policy.

---
