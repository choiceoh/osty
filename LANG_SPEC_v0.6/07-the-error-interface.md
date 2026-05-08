## 7. The Error Interface

Osty v0.6 represents errors as values, never as exceptions. The
chapter defines four pieces:

- **`Error` interface** (§7.1) — the structural protocol every error
  satisfies. Values that flow through this interface carry a nominal
  type tag for runtime downcast.
- **`BasicError`** (§7.2) — the stdlib constructor for ad-hoc string
  errors (`Error.new("...")`).
- **Custom error enums** (§7.3) — application-specific error
  hierarchies that integrate via the structural rules of §2.6.
- **Propagation** (§7.4) — the `?` operator for `Result<T, E>` /
  `Option<T>`, with up-cast to `Error` when the enclosing function
  returns `Result<_, Error>`.

Two v0.6 surfaces sit on top of this baseline:

- **`#[error_contract]`** (§7.5, G41) — a declaration-level
  attestation that catalogues which error variants flow out and
  under which conditions. Enables caller-side exhaustiveness pruning,
  machine-readable failure-mode export via `osty context` (§13.4),
  and the publish-time SemVer rule that adding a contracted variant
  is a breaking change (§3.14.3).
- **Cancellation as a recoverable error** — `Err(Cancelled { cause
  })` is the Result-shaped form of the cancel signal (§8.4.1). It
  flows through `?` identically to any other `Error`, so canceling a
  `taskGroup` requires no new control-flow construct in this
  chapter.

There is no `try`/`catch`. Programmer errors (`abort` / `unreachable`
/ `todo`) are deliberate process termination, not recoverable failure
— they bypass `?` propagation entirely (§4.12 rule 7).

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

#### 7.5.1 Contract enforcement at function boundary

`#[error_contract]` is checked at three points:

1. **Definition site** — every `Err(V)` returned (including via
   `?`-propagation that widens a callee's error to the contract
   variant) must use a variant listed in the contract. Returning an
   un-contracted variant is `E0410`. `Err`-from-helper must either
   be widened explicitly or the contract must list the propagated
   variant.

2. **Caller match exhaustiveness** — when the contracted function is
   the scrutinee of a `match`, exhaustiveness considers only contract
   variants. Uncontracted enum variants do not need to be matched
   (they're treated as dead per the contract). Match arms targeting
   uncontracted variants emit `W0413`.

3. **`?`-propagation through a contracted caller** — if the *caller*
   carries `#[error_contract]`, every callee error variant that
   could flow through `?` must be a subset of the caller's contract.
   Mismatch is `E0414`; the fix is either to expand the caller's
   contract or to convert the error explicitly.

#### 7.5.2 Worked patterns

**Wrapping multiple concrete errors into one contract.** A pipeline
function that calls `Email.parse` and `db.exec` carries a wrapping
enum that the contract enumerates:

```osty
pub enum SignupError {
    EmailFormat,
    DomainBlocked(String),
    DbConflict(Int),
    DbUnavailable,
}

#[error_contract(
    SignupError.EmailFormat     when "Email.parse rejected the input",
    SignupError.DomainBlocked   when "domain in deny-list",
    SignupError.DbConflict      when "email unique constraint",
    SignupError.DbUnavailable   when "DB connection lost mid-request",
)]
pub fn signup(email: String, db: Db) -> Result<UserId, SignupError> {
    let parsed = Email.parse(email)
        .orError(SignupError.EmailFormat)?
    if isBlocked(parsed.domain()) {
        return Err(SignupError.DomainBlocked(parsed.domain()))
    }
    db.exec(insertSql(parsed))
        .mapErr(|e| match e.downcast::<DbError>() {
            Some(DbError.Conflict(c)) -> SignupError.DbConflict(c),
            Some(_) -> SignupError.DbUnavailable,
            None -> SignupError.DbUnavailable,
        })
        .map(|_| UserId(0))
}
```

The contract documents *which* failure modes the caller will see,
and the type checker enforces that `db.exec`'s native error type
cannot leak out un-rewrapped.

**Optional-as-success in a contract context.** When a function
returns `Result<T?, E>` (e.g. "find user — error means DB issue,
`Ok(None)` means simply not found"), the contract is on the `E`
side only:

```osty
#[error_contract(
    LookupError.DbUnavailable when "DB connection lost",
)]
pub fn findUser(db: Db, id: Int) -> Result<User?, LookupError> {
    match db.queryOne::<User>("SELECT * FROM users WHERE id = ?", [id]) {
        Ok(u) -> Ok(u),                                // Some / None passes through
        Err(_) -> Err(LookupError.DbUnavailable),
    }
}
```

This pattern is preferred over a 3-way `Result<T, NotFoundOrError>`
because it keeps "user does not exist" — a normal flow outcome —
out of the contract.

#### 7.5.3 Contract evolution and SemVer

Contract changes are SemVer-relevant per §3.14.3:

| Change | `#[stability("stable")]` | `#[stability("experimental")]` |
|---|---|---|
| Add a contract variant | major bump | minor bump |
| Remove a contract variant | major bump | minor bump |
| Tighten the `when` clause text | patch bump (doc-only) | patch bump |
| Loosen — variant now flows from a new condition | patch bump (additive) | patch bump |

Adding a variant is breaking because callers' `match` expressions
that exhausted the previous contract no longer cover the new
variant. The recommended evolution path is to introduce the new
variant as `experimental` first, let downstream `match
#[match_compat]` clauses opt in, and promote to `stable` after a
release cycle.

#### 7.5.4 Cancellation in error contracts

`Cancelled` (§7.6, §8.4.1) is a structural concern that flows
orthogonally to domain failures. The convention is:

- **Do not list `Cancelled` in `#[error_contract]`.** The contract
  enumerates *domain* failure modes; cancellation is not a domain
  failure.
- **Cancellation propagates through a contracted Result naturally.**
  When a contracted function calls a stdlib blocking method and the
  caller's `taskGroup` is cancelled, the resulting `Err(Cancelled
  { cause })` flows through `?` and exits the contracted function.
  This *does not* count as an unlisted variant — `Cancelled` is
  outside the contract surface entirely.
- **Callers must handle cancellation separately.** A `match` against
  a contracted Result needs an `Err(_)` arm to catch `Cancelled`
  even when the contract is exhaustive on domain errors.

```osty
match createUser(email, db) {
    Ok(uid) -> ...,
    Err(SignupError.EmailFormat) -> ...,
    Err(SignupError.DomainBlocked(d)) -> ...,
    Err(SignupError.DbConflict(_)) -> ...,
    Err(SignupError.DbUnavailable) -> ...,
    Err(other) -> {
        // Cancelled or any future contract addition.
        if other.downcast::<Cancelled>().isSome() { return Err(other) }
        unreachable("contract violation: {other}")
    },
}
```

The `unreachable` arm catches contract violations at runtime — a
defense-in-depth backstop against soundness bugs in the contract
checker. In practice the checker prevents reaching it; the
`unreachable` panics if the invariant is violated.

### 7.6 Cancellation as a Recoverable Error

The cancel signal is delivered as `Err(Cancelled { cause })` (see
§8.4.1). It is an *ordinary* `Error`-shaped value: every rule in this
chapter — `?`-propagation, `match err.downcast::<Cancelled>()`,
`#[error_contract(Cancelled when ...)]`, conversion to a wrapping
enum — applies identically.

```osty
fn copyAll(fs: Fs, src: String, dst: String) -> Result<(), Error> {
    let r = fs.open(src)?
    defer r.close()
    let w = fs.create(dst)?
    defer w.close()

    io.copy(w, r)?            // returns Err(Cancelled) on cancel
    Ok(())
}
```

If recovery is desired (rare), a downcast tells `Cancelled` apart
from other errors. The recovered task **must** propagate cancellation
upward immediately afterward — swallowing a cancel is a soundness
bug:

```osty
fn try_with_log(fs: Fs, console: Console, src: String) -> Result<Bytes, Error> {
    match fs.read(src) {
        Ok(b) -> Ok(b),
        Err(e) -> match e.downcast::<Cancelled>() {
            Some(c) -> {
                console.eprintln("cancel mid-read: {c.cause.message()}")
                Err(e)         // re-propagate; do not return Ok or a placeholder
            },
            None -> Err(e),
        },
    }
}
```

**Cancellation is *not* listed in `#[error_contract]` by default.** A
contract that does not mention `Cancelled` still permits it to flow
out — the contract describes domain-specific failure modes, while
cancellation is a structural concern owned by §8.4.
