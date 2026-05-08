### 10.25 Terminal (`std.term`)

`std.term` is the low-level terminal boundary used by text UIs and simple
terminal games.

> **v0.6 capability**: terminal I/O is a host effect — reading a
> keystroke, querying terminal size, switching to raw mode, and
> writing escape sequences all consult `stdin`/`stdout`. The v0.6
> surface routes these through a `Terminal` capability obtained from
> `Console.terminal()` (§20.9.7); the legacy module-level functions
> remain under `--legacy-globals` (v0.6.x only).

Host-backed primitives — methods on a `Terminal` capability:

```osty
Console.terminal(self) -> Result<Terminal, Error>     // §20.9.7

Terminal.isOpen(self) -> Bool
Terminal.size(self) -> Result<Size, Error>
Terminal.setRawMode(self, enabled: Bool) -> Result<(), Error>
Terminal.readKey(self) -> Result<Key, Error>
Terminal.pollKey(self, timeoutMillis: Int) -> Result<Key?, Error>
Terminal.readEvent(self) -> Result<Event, Error>
Terminal.write(self, text: String) -> Result<(), Error>
Terminal.flush(self) -> Result<(), Error>
Terminal.restore(self) -> Result<(), Error>
```

Pure ANSI helpers — capability-free:

```osty
term.clearScreenSeq() -> String
term.clearLineSeq() -> String
term.moveToSeq(Position { x, y }) -> String
term.hideCursorSeq() -> String
term.showCursorSeq() -> String
term.enterAltScreenSeq() -> String
term.exitAltScreenSeq() -> String
term.styleSeq(style: Style) -> String
term.styled(text: String, style: Style) -> String
```

Worked pattern — entering raw mode with a `defer` for guaranteed
cleanup on every exit path (including cancel, §4.12 / §8.4.3):

```osty
fn runTui(console: Console) -> Result<(), Error> {
    let term = console.terminal()?
    term.setRawMode(true)?
    defer term.restore()             // restores raw mode + cursor + alt screen

    term.write(term.enterAltScreenSeq())?
    term.flush()?

    for {
        match term.pollKey(100)? {
            Some(Key.Char('q')) -> { break },
            Some(_) | None -> {},
        }
        thread.checkCancelled()?     // §8.4.2 cooperative cancel
    }
    Ok(())
}
```

Coordinates are zero-based in the Osty API. `moveToSeq` converts to the
one-based row/column convention used by ANSI terminals.

`Terminal.restore()` is the canonical cleanup point for programs that enter
raw mode, hide the cursor, or switch to the alternate screen.

Current LLVM backend coverage includes `isTerminal`, `size`, `write`, `flush`,
`setRawMode`, `readKey`, and `pollKey`. Resize event decoding is part of the
next host-backed terminal increment.

Legacy `term.open()` / `term.readKey()` / etc. (no capability args)
desugar to `std.term.host.open()` / `std.term.host.readKey()` under
`--legacy-globals` (v0.6.x only). Outside that mode, bare-arg form
is `E0780`.

#### 10.25.1 Cursor and screen restoration

Programs that enter raw mode or switch to the alternate screen
must restore the original state on exit. The recommended pattern
is `defer term.restore()`:

```osty
fn runTui(console: Console) -> Result<(), Error> {
    let term = console.terminal()?
    term.setRawMode(true)?
    defer term.restore()              // runs on every exit path

    term.write(term.enterAltScreenSeq())?
    term.flush()?
    // ... interactive loop ...
    Ok(())
}
```

`term.restore()` is idempotent — calling it twice is safe. The
implementation tracks which modes the program changed and reverses
exactly those.

If the program crashes (uncaught panic, signal-triggered abort)
without running `defer`, the restore step is skipped — the host
shell may need manual `reset` to recover the terminal. v0.6
baseline does not provide a process-exit hook; signal handlers
that want to restore must do so explicitly (§10.15.4).
