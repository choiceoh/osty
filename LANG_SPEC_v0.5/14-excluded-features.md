## 14. Excluded Features

The items below are excluded from v0.5. Items are grouped by reason so
readers can see the argument without consulting the change history.
Re-opening any of them requires a design proposal, a new minor or major
version, and a migration story for existing code.

### 14.1 Current Exclusions

**Safety / correctness by construction.**

- `null` / `nil` — Osty uses `Option<T>` and forces explicit handling.
- Exceptions, `try` / `catch`, `panic` / `recover` as user control flow
  — errors are values (`Result<T, E>`, `?`, `defer`). `panic` /
  `unreachable` / `todo` / `abort` are programmer-error aborts, not a
  recoverable control flow.
- Silent arithmetic overflow — use explicit `wrapping*` /
  `checked*` / `saturating*` methods on `Int` / `IntN`.
- Finalizers and weak references — GC trace graph is object-local;
  introducing finalizers would add unobservable ordering.
- `unsafe` block (user-facing) — the runtime sublanguage in §19 is
  implementation-private and rejected outside privileged packages
  with `E0770`. User code has no `unsafe` escape hatch.
- User-visible raw pointer — the `RawPtr` type in §19 is gated the
  same way; there is no user-facing address-of or dereference.

**Type system simplicity.**

- Inheritance — compose via structs and satisfy structural interfaces.
- Classes — `struct` + methods + interfaces cover the same ground.
- Lifetime annotations — GC eliminates the need; structured-concurrency
  `Handle<T>` / `TaskGroup` are non-escaping by a finite front-end
  rule (G13), not a lifetime system.
- Declaration-site or use-site variance (`in` / `out`, Java wildcards)
  — monomorphization makes variance a non-issue for concrete types.
- Generic type-parameter default values (e.g. `<T, U = T>`) — use a
  new type alias instead.
- `where` clauses — write `T: I1 + I2` directly in the type parameter
  list.
- Anonymous structs / structural records — a named `struct` is
  required. Tuples cover the remaining use cases.
- `UInt` (machine-word unsigned integer type) — `Int` is fixed at 64
  bits; sizes / counts / indices are `Int`.

**Grammar discipline.**

- Macros (declarative or procedural) — the compiler does not take
  user-defined syntax extensions.
- User-defined annotations / attributes — the annotation set is fixed
  by the compiler (§1.9, §3.8). As of v0.5 this set is
  `#[json(...)]`, `#[deprecated(...)]`, `#[op(...)]`, `#[cfg(...)]`,
  `#[test]`, `#[intrinsic]`, `#[pod]`, `#[repr(...)]`,
  `#[export(...)]`, `#[c_abi]`, `#[no_alloc]`.
- Function overloading — distinct names for distinct operations.
- C-style `for` loops — use `for x in xs` / `for cond { }` /
  `loop { break v }`.
- `impl` blocks — methods live inside `struct` / `enum` bodies.
- `spawn` keyword — use `g.spawn(|| ...)` from `taskGroup`.
- Detached concurrency — every task is owned by a `taskGroup`.
- `async` / `await` — structured concurrency via `taskGroup` replaces
  per-call-site async.
- `yield` / generators — compose iterators via §15 and lazy
  combinators in `std.iter`.
- `as` keyword for general type conversion — specific converter
  methods (`.toInt32()`, `.toFloat()`, `.toString()`, etc.) make the
  rounding / truncation / failure contract explicit. `as?` is a
  narrow exception, reserved for `Error` downcast (§4.9).
- Turbofish on enum variant construction (`Option::<Int>::Some(5)` —
  use inference or annotate the receiver).
- `Set` literal syntax — construct via `Set.of(...)` or from an
  iterable.
- Annotations on expressions or `use` statements — annotations apply
  only to declarations.

**Operator surface.**

- Operator overloading **beyond** the six arithmetic operators
  documented in §3.1 — `==` / `!=` / `<` / `<=` / `>` / `>=` use the
  `Equal` / `Ordered` interfaces; `[]` (indexing), `()` (call), `<<` /
  `>>` / `&` / `|` / `^` (bitwise) are primitive-only and cannot be
  overloaded. `#[op(+)]` / `#[op(-)]` / `#[op(*)]` / `#[op(/)]` /
  `#[op(%)]` (binary) and `#[op(-)]` (unary) are the complete allowed
  set; an attempt to overload any other operator is `E0725`.
- Named arguments **for required parameters** — required parameters
  are positional only. Defaulted parameters may use keyword-call form
  (§3.1).

**Testing / tooling hooks.**

- `beforeEach` / `afterEach` test hooks — use helpers or
  `testing.context`.
- `WaitGroup` — `taskGroup` covers all structured-concurrency waiting.
- Garbage collector tuning knobs — the runtime picks policy; tuning
  is not a user surface.
- `const` as a **run-time** immutable binding keyword — the `let`
  binding (which is immutable by default) already covers that
  meaning. `const fn` (§3.1.1) names a compile-time evaluable function,
  not a binding form; its body is constrained by the capability matrix
  in §3.1.1.

### 14.2 Accepted in v0.5 (previously excluded)

Two v0.4 exclusions were replaced with scoped forms in v0.5 because
the total ban blocked numerical code readability in realistic
programs:

- **Implicit numeric conversions** — replaced by **lossless widening
  only** (§2.2a): `Int8 → Int16 → Int32 → Int → Float64`, `Int →
  Float64`, `Float32 → Float64`. Narrowing remains explicit via
  rounding-mode-suffixed converters (`.toIntTrunc()` /
  `.toIntRound()` / `.toIntFloor()` / `.toIntCeil()` / `.toInt32()` /
  `.toInt16()` / `.toInt8()` / `.toFloat32()`). Implicit narrowing is
  `E0765`.
- **Operator overloading** — replaced by **six-operator opt-in** via
  `#[op(+)]` / `#[op(-)]` / `#[op(*)]` / `#[op(/)]` / `#[op(%)]`
  (binary) and `#[op(-)]` (unary) on structural methods (§3.1). All
  other operators remain primitive-only (see §14.1 above).

### 14.3 Newly accepted syntax (v0.5)

These forms were listed as excluded in v0.4 and were accepted in v0.5
as scoped additions:

- **`loop` keyword** — excluded in v0.4 as "subsumed by `for`". v0.5
  distinguishes `for cond { }` (Unit-returning while-style, unchanged
  since v0.3) from `loop { break value }` (value-returning unbounded
  loop, §4.4). The distinction is intentional.
- **Labeled `break` / `continue`** — `'label: for/loop ... break
  'label` (§4.4). Nested loops no longer require sentinel flags.
- **`const`** — `const fn` only. Compile-time evaluable function
  (§3.1.1), not a run-time binding form. `let` remains the sole
  binding form. The evaluable body is defined by the capability
  matrix in §3.1.1; constructs outside the matrix are `E0766`.
- **`#[cfg(...)]` annotation and `pub use` re-exports** — §5. Neither
  keyword is new; the surface is annotation-based and `use`-based
  respectively.

---
