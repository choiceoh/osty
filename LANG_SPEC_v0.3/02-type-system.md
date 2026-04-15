## 2. Type System

### 2.1 Primitive Types

```
Int                                  // signed 64-bit
Int8, Int16, Int32, Int64
UInt8, UInt16, UInt32, UInt64
Byte                                 // alias for UInt8
Float                                // alias for Float64
Float32, Float64
Bool
Char                                 // Unicode scalar value (32-bit)
String                               // UTF-8 byte sequence, immutable
Bytes                                // immutable byte array
Never                                // bottom type; type of expressions that do not return
```

`Int` is fixed at 64 bits regardless of platform. There is no `UInt` type
matching machine word size; use `Int` for sizes, counts, and indices.

`Never` is the type of expressions that never produce a value.

**`Char` — Unicode scalar value.** `Char` represents a single Unicode
scalar value: any code point in the ranges `U+0000 – U+D7FF` or
`U+E000 – U+10FFFF`. The surrogate range `U+D800 – U+DFFF` is **not**
representable.

- Character and `\u{...}` escapes that encode a surrogate are a
  compile error:
  ```osty
  let c: Char = '\u{D800}'   // ERROR: surrogate code point
  ```
- `Int.toChar(self) -> Char` aborts when the value is out of range or
  in the surrogate block.
- `Char.fromInt(n: Int) -> Char?` is the safe converter; it returns
  `None` for invalid code points.
- `Char.toInt(self) -> Int` is total (the scalar value as a signed
  integer).

Zero-sized structs (e.g. `struct Marker {}`) are allowed. They occupy no
storage; collections and struct fields treat them as ordinary values.

### 2.2 Numeric Conversions

No implicit numeric conversions between variables. Conversions via
methods:

```osty
let a: Int = 5
let b: Int64 = a.toInt64()
let c: Int32 = big.toInt32()?           // Err if out of range
let f: Float = a.toFloat()
```

Lossy conversions return `Result<T, Error>`; lossless return `T`.

**Literal inference.** Numeric literals are polymorphic until their
type is fixed by context. A literal without explicit suffix adopts the
type required by its usage:

```osty
let a: Float = 5              // 5 is Float
let b: Int32 = 100            // 100 is Int32
let c: Int = a.toInt()        // no auto-conversion from variable

fn f(x: Int64) { ... }
f(42)                         // 42 is Int64
```

A literal must fit in its inferred type; `let x: UInt8 = 300` is a
compile error.

If no context fixes the type, integer literals default to `Int` and
float literals default to `Float`.

### 2.3 Numeric Overflow

Arithmetic operators check for overflow and abort the program on overflow:

```osty
a + b               // aborts on overflow
a - b
a * b
```

Explicit alternatives:

```osty
a.wrappingAdd(b)    // modular arithmetic
a.checkedAdd(b)     // T?
a.saturatingAdd(b)  // clamps to T.MIN or T.MAX
```

**Shifts.** `a << b` and `a >> b` abort when `b` is negative or
`b ≥ bit-width(a)`. A shift by `0` is the identity. Explicit
alternatives mirror arithmetic:

```osty
a.wrappingShl(b)    // b mod bit-width(a), then shift
a.wrappingShr(b)
a.checkedShl(b)     // T?
a.checkedShr(b)
```

For unsigned types `>>` is logical (zero-fill); for signed types `>>`
is arithmetic (sign-extend).

**Division and modulo.** Integer division by zero aborts. Integer
modulo by zero aborts. The `%` operator follows the **dividend**
sign (C/Go convention): `-5 % 3 == -2`. For division-specific
overflow handling:

```osty
a.wrappingDiv(b)    // aborts only on b = 0; other cases wrap
a.wrappingMod(b)    // same
a.checkedDiv(b)     // T? — None on b = 0 or overflow
a.checkedMod(b)
a.saturatingDiv(b)  // clamps to T.MIN / T.MAX on overflow; aborts on b = 0
```

`Int.MIN.abs()` overflows (there is no positive counterpart) and
**aborts**. Use `MIN.checkedAbs()` (returns `None`) or
`MIN.wrappingAbs()` (returns `MIN`) for recovery.

**`pow`.** `Int.pow(exp: Int) -> Int` aborts when `exp < 0` (no integer
result). Use `Float.pow` for fractional/negative exponents.

### 2.4 Composite Types

```osty
struct Point { x: Int, y: Int }

enum Shape {
    Circle(Float),
    Rect(Float, Float),
    Empty,
}

interface Writer {
    fn write(self, data: Bytes) -> Result<Int, Error>
}

(Int, String, Bool)         // tuple type
fn(Int, Int) -> Int         // function type

List<T>
Map<K, V>
Set<T>
T?                          // syntactic sugar for Option<T>
Option<T>                   // canonical form
Result<T, E>
```

#### 2.4.1 The `Bytes` Type

`Bytes` is an immutable sequence of bytes (`UInt8`). It complements
`String` (an immutable sequence of UTF-8 encoded bytes) and is the
canonical type for binary data, I/O buffers, and FFI byte transport.

**Literals.** Two forms:

```osty
b'A'                  // single byte; ASCII only; Byte (= UInt8)
b"hello"              // byte sequence; ASCII only; Bytes
b"binary\x00data"     // \xNN escapes for non-printable bytes
```

The byte-string literal `b"..."` accepts only printable ASCII characters
plus the escapes `\n`, `\r`, `\t`, `\\`, `\"`, `\0`, and `\xNN`. It does
not interpolate. To embed non-ASCII data, use `\xNN` or build the value
programmatically with `Bytes.from(...)`.

**API.**

```
Bytes.len(self) -> Int
Bytes.isEmpty(self) -> Bool
Bytes.get(self, i: Int) -> Byte?
Bytes[i] -> Byte                 // aborts on out-of-range
Bytes[a..b] -> Bytes              // byte slicing; aborts on out-of-range
Bytes.concat(self, other: Bytes) -> Bytes
Bytes.toString(self) -> Result<String, Error>   // verifies UTF-8

String.toBytes(self) -> Bytes                   // zero-copy reinterpret
Bytes.from(items: List<Byte>) -> Bytes
```

`Bytes` implements `Equal`, `Ordered` (lexicographic), and `Hashable`.

### 2.5 Optional Type Sugar

`T?` is syntactic sugar for `Option<T>`. They are interchangeable at all
type positions. The formatter normalizes to `T?`.

### 2.6 Interfaces

An `interface` declaration specifies a set of method signatures,
optionally with default implementations. Any concrete type whose methods
match the interface's signatures satisfies it automatically (structural
typing).

#### 2.6.1 Composition

```osty
interface Reader {
    fn read(self, buf: Bytes) -> Result<Int, Error>
}

interface ReadWriter {
    Reader
    Writer
}
```

#### 2.6.2 Default Methods

```osty
interface Error {
    fn message(self) -> String
    fn source(self) -> Error? { None }
}
```

Default bodies may call other methods on the interface, use `self` and
`Self`, but may not access fields.

#### 2.6.3 The `Self` Type

Inside an interface body, `Self` refers to the implementing type. Inside
a `struct` or `enum` body, `Self` refers to the type being declared.

#### 2.6.4 Built-in Interfaces

```osty
interface Equal {
    fn eq(self, other: Self) -> Bool
    fn ne(self, other: Self) -> Bool { !self.eq(other) }
}

interface Ordered {
    Equal
    fn lt(self, other: Self) -> Bool
    fn le(self, other: Self) -> Bool { self.lt(other) || self.eq(other) }
    fn gt(self, other: Self) -> Bool { !self.le(other) }
    fn ge(self, other: Self) -> Bool { !self.lt(other) }
}

interface Hashable {
    Equal
    fn hash(self) -> Int
}
```

Comparison operators desugar to `Equal`/`Ordered` calls on implementing
types. Primitives use built-in comparison directly, **and** they are
considered to implement the corresponding interfaces — so a generic
function with bound `T: Ordered` accepts `Int`, `Float`, `String`, etc.

#### 2.6.5 Built-in Instances

The compiler treats the following types as implementing the interfaces
listed:

| Type | `Equal` | `Ordered` | `Hashable` | `ToString` (§17) |
|---|:---:|:---:|:---:|:---:|
| `Int`, `Int8…Int64`, `UInt8…UInt64`, `Byte` | ✓ | ✓ | ✓ | ✓ |
| `Float`, `Float32`, `Float64` | ✓ | ✓ † | ✗ ‡ | ✓ |
| `Bool` | ✓ | ✓ (false < true) | ✓ | ✓ |
| `Char` | ✓ | ✓ (scalar order) | ✓ | ✓ |
| `String` | ✓ | ✓ (lexicographic by byte) | ✓ | ✓ |
| `Bytes` | ✓ | ✓ (lexicographic) | ✓ | ✓ (hex-escaped) |
| Tuple `(T, U, …)` | ✓ if all components do | — | ✓ if all components do | ✓ if all components do |
| `Option<T>` | ✓ if `T: Equal` | ✓ if `T: Ordered` (`None < Some`) | ✓ if `T: Hashable` | ✓ |
| `Result<T, E>` | ✓ if both | — | ✓ if both | ✓ |

† `Float` ordering follows IEEE-754 total ordering: `NaN` is greater
than all finite and infinite values; `-0.0 < 0.0`.

‡ `Float` is **not** `Hashable` because `==` and `hash` would disagree
under `NaN` semantics. Convert via `f.toBits()` if you must hash.

**Collections.** Auto-derivation:

```
List<T>:    T: Equal     ⇒ List<T>: Equal
List<T>:    T: Hashable  ⇒ List<T>: Hashable
Set<T>:     T: Equal     ⇒ Set<T>: Equal
Set<T>:     T: Hashable  ⇒ Set<T>: Hashable
Map<K,V>:   K: Equal    + V: Equal    ⇒ Map<K,V>: Equal
Map<K,V>:   K: Hashable + V: Hashable ⇒ Map<K,V>: Hashable
```

`Ordered` is **not** auto-derived for collections.

User code cannot override the built-in `Equal`/`Hashable` instances of
collection types; they are structural by definition. (Resolves G1 from
the v0.1 gap list.)

### 2.7 Generics

```osty
fn first<T>(xs: List<T>) -> T? { ... }

struct Stack<T> {
    items: List<T>,
}
```

#### 2.7.1 Constraints

Type parameters may be constrained by any interface:

```osty
fn max<T: Ordered>(a: T, b: T) -> T {
    if a > b { a } else { b }
}

fn sortUnique<T: Ordered + Hashable>(xs: List<T>) -> List<T> { ... }
```

#### 2.7.2 Type Argument Specification

Use turbofish `::<...>` for explicit type arguments at expression
positions:

```osty
let cfg = json.parse::<Config>(text)?
```

The token following `::` **must** be `<`. Any other token is a parse
error:

```
expected '<' after '::', got 'Foo'. Did you mean '.'?
```

This was open issue O6 — `::` is exclusively the turbofish prefix, never
a path separator (path separator is `.`).

In **type position** (e.g. `let xs: List<List<Int>>`), the `<...>` form
is unambiguous and `::` is not required. The lexer emits `>>`, `>=`,
`>>=` as single tokens via maximal munch; the type parser splits them
back into `>` + `>` (or `>` + `=`) when a `>` is expected. This
"splittable `>`" rule (open issue O4) lets nested generics like
`List<List<Map<String, Int>>>` parse without explicit space between the
closing `>`s.

Turbofish on **enum variant construction** is not supported — the type
context infers the instantiation:

```osty
let x: Option<Int> = Some(5)             // OK
let x = Option::<Int>::Some(5)           // ERROR — turbofish not valid on variants
```

Generic **type parameters with default values** (e.g. `struct Pair<T, U = T>`)
are not provided.

#### 2.7.3 Compilation Model

Osty generics are **monomorphized**. At each distinct instantiation
(e.g. `List<Int>` and `List<String>`), the compiler emits a separate,
fully specialized copy of the generic definition. Consequences:

- **Zero runtime cost** per generic call — no type-parameter lookup,
  no boxing of primitives, no dispatch through a table.
- **Binary size grows** with the number of distinct instantiations.
  Generic code used with many type arguments is a deliberate design
  choice.
- **Go FFI mapping is direct**: `List<Int>` maps to `[]int64`,
  `Map<String, Int>` maps to `map[string]int64`, etc. (§12.2).
- **Generic function bodies are visible across packages.** When an
  imported generic function is called with a new type argument, the
  compiler reinstantiates it at the call site. The generic body is
  therefore effectively "header-like" — it must remain in the source
  distribution for downstream packages to use it with new type
  arguments. Non-generic functions retain the usual separate-
  compilation model.

**Interface values are a separate mechanism.** A function that names an
interface in parameter position, e.g. `fn f(x: Ordered)`, does **not**
trigger monomorphization. The parameter `x` is a fat pointer — a pair
of (data pointer, vtable pointer). Dispatch to methods uses the vtable.
This is the analogue of Rust's `dyn Trait`:

```osty
fn byGeneric<T: Ordered>(x: T) { ... }       // monomorphized per T
fn byInterface(x: Ordered) { ... }           // single body, vtable dispatch
```

`Error` retains its nominal type tag (§7.4) across both forms — the
runtime tag is orthogonal to the monomorph/vtable choice.

**Recursive generic types** are allowed:

```osty
pub struct Node<T> {
    pub value: T,
    pub next: Node<T>?,
}
```

**Generic methods on structs and enums** are independent of the
enclosing type's generics:

```osty
pub struct Stack<T> {
    items: List<T>,

    pub fn pushMapped<U>(mut self, xs: List<U>, f: fn(U) -> T) { ... }
}
```

**Variance is invariant** on all type parameters. `List<Cat>` and
`List<Animal>` are unrelated types even when `Cat` structurally
satisfies `Animal`. Osty does not provide declaration-site or use-site
variance annotations (no `in`/`out`, no wildcards).

### 2.8 Reference vs Value Semantics

| Type category | Semantics |
|---|---|
| Primitives | Value |
| `String`, `Bytes` | Value (immutable) |
| Tuples | Value (recursively) |
| `struct`, `enum` | Reference |
| `List`, `Map`, `Set` | Reference |
| Function types (closures) | Reference |

### 2.9 Equality and Hashing

`==` and `!=` for primitive types use built-in comparison. For `struct`,
`enum`, tuple, and collection types, `==` invokes the `Equal` interface.

**Automatic derivation.** The compiler provides automatic implementations
for `struct`, `enum`, and tuple types:

- `Equal` is derived when all components implement `Equal`.
- `Hashable` is derived when all components implement `Hashable`.

Automatic `Hashable` derivation combines hashes of fields in declaration
order.

User-provided implementations override automatic ones for `struct` and
`enum` types. **Built-in instances on primitives, `Option`, `Result`,
and the collection types `List`/`Map`/`Set` cannot be overridden** —
their definitions are structural and global. See §2.6.5 for the full
table.

`Ordered` is never automatically derived for `struct`, `enum`, or
collection types.

**Float NaN and reflexivity.** The general `Equal` contract requires
reflexivity (`a.eq(a)` is always true). `Float` is the one deliberate
exception: `NaN.eq(NaN)` is `false`, matching IEEE-754. Users writing
generic code over `T: Equal` must account for this when `T` can be
`Float`. `Float` as `Ordered` does *not* inherit this asymmetry — the
total-ordering defined in §2.6.5 places `NaN` above all finite values,
so `<`, `<=`, `>`, `>=` are well-defined on NaN. `-0.0 == 0.0` is
`true`; `-0.0 < 0.0` is `false` under `==` but `true` under the total
ordering exposed by `Ordered` (§2.6.5 footnote).

`Float` is not `Hashable` (§2.6.5).

**String equality is byte-wise.** `String.eq` compares the underlying
UTF-8 bytes. Osty does not apply Unicode normalization implicitly; use
`std.strings.normalize(form)` (NFC, NFD, NFKC, NFKD) when normalization
is required. `String` `Ordered` is lexicographic over those bytes.

Function types do not implement `Equal` or `Hashable`.

Reference identity: `std.ref.same(a, b) -> Bool`.

### 2.10 Mutability

All bindings are immutable by default. `mut` allows reassignment and
field mutation through that binding:

```osty
let x = 5                // immutable
let mut y = 5            // mutable
y = y + 1
```

A `let` binding to a reference type prevents reassignment and field
mutation through that binding.

### 2.11 Nullability

No `null`. Use `Option<T>` / `T?`:

```osty
fn find(id: Int) -> User? { ... }
```

---
