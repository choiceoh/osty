## 7. The Error Interface

### 7.1 Definition

`Error` is an interface defined in `std.error` and re-exported by the
prelude:

```osty
pub interface Error {
    fn message(self) -> String
    fn source(self) -> Error? { None }
}
```

### 7.2 BasicError

```osty
return Err(Error.new("invalid input"))
```

`Error.new` constructs a `BasicError`.

### 7.3 Custom Errors

```osty
pub enum FsError {
    NotFound(String),
    PermissionDenied(String),
    IoError(String),

    pub fn message(self) -> String {
        match self {
            NotFound(p) -> "not found: {p}",
            PermissionDenied(p) -> "permission denied: {p}",
            IoError(m) -> "io error: {m}",
        }
    }
}
```

### 7.4 Propagation and Downcasting

`?` up-casts concrete errors to the `Error` interface when the enclosing
function returns `Result<_, Error>`.

To recover the concrete type, use `downcast`:

```osty
fn handle(err: Error) -> Result<(), Error> {
    match err.downcast::<FsError>() {
        Some(fe) -> match fe {
            FsError.NotFound(p) -> retry(p),
            _ -> Err(err),
        },
        None -> Err(err),
    }
}
```

`Error.downcast::<T>()` returns `T?`.

**Runtime mechanism.** Although Osty's interface satisfaction is
otherwise structural (§2.6), values that flow through the `Error`
interface carry a **nominal type tag** identifying the originating
concrete type. The tag is set when a concrete error value is up-cast
to `Error` (e.g. via `?`, return-type widening, or explicit assignment
to an `Error`-typed binding) and is preserved across propagation.
`downcast::<T>()` succeeds iff the stored tag equals `T`'s nominal
identity; it does not perform structural matching.

This nominal exception is intentional: error recovery code routinely
needs to distinguish "this `Error` is really a `FsError`" from "this
`Error` happens to share the same shape as `FsError`," and structural
matching cannot do so safely. No other interface in Osty carries a
runtime type tag.

**Interaction with monomorphization.** The generic compilation model
(§2.7.3) is monomorphization, which erases the source-level distinction
between separate type arguments at runtime. `Error`'s nominal tag is a
separate, orthogonal mechanism: it is attached per-concrete-error-type
at up-cast time and does not depend on, nor interfere with, generic
specialization. `downcast::<T>()` works identically regardless of
whether the error propagated through generic code paths or fully
concrete ones.

**`?` and error type widening.** When a function returns
`Result<_, Error>` and `?` is applied to a `Result<_, CustomError>`
where `CustomError: Error`, the conversion is automatic — no explicit
cast is required. When the function returns `Result<_, SomeConcrete>`
and the `?` target has a different concrete error type, the conversion
is a compile error: convert explicitly or widen the function's return
type.

**Multiple error types in one function.** The recommended pattern is
either (a) widen to `Result<_, Error>` and let `?` upcast each concrete
error, or (b) define a local `enum` implementing `Error` that wraps the
concrete types and propagate that:

```osty
pub enum PipelineError {
    Fetch(FetchError),
    Parse(ParseError),
    Write(IoError),

    pub fn message(self) -> String {
        match self {
            Fetch(e) -> "fetch: {e.message()}",
            Parse(e) -> "parse: {e.message()}",
            Write(e) -> "write: {e.message()}",
        }
    }
}
```

The wrapping enum must then be constructed explicitly at each error
site; `?` does not synthesize wrappers.

---
