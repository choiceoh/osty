## 17. Display and Format Protocol

Osty v0.6 defines value-to-string conversion through a single
interface, `ToString`. String interpolation `"{expr}"` (§4.8), the
`println` / `print` / `eprint` family, and the formatter's
`Display` rendering all dispatch through `toString()`. The interface
is structural — any type that defines `fn toString(self) -> String`
satisfies it.

`ToString` interacts with two v0.6 surfaces:

- **Capability vs pure rendering.** `toString()` is a pure
  declaration on the value itself — it receives no capability and
  must not perform an effect. `print`/`println` consume the result
  and *write it* through the ambient `Console` capability (§20.9.7),
  which is where the effect actually lives. This split lets value
  types implement `ToString` without becoming capability-coupled.
- **Information flow.** A `ToString` impl preserves any
  `#[taint(...)]` tag on its self argument: the tag rides on the
  rendered `String` and must be sanitized before reaching a sink
  such as `http.respondHtml` (§21.8). The interface itself is
  flow-tag-agnostic.

Machine-readable intent annotations (`#[purpose]`, `#[example]`,
`#[fixture]` per §3.12) operate at a different layer (documentation
/ context export) and do not interact with `ToString`.

String interpolation and the `print*` family are defined in terms of a
single interface:

```osty
pub interface ToString {
    fn toString(self) -> String
}
```

**`{expr}` interpolation.** In a string literal, `{expr}` is rewritten
to `expr.toString()` and the result spliced in. Format specifiers
(width, precision, base) are not part of the interpolation grammar;
use explicit method calls for non-default formatting (e.g.
`{n.toFixed(2)}`, `{n.toString(base: 16)}`).

**Built-in instances.** Every primitive listed in §2.6.5 implements
`ToString` with its standard textual form:

```
42.toString()           // "42"
3.14.toString()          // "3.14"
true.toString()          // "true"
'A'.toString()           // "A"
"hi".toString()          // "hi"  (identity)
b"hi".toString()         // see §2.4.1; returns Result, separate from ToString
```

For `Bytes`, `ToString` produces a hex-escaped form (e.g. `"\\x68\\x69"`
for `b"hi"`); the UTF-8 reinterpretation is the explicit `Bytes.toString
() -> Result<String, Error>` (which shadows the interface name —
disambiguate by inference at the call site).

**Auto-derivation for `struct` and `enum`.** The compiler synthesizes
a `toString` implementation when the user does not provide one:

- `struct User { name: String, age: Int }` →
  `"User { name: \"alice\", age: 30 }"`
- `enum Shape { Circle(Float), Empty }` →
  `"Circle(1.5)"`, `"Empty"`

The auto-derived form is intended for debugging and log output. It
quotes string fields, recursively calls `toString` on each component,
and omits any field annotated `#[json(skip)]` (which is interpreted as
"do not expose externally"). User implementations override the
auto-derived one.

**Collections.**

```
List<T>:    T: ToString  ⇒ List<T>: ToString    // "[1, 2, 3]"
Set<T>:     T: ToString  ⇒ Set<T>: ToString
Map<K,V>:   K: ToString + V: ToString ⇒ Map<K,V>: ToString
Tuple:      all components ToString ⇒ tuple ToString
Option/Result: as in §2.6.5
```

**`println(x)` and friends.** `print(x)`, `println(x)`, `eprint(x)`,
`eprintln(x)` accept any `T: ToString` and emit `x.toString()` to the
respective stream (`println` adds a trailing `\n`).

**Relationship to `dbg`.** `dbg(x)` (§10.1) prints a developer-oriented
form including source location and the unprocessed expression text. It
is built on top of `ToString` for the value portion.

### 17.1 Sealed types and `ToString`

A `#[sealed_construct]` type (§3.4.5) implements `ToString` like any
other struct — there is no special interaction. The auto-derived form
exposes private fields as text, which is sometimes the wrong choice
for sealed types since a parsed `Email` value is meant to be opaque
to consumers:

```osty
#[sealed_construct(parse)]
pub struct Email {
    local: String,
    domain: String,

    pub fn parse(s: String) -> Email? { ... }

    // Author-supplied — render the canonical text form, not the
    // auto-derived `Email { local: ..., domain: ... }` debug form.
    pub fn toString(self) -> String {
        "{self.local}@{self.domain}"
    }
}

println(Email.parse("alice@example.com")?)   // alice@example.com
```

The convention across the v0.6 stdlib sealed types (`Email`, `Url`,
`Path`, `SqlIdent`, `Duration`, `Uuid`) is **always** to override
`toString()` with the canonical reverse of `parse(...)` — round-trip
through `ToString` ↔ `Type.parse` is part of the public contract.

### 17.2 Interaction with information flow

`toString()` preserves any `#[taint(σ)]` tag on its self argument:
the rendered `String` carries the same tag set, so a tainted value
flowing into `"{user.email}"` interpolation produces a tainted
`String`, which the type checker tracks into whatever sink the
interpolation result lands in.

```osty
fn render(form: #[taint("user_input")] FormData) -> String {
    "submitted: {form.message}"     // result is also #[taint("user_input")]
}

// Sink that requires `html_safe` rejects the tainted result —
// authors must sanitize first.
http.respondHtml(render(form))      // E0901 — missing #[sanitizes(...)]
```

Sanitization happens *before* the value reaches `toString()`. There
is no "format-time sanitizer" — that would hide the data flow that
§21 is explicitly designed to surface.

### 17.3 Capability-free guarantee

`toString()` is declared without any capability parameter. The
runtime does not synthesize one for the call, so a `ToString`
implementation cannot read the clock, query the environment, or open
a file. This is what lets the `print*` family safely route through
ambient `Console` (§20.9.7) — the rendering work is pure, and only
the *write* is effectful.

A user implementation that *does* need to consult an effect to
render a value should not abuse `ToString`; instead expose a
deliberate `render(self, clock: Clock) -> String` method that the
caller passes the capability to. The naming convention `render*`
keeps `ToString` free of hidden dependence.

### 17.4 `dbg` and v0.6 surfaces

`dbg(value)` (§10.1) prints a developer-oriented form including
source location and the unprocessed expression text. It is
designed for ad-hoc debugging, not for production output.

The v0.6 interactions:

**Capability dispatch.** `dbg` writes through the ambient
`Console` capability. Calling `dbg` outside an `#[ambient(console)]`
context is `E0780` (no Console available). This means library
functions cannot call `dbg` — only entry-point code and tests.

**Production use rejection.** `dbg` calls in `pub fn` declarations
are flagged by `osty lint` (`L0050`) — the assumption is that
`dbg` is for transient debugging and should be removed before
publishing.

**Information flow.** `dbg(taintedValue)` writes the tainted value
to the stdout/stderr console. This is *not* a sink in the §21
sense — `dbg` is debug-time only and does not have a registered
required tag. Authors who want production-safe value rendering use
`log.info` (§10.10) or explicit `console.println` with sanitization.

### 17.5 `ToString` and trait implementation

User types implement `ToString` by defining a `toString(self) ->
String` method:

```osty
pub struct Money {
    amount: Int,
    currency: String,

    pub fn toString(self) -> String {
        let dollars = self.amount / 100
        let cents = self.amount % 100
        "{self.currency}{dollars}.{cents.toString().padLeft(2, '0')}"
    }
}

println(Money { amount: 1234, currency: "$" })  // $12.34
```

The user's `toString` overrides the auto-derived form. To opt out
of the auto-derive entirely (so that the type is *not* `ToString`
unless explicitly implemented), declare the type with
`#[no_auto_to_string]` (rare; most types benefit from the
auto-derived form for debugging).

#### 17.5.1 Auto-derived form for sealed types

Auto-derived `toString` for a `#[sealed_construct]` struct exposes
private fields. The convention is to override:

```osty
#[sealed_construct(parse)]
pub struct Email {
    local: String,
    domain: String,

    pub fn toString(self) -> String { "{self.local}@{self.domain}" }
}
```

Without the override, `dbg(email)` would print
`Email { local: "alice", domain: "example.com" }` — useful for
debugging but inappropriate for production rendering. The override
makes the canonical text form the default `toString` output.

#### 17.5.2 `ToString` and reproducibility

`toString` is implicitly reproducible — the auto-derived form is
deterministic (it walks fields in declaration order). User
overrides should preserve determinism:

- Don't consult `Clock` / `Rng` / `Env` / `Fs` / `Net` /
  `Process` — capabilities are not parameters of `toString`.
- Don't iterate `Map.iter()` or `Set.iter()` (use
  `entriesSorted()`).
- Don't depend on `std.ref.same(a, b)` — pointer identity may vary
  across runs.

A `#[reproducible(scope = "portable")]` function may freely call
`x.toString()` — the result is determined by `x`'s value.

---
