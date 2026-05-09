### 10.6 Collection Methods

All standard collections satisfy `Iterable<T>` (§15) and may be used
directly with `for x in xs`.

Naming convention:
- **Verb form** (`push`, `sort`) — mutates in place; requires `mut self`.
- **Past-participle / `-ed`** (`sorted`, `appended`) — returns new
  collection.

#### `List<T>`

```
len() -> Int
isEmpty() -> Bool
first() -> T?
last() -> T?
get(index: Int) -> T?

contains(item: T) -> Bool                    // T: Equal
indexOf(item: T) -> Int?                     // T: Equal
find(pred: fn(T) -> Bool) -> T?

map<R>(f: fn(T) -> R) -> List<R>
filter(pred: fn(T) -> Bool) -> List<T>
fold<A>(init: A, f: fn(A, T) -> A) -> A
reduce(f: fn(T, T) -> T) -> T?
scan<A>(init: A, f: fn(A, T) -> A) -> List<A>
flatMap<R>(f: fn(T) -> List<R>) -> List<R>
sorted() -> List<T>                          // T: Ordered
sortedBy(key: fn(T) -> K) -> List<T>         // K: Ordered
reversed() -> List<T>
take(n: Int) -> List<T>
drop(n: Int) -> List<T>
appended(item: T) -> List<T>
concat(other: List<T>) -> List<T>
zip<U>(other: List<U>) -> List<(T, U)>
zip3<U, V>(other1: List<U>, other2: List<V>) -> List<(T, U, V)>
enumerate() -> List<(Int, T)>
groupBy<K>(key: fn(T) -> K) -> Map<K, List<T>>  // K: Hashable
chunked(size: Int) -> List<List<T>>          // aborts when size <= 0
windowed(size: Int, step: Int) -> List<List<T>> // aborts when size/step <= 0
partition(pred: fn(T) -> Bool) -> (List<T>, List<T>)

push(item: T)
pop() -> T?
insert(index: Int, item: T)
removeAt(index: Int) -> T
sort()
reverse()
clear()
```

#### `Map<K, V>`

```
len() -> Int
isEmpty() -> Bool
get(key: K) -> V?
getOr(key: K, default: V) -> V
getOrInsert(key: K, default: V) -> V             // eager default; inserts on miss
getOrInsertWith(key: K, make: fn() -> V) -> V    // lazy supplier; inserts on miss
containsKey(key: K) -> Bool
keys() -> List<K>
values() -> List<V>
entries() -> List<(K, V)>

forEach(f: fn(K, V))
any(pred: fn(K, V) -> Bool) -> Bool
all(pred: fn(K, V) -> Bool) -> Bool
count(pred: fn(K, V) -> Bool) -> Int
find(pred: fn(K, V) -> Bool) -> (K, V)?

filter(pred: fn(K, V) -> Bool) -> Map<K, V>
mapValues<R>(f: fn(V) -> R) -> Map<K, R>
merge(other: Map<K, V>) -> Map<K, V>             // other wins on conflict
mergeWith(other: Map<K, V>, combine: fn(V, V) -> V) -> Map<K, V>

insert(key: K, value: V)
remove(key: K) -> V?
clear()
update(key: K, f: fn(V?) -> V)                   // upsert with function
insertAll(other: Map<K, V>)                      // bulk overwrite
retainIf(pred: fn(K, V) -> Bool)                 // drop entries where pred is false
```

#### `Set<T>`

```
len() -> Int
isEmpty() -> Bool
contains(item: T) -> Bool
union(other: Set<T>) -> Set<T>
intersect(other: Set<T>) -> Set<T>
difference(other: Set<T>) -> Set<T>

insert(item: T)
remove(item: T) -> Bool
clear()
```

#### v0.6 reproducibility note

Collection methods that **return a new collection** preserve the
ordering of their inputs — a `List<T>.sortBy(...)` is deterministic,
and `List<T>.filter(...)` preserves the original order. Methods on
`Map<K, V>` / `Set<T>` that *iterate* are *not* deterministic in
iteration order:

| Method | Deterministic order? |
|---|---|
| `Map.iter()` / `Map.keys()` / `Map.values()` | No (hash-based, may vary across runs) |
| `Map.entriesSorted()` / `Map.entriesSortedBy(f)` | Yes |
| `Set.iter()` | No |
| `Set.toListSorted()` / `Set.toListSortedBy(f)` | Yes |
| `List.iter()` / `for x in list` | Yes (insertion order) |

A function annotated `#[pure]` (§3.11)
that iterates a `Map` or `Set` must use the `*Sorted` variant —
calling `Map.iter()` from a reproducible context is `E0786`.

#### Information flow propagation

Collection methods preserve flow tags element-wise:

```osty
let names: List<#[taint("user_input")] String> = [...]
let upper = names.map(|n| n.toUpperCase())   // List<#[taint("user_input")] String>
```

`map`, `filter`, `flatMap`, `take`, `drop`, `chunked`, etc. all
preserve the tag set of their elements. Aggregation methods that
collapse multiple elements (`reduce`, `fold`) union the tag sets:

```osty
let pairs: List<#[taint("user_input")] String> = [...]
let joined: #[taint("user_input")] String = pairs.reduce(|a, b| a + b)
```

The tag set on the result is the union of all element tag sets that
flowed into the reduction.
