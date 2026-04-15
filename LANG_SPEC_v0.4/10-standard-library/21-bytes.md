### 10.21 Bytes (`std.bytes`)

Byte-sequence manipulation, mirroring `std.strings` for `Bytes` values.
`Bytes` is an immutable byte sequence (§2.4.1); all operations return new
values and never mutate in place.

```osty
use std.bytes

let data: Bytes = b"Hello, World!"
let upper = bytes.toUpper(data)

if bytes.startsWith(data, b"Hello") {
    let rest = bytes.slice(data, 7, data.len())
}

let parts = bytes.split(data, b",")
let joined = bytes.join(parts, b" | ")

let idx = bytes.indexOf(data, b"World")   // Some(7)
let replaced = bytes.replace(data, b"World", b"Osty")
```

API:

```
bytes.len(b: Bytes) -> Int
bytes.isEmpty(b: Bytes) -> Bool
bytes.get(b: Bytes, i: Int) -> Byte?
bytes.slice(b: Bytes, start: Int, end: Int) -> Bytes

bytes.contains(b: Bytes, sub: Bytes) -> Bool
bytes.startsWith(b: Bytes, prefix: Bytes) -> Bool
bytes.endsWith(b: Bytes, suffix: Bytes) -> Bool
bytes.indexOf(b: Bytes, sub: Bytes) -> Int?
bytes.lastIndexOf(b: Bytes, sub: Bytes) -> Int?

bytes.split(b: Bytes, sep: Bytes) -> List<Bytes>
bytes.join(parts: List<Bytes>, sep: Bytes) -> Bytes
bytes.concat(a: Bytes, b: Bytes) -> Bytes
bytes.repeat(b: Bytes, n: Int) -> Bytes
bytes.replace(b: Bytes, old: Bytes, new: Bytes) -> Bytes
bytes.replaceAll(b: Bytes, old: Bytes, new: Bytes) -> Bytes

bytes.trimLeft(b: Bytes, strip: Bytes) -> Bytes
bytes.trimRight(b: Bytes, strip: Bytes) -> Bytes
bytes.trim(b: Bytes, strip: Bytes) -> Bytes

bytes.toUpper(b: Bytes) -> Bytes         // ASCII only
bytes.toLower(b: Bytes) -> Bytes         // ASCII only

bytes.fromString(s: String) -> Bytes
bytes.toString(b: Bytes) -> Result<String, Error>   // validates UTF-8
bytes.toHex(b: Bytes) -> String          // lowercase hex
bytes.fromHex(s: String) -> Result<Bytes, Error>
```

**Notes.**

- `slice(b, start, end)` uses half-open byte indices `[start, end)`.
  Out-of-range indices abort. Negative indices are not supported.
- `toUpper` / `toLower` operate on ASCII bytes only (0x41–0x5A,
  0x61–0x7A). Non-ASCII bytes are passed through unchanged.
- `toString` validates that the byte sequence is valid UTF-8 and
  returns `Err` otherwise; this is the safe reinterpretation path.
  For lossy conversion use `bytes.toHex` and display the hex form.
- `toHex` / `fromHex` are convenience wrappers over `std.encoding.hex`.
  Importing `std.encoding` directly is preferred when both encode and
  decode are needed in the same file.
