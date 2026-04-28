### 10.23 Network (`std.net`)

Low-level TCP and UDP networking. Higher-level HTTP is in `std.http`
(§10.2). All blocking operations are cancellation-aware (§8.4.2).

```osty
use std.net
use std.io

// TCP client
let conn = net.connect("example.com:443")?
defer conn.close()
io.writeAll(conn, b"GET / HTTP/1.0\r\n\r\n")?
let response = io.readAll(conn)?

// TCP server
let listener = net.listen("0.0.0.0:8080")?
defer listener.close()

for {
    let conn = listener.accept()?
    thread.spawn(|| {
        defer conn.close()
        handleConn(conn)
    })
}

// UDP
let sock = net.udpBind("0.0.0.0:9000")?
defer sock.close()
let (data, from) = sock.recvFrom(4096)?
sock.sendTo(b"pong", from)?

// Address utilities
let addr = net.resolve("localhost:80")?      // Result<Addr, Error>
addr.host        // "127.0.0.1"
addr.port        // 80
addr.toString()  // "127.0.0.1:80"
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
