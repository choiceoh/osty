### 10.7 Lazy Iterators (`std.iter`)

- **Scope**: Osty stdlib spec — 10.7 Lazy Iterators (`std.iter`)
- **Type**: Standard library specification
For pipeline-style transformations over sequences:

```osty
use std.iter

let result = iter.from(xs)
    .map(|x| x * 2)
    .filter(|x| x > 10)
    .take(5)
    .toList()
```

The current v0.4 stdlib implementation is eager over a backing
`List<T>`: adapters allocate a new `Iter<T>` immediately. The API is
kept fluent so user code can move to a lazy pull-based implementation
later without changing call sites.

API:

```
iter.from<T>(items: List<T>) -> Iter<T>
iter.empty<T>() -> Iter<T>
iter.range(start: Int, stop: Int) -> Iter<Int>

Iter.map<U>(f: fn(T) -> U) -> Iter<U>
Iter.filter(f: fn(T) -> Bool) -> Iter<T>
Iter.inspect(f: fn(T) -> ()) -> Iter<T>
Iter.filterMap<U>(f: fn(T) -> U?) -> Iter<U>
Iter.mapWhile<U>(f: fn(T) -> U?) -> Iter<U>
Iter.flatMap<U>(f: fn(T) -> Iter<U>) -> Iter<U>
Iter.take(n: Int) -> Iter<T>
Iter.takeLast(n: Int) -> Iter<T>
Iter.takeWhile(f: fn(T) -> Bool) -> Iter<T>
Iter.skip(n: Int) -> Iter<T>
Iter.skipLast(n: Int) -> Iter<T>
Iter.skipWhile(f: fn(T) -> Bool) -> Iter<T>
Iter.stepBy(step: Int) -> Iter<T>
Iter.chain(other: Iter<T>) -> Iter<T>
Iter.intersperse(separator: T) -> Iter<T>
Iter.reversed() -> Iter<T>
Iter.rev() -> Iter<T>
Iter.enumerate() -> Iter<(Int, T)>
Iter.zip<U>(other: Iter<U>) -> Iter<(T, U)>
Iter.zip3<U, V>(other1: Iter<U>, other2: Iter<V>) -> Iter<(T, U, V)>
Iter.chunked(size: Int) -> Iter<List<T>>
Iter.chunks(size: Int) -> Iter<List<T>>
Iter.windowed(size: Int, step: Int) -> Iter<List<T>>
Iter.windows(size: Int, step: Int) -> Iter<List<T>>

Iter.toList() -> List<T>
Iter.collect() -> List<T>
Iter.count() -> Int
Iter.countWhere(f: fn(T) -> Bool) -> Int
Iter.isEmpty() -> Bool
Iter.first() -> T?
Iter.last() -> T?
Iter.single() -> T?
Iter.nth(n: Int) -> T?
Iter.contains(item: T) -> Bool
Iter.find(f: fn(T) -> Bool) -> T?
Iter.lastWhere(f: fn(T) -> Bool) -> T?
Iter.findMap<U>(f: fn(T) -> U?) -> U?
Iter.indexWhere(f: fn(T) -> Bool) -> Int?
Iter.lastIndexWhere(f: fn(T) -> Bool) -> Int?
Iter.any(f: fn(T) -> Bool) -> Bool
Iter.all(f: fn(T) -> Bool) -> Bool
Iter.forEach(f: fn(T) -> ())
Iter.fold<U>(init: U, f: fn(U, T) -> U) -> U
Iter.reduce(f: fn(T, T) -> T) -> T?
Iter.scan<U>(init: U, f: fn(U, T) -> U) -> Iter<U>
Iter.partition(f: fn(T) -> Bool) -> (List<T>, List<T>)
```
