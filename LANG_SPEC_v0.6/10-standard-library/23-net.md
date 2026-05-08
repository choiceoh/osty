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

```
// TCP
net.connect(addr: String) -> Result<TcpConn, Error>
net.connectTimeout(addr: String, timeout: Duration) -> Result<TcpConn, Error>
net.listen(addr: String) -> Result<TcpListener, Error>

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
net.udpBind(addr: String) -> Result<UdpSocket, Error>
net.udpConnect(addr: String) -> Result<UdpSocket, Error>   // connected mode

pub struct UdpSocket {
    // implements Closer (§16)
    fn send(self, data: Bytes) -> Result<Int, Error>        // connected mode
    fn recv(self, maxBytes: Int) -> Result<Bytes, Error>    // connected mode
    fn sendTo(self, data: Bytes, addr: Addr) -> Result<Int, Error>
    fn recvFrom(self, maxBytes: Int) -> Result<(Bytes, Addr), Error>
    fn close(self) -> Result<(), Error>
    fn localAddr(self) -> Addr
}

// Address resolution
net.resolve(addr: String) -> Result<Addr, Error>
net.resolveAll(host: String) -> Result<List<Addr>, Error>

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
