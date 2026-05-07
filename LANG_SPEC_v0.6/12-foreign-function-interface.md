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

### 12.8 Runtime FFI Surface (`use runtime.*`, `use c "..."`)

The native (LLVM) backend exposes a parallel FFI surface keyed off the
`runtime.*` import path. This is the only FFI form supported when
compiling with `--backend llvm`; `use go "..."` is rejected with
`LLVM001` because the native backend cannot embed the Go runtime.

Three surface forms are recognised:

```osty
// 1. Runtime ABI symbols (osty_rt_* namespace, provided by the Osty
//    C runtime). Callable from non-privileged code.
use runtime.strings as strings {
    fn HasPrefix(s: String, prefix: String) -> Bool
}

// 2. Native C ABI imports — surface form. Each function name is
//    bound to the literal extern C symbol; the library name is a
//    descriptive tag for the linker.
use c "osty_demo" as demo {
    fn osty_demo_double(x: Int) -> Int
}

// 3. Native C ABI imports — canonical form. Equivalent to (2);
//    `use c "<lib>" { ... }` is the surface sugar that desugars
//    to this path at parse time.
use runtime.cabi.osty_demo as demo {
    fn osty_demo_double(x: Int) -> Int
}
```

Forms (2) and (3) produce the same AST — `IsRuntimeFFI = true`,
`RuntimePath = "runtime.cabi.<lib>"`. The canonical printer normalises
both to form (2). The lookahead in form (2) requires a string literal
immediately after `c`, so an ordinary `use c.foo` import path is
unaffected.

Symbol resolution:

| Path prefix | Emitted LLVM symbol | Linker contract |
|---|---|---|
| `runtime.strings`, `runtime.path.filepath`, `runtime.package.*` | `osty_rt_<path>_<name>` | Provided by `internal/backend/runtime/osty_runtime.c` |
| `runtime.cabi`, `runtime.cabi.<lib>` (incl. `use c "<lib>"`) | `<name>` (literal) | Caller's responsibility — link the providing object/library |

The constraints in §12.7 apply unchanged: no generics, no closures, no
defaults/keywords, monomorphic signatures only. Type mapping uses the
runtime ABI rules:

| Osty | LLVM | C equivalent (typical) |
|---|---|---|
| `Int` | `i64` | `int64_t` |
| `Float` | `double` | `double` |
| `Bool` | `i1` | `_Bool` (passed as `i1`) |
| `Char` | `i32` | `int32_t` (Unicode codepoint) — usable for C `int` |
| `Byte` | `i8` | `uint8_t` — usable for C `char` / `unsigned char` |
| `String`, `Bytes`, `Error`, `T?`, `(...)`, `fn(...) -> R` | `ptr` | opaque pointer |

`Int32` / `UInt8` / `Float32` and other narrow-width primitives are
**not yet** part of the runtime ABI — for `int abs(int)` style libc
calls the working bridge today is `Char` (i32). String marshalling
between Osty `String` (length-prefixed, GC-managed) and `const char*`
(NUL-terminated) requires an explicit `runtime.strings` helper at the
call site; passing an Osty `String` directly to a C symbol declared as
`String -> ptr` is **not** equivalent to passing a `const char*`.

`runtime.cabi.*` does not relax §12.6 panic semantics: a foreign symbol
that aborts the process aborts Osty too. Recoverable errors must surface
through return values, not host-side exceptions.

#### 12.8.1 Linking C Libraries

`use c "..."` only declares the symbols — the providing library must
be linked at the final native build step. The manifest's
`[target.<triple>]` table carries a `link` array of system library
names (passed to the linker as `-l<name>`, in source order):

```toml
[target.amd64-linux]
link = ["m", "pthread", "osty_demo"]
```

Library names follow the platform's linker convention (no `lib`
prefix, no extension on Unix; the linker resolves `libfoo.{a,so}` /
`foo.lib` / `foo.dylib` per platform). Source order is preserved so
authors can express link order when it matters (typical only with
static archives that have inter-archive symbol references).

The manifest never embeds full paths or `-L` directories; project-wide
search paths come from the build environment. CI / package authors
keep cross-platform link lists per `[target.<triple>]` table.

---
