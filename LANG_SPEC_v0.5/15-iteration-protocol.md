## 15. Iteration Protocol

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
| `Range` (`a..b`, `a..=b`) | `Int` |
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

---
