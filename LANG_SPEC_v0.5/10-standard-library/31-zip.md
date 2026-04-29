### 10.31 ZIP (`std.zip`)

`std.zip` creates and reads ZIP archives using the standard "store" method.
This needs no deflate runtime and produces interoperable ZIP files with local
headers, central directory records, EOCD, and CRC32 checks.

```osty
use std.bytes
use std.zip

let entry = zip.file("hello.txt", bytes.fromString("hello"))?
let archive = zip.encode([entry])?
let names = zip.list(archive)?
let payload = zip.extract(archive, "hello.txt")?
```

Core API:

```osty
zip.file(name, data) -> Result<Entry, Error>
zip.encode(entries) -> Result<Bytes, Error>
zip.decode(archive) -> Result<List<Entry>, Error>
zip.list(archive) -> Result<List<String>, Error>
zip.extract(archive, name) -> Result<Bytes?, Error>
zip.contains(archive, name) -> Bool
zip.isArchive(data) -> Bool
zip.crc32(data) -> Int
```

Behavior:

- Entry names must be relative paths and must not contain parent path
  segments.
- Stored entries are fully supported. Deflated entries are detected and return
  an error until `std.compress` grows a deflate runtime.
- Decode validates CRC32 and stored payload sizes.
