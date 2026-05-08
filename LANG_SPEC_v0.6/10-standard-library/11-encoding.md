### 10.11 Encoding (`std.encoding`)

Binary-to-text encodings.

```osty
use std.encoding

let encoded = encoding.base64.encode(bytes)
let decoded = encoding.base64.decode(text)?

let hex = encoding.hex.encode(bytes)
let raw = encoding.hex.decode(text)?

let safe = encoding.url.encode("hello world&foo=bar")
let back = encoding.url.decode(safe)?
```

Submodules:

- `encoding.base64` — standard alphabet with padding. `base64.url`
  for URL-safe alphabet.
- `encoding.hex` — lowercase output.
- `encoding.url` — percent-encoding for URL components.

#### v0.6 reproducibility and flow

Every encoding function is *pure* — `Bytes` ↔ `String` round-trips
preserve bit patterns and consult no capability. The whole module
is acceptable inside `#[reproducible(scope = "portable")]` and
`#[pure]` contexts.

Flow tags ride through encoding identically — `base64.encode` of a
tainted `Bytes` produces a tainted `String` with the same source
tag set. `encoding.url.encode` is *not* the same as the URL-safety
sanitizer:

- `encoding.url.encode(s)` does percent-encoding for placement in a
  URL component. It is a pure transformation; the result inherits
  the input's flow tag set.
- `std.url.encode(s)` (as registered in §21.18.2) is a *sanitizer*
  that produces `url_safe`-tagged output. It is the same byte-level
  transformation but with the additional flow-tag promotion.

Authors who need the sanitizer effect should use `std.url.encode`,
not `std.encoding.url.encode`. The two share an implementation but
differ in their type-system attestation.

#### Round-trip contract

Each pair satisfies `decode(encode(x))? == Some(x)` for every
`Bytes` `x`. The reverse `encode(decode(s)?)? == Some(s)` is also
guaranteed for every well-formed encoded string `s`. Malformed
input to `decode` returns `Err(...)`; the round-trip assertion is
on well-formed input only.

This round-trip property is documented as a `spec { law: ... }`
clause on each encode function (§3.13.1). Phase 5 turns the laws
into property tests.
