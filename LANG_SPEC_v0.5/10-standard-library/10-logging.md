### 10.10 Logging (`std.log`)

Structured logging with levels and pluggable handlers. Inspired by
Go's `slog`.

```osty
use std.log

log.info("user logged in", Fields { "userId": 42, "ip": "1.2.3.4" })
log.warn("rate limit approached", Fields { "remaining": 10 })
log.error("db connection failed", Fields { "error": err.message() })
```

API:

```
pub enum Level { Debug, Info, Warn, Error }

pub struct Fields { ... }            // String -> LogValue map

pub fn debug(msg: String, fields: Fields = Fields {:})
pub fn info(msg: String, fields: Fields = Fields {:})
pub fn warn(msg: String, fields: Fields = Fields {:})
pub fn error(msg: String, fields: Fields = Fields {:})

pub fn setLevel(level: Level)
pub fn setHandler(handler: Handler)

pub interface Handler {
    fn handle(self, record: Record)
}

pub struct TextHandler { ... }       // human-readable, default
pub struct JsonHandler { ... }       // structured JSON, one line per record
```

Defaults: output to stderr, `TextHandler`, `Info` level. Handler is
process-global; replace at startup.

**`LogValue`** is a concrete sum type (resolves G3):

```osty
pub enum LogValue {
    Null,
    Bool(Bool),
    Int(Int),
    Float(Float),
    String(String),
    Time(Instant),
    Duration(Duration),
    Bytes(Bytes),
    List(List<LogValue>),
    Map(Map<String, LogValue>),
}
```

The `Fields { "k": v }` map literal accepts heterogeneous values
because the compiler inserts an implicit `.toLogValue()` conversion at
the literal site for each value position. The `ToLogValue` trait is
internal:

```osty
interface ToLogValue {
    fn toLogValue(self) -> LogValue
}
```

It is auto-implemented for: every primitive (§2.6.5), `String`,
`Bytes`, `Instant`, `Duration`, `Option<T> where T: ToLogValue`,
`List<T> where T: ToLogValue`, and `Map<String, V> where V: ToLogValue`.
For struct/enum, an instance is auto-derived using the same scheme as
`#[json]` encoding (§10.8). User types may implement `ToLogValue`
explicitly.

`Fields {"k": v}` outside a `log.{debug,info,warn,error,...}` call is
just an ordinary `Map<String, LogValue>` literal — the implicit
conversion is what makes the heterogeneous value notation valid.
