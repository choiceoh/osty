## 18. Change History

This chapter records the evolution of the specification across released
versions. The latest release is at the top.

### 18.1 v0.3 → v0.4

v0.4 closes the v0.3 edge-case decision queue without adding a large new
surface area. The goal is a tighter baseline: finite front-end rules,
stable diagnostics, and implementation status that matches the spec.
**No known open gaps remain** at the time of this release.

| Gap | Resolution | Location |
|---|---|---|
| **G13** Structured-concurrency escape rules | `Handle<T>` and `TaskGroup` are non-escaping capabilities. Returning them, storing them in fields/collections for use outside the group, sending them over channels, or capturing them in escaping closures is `E0743`. | §8 |
| **G14** Generic method turbofish and method references | `obj.method::<T>(args)` names method-local generics only; owner generics come from the receiver. Partial explicit type args are illegal. Generic methods cannot be extracted as `obj.method` function values; use a wrapper closure. | §4.9 |
| **G15** Callable arity after erasure | Function values of type `fn(...) -> ...` do not preserve declaration default/keyword metadata. Calls through a function value are positional-only and exact arity (`E0701`). | §3.1, §4.9 |
| **G16** Closure parameter patterns | Closure params accept `LetPattern (':' Type)?` but only irrefutable patterns. Refutable literal/range/variant/or patterns are rejected with `E0741`. | §4.7 |
| **G17** Nested pattern witness diagnostics | Exhaustiveness diagnostics report one minimal missing pattern. Tuple/struct witnesses refine the leftmost missing component; closed enum/Option/Result payloads recurse; open types use `_`; guarded arms do not contribute coverage. | §4.3 |
| **G18** Stdlib protocol executable stubs | Protocol signatures in §10/§15/§16/§17 are tracked as checked `.osty` stubs first. Runtime/gen parity is implementation backlog unless a signature ambiguity is found. | §10, §15-§17 |

Additional grammar-freeze hardening:

- Empty turbofish (`foo::<>`) is a syntax error.
- Empty generic/type argument lists (`fn f<>()`, `List<>`) are syntax
  errors; every generic parameter or type-argument list is non-empty.
- Generic top-level functions and generic methods are not first-class
  function values; callers must instantiate them through a call or a
  wrapper closure.
- Erased function values reject keyword arguments even when the original
  declaration had parameter names.
- `(T,)` is a one-element tuple type, not a parenthesized `T`; `fn()`
  is the Unit-returning function type shorthand.

### 18.2 v0.2 → v0.3

v0.3 closes every remaining open gap from v0.2 and nails down the
ambiguities surfaced by a full audit of the v0.2 text. **No known open
gaps remain** at the time of this release.

#### Gap resolutions

| Gap | Resolution | Location |
|---|---|---|
| **G4** Closure parameter patterns | `ClosureParam ::= LetPattern (':' Type)?` — tuple/struct destructure allowed. Only irrefutable patterns. v0.3 specified the surface ahead of parser parity; v0.4 implements parser/checker support. | §4.7 |
| **G8** Channel `close()` semantics | Any task may close. Second close aborts. `recv` returns buffered values then `None`. `for x in ch` terminates naturally. | §8.5 |
| **G9** `Builder<T>` phantom type | Fully abstracted — `Builder<T>` surface only; internal phantom parameter names missing fields in the compile error. Users cannot construct or destructure. | §3.4 |
| **G10** `Char` / surrogate | Surrogate code points are not representable. Literal/escape errors at compile time. `Int.toChar()` aborts; safe `Char.fromInt(n) -> Char?`. | §2.1, §10.5 |
| **G11** Generic compilation model | **Monomorphization** (Rust-style). Interface values use fat-pointer + vtable. Error nominal tag (§7.4) remains orthogonal. | §2.7.3 |
| **G12** Cancellation propagation | **Task-group auto-propagation** (Kotlin scope-style). Cancel signal carries `Cancelled { cause: Error }`. All stdlib blocking calls are cancel-aware and return `Err(Cancelled)` immediately. Defer bodies run on cancel (uninterruptible). | §8.4 |

#### Additional decisions (from the v0.2 audit)

**Concurrency**
- `Handle<T>` may not escape its `taskGroup` block (compile error).
- `abort(msg)` terminates the process; it is not a recoverable error.
- `race` tie-breaking is scheduler-order-deterministic within a run.
- Channel buffer=0 is a synchronous rendezvous.
- `select` with `default`: `default` fires **only** when no other branch is ready.
- `select` branches are evaluated sequentially in registration order.
- `WaitGroup` is **removed** from the stdlib listing (§10.2) — `taskGroup` covers every use case.

**Numerics**
- Integer division / modulo by zero: **abort**.
- `%` follows the dividend sign (C/Go convention): `-5 % 3 == -2`.
- `Int.MIN.abs()`: abort. `checkedAbs`/`wrappingAbs` added.
- Shift by ≥ bit-width: abort. Shift by 0: identity.
- `Int.pow(exp)` with `exp < 0`: abort (use `Float.pow`).
- `wrappingDiv`/`wrappingMod`/`checkedDiv`/`checkedMod`/`saturatingDiv` added.
- **`Float → Int` conversion** is through explicit rounding-mode methods: `toIntTrunc`, `toIntRound` (banker's), `toIntFloor`, `toIntCeil`, each returning `Result<Int, Error>` (NaN/±Inf → `Err`). The ambiguous `Float.toInt()` is removed.
- `Float.round()` uses banker's rounding (half-to-even).
- Float `NaN`: `NaN.eq(NaN) == false` — documented exception to `Equal` reflexivity. `Float` is **not** `Hashable`.
- `-0.0 == 0.0` is true; `-0.0 < 0.0` false under `==`, true under the total ordering exposed by `Ordered`.
- Float literal exponent overflow (`1e1000`) parses to ±Infinity (IEEE).

**Strings**
- `String == String` is byte-wise. Normalization is explicit via `std.strings.normalize(form)`.
- `String` ordering is byte-lexicographic.
- String concatenation: `concat` method only — no `+` operator.
- `String` read from a file with invalid UTF-8: the bytes are retained as `Bytes`; `Bytes.toString()` returns `Err` at the conversion boundary.

**Generics**
- Invariant on all type parameters. No declaration-site or use-site variance.
- Recursive generic types (`struct Node<T> { next: Node<T>? }`) are allowed.
- Generic methods on structs/enums are allowed independently of the enclosing type's generics.
- No type-parameter default values (`<T, U = T>`).
- Turbofish is not used on enum variant construction.

**Interfaces**
- Default methods may call other default methods.
- Interface methods may themselves be generic.
- Partial implementation (missing required methods) is a compile error.
- Interface composition with name collisions is a compile error; explicit override is required.
- `Self` is permitted in parameter position (already used by `Equal`).

**Errors**
- `abort(msg)` inside `taskGroup` terminates the process; it does not deliver `Err` to siblings.
- A user type may be both `Error` and `Hashable` without restriction.
- `?` auto-widens concrete errors to `Error` only when the enclosing function returns `Result<_, Error>`. Otherwise explicit conversion is required.
- Recommended pattern for multiple error types: local enum wrapper implementing `Error`.

**Memory / GC**
- GC algorithm and triggers are **out of scope** for the spec (implementation-defined).
- No finalizers.
- No weak references.
- OOM aborts the process.
- Cycle collection is guaranteed.

**Patterns**
- Or-pattern alternatives must agree on binding names and types; different nesting depths allowed.
- Guards may reference pattern bindings; guards may not introduce new bindings.
- Range patterns require `Ordered` scrutinee; `Char` ranges (`'a'..='z'`) permitted, `String` ranges rejected.
- Literal patterns are type-strict (no numeric coercion).

**Modules**
- Partial declarations must agree on `pub`-ness.
- Type alias's visibility is of the alias declaration, not the aliased type.
- Diamond imports (A→B→D, A→C→D) are allowed.
- Version conflicts are resolved by `osty.lock`; spec defers to package manager.

**Defer**
- Runs on normal exit, `?`-propagated early return, and cancellation.
- Does **not** run on `abort`/`unreachable`/`todo`/`os.exit`.
- Blocking calls inside `defer` ignore cancellation (cleanup is uninterruptible).
- Bare `defer` at top level of a script is a compile error.

**FFI**
- Go `panic` crossing the bridge **aborts** the Osty process.
- Generic Osty functions cannot be declared in a `use go` block.
- Osty closures cannot cross the FFI boundary.
- Go `interface{}` is not representable in FFI declarations.
- Go `error → Osty Error` is best-effort — `message()` is preserved; `downcast::<T>()` is not supported for Go-origin errors.

**Testing**
- Test execution order is randomized with a printed reproducible seed; `--seed <hex>` replays a run.
- No `beforeEach`/`afterEach` hooks — use helpers or `testing.context`.

**JSON**
- Unknown object keys during decode are **silently ignored** (forward-compat default).
- Type mismatches fail fast with `Err`; no partial recovery.
- Duplicate effective variant tags are a compile error.
- Missing key vs `null` for `T?` both decode to `None`.

**Annotations**
- Cannot appear on `use` statements, expressions (including closures), or individual in-body statements.
- Same annotation name cannot be repeated on a single target.
- Partial-decl annotations are scoped to the declaration they appear in; the compiler does not merge across declarations.
- `#[deprecated]` does not propagate transitively.

**Miscellaneous**
- Zero-sized structs (`struct Marker {}`) are allowed.
- Recursive struct `auto-derive` (`Equal`, `Hashable`, `ToString`) is supported inductively.
- `??` precedence is grammar-authoritative (§1.7 cross-references the grammar).
- Closure capture of `mut` bindings is by mutable reference (existing §4.7).

### 18.3 v0.1 → v0.2

v0.2 reconciled every conflict between `LANG_SPEC_v0.1.md` and
`OSTY_GRAMMAR_v0.2.md` and resolved gaps G1, G2, G3, G5, G6, G7 from
`SPEC_GAPS.md`. The spec was also split into the per-section folder
that v0.3 inherits.

#### Conflict resolutions (LANG_SPEC v0.1 ↔ GRAMMAR v0.2)

| # | Topic | v0.1 | v0.2 |
|---|---|---|---|
| C1 | Annotation argument form | `name = value` only | `name = value` **or** bare `name` flag (§1.9) |
| C2 | `#[json]` arguments | `key` only | `key`, `skip`, `optional` (§3.8.1) |
| C3 | `#[json]` targets | struct fields + enum variants | struct fields + enum variants (unchanged; GRAMMAR text saying "struct fields only" was incorrect) |
| C4 | `#[deprecated]` arg name | `note` | `message` (§3.8.2) |
| C5 | `#[deprecated]` targets | fn/struct/enum/interface/type/let/fields/variants | fn/struct/enum/interface/type/let/methods/fields/variants |
| C6 | `=>` token | implied absent | explicitly **not a token**; match arms use `->` (§1.7, O7) |

#### Lexical / syntactic decisions imported from GRAMMAR

| Decision | Section |
|---|---|
| O2: `}` and `else` must be on one line | §1.8 |
| O3: leading `.` for chain continuation; trailing `.` is a syntax error | §1.8 |
| O4: splittable `>` for nested generics (`List<List<T>>`) | §2.7.2 |
| O5: `_` is a distinct `UNDERSCORE` token; `_foo` is an `IDENT` | §1.4 |
| O6: `::` must be followed by `<` (turbofish strict) | §2.7.2 |
| O7: `=>` is removed from the token set | §1.7 |
| R3: formal "RestrictedExpr" rule | §4.1.1 |
| R10: shebang at byte 0 only, once | §1.1 |
| Full ASI suppression rules from R2 | §1.8 |

#### Gap resolutions

| Gap | Resolution | Location |
|---|---|---|
| **G1** Collection `Equal`/`Hashable` derivation | Built-in instances table + collection derivation rules | §2.6.5 |
| **G2** `Duration.toString`, `Duration` as log value | `Duration.toString()` added; `LogValue::Duration` documented | §10.20, §10.10 |
| **G3** `LogValue` concrete enum + auto-promotion | Defined as a sum type with auto `ToLogValue` promotion | §10.10 |
| **G5** Script `return` semantics | Documented as return from implicit `main` | §6 |
| **G6** Annotations on partial declarations | Scoped per-declaration; no merging | §3.4 |
| **G7** Diagnostic template for positional-after-keyword | Fixed-form error box | §3.1 |

#### New sections (introduced in v0.2)

| § | Title | Purpose |
|---|---|---|
| §15 | Iteration Protocol | `Iterator<T>`, `Iterable<T>`, `for`-loop desugaring. |
| §16 | I/O Protocol | Method signatures for `Reader`, `Writer`, `Closer`, `ReadWriter`. |
| §17 | Display and Format Protocol | `ToString` interface, auto-derivation rules, string interpolation semantics. |

#### Additional v0.2 decisions

- `Bytes` literal `b"..."` (§2.4.1).
- `String` indexing is byte-based (§4.10).
- struct/enum `ToString` auto-derivation (§17).
- Bit shift overflow policy and `wrapping*`/`checked*` shift methods (§2.3, §10.5).
- `Error.downcast::<T>()` documented as the one nominal-typing exception (§7.4).
- Built-in-instances table (§2.6.5) enumerates primitive `Equal`/`Ordered`/`Hashable`/`ToString` membership.

### 18.4 Migration notes

**v0.2 → v0.3 breaking changes to watch for:**

- `'\u{D800}'` and similar surrogate literals now fail to compile (§2.1).
- `Float.toInt()` is removed; use `toIntTrunc`/`toIntRound`/`toIntFloor`/`toIntCeil`, all returning `Result<Int, Error>` (§10.5).
- `WaitGroup` references in user code must be migrated to `taskGroup` (§8.1).
- `time.sleep(d)` now returns `Result<(), Error>` instead of `()` (§10.20).
- Top-level `defer` in scripts is now an error — wrap in `{ ... }` (§6).

**v0.1 → v0.3 migration** follows both §18.1 and §18.2. Users moving
directly should read §18.2 first, then §18.1.
