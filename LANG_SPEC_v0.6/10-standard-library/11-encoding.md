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
