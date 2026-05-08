### 10.15 Operating System (`std.os` / v0.6: `std.process`)

Paths, process control, and signal handling. File I/O is in `std.fs`;
environment variables and arguments are in `std.env`.

> **v0.6 migration**: in v0.6, the *effectful* surface of this module
> moves to a `Process` capability (§20.9.6). The legacy `os.exec(...)`
> / `os.exit(...)` / `os.pid()` / `os.hostname()` globals stay
> available under `--legacy-globals` (v0.6.x only) and are renamed to
> `std.process` for canonical use. v0.7 removes the `os.*` globals;
> `osty audit --legacy-globals` enumerates remaining sites. See
> §10.46 for the full migration table.

```osty
use std.os

let joined = os.path.join(["data", "users", "alice.json"])
let ext = os.path.extension("report.pdf")            // "pdf"
let parent = os.path.dirname("/a/b/c.txt")           // "/a/b"

let result = os.exec("git", ["status"])?
println(result.stdout)

os.exit(0)
```

API:

```
os.path.join(parts: List<String>) -> String
os.path.split(path: String) -> (String, String)      // (dirname, basename)
os.path.extension(path: String) -> String
os.path.dirname(path: String) -> String
os.path.basename(path: String) -> String
os.path.absolute(path: String) -> Result<String, Error>
os.path.canonical(path: String) -> Result<String, Error>
os.path.isAbsolute(path: String) -> Bool
os.path.separator() -> String                         // "/" or "\\"

os.exec(cmd: String, args: List<String>) -> Result<Output, Error>
os.execShell(command: String) -> Result<Output, Error>

pub struct Output {
    pub exitCode: Int,
    pub stdout: String,
    pub stderr: String,
}

os.exit(code: Int) -> Never
os.pid() -> Int
os.hostname() -> String

os.onSignal(sig: Signal, handler: fn())
pub enum Signal { Interrupt, Terminate, Hangup }
```
