## 8. Concurrency

### 8.1 Structured Concurrency

All concurrent tasks belong to a `taskGroup` scope. There is no detached
spawn. `taskGroup` and `parallel` are in the prelude.

```osty
let result = taskGroup(|g| {
    let h1 = g.spawn(|| fetchA())
    let h2 = g.spawn(|| fetchB())
    let h3 = g.spawn(|| fetchC())
    Ok((h1.join()?, h2.join()?, h3.join()?))
})
```

`g.spawn(closure)` returns `Handle<T>`. A `Handle<T>` may not escape the
enclosing `taskGroup` block — returning it from the group body, storing
it in a field whose lifetime outlives the group, or leaking it through
a channel to code outside the group is a **compile error**. This
preserves the invariant that every task completes before its parent
returns.

### 8.2 Failure Semantics

**`taskGroup`** — if any child fails (returns `Err(e)`, panics via
`abort`, or completes through `unreachable`/`todo`), the group enters
cancellation: all remaining siblings receive a cancel signal (§8.4)
and the first observed error is propagated to the group's caller.

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

Cancellation in Osty is **structured and automatic**. It is the v0.3
resolution of gap G12.

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
`unreachable`, `todo`, or `os.exit` — these are immediate terminations.

#### 8.4.4 `collectAll` Under Cancel

`collectAll` keeps its children alive through sibling failures, but a
cancel signal from the **enclosing** `taskGroup` propagates into the
`collectAll` children normally — the collected list then contains
`Err(Cancelled { cause })` for any child that was mid-flight.

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

**Close semantics** (v0.3 resolution of gap G8).

- `ch.close()` signals that no further values will be sent.
- **Any task may close a channel.** A second `close` on an already-
  closed channel aborts. This is not idempotent by design — double
  close indicates a coordination bug.
- Sending on a closed channel aborts.
- `ch.recv()` returns buffered values until the buffer is empty **and**
  the channel is closed, at which point it returns `None`. `for x in ch`
  therefore terminates naturally when the channel is closed and
  drained.
- `ch.isClosed() -> Bool` reports close state without consuming a
  value.
- `ch.recv()` is a cancellation point per §8.4.2 — it returns `None`
  early when the surrounding task is cancelled (the caller distinguishes
  cancel from drain by checking `thread.isCancelled()`).

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

---
