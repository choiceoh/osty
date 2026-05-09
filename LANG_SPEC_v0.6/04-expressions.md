## 4. Expressions

Osty v0.6 is an expression-oriented language. Block (§4.1), `if`
(§4.2), `match` (§4.3), and `loop` are all expressions that produce
values when used in value position, and statements when used at
statement position. This chapter defines expression syntax and
semantics: blocks, conditionals, pattern matching, loops (§4.4),
error propagation (§4.5), optional chaining and
nil-coalescing (§4.6), closures (§4.7), string interpolation (§4.8),
member access (§4.9), indexing (§4.10), block scope (§4.11), `defer`
semantics including cancellation interaction (§4.12), and assignment
forms (§4.13).

Three v0.6 surfaces interact with this chapter without changing its
grammar:

- **Capability flow.** `?`-propagation, `defer` cleanup, and `match`
  exhaustiveness all see capability-typed bindings as ordinary values
  — there is no `effect` keyword, no `try`/`catch`, and no special
  `await` form. An `Err` from `fs.read(path)` propagates through `?`
  identically to any other `Result<_, _>` (§4.5); `defer
  conn.close()` runs through cancel paths exactly like every other
  cleanup (§4.12, §8.4.3).
- **Information flow.** `#[taint]`-tagged values flow through every
  expression form (`if`, `match`, `?`, closures, indexing,
  interpolation) preserving the tag set; sanitization is the only way
  to drop a tag, and it must be explicit (§21.5).

### 4.1 Block Expressions

```osty
let x = {
    let tmp = compute()
    tmp * 2
}
```

A block evaluates to its final expression. `{}` evaluates to `()` in
expression position. Empty map is `{:}`.

Blocks introduce lexical scope. `defer` statements registered inside run
on block exit (§4.12).

#### 4.1.1 Restricted Expression Position

Several constructs (`if`, `for`, `match`, `if let`, `for let`) take an
expression as their head. In these positions, **a struct literal of the
form `Type { ... }` is forbidden** because the trailing `{` would be
ambiguous with the block that follows. Wrapping in parentheses lifts the
restriction. The grammar refers to this as `RestrictedExpr`.

```osty
if (Point { x: 0, y: 0 }) == origin { ... }      // OK
if Point { x: 0, y: 0 } == origin { ... }         // ERROR

match (User { name: n, age: a }) { ... }          // OK
match User { name: n, age: a } { ... }            // ERROR

for x in (List { items: xs }).iter() { ... }      // OK
```

The same restriction applies to:
- The condition of `if` (§4.2)
- The scrutinee of `match` (§4.3)
- The iterable of `for x in <expr>` and the condition of `for <expr>` (§4.4)
- The right-hand side of `if let P = <expr>` and `for let P = <expr>`

### 4.2 If Expressions

```osty
let label = if score >= 90 {
    "A"
} else if score >= 80 {
    "B"
} else {
    "C"
}
```

When used as an expression, all branches must produce the same type and
`else` is required.

**`if let` form.** Pattern-matching form that binds on success:

```osty
if let Some(u) = user {
    println("hi, {u.name}")
}

if let Ok(cfg) = loadConfig() {
    apply(cfg)
} else {
    useDefaults()
}
```

Struct literals in the `if` head must be parenthesized — see §4.1.1
(restricted expression position). The same rule applies to `for` and
`match` heads.

#### 4.2.1 If as expression vs statement

`if` is both:

- **Expression** when the surrounding context expects a value
  (`let x = if ... else ...`). Both arms must be present and
  produce the same type.
- **Statement** when used purely for control flow (`if cond {
  body }` with no surrounding expression). The arm result is
  discarded and the `else` clause is optional.

The compiler decides expression vs statement contextually; there
is no syntactic distinction. An `if` with no `else` in expression
position is `E0671` (missing else branch).

#### 4.2.2 Information flow through if branches

Each branch's value carries the union of its own tag set and the
condition's tag set:

```osty
let user: #[taint("user_input")] User = ...
let label = if user.isAdmin { "admin" } else { user.name }
//          ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
//          label: #[taint("user_input")] String
//          (carries user's tag because the condition reads user)
```

This is conservative — even a static-string arm picks up the
condition's tag set if the condition consults a tainted value.
This conservativism prevents a class of timing-channel bugs where
the *choice* of branch leaks the condition to an observer.

#### 4.2.3 Capability flow through if branches

Capabilities captured by the surrounding scope remain in scope
inside both branches. There is no per-branch capability re-binding:

```osty
fn handle(net: Net, fs: Fs, mode: Mode) -> Result<(), Error> {
    if mode.usesNet {
        net.fetch(...)
    } else {
        fs.read(...)
    }
}
```

A `#[pure]` function may use `if` freely — the scope
constraint applies to the whole function body, not per branch.

### 4.3 Match Expressions

```osty
let area = match shape {
    Circle(r) -> 3.14 * r * r,
    Rect(w, h) -> w * h,
    Empty -> 0.0,
}
```

Match is exhaustive. `_` matches anything. Arms may be expressions or
blocks.

#### 4.3.1 Patterns

Supported patterns:

- **Wildcard:** `_` matches anything without binding.
- **Literal:** `42`, `"yes"`, `true`, `'\n'`.
- **Identifier:** `x` binds the matched value.
- **Tuple:** `(a, b)` destructures tuples.
- **Struct:** `User { name, age }`, `User { name, .. }` (ignore rest),
  `User { name: "alice", .. }` (match specific field value and bind
  nothing), `User { name: n, age }` (match and rename binding).
- **Variant:** `Some(x)`, `Ok(v)`, `Rect(w, h)`, `Empty`.
- **Range:** `0..=9`, `10..20`, `..=0`, `100..` (half-open ranges).
  Range patterns require an `Ordered` scrutinee. Numeric types and
  `Char` (e.g. `'a'..='z'`) are permitted; `String` and other types
  without a useful total order are a compile error.
- **Or:** `A | B | C` matches any alternative. All alternatives must
  bind the same names with the same types. Alternatives at different
  nesting depths are allowed (`A(B(x)) | C(D(x))`) as long as the
  bindings agree.
- **Literal patterns** are type-strict: `42 -> ...` does not match a
  `Float` scrutinee, and `'A' -> ...` does not match a `Byte`. There
  is no implicit numeric coercion in patterns.
- **Binding (`@`):** `name @ pattern` binds the whole matched value
  while also matching against `pattern`:
  ```osty
  match n {
      x @ 0..=9 -> "single digit: {x}",
      x @ 10..=99 -> "two digits: {x}",
      _ -> "more",
  }
  ```

Patterns nest. `Ok(Some(x))`, `Circle(r @ 0.0..=1.0)`, and
`User { name, age @ 18..=65 }` are all valid.

#### 4.3.2 Guards

A match arm may be conditioned on a boolean expression:

```osty
let label = match x {
    Some(n) if n > 0 -> "positive",
    Some(n) if n < 0 -> "negative",
    Some(_) -> "zero",
    None -> "missing",
}
```

Arms are tried in order; both the pattern and the guard must succeed.
Exhaustiveness treats guarded arms conservatively: a type is fully
covered only by arms without guards (or catch-alls). An otherwise
exhaustive match composed only of guarded arms requires a final
catch-all.

Guards may reference bindings introduced by the pattern — the guard
and the arm body share the same scope, so `Some(n) if n > 0` works as
expected. Guards may not introduce new bindings (no `if let` inside a
guard).

**Witness policy.** A non-exhaustive match diagnostic reports one
minimal missing pattern. For tuple and struct shapes, the witness
concretizes the leftmost missing component and uses `_` for the rest.
For closed enum, `Option`, and `Result` payloads, the witness recurses
into the missing payload shape; for open or scalar domains, the payload
is `_`. Guarded arms do not contribute to coverage.

#### 4.3.3 Match and v0.6 surfaces

`match` interacts with five v0.6 surfaces:

**Error contract pruning.** When matching on a function call that
carries `#[error_contract]`, exhaustiveness considers only contract
variants. Arms that target uncontracted variants emit `W0413` (dead-
per-contract). See §7.5.1.

**Match-compat fallback.** A function annotated
`#[match_compat("X.Y", fallback = name)]` (§3.14.4) wraps its
internal `match` so that a future enum variant added at version
later than `X.Y` falls into the named fallback handler instead of
crashing the exhaustiveness check.

**Information flow through match arms.** Each arm body inherits
the scrutinee's flow tag set on its bound variables:

```osty
let raw: #[taint("user_input")] String = ...
match parse(raw) {
    Some(parsed) -> use(parsed),    // parsed: #[taint("user_input")] T
    None -> defaultValue(),
}
```

The tag rides through the pattern destructuring — `Some(parsed)`
binds `parsed` with the same tag set the source had. Sanitization
must still happen explicitly before the value reaches a sink.

**Capability bindings in arm bodies.** Capabilities captured in
the surrounding scope remain in scope inside arm bodies:

```osty
fn dispatch(net: Net, evt: Event) -> Result<(), Error> {
    match evt {
        Event.Get(url)  -> net.fetch(url),       // net in scope
        Event.Post(url, body) -> net.send(url, body),
        Event.Close -> Ok(()),
    }
}
```

There is no per-arm capability re-binding; the closure-style
capture rules (§4.7.1) apply uniformly.

**`#[pure]` and match.** A `#[pure]` function may contain `match`
expressions; every arm body must itself be capability-free. The
scrutinee likewise has to be a value the function can compute without
consulting capabilities (since `#[pure]` rejects capability parameters,
this is automatic). v0.6 baseline does not gate against unordered
iteration of `Map.iter()` inside `#[pure]` — that responsibility moved
to the author after G39 was withdrawn.

### 4.4 Loops

```osty
for i in 0..10 { ... }
for i in 0..=10 { ... }
for i in 0..100 by 2 { ... }
for item in xs { ... }
for (k, v) in map { ... }

for cond { ... }                 // while-style
for { ... }                      // infinite

let winner = loop {
    let item = nextItem()
    if item.accepted { break item }
}

for x in xs {
    if found(x) { break }
}

'outer: for row in rows {
    for cell in row {
        if done(cell) { break 'outer }
        if skipRest(cell) { continue 'outer }
    }
}

for item in items {
    if !item.valid { continue }
    process(item)
}
```

**`for let` form.** Loop while a pattern match succeeds:

```osty
for let Some(x) = queue.pop() {
    process(x)
}

for let Ok(line) = reader.readLine() {
    handle(line)
}
```

On each iteration, the expression is evaluated and matched. If it fails,
the loop exits.

There is no C-style `for (init; cond; step)`.

**Range step.** `a..b by step` and `a..=b by step` set the iteration
stride for a range expression. `by` is contextual: outside range suffix
position it is an ordinary identifier.

`break` exits the innermost enclosing loop; `continue` skips to the next
iteration. A loop may be prefixed with a label (`'name: for ...` or
`'name: loop ...`); `break 'name` and `continue 'name` target that loop.
Unknown labels are `E0763`. Without a label, the target is the innermost
enclosing loop.

#### 4.4.1 Loop Expressions

`loop { ... }` is the value-returning unbounded loop form. It is distinct
from `for cond { ... }` and `for { ... }`, which are statement-style loops
with `()` result.

```osty
let firstHit = loop {
    let value = scanNext()
    if value.matches { break value }
}
```

The type of a `loop` expression is the common type of its `break value`
exits. A bare `break` exits with `()`; `break value` is valid only when
the target is a `loop` expression. For a labeled break, the value follows
the label: `break 'search result`.

#### 4.4.2 Loop forms summary

The complete catalogue of v0.6 loop forms:

| Form | Purpose | Result type | Cancellation point? |
|---|---|---|---|
| `for x in iterable { }` | Each-iteration over an `Iterable<T>` | `()` | No (use `thread.checkCancelled()`) |
| `for cond { }` | While-style loop | `()` | No |
| `for { }` | Infinite, no value | `()` | No |
| `for let Some(x) = expr { }` | Loop while pattern matches | `()` | No |
| `loop { ... break value }` | Value-returning loop | type of `break value` | No |

None of the loop forms is a *language-level* cancellation point —
the compiler does not insert cancel checks at loop heads. Tasks
that loop without making any stdlib blocking call must explicitly
call `thread.checkCancelled()?` to participate in cooperative
cancellation:

```osty
fn processQueue(jobs: List<Job>) -> Result<(), Error> {
    for job in jobs {
        thread.checkCancelled()?       // cooperative cancel check
        process(job)
    }
    Ok(())
}
```

A future revision may add compiler-inserted preemption at loop
backedges (§8.0); programs written to the explicit-check contract
keep working.

#### 4.4.3 Loops and capability flow

A loop body inherits the surrounding scope's capability bindings.
There is no per-iteration capability re-binding; `fs` named in the
function signature stays in scope for every iteration:

```osty
fn copyAll(fs: Fs, src: List<String>, dstDir: String) -> Result<(), Error> {
    for path in src {
        let bytes = fs.read(path)?       // fs reused per iteration
        fs.write("{dstDir}/{path}", bytes)?
    }
    Ok(())
}
```

Capability instances are typically interface values (fat pointer);
keeping them in scope across a loop is constant cost. There is no
hidden allocation per iteration.

#### 4.4.4 Loops and information flow

Loop iteration variables inherit the element type's flow tag set:

```osty
let lines: List<#[taint("user_input")] String> = [...]
for line in lines {
    // line: #[taint("user_input")] String
    // — must sanitize before reaching a sink
    let safe = std.html.escape(line)
    log.info("processed: {safe}")
}
```

The tag set is *per-iteration* — each `line` binding carries its
own tag. Loop body code that aggregates `line` into an outer
collection produces a tag-union for the collection element type
(per §21.5.1).

### 4.5 Error Propagation

```osty
// 라이브러리 함수는 `Fs` capability 를 받아 effect 를 명시 (§20).
fn loadConfig(fs: Fs, path: String) -> Result<Config, Error> {
    let text = fs.readToString(path)?
    let cfg: Config = json.parse(text)?
    Ok(cfg)
}

fn findActive(db: Db, id: Int) -> User? {
    let user = db.lookupUser(id)?
    if user.active { Some(user) } else { None }
}
```

The postfix `?` operator applies to `Result<T, E>` and `Option<T>`:

- On `Result<T, E>`: `Ok(v)` evaluates to `v`; `Err(e)` returns `Err(e)`
  from the enclosing function. The error type must be `E`, or `e` must
  satisfy `Error` with enclosing return `Result<_, Error>`.
- On `Option<T>`: `Some(v)` evaluates to `v`; `None` returns `None` from
  the enclosing function. The enclosing return must be `Option<_>`.

Mixing `Option<T>?` in a `Result<_, _>` function (or vice versa) is a
compile error. Convert explicitly with `Option.orError(msg)` or
`Result.ok()`.

The `?` operator chains freely with method calls:

```osty
let upper = fetchUser()?.getName()?.toUpper()
```

### 4.6 Optional Chaining and Nil-Coalescing

The `?.` operator accesses a field or calls a method on an `Option<T>`.
If `None`, the result is `None`; if `Some(v)`, the access is performed
on `v`:

```osty
let name: String? = user?.name
let city: String? = user?.address?.city
let len: Int? = user?.name?.len()
```

Chained access short-circuits on the first `None`.

The `??` operator supplies a default for `None`:

```osty
let name = user?.name ?? "anonymous"
let count = countCache?.value ?? 0
```

The right operand is evaluated only when the left is `None`. `??` binds
tighter than assignment but looser than comparison. `?.` binds at the
same precedence as `.`.

#### 4.6.1 `?.` and information flow

`?.` chain access preserves flow tags through every step:

```osty
let user: #[taint("user_input")] User? = ...
let city: #[taint("user_input")] String? = user?.address?.city
```

If the chain short-circuits on `None`, the result `None` carries
the same tag set the original optional carried — flow tracking is
not gated on the value being present.

#### 4.6.2 `??` and laziness

The right operand of `??` is evaluated lazily only when the left
side is `None`. Side-effecting defaults are uncommon but supported:

```osty
let logger = appConfig?.logger ?? defaultLogger()
```

`defaultLogger()` runs only when `appConfig` lacks a `logger`. The
flow tag of the result is the union of the present-side's tags
and the default-side's tags — *neither side's tag dominates*
because either could be the value the consumer sees.

#### 4.6.3 `??` is `Option`-only

`??` accepts `Option<T>` on the left and `T` on the right. Using
it on `Result<T, E>` is `E0672` — the recommended idiom is
`.unwrapOr(d)` for `Result`.

```osty
// ❌ E0672
let cfg = loadConfig() ?? defaultConfig()

// ✅
let cfg = loadConfig().unwrapOr(defaultConfig())
```

The asymmetry is intentional: `Result<T, E>` carries an error
value that `??` would silently discard. Forcing the explicit
`unwrapOr` keeps the discard visible at the call site.

### 4.7 Closures

```osty
let double = |x| x * 2
let add = |a, b| a + b
list.map(|x| x * 2)

list.map(|x| {
    let doubled = x * 2
    doubled + 1
})

let f = |x: Int| -> Int { x * 2 }
```

Closures capture by reference. Mutability is inherited from the captured
binding's declaration. A closure that outlives the block in which it was
created keeps the captured bindings alive (the GC roots them through the
closure value).

**Closure parameter patterns.** Any `LetPattern` (§3.2) is permitted as
a closure parameter, with the same destructuring and wildcard rules
applied at call time:

```osty
counts.entries().map(|(k, v)| "{k} = {v}")
users.map(|User { name, age }| "{name}({age})")
pairs.map(|(_, second)| second)
```

Only **irrefutable** patterns are allowed. Patterns that could fail to
match (enum variants like `Some(x)`, range patterns, or struct patterns
against multi-variant enums) are a compile error — call sites must
supply a value the closure can always destructure.

Annotations (`#[deprecated]`, `#[json]`, …) may not be applied to
closure expressions; annotations are a declaration-level feature.

> **Closure parameter pattern decision.** Patterned closure parameters are part of the
> front-end baseline. Irrefutable tuple/struct/wildcard/binding patterns
> bind as if the closure body began with a `let` destructure. Refutable
> literal, range, variant, or or-pattern parameters are rejected with
> `E0741`.

#### 4.7.1 Closures and capability capture

A closure that references a capability binding from its enclosing
scope captures the capability by reference, identical to any other
captured binding. The closure's lifetime extends the capability's
effective lifetime — the GC keeps the capability alive as long as
the closure is reachable:

```osty
fn buildHandler(net: Net) -> fn(String) -> Result<Bytes, Error> {
    |url| net.fetch(url)         // closure captures `net` by reference
}
```

Because capabilities are interface fat pointers, the captured
reference is constant-cost — no per-call lookup. Multiple
closures created from the same enclosing scope share the same
capability instance.

A closure that *does not* reference any capability is "pure" in
the sense that it runs without consulting any host effect. The
language does not enforce this distinction at the closure type
level — `fn(T) -> R` does not record capability dependencies.
Authors who want a strong "no effects" guarantee should define a
named function annotated `#[pure]` (§3.8.11) instead.

#### 4.7.2 Closures and information flow

A closure that captures a tainted value preserves the tag set
when invoked:

```osty
let raw: #[taint("user_input")] String = req.queryParam("q") ?? ""
let render = |suffix: String| "{raw} {suffix}"
let out = render("end")          // out: #[taint("user_input")] String
```

The closure body's expression result inherits both the captured
value's tags and the parameter's tags. There is no implicit
declassification at the closure boundary.

#### 4.7.3 Closures stored in data structures

A closure stored in a `List<fn(T) -> R>`, a `Map<K, fn(T) -> R>`,
or a struct field continues to satisfy the v0.6 capability and
flow rules — its type is the function shape, and the captured
state is opaque to the type system. This is why the recommended
pattern for "callback that needs an effect" is to take the
capability as a parameter rather than capture it:

```osty
// ❌ Captures `net` — opaque dependency.
let handlers: List<fn(String) -> Result<Bytes, Error>> = [
    |url| net1.fetch(url),
    |url| net2.fetch(url),
]

// ✅ Caller passes the capability — explicit dependency.
let handlers: List<fn(Net, String) -> Result<Bytes, Error>> = [
    |net, url| net.fetch(url),
    |net, url| net.fetchAlt(url),
]
```

The second form makes the dependency visible at the call site.

### 4.8 String Interpolation

See §1.6.3.

### 4.9 Member Access and Method Calls

```osty
user.name
user.greet()
User.new(...)
math.sqrt(2.0)
```

`.` for all member access. `::` only for turbofish.

**Generic-method decision.** In `obj.method::<T>(args)`, explicit
type arguments apply only to the method's own generic parameters. Generic
parameters from the receiver's owner type are already fixed by the
receiver type. Partial explicit method type arguments are not allowed:
provide exactly the method-local arity or omit them for inference.

`obj.method` as a function value is allowed only for non-generic
methods. A generic method must be wrapped explicitly:

```osty
let f = |x: Int| obj.method::<Int>(x)
```

The same rule applies to top-level generic functions: Osty does
not have first-class polymorphic function values or partial generic
application. `let f = identity` is legal only when `identity` is
non-generic; use `let f = |x: Int| identity::<Int>(x)` to fix a generic
callable's type arguments.

**Erased-callable decision.** A direct function or package-member
call uses declaration metadata, so default arguments and keyword
arguments are available there. Once the callable is stored as
`fn(...) -> ...`, that metadata is erased: calls through the function
value are positional-only and must pass exactly the declared arity.

#### 4.9.1 Method calls and capability flow

A method call `obj.method(args)` evaluates `obj` once, then
dispatches to the method. When `obj` is a capability instance
(e.g. `clock.now()`), the method call is the canonical site where
the capability's effect is observed. The compiler tracks each such
call for `osty audit --capabilities` reporting.

Method calls inherit the receiver's flow tag set when the method
preserves the source data:

```osty
let raw: #[taint("user_input")] String = ...
let upper = raw.toUpperCase()           // upper: #[taint("user_input")] String
let len = raw.len()                     // len: Int (no tag — primitive int)
```

The rule: if the method returns the receiver's data shape
(generally `String` → `String`, `List<T>` → `List<T>`), the tag
set rides through. If the method returns a primitive that does
not carry the source data (`len`, `count`), the tag set drops.
This is *not* declassification — the primitive simply doesn't
carry the source content.

#### 4.9.2 Static methods and namespaced calls

Static method calls (`Type.fnName(args)`) are not method calls in
the receiver sense; they are namespaced function calls. Capability
flow rules apply identically to ordinary function calls:

```osty
let parsed = Email.parse(form)?         // calls a free function under Email
let id = Uuid.parse(text)?              // same shape
```

The `Type.method` syntax is sugar for `<package>.<Type>.method` at
the resolver level; there is no implicit receiver. Authors should
not confuse this with method dispatch — `Email` is a type, not a
value.

### 4.10 Indexing

```osty
xs[0]                  // aborts on out-of-range
m["key"]               // aborts on missing key
xs.get(0)              // T?
m.get("key")           // V?
xs[2..5]               // slicing
s[2..5]                // String byte slicing; aborts on invalid UTF-8 boundary
```

**String indexing is in bytes, not Unicode scalars.** This matches the
Go and Rust convention: a `String` is an immutable UTF-8-encoded byte
sequence. `s.len()` returns the number of bytes; `s[i]` returns the
byte at index `i` as a `Byte`; `s[a..b]` returns a `String` slice (no
copy) that aborts at runtime if either endpoint falls inside a multi-
byte UTF-8 sequence.

For byte, Unicode-scalar, or grapheme iteration, use the explicit
`String` methods. These are intrinsic methods on `String`; higher-level
string processing helpers live in `std.strings`.

```osty
for c in s.chars() { ... }         // Iterator<Char>
for g in s.graphemes() { ... }     // Iterator<String> — extended grapheme clusters
let n = s.charCount()              // O(n) scan
```

`Bytes` indexing follows the same rules but never aborts on UTF-8
boundaries, since it carries no encoding contract.

#### 4.10.1 Indexing and information flow

Indexing preserves flow tags element-wise. `xs[i]` returns an
element with `xs`'s tag set; `xs[a..b]` returns a slice with the
same tag set:

```osty
let userInput: #[taint("user_input")] List<String> = ...
let first = userInput[0]              // first: #[taint("user_input")] String
let head = userInput[0..3]            // head: #[taint("user_input")] List<String>
```

The aborts-on-out-of-range semantics do not declassify — a panic
exits the process; recovery is not part of the language.

#### 4.10.2 Indexing and `#[pure]`

A `#[pure]` function may index `List<T>` and `String`
freely — the indexing operation is deterministic given the
collection and the index. `Map<K, V>` indexing (`m[k]`) is also
deterministic — the value at a given key is determined by the
map's contents.

The non-deterministic operation is *iteration order*, not
indexing. A `#[pure]` function may use `m["specific_key"]`
without issue; only `m.iter()` / `m.keys()` / `m.values()` are
flagged.

#### 4.10.3 Slicing patterns

Slice expressions (`xs[a..b]`) produce a new collection of the
same type. Common idioms:

| Idiom | Meaning |
|---|---|
| `xs[..n]` | First `n` elements |
| `xs[n..]` | All elements from index `n` |
| `xs[..]` | Whole collection (rarely useful — equivalent to `xs`) |
| `s[i..j]` | Substring; aborts on UTF-8 boundary mismatch |
| `bytes[i..j]` | Sub-byte-sequence; never aborts on boundary |

Slices share underlying storage with the source — no copy. This
keeps slicing constant-time but means a slice keeps the source
alive for the slice's lifetime (GC reachability rule).

### 4.11 Block Scope

Every `{ }` introduces a lexical scope. Scope exits occur when control
flow leaves the block (end of block, `return`, `break`/`continue` out
of loop, `?` propagation).

### 4.12 Defer

`defer` schedules an expression or block to run when the enclosing block
exits.

```osty
fn process(net: Net, fs: Fs, path: String) -> Result<(), Error> {
    let conn = net.dial("api.com", 443)?
    defer conn.close()

    let data = fs.readToBytes(path)?
    let _ = conn.send(data)?
    Ok(())
}
```

Inside a loop, `defer` runs at the end of each iteration:

```osty
for path in paths {
    let tmp = path + ".tmp"
    defer {
        ignoreError(fs.remove(tmp))
    }
    process(path)?
}
```

Rules:

1. `defer` is a statement.
2. The argument is an expression or block.
3. Multiple `defer`s in the same block run in LIFO order.
4. `defer` is scoped to the enclosing block. It is not valid at the
   top level of a script (§6); wrap top-level cleanup in an explicit
   `{ ... }` block.
5. Captured variables and expressions are evaluated at execution time.
6. Errors raised inside a deferred expression do not propagate.
7. `defer` runs on normal block exit, on `?`-propagated early return,
   and on task **cancellation** (§8.4.3). It does **not** run when
   the process terminates via `abort`, `panic` crossing the FFI bridge,
   `unreachable`, `todo`, or `os.exit`; those are immediate. If a
   deferred block itself terminates the process this way, remaining
   deferred blocks in the same LIFO stack are skipped.
8. Blocking calls inside a `defer` body are **not** cancellation
   points — cleanup is uninterruptible. Authors who need bounded
   cleanup must enforce their own timeout inside the `defer` body.
9. A deferred expression whose type is `Result<_, _>` produces an
   unused-result warning. Handle the result explicitly with one of the
   helpers from `std.process` (§10.1):

```osty
defer ignoreError(conn.close())
defer logError(f.close(), "file close failed")

// Or handle manually
defer {
    match db.close() {
        Ok(_) -> {},
        Err(e) -> log.warn("db close failed: {e.message()}"),
    }
}
```

### 4.13 Assignment

Assignment is a **statement**, not an expression. It never produces a
value and may not appear in expression position (`let x = (y = 1)` is
rejected by the grammar).

```osty
let mut n = 0
n = n + 1
n += 1
```

The left-hand side must be one of:

- a **mutable identifier** (`x` declared with `let mut`, or a `mut self`
  receiver parameter),
- a **field access** on a mutable struct value (`obj.field`,
  `self.field`),
- an **indexed element** on a mutable collection (`xs[i]`, `m[k]`).

The right-hand side is evaluated, then written to the place named by the
left-hand side. For `=`, the RHS type must match the LHS type except for
the numeric widening / float-promotion conversions allowed by §2.2.

#### 4.13.1 Compound Assignment

For each binary operator that produces a value of the same type as its
left operand — `+`, `-`, `*`, `/`, `%`, `&`, `|`, `^`, `<<`, `>>` —
Osty provides a compound assignment form.

| Compound | Desugars to       |
|----------|-------------------|
| `x += y` | `x = x + y`       |
| `x -= y` | `x = x - y`       |
| `x *= y` | `x = x * y`       |
| `x /= y` | `x = x / y`       |
| `x %= y` | `x = x % y`       |
| `x &= y` | `x = x & y`       |
| `x \|= y` | `x = x \| y`     |
| `x ^= y` | `x = x ^ y`       |
| `x <<= y`| `x = x << y`      |
| `x >>= y`| `x = x >> y`      |

Semantics:

1. Compound assignment is a **statement** with the same precedence as
   plain assignment (lowest, right-associative). It is grammar-level
   statement-only — `(x += 1)` is rejected.
2. The LHS place is evaluated **exactly once**. For an indexed target
   `xs[i] += v`, the index expression `i` is evaluated once, not twice.
3. Type rules follow the corresponding binary operator in §4 and §2:
   `x op= y` is well-typed iff `x op y` is well-typed and its result
   type is compatible with the LHS.
4. The LHS must be a valid mutable place (same rules as §4.13).
5. `+=` on `String` is equivalent to concatenation: `s += other` is
   `s = s + other`. Numeric compound assignment follows the same
   widening / float-promotion and narrowing-rejection rules as the
   corresponding binary operator and final assignment (§2.2).
6. Compound assignment on an immutable binding or field produces the
   same diagnostic as plain assignment (`E0601` et al.); there is no
   separate code.

There are no compound forms for `&&`, `||`, `??`, or comparison
operators. `++` and `--` are explicitly excluded (§14) and are not
reintroduced by the compound family.

### 4.14 Composing v0.6 surfaces in expression position

The grammar in §4.1 – §4.13 is unchanged from v0.5; this section
catalogues how the v0.6 attestation surfaces *appear* inside the
expressions a programmer actually writes. None of these are new
syntax — they are the existing constructs interacting with the v0.6
annotation set.

#### 4.14.1 `?` and capability-shaped errors

`?` propagates `Err(...)` from a capability call without unwrapping
the capability itself; the capability remains in scope across the
propagation.

```osty
fn loadUser(fs: Fs, id: Int) -> Result<User, Error> {
    let path = "users/{id}.json"
    let text = fs.readToString(path)?       // Err(Cancelled) or Err(NotFound) bubbles
    let user: User = json.parse(text)?
    Ok(user)
}
```

The function's return type is `Result<User, Error>`; the *concrete*
errors that flow out are whatever `Fs.readToString` and `json.parse`
produce, widened to `Error` at the up-cast site (§7.4). If the
caller wants to discriminate, `err.downcast::<FsError>()` or a
`#[error_contract(...)]` annotation makes that explicit.

#### 4.14.2 `match` against `#[error_contract]` returns

Match exhaustiveness against a contracted Result is computed against
the contract variants, not the underlying enum's full surface
(§7.5).

```osty
match createUser(email, db) {
    Ok(uid) -> uid,
    Err(UserCreateError.EmailFormat) -> ...,
    Err(UserCreateError.DomainBlocked(d)) -> ...,
    Err(UserCreateError.DbConflict(_)) -> ...,
    // No `_ -> ...` needed if the contract enumerates exactly these
    // three variants; future enum additions become Err arms via
    // #[match_compat] (§3.14.4).
}
```

#### 4.14.3 String interpolation and tainted bindings

Interpolation `"{expr}"` desugars to `expr.toString()` per §17. The
result `String` carries the tag set of `expr` — interpolation does
not declassify:

```osty
fn welcome(user: #[taint("user_input")] User) -> String {
    "hello {user.name}"     // result is #[taint("user_input")] String
}
```

A sink that requires a clean `html_safe` tag rejects the result
unless `std.html.escape` (or another registered sanitizer) sat
between the binding and the sink.

#### 4.14.4 Closures capture capabilities, not ambient bindings

`#[ambient(...)]` binds names *only* inside the entry-point function
body. A closure that runs later (in a `taskGroup`, in a callback
passed to `iter.map`, in `defer`) needs the binding to be in lexical
scope at the closure site:

```osty
#[ambient(net)]
fn main() {
    taskGroup(|g| {
        g.spawn(|| net.fetch("https://a"))   // ✅ captures `net` from main
        g.spawn(|| fetchOne(net, "https://b"))  // ✅ explicit pass
    })
}

fn fetchOne(net: Net, url: String) -> Result<Bytes, Error> {
    net.fetch(url)
}
```

The closure inside `g.spawn(|| ...)` does not "inherit ambient" — it
captures `net` by ordinary closure semantics. This is why
`#[ambient]` is restricted to entry-point functions: ambient
forwarding with structured concurrency would require an extra
mechanism that v0.6 deliberately does not introduce (§14.5 rule 2).

#### 4.14.5 `defer` is capability-blind

A `defer`red expression runs whatever code it contains; the cleanup
path can therefore call methods on the capability:

```osty
fn copyFile(fs: Fs, src: String, dst: String) -> Result<(), Error> {
    let r = fs.open(src)?
    defer logError(r.close(), "close src failed")

    let w = fs.createWriter(dst)?
    defer logError(w.close(), "close dst failed")

    io.copy(w, r)?
    Ok(())
}
```

`defer` runs on the cancel path too (§4.12 rule 7), so the close
calls execute even when the surrounding `taskGroup` is being torn
down. Blocking calls inside the `defer` body do **not** honor
cancellation — cleanup is uninterruptible (§4.12 rule 8) — so
authors who need bounded close-time use a timeout helper inside
the body.

---
