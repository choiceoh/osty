### 10.8 JSON (`std.json`)

Reflection-based encode/decode for primitives, `struct`, and `enum`:

```osty
struct Config {
    host: String,
    port: Int,
    tags: List<String>,
}

let text = json.encode(cfg)
let cfg: Config = json.decode(text)?
```

Mapping:
- Field names map verbatim to JSON keys unless overridden by
  `#[json(key = "...")]` (§3.8.1).
- Enum variants encode as tagged objects:
  `{"tag": "Circle", "value": 1.5}`. The tag string is the variant
  name unless overridden by `#[json(key = "...")]` (§3.8.1). Two
  variants of the same enum with the same effective tag are a compile
  error.
- `T?` maps to nullable JSON (`None` → `null`). During decode, a
  missing key and an explicit `null` both decode to `None`. To
  distinguish them, use a wrapper type or a custom `Decode` impl.
- Collections map to arrays/objects.
- Non-representable types (e.g. function types, generic type parameters
  that do not implement `Encode`) are compile errors.

**Decode behavior on errors.**

- **Unknown keys are silently ignored.** A JSON object may contain keys
  not named by the target struct; the decoder skips them. This is the
  forward-compatible default (Go/Rust convention). The `#[json(strict)]`
  annotation is *not* part of the v0.4 set — use a custom `Decode`
  implementation if strict rejection is required.
- **Type mismatches are fatal.** When an incoming value's JSON type
  does not match the target (e.g. string where integer expected),
  `json.decode` returns `Err` immediately. There is no partial recovery
  and no field-level skipping.
- **Lone surrogates in strings** (e.g. `"\uD800"`) are a decode `Err`.
  Osty's `String` is strict UTF-8 (§2.1) and does not represent
  surrogate code points.

Custom serialization via `Encode`/`Decode` interfaces:

```osty
interface Encode {
    fn toJson(self) -> Json
}

interface Decode {
    fn fromJson(value: Json) -> Result<Self, Error>
}
```

`Json` is an enum of `Object`, `Array`, `String`, `Number`, `Bool`,
`Null`.
