## 3. Declarations

This chapter defines Osty v0.6 declaration forms — functions (§3.1),
variables (§3.2), multiple assignment (§3.3), structs (§3.4) including
the v0.6 sealed-construct rule (§3.4.5, G40), enums (§3.5), interfaces
(§3.6), type aliases (§3.7), and annotations (§3.8). Sections §3.10
through §3.15 specify the v0.6 *hidden-dependency-surface*
annotations (G36–G46) — `#[spec]` for spec-link traceability,
`#[reproducible]` for environment-independence, `#[purpose]` /
`#[example]` / `#[fixture]` for structured intent, `spec { ... }`
blocks for executable specification, `#[since]` / `#[stability]` /
`#[match_compat]` for API evolution, and `#[budget]` for performance
contracts.

The v0.6 design north star (*hidden dependency is forbidden*) is
realized in this chapter: every external dependency, intent,
contract, or evolution rule that affects a declaration is expressible
at the declaration site. The annotation surface in §3.10 – §3.15
makes that visibility *machine-readable* so that diagnostics
(§3.10.4 spec link, §7.5 error contract), enforcement (§3.11
reproducibility, §3.4.5 sealed construct), and tooling (§13.4 `osty
context`, §13.5 `osty publish`) all share one source of truth.

The annotation set is finite — 31 compiler-recognized annotations as
of v0.6 (§3.8). Three sub-categories sit on top of the underlying
declaration grammar:

| Category | Annotations | Purpose |
|---|---|---|
| **Effect & flow** | `#[ambient]`, `#[reproducible]`, `#[reproducible_capability]`, `#[taint]`, `#[sanitizes]`, `#[requires]`, `#[trusted_declassify]` | Make capability use, reproducibility scope, and information-flow tags explicit at the declaration boundary. |
| **Intent & contract** | `#[purpose]`, `#[example]`, `#[fixture]`, `#[spec]`, `spec { }` block, `#[error_contract]`, `#[golden]` | Author-visible, machine-readable record of *what a declaration is for* and *what it must produce*. |
| **Evolution & budget** | `#[since]`, `#[stability]`, `#[match_compat]`, `#[deprecated]`, `#[budget]` | Promises about API surface stability and performance regression bounds, enforced by `osty publish` and `osty bench --budget`. |

Declarations without these annotations behave per the underlying
grammar — the annotations are *opt-in attestations*, not new syntactic
forms. The compiler does not synthesize defaults, so a function that
omits `#[stability]` is treated as un-attested rather than implicitly
stable.

### 3.1 Functions

```osty
fn add(a: Int, b: Int) -> Int {
    a + b
}

// 라이브러리 함수는 effect 를 capability parameter 로 받는다 (§20).
fn greet(name: String, console: Console) {
    console.println("hi, {name}")
}

fn connect(net: Net, host: String, port: Int = 80, timeout: Int = 30) -> Result<Conn, Error> {
    net.dial(host, port, timeout: timeout)
}

pub fn loadConfig(fs: Fs, path: String) -> Result<Config, Error> {
    let text = fs.readToString(path)?
    let cfg: Config = json.parse(text)?
    Ok(cfg)
}
```

Entry-point 함수는 `#[ambient(...)]` 로 prelude default capability 를
받아 callee 로 forward 한다:

```osty
#[ambient(console, fs, net)]
fn main() {
    greet("world", console)            // ambient forward
    let cfg = loadConfig(fs, "/etc/app.toml")?
    let _ = connect(net, "api.example.com", 443)
}
```

- Parameter types required.
- Return type required unless `()`.
- The body is a block expression; the final expression is the return.
- `return expr` for early return.
- No function overloading, no named arguments.

**Default arguments and keyword arguments.** Trailing parameters may
have default values, and parameters with defaults may be passed by
name:

```osty
fn connect(host: String, port: Int = 80, timeout: Int = 30) -> Result<Conn, Error>

connect("api.com")                            // defaults
connect("api.com", 443)                       // positional
connect("api.com", 443, 60)                   // positional
connect("api.com", timeout: 60)               // keyword skips port
connect("api.com", port: 443, timeout: 60)    // both keyword
connect("api.com", timeout: 60, port: 443)    // keywords in any order
```

Rules:
- Only trailing parameters may have defaults; once defaulted, all
  following parameters must also have defaults.
- Defaults must be `DefaultLiteral`s: numeric, string, char, byte, bool,
  `None`, `Ok(...)`, `Err(...)`, empty collection literals (`[]`, `{:}`),
  `()`, struct literals whose fields are `DefaultLiteral`s, or `const fn`
  calls allowed by §3.1.1.
- Defaults are evaluated at call time at each call site.
- Required parameters (no default) are **positional only**.
- Parameters with defaults may be passed either **positionally** or
  **by keyword**. Keyword form is `name: value`.
- Positional arguments must precede all keyword arguments.
- Each parameter may be supplied at most once.

Style note: more than two trailing defaults usually reads better as an
option struct, especially when callers would benefit from constructing
and reusing configuration values.

**Diagnostic template — positional after keyword.** The compiler emits
a fixed-form diagnostic when a positional argument follows a keyword
argument (resolves G7). Tooling (LSP, formatter) may rely on this
shape:

```
error: positional argument after keyword argument
  --> foo.osty:10:28
   |
10 |   connect("api.com", port: 443, 60)
   |                                 ^^ positional argument here
   |                      -------- previous keyword argument
   = help: convert the trailing positional argument to keyword form,
           or move all keyword arguments to the end of the call.
```

#### 3.1.1 `const fn` — Compile-Time Evaluable Functions

A function declared `const fn` (optionally `pub const fn`) is evaluable at
compile time. The sole motivating use case is composition of values
usable as `DefaultLiteral` (G21): a `const fn` call whose arguments are
themselves `DefaultLiteral`s may appear in a default-argument position.

```osty
const fn kb(n: Int) -> Int { n * 1024 }
const fn defaultBuffer() -> Int { kb(8) }

pub fn connect(host: String, buffer: Int = defaultBuffer()) -> Result<Conn, Error> {
    ...
}
```

The body of a `const fn` is restricted to the set below. A construct
outside this set is `E0766`.

**Capability matrix.**

| Construct                                                | Allowed |
|----------------------------------------------------------|---------|
| Literal values (numeric, string, char, byte, bool, `None`, `()`) | yes |
| Unary `-` on numeric literals                            | yes |
| Arithmetic `+ - * / %` on `Int` / `Float` operands       | yes |
| Comparison `< <= > >= == !=`                             | yes |
| Boolean `&& \|\| !`                                      | yes |
| `let` binding (immutable, single-assignment)             | yes |
| Parameter reference (own formals)                        | yes |
| Reference to a top-level `pub? let` of `DefaultLiteral` type | yes |
| Direct call to another `const fn` (acyclic; see below)   | yes |
| Struct literal with all-const fields                     | yes |
| Enum variant construction with all-const payloads (incl. `Some`/`Ok`/`Err`) | yes |
| Tuple literal with all-const elements                    | yes |
| List / Map literal with all-const elements               | yes |
| Parenthesized / block expression whose result is const   | yes |
| `if` / `match` / `for` / `loop` / `while`                | **no** |
| `return` statement                                       | **no** (use final-expression form) |
| `?` operator                                             | **no** |
| `defer`                                                  | **no** |
| Recursion, direct or through a `const fn` cycle          | **no** |
| String concatenation `+` or interpolation `"{expr}"`     | **no** |
| Closure / lambda expression                              | **no** |
| Method call, operator via `#[op(...)]`                   | **no** |
| `let mut`, assignment, compound assignment               | **no** |
| FFI symbols from `use go "..."` blocks                   | **no** |
| `panic` / `todo` / `abort` / `unreachable`               | **no** |
| Generic type parameters on the `const fn` itself         | **no** |
| I/O (`println`, `std.fs.*`, etc.)                        | **no** |

**Additional rules.**

- The call graph of `const fn` declarations must be acyclic. A cycle
  — direct or transitive — is `E0767`, reported at the resolver pass
  before type checking.
- `const fn` may not declare type parameters (`E0768`). If a type-
  generic compile-time value is needed, declare per-type `const fn`s
  or fall back to a runtime `pub let` (monomorphization of a generic
  `const fn` would require a const-evaluation engine Osty does not
  provide).
- The return type of a `const fn` must be a concrete type whose values
  are themselves `DefaultLiteral`-compatible under the extended
  definition (numeric / string / char / byte / bool / `None` / `()` /
  struct whose fields are such / enum variant whose payloads are such /
  tuple / list / map of such).
- A `const fn` call in any position other than a default-argument
  expression is evaluated at the call site exactly like an ordinary
  function call. The `const` prefix constrains the **body** and
  enables **default-argument use**; it does not force constant-
  folding in runtime call sites.

**Forward compatibility.** The FORBID rows above are the stable set for
v0.6 (carried unchanged from v0.5). Relaxing any of them is an
additive, semver-observable change — it enables source that previously
did not compile. Such changes must ship under a normal minor version
bump; no FORBID row silently flips to ALLOW inside a v0.6.x patch
release.

##### `const fn` and v0.6 surfaces

The v0.6 annotation surface composes with `const fn` as follows:

- **`#[reproducible]`** — every `const fn` is implicitly
  reproducible at every scope (the body is compile-time evaluable;
  the result is a constant). An explicit
  `#[reproducible(scope = "portable")]` is permitted but redundant.
- **`#[pure]`** — every `const fn` is implicitly pure (the body
  cannot consult any capability — capabilities are runtime
  values). Explicit `#[pure]` is permitted but redundant.
- **`#[budget]`** — `const fn` calls in default-argument position
  count for *zero* `allocs` / `io_calls` because the result is a
  compile-time constant. They count for `instructions` only at the
  runtime call site (when the `const fn` is called with non-const
  arguments).
- **`#[taint]`** — a `const fn` cannot receive a tainted input
  because tainted values are runtime constructs. The annotation is
  meaningless on a `const fn` parameter (`W0903`).
- **`#[stability]`** — applies normally; a `const fn` declaration
  is part of the public surface like any other function.

The discipline: `const fn` is a *compile-time* construct, while
v0.6's annotation surface is mostly about *runtime* contracts.
Most annotations are vacuously true on `const fn` and the formatter
omits redundant ones from rendered docs.

### 3.2 Variables

```osty
let x = 5                        // type inferred
let y: Int = 5                   // type annotated
let mut z = 0                    // mutable
let (a, b) = makeTuple()         // tuple destructuring
let (_, b) = makeTuple()         // wildcard
let User { name, age } = getUser()   // struct destructuring
let User { name, .. } = getUser()    // ignore rest
```

Patterns in `let`:
- Identifier bindings
- Tuple destructuring
- Struct destructuring (field shorthand, `..` for rest)
- Wildcard `_`

Enum-variant patterns are not permitted in `let`; use `match` or
`if let`.

#### 3.2.1 Variables and v0.6 surfaces

`let` bindings interact with the v0.6 annotation surface in several
predictable ways:

- **Capability binding** — `let net: Net = ...` is an ordinary
  reference-typed binding. The binding's `mut`-ness has no effect
  on the underlying capability (capabilities are interface values
  with shared internal state).
- **Flow-tagged binding** — `let raw: #[taint("user_input")]
  String = ...` annotates the binding's *type*. The annotation
  cannot be added by `let`; it must already be present on the RHS
  expression. (Authors who want to *add* a tag at a binding site
  use a passthrough function annotated `#[taint]` and call it from
  `let`.)
- **`let mut`** — for capability-typed bindings, `mut` allows
  rebinding to a *different* capability instance. The mutation
  goes through the binding, not through the underlying value:

  ```osty
  let mut clock: Clock = systemClock
  if testMode {
      clock = std.capability.testing.FakeClock(epoch_ms = 0)
  }
  ```
- **Tuple/struct destructuring** preserves flow tags element-wise.
  `let (a, b) = pair` where `pair: (#[taint("user_input")] String,
  #[taint("env_input")] String)` produces `a: #[taint("user_input")]
  String` and `b: #[taint("env_input")] String`.

#### 3.2.2 Top-level `let`

A `pub let` at module scope declares a *constant value* — the RHS
is evaluated once at module load time. Restrictions:

- The RHS must be a `DefaultLiteral` or a `const fn` call (§3.1.1).
  Runtime computation at module scope is `E0719`.
- `pub let` participates in the public API surface (§3.14.3) — its
  type is part of the package's exported signature.
- `let` (without `pub`) at top level is package-private; it is
  rare and used for derived constants reused inside the package.

Top-level `let` with capability-typed values is **not allowed** —
capabilities require runtime construction (host adapter factories
are themselves runtime functions). The pattern is to construct
capabilities inside `fn main` or via an `#[ambient]` entry point:

```osty
// ❌ E0719 — runtime evaluation at module scope.
pub let prodClock: Clock = time.systemClock

// ✅ Capability is local to main / passed explicitly.
#[ambient(clock)]
fn main() {
    runApp(clock)
}
```

Top-level `let` may be marked `pub`:

```osty
pub let MAX_USERS = 10000
```

### 3.3 Multiple Assignment

```osty
let mut a = 1
let mut b = 2

(a, b) = (b, a)                  // swap
(a, _) = makePair()              // assign first only
```

#### 3.3.1 Tuple destructuring rules

The LHS must be a tuple pattern; element count must match the RHS
tuple. Mismatch is `E0710`. Each LHS slot must be one of:

- A mutable identifier (declared with `let mut`).
- The wildcard `_` (skip).
- A field-access form (`obj.field`) where the receiver is mutable.
- An index form (`xs[i]`) where the collection is mutable.

Nested tuple patterns are not permitted (`((a, b), c) = ...` is
`E0711`). Authors who need nested destructuring use `let` with a
nested pattern, not multiple assignment.

#### 3.3.2 Multiple assignment and v0.6 surfaces

Multiple assignment behaves identically per slot to the single-
assignment rules. Specifically:

- **Capability bindings** — a `mut`-declared capability binding
  may be reassigned via tuple LHS. Reassignment swaps the
  underlying capability instance through that binding.
- **Flow tags** — each assignment slot inherits the corresponding
  RHS element's tag set. Tagged values flow through tuple
  destructuring without declassification.
- **Reproducibility** — multiple assignment inside a
  `#[reproducible]` function follows the same scope rules as
  single assignment; no special exemption.

### 3.4 Structs

```osty
pub struct User {
    pub name: String,
    pub age: Int,
    email: String,

    pub fn new(name: String, email: String) -> User {
        User { name, age: 0, email }
    }

    pub fn greet(self) -> String {
        "hi, {self.name}"
    }

    fn setAge(mut self, age: Int) {
        self.age = age
    }
}
```

A method whose first parameter is `self` or `mut self` is an instance
method. Methods without `self` are associated functions.

Fields, methods, and the struct itself may each be marked `pub`
independently.

**Field initialization shorthand:** `{ name }` means `{ name: name }`
when a binding of that name is in scope.

**Update syntax.** `..expr` copies fields from an existing value:

```osty
let user = User { name: "alice", age: 30, email: "a@x.com" }
let older = User { ..user, age: 31 }
let rebranded = User { ..user, email: "new@x.com", name: "Alice" }

let renamed = user { name: "Alice" }       // shorthand for User { ..user, name: "Alice" }
```

The `..expr` form must appear once per struct literal. Fields explicitly
listed override copied values. All fields must either be listed or
supplied by the spread source.

The receiver shorthand `value { field: newValue }` is accepted when
`value` is an in-scope struct-typed binding; it desugars to
`Type { ..value, field: newValue }`.

**Partial declarations.** A struct may be declared across multiple files
within the same package. Fields appear in exactly one declaration;
methods may be spread across any number of declarations.

```osty
// user.osty
pub struct User {
    pub name: String,
    pub age: Int,
    email: String,

    pub fn greet(self) -> String { ... }
}

// user_admin.osty (same package)
pub struct User {
    pub fn promote(mut self) { ... }
    pub fn demote(mut self) { ... }
}
```

Rules:
- All declarations must agree on type parameters and visibility.
- Exactly one declaration may contain fields.
- Method names must be unique across all declarations.
- Cross-package extension is not permitted.
- **Annotations are scoped to the declaration that physically contains
  them.** Because each field appears in exactly one declaration and each
  method name appears in exactly one declaration, an annotation has a
  single, unambiguous attachment site. The compiler does not synthesize
  cross-file annotation merging.

The same rules apply to `enum`.

**Auto-derived members.** The compiler automatically provides on every
`struct`:

1. `Type.default() -> Type` — available when every field has either an
   explicit default or a zero-value. `T?` defaults to `None`;
   collections default to empty. If any field lacks both, `default()`
   is not generated.

2. `Type.builder() -> Builder<Type>` — available when every private
   field has an explicit default. The generated builder exposes setters
   only for `pub` fields; private fields are filled from their defaults
   at `.build()` time.

3. `value.toBuilder() -> Builder<Type>` — available on any struct where
   `builder()` is generated. Returns a builder preloaded with all
   current field values.

The builder's `.build()` method requires that every `pub` field without
a default has been set. This is **enforced at compile time** — an
attempt to call `.build()` before all required fields are set produces
a dedicated diagnostic that names the missing fields:

```
error: cannot call build(): required fields not set
  --> foo.osty:42:22
   |
42 |   HttpConfig.builder().build()
   |                        ^^^^^ missing: url
   = help: set with `.url(<value>)` before calling `.build()`.
```

The compiler tracks set/unset status through internal type parameters
on `Builder<T>`. These parameters are deliberately **not exposed** in
the language surface: users see only `Builder<T>` in error messages and
cannot construct, name, or destructure them manually. A `Builder<T>` is
therefore usable only via the generated API — chained `.fieldName(...)`
calls terminated by `.build()`.

```osty
pub struct HttpConfig {
    pub url: String,                          // required pub
    pub method: String = "GET",               // optional pub
    pub timeout: Int = 30,                    // optional pub
    headers: Map<String, String> = {:},       // private, has default
}

let cfg = HttpConfig.builder()
    .url("api.com")
    .build()

let custom = HttpConfig.builder()
    .url("api.com")
    .method("POST")
    .timeout(60)
    .build()

let variant = cfg.toBuilder()
    .timeout(120)
    .build()

HttpConfig.builder().build()
// ERROR: url was not set
```

Visibility rules:
- Setters exist only for `pub` fields.
- A struct whose private fields lack defaults has no auto-generated
  builder.

```osty
pub struct AuthToken {
    value: String,        // private, no default
    issuer: String,       // private, no default

    pub fn signAndCreate(payload: String, key: Key) -> Self {
        Self { value: sign(payload, key), issuer: key.owner }
    }
}

AuthToken.builder()   // ERROR: no builder generated
```

**Override.** If the user defines `default`, `builder`, or `toBuilder`
on the type, the user's definition replaces the auto-generated one.

#### 3.4.4 Struct method receivers

Struct method declarations take their receiver in the first
parameter slot, with one of three shapes:

```osty
pub struct User {
    name: String,
    email: Email,

    // Borrowing — the most common shape. Body cannot mutate `self`.
    pub fn greet(self) -> String {
        "hi, {self.name}"
    }

    // Mutating — body may write through `self.field` and call
    // `mut self` methods. The receiver is the struct itself; there
    // is no separate "this".
    pub fn rename(mut self, newName: String) {
        self.name = newName
    }

    // Static (no receiver) — the method is namespaced under the
    // struct name but has no implicit `self`. Used for constructors
    // and free helpers tightly tied to the type.
    pub fn fromDsn(dsn: String) -> Result<User, Error> {
        ...
    }
}
```

`self` and `mut self` are *contextual keywords* (§1.3) — they are
identifiers in any other position. Type annotations are not
permitted on the receiver: `fn greet(self: User)` is `E0716`.

A method declared `mut self` may be called only through a `mut`
binding or a `mut` field. Calling a `mut self` method on an
immutable receiver is `E0717`.

### 3.4.5 `#[sealed_construct]` — Parse-don't-validate primitive (G40)

A `struct` may declare `#[sealed_construct(name)]` to restrict
construction paths to a single named constructor. This makes
"a value of this type exists" *equivalent* to "the validating
constructor accepted the input."

```osty
#[sealed_construct(parse)]
pub struct Email {
    local: String,
    domain: String,
}

impl Email {
    pub fn parse(s: String) -> Email? { ... }
    pub fn local(self) -> String { self.local }
    pub fn domain(self) -> String { self.domain }
}
```

**Forbidden construction paths.** The following all fail with `E0420`
when applied to a sealed struct from outside the named constructor:

1. External struct literal: `Email { local: "a", domain: "b" }`
2. Spread update: `Email { ..existing, domain: "x" }`
3. Direct field mutation (when the struct has `mut` fields)
4. Generic deserialise / FFI default construction (must route through
   the constructor — `#[json(constructor = parse)]` registers the path)
5. Test helpers in production builds (use `#[test_construct]` to opt
   into a test-only escape — production reachability is `E0421`)

**Stdlib escape: `#[trusted_construct(reason = "...")]`.** Restricted
to `std.*` and toolchain-internal packages (`E0422` from user code). All
sites are enumerated by `osty audit --trusted-construct`.

#### 3.4.5.1 Constructor signature requirements

The named constructor — `parse` in the example above — must satisfy
three rules at definition time:

1. **It resolves to an associated constructor on the sealed type.** The
   named item must exist on the struct's method set and must not take
   `self`, `mut self`, or `&self`; a missing name or instance method is
   `E0423`.
2. **It returns the sealed type** (or `Result<Self, _>` / `Self?`).
   Returning a wrapper or a generic `Self?` not parameterized on the
   sealed type is `E0423`.
3. **It is package-public** if the struct itself is `pub`. A `pub
   struct` with a non-`pub` constructor is `E0424` — external code
   needs *some* path to construct values, otherwise the type would be
   uninhabitable to importers.
4. **It accepts only ordinary parameters** — no `Self` / `mut self` /
   sealed-aware special args. The constructor reaches the body via
   normal call resolution and is not allowed to short-circuit
   sealed-construction checks via reflection.

A struct may declare *multiple* `#[sealed_construct(name)]` annotations
to register more than one validating constructor. Each name must
satisfy the three rules independently:

```osty
#[sealed_construct(parse)]
#[sealed_construct(fromBytes)]
pub struct Sha256Digest {
    bytes: Bytes,
}

impl Sha256Digest {
    pub fn parse(hex: String) -> Sha256Digest? { ... }
    pub fn fromBytes(b: Bytes) -> Sha256Digest? { ... }
}
```

#### 3.4.5.2 Round-trip with `ToString`

Stdlib v0.6 sealed types (`Email`, `Url`, `Path`, `SqlIdent`,
`Duration`, `Uuid`) maintain the contract `parse(value.toString())?
== Some(value)` for every value the constructor produces. User code
that defines a sealed type **should** uphold the same round-trip;
formalizing it via a `spec { law: ... }` clause (§3.13) gives the
checker a target:

```osty
#[sealed_construct(parse)]
pub struct Slug {
    text: String,

    pub fn parse(s: String) -> Slug? {
        spec {
            law: result.map(|sl| Slug.parse(sl.toString())) == Some(Some(result))
        }
        ...
    }

    pub fn toString(self) -> String { self.text }
}
```

The law is documentation in v0.6 baseline (§3.13.2 — Phase 3 v0
runs `example:` only). Phase 5 turns it into a property test.

#### 3.4.5.3 Information flow integration

A `#[sealed_construct(parse)]` constructor that performs full
validation should also register as a sanitizer through
`#[sanitizes("source", into = "trust")]`:

```osty
#[sanitizes("user_input", into = "url_safe")]
pub fn parse(s: String) -> Url? { ... }
```

Outputs of the parser then carry the `url_safe` trust tag, which is
what `#[requires("url_safe")]` sinks (§21.8) expect. Sealed
construction and sanitization compose orthogonally — sealed enforces
*who* can construct a value, sanitization records *which trust set*
the value carries.

### 3.5 Enums

```osty
pub enum Result<T, E> {
    Ok(T),
    Err(E),

    pub fn isOk(self) -> Bool {
        match self {
            Ok(_) -> true,
            Err(_) -> false,
        }
    }
}

pub enum Color {
    Red,
    Green,
    Blue,
    RGB(UInt8, UInt8, UInt8),
}

pub enum HttpStatus: Int {
    OK = 200,
    NotFound = 404,
}
```

Variants:
- Bare: `Red`
- Tuple-like: `RGB(UInt8, UInt8, UInt8)`
- Integer-discriminated: payload-free variants in an enum with an integer
  representation may assign explicit integer values.

Variant access: bare name within the same package, qualified from other
packages (`Color.Red`).

An enum with an explicit integer representation auto-derives
`.discriminant() -> Int` and `.fromDiscriminant(n: Int) -> Self?`.
Payload variants may not assign discriminants (`E0721`).

#### 3.5.1 `#[since]` on variants

A variant may carry `#[since("X.Y")]` to record when it was added.
The annotation is consumed by `osty publish` (§3.14.3) — adding a
`#[since]`-marked variant to a `#[stability("stable")]` enum is a
**major** SemVer bump because exhaustive `match` expressions on the
old enum become non-exhaustive.

```osty
pub enum HttpEvent {
    Get,
    Post,

    #[since("0.7")]
    Patch,                          // 도착 예정 v0.7
}
```

Callers that exhaustively match `HttpEvent` should reach for
`#[match_compat("0.6", fallback = name)]` (§3.14.4) to absorb the
new variant on upgrade. The compiler emits `W0413` (dead-per-
contract) for arms that target a variant *added later than the
caller's `#[match_compat]` pin*.

#### 3.5.2 Variant payloads and information flow

A variant payload is an ordinary value — flow tags ride through
construction and pattern-matching identically:

```osty
pub enum FormResult {
    Accepted(UserId),
    Rejected(#[taint("user_input")] String),  // payload-tagged at site
}

fn handle(req: HttpRequest) -> Response {
    let raw = req.queryParam("name") ?? ""    // String #[taint("user_input")]
    let r = FormResult.Rejected(raw)          // payload carries the tag

    match r {
        FormResult.Rejected(msg) -> {
            // `msg` is #[taint("user_input")] String
            log.warn("rejected: {std.html.escape(msg)}")     // sanitize before sink
        },
        FormResult.Accepted(id) -> ...,
    }
}
```

The pattern `FormResult.Rejected(msg)` binds `msg` with the same
flow-tag set the payload carried at construction time. The checker
tracks tags through both construction and destructuring without a
special rule.

#### 3.5.3 Methods inside enum bodies

Methods declared inside an enum body apply to *every variant* — the
same surface as struct methods (§3.4) but receiving a sum-typed
`self`:

```osty
pub enum Shape {
    Circle(Float),
    Rect(Float, Float),
    Empty,

    pub fn area(self) -> Float {
        match self {
            Circle(r) -> 3.14159 * r * r,
            Rect(w, h) -> w * h,
            Empty -> 0.0,
        }
    }
}
```

Methods inside the enum body may be `pub` (exported), private, or
have `mut self` for mutation through the receiver. Capability
parameters work like any other parameter: `fn render(self, console:
Console)` is a perfectly normal enum method.

#### 3.5.4 `#[error_contract]`-eligible enum

An enum used as the `E` parameter of `Result<T, E>` may participate
in `#[error_contract]` (§7.5). The contract enumerates which
variants flow out under which conditions; the type checker uses the
contract to prune match exhaustiveness on the caller side.

A `#[error_contract]`-eligible enum has no special declaration
syntax — any concrete enum suffices. The contract is on the
*function* that returns `Result<T, ThisEnum>`, not on the enum
declaration itself. This separation lets the same enum back
multiple functions with different contracts (subsetting the variant
list per-function).

### 3.6 Interfaces

```osty
pub interface Writer {
    fn write(self, data: Bytes) -> Result<Int, Error>
    fn flush(self) -> Result<(), Error>
}

// 사용자 정의 capability — `#[reproducible_capability]` 가 모든 메서드의
// `#[reproducible]` 부착을 강제 (§20.5).
#[reproducible_capability]
pub interface Hash {
    #[reproducible(scope = "portable")]
    fn hash(self, data: Bytes) -> Bytes32
}
```

See §2.6 for the full structural-typing rules and §20 for the canonical
capability set (`Clock`, `Rng`, `Env`, `Fs`, `Net`, `Process`,
`Console`).

#### 3.6.1 Default method implementations

An interface method may carry a body — a *default implementation* —
that types satisfying the interface inherit when they do not provide
their own. This avoids duplicating shared default logic across every
implementer:

```osty
pub interface Reader {
    fn read(self, maxBytes: Int) -> Result<Bytes, Error>

    // Default — derived from `read`.
    fn readAll(self) -> Result<Bytes, Error> {
        let mut acc = Bytes.empty()
        for {
            let chunk = self.read(4096)?
            if chunk.isEmpty() { break }
            acc = acc.concat(chunk)
        }
        Ok(acc)
    }
}
```

A concrete type that implements `Reader` only needs to provide
`read`; `readAll` is inherited. Overriding the default by providing
the method body in the concrete type is permitted and is the path
when a more efficient implementation exists (e.g. a `Bytes`-backed
reader can return its full payload in one shot).

Default implementations may not call methods that the interface does
not define (no escaping the structural envelope). Defaults that
require additional state or capabilities should instead be exposed
as ordinary helpers on the implementing type.

#### 3.6.2 Interface composition (`Reader + Writer`)

Interfaces compose by declaring multiple parent interface names in
the body — an interface that lists `Reader` and `Writer` is the
union of both contracts:

```osty
pub interface ReadWriter {
    Reader
    Writer
}

pub interface ReadCloser {
    Reader
    Closer
}

pub interface BufferedReader {
    ByteReader
    LineReader
}
```

A concrete type satisfies `ReadWriter` iff it satisfies both `Reader`
and `Writer` structurally. There is no diamond-inheritance hazard
because Osty has no inheritance — every method in the composed
interface is part of a single flat method set.

Interface composition is *deeply structural*. Adding a method to a
parent interface (e.g. `Reader`) makes every dependent composed
interface (`ReadWriter`, `ReadCloser`, …) require the new method
too. This is intentional — interfaces describe contracts, not
hierarchy.

#### 3.6.3 Interface as parameter — value vs generic

Two ways to take an interface-typed parameter:

```osty
// Generic — monomorphized; one specialized body per concrete T.
fn copyGen<R: Reader, W: Writer>(src: R, dst: W) -> Result<Int, Error> { ... }

// Interface value — single body, fat-pointer dispatch through vtable.
fn copyDyn(src: Reader, dst: Writer) -> Result<Int, Error> { ... }
```

The generic form runs faster (no vtable indirection, inlining
opportunities) but compiles each call site separately. The interface-
value form is what Rust calls `dyn Trait` — a fat pointer of (data,
vtable). Picking between them:

| Constraint | Choose |
|---|---|
| Hot path / small method set / known concrete types at most call sites | Generic |
| Heterogeneous collection (`List<Reader>` of mixed concrete types) | Interface value |
| Cross-package API (the function is published; binary size matters) | Interface value |
| Capability parameters | Interface value (capabilities are interfaces; `Net` etc. are passed as fat pointers in production builds) |

The mix is permitted on the same parameter list: a function may
take a generic `T: Reader` and an interface-value `Writer` in the
same signature.

#### 3.6.4 `#[reproducible_capability]` — deterministic interface

`#[reproducible_capability]` (§20.5) on an interface declaration
asserts that *every* method on the interface is `#[reproducible]`
at some scope. The compiler enforces this at the interface
definition: a method body that omits `#[reproducible(...)]` is
`E0783`.

```osty
#[reproducible_capability]
pub interface Hash {
    #[reproducible(scope = "portable")]
    fn hash(self, data: Bytes) -> Bytes32

    // ❌ E0783 — interface annotated #[reproducible_capability]
    //    but this method has no #[reproducible].
    fn salt(self) -> Bytes
}
```

A `Hash` value can therefore be received inside a `#[reproducible]`
function — the type checker knows every call goes to a deterministic
method, so the function's reproducibility contract holds.

Stdlib v0.6 baseline `#[reproducible_capability]` interfaces:

| Interface | Methods | Scope |
|---|---|---|
| `Hash` | `hash(self, data)` → `Bytes32` | `portable` |
| `Encoder<T>` | `encode(self, value)` → `Bytes` | `portable` |
| `Decoder<T>` | `decode(self, bytes)` → `Result<T, Error>` | `portable` |

Implementers must annotate every method with the same scope or
stronger. A weaker-scope method on a `#[reproducible_capability]`
interface is `E0783`; a method body that violates its declared
reproducibility scope is checked by the ordinary reproducibility
diagnostics (`E0786`–`E0788`).

#### 3.6.5 Interface 진화 rules

`#[stability("stable")]` interfaces follow standard SemVer rules
(§3.14.3) plus an interface-specific clause:

| Change | Stable interface | Experimental interface |
|---|---|---|
| Add a method *with* default body | minor bump (additive — implementers don't need changes) | patch bump |
| Add a method *without* default body | major bump (existing implementers fail compilation) | minor bump |
| Remove a method | major bump | minor bump |
| Change a method signature | major bump | minor bump |
| Change a default body | patch bump (semantic-only change) | patch bump |
| Promote `#[reproducible_capability]` | major bump (existing impls may need new annotations) | minor bump |

The "add method *with* default body" rule is the canonical *minor
bump* path for interface evolution — it lets stdlib add helper
methods to `Reader` / `Writer` without breaking downstream
implementers.

### 3.7 Type Aliases

```osty
type UserMap = Map<String, List<User>>
type Handler = fn(Request) -> Result<Response, Error>

pub type Pair<T> = (T, T)
```

Aliases are transparent; they create no new type.

#### 3.7.1 Type aliases and v0.6 surfaces

Type aliases interact with the v0.6 annotation surface in the
following ways:

**Capability aliases.** An alias for a capability bag struct is
common in workspaces with many capability-typed parameters:

```osty
pub type AppCaps = (Clock, Rng, Env, Fs, Net, Console)

fn run(caps: AppCaps) -> Result<(), Error> {
    let (clock, rng, env, fs, net, console) = caps
    ...
}
```

Tuple aliases are convenient but lose the named-field readability
of a `struct` bag (§20.17.1). For more than 3 capabilities, prefer
a struct.

**Flow-tagged aliases.** Aliases preserve flow tags transparently.
A type alias `type UserText = String` admits the same tag rules as
`String` directly — there is no implicit decoration applied by the
alias.

**`#[stability]` on aliases.** A `pub type` participates in the
public API surface. Changing the RHS of a `pub type` is:

| Change | `stable` alias | `experimental` alias |
|---|---|---|
| RHS expanded to a wider type (e.g. `Map<String, Int>` → `Map<String, Cell>` where `Cell.fromInt` works) | major bump | minor bump |
| RHS narrowed | major bump | minor bump |
| RHS structurally compatible reorganization (e.g. inlining a sub-alias) | patch bump | patch bump |

The compatibility rules are cosmetic — Osty's structural type
system means an alias's *meaning* is its RHS, and any change to
the RHS is a SemVer-relevant change at the level of the alias's
clients.

#### 3.7.2 Generic type aliases

A `pub type` may carry generic parameters:

```osty
pub type Result_<T> = Result<T, AppError>      // app-specific Result alias
pub type Cache<K> = Map<K, CacheEntry<V>>      // bound K, V via context
```

The generic parameters appear in the alias's surface; adding /
removing them follows generic struct evolution rules. Aliases do
not introduce monomorphization themselves — they expand at use
sites and the underlying generic struct is monomorphized.

### 3.8 Annotations

Osty has a fixed, compiler-recognized set of annotations. Applying any
other annotation is a compile error; there is no user-extension
mechanism. The complete set is:

**User-facing annotations.**

| Annotation | Applies to | Purpose |
|---|---|---|
| `#[json(...)]` | struct fields, enum variants | Customize JSON encoding/decoding (§10.8) |
| `#[deprecated(...)]` | `fn`, `struct`, `enum`, `interface`, `type`, top-level `let`, struct/enum methods, struct fields, enum variants | Emit a warning when the item is referenced |
| `#[op(...)]` | struct/enum methods | Opt-in overload for the bounded arithmetic operator set: `+ - * / %` binary and `-` unary (§14.2) |
| `#[cfg(...)]` | top-level declarations, struct/enum methods, struct fields, enum variants | Conditional compilation pre-resolve filter; keys are `os`, `target`, `arch`, and `feature`, with `all` / `any` / `not` composition (§14.3, §18 G29) |
| `#[test]` | top-level zero-arity `fn` declarations | Inline test function collected by `osty test` and excluded from production builds (§11) |
| `#[vectorize]` | top-level `fn` declarations, struct/enum methods | **Default-on as of v0.6** — no annotation needed for the hint. Bare `#[vectorize]` is a no-op; `#[vectorize(scalable, predicate, width = N)]` refines strategy (§3.8.3) |
| `#[no_vectorize]` | top-level `fn` declarations, struct/enum methods | Opt out of the default vectorize hint. Restores per-iteration safepoint polls (v0.6 A5.2) |
| `#[parallel]` | top-level `fn` declarations, struct/enum methods | Hint: every load/store in the body tagged with `!llvm.access.group`, every loop's metadata references it via `llvm.loop.parallel_accesses` to bypass alias analysis (v0.6 A6) |
| `#[unroll]` | top-level `fn` declarations, struct/enum methods | Hint: `llvm.loop.unroll.enable` on every loop in the body; `#[unroll(count = N)]` forces a specific unroll factor (v0.6 A7) |
| `#[inline]` / `#[inline(always)]` / `#[inline(never)]` | top-level `fn` declarations, struct/enum methods | LLVM `inlinehint` / `alwaysinline` / `noinline` fn attribute (v0.6 A8) |
| `#[hot]` / `#[cold]` | top-level `fn` declarations, struct/enum methods | LLVM `hot` / `cold` fn attribute + `.text.hot` / `.text.unlikely` section placement (v0.6 A9) |
| `#[target_feature(f1, f2, ...)]` | top-level `fn` declarations, struct/enum methods | LLVM `"target-features"="+f1,+f2"` fn attribute — per-function CPU feature override (v0.6 A10) |
| `#[noalias]` / `#[noalias(p1, p2)]` | top-level `fn` declarations, struct/enum methods | Promise pointer params do not alias — emits LLVM `noalias` param attr (v0.6 A11) |
| `#[pure]` | top-level `fn` declarations, struct/enum methods | Assert no side effects — emits LLVM `readnone` fn attr, unlocks CSE/hoisting (v0.6 A13) |

`const fn` is a declaration prefix, not an annotation; see §3.1.1.

**Runtime-only annotations** (privileged packages only — see §19.2 and §19.6).

| Annotation | Applies to | Purpose |
|---|---|---|
| `#[intrinsic]` | `fn` declarations | Body is supplied by the lowering layer; source body must be empty (§19.5). Generic intrinsics participate in monomorphization. |
| `#[pod]` | `struct` declarations | Requests the checker to verify the struct's `Pod` shape (§19.4); rejection is `E0771`. |
| `#[repr(c)]` | `struct` declarations | Forces C ABI field order, padding, and alignment (§19.6). |
| `#[export("name")]` | top-level `fn` declarations | Emit with the exact symbol name `name`, disabling Osty mangling (§19.6). |
| `#[c_abi]` | top-level `fn` declarations | Use the platform C calling convention (§19.6). |
| `#[no_alloc]` | `fn` and method declarations | Forbid managed allocation in the body, and forbid any direct or transitive call to a function that allocates (§19.6.1). |

Applying any runtime-only annotation outside a privileged package is
`E0770`, not the generic unknown-annotation error.

Syntax is defined in §1.9. Both key/value (`name = value`) and bare-flag
(`name`) argument forms are accepted.

#### 3.8.1 `#[json]`

Valid on `struct` fields and `enum` variants.

| Arg | Form | Default | Effect |
|---|---|---|---|
| `key` | `key = "<name>"` | source-level name | Rename the JSON key used for this field or variant tag |
| `skip` | flag (or `skip = true`) | absent | Exclude this field/variant from both encoding and decoding |
| `optional` | flag (or `optional = true`) | absent | For `T?` fields only — omit the JSON key when the value is `None` (default behavior emits `"key": null`) |

Multiple arguments may be combined:

```osty
pub struct User {
    #[json(key = "user_id")]
    pub userId: String,

    #[json(key = "email_address")]
    pub email: String,

    #[json(key = "phone", optional)]
    pub phone: String?,

    #[json(skip)]
    cachedHash: Int,
}

pub enum Shape {
    #[json(key = "circle")]
    Circle(Float),

    #[json(key = "rect")]
    Rectangle(Float, Float),
}
```

Constraints:
- `optional` is valid only on fields of type `T?`. Using it on a non-
  optional field is a compile error.
- `skip` is mutually exclusive with `key` and `optional` (skipped
  fields/variants have no JSON identity).
- Applying `#[json(...)]` outside a struct field or enum variant is a
  compile error.

#### 3.8.2 `#[deprecated]`

Valid on any named declaration listed in the §3.8 table — including
struct fields and enum variants.

| Arg | Form | Default | Effect |
|---|---|---|---|
| `since` | `since = "<version>"` | none | Version string; shown in the warning |
| `use`   | `use = "<name>"` | none | Name of the recommended replacement |
| `message` | `message = "<text>"` | none | Free-form explanation shown in the warning |

All arguments are optional; any combination is permitted. Referencing
a deprecated item produces a compiler warning that reproduces the
supplied arguments. The warning is anchored at the use-site; for
deprecated **fields**, this means each read or write of the field;
for deprecated **variants**, each construction or pattern match.

```osty
#[deprecated(since = "0.5", use = "loginV2")]
pub fn login(user: String, pass: String) -> Result<Session, Error> { ... }

#[deprecated(message = "replaced by ConfigV2")]
pub type LegacyConfig = Map<String, String>

#[deprecated]
pub let API_BASE_URL = "https://old.example.com"

pub struct User {
    #[deprecated(since = "0.7", use = "primaryEmail")]
    pub email: String,
    pub primaryEmail: String,
}

pub enum Status {
    Active,
    #[deprecated(message = "use Inactive(reason: \"unknown\") instead")]
    Inactive,
    Banned(String),
}
```

Deprecation warnings may be promoted to errors by build configuration;
they are not errors by default. Deprecation does **not** propagate
transitively: annotating a type as `#[deprecated]` does not deprecate
its methods, its fields, or types that reference it. Each target
carries its own annotation.

#### 3.8.3 Vectorize (default on)

**v0.6 A5.2 flip: vectorize is the default.** Every function's user-
written `for` loops receive `!llvm.loop !N` metadata with
`!"llvm.loop.vectorize.enable", i1 true`, and every function opts out
of the per-iteration GC loop safepoint poll, *without* the user typing
anything. This applies across the whole program — the stdlib, user
code, even compiler-synthesized surface (see §3.8.6 for the GC
contract that makes this safe).

The user types an annotation only to deviate from the default:

- `#[no_vectorize]` — opt out entirely. Restores per-iteration safepoint
  polls and suppresses the loop metadata. For long-running worker
  loops that must yield to GC mid-loop.
- `#[vectorize(scalable, predicate, width = N)]` — keep the default
  but refine the strategy. Bare `#[vectorize]` is accepted as a no-op
  (documents intent) and is equivalent to typing nothing.

Vectorize is a *hint*, not a guarantee. LLVM's loop vectorizer still
performs legality and profitability analysis; loops the cost model
rejects simply run scalar. The annotation set does not introduce new
syntax, does not change function types, and does not affect observable
behavior on correct programs — an unvectorized build produces the
same outputs as a vectorized one.

| Arg | Form | Default | LLVM property | Effect |
|---|---|---|---|---|
| `scalable` | flag | absent | `llvm.loop.vectorize.scalable.enable=true` | Prefer scalable vector ISAs (ARM SVE, RISC-V RVV) over fixed-width (NEON) on targets that support both. macOS/iOS aarch64 do not expose SVE, so Apple Silicon falls back to fixed-width NEON even when this hint is present |
| `predicate` | flag | absent | `llvm.loop.vectorize.predicate.enable=true` | Enable tail folding — process trip counts that are not a multiple of the vector width via masked ops instead of a scalar tail loop. Biggest win on SVE / AVX-512 / RVV with native mask registers |
| `width` | `width = <1..1024>` | compiler chooses | `llvm.loop.vectorize.width, i32 N` | Force a specific vectorization factor. Can unlock AVX-512 ZMM registers on Intel, where the default cost model otherwise prefers 256-bit YMM because of historical downclocking. The LLVM cost model may still override this hint; use `-mllvm -force-vector-width=N` at the build-time flag level when the override is required. Future target profiles may inject that flag for AVX-512 server presets |

```osty
// Default: no annotation needed. Every loop gets vectorize metadata
// and the function opts out of per-iteration safepoints.
pub fn sumTo(n: Int) -> Int {
    let mut acc = 0
    for i in 0..n {
        acc = acc + i
    }
    acc
}

// Power-user override: prefer scalable ISA, fold the tail, and hint
// a wide fixed factor for AVX-512 / wide SVE targets.
#[vectorize(scalable, predicate, width = 8)]
pub fn xorTo(n: Int) -> Int {
    let mut acc = 0
    for i in 0..n {
        acc = acc ^ i
    }
    acc
}

// Explicit opt-out: long-running worker loop that needs to yield to
// a concurrent STW request mid-loop.
#[no_vectorize]
pub fn drain(queue: Queue) {
    for job in queue {
        process(job)
    }
}
```

Unknown keys, duplicate keys, and out-of-range widths are rejected
with `E0739` (`CodeAnnotationBadArg`).

Scope rules:

- The tuning arguments are **function-scoped**. Every function receives
  the default vectorize metadata unless it carries `#[no_vectorize]`,
  but `scalable`, `predicate`, and `width = N` apply only to loops in
  the function where `#[vectorize(...)]` appears.
- Only loops originating from a user-written `for` statement carry the
  hint. Loops synthesized by the compiler (e.g. the per-iteration
  scaffold inside `testing.benchmark`, or the key-snapshot traversal
  inside map-mutating helpers) do not.
- Iterator-protocol loops (`for x in iter` where `iter` is not a
  `List<T>`, range, or `Map<K, V>`) currently lower through a
  callback-driven shape that LLVM cannot prove countable; the hint is
  attached but the vectorizer will reject them. This is documented in
  `SPEC_GAPS.md` under `vectorize-hint`.

**GC contract.** Shared by `#[vectorize]`, `#[parallel]`, and
`#[unroll]`. See §3.8.6.

#### 3.8.4 `#[parallel]`

Valid on top-level `fn` declarations and on struct/enum methods. Bare
flag. Status: **v0.6 A6**.

Asserts that memory accesses inside every loop in the annotated
function body are parallel — that is, iterations do not read and
write the same memory location in a way that creates loop-carried
dependencies. The LLVM backend materialises this promise as:

1. One per-function `!llvm.access.group` metadata node: `!N = distinct !{}`.
2. Every load and store instruction in the body tagged with
   `!llvm.access.group !N`.
3. A `!"llvm.loop.parallel_accesses", !N` property on every loop's
   back-edge metadata, letting the vectorizer bypass its default
   alias analysis for accesses tagged with the same group.

Composes with `#[vectorize(...)]` — in fact `#[parallel]` is often the
prerequisite for `#[vectorize]` to fire on real code, because the
loop vectorizer conservatively refuses to vectorize when it can't
prove absence of aliasing.

```osty
#[parallel]
#[vectorize(scalable)]
pub fn addInto(dst: List<Int>, src: List<Int>) {
    for i in 0..dst.len() {
        dst[i] = dst[i] + src[i]
    }
}
```

**Soundness is the programmer's responsibility.** If the loop body
actually does have loop-carried dependencies, the resulting code may
produce different values than the scalar version. The annotation is a
contract with the compiler, not a check. Use it only on loops whose
iteration order genuinely does not matter for the computed result.

No arguments are permitted; any argument is rejected with `E0739`.

#### 3.8.5 `#[unroll]`

Valid on top-level `fn` declarations and on struct/enum methods.
Status: **v0.6 A7**.

Emits `llvm.loop.unroll.enable` on every loop in the body (bare form)
or `llvm.loop.unroll.count, i32 N` with an explicit factor. Independent
of `#[vectorize]` — unrolling controls how many original iterations
are inlined into the body, separately from the vectorizer's
vectorization factor. The two hints compose: an annotated function
can be both vectorized and unrolled (effective throughput ≈ width ×
unroll count).

| Form | LLVM property | Effect |
|---|---|---|
| `#[unroll]` | `llvm.loop.unroll.enable=true` | Request unrolling; compiler picks factor based on target |
| `#[unroll(count = N)]` | `llvm.loop.unroll.count, i32 N` | Force an exact unroll factor (1..1024) |

```osty
#[vectorize]
#[unroll(count = 4)]
pub fn sumSquares(n: Int) -> Int {
    let mut acc = 0
    for i in 0..n {
        acc = acc + i * i
    }
    acc
}
```

Unknown keys, negative or zero counts, and out-of-range values are
rejected with `E0739` (`CodeAnnotationBadArg`).

#### 3.8.6 GC contract for loop-optimization annotations

`#[vectorize]`, `#[parallel]`, and `#[unroll]` all require that the
loop latch be free of side-effecting calls for LLVM's loop analyses
to see a well-formed countable loop. To honor this, **functions that
carry any of these three annotations opt out of the per-iteration GC
loop safepoint poll**. The function-entry safepoint still fires, and
the caller resumes its own safepoint cadence on return — so the
function is bracketed by polls on both sides. But inside the
function, a long-running optimized loop does not yield to a
concurrent STW request until it completes.

This is an explicit tradeoff: optimized execution in exchange for
GC latency across the function body. Callers that need mid-loop
responsiveness should drive the work in smaller chunks from an
unannotated outer loop. The author opts in knowingly; the compiler
does not second-guess.

#### 3.8.7 `#[inline]` family

Valid on top-level `fn` declarations and on struct/enum methods.
Status: **v0.6 A8**. Controls the LLVM inliner's decision for the
annotated function.

| Form | LLVM fn attribute | Effect |
|---|---|---|
| `#[inline]` | `inlinehint` | Soft hint — inliner prefers inlining but can still refuse on size / call-count grounds |
| `#[inline(always)]` | `alwaysinline` | Hard force — inliner **must** inline at every call site. Compile error if inlining fails (e.g. recursion) |
| `#[inline(never)]` | `noinline` | Inliner must **not** inline. Useful to preserve a stable symbol for profiling or for breaking LTO cycles |

Composes freely with `#[hot]`, `#[cold]`, `#[vectorize(...)]`, and
the rest of the v0.6 annotation set. Unknown sub-flags are `E0739`.

```osty
#[inline(always)]
pub fn lenUnchecked<T>(xs: List<T>) -> Int { xs.rawLen() }

#[inline(never)]
pub fn logBreakpoint(msg: String) {
    fmt.stderr.writeLine(msg)
}
```

#### 3.8.8 `#[hot]` / `#[cold]`

Bare-flag annotations on top-level `fn` declarations and struct/enum
methods. Status: **v0.6 A9**. Control the LLVM frequency attributes
and text-section placement.

| Annotation | LLVM fn attribute | Effect |
|---|---|---|
| `#[hot]` | `hot` | Function is frequently executed. Aggressive optimization; placed in the `.text.hot.` section so the linker can group hot code for i-cache locality |
| `#[cold]` | `cold` | Function is rarely executed (error paths, slow-path fallbacks). Size-optimized; placed in `.text.unlikely.`; call sites receive a branch-prediction-away hint |

The two are mutually exclusive on a single declaration — both together
is `E0609` (duplicate annotation) via the normal annotation rules.
Any argument on either is `E0739`.

```osty
#[hot]
pub fn fastPath(n: Int) -> Int { n * 2 }

#[cold]
pub fn errorReport(e: Error) -> Int {
    log.record(e)
    -1
}
```

#### 3.8.9 `#[target_feature(...)]`

Valid on top-level `fn` declarations and struct/enum methods. Status:
**v0.6 A10**. Overrides the LLVM backend's target-feature baseline
for just this one function, letting a library ship a SIMD-heavy routine
compiled for AVX-512 / SVE2 without forcing the whole program onto
that baseline.

Each bare-identifier argument names a CPU feature. The LLVM emitter
materialises them as a single `"target-features"="+f1,+f2"` fn
attribute (positive enable only in v0.6). Duplicate names are
rejected with `E0739`; positional values without a key and
`feature = "value"` shapes are also `E0739`.

```osty
// Only this one function compiles for AVX-512, even when the rest
// of the module targets baseline x86-64. Caller is responsible for
// CPU feature detection before dispatching here.
#[target_feature(avx512f, avx512bw)]
pub fn dotProductAVX512(xs: List<Int>, ys: List<Int>) -> Int {
    let mut acc = 0
    for i in 0..xs.len() {
        acc = acc + xs[i] * ys[i]
    }
    acc
}
```

**Safety contract.** The programmer is responsible for ensuring the
CPU running this function actually supports the declared features.
Calling a `#[target_feature(avx512f)]` routine on a CPU without
AVX-512 is undefined behavior at the hardware level (illegal
instruction fault). The usual pattern is a runtime-dispatch helper
that probes `cpuid` / `HWCAP` before calling the specialised variant.
Runtime dispatch is not built into v0.6; it belongs in the user's
application logic or a separate track.

Feature-name spelling matches LLVM's target-features vocabulary
(`avx2`, `avx512f`, `avx512bw`, `sve`, `sve2`, `neon`, `vfp4`, etc.).
Typos pass the resolver but will be ignored by the LLVM backend with
a warning at compile time — the resolver does not maintain a
per-target feature allowlist because it would need to evolve with
every LLVM release.

#### 3.8.10 `#[noalias]`

Valid on top-level `fn` declarations and on struct/enum methods.
Status: **v0.6 A11**. Promises pointer-typed parameters do not alias.

| Form | Effect |
|---|---|
| `#[noalias]` | Every `ptr`-typed parameter of this function gets the LLVM `noalias` parameter attribute |
| `#[noalias(p1, p2)]` | Only the named parameters get `noalias`; other pointer params stay potentially-aliasing |

Non-pointer parameters (Int, Bool, Float, ...) are silently skipped —
LLVM rejects `noalias` on non-pointer types.

**Why it matters.** LLVM's alias analyzer conservatively assumes two
pointer parameters can point at overlapping memory, which blocks
SROA, LICM, loop vectorization, and any transform that would reorder
loads and stores across the two. `noalias` tells the analyzer "these
pointers point at disjoint regions" and unlocks the full suite of
memory-dependency-based optimizations.

```osty
// Bare — the two slices are disjoint buffers.
#[noalias]
pub fn addInto(dst: List<Int>, src: List<Int>) {
    for i in 0..dst.len() {
        dst[i] = dst[i] + src[i]
    }
}

// Surgical — only `src` is guaranteed disjoint; `dst` and `scratch`
// may alias (e.g., they point into the same arena).
#[noalias(src)]
pub fn combine(src: List<Int>, dst: List<Int>, scratch: List<Int>) {
    for i in 0..src.len() {
        dst[i] = src[i] + scratch[i]
    }
}
```

**Soundness is the programmer's responsibility.** Calling a
`#[noalias]` function with aliasing pointers is undefined behavior
at the LLVM optimization level — the backend is free to reorder
loads/stores in ways that would be illegal under true aliasing.
Unlike `#[parallel]`, which is coarser (whole-loop), `#[noalias]`
scopes the promise to specific parameters and survives across
inlining and LTO.

Unknown keys, `key = value` shapes, and duplicate parameter names
are rejected with `E0739`.

#### 3.8.11 `#[pure]`

Valid on top-level `fn` declarations and on struct/enum methods.
Bare flag. Status: **v0.6 A13** (lenient — the compiler trusts the
annotation; see SPEC_GAPS `pure-enforce`).

Asserts the function has no observable side effects: no writes to
memory the caller can see, no I/O, no calls to impure functions.
The LLVM emitter sets the `readnone` fn attribute, which lets the
optimizer:

- **CSE** repeated calls with the same arguments — multiple
  `f(a, b)` invocations collapse to one.
- **Hoist** calls out of loops when their arguments are loop-
  invariant.
- **Inline aggressively** since there are no side-effect ordering
  constraints to preserve.
- **Dead-call elimination** if the return value is unused.

```osty
#[pure]
pub fn mixKeys(a: Int, b: Int) -> Int { a * 31 + b }
```

**Soundness is the programmer's responsibility.** The v0.6 compiler
does not verify that the annotated body is actually pure. A
`#[pure]` function that mutates shared state or performs I/O is
undefined behavior — the optimizer will drop, reorder, or duplicate
calls in ways that expose the lie. A future release will add a
checker pass that rejects non-pure bodies; the work is tracked under
SPEC_GAPS `pure-enforce`.

Any argument is rejected with `E0739`.

#### 3.8.12 Positioning Rules

- Annotations may appear only before a named declaration. They cannot
  be attached to:
  - Expressions (including closures and `if` branches)
  - `use` statements
  - Individual statements inside a function body
  - `self`/`mut self` method receivers (annotate the method instead)
- The same annotation name cannot appear more than once on the same
  target. `#[json(key="a")] #[json(key="b")] pub x: String` is a
  compile error — merge or pick one.
- In partial struct/enum declarations (§3.4), each declaration's
  annotations apply only to members named in that declaration; the
  compiler does not merge annotations across declarations.

#### 3.8.13 Annotation interaction matrix

The annotation set is intentionally small (31 entries in v0.6, §1.10.3),
which keeps the *interactions* between annotations bounded. The table
below catalogues the meaningful pairings — empty cells mean the
annotations are orthogonal (no special rule applies).

| Caller annotation | `#[reproducible]` | `#[pure]` | `#[error_contract]` | `#[budget]` | `#[golden]` |
|---|---|---|---|---|---|
| `#[ambient]` | rejected (only in entry-point) | rejected | OK | OK | OK |
| `#[reproducible]` | scope ≤ caller | implied stronger | OK | OK | implicit `#[reproducible]` |
| `#[pure]` | implies all scopes | (self) | OK | OK | OK |
| `#[error_contract]` | OK | OK | (only for `Result<_, E>`) | OK | OK |
| `#[budget(static)]` | OK | OK | OK | (self) | OK |
| `#[budget(runtime)]` | warned (perf measurement is non-deterministic) | warned | OK | (self) | warned |
| `#[golden]` | implicit `#[reproducible(scope = "target")]` | OK | OK | OK | (self) |

Reading examples:

- `#[ambient(clock)]` + `#[reproducible]`: rejected. `#[ambient]` is
  permitted only on entry-point functions, which are not
  reproducible. `E0782`.
- `#[golden]` + `#[budget(time_ms = 5)]`: warned. The golden
  comparison runs in the test harness; mixing it with runtime
  budget measurement makes the budget signal noisy. The recommended
  pattern is to keep `#[budget(runtime)]` on the production
  function and `#[golden]` on the test fixture that drives it.
- `#[error_contract]` + `#[pure]`: OK. A pure function may return
  `Result<_, E>` and carry an error contract.
- `#[reproducible(scope = "portable")]` + transitively-called
  `#[reproducible(scope = "target")]`: rejected with `E0787`. A
  `portable` caller cannot delegate to a `target` callee — the
  scope contract propagates downward (§3.11.1).

#### 3.8.14 Recommended ordering convention

Multiple annotations on the same declaration are independent — order
does not affect semantics. The conventional ordering, used by the
formatter and `osty doc` rendering, places annotations in this
sequence (top to bottom):

1. **Visibility** — `pub` (not technically an annotation, but
   appears in the slot)
2. **Intent** — `#[purpose]`
3. **Examples** — `#[example]` (one or more)
4. **Spec link** — `#[spec]`
5. **Error contract** — `#[error_contract]`
6. **Reproducibility / purity** — `#[reproducible]` / `#[pure]`
7. **Budget** — `#[budget]`
8. **Stability / since** — `#[stability]`, `#[since]`, `#[deprecated]`
9. **Performance hints** — `#[inline]`, `#[hot]` / `#[cold]`,
   `#[target_feature]`, `#[noalias]`, `#[parallel]`,
   `#[vectorize]`, `#[unroll]`, `#[no_vectorize]`
10. **Compatibility** — `#[match_compat]`
11. **Information flow** — `#[taint]` / `#[sanitizes]` / `#[trusted_declassify]` (function-level)
12. **Capability marker** — `#[reproducible_capability]` (interfaces)

The formatter normalizes to this ordering on save. Authors who
prefer a different sequence should set `formatter.annotation_order =
"as-written"` in `osty.toml`.

### 3.10 `#[spec("§X.Y")]` — Spec link (G38)

A declaration may carry `#[spec("§X.Y")]` to register a checked link
into the language specification. The compiler verifies the target
markdown anchor exists; missing anchors produce `E0790`. The compiler
includes the section's lead paragraph in `osty doc` output and LSP
hover.

```osty
#[spec("§2.2")]
fn checkNumericWidening(from: Type, to: Type) -> CheckResult { ... }

#[spec("§10.30.user")]
pub fn createUser(email: String, db: Db) -> Result<UserId, Error> { ... }
```

`#[spec]` may be applied to functions, methods, structs, enums, and
interfaces. Use is encouraged for compiler-internal code
(`internal/check`, `toolchain/*.osty`) and stdlib modules; user code
may use it for self-documentation. Osty has no `impl` blocks (§14) —
methods live in struct / enum bodies, where `#[spec]` applies
directly.

#### 3.10.1 Anchor resolution

The `§X.Y` form is resolved against the spec markdown corpus (the
files in `LANG_SPEC_v0.6/`) at compile time. Resolution rules:

1. The argument must be a *string literal* matching the regex
   `§\d+(\.\d+)*(\.\w+)*` (Unicode `§` is required — `&sect;` /
   `§` are not accepted).
2. The trailing path beyond `§X.Y` (e.g. `§10.30.user.create`)
   names a markdown anchor *under* §10.30 — the resolver looks for
   `<a id="user-create">` or a heading whose slugified form matches
   `user-create`.
3. Missing anchor → `E0790` with the suggested anchor list (the
   resolver fuzzy-matches and offers up to 3 alternatives).
4. Moved anchor (the file no longer contains the named heading but
   the corpus still has the anchor elsewhere) → `W0790` — the
   diagnostic suggests the new path. Tooling can auto-rewrite via
   `osty fix --spec-links`.

```osty
#[spec("§10.30.user.create")]   // resolves to LANG_SPEC_v0.6/10-standard-library/30-...
                                //   under heading "user.create"
pub fn createUser(...) -> ... { ... }
```

#### 3.10.2 Spec link inheritance

A `#[spec]` annotation on a `struct` or `enum` is *not* inherited by
its methods — each method declares its own link. This is intentional:
the struct's spec link describes the *type*, while a method's spec
link points at the specific method's contract. Tools resolve both
when generating documentation.

```osty
#[spec("§10.30.user")]
pub struct User {
    pub email: Email,
    ...

    #[spec("§10.30.user.toString")]
    pub fn toString(self) -> String { ... }

    // No #[spec] — `osty doc` falls back to "see User type spec".
    pub fn isVerified(self) -> Bool { self.verified }
}
```

#### 3.10.3 Spec link in `osty context` JSON

`osty context <symbol> --format=json` emits the resolved spec link
as a structured object containing the anchor, the file path, the
heading text, and the lead paragraph (first non-empty paragraph
after the heading). This lets agents read the spec body without
fetching and parsing markdown themselves.

```json
{
  "spec": {
    "anchor": "§10.30.user.create",
    "file": "LANG_SPEC_v0.6/10-standard-library/30-user.md",
    "heading": "user.create",
    "lead": "Create a user, validating email format..."
  }
}
```

#### 3.10.4 `#[spec]` and capability surface

A function with `#[spec("§X.Y")]` is *not* implicitly required to
match the capability surface declared in the spec target. The
checker verifies the anchor's *existence*, not its semantic
agreement with the function. Authors who want the stronger
guarantee can use `#[reproducible]` / `#[error_contract]` /
`#[budget]` together — those *do* enforce a contract — and use
`#[spec]` only as a documentation breadcrumb.

A future revision may add `osty validate-spec --strict` (§13.6)
that runs LLM-driven semantic comparison between the spec text and
the implementation — that gate is opt-in and not part of the v0.6
baseline.

### 3.11 `#[reproducible(scope=...)]` — Determinism contract (G39)

A function may declare `#[reproducible]` to assert that its output is
fully determined by its input — suitable for cache keys, build hashes,
migration IDs, content addressing.

```osty
#[reproducible(scope = "target")]
fn computeKey(data: Bytes) -> Bytes32 {
    sha256(data)
}
```

**Scope levels.**

| Scope | Meaning |
|---|---|
| `"run"` | Same output across one process execution. `Console` capability allowed. |
| `"target"` *(default)* | Same output across the same Osty version + target triple. |
| `"portable"` | Byte-equal across platforms (cross-compilation). Endianness-explicit, NaN-bit-pattern-stable. |

**Compiler checks.** The function (and its transitive callees):

- Must not receive non-deterministic capabilities (`Clock`, `Rng`,
  `Env`, `Fs`, `Net`, `Process`) — see §20.4. `E0784`.
- Must not iterate unordered collections (`Map.iter`, `Set.iter` —
  use `Map.entriesSorted` / `Set.toListSorted`). `E0786`.
- Must not depend on pointer identity comparisons.
- Must call only callees with at least the same scope strength.
  `E0787`.

`scope = "portable"` adds endianness and NaN-bit constraints (`E0788`).

`#[pure]` (carried from v0.5 §3.8) is strictly stronger than
`#[reproducible]` — it forbids capability receipt entirely, even for
deterministic capabilities like `Hash`. `E0785`.

#### 3.11.1 Scope inference rules

When a `#[reproducible(scope = X)]` function calls another
function `f`, the checker requires that `f` is also reproducible
with scope ≥ `X`. The strength order:

```
portable > target > run
```

A `portable` caller may freely call `target` or `run` callees? **No**
— stronger callers require *equally or more* strict callees. The
arrow runs the other way: a `portable` function cannot call a
`target` function, because `target`'s output may depend on
endianness or NaN-bit patterns that `portable` excludes.

| Caller scope | May call (callee scope) |
|---|---|
| `run` | `run`, `target`, `portable` |
| `target` | `target`, `portable` |
| `portable` | `portable` only |

Practical consequence: helper functions used inside `portable`
contexts must themselves be `portable`. The `#[reproducible(scope =
"portable")]` annotation propagates downward.

#### 3.11.2 Allowed capability shapes

Some capabilities are *deterministic by construction* — the same
input always produces the same output, regardless of process
identity. These remain receivable inside `#[reproducible]`:

| Capability | Deterministic? | Notes |
|---|---|---|
| `Hash` | Yes (when annotated `#[reproducible_capability]`) | Hashing is by definition deterministic |
| `Clock` | No | `now()` depends on wall time |
| `Rng` | No | Even seeded; the seed is process-local state |
| `CryptoRng` | No | Pulls from OS entropy pool |
| `Env` | No | Process environment is not stable input |
| `Fs` | No | Filesystem state is non-deterministic |
| `Net` | No | Network responses depend on remote state |
| `Process` | No | Subprocess output is non-deterministic |
| `Console` | No (effect), but allowed at `run` scope | Stdout writes are observable |

Deterministic capabilities (e.g. a `Hash` interface where every
method is annotated `#[reproducible(scope = "portable")]`) are
receivable in `portable` functions provided the *interface itself*
is annotated `#[reproducible_capability]` (§20.5).

#### 3.11.3 Reproducibility audit

`osty audit --reproducible` enumerates every `#[reproducible]`
declaration and shows its scope:

```sh
$ osty audit --reproducible
pkg myapp.users
  fn computeUserKey(...)         scope=target  callees=2 (sha256:portable, json.encode:target)
  fn migrationId(...)            scope=portable callees=1 (sha256:portable)
```

The output is suitable for security review: a `target`-scoped key
function called from a context that needs `portable` immediately
shows up as a scope mismatch.

### 3.12 Structured Intent — `#[purpose]`, `#[example]`, `#[fixture]` (G42)

Structured, machine-readable intent annotations complement free-text
doc comments. They feed `osty doc`, `osty test --example`, LSP hover,
property-test seed selection, and `osty context <symbol>`.

```osty
#[purpose("이메일 검증 후 DB에 사용자 저장")]
#[example(input = ["alice@example.com", "<Db>"], output = "Ok(42)")]
#[example(input = ["invalid", "<Db>"], output = "Err(EmailError.Format)")]
pub fn createUser(email: String, db: Db) -> Result<UserId, Error> { ... }

#[fixture(name = "alice")]
fn aliceUser() -> Email { Email.parse("alice@example.com")? }
```

| Annotation | Verified? | Consumed by |
|---|---|---|
| `#[purpose("...")]` | No (free text) | `osty doc`, LSP, `osty context` |
| `#[example(input = ..., output = ..., uses = ...)]` | Yes (call+compare) | doc, test, context |
| `#[fixture(name = "...")]` | Signature only | doc, test, property gen, golden |

`#[fixture]` is restricted to zero-arity functions. `E0432`.

#### 3.12.1 `#[purpose]` — free-text intent

`#[purpose("...")]` is a single string literal expressing *why* the
declaration exists. Unlike a doc comment (`///`), the purpose:

- Has a fixed syntactic shape — easy for tooling to extract.
- Is required to be a single string literal, not a concatenation
  or expression. `E0431`.
- Is rendered as the *primary one-liner* in `osty doc`, separate
  from longer explanatory prose in `///` comments.

Recommended length: under 80 characters; one sentence; no internal
formatting markers. The conventions favor a *what does this do?*
phrasing, not a *how does this do it?* one — the `///` comment
covers the latter.

```osty
// ✅ Good — declarative, one sentence, fits a render slot.
#[purpose("Validate the email and create a user row, returning the row id")]
pub fn createUser(...) -> ... { ... }

// ❌ Bad — too long, embedded markup, prose-like.
#[purpose("This function takes an `email` *string* and a `db` *capability* and ...")]
pub fn createUser(...) -> ... { ... }
```

#### 3.12.2 `#[example]` — input/output pairs

`#[example]` records a tested input/output pair. The annotation
reads as a function call: pass `input`, expect `output`, optionally
inject named fixtures via `uses`:

```osty
#[example(input = ["alice@example.com", "<Db>"], output = "Ok(42)")]
#[example(input = ["alice@example.com", "<Db>"],
          uses = "fakeDb",
          output = "Err(SignupError.DbConflict(1))")]
#[example(input = ["", "<Db>"], output = "Err(SignupError.EmailFormat)")]
pub fn signup(email: String, db: Db) -> Result<UserId, SignupError> { ... }
```

**Argument grammar.**

- `input = [a, b, c]` — a literal list whose elements match the
  function's positional parameters in order. `<Db>` / `<Net>` /
  etc. are *capability placeholders* — the runner substitutes the
  fixture named by the `uses` argument (or the default fake from
  `std.testing.capabilityFakes()` if no `uses` is given).
- `output = "..."` — a string literal parsed and type-checked as an
  Osty expression at test time. It must compare equal to the
  function's actual return value with `==` using the value's `Equal`
  implementation; `ToString` rendering is not involved.
- `uses = "name"` — references a `#[fixture(name = "name")]`
  function. Multiple `uses =` repeats inject each named fixture in
  order; capability placeholders in `input` are resolved against
  the fixture set.

**Verification.** `osty test --example` runs each `#[example]` as
an independent test. Output mismatch is a test failure. Missing
fixture (named in `uses` but no matching `#[fixture]`) is `E0433`.

#### 3.12.3 `#[fixture]` — canonical instances

`#[fixture(name)]` registers a zero-arity function as a named
canonical instance for tests, examples, and documentation:

```osty
#[fixture(name = "alice")]
fn alice() -> Email {
    Email.parse("alice@example.com").unwrap()
}

#[fixture(name = "fakeDb")]
fn fakeDb() -> Db {
    let db = std.capability.testing.FakeDb()
    db.seed(alice())                // fixtures may call other fixtures
    db
}
```

**Composition rules.**

1. Zero arity is mandatory (`E0432`). A fixture takes no parameters.
2. Fixtures may call other fixtures by name. Cycles are `E0433`.
3. Each call to a fixture function returns a *fresh instance* —
   tests never share fixture state.
4. Fixtures are package-local by default; `pub fn` annotated with
   `#[fixture]` is exported for downstream packages but is rare in
   practice (most fixtures are test-local).

Fixture names live in a package-qualified namespace. Inside the same
package, `uses = "fakeDb"` resolves to that package's fixture named
`fakeDb`. Downstream references to public fixtures use the normal
qualified path (`uses = "pkg.fakeDb"` or an imported alias). Two
fixtures with the same exported name in one package are rejected as a
duplicate public/test helper name (`E0554`).

**Consumption.**

- `#[example(uses = "name")]` — direct use as test input.
- `osty doc` — fixture body inlined under the example panel.
- `osty context <symbol> --format=json` — fixtures listed under
  `fixtures_referenced[]`.
- Property-test seeds (Phase 5, `forall x in fixture: ...`) — the
  fixture's value is the starting point for shrinking.

#### 3.12.4 Composition example — full intent surface

```osty
#[purpose("Validate the email and create a user row, returning the row id")]
#[example(input = ["alice@example.com", "<Db>"],
          uses = "freshDb",
          output = "Ok(UserId(1))")]
#[example(input = ["alice@example.com", "<Db>"],
          uses = "dbWithAlice",
          output = "Err(SignupError.DbConflict(1))")]
#[example(input = ["invalid", "<Db>"],
          uses = "freshDb",
          output = "Err(SignupError.EmailFormat)")]
#[spec("§10.30.user.signup")]
#[since("0.6")]
#[stability("stable")]
#[error_contract(
    SignupError.EmailFormat   when "Email.parse failed",
    SignupError.DomainBlocked when "domain in deny-list",
    SignupError.DbConflict    when "email unique constraint",
)]
pub fn signup(email: String, db: Db) -> Result<UserId, SignupError> {
    spec {
        example: signup("alice@example.com", freshDb()) == Ok(UserId(1))
    }
    ...
}

#[fixture(name = "freshDb")]
fn freshDb() -> Db { std.capability.testing.FakeDb() }

#[fixture(name = "dbWithAlice")]
fn dbWithAlice() -> Db {
    let db = freshDb()
    db.exec(insertSql(Email.parse("alice@example.com").unwrap()))
    db
}
```

This single declaration emits to:

- `osty doc` page with purpose / spec link / failure modes / 3
  example panels (each with the fixture body inlined).
- `osty context signup --format=json` with the full intent payload.
- `osty test --spec --example --golden` runs all three examples + the
  spec-block clause.
- `osty publish` validates the API surface against `#[stability("stable")]`
  and the contract.
- LSP hover shows the purpose + spec lead paragraph.

This is what *hidden dependency forbidden* looks like at a single
declaration: every external dependency, every failure mode, every
test fixture, every API stability promise — all surfaced explicitly
at the function's signature.

### 3.13 `spec { ... }` — Executable Spec Block (G43)

A top-level function or method body may begin with a `spec { ... }`
block stating executable specifications colocated with the implementation. The
block has no runtime cost — `example:` clauses run as tests under
`osty test --spec`, while `law:` and `invariant:` clauses are surfaced
in `osty doc` and LSP hover.

```osty
fn normalizeEmail(s: String) -> String {
    spec {
        example: normalizeEmail(" Alice@EXAMPLE.COM ") == "alice@example.com"
        example: normalizeEmail("") == ""
        law: result == result.trim()
        law: result == result.toLowerCase()
        invariant: result.indexOf(" ") == -1
    }
    s.trim().toLowerCase()
}
```

**Clauses.**

| Clause | v0.6 (Phase 3) | v1 (Phase 5) |
|---|---|---|
| `example: expr` | Run as test under `osty test --spec`; expected `Bool` (`E0441`) | (same) |
| `law: expr`, `invariant: expr` | Doc / LSP only; `result` is virtual binding for return value (`E0442`) | (same) |
| `forall x, y in gen: expr` | Reserved (parser allows; runtime defers) | Auto property test |

The `spec` block must be the top-level function or method body's
*first* statement (`E0440`). It is not an expression; it produces no
value. Closure bodies, `if` arms, `match` arms, and nested blocks cannot
host a `spec` block even when `spec` appears first inside that block.

#### 3.13.1 `result` virtual binding

Inside `law:` and `invariant:` clauses, the identifier `result`
refers to the function's return value as if it had already been
computed. The binding is *virtual* — it does not exist at runtime,
and the clauses themselves are not executed in v0.6 baseline (Phase
3 runs `example:` only). The checker uses `result` to type-check
the clause body so authors can reference it without `let result =
...` boilerplate.

```osty
fn parseInt(s: String) -> Result<Int, Error> {
    spec {
        example: parseInt("42") == Ok(42)
        example: parseInt("foo").isErr()
        law: result.isOk() == s.bytes().all(|b| b >= '0' && b <= '9') || s == ""
        invariant: !result.isOk() || result.unwrap() >= 0 || s.startsWith("-")
    }
    ...
}
```

`result` is in scope only inside `law:` / `invariant:` clauses.
Inside `example:` clauses, the function is called explicitly with
the example's input — there is no implicit `result` because the
runner needs to know which arguments to pass.

#### 3.13.2 Determinism rules for `example:` clauses

An `example:` clause is run as a test under `osty test --spec`.
The test harness applies these rules:

1. The clause body must evaluate to `Bool`. Anything else is `E0441`.
2. The clause is run with the surrounding function's `#[fixture]` set
   inlined as `let` bindings — every `name` in `#[example(uses =
   "name")]` is bound to the fixture body's return value.
3. The clause cannot consult non-deterministic capabilities directly.
   To exercise `Clock` / `Rng` / `Net` / `Fs`, route through
   `std.capability.testing.Fake*` via a `#[fixture]`.
4. Multiple `example:` clauses run independently — each gets a fresh
   fixture instance, so state mutations don't leak between examples.

```osty
#[fixture(name = "fakeClock")]
fn fakeClock() -> Clock {
    std.capability.testing.FakeClock(epoch_ms = 1_000_000)
}

fn timestampLine(clock: Clock, msg: String) -> String {
    spec {
        example: timestampLine(fakeClock(), "ready") == "1000000:ready"
    }
    "{clock.now().toEpochMillis()}:{msg}"
}
```

#### 3.13.3 Composing with `#[example]`

`spec { example: }` and `#[example]` are *additive* surfaces, not
substitutes:

| Surface | Best for |
|---|---|
| `spec { example: ... }` (inside body) | Examples that read naturally as code; "the parser accepts these inputs" |
| `#[example(input = ..., output = ..., uses = ...)]` (annotation) | Machine-readable input/output pairs; consumed by `osty doc` and `osty context` JSON |

A function may carry both. `osty test --spec` runs the spec-block
clauses; `osty test --example` runs the annotation entries; `osty
test --spec --example` runs both. They share the same `#[fixture]`
registry.

#### 3.13.4 Spec block and reproducibility

A spec block does not by itself make its enclosing function
`#[reproducible]` — that's a separate annotation (§3.11). However,
spec blocks compose well with reproducibility:

```osty
#[reproducible(scope = "target")]
fn merkleRoot(leaves: List<Bytes32>) -> Bytes32 {
    spec {
        example: merkleRoot([]) == Bytes32.zero()
        example: merkleRoot([Bytes32.zero()]).isHashOf(Bytes32.zero())
        law: result == merkleRoot(leaves)   // determinism
    }
    ...
}
```

The `law: result == merkleRoot(leaves)` clause is documentation in
v0.6 baseline; Phase 5 turns it into an enforced property test
(`forall leaves in gen.list(gen.bytes32(), 16): result ==
merkleRoot(leaves)`).

### 3.14 API Evolution — `#[since]`, `#[stability]`, `#[match_compat]` (G44)

Three annotations cooperate to make API versioning a first-class
concern.

```osty
pub enum HttpEvent {
    Get,
    Post,
    #[since("0.6")]
    Patch,                       // v0.6 신규 variant
}

#[stability("stable")]
#[since("0.6")]
pub fn parseEmail(s: String) -> Email? { ... }

#[stability("experimental", until = "0.7")]
pub fn parseEmailLoose(s: String) -> Email? { ... }

#[stability("deprecated", since = "0.6", remove = "0.8")]
pub fn oldApi() -> Int { ... }

#[match_compat("0.6", fallback = handlePatchAsPut)]
fn dispatch(e: HttpEvent) -> Response {
    match e {
        HttpEvent.Get -> handleGet(),
        HttpEvent.Post -> handlePost(),
    }
    // HttpEvent.Patch reaches `fallback`
}
```

`#[stability]` levels: `"stable"`, `"experimental"`, `"deprecated"`,
`"internal"`. `osty publish` enforces SemVer compatibility — breaking
changes to `stable` APIs require a major bump (`E2100`); see
`00-revision.md §3.14.3` for the surface diff algorithm.

`#[match_compat]` pins a `match` expression to a historical enum
shape so that adding a variant (with `#[since]`) does not silently
break existing code. Either `fallback = name` or `unsafe_silent =
true` is mandatory (`E0450`); the latter always emits `W0902`.

#### 3.14.1 Stability levels — when to use which

| Level | Audience | Breaking-change rule | Use case |
|---|---|---|---|
| `"stable"` | All external consumers | Major bump required (`E2100` if violated) | Public APIs that downstream packages depend on |
| `"experimental"` | Early adopters opting in | Warning (`W2100`); breaking changes allowed | New surfaces being trialed before promotion |
| `"deprecated"` | Existing users migrating away | Becomes `E2100` once `remove = "X.Y"` hits | APIs scheduled for removal |
| `"internal"` | Same-package only | Not part of public surface; no SemVer rule | Implementation helpers exposed across files |

Promotion path: `experimental` → `stable` (after a release cycle of
practical use). Demotion path: `stable` → `deprecated` (with a
`remove` target version) → removed. Direct `stable` → removed is a
SemVer violation regardless of major-bump.

#### 3.14.2 `#[since]` and version anchoring

`#[since("X.Y")]` is metadata, not a check. The version string must
match the manifest's `[package] version` history but is not
otherwise validated by the compiler — it is consumed by `osty doc`
and `osty changelog` to render version chips.

A `#[since]` value newer than the package's *current* version is
permitted (it pre-records a planned addition for an upcoming
release). `osty publish` reconciles `#[since]` against the actual
release version at publish time.

#### 3.14.3 `#[match_compat]` worked patterns

The simple form names a fallback handler:

```osty
#[match_compat("0.6", fallback = handleUnknown)]
fn dispatch(e: HttpEvent) -> Response {
    match e {
        HttpEvent.Get -> handleGet(),
        HttpEvent.Post -> handlePost(),
    }
}

fn handleUnknown(e: HttpEvent) -> Response {
    log.warn("unhandled event: {e}")
    http.notImplemented("")
}
```

The fallback receives the unmatched value as its first argument and
returns the same type as the match expression. `osty check` verifies
the fallback's signature.

The escape form `unsafe_silent = true` is for the rare case where a
silent default is acceptable:

```osty
#[match_compat("0.6", unsafe_silent = true)]
fn isReadEvent(e: HttpEvent) -> Bool {
    match e {
        HttpEvent.Get -> true,
        HttpEvent.Post -> false,
    }
    // Future variants silently return false.
}
```

`unsafe_silent = true` always emits `W0902` to make the silent fall-
through visible at every site, and `osty audit --match-compat` lists
all such uses.

#### 3.14.4 Coordinated evolution: enum + match + handler

When introducing a new enum variant, three coordinated changes
happen across releases:

```osty
// v0.6
pub enum HttpEvent {
    Get,
    Post,

    #[since("0.7")]
    Patch,                          // declared but not yet present
}

#[match_compat("0.6", fallback = handlePatchAsPost)]
fn dispatch(e: HttpEvent) -> Response { ... }

fn handlePatchAsPost(e: HttpEvent) -> Response { ... }

// v0.7
//   - Patch becomes #[since("0.6")]; the variant is materialized.
//   - dispatch's match adds Patch -> handlePatch().
//   - #[match_compat] is removed (or changed to "0.7").
//   - handlePatchAsPost is removed (or kept for older compat).
```

Authors who follow this pattern get *zero silent fallthroughs* across
the upgrade — the compiler-enforced fallback in v0.6 anchors the
behavior, and the explicit handler in v0.7 replaces it.

### 3.15 `#[budget]` — Performance Contract (G46)

A function may declare static and runtime performance budgets.

```osty
#[budget(allocs = 0, io_calls = 0, stack_depth = 100)]
fn pureCompute(data: Bytes) -> Bytes32 { ... }

#[budget(time_ms = 5, p99_ms = 20)]
fn routeRequest(req: Request) -> Response { ... }
```

**Static keys** (compiler-proven; violation `E0795`):

| Key | Meaning |
|---|---|
| `allocs = N` | Maximum GC allocation sites in transitive call graph |
| `io_calls = N` | Maximum capability-method calls |
| `stack_depth = N` | Maximum recursive depth |
| `instructions = N` | LLVM-cost-model estimate |

**Runtime keys** (measured by `osty bench --budget`; violation `W0795`):

| Key | Meaning |
|---|---|
| `time_ms = X` | Mean wall-clock time |
| `p99_ms = X` | p99 wall-clock time |

Static and runtime keys may coexist on the same annotation —
the compiler partitions them by category.

#### 3.15.1 Static-key proof rules

`allocs = N` is proved by counting allocation sites in the
function's transitive call graph. The checker walks every callee
reachable from the function body and sums the worst-case
allocations per call site. A function calling `List<Int>.append(x)`
in a loop with bound `n` counts `n` allocations (one per `append`),
so `#[budget(allocs = 0)]` on such a function is `E0795`.

`io_calls = N` counts capability-method calls. A function that takes
`Net` and calls `net.connect(...)` once carries `io_calls = 1`. A
helper that *transitively* calls `net.connect` through 3 wrapper
functions still counts the single underlying call.

`stack_depth = N` is proved by the maximum simple-cycle path in the
call graph. Recursive functions can carry `stack_depth = N` only
when the recursion has a structural bound (e.g. tree height); the
checker conservatively rejects unbounded recursion under any finite
budget.

`instructions = N` uses the LLVM IR cost model; the budget is
matched against the function's lowered IR instruction count. Tight
loops with `#[unroll]` raise the count; the budget should be set
based on observed values from `osty bench --instructions`.

#### 3.15.2 Runtime-key measurement

`time_ms` and `p99_ms` are sampled by `osty bench --budget` over a
large iteration count (default 1000, configurable via
`--benchtime`). The sampling rules:

- The benchmark warm-up phase (`max(N/10, 100)` iterations) is
  excluded from the budget check.
- Wall-clock time uses the monotonic clock; clock skew during the
  run does not affect the budget.
- Cancellation paths (`Err(Cancelled { ... })` returned mid-run) are
  counted as failed iterations; budget violations apply to
  successful iterations only.
- A regression of more than 20% over the previous published version
  promotes `W0795` (warning) to `E0795` (error) on `osty publish`,
  blocking release until fixed or the budget is intentionally
  loosened (which itself is a SemVer-relevant change — see §3.14).

#### 3.15.3 Worked budget patterns

**Hot pure helper** — zero alloc, zero IO, bounded depth:

```osty
#[budget(allocs = 0, io_calls = 0, stack_depth = 1)]
#[reproducible(scope = "portable")]
fn xorBytes(a: Bytes, b: Bytes) -> Bytes {
    let mut out = Bytes.zeros(a.len())
    for i in 0..a.len() {
        out[i] = a[i] ^ b[i]
    }
    out
}
```

The single output buffer is the lone allocation; `Bytes.zeros` is a
single allocation site. Adjusting to `allocs = 1` reflects the
honest cost.

**HTTP handler** — bounded time, bounded p99:

```osty
#[budget(time_ms = 5, p99_ms = 20)]
fn routeUserLookup(req: HttpRequest, db: Db) -> Result<HttpResponse, Error> {
    let id = req.queryParam("id") ?? ""
    match db.queryOne::<User>("SELECT * FROM users WHERE id = ?", [id])? {
        Some(u) -> Ok(http.okJson(u)),
        None -> Ok(http.notFound("")),
    }
}
```

`time_ms = 5` says the *mean* response is under 5ms; `p99_ms = 20`
says even the slowest 1% stays under 20ms. The benchmark drives
the function with synthetic input under `osty bench --budget`; CI
fails on regression.

**Combined static + runtime**:

```osty
#[budget(
    allocs = 4,
    io_calls = 1,
    time_ms = 5,
    p99_ms = 20,
)]
pub fn createUser(email: String, db: Db) -> Result<UserId, UserCreateError> { ... }
```

The compiler proves `allocs ≤ 4` and `io_calls ≤ 1` at compile
time; `osty bench --budget` measures `time_ms` and `p99_ms`. Both
gates run independently — a static violation blocks at `osty
check`, a runtime violation blocks at `osty publish`.

### 3.16 Combined v0.6 declaration patterns

이 섹션은 §3.10–§3.15 의 v0.6 어노테이션을 *함께* 사용하는 patterns
의 carry-forward 사례. 정식 의미는 각 sub-section.

#### 3.16.1 Library function — full v0.6 surface

```osty
#[purpose("Validates email and inserts user record")]
#[example(
    input = ["alice@example.com", "<Db>"],
    uses = "fakeDb",
    output = "Ok(42)",
)]
#[example(
    input = ["invalid", "<Db>"],
    uses = "fakeDb",
    output = "Err(UserCreateError.Format)",
)]
#[spec("§10.30.user.create")]
#[since("0.6")]
#[stability("stable")]
#[error_contract(
    UserCreateError.Format        when "Email.parse 실패",
    UserCreateError.DomainBlocked when "도메인 deny-list 등재",
    UserCreateError.DbConflict    when "이메일 unique 위반",
)]
#[budget(allocs = 8, io_calls = 1)]
pub fn createUser(email: String, db: Db) -> Result<UserId, UserCreateError> {
    let parsed = Email.parse(email).orError(UserCreateError.Format)?
    if isDomainBlocked(parsed.domain()) {
        return Err(UserCreateError.DomainBlocked(parsed.domain()))
    }
    db.insert(parsed).mapErr(|e| UserCreateError.DbConflict(e.id))
}

#[fixture(name = "fakeDb")]
fn fakeDb() -> Db { std.testing.db.inMemory() }
```

이 함수가 노출하는 surface:

- **Type**: `(String, Db) -> Result<UserId, UserCreateError>`
- **Capability**: `Db` (`Net` capability 의 wrapping — DB 가 외부 의존)
- **Failure**: 3 contracted variants
- **Performance**: 8 allocs / 1 io_call (compiler proven)
- **Stability**: stable since 0.6
- **Spec ref**: §10.30.user.create
- **Examples**: 2 auto-tested

`osty context std.user.createUser --format=json` 호출 시 위 정보
모두 single JSON 으로 노출 (§13.9).

#### 3.16.2 Reproducible utility — capability-free

```osty
#[purpose("Content-addressed hash of input bytes")]
#[example(input = "<empty bytes>", output = "Bytes32.fromHex(\"e3b0c44...\")")]
#[spec("§10.12.crypto")]
#[reproducible(scope = "portable")]
#[budget(allocs = 1, io_calls = 0)]
pub fn computeKey(data: Bytes) -> Bytes32 {
    sha256(data)
}
```

`#[reproducible(scope = "portable")]` 는 *플랫폼 간 byte-equal* 약속.
`scope = "portable"` 는 가장 강한 scope — endianness / NaN bit /
unordered iter 모두 거부 (§3.11.3).

#### 3.16.3 Spec-block-driven validation

```osty
#[purpose("Normalize email — trim + lowercase")]
fn normalizeEmail(s: String) -> String {
    spec {
        example: normalizeEmail(" Alice@EXAMPLE.COM ") == "alice@example.com"
        example: normalizeEmail("") == ""
        law: result == result.trim()
        law: result == result.toLowerCase()
        invariant: result.indexOf(" ") == -1
    }
    s.trim().toLowerCase()
}
```

`spec { example: }` 는 `osty test --spec` 자동 실행. `law:` /
`invariant:` 는 v0 (Phase 3) 에서 `osty doc` 만 — v1 (Phase 5) 에서
property test 자동 생성.

#### 3.16.4 Sealed type with structured intent

```osty
#[purpose("RFC 5322 email parser")]
#[example(input = "alice@example.com", output = "Some(...)")]
#[example(input = "no-at", output = "None")]
#[spec("§10.30.email.parse")]
#[since("0.6")]
#[stability("stable")]
#[sealed_construct(parse)]
pub struct Email {
    local: String,
    domain: String,
}

impl Email {
    pub fn parse(s: String) -> Email? {
        let parts = s.split("@")
        if parts.len() != 2 { return None }
        Some(Email { local: parts[0], domain: parts[1] })
    }

    pub fn local(self) -> String { self.local }
    pub fn domain(self) -> String { self.domain }
}
```

`Email.parse(...)` 외 path 로 `Email` 인스턴스 만들 수 없음 — 외부
struct literal `Email { local: ..., domain: ... }` 은 `E0420`. 이로써
`Email` 값이 존재한다 ⇒ 위 `parse` 가 OK 반환했다 ⇒ 검증 통과.

#### 3.16.5 Web handler — capability + taint + sanitize

```osty
#[purpose("Look up user by ID with SQL safety")]
#[since("0.6")]
#[stability("stable")]
#[error_contract(
    HandlerError.NotFound when "users 테이블에 없는 ID",
    HandlerError.DbDown   when "DB 연결 실패",
)]
#[budget(allocs = 4, io_calls = 1, time_ms = 50)]
pub fn lookupUser(
    #[taint("user_input")] userId: String,
    db: Db,
    clock: Clock,
) -> Result<UserSummary, HandlerError> {
    let safe = std.sql.escape(userId)             // sanitize: user_input → sql_safe
    let started = clock.monotonic()

    let rows = db.exec(
        "SELECT id, email FROM users WHERE id = ?",
        [safe],                                    // sink #[requires("sql_safe")] 충족
    ).mapErr(|_| HandlerError.DbDown)?

    if rows.isEmpty() { return Err(HandlerError.NotFound) }

    Ok(UserSummary {
        id: rows[0].getInt("id"),
        email: rows[0].getString("email"),
        lookedUpAt: clock.now(),
    })
}
```

여러 v0.6 surface 가 한 함수에 모임 — capability (`db: Db`,
`clock: Clock`) + taint (`userId` 가 user_input source) + sanitizer
(`std.sql.escape`) + sink (`db.exec` parameterized form) +
error_contract (2 failure modes) + runtime budget. `osty context`
호출 시 single JSON 으로 모두 export (§13.9.1).

#### 3.16.6 Match with versioned enum

```osty
pub enum HttpEvent {
    Get,
    Post,
    Put,
    Delete,

    #[since("0.7")]
    Patch,                       // v0.7 신규 — v0.6 코드는 fallback 로 처리
}

#[match_compat("0.6", fallback = handlePatchAsPut, reason = "Patch 는 v0.7 정식")]
pub fn dispatch(event: HttpEvent) -> Response {
    match event {
        HttpEvent.Get -> handleGet(),
        HttpEvent.Post -> handlePost(),
        HttpEvent.Put -> handlePut(),
        HttpEvent.Delete -> handleDelete(),
    }
    // HttpEvent.Patch (v0.7) 도달 시 fallback = handlePatchAsPut 호출
}

fn handlePatchAsPut() -> Response { handlePut() }
```

`#[match_compat]` 는 *versioned enum shape* 에 match 를 pin —
미래 variant 추가 시 silent 깨짐 방지 (§3.14.4 / E0450).

#### 3.16.7 Test 측 fake injection

production 코드는 explicit capability parameter — 테스트에선
deterministic fake 주입.

```osty
#[test]
fn test_lookupUser_returns_summary() {
    let db = std.capability.testing.FakeDb()
    db.seed(User.parse("alice@example.com")?)

    let clock = std.capability.testing.FakeClock(epoch_ms = 1_000_000)

    let result = lookupUser("123", db, clock)
    testing.assertOk(result)

    let summary = result.unwrap()
    testing.assertEq(summary.email, "alice@example.com")
    testing.assertEq(summary.lookedUpAt.toEpochMillis(), 1_000_000)
}
```

`FakeDb` / `FakeClock` 의 deterministic 동작이 테스트 결과의
재현성을 보장. v0.6 테스트는 `--legacy-globals` 의존 없음 — 모든
effect 가 fake 로 대체.

### 3.17 Declaration anti-patterns and how v0.6 surfaces them

The annotation surface in §3.10 – §3.15 / §20 / §21 is opt-in, but
the v0.6 toolchain can detect declarations that *should* carry one
of them and surface that as a lint or diagnostic. Six anti-patterns
recur often enough to be tracked by `osty audit`:

#### 3.17.1 Effect performed without a capability parameter

```osty
// ❌ Hidden dependency on the system clock — flagged by E0780.
fn buildId() -> String {
    "{time.now().toEpochMillis()}-{random.next()}"
}

// ✅ Explicit capability surface.
fn buildId(clock: Clock, rng: Rng) -> String {
    "{clock.now().toEpochMillis()}-{rng.next()}"
}
```

`E0780` fires on every bare global effect call outside
`--legacy-globals`. The fix is mechanical — accept the capability
as a parameter; entry-point callers either bind ambient or forward
explicitly.

#### 3.17.2 `pub` API without `#[stability]`

```osty
// ⚠ W2105 — pub fn carries no stability attestation.
pub fn createUser(email: String, db: Db) -> Result<UserId, Error> { ... }

// ✅
#[stability("stable")]
#[since("0.6")]
pub fn createUser(email: String, db: Db) -> Result<UserId, Error> { ... }
```

A `pub` symbol without `#[stability]` is *un-attested* — `osty
publish` treats it as `experimental` for SemVer purposes, which
allows breaking changes without a major bump but emits `W2100`
warnings on every diff.

#### 3.17.3 `Result<_, ConcreteEnum>` without `#[error_contract]`

```osty
// ⚠ W0414 — concrete Err type but no contract; documentation gap.
pub fn parseEmail(s: String) -> Result<Email, EmailError> { ... }

// ✅
#[error_contract(
    EmailError.Format        when "missing @ or wrong format",
    EmailError.DomainBlocked when "domain in deny-list",
)]
pub fn parseEmail(s: String) -> Result<Email, EmailError> { ... }
```

The diagnostic is a warning, not an error: `#[error_contract]` is
optional, but its absence on a concrete-enum return is almost always
oversight rather than intent.

#### 3.17.4 Sealed type external literal

```osty
// ❌ E0420 — Email is sealed; cannot construct outside its module.
let e = Email { local: "alice", domain: "example.com" }

// ✅
let e = Email.parse("alice@example.com")?
```

#### 3.17.5 Sink without sanitizer on tainted input

```osty
fn handler(req: HttpRequest, db: Db) -> Response {
    let id = req.queryParam("id") ?? ""
    // ❌ E0901 — tainted input reaches sql_safe sink without sanitization.
    db.query("SELECT * FROM users WHERE id = {id}")
    ...
}
```

The fix is parameterized query (`db.exec("SELECT ... WHERE id = ?",
[id])`) or explicit sanitization (`std.sql.escape(id)`).

#### 3.17.6 `#[reproducible]` with non-deterministic capability

```osty
// ❌ E0784 — reproducible function receives Clock (non-deterministic).
#[reproducible(scope = "target")]
fn cacheKey(clock: Clock, payload: Bytes) -> Bytes32 {
    sha256(payload + clock.now().toBytes())
}

// ✅ Receive a precomputed timestamp instead.
#[reproducible(scope = "target")]
fn cacheKey(timestamp: Int64, payload: Bytes) -> Bytes32 {
    sha256(payload + timestamp.toBytes())
}
```

The caller is then responsible for capturing `clock.now()` at a
non-reproducible boundary and passing the captured value down.
