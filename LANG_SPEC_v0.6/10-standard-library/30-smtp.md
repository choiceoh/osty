### 10.30 SMTP (`std.smtp`)

`std.smtp` builds SMTP protocol commands and parses server replies. It uses
`std.email.Envelope` for message data, but does not claim TLS/socket execution
until the runtime grows that transport layer.

Like `std.db` (§10.29), every function in `std.smtp` is *pure* — protocol
command rendering, reply parsing, capability-list extraction, and credential
encoding all operate on values. Actual transport (TCP connect, STARTTLS
handshake, command write/read) flows through the `Net` capability
(§20.9.5). A wrapper layer drives the protocol command list against a
`TcpConn`:

```osty
fn sendOnce(net: Net, cfg: ClientConfig, env: Envelope) -> Result<(), Error> {
    let tx = smtp.transaction(cfg, env)
    let cmds = smtp.commands(tx)?

    let conn = net.connect("{cfg.host}:{cfg.port}")?
    defer conn.close()

    for cmd in cmds {
        io.writeString(conn, cmd + "\r\n")?
        let line = io.readLine(conn)?
        let reply = smtp.parseReply(line)?
        if reply.isError() { return Err(reply.toError()) }
    }
    Ok(())
}
```

The protocol module never grows a transport surface of its own — the
`std.smtp` ↔ `Net` split is the canonical layering for v0.6 protocol
modules that target wire formats.

```osty
use std.smtp

let cfg = smtp.config("smtp.example.com", smtp.defaultPort(StartTls), "client.local")?
let authed = smtp.withAuth(cfg, smtp.plainAuth("user", "secret"))
let tx = smtp.transaction(authed, envelope)
let commands = smtp.commands(tx)?
```

Core API:

```osty
smtp.config(host, port, hostname) -> Result<ClientConfig, Error>
smtp.withSecurity(config, security) -> ClientConfig
smtp.withAuth(config, auth) -> ClientConfig
smtp.commands(transaction) -> Result<List<String>, Error>
smtp.renderCommands(commands) -> String

smtp.authPlain(username, password) -> String
smtp.authLogin(username, password) -> List<String>
smtp.parseReply(line) -> Result<Reply, Error>
smtp.parseReplies(text) -> Result<List<Reply>, Error>
smtp.capabilities(replies) -> List<Capability>
```

Behavior:

- AUTH PLAIN and AUTH LOGIN payloads are base64 encoded.
- Multi-line SMTP replies are represented as individual `Reply` values with
  `continuation = true` for dashed reply lines.
- STARTTLS is represented in the command plan; TLS upgrade is runtime-driver
  work.
