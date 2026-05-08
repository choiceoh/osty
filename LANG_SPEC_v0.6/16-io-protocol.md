## 16. I/O Protocol

Osty v0.6 defines stream I/O through three structural interfaces:
`Reader`, `Writer`, and `Closer`. The `EOF` sentinel is `Ok(0)` —
there is no distinguished error type for end-of-stream. The contract
is shared across `std.io`, stream-oriented standard-library modules,
and FFI byte-stream bridges.

The v0.6 capability surface (§20) is the *only* path that produces an
I/O handle: filesystem reads/writes flow through `Fs.open(self, path)`
or `Fs.readToString(self, path)` (§10.15), network reads/writes
through `Net.connect(self, addr)` or `Net.listen(self, addr)`
(§10.23). The handles those methods return implement
`Reader` / `Writer` / `Closer` exactly as defined here, so generic
helpers like `io.copy(dst, src)` accept any capability-derived
stream without a special case.

Information flow (§21) interacts with the protocol at two points:

- **Path-shaped sinks** on `Fs` (`open` / `read` / `write` / `remove`)
  carry `#[requires("path_safe")]`. Bytes that flow into them must
  have been sanitized (§21.8) — typically via `Path.parse(...)?` or
  `std.path.normalize`.
- **URL-shaped sinks** on `Net` (`connect` / `dial` / `httpClient`)
  similarly require `url_safe`. Phase 5 enforces this; v0.6 baseline
  surfaces the requirement in `osty audit`.

The interfaces themselves are pure protocol definitions and carry no
capability or flow tag — they describe what a stream *is*, not who
holds the right to open one.

The `Reader` and `Writer` interfaces define the streaming I/O contract
shared across `std.io`, stream-oriented standard-library modules, and
FFI byte-stream bridges. `std.fs` currently exposes whole-file and
path-mutation helpers; future handle-based filesystem APIs plug into
this same protocol surface.

```osty
pub interface Reader {
    /// Reads up to `maxBytes` from the stream and returns the bytes
    /// that were produced. An empty result indicates end of stream.
    /// Implementations may return fewer bytes than requested even when
    /// more data remains.
    fn read(self, maxBytes: Int) -> Result<Bytes, Error>
}

pub interface Writer {
    /// Writes `data` to the stream. Returns the number of bytes
    /// written, which is in the range [0, data.len()]. Short writes
    /// are permitted; callers wanting full-buffer semantics should
    /// loop or use the `writeAll` helper from `std.io`.
    fn write(self, data: Bytes) -> Result<Int, Error>

    /// Flushes any buffered output to the underlying sink. A no-op for
    /// unbuffered writers.
    fn flush(self) -> Result<(), Error>
}

pub interface Closer {
    /// Releases the resource. Subsequent operations return
    /// Err(Closed). Idempotent — closing twice returns Ok(()).
    fn close(self) -> Result<(), Error>
}

pub interface ReadWriter {
    Reader
    Writer
}

pub interface ReadCloser {
    Reader
    Closer
}

pub interface WriteCloser {
    Writer
    Closer
}

pub interface ByteReader {
    Reader
    fn peek(self, maxBytes: Int) -> Result<Bytes, Error>
    fn readByte(self) -> Result<Byte?, Error>
    fn unreadByte(self) -> Result<(), Error>
}

pub interface LineReader {
    Reader
    fn readLineBytes(self) -> Result<Bytes?, Error>
    fn readLine(self) -> Result<String?, Error>
}

pub interface ByteWriter {
    Writer
    fn writeByte(self, b: Byte) -> Result<Int, Error>
    fn writeString(self, s: String) -> Result<Int, Error>
    fn writeLine(self, s: String) -> Result<Int, Error>
}

pub interface ReaderFrom {
    fn readFrom(self, r: Reader) -> Result<Int, Error>
}

pub interface WriterTo {
    fn writeTo(self, w: Writer) -> Result<Int, Error>
}

pub interface BufferedReader {
    ByteReader
    LineReader
}

pub interface BufferedWriter {
    ByteWriter
    ReaderFrom
    WriterTo
}
```

**EOF.** A `Reader` signals end-of-stream with `Ok(b"")`. There is no
distinguished `EOF` error.

**Cancellation.** Standard library `Reader`/`Writer` implementations
that perform blocking I/O check the task-group cancellation token
(§8.4) and return `Err(Cancelled)` when the surrounding `taskGroup`
is being torn down.

**Helpers.** `std.io` provides:

```
io.copy(dst: Writer, src: Reader) -> Result<Int, Error>
io.copyN(dst: Writer, src: Reader, n: Int) -> Result<Int, Error>
io.readAll(r: Reader) -> Result<Bytes, Error>
io.readExact(r: Reader, n: Int) -> Result<Bytes, Error>
io.readString(r: Reader) -> Result<String, Error>
io.readLines(r: Reader) -> Result<List<String>, Error>
io.readAllLines(r: LineReader) -> Result<List<String>, Error>
io.discard(r: Reader) -> Result<Int, Error>
io.writeAll(w: Writer, data: Bytes) -> Result<(), Error>
io.writeString(w: Writer, s: String) -> Result<(), Error>
io.writeLine(w: Writer, s: String) -> Result<(), Error>
io.writeLines(w: ByteWriter, lines: List<String>) -> Result<Int, Error>
```

`io.readAll` accumulates a whole stream into a single `Bytes` value.
`io.readExact` and `io.copyN` require exactly `n` bytes and return
`Err` on early EOF. `io.readString` validates UTF-8 after `readAll`;
`io.readLines` splits that text into lines and normalizes trailing `\r`
from CRLF input, while `io.readAllLines` targets incremental
`LineReader` implementations directly. `io.discard` drains a reader and
reports how many bytes were skipped. `io.writeAll` retries short writes
until the full buffer is accepted, then flushes the writer.
`io.writeLines` targets the richer `ByteWriter` capability surface.
`io.copy` repeatedly reads chunks from `src`, writes them fully to
`dst`, flushes once at the end, and returns the total byte count copied.

**In-memory implementations.** `std.io` also ships small concrete types
for pure-Osty tests, adapters, and pipelines:

```
io.bytesReader(data: Bytes) -> io.BytesReader
io.stringReader(s: String) -> io.BytesReader
io.buffer() -> io.Buffer
```

`BytesReader` is a `Reader`/`Closer` over an in-memory `Bytes` payload
with cursor-style methods such as `remaining()`, `peek(n)`,
`readByte()`, `unreadByte()`, `skip(n)`, `readLineBytes()`,
`readLine()`, and `remainingBytes()`.
`Buffer` is an append-only in-memory writer that satisfies `Writer` and
adds inspection helpers such as `bytes()` and `toString()`, plus
buffer-management / piping helpers such as `clear()`, `truncate(n)`,
`reader()`, `readFrom(r)`, and `writeTo(w)`.

### 16.1 Composing capabilities and the I/O protocol

A typical effectful pipeline opens a stream from a capability,
operates on it through the protocol interfaces, and lets `defer`
close it on every exit path:

```osty
fn copyFile(fs: Fs, src: String, dst: String) -> Result<Int, Error> {
    let r = fs.open(src)?         // capability call → Reader+Closer
    defer r.close()

    let w = fs.create(dst)?       // capability call → Writer+Closer
    defer w.close()

    io.copy(w, r)                  // protocol-only — no capability
}
```

The middle layer (`io.copy`) operates on `Reader` / `Writer` only.
This is what lets in-memory adapters (`io.bytesReader` / `io.buffer`)
participate in the same pipelines as real files or sockets — the
test path constructs a pure adapter, and `io.copy` cannot tell the
difference.

### 16.2 In-memory adapters in tests

A test that exercises an `io.copy`-shaped function does not need a
`Fs` capability — it constructs `BytesReader` and `Buffer` directly
and feeds them to the function:

```osty
#[test]
fn test_copy_from_bytes_reader() {
    let r = io.bytesReader(b"hello")
    let w = io.buffer()
    let n = io.copy(w, r)?
    testing.assertEq(n, 5)
    testing.assertEq(w.toString()?, "hello")
}
```

For tests that *do* exercise capability-typed code, the
`std.capability.testing.FakeFs` adapter (§11.9) returns the same
`Reader`/`Writer`/`Closer`-shaped handles as the host adapter, so
the production code under test runs unmodified.

### 16.3 Information flow on streams

A `Reader` produces `Bytes`. The bytes carry the flow tag set of the
underlying source: `fs.read(path)` is conventionally tagged
`#[taint("fs_input")]`, `net.read(conn, n)` is `#[taint("user_input")]`,
and `io.bytesReader(data)` inherits whatever tags `data` had. Tags
ride through `io.readAll`, `io.copy`, and `io.readLines` without
declassification — sanitization must be explicit at the sink, not
implicit at the protocol boundary.

```osty
fn safeRender(net: Net, conn: TcpConn) -> Result<String, Error> {
    let raw = io.readAll(conn)?    // Bytes #[taint("user_input")]
    let text = raw.toString()?     // String #[taint("user_input")]
    Ok(std.html.escape(text))      // sanitize → #[trust("html_safe")]
}
```

`std.html.escape` is registered as a sanitizer with
`#[sanitizes("user_input", into = "html_safe")]` (§21.6); after the
call, the result is acceptable to `http.respondHtml`.

### 16.4 Cancellation surfaces on streams

Every blocking method on a capability-derived stream
(`net.read`/`net.write`/`fs.read`/`fs.write`) returns `Err(Cancelled
{ cause })` when the surrounding `taskGroup` is cancelled (§8.4.2,
§7.6). In-memory adapters (`BytesReader`, `Buffer`) never block, so
they never return `Cancelled` — a test that wants to exercise
cancellation paths must use a fake capability (`FakeNet`) that
honors the cancel token, not the in-memory adapter.

---
