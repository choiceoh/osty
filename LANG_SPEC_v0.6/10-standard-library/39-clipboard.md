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
