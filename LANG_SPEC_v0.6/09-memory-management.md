## 9. Memory Management

Osty v0.6 is a garbage-collected language. The runtime owns object
lifetime; user code never explicitly allocates or frees. There are no
`new`, `delete`, destructors, finalizers, or weak references — the
contract is *all reachable objects survive, all unreachable objects
are eventually reclaimed*. Resource cleanup outside memory (file
handles, sockets, mutex guards) is bound to lexical scope through
`defer` (§4.12) or closure-based stdlib helpers, never to GC timing.

The v0.6 specification adds no new memory-management surface. The
v0.6 capability surface (§20) and information flow (§21) compose with
GC ownership but do not change it: a tainted `String` is reclaimed
identically to an untainted one, and a `Clock` capability instance
has the same lifetime rules as any reference value.

Resource cleanup is done through `defer` (§4.12) or closure-based
helpers on the relevant capability (`Fs.withFile(self, path, body)`,
`Net.withConn(self, addr, body)`, etc.).

`std.sync` provides `Mutex`, `RwLock`, and atomics.

### 9.1 Scope of this specification

This chapter intentionally stops at the language-level contract. The
specification does **not** pin down:

- GC algorithm (generational, mark-sweep, concurrent, ...)
- Collection triggers (allocation pressure, timers, manual hints)
- Stop-the-world vs concurrent reclamation
- Heap sizing, tuning knobs, or runtime flags

These are implementation choices. Future Osty runtimes are free to
change them without a language-spec version bump. User code should not
rely on observable GC timing for correctness.

### 9.2 What is specified

- **No finalizers.** Osty deliberately omits finalizer hooks. All
  cleanup goes through `defer` or closure-scoped stdlib helpers, so
  resource lifetimes are tied to lexical scope — not to GC timing.
- **No weak references.** Osty has no `Weak<T>` type. Use explicit
  data structures (e.g. a `Map` keyed on ID) when decoupling lifetime
  from reachability is required.
- **Allocation failure (OOM) aborts the process.** OOM is not a
  recoverable condition in Osty. `abort(msg)` (§10.1) with a
  descriptive message is invoked before termination.
- **Reference cycles are reclaimed.** The GC must handle cycles. User
  code cannot leak a cycle between `struct`/`enum`/closure references,
  even via `mut` back-edges.

### 9.3 GC and v0.6 surfaces

The v0.6 annotation surface composes with GC ownership in three
ways:

#### 9.3.1 Capability instances and GC

Capability values (`Clock`, `Net`, etc.) are interface fat
pointers — the underlying capability data is GC-managed. A
captured capability extends its underlying data's lifetime exactly
like any other reference:

```osty
fn buildHandler(net: Net) -> fn(String) -> Result<Bytes, Error> {
    |url| net.fetch(url)         // captured `net` — GC keeps it alive
}
```

The closure returned by `buildHandler` outlives `buildHandler`'s
stack frame; the captured `net` is rooted through the closure, so
GC keeps it (and its underlying state) alive for the closure's
lifetime.

Capability adapter implementations may hold non-GC-managed
resources (file descriptors, sockets, process handles). Such
resources should be cleaned up via `defer` or `Closer.close`,
*not* via GC. The pattern: the capability *interface value* is GC-
managed; the resources it gates access to are handled by `defer`.

#### 9.3.2 Flow tags and GC

Flow tags are *value-level* annotations — they ride on bindings,
not on object identity. GC reclaims an object based on
reachability; tags are irrelevant to the reclamation decision.
Specifically:

- An untagged binding alive at scan time roots the object.
- A tagged binding alive at scan time also roots the object.
- The tag set has no GC observable.
- Reclamation does not "drop" tags; tags only matter while a
  binding holds a value.

This is what lets flow tracking work without per-object metadata
overhead.

#### 9.3.3 `#[budget(allocs)]` and GC pressure

The `allocs = N` budget (§3.15) counts allocation *sites* in the
function's call graph, not *bytes* allocated. The relationship to
GC pressure:

- `allocs = 0` — no GC work attributable to this function.
- `allocs = N` — at most N objects allocated per call. GC pressure
  scales with N × call frequency.
- `allocs = INF` — no budget; the function may allocate freely
  (default for non-budgeted functions).

The compiler proves the static count by walking the call graph;
runtime allocation count under `osty bench --budget` should match.
A discrepancy is a static-analysis bug or a runtime cost-model
issue.

### 9.4 Defer cleanup patterns

`defer` (§4.12) is the primary cleanup mechanism. The v0.6
patterns:

#### 9.4.1 Single resource

```osty
fn copyFile(fs: Fs, src: String, dst: String) -> Result<(), Error> {
    let r = fs.open(src)?
    defer r.close()
    let w = fs.create(dst)?
    defer w.close()
    io.copy(w, r)?
    Ok(())
}
```

Each `defer` runs on every exit path: normal completion, `?`-propagated
error, and cancellation. The two `close` calls run in LIFO order
(w first, then r).

#### 9.4.2 Conditional cleanup

```osty
fn renameIfNotEmpty(fs: Fs, src: String) -> Result<String, Error> {
    let f = fs.open(src)?
    defer f.close()
    let buf = io.readAll(f)?
    if buf.isEmpty() {
        return Err(Error.new("source is empty"))
        // f.close() runs here on early return
    }
    let dst = "{src}.processed"
    fs.rename(src, dst)?
    Ok(dst)
}
```

`defer` does not need conditionals — cleanup runs unconditionally
on exit. Authors who *want* conditional cleanup express it inside
the deferred body:

```osty
let mut succeeded = false
defer {
    if !succeeded {
        ignoreError(fs.remove(tempPath))
    }
}
// ... do work; on success: succeeded = true ...
```

#### 9.4.3 Defer and capability methods

Capability methods called inside `defer` follow the standard rule:
the capability is captured by the deferred expression. The
capability remains in scope until the deferred code runs (i.e.
until block exit).

For long-lived deferred bodies, the captured capability extends
its effective lifetime — unusual but legal.

### 9.5 Concurrency memory model

Osty v0.6 uses a **DRF-SC** memory model: every program with no data
races observes behavior equivalent to a sequentially consistent
interleaving of tasks. The scheduler may run tasks on many OS threads,
but synchronization edges define the only cross-task visibility
guarantees.

#### 9.5.1 Happens-before

The happens-before relation is the transitive closure of:

- Source order within one task.
- `Mutex.unlock` happens-before a later successful `Mutex.lock` on the
  same mutex.
- `RwLock` write unlock happens-before a later read or write lock; read
  unlock participates in the usual reader/writer exclusion ordering.
- Channel send happens-before the matching receive of that value.
- `ch.close()` happens-before any receive that observes the closed,
  drained state (`None`).
- Child task completion happens-before a successful `Handle.join` that
  observes that completion.
- `taskGroup` scope exit happens-after every child in the group has
  completed or observed cancellation.
- Atomic operations synchronize according to their declared ordering.
  v0.6 exposes `seq_cst` atomics as the portable baseline; weaker
  acquire/release/relaxed forms are reserved for a future revision.

#### 9.5.2 Data races

A data race occurs when two tasks access the same mutable memory
location concurrently, at least one access is a write, and the accesses
are not ordered by happens-before and are not atomic operations on the
same atomic object. A program with a data race is invalid: a conforming
implementation may reject it statically when it can prove the race,
abort in an instrumented runtime, or leave the behavior unspecified in
an optimized build. Safe Osty code should use channels, `Mutex`,
`RwLock`, or atomics for every shared mutable location.

Immutable values and values reachable only from one task are race-free.
Sharing an interface value or capability between tasks is legal only if
the implementation is internally synchronized or the caller protects it
with `std.sync`.

#### 9.5.3 Atomics and volatile

Atomic integer and boolean cells provide indivisible load, store,
compare-and-swap, and fetch-update operations. In v0.6 every public
atomic operation is sequentially consistent. This keeps the first
portable contract simple; lower-level memory orders require an RFC.

Osty has no user-visible `volatile` operation in v0.6. FFI bindings
that need volatile or device-memory semantics must hide them behind a
capability whose methods provide their own synchronization contract.

---
