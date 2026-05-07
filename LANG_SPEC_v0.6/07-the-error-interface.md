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

`Error.downcast::<T>()` returns `T?`. The postfix form `err as? T` is a
shortcut for the same operation and is valid only on `Error` values.

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

### 7.5 Error Contract (G44)

A function returning a `Result<T, E>` over a *concrete enum* `E` may
carry an `#[error_contract]` annotation that catalogues which error
variants are produced under which conditions. The contract is a
machine-readable failure-mode catalogue — `osty doc` renders it as a
table, `osty test --example` cross-checks it, `osty context` (§13.6)
exposes it as JSON, and the type checker uses it to prune match
exhaustiveness.

```osty
pub enum EmailError {
    Format,
    DomainBlocked(String),
    TooLong(Int),
}

#[error_contract(
    EmailError.Format         when "missing @ or wrong format",
    EmailError.DomainBlocked  when "domain is in blocklist",
    EmailError.TooLong        when "input exceeds 320 chars",
)]
pub fn parseEmail(s: String) -> Result<Email, EmailError> { ... }
```

**Static checks (Phase 4).**

| Check | Diagnostic |
|---|---|
| `Err(V)` return path uses a variant `V` not in the contract | `E0410` |
| Contract entry references a variant not on the declared error type | `E0411` |
| Contract entry never fires from any return path | `W0411` |

**Erased `Error` — declarative form only.**

`#[error_contract]` requires a *concrete enum* error type. Applying it
to a function returning `Result<_, Error>` is `E0412`. The
documentation-only form `#[error_contract(any)]` is permitted (no
check); it tells `osty doc` "this function returns many error types,
see body."

**`?` propagation.** When the caller carries `#[error_contract]`,
the caller's contract must be a *superset* of every callee contract
that flows through `?`:

```osty
#[error_contract(
    EmailError.Format         when "from parseEmail",
    EmailError.DomainBlocked  when "from parseEmail",
    DbError.Conflict          when "duplicate user",
)]
fn createUser(s: String) -> Result<UserId, EmailError | DbError> {
    let e = parseEmail(s)?       // OK — caller contract ⊇ parseEmail
    db.insert(e)?                 // OK — DbError.Conflict in caller contract
}
```

The closed union form `EmailError | DbError` enumerates the error
types statically. Open / row-polymorphic error unions are deferred to
v0.7+ (see `00-revision.md §8 Open Items`).

**Match exhaustiveness.** When a function carrying `#[error_contract]`
is the scrutinee of a `match`, exhaustiveness is computed against the
contract variants only:

- Covering every contract variant exhausts the match (other variants
  are treated as dead per the contract).
- A `_ -> ...` arm is also exhaustive.
- An `Err(V)` arm where `V` is on the enum but *not* in the contract
  emits `W0413` (dead-per-contract).

**Doc / context surfacing.**

`osty doc` rendering of a contracted function includes:

```
Failure modes:
  EmailError.Format        — missing @ or wrong format
  EmailError.DomainBlocked — domain is in blocklist
  EmailError.TooLong       — input exceeds 320 chars
```

`osty context <fn>` (§13.6) emits the same data as JSON.
