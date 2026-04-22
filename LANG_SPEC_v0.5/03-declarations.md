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
- Defaults must be literals: numeric, string, char, byte, bool, `None`,
  `Ok(literal)`, `Err(literal)`, empty collection literals (`[]`,
  `{:}`), or `()`.
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
```

The `..expr` form must appear once per struct literal. Fields explicitly
listed override copied values. All fields must either be listed or
supplied by the spread source.

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
```

Variants:
- Bare: `Red`
- Tuple-like: `RGB(UInt8, UInt8, UInt8)`

Variant access: bare name within the same package, qualified from other
packages (`Color.Red`).

### 3.6 Interfaces

```osty
pub interface Writer {
    fn write(self, data: Bytes) -> Result<Int, Error>
    fn close(self) -> Result<(), Error>
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
| `#[vectorize]` | top-level `fn` declarations, struct/enum methods | Hint: LLVM backend attaches `!llvm.loop.vectorize.enable` metadata to every loop lowered in the body (v0.6 A5 SIMD track) |

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

#### 3.8.3 `#[vectorize]`

Valid on top-level `fn` declarations and on struct/enum methods. Bare
flag — no arguments are permitted. Status: **v0.6 A5 (SIMD track)**;
semantics are defined here so the annotation is accepted by the v0.5
front end alongside the existing v0.5 set.

`#[vectorize]` is a *hint*, not a guarantee. The LLVM backend attaches
`!llvm.loop !N` metadata to every user-written `for` loop lowered
inside the annotated function body, where `!N` is a `distinct`
self-referential node whose property list contains
`!"llvm.loop.vectorize.enable", i1 true`. LLVM's loop vectorizer then
decides legality and profitability per loop. The annotation does not
introduce new syntax, does not change the type of the function, and
does not affect observable behavior on correct programs — an
unvectorized build produces the same outputs as a vectorized one.

```osty
#[vectorize]
pub fn sumTo(n: Int) -> Int {
    let mut acc = 0
    for i in 0..n {
        acc = acc + i
    }
    acc
}
```

Scope rules:

- The hint is **function-scoped**. A loop in an unannotated sibling
  function receives no metadata even when the two functions live in
  the same module.
- Only loops originating from a user-written `for` statement carry the
  hint. Loops synthesized by the compiler (e.g. the per-iteration
  scaffold inside `testing.benchmark`, or the key-snapshot traversal
  inside map-mutating helpers) do not.
- Iterator-protocol loops (`for x in iter` where `iter` is not a
  `List<T>`, range, or `Map<K, V>`) currently lower through a
  callback-driven shape that LLVM cannot prove countable; the hint is
  attached but the vectorizer will reject them. This is documented in
  `SPEC_GAPS.md` under `vectorize-hint`.

**GC contract.** To make the vectorizer's legality analysis succeed
on countable loops, `#[vectorize]` functions **opt out of the
per-iteration GC loop safepoint poll**. The function-entry safepoint
still fires, and the caller resumes its own safepoint cadence on
return — so the function is bracketed by polls on both sides. But
inside the function, a long-running vectorized loop does not yield
to a concurrent STW request until it completes.

This is an explicit tradeoff: SIMD execution in exchange for GC
latency across the function body. Callers that need mid-loop
responsiveness should drive the work in smaller chunks from an
unannotated outer loop. The author opts in knowingly by typing
`#[vectorize]`; the compiler does not second-guess.

Rejecting the annotation with arguments is `E0739`
(`CodeAnnotationBadArg`).

#### 3.8.4 Positioning Rules

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
