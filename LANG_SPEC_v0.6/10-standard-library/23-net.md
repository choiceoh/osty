### 10.23 Network (`std.net`)

Low-level TCP and UDP networking. Higher-level HTTP is in `std.http`
(§10.24). All blocking operations are cancellation-aware (§8.4.2).

> **v0.6 migration**: the v0.5 globals `net.dial(host, port)` /
> `net.listen(port)` move to `Net` capability methods (§20.9.5). The
> canonical host adapter is `capability.hostNet`; the bridge factory
> `net.host` is a transitional alias scheduled for v0.7 removal. For
> URL-accepting sinks, Phase 5 adds `#[requires("url_safe")]` —
> sanitize via `std.url.encode` or `Url.parse(...)?` first. See §10.46
> for the full migration table.

```osty
use std.net
use std.io

// `Net` is the v0.6 capability (§20.9.5). All connect / listen entry
// points hang off the parameter, so dependency tracking is explicit.
fn fetchHomepage(net: Net) -> Result<Bytes, Error> {
    let conn = net.connect("example.com:443")?
    defer conn.close()
    io.writeAll(conn, b"GET / HTTP/1.0\r\n\r\n")?
    io.readAll(conn)
}

fn serveLoop(net: Net) -> Result<(), Error> {
    let listener = net.listen("0.0.0.0:8080")?
    defer listener.close()

    taskGroup(|g| {
        for {
            let conn = listener.accept()?
            g.spawn(|| {
                defer conn.close()
                handleConn(conn)
            })
        }
    })
}

fn pongOnce(net: Net) -> Result<(), Error> {
    let sock = net.udpBind("0.0.0.0:9000")?
    defer sock.close()
    let (_data, from) = sock.recvFrom(4096)?
    sock.sendTo(b"pong", from)?
    Ok(())
}

#[ambient(net)]
fn main() {
    let _ = fetchHomepage(net)?
}

// Address utilities are pure — `net.resolve` is the lone exception
// since DNS is an effect.
fn addrInfo(net: Net) -> Result<Addr, Error> {
    let addr = net.resolve("localhost:80")?       // Result<Addr, Error>
    println("{addr.host}:{addr.port}")            // requires console capability
    Ok(addr)
}
```

API:

The methods below hang off the `Net` capability (§20.9.5). Listings
written `Net.method(self, ...)` are the canonical v0.6 form;
`net.method(...)` legacy aliases desugar to the same call when
`--legacy-globals` is active and are rejected otherwise (`E0780`).

```
// TCP
Net.connect(self, addr: String) -> Result<TcpConn, Error>
Net.connectTimeout(self, addr: String, timeout: Duration) -> Result<TcpConn, Error>
Net.listen(self, addr: String) -> Result<TcpListener, Error>

pub struct TcpConn {
    // implements Reader, Writer, Closer (§16)
    fn read(self, maxBytes: Int) -> Result<Bytes, Error>
    fn write(self, data: Bytes) -> Result<Int, Error>
    fn flush(self) -> Result<(), Error>
    fn close(self) -> Result<(), Error>

    fn localAddr(self) -> Addr
    fn remoteAddr(self) -> Addr
    fn setReadTimeout(self, d: Duration) -> Result<(), Error>
    fn setWriteTimeout(self, d: Duration) -> Result<(), Error>
}

pub struct TcpListener {
    // implements Closer (§16)
    fn accept(self) -> Result<TcpConn, Error>
    fn close(self) -> Result<(), Error>
    fn localAddr(self) -> Addr
}

// UDP
Net.udpBind(self, addr: String) -> Result<UdpSocket, Error>
Net.udpConnect(self, addr: String) -> Result<UdpSocket, Error>  // connected mode

pub struct UdpSocket {
    // implements Closer (§16)
    fn send(self, data: Bytes) -> Result<Int, Error>        // connected mode
    fn recv(self, maxBytes: Int) -> Result<Bytes, Error>    // connected mode
    fn sendTo(self, data: Bytes, addr: Addr) -> Result<Int, Error>
    fn recvFrom(self, maxBytes: Int) -> Result<(Bytes, Addr), Error>
    fn close(self) -> Result<(), Error>
    fn localAddr(self) -> Addr
}

// Address resolution (DNS — also a network effect, hence on the capability)
Net.resolve(self, addr: String) -> Result<Addr, Error>
Net.resolveAll(self, host: String) -> Result<List<Addr>, Error>

pub struct Addr {
    pub host: String,
    pub port: Int,

    fn toString(self) -> String    // "host:port"
}
```

**Cancellation.** All blocking operations (`connect`, `listen`, `accept`,
`read`, `write`, `recv`, `recvFrom`) return `Err(Cancelled { cause })`
when the surrounding task-group's cancel signal fires (§8.4). Setting a
timeout with `setReadTimeout` / `setWriteTimeout` returns
`Err(TimedOut { … })` on expiry; the connection remains open.

**TLS.** Encrypted transports (TLS/DTLS) are not part of `std.net` and
are excluded from the standard library (§10.3). Use Go FFI with
`crypto/tls` or a community package.

**`TcpConn` as `Reader`/`Writer`.** `TcpConn` satisfies both `Reader`
and `Writer` (§16), so `io.copy`, `io.readAll`, and `io.writeAll` work
directly on connections.

#### 10.23.1 Net capability and information flow

Bytes received from a TCP/UDP connection carry the
`#[taint("net_input")]` source tag (§21.18.1). A handler that
parses the bytes into structured data preserves the tag through
the parser:

```osty
fn handleConn(conn: TcpConn) -> Result<(), Error> {
    let raw = io.readAll(conn)?       // raw: #[taint("net_input")] Bytes
    let req = parseHttpRequest(raw)?  // req: #[taint("net_input")] HttpRequest
    let body = req.body                // body: #[taint("net_input")] Bytes
    ...
}
```

Sanitization happens at sink boundaries, not at the receive
boundary — the design intent is that *every* byte from the
network is treated as adversarial input until validated.

#### 10.23.2 DNS as a flow source

`net.resolve(addr)` is a flow source — the returned `Addr` is
data the remote DNS server (or local cache) returned, which
should be treated as untrusted. The v0.6 baseline marks DNS
output `#[taint("net_input")]`.

For SSRF prevention, parse the resolved address through
`std.security.checkUrl` or apply the address-block validation
(§10.34 `std.security`) before opening a connection to it.

#### 10.23.3 Read/write timeout discipline

`TcpConn.setReadTimeout(d)` and `TcpConn.setWriteTimeout(d)` set
operation deadlines. A timeout-expired call returns
`Err(TimedOut { ... })` with the original duration in the cause.
Subsequent calls on the same connection may proceed normally.

Timeouts compose with `taskGroup` cancellation:

```osty
fn fetchOrCancel(net: Net, url: String) -> Result<Bytes, Error> {
    taskGroup(|g| {
        let conn = net.connect(url)?
        defer conn.close()
        conn.setReadTimeout(5.s)?       // 5s per-read deadline
        let body = io.readAll(conn)?
        // — read returns Err(TimedOut) after 5s, OR
        // — read returns Err(Cancelled) on group cancel,
        //   whichever fires first
        Ok(body)
    })
}
```

The discipline: timeouts bound *individual* operations; the
surrounding `taskGroup` provides the *whole-task* deadline via
explicit cancel.

#### 10.23.4 UDP packet boundaries

UDP datagrams are delivered as units — `recv(maxBytes)` returns a
single datagram up to `maxBytes` (or the full datagram if smaller).
Truncation can occur if the actual datagram is larger than
`maxBytes`; the truncation is silent (no error). Authors who need
to detect truncation should size buffers conservatively.

`recvFrom` additionally returns the sender's address. Both `recv`
and `recvFrom` are cancellation points.

#### 10.23.5 Concurrent connection patterns

A `TcpListener` may accept multiple connections in parallel via
`taskGroup`:

```osty
fn serveAll(net: Net, addr: String) -> Result<(), Error> {
    let listener = net.listen(addr)?
    defer listener.close()

    taskGroup(|g| {
        for {
            let conn = listener.accept()?
            g.spawn(|| {
                defer conn.close()
                handleConn(conn)
            })
        }
    })
}
```

Each spawned handler is independent; the listener's `accept` is
cancellation-aware. When the surrounding task is cancelled,
`accept` returns `Err(Cancelled)` and the loop exits.

The non-escaping `Handle<T>` rule (§8.1) prevents leaking a
spawned handler outside its `taskGroup`; the listener itself can
outlive a single connection.
