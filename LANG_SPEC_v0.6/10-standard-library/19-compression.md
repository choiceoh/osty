### 10.19 Compression (`std.compress`)

gzip compression. Other formats (zstd, brotli, lz4) are available as
community packages.

```osty
use std.compress

let compressed = compress.gzip.encode(bytes)
let decompressed = compress.gzip.decode(compressed)?

// Streaming
let reader = compress.gzip.reader(sourceReader)
let writer = compress.gzip.writer(destWriter)
```

API:

```
compress.gzip.encode(data: Bytes) -> Bytes
compress.gzip.decode(data: Bytes) -> Result<Bytes, Error>

compress.gzip.reader(source: Reader) -> Reader
compress.gzip.writer(dest: Writer) -> Writer
```

#### v0.6 reproducibility and flow

`std.compress` is *pure* — every function transforms `Bytes` to
`Bytes` (or wraps a `Reader`/`Writer` pipeline) without consulting
any capability. Compression / decompression are acceptable inside
`#[reproducible(scope = "portable")]`.

Flow tags ride through compression — `compress.gzip.encode` of a
tainted `Bytes` produces a tainted `Bytes` with the same source
tag set. The compressed bytes are still tainted; decompressing
them produces the original tag set. This means compression is
*not* a sanitizer — moving data through gzip does not declassify
it from the type system's perspective.

Reader/Writer pipeline forms inherit the underlying stream's flow
tag set: `compress.gzip.reader(netConn)` produces a `Reader` whose
`.read()` calls return `Bytes` carrying `netConn`'s tags.
