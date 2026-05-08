## 15. Iteration Protocol

Osty v0.6 defines iteration through two structural interfaces:
`Iterator<T>` and `Iterable<T>`. Any type implementing `Iterable<T>`
participates in `for x in xs { ... }` loops.

The protocol is intentionally minimal so it composes cleanly with the
v0.6 surfaces:

- A `for x in xs` loop carrying a capability (e.g. `Net`) keeps it
  available in the body — the loop introduces no new scope boundary
  that ambient binding (§20.3) would have to cross.
- An iterator over `#[taint("σ")]` values yields tagged elements;
  flow tracking (§21) propagates the tag into each loop iteration
  binding without any `for`-specific rule.
- Backend vectorization (§3.8.5) targets `List<T>` / range /
  `Map<K, V>` shapes today; arbitrary `Iterable` implementations fall
  back to method dispatch but participate in the protocol identically.

Lazy iterator combinators (filter, map, take, …) live in `std.iter`
(§10.7). User types implement `Iterable<T>` directly; the compiler
does not auto-derive iteration support.

`for x in xs { ... }` is defined in terms of two interfaces:

```osty
pub interface Iterator<T> {
    fn next(mut self) -> T?
}

pub interface Iterable<T> {
    fn iter(self) -> Iterator<T>
}
```

A `for x in <expr>` loop desugars to:

```osty
{
    let mut __it = (<expr>).iter()
    for let Some(x) = __it.next() {
        // body
    }
}
```

Equivalently, a type used as the right-hand side of `for ... in` must
satisfy `Iterable<T>` for some `T`. The element type `T` is inferred
from the implementing `iter` return type.

**Built-in iterables.** The following standard types implement
`Iterable<T>`:

| Type | Element type `T` |
|---|---|
| `List<T>` | `T` |
| `Set<T>` | `T` |
| `Map<K, V>` | `(K, V)` |
| `Range` (`a..b`, `a..=b`, optional `by step`) | `Int` |
| `Channel<T>` (§8.5) | `T` (loop ends when channel is closed and drained) |
| `Iter<T>` (§10.7) | `T` |
| `String.chars()` | `Char` |
| `String.graphemes()` | `String` |
| `String.bytes()` | `Byte` |
| `Bytes` | `Byte` |

**User-defined iterables.** Any type with an `iter(self) -> Iterator<T>`
method satisfies `Iterable<T>` automatically (structural typing,
§2.6). A custom `Iterator<T>` need only provide `next`:

```osty
pub struct Countdown {
    n: Int,

    pub fn next(mut self) -> Int? {
        if self.n <= 0 {
            None
        } else {
            self.n = self.n - 1
            Some(self.n + 1)
        }
    }
}

// Countdown already satisfies Iterator<Int>; wrap it in an Iterable.
pub struct CountdownFrom {
    start: Int,

    pub fn iter(self) -> Countdown {
        Countdown { n: self.start }
    }
}

for x in (CountdownFrom { start: 3 }) {
    println("{x}")     // 3, 2, 1
}
```

(The parentheses around the struct literal are required by §4.1.1.)

### 15.1 Iteration over capability-derived sources

A capability that yields a stream returns an `Iterable<T>` whose
*construction* required the capability but whose *iteration* does
not — once the iterator is built, the capability is no longer
consulted to advance it. This separates the question of who is
allowed to *open* the source from the question of how the body of
the loop processes its elements:

```osty
fn auditLines(fs: Fs, path: String, console: Console) -> Result<(), Error> {
    let lines = fs.lines(path)?           // construction needs `fs`
    for line in lines {                   // iteration does not
        console.println(line)
    }
    Ok(())
}
```

The reverse — *consuming an iterator* that was built by someone else
— is therefore safe in a more restricted scope. A pure helper can
take an `Iterable<T>` of already-collected lines and process them
without needing `Fs`:

```osty
#[reproducible(scope = "target")]
fn classify(lines: Iterable<String>) -> Map<String, Int> {
    let mut counts: Map<String, Int> = {:}
    for line in lines {
        let cat = categoryOf(line)
        counts.update(cat, |n| (n ?? 0) + 1)
    }
    counts
}
```

### 15.2 Iteration over channels

`Channel<T>` (§8.5) implements `Iterable<T>`. Iteration ends
naturally when the channel is closed and drained. Inside a
`taskGroup`, the iterating task is automatically cancelled along
with its siblings if the group enters cancellation:

```osty
fn pipeline(net: Net, jobs: List<Job>) -> Result<(), Error> {
    let ch = thread.chan::<Job>(64)

    taskGroup(|g| {
        // Producer
        g.spawn(|| {
            for j in jobs { ch <- j }
            ch.close()
        })

        // Consumer (main task)
        for j in ch {
            net.dispatch(j)?     // returns Err(Cancelled) on cancel
        }
        Ok(())
    })
}
```

Reading `ch.recv()` directly (not via `for ... in`) returns `None`
both on natural close and on cancel; check `thread.isCancelled()` to
distinguish the two cases (§8.5).

### 15.3 Iteration and information flow

A `for x in xs` loop binds `x` with the element type's flow tag set
preserved. There is no implicit declassification at the loop
boundary:

```osty
let lines: Iterable<#[taint("user_input")] String> = ...
for line in lines {
    // `line` is #[taint("user_input")] String — must sanitize before
    // it reaches a sink such as db.query.
    db.query("SELECT * FROM logs WHERE msg = ?", [line])
}
```

The parameterized form (`db.query("...", [line])`) is the safe path
— the sink does not concatenate `line` into the SQL text, so the
flow tag never reaches a string-shaped sink and no sanitizer is
required. Direct concatenation into the SQL string would be flagged
by the §21 checker.

### 15.4 Iteration and `#[reproducible]`

A `for x in xs` loop inside a `#[reproducible(scope = X)]` function
must iterate a *deterministically-ordered* iterable:

- `List<T>` — insertion-order, deterministic. ✓
- `Range` — increasing or `by`-stepped, deterministic. ✓
- `Map.entriesSorted()` — explicit sort, deterministic. ✓
- `Map.iter()` / `Map.keys()` / `Map.values()` — hash-based, NOT
  deterministic. ✗ (E0786)
- `Set.toListSorted()` — explicit sort, deterministic. ✓
- `Set.iter()` — hash-based, NOT deterministic. ✗ (E0786)
- `Channel<T>` — runtime-dependent ordering, NOT deterministic. ✗

The checker walks every `for ... in` and ensures the iterable side
satisfies the determinism constraint. Mixing a `Map.iter()` with a
`#[reproducible]` annotation is therefore a compile error caught
before the body is even type-checked.

---
