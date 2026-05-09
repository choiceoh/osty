### 10.30 SMTP (`std.smtp`)

- **Scope**: Osty stdlib spec — 10.30 SMTP (`std.smtp`)
- **Type**: Standard library specification
`std.smtp` builds SMTP protocol commands and parses server replies. It uses
`std.email.Envelope` for message data, but does not claim TLS/socket execution
until the runtime grows that transport layer.

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
