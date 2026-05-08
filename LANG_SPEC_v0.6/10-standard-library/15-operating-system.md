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
use std.process

// Path helpers are pure — no capability required.
let joined = os.path.join(["data", "users", "alice.json"])
let ext = os.path.extension("report.pdf")            // "pdf"
let parent = os.path.dirname("/a/b/c.txt")           // "/a/b"

// Process effects flow through a `Process` capability (§20.9.6).
fn runStatus(proc: Process, console: Console) -> Result<(), Error> {
    let result = proc.exec("git", ["status"])?
    console.println(result.stdout)
    proc.exit(0)
}

// Entry-point binds ambient capability so script-level code is concise.
#[ambient(process, console)]
fn main() {
    runStatus(process, console)?
}
```

Legacy form (`--legacy-globals`, v0.6.x only):

```osty
let result = os.exec("git", ["status"])?
println(result.stdout)
os.exit(0)
```

API:

Path helpers are pure functions. Process effects (`exec`, `exit`, …) are
methods on the `Process` capability (§20.9.6); the function-style
listings below match the `--legacy-globals` desugar target.

```
// Pure path helpers — no capability required.
os.path.join(parts: List<String>) -> String
os.path.split(path: String) -> (String, String)      // (dirname, basename)
os.path.extension(path: String) -> String
os.path.dirname(path: String) -> String
os.path.basename(path: String) -> String
os.path.absolute(path: String) -> Result<String, Error>
os.path.canonical(path: String) -> Result<String, Error>
os.path.isAbsolute(path: String) -> Bool
os.path.separator() -> String                        // "/" or "\\"

// `Process` capability methods (canonical v0.6 surface).
Process.exec(self, cmd: String, args: List<String>) -> Result<Output, Error>
Process.execShell(self, command: String) -> Result<Output, Error>
Process.exit(self, code: Int) -> Never
Process.pid(self) -> Int
Process.hostname(self) -> String
Process.onSignal(self, sig: Signal, handler: fn())

pub struct Output {
    pub exitCode: Int,
    pub stdout: String,
    pub stderr: String,
}

pub enum Signal { Interrupt, Terminate, Hangup }
```

Legacy aliases (desugar to the methods above when `--legacy-globals` is
active; rejected otherwise — `E0780`):

```
os.exec(cmd, args)        ⟶  process.exec(cmd, args)
os.execShell(command)     ⟶  process.execShell(command)
os.exit(code)             ⟶  process.exit(code)
os.pid()                  ⟶  process.pid()
os.hostname()             ⟶  process.hostname()
os.onSignal(sig, handler) ⟶  process.onSignal(sig, handler)
```

#### 10.15.1 Process capability and information flow

`Process.exec(cmd, args)` is *not* a flow sink — argv-style
process invocation does not invoke a shell, so each `args[i]`
arrives at the child process as a literal argument. Tainted args
are accepted without sanitization; the kernel does not interpret
them.

`Process.execShell(cmdline)` *is* a sink (`#[requires("shell_safe")]`).
The shell *does* interpret metacharacters, so tainted strings
flowing into the cmdline must pass `std.shell.quote` first:

```osty
fn ping(proc: Process, host: #[taint("user_input")] String) -> Result<(), Error> {
    // ✅ argv-style — no shell.
    proc.exec("ping", ["-c", "1", host])

    // ❌ shell-style — needs shell_safe.
    let safe = std.shell.quote(host)
    proc.execShell("ping -c 1 {safe}")
}
```

The asymmetry between `exec` and `execShell` is what makes the
canonical recommendation "use `exec` whenever possible" testable
at the type level.

#### 10.15.2 Path helpers and information flow

The `os.path.*` helpers (`join`, `dirname`, `basename`,
`canonical`, `absolute`) are pure transformations on `String`.
They preserve flow tags element-wise and are *not* registered as
sanitizers — running `os.path.join(parts)` on tainted parts
produces a tainted joined path.

The path *sanitizer* is `std.path.normalize` / `Path.parse(...)?`
— these registered with `#[sanitizes("user_input", into =
"path_safe")]` and produce a `Path` value acceptable to `Fs.*`
sinks.
