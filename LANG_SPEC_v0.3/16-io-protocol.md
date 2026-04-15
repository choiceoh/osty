## 16. I/O Protocol

The `Reader` and `Writer` interfaces define the streaming I/O contract
shared across `std.io`, `std.fs`, `std.compress`, `std.http`, and the
FFI byte-stream bridges.

```osty
pub interface Reader {
    /// Reads up to `buf.len()` bytes into `buf`, starting at offset 0.
    /// Returns the number of bytes read. A return value of 0 indicates
    /// end of stream. Implementations may return fewer bytes than
    /// requested even when the stream is not exhausted.
    fn read(self, buf: mut Bytes) -> Result<Int, Error>
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
```

**EOF.** A `Reader` signals end-of-stream with `Ok(0)`. There is no
distinguished `EOF` error.

**Cancellation.** Standard library `Reader`/`Writer` implementations
that perform blocking I/O check the task-group cancellation token
(§8.4) and return `Err(Cancelled)` when the surrounding `taskGroup`
is being torn down.

**Helpers.** `std.io` provides:

```
io.copy(dst: Writer, src: Reader) -> Result<Int, Error>
io.readAll(r: Reader) -> Result<Bytes, Error>
io.writeAll(w: Writer, data: Bytes) -> Result<(), Error>
```

---
