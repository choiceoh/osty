## 14. Excluded Features

The items below are excluded from v0.6. Items are grouped by reason so
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
  by the compiler (§1.9, §3.8). As of v0.6 this set is the v0.5
  baseline (`#[json(...)]`, `#[deprecated(...)]`, `#[op(...)]`,
  `#[cfg(...)]`, `#[test]`, `#[intrinsic]`, `#[pod]`, `#[repr(...)]`,
  `#[export(...)]`, `#[c_abi]`, `#[no_alloc]`) plus the v0.6 G36–G46
  additions (§20 capabilities, §21 information flow, §3.10–§3.15
  intent / spec / reproducible / sealed / evolution / golden / budget).
  See `00-revision.md §5` for the full surface table.
- Function overloading — distinct names for distinct operations.
- C-style `for` loops — use `for x in xs` / `for cond { }` /
  `while cond { }` / `loop { break v }`.
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
  narrow exception, reserved for `Error` downcast (§7.4).
- Turbofish on enum variant construction (`Option::<Int>::Some(5)` —
  use inference or annotate the receiver).
- `Set` literal syntax — construct via `Set.from(...)` or from an
  iterable.
- Annotations on expressions or `use` statements — annotations apply
  only to declarations.

**Operator surface.**

- Operator overloading **beyond** the six arithmetic operators
  documented in §3.8 / §14.2 — `==` / `!=` / `<` / `<=` / `>` / `>=` use the
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

### 14.2 Newly accepted syntax (v0.6)

The following surface forms were added in v0.6. Each is *additive* —
v0.5 programs continue to compile (with the v0.5/v0.6 transition
caveats in `BREAKING_v0.6.md` and `MIGRATING_v0.5_to_v0.6.md`).

- **`while` keyword** (G49) — `while cond { body }` is a synonym for
  `for cond { body }`; both lower to the same IR. v0.5 reused `for`
  for keyword economy; v0.6 acknowledges that `while` matches a more
  common mental model (§4.4).
- **Capability parameters** (G36) — `Clock`, `Rng`, `Env`, `Fs`,
  `Net`, `Process`, `Console` interfaces become canonical for
  effectful surface (§20). `#[ambient(name1, ...)]` injects defaults
  at entry-point functions only.
- **Information flow annotations** (G37) — `#[taint("source")]`,
  `#[sanitizes("source", into = "trust")]`, parameter-position
  `#[requires("trust")]` form a 1-bit + N-tag flow tracking surface
  (§21).
- **Spec / intent / determinism / construction / error contract
  annotations** (G38–G46) — see §3.10–§3.15, §7.5, §11.5 for
  individual forms. All optional; missing forms compile unchanged.

### 14.3 Carried-forward exclusions from v0.5

The v0.5 reversal of "implicit numeric conversions" (→ lossless widening
only, §2.2) and "operator overloading" (→ six-operator opt-in
`#[op(...)]`, §3.8) carries forward into v0.6 with no further
narrowing. Anonymous structural records remain excluded — proposed as
G50 during the v0.6 batch but withdrawn to preserve the
"named types are nominal" discipline (see `SPEC_GAPS.md` for archived
discussion).

### 14.4 Historical reversals (v0.4 → v0.5, carried into v0.6)

Two v0.4 exclusions were replaced with scoped forms in v0.5; both
remain in v0.6 unchanged:

- **Implicit numeric conversions** — replaced by **lossless widening
  only** (§2.2): `Int8 → Int16 → Int32 → Int → Float64`, `Int →
  Float64`, `Float32 → Float64`. Narrowing remains explicit via
  rounding-mode-suffixed converters (`.toIntTrunc()` /
  `.toIntRound()` / `.toIntFloor()` / `.toIntCeil()` / `.toInt32()` /
  `.toInt16()` / `.toInt8()` / `.toFloat32()`). Implicit narrowing is
  `E0765`.
- **Operator overloading** — replaced by **six-operator opt-in** via
  `#[op(+)]` / `#[op(-)]` / `#[op(*)]` / `#[op(/)]` / `#[op(%)]`
  (binary) and `#[op(-)]` (unary) on structural methods. All other
  operators remain primitive-only.

The v0.4 → v0.5 newly accepted syntax (`loop`, labeled `break` /
`continue`, `const fn`, `#[cfg(...)]`, `pub use`) is documented in
[`18-change-history.md §18.1`](./18-change-history.md). All forms
remain in v0.6 unchanged.

---
