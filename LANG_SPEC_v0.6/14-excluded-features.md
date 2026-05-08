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

### 14.5 Why v0.6 stops where it does

The v0.6 surface adds 14 design decisions (G36–G49) catalogued in
[`00-revision.md`](./00-revision.md). Each is a *narrow* attestation
mechanism — capability parameter, taint tag, sealed construct,
error contract, intent annotation, spec block, golden test,
reproducibility scope, performance budget, API stability tier,
match-compat fallback, while keyword. None of them introduce a new
type kind, a new control-flow primitive, or a new evaluation rule.

This is intentional. The exclusions in §14.1 (no exceptions, no
inheritance, no lifetimes, no overloading, no implicit numeric
conversion, no macros, no user annotations) constrain the language
to a small grammar that compiles to a small IR. v0.6's additions
must compose with that grammar without breaking it. Every G36–G49
decision was vetted against three questions:

1. **Does it expand the type kind set?** If yes, the proposal is
   demoted to *attestation only* (e.g. `#[reproducible]` does not
   create a new function type — it is a checker-side promise).
2. **Does it introduce a new control-flow construct?** If yes, the
   proposal is rejected. `Cancelled` flows through `?`, capabilities
   ride on ordinary parameters, spec blocks are statement-shaped,
   `while` desugars to `for`.
3. **Does it require a new annotation argument shape?** If yes, the
   proposal is reshaped to fit the literal-only argument grammar
   (no expressions, no nested annotations).

Future proposals (G50+) tracked in `SPEC_GAPS.md` are evaluated
against the same three questions. Anonymous structural records, for
example, were proposed during the v0.6 batch and withdrawn because
they would have fork-extended the type-kind set without buying a
proportional safety / clarity gain.

### 14.6 v0.6-specific exclusions

The v0.6 baseline reaffirms certain v0.5 exclusions and clarifies
their interaction with the new annotation surface:

#### 14.6.1 No effect handlers

Unlike Koka, OCaml 5, or Eff, Osty does not provide effect
handlers. The v0.6 `#[ambient]` mechanism is *not* a handler — it
is a fixed name binding that injects from the prelude default
table. Authors who want "intercept all `Net` calls and route them
through a logger" use a wrapping capability:

```osty
pub struct LoggingNet {
    inner: Net,
    logger: Logger,
}

impl LoggingNet {
    pub fn fetch(self, url: String) -> Result<Bytes, Error> {
        self.logger.info("net.fetch: {url}")
        self.inner.fetch(url)
    }
    // ... satisfy the rest of Net interface ...
}
```

The wrapping struct satisfies `Net` structurally; downstream code
takes `Net` and is unaware. This is more verbose than effect
handlers but stays within the type system without introducing a
new control-flow construct.

#### 14.6.2 No row polymorphism in error contracts

`#[error_contract]` requires a *concrete enum* error type
(§7.5.1). Open / row-polymorphic error unions of the form `{
EmailError | DbError | ... }` are not provided. The closed-form
`EmailError | DbError` (the typed union) suffices for v0.6 use
cases.

Row polymorphism would let a function "add" errors to its contract
without changing existing match sites. Osty rejects this for
predictability — every error a function may emit must be
enumerable at compile time. Adding a contract variant is therefore
*always* a SemVer event.

#### 14.6.3 No type-level capability quantification

The closest thing to "this function uses the `Clock` effect" in
v0.6 is the type-level annotation `clock: Clock` in the
parameter list. There is no dedicated `effect Clock` syntax or
quantifier like `fn f<E: Clock>()`.

The capability is a value — passed, stored, returned — and the
absence of a type-level quantifier is what keeps the annotation
surface uniform with the rest of the type system. Quantifiers
would require new inference rules; capability values reuse the
existing structural-interface inference.

#### 14.6.4 No implicit prelude expansion

The prelude (§10.4) is a fixed set. Authors *cannot* extend it
per-package — there is no `pub use ... as prelude` mechanism. The
fixed prelude prevents version skew where a downstream consumer
sees different default symbols than the package author.

A package with a desired "common imports" surface declares them
explicitly:

```osty
// myapp/prelude.osty
pub use std.fs
pub use std.time
pub use std.log
```

Consumers `use myapp.prelude.*` to opt in. There is no implicit
auto-import.

---
