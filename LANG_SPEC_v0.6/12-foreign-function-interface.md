## 12. Foreign Function Interface

Osty v0.6 supports two FFI families:

- **Go FFI** (`use go "..." { ... }`) — for the bootstrap toolchain
  and any deployment that runs on the Go runtime. The bridge maps
  `Result<T, Error>` ↔ `(T, error)`, `T?` ↔ `*T`, and
  preserves panic-as-process-abort semantics. Closures, generics,
  empty interfaces, and channel types are not exposed across the
  boundary (§12.7).
- **Native C ABI** (`use c "..." { ... }` / `use runtime.cabi.*`) —
  for `--backend llvm` builds, where the Go runtime is not present.
  Same constraint set as Go FFI; symbol resolution and link
  contracts are described in §12.8.

Three v0.6 surfaces apply at the FFI boundary:

- **Information flow tags drop on entry.** Bytes that originate
  outside Osty have no `#[taint]` history, so the FFI wrapper
  receives them untagged. To re-tag (so downstream sinks pay the
  appropriate sanitization cost), apply `#[taint("σ")]` on the
  wrapper's return type at the boundary.
- **Audited declassification.** When Osty-side data must cross *out*
  of Osty's flow tracking despite carrying tags, the wrapper carries
  `#[trusted_declassify(reason)]` — every such site is enumerable
  via `osty audit --trusted-declassify` (§13.7).
- **Capability passthrough is not allowed.** A capability instance
  (`Clock`, `Net`, …) is an Osty-side object with no stable foreign
  ABI. FFI wrappers that need foreign-side effects must take the
  primitive arguments (paths, addresses, …) and own the effect
  inside the Osty wrapper, not pass a `Net` value through.

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

#### 12.2.1 Type mapping caveats

The bridge handles the listed type pairs but with some edge-case
constraints:

- **`String` ↔ `string`** — Osty's `String` is immutable and
  length-prefixed; Go's is similarly immutable. Cross-boundary
  passes copy the bytes (Go does not share the Osty heap).
- **`Bytes` ↔ `[]byte`** — Bytes are passed as a *copy*, not a
  slice into the Osty heap. Mutations on the Go side do not
  propagate back unless explicitly returned.
- **`List<T>` ↔ `[]T`** — Element type T must be one of the listed
  primitives or a `use go` struct. `List<List<Int>>` works (nested
  slice on Go side); `List<UserStruct>` works only if `UserStruct`
  is declared in the same `use go` block.
- **`Map<K, V>` ↔ `map[K]V`** — K must be a comparable Go type
  (string, int variants). User-defined struct keys are rejected.
- **`Result<T, Error>` ↔ `(T, error)`** — non-nil `error` becomes
  `Err(BasicError(error.Error()))` on the Osty side (§12.4).

#### 12.2.2 Generic types and FFI

Generic Osty types (`List<T>` with arbitrary T, `Option<T>`,
`Result<T, E>`) do not work directly across FFI; they require
monomorphization at the bridge. Each FFI declaration with a generic
parameter must be specialized:

```osty
use go "myproject" {
    fn ProcessInts(xs: List<Int>) -> List<Int>
    fn ProcessStrings(xs: List<String>) -> List<String>
}
```

A generic Osty function may *wrap* a monomorphic FFI call but
cannot itself cross the boundary as generic. This is a consequence
of monomorphization (§2.7.3).

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

### 12.9 FFI and v0.6 surfaces

This section catalogues how each v0.6 declaration-level annotation
surfaces interacts with FFI declarations. All rules apply uniformly
to `use go "..."`, `use c "..."`, and `use runtime.*` blocks.

#### 12.9.1 `#[stability]` on FFI imports

An FFI declaration with `#[stability("stable")]` is part of the
package's public API surface — adding, removing, or changing the
imported symbol's signature triggers a SemVer-relevant change
(§3.14.3). The rules:

- **Add a new FFI declaration in the same block** — minor bump
  (additive).
- **Remove a previously-public FFI symbol** — major bump
  (caller's `?`-propagation breaks).
- **Change the imported symbol's name without changing the Osty
  declaration name** — patch bump (the Osty surface is
  unchanged; only the link target moves).
- **Change the imported library** (`use go "net/http"` →
  `use go "github.com/foo/http"`) — major bump (link contract
  changes).

#### 12.9.2 `#[reproducible]` and FFI

A function annotated `#[reproducible(scope = X)]` *cannot* call an
FFI symbol unless that symbol is itself annotated `#[reproducible]`
inside the FFI block. Foreign symbols are deterministic-by-default
*not assumed* — the author must attest:

```osty
use c "myproject" {
    #[reproducible(scope = "portable")]
    fn fast_hash_v2(data: Bytes) -> Bytes32

    fn random_bytes(n: Int) -> Bytes        // no annotation — non-deterministic
}

#[reproducible(scope = "portable")]
fn computeKey(data: Bytes) -> Bytes32 {
    fast_hash_v2(data)        // OK — callee carries #[reproducible]
}

#[reproducible(scope = "portable")]
fn buildToken(n: Int) -> Bytes {
    random_bytes(n)           // E0786 — non-reproducible callee
}
```

The annotation is *attestation* — the compiler trusts it. A
mistakenly-annotated foreign symbol breaks reproducibility silently.

#### 12.9.3 Information flow at FFI boundary

By default, all data crossing into Osty from FFI carries *no* tags
(`#[taint(*)]` is empty). To re-tag the boundary, apply
`#[taint("σ")]` on the FFI declaration's return type:

```osty
use go "net/http" {
    fn Get(url: String) -> Result<#[taint("net_input")] Response, Error>
}
```

Outbound data (Osty → FFI) by default loses any flow tags it
carried — the foreign type system has no notion of Osty's tags.
For audited declassification at the boundary, wrap the call in a
function annotated `#[trusted_declassify(reason)]`:

```osty
#[trusted_declassify(reason = "JNI bridge to legacy code path")]
fn legacyBridge(payload: #[taint("user_input")] String) -> () {
    legacyJniSetText(payload)
}
```

Every `#[trusted_declassify]` site is enumerable via
`osty audit --trusted-declassify`.

#### 12.9.4 `#[error_contract]` and FFI errors

A function that propagates errors from an FFI call into a contract-
typed `Result<T, ConcreteEnum>` must convert the FFI error
explicitly:

```osty
use go "..." {
    fn DoIo() -> Result<Bytes, Error>
}

#[error_contract(MyError.IoFailed when "underlying I/O error")]
fn safeDoIo() -> Result<Bytes, MyError> {
    DoIo().mapErr(|_| MyError.IoFailed)
}
```

`?`-propagation through an FFI error directly into a contract is
`E0414` (caller contract does not include the FFI's error variant).
Conversion is mandatory.

#### 12.9.5 Capability access from FFI

FFI symbols cannot receive Osty capabilities as parameters —
capabilities are interface values with vtable layouts that have no
stable foreign ABI. To do effectful work via FFI:

- The FFI wrapper takes plain arguments (paths, addresses, byte
  buffers).
- The Osty side that calls the wrapper takes the relevant
  capability and constructs the FFI inputs from it.

```osty
use c "fastio" {
    fn osty_fastio_read(path: ptr, out: ptr, max: Int) -> Int
}

fn fastRead(fs: Fs, path: String) -> Result<Bytes, Error> {
    // Permission check via the capability — even though the actual
    // read happens in C, the capability still gates whether we
    // *should* read at all.
    if !fs.exists(path)? { return Err(Error.new("not found")) }
    // ... call osty_fastio_read with the path bytes ...
}
```

This pattern keeps capability discipline at the call site without
forcing capabilities through the foreign ABI.

---
