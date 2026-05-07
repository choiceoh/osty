## 16. I/O Protocol

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

---
