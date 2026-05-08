### 10.19 Compression (`std.compress`)

- **Scope**: Osty stdlib spec — 10.19 Compression (`std.compress`)
- **Type**: Standard library specification
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
