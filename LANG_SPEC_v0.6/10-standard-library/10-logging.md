### 10.10 Logging (`std.log`)

Structured logging with levels and pluggable handlers. Inspired by
Go's `slog`.

> **v0.6 capability note**: log emission is a host effect (writes to
> the process's `stderr`). The runtime handler binds to the ambient
> `Console` capability (§20.9.7). Library code may call
> `log.info(...)` etc. as a convenience — the call is desugared to
> dispatch through the per-task `Console` binding. Pure functions that
> wish to remain free of capability dependence should return values
> instead of logging.

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

#### v0.6 logging and information flow

`LogValue` carries the flow tag set of the value it was constructed
from. When a tainted `String` becomes a `LogValue.String`, the tag
set rides through. Log handlers are sinks for two reasons:

1. **Stdout / stderr write** — handlers ultimately write to a
   stream owned by the ambient `Console` capability (§20.9.7). Log
   output is therefore visible to whoever can read those streams.
2. **External persistence** — handlers may forward records to log
   aggregators (Loki, Datadog, etc.) over the network. Personally
   identifiable information that flows into a log line may end up
   in places the original consent did not anticipate.

The v0.6 stdlib provides `std.redact` (§10.34) for redacting
sensitive values *before* they reach a log handler. Authors who
want stronger guarantees can register `log.warn` etc. as
`#[requires("log_safe")]` sinks via a custom `Handler`
implementation; the `log_safe` tag is then produced by `std.redact`
or the application's own sanitizer.

#### Log handler as capability

The default `Handler` is process-global. For deterministic tests
and for code that must declare its log dependencies explicitly, the
v0.6 path is to receive a `Logger` parameter (a thin wrapper over
`Handler`) instead of using the global `log.*` functions:

```osty
fn handleRequest(logger: Logger, req: Request) -> Response {
    logger.info("incoming", Fields { "path": req.path() })
    ...
}

#[ambient(console)]
fn main() {
    let logger = log.handler(console)
    handleRequest(logger, ...)
}
```

This pattern is *opt-in* — process-global handlers remain the easy
path for scripts and tools where capability discipline is not the
priority.

#### Performance hint for log calls

Levels suppressed by `setLevel` short-circuit before any work — the
call site pays only the level check. The `instructions` cost
includes format-string interpolation even at suppressed levels (the
args are evaluated). Use the pre-check pattern when interpolation
is expensive:

```osty
if log.isEnabled(log.Debug) {
    log.debug("expensive: {expensiveComputation()}")
}
```
