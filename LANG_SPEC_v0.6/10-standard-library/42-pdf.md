### 10.42 PDF (`std.pdf`)

`std.pdf` provides dependency-free PDF inspection helpers. It does not render
pages or decode compressed streams; those require host/runtime-backed PDF
engines. The stdlib surface focuses on portable checks that are useful in CLI
tools, document search, ingestion pipelines, and diagnostics.

`std.pdf` is *pure* — every function operates on already-captured
`Bytes`. Filesystem reads happen at the caller's boundary through
`Fs` (§20.9.4), and the resulting `Bytes` are passed in. The pure
surface is acceptable inside `#[reproducible(scope = "target")]` —
the same input always produces the same parse tree.

Tainted PDF input (e.g. a user-uploaded file) carries the source's
flow tag through `pdf.extractText(data)` into the returned `String`.
A `String` extracted from a tainted PDF therefore lands at sinks
with the same `#[taint(...)]` set as the input bytes, and must be
sanitized before reaching `html_safe` / `sql_safe` / `shell_safe`
sinks.

```osty
use std.bytes
use std.pdf

let raw = bytes.fromString("%PDF-1.7\n1 0 obj\n<< /Type /Catalog >>\nendobj\n")
let doc = pdf.parse(raw)?
let ok = pdf.isPdf(raw)
```

Core API:

```osty
pdf.mime() -> String
pdf.isPdf(data) -> Bool
pdf.version(data) -> Result<Version, Error>
pdf.parse(data) -> Result<Document, Error>
pdf.metadata(data) -> Result<Info, Error>
pdf.pageCount(data) -> Result<Int, Error>
pdf.objects(data) -> Result<List<ObjectHeader>, Error>
pdf.extractText(data) -> Result<String, Error>
pdf.extractTextWithOptions(data, options) -> Result<String, Error>
pdf.hasText(data) -> Bool
```

Behavior:

- The PDF header may appear within the first 1024 bytes.
- `parse` reports version, page count, encryption marker, linearization marker,
  XRef stream marker, object count, and Info dictionary fields.
- `objects` scans classic `N N obj ... endobj` headers and classifies common
  object kinds such as catalog, pages, page, font, image, metadata, XRef, and
  content stream.
- `extractText` decodes literal and hex strings used by `Tj` / `TJ` text
  operators in uncompressed non-image streams. Streams with `/Filter` are
  skipped until a compression runtime is available.
