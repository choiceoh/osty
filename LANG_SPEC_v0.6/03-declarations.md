## 3. Declarations

### 3.1 Functions

```osty
fn add(a: Int, b: Int) -> Int {
    a + b
}

fn greet(name: String) {
    println("hi, {name}")
}

fn connect(host: String, port: Int = 80, timeout: Int = 30) -> Result<Conn, Error> {
    ...
}

pub fn loadConfig(path: String) -> Result<Config, Error> {
    let text = fs.readToString(path)?
    let cfg: Config = json.parse(text)?
    Ok(cfg)
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
v0.5. Relaxing any of them is an additive, semver-observable change —
it enables source that previously did not compile. Such changes must
ship under a normal minor version bump; no FORBID row silently flips
to ALLOW inside a v0.5.x patch release.

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
calls terminated by `.build()`. This design is the v0.3 resolution of
gap G9.

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

### 3.6 Interfaces

```osty
pub interface Writer {
    fn write(self, data: Bytes) -> Result<Int, Error>
    fn flush(self) -> Result<(), Error>
}
```

See §2.6.

### 3.7 Type Aliases

```osty
type UserMap = Map<String, List<User>>
type Handler = fn(Request) -> Result<Response, Error>

pub type Pair<T> = (T, T)
```

Aliases are transparent; they create no new type.

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

`#[pure]` (v0.5) is strictly stronger — it forbids capability receipt
entirely, even for deterministic capabilities like `Hash`. `E0785`.

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

### 3.13 `spec { ... }` — Executable Spec Block (G43)

A function body may begin with a `spec { ... }` block stating
executable specifications colocated with the implementation. The
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

The `spec` block must be the function body's *first* statement
(`E0440`). It is not an expression; it produces no value.

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
