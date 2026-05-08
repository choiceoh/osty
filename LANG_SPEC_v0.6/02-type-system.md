## 2. Type System

Osty v0.6 is a statically typed language with bidirectional type
inference, structural interfaces, monomorphized generics, and a
fixed-shape collection of value-semantic primitives plus
reference-semantic composites. The type system is designed to admit
compile-time guarantees without lifetimes or borrow analysis: the
v0.6 *capability surface* (§20) and *information flow tags* (§21)
ride on top of the standard type rules — capabilities appear as
ordinary parameter types, and flow tags decorate a type without
changing its identity.

This chapter covers primitives (§2.1), numeric conversions (§2.2),
overflow semantics (§2.3), composite types (§2.4), the optional sugar
`T?` (§2.5), structural interfaces and built-in protocols (§2.6),
monomorphized generics (§2.7), value vs reference semantics (§2.8),
equality and hashing (§2.9), mutability (§2.10), and nullability
(§2.11). The v0.6 annotation surfaces — capability parameters
(§20), information flow tags (§21), spec links and intent (§3.10 –
§3.12), reproducibility (§3.11), sealed construction (§3.4.5), error
contracts (§7.5), API evolution (§3.14), and budgets (§3.15) — all
*ride on top of* the type rules in this chapter; they decorate or
restrict declarations without introducing a new type kind. §2.12
catalogues those interactions in one place.

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

**Runtime-only types.** `RawPtr` is a pointer-shaped opaque type used by
the toolchain's runtime sublanguage. It is **not** part of the user
prelude and is unreachable from ordinary user code. See §19.3.

### 2.2 Numeric Conversions

Osty allows only lossless implicit numeric widening. The widening lattice
is `Int8 -> Int16 -> Int32 -> Int -> Float64`, `Int -> Float64`, and
`Float32 -> Float64`. Narrowing, signedness-changing conversions, and
lossy float/integer conversions require explicit methods; implicit
narrowing is `E0765`.

```osty
let a: Int = 5
let b: Float64 = a              // lossless widening
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
let c: Int = narrow.toInt()?   // explicit narrowing from variable

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
(Int,)                      // 1-element tuple type; comma required
fn(Int, Int) -> Int         // function type
fn()                        // Unit-returning function type shorthand

List<T>
Map<K, V>
Set<T>
T?                          // syntactic sugar for Option<T>
Option<T>                   // canonical form
Result<T, E>
```

`Set<T>` is a standard collection type, but there is no set literal;
construct one from an iterable, for example `Set.from([...])`.

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
    fn read(self, maxBytes: Int) -> Result<Bytes, Error>
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
collection types; they are structural by definition.

**Runtime-only marker.** `Pod` is a built-in marker interface
(no methods) used by the runtime sublanguage to constrain raw
load/store/CAS intrinsics. The compiler decides `Pod` membership
structurally; user code cannot `impl Pod`. `Pod` is not part of the
user prelude. See §19.4.

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

In Osty v0.6, `::` is exclusively the turbofish prefix, never a path
separator (path separator is `.`).

In **type position** (e.g. `let xs: List<List<Int>>`), the `<...>` form
is unambiguous and `::` is not required. The lexer emits `>>`, `>=`,
`>>=` as single tokens via maximal munch; the type parser splits them
back into `>` + `>` (or `>` + `=`) when a `>` is expected. The
"splittable `>`" rule lets nested generics like
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
fn find(db: Db, id: Int) -> User? { ... }
```

Capability parameters and `Option<T>` compose without special rules
— a missing user is not the same as a missing database, so a
function that *could* fail to find or *could* fail to access uses
both forms:

```osty
fn lookup(db: Db, id: Int) -> Result<User?, Error> {
    // Err(...)  — db itself failed (network, lock, etc.)
    // Ok(None) — db succeeded, no row matched
    // Ok(Some(u)) — found
    db.queryOne::<User>("SELECT * FROM users WHERE id = ?", [id])
}
```

---

### 2.12 v0.6 type system extensions

이 섹션은 v0.6 가 *기존 type system 위에 layer 한 메커니즘*을 한
곳에 정리. 정식 의미는 §20 (capabilities), §21 (information flow),
§3.10 (spec link), §3.11 (reproducibility), §3.4.5 (sealed
construct), §7.5 (error contract) 가 권위.

#### 2.12.1 Capability types — interface-level

v0.6 의 7 canonical capability (`Clock` / `Rng` / `Env` / `Fs` /
`Net` / `Process` / `Console`) 는 *일반 structural interface* 이다.
Type system 측 변경 0 — capability 는 새 type kind 가 아니다.

```osty
fn buildId(clock: Clock, rng: Rng) -> String { ... }
//          ^^^^^^^^^^^^^^^^^^^^^^^^
//          Clock, Rng 는 평범한 interface 타입.
```

검사기 (toolchain/check_gates.osty::runReproducibleCapabilityGate)
가 `#[reproducible]` 함수의 capability parameter 검사 — type system
위 *추가 enforcement layer* 만 추가됐다 (§20.4 / §3.11).

#### 2.12.2 Flow tags — type-orthogonal annotation

`#[taint("σ")]` / `#[sanitizes("σ", into = "τ")]` /
`#[requires("τ")]` 가 type 에 부착하는 *flow tag set* 은 type
identity 를 변경하지 않는다 — 같은 `String` 이라도 한 site 에선
`{user_input}` tag, 다른 site 에선 untagged 일 수 있다.

```osty
fn handler(form: #[taint("user_input")] String) { ... }
//                                       ^^^^^^
//                                       String 그대로. tag 만 부착.
```

Tag set 의 정확한 propagation rule 은 §21.5.3 의 inference rule.
요점: tag 는 *type-level* 이 아니라 *value-level* annotation —
ascription / equality / generic instantiation 등 type system 의 모든
규칙은 tag 와 무관하게 작동.

#### 2.12.3 Sealed construct — restriction on literal sites

`#[sealed_construct(parse)]` 는 struct 의 *literal 생성 위치* 만
제한한다 — type identity / interface satisfaction / generic
instantiation 등은 영향 없음.

```osty
#[sealed_construct(parse)]
pub struct Email { local: String, domain: String }
```

`Email` 은 평범한 struct — `Equal` / `Hashable` 자동 derive (§2.6.5),
`Map<Email, Int>` key 사용 가능, generic parameter 로 사용 가능. 다만
*construct* 만 sealed (§3.4.5 / E0420).

#### 2.12.4 Error contract — Result<T, E> 위 attestation

`#[error_contract]` 는 `Result<T, E>` 의 `E` 가 concrete enum 일 때만
적용 가능한 attestation. Type system 자체 변경 없음 — `Result<T, E>`
는 그대로 generic enum.

```osty
#[error_contract(EmailError.Format when "missing @")]
pub fn parseEmail(s: String) -> Result<Email, EmailError> { ... }
```

caller 의 match exhaustiveness 검사 가 contract variant 만 고려하도록
*추가 rule* 만 들어감 (§7.5.7). Erased `Error` interface 는
`#[error_contract(any)]` 만 (검증 없음, 문서용).

#### 2.12.5 Type erasure rules

- **Function value (G15)**: default / keyword metadata 는 erase. v0.6
  capability parameter 는 *positional 필수* 이므로 G15 와 충돌 없음
  — capability 는 함수값 시그니처 그대로 유지.
- **Interface value**: fat pointer (data + vtable). capability 도
  interface 이므로 같은 layout — `let f: Clock = systemClock` 형식의
  upcast 자유.
- **Flow tag erasure**: 함수값 으로 저장 시 tag 는 *유지* — `let f:
  fn(#[taint("user_input")] String) -> ...` 의 caller 측 검사가
  남는다.

#### 2.12.6 Generic monomorphization 와 v0.6 surface

generic 함수의 capability parameter 는 monomorphization 시 type
parameter 와 함께 결정.

```osty
fn id<T>(x: T) -> T { x }

#[ambient(clock)]
fn main() {
    let c = id(clock)         // T = Clock instance
    c.now()                    // OK — monomorphized id::<Clock>
}
```

flow tag 는 generic instance 별로 독립. `id<String@{user_input}>`
는 `id<String@{}>` 와 다른 monomorphization signature.

#### 2.12.7 Inference 와 v0.6 annotation

Bidirectional type inference (§2a) 는 v0.6 annotation 위에서 그대로
작동:

- `#[taint]`, `#[requires]` 는 type-level 검사 *후* 의 추가 layer —
  inference 자체에 영향 없음
- `#[reproducible]` 은 capability parameter 의 *수신 여부* 만 확인 —
  inference 가 결정한 parameter type 위 검사
- `#[sealed_construct]` 의 literal 차단은 *parse / resolve 단계* 검사
  — type checker 는 sealed type 의 일반 사용을 inference 통해 처리

#### 2.12.8 Variance, lifetime, where clause — 변경 없음

§14 의 exclusion (variance, lifetime, where, generic 기본값) 는
v0.6 에서 모두 carry-forward. v0.6 의 capability / flow tag /
sealed / error contract 는 *모두* 이 exclusion 을 지키는 형태로
설계.

| Excluded feature | v0.6 영향 |
|---|---|
| Variance annotation | capability `Clock` 등은 invariant interface — sub/super 관계 없음 |
| Lifetime annotation | flow tag 가 lifetime *대체* — `'a` 같은 ident 없음 |
| `where` clause | `T: I1 + I2` 그대로 — capability 도 동일 |
| Generic 기본값 | variation 없음 |

### 2.13 Equality / Hashability / Ordering — annotation interactions

The `Equal` / `Hashable` / `Ordered` rules in §2.6.5 / §2.9 stand on
their own; the v0.6 annotation surface does not perturb them.
Specifically, the answer to *"does annotation X affect equality
behavior?"* is uniformly **no** for all v0.6 surfaces:

| Annotation | Equal/Hashable 영향 |
|---|---|
| `#[sealed_construct]` | 영향 없음 — sealed 는 construct 만 제한 |
| `#[taint]` | 영향 없음 — flow tag 는 value-level |
| `#[reproducible]` | 영향 없음 — function-level annotation |
| `#[purpose]` / `#[example]` / `#[fixture]` | 영향 없음 — metadata |
| `#[stability]` | 영향 없음 — metadata |
| `#[budget]` | 영향 없음 — metadata + perf check |
