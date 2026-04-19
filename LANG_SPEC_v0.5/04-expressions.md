## 4. Expressions

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

**v0.4 witness policy.** A non-exhaustive match diagnostic reports one
minimal missing pattern. For tuple and struct shapes, the witness
concretizes the leftmost missing component and uses `_` for the rest.
For closed enum, `Option`, and `Result` payloads, the witness recurses
into the missing payload shape; for open or scalar domains, the payload
is `_`. Guarded arms do not contribute to coverage.

### 4.4 Loops

```osty
for i in 0..10 { ... }
for i in 0..=10 { ... }
for item in xs { ... }
for (k, v) in map { ... }

for cond { ... }                 // while-style
for { ... }                      // infinite

for x in xs {
    if found(x) { break }
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

`break` exits the innermost enclosing loop; `continue` skips to the next
iteration. Labelled `break` / `continue` are not provided.

### 4.5 Error Propagation

```osty
fn loadConfig(path: String) -> Result<Config, Error> {
    let text = fs.readToString(path)?
    let cfg: Config = json.parse(text)?
    Ok(cfg)
}

fn findActive(id: Int) -> User? {
    let user = lookupUser(id)?
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

> **v0.4 decision.** Patterned closure parameters are part of the
> front-end baseline. Irrefutable tuple/struct/wildcard/binding patterns
> bind as if the closure body began with a `let` destructure. Refutable
> literal, range, variant, or or-pattern parameters are rejected with
> `E0741`.

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

**v0.4 generic-method decision.** In `obj.method::<T>(args)`, explicit
type arguments apply only to the method's own generic parameters. Generic
parameters from the receiver's owner type are already fixed by the
receiver type. Partial explicit method type arguments are not allowed:
provide exactly the method-local arity or omit them for inference.

`obj.method` as a function value is allowed only for non-generic
methods. A generic method must be wrapped explicitly:

```osty
let f = |x: Int| obj.method::<Int>(x)
```

The same rule applies to top-level generic functions: Osty v0.4 does
not have first-class polymorphic function values or partial generic
application. `let f = identity` is legal only when `identity` is
non-generic; use `let f = |x: Int| identity::<Int>(x)` to fix a generic
callable's type arguments.

**v0.4 erased-callable decision.** A direct function or package-member
call uses declaration metadata, so default arguments and keyword
arguments are available there. Once the callable is stored as
`fn(...) -> ...`, that metadata is erased: calls through the function
value are positional-only and must pass exactly the declared arity.

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

For Unicode-scalar or grapheme iteration, use the explicit converters
exposed by `std.strings`:

```osty
for c in s.chars() { ... }         // Iterator<Char>
for g in s.graphemes() { ... }     // Iterator<String> — extended grapheme clusters
let n = s.charCount()              // O(n) scan
```

`Bytes` indexing follows the same rules but never aborts on UTF-8
boundaries, since it carries no encoding contract.

### 4.11 Block Scope

Every `{ }` introduces a lexical scope. Scope exits occur when control
flow leaves the block (end of block, `return`, `break`/`continue` out
of loop, `?` propagation).

### 4.12 Defer

`defer` schedules an expression or block to run when the enclosing block
exits.

```osty
fn process(path: String) -> Result<(), Error> {
    let f = fs.open(path)?
    defer f.close()

    let conn = net.connect("api.com")?
    defer conn.close()

    let data = f.read()?
    let result = conn.send(data)?
    Ok(())
}
```

Inside a loop, `defer` runs at the end of each iteration:

```osty
for path in paths {
    let f = fs.open(path)?
    defer f.close()
    process(f)?
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
   the process terminates via `abort`, `unreachable`, `todo`, or
   `os.exit`; those are immediate.
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
left-hand side. For `=`, the RHS type must match the LHS type; no
implicit numeric conversion occurs.

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
   `s = s + other`. Numeric mixing between `Int` and `Float64` is
   rejected, same as `+` (no implicit conversion).
6. Compound assignment on an immutable binding or field produces the
   same diagnostic as plain assignment (`E0601` et al.); there is no
   separate code.

There are no compound forms for `&&`, `||`, `??`, or comparison
operators. `++` and `--` are explicitly excluded (§14) and are not
reintroduced by the compound family.

---
