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
stdlib APIs (`fs.withFile`, `net.withConn`, etc.).

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

---
