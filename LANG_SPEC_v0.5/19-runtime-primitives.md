## 19. Runtime Primitives

This chapter specifies the **runtime sublanguage** — a small, package-gated
surface that lets the Osty toolchain implement the GC, allocator, and other
runtime services in Osty itself. None of this surface is reachable from
ordinary user code; the entire chapter is invisible to programs that do not
opt into the runtime gate.

The user-facing language guarantees of §9 (Memory Management) are
unchanged. There is no `unsafe` block, no raw pointer in the user prelude,
no manual deallocation API on `T`. §14 still excludes `unsafe`; that
exclusion is about *user* code. This chapter exists so the GC promise of
§9 has a concrete implementation strategy without requiring that strategy
to be written in C.

### 19.1 Scope and Non-Goals

**In scope.**

- A single opaque integer-shaped pointer type, `RawPtr`.
- A compile-time marker trait, `Pod`, that proves a type contains no
  managed references.
- Six new annotations: `#[intrinsic]`, `#[pod]`, `#[repr(...)]`,
  `#[export(...)]`, `#[c_abi]`, `#[no_alloc]`.
- A package-level gate (§19.2) that restricts the surface to the runtime
  packages.
- Lowering rules (§19.7) that connect intrinsics to LLVM IR and to the
  C ABI used by the runtime symbol set (`osty.gc.*`).
- The compiler-to-runtime safepoint contract (§19.10) that lets an
  Osty-side `safepoint_v1` see the same root array the C runtime sees
  today.

**Out of scope.**

- A general `unsafe` block. There isn't one.
- Address-of (`&x`) for managed references. Managed references remain
  GC handles in the language model; their representation is not
  observable.
- Stackmap *introspection* intrinsics. The runtime never reads stackmap
  metadata directly; instead, the compiler-emitted call site for
  `safepoint_v1` (§19.10) materializes the root array and passes it in.
  The runtime walks an array, not the stack.
- Volatile, atomic-fence, or inline-assembly primitives. The first GC
  delivered through this surface is single-threaded stop-the-world.
  The `cas` intrinsic (§19.5) is provided for forward compatibility
  with later concurrent algorithms; the v0.4 single-threaded GC is not
  required to exercise it.

**GC × scheduler interaction.** §8 specifies an M:N scheduler; §19
specifies a single-threaded STW collector. These compose as follows:

- A collection cycle is initiated from a worker that reaches a
  safepoint (§19.10) and finds the `stop_requested` flag set.
- Every other worker must reach a safepoint before the collector
  proceeds. Tasks parked at a language-level yield point (§8.0:
  channel send/recv, `Handle.join`, `thread.yield`, `thread.sleep`,
  `thread.select` blocking arms, cancellation checks) are treated as
  having **passed** a safepoint; the runtime registers their live
  root set when parking so the collector walks it in place.
- FFI calls declared through §19 or `use go` are **not** implicit
  safepoints; a worker stuck in a long blocking FFI call delays
  collection for the whole process. A future revision may add a
  `#[gc_safe]` annotation for FFI hand-off; v0.4 does not.
- When all workers are parked at safepoints, the collector runs
  single-threaded over the union of (a) the root array passed by the
  initiating safepoint and (b) the per-task parked-root arrays. It
  then releases the workers.

The v0.4 collector **does not** walk fiber-saved register state or
unregistered stack memory. Programs that allocate a value and hold it
as a local across a yield point are safe only if the compiler-emitted
safepoint at the yield point lists that local in the root array. The
MIR lowering for `IntrinsicSpawn`, `IntrinsicHandleJoin`,
`IntrinsicChanSend`, and `IntrinsicChanRecv` is required to emit a
safepoint with the caller's live roots before dispatching to the
corresponding `osty_rt_*` symbol. Runtime phases that do not yet
satisfy this contract are called out in `RUNTIME_SCHEDULER.md` with
their narrowing restriction (e.g., Phase 1A runs all tasks
sequentially on a single worker, so the parked-root concern is vacuous
but the contract is documented for Phase 1B and later).

### 19.2 Privileged Packages

The runtime surface is **gated by package path**. A package is privileged
if and only if either:

1. Its fully qualified path begins with the prefix `std.runtime.`
   (the public namespace for runtime intrinsics), or
2. Its `osty.toml` manifest contains `[capabilities] runtime = true`
   **and** the manifest is loaded from a path under the toolchain
   workspace root. The compiler refuses this capability bit when the
   manifest is loaded from a registry-fetched dependency, regardless of
   the bit's value in the published manifest.

The `[capabilities]` table is part of the manifest schema; the v0.4
runtime spec is the first chapter to require it. Manifest loaders that
do not yet recognize the table treat any unknown key as a parse error
(per §10/§11/§13 manifest conventions), so older toolchains refuse to
compile a package that depends on this surface — which is the desired
fail-closed behavior.

A non-privileged package that:

- declares a function with `#[intrinsic]`, `#[no_alloc]`, `#[c_abi]`, or
  `#[export(...)]`, or
- declares a type with `#[pod]` or `#[repr(c)]`, or
- imports `std.runtime.*` (any subpath), or
- names `RawPtr` or the `Pod` trait,

is rejected at resolution time with `E0770 runtime primitive used outside
privileged package`. The diagnostic preferentially points at the import
when one is present, otherwise at the offending annotation or identifier.

### 19.3 The `RawPtr` Type

```osty
opaque type RawPtr        // declared in std.runtime
```

`RawPtr` is an opaque integer-shaped type with the following contract:

- It occupies the target's pointer width. v0.4 supports 64-bit targets
  only, so `RawPtr` is 8 bytes.
- It is `Pod` (it contains no managed reference).
- It implements `Equal` and `Hashable`. It does **not** implement
  `Ordered`; the runtime uses tag bits, not numeric comparison, when it
  needs ordering on raw addresses.
- Pattern matching on `RawPtr` supports identifier and equality patterns
  (via `Equal`); range patterns are rejected with `E0723` (range
  pattern requires `Ordered`).
- The garbage collector does **not** trace `RawPtr` values. A `RawPtr`
  may dangle, alias, or refer to uninitialized memory. Its semantics
  are the C `uintptr_t` semantics, not a managed handle.

The literal sugar `T?` is *not* available on `RawPtr` directly — code
that wants a "maybe-null" representation in the runtime ABI uses
`RawPtr` and tests against `raw.null()`, because the allocator-failure
path returns its result in a single register matching the C ABI and an
`Option` tag would change the calling convention. `Option<RawPtr>`
*is* permitted in ordinary `#[no_alloc]` code (§19.4 allows
`Option<T: Pod>` as `Pod`); it just cannot appear as the return type of
a `#[c_abi]` function.

`RawPtr` values are constructed only by the §19.5 intrinsics
(`raw.alloc`, `raw.offset`, `raw.read::<RawPtr>`, `raw.null`,
`raw.fromBits`). They are observed as integers via `raw.bits`.

### 19.4 The `Pod` Marker Trait

```osty
interface Pod {}          // declared in std.runtime; compiler-derived
```

`Pod` is a compile-time marker that means: "values of this type contain
no managed reference, no closure capture, and no padding bits whose
value the compiler may choose freely". `Pod` membership is decided by
the checker, not by user impl. There is no `impl Pod for ...` syntax.

A type satisfies `Pod` exactly when one of the following derivation
rules applies:

1. **Primitives.** `Bool`, `Char`, `Byte`, `Int`, `Int8`–`Int64`,
   `UInt8`–`UInt64`, `Float` (alias for `Float64`), `Float32`,
   `Float64`, and `RawPtr` are `Pod`.
2. **Annotated structs.** A `struct` declaration is `Pod` if it carries
   both `#[pod]` and `#[repr(c)]` and every field type is `Pod`. The
   checker verifies the field shape; an offending field is reported as
   `E0771 type cannot be marked #[pod]`, naming the first non-`Pod`
   field.
3. **Generic annotated structs.** A generic `#[pod] #[repr(c)] struct
   S<T1, …, Tn>` requires every type parameter to carry the bound
   `Tk: Pod` at the declaration site. The resulting `Pod` instance is
   unconditional. Per-instantiation `Pod` (membership computed
   per-use) is **not** supported in v0.4; an unbound generic struct
   marked `#[pod]` is `E0771`.
4. **Tuples.** A tuple type `(T1, …, Tn)` is `Pod` if every component
   is `Pod`. Because struct values have reference semantics in the
   user-facing language (§2.8), a tuple component of type `S` is `Pod`
   *only* when `S` is a `#[pod] #[repr(c)]` struct, in which case the
   tuple stores `S` by value (the runtime sublanguage is the only
   place this by-value treatment is observable; in user-facing code the
   reference semantics of §2.8 still hold because user code never sees
   `Pod` types).
5. **`Option<T>` of `Pod`.** `Option<T>` is `Pod` when `T: Pod`. The
   lowering uses a tagged representation whose payload is exactly
   `sizeOf::<T>()` plus an aligned tag byte; no managed allocation
   participates.
6. **Nothing else.** `String`, `Bytes`, `List<T>`, `Map<K, V>`,
   `Set<T>`, closure values, function values, interface values,
   `Result<T, E>`, ordinary user-defined enums, and any type that
   transitively contains one of these are *not* `Pod`. `Result` is
   excluded because its error arm carries `Error`, an interface.
   Runtime code that wants result-style propagation returns `RawPtr`
   sentinels or aborts.

The trait has no methods. It exists only as a constraint:

```osty
fn raw.read<T: Pod>(p: RawPtr) -> T
fn raw.write<T: Pod>(p: RawPtr, v: T)
fn raw.cas<T: Pod>(p: RawPtr, old: T, new: T) -> Bool
```

Calling `raw.read::<List<Int>>(p)` is `E0727` (constraint not
satisfied), not a runtime crash.

**Footgun note.** `RawPtr` is `Pod` because the GC tracer does not
follow it. A `#[pod] #[repr(c)] struct Header { next: RawPtr, … }`
therefore satisfies `Pod` even though it stores an address into managed
memory. The privileged-package author is responsible for ensuring such
pointers do not become the only handle to a live object — the tracer
will not preserve them. This is exactly the pattern the current C
runtime uses for its `osty_gc_header.next` linked list, and the spec
permits it intentionally.

### 19.5 Intrinsic Functions

The runtime surface declares the following intrinsics inside the
package `std.runtime.raw`. Each declaration is body-less and carries
`#[intrinsic]`. Generic intrinsics participate in monomorphization
exactly like any other generic; the lowering layer (§19.7) supplies the
specialized body at each instantiation site, so the intrinsic is never
present as a real symbol in the final object file.

```osty
package std.runtime.raw

#[intrinsic] pub fn null() -> RawPtr
#[intrinsic] pub fn fromBits(n: Int) -> RawPtr
#[intrinsic] pub fn bits(p: RawPtr) -> Int
#[intrinsic] pub fn alloc(bytes: Int, align: Int) -> RawPtr
#[intrinsic] pub fn free(p: RawPtr)
#[intrinsic] pub fn zero(p: RawPtr, bytes: Int)
#[intrinsic] pub fn copy(dst: RawPtr, src: RawPtr, bytes: Int)
#[intrinsic] pub fn offset(p: RawPtr, bytes: Int) -> RawPtr
#[intrinsic] pub fn read<T: Pod>(p: RawPtr) -> T
#[intrinsic] pub fn write<T: Pod>(p: RawPtr, v: T)
#[intrinsic] pub fn cas<T: Pod>(p: RawPtr, old: T, new: T) -> Bool
#[intrinsic] pub fn sizeOf<T: Pod>() -> Int
#[intrinsic] pub fn alignOf<T: Pod>() -> Int
```

Privileged callers reach them through their fully-qualified path:

```osty
use std.runtime.raw

let header = raw.alloc(64, 8)
if raw.bits(header) == 0 { abort("oom") }
raw.write::<Int>(raw.offset(header, 8), 0)
```

**Behavior contract.**

- `null()` returns a `RawPtr` whose integer value is 0.
- `fromBits(n)` reinterprets a signed 64-bit integer as a pointer.
  `bits(p)` is the inverse. Both are total; arithmetic on `Int`
  follows §2.3.
- `alloc(bytes, align)` requests a fresh, naturally-aligned block of
  `bytes` bytes from the host allocator. The block is **not** zeroed;
  callers that need zero-initialized memory follow `alloc` with
  `zero` (or use the `alloc_zeroed` helper that the privileged `std.runtime`
  package layers on top — see §19.7 lowering for the rationale). On
  failure `alloc` returns the value of `null()`. `align` must be a
  power of two and at least 1; `bytes` must be non-negative; both
  preconditions abort on violation. The caller is responsible for
  matching every successful `alloc` with exactly one `free`.
- `free(p)` releases a block previously returned by `alloc`. `free`
  of `null()` is a no-op. `free` of a non-`null`, non-`alloc`-returned
  pointer aborts.
- `zero` and `copy` perform `memset` and `memmove` semantics
  respectively. `bytes` must be non-negative; negative values abort.
  Overlap is permitted in `copy` (it is `memmove`, not `memcpy`).
- `offset(p, n)` returns a `RawPtr` whose integer value is `bits(p) +
  n`. There is no overflow check; raw arithmetic overflow is the
  caller's problem and matches C `uintptr_t` semantics.
- `read::<T>(p)` performs an aligned load of `sizeOf::<T>()` bytes
  from `p`, reinterpreting them as `T`. The caller is responsible for
  alignment and for `p` pointing at a live, properly-typed location.
  **Reading from freed or uninitialized memory is undefined behavior**
  in the LLVM IR sense — the optimizer may transform surrounding code
  accordingly. The lowering applies an LLVM `freeze` so the resulting
  value is a fixed-but-unspecified bit pattern rather than `poison`,
  but this does not retroactively make the surrounding control flow
  defined. Privileged code that reads possibly-uninitialized memory
  must mask, check, or otherwise sanitize the result before branching
  on it.
- `write::<T>(p, v)` performs the matching aligned store.
- `cas::<T>(p, old, new)` is a sequentially-consistent
  compare-and-swap. The type argument is constrained to
  `sizeOf::<T>() ∈ {1, 2, 4, 8, 16}` and `alignOf::<T>() ==
  sizeOf::<T>()`. Other sizes are rejected at the call site with
  `E0772 cas type size invalid` (a checker-side rule, not a link
  error). The intrinsic is provided for forward compatibility with
  future concurrent collectors; the v0.4 single-threaded STW GC may
  leave it unused.
- `sizeOf::<T>()` and `alignOf::<T>()` are compile-time constants
  computed by the lowering layer. Calls with a non-`Pod` type
  argument are rejected statically with `E0727`.

These intrinsics are **not** exported by the standard prelude. User
code cannot `use std.runtime.raw`; resolution rejects the import with
`E0770`.

### 19.6 New Annotations

The `#[...]` annotation syntax is unchanged (§1.9). The recognized
annotation set in §3.8 grows by six entries. None require new tokens
or grammar rules.

| Annotation | Applies to | Purpose |
|---|---|---|
| `#[intrinsic]` | `fn` declarations in privileged packages | Marks a function whose body is supplied by the lowering layer. The source body must be empty (`fn foo() -> T;` or `fn foo() -> T {}`). Generic intrinsics participate in monomorphization (§19.5). |
| `#[pod]` | `struct` declarations in privileged packages | Requests the checker to verify the struct's `Pod` shape (§19.4); rejection is `E0771`. |
| `#[repr(c)]` | `struct` declarations in privileged packages | Forces C ABI field order, padding, and alignment. Required on any struct used with `raw.read`/`raw.write` or passed across a `#[c_abi]` boundary. |
| `#[export("name")]` | top-level `fn` declarations in privileged packages | Emits the function with the exact symbol name `name`, disabling Osty name mangling. The argument is a string literal (§1.9). |
| `#[c_abi]` | top-level `fn` declarations in privileged packages | Use the platform C calling convention rather than the Osty calling convention. Almost always paired with `#[export("...")]`. The annotation name is `c_abi`, not `ccc`, to keep the surface readable for toolchain authors who do not work in LLVM IR daily. |
| `#[no_alloc]` | `fn` and method declarations in privileged packages | Forbids managed allocation in the function body, **and** any direct or transitive call to a function that allocates (§19.6.1). |

Annotation order on the same declaration is not significant; the
following stacking is canonical:

```osty
#[export("osty.gc.alloc_v1")]
#[c_abi]
#[no_alloc]
pub fn alloc_v1(bytes: Int, kind: Int) -> RawPtr { ... }
```

The `#[deprecated]` annotation may also appear on these functions.
`#[json(...)]` may not — privileged types are never serialized through
the stdlib json codec.

#### 19.6.1 The `#[no_alloc]` Check

A `fn` carrying `#[no_alloc]` must contain no expression whose
evaluation requires the managed allocator, and must not call any
function that does. The checker computes a least-fixed-point over the
call graph restricted to privileged packages. Self-recursion and mutual
recursion among `#[no_alloc]` functions are permitted.

The checker rejects, with `E0772 managed allocation in #[no_alloc]
body`, any of the following constructs reachable from the function
body:

- string interpolation (`"x = {x}"`) and string concatenation that
  produces a fresh `String`;
- triple-quoted and raw string literals that produce a fresh `String`
  at runtime (compile-time-interned static literals — see below — are
  permitted);
- list, map, or set literals (`[1, 2, 3]`, `[a: 1]`, `{1, 2, 3}`);
- struct literals whose declared type is **not** `Pod`;
- enum variant construction whose enum is not `Option<T: Pod>`
  (permitted) and not declared in `std.runtime.*` (also permitted).
  All other enum constructions, including `Result<…, …>`, are
  rejected;
- `Builder<T>` use of any kind;
- direct or transitive call to a function that is not `#[no_alloc]`
  and lives in the privileged-package call graph;
- any call into ordinary stdlib (which is non-privileged), regardless
  of whether that callee actually allocates;
- **call through a function-pointer parameter or `fn(...)` value** —
  the body cannot be analyzed, so the call is rejected. Privileged
  callers that need indirect dispatch use a `match` over a closed enum
  of `Pod` discriminators;
- **call through an interface value (vtable dispatch, §2.7.3)** —
  the dynamic dispatch boundary makes the callee's `#[no_alloc]`-ness
  unknowable. Privileged callers do not use interface values;
- **call to a default interface method** unless that default body is
  itself `#[no_alloc]` and lives in a privileged package.

Permitted constructs include:

- all `Pod`-only arithmetic, comparison, and bit-ops;
- all `raw.*` intrinsics from §19.5;
- control flow (`if`, `for`, `match`, `defer`);
- pattern matching on `Pod` values;
- calls to other `#[no_alloc]` functions in privileged packages,
  including self-recursion and mutual recursion;
- construction of `Option<T: Pod>` and pattern matching on it;
- construction of variants of enums declared in `std.runtime.*`
  (the privileged enum hierarchy);
- struct literals whose declared type is `Pod`;
- reads from and writes to top-level `let mut` bindings of `Pod` type,
  and to `Pod` fields of such bindings reached through `Pod` field
  access;
- **plain string literals** (`"abort"`, `"oom"`) with no
  interpolation, no triple-quoting, and no raw form. These are
  compile-time-interned into `.rodata`-equivalent storage; passing
  one to a `#[c_abi]` function as an `i8*`-shaped argument is
  allocation-free. The lowering details are in §19.7.

This check is what makes the GC implementable in Osty: it is impossible
for `collect_v1` to recursively re-enter the allocator through a
forgotten string interpolation in a debug log line, and it is
impossible for the runtime to call into ordinary stdlib (which would
itself need a working allocator).

### 19.7 Lowering

LLVM lowering of the intrinsics is fixed by this specification. The
backend is permitted to specialize but not to weaken the contract.
Lowering syntax below is shown in pre-opaque-pointer LLVM (typed
pointers) for readability; on the toolchain's required LLVM version
(opaque pointers, ≥15) every `bitcast i8* p to T*` collapses and `load`
/ `store` use `ptr` directly.

| Intrinsic | LLVM lowering |
|---|---|
| `raw.null()` | `inttoptr i64 0 to i8*` |
| `raw.fromBits(n)` | `inttoptr i64 %n to i8*` |
| `raw.bits(p)` | `ptrtoint i8* %p to i64` |
| `raw.alloc(b, a)` | `call i8* @osty_rt_alloc_aligned(i64 %a, i64 %b)` — see "shim" below. |
| `raw.free(p)` | `call void @free(i8* %p)` |
| `raw.zero(p, n)` | `call void @llvm.memset.p0i8.i64(i8* %p, i8 0, i64 %n, i1 false)` |
| `raw.copy(d, s, n)` | `call void @llvm.memmove.p0i8.p0i8.i64(i8* %d, i8* %s, i64 %n, i1 false)` |
| `raw.offset(p, n)` | `getelementptr i8, i8* %p, i64 %n` |
| `raw.read::<T>(p)` | `bitcast i8* %p to T*; %v = load T, T* %1, align alignof(T); freeze T %v` |
| `raw.write::<T>(p, v)` | `store T %v, T* %1, align alignof(T)` (after the matching bitcast) |
| `raw.cas::<T>(p, old, new)` | `cmpxchg T* %p, T %old, T %new seq_cst seq_cst`, then extract the success bit. Only emitted when `sizeOf::<T>()` is in `{1, 2, 4, 8, 16}` and `alignOf::<T>() == sizeOf::<T>()`; other shapes are caught at §19.5 by `E0772`. |
| `raw.sizeOf::<T>()` | constant folded from the data layout (no runtime call). |
| `raw.alignOf::<T>()` | constant folded from the data layout. |

**`raw.alloc` shim.** The symbol `osty_rt_alloc_aligned` is provided by
a single-translation-unit C shim at
`internal/backend/runtime/raw_alloc_shim.c`, unconditionally linked
into any binary that contains a `raw.alloc` call. Its definition is
exactly:

```c
#include <stdlib.h>
void *osty_rt_alloc_aligned(size_t align, size_t bytes) {
    void *p = NULL;
    if (posix_memalign(&p, align < sizeof(void*) ? sizeof(void*) : align, bytes) != 0) {
        return NULL;
    }
    return p;
}
```

The shim does **not** zero memory; per §19.5 the contract for
`raw.alloc` is uninitialized memory. Privileged callers that need a
zero-initialized block compose `raw.alloc` with `raw.zero`, or use the
`alloc_zeroed` helper that the privileged `std.runtime` package wraps
on top:

```osty
#[no_alloc]
pub fn alloc_zeroed(bytes: Int, align: Int) -> RawPtr {
    let p = raw.alloc(bytes, align)
    if raw.bits(p) != 0 { raw.zero(p, bytes) }
    p
}
```

**Plain string literals.** A bare `"…"` literal in a `#[no_alloc]`
body is emitted as an LLVM `private unnamed_addr constant` of type
`[N x i8]` and referenced by a constant `getelementptr` to its first
byte. When passed to a `#[c_abi]` function declared with an `i8*`-
shaped parameter (Osty `RawPtr` or, in v0.4 minor, the future
`CStr` opaque), the value crosses the ABI boundary as a pointer to
this constant. No allocation, no copy.

**`#[c_abi]`** causes the function declaration to be emitted with
LLVM's `ccc` calling convention.

**`#[export("name")]`** causes the function to be emitted with
`external` linkage, the `dso_local` preemption hint, and the exact
symbol name `name`. The combination of `#[c_abi]` and
`#[export(...)]` is what lets `osty_runtime.c` and the Osty-side
runtime export the same `osty.gc.*_v1` ABI symbols interchangeably.

**`#[no_alloc]`** does not produce a runtime check; it is purely a
front-end discipline. The body, once accepted, lowers like any other
function.

### 19.8 Interaction with Other Chapters

- **§1.9, §3.8 — Annotations.** The annotation grammar is unchanged.
  The recognized-annotation table grows by six entries. Applying any
  of the six in a non-privileged package is `E0770`, not the generic
  unknown-annotation diagnostic (`E0607`).
- **§2.1 — Primitive Types.** `RawPtr` is added to the table with a
  pointer to §19.3. It is not a member of the user prelude.
- **§2.6 — Interfaces.** `Pod` is added to the built-in instances
  table with a pointer to §19.4.
- **§9 — Memory Management.** Unchanged. The chapter still does not
  pin the GC algorithm; it just becomes possible for the chosen
  algorithm to be written in Osty.
- **§10 / §11 / §13 — Manifest schema.** The `[capabilities]` table is
  introduced by this chapter; the manifest chapter must surface it in
  the next minor of the manifest reference. Until then the schema
  reference is §19.2.
- **§12 — FFI.** Unchanged. `use go "..."` continues to be the only
  way for *user* code to reach a foreign symbol. The runtime
  primitives are not an FFI mechanism; they are a code-generation
  hook for the toolchain itself.
- **§14 — Excluded Features.** The exclusion of `unsafe` stands.
  §19 does not introduce an `unsafe` keyword, an `unsafe` block, or a
  user-reachable raw pointer. The package gate (§19.2) is what makes
  the surface implementation-private rather than user-facing.

### 19.9 Diagnostics Added by This Chapter

Codes are allocated in the `E0770–E0779` typecheck-extension band.
The control-flow band `E0760–E0769` is already in use by
`internal/diag/codes.go` (`CodeUnreachableCode`, `CodeMissingReturn`,
`CodeDefaultNotLiteral`); the runtime sublanguage uses the next free
band to avoid collision.

| Code | Meaning |
|---|---|
| `E0770` | Runtime primitive (annotation, type, intrinsic, or `std.runtime.*` import) used outside a privileged package. |
| `E0771` | A struct marked `#[pod]` violates the §19.4 shape rule. The diagnostic names the first offending field, or — for unbounded generic structs — the first type parameter that lacks a `T: Pod` bound. |
| `E0772` | Either (a) a managed allocation was found inside a `#[no_alloc]` body, with a span pointing at the offending expression and (when transitive) the first non-`#[no_alloc]` callee on the chain; or (b) a `raw.cas::<T>` call where `sizeOf::<T>()` is not in `{1, 2, 4, 8, 16}` or alignment does not match. |

The `Wxxxx` and `Lxxxx` bands are not used by this chapter; runtime-
primitive misuse is always a hard error.

### 19.10 Compiler-to-Runtime Safepoint Contract

The runtime's safepoint entry point cannot identify roots by reading
the stack itself (§19.1 makes that out of scope). Instead, the
compiler emits, at every safepoint call site, a call sequence that
materializes the live root array and passes it to the runtime
explicitly. The runtime walks an array, not the stack.

The contract for the C ABI symbol `osty.gc.safepoint_v1` is:

```osty
// signature, exported from the privileged runtime package
#[export("osty.gc.safepoint_v1")]
#[c_abi]
#[no_alloc]
pub fn safepoint_v1(
    site_id: Int,         // statically assigned per call site
    roots: RawPtr,        // pointer to a contiguous array of N RawPtr slot addresses
    root_count: Int,      // N
)
```

At each safepoint, the LLVM lowering layer:

1. Computes the set of live managed slots at that program point from
   the LLVM stackmap.
2. Materializes their addresses into a stack-allocated
   `[root_count x i8*]` array.
3. Emits a call to the exported `safepoint_v1` symbol with that
   array's address and length.

The runtime treats `roots` as a `RawPtr` to the start of an array;
slot `k` is `raw.read::<RawPtr>(raw.offset(roots, k * 8))`. Each slot
address points at a managed pointer. The runtime is free to **read**
those slots (to mark) and to **overwrite** them (for a future moving
collector); it must not free the addresses themselves.

The matching contract for the existing C runtime ABI symbols
`osty.gc.root_bind_v1`, `osty.gc.root_release_v1`, `osty.gc.post_write_v1`,
`osty.gc.load_v1`, `osty.gc.mark_slot_v1`, and `osty.gc.alloc_v1` is
preserved bit-for-bit. The Osty-side reimplementation of any of these
declares the same C ABI signature with `#[c_abi]` and the matching
`#[export("...")]`, and is link-compatible with the existing
`internal/backend/runtime/osty_runtime.c` so that switching from the C
runtime to the Osty runtime is purely a build-system decision.

### 19.11 Implementation Status

This chapter ships in slices: the full surface is specified, but
each piece is delivered by its own PR with focused tests. The
table below records what is wired today vs. deferred. Update it
in the same PR that lands a new piece.

**Front-end (parser / resolver / checker / stdlib).** Closed.

| Piece | Status | PR |
|---|---|---|
| Spec chapter (this file) | landed | #284 |
| `#[no_alloc]` body walker (`E0772`) | landed | #288 |
| Privilege gate (`E0770`) — annotations + `use std.runtime.*` + RawPtr/Pod type refs | landed | #292 |
| `#[pod]` shape checker (`E0771`) | landed | #295 |
| `RawPtr` registered as `types.PRawPtr` + prelude `SymBuiltin` | landed | #312 |
| `Pod` in prelude as `SymBuiltin` + privilege gate covers generic bound clauses | landed | #314 |
| Annotation arg validators (`#[repr(c)]`, `#[export("name")]`, no-arg flags) | landed | #316 |
| `std.runtime.raw` stdlib module + nested-stub-path loader | landed | #319 |
| Privilege gate body walker (let-types / turbofish / closure params inside fn bodies) | landed | #322 |
| End-to-end fixture + regression test | landed | #325 |
| `#[intrinsic]` body must be empty (`E0773`) | landed | #347 |

**IR layer.** Every §19.6 annotation is now representable on
`ir.FnDecl` / `ir.StructDecl`. No codegen attached yet for most.

| Piece | Status | PR |
|---|---|---|
| `ir.FnDecl.ExportSymbol string` from `#[export("name")]` | landed | #329 |
| `ir.FnDecl.CABI bool` from `#[c_abi]` | landed | #330 |
| `ir.FnDecl.IsIntrinsic bool` from `#[intrinsic]` | landed | #334 |
| `ir.FnDecl.NoAlloc bool` from `#[no_alloc]` | landed | #336 |
| `ir.StructDecl.Pod bool` from `#[pod]` | landed | #336 |
| `ir.StructDecl.ReprC bool` from `#[repr(c)]` | landed | #336 |

**MIR / LLVM emit.** Both backend paths now honor `#[export]`. The
MIR pipeline (`GenerateFromMIR`, opt-in via `Options.UseMIR`)
overrides the emitted `@<name>` directly; the legacy
`GenerateModule` (default) preserves the original define and
appends an `alias` line so in-module callers continue to resolve
while the export symbol is link-reachable.

| Piece | Status | PR |
|---|---|---|
| MIR `Function.ExportSymbol` + `define @<symbol>` override | landed (MIR path) | #329 |
| MIR `Function.CABI` + `define ccc <ret> @<sym>(...)` emission | landed (MIR path) | #330 |
| MIR `Function.IsIntrinsic` propagation + backend-bail safety net | landed (MIR path) | #334 |
| Legacy `GenerateModule(IR)` emits `@<symbol> = dso_local alias ptr, ptr @<fn>` per `#[export]`-tagged fn | landed (legacy path) | #341 |
| Legacy `GenerateModule(IR)` honoring `#[c_abi]` | n/a — LLVM's default cc is `ccc`, so the legacy path is already correct semantically. The MIR path emits the `ccc ` keyword for documentation; if Osty later switches its native cc to a non-`ccc` form the legacy path will need a post-process inject. | — |
| Per-intrinsic LLVM emit for the 13 §19.5 `raw.*` intrinsics | **deferred** | — |
| §19.7 lowering table (`raw.null` → `inttoptr i64 0`, etc.) | **deferred** | — |
| §19.10 safepoint compiler-emitted root array | **deferred** | — |

**Type system / native checker / IR lowering.** Closed.

| Piece | Status | Notes |
|---|---|---|
| Native checker types `raw.null()` / `raw.alloc(b, a)` / `raw.bits(p)` correctly | **landed** | The native checker's `chk.Types[CallExpr]` and `chk.LetTypes[LetStmt]` already return `RawPtr` / `Int` for non-generic intrinsics. The IR-side fix (`PrimRawPtr` added to `ir.PrimKind` + `TRawPtr` singleton + missing case arms in `primitiveByKind` / `primitiveByName`) made the native-checker output reach `let.Type` instead of dropping to `ErrTypeVal`. (PR #345 originally misdiagnosed this as a native-checker gap; PR #349 corrected the diagnosis and landed the IR fix.) |
| Generic intrinsic typing — `raw.read::<T>` / `raw.write::<T>` / `raw.cas::<T>` / `raw.sizeOf::<T>` / `raw.alignOf::<T>` | **landed** | Two fixes in PR `claude/boundary-preserve-generics`: (a) `host_boundary.writeSelfhostPackageImport` now emits `<T: Pod>` via the new `selfhostGenericParams` helper so the native checker sees the stub's generic params; (b) `frontCheckTurbofishCall` (in `examples/selfhost-core/check.osty` + `internal/selfhost/generated.go`) now tries `frontCheckSigLookup(env, packageName)` on package-qualified turbofish calls before falling through to method lookup, mirroring the non-turbofish dispatch. The cached `osty-native-checker` binary at `<projectRoot>/.osty/toolchain/<version>/` must be deleted so `EnsureNativeChecker` rebuilds. The contract suite at `internal/llvmgen/runtime_intrinsic_typing_test.go` now has 9/9 PASS. |

**Out-of-scope (per §19.1) — never planned.**

- A user-facing `unsafe` block (§14 still excludes it).
- Stackmap *introspection* intrinsics (the runtime walks the
  compiler-emitted root array per §19.10, not the stack).
- Volatile / atomic-fence / inline-assembly primitives. The first
  GC delivered through this surface is single-threaded STW.
