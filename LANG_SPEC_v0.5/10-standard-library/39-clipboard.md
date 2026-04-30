### 10.39 Clipboard (`std.clipboard`)

`std.clipboard` provides small-tool clipboard text I/O. It is intentionally
text-only; binary clipboard formats, MIME negotiation, and platform-native
pasteboard metadata remain host/runtime concerns.

```osty
use std.clipboard

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
