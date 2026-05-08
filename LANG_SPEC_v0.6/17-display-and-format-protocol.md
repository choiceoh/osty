## 17. Display and Format Protocol

Osty v0.6 defines value-to-string conversion through a single
interface, `ToString`. String interpolation `"{expr}"` (§4.8), the
`println` / `print` / `eprint` family, and the formatter's
`Display` rendering all dispatch through `toString()`. The interface
is structural — any type that defines `fn toString(self) -> String`
satisfies it. v0.6 adds no new format protocol surface; the
machine-readable intent annotations (`#[purpose]`, `#[example]`,
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

---
