## 8. Concurrency

Osty v0.6 supports concurrency exclusively through *structured
concurrency* — every task lives within a `taskGroup` scope and joins
or fails with its parent. Detached tasks, `async`/`await`, and
`spawn` keywords are excluded by §14. Green tasks multiplex onto an
M:N scheduler (§8.0); communication uses channels (§8.5) or shared
state under `std.sync` primitives (§9.5). The non-escaping rule for
`Handle<T>` and `TaskGroup` capabilities (G13, §8.2) is enforced by a
finite front-end check, not lifetimes.

Capability parameters (§20) flow through `taskGroup` naturally:
spawned closures *capture* the outer scope's capabilities by
reference, so a child task that needs a `Clock` is satisfied by the
enclosing function's `clock` parameter without any explicit hand-off.
What does **not** cross is `#[ambient]` binding (§20.3) — ambient
binding is scoped to the enclosing entry-point function, never to
spawned closures. A child that wants ambient access must instead
*receive* the capability as a parameter (or capture a binding that
already received it).

`Net`, `Fs`, and `Clock` are cancellation-aware: every blocking
operation honors §8.4.2's cancellation contract and returns
`Err(Cancelled { cause })` when the surrounding `taskGroup` is being
torn down. CPU-bound work without stdlib calls must check
`thread.isCancelled()` (§8.4.2) explicitly.

The non-escaping rule for `Handle<T>` and `TaskGroup` (G13, §8.2) is
enforced by a finite front-end check, not lifetimes — the rule
predates v0.6 and is unchanged.

### 8.0 Scheduler Model

Osty specifies an **M:N scheduler**. Task-level units (`g.spawn(...)`, the
helpers in §8.3, and the root task started by `taskGroup`) are **green
tasks** multiplexed onto a pool of worker OS threads. The language does
not expose the OS thread a task runs on, and implementations are free to
migrate a task between workers between yield points.

Programs **MUST NOT** rely on any of the following:

- the identity of the OS thread executing a task (no thread-local
  storage observable through the language surface; no `gettid`-style
  introspection);
- a specific task running concurrently with, or serialized against,
  another task unless the synchronization is expressed through a
  language primitive (`Handle.join`, a channel operation, or the
  cancellation surface in §8.4);
- the number of worker threads, a ratio of tasks to workers, or a
  scheduling policy (FIFO vs work-stealing vs LIFO, fair vs unfair
  among ready tasks). `race` tie-breaking in §8.3 is explicit about
  this: deterministic within a run, unstable across runs.

**Yield points.** A conforming runtime may treat the following as
yield points (opportunities for the scheduler to run another task on
the same worker): channel send/recv, `Handle.join`, `thread.yield`,
`thread.sleep`, `thread.select` blocking arms, GC safepoints (§19.10),
and cancellation checks (§8.4). Tight compute loops with no such
points need not yield; programs that require progress on siblings
must include an explicit yield, a channel op, or a cancellation
check. A future revision may add compiler-inserted preemption at
loop backedges; programs written to the yield-point contract keep
working when that lands.

**Parallelism.** Parallel execution across workers is **permitted but
not guaranteed**. A single-worker implementation satisfies this
chapter; so does a many-worker implementation. The observable
contracts — structured lifetime (§8.1), failure propagation (§8.2),
cancellation with cause (§8.4) — hold identically in both cases. The
`RUNTIME_SCHEDULER.md` roadmap describes the delivery phases for the
reference LLVM-backed runtime.

**Thread-identity clause does not weaken FFI.** `use go` imports
and the `#[c_abi]` surface in §19 still run on an OS thread with its
own stack; blocking FFI calls do not freeze the whole scheduler, but
they may pin a worker for the duration of the call. A runtime is free
to grow its worker pool or hand off to a carrier thread to preserve
progress on other tasks.

#### 8.0.1 Scheduler interaction with v0.6 surfaces

The M:N scheduler design composes with the v0.6 surfaces as
follows:

**Capability access from any worker.** A capability instance is
shared (reference-semantic). A task that calls `net.fetch(...)`
may be running on any worker; the capability's host adapter is
internally synchronized for cross-worker access. Stdlib adapters
(`time.systemClock`, `random.host`, etc.) all satisfy this
contract.

**Flow tags are task-local.** Each task carries its own tag-set
view of the values it manipulates. Tasks do not share tag state
across the worker pool — sending a tagged value through a channel
preserves the tag (§8.5.1), but worker migration of a task does
not affect the tag set its bindings carry.

**Cancellation token is task-bound, not worker-bound.** When a
`taskGroup` cancels, the runtime sets a per-task flag. Workers
check the flag at yield points and propagate `Err(Cancelled
{ ... })`. Worker migration during a blocking operation does not
lose the cancel state — the flag rides with the task.

**`#[reproducible]` is not affected by worker scheduling.**
Reproducibility is a property of the *function's logical
behavior* (same input → same output). Worker assignment is
implementation-detail; a `#[reproducible]` function produces the
same result regardless of which worker happens to execute it.

#### 8.0.2 Scheduler and `#[budget]`

Runtime budget keys (`time_ms`, `p99_ms`) measure *wall-clock
time* — the time elapsed regardless of worker placement. A
function whose budget is `time_ms = 5` may run on a busy worker
(blocked behind other tasks) or a free worker (executing
immediately); the measurement is end-to-end, including any wait
time.

For deterministic budget testing, `osty bench --budget` runs each
benchmark in isolation by default — no other tasks contend for
workers during the timed loop. The flag `--bench-contention`
allows simulating multi-task contention if a benchmark wants to
measure that explicitly.

### 8.1 Structured Concurrency

All concurrent tasks belong to a `taskGroup` scope. There is no detached
spawn. `taskGroup` and `parallel` are in the prelude. Capability
parameters captured by spawned closures flow naturally through the
group:

```osty
fn fetchAll(net: Net) -> Result<(Bytes, Bytes, Bytes), Error> {
    taskGroup(|g| {
        let h1 = g.spawn(|| net.fetch("https://a.example/data"))
        let h2 = g.spawn(|| net.fetch("https://b.example/data"))
        let h3 = g.spawn(|| net.fetch("https://c.example/data"))
        Ok((h1.join()?, h2.join()?, h3.join()?))
    })
}
```

`g.spawn(closure)` returns `Handle<T>`. `Handle<T>` and `TaskGroup` are
**non-escaping capabilities**. They may be used inside the same
`taskGroup` closure and may be passed to helper functions that do not
store or return them, but they may not escape the group. Returning one,
storing one in a struct field or long-lived collection, sending one over
a channel, or capturing one in a closure that can outlive the group is a
**compile error** (`E0743`). This preserves the invariant that every task
completes before its parent returns.

**Escape check.** G13 is a conservative syntactic check. A `Handle<T>`
or `TaskGroup` value escapes when it is returned, assigned to a field
or global, inserted into a collection, sent over a channel, or captured
by a closure whose lifetime is not statically known to end before the
current `taskGroup` scope exits. Capturing is allowed only for
immediately-invoked closures, closures passed directly to the same
group's `g.spawn`, and standard-library higher-order parameters that
the spec marks no-escape. Merely capturing a handle and never using it
is still an escape if the closure can outlive the group; the diagnostic
is `E0743`.

### 8.2 Failure Semantics

**`taskGroup`** — if any child fails by returning `Err(e)`, the group
enters cancellation: all remaining siblings receive a cancel signal
(§8.4) and the first observed error is propagated to the group's
caller. Programmer-error termination (`abort`, `panic` from FFI,
`unreachable`, `todo`, `os.exit`) is not recoverable and bypasses the
group failure path.

**`collectAll`** — all children run to completion; results are
collected regardless of individual failures. The outer scope can still
cancel the `collectAll` by cancelling the enclosing `taskGroup`, at
which point children shut down via the normal cancel path.

```osty
fn collectAll<T>(body: fn(Group) -> List<Handle<T>>) -> List<Result<T, Error>>
```

**`abort(msg)` inside a task** terminates the process. It is not a
recoverable failure: `abort` bypasses the `taskGroup` failure path and
does not deliver `Err` to siblings or parents. Use `Err(Error.new(msg))`
when recovery is intended.

### 8.3 High-Level Helpers

```osty
fn parallel<T, R>(items: List<T>, concurrency: Int,
                  f: fn(T) -> Result<R, Error>) -> List<Result<R, Error>>

fn race<T>(body: fn(Group) -> List<Handle<T>>) -> Result<T, Error>
```

**`race` tie-breaking.** When two handles complete at indistinguishable
times, `race` returns the one whose completion the scheduler observes
first — i.e. the first completion registered in internal scheduler
order. This is deterministic within a run but not stable across runs;
do not depend on a specific tie-break.

### 8.4 Cancellation

Cancellation in Osty is **structured and automatic**.

#### 8.4.1 Propagation Model

A `taskGroup` defines a cancellation scope. If the group is cancelled
(either explicitly or because a sibling failed per §8.2), **every
descendant task** — including tasks spawned transitively by children —
receives the cancel signal. The cancel signal carries a **cause**:

```osty
pub enum Cancelled {
    cause: Error,     // the originating error, or Error.new("parent cancelled")
}
```

The stdlib `Cancelled` value is constructed by the runtime. Callers
encounter it as `Err(Cancelled { ... })` from any cancellation-aware
call.

#### 8.4.2 Cancellation Points

Every standard-library blocking call is cancellation-aware and returns
`Err(Cancelled { cause })` as soon as the cancel signal is observed —
regardless of how much real time remains on the operation:

```osty
time.sleep(30.min)         // returns early with Err(Cancelled) on cancel
net.read(conn, buf)        // likewise
fs.read(f, buf)            // likewise
ch.recv()                  // returns None and the enclosing call returns
```

CPU-bound code that does not make stdlib calls must check explicitly:

```osty
thread.isCancelled() -> Bool
thread.checkCancelled() -> Result<(), Error>   // helper: Err(Cancelled) when cancelled
```

#### 8.4.3 Interaction with `defer`

`defer`red blocks run on normal scope exit, on `Err(...)?` propagation,
**and** on cancellation. Cleanup is always executed. A `defer` block
is itself run to completion regardless of pending cancel state; a
blocking call inside a `defer` does not honor the cancel signal (the
cleanup path is intentionally uninterruptible). Authors who need
bounded cleanup should enforce a timeout inside the `defer` body.

`defer` does **not** run when the process terminates via `abort`,
`panic` crossing the FFI bridge, `unreachable`, `todo`, or `os.exit` —
these are immediate terminations.

When multiple `defer` blocks are registered in the same block, they run
in LIFO order. If a deferred block aborts, panics through FFI, or calls
`os.exit`, process termination begins immediately and remaining
deferred blocks are skipped. If cancellation arrives while a deferred
block is already running, it is observed only after the deferred block
finishes; the cleanup path is a cancellation-masked region.

#### 8.4.4 `collectAll` Under Cancel

`collectAll` keeps its children alive through sibling failures, but a
cancel signal from the **enclosing** `taskGroup` propagates into the
`collectAll` children normally — the collected list then contains
`Err(Cancelled { cause })` for any child that was mid-flight.

#### 8.4.5 Triggering Cancellation Explicitly

A `taskGroup` body may call `g.cancel(cause)` to trigger cancellation
on every descendant task without first failing a sibling. Use cases:

- A "first-success" pattern where one child finds the answer and the
  others should stop:

  ```osty
  fn findFirst(net: Net, urls: List<String>) -> Result<Bytes, Error> {
      taskGroup(|g| {
          let handles = urls.map(|u| g.spawn(|| net.fetch(u)))
          for h in handles {
              if let Ok(body) = h.join() {
                  g.cancel(Cancelled.New("found"))   // tell siblings to stop
                  return Ok(body)
              }
          }
          Err(Error.new("all fetches failed"))
      })
  }
  ```

- A timeout scope — the caller cancels after a deadline elapses:

  ```osty
  fn withTimeout<T>(clock: Clock, d: Duration,
                    body: fn(Group) -> Result<T, Error>) -> Result<T, Error> {
      taskGroup(|g| {
          g.spawn(|| {
              clock.sleep(d)?
              g.cancel(Cancelled.New("timeout"))
              Ok(())
          })
          body(g)
      })
  }
  ```

`g.cancel(cause)` is **idempotent** — calling it twice on the same
group raises the same signal once. The cause from the first call
wins; subsequent calls' causes are dropped.

#### 8.4.6 Cancellation does not equal failure

A `taskGroup` that completes successfully *despite* an internal
cancel (e.g. the first-success pattern above) returns `Ok(...)` to
its caller. Cancellation is the mechanism that *stops sibling work*,
not a failure mode in itself. The receiver of `Cancelled` must:

1. Run any necessary `defer` cleanup.
2. Return `Err(Cancelled { ... })` to its caller (do **not** swallow).
3. Avoid blocking calls inside `defer` (uninterruptible cleanup —
   §8.4.3).

A child that catches `Cancelled` and returns `Ok(())` is a soundness
bug: the parent then continues with stale state. The compiler does
not enforce this; it is a discipline that the cancellation contract
relies on.

#### 8.4.7 Capability adapter responsibilities

Every capability adapter that performs a blocking operation **must**
honor the cancel signal of the surrounding task. The contract for
adapter authors:

| Operation | Cancellation behavior |
|---|---|
| Read/write on a stream (`Net.read`, `Fs.read`, ...) | Return `Err(Cancelled { cause })` as soon as the signal is observed — partial buffer is fine |
| Sleep / wait (`Clock.sleep`, `clock.until(...)`) | Return immediately with `Err(Cancelled { cause })` |
| Channel ops (`ch.recv`, `ch.send` on full buffer) | Return `None` / unblock and propagate |
| FFI calls | Cannot generally be interrupted; document the limit and consider running them on a carrier thread |
| In-memory adapters (`io.bytesReader`, `io.buffer`) | No-op (never block, never see the signal) |

Adapters that do *not* honor cancellation are not added to the
canonical capability surface. A bespoke capability that wraps such
an adapter must document the limitation and is *not* automatically
acceptable to `#[reproducible_capability]` enforcement.

### 8.5 Channels

```osty
let ch = thread.chan::<Int>(100)
ch <- value                          // send statement
let x = ch.recv()                    // T?
for x in ch { ... }
ch.close()
```

Channel send (`<-`) is a statement.

**Buffering.** `thread.chan::<T>(capacity)` creates a channel with the
given capacity. A capacity of `0` means a **synchronous rendezvous**
channel: each send blocks until a matching receive is in progress (and
vice versa). Positive capacity gives FIFO buffering; sends block only
when the buffer is full.

**Send atomicity.** A single `<-` operation is atomic with respect to
other concurrent senders and receivers on the same channel. Values are
delivered whole — never partially observed.

**Close semantics.**

- `ch.close()` signals that no further values will be sent.
- **Any task may close a channel.** Close has a single linearization
  point. If two tasks close concurrently, exactly one close wins and
  every other close observes the already-closed state and aborts. This
  is not idempotent by design — double close indicates a coordination
  bug.
- Sending on a closed channel aborts.
- `ch.recv()` returns buffered values until the buffer is empty **and**
  the channel is closed, at which point it returns `None`. `for x in ch`
  therefore terminates naturally when the channel is closed and
  drained.
- A receiver blocked on an empty channel wakes promptly when `close`
  linearizes and returns `None`. Receivers blocked while buffered
  values exist wake to receive those values first; only receivers beyond
  the drained buffer observe `None`.
- `ch.isClosed() -> Bool` reports close state without consuming a
  value.
- `ch.recv()` is a cancellation point per §8.4.2 — it returns `None`
  early when the surrounding task is cancelled (the caller distinguishes
  cancel from drain by checking `thread.isCancelled()`).

#### 8.5.1 Channels and information flow

A `Channel<T>` carrying tainted values preserves the flow tag set
through send and receive. There is no implicit declassification at
the channel boundary:

```osty
let ch = thread.chan::<#[taint("user_input")] String>(64)

// Producer
ch <- userInput

// Consumer
for msg in ch {
    // `msg` is #[taint("user_input")] String — sanitize before sink
    db.exec(sql.eq("col", sql.string(msg))?)
}
```

A consumer task at a different point in the program receives the
same flow tags as the producer attached. Channels do not flatten
trust — they are a synchronous-with-respect-to-tags transport.

#### 8.5.2 Channels and capability lifetime

Sending a capability instance through a channel is *legal* but
discouraged. The capability remains live for as long as a receiver
holds it, which may extend its effective lifetime past the
construction context. Idiomatic patterns:

- **Send work, not capabilities.** A producer sends *requests*;
  the consumer holds its own capability and applies it to each
  request. Capabilities stay scoped to construction.
- **Send results, not handles.** A producer that does its own I/O
  sends `Result<T, Error>` payloads; the consumer never needs the
  upstream `Net` / `Fs` instance.

The non-escaping rule for `Handle<T>` and `TaskGroup` (§8.1, G13)
applies *only* to those two types — capability instances are not
included, but the discipline of avoiding cross-scope capability
sharing is recommended.

#### 8.5.3 Channels and `#[budget]`

A `thread.chan::<T>(capacity)` allocation counts as one allocation
site for `#[budget(allocs)]` purposes. Each `ch <- value` and
`ch.recv()` counts as one channel operation; a function that
loops `ch.recv()` to drain a channel of `n` items counts `n`
channel operations.

The runtime's per-channel state (FIFO buffer, sender/receiver
queues) is one allocation per channel; growing the buffer past its
declared capacity is not supported (the channel rejects further
sends until the receiver makes progress).

### 8.6 Select

```osty
let result = thread.select(|s| {
    s.recv(ch1, |x| handle1(x))
    s.recv(ch2, |x| handle2(x))
    s.send(out, value, || sent())
    s.timeout(5.s, || giveUp())
    s.default(|| nonBlocking())
})
```

Exactly one branch runs. When multiple non-`default` branches are ready
at the same moment, the scheduler chooses among them non-
deterministically. Branches registered on the `select` builder are
**evaluated sequentially** in registration order when computing
readiness; this sequential evaluation is observable only through side
effects inside a branch's argument expressions.

**`default` priority.** The `default` branch runs **only if no other
branch is ready** at the moment the `select` is evaluated. A ready
branch always wins over `default`. There is no race between `default`
and a simultaneously-ready branch.

**Closed channels in `select`.** A `recv` branch on a closed, drained
channel is "ready" and fires once with `None` (matching the `recv`
semantics of §8.5). A `send` branch on a closed channel aborts when
selected.

#### 8.6.1 Select and information flow

A `select` branch's body runs in the surrounding scope's lexical
environment, with the branch's parameter typed per the channel's
element type. Flow tags ride through identically:

```osty
let userInput: Channel<#[taint("user_input")] String> = thread.chan(64)
let serverEvents: Channel<#[taint("net_input")] Event> = thread.chan(64)

thread.select(|s| {
    s.recv(userInput, |msg| {
        // msg: #[taint("user_input")] String
        log.info("user typed: {std.html.escape(msg)}")
    })
    s.recv(serverEvents, |evt| {
        // evt: #[taint("net_input")] Event
        handle(evt)
    })
})
```

There is no implicit declassification at the select boundary —
each branch's body sees its channel's tag set directly.

#### 8.6.2 Select and cancellation

`thread.select` is a cancellation point per §8.4.2. When the
surrounding `taskGroup` is cancelled while `select` is blocked on
its branches, the runtime returns *as if no branch was selected*
— the `select` expression evaluates to `()` and execution
continues. The caller checks `thread.isCancelled()` to detect this:

```osty
thread.select(|s| {
    s.recv(ch1, |x| ...)
    s.recv(ch2, |x| ...)
})
thread.checkCancelled()?            // propagate cancel if it fired
```

If a `select` body needs to react to cancel separately from its
ready branches, register an explicit `s.timeout(d, ...)` arm with
a short duration — the timer fires before most blocking arms,
giving the body a periodic point to check `thread.isCancelled()`.

#### 8.6.3 Select and `#[budget]`

Each registered branch counts as zero `io_calls` until it actually
fires — `select` itself is the synchronization point. The `time`
spent waiting in `select` is *not* counted toward `#[budget(time_ms
= X)]`; only the active body's wall-clock time counts.

For a function that loops over `select`, the budget applies to
each iteration's *active branch body*, not to the number of
iterations. Use `loop { ... }` exit conditions to bound iteration
count separately if required.

### 8.7 Capabilities and Tasks

The capability surface (§20) and structured concurrency compose
through three rules:

#### 8.7.1 Closures capture by reference

A closure passed to `g.spawn(...)` keeps the outer scope's
capabilities alive for the closure's lifetime, exactly as it would
keep any other captured binding alive. The captured reference is
shared — multiple sibling tasks spawned from the same enclosing
function may legitimately call methods on the same `Net` or `Fs`
instance concurrently. Capabilities are themselves expected to be
internally synchronized; a host `Net` adapter that opens a TCP
connection per call is automatically thread-safe under this rule.

```osty
fn fanout(net: Net, urls: List<String>) -> Result<List<Bytes>, Error> {
    taskGroup(|g| {
        let handles = urls.map(|u| g.spawn(|| net.fetch(u)))   // shared `net`
        handles.map(|h| h.join()).traverse(|r| r)
    })
}
```

#### 8.7.2 Ambient binding does not cross spawn

`#[ambient(...)]` only binds names inside the *enclosing entry-point
function*. A child task spawned via `g.spawn(|| ...)` cannot reference
an ambient `clock` even if the entry point declared one — the closure
must capture the binding explicitly:

```osty
#[ambient(clock)]
fn main() {
    taskGroup(|g| {
        // ✅ closure captures the ambient `clock` from main's scope
        g.spawn(|| clock.sleep(1.s))

        // ❌ helper called below would receive *no* ambient clock
        //    — its capability parameter must be passed explicitly
        g.spawn(|| sweep(clock))
    })
}

fn sweep(clock: Clock) -> Result<(), Error> {
    clock.sleep(5.s)?
    Ok(())
}
```

#### 8.7.3 Cancel signals are capability-blind

Cancellation flows by *task lineage*, not by capability identity. If
a parent's `taskGroup` cancels, every descendant blocking call —
regardless of which capability it is on, including ones the parent
never directly used — observes `Err(Cancelled { cause })`. This is
why the `Net` / `Fs` / `Clock` adapters are required to be
cancellation-aware (§8.4.2).

A capability that cannot honor cancellation (a hypothetical
synchronous FFI symbol with no abort path) must document that limit
and is *not* added to the canonical capability surface.

#### 8.7.4 Capability-typed channels

A channel's element type may be a capability instance, but it is
almost always a mistake. A `thread.chan::<Net>(8)` would let a
producer hand a network handle to a consumer that lives in a
different `taskGroup`, breaking the lifetime model: capabilities
are not `Handle<T>`-flavored and have no escape rule, but in practice
a host `Net` adapter often holds resources tied to its construction
context. Prefer sending request/response data through the channel
and keep the capability bound to the original scope.

---
