### 10.39 Clipboard (`std.clipboard`)

`std.clipboard` provides small-tool clipboard text I/O. It is intentionally
text-only; binary clipboard formats, MIME negotiation, and platform-native
pasteboard metadata remain host/runtime concerns.

> **v0.6 capability note**: clipboard read/write is a host effect — the
> implementation spawns `pbcopy` / `pbpaste` / Wayland tools. The
> module-level functions stay as a convenience surface for scripts and
> small CLIs, but library code should funnel access through a `Process`
> capability (§20.9.6) explicitly, since clipboard contents may carry
> taint into shell-arg sinks (§21.8). A capability-shaped wrapper is
> tracked as Phase 5 follow-up.

```osty
use std.clipboard

// Script-level (ambient `process` binding):
clipboard.writeText("ready")?
let current = clipboard.readText()?
```

API:

```osty
clipboard.readText() -> Result<String, Error>
clipboard.writeText(text: String) -> Result<(), Error>
clipboard.read() -> Result<String, Error>
clipboard.write(text: String) -> Result<(), Error>
clipboard.clear() -> Result<(), Error>
```

`read` and `write` are short aliases for `readText` and `writeText`.
Implementations may delegate to host clipboard commands such as `pbpaste`,
`pbcopy`, Wayland/X11 clipboard tools, AppleScript, or PowerShell. Failure to
find or run a supported host command returns `Err`.

#### 10.39.1 Clipboard input flow tag

`clipboard.readText()` returns `#[taint("user_input")] String` —
clipboard contents are user-controlled, often from arbitrary
external sources (web pages, other applications). Treat it as
adversarial input by default:

```osty
fn pasteAndQuery(db: Db) -> Result<List<Row>, Error> {
    let raw = clipboard.readText()?              // tainted
    let safe = std.sql.escape(raw)               // sanitize → sql_safe
    db.query("SELECT * FROM logs WHERE msg = '{safe}'")
}
```

Clipboard data flowing into `Process.execShell`, `db.query`, or
`http.respondHtml` without sanitization is a §21 sink violation.

#### 10.39.2 Clipboard testing

There is no `FakeClipboard` in v0.6 baseline — tests that use
clipboard typically mock the underlying `Process` capability that
spawns `pbcopy` / `pbpaste` and assert the spawned commands and
inputs. A future `std.capability.testing.FakeClipboard` is
planned for Phase 5.

For tests that need clipboard behavior today, the recommended
pattern is a thin `Clipboard` interface in user code:

```osty
pub interface Clipboard {
    fn readText(self) -> Result<String, Error>
    fn writeText(self, s: String) -> Result<(), Error>
}

// Production
struct HostClipboard {}
impl HostClipboard {
    fn readText(self) -> Result<String, Error> { clipboard.readText() }
    fn writeText(self, s: String) -> Result<(), Error> { clipboard.writeText(s) }
}

// Test
struct FakeClipboard { content: String }
impl FakeClipboard {
    fn readText(self) -> Result<String, Error> { Ok(self.content) }
    fn writeText(self, s: String) -> Result<(), Error> { ... }
}
```

Library code takes the interface; tests inject the fake.
