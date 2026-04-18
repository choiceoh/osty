### 10.7 Lazy Iterators (`std.iter`)

For pipelines over large sequences, early termination, or infinite
sequences:

```osty
use std.iter

let result = iter.from(xs)
    .map(|x| x * 2)
    .filter(|x| x > 10)
    .take(5)
    .toList()
```

Adapters return new `Iter<T>` without evaluation. Terminal methods
drive evaluation. Infinite sources require `take`, `takeWhile`, etc.
before a terminal call.
