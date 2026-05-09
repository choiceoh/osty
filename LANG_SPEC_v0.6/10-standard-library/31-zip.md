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

#### v0.6 reproducibility and flow

`std.zip` is *pure* — every function transforms `Bytes` /
`String` / `Entry` values without consulting any capability. The
module is acceptable inside `#[pure]`.

Flow tags ride through ZIP encoding/decoding. A `zip.encode` of
tainted entries produces tainted archive bytes; decoding tainted
bytes produces tainted payloads. ZIP is *not* a sanitizer —
moving data through ZIP does not change its trust set.

#### Path safety

Entry names that resolve outside the archive root (e.g.
`../../../etc/passwd`) are rejected at encode time (`E2104`). This
prevents *zip slip* — a class of vulnerability where extracting an
archive writes outside the intended directory. The check happens
at archive *construction*, not at decode, so a maliciously crafted
archive cannot bypass it via decode → re-encode.

When decoding *external* archives, `zip.list(archive)?` returns
the raw entry name list; callers must validate against their
target directory before writing extracted bytes via `Fs.write`.
The validation is a second-line defense; the primary `path_safe`
sink check on `Fs.write` (§21.18.3) catches missed validations.

#### CRC and integrity

`zip.crc32(data)` computes the CRC32 checksum used by ZIP's
integrity check. The function is deterministic — same input
produces same checksum across all targets. `zip.decode` validates
each entry's stored CRC against the recomputed CRC; mismatch is
`Err(...)`. This makes the decode path resilient to bit-flip
corruption but is *not* cryptographic integrity (CRC is not
collision-resistant under adversarial input).
