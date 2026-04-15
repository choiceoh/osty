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
sorted() -> List<T>                          // T: Ordered
sortedBy(key: fn(T) -> K) -> List<T>         // K: Ordered
reversed() -> List<T>
appended(item: T) -> List<T>
concat(other: List<T>) -> List<T>
zip<U>(other: List<U>) -> List<(T, U)>
enumerate() -> List<(Int, T)>

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
containsKey(key: K) -> Bool
keys() -> List<K>
values() -> List<V>
entries() -> List<(K, V)>

insert(key: K, value: V)
remove(key: K) -> V?
clear()
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
